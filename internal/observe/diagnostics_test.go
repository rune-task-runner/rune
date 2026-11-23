package observe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCountDrop(t *testing.T) {
	s := newStore(t)
	first, later := testNow, testNow.Add(time.Hour)
	s.now = func() time.Time { return first }
	for _, reason := range []string{ReasonTooLong, ReasonHereDocument} {
		if err := s.CountDrop(reason); err != nil {
			t.Fatal(err)
		}
	}
	s.now = func() time.Time { return later }
	if err := s.CountDrop(ReasonTooLong); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDiagnostics(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Reasons[ReasonTooLong]; c.N != 2 || !c.Last.Equal(later.UTC().Truncate(time.Second)) {
		t.Errorf("too-long = %+v", c)
	}
	if c := d.Reasons[ReasonHereDocument]; c.N != 1 || !c.Last.Equal(first.UTC().Truncate(time.Second)) {
		t.Errorf("here-document = %+v", c)
	}
	// The file holds reason names, counts and times, and nothing else.
	data, err := os.ReadFile(filepath.Join(s.Dir(), diagnosticsFile))
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]map[string]map[string]any
	if err := json.Unmarshal(data, &shape); err != nil || len(shape) != 1 {
		t.Fatalf("%s: %v", data, err)
	}
	for reason, fields := range shape["reasons"] {
		if len(fields) != 2 || fields["n"] == nil || fields["last"] == nil {
			t.Errorf("%s holds %v", reason, fields)
		}
	}
}

func TestCountShared(t *testing.T) {
	base := filepath.Join(t.TempDir(), "rune", "observe")
	if err := CountShared(base, ReasonNoProject, testNow); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDiagnostics(base)
	if err != nil || d.Reasons[ReasonNoProject].N != 1 {
		t.Errorf("%+v %v", d, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CountShared(filepath.Join(file, "observe"), ReasonNoProject, testNow); err == nil {
		t.Error("counted under a file")
	}
}

func TestReadDiagnostics(t *testing.T) {
	dir := t.TempDir()
	if d, err := ReadDiagnostics(dir); err != nil || d.Reasons == nil || len(d.Reasons) != 0 {
		t.Errorf("a missing file: %+v %v", d, err)
	}
	path := filepath.Join(dir, diagnosticsFile)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := ReadDiagnostics(dir); err != nil || d.Reasons == nil {
		t.Errorf("no reasons: %+v %v", d, err)
	}
	if err := os.WriteFile(path, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := ReadDiagnostics(dir); err == nil || d.Reasons == nil {
		t.Errorf("a damaged file: %+v %v", d, err)
	}
	// Counting starts the damaged file again.
	if err := addCount(dir, ReasonTooLong, testNow); err != nil {
		t.Fatal(err)
	}
	if d, err := ReadDiagnostics(dir); err != nil || d.Reasons[ReasonTooLong].N != 1 {
		t.Errorf("after counting: %+v %v", d, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiagnostics(dir); err == nil {
		t.Error("read a directory")
	}
}

func TestDiagnosticsMerge(t *testing.T) {
	a := Diagnostics{Reasons: map[string]Count{"x": {2, testNow}, "y": {1, testNow}}}
	b := Diagnostics{Reasons: map[string]Count{"x": {3, testNow.Add(time.Hour)}}}
	got := a.Merge(b)
	if got.Reasons["x"].N != 5 || !got.Reasons["x"].Last.Equal(testNow.Add(time.Hour)) || got.Reasons["y"].N != 1 {
		t.Errorf("%+v", got)
	}
	if got := (Diagnostics{}).Merge(Diagnostics{}); got.Reasons == nil || len(got.Reasons) != 0 {
		t.Errorf("empty: %+v", got)
	}
}
