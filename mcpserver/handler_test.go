package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// Spec 023 US2 (contracts/mcp-schema.md, invocation-time enforcement): the
// server validates arguments against the advertised schema before the engine
// runs anything — scalar violations and per-element variadic violations both
// return a tool error carrying the parameter, the rejected value, and the
// accepted set (FR-006), with zero side effects (SC-002).
func TestHandlerRejectsConstraintViolations(t *testing.T) {
	eng := &fakeEngine{
		tasks: []TaskInfo{
			{Name: "deploy", Params: []ParamInfo{
				{Name: "env", Required: true, Kind: "enum", Enum: []string{"staging", "prod"}},
			}},
			{Name: "sum", Params: []ParamInfo{
				{Name: "nums", Required: true, Variadic: true, Kind: "number"},
			}},
		},
		result: Result{Stdout: "ran", ExitCode: 0},
	}
	srv := New(eng, Options{})
	cs := connect(t, srv)
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "deploy", Arguments: map[string]any{"env": "production"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("out-of-enum value accepted")
	}
	text := contentText(res)
	for _, want := range []string{"env", `"production"`, `"staging", "prod"`} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q missing %q", text, want)
		}
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q despite the violation", eng.lastCall)
	}

	// Variadic arrays are validated per element BEFORE the space-join — the
	// join destroys element boundaries, so this is the only point where
	// per-value semantics exist on the MCP path (FR-008).
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "sum", Arguments: map[string]any{"nums": []any{"1.5", "x", "3"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("bad variadic element accepted")
	}
	text = contentText(res)
	for _, want := range []string{"nums", `"x"`, "number"} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q missing %q", text, want)
		}
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q despite the violation", eng.lastCall)
	}

	// Valid values pass through unchanged, JSON numbers included: UseNumber
	// keeps the source spelling verbatim.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "sum", Arguments: map[string]any{"nums": []any{"1.5", 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("valid call rejected: %s", contentText(res))
	}
	if eng.lastCall != "sum" || eng.lastArgs["nums"] != "1.5 2" {
		t.Errorf("engine call = %q args %v", eng.lastCall, eng.lastArgs)
	}

	// A scalar supplied for a variadic parameter is validated as one element
	// (exactly as the CLI treats one quoted argument), so no value form
	// bypasses the constraint (FR-005).
	eng.lastCall, eng.lastArgs = "", nil
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "sum", Arguments: map[string]any{"nums": "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("string-form variadic bypassed the constraint")
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q despite the violation", eng.lastCall)
	}
}

// Large and small JSON numbers must reach the task with their source spelling:
// json.Unmarshal into any yields float64, whose default formatting mangles
// 1000000 into "1e+06" — decodeArgs decodes with UseNumber to prevent that.
// decodeAndConvert runs the two steps a handler runs on a call's arguments.
func decodeAndConvert(raw []byte, params []ParamInfo) (map[string]string, map[string][]string, error) {
	decoded, err := decodeArgs(raw)
	if err != nil {
		return nil, nil, err
	}
	return stringArgs(decoded, params)
}

func TestDecodeArgsKeepsNumberSpelling(t *testing.T) {
	params := []ParamInfo{
		{Name: "replicas", Kind: "number"},
		{Name: "nums", Variadic: true, Kind: "number"},
	}
	args, _, err := decodeAndConvert([]byte(`{"replicas": 1000000, "nums": [0.0000001, 12345678901234567890]}`), params)
	if err != nil {
		t.Fatal(err)
	}
	if args["replicas"] != "1000000" {
		t.Errorf("replicas = %q, want the verbatim source spelling %q", args["replicas"], "1000000")
	}
	if args["nums"] != "0.0000001 12345678901234567890" {
		t.Errorf("nums = %q, want verbatim spellings", args["nums"])
	}
}

