package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Spec 058: the built binary records hook events replayed from fixtures,
// lists what was repeated, and applies a suggestion. No recorded command is
// ever run.

// observeProject is a project directory with no Runefile, the environment
// that names it to the recorder, and the state directory.
type observeProject struct {
	dir, state string
	env        []string
	ids        int
}

func newObserveProject(t *testing.T, extra ...string) *observeProject {
	t.Helper()
	p := &observeProject{dir: t.TempDir(), state: t.TempDir()}
	p.env = append([]string{"XDG_STATE_HOME=" + p.state, "CLAUDE_PROJECT_DIR=" + p.dir}, extra...)
	return p
}

// replay pipes the events of n calls of a command into `rune agent record`:
// the event before each call and, for "ok" or "fail", the one after it.
func (p *observeProject) replay(t *testing.T, n int, command, cwd, outcome string) {
	t.Helper()
	if cwd == "" {
		cwd = p.dir
	}
	for range n {
		p.ids++
		id := "toolu_" + strconv.Itoa(p.ids)
		events := []map[string]any{{"hook_event_name": "PreToolUse"}}
		switch outcome {
		case "ok":
			events = append(events, map[string]any{"hook_event_name": "PostToolUse", "tool_response": map[string]any{"stdout": "out", "stderr": ""}})
		case "fail":
			events = append(events, map[string]any{"hook_event_name": "PostToolUseFailure", "error": "Exit code 1\nboom", "is_interrupt": false})
		}
		for _, ev := range events {
			ev["session_id"], ev["tool_name"], ev["tool_use_id"], ev["cwd"] = "session-1", "Bash", id, cwd
			ev["tool_input"] = map[string]any{"command": command, "description": "Run it"}
			data, err := json.Marshal(ev)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(runeBin, "agent", "record")
			cmd.Dir = p.dir
			cmd.Env = append(os.Environ(), p.env...)
			cmd.Stdin = bytes.NewReader(data)
			out, err := cmd.CombinedOutput()
			if err != nil || len(out) > 0 {
				t.Fatalf("rune agent record: %v %q", err, out)
			}
		}
	}
}

func (p *observeProject) rune(t *testing.T, args ...string) result {
	t.Helper()
	return run(t, p.dir, p.env, args...)
}

var suggestionID = regexp.MustCompile(`\[([0-9a-f]{8})\] the agent ran this (\d+) times`)

// suggested returns the IDs of the listing by the command they hold.
func suggested(t *testing.T, listing string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	blocks := strings.Split(listing, "\n\n")
	for _, b := range blocks {
		m := suggestionID.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		lines := strings.Split(b, "\n")
		ids[strings.TrimSpace(lines[1])] = m[1]
	}
	return ids
}

func TestSuggestColdStart(t *testing.T) {
	p := newObserveProject(t)
	if r := p.rune(t, "agent", "observe", "claude"); r.code != 0 || !strings.Contains(r.stderr, ".claude/settings.local.json: created") {
		t.Fatalf("observe: %d %s", r.code, r.stderr)
	}
	p.replay(t, 12, "go test -run TestX ./pkg/...", "", "ok")
	p.replay(t, 2, "go test -run TestX ./pkg/...", "", "fail")
	p.replay(t, 20, "touch SENTINEL", "", "ok")
	r := p.rune(t, "suggest")
	if r.code != 0 {
		t.Fatalf("suggest: %d %s", r.code, r.stderr)
	}
	for _, want := range []string{"the agent ran this 14 times (12 succeeded, 2 failed)", "the agent ran this 20 times (20 succeeded)", "Recorded 34 commands in 1 session"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("no %q in\n%s", want, r.stdout)
		}
	}
	ids := suggested(t, r.stdout)
	if p.rune(t, "suggest", "apply", ids["touch SENTINEL"], "--dry-run").code != 0 {
		t.Error("dry run failed")
	}
	if _, err := os.Stat(filepath.Join(p.dir, "SENTINEL")); err == nil {
		t.Fatal("a recorded command was run")
	}
}

