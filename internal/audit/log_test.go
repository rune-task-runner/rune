package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var testDay = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

func fixedClock(now time.Time) func() time.Time { return func() time.Time { return now } }

// openLog opens a log in a temporary base, outside a temporary project.
func openLog(t *testing.T, opts Options) *Log {
	t.Helper()
	if opts.Base == "" {
		opts.Base = t.TempDir()
	}
	if opts.Runefile == "" {
		opts.Runefile = "/repo/Runefile"
	}
	if opts.Now == nil {
		opts.Now = fixedClock(testDay)
	}
	l, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func callStart(id string, at time.Time) Record {
	return Record{Type: TypeCall, Phase: PhaseStart, ID: id, Time: at, Tool: "build", Task: "build"}
}

func callEnd(id string, at time.Time, outcome string, exit int) Record {
	ms := int64(5)
	return Record{Type: TypeCall, Phase: PhaseEnd, ID: id, Time: at, Outcome: outcome, ExitCode: &exit, DurationMs: &ms}
}

func dayPath(l *Log, day time.Time) string {
	return filepath.Join(l.Dir(), day.UTC().Format(dayLayout)+dayFileExt)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestOpenCreatesDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state", "rune", "audit")
	l := openLog(t, Options{Base: base})
	if l.Dir() != ProjectDir(base, "/repo/Runefile") {
		t.Errorf("Dir = %q", l.Dir())
	}
	data, err := os.ReadFile(filepath.Join(l.Dir(), projectFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"runefile":"/repo/Runefile"}`+"\n" {
		t.Errorf("project.json = %q", data)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for path, want := range map[string]os.FileMode{
		base:                                0o700,
		l.Dir():                             0o700,
		filepath.Join(l.Dir(), projectFile): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s has mode %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

func TestOpenTightensMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes do not restrict access on Windows")
	}
	base := t.TempDir()
	dir := ProjectDir(base, "/repo/Runefile")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	openLog(t, Options{Base: base})
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode %o after Open", info.Mode().Perm())
	}
}

func TestOpenRefusesInsideProject(t *testing.T) {
	project := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	for name, base := range map[string]string{
		"runefile directory": filepath.Join(project, ".audit"),
		"working directory":  filepath.Join(work, "logs"),
		"the directory":      project,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Open(Options{Base: base, Runefile: "/repo/Runefile", Outside: []string{project, "", work}})
			var loc *LocationError
			if !errors.As(err, &loc) {
				t.Fatalf("got %v, want a LocationError", err)
			}
			if !strings.HasSuffix(err.Error(), "set RUNE_AUDIT_DIR to a directory outside it") {
				t.Errorf("text: %s", err)
			}
			if _, statErr := os.Stat(filepath.Join(project, ".audit")); statErr == nil {
				t.Error("the refused directory was created")
			}
		})
	}
}

func TestOpenRefusesLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege on Windows")
	}
	t.Run("base", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(root, "base")
		if err := os.Symlink(target, base); err != nil {
			t.Fatal(err)
		}
		_, err := Open(Options{Base: base, Runefile: "/repo/Runefile"})
		var unusable *UnusableError
		if !errors.As(err, &unusable) || unusable.Reason != "is a symbolic link" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("project directory", func(t *testing.T) {
		base := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, ProjectDir(base, "/repo/Runefile")); err != nil {
			t.Fatal(err)
		}
		_, err := Open(Options{Base: base, Runefile: "/repo/Runefile"})
		var unusable *UnusableError
		if !errors.As(err, &unusable) || unusable.Reason != "is a symbolic link" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("project directory is a file", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(ProjectDir(base, "/repo/Runefile"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(Options{Base: base, Runefile: "/repo/Runefile"})
		var unusable *UnusableError
		if !errors.As(err, &unusable) || unusable.Reason != "is not a directory" {
			t.Fatalf("got %v", err)
		}
	})
}

func TestOpenRefusesAnotherUsersDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() != 0 {
		t.Skip("needs root to give a directory to another user")
	}
	base := t.TempDir()
	dir := ProjectDir(base, "/repo/Runefile")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, 65534, -1); err != nil {
		t.Skipf("cannot change the owner: %v", err)
	}
	_, err := Open(Options{Base: base, Runefile: "/repo/Runefile"})
	var unusable *UnusableError
	if !errors.As(err, &unusable) || unusable.Reason != "belongs to another user" {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRefusesNegativeRetention(t *testing.T) {
	if _, err := Open(Options{Base: t.TempDir(), Runefile: "/repo/Runefile", RetentionDays: -1}); err == nil {
		t.Fatal("no error")
	}
}

func TestOpenCannotCreateBase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Options{Base: filepath.Join(file, "audit"), Runefile: "/repo/Runefile"})
	if err == nil || !strings.Contains(err.Error(), "set RUNE_AUDIT_DIR to a writable directory") {
		t.Fatalf("got %v", err)
	}
}

func TestAppendWritesToTheStartDay(t *testing.T) {
	l := openLog(t, Options{})
	start := time.Date(2026, 11, 18, 23, 59, 50, 0, time.UTC)
	end := start.Add(20 * time.Second) // the next UTC day
	if err := l.Append(start, callStart("aa", start), true); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(start, callEnd("aa", end, OutcomeCompleted, 0), true); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, dayPath(l, start))
	if len(lines) != 2 {
		t.Fatalf("%d lines in the start's file", len(lines))
	}
	if _, err := os.Stat(dayPath(l, end)); err == nil {
		t.Error("the end went to the file of its own day")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dayPath(l, start))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("day file has mode %o", info.Mode().Perm())
		}
	}
}

func TestAppendRepairsTornTail(t *testing.T) {
	l := openLog(t, Options{})
	if err := l.Append(testDay, callStart("aa", testDay), false); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(dayPath(l, testDay), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"type":"call","pha`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := l.Append(testDay, callStart("bb", testDay), false); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, dayPath(l, testDay))
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3: %q", len(lines), lines)
	}
	var rec Record
	if err := json.Unmarshal([]byte(lines[2]), &rec); err != nil || rec.ID != "bb" {
		t.Errorf("the new record is not a line of its own: %q", lines[2])
	}
}

