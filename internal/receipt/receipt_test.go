package receipt

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewID(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 15, 0, 0, time.FixedZone("x", 3600))
	id := NewID(now)
	if !regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("id %q has the wrong form", id)
	}
	if !strings.HasPrefix(id, "20261125T091500Z-") {
		t.Fatalf("id %q does not start with the UTC time", id)
	}
	seen := map[string]bool{}
	for range 1000 {
		id := NewID(now)
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func minimalReceipt() Receipt {
	return Receipt{
		Version:    Version,
		ID:         "20261125T101500Z-1a2b3c4d5e6f7a8b",
		Invocation: "9f8e7d6c5b4a3921",
		Task:       "test",
		Role:       RoleTask,
		Status:     StatusPassed,
		StartedAt:  time.Date(2026, 11, 25, 10, 15, 0, 1, time.UTC),
		FinishedAt: time.Date(2026, 11, 25, 10, 15, 0, 2, time.UTC),
		Rune:       RuneInfo{Version: "0.12.0", Commit: "abc", OS: "linux", Arch: "amd64"},
		Executor:   "sh",
		Definition: "d",
		Project:    "Runefile",
		Runefiles:  []RunefileEntry{{Path: "Runefile", SHA256: "rf", Covered: true}},
		Channel:    ChannelCLI,
	}
}

func TestReceiptJSONOmitsEmptyOptionalFields(t *testing.T) {
	data, err := Statement(minimalReceipt())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, absent := range []string{
		"parentInvocation", "argumentsRedacted", "overrides", "related", "exitCode", "source",
		"uncoveredInputs", "outputs", "claims",
	} {
		if strings.Contains(got, `"`+absent+`"`) {
			t.Errorf("%s should be omitted when empty: %s", absent, got)
		}
	}
	if !strings.Contains(got, `"arguments":[]`) {
		t.Errorf("arguments should be an empty list, got %s", got)
	}
}

func TestReceiptJSONFieldOrder(t *testing.T) {
	r := minimalReceipt()
	r.ParentInvocation = "p"
	r.Arguments = []Arg{{Name: "a", Values: []string{"1"}}}
	r.ArgumentsRedacted = true
	r.Overrides = []string{"a", "b"}
	r.Related = &Related{Dependencies: []string{"d"}, Follows: "f", FailureOf: "x", Reuses: "r"}
	code := 0
	r.ExitCode = &code
	r.Source = &Source{Commit: "c", Before: "b", After: "a"}
	r.UncoveredInputs = []string{"u"}
	r.Outputs = &Outputs{Count: 1, Digest: "g", Files: []OutputFile{{Path: "o", Size: 1, SHA256: "s"}}}
	r.Claims = &Claims{Client: &Client{Name: "n", Version: "v"}, Auth: "process", Env: map[string]string{"CI": "true"}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		"version", "id", "invocation", "parentInvocation", "task", "arguments", "argumentsRedacted",
		"overrides", "role", "related", "status", "exitCode", "startedAt", "finishedAt", "durationMs", "rune",
		"executor", "definition", "project", "runefiles", "source", "uncoveredInputs", "outputs",
		"channel", "claims",
	}
	if !strings.Contains(string(data), `"argumentsRedacted":true,"overrides":["a","b"],`) {
		t.Fatalf("overrides not right after argumentsRedacted: %s", data)
	}
	last := -1
	for _, key := range order {
		i := strings.Index(string(data), `"`+key+`":`)
		if i < 0 || i < last {
			t.Fatalf("field %s missing or out of order in %s", key, data)
		}
		last = i
	}
}

func TestStatementWithSource(t *testing.T) {
	r := minimalReceipt()
	r.Source = &Source{Before: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", After: "x"}
	data, err := Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Type          string `json:"_type"`
		Subject       []map[string]any
		PredicateType string `json:"predicateType"`
		Predicate     Receipt
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.Type != "https://in-toto.io/Statement/v1" || st.PredicateType != PredicateType {
		t.Fatalf("statement header wrong: %s", data)
	}
	if PredicateType != "https://rune-task-runner.github.io/rune/receipt/v1" {
		t.Fatalf("predicate type changed: %s", PredicateType)
	}
	want := `[{"digest":{"gitTree":"4b825dc642cb6eb9a060e54bf8d69288fbee4904"},"name":"worktree"}]`
	got, _ := json.Marshal(st.Subject)
	if string(got) != want {
		t.Fatalf("subject %s, want %s", got, want)
	}
	if st.Predicate.ID != r.ID {
		t.Fatalf("predicate lost: %s", data)
	}
}

func TestStatementWithoutSource(t *testing.T) {
	r := minimalReceipt()
	data, err := Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"subject":[{"name":"Runefile","digest":{"sha256":"rf"}}]`) {
		t.Fatalf("subject should be the Runefile digest: %s", data)
	}
}

func TestStatementWithoutProjectEntry(t *testing.T) {
	r := minimalReceipt()
	r.Runefiles = nil
	if _, err := Statement(r); err == nil {
		t.Fatal("a receipt with no source and no Runefile entry has no subject")
	}
}

func TestParseStatement(t *testing.T) {
	r := minimalReceipt()
	r.Overrides = []string{"mode"}
	data, err := Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseStatement(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r.ID || got.Task != r.Task || !got.StartedAt.Equal(r.StartedAt) || len(got.Overrides) != 1 || got.Overrides[0] != "mode" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	for name, bad := range map[string]string{
		"not json":        "{",
		"other type":      `{"_type":"x","predicateType":"` + PredicateType + `","predicate":{}}`,
		"other predicate": `{"_type":"https://in-toto.io/Statement/v1","predicateType":"x","predicate":{}}`,
		"no task":         `{"_type":"https://in-toto.io/Statement/v1","predicateType":"` + PredicateType + `","predicate":{"version":1}}`,
	} {
		if _, err := ParseStatement([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
