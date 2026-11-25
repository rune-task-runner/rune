package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/receipt"
)

func runVerify(t *testing.T, dir string, opts Options, flags VerifyFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	opts.Cwd, opts.Stdout, opts.Stderr = dir, &out, &errb
	err = Verify(opts, flags, args)
	return out.String(), errb.String(), err
}

const verifyRunefile = "set receipts\n\nbuild:\n    @echo built\n\ntest: build\n    @echo ok\n\nbad:\n    @exit 2\n\nfmt:\n    @echo x >> a.txt\n"

func verifiedProject(t *testing.T) string {
	t.Helper()
	dir := receiptProject(t, verifyRunefile, map[string]string{"a.txt": "a\n"}, true)
	if _, err := runReceipts(t, dir, Options{}, "test"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func short(commit string) string { return commit[:7] }

func TestVerifyPassedExact(t *testing.T) {
	dir := verifiedProject(t)
	head := gitOut(t, dir, "rev-parse", "HEAD")
	key := receipt.KeyID(localPublicKey(t))
	stdout, stderr, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "test", "build")
	if err != nil {
		t.Fatalf("err %v\n%s%s", err, stdout, stderr)
	}
	want := "test: passed at " + short(head) + " (cli, signed by " + key + ")\n" +
		"build: passed at " + short(head) + " (cli, signed by " + key + ")\n" +
		"working tree: matches " + short(head) + "\n"
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestVerifyReasonsAndWorkingTree(t *testing.T) {
	dir := verifiedProject(t)
	head := gitOut(t, dir, "rev-parse", "HEAD")
	_, _ = runReceipts(t, dir, Options{}, "bad")
	_, _ = runReceipts(t, dir, Options{}, "fmt")
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "lint", "bad", "fmt")
	var failure *CommandFailure
	if !errors.As(err, &failure) || CodeFor(err) != 1 || err.Error() != "verify: 3 of 3 tasks not verified at "+short(head) {
		t.Fatalf("err %v", err)
	}
	for _, want := range []string{
		"lint: not verified: no receipt for " + short(head) + "\n",
		"bad: not verified: the run failed with exit code 2 (receipt ",
		"fmt: not verified: files changed during the run (receipt ",
		"working tree: differs from " + short(head) + "\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestVerifyIdenticalSnapshot(t *testing.T) {
	dir := receiptProject(t, verifyRunefile, map[string]string{"a.txt": "a\n"}, true)
	first := gitOut(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build")
	if !strings.Contains(stdout, "build: not verified: the run was on other files than "+short(first)) {
		t.Fatalf("a dirty run verified at the clean commit:\n%s", stdout)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "two")
	second := gitOut(t, dir, "rev-parse", "HEAD")
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build")
	if err != nil || !strings.Contains(stdout, "build: passed on a snapshot identical to "+short(second)+" (ran at "+short(first)+" with changes, cli, signed by ") {
		t.Fatalf("err %v\n%s", err, stdout)
	}
}

func TestVerifyJSON(t *testing.T) {
	dir := verifiedProject(t)
	head := gitOut(t, dir, "rev-parse", "HEAD")
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{JSON: true}, "HEAD", "test", "lint")
	if CodeFor(err) != 1 {
		t.Fatalf("err %v", err)
	}
	var report struct {
		Revision, Commit, Tree string
		Verified               bool
		Tasks                  []receipt.Verdict
		WorkingTree            struct{ Matches bool }
		Problems               []receipt.Problem
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&report); err != nil || dec.More() {
		t.Fatalf("not one JSON document: %v\n%s", err, stdout)
	}
	if report.Revision != "HEAD" || report.Commit != head || report.Verified || !report.WorkingTree.Matches || len(report.Tasks) != 2 {
		t.Fatalf("report %+v", report)
	}
	if !report.Tasks[0].Verified || report.Tasks[0].Receipt.Channel != "cli" || !report.Tasks[0].Receipt.Exact || report.Tasks[1].Reason != receipt.ReasonNoReceipt {
		t.Fatalf("tasks %+v", report.Tasks)
	}
	if strings.Contains(stdout, `"problems"`) {
		t.Fatal("an empty list is omitted")
	}
}

func TestVerifyUsageErrors(t *testing.T) {
	dir := verifiedProject(t)
	keyFile := filepath.Join(t.TempDir(), "bad.pub")
	if err := os.WriteFile(keyFile, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		dir   string
		flags VerifyFlags
		args  []string
		want  string
	}{
		"no task":       {dir, VerifyFlags{}, []string{"HEAD"}, "rune verify needs a revision and the tasks"},
		"nothing":       {dir, VerifyFlags{}, nil, "rune verify needs a revision and the tasks"},
		"unknown rev":   {dir, VerifyFlags{}, []string{"nope", "test"}, "unknown revision"},
		"bad key file":  {dir, VerifyFlags{Keys: []string{keyFile}}, []string{"HEAD", "test"}, "--key " + keyFile},
		"missing key":   {dir, VerifyFlags{Keys: []string{keyFile + "x"}}, []string{"HEAD", "test"}, "--key "},
		"no repository": {receiptProject(t, "x:\n    @true\n", map[string]string{}, false), VerifyFlags{}, []string{"HEAD", "x"}, "not a git repository"},
		"no Runefile":   {t.TempDir(), VerifyFlags{}, []string{"HEAD", "x"}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := runVerify(t, c.dir, Options{}, c.flags, c.args...)
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a usage error with %q", err, c.want)
			}
		})
	}
}

