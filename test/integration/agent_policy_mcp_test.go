package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 050, issue #48: [agent-allow] names the one value an agent run of a
// task may have for a parameter. These tests drive the built binary with an
// SDK client. The rows are those of
// specs/050-mcp-agent-value-policy/quickstart.md.

// agentPolicyRunefile is the quickstart scratch Runefile: every body writes a
// marker file, so "nothing ran" is checkable from the outside.
const agentPolicyRunefile = `# Deploy the service.
[agent-allow(env="staging")]
deploy env:enum("staging","prod"):
    @echo {{env}} > deploy.done

# Roll out to one region.
[agent-allow(env="staging", region="eu-west-1")]
rollout env:enum("staging","prod") region="eu-west-1":
    @echo {{env}}-{{quote(region)}} > rollout.done

# Deploy to production by default.
[agent-allow(env="staging")]
publish env:enum("staging","prod")="prod":
    @echo {{env}} > publish.done

# Run the tests.
test:
    @echo tested > test.done

# Preview on staging.
preview: test (deploy "staging")
    @echo previewed > preview.done

# Ship to production.
ship: test (deploy "prod")
    @echo shipped > ship.done

# Deploy where the caller says.
promote target: (deploy target)
    @echo promoted > promote.done

# Announce, then deploy production.
announce: && (deploy "prod")
    @echo announced > announce.done

# Try, deploying production on failure.
attempt: || (deploy "prod")
    @echo attempted > attempt.done

# Deploy by the state of a file.
nightly: test (deploy if path_exists("prod.flag") == "true" { "prod" } else { "staging" })
    @echo nightly > nightly.done

# Deploy to a setting that is missing.
broken: test (deploy env("RUNE_NO_SUCH_SETTING"))
    @echo broken > broken.done

# Release for real.
[confirm("Release for real?")]
[agent-allow(env="staging")]
release env:enum("staging","prod"):
    @echo {{env}} > release.done

# Tag a build.
[agent-allow(channel="beta")]
tag channel name="nightly":
    @echo {{quote(channel)}}-{{quote(name)}} > tag.done

# Deploy production from a body.
script:
    @echo scripted > script.done
    @"${RUNE_BIN:-rune}" deploy prod
`

const (
	deployRefused = `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"`
	// noAgentMark keeps a command-line run in these tests a person's run,
	// also when the suite itself was started by an agent through Rune.
	noAgentMark = "RUNE_AGENT_RUN="
)

// reachedRefused is the refusal of a call that reaches deploy with prod.
func reachedRefused(called string) string {
	return `task "` + called + `" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`
}

// policyCall calls a tool and returns the whole result.
func policyCall(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", tool, err)
	}
	return res
}

func policyText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// wantPolicyRefusal fails the test unless the call was refused with exactly
// want, carried no structured content, and left the marker files as they
// were.
func wantPolicyRefusal(t *testing.T, label, dir string, res *mcp.CallToolResult, want string, markers ...string) {
	t.Helper()
	if got := policyText(res); !res.IsError || got != want {
		t.Errorf("%s: isError=%v text=%q\nwant %q", label, res.IsError, got, want)
	}
	if res.StructuredContent != nil {
		t.Errorf("%s: a refusal carries structured content: %v", label, res.StructuredContent)
	}
	wantDone(t, label, dir, markers...)
}

// wantRan fails the test unless the call succeeded.
func wantRan(t *testing.T, label string, res *mcp.CallToolResult) {
	t.Helper()
	if res.IsError {
		t.Errorf("%s: isError with text %q", label, policyText(res))
	}
}

