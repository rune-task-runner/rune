package analyzer

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
)

func agentOutputDiags(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	var got []diag.Diagnostic
	for _, d := range analyzeDiags(t, src) {
		if d.Code == diag.CodeInvalidAgentOutput {
			got = append(got, d)
		}
	}
	return got
}

// Spec 057 FR-001 to FR-003: valid policies, alone or together, on any task.
func TestAgentOutputAccepted(t *testing.T) {
	cases := map[string]string{
		"tail 1":            `[agent-output(tail="1")]`,
		"tail 10000":        `[agent-output(tail="10000")]`,
		"leading zero":      `[agent-output(tail="050")]`,
		"head after tail":   `[agent-output(tail="40", head="10")]`,
		"head before tail":  `[agent-output(head="10", tail="40")]`,
		"summary alone":     `[agent-summary]`,
		"both":              "[agent-output(tail=\"50\")]\n[agent-summary]",
		"both in one list":  `[agent-summary, agent-output(tail="50")]`,
		"on a private task": "[private]\n[agent-output(tail=\"5\")]\n[agent-summary]",
		"next to confirm":   `[confirm, agent-output(tail="5"), agent-summary]`,
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			src := attrs + "\ntest:\n    @echo hi\n"
			if got := analyzeDiags(t, src); got.HasErrors() {
				t.Errorf("unexpected diagnostics: %v", got)
			}
		})
	}
}

// Spec 057 FR-004: each invalid policy is a RUNE2021 error at the part that
// needs the fix.
func TestAgentOutputRejected(t *testing.T) {
	const task = "\ntest:\n    @echo hi\n"
	badValue := func(v string) wantDiag {
		return wantDiag{`agent-output tail "` + v + `" must be a whole number of lines from 1 to 10000`, `"` + v + `"`, 1}
	}
	cases := []struct {
		name string
		src  string
		want []wantDiag
	}{
		{
			"unknown argument",
			`[agent-output(tail="5", lines="3")]` + task,
			[]wantDiag{{`unknown agent-output argument "lines" (expected head, tail)`, "lines", 1}},
		},
		{
			"argument twice",
			`[agent-output(tail="5", tail="6")]` + task,
			[]wantDiag{{`duplicate agent-output argument "tail"`, "tail", 1}},
		},
		{
			"no tail",
			`[agent-output(head="5")]` + task,
			[]wantDiag{{`agent-output(...) requires tail="<lines>"`, `agent-output(head="5")`, 1}},
		},
		{
			"bad head",
			`[agent-output(head="x", tail="5")]` + task,
			[]wantDiag{{`agent-output head "x" must be a whole number of lines from 1 to 10000`, `"x"`, 1}},
		},
		{"empty", `[agent-output(tail="")]` + task, []wantDiag{badValue("")}},
		{"zero", `[agent-output(tail="0")]` + task, []wantDiag{badValue("0")}},
		{"negative", `[agent-output(tail="-1")]` + task, []wantDiag{badValue("-1")}},
		{"plus sign", `[agent-output(tail="+5")]` + task, []wantDiag{badValue("+5")}},
		{"unit", `[agent-output(tail="5k")]` + task, []wantDiag{badValue("5k")}},
		{"space", `[agent-output(tail=" 5")]` + task, []wantDiag{badValue(" 5")}},
		{"fraction", `[agent-output(tail="1.5")]` + task, []wantDiag{badValue("1.5")}},
		{"above the maximum", `[agent-output(tail="10001")]` + task, []wantDiag{badValue("10001")}},
		{"six digits", `[agent-output(tail="999999")]` + task, []wantDiag{badValue("999999")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agentOutputDiags(t, c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %d RUNE2021 diagnostics, want %d: %v", len(got), len(c.want), got)
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
		})
	}
}

// A second attribute of either kind is an error at the second one, pointing
// back at the first.
func TestAgentOutputDuplicateAttribute(t *testing.T) {
	cases := []struct{ name, src, msg, span, related string }{
		{
			"agent-output",
			"[agent-output(tail=\"5\")]\n[agent-output(tail=\"6\")]\ntest:\n    @echo hi\n",
			`duplicate [agent-output] on task "test"`, `agent-output(tail="6")`, "first [agent-output] declared here",
		},
		{
			"agent-summary",
			"[agent-summary, agent-summary]\ntest:\n    @echo hi\n",
			`duplicate [agent-summary] on task "test"`, "agent-summary", "first [agent-summary] declared here",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agentOutputDiags(t, c.src)
			if len(got) != 1 {
				t.Fatalf("got %d RUNE2021 diagnostics, want 1: %v", len(got), got)
			}
			d := got[0]
			if d.Message != c.msg {
				t.Errorf("message = %q, want %q", d.Message, c.msg)
			}
			if span := c.src[d.Span.Start.Offset:d.Span.End.Offset]; span != c.span {
				t.Errorf("span covers %q, want %q", span, c.span)
			}
			if len(d.Related) != 1 || d.Related[0].Message != c.related || d.Related[0].Span.Start.Offset >= d.Span.Start.Offset {
				t.Errorf("related = %+v, want %q before the second attribute", d.Related, c.related)
			}
		})
	}
}
