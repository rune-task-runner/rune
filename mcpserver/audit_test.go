package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 052, issue #50: a server with an Auditor reports every request it
// handles, a tool call before anything of it runs and again when it ends.

// recorder is an Auditor that keeps what it is told.
type recorder struct {
	startErr, endErr error
	// onStart, when set, runs inside CallStarted.
	onStart func(AuditCall)

	mu       sync.Mutex
	events   []string // "start:<id>" and "end:<id>", in order
	starts   []AuditCall
	ends     map[string]AuditEnd
	requests []AuditRequest
	failures []AuditAuthFailure
	// ended is signalled for each CallEnded.
	ended chan string
}

func newRecorder() *recorder {
	return &recorder{ends: map[string]AuditEnd{}, ended: make(chan string, 256)}
}

func (r *recorder) CallStarted(_ context.Context, call AuditCall) error {
	if r.onStart != nil {
		r.onStart(call)
	}
	if r.startErr != nil {
		return r.startErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "start:"+call.ID)
	r.starts = append(r.starts, call)
	return nil
}

func (r *recorder) CallEnded(_ context.Context, call AuditCall, end AuditEnd) error {
	r.mu.Lock()
	r.events = append(r.events, "end:"+call.ID)
	r.ends[call.ID] = end
	r.mu.Unlock()
	r.ended <- call.ID
	return r.endErr
}

func (r *recorder) Request(_ context.Context, req AuditRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

func (r *recorder) AuthFailed(_ context.Context, failure AuditAuthFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, failure)
}

func (r *recorder) calls() []AuditCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.starts)
}

func (r *recorder) authFailures() []AuditAuthFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.failures)
}

func (r *recorder) methods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, req := range r.requests {
		out = append(out, req.Method)
	}
	return out
}

// only returns the single call the recorder was told about, with its end.
func (r *recorder) only(t *testing.T) (AuditCall, AuditEnd) {
	t.Helper()
	r.waitEnd(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.starts) != 1 {
		t.Fatalf("%d calls recorded, want 1", len(r.starts))
	}
	end, ok := r.ends[r.starts[0].ID]
	if !ok {
		t.Fatal("the call has no end")
	}
	return r.starts[0], end
}

func (r *recorder) waitEnd(t *testing.T) {
	t.Helper()
	select {
	case <-r.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("no call ended")
	}
}

// auditEngine is an Engine that states an identity and masks one secret.
type auditEngine struct {
	tasks  []TaskInfo
	result Result
	err    error
	ident  AuditIdentity
	secret string
	// run, when set, replaces the fixed result.
	run   func(ctx context.Context, name string, args map[string]string) (Result, error)
	calls atomic.Int32
}

func (e *auditEngine) Tasks() []TaskInfo { return e.tasks }

func (e *auditEngine) Call(ctx context.Context, name string, args map[string]string) (Result, error) {
	e.calls.Add(1)
	if e.run != nil {
		return e.run(ctx, name, args)
	}
	return e.result, e.err
}

func (e *auditEngine) AuditIdentity(context.Context) AuditIdentity { return e.ident }

func (e *auditEngine) Redact(s string) string {
	if e.secret == "" {
		return s
	}
	return strings.ReplaceAll(s, e.secret, "***")
}

const auditSecret = "s3cr3t-fixture-value"

func buildEngine() *auditEngine {
	return &auditEngine{
		tasks: []TaskInfo{
			{Name: "build", Params: []ParamInfo{{Name: "target"}}},
			{Name: "deploy", Params: []ParamInfo{{Name: "env", Kind: "enum", Enum: []string{"staging", "prod"}}, {Name: "token"}}},
			{Name: "clean", Destructive: true},
			{Name: "mod::lint"},
		},
		result: Result{Stdout: "ok\n"},
		ident:  AuditIdentity{Runefile: "/repo/Runefile", DefinitionDigest: "aa", ConfigDigest: "bb", GitState: "commit", GitCommit: "c0ffee"},
		secret: auditSecret,
	}
}

func isHexID(id string) bool {
	return len(id) == 16 && strings.Trim(id, "0123456789abcdef") == ""
}

func TestNoAuditorChangesNothing(t *testing.T) {
	plain, audited := buildEngine(), buildEngine()
	plain.result.ExitCode, audited.result.ExitCode = 2, 2
	seen := make(chan *CallAudit, 1)
	plain.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
		seen <- AuditFromContext(ctx)
		return plain.result, nil
	}
	a := connect(t, New(plain, Options{}))
	b := connect(t, New(audited, Options{Audit: newRecorder()}))

	args := map[string]any{"target": "release"}
	resA, resB := callTool(t, a, "build", args), callTool(t, b, "build", args)
	if got := <-seen; got != nil {
		t.Errorf("a server without an Auditor put a record in the context: %+v", got)
	}
	jsonOf := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if jsonOf(resA) != jsonOf(resB) {
		t.Errorf("results differ:\n%s\n%s", jsonOf(resA), jsonOf(resB))
	}
	listA, err := a.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listB, err := b.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(listA) != jsonOf(listB) {
		t.Error("tool lists differ")
	}
}

