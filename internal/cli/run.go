package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/analyzer"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/cache"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/dotenv"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
	rt "github.com/rune-task-runner/rune/internal/runtime"
	"github.com/rune-task-runner/rune/internal/runtime/agent"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/style"
	"github.com/rune-task-runner/rune/internal/token"
	"github.com/rune-task-runner/rune/mcpserver"
)

// execute is the inner pipeline for a resolved Runefile: parse → analyze →
// schedule → run.
func execute(opts Options, runefile string, args []string) error {
	root := filepath.Dir(runefile)

	// --fmt rewrites the Runefile canonically and exits (runs nothing).
	if opts.Fmt {
		return fmtRewrite(opts, runefile)
	}

	// --clear-cache removes the project-local cache and exits.
	if opts.ClearCache {
		if err := cache.Clear(root); err != nil {
			return &UsageError{Err: err}
		}
		// "cleared" is an accomplished-outcome label like "running" (014 C3).
		fmt.Fprintf(opts.Stderr, "%s %s\n", opts.themeStderr().Success.Render("cleared:"), filepath.Join(root, ".rune", "cache"))
		return nil
	}

	source, err := os.ReadFile(runefile)
	if err != nil {
		return &UsageError{Err: err}
	}

	file, diags := parser.Parse(runefile, string(source))
	// The files of the load are kept for an (agent) task: the tasks its agent
	// calls may not change them (spec 053).
	rec := &pathRecorder{}
	rec.addRead(runefile)
	srcProvider := rec.wrap(newSourceProvider(runefile, source))

	// Enforce the root Runefile's minimum_version before imports/analysis/execution
	// (FR-004). Reading the root file's own settings pre-compose is what keeps a
	// child/imported file from imposing or relaxing the requirement (FR-012).
	if err := enforceMinimumVersion(opts, file, srcProvider, opts.IgnoreVersion); err != nil {
		return err
	}

	// Splice imports and namespace submodules before analysis.
	cdiags := config.Compose(file, srcProvider)
	diags = append(diags, cdiags...)

	// Lexer/parser errors flow through the same spanned diagnostic path (T036).
	if diags.HasErrors() {
		renderDiags(opts, diags, srcProvider)
		return &ValidationError{Err: errorf("%d static error(s)", countErrors(diags))}
	}

	// Whole-file semantic analysis runs before any execution: emit ALL
	// diagnostics, run nothing, exit 3 (Principle II / FR-014).
	if adiags := analyzer.Analyze(file); adiags.HasErrors() {
		renderDiags(opts, adiags, srcProvider)
		return &ValidationError{Err: errorf("%d static error(s)", countErrors(adiags))}
	}

	if opts.Dump {
		return dumpFile(opts, file)
	}

	tasks := indexTasks(file)
	assigns := indexAssignments(file)

	overrides, rawInvs, err := splitArgs(args, tasks, opts.Commands)
	if err != nil {
		return err
	}

	scope := eval.NewScope(assigns, overrides)
	scope.GOOS = runtime.GOOS
	scope.Arch = runtime.GOARCH

	settings, sdiags := config.ResolveSettings(file, eval.New(scope))
	if sdiags.HasErrors() {
		renderDiags(opts, sdiags, srcProvider)
		return &ValidationError{Err: errorf("invalid settings")}
	}

	// Listing and inspection short-circuits (run nothing).
	if opts.List {
		listTasks(opts, file)
		return nil
	}

	workDir := resolveWorkDir(runefile, settings.WorkingDir)
	env, dotenvPairs := buildEnv(settings, scope, root)

	plan := planRun
	switch {
	case opts.DryRun:
		plan = planDryRun
	case opts.Summary:
		plan = planSummary
	}

	// `rune ci run` runs exactly one task: an override or a second task would
	// make the job run something other than its node (spec 059).
	if opts.nodeRun != nil && (len(overrides) > 0 || len(rawInvs) != 1) {
		return usagef("rune ci run runs one task with its arguments: rune ci run TASK [ARG...]")
	}

	// Bare `rune` (a real run with no task requested) prints the version + task
	// overview instead of running anything — Rune no longer auto-runs a default
	// task. (`--dry-run`/`--summary` with no task still error via resolveRoots.)
	if plan == planRun && len(rawInvs) == 0 {
		printOverview(opts, file)
		return nil
	}

	// Secret masking wraps the output streams once, before the engine exists,
	// so every downstream surface inherits it. The deferred flush runs after
	// scheduler.Run has joined all tasks — the only always-safe flush point.
	set := deriveMaskSet(env, tasks, settings.Secrets, settings.Unmasked)
	mopts, flushMask := maskOptions(opts, set)
	defer flushMask()

	// A body of an agent run started this rune: its tasks are still the
	// agent's, so their policies apply (spec 050). The mark goes back on
	// last, as it does for a tool call: a dotenv file or an exported variable
	// of its name must not take it from the bodies this run starts.
	agentRun := os.Getenv(agentRunVar) == "1"
	if agentRun {
		env = agentRunEnv(env)
	}

	eng := &engine{
		file:      file,
		tasks:     tasks,
		assigns:   assigns,
		overrides: overrides,
		scope:     scope,
		settings:  settings,
		workDir:   workDir,
		root:      root,
		env:       env,
		opts:      mopts,
		theme:     mopts.themeStderr(),
		plan:      plan,
		now:       func() string { return time.Now().UTC().Format(time.RFC3339) },
		ctx:       mopts.ctx(),
		src:       srcProvider,
		goos:      runtime.GOOS,
		stdinTTY:  isTerminal(mopts.Stdin),
		agent:     agentRun,
		mask:      maskerFor(set),
		surface:   surfaceCLI,
		// A body of an agent's call started this rune: what it records
		// names that call as its parent (spec 052).
		invocation: os.Getenv(invocationVar),
		// Computed only when an agent task opens its endpoint, so an
		// ordinary run pays nothing for it.
		identity: sync.OnceValue(func() planIdentity {
			return newPlanIdentity(file, root, settings, dotenvPairs, overrides)
		}),
		loaded:  rec.found(),
		missing: rec.notFound(),
	}
	if usesPlans(tasks) {
		eng.planIdent = eng.identity()
	}

	invs, err := eng.resolveRoots(rawInvs)
	if err != nil {
		return err
	}

	// Every task on the command line is checked before the first one runs.
	// Plan modes run no body, so they have nothing to refuse.
	if eng.agent && plan == planRun {
		for _, inv := range invs {
			if d := eng.preflightPolicy(inv.Task, inv.Params); d != nil {
				return &TaskFailure{Err: errors.New(d.text(eng.mask))}
			}
		}
	}

	// A task that applies a plan is checked, put to the person and claimed
	// before anything runs (spec 051). Plan modes run no body.
	if plan == planRun {
		if err := eng.prepareApplyRoot(invs); err != nil {
			return maskErr(err, set)
		}
	}

	run := scheduler.Run
	if opts.nodeRun != nil {
		run = func(_ scheduler.Engine, invs []scheduler.Invocation) error { return opts.nodeRun(eng, invs) }
	}
	var failure error
	if err := run(eng, invs); err != nil {
		failure = eng.classifyRunErr(err)
	}
	if eng.finishApply(CodeFor(failure)) && failure == nil {
		// The line that says so is already on standard error.
		failure = &TaskFailure{Err: errorf("the plan file changed while it was being applied"), Silent: true}
	}
	// maskErr covers the error text itself: the "rune: ..." banner is
	// printed by callers outside the masked writers.
	if failure != nil {
		return maskErr(failure, set)
	}
	return nil
}

