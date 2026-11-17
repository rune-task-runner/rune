package analyzer

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

// The Overview Runefile of spec 051.
const planOverview = `# Plan the infrastructure change.
[plan(file="tfplan", summary="tfplan.txt", tool="tofu")]
plan env:enum("staging","prod"):
    tofu plan -var env={{quote(env)}} -out=tfplan
    tofu show -no-color tfplan > tfplan.txt

# Apply the reviewed plan.
[applies("plan")]
apply env:enum("staging","prod"):
    tofu apply "$RUNE_PLAN_FILE"
`

// planPair is a plan task and a task that applies it, each with the given
// attribute line and header. A header may carry its own dependencies after a
// colon.
func planPair(planAttrs, planHeader, applyAttrs, applyHeader string) string {
	colon := func(header string) string {
		if strings.Contains(header, ":") && !strings.Contains(header, ":enum") && !strings.Contains(header, ":string") {
			return header
		}
		return header + ":"
	}
	return planAttrs + "\n" + colon(planHeader) + "\n    @echo plan\n" +
		applyAttrs + "\n" + colon(applyHeader) + "\n    @echo \"$RUNE_PLAN_FILE\"\n"
}

const (
	goodPlan    = `[plan(file="tfplan", summary="tfplan.txt")]`
	goodApplies = `[applies("plan")]`
)

func planDiags(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	var got []diag.Diagnostic
	for _, d := range analyzeDiags(t, src) {
		if d.Code == diag.CodeInvalidPlanBinding {
			got = append(got, d)
		}
	}
	return got
}

func TestPlanBindingAccepted(t *testing.T) {
	cases := map[string]string{
		"the overview":         planOverview,
		"defaults that differ": planPair(goodPlan, `plan env="staging"`, goodApplies, `apply env="prod"`),
		"no parameters":        planPair(goodPlan, "plan", goodApplies, "apply"),
		"all four arguments":   planPair(`[plan(expires="30m", tool="tofu", summary="out/plan.txt", file="out/tfplan")]`, "plan", goodApplies, "apply"),
		"two apply tasks":      planPair(goodPlan, "plan", goodApplies, "apply") + "[applies(\"plan\")]\napply-now:\n    @echo $RUNE_PLAN_FILE\n",
		"apply with confirm":   planPair(goodPlan, "plan", `[confirm("Sure?"), applies("plan")]`, "apply"),
		"apply with a dependency": planPair(goodPlan, "plan", goodApplies, "apply: lint") +
			"lint:\n    @echo lint\n",
		"plan run by another task": planPair(goodPlan, "plan", goodApplies, "apply") + "check: plan\n    @echo check\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if got := analyzeDiags(t, src); got.HasErrors() {
				t.Errorf("unexpected diagnostics: %v", got)
			}
		})
	}
}

