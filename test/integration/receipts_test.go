package integration

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/receipt"
)

// rcptProject makes a git repository holding a Runefile and files in one
// commit, and the environment of a run with its own state directory.
func rcptProject(t *testing.T, runefile string, files map[string]string) (dir string, env []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	files["Runefile"] = runefile
	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}
	rcptGit(t, dir, "init", "-q")
	rcptGit(t, dir, "add", "-A")
	rcptGit(t, dir, "commit", "-qm", "init")
	return dir, []string{"XDG_STATE_HOME=" + t.TempDir(), "RUNE_RECEIPT_KEY="}
}

func rcptGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// rcptKey is the local key of a run's environment.
func rcptKey(t *testing.T, env []string) ed25519.PrivateKey {
	t.Helper()
	state := strings.TrimPrefix(env[0], "XDG_STATE_HOME=")
	key, ok, err := receipt.LoadExisting(filepath.Join(state, "rune", "receipts"))
	if err != nil || !ok {
		t.Fatalf("no local key: %v", err)
	}
	return key
}

// rcptRead opens every receipt of the project with the run's local key.
func rcptRead(t *testing.T, dir string, env []string) []receipt.Receipt {
	t.Helper()
	pub := rcptKey(t, env).Public().(ed25519.PublicKey)
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

func TestReceiptsRecordPassAndFail(t *testing.T) {
	dir, env := rcptProject(t, "set receipts\n\nok:\n    @echo fine\n\nbad:\n    @exit 5\n", map[string]string{})
	if r := run(t, dir, env, "ok"); r.code != 0 || !strings.Contains(r.stderr, "receipts: created signing key ") {
		t.Fatalf("ok: %+v", r)
	}
	if r := run(t, dir, env, "bad"); r.code != 5 {
		t.Fatalf("bad: %+v", r)
	}
	tree := rcptGit(t, dir, "rev-parse", "HEAD^{tree}")
	got := map[string]receipt.Receipt{}
	for _, rc := range rcptRead(t, dir, env) {
		got[rc.Task] = rc
	}
	if rc := got["ok"]; rc.Status != receipt.StatusPassed || rc.Source == nil || rc.Source.Before != tree || rc.Source.After != tree {
		t.Errorf("ok: %+v", rc)
	}
	if rc := got["bad"]; rc.Status != receipt.StatusFailed || rc.ExitCode == nil || *rc.ExitCode != 5 {
		t.Errorf("bad: %+v", rc)
	}
}

// SC-008: two processes at once, each with tasks in parallel.
func TestReceiptsConcurrentRuns(t *testing.T) {
	runefile := "set receipts\n\n[parallel]\nall: a b c\n    @true\n\na:\n    @echo a\n\nb:\n    @echo b\n\nc:\n    @echo c\n"
	dir, env := rcptProject(t, runefile, map[string]string{})
	if r := run(t, dir, env, "a"); r.code != 0 { // creates the key once
		t.Fatalf("%+v", r)
	}
	var wg sync.WaitGroup
	results := make([]result, 2)
	for i := range results {
		wg.Go(func() { results[i] = run(t, dir, env, "all") })
	}
	wg.Wait()
	for _, r := range results {
		if r.code != 0 {
			t.Fatalf("%+v", r)
		}
	}
	byInvocation := map[string][]string{}
	for _, rc := range rcptRead(t, dir, env) {
		byInvocation[rc.Invocation] = append(byInvocation[rc.Invocation], rc.Task)
	}
	if len(byInvocation) != 3 {
		t.Fatalf("invocations %v", byInvocation)
	}
	for inv, tasks := range byInvocation {
		if len(tasks) != 4 && len(tasks) != 1 {
			t.Errorf("invocation %s has %v", inv, tasks)
		}
	}
}

// SC-007: without the setting nothing is written, anywhere.
func TestReceiptsOff(t *testing.T) {
	dir, env := rcptProject(t, "ok:\n    @echo fine\n", map[string]string{})
	if r := run(t, dir, env, "ok"); r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rune")); !os.IsNotExist(err) {
		t.Fatal(".rune was created")
	}
	state := strings.TrimPrefix(env[0], "XDG_STATE_HOME=")
	if entries, _ := os.ReadDir(state); len(entries) != 0 {
		t.Fatalf("state directory holds %v", entries)
	}
}

