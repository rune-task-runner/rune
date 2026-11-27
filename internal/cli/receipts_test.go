package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/receipt"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
)

func TestReceiptStatus(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	failing := &shell.ExecError{Task: "t", Code: 3}
	notRun := &bodyNotRunError{err: errors.New("declined")}
	exit := func(error) int { return 3 }
	cases := []struct {
		name     string
		ctx      context.Context
		err      error
		cached   bool
		started  bool
		observed bool
		status   string
		code     *int
	}{
		{"cache hit", live, nil, true, false, true, receipt.StatusCached, nil},
		{"passed", live, nil, false, true, true, receipt.StatusPassed, new(0)},
		{"passed unobserved", live, nil, false, true, false, receipt.StatusIncomplete, new(0)},
		{"interrupted while running", cancelled, failing, false, true, true, receipt.StatusInterrupted, new(130)},
		{"interrupted before the body", cancelled, context.Canceled, false, false, true, receipt.StatusInterrupted, nil},
		{"refused", live, notRun, false, true, true, receipt.StatusRefused, nil},
		{"not started", live, errors.New("policy"), false, false, true, receipt.StatusRefused, nil},
		{"failed", live, failing, false, true, true, receipt.StatusFailed, new(3)},
		{"failed unobserved", live, failing, false, true, false, receipt.StatusIncomplete, new(3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, code := receiptStatus(c.ctx, c.err, c.cached, c.started, exit, c.observed)
			if status != c.status || (code == nil) != (c.code == nil) || (code != nil && *code != *c.code) {
				t.Fatalf("got %s %v, want %s %v", status, code, c.status, c.code)
			}
		})
	}
}

func TestReceiptStatusRefusalWrapsBodyNotRun(t *testing.T) {
	if !errors.Is(&bodyNotRunError{err: errors.New("x")}, scheduler.ErrBodyNotRun) {
		t.Fatal("the refusal marker changed")
	}
}

func TestReceiptChannel(t *testing.T) {
	if got := receiptChannel(Options{}, false); got != receipt.ChannelCLI {
		t.Errorf("plain run: %s", got)
	}
	if got := receiptChannel(Options{}, true); got != receipt.ChannelAgent {
		t.Errorf("agent run: %s", got)
	}
	node := Options{nodeRun: func(*engine, []scheduler.Invocation) error { return nil }}
	if got := receiptChannel(node, true); got != receipt.ChannelCIRun {
		t.Errorf("ci run: %s", got)
	}
}

func TestEnvClaims(t *testing.T) {
	env := map[string]string{"CI": "true", "GITHUB_RUN_ID": "s3cret-run", "HOME": "/home/u", "GITHUB_TOKEN": "t"}
	mask := func(s string) string { return strings.ReplaceAll(s, "s3cret", "***") }
	got := envClaims(func(k string) string { return env[k] }, mask)
	if len(got) != 2 || got["CI"] != "true" || got["GITHUB_RUN_ID"] != "***-run" {
		t.Fatalf("claims = %v", got)
	}
	if got := envClaims(func(string) string { return "" }, mask); got != nil {
		t.Fatalf("no claims: %v", got)
	}
}

func TestReceiptArgs(t *testing.T) {
	f := parseRunefile(t, "deploy env token *files:\n    @true\n")
	task := f.Tasks[0]
	params := eval.Args{
		Values: map[string]string{"env": "prod", "token": "s3cret", "files": "a b"},
		Lists:  map[string][]string{"files": {"a", "b"}},
	}
	mask := func(s string) string { return strings.ReplaceAll(s, "s3cret", "***") }
	args, redacted := receiptArgs(task, params, mask)
	want := []receipt.Arg{{Name: "env", Values: []string{"prod"}}, {Name: "token", Values: []string{"***"}}, {Name: "files", Values: []string{"a", "b"}}}
	if !redacted || len(args) != 3 {
		t.Fatalf("args %v redacted %v", args, redacted)
	}
	for i := range want {
		if args[i].Name != want[i].Name || !slices.Equal(args[i].Values, want[i].Values) {
			t.Errorf("arg %d = %+v, want %+v", i, args[i], want[i])
		}
	}
	if params.Values["token"] != "s3cret" || params.Lists["files"][0] != "a" {
		t.Fatal("receiptArgs changed the bound arguments")
	}
	args, redacted = receiptArgs(task, eval.Args{Values: map[string]string{}}, mask)
	if redacted || args[2].Values == nil || len(args[2].Values) != 0 {
		t.Fatalf("an unbound variadic must be an empty list: %+v", args)
	}
	none, _ := receiptArgs(parseRunefile(t, "x:\n    @true\n").Tasks[0], eval.Args{}, mask)
	if none == nil || len(none) != 0 {
		t.Fatalf("no parameters: %v", none)
	}
}

