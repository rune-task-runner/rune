package integration

import (
	"strings"
	"testing"
)

// Spec 022 US2 (quickstart Scenario A): at the CLI a failing task runs its
// || hooks under a `diagnosing:` header on stderr, hook output streams
// normally, and the process exit code is exactly what it would be without
// hooks — hook outcomes never change it (FR-004).

const failHookCLIRunefile = `test: || diagnose
    @echo "running tests"
    @exit 7

diagnose:
    @echo "SUGGESTION: check the fixtures"
`

func TestFailureHooks_CLIHeaderAndExitCode(t *testing.T) {
	dir := writeRunefile(t, failHookCLIRunefile)
	r := run(t, dir, nil, "test")
	if r.code != 1 {
		t.Errorf("exit = %d, want 1 (task failure, unchanged by hooks)", r.code)
	}
	if !strings.Contains(r.stderr, "diagnosing: diagnose") {
		t.Errorf("stderr missing 'diagnosing: diagnose' header:\n%s", r.stderr)
	}
	if !strings.Contains(r.stdout, "SUGGESTION: check the fixtures") {
		t.Errorf("hook output missing from stdout:\n%s", r.stdout)
	}
}

func TestFailureHooks_CLISuccessNoHeader(t *testing.T) {
	dir := writeRunefile(t, "test: || diagnose\n    @echo ok\ndiagnose:\n    @echo SUGGESTION\n")
	r := run(t, dir, nil, "test")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0", r.code)
	}
	if strings.Contains(r.stderr, "diagnosing") || strings.Contains(r.stdout, "SUGGESTION") {
		t.Errorf("hook ran on success:\nstdout=%s\nstderr=%s", r.stdout, r.stderr)
	}
}

func TestFailureHooks_CLIHookFailureWarnsExitPreserved(t *testing.T) {
	dir := writeRunefile(t, "test: || diagnose\n    @exit 7\ndiagnose:\n    @exit 9\n")
	r := run(t, dir, nil, "test")
	if r.code != 1 {
		t.Errorf("exit = %d, want 1 (original failure, not the hook's)", r.code)
	}
	if !strings.Contains(r.stderr, "warning: failure hook diagnose failed") {
		t.Errorf("stderr missing hook-failure warning:\n%s", r.stderr)
	}
}

// The styled run's visible bytes must equal the plain run exactly (the
// project's plain-output invariant): styling is additive color only.
func TestFailureHooks_CLIStylingPlainInvariant(t *testing.T) {
	dir := writeRunefile(t, failHookCLIRunefile)
	styled := run(t, dir, nil, "--color", "always", "test")
	plain := run(t, dir, nil, "--color", "never", "test")
	if got := stripANSI(styled.stderr); got != plain.stderr {
		t.Errorf("styled stderr visible text != plain:\n stripped=%q\n plain   =%q", got, plain.stderr)
	}
	if !strings.Contains(styled.stderr, "\x1b[") {
		t.Errorf("--color always produced no styling on stderr:\n%q", styled.stderr)
	}
}
