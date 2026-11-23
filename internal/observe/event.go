package observe

import (
	"encoding/json"
	"errors"
	"io"
)

// MaxEventBytes is the largest hook event the recorder reads. An event of
// the Bash tool carries the command's output, so a large one is possible; a
// larger one is not recorded.
const MaxEventBytes = 4 << 20

// The Claude Code hook events the recorder records (research R1).
const (
	EventPre     = "PreToolUse"
	EventSuccess = "PostToolUse"
	EventFailure = "PostToolUseFailure"
)

// ToolBash is the only tool whose calls are recorded.
const ToolBash = "Bash"

// Why an event was not recorded. Only these names reach the diagnostics.
const (
	ReasonInvalidEvent       = "invalid-event"
	ReasonNotJSON            = "not-json"
	ReasonTooLarge           = "too-large"
	ReasonSlowInput          = "slow-input"
	ReasonNoProject          = "no-project"
	ReasonOutsideProject     = "outside-project"
	ReasonRunefileUnreadable = "runefile-unreadable"
	ReasonUnparsable         = "unparsable"
	ReasonHereDocument       = "here-document"
	ReasonTooLong            = "too-long"
	ReasonControlCharacter   = "control-character"
	ReasonStoreBusy          = "store-busy"
	ReasonStoreError         = "store-error"
)

// DropError says why an event was not recorded. Reason is one of the
// Reason constants; the error never holds text from the event.
type DropError struct {
	Reason string
}

func (e *DropError) Error() string { return "event not recorded: " + e.Reason }

// Event is one hook input, with the fields the recorder uses and nothing
// else. The command's output in tool_response is never decoded.
type Event struct {
	Name        string
	Tool        string
	ToolUse     string
	Session     string
	Cwd         string
	Command     string
	Description string
	Background  bool
	// Error is the failure event's error text. Only its first line is used.
	Error     string
	Interrupt bool
}

// Wanted reports whether the event is one the recorder records: a Bash call
// before it runs, after it succeeded, or after it failed. Any other event is
// left alone and not counted.
func (e Event) Wanted() bool {
	switch e.Name {
	case EventPre, EventSuccess, EventFailure:
		return e.Tool == ToolBash
	}
	return false
}

// DecodeEvent reads one hook event of at most MaxEventBytes. The tool input
// is decoded only for the Bash tool: another tool's input has another shape
// and is never recorded.
func DecodeEvent(r io.Reader) (Event, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxEventBytes+1))
	if err != nil {
		return Event{}, &DropError{ReasonNotJSON}
	}
	if len(data) > MaxEventBytes {
		return Event{}, &DropError{ReasonTooLarge}
	}
	var raw struct {
		Name      string          `json:"hook_event_name"`
		Tool      string          `json:"tool_name"`
		ToolUse   string          `json:"tool_use_id"`
		Session   string          `json:"session_id"`
		Cwd       string          `json:"cwd"`
		Input     json.RawMessage `json:"tool_input"`
		Error     json.RawMessage `json:"error"`
		Interrupt bool            `json:"is_interrupt"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Event{}, decodeError(err)
	}
	ev := Event{
		Name: raw.Name, Tool: raw.Tool, ToolUse: raw.ToolUse, Session: raw.Session,
		Cwd: raw.Cwd, Interrupt: raw.Interrupt,
	}
	if ev.Tool != ToolBash {
		return ev, nil
	}
	if len(raw.Input) > 0 {
		var input struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			Background  bool   `json:"run_in_background"`
		}
		if err := json.Unmarshal(raw.Input, &input); err != nil {
			return Event{}, decodeError(err)
		}
		ev.Command, ev.Description, ev.Background = input.Command, input.Description, input.Background
	}
	// The error of a tool other than Bash may be an object; Bash's is text.
	// Anything else leaves no error text, so no exit code is read from it.
	_ = json.Unmarshal(raw.Error, &ev.Error)
	return ev, nil
}

// decodeError tells input that is not JSON from JSON of the wrong shape.
func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return &DropError{ReasonInvalidEvent}
	}
	return &DropError{ReasonNotJSON}
}
