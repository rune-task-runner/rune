package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

const confirmSrc = "[confirm]\nt:\n    @echo body\n"

// confirmEngine builds a run-mode engine over src whose stdin and terminal
// state are injected, so every branch of the [confirm] gate is testable on any
// OS without a real terminal (spec 027).
func confirmEngine(t *testing.T, src string, stdin io.Reader, tty, yes bool) (*engine, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	assigns := indexAssignments(f)
	scope := eval.NewScope(assigns, map[string]string{})
	settings, _ := config.ResolveSettings(f, eval.New(scope))
	var stdout, stderr bytes.Buffer
	opts := Options{Stdin: stdin, Stdout: &stdout, Stderr: &stderr, Quiet: true, Yes: yes}
	return &engine{
		file:      f,
		src:       newSourceProvider("Runefile", []byte(src)),
		tasks:     indexTasks(f),
		assigns:   assigns,
		overrides: map[string]string{},
		scope:     scope,
		settings:  settings,
		workDir:   t.TempDir(),
		root:      t.TempDir(),
		opts:      opts,
		theme:     opts.themeStderr(),
		plan:      planRun,
		now:       func() string { return "" },
		ctx:       context.Background(),
		goos:      "linux",
		stdinTTY:  tty,
	}, &stdout, &stderr
}

// failReader fails the test if anything reads it: the non-interactive and
// --yes paths must leave stdin untouched (FR-003, FR-006).
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Errorf("stdin was read")
	return 0, io.EOF
}

// A non-terminal stdin declines without prompting or reading, whatever it
// holds (FR-003, FR-004), and the body never starts (FR-005).
func TestConfirmNonTerminalDeclinesWithoutReading(t *testing.T) {
	eng, stdout, stderr := confirmEngine(t, confirmSrc, failReader{t}, false, false)

	err := eng.Execute(eng.tasks["t"], nil)

	var tf *TaskFailure
	if !errors.As(err, &tf) {
		t.Fatalf("Execute error = %v, want *TaskFailure", err)
	}
	if !errors.Is(err, scheduler.ErrBodyNotRun) {
		t.Errorf("error chain lacks ErrBodyNotRun, so || hooks would fire: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("body ran: stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("unanswerable prompt printed: %q", stderr.String())
	}
}

func TestConfirmNonTerminalFailHookDeclinesWithoutReading(t *testing.T) {
	eng, stdout, _ := confirmEngine(t, confirmSrc, failReader{t}, false, false)

	err := eng.ExecuteFailHook(eng.tasks["t"], nil, scheduler.Failure{TaskName: "build", ExitCode: 1})

	var tf *TaskFailure
	if !errors.As(err, &tf) {
		t.Fatalf("ExecuteFailHook error = %v, want *TaskFailure", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("hook body ran: stdout = %q", stdout.String())
	}
}

// The MCP adapter passes no stdin at all; its decline must stay exactly as it
// was (FR-010).
func TestConfirmNoStdinKeepsSilentDecline(t *testing.T) {
	eng, _, stderr := confirmEngine(t, confirmSrc, nil, false, false)

	err := eng.Execute(eng.tasks["t"], nil)

	var tf *TaskFailure
	if !errors.As(err, &tf) {
		t.Fatalf("Execute error = %v, want *TaskFailure", err)
	}
	if !tf.Silent {
		t.Error("nil-stdin decline must stay silent")
	}
	if got, want := err.Error(), `task "t" was not confirmed`; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("nil-stdin decline wrote to stderr: %q", stderr.String())
	}
}

// --yes approves without consulting the gate at all (FR-006): a terminal
// holding "n" would decline if it were asked. That stdin then reaches the body
// untouched is pinned end to end by TestConfirmYesPassesStdinThrough.
func TestConfirmYesSkipsStdin(t *testing.T) {
	eng, stdout, stderr := confirmEngine(t, confirmSrc, strings.NewReader("n\n"), true, true)

	if err := eng.Execute(eng.tasks["t"], nil); err != nil {
		t.Fatalf("Execute with --yes: %v", err)
	}
	if !strings.Contains(stdout.String(), "body") {
		t.Errorf("body did not run: stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("--yes still prompted: %q", stderr.String())
	}
}