// Spec 030 (contracts/mcp.md): a variadic's elements reach the engine as a
// list next to the joined value.
func TestDecodeArgsLists(t *testing.T) {
	params := []ParamInfo{{Name: "names", Variadic: true}, {Name: "one"}}
	tests := []struct {
		raw       string
		wantArg   string
		hasArg    bool
		wantList  []string
		hasListed bool
	}{
		{`{"names": ["a b", "c"]}`, "a b c", true, []string{"a b", "c"}, true},
		{`{"names": []}`, "", true, []string{}, true},
		{`{"names": "a b"}`, "a b", true, []string{"a b"}, true},
		{`{"names": ""}`, "", true, []string{}, true},
		{`{}`, "", false, nil, false},
	}
	for _, tt := range tests {
		args, lists, err := decodeAndConvert([]byte(tt.raw), params)
		if err != nil {
			t.Fatalf("%s: %v", tt.raw, err)
		}
		if got, ok := args["names"]; ok != tt.hasArg || got != tt.wantArg {
			t.Errorf("%s: args[names] = %q (present %v)", tt.raw, got, ok)
		}
		got, ok := lists["names"]
		if ok != tt.hasListed || (ok && (got == nil || !reflect.DeepEqual(got, tt.wantList))) {
			t.Errorf("%s: lists[names] = %#v (present %v), want %#v", tt.raw, got, ok, tt.wantList)
		}
	}

	args, lists, err := decodeAndConvert([]byte(`{"one": ["x", "y"], "unknown": ["p", "q"]}`), params)
	if err != nil {
		t.Fatal(err)
	}
	if args["one"] != "x y" {
		t.Errorf("non-variadic arrays changed: %v", args)
	}
	// Spec 032: the handler refuses undeclared names first, so none reaches the
	// values a task receives.
	if _, ok := args["unknown"]; ok {
		t.Errorf("an undeclared name reached the task's values: %v", args)
	}
	if len(lists) != 0 {
		t.Errorf("lists for non-variadic or unknown parameters: %v", lists)
	}
}

type fakeListEngine struct {
	fakeEngine
	viaLists  bool
	lastLists map[string][]string
}

func (f *fakeListEngine) CallLists(_ context.Context, name string, args map[string]string, lists map[string][]string) (Result, error) {
	f.lastCall, f.lastArgs, f.lastLists, f.viaLists = name, args, lists, true
	return f.result, nil
}

func TestHandlerPrefersListEngine(t *testing.T) {
	tasks := []TaskInfo{{Name: "c", Params: []ParamInfo{{Name: "names", Variadic: true}}}}
	call := map[string]any{"names": []any{"a b", "c"}}

	le := &fakeListEngine{fakeEngine: fakeEngine{tasks: tasks}}
	if _, err := connect(t, New(le, Options{})).CallTool(context.Background(), &mcp.CallToolParams{Name: "c", Arguments: call}); err != nil {
		t.Fatal(err)
	}
	if !le.viaLists || le.lastArgs["names"] != "a b c" || !reflect.DeepEqual(le.lastLists["names"], []string{"a b", "c"}) {
		t.Errorf("ListEngine call: viaLists %v args %v lists %v", le.viaLists, le.lastArgs, le.lastLists)
	}

	plain := &fakeEngine{tasks: tasks}
	if _, err := connect(t, New(plain, Options{})).CallTool(context.Background(), &mcp.CallToolParams{Name: "c", Arguments: call}); err != nil {
		t.Fatal(err)
	}
	if plain.lastCall != "c" || plain.lastArgs["names"] != "a b c" {
		t.Errorf("plain Engine call = %q args %v", plain.lastCall, plain.lastArgs)
	}
}

// Spec 031: a grant lets exactly the named destructive tasks reach the engine.
// Any other destructive task is refused with the text naming the flag, and the
// task name (not the tool name) is what that text carries.
func TestHandlerGrantIsPerTask(t *testing.T) {
	eng := &fakeEngine{
		tasks: []TaskInfo{
			{Name: "clean", Destructive: true},
			{Name: "reset", Destructive: true},
			{Name: "tools::clean", Destructive: true},
		},
		result: Result{Stdout: "ran", ExitCode: 0},
	}
	cs := connect(t, New(eng, Options{AllowDestructive: []string{"clean"}, AllowDestructiveFlag: "--allow-destructive"}))
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "clean"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || eng.lastCall != "clean" || !strings.Contains(contentText(res), "ran") {
		t.Fatalf("granted clean did not run: isError=%v lastCall=%q text=%q", res.IsError, eng.lastCall, contentText(res))
	}

	eng.lastCall = ""
	for tool, task := range map[string]string{"reset": "reset", "tools__clean": "tools::clean"} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool})
		if err != nil {
			t.Fatal(err)
		}
		want := "task \"" + task + "\" is destructive; to permit it, add " + task + " to --allow-destructive and restart the server"
		if !res.IsError || contentText(res) != want {
			t.Errorf("call %s: isError=%v text=%q, want %q", tool, res.IsError, contentText(res), want)
		}
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q, but only clean is granted", eng.lastCall)
	}
}

