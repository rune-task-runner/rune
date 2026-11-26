package analyzer

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

func checkSettings(t *testing.T, src string) diag.List {
	t.Helper()
	f, pdiags := parser.Parse("Runefile", src)
	if pdiags.HasErrors() {
		t.Fatalf("parse: %v", pdiags)
	}
	return CheckSettings(f)
}

func TestInvalidSettingFlagged(t *testing.T) {
	d := hasCode(checkSettings(t, "set bogus := \"x\"\n"), diag.CodeInvalidSetting)
	if d == nil {
		t.Fatal("expected RUNE2008 for an unknown setting")
	}
	if d.Severity != diag.Error {
		t.Errorf("severity = %v, want Error", d.Severity)
	}
}

func TestValidSettingsNotFlagged(t *testing.T) {
	// Every real setting (including the version settings) must pass.
	src := "set working-directory := \"x\"\nset quiet\nset export\nset fallback\n" +
		"set dotenv := \".env\"\nset shell := [\"sh\"]\nset python := [\"python3\"]\n" +
		"set node := [\"node\"]\nset agent_cmd := [\"a\"]\nset agent_provider := \"p\"\n" +
		"set minimum_version := \"0.8.0\"\nset rune_version := \"0.8.0\"\nset receipts\n"
	if diags := checkSettings(t, src); len(diags) != 0 {
		t.Errorf("valid settings flagged: %v", diags)
	}
}

// TestAnalyzeExcludesSettingCheck guards that the execution-path analyzer does
// NOT flag unknown settings (backward compatibility): only CheckSettings does.
func TestAnalyzeExcludesSettingCheck(t *testing.T) {
	f, _ := parser.Parse("Runefile", "set bogus := \"x\"\n# B.\nbuild:\n    @echo b\n")
	if hasCode(Analyze(f), diag.CodeInvalidSetting) != nil {
		t.Error("Analyze must not emit RUNE2008 (execution stays backward-compatible)")
	}
}
