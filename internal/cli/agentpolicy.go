package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/mask"
)

// A task's [agent-allow] names the one value an agent run may have for each
// parameter it lists (spec 050). Everything in this file is that check: the
// comparison, the walk over what a call can reach before it starts, and the
// texts of a refusal.

// agentRunVar marks the environment of every body an agent run starts. A
// rune that a body runs reads it and treats its own run as an agent run, so
// a body is not a way around a policy. The body is the author's text and the
// agent supplies only arguments, which is why an environment variable is
// enough. The agent program of an agent task that runs inside an agent run
// inherits it like any body. Nothing is promised for that program's own
// shell, which can clear it.
const agentRunVar = "RUNE_AGENT_RUN"

const (
	// maxPolicyValueRunes cuts a value shown in a refusal. Values are
	// compared whole before any text is written.
	maxPolicyValueRunes = 64
	// maxPolicyReasonRunes cuts the evaluation error of an edge that did
	// not resolve.
	maxPolicyReasonRunes = 200
)

// agentRunEnv returns base, or the process environment when base is nil,
// with the agent-run mark added. It never writes into base.
func agentRunEnv(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	return append(slices.Clip(base), agentRunVar+"=1")
}

// maskerFor returns the function that masks the secrets of set in a string.
// MaskString needs a set, so an empty one passes text through.
func maskerFor(set *mask.Set) func(string) string {
	return func(s string) string {
		if set.Empty() {
			return s
		}
		return set.MaskString(s)
	}
}

// refusedValue is one predicate a run does not meet, with the value the task
// would run with.
type refusedValue struct{ name, value string }

// policyBreach returns the predicates of t's policy that args do not meet,
// in the attribute's order. The comparison is on the whole string.
func policyBreach(t *ast.Task, args eval.Args) []refusedValue {
	var refused []refusedValue
	for _, p := range t.AgentPolicy() {
		if value := args.Values[p.Name]; value != p.Value {
			refused = append(refused, refusedValue{name: p.Name, value: value})
		}
	}
	return refused
}

type denialKind int

const (
	denialBreach     denialKind = iota // a restricted task would run with a refused value
	denialVolatile                     // the value can differ between the check and the run
	denialUnresolved                   // the arguments on the way to a policy do not resolve
)

// policyDenial is why an agent run is refused.
type policyDenial struct {
	kind   denialKind
	called string // the task the agent called
	task   string // the restricted task, or the target of the edge that did not resolve
	owner  string // the task whose edge did not resolve
	// refused holds the predicates not met, with their values for a breach
	// and without for a volatile value.
	refused []refusedValue
	policy  []*ast.AllowPredicate
	reason  string // the evaluation error of an edge that did not resolve
}

// text is the refusal an agent reads. hide masks secret values and may be
// nil. Every value is masked before it is cut, so a cut cannot leave part of
// a secret behind.
func (d *policyDenial) text(hide func(string) string) string {
	if hide == nil {
		hide = func(s string) string { return s }
	}
	if d.kind == denialUnresolved {
		reason, cut := cutRunes(printableLine(hide(d.reason)), maxPolicyReasonRunes)
		if cut {
			reason += "..."
		}
		return fmt.Sprintf("task %q was not run: the arguments %q passes to %q could not be worked out before the run (%s), and an agent policy has to be checked on that path",
			d.called, d.owner, d.task, reason)
	}

	required := make([]string, len(d.policy))
	for i, p := range d.policy {
		required[i] = p.Name + "=" + renderPolicyValue(p.Value, hide)
	}
	requires := "agent policy requires " + strings.Join(required, ", ")

	if d.kind == denialVolatile {
		names := make([]string, len(d.refused))
		for i, r := range d.refused {
			names[i] = r.name
		}
		return fmt.Sprintf("task %q runs %q with a value of %s that is not known before the run; %s",
			d.called, d.task, strings.Join(names, ", "), requires)
	}

	with := make([]string, len(d.refused))
	for i, r := range d.refused {
		with[i] = r.name + "=" + renderPolicyValue(r.value, hide)
	}
	if d.called == d.task {
		return fmt.Sprintf("task %q is not allowed for agents with %s; %s", d.task, strings.Join(with, ", "), requires)
	}
	return fmt.Sprintf("task %q runs %q, which is not allowed for agents with %s; %s",
		d.called, d.task, strings.Join(with, ", "), requires)
}

// renderPolicyValue writes one value for a refusal: masked, cut, and quoted
// so it stays on its line.
func renderPolicyValue(value string, hide func(string) string) string {
	if hide != nil {
		value = hide(value)
	}
	value, cut := cutRunes(value, maxPolicyValueRunes)
	quoted := strconv.Quote(value)
	if cut {
		quoted += "..."
	}
	return quoted
}

