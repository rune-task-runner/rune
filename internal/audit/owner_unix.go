//go:build unix

package audit

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the file belongs to the user this
// process runs as. A file system that does not say counts as owned.
func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}
