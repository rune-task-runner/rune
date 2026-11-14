package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 043: a running `rune mcp` follows its Runefile. These tests drive the
// real binary with a client that stays connected while the files change.

const reloadRunefile = "# Builds.\nbuild:\n    @echo built\n"

const reloadLint = "\n# Lints.\nlint:\n    @echo linted\n"

// reloadTimeout bounds a wait that is not about timing. The test of the
// 2-second requirement uses its own deadline.
const reloadTimeout = 15 * time.Second

// syncBuffer is the server's stderr, written by the process copier while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mcpFrame is one JSON-RPC message from the server.
type mcpFrame struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mcpProc is a `rune mcp` process with an open stdio session.
type mcpProc struct {
	t      *testing.T
	dir    string
	stdin  io.WriteCloser
	frames chan mcpFrame
	stderr *syncBuffer
	// notes are the notifications read while waiting for a reply.
	notes  []string
	nextID int
	// init is the result of initialize.
	init json.RawMessage

	mu  sync.Mutex
	bad []string // stdout lines that are not JSON-RPC messages
}

// startMCP starts `rune mcp flags...` in dir and completes the handshake. The
// process is stopped by closing its stdin when the test ends, and must then
// exit with status 0.
func startMCP(t *testing.T, dir string, flags ...string) *mcpProc {
	t.Helper()
	return startMCPVersion(t, dir, "2024-11-05", flags...)
}

