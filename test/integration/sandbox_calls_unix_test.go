//go:build unix

package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What a call did before spec 053 it still does when its bodies are
// confined: Rune's own bookkeeping, the refusals, the masking, the limits.

const ownWorkRunefile = planApplyRunefile + `
# Generate from the input.
[cache(inputs = ["input.txt"], outputs = ["out.txt"])]
gen:
    @cat input.txt > out.txt; echo generated >> gen.done

# Take a while.
slow:
    @./probe sleep 1
`

// Quickstart U: the task cache, capturing a plan and applying it, the
// reload and the audit record are Rune's own work. They happen in the
// server, so they keep working for a confined call.
func TestSandboxKeepsRunesOwnWork(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, ownWorkRunefile)
	f.write(t, "state.serial", "1\n")
	f.write(t, "input.txt", "input\n")
	cs, stderr, _ := f.server(t, (&asker{answer: yes}).options())

	first := callTask(t, cs, "gen", nil)
	second := callTask(t, cs, "gen", nil)
	if first.exit != 0 || second.exit != 0 || !strings.Contains(second.stderr, "cached: gen") {
		t.Errorf("the cache: first %+v, second %+v", first, second)
	}
	if got := readDone(t, f.dir, "gen.done"); got != "generated" {
		t.Errorf("the cached task ran %q", got)
	}

	if res := callTask(t, cs, "plan", prodArgs); res.isError || res.exit != 0 {
		t.Fatalf("plan: %+v", res)
	}
	if hasMarker(f.dir, "tfplan") {
		t.Error("the plan file was left in the workspace")
	}
	if plans := storedPlans(t, f.dir); len(plans) != 1 {
		t.Fatalf("%d stored plans, want 1", len(plans))
	}
	if res := callTask(t, cs, "apply", prodArgs); res.isError || res.exit != 0 {
		t.Fatalf("apply: %+v", res)
	}
	applied, err := os.ReadFile(filepath.Join(f.dir, "applied.txt"))
	if err != nil || strings.TrimSpace(string(applied)) != "plan env=prod serial=1" {
		t.Errorf("the applied plan: %q, %v", applied, err)
	}

	// A person saves the Runefile while a confined call runs. The server
	// reloads, and the new task is confined like the others.
	done := make(chan taskResult, 1)
	go func() { done <- callTask(t, cs, "slow", nil) }()
	f.write(t, "Runefile", ownWorkRunefile+"\n# Added while the server ran.\nlate:\n"+attempt("echo x >> Runefile"))
	waitForText(t, stderr, "reloaded Runefile: 1 tool added", 1)
	if res := <-done; res.exit != 0 {
		t.Errorf("the call that ran during the reload: %+v", res)
	}
	if res := callTask(t, cs, "late", nil); res.stdout != "denied\n" {
		t.Errorf("the task of the reloaded Runefile: %+v", res)
	}

	calls := logCalls(t, f.dir, f.logEnv())
	if len(calls) != 6 {
		t.Fatalf("%d calls in the log, want 6", len(calls))
	}
	for _, call := range calls {
		if call.Outcome != "completed" || call.End == nil || call.Sandbox == nil || !call.Sandbox.Enforced {
			t.Errorf("call %s: outcome %q, sandbox %+v", call.Task, call.Outcome, call.Sandbox)
		}
	}
}

const semanticsRunefile = `# Print a secret.
leak:
    @echo token=$API_TOKEN

# Print a lot.
noisy:
    @i=0; while [ $i -lt 400 ]; do echo 0123456789abcdef; i=$((i+1)); done

# Fail and be diagnosed.
check: || diagnose
    @echo checking
    @exit 3

diagnose:
    @echo SUGGESTION: fix the fixtures

# Remove everything.
[confirm("Remove the build output?")]
clean:
    @: > clean.ran

# Deploy to staging only.
[agent-allow(env="staging")]
deploy env:
    @: > deploy.ran

# Succeed.
fine:
    @echo fine
`

