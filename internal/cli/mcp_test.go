package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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
	cs := serverSession(t, mcpserver.New(a, mcpserver.Options{AllowList: []string{"win-only"}}))
	ctx := context.Background()

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

	cs := serverSession(t, mcpserver.New(adapter, mcpserver.Options{Version: "test"}))
	call, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "clean"})
	if err != nil {
		t.Fatal(err)
	}
	// No restart advice: no flag can grant anything on this endpoint.
	want := `task "clean" is destructive and is not allowed in this session`
	if !call.IsError || resultText(call) != want {
		t.Errorf("agent endpoint: isError=%v text=%q, want %q", call.IsError, resultText(call), want)
	}
}

// serverSession connects a client to srv over an in-memory transport.
func serverSession(t *testing.T, srv *mcpserver.Server) *mcp.ClientSession {
	t.Helper()
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
	return cs
}

func resultText(res *mcp.CallToolResult) string {
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String()
}

// Spec 032 FR-010: the agent-task endpoint refuses an undeclared argument, and
// the declared call afterward shows the marker can detect a run.
func TestAgentEndpointRefusesUnknownArgument(t *testing.T) {
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "build.done"))
	src := "build target=\"debug\":\n    @echo {{quote(target)}} > '" + marker + "'\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	cs := serverSession(t, mcpserver.New(eng.newAgentAdapter(), mcpserver.Options{Version: "test"}))
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "build", Arguments: map[string]any{"targte": "release"}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `unknown argument "targte" for task "build" (did you mean "target"?)`
	if !res.IsError || resultText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, resultText(res), want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the task ran although refused")
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "build", Arguments: map[string]any{"target": "release"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if res.IsError || err != nil || strings.TrimSpace(string(got)) != "release" {
		t.Errorf("declared call: isError=%v marker=%q err=%v", res.IsError, got, err)
	}
}

// Spec 032 FR-010: the agent-task endpoint lists the keyword on every tool.
func TestAgentEndpointSchemasDisallowOtherNames(t *testing.T) {
	src := "build target=\"debug\":\n    @echo built\n\nclean_all:\n    @echo cleaned\n\n[confirm]\nwipe:\n    @echo wiped\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	cs := serverSession(t, mcpserver.New(eng.newAgentAdapter(), mcpserver.Options{Version: "test"}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("listed %d tools, want 3", len(res.Tools))
	}
	for _, tl := range res.Tools {
		if schema, ok := tl.InputSchema.(map[string]any); !ok || schema["additionalProperties"] != false {
			t.Errorf("%s: InputSchema = %v, want additionalProperties false", tl.Name, tl.InputSchema)
		}
	}
}

// Spec 033 FR-002: only a task with [readonly] is reported read-only.
func TestAdapterReadOnlyFlag(t *testing.T) {
	src := "[readonly]\nstatus:\n    @echo status\ndeploy:\n    @echo deploy\n[confirm]\nclean:\n    @echo clean\n"
	a := adapterFor(t, src)
	want := map[string]bool{"status": true, "deploy": false, "clean": false}
	tasks := a.Tasks()
	if len(tasks) != len(want) {
		t.Fatalf("Tasks() = %+v, want %d tasks", tasks, len(want))
	}
	for _, ti := range tasks {
		if ti.ReadOnly != want[ti.Name] {
			t.Errorf("%s: ReadOnly = %v, want %v", ti.Name, ti.ReadOnly, want[ti.Name])
		}
	}
}

// reach is one tool's destructive and network declarations as the adapter
// reports them.
type reach struct {
	destructive bool
	reaches     []string
	network     bool
}

func reachOf(a *mcpAdapter) map[string]reach {
	got := map[string]reach{}
	for _, ti := range a.Tasks() {
		got[ti.Name] = reach{destructive: ti.Destructive, reaches: ti.DestructiveReach, network: ti.Network}
	}
	return got
}

func (r reach) equal(o reach) bool {
	return r.destructive == o.destructive && r.network == o.network && slices.Equal(r.reaches, o.reaches)
}