func unknownArgsEngine() *fakeEngine {
	return &fakeEngine{
		tasks: []TaskInfo{
			{Name: "build", Params: []ParamInfo{{Name: "target"}}},
			{Name: "deploy", Params: []ParamInfo{
				{Name: "env", Required: true, Kind: "enum", Enum: []string{"staging", "prod"}},
				{Name: "replicas"},
			}},
			{Name: "each", Params: []ParamInfo{{Name: "names", Required: true, Variadic: true}}},
			{Name: "clean_all"},
			{Name: "wipe", Destructive: true},
			{Name: "tools::build", Params: []ParamInfo{{Name: "target"}}},
			{Name: "tie", Params: []ParamInfo{{Name: "tag"}, {Name: "tab"}}},
			{Name: "get", Params: []ParamInfo{{Name: "id"}}},
			{Name: "rm", Params: []ParamInfo{{Name: "path", Required: true}}},
		},
		result: Result{Stdout: "ran"},
	}
}

func callTool(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Spec 032: a call naming an undeclared argument is refused before the engine
// runs, instead of running with the parameter's default.
func TestHandlerRefusesUnknownArgument(t *testing.T) {
	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"typo", "build", map[string]any{"targte": "release"}, `unknown argument "targte" for task "build" (did you mean "target"?)`},
		{"typo beside a valid name", "deploy", map[string]any{"env": "prod", "replicaz": "5"}, `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`},
		{"typo of a required name", "deploy", map[string]any{"evn": "prod"}, `unknown argument "evn" for task "deploy" (did you mean "env"?)`},
		{"nothing close", "build", map[string]any{"zzzzzz": "1"}, `unknown argument "zzzzzz" for task "build"`},
		{"no parameters", "clean_all", map[string]any{"force": true}, `unknown argument "force" for task "clean_all"`},
		{"case differs", "build", map[string]any{"Target": "release"}, `unknown argument "Target" for task "build" (did you mean "target"?)`},
		{"imported task", "tools__build", map[string]any{"targte": "release"}, `unknown argument "targte" for task "tools::build" (did you mean "target"?)`},
		{"correct and misspelled", "build", map[string]any{"target": "a", "targte": "b"}, `unknown argument "targte" for task "build" (did you mean "target"?)`},
		{"bad value too", "deploy", map[string]any{"env": "nope", "replicaz": "5"}, `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`},
		{
			"two names", "deploy",
			map[string]any{"replicaz": "5", "evn": "prod"},
			`unknown argument "evn" for task "deploy" (did you mean "env"?)` + "\n" +
				`unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`,
		},
		{"variadic", "each", map[string]any{"namez": []any{"a", "b"}}, `unknown argument "namez" for task "each" (did you mean "names"?)`},
		// Alphabetical order would pick tab; FR-004 picks the one declared first.
		{"tie", "tie", map[string]any{"tad": "1"}, `unknown argument "tad" for task "tie" (did you mean "tag"?)`},
		// Following a hint to a name already in the call would overwrite its value.
		{"tie prefers an unset parameter", "tie", map[string]any{"tag": "1", "tad": "2"}, `unknown argument "tad" for task "tie" (did you mean "tab"?)`},
		{"tie with both set", "tie", map[string]any{"tag": "1", "tab": "2", "tad": "3"}, `unknown argument "tad" for task "tie" (did you mean "tag"?)`},
		{"closest wins over unset", "tie", map[string]any{"tag": "1", "tags": "2"}, `unknown argument "tags" for task "tie" (did you mean "tag"?)`},
		{"case only, three edits", "deploy", map[string]any{"ENV": "prod"}, `unknown argument "ENV" for task "deploy" (did you mean "env"?)`},
		{"case only, six edits", "build", map[string]any{"TARGET": "x"}, `unknown argument "TARGET" for task "build" (did you mean "target"?)`},
		{"short name", "get", map[string]any{"x": "1"}, `unknown argument "x" for task "get" (did you mean "id"?)`},
		// build declares target, but a suggestion comes only from the called task.
		{"other task's parameter", "get", map[string]any{"targte": "1"}, `unknown argument "targte" for task "get"`},
		{"newline in name", "build", map[string]any{"a\nb": "1"}, `unknown argument "a\nb" for task "build"`},
		{"empty name", "build", map[string]any{"": "1"}, `unknown argument "" for task "build"`},
		{"quote in name", "build", map[string]any{`a"b`: "1"}, `unknown argument "a\"b" for task "build"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := unknownArgsEngine()
			res := callTool(t, connect(t, New(eng, Options{})), c.tool, c.args)
			if !res.IsError || contentText(res) != c.want {
				t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), c.want)
			}
			if eng.lastCall != "" {
				t.Errorf("engine ran %q", eng.lastCall)
			}
		})
	}
}

// Spec 032 FR-007, FR-008: a name is refused whatever its value is, and the
// value never appears, since an agent may put a secret in a misnamed argument.
func TestHandlerUnknownArgumentIgnoresValue(t *testing.T) {
	eng := unknownArgsEngine()
	cs := connect(t, New(eng, Options{}))
	const want = `unknown argument "zzzzzz" for task "build"`
	for _, v := range []any{"x", 1, true, nil, []any{"a"}, map[string]any{"k": "v"}} {
		res := callTool(t, cs, "build", map[string]any{"zzzzzz": v})
		if !res.IsError || contentText(res) != want {
			t.Errorf("value %#v: isError=%v text=%q, want %q", v, res.IsError, contentText(res), want)
		}
	}
	for _, v := range []any{"s3cret", []any{"s3cret"}} {
		res := callTool(t, cs, "build", map[string]any{"targte": v})
		if !res.IsError || strings.Contains(contentText(res), "s3cret") {
			t.Errorf("value %#v: isError=%v text=%q", v, res.IsError, contentText(res))
		}
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q", eng.lastCall)
	}
}

// Spec 032 FR-006: who may call a task comes before how to call it.
func TestHandlerUnknownArgumentAfterRefusals(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		tool string
		args map[string]any
		want string
	}{
		{
			"destructive",
			Options{AllowDestructiveFlag: "--allow-destructive"},
			"wipe",
			map[string]any{"forcee": true},
			`task "wipe" is destructive; to permit it, add wipe to --allow-destructive and restart the server`,
		},
		{
			"allowlist",
			Options{AllowList: []string{"get"}},
			"build",
			map[string]any{"targte": "x"},
			`task "build" is not authorized for this session`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := unknownArgsEngine()
			res := callTool(t, connect(t, New(eng, c.opts)), c.tool, c.args)
			if !res.IsError || contentText(res) != c.want {
				t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), c.want)
			}
			if eng.lastCall != "" {
				t.Errorf("engine ran %q", eng.lastCall)
			}
		})
	}
}

// Spec 032: a grant permits calling the task, not calling it with a typo.
func TestHandlerUnknownArgumentGrantedDestructive(t *testing.T) {
	eng := unknownArgsEngine()
	cs := connect(t, New(eng, Options{AllowDestructive: []string{"wipe"}, AllowDestructiveFlag: "--allow-destructive"}))

	res := callTool(t, cs, "wipe", map[string]any{"forcee": true})
	const want = `unknown argument "forcee" for task "wipe"`
	if !res.IsError || contentText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), want)
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q", eng.lastCall)
	}

	if res := callTool(t, cs, "wipe", nil); res.IsError || eng.lastCall != "wipe" {
		t.Errorf("the grant is not in effect: isError=%v lastCall=%q text=%q", res.IsError, eng.lastCall, contentText(res))
	}
}

// Spec 032 FR-011: the refusal holds for an engine that takes lists too.
func TestHandlerUnknownArgumentSkipsListEngine(t *testing.T) {
	le := &fakeListEngine{fakeEngine: fakeEngine{tasks: []TaskInfo{
		{Name: "c", Params: []ParamInfo{{Name: "names", Variadic: true}}},
	}}}
	res := callTool(t, connect(t, New(le, Options{})), "c", map[string]any{"namez": []any{"a", "b"}})
	const want = `unknown argument "namez" for task "c" (did you mean "names"?)`
	if !res.IsError || contentText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), want)
	}
	if le.viaLists || le.lastCall != "" {
		t.Errorf("engine ran: viaLists=%v lastCall=%q", le.viaLists, le.lastCall)
	}
}

// Spec 032 FR-005, SC-005: JSON objects decode to a Go map, whose order varies
// between runs, so repeating the call is what would expose an unsorted text.
func TestHandlerUnknownArgumentStableText(t *testing.T) {
	cs := connect(t, New(unknownArgsEngine(), Options{}))
	want := `unknown argument "evn" for task "deploy" (did you mean "env"?)` + "\n" +
		`unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)` + "\n" +
		`unknown argument "zzzzzz" for task "deploy"`
	for i := range 100 {
		res := callTool(t, cs, "deploy", map[string]any{"replicaz": "5", "evn": "prod", "zzzzzz": "1"})
		if contentText(res) != want {
			t.Fatalf("call %d: text=%q, want %q", i, contentText(res), want)
		}
	}
}

// Spec 032 FR-012: the handler is called directly so the raw bytes are exact.
// The client would otherwise decide how absent and null arguments are sent.
func TestHandlerRawArguments(t *testing.T) {
	task := TaskInfo{Name: "build", Params: []ParamInfo{{Name: "target"}}}
	call := func(raw json.RawMessage) (*fakeEngine, *mcp.CallToolResult) {
		eng := &fakeEngine{tasks: []TaskInfo{task}, result: Result{Stdout: "ran"}}
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: task.Name, Arguments: raw}}
		res, err := New(eng, Options{}).handler(task)(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return eng, res
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`)} {
		eng, res := call(raw)
		if res.IsError || eng.lastCall != "build" {
			t.Errorf("arguments %q: isError=%v lastCall=%q text=%q", raw, res.IsError, eng.lastCall, contentText(res))
		}
	}
	// A number only UseNumber can hold is not a decode error, for either kind of name.
	eng, res := call(json.RawMessage(`{"target":1e999}`))
	if res.IsError || eng.lastArgs["target"] != "1e999" {
		t.Errorf("declared huge number: isError=%v args=%v text=%q", res.IsError, eng.lastArgs, contentText(res))
	}
	eng, res = call(json.RawMessage(`{"zzzzzz":1e999}`))
	if want := `unknown argument "zzzzzz" for task "build"`; !res.IsError || contentText(res) != want || eng.lastCall != "" {
		t.Errorf("undeclared huge number: isError=%v lastCall=%q text=%q, want %q", res.IsError, eng.lastCall, contentText(res), want)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`"x"`)} {
		eng, res := call(raw)
		if !res.IsError || !strings.HasPrefix(contentText(res), "invalid arguments: json: cannot unmarshal") {
			t.Errorf("arguments %q: isError=%v text=%q", raw, res.IsError, contentText(res))
		}
		if eng.lastCall != "" {
			t.Errorf("arguments %q: engine ran %q", raw, eng.lastCall)
		}
	}
}

