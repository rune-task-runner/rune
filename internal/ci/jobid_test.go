package ci

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
)

func TestSlug(t *testing.T) {
	for _, c := range []struct {
		task string
		args []string
		want string
	}{
		{"build", nil, "build"},
		{"api::build", nil, "api-build"},
		{"Build", []string{"Linux AMD64"}, "build-linux-amd64"},
		{"test", []string{"./pkg/..."}, "test-pkg"},
		{"_private", nil, "t-_private"},
		{"1st", nil, "t-1st"},
		{"t", []string{"!!!"}, "t"},
		{"x_y", []string{"a__b"}, "x_y-a__b"},
	} {
		if got := Slug(c.task, c.args); got != c.want {
			t.Errorf("Slug(%q, %q) = %q, want %q", c.task, c.args, got, c.want)
		}
	}
	if got := Slug("", nil); got != "t" {
		t.Errorf("Slug of nothing = %q, want t", got)
	}
}

func TestCut(t *testing.T) {
	if got := cut("abc-def", 4); got != "abc" {
		t.Errorf("cut = %q", got)
	}
	if got := cut("abc", 10); got != "abc" {
		t.Errorf("cut = %q", got)
	}
}

func node(name string, argv ...string) *Node {
	return &Node{Task: &ast.Task{Name: name}, Argv: argv}
}

func TestJobIDs(t *testing.T) {
	long := strings.Repeat("x", 80)
	a, b, c, d := node("api::build"), node("api-build"), node(long), node(long)
	ids := jobIDs([]*Graph{{OS: "linux", Nodes: []*Node{a, b, c, d}}})
	want := map[*Node]string{a: "api-build", b: "api-build-2", c: strings.Repeat("x", 64), d: strings.Repeat("x", 62) + "-2"}
	for n, w := range want {
		if ids[n] != w {
			t.Errorf("%s: id %q, want %q", n.Task.Name[:min(10, len(n.Task.Name))], ids[n], w)
		}
	}

	l, m := node("build"), node("build")
	ids = jobIDs([]*Graph{{OS: "linux", Nodes: []*Node{l}}, {OS: "darwin", Nodes: []*Node{m}}})
	if ids[l] != "build-linux" || ids[m] != "build-macos" {
		t.Errorf("ids = %q, %q", ids[l], ids[m])
	}
	e, f := node(long), node(long)
	ids = jobIDs([]*Graph{{OS: "linux", Nodes: []*Node{e, f}}, {OS: "darwin"}})
	for _, n := range []*Node{e, f} {
		if id := ids[n]; len(id) > maxJobID || !strings.HasSuffix(id, "-linux") {
			t.Errorf("id %q: over %d bytes or without its OS", id, maxJobID)
		}
	}
	if ids[e] == ids[f] {
		t.Error("two nodes share an ID")
	}
}

func TestLabel(t *testing.T) {
	n := node("build", "a b", "plain", "it's")
	if got := Label(n, "linux", false); got != `build "a b" plain "it's"` {
		t.Errorf("Label = %q", got)
	}
	if got := Label(node("api::test"), "darwin", true); got != "api::test (macos)" {
		t.Errorf("Label = %q", got)
	}
}

func TestShellWord(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "plain",
		"./pkg/...":   "./pkg/...",
		"a=b,c:d@e%f": "a=b,c:d@e%f",
		"-v":          "-v",
		"":            `""`,
		"a b":         `"a b"`,
		"$HOME":       `"\$HOME"`,
		"~":           `"~"`,
		"it's":        `"it's"`,
		"*":           `"*"`,
		"a\\b":        `"a\\b"`,
		"`x`":         "\"\\`x\\`\"",
		`say "hi"`:    `"say \"hi\""`,
	} {
		if got := ShellWord(in); got != want {
			t.Errorf("ShellWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunnerNames(t *testing.T) {
	if runsOn("linux") != "ubuntu-latest" || runsOn("darwin") != "macos-latest" {
		t.Error("runsOn")
	}
	if osName("darwin") != "macos" || osName("linux") != "linux" {
		t.Error("osName")
	}
}
