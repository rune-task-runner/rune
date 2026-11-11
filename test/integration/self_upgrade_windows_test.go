//go:build windows

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

// Windows cannot overwrite a running program's file, so an upgrade moves the
// old binary aside. The next --self-upgrade run removes it.
func TestSelfUpgradeWindowsLeavesOld(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")
	if r.code != 0 {
		t.Fatalf("exit %d; stderr:\n%s", r.code, r.stderr)
	}
	if v := runBin(t, in.bin, t.TempDir(), nil, "--version"); v.code != 0 || !strings.Contains(v.stdout, "9.9.9") {
		t.Fatalf("replaced binary --version: exit %d, stdout %q", v.code, v.stdout)
	}
	old := in.bin + ".old"
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the previous binary was not kept as %s: %v", old, err)
	}

	// The replaced binary is stamped 9.9.9, so this run finds nothing to
	// upgrade, and clears the leftover on the way.
	r = in.exec(t, t.TempDir(), nil, "--self-upgrade")
	if r.code != 0 || !strings.Contains(r.stderr, "current: nothing to upgrade") {
		t.Fatalf("second run: exit %d; stderr:\n%s", r.code, r.stderr)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("%s was not removed by the next --self-upgrade: %v", old, err)
	}
}
