package cli

import (
	"bytes"
	"context"
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
		reachesDestructive, reachesNetwork := a.reach(t)
		info := mcpserver.TaskInfo{
			Name:               t.Name,
			Doc:                t.Doc,
			Destructive:        t.Attr(ast.AttrConfirm) != nil,
			ReachesDestructive: reachesDestructive,
			Network:            t.Attr(ast.AttrNetwork) != nil || reachesNetwork,
			ReadOnly:           t.Attr(ast.AttrReadonly) != nil,
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
			info.Params = append(info.Params, pi)
		}
		out = append(out, info)
	}
	return out
}

// reach reports whether any task a run of t can reach through its
// dependencies and hooks has [confirm] or [network] (spec 033 FR-005). t's own
// attributes are not counted.
func (a *mcpAdapter) reach(t *ast.Task) (destructive, network bool) {
	type step struct {
		name     string
		failHook bool
	}
	var queue []step
	queueEdges := func(t *ast.Task, runs, failHooks bool) {
		if runs {
			for _, e := range t.Deps {
				queue = append(queue, step{e.Name, false})
			}
			for _, e := range t.PostHooks {
				queue = append(queue, step{e.Name, false})
			}
		}
		if failHooks {
			for _, e := range t.FailHooks {
				queue = append(queue, step{e.Name, true})
			}
		}
	}
	// A task run as a failure hook runs its dependencies and && hooks but not
	// its own || hooks: the scheduler does not chain post-mortems. hookOnly
	// marks such a task, so a later ordinary edge can still add them.
	visited := map[string]bool{t.Name: true}
	hookOnly := map[string]bool{}
	queueEdges(t, true, true)
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		next, ok := a.tasks[s.name]
		// A run skips a task unavailable on this host, and so never reaches
		// what that task would run (FR-006).
		if !ok || !next.AvailableOn(a.goos) {
			continue
		}
		switch {
		case !visited[next.Name]:
			visited[next.Name] = true
			hookOnly[next.Name] = s.failHook
			destructive = destructive || next.Attr(ast.AttrConfirm) != nil
			network = network || next.Attr(ast.AttrNetwork) != nil
			queueEdges(next, true, !s.failHook)
		case hookOnly[next.Name] && !s.failHook:
			hookOnly[next.Name] = false
			queueEdges(next, false, true)
		}
	}
	return destructive, network
}

var _ mcpserver.ListEngine = (*mcpAdapter)(nil)

// Call runs a task by name with named arguments, capturing stdout/stderr/exit.
// With no lists, each variadic value is one element.
func (a *mcpAdapter) Call(ctx context.Context, name string, args map[string]string) (mcpserver.Result, error) {
	return a.CallLists(ctx, name, args, nil)
}

// CallLists is Call with a variadic's elements as the agent sent them, so
// quote_each keeps their boundaries (spec 030).
func (a *mcpAdapter) CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (mcpserver.Result, error) {
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
	// syncWriter guards the capture buffers: [parallel] tasks (and their ||
	// failure hooks) write concurrently, and when the mask set is empty
	// maskOptions leaves the writers unwrapped, so the buffers themselves
	// must be safe. With a non-empty set the mask writer's own lock sits on
	// top; the extra mutex there is uncontended.
	var outBuf, errBuf bytes.Buffer
	outSink := &syncWriter{w: &outBuf}
	errSink := &syncWriter{w: &errBuf}
	scope := eval.NewScope(a.assigns, a.overrides)
	// One host-OS truth per adapter: availability (above), dependency
	// skipping (eng.goos below), and the os()/os_family() builtins must
	// never disagree, so the scope reads the same injected value.
	scope.GOOS = a.goos
	scope.Arch = runtime.GOARCH

	// The same masking choke point as the CLI path: the buffers only ever hold
	// masked text, so the tool result an agent receives is safe by construction.
	mopts, flushMask := maskOptions(
		Options{Stdin: nil, Stdout: outSink, Stderr: errSink, Cwd: a.workDir, Quiet: true},
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
		env:         a.baseEnv,
		opts:        mopts,
		plan:        planRun,
		now:         a.now,
		ctx:         ctx,
		goos:        a.goos,
		failHookOut: &syncWriter{w: fixSink},
		grants:      a.grants,
	}

	params, err := bindNamedParams(t, args, lists, scope)
	if err != nil {
		return mcpserver.Result{}, err
	}

	runErr := scheduler.Run(eng, []scheduler.Invocation{{Task: t, Params: params}})
	code := ExitSuccess
	if runErr != nil {
		// classifyRunErr can render diagnostics into the masked stderr, so it
		// must precede the flush that drains the writers into the buffers.
		code = CodeFor(eng.classifyRunErr(runErr))
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
