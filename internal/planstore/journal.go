package planstore

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The events of the journal.
const (
	EventPlanCreated       = "plan-created"
	EventPlanReplaced      = "plan-replaced"
	EventApprovalRequested = "approval-requested"
	EventApprovalGiven     = "approval-given"
	EventApprovalNotGiven  = "approval-not-given"
	EventApplyRefused      = "apply-refused"
	EventApplyStarted      = "apply-started"
	EventApplyFinished     = "apply-finished"
)

const (
	journalName = "journal.jsonl"
	lockName    = "journal.lock"

	lockWaitDefault = 2 * time.Second
	lockPoll        = 5 * time.Millisecond
	// A writer holds the lock for one append. A lock this old was left by a
	// process that died.
	lockStale = 10 * time.Second
)

// Event is one line of the journal.
type Event struct {
	Time       time.Time `json:"time"`
	Event      string    `json:"event"`
	Surface    string    `json:"surface"`
	PlanTask   string    `json:"planTask"`
	Plan       string    `json:"plan,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Args       string    `json:"args"`
	ApplyTask  string    `json:"applyTask,omitempty"`
	Attempt    string    `json:"attempt,omitempty"`
	ReplacedBy string    `json:"replacedBy,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Exit       *int      `json:"exit,omitempty"`
	State      string    `json:"state,omitempty"`
	Tampered   bool      `json:"tampered,omitempty"`
	// Invocation is the id of the agent's call the event happened in, as
	// the audit log records it (spec 052). Empty on the command line.
	Invocation string `json:"invocation,omitempty"`
}

// Log appends e to the journal as one line, with the clock's time when e has
// none. Writers take a lock file in turn, so two processes neither tear a
// line nor lose one while the journal is cut to its limit.
func (s *Store) Log(e Event) error {
	dir, err := ensureDir(s.root)
	if err != nil {
		return err
	}
	if e.Time.IsZero() {
		e.Time = s.now()
	}
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	// The journal is read by people: arguments keep their <, > and &.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	unlock, err := s.lockJournal(dir)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(dir, journalName)
	if err := appendLine(path, line.Bytes()); err != nil {
		return err
	}
	return trimJournal(path, cmp.Or(s.journalMax, MaxJournalBytes))
}

// lockJournal creates the lock file and returns the function that removes it.
// The wait and the age of a stale lock are measured on the wall clock, not on
// Store.Now: the age is compared with a time the file system wrote.
func (s *Store) lockJournal(dir string) (unlock func(), err error) {
	lock := filepath.Join(dir, lockName)
	deadline := time.Now().Add(cmp.Or(s.lockWait, lockWaitDefault))
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		// Windows refuses with a permission error while the file another
		// writer just removed is still going away. That is a held lock too.
		if !errors.Is(err, fs.ErrExist) && !errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("lock journal: %w", err)
		}
		if stealStale(lock) {
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("lock journal: %w", err)
		}
		time.Sleep(lockPoll)
	}
}

func staleLock(lock string) bool {
	info, err := os.Lstat(lock)
	return err == nil && time.Since(info.ModTime()) > lockStale
}

// stealStale takes away a lock whose writer died holding it, and reports
// whether it did. The lock is moved aside, which one of any number of waiters
// can do, and only then judged: a waiter that looked at the stale lock and
// was overtaken would otherwise remove the fresh lock of the one that won. A
// fresh lock moved aside by mistake is put back unless a new one is already
// there.
func stealStale(lock string) bool {
	if !staleLock(lock) {
		return false
	}
	aside := lock + "." + newID()
	if os.Rename(lock, aside) != nil {
		return false
	}
	stale := staleLock(aside)
	if !stale {
		_ = os.Link(aside, lock)
	}
	_ = os.Remove(aside)
	return stale
}

func appendLine(path string, line []byte) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("write journal: %s %w", path, problemNotRegular)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	_, err = f.Write(line)
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	return nil
}

// trimJournal cuts a journal larger than limit down to the whole lines of its
// newer half. The caller holds the lock.
func trimJournal(path string, limit int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cut journal: %w", err)
	}
	if info.Size() <= limit {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cut journal: %w", err)
	}
	if err := atomicWrite(path, newerHalf(data)); err != nil {
		return fmt.Errorf("cut journal: %w", err)
	}
	return nil
}

// newerHalf returns the whole lines that start in the second half of data.
func newerHalf(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	start := len(data) / 2
	if start > 0 && data[start-1] != '\n' {
		next := bytes.IndexByte(data[start:], '\n')
		if next < 0 {
			start = len(data)
		} else {
			start += next + 1
		}
	}
	if start >= len(data) {
		// The last line alone fills the newer half. It is the event just
		// written, so it stays.
		start = bytes.LastIndexByte(data[:len(data)-1], '\n') + 1
	}
	return data[start:]
}
