package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/analysis"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/tasksuggest"
)

// seed records n calls of a command, each with the event before it and,
// unless outcome is "", the event after it ("ok" or "fail").
func seed(t *testing.T, dir, cwd, command string, n int, outcome string) {
	t.Helper()
	for range n {
		seedID++
		id := fmt.Sprintf("toolu_%d", seedID)
		record(t, dir, hookEvent("PreToolUse", id, cwd, command, nil))
		switch outcome {
		case "ok":
			record(t, dir, hookEvent("PostToolUse", id, cwd, command, nil))
		case "fail":
			record(t, dir, hookEvent("PostToolUseFailure", id, cwd, command, map[string]any{"error": "Exit code 1"}))
		}
	}
}

var seedID int

func runSuggest(t *testing.T, opts Options, flags SuggestFlags) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	opts.Stdout, opts.Stderr = &out, &errb
	if flags.MinRuns == 0 {
		flags.MinRuns = tasksuggest.DefaultMinRuns
	}
	err = Suggest(opts, flags)
	return out.String(), errb.String(), err
}

func runApply(t *testing.T, opts Options, flags SuggestFlags, ids ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	opts.Stdout, opts.Stderr = &out, &errb
	if flags.MinRuns == 0 {
		flags.MinRuns = tasksuggest.DefaultMinRuns
	}
	err = SuggestApply(opts, flags, ids)
	return out.String(), errb.String(), err
}

var idPattern = regexp.MustCompile(`\[([0-9a-f]{8})\] the agent ran this`)

// listedIDs runs rune suggest in dir and returns the IDs it lists.
func suggestionIDs(t *testing.T, dir string) []string {
	t.Helper()
	stdout, _, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range idPattern.FindAllStringSubmatch(stdout, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// noDiagnostics fails when the Runefile has any error or warning.
func noDiagnostics(t *testing.T, runefile string) {
	t.Helper()
	snap, err := analysis.NewService(analysis.DiskSourceStore{}).Analyze(context.Background(), analysis.AnalyzeRequest{URI: runefile})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Diagnostics) > 0 {
		t.Errorf("diagnostics: %v", snap.Diagnostics)
	}
}

func TestSuggestNothingRecorded(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	root, _, _ := observeRoot("", dir)
	stdout, stderr, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if err != nil || stderr != "" {
		t.Fatalf("%v %q", err, stderr)
	}
	if want := "Nothing recorded for " + root + ".\nTurn recording on with: rune agent observe claude\n"; stdout != want {
		t.Errorf("got %q", stdout)
	}
}

func TestSuggest14Runs(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	seed(t, dir, dir, "go test -run TestX ./pkg/...", 12, "ok")
	seed(t, dir, dir, "go test -run TestX ./pkg/...", 2, "fail")
	seed(t, dir, dir, "go test -run TestX ./pkg/...", 1, "")
	stdout, _, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Recorded 14 commands in 1 session",
		"1 attempt has no recorded outcome.",
		"the agent ran this 14 times (12 succeeded, 2 failed) in 1 session",
		"    go-test:\n        go test -run TestX ./pkg/...\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in\n%s", want, stdout)
		}
	}
}

func TestSuggestErrors(t *testing.T) {
	observeState(t)
	for _, n := range []int{-1, 1001} {
		_, _, err := runSuggest(t, Options{Cwd: t.TempDir()}, SuggestFlags{MinRuns: n})
		if _, ok := err.(*UsageError); !ok {
			t.Errorf("--min-runs %d: %v", n, err)
		}
	}
	dir := importProject(t, map[string]string{"Runefile": "x:\n    @echo {{nope}}\n"})
	stdout, stderr, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if _, ok := err.(*ValidationError); !ok || stdout != "" || !strings.Contains(stderr, "nope") {
		t.Errorf("a Runefile with an error: %v %q %q", err, stdout, stderr)
	}
	_, _, err = runSuggest(t, Options{Cwd: dir, File: filepath.Join(dir, "missing")}, SuggestFlags{})
	if _, ok := err.(*UsageError); !ok {
		t.Errorf("a missing --file: %v", err)
	}
}

