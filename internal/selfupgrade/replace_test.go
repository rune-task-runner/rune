package selfupgrade

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStageAndReplace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rune")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := stage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(f.Name()) != dir || !strings.HasPrefix(filepath.Base(f.Name()), ".rune-upgrade-") {
		t.Errorf("stage created %s, want .rune-upgrade-* inside %s", f.Name(), dir)
	}
	if _, err := f.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(f.Name(), target); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("target = %q, want %q", b, "new")
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("staged file still exists: %v", err)
	}
	if b, _ := os.ReadFile(other); string(b) != "untouched" {
		t.Errorf("unrelated file changed to %q", b)
	}
}

func TestStageFailsInMissingDir(t *testing.T) {
	if _, err := stage(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("stage succeeded in a directory that does not exist")
	}
}

func TestReplaceBinaryMissingStaged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rune")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(filepath.Join(dir, "absent"), target); err == nil {
		t.Error("replaceBinary succeeded without a staged file")
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Errorf("target = %q after a failed replace, want it unchanged", b)
	}
}

func TestAsideNameAndRemoveAside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rune.exe")
	touch := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := asideName(target); got != target+".old" {
		t.Errorf("asideName = %q, want the plain .old name", got)
	}
	// An .old that could not be removed (still running) must not block the move.
	touch(target + ".old")
	if got := asideName(target); got != target+".old.1" {
		t.Errorf("asideName with .old taken = %q, want .old.1", got)
	}
	touch(target + ".old.1")
	if got := asideName(target); got != target+".old.2" {
		t.Errorf("asideName with .old and .old.1 taken = %q, want .old.2", got)
	}

	touch(target)
	// Only the plain and the numbered names are Rune's to delete.
	keep := []string{"rune.exe", "rune.exe.old.", "rune.exe.old.1a", "rune.exe.old.backup", "rune.exe.older"}
	for _, name := range keep[1:] {
		touch(filepath.Join(dir, name))
	}
	removeAside(target)
	if got := dirNames(t, dir); !slices.Equal(got, keep) {
		t.Errorf("after removeAside the directory holds %v, want %v", got, keep)
	}

	// Pattern characters in the install path must not hide the leftovers.
	odd := filepath.Join(t.TempDir(), "tools [x64]*")
	if err := os.Mkdir(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(odd, "rune.exe")
	for _, p := range []string{target, target + ".old", target + ".old.1", target + ".old.27"} {
		touch(p)
	}
	removeAside(target)
	if got := dirNames(t, odd); !slices.Equal(got, []string{"rune.exe"}) {
		t.Errorf("after removeAside the directory holds %v, want only rune.exe", got)
	}
	removeAside(filepath.Join(odd, "absent", "rune.exe"))
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRemoveOldWithoutLeftover(t *testing.T) {
	// On every system this must be safe to call when nothing was left behind.
	removeOld(filepath.Join(t.TempDir(), "rune"))
}
