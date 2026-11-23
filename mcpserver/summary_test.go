package mcpserver

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Spec 057 US2: the failure summary section, line by line, for each kind.
func TestRenderSummary(t *testing.T) {
	cases := []struct {
		name string
		f    FailureSummary
		want string
	}{
		{
			"step with everything",
			FailureSummary{
				Kind: FailureStep, Task: "test", FailedTask: "build", File: "Runefile", Line: 12, Command: "go build ./...",
				Reason: "exit status 2", FixSuggestion: true, StdoutTail: "ok\nFAIL\n", StderrTail: "boom",
			},
			"[failure summary - from rune]\ntask: test\nfailed task: build\nstep: Runefile:12: go build ./...\nreason: exit status 2\n" +
				"fix suggestion: follows, from the task's || failure hooks\nlast lines of stdout:\nok\nFAIL\nlast lines of stderr:\nboom",
		},
		{
			"script",
			FailureSummary{Kind: FailureScript, Task: "gen", FailedTask: "gen", File: "lib/gen.rune", Line: 3, Reason: "exit status 1"},
			"[failure summary - from rune]\ntask: gen\nfailed task: gen\nstep: lib/gen.rune:3: the task's script\nreason: exit status 1",
		},
		{
			"error",
			FailureSummary{Kind: FailureError, Task: "deploy", Reason: "environment variable not set: REGION", StderrTail: "x\n"},
			"[failure summary - from rune]\ntask: deploy\nstep: none, the run stopped before a body line failed\nreason: environment variable not set: REGION\nlast lines of stderr:\nx",
		},
		{
			"cancelled",
			FailureSummary{Kind: FailureCancelled, Task: "test", FailedTask: "test", Reason: cancelledReason},
			"[failure summary - from rune]\ntask: test\nfailed task: test\nreason: the call was cancelled",
		},
		{
			"unknown",
			FailureSummary{Kind: FailureUnknown, Task: "apply", Reason: unknownReason, StdoutTail: "done\n"},
			"[failure summary - from rune]\ntask: apply\nreason: not known, Rune has only the exit status\nlast lines of stdout:\ndone",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderSummary(c.f); got != c.want {
				t.Errorf("renderSummary =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

// The section stands between the exit line and the fix suggestion section.
func TestRenderTextWithSummary(t *testing.T) {
	r := Result{
		Stdout: "out\n", Stderr: "err\n", ExitCode: 1, FixSuggestion: "re-run with -update",
		Failure: &FailureSummary{Kind: FailureStep, Task: "test", FailedTask: "test", File: "Runefile", Line: 4, Command: "go test ./...", Reason: "exit status 1"},
	}
	got := renderText(capResult(r, 0))
	want := "out\nerr\n[exit 1]\n[failure summary - from rune]\ntask: test\nfailed task: test\nstep: Runefile:4: go test ./...\nreason: exit status 1\n" +
		"fix suggestion: follows, from the task's || failure hooks\nlast lines of stdout:\nout\nlast lines of stderr:\nerr\n" +
		"[fix suggestion - from Runefile || failure hooks]\nre-run with -update"
	if got != want {
		t.Errorf("renderText =\n%s\nwant\n%s", got, want)
	}
	r.FixSuggestion = ""
	if got := renderText(capResult(r, 0)); !strings.HasSuffix(got, "last lines of stderr:\nerr") {
		t.Errorf("without a fix suggestion the section ends the text: %q", got)
	}
}

// Spec 057 FR-022, FR-026: every field is one line and bounded, after the
// engine masked it.
func TestCompleteSummaryBounds(t *testing.T) {
	t.Run("control characters and invalid UTF-8", func(t *testing.T) {
		f := completeSummary(FailureSummary{
			Kind: FailureStep, Task: "t", File: "Runefile", Line: 1,
			Command: "a\nb\x00c\x7fd\te\xc2\x85f", Reason: "bad \xff byte",
		}, Result{ExitCode: 1})
		if f.Command != "a b c d\te f" {
			t.Errorf("command = %q", f.Command)
		}
		if f.Reason != "bad "+string(utf8.RuneError)+" byte" {
			t.Errorf("reason = %q", f.Reason)
		}
	})

	t.Run("each field at its size", func(t *testing.T) {
		long := strings.Repeat("n", 300)
		f := completeSummary(FailureSummary{
			Kind: FailureStep, Task: long, FailedTask: long, File: strings.Repeat("f", 1000),
			Line: 1, Command: strings.Repeat("c", 1000), Reason: strings.Repeat("r", 1000),
		}, Result{ExitCode: 1})
		for name, c := range map[string]struct {
			got  string
			size int
			full int
		}{
			"task": {f.Task, maxNameBytes, 300}, "failedTask": {f.FailedTask, maxNameBytes, 300},
			"file": {f.File, maxFileBytes, 1000}, "command": {f.Command, maxCommandBytes, 1000}, "reason": {f.Reason, maxReasonBytes, 1000},
		} {
			kept := strings.Index(c.got, " [cut ")
			if len(c.got) > c.size || kept < 0 || !strings.HasSuffix(c.got, fmt.Sprintf(" [cut %d bytes]", c.full-kept)) {
				t.Errorf("%s = %q (%d bytes), want at most %d with an exact marker", name, c.got, len(c.got), c.size)
			}
		}
	})

	t.Run("a cut never splits a character", func(t *testing.T) {
		cmd := strings.Repeat("a", 230) + strings.Repeat("€", 100)
		f := completeSummary(FailureSummary{Kind: FailureStep, Task: "t", File: "R", Line: 1, Command: cmd, Reason: "x"}, Result{ExitCode: 1})
		if !utf8.ValidString(f.Command) || len(f.Command) > maxCommandBytes {
			t.Errorf("command = %q", f.Command)
		}
	})

	t.Run("fields the kind does not show are dropped", func(t *testing.T) {
		f := completeSummary(FailureSummary{Kind: FailureError, Task: "t", File: "R", Line: 3, Command: "c", Reason: "r"}, Result{ExitCode: 1})
		if f.File != "" || f.Line != 0 || f.Command != "" {
			t.Errorf("summary = %+v", f)
		}
		f = completeSummary(FailureSummary{Kind: FailureScript, Task: "t", File: "R", Line: 3, Command: "c", Reason: "r"}, Result{ExitCode: 1})
		if f.File != "R" || f.Line != 3 || f.Command != "" {
			t.Errorf("summary = %+v", f)
		}
	})

	t.Run("the server's own reasons and kinds", func(t *testing.T) {
		if f := completeSummary(FailureSummary{Kind: FailureCancelled, Task: "t", Reason: "anything"}, Result{ExitCode: 130}); f.Reason != cancelledReason {
			t.Errorf("cancelled reason = %q", f.Reason)
		}
		if f := completeSummary(FailureSummary{Kind: "made-up", Task: "t", Reason: "x"}, Result{ExitCode: 1}); f.Kind != FailureUnknown || f.Reason != unknownReason {
			t.Errorf("an unknown kind = %+v", f)
		}
	})

	t.Run("the whole section", func(t *testing.T) {
		huge := strings.Repeat("\x01€", 5000)
		lines := strings.Repeat(strings.Repeat("L", 400)+"\n", 20)
		f := completeSummary(FailureSummary{
			Kind: FailureStep, Task: huge, FailedTask: huge, File: huge, Line: 1 << 30,
			Command: huge, Reason: huge,
		}, Result{ExitCode: 1, Stdout: lines, Stderr: lines, FixSuggestion: "x"})
		if got := len(renderSummary(f)); got > maxSummaryBytes {
			t.Errorf("the section is %d bytes, want at most %d", got, maxSummaryBytes)
		}
	})
}

// Spec 057 FR-020: the last up to 5 lines of a kept stream, at most 1 KiB.
func TestLastLines(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"two lines", "a\nb\n", "a\nb\n"},
		{"no final line feed", "a\nb", "a\nb"},
		{"twenty lines", strings.Repeat("x\n", 15) + "1\n2\n3\n4\n5\n", "1\n2\n3\n4\n5\n"},
		{"an omission line is quoted", "[omitted 9 lines, 99 bytes]\nz\n", "[omitted 9 lines, 99 bytes]\nz\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lastLines(c.in); got != c.want {
				t.Errorf("lastLines = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("longer than 1 KiB", func(t *testing.T) {
		in := strings.Repeat(strings.Repeat("y", 399)+"\n", 5)
		got := lastLines(in)
		if len(got) > maxTailBytes || !strings.HasSuffix(in, got[strings.Index(got, "\n")+1:]) {
			t.Errorf("lastLines = %q (%d bytes)", got, len(got))
		}
		marker := got[:strings.Index(got, "\n")+1]
		if want := fmt.Sprintf("[cut %d bytes]\n", len(in)-(len(got)-len(marker))); marker != want {
			t.Errorf("marker = %q, want %q", marker, want)
		}
	})

	t.Run("a cut never splits a character", func(t *testing.T) {
		got := lastLines(strings.Repeat("😀", 600))
		if !utf8.ValidString(got) || len(got) > maxTailBytes {
			t.Errorf("lastLines = %d bytes, valid %v", len(got), utf8.ValidString(got))
		}
	})
}

// Spec 057 FR-014, FR-021, FR-024: the server drops a summary of a success,
// sets what it alone knows, and leaves the exit status alone.
func TestCapResultSummary(t *testing.T) {
	f := &FailureSummary{Kind: FailureStep, Task: "t", File: "R", Line: 1, Command: "c", Reason: "exit status 1", FixSuggestion: true}
	if got := capResult(Result{ExitCode: 0, Failure: f}, 0); got.Failure != nil {
		t.Errorf("a success keeps its summary: %+v", got.Failure)
	}
	got := capResult(Result{ExitCode: 1, Stdout: strings.Repeat("o\n", 40000), Failure: f}, MinOutputLimit)
	if got.Failure == nil || got.Failure.FixSuggestion {
		t.Fatalf("summary = %+v, want no fix suggestion: the result has none", got.Failure)
	}
	if got.Failure.StdoutTail != lastLines(got.Stdout) || got.Failure.StderrTail != "" {
		t.Errorf("tails = %q, %q; want the capped stream's", got.Failure.StdoutTail, got.Failure.StderrTail)
	}
	if !f.FixSuggestion || f.StdoutTail != "" {
		t.Errorf("the engine's summary was changed: %+v", f)
	}
	if got.ExitCode != 1 {
		t.Errorf("exit = %d", got.ExitCode)
	}
}
