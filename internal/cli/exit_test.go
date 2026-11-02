package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/runtime/agent"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
)

// The decision table of spec 028 data-model.md: a body failure exits with the
// body's own code, never remapped; rune's own classes keep their codes.
func TestCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, ExitSuccess},
		{"usage", &UsageError{}, ExitUsage},
		{"validation", &ValidationError{}, ExitValidation},
		{"interrupted", &Interrupted{}, ExitInterrupt},
		{"shell body code", &TaskFailure{Err: &shell.ExecError{Code: 2}}, 2},
		{"interp body code", &TaskFailure{Err: &interp.ExecError{Code: 4}}, 4},
		{"body code equal to a rune code", &TaskFailure{Err: &shell.ExecError{Code: 130}}, 130},
		{"silent body code", &TaskFailure{Err: &shell.ExecError{Code: 6}, Silent: true}, 6},
		{"confirm declined", &TaskFailure{Err: errors.New(`task "x" was not confirmed`)}, ExitTaskFail},
		{"agent not installed", &TaskFailure{Err: &agent.NotInstalledError{Name: "x"}}, ExitTaskFail},
		{"shell error without status", &TaskFailure{Err: &shell.ExecError{Err: errors.New("parse")}}, ExitTaskFail},
		{"unclassified", errors.New("other"), ExitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeFor(tt.err); got != tt.want {
				t.Errorf("CodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// A failed body's exit code: only a positive executor code counts.
func TestBodyExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"shell code", &shell.ExecError{Code: 2}, 2},
		{"interp code", &interp.ExecError{Code: 4}, 4},
		{"interp code above 255", &interp.ExecError{Code: 300}, 300},
		{"interp negative code", &interp.ExecError{Code: -1}, 1},
		{"shell parse error without status", &shell.ExecError{Err: errors.New("parse error")}, 1},
		{"wrapped shell code", fmt.Errorf("run: %w", &shell.ExecError{Code: 3}), 3},
		{"wrapped interp code", fmt.Errorf("run: %w", &interp.ExecError{Code: 42}), 42},
		{"plain error", errors.New("not confirmed"), 1},
		{"agent error", &agent.NotInstalledError{Name: "x"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bodyExitCode(tt.err); got != tt.want {
				t.Errorf("bodyExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// [no-exit-message] hides the banner but keeps the code the same task would
// exit with without it (FR-008). The engine's ExitCode, which || hooks
// receive, is the same mapping (FR-009).
func TestClassifySilentFailureKeepsCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"body code", &shell.ExecError{Code: 6}, 6},
		{"agent not configured", &agent.NotConfiguredError{}, ExitUsage},
		{"run-time evaluation error", &eval.Error{Msg: "env var not set"}, ExitValidation},
		{"cancelled body", fmt.Errorf("task %q: %w", "slow", context.Canceled), ExitInterrupt},
		{"confirm declined", errors.New(`task "x" was not confirmed`), ExitTaskFail},
	}
	e := &engine{ctx: context.Background()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loud := CodeFor(classify(tt.err))
			silent := classify(&TaskFailure{Err: tt.err, Silent: true})
			if got := CodeFor(silent); got != tt.want || loud != tt.want {
				t.Errorf("code = %d silent, %d without the attribute, want %d", got, loud, tt.want)
			}
			if msg := BannerMessage(silent); msg != "" {
				t.Errorf("silent banner = %q, want none", msg)
			}
			if got := e.ExitCode(&TaskFailure{Err: tt.err, Silent: true}); got != tt.want {
				t.Errorf("hook code = %d, want %d", got, tt.want)
			}
		})
	}
}

// Once the run is cancelled, the error decides nothing: a child that traps
// SIGINT and exits 2 still makes rune exit 130, with its banner unchanged
// (and still suppressed for [no-exit-message]).
func TestClassifyRunErrCancelledRunIsInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &engine{ctx: ctx}
	body := &shell.ExecError{Task: "t", Line: "npm test", Code: 2}

	got := e.classifyRunErr(body)
	if code := CodeFor(got); code != ExitInterrupt {
		t.Errorf("code = %d, want %d", code, ExitInterrupt)
	}
	if msg, want := BannerMessage(got), body.Error(); msg != want {
		t.Errorf("banner = %q, want the body's %q", msg, want)
	}

	silent := e.classifyRunErr(&TaskFailure{Err: body, Silent: true})
	if code := CodeFor(silent); code != ExitInterrupt {
		t.Errorf("silent code = %d, want %d", code, ExitInterrupt)
	}
	if msg := BannerMessage(silent); msg != "" {
		t.Errorf("silent banner = %q, want none", msg)
	}
}

// A [no-exit-message] task wraps its body error in a silent TaskFailure, so a
// Ctrl-C'd body must still exit 130 and keep its banner suppressed (R14). The
// `set shell` override runs the body under the interpreter executor: the
// bundled shell's exec handler keeps a goroutine asleep for its kill timeout
// after a cancel, which goleak would report. `exec` leaves no orphaned sleep
// holding the stdout pipe.
func TestNoExitMessageInterruptExits130(t *testing.T) {
	for _, bin := range []string{"sh", "sleep"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	dir, _ := writeRunefile(t, "set shell := [\"sh\"]\n\n[no-exit-message]\nslow:\n    exec sleep 5\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()

	err := Run(Options{
		Cwd:    dir,
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Ctx:    ctx,
	}, []string{"slow"})

	if got := CodeFor(err); got != ExitInterrupt {
		t.Errorf("CodeFor(%v) = %d, want %d", err, got, ExitInterrupt)
	}
	if msg := BannerMessage(err); msg != "" {
		t.Errorf("banner = %q, want none for [no-exit-message]", msg)
	}
}