// Spec 032 FR-005: the refusal's size does not grow with the call, since it goes
// into the calling agent's context. A long name is cut at a character boundary.
func TestHandlerUnknownArgumentCapsEcho(t *testing.T) {
	eng := unknownArgsEngine()
	cs := connect(t, New(eng, Options{}))

	names := []struct{ name, want string }{
		{strings.Repeat("k", 64), `unknown argument "` + strings.Repeat("k", 64) + `" for task "build"`},
		{strings.Repeat("k", 65), `unknown argument "` + strings.Repeat("k", 64) + `..." for task "build"`},
		{strings.Repeat("k", 100_000), `unknown argument "` + strings.Repeat("k", 64) + `..." for task "build"`},
		{strings.Repeat("é", 65), `unknown argument "` + strings.Repeat("é", 64) + `..." for task "build"`},
	}
	for _, n := range names {
		res := callTool(t, cs, "build", map[string]any{n.name: "1"})
		if !res.IsError || contentText(res) != n.want {
			t.Errorf("name of %d bytes: isError=%v text=%q, want %q", len(n.name), res.IsError, contentText(res), n.want)
		}
	}

	lines := func(n int) (map[string]any, []string) {
		args := map[string]any{}
		var want []string
		for i := range n {
			name := fmt.Sprintf("zz%02d", i)
			args[name] = "1"
			if i < 20 {
				want = append(want, `unknown argument "`+name+`" for task "build"`)
			}
		}
		return args, want
	}
	args, want := lines(20)
	if res := callTool(t, cs, "build", args); contentText(res) != strings.Join(want, "\n") {
		t.Errorf("20 names: text=%q", contentText(res))
	}
	args, want = lines(25)
	want = append(want, `unknown arguments not shown for task "build": 5`)
	if res := callTool(t, cs, "build", args); !res.IsError || contentText(res) != strings.Join(want, "\n") {
		t.Errorf("25 names: isError=%v text=%q", res.IsError, contentText(res))
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q", eng.lastCall)
	}
}