// Quickstart V: a confined call returns what the same call returns on a
// server started unsandboxed, apart from the hint line of a failed body.
func TestSandboxKeepsCallSemantics(t *testing.T) {
	requireSandbox(t)
	const secret = "s3cr3t-sandbox-fixture"
	start := func(flags ...string) (*sandboxFixture, *mcp.ClientSession) {
		f := sandboxProject(t, semanticsRunefile)
		f.env = append(f.env, "API_TOKEN="+secret)
		cs, _, _ := f.server(t, nil, flags...)
		return f, cs
	}
	_, confined := start("--output-limit", "1KiB")
	_, plain := start("--output-limit", "1KiB", "--sandbox", "off")

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"leak", nil},
		{"noisy", nil},
		{"check", nil},
		{"clean", nil},
		{"deploy", map[string]any{"env": "prod"}},
		{"fine", map[string]any{"sandbox": "off"}},
		{"fine", nil},
	}
	for _, c := range calls {
		got, want := callTask(t, confined, c.tool, c.args), callTask(t, plain, c.tool, c.args)
		// The hint line is the one thing a confined failure adds.
		gotText := got.text
		if at := strings.Index(gotText, "note: task "); at >= 0 {
			end := strings.Index(gotText[at:], "\n")
			gotText = gotText[:at] + gotText[at+end+1:]
		}
		if gotText != want.text || got.isError != want.isError || got.exit != want.exit || got.hasExit != want.hasExit {
			t.Errorf("%s %v:\nconfined:    %q (error %v, exit %d)\nunsandboxed: %q (error %v, exit %d)", c.tool, c.args, got.text, got.isError, got.exit, want.text, want.isError, want.exit)
		}
	}

	if res := callTask(t, confined, "leak", nil); strings.Contains(res.text, secret) || !strings.Contains(res.text, "token=") {
		t.Errorf("the secret is in a confined result: %q", res.text)
	}
	if res := callTask(t, confined, "noisy", nil); !strings.Contains(res.text, "[truncated ") {
		t.Errorf("the output limit did not cut a confined result: %d bytes", len(res.text))
	}
	check := callTask(t, confined, "check", nil)
	if check.exit != 3 || !strings.Contains(check.text, "[exit 3]") || !strings.Contains(check.text, "SUGGESTION: fix the fixtures") || !strings.Contains(check.stderr, `note: task "check" ran in the sandbox`) {
		t.Errorf("a failing task with a failure hook: %+v", check)
	}
	if res := callTask(t, confined, "clean", nil); !res.isError || !strings.Contains(res.text, "destructive") {
		t.Errorf("a [confirm] task without a grant: %+v", res)
	}
	if res := callTask(t, confined, "fine", map[string]any{"sandbox": "off"}); !res.isError || !strings.Contains(res.text, `unknown argument "sandbox"`) {
		t.Errorf("a tool argument named sandbox: %+v", res)
	}

	granted, cs := start("--allow-destructive", "clean")
	if res := callTask(t, cs, "clean", nil); res.isError || !hasMarker(granted.dir, "clean.ran") {
		t.Errorf("a granted [confirm] task: %+v", res)
	}
}

