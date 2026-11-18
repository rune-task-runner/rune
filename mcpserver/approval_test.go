package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 049: a call that can run a destructive task the operator has not
// granted is put to the person at the client, and runs once on an explicit
// yes. The texts are those of specs/049-mcp-confirm-elicitation/contracts.

// approvedCall is one CallApproved an approvalEngine received.
type approvedCall struct {
	name  string
	args  map[string]string
	lists map[string][]string
	tasks []string
}

// approvalEngine is an Engine that can carry an approval. It runs nothing
// unless approve returned nil, so its run counter is the proof that a call
// did or did not run.
type approvalEngine struct {
	tasks    []TaskInfo
	messages map[string]string // [confirm] message by task
	result   Result
	// running, when set, is called while a run is in progress.
	running func()

	mu    sync.Mutex
	calls []approvedCall
	ran   []map[string]string // arguments of each run, approved or plain
	plain []string            // names called through Call, with no approval
	// returned is signalled each time CallApproved returns.
	returned chan struct{}
}

func (e *approvalEngine) Tasks() []TaskInfo { return e.tasks }

func (e *approvalEngine) Call(_ context.Context, name string, args map[string]string) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plain = append(e.plain, name)
	e.ran = append(e.ran, args)
	return e.result, nil
}

func (e *approvalEngine) CallApproved(ctx context.Context, name string, args map[string]string, lists map[string][]string, tasks []string, approve func(context.Context, ApprovalRequest) error) (Result, error) {
	defer func() {
		if e.returned != nil {
			e.returned <- struct{}{}
		}
	}()
	e.mu.Lock()
	e.calls = append(e.calls, approvedCall{name, args, lists, tasks})
	e.mu.Unlock()
	if err := approve(ctx, e.requestFor(name, args, lists, tasks)); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	e.ran = append(e.ran, args)
	e.mu.Unlock()
	if e.running != nil {
		e.running()
	}
	return e.result, nil
}

// requestFor reports the call as the rune adapter would: every declared
// parameter in order, and the message of each task to approve.
func (e *approvalEngine) requestFor(name string, args map[string]string, lists map[string][]string, tasks []string) ApprovalRequest {
	var req ApprovalRequest
	for _, t := range e.tasks {
		if t.Name != name {
			continue
		}
		for _, p := range t.Params {
			arg := ApprovalArg{Name: p.Name, Values: []string{args[p.Name]}}
			if p.Variadic {
				arg.Values = lists[p.Name]
			}
			req.Args = append(req.Args, arg)
		}
	}
	for _, task := range tasks {
		if msg, ok := e.messages[task]; ok {
			req.Confirmations = append(req.Confirmations, Confirmation{Task: task, Message: msg})
		}
	}
	return req
}

func (e *approvalEngine) runs() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.ran)
}

func (e *approvalEngine) approvedCalls() []approvedCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

// person plays the human at the client: it records each request it is shown
// and answers from a function.
type person struct {
	answer func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error)

	mu       sync.Mutex
	requests []*mcp.ElicitParams
}

func (p *person) handle(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req.Params)
	p.mu.Unlock()
	return p.answer(ctx, req.Params)
}

func (p *person) options() *mcp.ClientOptions {
	return &mcp.ClientOptions{ElicitationHandler: p.handle}
}

func (p *person) asked() []*mcp.ElicitParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

func answerWith(action string, content map[string]any) func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
	return func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: action, Content: content}, nil
	}
}

func approving() *person {
	return &person{answer: answerWith("accept", map[string]any{"approved": true})}
}

func declining() *person { return &person{answer: answerWith("decline", nil)} }

// connectClient is connect with client options, so a test can state
// capabilities and answer requests.
func connectClient(t *testing.T, srv *Server, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, opts)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// askOptions are the options `rune mcp` builds, with the given changes.
func askOptions(change func(*Options)) Options {
	opts := Options{AskApproval: true, RunCommand: "rune", AllowDestructiveFlag: grantFlag}
	if change != nil {
		change(&opts)
	}
	return opts
}

func applyEngine() *approvalEngine {
	return &approvalEngine{
		tasks: []TaskInfo{
			{Name: "apply", Destructive: true, Params: []ParamInfo{{Name: "env", Required: true}}},
			{Name: "build"},
		},
		messages: map[string]string{"apply": "Apply to prod? 3 resources change."},
		result:   Result{Stdout: "applied\n"},
	}
}

const (
	remedyApply   = ` To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.`
	noFormSuffix  = ` Rune could not ask for approval here: the client does not support MCP form elicitation.`
	tooBigSuffix  = ` Rune could not ask for approval here: the approval request would be too large to show.`
	messageApply  = "Approve this Rune task call?\n\nTask: apply\nArguments:\n- env: \"prod\"\nDestructive tasks it can run:\n- apply: Apply to prod? 3 resources change.\n\nApproving runs this call once."
	messageNoArgs = "Approve this Rune task call?\n\nTask: reset\nDestructive tasks it can run:\n- reset: Reset?\n- wipe: Run \"wipe\"?\n\nApproving runs this call once."
)

