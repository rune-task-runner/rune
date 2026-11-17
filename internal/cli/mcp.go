package cli

import (
	"context"
	"errors"
	"io"
	"runtime"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/mask"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/mcpserver"
)

// mcpAdapter implements mcpserver.Engine over a parsed Runefile, running each
// tool call through the same scheduler the CLI uses (FR-026) with output
// captured into buffers.
type mcpAdapter struct {
	file      *ast.File
	tasks     map[string]*ast.Task
	assigns   map[string]*ast.Assignment
	settings  config.Settings
	root      string
	workDir   string
	baseEnv   []string
	overrides map[string]string
	now       func() string
	maskSet   *mask.Set // derived once; env/tasks/settings are fixed per adapter
	goos      string    // host OS for availability checks; runtime.GOOS outside tests

	// grants is the operator's --allow-destructive set, copied into every
	// engine a call builds. Only the `rune serve` adapter has one; the agent
	// task endpoint's adapter never does (spec 031 FR-015).
	grants map[string]bool

	// outputLimit caps each captured stream of a call. Zero falls back to
	// `set mcp_output_limit`, then to the default; only `rune serve` sets it,
	// from --output-limit (spec 042).
	outputLimit int

	// planIdent is this load's part of a plan's binding, and version the
	// Rune version recorded with a plan (spec 051). The loaders set them. A
	// Runefile that uses plans on an adapter without an identity stores and
	// applies nothing.
	planIdent planIdentity
	version   string

	// stderr is the operator's standard error: the server's own, not the
	// captured stream of a call. A warning about the plan journal goes there,
	// because a refused call returns no output that could carry it. Nil
	// leaves the warning in the call's output.
	stderr io.Writer

	// clock, gitInfo and the two limits are the plan store's clock, the git
	// reader and the size limits. Zero means the real ones; tests set them.
	clock                           func() time.Time
	gitInfo                         func(ctx context.Context, dir string) (commit string, dirty *bool)
	planMaxArtifact, planMaxSummary int64

	// contextTimeout bounds the [context] hook run; zero means the fixed
	// defaultContextTimeout. A field (not a global) so tests shrink it per
	// adapter without racing each other.
	contextTimeout time.Duration
}

// Tasks returns the non-private tasks available on this host OS as
// agent-facing tool descriptors, so an agent can never see (or attempt) a
// platform-incompatible task. No secret values appear in any field (FR-029).
func (a *mcpAdapter) Tasks() []mcpserver.TaskInfo {
	var out []mcpserver.TaskInfo
	for _, t := range a.file.Tasks {
		// The [context] hook is delivered as instructions, never as a tool
		// (spec 021 FR-006) — independent of [private].
		if !visibleOn(t, a.goos) || t.Attr(ast.AttrContext) != nil {
			continue
		}
		destructiveReach, reachesNetwork := a.reach(t)
		info := mcpserver.TaskInfo{
			Name:             t.Name,
			Doc:              t.Doc,
			Destructive:      t.Attr(ast.AttrConfirm) != nil,
			DestructiveReach: destructiveReach,
			Network:          t.Attr(ast.AttrNetwork) != nil || reachesNetwork,
			ReadOnly:         t.Attr(ast.AttrReadonly) != nil,
			AppliesPlan:      t.AppliesPlan(),
			// Static Runefile text, but capped like every agent-facing string
			// so an absurd [returns] cannot bloat the tool list (FR-011).
			Returns: capAgentText(t.Returns()),
		}
		for _, p := range t.Params {
			pi := mcpserver.ParamInfo{
				Name:     p.Name,
				Required: p.Kind == ast.ParamRequired || p.Kind == ast.ParamVariadicPlus,
				Variadic: p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar,
				// Capped for the same FR-011 reason as Returns above: an
				// absurd [param-doc] must not bloat every tools/list reply.
				Description: capAgentText(t.ParamDoc(p.Name)),
			}
			// Constraint kinds/values are static Runefile string literals, so
			// no environment value can reach the schema (FR-011).
			if c := p.Constraint; c != nil {
				pi.Kind = c.KindName
				pi.Enum = c.Values
			}
			// The one value an agent may pass (spec 050). A Runefile literal,
			// like an enum value. A literal default the policy refuses makes
			// the parameter one an agent has to send.
			for _, allowed := range t.AgentPolicy() {
				if allowed.Name != p.Name || pi.Variadic {
					continue
				}
				pi.AgentValues = []string{allowed.Value}
				if lit, ok := p.Default.(*ast.StringLit); ok && lit.Value != allowed.Value {
					pi.Required = true
				}
			}
			info.Params = append(info.Params, pi)
		}
		out = append(out, info)
	}
	return out
}

