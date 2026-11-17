package ast

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func planAttr(pairs ...string) *Attribute {
	a := &Attribute{Kind: AttrPlan}
	for i := 0; i+1 < len(pairs); i += 2 {
		a.Allow = append(a.Allow, &AllowPredicate{Name: pairs[i], Value: pairs[i+1]})
	}
	return a
}

// Spec 051 research R1: the plan arguments are those of the first [plan].
func TestTaskPlanSpec(t *testing.T) {
	if got := (&Task{Attributes: []*Attribute{{Kind: AttrConfirm}}}).PlanSpec(); got != nil {
		t.Errorf("PlanSpec() without the attribute = %+v", got)
	}
	task := &Task{Attributes: []*Attribute{
		planAttr("file", "a", "summary", "b", "tool", "tofu", "expires", "30m", "other", "x"),
		planAttr("file", "second"),
	}}
	want := PlanSpec{File: "a", Summary: "b", Tool: "tofu", Expires: "30m"}
	if got := task.PlanSpec(); got == nil || *got != want {
		t.Errorf("PlanSpec() = %+v, want %+v", got, want)
	}
}

func TestTaskAppliesPlan(t *testing.T) {
	if got := (&Task{}).AppliesPlan(); got != "" {
		t.Errorf("AppliesPlan() without the attribute = %q", got)
	}
	task := &Task{Attributes: []*Attribute{{Kind: AttrApplies, Str: "plan"}, {Kind: AttrApplies, Str: "other"}}}
	if got := task.AppliesPlan(); got != "plan" {
		t.Errorf("AppliesPlan() = %q, want plan", got)
	}
}

func TestPlanExpiry(t *testing.T) {
	valid := []struct {
		text string
		want time.Duration
	}{
		{"", time.Hour},
		{"30m", 30 * time.Minute},
		{"1m", time.Minute},
		{"168h", 7 * 24 * time.Hour},
	}
	for _, c := range valid {
		if got, err := PlanExpiry(c.text); err != nil || got != c.want {
			t.Errorf("PlanExpiry(%q) = %v, %v, want %v", c.text, got, err, c.want)
		}
	}
	for _, text := range []string{"59s", "169h", "soon", "-1h", "0"} {
		if got, err := PlanExpiry(text); err == nil {
			t.Errorf("PlanExpiry(%q) = %v, want an error", text, got)
		}
	}
}

// The rule reads the text only, so it is the same on every host.
func TestPlanPath(t *testing.T) {
	for _, p := range []string{"tfplan", "out/tfplan", "./tfplan", "a..b", "..a/b"} {
		if got := PlanPathError(p); got != "" {
			t.Errorf("PlanPathError(%q) = %q, want accepted", p, got)
		}
	}
	for _, p := range []string{"", "/tmp/x", `\tmp\x`, "C:/x", "c:x", `out\x`, "../x", "a/../x", "a/.."} {
		if got := PlanPathError(p); got == "" {
			t.Errorf("PlanPathError(%q) accepted", p)
		}
	}
}

func TestSamePlanPath(t *testing.T) {
	if !SamePlanPath("tfplan", "./tfplan") || !SamePlanPath("a/b", "a//b") {
		t.Error("equal paths reported as different")
	}
	if SamePlanPath("tfplan", "tfplan.txt") {
		t.Error("different paths reported as equal")
	}
}

func TestDumpPlan(t *testing.T) {
	f := &File{Tasks: []*Task{
		{Name: "plan", Attributes: []*Attribute{planAttr("file", "a", "summary", "b")}},
		{Name: "apply", Attributes: []*Attribute{{Kind: AttrApplies, Str: "plan"}}},
	}}
	got := Dump(f)
	for _, want := range []string{"    Attr plan(file=\"a\", summary=\"b\")\n", "    Attr applies(\"plan\")\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("dump has no %q:\n%s", want, got)
		}
	}
}

func TestKnownAttributesHasPlan(t *testing.T) {
	for _, name := range []string{AttrPlan, AttrApplies} {
		if !slices.Contains(KnownAttributes, name) {
			t.Errorf("KnownAttributes has no %q", name)
		}
	}
}
