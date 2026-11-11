package selfupgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// stagePrefix names every temporary file an upgrade creates.
const stagePrefix = ".rune-upgrade-"

// oldSuffix names the previous binary kept beside the new one on Windows.
const oldSuffix = ".old"

// stage creates a temporary file in dir. Staging beside the binary keeps the
// final step a rename within one filesystem.
func stage(dir string) (*os.File, error) {
	return os.CreateTemp(dir, stagePrefix+"*")
}

// asideName picks where the binary at target is moved to. The plain ".old"
// name is taken while a process started from an earlier binary still runs
// from it, so a numbered name is used then.
func asideName(target string) string {
	name := target + oldSuffix
	for i := 1; i < 100; i++ {
		if _, err := os.Lstat(name); err != nil {
			return name
		}
		name = fmt.Sprintf("%s%s.%d", target, oldSuffix, i)
	}
	return name
}

// removeAside deletes the binaries earlier upgrades moved aside. One that is
// still running cannot be deleted; a later run removes it.
func removeAside(target string) {
	_ = os.Remove(target + oldSuffix)
	dir := filepath.Dir(target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	// Names are compared, not globbed: the install path may hold pattern
	// characters, and only the numbered names are Rune's to delete.
	prefix := filepath.Base(target) + oldSuffix + "."
	for _, e := range entries {
		if n, ok := strings.CutPrefix(e.Name(), prefix); ok && allDigits(n) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// rollbackError reports a replacement that failed and could not be undone:
// the target is missing and the previous binary sits at old.
type rollbackError struct {
	err error
	old string
}

func (e *rollbackError) Error() string { return e.err.Error() }
func (e *rollbackError) Unwrap() error { return e.err }