func TestAuditCompletedAndFailed(t *testing.T) {
	for _, tt := range []struct {
		exit    int
		outcome string
	}{{0, AuditCompleted}, {3, AuditFailed}} {
		t.Run(tt.outcome, func(t *testing.T) {
			eng, rec := buildEngine(), newRecorder()
			eng.result.ExitCode = tt.exit
			var idInEngine string
			var startsBeforeRun int
			eng.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
				idInEngine = AuditFromContext(ctx).ID()
				startsBeforeRun = len(rec.calls())
				time.Sleep(time.Millisecond)
				return eng.result, nil
			}
			cs := connect(t, New(eng, Options{Audit: rec}))
			callTool(t, cs, "build", map[string]any{"target": "release"})

			call, end := rec.only(t)
			if startsBeforeRun != 1 {
				t.Errorf("%d starts were recorded when the engine ran, want 1", startsBeforeRun)
			}
			if !isHexID(call.ID) || call.ID != idInEngine {
				t.Errorf("id %q, in the engine %q", call.ID, idInEngine)
			}
			if call.Tool != "build" || call.Task != "build" {
				t.Errorf("tool %q, task %q", call.Tool, call.Task)
			}
			if call.Session.ClientName != "test" || call.Session.ClientVersion != "v1" {
				t.Errorf("client %+v", call.Session)
			}
			if call.Session.Transport != "stdio" || call.Session.Auth != "process" || !isHexID(call.Session.ID) {
				t.Errorf("session %+v", call.Session)
			}
			if call.Identity != eng.ident {
				t.Errorf("identity %+v", call.Identity)
			}
			if !reflect.DeepEqual(call.Supplied, map[string]any{"target": "release"}) {
				t.Errorf("supplied %#v", call.Supplied)
			}
			if end.Outcome != tt.outcome || end.ExitCode == nil || *end.ExitCode != tt.exit {
				t.Errorf("outcome %q, exit %v", end.Outcome, end.ExitCode)
			}
			if end.ErrorKind != "" || end.Error != "" || end.Approval != nil || end.Grants != nil || end.Plan != nil {
				t.Errorf("a run has no error, approval, grant or plan: %+v", end)
			}
			if end.Duration <= 0 || !end.Time.After(call.Start) {
				t.Errorf("duration %s, end %s, start %s", end.Duration, end.Time, call.Start)
			}
		})
	}
}

func TestAuditNamespacedTool(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	cs := connect(t, New(eng, Options{Audit: rec}))
	callTool(t, cs, "mod__lint", nil)
	call, _ := rec.only(t)
	if call.Tool != "mod__lint" || call.Task != "mod::lint" || call.Supplied != nil {
		t.Errorf("tool %q, task %q, supplied %#v", call.Tool, call.Task, call.Supplied)
	}
}

func TestAuditRequests(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	cs := connect(t, New(eng, Options{Audit: rec}))
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: auditSecret}); err == nil {
		t.Fatal("a prompt that does not exist was returned")
	}
	methods := rec.methods()
	for _, want := range []string{"initialize", "tools/list", "prompts/get"} {
		if !slices.Contains(methods, want) {
			t.Errorf("no record of %s in %v", want, methods)
		}
	}
	if len(rec.calls()) != 0 {
		t.Error("a request was recorded as a call")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, req := range rec.requests {
		if req.Time.IsZero() || req.Duration < 0 || !isHexID(req.Session.ID) {
			t.Errorf("%s: %+v", req.Method, req)
		}
		switch req.Method {
		case "initialize":
			if req.Session.ClientName != "test" || req.Failed {
				t.Errorf("initialize: %+v", req)
			}
		case "prompts/get":
			if !req.Failed || req.Error == "" || strings.Contains(req.Error, auditSecret) {
				t.Errorf("a failed request: %+v", req)
			}
		case "tools/list":
			if req.Failed || req.Error != "" {
				t.Errorf("tools/list: %+v", req)
			}
		}
	}
}

func TestAuditIDsAndSession(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	srv := New(eng, Options{Audit: rec})
	cs := connect(t, srv)
	callTool(t, cs, "build", nil)
	rec.waitEnd(t)
	callTool(t, cs, "build", nil)
	rec.waitEnd(t)
	calls := rec.calls()
	if len(calls) != 2 || calls[0].ID == calls[1].ID {
		t.Fatalf("calls %+v", calls)
	}
	if calls[0].Session.ID != calls[1].Session.ID {
		t.Error("two calls of one session have two session ids")
	}
	other := newRecorder()
	cs2 := connect(t, New(buildEngine(), Options{Audit: other}))
	callTool(t, cs2, "build", nil)
	otherCall, _ := other.only(t)
	if otherCall.Session.ID == calls[0].Session.ID {
		t.Error("two servers share a session id")
	}
}

func TestSessionID(t *testing.T) {
	a := sessionID("instance", "protocol")
	if !isHexID(a) || strings.Contains(a, "protocol") {
		t.Errorf("id %q", a)
	}
	if a != sessionID("instance", "protocol") {
		t.Error("not stable")
	}
	if a == sessionID("instance", "other") || a == sessionID("other", "protocol") || a == sessionID("instance", "") {
		t.Error("ids collide")
	}
	// The separator keeps ("ab", "c") and ("a", "bc") apart.
	if sessionID("ab", "c") == sessionID("a", "bc") {
		t.Error("the parts run together")
	}
}

func TestNewAuditID(t *testing.T) {
	if a, b := newAuditID(), newAuditID(); !isHexID(a) || a == b {
		t.Errorf("ids %q, %q", a, b)
	}
}