func TestApprovalSchema(t *testing.T) {
	got, err := json.Marshal(approvalSchema())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"approved":{"default":false,"description":"Run this task call once.","title":"Approve this call","type":"boolean"}},"required":["approved"],"type":"object"}`
	if string(got) != want {
		t.Errorf("approvalSchema:\n got %s\nwant %s", got, want)
	}
	first, second := approvalSchema(), approvalSchema()
	first["type"] = "changed"
	if second["type"] != "object" {
		t.Error("two approvalSchema results share a map")
	}
}

func TestApprovalMessageLayout(t *testing.T) {
	got, fits := approvalMessage("apply", []string{"apply"}, ApprovalRequest{
		Args:          []ApprovalArg{{Name: "env", Values: []string{"prod"}}},
		Confirmations: []Confirmation{{Task: "apply", Message: "Apply to prod? 3 resources change."}},
	})
	if got != messageApply || !fits {
		t.Errorf("approvalMessage:\n got %q, fits=%v\nwant %q", got, fits, messageApply)
	}

	// No parameter, a missing message, an empty one, and an entry for a task
	// the server did not name.
	got, _ = approvalMessage("reset", []string{"reset", "wipe"}, ApprovalRequest{
		Confirmations: []Confirmation{{Task: "reset", Message: "Reset?"}, {Task: "wipe", Message: ""}, {Task: "other", Message: "Other?"}},
	})
	if got != messageNoArgs {
		t.Errorf("approvalMessage without arguments:\n got %q\nwant %q", got, messageNoArgs)
	}
	got, _ = approvalMessage("reset", []string{"wipe", "reset"}, ApprovalRequest{})
	if !strings.Contains(got, "- wipe: Run \"wipe\"?\n- reset: Run \"reset\"?\n") {
		t.Errorf("names not in the order given, or no default question: %q", got)
	}

	got, _ = approvalMessage("pack", []string{"pack"}, ApprovalRequest{Args: []ApprovalArg{
		{Name: "files", Values: []string{"a b", "c"}},
		{Name: "extra", Values: nil},
		{Name: "empty", Values: []string{""}},
	}})
	want := "Arguments:\n- files: \"a b\" \"c\"\n- extra: (none)\n- empty: \"\"\n"
	if !strings.Contains(got, want) {
		t.Errorf("variadic and empty values:\n got %q\nwant it to hold %q", got, want)
	}
}

// formClient connects a client that states caps and returns the server's
// side of the session.
func formClient(t *testing.T, caps *mcp.ClientCapabilities) *mcp.ServerSession {
	t.Helper()
	srv := New(sampleEngine(), Options{})
	connectClient(t, srv, &mcp.ClientOptions{Capabilities: caps})
	for ss := range srv.MCP().Sessions() {
		return ss
	}
	t.Fatal("no server session")
	return nil
}

func TestFormSupport(t *testing.T) {
	cases := []struct {
		name string
		caps *mcp.ClientCapabilities
		want bool
	}{
		{"no elicitation", &mcp.ClientCapabilities{}, false},
		{"default client", nil, false},
		{"no mode", &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{}}, true},
		{"form", &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}}}, true},
		{"form and URL", &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}, URL: &mcp.URLElicitationCapabilities{}}}, true},
		{"URL only", &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}, false},
	}
	for _, c := range cases {
		if got := formSupport(formClient(t, c.caps)); got != c.want {
			t.Errorf("%s: formSupport = %v, want %v", c.name, got, c.want)
		}
	}
	if formSupport(nil) {
		t.Error("formSupport(nil session) = true")
	}
	if formCapability(nil) || formCapability(&mcp.InitializeParams{}) {
		t.Error("a session with no initialize parameters or no capabilities has form support")
	}
}

func TestReadAnswerApproved(t *testing.T) {
	res := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}
	if got := readAnswer(res); got != outcomeApproved {
		t.Errorf("readAnswer(accept, approved true) = %v, want approved", got)
	}
}

func TestReadAnswer(t *testing.T) {
	cases := []struct {
		name string
		res  *mcp.ElicitResult
		want outcome
	}{
		{"accept false", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": false}}, outcomeNotGiven},
		{"decline", &mcp.ElicitResult{Action: "decline"}, outcomeDeclined},
		{"decline with approved true", &mcp.ElicitResult{Action: "decline", Content: map[string]any{"approved": true}}, outcomeDeclined},
		{"cancel", &mcp.ElicitResult{Action: "cancel"}, outcomeDismissed},
		{"accept, nil content", &mcp.ElicitResult{Action: "accept"}, outcomeNoAnswer},
		{"accept, no approved key", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"other": true}}, outcomeNoAnswer},
		{"accept, string", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": "true"}}, outcomeNoAnswer},
		{"accept, number", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": 1}}, outcomeNoAnswer},
		{"accept, null", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": nil}}, outcomeNoAnswer},
		{"unknown action", &mcp.ElicitResult{Action: "sure", Content: map[string]any{"approved": true}}, outcomeNoAnswer},
		{"empty action", &mcp.ElicitResult{Content: map[string]any{"approved": true}}, outcomeNoAnswer},
		{"extra keys", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true, "note": "x"}}, outcomeApproved},
		{"nil result", nil, outcomeNoAnswer},
	}
	for _, c := range cases {
		if got := readAnswer(c.res); got != c.want {
			t.Errorf("%s: readAnswer = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOutcomeText(t *testing.T) {
	srv := New(applyEngine(), askOptions(nil))
	names := []string{"apply"}
	cases := []struct {
		o    outcome
		want string
	}{
		{outcomeNotGiven, `task "apply" was not approved: approval was not given. Nothing ran.` + remedyApply},
		{outcomeDeclined, `task "apply" was not approved: the request was declined. Nothing ran.` + remedyApply},
		{outcomeDismissed, `task "apply" was not approved: the request was dismissed. Nothing ran.` + remedyApply},
		{outcomeTimedOut, `task "apply" was not approved: no answer arrived within 5m0s. Nothing ran.` + remedyApply},
		{outcomeNoAnswer, `task "apply" was not approved: the client returned no valid answer. Nothing ran.` + remedyApply},
		{outcomeCancelled, `task "apply" was not approved: the call was cancelled. Nothing ran.`},
		{outcomeChanged, `task "apply" was not approved: the Runefile changed while the request was pending; call again. Nothing ran.`},
	}
	for _, c := range cases {
		if got := srv.outcomeText(c.o, "apply", names, true); got != c.want {
			t.Errorf("outcomeText(%v):\n got %q\nwant %q", c.o, got, c.want)
		}
	}
	short := New(applyEngine(), askOptions(func(o *Options) { o.ApprovalTimeout = 50 * time.Millisecond }))
	want := `task "apply" was not approved: no answer arrived within 50ms. Nothing ran.` + remedyApply
	if got := short.outcomeText(outcomeTimedOut, "apply", names, true); got != want {
		t.Errorf("timeout text with a configured wait:\n got %q\nwant %q", got, want)
	}
}

func TestRemedy(t *testing.T) {
	names := []string{"clean", "wipe"}
	cases := []struct {
		name   string
		change func(*Options)
		want   string
	}{
		{"flag and command", nil, ` To run it without a request, the operator can add clean,wipe to --allow-destructive and restart the server, or run "rune release" themselves.`},
		{"command only", func(o *Options) { o.AllowDestructiveFlag = "" }, ` To run it without a request, the operator can run "rune release" themselves.`},
		{"flag only", func(o *Options) { o.RunCommand = "" }, ` To run it without a request, the operator can add clean,wipe to --allow-destructive and restart the server.`},
		{"neither", func(o *Options) { o.RunCommand, o.AllowDestructiveFlag = "", "" }, ""},
		{
			"a command with a Windows path", func(o *Options) { o.RunCommand, o.AllowDestructiveFlag = `rune -f C:\repo\Runefile`, "" },
			` To run it without a request, the operator can run "rune -f C:\repo\Runefile release" themselves.`,
		},
	}
	for _, c := range cases {
		srv := New(applyEngine(), askOptions(c.change))
		if got := srv.remedy("release", names, true); got != c.want {
			t.Errorf("%s: remedy = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCannotAsk(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Options)
		reason string
		want   string
	}{
		{"flag and command", nil, reasonNoForm, `. Or the operator can run "rune release" themselves.` + noFormSuffix},
		{"no flag", func(o *Options) { o.AllowDestructiveFlag = "" }, reasonNoForm, `. The operator can run "rune release" themselves.` + noFormSuffix},
		{"no command", func(o *Options) { o.RunCommand = "" }, reasonNoForm, `.` + noFormSuffix},
		{"too large", nil, reasonTooLarge, `. Or the operator can run "rune release" themselves.` + tooBigSuffix},
		{
			"a command with a Windows path", func(o *Options) { o.RunCommand = `rune -f C:\repo\Runefile` }, reasonNoForm,
			`. Or the operator can run "rune -f C:\repo\Runefile release" themselves.` + noFormSuffix,
		},
	}
	for _, c := range cases {
		srv := New(applyEngine(), askOptions(c.change))
		if got := srv.cannotAsk("release", c.reason); got != c.want {
			t.Errorf("%s: cannotAsk = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestApprovalTimeoutDefault(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if got := New(applyEngine(), Options{ApprovalTimeout: d}).approvalTimeout(); got != DefaultApprovalTimeout {
			t.Errorf("approvalTimeout for %v = %v, want %v", d, got, DefaultApprovalTimeout)
		}
	}
	if got := New(applyEngine(), Options{ApprovalTimeout: time.Second}).approvalTimeout(); got != time.Second {
		t.Errorf("approvalTimeout = %v, want the configured 1s", got)
	}
}

// An answer that arrives as the wait or the call ends does not count (FR-018).
func TestSettled(t *testing.T) {
	live := context.Background()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name          string
		call, wait    context.Context
		given, wanted outcome
	}{
		{"both live", live, live, outcomeApproved, outcomeApproved},
		{"wait over", live, done, outcomeApproved, outcomeTimedOut},
		{"call over", done, done, outcomeApproved, outcomeCancelled},
		{"error, wait over", live, done, outcomeNoAnswer, outcomeTimedOut},
		{"error, both live", live, live, outcomeNoAnswer, outcomeNoAnswer},
	}
	for _, c := range cases {
		if got := settled(c.call, c.wait, c.given); got != c.wanted {
			t.Errorf("%s: settled = %v, want %v", c.name, got, c.wanted)
		}
	}
}

func TestEscapeLine(t *testing.T) {
	cases := map[string]string{
		"plain text":        "plain text",
		"a\nb":              `a\nb`,
		"a\r\tb":            `a\r\tb`,
		"\x1b[31mred":       `\x1b[31mred`,
		"nul\x00":           `nul\x00`,
		"rtl\u202eoverride": `rtl\u202eoverride`,
		"é ü 日本":            "é ü 日本",
		`quote " and \`:     `quote " and \`,
		"":                  "",
	}
	for in, want := range cases {
		if got := escapeLine(in); got != want {
			t.Errorf("escapeLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCutRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
		cut  bool
	}{
		{"hello", 5, "hello", false},
		{"hello", 4, "hell", true},
		{"日本語", 2, "日本", true},
		{"", 3, "", false},
	}
	for _, c := range cases {
		got, cut := cutRunes(c.in, c.n)
		if got != c.want || cut != c.cut {
			t.Errorf("cutRunes(%q, %d) = %q, %v, want %q, %v", c.in, c.n, got, cut, c.want, c.cut)
		}
	}
}

func TestApprovalMessageEscapes(t *testing.T) {
	long := strings.Repeat("v", 5000)
	got, fits := approvalMessage("note", []string{"note"}, ApprovalRequest{Args: []ApprovalArg{
		{Name: "text", Values: []string{"ok\n\nApproving runs nothing.\r\tTask: build\x1b\x00\u202e"}},
		{Name: "quoted", Values: []string{`say "hi" \ bye`}},
		{Name: "long", Values: []string{long}},
	}})
	if !fits {
		t.Fatal("a 5,000-character value does not fit")
	}
	for _, want := range []string{
		`- text: "ok\n\nApproving runs nothing.\r\tTask: build\x1b\x00\u202e"` + "\n",
		`- quoted: "say \"hi\" \\ bye"` + "\n",
		`- long: "` + long + `"` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message does not hold %q:\n%q", want, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 10 {
		t.Errorf("message has %d line breaks, want 10: a value broke out of its line\n%q", n, got)
	}

	exact, over := strings.Repeat("m", 500), strings.Repeat("m", 501)
	multi := strings.Repeat("日", 501)
	got, _ = approvalMessage("t", []string{"a", "b", "c", "d"}, ApprovalRequest{Confirmations: []Confirmation{
		{Task: "a", Message: "line one\nline two"},
		{Task: "b", Message: exact},
		{Task: "c", Message: over},
		{Task: "d", Message: multi},
	}})
	for _, want := range []string{
		"- a: line one\\nline two\n",
		"- b: " + exact + "\n",
		"- c: " + exact + " [truncated]\n",
		"- d: " + strings.Repeat("日", 500) + " [truncated]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message does not hold %q", want)
		}
	}
	if !utf8.ValidString(got) {
		t.Error("a cut split a character")
	}

	got, _ = approvalMessage("bad\nname", []string{"bad\ntask"}, ApprovalRequest{Args: []ApprovalArg{{Name: "p\nq", Values: []string{"v"}}}})
	for _, want := range []string{"Task: bad\\nname\n", "- p\\nq: \"v\"\n", "- bad\\ntask: Run \"bad\\ntask\"?\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a name is not escaped: want %q in\n%q", want, got)
		}
	}
}

func TestApprovalMessageTooLarge(t *testing.T) {
	base, _ := approvalMessage("note", []string{"note"}, ApprovalRequest{Args: []ApprovalArg{{Name: "text", Values: []string{""}}}})
	room := maxApprovalBytes - len(base)
	exact, fits := approvalMessage("note", []string{"note"}, ApprovalRequest{Args: []ApprovalArg{{Name: "text", Values: []string{strings.Repeat("v", room)}}}})
	if len(exact) != maxApprovalBytes || !fits {
		t.Errorf("a message of %d bytes: fits=%v, want it to fit at exactly %d", len(exact), fits, maxApprovalBytes)
	}
	if _, fits := approvalMessage("note", []string{"note"}, ApprovalRequest{Args: []ApprovalArg{{Name: "text", Values: []string{strings.Repeat("v", room+1)}}}}); fits {
		t.Error("a message one byte over the limit fits")
	}
	// A value that cannot fit by its own size is refused before it is quoted.
	huge := strings.Repeat("\x00", maxApprovalBytes)
	if got, fits := approvalMessage("note", []string{"note"}, ApprovalRequest{Args: []ApprovalArg{{Name: "text", Values: []string{huge}}}}); fits || got != "" {
		t.Errorf("a value of the limit's size: fits=%v with a message of %d bytes, want no message", fits, len(got))
	}
	var names []string
	var confirmations []Confirmation
	for i := range 40 {
		name := "task" + strconv.Itoa(i)
		names = append(names, name)
		confirmations = append(confirmations, Confirmation{Task: name, Message: strings.Repeat("m", 400)})
	}
	if _, fits := approvalMessage("big", names, ApprovalRequest{Confirmations: confirmations}); fits {
		t.Error("40 tasks with 400-character messages fit")
	}
}

// callFor calls a tool and fails the test on a protocol error.
func callFor(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (text string, res *mcp.CallToolResult) {
	t.Helper()
	res = callTool(t, cs, tool, args)
	return contentText(res), res
}

// US1: the person is asked before anything runs, and the call runs once on
// approval. A second call asks again.
func TestHandlerAsksThenRuns(t *testing.T) {
	eng := applyEngine()
	who := approving()
	runsWhenAsked := -1
	who.answer = func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		runsWhenAsked = eng.runs()
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())

	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
	if res.IsError || text != "applied\n[exit 0]" {
		t.Fatalf("approved call: isError=%v text=%q", res.IsError, text)
	}
	if res.StructuredContent == nil {
		t.Error("approved call has no structured content")
	}
	asked := who.asked()
	if len(asked) != 1 || asked[0].Message != messageApply || asked[0].Mode != "form" {
		t.Fatalf("requests = %+v, want one form request with %q", asked, messageApply)
	}
	schema, _ := json.Marshal(asked[0].RequestedSchema)
	want, _ := json.Marshal(approvalSchema())
	if string(schema) != string(want) {
		t.Errorf("requested schema = %s, want %s", schema, want)
	}
	if runsWhenAsked != 0 {
		t.Errorf("the engine had run %d times when the person was asked", runsWhenAsked)
	}
	calls := eng.approvedCalls()
	if len(calls) != 1 || calls[0].name != "apply" || !slices.Equal(calls[0].tasks, []string{"apply"}) || calls[0].args["env"] != "prod" {
		t.Errorf("CallApproved calls = %+v", calls)
	}
	if eng.runs() != 1 || len(eng.plain) != 0 {
		t.Errorf("runs = %d, plain calls = %v, want one approved run", eng.runs(), eng.plain)
	}

	callFor(t, cs, "apply", map[string]any{"env": "prod"})
	if len(who.asked()) != 2 || eng.runs() != 2 {
		t.Errorf("second call: %d requests, %d runs, want 2 and 2", len(who.asked()), eng.runs())
	}

	// The task's own failure is the result; the approval is not mentioned.
	eng.result = Result{Stdout: "boom\n", ExitCode: 3}
	text, res = callFor(t, cs, "apply", map[string]any{"env": "prod"})
	if !res.IsError || text != "boom\n[exit 3]" {
		t.Errorf("approved call that fails: isError=%v text=%q", res.IsError, text)
	}
}

func TestHandlerGrantSkipsApproval(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.AllowDestructive = []string{"apply"} })), who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
	if res.IsError || text != "applied\n[exit 0]" {
		t.Errorf("granted call: isError=%v text=%q", res.IsError, text)
	}
	if len(who.asked()) != 0 || len(eng.approvedCalls()) != 0 || !slices.Equal(eng.plain, []string{"apply"}) {
		t.Errorf("granted call: %d requests, approved calls %v, plain %v", len(who.asked()), eng.approvedCalls(), eng.plain)
	}
}

func TestHandlerNoApprovalNeeded(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	if _, res := callFor(t, cs, "build", nil); res.IsError {
		t.Errorf("build refused: %q", contentText(res))
	}
	if len(who.asked()) != 0 || !slices.Equal(eng.plain, []string{"build"}) {
		t.Errorf("non-destructive call: %d requests, plain %v", len(who.asked()), eng.plain)
	}
}

// US2: every answer but an explicit yes runs nothing, says which case it
// was, and is asked once.
func TestHandlerNotApproved(t *testing.T) {
	head := `task "apply" was not approved: `
	cases := []struct {
		name   string
		answer func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error)
		want   string
	}{
		{"decline", answerWith("decline", nil), head + "the request was declined. Nothing ran." + remedyApply},
		{"cancel", answerWith("cancel", nil), head + "the request was dismissed. Nothing ran." + remedyApply},
		{"accept false", answerWith("accept", map[string]any{"approved": false}), head + "approval was not given. Nothing ran." + remedyApply},
		{"accept, no content", answerWith("accept", nil), head + "the client returned no valid answer. Nothing ran." + remedyApply},
		{"accept, empty content", answerWith("accept", map[string]any{}), head + "the client returned no valid answer. Nothing ran." + remedyApply},
		{"unknown action", answerWith("sure", map[string]any{"approved": true}), head + "the client returned no valid answer. Nothing ran." + remedyApply},
		{"client error", func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "s3cr3t from the client"}
		}, head + "the client returned no valid answer. Nothing ran." + remedyApply},
	}
	for _, c := range cases {
		eng := applyEngine()
		who := &person{answer: c.answer}
		cs := connectClient(t, New(eng, askOptions(nil)), who.options())
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if !res.IsError || text != c.want {
			t.Errorf("%s:\n got isError=%v %q\nwant %q", c.name, res.IsError, text, c.want)
		}
		if len(res.Content) != 1 || res.StructuredContent != nil {
			t.Errorf("%s: %d content blocks, structured %v, want one text block and none", c.name, len(res.Content), res.StructuredContent)
		}
		if eng.runs() != 0 {
			t.Errorf("%s: the engine ran", c.name)
		}
		if n := len(who.asked()); n != 1 {
			t.Errorf("%s: asked %d times, want once", c.name, n)
		}
	}
}

// Quickstart Y: an approval that arrives after Rune stopped waiting runs
// nothing.
func TestHandlerApprovalTimeout(t *testing.T) {
	eng := applyEngine()
	answered := make(chan struct{})
	who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		defer close(answered)
		time.Sleep(300 * time.Millisecond)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	srv := New(eng, askOptions(func(o *Options) { o.ApprovalTimeout = 50 * time.Millisecond }))
	cs := connectClient(t, srv, who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
	want := `task "apply" was not approved: no answer arrived within 50ms. Nothing ran.` + remedyApply
	if !res.IsError || text != want {
		t.Errorf("timeout:\n got isError=%v %q\nwant %q", res.IsError, text, want)
	}
	<-answered
	// The late answer has been sent. Give the server a moment to drop it.
	time.Sleep(50 * time.Millisecond)
	if eng.runs() != 0 {
		t.Error("the engine ran on an approval that arrived after the wait")
	}
}

func TestHandlerCallCancelled(t *testing.T) {
	eng := applyEngine()
	eng.returned = make(chan struct{}, 1)
	shown := make(chan struct{})
	withdrawn := make(chan struct{})
	who := &person{answer: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		close(shown)
		<-ctx.Done()
		close(withdrawn)
		return nil, ctx.Err()
	}}
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-shown
		cancel()
	}()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled call returned %v, want context.Canceled", err)
	}
	<-eng.returned
	select {
	case <-withdrawn:
	case <-time.After(5 * time.Second):
		t.Error("the request was not withdrawn from the person")
	}
	if eng.runs() != 0 {
		t.Error("the engine ran a cancelled call")
	}
}

// A client that goes away while the person is deciding leaves nothing run.
// The pipes stand for a stdio client whose process ended: the server reads
// end of input.
func TestHandlerClientDisconnects(t *testing.T) {
	eng := applyEngine()
	eng.returned = make(chan struct{}, 1)
	shown := make(chan struct{})
	release := make(chan struct{})
	who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		close(shown)
		<-release
		return nil, errors.New("gone")
	}}
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	ctx := context.Background()
	if _, err := New(eng, askOptions(nil)).MCP().Connect(ctx, &mcp.IOTransport{Reader: toServer, Writer: fromServer}, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, who.options())
	cs, err := client.Connect(ctx, &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(release)
		_ = cs.Close()
		_ = toServer.Close()
		_ = fromServer.Close()
	})
	go func() {
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	}()
	<-shown
	_ = fromClient.Close()
	select {
	case <-eng.returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the client went away")
	}
	if eng.runs() != 0 {
		t.Error("the engine ran after the client went away")
	}
}

// US3: the request names every ungranted destructive task the call can run,
// and the engine is told exactly those names.
func TestHandlerAsksForReach(t *testing.T) {
	tasks := []TaskInfo{
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		{Name: "full", DestructiveReach: []string{"clean", "wipe"}},
	}
	messages := map[string]string{"clean": "Delete dist?", "reset": "Reset?"}
	cases := []struct {
		tool   string
		grants []string
		names  []string
		lines  string
	}{
		{"release", nil, []string{"clean"}, "Task: release\nDestructive tasks it can run:\n- clean: Delete dist?\n\n"},
		{"reset", nil, []string{"reset", "wipe"}, "Task: reset\nDestructive tasks it can run:\n- reset: Reset?\n- wipe: Run \"wipe\"?\n\n"},
		{"full", []string{"clean"}, []string{"wipe"}, "Task: full\nDestructive tasks it can run:\n- wipe: Run \"wipe\"?\n\n"},
	}
	for _, c := range cases {
		eng := &approvalEngine{tasks: tasks, messages: messages}
		who := approving()
		cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.AllowDestructive = c.grants })), who.options())
		if _, res := callFor(t, cs, c.tool, nil); res.IsError {
			t.Errorf("%s: approved call refused: %q", c.tool, contentText(res))
		}
		calls, asked := eng.approvedCalls(), who.asked()
		if len(calls) != 1 || !slices.Equal(calls[0].tasks, c.names) {
			t.Errorf("%s: CallApproved tasks = %+v, want %q", c.tool, calls, c.names)
		}
		if len(asked) != 1 || !strings.Contains(asked[0].Message, c.lines) {
			t.Errorf("%s: request %+v does not hold %q", c.tool, asked, c.lines)
		}
	}
}

// US5, SC-003: concurrent calls each get their own request and their own
// answer.
func TestHandlerConcurrentApprovals(t *testing.T) {
	eng := &approvalEngine{tasks: []TaskInfo{{Name: "note", Destructive: true, Params: []ParamInfo{{Name: "id", Required: true}}}}}
	who := &person{answer: func(_ context.Context, p *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		// The request shows the id; only even ones are approved.
		_, rest, _ := strings.Cut(p.Message, `- id: "`)
		id, _, _ := strings.Cut(rest, `"`)
		n, err := strconv.Atoi(id)
		if err != nil {
			return nil, err
		}
		if n%2 == 0 {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
		}
		return &mcp.ElicitResult{Action: "decline"}, nil
	}}
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())

	const calls = 100
	results := make([]*mcp.CallToolResult, calls)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "note", Arguments: map[string]any{"id": strconv.Itoa(i)}})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			results[i] = res
		}()
	}
	wg.Wait()

	ranIDs := map[string]int{}
	eng.mu.Lock()
	for _, args := range eng.ran {
		ranIDs[args["id"]]++
	}
	eng.mu.Unlock()
	for i := range calls {
		id := strconv.Itoa(i)
		want := 0
		if i%2 == 0 {
			want = 1
		}
		if ranIDs[id] != want {
			t.Errorf("id %d ran %d times, want %d", i, ranIDs[id], want)
		}
		if res := results[i]; res != nil && res.IsError != (want == 0) {
			t.Errorf("id %d: isError=%v", i, res.IsError)
		}
	}
	if n := len(who.asked()); n != calls {
		t.Errorf("%d requests for %d calls", n, calls)
	}
}