// Quickstart A, B, C, N, R (US1): the allowed value runs, any other is
// refused with the values and the policy, and nothing runs.
func TestAgentPolicyMCP_DirectCall(t *testing.T) {
	for _, transport := range bothTransports {
		dir := writeRunefile(t, agentPolicyRunefile)
		cs, _ := approvalSession(t, transport, dir, nil, nil, nil)

		wantPolicyRefusal(t, transport+" B", dir, policyCall(t, cs, "deploy", map[string]any{"env": "prod"}), deployRefused)
		wantPolicyRefusal(t, transport+" C", dir,
			policyCall(t, cs, "rollout", map[string]any{"env": "staging", "region": "us-east-1"}),
			`task "rollout" is not allowed for agents with region="us-east-1"; agent policy requires env="staging", region="eu-west-1"`)
		wantPolicyRefusal(t, transport+" both refused", dir,
			policyCall(t, cs, "rollout", map[string]any{"env": "prod", "region": "us-east-1"}),
			`task "rollout" is not allowed for agents with env="prod", region="us-east-1"; agent policy requires env="staging", region="eu-west-1"`)
		wantPolicyRefusal(t, transport+" R", dir,
			policyCall(t, cs, "tag", map[string]any{"channel": "stable"}),
			`task "tag" is not allowed for agents with channel="stable"; agent policy requires channel="beta"`)

		// N: a misspelled name is the unknown-argument error, not a run with
		// some other value.
		res := policyCall(t, cs, "deploy", map[string]any{"evn": "prod"})
		if got := policyText(res); !res.IsError || !strings.HasPrefix(got, `unknown argument "evn" for task "deploy"`) {
			t.Errorf("%s N: isError=%v text=%q", transport, res.IsError, got)
		}
		wantDone(t, transport+" N", dir)

		wantRan(t, transport+" A", policyCall(t, cs, "deploy", map[string]any{"env": "staging"}))
		wantRan(t, transport+" R", policyCall(t, cs, "tag", map[string]any{"channel": "beta", "name": "x"}))
		wantDone(t, transport, dir, "deploy.done", "tag.done")
		if got := readDone(t, dir, "deploy.done"); got != "staging" {
			t.Errorf("%s A: deploy.done = %q, want staging", transport, got)
		}
		if got := readDone(t, dir, "tag.done"); got != "beta-x" {
			t.Errorf("%s R: tag.done = %q, want beta-x", transport, got)
		}
	}
}

// A client that ignores the schema and sends something that is not the
// allowed string never makes the task run with another value (FR-030).
func TestAgentPolicyMCP_MalformedValues(t *testing.T) {
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	for _, value := range []any{7, true, nil, map[string]any{}, []any{"be", "ta"}, []any{"beta", "beta"}, ""} {
		res := policyCall(t, cs, "tag", map[string]any{"channel": value})
		if !res.IsError {
			t.Errorf("channel=%v ran: %q", value, policyText(res))
		}
		wantDone(t, "malformed", dir)
	}
	// A one-element array is that element: the task runs with beta or not at
	// all.
	res := policyCall(t, cs, "tag", map[string]any{"channel": []any{"beta"}})
	if !res.IsError {
		if got := readDone(t, dir, "tag.done"); got != "beta-nightly" {
			t.Errorf("tag.done = %q, want beta-nightly", got)
		}
	}
}

// SC-006: a refused value that is a secret is shown masked.
func TestAgentPolicyMCP_MasksSecret(t *testing.T) {
	const secret = "hunter2-policy-secret"
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, []string{"DEMO_API_TOKEN=" + secret}, nil)
	res := policyCall(t, cs, "tag", map[string]any{"channel": "pre " + secret})
	wantPolicyRefusal(t, "secret", dir, res, `task "tag" is not allowed for agents with channel="pre ***"; agent policy requires channel="beta"`)
	if strings.Contains(policyText(res), secret) {
		t.Errorf("the refusal holds the secret: %q", policyText(res))
	}
}

