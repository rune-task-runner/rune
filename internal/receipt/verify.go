package receipt

import (
	"bytes"
	"cmp"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"time"
)

// The reasons a task is not verified. ReasonMalformed is only ever a
// problem: a candidate that cannot be read names no task.
const (
	ReasonNoReceipt         = "no-receipt"
	ReasonUnknownSigner     = "unknown-signer"
	ReasonBadSignature      = "bad-signature"
	ReasonUnsupported       = "unsupported-version"
	ReasonFailed            = "failed"
	ReasonInterrupted       = "interrupted"
	ReasonRefused           = "refused"
	ReasonCached            = "cached"
	ReasonIncomplete        = "incomplete"
	ReasonNoSnapshot        = "no-snapshot"
	ReasonChangedDuringRun  = "changed-during-run"
	ReasonDifferentSnap     = "different-snapshot"
	ReasonUncoveredRunefile = "uncovered-runefile"
	ReasonUncoveredInputs   = "uncovered-inputs"
	ReasonMalformed         = "malformed"
)

// Where a candidate comes from.
const (
	OriginLocal = "local"
	OriginNote  = "note"
)

// Candidate is one envelope that may be evidence. Name says where it was
// found, for a problem report.
type Candidate struct {
	Line   []byte
	Origin string
	Name   string
}

