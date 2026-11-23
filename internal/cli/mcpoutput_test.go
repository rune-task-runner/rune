package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/token"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 057: [agent-output] and [agent-summary] through the real adapter, the
// real scheduler and the real masking. The bodies use shell builtins only, so
// they run on every OS.

// linesBody prints n numbered lines to stdout, or to stderr when toStderr.
func linesBody(prefix string, n int, toStderr bool) string {
	redirect := ""
	if toStderr {
		redirect = " >&2"
	}
	return fmt.Sprintf(`    @i=1; while [ $i -le %d ]; do echo "%s $i"%s; i=$((i+1)); done`, n, prefix, redirect) + "\n"
}

func numberedOutput(prefix string, from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

// US1: each stream keeps its own last lines and omission line.
func TestAgentOutputTailThroughTheAdapter(t *testing.T) {
	a := adapterFor(t, "[agent-output(tail=\"50\")]\nnoisy:\n"+linesBody("out", 2000, false)+linesBody("err", 300, true))
	res := callTask(t, a, "noisy")

	wantOut := fmt.Sprintf("[omitted 1950 lines, %d bytes]\n", len(numberedOutput("out", 1, 1950))) + numberedOutput("out", 1951, 2000)
	if res.Stdout != wantOut {
		t.Errorf("stdout = %q", trunc(res.Stdout))
	}
	wantErr := fmt.Sprintf("[omitted 250 lines, %d bytes]\n", len(numberedOutput("err", 1, 250))) + numberedOutput("err", 251, 300)
	if res.Stderr != wantErr {
		t.Errorf("stderr = %q", trunc(res.Stderr))
	}
	want := &mcpserver.OutputReport{
		Head: 0, Tail: 50,
		Stdout: mcpserver.StreamReport{Lines: 2000, Bytes: int64(len(numberedOutput("out", 1, 2000))), KeptLines: 50, KeptBytes: int64(len(numberedOutput("out", 1951, 2000)))},
		Stderr: mcpserver.StreamReport{Lines: 300, Bytes: int64(len(numberedOutput("err", 1, 300))), KeptLines: 50, KeptBytes: int64(len(numberedOutput("err", 251, 300)))},
	}
	if res.Output == nil || *res.Output != *want {
		t.Errorf("Output = %+v, want %+v", res.Output, want)
	}
	if res.ExitCode != 0 || res.Failure != nil {
		t.Errorf("exit %d, failure %+v", res.ExitCode, res.Failure)
	}
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:150] + "..." + s[len(s)-150:]
	}
	return s
}

func TestAgentOutputShortStreamIsWhole(t *testing.T) {
	a := adapterFor(t, "[agent-output(tail=\"50\")]\nquiet:\n"+linesBody("out", 30, false))
	res := callTask(t, a, "quiet")
	if res.Stdout != numberedOutput("out", 1, 30) || res.Stderr != "" {
		t.Errorf("stdout = %q, stderr = %q", trunc(res.Stdout), res.Stderr)
	}
	if res.Output == nil || res.Output.Stdout.KeptLines != 30 || res.Output.Stdout.Lines != 30 {
		t.Errorf("Output = %+v", res.Output)
	}
}

// FR-024: the exit status of a failure survives a policy that drops most of
// the output.
func TestAgentOutputKeepsTheExitStatus(t *testing.T) {
	a := adapterFor(t, "[agent-output(tail=\"5\")]\nfails:\n"+linesBody("out", 10000, false)+"    @exit 3\n")
	res := callTask(t, a, "fails")
	if res.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", res.ExitCode)
	}
	if !strings.HasSuffix(res.Stdout, numberedOutput("out", 9996, 10000)) || !strings.HasPrefix(res.Stdout, "[omitted 9995 lines, ") {
		t.Errorf("stdout = %q", trunc(res.Stdout))
	}
}

// FR-006, FR-031: without the attribute on the called task the result is what
// it was before spec 057, whatever the tasks it reaches declare.
func TestAgentOutputOnlyTheCalledTask(t *testing.T) {
	src := "plain:\n" + linesBody("out", 100, false) +
		"wrapper: trimmed\n    @echo wrapped\n" +
		"[agent-output(tail=\"1\")]\n[agent-summary]\ntrimmed:\n" + linesBody("dep", 100, false)
	a := adapterFor(t, src)
	for _, task := range []string{"plain", "wrapper"} {
		res := callTask(t, a, task)
		if res.Output != nil || res.Failure != nil {
			t.Errorf("%s: Output %+v, Failure %+v, want neither", task, res.Output, res.Failure)
		}
		if strings.Contains(res.Stdout, "[omitted") {
			t.Errorf("%s: stdout = %q", task, trunc(res.Stdout))
		}
	}
	if got := callTask(t, a, "plain").Stdout; got != numberedOutput("out", 1, 100) {
		t.Errorf("plain: stdout = %q", trunc(got))
	}
}

