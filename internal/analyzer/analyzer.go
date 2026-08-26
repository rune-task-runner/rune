// Package analyzer is the whole-file semantic analyzer. It runs before any task
// executes (Principle II / FR-012, FR-014) and reports every statically
// detectable error with a file:line:col span: undefined names, unknown
// tasks/dependencies, dependency cycles, arity mismatches, duplicate settings,
// and malformed parameter lists.
package analyzer

import (
	"fmt"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/token"
)

// Analyze validates the file and returns all diagnostics found.
func Analyze(f *ast.File) diag.List {
	a := &analyzer{
		file:  f,
		vars:  map[string]bool{},
		tasks: map[string]*ast.Task{},
	}
	a.collect()
	a.checkContext()
	a.checkExpressions()
	a.checkDependencies()
	a.checkCycles()
	return a.diags
}

type analyzer struct {
	file  *ast.File
	diags diag.List
	vars  map[string]bool
	tasks map[string]*ast.Task
}

// collect builds the name tables and reports duplicates, malformed parameter
// lists, and duplicate settings.
func (a *analyzer) collect() {
	settingSeen := map[string]token.Span{}
	for _, s := range a.file.Settings {
		if prev, ok := settingSeen[s.Name]; ok {
			a.diags.Errorf(s.Sp, "duplicate setting %q (already set at %s)", s.Name, prev.Start)
			continue
		}
		settingSeen[s.Name] = s.Sp
	}

	for _, v := range a.file.Assignments {
		if a.vars[v.Name] {
			a.diags.Errorf(v.Sp, "duplicate variable %q", v.Name)
			continue
		}
		a.vars[v.Name] = true
	}

	for _, t := range a.file.Tasks {
		if _, ok := a.tasks[t.Name]; ok {
			a.diags.Codef(diag.CodeDuplicateTask, t.Sp, "duplicate task %q", t.Name)
			continue
		}
		a.tasks[t.Name] = t
		a.checkParams(t)
	}
}

// checkParams enforces: unique names, at most one variadic (and it must be
// last), and defaulted params follow required ones.
func (a *analyzer) checkParams(t *ast.Task) {
	seen := map[string]bool{}
	sawDefaulted := false
	for i, p := range t.Params {
		if seen[p.Name] {
			a.diags.Codef(diag.CodeDuplicateParam, p.Sp, "duplicate parameter %q in task %q", p.Name, t.Name)
		}
		seen[p.Name] = true

		isVariadic := p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar
		if isVariadic && i != len(t.Params)-1 {
			a.diags.Errorf(p.Sp, "variadic parameter %q must be the last parameter", p.Name)
		}
		switch p.Kind {
		case ast.ParamDefaulted:
			sawDefaulted = true
		case ast.ParamRequired:
			if sawDefaulted {
				a.diags.Errorf(p.Sp, "required parameter %q cannot follow a defaulted parameter", p.Name)
			}
		}
	}
}

// checkContext enforces the [context] hook contract (spec 021 FR-001,
// FR-007): at most one context task in the composed file, and the hook must
// be runnable unattended — no [confirm], no parameter without a default,
// and not an agent task (which would recurse into an agent session). The
// unattended rules apply to the hook's whole dependency closure: the hook
// runs with no stdin and no operator, so a [confirm] or agent-executor task
// anywhere beneath it would silently doom every run.
func (a *analyzer) checkContext() {
	var first *ast.Task
	for _, t := range a.file.Tasks {
		attr := t.Attr(ast.AttrContext)
		if attr == nil {
			continue
		}
		if first != nil {
			a.diags.Add(diag.New(attr.Span(),
				fmt.Sprintf("duplicate [context] task %q (already declared on %q)", t.Name, first.Name)).
				WithCode(diag.CodeInvalidAttribute).
				WithRelated(diag.RelatedLocation{Span: first.Sp, Message: "first [context] task declared here"}))
			continue
		}
		first = t
		if t.Attr(ast.AttrConfirm) != nil {
			a.diags.Codef(diag.CodeInvalidAttribute, attr.Span(), "[context] task %q cannot use [confirm]: the hook runs unattended", t.Name)
		}
		if t.Executor == ast.ExecAgent {
			a.diags.Codef(diag.CodeInvalidAttribute, attr.Span(), "[context] task %q cannot use the agent executor", t.Name)
		}
		for _, p := range t.Params {
			if p.Kind == ast.ParamRequired || p.Kind == ast.ParamVariadicPlus {
				a.diags.Codef(diag.CodeInvalidAttribute, p.Sp, "parameter %q of [context] task %q must have a default: the hook is invoked with no arguments", p.Name, t.Name)
			}
		}
		a.checkContextClosure(t)
	}
}