func TestSuggestTaskNamedSuggest(t *testing.T) {
	observeState(t)
	dir := importProject(t, map[string]string{"Runefile": "# Suggest.\nsuggest:\n    @echo x\n"})
	_, stderr, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if err != nil || !strings.Contains(stderr, suggestTaskNote) {
		t.Errorf("%v %q", err, stderr)
	}
}

func TestSuggestRoot(t *testing.T) {
	observeState(t)
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, "sub/x": ""})
	seed(t, dir, dir, "go vet ./...", 3, "ok")
	for name, opts := range map[string]Options{
		"from a subdirectory": {Cwd: filepath.Join(dir, "sub")},
		"with --file":         {Cwd: t.TempDir(), File: filepath.Join(dir, "Runefile")},
	} {
		stdout, _, err := runSuggest(t, opts, SuggestFlags{})
		if err != nil || !strings.Contains(stdout, "go vet ./...") {
			t.Errorf("%s: %v\n%s", name, err, stdout)
		}
	}
	// Without a Runefile the project is the working directory itself.
	stdout, _, _ := runSuggest(t, Options{Cwd: t.TempDir()}, SuggestFlags{})
	if !strings.HasPrefix(stdout, "Nothing recorded") {
		t.Errorf("another directory: %s", stdout)
	}
}

func TestSuggestApplyCreates(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	seed(t, dir, dir, "go build ./...", 3, "ok")
	ids := suggestionIDs(t, dir)
	if len(ids) != 1 {
		t.Fatalf("ids %v", ids)
	}
	stdout, stderr, err := runApply(t, Options{Cwd: dir}, SuggestFlags{DryRun: true}, ids[0])
	if err != nil || !strings.Contains(stdout, "+go-build:") || !strings.Contains(stderr, "Runefile: would be created, adding go-build") {
		t.Fatalf("dry run: %v\n%s\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "Runefile")); err == nil {
		t.Fatal("a dry run wrote")
	}
	_, stderr, err = runApply(t, Options{Cwd: dir}, SuggestFlags{}, ids[0])
	if err != nil || !strings.HasPrefix(stderr, "Runefile: created, added go-build\nnote: these tasks have no attributes") {
		t.Fatalf("%v\n%s", err, stderr)
	}
	runefile := filepath.Join(dir, "Runefile")
	if got, want := readFile(t, runefile), "# Run it\ngo-build:\n    go build ./...\n"; got != want {
		t.Errorf("Runefile %q, want %q", got, want)
	}
	noDiagnostics(t, runefile)
	stdout, _, _ = runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if !strings.Contains(stdout, "Already tasks: go-build (3 runs).") || strings.Contains(stdout, "[") {
		t.Errorf("after apply:\n%s", stdout)
	}
}

func TestSuggestApplyAppends(t *testing.T) {
	observeState(t)
	old := "# The project.\nimport \"lib.rune\"\n\n# Build it.\ngo-build:\n    go build -v\n"
	dir := importProject(t, map[string]string{"Runefile": old, "lib.rune": "# Lint.\nlint:\n    @echo lint\n"})
	seed(t, dir, dir, "go build ./...", 3, "ok")
	seed(t, dir, filepath.Join(dir, "web"), "npm test", 4, "ok")
	ids := suggestionIDs(t, dir)
	if len(ids) != 2 {
		t.Fatalf("ids %v", ids)
	}
	_, stderr, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, ids[0], ids[1], ids[0])
	if err != nil || !strings.HasPrefix(stderr, "Runefile: updated, added npm-test, go-build-2\n") {
		t.Fatalf("%v\n%s", err, stderr)
	}
	runefile := filepath.Join(dir, "Runefile")
	got := readFile(t, runefile)
	want := old + "\n# Run it\n[working-directory(\"web\")]\nnpm-test:\n    npm test\n\n# Run it\ngo-build-2:\n    go build ./...\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	noDiagnostics(t, runefile)
}

