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
	srv := New(eng, Options{AllowDestructive: true})
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
	srv := New(eng, Options{AllowDestructive: true})
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
		"path": "string", "enum": "string",
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
