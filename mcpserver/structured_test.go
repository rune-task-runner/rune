package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 044 (contracts/mcp-results.md): every tool declares one output
// schema, and every result of a task run carries the exit code and the two
// streams as fields, beside the text it always had.

// wantOutputSchema is the contract's schema, written out so a change to the
// production text is a change to this test.
const wantOutputSchema = `{
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

// asJSONValue decodes v's JSON form into plain maps, slices and scalars, so
// two values compare by content whatever Go types they came from.
func asJSONValue(t *testing.T, v any) any {
	t.Helper()
	raw, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(b)
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return out
}

// structured returns a result's structured content as a decoded JSON object,
// or nil when it has none.
func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent == nil {
		return nil
	}
	obj, ok := asJSONValue(t, res.StructuredContent).(map[string]any)
	if !ok {
		t.Fatalf("structured content is not an object: %v", res.StructuredContent)
	}
	return obj
}

func TestCapResult(t *testing.T) {
	stream := strings.Repeat("0123456789", 1000)
	in := Result{Stdout: stream, Stderr: "e" + stream, ExitCode: 7, FixSuggestion: "fix"}
	got := capResult(in, 1024)
	if len(got.Stdout) > 1024 || got.Stdout != capOutput(in.Stdout, 1024) {
		t.Errorf("stdout is %d bytes, want capOutput's %d", len(got.Stdout), len(capOutput(in.Stdout, 1024)))
	}
	if len(got.Stderr) > 1024 || got.Stderr != capOutput(in.Stderr, 1024) {
		t.Errorf("stderr is %d bytes, want capOutput's %d", len(got.Stderr), len(capOutput(in.Stderr, 1024)))
	}
	if got.ExitCode != 7 || got.FixSuggestion != "fix" {
		t.Errorf("capResult changed the exit code or the fix suggestion: %+v", got)
	}
	small := Result{Stdout: "a\n", Stderr: "b", ExitCode: 1}
	if got := capResult(small, 1024); got != small {
		t.Errorf("a result within the limit changed: %+v", got)
	}
}

// resultCases covers every way the text joins its parts, and both sides of
// the limit used with them, 1024.
func resultCases() map[string]Result {
	long := strings.Repeat("0123456789", 300)
	return map[string]Result{
		"empty":                 {},
		"stdout":                {Stdout: "out\n"},
		"stdout no newline":     {Stdout: "out"},
		"stderr":                {Stderr: "err\n", ExitCode: 1},
		"stderr no newline":     {Stderr: "err", ExitCode: 1},
		"both":                  {Stdout: "out\n", Stderr: "err\n"},
		"both no newlines":      {Stdout: "out", Stderr: "err", ExitCode: 3},
		"stdout newline only":   {Stdout: "out\n", Stderr: "err", ExitCode: 3},
		"fix":                   {Stdout: "out\n", Stderr: "err\n", ExitCode: 1, FixSuggestion: "re-run with -update"},
		"fix alone":             {ExitCode: 1, FixSuggestion: "do this\n"},
		"over the limit":        {Stdout: long, Stderr: long + "\n", ExitCode: 2, FixSuggestion: "fix"},
		"interrupted":           {Stdout: "partial", ExitCode: 130},
		"exit 255":              {Stderr: "bad\n", ExitCode: 255},
		"prints an exit line":   {Stdout: "[exit 0]\n", ExitCode: 1},
		"invalid UTF-8":         {Stdout: "a\xffb\n", Stderr: "\xc3\x28\n"},
		"quotes and brackets":   {Stdout: "<a href=\"x\">&</a>\n", Stderr: "{\"k\": [1]}\n"},
		"blank lines":           {Stdout: "\n\n", Stderr: "\n"},
		"carriage returns":      {Stdout: "a\r\n", Stderr: "b\r\n"},
		"fix with exit zero":    {Stdout: "ok\n", FixSuggestion: "unused"},
		"stderr then no stdout": {Stderr: "only\n", FixSuggestion: "fix\n", ExitCode: 4},
	}
}

// renderText reads only the result it is given: a stream over the limit is
// rendered whole, because capping is capResult's job. That split is what
// lets the handler cap once and build the text and the fields from one value.
func TestRenderTextDoesNotCap(t *testing.T) {
	long := strings.Repeat("0123456789", 300)
	r := Result{Stdout: long, Stderr: "err", ExitCode: 2, FixSuggestion: "fix"}
	want := long + "\nerr\n[exit 2]\n[fix suggestion - from Runefile || failure hooks]\nfix"
	if got := renderText(r); got != want {
		t.Errorf("renderText = %d bytes, want the %d of the uncapped parts", len(got), len(want))
	}
	if got := renderText(capResult(r, 1024)); len(got) >= len(want) || !strings.Contains(got, "[truncated ") {
		t.Errorf("renderText of a capped result is %d bytes with no marker", len(got))
	}
}

func TestOutputSchema(t *testing.T) {
	if got, want := asJSONValue(t, outputSchema()), asJSONValue(t, wantOutputSchema); !reflect.DeepEqual(got, want) {
		b, _ := json.MarshalIndent(outputSchema(), "", "  ")
		t.Errorf("outputSchema() =\n%s\nwant\n%s", b, wantOutputSchema)
	}
	// Each tool gets its own map: one tool's listing cannot change another's.
	a, b := outputSchema(), outputSchema()
	a["type"] = "changed"
	if b["type"] != "object" {
		t.Error("two calls of outputSchema share a map")
	}
}

func TestEveryToolDeclaresOutputSchema(t *testing.T) {
	want := asJSONValue(t, wantOutputSchema)
	for name, opts := range map[string]Options{
		"all":      {},
		"filtered": {AllowList: []string{"logs", "clean", "docker::push"}},
	} {
		res, err := connect(t, New(sampleEngine(), opts)).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Tools) == 0 {
			t.Fatalf("%s: no tools listed", name)
		}
		for _, tool := range res.Tools {
			if got := asJSONValue(t, tool.OutputSchema); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: tool %s declares output schema %v", name, tool.Name, tool.OutputSchema)
			}
		}
	}
}

func TestStructuredContent(t *testing.T) {
	cases := []struct {
		name    string
		result  Result
		want    string
		isError bool
	}{
		{"success", Result{Stdout: "out\n", Stderr: "err\n"}, `{"exitCode":0,"stdout":"out\n","stderr":"err\n"}`, false},
		{"failure", Result{Stdout: "out\n", Stderr: "err\n", ExitCode: 3}, `{"exitCode":3,"stdout":"out\n","stderr":"err\n"}`, true},
		{"no output", Result{}, `{"exitCode":0,"stdout":"","stderr":""}`, false},
		{
			"fix suggestion",
			Result{Stderr: "boom\n", ExitCode: 1, FixSuggestion: "re-run\n"},
			`{"exitCode":1,"stdout":"","stderr":"boom\n","fixSuggestion":"re-run\n"}`, true,
		},
	}
	for _, c := range cases {
		res := callResult(t, c.result, Options{})
		if got, want := structured(t, res), asJSONValue(t, c.want); !reflect.DeepEqual(any(got), want) {
			t.Errorf("%s: structured content = %v, want %s", c.name, got, c.want)
		}
		if res.IsError != c.isError {
			t.Errorf("%s: isError = %v, want %v", c.name, res.IsError, c.isError)
		}
	}
}

// The key is left out, not sent empty, so its presence says the text has a
// fix-suggestion section (FR-009).
func TestStructuredOmitsEmptyFixSuggestion(t *testing.T) {
	s := structuredFrom(Result{Stdout: "ok\n"})
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"exitCode":0,"stdout":"ok\n","stderr":""}`; string(raw) != want {
		t.Errorf("marshaled structured result = %s, want %s", raw, want)
	}
	if _, has := structured(t, callResult(t, Result{Stdout: "ok\n", ExitCode: 1}, Options{}))["fixSuggestion"]; has {
		t.Error("a result without a fix suggestion has the fixSuggestion key")
	}
}

