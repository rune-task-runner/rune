package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 052: what the rune engine adds to the audit record of a call: the
// arguments it bound, why it refused, the plan, and the id its tasks see.

const auditSrc = `build target="debug" *files:
    @echo "{{target}}" > build.done

deploy env:enum("staging","prod") token="":
    @echo deployed > deploy.done

[agent-allow(env="staging")]
ship env:
    @echo shipped > ship.done

broken target=missing_var:
    @echo never > broken.done

show-id:
    @echo "$RUNE_INVOCATION_ID" > id.txt
`

// auditedServer serves a through a server that records into a log in a
// temporary directory, with no git.
func auditedServer(t *testing.T, a *mcpAdapter, opts mcpserver.Options, client *mcp.ClientOptions) (*mcp.ClientSession, string, *bytes.Buffer) {
	t.Helper()
	a.gitState = func(context.Context, string) (string, string, *bool) { return gitStateNone, "", nil }
	aud, stderr := testAuditor(t)
	opts.Audit = aud
	return clientSession(t, mcpserver.New(a, opts), client), aud.log.Dir(), stderr
}

// lastCall returns the newest call in the log.
func lastCall(t *testing.T, dir string) audit.Action {
	t.Helper()
	var calls []audit.Action
	for _, a := range auditActions(t, dir) {
		if a.Type == audit.TypeCall {
			calls = append(calls, a)
		}
	}
	if len(calls) == 0 {
		t.Fatal("no call in the log")
	}
	return calls[len(calls)-1]
}

func callWith(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAdapterReportsEffectiveArguments(t *testing.T) {
	a := adapterFor(t, auditSrc)
	cs, dir, _ := auditedServer(t, a, mcpserver.Options{}, nil)

	callWith(t, cs, "build", nil)
	c := lastCall(t, dir)
	if c.Arguments == nil || c.Arguments.Supplied != nil || string(c.Arguments.Effective) != `{"target":"debug","files":[]}` {
		t.Errorf("defaults: %+v", c.Arguments)
	}
	if c.Outcome != "completed" || c.ExitCode == nil || *c.ExitCode != 0 {
		t.Errorf("outcome %q exit %v", c.Outcome, c.ExitCode)
	}

	callWith(t, cs, "build", map[string]any{"target": "release", "files": []any{"a b", "c"}})
	c = lastCall(t, dir)
	if string(c.Arguments.Effective) != `{"target":"release","files":["a b","c"]}` {
		t.Errorf("a variadic keeps its elements apart: %s", c.Arguments.Effective)
	}
	if string(c.Arguments.Supplied) != `{"files":["a b","c"],"target":"release"}` {
		t.Errorf("supplied %s", c.Arguments.Supplied)
	}
}

func TestAdapterMasksEffectiveArguments(t *testing.T) {
	a := adapterFor(t, auditSrc)
	a.baseEnv = []string{"API_TOKEN=s3cr3t-fixture-value"}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)
	cs, dir, _ := auditedServer(t, a, mcpserver.Options{}, nil)
	callWith(t, cs, "build", map[string]any{"target": "s3cr3t-fixture-value"})
	callWith(t, cs, "deploy", map[string]any{"env": "staging", "token": "anything"})
	data, err := os.ReadFile(dayFileOf(dir, lastCall(t, dir).Start))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cr3t-fixture-value") || strings.Contains(string(data), "anything") {
		t.Errorf("a secret or a secret-named value is in the log: %s", data)
	}
	if got := string(lastCall(t, dir).Arguments.Effective); got != `{"env":"staging","token":"***"}` {
		t.Errorf("effective %s", got)
	}
}

// A refusal quotes the value it refused. In the record the value of a
// secret-named parameter is hidden there too (FR-019), and the agent's own
// result keeps the text it always had (FR-038).
func TestAdapterHidesSecretNamedValueInErrorText(t *testing.T) {
	src := "login auth:enum(\"oidc\",\"basic\"):\n    @echo in > login.done\n\n" +
		"[agent-allow(passwd=\"none\")]\nreset passwd:\n    @echo reset > reset.done\n"
	tests := []struct {
		name, tool string
		args       map[string]any
	}{
		{"a constraint", "login", map[string]any{"auth": "hunter2-fixture"}},
		{"an agent policy", "reset", map[string]any{"passwd": "hunter2-fixture"}},
		{"a value that is quoted with escapes", "login", map[string]any{"auth": "hunter2\t\"fixture"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := adapterFor(t, src)
			cs, dir, _ := auditedServer(t, a, mcpserver.Options{}, nil)
			res := callWith(t, cs, tt.tool, tt.args)
			if !res.IsError || !strings.Contains(resultText(res), "hunter2") {
				t.Fatalf("the result does not quote the value: %s", resultText(res))
			}
			c := lastCall(t, dir)
			if c.Error == nil || c.Error.Message == "" {
				t.Fatalf("no error text: %+v", c)
			}
			data, err := os.ReadFile(dayFileOf(dir, c.Start))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "fixture") {
				t.Errorf("the value of a secret-named parameter is in the log: %s", data)
			}
		})
	}
}

