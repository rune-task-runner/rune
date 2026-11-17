//go:build !windows

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Quickstart Z at a terminal: the question shows the stored plan's digest,
// "n" runs nothing and leaves the plan, "y" applies it.
func TestPlanApplyCLI_Terminal(t *testing.T) {
	// The child inherits this process's environment, which may be an agent's.
	t.Setenv("RUNE_AGENT_RUN", "")
	runefile := strings.Replace(planApplyRunefile, `[applies("plan")]`, `[confirm("Really apply?"), applies("plan")]`, 1)
	dir := planProject(t, runefile)
	if r := run(t, dir, []string{noAgentMark}, "plan", "prod"); r.code != 0 {
		t.Fatalf("rune plan prod: exit %d, stderr %q", r.code, r.stderr)
	}
	rec := storedPlans(t, dir)[0]

	r := runWithPTY(t, dir, "n\n", "apply", "prod")
	for _, want := range []string{"Artifact: sha256:" + rec.Artifact.SHA256, "Confirmation: Really apply?", "Apply this plan once? [y/N]"} {
		if !strings.Contains(r.stdout+r.stderr, want) {
			t.Errorf("terminal output = %q, want %q", r.stdout+r.stderr, want)
		}
	}
	if r.code == 0 {
		t.Error("answering n exits 0")
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Fatal("the apply body ran after n")
	}

	r = runWithPTY(t, dir, "y\n", "apply", "prod")
	if r.code != 0 {
		t.Fatalf("answering y: exit %d, output %q", r.code, r.stdout+r.stderr)
	}
	if strings.Count(r.stdout+r.stderr, "[y/N]") != 1 {
		t.Errorf("terminal output = %q, want one question covering the task's own [confirm]", r.stdout+r.stderr)
	}
	if got := readDone(t, dir, "applied.txt"); got != "plan env=prod serial=1" {
		t.Errorf("the body read %q, want the stored plan", got)
	}
}
