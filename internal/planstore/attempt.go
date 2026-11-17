package planstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	claimName     = "claim"
	outcomeName   = "outcome.json"
	attemptPrefix = "attempt-"
	copyName      = "plan"
)

// claimFile is the content of a record's claim: who took the plan, and when.
type claimFile struct {
	Attempt   string    `json:"attempt"`
	ApplyTask string    `json:"applyTask"`
	Surface   string    `json:"surface"`
	Started   time.Time `json:"started"`
}

// outcomeFile is the content of outcome.json: the claim and how it ended.
type outcomeFile struct {
	claimFile
	Finished time.Time `json:"finished"`
	Exit     int       `json:"exit"`
	State    string    `json:"state"`
	Tampered bool      `json:"tampered"`
}

// AttemptInfo is the one attempt to apply a plan.
type AttemptInfo struct {
	Plan      string
	Attempt   string
	ApplyTask string
	Surface   string
	Started   time.Time // from the claim, or the claim file's modification time when it cannot be read
	Finished  time.Time // zero when unfinished
	Exit      int
	State     State // StateApplied, StateFailed or StateUnfinished
	Tampered  bool
}

// attemptFiles is what the claim and the outcome of a record directory say.
type attemptFiles struct {
	claimed  bool // a claim or an outcome exists, readable or not
	claim    claimFile
	started  time.Time
	finished bool // the outcome is readable and says applied or failed
	outcome  outcomeFile
	state    State
}

func loadAttempt(dir string) attemptFiles {
	var a attemptFiles
	claimPath := filepath.Join(dir, claimName)
	outcomePath := filepath.Join(dir, outcomeName)
	// The claim comes last, so its modification time is the one that stays.
	for _, path := range [...]string{outcomePath, claimPath} {
		if info, err := os.Lstat(path); err == nil {
			a.claimed = true
			a.started = info.ModTime().UTC()
		}
	}
	if !a.claimed {
		return a
	}
	var outcome outcomeFile
	if readJSON(outcomePath, &outcome) == nil {
		if state, ok := outcomeState(outcome.State); ok {
			a.finished, a.outcome, a.state = true, outcome, state
			a.claim = outcome.claimFile
		}
	}
	var claim claimFile
	if readJSON(claimPath, &claim) == nil && !claim.Started.IsZero() {
		a.claim = claim
	}
	if !a.claim.Started.IsZero() {
		a.started = a.claim.Started
	}
	return a
}

func outcomeState(text string) (State, bool) {
	switch text {
	case StateApplied.String():
		return StateApplied, true
	case StateFailed.String():
		return StateFailed, true
	}
	return StateUnfinished, false
}

func (a attemptFiles) info(plan string) (AttemptInfo, bool) {
	if !a.claimed {
		return AttemptInfo{}, false
	}
	info := AttemptInfo{
		Plan:      plan,
		Attempt:   a.claim.Attempt,
		ApplyTask: a.claim.ApplyTask,
		Surface:   a.claim.Surface,
		Started:   a.started,
		State:     StateUnfinished,
	}
	if a.finished {
		info.Finished = a.outcome.Finished
		info.Exit = a.outcome.Exit
		info.State = a.state
		info.Tampered = a.outcome.Tampered
	}
	return info, true
}

// lastEvent is the latest of the record's creation, its claim and its outcome.
func (a attemptFiles) lastEvent(created time.Time) time.Time {
	last := created
	for _, t := range [...]time.Time{a.started, a.outcome.Finished} {
		if t.After(last) {
			last = t
		}
	}
	return last
}

// Attempt is a claimed plan: the one run that may apply it.
type Attempt struct {
	ID     string
	Path   string // the private copy of the artifact
	Record Record

	store    *Store
	claim    claimFile
	done     bool
	tampered bool
	err      error
}

