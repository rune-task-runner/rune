package integration

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

func TestSelfUpgradeConflict(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	dir := writeRunefile(t, "build:\n    @echo BUILT\n")
	cases := map[string][]string{
		"task arguments": {"--self-upgrade", "build"},
		"--list":         {"--self-upgrade", "--list"},
		"--version":      {"--self-upgrade", "--version"},
		"--dry-run":      {"--dry-run", "--self-upgrade"},
	}
	for what, args := range cases {
		r := in.exec(t, dir, []string{"RUNE_TEST_VERSION=0.0.1"}, args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d, want 2", args, r.code)
		}
		if want := "rune: --self-upgrade cannot be combined with " + what; strings.TrimSpace(r.stderr) != want {
			t.Errorf("%v: stderr = %q, want %q", args, r.stderr, want)
		}
		if r.stdout != "" {
			t.Errorf("%v: stdout = %q, want nothing to run or print", args, r.stdout)
		}
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made by rejected command lines", rel.Requests())
	}
	in.unchanged(t)
}

func TestSelfUpgradePassthrough(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	dir := writeRunefile(t, "deploy *args:\n    @echo ARGS {{args}}\n")
	for _, args := range [][]string{{"deploy", "--self-upgrade"}, {"deploy", "--", "--self-upgrade"}} {
		r := in.exec(t, dir, []string{"RUNE_TEST_VERSION=0.0.1"}, args...)
		if r.code != 0 || !strings.Contains(r.stdout, "--self-upgrade") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, r.code, r.stdout, r.stderr)
		}
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made while running a task", rel.Requests())
	}
	in.unchanged(t)
}

func TestSelfUpgradeHelp(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	dir := t.TempDir()
	for _, args := range [][]string{{"--help"}, {"--self-upgrade", "--help"}} {
		r := in.exec(t, dir, []string{"RUNE_TEST_VERSION=0.0.1"}, args...)
		if r.code != 0 {
			t.Errorf("%v: exit %d", args, r.code)
		}
		if !strings.Contains(r.stdout, "--self-upgrade") || !strings.Contains(r.stdout, "upgrade this rune to the latest stable release; runs no task") {
			t.Errorf("%v: help does not list the flag:\n%s", args, r.stdout)
		}
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made while printing help", rel.Requests())
	}
	in.unchanged(t)
}
