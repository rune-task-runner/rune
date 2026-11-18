package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

// The address a listener bound decides, whatever --addr spelled (spec 045
// FR-007, FR-008).
func TestIsLoopback(t *testing.T) {
	tcp := func(ip string) net.Addr { return &net.TCPAddr{IP: net.ParseIP(ip), Port: 7777} }
	cases := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{"127.0.0.1", tcp("127.0.0.1"), true},
		{"127.0.0.2", tcp("127.0.0.2"), true},
		{"::1", tcp("::1"), true},
		{"::ffff:127.0.0.1", tcp("::ffff:127.0.0.1"), true},
		{"0.0.0.0", tcp("0.0.0.0"), false},
		{"::", tcp("::"), false},
		{"192.168.1.5", tcp("192.168.1.5"), false},
		{"2001:db8::1", tcp("2001:db8::1"), false},
		{"no IP", &net.TCPAddr{Port: 7777}, false},
		{"not TCP", &net.UnixAddr{Name: "/tmp/sock", Net: "unix"}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isLoopback(c.addr); got != c.want {
			t.Errorf("isLoopback(%s) = %v, want %v", c.name, got, c.want)
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
	return httpClient(t, addr, token, nil)
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

// cleartextText is the warning of spec 045, pinned apart from the constant
// the server uses.
const cleartextText = "serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext"

// warnLog records what HTTPConfig.Warn receives. ServeHTTP calls it from the
// goroutine the test started.
type warnLog struct {
	mu  sync.Mutex
	got []string
}

func (l *warnLog) add(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, message)
}

func (l *warnLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

// loopbackOf is the loopback address of the port in addr, to reach a server
// bound to every interface.
func loopbackOf(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// freeLoopbackAddr is a 127.0.0.1 address whose port was free a moment ago.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// waitUnauthorized returns once the server at addr answers 401 to a request
// with no token: the bind happened and the token is enforced.
func waitUnauthorized(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Post("http://"+addr, "application/json", nil)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status without a token = %d, want 401", resp.StatusCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer from %s: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Spec 045 FR-002, FR-012: a bind that is not loopback is reported once,
// before StartHTTP returns.
func TestStartHTTPWarnsOnNonLoopbackBind(t *testing.T) {
	for _, bind := range []string{"0.0.0.0:0", ":0"} {
		var warned warnLog
		addr, stop, err := New(sampleEngine(), Options{}).StartHTTP(HTTPConfig{Addr: bind, Token: "secret", Warn: warned.add})
		if err != nil {
			t.Fatalf("%s: %v", bind, err)
		}
		if got := warned.all(); !slices.Equal(got, []string{cleartextText}) {
			t.Errorf("%s: warnings = %q, want the cleartext warning once", bind, got)
		}
		// The warning does not change what the server accepts (FR-004).
		waitUnauthorized(t, loopbackOf(t, addr))
		stop()
	}
}

func TestServeHTTPWarnsOnNonLoopbackBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 4)
	done := make(chan error, 1)
	go func() {
		done <- New(sampleEngine(), Options{}).ServeHTTP(ctx, HTTPConfig{
			Addr: "0.0.0.0:0", Token: "secret", Warn: func(m string) { got <- m },
		})
	}()
	select {
	case m := <-got:
		if m != cleartextText {
			t.Errorf("warning = %q, want %q", m, cleartextText)
		}
	case err := <-done:
		t.Fatalf("ServeHTTP returned %v before it warned", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no warning for a bind to every interface")
	}
	cancel()
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ServeHTTP = %v, want http.ErrServerClosed", err)
	}
	if n := len(got); n != 0 {
		t.Errorf("%d more warnings, want one in all", n)
	}
}

// Spec 045 FR-003: the default address and an explicit loopback address are
// not reported.
func TestStartHTTPQuietOnLoopbackBind(t *testing.T) {
	for _, bind := range []string{"", "127.0.0.1:0"} {
		var warned warnLog
		addr, stop, err := New(sampleEngine(), Options{}).StartHTTP(HTTPConfig{Addr: bind, Token: "secret", Warn: warned.add})
		if err != nil {
			t.Fatalf("%q: %v", bind, err)
		}
		stop()
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Errorf("%q bound %s, want a 127.0.0.1 address", bind, addr)
		}
		if got := warned.all(); len(got) != 0 {
			t.Errorf("%q: warnings = %q, want none", bind, got)
		}
	}
}

func TestServeHTTPQuietOnLoopbackBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var warned warnLog
	addr := freeLoopbackAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- New(sampleEngine(), Options{}).ServeHTTP(ctx, HTTPConfig{Addr: addr, Token: "secret", Warn: warned.add})
	}()
	// An answer proves the bind happened, so the absence below means
	// something.
	waitUnauthorized(t, addr)
	cancel()
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ServeHTTP = %v, want http.ErrServerClosed", err)
	}
	if got := warned.all(); len(got) != 0 {
		t.Errorf("warnings = %q, want none on loopback", got)
	}
}

