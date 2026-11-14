package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectNotified is connect with a client that signals changed each time
// the server says its tool list changed.
func connectNotified(t *testing.T, srv *Server) (cs *mcp.ClientSession, changed chan struct{}) {
	t.Helper()
	changed = make(chan struct{}, 16)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	})
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, changed
}

func waitChanged(t *testing.T, changed <-chan struct{}) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("no tool-list-changed notification within 2s")
	}
}

func listNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	slices.Sort(names)
	return names
}

func callText(t *testing.T, cs *mcp.ClientSession, name string) (string, error) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name})
	if err != nil {
		return "", err
	}
	return res.Content[0].(*mcp.TextContent).Text, nil
}

// withTask is sampleEngine plus one task, answering with stdout.
func withTask(stdout string, extra ...TaskInfo) *fakeEngine {
	eng := sampleEngine()
	eng.tasks = append(eng.tasks, extra...)
	eng.result = Result{Stdout: stdout}
	return eng
}

func TestReplaceAddsTool(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	cs, changed := connectNotified(t, srv)

	next := withTask("from-next", TaskInfo{Name: "lint", Doc: "Lints."})
	if got := srv.Replace(next, 0); got != (Change{Added: 1}) {
		t.Errorf("Replace = %+v, want one added", got)
	}
	waitChanged(t, changed)
	if names := listNames(t, cs); !slices.Contains(names, "lint") {
		t.Errorf("tools = %v, want lint listed", names)
	}
	text, err := callText(t, cs, "lint")
	if err != nil || !strings.HasPrefix(text, "from-next") {
		t.Errorf("call lint = %q, %v, want the new engine's output", text, err)
	}
	if next.lastCall != "lint" {
		t.Errorf("new engine saw call %q, want lint", next.lastCall)
	}
}

func TestReplaceRemovesTool(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	cs, changed := connectNotified(t, srv)

	next := sampleEngine()
	next.tasks = next.tasks[1:] // drops logs
	if got := srv.Replace(next, 0); got != (Change{Removed: 1}) {
		t.Errorf("Replace = %+v, want one removed", got)
	}
	waitChanged(t, changed)
	if names := listNames(t, cs); slices.Contains(names, "logs") {
		t.Errorf("tools = %v, want logs gone", names)
	}
	_, gone := callText(t, cs, "logs")
	_, never := callText(t, cs, "nope")
	if gone == nil || never == nil {
		t.Fatalf("errors = %v, %v, want both calls refused", gone, never)
	}
	if got, want := gone.Error(), strings.ReplaceAll(never.Error(), "nope", "logs"); got != want {
		t.Errorf("removed tool error = %q, want the unknown-name error %q", got, want)
	}
}

// A call that reaches a tool's handler after a Replace removed its task is
// answered as the SDK answers a name it does not know, and runs nothing
// (spec 043 FR-014).
func TestHandlerRemovedTool(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	cs, _ := connectNotified(t, srv)
	// The handler the SDK found before the Replace took the tool away.
	found := srv.handler("logs")
	next := sampleEngine()
	next.tasks = next.tasks[1:] // drops logs
	srv.Replace(next, 0)

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "logs"}}
	res, err := found(context.Background(), req)
	var rpcErr *jsonrpc.Error
	if res != nil || !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("removed tool = %+v, %v, want an invalid-params error", res, err)
	}
	_, never := callText(t, cs, "nope")
	if never == nil || !strings.Contains(never.Error(), strings.ReplaceAll(rpcErr.Message, "logs", "nope")) {
		t.Errorf("removed tool error = %q, want the text of the unknown-name error %v", rpcErr.Message, never)
	}
	if next.lastCall != "" {
		t.Errorf("the new engine ran %q for a removed tool", next.lastCall)
	}
}

