// Package cli wires the command-line surface: Runefile resolution, the parse →
// analyze → run pipeline, task dispatch, and exit-code mapping.
package cli

import (
	"errors"

	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
)

// Exit codes (spec 001 contracts/cli.md FR-021; spec 028 contracts/exit-codes.md).
// A task body failure exits with the body's own code instead of a constant.
const (
	ExitSuccess    = 0   // all requested tasks succeeded
	ExitTaskFail   = 1   // a task failed without a body exit code
	ExitUsage      = 2   // usage error / no Runefile / unknown task / bad args
	ExitValidation = 3   // static parse/analyze error — nothing executed
	ExitInterrupt  = 130 // interrupted (SIGINT)
)

// UsageError marks an error as a usage/discovery problem (exit 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// ValidationError marks a static parse/analyze failure (exit 3). The diagnostics
// have already been rendered to stderr by the caller; this type only carries the
// exit code intent.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// TaskFailure marks a task failure. It exits with the wrapped body's exit code,
// or 1 when there is none. Silent suppresses the trailing error banner (the
// [no-exit-message] attribute) without changing the exit code.
type TaskFailure struct {
	Err    error
	Silent bool
}

func (e *TaskFailure) Error() string { return e.Err.Error() }
func (e *TaskFailure) Unwrap() error { return e.Err }

// CommandFailure marks a built-in command that could not do what was asked
// (exit 1). It is the non-task counterpart of TaskFailure: without it such an
// error would fall through to the usage exit code.
type CommandFailure struct{ Err error }

func (e *CommandFailure) Error() string { return e.Err.Error() }
func (e *CommandFailure) Unwrap() error { return e.Err }

// Interrupted marks a SIGINT (exit 130).
type Interrupted struct{ Err error }

func (e *Interrupted) Error() string {
	if e.Err == nil {
		return "interrupted"
	}
	return e.Err.Error()
}
func (e *Interrupted) Unwrap() error { return e.Err }

// CodeFor maps an error returned by Run to a process exit code.
func CodeFor(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ExitValidation
	}
	var ti *Interrupted
	if errors.As(err, &ti) {
		return ExitInterrupt
	}
	var tf *TaskFailure
	if errors.As(err, &tf) {
		return bodyExitCode(err)
	}
	var cf *CommandFailure
	if errors.As(err, &cf) {
		return ExitTaskFail
	}
	// Default: treat unknown errors as usage problems.
	return ExitUsage
}

// bodyExitCode extracts a failed body's exit code: the shell or interpreter
// executor's real exit status when available, else 1. Only a positive Code
// counts: 0 means no status was recorded (e.g. a shell parse error) and a
// negative one means the platform reported none, so both fall back to 1 rather
// than claiming success or leaking -1.
func bodyExitCode(err error) int {
	var se *shell.ExecError
	if errors.As(err, &se) && se.Code > 0 {
		return se.Code
	}
	var ie *interp.ExecError
	if errors.As(err, &ie) && ie.Code > 0 {
		return ie.Code
	}
	return ExitTaskFail
}

// usagef builds a UsageError with a formatted message.
func usagef(format string, args ...any) error {
	return &UsageError{Err: errorf(format, args...)}
}