// Spec 033 FR-003 to FR-005, FR-014, SC-002: a task inherits the [confirm] and
// [network] of everything its dependencies and hooks run, and nothing else.
// Destructive stays the task's own [confirm]. Spec 034 FR-005: the reached
// [confirm] tasks are listed once each, in the order a run first reaches them.
func TestAdapterReach(t *testing.T) {
	const clean = "[confirm]\nclean:\n    @echo clean\n"
	cases := []struct {
		name string
		src  string
		want map[string]reach
	}{
		{
			"dependency", "release: clean\n    @echo release\n" + clean,
			map[string]reach{"release": {reaches: []string{"clean"}}, "clean": {destructive: true}},
		},
		{
			"post-hook", "post: && clean\n    @echo post\n" + clean,
			map[string]reach{"post": {reaches: []string{"clean"}}},
		},
		{
			"failure hook", "bad: || clean\n    @exit 1\n" + clean,
			map[string]reach{"bad": {reaches: []string{"clean"}}},
		},
		{
			"three levels, confirm", "a: b\n    @echo a\nb: && c\n    @echo b\nc: || d\n    @echo c\n[confirm]\nd:\n    @echo d\n",
			map[string]reach{"a": {reaches: []string{"d"}}, "b": {reaches: []string{"d"}}, "c": {reaches: []string{"d"}}, "d": {destructive: true}},
		},
		{
			"three levels, network", "a: b\n    @echo a\nb: && c\n    @echo b\nc: || d\n    @echo c\n[network]\nd:\n    @echo d\n",
			map[string]reach{"a": {network: true}, "b": {network: true}, "c": {network: true}, "d": {network: true}},
		},
		{
			"private target", "ship: _wipe\n    @echo ship\n[private]\n[confirm]\n_wipe:\n    @echo wipe\n",
			map[string]reach{"ship": {reaches: []string{"_wipe"}}},
		},
		{
			"diamond", "top: left right\n    @echo top\nleft: base\n    @echo left\nright: base\n    @echo right\n[confirm]\nbase:\n    @echo base\n",
			map[string]reach{"top": {reaches: []string{"base"}}, "left": {reaches: []string{"base"}}, "right": {reaches: []string{"base"}}, "base": {destructive: true}},
		},
		{
			"own confirm", clean,
			map[string]reach{"clean": {destructive: true}},
		},
		{
			"body text", "sweep:\n    rune clean\n    rm -rf dist\n" + clean,
			map[string]reach{"sweep": {}},
		},
		{
			"agent executor", "ask (agent):\n    Run clean, then fetch the data.\n" + clean,
			map[string]reach{"ask": {}},
		},
		{
			// A failure hook's own || hooks never run: post-mortems do not chain.
			"failure hook of a failure hook, confirm", "bad: || cleanup\n    @exit 1\ncleanup: || wipe\n    @echo cleanup\n[confirm]\nwipe:\n    @echo wipe\n",
			map[string]reach{"bad": {}, "cleanup": {reaches: []string{"wipe"}}},
		},
		{
			"failure hook of a failure hook, network", "bad: || cleanup\n    @exit 1\ncleanup: || probe\n    @echo cleanup\n[network]\nprobe:\n    @echo probe\n",
			map[string]reach{"bad": {}, "cleanup": {network: true}},
		},
		{
			// A failure hook still runs its dependencies and && hooks, and a
			// dependency's own || hooks fire as usual.
			"what a failure hook runs", "bad: || h\n    @exit 1\nh: d && p\n    @echo h\nd: || x\n    @echo d\n[network]\np:\n    @echo p\n[confirm]\nx:\n    @echo x\n",
			map[string]reach{"bad": {reaches: []string{"x"}, network: true}},
		},
		{
			// cleanup is queued first as top's failure hook, then reached as
			// helper's dependency, where its || hooks do fire.
			"failure hook also a dependency", "top: helper || cleanup\n    @exit 1\nhelper: cleanup\n    @echo helper\ncleanup: || wipe\n    @echo cleanup\n[confirm]\nwipe:\n    @echo wipe\n",
			map[string]reach{"top": {reaches: []string{"wipe"}}},
		},
		{
			// A run reaches wipe, through stage, before clean.
			"depth-first order", "ordered: stage clean\n    @echo ordered\nstage: wipe\n    @echo stage\n[confirm]\nwipe:\n    @echo wipe\n" + clean,
			map[string]reach{"ordered": {reaches: []string{"wipe", "clean"}}, "stage": {reaches: []string{"wipe"}}},
		},
		{
			// A task is named before what it runs, as the called task is.
			"confirm under confirm", "outer: reset\n    @echo outer\n[confirm]\nreset: wipe\n    @echo reset\n[confirm]\nwipe:\n    @echo wipe\n",
			map[string]reach{"outer": {reaches: []string{"reset", "wipe"}}, "reset": {destructive: true, reaches: []string{"wipe"}}},
		},
		{
			"edge kinds in order", "t: d && p || f\n    @echo t\n[confirm]\nd:\n    @echo d\n[confirm]\np:\n    @echo p\n[confirm]\nf:\n    @echo f\n",
			map[string]reach{"t": {reaches: []string{"d", "p", "f"}}},
		},
		{
			// cleanup is first reached as a's failure hook, then as b's
			// dependency, where its || hooks do fire.
			"failure hook first, dependency later", "top: a b\n    @echo top\na: || cleanup\n    @echo a\nb: cleanup\n    @echo b\ncleanup: || wipe\n    @echo cleanup\n[confirm]\nwipe:\n    @echo wipe\n",
			map[string]reach{"top": {reaches: []string{"wipe"}}, "a": {}, "b": {reaches: []string{"wipe"}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reachOf(adapterFor(t, c.src))
			for name, want := range c.want {
				if g, ok := got[name]; !ok || !g.equal(want) {
					t.Errorf("%s: %+v (listed %v), want %+v", name, g, ok, want)
				}
			}
		})
	}
}

// Spec 033 FR-006: a task unavailable on the host OS does not count, and
// neither does anything reached only through it.
func TestAdapterReachFollowsHostOS(t *testing.T) {
	src := "dispatch: wipe-win\n    @echo dispatch\n[windows, confirm(\"Wipe?\")]\nwipe-win:\n    @echo wipe\n" +
		"viawin: winonly\n    @echo viawin\n[windows]\nwinonly: clean\n    @echo winonly\n" +
		"both: winonly clean\n    @echo both\n[confirm]\nclean:\n    @echo clean\n"
	elsewhere := map[string][]string{"dispatch": nil, "viawin": nil, "both": {"clean"}}
	want := map[string]map[string][]string{
		"linux":   elsewhere,
		"darwin":  elsewhere,
		"windows": {"dispatch": {"wipe-win"}, "viawin": {"clean"}, "both": {"clean"}},
	}
	for goos, tasks := range want {
		a := adapterFor(t, src)
		a.goos = goos
		got := reachOf(a)
		for name, reaches := range tasks {
			if !slices.Equal(got[name].reaches, reaches) {
				t.Errorf("%s on %s: DestructiveReach = %q, want %q", name, goos, got[name].reaches, reaches)
			}
		}
	}
}

// Spec 034 FR-003 (quickstart N, Windows half): a dependency available only on
// Windows counts there, so the call is refused before anything runs.
func TestServerRefusesReachOnHostOS(t *testing.T) {
	a := adapterFor(t, "dispatch: wipe-win\n    @echo dispatch\n[windows, confirm(\"Wipe?\")]\nwipe-win:\n    @echo wipe\n")
	a.goos = "windows"
	cs := serverSession(t, mcpserver.New(a, mcpserver.Options{Version: "test", AllowDestructiveFlag: "--allow-destructive"}))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	want := `task "dispatch" can run destructive task "wipe-win"; to permit it, add wipe-win to --allow-destructive and restart the server`
	if !res.IsError || resultText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, resultText(res), want)
	}
}

