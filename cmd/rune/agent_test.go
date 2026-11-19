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

// execAgent runs `rune agent` through the root command, in dir.
func execAgent(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"agent"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func agentDir(t *testing.T, runefile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(runefile), 0o644); err != nil {
		t.Fatal(err)
	}
	// No client of the host is found, so every run takes the same path.
	t.Setenv("PATH", t.TempDir())
	return dir
}

// Spec 055: `agent` is a built-in command with one subcommand.
func TestAgentCommandIsRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "agent") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	agent := newAgentCmd(&opts)
	init, _, err := agent.Find([]string{"init"})
	if err != nil || init.Name() != "init" {
		t.Fatalf("no init subcommand: %v", err)
	}
	for _, want := range []string{
		".mcp.json and .claude/settings.json", ".codex/config.toml", ".github/agents/rune.agent.md",
		"Only files inside the project are written", "a deny rule is never removed", "No task runs",
		"Rune-only", "Rune-only within SCOPE", "Registered only", "only for a client version that was checked",
		"--unpinned", "--dry-run", "--check", "--force", "starts no\nprogram",
		"0 for 'Rune-only'", "2 for a usage error or a conflict", "3 when the\nRunefile or a client file is not valid",
		"A Runefile task named 'agent' is run with 'rune -- agent'",
	} {
		if !strings.Contains(init.Long, want) {
			t.Errorf("Long lacks %q", want)
		}
	}
	for _, flag := range []string{"file", "unpinned", "force", "dry-run", "check"} {
		if init.Flags().Lookup(flag) == nil {
			t.Errorf("no --%s flag", flag)
		}
	}
	if init.Flags().ShorthandLookup("f") == nil {
		t.Error("no -f shorthand for --file")
	}
	// The run flags are not this command's: --yes would read as approval.
	for _, flag := range []string{"yes", "watch", "set"} {
		if init.Flags().Lookup(flag) != nil {
			t.Errorf("the command has --%s", flag)
		}
	}
	if !strings.Contains(agent.Long, "A Runefile task named 'agent' is run with 'rune -- agent'") {
		t.Errorf("the help of `rune agent` does not say how a task named agent is run")
	}

	got, directive := init.ValidArgsFunction(init, nil, "")
	if !slices.Equal(got, []cobra.Completion{"claude", "codex", "vscode"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("completion = %q, %v", got, directive)
	}
	if got, _ := init.ValidArgsFunction(init, []string{"claude"}, ""); len(got) != 0 {
		t.Errorf("completion after the client = %q", got)
	}
}

// The setup outcome contract of spec 047 allows three outcomes and no other
// word for a setup. The help keeps to it too.
func TestAgentHelpUsesNoOtherWordForASetup(t *testing.T) {
	var opts cli.Options
	agent := newAgentCmd(&opts)
	init, _, _ := agent.Find([]string{"init"})
	forbidden := regexp.MustCompile(`(?i)\b(restricted|locked down|safe)\b`)
	for _, cmd := range []*cobra.Command{agent, init} {
		text := cmd.Short + "\n" + cmd.Long + "\n" + cmd.Example
		cmd.Flags().VisitAll(func(f *pflag.Flag) { text += "\n" + f.Usage })
		if word := forbidden.FindString(text); word != "" {
			t.Errorf("the help of %q says %q", cmd.CommandPath(), word)
		}
	}
}

func TestAgentWithoutSubcommand(t *testing.T) {
	dir := agentDir(t, "# Ask.\nagent:\n    @echo hi\n")
	stdout, stderr, err := execAgent(t, dir)
	if err != nil {
		t.Fatalf("rune agent: %v", err)
	}
	if !strings.Contains(stdout, "init") || !strings.Contains(stdout, "rune -- agent") {
		t.Errorf("stdout is not the help:\n%s", stdout)
	}
	if want := `note: "agent" is also a task of this Runefile; run it with: rune -- agent` + "\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	// Anything but a subcommand is an error: a script that ran the task
	// must not succeed while doing nothing.
	for _, args := range [][]string{{"deploy"}, {"deploy", "now"}, {"Init"}} {
		_, _, err = execAgent(t, dir, args...)
		var usage *cli.UsageError
		want := `unknown command "` + args[0] + `" for "rune agent"; to run a task named agent, use: rune -- agent`
		if !asUsage(err, &usage) || err.Error() != want {
			t.Errorf("rune agent %v: err = %v, want %q", args, err, want)
		}
	}

	plain := agentDir(t, "build:\n    @echo x\n")
	if _, stderr, err := execAgent(t, plain); err != nil || stderr != "" {
		t.Errorf("no task named agent: err = %v, stderr = %q", err, stderr)
	}
}

