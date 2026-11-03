package runtime

import (
	"slices"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
)

// The rows follow spec 029 research R3: only a known shell's -c with no code after
// it is removed; wrappers, lists with their own code, and other programs keep it.
func TestCommandWithoutStringFlag(t *testing.T) {
	tests := []struct {
		in, want []string
	}{
		{[]string{"bash", "-cu"}, []string{"bash", "-u"}},
		{[]string{"bash", "-euo", "pipefail", "-c"}, []string{"bash", "-euo", "pipefail"}},
		{[]string{"bash", "-euc"}, []string{"bash", "-eu"}},
		{[]string{"bash", "-c", "-e"}, []string{"bash", "-e"}},
		{[]string{"bash", "-c", "-o", "pipefail"}, []string{"bash", "-o", "pipefail"}},
		{[]string{"bash", "-c", "-euo", "pipefail"}, []string{"bash", "-euo", "pipefail"}},
		{[]string{"bash", "-c", "-c"}, []string{"bash"}},
		{[]string{"zsh", "-fc"}, []string{"zsh", "-f"}},
		{[]string{"sh", "-c"}, []string{"sh"}},
		{[]string{"bash", "--norc", "-c"}, []string{"bash", "--norc"}},
		{[]string{"/usr/bin/env", "bash", "-euc"}, []string{"/usr/bin/env", "bash", "-eu"}},
		{[]string{"busybox", "sh", "-c"}, []string{"busybox", "sh"}},
		{[]string{"BASH.EXE", "-cu"}, []string{"BASH.EXE", "-u"}},

		{[]string{"bash", "-o", "pipefail"}, []string{"bash", "-o", "pipefail"}},
		{[]string{"bash", "-O", "extglob"}, []string{"bash", "-O", "extglob"}},
		{[]string{"bash", "-eu"}, []string{"bash", "-eu"}},
		{[]string{"bash", "-O2"}, []string{"bash", "-O2"}},
		{[]string{"bash", "--rcfile=c"}, []string{"bash", "--rcfile=c"}},
		{[]string{"bash", "-c", "exec bash -eu $0"}, []string{"bash", "-c", "exec bash -eu $0"}},
		{[]string{"bash", "-sc"}, []string{"bash", "-sc"}},
		{[]string{"bash", "-s", "-c"}, []string{"bash", "-s", "-c"}},
		{[]string{"bash", "-c", "-es"}, []string{"bash", "-c", "-es"}},
		{[]string{"nix", "develop", "-c", "bash", "-eu"}, []string{"nix", "develop", "-c", "bash", "-eu"}},
		{[]string{"php", "-c", "/etc/php.ini"}, []string{"php", "-c", "/etc/php.ini"}},
		{[]string{"jq", "-c", "-f"}, []string{"jq", "-c", "-f"}},
		{[]string{"node", "-c"}, []string{"node", "-c"}},
		{[]string{"pwsh", "-c"}, []string{"pwsh", "-c"}},
		{[]string{"-c"}, []string{"-c"}},
	}
	for _, tt := range tests {
		in := slices.Clone(tt.in)
		got := commandWithoutStringFlag(tt.in)
		if !slices.Equal(got, tt.want) {
			t.Errorf("commandWithoutStringFlag(%q) = %q, want %q", in, got, tt.want)
		}
		// settings.Shell is shared by every task in the module.
		if !slices.Equal(tt.in, in) {
			t.Errorf("commandWithoutStringFlag changed its input: %q, was %q", tt.in, in)
		}
	}
}

func TestSelectDefaultShell(t *testing.T) {
	sel := Select(&ast.Task{Name: "x"}, config.Settings{})
	if sel.Kind != KindShell {
		t.Errorf("kind = %v, want KindShell", sel.Kind)
	}
}

func TestSelectPythonNode(t *testing.T) {
	py := Select(&ast.Task{Name: "a", Executor: "python"}, config.Settings{})
	if py.Kind != KindInterp || py.Command[0] != "python3" {
		t.Errorf("python sel = %+v", py)
	}
	nd := Select(&ast.Task{Name: "b", Executor: "node"}, config.Settings{})
	if nd.Kind != KindInterp || nd.Command[0] != "node" {
		t.Errorf("node sel = %+v", nd)
	}
}

