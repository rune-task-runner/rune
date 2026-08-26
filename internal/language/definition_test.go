package language

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/parser"
)

// resolve parses+composes src and returns the file and index for cursor tests.
func resolve(t *testing.T, src string) (*ast.File, *Index) {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	config.Compose(f, nil)
	return f, BuildIndex(f)
}

func at(src, substr string) int { return strings.Index(src, substr) }

func TestDefinitionDependencyToTask(t *testing.T) {
	src := "# Build.\nbuild:\n    @echo build\ndeploy: build\n    @echo deploy\n"
	f, ix := resolve(t, src)
	// Cursor on "build" in "deploy: build".
	offset := at(src, "deploy: build") + len("deploy: ")
	spans, ok := Definition(ix, f, "Runefile", offset)
	if !ok || len(spans) != 1 {
		t.Fatalf("definition not resolved: ok=%v spans=%v", ok, spans)
	}
	// It should point at the build task declaration (offset of "build:").
	wantLine := 2 // "build:" is on line 2 (1-based)
	if spans[0].Start.Line != wantLine {
		t.Errorf("definition line = %d, want %d", spans[0].Start.Line, wantLine)
	}
}

func TestDefinitionVariableReference(t *testing.T) {
	src := "output := \"dist\"\n# B.\nbuild:\n    @echo {{output}}\n"
	f, ix := resolve(t, src)
	offset := at(src, "{{output}}") + 2 // on "output" inside interpolation
	spans, ok := Definition(ix, f, "Runefile", offset)
	if !ok || len(spans) != 1 {
		t.Fatalf("variable definition not resolved: ok=%v spans=%v", ok, spans)
	}
	if spans[0].Start.Line != 1 { // assignment on line 1
		t.Errorf("definition line = %d, want 1", spans[0].Start.Line)
	}
}

// TestDefinitionInterpolationDeeperIndent guards the body-line offset mapping:
// a line indented deeper than the body's base width keeps that extra
// indentation in its Raw text, so the source offset must be shifted by it to
// hit-test the interpolation. Without the shift this reference is missed.
func TestDefinitionInterpolationDeeperIndent(t *testing.T) {
	src := "output := \"dist\"\n# B.\nbuild:\n    @echo start\n        echo {{output}}\n"
	f, ix := resolve(t, src)
	offset := at(src, "{{output}}") + 2 // on "output" inside the deeper-indented line
	spans, ok := Definition(ix, f, "Runefile", offset)
	if !ok || len(spans) != 1 {
		t.Fatalf("variable definition not resolved on deeper-indented line: ok=%v spans=%v", ok, spans)
	}
	if spans[0].Start.Line != 1 { // assignment on line 1
		t.Errorf("definition line = %d, want 1", spans[0].Start.Line)
	}
}

func TestDefinitionParameterInterpolation(t *testing.T) {
	src := "# G.\ngreet name=\"world\":\n    @echo {{name}}\n"
	f, ix := resolve(t, src)
	offset := at(src, "{{name}}") + 2
	spans, ok := Definition(ix, f, "Runefile", offset)
	if !ok || len(spans) != 1 {
		t.Fatalf("parameter definition not resolved: ok=%v spans=%v", ok, spans)
	}
	if spans[0].Start.Line != 2 { // header on line 2
		t.Errorf("definition line = %d, want 2", spans[0].Start.Line)
	}
}

func TestDefinitionNoTargetReturnsFalse(t *testing.T) {
	src := "# B.\nbuild:\n    @echo hello\n"
	f, ix := resolve(t, src)
	offset := at(src, "hello") // plain body text, not a reference
	if _, ok := Definition(ix, f, "Runefile", offset); ok {
		t.Error("plain body text should not resolve to a definition")
	}
}

// Spec 022: go-to-definition works on a || failure-hook reference.
func TestDefinitionFailHookToTask(t *testing.T) {
	src := "# Diagnose.\ndiagnose:\n    @echo d\ntest: || diagnose\n    @echo t\n"
	f, ix := resolve(t, src)
	offset := at(src, "test: || diagnose") + len("test: || ")
	spans, ok := Definition(ix, f, "Runefile", offset)
	if !ok || len(spans) != 1 {
		t.Fatalf("definition not resolved: ok=%v spans=%v", ok, spans)
	}
	if wantLine := 2; spans[0].Start.Line != wantLine {
		t.Errorf("definition line = %d, want %d", spans[0].Start.Line, wantLine)
	}
}
