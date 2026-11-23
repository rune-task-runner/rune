package observe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 23, 10, 15, 2, 500, time.FixedZone("x", 3600))

// testRoot is a resolved project directory with a subdirectory web.
func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func bashEvent(name, root string) Event {
	return Event{Name: name, Tool: ToolBash, ToolUse: "toolu_1", Session: "s1", Cwd: root, Command: "go test ./...", Description: "Run the tests"}
}

func TestNewRecord(t *testing.T) {
	root := testRoot(t)
	rec, err := NewRecord(bashEvent(EventPre, root), root, testSecrets(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{V: 1, Time: time.Date(2026, 11, 23, 9, 15, 2, 0, time.UTC), Client: "claude", Session: "s1", ToolUse: "toolu_1", Event: KindPre, Command: "go test ./...", Description: "Run the tests"}
	if !rec.Time.Equal(want.Time) || rec.Time.Location() != time.UTC {
		t.Errorf("time %v, want %v in UTC", rec.Time, want.Time)
	}
	rec.Time = want.Time
	if rec.Exit != nil || rec != want {
		t.Errorf("got  %+v\nwant %+v", rec, want)
	}

	t.Run("kinds", func(t *testing.T) {
		for name, kind := range map[string]string{EventPre: KindPre, EventSuccess: KindSuccess, EventFailure: KindFailure} {
			if rec, err := NewRecord(bashEvent(name, root), root, Secrets{}, testNow); err != nil || rec.Event != kind {
				t.Errorf("%s: %q %v", name, rec.Event, err)
			}
		}
	})
	t.Run("ignored", func(t *testing.T) {
		other := bashEvent(EventSuccess, root)
		other.Tool = "Write"
		start := bashEvent("SessionStart", root)
		for _, ev := range []Event{other, start} {
			if _, err := NewRecord(ev, root, Secrets{}, testNow); !errors.Is(err, ErrIgnored) {
				t.Errorf("%+v: %v", ev, err)
			}
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for name, change := range map[string]func(*Event){
			"no tool use":       func(e *Event) { e.ToolUse = "" },
			"long tool use":     func(e *Event) { e.ToolUse = strings.Repeat("t", maxIDBytes+1) },
			"control character": func(e *Event) { e.ToolUse = "toolu\n1" },
			"no session":        func(e *Event) { e.Session = "" },
			"no command":        func(e *Event) { e.Command = "" },
			"no cwd":            func(e *Event) { e.Cwd = "" },
		} {
			ev := bashEvent(EventPre, root)
			change(&ev)
			if _, err := NewRecord(ev, root, Secrets{}, testNow); dropReason(err) != ReasonInvalidEvent {
				t.Errorf("%s: %v", name, err)
			}
		}
		ev := bashEvent(EventPre, root)
		ev.ToolUse = strings.Repeat("t", maxIDBytes)
		if _, err := NewRecord(ev, root, Secrets{}, testNow); err != nil {
			t.Errorf("an id of the longest length: %v", err)
		}
	})
	t.Run("working directory", func(t *testing.T) {
		tests := map[string]struct {
			cwd    string
			want   string
			reason string
		}{
			"root":          {root, "", ""},
			"subdirectory":  {filepath.Join(root, "web"), "web", ""},
			"missing below": {filepath.Join(root, "a", "b"), "a/b", ""},
			"outside":       {t.TempDir(), "", ReasonOutsideProject},
			"parent":        {filepath.Dir(root), "", ReasonOutsideProject},
			"relative":      {"web", "", ReasonOutsideProject},
		}
		for name, tt := range tests {
			ev := bashEvent(EventPre, root)
			ev.Cwd = tt.cwd
			rec, err := NewRecord(ev, root, Secrets{}, testNow)
			if dropReason(err) != tt.reason || rec.Cwd != tt.want {
				t.Errorf("%s: cwd %q, %v", name, rec.Cwd, err)
			}
		}
		if runtime.GOOS == "windows" {
			return
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		ev := bashEvent(EventPre, root)
		ev.Cwd = filepath.Join(link, "web")
		if rec, err := NewRecord(ev, root, Secrets{}, testNow); err != nil || rec.Cwd != "web" {
			t.Errorf("through a link: %q %v", rec.Cwd, err)
		}
	})
	t.Run("failure", func(t *testing.T) {
		ev := bashEvent(EventFailure, root)
		ev.Error = "Exit code 2\nboom"
		rec, err := NewRecord(ev, root, Secrets{}, testNow)
		if err != nil || rec.Exit == nil || *rec.Exit != 2 || rec.Interrupted {
			t.Errorf("exit 2: %+v %v", rec, err)
		}
		ev.Error, ev.Interrupt = "Interrupted", true
		if rec, err = NewRecord(ev, root, Secrets{}, testNow); err != nil || rec.Exit != nil || !rec.Interrupted {
			t.Errorf("interrupted: %+v %v", rec, err)
		}
		// A success says nothing of an exit code or an interrupt.
		ok := bashEvent(EventSuccess, root)
		ok.Error, ok.Interrupt, ok.Background = "Exit code 1", true, true
		if rec, err = NewRecord(ok, root, Secrets{}, testNow); err != nil || rec.Exit != nil || rec.Interrupted || !rec.Background {
			t.Errorf("success: %+v %v", rec, err)
		}
	})
	t.Run("redaction", func(t *testing.T) {
		ev := bashEvent(EventPre, root)
		ev.Command, ev.Description = "API_TOKEN=abc123xyz deploy", "Deploy with s3cr3tvalue"
		rec, err := NewRecord(ev, root, testSecrets(), testNow)
		if err != nil || rec.Command != "API_TOKEN=*** deploy" || rec.Description != "Deploy with ***" {
			t.Errorf("%+v %v", rec, err)
		}
		ev.Command = "cat <<EOF\nx\nEOF"
		if _, err := NewRecord(ev, root, testSecrets(), testNow); dropReason(err) != ReasonHereDocument {
			t.Errorf("here-document: %v", err)
		}
	})
}

func TestExitCode(t *testing.T) {
	tests := map[string]int{"Exit code 1": 1, "Exit code 0\nx": 0, "Exit code 127": 127}
	for in, want := range tests {
		if got := exitCode(in); got == nil || *got != want {
			t.Errorf("exitCode(%q) = %v, want %d", in, got, want)
		}
	}
	for _, in := range []string{"", "exit code 1", "Exit code ", "Exit code -1", "Exit code +1", "Exit code 1x", "x\nExit code 1", "Exit code 99999999999999999999"} {
		if got := exitCode(in); got != nil {
			t.Errorf("exitCode(%q) = %d, want none", in, *got)
		}
	}
}

func TestRecordEncode(t *testing.T) {
	two := 2
	rec := Record{V: 1, Time: time.Date(2026, 11, 23, 10, 15, 2, 0, time.UTC), Client: "claude", Session: "abc123", ToolUse: "toolu_01ABC123", Event: KindFailure, Exit: &two, Command: "a && b > c", Description: "Run"}
	line, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"time":"2026-11-23T10:15:02Z","client":"claude","session":"abc123","toolUse":"toolu_01ABC123","event":"failure","exit":2,"command":"a && b > c","description":"Run"}` + "\n"
	if string(line) != want {
		t.Errorf("got  %s\nwant %s", line, want)
	}
	var back Record
	if err := json.Unmarshal(line, &back); err != nil || back.Command != rec.Command || *back.Exit != 2 {
		t.Errorf("read back: %+v %v", back, err)
	}
	// Quotes take two bytes each once written, so a command of the longest
	// length can still be too long as a record.
	rec.Command = strings.Repeat(`"`, MaxCommandBytes)
	if _, err := rec.Encode(); dropReason(err) != ReasonTooLong {
		t.Errorf("a line over the limit: %v", err)
	}
	rec.Command = strings.Repeat("a", MaxCommandBytes)
	rec.Description = strings.Repeat("d", MaxDescriptionBytes)
	if line, err := rec.Encode(); err != nil || len(line) > MaxRecordBytes {
		t.Errorf("the longest plain record: %d bytes, %v", len(line), err)
	}
}
