package parser

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
)

// Spec 061 FR-001, FR-002: [reuse] takes no argument.
func TestParseReuse(t *testing.T) {
	t.Run("alone", func(t *testing.T) {
		src := "[reuse]\ntest:\n    @echo hi\n"
		a := mustParse(t, src).Tasks[0].Attr(ast.AttrReuse)
		if a == nil || a.Str != "" || len(a.Allow) != 0 {
			t.Fatalf("attribute = %+v", a)
		}
		if got := spanText(src, a.Sp); got != "reuse" {
			t.Errorf("attribute span covers %q", got)
		}
	})

	t.Run("with cache in one bracket list", func(t *testing.T) {
		task := mustParse(t, "[reuse, cache(inputs=[\"a\"])]\ntest:\n    @echo hi\n").Tasks[0]
		if task.Attr(ast.AttrReuse) == nil || task.Attr(ast.AttrCache) == nil {
			t.Errorf("attributes = %+v", task.Attributes)
		}
	})
}

// An argument to [reuse] is a syntax error, never an unknown attribute.
func TestParseReuseErrors(t *testing.T) {
	for _, attr := range []string{`[reuse("x")]`, `[reuse()]`, `[reuse(x)]`} {
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
