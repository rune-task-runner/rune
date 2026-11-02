package interp

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/token"
)

func TestRunSuccess(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), "greet", "echo hello-from-script\n", []string{"sh"}, token.Span{}, Options{Stdout: &out})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "hello-from-script" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRunNonZeroExit(t *testing.T) {
	err := Run(context.Background(), "boom", "exit 3\n", []string{"sh"}, token.Span{}, Options{})
	var ee *ExecError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want ExecError", err)
	}
	if ee.Code != 3 {
		t.Errorf("code = %d, want 3", ee.Code)
	}
}

func TestRunMissingInterpreter(t *testing.T) {
	err := Run(context.Background(), "x", "noop\n", []string{"definitely-not-a-real-interpreter-xyz"}, token.Span{}, Options{})
	var me *MissingInterpreterError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want MissingInterpreterError", err)
	}
	if !strings.Contains(me.Error(), "not found on PATH") {
		t.Errorf("message not actionable: %q", me.Error())
	}
}

func TestRunNoInterpreter(t *testing.T) {
	err := Run(context.Background(), "x", "noop\n", nil, token.Span{}, Options{})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// exec.CommandContext kills the child on cancellation and Cmd.Wait then
// reports the kill, not the context error; Run must still surface the
// cancellation so the CLI exits 130 and skips || hooks (spec 028 R4).
func TestRunCancelledReturnsContextError(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()

	err := Run(ctx, "slow", "sleep 5\n", []string{"sh"}, token.Span{}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	var ee *ExecError
	if errors.As(err, &ee) {
		t.Errorf("err = %v, want no ExecError for a cancelled body", err)
	}
}
