package selfupgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)

	release, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	_, err = acquireLock(dir, time.Now())
	wantReason(t, "second acquire", err, ReasonInProgress)

	release()
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("release left the lock file: %v", err)
	}
	release2, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

func TestAcquireLockStale(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	age(t, lock, 14*time.Minute)
	_, err := acquireLock(dir, time.Now())
	wantReason(t, "14 minute old lock", err, ReasonInProgress)
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("a live lock was removed: %v", err)
	}

	age(t, lock, 16*time.Minute)
	release, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatalf("16 minute old lock was not taken over: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != lockName {
		t.Errorf("after takeover the directory holds %v, want only the lock", entries)
	}
	release()
}

func TestAcquireLockStaleRace(t *testing.T) {
	for range 20 {
		dir := t.TempDir()
		lock := filepath.Join(dir, lockName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, lock, 16*time.Minute)

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			winners int
			start   = make(chan struct{})
		)
		for range 20 {
			wg.Go(func() {
				<-start
				if _, err := acquireLock(dir, time.Now()); err == nil {
					mu.Lock()
					winners++
					mu.Unlock()
				}
			})
		}
		close(start)
		wg.Wait()
		if winners != 1 {
			t.Fatalf("%d goroutines hold the lock, want exactly 1", winners)
		}
	}
}

func TestAcquireLockAbandonedTakeover(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	takeover := filepath.Join(dir, takeoverName)
	for _, p := range []string{lock, takeover} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, p, 16*time.Minute)
	}
	// A takeover marker left by a killed process is cleared, and the next
	// attempt gets through.
	_, err := acquireLock(dir, time.Now())
	wantReason(t, "abandoned takeover, first attempt", err, ReasonInProgress)
	release, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatalf("second attempt after an abandoned takeover: %v", err)
	}
	release()
}

func TestAcquireLockUnwritable(t *testing.T) {
	_, err := acquireLock(filepath.Join(t.TempDir(), "absent"), time.Now())
	wantReason(t, "missing directory", err, ReasonNotWritable)

	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a non-root user on a system that enforces directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err = acquireLock(dir, time.Now())
	var e *Error
	wantReason(t, "read-only directory", err, ReasonNotWritable)
	if errorsAs(err, &e) && e.Subject != dir {
		t.Errorf("Subject = %q, want %q", e.Subject, dir)
	}
}

func TestSweepStale(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, d time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, p, d)
		return p
	}
	oldStage := write(".rune-upgrade-1234567", 16*time.Minute)
	newStage := write(".rune-upgrade-89", time.Minute)
	other := write("rune", 24*time.Hour)
	lock := write(lockName, 24*time.Hour)
	// Names that only look like staging files belong to someone else.
	notes := write(".rune-upgrade-notes.txt", 24*time.Hour)
	bare := write(".rune-upgrade-", 24*time.Hour)
	sub := filepath.Join(dir, ".rune-upgrade-555")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	sweepStaged(dir)

	// The lock holder owns the directory, so a staging file of any age is a leftover.
	for _, p := range []string{oldStage, newStage} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("staging file %s was kept", filepath.Base(p))
		}
	}
	for _, p := range []string{other, lock, notes, bare, sub} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(p), err)
		}
	}
	sweepStaged(filepath.Join(dir, "absent"))
}

// After a takeover the lock belongs to the new owner; the old one must not
// remove it on its way out.
func TestReleaseLeavesAnotherOwnersLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	release, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ownsLock(dir) {
		t.Fatal("ownsLock is false right after acquiring")
	}
	if err := os.WriteFile(lock, []byte("1 another-host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ownsLock(dir) {
		t.Error("ownsLock is true for a lock another process wrote")
	}
	release()
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("release removed a lock it no longer owned: %v", err)
	}
	if ownsLock(filepath.Join(dir, "absent")) {
		t.Error("ownsLock is true with no lock file")
	}
}

// deadPid returns the id of a process that has exited.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestAcquireLockRecordsOwner(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	host, _ := os.Hostname()
	b, _ := os.ReadFile(filepath.Join(dir, lockName))
	if want := fmt.Sprintf("%d %s\n", os.Getpid(), host); string(b) != want {
		t.Errorf("lock holds %q, want %q", b, want)
	}
}

// A run that was killed leaves a fresh lock; it must not block for 15 minutes.
func TestAcquireLockDeadOwner(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("no hostname")
	}
	dead := deadPid(t)
	tests := map[string]struct {
		content string
		gone    bool
	}{
		"dead process on this host": {fmt.Sprintf("%d %s\n", dead, host), true},
		"live process on this host": {fmt.Sprintf("%d %s\n", os.Getpid(), host), false},
		"process on another host":   {fmt.Sprintf("%d %s-elsewhere\n", dead, host), false},
		"no owner recorded":         {"", false},
		"half written":              {fmt.Sprintf("%d", dead), false},
		"not a pid":                 {"abc " + host + "\n", false},
		"zero pid":                  {"0 " + host + "\n", false},
	}
	for name, tt := range tests {
		dir := t.TempDir()
		lock := filepath.Join(dir, lockName)
		if err := os.WriteFile(lock, []byte(tt.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ownerGone(lock); got != tt.gone {
			t.Errorf("%s: ownerGone = %v, want %v", name, got, tt.gone)
		}
		release, err := acquireLock(dir, time.Now())
		if tt.gone {
			if err != nil {
				t.Errorf("%s: lock not taken over: %v", name, err)
				continue
			}
			release()
			continue
		}
		wantReason(t, name, err, ReasonInProgress)
	}
	if ownerGone(filepath.Join(t.TempDir(), "absent")) {
		t.Error("a missing lock reported a gone owner")
	}
}

func TestProcessExists(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Error("this process reported as not existing")
	}
	if processExists(deadPid(t)) {
		t.Error("an exited process reported as existing")
	}
}