func TestVerifyTrust(t *testing.T) {
	dir := verifiedProject(t)
	// Another machine's key: the local one is moved away.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stdout, _, _ := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "test")
	if !strings.Contains(stdout, "test: not verified: signed by an untrusted key SHA256:") {
		t.Fatalf("untrusted:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "rune")); !os.IsNotExist(err) {
		t.Fatal("verify created a key")
	}
}

func TestVerifyTrustsKeyFileAndEnvKey(t *testing.T) {
	dir := receiptProject(t, verifyRunefile, map[string]string{"a.txt": "a\n"}, true)
	ci := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	if _, err := runReceipts(t, dir, Options{ReceiptKey: privatePEM(t, ci)}, "build"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build"); CodeFor(err) != 1 {
		t.Fatalf("the CI key is not trusted by default: %v", err)
	}
	pub := filepath.Join(t.TempDir(), "ci.pub")
	if err := os.WriteFile(pub, receipt.PublicPEM(ci.Public().(ed25519.PublicKey)), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{Keys: []string{pub}}, "HEAD", "build"); err != nil {
		t.Fatalf("with --key: %v\n%s", err, stdout)
	}
	if _, _, err := runVerify(t, dir, Options{ReceiptKey: privatePEM(t, ci)}, VerifyFlags{}, "HEAD", "build"); err != nil {
		t.Fatalf("the key this machine signs with is trusted: %v", err)
	}
	// A machine with no state directory has no local key, and --key is
	// enough.
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{Keys: []string{pub}}, "HEAD", "build"); err != nil {
		t.Fatalf("no state directory, with --key: %v\n%s", err, stdout)
	}
}

func TestVerifyWritesNothing(t *testing.T) {
	dir := verifiedProject(t)
	state := func() string {
		index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
		entries, _ := os.ReadDir(receipt.Dir(dir))
		return string(index) + gitOut(t, dir, "for-each-ref") + gitOut(t, dir, "count-objects", "-v") + string(rune(len(entries)))
	}
	before := state()
	_, _, _ = runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "test")
	_, _, _ = runVerify(t, dir, Options{}, VerifyFlags{JSON: true}, "HEAD", "lint")
	if after := state(); after != before {
		t.Fatal("verify changed the repository or the receipts")
	}
}

func TestVerifyMalformedAndNote(t *testing.T) {
	dir := verifiedProject(t)
	if err := os.WriteFile(filepath.Join(receipt.Dir(dir), "junk.json"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "test")
	if !strings.Contains(stdout, "problem: .rune/receipts/junk.json: malformed\n") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	// From the note alone: the local receipts are gone.
	stored, _ := receipt.List(receipt.Dir(dir))
	var note bytes.Buffer
	for _, s := range stored {
		note.Write(s.Data)
	}
	notePath := filepath.Join(t.TempDir(), "note")
	if err := os.WriteFile(notePath, note.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "notes", "--ref="+receipt.NotesRef, "add", "-F", notePath, "HEAD")
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "test")
	if err != nil || !strings.Contains(stdout, "test: passed at ") || !strings.Contains(stdout, "problem: refs/notes/rune-receipts: malformed") {
		t.Fatalf("from the note: %v\n%s", err, stdout)
	}
}

