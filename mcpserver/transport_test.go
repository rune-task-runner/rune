package mcpserver

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
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
	first, second := httpSession(t, addr, "secret"), httpSession(t, addr, "secret")

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

// Spec 034 (quickstart T): over Streamable HTTP, a call that can reach an
// ungranted destructive task is refused as on stdio, and the engine never runs.
func TestHTTPReachRefused(t *testing.T) {
	eng := &syncEngine{tasks: []TaskInfo{
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "clean", Destructive: true},
	}}
	srv := New(eng, Options{AllowDestructiveFlag: "--allow-destructive"})
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	res, err := httpSession(t, addr, "secret").CallTool(context.Background(), &mcp.CallToolParams{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	want := `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
	if !res.IsError || contentText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), want)
	}
	if got := eng.called(); len(got) != 0 {
		t.Errorf("engine calls = %q, want none", got)
	}
}

// httpSession connects a client to an HTTP server with the bearer token. It
// registers the session's close as a cleanup, so register the server's stop
// first: cleanups run last-in first-out, and no session goroutine is left.
func httpSession(t *testing.T, addr, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             "http://" + addr,
		HTTPClient:           &http.Client{Transport: bearer{token}},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Spec 032 FR-010: the refusal holds over Streamable HTTP as on stdio.
func TestHTTPUnknownArgumentRefused(t *testing.T) {
	eng := &syncEngine{tasks: []TaskInfo{{Name: "build", Params: []ParamInfo{{Name: "target"}}}}}
	addr, stop, err := New(eng, Options{}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	cs := httpSession(t, addr, "secret")

	res := callTool(t, cs, "build", map[string]any{"targte": "release"})
	const want = `unknown argument "targte" for task "build" (did you mean "target"?)`
	if !res.IsError || contentText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), want)
	}
	if got := eng.called(); len(got) != 0 {
		t.Errorf("engine calls = %q, want none", got)
	}

	if res := callTool(t, cs, "build", map[string]any{"target": "release"}); res.IsError {
		t.Errorf("declared call refused: %q", contentText(res))
	}
	if got := eng.called(); !slices.Equal(got, []string{"build"}) {
		t.Errorf("engine calls = %q, want build", got)
	}
}

// Spec 032 FR-010: HTTP lists the same schemas as a directly connected client.
// Matching alone would pass with the keyword missing from both, so it is
// checked too.
func TestHTTPToolsListMatchesStdio(t *testing.T) {
	tasks := sampleEngine().tasks
	addr, stop, err := New(&syncEngine{tasks: tasks}, Options{}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ctx := context.Background()

	overHTTP, err := httpSession(t, addr, "secret").ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := connect(t, New(&syncEngine{tasks: tasks}, Options{})).ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(overHTTP.Tools, direct.Tools) {
		t.Errorf("tools differ:\nHTTP:   %+v\ndirect: %+v", overHTTP.Tools, direct.Tools)
	}
	if len(overHTTP.Tools) != len(tasks) {
		t.Fatalf("listed %d tools, want %d", len(overHTTP.Tools), len(tasks))
	}
	for _, tl := range overHTTP.Tools {
		if schema, ok := tl.InputSchema.(map[string]any); !ok || schema["additionalProperties"] != false {
			t.Errorf("%s: InputSchema = %v, want additionalProperties false", tl.Name, tl.InputSchema)
		}
	}
}

// Spec 033 FR-011 (quickstart AA): hints over HTTP are the ones a direct
// client sees, for each descriptor field that sets one.
func TestHTTPToolsListCarriesHints(t *testing.T) {
	tasks := []TaskInfo{
		{Name: "status", ReadOnly: true},
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "mirror", Network: true},
	}
	want := map[string]hintSet{
		"status":  {readOnly: true},
		"release": {destructive: true},
		"mirror":  {openWorld: true},
	}
	addr, stop, err := New(&syncEngine{tasks: tasks}, Options{}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	overHTTP, err := httpSession(t, addr, "secret").ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	direct := listedTools(t, &syncEngine{tasks: tasks}, Options{})
	if !reflect.DeepEqual(overHTTP.Tools, direct) {
		t.Errorf("tools differ:\nHTTP:   %+v\ndirect: %+v", overHTTP.Tools, direct)
	}
	if len(overHTTP.Tools) != len(want) {
		t.Fatalf("listed %d tools, want %d", len(overHTTP.Tools), len(want))
	}
	for _, tl := range overHTTP.Tools {
		if got := hintsOf(t, tl); got != want[tl.Name] {
			t.Errorf("%s: hints %+v, want %+v", tl.Name, got, want[tl.Name])
		}
	}
}

// Spec 041 FR-015: the filters apply over Streamable HTTP as on stdio.
func TestHTTPAllowListFiltersList(t *testing.T) {
	srv := New(&syncEngine{tasks: sampleEngine().tasks}, Options{AllowList: []string{"logs", "greet"}, DenyList: []string{"greet"}})
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	res, err := httpSession(t, addr, "secret").ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "logs" {
		t.Errorf("tools over HTTP = %+v, want only logs", res.Tools)
	}
}

// TestHTTPResultIsCapped: the output limit holds on HTTP as on stdio (spec
// 042 FR-018).
func TestHTTPResultIsCapped(t *testing.T) {
	eng := sampleEngine()
	eng.result = Result{Stdout: string(numbered(10_000))}
	srv := New(eng, Options{OutputLimit: 1024})
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	text := contentText(callTool(t, httpSession(t, addr, "tok"), "logs", nil))
	if len(text) > 1056 {
		t.Errorf("result is %d bytes, want at most 1056", len(text))
	}
	if !strings.Contains(text, "\n[truncated ") || !strings.HasSuffix(text, "[exit 0]") {
		t.Errorf("result has no marker or exit line: %q", text)
	}
}
