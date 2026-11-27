package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/receipt"
)

// reuseRunefile counts the runs of its bodies in a file git ignores, so a
// run does not change the snapshot. build's output is ignored as well.
const reuseRunefile = "set receipts\n\nmode := \"debug\"\n\n" +
	"[reuse]\ncheck:\n    @echo {{mode}} >> ran.log\n\n" +
	"[reuse]\npkgs p:\n    @echo {{p}} >> ran.log\n\n" +
	"[reuse]\ndeploy token:\n    @echo deploying >> ran.log\n\n" +
	"[reuse, cache(inputs=[\"in.txt\"], outputs=[\"out.bin\"])]\nbuild:\n    @echo built > out.bin\n    @echo b >> ran.log\n"

// ciKey makes a CI signing key: the environment entry that passes it, the
// key itself and its public key in a file outside the project.
func ciKey(t *testing.T) (envEntry string, key ed25519.PrivateKey, pubFile string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pubFile = filepath.Join(t.TempDir(), "ci.pub")
	writeFile(t, pubFile, string(receipt.PublicPEM(pub)))
	return "RUNE_RECEIPT_KEY=" + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), key, pubFile
}

// reuseProject is a project with reuseRunefile whose runs sign with a CI
// key.
func reuseProject(t *testing.T) (dir string, env []string, key ed25519.PrivateKey, pubFile string) {
	t.Helper()
	dir, env = rcptProject(t, reuseRunefile, map[string]string{".gitignore": "*.log\n*.bin\n", "in.txt": "in\n"})
	entry, key, pubFile := ciKey(t)
	env[1] = entry
	return dir, env, key, pubFile
}

