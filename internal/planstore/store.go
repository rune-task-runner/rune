// Package planstore keeps the plans a task captured until an apply task takes
// one of them, exactly once. Each plan is a directory under <root>/.rune/plans
// with a record, the artifact and its summary. The state of a plan is derived
// from the files of that directory, so separate processes agree on it without
// sharing a lock.
package planstore

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The limits of the store.
const (
	MaxArtifactBytes int64 = 256 << 20
	MaxSummaryBytes  int64 = 1 << 20
	Retention              = 7 * 24 * time.Hour
	UnfinishedGrace        = 24 * time.Hour
	MaxJournalBytes  int64 = 1 << 20
)

const (
	recordName   = "record.json"
	artifactName = "artifact"
	summaryName  = "summary"
	replacedName = "replaced"

	detailDigest = "does not have the recorded digest"

	// Records, claims and outcomes are a few hundred bytes. The cap keeps a
	// file put in their place from being read whole.
	maxMetaBytes = 1 << 20
)

// State is what the files of a plan's directory say about it.
type State int

// The states of a plan. Only a ready plan can be applied.
const (
	StateReady State = iota
	StateUnfinished
	StateApplied
	StateFailed
	StateReplaced
	StateExpired
)

func (s State) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateUnfinished:
		return "unfinished"
	case StateApplied:
		return "applied"
	case StateFailed:
		return "failed"
	case StateReplaced:
		return "replaced"
	case StateExpired:
		return "expired"
	}
	return "unknown"
}

// Digest identifies the bytes of a stored file.
type Digest struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Binding is what a plan was made with. The engine compares it before an
// apply; the store only keeps it.
type Binding struct {
	WorkDir    string `json:"workDir"`
	Runefile   string `json:"runefile"`
	Config     string `json:"config"`
	Tool       string `json:"tool,omitempty"`
	ToolPath   string `json:"toolPath,omitempty"`
	ToolDigest string `json:"toolDigest,omitempty"`
	Host       string `json:"host"`
}

// Git is the repository state a plan was made in, as far as it was known.
type Git struct {
	Commit string `json:"commit,omitempty"`
	Dirty  *bool  `json:"dirty,omitempty"`
}

// Record describes one stored plan. It never holds plan or summary bytes.
type Record struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	PlanTask    string    `json:"planTask"`
	ApplyTasks  []string  `json:"applyTasks"`
	Args        string    `json:"args"`
	Binding     Binding   `json:"binding"`
	Artifact    Digest    `json:"artifact"`
	Summary     Digest    `json:"summary"`
	RuneVersion string    `json:"runeVersion,omitempty"`
	Git         Git       `json:"git"`
	Surface     string    `json:"surface"`
}

// SaveInput is a plan to store. Record carries the fields the caller
// computed: Key, PlanTask, ApplyTasks, Args, Binding, RuneVersion, Git and
// Surface. Save fills in the rest.
type SaveInput struct {
	Record       Record
	Expiry       time.Duration
	ArtifactPath string
	SummaryPath  string
	MaxArtifact  int64 // zero means MaxArtifactBytes
	MaxSummary   int64 // zero means MaxSummaryBytes
}

// Errors of Claim for a plan that is not ready.
var (
	ErrClaimed  = errors.New("plan was already claimed")
	ErrReplaced = errors.New("plan was replaced by a newer one")
	ErrExpired  = errors.New("plan has expired")
)

// ArtifactError reports a stored file that is not what its record says. What
// is "artifact" or "summary".
type ArtifactError struct {
	What   string
	Detail string
}

func (e *ArtifactError) Error() string { return "plan " + e.What + " " + e.Detail }

// SourceError reports a file that Save cannot take.
type SourceError struct {
	Path   string
	Detail string
}

func (e *SourceError) Error() string { return e.Path + " " + e.Detail }

// UnusableError reports a store directory that is a link, is not a directory
// or belongs to another user.
type UnusableError struct {
	Path   string
	Reason string
}

func (e *UnusableError) Error() string { return "plan store " + e.Path + " " + e.Reason }

// Store is the plan store of one Runefile directory.
type Store struct {
	// Now is the clock for every time the store writes or compares. Nil means
	// time.Now.
	Now func() time.Time

	root string

	// Zero means the limit of the contract. Tests shorten them.
	journalMax int64
	lockWait   time.Duration
}

// Open returns the store under root. It does not touch the file system: the
// store is created by the first Save.
func Open(root string) *Store { return &Store{root: root} }

// Dir is the directory the store keeps its plans in.
func (s *Store) Dir() string { return filepath.Join(s.root, runeDirName, plansDirName) }