func TestReplaceChangesTool(t *testing.T) {
	cases := map[string]func(*TaskInfo){
		"doc":      func(ti *TaskInfo) { ti.Doc = "Show the log." },
		"params":   func(ti *TaskInfo) { ti.Params = []ParamInfo{{Name: "n"}} },
		"readonly": func(ti *TaskInfo) { ti.ReadOnly = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			srv := New(sampleEngine(), Options{Reloadable: true})
			cs, changed := connectNotified(t, srv)
			next := sampleEngine()
			mutate(&next.tasks[0])
			if got := srv.Replace(next, 0); got != (Change{Changed: 1}) {
				t.Errorf("Replace = %+v, want one changed", got)
			}
			waitChanged(t, changed)
			res, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(toolFor(next.tasks[0]))
			for _, tool := range res.Tools {
				if tool.Name != "logs" {
					continue
				}
				// Compare what the client holds with the new descriptor on
				// the fields a client sees.
				if tool.Description != next.tasks[0].Doc || tool.Annotations.ReadOnlyHint != next.tasks[0].ReadOnly {
					t.Errorf("listed logs = %+v, want %s", tool, want)
				}
				props := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
				if len(props) != len(next.tasks[0].Params) {
					t.Errorf("listed logs properties = %v, want %d", props, len(next.tasks[0].Params))
				}
			}
		})
	}
}

// A reload that leaves every advertised tool as it was tells the client
// nothing, and later calls still reach the new engine: a body-only edit
// (spec 043 FR-017).
func TestReplaceSameToolsNoNotification(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	cs, changed := connectNotified(t, srv)

	same := withTask("new-body")
	if got := srv.Replace(same, 0); got != (Change{}) {
		t.Errorf("Replace = %+v, want no change", got)
	}
	if text, err := callText(t, cs, "logs"); err != nil || !strings.HasPrefix(text, "new-body") {
		t.Errorf("call logs = %q, %v, want the new engine's output", text, err)
	}

	srv.Replace(withTask("x", TaskInfo{Name: "lint"}), 0)
	waitChanged(t, changed)
	if names := listNames(t, cs); !slices.Contains(names, "lint") {
		t.Fatalf("tools = %v, want lint listed", names)
	}
	// The list round trip above is later than any notification the first
	// Replace could have caused.
	select {
	case <-changed:
		t.Error("a second notification arrived: the unchanged Replace notified")
	default:
	}
}

func TestReplaceNotifiesEverySession(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	_, first := connectNotified(t, srv)
	_, second := connectNotified(t, srv)
	srv.Replace(withTask("x", TaskInfo{Name: "lint"}), 0)
	waitChanged(t, first)
	waitChanged(t, second)
}

func TestReplaceAppliesFiltersAndGrants(t *testing.T) {
	srv := New(sampleEngine(), Options{
		Reloadable:       true,
		AllowList:        []string{"logs", "wipe", "reset", "hidden"},
		DenyList:         []string{"hidden"},
		AllowDestructive: []string{"wipe"},
	})
	cs, _ := connectNotified(t, srv)
	if got := listNames(t, cs); !slices.Equal(got, []string{"logs"}) {
		t.Fatalf("tools before = %v, want [logs]", got)
	}

	next := withTask("ran",
		TaskInfo{Name: "wipe", Destructive: true},
		TaskInfo{Name: "reset", Destructive: true},
		TaskInfo{Name: "hidden"},
	)
	srv.Replace(next, 0)
	if got := listNames(t, cs); !slices.Equal(got, []string{"logs", "reset", "wipe"}) {
		t.Errorf("tools after = %v, want [logs reset wipe]", got)
	}
	if exposed, exposable := srv.Exposure(); exposed != 3 || exposable != len(next.tasks) {
		t.Errorf("Exposure() = %d, %d, want 3, %d", exposed, exposable, len(next.tasks))
	}
	if text, err := callText(t, cs, "wipe"); err != nil || !strings.HasPrefix(text, "ran") {
		t.Errorf("granted wipe = %q, %v, want it to run", text, err)
	}
	if text, err := callText(t, cs, "reset"); err != nil || !strings.Contains(text, "destructive") {
		t.Errorf("ungranted reset = %q, %v, want a refusal", text, err)
	}
}

// blockEngine holds every call until release is closed.
type blockEngine struct {
	fakeEngine
	entered chan struct{}
	release chan struct{}
}

func (b *blockEngine) Call(ctx context.Context, name string, args map[string]string) (Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return Result{Stdout: "old-engine"}, nil
}