// Quickstart D, E, F (US2): an omitted argument is checked against the
// default, and the default is never replaced.
func TestAgentPolicyMCP_Defaults(t *testing.T) {
	for _, transport := range bothTransports {
		dir := writeRunefile(t, agentPolicyRunefile)
		cs, _ := approvalSession(t, transport, dir, nil, nil, nil)

		wantPolicyRefusal(t, transport+" E", dir, policyCall(t, cs, "publish", nil),
			`task "publish" is not allowed for agents with env="prod"; agent policy requires env="staging"`)

		res := policyCall(t, cs, "publish", map[string]any{"evn": "staging"})
		if got := policyText(res); !res.IsError || !strings.HasPrefix(got, `unknown argument "evn" for task "publish"`) {
			t.Errorf("%s misspelled: isError=%v text=%q", transport, res.IsError, got)
		}
		wantDone(t, transport+" misspelled", dir)

		wantRan(t, transport+" F", policyCall(t, cs, "publish", map[string]any{"env": "staging"}))
		wantRan(t, transport+" D", policyCall(t, cs, "rollout", map[string]any{"env": "staging"}))
		wantDone(t, transport, dir, "publish.done", "rollout.done")
		if got := readDone(t, dir, "publish.done"); got != "staging" {
			t.Errorf("%s F: publish.done = %q, want staging", transport, got)
		}
		if got := readDone(t, dir, "rollout.done"); got != "staging-eu-west-1" {
			t.Errorf("%s D: rollout.done = %q, want staging-eu-west-1", transport, got)
		}
	}
}

// Quickstart G to M (US3): what a call can reach is checked before anything
// runs, through dependencies, hooks and modules.
func TestAgentPolicyMCP_Reach(t *testing.T) {
	for _, transport := range bothTransports {
		dir := writeRunefile(t, agentPolicyRunefile)
		cs, _ := approvalSession(t, transport, dir, nil, nil, nil)

		wantPolicyRefusal(t, transport+" G", dir, policyCall(t, cs, "ship", nil), reachedRefused("ship"))
		wantPolicyRefusal(t, transport+" I", dir, policyCall(t, cs, "promote", map[string]any{"target": "prod"}), reachedRefused("promote"))
		wantPolicyRefusal(t, transport+" J", dir, policyCall(t, cs, "announce", nil), reachedRefused("announce"))
		wantPolicyRefusal(t, transport+" K", dir, policyCall(t, cs, "attempt", nil), reachedRefused("attempt"))

		wantRan(t, transport+" H", policyCall(t, cs, "preview", nil))
		wantDone(t, transport+" H", dir, "test.done", "deploy.done", "preview.done")
		wantRan(t, transport+" I", policyCall(t, cs, "promote", map[string]any{"target": "staging"}))
		wantDone(t, transport+" I", dir, "test.done", "deploy.done", "preview.done", "promote.done")

		// The module's task carries the policy: by its own tool, and when a
		// root task runs it.
		mod := writeRunefile(t, "mod infra \"infra.rune\"\n\n# Ships.\nshipit: (infra::deploy \"prod\")\n    @echo shipped > ship.done\n")
		module := "# Deploys.\n[agent-allow(env=\"staging\")]\ndeploy env:enum(\"staging\",\"prod\"):\n    @echo {{env}} > deploy.done\n"
		if err := os.WriteFile(filepath.Join(mod, "infra.rune"), []byte(module), 0o644); err != nil {
			t.Fatal(err)
		}
		ms, _ := approvalSession(t, transport, mod, nil, nil, nil)
		wantPolicyRefusal(t, transport+" module tool", mod, policyCall(t, ms, "infra__deploy", map[string]any{"env": "prod"}),
			`task "infra::deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"`)
		wantPolicyRefusal(t, transport+" module dependency", mod, policyCall(t, ms, "shipit", nil),
			`task "shipit" runs "infra::deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"`)
	}

	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	wantPolicyRefusal(t, "L", dir, policyCall(t, cs, "nightly", nil),
		`task "nightly" runs "deploy" with a value of env that is not known before the run; agent policy requires env="staging"`)
	wantPolicyRefusal(t, "M", dir, policyCall(t, cs, "broken", nil),
		`task "broken" was not run: the arguments "broken" passes to "deploy" could not be worked out before the run (environment variable not set: RUNE_NO_SUCH_SETTING), and an agent policy has to be checked on that path`)
}