// The handler runs against the snapshot the start was reported for, also
// when the server's tasks are replaced in between.
func TestAuditSnapshotOfTheStart(t *testing.T) {
	old, next := buildEngine(), buildEngine()
	next.ident.DefinitionDigest = "cc"
	next.result.Stdout = "new\n"
	rec := newRecorder()
	srv := New(old, Options{Audit: rec, Reloadable: true})
	rec.onStart = func(AuditCall) { srv.Replace(next, 0) }
	cs := connect(t, srv)
	res := callTool(t, cs, "build", nil)
	call, _ := rec.only(t)
	if call.Identity.DefinitionDigest != "aa" {
		t.Errorf("the record names digest %q, want the old load", call.Identity.DefinitionDigest)
	}
	if old.calls.Load() != 1 || next.calls.Load() != 0 {
		t.Errorf("old engine ran %d times, new one %d", old.calls.Load(), next.calls.Load())
	}
	if !strings.HasPrefix(contentText(res), "ok\n") {
		t.Errorf("result %q", contentText(res))
	}
	rec.onStart = nil
	callTool(t, cs, "build", nil)
	rec.waitEnd(t)
	if calls := rec.calls(); calls[1].Identity.DefinitionDigest != "cc" {
		t.Errorf("the next call names digest %q, want the new load", calls[1].Identity.DefinitionDigest)
	}
}

func TestAuditEffectiveArguments(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	eng.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
		AuditFromContext(ctx).SetEffective([]AuditArg{
			{Name: "target", Values: []string{"debug"}},
			{Name: "files", Values: []string{"a b", "c"}, Variadic: true},
		})
		return eng.result, nil
	}
	cs := connect(t, New(eng, Options{Audit: rec}))
	callTool(t, cs, "build", nil)
	call, end := rec.only(t)
	if call.Supplied != nil {
		t.Errorf("supplied %#v for a call with no arguments", call.Supplied)
	}
	want := []AuditArg{{Name: "target", Values: []string{"debug"}}, {Name: "files", Values: []string{"a b", "c"}, Variadic: true}}
	if !reflect.DeepEqual(end.Effective, want) {
		t.Errorf("effective %+v", end.Effective)
	}
}

func TestAuditOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		args    any
		engine  func(*auditEngine)
		outcome string
		kind    string
		task    string
		errText string // a part of the recorded error
		callErr bool   // the client gets a protocol error
	}{
		{name: "unknown tool", tool: "nope", outcome: AuditUnknownTool, kind: AuditKindProtocol, errText: "nope", callErr: true},
		{name: "arguments that are not an object", tool: "build", args: "a string", outcome: AuditInvalidArguments, kind: AuditKindValidation, task: "build", errText: "invalid arguments"},
		{name: "an undeclared name", tool: "build", args: map[string]any{"colour": "red"}, outcome: AuditInvalidArguments, kind: AuditKindValidation, task: "build", errText: `unknown argument "colour"`},
		{name: "a value outside the enum", tool: "deploy", args: map[string]any{"env": "qa"}, outcome: AuditInvalidArguments, kind: AuditKindValidation, task: "deploy", errText: `invalid value "qa"`},
		{name: "a destructive task nobody granted", tool: "clean", outcome: AuditRefused, kind: AuditKindPolicy, task: "clean", errText: "destructive"},
		{
			name: "the engine refuses the arguments", tool: "build",
			engine: func(e *auditEngine) {
				e.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
					AuditFromContext(ctx).SetErrorKind(AuditKindValidation)
					return Result{}, errors.New("missing required argument")
				}
			},
			outcome: AuditInvalidArguments, kind: AuditKindValidation, task: "build", errText: "missing required argument",
		},
		{
			name: "the engine's policy refuses", tool: "build",
			engine: func(e *auditEngine) {
				e.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
					audit := AuditFromContext(ctx)
					audit.SetErrorKind(AuditKindPolicy)
					audit.SetErrorKind(AuditKindValidation) // the first kind stays
					return Result{}, errors.New("agents may not pass " + auditSecret)
				}
			},
			outcome: AuditRefused, kind: AuditKindPolicy, task: "build", errText: "agents may not pass ***",
		},
		{
			name: "an engine error nobody marked", tool: "build",
			engine:  func(e *auditEngine) { e.err = errors.New("boom") },
			outcome: AuditError, kind: AuditKindInternal, task: "build", errText: "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, rec := buildEngine(), newRecorder()
			if tt.engine != nil {
				tt.engine(eng)
			}
			cs := connect(t, New(eng, Options{Audit: rec}))
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if (err != nil) != tt.callErr {
				t.Fatalf("call error %v", err)
			}
			if err == nil && !res.IsError {
				t.Error("the result is not an error")
			}
			call, end := rec.only(t)
			if end.Outcome != tt.outcome || end.ErrorKind != tt.kind {
				t.Errorf("outcome %q kind %q, want %q %q", end.Outcome, end.ErrorKind, tt.outcome, tt.kind)
			}
			if call.Task != tt.task || call.Tool != tt.tool {
				t.Errorf("tool %q task %q", call.Tool, call.Task)
			}
			if end.ExitCode != nil {
				t.Errorf("a call that ran nothing has exit code %d", *end.ExitCode)
			}
			if !strings.Contains(end.Error, tt.errText) || strings.Contains(end.Error, auditSecret) {
				t.Errorf("error %q, want it to hold %q", end.Error, tt.errText)
			}
			if end.Effective != nil || end.Grants != nil {
				t.Errorf("effective %v grants %v", end.Effective, end.Grants)
			}
		})
	}
}

