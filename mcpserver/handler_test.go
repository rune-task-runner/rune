package mcpserver

import "testing"

// Spec 022 (contracts/mcp-result.md): a non-empty FixSuggestion renders as a
// delimited section after the [exit N] line; an empty one leaves the legacy
// layout byte-identical.

func TestFormatResultWithFixSuggestion(t *testing.T) {
	r := Result{
		Stdout:        "running tests\n",
		Stderr:        "warning: failure hook x\n",
		ExitCode:      1,
		FixSuggestion: "SUGGESTION: check the fixtures",
	}
	got := formatResult(r)
	want := "running tests\n" +
		"warning: failure hook x\n" +
		"[exit 1]\n" +
		"[fix suggestion - from Runefile || failure hooks]\n" +
		"SUGGESTION: check the fixtures"
	if got != want {
		t.Errorf("formatResult:\n got %q\nwant %q", got, want)
	}
}

func TestFormatResultWithoutFixSuggestionLegacy(t *testing.T) {
	r := Result{Stdout: "ok\n", ExitCode: 0}
	if got, want := formatResult(r), "ok\n[exit 0]"; got != want {
		t.Errorf("legacy layout changed:\n got %q\nwant %q", got, want)
	}
	r = Result{Stdout: "boom\n", ExitCode: 1}
	if got, want := formatResult(r), "boom\n[exit 1]"; got != want {
		t.Errorf("legacy failure layout changed:\n got %q\nwant %q", got, want)
	}
}
