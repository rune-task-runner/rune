package language

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/parser"
)

const typedHoverSrc = "[param-doc(\"env\", \"Target environment\")]\n" +
	"deploy env:enum(\"a\",\"b\") replicas:number=\"2\":\n" +
	"    @echo {{env}} {{replicas}}\n"

// Spec 023 (plan R8): a parameter's hover carries its type annotation and
// [param-doc] description.
func TestParamHoverShowsConstraint(t *testing.T) {
	f, diags := parser.Parse("Runefile", typedHoverSrc)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	offset := strings.Index(typedHoverSrc, "{{env}}") + 2
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok {
		t.Fatal("no hover")
	}
	for _, want := range []string{`env:enum("a","b")`, "Target environment", "parameter of task `deploy`"} {
		if !strings.Contains(md, want) {
			t.Errorf("hover %q missing %q", md, want)
		}
	}
}

// Completion Detail surfaces the annotation so the agent-facing contract is
// visible while authoring; unannotated params keep the plain detail.
func TestParamCompletionDetailShowsConstraint(t *testing.T) {
	f, diags := parser.Parse("Runefile", typedHoverSrc)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	ix := BuildIndex(f)
	items := expressionCompletions(ix, ScopeID("deploy"), "")
	byLabel := map[string]string{}
	for _, it := range items {
		if it.Kind == CompletionParameter {
			byLabel[it.Label] = it.Detail
		}
	}
	if got := byLabel["env"]; got != `parameter :enum("a","b")` {
		t.Errorf("env detail = %q", got)
	}
	if got := byLabel["replicas"]; got != "parameter :number" {
		t.Errorf("replicas detail = %q", got)
	}
}

// Spec 030 FR-018: hover on a raw parameter says what raw means, after any
// [param-doc] text.
func TestRawParamHover(t *testing.T) {
	src := "[param-doc(\"flags\", \"Extra go test flags\")]\ntest flags:raw=\"-v -race\":\n    go test {{flags}}\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	md, _, ok := Hover(f, "Runefile", strings.Index(src, "{{flags}}")+2)
	if !ok {
		t.Fatal("no hover")
	}
	if !strings.Contains(md, "flags:raw") || !strings.HasSuffix(md, "Extra go test flags\n\nSplit into shell words without quoting.") {
		t.Errorf("hover = %q", md)
	}
}
