//go:build !unix

package audit

import "os"

// ownedByCurrentUser accepts every file: this host has no POSIX owner to
// compare, and its modes do not restrict access either.
func ownedByCurrentUser(os.FileInfo) bool { return true }
