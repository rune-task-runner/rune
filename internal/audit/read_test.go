package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustAppend(t *testing.T, l *Log, start time.Time, rec Record) {
	t.Helper()
	if err := l.Append(start, rec, false); err != nil {
		t.Fatal(err)
	}
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func ids(actions []Action) string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.ID
	}
	return strings.Join(out, ",")
}

func TestReadPairsRecords(t *testing.T) {
	l := openLog(t, Options{})
	at := testDay
	start := callStart("aa", at)
	start.Session = "s1"
	start.Client = &Client{Name: "claude-code", Version: "2.1.0"}
	start.Arguments = &Arguments{Supplied: []byte(`{"target":"release"}`)}
	mustAppend(t, l, at, start)
	end := callEnd("aa", at.Add(2*time.Second), OutcomeFailed, 3)
	end.Arguments = &Arguments{Effective: []byte(`{"target":"release"}`)}
	end.Error = &Error{Kind: "internal", Message: "boom"}
	end.Approval = &Approval{Asked: true, Decision: "approved", Tasks: []string{"build"}}
	end.Grants = []string{"clean"}
	end.Plan = &Plan{Role: "stored", ID: "p1", Digest: "sha256:ab"}
	mustAppend(t, l, at, end)
	mustAppend(t, l, at.Add(time.Second), callStart("bb", at.Add(time.Second)))
	mustAppend(t, l, at.Add(3*time.Second), Record{Type: TypeRequest, ID: "cc", Time: at.Add(3 * time.Second), Method: "tools/list", Outcome: OutcomeOK})

	actions, damage, err := Read(l.Dir(), at, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(damage) != 0 {
		t.Errorf("damage: %v", damage)
	}
	if got := ids(actions); got != "aa,bb,cc" {
		t.Fatalf("actions %s", got)
	}
	a := actions[0]
	if a.SchemaVersion != 1 || a.Type != TypeCall || !a.Start.Equal(at) || a.End == nil || !a.End.Equal(at.Add(2*time.Second)) {
		t.Errorf("times or version: %+v", a)
	}
	if a.Outcome != OutcomeFailed || a.ExitCode == nil || *a.ExitCode != 3 || a.DurationMs == nil {
		t.Errorf("outcome: %+v", a)
	}
	if a.Client == nil || !a.Client.Reported || a.Client.Name != "claude-code" {
		t.Errorf("client: %+v", a.Client)
	}
	if a.Arguments == nil || string(a.Arguments.Supplied) != `{"target":"release"}` || string(a.Arguments.Effective) != `{"target":"release"}` {
		t.Errorf("arguments: %+v", a.Arguments)
	}
	if a.Error == nil || a.Approval == nil || a.Plan == nil || len(a.Grants) != 1 || a.Session != "s1" {
		t.Errorf("end fields: %+v", a)
	}
	b := actions[1]
	if b.Outcome != OutcomeIncomplete || b.End != nil || b.ExitCode != nil || b.DurationMs != nil {
		t.Errorf("a start without an end: %+v", b)
	}
	if c := actions[2]; c.Type != TypeRequest || c.Method != "tools/list" || c.Outcome != OutcomeOK || c.End != nil {
		t.Errorf("a request: %+v", c)
	}
}

func TestReadEffectiveWithoutSupplied(t *testing.T) {
	l := openLog(t, Options{})
	mustAppend(t, l, testDay, callStart("aa", testDay))
	end := callEnd("aa", testDay, OutcomeCompleted, 0)
	end.Arguments = &Arguments{Effective: []byte(`{"target":"debug"}`)}
	mustAppend(t, l, testDay, end)
	actions, _, err := Read(l.Dir(), testDay, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if args := actions[0].Arguments; args == nil || args.Supplied != nil || string(args.Effective) != `{"target":"debug"}` {
		t.Errorf("arguments: %+v", args)
	}
}

func TestReadSkipsDamage(t *testing.T) {
	l := openLog(t, Options{})
	path := dayPath(l, testDay)
	mustAppend(t, l, testDay, callStart("aa", testDay))
	mustAppend(t, l, testDay, callEnd("aa", testDay, OutcomeCompleted, 0))
	at := testDay.Format(time.RFC3339)
	appendRaw(t, path, "not json\n")
	appendRaw(t, path, `{"v":2,"type":"call","phase":"start","id":"v2","time":"`+at+`"}`+"\n")
	appendRaw(t, path, `{"v":1,"phase":"start","id":"notype","time":"`+at+`"}`+"\n")
	appendRaw(t, path, `{"v":1,"type":"call","phase":"start","time":"`+at+`"}`+"\n")
	appendRaw(t, path, `{"v":1,"type":"call","phase":"start","id":"notime"}`+"\n")
	appendRaw(t, path, `{"v":1,"type":"call","phase":"end","id":"orphan","time":"`+at+`"}`+"\n")
	appendRaw(t, path, `{"v":1,"type":"call","phase":"start","id":"aa","time":"`+at+`"}`+"\n")
	appendRaw(t, path, `{"v":1,"type":"call","phase":"end","id":"aa","time":"`+at+`"}`+"\n")
	appendRaw(t, path, strings.Repeat("x", maxLineBytes+10)+"\n")
	appendRaw(t, path, "\n")
	mustAppend(t, l, testDay, callStart("zz", testDay.Add(time.Minute)))
	appendRaw(t, path, `{"v":1,"type":"call","pha`)

	actions, damage, err := Read(l.Dir(), testDay, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(actions); got != "aa,zz" {
		t.Errorf("actions %s", got)
	}
	if len(damage) != 1 || damage[0].File != path || damage[0].Count != 10 {
		t.Errorf("damage %+v, want 10 in %s", damage, path)
	}
	if actions[0].Outcome != OutcomeCompleted {
		t.Errorf("the repeated end changed the action: %+v", actions[0])
	}
}

func TestReadUnknownTypeIsKept(t *testing.T) {
	l := openLog(t, Options{})
	appendRaw(t, dayPath(l, testDay), `{"v":1,"type":"future","id":"ff","time":"`+testDay.Format(time.RFC3339)+`"}`+"\n")
	actions, damage, err := Read(l.Dir(), testDay, testDay)
	if err != nil || len(damage) != 0 || len(actions) != 1 || actions[0].Type != "future" {
		t.Fatalf("actions %+v, damage %v, err %v", actions, damage, err)
	}
}

func TestReadRange(t *testing.T) {
	l := openLog(t, Options{})
	days := []time.Time{
		time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	}
	for i, day := range days {
		mustAppend(t, l, day, callStart(fmt.Sprintf("d%d", i), day))
	}
	if err := os.WriteFile(filepath.Join(l.Dir(), "notes.jsonl"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		from, to time.Time
		want     string
	}{
		{"one day", days[1], days[1], "d1"},
		{"two days", days[1], days[2], "d1,d2"},
		{"open start", time.Time{}, days[1], "d0,d1"},
		{"open end", days[1], time.Time{}, "d1,d2"},
		{"everything", time.Time{}, time.Time{}, "d0,d1,d2"},
		{"a zone ahead of UTC reads the UTC day", time.Date(2026, 11, 18, 1, 0, 0, 0, time.FixedZone("x", 2*3600)), time.Date(2026, 11, 18, 1, 30, 0, 0, time.FixedZone("x", 2*3600)), "d1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions, _, err := Read(l.Dir(), tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(actions); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestReadSortsAcrossFiles(t *testing.T) {
	l := openLog(t, Options{})
	late := time.Date(2026, 11, 17, 23, 0, 0, 0, time.UTC)
	early := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	mustAppend(t, l, late, callStart("late", late))
	mustAppend(t, l, early, callStart("early", early))
	actions, _, err := Read(l.Dir(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(actions); got != "early,late" {
		t.Errorf("got %s", got)
	}
}

func TestReadMissingDirectory(t *testing.T) {
	actions, damage, err := Read(filepath.Join(t.TempDir(), "missing"), time.Time{}, time.Time{})
	if err != nil || actions != nil || damage != nil {
		t.Fatalf("actions %v, damage %v, err %v", actions, damage, err)
	}
}

func TestReadErrors(t *testing.T) {
	t.Run("the directory is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Read(file, time.Time{}, time.Time{}); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("a day file is a directory", func(t *testing.T) {
		l := openLog(t, Options{})
		if err := os.Mkdir(dayPath(l, testDay), 0o700); err != nil {
			t.Fatal(err)
		}
		_, _, err := Read(l.Dir(), time.Time{}, time.Time{})
		if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
			t.Fatalf("got %v", err)
		}
	})
}

// SC-007: a day of 10,000 actions is read and filtered in under 2 seconds.
func TestReadTenThousandActions(t *testing.T) {
	l := openLog(t, Options{})
	var b strings.Builder
	for i := range 10000 {
		at := testDay.Add(time.Duration(i) * time.Millisecond)
		id := fmt.Sprintf("%016d", i)
		for _, rec := range []Record{callStart(id, at), callEnd(id, at, OutcomeCompleted, 0)} {
			line, err := rec.Encode()
			if err != nil {
				t.Fatal(err)
			}
			b.Write(line)
		}
	}
	if err := os.WriteFile(dayPath(l, testDay), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	actions, damage, err := Read(l.Dir(), testDay, testDay)
	if err != nil || len(damage) != 0 {
		t.Fatalf("damage %v, err %v", damage, err)
	}
	filter := Filter{Outcomes: []string{OutcomeCompleted}}
	matched := 0
	for _, a := range actions {
		if filter.Match(a) {
			matched++
		}
	}
	took := time.Since(begin)
	t.Logf("read and filtered %d actions in %s", matched, took)
	if matched != 10000 {
		t.Errorf("%d actions, want 10000", matched)
	}
	if took > 2*time.Second && !raceEnabled {
		t.Errorf("took %s, want under 2s", took)
	}
}
