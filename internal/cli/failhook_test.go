package cli

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// Spec 022 US1: the MCP adapter captures || failure-hook stdout into
// Result.FixSuggestion — masked, trimmed, capped — while hook stderr joins
// the normal captured stderr.

func TestCallFailHookFixSuggestion(t *testing.T) {
	a := adapterFor(t, "test: || diagnose\n    @exit 7\ndiagnose:\n    @echo SUGGESTION: check the fixtures\n    @echo to-stderr >&2\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 {
		t.Fatal("failing task must keep a non-zero exit code")
	}
	if res.FixSuggestion != "SUGGESTION: check the fixtures" {
		t.Errorf("FixSuggestion = %q", res.FixSuggestion)
	}
	if strings.Contains(res.FixSuggestion, "to-stderr") {
		t.Errorf("hook stderr leaked into the suggestion: %q", res.FixSuggestion)
	}
	if !strings.Contains(res.Stderr, "to-stderr") {
		t.Errorf("hook stderr should join captured stderr, got %q", res.Stderr)
	}
	if strings.Contains(res.Stdout, "SUGGESTION") {
		t.Errorf("hook stdout leaked into task stdout: %q", res.Stdout)
	}
}

func TestCallFailHookSuccessNoSuggestion(t *testing.T) {
	a := adapterFor(t, "test: || diagnose\n    @echo ok\ndiagnose:\n    @echo SUGGESTION\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FixSuggestion != "" {
		t.Errorf("success must not produce a suggestion, got %q", res.FixSuggestion)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
}

func TestCallNoHooksNoSuggestion(t *testing.T) {
	a := adapterFor(t, "test:\n    @exit 3\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FixSuggestion != "" {
		t.Errorf("no hooks: suggestion must be empty, got %q", res.FixSuggestion)
	}
}

func TestCallFailHookSuggestionMasked(t *testing.T) {
	a := adapterFor(t, "test: || diagnose\n    @exit 1\ndiagnose:\n    @echo token=$MY_API_TOKEN\n")
	a.baseEnv = []string{"MY_API_TOKEN=hunter2-super-secret"}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.FixSuggestion, "hunter2-super-secret") {
		t.Fatalf("secret leaked into fix suggestion: %q", res.FixSuggestion)
	}
	if !strings.Contains(res.FixSuggestion, "***") {
		t.Errorf("masked placeholder expected, got %q", res.FixSuggestion)
	}
}

func TestCallFailHookSuggestionTruncates(t *testing.T) {
	// ~9000 bytes of hook stdout; cap is 8192 content bytes plus the marker.
	a := adapterFor(t, "test: || diagnose\n    @exit 1\ndiagnose:\n    @printf '%09000d' 0\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.FixSuggestion, "[truncated]") {
		t.Errorf("capped suggestion must end with the marker (len=%d)", len(res.FixSuggestion))
	}
	if len(res.FixSuggestion) > contextMaxBytes+len("\n[truncated]") {
		t.Errorf("suggestion length %d exceeds cap", len(res.FixSuggestion))
	}
}

func TestCallFailHookSuggestionTruncatesOnRuneBoundary(t *testing.T) {
	// 3-byte runes straddle the 8192-byte cap (8192 % 3 != 0).
	a := adapterFor(t, "test: || diagnose\n    @exit 1\ndiagnose:\n    @awk 'BEGIN { for (i = 0; i < 3000; i++) printf \"…\" }'\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.FixSuggestion, "[truncated]") {
		t.Fatalf("expected truncation, got %d bytes", len(res.FixSuggestion))
	}
	if !utf8.ValidString(res.FixSuggestion) {
		t.Fatal("truncation split a rune: suggestion is not valid UTF-8")
	}
}

// FR-003: a declined [confirm] prompt (MCP has no stdin, so it auto-declines)
// is not a body failure — the body never started, so no hook fires and no
// suggestion is produced.
func TestCallConfirmDeclineDoesNotFireFailHooks(t *testing.T) {
	a := adapterFor(t, "[confirm]\ndeploy: || diagnose\n    @echo body\ndiagnose:\n    @echo SUGGESTION\n")
	res, err := a.Call(context.Background(), "deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 {
		t.Fatal("declined [confirm] must not succeed")
	}
	if res.FixSuggestion != "" {
		t.Errorf("declined confirm produced a suggestion: %q", res.FixSuggestion)
	}
	if strings.Contains(res.Stderr, "diagnosing") {
		t.Errorf("hook ran for a body that never started:\n%s", res.Stderr)
	}
}

// Spec 022 US3 (contracts/failure-env.md): the hook body sees the failed
// task's name and real exit code; the failure context wins a name collision
// with a static [env(...)] declaration.
func TestCallFailHookEnvContext(t *testing.T) {
	a := adapterFor(t, "test: || diagnose\n    @exit 7\n[env(\"RUNE_FAILED_TASK\", \"bogus\")]\ndiagnose:\n    @echo failed=$RUNE_FAILED_TASK code=$RUNE_FAILED_EXIT_CODE\n")
	res, err := a.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FixSuggestion != "failed=test code=7" {
		t.Errorf("FixSuggestion = %q, want %q", res.FixSuggestion, "failed=test code=7")
	}
}