// With no address ServeHTTP binds loopback, like StartHTTP. The context is
// done from the start, so the server stops as soon as it has bound.
func TestServeHTTPDefaultAddrIsQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var warned warnLog
	err := New(sampleEngine(), Options{}).ServeHTTP(ctx, HTTPConfig{Token: "secret", Warn: warned.add})
	if !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ServeHTTP = %v, want http.ErrServerClosed: the default address was bound", err)
	}
	if got := warned.all(); len(got) != 0 {
		t.Errorf("warnings = %q, want none for the default address", got)
	}
}

// Spec 045 FR-002: a start that fails serves nothing, so it reports nothing.
func TestHTTPFailedStartDoesNotWarn(t *testing.T) {
	cases := []struct {
		name string
		cfg  HTTPConfig
	}{
		{"port out of range", HTTPConfig{Addr: "0.0.0.0:99999", Token: "secret"}},
		{"no token", HTTPConfig{Addr: "0.0.0.0:0"}},
	}
	for _, c := range cases {
		var warned warnLog
		c.cfg.Warn = warned.add
		srv := New(sampleEngine(), Options{})
		if _, stop, err := srv.StartHTTP(c.cfg); err == nil {
			stop()
			t.Errorf("%s: StartHTTP succeeded", c.name)
		}
		if err := srv.ServeHTTP(context.Background(), c.cfg); err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Errorf("%s: ServeHTTP = %v, want a start error", c.name, err)
		}
		if got := warned.all(); len(got) != 0 {
			t.Errorf("%s: warnings = %q, want none", c.name, got)
		}
	}
}

// Spec 045 FR-012: with no Warn the library says nothing anywhere, because an
// embedder owns the process's output.
func TestHTTPNilWarnIsSilent(t *testing.T) {
	// Must not panic for a bind that would be reported.
	HTTPConfig{}.warnCleartext(&net.TCPAddr{IP: net.IPv4zero})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The standard logger keeps the stderr it was created with, so it is
	// pointed at the pipe too.
	stdout, stderr, logOut := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w)
	addr, stop, err := New(sampleEngine(), Options{}).StartHTTP(HTTPConfig{Addr: "0.0.0.0:0", Token: "secret"})
	os.Stdout, os.Stderr = stdout, stderr
	log.SetOutput(logOut)
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(out) != 0 {
		t.Errorf("the library wrote %q to the process output", out)
	}
	waitUnauthorized(t, loopbackOf(t, addr))
}

// httpClient is httpSession with client options, so a test can answer
// approval requests over HTTP. Register the server's stop first.
func httpClient(t *testing.T, addr, token string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             "http://" + addr,
		HTTPClient:           &http.Client{Transport: bearer{token}},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, opts)
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// askServer starts a server that asks for approval over HTTP.
func askServer(t *testing.T, eng Engine, change func(*Options)) string {
	t.Helper()
	addr, stop, err := New(eng, askOptions(change)).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return addr
}

// Spec 049 FR-001: over Streamable HTTP the person is asked before anything
// runs, and the call runs once on approval, as on stdio.
func TestHTTPApprovedCall(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := httpClient(t, askServer(t, eng, nil), "secret", who.options())
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || contentText(res) != "applied\n[exit 0]" {
		t.Errorf("approved call over HTTP: isError=%v text=%q", res.IsError, contentText(res))
	}
	asked := who.asked()
	if len(asked) != 1 || asked[0].Message != messageApply {
		t.Errorf("requests over HTTP = %+v, want one with %q", asked, messageApply)
	}
	if eng.runs() != 1 {
		t.Errorf("runs = %d, want 1", eng.runs())
	}
}

