package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
	srcProvider := newSourceProvider(runefile, source)

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
	env := buildEnv(settings, scope, root)

	plan := planRun
	switch {
	case opts.DryRun:
		plan = planDryRun
	case opts.Summary:
		plan = planSummary
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
	}

	invs, err := eng.resolveRoots(rawInvs)
	if err != nil {
		return err
	}

	if err := scheduler.Run(eng, invs); err != nil {
		// maskErr covers the error text itself: the "rune: ..." banner is
		// printed by callers outside the masked writers.
		return maskErr(eng.classifyRunErr(err), set)
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
// OS-mismatched targets are returned unevaluated (nil params): the scheduler
// skips them, so their args and defaults must not run on this host.
func (e *engine) ResolveDep(curTask *ast.Task, curParams map[string]string, dep *ast.DepCall) (*ast.Task, map[string]string, error) {
	target, ok := e.tasks[dep.Name]
	if !ok {
		return nil, nil, &ValidationError{Err: errorf("unknown dependency %q of task %q", dep.Name, curTask.Name)}
	}
	// The scheduler skips OS-mismatched targets silently, so don't evaluate
	// their args or parameter defaults: those may only resolve on the matching
	// host (e.g. a [windows] dep defaulting to env("APPDATA") must not abort
	// the dispatch pattern on linux).
	if !e.Available(target) {
		return target, nil, nil
	}
	ev := eval.New(e.scope.WithParams(curParams))
	pos := make([]string, len(dep.Args))
	for i, a := range dep.Args {
		v, evErr := ev.Eval(a)
		if evErr != nil {
			return nil, nil, &ValidationError{Err: evErr}
		}
		pos[i] = v
	}
	params, err := bindParams(target, pos, e.scope)
	if err != nil {
		return nil, nil, err
	}
	return target, params, nil
}

// Execute handles plan modes, confirmation, and caching, then runs the body.
func (e *engine) Execute(task *ast.Task, params map[string]string) error {
	if e.plan == planSummary {
		fmt.Fprintln(e.opts.Stdout, task.Name)
		return nil
	}

	if c := task.Attr(ast.AttrConfirm); c != nil && e.plan == planRun {
		if !e.opts.Yes && !e.confirm(task, c.Str) {
			// The body never started, so || failure hooks must not fire
			// (FR-003): the wrapper lets the scheduler recognize this via
			// scheduler.ErrBodyNotRun without changing the rendered message.
			return &TaskFailure{Err: &bodyNotRunError{err: errorf("task %q was not confirmed", task.Name)}, Silent: true}
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
func (e *engine) ExecuteFailHook(task *ast.Task, params map[string]string, f scheduler.Failure) error {
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
	if c := task.Attr(ast.AttrConfirm); c != nil {
		if !e.opts.Yes && !e.confirm(task, c.Str) {
			return &TaskFailure{Err: errorf("task %q was not confirmed", task.Name), Silent: true}
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
func (e *engine) runBody(task *ast.Task, params map[string]string) error {
	return e.runBodyTo(task, params, e.opts.Stdout, nil)
}

// runBodyTo is runBody with an explicit stdout sink and optional extra
// environment entries; || failure hooks on the MCP path redirect their body
// stdout into the fix-suggestion capture, and every hook run appends the
// failure context (contracts/failure-env.md). extraEnv comes last so it wins
// over static declarations of the same names.
func (e *engine) runBodyTo(task *ast.Task, params map[string]string, stdout io.Writer, extraEnv []string) error {
	ev := eval.New(e.scope.WithParams(params))

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
	switch sel.Kind {
	case rt.KindAgent:
		err = e.executeAgent(task, lines, dir, env)
	case rt.KindShell:
		err = shell.Run(e.ctx, task.Name, lines, shell.Options{
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
		err = interp.Run(e.ctx, task.Name, script, sel.Command, span, interp.Options{
			Stdin:  e.opts.Stdin,
			Stdout: stdout,
			Stderr: e.opts.Stderr,
			Dir:    dir,
			Env:    env,
		})
	default:
		return &UsageError{Err: errorf("executor %q is not supported yet", sel.Display)}
	}

	// [no-exit-message] suppresses the trailing error banner (not the exit code).
	if err != nil && task.Attr(ast.AttrNoExitMessage) != nil {
		return &TaskFailure{Err: err, Silent: true}
	}
	return err
}

// confirm prompts the operator to approve a destructive ([confirm]) task. It
// returns false on any non-affirmative answer or when stdin is unavailable.
func (e *engine) confirm(task *ast.Task, prompt string) bool {
	if e.opts.Stdin == nil {
		return false
	}
	if prompt == "" {
		prompt = fmt.Sprintf("Run %q?", task.Name)
	}
	// A destructive-action prompt uses the warning role; the [y/N] hint stays
	// plain so the input convention is never obscured by styling (014 C3).
	fmt.Fprintf(e.opts.Stderr, "%s [y/N] ", e.theme.Warning.Render(prompt))
	reader := bufio.NewReader(e.opts.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
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
func (e *engine) cacheSpec(task *ast.Task, params map[string]string, attr *ast.Attribute) cache.Spec {
	ev := eval.New(e.scope.WithParams(params))
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
func (e *engine) collectVars(task *ast.Task, params map[string]string) map[string]string {
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
	ev := eval.New(e.scope.WithParams(params))
	out := map[string]string{}
	for name := range names {
		if v, err := ev.Eval(&ast.VarRef{Name: name}); err == nil {
			out[name] = v
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
	// An error already classified by runBody (e.g. a Silent [no-exit-message]
	// failure or a declined confirmation) passes through unchanged.
	var already *TaskFailure
	if errors.As(err, &already) {
		return already
	}
	var evErr *eval.Error
	if errors.As(err, &evErr) {
		renderDiags(e.opts, diag.List{diag.New(evErr.Span, evErr.Msg)}, e.src)
		return &ValidationError{Err: err}
	}
	var cyc *scheduler.CycleError
	if errors.As(err, &cyc) {
		printErrorBanner(e.opts, e.theme, cyc.Error())
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
func buildEnv(settings config.Settings, scope *eval.Scope, root string) []string {
	env := os.Environ()
	if settings.Dotenv != "" {
		path := settings.Dotenv
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if pairs, err := dotenv.Load(path); err == nil {
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
	return env
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
	type row struct{ name, doc string }
	order, taskGroups := visibleTasksByGroup(f)
	groups := map[string][]row{}
	width := 0
	for g, tasks := range taskGroups {
		for _, t := range tasks {
			groups[g] = append(groups[g], row{t.Name, firstLine(t.Doc)})
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
