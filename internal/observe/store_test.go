package observe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := OpenStore(filepath.Join(t.TempDir(), "observe"), testRoot(t))
	s.now = func() time.Time { return testNow }
	return s
}

func testRecord(id string, at time.Time) Record {
	return Record{V: 1, Time: at.UTC().Truncate(time.Second), Client: ClientClaude, Session: "s1", ToolUse: id, Event: KindSuccess, Command: "go test ./..."}
}

func mustAppend(t *testing.T, s *Store, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestStoreAppendAndRead(t *testing.T) {
	s := newStore(t)
	if recs, stats, err := s.Read(); err != nil || recs != nil || stats.Damaged != 0 {
		t.Fatalf("an empty store: %v %v %v", recs, stats, err)
	}
	mustAppend(t, s, testRecord("t1", testNow), testRecord("t2", testNow))
	recs, stats, err := s.Read()
	if err != nil || len(recs) != 2 || recs[0].ToolUse != "t1" || recs[1].ToolUse != "t2" || stats.Damaged != 0 {
		t.Fatalf("got %+v %+v %v", recs, stats, err)
	}
	if !strings.HasPrefix(s.Dir(), s.base) {
		t.Errorf("dir %s is not under %s", s.Dir(), s.base)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{s.Dir(): 0o700, s.events(): 0o600} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
				t.Errorf("%s: mode %v, want %o", path, info.Mode().Perm(), want)
			}
		}
	}
}

func TestStoreDamagedLines(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, testRecord("t1", testNow))
	f, err := os.OpenFile(s.events(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"not json", `{"v":2,"toolUse":"x","event":"pre","command":"c"}`, `{"v":1,"toolUse":"x","event":"later","command":"c"}`, `{"v":1,"event":"pre","command":"c"}`, "", "  "} {
		fmt.Fprintln(f, line)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, testRecord("t2", testNow))
	recs, stats, err := s.Read()
	if err != nil || len(recs) != 2 || stats.Damaged != 4 {
		t.Errorf("got %d records, %d damaged, %v", len(recs), stats.Damaged, err)
	}
}

// A writer that died in the middle of a record leaves a line with no line
// feed; the next record still gets a line of its own.
func TestStoreAppendAfterTornLine(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, testRecord("t1", testNow))
	f, err := os.OpenFile(s.events(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"time":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, testRecord("t2", testNow))
	recs, stats, err := s.Read()
	if err != nil || len(recs) != 2 || recs[1].ToolUse != "t2" || stats.Damaged != 1 {
		t.Errorf("got %+v, %d damaged, %v", recs, stats.Damaged, err)
	}
}