func TestSuggestApplyRefuses(t *testing.T) {
	observeState(t)
	old := "# Build it.\nbuild:\n    go build -v\n"
	dir := importProject(t, map[string]string{"Runefile": old})
	seed(t, dir, dir, "go build ./...", 3, "ok")
	ids := suggestionIDs(t, dir)
	runefile := filepath.Join(dir, "Runefile")
	unchanged := func(name string) {
		t.Helper()
		if readFile(t, runefile) != old {
			t.Errorf("%s: the Runefile changed", name)
		}
	}

	_, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{})
	wantErr[*UsageError](t, err, "rune suggest apply needs the ID of a suggestion; list them with: rune suggest")
	_, _, err = runApply(t, Options{Cwd: dir}, SuggestFlags{}, ids[0], "ffffffff")
	wantErr[*UsageError](t, err, "no suggestion has the ID ffffffff; list them with: rune suggest (and the same --min-runs)")
	unchanged("an unknown id")
	_, _, err = runApply(t, Options{Cwd: dir}, SuggestFlags{MinRuns: 2000}, ids[0])
	if _, ok := err.(*UsageError); !ok {
		t.Errorf("--min-runs 2000: %v", err)
	}

	other := "# Changed.\nx:\n    y\n"
	_, _, err = runApply(t, Options{Cwd: dir}, SuggestFlags{beforeWrite: func() {
		if err := os.WriteFile(runefile, []byte(other), 0o644); err != nil {
			t.Fatal(err)
		}
	}}, ids[0])
	wantErr[*UsageError](t, err, "Runefile changed while the tasks were being added; nothing written. Run the command again")
	if readFile(t, runefile) != other {
		t.Error("the other program's change was replaced")
	}
}

