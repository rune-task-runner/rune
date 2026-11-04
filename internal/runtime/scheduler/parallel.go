package scheduler

import (
	"runtime"

	"golang.org/x/sync/errgroup"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
)

// runDepsParallel runs a task's dependencies concurrently, bounded by the CPU
// count, with first-error cancellation. Run-once memoization is preserved by the
// shared, mutex-guarded singleflight state in run().
func (s *state) runDepsParallel(curTask *ast.Task, curParams eval.Args, deps []*ast.DepCall, chain []string) error {
	g := new(errgroup.Group)
	limit := runtime.NumCPU()
	if limit < 1 {
		limit = 1
	}
	g.SetLimit(limit)
	for _, dep := range deps {
		dep := dep
		g.Go(func() error {
			return s.runDep(curTask, curParams, dep, chain)
		})
	}
	return g.Wait()
}
