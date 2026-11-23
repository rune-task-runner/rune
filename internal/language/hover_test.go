package language

import (
	"strings"
	"testing"
)

func TestHoverTask(t *testing.T) {
	src := "# Build the project.\nbuild target=\"debug\":\n    @echo {{target}}\n"
	f, _ := resolve(t, src)
	offset := at(src, "build target") // on the task name
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok {
		t.Fatal("expected hover on task name")
	}
	for _, want := range []string{`build target="debug"`, "Build the project.", "Executor: sh"} {
		if !strings.Contains(md, want) {
			t.Errorf("hover missing %q:\n%s", want, md)
		}
	}
}

func TestHoverTaskWithGroupAndExecutor(t *testing.T) {
	src := "# Test it.\n[group(\"ci\")]\ntest (python):\n    print(1)\n"
	f, _ := resolve(t, src)
	offset := at(src, "test (python)")
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok {
		t.Fatal("expected hover")
	}
	if !strings.Contains(md, "Executor: python") || !strings.Contains(md, "Group: ci") {
		t.Errorf("hover missing executor/group:\n%s", md)
	}
}

func TestHoverParameterInterpolation(t *testing.T) {
	src := "# G.\ngreet name=\"world\":\n    @echo {{name}}\n"
	f, _ := resolve(t, src)
	offset := at(src, "{{name}}") + 2
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok || !strings.Contains(md, "parameter") {
		t.Errorf("expected parameter hover, got ok=%v md=%q", ok, md)
	}
}

func TestHoverBuiltin(t *testing.T) {
	src := "x := env(\"HOME\")\n# B.\nbuild:\n    @echo {{x}}\n"
	f, _ := resolve(t, src)
	offset := at(src, "env(")
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok || !strings.Contains(md, "env(name, default?)") {
		t.Errorf("expected builtin hover, got ok=%v md=%q", ok, md)
	}
}

func TestHoverAttribute(t *testing.T) {
	src := "# B.\n[parallel]\nbuild: a b\n    @echo hi\na:\n    @echo a\nb:\n    @echo b\n"
	f, _ := resolve(t, src)
	offset := at(src, "parallel")
	md, _, ok := Hover(f, "Runefile", offset)
	if !ok || !strings.Contains(md, "[parallel]") {
		t.Errorf("expected attribute hover, got ok=%v md=%q", ok, md)
	}
}

func TestHoverAgentOutputAttributes(t *testing.T) {
	src := "# T.\n[agent-output(tail=\"5\"), agent-summary]\ntest:\n    @echo hi\n"
	f, _ := resolve(t, src)
	for name, want := range map[string]string{
		"agent-output":  `[agent-output(head="10", tail="50")]`,
		"agent-summary": "[agent-summary]",
	} {
		md, _, ok := Hover(f, "Runefile", at(src, name))
		if !ok || !strings.Contains(md, want) {
			t.Errorf("hover on %s: ok=%v md=%q, want %q", name, ok, md, want)
		}
	}
}
