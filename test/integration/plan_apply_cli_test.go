package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 051 on the command line (quickstart Z): a person who names a task
// that applies a plan gets the same checks as an agent, and one question.

func TestPlanApplyCLI_PlanThenApply(t *testing.T) {
	// The piped run below inherits this process's environment, which may be
	// an agent's.
	t.Setenv("RUNE_AGENT_RUN", "")
	dir := planProject(t, planApplyRunefile)
	r := run(t, dir, []string{noAgentMark}, "plan", "prod")
	if r.code != 0 {
		t.Fatalf("rune plan prod: exit %d, stderr %q", r.code, r.stderr)
	}
	rec := storedPlans(t, dir)[0]
	if want := "plan " + rec.ID + ": sha256:" + rec.Artifact.SHA256; !strings.Contains(r.stderr, want) || rec.Surface != "cli" {
		t.Errorf("stderr = %q, surface %q, want the plan line %q", r.stderr, rec.Surface, want)
	}

	// Without a terminal and without --yes nobody can answer.
	r = runWithStdin(t, dir, "y\n", "apply", "prod")
	want := `task "apply" was not confirmed (Apply this plan once?): stdin is not a terminal; pass --yes to approve`
	if r.code != 1 || !strings.Contains(r.stderr, want) {
		t.Errorf("piped input: exit %d, stderr %q, want exit 1 and %q", r.code, r.stderr, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Fatal("the apply body ran on piped input")
	}

	r = run(t, dir, []string{noAgentMark}, "--yes", "apply", "prod")
	if r.code != 0 {
		t.Fatalf("rune --yes apply prod: exit %d, stderr %q", r.code, r.stderr)
	}
	for _, want := range []string{
		"Plan " + rec.ID + " made by \"plan\" at ",
		"Target: apply env=\"prod\"\n",
		"Artifact: sha256:" + rec.Artifact.SHA256 + " (",
		"Summary:\n  Plan: 1 to add for prod\n",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr = %q, want %q", r.stderr, want)
		}
	}
	if got := readDone(t, dir, "applied.txt"); got != "plan env=prod serial=1" {
		t.Errorf("the body read %q, want the stored plan", got)
	}

	// The same refusals as for an agent.
	r = run(t, dir, []string{noAgentMark}, "--yes", "apply", "prod")
	if r.code == 0 || !strings.Contains(r.stderr, applyNotRun+"plan "+rec.ID+" was already applied at ") {
		t.Errorf("second apply: exit %d, stderr %q", r.code, r.stderr)
	}
	r = run(t, dir, []string{noAgentMark}, "--yes", "apply", "staging")
	if want := applyNotRun + `there is no plan for env="staging"; run "plan" with the same arguments first`; r.code == 0 || !strings.Contains(r.stderr, want) {
		t.Errorf("no plan: exit %d, stderr %q, want %q", r.code, r.stderr, want)
	}
}

func TestPlanApplyCLI_RunsNothingItShouldNot(t *testing.T) {
	t.Run("plan modes", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		for _, args := range [][]string{
			{"--dry-run", "plan", "prod"},
			{"--summary", "plan", "prod"},
			{"--dry-run", "apply", "prod"},
			{"--summary", "apply", "prod"},
		} {
			if r := run(t, dir, []string{noAgentMark}, args...); r.code != 0 {
				t.Errorf("rune %v: exit %d, stderr %q", args, r.code, r.stderr)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".rune")); err == nil {
			t.Error("a plan mode made a store")
		}
		wantDone(t, "plan modes", dir)
	})

	t.Run("more than one task", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		if r := run(t, dir, []string{noAgentMark}, "plan", "prod"); r.code != 0 {
			t.Fatalf("rune plan prod: exit %d, stderr %q", r.code, r.stderr)
		}
		for _, args := range [][]string{
			{"--yes", "apply", "prod", "apply", "staging"},
			{"--yes", "plan", "prod", "apply", "prod"},
		} {
			r := run(t, dir, []string{noAgentMark}, args...)
			if want := "a task that applies a plan must be the only task named in a command"; r.code != 2 || !strings.Contains(r.stderr, want) {
				t.Errorf("rune %v: exit %d, stderr %q, want the usage error", args, r.code, r.stderr)
			}
		}
		wantDone(t, "more than one task", dir, "plan.done")
		if got := readDone(t, dir, "plan.done"); got != "planned" {
			t.Errorf("plan.done = %q: a refused command ran the plan body", got)
		}
	})

	t.Run("an agent run", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		if r := run(t, dir, []string{noAgentMark}, "plan", "prod"); r.code != 0 {
			t.Fatalf("rune plan prod: exit %d, stderr %q", r.code, r.stderr)
		}
		want := `task "apply" applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it`
		for _, args := range [][]string{{"apply", "prod"}, {"--yes", "apply", "prod"}} {
			if r := run(t, dir, []string{"RUNE_AGENT_RUN=1"}, args...); r.code == 0 || !strings.Contains(r.stderr, want) {
				t.Errorf("rune %v in an agent run: exit %d, stderr %q", args, r.code, r.stderr)
			}
		}
		wantDone(t, "an agent run", dir, "plan.done")
	})
}