// planMode controls whether Execute runs, or only reports, each task.
type planMode int

const (
	planRun     planMode = iota
	planDryRun           // --dry-run: print the plan + would-be cache decision
	planSummary          // --summary: print task names, one per line
)

// engine implements scheduler.Engine over the parsed module.
type engine struct {
	file      *ast.File
	tasks     map[string]*ast.Task
	assigns   map[string]*ast.Assignment
	overrides map[string]string
	scope     *eval.Scope
	settings  config.Settings
	workDir   string
	root      string // directory containing the Runefile (cache root)
	env       []string
	opts      Options
	theme     style.Theme // stderr styling for status/echo/cache lines
	plan      planMode
	now       func() string
	ctx       context.Context
	src       diag.SourceProvider
	goos      string // host OS for availability checks; runtime.GOOS outside tests

	// stdinTTY records, once per invocation, whether stdin is a terminal: only
	// then may a [confirm] prompt be answered (spec 027). Always false on the
	// MCP path, which passes no stdin at all.
	stdinTTY bool

	// confirmMu holds one [confirm] prompt and its answer at a time.
	confirmMu sync.Mutex

	// grants holds the operator's --allow-destructive task names, each of
	// which stands in for its own [confirm] answer (spec 031). Set only on the
	// MCP server path; nil on the CLI path and on an agent task's endpoint.
	grants map[string]bool

	// approved holds the [confirm] tasks a person approved for this one MCP
	// call, through the client (spec 049). Each stands in for its own answer,
	// as a grant does, but only on the engine built for that call: nothing
	// else reads it, and it ends with the call.
	approved map[string]bool

	// agent marks a run that started from an agent's tool call: every task
	// it reaches is held to its [agent-allow] policy (spec 050). The MCP
	// adapter sets it on the engine of each call, and the command line sets
	// it when a body of such a run started this rune (agentRunVar). False for
	// a person's run.
	agent bool

	// mask hides secret values in the text of a policy refusal. Nil masks
	// nothing.
	mask func(string) string

	// planIdent is what this load of the Runefile contributes to a plan's
	// binding (spec 051). Zero when no task makes or applies a plan.
	planIdent planIdentity

	// surface names where plan events come from in the journal: surfaceMCP
	// or surfaceCLI.
	surface string

	// invocation is the id of the agent's call this run belongs to: the
	// call itself on the MCP path, the call whose body started this rune on
	// the command line, or "" for a person's run (spec 052). audit is the
	// record of that call on the MCP path; nil records nothing.
	invocation string
	audit      *mcpserver.CallAudit

	// identity computes the identity of this load for the audit record of
	// an agent run. Nil on the MCP path, where the adapter has it.
	identity func() planIdentity

	// operatorErr, when set (MCP path), receives a warning about the plan
	// journal in place of the call's captured standard error, which a refused
	// call drops.
	operatorErr io.Writer

	// attempt is the claimed plan of this run, set once a person approved it
	// and until the run ends. Only the body of its invocation is handed the
	// plan.
	attempt *applyAttempt

	// clock is the plan store's clock and gitInfo reads the commit recorded
	// with a plan. Nil means the real ones; tests set them.
	clock   func() time.Time
	gitInfo func(ctx context.Context, dir string) (commit string, dirty *bool)

	// planMaxArtifact and planMaxSummary lower the store's size limits in
	// tests. Zero means the store's own.
	planMaxArtifact, planMaxSummary int64

	// sandbox, when set, confines every body of this run: the engine of an
	// agent's call on a server that enforces (spec 053). Nil runs bodies in
	// this process, as a person's run does.
	sandbox *bodySandbox

	// endpointSandbox replaces the host's sandbox for the endpoint of an
	// (agent) task. Nil means the host's; tests set it.
	endpointSandbox *sandboxState

	// loaded and missing are the files this load read, and those it looked
	// for and did not find. Set on the command line, for the endpoint an
	// (agent) task opens.
	loaded, missing []string

	// failHookOut, when set (MCP path), receives || failure-hook body stdout
	// instead of opts.Stdout, so the adapter can deliver it as a delimited
	// fix-suggestion section (spec 022). Nil on the CLI path: hook output
	// streams to the terminal like any task's.
	failHookOut io.Writer
}

