package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
)

// Spec 022 failure hooks (|| clause): fired by the declaring task's own body
// failure, exempt from run-once memoization (once per failing task), never
// altering the original error, never chaining.

func withFailHooks(t *ast.Task, hooks ...string) *ast.Task {
	for _, h := range hooks {
		t.FailHooks = append(t.FailHooks, &ast.DepCall{Name: h})
	}
	return t
}

func countOrder(m *mockEngine, name string) int {
	n := 0
	for _, o := range m.order {
		if o == name {
			n++
		}
	}
	return n
}

func TestFailHookFiresOnBodyFailure(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("original error not returned: %v", err)
	}
	if countOrder(m, "||diagnose") != 1 {
		t.Errorf("hook did not run exactly once: %v", m.order)
	}
	if len(m.failures) != 1 || m.failures[0].TaskName != "main" || m.failures[0].ExitCode != 1 {
		t.Errorf("failure context = %+v, want {main 1}", m.failures)
	}
}

func TestFailHookNotFiredOnSuccess(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	if err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}}); err != nil {
		t.Fatal(err)
	}
	if countOrder(m, "||diagnose") != 0 {
		t.Errorf("hook ran on success: %v", m.order)
	}
}

func TestFailHookNotFiredOnCancellation(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = context.Canceled

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if countOrder(m, "||diagnose") != 0 {
		t.Errorf("hook ran on cancellation: %v", m.order)
	}
}

// FR-003: a failing dependency fires its own hooks, never the dependent's.
func TestFailHookBodyOnlyNotDependencyFailure(t *testing.T) {
	depHook := task("dep-hook")
	mainHook := task("main-hook")
	dep := withFailHooks(task("dep"), "dep-hook")
	main := withFailHooks(task("main", "dep"), "main-hook")
	m := newEngine(depHook, mainHook, dep, main)
	m.failOn = "dep"
	m.execErr = errors.New("dep boom")

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if err == nil || !strings.Contains(err.Error(), "dep boom") {
		t.Fatalf("err = %v", err)
	}
	if countOrder(m, "||dep-hook") != 1 {
		t.Errorf("dependency's own hook did not fire: %v", m.order)
	}
	if countOrder(m, "||main-hook") != 0 {
		t.Errorf("dependent's hook fired on dependency failure: %v", m.order)
	}
	if countOrder(m, "main") != 0 {
		t.Errorf("main body ran after dep failure: %v", m.order)
	}
}