// rcptRewrite replaces a receipt file with a changed copy of its envelope.
func rcptRewrite(t *testing.T, path string, change func(env map[string]any)) (restore func()) {
	t.Helper()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(original, &env); err != nil {
		t.Fatal(err)
	}
	change(env)
	changed, _ := json.Marshal(env)
	if err := os.WriteFile(path, append(changed, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rcptResign writes a copy of a receipt with its predicate changed, signed
// with the run's local key: what a broken or interrupted run would leave.
func rcptResign(t *testing.T, dir string, env []string, from receipt.Receipt, change func(*receipt.Receipt)) {
	t.Helper()
	r := from
	r.ID = receipt.NewID(r.StartedAt)
	r.FinishedAt = r.FinishedAt.Add(time.Hour)
	change(&r)
	st, err := receipt.Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := receipt.Sign(rcptKey(t, env), st)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(receipt.Dir(dir), r.ID, line); err != nil {
		t.Fatal(err)
	}
}

func rcptVerify(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	return run(t, dir, env, append([]string{"verify"}, args...)...)
}

func wantVerified(t *testing.T, r result, want string) {
	t.Helper()
	if r.code != 0 || !strings.Contains(r.stdout, want) {
		t.Fatalf("want verified with %q: %+v", want, r)
	}
}

func wantNotVerified(t *testing.T, r result, want string) {
	t.Helper()
	if r.code != 1 || !strings.Contains(r.stdout, want) {
		t.Fatalf("want not verified with %q: %+v", want, r)
	}
}

const rcptTamperRunefile = "set receipts\nimport \"common.rune\"\n\ntest:\n    @cat input.txt\n"

// SC-001: each kind of tampering is caught, with its reason.
func TestReceiptsTampering(t *testing.T) {
	dir, env := rcptProject(t, rcptTamperRunefile, map[string]string{"common.rune": "lint:\n    @true\n", "input.txt": "in\n"})
	if r := run(t, dir, env, "test"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	c0 := rcptGit(t, dir, "rev-parse", "HEAD")
	wantVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "test: passed at "+c0[:7])
	stored, _ := receipt.List(receipt.Dir(dir))
	path := filepath.Join(receipt.Dir(dir), stored[0].Name)

	restore := rcptRewrite(t, path, func(env map[string]any) {
		payload, _ := base64.StdEncoding.DecodeString(env["payload"].(string))
		var st map[string]any
		_ = json.Unmarshal(payload, &st)
		st["predicate"].(map[string]any)["status"] = "passed"
		st["predicate"].(map[string]any)["durationMs"] = 1
		payload, _ = json.Marshal(st)
		env["payload"] = base64.StdEncoding.EncodeToString(payload)
	})
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the signature does not match the receipt")
	restore()
	restore = rcptRewrite(t, path, func(env map[string]any) {
		sig := env["signatures"].([]any)[0].(map[string]any)
		raw, _ := base64.StdEncoding.DecodeString(sig["sig"].(string))
		raw[0] ^= 1
		sig["sig"] = base64.StdEncoding.EncodeToString(raw)
	})
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the signature does not match the receipt")
	restore()

	for name, edit := range map[string]string{"Runefile": rcptTamperRunefile + "# changed\n", "common.rune": "lint:\n    @false\n", "input.txt": "other\n"} {
		writeFile(t, filepath.Join(dir, name), edit)
		rcptGit(t, dir, "commit", "-qam", "change "+name)
		wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the run was on other files than")
	}
	// The original revision still has its evidence; the working tree does not.
	r := rcptVerify(t, dir, env, c0, "test")
	wantVerified(t, r, "working tree: differs from "+c0[:7])
}

func TestReceiptsOutputChanged(t *testing.T) {
	runefile := "set receipts\n\n[cache(outputs=[\"out.txt\"])]\nbuild:\n    @echo built > out.txt\n"
	dir, env := rcptProject(t, runefile, map[string]string{".gitignore": "out.txt\n"})
	if r := run(t, dir, env, "build"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	writeFile(t, filepath.Join(dir, "out.txt"), "tampered\n")
	wantVerified(t, rcptVerify(t, dir, env, "HEAD", "build"), "outputs of build: out.txt changed")
}

// SC-002: a dirty tree, an untracked file and a change during the run never
// pass at the clean commit.
func TestReceiptsNeverMisleadAboutTheCommit(t *testing.T) {
	runefile := "set receipts\n\ntest:\n    @cat input.txt\n\ngen:\n    @echo more >> input.txt\n"
	dir, env := rcptProject(t, runefile, map[string]string{"input.txt": "in\n"})
	writeFile(t, filepath.Join(dir, "input.txt"), "dirty\n")
	run(t, dir, env, "test")
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the run was on other files than")
	rcptGit(t, dir, "checkout", "-q", "--", "input.txt")

	writeFile(t, filepath.Join(dir, "new.txt"), "untracked\n")
	run(t, dir, env, "test")
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the run was on other files than")
	if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}

	run(t, dir, env, "gen")
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "gen"), "files changed during the run")

	// The dirty run is evidence for the commit that holds exactly its files.
	rcptGit(t, dir, "checkout", "-q", "--", "input.txt")
	writeFile(t, filepath.Join(dir, "input.txt"), "dirty\n")
	rcptGit(t, dir, "commit", "-qam", "dirty")
	wantVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "test: passed on a snapshot identical to")
}

