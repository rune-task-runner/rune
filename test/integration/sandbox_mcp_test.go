package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 053: the tasks an agent calls run in a sandbox, or the call is
// refused. This file holds what is true on every system: a server with no
// backend refuses, a server started unsandboxed says so, and a mode that
// cannot be read stops the server. The tests that start a real sandbox are in
// the _unix files.

const sandboxBasicRunefile = `# Report the project state.
[context]
health:
    @: > context.ran
    @echo healthy

# Build.
build:
    @: > build.ran
    @echo built

# Write a file where the caller says.
write-outside path:
    @: > "{{path}}"
`

// serverEnv is the environment of a server a sandbox test starts: the test's
// own without the harness's RUNE_SANDBOX=off, with the audit log and the
// cache directory in places of the test's, and then extra.
func serverEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+len(extra)+3)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "RUNE_SANDBOX=") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"RUNE_AUDIT_DIR="+t.TempDir(),
		"RUNE_AUDIT_RETENTION_DAYS=",
		"XDG_CACHE_HOME="+t.TempDir(),
	)
	return append(env, extra...)
}

// noBackendEnv is a server environment in which no backend can be found: on
// Linux a PATH without bwrap. Other systems have none whatever the PATH is.
func noBackendEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	return serverEnv(t, append([]string{"PATH=" + t.TempDir()}, extra...)...)
}

// sandboxServer starts `rune mcp` in dir with exactly env and returns a
// client session, the server's standard error and its process.
func sandboxServer(t *testing.T, dir string, env []string, opts *mcp.ClientOptions, flags ...string) (*mcp.ClientSession, *syncBuffer, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command(runeBin, append([]string{"mcp", "--color=never"}, flags...)...)
	cmd.Dir = dir
	cmd.Env = env
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, opts)
	cs, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("the server did not start: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, stderr, cmd
}

// taskResult is what a tool call returned.
type taskResult struct {
	text     string // every text block
	stdout   string
	stderr   string
	exit     int
	isError  bool
	hasExit  bool
	duration time.Duration
}

// callTask calls a tool and reads its text and its structured fields.
func callTask(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) taskResult {
	t.Helper()
	return callTaskCtx(t, context.Background(), cs, tool, args)
}