func TestReplaceInFlightCallKeepsEngine(t *testing.T) {
	old := &blockEngine{entered: make(chan struct{}, 1), release: make(chan struct{})}
	old.tasks = sampleEngine().tasks
	srv := New(old, Options{Reloadable: true})
	cs, _ := connectNotified(t, srv)

	type reply struct {
		text string
		err  error
	}
	done := make(chan reply, 1)
	go func() {
		text, err := callText(t, cs, "logs")
		done <- reply{text, err}
	}()
	<-old.entered

	// Replace returns while the call is still inside the old engine.
	srv.Replace(withTask("new-engine"), 0)
	select {
	case r := <-done:
		t.Fatalf("the call returned %+v before the old engine released it", r)
	default:
	}
	close(old.release)
	if r := <-done; r.err != nil || !strings.HasPrefix(r.text, "old-engine") {
		t.Errorf("in-flight call = %+v, want the old engine's output", r)
	}
	if text, _ := callText(t, cs, "logs"); !strings.HasPrefix(text, "new-engine") {
		t.Errorf("later call = %q, want the new engine's output", text)
	}
}

func TestReplaceOutputLimit(t *testing.T) {
	big := strings.Repeat("x", 10000)
	srv := New(withTask(big), Options{Reloadable: true})
	cs, _ := connectNotified(t, srv)

	srv.Replace(withTask(big), 1024)
	text, err := callText(t, cs, "logs")
	if err != nil {
		t.Fatal(err)
	}
	if stdout := strings.TrimSuffix(text, "\n[exit 0]"); len(stdout) > 1024 || !strings.Contains(stdout, "[truncated ") {
		t.Errorf("stdout is %d bytes, want at most 1024 with a marker", len(stdout))
	}

	srv.Replace(withTask(big), 0)
	if text, _ := callText(t, cs, "logs"); strings.Contains(text, "[truncated ") {
		t.Error("the default limit truncated 10,000 bytes")
	}
}

// A list taken while tools are being replaced is one whole tool set, never
// part of each (spec 043 FR-019).
func TestReplaceConcurrentList(t *testing.T) {
	a := &fakeEngine{tasks: []TaskInfo{{Name: "a1", Doc: "A"}, {Name: "shared", Doc: "A"}}}
	b := &fakeEngine{tasks: []TaskInfo{{Name: "b1", Doc: "B"}, {Name: "b2", Doc: "B"}, {Name: "shared", Doc: "B"}}}
	srv := New(a, Options{Reloadable: true})
	cs, _ := connectNotified(t, srv)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			if i%2 == 0 {
				srv.Replace(b, 0)
			} else {
				srv.Replace(a, 0)
			}
		}
		close(stop)
	}()
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		docs := map[string]bool{}
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
			docs[tool.Description] = true
		}
		slices.Sort(names)
		whole := slices.Equal(names, []string{"a1", "shared"}) || slices.Equal(names, []string{"b1", "b2", "shared"})
		if !whole || len(docs) != 1 {
			t.Fatalf("list mixes two tool sets: %v %v", names, docs)
		}
	}
	wg.Wait()
}

func TestReloadableCapabilities(t *testing.T) {
	reloadable := connect(t, New(&fakeEngine{}, Options{Reloadable: true}))
	tools := reloadable.InitializeResult().Capabilities.Tools
	if tools == nil || !tools.ListChanged {
		t.Errorf("tools capability = %+v, want listChanged with no tool", tools)
	}
	fixed := connect(t, New(&fakeEngine{}, Options{}))
	if fixed.InitializeResult().Capabilities.Tools != nil {
		t.Error("a server that is not reloadable changed its capabilities")
	}
	// With tools, a reloadable server states what the SDK infers.
	inferred := connect(t, New(sampleEngine(), Options{})).InitializeResult().Capabilities
	stated := connect(t, New(sampleEngine(), Options{Reloadable: true})).InitializeResult().Capabilities
	got, _ := json.Marshal(stated)
	want, _ := json.Marshal(inferred)
	if string(got) != string(want) {
		t.Errorf("capabilities = %s, want %s", got, want)
	}
}

func TestSetInstructions(t *testing.T) {
	srv := New(sampleEngine(), Options{Instructions: "v1"})
	before := connect(t, srv)
	if got := before.InitializeResult().Instructions; got != "v1" {
		t.Errorf("first session instructions = %q, want v1", got)
	}
	srv.SetInstructions("v2")
	if got := connect(t, srv).InitializeResult().Instructions; got != "v2" {
		t.Errorf("later session instructions = %q, want v2", got)
	}
	if got := before.InitializeResult().Instructions; got != "v1" {
		t.Errorf("open session instructions became %q", got)
	}
	srv.SetInstructions("")
	if got := connect(t, srv).InitializeResult().Instructions; got != "" {
		t.Errorf("instructions after clearing = %q, want none", got)
	}
}
