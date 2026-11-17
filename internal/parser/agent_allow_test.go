package parser

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// spanText is the source text a span covers.
func spanText(src string, sp token.Span) string {
	return src[sp.Start.Offset:sp.End.Offset]
}

// Spec 050 FR-001: [agent-allow(name="value", ...)] holds one or more
// predicates, each with the spans its diagnostics point at.
func TestParseAgentAllow(t *testing.T) {
	t.Run("one predicate", func(t *testing.T) {
		src := "[agent-allow(env=\"staging\")]\ndeploy env:enum(\"staging\",\"prod\"):\n    @echo {{env}}\n"
		task := mustParse(t, src).Tasks[0]
		a := task.Attr(ast.AttrAgentAllow)
		if a == nil || len(a.Allow) != 1 {
			t.Fatalf("attribute = %+v", a)
		}
		p := a.Allow[0]
		if p.Name != "env" || p.Value != "staging" {
			t.Errorf("predicate = %+v", p)
		}
		if got := spanText(src, p.NameSp); got != "env" {
			t.Errorf("name span covers %q", got)
		}
		if got := spanText(src, p.ValueSp); got != `"staging"` {
			t.Errorf("value span covers %q", got)
		}
		if got := spanText(src, a.Sp); got != `agent-allow(env="staging")` {
			t.Errorf("attribute span covers %q", got)
		}
	})

	t.Run("two predicates in source order", func(t *testing.T) {
		src := "[agent-allow(env = \"staging\", region=\"eu-west-1\")]\nrollout env region:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrAgentAllow)
		if a == nil || len(a.Allow) != 2 {
			t.Fatalf("attribute = %+v", a)
		}
		if a.Allow[0].Name != "env" || a.Allow[0].Value != "staging" ||
			a.Allow[1].Name != "region" || a.Allow[1].Value != "eu-west-1" {
			t.Errorf("predicates = %+v, %+v", a.Allow[0], a.Allow[1])
		}
	})

	t.Run("shares a bracket list", func(t *testing.T) {
		task := mustParse(t, "[confirm, agent-allow(env=\"staging\")]\ndeploy env:\n    @echo hi\n").Tasks[0]
		if task.Attr(ast.AttrConfirm) == nil || task.Attr(ast.AttrAgentAllow) == nil {
			t.Errorf("attributes = %+v", task.Attributes)
		}
	})

	t.Run("decoded value and hyphenated name", func(t *testing.T) {
		src := "[agent-allow(dry-run=\"a\\\"b\")]\nt dry-run:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrAgentAllow)
		if a == nil || len(a.Allow) != 1 || a.Allow[0].Name != "dry-run" || a.Allow[0].Value != `a"b` {
			t.Errorf("attribute = %+v", a)
		}
	})
}

// Spec 050 FR-004: an attribute with no predicate, and a predicate that is
// not name="value", is a syntax error, never an unknown attribute.
func TestParseAgentAllowErrors(t *testing.T) {
	for _, attr := range []string{
		`[agent-allow]`,
		`[agent-allow()]`,
		`[agent-allow(env)]`,
		`[agent-allow(env=)]`,
		`[agent-allow(env=prod)]`,
		`[agent-allow(env="a",)]`,
		`[agent-allow("env"="a")]`,
		`[agent-allow(env=prod]`,
		`[agent-allow(env=prod`,
	} {
		t.Run(attr, func(t *testing.T) {
			_, diags := Parse("Runefile", attr+"\nt env:\n    @echo hi\n")
			syntax := false
			for _, d := range diags {
				if d.Severity == diag.Error && d.Code == diag.CodeUnexpectedToken {
					syntax = true
				}
				if strings.Contains(d.Message, "unknown attribute") {
					t.Errorf("reported as unknown: %s", d.Message)
				}
			}
			if !syntax {
				t.Errorf("no RUNE1001 error in %v", diags)
			}
		})
	}
}

// One mistake inside the parentheses gives one error: what is left of the
// group is skipped, so it is not read as the rest of the file.
func TestParseAgentAllowOneError(t *testing.T) {
	for _, attr := range []string{
		`[agent-allow()]`,
		`[agent-allow(env)]`,
		`[agent-allow(env=)]`,
		`[agent-allow(env=prod)]`,
		`[agent-allow(env="a",)]`,
		`[agent-allow("env"="a")]`,
		`[agent-allow(env="a" region="b")]`,
		`[agent-allow(env=prod), confirm]`,
		`[agent-allow(env=env("X"))]`,
	} {
		t.Run(attr, func(t *testing.T) {
			f, diags := Parse("Runefile", attr+"\nt env:\n    @echo hi\n")
			if len(diags) != 1 {
				t.Errorf("%d diagnostics, want 1: %v", len(diags), diags)
			}
			if len(f.Tasks) != 1 || f.Tasks[0].Name != "t" {
				t.Errorf("the task after the attribute was not parsed: %d tasks", len(f.Tasks))
			}
		})
	}
}
