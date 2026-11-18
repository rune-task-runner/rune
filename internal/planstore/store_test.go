package planstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 10, 0, 0, 123456789, time.UTC)}
	s := Open(t.TempDir())
	s.Now = clock.Now
	return s, clock
}

func posixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links and file modes are POSIX behavior")
	}
}

func source(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	planBytes    = "PLAN-ARTIFACT-\x00\x01\x02-create 3 destroy 1 QQQQ"
	summaryBytes = "SUMMARY-OF-PLAN: three to add, one to destroy\n"
)

func saveInput(t *testing.T, key, artifact, summary string) SaveInput {
	t.Helper()
	return SaveInput{
		Record: Record{
			Key:        key,
			PlanTask:   "plan",
			ApplyTasks: []string{"apply"},
			Args:       `env="prod"`,
			Binding:    Binding{WorkDir: "/work", Runefile: "rf", Config: "cf", Host: "linux/amd64"},
			Surface:    "mcp",
		},
		Expiry:       time.Hour,
		ArtifactPath: source(t, artifact),
		SummaryPath:  source(t, summary),
	}
}

func savePlan(t *testing.T, s *Store, key string) Record {
	t.Helper()
	rec, _, err := s.Save(saveInput(t, key, planBytes, summaryBytes))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return rec
}

func digestOf(content string) Digest {
	sum := sha256.Sum256([]byte(content))
	return Digest{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}
}

func recDir(s *Store, rec Record) string { return filepath.Join(s.Dir(), rec.ID) }

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func put(t *testing.T, path, content string) {
	t.Helper()
	_ = os.RemoveAll(path)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// sharesBytes reports whether any n-byte window of needle occurs in hay.
func sharesBytes(hay, needle []byte, n int) bool {
	for i := 0; i+n <= len(needle); i++ {
		if bytes.Contains(hay, needle[i:i+n]) {
			return true
		}
	}
	return false
}

func attemptDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "attempt-") {
			names = append(names, e.Name())
		}
	}
	return names
}

func recordDirs(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestOpenTouchesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := Open(root)
	if got, want := s.Dir(), filepath.Join(root, ".rune", "plans"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("Open created %v (err %v)", entries, err)
	}
}

func TestNowDefaultsToTheWallClock(t *testing.T) {
	t.Parallel()
	s := Open(t.TempDir())
	before := time.Now()
	got := s.now()
	if got.Before(before.Add(-time.Second)) || got.After(time.Now().Add(time.Second)) {
		t.Errorf("now() = %v, not the wall clock", got)
	}
	if got.Location() != time.UTC {
		t.Errorf("now() location = %v, want UTC", got.Location())
	}
}