// printableLine writes every character that is not printable as its Go
// escape, so the text stays on one line. An evaluation error can carry text
// the agent sent, and that covers more than control characters: a direction
// override is not one, and can reorder what a person reads. It is the rule
// mcpserver applies to an approval request. That package is public and
// imports nothing internal, so the two are deliberate copies, like cutRunes.
func printableLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strconv.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRune(r)
		b.WriteString(quoted[1 : len(quoted)-1])
	}
	return b.String()
}

// cutRunes returns the first n characters of s and whether any were left
// out.
func cutRunes(s string, n int) (string, bool) {
	count := 0
	for i := range s {
		if count == n {
			return s[:i], true
		}
		count++
	}
	return s, false
}

// builtinVolatility says whether a built-in function can return another
// value when a run evaluates it again after the check: a new random value, a
// later time, or the state of a file or directory a task of the same run can
// change. known is false for a name in neither list, which then counts as
// volatile: a new function has to be classified here, and a test holds the
// lists to eval.BuiltinNames.
func builtinVolatility(name string) (volatile, known bool) {
	switch name {
	case "uuid", "datetime", "path_exists", "read", "sha256_file", "which", "require", "absolute_path":
		return true, true
	case "env", "os", "arch", "os_family", "num_cpus",
		"join", "clean", "extension", "file_name", "file_stem", "parent_dir",
		"uppercase", "lowercase", "trim", "trim_start", "trim_end", "capitalize",
		"replace", "replace_regex", "sha256", "error", "quote", "quote_each":
		return false, true
	}
	return true, false
}

// policyWalk checks what one call can reach before the call starts.
type policyWalk struct {
	e *engine
	// scope is a fork of the engine's: what the walk reads is not cached for
	// the run, which must read a module variable when it gets there. Set
	// once the called task is known to reach a policy.
	scope  *eval.Scope
	called string
	seen   map[string]bool // runs already walked
	reach  map[string]bool // per task: has a policy or can reach one
	vars   map[string]bool // per module variable: volatile
}

func newPolicyWalk(e *engine, called string) *policyWalk {
	return &policyWalk{
		e:      e,
		called: called,
		seen:   map[string]bool{},
		reach:  map[string]bool{},
		vars:   map[string]bool{},
	}
}

// preflightPolicy walks what a run of root with args would reach, with the
// arguments the Runefile passes along each dependency and hook, and returns
// the first run that breaks an agent policy, or nil. It runs nothing.
//
// A Runefile with no policy on the way is not walked at all, so an
// evaluation error there stays where it was, in the run.
func (e *engine) preflightPolicy(root *ast.Task, args eval.Args) *policyDenial {
	w := newPolicyWalk(e, root.Name)
	if !w.reaches(root.Name) {
		return nil
	}
	w.scope = e.scope.Fork()
	// The called task is bound once and the run uses those arguments, so
	// none of them can differ.
	fixed := make(map[string]bool, len(root.Params))
	for _, p := range root.Params {
		fixed[p.Name] = false
	}
	return w.visit(root, args, fixed, false)
}

// reaches reports whether the named task has a policy or can run a task that
// has one. It reads names only, so it is the same on every host.
func (w *policyWalk) reaches(name string) bool {
	if known, ok := w.reach[name]; ok {
		return known
	}
	task, ok := w.e.tasks[name]
	if !ok {
		return false
	}
	// Set before the edges are read: the analyzer rejects a cycle, and this
	// keeps a walk over one from recursing.
	w.reach[name] = false
	found := len(task.AgentPolicy()) > 0
	for _, edge := range task.Edges() {
		if found {
			break
		}
		found = w.reaches(edge.Name)
	}
	w.reach[name] = found
	return found
}

// visit checks one run and then what it runs, in the scheduler's order:
// dependencies, && hooks, then || hooks. A task run as a || hook does not
// run its own || hooks, so failHook leaves them out.
func (w *policyWalk) visit(task *ast.Task, args eval.Args, volatile map[string]bool, failHook bool) *policyDenial {
	key := w.runKey(task, args, volatile, failHook)
	if w.seen[key] {
		return nil
	}
	w.seen[key] = true

	if d := w.check(task, args, volatile); d != nil {
		return d
	}
	if d := w.visitEdges(task, args, volatile, task.Deps, false); d != nil {
		return d
	}
	if d := w.visitEdges(task, args, volatile, task.PostHooks, false); d != nil {
		return d
	}
	if failHook {
		return nil
	}
	return w.visitEdges(task, args, volatile, task.FailHooks, true)
}

func (w *policyWalk) visitEdges(task *ast.Task, args eval.Args, volatile map[string]bool, edges []*ast.DepCall, failHooks bool) *policyDenial {
	for _, edge := range edges {
		target, ok := w.e.tasks[edge.Name]
		// The run skips a task unavailable on this host, and a subtree with
		// no policy has nothing to check.
		if !ok || !w.e.Available(target) || !w.reaches(target.Name) {
			continue
		}
		_, targetArgs, err := w.e.resolveDepIn(w.scope, task, args, edge)
		if err != nil {
			// The target cannot be bound, so it cannot be walked: whichever
			// argument failed, the policy below it is unchecked.
			return &policyDenial{kind: denialUnresolved, called: w.called, owner: task.Name, task: target.Name, reason: err.Error()}
		}
		if d := w.visit(target, targetArgs, w.volatility(volatile, edge, target), failHooks); d != nil {
			return d
		}
	}
	return nil
}