// SC-003: no other status is ever a pass.
func TestReceiptsOnlyPassedCounts(t *testing.T) {
	runefile := "set receipts\n\nbad:\n    @exit 1\n\n[confirm]\ndanger:\n    @true\n\n[cache(inputs=[\"input.txt\"])]\ncached:\n    @true\n\nok:\n    @true\n"
	dir, env := rcptProject(t, runefile, map[string]string{"input.txt": "in\n"})
	run(t, dir, env, "cached")
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil { // keep only the hit
		t.Fatal(err)
	}
	run(t, dir, env, "cached")
	run(t, dir, env, "bad")
	run(t, dir, env, "danger")
	if r := run(t, dir, env, "ok"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "bad"), "the run failed with exit code 1")
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "danger"), "the body did not run: it was refused")
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "cached"), "a cache hit, not a run")

	var ok receipt.Receipt
	for _, rc := range rcptRead(t, dir, env) {
		if rc.Task == "ok" {
			ok = rc
		}
	}
	for status, text := range map[string]string{receipt.StatusInterrupted: "the run was interrupted", receipt.StatusIncomplete: "the run was not fully observed"} {
		rcptResign(t, dir, env, ok, func(r *receipt.Receipt) { r.Task, r.Status = status, status })
		wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", status), text)
	}
}

