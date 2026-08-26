// Package scheduler builds and runs the task dependency DAG. It memoizes each
// (namespace, task, canonical-args) so a node runs at most once per invocation
// (FR-005), runs dependencies before the body and post-hooks after, detects
// cycles, and fails fast on the first error. [parallel] dependencies run
// concurrently (bounded by CPU count) while preserving run-once semantics.
// Dependency and post-hook targets the Engine reports unavailable (e.g.
// declared for another OS) are skipped silently — they never execute and
// leave no memo entry — while the depending task still runs (spec 020).
//
// || failure hooks (spec 022) are the one deliberate exception to run-once:
// a hook body runs once per FAILING task (its dependencies still memoize),
// carries the failure context, never alters the original error, and never
// chains its own || hooks. User cancellation fires no hooks. Unavailable or
// failing hooks warn via Engine.Warnf instead of being silent — a silent
// skip would hide why no diagnosis appeared.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
)

// ErrBodyNotRun marks an Execute error raised before the task's body started
// (e.g. a declined [confirm] prompt). Failure hooks fire for body failures
// only (spec 022 FR-003), so errors wrapping this sentinel suppress the
// task's || hooks.
var ErrBodyNotRun = errors.New("task body did not run")

// Engine resolves dependencies and executes task bodies. The CLI layer
// implements it (it owns the evaluator, parameter binding, and executors).
type Engine interface {
	// ResolveDep evaluates a dependency/post-hook call in the scope of the
	// calling task, returning the target task and its bound parameters.
	ResolveDep(curTask *ast.Task, curParams map[string]string, dep *ast.DepCall) (*ast.Task, map[string]string, error)
	// Execute runs a single task body with its bound parameters.
	Execute(task *ast.Task, params map[string]string) error
	// ExecuteFailHook runs a || failure-hook body with the failure context
	// available to it (spec 022 FR-009). Called outside the memo table.
	ExecuteFailHook(task *ast.Task, params map[string]string, f Failure) error
	// Warnf emits a one-line warning (failure-hook skip or failure).
	Warnf(format string, args ...any)
	// Namespace returns the memoization namespace for a task (mod path, or "").
	Namespace(task *ast.Task) string
	// Available reports whether a task may run on this host. Unavailable
	// dependency/post-hook targets are skipped silently.
	Available(task *ast.Task) bool
}

// Failure is the context of one task-body failure, handed to every failure
// hook fired for it.
type Failure struct {
	TaskName string // user-visible (namespaced) name of the failed task
	ExitCode int    // exit code of the failed body; 1 when non-numeric
}

// Invocation is a task plus its resolved parameters (a scheduler root).
type Invocation struct {
	Task   *ast.Task
	Params map[string]string
}

// Run executes the given root invocations in order, sharing one memo table so
// repeated tasks run once across the whole invocation.
func Run(engine Engine, roots []Invocation) error {
	s := &state{
		engine:   engine,
		done:     map[string]error{},
		inflight: map[string]*sync.WaitGroup{},
	}
	for _, r := range roots {
		if err := s.run(r.Task, r.Params, nil); err != nil {
			return err
		}
	}
	return nil
}

type state struct {
	engine Engine

	mu       sync.Mutex
	done     map[string]error           // completed keys -> result
	inflight map[string]*sync.WaitGroup // keys currently running

	// hookMu serializes || hook BODY runs. Hook bodies live outside the
	// memo/inflight table (once per failing task), so without this two
	// [parallel] siblings failing at once would run the same hook body
	// concurrently with itself — interleaving its output and racing any
	// side effects. Hook dependencies still parallelize normally.
	hookMu sync.Mutex
}

// run executes a task once (singleflight by memo key). chain is the
// goroutine-local dependency path of task NAMES, used for cycle detection
// (concurrency-safe because it is passed by value, never shared).
func (s *state) run(task *ast.Task, params map[string]string, chain []string) error {
	for _, name := range chain {
		if name == task.Name {
			return &CycleError{Path: cyclePath(chain, task.Name)}
		}
	}
	key := memoKey(s.engine.Namespace(task), task.Name, params)

	s.mu.Lock()
	if err, ok := s.done[key]; ok {
		s.mu.Unlock()
		return err
	}
	if wg, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		wg.Wait()
		s.mu.Lock()
		err := s.done[key]
		s.mu.Unlock()
		return err
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	s.inflight[key] = wg
	s.mu.Unlock()

	err := s.execute(task, params, append(append([]string{}, chain...), task.Name))

	s.mu.Lock()
	s.done[key] = err
	delete(s.inflight, key)
	s.mu.Unlock()
	wg.Done()
	return err
}

