//go:build !windows

package selfupgrade

import (
	"os"
	"path/filepath"
)

// replaceBinary moves staged over target in one atomic step.
func replaceBinary(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	// Persist the rename; a failure here does not undo it.
	if dir, err := os.Open(filepath.Dir(target)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// removeOld has nothing to do here: only Windows leaves the old binary behind.
func removeOld(string) {}
