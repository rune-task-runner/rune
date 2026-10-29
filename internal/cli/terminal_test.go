package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isTerminal must say no to everything that is not a terminal-backed file:
// a [confirm] answer may only come from a person at a terminal (spec 027).
func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	regular, err := os.Create(filepath.Join(t.TempDir(), "answer.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = regular.Close() })

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	cases := map[string]any{
		"nil":          nil,
		"reader":       strings.NewReader("y\n"),
		"regular file": regular,
		"pipe":         pr,
	}
	for name, stream := range cases {
		if isTerminal(stream) {
			t.Errorf("isTerminal(%s) = true, want false", name)
		}
	}
}
