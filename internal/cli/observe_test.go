package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/observe"
)

var recordNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

// observeState points the state directory at a fresh directory and returns
// the base of the stores.
func observeState(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	return filepath.Join(state, "rune", "observe")
}

func runObserve(t *testing.T, dir string, flags AgentObserveFlags, client string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	err = AgentObserve(Options{Cwd: dir, Stdout: &out, Stderr: &errb}, flags, client)
	return out.String(), errb.String(), err
}

// hookEvent is the JSON of one hook event of the Bash tool.
func hookEvent(name, id, cwd, command string, extra map[string]any) string {
	ev := map[string]any{
		"session_id": "s1", "cwd": cwd, "hook_event_name": name, "tool_name": "Bash",
		"tool_use_id": id, "tool_input": map[string]any{"command": command, "description": "Run it"},
	}
	for k, v := range extra {
		ev[k] = v
	}
	data, _ := json.Marshal(ev)
	return string(data)
}

// record runs the recorder on one input for the project at dir, which may
// be "" for none, and checks that it printed nothing.
func record(t *testing.T, dir, input string) {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", dir)
	var out, errb bytes.Buffer
	recordEvent(Options{Stdin: strings.NewReader(input), Stdout: &out, Stderr: &errb}, time.Second, recordNow)
	if out.Len()+errb.Len() > 0 {
		t.Errorf("the recorder printed %q %q", out.String(), errb.String())
	}
}