// Summary is what a verdict shows of the receipt it comes from.
type Summary struct {
	ID         string    `json:"id"`
	Origin     string    `json:"origin"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	Arguments  []Arg     `json:"arguments"`
	Channel    string    `json:"channel"`
	Claims     *Claims   `json:"claims,omitempty"`
	Signer     string    `json:"signer"`
	FinishedAt time.Time `json:"finishedAt"`
	Commit     string    `json:"commit,omitempty"`
	Dirty      bool      `json:"dirty"`
	Exact      bool      `json:"exact"`
	Reuses     string    `json:"reuses,omitempty"`
	// Uncovered is the first Runefile entry no snapshot holds, and
	// UncoveredInputs counts the declared inputs git ignores.
	Uncovered       string   `json:"uncoveredRunefile,omitempty"`
	UncoveredInputs int      `json:"uncoveredInputs,omitempty"`
	Outputs         *Outputs `json:"-"`
}

// Verdict is the answer for one task at one revision.
type Verdict struct {
	Task     string   `json:"task"`
	Verified bool     `json:"verified"`
	Reason   string   `json:"reason,omitempty"`
	Receipt  *Summary `json:"receipt,omitempty"`
}

// Problem is a candidate that names no task.
type Problem struct {
	Origin string `json:"origin"`
	Reason string `json:"reason"`
}

// judged is one readable candidate and why it is not evidence ("" when it
// is).
type judged struct {
	r       Receipt
	sum     Summary
	reason  string
	matches bool // its snapshot before is the revision's tree
}

// Evaluate judges candidates as evidence that each task of project (its
// Runefile's path from the top of the repository) passed on exactly the files
// of commit, whose tree is tree. Only a receipt signed by a trusted key
// counts. Each task gets one verdict, in the order given, each named once.
func Evaluate(cands []Candidate, trusted []ed25519.PublicKey, project string, tasks []string, commit, tree string) ([]Verdict, []Problem) {
	var problems []Problem
	byTask := map[string][]judged{}
	var seen [][]byte
	for _, c := range cands {
		line := bytes.TrimSpace(c.Line)
		if slices.ContainsFunc(seen, func(s []byte) bool { return bytes.Equal(s, line) }) {
			continue
		}
		seen = append(seen, line)
		j, problem := judge(line, c.Origin, trusted, commit, tree)
		if problem != "" {
			problems = append(problems, Problem{Origin: c.Name, Reason: problem})
			continue
		}
		// A commit's note holds the receipts of every project in the
		// repository, and another project can have a task of the same name.
		if j.r.Project != project {
			continue
		}
		byTask[j.r.Task] = append(byTask[j.r.Task], j)
	}
	var verdicts []Verdict
	for _, task := range tasks {
		if slices.ContainsFunc(verdicts, func(v Verdict) bool { return v.Task == task }) {
			continue
		}
		verdicts = append(verdicts, verdictOf(task, byTask[task]))
	}
	return verdicts, problems
}

// judge reads one envelope. A problem is returned for one that names no
// task: unreadable, or not a receipt.
func judge(line []byte, origin string, trusted []ed25519.PublicKey, commit, tree string) (judged, string) {
	env, payload, err := Decode(line)
	// Open checks the payload type only after the signature, and would call
	// a signed envelope of another type a bad signature.
	if err != nil || env.PayloadType != PayloadType {
		return judged{}, ReasonMalformed
	}
	_, keyID, sigErr := Open(line, trusted)
	r, err := ParseStatement(payload)
	if err != nil {
		switch {
		case errors.Is(sigErr, ErrUnknownSigner):
			return judged{}, ReasonUnknownSigner
		case errors.Is(sigErr, ErrBadSignature):
			return judged{}, ReasonBadSignature
		}
		return judged{}, ReasonMalformed
	}
	j := judged{r: r, sum: summarize(r, origin, keyID, commit)}
	j.matches = r.Source != nil && r.Source.Before == tree
	switch {
	case errors.Is(sigErr, ErrUnknownSigner):
		j.reason = ReasonUnknownSigner
	case sigErr != nil:
		j.reason = ReasonBadSignature
	default:
		j.reason = acceptance(r, tree)
	}
	return j, ""
}

// acceptance is why a signed receipt is not evidence for tree, in the order
// of research R12, or "" when it is.
func acceptance(r Receipt, tree string) string {
	if r.Version != Version {
		return ReasonUnsupported
	}
	switch r.Status {
	case StatusPassed:
	case StatusFailed, StatusInterrupted, StatusRefused, StatusCached, StatusIncomplete:
		return r.Status // each status is its own reason
	default:
		return ReasonUnsupported
	}
	switch {
	case r.Source == nil:
		return ReasonNoSnapshot
	case r.Source.Before != r.Source.After:
		return ReasonChangedDuringRun
	case r.Source.Before != tree:
		return ReasonDifferentSnap
	case slices.ContainsFunc(r.Runefiles, func(f RunefileEntry) bool { return !f.Covered }):
		return ReasonUncoveredRunefile
	case len(r.UncoveredInputs) > 0:
		return ReasonUncoveredInputs
	}
	return ""
}

func summarize(r Receipt, origin, keyID, commit string) Summary {
	s := Summary{
		ID: r.ID, Origin: origin, Status: r.Status, ExitCode: r.ExitCode, Arguments: r.Arguments,
		Channel: r.Channel, Claims: r.Claims, Signer: keyID, FinishedAt: r.FinishedAt,
		UncoveredInputs: len(r.UncoveredInputs), Outputs: r.Outputs,
	}
	if s.Arguments == nil {
		s.Arguments = []Arg{}
	}
	if r.Related != nil {
		s.Reuses = r.Related.Reuses
	}
	if r.Source != nil {
		s.Commit, s.Dirty = r.Source.Commit, r.Source.Dirty
		s.Exact = r.Source.Commit == commit && !r.Source.Dirty
	}
	for _, f := range r.Runefiles {
		if !f.Covered {
			s.Uncovered = f.Path
			break
		}
	}
	return s
}

// verdictOf picks the newest accepted receipt; without one, the reason of the
// newest whose snapshot matches, else of the newest.
func verdictOf(task string, js []judged) Verdict {
	newest := func(keep func(judged) bool) *judged {
		var best *judged
		for i := range js {
			j := &js[i]
			if !keep(*j) {
				continue
			}
			if best == nil || cmp.Or(j.r.FinishedAt.Compare(best.r.FinishedAt), strings.Compare(j.r.ID, best.r.ID)) > 0 {
				best = j
			}
		}
		return best
	}
	if j := newest(func(j judged) bool { return j.reason == "" }); j != nil {
		return Verdict{Task: task, Verified: true, Receipt: &j.sum}
	}
	j := newest(func(j judged) bool { return j.matches })
	if j == nil {
		j = newest(func(judged) bool { return true })
	}
	if j == nil {
		return Verdict{Task: task, Reason: ReasonNoReceipt}
	}
	return Verdict{Task: task, Reason: j.reason, Receipt: &j.sum}
}