func TestSelectPythonOverride(t *testing.T) {
	sel := Select(&ast.Task{Name: "a", Executor: "python"}, config.Settings{Python: []string{"python3.12", "-u"}})
	if sel.Command[0] != "python3.12" || len(sel.Command) != 2 {
		t.Errorf("override = %+v", sel)
	}
}

func TestSelectShellOverride(t *testing.T) {
	// `set shell := ["bash","-cu"]` switches even default bodies to temp-file exec,
	// with the -c removed so -u applies to the script (spec 029).
	sel := Select(&ast.Task{Name: "a"}, config.Settings{Shell: []string{"bash", "-cu"}})
	if sel.Kind != KindInterp || !slices.Equal(sel.Command, []string{"bash", "-u"}) || sel.Display != "shell:bash" {
		t.Errorf("shell override = %+v", sel)
	}
}

func TestSelectShellOverrideRemovesStringFlag(t *testing.T) {
	sel := Select(&ast.Task{Name: "a"}, config.Settings{Shell: []string{"bash", "-euo", "pipefail", "-c"}})
	if sel.Kind != KindInterp || !slices.Equal(sel.Command, []string{"bash", "-euo", "pipefail"}) {
		t.Errorf("shell override = %+v", sel)
	}
}

func TestSelectScriptAttributeRemovesStringFlag(t *testing.T) {
	task := &ast.Task{Name: "a", Attributes: []*ast.Attribute{{Kind: ast.AttrScript, Str: "bash -euc"}}}
	sel := Select(task, config.Settings{})
	if sel.Kind != KindInterp || !slices.Equal(sel.Command, []string{"bash", "-eu"}) || sel.Display != "bash -euc" {
		t.Errorf("script attr = %+v", sel)
	}
}

func TestSelectScriptAttribute(t *testing.T) {
	task := &ast.Task{Name: "a", Attributes: []*ast.Attribute{{Kind: ast.AttrScript, Str: "ruby -w"}}}
	sel := Select(task, config.Settings{})
	if sel.Kind != KindInterp || sel.Command[0] != "ruby" || sel.Command[1] != "-w" {
		t.Errorf("script attr = %+v", sel)
	}
}

// The -c rule covers only `set shell` and [script] commands that name a shell
// (spec 029 FR-008).
func TestSelectInterpreterOverridesKeepStringFlag(t *testing.T) {
	py := Select(&ast.Task{Name: "a", Executor: "python"}, config.Settings{Python: []string{"python3", "-c"}})
	if !slices.Equal(py.Command, []string{"python3", "-c"}) {
		t.Errorf("python override = %+v", py)
	}
	nd := Select(&ast.Task{Name: "b", Executor: "node"}, config.Settings{Node: []string{"node", "-c"}})
	if !slices.Equal(nd.Command, []string{"node", "-c"}) {
		t.Errorf("node override = %+v", nd)
	}
}

func TestSelectScriptAttributeKeepsNonShellFlag(t *testing.T) {
	for _, tt := range []struct {
		str  string
		want []string
	}{
		{"node -c", []string{"node", "-c"}},
		{"php -c /etc/php.ini", []string{"php", "-c", "/etc/php.ini"}},
	} {
		task := &ast.Task{Name: "a", Attributes: []*ast.Attribute{{Kind: ast.AttrScript, Str: tt.str}}}
		sel := Select(task, config.Settings{})
		if !slices.Equal(sel.Command, tt.want) || sel.Display != tt.str {
			t.Errorf("script(%q) = %+v, want Command %q", tt.str, sel, tt.want)
		}
	}
}

func TestSelectCustomExecutor(t *testing.T) {
	sel := Select(&ast.Task{Name: "a", Executor: "ruby"}, config.Settings{})
	if sel.Kind != KindInterp || sel.Command[0] != "ruby" {
		t.Errorf("custom = %+v", sel)
	}
}

func TestSelectAgent(t *testing.T) {
	sel := Select(&ast.Task{Name: "a", Executor: "agent"}, config.Settings{})
	if sel.Kind != KindAgent {
		t.Errorf("agent kind = %v", sel.Kind)
	}
}