// The exit code is the engine's, never read back out of what the task
// printed.
func TestStructuredExitNotParsedFromOutput(t *testing.T) {
	got := structured(t, callResult(t, Result{Stdout: "[exit 0]\n", ExitCode: 1}, Options{}))
	if got["exitCode"] != float64(1) || got["stdout"] != "[exit 0]\n" {
		t.Errorf("structured content = %v, want exitCode 1 and the printed line as stdout", got)
	}
}

// outputValidator resolves the output schema a listed tool declares.
func outputValidator(t *testing.T, cs *mcp.ClientSession) *jsonschema.Resolved {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) == 0 {
		t.Fatalf("ListTools = %v, %v", res, err)
	}
	raw, err := json.Marshal(res.Tools[0].OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("the declared output schema is not a JSON Schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolving the declared output schema: %v", err)
	}
	return resolved
}

func TestStructuredValidatesAgainstSchema(t *testing.T) {
	eng := sampleEngine()
	cs := connect(t, New(eng, Options{OutputLimit: 1024}))
	validator := outputValidator(t, cs)
	for name, r := range resultCases() {
		eng.result = r
		got := structured(t, callTool(t, cs, "logs", nil))
		if got == nil {
			t.Errorf("%s: no structured content", name)
			continue
		}
		if err := validator.Validate(got); err != nil {
			t.Errorf("%s: structured content %v is not valid against the declared schema: %v", name, got, err)
		}
	}

	// The schema is strict, so the passes above mean something.
	invalid := map[string]map[string]any{
		"extra key":        {"exitCode": 0, "stdout": "", "stderr": "", "signal": 9},
		"no stdout":        {"exitCode": 0, "stderr": ""},
		"no exit code":     {"stdout": "", "stderr": ""},
		"exit code string": {"exitCode": "0", "stdout": "", "stderr": ""},
		"stdout number":    {"exitCode": 0, "stdout": 1, "stderr": ""},
	}
	for name, obj := range invalid {
		if err := validator.Validate(asJSONValue(t, obj)); err == nil {
			t.Errorf("%s: the declared schema accepts %v", name, obj)
		}
	}
}