func TestAuditCancelledCall(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	running := make(chan struct{})
	eng.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
		close(running)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}
	cs := connect(t, New(eng, Options{Audit: rec}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "build"})
		done <- err
	}()
	<-running
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the cancelled call returned no error")
	}
	_, end := rec.only(t)
	if end.Outcome != AuditCancelled || end.ExitCode != nil {
		t.Errorf("outcome %q, exit %v", end.Outcome, end.ExitCode)
	}
}

func TestCallAuditEnd(t *testing.T) {
	exit := func(n int) *int { return &n }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	live := context.Background()

	tests := []struct {
		name    string
		ctx     context.Context
		rec     *CallAudit
		callErr error
		outcome string
		kind    string
	}{
		{"exit 0", live, &CallAudit{exit: exit(0)}, nil, AuditCompleted, ""},
		{"exit 1", live, &CallAudit{exit: exit(1)}, nil, AuditFailed, ""},
		{"a kind recorded before a run is dropped", live, &CallAudit{exit: exit(0), kind: AuditKindPolicy}, nil, AuditCompleted, ""},
		{"unknown tool", live, &CallAudit{unknown: true}, errors.New("unknown tool"), AuditUnknownTool, AuditKindProtocol},
		{"validation", live, &CallAudit{kind: AuditKindValidation}, nil, AuditInvalidArguments, AuditKindValidation},
		{"policy", live, &CallAudit{kind: AuditKindPolicy}, nil, AuditRefused, AuditKindPolicy},
		{"approval declined", live, &CallAudit{kind: AuditKindApproval, approval: &AuditApproval{Asked: true, Decision: AuditDecisionDeclined}}, nil, AuditRefused, AuditKindApproval},
		{"approval with no decision", live, &CallAudit{kind: AuditKindApproval}, nil, AuditRefused, AuditKindApproval},
		{"approval timed out", live, &CallAudit{kind: AuditKindApproval, approval: &AuditApproval{Asked: true, Decision: AuditDecisionTimedOut}}, nil, AuditTimedOut, AuditKindApproval},
		{"approval cancelled", live, &CallAudit{kind: AuditKindApproval, approval: &AuditApproval{Asked: true, Decision: AuditDecisionCancelled}}, nil, AuditCancelled, AuditKindApproval},
		{"a protocol error for a known tool", live, &CallAudit{}, errors.New("bad"), AuditError, AuditKindProtocol},
		{"nothing recorded", live, &CallAudit{}, nil, AuditError, AuditKindInternal},
		{"a kind this version does not derive an outcome from", live, &CallAudit{kind: "other"}, nil, AuditError, "other"},
		{"the context was cancelled", cancelled, &CallAudit{kind: AuditKindPolicy}, nil, AuditCancelled, AuditKindPolicy},
		{"the deadline passed", expired, &CallAudit{exit: exit(0)}, nil, AuditTimedOut, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now().Add(-time.Second)
			end := tt.rec.end(tt.ctx, start, tt.callErr)
			if end.Outcome != tt.outcome || end.ErrorKind != tt.kind {
				t.Errorf("outcome %q kind %q, want %q %q", end.Outcome, end.ErrorKind, tt.outcome, tt.kind)
			}
			if end.Duration < time.Second || end.Time.Before(start) {
				t.Errorf("duration %s", end.Duration)
			}
			if tt.callErr != nil && end.Error != tt.callErr.Error() {
				t.Errorf("error %q", end.Error)
			}
		})
	}
}

func TestCallAuditNilReceiver(t *testing.T) {
	var c *CallAudit
	if c.ID() != "" {
		t.Error("an id on nil")
	}
	c.SetEffective([]AuditArg{{Name: "a"}})
	c.SetErrorKind(AuditKindPolicy)
	c.SetPlan(AuditPlanStored, "id", "digest")
	c.fail(AuditKindPolicy, "text")
	c.ran(0, nil)
	c.decided(true, AuditDecisionApproved, nil)
	if AuditFromContext(context.Background()) != nil {
		t.Error("a record in an empty context")
	}
	if snapshotFrom(context.Background()) != nil {
		t.Error("a snapshot in an empty context")
	}
}

func TestCallAuditSetPlan(t *testing.T) {
	c := &CallAudit{exit: new(int)}
	c.SetPlan(AuditPlanApplied, "7f3c", "9b1f")
	end := c.end(context.Background(), time.Now(), nil)
	if end.Plan == nil || *end.Plan != (AuditPlan{Role: AuditPlanApplied, ID: "7f3c", Digest: "9b1f"}) {
		t.Errorf("plan %+v", end.Plan)
	}
}

func TestAuditStartFailureRefuses(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	rec.startErr = errors.New("audit log: this call was not run")
	cs := connect(t, New(eng, Options{Audit: rec}))
	res := callTool(t, cs, "build", nil)
	if !res.IsError || contentText(res) != "audit log: this call was not run" || len(res.Content) != 1 {
		t.Errorf("result %q, error %v", contentText(res), res.IsError)
	}
	if res.StructuredContent != nil {
		t.Errorf("a refused call has structured content: %v", res.StructuredContent)
	}
	if eng.calls.Load() != 0 {
		t.Error("the engine ran")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.ends) != 0 {
		t.Error("a call that never started was ended")
	}
}

