package observe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func dropReason(err error) string {
	var drop *DropError
	if errors.As(err, &drop) {
		return drop.Reason
	}
	return ""
}

func TestDecodeEventFixtures(t *testing.T) {
	tests := []struct {
		file string
		want Event
	}{
		{"pre.json", Event{Name: EventPre, Tool: "Bash", ToolUse: "toolu_01ABC123", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test", Description: "Run test suite"}},
		{"success.json", Event{Name: EventSuccess, Tool: "Bash", ToolUse: "toolu_01ABC123", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test", Description: "Run test suite"}},
		{"failure-exit.json", Event{Name: EventFailure, Tool: "Bash", ToolUse: "toolu_01ABC123", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test", Description: "Run test suite", Error: "Exit code 1\nError: Cannot find module 'express'"}},
		{"failure-no-exit.json", Event{Name: EventFailure, Tool: "Bash", ToolUse: "toolu_01ABC124", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test", Error: "spawn /bin/bash ENOENT"}},
		{"interrupt.json", Event{Name: EventFailure, Tool: "Bash", ToolUse: "toolu_01ABC125", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test", Error: "Interrupted by user", Interrupt: true}},
		{"background.json", Event{Name: EventSuccess, Tool: "Bash", ToolUse: "toolu_01ABC126", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm run dev", Description: "Start the dev server", Background: true}},
		{"other-tool.json", Event{Name: EventSuccess, Tool: "Write", ToolUse: "toolu_01ABC127", Session: "abc123", Cwd: "/home/user/my-project"}},
		{"other-event.json", Event{Name: "SessionStart", Session: "abc123", Cwd: "/home/user/my-project"}},
		{"no-tool-use.json", Event{Name: EventSuccess, Tool: "Bash", Session: "abc123", Cwd: "/home/user/my-project", Command: "npm test"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := DecodeEvent(strings.NewReader(string(fixture(t, tt.file))))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeEventDrops(t *testing.T) {
	if _, err := DecodeEvent(strings.NewReader(string(fixture(t, "not-json.txt")))); dropReason(err) != ReasonNotJSON {
		t.Errorf("not JSON: %v", err)
	}
	if _, err := DecodeEvent(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":5}}`)); dropReason(err) != ReasonInvalidEvent {
		t.Errorf("a command that is a number: %v", err)
	}
	if _, err := DecodeEvent(strings.NewReader(`{"tool_name":"Bash","session_id":7}`)); dropReason(err) != ReasonInvalidEvent {
		t.Errorf("a session that is a number: %v", err)
	}
	if _, err := DecodeEvent(iotest.ErrReader(errors.New("broken pipe"))); dropReason(err) != ReasonNotJSON {
		t.Errorf("a read error: %v", err)
	}
	// Another tool's input is never decoded, whatever its shape.
	if ev, err := DecodeEvent(strings.NewReader(`{"tool_name":"Write","tool_input":{"command":5},"error":{"x":1}}`)); err != nil || ev.Tool != "Write" {
		t.Errorf("another tool: %+v %v", ev, err)
	}
	// An error that is not text leaves no error.
	if ev, err := DecodeEvent(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"x"},"error":{"x":1}}`)); err != nil || ev.Error != "" {
		t.Errorf("an error object: %+v %v", ev, err)
	}
}

func TestDecodeEventSize(t *testing.T) {
	head := `{"tool_name":"Bash","tool_input":{"command":"x","description":"`
	tail := `"}}`
	exact := head + strings.Repeat("d", MaxEventBytes-len(head)-len(tail)) + tail
	if len(exact) != MaxEventBytes {
		t.Fatalf("built %d bytes", len(exact))
	}
	if ev, err := DecodeEvent(strings.NewReader(exact)); err != nil || ev.Command != "x" {
		t.Errorf("exactly the limit: %v", err)
	}
	if _, err := DecodeEvent(strings.NewReader(exact + " ")); dropReason(err) != ReasonTooLarge {
		t.Errorf("one byte over: %v", err)
	}
}

func TestDropErrorText(t *testing.T) {
	if got := (&DropError{ReasonTooLong}).Error(); got != "event not recorded: too-long" {
		t.Errorf("got %q", got)
	}
}

func TestEventWanted(t *testing.T) {
	for _, tt := range []struct {
		ev   Event
		want bool
	}{
		{Event{Name: EventPre, Tool: ToolBash}, true},
		{Event{Name: EventSuccess, Tool: ToolBash}, true},
		{Event{Name: EventFailure, Tool: ToolBash}, true},
		{Event{Name: EventSuccess, Tool: "Write"}, false},
		{Event{Name: "SessionStart", Tool: ToolBash}, false},
		{Event{}, false},
	} {
		if got := tt.ev.Wanted(); got != tt.want {
			t.Errorf("%+v: %v", tt.ev, got)
		}
	}
}
