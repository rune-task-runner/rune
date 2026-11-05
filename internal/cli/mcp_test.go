package cli

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/mcpserver"
)

func adapterFor(t *testing.T, src string) *mcpAdapter {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	scope := eval.NewScope(indexAssignments(f), map[string]string{})
	settings, _ := config.ResolveSettings(f, eval.New(scope))
	return &mcpAdapter{
		file:      f,
		tasks:     indexTasks(f),
		assigns:   indexAssignments(f),
		settings:  settings,
		root:      t.TempDir(),
		workDir:   t.TempDir(),
		baseEnv:   nil,
		overrides: map[string]string{},
		now:       func() string { return "" },
		goos:      runtime.GOOS,
	}
}

// TestAdapterExcludesOSMismatchedTasks: an agent must never see a task the
// host cannot run (spec 020 US1). Both transports (`rune mcp` stdio and
// `rune serve` HTTP) build this same adapter, so adapter-level assertions
// cover both.
func TestAdapterExcludesOSMismatchedTasks(t *testing.T) {
	src := "" +
		"everywhere:\n    @echo e\n" +
		"[windows]\nwin-only:\n    @echo w\n" +
		"[linux]\nlinux-only:\n    @echo l\n" +
		"[linux, windows]\neither:\n    @echo lw\n" +
		"[linux]\n[private]\nhidden-linux:\n    @echo h\n"
	a := adapterFor(t, src)
	a.goos = "linux"
	names := map[string]bool{}
	for _, ti := range a.Tasks() {
		names[ti.Name] = true
	}
	for _, want := range []string{"everywhere", "linux-only", "either"} {
		if !names[want] {
			t.Errorf("task %q should be exposed on linux: %v", want, names)
		}
	}
	if names["win-only"] {
		t.Errorf("[windows] task must not be exposed on linux: %v", names)
	}
	if names["hidden-linux"] {
		t.Errorf("private task must stay hidden even when OS matches: %v", names)
	}
}

// TestServerNeverRegistersOSMismatchedTools proves the whole agent surface
// consumes the filtered task set (spec 020 Story 1 scenario 4): tool
// registration skips the mismatched task, and even an allowlist naming it
// explicitly cannot resurrect it — authz narrows the filtered set, it never
// widens it.
func TestServerNeverRegistersOSMismatchedTools(t *testing.T) {
	src := "everywhere:\n    @echo e\n[windows]\nwin-only:\n    @echo w\n"
	a := adapterFor(t, src)
	a.goos = "linux"
	srv := mcpserver.New(a, mcpserver.Options{AllowList: []string{"win-only"}})

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

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name == "win-only" {
			t.Fatalf("OS-mismatched task registered as a tool: %v", res.Tools)
		}
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "win-only"}); err == nil {
		t.Error("calling the unregistered OS-mismatched tool must fail even when allowlisted")
	}
}

// A call naming an OS-mismatched task is refused with the availability
// error, not "unknown task" — defense-in-depth for agents that guessed or
// cached a tool name (spec 020 US2; contracts/mcp-surface.md).
func TestAdapterCallRefusesOSMismatchedTask(t *testing.T) {
	src := "everywhere:\n    @echo e\n[windows]\nwin-only:\n    @echo w\n"
	a := adapterFor(t, src)
	a.goos = "linux"
	_, err := a.Call(context.Background(), "win-only", nil)
	if err == nil {
		t.Fatal("OS-mismatched call accepted")
	}
	if strings.Contains(err.Error(), "unknown task") {
		t.Errorf("mismatched task misreported as unknown: %q", err.Error())
	}
	for _, part := range []string{`"win-only"`, "not available on linux", "requires windows"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("message %q missing %q", err.Error(), part)
		}
	}
	// Genuinely unknown names keep the existing diagnostic.
	if _, err := a.Call(context.Background(), "no-such-task", nil); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Errorf("unknown-task diagnostic changed: %v", err)
	}
}

func TestAdapterExcludesPrivateTasks(t *testing.T) {
	src := "logs:\n    @echo logs\n[private]\nsecret:\n    @echo s\n_hidden:\n    @echo h\n"
	a := adapterFor(t, src)
	names := map[string]bool{}
	for _, ti := range a.Tasks() {
		names[ti.Name] = true
	}
	if !names["logs"] {
		t.Error("public task logs should be exposed")
	}
	if names["secret"] || names["_hidden"] {
		t.Errorf("private tasks must not be exposed: %v", names)
	}
}

func TestAdapterNoSecretValuesInToolFields(t *testing.T) {
	// A variable holding a "secret" must never appear in any exposed tool field.
	src := "api_key := \"super-secret-value\"\n# Deploy the app.\ndeploy:\n    @echo deploying with {{api_key}}\n"
	a := adapterFor(t, src)
	for _, ti := range a.Tasks() {
		blob := ti.Name + " " + ti.Doc
		for _, p := range ti.Params {
			blob += " " + p.Name
		}
		if strings.Contains(blob, "super-secret-value") {
			t.Errorf("secret leaked into tool fields: %q", blob)
		}
	}
}

