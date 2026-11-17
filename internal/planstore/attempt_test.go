package planstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func readOutcome(t *testing.T, s *Store, rec Record) outcomeFile {
	t.Helper()
	var out outcomeFile
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(recDir(s, rec), "outcome.json"))), &out); err != nil {
		t.Fatalf("outcome.json: %v", err)
	}
	return out
}

func TestClaim(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	rec := savePlan(t, s, "k1")
	clock.Advance(time.Minute)

	a, err := s.Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !validID(a.ID) {
		t.Errorf("attempt id = %q, want 16 hex characters", a.ID)
	}
	if a.Record.ID != rec.ID {
		t.Errorf("attempt record = %s, want %s", a.Record.ID, rec.ID)
	}
	if !filepath.IsAbs(a.Path) {
		t.Errorf("Path = %q, want an absolute path", a.Path)
	}
	if want := filepath.Join(recDir(s, rec), "attempt-"+a.ID, "plan"); a.Path != want {
		t.Errorf("Path = %q, want %q", a.Path, want)
	}
	if filepath.Dir(a.Path) == recDir(s, rec) {
		t.Error("the private copy shares the artifact's directory")
	}
	info, err := os.Lstat(a.Path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the private copy is not a regular file: %v", err)
	}
	if got := mustRead(t, a.Path); got != planBytes {
		t.Errorf("the private copy = %q, want the record's bytes", got)
	}
	if got := s.StateOf(rec); got != StateUnfinished {
		t.Errorf("state = %v, want unfinished", got)
	}

	var claim claimFile
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(recDir(s, rec), "claim"))), &claim); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Attempt != a.ID || claim.ApplyTask != "apply" || claim.Surface != "mcp" || !claim.Started.Equal(clock.Now()) {
		t.Errorf("claim = %+v", claim)
	}

	if _, err := s.Claim(rec, "apply", "mcp"); !errors.Is(err, ErrClaimed) {
		t.Errorf("second Claim error = %v, want ErrClaimed", err)
	}
	if got := attemptDirs(t, recDir(s, rec)); len(got) != 1 {
		t.Errorf("attempt directories = %v, want one", got)
	}
}

func TestClaimModes(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	s, _ := newStore(t)
	a, err := s.Claim(savePlan(t, s, "k1"), "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Lstat(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Mode().Perm() != 0o400 {
		t.Errorf("the private copy has mode %o, want 400", file.Mode().Perm())
	}
	dir, err := os.Lstat(filepath.Dir(a.Path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("the attempt directory has mode %o, want 700", dir.Mode().Perm())
	}
}

func TestClaimWithARelativeRoot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s := Open(".")
	rec := savePlan(t, s, "k1")
	a, err := s.Claim(rec, "apply", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(a.Path) {
		t.Errorf("Path = %q, want an absolute path", a.Path)
	}
	if got := mustRead(t, a.Path); got != planBytes {
		t.Errorf("the private copy = %q", got)
	}
}

func TestConcurrentClaims(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")

	const n = 50
	attempts := make([]*Attempt, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			attempts[i], errs[i] = s.Claim(rec, "apply", "mcp")
		})
	}
	close(start)
	wg.Wait()

	var won *Attempt
	for i, err := range errs {
		switch {
		case err == nil && won != nil:
			t.Fatalf("two callers claimed the record: %s and %s", won.ID, attempts[i].ID)
		case err == nil:
			won = attempts[i]
		case !errors.Is(err, ErrClaimed):
			t.Errorf("caller %d: error %v, want ErrClaimed", i, err)
		}
	}
	if won == nil {
		t.Fatal("no caller claimed the record")
	}
	dirs := attemptDirs(t, recDir(s, rec))
	if len(dirs) != 1 || dirs[0] != "attempt-"+won.ID {
		t.Errorf("attempt directories = %v, want only the winner's attempt-%s", dirs, won.ID)
	}
	if got := mustRead(t, won.Path); got != planBytes {
		t.Errorf("the winner's copy = %q", got)
	}
}