// Claim takes rec for one apply and returns a private copy of its artifact
// with the recorded digest. Of any number of callers one succeeds. A Claim
// that fails leaves the record as it was.
func (s *Store) Claim(rec Record, applyTask, surface string) (*Attempt, error) {
	dir, err := s.lookup(rec)
	if err != nil {
		return nil, err
	}
	switch s.StateOf(rec) {
	case StateReady:
	case StateReplaced:
		return nil, ErrReplaced
	case StateExpired:
		return nil, ErrExpired
	default:
		return nil, ErrClaimed
	}
	a := &Attempt{
		ID:     newID(),
		Record: rec,
		store:  s,
	}
	a.claim = claimFile{Attempt: a.ID, ApplyTask: applyTask, Surface: surface, Started: s.now()}
	copyDir := filepath.Join(dir, attemptPrefix+a.ID)
	if a.Path, err = filepath.Abs(filepath.Join(copyDir, copyName)); err != nil {
		return nil, fmt.Errorf("claim plan: %w", err)
	}
	// The copy is made before the claim: a plan whose bytes changed must stay
	// unclaimed, and the claim must never exist without its copy.
	if err := copyArtifact(filepath.Join(dir, artifactName), copyDir, rec.Artifact); err != nil {
		_ = removeCopy(copyDir)
		return nil, err
	}
	if err := takeClaim(dir, a.claim); err != nil {
		_ = removeCopy(copyDir)
		return nil, err
	}
	return a, nil
}

// takeClaim writes the claim of a record directory and then looks for the
// replaced marker. A Save may have marked the record after Claim read it as
// ready, while the artifact was copied. The claim is then withdrawn, so a
// plan that was replaced is not applied. See placeMarker for the other side.
func takeClaim(dir string, claim claimFile) error {
	path := filepath.Join(dir, claimName)
	if err := writeClaim(path, claim); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, replacedName)); err == nil {
		_ = os.Remove(path)
		return ErrReplaced
	}
	return nil
}

// copyArtifact copies the artifact into a new attempt directory and checks
// the digest of the bytes it copied.
func copyArtifact(src, copyDir string, want Digest) error {
	if err := os.Mkdir(copyDir, 0o700); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &ArtifactError{What: artifactName, Detail: string(problemMissing)}
		}
		return fmt.Errorf("claim plan: %w", err)
	}
	out, err := os.OpenFile(filepath.Join(copyDir, copyName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return fmt.Errorf("claim plan: %w", err)
	}
	if err := checkStored(src, artifactName, want, out); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("claim plan: %w", err)
	}
	return nil
}

// writeClaim creates the claim file. The exclusive create is what makes one
// caller win, across goroutines and processes.
func writeClaim(path string, claim claimFile) error {
	data, err := json.Marshal(claim)
	if err != nil {
		return fmt.Errorf("claim plan: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ErrClaimed
	}
	if err != nil {
		return fmt.Errorf("claim plan: %w", err)
	}
	_, err = f.Write(append(data, '\n'))
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("claim plan: %w", err)
	}
	return nil
}

// Finish records how the apply ended and removes the artifact, the summary
// and the private copy. The copy is hashed first: a copy that changed makes
// the attempt failed whatever the exit code. A second call returns the result
// of the first, because by then the copy is gone and would count as changed.
func (a *Attempt) Finish(exit int) (tampered bool, err error) {
	if a.done {
		return a.tampered, a.err
	}
	copyDir := filepath.Dir(a.Path)
	dir := filepath.Dir(copyDir)
	tampered = checkStored(a.Path, artifactName, a.Record.Artifact, io.Discard) != nil
	state := StateApplied
	if exit != 0 || tampered {
		state = StateFailed
	}
	err = writeJSON(filepath.Join(dir, outcomeName), outcomeFile{
		claimFile: a.claim,
		Finished:  a.store.now(),
		Exit:      exit,
		State:     state.String(),
		Tampered:  tampered,
	})
	if err = errors.Join(err, removeBytes(dir), removeCopy(copyDir)); err != nil {
		err = fmt.Errorf("finish plan %s: %w", a.Record.ID, err)
	}
	a.done, a.tampered, a.err = true, tampered, err
	return tampered, err
}
