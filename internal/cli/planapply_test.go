package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/planstore"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 051: a task with [plan] stores its plan, and a task with [applies]
// runs only with that stored plan, after a person approved it, once. Every
// body here writes a marker file, so "ran" is read from the disk.

const planSrc = `[plan(file="tfplan", summary="tfplan.txt")]
plan env:
    @echo "plan for {{env}}" > tfplan
    @echo "Plan: 1 to add for {{env}}" > tfplan.txt
    @echo planned >> plan.done

[applies("plan")]
apply env:
    @read -r line < "$RUNE_PLAN_FILE"; echo "$line" > applied.txt
    @echo "$RUNE_PLAN_ID $RUNE_PLAN_DIGEST" > applied.id
    @echo applied >> apply.done
`

// planClock is the store's clock in these tests. Every reading is a second
// later than the one before, so two plans never share a creation time.
type planClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *planClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func (c *planClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// planAdapter builds an adapter whose bodies run in the directory the store
// is under, with a test clock and no git.
func planAdapter(t *testing.T, src string) (*mcpAdapter, *planClock) {
	t.Helper()
	a := adapterFor(t, src)
	a.workDir = a.root
	clock := &planClock{t: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)}
	a.clock = clock.now
	a.gitInfo = func(context.Context, string) (string, *bool) { return "", nil }
	if usesPlans(a.tasks) {
		a.planIdent = newPlanIdentity(a.file, a.root, a.settings, nil, a.overrides)
	}
	return a, clock
}

func planDir(a *mcpAdapter) string { return filepath.Join(a.root, ".rune", "plans") }

// planRecords reads every record of the adapter's store, oldest first.
func planRecords(t *testing.T, a *mcpAdapter) []planstore.Record {
	t.Helper()
	if info, err := os.Stat(planDir(a)); err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(planDir(a))
	if err != nil {
		t.Fatal(err)
	}
	var recs []planstore.Record
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(planDir(a), entry.Name(), "record.json"))
		if err != nil {
			continue
		}
		var rec planstore.Record
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatalf("record of %s: %v", entry.Name(), err)
		}
		recs = append(recs, rec)
	}
	slices.SortFunc(recs, func(x, y planstore.Record) int { return x.Created.Compare(y.Created) })
	return recs
}

func planStoreOf(a *mcpAdapter) *planstore.Store {
	store := planstore.Open(a.root)
	store.Now = a.clock
	return store
}

func planState(t *testing.T, a *mcpAdapter, rec planstore.Record) planstore.State {
	t.Helper()
	latest, state, ok, err := planStoreOf(a).Latest(rec.Key)
	if err != nil || !ok || latest.ID != rec.ID {
		t.Fatalf("Latest = %+v, %v, %v, want record %s", latest, ok, err, rec.ID)
	}
	return state
}

func planJournal(t *testing.T, a *mcpAdapter) []planstore.Event {
	t.Helper()
	f, err := os.Open(filepath.Join(planDir(a), "journal.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []planstore.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev planstore.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("journal line %q: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}
	return events
}

func eventNames(events []planstore.Event) []string {
	names := make([]string, len(events))
	for i, ev := range events {
		names[i] = ev.Event
	}
	return names
}

// mustPlan runs the plan task and returns the record it stored.
func mustPlan(t *testing.T, a *mcpAdapter, args map[string]string) planstore.Record {
	t.Helper()
	res, err := a.Call(context.Background(), "plan", args)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d\nstderr: %s", err, res.ExitCode, res.Stderr)
	}
	recs := planRecords(t, a)
	if len(recs) == 0 {
		t.Fatal("plan stored no record")
	}
	return recs[len(recs)-1]
}

func applyWith(a *mcpAdapter, args map[string]string, who *asked) (mcpserver.Result, error) {
	return a.CallApproved(context.Background(), "apply", args, nil, []string{"apply"}, who.approve)
}

func planFileText(t *testing.T, a *mcpAdapter, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.workDir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(data))
}

func planFileExists(a *mcpAdapter, name string) bool {
	_, err := os.Lstat(filepath.Join(a.workDir, name))
	return err == nil
}

var prod = map[string]string{"env": "prod"}

