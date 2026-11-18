package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/planstore"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/style"
)

// Spec 051 on the command line: a person who names a task that applies a
// plan gets the same checks, the plan on standard error, and one question.

// runPlanCLI runs the command-line entry in dir and returns standard error.
func runPlanCLI(t *testing.T, dir string, opts Options, args ...string) (string, error) {
	t.Helper()
	var stderr bytes.Buffer
	opts.Cwd, opts.Stdout, opts.Stderr = dir, &bytes.Buffer{}, &stderr
	err := Run(opts, args)
	return stderr.String(), err
}

func planDone(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func cliRecords(t *testing.T, dir string) []planstore.Record {
	t.Helper()
	return planRecords(t, &mcpAdapter{root: dir})
}

func cliJournal(t *testing.T, dir string) []string {
	t.Helper()
	return eventNames(planJournal(t, &mcpAdapter{root: dir}))
}

func TestCommandLinePlanThenApply(t *testing.T) {
	t.Setenv(agentRunVar, "")
	dir, _ := writeRunefile(t, planSrc)

	stderr, err := runPlanCLI(t, dir, Options{}, "plan", "prod")
	if err != nil {
		t.Fatalf("rune plan prod: %v\n%s", err, stderr)
	}
	recs := cliRecords(t, dir)
	if len(recs) != 1 || recs[0].Surface != "cli" {
		t.Fatalf("records = %+v", recs)
	}
	rec := recs[0]
	if want := "plan " + rec.ID + ": sha256:" + rec.Artifact.SHA256; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want the plan line %q", stderr, want)
	}

	stderr, err = runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod")
	if err != nil {
		t.Fatalf("rune --yes apply prod: %v\n%s", err, stderr)
	}
	want := "Plan " + rec.ID + " made by \"plan\" at " + planTime(rec.Created) + "\n" +
		"Target: apply env=\"prod\"\n" +
		"Artifact: sha256:" + rec.Artifact.SHA256 + " (14 bytes)\n" +
		"Summary:\n  Plan: 1 to add for prod\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if strings.Contains(stderr, applyQuestion) {
		t.Error("--yes was given and the question was still asked")
	}
	data, err := os.ReadFile(filepath.Join(dir, "applied.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "plan for prod" {
		t.Errorf("the body read %q, %v", data, err)
	}
	if got, want := cliJournal(t, dir), []string{"plan-created", "approval-requested", "approval-given", "apply-started", "apply-finished"}; !slices.Equal(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
	events := planJournal(t, &mcpAdapter{root: dir})
	if events[2].Reason != "--yes" || events[2].Surface != "cli" {
		t.Errorf("approval-given = %+v, want the reason --yes", events[2])
	}

	// The plan is used up for the command line as it is for an agent.
	stderr, err = runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod")
	if err == nil || !strings.Contains(err.Error(), `task "apply" was not run: plan `+rec.ID+` was already applied at `) {
		t.Errorf("second apply: %v\n%s", err, stderr)
	}
}

func TestCommandLineApplyRefusals(t *testing.T) {
	t.Setenv(agentRunVar, "")

	t.Run("no plan", func(t *testing.T) {
		dir, _ := writeRunefile(t, planSrc)
		_, err := runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod")
		want := `task "apply" was not run: there is no plan for env="prod"; run "plan" with the same arguments first`
		var failure *TaskFailure
		if err == nil || err.Error() != want || !errors.As(err, &failure) {
			t.Errorf("error = %v, want %s", err, want)
		}
		if planDone(dir, "apply.done") {
			t.Error("the body ran")
		}
	})

	t.Run("stdin is not a terminal", func(t *testing.T) {
		dir, _ := writeRunefile(t, planSrc)
		if _, err := runPlanCLI(t, dir, Options{}, "plan", "prod"); err != nil {
			t.Fatal(err)
		}
		_, err := runPlanCLI(t, dir, Options{Stdin: strings.NewReader("y\n")}, "apply", "prod")
		want := `task "apply" was not confirmed (Apply this plan once?): stdin is not a terminal; pass --yes to approve`
		if err == nil || err.Error() != want {
			t.Errorf("error = %v, want %s", err, want)
		}
		if planDone(dir, "apply.done") {
			t.Error("the body ran on piped input")
		}
		if got := cliJournal(t, dir); !slices.Equal(got, []string{"plan-created", "approval-requested", "approval-not-given"}) {
			t.Errorf("journal = %v", got)
		}
		// Nothing was used: the plan is still there for an approved call.
		if _, err := runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod"); err != nil || !planDone(dir, "apply.done") {
			t.Errorf("a later approved apply: %v", err)
		}
	})

	t.Run("no stdin declines silently", func(t *testing.T) {
		dir, _ := writeRunefile(t, planSrc)
		if _, err := runPlanCLI(t, dir, Options{}, "plan", "prod"); err != nil {
			t.Fatal(err)
		}
		_, err := runPlanCLI(t, dir, Options{}, "apply", "prod")
		var failure *TaskFailure
		if !errors.As(err, &failure) || !failure.Silent || planDone(dir, "apply.done") {
			t.Errorf("error = %v, want a silent decline", err)
		}
	})

	t.Run("an agent run", func(t *testing.T) {
		dir, _ := writeRunefile(t, planSrc)
		if _, err := runPlanCLI(t, dir, Options{}, "plan", "prod"); err != nil {
			t.Fatal(err)
		}
		t.Setenv(agentRunVar, "1")
		want := `task "apply" applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it`
		for _, yes := range []bool{false, true} {
			if _, err := runPlanCLI(t, dir, Options{Yes: yes}, "apply", "prod"); err == nil || err.Error() != want {
				t.Errorf("--yes %v: error = %v, want %s", yes, err, want)
			}
		}
		if planDone(dir, "apply.done") {
			t.Error("the body ran inside an agent run")
		}
	})

	t.Run("more than one task", func(t *testing.T) {
		dir, _ := writeRunefile(t, planSrc+"\nother:\n    @echo other > other.done\n")
		if _, err := runPlanCLI(t, dir, Options{}, "plan", "prod"); err != nil {
			t.Fatal(err)
		}
		want := "a task that applies a plan must be the only task named in a command"
		for _, args := range [][]string{
			{"apply", "prod", "apply", "staging"},
			{"plan", "prod", "apply", "prod"},
			{"apply", "prod", "other"},
		} {
			_, err := runPlanCLI(t, dir, Options{Yes: true}, args...)
			var usage *UsageError
			if !errors.As(err, &usage) || err.Error() != want {
				t.Errorf("rune %v: %v, want the usage error", args, err)
			}
		}
		if planDone(dir, "apply.done") || planDone(dir, "other.done") {
			t.Error("a task ran")
		}
		if len(cliRecords(t, dir)) != 1 {
			t.Error("the refused command stored a plan")
		}
	})

	t.Run("a variable override is another configuration", func(t *testing.T) {
		dir, _ := writeRunefile(t, "region := \"eu\"\n\n"+planSrc)
		if _, err := runPlanCLI(t, dir, Options{}, "region=eu", "plan", "prod"); err != nil {
			t.Fatal(err)
		}
		_, err := runPlanCLI(t, dir, Options{Yes: true}, "region=us", "apply", "prod")
		if err == nil || !strings.Contains(err.Error(), "no longer matches: the configuration changed since it was made") {
			t.Errorf("error = %v", err)
		}
		if _, err := runPlanCLI(t, dir, Options{Yes: true}, "region=eu", "apply", "prod"); err != nil {
			t.Errorf("the same override: %v", err)
		}
	})
}