func TestAuditEndFailureKeepsResult(t *testing.T) {
	for _, exit := range []int{0, 2} {
		t.Run(fmt.Sprintf("exit %d", exit), func(t *testing.T) {
			plain, audited := buildEngine(), buildEngine()
			plain.result = Result{Stdout: "out\n", Stderr: "err\n", ExitCode: exit}
			audited.result = plain.result
			rec := newRecorder()
			rec.endErr = errors.New("[audit log] The completion of this call was not recorded")
			want := callTool(t, connect(t, New(plain, Options{})), "build", nil)
			got := callTool(t, connect(t, New(audited, Options{Audit: rec})), "build", nil)

			if got.IsError != want.IsError || got.IsError != (exit != 0) {
				t.Errorf("error flag %v, want %v", got.IsError, want.IsError)
			}
			if !reflect.DeepEqual(got.StructuredContent, want.StructuredContent) {
				t.Errorf("structured content changed: %v", got.StructuredContent)
			}
			if len(got.Content) != 2 {
				t.Fatalf("%d content blocks, want 2", len(got.Content))
			}
			first, _ := got.Content[0].(*mcp.TextContent)
			second, _ := got.Content[1].(*mcp.TextContent)
			if first == nil || first.Text != contentText(want) {
				t.Errorf("the first block changed: %+v", first)
			}
			if second == nil || second.Text != rec.endErr.Error() {
				t.Errorf("the second block: %+v", second)
			}
			if audited.calls.Load() != 1 {
				t.Errorf("the engine ran %d times", audited.calls.Load())
			}
		})
	}
}

func TestAuditEndFailureOnProtocolError(t *testing.T) {
	rec := newRecorder()
	rec.endErr = errors.New("not recorded")
	cs := connect(t, New(buildEngine(), Options{Audit: rec}))
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nope"}); err == nil {
		t.Fatal("an unknown tool was called")
	}
	rec.waitEnd(t)
}

func TestAuditFailingAuditorServesRequests(t *testing.T) {
	rec := newRecorder()
	rec.startErr, rec.endErr = errors.New("no start"), errors.New("no end")
	audited := connect(t, New(buildEngine(), Options{Audit: rec}))
	plain := connect(t, New(buildEngine(), Options{}))
	a, err := audited.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := plain.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tools) != len(b.Tools) || len(a.Tools) == 0 {
		t.Errorf("%d tools with a failing auditor, %d without", len(a.Tools), len(b.Tools))
	}
	if audited.InitializeResult().ServerInfo.Name != plain.InitializeResult().ServerInfo.Name {
		t.Error("initialize differs")
	}
}

func TestRedactSupplied(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, auditSecret, "***") }
	long := strings.Repeat("é", 600) // 1200 bytes; byte 1024 is a character start, so one more is kept

	names := map[string]any{}
	for i := range 100 {
		names[fmt.Sprintf("n%03d", i)] = i
	}
	var elements []any
	for i := range 100 {
		elements = append(elements, fmt.Sprint(i))
	}
	deep := any("leaf")
	for range 9 {
		deep = map[string]any{"in": deep}
	}
	raw, err := json.Marshal(map[string]any{
		"plain":          "value",
		"top":            auditSecret,
		"list":           []any{"a", auditSecret},
		"nested":         map[string]any{"inner": "x" + auditSecret},
		auditSecret:      "a secret as a name",
		"token":          "t0k",
		"Password":       12345,
		"api_key":        []any{"k1", "k2"},
		"credentialFile": map[string]any{"path": "/x"},
		"long":           long,
		"names":          names,
		"elements":       elements,
		"deep":           deep,
		"number":         json.Number("1000000"),
		"flag":           true,
		"nothing":        nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := redactSupplied(raw, redact).(map[string]any)
	if !ok {
		t.Fatal("not an object")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), auditSecret) {
		t.Errorf("the secret is in %s", encoded)
	}
	for name, want := range map[string]any{
		"plain":          "value",
		"top":            "***",
		"***":            "a secret as a name",
		"token":          "***",
		"Password":       "***",
		"api_key":        "***",
		"credentialFile": "***",
		"number":         json.Number("1000000"),
		"flag":           true,
		"nothing":        nil,
	} {
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s = %#v, want %#v", name, got[name], want)
		}
	}
	if !reflect.DeepEqual(got["list"], []any{"a", "***"}) {
		t.Errorf("list = %#v", got["list"])
	}
	if !reflect.DeepEqual(got["nested"], map[string]any{"inner": "x***"}) {
		t.Errorf("nested = %#v", got["nested"])
	}
	cut, _ := got["long"].(string)
	if !strings.HasSuffix(cut, "[176 bytes cut]") || len(strings.TrimSuffix(cut, "[176 bytes cut]")) != 1024 {
		t.Errorf("long value kept %d bytes: ...%q", len(cut), cut[max(0, len(cut)-30):])
	}
	listed, _ := got["names"].(map[string]any)
	if len(listed) != 65 || listed["[more]"] != 36 || listed["n000"] == nil || listed["n064"] != nil {
		t.Errorf("names: %d entries, more %v", len(listed), listed["[more]"])
	}
	shown, _ := got["elements"].([]any)
	if len(shown) != 65 || shown[64] != "[36 more]" || shown[0] != "0" {
		t.Errorf("elements: %d entries, last %v", len(shown), shown[len(shown)-1])
	}
	level := got["deep"]
	for i := 2; i <= 8; i++ {
		m, ok := level.(map[string]any)
		if !ok {
			t.Fatalf("level %d is %#v", i, level)
		}
		level = m["in"]
	}
	if level != "[nested]" {
		t.Errorf("level 9 is %#v, want the mark", level)
	}
}