// US5: a reload while the person is deciding makes the approval unusable.
func TestHandlerReloadInvalidatesApproval(t *testing.T) {
	eng := applyEngine()
	var srv *Server
	who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		srv.Replace(eng, 0)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	srv = New(eng, askOptions(nil))
	cs := connectClient(t, srv, who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
	want := `task "apply" was not approved: the Runefile changed while the request was pending; call again. Nothing ran.`
	if !res.IsError || text != want {
		t.Errorf("approval after a reload:\n got isError=%v %q\nwant %q", res.IsError, text, want)
	}
	if eng.runs() != 0 {
		t.Error("the engine ran a call approved before a reload")
	}

	// Without a reload the same approval runs.
	who.answer = answerWith("accept", map[string]any{"approved": true})
	if _, res := callFor(t, cs, "apply", map[string]any{"env": "prod"}); res.IsError || eng.runs() != 1 {
		t.Errorf("approval with no reload: isError=%v runs=%d", res.IsError, eng.runs())
	}

	// A reload once the run has started does not stop it.
	eng.running = func() { srv.Replace(eng, 0) }
	if _, res := callFor(t, cs, "apply", map[string]any{"env": "prod"}); res.IsError || eng.runs() != 2 {
		t.Errorf("reload during the run: isError=%v runs=%d", res.IsError, eng.runs())
	}
}

// FR-009: nothing the agent sends counts as approval.
func TestHandlerApprovalArgumentIsUnknown(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod", "approved": true})
	if want := `unknown argument "approved" for task "apply"`; !res.IsError || text != want {
		t.Errorf("approved as an argument: isError=%v %q, want %q", res.IsError, text, want)
	}
	if len(who.asked()) != 0 || eng.runs() != 0 {
		t.Errorf("%d requests, %d runs, want none", len(who.asked()), eng.runs())
	}
}

// FR-004: a call that could not run is not put to the person.
func TestHandlerInvalidValueNotAsked(t *testing.T) {
	eng := &approvalEngine{tasks: []TaskInfo{{Name: "apply", Destructive: true, Params: []ParamInfo{
		{Name: "env", Required: true, Kind: "enum", Enum: []string{"staging", "prod"}},
	}}}}
	who := approving()
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	for _, args := range []map[string]any{{"env": "production"}, {"env": map[string]any{"a": 1}}} {
		text, res := callFor(t, cs, "apply", args)
		if !res.IsError || !strings.HasPrefix(text, "invalid arguments: ") {
			t.Errorf("%v: isError=%v %q, want an invalid-arguments error", args, res.IsError, text)
		}
	}
	if len(who.asked()) != 0 || eng.runs() != 0 {
		t.Errorf("%d requests, %d runs, want none", len(who.asked()), eng.runs())
	}
}

// FR-024: a call sent again is a new call.
func TestHandlerRepeatedCallAsksAgain(t *testing.T) {
	eng := applyEngine()
	answers := []string{"accept", "decline"}
	var mu sync.Mutex
	who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		mu.Lock()
		defer mu.Unlock()
		action := answers[0]
		answers = answers[1:]
		return &mcp.ElicitResult{Action: action, Content: map[string]any{"approved": true}}, nil
	}}
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	args := map[string]any{"env": "prod"}
	if _, res := callFor(t, cs, "apply", args); res.IsError {
		t.Errorf("first call refused: %q", contentText(res))
	}
	if _, res := callFor(t, cs, "apply", args); !res.IsError {
		t.Error("the same call sent again ran without its own approval")
	}
	if len(who.asked()) != 2 || eng.runs() != 1 {
		t.Errorf("%d requests, %d runs, want 2 and 1", len(who.asked()), eng.runs())
	}
}