func TestSuggestApplyNewFileAppears(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	seed(t, dir, dir, "make", 3, "ok")
	ids := suggestionIDs(t, dir)
	runefile := filepath.Join(dir, "Runefile")
	_, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{beforeWrite: func() {
		if err := os.WriteFile(runefile, []byte("mine:\n    x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}}, ids[0])
	wantErr[*UsageError](t, err, "Runefile changed while the tasks were being added; nothing written. Run the command again")
	if readFile(t, runefile) != "mine:\n    x\n" {
		t.Error("the file that appeared was replaced")
	}
}

func TestSuggestApplyInvalidRunefile(t *testing.T) {
	observeState(t)
	dir := importProject(t, map[string]string{"Runefile": "x:\n    @echo {{nope}}\n"})
	_, stderr, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, "12345678")
	var invalid *ValidationError
	if !errors.As(err, &invalid) || !strings.HasSuffix(err.Error(), "; fix the Runefile first") || !strings.Contains(stderr, "nope") {
		t.Errorf("%v\n%s", err, stderr)
	}
}

func TestSuggestApplySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	observeState(t)
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "real.rune")
	if err := os.WriteFile(target, []byte("x:\n    y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "Runefile")); err != nil {
		t.Fatal(err)
	}
	_, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, "12345678")
	if _, ok := err.(*UsageError); !ok || !strings.Contains(err.Error(), "Runefile is a symbolic link to ") {
		t.Errorf("got %v", err)
	}
}

func TestSuggestForget(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	root, _, _ := observeRoot("", dir)
	seed(t, dir, dir, "make", 1, "ok")
	var errb bytes.Buffer
	if err := SuggestForget(Options{Cwd: dir, Stderr: &errb}); err != nil || errb.String() != "deleted 2 records for "+root+"\n" {
		t.Errorf("%v %q", err, errb.String())
	}
	errb.Reset()
	if err := SuggestForget(Options{Cwd: dir, Stderr: &errb}); err != nil || errb.String() != "nothing recorded for "+root+"\n" {
		t.Errorf("second time: %v %q", err, errb.String())
	}
	seed(t, dir, dir, "make", 1, "")
	errb.Reset()
	if err := SuggestForget(Options{Cwd: dir, Stderr: &errb}); err != nil || errb.String() != "deleted 1 record for "+root+"\n" {
		t.Errorf("one record: %v %q", err, errb.String())
	}
	if err := SuggestForget(Options{Cwd: dir, File: filepath.Join(dir, "missing"), Stderr: &errb}); err == nil {
		t.Error("a missing --file")
	}
}

func TestTaskInfo(t *testing.T) {
	root := t.TempDir()
	body := []*ast.BodyLine{{Raw: "  go vet ./...  "}}
	attr := func(kind, str string) *ast.Attribute { return &ast.Attribute{Kind: kind, Str: str} }
	tests := []struct {
		name string
		task *ast.Task
		want tasksuggest.TaskInfo
	}{
		{"plain", &ast.Task{Name: "vet", Body: body}, tasksuggest.TaskInfo{Name: "vet", Body: "go vet ./..."}},
		{"two lines", &ast.Task{Name: "a", Body: append(body, body...)}, tasksuggest.TaskInfo{Name: "a"}},
		{"python", &ast.Task{Name: "a", Executor: ast.ExecPython, Body: body}, tasksuggest.TaskInfo{Name: "a"}},
		{"sh", &ast.Task{Name: "a", Executor: ast.ExecSh, Body: body}, tasksuggest.TaskInfo{Name: "a", Body: "go vet ./..."}},
		{"script", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrScript, "python3")}, Body: body}, tasksuggest.TaskInfo{Name: "a"}},
		{"no-cd", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrNoCD, "")}, Body: body}, tasksuggest.TaskInfo{Name: "a"}},
		{"relative dir", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrWorkingDirectory, "./web/")}, Body: body}, tasksuggest.TaskInfo{Name: "a", Dir: "web", Body: "go vet ./..."}},
		{"root dir", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrWorkingDirectory, ".")}, Body: body}, tasksuggest.TaskInfo{Name: "a", Body: "go vet ./..."}},
		{"absolute dir", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrWorkingDirectory, filepath.Join(root, "web"))}, Body: body}, tasksuggest.TaskInfo{Name: "a", Dir: "web", Body: "go vet ./..."}},
		{"outside", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrWorkingDirectory, "../x")}, Body: body}, tasksuggest.TaskInfo{Name: "a", Dir: "../x"}},
	}
	for _, tt := range tests {
		if got := taskInfo(tt.task, root, ""); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// set working-directory is the directory of a task that names none.
	for _, tt := range []struct {
		name, defaultDir string
		task             *ast.Task
		want             tasksuggest.TaskInfo
	}{
		{"default dir", "sub", &ast.Task{Name: "a", Body: body}, tasksuggest.TaskInfo{Name: "a", Dir: "sub", Body: "go vet ./..."}},
		{"attribute wins", "sub", &ast.Task{Name: "a", Attributes: []*ast.Attribute{attr(ast.AttrWorkingDirectory, ".")}, Body: body}, tasksuggest.TaskInfo{Name: "a", Body: "go vet ./..."}},
		{"default outside", "../x", &ast.Task{Name: "a", Body: body}, tasksuggest.TaskInfo{Name: "a", Dir: "../x"}},
	} {
		if got := taskInfo(tt.task, root, tt.defaultDir); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// A Runefile that sets working-directory runs a task without the attribute
// there: a command run at the root is no such task, and the task added for it
// names the root.
func TestSuggestWorkingDirectorySetting(t *testing.T) {
	observeState(t)
	old := "set working-directory := \"sub\"\n\n# Build it.\nbuild:\n    go build ./...\n"
	dir := importProject(t, map[string]string{"Runefile": old, "sub/.keep": ""})
	seed(t, dir, dir, "go build ./...", 3, "ok")
	seed(t, dir, filepath.Join(dir, "sub"), "go vet ./...", 3, "ok")
	seed(t, dir, filepath.Join(dir, "sub"), "go build ./...", 3, "ok")
	stdout, _, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Already tasks: build (3 runs).") {
		t.Errorf("the build in sub is not the task:\n%s", stdout)
	}
	ids := suggestionIDs(t, dir)
	if len(ids) != 2 {
		t.Fatalf("ids %v in\n%s", ids, stdout)
	}
	if _, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, ids...); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "Runefile"))
	for _, want := range []string{
		"\n# Run it\n[working-directory(\".\")]\ngo-build:\n    go build ./...\n",
		"\n# Run it\ngo-vet:\n    go vet ./...\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
}