// Read takes no lock, so a last line with no line feed may be a record a
// writer is still adding: it is not counted as damaged.
func TestStoreReadUnfinishedLine(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, testRecord("t1", testNow))
	f, err := os.OpenFile(s.events(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"time":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	recs, stats, err := s.Read()
	if err != nil || len(recs) != 1 || stats.Damaged != 0 {
		t.Errorf("got %d records, %d damaged, %v", len(recs), stats.Damaged, err)
	}
}

func TestStoreRetention(t *testing.T) {
	s := newStore(t)
	old := testRecord("old", testNow.Add(-Retention-time.Hour))
	mustAppend(t, s, old)
	if recs, _, _ := s.Read(); len(recs) != 0 {
		t.Errorf("Read shows a record past retention: %+v", recs)
	}
	// The next append finds the first record past retention and compacts.
	mustAppend(t, s, testRecord("new", testNow))
	data, err := os.ReadFile(s.events())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"old"`) || !strings.Contains(string(data), `"new"`) {
		t.Errorf("after compaction: %s", data)
	}
	// Prune does the same for a store that only has old records.
	s2 := newStore(t)
	mustAppend(t, s2, old)
	if err := s2.Prune(); err != nil {
		t.Fatal(err)
	}
	if fileSize(t, s2.events()) != 0 {
		t.Error("Prune left a record past retention")
	}
	// Prune of a store that holds nothing creates nothing.
	s3 := newStore(t)
	if err := s3.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s3.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Prune created %s", s3.Dir())
	}
}

func TestStoreSizeBound(t *testing.T) {
	s := newStore(t)
	big := testRecord("", testNow)
	big.Command = strings.Repeat("a", MaxCommandBytes)
	var largest int64
	n := 0
	// About three times the bound, so the store compacts several times.
	for ; n < 3000; n++ {
		big.ToolUse = fmt.Sprintf("t%d", n)
		mustAppend(t, s, big)
		largest = max(largest, fileSize(t, s.events()))
	}
	if largest > MaxStoreBytes {
		t.Errorf("the file reached %d bytes", largest)
	}
	recs, _, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if last := recs[len(recs)-1].ToolUse; last != fmt.Sprintf("t%d", n-1) {
		t.Errorf("the newest record is %s", last)
	}
	if recs[0].ToolUse == "t0" {
		t.Error("the oldest record was kept")
	}
}

// SC-008: however many events arrive, one project's records stay within
// the bound. TestStoreSizeBound proves the bound by volume in every run;
// this one counts 100,000 events, which takes too long under the race
// detector.
func TestStoreManyEvents(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("appends 100,000 records")
	}
	s := newStore(t)
	rec := testRecord("", testNow)
	rec.Command = strings.Repeat("c", 200)
	var largest int64
	for i := range 100_000 {
		rec.ToolUse = fmt.Sprintf("t%d", i)
		mustAppend(t, s, rec)
		if i%500 == 0 {
			largest = max(largest, fileSize(t, s.events()))
		}
	}
	if size := fileSize(t, s.events()); size > MaxStoreBytes || largest > MaxStoreBytes {
		t.Errorf("the file holds %d bytes, reached %d", size, largest)
	}
}

func TestStoreForget(t *testing.T) {
	s := newStore(t)
	if n, err := s.Forget(); n != 0 || err != nil {
		t.Errorf("an empty store: %d %v", n, err)
	}
	mustAppend(t, s, testRecord("t1", testNow), testRecord("t2", testNow.Add(-Retention-time.Hour)))
	if err := s.CountDrop(ReasonTooLong); err != nil {
		t.Fatal(err)
	}
	n, err := s.Forget()
	if n != 2 || err != nil {
		t.Errorf("Forget = %d, %v", n, err)
	}
	if _, err := os.Stat(s.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the directory is still there: %v", err)
	}
}

func TestStoreBusy(t *testing.T) {
	s := newStore(t)
	s.lockWait = 20 * time.Millisecond
	mustAppend(t, s, testRecord("t1", testNow))
	if err := os.WriteFile(filepath.Join(s.Dir(), ".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(testRecord("t2", testNow)); !errors.Is(err, ErrBusy) {
		t.Errorf("a held lock: %v", err)
	}
}

func TestStoreInsideProject(t *testing.T) {
	root := testRoot(t)
	s := OpenStore(filepath.Join(root, ".state", "observe"), root)
	if err := s.Append(testRecord("t1", testNow)); !errors.Is(err, ErrInsideProject) {
		t.Errorf("a store inside the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".state")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the refused store was created")
	}
}

func TestStoreErrors(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, testRecord("t1", testNow))
	// A directory where the file should be.
	if err := os.Remove(s.events()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.events(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(testRecord("t2", testNow)); err == nil {
		t.Error("appended to a directory")
	}
	if _, _, err := s.Read(); err == nil {
		t.Error("read a directory")
	}
	// A record that does not encode is not written.
	long := testRecord("t3", testNow)
	long.Command = strings.Repeat(`"`, MaxCommandBytes)
	if err := newStore(t).Append(long); dropReason(err) != ReasonTooLong {
		t.Errorf("a record over the limit: %v", err)
	}
	// A base that cannot be created.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := OpenStore(filepath.Join(file, "observe"), testRoot(t))
	if err := bad.Append(testRecord("t4", testNow)); err == nil || !strings.Contains(err.Error(), "XDG_STATE_HOME") {
		t.Errorf("an unwritable base: %v", err)
	}
}

// Writers of several processes take the lock in turn; goroutines with
// stores of their own do the same here.
func TestStoreConcurrentAppends(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			own := OpenStore(s.base, s.root)
			own.lockWait = 30 * time.Second
			for i := range 200 {
				if err := own.Append(testRecord(fmt.Sprintf("w%d-%d", w, i), testNow)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	recs, stats, err := s.Read()
	if err != nil || stats.Damaged != 0 {
		t.Fatalf("%v, %d damaged", err, stats.Damaged)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.ToolUse] = true
	}
	if len(recs) != 1600 || len(seen) != 1600 {
		t.Errorf("%d records, %d distinct, want 1600", len(recs), len(seen))
	}
}

func TestFirstExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	cutoff := testNow.Add(-Retention)
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	line := func(r Record) string {
		b, err := r.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write(line(testRecord("a", testNow)))
	if firstExpired(path, cutoff) {
		t.Error("a fresh first record")
	}
	write(line(testRecord("a", cutoff.Add(-time.Hour))))
	if !firstExpired(path, cutoff) {
		t.Error("an old first record")
	}
	write("garbage\n")
	if !firstExpired(path, cutoff) {
		t.Error("a damaged first line")
	}
	write(strings.Repeat("x", MaxRecordBytes+10) + "\n")
	if !firstExpired(path, cutoff) {
		t.Error("an overlong first line")
	}
	if firstExpired(filepath.Join(dir, "missing"), cutoff) {
		t.Error("a missing file")
	}
}

func TestBase(t *testing.T) {
	if got := Base(filepath.FromSlash("/s/rune")); got != filepath.FromSlash("/s/rune/observe") {
		t.Errorf("Base = %q", got)
	}
}

// The store's errors: a held lock, and files and directories in the way.
func TestStoreFailures(t *testing.T) {
	busy := func(t *testing.T) *Store {
		t.Helper()
		s := newStore(t)
		s.lockWait = 10 * time.Millisecond
		mustAppend(t, s, testRecord("t1", testNow))
		if err := os.WriteFile(filepath.Join(s.Dir(), ".lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("busy", func(t *testing.T) {
		s := busy(t)
		if _, err := s.Forget(); !errors.Is(err, ErrBusy) {
			t.Errorf("Forget: %v", err)
		}
		if err := s.Prune(); !errors.Is(err, ErrBusy) {
			t.Errorf("Prune: %v", err)
		}
		if err := s.CountDrop(ReasonTooLong); !errors.Is(err, ErrBusy) {
			t.Errorf("CountDrop: %v", err)
		}
	})
	t.Run("the records are a directory", func(t *testing.T) {
		s := newStore(t)
		if err := os.MkdirAll(s.events(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.events(), "x"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Forget(); err == nil {
			t.Error("Forget of a directory")
		}
		if err := s.Prune(); err == nil {
			t.Error("Prune of a directory")
		}
	})
	t.Run("the store's directory is a file", func(t *testing.T) {
		s := newStore(t)
		if err := os.MkdirAll(filepath.Dir(s.Dir()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.Dir(), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// Windows reads a path under a file as missing, so there is
		// nothing to prune there; elsewhere it is an error.
		if err := s.Prune(); (err == nil) != (runtime.GOOS == "windows") {
			t.Errorf("Prune under a file: %v", err)
		}
		if _, err := s.Forget(); err == nil {
			t.Error("Forget of a file")
		}
	})
	t.Run("the shared counts are held", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "observe")
		if err := CountShared(base, ReasonNoProject, testNow); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, ".lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := CountShared(base, ReasonNoProject, testNow); err == nil {
			t.Error("counted through a held lock")
		}
	})
}

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	if err := replaceFile(filepath.Join(dir, "missing"), filepath.Join(dir, "missing", "f"), []byte("x")); err == nil {
		t.Error("a temporary file in a missing directory")
	}
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(dir, target, []byte("x")); err == nil {
		t.Error("renamed over a directory that is not empty")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the temporary file was left: %d entries", len(entries))
	}
}

func TestTorn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	for content, want := range map[string]bool{"": false, "a\n": false, "a": true} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := torn(f); got != want {
			t.Errorf("torn(%q) = %v", content, got)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		// A file that cannot be read is not judged torn.
		if torn(f) {
			t.Errorf("a closed file %q is torn", content)
		}
	}
}