// startMCPVersion is startMCP with the protocol version the client asks for.
func startMCPVersion(t *testing.T, dir, version string, flags ...string) *mcpProc {
	t.Helper()
	cmd := exec.Command(runeBin, append([]string{"mcp"}, flags...)...)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &mcpProc{t: t, dir: dir, stdin: stdin, frames: make(chan mcpFrame, 256), stderr: &syncBuffer{}, nextID: 1}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer close(p.frames)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			var f mcpFrame
			if err := json.Unmarshal(sc.Bytes(), &f); err != nil || (f.ID == nil && f.Method == "") {
				p.mu.Lock()
				p.bad = append(p.bad, sc.Text())
				p.mu.Unlock()
				continue
			}
			p.frames <- f
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		watchdog := time.AfterFunc(reloadTimeout, func() { _ = cmd.Process.Kill() })
		defer watchdog.Stop()
		<-read
		if err := cmd.Wait(); err != nil {
			t.Errorf("rune mcp did not exit cleanly when its stdin closed: %v\nstderr:\n%s", err, p.stderr.String())
		}
	})

	p.init = p.request("initialize", fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"it","version":"0.0.0"}}`, version))
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return p
}

func (p *mcpProc) send(frame string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, frame+"\n"); err != nil {
		p.t.Fatalf("writing %s: %v", frame, err)
	}
}

// next reads one frame, failing the test at the deadline.
func (p *mcpProc) next(deadline <-chan time.Time, waitingFor string) mcpFrame {
	p.t.Helper()
	select {
	case f, ok := <-p.frames:
		if !ok {
			p.t.Fatalf("the server closed stdout while waiting for %s\nstderr:\n%s", waitingFor, p.stderr.String())
		}
		return f
	case <-deadline:
		p.t.Fatalf("timed out waiting for %s\nstderr:\n%s", waitingFor, p.stderr.String())
	}
	return mcpFrame{}
}

// reply sends a request and returns the frame that answers it. Notifications
// that arrive first are kept for waitNotification.
func (p *mcpProc) reply(method, params string) mcpFrame {
	p.t.Helper()
	id := p.nextID
	p.nextID++
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
	deadline := time.After(reloadTimeout)
	for {
		f := p.next(deadline, "the reply to "+method)
		if f.ID != nil && *f.ID == id {
			return f
		}
		if f.Method != "" {
			p.notes = append(p.notes, f.Method)
		}
	}
}

// request is reply for a call that must succeed.
func (p *mcpProc) request(method, params string) json.RawMessage {
	p.t.Helper()
	f := p.reply(method, params)
	if f.Error != nil {
		p.t.Fatalf("%s failed: %d %s", method, f.Error.Code, f.Error.Message)
	}
	return f.Result
}

// waitNotification waits for a notification of method.
func (p *mcpProc) waitNotification(method string, within time.Duration) {
	p.t.Helper()
	if i := slices.Index(p.notes, method); i >= 0 {
		p.notes = slices.Delete(p.notes, i, i+1)
		return
	}
	deadline := time.After(within)
	for {
		if f := p.next(deadline, method+" within "+within.String()); f.Method == method {
			return
		} else if f.Method != "" {
			p.notes = append(p.notes, f.Method)
		}
	}
}

// notified reports whether a notification of method has arrived. A request
// round trip first makes sure everything the server sent before is read.
func (p *mcpProc) notified(method string) bool {
	p.t.Helper()
	p.tools()
	return slices.Contains(p.notes, method)
}

func (p *mcpProc) tools() []string {
	p.t.Helper()
	var res struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(p.request("tools/list", `{}`), &res); err != nil {
		p.t.Fatal(err)
	}
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	slices.Sort(names)
	return names
}

func (p *mcpProc) wantTools(want ...string) {
	p.t.Helper()
	if got := p.tools(); !slices.Equal(got, want) {
		p.t.Errorf("tools = %v, want %v\nstderr:\n%s", got, want, p.stderr.String())
	}
}

// call runs a tool and returns its text.
func (p *mcpProc) call(tool string) string {
	p.t.Helper()
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(p.request("tools/call", fmt.Sprintf(`{"name":%q,"arguments":{}}`, tool)), &res); err != nil || len(res.Content) == 0 {
		p.t.Fatalf("call %s: %v, %+v", tool, err, res)
	}
	return res.Content[0].Text
}

// waitStderr waits until stderr holds text n times and returns stderr.
func (p *mcpProc) waitStderr(text string, n int) string {
	p.t.Helper()
	return waitForText(p.t, p.stderr, text, n)
}

func waitForText(t *testing.T, stderr *syncBuffer, text string, n int) string {
	t.Helper()
	deadline := time.Now().Add(reloadTimeout)
	for {
		got := stderr.String()
		if strings.Count(got, text) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr does not hold %q %d time(s):\n%s", text, n, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *mcpProc) write(name, content string) {
	p.t.Helper()
	path := filepath.Join(p.dir, filepath.FromSlash(name))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// The acceptance test of issue #41, which names it
// TestMCPE2E_Pending_RunefileReload: adding a task notifies the client within
// 2 seconds, and the task is listed and runs (spec 043 FR-037).
func TestReloadMCP_AddedTaskIsListed(t *testing.T) {
	p := startMCP(t, writeRunefile(t, reloadRunefile))
	p.wantTools("build")

	p.write("Runefile", reloadRunefile+reloadLint)
	p.waitNotification("notifications/tools/list_changed", 2*time.Second)
	p.wantTools("build", "lint")
	if got := p.call("lint"); got != "linted\n[exit 0]" {
		t.Errorf("call lint = %q", got)
	}
	got := p.waitStderr("reloaded Runefile:", 1)
	if want := "rune MCP server on stdio\nreloaded Runefile: 1 tool added, 0 removed, 0 changed\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestReloadMCP_RemovedTaskIsUnknown(t *testing.T) {
	p := startMCP(t, writeRunefile(t, reloadRunefile+reloadLint))
	p.write("Runefile", reloadRunefile)
	p.waitNotification("notifications/tools/list_changed", reloadTimeout)
	p.wantTools("build")

	gone := p.reply("tools/call", `{"name":"lint","arguments":{}}`)
	never := p.reply("tools/call", `{"name":"nope","arguments":{}}`)
	if gone.Error == nil || never.Error == nil {
		t.Fatalf("replies = %+v, %+v, want two errors", gone, never)
	}
	if want := strings.ReplaceAll(never.Error.Message, "nope", "lint"); gone.Error.Code != never.Error.Code || gone.Error.Message != want {
		t.Errorf("removed tool: %d %q, want the unknown-name answer %d %q", gone.Error.Code, gone.Error.Message, never.Error.Code, want)
	}
}

// A server that starts with nothing to expose still says its tool list can
// change, and then gains its first tool.
func TestReloadMCP_InitializeAdvertisesListChanged(t *testing.T) {
	p := startMCP(t, writeRunefile(t, "_setup:\n    @echo setup\n"))
	var init struct {
		Capabilities struct {
			Tools *struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(p.init, &init); err != nil {
		t.Fatal(err)
	}
	if init.Capabilities.Tools == nil || !init.Capabilities.Tools.ListChanged {
		t.Errorf("initialize result %s does not state tools.listChanged", p.init)
	}
	p.wantTools()

	p.write("Runefile", "_setup:\n    @echo setup\n"+reloadLint)
	p.waitNotification("notifications/tools/list_changed", reloadTimeout)
	p.wantTools("lint")
}

func TestReloadMCP_StaticErrorKeepsTools(t *testing.T) {
	p := startMCP(t, writeRunefile(t, reloadRunefile))
	p.write("Runefile", "# Builds.\nbuild: nope\n    @echo broken\n")
	got := p.waitStderr("reload failed:", 1)
	for _, want := range []string{"Runefile:2:", "^", "reload failed: 1 static error(s); keeping the previous tools\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if p.notified("notifications/tools/list_changed") {
		t.Error("a failed reload notified the client")
	}
	p.wantTools("build")
	if got := p.call("build"); got != "built\n[exit 0]" {
		t.Errorf("call build = %q, want the task as it was", got)
	}

	p.write("Runefile", reloadRunefile+reloadLint)
	p.waitNotification("notifications/tools/list_changed", reloadTimeout)
	p.wantTools("build", "lint")

	// Stdout carried only protocol messages through both reloads (FR-025).
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bad) > 0 {
		t.Errorf("stdout lines that are not JSON-RPC: %q", p.bad)
	}
}

func TestReloadMCP_ImportAndModule(t *testing.T) {
	dir := writeRunefile(t, "import \"ci.rune\"\nmod deploy\n\n"+reloadRunefile)
	ci := "# Tests.\ntest:\n    @echo tested\n"
	deploy := "# Ships.\nship:\n    @echo shipped\n"
	for name, content := range map[string]string{"ci.rune": ci, "deploy.rune": deploy} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := startMCP(t, dir)
	p.wantTools("build", "deploy__ship", "test")

	p.write("ci.rune", ci+reloadLint)
	p.waitNotification("notifications/tools/list_changed", 2*time.Second)
	p.wantTools("build", "deploy__ship", "lint", "test")

	p.write("deploy.rune", deploy+"\n# Pushes.\npush:\n    @echo pushed\n")
	p.waitNotification("notifications/tools/list_changed", 2*time.Second)
	p.wantTools("build", "deploy__push", "deploy__ship", "lint", "test")
	if got := p.call("deploy__push"); got != "pushed\n[exit 0]" {
		t.Errorf("call deploy__push = %q", got)
	}
}

// An editor that writes a new file and renames it over the Runefile.
func TestReloadMCP_RenameOverSave(t *testing.T) {
	p := startMCP(t, writeRunefile(t, reloadRunefile))
	content := reloadRunefile
	for i := 1; i <= 2; i++ {
		content += fmt.Sprintf("\n# Extra.\nextra%d:\n    @echo e\n", i)
		p.write("Runefile.tmp", content)
		if err := os.Rename(filepath.Join(p.dir, "Runefile.tmp"), filepath.Join(p.dir, "Runefile")); err != nil {
			t.Fatal(err)
		}
		p.waitNotification("notifications/tools/list_changed", reloadTimeout)
	}
	p.wantTools("build", "extra1", "extra2")
}

func TestReloadMCP_NoReload(t *testing.T) {
	p := startMCP(t, writeRunefile(t, reloadRunefile), "--no-reload")
	p.write("Runefile", reloadRunefile+reloadLint)
	// Longer than the debounce and a load take together.
	time.Sleep(time.Second)
	if p.notified("notifications/tools/list_changed") {
		t.Error("--no-reload: the client was notified")
	}
	p.wantTools("build")
	if got := p.stderr.String(); got != "rune MCP server on stdio\n" {
		t.Errorf("--no-reload: stderr = %q, want only the startup line", got)
	}

	// Without tools, the capabilities are those of earlier releases.
	empty := startMCP(t, writeRunefile(t, "_setup:\n    @echo setup\n"), "--no-reload")
	if strings.Contains(string(empty.init), `"tools"`) {
		t.Errorf("--no-reload with no tool: initialize result %s states a tools capability", empty.init)
	}
}

// reloadBearer adds the token to every request the MCP client sends.
type reloadBearer struct{ token string }

func (b reloadBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// On HTTP, a session that starts after a reload gets the [context] output of
// the reloaded Runefile (spec 043 FR-026).
func TestReloadMCP_HTTPInstructions(t *testing.T) {
	hook := func(text string) string {
		return reloadRunefile + "\n# Health.\n[context]\nhealth:\n    @echo " + text + "\n"
	}
	dir := writeRunefile(t, hook("v1"))
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("it-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, runeBin, "serve", "--http", "--addr", addr, "--token-file", tokenPath)
	cmd.Dir = dir
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	})
	waitForText(t, stderr, "rune MCP server on http://", 1)

	instructions := func() string {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, nil)
		transport := &mcp.StreamableClientTransport{
			Endpoint:   "http://" + addr,
			HTTPClient: &http.Client{Transport: reloadBearer{"it-token"}},
		}
		var cs *mcp.ClientSession
		var err error
		// The startup line is printed just before the listener is bound.
		for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(50 * time.Millisecond) {
			if cs, err = client.Connect(ctx, transport, nil); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatalf("connecting: %v\nstderr:\n%s", err, stderr.String())
		}
		defer func() { _ = cs.Close() }()
		return cs.InitializeResult().Instructions
	}

	if got := instructions(); got != "v1" {
		t.Fatalf("first session instructions = %q, want v1", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(hook("v2")), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForText(t, stderr, "reloaded Runefile: 0 tools added, 0 removed, 0 changed", 1)
	if got := instructions(); got != "v2" {
		t.Errorf("session after the reload: instructions = %q, want v2", got)
	}
}
