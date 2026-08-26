package analyzer

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
)

// Failure hooks (|| clause, spec 022) get the same static validation as
// dependencies and && post-hooks: unknown target, arity, cycles, and
// expression resolution — plus the agent-executor rejection (RUNE2011).

func TestFailHookUnknownTarget(t *testing.T) {
	diags := analyzeDiags(t, "test: || missing\n    @echo t\n")
	if hasCode(diags, diag.CodeUnknownDependency) == nil {
		t.Fatalf("want RUNE2001 for unknown fail-hook target, got: %v", diags)
	}
}

func TestFailHookArity(t *testing.T) {
	src := "test: || (diagnose \"a\" \"b\")\n    @echo t\ndiagnose mode:\n    @echo d\n"
	diags := analyzeDiags(t, src)
	if hasCode(diags, diag.CodeWrongArgCount) == nil {
		t.Fatalf("want RUNE2005 for fail-hook arity, got: %v", diags)
	}
}

func TestFailHookCycle(t *testing.T) {
	diags := analyzeDiags(t, "a: || b\n    @echo a\nb: a\n    @echo b\n")
	if hasCode(diags, diag.CodeDependencyCycle) == nil {
		t.Fatalf("want RUNE2003 for cycle through || edge, got: %v", diags)
	}
}

func TestFailHookSelfCycle(t *testing.T) {
	diags := analyzeDiags(t, "a: || a\n    @echo a\n")
	if hasCode(diags, diag.CodeDependencyCycle) == nil {
		t.Fatalf("want RUNE2003 for || self-cycle, got: %v", diags)
	}
}

func TestFailHookArgUndefinedVariable(t *testing.T) {
	src := "test: || (diagnose nope)\n    @echo t\ndiagnose mode=\"\":\n    @echo d\n"
	diags := analyzeDiags(t, src)
	if hasCode(diags, diag.CodeUndefinedVariable) == nil {
		t.Fatalf("want RUNE2004 for undefined var in fail-hook arg, got: %v", diags)
	}
}

func TestFailHookAgentExecutorRejected(t *testing.T) {
	src := "test: || fixit\n    @echo t\nfixit (agent):\n    Fix the failing tests.\n"
	diags := analyzeDiags(t, src)
	d := hasCode(diags, diag.CodeInvalidFailureHook)
	if d == nil {
		t.Fatalf("want RUNE2011 for agent-executor fail hook, got: %v", diags)
	}
	if d.Severity != diag.Error {
		t.Errorf("RUNE2011 severity = %v, want Error", d.Severity)
	}
	if !d.Span.IsValid() {
		t.Error("RUNE2011 span not populated")
	}
}

// FR-011 is transitive: an agent-executor task reachable through the hook's
// dependency closure would still spawn an agent session during a post-mortem
// (the hook's deps run under normal semantics), so it is rejected too.
func TestFailHookAgentExecutorRejectedTransitively(t *testing.T) {
	src := "test: || diagnose\n    @echo t\n" +
		"diagnose: fixit\n    @echo d\n" +
		"fixit (agent):\n    Fix the failing tests.\n"
	diags := analyzeDiags(t, src)
	d := hasCode(diags, diag.CodeInvalidFailureHook)
	if d == nil {
		t.Fatalf("want RUNE2011 for agent task in fail-hook closure, got: %v", diags)
	}
	if !d.Span.IsValid() {
		t.Error("RUNE2011 span not populated")
	}
}

func TestFailHookPlainTargetAllowed(t *testing.T) {
	src := "test: || diagnose\n    @echo t\ndiagnose:\n    @echo d\n"
	if diags := analyzeDiags(t, src); diags.HasErrors() {
		t.Fatalf("plain fail hook should be allowed: %v", diags)
	}
}

// The [context] closure walk must cross || edges too: an unattended context
// hook whose fail hook needs [confirm] (or an agent) would doom every run.
func TestContextConfirmFailHookRejected(t *testing.T) {
	src := "[confirm]\nnotify:\n    @echo n\n[context]\nhealth: || notify\n    @echo ok\n"
	diags := analyzeDiags(t, src)
	if !diags.HasErrors() || !containsMsg(diags, `dependency "notify"`) {
		t.Fatalf("want [confirm] fail-hook rejection in [context] closure, got: %v", diags)
	}
}