func TestCreateFile(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "Runefile")
	if err := createFile(name, []byte("a")); err != nil || readFile(t, name) != "a" {
		t.Fatalf("%v", err)
	}
	if err := createFile(name, []byte("b")); !errors.Is(err, fs.ErrExist) || readFile(t, name) != "a" {
		t.Errorf("over an existing file: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left %d files", len(entries))
	}
	if err := createFile(filepath.Join(dir, "missing", "Runefile"), nil); err == nil {
		t.Error("in a missing directory")
	}
}

func TestReadIfExists(t *testing.T) {
	dir := t.TempDir()
	if data, err := readIfExists(filepath.Join(dir, "missing")); data != nil || err != nil {
		t.Errorf("missing: %v %v", data, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := readIfExists(empty); data == nil || len(data) != 0 || err != nil {
		t.Errorf("empty: %v %v", data, err)
	}
	if _, err := readIfExists(dir); err == nil {
		t.Error("a directory")
	}
}

// An error in the added text is a defect of rune suggest: nothing is
// written and the message says so.
func TestCheckSuggested(t *testing.T) {
	dir := t.TempDir()
	var errb bytes.Buffer
	err := checkSuggested(Options{Stderr: &errb}, filepath.Join(dir, "Runefile"), []byte("x:\n    @echo {{nope}}\n"))
	wantErr[*CommandFailure](t, err, "the added tasks are not valid; this is a bug in rune suggest")
	if !strings.Contains(errb.String(), "nope") {
		t.Errorf("the error is not shown: %s", errb.String())
	}
	if err := checkSuggested(Options{}, filepath.Join(dir, "Runefile"), []byte("# X.\nx:\n    @echo x\n")); err != nil {
		t.Errorf("valid text: %v", err)
	}
}

func TestSuggestWithoutStateDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("LocalAppData", "")
	t.Setenv("USERPROFILE", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("a home directory is still found")
	}
	dir := t.TempDir()
	if _, _, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{}); err == nil {
		t.Error("Suggest without a state directory")
	}
	if _, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, "12345678"); err == nil {
		t.Error("SuggestApply without a state directory")
	}
	if err := SuggestForget(Options{Cwd: dir, Stderr: &bytes.Buffer{}}); err == nil {
		t.Error("SuggestForget without a state directory")
	}
}

func TestSuggestUnreadableStore(t *testing.T) {
	base := observeState(t)
	dir := t.TempDir()
	store := storeOf(t, base, dir)
	if err := os.MkdirAll(filepath.Join(store.Dir(), "events.jsonl", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSuggest(t, Options{Cwd: dir}, SuggestFlags{}); err == nil {
		t.Error("Suggest with records that cannot be read")
	}
	if _, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{}, "12345678"); err == nil {
		t.Error("SuggestApply with records that cannot be read")
	}
	if err := SuggestForget(Options{Cwd: dir, Stderr: &bytes.Buffer{}}); err == nil {
		t.Error("SuggestForget with records that cannot be removed")
	}
}

// A write that fails for another reason than a file in the way is a
// failure of the command.
func TestSuggestApplyWriteFails(t *testing.T) {
	observeState(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "p")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed(t, dir, dir, "make", 3, "ok")
	ids := suggestionIDs(t, dir)
	_, _, err := runApply(t, Options{Cwd: dir}, SuggestFlags{beforeWrite: func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}}, ids[0])
	if _, ok := err.(*CommandFailure); !ok || !strings.HasPrefix(err.Error(), "Runefile: ") {
		t.Errorf("got %v", err)
	}
}

// The Runefile is named relative to the working directory also when that
// directory is reached through a link (a short name on Windows does the
// same).
func TestSuggestApplyShownThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	observeState(t)
	dir := t.TempDir()
	seed(t, dir, dir, "make", 3, "ok")
	ids := suggestionIDs(t, dir)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runApply(t, Options{Cwd: link}, SuggestFlags{}, ids[0])
	if err != nil || !strings.HasPrefix(stderr, "Runefile: created, added make\n") {
		t.Errorf("%v\n%s", err, stderr)
	}
}
