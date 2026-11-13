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

// taskListFlags are the serve flags that take task names. They share one
// parser and one name check (spec 041 R4).
var taskListFlags = []string{"--allow-destructive", "--allow", "--deny"}

// Spec 031 contracts/cli-serve.md "Accepted forms": values split on commas,
// items trimmed, duplicates dropped, first-occurrence order kept.
func TestParseTaskNames(t *testing.T) {
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
	for _, flag := range taskListFlags {
		for _, c := range cases {
			got, err := parseTaskNames(flag, c.in)
			if err != nil {
				t.Errorf("parseTaskNames(%s, %q): unexpected error %v", flag, c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseTaskNames(%s, %q) = %q, want %q", flag, c.in, got, c.want)
			}
		}
	}
}

// Spec 031 FR-009, spec 041 FR-010: an empty name, from an empty value or an
// empty item between commas, is a usage error naming the flag.
func TestParseTaskNamesRejectsEmptyName(t *testing.T) {
	wants := map[string]string{
		"--allow-destructive": "--allow-destructive: empty task name",
		"--allow":             "--allow: empty task name",
		"--deny":              "--deny: empty task name",
	}
	for _, flag := range taskListFlags {
		for _, in := range [][]string{{""}, {"clean,"}, {", clean"}, {"clean,,reset"}, {"  "}, {"clean", ""}} {
			_, err := parseTaskNames(flag, in)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Errorf("parseTaskNames(%s, %q) error = %v, want *UsageError", flag, in, err)
				continue
			}
			if got := err.Error(); got != wants[flag] {
				t.Errorf("parseTaskNames(%s, %q) error = %q, want %q", flag, in, got, wants[flag])
			}
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

// Spec 031 FR-008, spec 041 FR-009: an unknown name is a usage error naming
// the flag and the name, with the closest task suggested when one is near.
// Known names of every kind are accepted.
func TestCheckTaskNames(t *testing.T) {
	tasks := grantTasks(t)
	type want struct{ destructive, allow, deny string }
	cases := []struct {
		names []string
		want  want
	}{
		{[]string{"claen"}, want{
			`unknown task in --allow-destructive: claen (did you mean "clean"?)`,
			`unknown task in --allow: claen (did you mean "clean"?)`,
			`unknown task in --deny: claen (did you mean "clean"?)`,
		}},
		{[]string{"zzz"}, want{
			"unknown task in --allow-destructive: zzz",
			"unknown task in --allow: zzz",
			"unknown task in --deny: zzz",
		}},
		{[]string{"*"}, want{
			"unknown task in --allow-destructive: *",
			"unknown task in --allow: *",
			"unknown task in --deny: *",
		}},
		{[]string{"tools__clean"}, want{
			`unknown task in --allow-destructive: tools__clean (did you mean "tools::clean"?)`,
			`unknown task in --allow: tools__clean (did you mean "tools::clean"?)`,
			`unknown task in --deny: tools__clean (did you mean "tools::clean"?)`,
		}},
		{[]string{"clean", "nope"}, want{
			"unknown task in --allow-destructive: nope",
			"unknown task in --allow: nope",
			"unknown task in --deny: nope",
		}},
		{[]string{"clean", "build", "_wipe", "winonly", "tools::clean"}, want{}},
	}
	for _, c := range cases {
		for flag, wantMsg := range map[string]string{
			"--allow-destructive": c.want.destructive, "--allow": c.want.allow, "--deny": c.want.deny,
		} {
			for range 2 { // candidates are sorted, so the suggestion never varies
				err := checkTaskNames(flag, c.names, tasks)
				if wantMsg == "" {
					if err != nil {
						t.Errorf("checkTaskNames(%s, %q) = %v, want nil", flag, c.names, err)
					}
					continue
				}
				var ue *UsageError
				if !errors.As(err, &ue) || err.Error() != wantMsg {
					t.Errorf("checkTaskNames(%s, %q) = %v, want usage error %q", flag, c.names, err, wantMsg)
				}
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

// With no grant and no filter the line is byte-identical to before spec 031
// (spec 031 FR-012, spec 041 FR-016). A filter adds the exposed count (spec
// 041 FR-014).
func TestStartupLine(t *testing.T) {
	both := []string{"clean", "reset"}
	one := []string{"clean"}
	two := &exposure{exposed: 2, exposable: 14}
	cases := []struct {
		http    bool
		addr    string
		exp     *exposure
		granted []string
		want    string
	}{
		{false, "", nil, nil, "rune MCP server on stdio"},
		{false, "", nil, both, "rune MCP server on stdio (destructive tasks allowed: clean, reset)"},
		{true, "127.0.0.1:7777", nil, nil, "rune MCP server on http://127.0.0.1:7777 (token required)"},
		{true, "127.0.0.1:7777", nil, both, "rune MCP server on http://127.0.0.1:7777 (token required; destructive tasks allowed: clean, reset)"},
		{false, "", two, nil, "rune MCP server on stdio (2 of 14 tasks exposed)"},
		{false, "", two, one, "rune MCP server on stdio (2 of 14 tasks exposed; destructive tasks allowed: clean)"},
		{true, "127.0.0.1:7777", two, nil, "rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed)"},
		{true, "127.0.0.1:7777", two, one, "rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed; destructive tasks allowed: clean)"},
		{false, "", &exposure{exposed: 0, exposable: 14}, nil, "rune MCP server on stdio (0 of 14 tasks exposed)"},
	}
	for _, c := range cases {
		if got := startupLine(c.http, c.addr, c.exp, c.granted); got != c.want {
			t.Errorf("startupLine(%v, %q, %v, %q) = %q, want %q", c.http, c.addr, c.exp, c.granted, got, c.want)
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

// Spec 041 FR-009 to FR-011: a bad --allow or --deny value stops ServeMCP
// before any Runefile code runs, and the flags are checked in a fixed order.
func TestServeMCPFilterUsageErrors(t *testing.T) {
	const src = "# Health.\n[context]\nhealth:\n    @echo ok > context.done\n\n# Build.\nbuild:\n    @echo b\n\n# Clean.\n[confirm]\nclean:\n    @echo c\n"
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"unknown allow", Options{MCPAllow: []string{"biuld"}}, `unknown task in --allow: biuld (did you mean "build"?)`},
		{"unknown deny", Options{MCPDeny: []string{"biuld"}}, `unknown task in --deny: biuld (did you mean "build"?)`},
		{"empty allow", Options{MCPAllow: []string{""}}, "--allow: empty task name"},
		{"empty deny item", Options{MCPDeny: []string{"build,"}}, "--deny: empty task name"},
		{"wildcard", Options{MCPAllow: []string{"*"}}, "unknown task in --allow: *"},
		{
			"destructive name first",
			Options{MCPAllowDestructive: []string{"claen"}, MCPAllow: []string{"biuld"}},
			`unknown task in --allow-destructive: claen (did you mean "clean"?)`,
		},
		{
			"allow before deny",
			Options{MCPAllow: []string{"biuld"}, MCPDeny: []string{"zzz"}},
			`unknown task in --allow: biuld (did you mean "build"?)`,
		},
		{
			"empty item before unknown name",
			Options{MCPAllow: []string{"biuld"}, MCPDeny: []string{""}},
			"--deny: empty task name",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			opts := c.opts
			opts.Cwd, opts.Stderr = dir, &bytes.Buffer{}
			err := ServeMCP(opts, false, "", "")
			var ue *UsageError
			if !errors.As(err, &ue) || err.Error() != c.want {
				t.Errorf("error = %v, want usage error %q", err, c.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
				t.Error("the [context] hook ran before the usage error")
			}
		})
	}
}

// Spec 041 FR-012: a name that can never be a tool on any host warns once per
// flag. A task restricted to another OS is silent.
func TestWarnNeverExposed(t *testing.T) {
	f, diags := parser.Parse("Runefile", "build:\n    @echo b\n\n_setup:\n    @echo s\n\n[private]\nhidden:\n    @echo h\n\n"+
		"[context]\nhealth:\n    @echo ok\n\n[windows]\nwinonly:\n    @echo w\n\n[linux]\nlinuxonly:\n    @echo l\n")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	tasks := indexTasks(f)
	names := []string{"build", "_setup", "winonly", "hidden", "health", "linuxonly"}
	for _, flag := range []string{"--allow", "--deny"} {
		var buf bytes.Buffer
		warnNeverExposed(&buf, style.New(false, &buf), flag, names, tasks)
		want := "warning: " + flag + " _setup has no effect: task \"_setup\" is never exposed to agents\n" +
			"warning: " + flag + " hidden has no effect: task \"hidden\" is never exposed to agents\n" +
			"warning: " + flag + " health has no effect: task \"health\" is never exposed to agents\n"
		if buf.String() != want {
			t.Errorf("%s warnings = %q, want %q", flag, buf.String(), want)
		}
	}
	var buf bytes.Buffer
	warnNeverExposed(&buf, style.New(false, &buf), "--allow", []string{"build", "winonly", "linuxonly"}, tasks)
	if buf.Len() != 0 {
		t.Errorf("exposable and other-OS names warned: %q", buf.String())
	}
}

// Spec 041 FR-013.
func TestWarnNoTasksExposed(t *testing.T) {
	var buf bytes.Buffer
	warnNoTasksExposed(&buf, style.New(false, &buf))
	if want := "warning: no tasks are exposed to agents; check --allow and --deny\n"; buf.String() != want {
		t.Errorf("warning = %q, want %q", buf.String(), want)
	}
}
