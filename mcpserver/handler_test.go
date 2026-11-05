package mcpserver

import (
	"context"
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
func TestDecodeArgsKeepsNumberSpelling(t *testing.T) {
	params := []ParamInfo{
		{Name: "replicas", Kind: "number"},
		{Name: "nums", Variadic: true, Kind: "number"},
	}
	args, _, err := decodeArgs([]byte(`{"replicas": 1000000, "nums": [0.0000001, 12345678901234567890]}`), params)
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
		args, lists, err := decodeArgs([]byte(tt.raw), params)
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

	args, lists, err := decodeArgs([]byte(`{"one": ["x", "y"], "unknown": ["p", "q"]}`), params)
	if err != nil {
		t.Fatal(err)
	}
	if args["one"] != "x y" || args["unknown"] != "p q" {
		t.Errorf("non-variadic arrays changed: %v", args)
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