// Quickstart W (FR-012a): a body that runs rune carries the agent run with
// it, and the same body run by a person does not.
func TestAgentPolicyMCP_BodyRunsRune(t *testing.T) {
	binary := "RUNE_BIN=" + runeBin
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, []string{binary}, nil)
	res := policyCall(t, cs, "script", nil)
	text := policyText(res)
	if !res.IsError || !strings.Contains(text, deployRefused) || !strings.HasSuffix(text, "[exit 1]") {
		t.Errorf("script: isError=%v text=%q, want the refusal and exit 1", res.IsError, text)
	}
	wantDone(t, "script through an agent", dir, "script.done")

	person := writeRunefile(t, agentPolicyRunefile)
	if r := run(t, person, []string{binary, noAgentMark}, "script"); r.code != 0 {
		t.Fatalf("rune script: exit %d, stderr %q", r.code, r.stderr)
	}
	wantDone(t, "script by a person", person, "script.done", "deploy.done")
	if got := readDone(t, person, "deploy.done"); got != "prod" {
		t.Errorf("deploy.done = %q, want prod", got)
	}
}

// Quickstart O, P, Q (US4): a grant and an approval answer whether a
// destructive task may run, not which values an agent may run it with.
func TestAgentPolicyMCP_ApprovalAndGrant(t *testing.T) {
	const releaseRefused = `task "release" is not allowed for agents with env="prod"; agent policy requires env="staging"`
	for _, transport := range bothTransports {
		granted := writeRunefile(t, agentPolicyRunefile)
		gs, _ := approvalSession(t, transport, granted, []string{"--allow-destructive", "release"}, nil, nil)
		wantPolicyRefusal(t, transport+" O", granted, policyCall(t, gs, "release", map[string]any{"env": "prod"}), releaseRefused)

		asked := writeRunefile(t, agentPolicyRunefile)
		who := &asker{answer: yes}
		as, _ := approvalSession(t, transport, asked, nil, nil, who.options())
		wantPolicyRefusal(t, transport+" P", asked, policyCall(t, as, "release", map[string]any{"env": "prod"}), releaseRefused)
		if shown := who.shown(); len(shown) != 0 {
			t.Errorf("%s P: the person was asked about a call the policy refuses: %q", transport, shown)
		}

		wantRan(t, transport+" Q", policyCall(t, as, "release", map[string]any{"env": "staging"}))
		if shown := who.shown(); len(shown) != 1 || !strings.Contains(shown[0], `- env: "staging"`) {
			t.Errorf("%s Q: requests = %q, want one for staging", transport, shown)
		}
		wantDone(t, transport+" Q", asked, "release.done")
		if got := readDone(t, asked, "release.done"); got != "staging" {
			t.Errorf("%s Q: release.done = %q, want staging", transport, got)
		}
	}

	// A client that cannot be asked keeps the destructive refusal, at its
	// place before the arguments are read (FR-022).
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	res := policyCall(t, cs, "release", map[string]any{"env": "prod"})
	want := `task "release" is destructive; to permit it, add release to --allow-destructive and restart the server` + cannotAskSuffix("release")
	if got := policyText(res); !res.IsError || got != want {
		t.Errorf("no elicitation: isError=%v text=%q\nwant %q", res.IsError, got, want)
	}
	wantDone(t, "no elicitation", dir)

	allowed := writeRunefile(t, agentPolicyRunefile)
	ls, _ := approvalSession(t, "stdio", allowed, []string{"--allow", "deploy"}, nil, nil)
	wantPolicyRefusal(t, "--allow deploy", allowed, policyCall(t, ls, "deploy", map[string]any{"env": "prod"}), deployRefused)
}

// FR-015: the attribute exposes nothing, and a hidden restricted task is
// still checked when an exposed task would run it.
func TestAgentPolicyMCP_ExposesNothing(t *testing.T) {
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, []string{"--deny", "deploy"}, nil, nil)
	if _, listed := policyTools(t, cs)["deploy"]; listed {
		t.Error("deploy is a tool although --deny names it")
	}
	wantPolicyRefusal(t, "ship with deploy hidden", dir, policyCall(t, cs, "ship", nil), reachedRefused("ship"))
}

