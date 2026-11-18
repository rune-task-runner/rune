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

// execLog runs `rune log` through the root command, in dir, with the audit
// log under base.
func execLog(t *testing.T, dir, base string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	t.Setenv("RUNE_AUDIT_DIR", base)
	t.Setenv("RUNE_AUDIT_RETENTION_DAYS", "")
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"log"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func logProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Spec 052: `log` is a built-in command.
func TestLogCommandIsRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "log") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	cmd := newLogCmd(&opts)
	for _, want := range []string{"audit log of this project", "RUNE_AUDIT_DIR", "RUNE_AUDIT_RETENTION_DAYS", "rune -- log", "incomplete"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q", want)
		}
	}
	for _, flag := range []string{"file", "date", "since", "until", "tz", "task", "outcome", "client", "session", "all", "json", "dir"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("no --%s flag", flag)
		}
	}
	if cmd.Flags().ShorthandLookup("f") == nil {
		t.Error("no -f flag")
	}
	// The run flags of the root are not the command's.
	if cmd.Flags().Lookup("yes") != nil {
		t.Error("`rune log` accepts --yes")
	}
}

// The command reads the log directory from the environment and prints the
// directory of the project it was run in.
func TestLogCommandDir(t *testing.T) {
	dir, base := logProject(t), t.TempDir()
	out, errOut, err := execLog(t, dir, base, "--dir")
	if err != nil || errOut != "" {
		t.Fatalf("err %v, standard error %q", err, errOut)
	}
	got := strings.TrimSpace(out)
	if filepath.Dir(got) != base || len(filepath.Base(got)) != 16 {
		t.Errorf("--dir printed %q, want a project directory under %q", got, base)
	}

	// -f names the Runefile from another directory.
	other, err2 := execLogIn(t, t.TempDir(), base, "-f", filepath.Join(dir, "Runefile"), "--dir")
	if err2 != nil || strings.TrimSpace(other) != got {
		t.Errorf("-f: %q, %v, want %q", other, err2, got)
	}
}

func execLogIn(t *testing.T, dir, base string, args ...string) (string, error) {
	t.Helper()
	out, _, err := execLog(t, dir, base, args...)
	return out, err
}

func TestLogCommandLists(t *testing.T) {
	dir, base := logProject(t), t.TempDir()
	out, _, err := execLog(t, dir, base, "--tz", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Agent actions on ") || !strings.HasSuffix(out, "\nNo agent actions.\n") {
		t.Errorf("output %q", out)
	}
	if out, _, err := execLog(t, dir, base, "--json", "--task", "a,b", "--task", "c", "--outcome", "failed", "--client", "x", "--session", "y", "--all", "--since", "2h", "--until", "1h"); err != nil || out != "" {
		t.Errorf("every flag: %q, %v", out, err)
	}
}

func TestLogCommandUsageErrors(t *testing.T) {
	dir, base := logProject(t), t.TempDir()
	if _, _, err := execLog(t, dir, base, "extra"); err == nil {
		t.Error("a positional argument was accepted")
	}
	_, _, err := execLog(t, dir, base, "--outcome", "nope")
	if err == nil || cli.CodeFor(err) != cli.ExitUsage || !strings.HasPrefix(err.Error(), `--outcome: unknown outcome "nope"`) {
		t.Errorf("got %v", err)
	}
	if _, _, err := execLog(t, t.TempDir(), base); err == nil || cli.CodeFor(err) != cli.ExitUsage {
		t.Errorf("no Runefile: %v", err)
	}
}

// The two variables reach the options every command reads.
func TestRootReadsAuditEnvironment(t *testing.T) {
	dir := logProject(t)
	t.Chdir(dir)
	t.Setenv("RUNE_AUDIT_DIR", "/var/log/rune-audit")
	t.Setenv("RUNE_AUDIT_RETENTION_DAYS", "30")
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.AuditDir != "/var/log/rune-audit" || opts.AuditRetention != "30" {
		t.Errorf("options: %q %q", opts.AuditDir, opts.AuditRetention)
	}
}
