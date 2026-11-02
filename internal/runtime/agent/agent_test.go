package agent

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func lookFalse() (string, error) { return exec.LookPath("false") }

func TestRunNotConfigured(t *testing.T) {
	_, err := CLIProvider{}.Run(context.Background(), "do something", Options{})
	var nc *NotConfiguredError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want NotConfiguredError (never invent credentials)", err)
	}
}

func TestRunMissingCLI(t *testing.T) {
	_, err := CLIProvider{}.Run(context.Background(), "prompt", Options{
		AgentCmd: []string{"definitely-not-a-real-agent-cli-xyz", "-p"},
	})
	var ni *NotInstalledError
	if !errors.As(err, &ni) {
		t.Fatalf("err = %v, want NotInstalledError", err)
	}
	if !strings.Contains(ni.Error(), "not found on PATH") {
		t.Errorf("message not actionable: %q", ni.Error())
	}
}

func TestRunCapturesOutput(t *testing.T) {
	// Use a real always-present binary as a stand-in "agent CLI": echo prints
	// the prompt, which we capture as the final text.
	out, err := CLIProvider{}.Run(context.Background(), "hello-prompt", Options{
		AgentCmd: []string{"echo"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello-prompt") {
		t.Errorf("captured output = %q, want it to contain the prompt", out)
	}
}

func TestRunFailureIsAuthError(t *testing.T) {
	// `false` exits non-zero, modeling an unauthenticated/failed agent CLI.
	if _, err := lookFalse(); err != nil {
		t.Skip("no `false` binary available")
	}
	_, err := CLIProvider{}.Run(context.Background(), "prompt", Options{AgentCmd: []string{"false"}})
	var ae *AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AuthError", err)
	}
}

// Ctrl-C kills the agent CLI, and Wait reports that as an exit error. Run must
// return the context error so the run exits 130 instead of reporting an
// agent failure (spec 028 user-interrupt edge case).
func TestRunCancelledReturnsContextError(t *testing.T) {
	for _, bin := range []string{"sh", "sleep"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()

	_, err := CLIProvider{}.Run(ctx, "prompt", Options{AgentCmd: []string{"sh", "-c", "exec sleep 5"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
