package tasksuggest

import (
	"strings"
	"testing"
	"time"
)

func build(calls []call, tasks ...TaskInfo) *Report {
	return Build(records(calls...), tasks, Options{})
}

func TestFamily(t *testing.T) {
	rep := build(join(
		times(5, call{cmd: "go test -run TestX ./pkg/..."}),
		times(3, call{cmd: "go test -run TestY ./pkg/..."}),
		times(1, call{cmd: "go test -run TestZ ./pkg/..."}),
	))
	if len(rep.Suggestions) != 1 {
		t.Fatalf("%d suggestions", len(rep.Suggestions))
	}
	s := rep.Suggestions[0]
	if s.Runs != 9 || s.Param == nil || s.Param.Name != "run" || s.Param.Default != "TestX" {
		t.Fatalf("got %+v %+v", s, s.Param)
	}
	if s.Body != "go test -run {{quote(run)}} ./pkg/..." || s.Name != "go-test" {
		t.Errorf("body %q, name %q", s.Body, s.Name)
	}
	if len(s.Param.Values) != 3 || s.Param.Values[0].Value != "TestX" || s.Param.Values[2].Runs != 1 {
		t.Errorf("values %+v", s.Param.Values)
	}
	// SC-007: the body with the default in place of the parameter is the
	// recorded command, word for word.
	f, err := parse(strings.Replace(s.Body, "{{quote(run)}}", "TestX", 1))
	if err != nil || printNode(f) != "go test -run TestX ./pkg/..." {
		t.Errorf("body read back: %v", err)
	}
}

func TestFamilyChoices(t *testing.T) {
	t.Run("a tie picks the latest", func(t *testing.T) {
		rep := build(join(
			times(2, call{cmd: "make a", at: t0}),
			times(2, call{cmd: "make b", at: t0.Add(time.Hour)}),
		))
		if p := rep.Suggestions[0].Param; p == nil || p.Default != "b" || p.Name != "value" {
			t.Errorf("param %+v", p)
		}
	})
	t.Run("the value of --flag=", func(t *testing.T) {
		rep := build(join(times(3, call{cmd: "go test --run=TestA ./..."}), times(2, call{cmd: "go test --run=TestB ./..."})))
		s := rep.Suggestions[0]
		if s.Param == nil || s.Param.Name != "run" || s.Body != "go test --run={{quote(run)}} ./..." || s.Param.Values[0].Value != "TestA" {
			t.Errorf("got %q %+v", s.Body, s.Param)
		}
	})
	t.Run("a flag that is no name", func(t *testing.T) {
		for cmd, want := range map[string]string{
			"x --Build-Mode %s": "build_mode",
			"x -9 %s":           "value",
			"x --if %s":         "value",
			"x --a.b %s":        "value",
		} {
			rep := build(join(times(2, call{cmd: strings.Replace(cmd, "%s", "a", 1)}), times(2, call{cmd: strings.Replace(cmd, "%s", "b", 1)})))
			if len(rep.Suggestions) != 1 || rep.Suggestions[0].Param == nil || rep.Suggestions[0].Param.Name != want {
				t.Errorf("%s: %+v", cmd, rep.Suggestions)
			}
		}
	})
}

func TestNoFamily(t *testing.T) {
	tests := map[string][2]call{
		"the first word":         {{cmd: "go test"}, {cmd: "npm test"}},
		"two words":              {{cmd: "go test -run A ./a"}, {cmd: "go test -run B ./b"}},
		"a quoted value":         {{cmd: `go test -run "A" ./...`}, {cmd: `go test -run "B" ./...`}},
		"a value with $":         {{cmd: "echo $A"}, {cmd: "echo $B"}},
		"a value with *":         {{cmd: "ls a*"}, {cmd: "ls b*"}},
		"a value that is a flag": {{cmd: "ls -a"}, {cmd: "ls -b"}},
		"a long value":           {{cmd: "echo " + strings.Repeat("a", 257)}, {cmd: "echo " + strings.Repeat("b", 257)}},
		"another directory":      {{cmd: "make a"}, {cmd: "make b", cwd: "web"}},
		"other assignments":      {{cmd: "X=1 make a"}, {cmd: "X=2 make b"}},
		"a pipeline":             {{cmd: "make a | tail"}, {cmd: "make b | tail"}},
		"other flags":            {{cmd: "x --a=1"}, {cmd: "x --b=2"}},
	}
	for name, pair := range tests {
		t.Run(name, func(t *testing.T) {
			rep := build(join(times(3, pair[0]), times(3, pair[1])))
			for _, s := range rep.Suggestions {
				if s.Param != nil {
					t.Errorf("merged into %q", s.Body)
				}
			}
		})
	}
	t.Run("one value alone", func(t *testing.T) {
		rep := build(times(4, call{cmd: "go test -run A ./..."}))
		if len(rep.Suggestions) != 1 || rep.Suggestions[0].Param != nil {
			t.Errorf("got %+v", rep.Suggestions)
		}
	})
	t.Run("below the threshold together", func(t *testing.T) {
		rep := build([]call{{cmd: "make a", outcome: Succeeded}, {cmd: "make b", outcome: Succeeded}})
		if len(rep.Suggestions) != 0 {
			t.Errorf("got %+v", rep.Suggestions)
		}
	})
}

// A group joins one family; merged groups are not listed alone.
func TestFamilyMembership(t *testing.T) {
	rep := build(join(
		times(5, call{cmd: "tool a x"}),
		times(4, call{cmd: "tool b x"}),
		times(3, call{cmd: "tool a y"}),
	))
	var families, alone int
	for _, s := range rep.Suggestions {
		if s.Param != nil {
			families++
			if s.Runs != 9 || s.Param.Default != "a" {
				t.Errorf("family %q runs %d default %q", s.Body, s.Runs, s.Param.Default)
			}
		} else {
			alone++
			if s.Groups[0].Text != "tool a y" {
				t.Errorf("alone: %q", s.Groups[0].Text)
			}
		}
	}
	if families != 1 || alone != 1 {
		t.Errorf("%d families, %d alone", families, alone)
	}
}

func TestFamilyValue(t *testing.T) {
	tests := []struct {
		lit, prefix, value string
		ok                 bool
	}{
		{"TestX", "", "TestX", true},
		{"--run=TestX", "--run=", "TestX", true},
		{"--run", "", "", false},
		{"--run=-x", "--run=", "-x", false},
		{"", "", "", false},
		{"a b", "", "a b", false},
	}
	for _, tt := range tests {
		prefix, value, ok := familyValue(tt.lit)
		if prefix != tt.prefix || value != tt.value || ok != tt.ok {
			t.Errorf("familyValue(%q) = %q %q %v", tt.lit, prefix, value, ok)
		}
	}
}

func TestFamilyWithPlaceholderText(t *testing.T) {
	rep := build(join(times(2, call{cmd: "echo RUNEPARAMPLACEHOLDER a"}), times(2, call{cmd: "echo RUNEPARAMPLACEHOLDER b"})))
	for _, s := range rep.Suggestions {
		if s.Param != nil {
			t.Errorf("a family was made of a command holding the placeholder: %q", s.Body)
		}
	}
}
