package cli

import (
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/token"
	"github.com/rune-task-runner/rune/mcpserver"
)

// streamBuffer is a capture of one stream of a call: an OutputBuffer, or a
// LineBuffer for a task with an output policy.
type streamBuffer interface {
	io.Writer
	String() string
}

// newStreamBuffer returns the capture of one stream of a call of t: the
// lines its [agent-output] keeps, or the head and tail of spec 042 when it
// has none (spec 057). Only the called task's policy counts.
func newStreamBuffer(limit int, t *ast.Task) streamBuffer {
	if head, tail, ok := t.AgentOutput(); ok {
		return mcpserver.NewLineBuffer(limit, head, tail)
	}
	return mcpserver.NewOutputBuffer(limit)
}

// keptStreams returns the kept stdout and stderr of a call of t and their
// report, which is nil when t has no output policy. Each stream is read
// once, so its report always describes the text the agent gets.
func keptStreams(t *ast.Task, stdout, stderr streamBuffer) (string, string, *mcpserver.OutputReport) {
	head, tail, ok := t.AgentOutput()
	out, okOut := stdout.(*mcpserver.LineBuffer)
	errs, okErr := stderr.(*mcpserver.LineBuffer)
	if !ok || !okOut || !okErr {
		return stdout.String(), stderr.String(), nil
	}
	outText, outRep := out.Kept()
	errText, errRep := errs.Kept()
	return outText, errText, &mcpserver.OutputReport{Head: head, Tail: tail, Stdout: outRep, Stderr: errRep}
}

// failureFacts is what the run of a call of called observed about its
// failure, for its [agent-summary] (spec 057). It reads the run error before
// classification, decides the kind in the order of
// contracts/mcp-results.md, and masks the command and the reason. The server
// bounds the fields and adds the rest. cancelled is whether classification
// found the call interrupted: read once there, so a cancellation that comes
// later cannot give a summary its exit code contradicts.
func failureFacts(cancelled bool, called *ast.Task, runErr error, root string, mask func(string) string) *mcpserver.FailureSummary {
	f := &mcpserver.FailureSummary{Task: called.Name}
	var step *shell.ExecError
	var script *interp.ExecError
	isStep := errors.As(runErr, &step)
	isScript := !isStep && errors.As(runErr, &script)
	switch {
	case cancelled:
		// The child may exit with any status when it is interrupted.
		f.Kind = mcpserver.FailureCancelled
		if isStep {
			f.FailedTask = step.Task
		} else if isScript {
			f.FailedTask = script.Task
		}
	// An executor that failed before it ran a body line (a missing working
	// directory, a temporary file it could not write) leaves no span: the
	// run stopped before a step, so it is kind error.
	case isStep && step.Span.File != "":
		f.Kind = mcpserver.FailureStep
		f.FailedTask = step.Task
		f.File, f.Line = spanPlace(step.Span, root)
		f.Command = mask(step.Line)
		f.Reason = mask(execReason(step.Code, step.Err))
	case isScript && script.Span.File != "":
		f.Kind = mcpserver.FailureScript
		f.FailedTask = script.Task
		f.File, f.Line = spanPlace(script.Span, root)
		f.Reason = mask(execReason(script.Code, script.Err))
	case runErr != nil:
		f.Kind = mcpserver.FailureError
		f.Reason = mask(firstLine(runErr.Error()))
		if isStep {
			f.FailedTask = step.Task
		} else if isScript {
			f.FailedTask = script.Task
		}
	default:
		f.Kind = mcpserver.FailureUnknown
	}
	return f
}

// execReason is why a body failed: its exit status, or the executor's error
// when it has none.
func execReason(code int, err error) string {
	if err != nil {
		return firstLine(err.Error())
	}
	return "exit status " + strconv.Itoa(code)
}

// spanPlace is the file and line of a span as a summary shows them: the file
// relative to the directory of the Runefile with forward slashes, or as Rune
// read it when it is outside that directory.
func spanPlace(sp token.Span, root string) (string, int) {
	file := sp.File
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(root, file); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			file = rel
		}
	}
	return filepath.ToSlash(file), sp.Start.Line
}
