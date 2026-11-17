package analyzer

import (
	"fmt"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
)

// The RUNE2019 texts of the spec 050 attribute contract.
const onlyStringOrEnum = ": only string and enum parameters are supported"

func allowUnknown(name, task string) string {
	return fmt.Sprintf("agent-allow names unknown parameter %q of task %q", name, task)
}

func allowKind(name, kind string) string {
	return fmt.Sprintf("agent-allow cannot restrict parameter %q of type %s", name, kind) + onlyStringOrEnum
}

func allowVariadic(name string) string {
	return fmt.Sprintf("agent-allow cannot restrict variadic parameter %q", name) + onlyStringOrEnum
}

// allowTask puts attrs above a task whose body does not matter.
func allowTask(attrs, header string) string {
	return attrs + "\n" + header + ":\n    @echo hi\n"
}

// Spec 050 FR-002, FR-003: each invalid declaration is one error, at the
// part that needs the fix.
func TestAgentAllowRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []wantDiag
	}{
		{
			"a second attribute",
			allowTask("[agent-allow(env=\"staging\")]\n[agent-allow(evn=\"prod\")]", "deploy env"),
			[]wantDiag{{`duplicate [agent-allow] on task "deploy": put every predicate in one attribute`, `agent-allow(evn="prod")`, 2}},
		},
		{
			"an unknown name close to a parameter",
			allowTask(`[agent-allow(evn="staging")]`, "deploy env"),
			[]wantDiag{{allowUnknown("evn", "deploy") + ` (did you mean "env"?)`, "evn", 1}},
		},
		{
			"an unknown name close to nothing",
			allowTask(`[agent-allow(completely-else="staging")]`, "deploy env"),
			[]wantDiag{{allowUnknown("completely-else", "deploy"), "completely-else", 1}},
		},
		{
			"an unknown name on a task with no parameter",
			allowTask(`[agent-allow(env="staging")]`, "deploy"),
			[]wantDiag{{allowUnknown("env", "deploy"), "env", 1}},
		},
		{
			"a name used twice",
			allowTask(`[agent-allow(env="staging", env="prod")]`, "deploy env"),
			[]wantDiag{{`duplicate agent-allow predicate for parameter "env"`, "env", 1}},
		},
		{
			"a + variadic",
			allowTask(`[agent-allow(paths="a")]`, "lint +paths"),
			[]wantDiag{{allowVariadic("paths"), "paths", 1}},
		},
		{
			"a typed * variadic is reported as variadic",
			allowTask(`[agent-allow(paths="a")]`, "lint *paths:number"),
			[]wantDiag{{allowVariadic("paths"), "paths", 1}},
		},
		{
			"a number",
			allowTask(`[agent-allow(replicas="2")]`, "scale replicas:number"),
			[]wantDiag{{allowKind("replicas", "number"), "replicas", 1}},
		},
		{
			"a boolean",
			allowTask(`[agent-allow(force="false")]`, "push force:boolean"),
			[]wantDiag{{allowKind("force", "boolean"), "force", 1}},
		},
		{
			"a path",
			allowTask(`[agent-allow(dir="dist")]`, "pack dir:path"),
			[]wantDiag{{allowKind("dir", "path"), "dir", 1}},
		},
		{
			"a raw value",
			allowTask(`[agent-allow(flags="-v")]`, "run flags:raw"),
			[]wantDiag{{allowKind("flags", "raw"), "flags", 1}},
		},
		{
			"a value the enum does not have",
			allowTask(`[agent-allow(env="qa")]`, `deploy env:enum("staging","prod")`),
			[]wantDiag{{`agent-allow value "qa" is not an allowed value of parameter "env" (allowed values: "staging", "prod")`, `"qa"`, 1}},
		},
		{
			"a value with a NUL",
			allowTask(`[agent-allow(env="a\0b")]`, "deploy env"),
			[]wantDiag{{`agent-allow value for parameter "env" holds a character that is not printable`, `"a\0b"`, 1}},
		},
		{
			"a value with a line break",
			allowTask(`[agent-allow(env="a\nb")]`, "deploy env:string"),
			[]wantDiag{{`agent-allow value for parameter "env" holds a character that is not printable`, `"a\nb"`, 1}},
		},
		{
			"a value with a byte that is not UTF-8",
			allowTask("[agent-allow(env=\"a\xffb\")]", "deploy env"),
			[]wantDiag{{`agent-allow value for parameter "env" holds a character that is not printable`, "\"a\xffb\"", 1}},
		},
		{
			"two faults in one attribute",
			allowTask(`[agent-allow(evn="staging", replicas="2")]`, "deploy env replicas:number"),
			[]wantDiag{
				{allowUnknown("evn", "deploy") + ` (did you mean "env"?)`, "evn", 1},
				{allowKind("replicas", "number"), "replicas", 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []diag.Diagnostic
			for _, d := range analyzeDiags(t, c.src) {
				if d.Code == diag.CodeInvalidAgentAllow {
					got = append(got, d)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d RUNE2019 diagnostics, want %d: %v", len(got), len(c.want), got)
			}
			for i, d := range got {
				w := c.want[i]
				if d.Severity != diag.Error {
					t.Errorf("severity = %v, want error", d.Severity)
				}
				if d.Message != w.msg {
					t.Errorf("message = %q\nwant      %q", d.Message, w.msg)
				}
				if span := c.src[d.Span.Start.Offset:d.Span.End.Offset]; span != w.span || d.Span.Start.Line != w.line {
					t.Errorf("span %q on line %d, want %q on line %d", span, d.Span.Start.Line, w.span, w.line)
				}
			}
		})
	}

	t.Run("the first attribute is the related location of the duplicate", func(t *testing.T) {
		src := allowTask("[agent-allow(env=\"staging\")]\n[agent-allow(env=\"prod\")]", "deploy env")
		d := hasCode(analyzeDiags(t, src), diag.CodeInvalidAgentAllow)
		if d == nil || len(d.Related) != 1 || d.Related[0].Span.Start.Line != 1 {
			t.Fatalf("diagnostic = %+v, want one related location on line 1", d)
		}
	})

	t.Run("an unknown type name is RUNE2012 only", func(t *testing.T) {
		diags := analyzeDiags(t, allowTask(`[agent-allow(env="staging")]`, "deploy env:wibble"))
		if hasCode(diags, diag.CodeUnknownParamType) == nil {
			t.Error("no RUNE2012")
		}
		if d := hasCode(diags, diag.CodeInvalidAgentAllow); d != nil {
			t.Errorf("unexpected RUNE2019: %s", d.Message)
		}
	})
}

// Spec 050 FR-001, FR-002, and the assumptions: forms the rule allows.
func TestAgentAllowAccepted(t *testing.T) {
	cases := map[string]string{
		"an unannotated parameter":      allowTask(`[agent-allow(env="staging")]`, "deploy env"),
		"a string parameter":            allowTask(`[agent-allow(env="staging")]`, "deploy env:string"),
		"an enum parameter":             allowTask(`[agent-allow(env="staging")]`, `deploy env:enum("staging","prod")`),
		"two predicates":                allowTask(`[agent-allow(env="staging", region="eu-west-1")]`, `rollout env:enum("staging","prod") region="eu-west-1"`),
		"the empty string":              allowTask(`[agent-allow(env="")]`, `deploy env=""`),
		"letters outside ASCII":         allowTask(`[agent-allow(env="préprod")]`, "deploy env"),
		"a default the policy forbids":  allowTask(`[agent-allow(env="staging")]`, `publish env:enum("staging","prod")="prod"`),
		"with private":                  allowTask(`[private, agent-allow(env="staging")]`, "deploy env"),
		"with confirm":                  allowTask(`[confirm, agent-allow(env="staging")]`, "deploy env"),
		"with context":                  allowTask(`[context, agent-allow(env="staging")]`, `health env="staging"`),
		"a dependency the policy stops": allowTask(`[agent-allow(env="staging")]`, "deploy env") + "ship: (deploy \"prod\")\n    @echo s\n",
		"a hyphenated parameter":        allowTask(`[agent-allow(dry-run="yes")]`, "deploy dry-run"),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if d := hasCode(analyzeDiags(t, src), diag.CodeInvalidAgentAllow); d != nil {
				t.Errorf("unexpected RUNE2019: %s", d.Message)
			}
		})
	}
}
