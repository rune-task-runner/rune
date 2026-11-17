package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 051: a task that applies a stored plan. Every call is put to the
// person with the plan in the request, whatever the operator granted.

// planEngine is approvalEngine whose request carries a plan. refuse, when
// set, is what the engine's own checks return before anyone is asked.
type planEngine struct {
	*approvalEngine
	plan   *PlanApproval
	refuse error
}

func (e *planEngine) CallApproved(ctx context.Context, name string, args map[string]string, lists map[string][]string, tasks []string, approve func(context.Context, ApprovalRequest) error) (Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, approvedCall{name, args, lists, tasks})
	e.mu.Unlock()
	if e.refuse != nil {
		return Result{}, e.refuse
	}
	req := e.requestFor(name, args, lists, tasks)
	req.Plan = e.plan
	if err := approve(ctx, req); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	e.ran = append(e.ran, args)
	e.mu.Unlock()
	return e.result, nil
}

func samplePlan() *PlanApproval {
	return &PlanApproval{
		ID: "7f3c9a1e52b84d06", Task: "plan", Created: "2026-11-17T10:04:11Z",
		Digest: "9b1f", Size: 48213, Summary: "Plan: 2 to add, 1 to change, 0 to destroy.\n",
	}
}

func planServerEngine() *planEngine {
	return &planEngine{
		approvalEngine: &approvalEngine{
			tasks: []TaskInfo{
				{Name: "apply", AppliesPlan: "plan", Params: []ParamInfo{{Name: "env", Required: true}}},
				{Name: "plan", Params: []ParamInfo{{Name: "env", Required: true}}},
			},
			result: Result{Stdout: "applied\n"},
		},
		plan: samplePlan(),
	}
}

const (
	messagePlan = "Approve this Rune task call?\n\nTask: apply\nArguments:\n- env: \"prod\"\n" +
		"Plan: 7f3c9a1e52b84d06 made by \"plan\" at 2026-11-17T10:04:11Z\n" +
		"Artifact: sha256:9b1f (48213 bytes)\n" +
		"Summary:\n  Plan: 2 to add, 1 to change, 0 to destroy.\n" +
		"Destructive tasks it can run:\n- apply: Run \"apply\"?\n\nApproving applies this plan once."
	planNeedsApply = `task "apply" applies a plan and runs only when the person at the client approves that plan`
	operatorApply  = ` The operator can run "rune apply" themselves.`
)

func TestToolForAppliesPlan(t *testing.T) {
	tool := toolFor(TaskInfo{
		Name: "apply", Doc: "Apply the reviewed plan.", Returns: "the applied changes", AppliesPlan: "plan",
		Params: []ParamInfo{{Name: "env", AgentValues: []string{"staging"}}},
	})
	if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Error("the tool is not advertised as destructive")
	}
	want := "Apply the reviewed plan.\n\nReturns: the applied changes\n\n" +
		`Agent policy: agents may run this task only with env="staging".` + "\n\n" +
		`Plan approval: applies the plan made by task "plan". Every call asks the person at the client to approve that plan.`
	if tool.Description != want {
		t.Errorf("description = %q, want %q", tool.Description, want)
	}
	if only := toolFor(TaskInfo{Name: "apply", AppliesPlan: "plan"}); !strings.HasPrefix(only.Description, "Plan approval:") {
		t.Errorf("description with no doc = %q", only.Description)
	}

	// A task without the field is what it was: the zero value adds nothing.
	plain, _ := json.Marshal(toolFor(TaskInfo{Name: "build", Doc: "Build."}))
	if strings.Contains(string(plain), "Plan approval") || !strings.Contains(string(plain), `"destructiveHint":false`) {
		t.Errorf("tool without AppliesPlan = %s", plain)
	}
}

