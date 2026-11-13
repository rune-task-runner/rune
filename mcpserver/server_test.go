package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/ast"
)

type fakeEngine struct {
	tasks    []TaskInfo
	lastCall string
	lastArgs map[string]string
	result   Result
}

func (f *fakeEngine) Tasks() []TaskInfo { return f.tasks }

func (f *fakeEngine) Call(_ context.Context, name string, args map[string]string) (Result, error) {
	f.lastCall = name
	f.lastArgs = args
	return f.result, nil
}

func sampleEngine() *fakeEngine {
	return &fakeEngine{
		tasks: []TaskInfo{
			{Name: "logs", Doc: "Show recent git log."},
			{Name: "greet", Doc: "Greet someone.", Params: []ParamInfo{{Name: "name", Required: true}}},
			{Name: "clean", Doc: "Remove build output.", Destructive: true},
			{Name: "fetch", Doc: "Fetch a URL.", Network: true},
			{Name: "docker::push", Doc: "Push image."},
		},
		result: Result{Stdout: "output-here", ExitCode: 0},
	}
}

func TestToolNameNamespacing(t *testing.T) {
	if got := toolName("docker::push"); got != "docker__push" {
		t.Errorf("toolName = %q, want docker__push", got)
	}
	if got := toolName("plain"); got != "plain" {
		t.Errorf("toolName = %q, want plain", got)
	}
}

func TestInputSchema(t *testing.T) {
	schema := inputSchema([]ParamInfo{
		{Name: "a", Required: true},
		{Name: "b"},
		{Name: "rest", Variadic: true},
	})
	props := schema["properties"].(map[string]any)
	if props["a"] == nil || props["b"] == nil {
		t.Fatalf("missing properties: %v", props)
	}
	if rest := props["rest"].(map[string]any); rest["type"] != "array" {
		t.Errorf("variadic param should be an array, got %v", rest)
	}
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "a" {
		t.Errorf("required = %v, want [a]", req)
	}
}

func TestToolAnnotations(t *testing.T) {
	clean := toolFor(TaskInfo{Name: "clean", Destructive: true})
	if clean.Annotations.DestructiveHint == nil || !*clean.Annotations.DestructiveHint {
		t.Error("clean should have destructiveHint=true")
	}
	fetch := toolFor(TaskInfo{Name: "fetch", Network: true})
	if fetch.Annotations.OpenWorldHint == nil || !*fetch.Annotations.OpenWorldHint {
		t.Error("fetch should have openWorldHint=true")
	}
}

// connect wires a client to the server over an in-memory transport.
func connect(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestListToolsMapping(t *testing.T) {
	eng := sampleEngine()
	srv := New(eng, Options{})
	cs := connect(t, srv)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	if byName["logs"] == nil || byName["logs"].Description != "Show recent git log." {
		t.Errorf("logs tool wrong: %+v", byName["logs"])
	}
	if byName["docker__push"] == nil {
		t.Errorf("submodule task not namespaced as docker__push: %v", byName)
	}
	if d := byName["clean"]; d == nil || d.Annotations.DestructiveHint == nil || !*d.Annotations.DestructiveHint {
		t.Errorf("clean destructiveHint missing: %+v", d)
	}
}

func TestCallToolThroughEngine(t *testing.T) {
	eng := sampleEngine()
	srv := New(eng, Options{})
	cs := connect(t, srv)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "logs"})
	if err != nil {
		t.Fatal(err)
	}
	if eng.lastCall != "logs" {
		t.Errorf("engine called with %q, want logs", eng.lastCall)
	}
	text := contentText(res)
	if !strings.Contains(text, "output-here") {
		t.Errorf("result missing engine output: %q", text)
	}
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestInputSchemaTyped pins the kind → JSON Schema mapping
// (specs/023-mcp-typed-schemas/contracts/mcp-schema.md): enum value lists,
// typed numbers/booleans, path format, per-parameter descriptions, and typed
// variadic arrays. The unannotated property must stay byte-identical to the
// pre-023 shape (SC-003).
func TestInputSchemaTyped(t *testing.T) {
	schema := inputSchema([]ParamInfo{
		{Name: "env", Required: true, Kind: "enum", Enum: []string{"staging", "prod"}, Description: "Target environment"},
		{Name: "replicas", Kind: "number"},
		{Name: "verbose", Kind: "boolean"},
		{Name: "target", Kind: "path"},
		{Name: "plain"},
		{Name: "packages", Variadic: true, Required: true, Kind: "path"},
	})
	props := schema["properties"].(map[string]any)

	env := props["env"].(map[string]any)
	if env["type"] != "string" {
		t.Errorf(`env type = %v, want "string"`, env["type"])
	}
	if got, ok := env["enum"].([]string); !ok || len(got) != 2 || got[0] != "staging" || got[1] != "prod" {
		t.Errorf("env enum = %v, want [staging prod]", env["enum"])
	}
	if env["description"] != "Target environment" {
		t.Errorf("env description = %v", env["description"])
	}
	if got := props["replicas"].(map[string]any)["type"]; got != "number" {
		t.Errorf("replicas type = %v, want number", got)
	}
	if got := props["verbose"].(map[string]any)["type"]; got != "boolean" {
		t.Errorf("verbose type = %v, want boolean", got)
	}
	target := props["target"].(map[string]any)
	if target["type"] != "string" || target["format"] != "path" {
		t.Errorf("target = %v, want string with format path", target)
	}
	if !reflect.DeepEqual(props["plain"], map[string]any{"type": "string"}) {
		t.Errorf("unannotated param drifted from the pre-023 shape: %v", props["plain"])
	}
	pkgs := props["packages"].(map[string]any)
	if pkgs["type"] != "array" {
		t.Fatalf("packages = %v, want array", pkgs)
	}
	items := pkgs["items"].(map[string]any)
	if items["type"] != "string" || items["format"] != "path" {
		t.Errorf("packages items = %v, want typed items", items)
	}
}

