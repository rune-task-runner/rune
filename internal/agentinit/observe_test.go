package agentinit

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func planObserve(t *testing.T, files map[string][]byte) *ObserveResult {
	t.Helper()
	res, err := PlanObserve(ObserveInputs{Files: files, ShownDir: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decoded(t *testing.T, doc []byte) any {
	t.Helper()
	root, err := parseJSON(ObservePath, doc)
	if err != nil {
		t.Fatalf("%v:\n%s", err, doc)
	}
	return decodeJSON(root)
}

const newObserveFile = `{
  "hooks": {
    "PreToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }],
    "PostToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }],
    "PostToolUseFailure": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "rune agent record", "async": true }] }]
  }
}
`

func TestPlanObserveNewFile(t *testing.T) {
	res := planObserve(t, nil)
	if len(res.Conflicts) != 0 || res.File.Status() != "created" || res.File.Path != ObservePath {
		t.Fatalf("got %+v", res)
	}
	if string(res.File.New) != newObserveFile {
		t.Errorf("new file:\n%s\nwant\n%s", res.File.New, newObserveFile)
	}
	if got := entryStates(res.File); got != "hooks.PreToolUse=missing hooks.PostToolUse=missing hooks.PostToolUseFailure=missing" {
		t.Errorf("entries: %s", got)
	}
	// A second plan over the result changes nothing.
	again := planObserve(t, map[string][]byte{ObservePath: res.File.New})
	if again.File.Status() != "unchanged" || len(again.File.Changes) != 0 {
		t.Errorf("second run: %s %q", again.File.Status(), again.File.Changes)
	}
	if got := entryStates(again.File); got != "hooks.PreToolUse=present hooks.PostToolUse=present hooks.PostToolUseFailure=present" {
		t.Errorf("second run entries: %s", got)
	}
}

// The person's own hooks, settings, comments and layout stay; only Rune's
// groups are added, and the permission rules are never touched.
func TestPlanObserveKeepsTheRest(t *testing.T) {
	old := `// my settings
{
  "permissions": { "allow": ["Read"], "deny": ["WebFetch"] },
  "hooks": {
    "PostToolUse": [
      { "matcher": "Write", "hooks": [{ "type": "command", "command": "prettier --write" }] },
    ],
  },
  "model": "opus",
}
`
	res := planObserve(t, map[string][]byte{ObservePath: []byte(old)})
	if res.File.Status() != "updated" || len(res.Conflicts) != 0 {
		t.Fatalf("got %+v", res)
	}
	got := string(res.File.New)
	for _, kept := range []string{"// my settings\n", `"permissions": { "allow": ["Read"], "deny": ["WebFetch"] },`, `{ "matcher": "Write", "hooks": [{ "type": "command", "command": "prettier --write" }] },`, `"model": "opus",`} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q:\n%s", kept, got)
		}
	}
	before, after := decoded(t, []byte(old)).(map[string]any), decoded(t, res.File.New).(map[string]any)
	if !reflect.DeepEqual(before["permissions"], after["permissions"]) {
		t.Errorf("permissions changed: %v", after["permissions"])
	}
	post := after["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(post) != 2 || post[0].(map[string]any)["matcher"] != "Write" {
		t.Errorf("PostToolUse = %v", post)
	}
	for _, f := range ObservePaths() {
		if f == claudeSettingsPath && res.File.Path == f {
			t.Error("the shared settings are planned")
		}
	}
}

// A handler of Rune's with keys of the person's counts as installed.
func TestPlanObserveHandlerWithOtherKeys(t *testing.T) {
	old := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rune agent record", "timeout": 5}]}]}}`
	res := planObserve(t, map[string][]byte{ObservePath: []byte(old)})
	if got := entryStates(res.File); got != "hooks.PreToolUse=present hooks.PostToolUse=missing hooks.PostToolUseFailure=missing" {
		t.Errorf("entries: %s", got)
	}
	if !strings.HasPrefix(string(res.File.New), `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rune agent record", "timeout": 5}]}]`) {
		t.Errorf("the handler was changed:\n%s", res.File.New)
	}
}

func TestPlanObserveByteOrderMark(t *testing.T) {
	res := planObserve(t, map[string][]byte{ObservePath: []byte("\xEF\xBB\xBF{}")})
	if !strings.HasPrefix(string(res.File.New), "\xEF\xBB\xBF{") || len(res.Conflicts) != 0 {
		t.Errorf("got %q", res.File.New)
	}
}

func TestPlanObserveConflicts(t *testing.T) {
	tests := map[string]struct{ doc, msg string }{
		"not an object":       {`[1]`, ObservePath + " is not an object; rune agent observe cannot add to it"},
		"hooks not an object": {`{"hooks": []}`, "hooks is not an object; rune agent observe cannot add to it"},
		"event not a list":    {`{"hooks": {"PreToolUse": {}}}`, "hooks.PreToolUse is not a list; rune agent observe cannot add to it"},
		"group hooks not a list": {
			`{"hooks": {"PostToolUse": [{"matcher": "Bash", "hooks": "x"}]}}`,
			"a group of hooks.PostToolUse has hooks that is not a list; rune agent observe cannot read it",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res := planObserve(t, map[string][]byte{ObservePath: []byte(tt.doc)})
			if len(res.Conflicts) != 1 || res.Conflicts[0].Msg != tt.msg || res.Conflicts[0].Forceable {
				t.Fatalf("conflicts %+v", res.Conflicts)
			}
			if res.File.Status() != "unchanged" || res.File.Changes != nil {
				t.Errorf("a file with a conflict is changed: %s", res.File.New)
			}
		})
	}
	// Other groups and handlers that are not objects are the person's.
	res := planObserve(t, map[string][]byte{ObservePath: []byte(`{"hooks": {"PreToolUse": [1, {"matcher": "x"}, {"hooks": [2]}]}}`)})
	if len(res.Conflicts) != 0 || res.File.Status() != "updated" {
		t.Errorf("got %+v", res.Conflicts)
	}
}