// resolveRoots turns CLI task invocations into scheduler roots. Bare `rune`
// (no invocation) is handled earlier in execute by printOverview, so reaching
// here with no task means an action flag (e.g. --dry-run) was given without one.
func (e *engine) resolveRoots(raw []rawInvocation) ([]scheduler.Invocation, error) {
	if len(raw) == 0 {
		return nil, usagef("no task specified; run 'rune' for an overview or 'rune --list' to see tasks")
	}
	var invs []scheduler.Invocation
	for _, r := range raw {
		t := e.tasks[r.name] // existence already checked in splitArgs
		// An explicitly requested task must be runnable here: unlike a
		// dependency (which the scheduler skips silently), an OS-mismatched
		// root aborts the whole invocation before anything executes. The
		// caret-anchored diagnostic renders here because ValidationError
		// suppresses the trailing banner (its contract: already rendered).
		if !t.AvailableOn(e.goos) {
			err := availabilityErr(r.name, t, e.goos)
			d := diag.New(osAttrSpan(t), err.Error())
			renderDiags(e.opts, diag.List{d}, e.src)
			return nil, &ValidationError{Err: err}
		}
		params, err := bindParams(t, r.args, e.scope)
		if err != nil {
			return nil, err
		}
		invs = append(invs, scheduler.Invocation{Task: t, Params: params})
	}
	return invs, nil
}

// availabilityErr reports an OS-mismatched task in attribute vocabulary,
// e.g.: task "setup-win" is not available on macos (requires windows).
// It returns a plain error: only callers that render a diagnostic first may
// wrap it in ValidationError (whose contract is "already rendered").
func availabilityErr(name string, t *ast.Task, goos string) error {
	return errorf("task %q is not available on %s (requires %s)",
		name, displayOS(goos), strings.Join(t.OSFilters(), " or "))
}

// osAttrSpan anchors the availability diagnostic at the task's first OS
// attribute, falling back to the task itself.
func osAttrSpan(t *ast.Task) token.Span {
	if kinds := t.OSFilters(); len(kinds) > 0 {
		return t.Attr(kinds[0]).Sp
	}
	return t.Sp
}

// Available reports whether the task may run on this host OS; the scheduler
// skips unavailable dependency/post-hook targets silently.
func (e *engine) Available(task *ast.Task) bool { return task.AvailableOn(e.goos) }

// ResolveDep evaluates a dependency call in the caller's scope and binds args.
// OS-mismatched targets are returned unevaluated (empty Args): the scheduler
// skips them, so their args and defaults must not run on this host.
func (e *engine) ResolveDep(curTask *ast.Task, cur eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error) {
	return e.resolveDepIn(e.scope, curTask, cur, dep)
}