// checkContextClosure walks the [context] hook's dependency/post-hook
// closure and rejects tasks the unattended hook could never run: a
// [confirm] task auto-declines (the hook engine has no stdin), and an
// agent-executor task would spawn an agent session from inside context
// prep. Unknown dependency names are left to checkDependencies.
func (a *analyzer) checkContextClosure(hook *ast.Task) {
	visited := map[string]bool{hook.Name: true}
	queue := hook.Edges()
	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]
		t, ok := a.tasks[dep.Name]
		if !ok || visited[t.Name] {
			continue
		}
		visited[t.Name] = true
		if t.Attr(ast.AttrConfirm) != nil {
			a.diags.Codef(diag.CodeInvalidAttribute, dep.Sp, "dependency %q of [context] task %q cannot use [confirm]: the hook runs unattended", t.Name, hook.Name)
		}
		if t.Executor == ast.ExecAgent {
			a.diags.Codef(diag.CodeInvalidAttribute, dep.Sp, "dependency %q of [context] task %q cannot use the agent executor", t.Name, hook.Name)
		}
		queue = append(queue, t.Edges()...)
	}
}

// checkExpressions resolves every name reference in the file.
func (a *analyzer) checkExpressions() {
	// Module-level expressions see only module variables.
	for _, v := range a.file.Assignments {
		a.resolveExpr(v.Expr, nil)
	}
	for _, s := range a.file.Settings {
		a.resolveExpr(s.Value, nil)
		for _, e := range s.List {
			a.resolveExpr(e, nil)
		}
	}

	// Task expressions also see the task's parameters.
	for _, t := range a.file.Tasks {
		params := paramSet(t)
		for _, p := range t.Params {
			a.resolveExpr(p.Default, params)
		}
		for _, dep := range t.Edges() {
			for _, arg := range dep.Args {
				a.resolveExpr(arg, params)
			}
		}
		for _, attr := range t.Attributes {
			for _, e := range attr.Inputs {
				a.resolveExpr(e, params)
			}
			for _, e := range attr.Outputs {
				a.resolveExpr(e, params)
			}
		}
		for _, bl := range t.Body {
			a.resolveInterpolations(bl, params)
		}
	}
}

// resolveExpr walks an expression, reporting undefined variables and unknown
// functions. validParams is the set of in-scope task params (nil at module
// level).
func (a *analyzer) resolveExpr(e ast.Expr, validParams map[string]bool) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.StringLit:
		return
	case *ast.VarRef:
		if !a.vars[x.Name] && !validParams[x.Name] {
			a.diags.Codef(diag.CodeUndefinedVariable, x.Sp, "undefined variable: %s", x.Name)
		}
	case *ast.Binary:
		a.resolveExpr(x.Left, validParams)
		a.resolveExpr(x.Right, validParams)
	case *ast.FuncCall:
		if !eval.IsBuiltin(x.Name) {
			a.diags.Errorf(x.Sp, "unknown function: %s", x.Name)
		}
		for _, arg := range x.Args {
			a.resolveExpr(arg, validParams)
		}
	case *ast.Conditional:
		for _, br := range x.Branches {
			a.resolveExpr(br.Left, validParams)
			a.resolveExpr(br.Right, validParams)
			a.resolveExpr(br.Result, validParams)
		}
		a.resolveExpr(x.Else, validParams)
	}
}

// checkDependencies resolves dependency/post-hook/fail-hook target names and
// arity. Fail-hook targets additionally must not reach the agent executor
// anywhere in their closure: a post-mortem must never recursively require an
// agent session (spec 022 FR-011).
func (a *analyzer) checkDependencies() {
	for _, t := range a.file.Tasks {
		for _, dep := range t.Edges() {
			target, ok := a.tasks[dep.Name]
			if !ok {
				a.diags.Codef(diag.CodeUnknownDependency, dep.Sp, "unknown task: %s", dep.Name)
				continue
			}
			a.checkArity(target, len(dep.Args), dep.Sp)
		}
		for _, dep := range t.FailHooks {
			a.checkFailHookClosure(t, dep)
		}
	}
}

// checkFailHookClosure rejects a || hook whose reachable task closure
// contains an agent-executor task (RUNE2011, FR-011). During a hook run the
// hook's dependencies and && post-hooks execute under normal semantics, so
// checking only the direct target would still let a post-mortem spawn an
// agent session through them — the same transitive rule as the [context]
// closure walk. Unknown names are left to checkDependencies.
func (a *analyzer) checkFailHookClosure(t *ast.Task, dep *ast.DepCall) {
	start, ok := a.tasks[dep.Name]
	if !ok {
		return
	}
	visited := map[string]bool{}
	queue := []*ast.Task{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur.Name] {
			continue
		}
		visited[cur.Name] = true
		if cur.Executor == ast.ExecAgent {
			if cur == start {
				a.diags.Codef(diag.CodeInvalidFailureHook, dep.Sp, "failure hook %q of task %q cannot use the agent executor", start.Name, t.Name)
			} else {
				a.diags.Codef(diag.CodeInvalidFailureHook, dep.Sp, "failure hook %q of task %q reaches task %q, which uses the agent executor", start.Name, t.Name, cur.Name)
			}
			return
		}
		for _, e := range cur.Edges() {
			if next, ok := a.tasks[e.Name]; ok && !visited[next.Name] {
				queue = append(queue, next)
			}
		}
	}
}

