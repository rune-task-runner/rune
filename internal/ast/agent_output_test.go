package ast

import "testing"

// Spec 057 FR-001: a number of lines is 1 to 5 decimal digits whose value is
// from 1 to 10,000.
func TestAgentOutputLines(t *testing.T) {
	for text, want := range map[string]int{"1": 1, "50": 50, "050": 50, "10000": 10000, "00001": 1} {
		got, err := AgentOutputLines(text)
		if err != nil || got != want {
			t.Errorf("AgentOutputLines(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "0", "00000", "-1", "+5", "5k", " 5", "5 ", "1.5", "10001", "999999", "٣"} {
		if got, err := AgentOutputLines(text); err == nil {
			t.Errorf("AgentOutputLines(%q) = %d, want an error", text, got)
		}
	}
}

func TestTaskAgentOutput(t *testing.T) {
	arg := func(name, value string) *AllowPredicate { return &AllowPredicate{Name: name, Value: value} }
	cases := []struct {
		name             string
		attrs            []*Attribute
		head, tail       int
		ok, wantsSummary bool
	}{
		{"none", nil, 0, 0, false, false},
		{"tail", []*Attribute{{Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("tail", "50")}}}, 0, 50, true, false},
		{"head and tail", []*Attribute{{Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("tail", "40"), arg("head", "10")}}}, 10, 40, true, false},
		{"summary", []*Attribute{{Kind: AttrAgentSummary}}, 0, 0, false, true},
		{"both", []*Attribute{{Kind: AttrAgentSummary}, {Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("tail", "3")}}}, 0, 3, true, true},
		// The analyzer rejects these; the engine must still read no policy
		// from them rather than a partial one.
		{"no tail", []*Attribute{{Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("head", "10")}}}, 0, 0, false, false},
		{"bad value", []*Attribute{{Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("tail", "x")}}}, 0, 0, false, false},
		{"bad head", []*Attribute{{Kind: AttrAgentOutput, Allow: []*AllowPredicate{arg("tail", "5"), arg("head", "0")}}}, 0, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := &Task{Name: "test", Attributes: c.attrs}
			head, tail, ok := task.AgentOutput()
			if head != c.head || tail != c.tail || ok != c.ok {
				t.Errorf("AgentOutput() = %d, %d, %v; want %d, %d, %v", head, tail, ok, c.head, c.tail, c.ok)
			}
			if got := task.AgentSummary(); got != c.wantsSummary {
				t.Errorf("AgentSummary() = %v, want %v", got, c.wantsSummary)
			}
		})
	}
}