// runExact runs the rune binary in dir with exactly env.
func runExact(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(runeBin, args...)
	cmd.Dir, cmd.Env = dir, env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !asExit(err, &exit) {
			t.Fatalf("run %v failed to start: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

// agentEndpointProject is a project whose (agent) task a person runs: its
// agent is this test binary, which calls the tool "build" through the
// endpoint, and both the [context] task and that tool try to write outside
// the project.
func agentEndpointProject(t *testing.T) *sandboxFixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := sandboxProject(t, "# Build.\nbuild:\n    @echo built\n")
	f.write(t, "Runefile", "set agent_cmd := [\""+filepath.ToSlash(self)+"\"]\n\n"+
		"# Report the project state.\n[context]\nhealth:\n"+attempt("echo x > "+f.outside+"/from-context")+"    @echo healthy\n\n"+
		"# Build.\nbuild:\n"+attempt("echo x > "+f.outside+"/from-build")+
		"\n# Review with the agent.\nreview (agent):\n    Review the project.\n")
	f.env = append(f.env, helperAgentVar+"=1")
	return f
}

// Quickstart S: the [context] task and the calls of an (agent) task a
// person runs are confined, under the mode of RUNE_SANDBOX.
func TestSandboxAgentEndpoint(t *testing.T) {
	requireSandbox(t)
	outsideFiles := func(f *sandboxFixture) []string {
		entries, err := os.ReadDir(f.outside)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}

	t.Run("confined", func(t *testing.T) {
		f := agentEndpointProject(t)
		res := runExact(t, f.dir, f.env, "review")
		if res.code != 0 || !strings.Contains(res.stdout, "agent done") {
			t.Fatalf("rune review: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
		}
		if strings.Contains(res.stderr, "warning: sandbox:") {
			t.Errorf("a confining endpoint warned: %q", res.stderr)
		}
		if got := outsideFiles(f); len(got) != 1 {
			t.Errorf("the directory outside the project holds %v", got)
		}
		call := lastLogCall(t, f.dir, f.logEnv())
		if call.Task != "build" || call.Transport != "agent-endpoint" || call.Sandbox == nil || !call.Sandbox.Enforced {
			t.Errorf("the agent's call: %+v, sandbox %+v", call, call.Sandbox)
		}
	})

	t.Run("off", func(t *testing.T) {
		f := agentEndpointProject(t)
		res := runExact(t, f.dir, append(f.env, "RUNE_SANDBOX=off"), "review")
		if res.code != 0 || !strings.HasPrefix(res.stderr, sandboxOffWarning+"\n") {
			t.Fatalf("rune review: exit %d, stderr %q", res.code, res.stderr)
		}
		if got := outsideFiles(f); len(got) != 3 {
			t.Errorf("the directory outside the project holds %v, want the marker and both written files", got)
		}
	})

	t.Run("no backend", func(t *testing.T) {
		f := agentEndpointProject(t)
		bin := filepath.Join(f.root, "bin")
		if err := os.Mkdir(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		res := runExact(t, f.dir, append(f.env, "PATH="+bin), "review")
		want := "warning: sandbox: bubblewrap (bwrap) was not found on the PATH. Every task call will be refused. Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.\n"
		if !strings.HasPrefix(res.stderr, want) {
			t.Errorf("stderr = %q, want it to start with %q", res.stderr, want)
		}
		// The agent's call is refused, so the stand-in agent fails.
		if res.code == 0 || strings.Contains(res.stdout, "agent done") {
			t.Errorf("exit %d, stdout %q", res.code, res.stdout)
		}
		if got := outsideFiles(f); len(got) != 1 {
			t.Errorf("the directory outside the project holds %v", got)
		}
	})

	t.Run("a mode that cannot be read", func(t *testing.T) {
		f := agentEndpointProject(t)
		res := runExact(t, f.dir, append(f.env, "RUNE_SANDBOX=maybe"), "review")
		if res.code != 2 || !strings.Contains(res.stderr, `RUNE_SANDBOX: "maybe" is not a mode; use enforce or off`) {
			t.Errorf("exit %d, stderr %q", res.code, res.stderr)
		}
		if strings.Contains(res.stdout, "agent done") || len(outsideFiles(f)) != 1 {
			t.Errorf("the agent or the [context] task ran: stdout %q, outside %v", res.stdout, outsideFiles(f))
		}
	})
}

// An (agent) task that an agent calls runs its agent program inside the
// sandbox: without [network] it reaches nothing, and Rune opens no endpoint
// for it.
func TestSandboxRuleAgentTaskCalledByAgent(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "# Build.\nbuild:\n    @echo built\n")
	f.write(t, "Runefile", "set agent_cmd := [\"./probe\", \"agent\", \""+f.tcp+"\"]\n\n# Ask the agent.\nask (agent):\n    Do it.\n")
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "ask", nil)
	if res.exit != 0 || res.stdout != "agent: no connection, no endpoint\n" {
		t.Errorf("the agent program: %+v", res)
	}
	if got := f.tcpCount.Load(); got != 0 {
		t.Errorf("the listener saw %d connections", got)
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Errorf("the server stopped answering: %v", err)
	}
}

// A project reached through a link that lies under /tmp, which the sandbox
// replaces: the body runs at the project's real path, and a task that applies
// a plan is handed the plan at a path that exists for it.
func TestSandboxPlanApplyThroughLink(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, planApplyRunefile)
	f.write(t, "state.serial", "1\n")
	linkDir, err := os.MkdirTemp("/tmp", "rune-link-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(linkDir) })
	link := filepath.Join(linkDir, "project")
	if err := os.Symlink(f.dir, link); err != nil {
		t.Fatal(err)
	}
	project := f.dir
	f.dir = link
	cs, _, _ := f.server(t, (&asker{answer: yes}).options())
	if res := callTask(t, cs, "plan", prodArgs); res.isError || res.exit != 0 {
		t.Fatalf("plan: %+v", res)
	}
	if res := callTask(t, cs, "apply", prodArgs); res.isError || res.exit != 0 {
		t.Fatalf("apply: %+v", res)
	}
	applied, err := os.ReadFile(filepath.Join(project, "applied.txt"))
	if err != nil || strings.TrimSpace(string(applied)) != "plan env=prod serial=1" {
		t.Errorf("the applied plan: %q, %v", applied, err)
	}
}