func TestAdapterCallRunsThroughEngine(t *testing.T) {
	src := "greet name=\"world\":\n    @echo hi {{name}}\n"
	a := adapterFor(t, src)
	res, err := a.Call(context.Background(), "greet", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hi Ada") {
		t.Errorf("call result = %+v", res)
	}
}

// The in-process agent adapter must derive a mask set from the engine's
// environment, so a task the agent calls back into cannot leak a secret value
// into the agent's chat history. Regression for the code-review finding that
// newAgentAdapter left maskSet nil (masking silently skipped).
func TestAgentAdapterMasksSecrets(t *testing.T) {
	const secret = "supersecretvalue123"
	src := "greet:\n    @echo " + secret + "\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	scope := eval.NewScope(indexAssignments(f), map[string]string{})
	settings, _ := config.ResolveSettings(f, eval.New(scope))
	eng := &engine{
		file:     f,
		tasks:    indexTasks(f),
		assigns:  indexAssignments(f),
		settings: settings,
		root:     t.TempDir(),
		workDir:  t.TempDir(),
		env:      []string{"MY_API_TOKEN=" + secret},
		now:      func() string { return "" },
	}

	adapter := eng.newAgentAdapter()
	if adapter.maskSet.Empty() {
		t.Fatal("agent adapter maskSet is empty; secret values would leak into agent write-back")
	}
	res, err := adapter.Call(context.Background(), "greet", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, secret) {
		t.Errorf("agent adapter leaked the secret: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "***") {
		t.Errorf("agent adapter should mask the secret with ***: %q", res.Stdout)
	}
}

// 014 US2 (C7): an MCP tool result must never carry ANSI — the adapter builds
// its engine Options with both color booleans false, so every status line and
// echoed command captured in the buffers is plain by construction.
func TestAdapterResultCarriesNoANSI(t *testing.T) {
	src := "greet:\n    echo hi-from-task\n"
	a := adapterFor(t, src)
	res, err := a.Call(context.Background(), "greet", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(res.Stdout, '\x1b') || strings.ContainsRune(res.Stderr, '\x1b') {
		t.Errorf("tool result carried ANSI: stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hi-from-task") {
		t.Errorf("missing task output: %+v", res)
	}
}

func TestAdapterDestructiveFlag(t *testing.T) {
	src := "[confirm(\"sure?\")]\nclean:\n    @echo clean\nlogs:\n    @echo logs\n"
	a := adapterFor(t, src)
	for _, ti := range a.Tasks() {
		if ti.Name == "clean" && !ti.Destructive {
			t.Error("clean should be marked destructive")
		}
		if ti.Name == "logs" && ti.Destructive {
			t.Error("logs should not be destructive")
		}
	}
	_ = ast.AttrConfirm
}

// Spec 031 US1: a granted [confirm] task runs over MCP with its real output
// and exit status, and no prompt text appears anywhere in the result.
func TestAdapterGrantRunsDestructiveTask(t *testing.T) {
	src := "[confirm(\"Delete?\")]\nclean:\n    @echo cleaned\n\n[confirm]\nfail3:\n    @exit 3\n"
	ctx := context.Background()

	ungranted := adapterFor(t, src)
	res, err := ungranted.Call(ctx, "clean", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 || strings.Contains(res.Stdout, "cleaned") {
		t.Fatalf("clean ran without a grant: %+v", res)
	}

	a := adapterFor(t, src)
	a.grants = map[string]bool{"clean": true, "fail3": true}
	res, err = a.Call(ctx, "clean", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "cleaned") {
		t.Errorf("granted clean: %+v", res)
	}
	res3, err := a.Call(ctx, "fail3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res3.ExitCode != 3 {
		t.Errorf("granted fail3 exit = %d, want 3 (%+v)", res3.ExitCode, res3)
	}
	for _, r := range []mcpserver.Result{res, res3} {
		for _, s := range []string{r.Stdout, r.Stderr} {
			if strings.Contains(s, "Delete?") || strings.Contains(s, "[y/N]") {
				t.Errorf("prompt text in result: %+v", r)
			}
		}
	}
}

// Spec 031 FR-015: the endpoint an agent task exposes to its own agent never
// permits a destructive task, even if the engine running the agent task held
// a grant. Neither the adapter nor its server gets one.
func TestAgentAdapterGrantsNothing(t *testing.T) {
	src := "[confirm]\nclean:\n    @echo cleaned\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	eng.grants = map[string]bool{"clean": true}

	adapter := eng.newAgentAdapter()
	if len(adapter.grants) != 0 {
		t.Fatalf("agent adapter inherited grants %v", adapter.grants)
	}
	res, err := adapter.Call(context.Background(), "clean", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 || strings.Contains(res.Stdout, "cleaned") {
		t.Errorf("agent adapter ran a destructive task: %+v", res)
	}

	srv := mcpserver.New(adapter, mcpserver.Options{Version: "test"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	call, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "clean"})
	if err != nil {
		t.Fatal(err)
	}
	// No restart advice: no flag can grant anything on this endpoint.
	want := `task "clean" is destructive and is not allowed in this session`
	var text strings.Builder
	for _, c := range call.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !call.IsError || text.String() != want {
		t.Errorf("agent endpoint: isError=%v text=%q, want %q", call.IsError, text.String(), want)
	}
}
