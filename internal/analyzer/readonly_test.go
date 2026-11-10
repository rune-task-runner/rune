package analyzer

import (
	"fmt"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
)

// The three RUNE2018 texts from the spec 033 attribute contract.
func readonlyWithConfirm(task string) string {
	return fmt.Sprintf("[readonly] task %q cannot use [confirm]: remove one of them", task)
}

func readonlyRunsConfirm(task, target string) string {
	return fmt.Sprintf("[readonly] task %q runs %q, which uses [confirm]: remove [readonly] from %q", task, target, task)
}

func readonlyRunsUnmarked(task, target string) string {
	return fmt.Sprintf("[readonly] task %q runs %q, which is not [readonly]: add [readonly] to %q or remove it from %q", task, target, target, task)
}

// wantDiag is one expected RUNE2018: its message, the source text its span
// covers, and the line it starts on.
type wantDiag struct {
	msg  string
	span string
	line int
}

// Spec 033 FR-007, FR-008: each contradiction is one error, at the place
// that needs the fix.
func TestReadonlyRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []wantDiag
	}{
		{
			"with confirm", "[readonly, confirm(\"x?\")]\nwipe:\n    @echo w\n",
			[]wantDiag{{readonlyWithConfirm("wipe"), "readonly", 1}},
		},
		{
			"unmarked dependency", "[readonly]\nreport: build\n    @echo r\nbuild:\n    @echo b\n",
			[]wantDiag{{readonlyRunsUnmarked("report", "build"), "build", 2}},
		},
		{
			"confirm dependency", "[readonly]\nreport: wipe\n    @echo r\n[confirm]\nwipe:\n    @echo w\n",
			[]wantDiag{{readonlyRunsConfirm("report", "wipe"), "wipe", 2}},
		},
		{
			"post-hook", "[readonly]\nreport: && build\n    @echo r\nbuild:\n    @echo b\n",
			[]wantDiag{{readonlyRunsUnmarked("report", "build"), "build", 2}},
		},
		{
			"failure hook", "[readonly]\nreport: || build\n    @echo r\nbuild:\n    @echo b\n",
			[]wantDiag{{readonlyRunsUnmarked("report", "build"), "build", 2}},
		},
		{
			"chain", "[readonly]\na: b\n    @echo a\n[readonly]\nb: c\n    @echo b\nc:\n    @echo c\n",
			[]wantDiag{{readonlyRunsUnmarked("b", "c"), "c", 5}},
		},
		{
			"target with both", "[readonly]\nreport: wipe\n    @echo r\n[readonly, confirm]\nwipe:\n    @echo w\n",
			[]wantDiag{{readonlyWithConfirm("wipe"), "readonly", 4}},
		},
		{
			"target on another OS", "[readonly]\nreport: winonly\n    @echo r\n[windows]\nwinonly:\n    @echo w\n",
			[]wantDiag{{readonlyRunsUnmarked("report", "winonly"), "winonly", 2}},
		},
		{
			"reference named twice", "[readonly]\na: b b\n    @echo a\nb:\n    @echo b\n",
			[]wantDiag{{readonlyRunsUnmarked("a", "b"), "b", 2}, {readonlyRunsUnmarked("a", "b"), "b", 2}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []diag.Diagnostic
			for _, d := range analyzeDiags(t, c.src) {
				if d.Code == diag.CodeInvalidReadonly {
					got = append(got, d)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d RUNE2018 diagnostics, want %d: %v", len(got), len(c.want), got)
			}
			for i, d := range got {
				w := c.want[i]
				if d.Severity != diag.Error {
					t.Errorf("severity = %v, want error", d.Severity)
				}
				if d.Message != w.msg {
					t.Errorf("message = %q, want %q", d.Message, w.msg)
				}
				if span := c.src[d.Span.Start.Offset:d.Span.End.Offset]; span != w.span || d.Span.Start.Line != w.line {
					t.Errorf("span %q on line %d, want %q on line %d", span, d.Span.Start.Line, w.span, w.line)
				}
			}
			if len(got) == 2 && got[0].Span == got[1].Span {
				t.Errorf("both diagnostics are at %v, want one per reference", got[0].Span)
			}
		})
	}
}

// Spec 033 FR-008, FR-014: forms the rule allows.
func TestReadonlyAllowed(t *testing.T) {
	cases := map[string]string{
		"chain of readonly tasks":    "[readonly]\na: b\n    @echo a\n[readonly]\nb:\n    @echo b\n",
		"with network":               "[readonly, network]\nt:\n    @echo t\n",
		"with private":               "[readonly, private]\nt:\n    @echo t\n",
		"with context":               "[readonly, context]\nhealth:\n    @echo ok\n",
		"agent executor":             "[readonly]\nask (agent):\n    Summarize the logs.\n",
		"unmarked runs a readonly":   "build: status\n    @echo b\n[readonly]\nstatus:\n    @echo s\n",
		"unknown target is RUNE2001": "[readonly]\na: nosuch\n    @echo a\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			diags := analyzeDiags(t, src)
			if d := hasCode(diags, diag.CodeInvalidReadonly); d != nil {
				t.Errorf("unexpected RUNE2018: %s", d.Message)
			}
		})
	}
	if hasCode(analyzeDiags(t, cases["unknown target is RUNE2001"]), diag.CodeUnknownDependency) == nil {
		t.Error("an unknown target should get RUNE2001")
	}
}
