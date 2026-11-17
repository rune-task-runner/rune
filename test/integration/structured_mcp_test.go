package integration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// Spec 044: every tool declares one output schema, and a task run returns
// its exit code and streams as fields beside the unchanged text. These drive
// the real binary over stdio.

// structuredRunefile is the spec's quickstart Runefile.
const structuredRunefile = `# Runs the tests.
test: || hint
    @echo running tests
    @echo "FAIL: fixtures out of date" >&2
    @exit 1

hint:
    @echo "re-run with -update"

# Deletes things.
[confirm]
clean:
    @echo cleaned
`

// structuredSchema is the output schema of contracts/mcp-results.md.
const structuredSchema = `{
  "type": "object",
  "properties": {
    "exitCode": {
      "type": "integer",
      "description": "Exit status of the run: 0 on success. The same number as the [exit N] line of the text result."
    },
    "stdout": {
      "type": "string",
      "description": "What the run wrote to standard output, with secrets masked. Output over the server's limit keeps its beginning and its end around a [truncated N bytes] line."
    },
    "stderr": {
      "type": "string",
      "description": "What the run wrote to standard error, with secrets masked. Output over the server's limit keeps its beginning and its end around a [truncated N bytes] line."
    },
    "fixSuggestion": {
      "type": "string",
      "description": "Output of the task's || failure hooks. Present only when a failed run produced one."
    }
  },
  "required": ["exitCode", "stdout", "stderr"],
  "additionalProperties": false
}`

// rawResult is a tools/call result with its structured content left as JSON,
// so a missing key and an empty value stay apart.
type rawResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
}

// callRaw calls a tool with arguments (a JSON object) and decodes the result.
func (p *mcpProc) callRaw(tool, arguments string) rawResult {
	p.t.Helper()
	var res rawResult
	raw := p.request("tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, tool, arguments))
	if err := json.Unmarshal(raw, &res); err != nil {
		p.t.Fatalf("decoding the result of %s: %v\n%s", tool, err, raw)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		p.t.Fatalf("result of %s does not hold exactly one text block: %s", tool, raw)
	}
	return res
}

func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("decoding %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("decoding %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

// The acceptance test of issue #42, which names it
// TestMCPE2E_Pending_StructuredResult (spec 044 FR-021).
func TestStructuredMCP_ResultFields(t *testing.T) {
	// The first protocol version that defines output schemas and structured
	// content. The server must agree to it, not fall back.
	const version = "2025-06-18"
	p := startMCPVersion(t, writeRunefile(t, structuredRunefile), version)
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(p.init, &init); err != nil || init.ProtocolVersion != version {
		t.Fatalf("negotiated protocol %q (%v), want %s", init.ProtocolVersion, err, version)
	}

	var list struct {
		Tools []struct {
			Name         string          `json:"name"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(p.request("tools/list", `{}`), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 3 {
		t.Fatalf("%d tools listed, want 3", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if len(tool.OutputSchema) == 0 || !sameJSON(t, string(tool.OutputSchema), structuredSchema) {
			t.Errorf("tool %s declares output schema %s", tool.Name, tool.OutputSchema)
		}
	}

	// The text below is what the binary built from main returned for this
	// Runefile before the feature.
	failed := p.callRaw("test", `{}`)
	const failedText = "running tests\nFAIL: fixtures out of date\ndiagnosing: hint\n[exit 1]\n" +
		"[fix suggestion - from Runefile || failure hooks]\nre-run with -update"
	if failed.Content[0].Text != failedText || !failed.IsError {
		t.Errorf("test: text = %q, isError = %v, want %q and true", failed.Content[0].Text, failed.IsError, failedText)
	}
	const failedFields = `{"exitCode":1,"stdout":"running tests\n",` +
		`"stderr":"FAIL: fixtures out of date\ndiagnosing: hint\n","fixSuggestion":"re-run with -update"}`
	if !sameJSON(t, string(failed.StructuredContent), failedFields) {
		t.Errorf("test: structured content = %s, want %s", failed.StructuredContent, failedFields)
	}

	passed := p.callRaw("hint", `{}`)
	if passed.Content[0].Text != "re-run with -update\n[exit 0]" || passed.IsError {
		t.Errorf("hint: text = %q, isError = %v", passed.Content[0].Text, passed.IsError)
	}
	// Compared as text: a passing run has no fixSuggestion key at all.
	if got, want := string(passed.StructuredContent), `{"exitCode":0,"stdout":"re-run with -update\n","stderr":""}`; got != want {
		t.Errorf("hint: structured content = %s, want %s", got, want)
	}
}

// A call that runs no task returns what it always did: an error with text
// and no structured content (FR-016).
func TestStructuredMCP_RefusalHasNone(t *testing.T) {
	p := startMCP(t, writeRunefile(t, structuredRunefile))

	refused := p.callRaw("clean", `{}`)
	refusal := `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server` + cannotAskSuffix("clean")
	if refused.Content[0].Text != refusal || !refused.IsError || refused.StructuredContent != nil {
		t.Errorf("clean: text = %q, isError = %v, structured = %s", refused.Content[0].Text, refused.IsError, refused.StructuredContent)
	}

	unknown := p.callRaw("hint", `{"verbose":"1"}`)
	if want := `unknown argument "verbose" for task "hint"`; unknown.Content[0].Text != want || !unknown.IsError || unknown.StructuredContent != nil {
		t.Errorf("hint with an undeclared argument: text = %q, isError = %v, structured = %s",
			unknown.Content[0].Text, unknown.IsError, unknown.StructuredContent)
	}
}

// A client of the protocol version that predates structured content reads
// the text, which is what it was.
func TestStructuredMCP_OldProtocolClient(t *testing.T) {
	text, isError, _ := toolCall(t, writeRunefile(t, structuredRunefile), nil, "hint")
	if text != "re-run with -update\n[exit 0]" || isError {
		t.Errorf("hint over protocol 2024-11-05: text = %q, isError = %v", text, isError)
	}
}
