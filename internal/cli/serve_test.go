package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/style"
)

// Spec 031 contracts/cli-serve.md "Accepted forms": values split on commas,
// items trimmed, duplicates dropped, first-occurrence order kept.
func TestParseAllowDestructive(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"clean"}, []string{"clean"}},
		{[]string{"clean,reset"}, []string{"clean", "reset"}},
		{[]string{"clean", "reset"}, []string{"clean", "reset"}},
		{[]string{"clean, reset"}, []string{"clean", "reset"}},
		{[]string{"clean,clean"}, []string{"clean"}},
		{[]string{"reset", "clean,reset"}, []string{"reset", "clean"}},
		{[]string{"tools::clean"}, []string{"tools::clean"}},
	}
	for _, c := range cases {
		got, err := parseAllowDestructive(c.in)
		if err != nil {
			t.Errorf("parseAllowDestructive(%q): unexpected error %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseAllowDestructive(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FR-009: an empty name, from an empty value or an empty item between
// commas, is a usage error.
func TestParseAllowDestructiveRejectsEmptyName(t *testing.T) {
	for _, in := range [][]string{{""}, {"clean,"}, {", clean"}, {"clean,,reset"}, {"  "}, {"clean", ""}} {
		_, err := parseAllowDestructive(in)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("parseAllowDestructive(%q) error = %v, want *UsageError", in, err)
			continue
		}
		if got, want := err.Error(), "--allow-destructive: empty task name"; got != want {
			t.Errorf("parseAllowDestructive(%q) error = %q, want %q", in, got, want)
		}
	}
}

// grantTasks is a task map with a destructive task, a plain one, a private
// destructive one, one restricted to another OS, and an imported destructive
// one, keyed the way indexTasks keys a composed file.
func grantTasks(t *testing.T) map[string]*ast.Task {
	t.Helper()
	src := "[confirm]\nclean:\n    @echo c\n\nbuild:\n    @echo b\n\n[private]\n[confirm]\n_wipe:\n    @echo w\n\n[windows]\n[confirm]\nwinonly:\n    @echo x\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	tasks := indexTasks(f)
	imported := *tasks["clean"]
	imported.Name = "tools::clean"
	tasks["tools::clean"] = &imported
	return tasks
}

// FR-008: an unknown name is a usage error naming it, with the closest
// task suggested when one is near. Known names of every kind are accepted.
func TestCheckGrant(t *testing.T) {
	tasks := grantTasks(t)
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"claen"}, `unknown task in --allow-destructive: claen (did you mean "clean"?)`},
		{[]string{"zzz"}, "unknown task in --allow-destructive: zzz"},
		{[]string{"*"}, "unknown task in --allow-destructive: *"},
		{[]string{"tools__clean"}, `unknown task in --allow-destructive: tools__clean (did you mean "tools::clean"?)`},
		{[]string{"clean", "nope"}, "unknown task in --allow-destructive: nope"},
		{[]string{"clean", "build", "_wipe", "winonly", "tools::clean"}, ""},
	}
	for _, c := range cases {
		for range 2 { // candidates are sorted, so the suggestion never varies
			err := checkGrant(c.names, tasks)
			if c.want == "" {
				if err != nil {
					t.Errorf("checkGrant(%q) = %v, want nil", c.names, err)
				}
				continue
			}
			var ue *UsageError
			if !errors.As(err, &ue) || err.Error() != c.want {
				t.Errorf("checkGrant(%q) = %v, want usage error %q", c.names, err, c.want)
			}
		}
	}
}

// FR-011: a grant for a task without [confirm] warns once and has no effect.
func TestWarnNonDestructive(t *testing.T) {
	tasks := grantTasks(t)
	var buf bytes.Buffer
	warnNonDestructive(&buf, style.New(false, &buf), []string{"build", "clean"}, tasks)
	want := "warning: --allow-destructive build has no effect: task \"build\" is not destructive\n"
	if buf.String() != want {
		t.Errorf("warning = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	warnNonDestructive(&buf, style.New(false, &buf), []string{"_wipe", "winonly", "clean", "tools::clean"}, tasks)
	if buf.Len() != 0 {
		t.Errorf("destructive grants warned: %q", buf.String())
	}
}

// FR-010: a bad --allow-destructive value stops ServeMCP before any Runefile
// code runs, including the [context] hook.
func TestServeMCPUsageErrorRunsNoRunefileCode(t *testing.T) {
	const src = "# Health.\n[context]\nhealth:\n    @echo ok > context.done\n\n# Clean.\n[confirm]\nclean:\n    @echo c\n"
	for _, v := range [][]string{{""}, {"clean,"}, {"claen"}, {"*"}} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		err := ServeMCP(Options{Cwd: dir, Stderr: &stderr, MCPAllowDestructive: v}, false, "", "")
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("ServeMCP with %q: error = %v, want *UsageError", v, err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
			t.Errorf("ServeMCP with %q ran the [context] hook before failing", v)
		}
	}

	// The empty name is reported before the Runefile is even looked for.
	err := ServeMCP(Options{Cwd: t.TempDir(), Stderr: &bytes.Buffer{}, MCPAllowDestructive: []string{""}}, false, "", "")
	if err == nil || err.Error() != "--allow-destructive: empty task name" {
		t.Errorf("empty name without a Runefile: error = %v, want the empty-name usage error", err)
	}
}

// With no grant the line is byte-identical to before spec 031 (FR-012).
func TestStartupLine(t *testing.T) {
	both := []string{"clean", "reset"}
	cases := []struct {
		http    bool
		addr    string
		granted []string
		want    string
	}{
		{false, "", nil, "rune MCP server on stdio"},
		{false, "", both, "rune MCP server on stdio (destructive tasks allowed: clean, reset)"},
		{true, "127.0.0.1:7777", nil, "rune MCP server on http://127.0.0.1:7777 (token required)"},
		{true, "127.0.0.1:7777", both, "rune MCP server on http://127.0.0.1:7777 (token required; destructive tasks allowed: clean, reset)"},
	}
	for _, c := range cases {
		if got := startupLine(c.http, c.addr, c.granted); got != c.want {
			t.Errorf("startupLine(%v, %q, %q) = %q, want %q", c.http, c.addr, c.granted, got, c.want)
		}
	}
}

// Spec 031 FR-012: the startup line lists only grants that take effect on
// this host. A name whose task has no [confirm], or whose task is restricted
// to another OS, is left out; a private destructive task stays, since a
// dependency or hook can reach it.
func TestDestructiveOnly(t *testing.T) {
	f, diags := parser.Parse("Runefile", "[confirm]\nclean:\n    @echo c\n\nbuild:\n    @echo b\n\n[confirm]\nreset:\n    @echo r\n\n"+
		"[windows]\n[confirm]\nwinonly:\n    @echo w\n\n[linux]\n[confirm]\nlinuxonly:\n    @echo l\n\n[private]\n[confirm]\n_wipe:\n    @echo p\n")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	names := []string{"reset", "build", "winonly", "clean", "missing", "linuxonly", "_wipe"}
	got := destructiveOnly(names, indexTasks(f), "linux")
	if want := []string{"reset", "clean", "linuxonly", "_wipe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("destructiveOnly on linux = %q, want %q", got, want)
	}
	got = destructiveOnly(names, indexTasks(f), "windows")
	if want := []string{"reset", "winonly", "clean", "_wipe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("destructiveOnly on windows = %q, want %q", got, want)
	}
}