func TestVerifyOutputsReport(t *testing.T) {
	runefile := "set receipts\n\n[cache(outputs=[\"out.txt\"])]\nbuild:\n    @echo built > out.txt\n"
	dir := receiptProject(t, runefile, map[string]string{".gitignore": "out.txt\n"}, true)
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build")
	if !strings.Contains(stdout, "outputs of build: all 1 match\n") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build")
	if err != nil || !strings.Contains(stdout, "outputs of build: out.txt changed\n") {
		t.Fatalf("err %v\n%s", err, stdout)
	}
}

func TestVerifyTaskNote(t *testing.T) {
	dir := receiptProject(t, "verify:\n    @true\n", map[string]string{}, true)
	const note = `note: "verify" is also a task of this Runefile; run it with: rune -- verify`
	_, stderr, _ := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "verify")
	if !strings.Contains(stderr, note) {
		t.Fatalf("stderr %q", stderr)
	}
	// `rune verify` alone is how a person tries to run the task.
	_, stderr, err := runVerify(t, dir, Options{}, VerifyFlags{})
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(stderr, note) {
		t.Fatalf("no arguments: %v, stderr %q", err, stderr)
	}
}

func TestVerifyClaimsInText(t *testing.T) {
	s := &receipt.Summary{Channel: "mcp", Signer: "SHA256:k", Claims: &receipt.Claims{
		Client: &receipt.Client{Name: "agent", Version: "1.0"}, Auth: "process", Env: map[string]string{"CI": "true"},
	}, Arguments: []receipt.Arg{{Name: "pkg", Values: []string{"./..."}}}}
	if got := receiptDetail(s); got != "with pkg=./..., mcp, client agent 1.0, auth process, CI=true, signed by SHA256:k" {
		t.Fatalf("detail %q", got)
	}
}

func privatePEM(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestReasonText(t *testing.T) {
	s := &receipt.Summary{Signer: "SHA256:k", ExitCode: new(4), Reuses: "r1", Uncovered: "../x.rune", UncoveredInputs: 2}
	bare := &receipt.Summary{}
	cases := []struct {
		reason string
		sum    *receipt.Summary
		want   string
	}{
		{receipt.ReasonUnknownSigner, s, "signed by an untrusted key SHA256:k"},
		{receipt.ReasonBadSignature, s, "the signature does not match the receipt"},
		{receipt.ReasonUnsupported, s, "its format version or status is not supported by this rune"},
		{receipt.ReasonFailed, s, "the run failed with exit code 4"},
		{receipt.ReasonFailed, bare, "the run failed"},
		{receipt.ReasonInterrupted, s, "the run was interrupted"},
		{receipt.ReasonRefused, s, "the body did not run: it was refused"},
		{receipt.ReasonCached, s, "a cache hit, not a run (reuses receipt r1)"},
		{receipt.ReasonCached, bare, "a cache hit, not a run"},
		{receipt.ReasonIncomplete, s, "the run was not fully observed"},
		{receipt.ReasonNoSnapshot, s, "the run was not in a git repository"},
		{receipt.ReasonChangedDuringRun, s, "files changed during the run"},
		{receipt.ReasonDifferentSnap, s, "the run was on other files than abc1234"},
		{receipt.ReasonUncoveredRunefile, s, "the Runefile or an import is outside the repository or ignored: ../x.rune"},
		{receipt.ReasonUncoveredInputs, s, "2 declared inputs are ignored by git"},
		{"something-new", s, "something-new"},
	}
	for _, c := range cases {
		if got := reasonText(receipt.Verdict{Reason: c.reason, Receipt: c.sum}, "abc1234"); got != c.want {
			t.Errorf("%s: %q, want %q", c.reason, got, c.want)
		}
	}
}

func TestRanAt(t *testing.T) {
	for want, s := range map[string]*receipt.Summary{
		"ran before the first commit": {},
		"ran at 0123456 with changes": {Commit: "0123456789", Dirty: true},
		"ran at 0123456":              {Commit: "0123456789"},
	} {
		if got := ranAt(s); got != want {
			t.Errorf("ranAt(%+v) = %q, want %q", s, got, want)
		}
	}
}

func TestShortCommit(t *testing.T) {
	if shortCommit("0123456789") != "0123456" || shortCommit("abc") != "abc" {
		t.Fatal("shortCommit")
	}
}
