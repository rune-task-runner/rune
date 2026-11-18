package planstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func journalLines(t *testing.T, s *Store) []string {
	t.Helper()
	data := mustRead(t, filepath.Join(s.Dir(), "journal.jsonl"))
	if data == "" {
		return nil
	}
	if !strings.HasSuffix(data, "\n") {
		t.Fatalf("the journal does not end with a newline: %q", data)
	}
	return strings.Split(strings.TrimSuffix(data, "\n"), "\n")
}

func journalEvents(t *testing.T, s *Store) []Event {
	t.Helper()
	var events []Event
	for i, line := range journalLines(t, s) {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("journal line %d is not whole: %q: %v", i+1, line, err)
		}
		events = append(events, e)
	}
	return events
}

func TestLogAppendsOneLinePerEvent(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	exit := 0
	created := Event{
		Time:     time.Date(2026, 11, 17, 10, 4, 11, 0, time.UTC),
		Event:    EventPlanCreated,
		Surface:  "mcp",
		PlanTask: "plan",
		Plan:     "7f3c9a1e52b84d06",
		Digest:   "9b1f",
		Args:     `env="prod"`,
	}
	finished := Event{
		Time:      time.Date(2026, 11, 17, 10, 5, 31, 0, time.UTC),
		Event:     EventApplyFinished,
		Surface:   "mcp",
		PlanTask:  "plan",
		Plan:      "7f3c9a1e52b84d06",
		Digest:    "9b1f",
		Args:      `env="prod"`,
		ApplyTask: "apply",
		Attempt:   "c41d0e7a9b3355f2",
		Exit:      &exit,
		State:     "applied",
	}
	refused := Event{Event: EventApplyRefused, Surface: "cli", PlanTask: "plan", Args: "a<b & c", Reason: "no plan", ApplyTask: "apply"}
	replaced := Event{Event: EventPlanReplaced, Surface: "mcp", PlanTask: "plan", Plan: "00000000000000aa", ReplacedBy: "00000000000000bb", Tampered: true}
	for _, e := range []Event{created, finished, refused, replaced} {
		if err := s.Log(e); err != nil {
			t.Fatalf("Log(%s): %v", e.Event, err)
		}
	}

	want := []string{
		`{"time":"2026-11-17T10:04:11Z","event":"plan-created","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f","args":"env=\"prod\""}`,
		`{"time":"2026-11-17T10:05:31Z","event":"apply-finished","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f","args":"env=\"prod\"","applyTask":"apply","attempt":"c41d0e7a9b3355f2","exit":0,"state":"applied"}`,
		`{"time":"2026-11-17T10:00:00.123456789Z","event":"apply-refused","surface":"cli","planTask":"plan","args":"a<b & c","applyTask":"apply","reason":"no plan"}`,
		`{"time":"2026-11-17T10:00:00.123456789Z","event":"plan-replaced","surface":"mcp","planTask":"plan","plan":"00000000000000aa","args":"","replacedBy":"00000000000000bb","tampered":true}`,
	}
	got := journalLines(t, s)
	if len(got) != len(want) {
		t.Fatalf("the journal has %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i+1, got[i], want[i])
		}
	}
	events := journalEvents(t, s)
	if !events[2].Time.Equal(clock.Now()) {
		t.Errorf("an event without a time got %v, want the clock's %v", events[2].Time, clock.Now())
	}
	if events[1].Exit == nil || *events[1].Exit != 0 {
		t.Errorf("exit 0 was not written: %+v", events[1])
	}
	if events[0].Exit != nil {
		t.Errorf("an event without an exit got one: %+v", events[0])
	}
	if exists(filepath.Join(s.Dir(), "journal.lock")) {
		t.Error("Log left its lock file")
	}
}

func TestEventNames(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		EventPlanCreated:       "plan-created",
		EventPlanReplaced:      "plan-replaced",
		EventApprovalRequested: "approval-requested",
		EventApprovalGiven:     "approval-given",
		EventApprovalNotGiven:  "approval-not-given",
		EventApplyRefused:      "apply-refused",
		EventApplyStarted:      "apply-started",
		EventApplyFinished:     "apply-finished",
	}
	if len(want) != 8 {
		t.Fatalf("two event names are equal: %v", want)
	}
	for got, name := range want {
		if got != name {
			t.Errorf("event name = %q, want %q", got, name)
		}
	}
}

func TestLogCreatesTheStore(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	if err := s.Log(Event{Event: EventApplyRefused, Reason: "no plan"}); err != nil {
		t.Fatalf("Log on a missing store: %v", err)
	}
	if got := mustRead(t, filepath.Join(s.Dir(), ".gitignore")); got != "*\n" {
		t.Errorf(".gitignore = %q", got)
	}
	if events := journalEvents(t, s); len(events) != 1 || events[0].Reason != "no plan" {
		t.Errorf("journal = %+v", events)
	}
}