// storeOf is the store of the project at dir.
func storeOf(t *testing.T, base, dir string) *observe.Store {
	t.Helper()
	root, _, err := observeRoot("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return observe.OpenStore(base, root)
}

func readStore(t *testing.T, base, dir string) []observe.Record {
	t.Helper()
	s := storeOf(t, base, dir)
	recs, _, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func drops(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	d, err := observe.ReadDiagnostics(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for reason, c := range d.Reasons {
		out[reason] = c.N
	}
	return out
}

func TestAgentObserveNewProject(t *testing.T) {
	observeState(t)
	dir := t.TempDir()
	stdout, stderr, err := runObserve(t, dir, AgentObserveFlags{}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || !strings.HasPrefix(stderr, ".claude/settings.local.json: created\nobserving: ") {
		t.Errorf("stdout %q\nstderr %s", stdout, stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil || strings.Count(string(data), `"command": "rune agent record"`) != 3 {
		t.Errorf("settings: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
		t.Error("the shared settings were written")
	}
	_, stderr, err = runObserve(t, dir, AgentObserveFlags{}, "claude")
	if err != nil || !strings.HasPrefix(stderr, ".claude/settings.local.json: unchanged\n") {
		t.Errorf("second run: %v\n%s", err, stderr)
	}
}

func TestAgentObserveDryRun(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, err := runObserve(t, dir, AgentObserveFlags{DryRun: true}, "claude")
	if err != nil || !strings.Contains(stdout, "+++ b/.claude/settings.local.json") || !strings.Contains(stderr, "would be created") {
		t.Errorf("%v\n%s\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("a dry run wrote")
	}
}

func TestAgentObserveErrors(t *testing.T) {
	_, _, err := runObserve(t, t.TempDir(), AgentObserveFlags{}, "codex")
	wantErr[*UsageError](t, err, `rune agent observe supports claude only, not "codex"`)

	dir := importProject(t, map[string]string{".claude/settings.local.json": `{"a": `})
	_, stderr, err := runObserve(t, dir, AgentObserveFlags{}, "claude")
	wantErr[*ValidationError](t, err, ".claude/settings.local.json is not valid")
	if !strings.Contains(stderr, ".claude/settings.local.json:1:") {
		t.Errorf("no position:\n%s", stderr)
	}

	dir = importProject(t, map[string]string{".claude/settings.local.json": `{"hooks": []}`})
	_, stderr, err = runObserve(t, dir, AgentObserveFlags{}, "claude")
	wantErr[*UsageError](t, err, "agent observe: 1 conflict(s), nothing written")
	if !strings.Contains(stderr, "hooks is not an object") {
		t.Errorf("conflict not shown:\n%s", stderr)
	}

	_, _, err = runObserve(t, filepath.Join(t.TempDir(), "missing"), AgentObserveFlags{}, "claude")
	if err == nil {
		t.Error("a missing directory")
	}
	bad := importProject(t, map[string]string{"Runefile": "x:\n    y\n", ".runefile": "x:\n    y\n"})
	if _, _, err := runObserve(t, bad, AgentObserveFlags{}, "claude"); err == nil {
		t.Error("two Runefiles in one directory")
	}
}

func TestAgentObserveRefusesLinkOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".claude")); err != nil {
		t.Fatal(err)
	}
	_, _, err := runObserve(t, dir, AgentObserveFlags{}, "claude")
	if _, ok := err.(*UsageError); !ok || !strings.Contains(err.Error(), "rune agent observe writes only inside the project") {
		t.Errorf("got %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Error("wrote through the link")
	}
}

func TestAgentObserveWithRunefile(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, "sub/x": ""})
	if _, _, err := runObserve(t, filepath.Join(dir, "sub"), AgentObserveFlags{}, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.local.json")); err != nil {
		t.Errorf("not written in the Runefile's directory: %v", err)
	}
}

func TestAgentRecordFixtures(t *testing.T) {
	base := observeState(t)
	dir := t.TempDir()
	fixtures, err := filepath.Glob(filepath.Join("..", "observe", "testdata", "hooks", "*"))
	if err != nil || len(fixtures) < 10 {
		t.Fatalf("fixtures %v %v", fixtures, err)
	}
	for _, f := range fixtures {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// The fixtures name a project of their own; the event happens here.
		record(t, dir, strings.ReplaceAll(string(data), "/home/user/my-project", jsonPath(dir)))
	}
	recs := readStore(t, base, dir)
	if len(recs) != 6 {
		t.Errorf("%d records, want 6: %+v", len(recs), recs)
	}
	store := storeOf(t, base, dir)
	got := drops(t, store.Dir())
	if got[observe.ReasonInvalidEvent] != 1 || got[observe.ReasonNotJSON] != 1 || len(got) != 2 {
		t.Errorf("drops %v", got)
	}
	for _, r := range recs {
		if strings.Contains(r.Command, "passed") || r.Cwd != "" {
			t.Errorf("record %+v", r)
		}
	}
}

func TestAgentRecordDrops(t *testing.T) {
	t.Run("no project", func(t *testing.T) {
		base := observeState(t)
		record(t, "", hookEvent("PreToolUse", "t1", "/x", "make", nil))
		record(t, "relative", hookEvent("PreToolUse", "t2", "/x", "make", nil))
		if got := drops(t, base); got[observe.ReasonNoProject] != 2 {
			t.Errorf("drops %v", got)
		}
		// Another tool's event is not counted anywhere.
		record(t, "", `{"hook_event_name":"PostToolUse","tool_name":"Write"}`)
		if got := drops(t, base); got[observe.ReasonNoProject] != 2 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("outside the project", func(t *testing.T) {
		base := observeState(t)
		dir := t.TempDir()
		record(t, dir, hookEvent("PreToolUse", "t1", t.TempDir(), "make", nil))
		if got := drops(t, storeOf(t, base, dir).Dir()); got[observe.ReasonOutsideProject] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("a here-document", func(t *testing.T) {
		base := observeState(t)
		dir := t.TempDir()
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "cat <<EOF\nAPI_TOKEN=x\nEOF", nil))
		if got := drops(t, storeOf(t, base, dir).Dir()); got[observe.ReasonHereDocument] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("input that never ends", func(t *testing.T) {
		base := observeState(t)
		dir := t.TempDir()
		t.Setenv("CLAUDE_PROJECT_DIR", dir)
		r, w := io.Pipe()
		defer func() { _ = w.Close() }()
		start := time.Now()
		recordEvent(Options{Stdin: r}, 50*time.Millisecond, recordNow)
		if time.Since(start) > 5*time.Second {
			t.Error("the recorder waited too long")
		}
		if got := drops(t, storeOf(t, base, dir).Dir()); got[observe.ReasonSlowInput] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("a store that cannot be written", func(t *testing.T) {
		base := observeState(t)
		dir := t.TempDir()
		store := storeOf(t, base, dir)
		if err := os.MkdirAll(filepath.Join(store.Dir(), "events.jsonl"), 0o700); err != nil {
			t.Fatal(err)
		}
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "make", nil))
		if got := drops(t, store.Dir()); got[observe.ReasonStoreError] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("a busy store", func(t *testing.T) {
		base := observeState(t)
		dir := t.TempDir()
		store := storeOf(t, base, dir)
		if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(store.Dir(), ".lock")
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// The other writer lets go after the recorder gave up on the record,
		// so the count gets the lock.
		release := time.AfterFunc(2200*time.Millisecond, func() { _ = os.Remove(lock) })
		defer release.Stop()
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "make", nil))
		if got := drops(t, store.Dir()); got[observe.ReasonStoreBusy] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("a store inside the project", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", filepath.Join(dir, ".state"))
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "make", nil))
		if _, err := os.Stat(filepath.Join(dir, ".state")); err == nil {
			t.Error("the store was created inside the project")
		}
	})
	t.Run("no state directory", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		t.Setenv("LocalAppData", "")
		t.Setenv("USERPROFILE", "")
		record(t, t.TempDir(), hookEvent("PreToolUse", "t1", "/x", "make", nil))
	})
}

func TestAgentRecordReturnsNil(t *testing.T) {
	observeState(t)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	if err := AgentRecord(Options{Stdin: strings.NewReader("{")}); err != nil {
		t.Errorf("AgentRecord = %v", err)
	}
}

// A project reached through a link records into the store that rune suggest
// reads from the resolved directory.
func TestAgentRecordThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	base := observeState(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	record(t, link, hookEvent("PostToolUse", "t1", filepath.Join(link, "web"), "npm test", nil))
	recs := readStore(t, base, dir)
	if len(recs) != 1 || recs[0].Cwd != "web" {
		t.Errorf("records %+v", recs)
	}
}

func TestRecordSecrets(t *testing.T) {
	t.Setenv("API_TOKEN", "envtoken123")
	t.Run("no Runefile", func(t *testing.T) {
		s, err := recordSecrets(Options{}, "")
		if err != nil {
			t.Fatal(err)
		}
		if !s.Names("API_TOKEN") || s.Names("DB_URL") || s.Names("GIT_AUTHOR_NAME") {
			t.Error("names")
		}
		if got := s.Values.MaskString("x envtoken123 y"); got != "x *** y" {
			t.Errorf("values: %q", got)
		}
	})
	t.Run("a Runefile", func(t *testing.T) {
		t.Setenv("DB_URL", "postgres-url-value")
		t.Setenv("PUBLIC_TOKEN", "publictokenvalue")
		dir := importProject(t, map[string]string{
			"Runefile": "set secrets := [\"DB_URL\"]\nset unmasked := [\"PUBLIC_TOKEN\"]\nset dotenv := \".env\"\n\n" +
				"[env(\"DEPLOY_SECRET\", \"attrkeyvalue\")]\nx:\n    @echo x\n",
			".env": "SERVICE_PASSWORD=dotenvsecret\n",
		})
		s, err := recordSecrets(Options{Cwd: dir}, filepath.Join(dir, "Runefile"))
		if err != nil {
			t.Fatal(err)
		}
		if !s.Names("DB_URL") || s.Names("PUBLIC_TOKEN") || !s.Names("API_TOKEN") {
			t.Error("names")
		}
		for value, want := range map[string]string{
			"postgres-url-value": "***", "publictokenvalue": "publictokenvalue", "envtoken123": "***",
			"attrkeyvalue": "***", "dotenvsecret": "***",
		} {
			if got := s.Values.MaskString(value); got != want {
				t.Errorf("%s masked as %q", value, got)
			}
		}
	})
	t.Run("a Runefile that does not load", func(t *testing.T) {
		base := observeState(t)
		dir := importProject(t, map[string]string{"Runefile": "x:\n    @echo {{nope}}\n"})
		if _, err := recordSecrets(Options{Cwd: dir}, filepath.Join(dir, "Runefile")); err == nil {
			t.Fatal("no error")
		}
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "make", nil))
		if got := drops(t, storeOf(t, base, dir).Dir()); got[observe.ReasonRunefileUnreadable] != 1 {
			t.Errorf("drops %v", got)
		}
	})
	t.Run("two Runefiles", func(t *testing.T) {
		base := observeState(t)
		dir := importProject(t, map[string]string{"Runefile": "x:\n    y\n", ".runefile": "x:\n    y\n"})
		record(t, dir, hookEvent("PreToolUse", "t1", dir, "make", nil))
		resolved, _ := filepath.EvalSymlinks(dir)
		if got := drops(t, observe.OpenStore(base, resolved).Dir()); got[observe.ReasonRunefileUnreadable] != 1 {
			t.Errorf("drops %v", got)
		}
		if runtime.GOOS == "windows" {
			return
		}
		// Through a link, the count lands in the store of the resolved
		// directory too.
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		record(t, link, hookEvent("PreToolUse", "t2", link, "make", nil))
		if got := drops(t, observe.OpenStore(base, resolved).Dir()); got[observe.ReasonRunefileUnreadable] != 2 {
			t.Errorf("drops through a link %v", got)
		}
	})
}

func TestAgentObserveRemove(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runObserve(t, dir, AgentObserveFlags{}, "claude"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, ".claude", "settings.local.json")
	stdout, stderr, err := runObserve(t, dir, AgentObserveFlags{Remove: true, DryRun: true}, "claude")
	if err != nil || !strings.Contains(stdout, `-    "PreToolUse"`) || !strings.HasPrefix(stderr, ".claude/settings.local.json: would be updated\nnot observing") {
		t.Errorf("dry run: %v\n%s\n%s", err, stdout, stderr)
	}
	if !strings.Contains(readFile(t, settings), "rune agent record") {
		t.Error("the dry run removed the handlers")
	}
	_, stderr, err = runObserve(t, dir, AgentObserveFlags{Remove: true}, "claude")
	if err != nil || !strings.HasPrefix(stderr, ".claude/settings.local.json: updated\nnot observing") {
		t.Errorf("%v\n%s", err, stderr)
	}
	if got := readFile(t, settings); strings.Contains(got, "rune agent record") {
		t.Errorf("still installed:\n%s", got)
	}
}
