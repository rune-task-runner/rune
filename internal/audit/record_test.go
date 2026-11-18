package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecordEncode(t *testing.T) {
	exit := 3
	ms := int64(12)
	rec := Record{
		Type:       TypeCall,
		Phase:      PhaseEnd,
		ID:         "0123456789abcdef",
		Time:       time.Date(2026, 11, 18, 8, 5, 2, 114, time.UTC),
		DurationMs: &ms,
		Outcome:    OutcomeFailed,
		ExitCode:   &exit,
		Arguments:  &Arguments{Effective: json.RawMessage(`{"q":"a<b>&c"}`)},
		Sandbox:    &Sandbox{},
	}
	line, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(line, []byte("\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("not one line: %q", line)
	}
	text := string(line)
	for _, want := range []string{
		`"v":1`, `"type":"call"`, `"phase":"end"`, `"id":"0123456789abcdef"`,
		`"time":"2026-11-18T08:05:02.000000114Z"`, `"durationMs":12`,
		`"outcome":"failed"`, `"exitCode":3`, `"a<b>&c"`, `"sandbox":{"enforced":false}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("line lacks %s: %s", want, text)
		}
	}
	for _, absent := range []string{"session", "client", "error", "approval", "plan", "project", "remote", "suppressed", "supplied"} {
		if strings.Contains(text, `"`+absent+`"`) {
			t.Errorf("line holds the unset field %q: %s", absent, text)
		}
	}
}

func TestRecordEncodeBounds(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("x", MaxRecordBytes) + `"`)
	t.Run("supplied", func(t *testing.T) {
		rec := Record{Type: TypeCall, ID: "a", Time: time.Unix(1, 0), Arguments: &Arguments{Supplied: big}}
		line, err := rec.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if len(line) > MaxRecordBytes {
			t.Fatalf("line of %d bytes", len(line))
		}
		if !strings.Contains(string(line), `"supplied":"[65538 bytes not stored]"`) {
			t.Errorf("no mark in %s", line)
		}
		if len(rec.Arguments.Supplied) != len(big) {
			t.Error("Encode changed the caller's record")
		}
	})
	t.Run("effective too", func(t *testing.T) {
		rec := Record{Type: TypeCall, ID: "a", Time: time.Unix(1, 0), Arguments: &Arguments{Supplied: big, Effective: big}}
		line, err := rec.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if len(line) > MaxRecordBytes {
			t.Fatalf("line of %d bytes", len(line))
		}
		if !strings.Contains(string(line), `"effective":"[65538 bytes not stored]"`) {
			t.Errorf("no mark in %s", line)
		}
	})
	t.Run("small record is kept", func(t *testing.T) {
		rec := Record{Type: TypeCall, ID: "a", Time: time.Unix(1, 0), Arguments: &Arguments{Supplied: json.RawMessage(`{"a":1}`)}}
		line, err := rec.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(line), `"supplied":{"a":1}`) {
			t.Errorf("arguments changed: %s", line)
		}
	})
}

func TestRecordEncodeError(t *testing.T) {
	rec := Record{Type: TypeCall, ID: "a", Time: time.Unix(1, 0), Arguments: &Arguments{Supplied: json.RawMessage(`{`)}}
	if _, err := rec.Encode(); err == nil {
		t.Fatal("no error for arguments that are not JSON")
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Fatalf("id %q is not 16 hex digits", id)
		}
		if seen[id] {
			t.Fatalf("id %q repeated", id)
		}
		seen[id] = true
	}
}

// The sandbox object states the backend and the mode when a server has them
// (spec 053), and stays as it was for a record that has neither.
func TestSandboxFields(t *testing.T) {
	for _, c := range []struct {
		sandbox Sandbox
		want    string
	}{
		{Sandbox{}, `"sandbox":{"enforced":false}`},
		{Sandbox{Mode: "off"}, `"sandbox":{"enforced":false,"mode":"off"}`},
		{Sandbox{Mode: "enforce"}, `"sandbox":{"enforced":false,"mode":"enforce"}`},
		{Sandbox{Enforced: true, Backend: "bubblewrap", Mode: "enforce"}, `"sandbox":{"enforced":true,"backend":"bubblewrap","mode":"enforce"}`},
	} {
		line, err := Record{Type: TypeCall, Phase: PhaseStart, ID: "0123456789abcdef", Time: time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC), Sandbox: &c.sandbox}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(line), c.want) {
			t.Errorf("line lacks %s: %s", c.want, line)
		}
	}
}