func TestAppendFailsOnADirectory(t *testing.T) {
	l := openLog(t, Options{})
	if err := os.Mkdir(dayPath(l, testDay), 0o700); err != nil {
		t.Fatal(err)
	}
	err := l.Append(testDay, callStart("aa", testDay), true)
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.Dir(), lockName)); err == nil {
		t.Error("the lock was left behind")
	}
}

func TestAppendRecreatesDirectory(t *testing.T) {
	l := openLog(t, Options{})
	if err := os.RemoveAll(l.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(testDay, callStart("aa", testDay), false); err != nil {
		t.Fatal(err)
	}
	if got := readLines(t, dayPath(l, testDay)); len(got) != 1 {
		t.Errorf("%d lines", len(got))
	}
}

func TestAppendEncodeError(t *testing.T) {
	l := openLog(t, Options{})
	rec := callStart("aa", testDay)
	rec.Arguments = &Arguments{Supplied: json.RawMessage(`{`)}
	if err := l.Append(testDay, rec, false); err == nil {
		t.Fatal("no error")
	}
}

func TestAppendConcurrentLogs(t *testing.T) {
	base := t.TempDir()
	logs := []*Log{openLog(t, Options{Base: base}), openLog(t, Options{Base: base})}
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			l := logs[w%2]
			for i := range 50 {
				id := fmt.Sprintf("%02d%04d", w, i)
				if err := l.Append(testDay, callStart(id, testDay), false); err != nil {
					t.Errorf("append: %v", err)
				}
			}
		})
	}
	wg.Wait()
	f, err := os.Open(dayPath(logs[0], testDay))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("a line does not parse: %q", sc.Text())
		}
		if seen[rec.ID] {
			t.Fatalf("id %s twice", rec.ID)
		}
		seen[rec.ID] = true
	}
	if len(seen) != 400 {
		t.Errorf("%d records, want 400", len(seen))
	}
}