func TestAdapterMarksKinds(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		outcome string
		kind    string
		marker  string
	}{
		{"a missing required argument", "deploy", nil, "invalid-arguments", "validation", "deploy.done"},
		{"an agent policy", "ship", map[string]any{"env": "prod"}, "refused", "policy", "ship.done"},
		{"a default that does not evaluate", "broken", nil, "invalid-arguments", "validation", "broken.done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := adapterFor(t, auditSrc)
			cs, dir, _ := auditedServer(t, a, mcpserver.Options{}, nil)
			res := callWith(t, cs, tt.tool, tt.args)
			if !res.IsError {
				t.Fatalf("the call was not refused: %s", resultText(res))
			}
			c := lastCall(t, dir)
			if c.Outcome != tt.outcome || c.Error == nil || c.Error.Kind != tt.kind || c.Error.Message == "" {
				t.Errorf("outcome %q error %+v", c.Outcome, c.Error)
			}
			if c.ExitCode != nil {
				t.Errorf("exit code %d for a call that ran nothing", *c.ExitCode)
			}
			if _, err := os.Stat(filepath.Join(a.workDir, tt.marker)); err == nil {
				t.Error("the task ran")
			}
		})
	}
}

func TestAdapterMarksUnavailableTask(t *testing.T) {
	a := adapterFor(t, "[windows]\nwin:\n    @echo w\n")
	a.goos = "linux"
	// The server never registers such a task, so the engine is called as an
	// embedder would call it.
	if _, err := a.Call(context.Background(), "win", nil); err == nil {
		t.Fatal("a task of another host ran")
	}
}

func TestAdapterMarksPlanRefusal(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	a.ident = a.planIdent
	opts := serveMCPOptions("test", "rune", nil, nil, nil, a.limit(), "", false)
	approve := &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	cs, dir, _ := auditedServer(t, a, opts, approve)
	res := callWith(t, cs, "apply", map[string]any{"env": "prod"})
	if !res.IsError {
		t.Fatal("an apply with no plan ran")
	}
	c := lastCall(t, dir)
	if c.Outcome != "refused" || c.Error == nil || c.Error.Kind != "policy" || c.ExitCode != nil || c.Plan != nil {
		t.Errorf("apply with no stored plan: %+v", c)
	}
}