// Spec 051 FR-002, FR-003: each invalid declaration is an error at the part
// that needs the fix.
func TestPlanBindingRejected(t *testing.T) {
	const relative = " must be a relative path with forward slashes inside the task's directory"
	cases := []struct {
		name string
		src  string
		want []wantDiag
	}{
		{
			"unknown argument",
			planPair(`[plan(file="a", summary="b", path="c")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`unknown plan argument "path" (expected file, summary, tool, expires)`, "path", 1}},
		},
		{
			"argument twice",
			planPair(`[plan(file="a", summary="b", file="c")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`duplicate plan argument "file"`, "file", 1}},
		},
		{
			"no summary",
			planPair(`[plan(file="a")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan(...) requires summary="<path>"`, `plan(file="a")`, 1}},
		},
		{
			"no file",
			planPair(`[plan(summary="a")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan(...) requires file="<path>"`, `plan(summary="a")`, 1}},
		},
		{
			"empty value",
			planPair(`[plan(file="", summary="b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan argument "file" is empty`, `""`, 1}},
		},
		{
			"absolute path",
			planPair(`[plan(file="/tmp/tfplan", summary="b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan file "/tmp/tfplan"` + relative, `"/tmp/tfplan"`, 1}},
		},
		{
			"path that leaves the directory",
			planPair(`[plan(file="a", summary="../b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan summary "../b"` + relative, `"../b"`, 1}},
		},
		{
			"backslash",
			planPair(`[plan(file="out\\tfplan", summary="b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan file "out\\tfplan"` + relative, `"out\\tfplan"`, 1}},
		},
		{
			"drive prefix",
			planPair(`[plan(file="C:/tfplan", summary="b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan file "C:/tfplan"` + relative, `"C:/tfplan"`, 1}},
		},
		{
			"summary is the plan file",
			planPair(`[plan(file="tfplan", summary="./tfplan")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan summary must be a different file from the plan file`, `"./tfplan"`, 1}},
		},
		{
			"tool is a path",
			planPair(`[plan(file="a", summary="b", tool="bin/tofu")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan tool "bin/tofu" must be an executable name, not a path`, `"bin/tofu"`, 1}},
		},
		{
			"expires is not a duration",
			planPair(`[plan(file="a", summary="b", expires="soon")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan expires "soon" must be a duration from 1m to 168h, such as "30m"`, `"soon"`, 1}},
		},
		{
			"expires is too short",
			planPair(`[plan(file="a", summary="b", expires="10s")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan expires "10s" must be a duration from 1m to 168h, such as "30m"`, `"10s"`, 1}},
		},
		{
			"a character that is not printable",
			planPair(`[plan(file="a\tb", summary="b")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`plan argument "file" holds a character that is not printable`, `"a\tb"`, 1}},
		},
		{
			"a second plan",
			planPair(goodPlan+"\n"+`[plan(file="x", summary="y")]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`duplicate [plan] on task "plan"`, `plan(file="x", summary="y")`, 2}},
		},
		{
			"a second applies",
			planPair(goodPlan, "plan", goodApplies+"\n"+`[applies("plan")]`, "apply"),
			[]wantDiag{{`duplicate [applies] on task "apply"`, `applies("plan")`, 5}},
		},
		{
			"both on one task",
			planPair(goodPlan, "plan", `[plan(file="a", summary="b"), applies("plan")]`, "both"),
			[]wantDiag{{`task "both" cannot both make a plan and apply one`, `applies("plan")`, 4}},
		},
		{
			"plan with cache",
			planPair(`[plan(file="a", summary="b"), cache(inputs=["x"])]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`[plan] cannot be combined with [cache] on task "plan"`, `plan(file="a", summary="b")`, 1}},
		},
		{
			"applies with readonly",
			planPair(goodPlan, "plan", `[applies("plan"), readonly]`, "apply"),
			[]wantDiag{{`[applies] cannot be combined with [readonly] on task "apply"`, `applies("plan")`, 4}},
		},
		{
			"plan with context",
			planPair(`[plan(file="a", summary="b"), context]`, "plan", goodApplies, "apply"),
			[]wantDiag{{`[plan] cannot be combined with [context] on task "plan"`, `plan(file="a", summary="b")`, 1}},
		},
		{
			"plan on an agent task",
			planPair(goodPlan, "plan (agent)", goodApplies, "apply"),
			[]wantDiag{{`[plan] cannot be used on agent task "plan"`, `plan(file="tfplan", summary="tfplan.txt")`, 1}},
		},
		{
			"applies on an agent task",
			planPair(goodPlan, "plan", goodApplies, "apply (agent)"),
			[]wantDiag{{`[applies] cannot be used on agent task "apply"`, `applies("plan")`, 4}},
		},
		{
			"unknown plan task",
			planPair(goodPlan, "plan", `[applies("paln")]`, "apply"),
			[]wantDiag{{`applies names unknown task "paln" (did you mean "plan"?)`, `applies("paln")`, 4}},
		},
		{
			"plan task without the attribute",
			"build:\n    @echo build\n[applies(\"build\")]\napply:\n    @echo $RUNE_PLAN_FILE\n",
			[]wantDiag{{`task "build" named by applies has no [plan] attribute`, `applies("build")`, 3}},
		},
		{
			"one more parameter",
			planPair(goodPlan, "plan env", goodApplies, "apply env region"),
			[]wantDiag{{`task "apply" must have the same parameters as its plan task "plan" (env)`, `applies("plan")`, 4}},
		},
		{
			"another name",
			planPair(goodPlan, "plan", goodApplies, "apply env"),
			[]wantDiag{{`task "apply" must have the same parameters as its plan task "plan" (no parameters)`, `applies("plan")`, 4}},
		},
		{
			"other enum values",
			planPair(goodPlan, `plan env:enum("staging","prod") +files`, goodApplies, `apply env:enum("staging","qa") +files`),
			[]wantDiag{{`task "apply" must have the same parameters as its plan task "plan" (env:enum("staging","prod") +files)`, `applies("plan")`, 4}},
		},
		{
			"another parameter kind",
			planPair(goodPlan, "plan *files", goodApplies, "apply +files"),
			[]wantDiag{{`task "apply" must have the same parameters as its plan task "plan" (*files)`, `applies("plan")`, 4}},
		},
		{
			"annotated against unannotated",
			planPair(goodPlan, "plan env:string", goodApplies, "apply env"),
			[]wantDiag{{`task "apply" must have the same parameters as its plan task "plan" (env:string)`, `applies("plan")`, 4}},
		},
		{
			"body without the variable",
			goodPlan + "\nplan:\n    @echo plan\n" + goodApplies + "\napply:\n    tofu apply\n",
			[]wantDiag{{`task "apply" applies a plan but its body does not use RUNE_PLAN_FILE, so it would not apply the reviewed plan`, `applies("plan")`, 4}},
		},
		{
			"apply runs its plan task",
			planPair(goodPlan, "plan", goodApplies, "apply: plan"),
			[]wantDiag{{`task "apply" must not run its plan task "plan": the plan would be made again after it was approved`, `applies("plan")`, 4}},
		},
		{
			"apply reaches its plan task",
			planPair(goodPlan, "plan", goodApplies, "apply: && prep") + "prep: plan\n    @echo prep\n",
			[]wantDiag{{`task "apply" must not run its plan task "plan": the plan would be made again after it was approved`, `applies("plan")`, 4}},
		},
		{
			"env sets the plan file",
			planPair(goodPlan, "plan", goodApplies+"\n"+`[env("RUNE_PLAN_FILE", "tfplan")]`, "apply"),
			[]wantDiag{{`task "apply" must not set RUNE_PLAN_FILE with [env]: Rune sets it to the approved plan`, `env("RUNE_PLAN_FILE", "tfplan")`, 5}},
		},
		{
			"env sets the plan digest",
			planPair(goodPlan, "plan", `[applies("plan"), env("RUNE_PLAN_DIGEST", "x")]`, "apply"),
			[]wantDiag{{`task "apply" must not set RUNE_PLAN_DIGEST with [env]: Rune sets it to the approved plan`, `env("RUNE_PLAN_DIGEST", "x")`, 4}},
		},
		{
			"a dependency names an apply task",
			planPair(goodPlan, "plan", goodApplies, "apply") + "release: apply\n    @echo release\n",
			[]wantDiag{{`task "apply" applies a plan and cannot be run by task "release": it has to be called itself`, "apply", 7}},
		},
		{
			"a post-hook names an apply task",
			planPair(goodPlan, "plan", goodApplies, "apply") + "release: && apply\n    @echo release\n",
			[]wantDiag{{`task "apply" applies a plan and cannot be run by task "release": it has to be called itself`, "apply", 7}},
		},
		{
			"a failure hook names an apply task",
			planPair(goodPlan, "plan", goodApplies, "apply") + "release: || apply\n    @echo release\n",
			[]wantDiag{{`task "apply" applies a plan and cannot be run by task "release": it has to be called itself`, "apply", 7}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planDiags(t, c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %d RUNE2020 diagnostics, want %d: %v", len(got), len(c.want), got)
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

// A duplicate attribute points back at the first one.
func TestPlanBindingDuplicateIsRelated(t *testing.T) {
	got := planDiags(t, planPair(goodPlan+"\n"+`[plan(file="x", summary="y")]`, "plan", goodApplies, "apply"))
	if len(got) != 1 || len(got[0].Related) != 1 || got[0].Related[0].Message != "first [plan] declared here" {
		t.Errorf("diagnostics = %+v", got)
	}
}

func TestParamSignature(t *testing.T) {
	parse := func(src string) *ast.File {
		t.Helper()
		f, diags := parser.Parse("Runefile", src)
		if diags.HasErrors() {
			t.Fatalf("parse: %v", diags)
		}
		return f
	}
	f := parse("t env:enum(\"a\",\"b\") region=\"eu\" n:number *rest:\n    @echo hi\nnone:\n    @echo hi\n")
	if got, want := paramSignature(f.Tasks[0]), `env:enum("a","b") region n:number *rest`; got != want {
		t.Errorf("paramSignature = %q, want %q", got, want)
	}
	if got := paramSignature(f.Tasks[1]); got != "no parameters" {
		t.Errorf("paramSignature = %q", got)
	}
	if !strings.Contains(paramSignature(parse("t +files:\n    @echo hi\n").Tasks[0]), "+files") {
		t.Error("no + before a one-or-more parameter")
	}
}
