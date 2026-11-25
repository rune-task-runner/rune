//go:build unix

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// SC-005: a body an agent calls through a server that confines it sees the
// local receipt key directory empty, while the server signs with the key in
// it.
func TestSandboxReceiptKeyHidden(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "set receipts\n\nlook:\n    @ls -A \"$HOME/.local/state/rune/receipts\" | wc -l\n")
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "look", nil)
	if res.exit != 0 || res.stdout != "0\n" {
		t.Fatalf("the body saw the key directory: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local", "state", "rune", "receipts", "key.pem")); err != nil {
		t.Fatalf("the server has no key: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.dir, ".rune", "receipts")); len(entries) < 2 {
		t.Fatalf("no receipt was written: %v", entries)
	}
}