// FR-026: the server and the command line share one store, so the operator
// of a client that cannot be asked applies the plan the agent made.
func TestPlanApplyCLI_AppliesThePlanAnAgentMade(t *testing.T) {
	dir := planProject(t, planApplyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	rec := makePlan(t, cs, dir, prodArgs)
	if text, isErr := callText(t, cs, "apply", prodArgs); !isErr || !strings.Contains(text, `The operator can run "rune apply" themselves.`) {
		t.Fatalf("the agent's apply: isError=%v text=%q", isErr, text)
	}
	r := run(t, dir, []string{noAgentMark}, "--yes", "apply", "prod")
	if r.code != 0 || !strings.Contains(r.stderr, "Plan "+rec.ID+" made by") {
		t.Fatalf("rune --yes apply prod: exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readDone(t, dir, "applied.txt"); got != "plan env=prod serial=1" {
		t.Errorf("the body read %q", got)
	}
	var surfaces []string
	for _, ev := range planEvents(t, dir) {
		surfaces = append(surfaces, ev.Event+"/"+ev.Surface)
	}
	want := "plan-created/mcp approval-requested/mcp approval-not-given/mcp approval-requested/cli approval-given/cli apply-started/cli apply-finished/cli"
	if got := strings.Join(surfaces, " "); got != want {
		t.Errorf("journal = %s\nwant      %s", got, want)
	}
}

// FR-014 across processes: two commands started together claim one plan once.
func TestPlanApplyCLI_TwoProcesses(t *testing.T) {
	dir := planProject(t, planApplyRunefile)
	if r := run(t, dir, []string{noAgentMark}, "plan", "prod"); r.code != 0 {
		t.Fatalf("rune plan prod: exit %d, stderr %q", r.code, r.stderr)
	}
	codes := make([]int, 4)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			cmd := exec.Command(runeBin, "--yes", "apply", "prod")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), noAgentMark)
			if err := cmd.Run(); err != nil {
				codes[i] = 1
			}
		})
	}
	wg.Wait()
	ran := 0
	for _, code := range codes {
		if code == 0 {
			ran++
		}
	}
	if ran != 1 || readDone(t, dir, "apply.done") != "applied" {
		t.Errorf("%d commands applied and apply.done is %q, want exactly one", ran, readDone(t, dir, "apply.done"))
	}
}

// US6: a wrong declaration is a static error and the server does not start.
func TestPlanApplyCLI_InvalidDeclaration(t *testing.T) {
	dir := planProject(t, strings.Replace(planApplyRunefile, `[applies("plan")]`, `[applies("paln")]`, 1))
	r := run(t, dir, nil, "analyze")
	if r.code != 3 || !strings.Contains(r.stderr+r.stdout, "error[RUNE2020]") || !strings.Contains(r.stderr+r.stdout, `applies names unknown task "paln" (did you mean "plan"?)`) {
		t.Errorf("rune analyze: exit %d\nstdout %q\nstderr %q", r.code, r.stdout, r.stderr)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, nil)
	cmd := exec.Command(runeBin, "mcp", "--color=never")
	cmd.Dir = dir
	if cs, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil); err == nil {
		_ = cs.Close()
		t.Error("rune mcp started on a Runefile with a static error")
	}
}
