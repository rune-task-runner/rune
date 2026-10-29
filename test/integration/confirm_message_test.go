package integration

import (
	"strings"
	"testing"
)

// Spec 027 Story 3: the decline ends with exactly one line naming the task,
// its question, the reason, and the fix (contracts/confirm-cli.md §2).
func TestConfirmNonTerminalBanner(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	const want = "rune: task \"apply\" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve\n"

	plain := runWithInput(t, dir, strings.NewReader("y\n"), "--color=never", "apply")
	styled := runWithInput(t, dir, strings.NewReader("y\n"), "--color=always", "apply")

	if plain.stderr != want {
		t.Errorf("stderr = %q\n want %q", plain.stderr, want)
	}
	if strings.Contains(plain.stderr, "[y/N]") {
		t.Errorf("unanswerable prompt printed: %q", plain.stderr)
	}
	if styled.code != plain.code || plain.code != 1 {
		t.Errorf("exit codes styled=%d plain=%d, want both 1", styled.code, plain.code)
	}
	if !hasANSI(styled.stderr) {
		t.Errorf("banner should carry ANSI under --color=always: %q", styled.stderr)
	}
	if got := stripANSI(styled.stderr); got != plain.stderr {
		t.Errorf("styled banner (stripped) != plain:\n stripped=%q\n plain=%q", got, plain.stderr)
	}
}

// Contract §2a: a [confirm] failure hook reports the same text once, through
// the existing hook warning, and the original failure stands.
func TestConfirmNonTerminalFailHookWarning(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	const want = `warning: failure hook diag failed: task "diag" was not confirmed (Run "diag"?): stdin is not a terminal; pass --yes to approve`

	r := runWithInput(t, dir, strings.NewReader("y\n"), "--color=never", "fails")

	if n := strings.Count(r.stderr, want); n != 1 {
		t.Errorf("hook warning appears %d times, want 1\nstderr: %s", n, r.stderr)
	}
	if r.code != 1 {
		t.Errorf("exit = %d, want the original failure's 1", r.code)
	}
}