// runKey names one walked run. The volatile parameters are part of it: two
// edges can resolve to the same arguments now while only one of them can
// resolve to others later.
func (w *policyWalk) runKey(task *ast.Task, args eval.Args, volatile map[string]bool, failHook bool) string {
	var names []string
	for name, is := range volatile {
		if is {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return fmt.Sprintf("%s(%s)|%v|%s", task.Name, args.Key(), failHook, strings.Join(names, ","))
}

// check compares one run with its task's policy. A volatile parameter is
// refused before its value is looked at: the value seen now proves nothing.
func (w *policyWalk) check(task *ast.Task, args eval.Args, volatile map[string]bool) *policyDenial {
	policy := task.AgentPolicy()
	if len(policy) == 0 {
		return nil
	}
	var unknown []refusedValue
	for _, p := range policy {
		if volatile[p.Name] {
			unknown = append(unknown, refusedValue{name: p.Name})
		}
	}
	if len(unknown) > 0 {
		return &policyDenial{kind: denialVolatile, called: w.called, task: task.Name, refused: unknown, policy: policy}
	}
	if refused := policyBreach(task, args); len(refused) > 0 {
		return &policyDenial{kind: denialBreach, called: w.called, task: task.Name, refused: refused, policy: policy}
	}
	return nil
}

// volatility says, for each parameter of target, whether the value an edge
// gives it can differ when the run resolves the edge again. caller is the
// same for the task that owns the edge.
func (w *policyWalk) volatility(caller map[string]bool, edge *ast.DepCall, target *ast.Task) map[string]bool {
	out := make(map[string]bool, len(target.Params))
	for i, p := range target.Params {
		switch {
		case p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar:
			// Set also with no element: the parameter is then empty, and it
			// still shadows a module variable of its name.
			out[p.Name] = false
			for _, arg := range edge.Args[min(i, len(edge.Args)):] {
				out[p.Name] = out[p.Name] || w.volatileExpr(arg, caller)
			}
		case i < len(edge.Args):
			out[p.Name] = w.volatileExpr(edge.Args[i], caller)
		default:
			// A default sees the target's earlier parameters, not the
			// caller's.
			out[p.Name] = w.volatileExpr(p.Default, out)
		}
	}
	return out
}

// volatileExpr reports whether an expression can have another value when the
// run evaluates it again. params holds the parameters in scope: they shadow
// module variables, as they do when the expression is evaluated.
func (w *policyWalk) volatileExpr(e ast.Expr, params map[string]bool) bool {
	switch x := e.(type) {
	case *ast.VarRef:
		if volatile, isParam := params[x.Name]; isParam {
			return volatile
		}
		return w.volatileVar(x.Name)
	case *ast.Binary:
		return w.volatileExpr(x.Left, params) || w.volatileExpr(x.Right, params)
	case *ast.FuncCall:
		if volatile, _ := builtinVolatility(x.Name); volatile {
			return true
		}
		for _, arg := range x.Args {
			if w.volatileExpr(arg, params) {
				return true
			}
		}
	case *ast.Conditional:
		for _, br := range x.Branches {
			if w.volatileExpr(br.Left, params) || w.volatileExpr(br.Right, params) || w.volatileExpr(br.Result, params) {
				return true
			}
		}
		return w.volatileExpr(x.Else, params)
	}
	return false
}

// volatileVar reports whether a module variable's assignment can give
// another value when the run first reads it. The walk reads variables on its
// own fork, so the run still reads them itself.
func (w *policyWalk) volatileVar(name string) bool {
	if known, ok := w.vars[name]; ok {
		return known
	}
	// A run-time override replaces the assignment with a fixed string.
	if _, overridden := w.e.scope.Overrides[name]; overridden {
		return false
	}
	assign, ok := w.e.scope.Assigns[name]
	if !ok {
		return false
	}
	// Set first: an assignment cycle is an evaluation error, not a value.
	w.vars[name] = false
	w.vars[name] = w.volatileExpr(assign.Expr, nil)
	return w.vars[name]
}

// policyGate is the check where a restricted task's body would start in an
// agent run. The walk before the run should make it unreachable: it is what
// keeps a restricted task from running with a refused value if the walk and
// the scheduler ever disagree.
func (e *engine) policyGate(task *ast.Task, args eval.Args) *TaskFailure {
	if !e.agent {
		return nil
	}
	refused := policyBreach(task, args)
	if len(refused) == 0 {
		return nil
	}
	d := &policyDenial{kind: denialBreach, called: task.Name, task: task.Name, refused: refused, policy: task.AgentPolicy()}
	return &TaskFailure{Err: errors.New(d.text(e.mask))}
}