// TestToolForReturnsTrailer pins the outcome-description composition
// (spec 023 US3, contracts/mcp-schema.md): a Returns trailer after the doc,
// no invented placeholder when absent.
func TestToolForReturnsTrailer(t *testing.T) {
	tool := toolFor(TaskInfo{Name: "deploy", Doc: "Deploy the service.", Returns: "URL of the deployed environment"})
	if want := "Deploy the service.\n\nReturns: URL of the deployed environment"; tool.Description != want {
		t.Errorf("description = %q, want %q", tool.Description, want)
	}
	if got := toolFor(TaskInfo{Name: "logs", Doc: "Show logs."}).Description; got != "Show logs." {
		t.Errorf("no-returns description drifted: %q", got)
	}
	if got := toolFor(TaskInfo{Name: "ids", Returns: "one ID per line"}).Description; got != "Returns: one ID per line" {
		t.Errorf("doc-less returns composition = %q", got)
	}
}

// TestParamCheckMatchesASTConstraint pins mcpserver's ParamInfo.check to
// ast.Constraint.Check. The two validators are deliberate copies (the
// non-test mcpserver package imports no internal packages), so this test is
// what keeps a value from being accepted on one transport and rejected on
// another — tightening one rule without the other fails here.
func TestParamCheckMatchesASTConstraint(t *testing.T) {
	values := []string{
		"", "abc", "true", "false", "TRUE", "1", "-2.5", "1e5", "007", ".5",
		"NaN", "nan", "Inf", "+Inf", "infinity", "0x1p4", "1_000",
		"staging", "prod", "a b",
	}
	enum := []string{"staging", "prod"}
	for _, kindName := range ast.SupportedConstraintKinds {
		kind, ok := ast.ConstraintKindFromName(kindName)
		if !ok {
			t.Fatalf("kind %q listed in SupportedConstraintKinds but unresolvable", kindName)
		}
		c := &ast.Constraint{Kind: kind, KindName: kindName}
		p := ParamInfo{Name: "v", Kind: kindName}
		if kindName == "enum" {
			c.Values = enum
			p.Enum = enum
		}
		for _, v := range values {
			astOK := c.Check(v) == nil
			mcpOK := p.check(v) == nil
			if astOK != mcpOK {
				t.Errorf("kind %s value %q: ast accepts=%v, mcpserver accepts=%v",
					kindName, v, astOK, mcpOK)
			}
		}
	}
}

