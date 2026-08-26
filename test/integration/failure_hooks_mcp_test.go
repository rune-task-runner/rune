package integration

import (
	"strings"
	"testing"
)

// Spec 022 US1 (quickstart Scenario C): a failing task with || hooks returns
// the task output, the exit marker, and the delimited fix-suggestion section
// in the SAME tool response; a succeeding task's response carries no section.

const failHookMCPRunefile = `test: || diagnose
    @echo "running tests"
    @exit 7

pass:
    @echo "all good"

diagnose:
    @echo "SUGGESTION: check the fixtures"
`

func TestFailureHooks_MCPFixSuggestionInToolResult(t *testing.T) {
	dir := writeRunefile(t, failHookMCPRunefile)
	result, _ := mcpCall(t, dir, "test")

	for _, want := range []string{
		"running tests",
		"[fix suggestion - from Runefile || failure hooks]",
		"SUGGESTION: check the fixtures",
		`\"isError\":true`,
	} {
		// The tool result arrives JSON-encoded inside the JSON-RPC frame.
		if !strings.Contains(result, want) && !strings.Contains(result, strings.ReplaceAll(want, `\"`, `"`)) {
			t.Errorf("tool result missing %q:\n%s", want, result)
		}
	}
	if !strings.Contains(result, "[exit ") {
		t.Errorf("tool result missing exit marker:\n%s", result)
	}
	// No ANSI escapes may reach the agent.
	if strings.Contains(result, "\\u001b") || strings.Contains(result, "\x1b") {
		t.Errorf("ANSI escape leaked into tool result:\n%s", result)
	}
}

func TestFailureHooks_MCPSuccessHasNoSuggestionSection(t *testing.T) {
	dir := writeRunefile(t, failHookMCPRunefile)
	result, _ := mcpCall(t, dir, "pass")

	if strings.Contains(result, "fix suggestion") {
		t.Errorf("succeeding task must not carry a suggestion section:\n%s", result)
	}
	if !strings.Contains(result, "all good") {
		t.Errorf("task output missing:\n%s", result)
	}
}
