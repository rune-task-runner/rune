package integration

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Spec 055: `rune agent init <client>` writes the project files that set an
// agent client up to work through Rune. These tests run the built binary and
// look at the project directory from the outside.
//
// Every run sets PATH to a directory the test made, so a client installed on
// the host is never found and never started.

const agentInitRunefile = "# Build it.\nbuild:\n    @echo build-ok\n"

// The versions the adapters were checked with (spec 047).
const (
	checkedClaude = "2.1.325"
	checkedCodex  = "0.167.0"
)

// agentProject makes a project directory with a Runefile and the files given.
func agentProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := writeRunefile(t, agentInitRunefile)
	for name, text := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), text)
	}
	return dir
}

// noClient is a PATH directory with no client in it.
func noClient(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// fakeClient is a PATH directory with a program of that name that prints a
// version. The stand-in is a shell script, so the caller skips on Windows.
func fakeClient(t *testing.T, name, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in client is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho '"+version+" (stand-in)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runAgentInit runs `rune agent args...` in dir with pathDir as the PATH.
func runAgentInit(t *testing.T, dir, pathDir string, args ...string) result {
	t.Helper()
	return run(t, dir, []string{"PATH=" + pathDir}, append([]string{"agent"}, args...)...)
}

func TestAgentInit_NewProject(t *testing.T) {
	for client, files := range map[string][]string{
		"claude": {".mcp.json", ".claude/settings.json"},
		"codex":  {".codex/config.toml"},
	} {
		t.Run(client, func(t *testing.T) {
			dir := agentProject(t, nil)
			got := runAgentInit(t, dir, noClient(t), "init", client)
			if got.code != 1 || !strings.Contains(got.stderr, "outcome: Registered only, pending ") ||
				!strings.Contains(got.stderr, "reason: no version was found\n") ||
				!strings.HasSuffix(got.stderr, "rune: agent init: the outcome is \"Registered only\"\n") {
				t.Errorf("no client on PATH: exit %d\n%s", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
			for _, file := range files {
				if !exists(t, dir, filepath.FromSlash(file)) {
					t.Errorf("%s was not written", file)
				}
			}

			// A second run writes nothing.
			old := time.Now().Add(-time.Hour).Truncate(time.Second)
			for _, file := range files {
				if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(file)), old, old); err != nil {
					t.Fatal(err)
				}
			}
			before := dirSnapshot(t, dir)
			again := runAgentInit(t, dir, noClient(t), "init", client)
			if again.code != 1 || !strings.Contains(again.stderr, files[0]+": unchanged\n") {
				t.Errorf("second run: exit %d\n%s", again.code, again.stderr)
			}
			if !maps.Equal(before, dirSnapshot(t, dir)) {
				t.Error("a second run changed a file")
			}
			for _, file := range files {
				info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file)))
				if err != nil || !info.ModTime().Equal(old) {
					t.Errorf("%s was written again by a run that changes nothing", file)
				}
			}
		})
	}
}

// The outcome follows the client's version, and the files do not (SC-005).
func TestAgentInit_OutcomeFollowsTheVersion(t *testing.T) {
	for client, checked := range map[string]string{"claude": checkedClaude, "codex": checkedCodex} {
		t.Run(client, func(t *testing.T) {
			dir := agentProject(t, nil)
			got := runAgentInit(t, dir, fakeClient(t, client, checked), "init", client)
			if got.code != 0 || !strings.Contains(got.stderr, "outcome: Rune-only, pending ") || strings.Contains(got.stderr, "Registered only") {
				t.Fatalf("checked version: exit %d\n%s", got.code, got.stderr)
			}
			written := dirSnapshot(t, dir)

			for name := range written {
				if name != "Runefile" {
					if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
						t.Fatal(err)
					}
				}
			}
			got = runAgentInit(t, dir, fakeClient(t, client, "9.9.9"), "init", client)
			if got.code != 1 || !strings.Contains(got.stderr, "outcome: Registered only, pending ") ||
				!strings.Contains(got.stderr, "reason: this version was not checked\n") || strings.Contains(got.stderr, "Rune-only") {
				t.Errorf("unchecked version: exit %d\n%s", got.code, got.stderr)
			}
			if !maps.Equal(written, dirSnapshot(t, dir)) {
				t.Error("an unchecked version wrote other files than a checked one")
			}
		})
	}
}