// TestScalarTypeCoversAllKinds forces a conscious JSON-schema decision for
// every supported kind: a kind added to ast without extending scalarType,
// inputSchema, and this map together fails here instead of silently
// advertising an untyped string the engine then enforces differently.
func TestScalarTypeCoversAllKinds(t *testing.T) {
	wantJSON := map[string]string{
		"string": "string", "number": "number", "boolean": "boolean",
		"path": "string", "raw": "string", "enum": "string",
	}
	for _, kind := range ast.SupportedConstraintKinds {
		want, ok := wantJSON[kind]
		if !ok {
			t.Fatalf("kind %q has no pinned JSON schema type: extend scalarType and this test together", kind)
		}
		if got := scalarType(kind); got != want {
			t.Errorf("scalarType(%q) = %q, want %q", kind, got, want)
		}
	}
	if got := scalarType(""); got != "string" {
		t.Errorf(`scalarType("") = %q, want "string" (the unannotated pre-023 shape)`, got)
	}
}

// Spec 030 FR-019 (contracts/mcp.md, "Schema mapping delta"): a raw parameter
// tells agents its value is split into shell words.
func TestInputSchemaRaw(t *testing.T) {
	const sentence = "Split into shell words without quoting."
	schema := inputSchema([]ParamInfo{
		{Name: "a", Kind: "raw"},
		{Name: "b", Kind: "raw", Description: "Extra flags"},
		{Name: "c", Kind: "raw", Description: "Extra flags."},
		{Name: "d", Kind: "raw", Variadic: true},
		{Name: "e", Kind: "string", Description: "Extra flags"},
	})
	props := schema["properties"].(map[string]any)
	want := map[string]any{
		"a": map[string]any{"type": "string", "description": sentence},
		"b": map[string]any{"type": "string", "description": "Extra flags. " + sentence},
		"c": map[string]any{"type": "string", "description": "Extra flags. " + sentence},
		"d": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": sentence},
		"e": map[string]any{"type": "string", "description": "Extra flags"},
	}
	for name, w := range want {
		if !reflect.DeepEqual(props[name], w) {
			t.Errorf("%s = %v, want %v", name, props[name], w)
		}
	}
}

// Spec 031 FR-014: a grant changes what may be called, never what is listed.
func TestListToolsSameWithGrant(t *testing.T) {
	list := func(opts Options) []*mcp.Tool {
		res, err := connect(t, New(sampleEngine(), opts)).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Tools
	}
	without := list(Options{})
	with := list(Options{AllowDestructive: []string{"clean"}})
	if !reflect.DeepEqual(without, with) {
		t.Errorf("tools/list changed with a grant:\nwithout: %+v\nwith:    %+v", without, with)
	}
}

// Spec 032 FR-009: every schema says that no other names are allowed, and
// nothing else in it changes.
func TestInputSchemaDisallowsOtherNames(t *testing.T) {
	shapes := map[string][]ParamInfo{
		"no parameters": nil,
		"defaulted":     {{Name: "a"}},
		"required":      {{Name: "a", Required: true}},
		"enum":          {{Name: "a", Kind: "enum", Enum: []string{"x"}}},
		"number":        {{Name: "a", Kind: "number"}},
		"boolean":       {{Name: "a", Kind: "boolean"}},
		"path":          {{Name: "a", Kind: "path"}},
		"raw":           {{Name: "a", Kind: "raw"}},
		"variadic":      {{Name: "a", Variadic: true}},
	}
	for name, params := range shapes {
		if got := inputSchema(params)["additionalProperties"]; got != false {
			t.Errorf("%s: additionalProperties = %v, want false", name, got)
		}
	}

	str := map[string]any{"type": "string"}
	full := []struct {
		params []ParamInfo
		want   map[string]any
	}{
		{
			[]ParamInfo{{Name: "target"}},
			map[string]any{"additionalProperties": false, "properties": map[string]any{"target": str}, "type": "object"},
		},
		{
			nil,
			map[string]any{"additionalProperties": false, "properties": map[string]any{}, "type": "object"},
		},
		{
			[]ParamInfo{{Name: "env", Required: true}, {Name: "replicas"}},
			map[string]any{
				"additionalProperties": false,
				"properties":           map[string]any{"env": str, "replicas": str},
				"required":             []string{"env"},
				"type":                 "object",
			},
		},
	}
	for _, f := range full {
		if got := inputSchema(f.params); !reflect.DeepEqual(got, f.want) {
			t.Errorf("inputSchema(%+v) = %v, want %v", f.params, got, f.want)
		}
	}
}

