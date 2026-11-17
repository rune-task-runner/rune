package analyzer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/suggest"
	"github.com/rune-task-runner/rune/internal/token"
)

// planArguments are the names [plan(...)] takes, in the order a diagnostic
// lists them.
var planArguments = []string{"file", "summary", "tool", "expires"}

// checkPlanBinding validates [plan] and [applies] (spec 051, RUNE2020). A
// binding Rune cannot enforce as written is an error, never dropped: a
// Runefile that reads as applying only reviewed plans must do so.
func (a *analyzer) checkPlanBinding() {
	for _, t := range a.file.Tasks {
		plan := a.firstOf(t, ast.AttrPlan)
		applies := a.firstOf(t, ast.AttrApplies)
		if plan != nil && applies != nil {
			a.planErrorf(applies.Sp, "task %q cannot both make a plan and apply one", t.Name)
		}
		for _, attr := range []*ast.Attribute{plan, applies} {
			if attr == nil {
				continue
			}
			for _, other := range []string{ast.AttrCache, ast.AttrReadonly, ast.AttrContext} {
				if t.Attr(other) != nil {
					a.planErrorf(attr.Sp, "[%s] cannot be combined with [%s] on task %q", attr.Kind, other, t.Name)
				}
			}
			if t.Executor == ast.ExecAgent {
				a.planErrorf(attr.Sp, "[%s] cannot be used on agent task %q", attr.Kind, t.Name)
			}
		}
		if plan != nil {
			a.checkPlanArgs(plan)
		}
		if applies != nil {
			a.checkApplies(t, applies)
		}
		// One approval names one plan for one call, so a task that applies a
		// plan is never run in the middle of another task's run.
		for _, edge := range t.Edges() {
			if target, ok := a.tasks[edge.Name]; ok && target.AppliesPlan() != "" {
				a.planErrorf(edge.Sp, "task %q applies a plan and cannot be run by task %q: it has to be called itself", target.Name, t.Name)
			}
		}
	}
}

func (a *analyzer) planErrorf(span token.Span, format string, args ...any) {
	a.diags.Codef(diag.CodeInvalidPlanBinding, span, format, args...)
}

// firstOf returns the task's first attribute of kind and reports every later
// one: the engine reads the first only, so a second would be silently unused.
func (a *analyzer) firstOf(t *ast.Task, kind string) *ast.Attribute {
	var first *ast.Attribute
	for _, attr := range t.Attributes {
		if attr.Kind != kind {
			continue
		}
		if first == nil {
			first = attr
			continue
		}
		a.diags.Add(diag.New(attr.Sp, fmt.Sprintf("duplicate [%s] on task %q", kind, t.Name)).
			WithCode(diag.CodeInvalidPlanBinding).
			WithRelated(diag.RelatedLocation{Span: first.Sp, Message: fmt.Sprintf("first [%s] declared here", kind)}))
	}
	return first
}

