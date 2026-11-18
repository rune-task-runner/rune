package parser

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
)

// Spec 051 FR-001: [plan(...)] holds named string arguments in source order,
// and [applies("task")] names the plan task.
func TestParsePlan(t *testing.T) {
	t.Run("file and summary", func(t *testing.T) {
		src := "[plan(file=\"tfplan\", summary=\"tfplan.txt\")]\nplan env:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrPlan)
		if a == nil || len(a.Allow) != 2 {
			t.Fatalf("attribute = %+v", a)
		}
		if a.Allow[0].Name != "file" || a.Allow[0].Value != "tfplan" ||
			a.Allow[1].Name != "summary" || a.Allow[1].Value != "tfplan.txt" {
			t.Errorf("arguments = %+v, %+v", a.Allow[0], a.Allow[1])
		}
		if got := spanText(src, a.Allow[1].NameSp); got != "summary" {
			t.Errorf("name span covers %q", got)
		}
		if got := spanText(src, a.Allow[1].ValueSp); got != `"tfplan.txt"` {
			t.Errorf("value span covers %q", got)
		}
		if got := spanText(src, a.Sp); got != `plan(file="tfplan", summary="tfplan.txt")` {
			t.Errorf("attribute span covers %q", got)
		}
	})

	t.Run("all four in the order written", func(t *testing.T) {
		src := "[plan(expires=\"30m\", tool=\"tofu\", summary=\"s\", file=\"f\")]\nplan:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrPlan)
		if a == nil || len(a.Allow) != 4 {
			t.Fatalf("attribute = %+v", a)
		}
		var names []string
		for _, arg := range a.Allow {
			names = append(names, arg.Name)
		}
		if got := names; got[0] != "expires" || got[1] != "tool" || got[2] != "summary" || got[3] != "file" {
			t.Errorf("names = %v", got)
		}
	})

	t.Run("applies", func(t *testing.T) {
		task := mustParse(t, "[applies(\"plan\")]\napply:\n    @echo $RUNE_PLAN_FILE\n").Tasks[0]
		a := task.Attr(ast.AttrApplies)
		if a == nil || a.Str != "plan" || task.AppliesPlan() != "plan" {
			t.Errorf("attribute = %+v", a)
		}
	})

	t.Run("shares a bracket list", func(t *testing.T) {
		task := mustParse(t, "[confirm, applies(\"plan\")]\napply:\n    @echo hi\n").Tasks[0]
		if task.Attr(ast.AttrConfirm) == nil || task.Attr(ast.AttrApplies) == nil {
			t.Errorf("attributes = %+v", task.Attributes)
		}
	})
}

// A malformed [plan] or [applies] is a syntax error at the token found, never
// an unknown attribute.
func TestParsePlanErrors(t *testing.T) {
	for _, attr := range []string{
		`[plan]`,
		`[plan()]`,
		`[plan(file)]`,
		`[plan(file=)]`,
		`[plan(file=tfplan)]`,
		`[plan(file="a",)]`,
		`[applies]`,
		`[applies()]`,
		`[applies(plan)]`,
	} {
		t.Run(attr, func(t *testing.T) {
			_, diags := Parse("Runefile", attr+"\ntask:\n    @echo hi\n")
			if !diags.HasErrors() {
				t.Fatalf("no error for %s", attr)
			}
			for _, d := range diags {
				if d.Severity == diag.Error && d.Code != diag.CodeUnexpectedToken && d.Code != diag.CodeInvalidAttribute {
					t.Errorf("code = %s, want a syntax error: %s", d.Code, d.Message)
				}
				if strings.Contains(d.Message, "unknown attribute") {
					t.Errorf("reported as unknown: %s", d.Message)
				}
			}
		})
	}
}

// The plan messages name the argument, and the agent-allow ones keep their
// wording.
func TestNamedStringArgMessages(t *testing.T) {
	cases := []struct{ src, want string }{
		{`[plan(file)]`, "after the argument name in plan(...)"},
		{`[plan(=)]`, "as an argument name in plan(...)"},
		{`[plan(file=x)]`, "as the argument's value"},
		{`[agent-allow(env)]`, "after the parameter name in agent-allow(...)"},
		{`[agent-allow(=)]`, "as a parameter name in agent-allow(...)"},
		{`[agent-allow(env=x)]`, "as the value agents may pass"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			_, diags := Parse("Runefile", c.src+"\ntask env:\n    @echo hi\n")
			for _, d := range diags {
				if strings.Contains(d.Message, c.want) {
					return
				}
			}
			t.Errorf("no diagnostic holds %q: %v", c.want, diags)
		})
	}
}