// The command takes the name `agent`. A task of that name is still run with
// `rune -- agent`, and `rune agent` never succeeds by doing nothing.
func TestAgentInit_TaskNamedAgent(t *testing.T) {
	dir := writeRunefile(t, "# Ask.\nagent:\n    @echo from-the-task\n")
	const note = "note: \"agent\" is also a task of this Runefile; run it with: rune -- agent\n"

	if got := run(t, dir, nil, "--", "agent"); got.code != 0 || got.stdout != "from-the-task\n" {
		t.Errorf("rune -- agent: exit %d, stdout %q\n%s", got.code, got.stdout, got.stderr)
	}
	if got := run(t, dir, nil, "agent"); got.code != 0 || got.stderr != note || !strings.Contains(got.stdout, "init") {
		t.Errorf("rune agent: exit %d, stderr %q\n%s", got.code, got.stderr, got.stdout)
	}
	got := run(t, dir, nil, "agent", "deploy")
	want := "rune: unknown command \"deploy\" for \"rune agent\"; to run a task named agent, use: rune -- agent\n"
	if got.code != 2 || got.stderr != want || got.stdout != "" {
		t.Errorf("rune agent deploy: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runAgentInit(t, dir, noClient(t), "init", "codex", "--check"); !strings.HasPrefix(got.stderr, note) {
		t.Errorf("rune agent init: the note is missing:\n%s", got.stderr)
	}
}

// Issue #53: the server starts for the right project even when the client
// starts it somewhere else. The arguments are the ones the command wrote.
func TestAgentInit_RegisteredServerServesTheProject(t *testing.T) {
	dir := agentProject(t, nil)
	if got := runAgentInit(t, dir, noClient(t), "init", "claude"); got.code != 1 {
		t.Fatalf("setup: exit %d\n%s", got.code, got.stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v\n%s", err, data)
	}
	server := config.MCPServers["rune"]
	if server.Type != "stdio" || server.Command != "rune" {
		t.Fatalf("server = %+v", server)
	}

	p := startMCPArgs(t, t.TempDir(), fileFlagProtocol, server.Args...)
	p.wantTools("build")
	if got := p.call("build"); got != "build-ok\n[exit 0]" {
		t.Errorf("call build = %q", got)
	}
}

// An existing file keeps everything the command does not own (SC-002).
func TestAgentInit_KeepsExistingConfiguration(t *testing.T) {
	fixture := filepath.Join("..", "..", "internal", "agentinit", "testdata", "claude", "other-servers", "in")
	dir := agentProject(t, nil)
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	before := dirSnapshot(t, dir)
	if got := runAgentInit(t, dir, noClient(t), "init", "claude"); got.code != 1 {
		t.Fatalf("exit %d\n%s", got.code, got.stderr)
	}
	after := dirSnapshot(t, dir)
	for _, file := range []string{".mcp.json", ".claude/settings.json"} {
		if !bytesSubsequence(before[file], after[file]) {
			t.Errorf("%s lost bytes it had:\n%s", file, after[file])
		}
	}
	for _, want := range []string{`"docs": {`, `"db": { "command": "db-mcp"`, `"unknownKey": [1, 2, {"deep": null}]`, `"rune": {`} {
		if !strings.Contains(after[".mcp.json"], want) {
			t.Errorf(".mcp.json lacks %q:\n%s", want, after[".mcp.json"])
		}
	}
	runAgentInit(t, dir, noClient(t), "init", "claude")
	if !maps.Equal(after, dirSnapshot(t, dir)) {
		t.Error("a second run changed a file")
	}
}

func bytesSubsequence(sub, all string) bool {
	i := 0
	for j := 0; j < len(all) && i < len(sub); j++ {
		if sub[i] == all[j] {
			i++
		}
	}
	return i == len(sub)
}

func TestAgentInit_ConflictWritesNothing(t *testing.T) {
	const mcp = "{\n  \"mcpServers\": {\n    \"rune\": { \"type\": \"stdio\", \"command\": \"rune\", \"args\": [\"mcp\"], \"env\": { \"A\": \"b\" } }\n  }\n}\n"
	dir := agentProject(t, map[string]string{".mcp.json": mcp})
	before := dirSnapshot(t, dir)

	got := runAgentInit(t, dir, noClient(t), "init", "claude")
	if got.code != 2 || !strings.Contains(got.stderr, ".mcp.json:3:59") || !strings.Contains(got.stderr, "run again with --force") ||
		!strings.HasSuffix(got.stderr, "rune: agent init: 1 conflict(s), nothing written\n") || strings.Contains(got.stderr, "outcome:") {
		t.Errorf("conflict: exit %d\n%s", got.code, got.stderr)
	}
	if !maps.Equal(before, dirSnapshot(t, dir)) {
		t.Error("a run with a conflict changed the project")
	}

	got = runAgentInit(t, dir, noClient(t), "init", "claude", "--force")
	if got.code != 1 || !strings.Contains(got.stderr, ".mcp.json: updated: replaced args of server \"rune\"\n") {
		t.Errorf("--force: exit %d\n%s", got.code, got.stderr)
	}
	if after := dirSnapshot(t, dir)[".mcp.json"]; !strings.Contains(after, `"env": { "A": "b" }`) || strings.Contains(after, `"args": ["mcp"]`) {
		t.Errorf("--force did not replace only the value:\n%s", after)
	}

	// A file that cannot be written leaves the project as it was.
	blocked := agentProject(t, map[string]string{".codex": "a file, not a directory\n"})
	before = dirSnapshot(t, blocked)
	if got := runAgentInit(t, blocked, noClient(t), "init", "codex"); got.code != 1 {
		t.Errorf(".codex is a file: exit %d\n%s", got.code, got.stderr)
	}
	if !maps.Equal(before, dirSnapshot(t, blocked)) {
		t.Error("a run that could not write changed the project")
	}
}

// --dry-run and --check write nothing, and the difference a dry run prints
// is the change a real run makes (SC-004).
func TestAgentInit_DryRunAndCheck(t *testing.T) {
	files := map[string]string{
		".claude/settings.json": "{\n  \"permissions\": {\n    \"deny\": [\n      \"WebFetch\"\n    ]\n  }\n}\n",
	}
	dir := agentProject(t, files)
	before := dirSnapshot(t, dir)

	// --unpinned, so that the same files come out in another directory.
	dry := runAgentInit(t, dir, noClient(t), "init", "claude", "--dry-run", "--unpinned")
	if dry.code != 1 || !strings.Contains(dry.stdout, "--- /dev/null\n+++ b/.mcp.json\n") || !strings.HasSuffix(dry.stderr,
		"dry run: nothing written\nrune: agent init: the outcome is \"Registered only\"\n") {
		t.Errorf("--dry-run: exit %d\n%s\n%s", dry.code, dry.stdout, dry.stderr)
	}
	check := runAgentInit(t, dir, noClient(t), "init", "claude", "--check", "--unpinned")
	if check.code != 1 || !strings.Contains(check.stderr, "incomplete: 10 of 10 entries are missing or different.") {
		t.Errorf("--check on a project never set up: exit %d\n%s", check.code, check.stderr)
	}
	if !maps.Equal(before, dirSnapshot(t, dir)) {
		t.Error("--dry-run or --check changed the project")
	}

	if got := runAgentInit(t, dir, noClient(t), "init", "claude", "--unpinned"); got.code != 1 {
		t.Fatalf("setup: exit %d\n%s", got.code, got.stderr)
	}
	written := dirSnapshot(t, dir)

	// A complete setup passes the check on a machine with no client.
	check = runAgentInit(t, dir, noClient(t), "init", "claude", "--check", "--unpinned")
	if check.code != 0 || !strings.Contains(check.stderr, "complete: all 10 entries are present\n") || strings.Contains(check.stderr, "outcome:") {
		t.Errorf("--check on a complete setup: exit %d\n%s", check.code, check.stderr)
	}
	if !maps.Equal(written, dirSnapshot(t, dir)) {
		t.Error("--check changed the project")
	}

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed: the printed difference is not applied")
	}
	copyDir := agentProject(t, files)
	patch := filepath.Join(t.TempDir(), "setup.patch")
	if err := os.WriteFile(patch, []byte(dry.stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	apply := exec.Command(git, "apply", patch)
	apply.Dir = copyDir
	if out, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("git apply: %v\n%s\n--- patch ---\n%s", err, out, dry.stdout)
	}
	if applied := dirSnapshot(t, copyDir); !maps.Equal(applied, written) {
		t.Errorf("the applied difference gives %v\nwant %v", applied, written)
	}
}

// The two adapters share .mcp.json and never conflict over it.
func TestAgentInit_VSCodeAfterClaude(t *testing.T) {
	dir := agentProject(t, nil)
	if got := runAgentInit(t, dir, noClient(t), "init", "claude"); got.code != 1 {
		t.Fatalf("claude: exit %d\n%s", got.code, got.stderr)
	}
	claude := dirSnapshot(t, dir)

	got := runAgentInit(t, dir, noClient(t), "init", "vscode")
	if got.code != 1 || !strings.Contains(got.stderr, ".mcp.json: unchanged\n") ||
		!strings.Contains(got.stderr, "outcome: Registered only, pending 2 steps\n") ||
		!strings.Contains(got.stderr, "reason: no VS Code version has been checked with these files\n") {
		t.Errorf("vscode: exit %d\n%s", got.code, got.stderr)
	}
	after := dirSnapshot(t, dir)
	for _, file := range []string{".mcp.json", ".claude/settings.json"} {
		if after[file] != claude[file] {
			t.Errorf("%s changed", file)
		}
	}
	agent := after[".github/agents/rune.agent.md"]
	if !strings.Contains(agent, "tools: ['rune/*']\n") || !strings.Contains(agent, "agents: []\n") {
		t.Errorf("agent file:\n%s", agent)
	}
	if !strings.Contains(after[".vscode/mcp.json"], "\"servers\": {\n    \"rune\": {") {
		t.Errorf(".vscode/mcp.json:\n%s", after[".vscode/mcp.json"])
	}
}

// Nothing outside the project is written (SC-008).
func TestAgentInit_RefusesALinkOutOfTheProject(t *testing.T) {
	dir := agentProject(t, nil)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".claude")); err != nil {
		t.Skipf("cannot make a symbolic link: %v", err)
	}
	got := runAgentInit(t, dir, noClient(t), "init", "claude")
	if got.code != 2 || !strings.Contains(got.stderr, ".claude leads outside the project, to ") {
		t.Errorf("exit %d\n%s", got.code, got.stderr)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Errorf("the directory outside the project holds %v (%v)", entries, err)
	}
	if exists(t, dir, ".mcp.json") {
		t.Error(".mcp.json was written although the run was refused")
	}
}