func ranLines(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "ran.log"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// readSigned opens every receipt of the project with pub.
func readSigned(t *testing.T, dir string, pub ed25519.PublicKey) []receipt.Receipt {
	t.Helper()
	stored, err := receipt.List(receipt.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out []receipt.Receipt
	for _, s := range stored {
		payload, _, err := receipt.Open(s.Data, []ed25519.PublicKey{pub})
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		rc, err := receipt.ParseStatement(payload)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rc)
	}
	return out
}

// US1, SC-001: a task CI already proved is not run again.
func TestReuseSkipsAProvedTask(t *testing.T) {
	dir, env, key, pubFile := reuseProject(t)
	if r := run(t, dir, env, "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	first := readSigned(t, dir, key.Public().(ed25519.PublicKey))[0]
	r := run(t, dir, env, "--reuse-key", pubFile, "check")
	want := "reused: check (receipt " + first.ID + ", signed by " + receipt.KeyID(key.Public().(ed25519.PublicKey)) + ")"
	if r.code != 0 || ranLines(t, dir) != 1 || !strings.Contains(r.stderr, want) {
		t.Fatalf("runs %d: %+v", ranLines(t, dir), r)
	}
	wantVerified(t, rcptVerify(t, dir, env, "--key", pubFile, "HEAD", "check"), "check: passed at ")

	// The receipt of the reuse is never evidence of a pass.
	if err := os.Remove(filepath.Join(receipt.Dir(dir), first.ID+".json")); err != nil {
		t.Fatal(err)
	}
	wantNotVerified(t, rcptVerify(t, dir, env, "--key", pubFile, "HEAD", "check"), "a cache hit, not a run (reuses receipt "+first.ID+")")

	// A cached receipt is never reused either: the body runs.
	if r := run(t, dir, env, "--reuse-key", pubFile, "check"); r.code != 0 || ranLines(t, dir) != 2 || !strings.Contains(r.stderr, "not reused: check (a cache hit, not a run") {
		t.Fatalf("runs %d: %+v", ranLines(t, dir), r)
	}
	if r := run(t, dir, env, "ci", "run", "--reuse-key", pubFile, "check"); r.code != 0 || ranLines(t, dir) != 2 || !strings.Contains(r.stderr, "reused: check (") {
		t.Fatalf("ci run: runs %d: %+v", ranLines(t, dir), r)
	}
}

// resignWith writes a copy of a receipt, changed, signed with key.
func resignWith(t *testing.T, dir string, key ed25519.PrivateKey, from receipt.Receipt, change func(*receipt.Receipt)) string {
	t.Helper()
	r := from
	r.ID = receipt.NewID(time.Now())
	change(&r)
	st, err := receipt.Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := receipt.Sign(key, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(receipt.Dir(dir), r.ID, line); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(receipt.Dir(dir), r.ID+".json")
}

// changeEnvelope rewrites the envelope in path.
func changeEnvelope(t *testing.T, path string, change func(env *receipt.Envelope)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env receipt.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	change(&env)
	out, _ := json.Marshal(env)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// US2, SC-002 to SC-005: evidence that does not qualify never skips a task,
// and the notice names why.
func TestReuseRefusesBadEvidence(t *testing.T) {
	dir, env, key, pubFile := reuseProject(t)
	if r := run(t, dir, env, "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	good := readSigned(t, dir, key.Public().(ed25519.PublicKey))[0]
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherPub := filepath.Join(t.TempDir(), "other.pub")
	writeFile(t, otherPub, string(receipt.PublicPEM(otherKey.Public().(ed25519.PublicKey))))

	// Each fixture leaves exactly one receipt, then tries reuse.
	resigned := func(change func(*receipt.Receipt)) func() {
		return func() { resignWith(t, dir, key, good, change) }
	}
	cases := []struct {
		name, want string
		fixture    func()
		keyFile    string
		args       []string
	}{
		{"changed payload", "the signature does not match the receipt", func() {
			path := resignWith(t, dir, key, good, func(*receipt.Receipt) {})
			changeEnvelope(t, path, func(e *receipt.Envelope) {
				payload, _ := base64.StdEncoding.DecodeString(e.Payload)
				e.Payload = base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(payload), `"durationMs":`, `"durationMs":1`, 1)))
			})
		}, "", nil},
		{"changed signature", "the signature does not match the receipt", func() {
			path := resignWith(t, dir, key, good, func(*receipt.Receipt) {})
			changeEnvelope(t, path, func(e *receipt.Envelope) {
				sig, _ := base64.StdEncoding.DecodeString(e.Signatures[0].Sig)
				sig[0] ^= 1
				e.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
			})
		}, "", nil},
		{"untrusted key", "signed by an untrusted key ", func() { resignWith(t, dir, otherKey, good, func(*receipt.Receipt) {}) }, "", nil},
		{"the CI key not given", "signed by an untrusted key ", resigned(func(*receipt.Receipt) {}), otherPub, nil},
		{"older than the maximum age", "the receipt is older than 24h0m0s", resigned(func(r *receipt.Receipt) { r.FinishedAt = time.Now().Add(-25 * time.Hour) }), "", nil},
		{"finished in the future", "the receipt finished in the future", resigned(func(r *receipt.Receipt) { r.FinishedAt = time.Now().Add(time.Hour) }), "", nil},
		{"failed", "the run failed with exit code 1", resigned(func(r *receipt.Receipt) { r.Status, r.ExitCode = receipt.StatusFailed, new(1) }), "", nil},
		{"interrupted", "the run was interrupted", resigned(func(r *receipt.Receipt) { r.Status = receipt.StatusInterrupted }), "", nil},
		{"refused", "the body did not run: it was refused", resigned(func(r *receipt.Receipt) { r.Status = receipt.StatusRefused }), "", nil},
		{"cached", "a cache hit, not a run", resigned(func(r *receipt.Receipt) { r.Status = receipt.StatusCached }), "", nil},
		{"incomplete", "the run was not fully observed", resigned(func(r *receipt.Receipt) { r.Status = receipt.StatusIncomplete }), "", nil},
		{"changed during the run", "files changed during the run", resigned(func(r *receipt.Receipt) {
			r.Source = &receipt.Source{Commit: r.Source.Commit, Before: r.Source.Before, After: strings.Repeat("0", 40)}
		}), "", nil},
		{"uncovered Runefile", "the Runefile or an import is outside the repository or ignored", resigned(func(r *receipt.Receipt) {
			r.Runefiles = append(append([]receipt.RunefileEntry(nil), r.Runefiles...), receipt.RunefileEntry{Path: "../x.rune"})
		}), "", nil},
		{"uncovered inputs", "1 declared inputs are ignored by git", resigned(func(r *receipt.Receipt) { r.UncoveredInputs = []string{"vendor/a"} }), "", nil},
		{"other OS", "it ran on another OS or architecture", resigned(func(r *receipt.Receipt) { r.Rune.OS = "plan9" }), "", nil},
		{"other architecture", "it ran on another OS or architecture", resigned(func(r *receipt.Receipt) { r.Rune.Arch = "mips" }), "", nil},
		{"other rune", "it ran with another rune", resigned(func(r *receipt.Receipt) { r.Rune.Version = "0.0.1" }), "", nil},
		{"other definition", "the task's definition changed", resigned(func(r *receipt.Receipt) { r.Definition = "other" }), "", nil},
		{"masked arguments in the receipt", "masked arguments cannot be compared", resigned(func(r *receipt.Receipt) { r.ArgumentsRedacted = true }), "", nil},
		{"overridden in the receipt", "a variable was overridden on the command line", resigned(func(r *receipt.Receipt) { r.Overrides = []string{"mode"} }), "", nil},
		{"overridden now", "a variable was overridden on the command line", resigned(func(*receipt.Receipt) {}), "", []string{"mode=release", "check"}},
		{"another project", "no receipt for these files", resigned(func(r *receipt.Receipt) { r.Project = "sub/Runefile" }), "", nil},
		{"other files", "the run was on other files than these", func() {
			resignWith(t, dir, key, good, func(*receipt.Receipt) {})
			writeFile(t, filepath.Join(dir, "extra.txt"), "x\n")
		}, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Remove(filepath.Join(dir, "extra.txt")) }()
			c.fixture()
			keyFile := pubFile
			if c.keyFile != "" {
				keyFile = c.keyFile
			}
			args := c.args
			if args == nil {
				args = []string{"check"}
			}
			before := ranLines(t, dir)
			r := run(t, dir, env, append([]string{"--reuse-key", keyFile}, args...)...)
			if r.code != 0 || ranLines(t, dir) != before+1 || !strings.Contains(r.stderr, "not reused: check ("+c.want) {
				t.Fatalf("runs %d -> %d: %+v", before, ranLines(t, dir), r)
			}
		})
	}
}

