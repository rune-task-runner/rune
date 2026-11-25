package receipt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version is the receipt format version this package writes and accepts.
const Version = 1

// PredicateType names Rune receipts inside an in-toto statement.
const PredicateType = "https://rune-task-runner.github.io/rune/receipt/v1"

const statementType = "https://in-toto.io/Statement/v1"

// The statuses of a run.
const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
	StatusRefused     = "refused"
	StatusCached      = "cached"
	StatusIncomplete  = "incomplete"
)

// The paths a run can start from.
const (
	ChannelCLI   = "cli"
	ChannelCIRun = "ci-run"
	ChannelMCP   = "mcp"
	ChannelAgent = "agent"
)

// The roles of a run.
const (
	RoleTask        = "task"
	RoleFailureHook = "failure-hook"
)

// Receipt is the signed record of one task run (contracts/receipt.md). The
// field order is the order it is written in.
type Receipt struct {
	Version           int             `json:"version"`
	ID                string          `json:"id"`
	Invocation        string          `json:"invocation"`
	ParentInvocation  string          `json:"parentInvocation,omitempty"`
	Task              string          `json:"task"`
	Arguments         []Arg           `json:"arguments"`
	ArgumentsRedacted bool            `json:"argumentsRedacted,omitempty"`
	Role              string          `json:"role"`
	Related           *Related        `json:"related,omitempty"`
	Status            string          `json:"status"`
	ExitCode          *int            `json:"exitCode,omitempty"`
	StartedAt         time.Time       `json:"startedAt"`
	FinishedAt        time.Time       `json:"finishedAt"`
	DurationMs        int64           `json:"durationMs"`
	Rune              RuneInfo        `json:"rune"`
	Executor          string          `json:"executor"`
	Definition        string          `json:"definition"`
	Project           string          `json:"project"`
	Runefiles         []RunefileEntry `json:"runefiles"`
	Source            *Source         `json:"source,omitempty"`
	UncoveredInputs   []string        `json:"uncoveredInputs,omitempty"`
	Outputs           *Outputs        `json:"outputs,omitempty"`
	Channel           string          `json:"channel"`
	Claims            *Claims         `json:"claims,omitempty"`
}

// Arg is one parameter of the task and the values it was bound to.
type Arg struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Related names the receipts a run relates to.
type Related struct {
	Dependencies []string `json:"dependencies,omitempty"`
	Follows      string   `json:"follows,omitempty"`
	FailureOf    string   `json:"failureOf,omitempty"`
	Reuses       string   `json:"reuses,omitempty"`
}

// RuneInfo identifies the binary that recorded a run.
type RuneInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// RunefileEntry is one file the load of the Runefile read. Covered is false
// when a snapshot cannot hold it: it lies outside the repository or git
// ignores it.
type RunefileEntry struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Covered bool   `json:"covered"`
}

// Source is the working tree before and after the body.
type Source struct {
	Commit string `json:"commit,omitempty"`
	Dirty  bool   `json:"dirty"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Outputs are the declared output files of a passed run. Files holds them
// when there are at most MaxInlineOutputs, Manifest names the file that does
// otherwise, and Digest binds either.
type Outputs struct {
	Count    int          `json:"count"`
	Digest   string       `json:"digest"`
	Files    []OutputFile `json:"files,omitempty"`
	Manifest string       `json:"manifest,omitempty"`
}

// OutputFile is one output file.
type OutputFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Claims are what the caller reported or the environment said. None of it is
// authenticated: only the signing key is.
type Claims struct {
	Client *Client           `json:"client,omitempty"`
	Auth   string            `json:"auth,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
}

// Client is what an MCP client reported about itself.
type Client struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// NewID returns a new receipt ID: the UTC time of now, then 64 random bits.
func NewID(now time.Time) string {
	var b [8]byte
	// crypto/rand does not fail on the supported systems.
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type statement struct {
	Type          string    `json:"_type"`
	Subject       []subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Receipt   `json:"predicate"`
}

// Statement returns the in-toto statement that carries r. Its subject is the
// snapshot before the body, or the Runefile's digest outside a repository.
func Statement(r Receipt) ([]byte, error) {
	if r.Arguments == nil {
		r.Arguments = []Arg{}
	}
	var subj subject
	if r.Source != nil {
		subj = subject{Name: "worktree", Digest: map[string]string{"gitTree": r.Source.Before}}
	} else {
		for _, f := range r.Runefiles {
			if f.Path == r.Project {
				subj = subject{Name: r.Project, Digest: map[string]string{"sha256": f.SHA256}}
				break
			}
		}
		if subj.Name == "" {
			return nil, errors.New("receipt has neither a snapshot nor its Runefile's digest")
		}
	}
	data, err := json.Marshal(statement{
		Type:          statementType,
		Subject:       []subject{subj},
		PredicateType: PredicateType,
		Predicate:     r,
	})
	if err != nil {
		return nil, fmt.Errorf("encode receipt: %w", err)
	}
	return data, nil
}

// ParseStatement reads the receipt out of a statement. It checks the
// statement and predicate types and that a task is named; the version is the
// caller's to judge.
func ParseStatement(data []byte) (Receipt, error) {
	var st statement
	if err := json.Unmarshal(data, &st); err != nil {
		return Receipt{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if st.Type != statementType || st.PredicateType != PredicateType {
		return Receipt{}, fmt.Errorf("%w: not a Rune receipt statement", ErrMalformed)
	}
	if st.Predicate.Task == "" {
		return Receipt{}, fmt.Errorf("%w: the receipt names no task", ErrMalformed)
	}
	return st.Predicate, nil
}
