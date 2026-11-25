package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/cli"
)

// Spec 060: `verify` and `receipt` are built-in commands with their flags.
func TestReceiptCommandsAreRegistered(t *testing.T) {
	root, _ := commandTree(t, &cli.Options{})
	verify, _, err := root.Find([]string{"verify"})
	if err != nil || verify.Name() != "verify" {
		t.Fatalf("no verify command: %v", err)
	}
	for _, name := range []string{"key", "json", "file"} {
		if verify.Flags().Lookup(name) == nil {
			t.Errorf("verify has no --%s", name)
		}
	}
	for _, path := range [][]string{{"receipt", "key"}, {"receipt", "attach"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd.Name() != path[1] {
			t.Fatalf("no %v command: %v", path, err)
		}
	}
	attach, _, _ := root.Find([]string{"receipt", "attach"})
	for _, name := range []string{"key", "file"} {
		if attach.Flags().Lookup(name) == nil {
			t.Errorf("receipt attach has no --%s", name)
		}
	}
}

func TestReceiptParentCommand(t *testing.T) {
	dir := ciDir(t, "receipt:\n    @true\n")
	stdout, stderr, err := execCI(t, dir, "receipt")
	if err != nil || !strings.Contains(stdout, "attach") || !strings.Contains(stderr, `note: "receipt" is also a task of this Runefile; run it with: rune -- receipt`) {
		t.Fatalf("err %v\nstdout %s\nstderr %s", err, stdout, stderr)
	}
	_, _, err = execCI(t, dir, "receipt", "deploy")
	var usage *cli.UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "rune -- receipt") {
		t.Fatalf("unknown subcommand: %v", err)
	}
}

func TestVerifyFileFlagNeedsPath(t *testing.T) {
	_, _, err := execCI(t, ciDir(t, "x:\n    @true\n"), "verify", "--file=", "HEAD", "x")
	var usage *cli.UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--file needs a path") {
		t.Fatalf("err %v", err)
	}
	_, _, err = execCI(t, ciDir(t, "x:\n    @true\n"), "receipt", "attach", "--file=")
	if !errors.As(err, &usage) {
		t.Fatalf("attach: %v", err)
	}
}

func TestVerifyNeedsTasks(t *testing.T) {
	_, _, err := execCI(t, ciDir(t, "x:\n    @true\n"), "verify", "HEAD")
	var usage *cli.UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "rune verify needs a revision and the tasks") {
		t.Fatalf("err %v", err)
	}
}