func TestClaimRefused(t *testing.T) {
	t.Parallel()
	artifact := func(s *Store, rec Record) string { return filepath.Join(recDir(s, rec), "artifact") }
	tests := []struct {
		name    string
		posix   bool
		setup   func(t *testing.T, s *Store, c *fakeClock, rec Record)
		wantErr error
		detail  string
	}{
		{
			name: "replaced",
			setup: func(t *testing.T, s *Store, c *fakeClock, _ Record) {
				c.Advance(time.Minute)
				savePlan(t, s, "k1")
			},
			wantErr: ErrReplaced,
		},
		{
			name:    "expired",
			setup:   func(_ *testing.T, _ *Store, c *fakeClock, _ Record) { c.Advance(time.Hour + time.Second) },
			wantErr: ErrExpired,
		},
		{
			name: "artifact overwritten with the same size",
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				put(t, artifact(s, rec), strings.Repeat("z", len(planBytes)))
			},
			detail: "does not have the recorded digest",
		},
		{
			name: "artifact overwritten with a longer file",
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				put(t, artifact(s, rec), planBytes+"and more")
			},
			detail: "does not have the recorded digest",
		},
		{
			name: "artifact removed",
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				if err := os.Remove(artifact(s, rec)); err != nil {
					t.Fatal(err)
				}
			},
			detail: "is missing",
		},
		{
			name: "artifact replaced by a directory",
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				if err := os.Remove(artifact(s, rec)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(artifact(s, rec), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			detail: "is not a regular file",
		},
		{
			name:  "artifact replaced by a symbolic link",
			posix: true,
			setup: func(t *testing.T, s *Store, _ *fakeClock, rec Record) {
				if err := os.Remove(artifact(s, rec)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source(t, planBytes), artifact(s, rec)); err != nil {
					t.Fatal(err)
				}
			},
			detail: "is not a regular file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.posix {
				posixOnly(t)
			}
			s, clock := newStore(t)
			rec := savePlan(t, s, "k1")
			tt.setup(t, s, clock, rec)
			before := s.StateOf(rec)

			a, err := s.Claim(rec, "apply", "mcp")
			if a != nil {
				t.Errorf("a refused Claim returned an attempt: %+v", a)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Claim error = %v, want %v", err, tt.wantErr)
				}
			} else {
				var ae *ArtifactError
				if !errors.As(err, &ae) || ae.What != "artifact" || ae.Detail != tt.detail {
					t.Errorf("Claim error = %v, want an *ArtifactError: artifact %s", err, tt.detail)
				}
			}
			dir := recDir(s, rec)
			if exists(filepath.Join(dir, "claim")) || exists(filepath.Join(dir, "outcome.json")) {
				t.Error("a refused Claim left a claim or an outcome")
			}
			if got := attemptDirs(t, dir); len(got) != 0 {
				t.Errorf("a refused Claim left %v", got)
			}
			if got := s.StateOf(rec); got != before {
				t.Errorf("a refused Claim changed the state from %v to %v", before, got)
			}
			if _, ok := s.AttemptOf(rec); ok {
				t.Error("a refused Claim left an attempt")
			}
		})
	}
}

func TestClaimOfAFinishedRecord(t *testing.T) {
	t.Parallel()
	for _, exit := range []int{0, 1} {
		s, _ := newStore(t)
		rec := savePlan(t, s, "k1")
		a, err := s.Claim(rec, "apply", "mcp")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Finish(exit); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(rec, "apply", "mcp"); !errors.Is(err, ErrClaimed) {
			t.Errorf("Claim after Finish(%d) = %v, want ErrClaimed", exit, err)
		}
		if got := attemptDirs(t, recDir(s, rec)); len(got) != 0 {
			t.Errorf("attempt directories = %v", got)
		}
	}
}

