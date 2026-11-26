package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/receipt"
)

// receiptProject writes a Runefile and files into a new directory, with the
// state directory of the test elsewhere. With commit, the directory is a git
// repository whose one commit holds everything.
func receiptProject(t *testing.T, runefile string, files map[string]string, commit bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(agentRunVar, "")
	t.Setenv(invocationVar, "")
	clearClaimVars(t)
	dir := t.TempDir()
	files["Runefile"] = runefile
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if commit {
		gitIn(t, dir, "init", "-q")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "init")
	}
	return dir
}

func runReceipts(t *testing.T, dir string, opts Options, args ...string) (string, error) {
	t.Helper()
	var stderr bytes.Buffer
	opts.Cwd, opts.Stdout, opts.Stderr, opts.Quiet = dir, &bytes.Buffer{}, &stderr, true
	err := Run(opts, args)
	return stderr.String(), err
}

func localPublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	dir, err := receiptKeyDir(os.Getenv, runtime.GOOS, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := receipt.LoadExisting(dir)
	if err != nil || !ok {
		t.Fatalf("no local key: %v", err)
	}
	return key.Public().(ed25519.PublicKey)
}

// readReceipts opens every receipt of the project with the trusted keys.
func readReceipts(t *testing.T, dir string, trusted ...ed25519.PublicKey) []receipt.Receipt {
	t.Helper()
	if len(trusted) == 0 {
		trusted = []ed25519.PublicKey{localPublicKey(t)}
	}
	stored, err := receipt.List(receipt.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out []receipt.Receipt
	for _, s := range stored {
		payload, _, err := receipt.Open(s.Data, trusted)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		rc, err := receipt.ParseStatement(payload)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if s.Name != rc.ID+".json" {
			t.Fatalf("file %s holds receipt %s", s.Name, rc.ID)
		}
		out = append(out, rc)
	}
	return out
}

func receiptOf(t *testing.T, list []receipt.Receipt, task string) receipt.Receipt {
	t.Helper()
	var found []receipt.Receipt
	for _, rc := range list {
		if rc.Task == task {
			found = append(found, rc)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d receipts for %s in %+v", len(found), task, list)
	}
	return found[0]
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestReceiptsForDependencyAndRoot(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @echo built\n\ntest: build\n    @echo ok\n", map[string]string{}, true)
	stderr, err := runReceipts(t, dir, Options{Version: "1.2.3", Commit: "abc"}, "test")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if strings.Count(stderr, "receipts: created signing key ") != 1 || !strings.Contains(stderr, "(SHA256:") {
		t.Fatalf("no creation notice: %s", stderr)
	}
	list := readReceipts(t, dir)
	if len(list) != 2 {
		t.Fatalf("%d receipts", len(list))
	}
	build, test := receiptOf(t, list, "build"), receiptOf(t, list, "test")
	head, tree := gitOut(t, dir, "rev-parse", "HEAD"), gitOut(t, dir, "rev-parse", "HEAD^{tree}")
	for _, rc := range []receipt.Receipt{build, test} {
		if rc.Status != receipt.StatusPassed || rc.ExitCode == nil || *rc.ExitCode != 0 {
			t.Errorf("%s: %s %v", rc.Task, rc.Status, rc.ExitCode)
		}
		if rc.Source == nil || rc.Source.Before != tree || rc.Source.After != tree || rc.Source.Commit != head || rc.Source.Dirty {
			t.Errorf("%s: source %+v, want %s at %s", rc.Task, rc.Source, tree, head)
		}
		if rc.Channel != receipt.ChannelCLI || rc.Role != receipt.RoleTask || rc.Project != "Runefile" {
			t.Errorf("%s: %s %s %s", rc.Task, rc.Channel, rc.Role, rc.Project)
		}
		if rc.Rune.Version != "1.2.3" || rc.Rune.Commit != "abc" || rc.Rune.OS != runtime.GOOS {
			t.Errorf("%s: rune %+v", rc.Task, rc.Rune)
		}
		if len(rc.Runefiles) != 1 || !rc.Runefiles[0].Covered || rc.Definition == "" || rc.Executor == "" {
			t.Errorf("%s: %+v %q %q", rc.Task, rc.Runefiles, rc.Definition, rc.Executor)
		}
	}
	if build.Invocation != test.Invocation || len(build.Invocation) != 16 {
		t.Errorf("invocations %q %q", build.Invocation, test.Invocation)
	}
	if test.Related == nil || !slices.Equal(test.Related.Dependencies, []string{build.ID}) {
		t.Errorf("test's relations %+v, want the build receipt", test.Related)
	}
	if !build.FinishedAt.After(build.StartedAt) && !build.FinishedAt.Equal(build.StartedAt) {
		t.Error("finished before it started")
	}

	stderr, err = runReceipts(t, dir, Options{}, "build")
	if err != nil || strings.Contains(stderr, "created signing key") {
		t.Fatalf("second run: %v %s", err, stderr)
	}
	if n := len(readReceipts(t, dir)); n != 3 {
		t.Fatalf("%d receipts after a second run", n)
	}
}

func TestReceiptsFailureAndHook(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\ndiag:\n    @echo diagnosing\n\nbroken: || diag\n    @exit 3\n", map[string]string{}, true)
	_, err := runReceipts(t, dir, Options{}, "broken")
	if CodeFor(err) != 3 {
		t.Fatalf("exit %d: %v", CodeFor(err), err)
	}
	list := readReceipts(t, dir)
	broken, diag := receiptOf(t, list, "broken"), receiptOf(t, list, "diag")
	if broken.Status != receipt.StatusFailed || broken.ExitCode == nil || *broken.ExitCode != 3 {
		t.Errorf("broken: %s %v", broken.Status, broken.ExitCode)
	}
	if diag.Role != receipt.RoleFailureHook || diag.Status != receipt.StatusPassed || diag.Related == nil || diag.Related.FailureOf != broken.ID {
		t.Errorf("diag: %+v", diag)
	}
}

func TestReceiptsPostHookFollows(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nnotify:\n    @echo n\n\ndeploy: && notify\n    @echo d\n", map[string]string{}, true)
	if _, err := runReceipts(t, dir, Options{}, "deploy"); err != nil {
		t.Fatal(err)
	}
	list := readReceipts(t, dir)
	deploy, notify := receiptOf(t, list, "deploy"), receiptOf(t, list, "notify")
	if notify.Related == nil || notify.Related.Follows != deploy.ID {
		t.Errorf("notify: %+v", notify.Related)
	}
}

// A task that runs after the Runefile changed runs the definitions rune
// loaded, which its snapshot does not hold: its receipt is not evidence.
func TestReceiptsRunefileChangedAfterLoad(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nedit:\n    @echo '# edited' >> Runefile\n\ntest: edit\n    @echo ok\n", map[string]string{}, true)
	if _, err := runReceipts(t, dir, Options{}, "test"); err != nil {
		t.Fatal(err)
	}
	list := readReceipts(t, dir)
	if edit := receiptOf(t, list, "edit"); edit.Status != receipt.StatusPassed || edit.Source == nil || edit.Source.Before == edit.Source.After {
		t.Errorf("edit: %s %+v", edit.Status, edit.Source)
	}
	if test := receiptOf(t, list, "test"); test.Status != receipt.StatusIncomplete {
		t.Errorf("test after the Runefile changed: %s", test.Status)
	}
}

func TestReceiptsCacheHitReuses(t *testing.T) {
	runefile := "set receipts\n\n[cache(inputs=[\"in.txt\"], outputs=[\"out.txt\"])]\nbuild:\n    @cp in.txt out.txt\n"
	dir := receiptProject(t, runefile, map[string]string{"in.txt": "x", ".gitignore": "out.txt\n"}, true)
	for range 2 {
		if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
			t.Fatal(err)
		}
	}
	list := readReceipts(t, dir)
	if len(list) != 2 {
		t.Fatalf("%d receipts", len(list))
	}
	// IDs of one second sort by their random part, so order by start time.
	slices.SortFunc(list, func(a, b receipt.Receipt) int { return a.StartedAt.Compare(b.StartedAt) })
	first, second := list[0], list[1]
	if first.Status != receipt.StatusPassed || first.Outputs == nil || first.Outputs.Count != 1 || first.Outputs.Files[0].Path != "out.txt" {
		t.Fatalf("first: %s %+v", first.Status, first.Outputs)
	}
	if second.Status != receipt.StatusCached || second.ExitCode != nil || second.Related == nil || second.Related.Reuses != first.ID {
		t.Fatalf("second: %+v", second)
	}
}

func TestReceiptsUncoveredInputs(t *testing.T) {
	runefile := "set receipts\n\n[cache(inputs=[\"vendor/*.go\", \"a.go\"])]\nbuild:\n    @true\n"
	dir := receiptProject(t, runefile, map[string]string{"vendor/x.go": "x", "a.go": "a", ".gitignore": "vendor/\n"}, true)
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if !slices.Equal(rc.UncoveredInputs, []string{"vendor/x.go"}) {
		t.Fatalf("uncovered %v", rc.UncoveredInputs)
	}
}

// A declared input the cache cannot read leaves unknown whether git ignores
// one, so the run is not fully observed.
func TestReceiptsUnlistedInputsIncomplete(t *testing.T) {
	runefile := "set receipts\n\n[cache(inputs=[\"in/*\"])]\nbuild:\n    @true\n"
	dir := receiptProject(t, runefile, map[string]string{"in/a": "a"}, false)
	if err := os.Symlink("nowhere", filepath.Join(dir, "in", "broken")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	if rc := readReceipts(t, dir)[0]; rc.Status != receipt.StatusIncomplete {
		t.Fatalf("status %s", rc.Status)
	}
}

func TestReceiptsChangedDuringRun(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nfmt:\n    @echo x >> a.txt\n", map[string]string{"a.txt": "a\n"}, true)
	if _, err := runReceipts(t, dir, Options{}, "fmt"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Source == nil || rc.Source.Before == rc.Source.After {
		t.Fatalf("source %+v", rc.Source)
	}
}

func TestReceiptsInterruptedAndRefused(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nslow:\n    @true\n\n[confirm]\ndanger:\n    @touch ran\n", map[string]string{}, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runReceipts(t, dir, Options{Ctx: ctx}, "slow"); err == nil {
		t.Fatal("a cancelled run succeeded")
	}
	if _, err := runReceipts(t, dir, Options{}, "danger"); err == nil {
		t.Fatal("an unconfirmed task ran")
	}
	if fileExists(filepath.Join(dir, "ran")) {
		t.Fatal("the refused body ran")
	}
	list := readReceipts(t, dir)
	if rc := receiptOf(t, list, "slow"); rc.Status != receipt.StatusInterrupted {
		t.Errorf("slow: %s", rc.Status)
	}
	if rc := receiptOf(t, list, "danger"); rc.Status != receipt.StatusRefused || rc.ExitCode != nil {
		t.Errorf("danger: %s %v", rc.Status, rc.ExitCode)
	}
}

func TestReceiptsOffWritesNothing(t *testing.T) {
	dir := receiptProject(t, "build:\n    @echo hi\n", map[string]string{}, true)
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, ".rune", "receipts")) {
		t.Fatal("receipts written without the setting")
	}
	keyDir, _ := receiptKeyDir(os.Getenv, runtime.GOOS, os.UserHomeDir)
	if fileExists(keyDir) {
		t.Fatal("a key was created without the setting")
	}
}

func TestReceiptsPlanModesWriteNothing(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @echo hi\n", map[string]string{}, true)
	for _, opts := range []Options{{DryRun: true}, {Summary: true}, {List: true}} {
		if _, err := runReceipts(t, dir, opts, "build"); err != nil {
			t.Fatal(err)
		}
	}
	if fileExists(filepath.Join(dir, ".rune", "receipts")) {
		t.Fatal("a plan mode wrote receipts")
	}
}

func TestReceiptsWriteFailureKeepsExitCode(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @echo hi\n\nbad:\n    @exit 4\n", map[string]string{}, true)
	if err := os.MkdirAll(filepath.Join(dir, ".rune"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rune", "receipts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, err := runReceipts(t, dir, Options{}, "build")
	if err != nil || !strings.Contains(stderr, "warning: receipt for build not written: ") {
		t.Fatalf("err %v stderr %s", err, stderr)
	}
	stderr, err = runReceipts(t, dir, Options{}, "bad")
	if CodeFor(err) != 4 || !strings.Contains(stderr, "warning: receipt for bad not written: ") {
		t.Fatalf("exit %d stderr %s", CodeFor(err), stderr)
	}
}

func TestReceiptsBadKeyRunsNothing(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @touch ran\n", map[string]string{}, true)
	_, err := runReceipts(t, dir, Options{ReceiptKey: "not a key"}, "build")
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "RUNE_RECEIPT_KEY") {
		t.Fatalf("err = %v", err)
	}
	if fileExists(filepath.Join(dir, "ran")) {
		t.Fatal("a body ran without a signer")
	}
}

func TestReceiptsOutsideRepository(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @echo hi\n", map[string]string{}, false)
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Source != nil || rc.Status != receipt.StatusPassed || rc.Project != "Runefile" || rc.Runefiles[0].Covered {
		t.Fatalf("receipt %+v", rc)
	}
}

func TestReceiptsNoCommitYet(t *testing.T) {
	dir := receiptProject(t, "set receipts\n\nbuild:\n    @echo hi\n", map[string]string{}, false)
	gitIn(t, dir, "init", "-q")
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Source == nil || rc.Source.Before == "" || rc.Source.Commit != "" || !rc.Source.Dirty {
		t.Fatalf("source %+v", rc.Source)
	}
}

func TestReceiptsSubdirectoryProject(t *testing.T) {
	dir := receiptProject(t, "x:\n    @true\n", map[string]string{"app/Runefile": "set receipts\n\nbuild:\n    @echo hi\n"}, true)
	if _, err := runReceipts(t, filepath.Join(dir, "app"), Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, filepath.Join(dir, "app"))[0]
	if rc.Project != "app/Runefile" || rc.Source.Before != gitOut(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatalf("project %q source %+v", rc.Project, rc.Source)
	}
}

// Above 64 output files the list moves to a manifest next to the receipt,
// and verify still compares every file.
func TestReceiptsOutputManifest(t *testing.T) {
	runefile := "set receipts\n\n[cache(outputs=[\"out\"])]\nbuild:\n    @mkdir -p out && for i in $(seq 1 65); do echo $i > out/f$i; done\n"
	dir := receiptProject(t, runefile, map[string]string{".gitignore": "out/\n"}, true)
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Outputs == nil || rc.Outputs.Count != 65 || rc.Outputs.Files != nil || rc.Outputs.Manifest != rc.ID+".outputs.json" {
		t.Fatalf("outputs %+v", rc.Outputs)
	}
	if !fileExists(filepath.Join(receipt.Dir(dir), rc.Outputs.Manifest)) {
		t.Fatal("no manifest file")
	}
	stdout, _, err := runVerify(t, dir, Options{}, VerifyFlags{}, "HEAD", "build")
	if err != nil || !strings.Contains(stdout, "outputs of build: all 65 match") {
		t.Fatalf("err %v\n%s", err, stdout)
	}
}

func TestEngineReceiptHelpers(t *testing.T) {
	e := &engine{}
	if e.receiptSigner() != nil {
		t.Fatal("an engine without receipts has a signer")
	}
	if got := e.maskFunc()("x"); got != "x" {
		t.Fatalf("no mask: %q", got)
	}
	e.mask = strings.ToUpper
	if got := e.maskFunc()("x"); got != "X" {
		t.Fatalf("mask: %q", got)
	}
	s := &receiptSigner{}
	e.receipts = newRecorder(s, receipt.ChannelCLI, "i", "", nil)
	if e.receiptSigner() != s {
		t.Fatal("the signer of the recorder")
	}
	if got := resolvedPath(filepath.Join(t.TempDir(), "missing")); !strings.HasSuffix(got, "missing") {
		t.Fatalf("resolvedPath of a missing path: %q", got)
	}
}

func TestWriteReceiptWithoutSubject(t *testing.T) {
	s := &receiptSigner{dir: t.TempDir()}
	if err := writeReceipt(s, receipt.Receipt{ID: "x", Task: "t", Project: "Runefile"}); err == nil {
		t.Fatal("a receipt with no snapshot and no Runefile digest was written")
	}
}
