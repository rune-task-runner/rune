package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/rune-task-runner/rune/internal/ast"
)

// confirmOutcome is the result of the [confirm] gate for one task.
type confirmOutcome int

const (
	confirmApproved   confirmOutcome = iota
	confirmDeclined                  // a person answered anything but y/yes, or there is no stdin stream
	confirmNoTerminal                // stdin exists but is not a terminal, so nobody could answer
)

// confirmFailure runs the [confirm] gate for task. It returns nil when the
// body may run, otherwise the failure to report in its place.
func (e *engine) confirmFailure(task *ast.Task, prompt string) *TaskFailure {
	if e.opts.Yes {
		return nil
	}
	switch e.confirm(task, prompt) {
	case confirmApproved:
		return nil
	case confirmNoTerminal:
		// Not silent: the banner is the only place the caller learns about
		// --yes. [no-exit-message] does not apply; the body never started.
		return &TaskFailure{Err: noTerminalError(task, prompt)}
	default:
		return &TaskFailure{Err: errorf("task %q was not confirmed", task.Name), Silent: true}
	}
}

// confirm asks the operator to approve a destructive ([confirm]) task. A nil
// stdin (the MCP adapter) declines silently, as it always has. Any other
// non-terminal stdin declines without being read: piped or redirected input
// is not a person answering, whatever it says (spec 027, issue #30).
func (e *engine) confirm(task *ast.Task, prompt string) confirmOutcome {
	if e.opts.Stdin == nil {
		return confirmDeclined
	}
	if !e.stdinTTY {
		return confirmNoTerminal
	}
	// [parallel] dependencies can reach the gate at once; one prompt at a time
	// keeps each answer paired with the question it was typed for.
	e.confirmMu.Lock()
	defer e.confirmMu.Unlock()
	// A destructive-action prompt uses the warning role; the [y/N] hint stays
	// plain so the input convention is never obscured by styling (014 C3).
	fmt.Fprintf(e.opts.Stderr, "%s [y/N] ", e.theme.Warning.Render(confirmQuestion(task, prompt)))
	switch strings.ToLower(strings.TrimSpace(readLine(e.opts.Stdin))) {
	case "y", "yes":
		return confirmApproved
	default:
		return confirmDeclined
	}
}

// readLine reads one line a byte at a time, so input after the newline stays
// unread for the next prompt or the body. A read error ends the line with
// whatever arrived before it; EOF on an empty line therefore declines like any
// other non-affirmative answer.
func readLine(r io.Reader) string {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				return string(line)
			}
			line = append(line, b[0])
		}
		if err != nil {
			return string(line)
		}
	}
}

// confirmQuestion is the question shown for a [confirm] task: the author's
// prompt text, or a default naming the task.
func confirmQuestion(task *ast.Task, prompt string) string {
	if prompt == "" {
		return fmt.Sprintf("Run %q?", task.Name)
	}
	return prompt
}

// noTerminalError explains a non-interactive decline and names the fix
// (contracts/confirm-cli.md §2).
func noTerminalError(task *ast.Task, prompt string) error {
	return errorf("task %q was not confirmed (%s): stdin is not a terminal; pass --yes to approve",
		task.Name, oneLine(confirmQuestion(task, prompt)))
}

// oneLine escapes control characters, so the banner stays a single line
// whatever the author's prompt contains.
func oneLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