// Quickstart T (US7): a person's run is not limited.
func TestAgentPolicy_CommandLineUnchanged(t *testing.T) {
	env := []string{noAgentMark}
	cases := []struct {
		args    []string
		markers []string
		file    string
	}{
		{[]string{"deploy", "prod"}, []string{"deploy.done"}, "deploy.done"},
		{[]string{"ship"}, []string{"test.done", "deploy.done", "ship.done"}, "deploy.done"},
		{[]string{"publish"}, []string{"publish.done"}, "publish.done"},
		{[]string{"--yes", "release", "prod"}, []string{"release.done"}, "release.done"},
	}
	for _, c := range cases {
		label := "rune " + strings.Join(c.args, " ")
		dir := writeRunefile(t, agentPolicyRunefile)
		if r := run(t, dir, env, c.args...); r.code != 0 {
			t.Errorf("%s: exit %d, stderr %q", label, r.code, r.stderr)
			continue
		}
		wantDone(t, label, dir, c.markers...)
		if got := readDone(t, dir, c.file); got != "prod" {
			t.Errorf("%s: %s = %q, want prod", label, c.file, got)
		}
	}
	dir := writeRunefile(t, agentPolicyRunefile)
	if r := run(t, dir, env, "--list"); r.code != 0 || !strings.Contains(r.stdout, "deploy") {
		t.Errorf("rune --list: exit %d, stdout %q", r.code, r.stdout)
	}
}

// Quickstart V (US5): a policy Rune cannot apply stops everything, with its
// code.
func TestAgentPolicy_InvalidDeclaration(t *testing.T) {
	src := "# Deploy the service.\n[agent-allow(evn=\"staging\")]\ndeploy env:enum(\"staging\",\"prod\"):\n    @echo {{env}} > deploy.done\n"
	want := `error[RUNE2019]: agent-allow names unknown parameter "evn" of task "deploy" (did you mean "env"?)`
	dir := writeRunefile(t, src)

	r := run(t, dir, nil, "analyze", "--color=never")
	out := r.stdout + r.stderr
	if r.code != 3 || strings.Count(out, want) != 1 {
		t.Errorf("rune analyze: exit %d, output %q, want exit 3 and %q once", r.code, out, want)
	}
	if r := run(t, dir, []string{noAgentMark}, "deploy", "staging"); r.code != 3 {
		t.Errorf("rune deploy staging: exit %d, want 3", r.code)
	}
	if r := runWithStdin(t, dir, "", "mcp"); r.code != 3 {
		t.Errorf("rune mcp: exit %d, stderr %q, want 3", r.code, r.stderr)
	}
	wantDone(t, "invalid declaration", dir)
}

// policyTool is what tools/list says about one tool.
type policyTool struct {
	description string
	schema      map[string]any
	json        string
}