// US2: what this run is, compared with what the receipt's run was.
func TestReuseComparesTheRun(t *testing.T) {
	dir, env, _, pubFile := reuseProject(t)
	reuse := func(args ...string) result {
		return run(t, dir, env, append([]string{"--reuse-key", pubFile}, args...)...)
	}

	// The machine's own local key is not trusted for reuse.
	local := []string{env[0], "RUNE_RECEIPT_KEY="}
	if r := run(t, dir, local, "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, dir, local, "--reuse-key", pubFile, "check"); !strings.Contains(r.stderr, "not reused: check (signed by an untrusted key ") || ranLines(t, dir) != 2 {
		t.Fatalf("local key: %+v", r)
	}

	// Other arguments.
	if r := run(t, dir, env, "pkgs", "a"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := reuse("pkgs", "b"); !strings.Contains(r.stderr, "not reused: pkgs (it ran with other arguments)") {
		t.Fatalf("other arguments: %+v", r)
	}
	if r := reuse("pkgs", "a"); !strings.Contains(r.stderr, "reused: pkgs (") {
		t.Fatalf("the same arguments: %+v", r)
	}

	// An override recorded by the receipt.
	if r := run(t, dir, env, "mode=release", "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := reuse("check"); !strings.Contains(r.stderr, "not reused: check (a variable was overridden on the command line)") {
		t.Fatalf("override in the receipt: %+v", r)
	}

	// A changed [cache] output.
	if r := run(t, dir, env, "build"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	writeFile(t, filepath.Join(dir, "out.bin"), "changed\n")
	if err := os.RemoveAll(filepath.Join(dir, ".rune", "cache")); err != nil {
		t.Fatal(err)
	}
	if r := reuse("build"); !strings.Contains(r.stderr, "not reused: build (the declared outputs are not the ones it made)") {
		t.Fatalf("changed output: %+v", r)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".rune", "cache")); err != nil {
		t.Fatal(err)
	}
	if r := reuse("build"); !strings.Contains(r.stderr, "reused: build (") {
		t.Fatalf("the outputs it made: %+v", r)
	}
}

// SC-008: a secret argument is masked, so it is never compared and never
// printed or recorded.
func TestReuseKeepsSecrets(t *testing.T) {
	const secret = "s3cr3t-reuse-fixture"
	dir, env, _, pubFile := reuseProject(t)
	env = append(env, "DEPLOY_TOKEN="+secret)
	if r := run(t, dir, env, "deploy", secret); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r := run(t, dir, env, "--reuse-key", pubFile, "deploy", secret)
	if !strings.Contains(r.stderr, "not reused: deploy (masked arguments cannot be compared)") || ranLines(t, dir) != 2 {
		t.Fatalf("%+v", r)
	}
	var stored strings.Builder
	files, _ := os.ReadDir(receipt.Dir(dir))
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(receipt.Dir(dir), f.Name()))
		stored.Write(data)
		stored.WriteString(decodePayloads(data))
	}
	if strings.Contains(r.stdout+r.stderr, secret) || strings.Contains(stored.String(), secret) {
		t.Fatal("the secret left in a notice or a receipt")
	}
}

