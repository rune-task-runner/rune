package selfupgrade

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	lockName     = ".rune-upgrade.lock"
	takeoverName = lockName + ".takeover"
	// A run is capped at runLimit, so anything older than this was left by a
	// process that was killed, not by one that is still working.
	staleAfter = 15 * time.Minute
)

// acquireLock makes this process the only one upgrading the binary in dir.
// The lock is a file created exclusively and removed by release. Creating it
// also proves the directory is writable, before anything is downloaded.
//
// A lock left by a killed run is taken over at once when its owner is known
// to be gone, and after staleAfter otherwise.
func acquireLock(dir string, now time.Time) (release func(), err error) {
	lock := filepath.Join(dir, lockName)
	created, err := createLock(lock)
	if err != nil {
		return nil, &Error{Reason: ReasonNotWritable, Subject: dir, Err: err}
	}
	if !created {
		if !abandoned(lock, now) {
			return nil, &Error{Reason: ReasonInProgress}
		}
		if err := takeOver(dir, lock, now); err != nil {
			return nil, err
		}
	}
	// Remove only a lock that is still this run's: after a takeover the file
	// belongs to someone else.
	return func() {
		if ownsLock(dir) {
			_ = os.Remove(lock)
		}
	}, nil
}

// lockOwner is what this process writes into a lock it holds.
func lockOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%d %s\n", os.Getpid(), host)
}

// ownsLock reports whether the lock in dir is the one this process created.
func ownsLock(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, lockName))
	return err == nil && string(b) == lockOwner()
}

// takeOver replaces a stale lock. Removing a stale file and creating a fresh
// one is not atomic, so only the holder of a second exclusive file may do it;
// everyone else backs off.
func takeOver(dir, lock string, now time.Time) error {
	takeover := filepath.Join(dir, takeoverName)
	created, err := createExclusive(takeover)
	if err != nil {
		return &Error{Reason: ReasonNotWritable, Subject: dir, Err: err}
	}
	if !created {
		// A marker this old was abandoned mid-takeover. Clear it so the next
		// run can proceed, and back off this time.
		if olderThan(takeover, now, staleAfter) {
			_ = os.Remove(takeover)
		}
		return &Error{Reason: ReasonInProgress}
	}
	defer func() { _ = os.Remove(takeover) }()

	// Another process may have finished a takeover between our two checks.
	if !abandoned(lock, now) {
		return &Error{Reason: ReasonInProgress}
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &Error{Reason: ReasonNotWritable, Subject: dir, Err: err}
	}
	created, err = createLock(lock)
	if err != nil {
		return &Error{Reason: ReasonNotWritable, Subject: dir, Err: err}
	}
	if !created {
		return &Error{Reason: ReasonInProgress}
	}
	return nil
}

// createLock creates the lock file and records who holds it.
func createLock(lock string) (bool, error) {
	created, err := createExclusive(lock)
	if err != nil || !created {
		return created, err
	}
	if err := os.WriteFile(lock, []byte(lockOwner()), 0o600); err != nil {
		_ = os.Remove(lock)
		return false, err
	}
	return true, nil
}

// abandoned reports whether the lock's owner is gone: it is too old for any
// live run, or it names a process on this machine that no longer exists.
func abandoned(lock string, now time.Time) bool {
	return olderThan(lock, now, staleAfter) || ownerGone(lock)
}

// ownerGone is true only when the lock names a process on this host and
// that process does not exist. An unreadable or foreign lock is not judged:
// a process id means nothing on another machine.
func ownerGone(lock string) bool {
	b, err := os.ReadFile(lock)
	if err != nil {
		return false
	}
	pidText, owner, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	pid, err := strconv.Atoi(pidText)
	if !ok || err != nil || pid <= 0 {
		return false
	}
	host, err := os.Hostname()
	if err != nil || host == "" || host != owner {
		return false
	}
	return !processExists(pid)
}

// createExclusive creates path only if it does not exist. It reports false,
// with no error, when it already does.
func createExclusive(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, f.Close()
}

// olderThan reports whether path was last modified more than d before now. A
// file that has vanished counts as not old: its owner just released it.
func olderThan(path string, now time.Time, d time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > d
}

// sweepStaged removes staging files killed runs left in dir. The caller holds
// the lock, so no live run owns any of them.
func sweepStaged(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		// Only what stage creates: a regular file named by the prefix and a number.
		n, ok := strings.CutPrefix(e.Name(), stagePrefix)
		if !ok || !allDigits(n) || !e.Type().IsRegular() {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