// Spec 034 US3.1 (quickstart S): the endpoint an agent task opens refuses a
// call that can reach a destructive task, even when the engine running the
// agent task holds a grant for it, and nothing runs.
func TestAgentEndpointRefusesReach(t *testing.T) {
	dir := t.TempDir()
	marker := func(name string) string { return filepath.ToSlash(filepath.Join(dir, name+".done")) }
	src := "release: clean\n    @echo released > '" + marker("release") + "'\n" +
		"[confirm]\nclean:\n    @echo cleaned > '" + marker("clean") + "'\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	eng.grants = map[string]bool{"clean": true}
	cs := serverSession(t, mcpserver.New(eng.newAgentAdapter(), mcpserver.Options{Version: "test"}))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	want := `task "release" can run destructive task "clean"; it is not allowed in this session`
	if !res.IsError || resultText(res) != want {
		t.Errorf("isError=%v text=%q, want %q", res.IsError, resultText(res), want)
	}
	for _, name := range []string{"release", "clean"} {
		if _, err := os.Stat(marker(name)); err == nil {
			t.Errorf("%s ran although the call was refused", name)
		}
	}
}

// Spec 033 FR-011 (quickstart AB): the endpoint an agent task opens for its
// own agent lists the same hints.
func TestAgentEndpointHints(t *testing.T) {
	src := "release: clean\n    @echo release\n[confirm]\nclean:\n    @echo clean\n" +
		"mirror: fetch\n    @echo mirror\n[network]\nfetch:\n    @echo fetch\n" +
		"[readonly]\nstatus:\n    @echo status\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	cs := serverSession(t, mcpserver.New(eng.newAgentAdapter(), mcpserver.Options{Version: "test"}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.ToolAnnotations{}
	for _, tl := range res.Tools {
		tools[tl.Name] = tl.Annotations
	}
	if a := tools["release"]; a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Errorf("release: annotations %+v, want destructiveHint true", a)
	}
	if a := tools["mirror"]; a == nil || a.OpenWorldHint == nil || !*a.OpenWorldHint {
		t.Errorf("mirror: annotations %+v, want openWorldHint true", a)
	}
	if a := tools["status"]; a == nil || !a.ReadOnlyHint {
		t.Errorf("status: annotations %+v, want readOnlyHint true", a)
	}
}

