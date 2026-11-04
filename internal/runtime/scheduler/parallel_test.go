package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
)

func parallelTask(name string, deps ...string) *ast.Task {
	t := task(name, deps...)
	t.Attributes = append(t.Attributes, &ast.Attribute{Kind: ast.AttrParallel})
	return t
}

func TestParallelDepsRunConcurrently(t *testing.T) {
	a := task("a")
	b := task("b")
	top := parallelTask("top", "a", "b")
	m := newEngine(a, b, top)
	m.delay = 50 * time.Millisecond

	start := time.Now()
	if err := Run(m, []Invocation{{Task: top}}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if m.maxConc < 2 {
		t.Errorf("max concurrency = %d, want >= 2 (deps did not run in parallel)", m.maxConc)
	}
	// Sequential would be ~150ms (a + b + top); parallel deps ~100ms.
	if elapsed > 140*time.Millisecond {
		t.Errorf("elapsed = %v, expected parallel deps to overlap", elapsed)
	}
}

func TestParallelRunOncePreserved(t *testing.T) {
	base := task("base")
	a := task("a", "base")
	b := task("b", "base")
	top := parallelTask("top", "a", "b")
	m := newEngine(base, a, b, top)
	m.delay = 20 * time.Millisecond

	if err := Run(m, []Invocation{{Task: top}}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range m.order {
		if n == "base" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("base ran %d times under concurrency, want 1 (order=%v)", count, m.order)
	}
}

func TestParallelFirstErrorCancels(t *testing.T) {
	a := task("a")
	b := task("b")
	top := parallelTask("top", "a", "b")
	m := newEngine(a, b, top)
	m.failOn = "a"
	m.execErr = errors.New("boom")

	err := Run(m, []Invocation{{Task: top}})
	if err == nil {
		t.Fatal("expected an error from a failing parallel dep")
	}
	// top's body must not run if a dependency failed.
	for _, n := range m.order {
		if n == "top" {
			t.Error("top ran despite a failed parallel dependency")
		}
	}
}
