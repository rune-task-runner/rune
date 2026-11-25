package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
)

// Spec 059 FR-021/FR-022: RunOnly runs one body, never its dependencies or
// post-hooks, and fires its failure hooks as Run does.

func TestRunOnlyRunsOnlyTheBody(t *testing.T) {
	dep := task("dep")
	hook := task("hook")
	top := task("top", "dep")
	top.PostHooks = []*ast.DepCall{{Name: "hook"}}
	m := newEngine(dep, hook, top)
	if err := RunOnly(m, Invocation{Task: top}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"top"}; !slices.Equal(m.order, want) {
		t.Errorf("order = %v, want %v", m.order, want)
	}
}

func TestRunOnlyFiresFailHooks(t *testing.T) {
	diag := task("diagnose")
	top := withFailHooks(task("top"), "diagnose")
	m := newEngine(diag, top)
	m.failOn = "top"
	m.execErr = errors.New("boom")
	m.exitCode = func(error) int { return 7 }
	err := RunOnly(m, Invocation{Task: top})
	if !errors.Is(err, m.execErr) {
		t.Fatalf("err = %v, want the body error", err)
	}
	if want := []string{"top", "||diagnose"}; !slices.Equal(m.order, want) {
		t.Errorf("order = %v, want %v", m.order, want)
	}
	if len(m.failures) != 1 || m.failures[0] != (Failure{TaskName: "top", ExitCode: 7}) {
		t.Errorf("failures = %v", m.failures)
	}
}

func TestRunOnlyNoHooksWhenBodyDidNotRunOrWasCancelled(t *testing.T) {
	for _, bodyErr := range []error{
		fmt.Errorf("declined: %w", ErrBodyNotRun),
		fmt.Errorf("stopped: %w", context.Canceled),
	} {
		diag := task("diagnose")
		top := withFailHooks(task("top"), "diagnose")
		m := newEngine(diag, top)
		m.failOn = "top"
		m.execErr = bodyErr
		if err := RunOnly(m, Invocation{Task: top}); !errors.Is(err, bodyErr) {
			t.Fatalf("err = %v, want %v", err, bodyErr)
		}
		if countOrder(m, "||diagnose") != 0 {
			t.Errorf("%v: a failure hook ran: %v", bodyErr, m.order)
		}
	}
}