// resolveDepIn is ResolveDep against a given scope. The policy check before
// an agent run resolves on a fork of the run's scope, so what it reads is
// not cached for the run (spec 050).
func (e *engine) resolveDepIn(scope *eval.Scope, curTask *ast.Task, cur eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error) {
	target, ok := e.tasks[dep.Name]
	if !ok {
		return nil, eval.Args{}, &ValidationError{Err: errorf("unknown dependency %q of task %q", dep.Name, curTask.Name)}
	}
	// The scheduler skips OS-mismatched targets silently, so don't evaluate
	// their args or parameter defaults: those may only resolve on the matching
	// host (e.g. a [windows] dep defaulting to env("APPDATA") must not abort
	// the dispatch pattern on linux).
	if !e.Available(target) {
		return target, eval.Args{}, nil
	}
	ev := eval.New(scope.WithArgs(cur))
	vals := make([]string, 0, len(dep.Args))
	for _, a := range dep.Args {
		val, evErr := ev.Eval(a)
		if evErr != nil {
			return nil, eval.Args{}, &ValidationError{Err: evErr}
		}
		vals = append(vals, val)
	}
	v := variadicIndex(target)
	if v < 0 || len(vals) <= v {
		params, err := bindParams(target, vals, scope)
		if err != nil {
			return nil, eval.Args{}, err
		}
		return target, params, nil
	}
	// Spreading stops at the target's variadic slot, so scalar binding and
	// arity stay what they were before spec 030 (R4).
	elems := []string{}
	for i, a := range dep.Args[v:] {
		if ref, isRef := a.(*ast.VarRef); isRef {
			if l, ok := cur.Lists[ref.Name]; ok {
				elems = append(elems, l...)
				continue
			}
		}
		elems = append(elems, vals[v+i])
	}
	params, err := bindParams(target, append(vals[:v:v], elems...), scope)
	if err != nil {
		// Forwarding has no opt-in, so a call v0.6.0 bound from the written
		// arguments' text must still run, e.g. an empty list into a + parameter.
		var oldErr error
		if params, oldErr = bindParams(target, vals, scope); oldErr != nil {
			return nil, eval.Args{}, err
		}
	}
	name := target.Params[v].Name
	params.Values[name] = strings.Join(vals[v:], " ")
	params.Lists[name] = elems
	return target, params, nil
}

// variadicIndex returns the index of the task's variadic parameter, or -1.
func variadicIndex(t *ast.Task) int {
	for i, p := range t.Params {
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			return i
		}
	}
	return -1
}

// Execute handles plan modes, confirmation, and caching, then runs the body.
func (e *engine) Execute(task *ast.Task, params eval.Args) error {
	if e.plan == planSummary {
		fmt.Fprintln(e.opts.Stdout, task.Name)
		return nil
	}

	if e.plan == planRun {
		if f := e.policyGate(task, params); f != nil {
			// Printed here because a silent failure has no banner, and marked
			// as body-not-run so || failure hooks do not fire for it.
			printErrorBanner(e.opts, e.theme, f.Error())
			return &TaskFailure{Err: &bodyNotRunError{err: f.Err}, Silent: true}
		}
		if f := e.applyGate(task, params); f != nil {
			printErrorBanner(e.opts, e.theme, f.Error())
			return &TaskFailure{Err: &bodyNotRunError{err: f.Err}, Silent: true}
		}
	}

	if c := task.Attr(ast.AttrConfirm); c != nil && e.plan == planRun {
		if f := e.confirmFailure(task, c.Str); f != nil {
			// The body never started, so || failure hooks must not fire
			// (FR-003): the wrapper lets the scheduler recognize this via
			// scheduler.ErrBodyNotRun without changing the rendered message.
			return &TaskFailure{Err: &bodyNotRunError{err: f.Err}, Silent: f.Silent}
		}
	}

	cacheAttr := task.Attr(ast.AttrCache)

	if e.plan == planDryRun {
		label := "would run"
		if cacheAttr != nil {
			if d, err := cache.Decide(e.cacheSpec(task, params, cacheAttr)); err == nil && d.Skip {
				label = "would skip (cached)"
			}
		}
		// Dry-run notices are meta: dim the whole line (plain when color is off).
		fmt.Fprintf(e.opts.Stderr, "%s\n", e.theme.Muted.Render(fmt.Sprintf("%s: %s", label, task.Name)))
		return nil
	}

	if cacheAttr != nil {
		spec := e.cacheSpec(task, params, cacheAttr)
		d, derr := cache.Decide(spec)
		if derr == nil && d.Skip {
			// Cache-hit notice is dimmed so real output stands out (FR-014).
			fmt.Fprintf(e.opts.Stderr, "%s\n", e.theme.Muted.Render(fmt.Sprintf("cached: %s", task.Name)))
			return nil
		}
		// "running" is an active status label (FR-014).
		fmt.Fprintf(e.opts.Stderr, "%s: %s\n", e.theme.Success.Render("running"), task.Name)
		if err := e.runBody(task, params); err != nil {
			return err
		}
		if derr == nil {
			if cerr := cache.Store(spec, d.Hash, e.now()); cerr != nil {
				fmt.Fprintf(e.opts.Stderr, "%s: failed to write cache for %s: %v\n", e.theme.Warning.Render("warning"), task.Name, cerr)
			}
		}
		return nil
	}

	return e.runBody(task, params)
}

