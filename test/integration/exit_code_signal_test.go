//go:build !windows

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// A body terminated by signal S makes rune exit 128 + S (spec 028 FR-007).
// Shell bodies get this from the bundled shell; interpreter bodies from
// internal/runtime/interp.

func TestExitCode_ShellBodyKilledBySignal(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Single quotes keep $$ for the inner sh, so it signals itself, not rune.
	dir := writeRunefile(t, "sig:\n    @sh -c 'kill -TERM $$'\n")
	r := run(t, dir, nil, "sig")
	if r.code != 143 {
		t.Errorf("exit = %d, want 143 (128 + SIGTERM); stderr=%s", r.code, r.stderr)
	}
}

func TestExitCode_InterpreterBodyKilledBySignal(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := writeRunefile(t, "sigpy (python):\n    import os, signal; os.kill(os.getpid(), signal.SIGTERM)\n")
	r := run(t, dir, nil, "sigpy")
	if r.code != 143 {
		t.Errorf("exit = %d, want 143 (128 + SIGTERM); stderr=%s", r.code, r.stderr)
	}
}

// Ctrl-C makes rune exit 130 even when the body's child traps SIGINT and exits
// with a code of its own, as test runners commonly do (spec 028 user-interrupt
// edge case). Only rune gets the signal; rune forwards it to the child.
func TestExitCode_InterruptedTrappingChildExits130(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := writeRunefile(t, "trapper:\n    @sh -c 'trap \"exit 2\" INT; echo ready; while :; do sleep 0.1; done'\n")
	cmd := exec.Command(runeBin, "trapper")
	cmd.Dir = dir
	out := &readyWriter{ready: make(chan struct{})}
	var errb bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.ready:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("body never printed ready; stderr=%s", errb.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if !asExit(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	if code != 130 {
		t.Errorf("exit = %d, want 130 (interrupted, not the child's 2); stderr=%s", code, errb.String())
	}
}

// readyWriter closes ready once the output contains "ready".
type readyWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	once  sync.Once
	ready chan struct{}
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if strings.Contains(w.buf.String(), "ready") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(p), nil
}