func TestStateString(t *testing.T) {
	t.Parallel()
	want := map[State]string{
		StateReady:      "ready",
		StateUnfinished: "unfinished",
		StateApplied:    "applied",
		StateFailed:     "failed",
		StateReplaced:   "replaced",
		StateExpired:    "expired",
		State(99):       "unknown",
	}
	for state, text := range want {
		if got := state.String(); got != text {
			t.Errorf("State(%d).String() = %q, want %q", int(state), got, text)
		}
	}
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{&ArtifactError{What: "artifact", Detail: "is missing"}, "plan artifact is missing"},
		{&ArtifactError{What: "summary", Detail: "does not have the recorded digest"}, "plan summary does not have the recorded digest"},
		{&SourceError{Path: "out/plan.bin", Detail: "is larger than 256 MiB"}, "out/plan.bin is larger than 256 MiB"},
		{&UnusableError{Path: "/p/.rune/plans", Reason: "is a symbolic link"}, "plan store /p/.rune/plans is a symbolic link"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestFormatLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n    int64
		want string
	}{
		{MaxArtifactBytes, "256 MiB"},
		{MaxSummaryBytes, "1 MiB"},
		{3 << 20, "3 MiB"},
		{4, "4 bytes"},
		{1<<20 + 1, "1048577 bytes"},
	}
	for _, tt := range tests {
		if got := formatLimit(tt.n); got != tt.want {
			t.Errorf("formatLimit(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestValidID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		want bool
	}{
		{"7f3c9a1e52b84d06", true},
		{"0000000000000000", true},
		{"7f3c9a1e52b84d0", false},
		{"7f3c9a1e52b84d066", false},
		{"7F3C9A1E52B84D06", false},
		{"../../../../etc/", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := validID(tt.id); got != tt.want {
			t.Errorf("validID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
	for range 20 {
		if id := newID(); !validID(id) {
			t.Fatalf("newID() = %q, not 16 hex characters", id)
		}
	}
	if a, b := newID(), newID(); a == b {
		t.Errorf("newID() returned %s twice", a)
	}
}

func TestCompareRecords(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		a, b Record
		want int
	}{
		{"earlier created", Record{ID: "b", Created: t0}, Record{ID: "a", Created: t0.Add(time.Nanosecond)}, -1},
		{"later created", Record{ID: "a", Created: t0.Add(time.Nanosecond)}, Record{ID: "b", Created: t0}, 1},
		{"equal created, smaller id", Record{ID: "a", Created: t0}, Record{ID: "b", Created: t0}, -1},
		{"equal created, greater id", Record{ID: "b", Created: t0}, Record{ID: "a", Created: t0}, 1},
		{"same", Record{ID: "a", Created: t0}, Record{ID: "a", Created: t0}, 0},
	}
	for _, tt := range tests {
		if got := compareRecords(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: compareRecords = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestSaveStoresTheBytes(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	in := saveInput(t, "k1", planBytes, summaryBytes)
	in.Record.RuneVersion = "0.9.0"
	dirty := true
	in.Record.Git = Git{Commit: "abc123", Dirty: &dirty}
	in.Record.Binding.Tool = "terraform"
	in.Record.Binding.ToolPath = "/usr/bin/terraform"
	in.Record.Binding.ToolDigest = "feed"

	rec, replaced, err := s.Save(in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(replaced) != 0 {
		t.Errorf("replaced = %v, want none", replaced)
	}
	if !validID(rec.ID) {
		t.Errorf("ID = %q, want 16 hex characters", rec.ID)
	}
	if !rec.Created.Equal(clock.Now()) || rec.Created.Nanosecond() != 123456789 || rec.Created.Location() != time.UTC {
		t.Errorf("Created = %v, want the clock's time in UTC with nanoseconds", rec.Created)
	}
	if !rec.Expires.Equal(clock.Now().Add(time.Hour)) {
		t.Errorf("Expires = %v, want one hour after Created", rec.Expires)
	}
	if rec.Artifact != digestOf(planBytes) {
		t.Errorf("Artifact = %+v, want %+v", rec.Artifact, digestOf(planBytes))
	}
	if rec.Summary != digestOf(summaryBytes) {
		t.Errorf("Summary = %+v, want %+v", rec.Summary, digestOf(summaryBytes))
	}
	if rec.Key != "k1" || rec.PlanTask != "plan" || rec.Args != `env="prod"` || rec.Surface != "mcp" ||
		rec.RuneVersion != "0.9.0" || rec.Binding != in.Record.Binding || rec.Git.Commit != "abc123" ||
		rec.Git.Dirty == nil || !*rec.Git.Dirty || !slices.Equal(rec.ApplyTasks, []string{"apply"}) {
		t.Errorf("Save changed the caller's fields: %+v", rec)
	}

	dir := recDir(s, rec)
	if got := mustRead(t, filepath.Join(dir, "artifact")); got != planBytes {
		t.Errorf("stored artifact = %q, want the source bytes", got)
	}
	if got := mustRead(t, filepath.Join(dir, "summary")); got != summaryBytes {
		t.Errorf("stored summary = %q, want the source bytes", got)
	}
	if got := mustRead(t, in.ArtifactPath); got != planBytes {
		t.Errorf("Save changed the source: %q", got)
	}

	raw := []byte(mustRead(t, filepath.Join(dir, "record.json")))
	if sharesBytes(raw, []byte(planBytes), 8) || sharesBytes(raw, []byte(summaryBytes), 8) {
		t.Errorf("record.json holds plan or summary bytes: %s", raw)
	}
	if exists(filepath.Join(dir, "record.json.tmp")) {
		t.Error("the temporary record file was left behind")
	}
	if got := mustRead(t, filepath.Join(s.Dir(), ".gitignore")); got != "*\n" {
		t.Errorf(".gitignore = %q, want %q", got, "*\n")
	}

	got, state, ok, err := s.Latest("k1")
	if err != nil || !ok || state != StateReady {
		t.Fatalf("Latest = state %v, ok %v, err %v; want ready", state, ok, err)
	}
	wantJSON, _ := json.Marshal(rec)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("Latest record = %s, want %s", gotJSON, wantJSON)
	}
	if !got.Created.Equal(rec.Created) {
		t.Errorf("Latest lost precision: %v, want %v", got.Created, rec.Created)
	}
}

func TestSaveModes(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	if err := s.Log(Event{Event: EventPlanCreated, Plan: rec.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(rec, "apply", "mcp"); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(s.Dir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		switch {
		case d.IsDir() && perm != 0o700:
			t.Errorf("directory %s has mode %o, want 700", path, perm)
		case !d.IsDir() && perm&0o077 != 0:
			t.Errorf("file %s has mode %o, readable by group or others", path, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]fs.FileMode{
		"record.json": 0o600,
		"claim":       0o600,
		"artifact":    0o400,
		"summary":     0o400,
	}
	for name, mode := range want {
		info, err := os.Lstat(filepath.Join(recDir(s, rec), name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s has mode %o, want %o", name, info.Mode().Perm(), mode)
		}
	}
}

func TestSaveRefusesSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		posix  bool
		change func(t *testing.T, in *SaveInput) string
		detail string
	}{
		{
			name: "missing artifact",
			change: func(t *testing.T, in *SaveInput) string {
				in.ArtifactPath = filepath.Join(t.TempDir(), "absent")
				return in.ArtifactPath
			},
			detail: "is missing",
		},
		{
			name: "missing summary",
			change: func(t *testing.T, in *SaveInput) string {
				in.SummaryPath = filepath.Join(t.TempDir(), "absent")
				return in.SummaryPath
			},
			detail: "is missing",
		},
		{
			name: "artifact is a directory",
			change: func(t *testing.T, in *SaveInput) string {
				in.ArtifactPath = t.TempDir()
				return in.ArtifactPath
			},
			detail: "is not a regular file",
		},
		{
			name: "summary is a directory",
			change: func(t *testing.T, in *SaveInput) string {
				in.SummaryPath = t.TempDir()
				return in.SummaryPath
			},
			detail: "is not a regular file",
		},
		{
			name:  "artifact is a symbolic link",
			posix: true,
			change: func(t *testing.T, in *SaveInput) string {
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(in.ArtifactPath, link); err != nil {
					t.Fatal(err)
				}
				in.ArtifactPath = link
				return link
			},
			detail: "is a symbolic link",
		},
		{
			name:  "summary is a symbolic link",
			posix: true,
			change: func(t *testing.T, in *SaveInput) string {
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(in.SummaryPath, link); err != nil {
					t.Fatal(err)
				}
				in.SummaryPath = link
				return link
			},
			detail: "is a symbolic link",
		},
		{
			name: "artifact over the limit",
			change: func(_ *testing.T, in *SaveInput) string {
				in.MaxArtifact = int64(len(planBytes)) - 1
				return in.ArtifactPath
			},
			detail: fmt.Sprintf("is larger than %d bytes", len(planBytes)-1),
		},
		{
			name: "summary over the limit",
			change: func(_ *testing.T, in *SaveInput) string {
				in.MaxSummary = 4
				return in.SummaryPath
			},
			detail: "is larger than 4 bytes",
		},
		{
			name: "artifact over the default limit",
			change: func(t *testing.T, in *SaveInput) string {
				f, err := os.Create(in.ArtifactPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = f.Close() }()
				if err := f.Truncate(MaxArtifactBytes + 1); err != nil {
					t.Skipf("cannot make a sparse file: %v", err)
				}
				return in.ArtifactPath
			},
			detail: "is larger than 256 MiB",
		},
		{
			name: "summary over the default limit",
			change: func(t *testing.T, in *SaveInput) string {
				if err := os.Truncate(in.SummaryPath, MaxSummaryBytes+1); err != nil {
					t.Skipf("cannot make a sparse file: %v", err)
				}
				return in.SummaryPath
			},
			detail: "is larger than 1 MiB",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.posix {
				posixOnly(t)
			}
			s, _ := newStore(t)
			in := saveInput(t, "k1", planBytes, summaryBytes)
			path := tt.change(t, &in)

			_, replaced, err := s.Save(in)
			var se *SourceError
			if !errors.As(err, &se) {
				t.Fatalf("Save error = %v, want a *SourceError", err)
			}
			if se.Path != path || se.Detail != tt.detail {
				t.Errorf("SourceError = %+v, want path %q, detail %q", se, path, tt.detail)
			}
			if len(replaced) != 0 {
				t.Errorf("replaced = %v, want none", replaced)
			}
			if dirs := recordDirs(t, s); len(dirs) != 0 {
				t.Errorf("a refused Save left %v behind", dirs)
			}
			if _, _, ok, err := s.Latest("k1"); ok || err != nil {
				t.Errorf("Latest after a refused Save = ok %v, err %v", ok, err)
			}
		})
	}
}

func TestSaveAtTheLimit(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	in := saveInput(t, "k1", planBytes, summaryBytes)
	in.MaxArtifact = int64(len(planBytes))
	in.MaxSummary = int64(len(summaryBytes))
	if _, _, err := s.Save(in); err != nil {
		t.Errorf("Save of sources exactly at the limit: %v", err)
	}
}

// A file under /proc reports size zero and still yields bytes, like a file
// that grows after it was inspected.
func TestSaveCatchesGrowthWhileCopying(t *testing.T) {
	t.Parallel()
	const grower = "/proc/version"
	if info, err := os.Lstat(grower); runtime.GOOS != "linux" || err != nil || info.Size() != 0 {
		t.Skip("needs a /proc file that reports size zero")
	}
	s, _ := newStore(t)
	in := saveInput(t, "k1", planBytes, summaryBytes)
	in.ArtifactPath = grower
	in.MaxArtifact = 8

	_, _, err := s.Save(in)
	var se *SourceError
	if !errors.As(err, &se) || se.Detail != "is larger than 8 bytes" {
		t.Fatalf("Save error = %v, want a *SourceError for the limit", err)
	}
	if dirs := recordDirs(t, s); len(dirs) != 0 {
		t.Errorf("a refused Save left %v behind", dirs)
	}
}

func TestSaveFailsWithoutARoot(t *testing.T) {
	t.Parallel()
	s := Open(filepath.Join(t.TempDir(), "absent"))
	_, _, err := s.Save(saveInput(t, "k1", planBytes, summaryBytes))
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Save error = %v, want one wrapping fs.ErrNotExist", err)
	}
}

func TestSaveReplacesOlder(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	first := savePlan(t, s, "k1")
	other := savePlan(t, s, "k2")
	clock.Advance(time.Minute)

	second, replaced, err := s.Save(saveInput(t, "k1", "second plan", "second summary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].ID != first.ID {
		t.Fatalf("replaced = %+v, want only %s", replaced, first.ID)
	}
	if got := s.StateOf(first); got != StateReplaced {
		t.Errorf("state of the first = %v, want replaced", got)
	}
	dir := recDir(s, first)
	if exists(filepath.Join(dir, "artifact")) || exists(filepath.Join(dir, "summary")) {
		t.Error("the replaced record kept its bytes")
	}
	if !exists(filepath.Join(dir, "replaced")) || !exists(filepath.Join(dir, "record.json")) {
		t.Error("the replaced record lost its marker or its record")
	}
	if got := s.StateOf(other); got != StateReady {
		t.Errorf("state of another key = %v, want ready", got)
	}
	if err := s.Verify(other); err != nil {
		t.Errorf("Verify of another key: %v", err)
	}
	got, state, ok, err := s.Latest("k1")
	if err != nil || !ok || got.ID != second.ID || state != StateReady {
		t.Errorf("Latest = %s %v %v %v, want %s ready", got.ID, state, ok, err, second.ID)
	}

	clock.Advance(time.Minute)
	_, replaced, err = s.Save(saveInput(t, "k1", "third plan", "third summary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].ID != second.ID {
		t.Errorf("replaced = %+v, want only %s: a record is marked once", replaced, second.ID)
	}
}

func TestSaveWithAnotherKeyReplacesNothing(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	first := savePlan(t, s, "k1")
	clock.Advance(time.Minute)
	_, replaced, err := s.Save(saveInput(t, "k2", planBytes, summaryBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 0 {
		t.Errorf("replaced = %+v, want none", replaced)
	}
	if err := s.Verify(first); err != nil {
		t.Errorf("Verify of the other key's record: %v", err)
	}
	if got := s.StateOf(first); got != StateReady {
		t.Errorf("state = %v, want ready", got)
	}
}

func TestSaveLeavesAClaimedRecord(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	first := savePlan(t, s, "k1")
	attempt, err := s.Claim(first, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	_, replaced, err := s.Save(saveInput(t, "k1", planBytes, summaryBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 0 {
		t.Errorf("replaced = %+v, want none: the older record has a claim", replaced)
	}
	if exists(filepath.Join(recDir(s, first), "replaced")) {
		t.Error("a claimed record was marked replaced")
	}
	if got := mustRead(t, attempt.Path); got != planBytes {
		t.Errorf("the private copy changed: %q", got)
	}
	if got := s.StateOf(first); got != StateUnfinished {
		t.Errorf("state = %v, want unfinished", got)
	}
}

// A plan stored later is the newer one whatever the clock says, and marking
// the older ones is the newer one's work only: an older record never marks a
// newer one, which keeps two Saves that run at once from replacing each
// other.
func TestSaveNeverMarksANewerRecord(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	older := savePlan(t, s, "k1")
	clock.Advance(-time.Minute)
	newer, replaced, err := s.Save(saveInput(t, "k1", planBytes, summaryBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].ID != older.ID {
		t.Errorf("replaced = %+v, want the plan stored before", replaced)
	}
	if !newer.Created.After(older.Created) {
		t.Errorf("the plan stored later was created at %v, the earlier one at %v", newer.Created, older.Created)
	}
	if got := s.replaceOlder(older); len(got) != 0 {
		t.Errorf("the older record marked %+v", got)
	}
	if got := s.StateOf(newer); got != StateReady {
		t.Errorf("state of the newer record = %v, want ready", got)
	}
	got, _, ok, err := s.Latest("k1")
	if err != nil || !ok || got.ID != newer.ID {
		t.Errorf("Latest = %s, want the newer %s", got.ID, newer.ID)
	}

	s.Prune()
	if got := s.StateOf(older); got != StateReplaced {
		t.Errorf("after Prune the older record is %v, want replaced", got)
	}
	if exists(filepath.Join(recDir(s, older), "artifact")) || exists(filepath.Join(recDir(s, older), "summary")) {
		t.Error("Prune left the bytes of a replaced record")
	}
	if err := s.Verify(newer); err != nil {
		t.Errorf("Verify of the newer record after Prune: %v", err)
	}
}

func TestConcurrentSaves(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	base := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	var tick atomic.Int64
	s.Now = func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Millisecond) }

	const n = 20
	inputs := make([]SaveInput, n)
	for i := range inputs {
		inputs[i] = saveInput(t, "k1", planBytes, summaryBytes)
	}
	recs := make([]Record, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { recs[i], _, errs[i] = s.Save(inputs[i]) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	newest := slices.MaxFunc(recs, compareRecords)
	got, state, ok, err := s.Latest("k1")
	if err != nil || !ok || got.ID != newest.ID || state != StateReady {
		t.Fatalf("Latest = %s %v %v %v, want %s ready", got.ID, state, ok, err, newest.ID)
	}

	s.Prune()
	for _, rec := range recs {
		want := StateReplaced
		if rec.ID == newest.ID {
			want = StateReady
		}
		if got := s.StateOf(rec); got != want {
			t.Errorf("record %s created %v is %v, want %v", rec.ID, rec.Created, got, want)
		}
	}
	if err := s.Verify(newest); err != nil {
		t.Errorf("Verify of the newest: %v", err)
	}
}

func TestEqualCreatedIsOrderedByID(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	// Two Saves that run at once do not see each other and can read the same
	// time. The second record is stored under another key and then given the
	// first one's, which is what such a pair looks like on disk.
	a := savePlan(t, s, "k1")
	b := savePlan(t, s, "k2")
	b.Key = "k1"
	if err := writeJSON(filepath.Join(recDir(s, b), recordName), b); err != nil {
		t.Fatal(err)
	}
	if !a.Created.Equal(b.Created) {
		t.Fatalf("the records differ in created: %v, %v", a.Created, b.Created)
	}
	newest, oldest := a, b
	if b.ID > a.ID {
		newest, oldest = b, a
	}
	got, _, ok, err := s.Latest("k1")
	if err != nil || !ok || got.ID != newest.ID {
		t.Fatalf("Latest = %s, want the greater id %s", got.ID, newest.ID)
	}
	s.Prune()
	if got := s.StateOf(oldest); got != StateReplaced {
		t.Errorf("the smaller id is %v, want replaced", got)
	}
	if got := s.StateOf(newest); got != StateReady {
		t.Errorf("the greater id is %v, want ready", got)
	}
}

func TestLatestNotFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := Open(root)
	if _, _, ok, err := s.Latest("k1"); ok || err != nil {
		t.Errorf("Latest on a missing store = ok %v, err %v", ok, err)
	}
	if _, ok := s.LastAttempt("k1", ""); ok {
		t.Error("LastAttempt on a missing store found an attempt")
	}
	if _, ok := s.AttemptOf(Record{ID: "7f3c9a1e52b84d06"}); ok {
		t.Error("AttemptOf on a missing store found an attempt")
	}
	s.Prune()
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a lookup created %v", entries)
	}

	savePlan(t, s, "k1")
	if _, _, ok, err := s.Latest("k2"); ok || err != nil {
		t.Errorf("Latest for another key = ok %v, err %v", ok, err)
	}
}

const (
	claimJSON   = `{"attempt":"00000000000000aa","applyTask":"apply","surface":"mcp","started":"2026-11-17T10:00:01Z"}`
	appliedJSON = `{"attempt":"00000000000000aa","applyTask":"apply","surface":"mcp","started":"2026-11-17T10:00:01Z","finished":"2026-11-17T10:00:05Z","exit":0,"state":"applied","tampered":false}`
	failedJSON  = `{"attempt":"00000000000000aa","applyTask":"apply","surface":"mcp","started":"2026-11-17T10:00:01Z","finished":"2026-11-17T10:00:05Z","exit":3,"state":"failed","tampered":true}`
)

func TestStateFromFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		files   map[string]string
		expired bool
		want    State
	}{
		{name: "nothing", want: StateReady},
		{name: "expired", expired: true, want: StateExpired},
		{name: "replaced", files: map[string]string{"replaced": ""}, want: StateReplaced},
		{name: "replaced and expired", files: map[string]string{"replaced": ""}, expired: true, want: StateReplaced},
		{name: "claim", files: map[string]string{"claim": claimJSON}, want: StateUnfinished},
		{name: "unreadable claim", files: map[string]string{"claim": "{"}, want: StateUnfinished},
		{name: "claim beside a replaced marker", files: map[string]string{"claim": claimJSON, "replaced": ""}, want: StateUnfinished},
		{name: "claim on an expired record", files: map[string]string{"claim": claimJSON}, expired: true, want: StateUnfinished},
		{name: "claim and unreadable outcome", files: map[string]string{"claim": claimJSON, "outcome.json": "{not json"}, want: StateUnfinished},
		{name: "unreadable outcome alone", files: map[string]string{"outcome.json": ""}, want: StateUnfinished},
		{name: "outcome with an unknown state", files: map[string]string{"claim": claimJSON, "outcome.json": `{"state":"ready"}`}, want: StateUnfinished},
		{name: "applied", files: map[string]string{"claim": claimJSON, "outcome.json": appliedJSON}, want: StateApplied},
		{name: "applied without a claim", files: map[string]string{"outcome.json": appliedJSON}, want: StateApplied},
		{name: "failed", files: map[string]string{"claim": claimJSON, "outcome.json": failedJSON}, want: StateFailed},
		{name: "failed, replaced and expired", files: map[string]string{"claim": claimJSON, "outcome.json": failedJSON, "replaced": ""}, expired: true, want: StateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, clock := newStore(t)
			rec := savePlan(t, s, "k1")
			for name, content := range tt.files {
				put(t, filepath.Join(recDir(s, rec), name), content)
			}
			if tt.expired {
				clock.Advance(time.Hour + time.Nanosecond)
			}
			got, state, ok, err := s.Latest("k1")
			if err != nil || !ok || got.ID != rec.ID {
				t.Fatalf("Latest = %s, ok %v, err %v", got.ID, ok, err)
			}
			if state != tt.want {
				t.Errorf("state = %v, want %v", state, tt.want)
			}
		})
	}
}

func TestExpiryIsStrict(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	rec := savePlan(t, s, "k1")
	clock.Advance(time.Hour)
	if got := s.StateOf(rec); got != StateReady {
		t.Errorf("state at the expiry instant = %v, want ready", got)
	}
	clock.Advance(time.Nanosecond)
	if got := s.StateOf(rec); got != StateExpired {
		t.Errorf("state after the expiry instant = %v, want expired", got)
	}
}

func TestUnreadableRecordIsInvisible(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	first := savePlan(t, s, "k1")
	clock.Advance(time.Minute)
	second := savePlan(t, s, "k1")

	put(t, filepath.Join(recDir(s, second), "record.json"), "{broken")
	got, state, ok, err := s.Latest("k1")
	if err != nil || !ok || got.ID != first.ID || state != StateReplaced {
		t.Fatalf("Latest = %s %v %v %v, want the older %s, replaced", got.ID, state, ok, err, first.ID)
	}

	if err := os.Remove(filepath.Join(recDir(s, first), "record.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.Latest("k1"); ok || err != nil {
		t.Errorf("Latest = ok %v, err %v, want nothing visible", ok, err)
	}
}

func TestRecordInAForeignDirectoryIsInvisible(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	raw := mustRead(t, filepath.Join(recDir(s, rec), "record.json"))
	if err := os.RemoveAll(recDir(s, rec)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"00000000000000ff", "not-an-id"} {
		dir := filepath.Join(s.Dir(), name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, "record.json"), raw)
	}
	put(t, filepath.Join(s.Dir(), "00000000000000ee"), raw)
	if _, _, ok, err := s.Latest("k1"); ok || err != nil {
		t.Errorf("Latest = ok %v, err %v: a record outside its own directory must be invisible", ok, err)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	type change func(t *testing.T, path string)
	remove := func(t *testing.T, path string) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	toDir := func(t *testing.T, path string) {
		remove(t, path)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	toLink := func(t *testing.T, path string) {
		same := source(t, mustRead(t, path))
		remove(t, path)
		if err := os.Symlink(same, path); err != nil {
			t.Fatal(err)
		}
	}
	shorter := func(t *testing.T, path string) { put(t, path, "x") }
	sameSize := func(t *testing.T, path string) {
		put(t, path, strings.Repeat("x", len(mustRead(t, path))))
	}
	tests := []struct {
		name   string
		posix  bool
		change change
		detail string
	}{
		{name: "untouched"},
		{name: "removed", change: remove, detail: "is missing"},
		{name: "replaced by a directory", change: toDir, detail: "is not a regular file"},
		{name: "replaced by a symbolic link", posix: true, change: toLink, detail: "is not a regular file"},
		{name: "overwritten with another size", change: shorter, detail: "does not have the recorded digest"},
		{name: "overwritten with the same size", change: sameSize, detail: "does not have the recorded digest"},
	}
	for _, what := range []string{"artifact", "summary"} {
		for _, tt := range tests {
			t.Run(what+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				if tt.posix {
					posixOnly(t)
				}
				s, _ := newStore(t)
				rec := savePlan(t, s, "k1")
				if tt.change != nil {
					tt.change(t, filepath.Join(recDir(s, rec), what))
				}
				err := s.Verify(rec)
				if tt.detail == "" {
					if err != nil {
						t.Fatalf("Verify of an untouched record: %v", err)
					}
					return
				}
				var ae *ArtifactError
				if !errors.As(err, &ae) {
					t.Fatalf("Verify error = %v, want an *ArtifactError", err)
				}
				if ae.What != what || ae.Detail != tt.detail {
					t.Errorf("ArtifactError = %+v, want %s %s", ae, what, tt.detail)
				}
			})
		}
	}
}

func TestVerifyComparesWithTheRecord(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	rec.Artifact.SHA256 = strings.Repeat("0", 64)
	var ae *ArtifactError
	if err := s.Verify(rec); !errors.As(err, &ae) || ae.What != "artifact" || ae.Detail != "does not have the recorded digest" {
		t.Errorf("Verify against another digest = %v", err)
	}
}

func TestSummary(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	got, err := s.Summary(rec)
	if err != nil || got != summaryBytes {
		t.Fatalf("Summary = %q, %v; want the stored text", got, err)
	}

	put(t, filepath.Join(recDir(s, rec), "summary"), strings.Repeat("y", len(summaryBytes)))
	got, err = s.Summary(rec)
	var ae *ArtifactError
	if !errors.As(err, &ae) || ae.What != "summary" || ae.Detail != "does not have the recorded digest" {
		t.Errorf("Summary of an overwritten file: error %v", err)
	}
	if got != "" {
		t.Errorf("Summary returned %q for an overwritten file", got)
	}

	if err := os.Remove(filepath.Join(recDir(s, rec), "summary")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Summary(rec); !errors.As(err, &ae) || ae.Detail != "is missing" {
		t.Errorf("Summary of a removed file: error %v", err)
	}
}

func TestRecordWithAForeignIDReachesNothing(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	outside := filepath.Join(s.Dir(), "..", "escape")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(outside, "artifact"), planBytes)
	put(t, filepath.Join(outside, "summary"), summaryBytes)
	put(t, filepath.Join(outside, "claim"), claimJSON)
	rec.ID = filepath.Join("..", "escape")

	var ae *ArtifactError
	if err := s.Verify(rec); !errors.As(err, &ae) || ae.Detail != "is missing" {
		t.Errorf("Verify = %v, want the artifact missing", err)
	}
	if _, err := s.Summary(rec); !errors.As(err, &ae) || ae.Detail != "is missing" {
		t.Errorf("Summary = %v, want the artifact missing", err)
	}
	if _, err := s.Claim(rec, "apply", "mcp"); !errors.As(err, &ae) || ae.Detail != "is missing" {
		t.Errorf("Claim = %v, want the artifact missing", err)
	}
	if _, ok := s.AttemptOf(rec); ok {
		t.Error("AttemptOf read a claim outside the store")
	}
	if got := attemptDirs(t, outside); len(got) != 0 {
		t.Errorf("Claim wrote %v outside the store", got)
	}
}

func TestUnusableStore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		posix  bool
		damage func(t *testing.T, root string) string
		reason string
	}{
		{
			name:  ".rune is a symbolic link",
			posix: true,
			damage: func(t *testing.T, root string) string {
				runeDir := filepath.Join(root, ".rune")
				moved := filepath.Join(root, "moved")
				if err := os.Rename(runeDir, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, runeDir); err != nil {
					t.Fatal(err)
				}
				return runeDir
			},
			reason: "is a symbolic link",
		},
		{
			name:  ".rune/plans is a symbolic link",
			posix: true,
			damage: func(t *testing.T, root string) string {
				plans := filepath.Join(root, ".rune", "plans")
				moved := filepath.Join(root, "moved")
				if err := os.Rename(plans, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, plans); err != nil {
					t.Fatal(err)
				}
				return plans
			},
			reason: "is a symbolic link",
		},
		{
			name: "plans is a file",
			damage: func(t *testing.T, root string) string {
				plans := filepath.Join(root, ".rune", "plans")
				put(t, plans, "not a directory")
				return plans
			},
			reason: "is not a directory",
		},
		{
			name: ".rune is a file",
			damage: func(t *testing.T, root string) string {
				runeDir := filepath.Join(root, ".rune")
				put(t, runeDir, "not a directory")
				return runeDir
			},
			reason: "is not a directory",
		},
		{
			name:  "plans belongs to another user",
			posix: true,
			damage: func(t *testing.T, root string) string {
				if os.Getuid() != 0 {
					t.Skip("only root can give a directory away")
				}
				plans := filepath.Join(root, ".rune", "plans")
				if err := os.Chown(plans, 12345, 12345); err != nil {
					t.Fatal(err)
				}
				return plans
			},
			reason: "belongs to another user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.posix {
				posixOnly(t)
			}
			s, _ := newStore(t)
			rec := savePlan(t, s, "k1")
			path := tt.damage(t, s.root)
			moved := filepath.Join(s.root, "moved")
			var before []string
			if exists(moved) {
				before = tree(t, moved)
			}

			check := func(op string, err error) {
				t.Helper()
				var ue *UnusableError
				if !errors.As(err, &ue) {
					t.Errorf("%s error = %v, want an *UnusableError", op, err)
					return
				}
				if ue.Path != path || ue.Reason != tt.reason {
					t.Errorf("%s: UnusableError = %+v, want %q %q", op, ue, path, tt.reason)
				}
			}
			_, _, err := s.Save(saveInput(t, "k1", planBytes, summaryBytes))
			check("Save", err)
			_, _, ok, err := s.Latest("k1")
			check("Latest", err)
			if ok {
				t.Error("Latest found a record in an unusable store")
			}
			check("Verify", s.Verify(rec))
			_, err = s.Summary(rec)
			check("Summary", err)
			_, err = s.Claim(rec, "apply", "mcp")
			check("Claim", err)
			check("Log", s.Log(Event{Event: EventPlanCreated}))
			if _, ok := s.LastAttempt("k1", ""); ok {
				t.Error("LastAttempt found an attempt in an unusable store")
			}
			if _, ok := s.AttemptOf(rec); ok {
				t.Error("AttemptOf found an attempt in an unusable store")
			}
			s.Prune()
			if exists(moved) {
				if after := tree(t, moved); !slices.Equal(before, after) {
					t.Errorf("the link's target changed:\nbefore %v\nafter  %v", before, after)
				}
			}
		})
	}
}

func tree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		paths = append(paths, path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestPrune(t *testing.T) {
	t.Parallel()
	const (
		none = iota
		keep
		gone
	)
	claim := func(t *testing.T, s *Store, rec Record) *Attempt {
		t.Helper()
		a, err := s.Claim(rec, "apply", "mcp")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	restoreBytes := func(t *testing.T, s *Store, rec Record) {
		t.Helper()
		dir := recDir(s, rec)
		put(t, filepath.Join(dir, "artifact"), planBytes)
		put(t, filepath.Join(dir, "summary"), summaryBytes)
		if err := os.Mkdir(filepath.Join(dir, "attempt-00000000000000aa"), 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, "attempt-00000000000000aa", "plan"), planBytes)
	}
	finished := func(exit int) func(t *testing.T, s *Store, c *fakeClock, rec Record) {
		return func(t *testing.T, s *Store, c *fakeClock, rec Record) {
			c.Advance(50 * time.Minute)
			a := claim(t, s, rec)
			c.Advance(5 * time.Minute)
			if _, err := a.Finish(exit); err != nil {
				t.Fatal(err)
			}
			restoreBytes(t, s, rec)
		}
	}
	unfinished := func(t *testing.T, s *Store, c *fakeClock, rec Record) {
		c.Advance(50 * time.Minute)
		claim(t, s, rec)
	}
	unreadableClaim := func(t *testing.T, s *Store, c *fakeClock, rec Record) {
		unfinished(t, s, c, rec)
		path := filepath.Join(recDir(s, rec), "claim")
		put(t, path, "{broken")
		if err := os.Chtimes(path, c.Now(), c.Now()); err != nil {
			t.Fatal(err)
		}
	}
	const day = 24 * time.Hour

	tests := []struct {
		name   string
		setup  func(t *testing.T, s *Store, c *fakeClock, rec Record)
		wait   time.Duration
		bytes  int
		copy   int
		record int
		state  State
	}{
		{name: "ready", wait: 30 * time.Minute, bytes: keep, record: keep, state: StateReady},
		{
			name: "replaced",
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				put(t, filepath.Join(recDir(s, rec), "replaced"), "")
			},
			bytes: gone, record: keep, state: StateReplaced,
		},
		{name: "expired", wait: 2 * time.Hour, bytes: gone, record: keep, state: StateExpired},
		{name: "applied", setup: finished(0), bytes: gone, copy: gone, record: keep, state: StateApplied},
		{name: "failed", setup: finished(2), bytes: gone, copy: gone, record: keep, state: StateFailed},
		{name: "unfinished within the grace", setup: unfinished, wait: day - time.Second, bytes: keep, copy: keep, record: keep, state: StateUnfinished},
		{name: "unfinished at the grace", setup: unfinished, wait: day, bytes: gone, copy: gone, record: keep, state: StateUnfinished},
		{name: "unfinished after the grace", setup: unfinished, wait: 3 * day, bytes: gone, copy: gone, record: keep, state: StateUnfinished},
		{name: "unreadable claim within the grace", setup: unreadableClaim, wait: day - time.Minute, bytes: keep, copy: keep, record: keep, state: StateUnfinished},
		{name: "unreadable claim after the grace", setup: unreadableClaim, wait: day, bytes: gone, copy: gone, record: keep, state: StateUnfinished},
		{name: "seven days after creation", wait: 7 * day, bytes: gone, record: keep, state: StateExpired},
		{name: "more than seven days after creation", wait: 7*day + time.Second, bytes: gone, record: gone},
		{name: "seven days after the claim", setup: unfinished, wait: 7 * day, bytes: gone, copy: gone, record: keep, state: StateUnfinished},
		{name: "more than seven days after the claim", setup: unfinished, wait: 7*day + time.Second, bytes: gone, copy: gone, record: gone},
		{name: "seven days after the outcome", setup: finished(0), wait: 7 * day, bytes: gone, copy: gone, record: keep, state: StateApplied},
		{name: "more than seven days after the outcome", setup: finished(0), wait: 7*day + time.Second, bytes: gone, copy: gone, record: gone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, clock := newStore(t)
			rec := savePlan(t, s, "k1")
			if tt.setup != nil {
				tt.setup(t, s, clock, rec)
			}
			clock.Advance(tt.wait)
			s.Prune()

			dir := recDir(s, rec)
			if got := exists(dir); got != (tt.record == keep) {
				t.Fatalf("record directory exists = %v", got)
			}
			if tt.record == gone {
				if _, _, ok, _ := s.Latest("k1"); ok {
					t.Error("Latest still finds the removed record")
				}
				return
			}
			for _, name := range []string{"artifact", "summary"} {
				if got := exists(filepath.Join(dir, name)); got != (tt.bytes == keep) {
					t.Errorf("%s exists = %v", name, got)
				}
			}
			if copies := attemptDirs(t, dir); tt.copy != none && (len(copies) > 0) != (tt.copy == keep) {
				t.Errorf("attempt directories = %v", copies)
			}
			if !exists(filepath.Join(dir, "record.json")) {
				t.Error("record.json is gone")
			}
			if got := s.StateOf(rec); got != tt.state {
				t.Errorf("state = %v, want %v", got, tt.state)
			}
		})
	}
}

// A Claim that was killed between its copy and its claim leaves the copy in a
// record nobody claimed. It holds the plan's bytes, so it goes with them.
func TestPruneRemovesLeftoverCopies(t *testing.T) {
	t.Parallel()
	const day = 24 * time.Hour
	tests := []struct {
		name     string
		replaced bool
		wait     time.Duration
		kept     bool
	}{
		{name: "replaced", replaced: true},
		{name: "expired within the grace", wait: time.Hour + day - time.Second, kept: true},
		{name: "expired at the grace", wait: time.Hour + day},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, clock := newStore(t)
			rec := savePlan(t, s, "k1")
			dir := recDir(s, rec)
			left := filepath.Join(dir, "attempt-00000000000000aa")
			if err := os.Mkdir(left, 0o700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(left, "plan"), planBytes)
			if tt.replaced {
				put(t, filepath.Join(dir, "replaced"), "")
			}
			clock.Advance(tt.wait)
			s.Prune()
			if got := exists(left); got != tt.kept {
				t.Errorf("the leftover copy exists = %v, want %v", got, tt.kept)
			}
			if exists(filepath.Join(dir, "artifact")) {
				t.Error("the artifact is still there")
			}
		})
	}
}

func TestPruneKeepsAClaimedOlderRecordUnmarked(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	first := savePlan(t, s, "k1")
	if _, err := s.Claim(first, "apply", "mcp"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	second := savePlan(t, s, "k1")
	s.Prune()
	if exists(filepath.Join(recDir(s, first), "replaced")) {
		t.Error("Prune marked a claimed record replaced")
	}
	if err := s.Verify(first); err != nil {
		t.Errorf("Prune touched a record being applied: %v", err)
	}
	if err := s.Verify(second); err != nil {
		t.Errorf("Prune touched the ready record: %v", err)
	}
}

func TestPlanBytesStayInTheirFiles(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	check := func(stage string) {
		t.Helper()
		err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if name := d.Name(); name == "artifact" || name == "summary" || name == "plan" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if sharesBytes(data, []byte(planBytes), 8) || sharesBytes(data, []byte(summaryBytes), 8) {
				t.Errorf("%s: %s holds plan or summary bytes", stage, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	rec := savePlan(t, s, "k1")
	if err := s.Log(Event{Event: EventPlanCreated, Plan: rec.ID, Digest: rec.Artifact.SHA256}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summary(rec); err != nil {
		t.Fatal(err)
	}
	check("after Save")
	a, err := s.Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	check("after Claim")
	if _, err := a.Finish(0); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	savePlan(t, s, "k1")
	clock.Advance(time.Minute)
	savePlan(t, s, "k1")
	s.Prune()
	check("after Finish and Prune")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestHashCopy(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	got, err := hashCopy(&out, strings.NewReader("abcdef"), 6)
	if err != nil || got != digestOf("abcdef") || out.String() != "abcdef" {
		t.Errorf("hashCopy within the limit = %+v, %q, %v", got, out.String(), err)
	}
	out.Reset()
	got, err = hashCopy(&out, strings.NewReader("abcdef"), 3)
	if err != nil || got != digestOf("abcd") || out.String() != "abcd" {
		t.Errorf("hashCopy over the limit = %+v, %q, %v; want one byte past the limit", got, out.String(), err)
	}
	if _, err := hashCopy(failingWriter{}, strings.NewReader("abc"), 3); err == nil {
		t.Error("hashCopy hid a write error")
	}
}

func TestWriteJSONAndReadJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "value.json")
	in := claimFile{Attempt: "aa", ApplyTask: "apply", Surface: "cli", Started: time.Date(2026, 11, 17, 10, 0, 0, 5, time.UTC)}
	if err := writeJSON(path, in); err != nil {
		t.Fatal(err)
	}
	var out claimFile
	if err := readJSON(path, &out); err != nil {
		t.Fatal(err)
	}
	if out.Attempt != "aa" || out.ApplyTask != "apply" || out.Surface != "cli" || !out.Started.Equal(in.Started) {
		t.Errorf("read %+v, want %+v", out, in)
	}
	if exists(path + ".tmp") {
		t.Error("writeJSON left its temporary file")
	}

	if err := writeJSON(path, make(chan int)); err == nil {
		t.Error("writeJSON accepted a value JSON cannot hold")
	}
	if err := readJSON(path, &out); err != nil || out.Attempt != "aa" {
		t.Errorf("a failed writeJSON changed the file: %+v, %v", out, err)
	}
	if err := writeJSON(filepath.Join(dir, "absent", "value.json"), in); err == nil {
		t.Error("writeJSON into a missing directory returned no error")
	}

	err := readJSON(filepath.Join(dir, "absent.json"), &out)
	if problem, ok := errors.AsType[fileProblem](err); !ok || problem != problemMissing {
		t.Errorf("readJSON of a missing file = %v", err)
	}
	put(t, path, "{broken")
	err = readJSON(path, &out)
	if _, ok := errors.AsType[fileProblem](err); err == nil || ok {
		t.Errorf("readJSON of a broken file = %v, want a parse error", err)
	}
}

func TestAtomicWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	for _, content := range []string{"one", "two"} {
		if err := atomicWrite(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, path); got != content {
			t.Errorf("file = %q, want %q", got, content)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 600", info.Mode().Perm())
		}
	}

	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(blocked, "child"), "x")
	if err := atomicWrite(blocked, []byte("x")); err == nil {
		t.Error("atomicWrite over a directory returned no error")
	}
	if exists(blocked + ".tmp") {
		t.Error("a failed atomicWrite left its temporary file")
	}
}

func TestStoreSource(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "artifact")
	got, err := storeSource(source(t, "abc"), dst, 3)
	if err != nil || got != digestOf("abc") || mustRead(t, dst) != "abc" {
		t.Fatalf("storeSource = %+v, %v", got, err)
	}

	_, err = storeSource(source(t, "abc"), dst, 3)
	var se *SourceError
	if err == nil || errors.As(err, &se) {
		t.Errorf("storeSource over an existing file = %v, want the system's error", err)
	}
	if got := mustRead(t, dst); got != "abc" {
		t.Errorf("storeSource overwrote a stored file: %q", got)
	}

	_, err = storeSource(source(t, "abcd"), filepath.Join(t.TempDir(), "artifact"), 3)
	if !errors.As(err, &se) || se.Detail != "is larger than 3 bytes" {
		t.Errorf("storeSource over the limit = %v", err)
	}
}

func TestCheckStored(t *testing.T) {
	t.Parallel()
	path := source(t, "abc")
	var out bytes.Buffer
	if err := checkStored(path, "summary", digestOf("abc"), &out); err != nil || out.String() != "abc" {
		t.Fatalf("checkStored = %v, wrote %q", err, out.String())
	}
	var ae *ArtifactError
	if err := checkStored(path, "summary", digestOf("abd"), io.Discard); !errors.As(err, &ae) || ae.What != "summary" || ae.Detail != detailDigest {
		t.Errorf("checkStored against another digest = %v", err)
	}
	if err := checkStored(path, "summary", Digest{SHA256: digestOf("abc").SHA256, Size: 2}, io.Discard); !errors.As(err, &ae) || ae.Detail != detailDigest {
		t.Errorf("checkStored against another size = %v", err)
	}
	if err := checkStored(path+"-absent", "artifact", digestOf("abc"), io.Discard); !errors.As(err, &ae) || ae.What != "artifact" || ae.Detail != "is missing" {
		t.Errorf("checkStored of a missing file = %v", err)
	}
	if err := checkStored(path, "artifact", digestOf("abc"), failingWriter{}); err == nil || errors.As(err, &ae) {
		t.Errorf("checkStored with a failing writer = %v, want the writer's error", err)
	}
	if runtime.GOOS != "windows" {
		if err := checkStored(filepath.Join(path, "child"), "artifact", digestOf("abc"), io.Discard); err == nil || errors.As(err, &ae) {
			t.Errorf("checkStored below a file = %v, want the system's error", err)
		}
	}
}

func TestRecords(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	if got := s.records(); got != nil {
		t.Errorf("records of a missing store = %v", got)
	}
	a := savePlan(t, s, "k1")
	clock.Advance(-time.Minute)
	b := savePlan(t, s, "k2")
	clock.Advance(2 * time.Minute)
	c := savePlan(t, s, "k1")

	var ids []string
	for _, rec := range s.records() {
		ids = append(ids, rec.ID)
	}
	if want := []string{b.ID, a.ID, c.ID}; !slices.Equal(ids, want) {
		t.Errorf("records = %v, want %v: every key, oldest first", ids, want)
	}
}

func TestMarkReplaced(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	if !s.markReplaced(rec) {
		t.Fatal("markReplaced did not mark an unclaimed record")
	}
	if s.markReplaced(rec) {
		t.Error("markReplaced marked a record twice")
	}
	if exists(filepath.Join(recDir(s, rec), "artifact")) || exists(filepath.Join(recDir(s, rec), "summary")) {
		t.Error("markReplaced left the bytes")
	}

	claimed := savePlan(t, s, "k2")
	if _, err := s.Claim(claimed, "apply", "mcp"); err != nil {
		t.Fatal(err)
	}
	if s.markReplaced(claimed) {
		t.Error("markReplaced marked a claimed record")
	}
	if err := s.Verify(claimed); err != nil {
		t.Errorf("markReplaced touched a claimed record: %v", err)
	}
	if s.markReplaced(Record{ID: "00000000000000ab"}) {
		t.Error("markReplaced marked a record that does not exist")
	}
}

// A claim written after markReplaced read the record as unclaimed, and before
// the marker exists, keeps the record: it is not reported as replaced and its
// bytes stay for the apply.
func TestPlaceMarkerLeavesARecordClaimedBesideIt(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k")
	dir := recDir(s, rec)
	if err := writeClaim(filepath.Join(dir, "claim"), claimFile{Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: s.now()}); err != nil {
		t.Fatal(err)
	}
	if placeMarker(dir) {
		t.Error("placeMarker reported a claimed record as replaced")
	}
	if err := s.Verify(rec); err != nil {
		t.Errorf("placeMarker removed the bytes of a claimed record: %v", err)
	}
	if got := s.StateOf(rec); got != StateUnfinished {
		t.Errorf("state = %v, want unfinished: the claim wins over the marker", got)
	}
}

func TestReplaceOlder(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	old := savePlan(t, s, "k1")
	other := savePlan(t, s, "k2")
	clock.Advance(time.Minute)
	newer := Record{ID: "ffffffffffffffff", Key: "k1", Created: clock.Now()}

	if marked := s.replaceOlder(old); len(marked) != 0 {
		t.Errorf("replaceOlder(old) marked %+v: a record is not older than itself", marked)
	}
	marked := s.replaceOlder(newer)
	if len(marked) != 1 || marked[0].ID != old.ID {
		t.Fatalf("replaceOlder marked %+v, want %s", marked, old.ID)
	}
	if marked := s.replaceOlder(newer); len(marked) != 0 {
		t.Errorf("a second replaceOlder marked %+v", marked)
	}
	if got := s.StateOf(other); got != StateReady {
		t.Errorf("another key is %v", got)
	}
}

func TestRemoveBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := removeBytes(dir); err != nil {
		t.Errorf("removeBytes of an empty directory: %v", err)
	}
	put(t, filepath.Join(dir, "artifact"), "a")
	if err := os.Mkdir(filepath.Join(dir, "summary"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "summary", "child"), "s")
	put(t, filepath.Join(dir, "record.json"), "{}")
	if err := removeBytes(dir); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "artifact")) || exists(filepath.Join(dir, "summary")) {
		t.Error("removeBytes left the artifact or the summary")
	}
	if !exists(filepath.Join(dir, "record.json")) {
		t.Error("removeBytes removed the record")
	}
}

func TestStateOf(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	live := Record{Expires: now.Add(time.Minute)}
	dead := Record{Expires: now.Add(-time.Minute)}
	plain := t.TempDir()
	marked := t.TempDir()
	put(t, filepath.Join(marked, "replaced"), "")
	tests := []struct {
		name string
		rec  Record
		dir  string
		a    attemptFiles
		want State
	}{
		{"ready", live, plain, attemptFiles{}, StateReady},
		{"expired", dead, plain, attemptFiles{}, StateExpired},
		{"replaced", live, marked, attemptFiles{}, StateReplaced},
		{"replaced before expired", dead, marked, attemptFiles{}, StateReplaced},
		{"claimed before replaced", dead, marked, attemptFiles{claimed: true}, StateUnfinished},
		{"outcome before claim", dead, marked, attemptFiles{claimed: true, finished: true, state: StateFailed}, StateFailed},
		{"applied", live, plain, attemptFiles{claimed: true, finished: true, state: StateApplied}, StateApplied},
	}
	for _, tt := range tests {
		if got := stateOf(tt.rec, tt.dir, tt.a, now); got != tt.want {
			t.Errorf("%s: stateOf = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	dir, err := s.lookup(Record{ID: "00000000000000ab"})
	if err != nil || dir != filepath.Join(s.Dir(), "00000000000000ab") {
		t.Errorf("lookup in a missing store = %q, %v", dir, err)
	}
	var ae *ArtifactError
	if _, err := s.lookup(Record{ID: "x"}); !errors.As(err, &ae) || ae.What != "artifact" || ae.Detail != "is missing" {
		t.Errorf("lookup of a foreign id = %v", err)
	}
	put(t, filepath.Join(s.root, ".rune"), "x")
	var ue *UnusableError
	if _, err := s.lookup(Record{ID: "00000000000000ab"}); !errors.As(err, &ue) {
		t.Errorf("lookup in an unusable store = %v", err)
	}
}

func TestRemoveCopies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	removeCopies(filepath.Join(dir, "absent"))
	for _, name := range []string{"attempt-00000000000000aa", "attempt-00000000000000bb", "other"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "attempt-00000000000000aa", "plan"), []byte("p"), 0o400); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "record.json"), "{}")
	removeCopies(dir)
	if got := attemptDirs(t, dir); len(got) != 0 {
		t.Errorf("removeCopies left %v", got)
	}
	if !exists(filepath.Join(dir, "other")) || !exists(filepath.Join(dir, "record.json")) {
		t.Error("removeCopies removed something that is not a copy")
	}
}

func TestRemoveCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := removeCopy(filepath.Join(dir, "absent")); err != nil {
		t.Errorf("removeCopy of a missing directory: %v", err)
	}
	posixOnly(t)
	target := t.TempDir()
	put(t, filepath.Join(target, "plan"), "kept")
	if err := os.Chmod(target, 0o500); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "attempt-00000000000000aa")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := removeCopy(link); err != nil {
		t.Fatal(err)
	}
	if exists(link) {
		t.Error("removeCopy left the link")
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode().Perm() != 0o500 || !exists(filepath.Join(target, "plan")) {
		t.Errorf("removeCopy followed a link: %v, %v", info, err)
	}
	_ = os.Chmod(target, 0o700)
}

// A Save that was killed before it wrote its record leaves the plan's bytes
// in a directory no lookup sees. Prune removes it once it cannot be a Save
// that is still copying.
func TestPruneRemovesOrphans(t *testing.T) {
	s, clock := newStore(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := func(id string, age time.Duration) string {
		t.Helper()
		dir := filepath.Join(s.Dir(), id)
		if err := os.MkdirAll(filepath.Join(dir, attemptPrefix+"0123456789abcdef"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, artifactName), []byte("plan bytes"), 0o400); err != nil {
			t.Fatal(err)
		}
		when := clock.Now().Add(-age)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	old := orphan("00000000000000aa", UnfinishedGrace+time.Hour)
	young := orphan("00000000000000bb", time.Hour)
	other := filepath.Join(s.Dir(), "not-a-plan")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	when := clock.Now().Add(-UnfinishedGrace - time.Hour)
	if err := os.Chtimes(other, when, when); err != nil {
		t.Fatal(err)
	}

	s.Prune()
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an old directory with no record is still there: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("a young directory with no record was removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a directory that is not a plan's was removed: %v", err)
	}
}

func TestStateOfExported(t *testing.T) {
	s, clock := newStore(t)
	rec := savePlan(t, s, "key")
	if got := s.StateOf(rec); got != StateReady {
		t.Errorf("a new plan is %v", got)
	}
	clock.Advance(2 * time.Hour)
	if got := s.StateOf(rec); got != StateExpired {
		t.Errorf("a plan past its expiry is %v", got)
	}
}

// Which plan is newest must not depend on the clock moving forward: a plan
// stored after another one is the newer one, also when the clock stepped
// back in between, or it would be replaced by the plan it replaces.
func TestSaveAfterTheClockSteppedBack(t *testing.T) {
	s, clock := newStore(t)
	first := savePlan(t, s, "key")
	clock.Advance(-time.Hour)
	stored := clock.Now()
	second := savePlan(t, s, "key")
	if !second.Created.After(first.Created) {
		t.Fatalf("the later plan was created at %v, the earlier at %v", second.Created, first.Created)
	}
	if got := second.Expires.Sub(stored); got < time.Hour || got > time.Hour+time.Minute {
		t.Errorf("the expiry is %v after the plan was stored, want the declared hour", got)
	}
	s.Prune()
	latest, state, ok, err := s.Latest("key")
	if err != nil || !ok || latest.ID != second.ID || state != StateReady {
		t.Errorf("Latest = %s %v, %v, %v, want the later plan %s ready", latest.ID, state, ok, err, second.ID)
	}
	if got := s.StateOf(first); got != StateReplaced {
		t.Errorf("the earlier plan is %v, want replaced", got)
	}
	if err := s.Verify(second); err != nil {
		t.Errorf("the later plan lost its bytes: %v", err)
	}
}
