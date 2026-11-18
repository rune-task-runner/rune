// Package audit keeps the audit log of one project: what agents asked Rune's
// MCP servers to do. The log is a directory of JSON-lines files, one per UTC
// day, outside the project. A record is appended and never changed, so a
// call that was started and never finished stays visible as a start without
// an end. Writers in separate processes take a lock file in turn.
package audit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Version is the version of the record format.
const Version = 1

// The types of a record.
const (
	TypeCall        = "call"
	TypeAgentRun    = "agent-run"
	TypeRequest     = "request"
	TypeAuthFailure = "auth-failure"
)

// The phases of a record that has two: a call and an agent run.
const (
	PhaseStart = "start"
	PhaseEnd   = "end"
)

// The outcomes of an action. OutcomeIncomplete is never stored: a reader
// gives it to a start that has no end.
const (
	OutcomeCompleted        = "completed"
	OutcomeFailed           = "failed"
	OutcomeRefused          = "refused"
	OutcomeInvalidArguments = "invalid-arguments"
	OutcomeUnknownTool      = "unknown-tool"
	OutcomeCancelled        = "cancelled"
	OutcomeTimedOut         = "timed-out"
	OutcomeError            = "error"
	OutcomeOK               = "ok"
	OutcomeIncomplete       = "incomplete"
)

// Outcomes lists every outcome a reader can report, in the order the
// documentation gives them.
var Outcomes = []string{
	OutcomeCompleted, OutcomeFailed, OutcomeRefused, OutcomeInvalidArguments,
	OutcomeUnknownTool, OutcomeCancelled, OutcomeTimedOut, OutcomeError,
	OutcomeIncomplete, OutcomeOK,
}

// MaxRecordBytes is the most one stored line holds.
const MaxRecordBytes = 64 << 10

// Client is what a client reported about itself in the handshake. Reported
// is set only on an Action, to mark the name as the client's own claim.
type Client struct {
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Reported bool   `json:"reported,omitempty"`
}

// Arguments are a call's arguments: as received, and as the task was bound.
// Both are JSON kept as written, so numbers and the order of names survive.
type Arguments struct {
	Supplied  json.RawMessage `json:"supplied,omitempty"`
	Effective json.RawMessage `json:"effective,omitempty"`
}

// Git is the repository state at a call. Dirty is nil when git did not say.
type Git struct {
	State  string `json:"state"`
	Commit string `json:"commit,omitempty"`
	Dirty  *bool  `json:"dirty,omitempty"`
}

// Project is what a call ran against.
type Project struct {
	Runefile         string `json:"runefile,omitempty"`
	DefinitionDigest string `json:"definitionDigest,omitempty"`
	ConfigDigest     string `json:"configDigest,omitempty"`
	Git              *Git   `json:"git,omitempty"`
}

// Sandbox states whether a sandbox was enforced for a call.
type Sandbox struct {
	Enforced bool `json:"enforced"`
}

// Error says why an action ran no task.
type Error struct {
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message,omitempty"`
}

// Approval is the decision about a call that needed one.
type Approval struct {
	Asked    bool     `json:"asked"`
	Decision string   `json:"decision"`
	Tasks    []string `json:"tasks,omitempty"`
}

// Plan is the stored plan a call made or applied.
type Plan struct {
	Role   string `json:"role"`
	ID     string `json:"id"`
	Digest string `json:"digest,omitempty"`
}

// Record is one stored line. Which fields are set depends on Type and Phase.
type Record struct {
	V     int       `json:"v"`
	Type  string    `json:"type"`
	Phase string    `json:"phase,omitempty"`
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`

	DurationMs *int64 `json:"durationMs,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`

	Session   string     `json:"session,omitempty"`
	Parent    string     `json:"parent,omitempty"`
	Transport string     `json:"transport,omitempty"`
	Auth      string     `json:"auth,omitempty"`
	Client    *Client    `json:"client,omitempty"`
	Method    string     `json:"method,omitempty"`
	Tool      string     `json:"tool,omitempty"`
	Task      string     `json:"task,omitempty"`
	Arguments *Arguments `json:"arguments,omitempty"`
	Error     *Error     `json:"error,omitempty"`
	Approval  *Approval  `json:"approval,omitempty"`
	Grants    []string   `json:"grants,omitempty"`
	Plan      *Plan      `json:"plan,omitempty"`
	Sandbox   *Sandbox   `json:"sandbox,omitempty"`
	Project   *Project   `json:"project,omitempty"`

	Remote     string `json:"remote,omitempty"`
	Suppressed int    `json:"suppressed,omitempty"`
}

// NewID returns a new id for an action: 16 hex digits.
func NewID() string {
	var b [8]byte
	// crypto/rand does not fail on the supported systems.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Encode writes the record as one line that ends with a line break. A record
// that would pass MaxRecordBytes loses its arguments, which are the only
// part an agent sizes: they are replaced by a mark with the bytes left out.
func (r Record) Encode() ([]byte, error) {
	r.V = Version
	line, err := encodeLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) <= MaxRecordBytes || r.Arguments == nil {
		return line, nil
	}
	args := *r.Arguments
	if n := len(args.Supplied); n > 0 {
		args.Supplied = notStored(n)
	}
	r.Arguments = &args
	if line, err = encodeLine(r); err != nil || len(line) <= MaxRecordBytes {
		return line, err
	}
	if n := len(args.Effective); n > 0 {
		args.Effective = notStored(n)
	}
	return encodeLine(r)
}

func notStored(n int) json.RawMessage {
	// A quoted string of digits and fixed words marshals without error.
	mark, _ := json.Marshal(fmt.Sprintf("[%d bytes not stored]", n))
	return mark
}

func encodeLine(v any) ([]byte, error) {
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	// The log is read by people: values keep their <, > and &.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode audit record: %w", err)
	}
	return line.Bytes(), nil
}
