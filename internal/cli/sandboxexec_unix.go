//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// The descriptors a worker is started with, after standard input, output
// and error.
const (
	workerJobFD    = 3
	workerResultFD = 4
)

// workerPipes takes the two pipes a worker is started with. Both must be
// pipes: a binary run by hand has none, or something else at those numbers.
// They are closed on exec, so the programs a body starts do not hold them.
func workerPipes() (job, result *os.File, err error) {
	for _, fd := range []int{workerJobFD, workerResultFD} {
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
			return nil, nil, errors.New("no worker pipes")
		}
		syscall.CloseOnExec(fd)
	}
	return os.NewFile(workerJobFD, "job"), os.NewFile(workerResultFD, "result"), nil
}

// holdTmp takes the lock that marks a body's temporary directory as in use.
// The lock is this process's and ends with it. release gives it up and
// removes the file.
//
// The lock is taken on a file that has another name, and the file is given
// the name of the lock only once it is held. A lock file that exists and
// that nobody holds is what a dead server leaves, and the sweep of another
// call removes its directory: a file created under the lock's name and
// locked a moment later reads as exactly that for the time between.
func holdTmp(lock string) (release func(), err error) {
	file, err := os.CreateTemp(filepath.Dir(lock), filepath.Base(lock)+".*")
	if err != nil {
		return nil, err
	}
	unnamed := file.Name()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		// A link fails where the name is taken, so a lock that another
		// body holds is never replaced.
		err = os.Link(unnamed, lock)
	}
	_ = os.Remove(unnamed)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = os.Remove(lock)
		_ = file.Close()
	}, nil
}

// tmpHeld reports whether some process holds the lock: this one, through
// another body, or another server of the project.
func tmpHeld(lock string) bool {
	file, err := os.OpenFile(lock, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

// closeInheritedOnExec marks every open descriptor from 3 up close-on-exec.
// Go does that for what it opens. A descriptor the server inherited from the
// program that started it would otherwise pass through the sandbox into
// every body.
func closeInheritedOnExec() {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
}
