package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/rune-task-runner/rune/internal/cli"
)

// execRoot runs the root command in dir with a stub upgrade and reports how
// often the stub ran.
func execRoot(t *testing.T, dir string, args ...string) (stdout, stderr string, calls int, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", func(context.Context, *cli.Options) error {
		calls++
		return nil
	})
	root.AddCommand(newVersionCmd(&opts))
	applyHelp(root)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), calls, err
}

func TestSelfUpgradeRunsBeforeRunefileDiscovery(t *testing.T) {
	empty := t.TempDir()
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "Runefile"), []byte("}{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	needsNewer := t.TempDir()
	if err := os.WriteFile(filepath.Join(needsNewer, "Runefile"), []byte("set minimum_version := \"99.0.0\"\n\nbuild:\n    echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"no Runefile": empty, "broken Runefile": broken, "Runefile needs newer rune": needsNewer} {
		stdout, stderr, calls, err := execRoot(t, dir, "--self-upgrade")
		if err != nil {
			t.Errorf("%s: err = %v", name, err)
		}
		if calls != 1 {
			t.Errorf("%s: upgrade ran %d times, want 1", name, calls)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("%s: unexpected output: stdout %q, stderr %q", name, stdout, stderr)
		}
	}
}

// The root command enforces the rule itself, for callers that do not run
// the pre-dispatch check in main.
func TestRootRejectsSelfUpgradeCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--self-upgrade", "build"},
		{"--self-upgrade", "--list"},
		{"--dry-run", "--self-upgrade"},
		{"--self-upgrade", "--", "build"},
	} {
		_, _, calls, err := execRoot(t, t.TempDir(), args...)
		var ue *cli.UsageError
		if !errors.As(err, &ue) || calls != 0 {
			t.Errorf("%v: err = %v, upgrade ran %d times; want a usage error and no upgrade", args, err, calls)
		}
	}
	if _, _, calls, err := execRoot(t, t.TempDir(), "--color", "never", "--self-upgrade"); err != nil || calls != 1 {
		t.Errorf("--color with --self-upgrade: err %v, upgrade ran %d times, want 1", err, calls)
	}
}

// The message names the first conflicting flag as typed, not alphabetically.
func TestSelfUpgradeConflictNamesFirstTyped(t *testing.T) {
	tests := map[string][]string{
		"--yes":     {"--self-upgrade", "--yes", "--list"},
		"--quiet":   {"--quiet", "--self-upgrade", "--dry-run"},
		"--file":    {"--self-upgrade", "-f", "x", "--dry-run"},
		"--summary": {"--color", "never", "--summary", "--choose", "--self-upgrade"},
	}
	for want, args := range tests {
		err := selfUpgradeConflict(args)
		if err == nil || err.Error() != "--self-upgrade cannot be combined with "+want {
			t.Errorf("%v: err = %v, want %s named", args, err, want)
		}
	}
}

func TestMentionsSelfUpgrade(t *testing.T) {
	yes := [][]string{{"--self-upgrade"}, {"--list", "--self-upgrade"}, {"--self-upgrade=true"}, {"build", "--self-upgrade"}}
	no := [][]string{nil, {"build"}, {"--list"}, {"--self-upgrades"}, {"-self-upgrade"}, {"self-upgrade"}}
	for _, args := range yes {
		if !mentionsSelfUpgrade(args) {
			t.Errorf("%v: not recognised", args)
		}
	}
	for _, args := range no {
		if mentionsSelfUpgrade(args) {
			t.Errorf("%v: wrongly recognised", args)
		}
	}
}

func TestWithoutFlagUpgradeNeverRuns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--list"}, {"--dry-run", "build"}, {"version"}} {
		if _, _, calls, err := execRoot(t, dir, args...); err != nil || calls != 0 {
			t.Errorf("%v: err %v, upgrade ran %d times, want 0", args, err, calls)
		}
	}
}