func TestRedactSuppliedShapes(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, auditSecret, "***") }
	tests := []struct {
		raw  string
		want any
	}{
		{"", nil},
		{"   ", nil},
		{"null", nil},
		{`"x` + auditSecret + `"`, "x***"},
		{`[1,"a"]`, []any{json.Number("1"), "a"}},
		{`12345`, json.Number("12345")},
		{`{`, "[not JSON]"},
	}
	for _, tt := range tests {
		if got := redactSupplied(json.RawMessage(tt.raw), redact); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("redactSupplied(%q) = %#v, want %#v", tt.raw, got, tt.want)
		}
	}
	// A secret that is all digits is masked as a number too.
	digits := func(s string) string { return strings.ReplaceAll(s, "424242", "***") }
	if got := redactSupplied(json.RawMessage(`{"pin":424242}`), digits); !reflect.DeepEqual(got, map[string]any{"pin": "***"}) {
		t.Errorf("a secret number: %#v", got)
	}
}

func TestRedactText(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, auditSecret, "***") }
	if got := redactText("x "+auditSecret, redact, 100); got != "x ***" {
		t.Errorf("got %q", got)
	}
	if got := redactText("unchanged", nil, 100); got != "unchanged" {
		t.Errorf("a nil redactor: %q", got)
	}
	got := redactText(strings.Repeat("a", 3000), redact, maxAuditErrorBytes)
	if got != strings.Repeat("a", 2048)+"[952 bytes cut]" {
		t.Errorf("cut text ends %q", got[len(got)-20:])
	}
}

func TestCutValue(t *testing.T) {
	if got := cutValue("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := cutValue("ééé", 3); got != "é[4 bytes cut]" {
		t.Errorf("got %q", got)
	}
}

func TestBoundValues(t *testing.T) {
	many := make([]string, 70)
	for i := range many {
		many[i] = "v"
	}
	got := boundValues(AuditArg{Name: "files", Values: many})
	if len(got) != 65 || got[64] != "[6 more]" {
		t.Errorf("%d values, last %q", len(got), got[len(got)-1])
	}
	if got := boundValues(AuditArg{Name: "apiToken", Values: []string{"a", "b"}}); !slices.Equal(got, []string{"***", "***"}) {
		t.Errorf("a secret-named parameter: %v", got)
	}
	if got := boundValues(AuditArg{Name: "x", Values: []string{strings.Repeat("a", 2000)}}); !strings.HasSuffix(got[0], "[976 bytes cut]") {
		t.Errorf("a long value: ...%q", got[0][len(got[0])-20:])
	}
	if got := boundValues(AuditArg{Name: "x"}); len(got) != 0 {
		t.Errorf("no values: %v", got)
	}
}

func TestNamedSecrets(t *testing.T) {
	raw := json.RawMessage(`{"env":"prod","token":"t0k","secret":4242,"auth":{"user":"u","keys":["k1","k2"],"on":true},"list":[{"passwd":"p\tw"}],"apiKey":""}`)
	got := namedSecrets(raw)
	slices.Sort(got)
	want := []string{"4242", "k1", "k2", "p\tw", `p\tw`, "t0k", "u"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, raw := range []string{"", "null", `"token"`, `{"env":"prod"}`, "{"} {
		if got := namedSecrets(json.RawMessage(raw)); len(got) != 0 {
			t.Errorf("namedSecrets(%q) = %q", raw, got)
		}
	}
}

func TestAppendHidden(t *testing.T) {
	if got := appendHidden(nil, ""); got != nil {
		t.Errorf("an empty value: %q", got)
	}
	if got := appendHidden([]string{"a"}, "plain"); !slices.Equal(got, []string{"a", "plain"}) {
		t.Errorf("a plain value: %q", got)
	}
	// The quoted form is first: the plain one would match only its start.
	if got := appendHidden(nil, `a"b`); !slices.Equal(got, []string{`a\"b`, `a"b`}) {
		t.Errorf("a value with a quote: %q", got)
	}
}

func TestHideValue(t *testing.T) {
	long := "0123456789abcdef"
	tests := []struct {
		name, text, value, want string
	}{
		{"an empty value", "text", "", "text"},
		{"no match", "text", "value", "text"},
		{"a whole value, twice", `"t0k1" and t0k1.`, "t0k1", `"***" and ***.`},
		{"a value is not taken by a short start", "t0k t0k1", "t0k1", "t0k ***"},
		{"a very short value is hidden between quotes only", `invalid value "1" (allowed: "10", "11"), 1 of 1`, "1", `invalid value "***" (allowed: "10", "11"), 1 of 1`},
		{"a long value that the text cut", `with "0123456789ab"... for x`, long, `with "***"... for x`},
		{"a start shorter than the least is kept", "0123456 x", long, "0123456 x"},
		{"a long value whole and cut", long + " " + long[:8], long, "*** ***"},
		{"a character matched in part goes whole", "x ééééè y", "ééééê", "x *** y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hideValue(tt.text, tt.value); got != tt.want {
				t.Errorf("hideValue(%q, %q) = %q, want %q", tt.text, tt.value, got, tt.want)
			}
		})
	}
}

