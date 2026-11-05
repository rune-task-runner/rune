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

	err := eng.Execute(eng.tasks["t"], eval.Args{})

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

	err := eng.ExecuteFailHook(eng.tasks["t"], eval.Args{}, scheduler.Failure{TaskName: "build", ExitCode: 1})

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

	err := eng.Execute(eng.tasks["t"], eval.Args{})

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

	if err := eng.Execute(eng.tasks["t"], eval.Args{}); err != nil {
		t.Fatalf("Execute with --yes: %v", err)
	}
	if !strings.Contains(stdout.String(), "body") {
		t.Errorf("body did not run: stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("--yes still prompted: %q", stderr.String())
	}
}

// Spec 031: on the MCP server path, the operator's grant approves the named
// task in place of a prompt. Nothing is printed and nothing waits for input.
func TestConfirmGrantApprovesNamedTask(t *testing.T) {
	eng, stdout, stderr := confirmEngine(t, confirmSrc, nil, false, false)
	eng.grants = map[string]bool{"t": true}

	if err := eng.Execute(eng.tasks["t"], eval.Args{}); err != nil {
		t.Fatalf("Execute with a grant: %v", err)
	}
	if !strings.Contains(stdout.String(), "body") {
		t.Errorf("body did not run: stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("granted task wrote to stderr: %q", stderr.String())
	}
}

// A grant skips the gate even where a prompt could be answered: a terminal
// holding "n" would decline if it were asked, as in TestConfirmYesSkipsStdin.
// (stdin itself still reaches the body, so a reader that fails on any read
// cannot tell the gate from the body.)
func TestConfirmGrantSkipsPrompt(t *testing.T) {
	eng, stdout, stderr := confirmEngine(t, confirmSrc, strings.NewReader("n\n"), true, false)
	eng.grants = map[string]bool{"t": true}

	if err := eng.Execute(eng.tasks["t"], eval.Args{}); err != nil {
		t.Fatalf("Execute with a grant: %v", err)
	}
	if !strings.Contains(stdout.String(), "body") {
		t.Errorf("body did not run: stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("granted task still prompted: %q", stderr.String())
	}
}

// Spec 031 FR-005/FR-006 (data-model "Outcomes by path"): a grant approves
// its own task wherever a run reaches it, and never any other task. Every
// engine here has no stdin, as on the MCP path.
func TestConfirmGrantIsPerTask(t *testing.T) {
	const (
		depSrc   = "[confirm]\nclean:\n    @echo ran:clean\n\nrelease: clean\n    @echo ran:release\n"
		chainSrc = "[confirm]\nwipe:\n    @echo ran:wipe\n\n[confirm]\nclean: wipe\n    @echo ran:clean\n"
		postSrc  = "[confirm]\nwipe:\n    @echo ran:wipe\n\nbuild: && wipe\n    @echo ran:build\n"
		failSrc  = "[confirm]\nwipe:\n    @echo ran:wipe\n\nbuild: || wipe\n    @echo ran:build\n    @exit 1\n"
		privSrc  = "[private]\n[confirm]\n_wipe:\n    @echo ran:_wipe\n\nship: _wipe\n    @echo ran:ship\n"
	)
	cases := []struct {
		name     string
		src      string
		call     string
		grants   []string
		ran      []string
		notRan   []string
		wantCode int
	}{
		{"granted dependency runs", depSrc, "release", []string{"clean"}, []string{"clean", "release"}, nil, 0},
		{"ungranted dependency stops the run", depSrc, "release", nil, nil, []string{"clean", "release"}, 1},
		{"grant does not reach the granted task's own dependency", chainSrc, "clean", []string{"clean"}, nil, []string{"wipe", "clean"}, 1},
		{"ungranted post-hook does not run", postSrc, "build", nil, []string{"build"}, []string{"wipe"}, 1},
		{"granted post-hook runs", postSrc, "build", []string{"wipe"}, []string{"build", "wipe"}, nil, 0},
		{"ungranted failure hook does not run", failSrc, "build", nil, []string{"build"}, []string{"wipe"}, 1},
		{"granted failure hook runs", failSrc, "build", []string{"wipe"}, []string{"build", "wipe"}, nil, 1},
		{"granted private dependency runs", privSrc, "ship", []string{"_wipe"}, []string{"_wipe", "ship"}, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng, stdout, stderr := confirmEngine(t, c.src, nil, false, false)
			eng.grants = map[string]bool{}
			for _, g := range c.grants {
				eng.grants[g] = true
			}
			err := scheduler.Run(eng, []scheduler.Invocation{{Task: eng.tasks[c.call]}})
			if got := CodeFor(eng.classifyRunErr(err)); got != c.wantCode {
				t.Errorf("exit code = %d, want %d (err %v)", got, c.wantCode, err)
			}
			for _, name := range c.ran {
				if !strings.Contains(stdout.String(), "ran:"+name+"\n") {
					t.Errorf("%s did not run: stdout = %q", name, stdout.String())
				}
			}
			for _, name := range c.notRan {
				if strings.Contains(stdout.String(), "ran:"+name+"\n") {
					t.Errorf("%s ran: stdout = %q", name, stdout.String())
				}
			}
			if c.src == failSrc && len(c.grants) == 0 {
				var warnings []string
				for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
					if strings.Contains(line, "failure hook") {
						warnings = append(warnings, line)
					}
				}
				if len(warnings) != 1 || !strings.Contains(warnings[0], "wipe") {
					t.Errorf("want one warning about the declined hook wipe, stderr = %q", stderr.String())
				}
			}
		})
	}
}
