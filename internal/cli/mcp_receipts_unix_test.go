//go:build unix

package cli

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rune-task-runner/rune/internal/receipt"
)

// FR-004: a body whose sandbox could not start is refused, and its receipt
// says so, with no exit code.
func TestSandboxRefusalReceipt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, stub := confinedAdapter(t, "set receipts\n\nbuild:\n    @touch built\n")
	stub.command = func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "no-such-program"))
	}
	gitIn(t, a.root, "init", "-q")
	signer, err := newReceiptSigner(context.Background(), Options{Stderr: &bytes.Buffer{}}, filepath.Join(a.root, "Runefile"), a.loaded, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = signer
	_, _ = a.Call(context.Background(), "build", nil)
	rc := readReceipts(t, a.root)
	if len(rc) != 1 || rc[0].Status != receipt.StatusRefused || rc[0].ExitCode != nil || rc[0].Channel != receipt.ChannelMCP {
		t.Fatalf("receipts %+v", rc)
	}
}