// A refusal quotes the value it refused. The record holds that text without
// the values under secret-like names, supplied or bound.
func TestCallAuditErrorText(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, auditSecret, "***") }
	c := &CallAudit{redact: redact, named: namedSecrets(json.RawMessage(`{"token":"a\"long-supplied-value","env":"prod"}`))}
	c.SetEffective([]AuditArg{{Name: "env", Values: []string{"prod"}}, {Name: "passwd", Values: []string{"bound-default"}}})

	c.fail(AuditKindValidation, `invalid value "a\"long-supplied-value" for "token", env "prod", passwd "bound-default", `+auditSecret)
	end := c.end(context.Background(), time.Now(), nil)
	if want := `invalid value "***" for "token", env "prod", passwd "***", ***`; end.Error != want {
		t.Errorf("a failure\n got %q\nwant %q", end.Error, want)
	}

	// The text of a protocol error is the agent's too.
	c = &CallAudit{named: namedSecrets(json.RawMessage(`{"token":"t0k"}`))}
	if got := c.end(context.Background(), time.Now(), errors.New(`bad "t0k"`)).Error; got != `bad "***"` {
		t.Errorf("a protocol error: %q", got)
	}

	// The value is hidden whole before the mask can change a part of it.
	c = &CallAudit{redact: redact, named: []string{"pre-" + auditSecret + "-post"}}
	c.fail("", "value pre-"+auditSecret+"-post.")
	if got := c.end(context.Background(), time.Now(), nil).Error; got != "value ***." {
		t.Errorf("a value that holds a masked secret: %q", got)
	}
}

func TestAuditRedactsWhatItReports(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	eng.run = func(ctx context.Context, _ string, _ map[string]string) (Result, error) {
		// The engine forgot to mask: the name rule still hides the value.
		AuditFromContext(ctx).SetEffective([]AuditArg{{Name: "env", Values: []string{"staging"}}, {Name: "token", Values: []string{"t0k"}}})
		return eng.result, nil
	}
	srv := New(eng, Options{Audit: rec})
	client := mcp.NewClient(&mcp.Implementation{Name: "client-" + auditSecret, Version: "v-" + auditSecret}, nil)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	callTool(t, cs, "deploy", map[string]any{"env": "staging", "token": auditSecret})
	call, end := rec.only(t)
	if call.Session.ClientName != "client-***" || call.Session.ClientVersion != "v-***" {
		t.Errorf("client %+v", call.Session)
	}
	if !reflect.DeepEqual(call.Supplied, map[string]any{"env": "staging", "token": "***"}) {
		t.Errorf("supplied %#v", call.Supplied)
	}
	want := []AuditArg{{Name: "env", Values: []string{"staging"}}, {Name: "token", Values: []string{"***"}}}
	if !reflect.DeepEqual(end.Effective, want) {
		t.Errorf("effective %+v", end.Effective)
	}

	// A tool name is the agent's text too.
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "x" + auditSecret}); err == nil {
		t.Fatal("an unknown tool was called")
	}
	rec.waitEnd(t)
	calls := rec.calls()
	if got := calls[len(calls)-1].Tool; got != "x***" {
		t.Errorf("tool %q", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := rec.ends[calls[len(calls)-1].ID].Error; strings.Contains(got, auditSecret) {
		t.Errorf("the error holds the secret: %q", got)
	}
}

// An engine that is not an AuditEngine gives no identity and masks nothing,
// and the name rule and the bounds still hold.
func TestAuditPlainEngine(t *testing.T) {
	eng := &fakeEngine{
		tasks:  []TaskInfo{{Name: "deploy", Params: []ParamInfo{{Name: "env"}, {Name: "token"}}}},
		result: Result{Stdout: "ok"},
	}
	rec := newRecorder()
	cs := connect(t, New(eng, Options{Audit: rec}))
	callTool(t, cs, "deploy", map[string]any{"env": strings.Repeat("e", 2000), "token": "t0k"})
	call, end := rec.only(t)
	if call.Identity != (AuditIdentity{}) {
		t.Errorf("identity %+v", call.Identity)
	}
	supplied, _ := call.Supplied.(map[string]any)
	if supplied["token"] != "***" {
		t.Errorf("token %#v", supplied["token"])
	}
	if env, _ := supplied["env"].(string); !strings.HasSuffix(env, "[976 bytes cut]") {
		t.Errorf("a long value was kept whole: %d bytes", len(env))
	}
	if end.Outcome != AuditCompleted {
		t.Errorf("outcome %q", end.Outcome)
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestAuditConcurrentCalls(t *testing.T) {
	eng, rec := buildEngine(), newRecorder()
	cs := connect(t, New(eng, Options{Audit: rec}))
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "build"}); err != nil {
				t.Errorf("call: %v", err)
			}
		})
	}
	wg.Wait()
	for range 50 {
		rec.waitEnd(t)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	seen := map[string]bool{}
	started := map[string]bool{}
	for _, event := range rec.events {
		kind, id, _ := strings.Cut(event, ":")
		switch kind {
		case "start":
			if seen[id] {
				t.Fatalf("id %s twice", id)
			}
			seen[id], started[id] = true, true
		case "end":
			if !started[id] {
				t.Fatalf("id %s ended before it started", id)
			}
		}
	}
	if len(seen) != 50 || len(rec.ends) != 50 {
		t.Errorf("%d starts, %d ends", len(seen), len(rec.ends))
	}
}