func TestDefinitionDigest(t *testing.T) {
	tasks := func(src string) map[string]*ast.Task { return indexTasks(parseRunefile(t, src)) }
	base := tasks("a:\n    echo a\n\nb:\n    echo b\n")
	body := tasks("a:\n    echo A\n\nb:\n    echo b\n")
	attr := tasks("[no-cd]\na:\n    echo a\n\nb:\n    echo b\n")
	other := tasks("a:\n    echo a\n\nb:\n    echo B\n")
	d := definitionDigest(base["a"], "sh")
	if d != definitionDigest(other["a"], "sh") {
		t.Error("another task's change changed the digest")
	}
	for name, got := range map[string]string{
		"body":      definitionDigest(body["a"], "sh"),
		"attribute": definitionDigest(attr["a"], "sh"),
		"executor":  definitionDigest(base["a"], "python:python3"),
	} {
		if got == d {
			t.Errorf("a change of %s kept the digest", name)
		}
	}
}

func TestReceiptKey(t *testing.T) {
	task := parseRunefile(t, "a x:\n    @true\n").Tasks[0]
	one := receiptKey(task, eval.Args{Values: map[string]string{"x": "1"}})
	two := receiptKey(task, eval.Args{Values: map[string]string{"x": "2"}})
	if one == two || !strings.HasPrefix(one, "a(") {
		t.Fatalf("keys %q %q", one, two)
	}
}

func TestRunefileEntries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	runefile := write("Runefile", "x")
	ignored := write("local.rune", "y")
	write(".gitignore", "local.rune\n")
	outside := filepath.Join(t.TempDir(), "shared.rune")
	if err := os.WriteFile(outside, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := receipt.OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &receiptSigner{root: dir, repo: repo}
	entries := s.runefileEntries(ctx, []string{runefile, ignored, outside, runefile, filepath.Join(dir, "missing")}, func(s string) string { return s })
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Path != "Runefile" || !entries[0].Covered || entries[0].SHA256 == "" {
		t.Errorf("Runefile = %+v", entries[0])
	}
	if entries[1].Path != "local.rune" || entries[1].Covered {
		t.Errorf("ignored file = %+v", entries[1])
	}
	if !strings.HasPrefix(entries[2].Path, "../") || entries[2].Covered {
		t.Errorf("outside file = %+v", entries[2])
	}

	plain := &receiptSigner{root: dir}
	entries = plain.runefileEntries(ctx, []string{runefile}, strings.ToUpper)
	if len(entries) != 1 || entries[0].Path != "RUNEFILE" || entries[0].Covered {
		t.Errorf("outside a repository = %+v", entries)
	}
}

func TestReceiptRunNilSafe(t *testing.T) {
	var r *receiptRun
	if r.ID() != "" {
		t.Fatal("a nil run has an ID")
	}
	e := &engine{}
	e.bodyStarting(nil, nil)
	e.cachedHit(nil, "", nil)
	e.finishReceipt(nil, nil)
	if e.beginReceipt(nil, eval.Args{}, receipt.RoleTask, "") != nil {
		t.Fatal("an engine without receipts began one")
	}
}

func TestSigningKey(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	key, _, _, err := signingKey(Options{}, false)
	if err != nil || key != nil {
		t.Fatalf("no local key yet: %v %v", key, err)
	}
	key, source, created, err := signingKey(Options{}, true)
	if err != nil || key == nil || !created || !strings.HasPrefix(source, "local: ") {
		t.Fatalf("create: %v %q %v %v", key, source, created, err)
	}
	again, _, created, err := signingKey(Options{}, false)
	if err != nil || created || !again.Equal(key) {
		t.Fatalf("existing: %v %v", created, err)
	}
	if _, _, _, err := signingKey(Options{ReceiptKey: "junk"}, true); err == nil || !strings.HasPrefix(err.Error(), "RUNE_RECEIPT_KEY: ") {
		t.Fatalf("bad env key: %v", err)
	}
	// No state directory on any system: Windows reads LocalAppData, and its
	// home is USERPROFILE.
	for _, name := range []string{"XDG_STATE_HOME", "HOME", "LocalAppData", "USERPROFILE"} {
		t.Setenv(name, "")
	}
	if _, _, _, err := signingKey(Options{}, true); err == nil {
		t.Fatal("no state directory must be an error")
	}
}

func TestRunefilesChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Runefile")
	if err := os.WriteFile(path, []byte("a:\n    @true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &receiptSigner{root: filepath.Dir(path)}
	s.runefileEntries(context.Background(), []string{path}, func(v string) string { return v })
	if s.runefilesChanged() {
		t.Fatal("unchanged file reported as changed")
	}
	if err := os.WriteFile(path, []byte("a:\n    @false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.runefilesChanged() {
		t.Fatal("an edit after the load is not seen")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !s.runefilesChanged() {
		t.Fatal("a removed file is not seen")
	}
}

// clearClaimVars empties the claim variables for a test: a CI runner sets
// them, and a receipt records them as claims.
func clearClaimVars(t *testing.T) {
	t.Helper()
	for _, name := range receiptClaimVars {
		t.Setenv(name, "")
	}
}

// Spec 061 R9: a snapshot taken for reuse is the one the body runs on.
func TestBodyStartingKeepsSnapshot(t *testing.T) {
	e, _ := reuseEngine(t, false)
	task := e.tasks["check"]
	r := &receiptRun{task: task, snapped: true, before: "kept"}
	e.bodyStarting(r, []string{"in.txt"})
	if !r.started || r.before != "kept" || len(r.inputs) != 1 {
		t.Fatalf("run %+v", r)
	}
	r = &receiptRun{task: task}
	e.bodyStarting(r, nil)
	if !r.started || !r.snapped || r.before == "" || r.snapErr != nil {
		t.Fatalf("run %+v", r)
	}
}

// Spec 061 R6: the outputs now, digested as a passed run records them,
// without writing a manifest.
func TestCurrentOutputs(t *testing.T) {
	src := "set receipts\n\n[cache(inputs=[\"in.txt\"], outputs=[\"out\"])]\nbuild:\n    @echo b\n\nplain:\n    @echo p\n"
	e := availEngine(t, src, "linux", planRun, &bytes.Buffer{})
	same := func(s string) string { return s }
	write := func(n int) []string {
		t.Helper()
		var rel []string
		for i := range n {
			name := fmt.Sprintf("out/f%03d", i)
			if err := os.MkdirAll(filepath.Join(e.root, "out"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.root, name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
			rel = append(rel, name)
		}
		return rel
	}
	if out, manifest, err := e.currentOutputs(e.tasks["build"], eval.Args{}, same); out != nil || manifest != nil || err != nil {
		t.Fatalf("no output files: %+v %v", out, err)
	}
	rel := write(2)
	files, err := receipt.OutputFiles(e.root, rel)
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := receipt.BuildOutputs(files)
	out, manifest, err := e.currentOutputs(e.tasks["build"], eval.Args{}, same)
	if err != nil || manifest != nil || out.Digest != want.Digest || out.Count != 2 {
		t.Fatalf("outputs %+v %v, want %+v", out, err, want)
	}
	if out, _, err := e.currentOutputs(e.tasks["plain"], eval.Args{}, same); out != nil || err != nil {
		t.Fatalf("no [cache]: %+v %v", out, err)
	}
	write(receipt.MaxInlineOutputs + 1)
	out, manifest, err = e.currentOutputs(e.tasks["build"], eval.Args{}, same)
	if err != nil || manifest == nil || out.Count != receipt.MaxInlineOutputs+1 {
		t.Fatalf("many outputs: %+v %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".rune")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something was written: %v", err)
	}
}

// Spec 061 FR-017: every receipt names the variables the invocation
// overrode on the command line, sorted, and never their values.
func TestReceiptsRecordOverrides(t *testing.T) {
	src := "set receipts\n\nmode := \"debug\"\nlevel := \"1\"\n\nbuild:\n    @echo {{mode}}\n\ntest: build\n    @echo {{level}}\n"
	dir := receiptProject(t, src, map[string]string{}, true)
	if _, err := runReceipts(t, dir, Options{}, "mode=release-value", "level=2", "test"); err != nil {
		t.Fatal(err)
	}
	list := readReceipts(t, dir)
	if len(list) != 2 {
		t.Fatalf("%d receipts", len(list))
	}
	for _, rc := range list {
		if !slices.Equal(rc.Overrides, []string{"level", "mode"}) {
			t.Errorf("%s: overrides %v", rc.Task, rc.Overrides)
		}
	}
	data, err := os.ReadFile(filepath.Join(receipt.Dir(dir), list[0].ID+".json"))
	if err != nil || strings.Contains(string(data), "release-value") {
		t.Fatalf("a value was recorded: %v", err)
	}
	if err := os.RemoveAll(receipt.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	if rc := readReceipts(t, dir)[0]; rc.Overrides != nil {
		t.Fatalf("no override: %v", rc.Overrides)
	}
}

func TestOverrideNames(t *testing.T) {
	if got := overrideNames(map[string]string{}, strings.ToUpper); got != nil {
		t.Errorf("none: %v", got)
	}
	got := overrideNames(map[string]string{"b": "1", "a": "2"}, strings.ToUpper)
	if !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("names %v", got)
	}
}
