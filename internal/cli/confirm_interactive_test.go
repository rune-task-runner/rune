package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

// With a terminal on stdin the prompt behaves exactly as before spec 027:
// only y/yes (any case, whitespace trimmed) approves (FR-002).
func TestConfirmTerminalAnswers(t *testing.T) {
	const customSrc = "[confirm(\"Sure?\")]\nt:\n    @echo body\n"
	tests := []struct {
		name       string
		src        string
		input      string
		wantPrompt string
		approve    bool
	}{
		{"y", confirmSrc, "y\n", `Run "t"? [y/N] `, true},
		{"yes", confirmSrc, "yes\n", `Run "t"? [y/N] `, true},
		{"YES", confirmSrc, "YES\n", `Run "t"? [y/N] `, true},
		{"padded y", confirmSrc, "  y  \n", `Run "t"? [y/N] `, true},
		{"n", confirmSrc, "n\n", `Run "t"? [y/N] `, false},
		{"empty line", confirmSrc, "\n", `Run "t"? [y/N] `, false},
		{"end of input", confirmSrc, "", `Run "t"? [y/N] `, false},
		{"custom prompt", customSrc, "n\n", "Sure? [y/N] ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, stdout, stderr := confirmEngine(t, tt.src, strings.NewReader(tt.input), true, false)

			err := eng.Execute(eng.tasks["t"], nil)

			if !strings.Contains(stderr.String(), tt.wantPrompt) {
				t.Errorf("stderr = %q, want prompt %q", stderr.String(), tt.wantPrompt)
			}
			if tt.approve {
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if !strings.Contains(stdout.String(), "body") {
					t.Errorf("body did not run: stdout = %q", stdout.String())
				}
				return
			}
			var tf *TaskFailure
			if !errors.As(err, &tf) || !tf.Silent {
				t.Fatalf("Execute error = %#v, want a silent *TaskFailure", err)
			}
			if !errors.Is(err, scheduler.ErrBodyNotRun) {
				t.Errorf("error chain lacks ErrBodyNotRun: %v", err)
			}
			if got, want := err.Error(), `task "t" was not confirmed`; got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
			if stdout.Len() != 0 {
				t.Errorf("body ran after a decline: stdout = %q", stdout.String())
			}
		})
	}
}

// The prompt consumes its answer line and nothing more: input typed ahead
// belongs to the next prompt or to the body.
func TestConfirmReadsOnlyTheAnswerLine(t *testing.T) {
	stdin := strings.NewReader("y\nnext\n")
	eng, _, _ := confirmEngine(t, confirmSrc, stdin, true, false)

	if got := eng.confirm(eng.tasks["t"], ""); got != confirmApproved {
		t.Fatalf("confirm = %v, want approved", got)
	}
	if rest, _ := io.ReadAll(stdin); string(rest) != "next\n" {
		t.Errorf("unread input = %q, want %q", rest, "next\n")
	}
}

// promptLog hands each write to the test, so it can see when a prompt appears.
type promptLog chan string

func (p promptLog) Write(b []byte) (int, error) {
	p <- string(b)
	return len(b), nil
}

// [parallel] dependencies may reach the gate at once; each prompt must get
// its own answer, so a second prompt waits until the first is answered.
func TestConfirmSerializesConcurrentPrompts(t *testing.T) {
	const src = "[confirm]\na:\n    @echo a\n\n[confirm]\nb:\n    @echo b\n"
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	eng, _, _ := confirmEngine(t, src, pr, true, false)
	prompts := make(promptLog, 2)
	eng.opts.Stderr = prompts
	results := make(chan confirmOutcome, 2)

	go func() { results <- eng.confirm(eng.tasks["a"], "") }()
	if p := <-prompts; !strings.Contains(p, `Run "a"?`) {
		t.Fatalf("first prompt = %q", p)
	}
	go func() { results <- eng.confirm(eng.tasks["b"], "") }()
	select {
	case p := <-prompts:
		t.Fatalf("prompt %q shown while the first prompt awaits its answer", p)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := io.WriteString(pw, "y\n"); err != nil {
		t.Fatal(err)
	}
	if got := <-results; got != confirmApproved {
		t.Errorf("first answer = %v, want approved", got)
	}
	if p := <-prompts; !strings.Contains(p, `Run "b"?`) {
		t.Fatalf("second prompt = %q", p)
	}
	if _, err := io.WriteString(pw, "n\n"); err != nil {
		t.Fatal(err)
	}
	if got := <-results; got != confirmDeclined {
		t.Errorf("second answer = %v, want declined", got)
	}
}