func (a *analyzer) checkArity(target *ast.Task, argc int, span token.Span) {
	required := 0
	hasVariadic := false
	variadicPlus := false
	for _, p := range target.Params {
		switch p.Kind {
		case ast.ParamRequired:
			required++
		case ast.ParamVariadicPlus:
			hasVariadic = true
			variadicPlus = true
		case ast.ParamVariadicStar:
			hasVariadic = true
		}
	}
	min := required
	if variadicPlus {
		min++
	}
	if argc < min {
		a.diags.Codef(diag.CodeWrongArgCount, span, "task %q expects at least %d argument(s), got %d", target.Name, min, argc)
		return
	}
	if !hasVariadic && argc > len(target.Params) {
		a.diags.Codef(diag.CodeWrongArgCount, span, "task %q accepts at most %d argument(s), got %d", target.Name, len(target.Params), argc)
	}
}

// checkCycles detects dependency cycles (deps + post-hooks) and reports the path.
func (a *analyzer) checkCycles() {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string

	var visit func(name string) bool
	visit = func(name string) bool {
		t, ok := a.tasks[name]
		if !ok {
			return false // unknown task already reported
		}
		color[name] = gray
		stack = append(stack, name)
		for _, dep := range t.Edges() {
			switch color[dep.Name] {
			case gray:
				a.reportCycle(t.Sp, stack, dep.Name)
				stack = stack[:len(stack)-1]
				color[name] = black
				return true
			case white:
				if _, known := a.tasks[dep.Name]; known {
					if visit(dep.Name) {
						stack = stack[:len(stack)-1]
						color[name] = black
						return true
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return false
	}

	for _, t := range a.file.Tasks {
		if color[t.Name] == white {
			if visit(t.Name) {
				return // report one cycle to avoid duplicate noise
			}
		}
	}
}

// reportCycle emits the RUNE2003 dependency-cycle diagnostic, attaching every
// task in the cycle as a related location (spec FR-009).
func (a *analyzer) reportCycle(at token.Span, stack []string, back string) {
	path := cycleNodes(stack, back)
	d := diag.Diagnostic{
		Severity: diag.Error,
		Span:     at,
		Message:  "dependency cycle: " + arrowJoin(path),
		Code:     diag.CodeDependencyCycle,
	}
	for _, n := range path {
		if tk := a.tasks[n]; tk != nil {
			d.Related = append(d.Related, diag.RelatedLocation{Span: tk.Sp, Message: n})
		}
	}
	a.diags.Add(d)
}

// cycleNodes returns the cycle path (including the closing node) from where
// `back` re-enters the stack.
func cycleNodes(stack []string, back string) []string {
	start := 0
	for i, n := range stack {
		if n == back {
			start = i
			break
		}
	}
	return append(append([]string{}, stack[start:]...), back)
}

// arrowJoin renders a task path as "a → b → c".
func arrowJoin(path []string) string {
	out := path[0]
	for _, n := range path[1:] {
		out += " → " + n
	}
	return out
}

func paramSet(t *ast.Task) map[string]bool {
	m := make(map[string]bool, len(t.Params))
	for _, p := range t.Params {
		m[p.Name] = true
	}
	return m
}

// resolveInterpolations extracts {{ ... }} expressions from a body line and
// resolves them, attaching diagnostics to the precise interpolation span.
func (a *analyzer) resolveInterpolations(bl *ast.BodyLine, params map[string]bool) {
	for _, seg := range extractInterps(bl.Raw, bl.Sp) {
		expr, diags := parseFragment(seg.text, seg.span)
		if diags.HasErrors() {
			a.diags.Errorf(seg.span, "invalid interpolation: %s", diags[0].Message)
			continue
		}
		a.resolveExprAt(expr, params, seg.span)
	}
}

// resolveExprAt is like resolveExpr but reports every diagnostic at reportSpan
// (used for interpolations, whose inner spans are relative to the fragment).
func (a *analyzer) resolveExprAt(e ast.Expr, params map[string]bool, reportSpan token.Span) {
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.VarRef:
			if !a.vars[x.Name] && !params[x.Name] {
				a.diags.Codef(diag.CodeUndefinedVariable, reportSpan, "undefined variable: %s", x.Name)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.FuncCall:
			if !eval.IsBuiltin(x.Name) {
				a.diags.Errorf(reportSpan, "unknown function: %s", x.Name)
			}
			for _, arg := range x.Args {
				walk(arg)
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
	walk(e)
}