func TestStructuredOverHTTP(t *testing.T) {
	eng := &syncEngine{tasks: []TaskInfo{{Name: "build"}}}
	addr, stop, err := New(eng, Options{}).StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	cs := httpSession(t, addr, "secret")

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("ListTools = %v, %v", list, err)
	}
	if got, want := asJSONValue(t, list.Tools[0].OutputSchema), asJSONValue(t, wantOutputSchema); !reflect.DeepEqual(got, want) {
		t.Errorf("over HTTP the tool declares output schema %v", list.Tools[0].OutputSchema)
	}
	got := structured(t, callTool(t, cs, "build", nil))
	if want := asJSONValue(t, `{"exitCode":0,"stdout":"ran build","stderr":""}`); !reflect.DeepEqual(any(got), want) {
		t.Errorf("over HTTP structured content = %v", got)
	}
}

func TestStructuredListEngine(t *testing.T) {
	eng := &fakeListEngine{}
	eng.tasks = []TaskInfo{{Name: "build"}}
	eng.result = Result{Stdout: "built\n", ExitCode: 2}
	got := structured(t, callTool(t, connect(t, New(eng, Options{})), "build", nil))
	if want := asJSONValue(t, `{"exitCode":2,"stdout":"built\n","stderr":""}`); !reflect.DeepEqual(any(got), want) {
		t.Errorf("ListEngine structured content = %v", got)
	}
}