func TestAgentInitArguments(t *testing.T) {
	dir := agentDir(t, "build:\n    @echo x\n")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"init"}, "rune agent init needs a client: claude, codex or vscode"},
		{[]string{"init", "claude", "codex"}, "rune agent init takes one client: claude, codex or vscode"},
		{[]string{"init", "cursor"}, `unknown client "cursor": the clients are claude, codex and vscode`},
		{[]string{"init", "claude", "--check", "--force"}, "--check cannot be combined with --force or --dry-run"},
		{[]string{"init", "claude", "--file", ""}, "--file needs a path"},
		{[]string{"init", "claude", "-f", ""}, "--file needs a path"},
	}
	for _, tt := range tests {
		_, _, err := execAgent(t, dir, tt.args...)
		var usage *cli.UsageError
		if !asUsage(err, &usage) || err.Error() != tt.want {
			t.Errorf("rune agent %v: err = %v, want %q", tt.args, err, tt.want)
		}
	}
	if got := entryNames(t, dir); got != "Runefile" {
		t.Errorf("a usage error wrote %q", got)
	}
}

// The flags reach the command: each changes what a run does.
func TestAgentInitFlags(t *testing.T) {
	dir := agentDir(t, "build:\n    @echo x\n")

	stdout, stderr, _ := execAgent(t, dir, "init", "claude", "--dry-run", "--unpinned")
	if !strings.Contains(stdout, `+      "args": ["mcp", "--no-reload"]`) || !strings.HasSuffix(stderr, "dry run: nothing written\n") {
		t.Errorf("--dry-run --unpinned:\n%s\n%s", stdout, stderr)
	}
	if got := entryNames(t, dir); got != "Runefile" {
		t.Errorf("--dry-run wrote %q", got)
	}

	if _, stderr, _ = execAgent(t, dir, "init", "claude", "--check"); !strings.Contains(stderr, "incomplete: 10 of 10 entries") {
		t.Errorf("--check:\n%s", stderr)
	}

	if _, stderr, _ = execAgent(t, dir, "init", "claude", "--unpinned"); !strings.Contains(stderr, ".mcp.json: created") {
		t.Fatalf("setup:\n%s", stderr)
	}
	_, _, err := execAgent(t, dir, "init", "claude")
	if err == nil || err.Error() != "agent init: 1 conflict(s), nothing written" {
		t.Errorf("a run with the path after one without: err = %v", err)
	}
	if _, stderr, _ = execAgent(t, dir, "init", "claude", "--force"); !strings.Contains(stderr, `.mcp.json: updated: replaced args of server "rune"`) {
		t.Errorf("--force:\n%s", stderr)
	}

	other := agentDir(t, "build:\n    @echo x\n")
	elsewhere := t.TempDir()
	for _, flag := range []string{"--file", "-f"} {
		if _, stderr, _ = execAgent(t, elsewhere, "init", "codex", flag, filepath.Join(other, "Runefile")); !strings.Contains(stderr, ".codex/config.toml: ") {
			t.Errorf("%s:\n%s", flag, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(other, ".codex", "config.toml")); err != nil {
		t.Errorf("--file did not set up the named project: %v", err)
	}
	if got := entryNames(t, elsewhere); got != "" {
		t.Errorf("--file wrote %q in the working directory", got)
	}
}

func asUsage(err error, target **cli.UsageError) bool {
	u, ok := err.(*cli.UsageError)
	*target = u
	return ok
}

// entryNames lists the entries of dir.
func entryNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return strings.Join(names, " ")
}
