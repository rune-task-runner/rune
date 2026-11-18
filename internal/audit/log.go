package audit

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
)

const (
	// DefaultRetentionDays is how long a day file is kept when the operator
	// names no period.
	DefaultRetentionDays = 90

	// maxRetentionDays bounds the period. A day file is named with a year of
	// four digits, so none is older than this, and a larger number would
	// wrap the date it is counted back to into one every file is older than.
	maxRetentionDays = 366 * 10000

	dayLayout   = "2006-01-02"
	dayFileExt  = ".jsonl"
	projectFile = "project.json"
	lockName    = ".lock"

	lockWaitDefault = 2 * time.Second
	lockPoll        = 5 * time.Millisecond
	// A writer holds the lock for one append. A lock this old was left by a
	// process that died.
	lockStale = 10 * time.Second
)

// dayFile matches the name of a day file and nothing else, so retention
// never touches a file the log did not write.
var dayFile = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.jsonl$`)

// Options says where a log lives and how long it keeps its files.
type Options struct {
	// Base is the directory the logs of every project live under.
	Base string
	// Runefile is the resolved path of the project's Runefile.
	Runefile string
	// Outside lists the directories the log must not be inside: the places
	// a task of the project normally writes.
	Outside []string
	// RetentionDays is how many days a day file is kept. Zero keeps every
	// file.
	RetentionDays int
	// Now is the clock for retention. Nil means time.Now.
	Now func() time.Time
	// Warn is told about a day file that could not be removed. Nil drops
	// the warning. It is never called for a record that was not written:
	// Append returns that error.
	Warn func(error)
}

// Log is the audit log of one project. It is safe for concurrent use, and
// several processes may write to one directory.
type Log struct {
	dir       string
	base      string
	runefile  string
	retention int
	now       func() time.Time
	warn      func(error)

	// mu serializes the writers of this process. The lock file serializes
	// them with the writers of other processes.
	mu sync.Mutex
	// day is the UTC day of the last append, so that the first append of a
	// new day removes the files that became too old.
	day      string
	lockWait time.Duration
}

// Open returns the log of opts.Runefile under opts.Base, creating its
// directory when it is missing. It refuses a directory inside the project,
// one that is a symbolic link, and one that belongs to another user. Files
// past the retention period are removed.
func Open(opts Options) (*Log, error) {
	if opts.RetentionDays < 0 {
		return nil, fmt.Errorf("retention of %d days is negative", opts.RetentionDays)
	}
	l := &Log{
		dir:       ProjectDir(opts.Base, opts.Runefile),
		base:      opts.Base,
		runefile:  opts.Runefile,
		retention: min(opts.RetentionDays, maxRetentionDays),
		now:       opts.Now,
		warn:      opts.Warn,
	}
	if l.now == nil {
		l.now = time.Now
	}
	// Judged before anything is created, so a refused location is left as
	// it was found.
	resolved := resolve(l.dir)
	for _, project := range opts.Outside {
		if project == "" {
			continue
		}
		if within(resolved, resolve(project)) {
			return nil, &LocationError{Dir: l.dir, Project: project}
		}
	}
	if err := l.ensureDir(); err != nil {
		return nil, err
	}
	l.day = l.now().UTC().Format(dayLayout)
	l.prune()
	return l, nil
}

// Dir is the directory that holds the project's day files.
func (l *Log) Dir() string { return l.dir }

// ensureDir creates the base and the project directory when they are
// missing and checks that they are what the log would have created.
func (l *Log) ensureDir() error {
	if err := os.MkdirAll(l.base, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w; set RUNE_AUDIT_DIR to a writable directory", l.base, err)
	}
	if _, err := realDir(l.base); err != nil {
		return err
	}
	if err := os.Mkdir(l.dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("cannot create %s: %w; set RUNE_AUDIT_DIR to a writable directory", l.dir, err)
	}
	info, err := realDir(l.dir)
	if err != nil {
		return err
	}
	if !ownedByCurrentUser(info) {
		return &UnusableError{Path: l.dir, Reason: "belongs to another user"}
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(l.dir, 0o700); err != nil {
			return fmt.Errorf("restrict %s: %w", l.dir, err)
		}
	}
	// A hint for a person who browses the base directory: which project a
	// directory named by a digest belongs to.
	hint := filepath.Join(l.dir, projectFile)
	if _, err := os.Lstat(hint); errors.Is(err, fs.ErrNotExist) {
		data, err := json.Marshal(struct {
			Runefile string `json:"runefile"`
		}{l.runefile})
		if err != nil {
			return fmt.Errorf("write %s: %w", hint, err)
		}
		if err := os.WriteFile(hint, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", hint, err)
		}
	}
	return nil
}

// Append writes rec to the file of the UTC day its action started on. The
// end of an action is given the start of that action, so both are in one
// file. With sync the bytes are flushed to the disk before Append returns.
func (l *Log) Append(start time.Time, rec Record, sync bool) error {
	line, err := rec.Encode()
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// The directory is checked on every write: it may have been removed, or
	// replaced by a link, since the log was opened.
	if err := l.ensureDir(); err != nil {
		return err
	}
	unlock, err := lockDir(l.dir, cmp.Or(l.lockWait, lockWaitDefault))
	if err != nil {
		return err
	}
	err = appendLine(filepath.Join(l.dir, start.UTC().Format(dayLayout)+dayFileExt), line, sync)
	unlock()
	if err != nil {
		return err
	}
	if today := l.now().UTC().Format(dayLayout); today != l.day {
		l.day = today
		l.prune()
	}
	return nil
}

// prune removes the old day files and reports a failure as a warning: a
// file that stays must never stop a record from being written.
func (l *Log) prune() {
	if err := l.Prune(); err != nil && l.warn != nil {
		l.warn(err)
	}
}

// Prune removes the day files older than the retention period. Only files
// named as a day file are touched.
func (l *Log) Prune() error {
	if l.retention == 0 {
		return nil
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", l.dir, err)
	}
	today := l.now().UTC().Truncate(24 * time.Hour)
	oldest := today.AddDate(0, 0, -l.retention)
	var errs []error
	for _, entry := range entries {
		m := dayFile.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		day, err := time.Parse(dayLayout, m[1])
		if err != nil || !day.Before(oldest) {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// lockDir creates the lock file of dir and returns the function that
// removes it. The wait and the age of a stale lock are measured on the wall
// clock: the age is compared with a time the file system wrote.
func lockDir(dir string, wait time.Duration) (unlock func(), err error) {
	lock := filepath.Join(dir, lockName)
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !lockHeld(err, runtime.GOOS) {
			return nil, fmt.Errorf("lock %s: %w", dir, err)
		}
		if stealStale(lock) {
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("lock %s: another writer holds the lock", dir)
		}
		time.Sleep(lockPoll)
	}
}

// lockHeld reports whether err, from creating the lock file, says another
// writer holds the lock. Windows refuses with a permission error while the
// file another writer just removed is still going away, which is a held lock
// too. Elsewhere a permission error is a directory this user cannot write
// in, and waiting does not change that.
func lockHeld(err error, goos string) bool {
	return errors.Is(err, fs.ErrExist) || (goos == "windows" && errors.Is(err, fs.ErrPermission))
}

func staleLock(lock string) bool {
	info, err := os.Lstat(lock)
	return err == nil && time.Since(info.ModTime()) > lockStale
}

// stealStale takes away a lock whose writer died holding it, and reports
// whether it did. The lock is moved aside, which one of any number of
// waiters can do, and only then judged: a waiter that looked at the stale
// lock and was overtaken would otherwise remove the fresh lock of the one
// that won. A fresh lock moved aside by mistake is put back unless a new
// one is already there.
func stealStale(lock string) bool {
	if !staleLock(lock) {
		return false
	}
	aside := lock + "." + NewID()
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

// appendLine adds line to the file at path, creating it when it is missing.
// A file that ends in the middle of a record, left by a writer that died,
// gets a line break first, so the new record is a line of its own.
func appendLine(path string, line []byte, sync bool) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if torn(f) {
		line = append([]byte{'\n'}, line...)
	}
	// One Write for the whole record: with O_APPEND the system puts it at
	// the end in one step.
	_, err = f.Write(line)
	if err == nil && sync {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// torn reports whether f holds bytes and does not end with a line break.
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