// Spec 041 FR-005 (quickstart Q): an allowlist naming only a task restricted
// to another OS exposes nothing, and never adds that task.
func TestAllowOtherOSTaskStartsEmpty(t *testing.T) {
	a := adapterFor(t, "everywhere:\n    @echo e\n[windows]\nwin-only:\n    @echo w\n")
	a.goos = "linux"
	srv := mcpserver.New(a, mcpserver.Options{AllowList: []string{"win-only"}})
	res, err := serverSession(t, srv).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("tools = %v, want none", res.Tools)
	}
	if exposed, exposable := srv.Exposure(); exposed != 0 || exposable != 1 {
		t.Errorf("Exposure() = %d, %d, want 0, 1", exposed, exposable)
	}
}

// Spec 041 FR-005: the allowlist never makes a private task or the [context]
// hook a tool.
func TestAllowNeverWidens(t *testing.T) {
	a := adapterFor(t, "build:\n    @echo b\n\n_setup:\n    @echo s\n\n[context]\nhealth:\n    @echo ok\n")
	srv := mcpserver.New(a, mcpserver.Options{AllowList: []string{"_setup", "health"}})
	res, err := serverSession(t, srv).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("tools = %v, want none", res.Tools)
	}
}

// Spec 041 FR-017: the endpoint an agent task opens for its own agent is built
// without the serve filters, whatever the options of the run hold.
func TestAgentEndpointIgnoresFilters(t *testing.T) {
	src := "build:\n    @echo b\n\ntest:\n    @echo t\n"
	eng, _, _ := confirmEngine(t, src, nil, false, false)
	eng.opts.MCPAllow = []string{"build"}
	eng.opts.MCPDeny = []string{"test"}

	eng.opts.MCPAllowDestructive = []string{"build"}
	eng.opts.Version = "test"

	// The options executeAgent builds its endpoint from.
	opts := eng.agentMCPOptions()
	if want := (mcpserver.Options{Version: "test"}); !reflect.DeepEqual(opts, want) {
		t.Errorf("agentMCPOptions() = %+v, want %+v", opts, want)
	}
	srv := mcpserver.New(eng.newAgentAdapter(), opts)
	res, err := serverSession(t, srv).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	if want := []string{"build", "test"}; !slices.Equal(names, want) {
		t.Errorf("agent endpoint tools = %q, want %q", names, want)
	}
}
