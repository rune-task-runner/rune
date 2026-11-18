//go:build unix

package sandbox

import (
	"os"
	"syscall"
)

// linkCount is the number of names a file has.
func linkCount(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink) //nolint:unconvert // the field's type differs between systems
	}
	return 1
}