func (a *analyzer) checkPlanArgs(attr *ast.Attribute) {
	seen := map[string]*ast.AllowPredicate{}
	for _, arg := range attr.Allow {
		if !slices.Contains(planArguments, arg.Name) {
			a.planErrorf(arg.NameSp, "unknown plan argument %q (expected %s)", arg.Name, strings.Join(planArguments, ", "))
			continue
		}
		if seen[arg.Name] != nil {
			a.planErrorf(arg.NameSp, "duplicate plan argument %q", arg.Name)
			continue
		}
		seen[arg.Name] = arg
		if unreadable(arg.Value) {
			a.planErrorf(arg.ValueSp, "plan argument %q holds a character that is not printable", arg.Name)
			continue
		}
		switch {
		case arg.Value == "":
			a.planErrorf(arg.ValueSp, "plan argument %q is empty", arg.Name)
		case arg.Name == "file" || arg.Name == "summary":
			if ast.PlanPathError(arg.Value) != "" {
				a.planErrorf(arg.ValueSp, "plan %s %q must be a relative path with forward slashes inside the task's directory", arg.Name, arg.Value)
			}
		case arg.Name == "tool":
			if strings.ContainsAny(arg.Value, `/\`) {
				a.planErrorf(arg.ValueSp, "plan tool %q must be an executable name, not a path", arg.Value)
			}
		case arg.Name == "expires":
			if _, err := ast.PlanExpiry(arg.Value); err != nil {
				a.planErrorf(arg.ValueSp, `plan expires %q must be a duration from 1m to 168h, such as "30m"`, arg.Value)
			}
		}
	}
	for _, name := range []string{"file", "summary"} {
		if seen[name] == nil {
			a.planErrorf(attr.Sp, `plan(...) requires %s="<path>"`, name)
		}
	}
	if file, summary := seen["file"], seen["summary"]; file != nil && summary != nil &&
		file.Value != "" && summary.Value != "" && ast.SamePlanPath(file.Value, summary.Value) {
		a.planErrorf(summary.ValueSp, "plan summary must be a different file from the plan file")
	}
}

func (a *analyzer) checkApplies(t *ast.Task, attr *ast.Attribute) {
	for _, env := range t.Attributes {
		if env.Kind == ast.AttrEnv && slices.Contains([]string{ast.PlanFileVar, ast.PlanIDVar, ast.PlanDigestVar}, env.Str) {
			a.planErrorf(env.Sp, "task %q must not set %s with [env]: Rune sets it to the approved plan", t.Name, env.Str)
		}
	}
	// The body is the only place the stored plan can be used. One that never
	// names the variable would run something else under the approval.
	if !slices.ContainsFunc(t.Body, func(line *ast.BodyLine) bool { return strings.Contains(line.Raw, ast.PlanFileVar) }) {
		a.planErrorf(attr.Sp, "task %q applies a plan but its body does not use %s, so it would not apply the reviewed plan", t.Name, ast.PlanFileVar)
	}

	plan, ok := a.tasks[attr.Str]
	if !ok {
		msg := fmt.Sprintf("applies names unknown task %q", attr.Str)
		if near, found := suggest.Nearest(attr.Str, slices.Sorted(maps.Keys(a.tasks))); found {
			msg += fmt.Sprintf(" (did you mean %q?)", near)
		}
		a.planErrorf(attr.Sp, "%s", msg)
		return
	}
	if plan.Attr(ast.AttrPlan) == nil {
		a.planErrorf(attr.Sp, "task %q named by applies has no [plan] attribute", plan.Name)
		return
	}
	// The arguments are the target: an apply uses the plan made for equal
	// arguments, which only means something when the two tasks take the same.
	if !slices.EqualFunc(t.Params, plan.Params, sameParam) {
		a.planErrorf(attr.Sp, "task %q must have the same parameters as its plan task %q (%s)", t.Name, plan.Name, paramSignature(plan))
	}
	if a.reaches(t, plan.Name, map[string]bool{}) {
		a.planErrorf(attr.Sp, "task %q must not run its plan task %q: the plan would be made again after it was approved", t.Name, plan.Name)
	}
}

// sameParam reports whether two parameters take the same values in the same
// way. Defaults may differ: the effective arguments are compared at the call.
func sameParam(x, y *ast.Param) bool {
	if x.Name != y.Name || x.Kind != y.Kind || (x.Constraint == nil) != (y.Constraint == nil) {
		return false
	}
	return x.Constraint == nil ||
		(x.Constraint.KindName == y.Constraint.KindName && slices.Equal(x.Constraint.Values, y.Constraint.Values))
}

// paramSignature writes a task's parameters the way a header declares them,
// without defaults.
func paramSignature(t *ast.Task) string {
	if len(t.Params) == 0 {
		return "no parameters"
	}
	parts := make([]string, len(t.Params))
	for i, p := range t.Params {
		var b strings.Builder
		switch p.Kind {
		case ast.ParamVariadicPlus:
			b.WriteByte('+')
		case ast.ParamVariadicStar:
			b.WriteByte('*')
		}
		b.WriteString(p.Name)
		b.WriteString(p.Constraint.String())
		parts[i] = b.String()
	}
	return strings.Join(parts, " ")
}

// reaches reports whether a run of t can run the named task through its
// dependencies and hooks. seen guards a cycle, which is its own error.
func (a *analyzer) reaches(t *ast.Task, name string, seen map[string]bool) bool {
	if seen[t.Name] {
		return false
	}
	seen[t.Name] = true
	for _, edge := range t.Edges() {
		if edge.Name == name {
			return true
		}
		if next, ok := a.tasks[edge.Name]; ok && a.reaches(next, name, seen) {
			return true
		}
	}
	return false
}