func TestClaimOfAVanishedRecord(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	if err := os.RemoveAll(recDir(s, rec)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Claim(rec, "apply", "mcp")
	var ae *ArtifactError
	if !errors.As(err, &ae) || ae.What != "artifact" || ae.Detail != "is missing" {
		t.Errorf("Claim error = %v, want the artifact missing", err)
	}
	if exists(recDir(s, rec)) {
		t.Error("Claim created the record directory again")
	}
}

func TestFinish(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		exit  int
		state State
	}{
		{"exit 0", 0, StateApplied},
		{"exit 2", 2, StateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, clock := newStore(t)
			rec := savePlan(t, s, "k1")
			clock.Advance(time.Minute)
			started := clock.Now()
			a, err := s.Claim(rec, "apply", "mcp")
			if err != nil {
				t.Fatal(err)
			}
			clock.Advance(20 * time.Second)

			tampered, err := a.Finish(tt.exit)
			if err != nil || tampered {
				t.Fatalf("Finish = tampered %v, err %v", tampered, err)
			}
			out := readOutcome(t, s, rec)
			if out.Attempt != a.ID || out.ApplyTask != "apply" || out.Surface != "mcp" ||
				!out.Started.Equal(started) || !out.Finished.Equal(clock.Now()) ||
				out.Exit != tt.exit || out.State != tt.state.String() || out.Tampered {
				t.Errorf("outcome = %+v", out)
			}
			if got := s.StateOf(rec); got != tt.state {
				t.Errorf("state = %v, want %v", got, tt.state)
			}
			dir := recDir(s, rec)
			if exists(filepath.Join(dir, "artifact")) || exists(filepath.Join(dir, "summary")) {
				t.Error("Finish left the artifact or the summary")
			}
			if exists(a.Path) || len(attemptDirs(t, dir)) != 0 {
				t.Error("Finish left the private copy")
			}
			if exists(filepath.Join(dir, "outcome.json.tmp")) {
				t.Error("Finish left its temporary file")
			}

			info, ok := s.AttemptOf(rec)
			want := AttemptInfo{
				Plan: rec.ID, Attempt: a.ID, ApplyTask: "apply", Surface: "mcp",
				Started: started, Finished: clock.Now(), Exit: tt.exit, State: tt.state,
			}
			if !ok || !info.Started.Equal(want.Started) || !info.Finished.Equal(want.Finished) {
				t.Fatalf("AttemptOf = %+v, %v; want %+v", info, ok, want)
			}
			info.Started, info.Finished = want.Started, want.Finished
			if info != want {
				t.Errorf("AttemptOf = %+v, want %+v", info, want)
			}
		})
	}
}

func TestFinishTampered(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		posix  bool
		tamper func(t *testing.T, a *Attempt)
	}{
		{
			name:   "rewritten",
			tamper: func(t *testing.T, a *Attempt) { put(t, a.Path, strings.Repeat("e", len(planBytes))) },
		},
		{
			name:   "grown",
			tamper: func(t *testing.T, a *Attempt) { put(t, a.Path, planBytes+"x") },
		},
		{
			name: "removed",
			tamper: func(t *testing.T, a *Attempt) {
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "replaced by a symbolic link",
			posix: true,
			tamper: func(t *testing.T, a *Attempt) {
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source(t, planBytes), a.Path); err != nil {
					t.Fatal(err)
				}
			},
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
			a, err := s.Claim(rec, "apply", "mcp")
			if err != nil {
				t.Fatal(err)
			}
			tt.tamper(t, a)

			tampered, err := a.Finish(0)
			if err != nil || !tampered {
				t.Fatalf("Finish = tampered %v, err %v; want tampered", tampered, err)
			}
			out := readOutcome(t, s, rec)
			if out.State != "failed" || !out.Tampered || out.Exit != 0 {
				t.Errorf("outcome = %+v, want failed, tampered, exit 0", out)
			}
			if got := s.StateOf(rec); got != StateFailed {
				t.Errorf("state = %v, want failed", got)
			}
			if info, ok := s.AttemptOf(rec); !ok || !info.Tampered || info.State != StateFailed {
				t.Errorf("AttemptOf = %+v, %v", info, ok)
			}
			if len(attemptDirs(t, recDir(s, rec))) != 0 {
				t.Error("Finish left the private copy")
			}
		})
	}
}

