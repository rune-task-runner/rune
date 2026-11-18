//go:build !unix

package sandbox

import "os"

// linkCount is the number of names a file has. No backend exists on these
// systems, so nothing asks.
func linkCount(os.FileInfo) uint64 { return 1 }