func reachEngine() *approvalEngine {
	return &approvalEngine{tasks: []TaskInfo{
		{Name: "clean", Destructive: true},
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		{Name: "tools::clean", Destructive: true},
	}}
}

// US4: a client that cannot show a form is not asked, and its refusal names
// the command to run and why Rune could not ask.
func TestHandlerClientCannotBeAsked(t *testing.T) {
	want := map[string]string{
		"clean":        `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune clean" themselves.` + noFormSuffix,
		"release":      `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune release" themselves.` + noFormSuffix,
		"reset":        `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server. Or the operator can run "rune reset" themselves.` + noFormSuffix,
		"tools__clean": `task "tools::clean" is destructive; to permit it, add tools::clean to --allow-destructive and restart the server. Or the operator can run "rune tools::clean" themselves.` + noFormSuffix,
	}
	urlOnly := &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	clients := map[string]func(*person) *mcp.ClientOptions{
		"no elicitation": func(*person) *mcp.ClientOptions { return nil },
		"URL only": func(p *person) *mcp.ClientOptions {
			return &mcp.ClientOptions{ElicitationHandler: p.handle, Capabilities: urlOnly}
		},
	}
	for name, options := range clients {
		eng := reachEngine()
		who := approving()
		cs := connectClient(t, New(eng, askOptions(nil)), options(who))
		for tool, text := range want {
			got, res := callFor(t, cs, tool, nil)
			if !res.IsError || got != text {
				t.Errorf("%s, %s:\n got isError=%v %q\nwant %q", name, tool, res.IsError, got, text)
			}
		}
		// The refusal still comes before the argument checks (spec 034 FR-010).
		if got, _ := callFor(t, cs, "clean", map[string]any{"bogus": 1}); got != want["clean"] {
			t.Errorf("%s, clean with a bogus argument: %q, want the refusal", name, got)
		}
		if len(who.asked()) != 0 || eng.runs() != 0 || len(eng.approvedCalls()) != 0 {
			t.Errorf("%s: %d requests, %d runs", name, len(who.asked()), eng.runs())
		}
	}

	// Elicitation stated with no mode is form support (quickstart R).
	eng := reachEngine()
	who := approving()
	bare := &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{}}
	cs := connectClient(t, New(eng, askOptions(nil)), &mcp.ClientOptions{ElicitationHandler: who.handle, Capabilities: bare})
	if _, res := callFor(t, cs, "clean", nil); res.IsError || len(who.asked()) != 1 || eng.runs() != 1 {
		t.Errorf("elicitation with no mode: isError=%v, %d requests, %d runs", res.IsError, len(who.asked()), eng.runs())
	}
}

