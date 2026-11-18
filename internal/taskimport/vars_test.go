package taskimport

import (
	"maps"
	"slices"
	"testing"
)

func TestAddVars(t *testing.T) {
	src := &source{}
	good, bad := src.addVars([]*srcVar{
		{name: "late", line: 1, expr: `base + "x"`, refs: []string{"base"}},
		{name: "base", line: 2, expr: `"b"`},
		{name: "cmd", line: 3, reason: "its value runs a command"},
		{name: "uses_cmd", line: 4, expr: "cmd", refs: []string{"cmd"}},
		{name: "loop_a", line: 5, expr: "loop_b", refs: []string{"loop_b"}},
		{name: "loop_b", line: 6, expr: "loop_a", refs: []string{"loop_a"}},
		{name: "self", line: 7, expr: "self", refs: []string{"self"}},
		{name: "unknown", line: 8, expr: "nowhere", refs: []string{"nowhere"}},
		{name: "set", line: 9, expr: `"x"`},
		{name: "a.b", line: 10, expr: `"x"`},
		{name: "exported", line: 11, reason: "it is exported", global: true},
	})
	if got := slices.Sorted(maps.Keys(good)); !slices.Equal(got, []string{"base", "late"}) {
		t.Errorf("good = %q, want base and late", got)
	}
	wantBad := map[string]int{"cmd": 3, "uses_cmd": 4, "loop_a": 5, "loop_b": 6, "self": 7, "unknown": 8, "set": 9, "a.b": 10, "exported": 11}
	if !maps.Equal(bad, wantBad) {
		t.Errorf("bad = %v, want %v", bad, wantBad)
	}
	wantVars := []Var{{Name: "late", Expr: `base + "x"`}, {Name: "base", Expr: `"b"`}}
	if !slices.Equal(src.vars, wantVars) {
		t.Errorf("vars = %v, want %v in source order", src.vars, wantVars)
	}
	wantItems := []Item{
		{Line: 3, Name: "variable cmd", Note: "its value runs a command"},
		{Line: 4, Name: "variable uses_cmd", Note: `it uses "cmd", which is not a converted variable`},
		{Line: 5, Name: "variable loop_a", Note: `it uses "loop_b", which is not a converted variable`},
		{Line: 6, Name: "variable loop_b", Note: `it uses "loop_a", which is not a converted variable`},
		{Line: 7, Name: "variable self", Note: `it uses "self", which is not a converted variable`},
		{Line: 8, Name: "variable unknown", Note: `it uses "nowhere", which is not a converted variable`},
		{Line: 9, Name: "variable set", Note: "its name is not a valid Rune name"},
		{Line: 10, Name: "variable a.b", Note: "its name is not a valid Rune name"},
		{Line: 11, Name: "variable exported", Note: "it is exported", Global: true},
	}
	if !slices.Equal(src.items, wantItems) {
		t.Errorf("items = %v\nwant    %v", src.items, wantItems)
	}
}
