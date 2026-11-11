//go:build windows

package selfupgrade

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// copyRunningExe copies this test binary to dir and starts the copy, so the
// file is in use the way a running rune.exe is.
func copyRunningExe(t *testing.T, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	target := filepath.Join(dir, "rune.exe")
	dst, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(target, "-test.run", "^TestHelperSleep$")
	cmd.Env = append(os.Environ(), "RUNE_TEST_HELPER_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	time.Sleep(500 * time.Millisecond)
	return target
}

// TestHelperSleep keeps a copied test binary running; it does nothing when
// run as part of the normal suite.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("RUNE_TEST_HELPER_SLEEP") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestReplaceRunningBinary(t *testing.T) {
	dir := t.TempDir()
	target := copyRunningExe(t, dir)
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".rune-upgrade-new")
	if err := os.WriteFile(staged, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(staged, target); err != nil {
		t.Fatalf("replaceBinary on a running executable: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new binary" {
		t.Errorf("target does not hold the new binary")
	}
	kept, err := os.ReadFile(target + ".old")
	if err != nil || len(kept) != len(old) {
		t.Errorf("old binary not kept as .old (err %v)", err)
	}
}

// A process still running from the binary an earlier upgrade moved aside
// must not block the next upgrade.
func TestReplaceWhileOldBinaryStillRuns(t *testing.T) {
	dir := t.TempDir()
	target := copyRunningExe(t, dir)
	for i, content := range []string{"second binary", "third binary"} {
		staged := filepath.Join(dir, ".rune-upgrade-new")
		if err := os.WriteFile(staged, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := replaceBinary(staged, target); err != nil {
			t.Fatalf("upgrade %d: %v", i+1, err)
		}
		if b, _ := os.ReadFile(target); string(b) != content {
			t.Errorf("upgrade %d: target holds %q", i+1, b)
		}
	}
	if _, err := os.Stat(target + ".old"); err != nil {
		t.Errorf("the running old binary is gone: %v", err)
	}
	if b, _ := os.ReadFile(target + ".old.1"); string(b) != "second binary" {
		t.Errorf(".old.1 holds %q, want the second binary", b)
	}
}

func TestReplaceRollsBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rune.exe")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(filepath.Join(dir, "absent"), target); err == nil {
		t.Fatal("replaceBinary succeeded without a staged file")
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Errorf("target = %q after a failed replace, want the old binary restored", b)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old left behind after a rollback: %v", err)
	}
}

func TestRemoveOld(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rune.exe")
	removeOld(target)
	if err := os.WriteFile(target+".old", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	removeOld(target)
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("removeOld left the file: %v", err)
	}
}
