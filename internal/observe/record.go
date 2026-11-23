package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The kinds of record: before a call, after it succeeded, after it failed.
const (
	KindPre     = "pre"
	KindSuccess = "success"
	KindFailure = "failure"
)

// ClientClaude is the only client recorded.
const ClientClaude = "claude"

const (
	// RecordVersion is the version of the record format.
	RecordVersion = 1
	// MaxRecordBytes bounds one line of the store.
	MaxRecordBytes = 8 << 10
	// maxIDBytes bounds a session or tool-use id. A longer one is not cut:
	// two cut ids could join two calls.
	maxIDBytes = 128
)

// ErrIgnored is returned for an event the recorder does not record and does
// not count: another tool's call or another hook event.
var ErrIgnored = errors.New("not an event the recorder records")

// Record is what the store keeps of one hook event (research R5).
type Record struct {
	V           int       `json:"v"`
	Time        time.Time `json:"time"`
	Client      string    `json:"client"`
	Session     string    `json:"session"`
	ToolUse     string    `json:"toolUse"`
	Event       string    `json:"event"`
	Exit        *int      `json:"exit,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
	Background  bool      `json:"background,omitempty"`
	Cwd         string    `json:"cwd,omitempty"`
	Command     string    `json:"command"`
	Description string    `json:"description,omitempty"`
}

var kinds = map[string]string{EventPre: KindPre, EventSuccess: KindSuccess, EventFailure: KindFailure}

// NewRecord builds the record of ev for the project at root, which is
// resolved and absolute. It returns ErrIgnored for an event that is not
// recorded and a *DropError for one that cannot be. The working directory
// is kept relative to root with forward slashes.
func NewRecord(ev Event, root string, s Secrets, now time.Time) (Record, error) {
	kind, ok := kinds[ev.Name]
	if !ok || ev.Tool != ToolBash {
		return Record{}, ErrIgnored
	}
	if !validID(ev.ToolUse) || !validID(ev.Session) || ev.Command == "" || ev.Cwd == "" {
		return Record{}, &DropError{ReasonInvalidEvent}
	}
	cwd, ok := relative(root, ev.Cwd)
	if !ok {
		return Record{}, &DropError{ReasonOutsideProject}
	}
	command, err := Redact(ev.Command, s)
	if err != nil {
		return Record{}, err
	}
	rec := Record{
		V:           RecordVersion,
		Time:        now.UTC().Truncate(time.Second),
		Client:      ClientClaude,
		Session:     ev.Session,
		ToolUse:     ev.ToolUse,
		Event:       kind,
		Background:  ev.Background,
		Cwd:         cwd,
		Command:     command,
		Description: CleanDescription(ev.Description, s),
	}
	if kind == KindFailure {
		rec.Interrupted = ev.Interrupt
		rec.Exit = exitCode(ev.Error)
	}
	return rec, nil
}

func validID(id string) bool {
	return id != "" && len(id) <= maxIDBytes && !strings.ContainsFunc(id, unicode.IsControl)
}

// relative is dir relative to root, "" for root itself, with forward
// slashes. ok is false for a directory outside root. dir is resolved like
// root, so that two names of one directory agree: a link, macOS's /var for
// /private/var, a short name of Windows.
func relative(root, dir string) (rel string, ok bool) {
	if !filepath.IsAbs(dir) {
		return "", false
	}
	rel, err := filepath.Rel(root, resolveExisting(filepath.Clean(dir)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// resolveExisting follows the links of the part of dir that exists and keeps
// the rest as written: a command may run in a directory that is gone, or not
// there yet.
func resolveExisting(dir string) string {
	rest := ""
	for d := dir; ; d = filepath.Dir(d) {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(d) == d {
			return dir
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// exitCode reads "Exit code N" from the first line of a failure's error.
func exitCode(text string) *int {
	line, _, _ := strings.Cut(text, "\n")
	digits, ok := strings.CutPrefix(line, "Exit code ")
	if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return nil
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return nil
	}
	return &n
}

// Encode writes the record as one line, at most MaxRecordBytes long with its
// line feed. A record that does not fit is a *DropError: its command is too
// long once written as JSON.
func (r Record) Encode() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	// <, > and & are common in commands and stay as they are, so a record
	// is as long as its text.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	if b.Len() > MaxRecordBytes {
		return nil, &DropError{ReasonTooLong}
	}
	return b.Bytes(), nil
}