// rebuildText is the documented rule that gives the text from the fields
// (specs/044-mcp-structured-results/data-model.md), written here without
// the production code.
func rebuildText(fields map[string]any) string {
	stdout, _ := fields["stdout"].(string)
	stderr, _ := fields["stderr"].(string)
	text := stdout
	if stderr != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += stderr
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "[exit " + jsonInt(fields["exitCode"]) + "]"
	if fix, ok := fields["fixSuggestion"].(string); ok {
		text += "\n[fix suggestion - from Runefile || failure hooks]\n" + fix
	}
	return text
}

func jsonInt(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// SC-006: the fields are the text's parts, so the text can be rebuilt from
// them for every result.
func TestTextRebuiltFromStructured(t *testing.T) {
	eng := sampleEngine()
	cs := connect(t, New(eng, Options{OutputLimit: 1024}))
	for name, r := range resultCases() {
		eng.result = r
		res := callTool(t, cs, "logs", nil)
		if got, want := rebuildText(structured(t, res)), contentText(res); got != want {
			t.Errorf("%s: text rebuilt from the fields = %q, want the text content %q", name, got, want)
		}
	}
}

type errEngine struct{ fakeEngine }

func (e *errEngine) Call(context.Context, string, map[string]string) (Result, error) {
	return Result{}, errors.New("unknown task: logs")
}

// noRunCalls returns a result of each kind of call that runs no task, with
// the text it has always had.
func noRunCalls(t *testing.T) map[string]struct {
	res  *mcp.CallToolResult
	text string
} {
	t.Helper()
	eng := &fakeEngine{tasks: []TaskInfo{
		{Name: "clean", Destructive: true},
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "deploy", Params: []ParamInfo{{Name: "env", Kind: "enum", Enum: []string{"staging", "prod"}}}},
	}}
	cs := connect(t, New(eng, Options{AllowDestructiveFlag: "--allow-destructive"}))
	failing := &errEngine{}
	failing.tasks = []TaskInfo{{Name: "logs"}}

	type call = struct {
		res  *mcp.CallToolResult
		text string
	}
	rawReq := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "deploy", Arguments: json.RawMessage(`[1]`)}}
	undecodable, err := New(eng, Options{}).handler("deploy")(context.Background(), rawReq)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]call{
		"destructive": {
			callTool(t, cs, "clean", nil),
			`task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`,
		},
		"reaches destructive": {
			callTool(t, cs, "release", nil),
			`task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`,
		},
		"undeclared argument": {
			callTool(t, cs, "deploy", map[string]any{"evn": "prod"}),
			`unknown argument "evn" for task "deploy" (did you mean "env"?)`,
		},
		"invalid value": {
			callTool(t, cs, "deploy", map[string]any{"env": "qa"}),
			`invalid arguments: parameter "env": invalid value "qa" (allowed values: "staging", "prod")`,
		},
		"undecodable arguments": {
			undecodable,
			"invalid arguments: json: cannot unmarshal array into Go value of type map[string]interface {}",
		},
		"engine error": {
			callTool(t, connect(t, New(failing, Options{})), "logs", nil),
			"unknown task: logs",
		},
	}
	if eng.lastCall != "" {
		t.Fatalf("a call that must not run reached the engine: %q", eng.lastCall)
	}
	return calls
}

// FR-016: a call that runs no task has no exit code to report. It returns
// what it always did.
func TestNoStructuredContentWithoutARun(t *testing.T) {
	for name, c := range noRunCalls(t) {
		if c.res.StructuredContent != nil {
			t.Errorf("%s: structured content = %v, want none", name, c.res.StructuredContent)
		}
		if got := contentText(c.res); got != c.text {
			t.Errorf("%s: text = %q, want %q", name, got, c.text)
		}
		if !c.res.IsError {
			t.Errorf("%s: a result without structured content is not marked as an error", name)
		}
	}
}

