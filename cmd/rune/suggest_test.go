package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/cli"
)

// execRune runs the root command with args in dir, with stdin as standard
// input.
func execRune(t *testing.T, dir, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func wantUsage(t *testing.T, err error, message string) {
	t.Helper()
	usage, ok := err.(*cli.UsageError)
	if !ok || !strings.Contains(usage.Error(), message) {
		t.Errorf("err = %v, want a usage error with %q", err, message)
	}
}

// Spec 058: suggest is a built-in command with apply and forget; agent has
// observe and a hidden record.
func TestSuggestCommandsAreRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "suggest") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	suggest, _, err := root.Find([]string{"suggest"})
	if err != nil || suggest.Name() != "suggest" {
		t.Fatal(err)
	}
	if !strings.Contains(suggest.Long, "A Runefile task named 'suggest' is run with 'rune -- suggest'") {
		t.Error("the Long help does not name rune -- suggest")
	}
	for _, flag := range []string{"min-runs", "file"} {
		if suggest.Flags().Lookup(flag) == nil {
			t.Errorf("suggest has no --%s", flag)
		}
	}
	apply, _, _ := root.Find([]string{"suggest", "apply"})
	if apply.Name() != "apply" || apply.Flags().Lookup("dry-run") == nil || apply.Flags().Lookup("min-runs") == nil {
		t.Error("suggest apply and its flags")
	}
	if forget, _, _ := root.Find([]string{"suggest", "forget"}); forget.Name() != "forget" {
		t.Error("no suggest forget")
	}
	observe, _, _ := root.Find([]string{"agent", "observe"})
	if observe.Name() != "observe" || observe.Flags().Lookup("remove") == nil || observe.Flags().Lookup("dry-run") == nil || observe.Flags().ShorthandLookup("f") == nil {
		t.Error("agent observe and its flags")
	}
	record, _, _ := root.Find([]string{"agent", "record"})
	if record.Name() != "record" || !record.Hidden {
		t.Error("agent record is not a hidden command")
	}
	var help bytes.Buffer
	agent, _, _ := root.Find([]string{"agent"})
	agent.SetOut(&help)
	_ = agent.Help()
	if !strings.Contains(help.String(), "\n  observe ") || strings.Contains(help.String(), "\n  record ") {
		t.Errorf("agent help:\n%s", help.String())
	}
}

func TestSuggestCommandErrors(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	_, _, err := execRune(t, dir, "", "suggest", "bogus")
	wantUsage(t, err, `unknown command "bogus" for "rune suggest"; to run a task named suggest, use: rune -- suggest`)
	_, _, err = execRune(t, dir, "", "suggest", "--min-runs", "0")
	wantUsage(t, err, "--min-runs must be a whole number from 1 to 1000")
	_, _, err = execRune(t, dir, "", "suggest", "apply")
	wantUsage(t, err, "rune suggest apply needs the ID of a suggestion")
	_, _, err = execRune(t, dir, "", "suggest", "--file", "")
	wantUsage(t, err, "--file needs a path")
	_, _, err = execRune(t, dir, "", "suggest", "apply", "--file=", "x")
	wantUsage(t, err, "--file needs a path")
	_, _, err = execRune(t, dir, "", "suggest", "forget", "--file=")
	wantUsage(t, err, "--file needs a path")
	_, _, err = execRune(t, dir, "", "agent", "observe")
	wantUsage(t, err, "rune agent observe takes one client: claude")
	_, _, err = execRune(t, dir, "", "agent", "observe", "claude", "--file=")
	wantUsage(t, err, "--file needs a path")
}

// The hook's command prints nothing and succeeds whatever it is given.
func TestAgentRecordCommand(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	for _, args := range [][]string{{"agent", "record"}, {"agent", "record", "--bogus", "x"}} {
		stdout, stderr, err := execRune(t, t.TempDir(), "not json", args...)
		if err != nil || stdout != "" || stderr != "" {
			t.Errorf("%v: %v %q %q", args, err, stdout, stderr)
		}
	}
}

// Observe, record, list, preview, apply and forget through the commands.
func TestSuggestCommandFlow(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", dir)
	if _, stderr, err := execRune(t, dir, "", "agent", "observe", "claude"); err != nil || !strings.Contains(stderr, "created") {
		t.Fatalf("%v %s", err, stderr)
	}
	for i := range 3 {
		for _, event := range []string{"PreToolUse", "PostToolUse"} {
			input := fmt.Sprintf(`{"hook_event_name":%q,"tool_name":"Bash","session_id":"s","tool_use_id":"t%d","cwd":%q,"tool_input":{"command":"go vet ./..."}}`, event, i, dir)
			if _, _, err := execRune(t, dir, input, "agent", "record"); err != nil {
				t.Fatal(err)
			}
		}
	}
	stdout, _, err := execRune(t, dir, "", "suggest")
	m := regexp.MustCompile(`\[([0-9a-f]{8})\]`).FindStringSubmatch(stdout)
	if err != nil || m == nil || !strings.Contains(stdout, "ran this 3 times") {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if stdout, _, err := execRune(t, dir, "", "suggest", "apply", m[1], "--dry-run"); err != nil || !strings.Contains(stdout, "+go-vet:") {
		t.Errorf("dry run: %v\n%s", err, stdout)
	}
	if _, stderr, err := execRune(t, dir, "", "suggest", "apply", m[1]); err != nil || !strings.Contains(stderr, "Runefile: created, added go-vet") {
		t.Errorf("apply: %v\n%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "Runefile")); err != nil {
		t.Error(err)
	}
	if _, stderr, err := execRune(t, dir, "", "suggest", "forget"); err != nil || !strings.HasPrefix(stderr, "deleted 6 records") {
		t.Errorf("forget: %v %s", err, stderr)
	}
	if _, stderr, err := execRune(t, dir, "", "agent", "observe", "claude", "--remove"); err != nil || !strings.Contains(stderr, "not observing") {
		t.Errorf("remove: %v %s", err, stderr)
	}
}