func TestFinishRemovesALockedDownCopy(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	a, err := s.Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(a.Path), 0o500); err != nil {
		t.Fatal(err)
	}
	if tampered, err := a.Finish(0); err != nil || tampered {
		t.Fatalf("Finish = tampered %v, err %v", tampered, err)
	}
	if len(attemptDirs(t, recDir(s, rec))) != 0 {
		t.Error("Finish left a copy whose directory was read-only")
	}
}

func TestFinishTwiceKeepsTheFirstOutcome(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	a, err := s.Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if tampered, err := a.Finish(0); err != nil || tampered {
		t.Fatalf("Finish = tampered %v, err %v", tampered, err)
	}
	if tampered, err := a.Finish(1); err != nil || tampered {
		t.Errorf("second Finish = tampered %v, err %v; want the first result", tampered, err)
	}
	if out := readOutcome(t, s, rec); out.State != "applied" || out.Tampered || out.Exit != 0 {
		t.Errorf("a second Finish changed the outcome: %+v", out)
	}
}

func TestFinishReportsAnOutcomeItCannotWrite(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	a, err := s.Claim(rec, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(recDir(s, rec), "outcome.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Finish(0); err == nil {
		t.Error("Finish returned no error for an outcome it could not write")
	}
	dir := recDir(s, rec)
	if exists(filepath.Join(dir, "artifact")) || exists(filepath.Join(dir, "summary")) || len(attemptDirs(t, dir)) != 0 {
		t.Error("Finish left plan bytes behind")
	}
	if got := s.StateOf(rec); got != StateUnfinished {
		t.Errorf("state = %v, want unfinished", got)
	}
}

func TestAttemptOfUnclaimed(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k1")
	if info, ok := s.AttemptOf(rec); ok {
		t.Errorf("AttemptOf an unclaimed record = %+v", info)
	}
}

func TestAttemptOfReadsWhatIsThere(t *testing.T) {
	t.Parallel()
	claimed := time.Date(2026, 11, 17, 10, 0, 1, 0, time.UTC)
	touched := time.Date(2026, 11, 16, 8, 30, 0, 0, time.UTC)
	tests := []struct {
		name  string
		files map[string]string
		want  AttemptInfo
	}{
		{
			name:  "claim",
			files: map[string]string{"claim": claimJSON},
			want:  AttemptInfo{Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: claimed, State: StateUnfinished},
		},
		{
			name:  "unreadable claim",
			files: map[string]string{"claim": "{broken"},
			want:  AttemptInfo{Started: touched, State: StateUnfinished},
		},
		{
			name:  "claim without a start",
			files: map[string]string{"claim": `{"attempt":"00000000000000aa"}`},
			want:  AttemptInfo{Started: touched, State: StateUnfinished},
		},
		{
			name:  "claim and unreadable outcome",
			files: map[string]string{"claim": claimJSON, "outcome.json": "{broken"},
			want:  AttemptInfo{Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: claimed, State: StateUnfinished},
		},
		{
			name:  "unreadable outcome alone",
			files: map[string]string{"outcome.json": "{broken"},
			want:  AttemptInfo{Started: touched, State: StateUnfinished},
		},
		{
			name:  "failed outcome beside an unreadable claim",
			files: map[string]string{"claim": "", "outcome.json": failedJSON},
			want: AttemptInfo{
				Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: claimed,
				Finished: claimed.Add(4 * time.Second), Exit: 3, State: StateFailed, Tampered: true,
			},
		},
		{
			name:  "applied outcome alone",
			files: map[string]string{"outcome.json": appliedJSON},
			want: AttemptInfo{
				Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: claimed,
				Finished: claimed.Add(4 * time.Second), State: StateApplied,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newStore(t)
			rec := savePlan(t, s, "k1")
			for name, content := range tt.files {
				path := filepath.Join(recDir(s, rec), name)
				put(t, path, content)
				if err := os.Chtimes(path, touched, touched); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := s.AttemptOf(rec)
			if !ok {
				t.Fatal("AttemptOf found no attempt")
			}
			want := tt.want
			want.Plan = rec.ID
			if !got.Started.Equal(want.Started) || !got.Finished.Equal(want.Finished) {
				t.Errorf("times = %v, %v; want %v, %v", got.Started, got.Finished, want.Started, want.Finished)
			}
			got.Started, got.Finished = want.Started, want.Finished
			if got != want {
				t.Errorf("AttemptOf = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLastAttempt(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	if _, ok := s.LastAttempt("k1", ""); ok {
		t.Fatal("LastAttempt on an empty store found an attempt")
	}

	first := savePlan(t, s, "k1")
	if _, ok := s.LastAttempt("k1", ""); ok {
		t.Error("LastAttempt found an attempt though no record was claimed")
	}
	clock.Advance(time.Minute)
	firstStart := clock.Now()
	a1, err := s.Claim(first, "apply", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := a1.Finish(4); err != nil {
		t.Fatal(err)
	}
	firstEnd := clock.Now()

	clock.Advance(time.Minute)
	second := savePlan(t, s, "k1")
	clock.Advance(time.Minute)
	secondStart := clock.Now()
	a2, err := s.Claim(second, "deploy", "cli")
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Minute)
	third := savePlan(t, s, "k1")
	other := savePlan(t, s, "k2")

	got, ok := s.LastAttempt("k1", third.ID)
	want := AttemptInfo{Plan: second.ID, Attempt: a2.ID, ApplyTask: "deploy", Surface: "cli", Started: secondStart, State: StateUnfinished}
	if !ok || !got.Started.Equal(secondStart) || !got.Finished.IsZero() {
		t.Fatalf("LastAttempt = %+v, %v; want %+v", got, ok, want)
	}
	got.Started = want.Started
	if got != want {
		t.Errorf("LastAttempt = %+v, want %+v", got, want)
	}

	got, ok = s.LastAttempt("k1", second.ID)
	want = AttemptInfo{Plan: first.ID, Attempt: a1.ID, ApplyTask: "apply", Surface: "mcp", Exit: 4, State: StateFailed}
	if !ok || !got.Started.Equal(firstStart) || !got.Finished.Equal(firstEnd) {
		t.Fatalf("LastAttempt = %+v, %v; want started %v, finished %v", got, ok, firstStart, firstEnd)
	}
	got.Started, got.Finished = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("LastAttempt = %+v, want %+v", got, want)
	}

	if got, ok := s.LastAttempt("k1", ""); !ok || got.Plan != second.ID {
		t.Errorf("LastAttempt without an exception = %+v, %v; want plan %s", got, ok, second.ID)
	}
	if _, ok := s.LastAttempt("k2", ""); ok {
		t.Error("LastAttempt found an attempt for a key never claimed")
	}
	if _, ok := s.LastAttempt("k2", other.ID); ok {
		t.Error("LastAttempt found an attempt for a key never claimed")
	}
	if _, ok := s.LastAttempt("k3", ""); ok {
		t.Error("LastAttempt found an attempt for an unknown key")
	}
}

func TestLastAttemptTakesStartedFromTheClaimFile(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	rec := savePlan(t, s, "k1")
	if _, err := s.Claim(rec, "apply", "mcp"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(recDir(s, rec), "claim")
	put(t, path, "not json")
	touched := clock.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	got, ok := s.LastAttempt("k1", "")
	if !ok || got.State != StateUnfinished || got.Plan != rec.ID {
		t.Fatalf("LastAttempt = %+v, %v", got, ok)
	}
	if !got.Started.Equal(touched) {
		t.Errorf("Started = %v, want the claim file's modification time %v", got.Started, touched)
	}
	if got.Attempt != "" {
		t.Errorf("Attempt = %q, want nothing from an unreadable claim", got.Attempt)
	}
}

func TestLoadAttempt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if a := loadAttempt(dir); a.claimed || a.finished || !a.started.IsZero() {
		t.Errorf("loadAttempt of an unclaimed directory = %+v", a)
	}
	if _, ok := loadAttempt(dir).info("p"); ok {
		t.Error("an unclaimed directory has an attempt")
	}
	put(t, filepath.Join(dir, "claim"), claimJSON)
	a := loadAttempt(dir)
	if !a.claimed || a.finished || a.claim.Attempt != "00000000000000aa" || !a.started.Equal(a.claim.Started) {
		t.Errorf("loadAttempt of a claimed directory = %+v", a)
	}
	put(t, filepath.Join(dir, "outcome.json"), failedJSON)
	a = loadAttempt(dir)
	if !a.finished || a.state != StateFailed || a.outcome.Exit != 3 || !a.outcome.Tampered {
		t.Errorf("loadAttempt of a finished directory = %+v", a)
	}
}

func TestOutcomeState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want State
		ok   bool
	}{
		{"applied", StateApplied, true},
		{"failed", StateFailed, true},
		{"unfinished", StateUnfinished, false},
		{"ready", StateUnfinished, false},
		{"", StateUnfinished, false},
	}
	for _, tt := range tests {
		if got, ok := outcomeState(tt.text); got != tt.want || ok != tt.ok {
			t.Errorf("outcomeState(%q) = %v, %v; want %v, %v", tt.text, got, ok, tt.want, tt.ok)
		}
	}
}

func TestLastEvent(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		a    attemptFiles
		want time.Time
	}{
		{"unclaimed", attemptFiles{}, t0},
		{"claimed", attemptFiles{started: t0.Add(time.Hour)}, t0.Add(time.Hour)},
		{"finished", attemptFiles{started: t0.Add(time.Hour), outcome: outcomeFile{Finished: t0.Add(2 * time.Hour)}}, t0.Add(2 * time.Hour)},
		{"claim file older than the record", attemptFiles{started: t0.Add(-time.Hour)}, t0},
	}
	for _, tt := range tests {
		if got := tt.a.lastEvent(t0); !got.Equal(tt.want) {
			t.Errorf("%s: lastEvent = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCopyArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "artifact")
	put(t, src, planBytes)
	copyDir := filepath.Join(dir, "attempt-00000000000000aa")
	if err := copyArtifact(src, copyDir, digestOf(planBytes)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(copyDir, "plan")); got != planBytes {
		t.Errorf("copy = %q", got)
	}

	var ae *ArtifactError
	if err := copyArtifact(src, copyDir, digestOf(planBytes)); err == nil || errors.As(err, &ae) {
		t.Errorf("copyArtifact into an existing directory = %v, want the system's error", err)
	}
	if err := copyArtifact(src, filepath.Join(dir, "absent", "attempt-00000000000000aa"), digestOf(planBytes)); !errors.As(err, &ae) || ae.Detail != "is missing" {
		t.Errorf("copyArtifact into a missing record = %v", err)
	}
	if err := copyArtifact(src, filepath.Join(dir, "attempt-00000000000000bb"), digestOf("other")); !errors.As(err, &ae) || ae.Detail != detailDigest {
		t.Errorf("copyArtifact of changed bytes = %v", err)
	}
}

func TestWriteClaim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "claim")
	claim := claimFile{Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: time.Date(2026, 11, 17, 10, 0, 1, 0, time.UTC)}
	if err := writeClaim(path, claim); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != claimJSON+"\n" {
		t.Errorf("claim = %q, want %q", got, claimJSON+"\n")
	}
	if err := writeClaim(path, claim); !errors.Is(err, ErrClaimed) {
		t.Errorf("second writeClaim = %v, want ErrClaimed", err)
	}
	if err := writeClaim(filepath.Join(dir, "absent", "claim"), claim); err == nil || errors.Is(err, ErrClaimed) {
		t.Errorf("writeClaim into a missing directory = %v", err)
	}
	other := filepath.Join(dir, "other")
	claim.Started = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeClaim(other, claim); err == nil || exists(other) {
		t.Errorf("writeClaim of a time JSON cannot hold = %v, file exists %v", err, exists(other))
	}
}

// A Save can mark a record as replaced after Claim read it as ready, while
// the artifact is copied. The claim is withdrawn, so the replaced plan is not
// applied and is not left claimed.
func TestTakeClaimWithdrawsFromAReplacedRecord(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := savePlan(t, s, "k")
	dir := recDir(s, rec)
	claim := claimFile{Attempt: "00000000000000aa", ApplyTask: "apply", Surface: "mcp", Started: s.now()}
	put(t, filepath.Join(dir, "replaced"), "")
	if err := takeClaim(dir, claim); !errors.Is(err, ErrReplaced) {
		t.Fatalf("takeClaim of a replaced record = %v, want ErrReplaced", err)
	}
	if exists(filepath.Join(dir, "claim")) {
		t.Error("the withdrawn claim was left behind")
	}
	if got := s.StateOf(rec); got != StateReplaced {
		t.Errorf("state = %v, want replaced", got)
	}

	ready := savePlan(t, s, "other")
	if err := takeClaim(recDir(s, ready), claim); err != nil {
		t.Fatalf("takeClaim of a ready record: %v", err)
	}
	if err := takeClaim(recDir(s, ready), claim); !errors.Is(err, ErrClaimed) {
		t.Errorf("second takeClaim = %v, want ErrClaimed", err)
	}
}

// Claim and a Save of the same key run beside each other: the old plan is
// either applied or reported as replaced, never both.
func TestClaimBesideSaveNeverAppliesAReplacedPlan(t *testing.T) {
	t.Parallel()
	artifact := strings.Repeat("x", 4<<20)
	for range 20 {
		s, clock := newStore(t)
		old, _, err := s.Save(saveInput(t, "k", artifact, summaryBytes))
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Second)
		next := saveInput(t, "k", planBytes, summaryBytes)
		var (
			wg       sync.WaitGroup
			claimErr error
			replaced []Record
		)
		wg.Go(func() { _, claimErr = s.Claim(old, "apply", "mcp") })
		wg.Go(func() { _, replaced, _ = s.Save(next) })
		wg.Wait()
		marked := slices.ContainsFunc(replaced, func(r Record) bool { return r.ID == old.ID })
		if claimErr == nil && marked {
			t.Fatal("a plan was claimed and reported as replaced")
		}
		if claimErr != nil && !errors.Is(claimErr, ErrReplaced) && !errors.As(claimErr, new(*ArtifactError)) {
			t.Fatalf("Claim beside a Save = %v", claimErr)
		}
	}
}

func TestFill(t *testing.T) {
	t.Parallel()
	s, clock := newStore(t)
	in := saveInput(t, "k1", planBytes, summaryBytes)
	dir := t.TempDir()
	rec, err := s.fill(in, "00000000000000ab", dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "00000000000000ab" || !rec.Created.Equal(clock.Now()) || rec.Artifact != digestOf(planBytes) || rec.Summary != digestOf(summaryBytes) {
		t.Errorf("fill = %+v", rec)
	}
	for _, name := range []string{"artifact", "summary", "record.json"} {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("fill did not write %s", name)
		}
	}

	blocked := t.TempDir()
	if err := os.Mkdir(filepath.Join(blocked, "record.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fill(in, "00000000000000ab", blocked); err == nil {
		t.Error("fill returned no error for a record it could not write")
	}
}
