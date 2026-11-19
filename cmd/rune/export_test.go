package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rune-task-runner/rune/internal/cli"
)

const exportTestRunefile = "# Build.\nbuild:\n    @echo x\n"

// execExport runs `rune export` through the root command, in dir.
func execExport(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"export"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func exportDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(exportTestRunefile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Spec 056: `export` is a built-in command, and its help says what the
// contract asks of it.
func TestExportCommandIsRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "export") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	export := newExportCmd(&opts)
	help := export.Short + "\n" + export.Long + "\n" + export.Example
	for _, want := range []string{
		"agents-md", "claude-skill", "AGENTS.md", ".claude/skills/rune-tasks/",
		"Nothing of the Runefile runs", "The text is guidance", "does not limit\nwhat a shell can run",
		"Exit status: 0", "run with 'rune -- export'",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the help of rune export lacks %q", want)
		}
	}
	for name, short := range map[string]string{"file": "f", "output": "o", "write": "", "check": ""} {
		f := export.Flags().Lookup(name)
		if f == nil || f.Shorthand != short {
			t.Errorf("flag --%s: %+v, want the short form %q", name, f, short)
		}
	}
}

// What the exported file does is described in plain words: it is guidance.
func TestExportHelpClaimsNoEnforcement(t *testing.T) {
	var opts cli.Options
	export := newExportCmd(&opts)
	text := export.Short + "\n" + export.Long + "\n" + export.Example
	export.Flags().VisitAll(func(f *pflag.Flag) { text += "\n" + f.Usage })
	if word := regexp.MustCompile(`(?i)\b(restricted|locked down|safe|secure|sandboxed)\b`).FindString(text); word != "" {
		t.Errorf("the help of rune export says %q", word)
	}
}

func TestExportCompletesTheFormat(t *testing.T) {
	var opts cli.Options
	export := newExportCmd(&opts)
	got, directive := export.ValidArgsFunction(export, nil, "")
	if !slices.Equal(got, []cobra.Completion{"agents-md", "claude-skill"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("completion = %q, %v", got, directive)
	}
	if got, _ := export.ValidArgsFunction(export, []string{"agents-md"}, ""); got != nil {
		t.Errorf("completion after a format = %q, want nothing", got)
	}
}

// The command accepts any arguments and hands them to cli.Export, which
// knows the formats: every mistake is a usage error that says how to run a
// task named export.
func TestExportArguments(t *testing.T) {
	dir := exportDir(t)
	const needs = "rune export needs one format: agents-md or claude-skill; to run a task named export, use: rune -- export"
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, needs},
		{[]string{"agents-md", "claude-skill"}, needs},
		{[]string{"deploy"}, `unknown format "deploy" for "rune export" (agents-md or claude-skill); to run a task named export, use: rune -- export`},
		{[]string{"agents-md", "--file", ""}, "--file needs a path"},
		{[]string{"agents-md", "--output", ""}, "--output needs a path"},
		{[]string{"agents-md", "--check", "-o", ""}, "--output needs a path"},
		{[]string{"agents-md", "--check", "--write"}, "--check cannot be combined with --write: a check writes nothing"},
	} {
		stdout, _, err := execExport(t, dir, c.args...)
		var usage *cli.UsageError
		if !asUsage(err, &usage) || err.Error() != c.want {
			t.Errorf("rune export %q: err = %v, want the usage error %q", c.args, err, c.want)
		}
		if stdout != "" {
			t.Errorf("rune export %q printed %q", c.args, stdout)
		}
	}
}

// The flags reach the command: each changes what a run does.
func TestExportFlags(t *testing.T) {
	dir := exportDir(t)
	stdout, stderr, err := execExport(t, dir, "agents-md")
	if err != nil || !strings.HasPrefix(stdout, "<!-- rune:begin tasks fingerprint=sha256:") || stderr != "" {
		t.Fatalf("print: err = %v\n%s\n%s", err, stdout, stderr)
	}
	if got := entryNames(t, dir); got != "Runefile" {
		t.Errorf("a run with no flag wrote %q", got)
	}
	if _, stderr, _ = execExport(t, dir, "agents-md", "--check"); !strings.HasPrefix(stderr, "AGENTS.md: missing\n") {
		t.Errorf("--check:\n%s", stderr)
	}
	if _, stderr, _ = execExport(t, dir, "agents-md", "--write"); !strings.HasPrefix(stderr, "AGENTS.md: created: 1 task, ") {
		t.Errorf("--write:\n%s", stderr)
	}
	if _, stderr, err = execExport(t, dir, "agents-md", "--check"); err != nil || stderr != "AGENTS.md: current\n" {
		t.Errorf("--check after --write: err = %v\n%s", err, stderr)
	}
	for _, flag := range []string{"--output", "-o"} {
		if _, stderr, _ = execExport(t, dir, "claude-skill", flag, "skills/mine"); !strings.HasPrefix(stderr, "skills/mine/SKILL.md: ") {
			t.Errorf("%s:\n%s", flag, stderr)
		}
	}

	elsewhere := t.TempDir()
	for _, flag := range []string{"--file", "-f"} {
		if stdout, _, err = execExport(t, elsewhere, "agents-md", flag, filepath.Join(dir, "Runefile")); err != nil || !strings.Contains(stdout, "### `build`") {
			t.Errorf("%s: err = %v\n%s", flag, err, stdout)
		}
	}
	if got := entryNames(t, elsewhere); got != "" {
		t.Errorf("--file wrote %q in the working directory", got)
	}
}