// FR-026: a secret is masked before lines are kept or cut, wherever it sat.
func TestAgentOutputMasksBeforeCutting(t *testing.T) {
	const secret = "Zq7Lw9Xk2Vb5Nm8Rt4Yp6Hs3"
	cases := map[string]string{
		"on the last line":   "    @echo before\n    @echo last=$MY_API_TOKEN\n",
		"on an omitted line": "    @echo omitted=$MY_API_TOKEN\n" + linesBody("out", 5, false),
		// One line of 1,000 + 24 + 950 + 1 bytes, longer than the limit: cut
		// on the bytes as written, the kept tail would start inside the
		// secret. The cut must fall in masked text instead.
		"across the cut": "    @" + xs(1000) + "; printf '%s' \"$MY_API_TOKEN\"; " + xs(950) + "; echo\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			a := adapterFor(t, "[agent-output(tail=\"2\")]\nleak:\n"+body)
			a.outputLimit = mcpserver.MinOutputLimit
			a.baseEnv = []string{"MY_API_TOKEN=" + secret}
			a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)
			res := callTask(t, a, "leak")
			for _, s := range []string{res.Stdout, res.Stderr} {
				for i := 0; i+4 <= len(secret); i++ {
					if strings.Contains(s, secret[i:i+4]) {
						t.Fatalf("a part of the secret, %q, is in %q", secret[i:i+4], trunc(s))
					}
				}
			}
		})
	}
}

