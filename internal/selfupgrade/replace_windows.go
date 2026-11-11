//go:build windows

package selfupgrade

import "os"

// replaceBinary swaps staged in for target. Windows refuses to overwrite the
// file of a running program but allows renaming it, so the old binary is
// moved aside first and moved back if the second step fails. Between the two
// renames the target does not exist; the old binary is then at the aside name.
func replaceBinary(staged, target string) error {
	removeAside(target)
	old := asideName(target)
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if back := os.Rename(old, target); back != nil {
			return &rollbackError{err: err, old: old}
		}
		return err
	}
	return nil
}

// removeOld deletes the binaries previous upgrades moved aside.
func removeOld(target string) { removeAside(target) }