func policyTools(t *testing.T, cs *mcp.ClientSession) map[string]policyTool {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	tools := map[string]policyTool{}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			InputSchema map[string]any `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = policyTool{description: tool.Description, schema: decoded.InputSchema, json: string(raw)}
	}
	return tools
}

func (p policyTool) property(name string) any {
	properties, _ := p.schema["properties"].(map[string]any)
	return properties[name]
}

func (p policyTool) required() []string {
	var names []string
	list, _ := p.schema["required"].([]any)
	for _, name := range list {
		names = append(names, name.(string))
	}
	return names
}

// Quickstart S and X (US6): the tool names the allowed value, and a tool
// with no policy is what it was.
func TestAgentPolicyMCP_ToolsList(t *testing.T) {
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	tools := policyTools(t, cs)

	oneValue := func(value string) map[string]any {
		return map[string]any{"type": "string", "enum": []any{value}}
	}
	deploy := tools["deploy"]
	if got := deploy.property("env"); !reflect.DeepEqual(got, oneValue("staging")) {
		t.Errorf("deploy env = %v, want the one value staging", got)
	}
	if want := "Deploy the service.\n\nAgent policy: agents may run this task only with env=\"staging\"."; deploy.description != want {
		t.Errorf("deploy description = %q, want %q", deploy.description, want)
	}
	if got := tools["tag"].property("channel"); !reflect.DeepEqual(got, oneValue("beta")) {
		t.Errorf("tag channel = %v, want the one value beta", got)
	}
	if want := "Roll out to one region.\n\nAgent policy: agents may run this task only with env=\"staging\" and region=\"eu-west-1\"."; tools["rollout"].description != want {
		t.Errorf("rollout description = %q, want %q", tools["rollout"].description, want)
	}

	// X: a literal default the policy refuses has to be sent, so the schema
	// says so. A default that is the allowed value stays optional.
	if got := tools["publish"].required(); !slices.Equal(got, []string{"env"}) {
		t.Errorf("publish required = %v, want env", got)
	}
	if got := tools["rollout"].required(); !slices.Equal(got, []string{"env"}) {
		t.Errorf("rollout required = %v, want env only", got)
	}

	// The same task on a Runefile with no policy at all is the same tool.
	var plain []string
	for line := range strings.SplitSeq(agentPolicyRunefile, "\n") {
		if !strings.HasPrefix(line, "[agent-allow(") {
			plain = append(plain, line)
		}
	}
	other := writeRunefile(t, strings.Join(plain, "\n"))
	os2, _ := approvalSession(t, "stdio", other, nil, nil, nil)
	without := policyTools(t, os2)
	for _, name := range []string{"test", "preview", "ship"} {
		if tools[name].json != without[name].json {
			t.Errorf("%s changed with a policy elsewhere in the Runefile:\n%s\n%s", name, tools[name].json, without[name].json)
		}
	}
	if got := without["deploy"].property("env"); !reflect.DeepEqual(got, map[string]any{"type": "string", "enum": []any{"staging", "prod"}}) {
		t.Errorf("deploy env with no policy = %v, want both values", got)
	}
}

// FR-031: after a reload the tool and the check follow the new Runefile.
func TestAgentPolicyMCP_Reload(t *testing.T) {
	dir := writeRunefile(t, agentPolicyRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	wantPolicyRefusal(t, "before the reload", dir, policyCall(t, cs, "deploy", map[string]any{"env": "prod"}), deployRefused)

	relaxed := strings.Replace(agentPolicyRunefile, "[agent-allow(env=\"staging\")]\ndeploy ", "deploy ", 1)
	if relaxed == agentPolicyRunefile {
		t.Fatal("the policy on deploy was not removed")
	}
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(relaxed), 0o644); err != nil {
		t.Fatal(err)
	}
	both := map[string]any{"type": "string", "enum": []any{"staging", "prod"}}
	for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(50 * time.Millisecond) {
		if reflect.DeepEqual(policyTools(t, cs)["deploy"].property("env"), both) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deploy still lists %v after the Runefile changed", policyTools(t, cs)["deploy"].property("env"))
		}
	}
	wantRan(t, "after the reload", policyCall(t, cs, "deploy", map[string]any{"env": "prod"}))
	if got := readDone(t, dir, "deploy.done"); got != "prod" {
		t.Errorf("deploy.done = %q, want prod", got)
	}
}

// The agent program of an agent task inherits the mark when the task runs in
// an agent run, like any body, and has none when a person runs the task
// (research R15). The stub prints the variable in place of an agent's answer.
func TestAgentPolicy_AgentTaskInheritsMark(t *testing.T) {
	src := `set agent_cmd := ["sh", "-c", "printf 'mark=%s' \"$RUNE_AGENT_RUN\"", "rune-agent-stub"]

# Ask the agent.
ask (agent):
    Say hello.
`
	dir := writeRunefile(t, src)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	res := policyCall(t, cs, "ask", nil)
	if text := policyText(res); res.IsError || !strings.HasPrefix(text, "mark=1\n") {
		t.Errorf("ask through an agent: isError=%v text=%q, want the mark", res.IsError, text)
	}

	r := run(t, dir, []string{noAgentMark}, "ask")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "mark=" {
		t.Errorf("rune ask by a person: exit %d, stdout %q, want no mark", r.code, r.stdout)
	}
}
