package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Spec 029: a `set shell` list or [script] command whose shell has -c and no code
// of its own runs the body with the list's other flags (issue #34).

// requireBash skips tests that run bodies under a real bash.
func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash on Windows CI may be the WSL launcher or Git Bash (spec 029 research R7)")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
}

const shellOverrideRepro = `set shell := ["bash", "-euo", "pipefail", "-c"]

# Should stop at false.
t:
    @echo one
    @false
    @echo two
`

func TestShellOverride_IssueRepro(t *testing.T) {
	requireBash(t)
	r := run(t, writeRunefile(t, shellOverrideRepro), nil, "--color=never", "t")
	if r.stdout != "one\n" || r.code != 1 {
		t.Errorf("stdout = %q, exit = %d; want %q, 1", r.stdout, r.code, "one\n")
	}
	if want := `task "t" (bash) failed: exit status 1`; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", r.stderr, want)
	}
}

func TestShellOverride_UnsetVariable(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-cu\"]\n\nt:\n    @echo \"x=$RUNE_029_UNSET\"\n    @echo after\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.code == 0 || !strings.Contains(r.stderr, "unbound variable") || strings.Contains(r.stdout, "after") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want an unbound variable failure", r.code, r.stdout, r.stderr)
	}
}

func TestShellOverride_FlagInCluster(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-euc\"]\n\nt:\n    @echo a\n    @(exit 3)\n    @echo b\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.stdout != "a\n" || r.code != 3 {
		t.Errorf("stdout = %q, exit = %d; want %q, 3", r.stdout, r.code, "a\n")
	}
}

func TestShellOverride_Pipefail(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-eo\", \"pipefail\", \"-c\"]\n\nt:\n    @false | true\n    @echo after\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.code != 1 || strings.Contains(r.stdout, "after") {
		t.Errorf("stdout = %q, exit = %d; want the failing pipeline to stop the task with 1", r.stdout, r.code)
	}
}

func TestShellOverride_ScriptAttribute(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "[script(\"bash -euc\")]\nt:\n    echo a\n    false\n    echo b\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.stdout != "a\n" || r.code != 1 {
		t.Errorf("stdout = %q, exit = %d; want %q, 1", r.stdout, r.code, "a\n")
	}
}

func TestShellOverride_Stdin(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-euc\"]\n\nt:\n    @read -r line\n    @echo \"got=$line\"\n")
	r := runWithStdin(t, dir, "hello\n", "--color=never", "t")
	if r.stdout != "got=hello\n" || r.code != 0 {
		t.Errorf("stdout = %q, exit = %d, stderr = %q; want %q, 0", r.stdout, r.code, r.stderr, "got=hello\n")
	}
}

func TestShellOverride_FailureHook(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-euc\"]\n\n"+
		"t: || diagnose\n    @echo a\n    @(exit 3)\n    @echo b\n\n"+
		"diagnose:\n    @echo \"code=$RUNE_FAILED_EXIT_CODE\"\n")
	r := run(t, dir, nil, "--color=never", "t")
	if !strings.Contains(r.stdout, "code=3") || strings.Contains(r.stdout, "\nb\n") || r.code != 3 {
		t.Errorf("stdout = %q, exit = %d; want the hook to see code=3 and rune to exit 3", r.stdout, r.code)
	}
}

func TestShellOverride_MCP(t *testing.T) {
	requireBash(t)
	result, _ := mcpCall(t, writeRunefile(t, shellOverrideRepro), "t")
	for _, want := range []string{"[exit 1]", `\"isError\":true`} {
		// The tool result arrives JSON-encoded inside the JSON-RPC frame.
		if !strings.Contains(result, want) && !strings.Contains(result, strings.ReplaceAll(want, `\"`, `"`)) {
			t.Errorf("tool result missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "two") {
		t.Errorf("tool result shows a line after the failure:\n%s", result)
	}
}

// Adding -c to a shell list must not change what a task does (spec 029 SC-002).
// stderr is not compared: bash's own messages name the random script path.
func TestShellOverride_EquivalenceMatrix(t *testing.T) {
	requireBash(t)
	pairs := []struct{ with, without string }{
		{`["bash", "-c"]`, `["bash"]`},
		{`["bash", "-cu"]`, `["bash", "-u"]`},
		{`["bash", "-euc"]`, `["bash", "-eu"]`},
		{`["bash", "-euo", "pipefail", "-c"]`, `["bash", "-euo", "pipefail"]`},
		{`["bash", "-c", "-eu"]`, `["bash", "-eu"]`},
	}
	bodies := []struct{ name, body string }{
		{"all-succeed", "    @echo a\n    @echo b\n"},
		{"middle-fails", "    @echo a\n    @false\n    @echo b\n"},
		{"unset-variable", "    @echo \"x=$RUNE_029_UNSET\"\n    @echo b\n"},
		{"failing-pipeline", "    @false | true\n    @echo b\n"},
	}
	for i, p := range pairs {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			for _, b := range bodies {
				t.Run(b.name, func(t *testing.T) {
					with := run(t, writeRunefile(t, "set shell := "+p.with+"\n\nt:\n"+b.body), nil, "--color=never", "t")
					without := run(t, writeRunefile(t, "set shell := "+p.without+"\n\nt:\n"+b.body), nil, "--color=never", "t")
					if with.stdout != without.stdout || with.code != without.code {
						t.Errorf("%s: stdout %q, exit %d; %s: stdout %q, exit %d",
							p.with, with.stdout, with.code, p.without, without.stdout, without.code)
					}
				})
			}
		})
	}
}

// Without -e the shell keeps going past a failing line, as `["bash", "-u"]` does.
func TestShellOverride_ListWithoutErrexit(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-cu\"]\n\nt:\n    @echo a\n    @false\n    @echo b\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.stdout != "a\nb\n" || r.code != 0 {
		t.Errorf("stdout = %q, exit = %d; want %q, 0", r.stdout, r.code, "a\nb\n")
	}
}

func TestShellOverride_StateAcrossLines(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-euc\"]\n\nt:\n    @greeting=hello\n    @cd sub\n    @echo \"$greeting $(basename \"$PWD\")\"\n")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, nil, "--color=never", "t")
	if r.stdout != "hello sub\n" || r.code != 0 {
		t.Errorf("stdout = %q, exit = %d, stderr = %q; want %q, 0", r.stdout, r.code, r.stderr, "hello sub\n")
	}
}

// A list that gives the shell its own code is left as written: the script arrives
// as $0, as it does on main.
func TestShellOverride_OwnCommandString(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-c\", \"exec bash -eu $0\"]\n\nt:\n    @echo a\n    @false\n    @echo b\n")
	r := run(t, dir, nil, "--color=never", "t")
	if r.stdout != "a\n" || r.code != 1 {
		t.Errorf("stdout = %q, exit = %d, stderr = %q; want %q, 1", r.stdout, r.code, r.stderr, "a\n")
	}
}
