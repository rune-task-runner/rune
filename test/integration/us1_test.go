package integration

import (
	"os"
	"strings"
	"testing"
)

const us1Runefile = `set default := "greet"

# Say hello.
greet name="world":
    @echo "hello, {{name}}"

build: greet
    @echo "building..."
`

// TestUS1_BareShowsOverview: bare `rune` prints a version header + the available
// task listing and runs nothing (it no longer auto-runs a default task).
func TestUS1_BareShowsOverview(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	r := run(t, dir, nil)
	if r.code != 0 {
		t.Fatalf("exit = %d, stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "rune version:") {
		t.Errorf("bare rune missing version header: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "Available tasks:") || !strings.Contains(r.stdout, "greet") {
		t.Errorf("bare rune should list tasks: %q", r.stdout)
	}
	// It must NOT run the (formerly default) task.
	if strings.Contains(r.stdout, "hello, world") {
		t.Errorf("bare rune should not run a task: %q", r.stdout)
	}
}

// TestUS1_PlanFlagsNoTaskError: --dry-run/--summary with no task still error —
// there is no default task to plan (only bare `rune` shows the overview).
func TestUS1_PlanFlagsNoTaskError(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	for _, flag := range []string{"--dry-run", "--summary"} {
		r := run(t, dir, nil, flag)
		if r.code == 0 {
			t.Errorf("%s with no task should exit non-zero, got 0 (stdout=%q)", flag, r.stdout)
		}
		if !strings.Contains(r.stderr, "no task specified") {
			t.Errorf("%s with no task: stderr = %q, want 'no task specified'", flag, r.stderr)
		}
	}
}

// TestUS1_BareNoTasksPointsToHelpAndDocs: when the Runefile exposes no runnable
// tasks, bare `rune` shows the version header, a not-found line, and pointers to
// --help and the docs.
func TestUS1_BareNoTasksPointsToHelpAndDocs(t *testing.T) {
	dir := writeRunefile(t, "_hidden:\n    @echo hi\n")
	r := run(t, dir, nil)
	if r.code != 0 {
		t.Fatalf("exit = %d, stderr=%s", r.code, r.stderr)
	}
	for _, want := range []string{"rune version:", "No available tasks", "rune --help", "https://github.com/rune-task-runner/rune/tree/main/docs"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("no-tasks overview missing %q; got:\n%s", want, r.stdout)
		}
	}
}

func TestUS1_ParamPassing(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	r := run(t, dir, nil, "greet", "Ada")
	if r.code != 0 {
		t.Fatalf("exit = %d, stderr=%s", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "hello, Ada" {
		t.Errorf("stdout = %q, want 'hello, Ada'", r.stdout)
	}
}

// The documented {{ expr }} form, with spaces inside the braces, interpolates
// like {{expr}} (issue #69).
func TestUS1_SpacedInterpolation(t *testing.T) {
	dir := writeRunefile(t, "greet name=\"world\":\n    @echo \"hello, {{ name }}{{ \"!\" }}\"\n")
	r := run(t, dir, nil, "greet", "Ada")
	if r.code != 0 {
		t.Fatalf("exit = %d, stderr=%s", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "hello, Ada!" {
		t.Errorf("stdout = %q, want 'hello, Ada!'", r.stdout)
	}
}

func TestUS1_DependencyRunsFirstAndOnce(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	r := run(t, dir, nil, "build")
	if r.code != 0 {
		t.Fatalf("exit = %d, stderr=%s", r.code, r.stderr)
	}
	lines := splitNonEmpty(r.stdout)
	if len(lines) != 2 || lines[0] != "hello, world" || lines[1] != "building..." {
		t.Errorf("stdout lines = %v, want [hello, world, building...]", lines)
	}
}

func TestUS1_List(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	r := run(t, dir, nil, "--list")
	if r.code != 0 {
		t.Fatalf("exit = %d", r.code)
	}
	if !strings.Contains(r.stdout, "greet") || !strings.Contains(r.stdout, "Say hello.") {
		t.Errorf("--list stdout = %q, want greet + doc", r.stdout)
	}
	if !strings.Contains(r.stdout, "build") {
		t.Errorf("--list missing build: %q", r.stdout)
	}
	// --list runs nothing (no task body output).
	if strings.Contains(r.stdout, "hello, world") || strings.Contains(r.stdout, "building...") {
		t.Errorf("--list executed a task: %q", r.stdout)
	}
}

// TestUS1_ListStyledMatchesPlain proves the styled --list adds only zero-width
// emphasis: with --color=always it emits ANSI, and stripping that ANSI yields
// the exact plain bytes — so task names/groups/docs are distinguished while the
// "#" column and every other byte stay aligned (FR-013, SC-002).
func TestUS1_ListStyledMatchesPlain(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	plain := run(t, dir, nil, "--list")
	styled := run(t, dir, nil, "--color=always", "--list")
	if styled.code != 0 {
		t.Fatalf("styled --list exit = %d, stderr=%q", styled.code, styled.stderr)
	}
	if !hasANSI(styled.stdout) {
		t.Errorf("--color=always --list emitted no ANSI: %q", styled.stdout)
	}
	if got := stripANSI(styled.stdout); got != plain.stdout {
		t.Errorf("styled --list visible text != plain:\n stripped=%q\n plain   =%q", got, plain.stdout)
	}
}

func TestUS1_FailingTaskExitsNonZero(t *testing.T) {
	dir := writeRunefile(t, "boom:\n    exit 7\n")
	r := run(t, dir, nil, "boom")
	if r.code != 7 {
		t.Errorf("exit = %d, want 7 (the body's code)", r.code)
	}
}

func TestUS1_NoRunefileExits2(t *testing.T) {
	dir := t.TempDir()
	r := run(t, dir, nil, "anything")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2 (no Runefile)", r.code)
	}
}

func TestUS1_UpwardDiscovery(t *testing.T) {
	dir := writeRunefile(t, us1Runefile)
	// Run from a nested subdirectory; discovery should walk up to the Runefile.
	sub := dir + "/a/b"
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := run(t, sub, nil, "greet", "Ada")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "hello, Ada" {
		t.Errorf("nested discovery: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, strings.TrimSpace(ln))
		}
	}
	return out
}