// execute runs dependencies (parallel if [parallel]), the body, then post-hooks.
func (s *state) execute(task *ast.Task, params map[string]string, chain []string) error {
	if task.Attr(ast.AttrParallel) != nil {
		if err := s.runDepsParallel(task, params, task.Deps, chain); err != nil {
			return err
		}
	} else {
		for _, dep := range task.Deps {
			if err := s.runDep(task, params, dep, chain); err != nil {
				return err
			}
		}
	}

	if err := s.engine.Execute(task, params); err != nil {
		s.runFailHooks(task, params, err, chain)
		return err
	}

	for _, hook := range task.PostHooks {
		if err := s.runDep(task, params, hook, chain); err != nil {
			return err
		}
	}
	return nil
}

// runFailHooks fires the task's || hooks after its own body failed. Hook
// outcomes never surface: the caller returns the original body error, so the
// exit code is preserved (FR-004); hook failures and skips become warnings.
// User cancellation aborts everything — no post-mortem runs (FR-002).
func (s *state) runFailHooks(task *ast.Task, params map[string]string, bodyErr error, chain []string) {
	if len(task.FailHooks) == 0 || errors.Is(bodyErr, context.Canceled) || errors.Is(bodyErr, ErrBodyNotRun) {
		return
	}
	f := Failure{TaskName: task.Name, ExitCode: failureExitCode(bodyErr)}
	for _, hook := range task.FailHooks {
		target, hookParams, err := s.engine.ResolveDep(task, params, hook)
		if err != nil {
			s.engine.Warnf("failure hook %s could not be resolved: %v", hook.Name, err)
			continue
		}
		if !s.engine.Available(target) {
			s.engine.Warnf("failure hook %s skipped: not available on this OS", target.Name)
			continue
		}
		if err := s.runFailHook(target, hookParams, f, chain); err != nil {
			if errors.Is(err, context.Canceled) {
				// The run is being cancelled (Ctrl-C): stop firing hooks
				// quietly — a "failure hook failed" warning here would be
				// noise about the user's own interrupt (FR-002).
				return
			}
			s.engine.Warnf("failure hook %s failed: %v", target.Name, err)
		}
	}
}

// runFailHook runs one hook occurrence: dependencies and && post-hooks under
// normal memoized semantics, the body via ExecuteFailHook OUTSIDE the memo
// table (once per failure, spec 022 clarification Q2). The hook's own ||
// hooks are deliberately not consulted — post-mortems do not chain.
func (s *state) runFailHook(hook *ast.Task, params map[string]string, f Failure, chain []string) error {
	for _, name := range chain {
		if name == hook.Name {
			return &CycleError{Path: cyclePath(chain, hook.Name)}
		}
	}
	chain = append(append([]string{}, chain...), hook.Name)

	if hook.Attr(ast.AttrParallel) != nil {
		if err := s.runDepsParallel(hook, params, hook.Deps, chain); err != nil {
			return err
		}
	} else {
		for _, dep := range hook.Deps {
			if err := s.runDep(hook, params, dep, chain); err != nil {
				return err
			}
		}
	}

	s.hookMu.Lock()
	err := s.engine.ExecuteFailHook(hook, params, f)
	s.hookMu.Unlock()
	if err != nil {
		return err
	}

	for _, ph := range hook.PostHooks {
		if err := s.runDep(hook, params, ph, chain); err != nil {
			return err
		}
	}
	return nil
}

// failureExitCode extracts the failed body's exit code: the shell or
// interpreter executor's real exit status when available, else 1 (mirroring
// the CLI's ExitTaskFail). A zero Code on an executor error means no exit
// status was recorded (e.g. a shell parse error) — the task still failed, so
// that also falls back to 1 rather than reporting a bogus "exit 0".
func failureExitCode(err error) int {
	var se *shell.ExecError
	if errors.As(err, &se) && se.Code != 0 {
		return se.Code
	}
	var ie *interp.ExecError
	if errors.As(err, &ie) && ie.Code != 0 {
		return ie.Code
	}
	return 1
}

func (s *state) runDep(curTask *ast.Task, curParams map[string]string, dep *ast.DepCall, chain []string) error {
	target, depParams, err := s.engine.ResolveDep(curTask, curParams, dep)
	if err != nil {
		return err
	}
	// A target declared for another platform is skipped before entering
	// run(): no execution, no memo entry, no chain participation. This is
	// what turns per-OS deps into a dispatch pattern (spec 020 US3).
	if !s.engine.Available(target) {
		return nil
	}
	return s.run(target, depParams, chain)
}

// memoKey builds a canonical, order-stable key for a task invocation.
func memoKey(namespace, name string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(namespace)
	b.WriteString("::")
	b.WriteString(name)
	b.WriteByte('(')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, "%s=%s", k, params[k])
	}
	b.WriteByte(')')
	return b.String()
}

// CycleError reports a dependency cycle with the offending path.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "dependency cycle: " + strings.Join(e.Path, " → ")
}

// cyclePath returns the cycle starting from where `back` first appears in chain.
func cyclePath(chain []string, back string) []string {
	start := 0
	for i, n := range chain {
		if n == back {
			start = i
			break
		}
	}
	return append(append([]string{}, chain[start:]...), back)
}