func TestPlanObserveInvalidFile(t *testing.T) {
	for _, path := range ObservePaths() {
		_, err := PlanObserve(ObserveInputs{Files: map[string][]byte{path: []byte(`{"a": `)}})
		var bad *ParseError
		if !errors.As(err, &bad) || bad.File != path {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestPlanObserveNotes(t *testing.T) {
	start := "start Claude Code in /p, the project this file belongs to; commands are recorded for that project."
	deny := "Claude Code's settings deny the Bash tool here, so the agent runs no shell command and nothing is recorded. Rune does not change that rule."
	tests := map[string]struct {
		files map[string][]byte
		deny  bool
	}{
		"no rule":               {nil, false},
		"Bash in shared":        {map[string][]byte{claudeSettingsPath: []byte(`{"permissions": {"deny": ["Bash"]}}`)}, true},
		"Bash(*) in local":      {map[string][]byte{ObservePath: []byte(`{"permissions": {"deny": ["Bash( * )"]}}`)}, true},
		"one command only":      {map[string][]byte{claudeSettingsPath: []byte(`{"permissions": {"deny": ["Bash(git *)"]}}`)}, false},
		"marked shared file":    {map[string][]byte{claudeSettingsPath: []byte("\xEF\xBB\xBF{\"permissions\": {\"deny\": [\"Bash\"]}}")}, true},
		"permissions elsewhere": {map[string][]byte{claudeSettingsPath: []byte(`{"permissions": 1}`)}, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res := planObserve(t, tt.files)
			want := []string{start}
			if tt.deny {
				want = []string{deny, start}
			}
			if !reflect.DeepEqual(res.Notes, want) {
				t.Errorf("notes %q", res.Notes)
			}
			if tt.files[claudeSettingsPath] != nil && res.File.Path == claudeSettingsPath {
				t.Error("the shared settings would be written")
			}
		})
	}
}

func TestObserveReport(t *testing.T) {
	res := planObserve(t, nil)
	want := ".claude/settings.local.json: created\n" +
		"observing: the shell commands Claude Code runs in this project are recorded on this\n" +
		"  machine, without their output, for up to 30 days. See them with: rune suggest\n" +
		"note: start Claude Code in /p, the project this file belongs to; commands are recorded for that project.\n"
	if got := ObserveReport(res, false); got != want {
		t.Errorf("report:\n%s", got)
	}
	if got := ObserveReport(res, true); !strings.HasPrefix(got, ".claude/settings.local.json: would be created\n") || !strings.HasSuffix(got, "dry run: nothing written\n") {
		t.Errorf("dry run:\n%s", got)
	}
	same := planObserve(t, map[string][]byte{ObservePath: res.File.New})
	if got := ObserveReport(same, true); !strings.HasPrefix(got, ".claude/settings.local.json: unchanged\n") {
		t.Errorf("unchanged dry run:\n%s", got)
	}
	if diff := ObserveDiff(res); !strings.Contains(diff, "+++ b/.claude/settings.local.json") || !strings.Contains(diff, `+    "PreToolUse"`) {
		t.Errorf("diff:\n%s", diff)
	}
}

func TestJSONAppendValueErrors(t *testing.T) {
	if _, err := jsonAppendValue([]byte(`{"a": `), []string{"hooks"}, 1); err == nil {
		t.Error("an invalid document")
	}
	if _, err := jsonAppendValue([]byte(`[]`), []string{"hooks"}, 1); err == nil {
		t.Error("a document that is not an object")
	}
	if _, err := jsonAppendValue([]byte(`{"hooks": 1}`), []string{"hooks", "x"}, 1); err == nil {
		t.Error("a value on the way that is not an object")
	}
}

func TestVerifyObserve(t *testing.T) {
	if err := verifyObserve([]byte(`{`), []byte(`{}`), true); err == nil {
		t.Error("an old text that does not parse")
	}
	if err := verifyObserve([]byte(`{}`), []byte(`{`), true); err == nil {
		t.Error("a new text that does not parse")
	}
	if err := verifyObserve([]byte(`[]`), []byte(`[]`), true); err == nil {
		t.Error("an old text that is not an object")
	}
	if err := verifyObserve([]byte(`{}`), []byte(`{"x": 1}`), true); err == nil {
		t.Error("a change that is not Rune's")
	}
}

// Spec 058 FR-004: removal takes out exactly Rune's handlers and what they
// leave empty, and keeps everything else.
func TestPlanObserveRemove(t *testing.T) {
	t.Run("after an install", func(t *testing.T) {
		mine := "{\n  \"hooks\": {\n    \"PostToolUse\": [\n      { \"matcher\": \"Write\", \"hooks\": [{ \"type\": \"command\", \"command\": \"fmt\" }] }\n    ]\n  },\n  \"model\": \"opus\"\n}\n"
		installed := planObserve(t, map[string][]byte{ObservePath: []byte(mine)}).File.New
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: installed}, Remove: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(res.File.New) != mine || res.File.Status() != "updated" || len(res.File.Changes) != 3 {
			t.Errorf("got %s %q:\n%s", res.File.Status(), res.File.Changes, res.File.New)
		}
		if got := ObserveReport(res, false); !strings.Contains(got, "not observing: what was recorded stays until it expires; delete it with: rune suggest forget\n") {
			t.Errorf("report:\n%s", got)
		}
	})
	t.Run("a new file leaves the hooks object", func(t *testing.T) {
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(newObserveFile)}, Remove: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(res.File.New) != "{\n  \"hooks\": {}\n}\n" {
			t.Errorf("got %q", res.File.New)
		}
	})
	t.Run("only Rune's handler in a group of the person's", func(t *testing.T) {
		doc := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "audit.sh"}, {"type": "command", "command": "rune agent record"}]}]}}`
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(doc)}, Remove: true})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "audit.sh"}]}]}}`
		if string(res.File.New) != want {
			t.Errorf("got %s", res.File.New)
		}
	})
	t.Run("nothing to remove", func(t *testing.T) {
		for _, doc := range []string{"", `{}`, `{"hooks": 1}`, `{"hooks": {"PreToolUse": 1, "PostToolUse": [1, {"hooks": 2}, {"hooks": [3]}]}}`} {
			res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(doc)}, Remove: true})
			if err != nil || res.File.Status() != "unchanged" || len(res.Conflicts) != 0 {
				t.Errorf("%q: %v %s", doc, err, res.File.Status())
			}
		}
		res, err := PlanObserve(ObserveInputs{Remove: true})
		if err != nil || res.File.Status() != "unchanged" || res.File.Existed {
			t.Errorf("no file: %v %+v", err, res.File)
		}
	})
	t.Run("an empty list of the person's stays", func(t *testing.T) {
		doc := `{"hooks": {"PreToolUse": [], "PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rune agent record"}]}]}}`
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(doc)}, Remove: true})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"hooks": {"PreToolUse": []}}`; string(res.File.New) != want {
			t.Errorf("got %s, want %s", res.File.New, want)
		}
	})
	t.Run("a comment of the person's stays", func(t *testing.T) {
		doc := "{\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\"matcher\": \"Write\", \"hooks\": []}, // mine\n      {\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"rune agent record\"}]}\n    ]\n  }\n}\n"
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(doc)}, Remove: true})
		if err != nil {
			t.Fatal(err)
		}
		want := "{\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\"matcher\": \"Write\", \"hooks\": []} // mine\n    ]\n  }\n}\n"
		if string(res.File.New) != want {
			t.Errorf("got %q, want %q", res.File.New, want)
		}
	})
	t.Run("a marked file keeps its mark", func(t *testing.T) {
		res, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: append([]byte("\xEF\xBB\xBF"), newObserveFile...)}, Remove: true})
		if err != nil || !strings.HasPrefix(string(res.File.New), "\xEF\xBB\xBF{") {
			t.Errorf("%v %q", err, res.File.New)
		}
	})
	t.Run("not valid", func(t *testing.T) {
		if _, err := PlanObserve(ObserveInputs{Files: map[string][]byte{ObservePath: []byte(`{"a": `)}, Remove: true}); err == nil {
			t.Error("no error")
		}
	})
	t.Run("verified", func(t *testing.T) {
		if err := verifyObserve([]byte(newObserveFile), []byte(`{"hooks": {}, "x": 1}`), false); err == nil {
			t.Error("a removal that changed something else")
		}
	})
}
