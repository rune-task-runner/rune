//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A new file gets the permissions any new file gets, so a strict umask is
// honored. A file that is there keeps the ones it has.
func TestWriteFileAtomicPermissions(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	created := filepath.Join(dir, "new.json")
	if err := writeFileAtomic(created, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(created); info.Mode().Perm() != 0o600 {
		t.Errorf("a new file under umask 077 has mode %v, want 0600", info.Mode().Perm())
	}

	existing := filepath.Join(dir, "old.json")
	if err := os.WriteFile(existing, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(existing, []byte("{\"a\": 1}\n")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(existing); info.Mode().Perm() != 0o640 {
		t.Errorf("an existing file has mode %v after a write, want the 0640 it had", info.Mode().Perm())
	}
}

// A path that is not valid UTF-8 cannot be written into a JSON or TOML file
// as it is. The command says so, and does not report a bug of its own.
func TestAgentInitRunefilePathNotUTF8(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "caf\xe9")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this file system takes no name that is not UTF-8: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(agentRunefile), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "claude")
	wantErr[*UsageError](t, err, "the path of the Runefile is not valid UTF-8, so it cannot be written into a client file; register the server without it: --unpinned")
	if got := dirNames(t, dir); got != "Runefile" {
		t.Errorf("files were written: %q", got)
	}
	// Without the path in the registration the setup can be written.
	if _, stderr, err := runAgentInit(t, dir, AgentInitFlags{Unpinned: true}, "claude"); err != nil {
		t.Errorf("--unpinned: %v\n%s", err, stderr)
	}
}
