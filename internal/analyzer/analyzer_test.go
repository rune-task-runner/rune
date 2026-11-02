package analyzer

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/parser"
)

// analyze parses src and runs the analyzer, returning its diagnostics.
func analyze(t *testing.T, src string) []string {
	t.Helper()
	f, pdiags := parser.Parse("Runefile", src)
	if pdiags.HasErrors() {
		t.Fatalf("unexpected parse errors: %v", pdiags)
	}
	diags := Analyze(f)
	msgs := make([]string, len(diags))
	for i, d := range diags {
		msgs[i] = d.Message
	}
	return msgs
}

func hasMsg(msgs []string, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

func TestAnalyzeClean(t *testing.T) {
	src := "x := \"1\"\ngreet name=\"world\":\n    @echo {{name}} {{x}}\nbuild: greet\n    @echo build\n"
	if msgs := analyze(t, src); len(msgs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", msgs)
	}
}

// Issue #69: {{ expr }} with spaces inside the braces is the documented form.
func TestAnalyzeSpacedInterpolation(t *testing.T) {
	src := "x := \"1\"\ngreet name=\"world\":\n    @echo {{ name }} {{ x}} {{ env(\"HOME\") }} {{ name + x }}\n"
	if msgs := analyze(t, src); len(msgs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", msgs)
	}
	if msgs := analyze(t, "greet:\n    @echo {{ nope }}\n"); !hasMsg(msgs, "undefined variable: nope") {
		t.Errorf("a padded expression must still be resolved: msgs = %v", msgs)
	}
}

func TestAnalyzeUndefinedVariable(t *testing.T) {
	msgs := analyze(t, "greet:\n    @echo {{nope}}\n")
	if !hasMsg(msgs, "undefined variable: nope") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeUndefinedInAssignment(t *testing.T) {
	msgs := analyze(t, "a := b\n")
	if !hasMsg(msgs, "undefined variable: b") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeUnknownDependency(t *testing.T) {
	msgs := analyze(t, "a: b\n    @echo a\n")
	if !hasMsg(msgs, "unknown task: b") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeSelfCycle(t *testing.T) {
	msgs := analyze(t, "c: c\n    @echo c\n")
	if !hasMsg(msgs, "dependency cycle: c → c") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeLongerCycle(t *testing.T) {
	src := "a: b\n    @echo a\nb: c\n    @echo b\nc: a\n    @echo c\n"
	msgs := analyze(t, src)
	if !hasMsg(msgs, "dependency cycle:") {
		t.Fatalf("expected cycle, got %v", msgs)
	}
	// The reported path should include all three tasks.
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "a") && strings.Contains(m, "b") && strings.Contains(m, "c") && strings.Contains(m, "→") {
			found = true
		}
	}
	if !found {
		t.Errorf("cycle path incomplete: %v", msgs)
	}
}

func TestAnalyzeArityTooFew(t *testing.T) {
	src := "greet name:\n    @echo {{name}}\nall: greet\n    @echo all\n"
	msgs := analyze(t, src)
	if !hasMsg(msgs, "at least 1 argument") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeArityTooMany(t *testing.T) {
	src := "greet:\n    @echo hi\nall: (greet \"x\")\n    @echo all\n"
	msgs := analyze(t, src)
	if !hasMsg(msgs, "at most 0 argument") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeDuplicateSetting(t *testing.T) {
	msgs := analyze(t, "set quiet\nset quiet\ngreet:\n    @echo hi\n")
	if !hasMsg(msgs, "duplicate setting \"quiet\"") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeVariadicNotLast(t *testing.T) {
	msgs := analyze(t, "run +args name:\n    @echo hi\n")
	if !hasMsg(msgs, "must be the last parameter") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeRequiredAfterDefaulted(t *testing.T) {
	msgs := analyze(t, "run a=\"x\" b:\n    @echo hi\n")
	if !hasMsg(msgs, "cannot follow a defaulted parameter") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeUnknownFunction(t *testing.T) {
	msgs := analyze(t, "x := bogus(\"a\")\n")
	if !hasMsg(msgs, "unknown function: bogus") {
		t.Errorf("msgs = %v", msgs)
	}
}

func TestAnalyzeEmitsAllDiagnostics(t *testing.T) {
	// The scenario-2 file has three distinct errors; all must be reported.
	src := "a: b\n    @echo a\nc: c\n    @echo c\ngreet:\n    @echo {{undefined_var}}\n"
	msgs := analyze(t, src)
	if !hasMsg(msgs, "unknown task: b") || !hasMsg(msgs, "dependency cycle") || !hasMsg(msgs, "undefined variable: undefined_var") {
		t.Errorf("expected all three errors, got %v", msgs)
	}
}