func TestSuggestApply(t *testing.T) {
	p := newObserveProject(t)
	p.replay(t, 14, "go test -run TestX ./pkg/...", "", "ok")
	ids := suggested(t, p.rune(t, "suggest").stdout)
	id := ids["go test -run TestX ./pkg/..."]
	if id == "" {
		t.Fatalf("ids %v", ids)
	}
	r := p.rune(t, "suggest", "apply", id, "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "+go-test:") {
		t.Fatalf("dry run: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(p.dir, "Runefile")); err == nil {
		t.Fatal("the dry run wrote the Runefile")
	}
	if r := p.rune(t, "suggest", "apply", id); r.code != 0 || !strings.HasPrefix(r.stderr, "Runefile: created, added go-test") {
		t.Fatalf("apply: %d %s", r.code, r.stderr)
	}
	if r := p.rune(t, "analyze"); r.code != 0 || !strings.Contains(r.stdout+r.stderr, "0 errors, 0 warnings") {
		t.Errorf("analyze: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := p.rune(t, "suggest"); !strings.Contains(r.stdout, "Already tasks: go-test (14 runs).") || suggestionID.MatchString(r.stdout) {
		t.Errorf("after apply:\n%s", r.stdout)
	}
}

func TestSuggestSecrets(t *testing.T) {
	p := newObserveProject(t, "DEPLOY_SECRET=s3cr3tvalue")
	secrets := []string{"abc123xyz", "hunter22", "tok_live_9", "zzz111", "pw0rd99", "s3cr3tvalue", "heredocsecret"}
	for _, cmd := range []string{
		"API_TOKEN=abc123xyz deploy",
		"deploy --password=hunter22 --token tok_live_9",
		`curl -H "Authorization: Bearer zzz111" https://api.example.com`,
		"psql postgres://app:pw0rd99@db/app",
		`echo "s3cr3tvalue"`,
		"cat <<EOF > .env\nTOKEN=heredocsecret\nEOF",
		"make build",
	} {
		p.replay(t, 3, cmd, "", "ok")
	}
	listing := p.rune(t, "suggest")
	if !strings.Contains(listing.stdout, "Not recorded: 6 events (here-document 6).") {
		t.Errorf("the here-document is not counted:\n%s", listing.stdout)
	}
	outputs := []string{listing.stdout, listing.stderr}
	for _, id := range suggested(t, listing.stdout) {
		r := p.rune(t, "suggest", "apply", id, "--dry-run")
		outputs = append(outputs, r.stdout, r.stderr)
	}
	ids := suggested(t, listing.stdout)
	if id := ids["make build"]; id == "" {
		t.Errorf("ids %v", ids)
	} else if r := p.rune(t, "suggest", "apply", id); r.code != 0 {
		t.Errorf("apply: %s", r.stderr)
	}
	if data, err := os.ReadFile(filepath.Join(p.dir, "Runefile")); err == nil {
		outputs = append(outputs, string(data))
	}
	err := filepath.WalkDir(p.state, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		outputs = append(outputs, string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		for _, out := range outputs {
			if strings.Contains(out, secret) {
				t.Errorf("%q is in %q", secret, out)
			}
		}
	}
}

func TestSuggestFamily(t *testing.T) {
	p := newObserveProject(t)
	p.replay(t, 5, "go test -run TestX ./pkg/...", "", "ok")
	p.replay(t, 3, "go test -run TestY ./pkg/...", "", "ok")
	p.replay(t, 1, "go test -run TestZ ./pkg/...", "", "ok")
	r := p.rune(t, "suggest")
	m := suggestionID.FindStringSubmatch(r.stdout)
	if m == nil || m[2] != "9" || !strings.Contains(r.stdout, `go-test run="TestX":`) || !strings.Contains(r.stdout, "go test -run {{quote(run)}} ./pkg/...") {
		t.Fatalf("listing:\n%s", r.stdout)
	}
	if r := p.rune(t, "suggest", "apply", m[1]); r.code != 0 {
		t.Fatalf("apply: %s", r.stderr)
	}
	if r := p.rune(t, "analyze"); r.code != 0 || !strings.Contains(r.stdout+r.stderr, "0 errors, 0 warnings") {
		t.Errorf("analyze: %d\n%s%s", r.code, r.stdout, r.stderr)
	}

	// The value reaches the shell as one word, whatever it holds: the
	// person runs the task they applied, with a harmless body.
	q := newObserveProject(t)
	q.replay(t, 2, "echo hello TestX", "", "ok")
	q.replay(t, 2, "echo hello TestY", "", "ok")
	m = suggestionID.FindStringSubmatch(q.rune(t, "suggest").stdout)
	if m == nil {
		t.Fatal("no suggestion")
	}
	if r := q.rune(t, "suggest", "apply", m[1]); r.code != 0 {
		t.Fatalf("apply: %s", r.stderr)
	}
	r = q.rune(t, "echo-hello", "Test A;echo injected")
	if r.code != 0 || r.stdout != "hello Test A;echo injected\n" {
		t.Errorf("run: %d %q %q", r.code, r.stdout, r.stderr)
	}
}

func TestSuggestOff(t *testing.T) {
	p := newObserveProject(t)
	settings := filepath.Join(p.dir, ".claude", "settings.local.json")
	mine := `{
  "hooks": {
    "PostToolUse": [
      { "matcher": "Write", "hooks": [{ "type": "command", "command": "prettier --write" }] }
    ]
  },
  "model": "opus"
}
`
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := p.rune(t, "agent", "observe", "claude"); r.code != 0 || !strings.Contains(r.stderr, "updated") {
		t.Fatalf("observe: %s", r.stderr)
	}
	installed, _ := os.ReadFile(settings)
	if r := p.rune(t, "agent", "observe", "claude"); r.code != 0 || !strings.Contains(r.stderr, "unchanged") {
		t.Errorf("second run: %s", r.stderr)
	}
	if again, _ := os.ReadFile(settings); !bytes.Equal(again, installed) {
		t.Error("the second run changed the file")
	}
	p.replay(t, 1, "make", "", "ok")
	if r := p.rune(t, "agent", "observe", "claude", "--remove"); r.code != 0 || !strings.Contains(r.stderr, "not observing") {
		t.Errorf("remove: %s", r.stderr)
	}
	removed, _ := os.ReadFile(settings)
	if string(removed) != mine {
		t.Errorf("after removal:\n%s\nwant\n%s", removed, mine)
	}
	if r := p.rune(t, "suggest", "forget"); r.code != 0 || !strings.HasPrefix(r.stderr, "deleted 2 records") {
		t.Errorf("forget: %s", r.stderr)
	}
}
