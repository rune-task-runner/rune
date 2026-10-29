//go:build !windows

package integration

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// runWithPTY runs the binary with stdin on a pseudo-terminal and stdout/stderr
// on pipes, then types answer. It is the only way to prove the real binary
// still treats an interactive stdin as one a person can answer (spec 027
// SC-002); Windows has no pty here and relies on the shared isTTY helper.
func runWithPTY(t *testing.T, dir, answer string, args ...string) result {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("open pseudo-terminal: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runeBin, args...)
	cmd.Dir = dir
	cmd.Stdin = tty
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start %v: %v", args, err)
	}
	// The child holds its own copy of the terminal.
	_ = tty.Close()
	// The line discipline buffers the answer until the child reads it.
	if _, err := io.WriteString(ptmx, answer); err != nil {
		t.Fatalf("type answer: %v", err)
	}
	// Drain the terminal's echo so the child never blocks writing it; the
	// deferred ptmx.Close ends this copy once the child is gone.
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	code := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if !asExit(err, &ee) {
			t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

func TestConfirmTerminalApproves(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	r := runWithPTY(t, dir, "y\n", "--color=never", "apply")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "would-apply") {
		t.Errorf("body did not run after y: stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "Apply to live infrastructure? [y/N] ") {
		t.Errorf("prompt missing: %q", r.stderr)
	}
}

func TestConfirmTerminalDeclines(t *testing.T) {
	dir := writeRunefile(t, confirmRunefile)
	r := runWithPTY(t, dir, "n\n", "--color=never", "apply")
	if r.code != 1 {
		t.Errorf("exit = %d, want 1\nstderr: %s", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, "would-apply") {
		t.Errorf("body ran after n: %q", r.stdout)
	}
	if strings.Contains(r.stderr, "rune:") {
		t.Errorf("an interactive decline stays silent, got a banner: %q", r.stderr)
	}
}

// Moved from status_styling_test.go (014 US2, C3): since spec 027 the prompt
// only appears when stdin is a terminal.
func TestConfirmPromptStyled(t *testing.T) {
	src := "[confirm]\ndanger:\n    @echo done\n"
	dir := writeRunefile(t, src)

	styled := runWithPTY(t, dir, "n\n", "--color=always", "danger")
	plain := runWithPTY(t, dir, "n\n", "--color=never", "danger")
	if styled.code != plain.code {
		t.Fatalf("exit diverged: styled=%d plain=%d", styled.code, plain.code)
	}
	if !strings.Contains(plain.stderr, `Run "danger"? [y/N] `) {
		t.Fatalf("prompt shape changed: %q", plain.stderr)
	}
	if !hasANSI(styled.stderr) {
		t.Errorf("confirm prompt should carry ANSI: %q", styled.stderr)
	}
	if got := stripANSI(styled.stderr); got != plain.stderr {
		t.Errorf("styled prompt (stripped) != plain:\n stripped=%q\n plain=%q", got, plain.stderr)
	}
	// The [y/N] hint stays plain: no escape may open after the prompt text.
	if !strings.HasSuffix(styled.stderr, " [y/N] ") {
		t.Errorf("[y/N] hint should be plain at the end of the prompt: %q", styled.stderr)
	}
	// Declining still refuses to run.
	if strings.Contains(styled.stdout, "done") || strings.Contains(plain.stdout, "done") {
		t.Errorf("task ran despite declined confirm")
	}
}