func (s *Store) recordDir(id string) string { return filepath.Join(s.Dir(), id) }

// now drops the monotonic reading, so a time compares equal to itself after
// it went through record.json.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Round(0)
	}
	return time.Now().UTC().Round(0)
}

// Save stores the two source files as a new plan and marks every older,
// unclaimed plan of the same key as replaced. It returns the records it
// marked.
func (s *Store) Save(in SaveInput) (rec Record, replaced []Record, err error) {
	dir, err := ensureDir(s.root)
	if err != nil {
		return Record{}, nil, err
	}
	id := newID()
	recDir := filepath.Join(dir, id)
	if err := os.Mkdir(recDir, 0o700); err != nil {
		return Record{}, nil, fmt.Errorf("store plan: %w", err)
	}
	rec, err = s.fill(in, id, recDir)
	if err != nil {
		_ = os.RemoveAll(recDir)
		return Record{}, nil, err
	}
	return rec, s.replaceOlder(rec), nil
}

// fill copies the sources into recDir and writes the record last: a directory
// without a record is invisible, so a plan appears whole or not at all.
func (s *Store) fill(in SaveInput, id, recDir string) (Record, error) {
	artifact, err := storeSource(in.ArtifactPath, filepath.Join(recDir, artifactName), cmp.Or(in.MaxArtifact, MaxArtifactBytes))
	if err != nil {
		return Record{}, err
	}
	summary, err := storeSource(in.SummaryPath, filepath.Join(recDir, summaryName), cmp.Or(in.MaxSummary, MaxSummaryBytes))
	if err != nil {
		return Record{}, err
	}
	rec := in.Record
	rec.ID = id
	now := s.now()
	rec.Created = s.after(rec.Key, now)
	rec.Expires = now.Add(in.Expiry)
	rec.Artifact = artifact
	rec.Summary = summary
	if err := writeJSON(filepath.Join(recDir, recordName), rec); err != nil {
		return Record{}, fmt.Errorf("store plan: %w", err)
	}
	return rec, nil
}

// after returns now, or a time just after the newest record of key when the
// clock has not passed it. Records are ordered by creation time, so a plan
// stored later must not count as the older one because the clock stepped
// back, or because another host that shares the store runs ahead: it would
// be replaced by the plan it replaces.
func (s *Store) after(key string, now time.Time) time.Time {
	for _, rec := range s.records() {
		if rec.Key == key && !now.After(rec.Created) {
			now = rec.Created.Add(time.Nanosecond)
		}
	}
	return now
}

// storeSource copies the file at src to dst and returns its digest. The limit
// is enforced on the bytes copied, not on the size the file reported, so a
// source that grows while it is read is refused too.
func storeSource(src, dst string, limit int64) (Digest, error) {
	tooLarge := &SourceError{Path: src, Detail: "is larger than " + formatLimit(limit)}
	in, info, err := openRegular(src)
	if problem, ok := errors.AsType[fileProblem](err); ok {
		return Digest{}, &SourceError{Path: src, Detail: string(problem)}
	}
	if err != nil {
		return Digest{}, fmt.Errorf("store plan: %w", err)
	}
	defer func() { _ = in.Close() }()
	if info.Size() > limit {
		return Digest{}, tooLarge
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return Digest{}, fmt.Errorf("store plan: %w", err)
	}
	digest, err := hashCopy(out, in, limit)
	if err = errors.Join(err, out.Close()); err != nil {
		return Digest{}, fmt.Errorf("store plan: %w", err)
	}
	if digest.Size > limit {
		return Digest{}, tooLarge
	}
	return digest, nil
}

// hashCopy copies at most limit+1 bytes of r to w and returns the digest of
// what it copied. The extra byte lets the caller see a stream longer than
// limit without reading it to the end.
func hashCopy(w io.Writer, r io.Reader, limit int64) (Digest, error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(r, limit+1))
	if err != nil {
		return Digest{}, err
	}
	return Digest{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

func formatLimit(n int64) string {
	const mib = 1 << 20
	if n >= mib && n%mib == 0 {
		return fmt.Sprintf("%d MiB", n/mib)
	}
	return fmt.Sprintf("%d bytes", n)
}

func newID() string {
	var b [8]byte
	// crypto/rand.Read never returns an error: it ends the process instead.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// validID keeps an id from a record file or a caller from naming a path
// outside the store.
func validID(id string) bool {
	return len(id) == 16 && strings.Trim(id, "0123456789abcdef") == ""
}

// writeJSON writes v to path through a temporary name, so a reader sees the
// old file or the whole new one.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	f, _, err := openRegular(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMetaBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func compareRecords(a, b Record) int {
	return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
}

// records returns every readable record of the store, oldest first. A
// directory whose record cannot be read, or names another id, is skipped.
func (s *Store) records() []Record {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		return nil
	}
	var recs []Record
	for _, e := range entries {
		if !e.IsDir() || !validID(e.Name()) {
			continue
		}
		var rec Record
		if readJSON(filepath.Join(s.recordDir(e.Name()), recordName), &rec) != nil || rec.ID != e.Name() {
			continue
		}
		recs = append(recs, rec)
	}
	slices.SortFunc(recs, compareRecords)
	return recs
}

