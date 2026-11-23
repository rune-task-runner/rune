package parser

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
)

// Spec 057 FR-001, FR-002: [agent-output(...)] holds named string arguments
// in source order, and [agent-summary] takes none.
func TestParseAgentOutput(t *testing.T) {
	t.Run("tail", func(t *testing.T) {
		src := "[agent-output(tail=\"50\")]\ntest:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrAgentOutput)
		if a == nil || len(a.Allow) != 1 || a.Allow[0].Name != "tail" || a.Allow[0].Value != "50" {
			t.Fatalf("attribute = %+v", a)
		}
		if got := spanText(src, a.Sp); got != `agent-output(tail="50")` {
			t.Errorf("attribute span covers %q", got)
		}
		if got := spanText(src, a.Allow[0].ValueSp); got != `"50"` {
			t.Errorf("value span covers %q", got)
		}
	})

	t.Run("head and tail in the order written", func(t *testing.T) {
		src := "[agent-output(tail=\"40\", head=\"10\")]\nbuild:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrAgentOutput)
		if a == nil || len(a.Allow) != 2 || a.Allow[0].Name != "tail" || a.Allow[1].Name != "head" {
			t.Fatalf("attribute = %+v", a)
		}
	})

	t.Run("summary", func(t *testing.T) {
		task := mustParse(t, "[agent-summary]\ndeploy:\n    @echo hi\n").Tasks[0]
		a := task.Attr(ast.AttrAgentSummary)
		if a == nil || len(a.Allow) != 0 || a.Str != "" {
			t.Fatalf("attribute = %+v", a)
		}
	})

	t.Run("both in one bracket list", func(t *testing.T) {
		task := mustParse(t, "[agent-summary, agent-output(tail=\"5\")]\ntest:\n    @echo hi\n").Tasks[0]
		if task.Attr(ast.AttrAgentSummary) == nil || task.Attr(ast.AttrAgentOutput) == nil {
			t.Errorf("attributes = %+v", task.Attributes)
		}
	})
}

// A malformed [agent-output] or an argument to [agent-summary] is a syntax
// error at the token found, never an unknown attribute.
func TestParseAgentOutputErrors(t *testing.T) {
	for _, attr := range []string{
		`[agent-output]`,
		`[agent-output()]`,
		`[agent-output(tail)]`,
		`[agent-output(tail=)]`,
		`[agent-output(tail=50)]`,
		`[agent-output(tail="5",)]`,
		`[agent-summary("x")]`,
		`[agent-summary()]`,
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

// The messages of a malformed [agent-output] name its argument and value.
func TestAgentOutputArgMessages(t *testing.T) {
	cases := []struct{ src, want string }{
		{`[agent-output(tail)]`, "after the argument name in agent-output(...)"},
		{`[agent-output(=)]`, "as an argument name in agent-output(...)"},
		{`[agent-output(tail=5)]`, "as a number of lines"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			_, diags := Parse("Runefile", c.src+"\ntask:\n    @echo hi\n")
			for _, d := range diags {
				if strings.Contains(d.Message, c.want) {
					return
				}
			}
			t.Errorf("no message contains %q: %v", c.want, diags)
		})
	}
}
