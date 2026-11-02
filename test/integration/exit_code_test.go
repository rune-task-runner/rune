package integration

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Spec 028: a failing task body makes rune exit with the body's own code,
// including codes that equal rune's own (2, 3, 130), and the banner is unchanged.
func TestExitCode_BodyCodePassesThrough(t *testing.T) {
	for _, n := range []int{1, 2, 3, 42, 126, 127, 130, 255} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dir := writeRunefile(t, fmt.Sprintf("t:\n    @exit %d\n", n))
			r := run(t, dir, nil, "--color=never", "t")
			if r.code != n {
				t.Errorf("exit = %d, want %d (the body's code)", r.code, n)
			}
			want := fmt.Sprintf(`rune: task "t" failed at "exit %d": exit status %d`, n, n)
			if !strings.Contains(r.stderr, want) {
				t.Errorf("stderr missing banner %q:\n%s", want, r.stderr)
			}
		})
	}
}

func TestExitCode_StopsAtFailingLine(t *testing.T) {
	dir := writeRunefile(t, "t:\n    @echo first\n    @exit 5\n    @echo never\n")
	r := run(t, dir, nil, "t")
	if r.code != 5 {
		t.Errorf("exit = %d, want 5", r.code)
	}
	if !strings.Contains(r.stdout, "first") || strings.Contains(r.stdout, "never") {
		t.Errorf("stdout = %q, want the first line only", r.stdout)
	}
}

// [no-exit-message] hides the banner only: the code is what the same task
// exits with without the attribute (FR-008).
func TestExitCode_NoExitMessage(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"body code", "quiet:\n    @exit 6\n", 6},
		{"run-time validation error", "quiet:\n    @echo {{env(\"RUNE_EXITCODE_TEST_UNSET\")}}\n", 3},
		{"agent not configured", "quiet (agent):\n    summarize the failures\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRunefile(t, "[no-exit-message]\n"+tc.body)
			r := run(t, dir, nil, "quiet")
			if r.code != tc.want {
				t.Errorf("exit = %d, want %d (banner suppressed, code kept)", r.code, tc.want)
			}
			// A validation error still renders its diagnostic; only the
			// "rune:" banner is suppressed.
			if strings.Contains(r.stderr, "rune: ") {
				t.Errorf("stderr = %q, want no banner", r.stderr)
			}
		})
	}
}

func TestExitCode_ContinueOnError(t *testing.T) {
	dir := writeRunefile(t, "t:\n    -exit 3\n    @echo ok\n")
	r := run(t, dir, nil, "t")
	if r.code != 0 {
		t.Errorf("exit = %d, want 0 (a - line never fails the task); stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "ok") {
		t.Errorf("stdout = %q, want the line after the ignored failure", r.stdout)
	}
}

func TestExitCode_InterpreterBody(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := writeRunefile(t, "pyfail (python):\n    import sys; sys.exit(4)\n")
	r := run(t, dir, nil, "pyfail")
	if r.code != 4 {
		t.Errorf("exit = %d, want 4; stderr=%s", r.code, r.stderr)
	}
}

// RUNE_FAILED_EXIT_CODE is the code rune exits with, for a body's own code and
// for rune errors raised while the task runs (FR-009).
func TestExitCode_HookSeesSameCode(t *testing.T) {
	cases := []struct {
		name, task string
		want       int
	}{
		{"body code", "hooked: || diagnose\n    @exit 5\n", 5},
		{"run-time validation error", "hooked: || diagnose\n    @echo {{env(\"RUNE_EXITCODE_TEST_UNSET\")}}\n", 3},
		{"agent not configured", "hooked (agent): || diagnose\n    summarize the failures\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRunefile(t, tc.task+"diagnose:\n    @echo \"code=$RUNE_FAILED_EXIT_CODE\"\n")
			r := run(t, dir, nil, "hooked")
			if r.code != tc.want {
				t.Errorf("exit = %d, want %d; stderr=%s", r.code, tc.want, r.stderr)
			}
			if want := fmt.Sprintf("code=%d", tc.want); !strings.Contains(r.stdout, want) {
				t.Errorf("hook output = %q, want %s", r.stdout, want)
			}
		})
	}
}

