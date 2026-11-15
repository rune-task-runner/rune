package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 045: `rune serve --http` has no TLS. A bind beyond loopback sends the
// bearer token in cleartext, and the server says so once on stderr.

const bindWarningRunefile = "# Says hello.\nhello:\n    @echo hello\n"

const bindWarningLine = "warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext\n"

const bindWarningToken = "it-token"

// freePort is a port that was free on host a moment ago. The probe binds the
// host the server will bind, so a port taken on another interface is not
// handed out for a bind to every interface.
func freePort(t *testing.T, host string) string {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// writeToken writes the token file of a server in dir and returns its path.
func writeToken(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "token")
	if err := os.WriteFile(p, []byte(bindWarningToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// startServeHTTP starts `rune serve --http --addr addr` on a one-task
// Runefile. The process is killed when the test ends, or by stop.
func startServeHTTP(t *testing.T, addr string) (stdout, stderr *syncBuffer, stop func()) {
	t.Helper()
	dir := writeRunefile(t, bindWarningRunefile)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, runeBin, "serve", "--http", "--addr", addr, "--token-file", writeToken(t, dir))
	cmd.Dir = dir
	stdout, stderr = &syncBuffer{}, &syncBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		_ = cmd.Wait()
	}
	t.Cleanup(func() {
		stop()
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	})
	return stdout, stderr, stop
}

// waitUnauthorized returns once the server at addr answers 401 to a request
// with no token: the address is bound and the token is enforced.
func waitUnauthorized(t *testing.T, addr string, stderr *syncBuffer) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(20 * time.Millisecond) {
		resp, err := client.Post("http://"+addr, "application/json", nil)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status without a token = %d, want 401", resp.StatusCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer from %s: %v\nstderr:\n%s", addr, err, stderr.String())
		}
	}
}

func TestBindWarning_NonLoopback(t *testing.T) {
	for _, host := range []string{"0.0.0.0", ""} {
		t.Run("host="+host, func(t *testing.T) {
			port := freePort(t, host)
			stdout, stderr, stop := startServeHTTP(t, net.JoinHostPort(host, port))
			waitForText(t, stderr, bindWarningLine, 1)

			// The warning changes nothing about what the server accepts.
			local := net.JoinHostPort("127.0.0.1", port)
			waitUnauthorized(t, local, stderr)
			client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, nil)
			cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
				Endpoint:   "http://" + local,
				HTTPClient: &http.Client{Transport: reloadBearer{bindWarningToken}},
			}, nil)
			if err != nil {
				t.Fatalf("connecting with the token: %v\nstderr:\n%s", err, stderr.String())
			}
			list, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != 1 || list.Tools[0].Name != "hello" {
				t.Errorf("tools = %v, want hello", list.Tools)
			}
			// The warning is for the operator, not for the client.
			sent, err := json.Marshal([]any{cs.InitializeResult(), list})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(sent), "cleartext") {
				t.Errorf("the warning reached the client: %s", sent)
			}
			_ = cs.Close()

			stop()
			if got := stderr.String(); strings.Count(got, "cleartext") != 1 {
				t.Errorf("stderr holds the warning %d times, want once:\n%s", strings.Count(got, "cleartext"), got)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout = %q, want nothing", got)
			}
		})
	}
}

func TestBindWarning_LoopbackIsQuiet(t *testing.T) {
	addr := net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))
	_, stderr, stop := startServeHTTP(t, addr)
	// An answer proves the bind happened, so the absence below means
	// something.
	waitUnauthorized(t, addr, stderr)
	stop()
	got := stderr.String()
	if !strings.Contains(got, "rune MCP server on http://"+addr) {
		t.Errorf("stderr lacks the startup line:\n%s", got)
	}
	if strings.Contains(got, "cleartext") {
		t.Errorf("a loopback server printed the cleartext warning:\n%s", got)
	}
}

// A bind that fails serves nothing, so there is nothing to warn about.
func TestBindWarning_FailedBindIsQuiet(t *testing.T) {
	// Held on the network the server listens on, so the address it is given
	// is the one already bound and the second bind fails on every OS.
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()

	dir := writeRunefile(t, bindWarningRunefile)
	r := runWithStdin(t, dir, "", "serve", "--http", "--addr", held.Addr().String(), "--token-file", writeToken(t, dir))
	if r.code == 0 {
		t.Errorf("exit = 0 on a taken port; stderr=%s", r.stderr)
	}
	if strings.Contains(r.stderr, "cleartext") {
		t.Errorf("a failed bind printed the cleartext warning:\n%s", r.stderr)
	}
}

func TestBindWarning_NoTokenIsQuiet(t *testing.T) {
	dir := writeRunefile(t, bindWarningRunefile)
	r := runWithStdin(t, dir, "", "serve", "--http", "--addr", "0.0.0.0:0")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2 (missing token); stderr=%s", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "cleartext") {
		t.Errorf("a start without a token printed the cleartext warning:\n%s", r.stderr)
	}
}

// The endpoint an agent task opens reports its bind the same way, so a quiet
// stderr shows that it bound loopback (FR-013).
func TestBindWarning_AgentTaskIsQuiet(t *testing.T) {
	dir := writeRunefile(t, contextRunefile)
	r := run(t, dir, nil, "triage")
	if r.code != 0 {
		t.Fatalf("exit = %d; stderr=%s", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "cleartext") || strings.Contains(r.stdout, "cleartext") {
		t.Errorf("an agent task printed the cleartext warning: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
}

func TestBindWarning_StdioIsQuiet(t *testing.T) {
	p := startMCP(t, writeRunefile(t, bindWarningRunefile))
	p.wantTools("hello")
	if got := p.stderr.String(); strings.Contains(got, "cleartext") {
		t.Errorf("a stdio server printed the cleartext warning:\n%s", got)
	}
}