// replaceOlder marks the unclaimed records of rec's key that are older than
// rec. It never marks a newer one, so concurrent Saves cannot replace each
// other.
func (s *Store) replaceOlder(rec Record) []Record {
	var marked []Record
	for _, old := range s.records() {
		if old.Key == rec.Key && compareRecords(old, rec) < 0 && s.markReplaced(old) {
			marked = append(marked, old)
		}
	}
	return marked
}

// markReplaced marks an unclaimed record as replaced and removes its bytes.
// The marker is created exclusively, so of several callers one reports the
// record as marked.
func (s *Store) markReplaced(rec Record) bool {
	dir := s.recordDir(rec.ID)
	if loadAttempt(dir).claimed {
		return false
	}
	return placeMarker(dir)
}

// placeMarker creates the replaced marker of a record directory and removes
// the record's bytes. A Claim can run beside it: it read the record as ready
// before the marker existed. The claim is looked for again after the marker is
// written, and takeClaim looks for the marker after the claim is written, so
// one of the two always sees the other and a plan is never both applied and
// reported as replaced. The marker stays when a claim is found: that claim
// may be one that withdraws because of the marker.
func placeMarker(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, replacedName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	if loadAttempt(dir).claimed {
		return false
	}
	_ = removeBytes(dir)
	return true
}

// removeBytes removes the artifact and the summary of a record directory.
func removeBytes(dir string) error {
	return errors.Join(
		os.RemoveAll(filepath.Join(dir, artifactName)),
		os.RemoveAll(filepath.Join(dir, summaryName)),
	)
}

func stateOf(rec Record, dir string, a attemptFiles, now time.Time) State {
	switch {
	case a.finished:
		return a.state
	case a.claimed:
		return StateUnfinished
	}
	if _, err := os.Lstat(filepath.Join(dir, replacedName)); err == nil {
		return StateReplaced
	}
	if now.After(rec.Expires) {
		return StateExpired
	}
	return StateReady
}

// StateOf returns the state of a record as its files say now. A caller that
// looked a plan up earlier uses it to tell a plan another call has used since
// from one whose stored bytes went missing.
func (s *Store) StateOf(rec Record) State {
	dir := s.recordDir(rec.ID)
	return stateOf(rec, dir, loadAttempt(dir), s.now())
}

// Latest returns the newest record of key and its state. A store that does
// not exist holds no record, and is not created.
func (s *Store) Latest(key string) (Record, State, bool, error) {
	if ok, err := checkDir(s.root); err != nil || !ok {
		return Record{}, StateReady, false, err
	}
	for _, rec := range slices.Backward(s.records()) {
		if rec.Key == key {
			return rec, s.StateOf(rec), true, nil
		}
	}
	return Record{}, StateReady, false, nil
}

// lookup returns the directory of rec for a read. A store that does not exist
// is no error here: the caller finds the files missing.
func (s *Store) lookup(rec Record) (string, error) {
	if _, err := checkDir(s.root); err != nil {
		return "", err
	}
	if !validID(rec.ID) {
		return "", &ArtifactError{What: artifactName, Detail: string(problemMissing)}
	}
	return s.recordDir(rec.ID), nil
}

// AttemptOf returns the claim of rec and its outcome. It reports false for a
// record that was never claimed.
func (s *Store) AttemptOf(rec Record) (AttemptInfo, bool) {
	dir, err := s.lookup(rec)
	if err != nil {
		return AttemptInfo{}, false
	}
	return loadAttempt(dir).info(rec.ID)
}

// LastAttempt returns the attempt of the newest claimed record of key, other
// than the record exceptID.
func (s *Store) LastAttempt(key, exceptID string) (AttemptInfo, bool) {
	if ok, err := checkDir(s.root); err != nil || !ok {
		return AttemptInfo{}, false
	}
	for _, rec := range slices.Backward(s.records()) {
		if rec.Key != key || rec.ID == exceptID {
			continue
		}
		if info, ok := loadAttempt(s.recordDir(rec.ID)).info(rec.ID); ok {
			return info, true
		}
	}
	return AttemptInfo{}, false
}

