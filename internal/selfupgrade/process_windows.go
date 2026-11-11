//go:build windows

package selfupgrade

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a running process.
const stillActive = 259

// processExists reports whether a process with this id is running. Anything
// but a clear answer that it is not counts as running.
func processExists(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Windows answers "invalid parameter" for an id no process has.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
