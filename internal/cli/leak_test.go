package cli

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs this package's tests under goleak to catch leaked goroutines
// (Constitution Principle VIII: every goroutine must have a guaranteed exit).
//
// A server started by a test writes an audit log (spec 052). The state
// directory is pointed at a temporary one for the whole package, so no test
// writes into a real home directory.
func TestMain(m *testing.M) {
	// A run of this binary as the worker of a confined body, which the stub
	// backend of the sandbox tests starts in place of the rune binary.
	if len(os.Args) == 2 && os.Args[1] == SandboxWorkerArg {
		os.Exit(SandboxWorker(context.Background()))
	}
	state, err := os.MkdirTemp("", "rune-cli-state")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_STATE_HOME", state); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	goleak.VerifyTestMain(m, goleak.Cleanup(func(code int) {
		_ = os.RemoveAll(state)
		os.Exit(code)
	}))
}
