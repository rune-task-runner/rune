package analyzer

import (
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
)

// reuseConflicts are the attributes [reuse] cannot be combined with, and why:
// a run of such a task is its effect, or its result depends on more than its
// files (spec 061).
var reuseConflicts = []struct{ kind, why string }{
	{ast.AttrConfirm, "its run is its effect"},
	{ast.AttrPlan, "its run makes the plan"},
	{ast.AttrApplies, "its run applies a plan"},
	{ast.AttrNetwork, "its result depends on the network"},
}

// checkReuse validates [reuse] (RUNE2022). A task Rune could skip on a
// receipt must have a result that depends only on what a snapshot holds.
func (a *analyzer) checkReuse() {
	for _, t := range a.file.Tasks {
		attr := a.firstOf(t, ast.AttrReuse, diag.CodeInvalidReuse)
		if attr == nil {
			continue
		}
		for _, c := range reuseConflicts {
			if t.Attr(c.kind) != nil {
				a.diags.Codef(diag.CodeInvalidReuse, attr.Sp, "[reuse] cannot be combined with [%s] on task %q: %s", c.kind, t.Name, c.why)
			}
		}
		if t.Executor == ast.ExecAgent {
			a.diags.Codef(diag.CodeInvalidReuse, attr.Sp, "[reuse] cannot be used on agent task %q: its result is a model's answer", t.Name)
		}
	}
}