func callTaskCtx(t *testing.T, ctx context.Context, cs *mcp.ClientSession, tool string, args map[string]any) taskResult {
	t.Helper()
	begin := time.Now()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", tool, err)
	}
	out := taskResult{isError: res.IsError, duration: time.Since(begin)}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	out.text = b.String()
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			ExitCode *int   `json:"exitCode"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("structured content %s: %v", raw, err)
		}
		out.stdout, out.stderr = fields.Stdout, fields.Stderr
		if fields.ExitCode != nil {
			out.exit, out.hasExit = *fields.ExitCode, true
		}
	}
	return out
}

// envValue is the value env gives name, or "".
func envValue(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, name+"="); ok {
			value = v
		}
	}
	return value
}

// auditEnv is what `rune log` needs to read the log of a server started with
// env.
func auditEnv(env []string) []string {
	return []string{"RUNE_AUDIT_DIR=" + envValue(env, "RUNE_AUDIT_DIR"), "RUNE_AUDIT_RETENTION_DAYS="}
}

// noBackendReason is why this host has no sandbox when the PATH holds no
// bwrap.
func noBackendReason() string {
	switch runtime.GOOS {
	case "linux":
		return "bubblewrap (bwrap) was not found on the PATH"
	case "darwin":
		return "this system (macos) has no sandbox backend"
	}
	return "this system (" + runtime.GOOS + ") has no sandbox backend"
}

func noBackendRemedy() string {
	if runtime.GOOS == "linux" {
		return "Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
	}
	return "Turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
}

// Quickstart N: a server with no backend starts, says so, serves its tool
// list and refuses every call before anything runs.
func TestSandboxUnavailableRefuses(t *testing.T) {
	dir := writeRunefile(t, sandboxBasicRunefile)
	env := noBackendEnv(t)
	cs, stderr, _ := sandboxServer(t, dir, env, nil)

	got := waitForText(t, stderr, "rune MCP server on stdio", 1)
	warning := "warning: sandbox: " + noBackendReason() + ". Every task call will be refused. " + noBackendRemedy() + "\n"
	if !strings.Contains(got, warning) {
		t.Errorf("stderr lacks the warning %q:\n%s", warning, got)
	}
	if !strings.Contains(got, "rune MCP server on stdio (sandbox: unavailable, calls are refused)\n") {
		t.Errorf("stderr lacks the startup line:\n%s", got)
	}
	if strings.Index(got, "warning: sandbox:") > strings.Index(got, "rune MCP server on") {
		t.Errorf("the warning came after the startup line:\n%s", got)
	}

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("tools: %v, %v", tools, err)
	}

	res := callTask(t, cs, "build", nil)
	want := `task "build" was not run: the sandbox is unavailable: ` + noBackendReason() + ". " + noBackendRemedy()
	if !res.isError || !strings.Contains(res.text, want) {
		t.Errorf("result: isError %v, text %q, want %q in it", res.isError, res.text, want)
	}
	if res.duration > time.Second {
		t.Errorf("the refusal took %v", res.duration)
	}
	if hasMarker(dir, "build.ran") {
		t.Error("the task ran with no sandbox")
	}

	call := lastLogCall(t, dir, auditEnv(env))
	if call.Outcome != "refused" || call.ExitCode != nil || call.Error == nil || call.Error.Kind != "sandbox" || !strings.Contains(call.Error.Message, "the sandbox is unavailable") {
		t.Errorf("the record: %+v, error %+v", call, call.Error)
	}
	if call.Sandbox == nil || call.Sandbox.Enforced || call.Sandbox.Backend != "" || call.Sandbox.Mode != "enforce" {
		t.Errorf("the record's sandbox: %+v", call.Sandbox)
	}
	text := run(t, dir, auditEnv(env), "log", "--since", "1h").stdout
	if !strings.Contains(text, `sandbox: task "build" was not run: the sandbox is unavailable`) || strings.Contains(text, "ran without a sandbox") {
		t.Errorf("the listing:\n%s", text)
	}
}

// Quickstart O: with no sandbox the [context] task does not run, and the
// instructions say that the project context is missing.
func TestSandboxUnavailableContext(t *testing.T) {
	dir := writeRunefile(t, sandboxBasicRunefile)
	cs, _, _ := sandboxServer(t, dir, noBackendEnv(t), nil)
	instructions := cs.InitializeResult().Instructions
	if want := `(context hook "health" failed; proceeding without project context)`; instructions != want {
		t.Errorf("instructions = %q, want %q", instructions, want)
	}
	if hasMarker(dir, "context.ran") {
		t.Error("the [context] task ran with no sandbox")
	}
}

// Quickstart P: a server started unsandboxed says so, runs tasks as before
// spec 053, and every record states it.
func TestSandboxOff(t *testing.T) {
	cases := []struct {
		name  string
		env   []string
		flags []string
	}{
		{"the flag", nil, []string{"--sandbox", "off"}},
		{"the variable", []string{"RUNE_SANDBOX=off"}, nil},
		{"the flag over the variable", []string{"RUNE_SANDBOX=enforce"}, []string{"--sandbox", "off"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeRunefile(t, sandboxBasicRunefile)
			outside := filepath.Join(t.TempDir(), "written")
			// A PATH with no backend: unsandboxed must not need one.
			env := noBackendEnv(t, c.env...)
			cs, stderr, _ := sandboxServer(t, dir, env, nil, c.flags...)

			got := waitForText(t, stderr, "rune MCP server on stdio", 1)
			if !strings.Contains(got, sandboxOffWarning+"\n") {
				t.Errorf("stderr lacks the warning:\n%s", got)
			}
			if !strings.Contains(got, "rune MCP server on stdio ("+sandboxOffPart+")\n") {
				t.Errorf("stderr lacks the startup line:\n%s", got)
			}
			if !hasMarker(dir, "context.ran") || cs.InitializeResult().Instructions != "healthy" {
				t.Errorf("the [context] task: ran %v, instructions %q", hasMarker(dir, "context.ran"), cs.InitializeResult().Instructions)
			}

			res := callTask(t, cs, "write-outside", map[string]any{"path": filepath.ToSlash(outside)})
			if res.isError || res.exit != 0 {
				t.Fatalf("result: %+v", res)
			}
			if _, err := os.Stat(outside); err != nil {
				t.Errorf("the task did not write outside the project: %v", err)
			}

			call := lastLogCall(t, dir, auditEnv(env))
			if call.Sandbox == nil || call.Sandbox.Enforced || call.Sandbox.Backend != "" || call.Sandbox.Mode != "off" {
				t.Errorf("the record's sandbox: %+v", call.Sandbox)
			}
			text := run(t, dir, auditEnv(env), "log", "--since", "1h").stdout
			if !strings.Contains(text, "ran without a sandbox (the server was started with the sandbox off)") {
				t.Errorf("the listing:\n%s", text)
			}
		})
	}

	// A dotenv file is the Runefile's, and the Runefile cannot choose the
	// mode: a task could edit it.
	t.Run("a dotenv file does not turn it off", func(t *testing.T) {
		dir := writeRunefile(t, "set dotenv := \".env\"\n\n"+sandboxBasicRunefile)
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("RUNE_SANDBOX=off\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cs, stderr, _ := sandboxServer(t, dir, noBackendEnv(t), nil)
		got := waitForText(t, stderr, "rune MCP server on stdio", 1)
		if strings.Contains(got, "sandbox: off") || !strings.Contains(got, "sandbox: unavailable, calls are refused") {
			t.Errorf("the dotenv file changed the mode:\n%s", got)
		}
		if res := callTask(t, cs, "build", nil); !res.isError || hasMarker(dir, "build.ran") {
			t.Errorf("the call ran: %+v", res)
		}
	})
}

// Quickstart Q: a mode that cannot be read stops the server with a usage
// error, before any Runefile code runs.
func TestSandboxBadMode(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"the flag", nil, []string{"mcp", "--sandbox", "maybe"}, `rune: --sandbox: "maybe" is not a mode; use enforce or off`},
		{"the variable", []string{"RUNE_SANDBOX=maybe"}, []string{"mcp"}, `rune: RUNE_SANDBOX: "maybe" is not a mode; use enforce or off`},
		{"an empty flag", nil, []string{"mcp", "--sandbox", ""}, `rune: --sandbox: "" is not a mode; use enforce or off`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeRunefile(t, sandboxBasicRunefile)
			res := run(t, dir, c.env, c.args...)
			if res.code != 2 || !strings.Contains(res.stderr, c.want) {
				t.Errorf("exit %d, stderr %q, want %q", res.code, res.stderr, c.want)
			}
			if hasMarker(dir, "context.ran") {
				t.Error("the [context] task ran before the usage error")
			}
		})
	}
}

// The two internal ways to start the binary: the worker run by hand has no
// pipes and says so, and the probe exits 0 in silence.
func TestSandboxWorkerByHand(t *testing.T) {
	dir := t.TempDir()
	res := run(t, dir, nil, "--sandbox-worker")
	if res.code != 2 || res.stderr != "rune: --sandbox-worker is started by rune itself\n" || res.stdout != "" {
		t.Errorf("worker: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
	}
	res = run(t, dir, nil, "--sandbox-probe")
	if res.code != 0 || res.stdout != "" || res.stderr != "" {
		t.Errorf("probe: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
	}
}

// Quickstart W: a task a person runs is not confined, whatever RUNE_SANDBOX
// holds, also a value that is no mode.
func TestSandboxCommandLineIgnoresMode(t *testing.T) {
	for _, mode := range []string{"enforce", "off", "maybe"} {
		t.Run(mode, func(t *testing.T) {
			dir := writeRunefile(t, sandboxBasicRunefile)
			outside := filepath.Join(t.TempDir(), "written")
			res := run(t, dir, []string{"RUNE_SANDBOX=" + mode}, "write-outside", filepath.ToSlash(outside))
			if res.code != 0 {
				t.Fatalf("exit %d: %s", res.code, res.stderr)
			}
			if _, err := os.Stat(outside); err != nil {
				t.Errorf("the task did not write outside the project: %v", err)
			}
			if strings.Contains(res.stderr, "sandbox") {
				t.Errorf("a person's run mentioned the sandbox: %q", res.stderr)
			}
		})
	}
}

// A directory with two Runefiles is an error on the command line and for a
// server: taking one of them would let a file a task wrote beside the
// Runefile replace it at the next start.
func TestSandboxAmbiguousRunefile(t *testing.T) {
	dir := writeRunefile(t, sandboxBasicRunefile)
	if err := os.WriteFile(filepath.Join(dir, ".runefile"), []byte("# Planted.\nbuild:\n    @: > planted.ran\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "holds more than one Runefile (.runefile, Runefile); remove all but one, or name one with --file"
	for _, args := range [][]string{{"build"}, {"mcp"}, {"--list"}} {
		res := run(t, dir, nil, args...)
		if res.code != 2 || !strings.Contains(res.stderr, want) {
			t.Errorf("rune %v: exit %d, stderr %q", args, res.code, res.stderr)
		}
	}
	if hasMarker(dir, "planted.ran") || hasMarker(dir, "build.ran") || hasMarker(dir, "context.ran") {
		t.Error("a task ran")
	}
	if res := run(t, dir, nil, "-f", "Runefile", "build"); res.code != 0 || !hasMarker(dir, "build.ran") {
		t.Errorf("with --file: exit %d, stderr %q", res.code, res.stderr)
	}
}