func TestCapturePlan(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	a.version = "v9.9.9"
	dirty := true
	a.gitInfo = func(context.Context, string) (string, *bool) { return "abc123", &dirty }

	res, err := a.Call(context.Background(), "plan", prod)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	recs := planRecords(t, a)
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one", recs)
	}
	rec := recs[0]
	artifact, err := os.ReadFile(filepath.Join(planDir(a), rec.ID, "artifact"))
	if err != nil || string(artifact) != "plan for prod\n" {
		t.Errorf("stored artifact = %q, %v", artifact, err)
	}
	if rec.Artifact.SHA256 != hexDigest(artifact) || rec.Artifact.Size != int64(len(artifact)) {
		t.Errorf("artifact digest = %+v", rec.Artifact)
	}
	if rec.Summary.SHA256 != hexDigest([]byte("Plan: 1 to add for prod\n")) {
		t.Errorf("summary digest = %+v", rec.Summary)
	}
	if planFileExists(a, "tfplan") || planFileExists(a, "tfplan.txt") {
		t.Error("the plan file or its summary is still in the workspace")
	}
	wantLine := fmt.Sprintf("plan %s: sha256:%s, %d bytes, expires %s. Apply it with \"apply\".\n",
		rec.ID, rec.Artifact.SHA256, rec.Artifact.Size, planTime(rec.Expires))
	if !strings.Contains(res.Stderr, wantLine) {
		t.Errorf("stderr = %q, want it to hold %q", res.Stderr, wantLine)
	}

	task := a.tasks["plan"]
	args := eval.Args{Values: map[string]string{"env": "prod"}}
	want := planstore.Record{
		ID: rec.ID, Created: rec.Created, Expires: rec.Created.Add(time.Hour),
		Key:        targetKey(runefilePath(a.file, a.root), "plan", args),
		PlanTask:   "plan",
		ApplyTasks: []string{"apply"},
		Args:       `env="prod"`,
		Binding: planstore.Binding{
			WorkDir:  realPath(a.workDir),
			Runefile: runefileDigest(a.file),
			Config:   configDigest(a.settings, nil, a.overrides),
			Host:     runtime.GOOS + "/" + runtime.GOARCH,
		},
		Artifact:    rec.Artifact,
		Summary:     rec.Summary,
		RuneVersion: "v9.9.9",
		Git:         planstore.Git{Commit: "abc123", Dirty: &dirty},
		Surface:     "mcp",
	}
	if got, _ := json.Marshal(rec); string(got) != string(mustJSON(t, want)) {
		t.Errorf("record = %s\nwant     %s", got, mustJSON(t, want))
	}
	if task.PlanSpec() == nil {
		t.Fatal("the task lost its plan arguments")
	}

	events := planJournal(t, a)
	if len(events) != 1 || events[0].Event != planstore.EventPlanCreated || events[0].Plan != rec.ID ||
		events[0].Digest != rec.Artifact.SHA256 || events[0].PlanTask != "plan" || events[0].Surface != "mcp" || events[0].Args != `env="prod"` {
		t.Errorf("journal = %+v", events)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCapturePlanNoGitNoApply(t *testing.T) {
	a, _ := planAdapter(t, "[plan(file=\"p\", summary=\"s\", expires=\"30m\")]\nplan:\n    @echo x > p\n    @echo y > s\n")
	res, err := a.Call(context.Background(), "plan", nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	rec := planRecords(t, a)[0]
	if rec.Git.Commit != "" || rec.Git.Dirty != nil {
		t.Errorf("git = %+v, want empty", rec.Git)
	}
	if got := rec.Expires.Sub(rec.Created); got != 30*time.Minute {
		t.Errorf("expiry = %v, want 30m", got)
	}
	if rec.Args != "" || len(rec.ApplyTasks) != 0 {
		t.Errorf("args = %q, apply tasks = %v", rec.Args, rec.ApplyTasks)
	}
	if !strings.Contains(res.Stderr, " No task applies it.\n") {
		t.Errorf("stderr = %q", res.Stderr)
	}
}

func TestCapturePlanWorkingDirectory(t *testing.T) {
	a, _ := planAdapter(t, "[plan(file=\"out/p\", summary=\"s\")]\n[working-directory(\"infra\")]\nplan:\n    @echo x > out/p\n    @echo y > s\n")
	if err := os.MkdirAll(filepath.Join(a.root, "infra", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := a.Call(context.Background(), "plan", nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	rec := planRecords(t, a)[0]
	if want := realPath(filepath.Join(a.root, "infra")); rec.Binding.WorkDir != want {
		t.Errorf("working directory = %q, want %q", rec.Binding.WorkDir, want)
	}
	if _, err := os.Stat(filepath.Join(a.root, "infra", "out", "p")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plan file is still in the task's directory: %v", err)
	}
}

func TestApplyApproved(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	rec := mustPlan(t, a, prod)
	who := &asked{}
	res, err := applyWith(a, prod, who)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("apply: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	if len(who.requests) != 1 {
		t.Fatalf("requests = %d, want one", len(who.requests))
	}
	want := &mcpserver.PlanApproval{
		ID: rec.ID, Task: "plan", Created: planTime(rec.Created),
		Digest: rec.Artifact.SHA256, Size: rec.Artifact.Size, Summary: "Plan: 1 to add for prod\n",
	}
	if got := who.requests[0].Plan; got == nil || fmt.Sprintf("%+v", *got) != fmt.Sprintf("%+v", *want) {
		t.Errorf("plan in the request = %+v, want %+v", got, want)
	}
	if got := planFileText(t, a, "applied.txt"); got != "plan for prod" {
		t.Errorf("the body read %q, want the stored plan", got)
	}
	if got, want := planFileText(t, a, "applied.id"), rec.ID+" sha256:"+rec.Artifact.SHA256; got != want {
		t.Errorf("plan variables = %q, want %q", got, want)
	}
	if got := planFileText(t, a, "plan.done"); got != "planned" {
		t.Errorf("the plan body ran %d times", strings.Count(got, "planned"))
	}
	if got := planFileText(t, a, "apply.done"); got != "applied" {
		t.Errorf("apply.done = %q", got)
	}

	if state := planState(t, a, rec); state != planstore.StateApplied {
		t.Errorf("state = %v, want applied", state)
	}
	if _, err := os.Stat(filepath.Join(planDir(a), rec.ID, "artifact")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the artifact is still stored: %v", err)
	}
	events := planJournal(t, a)
	if got, want := eventNames(events), []string{"plan-created", "approval-requested", "approval-given", "apply-started", "apply-finished"}; !slices.Equal(got, want) {
		t.Fatalf("journal = %v, want %v", got, want)
	}
	started, finished := events[3], events[4]
	if started.Attempt == "" || started.Attempt != finished.Attempt || finished.State != "applied" || finished.Exit == nil || *finished.Exit != 0 {
		t.Errorf("apply events = %+v, %+v", started, finished)
	}
	for _, ev := range events[1:] {
		if ev.Plan != rec.ID || ev.Digest != rec.Artifact.SHA256 || ev.ApplyTask != "apply" || ev.PlanTask != "plan" || ev.Args != `env="prod"` {
			t.Errorf("event %s = %+v", ev.Event, ev)
		}
	}
}

func TestApplyEnvOnlyForApplyBody(t *testing.T) {
	src := "[plan(file=\"p\", summary=\"s\")]\nplan:\n    @echo x > p\n    @echo y > s\n\n" +
		"before:\n    @echo \"${RUNE_PLAN_FILE:-none}\" > before.txt\n\n" +
		"after:\n    @echo \"${RUNE_PLAN_FILE:-none}\" > after.txt\n\n" +
		"[applies(\"plan\")]\napply: before && after\n    @echo \"${RUNE_PLAN_FILE:-none}\" > body.txt\n"
	a, _ := planAdapter(t, src)
	mustPlan(t, a, nil)
	if res, err := applyWith(a, nil, &asked{}); err != nil || res.ExitCode != 0 {
		t.Fatalf("apply: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	if before, after := planFileText(t, a, "before.txt"), planFileText(t, a, "after.txt"); before != "none" || after != "none" {
		t.Errorf("a dependency saw %q and a hook saw %q, want none", before, after)
	}
	if body := planFileText(t, a, "body.txt"); body == "none" || !filepath.IsAbs(body) {
		t.Errorf("the body saw %q, want the private copy", body)
	}
}

// Nothing else of the variable's name reaches the body in its place.
func TestApplyEnvWins(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	a.baseEnv = append(os.Environ(), "RUNE_PLAN_FILE=decoy", "RUNE_PLAN_ID=decoy")
	rec := mustPlan(t, a, prod)
	if res, err := applyWith(a, prod, &asked{}); err != nil || res.ExitCode != 0 {
		t.Fatalf("apply: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	if got := planFileText(t, a, "applied.txt"); got != "plan for prod" {
		t.Errorf("the body read %q", got)
	}
	if got := planFileText(t, a, "applied.id"); !strings.HasPrefix(got, rec.ID+" ") {
		t.Errorf("plan id in the body = %q", got)
	}
}

func TestApplySummaryMasked(t *testing.T) {
	const secret = "supersecretvalue123"
	src := "[plan(file=\"p\", summary=\"s\")]\nplan:\n    @echo x > p\n    @echo \"token " + secret + " set\" > s\n\n[applies(\"plan\")]\napply:\n    @echo \"$RUNE_PLAN_FILE\" > apply.done\n"
	a, _ := planAdapter(t, src)
	a.baseEnv = append(os.Environ(), "MY_API_TOKEN="+secret)
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	mustPlan(t, a, nil)
	who := &asked{}
	if _, err := applyWith(a, nil, who); err != nil {
		t.Fatal(err)
	}
	if got := who.requests[0].Plan.Summary; got != "token *** set\n" {
		t.Errorf("summary in the request = %q", got)
	}
}

func TestTargetKey(t *testing.T) {
	one := eval.Args{Values: map[string]string{"env": "prod"}}
	base := targetKey("/repo/Runefile", "plan", one)
	if base != targetKey("/repo/Runefile", "plan", eval.Args{Values: map[string]string{"env": "prod"}}) {
		t.Error("equal inputs give different keys")
	}
	joined := eval.Args{Values: map[string]string{"files": "a b"}, Lists: map[string][]string{"files": {"a b"}}}
	split := eval.Args{Values: map[string]string{"files": "a b"}, Lists: map[string][]string{"files": {"a", "b"}}}
	others := map[string]string{
		"another Runefile": targetKey("/repo/ops.rune", "plan", one),
		"another task":     targetKey("/repo/Runefile", "plan2", one),
		"another value":    targetKey("/repo/Runefile", "plan", eval.Args{Values: map[string]string{"env": "staging"}}),
		"a list":           targetKey("/repo/Runefile", "plan", joined),
	}
	for name, key := range others {
		if key == base {
			t.Errorf("%s gives the same key", name)
		}
	}
	if targetKey("/repo/Runefile", "plan", joined) == targetKey("/repo/Runefile", "plan", split) {
		t.Error("one element and two elements give the same key")
	}
}

func TestRunefilePath(t *testing.T) {
	// The temporary directory is itself behind a link on some hosts.
	root := realPath(t.TempDir())
	if got, want := runefilePath(nil, root), filepath.Join(root, "Runefile"); got != want {
		t.Errorf("runefilePath(nil) = %q, want %q", got, want)
	}
	if got, want := runefilePath(&ast.File{Path: "sub/ops.rune"}, root), filepath.Join(root, "ops.rune"); got != want {
		t.Errorf("relative path = %q, want %q", got, want)
	}
	abs := filepath.Join(root, "x", "Runefile")
	if got := runefilePath(&ast.File{Path: abs}, "/elsewhere"); got != abs {
		t.Errorf("absolute path = %q, want %q", got, abs)
	}
}

func TestBindingDigests(t *testing.T) {
	t.Run("definition", func(t *testing.T) {
		one := parseRunefile(t, "build:\n    @echo one\n")
		same := parseRunefile(t, "build:\n    @echo one\n")
		other := parseRunefile(t, "build:\n    @echo two\n")
		if runefileDigest(one) != runefileDigest(same) {
			t.Error("the same definition gives two digests")
		}
		if runefileDigest(one) == runefileDigest(other) {
			t.Error("a changed body keeps the digest")
		}
		if runefileDigest(nil) == runefileDigest(one) {
			t.Error("no file and a file give the same digest")
		}
	})

	t.Run("configuration", func(t *testing.T) {
		base := configDigest(config.Settings{Quiet: true}, []string{"A=1"}, map[string]string{"region": "eu"})
		if base != configDigest(config.Settings{Quiet: true}, []string{"A=1"}, map[string]string{"region": "eu"}) {
			t.Error("the same configuration gives two digests")
		}
		others := map[string]string{
			"a setting":      configDigest(config.Settings{}, []string{"A=1"}, map[string]string{"region": "eu"}),
			"a dotenv entry": configDigest(config.Settings{Quiet: true}, []string{"A=2"}, map[string]string{"region": "eu"}),
			"an override":    configDigest(config.Settings{Quiet: true}, []string{"A=1"}, map[string]string{"region": "us"}),
		}
		for name, digest := range others {
			if digest == base {
				t.Errorf("%s does not change the digest", name)
			}
		}
		if configDigest(config.Settings{}, nil, map[string]string{"a": "1", "b": "2"}) != configDigest(config.Settings{}, nil, map[string]string{"b": "2", "a": "1"}) {
			t.Error("the order of overrides changes the digest")
		}
	})

	t.Run("arguments text", func(t *testing.T) {
		task := parseRunefile(t, "t env region=\"eu\" *files:\n    @echo hi\n").Tasks[0]
		args := eval.Args{
			Values: map[string]string{"env": "the secret", "region": strings.Repeat("x", 200), "files": "a b"},
			Lists:  map[string][]string{"files": {"a", "b c"}},
		}
		hide := func(s string) string { return strings.ReplaceAll(s, "secret", "***") }
		want := `env="the ***", region="` + strings.Repeat("x", 64) + `"..., files=["a" "b c"]`
		if got := argsText(task, args, hide); got != want {
			t.Errorf("argsText = %q, want %q", got, want)
		}
		if got := argsText(parseRunefile(t, "t:\n    @echo hi\n").Tasks[0], eval.Args{}, nil); got != "" {
			t.Errorf("argsText with no parameter = %q", got)
		}
	})
}

// fakeTool writes an executable named name with the given bytes and returns
// the environment whose PATH finds it.
func fakeTool(t *testing.T, dir, name, content string) []string {
	t.Helper()
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"PATH=" + dir, "PATHEXT=.EXE"}
}

func TestToolIdentity(t *testing.T) {
	dir := t.TempDir()
	env := fakeTool(t, dir, "faketool", "version one")
	path, digest, err := toolIdentity("faketool", "", env)
	if err != nil {
		t.Fatal(err)
	}
	if digest != hexDigest([]byte("version one")) || !strings.HasPrefix(filepath.Base(path), "faketool") {
		t.Errorf("identity = %q, %q", path, digest)
	}
	if _, _, err := toolIdentity("nosuchtool", "", env); !errors.Is(err, errToolNotFound) {
		t.Errorf("a tool that is not there: %v, want errToolNotFound", err)
	}
	if _, _, err := toolIdentity("faketool", "", []string{"PATH=", "NOEQUALS"}); err == nil {
		t.Error("an empty PATH finds the tool")
	}

	t.Run("the last PATH wins", func(t *testing.T) {
		other := t.TempDir()
		fakeTool(t, other, "faketool", "version two")
		_, digest, err := toolIdentity("faketool", "", append(env, "PATH="+other))
		if err != nil || digest != hexDigest([]byte("version two")) {
			t.Errorf("digest = %q, %v", digest, err)
		}
	})

	t.Run("a relative entry is resolved against the body's directory", func(t *testing.T) {
		project := t.TempDir()
		if err := os.Mkdir(filepath.Join(project, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		relative := fakeTool(t, filepath.Join(project, "bin"), "localtool", "version three")
		relative[0] = "PATH=bin"
		path, digest, err := toolIdentity("localtool", project, relative)
		if err != nil || digest != hexDigest([]byte("version three")) || !filepath.IsAbs(path) {
			t.Errorf("identity = %q, %q, %v", path, digest, err)
		}
		if _, _, err := toolIdentity("localtool", t.TempDir(), relative); !errors.Is(err, errToolNotFound) {
			t.Errorf("from another directory: %v, want errToolNotFound", err)
		}
	})

	// The shell that runs a body reads an empty entry as its directory. A
	// lookup that skipped it would record the tool of a later entry while the
	// body starts the one beside it.
	t.Run("an empty entry is the body's directory", func(t *testing.T) {
		project, later := t.TempDir(), t.TempDir()
		fakeTool(t, later, "heretool", "on a later entry")
		here := fakeTool(t, project, "heretool", "in the body's directory")
		here[0] = "PATH=" + string(os.PathListSeparator) + later
		path, digest, err := toolIdentity("heretool", project, here)
		if err != nil || digest != hexDigest([]byte("in the body's directory")) || !filepath.IsAbs(path) {
			t.Errorf("identity = %q, %q, %v", path, digest, err)
		}
	})

	t.Run("nil reads the process environment", func(t *testing.T) {
		t.Setenv("PATH", dir)
		if runtime.GOOS == "windows" {
			t.Setenv("PATHEXT", ".EXE")
		}
		if _, _, err := toolIdentity("faketool", "", nil); err != nil {
			t.Error(err)
		}
	})

	if runtime.GOOS == "windows" {
		return
	}
	t.Run("a file that cannot be run is not a tool", func(t *testing.T) {
		plain := t.TempDir()
		if err := os.WriteFile(filepath.Join(plain, "notes"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := toolIdentity("notes", "", []string{"PATH=" + plain}); err == nil {
			t.Error("a file without the execute bit was found")
		}
	})
	t.Run("a link is resolved", func(t *testing.T) {
		links := t.TempDir()
		if err := os.Symlink(filepath.Join(dir, "faketool"), filepath.Join(links, "shim")); err != nil {
			t.Fatal(err)
		}
		path, digest, err := toolIdentity("shim", "", []string{"PATH=" + links})
		if err != nil || digest != hexDigest([]byte("version one")) || filepath.Base(path) != "faketool" {
			t.Errorf("identity = %q, %q, %v", path, digest, err)
		}
	})
}

func TestGitInfo(t *testing.T) {
	commit, dirty := gitInfo(context.Background(), t.TempDir())
	if commit != "" || dirty != nil {
		t.Errorf("outside a checkout: %q, %v", commit, dirty)
	}
	t.Run("no git on the path", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if commit, dirty := gitInfo(context.Background(), t.TempDir()); commit != "" || dirty != nil {
			t.Errorf("without git: %q, %v", commit, dirty)
		}
	})
}

// In a checkout git names the commit and says whether the tree is dirty.
func TestGitInfoInACheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if commit, dirty := gitInfo(context.Background(), dir); commit != "" || dirty != nil {
		t.Errorf("a checkout with no commit: %q, %v", commit, dirty)
	}
	git("add", "Runefile")
	git("commit", "-q", "-m", "first")
	commit, dirty := gitInfo(context.Background(), dir)
	if len(commit) < 40 || dirty == nil || *dirty {
		t.Errorf("a clean checkout: %q, %v", commit, dirty)
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, dirty := gitInfo(context.Background(), dir); dirty == nil || !*dirty {
		t.Errorf("a changed tree is not reported as dirty: %v", dirty)
	}
}

// gitCheckout makes dir a checkout with one commit that holds a Runefile.
func gitCheckout(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "Runefile"}, {"commit", "-q", "-m", "first"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// The plan file and its summary are still in the workspace when git is
// asked. They are what the task makes, not a change to the tree.
func TestGitInfoIgnoresThePlanFiles(t *testing.T) {
	dir := t.TempDir()
	gitCheckout(t, dir)
	for _, name := range []string{"tfplan", "tfplan.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, dirty := gitInfo(context.Background(), dir); dirty == nil || !*dirty {
		t.Errorf("untracked files with nothing ignored: dirty = %v, want true", dirty)
	}
	commit, dirty := gitInfo(context.Background(), dir, "tfplan", "./tfplan.txt")
	if len(commit) < 40 || dirty == nil || *dirty {
		t.Errorf("only the plan files are new: %q, dirty = %v, want false", commit, dirty)
	}
	if _, dirty := gitInfo(context.Background(), dir, "tfplan"); dirty == nil || !*dirty {
		t.Errorf("a file that is not ignored: dirty = %v, want true", dirty)
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, dirty := gitInfo(context.Background(), dir, "tfplan", "tfplan.txt"); dirty == nil || !*dirty {
		t.Errorf("a changed file beside the plan files: dirty = %v, want true", dirty)
	}
}

// A plan made in a clean checkout is recorded as made in a clean tree.
func TestCapturePlanRecordsACleanTree(t *testing.T) {
	a, _ := planAdapter(t, "[plan(file=\"tfplan\", summary=\"tfplan.txt\")]\nplan:\n    @echo x > tfplan\n    @echo y > tfplan.txt\n")
	gitCheckout(t, a.root)
	a.gitInfo = nil
	res, err := a.Call(context.Background(), "plan", nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	rec := planRecords(t, a)[0]
	if len(rec.Git.Commit) < 40 || rec.Git.Dirty == nil || *rec.Git.Dirty {
		t.Errorf("git = %+v, want the commit and a clean tree", rec.Git)
	}
}

func TestEngineGitHook(t *testing.T) {
	yes := true
	e := &engine{ctx: context.Background(), gitInfo: func(context.Context, string) (string, *bool) { return "c0ffee", &yes }}
	if commit, dirty := e.git("x"); commit != "c0ffee" || dirty == nil || !*dirty {
		t.Errorf("hook = %q, %v", commit, dirty)
	}
	if commit, _ := (&engine{ctx: context.Background()}).git(t.TempDir()); commit != "" {
		t.Errorf("real git outside a checkout = %q", commit)
	}
}

func TestChangedParts(t *testing.T) {
	then := planstore.Binding{WorkDir: "/w", Runefile: "r", Config: "c", Host: "linux/amd64", Tool: "tofu", ToolPath: "/bin/tofu", ToolDigest: "d"}
	if got := changedParts(then, then); got != nil {
		t.Errorf("equal bindings differ in %v", got)
	}
	all := planstore.Binding{WorkDir: "/x", Runefile: "x", Config: "x", Host: "x", Tool: "tofu", ToolPath: "/bin/tofu", ToolDigest: "x"}
	want := []string{"Runefile definition", "configuration", "working directory", "host", `tool "tofu"`}
	if got := changedParts(then, all); !slices.Equal(got, want) {
		t.Errorf("changedParts = %v, want %v", got, want)
	}
	added := planstore.Binding{WorkDir: "/w", Runefile: "r", Config: "c", Host: "linux/amd64", Tool: "terraform"}
	if got := changedParts(planstore.Binding{WorkDir: "/w", Runefile: "r", Config: "c", Host: "linux/amd64"}, added); !slices.Equal(got, []string{`tool "terraform"`}) {
		t.Errorf("a tool declared later = %v", got)
	}
}

// refusalCase prepares a store and names the refusal an apply call then gets.
type refusalCase struct {
	name string
	src  string
	args map[string]string
	prep func(t *testing.T, a *mcpAdapter, clock *planClock) string
}

const applyNotRun = `task "apply" was not run: `

func rewriteStored(t *testing.T, a *mcpAdapter, rec planstore.Record, file, content string) {
	t.Helper()
	path := filepath.Join(planDir(a), rec.ID, file)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRefusals(t *testing.T) {
	const noParams = "[plan(file=\"p\", summary=\"s\")]\nplan:\n    @echo x > p\n    @echo y > s\n\n[applies(\"plan\")]\napply:\n    @echo \"$RUNE_PLAN_FILE\" > apply.done\n"
	failing := strings.Replace(planSrc, "    @echo applied >> apply.done\n", "    @exit 3\n", 1)
	withTool := strings.Replace(planSrc, `summary="tfplan.txt")]`, `summary="tfplan.txt", tool="faketool")]`, 1)
	again := `run "plan" again and review the new plan`

	stored := func(what, detail string, change func(t *testing.T, a *mcpAdapter, rec planstore.Record)) func(*testing.T, *mcpAdapter, *planClock) string {
		return func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			rec := mustPlan(t, a, prod)
			change(t, a, rec)
			return fmt.Sprintf(`the stored %s of plan %s %s; run "plan" again`, what, rec.ID, detail)
		}
	}
	remove := func(file string) func(*testing.T, *mcpAdapter, planstore.Record) {
		return func(t *testing.T, a *mcpAdapter, rec planstore.Record) {
			if err := os.Remove(filepath.Join(planDir(a), rec.ID, file)); err != nil {
				t.Fatal(err)
			}
		}
	}
	overwrite := func(file string) func(*testing.T, *mcpAdapter, planstore.Record) {
		return func(t *testing.T, a *mcpAdapter, rec planstore.Record) {
			rewriteStored(t, a, rec, file, "other bytes\n")
		}
	}
	directory := func(file string) func(*testing.T, *mcpAdapter, planstore.Record) {
		return func(t *testing.T, a *mcpAdapter, rec planstore.Record) {
			path := filepath.Join(planDir(a), rec.ID, file)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	changed := func(parts string, change func(a *mcpAdapter)) func(*testing.T, *mcpAdapter, *planClock) string {
		return func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			rec := mustPlan(t, a, prod)
			change(a)
			return fmt.Sprintf(`plan %s no longer matches: the %s changed since it was made; run "plan" again`, rec.ID, parts)
		}
	}

	cases := []refusalCase{
		{"no plan", planSrc, prod, func(*testing.T, *mcpAdapter, *planClock) string {
			return `there is no plan for env="prod"; run "plan" with the same arguments first`
		}},
		{"no plan and no parameters", noParams, nil, func(*testing.T, *mcpAdapter, *planClock) string {
			return `there is no plan; run "plan" first`
		}},
		{"a plan for other arguments", planSrc, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			mustPlan(t, a, map[string]string{"env": "staging"})
			return `there is no plan for env="prod"; run "plan" with the same arguments first`
		}},
		{"applied", planSrc, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			rec := mustPlan(t, a, prod)
			if _, err := applyWith(a, prod, &asked{}); err != nil {
				t.Fatal(err)
			}
			done, _ := planStoreOf(a).AttemptOf(rec)
			return fmt.Sprintf("plan %s was already applied at %s; %s", rec.ID, planTime(done.Finished), again)
		}},
		{"failed", failing, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			rec := mustPlan(t, a, prod)
			if res, err := applyWith(a, prod, &asked{}); err != nil || res.ExitCode != 3 {
				t.Fatalf("the failing apply: %v, exit %d", err, res.ExitCode)
			}
			done, _ := planStoreOf(a).AttemptOf(rec)
			return fmt.Sprintf("the apply of plan %s failed at %s; check the target, then %s", rec.ID, planTime(done.Finished), again)
		}},
		{"unfinished", planSrc, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			rec := mustPlan(t, a, prod)
			claim, err := planStoreOf(a).Claim(rec, "apply", "mcp")
			if err != nil {
				t.Fatal(err)
			}
			started, _ := planStoreOf(a).AttemptOf(claim.Record)
			return fmt.Sprintf("an apply of plan %s started at %s and did not finish, so its outcome is unknown; check the target, then %s", rec.ID, planTime(started.Started), again)
		}},
		{"expired", planSrc, prod, func(t *testing.T, a *mcpAdapter, clock *planClock) string {
			rec := mustPlan(t, a, prod)
			clock.advance(2 * time.Hour)
			return fmt.Sprintf(`plan %s expired at %s; run "plan" again`, rec.ID, planTime(rec.Expires))
		}},
		{"the definition changed", planSrc, prod, changed("Runefile definition", func(a *mcpAdapter) { a.planIdent.def = "other" })},
		{"the configuration changed", planSrc, prod, changed("configuration", func(a *mcpAdapter) { a.planIdent.config = "other" })},
		{"the working directory changed", planSrc, prod, changed("working directory", func(a *mcpAdapter) { a.workDir = filepath.Join(a.root, "moved") })},
		{"the host changed", planSrc, prod, changed("host", func(a *mcpAdapter) { a.goos = "plan9" })},
		{"two parts changed", planSrc, prod, changed("Runefile definition, configuration", func(a *mcpAdapter) {
			a.planIdent.def, a.planIdent.config = "other", "other"
		})},
		{"the tool changed", withTool, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			tools := t.TempDir()
			a.baseEnv = fakeTool(t, tools, "faketool", "version one")
			rec := mustPlan(t, a, prod)
			fakeTool(t, tools, "faketool", "version two")
			return fmt.Sprintf(`plan %s no longer matches: the tool "faketool" changed since it was made; run "plan" again`, rec.ID)
		}},
		{"the tool is gone", withTool, prod, func(t *testing.T, a *mcpAdapter, _ *planClock) string {
			tools := t.TempDir()
			a.baseEnv = fakeTool(t, tools, "faketool", "version one")
			rec := mustPlan(t, a, prod)
			a.baseEnv = []string{"PATH=" + t.TempDir()}
			return fmt.Sprintf(`plan %s no longer matches: the tool "faketool" changed since it was made; run "plan" again`, rec.ID)
		}},
		{"the artifact is missing", planSrc, prod, stored("artifact", "is missing", remove("artifact"))},
		{"the artifact was overwritten", planSrc, prod, stored("artifact", "does not have the recorded digest", overwrite("artifact"))},
		{"the artifact is a directory", planSrc, prod, stored("artifact", "is not a regular file", directory("artifact"))},
		{"the summary is missing", planSrc, prod, stored("summary", "is missing", remove("summary"))},
		{"the summary was overwritten", planSrc, prod, stored("summary", "does not have the recorded digest", overwrite("summary"))},
		{"the summary is a directory", planSrc, prod, stored("summary", "is not a regular file", directory("summary"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, clock := planAdapter(t, c.src)
			reason := c.prep(t, a, clock)
			before := len(planJournal(t, a))
			_ = os.Remove(filepath.Join(a.workDir, "apply.done"))

			who := &asked{}
			_, err := applyWith(a, c.args, who)
			if err == nil || err.Error() != applyNotRun+reason {
				t.Fatalf("refusal = %v\nwant      %s", err, applyNotRun+reason)
			}
			if len(who.requests) != 0 {
				t.Errorf("the person was asked %d times", len(who.requests))
			}
			if planFileExists(a, "apply.done") {
				t.Error("the apply body ran")
			}
			events := planJournal(t, a)[before:]
			if len(events) != 1 || events[0].Event != planstore.EventApplyRefused || events[0].Reason != reason || events[0].ApplyTask != "apply" {
				t.Errorf("journal after the call = %+v", events)
			}
		})
	}
}

func TestApplyRefusedForALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	for _, file := range []string{"artifact", "summary"} {
		a, _ := planAdapter(t, planSrc)
		rec := mustPlan(t, a, prod)
		path := filepath.Join(planDir(a), rec.ID, file)
		target := filepath.Join(a.root, "elsewhere")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		who := &asked{}
		_, err = applyWith(a, prod, who)
		want := fmt.Sprintf(`%sthe stored %s of plan %s is not a regular file; run "plan" again`, applyNotRun, file, rec.ID)
		if err == nil || err.Error() != want || len(who.requests) != 0 {
			t.Errorf("%s as a link: %v, %d requests, want %s", file, err, len(who.requests), want)
		}
	}
}

func TestRefusalMasksArguments(t *testing.T) {
	const secret = "supersecretvalue123"
	a, _ := planAdapter(t, planSrc)
	a.baseEnv = append(os.Environ(), "MY_API_TOKEN="+secret)
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	_, err := applyWith(a, map[string]string{"env": "x" + secret}, &asked{})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), `env="x***"`) {
		t.Errorf("refusal = %v", err)
	}
	_, err = applyWith(a, map[string]string{"env": strings.Repeat("y", 200)}, &asked{})
	if want := `env="` + strings.Repeat("y", 64) + `"...;`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("refusal = %v, want it to hold %s", err, want)
	}
}

func TestCaptureRefusals(t *testing.T) {
	plan := func(attr, body string) string {
		return attr + "\nplan:\n" + body + "    @echo planned >> plan.done\n"
	}
	const attr = `[plan(file="tfplan", summary="tfplan.txt")]`
	cases := []struct {
		name, src, want string
		prep            func(t *testing.T, a *mcpAdapter)
		posix           bool
	}{
		{"no plan file", plan(attr, "    @echo y > tfplan.txt\n"), `plan file "tfplan" of task "plan" is missing`, nil, false},
		{"no summary", plan(attr, "    @echo x > tfplan\n"), `plan summary "tfplan.txt" of task "plan" is missing`, nil, false},
		{"a directory", plan(attr, "    @mkdir tfplan\n    @echo y > tfplan.txt\n"), `plan file "tfplan" of task "plan" is not a regular file`, nil, true},
		{"a link", plan(attr, "    @echo x > real\n    @ln -s real tfplan\n    @echo y > tfplan.txt\n"), `plan file "tfplan" of task "plan" is a symbolic link`, nil, true},
		{
			"a file over the limit", plan(attr, "    @echo 0123456789 > tfplan\n    @echo y > tfplan.txt\n"),
			`plan file "tfplan" of task "plan" is larger than `,
			func(_ *testing.T, a *mcpAdapter) { a.planMaxArtifact = 4 }, false,
		},
		{
			"a summary over the limit", plan(attr, "    @echo x > tfplan\n    @echo 0123456789 > tfplan.txt\n"),
			`plan summary "tfplan.txt" of task "plan" is larger than `,
			func(_ *testing.T, a *mcpAdapter) { a.planMaxSummary = 4 }, false,
		},
		{
			"a tool that is not there", plan(`[plan(file="tfplan", summary="tfplan.txt", tool="nosuchtool")]`, "    @echo x > tfplan\n    @echo y > tfplan.txt\n"),
			`plan tool "nosuchtool" of task "plan" was not found on the path`, nil, false,
		},
		{
			"a store that cannot be used", plan(attr, "    @echo x > tfplan\n    @echo y > tfplan.txt\n"),
			`the plan of task "plan" was not stored: `,
			func(t *testing.T, a *mcpAdapter) {
				if err := os.MkdirAll(filepath.Join(a.root, ".rune"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(planDir(a), []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.posix && runtime.GOOS == "windows" {
				t.Skip("needs mkdir and ln")
			}
			a, _ := planAdapter(t, c.src)
			if c.prep != nil {
				c.prep(t, a)
			}
			res, err := a.Call(context.Background(), "plan", nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.ExitCode == 0 || !strings.Contains(res.Stderr, c.want) {
				t.Errorf("exit %d, stderr %q, want a failure with %q", res.ExitCode, res.Stderr, c.want)
			}
			if recs := planRecords(t, a); len(recs) != 0 {
				t.Errorf("a record was stored: %+v", recs)
			}
			if !planFileExists(a, "plan.done") {
				t.Error("the plan body did not run")
			}
		})
	}
}

func TestPlanBodyFailureStoresNothing(t *testing.T) {
	failing := strings.Replace(planSrc, "    @echo planned >> plan.done\n", "    @exit 2\n", 1)
	a, _ := planAdapter(t, failing)
	res, err := a.Call(context.Background(), "plan", prod)
	if err != nil || res.ExitCode != 2 {
		t.Fatalf("plan: %v, exit %d", err, res.ExitCode)
	}
	if recs := planRecords(t, a); len(recs) != 0 {
		t.Errorf("a failed plan body stored %+v", recs)
	}
	if !planFileExists(a, "tfplan") {
		t.Error("the workspace file of a failed plan was removed")
	}
}

func TestReplanReplaces(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	first := mustPlan(t, a, prod)
	second := mustPlan(t, a, prod)
	if first.ID == second.ID {
		t.Fatal("the second plan has the first one's id")
	}
	if _, err := os.Stat(filepath.Join(planDir(a), first.ID, "artifact")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the replaced plan keeps its artifact: %v", err)
	}
	if state := planState(t, a, second); state != planstore.StateReady {
		t.Errorf("the new plan is %v", state)
	}
	events := planJournal(t, a)
	last := events[len(events)-1]
	if last.Event != planstore.EventPlanReplaced || last.Plan != first.ID || last.ReplacedBy != second.ID {
		t.Errorf("last event = %+v, want plan-replaced of %s by %s", last, first.ID, second.ID)
	}

	who := &asked{}
	if _, err := applyWith(a, prod, who); err != nil {
		t.Fatal(err)
	}
	if got := who.requests[0].Plan.ID; got != second.ID {
		t.Errorf("the person was asked about plan %s, want %s", got, second.ID)
	}
}

func TestApplyOnce(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	mustPlan(t, a, prod)
	if _, err := applyWith(a, prod, &asked{}); err != nil {
		t.Fatal(err)
	}
	who := &asked{}
	_, err := applyWith(a, prod, who)
	if err == nil || !strings.Contains(err.Error(), "was already applied at") || len(who.requests) != 0 {
		t.Errorf("second call: %v, %d requests", err, len(who.requests))
	}
	if got := planFileText(t, a, "apply.done"); got != "applied" {
		t.Errorf("apply.done = %q, want one line", got)
	}
}

func TestApplyConcurrent(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	mustPlan(t, a, prod)
	const callers = 20
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			_, errs[i] = applyWith(a, prod, &asked{})
		})
	}
	wg.Wait()
	ran := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ran++
		case strings.Contains(err.Error(), "was already used by another call"),
			strings.Contains(err.Error(), "was already applied at"),
			strings.Contains(err.Error(), "did not finish"):
		default:
			t.Errorf("unexpected refusal: %v", err)
		}
	}
	if ran != 1 {
		t.Errorf("%d calls ran, want exactly one", ran)
	}
	if got := planFileText(t, a, "apply.done"); got != "applied" {
		t.Errorf("apply.done = %q, want one line", got)
	}
}

func TestApplyFailedIsFinal(t *testing.T) {
	failing := strings.Replace(planSrc, "    @echo applied >> apply.done\n", "    @test -f allow.flag\n    @echo applied >> apply.done\n", 1)
	a, _ := planAdapter(t, failing)
	rec := mustPlan(t, a, prod)
	res, err := applyWith(a, prod, &asked{})
	if err != nil || res.ExitCode == 0 {
		t.Fatalf("the apply: %v, exit %d", err, res.ExitCode)
	}
	done, ok := planStoreOf(a).AttemptOf(rec)
	if !ok || done.State != planstore.StateFailed || done.Exit != res.ExitCode {
		t.Fatalf("outcome = %+v, %v", done, ok)
	}

	who := &asked{}
	if _, err := applyWith(a, prod, who); err == nil || !strings.Contains(err.Error(), "the apply of plan "+rec.ID+" failed at") || len(who.requests) != 0 {
		t.Errorf("call after the failure: %v, %d requests", err, len(who.requests))
	}

	if err := os.WriteFile(filepath.Join(a.workDir, "allow.flag"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mustPlan(t, a, prod)
	who = &asked{}
	if _, err := applyWith(a, prod, who); err != nil {
		t.Fatal(err)
	}
	want := []string{"the previous apply of this target failed at " + planTime(done.Finished) + ". Check the target before approving."}
	if got := who.requests[0].Plan.Notes; !slices.Equal(got, want) {
		t.Errorf("notes = %q, want %q", got, want)
	}

	// The apply after that succeeded, so the next plan carries no note.
	mustPlan(t, a, prod)
	who = &asked{}
	if _, err := applyWith(a, prod, who); err != nil {
		t.Fatal(err)
	}
	if got := who.requests[0].Plan.Notes; len(got) != 0 {
		t.Errorf("notes after a successful apply = %q", got)
	}
}

func TestApplyUnfinished(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	rec := mustPlan(t, a, prod)
	claim, err := planStoreOf(a).Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	started, _ := planStoreOf(a).AttemptOf(claim.Record)

	who := &asked{}
	if _, err := applyWith(a, prod, who); err == nil || !strings.Contains(err.Error(), "did not finish, so its outcome is unknown") || len(who.requests) != 0 {
		t.Errorf("call on an unfinished plan: %v", err)
	}
	mustPlan(t, a, prod)
	who = &asked{}
	if _, err := applyWith(a, prod, who); err != nil {
		t.Fatal(err)
	}
	want := []string{"the previous apply of this target started at " + planTime(started.Started) + " and did not finish. Check the target before approving."}
	if got := who.requests[0].Plan.Notes; !slices.Equal(got, want) {
		t.Errorf("notes = %q, want %q", got, want)
	}
}

func TestApprovalNotGivenLeavesPlanReady(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	rec := mustPlan(t, a, prod)
	refusal := errors.New(`task "apply" was not approved: the request was declined. Nothing ran.`)
	_, err := applyWith(a, prod, &asked{err: refusal})
	if !errors.Is(err, refusal) {
		t.Fatalf("error = %v, want the approver's unchanged", err)
	}
	if planFileExists(a, "apply.done") {
		t.Error("the body ran without an approval")
	}
	if state := planState(t, a, rec); state != planstore.StateReady {
		t.Errorf("state = %v, want ready", state)
	}
	events := planJournal(t, a)
	last := events[len(events)-1]
	if last.Event != planstore.EventApprovalNotGiven || last.Reason != refusal.Error() {
		t.Errorf("last event = %+v", last)
	}
	if _, err := applyWith(a, prod, &asked{}); err != nil || !planFileExists(a, "apply.done") {
		t.Errorf("a later approved call: %v", err)
	}
}

// A plan that stops being the one to apply while the person is asked is
// refused at the claim, and that is not a failed apply.
func TestClaimRefusedAfterApproval(t *testing.T) {
	cases := []struct {
		name   string
		during func(t *testing.T, a *mcpAdapter, clock *planClock, rec planstore.Record)
		want   func(rec planstore.Record) string
		state  planstore.State
	}{
		{
			"a newer plan was stored",
			func(t *testing.T, a *mcpAdapter, _ *planClock, _ planstore.Record) { mustPlan(t, a, prod) },
			func(rec planstore.Record) string {
				return fmt.Sprintf(`plan %s was replaced by a newer plan; call "apply" again`, rec.ID)
			},
			planstore.StateReplaced,
		},
		{
			"the plan expired",
			func(_ *testing.T, _ *mcpAdapter, clock *planClock, _ planstore.Record) { clock.advance(2 * time.Hour) },
			func(rec planstore.Record) string {
				return fmt.Sprintf(`plan %s expired at %s; run "plan" again`, rec.ID, planTime(rec.Expires))
			},
			planstore.StateExpired,
		},
		{
			"the artifact was overwritten",
			func(t *testing.T, a *mcpAdapter, _ *planClock, rec planstore.Record) {
				rewriteStored(t, a, rec, "artifact", "other bytes\n")
			},
			func(rec planstore.Record) string {
				return fmt.Sprintf(`the stored artifact of plan %s does not have the recorded digest; run "plan" again`, rec.ID)
			},
			planstore.StateReady,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, clock := planAdapter(t, planSrc)
			rec := mustPlan(t, a, prod)
			who := &asked{before: func() { c.during(t, a, clock, rec) }}
			_, err := applyWith(a, prod, who)
			if want := applyNotRun + c.want(rec); err == nil || err.Error() != want {
				t.Fatalf("refusal = %v\nwant      %s", err, want)
			}
			if planFileExists(a, "apply.done") {
				t.Error("the body ran")
			}
			dir := filepath.Join(planDir(a), rec.ID)
			for _, file := range []string{"claim", "outcome.json"} {
				if _, err := os.Stat(filepath.Join(dir, file)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s exists after a refused claim: %v", file, err)
				}
			}
			if _, ok := planStoreOf(a).AttemptOf(rec); ok {
				t.Error("the refused claim is recorded as an attempt")
			}
			var after []string
			for _, ev := range planJournal(t, a) {
				if ev.Plan == rec.ID && ev.ApplyTask == "apply" {
					after = append(after, ev.Event)
				}
			}
			if want := []string{"approval-requested", "approval-given", "apply-refused"}; !slices.Equal(after, want) {
				t.Errorf("events of the call = %v, want %v", after, want)
			}

			// The next plan is asked about with no note of a failure.
			mustPlan(t, a, prod)
			next := &asked{}
			if _, err := applyWith(a, prod, next); err != nil {
				t.Fatal(err)
			}
			if got := next.requests[0].Plan.Notes; len(got) != 0 {
				t.Errorf("notes after a refused claim = %q", got)
			}
		})
	}
}

func TestDependencyFailureConsumesPlan(t *testing.T) {
	src := strings.Replace(planSrc, "apply env:\n", "apply env: check\n", 1) + "\ncheck:\n    @exit 4\n"
	a, _ := planAdapter(t, src)
	rec := mustPlan(t, a, prod)
	res, err := applyWith(a, prod, &asked{})
	if err != nil || res.ExitCode == 0 {
		t.Fatalf("apply: %v, exit %d", err, res.ExitCode)
	}
	if planFileExists(a, "apply.done") {
		t.Error("the apply body ran after its dependency failed")
	}
	if state := planState(t, a, rec); state != planstore.StateFailed {
		t.Errorf("state = %v, want failed: the attempt is not given back", state)
	}
}

func TestApplyTampered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs chmod")
	}
	src := strings.Replace(planSrc, "    @echo applied >> apply.done\n", "    @chmod u+w \"$RUNE_PLAN_FILE\" && echo changed > \"$RUNE_PLAN_FILE\"\n", 1)
	a, _ := planAdapter(t, src)
	rec := mustPlan(t, a, prod)
	res, err := applyWith(a, prod, &asked{})
	if err != nil {
		t.Fatal(err)
	}
	want := "plan " + rec.ID + ": the plan file changed while it was being applied; recorded as failed and tampered\n"
	if res.ExitCode != ExitTaskFail || !strings.Contains(res.Stderr, want) {
		t.Errorf("exit %d, stderr %q, want exit 1 and %q", res.ExitCode, res.Stderr, want)
	}
	done, ok := planStoreOf(a).AttemptOf(rec)
	if !ok || done.State != planstore.StateFailed || !done.Tampered {
		t.Errorf("outcome = %+v", done)
	}
	events := planJournal(t, a)
	if last := events[len(events)-1]; last.Event != planstore.EventApplyFinished || !last.Tampered || last.State != "failed" {
		t.Errorf("last event = %+v", last)
	}
}

func TestCallListsRefusesApply(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	mustPlan(t, a, prod)
	before := len(planJournal(t, a))
	_, err := a.Call(context.Background(), "apply", prod)
	if want := `task "apply" applies a plan and runs only when a person approves that plan; nobody can be asked on this path`; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
	if planFileExists(a, "apply.done") || len(planJournal(t, a)) != before {
		t.Error("the body ran, or the refusal was journaled")
	}
}

func TestPolicyBeforePlan(t *testing.T) {
	src := strings.Replace(planSrc, `[applies("plan")]`, "[applies(\"plan\")]\n[agent-allow(env=\"staging\")]", 1)
	a, _ := planAdapter(t, src)
	who := &asked{}
	_, err := applyWith(a, prod, who)
	if want := `task "apply" is not allowed for agents with env="prod"; agent policy requires env="staging"`; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
	if len(who.requests) != 0 {
		t.Error("the person was asked about a call the policy refuses")
	}
	if _, err := os.Stat(filepath.Join(a.root, ".rune")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the store was touched: %v", err)
	}
}

func TestApplyWithConfirm(t *testing.T) {
	src := strings.Replace(planSrc, `[applies("plan")]`, `[confirm("Really?"), applies("plan")]`, 1)
	a, _ := planAdapter(t, src)
	mustPlan(t, a, prod)
	who := &asked{}
	res, err := applyWith(a, prod, who)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("apply: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	req := who.requests[0]
	if req.Plan == nil || len(req.Confirmations) != 1 || req.Confirmations[0] != (mcpserver.Confirmation{Task: "apply", Message: "Really?"}) {
		t.Errorf("request = %+v", req)
	}
	if !planFileExists(a, "apply.done") {
		t.Error("the body did not run: the approval did not cover the task's own [confirm]")
	}
}

func TestApplyGate(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	apply, plan := a.tasks["apply"], a.tasks["plan"]
	args := eval.Args{Values: map[string]string{"env": "prod"}}
	other := eval.Args{Values: map[string]string{"env": "staging"}}
	e := &engine{}
	if f := e.applyGate(plan, args); f != nil {
		t.Errorf("a task that applies nothing is held: %v", f)
	}
	want := `task "apply" applies a plan and was reached without an approved plan`
	if f := e.applyGate(apply, args); f == nil || f.Error() != want {
		t.Errorf("no attempt: %v, want %s", f, want)
	}
	e.attempt = &applyAttempt{
		task: "apply", argsKey: args.Key(),
		ticket: &planTicket{rec: planstore.Record{ID: "abc", Artifact: planstore.Digest{SHA256: "d"}}},
		claim:  &planstore.Attempt{Path: "/store/copy"},
	}
	if f := e.applyGate(apply, args); f != nil {
		t.Errorf("the claimed invocation is held: %v", f)
	}
	if f := e.applyGate(apply, other); f == nil {
		t.Error("an invocation with other arguments passes on another one's claim")
	}
	if got, want := e.planEnv(apply, args), []string{"RUNE_PLAN_FILE=/store/copy", "RUNE_PLAN_ID=abc", "RUNE_PLAN_DIGEST=sha256:d"}; !slices.Equal(got, want) {
		t.Errorf("planEnv = %v, want %v", got, want)
	}
	if got := e.planEnv(plan, args); got != nil {
		t.Errorf("planEnv of another task = %v", got)
	}
}

// The gate holds inside a run too: Execute never starts the body of a task
// that applies a plan without a claim.
func TestExecuteRefusesUnclaimedApply(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	var stderr bytes.Buffer
	e := &engine{tasks: a.tasks, plan: planRun, opts: Options{Stderr: &stderr, Stdout: &stderr}, ctx: context.Background(), workDir: a.workDir, root: a.root}
	err := e.Execute(a.tasks["apply"], eval.Args{Values: map[string]string{"env": "prod"}})
	if err == nil || !strings.Contains(stderr.String(), "was reached without an approved plan") {
		t.Errorf("Execute = %v, stderr %q", err, stderr.String())
	}
	if planFileExists(a, "apply.done") {
		t.Error("the body ran")
	}
}

func TestNoStoreWithoutAttributes(t *testing.T) {
	a, _ := planAdapter(t, "build:\n    @echo built > build.done\n")
	if a.planIdent != (planIdentity{}) {
		t.Errorf("identity of a Runefile without plans = %+v", a.planIdent)
	}
	if res, err := a.Call(context.Background(), "build", nil); err != nil || res.ExitCode != 0 {
		t.Fatalf("build: %v, exit %d", err, res.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(a.root, ".rune")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a Runefile with no plan touched the store: %v", err)
	}
	if usesPlans(a.tasks) {
		t.Error("usesPlans is true without the attributes")
	}
}

func TestAdapterTasksAppliesPlan(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	for _, info := range a.Tasks() {
		switch info.Name {
		case "apply":
			if info.AppliesPlan != "plan" || info.Destructive {
				t.Errorf("apply = %+v, want AppliesPlan plan and Destructive left to [confirm]", info)
			}
		case "plan":
			if info.AppliesPlan != "" {
				t.Errorf("plan = %+v", info)
			}
		}
	}
}

func TestJournalMasksArguments(t *testing.T) {
	const secret = "supersecretvalue123"
	a, _ := planAdapter(t, planSrc)
	a.baseEnv = append(os.Environ(), "MY_API_TOKEN="+secret)
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	args := map[string]string{"env": secret}
	rec := mustPlan(t, a, args)
	if _, err := applyWith(a, args, &asked{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"journal.jsonl", filepath.Join(rec.ID, "record.json"), filepath.Join(rec.ID, "outcome.json")} {
		data, err := os.ReadFile(filepath.Join(planDir(a), file))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s holds the secret", file)
		}
		for _, stored := range []string{"plan for", "Plan: 1 to add"} {
			if bytes.Contains(data, []byte(stored)) {
				t.Errorf("%s holds bytes of the plan or its summary", file)
			}
		}
	}
}

func TestPruneOnUse(t *testing.T) {
	a, clock := planAdapter(t, planSrc)
	old := mustPlan(t, a, map[string]string{"env": "staging"})
	clock.advance(2 * time.Hour)
	mustPlan(t, a, prod)
	if _, err := os.Stat(filepath.Join(planDir(a), old.ID, "artifact")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an expired plan keeps its artifact after the store was used: %v", err)
	}
}

func TestJournalFailureWarns(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	if err := os.MkdirAll(filepath.Join(planDir(a), "journal.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := a.Call(context.Background(), "plan", prod)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "warning: the plan journal was not written: ") {
		t.Errorf("stderr = %q, want a warning", res.Stderr)
	}
	if len(planRecords(t, a)) != 1 {
		t.Error("the plan was not stored")
	}
}

// A tool that is on the path and cannot be read is not reported as missing:
// adding it to the path would not help.
func TestCapturePlanToolThatCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a file the test can run and cannot read")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lockedtool"), []byte("x"), 0o111); err != nil {
		t.Fatal(err)
	}
	a, _ := planAdapter(t, "[plan(file=\"p\", summary=\"s\", tool=\"lockedtool\")]\nplan:\n    @echo x > p\n    @echo y > s\n")
	a.baseEnv = []string{"PATH=" + bin}
	if _, _, err := toolIdentity("lockedtool", "", a.baseEnv); err == nil || errors.Is(err, errToolNotFound) {
		t.Fatalf("toolIdentity = %v, want an error that is not errToolNotFound", err)
	}
	res, err := a.Call(context.Background(), "plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := `plan tool "lockedtool" of task "plan" cannot be identified: `; res.ExitCode == 0 || !strings.Contains(res.Stderr, want) {
		t.Errorf("exit %d, stderr = %q, want %q", res.ExitCode, res.Stderr, want)
	}
	if strings.Contains(res.Stderr, "was not found on the path") {
		t.Errorf("stderr = %q: the tool is on the path", res.Stderr)
	}
	if len(planRecords(t, a)) != 0 {
		t.Error("a plan was stored without the identity of its tool")
	}
}

func TestWorkspaceFileNotRemoved(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a directory the test cannot write to")
	}
	src := "[plan(file=\"p\", summary=\"s\")]\n[working-directory(\"infra\")]\nplan:\n    @echo x > p\n    @echo y > s\n    @chmod a-w .\n"
	a, _ := planAdapter(t, src)
	infra := filepath.Join(a.root, "infra")
	if err := os.Mkdir(infra, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(infra, 0o755) })
	res, err := a.Call(context.Background(), "plan", nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("plan: %v, exit %d, stderr %s", err, res.ExitCode, res.Stderr)
	}
	rec := planRecords(t, a)[0]
	if want := "warning: plan " + rec.ID + ": could not remove "; !strings.Contains(res.Stderr, want) {
		t.Errorf("stderr = %q, want %q", res.Stderr, want)
	}
}

func TestPlanTicketApproval(t *testing.T) {
	created := time.Date(2026, 11, 17, 10, 4, 11, 500, time.UTC)
	ticket := &planTicket{
		rec:     planstore.Record{ID: "id", PlanTask: "plan", Created: created, Artifact: planstore.Digest{SHA256: "d", Size: 7}},
		summary: "a secret line",
		notes:   []string{"note"},
	}
	got := ticket.approval(nil)
	if got.Created != "2026-11-17T10:04:11Z" || got.Summary != "a secret line" || got.Size != 7 || !slices.Equal(got.Notes, []string{"note"}) {
		t.Errorf("approval = %+v", got)
	}
	hidden := ticket.approval(func(s string) string { return strings.ReplaceAll(s, "secret", "***") })
	if hidden.Summary != "a *** line" {
		t.Errorf("masked summary = %q", hidden.Summary)
	}
}

func TestStoreReason(t *testing.T) {
	rec := planstore.Record{ID: "id", Expires: time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)}
	cases := []struct {
		err  error
		want string
	}{
		{&planstore.ArtifactError{What: "summary", Detail: "is missing"}, `the stored summary of plan id is missing; run "plan" again`},
		{planstore.ErrClaimed, `plan id was already used by another call`},
		{planstore.ErrExpired, `plan id expired at 2026-11-17T11:00:00Z; run "plan" again`},
		{errors.New("disk on fire"), `the plan store cannot be used: disk on fire`},
	}
	for _, c := range cases {
		if got := storeReason("plan", rec, c.err); got != c.want {
			t.Errorf("storeReason(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestFindPlanWithoutPlanTask(t *testing.T) {
	a, _ := planAdapter(t, "[applies(\"gone\")]\napply:\n    @echo \"$RUNE_PLAN_FILE\"\n")
	_, err := applyWith(a, nil, &asked{})
	if want := `task "apply" was not run: its plan task "gone" does not exist`; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

// The approval is of a plan made with one executable. An apply task whose
// own environment resolves the tool to another one is refused, though the
// plan task's environment has not changed.
func TestApplyRefusedForAnotherToolInItsOwnEnvironment(t *testing.T) {
	planTools, applyTools := t.TempDir(), t.TempDir()
	planEnv := fakeTool(t, planTools, "faketool", "version one")
	fakeTool(t, applyTools, "faketool", "version two")
	src := strings.Replace(planSrc, `summary="tfplan.txt")]`, `summary="tfplan.txt", tool="faketool")]`, 1)
	src = strings.Replace(src, `[applies("plan")]`, `[applies("plan")]`+"\n"+`[env("PATH", "`+filepath.ToSlash(applyTools)+`")]`, 1)
	a, _ := planAdapter(t, src)
	a.baseEnv = planEnv
	rec := mustPlan(t, a, prod)

	who := &asked{}
	_, err := applyWith(a, prod, who)
	want := fmt.Sprintf(`%splan %s no longer matches: the tool "faketool" changed since it was made; run "plan" again`, applyNotRun, rec.ID)
	if err == nil || err.Error() != want || len(who.requests) != 0 {
		t.Errorf("refusal = %v, %d requests\nwant      %s", err, len(who.requests), want)
	}
	if planFileExists(a, "apply.done") {
		t.Error("the apply body ran with another executable than the plan was made with")
	}
}

// An engine built without the identity of its load must not bind plans to
// nothing: every binding would then compare equal.
func TestNoPlanIdentity(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	a.planIdent = planIdentity{}
	res, err := a.Call(context.Background(), "plan", prod)
	if err != nil {
		t.Fatal(err)
	}
	if want := `the plan of task "plan" was not stored: this load of the Runefile has no plan identity`; res.ExitCode == 0 || !strings.Contains(res.Stderr, want) {
		t.Errorf("plan: exit %d, stderr %q, want %q", res.ExitCode, res.Stderr, want)
	}
	if recs := planRecords(t, a); len(recs) != 0 {
		t.Errorf("a plan was stored: %+v", recs)
	}
	who := &asked{}
	_, err = applyWith(a, prod, who)
	if want := `task "apply" was not run: the plan store cannot be used: this load of the Runefile has no plan identity`; err == nil || err.Error() != want || len(who.requests) != 0 {
		t.Errorf("apply: %v, want %s", err, want)
	}
}

func TestRealPath(t *testing.T) {
	dir := realPath(t.TempDir())
	if got := realPath(dir); got != dir {
		t.Errorf("a resolved path changed: %q, want %q", got, dir)
	}
	missing := filepath.Join(dir, "gone", "..", "gone")
	if got, want := realPath(missing), filepath.Join(dir, "gone"); got != want {
		t.Errorf("a path that does not exist = %q, want it cleaned to %q", got, want)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got := realPath(link); got != dir {
		t.Errorf("a link resolves to %q, want %q", got, dir)
	}
}