// US4, SC-007: a pull request's receipts count after a merge to another
// commit with the same files.
func TestReuseAcrossCommitsThroughNotes(t *testing.T) {
	dir, env, _, pubFile := reuseProject(t)
	env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if r := run(t, dir, env, "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, dir, env, "receipt", "attach", "--key", pubFile); r.code != 0 {
		t.Fatalf("attach: %+v", r)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	rcptGit(t, dir, "init", "-q", "--bare", remote)
	rcptGit(t, dir, "push", "-q", remote, "HEAD:refs/heads/main", "refs/notes/rune-receipts")

	// The merge: another commit with exactly the same tree.
	clone := filepath.Join(t.TempDir(), "clone")
	rcptGit(t, dir, "clone", "-q", "-b", "main", remote, clone)
	rcptGit(t, clone, "commit", "-q", "--allow-empty", "-m", "merge")
	rcptGit(t, clone, "fetch", "-q", "origin", "refs/notes/rune-receipts:refs/notes/rune-receipts")
	elsewhere := []string{"XDG_STATE_HOME=" + t.TempDir(), "RUNE_RECEIPT_KEY="}
	r := run(t, clone, elsewhere, "--reuse-key", pubFile, "check")
	if r.code != 0 || !strings.Contains(r.stderr, "reused: check (") || ranLines(t, clone) != 0 {
		t.Fatalf("same tree: runs %d: %+v", ranLines(t, clone), r)
	}

	// A commit with other files finds nothing in the notes.
	writeFile(t, filepath.Join(clone, "new.txt"), "n\n")
	rcptGit(t, clone, "add", "new.txt")
	rcptGit(t, clone, "commit", "-qm", "other files")
	if err := os.RemoveAll(receipt.Dir(clone)); err != nil {
		t.Fatal(err)
	}
	r = run(t, clone, elsewhere, "--reuse-key", pubFile, "check")
	if r.code != 0 || !strings.Contains(r.stderr, "not reused: check (no receipt for these files)") || ranLines(t, clone) != 1 {
		t.Fatalf("other tree: runs %d: %+v", ranLines(t, clone), r)
	}
}

// US4: the pull request's merge preview is never pushed, so a clone of main
// has the note but not the commit it annotates; the squash commit with the
// same files still reuses its receipts.
func TestReuseFromANoteOnAnUnfetchedCommit(t *testing.T) {
	dir, env, _, pubFile := reuseProject(t)
	env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	base := rcptGit(t, dir, "rev-parse", "HEAD")
	rcptGit(t, dir, "checkout", "-q", "--detach")
	writeFile(t, filepath.Join(dir, "feature.txt"), "f\n")
	rcptGit(t, dir, "add", "feature.txt")
	rcptGit(t, dir, "commit", "-qm", "merge preview")
	preview := rcptGit(t, dir, "rev-parse", "HEAD")
	if r := run(t, dir, env, "check"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, dir, env, "receipt", "attach", "--key", pubFile); r.code != 0 {
		t.Fatalf("attach: %+v", r)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	rcptGit(t, dir, "init", "-q", "--bare", remote)
	rcptGit(t, dir, "push", "-q", remote, base+":refs/heads/main", "refs/notes/rune-receipts")

	clone := filepath.Join(t.TempDir(), "clone")
	rcptGit(t, dir, "clone", "-q", "-b", "main", remote, clone)
	writeFile(t, filepath.Join(clone, "feature.txt"), "f\n")
	rcptGit(t, clone, "add", "feature.txt")
	rcptGit(t, clone, "commit", "-qm", "squash")
	rcptGit(t, clone, "fetch", "-q", "origin", "refs/notes/rune-receipts:refs/notes/rune-receipts")
	if rcptGit(t, clone, "rev-parse", "HEAD^{tree}") != rcptGit(t, dir, "rev-parse", preview+"^{tree}") {
		t.Fatal("the squash commit does not have the preview's tree")
	}
	if out, err := exec.Command("git", "-C", clone, "cat-file", "-e", preview).CombinedOutput(); err == nil {
		t.Fatalf("the clone has the preview commit: %s", out)
	}
	elsewhere := []string{"XDG_STATE_HOME=" + t.TempDir(), "RUNE_RECEIPT_KEY="}
	r := run(t, clone, elsewhere, "--reuse-key", pubFile, "check")
	if r.code != 0 || !strings.Contains(r.stderr, "reused: check (") || ranLines(t, clone) != 0 {
		t.Fatalf("runs %d: %+v", ranLines(t, clone), r)
	}
}