// reach lists the [confirm] tasks a run of t can reach through its
// dependencies and hooks, each once, in the order a run first reaches them
// (spec 034 FR-005), and reports whether any of the tasks it reaches has
// [network] (spec 033 FR-005). t's own attributes are not counted.
//
// The walk is depth-first and names a task before what it runs, as the
// refusal names the called task first. Edges follow the scheduler's order:
// dependencies, then && hooks, then || hooks. So the list reads in the order
// the Runefile does: for outer: reset and reset: wipe, it is reset, wipe.
func (a *mcpAdapter) reach(t *ast.Task) (destructive []string, network bool) {
	// A task run as a failure hook runs its dependencies and && hooks but not
	// its own || hooks: the scheduler does not chain post-mortems. hookOnly
	// marks such a task, so a later ordinary edge can still walk them.
	visited := map[string]bool{t.Name: true}
	hookOnly := map[string]bool{}
	var walk func(task *ast.Task, runs, failHooks bool)
	visit := func(name string, failHook bool) {
		next, ok := a.tasks[name]
		// A run skips a task unavailable on this host, and so never reaches
		// what that task would run (spec 033 FR-006).
		if !ok || !next.AvailableOn(a.goos) {
			return
		}
		switch {
		case !visited[next.Name]:
			visited[next.Name] = true
			hookOnly[next.Name] = failHook
			if next.Attr(ast.AttrConfirm) != nil {
				destructive = append(destructive, next.Name)
			}
			network = network || next.Attr(ast.AttrNetwork) != nil
			walk(next, true, !failHook)
		case hookOnly[next.Name] && !failHook:
			hookOnly[next.Name] = false
			walk(next, false, true)
		}
	}
	walk = func(task *ast.Task, runs, failHooks bool) {
		if runs {
			for _, e := range task.Deps {
				visit(e.Name, false)
			}
			for _, e := range task.PostHooks {
				visit(e.Name, false)
			}
		}
		if failHooks {
			for _, e := range task.FailHooks {
				visit(e.Name, true)
			}
		}
	}
	walk(t, true, true)
	return destructive, network
}

var (
	_ mcpserver.ListEngine     = (*mcpAdapter)(nil)
	_ mcpserver.ApprovalEngine = (*mcpAdapter)(nil)
)

// limit is the output limit in force for this adapter's calls: the
// operator's flag, else the Runefile's setting, else the default.
func (a *mcpAdapter) limit() int {
	switch {
	case a.outputLimit > 0:
		return a.outputLimit
	case a.settings.MCPOutputLimit > 0:
		return a.settings.MCPOutputLimit
	}
	return mcpserver.DefaultOutputLimit
}

// Call runs a task by name with named arguments, capturing stdout/stderr/exit.
// With no lists, each variadic value is one element.
func (a *mcpAdapter) Call(ctx context.Context, name string, args map[string]string) (mcpserver.Result, error) {
	return a.CallLists(ctx, name, args, nil)
}

// CallLists is Call with a variadic's elements as the agent sent them, so
// quote_each keeps their boundaries (spec 030).
func (a *mcpAdapter) CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (mcpserver.Result, error) {
	return a.call(ctx, name, args, lists, a.limit(), nil)
}

// CallApproved is CallLists for a call that needs a person's approval: it
// binds the arguments, puts the call to approve, and runs it only when
// approve returns nil, with the [confirm] gate of the tasks named passed for
// this run alone (spec 049).
func (a *mcpAdapter) CallApproved(ctx context.Context, name string, args map[string]string, lists map[string][]string, tasks []string, approve func(context.Context, mcpserver.ApprovalRequest) error) (mcpserver.Result, error) {
	return a.call(ctx, name, args, lists, a.limit(), &pendingApproval{tasks: tasks, ask: approve})
}

