package parser

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
)

// TestParseTypedParams covers the inline `name:kind` annotation grammar
// (spec 023 FR-001): every kind, defaults after annotations, and variadics.
func TestParseTypedParams(t *testing.T) {
	src := "deploy env:enum(\"staging\",\"prod\") replicas:number=\"2\":\n" +
		"    ./deploy.sh {{env}} {{replicas}}\n" +
		"build target:path=\"./dist\" flag:boolean=\"false\" note:string=\"\":\n" +
		"    go build\n" +
		"test +packages:path:\n" +
		"    go test {{packages}}\n"
	f := mustParse(t, src)
	if len(f.Tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(f.Tasks))
	}

	deploy := f.Tasks[0]
	env := deploy.Params[0]
	if env.Constraint == nil || env.Constraint.Kind != ast.KindEnum {
		t.Fatalf("env constraint = %+v", env.Constraint)
	}
	if got := env.Constraint.Values; len(got) != 2 || got[0] != "staging" || got[1] != "prod" {
		t.Errorf("enum values = %v", got)
	}
	if !env.Constraint.Sp.IsValid() {
		t.Errorf("constraint span not populated")
	}
	replicas := deploy.Params[1]
	if replicas.Constraint == nil || replicas.Constraint.Kind != ast.KindNumber {
		t.Fatalf("replicas constraint = %+v", replicas.Constraint)
	}
	if replicas.Kind != ast.ParamDefaulted || replicas.Default == nil {
		t.Errorf("annotated param lost its default: %+v", replicas)
	}

	build := f.Tasks[1]
	wantKinds := []ast.ConstraintKind{ast.KindPath, ast.KindBoolean, ast.KindString}
	for i, want := range wantKinds {
		c := build.Params[i].Constraint
		if c == nil || c.Kind != want {
			t.Errorf("build param %d constraint = %+v, want kind %v", i, c, want)
		}
	}

	pkgs := f.Tasks[2].Params[0]
	if pkgs.Kind != ast.ParamVariadicPlus || pkgs.Constraint == nil || pkgs.Constraint.Kind != ast.KindPath {
		t.Errorf("variadic annotated param = %+v (constraint %+v)", pkgs, pkgs.Constraint)
	}
}

// TestSpacedColonStaysHeaderColon pins the disambiguation rule: an annotation
// requires the colon to be offset-adjacent on both sides, so every spaced form
// keeps its pre-023 meaning (SC-003).
func TestSpacedColonStaysHeaderColon(t *testing.T) {
	for _, src := range []string{
		"deploy env: build\n    @echo {{env}}\nbuild:\n    @echo b\n",
		"deploy env :build\n    @echo {{env}}\nbuild:\n    @echo b\n",
		"deploy env : build\n    @echo {{env}}\nbuild:\n    @echo b\n",
	} {
		f := mustParse(t, src)
		deploy := f.Tasks[0]
		if deploy.Params[0].Constraint != nil {
			t.Errorf("src %q: spaced colon parsed as annotation: %+v", src, deploy.Params[0].Constraint)
		}
		if len(deploy.Deps) != 1 || deploy.Deps[0].Name != "build" {
			t.Errorf("src %q: deps = %+v, want [build]", src, deploy.Deps)
		}
	}
}

// TestAdjacentUnknownKindParses verifies an adjacent unknown kind still parses
// (kind validity is the analyzer's job, RUNE2012) when the header colon is
// present.
func TestAdjacentUnknownKindParses(t *testing.T) {
	f := mustParse(t, "deploy env:bogus:\n    @echo {{env}}\n")
	c := f.Tasks[0].Params[0].Constraint
	if c == nil || c.KindName != "bogus" {
		t.Fatalf("constraint = %+v, want KindName bogus", c)
	}
}