// Spec 032 US2: a client reading the list sees the keyword on every tool, a
// destructive or namespaced one included.
func TestListToolsDisallowOtherNames(t *testing.T) {
	res, err := connect(t, New(sampleEngine(), Options{})).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 5 {
		t.Fatalf("listed %d tools, want 5", len(res.Tools))
	}
	for _, tl := range res.Tools {
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: InputSchema is %T", tl.Name, tl.InputSchema)
		}
		if schema["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties = %v, want false", tl.Name, schema["additionalProperties"])
		}
		if tl.Name == "logs" {
			if props, ok := schema["properties"].(map[string]any); !ok || len(props) != 0 {
				t.Errorf("logs: properties = %v, want an empty object", schema["properties"])
			}
		}
	}
}

// hintSet is a tool's three hints as plain booleans.
type hintSet struct{ readOnly, destructive, openWorld bool }

// hintsOf reads a tool's hints. The server sets both pointer hints on every
// tool, so a nil one fails the test (spec 033 R5).
func hintsOf(t *testing.T, tool *mcp.Tool) hintSet {
	t.Helper()
	a := tool.Annotations
	if a == nil || a.DestructiveHint == nil || a.OpenWorldHint == nil {
		t.Fatalf("%s: annotations %+v, want both pointer hints set", tool.Name, a)
	}
	return hintSet{readOnly: a.ReadOnlyHint, destructive: *a.DestructiveHint, openWorld: *a.OpenWorldHint}
}

var readOnlyCases = []struct {
	info TaskInfo
	want hintSet
}{
	{TaskInfo{Name: "deploy"}, hintSet{}},
	{TaskInfo{Name: "status", ReadOnly: true}, hintSet{readOnly: true}},
	{TaskInfo{Name: "clean", Destructive: true}, hintSet{destructive: true}},
	{TaskInfo{Name: "fetch", Network: true}, hintSet{openWorld: true}},
}

// Spec 033 FR-002, FR-012: only a descriptor that declares ReadOnly claims
// it, and one that sets nothing gets hints from its own flags alone.
func TestToolHintsReadOnly(t *testing.T) {
	for _, c := range readOnlyCases {
		if got := hintsOf(t, toolFor(c.info)); got != c.want {
			t.Errorf("%s: hints %+v, want %+v", c.info.Name, got, c.want)
		}
	}
}

// Spec 033 FR-012: a client reads the same hints off the wire.
func TestListToolsReadOnlyThroughClient(t *testing.T) {
	eng := &fakeEngine{}
	want := map[string]hintSet{}
	for _, c := range readOnlyCases {
		eng.tasks = append(eng.tasks, c.info)
		want[c.info.Name] = c.want
	}
	res, err := connect(t, New(eng, Options{})).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(want) {
		t.Fatalf("listed %d tools, want %d", len(res.Tools), len(want))
	}
	for _, tool := range res.Tools {
		if got := hintsOf(t, tool); got != want[tool.Name] {
			t.Errorf("%s: hints %+v, want %+v", tool.Name, got, want[tool.Name])
		}
	}
}

// listedTools lists engine's tools through a client of a server built with opts.
func listedTools(t *testing.T, eng Engine, opts Options) []*mcp.Tool {
	t.Helper()
	res, err := connect(t, New(eng, opts)).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

// Spec 033 FR-003, FR-012: a descriptor that reaches a destructive task
// advertises the destructive hint, whether or not it is destructive itself.
// An empty list reaches nothing (spec 034 R2).
func TestToolHintsDestructiveReach(t *testing.T) {
	for _, c := range []struct {
		info TaskInfo
		want hintSet
	}{
		{TaskInfo{Name: "release", DestructiveReach: []string{"clean"}}, hintSet{destructive: true}},
		{TaskInfo{Name: "clean", Destructive: true, DestructiveReach: []string{"wipe"}}, hintSet{destructive: true}},
		{TaskInfo{Name: "logs", DestructiveReach: []string{}}, hintSet{}},
	} {
		if got := hintsOf(t, toolFor(c.info)); got != c.want {
			t.Errorf("%s: hints %+v, want %+v", c.info.Name, got, c.want)
		}
	}
}

// Spec 033 FR-010: a grant changes no hint, also for a task that only reaches
// a destructive one.
func TestListToolsSameWithGrantWhenReaching(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "clean", Destructive: true},
	}}
	without := listedTools(t, eng, Options{})
	with := listedTools(t, eng, Options{AllowDestructive: []string{"clean"}})
	if !reflect.DeepEqual(without, with) {
		t.Errorf("tools/list changed with a grant:\nwithout: %+v\nwith:    %+v", without, with)
	}
}