// FR-023: a server that was not told to ask, or whose engine cannot carry an
// approval, refuses as before.
func TestHandlerDoesNotAskByDefault(t *testing.T) {
	who := approving()
	eng := reachEngine()
	cs := connectClient(t, New(eng, Options{AllowDestructiveFlag: grantFlag}), who.options())
	if got, res := callFor(t, cs, "clean", nil); !res.IsError || got != refusedClean {
		t.Errorf("server that does not ask: %q, want %q", got, refusedClean)
	}
	plain := &fakeEngine{tasks: []TaskInfo{{Name: "clean", Destructive: true}}}
	cs = connectClient(t, New(plain, askOptions(nil)), who.options())
	if got, res := callFor(t, cs, "clean", nil); !res.IsError || got != refusedClean {
		t.Errorf("engine that cannot carry an approval: %q, want %q", got, refusedClean)
	}
	if len(who.asked()) != 0 || eng.runs() != 0 || plain.lastCall != "" {
		t.Errorf("%d requests, %d runs, plain call %q", len(who.asked()), eng.runs(), plain.lastCall)
	}
}

// FR-005: approval never reaches a task the operator hid.
func TestHandlerHiddenTaskNeverAsked(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.DenyList = []string{"apply"} })), who.options())
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("call to a denied task: err = %v, want unknown tool", err)
	}
	if len(who.asked()) != 0 || eng.runs() != 0 {
		t.Errorf("%d requests, %d runs, want none", len(who.asked()), eng.runs())
	}
}

