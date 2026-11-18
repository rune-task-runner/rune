package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// maxLineBytes is the longest line a reader takes in. A record is at most
// MaxRecordBytes, so a longer line is damage, not a record.
const maxLineBytes = 1 << 20

// Action is what a reader builds from the records of one id: the start and,
// when there is one, the end. It is the object `rune log --json` prints.
type Action struct {
	SchemaVersion int        `json:"schemaVersion"`
	Type          string     `json:"type"`
	ID            string     `json:"id"`
	Start         time.Time  `json:"start"`
	End           *time.Time `json:"end,omitempty"`
	DurationMs    *int64     `json:"durationMs,omitempty"`
	Outcome       string     `json:"outcome,omitempty"`
	ExitCode      *int       `json:"exitCode,omitempty"`

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

// Damage counts the records of one file that could not be read.
type Damage struct {
	File  string
	Count int
}

// Read returns the actions in the day files of dir whose UTC day lies
// between the days of from and to, oldest first, and the records it had to
// skip. A zero from starts at the oldest file and a zero to ends at the
// newest. A directory that does not exist holds no action.
//
// An action is in the file of the day it started on, so the days of a range
// of start times are exactly the files to read.
func Read(dir string, from, to time.Time) ([]Action, []Damage, error) {
	// The directory is inspected first: Windows reports a file in its place
	// as a path that does not exist, which would read as an empty log.
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read audit log: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("read audit log: %s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read audit log: %w", err)
	}
	first, last := "", ""
	if !from.IsZero() {
		first = from.UTC().Format(dayLayout)
	}
	if !to.IsZero() {
		last = to.UTC().Format(dayLayout)
	}
	var actions []Action
	var damage []Damage
	// ReadDir sorts by name, and a day file's name sorts as its day does.
	for _, entry := range entries {
		m := dayFile.FindStringSubmatch(entry.Name())
		if m == nil || (first != "" && m[1] < first) || (last != "" && m[1] > last) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		read, bad, err := readFile(path)
		if err != nil {
			return nil, nil, err
		}
		actions = append(actions, read...)
		if bad > 0 {
			damage = append(damage, Damage{File: path, Count: bad})
		}
	}
	slices.SortStableFunc(actions, func(a, b Action) int { return a.Start.Compare(b.Start) })
	return actions, damage, nil
}

// readFile reads one day file. It returns the actions in the order their
// first record was written and the number of lines it skipped.
func readFile(path string) (actions []Action, damaged int, err error) {
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("is not a regular file")
		}
		return nil, 0, fmt.Errorf("read audit log: %s: %w", path, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read audit log: %w", err)
	}
	defer func() { _ = f.Close() }()

	index := map[string]int{}
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, tooLong, err := readLine(r)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, fmt.Errorf("read audit log: %s: %w", path, err)
		}
		if len(line) > 0 || tooLong {
			if tooLong || !take(line, &actions, index) {
				damaged++
			}
		}
		if err != nil {
			return actions, damaged, nil
		}
	}
}

// readLine returns the next line without its line break. A line longer than
// maxLineBytes is read to its end and reported as too long, with no bytes.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		part, err := r.ReadSlice('\n')
		if !tooLong {
			line = append(line, part...)
			if len(line) > maxLineBytes {
				line, tooLong = nil, true
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if n := len(line); n > 0 && line[n-1] == '\n' {
			line = line[:n-1]
		}
		return line, tooLong, err
	}
}

// take adds the record of one line to actions and reports whether the line
// was a record it could place.
func take(line []byte, actions *[]Action, index map[string]int) bool {
	var rec Record
	if json.Unmarshal(line, &rec) != nil {
		return false
	}
	if rec.V != Version || rec.Type == "" || rec.ID == "" || rec.Time.IsZero() {
		return false
	}
	switch rec.Phase {
	case PhaseStart:
		if _, seen := index[rec.ID]; seen {
			return false
		}
		index[rec.ID] = len(*actions)
		a := actionOf(rec)
		a.Outcome = OutcomeIncomplete
		*actions = append(*actions, a)
	case PhaseEnd:
		i, seen := index[rec.ID]
		if !seen || (*actions)[i].End != nil {
			return false
		}
		finish(&(*actions)[i], rec)
	default:
		*actions = append(*actions, actionOf(rec))
	}
	return true
}

// actionOf is the action a start record, or a record with no phase, gives.
func actionOf(rec Record) Action {
	a := Action{
		SchemaVersion: Version,
		Type:          rec.Type,
		ID:            rec.ID,
		Start:         rec.Time,
		DurationMs:    rec.DurationMs,
		Outcome:       rec.Outcome,
		ExitCode:      rec.ExitCode,
		Session:       rec.Session,
		Parent:        rec.Parent,
		Transport:     rec.Transport,
		Auth:          rec.Auth,
		Method:        rec.Method,
		Tool:          rec.Tool,
		Task:          rec.Task,
		Arguments:     rec.Arguments,
		Error:         rec.Error,
		Approval:      rec.Approval,
		Grants:        rec.Grants,
		Plan:          rec.Plan,
		Sandbox:       rec.Sandbox,
		Project:       rec.Project,
		Remote:        rec.Remote,
		Suppressed:    rec.Suppressed,
	}
	if rec.Client != nil {
		// The name is the client's own claim, and the object says so.
		client := *rec.Client
		client.Reported = true
		a.Client = &client
	}
	return a
}

// finish adds what an end record knows to the action its start made.
func finish(a *Action, end Record) {
	at := end.Time
	a.End = &at
	a.DurationMs = end.DurationMs
	a.Outcome = end.Outcome
	a.ExitCode = end.ExitCode
	a.Error = end.Error
	a.Approval = end.Approval
	a.Grants = end.Grants
	a.Plan = end.Plan
	if end.Arguments != nil && len(end.Arguments.Effective) > 0 {
		args := Arguments{Effective: end.Arguments.Effective}
		if a.Arguments != nil {
			args.Supplied = a.Arguments.Supplied
		}
		a.Arguments = &args
	}
}