// ExecuteFailHook runs a || failure-hook body (spec 022). Hooks bypass the
// cache deliberately — a "cached" diagnostic would skip the one run whose
// output is wanted — but keep the [confirm] gate: an unconfirmable hook
// declines, fails, and surfaces as a one-line warning upstream.
func (e *engine) ExecuteFailHook(task *ast.Task, params eval.Args, f scheduler.Failure) error {
	if e.plan != planRun {
		return nil // plan modes never execute bodies, so no failure can reach here
	}
	// User cancellation fires no post-mortems (FR-002). The scheduler filters
	// a bare context.Canceled body error, but Ctrl-C usually reaches the
	// child process first, so the body error is an executor ExecError (exit
	// 130 / killed) — the run context is the reliable signal.
	if e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	// Not printed here: the scheduler reports a hook's failure itself.
	if f := e.policyGate(task, params); f != nil {
		return f
	}
	if f := e.applyGate(task, params); f != nil {
		return f
	}
	if c := task.Attr(ast.AttrConfirm); c != nil {
		if f := e.confirmFailure(task, c.Str); f != nil {
			return f
		}
	}
	// "diagnosing" is an active status label like "running:" (label styled,
	// name plain) so the post-mortem is visible at the point of failure.
	fmt.Fprintf(e.opts.Stderr, "%s: %s\n", e.theme.Warning.Render("diagnosing"), task.Name)

	stdout := io.Writer(e.opts.Stdout)
	if e.failHookOut != nil {
		stdout = e.failHookOut
	}
	failEnv := []string{
		"RUNE_FAILED_TASK=" + f.TaskName,
		fmt.Sprintf("RUNE_FAILED_EXIT_CODE=%d", f.ExitCode),
	}
	return e.runBodyTo(task, params, stdout, failEnv)
}

// bodyNotRunError wraps a pre-body failure so the scheduler can recognize it
// (errors.Is(err, scheduler.ErrBodyNotRun) suppresses || failure hooks)
// while the rendered message stays exactly the wrapped error's.
type bodyNotRunError struct{ err error }

func (e *bodyNotRunError) Error() string   { return e.err.Error() }
func (e *bodyNotRunError) Unwrap() []error { return []error{e.err, scheduler.ErrBodyNotRun} }

// Warnf prints the standard one-line warning to stderr (label styled, text
// plain). The scheduler uses it for failure-hook skips and failures.
func (e *engine) Warnf(format string, args ...any) {
	fmt.Fprintf(e.opts.Stderr, "%s: %s\n", e.theme.Warning.Render("warning"), fmt.Sprintf(format, args...))
}

// runBody interpolates and runs a task body via the selected executor.
func (e *engine) runBody(task *ast.Task, params eval.Args) error {
	return e.runBodyTo(task, params, e.opts.Stdout, nil)
}

// runBodyTo is runBody with an explicit stdout sink and optional extra
// environment entries; || failure hooks on the MCP path redirect their body
// stdout into the fix-suggestion capture, and every hook run appends the
// failure context (contracts/failure-env.md). extraEnv comes last so it wins
// over static declarations of the same names.
func (e *engine) runBodyTo(task *ast.Task, params eval.Args, stdout io.Writer, extraEnv []string) error {
	ev := eval.New(e.scope.WithArgs(params))

	lines := make([]shell.Line, 0, len(task.Body))
	for _, bl := range task.Body {
		text, evErr := ev.Interpolate(bl.Raw, bl.Sp)
		if evErr != nil {
			return &ValidationError{Err: evErr}
		}
		lines = append(lines, shell.Line{
			Text:            text,
			NoEcho:          bl.NoEcho,
			ContinueOnError: bl.ContinueOnError,
			Span:            bl.Sp,
		})
	}

	dir := e.taskDir(task)
	env := e.taskEnv(task)
	// Last, so that nothing else of these names reaches the body in their
	// place (spec 051).
	extraEnv = append(slices.Clip(extraEnv), e.planEnv(task, params)...)
	if len(extraEnv) > 0 {
		// A nil env means "inherit the process environment" to the executors;
		// adding entries must not silently drop that inheritance.
		if env == nil {
			env = os.Environ()
		}
		env = append(env[:len(env):len(env)], extraEnv...)
	}

	sel := rt.Select(task, e.settings)
	var err error
	if e.sandbox != nil {
		err = e.runConfined(task, sel, lines, dir, env, stdout)
		var (
			notStarted *sandboxStartError
			died       *sandboxWorkerError
		)
		switch {
		case errors.As(err, &notStarted):
			// Printed here, into the call's output, so that the agent reads
			// why nothing ran. A silent failure has no banner of its own.
			printErrorBanner(e.opts, e.theme, notStarted.Error())
			return &TaskFailure{Err: &bodyNotRunError{err: notStarted}, Silent: true}
		case errors.As(err, &died):
			printErrorBanner(e.opts, e.theme, died.Error())
			return &TaskFailure{Err: died, Silent: true}
		case err != nil && e.ctx.Err() == nil:
			fmt.Fprintln(e.opts.Stderr, hintLine(task.Name, task.Attr(ast.AttrNetwork) != nil, e.sandbox.workspace))
		}
	} else {
		err = e.runExecutor(task, sel, lines, dir, env, stdout)
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return err
	}

	// [no-exit-message] suppresses the trailing error banner (not the exit code).
	if err != nil && task.Attr(ast.AttrNoExitMessage) != nil {
		return &TaskFailure{Err: err, Silent: true}
	}
	if err == nil && task.Attr(ast.AttrPlan) != nil {
		return e.capturePlan(task, params)
	}
	return err
}