// FR-011: a request that does not fit is not sent, and nothing in it is
// dropped to make it fit.
func TestHandlerOversizeRequestRefused(t *testing.T) {
	eng := applyEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": strings.Repeat("x", 10000)})
	want := `task "apply" is destructive; to permit it, add apply to --allow-destructive and restart the server. Or the operator can run "rune apply" themselves.` + tooBigSuffix
	if !res.IsError || text != want {
		t.Errorf("oversize request:\n got isError=%v %q\nwant %q", res.IsError, text, want)
	}
	if len(who.asked()) != 0 || eng.runs() != 0 {
		t.Errorf("%d requests, %d runs, want none", len(who.asked()), eng.runs())
	}
}

func TestOutcomeNames(t *testing.T) {
	// Every outcome has its own text, so a new one cannot fall through to
	// another's.
	srv := New(applyEngine(), askOptions(nil))
	seen := map[string]outcome{}
	for o := outcomeNotGiven; o <= outcomeChanged; o++ {
		text := srv.outcomeText(o, "apply", []string{"apply"}, true)
		if other, dup := seen[text]; dup {
			t.Errorf("outcomes %v and %v share the text %q", other, o, text)
		}
		seen[text] = o
	}
	if got := fmt.Sprint(reflect.TypeOf(outcomeApproved)); got != "mcpserver.outcome" {
		t.Errorf("outcome type = %s", got)
	}
}

