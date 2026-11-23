package tasksuggest

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBuild14Runs(t *testing.T) {
	calls := join(
		times(12, call{cmd: "go test -run TestX ./pkg/...", desc: "Run the parser tests", session: "a"}),
		times(2, call{cmd: "go test -run TestX ./pkg/...", session: "b", outcome: Failed, exit: 1}),
		[]call{{cmd: "go test -run TestX ./pkg/...", outcome: Attempt}},
	)
	rep := build(calls)
	if rep.Commands != 14 || rep.Attempts != 1 || rep.Sessions != 2 {
		t.Errorf("header counts %d %d %d", rep.Commands, rep.Attempts, rep.Sessions)
	}
	if len(rep.Suggestions) != 1 {
		t.Fatalf("%d suggestions", len(rep.Suggestions))
	}
	s := rep.Suggestions[0]
	if s.Runs != 14 || s.Outcomes[Succeeded] != 12 || s.Outcomes[Failed] != 2 || s.Sessions() != 2 {
		t.Errorf("got %+v", s)
	}
	if s.Name != "go-test" || s.Description != "Run the parser tests" || s.Body != "go test -run TestX ./pkg/..." {
		t.Errorf("name %q description %q body %q", s.Name, s.Description, s.Body)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(s.ID) {
		t.Errorf("id %q", s.ID)
	}
}

func TestBuildIDIsStable(t *testing.T) {
	base := times(3, call{cmd: "make build"})
	id := build(base).Suggestions[0].ID
	// Commands that cannot merge with it into one task with a parameter.
	more := build(join(base, times(5, call{cmd: "npm test"}), times(2, call{cmd: "make build", cwd: "web"})))
	var found bool
	for _, s := range more.Suggestions {
		if s.Body == "make build" && s.Dir == "" {
			found = s.ID == id
		}
	}
	if !found {
		t.Error("the id changed when other records arrived")
	}
	if build(times(3, call{cmd: "make build", cwd: "web"})).Suggestions[0].ID == id {
		t.Error("another directory has the same id")
	}
}

func TestWidenClashes(t *testing.T) {
	a := &Suggestion{ID: "aaaaaaaa", Dir: "", key: "x"}
	b := &Suggestion{ID: "aaaaaaaa", Dir: "", key: "y"}
	c := &Suggestion{ID: "cccccccc", key: "z"}
	widenClashes([]*Suggestion{a, b, c})
	if len(a.ID) != 16 || len(b.ID) != 16 || a.ID == b.ID || c.ID != "cccccccc" {
		t.Errorf("ids %q %q %q", a.ID, b.ID, c.ID)
	}
}

func TestNames(t *testing.T) {
	tests := []struct {
		cmd   string
		tasks []TaskInfo
		want  string
	}{
		{"go test -run X ./...", nil, "go-test"},
		{"npm run lint", nil, "npm-run-lint"},
		{"npm run lint extra", nil, "npm-run-lint"},
		{"./bin/tool build", nil, "task"},
		{"CGO_ENABLED=0 go build ./...", nil, "go-build"},
		{"cd web && npm test", nil, "npm-test"},
		{"go test ./...", []TaskInfo{{Name: "go-test", Body: "go test -v"}}, "go-test-2"},
		{"go test ./...", []TaskInfo{{Name: "go-test"}, {Name: "go-test-2"}}, "go-test-3"},
		{"log x", nil, "log-x"},
		{"suggest", nil, "suggest-2"},
		{"agent", nil, "agent-2"},
		{"import", nil, "import-2"},
		{"go test 2>&1 | tail", nil, "go-test"},
	}
	for _, tt := range tests {
		rep := build(times(3, call{cmd: tt.cmd}), tt.tasks...)
		if len(rep.Suggestions) != 1 || rep.Suggestions[0].Name != tt.want {
			t.Errorf("%q: %+v, want %s", tt.cmd, rep.Suggestions, tt.want)
		}
	}
	// Two suggestions with the same words get distinct names in listing
	// order.
	rep := build(join(times(4, call{cmd: "go test ./a"}), times(3, call{cmd: "go test -v ./b"})))
	if rep.Suggestions[0].Name != "go-test" || rep.Suggestions[1].Name != "go-test-2" {
		t.Errorf("names %q %q", rep.Suggestions[0].Name, rep.Suggestions[1].Name)
	}
}

func TestDescriptions(t *testing.T) {
	tests := []struct {
		calls []call
		want  string
	}{
		{join(times(2, call{cmd: "make", desc: "A"}), times(1, call{cmd: "make", desc: "B"})), "A"},
		{join(times(1, call{cmd: "make", desc: "A", at: t0}), times(1, call{cmd: "make", desc: "B", at: t0.Add(time.Hour)}), times(1, call{cmd: "make"})), "B"},
		{times(3, call{cmd: "go test ./..."}), "Run go test."},
		{times(3, call{cmd: "./x"}), "Run the recorded command."},
	}
	for _, tt := range tests {
		rep := build(tt.calls)
		if got := rep.Suggestions[0].Description; got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestBuildOrder(t *testing.T) {
	rep := build(join(
		times(3, call{cmd: "go build", at: t0}),
		times(5, call{cmd: "npm test", cwd: "x"}),
		times(3, call{cmd: "cargo test", at: t0.Add(time.Hour)}),
		times(4, call{cmd: "for x in 1; do y; done"}),
		times(6, call{cmd: "while true; do y; done"}),
	))
	var bodies []string
	for _, s := range rep.Suggestions {
		bodies = append(bodies, s.Body)
	}
	if got := strings.Join(bodies, ","); got != "npm test,cargo test,go build" {
		t.Errorf("suggestions in order %s", got)
	}
	if len(rep.Reviews) != 2 || rep.Reviews[0].Group.Runs != 6 || rep.Reviews[1].Group.Runs != 4 {
		t.Errorf("reviews %+v", rep.Reviews)
	}
}

func TestBuildExclusions(t *testing.T) {
	rep := build(join(
		times(10, call{cmd: "rune test"}),
		times(10, call{cmd: "go vet ./..."}),
		times(2, call{cmd: "go fmt ./..."}),
		times(3, call{cmd: "@echo x"}),
		times(3, call{cmd: "# only a comment"}),
	), TaskInfo{Name: "vet", Body: "go vet ./..."}, TaskInfo{Name: "fmt", Body: "go fmt ./..."})
	if len(rep.Suggestions) != 0 {
		t.Errorf("suggestions %+v", rep.Suggestions)
	}
	if len(rep.Covered) != 1 || rep.Covered[0] != (Covered{Task: "vet", Runs: 10}) {
		t.Errorf("covered %+v", rep.Covered)
	}
	if len(rep.Reviews) != 1 || rep.Reviews[0].Reason != reasonFeature {
		t.Errorf("a command that starts like a body prefix, and no comment: %+v", rep.Reviews)
	}
	if rep.Commands != 28 {
		t.Errorf("%d commands recorded", rep.Commands)
	}
}

func TestBuildMinRuns(t *testing.T) {
	calls := times(2, call{cmd: "make"})
	if rep := Build(records(calls...), nil, Options{MinRuns: 2}); len(rep.Suggestions) != 1 {
		t.Error("two runs with a bar of 2")
	}
	if rep := Build(records(calls...), nil, Options{}); len(rep.Suggestions) != 0 {
		t.Error("two runs with the default bar")
	}
}

func TestBuildUnwritableDirectory(t *testing.T) {
	rep := build(times(3, call{cmd: "make", cwd: "a\x01b"}))
	if len(rep.Suggestions) != 0 || len(rep.Reviews) != 1 {
		t.Errorf("got %+v %+v", rep.Suggestions, rep.Reviews)
	}
}

func TestFind(t *testing.T) {
	rep := build(times(3, call{cmd: "make"}))
	if s := rep.Find(rep.Suggestions[0].ID); s != rep.Suggestions[0] {
		t.Error("the listed id is not found")
	}
	if rep.Find("ffffffff") != nil {
		t.Error("an unknown id is found")
	}
}