// FR-033: a person's run prints every line.
func TestAgentOutputCommandLineUnchanged(t *testing.T) {
	t.Setenv(agentRunVar, "")
	dir := t.TempDir()
	src := "[agent-output(tail=\"2\")]\n[agent-summary]\nnoisy:\n" + linesBody("out", 50, false)
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := Run(Options{Cwd: dir, Stdout: &stdout, Stderr: &bytes.Buffer{}, Quiet: true}, []string{"noisy"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != numberedOutput("out", 1, 50) {
		t.Errorf("stdout = %q", trunc(stdout.String()))
	}
}

// xs is a shell snippet that prints n x's with no line feed.
func xs(n int) string {
	return fmt.Sprintf(`i=0; while [ $i -lt %d ]; do printf 'x'; i=$((i+1)); done`, n)
}

// US2: the facts of a failed call, through the real engine.
func TestAgentSummaryThroughTheAdapter(t *testing.T) {
	t.Run("a body line", func(t *testing.T) {
		a := adapterFor(t, "[agent-summary]\ntest:\n    @echo one\n    @echo two\n    @exit 2\n")
		res := callTask(t, a, "test")
		want := mcpserver.FailureSummary{
			Kind: mcpserver.FailureStep, Task: "test", FailedTask: "test",
			File: "Runefile", Line: 5, Command: "exit 2", Reason: "exit status 2",
		}
		if res.ExitCode != 2 || res.Failure == nil || *res.Failure != want {
			t.Errorf("exit %d, Failure %+v; want %+v", res.ExitCode, res.Failure, want)
		}
	})

	t.Run("a dependency", func(t *testing.T) {
		a := adapterFor(t, "[agent-summary]\ndeploy: build\n    @echo deploy\nbuild:\n    @echo building\n    @exit 4\n")
		res := callTask(t, a, "deploy")
		if res.Failure == nil || res.Failure.Task != "deploy" || res.Failure.FailedTask != "build" || res.Failure.Line != 6 {
			t.Errorf("Failure = %+v", res.Failure)
		}
	})

	t.Run("an evaluation error", func(t *testing.T) {
		t.Setenv("RUNE_TEST_UNSET_057", "")
		if err := os.Unsetenv("RUNE_TEST_UNSET_057"); err != nil {
			t.Fatal(err)
		}
		a := adapterFor(t, "[agent-summary]\ndeploy:\n    @echo {{env(\"RUNE_TEST_UNSET_057\")}}\n")
		res := callTask(t, a, "deploy")
		if res.Failure == nil || res.Failure.Kind != mcpserver.FailureError || res.Failure.Line != 0 ||
			!strings.Contains(res.Failure.Reason, "environment variable not set: RUNE_TEST_UNSET_057") {
			t.Errorf("Failure = %+v", res.Failure)
		}
		if res.ExitCode == 0 {
			t.Error("the call succeeded")
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		a := adapterFor(t, "[agent-summary]\nlong:\n    @echo started\n")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := a.Call(ctx, "long", map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		// The kind comes from the classification that set the exit code, so
		// the two always agree.
		if res.ExitCode != ExitInterrupt || res.Failure == nil || res.Failure.Kind != mcpserver.FailureCancelled {
			t.Errorf("exit %d, Failure %+v", res.ExitCode, res.Failure)
		}
	})

	t.Run("a success", func(t *testing.T) {
		a := adapterFor(t, "[agent-summary]\nok:\n    @echo fine\n")
		if res := callTask(t, a, "ok"); res.Failure != nil || res.ExitCode != 0 {
			t.Errorf("exit %d, Failure %+v", res.ExitCode, res.Failure)
		}
	})

	t.Run("no attribute", func(t *testing.T) {
		a := adapterFor(t, "fails:\n    @exit 1\n")
		if res := callTask(t, a, "fails"); res.Failure != nil {
			t.Errorf("Failure = %+v", res.Failure)
		}
	})
}

// The kinds the real engine cannot be made to give portably, and the path
// of a file in another directory.
func TestFailureFacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	called := &ast.Task{Name: "test"}
	identity := func(s string) string { return s }
	step := &shell.ExecError{
		Task: "build", Line: "make", Code: 2,
		Span: token.Span{File: filepath.Join(root, "lib", "build.rune"), Start: token.Position{Line: 7}},
	}
	cases := []struct {
		name      string
		cancelled bool
		err       error
		want      mcpserver.FailureSummary
	}{
		{
			"a step in a module", false, fmt.Errorf("wrapped: %w", step),
			mcpserver.FailureSummary{Kind: mcpserver.FailureStep, Task: "test", FailedTask: "build", File: "lib/build.rune", Line: 7, Command: "make", Reason: "exit status 2"},
		},
		{
			"a step that did not start", false, &shell.ExecError{Task: "test", Line: "nope", Err: errors.New("not found\nmore"), Span: token.Span{File: "Runefile", Start: token.Position{Line: 3}}},
			mcpserver.FailureSummary{Kind: mcpserver.FailureStep, Task: "test", FailedTask: "test", File: "Runefile", Line: 3, Command: "nope", Reason: "not found"},
		},
		{
			"a script", false, &interp.ExecError{Task: "gen", Name: "python3", Code: 1, Span: token.Span{File: filepath.Join(root, "Runefile"), Start: token.Position{Line: 9}}},
			mcpserver.FailureSummary{Kind: mcpserver.FailureScript, Task: "test", FailedTask: "gen", File: "Runefile", Line: 9, Reason: "exit status 1"},
		},
		{
			"a file outside the project", false, &shell.ExecError{Task: "x", Line: "l", Code: 1, Span: token.Span{File: filepath.Join(filepath.Dir(root), "other.rune"), Start: token.Position{Line: 1}}},
			mcpserver.FailureSummary{Kind: mcpserver.FailureStep, Task: "test", FailedTask: "x", File: filepath.ToSlash(filepath.Join(filepath.Dir(root), "other.rune")), Line: 1, Command: "l", Reason: "exit status 1"},
		},
		{
			"cancelled in a step", true, step,
			mcpserver.FailureSummary{Kind: mcpserver.FailureCancelled, Task: "test", FailedTask: "build"},
		},
		{
			"cancelled before a step", true, context.Canceled,
			mcpserver.FailureSummary{Kind: mcpserver.FailureCancelled, Task: "test"},
		},
		{
			"a shell that did not start", false, &shell.ExecError{Task: "build", Err: errors.New("chdir /missing: no such file")},
			mcpserver.FailureSummary{Kind: mcpserver.FailureError, Task: "test", FailedTask: "build", Reason: `task "build" failed at "": chdir /missing: no such file`},
		},
		{
			"a script that did not start", false, &interp.ExecError{Task: "gen", Err: errors.New("no interpreter configured")},
			mcpserver.FailureSummary{Kind: mcpserver.FailureError, Task: "test", FailedTask: "gen", Reason: `task "gen" () failed: no interpreter configured`},
		},
		{
			"another error", false, errors.New("refused\nsecond line"),
			mcpserver.FailureSummary{Kind: mcpserver.FailureError, Task: "test", Reason: "refused"},
		},
		{
			"no error", false, nil,
			mcpserver.FailureSummary{Kind: mcpserver.FailureUnknown, Task: "test"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := failureFacts(c.cancelled, called, c.err, root, identity)
			if got == nil || *got != c.want {
				t.Errorf("failureFacts = %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// FR-026: a secret in the failed command or in a message is masked before it
// leaves the engine.
func TestAgentSummaryMasksSecrets(t *testing.T) {
	const secret = "Zq7Lw9Xk2Vb5Nm8Rt4Yp6Hs3"
	a := adapterFor(t, "[agent-summary]\nleak:\n    @echo token=$MY_API_TOKEN >&2\n    @exit 3 # {{env(\"MY_API_TOKEN\")}}\n")
	a.baseEnv = []string{"MY_API_TOKEN=" + secret}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)
	t.Setenv("MY_API_TOKEN", secret)
	text, fields, whole := structuredCall(t, structuredServer(t, a), "leak")
	if strings.Contains(whole, secret) || strings.Contains(text, secret) {
		t.Errorf("the result holds the secret: %s", whole)
	}
	if fields.ExitCode != 3 || !strings.Contains(text, "step: Runefile:4: exit 3 # ***") {
		t.Errorf("text = %q", text)
	}
	if got := failureFacts(false, a.tasks["leak"], errors.New("bad value "+secret), a.root, a.maskText); strings.Contains(got.Reason, secret) {
		t.Errorf("reason = %q", got.Reason)
	}
}

// FR-021: a || suggestion is announced in the summary and follows it.
func TestAgentSummaryWithFixSuggestion(t *testing.T) {
	a := adapterFor(t, "[agent-summary]\ntest: || hint\n    @echo running\n    @exit 1\nhint:\n    @echo re-run with -update\n")
	text, fields, _ := structuredCall(t, structuredServer(t, a), "test")
	summaryAt := strings.Index(text, "[failure summary - from rune]")
	fixAt := strings.Index(text, "[fix suggestion - from Runefile || failure hooks]\nre-run with -update")
	if summaryAt < 0 || fixAt < summaryAt || !strings.Contains(text[summaryAt:fixAt], "fix suggestion: follows, from the task's || failure hooks\n") {
		t.Errorf("text = %q", text)
	}
	if fields.FixSuggestion == nil || *fields.FixSuggestion != "re-run with -update" {
		t.Errorf("fixSuggestion = %v", fields.FixSuggestion)
	}
}

// US3: the first and the last lines through the adapter.
func TestAgentOutputHeadAndTailThroughTheAdapter(t *testing.T) {
	a := adapterFor(t, "[agent-output(head=\"10\", tail=\"40\")]\nbuild:\n"+linesBody("line", 500, false))
	res := callTask(t, a, "build")
	want := numberedOutput("line", 1, 10) +
		fmt.Sprintf("[omitted 450 lines, %d bytes]\n", len(numberedOutput("line", 11, 460))) +
		numberedOutput("line", 461, 500)
	if res.Stdout != want {
		t.Errorf("stdout = %q", trunc(res.Stdout))
	}
	if res.Output == nil || res.Output.Head != 10 || res.Output.Tail != 40 || res.Output.Stdout.KeptLines != 50 {
		t.Errorf("Output = %+v", res.Output)
	}
}

// The report of each stream describes the text it is returned with, and a
// task with no policy has none.
func TestKeptStreams(t *testing.T) {
	policy := &ast.Task{Name: "p", Attributes: []*ast.Attribute{{Kind: ast.AttrAgentOutput, Allow: []*ast.AllowPredicate{{Name: "tail", Value: "1"}}}}}
	out, errs := newStreamBuffer(0, policy), newStreamBuffer(0, policy)
	_, _ = out.Write([]byte("a\nb\n"))
	_, _ = errs.Write([]byte("e\n"))
	stdout, stderr, rep := keptStreams(policy, out, errs)
	if stdout != "[omitted 1 line, 2 bytes]\nb\n" || stderr != "e\n" {
		t.Errorf("streams = %q, %q", stdout, stderr)
	}
	if rep == nil || rep.Tail != 1 || rep.Stdout.KeptLines != 1 || rep.Stderr.KeptLines != 1 {
		t.Errorf("report = %+v", rep)
	}

	plain := &ast.Task{Name: "q"}
	out, errs = newStreamBuffer(0, plain), newStreamBuffer(0, plain)
	_, _ = out.Write([]byte("a\nb\n"))
	if stdout, stderr, rep := keptStreams(plain, out, errs); stdout != "a\nb\n" || stderr != "" || rep != nil {
		t.Errorf("keptStreams = %q, %q, %+v", stdout, stderr, rep)
	}
}
