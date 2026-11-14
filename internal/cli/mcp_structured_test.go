package cli

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 044: the structured fields of a tool result are the masked, capped
// strings its text is made of. These go through the real adapter and a real
// server, so the masking and the capture are the production ones.

// structuredFields keeps fixSuggestion as a pointer, so a missing key and an
// empty value stay apart.
type structuredFields struct {
	ExitCode      int     `json:"exitCode"`
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	FixSuggestion *string `json:"fixSuggestion"`
}

// structuredCall calls a tool and returns the result with its structured
// content decoded.
func structuredCall(t *testing.T, cs *mcp.ClientSession, tool string) (text string, fields structuredFields, raw string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool})
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	if res.StructuredContent == nil {
		t.Fatalf("call %s: no structured content; text %q", tool, resultText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("decoding %s: %v", b, err)
	}
	whole, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return resultText(res), fields, string(whole)
}

// joinFields is the documented rule that gives the text from the fields.
func joinFields(f structuredFields) string {
	text := f.Stdout
	if f.Stderr != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += f.Stderr
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "[exit " + strconv.Itoa(f.ExitCode) + "]"
	if f.FixSuggestion != nil {
		text += "\n[fix suggestion - from Runefile || failure hooks]\n" + *f.FixSuggestion
	}
	return text
}

func structuredServer(t *testing.T, a *mcpAdapter) *mcp.ClientSession {
	t.Helper()
	return serverSession(t, mcpserver.New(a, mcpserver.Options{Version: "test", OutputLimit: a.limit()}))
}

func TestMCPStructuredMasksSecrets(t *testing.T) {
	const secret = "Zq7Lw9Xk2Vb5Nm8Rt4Yp6Hs3"
	a := adapterFor(t, "leak: || diagnose\n"+
		"    @echo out=$MY_API_TOKEN\n"+
		"    @echo err=$MY_API_TOKEN >&2\n"+
		"    @exit 5\n"+
		"diagnose:\n"+
		"    @echo fix=$MY_API_TOKEN\n")
	a.baseEnv = []string{"MY_API_TOKEN=" + secret}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)

	text, fields, whole := structuredCall(t, structuredServer(t, a), "leak")
	if strings.Contains(whole, secret) {
		t.Errorf("the result holds the secret: %s", whole)
	}
	if fields.ExitCode != 5 {
		t.Errorf("exitCode = %d, want 5", fields.ExitCode)
	}
	if fields.Stdout != "out=***\n" || !strings.Contains(fields.Stderr, "err=***\n") {
		t.Errorf("stdout = %q, stderr = %q, want the mask where the secret was", fields.Stdout, fields.Stderr)
	}
	if fields.FixSuggestion == nil || *fields.FixSuggestion != "fix=***" {
		t.Errorf("fixSuggestion = %v, want the masked hook output", fields.FixSuggestion)
	}
	if got := joinFields(fields); got != text {
		t.Errorf("text rebuilt from the fields = %q, want %q", got, text)
	}
}

func TestMCPStructuredCapsStreams(t *testing.T) {
	a := adapterFor(t, "flood:\n"+
		"    @"+fill+`; printf 'BEGIN\n%s\nEND\n' "$s"; printf 'EBEGIN\n%s\nEEND\n' "$s" >&2`+"\n")
	text, fields, _ := structuredCall(t, structuredServer(t, a), "flood")
	for name, field := range map[string]string{"stdout": fields.Stdout, "stderr": fields.Stderr} {
		if len(field) > 65536 {
			t.Errorf("%s field is %d bytes, want at most 65536", name, len(field))
		}
		shownAndDropped(t, field) // exactly one marker
	}
	if !strings.HasPrefix(fields.Stdout, "BEGIN\n") || !strings.HasSuffix(fields.Stdout, "\nEND\n") {
		t.Error("the stdout field does not keep the stream's beginning and end")
	}
	if !strings.HasPrefix(fields.Stderr, "EBEGIN\n") || !strings.HasSuffix(fields.Stderr, "\nEEND\n") {
		t.Error("the stderr field does not keep the stream's beginning and end")
	}
	if got := joinFields(fields); got != text {
		t.Errorf("text rebuilt from the fields differs from the text content (%d and %d bytes)", len(got), len(text))
	}
}