// SC-006: receipts travel in a notes ref: attach, push, clone, fetch, verify.
func TestReceiptsNotesRoundTrip(t *testing.T) {
	dir, env := rcptProject(t, "set receipts\n\ntest:\n    @echo ok\n", map[string]string{})
	env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if r := run(t, dir, env, "test"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	rcptGit(t, dir, "notes", "add", "-m", "unrelated", "HEAD")
	r := run(t, dir, env, "receipt", "attach")
	if r.code != 0 || !strings.Contains(r.stderr, "attached 1 receipts to ") {
		t.Fatalf("attach: %+v", r)
	}
	key := run(t, dir, env, "receipt", "key")
	pub := filepath.Join(t.TempDir(), "me.pub")
	writeFile(t, pub, key.stdout)

	remote := filepath.Join(t.TempDir(), "remote.git")
	rcptGit(t, dir, "init", "-q", "--bare", remote)
	rcptGit(t, dir, "push", "-q", remote, "HEAD:refs/heads/main", "refs/notes/rune-receipts")
	clone := filepath.Join(t.TempDir(), "clone")
	rcptGit(t, dir, "clone", "-q", "-b", "main", remote, clone)
	rcptGit(t, clone, "fetch", "-q", "origin", "refs/notes/rune-receipts:refs/notes/rune-receipts")
	elsewhere := []string{"XDG_STATE_HOME=" + t.TempDir(), "RUNE_RECEIPT_KEY="}
	wantVerified(t, rcptVerify(t, clone, elsewhere, "--key", pub, "HEAD", "test"), "test: passed at ")
	if _, err := os.Stat(receipt.Dir(clone)); !os.IsNotExist(err) {
		t.Fatal("the clone has local receipts: the note was not what verified")
	}
	if got := rcptGit(t, dir, "notes", "show", "HEAD"); got != "unrelated" {
		t.Fatalf("the unrelated note is %q", got)
	}

	// A note moved onto a commit with other files makes nothing valid there.
	writeFile(t, filepath.Join(dir, "x.txt"), "x\n")
	rcptGit(t, dir, "add", "x.txt")
	rcptGit(t, dir, "commit", "-qm", "x")
	rcptGit(t, dir, "notes", "--ref=rune-receipts", "copy", "HEAD~1", "HEAD")
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	wantNotVerified(t, rcptVerify(t, dir, env, "HEAD", "test"), "the run was on other files than")
}

func TestReceiptsCIRunChannel(t *testing.T) {
	dir, env := rcptProject(t, "set receipts\n\nbuild:\n    @echo b\n\ntest: build\n    @echo t\n", map[string]string{})
	if r := run(t, dir, env, "ci", "run", "test"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	list := rcptRead(t, dir, env)
	if len(list) != 1 || list[0].Task != "test" || list[0].Channel != receipt.ChannelCIRun || list[0].Related != nil {
		t.Fatalf("receipts %+v", list)
	}
}

// SC-004: a secret in an argument, the environment or a dotenv file never
// leaves in a receipt, a note or a report.
func TestReceiptsKeepSecrets(t *testing.T) {
	const fromEnv, fromDotenv = "s3cr3t-env-fixture", "s3cr3t-dotenv-fixture"
	runefile := "set receipts\nset dotenv := \".env\"\n\ndeploy token:\n    @echo deploying\n"
	dir, env := rcptProject(t, runefile, map[string]string{".gitignore": ".env\n"})
	writeFile(t, filepath.Join(dir, ".env"), "API_TOKEN="+fromDotenv+"\n")
	env = append(env, "DEPLOY_TOKEN="+fromEnv, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	for _, value := range []string{fromEnv, fromDotenv} {
		if r := run(t, dir, env, "deploy", value); r.code != 0 {
			t.Fatalf("%+v", r)
		}
	}
	for _, rc := range rcptRead(t, dir, env) {
		if !rc.ArgumentsRedacted {
			t.Errorf("receipt %s does not say its arguments were redacted", rc.ID)
		}
	}
	attach := run(t, dir, env, "receipt", "attach")
	text := rcptVerify(t, dir, env, "HEAD", "deploy")
	jsonOut := rcptVerify(t, dir, env, "--json", "HEAD", "deploy")
	note := rcptGit(t, dir, "notes", "--ref=rune-receipts", "show", "HEAD")
	var stored strings.Builder
	files, _ := os.ReadDir(receipt.Dir(dir))
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(receipt.Dir(dir), f.Name()))
		stored.Write(data)
		if payload := decodePayloads(data); payload != "" {
			stored.WriteString(payload)
		}
	}
	for _, secret := range []string{fromEnv, fromDotenv} {
		for name, text := range map[string]string{
			"receipts": stored.String(), "note": note + decodePayloads([]byte(note)),
			"attach": attach.stdout + attach.stderr, "verify": text.stdout + text.stderr, "json": jsonOut.stdout + jsonOut.stderr,
		} {
			if strings.Contains(text, secret) {
				t.Errorf("%s holds the secret %s", name, secret)
			}
		}
	}
}

// decodePayloads returns the decoded payloads of the envelope lines in data,
// where a secret would sit in plain text.
func decodePayloads(data []byte) string {
	var out strings.Builder
	for line := range strings.SplitSeq(string(data), "\n") {
		var env struct{ Payload string }
		if json.Unmarshal([]byte(line), &env) == nil {
			payload, _ := base64.StdEncoding.DecodeString(env.Payload)
			out.Write(payload)
		}
	}
	return out.String()
}
