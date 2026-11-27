package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/receipt"
	"github.com/rune-task-runner/rune/mcpserver"
)

// reuseRunefile has a [reuse] task whose body counts its runs in a file git
// ignores, so a run does not change the snapshot.
const reuseRunefile = "set receipts\n\n" +
	"[reuse]\ncheck:\n    @echo x >> ran.log\n\n" +
	"plain:\n    @echo x >> ran.log\n"

func reuseProject(t *testing.T, runefile string) string {
	t.Helper()
	return receiptProject(t, runefile, map[string]string{".gitignore": "*.log\n"}, true)
}

// publicKeyFile writes pub to a file outside the project and returns its
// path.
func publicKeyFile(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted.pub")
	if err := os.WriteFile(path, receipt.PublicPEM(pub), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// otherKeyFile is a trusted key that signed nothing.
func otherKeyFile(t *testing.T) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	return publicKeyFile(t, key.Public().(ed25519.PublicKey))
}

func runs(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "ran.log"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// recorded runs task once with receipts and returns the trusted key file of
// the local key that signed it.
func recorded(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := runReceipts(t, dir, Options{}, args...); err != nil {
		t.Fatal(err)
	}
	return publicKeyFile(t, localPublicKey(t))
}

func TestNewReuser(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	repo, err := receipt.OpenRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	signer := &receiptSigner{repo: repo}
	keyFile := otherKeyFile(t)

	if u, err := newReuser(Options{}, true, signer, false); u != nil || err != nil {
		t.Fatalf("no keys: %v %v", u, err)
	}
	u, err := newReuser(Options{ReuseKeys: []string{keyFile, keyFile}}, true, signer, true)
	if err != nil || len(u.keys) != 2 || u.maxAge != DefaultReuseMaxAge || !u.overridden {
		t.Fatalf("reuser %+v %v", u, err)
	}
	if u, _ := newReuser(Options{ReuseKeys: []string{keyFile}, ReuseMaxAge: time.Hour}, true, signer, false); u.maxAge != time.Hour {
		t.Fatalf("max age %v", u.maxAge)
	}
	for name, c := range map[string]struct {
		on     bool
		signer *receiptSigner
		files  []string
		want   string
	}{
		"receipts off":  {false, nil, []string{keyFile}, `--reuse-key: the Runefile does not set receipts, so no receipt can be reused; add "set receipts"`},
		"no repository": {true, &receiptSigner{}, []string{keyFile}, "--reuse-key: no git repository could be opened for the Runefile, so no receipt can match its files"},
		"bad key file":  {true, signer, []string{filepath.Join(dir, "missing.pub")}, "--reuse-key " + filepath.Join(dir, "missing.pub") + ": "},
	} {
		_, err := newReuser(Options{ReuseKeys: c.files}, c.on, c.signer, false)
		var usage *UsageError
		if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The key this machine signs with is never trusted for reuse unless given.
func TestReuseTrustsOnlyGivenKeys(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	recorded(t, dir, "check")
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{otherKeyFile(t)}}, "check")
	if err != nil {
		t.Fatal(err)
	}
	if runs(t, dir) != 2 || !strings.Contains(stderr, "not reused: check (signed by an untrusted key SHA256:") {
		t.Fatalf("runs %d stderr %q", runs(t, dir), stderr)
	}
}

func TestReuseSkipsTheBody(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	keyFile := recorded(t, dir, "check")
	first := readReceipts(t, dir)[0]
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil {
		t.Fatal(err)
	}
	want := "reused: check (receipt " + first.ID + ", signed by " + receipt.KeyID(localPublicKey(t)) + ")\n"
	if runs(t, dir) != 1 || stderr != want {
		t.Fatalf("runs %d stderr %q, want %q", runs(t, dir), stderr, want)
	}
	var second receipt.Receipt
	for _, rc := range readReceipts(t, dir) {
		if rc.ID != first.ID {
			second = rc
		}
	}
	if second.Status != receipt.StatusCached || second.ExitCode != nil || second.Related == nil || second.Related.Reuses != first.ID {
		t.Fatalf("receipt of the reuse: %+v", second)
	}
	if second.Source == nil || second.Source.Before != first.Source.Before || second.Source.After != first.Source.Before {
		t.Fatalf("snapshot of the reuse: %+v", second.Source)
	}
}

func TestReuseNeedsTheFlagAndTheAttribute(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	keyFile := recorded(t, dir, "check")
	stderr, err := runReceipts(t, dir, Options{}, "check")
	if err != nil || runs(t, dir) != 2 || strings.Contains(stderr, "reuse") {
		t.Fatalf("without --reuse-key: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
	recorded(t, dir, "plain")
	stderr, err = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "plain")
	if err != nil || runs(t, dir) != 4 || strings.Contains(stderr, "reuse") {
		t.Fatalf("without [reuse]: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestReuseWithCache(t *testing.T) {
	src := "set receipts\n\n[reuse, cache(inputs=[\"in.txt\"])]\ncheck:\n    @echo x >> ran.log\n"
	dir := receiptProject(t, src, map[string]string{".gitignore": "*.log\n", "in.txt": "a\n"}, true)
	keyFile := recorded(t, dir, "check")
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 1 || !strings.Contains(stderr, "cached: check") || strings.Contains(stderr, "reuse") {
		t.Fatalf("a current cache: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".rune", "cache")); err != nil {
		t.Fatal(err)
	}
	stderr, err = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 1 || !strings.Contains(stderr, "reused: check") || strings.Contains(stderr, "running:") {
		t.Fatalf("after a cache miss: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestReuseDependencyAndDependent(t *testing.T) {
	src := "set receipts\n\n[reuse]\nbuild:\n    @echo b >> ran.log\n\n[reuse]\ntest: build\n    @echo t >> ran.log\n"
	dir := reuseProject(t, src)
	keyFile := recorded(t, dir, "test")
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "test")
	if err != nil || runs(t, dir) != 2 || !strings.Contains(stderr, "reused: build (") || !strings.Contains(stderr, "reused: test (") {
		t.Fatalf("runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestReusePostHookAndFailureHook(t *testing.T) {
	src := "set receipts\n\n[reuse]\nnotify:\n    @echo n >> ran.log\n\ndeploy: && notify\n    @echo d >> ran.log\n\n" +
		"[reuse]\ndiag:\n    @echo g >> ran.log\n\nbroken: || diag\n    @exit 3\n"
	dir := reuseProject(t, src)
	keyFile := recorded(t, dir, "deploy")
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "deploy")
	if err != nil || runs(t, dir) != 3 || !strings.Contains(stderr, "reused: notify (") {
		t.Fatalf("post-hook: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
	for range 2 {
		_, _ = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "broken")
	}
	if runs(t, dir) != 5 {
		t.Fatalf("a failure hook is never reused: runs %d", runs(t, dir))
	}
	// Nor does its run stand in for a run of the task: a hook's body ran
	// with the failure's variables.
	stderr, err = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "diag")
	if err != nil || runs(t, dir) != 6 || !strings.Contains(stderr, "not reused: diag (it ran as a failure hook)") {
		t.Fatalf("diag: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestReuseDryRun(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	keyFile := recorded(t, dir, "check")
	stderr, err := runReceipts(t, dir, Options{DryRun: true, ReuseKeys: []string{keyFile}}, "check")
	if err != nil || !strings.Contains(stderr, "would run: check") || strings.Contains(stderr, "reuse") {
		t.Fatalf("stderr %q %v", stderr, err)
	}
}

func TestReuseRunefileChangedAfterLoad(t *testing.T) {
	src := "set receipts\n\nedit:\n    @echo '# edited' >> Runefile\n\n[reuse]\ncheck: edit\n    @echo x >> ran.log\n"
	dir := reuseProject(t, src)
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{otherKeyFile(t)}}, "check")
	if err != nil || runs(t, dir) != 1 || !strings.Contains(stderr, "not reused: check (the Runefile changed after it was loaded)\n") {
		t.Fatalf("runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestReuseUsageErrorsRunNothing(t *testing.T) {
	dir := reuseProject(t, "[reuse]\ncheck:\n    @echo x >> ran.log\n")
	_, err := runReceipts(t, dir, Options{ReuseKeys: []string{otherKeyFile(t)}}, "check")
	if CodeFor(err) != ExitUsage || runs(t, dir) != 0 {
		t.Fatalf("receipts off: %v, runs %d", err, runs(t, dir))
	}
}

// The agent surface never reuses, even with keys in its options.
func TestMCPCallNeverReuses(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	keyFile := recorded(t, dir, "check")
	a, _, err := loadMCPAdapter(context.Background(), Options{Stderr: &bytes.Buffer{}, Version: "1.0.0", ReuseKeys: []string{keyFile}}, filepath.Join(dir, "Runefile"))
	if err != nil {
		t.Fatal(err)
	}
	cs, _, _ := auditedServer(t, a, mcpserver.Options{}, nil)
	callWith(t, cs, "check", nil)
	if runs(t, dir) != 2 {
		t.Fatalf("runs %d", runs(t, dir))
	}
}

// reuseEngine is an engine with receipts and reuse on, over a repository
// whose snapshot cannot be taken when broken is set.
func reuseEngine(t *testing.T, broken bool) (*engine, *bytes.Buffer) {
	t.Helper()
	dir := reuseProject(t, reuseRunefile)
	repo, err := receipt.OpenRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if broken {
		repo.Top = t.TempDir() // not a repository: git fails there
	}
	var stderr bytes.Buffer
	e := availEngine(t, reuseRunefile, "linux", planRun, &bytes.Buffer{})
	e.opts.Stderr = &stderr
	e.receipts = newRecorder(&receiptSigner{repo: repo, dir: receipt.Dir(dir), root: dir}, receipt.ChannelCLI, "i", "", nil, nil)
	e.reuse = &reuser{maxAge: time.Hour}
	return e, &stderr
}

func TestTryReuseCurrentRunConditions(t *testing.T) {
	check := func(e *engine) *ast.Task { return e.tasks["check"] }

	e, stderr := reuseEngine(t, false)
	run := e.beginReceipt(check(e), eval.Args{}, receipt.RoleTask, "")
	if e.tryReuse(check(e), eval.Args{}, run, nil, errors.New("boom")) || run.snapped {
		t.Fatal("reused, or took a snapshot, without the declared inputs")
	}
	if got := stderr.String(); got != "not reused: check (the declared inputs could not be listed: boom)\n" {
		t.Fatalf("stderr %q", got)
	}

	e, stderr = reuseEngine(t, true)
	run = e.beginReceipt(check(e), eval.Args{}, receipt.RoleTask, "")
	if e.tryReuse(check(e), eval.Args{}, run, nil, nil) {
		t.Fatal("reused without a snapshot")
	}
	if got := stderr.String(); !strings.HasPrefix(got, "not reused: check (the files could not be snapshotted: ") {
		t.Fatalf("stderr %q", got)
	}

	// Nothing is tried, or printed, for a task without [reuse], without a
	// receipt run or without a reuser.
	e, stderr = reuseEngine(t, false)
	plain := e.tasks["plain"]
	if e.tryReuse(plain, eval.Args{}, e.beginReceipt(plain, eval.Args{}, receipt.RoleTask, ""), nil, nil) ||
		e.tryReuse(check(e), eval.Args{}, nil, nil, nil) || stderr.Len() != 0 {
		t.Fatalf("stderr %q", stderr.String())
	}
	e.reuse = nil
	if e.tryReuse(check(e), eval.Args{}, e.beginReceipt(check(e), eval.Args{}, receipt.RoleTask, ""), nil, nil) || stderr.Len() != 0 {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestReuseReasonText(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	with := func(reason string, f func(*receipt.Summary)) receipt.Verdict {
		s := &receipt.Summary{Signer: "SHA256:k", FinishedAt: now.Add(-48 * time.Hour)}
		if f != nil {
			f(s)
		}
		return receipt.Verdict{Task: "check", Reason: reason, Receipt: s}
	}
	cases := map[string]receipt.Verdict{
		"no receipt for these files":                    {Task: "check", Reason: receipt.ReasonNoReceipt},
		"the receipt is older than 24h0m0s":             with(receipt.ReasonStale, nil),
		"the receipt finished in the future":            with(receipt.ReasonStale, func(s *receipt.Summary) { s.FinishedAt = now.Add(time.Minute) }),
		"it ran on another OS or architecture":          with(receipt.ReasonOtherPlatform, nil),
		"it ran with another rune":                      with(receipt.ReasonOtherRune, nil),
		"the task's definition changed":                 with(receipt.ReasonOtherDefinition, nil),
		"masked arguments cannot be compared":           with(receipt.ReasonMaskedArguments, nil),
		"it ran with other arguments":                   with(receipt.ReasonOtherArguments, nil),
		"a variable was overridden on the command line": with(receipt.ReasonOverridden, nil),
		"the declared outputs are not the ones it made": with(receipt.ReasonOutputsDiffer, nil),
		"it ran as a failure hook":                      with(receipt.ReasonFailureHook, nil),
		"the run was on other files than these":         with(receipt.ReasonDifferentSnap, nil),
		"signed by an untrusted key SHA256:k":           with(receipt.ReasonUnknownSigner, nil),
		"the run failed with exit code 3":               with(receipt.ReasonFailed, func(s *receipt.Summary) { s.ExitCode = new(3) }),
	}
	for want, v := range cases {
		if got := reuseReasonText(v, now, 24*time.Hour); got != want {
			t.Errorf("%s: %q, want %q", v.Reason, got, want)
		}
	}
}

// US4: receipts in notes are found by the files, not by the commit.
func TestReuseFromNotesByTree(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(v, "t")
	}
	for _, v := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(v, "t@example.invalid")
	}
	keyFile := recorded(t, dir, "check")
	if err := ReceiptAttach(Options{Cwd: dir, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, AttachFlags{}, nil); err != nil {
		t.Fatal(err)
	}
	// A note line that is not a receipt is passed over.
	gitIn(t, dir, "notes", "--ref="+receipt.NotesRef, "append", "-m", "not a receipt", "HEAD")

	// Both in the directory and in the note: one candidate.
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 1 || !strings.Contains(stderr, "reused: check (") {
		t.Fatalf("runs %d stderr %q %v", runs(t, dir), stderr, err)
	}

	// Another commit with the same tree, and no local receipt.
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "same tree")
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	stderr, err = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 1 || !strings.Contains(stderr, "reused: check (") {
		t.Fatalf("same tree: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}

	// A commit with other files finds nothing.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "new.txt")
	gitIn(t, dir, "commit", "-qm", "other tree")
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	stderr, err = runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 2 || !strings.Contains(stderr, "not reused: check (no receipt for these files)") {
		t.Fatalf("other tree: runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

func TestNoteLinesCachedPerTree(t *testing.T) {
	dir := reuseProject(t, reuseRunefile)
	repo, err := receipt.OpenRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var warned []string
	warn := func(format string, args ...any) { warned = append(warned, format) }
	u := &reuser{}
	tree := gitOut(t, dir, "rev-parse", "HEAD^{tree}")
	if lines := u.noteLines(context.Background(), repo, tree, warn); lines != nil || len(warned) != 0 {
		t.Fatalf("no notes: %q %v", lines, warned)
	}
	gitIn(t, dir, "notes", "--ref="+receipt.NotesRef, "add", "-m", "x", "HEAD")
	if lines := u.noteLines(context.Background(), repo, tree, warn); lines != nil {
		t.Fatalf("not read again for the same tree: %q", lines)
	}
	// git that cannot run is a failure, not an absence of notes.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lines := u.noteLines(ctx, repo, "other", warn); lines != nil || len(warned) != 1 {
		t.Fatalf("a failure warns once: %q %v", lines, warned)
	}
}

// A receipts directory that cannot be read is said, and the body runs.
func TestTryReuseUnreadableReceipts(t *testing.T) {
	e, stderr := reuseEngine(t, false)
	s := e.receipts.signer
	s.dir = filepath.Join(s.root, "not-a-dir")
	if err := os.WriteFile(s.dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := e.tasks["check"]
	if e.tryReuse(task, eval.Args{}, e.beginReceipt(task, eval.Args{}, receipt.RoleTask, ""), nil, nil) {
		t.Fatal("reused without candidates")
	}
	if got := stderr.String(); !strings.HasPrefix(got, "not reused: check (read receipts: ") {
		t.Fatalf("stderr %q", got)
	}
}

// A declared input git ignores is in no snapshot: a receipt of the same
// snapshot, made when the input was absent, does not stand in for a run
// that reads it.
func TestReuseRefusesIgnoredInputNow(t *testing.T) {
	src := "set receipts\n\n[reuse, cache(inputs=[\"in.txt\", \"local.cfg\"])]\ncheck:\n    @echo x >> ran.log\n"
	dir := receiptProject(t, src, map[string]string{".gitignore": "*.log\nlocal.cfg\n", "in.txt": "a\n"}, true)
	keyFile := recorded(t, dir, "check")
	if err := os.WriteFile(filepath.Join(dir, "local.cfg"), []byte("debug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, err := runReceipts(t, dir, Options{ReuseKeys: []string{keyFile}}, "check")
	if err != nil || runs(t, dir) != 2 || !strings.Contains(stderr, "not reused: check (1 declared inputs are ignored by git)\n") {
		t.Fatalf("runs %d stderr %q %v", runs(t, dir), stderr, err)
	}
}

// A Runefile or import that no snapshot holds keeps reuse from being tried.
func TestTryReuseUncoveredRunefile(t *testing.T) {
	e, stderr := reuseEngine(t, false)
	e.receipts.signer.runefiles = []receipt.RunefileEntry{{Path: "Runefile", Covered: true}, {Path: "../shared.rune"}}
	task := e.tasks["check"]
	if e.tryReuse(task, eval.Args{}, e.beginReceipt(task, eval.Args{}, receipt.RoleTask, ""), nil, nil) {
		t.Fatal("reused with an uncovered import")
	}
	if got := stderr.String(); got != "not reused: check (the Runefile or an import is outside the repository or ignored: ../shared.rune)\n" {
		t.Fatalf("stderr %q", got)
	}
}