func TestCommandLinePlanModesStoreNothing(t *testing.T) {
	t.Setenv(agentRunVar, "")
	dir, _ := writeRunefile(t, planSrc)
	for _, opts := range []Options{{DryRun: true}, {Summary: true}} {
		for _, task := range []string{"plan", "apply"} {
			if stderr, err := runPlanCLI(t, dir, opts, task, "prod"); err != nil {
				t.Errorf("%s with %+v: %v\n%s", task, opts, err, stderr)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rune")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a plan mode touched the store: %v", err)
	}
	if planDone(dir, "plan.done") || planDone(dir, "apply.done") {
		t.Error("a plan mode ran a body")
	}
}

func TestCommandLineApplyWithConfirm(t *testing.T) {
	t.Setenv(agentRunVar, "")
	src := strings.Replace(planSrc, `[applies("plan")]`, `[confirm("Really apply?"), applies("plan")]`, 1)
	dir, _ := writeRunefile(t, src)
	if _, err := runPlanCLI(t, dir, Options{}, "plan", "prod"); err != nil {
		t.Fatal(err)
	}
	stderr, err := runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod")
	if err != nil || !planDone(dir, "apply.done") {
		t.Fatalf("apply: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Confirmation: Really apply?\n") {
		t.Errorf("stderr = %q, want the confirmation message", stderr)
	}
}

// prepareApplyRoot is the step every branch above goes through. These are
// the ones a whole command cannot reach.
func TestPrepareApplyRoot(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	mustPlan(t, a, prod)
	args := eval.Args{Values: map[string]string{"env": "prod"}}
	newEngine := func(stderr *bytes.Buffer) *engine {
		return &engine{
			tasks: a.tasks, root: a.root, workDir: a.workDir, goos: a.goos, clock: a.clock, gitInfo: a.gitInfo,
			planIdent: a.planIdent, surface: surfaceCLI, theme: style.New(false, stderr),
			opts: Options{Stderr: stderr, Stdout: stderr},
		}
	}

	t.Run("a command without an apply task is left alone", func(t *testing.T) {
		var stderr bytes.Buffer
		e := newEngine(&stderr)
		if err := e.prepareApplyRoot([]scheduler.Invocation{{Task: a.tasks["plan"], Params: args}}); err != nil || e.attempt != nil || stderr.Len() != 0 {
			t.Errorf("err = %v, attempt = %v, stderr = %q", err, e.attempt, stderr.String())
		}
	})

	t.Run("an approved root holds the claim and covers its own confirm", func(t *testing.T) {
		var stderr bytes.Buffer
		e := newEngine(&stderr)
		e.opts.Yes = true
		if err := e.prepareApplyRoot([]scheduler.Invocation{{Task: a.tasks["apply"], Params: args}}); err != nil {
			t.Fatal(err)
		}
		if e.attempt == nil || e.attempt.task != "apply" || e.attempt.argsKey != args.Key() || !e.approved["apply"] {
			t.Errorf("attempt = %+v, approved = %v", e.attempt, e.approved)
		}
		if e.finishApply(0) {
			t.Error("an untouched copy is reported as tampered")
		}
		if e.attempt != nil || e.finishApply(0) {
			t.Error("the attempt outlives its finish")
		}
	})

	t.Run("a claim that is lost is the command's failure", func(t *testing.T) {
		var stderr bytes.Buffer
		e := newEngine(&stderr)
		e.opts.Yes = true
		err := e.prepareApplyRoot([]scheduler.Invocation{{Task: a.tasks["apply"], Params: args}})
		var failure *TaskFailure
		if !errors.As(err, &failure) || !strings.Contains(err.Error(), "was already applied at") {
			t.Errorf("error = %v", err)
		}
	})
}

func TestPrintPlan(t *testing.T) {
	a, _ := planAdapter(t, strings.Replace(planSrc, `[applies("plan")]`, `[confirm("Line one\nLine two"), applies("plan")]`, 1))
	rec := mustPlan(t, a, prod)
	var stderr bytes.Buffer
	e := &engine{opts: Options{Stderr: &stderr}, mask: func(s string) string { return strings.ReplaceAll(s, "add", "***") }}
	e.printPlan(&planTicket{
		rec: rec, summary: "Plan: 1 to add\n", apply: a.tasks["apply"], args: `env="prod"`,
		notes: []string{"the previous apply of this target failed at T. Check the target before approving."},
	})
	want := "Plan " + rec.ID + " made by \"plan\" at " + planTime(rec.Created) + "\n" +
		"Target: apply env=\"prod\"\n" +
		"Artifact: sha256:" + rec.Artifact.SHA256 + " (14 bytes)\n" +
		"Note: the previous apply of this target failed at T. Check the target before approving.\n" +
		"Summary:\n  Plan: 1 to ***\n" +
		"Confirmation: Line one\\nLine two\n"
	if stderr.String() != want {
		t.Errorf("printed %q\nwant    %q", stderr.String(), want)
	}

	stderr.Reset()
	plain := &engine{opts: Options{Stderr: &stderr}}
	plain.printPlan(&planTicket{rec: rec, apply: a.tasks["plan"]})
	if got := stderr.String(); !strings.Contains(got, "Target: plan\n") || !strings.Contains(got, "Summary:\n  (empty)\n") || strings.Contains(got, "Confirmation") {
		t.Errorf("printed %q", got)
	}
}

// Spec 051: no grant approves a plan, with or without [confirm], and such a
// grant is not listed as one that takes effect.
func TestGrantForApplyTask(t *testing.T) {
	src := planSrc + "\n[confirm, applies(\"plan\")]\napply-now env:\n    @echo \"$RUNE_PLAN_FILE\"\n\n[confirm]\nclean:\n    @echo clean\n"
	tasks := indexTasks(parseRunefile(t, src))
	var buf bytes.Buffer
	warnNonDestructive(&buf, style.New(false, &buf), []string{"apply", "apply-now", "clean"}, tasks)
	want := "warning: --allow-destructive apply approves no plan: task \"apply\" asks the person at the client on every call\n" +
		"warning: --allow-destructive apply-now approves no plan: task \"apply-now\" asks the person at the client on every call\n"
	if buf.String() != want {
		t.Errorf("warnings = %q, want %q", buf.String(), want)
	}
	if got := destructiveOnly([]string{"apply", "apply-now", "clean"}, tasks, "linux"); !slices.Equal(got, []string{"clean"}) {
		t.Errorf("destructiveOnly = %v, want only clean", got)
	}
}

// One project reached by two paths has one set of plans: a server started
// from the physical directory applies the plan a shell made through a link.
func TestCommandLineSameProjectThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	t.Setenv(agentRunVar, "")
	dir, _ := writeRunefile(t, planSrc)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if stderr, err := runPlanCLI(t, link, Options{}, "plan", "prod"); err != nil {
		t.Fatalf("rune plan prod through the link: %v\n%s", err, stderr)
	}
	if stderr, err := runPlanCLI(t, dir, Options{Yes: true}, "apply", "prod"); err != nil || !planDone(dir, "apply.done") {
		t.Errorf("rune --yes apply prod in the directory itself: %v\n%s", err, stderr)
	}
}

// A refused call has no result to carry a warning, so the operator's own
// standard error gets it.
func TestJournalWarningOnARefusedCall(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	var operator bytes.Buffer
	a.stderr = &operator
	if err := os.MkdirAll(filepath.Join(planDir(a), "journal.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := applyWith(a, prod, &asked{}); err == nil || !strings.Contains(err.Error(), "there is no plan") {
		t.Fatalf("apply: %v, want the refusal for no plan", err)
	}
	if got := operator.String(); !strings.HasPrefix(got, "warning: the plan journal was not written: ") || !strings.HasSuffix(got, "\n") {
		t.Errorf("the operator's standard error = %q", got)
	}
}