// Spec 041 FR-008: an allowlist changes which tools are listed and nothing
// about the ones left.
func TestAllowListKeepsListedToolsUnchanged(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "status", ReadOnly: true, Doc: "Shows status.", Params: []ParamInfo{{Name: "verbose"}}},
	}}
	without := listedTools(t, eng, Options{})
	with := listedTools(t, eng, Options{AllowList: []string{"status"}})
	if len(with) != 1 {
		t.Fatalf("listed %d tools, want 1: %+v", len(with), with)
	}
	if !reflect.DeepEqual(with[0], without[1]) {
		t.Errorf("status changed under an allowlist:\nwithout: %+v\nwith:    %+v", without[1], with[0])
	}
}

// listedNames lists the tool names a client of a server built with opts sees.
func listedNames(t *testing.T, eng Engine, opts Options) []string {
	t.Helper()
	var names []string
	for _, tl := range listedTools(t, eng, opts) {
		names = append(names, tl.Name)
	}
	return names
}

// Spec 041 FR-002, FR-003: only allowlisted tasks are registered, and the
// server counts them.
func TestAllowListRegistersOnlyNamed(t *testing.T) {
	eng := sampleEngine()
	opts := Options{AllowList: []string{"logs"}}
	if got := listedNames(t, eng, opts); !reflect.DeepEqual(got, []string{"logs"}) {
		t.Errorf("tools = %q, want only logs", got)
	}
	exposed, exposable := New(eng, opts).Exposure()
	if exposed != 1 || exposable != len(eng.tasks) {
		t.Errorf("Exposure() = %d, %d, want 1, %d", exposed, exposable, len(eng.tasks))
	}
}

// Spec 041 FR-004, SC-003: a call to a hidden task gets the response a call to
// a name no task has gets, apart from the name it echoes.
func TestHiddenCallEqualsMissingCall(t *testing.T) {
	eng := sampleEngine()
	cs := connect(t, New(eng, Options{AllowList: []string{"logs"}}))
	_, hidden := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "greet"})
	_, missing := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nosuch"})
	if hidden == nil || missing == nil {
		t.Fatalf("hidden err = %v, missing err = %v, want both non-nil", hidden, missing)
	}
	h := strings.ReplaceAll(hidden.Error(), "greet", "NAME")
	m := strings.ReplaceAll(missing.Error(), "nosuch", "NAME")
	if h != m {
		t.Errorf("responses differ:\nhidden:  %s\nmissing: %s", hidden, missing)
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q", eng.lastCall)
	}
}

// Spec 041 FR-002: the denylist removes tasks, wins over the allowlist, and
// ignores names no task has. The SDK lists tools sorted by name.
func TestDenyListHides(t *testing.T) {
	all := []string{"clean", "docker__push", "fetch", "greet", "logs"}
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{"deny one", Options{DenyList: []string{"logs"}}, all[:4]},
		{"deny wins", Options{AllowList: []string{"logs", "greet"}, DenyList: []string{"logs"}}, []string{"greet"}},
		{"nothing left", Options{AllowList: []string{"logs"}, DenyList: []string{"logs"}}, nil},
		{"unknown name", Options{DenyList: []string{"nosuch"}}, all},
		{"namespaced by task name", Options{DenyList: []string{"docker::push"}}, []string{"clean", "fetch", "greet", "logs"}},
		{"tool name is not a task name", Options{DenyList: []string{"docker__push"}}, all},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := sampleEngine()
			if got := listedNames(t, eng, c.opts); !reflect.DeepEqual(got, c.want) {
				t.Errorf("tools = %q, want %q", got, c.want)
			}
			exposed, exposable := New(eng, c.opts).Exposure()
			if exposed != len(c.want) || exposable != len(all) {
				t.Errorf("Exposure() = %d, %d, want %d, %d", exposed, exposable, len(c.want), len(all))
			}
		})
	}
}

