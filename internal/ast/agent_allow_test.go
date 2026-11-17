package ast

import (
	"slices"
	"strings"
	"testing"
)

// Spec 050 research R1: the policy is the first [agent-allow] of a task. A
// second one is the analyzer's error, and must not widen the first.
func TestTaskAgentPolicy(t *testing.T) {
	first := []*AllowPredicate{{Name: "env", Value: "staging"}}
	second := []*AllowPredicate{{Name: "env", Value: "prod"}}
	cases := []struct {
		name string
		task *Task
		want []*AllowPredicate
	}{
		{"no attribute", &Task{Attributes: []*Attribute{{Kind: AttrConfirm}}}, nil},
		{"one", &Task{Attributes: []*Attribute{{Kind: AttrAgentAllow, Allow: first}}}, first},
		{"two", &Task{Attributes: []*Attribute{{Kind: AttrAgentAllow, Allow: first}, {Kind: AttrAgentAllow, Allow: second}}}, first},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.task.AgentPolicy(); !slices.Equal(got, c.want) {
				t.Errorf("AgentPolicy() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDumpAgentAllow(t *testing.T) {
	f := &File{Tasks: []*Task{{
		Name: "rollout",
		Attributes: []*Attribute{{Kind: AttrAgentAllow, Allow: []*AllowPredicate{
			{Name: "env", Value: "staging"},
			{Name: "region", Value: "eu-west-1"},
		}}},
	}}}
	want := "    Attr agent-allow(env=\"staging\", region=\"eu-west-1\")\n"
	if got := Dump(f); !strings.Contains(got, want) {
		t.Errorf("dump has no %q:\n%s", want, got)
	}
}

// The formatter runs without the analyzer, so FormatAllow writes every value
// in a form the lexer reads back unchanged, also one RUNE2019 rejects.
func TestFormatAllowQuoting(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"plain", "staging", `"staging"`},
		{"empty", "", `""`},
		{"quote and backslash", `a"b\c`, `"a\"b\\c"`},
		{"escapes the lexer reads", "a\nb\tc\rd\x00e", `"a\nb\tc\rd\0e"`},
		{"not printable", "staging\u00a0", "\"staging\u00a0\""},
		{"not UTF-8", "a\xffb", "\"a\xffb\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatAllow([]*AllowPredicate{{Name: "env", Value: c.value}})
			if want := "env=" + c.want; got != want {
				t.Errorf("FormatAllow = %q, want %q", got, want)
			}
		})
	}
}

func TestKnownAttributesHasAgentAllow(t *testing.T) {
	if !slices.Contains(KnownAttributes, AttrAgentAllow) {
		t.Errorf("KnownAttributes has no %q", AttrAgentAllow)
	}
}
