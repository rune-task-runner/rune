//go:build !windows

package selfupgrade

import (
	"errors"
	"syscall"
)

// processExists reports whether a process with this id exists. Anything but
// a clear "no such process" counts as existing.
func processExists(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