func TestLock(t *testing.T) {
	t.Run("a fresh lock makes a writer wait and fail", func(t *testing.T) {
		l := openLog(t, Options{})
		l.lockWait = 30 * time.Millisecond
		lock := filepath.Join(l.Dir(), lockName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		err := l.Append(testDay, callStart("aa", testDay), false)
		if err == nil || !strings.Contains(err.Error(), "another writer holds the lock") {
			t.Fatalf("got %v", err)
		}
		if _, err := os.Stat(lock); err != nil {
			t.Error("the other writer's lock was removed")
		}
	})
	t.Run("a stale lock is taken over", func(t *testing.T) {
		l := openLog(t, Options{})
		lock := filepath.Join(l.Dir(), lockName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Fatal(err)
		}
		if err := l.Append(testDay, callStart("aa", testDay), false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lock); err == nil {
			t.Error("the lock was left behind")
		}
	})
	t.Run("a lock that cannot be created", func(t *testing.T) {
		if _, err := lockDir(filepath.Join(t.TempDir(), "missing"), time.Millisecond); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("a permission error is a held lock on Windows only", func(t *testing.T) {
		tests := []struct {
			err  error
			goos string
			want bool
		}{
			{fs.ErrExist, "linux", true},
			{fs.ErrExist, "windows", true},
			{fs.ErrPermission, "windows", true},
			{fs.ErrPermission, "linux", false},
			{fs.ErrPermission, "darwin", false},
			{fs.ErrNotExist, "windows", false},
		}
		for _, tt := range tests {
			if got := lockHeld(tt.err, tt.goos); got != tt.want {
				t.Errorf("lockHeld(%v, %s) = %v, want %v", tt.err, tt.goos, got, tt.want)
			}
		}
	})
}

func TestPrivateDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	dir := filepath.Join(base, "p")
	if err := PrivateDir(base, dir, "; hint"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := PrivateDir(base, dir, "; hint"); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("mode not tightened: %v %v", info.Mode().Perm(), err)
		}
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(filepath.Join(file, "base"), filepath.Join(file, "base", "p"), "; hint"); err == nil || !strings.HasSuffix(err.Error(), "; hint") {
		t.Errorf("a base that cannot be created: %v", err)
	}
	if err := PrivateDir(filepath.Dir(file), file, "; hint"); err == nil {
		t.Error("a file is accepted as the directory")
	}
	missing := filepath.Join(t.TempDir(), "base")
	if err := PrivateDir(missing, filepath.Join(missing, "a", "b"), "; hint"); err == nil || !strings.HasSuffix(err.Error(), "; hint") {
		t.Errorf("a directory whose parent is missing: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var unusable *UnusableError
	if err := PrivateDir(base, link, "; hint"); !errors.As(err, &unusable) || unusable.Reason != "is a symbolic link" {
		t.Errorf("a link: %v", err)
	}
	if err := PrivateDir(link, filepath.Join(link, "p"), "; hint"); !errors.As(err, &unusable) {
		t.Errorf("a base that is a link: %v", err)
	}
}

func TestLockDir(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockDir(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockDir(dir, 20*time.Millisecond); !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "another writer holds the lock") {
		t.Errorf("a second writer: %v", err)
	}
	unlock()
	unlock, err = LockDir(dir, time.Second)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	unlock()
}

func TestStealStale(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	if stealStale(lock) {
		t.Error("stole a lock that does not exist")
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if stealStale(lock) {
		t.Error("stole a fresh lock")
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !stealStale(lock) {
		t.Error("left a stale lock")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d files left", len(entries))
	}
}

func TestPrune(t *testing.T) {
	write := func(t *testing.T, l *Log, name string) string {
		t.Helper()
		path := filepath.Join(l.Dir(), name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	t.Run("removes files past the period", func(t *testing.T) {
		base := t.TempDir()
		seed := openLog(t, Options{Base: base})
		old := write(t, seed, "2026-10-18.jsonl")  // 31 days before
		edge := write(t, seed, "2026-10-19.jsonl") // 30 days before
		recent := write(t, seed, "2026-11-17.jsonl")
		other := write(t, seed, "notes-2020-01-01.jsonl")
		almost := write(t, seed, "2020-01-01.jsonl.bak")
		openLog(t, Options{Base: base, RetentionDays: 30})
		if exists(old) {
			t.Error("a file 31 days old was kept")
		}
		for _, path := range []string{edge, recent, other, almost, filepath.Join(seed.Dir(), projectFile)} {
			if !exists(path) {
				t.Errorf("%s was removed", filepath.Base(path))
			}
		}
	})
	t.Run("zero keeps every file", func(t *testing.T) {
		base := t.TempDir()
		seed := openLog(t, Options{Base: base})
		old := write(t, seed, "2000-01-01.jsonl")
		openLog(t, Options{Base: base})
		if !exists(old) {
			t.Error("the file was removed")
		}
	})
	t.Run("a period longer than the calendar keeps every file", func(t *testing.T) {
		// Counted back as written, the period wraps to a day after today.
		base := t.TempDir()
		seed := openLog(t, Options{Base: base})
		old := write(t, seed, "2000-01-01.jsonl")
		today := write(t, seed, "2026-11-18.jsonl")
		openLog(t, Options{Base: base, RetentionDays: math.MaxInt})
		if !exists(old) || !exists(today) {
			t.Error("a file was removed")
		}
	})
	t.Run("the first append of a new day prunes", func(t *testing.T) {
		now := testDay
		l := openLog(t, Options{RetentionDays: 1, Now: func() time.Time { return now }})
		if err := l.Append(now, callStart("aa", now), false); err != nil {
			t.Fatal(err)
		}
		first := dayPath(l, now)
		now = now.AddDate(0, 0, 2)
		if err := l.Append(now, callStart("bb", now), false); err != nil {
			t.Fatal(err)
		}
		if exists(first) {
			t.Error("the old day was kept")
		}
		if !exists(dayPath(l, now)) {
			t.Error("the new day was removed")
		}
	})
	t.Run("a file that cannot be removed is a warning", func(t *testing.T) {
		base := t.TempDir()
		seed := openLog(t, Options{Base: base})
		stuck := filepath.Join(seed.Dir(), "2000-01-01.jsonl")
		if err := os.MkdirAll(filepath.Join(stuck, "x"), 0o700); err != nil {
			t.Fatal(err)
		}
		var warned []error
		l, err := Open(Options{
			Base: base, Runefile: "/repo/Runefile", RetentionDays: 1,
			Now: fixedClock(testDay), Warn: func(err error) { warned = append(warned, err) },
		})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		if len(warned) != 1 {
			t.Fatalf("%d warnings, want 1", len(warned))
		}
		if err := l.Append(testDay, callStart("aa", testDay), false); err != nil {
			t.Errorf("Append failed: %v", err)
		}
		if err := l.Prune(); err == nil {
			t.Error("Prune returned no error")
		}
	})
	t.Run("a missing directory", func(t *testing.T) {
		l := openLog(t, Options{RetentionDays: 1})
		if err := os.RemoveAll(l.Dir()); err != nil {
			t.Fatal(err)
		}
		if err := l.Prune(); err == nil {
			t.Error("no error")
		}
	})
}

func TestTorn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	check := func(content string, want bool) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if got := torn(f); got != want {
			t.Errorf("torn(%q) = %v", content, got)
		}
	}
	check("", false)
	check("a\n", false)
	check("a\nb", true)
}

// SC-008: two synced appends must stay well inside the 50 ms a call may cost.
func TestAppendIsFast(t *testing.T) {
	l := openLog(t, Options{})
	const n = 100
	begin := time.Now()
	for i := range n {
		if err := l.Append(testDay, callStart(fmt.Sprintf("%016d", i), testDay), true); err != nil {
			t.Fatal(err)
		}
	}
	avg := time.Since(begin) / n
	t.Logf("a synced append takes %s on average", avg)
	if avg > 20*time.Millisecond && !raceEnabled {
		t.Errorf("a synced append takes %s on average, want under 20ms", avg)
	}
}

func BenchmarkAppend(b *testing.B) {
	l, err := Open(Options{Base: b.TempDir(), Runefile: "/repo/Runefile"})
	if err != nil {
		b.Fatal(err)
	}
	rec := callStart("0123456789abcdef", testDay)
	for b.Loop() {
		if err := l.Append(testDay, rec, false); err != nil {
			b.Fatal(err)
		}
	}
}