func TestRunHint(t *testing.T) {
	srv := New(applyEngine(), askOptions(func(o *Options) { o.RunCommand = `rune -f C:\repo\Runefile` }))
	if got, want := srv.runHint("tools::clean"), `"rune -f C:\repo\Runefile tools::clean"`; got != want {
		t.Errorf("runHint = %s, want %s", got, want)
	}
	// A task name is an embedder's to choose; it still cannot break the line.
	if got, want := srv.runHint("bad\nname"), `"rune -f C:\repo\Runefile bad\nname"`; got != want {
		t.Errorf("runHint with a line break in the name = %s, want %s", got, want)
	}
}

// Spec 052: the record of a call that needed approval says whether the
// person was asked, how the request ended and what it covered. A call that
// ran nothing has no exit code.
func TestAuditApprovalDecision(t *testing.T) {
	accept := map[string]any{"approved": true}
	tests := []struct {
		name     string
		answer   func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error)
		options  func(*Options)
		noForm   bool
		args     map[string]any
		asked    bool
		decision string
		outcome  string
		kind     string
	}{
		{name: "approved", answer: answerWith("accept", accept), asked: true, decision: AuditDecisionApproved, outcome: AuditCompleted},
		{name: "declined", answer: answerWith("decline", nil), asked: true, decision: AuditDecisionDeclined, outcome: AuditRefused, kind: AuditKindApproval},
		{name: "dismissed", answer: answerWith("cancel", nil), asked: true, decision: AuditDecisionDismissed, outcome: AuditRefused, kind: AuditKindApproval},
		{name: "not given", answer: answerWith("accept", map[string]any{"approved": false}), asked: true, decision: AuditDecisionNotGiven, outcome: AuditRefused, kind: AuditKindApproval},
		{name: "no valid answer", answer: answerWith("accept", nil), asked: true, decision: AuditDecisionNoAnswer, outcome: AuditRefused, kind: AuditKindApproval},
		{
			name: "timed out",
			answer: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			options: func(o *Options) { o.ApprovalTimeout = 30 * time.Millisecond },
			asked:   true, decision: AuditDecisionTimedOut, outcome: AuditTimedOut, kind: AuditKindApproval,
		},
		{name: "a client that cannot show a form", noForm: true, decision: AuditDecisionCannotAsk, outcome: AuditRefused, kind: AuditKindPolicy},
		{
			name: "a request too large to show", answer: answerWith("accept", accept),
			args:     map[string]any{"env": strings.Repeat("x", 10000)},
			decision: AuditDecisionCannotAsk, outcome: AuditRefused, kind: AuditKindPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, rec := applyEngine(), newRecorder()
			opts := askOptions(func(o *Options) {
				o.Audit = rec
				if tt.options != nil {
					tt.options(o)
				}
			})
			var clientOpts *mcp.ClientOptions
			if !tt.noForm {
				clientOpts = (&person{answer: tt.answer}).options()
			}
			cs := connectClient(t, New(eng, opts), clientOpts)
			args := tt.args
			if args == nil {
				args = map[string]any{"env": "prod"}
			}
			callTool(t, cs, "apply", args)
			_, end := rec.only(t)
			want := AuditApproval{Asked: tt.asked, Decision: tt.decision, Tasks: []string{"apply"}}
			if end.Approval == nil || !reflect.DeepEqual(*end.Approval, want) {
				t.Errorf("approval %+v, want %+v", end.Approval, want)
			}
			if end.Outcome != tt.outcome || end.ErrorKind != tt.kind {
				t.Errorf("outcome %q kind %q, want %q %q", end.Outcome, end.ErrorKind, tt.outcome, tt.kind)
			}
			ran := tt.outcome == AuditCompleted
			if (end.ExitCode != nil) != ran || (eng.runs() == 1) != ran {
				t.Errorf("exit code %v, %d runs", end.ExitCode, eng.runs())
			}
			if !ran && end.Error == "" {
				t.Error("a refusal with no error text")
			}
		})
	}

	t.Run("the Runefile changed", func(t *testing.T) {
		eng, rec := applyEngine(), newRecorder()
		var srv *Server
		who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			srv.Replace(eng, 0)
			return &mcp.ElicitResult{Action: "accept", Content: accept}, nil
		}}
		srv = New(eng, askOptions(func(o *Options) { o.Audit = rec }))
		callTool(t, connectClient(t, srv, who.options()), "apply", map[string]any{"env": "prod"})
		_, end := rec.only(t)
		if end.Approval == nil || end.Approval.Decision != AuditDecisionChanged || end.Outcome != AuditRefused || end.ExitCode != nil {
			t.Errorf("approval %+v, outcome %q", end.Approval, end.Outcome)
		}
	})

	t.Run("the call was cancelled while the request was open", func(t *testing.T) {
		eng, rec := applyEngine(), newRecorder()
		shown := make(chan struct{})
		who := &person{answer: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			close(shown)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.Audit = rec })), who.options())
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-shown
			cancel()
		}()
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}}); err == nil {
			t.Fatal("the cancelled call returned no error")
		}
		_, end := rec.only(t)
		if end.Approval == nil || end.Approval.Decision != AuditDecisionCancelled || !end.Approval.Asked {
			t.Errorf("approval %+v", end.Approval)
		}
		if end.Outcome != AuditCancelled || end.ExitCode != nil || eng.runs() != 0 {
			t.Errorf("outcome %q, exit %v, %d runs", end.Outcome, end.ExitCode, eng.runs())
		}
	})

	t.Run("a server that asks nobody", func(t *testing.T) {
		eng, rec := applyEngine(), newRecorder()
		cs := connectClient(t, New(eng, Options{Audit: rec}), approving().options())
		callTool(t, cs, "apply", map[string]any{"env": "prod"})
		_, end := rec.only(t)
		if end.Approval != nil || end.Outcome != AuditRefused || end.ErrorKind != AuditKindPolicy {
			t.Errorf("approval %+v, outcome %q, kind %q", end.Approval, end.Outcome, end.ErrorKind)
		}
	})
}

