package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/cli"
)

// execRune runs rune with args through the root command, in dir.
func execCI(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func ciDir(t *testing.T, runefile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(runefile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Spec 059: `ci` is a built-in command with github and run.
func TestCICommandIsRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "ci") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	ci := newCICmd(&opts)
	var names []string
	for _, c := range ci.Commands() {
		names = append(names, c.Name())
	}
	if !slices.Equal(names, []string{"github", "run"}) {
		t.Errorf("ci subcommands = %v", names)
	}
	github, run := newCIGithubCmd(&opts), newCIRunCmd(&opts)
	for name, short := range map[string]string{"file": "f", "output": "o", "write": "", "check": "", "os": "", "rune-version": "", "branch": ""} {
		if f := github.Flags().Lookup(name); f == nil || f.Shorthand != short {
			t.Errorf("ci github --%s: %+v", name, f)
		}
	}
	for _, name := range []string{"file", "dir", "key"} {
		if run.Flags().Lookup(name) == nil {
			t.Errorf("ci run has no --%s", name)
		}
	}
	for _, want := range []string{"Exit status: 0", "Nothing runs", "no secret", "--check"} {
		if !strings.Contains(github.Long, want) && !strings.Contains(github.Long, strings.ToLower(want)) {
			t.Errorf("the help of ci github lacks %q", want)
		}
	}
}

func TestCITaskNamedCI(t *testing.T) {
	dir := ciDir(t, "ci:\n    @echo task-ci-ran\n")
	stdout, stderr, err := execCI(t, dir, "ci")
	if err != nil || !strings.Contains(stderr, `note: "ci" is also a task of this Runefile; run it with: rune -- ci`) {
		t.Errorf("rune ci: %v, %q", err, stderr)
	}
	if !strings.Contains(stdout, "github") {
		t.Errorf("rune ci printed no help: %q", stdout)
	}
	_, _, err = execCI(t, dir, "ci", "deploy")
	if err == nil || !strings.Contains(err.Error(), `unknown command "deploy" for "rune ci"; to run a task named ci, use: rune -- ci`) {
		t.Errorf("rune ci deploy: %v", err)
	}
}

func TestCIGithubWiring(t *testing.T) {
	dir := ciDir(t, "a:\n    @echo a\n")
	stdout, stderr, err := execCI(t, dir, "ci", "github", "--rune-version", "0.11.0", "--branch", "trunk", "--os", "macos", "a")
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	for _, want := range []string{"branches: ['trunk']", "runs-on: macos-latest", "--os macos --branch trunk --rune-version 0.11.0 --write a"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in the workflow", want)
		}
	}
	for _, args := range [][]string{
		{"ci", "github", "--output", "", "a"},
		{"ci", "github", "--file", "", "a"},
	} {
		if _, _, err := execCI(t, dir, args...); cli.CodeFor(err) != cli.ExitUsage {
			t.Errorf("%q: %v", args, err)
		}
	}
	if _, stderr, err := execCI(t, dir, "ci", "github", "--rune-version", "0.11.0", "--write", "a"); err != nil || !strings.Contains(stderr, "created") {
		t.Errorf("--write: %v %s", err, stderr)
	}
	if _, stderr, err := execCI(t, dir, "ci", "github", "--rune-version", "0.11.0", "--check", "a"); err != nil || !strings.Contains(stderr, "current") {
		t.Errorf("--check: %v %s", err, stderr)
	}
}

func TestCIRunWiring(t *testing.T) {
	dir := ciDir(t, "dep:\n    @echo dep > ran-dep\n\nt *words: dep\n    @echo \"{{words}}\" > ran-t\n")
	if _, stderr, err := execCI(t, dir, "ci", "run", "--", "t", "-v", "--x"); err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ran-t"))
	if err != nil || strings.TrimSpace(string(got)) != "-v --x" {
		t.Errorf("body got %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran-dep")); err == nil {
		t.Error("the dependency ran")
	}
	if _, _, err := execCI(t, dir, "ci", "run", "--dir", "", "t"); cli.CodeFor(err) != cli.ExitUsage {
		t.Errorf("--dir with no path: %v", err)
	}
	if _, _, err := execCI(t, dir, "ci", "run", "--file", "", "t"); cli.CodeFor(err) != cli.ExitUsage {
		t.Errorf("--file with no path: %v", err)
	}
}