// Spec 032 FR-015: JSON null and objects have no string a task could receive.
// fmt.Sprint would turn them into <nil> and map[k:v], and the task would run on
// that, a required parameter included.
func TestHandlerRefusesNullAndObjectValues(t *testing.T) {
	const (
		isNull   = "null is not a valid value"
		isObject = "an object is not a valid value"
		isNested = "a nested array is not a valid value"
	)
	cases := []struct {
		name, tool, param string
		value             any
		want              string
	}{
		{"null, defaulted", "build", "target", nil, isNull},
		{"null, required", "rm", "path", nil, isNull},
		{"null, typed", "deploy", "env", nil, isNull},
		{"null, variadic", "each", "names", nil, isNull},
		{"object", "build", "target", map[string]any{"k": "v"}, isObject},
		{"object, required", "rm", "path", map[string]any{}, isObject},
		{"null element", "each", "names", []any{"a", nil}, isNull},
		{"object element", "each", "names", []any{map[string]any{"k": "v"}}, isObject},
		{"nested array", "each", "names", []any{[]any{"a"}}, isNested},
		{"null element, not variadic", "build", "target", []any{nil}, isNull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := unknownArgsEngine()
			res := callTool(t, connect(t, New(eng, Options{})), c.tool, map[string]any{c.param: c.value})
			want := fmt.Sprintf("invalid arguments: parameter %q: %s", c.param, c.want)
			if !res.IsError || contentText(res) != want {
				t.Errorf("isError=%v text=%q, want %q", res.IsError, contentText(res), want)
			}
			if eng.lastCall != "" {
				t.Errorf("engine ran %q with %v", eng.lastCall, eng.lastArgs)
			}
		})
	}

	// Scalars keep reaching the task as their text.
	for _, c := range []struct {
		tool, param string
		value       any
		want        string
	}{
		{"build", "target", true, "true"},
		{"build", "target", 1, "1"},
		{"build", "target", "x", "x"},
		{"each", "names", []any{"a", 1, false}, "a 1 false"},
	} {
		eng := unknownArgsEngine()
		res := callTool(t, connect(t, New(eng, Options{})), c.tool, map[string]any{c.param: c.value})
		if res.IsError || eng.lastArgs[c.param] != c.want {
			t.Errorf("%s(%s=%v): isError=%v args=%v, want %q", c.tool, c.param, c.value, res.IsError, eng.lastArgs, c.want)
		}
	}
}

// Spec 032: with two invalid values, the parameter declared first is reported,
// so the same call always gets the same text. Ranging over the decoded map
// would report either one.
func TestHandlerInvalidValuesReportFirstDeclared(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{{Name: "pair", Params: []ParamInfo{
		{Name: "second", Kind: "number"},
		{Name: "first", Kind: "number"},
	}}}}
	cs := connect(t, New(eng, Options{}))
	const want = `invalid arguments: parameter "second": invalid value "x" (expected a number)`
	for i := range 100 {
		res := callTool(t, cs, "pair", map[string]any{"first": "x", "second": "x"})
		if contentText(res) != want {
			t.Fatalf("call %d: text=%q, want %q", i, contentText(res), want)
		}
	}
}