// Spec 052 with spec 051: a task that applies a plan records why nobody
// could be asked.
func TestAuditPlanApprovalCannotAsk(t *testing.T) {
	plan := samplePlan()
	newEngine := func(p *PlanApproval) Engine {
		eng := planServerEngine()
		eng.plan = p
		return eng
	}

	tests := []struct {
		name     string
		options  Options
		client   *mcp.ClientOptions
		plan     *PlanApproval
		decision string
		kind     string
		outcome  string
	}{
		{"nobody is asked on this server", Options{}, approving().options(), plan, AuditDecisionCannotAsk, AuditKindPolicy, AuditRefused},
		{"the client cannot show a form", Options{AskApproval: true}, nil, plan, AuditDecisionCannotAsk, AuditKindPolicy, AuditRefused},
		{"the engine reported no plan", Options{AskApproval: true}, approving().options(), nil, "", AuditKindInternal, AuditError},
		{"approved", Options{AskApproval: true}, approving().options(), plan, AuditDecisionApproved, "", AuditCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder()
			tt.options.Audit = rec
			cs := connectClient(t, New(newEngine(tt.plan), tt.options), tt.client)
			callTool(t, cs, "apply", map[string]any{"env": "prod"})
			_, end := rec.only(t)
			if end.Outcome != tt.outcome || end.ErrorKind != tt.kind {
				t.Errorf("outcome %q kind %q", end.Outcome, end.ErrorKind)
			}
			switch {
			case tt.decision == "" && end.Approval != nil:
				t.Errorf("approval %+v, want none", end.Approval)
			case tt.decision != "" && (end.Approval == nil || end.Approval.Decision != tt.decision):
				t.Errorf("approval %+v, want %q", end.Approval, tt.decision)
			}
		})
	}
}