func TestResultHasOneTextBlock(t *testing.T) {
	results := map[string]*mcp.CallToolResult{
		"ran":    callResult(t, Result{Stdout: "out\n", Stderr: "err\n"}, Options{}),
		"failed": callResult(t, Result{Stderr: "err\n", ExitCode: 2, FixSuggestion: "fix"}, Options{}),
	}
	for name, c := range noRunCalls(t) {
		results[name] = c.res
	}
	for name, res := range results {
		if len(res.Content) != 1 {
			t.Errorf("%s: %d content blocks, want 1", name, len(res.Content))
			continue
		}
		if _, ok := res.Content[0].(*mcp.TextContent); !ok {
			t.Errorf("%s: content block is %T, want text", name, res.Content[0])
		}
	}
	if results["ran"].IsError || !results["failed"].IsError {
		t.Errorf("isError: ran=%v failed=%v, want false and true", results["ran"].IsError, results["failed"].IsError)
	}
}

// FR-017: a tool's listing is what it was, plus the output schema.
func TestToolListingOnlyGainsOutputSchema(t *testing.T) {
	const before = `[
  {"name":"clean","description":"Remove build output.","inputSchema":{"type":"object","properties":{},"additionalProperties":false},"annotations":{"destructiveHint":true,"openWorldHint":false}},
  {"name":"docker__push","description":"Push image.","inputSchema":{"type":"object","properties":{},"additionalProperties":false},"annotations":{"destructiveHint":false,"openWorldHint":false}},
  {"name":"fetch","description":"Fetch a URL.","inputSchema":{"type":"object","properties":{},"additionalProperties":false},"annotations":{"destructiveHint":false,"openWorldHint":true}},
  {"name":"greet","description":"Greet someone.","inputSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false},"annotations":{"destructiveHint":false,"openWorldHint":false}},
  {"name":"logs","description":"Show recent git log.","inputSchema":{"type":"object","properties":{},"additionalProperties":false},"annotations":{"destructiveHint":false,"openWorldHint":false}}
]`
	res, err := connect(t, New(sampleEngine(), Options{})).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed, ok := asJSONValue(t, res.Tools).([]any)
	if !ok {
		t.Fatalf("the tool list is not an array: %v", res.Tools)
	}
	for _, tool := range listed {
		entry := tool.(map[string]any)
		if _, has := entry["outputSchema"]; !has {
			t.Errorf("tool %v has no output schema", entry["name"])
		}
		delete(entry, "outputSchema")
	}
	if want := asJSONValue(t, before); !reflect.DeepEqual(any(listed), want) {
		got, _ := json.Marshal(listed)
		t.Errorf("without outputSchema the listing is\n%s\nwant\n%s", got, before)
	}
}

// FR-018: every tool carries the same schema on every load, so a reload
// that changes nothing still tells clients nothing.
func TestReplaceSameToolsStillSilent(t *testing.T) {
	srv := New(sampleEngine(), Options{Reloadable: true})
	if got := srv.Replace(sampleEngine(), 0); got != (Change{}) {
		t.Errorf("Replace with equal tasks = %+v, want no change", got)
	}
}

// FR-019: the library caps an embedder's uncapped result once, and the
// fields are the capped strings the text is made of.
func TestStructuredCappedForAnyEngine(t *testing.T) {
	stream := strings.Repeat("0123456789", 20000)
	res := callResult(t, Result{Stdout: "o" + stream, Stderr: "e" + stream, ExitCode: 1}, Options{OutputLimit: 4096})
	got := structured(t, res)
	stdout, _ := got["stdout"].(string)
	stderr, _ := got["stderr"].(string)
	for name, field := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if len(field) > 4096 || len(markerLineRE.FindAllString(field, -1)) != 1 {
			t.Errorf("%s field is %d bytes with %d markers, want at most 4096 and one marker",
				name, len(field), len(markerLineRE.FindAllString(field, -1)))
		}
	}
	if !strings.HasPrefix(stdout, "o0123") || !strings.HasPrefix(stderr, "e0123") {
		t.Error("the fields do not start with the streams' first bytes")
	}
	if want := stdout + "\n" + stderr + "\n[exit 1]"; contentText(res) != want {
		t.Error("the text is not the two capped fields joined by the text rule")
	}
}
