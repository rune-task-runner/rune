package observe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rune-task-runner/rune/internal/audit"
)

const (
	// MaxStoreBytes bounds one project's records.
	MaxStoreBytes = 4 << 20
	// CompactTarget is what a compaction keeps at most: the slack up to
	// MaxStoreBytes keeps a full store from being rewritten on every event.
	CompactTarget = 3 << 20
	// Retention is how long a record is kept.
	Retention = 30 * 24 * time.Hour

	eventsFile      = "events.jsonl"
	lockWaitDefault = 2 * time.Second
	stateHint       = "; set XDG_STATE_HOME to a writable directory"
)

var (
	// ErrBusy reports a store another writer held for the whole wait.
	ErrBusy = errors.New("the store is busy")
	// ErrInsideProject reports a store that would lie inside the project.
	ErrInsideProject = errors.New("the store would be inside the project")
)

// Base is the directory the stores of every project live under, in the
// state directory Rune keeps (audit.StateDir).
func Base(stateDir string) string { return filepath.Join(stateDir, "observe") }

// Store holds the records of one project, in a directory of its own under
// the base, named by audit.ProjectID of the project's root.
type Store struct {
	base, dir, root string
	now             func() time.Time
	lockWait        time.Duration
}

// OpenStore returns the store of the project at root, which is resolved and
// absolute. Nothing is created until something is written.
func OpenStore(base, root string) *Store {
	return &Store{
		base:     base,
		dir:      filepath.Join(base, audit.ProjectID(root)),
		root:     root,
		now:      time.Now,
		lockWait: lockWaitDefault,
	}
}

// Dir is the store's directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) events() string { return filepath.Join(s.dir, eventsFile) }

// Stats says what reading the store found besides the records.
type Stats struct {
	// Damaged counts the lines that are not a record.
	Damaged int
}

// lock checks the store's directory, creating it when missing, and takes
// its lock.
func (s *Store) lock() (unlock func(), err error) {
	if audit.Inside(s.dir, s.root) {
		return nil, ErrInsideProject
	}
	if err := audit.PrivateDir(s.base, s.dir, stateHint); err != nil {
		return nil, err
	}
	unlock, err = audit.LockDir(s.dir, s.lockWait)
	if errors.Is(err, audit.ErrLocked) {
		return nil, fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return unlock, err
}

// Append adds rec to the store. A record that would take the file past
// MaxStoreBytes, or a first record past Retention, first has the file
// compacted, so the file is never larger than MaxStoreBytes.
func (s *Store) Append(rec Record) error {
	line, err := rec.Encode()
	if err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	// One more byte for the line feed a torn file is given first.
	if err := s.compactFor(len(line) + 1); err != nil {
		return err
	}
	f, err := os.OpenFile(s.events(), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	// A file a writer that died left in the middle of a line would join
	// that line and this record into one damaged line.
	if torn(f) {
		line = append([]byte{'\n'}, line...)
	}
	// One Write for the whole record: with O_APPEND it lands at the end in
	// one step.
	_, err = f.Write(line)
	return errors.Join(err, f.Close())
}

// Read returns the records not older than Retention, oldest first. It takes
// no lock: a compaction replaces the file by renaming, so a reader sees the
// old file or the new one.
func (s *Store) Read() ([]Record, Stats, error) {
	recs, _, damaged, err := readRecords(s.events(), s.now().Add(-Retention))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Stats{}, nil
	}
	return recs, Stats{Damaged: damaged}, err
}

// Prune compacts the store when it is over a bound. A store with no records
// is left as it is.
func (s *Store) Prune() error {
	if _, err := os.Lstat(s.events()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.compactFor(0)
}

// Forget deletes the store and returns how many records it held.
func (s *Store) Forget() (int, error) {
	if _, err := os.Lstat(s.dir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return 0, err
	}
	recs, _, _, err := readRecords(s.events(), time.Time{})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		unlock()
		return 0, err
	}
	var errs []error
	for _, name := range []string{eventsFile, diagnosticsFile} {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	unlock()
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}
	// The directory goes too when nothing else is in it.
	_ = os.Remove(s.dir)
	return len(recs), nil
}

// compactFor compacts the file when adding n bytes would take it past
// MaxStoreBytes or its first record is past Retention. The lock is held.
func (s *Store) compactFor(n int) error {
	info, err := os.Lstat(s.events())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", s.events())
	}
	cutoff := s.now().Add(-Retention)
	if info.Size()+int64(n) <= MaxStoreBytes && !firstExpired(s.events(), cutoff) {
		return nil
	}
	_, lines, _, err := readRecords(s.events(), cutoff)
	if err != nil {
		return err
	}
	// The newest records that fit in CompactTarget.
	start, size := len(lines), 0
	for start > 0 && size+len(lines[start-1]) <= CompactTarget {
		start--
		size += len(lines[start])
	}
	return replaceFile(s.dir, s.events(), bytes.Join(lines[start:], nil))
}

// torn reports whether f is not empty and does not end with a line feed.
func torn(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// firstExpired reports whether the first line of the file is a record older
// than cutoff, or not a record at all: either way a compaction removes it.
func firstExpired(path string, cutoff time.Time) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	line, err := bufio.NewReaderSize(f, MaxRecordBytes+1).ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	rec, ok := decodeRecord(line)
	return !ok || rec.Time.Before(cutoff)
}

// readRecords reads the records of a file that are not older than cutoff,
// with their lines, and counts the lines that are not records.
func readRecords(path string, cutoff time.Time) (recs []Record, lines [][]byte, damaged int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			rec, ok := decodeRecord(line)
			switch {
			case !ok && err == nil:
				damaged++
			case !ok:
				// A last line with no line feed may be a record a writer is
				// still adding: Read takes no lock. A torn one is counted once
				// the next record has ended it.
			case !rec.Time.Before(cutoff):
				recs = append(recs, rec)
				if line[len(line)-1] != '\n' {
					line = append(line, '\n')
				}
				lines = append(lines, line)
			}
		}
		if errors.Is(err, io.EOF) {
			return recs, lines, damaged, nil
		}
		if err != nil {
			return nil, nil, 0, err
		}
	}
}

// decodeRecord reads one line as a record of this version.
func decodeRecord(line []byte) (Record, bool) {
	var rec Record
	if len(line) > MaxRecordBytes || json.Unmarshal(line, &rec) != nil || rec.V != RecordVersion {
		return Record{}, false
	}
	switch rec.Event {
	case KindPre, KindSuccess, KindFailure:
	default:
		return Record{}, false
	}
	return rec, rec.ToolUse != "" && rec.Command != ""
}

// replaceFile writes data to a temporary file in dir and renames it over
// path, so a reader sees the old file or the new one.
func replaceFile(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