// checkStored reads a stored file through one handle into w and compares its
// digest with want. What it wrote to w is good only when it returns nil.
func checkStored(path, what string, want Digest, w io.Writer) error {
	f, _, err := openRegular(path)
	if problem, ok := errors.AsType[fileProblem](err); ok {
		// A link is one of the ways a stored file is not a regular file.
		if problem == problemSymlink {
			problem = problemNotRegular
		}
		return &ArtifactError{What: what, Detail: string(problem)}
	}
	if err != nil {
		return fmt.Errorf("read plan %s: %w", what, err)
	}
	defer func() { _ = f.Close() }()
	got, err := hashCopy(w, f, want.Size)
	if err != nil {
		return fmt.Errorf("read plan %s: %w", what, err)
	}
	if got != want {
		return &ArtifactError{What: what, Detail: detailDigest}
	}
	return nil
}

// Verify compares the stored artifact and summary of rec with the record.
func (s *Store) Verify(rec Record) error {
	dir, err := s.lookup(rec)
	if err != nil {
		return err
	}
	if err := checkStored(filepath.Join(dir, artifactName), artifactName, rec.Artifact, io.Discard); err != nil {
		return err
	}
	return checkStored(filepath.Join(dir, summaryName), summaryName, rec.Summary, io.Discard)
}

// Summary returns the stored summary of rec, read through the same check as
// Verify.
func (s *Store) Summary(rec Record) (string, error) {
	dir, err := s.lookup(rec)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	if err := checkStored(filepath.Join(dir, summaryName), summaryName, rec.Summary, &text); err != nil {
		return "", err
	}
	return text.String(), nil
}

// Prune removes what the store no longer needs: the bytes of every plan that
// cannot be applied any more, the copies made of them, and records past their
// retention. It also marks the older unclaimed records of a key that a
// concurrent Save left unmarked. It reports nothing: a store it cannot clean
// now is cleaned by a later call.
func (s *Store) Prune() {
	if ok, err := checkDir(s.root); err != nil || !ok {
		return
	}
	now := s.now()
	recs := s.records()
	newest := make(map[string]string, len(recs))
	for _, rec := range recs {
		newest[rec.Key] = rec.ID
	}
	for _, rec := range recs {
		if newest[rec.Key] != rec.ID {
			s.markReplaced(rec)
		}
		dir := s.recordDir(rec.ID)
		a := loadAttempt(dir)
		state := stateOf(rec, dir, a, now)
		// An apply may still be reading the copy.
		if state == StateUnfinished && now.Sub(a.started) < UnfinishedGrace {
			continue
		}
		switch {
		case now.Sub(a.lastEvent(rec.Created)) > Retention:
			removeCopies(dir)
			_ = os.RemoveAll(dir)
		case state == StateReady:
		case state == StateReplaced:
			// A Claim that was killed left its copy. One still running finds
			// the marker and withdraws.
			_ = removeBytes(dir)
			removeCopies(dir)
		case state == StateExpired:
			_ = removeBytes(dir)
			// A Claim that read the plan as ready may still be writing its
			// copy, and takes the plan when it is done.
			if now.Sub(rec.Expires) >= UnfinishedGrace {
				removeCopies(dir)
			}
		default:
			_ = removeBytes(dir)
			removeCopies(dir)
		}
	}
	s.removeOrphans(recs, now)
}

// removeOrphans removes the plan directories that have no readable record. A
// Save writes its record last, so one that was killed leaves the plan's bytes
// in a directory nothing else would ever look at. A young one may be a Save
// still copying, and is left alone.
func (s *Store) removeOrphans(recs []Record, now time.Time) {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		return
	}
	known := make(map[string]bool, len(recs))
	for _, rec := range recs {
		known[rec.ID] = true
	}
	for _, e := range entries {
		if !e.IsDir() || !validID(e.Name()) || known[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < UnfinishedGrace {
			continue
		}
		dir := s.recordDir(e.Name())
		removeCopies(dir)
		_ = os.RemoveAll(dir)
	}
}

// removeCopies removes every attempt directory of a record directory.
func removeCopies(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), attemptPrefix) {
			_ = removeCopy(filepath.Join(dir, e.Name()))
		}
	}
}

// removeCopy removes an attempt directory. The apply body had the copy in
// hand and may have taken the write permission from the directory, which
// would keep its entries from being removed.
func removeCopy(dir string) error {
	if info, err := os.Lstat(dir); err == nil && info.IsDir() {
		_ = os.Chmod(dir, 0o700)
	}
	return os.RemoveAll(dir)
}