func TestExposes(t *testing.T) {
	cases := []struct {
		allow, deny []string
		want        bool
	}{
		{nil, nil, true},
		{[]string{"a"}, nil, true},
		{[]string{"b"}, nil, false},
		{nil, []string{"a"}, false},
		{nil, []string{"b"}, true},
		{[]string{"a"}, []string{"a"}, false},
		{[]string{"a"}, []string{"b"}, true},
		{[]string{"b"}, []string{"a"}, false},
	}
	for _, c := range cases {
		srv := New(&fakeEngine{}, Options{AllowList: c.allow, DenyList: c.deny})
		if got := srv.exposes("a"); got != c.want {
			t.Errorf("allow %q, deny %q: exposes(a) = %v, want %v", c.allow, c.deny, got, c.want)
		}
	}
}

// Spec 041 FR-007, FR-008: an exposed task keeps the destructive hint and the
// refusal for a hidden destructive task it can reach.
func TestFilterKeepsHintsOfReachingTask(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{
		{Name: "release", DestructiveReach: []string{"clean"}},
		{Name: "clean", Destructive: true},
	}}
	srv := New(eng, Options{AllowList: []string{"release"}, AllowDestructiveFlag: "--allow-destructive"})
	cs := connect(t, srv)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "release" {
		t.Fatalf("tools = %+v, want only release", res.Tools)
	}
	if h := res.Tools[0].Annotations.DestructiveHint; h == nil || !*h {
		t.Errorf("release lost its destructive hint: %+v", res.Tools[0].Annotations)
	}
	call, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	want := `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
	if !call.IsError || contentText(call) != want {
		t.Errorf("isError=%v text=%q, want %q", call.IsError, contentText(call), want)
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q", eng.lastCall)
	}
}

// Spec 041 R3: with no filter every offered task is exposed.
func TestExposureUnfiltered(t *testing.T) {
	eng := sampleEngine()
	exposed, exposable := New(eng, Options{}).Exposure()
	if want := len(eng.Tasks()); exposed != want || exposable != want {
		t.Errorf("Exposure() = %d, %d, want %d, %d", exposed, exposable, want, want)
	}
}

// A filter that leaves no tool still advertises the tools capability, so a
// client sees an empty tool list, not a server without tool support (spec 041
// FR-013). Without a filter the capabilities are as before.
func TestEmptyFilteredServerAdvertisesTools(t *testing.T) {
	filtered := connect(t, New(sampleEngine(), Options{AllowList: []string{"logs"}, DenyList: []string{"logs"}}))
	if filtered.InitializeResult().Capabilities.Tools == nil {
		t.Error("a filtered server with no tools left does not advertise the tools capability")
	}
	res, err := filtered.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) != 0 {
		t.Errorf("ListTools = %v, %v, want an empty list and no error", res, err)
	}
	unfiltered := connect(t, New(&fakeEngine{}, Options{}))
	if unfiltered.InitializeResult().Capabilities.Tools != nil {
		t.Error("an unfiltered server with no tasks changed its capabilities")
	}
}

// A filtered server states the capabilities the SDK infers for an unfiltered
// one with tools, so the filter changes nothing else a client negotiates.
func TestFilteredCapabilitiesMatchInferred(t *testing.T) {
	inferred := connect(t, New(sampleEngine(), Options{})).InitializeResult().Capabilities
	for _, opts := range []Options{
		{AllowList: []string{"logs"}},
		{DenyList: []string{"logs"}},
		{AllowList: []string{"logs"}, DenyList: []string{"logs"}},
	} {
		got := connect(t, New(sampleEngine(), opts)).InitializeResult().Capabilities
		if !reflect.DeepEqual(got, inferred) {
			t.Errorf("%+v: capabilities = %+v, want the inferred %+v", opts, got, inferred)
		}
	}
	if capabilities(Options{}) != nil {
		t.Error("an unfiltered server states capabilities instead of letting the SDK infer them")
	}
}