// runExecutor runs a body in this process with the executor selected for it.
func (e *engine) runExecutor(task *ast.Task, sel rt.Selection, lines []shell.Line, dir string, env []string, stdout io.Writer) error {
	switch sel.Kind {
	case rt.KindAgent:
		return e.executeAgent(task, lines, dir, env)
	case rt.KindShell:
		return shell.Run(e.ctx, task.Name, lines, shell.Options{
			Stdin:     e.opts.Stdin,
			Stdout:    stdout,
			Stderr:    e.opts.Stderr,
			Dir:       dir,
			Env:       env,
			Quiet:     e.settings.Quiet || e.opts.Quiet,
			EchoStyle: func(s string) string { return e.theme.Muted.Render(s) },
		})
	case rt.KindInterp:
		script := joinBody(lines)
		var span token.Span
		if len(task.Body) > 0 {
			span = task.Body[0].Sp
		}
		return interp.Run(e.ctx, task.Name, script, sel.Command, span, interp.Options{
			Stdin:  e.opts.Stdin,
			Stdout: stdout,
			Stderr: e.opts.Stderr,
			Dir:    dir,
			Env:    env,
		})
	}
	return &UsageError{Err: errorf("executor %q is not supported yet", sel.Display)}
}

// taskDir resolves the working directory for a task, honoring [no-cd] (run in
// the invocation directory) and [working-directory("path")] (relative to root).
func (e *engine) taskDir(task *ast.Task) string {
	if task.Attr(ast.AttrNoCD) != nil {
		if e.opts.Cwd != "" {
			return e.opts.Cwd
		}
		return e.workDir
	}
	if wd := task.Attr(ast.AttrWorkingDirectory); wd != nil && wd.Str != "" {
		if filepath.IsAbs(wd.Str) {
			return wd.Str
		}
		return filepath.Join(e.root, wd.Str)
	}
	return e.workDir
}

// taskEnv appends any [env("NAME","VALUE")] attributes to the base environment.
func (e *engine) taskEnv(task *ast.Task) []string {
	extra := envAttrPairs(task)
	if len(extra) == 0 {
		return e.env
	}
	return append(append([]string{}, e.env...), extra...)
}

// joinBody concatenates interpolated body lines into a single interpreter script.
func joinBody(lines []shell.Line) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func (e *engine) Namespace(_ *ast.Task) string { return "" }

// cacheSpec builds the cache fingerprint spec for a [cache] task.
func (e *engine) cacheSpec(task *ast.Task, params eval.Args, attr *ast.Attribute) cache.Spec {
	ev := eval.New(e.scope.WithArgs(params))
	evalAll := func(exprs []ast.Expr) []string {
		out := make([]string, 0, len(exprs))
		for _, ex := range exprs {
			if v, err := ev.Eval(ex); err == nil {
				out = append(out, v)
			}
		}
		return out
	}
	var body strings.Builder
	for _, bl := range task.Body {
		body.WriteString(bl.Raw)
		body.WriteByte('\n')
	}
	sel := rt.Select(task, e.settings)
	execID := sel.Display
	if len(sel.Command) > 0 {
		execID += ":" + strings.Join(sel.Command, " ")
	}
	return cache.Spec{
		Key:      task.Name,
		Root:     e.root,
		Inputs:   evalAll(attr.Inputs),
		Outputs:  evalAll(attr.Outputs),
		Body:     body.String(),
		Vars:     e.collectVars(task, params),
		Executor: execID,
	}
}

// collectVars resolves the values of every variable/param the task references
// (Principle I: the fingerprint must reflect interpolated values).
func (e *engine) collectVars(task *ast.Task, params eval.Args) map[string]string {
	names := map[string]bool{}
	var walk func(ex ast.Expr)
	walk = func(ex ast.Expr) {
		switch x := ex.(type) {
		case *ast.VarRef:
			names[x.Name] = true
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.FuncCall:
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Conditional:
			for _, br := range x.Branches {
				walk(br.Left)
				walk(br.Right)
				walk(br.Result)
			}
			walk(x.Else)
		}
	}
	for _, p := range task.Params {
		walk(p.Default)
	}
	for _, dep := range task.Deps {
		for _, a := range dep.Args {
			walk(a)
		}
	}
	for _, bl := range task.Body {
		for _, frag := range bodyInterpFragments(bl.Raw) {
			if ex, d := parser.ParseExprFragment(e.root, frag); !d.HasErrors() {
				walk(ex)
			}
		}
	}
	ev := eval.New(e.scope.WithArgs(params))
	out := map[string]string{}
	for name := range names {
		if v, err := ev.Eval(&ast.VarRef{Name: name}); err == nil {
			out[name] = v
		}
		// The joined text cannot tell ["a b"] from ["a", "b"], so the elements
		// get their own entry. Its "[]" suffix collides with no identifier. It
		// changes the fingerprint of every [cache] task that references a
		// variadic, which therefore runs once after upgrading (spec 030 R5).
		if elems, ok := params.Lists[name]; ok {
			out[name+"[]"] = eval.ElementsKey(elems)
		}
	}
	return out
}

// bodyInterpFragments extracts the {{ ... }} expression texts from a body line.
func bodyInterpFragments(raw string) []string {
	var out []string
	i := 0
	for i < len(raw) {
		if strings.HasPrefix(raw[i:], "{{{{") || strings.HasPrefix(raw[i:], "}}}}") {
			i += 4
			continue
		}
		if strings.HasPrefix(raw[i:], "{{") {
			end := strings.Index(raw[i+2:], "}}")
			if end < 0 {
				break
			}
			out = append(out, raw[i+2:i+2+end])
			i = i + 2 + end + 2
			continue
		}
		i++
	}
	return out
}