// TestLegacyUnspacedHeaderFailsWithHint pins the documented narrow break
// (constitution v1.1.0 re-parse exception): a legacy unspaced header
// `deploy env:build` consumed its only colon as an annotation, so the missing
// header colon must fail loudly with the exact fix.
func TestLegacyUnspacedHeaderFailsWithHint(t *testing.T) {
	for _, src := range []string{
		"deploy env:build\n    @echo {{env}}\nbuild:\n    @echo b\n",
		// Multiple legacy dependencies: `test` re-parses as a second,
		// unannotated parameter, but the hint must still name env:build.
		"deploy env:build test\n    @echo {{env}}\nbuild:\n    @echo b\ntest:\n    @echo t\n",
	} {
		_, diags := Parse("Runefile", src)
		d := firstCode(diags, diag.CodeMalformedTaskDecl)
		if d == nil {
			t.Fatalf("src %q: expected %s, got %v", src, diag.CodeMalformedTaskDecl, diags)
		}
		for _, want := range []string{"add a space", `env: build`} {
			if !strings.Contains(d.Message, want) {
				t.Errorf("src %q: hint missing from %q (want substring %q)", src, d.Message, want)
			}
		}
	}
}

// TestMalformedConstraints covers the RUNE1006 parse errors (spec 023 FR-003
// syntax half + contracts/diagnostic-codes.md).
func TestMalformedConstraints(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"enum without value list", "deploy env:enum:\n    @echo hi\n"},
		{"empty enum", "deploy env:enum():\n    @echo hi\n"},
		{"non-string enum value", "deploy env:enum(x):\n    @echo hi\n"},
		{"value list on non-enum kind", "deploy env:number(\"1\"):\n    @echo hi\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := Parse("Runefile", tc.src)
			d := firstCode(diags, diag.CodeMalformedConstraint)
			if d == nil {
				t.Fatalf("expected %s, got %v", diag.CodeMalformedConstraint, diags)
			}
			if d.Severity != diag.Error {
				t.Errorf("severity = %v, want Error", d.Severity)
			}
			if !d.Span.IsValid() {
				t.Errorf("span not populated")
			}
		})
	}
}

// TestParserKnowsAllASTAttributes locks parseAttrItem's switch to the
// canonical ast.KnownAttributes set: every listed attribute must be
// recognized by the parser (whatever its argument shape), never rejected as
// "unknown attribute". This is the parser-side half of the sync that
// builtin_sync_test provides for the language registry.
func TestParserKnowsAllASTAttributes(t *testing.T) {
	for _, name := range ast.KnownAttributes {
		t.Run(name, func(t *testing.T) {
			_, diags := Parse("Runefile", "["+name+"]\nt:\n    @echo hi\n")
			for i := range diags {
				if strings.Contains(diags[i].Message, "unknown attribute") {
					t.Errorf("attribute %q from ast.KnownAttributes is unknown to the parser: %s",
						name, diags[i].Message)
				}
			}
		})
	}
}

// TestParseParamDoc covers the [param-doc("name","text")] attribute
// (spec 023 FR-014).
func TestParseParamDoc(t *testing.T) {
	src := "[param-doc(\"env\", \"Target environment\")]\ndeploy env:\n    @echo {{env}}\n"
	f := mustParse(t, src)
	task := f.Tasks[0]
	if got := task.ParamDoc("env"); got != "Target environment" {
		t.Errorf("ParamDoc(env) = %q", got)
	}
	if got := task.ParamDoc("other"); got != "" {
		t.Errorf("ParamDoc(other) = %q, want empty", got)
	}
	a := task.Attr(ast.AttrParamDoc)
	if a == nil || a.Str != "env" || a.Str2 != "Target environment" {
		t.Errorf("attribute = %+v", a)
	}
}

// TestParseReturns covers the [returns("...")] attribute (spec 023 FR-009).
func TestParseReturns(t *testing.T) {
	f := mustParse(t, "[returns(\"JSON array of IDs\")]\nids:\n    @echo []\n")
	if got := f.Tasks[0].Returns(); got != "JSON array of IDs" {
		t.Errorf("Returns() = %q", got)
	}
	f = mustParse(t, "plain:\n    @echo hi\n")
	if got := f.Tasks[0].Returns(); got != "" {
		t.Errorf("Returns() on unannotated task = %q", got)
	}
}
