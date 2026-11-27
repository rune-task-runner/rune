package analyzer

import (
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
)

// Spec 061 FR-002, FR-003: [reuse] is refused where a skip would hide an
// effect or an input no snapshot covers, at the [reuse] attribute.
func TestReuseRejected(t *testing.T) {
	plan := "[plan(file=\"tfplan\", summary=\"tfplan.txt\")]\nplan:\n    @echo p\n"
	cases := []struct {
		name string
		src  string
		want wantDiag
	}{
		{
			"with confirm", "[reuse, confirm]\nwipe:\n    @echo w\n",
			wantDiag{`[reuse] cannot be combined with [confirm] on task "wipe": its run is its effect`, "reuse", 1},
		},
		{
			"with plan", "[reuse]\n" + plan,
			wantDiag{`[reuse] cannot be combined with [plan] on task "plan": its run makes the plan`, "reuse", 1},
		},
		{
			"with applies", plan + "[reuse]\n[applies(\"plan\")]\napply:\n    @cat \"$RUNE_PLAN_FILE\"\n",
			wantDiag{`[reuse] cannot be combined with [applies] on task "apply": its run applies a plan`, "reuse", 4},
		},
		{
			"with network", "[network]\n[reuse]\nfetch:\n    @echo f\n",
			wantDiag{`[reuse] cannot be combined with [network] on task "fetch": its result depends on the network`, "reuse", 2},
		},
		{
			"agent executor", "[reuse]\nask (agent):\n    Summarize the logs.\n",
			wantDiag{`[reuse] cannot be used on agent task "ask": its result is a model's answer`, "reuse", 1},
		},
		{
			"twice", "[reuse]\n[reuse]\nt:\n    @echo t\n",
			wantDiag{`duplicate [reuse] on task "t"`, "reuse", 2},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []diag.Diagnostic
			for _, d := range analyzeDiags(t, c.src) {
				if d.Code == diag.CodeInvalidReuse {
					got = append(got, d)
				}
			}
			if len(got) != 1 {
				t.Fatalf("got %d RUNE2022 diagnostics, want 1: %v", len(got), got)
			}
			d := got[0]
			if d.Severity != diag.Error || d.Message != c.want.msg {
				t.Errorf("%v %q, want error %q", d.Severity, d.Message, c.want.msg)
			}
			if span := c.src[d.Span.Start.Offset:d.Span.End.Offset]; span != c.want.span || d.Span.Start.Line != c.want.line {
				t.Errorf("span %q on line %d, want %q on line %d", span, d.Span.Start.Line, c.want.span, c.want.line)
			}
		})
	}
	if d := hasCode(analyzeDiags(t, "[reuse]\n[reuse]\nt:\n    @echo t\n"), diag.CodeInvalidReuse); d == nil || len(d.Related) != 1 {
		t.Errorf("a duplicate should point at the first: %+v", d)
	}
}

func TestReuseAllowed(t *testing.T) {
	cases := map[string]string{
		"alone":      "[reuse]\nt:\n    @echo t\n",
		"with cache": "[reuse, cache(inputs=[\"a\"], outputs=[\"b\"])]\nt:\n    @echo t\n",
		"parallel":   "[reuse, parallel]\nt: a\n    @echo t\na:\n    @echo a\n",
		"readonly":   "[reuse, readonly]\nt:\n    @echo t\n",
		"an OS":      "[reuse, linux]\nt:\n    @echo t\n",
		"private":    "[reuse, private]\n_t:\n    @echo t\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if d := hasCode(analyzeDiags(t, src), diag.CodeInvalidReuse); d != nil {
				t.Errorf("unexpected RUNE2022: %s", d.Message)
			}
		})
	}
}