// Spec 049 FR-016, FR-028: over HTTP too, a decline, no answer in time and a
// cancelled call run nothing.
func TestHTTPNotApproved(t *testing.T) {
	head := `task "apply" was not approved: `

	eng := applyEngine()
	cs := httpClient(t, askServer(t, eng, nil), "secret", declining().options())
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := head + "the request was declined. Nothing ran." + remedyApply; !res.IsError || contentText(res) != want {
		t.Errorf("decline over HTTP: isError=%v %q, want %q", res.IsError, contentText(res), want)
	}
	if eng.runs() != 0 {
		t.Error("the engine ran a declined call")
	}

	eng = applyEngine()
	slow := &person{answer: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	addr := askServer(t, eng, func(o *Options) { o.ApprovalTimeout = 50 * time.Millisecond })
	cs = httpClient(t, addr, "secret", slow.options())
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := head + "no answer arrived within 50ms. Nothing ran." + remedyApply; !res.IsError || contentText(res) != want {
		t.Errorf("timeout over HTTP: isError=%v %q, want %q", res.IsError, contentText(res), want)
	}
	if eng.runs() != 0 {
		t.Error("the engine ran a call that timed out")
	}

	eng = applyEngine()
	eng.returned = make(chan struct{}, 1)
	shown := make(chan struct{})
	// A client that cancels its own call takes its own prompt down. The
	// server's withdrawal travels on the stream the cancellation closed, so
	// the test ends the handler itself.
	release := make(chan struct{})
	waiting := &person{answer: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		close(shown)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return nil, errors.New("withdrawn")
	}}
	cs = httpClient(t, askServer(t, eng, nil), "secret", waiting.options())
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-shown
		cancel()
	}()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}}); err == nil {
		t.Error("cancelled call over HTTP returned no error")
	}
	select {
	case <-eng.returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the call was cancelled")
	}
	if eng.runs() != 0 {
		t.Error("the engine ran a cancelled call")
	}
}

// Spec 049 FR-015: a request goes only to the session that made the call.
func TestHTTPApprovalStaysOnSession(t *testing.T) {
	eng := applyEngine()
	addr := askServer(t, eng, nil)
	first, second := approving(), approving()
	sessions := []*mcp.ClientSession{
		httpClient(t, addr, "secret", first.options()),
		httpClient(t, addr, "secret", second.options()),
	}
	var wg sync.WaitGroup
	for i, env := range []string{"staging", "prod"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sessions[i].CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": env}}); err != nil {
				t.Errorf("session %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	for i, c := range []struct {
		who *person
		env string
	}{{first, "staging"}, {second, "prod"}} {
		asked := c.who.asked()
		if len(asked) != 1 || !strings.Contains(asked[0].Message, `- env: "`+c.env+`"`) {
			t.Errorf("session %d was shown %+v, want only its own request for %s", i, asked, c.env)
		}
	}
	if eng.runs() != 2 {
		t.Errorf("runs = %d, want 2", eng.runs())
	}
}

// postToken sends one request with the given Authorization header and
// returns the status.
func postToken(t *testing.T, addr, authorization string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	req.Header.Set("X-Client-Name", "asserted-client")
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Spec 052 FR-011: a request refused for its bearer token leaves a minimal
// report, with nothing the request said about itself.
func TestAuditAuthFailure(t *testing.T) {
	rec := newRecorder()
	addr, stop, err := New(buildEngine(), Options{Audit: rec}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)

	if status := postToken(t, addr, ""); status != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", status)
	}
	if status := postToken(t, addr, "Bearer wrong-token-value"); status != http.StatusUnauthorized {
		t.Fatalf("a wrong token: status %d", status)
	}
	failures := rec.authFailures()
	if len(failures) != 2 {
		t.Fatalf("%d reports, want 2", len(failures))
	}
	for _, f := range failures {
		if !strings.HasPrefix(f.Remote, "127.0.0.1:") || f.Time.IsZero() || f.Suppressed != 0 {
			t.Errorf("report %+v", f)
		}
		if text := fmt.Sprintf("%+v", f); strings.Contains(text, "wrong-token-value") || strings.Contains(text, "asserted-client") {
			t.Errorf("the report holds what the request sent: %s", text)
		}
	}

	cs := httpSession(t, addr, "secret")
	callTool(t, cs, "build", nil)
	call, _ := rec.only(t)
	if call.Session.Transport != "http" || call.Session.Auth != "bearer-token" || !isHexID(call.Session.ID) {
		t.Errorf("session %+v", call.Session)
	}
	if got := rec.authFailures(); len(got) != 2 {
		t.Errorf("a request with the right token was reported: %d reports", len(got))
	}
}

func TestAuditAuthFailureLimit(t *testing.T) {
	rec := newRecorder()
	srv := New(buildEngine(), Options{Audit: rec})
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	srv.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	addr, stop, err := srv.StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	for range 30 {
		postToken(t, addr, "Bearer nope")
	}
	if got := len(rec.authFailures()); got != maxAuthFailuresPerMinute {
		t.Fatalf("%d reports in one minute, want %d", got, maxAuthFailuresPerMinute)
	}
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	postToken(t, addr, "Bearer nope")
	failures := rec.authFailures()
	if len(failures) != 11 || failures[10].Suppressed != 20 {
		t.Fatalf("%d reports, the last left out %d, want 11 and 20", len(failures), failures[len(failures)-1].Suppressed)
	}
}

func TestAuthFailureWithoutAuditor(t *testing.T) {
	addr, stop, err := New(buildEngine(), Options{}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if status := postToken(t, addr, "Bearer nope"); status != http.StatusUnauthorized {
		t.Fatalf("status %d", status)
	}
}