// pendingApproval is the approval one call waits for: the [confirm] tasks it
// would cover and the function that asks the person.
type pendingApproval struct {
	tasks []string
	ask   func(context.Context, mcpserver.ApprovalRequest) error
}

// approvalRequest describes a bound call for the person asked to approve it:
// every parameter with what the task will receive, and the [confirm] message
// of each task named. Values and messages are masked here, where the secret
// set is. The server only escapes and bounds them.
func (a *mcpAdapter) approvalRequest(t *ast.Task, params eval.Args, tasks []string) mcpserver.ApprovalRequest {
	masked := a.maskText
	var req mcpserver.ApprovalRequest
	for _, p := range t.Params {
		arg := mcpserver.ApprovalArg{Name: p.Name}
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			// The elements, not the joined value: a quote_each body gets
			// them one by one, so "a b" and "a", "b" are different calls.
			for _, element := range params.Lists[p.Name] {
				arg.Values = append(arg.Values, masked(element))
			}
		} else {
			arg.Values = []string{masked(params.Values[p.Name])}
		}
		req.Args = append(req.Args, arg)
	}
	for _, name := range tasks {
		task, ok := a.tasks[name]
		if !ok {
			continue
		}
		if c := task.Attr(ast.AttrConfirm); c != nil {
			req.Confirmations = append(req.Confirmations, mcpserver.Confirmation{Task: name, Message: masked(c.Str)})
		}
	}
	return req
}

// maskText masks the adapter's secrets in s. An adapter built without a mask
// set masks nothing.
func (a *mcpAdapter) maskText(s string) string {
	return maskerFor(a.maskSet)(s)
}