// classifyRunErr maps a scheduler/execution error to a CLI error class, first
// rendering any spanned diagnostic.
func (e *engine) classifyRunErr(err error) error {
	if err == nil {
		return nil
	}
	// The run context, not the error, decides an interrupt: a child that traps
	// SIGINT can exit with any status, and a killed agent CLI reads as a plain
	// failure. Wrapping keeps the banner text, and keeps a silent
	// [no-exit-message] failure silent.
	if errors.Is(e.ctx.Err(), context.Canceled) {
		return &Interrupted{Err: err}
	}
	var tf *TaskFailure
	if !errors.As(err, &tf) {
		var evErr *eval.Error
		if errors.As(err, &evErr) {
			renderDiags(e.opts, diag.List{diag.New(evErr.Span, evErr.Msg)}, e.src)
		}
		var cyc *scheduler.CycleError
		if errors.As(err, &cyc) {
			printErrorBanner(e.opts, e.theme, cyc.Error())
		}
	}
	return classify(err)
}

// ExitCode is the code rune exits with for a failed body's error, so a ||
// hook's RUNE_FAILED_EXIT_CODE always matches it (spec 028 FR-009).
func (e *engine) ExitCode(err error) int {
	return CodeFor(classify(err))
}

// classify maps a run error to the class that sets its exit code and banner,
// without printing anything.
func classify(err error) error {
	// An error already classified by runBody (e.g. a declined confirmation)
	// passes through unchanged. A silent [no-exit-message] failure keeps its
	// banner suppressed, but its code follows the error it wraps (FR-008).
	var already *TaskFailure
	if errors.As(err, &already) {
		if already.Silent {
			return &TaskFailure{Err: classify(already.Err), Silent: true}
		}
		return already
	}
	var evErr *eval.Error
	if errors.As(err, &evErr) {
		return &ValidationError{Err: err}
	}
	var cyc *scheduler.CycleError
	if errors.As(err, &cyc) {
		return &ValidationError{Err: err}
	}
	var exec *shell.ExecError
	if errors.As(err, &exec) {
		return &TaskFailure{Err: err}
	}
	var notConfigured *agent.NotConfiguredError
	if errors.As(err, &notConfigured) {
		return &UsageError{Err: err}
	}
	var notInstalled *agent.NotInstalledError
	if errors.As(err, &notInstalled) {
		return &TaskFailure{Err: err}
	}
	var authErr *agent.AuthError
	if errors.As(err, &authErr) {
		return &TaskFailure{Err: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Interrupted{Err: err}
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return err
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		return err
	}
	return &TaskFailure{Err: err}
}

// --- module helpers ---

func indexTasks(f *ast.File) map[string]*ast.Task {
	m := make(map[string]*ast.Task, len(f.Tasks))
	for _, t := range f.Tasks {
		m[t.Name] = t
	}
	return m
}

func indexAssignments(f *ast.File) map[string]*ast.Assignment {
	m := make(map[string]*ast.Assignment, len(f.Assignments))
	for _, a := range f.Assignments {
		m[a.Name] = a
	}
	return m
}

func resolveWorkDir(runefile, setting string) string {
	base := filepath.Dir(runefile)
	if setting == "" {
		return base
	}
	if filepath.IsAbs(setting) {
		return setting
	}
	return filepath.Join(base, setting)
}

// buildEnv assembles the task environment: the inherited process environment,
// any `set dotenv` file, plus — when `set export` is active — every
// successfully-resolved module variable.
//
// It also returns the entries the dotenv file gave, as this load read them:
// they are part of what a plan is made for (spec 051).
func buildEnv(settings config.Settings, scope *eval.Scope, root string) (env, dotenvPairs []string) {
	env = os.Environ()
	if settings.Dotenv != "" {
		path := settings.Dotenv
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if pairs, err := dotenv.Load(path); err == nil {
			dotenvPairs = pairs
			env = append(env, pairs...)
		}
	}
	if settings.Export {
		ev := eval.New(scope)
		names := make([]string, 0, len(scope.Assigns))
		for name := range scope.Assigns {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if v, err := ev.Eval(scope.Assigns[name].Expr); err == nil {
				env = append(env, name+"="+v)
			}
		}
	}
	return env, dotenvPairs
}

// docsURL is where the overview points users when a Runefile defines no tasks.
const docsURL = "https://github.com/rune-task-runner/rune/tree/main/docs"

// printOverview renders the screen shown for bare `rune`: a version header
// followed by the available-task listing, or a friendly pointer to --help and
// the docs when the Runefile exposes no runnable tasks.
func printOverview(opts Options, f *ast.File) {
	// The header label is themed so the overview reads as one screen with the
	// styled task list below it; the version value stays plain for copy-paste.
	th := opts.themeStdout()
	fmt.Fprintf(opts.Stdout, "%s %s\n", th.Heading.Render("rune version:"), opts.Version)
	if hasVisibleTasks(f) {
		listTasks(opts, f)
		return
	}
	fmt.Fprintln(opts.Stdout, "No available tasks found in this Runefile.")
	fmt.Fprintln(opts.Stdout, "Use 'rune --help' to see commands, and read the docs:")
	fmt.Fprintln(opts.Stdout, "  "+th.Muted.Render(docsURL))
}

// visibleOn is the single visibility predicate shared by every listing
// surface (--list, the overview, the picker, completion, MCP tools): a task
// is visible when it is non-private and available on the given OS. The MCP
// tool surface additionally excludes the [context] hook (spec 021 FR-006) —
// see mcpAdapter.Tasks; that rule is tool-specific, not a listing rule.
func visibleOn(t *ast.Task, goos string) bool {
	return !t.IsPrivate() && t.AvailableOn(goos)
}

// hasVisibleTasks reports whether the file exposes at least one non-private task
// that matches the current OS (the same visibility filter listTasks applies).
func hasVisibleTasks(f *ast.File) bool {
	for _, t := range f.Tasks {
		if visibleOn(t, runtime.GOOS) {
			return true
		}
	}
	return false
}

// visibleTasksByGroup partitions f's visible tasks (non-private, OS-matching
// per ast.Task.AvailableOn) by their group("...") attribute, in the order each group
// name first occurs. The "" key holds tasks with no group attribute. This is
// the single source of truth for group ordering/membership shared by --list
// (listTasks) and the interactive picker (pickerItems in choose.go) so the
// two surfaces can never drift apart.
func visibleTasksByGroup(f *ast.File) (order []string, groups map[string][]*ast.Task) {
	groups = map[string][]*ast.Task{}
	for _, t := range f.Tasks {
		if !visibleOn(t, runtime.GOOS) {
			continue
		}
		g := ""
		if ga := t.Attr(ast.AttrGroup); ga != nil {
			g = ga.Str
		}
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], t)
	}
	return order, groups
}

