package analyzer

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

// analyzeDiags parses src and returns the analyzer's raw diagnostics.
func analyzeDiags(t *testing.T, src string) diag.List {
	t.Helper()
	f, pdiags := parser.Parse("Runefile", src)
	if pdiags.HasErrors() {
		t.Fatalf("unexpected parse errors: %v", pdiags)
	}
	return Analyze(f)
}

// hasCode reports whether any diagnostic carries the given stable code.
func hasCode(diags diag.List, code string) *diag.Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}

func TestSemanticCodes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		sev  diag.Severity
	}{
		{"unknown dependency", "a: b\n    @echo a\n", diag.CodeUnknownDependency, diag.Error},
		{"duplicate task", "a:\n    @echo a\na:\n    @echo a2\n", diag.CodeDuplicateTask, diag.Error},
		{"undefined variable", "greet:\n    @echo {{nope}}\n", diag.CodeUndefinedVariable, diag.Error},
		{"wrong arg count", "greet name:\n    @echo {{name}}\nrun: (greet \"a\" \"b\")\n    @echo run\n", diag.CodeWrongArgCount, diag.Error},
		{"duplicate parameter", "greet a a:\n    @echo hi\n", diag.CodeDuplicateParam, diag.Error},
		{"dependency cycle", "c: c\n    @echo c\n", diag.CodeDependencyCycle, diag.Error},
		{"unknown parameter type", "deploy env:bogus:\n    @echo {{env}}\n", diag.CodeUnknownParamType, diag.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := analyzeDiags(t, tc.src)
			d := hasCode(diags, tc.code)
			if d == nil {
				t.Fatalf("expected code %s, got %v", tc.code, diags)
			}
			if d.Severity != tc.sev {
				t.Errorf("code %s: severity = %v, want %v", tc.code, d.Severity, tc.sev)
			}
			if !d.Span.IsValid() {
				t.Errorf("code %s: span is not populated (range must be valid)", tc.code)
			}
		})
	}
}

// TestCycleRelatedLocations verifies RUNE2003 lists every task in the cycle as a
// related location (spec FR-009).
func TestCycleRelatedLocations(t *testing.T) {
	src := "a: b\n    @echo a\nb: c\n    @echo b\nc: a\n    @echo c\n"
	diags := analyzeDiags(t, src)
	d := hasCode(diags, diag.CodeDependencyCycle)
	if d == nil {
		t.Fatalf("expected a dependency-cycle diagnostic, got %v", diags)
	}
	if len(d.Related) < 3 {
		t.Fatalf("expected >=3 related locations (a, b, c), got %d: %+v", len(d.Related), d.Related)
	}
	names := map[string]bool{}
	for _, r := range d.Related {
		names[r.Message] = true
		if !r.Span.IsValid() {
			t.Errorf("related location for %q has invalid span", r.Message)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("cycle related locations missing task %q (got %v)", want, names)
		}
	}
}

// Spec 023 US4: authoring mistakes in parameter annotations are caught
// statically, each with its stable code, correct severity, and a valid span
// (quickstart §3).
func TestAnnotationCodes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		sev  diag.Severity
	}{
		{"duplicate enum value", "deploy env:enum(\"a\",\"a\"):\n    @echo hi\n", diag.CodeInvalidEnumValues, diag.Error},
		{"default violates enum", "deploy env:enum(\"a\",\"b\")=\"c\":\n    @echo hi\n", diag.CodeDefaultViolatesType, diag.Error},
		{"default violates number", "deploy n:number=\"abc\":\n    @echo hi\n", diag.CodeDefaultViolatesType, diag.Error},
		{"default violates boolean", "deploy b:boolean=\"yes\":\n    @echo hi\n", diag.CodeDefaultViolatesType, diag.Error},
		{"param-doc unknown parameter", "[param-doc(\"nope\", \"x\")]\ndeploy env:\n    @echo hi\n", diag.CodeInvalidAnnotation, diag.Error},
		{"duplicate param-doc", "[param-doc(\"env\", \"a\")]\n[param-doc(\"env\", \"b\")]\ndeploy env:\n    @echo hi\n", diag.CodeInvalidAnnotation, diag.Error},
		{"duplicate returns", "[returns(\"a\")]\n[returns(\"b\")]\ndeploy:\n    @echo hi\n", diag.CodeInvalidAnnotation, diag.Error},
		{"kind shadows task name", "string:\n    @echo s\ndeploy env:string:\n    @echo hi\n", diag.CodeKindShadowsTask, diag.Warning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := analyzeDiags(t, tc.src)
			d := hasCode(diags, tc.code)
			if d == nil {
				t.Fatalf("expected code %s, got %v", tc.code, diags)
			}
			if d.Severity != tc.sev {
				t.Errorf("code %s: severity = %v, want %v", tc.code, d.Severity, tc.sev)
			}
			if !d.Span.IsValid() {
				t.Errorf("code %s: span not populated", tc.code)
			}
		})
	}
}

// The RUNE2016 warning names both readings and the spaced spelling that
// restores the dependency; a valid kind with no same-named task draws nothing.
func TestKindShadowWarningContent(t *testing.T) {
	diags := analyzeDiags(t, "string:\n    @echo s\ndeploy env:string:\n    @echo hi\n")
	d := hasCode(diags, diag.CodeKindShadowsTask)
	if d == nil {
		t.Fatal("expected RUNE2016")
	}
	if !strings.Contains(d.Message, "env: string") {
		t.Errorf("message %q missing the spaced spelling", d.Message)
	}
	clean := analyzeDiags(t, "deploy env:string:\n    @echo hi\n")
	if hasCode(clean, diag.CodeKindShadowsTask) != nil {
		t.Error("RUNE2016 fired with no same-named task")
	}

	// A value list rules the legacy re-parse reading out — no pre-023 header
	// could carry `("a","b")` — so an enum annotation never warns, even when
	// a task named "enum" exists.
	withValues := analyzeDiags(t, "enum:\n    @echo e\ndeploy env:enum(\"a\",\"b\"):\n    @echo hi\n")
	if hasCode(withValues, diag.CodeKindShadowsTask) != nil {
		t.Error("RUNE2016 fired for an annotation with a value list")
	}
}

// RUNE2012's related note points at the same-named task when one exists
// (contracts/diagnostic-codes.md).
func TestUnknownKindTaskNameHint(t *testing.T) {
	diags := analyzeDiags(t, "bogus:\n    @echo b\ndeploy env:bogus:\n    @echo hi\n")
	d := hasCode(diags, diag.CodeUnknownParamType)
	if d == nil {
		t.Fatal("expected RUNE2012")
	}
	if len(d.Related) == 0 || !strings.Contains(d.Related[0].Message, "add a space") {
		t.Errorf("related hint missing: %+v", d.Related)
	}
}