// call runs a task with each of stdout and stderr captured in at most limit
// bytes. Once the arguments are bound it checks the [agent-allow] policy of
// the task and of everything the call can reach, and returns the refusal
// before anything runs (spec 050). With an approval pending, it asks after
// that, so nobody is asked about a call a policy refuses.
func (a *mcpAdapter) call(ctx context.Context, name string, args map[string]string, lists map[string][]string, limit int, pending *pendingApproval) (mcpserver.Result, error) {
	t, ok := a.tasks[name]
	if !ok {
		return mcpserver.Result{}, errorf("unknown task: %s", name)
	}
	// Defense-in-depth for direct Engine.Call users (embedders, tests): over
	// the real MCP transport a mismatched task is never registered as a tool,
	// so the SDK rejects it as unknown before this line; the check here keeps
	// the Engine contract safe for callers that bypass tool registration.
	if !t.AvailableOn(a.goos) {
		return mcpserver.Result{}, availabilityErr(name, t, a.goos)
	}
	// The capture is bounded while the task writes, not cut afterwards, so a
	// task that prints without end cannot grow this long-lived server (spec
	// 042 FR-015). The buffers lock themselves: [parallel] tasks and their ||
	// failure hooks write concurrently, and with an empty mask set nothing
	// else sits between those writers and the buffers.
	outBuf := mcpserver.NewOutputBuffer(limit)
	errBuf := mcpserver.NewOutputBuffer(limit)
	scope := eval.NewScope(a.assigns, a.overrides)
	// One host-OS truth per adapter: availability (above), dependency
	// skipping (eng.goos below), and the os()/os_family() builtins must
	// never disagree, so the scope reads the same injected value.
	scope.GOOS = a.goos
	scope.Arch = runtime.GOARCH

	// The same masking choke point as the CLI path: the buffers only ever hold
	// masked text, so the tool result an agent receives is safe by
	// construction, and a truncation cut can never split a secret.
	mopts, flushMask := maskOptions(
		Options{Stdin: nil, Stdout: outBuf, Stderr: errBuf, Cwd: a.workDir, Quiet: true, Version: a.version},
		a.maskSet,
	)

	// Deliberately no `file` here: executeAgent gates its [context] gathering
	// and callback endpoint on e.file != nil, so leaving it unset is what
	// keeps gatherContext → Call → executeAgent from recursing when an agent
	// task is reached through this path. If you ever thread file through,
	// add an explicit recursion guard first (spec 021, review finding).
	// || failure-hook stdout is captured separately (spec 022): masked at the
	// writer like the main buffers, serialized because [parallel] failures can
	// fire hooks concurrently, and capped twice — the buffer stops storing a
	// little past the budget (so a runaway hook cannot grow the long-lived
	// server's memory) and capAgentText renders the exact cap after the flush.
	fixBuf := &cappedBuffer{max: contextMaxBytes + 64}
	fixSink, flushFix := maskWriter(fixBuf, a.maskSet)

	eng := &engine{
		tasks:       a.tasks,
		scope:       scope,
		settings:    a.settings,
		workDir:     a.workDir,
		root:        a.root,
		env:         agentRunEnv(a.baseEnv),
		opts:        mopts,
		plan:        planRun,
		now:         a.now,
		ctx:         ctx,
		goos:        a.goos,
		failHookOut: &syncWriter{w: fixSink},
		grants:      a.grants,
		// Every call through the adapter starts from an agent, the [context]
		// hook included: it runs because an agent connected.
		agent: true,
		mask:  a.maskText,

		planIdent:       a.planIdent,
		surface:         surfaceMCP,
		operatorErr:     a.stderr,
		clock:           a.clock,
		gitInfo:         a.gitInfo,
		planMaxArtifact: a.planMaxArtifact,
		planMaxSummary:  a.planMaxSummary,
	}

	params, err := bindNamedParams(t, args, lists, scope)
	if err != nil {
		return mcpserver.Result{}, err
	}

	// A grant or an approval answers whether a destructive task may run, not
	// which values an agent may run it with, so the policy comes first.
	if d := eng.preflightPolicy(t, params); d != nil {
		return mcpserver.Result{}, errors.New(d.text(a.maskText))
	}

	// A task that applies a plan runs only with a stored plan that passed
	// every check, and only once a person approved that plan in this call
	// (spec 051). The checks come before the question, so nobody is asked
	// about a plan that could not be applied.
	var ticket *planTicket
	if t.AppliesPlan() != "" {
		if pending == nil {
			return mcpserver.Result{}, errors.New(applyNobodyText(t.Name))
		}
		if ticket, err = eng.findPlan(t, params); err != nil {
			return mcpserver.Result{}, err
		}
	}

	if pending != nil {
		// Binding ran no command, so nothing has happened yet. The person
		// is shown the values bound above, which are the ones the run uses.
		req := a.approvalRequest(t, params, pending.tasks)
		ask := func() error { return pending.ask(ctx, req) }
		if ticket != nil {
			req.Plan = ticket.approval(a.maskText)
			ask = func() error {
				return eng.askApproval(ticket, func() (string, error) { return "", pending.ask(ctx, req) })
			}
		}
		if err := ask(); err != nil {
			return mcpserver.Result{}, err
		}
		if ticket != nil {
			if err := eng.claimPlan(ticket, params); err != nil {
				return mcpserver.Result{}, err
			}
		}
		eng.approved = make(map[string]bool, len(pending.tasks))
		for _, task := range pending.tasks {
			eng.approved[task] = true
		}
	}

	runErr := scheduler.Run(eng, []scheduler.Invocation{{Task: t, Params: params}})
	code := ExitSuccess
	if runErr != nil {
		// classifyRunErr can render diagnostics into the masked stderr, so it
		// must precede the flush that drains the writers into the buffers.
		code = CodeFor(eng.classifyRunErr(runErr))
	}
	// Before the flush too: a tampered copy is reported on standard error.
	if eng.finishApply(code) && code == ExitSuccess {
		code = ExitTaskFail
	}
	flushMask()
	flushFix()
	return mcpserver.Result{
		Stdout:        outBuf.String(),
		Stderr:        errBuf.String(),
		ExitCode:      code,
		FixSuggestion: capAgentText(fixBuf.String()),
	}, nil
}
