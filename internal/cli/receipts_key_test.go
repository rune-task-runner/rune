package cli

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/receipt"
)

func runReceiptKey(t *testing.T, dir string, opts Options) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	opts.Cwd, opts.Stdout, opts.Stderr = dir, &out, &errb
	err = ReceiptKey(opts)
	return out.String(), errb.String(), err
}

// FR-022: the CI key never reaches a body. cmd/rune takes the variable out
// of the environment; here it is only in the options, and a body that
// prints its environment does not see it.
func TestReceiptKeyNotInBodyEnvironment(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nshow:\n    @env > env.txt\n", map[string]string{".gitignore": "env.txt\n"}, true)
	ci := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	pem := privatePEM(t, ci)
	if _, err := runReceipts(t, dir, Options{ReceiptKey: pem}, "show"); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "RUNE_RECEIPT_KEY") || strings.Contains(string(env), "PRIVATE KEY") {
		t.Fatal("the body saw the signing key")
	}
	list := readReceipts(t, dir, ci.Public().(ed25519.PublicKey))
	if len(list) != 1 {
		t.Fatalf("%d receipts signed by the CI key", len(list))
	}
}

func TestReceiptKeyCommand(t *testing.T) {
	dir := receiptProject(t, "x:\n    @true\n", map[string]string{}, true)
	stdout, stderr, err := runReceiptKey(t, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	keys, perr := receipt.ParsePublicKeys([]byte(stdout))
	if perr != nil || len(keys) != 1 || !keys[0].Equal(localPublicKey(t)) {
		t.Fatalf("stdout is not the local public key: %v\n%s", perr, stdout)
	}
	if !strings.Contains(stderr, "receipts: created signing key ") || !strings.Contains(stderr, "key "+receipt.KeyID(keys[0])+" (local: ") {
		t.Fatalf("stderr %q", stderr)
	}
	_, stderr, _ = runReceiptKey(t, dir, Options{})
	if strings.Contains(stderr, "created") {
		t.Fatalf("created twice: %q", stderr)
	}

	ci := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, ed25519.SeedSize))
	stdout, stderr, err = runReceiptKey(t, dir, Options{ReceiptKey: privatePEM(t, ci)})
	if err != nil || !strings.Contains(stderr, "(RUNE_RECEIPT_KEY)") || stdout != string(receipt.PublicPEM(ci.Public().(ed25519.PublicKey))) {
		t.Fatalf("CI key: %v %q %q", err, stdout, stderr)
	}
	_, _, err = runReceiptKey(t, dir, Options{ReceiptKey: "junk"})
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("an unusable key: %v", err)
	}
}

func TestReceiptKeyTaskNote(t *testing.T) {
	dir := receiptProject(t, "receipt:\n    @true\n", map[string]string{}, true)
	_, stderr, _ := runReceiptKey(t, dir, Options{})
	if !strings.Contains(stderr, `note: "receipt" is also a task of this Runefile; run it with: rune -- receipt`) {
		t.Fatalf("stderr %q", stderr)
	}
}

func runAttach(t *testing.T, dir string, opts Options, flags AttachFlags, args ...string) (stderr string, err error) {
	t.Helper()
	var errb bytes.Buffer
	opts.Cwd, opts.Stdout, opts.Stderr = dir, &bytes.Buffer{}, &errb
	err = ReceiptAttach(opts, flags, args)
	return errb.String(), err
}

func noteLines(t *testing.T, dir, rev string) []string {
	t.Helper()
	out, err := gitOutput(dir, "notes", "--ref="+receipt.NotesRef, "show", rev)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out), err
}

func TestReceiptAttach(t *testing.T) {
	for _, name := range []string{"GIT_COMMITTER_NAME", "GIT_AUTHOR_NAME"} {
		t.Setenv(name, "t")
	}
	for _, name := range []string{"GIT_COMMITTER_EMAIL", "GIT_AUTHOR_EMAIL"} {
		t.Setenv(name, "t@example.com")
	}
	dir := receiptProject(t, verifyRunefile, map[string]string{"a.txt": "a\n"}, true)
	head := gitOut(t, dir, "rev-parse", "HEAD")
	if _, err := runReceipts(t, dir, Options{}, "test"); err != nil {
		t.Fatal(err)
	}
	_, _ = runReceipts(t, dir, Options{}, "bad") // a failure is evidence too
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, ed25519.SeedSize))
	if _, err := runReceipts(t, dir, Options{ReceiptKey: privatePEM(t, other)}, "build"); err != nil {
		t.Fatal(err)
	}
	_, _ = runReceipts(t, dir, Options{}, "fmt") // changed during its run: not bound
	gitIn(t, dir, "checkout", "-q", "--", "a.txt")

	stderr, err := runAttach(t, dir, Options{}, AttachFlags{})
	if err != nil || stderr != "attached 3 receipts to "+head[:7]+" (0 already there) in refs/notes/rune-receipts\n" {
		t.Fatalf("err %v stderr %q", err, stderr)
	}
	if n := len(noteLines(t, dir, "HEAD")); n != 3 {
		t.Fatalf("%d lines in the note", n)
	}
	pub := filepath.Join(t.TempDir(), "other.pub")
	if err := os.WriteFile(pub, receipt.PublicPEM(other.Public().(ed25519.PublicKey)), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, err = runAttach(t, dir, Options{}, AttachFlags{Keys: []string{pub}}, "HEAD")
	if err != nil || !strings.Contains(stderr, "attached 1 receipts to ") || !strings.Contains(stderr, "(3 already there)") {
		t.Fatalf("with --key: err %v stderr %q", err, stderr)
	}

	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "empty")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "b")
	gitIn(t, dir, "add", "b.txt")
	gitIn(t, dir, "commit", "-qm", "b")
	_, err = runAttach(t, dir, Options{}, AttachFlags{})
	if CodeFor(err) != 1 || !strings.Contains(err.Error(), "no receipt for ") || !strings.Contains(err.Error(), " to attach") {
		t.Fatalf("nothing bound: %v", err)
	}
	if _, err := runAttach(t, dir, Options{}, AttachFlags{}, "a", "b"); !errors.As(err, new(*UsageError)) {
		t.Fatalf("two revisions: %v", err)
	}
	if _, err := runAttach(t, dir, Options{}, AttachFlags{}, "nope"); !errors.As(err, new(*UsageError)) {
		t.Fatalf("unknown revision: %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