func TestLogMode(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	s, _ := newStore(t)
	if err := s.Log(Event{Event: EventPlanCreated}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(s.Dir(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the journal has mode %o, want 600", info.Mode().Perm())
	}
}

func TestLogKeepsTheNewerHalf(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	s.journalMax = 2000
	const n = 200
	for i := range n {
		if err := s.Log(Event{Event: EventApplyRefused, Reason: fmt.Sprintf("event %03d", i)}); err != nil {
			t.Fatalf("Log %d: %v", i, err)
		}
		info, err := os.Stat(filepath.Join(s.Dir(), "journal.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > s.journalMax {
			t.Fatalf("after event %d the journal has %d bytes, over the limit %d", i, info.Size(), s.journalMax)
		}
	}
	events := journalEvents(t, s)
	if len(events) < 2 || len(events) >= n {
		t.Fatalf("the journal kept %d of %d events", len(events), n)
	}
	first := n - len(events)
	for i, e := range events {
		if want := fmt.Sprintf("event %03d", first+i); e.Reason != want {
			t.Fatalf("line %d holds %q, want %q: the kept lines must be the newest, in order", i+1, e.Reason, want)
		}
	}
	if exists(filepath.Join(s.Dir(), "journal.jsonl.tmp")) {
		t.Error("the cut left its temporary file")
	}
}

func TestNewerHalf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"cut on a line boundary", "a\nb\nc\nd\n", "c\nd\n"},
		{"cut inside a line", "aaa\nbbb\ncc\n", "cc\n"},
		{"one line", "abcdef\n", "abcdef\n"},
		{"last line longer than half", "a\nbbbbbbbb\n", "bbbbbbbb\n"},
		{"no final newline", "abc", "abc"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		if got := string(newerHalf([]byte(tt.in))); got != tt.want {
			t.Errorf("%s: newerHalf(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestLogFromTwoStores(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)}
	stores := []*Store{Open(root), Open(root)}
	for _, s := range stores {
		s.Now = clock.Now
	}

	const workers, each = 8, 50
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			s := stores[w%len(stores)]
			for i := range each {
				if err := s.Log(Event{Event: EventApplyStarted, Reason: fmt.Sprintf("worker %d event %d", w, i)}); err != nil {
					errs[w] = err
					return
				}
			}
		})
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}

	events := journalEvents(t, stores[0])
	if len(events) != workers*each {
		t.Fatalf("the journal has %d lines, want %d", len(events), workers*each)
	}
	seen := map[string]bool{}
	next := make([]int, workers)
	for _, e := range events {
		var w, i int
		if _, err := fmt.Sscanf(e.Reason, "worker %d event %d", &w, &i); err != nil {
			t.Fatalf("unexpected line %+v", e)
		}
		if i != next[w] {
			t.Errorf("worker %d: event %d written where %d was due", w, i, next[w])
		}
		next[w] = i + 1
		seen[e.Reason] = true
	}
	if len(seen) != workers*each {
		t.Errorf("%d distinct events, want %d", len(seen), workers*each)
	}
	if exists(filepath.Join(stores[0].Dir(), "journal.lock")) {
		t.Error("a lock file was left behind")
	}
}

func TestLogFromTwoStoresWhileTheJournalIsCut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stores := []*Store{Open(root), Open(root)}
	for _, s := range stores {
		s.journalMax = 4000
	}
	const workers, each = 8, 50
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			s := stores[w%len(stores)]
			for i := range each {
				if err := s.Log(Event{Event: EventApplyStarted, Reason: fmt.Sprintf("worker %d event %d", w, i)}); err != nil {
					errs[w] = err
					return
				}
			}
		})
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}
	events := journalEvents(t, stores[0])
	if len(events) == 0 || len(events) >= workers*each {
		t.Fatalf("the journal kept %d lines", len(events))
	}
	next := map[int]int{}
	for _, e := range events {
		var w, i int
		if _, err := fmt.Sscanf(e.Reason, "worker %d event %d", &w, &i); err != nil {
			t.Fatalf("unexpected line %+v", e)
		}
		if last, ok := next[w]; ok && i != last {
			t.Errorf("worker %d: event %d kept where %d was due: a line was lost", w, i, last)
		}
		next[w] = i + 1
	}
}

func TestLogRemovesAStaleLock(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	s.lockWait = 50 * time.Millisecond
	if err := s.Log(Event{Event: EventPlanCreated}); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.Dir(), "journal.lock")
	put(t, lock, "")
	old := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Log(Event{Event: EventApplyStarted}); err != nil {
		t.Fatalf("Log with a stale lock: %v", err)
	}
	if exists(lock) {
		t.Error("the lock file is still there")
	}
	if events := journalEvents(t, s); len(events) != 2 || events[1].Event != EventApplyStarted {
		t.Errorf("journal = %+v", events)
	}
}