func TestHelpListsSelfUpgrade(t *testing.T) {
	stdout, _, calls, err := execRoot(t, t.TempDir(), "--help")
	if err != nil || calls != 0 {
		t.Fatalf("--help: err %v, calls %d", err, calls)
	}
	var line string
	for l := range strings.Lines(stdout) {
		if strings.Contains(l, "--self-upgrade") {
			line = l
		}
	}
	if !strings.Contains(line, "upgrade this rune to the latest stable release; runs no task") {
		t.Errorf("help line for --self-upgrade = %q", line)
	}
}

func TestSelfUpgradeConflict(t *testing.T) {
	allowed := [][]string{
		{"--self-upgrade"},
		{"--self-upgrade", "--color", "never"},
		{"--color=always", "--self-upgrade"},
		{"--self-upgrade", "--self-upgrade"},
		{"--self-upgrade", "--help"},
		{"-h", "--self-upgrade"},
		// The flag is not set at root level: nothing to object to.
		{},
		{"build"},
		{"deploy", "--self-upgrade"},
		{"deploy", "--", "--self-upgrade"},
		{"--", "--self-upgrade"},
		{"serve", "--self-upgrade"},
		{"--list"},
		{"--no-such-flag"},
		{"--no-such-flag", "--self-upgrade"},
	}
	for _, args := range allowed {
		if err := selfUpgradeConflict(args); err != nil {
			t.Errorf("%v: err = %v, want nil", args, err)
		}
	}

	conflicts := map[string][][]string{
		"task arguments": {
			{"--self-upgrade", "build"},
			{"--self-upgrade", "X=1"},
			{"--self-upgrade", "--", "build"},
			{"--self-upgrade", "serve"},
			{"--color", "never", "--self-upgrade", "build"},
		},
		"--list":           {{"--self-upgrade", "--list"}, {"--list", "--self-upgrade"}},
		"--version":        {{"--self-upgrade", "--version"}, {"--version", "--self-upgrade"}},
		"--dry-run":        {{"--self-upgrade", "--dry-run"}, {"--dry-run", "--self-upgrade"}},
		"--summary":        {{"--self-upgrade", "--summary"}},
		"--dump":           {{"--self-upgrade", "--dump"}},
		"--format":         {{"--self-upgrade", "--format", "json"}, {"--format=json", "--self-upgrade"}},
		"--fmt":            {{"--self-upgrade", "--fmt"}},
		"--watch":          {{"--self-upgrade", "--watch"}},
		"--choose":         {{"--self-upgrade", "--choose"}},
		"--clear-cache":    {{"--self-upgrade", "--clear-cache"}},
		"--file":           {{"--self-upgrade", "--file", "x"}, {"-f", "x", "--self-upgrade"}},
		"--set":            {{"--self-upgrade", "--set", "A=1"}},
		"--yes":            {{"--self-upgrade", "--yes"}},
		"--quiet":          {{"--self-upgrade", "--quiet"}},
		"--ignore-version": {{"--self-upgrade", "--ignore-version"}},
	}
	for what, cases := range conflicts {
		for _, args := range cases {
			err := selfUpgradeConflict(args)
			var ue *cli.UsageError
			want := "--self-upgrade cannot be combined with " + what
			if !errors.As(err, &ue) || err.Error() != want {
				t.Errorf("%v: err = %v, want usage error %q", args, err, want)
			}
		}
	}
}

// A flag added to the root command later must conflict unless someone
// decides otherwise here.
func TestEveryRootFlagConflictsWithSelfUpgrade(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "", "", nil)
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	exempt := map[string]bool{"self-upgrade": true, "color": true, "help": true}
	check := func(f *pflag.Flag) {
		if exempt[f.Name] {
			return
		}
		args := []string{"--self-upgrade", "--" + f.Name}
		if f.Value.Type() != "bool" {
			args = append(args, "x")
		}
		if err := selfUpgradeConflict(args); err == nil {
			t.Errorf("--%s can be combined with --self-upgrade", f.Name)
		}
	}
	root.Flags().VisitAll(check)
	root.PersistentFlags().VisitAll(check)
}
