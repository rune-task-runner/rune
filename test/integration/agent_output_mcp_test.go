package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 057: [agent-output] and [agent-summary] through the real binary, on
// stdio and on HTTP, with an audit log.

const agentOutputRunefile = `# Run the tests.
[agent-output(tail="3")]
[agent-summary]
test: || hint
    @i=1; while [ $i -le 2000 ]; do echo "ok $i"; i=$((i+1)); done
    @echo "FAIL: TestParse token=$API_TOKEN" >&2
    @exit 1

hint:
    @echo "re-run with -update"

# Print 3 MB on each stream.
[agent-output(tail="10")]
flood:
    @s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; i=0; while [ $i -lt 15 ]; do s=$s$s; i=$((i+1)); done; printf '%s\n' "$s"; printf '%s\n' "$s" >&2

# Fail without the attributes.
plain:
    @echo plain-output
    @exit 1
`

// callStructured calls a tool and returns its text, error flag and
// structured content as plain JSON values.
func callStructured(t *testing.T, cs *mcp.ClientSession, tool string) (string, bool, map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool})
	if err != nil {
		t.Fatalf("calling %s: %v", tool, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("structured content of %s: %v", tool, err)
	}
	whole, _ := json.Marshal(res)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError, fields, string(whole)
}

func TestAgentOutputMCP(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, base := auditProject(t, agentOutputRunefile)
			cs, _ := approvalSession(t, transport, dir, nil, env, nil)

			// One schema for every tool, with the two optional objects.
			list, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			schemas := map[string]any{}
			for _, tool := range list.Tools {
				schemas[tool.Name] = tool.OutputSchema
			}
			if !reflect.DeepEqual(schemas["test"], schemas["plain"]) {
				t.Errorf("a task with the attributes and one without list different schemas")
			}
			raw, _ := json.Marshal(schemas["test"])
			var schema jsonschema.Schema
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			for _, prop := range []string{"output", "failureSummary"} {
				if _, ok := schema.Properties[prop]; !ok {
					t.Errorf("the schema has no %s property", prop)
				}
			}
			validator, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}

			text, isErr, fields, whole := callStructured(t, cs, "test")
			if err := validator.Validate(fields); err != nil {
				t.Errorf("structured content is not valid: %v", err)
			}
			if !isErr || fields["exitCode"] != float64(1) {
				t.Errorf("isError %v, exitCode %v", isErr, fields["exitCode"])
			}
			for _, want := range []string{
				"[omitted 1997 lines, ",
				"ok 1998\nok 1999\nok 2000\n",
				"FAIL: TestParse token=***\n",
				"[exit 1]\n[failure summary - from rune]\ntask: test\nfailed task: test\nstep: Runefile:7: exit 1\nreason: exit status 1\n",
				"fix suggestion: follows, from the task's || failure hooks\n",
				"\n[fix suggestion - from Runefile || failure hooks]\nre-run with -update",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the text has no %q:\n%s", want, text)
				}
			}
			if strings.Contains(whole, auditFixture) {
				t.Error("the result holds the secret")
			}
			if fields["stdout"] != text[:len(fields["stdout"].(string))] {
				t.Error("the stdout field is not the start of the text")
			}
			output := fields["output"].(map[string]any)
			if stdout := output["stdout"].(map[string]any); stdout["lines"] != float64(2000) || stdout["keptLines"] != float64(3) {
				t.Errorf("output = %v", output)
			}
			summary := fields["failureSummary"].(map[string]any)
			if summary["kind"] != "step" || summary["line"] != float64(7) || summary["fixSuggestion"] != true {
				t.Errorf("failureSummary = %v", summary)
			}

			_, _, flood, _ := callStructured(t, cs, "flood")
			for _, name := range []string{"stdout", "stderr"} {
				s := flood[name].(string)
				if len(s) > 65536 || !strings.HasPrefix(s, "[omitted 0 lines, ") {
					t.Errorf("flood %s: %d bytes, starts %q", name, len(s), s[:min(len(s), 40)])
				}
			}

			plainText, plainErr, plain, _ := callStructured(t, cs, "plain")
			if !plainErr || plainText != "plain-output\n[exit 1]" {
				t.Errorf("plain: isError %v, text %q", plainErr, plainText)
			}
			for key := range plain {
				if key != "exitCode" && key != "stdout" && key != "stderr" {
					t.Errorf("plain: field %q", key)
				}
			}

			// The audit log keeps metadata only.
			if len(logCalls(t, dir, env)) != 3 {
				t.Errorf("%d calls in the log, want 3", len(logCalls(t, dir, env)))
			}
			for _, needle := range []string{"ok 1999", "FAIL: TestParse", "failure summary", "re-run with -update", "plain-output", auditFixture} {
				if found := filesHolding(t, base, needle); len(found) != 0 {
					t.Errorf("%q is in %v", needle, found)
				}
			}
		})
	}
}