// listTasks prints non-private tasks, grouped by [group("...")], excluding tasks
// filtered out by an OS attribute that does not match the current platform.
func listTasks(opts Options, f *ast.File) {
	type row struct{ name, doc, returns string }
	order, taskGroups := visibleTasksByGroup(f)
	groups := map[string][]row{}
	width := 0
	for g, tasks := range taskGroups {
		for _, t := range tasks {
			groups[g] = append(groups[g], row{t.Name, firstLine(t.Doc), firstLine(t.Returns())})
			if len(t.Name) > width {
				width = len(t.Name)
			}
		}
	}

	// Styling is additive and stream-gated: when color is off, the theme roles
	// are no-ops AND the plain branch below reproduces the exact pre-feature
	// bytes (byte-for-byte invariance, FR-010). When color is on, the doc rows
	// pad by visible rune width so the "#" column stays aligned despite the
	// zero-width ANSI escapes (FR-012/SC-002).
	th := opts.themeStdout()
	fmt.Fprintln(opts.Stdout, th.Heading.Render("Available tasks:"))
	for _, g := range order {
		if g != "" {
			fmt.Fprintf(opts.Stdout, "  %s\n", th.Heading.Render("["+g+"]"))
		}
		for _, r := range groups[g] {
			switch {
			case r.doc == "":
				fmt.Fprintf(opts.Stdout, "    %s\n", th.TaskName.Render(r.name))
			case opts.ColorStdout:
				// Pad by visible rune width (task names are ASCII today, so this
				// equals the byte width %-*s uses, but it stays correct if the ANSI
				// styling or names ever widen).
				pad := strings.Repeat(" ", width-utf8.RuneCountInString(r.name))
				fmt.Fprintf(opts.Stdout, "    %s%s  %s\n", th.TaskName.Render(r.name), pad, th.Muted.Render("# "+r.doc))
			default:
				fmt.Fprintf(opts.Stdout, "    %-*s  # %s\n", width, r.name, r.doc)
			}
			// The [returns] outcome description gets an aligned continuation
			// line (spec 023 FR-010); unannotated tasks stay byte-identical.
			if r.returns != "" {
				if opts.ColorStdout {
					pad := strings.Repeat(" ", width)
					fmt.Fprintf(opts.Stdout, "    %s  %s\n", pad, th.Muted.Render("# returns: "+r.returns))
				} else {
					fmt.Fprintf(opts.Stdout, "    %-*s  # returns: %s\n", width, "", r.returns)
				}
			}
		}
	}
}

// displayOS renders a GOOS value in the Runefile attribute vocabulary so
// diagnostics never mix internal platform names with attribute names
// ("darwin" is written [macos] in a Runefile).
func displayOS(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- diagnostics ---

func newSourceProvider(mainPath string, mainSrc []byte) diag.SourceProvider {
	cache := map[string][]byte{mainPath: mainSrc}
	return func(path string) ([]byte, bool) {
		if b, ok := cache[path]; ok {
			return b, true
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		cache[path] = b
		return b, true
	}
}

func renderDiags(opts Options, diags diag.List, src diag.SourceProvider) {
	fmt.Fprintln(opts.Stderr, diag.RenderAll(diags, src, opts.themeStderr()))
}

func countErrors(diags diag.List) int {
	n := 0
	for _, d := range diags {
		if d.Severity == diag.Error {
			n++
		}
	}
	return n
}

// errorDiags returns the diagnostics that are errors, in their order.
func errorDiags(diags diag.List) diag.List {
	var errs diag.List
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}