func TestFailHooksRunInDeclarationOrder(t *testing.T) {
	h1, h2 := task("h1"), task("h2")
	main := withFailHooks(task("main"), "h1", "h2")
	m := newEngine(h1, h2, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	want := []string{"main", "||h1", "||h2"}
	if strings.Join(m.order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", m.order, want)
	}
}

// FR-005: a failing hook warns once, does not chain its own || hooks, and the
// remaining hooks still run; the original error is preserved.
func TestFailHookFailureWarnsAndContinues(t *testing.T) {
	nested := task("nested")
	bad := withFailHooks(task("bad"), "nested") // bad's own || must be ignored
	good := task("good")
	main := withFailHooks(task("main"), "bad", "good")
	m := newEngine(nested, bad, good, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")
	m.hookFailOn = "bad"

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("original error not preserved: %v", err)
	}
	if countOrder(m, "||nested") != 0 {
		t.Errorf("hook chained its own || hooks: %v", m.order)
	}
	if countOrder(m, "||good") != 1 {
		t.Errorf("remaining hook did not run after a hook failure: %v", m.order)
	}
	found := false
	for _, w := range m.warnings {
		if strings.Contains(w, "bad") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for failing hook: %v", m.warnings)
	}
}

// FR-012: an OS-unavailable hook is skipped WITH a warning (unlike silent
// dep/post-hook skips).
func TestFailHookUnavailableSkippedWithWarning(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")
	m.unavailable = map[string]bool{"diagnose": true}

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if countOrder(m, "||diagnose") != 0 {
		t.Errorf("unavailable hook executed: %v", m.order)
	}
	found := false
	for _, w := range m.warnings {
		if strings.Contains(w, "diagnose") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for OS-skipped hook: %v", m.warnings)
	}
}

// Clarification Q2: a shared hook runs once PER FAILING TASK (memo bypass for
// the hook body), while the hook's own dependencies still memoize normally.
func TestFailHookRunsOncePerFailure(t *testing.T) {
	prep := task("prep")
	diagnose := withFailHooks(task("diagnose", "prep")) // hook with a dep
	a := withFailHooks(task("a"), "diagnose")
	b := withFailHooks(task("b"), "diagnose")
	top := task("top", "a", "b")
	top.Attributes = append(top.Attributes, &ast.Attribute{Kind: ast.AttrParallel})
	m := newEngine(prep, diagnose, a, b, top)
	m.failErrs = map[string]error{"a": errors.New("a boom"), "b": errors.New("b boom")}

	err := Run(m, []Invocation{{Task: top, Params: map[string]string{}}})
	if err == nil {
		t.Fatal("expected error from failing parallel deps")
	}
	if got := countOrder(m, "||diagnose"); got != 2 {
		t.Errorf("shared hook ran %d times, want 2 (once per failure): %v", got, m.order)
	}
	if got := countOrder(m, "prep"); got != 1 {
		t.Errorf("hook dependency ran %d times, want 1 (deps memoize): %v", got, m.order)
	}
	seen := map[string]bool{}
	for _, f := range m.failures {
		seen[f.TaskName] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("failure contexts = %+v, want one for a and one for b", m.failures)
	}
}

// A hook's && post-hooks run under normal semantics on hook success.
func TestFailHookPostHooksRun(t *testing.T) {
	after := task("after")
	diagnose := task("diagnose")
	diagnose.PostHooks = append(diagnose.PostHooks, &ast.DepCall{Name: "after"})
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(after, diagnose, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if countOrder(m, "after") != 1 {
		t.Errorf("hook's && post-hook did not run: %v", m.order)
	}
}

// R5: ExitCode derives from *shell.ExecError.Code; other errors default to 1.
func TestFailureExitCodeFromExecError(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = &shell.ExecError{Task: "main", Code: 7, Err: errors.New("exit status 7")}

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if len(m.failures) != 1 || m.failures[0].ExitCode != 7 {
		t.Errorf("failure = %+v, want ExitCode 7", m.failures)
	}
}

// The interp executor (python/node) reports failures with its own ExecError
// type; the hook must still see the real exit code (failure-env contract:
// "stable across executors").
func TestFailureExitCodeFromInterpExecError(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = &interp.ExecError{Task: "main", Name: "python3", Code: 5}

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if len(m.failures) != 1 || m.failures[0].ExitCode != 5 {
		t.Errorf("failure = %+v, want ExitCode 5", m.failures)
	}
}

// A shell ExecError without an exit status (Code 0, e.g. a parse error) must
// fall back to 1 — RUNE_FAILED_EXIT_CODE=0 would claim the failed task
// succeeded.
func TestFailureExitCodeZeroFallsBackToOne(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = &shell.ExecError{Task: "main", Err: errors.New("syntax error")}

	_ = Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if len(m.failures) != 1 || m.failures[0].ExitCode != 1 {
		t.Errorf("failure = %+v, want ExitCode 1", m.failures)
	}
}

// FR-003: an Execute error wrapping ErrBodyNotRun (e.g. a declined [confirm]
// prompt) means the body never started — no post-mortem fires.
func TestFailHookNotFiredWhenBodyNotRun(t *testing.T) {
	diagnose := task("diagnose")
	main := withFailHooks(task("main"), "diagnose")
	m := newEngine(diagnose, main)
	m.failOn = "main"
	m.execErr = fmt.Errorf("task %q was not confirmed: %w", "main", ErrBodyNotRun)

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if err == nil {
		t.Fatal("original error must still be returned")
	}
	if countOrder(m, "||diagnose") != 0 {
		t.Errorf("hook fired for a body that never ran: %v", m.order)
	}
	if len(m.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", m.warnings)
	}
}

// FR-002: a hook failing with context.Canceled means the run is being
// cancelled — stop firing hooks quietly (no warning, no further hooks).
func TestFailHookCancellationStopsQuietly(t *testing.T) {
	bad, good := task("bad"), task("good")
	main := withFailHooks(task("main"), "bad", "good")
	m := newEngine(bad, good, main)
	m.failOn = "main"
	m.execErr = errors.New("boom")
	m.hookFailOn = "bad"
	m.hookErr = context.Canceled

	err := Run(m, []Invocation{{Task: main, Params: map[string]string{}}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("original error not preserved: %v", err)
	}
	if countOrder(m, "||good") != 0 {
		t.Errorf("hooks kept firing during cancellation: %v", m.order)
	}
	if len(m.warnings) != 0 {
		t.Errorf("cancellation must not warn: %v", m.warnings)
	}
}

// Two [parallel] siblings failing at once share a hook: the hook body runs
// once per failure but never concurrently with itself (hookMu).
func TestFailHookBodiesNeverConcurrent(t *testing.T) {
	diagnose := task("diagnose")
	a := withFailHooks(task("a"), "diagnose")
	b := withFailHooks(task("b"), "diagnose")
	top := task("top", "a", "b")
	top.Attributes = append(top.Attributes, &ast.Attribute{Kind: ast.AttrParallel})
	m := newEngine(diagnose, a, b, top)
	m.failErrs = map[string]error{"a": errors.New("a boom"), "b": errors.New("b boom")}

	var mu sync.Mutex
	inHook, maxInHook := 0, 0
	m.hookGate = func() {
		mu.Lock()
		inHook++
		if inHook > maxInHook {
			maxInHook = inHook
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		inHook--
		mu.Unlock()
	}

	_ = Run(m, []Invocation{{Task: top, Params: map[string]string{}}})
	if got := countOrder(m, "||diagnose"); got != 2 {
		t.Fatalf("hook ran %d times, want 2: %v", got, m.order)
	}
	if maxInHook != 1 {
		t.Errorf("hook bodies overlapped (max concurrency %d, want 1)", maxInHook)
	}
}