func TestLogGivesUpOnAHeldLock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		age  time.Duration
	}{
		{"fresh", 0},
		{"five seconds old", 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newStore(t)
			s.lockWait = 40 * time.Millisecond
			if err := s.Log(Event{Event: EventPlanCreated}); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(s.Dir(), "journal.lock")
			put(t, lock, "")
			held := time.Now().Add(-tt.age)
			if err := os.Chtimes(lock, held, held); err != nil {
				t.Fatal(err)
			}

			start := time.Now()
			err := s.Log(Event{Event: EventApplyStarted})
			if err == nil {
				t.Fatal("Log wrote while another process held the lock")
			}
			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("Log error = %v, want one wrapping fs.ErrExist", err)
			}
			if waited := time.Since(start); waited < s.lockWait {
				t.Errorf("Log gave up after %v, before its wait of %v", waited, s.lockWait)
			}
			if !exists(lock) {
				t.Error("Log removed a lock it did not hold")
			}
			if events := journalEvents(t, s); len(events) != 1 {
				t.Errorf("journal = %+v, want only the first event", events)
			}
		})
	}
}

func TestLockJournal(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	s.lockWait = 20 * time.Millisecond
	dir := t.TempDir()
	unlock, err := s.lockJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, "journal.lock")
	if !exists(lock) {
		t.Fatal("no lock file")
	}
	if _, err := s.lockJournal(dir); err == nil {
		t.Error("the lock was taken twice")
	}
	unlock()
	if exists(lock) {
		t.Error("unlock left the lock file")
	}
	unlock, err = s.lockJournal(dir)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	unlock()

	if _, err := s.lockJournal(filepath.Join(dir, "absent")); err == nil || errors.Is(err, fs.ErrExist) {
		t.Errorf("lock in a missing directory: %v", err)
	}
}

func TestStaleLock(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "journal.lock")
	if staleLock(lock) {
		t.Error("a missing lock counts as stale")
	}
	put(t, lock, "")
	if staleLock(lock) {
		t.Error("a fresh lock counts as stale")
	}
	old := time.Now().Add(-lockStale - time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !staleLock(lock) {
		t.Error("a lock older than ten seconds does not count as stale")
	}
}

func TestLogRefusesAJournalThatIsNotAFile(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	savePlan(t, s, "k1")
	journal := filepath.Join(s.Dir(), "journal.jsonl")
	if err := os.Mkdir(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Log(Event{Event: EventPlanCreated}); err == nil {
		t.Error("Log wrote to a directory")
	}
	if exists(filepath.Join(s.Dir(), "journal.lock")) {
		t.Error("a failed Log left its lock file")
	}
}

func TestLogRefusesAJournalThatIsALink(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	s, _ := newStore(t)
	savePlan(t, s, "k1")
	target := source(t, "untouched\n")
	if err := os.Symlink(target, filepath.Join(s.Dir(), "journal.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := s.Log(Event{Event: EventPlanCreated}); err == nil {
		t.Error("Log wrote through a symbolic link")
	}
	if got := mustRead(t, target); got != "untouched\n" {
		t.Errorf("the link's target changed: %q", got)
	}
}

func TestLogReportsAnEventItCannotEncode(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	err := s.Log(Event{Event: EventPlanCreated, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err == nil {
		t.Fatal("Log accepted a time JSON cannot hold")
	}
	if exists(filepath.Join(s.Dir(), "journal.jsonl")) {
		t.Error("a failed Log created the journal")
	}
}

func TestTrimJournal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	if err := trimJournal(path, 10); err == nil {
		t.Error("trimJournal of a missing file returned no error")
	}
	put(t, path, "one\ntwo\nthree\n")
	if err := trimJournal(path, 14); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "one\ntwo\nthree\n" {
		t.Errorf("a journal at the limit was cut: %q", got)
	}
	if err := trimJournal(path, 13); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "three\n" {
		t.Errorf("journal = %q, want the newer half", got)
	}
}

func TestAppendLine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	for _, line := range []string{"one\n", "two\n"} {
		if err := appendLine(path, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustRead(t, path); got != "one\ntwo\n" {
		t.Errorf("journal = %q", got)
	}
	if err := appendLine(filepath.Join(t.TempDir(), "absent", "journal.jsonl"), []byte("x\n")); err == nil {
		t.Error("appendLine in a missing directory returned no error")
	}
}

// A lock is stolen by moving it aside, so that of the waiters that saw it
// stale one takes it, and a fresh lock is never the one removed.
func TestStealStale(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	if stealStale(lock) {
		t.Error("a lock that is not there was stolen")
	}

	write := func(age time.Duration) {
		t.Helper()
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(lock, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write(0)
	if stealStale(lock) {
		t.Error("a fresh lock was stolen")
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Errorf("the fresh lock is gone: %v", err)
	}

	write(2 * lockStale)
	if !stealStale(lock) {
		t.Error("a stale lock was not stolen")
	}
	if _, err := os.Lstat(lock); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the stale lock is still there: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("stealing left files behind: %v, %v", entries, err)
	}
}

func TestStealStaleOnce(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, lockName)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(lock, when, when); err != nil {
		t.Fatal(err)
	}
	const waiters = 16
	var stolen atomic.Int32
	var wg sync.WaitGroup
	for range waiters {
		wg.Go(func() {
			if stealStale(lock) {
				stolen.Add(1)
			}
		})
	}
	wg.Wait()
	if got := stolen.Load(); got != 1 {
		t.Errorf("%d waiters stole one stale lock, want 1", got)
	}
}