// The MCP [exit N] marker equals the CLI exit code for the same task, for body
// failures, success, and rune errors raised while the task runs (SC-002).
func TestExitCode_MCPMatchesCLI(t *testing.T) {
	const runefile = `ok:
    @echo fine

# Fails expression evaluation at run time: a validation error (3).
v:
    @echo {{env("RUNE_EXITCODE_TEST_UNSET")}}

# No agent_cmd is set: a usage error (2) raised while the task runs.
triage (agent):
    summarize the failures
`
	codes := []int{1, 2, 3, 42, 126, 127, 130, 255}
	src := runefile
	for _, n := range codes {
		src += fmt.Sprintf("\nc%d:\n    @exit %d\n", n, n)
	}
	dir := writeRunefile(t, src)

	cases := map[string]int{"ok": 0, "v": 3, "triage": 2}
	for _, n := range codes {
		cases[fmt.Sprintf("c%d", n)] = n
	}
	for task, want := range cases {
		t.Run(task, func(t *testing.T) {
			if r := run(t, dir, nil, task); r.code != want {
				t.Fatalf("CLI exit = %d, want %d; stderr=%s", r.code, want, r.stderr)
			}
			result, _ := mcpCall(t, dir, task)
			if marker := fmt.Sprintf("[exit %d]", want); !strings.Contains(result, marker) {
				t.Errorf("MCP result missing %s:\n%s", marker, result)
			}
			if isErr := strings.Contains(result, `"isError":true`); isErr != (want != 0) {
				t.Errorf("isError = %v, want %v:\n%s", isErr, want != 0, result)
			}
		})
	}
}

// The exit code is the code of the body whose failure stopped the run, the same
// failure the banner names (FR-002, FR-003).
func TestExitCode_Dependency(t *testing.T) {
	dir := writeRunefile(t, "a: b\n    @echo never\nb:\n    @exit 7\n")
	r := run(t, dir, nil, "a")
	if r.code != 7 {
		t.Errorf("exit = %d, want 7 (the dependency's code)", r.code)
	}
	if strings.Contains(r.stdout, "never") {
		t.Errorf("a's body ran after its dependency failed: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, `task "b" failed`) {
		t.Errorf("banner does not name b:\n%s", r.stderr)
	}
}

func TestExitCode_MultipleRoots(t *testing.T) {
	dir := writeRunefile(t, "x:\n    @echo ok\ny:\n    @exit 9\n")
	if r := run(t, dir, nil, "x", "y"); r.code != 9 {
		t.Errorf("rune x y: exit = %d, want 9", r.code)
	}
	r := run(t, dir, nil, "y", "x")
	if r.code != 9 {
		t.Errorf("rune y x: exit = %d, want 9", r.code)
	}
	if strings.Contains(r.stdout, "ok") {
		t.Errorf("rune y x: x ran after y failed: %q", r.stdout)
	}
}

func TestExitCode_Parallel(t *testing.T) {
	dir := writeRunefile(t, "[parallel]\np: d1 d2\n    @echo done\nd1:\n    @exit 4\nd2:\n    @exit 5\n")
	r := run(t, dir, nil, "--color=never", "p")
	if r.code != 4 && r.code != 5 {
		t.Fatalf("exit = %d, want 4 or 5; stderr=%s", r.code, r.stderr)
	}
	m := regexp.MustCompile(`exit status (\d+)`).FindStringSubmatch(r.stderr)
	if m == nil || m[1] != fmt.Sprint(r.code) {
		t.Errorf("exit = %d, but the banner reports %v:\n%s", r.code, m, r.stderr)
	}
}