func TestAuditGrants(t *testing.T) {
	tasks := []TaskInfo{
		{Name: "clean", Destructive: true},
		{Name: "reset", DestructiveReach: []string{"clean", "wipe", "clean"}},
		{Name: "wipe", Destructive: true},
		{Name: "build"},
	}
	t.Run("a granted task and what it reaches", func(t *testing.T) {
		eng, rec := &auditEngine{tasks: tasks, result: Result{Stdout: "ok"}}, newRecorder()
		cs := connect(t, New(eng, Options{Audit: rec, AllowDestructive: []string{"clean", "wipe"}}))
		callTool(t, cs, "clean", nil)
		_, end := rec.only(t)
		if !slices.Equal(end.Grants, []string{"clean"}) || end.Approval != nil {
			t.Errorf("grants %v, approval %v", end.Grants, end.Approval)
		}
		callTool(t, cs, "reset", nil)
		rec.waitEnd(t)
		calls := rec.calls()
		rec.mu.Lock()
		got := rec.ends[calls[1].ID].Grants
		rec.mu.Unlock()
		if !slices.Equal(got, []string{"clean", "wipe"}) {
			t.Errorf("grants of a task that reaches two: %v", got)
		}
		callTool(t, cs, "build", nil)
		rec.waitEnd(t)
		calls = rec.calls()
		rec.mu.Lock()
		got = rec.ends[calls[2].ID].Grants
		rec.mu.Unlock()
		if got != nil {
			t.Errorf("grants of an ordinary task: %v", got)
		}
	})
	t.Run("an approved call has an approval and no grant", func(t *testing.T) {
		eng, rec := applyEngine(), newRecorder()
		cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.Audit = rec })), approving().options())
		callTool(t, cs, "apply", map[string]any{"env": "prod"})
		_, end := rec.only(t)
		if end.Grants != nil {
			t.Errorf("grants %v", end.Grants)
		}
		want := AuditApproval{Asked: true, Decision: AuditDecisionApproved, Tasks: []string{"apply"}}
		if end.Approval == nil || !reflect.DeepEqual(*end.Approval, want) {
			t.Errorf("approval %+v", end.Approval)
		}
		if end.Outcome != AuditCompleted || end.ExitCode == nil {
			t.Errorf("outcome %q", end.Outcome)
		}
	})
}

func TestGrantsOf(t *testing.T) {
	s := &Server{granted: map[string]bool{"a": true, "c": true}}
	tests := []struct {
		task TaskInfo
		want []string
	}{
		{TaskInfo{Name: "a", Destructive: true}, []string{"a"}},
		{TaskInfo{Name: "a"}, nil},
		{TaskInfo{Name: "b", Destructive: true}, nil},
		{TaskInfo{Name: "x", DestructiveReach: []string{"b", "c", "x", "c"}}, []string{"c"}},
		{TaskInfo{Name: "a", Destructive: true, DestructiveReach: []string{"a", "c"}}, []string{"a", "c"}},
	}
	for _, tt := range tests {
		if got := s.grantsOf(tt.task); !slices.Equal(got, tt.want) {
			t.Errorf("grantsOf(%+v) = %v, want %v", tt.task, got, tt.want)
		}
	}
}

func TestCallParams(t *testing.T) {
	name, args := callParams(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "build", Arguments: json.RawMessage(`{"a":1}`)}})
	if name != "build" || string(args) != `{"a":1}` {
		t.Errorf("name %q args %s", name, args)
	}
	if name, args := callParams(&mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}); name != "" || args != nil {
		t.Errorf("a request that is not a call: %q %s", name, args)
	}
}

func TestAuthLimiter(t *testing.T) {
	var l authLimiter
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	allowed := 0
	for i := range 30 {
		ok, suppressed := l.allow(now.Add(time.Duration(i) * time.Second))
		if ok {
			allowed++
		}
		if suppressed != 0 {
			t.Errorf("failure %d reports %d left out", i, suppressed)
		}
	}
	if allowed != 10 {
		t.Errorf("%d reports in one minute, want 10", allowed)
	}
	ok, suppressed := l.allow(now.Add(time.Minute))
	if !ok || suppressed != 20 {
		t.Errorf("the next minute: ok %v, %d left out, want 20", ok, suppressed)
	}
	if ok, suppressed := l.allow(now.Add(time.Minute + time.Second)); !ok || suppressed != 0 {
		t.Errorf("the count was not reset: ok %v, %d", ok, suppressed)
	}
}

func TestOutcomeDecision(t *testing.T) {
	for o, want := range map[outcome]string{
		outcomeApproved:  AuditDecisionApproved,
		outcomeNotGiven:  AuditDecisionNotGiven,
		outcomeDeclined:  AuditDecisionDeclined,
		outcomeDismissed: AuditDecisionDismissed,
		outcomeTimedOut:  AuditDecisionTimedOut,
		outcomeNoAnswer:  AuditDecisionNoAnswer,
		outcomeCancelled: AuditDecisionCancelled,
		outcomeChanged:   AuditDecisionChanged,
	} {
		if got := o.decision(); got != want {
			t.Errorf("decision of %d = %q, want %q", o, got, want)
		}
	}
}

func TestRedactorOf(t *testing.T) {
	if got := redactorOf(&fakeEngine{})("x"); got != "x" {
		t.Errorf("a plain engine changed %q", got)
	}
	if got := redactorOf(&auditEngine{secret: "x"})("axb"); got != "a***b" {
		t.Errorf("an audit engine gave %q", got)
	}
}