func TestSummaryLines(t *testing.T) {
	cases := []struct {
		name, summary string
		head, tail    int
		want          string
	}{
		{"empty", "", 10, 10, "  (empty)"},
		{"only space", " \n\t\n", 10, 10, "  (empty)"},
		{"one line", "Plan: 1 to add.\n", 100, 100, "  Plan: 1 to add."},
		{"several lines and CRLF", "a\r\nb\n\nc\n", 100, 100, "  a\n  b\n  \n  c"},
		{"not printable", "a\x1b[31mb\u202ec\n", 100, 100, "  a\\x1b[31mb\\u202ec"},
		{"head and tail", "aaaa\nbbbb\ncccc\ndddd\neeee\n", 14, 7, "  aaaa\n  bbbb\n  [summary cut: 14 bytes not shown]\n  eeee"},
		{"no tail", "aaaa\nbbbb\ncccc\n", 7, 0, "  aaaa\n  [summary cut: 14 bytes not shown]"},
		{"first line too long", "abcdefghij\nz\n", 6, 4, "  abcd\n  [summary cut: 6 bytes not shown]\n  z"},
		{"last line too long", "a\nbcdefghijk\n", 4, 6, "  a\n  [summary cut: 6 bytes not shown]\n  hijk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SummaryLines(c.summary, c.head, c.tail); got != c.want {
				t.Errorf("SummaryLines = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("cuts at a character boundary", func(t *testing.T) {
		got := SummaryLines(strings.Repeat("é", 40)+"\n"+strings.Repeat("ü", 40), 9, 9)
		if !utf8.ValidString(got) {
			t.Errorf("result is not valid UTF-8: %q", got)
		}
	})

	t.Run("bounded", func(t *testing.T) {
		long := strings.Repeat(strings.Repeat("x", 70)+"\n", 400)
		got := SummaryLines(long, summaryHeadBytes, summaryTailBytes)
		if len(got) > summaryHeadBytes+summaryTailBytes+64 {
			t.Errorf("rendered %d bytes", len(got))
		}
		if !strings.Contains(got, "  [summary cut: ") || strings.HasSuffix(got, "\n") {
			t.Errorf("no cut mark, or a trailing line break: %q", got[len(got)-80:])
		}
	})
}

func TestCutBytes(t *testing.T) {
	if got := cutBytes("héllo", 2); got != "h" {
		t.Errorf("cutBytes = %q, want h", got)
	}
	if got := cutBytes("abc", 5); got != "abc" {
		t.Errorf("cutBytes = %q, want abc", got)
	}
	if got := cutBytesEnd("hellé", 1); got != "" {
		t.Errorf("cutBytesEnd = %q, want empty", got)
	}
	if got := cutBytesEnd("hello", 2); got != "lo" {
		t.Errorf("cutBytesEnd = %q, want lo", got)
	}
	if got := cutBytesEnd("ab", 5); got != "ab" {
		t.Errorf("cutBytesEnd = %q, want ab", got)
	}
}

func TestApprovalMessagePlan(t *testing.T) {
	req := ApprovalRequest{Args: []ApprovalArg{{Name: "env", Values: []string{"prod"}}}, Plan: samplePlan()}
	got, fits := approvalMessage("apply", []string{"apply"}, req)
	if !fits || got != messagePlan {
		t.Errorf("message = %q, fits %v, want %q", got, fits, messagePlan)
	}

	t.Run("notes above the summary", func(t *testing.T) {
		plan := samplePlan()
		plan.Notes = []string{"the previous apply of this target failed at 2026-11-17T09:00:00Z. Check the target before approving."}
		got, _ := approvalMessage("apply", []string{"apply"}, ApprovalRequest{Plan: plan})
		want := "Artifact: sha256:9b1f (48213 bytes)\nNote: the previous apply of this target failed at 2026-11-17T09:00:00Z. Check the target before approving.\nSummary:\n"
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	})

	t.Run("a long summary fits", func(t *testing.T) {
		plan := samplePlan()
		plan.Summary = strings.Repeat("resource \"x\" will be created\n", 2000) + "Plan: 2000 to add."
		got, fits := approvalMessage("apply", []string{"apply"}, ApprovalRequest{Plan: plan})
		if !fits || len(got) > maxApprovalBytes {
			t.Fatalf("fits %v, %d bytes", fits, len(got))
		}
		if !strings.Contains(got, "  [summary cut: ") || !strings.Contains(got, "  Plan: 2000 to add.\nDestructive tasks") {
			t.Errorf("no cut mark, or the last summary line is not shown")
		}
	})

	t.Run("fields are escaped", func(t *testing.T) {
		plan := &PlanApproval{ID: "a\nb", Task: "p\nq", Created: "t\nu", Digest: "d\ne", Summary: "x"}
		got, _ := approvalMessage("apply", []string{"apply"}, ApprovalRequest{Plan: plan})
		if !strings.Contains(got, `Plan: a\nb made by "p\nq" at t\nu`+"\n"+`Artifact: sha256:d\ne (0 bytes)`) {
			t.Errorf("message = %q", got)
		}
	})

	t.Run("without a plan the text is unchanged", func(t *testing.T) {
		req := ApprovalRequest{Args: []ApprovalArg{{Name: "env", Values: []string{"prod"}}}, Confirmations: []Confirmation{{Task: "apply", Message: "Apply to prod? 3 resources change."}}}
		if got, _ := approvalMessage("apply", []string{"apply"}, req); got != messageApply {
			t.Errorf("message = %q, want %q", got, messageApply)
		}
	})
}

func TestPlanNames(t *testing.T) {
	cases := []struct {
		ungranted, want []string
	}{
		{nil, []string{"apply"}},
		{[]string{"apply"}, []string{"apply"}},
		{[]string{"apply", "wipe"}, []string{"apply", "wipe"}},
		{[]string{"wipe", "clean"}, []string{"apply", "wipe", "clean"}},
	}
	for _, c := range cases {
		if got := planNames("apply", c.ungranted); !slices.Equal(got, c.want) {
			t.Errorf("planNames(%v) = %v, want %v", c.ungranted, got, c.want)
		}
	}
}

func TestPlanRefusalTexts(t *testing.T) {
	with := New(planServerEngine(), Options{RunCommand: "rune"})
	without := New(planServerEngine(), Options{})
	if got, want := with.planNobody("apply"), planNeedsApply+"; nobody can be asked in this session."+operatorApply; got != want {
		t.Errorf("planNobody = %q, want %q", got, want)
	}
	if got, want := without.planNobody("apply"), planNeedsApply+"; nobody can be asked in this session."; got != want {
		t.Errorf("planNobody = %q, want %q", got, want)
	}
	if got, want := with.planCannotAsk("apply", reasonNoForm), planNeedsApply+"."+operatorApply+noFormSuffix; got != want {
		t.Errorf("planCannotAsk = %q, want %q", got, want)
	}
	if got, want := without.planCannotAsk("apply", reasonTooLarge), planNeedsApply+"."+tooBigSuffix; got != want {
		t.Errorf("planCannotAsk = %q, want %q", got, want)
	}
}

// A grant does not approve a plan: the person is asked either way, and an
// ungranted destructive task the call reaches is named after the task.
func TestApplyAlwaysAsks(t *testing.T) {
	for _, granted := range [][]string{nil, {"apply"}, {"apply", "clean"}} {
		eng := planServerEngine()
		eng.tasks[0].Destructive = true
		eng.tasks[0].DestructiveReach = []string{"clean", "wipe"}
		who := approving()
		cs := connectClient(t, New(eng, askOptions(func(o *Options) { o.AllowDestructive = granted })), who.options())

		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if res.IsError || text != "applied\n[exit 0]" {
			t.Fatalf("granted %v: isError=%v text=%q", granted, res.IsError, text)
		}
		if len(who.asked()) != 1 {
			t.Fatalf("granted %v: %d requests, want 1", granted, len(who.asked()))
		}
		want := []string{"apply", "clean", "wipe"}
		if slices.Contains(granted, "clean") {
			want = []string{"apply", "wipe"}
		}
		calls := eng.approvedCalls()
		if len(calls) != 1 || !slices.Equal(calls[0].tasks, want) {
			t.Errorf("granted %v: names = %+v, want %v", granted, calls, want)
		}
		if len(eng.plain) != 0 {
			t.Errorf("granted %v: the call ran without an approval: %v", granted, eng.plain)
		}
	}
}

func TestApplyRequestShowsThePlan(t *testing.T) {
	eng := planServerEngine()
	who := approving()
	cs := connectClient(t, New(eng, askOptions(nil)), who.options())
	callFor(t, cs, "apply", map[string]any{"env": "prod"})
	asked := who.asked()
	if len(asked) != 1 || asked[0].Message != messagePlan {
		t.Errorf("requests = %+v, want %q", asked, messagePlan)
	}
}

func TestApplyCannotBeAsked(t *testing.T) {
	t.Run("an engine that takes no approval is refused before its arguments are read", func(t *testing.T) {
		eng := &sampleApplyEngine{tasks: planServerEngine().tasks}
		cs := connectClient(t, New(eng, askOptions(nil)), approving().options())
		text, res := callFor(t, cs, "apply", map[string]any{"evn": "prod"})
		if want := planNeedsApply + "; nobody can be asked in this session." + operatorApply; !res.IsError || text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if eng.called != 0 {
			t.Errorf("the engine was called %d times", eng.called)
		}
	})

	t.Run("a server that does not ask", func(t *testing.T) {
		eng := planServerEngine()
		cs := connectClient(t, New(eng, Options{RunCommand: "rune"}), approving().options())
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if want := planNeedsApply + "; nobody can be asked in this session." + operatorApply; !res.IsError || text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if len(eng.approvedCalls()) != 1 || eng.runs() != 0 {
			t.Errorf("calls = %d, runs = %d, want the engine asked once and nothing run", len(eng.approvedCalls()), eng.runs())
		}
	})

	t.Run("a client without form elicitation", func(t *testing.T) {
		eng := planServerEngine()
		cs := connectClient(t, New(eng, askOptions(nil)), nil)
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if want := planNeedsApply + "." + operatorApply + noFormSuffix; !res.IsError || text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if eng.runs() != 0 {
			t.Errorf("runs = %d", eng.runs())
		}
	})

	t.Run("the engine's own refusal comes first", func(t *testing.T) {
		eng := planServerEngine()
		eng.refuse = errors.New(`task "apply" was not run: there is no plan`)
		cs := connectClient(t, New(eng, askOptions(nil)), nil)
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if !res.IsError || text != eng.refuse.Error() {
			t.Errorf("text = %q, want the engine's refusal", text)
		}
	})

	t.Run("invalid arguments are refused before the engine", func(t *testing.T) {
		eng := planServerEngine()
		cs := connectClient(t, New(eng, askOptions(nil)), approving().options())
		text, res := callFor(t, cs, "apply", map[string]any{"evn": "prod"})
		if !res.IsError || !strings.Contains(text, `unknown argument "evn"`) {
			t.Errorf("text = %q", text)
		}
		if len(eng.approvedCalls()) != 0 {
			t.Errorf("the engine was called: %+v", eng.approvedCalls())
		}
	})

	t.Run("a request that is too large", func(t *testing.T) {
		eng := planServerEngine()
		who := approving()
		cs := connectClient(t, New(eng, askOptions(nil)), who.options())
		text, res := callFor(t, cs, "apply", map[string]any{"env": strings.Repeat("x", maxApprovalBytes)})
		if want := planNeedsApply + "." + operatorApply + tooBigSuffix; !res.IsError || text != want {
			t.Errorf("text = %q", text)
		}
		if len(who.asked()) != 0 || eng.runs() != 0 {
			t.Errorf("requests = %d, runs = %d", len(who.asked()), eng.runs())
		}
	})

	t.Run("a request with no plan", func(t *testing.T) {
		eng := planServerEngine()
		eng.plan = nil
		who := approving()
		cs := connectClient(t, New(eng, askOptions(nil)), who.options())
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		if want := `task "apply" was not run: the engine reported no plan to approve`; !res.IsError || text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if len(who.asked()) != 0 || eng.runs() != 0 {
			t.Errorf("requests = %d, runs = %d", len(who.asked()), eng.runs())
		}
	})
}

// sampleApplyEngine is an Engine that cannot carry an approval.
type sampleApplyEngine struct {
	tasks  []TaskInfo
	called int
}

func (e *sampleApplyEngine) Tasks() []TaskInfo { return e.tasks }

func (e *sampleApplyEngine) Call(context.Context, string, map[string]string) (Result, error) {
	e.called++
	return Result{}, nil
}

// A request that does not end in an approval never offers the grant flag: no
// grant runs a task that applies a plan.
func TestApplyNotApprovedOffersNoGrant(t *testing.T) {
	answers := map[string]*person{
		"the request was declined":  declining(),
		"the request was dismissed": {answer: answerWith("cancel", nil)},
		"approval was not given":    {answer: answerWith("accept", map[string]any{"approved": false})},
	}
	for reason, who := range answers {
		eng := planServerEngine()
		cs := connectClient(t, New(eng, askOptions(nil)), who.options())
		text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
		want := `task "apply" was not approved: ` + reason + `. Nothing ran. To run it without a request, the operator can run "rune apply" themselves.`
		if !res.IsError || text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if eng.runs() != 0 {
			t.Errorf("%s: runs = %d", reason, eng.runs())
		}
	}
}

func TestRemedyWithoutAGrant(t *testing.T) {
	s := New(planServerEngine(), askOptions(nil))
	if got := s.remedy("apply", []string{"apply"}, true); got != remedyApply {
		t.Errorf("remedy with a grant = %q, want %q", got, remedyApply)
	}
	if got, want := s.remedy("apply", []string{"apply"}, false), ` To run it without a request, the operator can run "rune apply" themselves.`; got != want {
		t.Errorf("remedy without = %q, want %q", got, want)
	}
	if got := New(planServerEngine(), Options{}).remedy("apply", []string{"apply"}, false); got != "" {
		t.Errorf("remedy with no way = %q", got)
	}
	if got, want := s.outcomeText(outcomeDeclined, "apply", []string{"apply"}, false), `task "apply" was not approved: the request was declined. Nothing ran. To run it without a request, the operator can run "rune apply" themselves.`; got != want {
		t.Errorf("outcomeText = %q, want %q", got, want)
	}
}

// The person approved what one load serves. After a Replace nothing runs.
func TestApplyReloadWhilePending(t *testing.T) {
	eng := planServerEngine()
	var srv *Server
	who := &person{answer: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		srv.Replace(planServerEngine(), 0)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	srv = New(eng, askOptions(func(o *Options) { o.Reloadable = true }))
	cs := connectClient(t, srv, who.options())
	text, res := callFor(t, cs, "apply", map[string]any{"env": "prod"})
	if want := `task "apply" was not approved: the Runefile changed while the request was pending; call again. Nothing ran.`; !res.IsError || text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if eng.runs() != 0 {
		t.Errorf("runs = %d", eng.runs())
	}
}