func TestInvocationIDInTaskEnvironment(t *testing.T) {
	a := adapterFor(t, auditSrc)
	a.workDir = a.root
	cs, dir, _ := auditedServer(t, a, mcpserver.Options{}, nil)
	callWith(t, cs, "show-id", nil)
	got, err := os.ReadFile(filepath.Join(a.workDir, "id.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if id := strings.TrimSpace(string(got)); id != lastCall(t, dir).ID || len(id) != 16 {
		t.Errorf("the task saw %q, the record has %q", id, lastCall(t, dir).ID)
	}

	// A server that keeps no record gives no id.
	t.Setenv(invocationVar, "")
	plain := adapterFor(t, auditSrc)
	plain.workDir = plain.root
	callWith(t, serverSession(t, mcpserver.New(plain, mcpserver.Options{})), "show-id", nil)
	got, err = os.ReadFile(filepath.Join(plain.workDir, "id.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "" {
		t.Errorf("an unrecorded call saw the id %q", got)
	}
}

func TestAdapterAuditIdentity(t *testing.T) {
	a := adapterFor(t, auditSrc)
	a.ident = planIdentity{runefile: "/repo/Runefile", def: "aa", config: "bb"}
	dirty := true
	var asked string
	a.gitState = func(_ context.Context, dir string) (string, string, *bool) {
		asked = dir
		return gitStateCommit, "c0ffee", &dirty
	}
	got := a.AuditIdentity(context.Background())
	want := mcpserver.AuditIdentity{Runefile: "/repo/Runefile", DefinitionDigest: "aa", ConfigDigest: "bb", GitState: "commit", GitCommit: "c0ffee", GitDirty: &dirty}
	if got != want {
		t.Errorf("identity %+v", got)
	}
	if asked != a.root {
		t.Errorf("git was asked about %q, want the Runefile's directory", asked)
	}

	// With no hook the real git answers: a temporary directory is no
	// repository, or git is missing.
	a.gitState = nil
	if state := a.AuditIdentity(context.Background()).GitState; state != gitStateNone && state != gitStateUnavailable {
		t.Errorf("the real git said %q about a temporary directory", state)
	}
}

func TestAdapterRedact(t *testing.T) {
	a := adapterFor(t, auditSrc)
	if got := a.Redact("plain"); got != "plain" {
		t.Errorf("no mask set: %q", got)
	}
	a.baseEnv = []string{"API_TOKEN=s3cr3t-fixture-value"}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)
	if got := a.Redact("x s3cr3t-fixture-value y"); got != "x *** y" {
		t.Errorf("got %q", got)
	}
}

// The identity of a load is computed for every Runefile, with plans or
// without.
func TestLoadComputesIdentity(t *testing.T) {
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte("build:\n    @echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _, err := loadMCPAdapter(Options{Stderr: &bytes.Buffer{}}, runefile)
	if err != nil {
		t.Fatal(err)
	}
	if a.ident.runefile != runefilePath(a.file, a.root) || len(a.ident.def) != 64 || len(a.ident.config) != 64 {
		t.Errorf("identity %+v", a.ident)
	}
	if a.planIdent != (planIdentity{}) {
		t.Errorf("a Runefile with no plan has a plan identity: %+v", a.planIdent)
	}
}

// A plan call and an apply call link the plan to their records, and every
// journal line they write carries the call's id.
func TestPlanEventsCarryInvocation(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	a.ident = a.planIdent
	opts := serveMCPOptions("test", "rune", nil, nil, nil, a.limit(), "", false)
	approve := &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	cs, dir, _ := auditedServer(t, a, opts, approve)

	if res := callWith(t, cs, "plan", map[string]any{"env": "prod"}); res.IsError {
		t.Fatalf("plan: %s", resultText(res))
	}
	planCall := lastCall(t, dir)
	rec := planRecords(t, a)[0]
	want := audit.Plan{Role: "stored", ID: rec.ID, Digest: "sha256:" + rec.Artifact.SHA256}
	if planCall.Plan == nil || *planCall.Plan != want {
		t.Errorf("plan of the plan call: %+v, want %+v", planCall.Plan, want)
	}

	if res := callWith(t, cs, "apply", map[string]any{"env": "prod"}); res.IsError {
		t.Fatalf("apply: %s", resultText(res))
	}
	applyCall := lastCall(t, dir)
	want.Role = "applied"
	if applyCall.Plan == nil || *applyCall.Plan != want {
		t.Errorf("plan of the apply call: %+v, want %+v", applyCall.Plan, want)
	}
	if applyCall.Approval == nil || applyCall.Approval.Decision != "approved" || !applyCall.Approval.Asked {
		t.Errorf("approval of the apply call: %+v", applyCall.Approval)
	}
	if applyCall.Sandbox == nil || applyCall.Sandbox.Enforced {
		t.Errorf("sandbox: %+v", applyCall.Sandbox)
	}

	events := planJournal(t, a)
	if len(events) < 5 {
		t.Fatalf("journal: %v", eventNames(events))
	}
	for _, ev := range events {
		wantID := applyCall.ID
		if ev.Event == "plan-created" {
			wantID = planCall.ID
		}
		if ev.Invocation != wantID {
			t.Errorf("%s carries invocation %q, want %q", ev.Event, ev.Invocation, wantID)
		}
	}
}

// On the command line there is no call, and a journal line has no id.
func TestPlanEventsWithoutInvocation(t *testing.T) {
	a, _ := planAdapter(t, planSrc)
	if _, err := a.Call(context.Background(), "plan", prod); err != nil {
		t.Fatal(err)
	}
	for _, ev := range planJournal(t, a) {
		if ev.Invocation != "" {
			t.Errorf("%s carries invocation %q", ev.Event, ev.Invocation)
		}
	}
	data, err := os.ReadFile(filepath.Join(planDir(a), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "invocation") {
		t.Errorf("an empty invocation was written: %s", data)
	}
}
