//go:build !windows

package interp

import (
	"context"
	"errors"
	"testing"

	"github.com/rune-task-runner/rune/internal/token"
)

// A signal-killed body reports 128 + S, the shell convention, instead of the
// -1 that exec.ExitError.ExitCode returns (spec 028 R3, FR-007).
func TestRunSignalKilledReports128PlusSignal(t *testing.T) {
	err := Run(context.Background(), "sig", "kill -TERM $$\n", []string{"sh"}, token.Span{}, Options{})
	var ee *ExecError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want ExecError", err)
	}
	if ee.Code != 143 {
		t.Errorf("code = %d, want 143 (128 + SIGTERM)", ee.Code)
	}
}
