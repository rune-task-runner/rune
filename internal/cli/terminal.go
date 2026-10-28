package cli

import (
	"os"

	"github.com/mattn/go-isatty"
)

// isTerminal reports whether stream is a terminal-backed *os.File. Pipes,
// regular files, buffers, and nil are never terminals. It is the one
// definition shared by the picker (which draws a UI) and the [confirm] gate
// (which only accepts an answer a person could have typed, spec 027).
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && f != nil && isTTY(f.Fd())
}

// isTTY covers native terminals and the MSYS/Cygwin pty pipes used by Git
// Bash and mintty on Windows.
func isTTY(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
