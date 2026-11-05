package mcpserver

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStartHTTPRequiresToken(t *testing.T) {
	srv := New(sampleEngine(), Options{})
	if _, _, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0"}); err == nil {
		t.Error("HTTP transport must require a token")
	}
}

func TestHTTPRejectsMissingToken(t *testing.T) {
	srv := New(sampleEngine(), Options{})
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// No Authorization header -> rejected (SC-010).
	req, _ := http.NewRequest("POST", "http://"+addr, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", resp.StatusCode)
	}

	// Wrong token -> rejected.
	req2, _ := http.NewRequest("POST", "http://"+addr, nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 with a wrong token", resp2.StatusCode)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7777": true,
		"localhost:80":   true,
		"[::1]:7777":     true,
		"0.0.0.0:7777":   false,
		"192.168.1.5:80": false,
	}
	for addr, want := range cases {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// syncEngine records calls under a lock: over HTTP the handler runs on the
// server's goroutines, which the test reads from.
type syncEngine struct {
	tasks []TaskInfo
	mu    sync.Mutex
	calls []string
}

func (e *syncEngine) Tasks() []TaskInfo { return e.tasks }

func (e *syncEngine) Call(_ context.Context, name string, _ map[string]string) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, name)
	return Result{Stdout: "ran " + name}, nil
}

func (e *syncEngine) called() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

// bearer adds the token to every request the MCP client sends.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// Spec 031 FR-013: the grant applies over Streamable HTTP as on stdio, to
// every session, and a call does not use it up.
func TestHTTPGrantedDestructiveCall(t *testing.T) {
	eng := &syncEngine{tasks: []TaskInfo{
		{Name: "clean", Destructive: true},
		{Name: "reset", Destructive: true},
	}}
	srv := New(eng, Options{AllowDestructive: []string{"clean"}, AllowDestructiveFlag: "--allow-destructive"})
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	// Cleanups run last-in first-out: the sessions registered below close
	// before the server stops, so no server-side session goroutine is left.
	t.Cleanup(stop)

	ctx := context.Background()
	session := func() *mcp.ClientSession {
		transport := &mcp.StreamableClientTransport{
			Endpoint:             "http://" + addr,
			HTTPClient:           &http.Client{Transport: bearer{"secret"}},
			DisableStandaloneSSE: true,
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
		cs, err := client.Connect(ctx, transport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		return cs
	}
	first, second := session(), session()

	for _, cs := range []*mcp.ClientSession{first, second} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "clean"})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Errorf("granted clean refused over HTTP: %q", contentText(res))
		}
	}
	res, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "reset"})
	if err != nil {
		t.Fatal(err)
	}
	want := `task "reset" is destructive; to permit it, add reset to --allow-destructive and restart the server`
	if !res.IsError || contentText(res) != want {
		t.Errorf("reset over HTTP: isError=%v text=%q, want %q", res.IsError, contentText(res), want)
	}
	if got := eng.called(); !slices.Equal(got, []string{"clean", "clean"}) {
		t.Errorf("engine calls = %q, want clean from both sessions and no reset", got)
	}
}