func TestMCPStructuredHonorsLimit(t *testing.T) {
	const src = "set mcp_output_limit := \"1KiB\"\n\nnoisy:\n    @printf '%010000d' 0; printf '%010000d' 0 >&2\n"
	cases := []struct {
		name      string
		flagLimit int
		max, min  int
	}{
		{"setting", 0, 1024, 512},
		{"flag over setting", 2048, 2048, 1025},
	}
	for _, c := range cases {
		a := adapterFor(t, src)
		a.outputLimit = c.flagLimit
		_, fields, _ := structuredCall(t, structuredServer(t, a), "noisy")
		for name, field := range map[string]string{"stdout": fields.Stdout, "stderr": fields.Stderr} {
			if len(field) > c.max || len(field) < c.min {
				t.Errorf("%s: %s field is %d bytes, want %d to %d", c.name, name, len(field), c.min, c.max)
			}
		}
	}
}

func TestMCPStructuredFixSuggestionCap(t *testing.T) {
	a := adapterFor(t, "test: || diagnose\n    @exit 1\ndiagnose:\n    @"+fill+`; printf '%s' "$s"`+"\n")
	text, fields, _ := structuredCall(t, structuredServer(t, a), "test")
	if fields.FixSuggestion == nil {
		t.Fatal("no fixSuggestion field")
	}
	fix := *fields.FixSuggestion
	if !strings.HasSuffix(fix, "\n[truncated]") || len(fix) > contextMaxBytes+len("\n[truncated]") {
		t.Errorf("fixSuggestion is %d bytes ending %q, want the 8 KiB cap and its marker", len(fix), fix[max(len(fix)-20, 0):])
	}
	if !strings.HasSuffix(text, "\n[fix suggestion - from Runefile || failure hooks]\n"+fix) {
		t.Error("fixSuggestion is not the text's fix-suggestion section")
	}
}

func TestMCPStructuredDependencies(t *testing.T) {
	a := adapterFor(t, "main: dep && hook\n    @echo main; echo main-err >&2\n"+
		"dep:\n    @echo dep; echo dep-err >&2\n"+
		"hook:\n    @echo hook; echo hook-err >&2\n")
	text, fields, _ := structuredCall(t, structuredServer(t, a), "main")
	if fields.Stdout != "dep\nmain\nhook\n" || fields.Stderr != "dep-err\nmain-err\nhook-err\n" {
		t.Errorf("stdout = %q, stderr = %q, want the whole run's streams", fields.Stdout, fields.Stderr)
	}
	if fields.FixSuggestion != nil {
		t.Errorf("fixSuggestion = %q on a run that passed", *fields.FixSuggestion)
	}
	if got := joinFields(fields); got != text {
		t.Errorf("text rebuilt from the fields = %q, want %q", got, text)
	}
}

// A required argument left out is rejected by the engine before the task
// starts, so the result is an error with text and no structured content
// (FR-016). docs/mcp.md lists this case.
func TestMCPStructuredNoneForMissingArgument(t *testing.T) {
	a := adapterFor(t, "greet name:\n    @echo hi {{name}}\n")
	res, err := structuredServer(t, a).CallTool(context.Background(), &mcp.CallToolParams{Name: "greet"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `task "greet" is missing required argument "name"`
	if got := resultText(res); got != want || !res.IsError || res.StructuredContent != nil {
		t.Errorf("text = %q, isError = %v, structured = %v, want %q, true and none", got, res.IsError, res.StructuredContent, want)
	}
}

// The endpoint an agent task opens for its own agent is the same server, so
// it declares the schema and returns the fields too (FR-003).
func TestAgentEndpointDeclaresOutputSchema(t *testing.T) {
	a := adapterFor(t, "# Builds.\nbuild:\n    @echo built\n")
	eng := &engine{file: a.file, tasks: a.tasks, settings: a.settings, opts: Options{Version: "test"}}
	cs := serverSession(t, mcpserver.New(a, eng.agentMCPOptions(a.limit())))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("ListTools = %v, %v", list, err)
	}
	schema, err := json.Marshal(list.Tools[0].OutputSchema)
	if err != nil || !strings.Contains(string(schema), `"exitCode"`) || !strings.Contains(string(schema), `"additionalProperties":false`) {
		t.Errorf("agent endpoint output schema = %s, %v", schema, err)
	}
	// FR-003 asks for the schema `rune mcp` declares, not for any schema.
	served, err := structuredServer(t, a).ListTools(context.Background(), nil)
	if err != nil || len(served.Tools) != 1 {
		t.Fatalf("ListTools = %v, %v", served, err)
	}
	if want, err := json.Marshal(served.Tools[0].OutputSchema); err != nil || string(schema) != string(want) {
		t.Errorf("agent endpoint output schema = %s, want the serve schema %s (%v)", schema, want, err)
	}
	_, fields, _ := structuredCall(t, cs, "build")
	if fields.ExitCode != 0 || fields.Stdout != "built\n" || fields.Stderr != "" {
		t.Errorf("agent endpoint fields = %+v", fields)
	}
}
