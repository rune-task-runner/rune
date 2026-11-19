package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

const agentRunefile = "# Build it.\nbuild:\n    @echo build-ok\n"

// checkedVersion answers as a client whose version the adapters were checked
// with.
func checkedVersion(_ context.Context, binary string) string {
	return map[string]string{"claude": "2.1.325", "codex": "0.167.0", "code": "1.146.0"}[binary]
}

// runAgentInit runs AgentInit in dir with a client that reports a checked
// version, unless flags names another lookup.
func runAgentInit(t *testing.T, dir string, flags AgentInitFlags, client string) (stdout, stderr string, err error) {
	t.Helper()
	if flags.version == nil {
		flags.version = checkedVersion
	}
	var out, errb bytes.Buffer
	err = AgentInit(Options{Cwd: dir, Stdout: &out, Stderr: &errb}, flags, client)
	return out.String(), errb.String(), err
}

// wantErr fails unless err is of type T with the message given.
func wantErr[T error](t *testing.T, err error, message string) {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("err = %v (%T), want a %T", err, err, target)
	}
	if err.Error() != message {
		t.Errorf("err = %q, want %q", err.Error(), message)
	}
}

// jsonPath is a path as it stands inside a JSON string.
func jsonPath(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(path, `\`, `\\`), `"`, `\"`)
}

func TestAgentInitClaudeNewProject(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile})
	stdout, stderr, err := runAgentInit(t, dir, AgentInitFlags{}, "claude")
	if err != nil {
		t.Fatalf("AgentInit: %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: the report goes to stderr", stdout)
	}
	runefile := filepath.Join(dir, "Runefile")
	wantMCP := "{\n  \"mcpServers\": {\n    \"rune\": {\n      \"type\": \"stdio\",\n      \"command\": \"rune\",\n" +
		"      \"args\": [\"mcp\", \"--file\", \"" + jsonPath(runefile) + "\", \"--no-reload\"]\n    }\n  }\n}\n"
	if got := readFile(t, filepath.Join(dir, ".mcp.json")); got != wantMCP {
		t.Errorf(".mcp.json = %q\nwant        %q", got, wantMCP)
	}
	wantSettings := "{\n  \"permissions\": {\n    \"deny\": [\"Bash\", \"PowerShell\", \"Monitor\", \"Workflow\", \"CronCreate\", \"RemoteTrigger\"],\n" +
		"    \"allow\": [\"mcp__rune\"]\n  }\n}\n"
	if got := readFile(t, filepath.Join(dir, ".claude", "settings.json")); got != wantSettings {
		t.Errorf("settings.json = %q\nwant            %q", got, wantSettings)
	}
	for _, want := range []string{
		".mcp.json: created: server \"rune\" runs: rune mcp --file " + shellWord(runefile, runtime.GOOS) + " --no-reload\n",
		".claude/settings.json: created: deny Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger; allow mcp__rune\n",
		"client: Claude Code 2.1.325 (checked: 2.1.325",
		"outcome: Rune-only, pending 2 steps\n",
		"step: trust the project: start claude in " + shellWord(dir, runtime.GOOS) + " and accept the trust question.",
		"note: the server is registered with this checkout's path. For a file you commit, use --unpinned.\n",
		"check: claude -p hi --output-format stream-json --verbose\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]fs.FileMode{".mcp.json": 0o644, ".claude": 0o755 | fs.ModeDir, ".claude/settings.json": 0o644} {
			info, err := os.Stat(filepath.Join(dir, path))
			if err != nil || info.Mode() != want {
				t.Errorf("%s has mode %v, want %v (%v)", path, info.Mode(), want, err)
			}
		}
	}

	// A second run writes nothing.
	before := map[string]time.Time{}
	for _, path := range []string{".mcp.json", ".claude/settings.json"} {
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(dir, path), old, old); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(filepath.Join(dir, path))
		before[path] = info.ModTime()
	}
	_, stderr, err = runAgentInit(t, dir, AgentInitFlags{}, "claude")
	if err != nil || !strings.Contains(stderr, ".mcp.json: unchanged\n.claude/settings.json: unchanged\n") {
		t.Errorf("second run: %v\n%s", err, stderr)
	}
	for path, old := range before {
		if info, _ := os.Stat(filepath.Join(dir, path)); !info.ModTime().Equal(old) {
			t.Errorf("%s was written again by a run that changes nothing", path)
		}
	}
}

func TestAgentInitCodexNewProject(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile})
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{}, "codex")
	if err != nil {
		t.Fatalf("AgentInit: %v\n%s", err, stderr)
	}
	got := readFile(t, filepath.Join(dir, ".codex", "config.toml"))
	for _, want := range []string{"[features]\nshell_tool = false\n", "[agents]\nenabled = false\n", "[mcp_servers.rune]\ncommand = \"rune\"\nargs = [\"mcp\", \"--file\", "} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(stderr, "outcome: Rune-only, pending 1 step\n") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// The project is the directory of the Runefile, wherever the command runs.
func TestAgentInitFindsTheProject(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, "sub/deep/keep": ""})
	if _, stderr, err := runAgentInit(t, filepath.Join(dir, "sub", "deep"), AgentInitFlags{}, "codex"); err != nil {
		t.Fatalf("from a subdirectory: %v\n%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".codex", "config.toml")); err != nil {
		t.Errorf("the file is not in the Runefile's directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "deep", ".codex")); err == nil {
		t.Error("the file was written in the working directory")
	}

	other := importProject(t, map[string]string{"Runefile": agentRunefile})
	elsewhere := t.TempDir()
	var errb bytes.Buffer
	opts := Options{Cwd: elsewhere, File: filepath.Join(other, "Runefile"), Stdout: &errb, Stderr: &errb}
	if err := AgentInit(opts, AgentInitFlags{version: checkedVersion}, "codex"); err != nil {
		t.Fatalf("--file: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(filepath.Join(other, ".codex", "config.toml")); err != nil {
		t.Errorf("--file: the file is not in the named project: %v", err)
	}
	if got := dirNames(t, elsewhere); got != "" {
		t.Errorf("--file: the working directory holds %q", got)
	}
}

func TestAgentInitUsageErrors(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile})
	_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "cursor")
	wantErr[*UsageError](t, err, `unknown client "cursor": the clients are claude, codex and vscode`)

	for _, flags := range []AgentInitFlags{{Check: true, Force: true}, {Check: true, DryRun: true}} {
		_, _, err = runAgentInit(t, dir, flags, "claude")
		wantErr[*UsageError](t, err, "--check cannot be combined with --force or --dry-run")
	}

	_, _, err = runAgentInit(t, t.TempDir(), AgentInitFlags{}, "claude")
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "no Runefile found") {
		t.Errorf("no Runefile: err = %v", err)
	}
	if got := dirNames(t, dir); got != "Runefile" {
		t.Errorf("a usage error left %q", got)
	}
}

// A server registered for a Runefile with errors would not start.
func TestAgentInitRunefileWithErrors(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": "build: missing\n    @echo x\n"})
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{}, "claude")
	wantErr[*ValidationError](t, err, "1 error(s)")
	if !strings.Contains(stderr, "Runefile:1:") || !strings.Contains(stderr, "missing") {
		t.Errorf("stderr does not show the diagnostic:\n%s", stderr)
	}
	if got := dirNames(t, dir); got != "Runefile" {
		t.Errorf("files were written: %q", got)
	}
}

// Setting a client up runs nothing of the Runefile.
func TestAgentInitRunsNothing(t *testing.T) {
	runefile := "# Health.\n[context]\nhealth:\n    @echo x > marker-context\n\n" +
		"# Build it.\nbuild: && after\n    @echo x > marker-task\n\n# After.\nafter:\n    @echo x > marker-hook\n"
	dir := importProject(t, map[string]string{"Runefile": runefile})
	for _, client := range []string{"claude", "codex", "vscode"} {
		for _, flags := range []AgentInitFlags{{DryRun: true}, {Check: true}, {}} {
			_, stderr, err := runAgentInit(t, dir, flags, client)
			var validation *ValidationError
			var usage *UsageError
			if errors.As(err, &validation) || errors.As(err, &usage) {
				t.Fatalf("%s %+v: %v\n%s", client, flags, err, stderr)
			}
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "marker-*"))
	if err != nil || len(entries) != 0 {
		t.Errorf("something of the Runefile ran: %v", entries)
	}
}

func TestAgentInitNotesATaskNamedAgent(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": "# Ask.\nagent:\n    @echo hi\n"})
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{}, "codex")
	if err != nil || !strings.HasPrefix(stderr, agentTaskNote+"\n") {
		t.Errorf("err = %v, stderr:\n%s", err, stderr)
	}

	var errb bytes.Buffer
	AgentTaskNote(Options{Cwd: dir, Stderr: &errb})
	if errb.String() != agentTaskNote+"\n" {
		t.Errorf("AgentTaskNote printed %q", errb.String())
	}
	for _, cwd := range []string{importProject(t, map[string]string{"Runefile": agentRunefile}), t.TempDir()} {
		errb.Reset()
		AgentTaskNote(Options{Cwd: cwd, Stderr: &errb})
		if errb.Len() != 0 {
			t.Errorf("AgentTaskNote printed %q with no task named agent", errb.String())
		}
	}
}

// Nothing outside the project is written: a link that leads out of it is
// refused, whatever it leads to.
func TestAgentInitRefusesALinkOutOfTheProject(t *testing.T) {
	link := func(t *testing.T, target, name string) {
		t.Helper()
		if err := os.Symlink(target, name); err != nil {
			t.Skipf("cannot make a symbolic link: %v", err)
		}
	}

	t.Run("directory", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile})
		outside := t.TempDir()
		link(t, outside, filepath.Join(dir, ".claude"))
		for _, flags := range []AgentInitFlags{{}, {DryRun: true}, {Check: true}} {
			_, _, err := runAgentInit(t, dir, flags, "claude")
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), ".claude leads outside the project, to ") ||
				!strings.HasSuffix(err.Error(), ". rune agent init writes only inside the project") {
				t.Errorf("%+v: err = %v", flags, err)
			}
		}
		if got := dirNames(t, outside); got != "" {
			t.Errorf("the directory outside the project holds %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
			t.Error(".mcp.json was written although the run was refused")
		}
	})

	t.Run("file", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile})
		outside := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(outside, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		link(t, outside, filepath.Join(dir, ".codex", "config.toml"))
		_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "codex")
		var usage *UsageError
		if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), ".codex/config.toml leads outside the project, to ") {
			t.Errorf("err = %v", err)
		}
		if got := readFile(t, outside); got != "# mine\n" {
			t.Errorf("the file outside the project changed: %q", got)
		}
	})

	t.Run("a link that leads nowhere", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile})
		link(t, filepath.Join(dir, "gone"), filepath.Join(dir, ".codex"))
		_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "codex")
		var usage *UsageError
		if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), ".codex: ") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a link inside the project is followed", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile, "conf/codex/config.toml": "# mine\n"})
		link(t, filepath.Join("conf", "codex"), filepath.Join(dir, ".codex"))
		if _, stderr, err := runAgentInit(t, dir, AgentInitFlags{}, "codex"); err != nil {
			t.Fatalf("err = %v\n%s", err, stderr)
		}
		got := readFile(t, filepath.Join(dir, "conf", "codex", "config.toml"))
		if !strings.HasPrefix(got, "# mine\n") || !strings.Contains(got, "shell_tool = false") {
			t.Errorf("the file behind the link is %q", got)
		}
		if info, err := os.Lstat(filepath.Join(dir, ".codex")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf(".codex is no longer a link: %v", err)
		}
	})
}

// A write that fails leaves no half setup: what the run already wrote is
// put back.
func TestAgentInitPutsFilesBackWhenAWriteFails(t *testing.T) {
	failOn := func(suffix string) func(string, []byte) error {
		return func(path string, data []byte) error {
			if strings.HasSuffix(filepath.ToSlash(path), suffix) {
				return errors.New("disk is full")
			}
			return writeFileAtomic(path, data)
		}
	}

	t.Run("an updated file gets its bytes and mode back", func(t *testing.T) {
		const old = "{\"mcpServers\": {\"docs\": {\"command\": \"docs\"}}}\n"
		dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".mcp.json": old})
		if err := os.Chmod(filepath.Join(dir, ".mcp.json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, stderr, err := runAgentInit(t, dir, AgentInitFlags{write: failOn(".claude/settings.json")}, "claude")
		wantErr[*CommandFailure](t, err, ".claude/settings.json: disk is full")
		if got := readFile(t, filepath.Join(dir, ".mcp.json")); got != old {
			t.Errorf(".mcp.json = %q, want it as it was", got)
		}
		if info, _ := os.Stat(filepath.Join(dir, ".mcp.json")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf(".mcp.json has mode %v, want 0600", info.Mode().Perm())
		}
		if got := dirNames(t, dir); got != ".mcp.json Runefile" {
			t.Errorf("the project holds %q", got)
		}
		if strings.Contains(stderr, "outcome:") {
			t.Errorf("an outcome is named for a setup that was not written:\n%s", stderr)
		}
	})

	t.Run("a created file and its directory are removed", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile})
		_, _, err := runAgentInit(t, dir, AgentInitFlags{write: failOn(".github/agents/rune.agent.md")}, "vscode")
		wantErr[*CommandFailure](t, err, ".github/agents/rune.agent.md: disk is full")
		if got := dirNames(t, dir); got != "Runefile" {
			t.Errorf("the project holds %q, want only the Runefile", got)
		}
	})

	t.Run("a directory that cannot be made", func(t *testing.T) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".github": "a file, not a directory\n"})
		_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "vscode")
		var failure *CommandFailure
		if !errors.As(err, &failure) || !strings.Contains(err.Error(), "rune.agent.md") {
			t.Errorf("err = %v, want a CommandFailure that names the agent file", err)
		}
		if got := dirNames(t, dir); got != ".github Runefile" {
			t.Errorf("the project holds %q", got)
		}
	})
}

func TestAgentInitClientFileThatIsADirectory(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".mcp.json/keep": ""})
	_, _, err := runAgentInit(t, dir, AgentInitFlags{}, "claude")
	var failure *CommandFailure
	if !errors.As(err, &failure) || !strings.Contains(err.Error(), ".mcp.json") {
		t.Errorf("err = %v, want a CommandFailure that names .mcp.json", err)
	}
}

func TestAgentInitConflict(t *testing.T) {
	const mcp = "{\n  \"mcpServers\": {\n    \"rune\": {\n      \"type\": \"stdio\",\n      \"command\": \"rune\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}\n"
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".mcp.json": mcp})
	// A run that stops on a conflict starts no program: the client is not
	// asked for a version that would be thrown away.
	never := func(context.Context, string) string {
		t.Error("a run that stopped on a conflict asked the client for its version")
		return ""
	}
	for _, flags := range []AgentInitFlags{{version: never}, {DryRun: true, version: never}} {
		stdout, stderr, err := runAgentInit(t, dir, flags, "claude")
		wantErr[*UsageError](t, err, "agent init: 1 conflict(s), nothing written")
		for _, want := range []string{".mcp.json:6:15", `mcpServers.rune.args is ["mcp"]; rune agent init writes ["mcp", "--file", `, "run again with --force", `"args": ["mcp"]`, "^^^^^^^"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
		// No outcome is named for a setup that was not written.
		for _, never := range []string{"outcome:", "client:", "created", "step:"} {
			if strings.Contains(stderr, never) {
				t.Errorf("stderr has %q:\n%s", never, stderr)
			}
		}
		if stdout != "" || readFile(t, filepath.Join(dir, ".mcp.json")) != mcp || dirNames(t, dir) != ".mcp.json Runefile" {
			t.Errorf("%+v: something was written; stdout %q, files %q", flags, stdout, dirNames(t, dir))
		}
	}

	if err := os.Chmod(filepath.Join(dir, ".mcp.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{Force: true}, "claude")
	if err != nil || !strings.Contains(stderr, ".mcp.json: updated: replaced args of server \"rune\"\n") {
		t.Fatalf("--force: %v\n%s", err, stderr)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".mcp.json")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("an updated file has mode %v, want the 0600 it had", info.Mode().Perm())
	}
}

func TestAgentInitFilesThatCannotBePlanned(t *testing.T) {
	never := func(context.Context, string) string {
		t.Error("a run that could not plan asked the client for its version")
		return ""
	}
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".mcp.json": "{\n  \"mcpServers\": {,}\n}\n"})
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{version: never}, "claude")
	wantErr[*ValidationError](t, err, ".mcp.json is not valid")
	if !strings.Contains(stderr, ".mcp.json:2:18") || !strings.Contains(stderr, "expected a key in quotation marks") {
		t.Errorf("stderr:\n%s", stderr)
	}

	const toml = "model = \"x\"\nmcp_servers = { rune = { command = \"rune\" } }\n"
	dir = importProject(t, map[string]string{"Runefile": agentRunefile, ".codex/config.toml": toml})
	for _, flags := range []AgentInitFlags{{}, {Check: true}} {
		_, stderr, err = runAgentInit(t, dir, flags, "codex")
		wantErr[*UsageError](t, err, "agent init: .codex/config.toml needs a setting added by hand, nothing written")
		for _, want := range []string{".codex/config.toml:2:1", "rune agent init cannot add to this file in place. Set this yourself: mcp_servers.rune.args = [\"mcp\", "} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
	}
	if got := readFile(t, filepath.Join(dir, ".codex", "config.toml")); got != toml {
		t.Errorf("config.toml changed: %q", got)
	}
}

// The outcome follows the version, and the files do not.
func TestAgentInitOutcomeFollowsTheVersion(t *testing.T) {
	files := func(version string) (string, string, error) {
		dir := importProject(t, map[string]string{"Runefile": agentRunefile})
		_, stderr, err := runAgentInit(t, dir, AgentInitFlags{Unpinned: true, version: func(context.Context, string) string { return version }}, "claude")
		return readFile(t, filepath.Join(dir, ".mcp.json")) + readFile(t, filepath.Join(dir, ".claude", "settings.json")), stderr, err
	}
	checked, stderr, err := files("2.1.325")
	if err != nil || !strings.Contains(stderr, "outcome: Rune-only, pending 2 steps\n") {
		t.Fatalf("checked version: %v\n%s", err, stderr)
	}
	for _, version := range []string{"9.9.9", ""} {
		got, stderr, err := files(version)
		wantErr[*CommandFailure](t, err, `agent init: the outcome is "Registered only"`)
		if !strings.Contains(stderr, "outcome: Registered only, pending 2 steps\n") || strings.Contains(stderr, "Rune-only") {
			t.Errorf("version %q:\n%s", version, stderr)
		}
		if got != checked {
			t.Errorf("version %q wrote other files than a checked version", version)
		}
	}
}

func TestRunClientVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in clients are shell scripts")
	}
	bin := t.TempDir()
	for name, script := range map[string]string{
		"good":   "echo '2.1.326 (Claude Code)'",
		"fails":  "echo 2.1.326; exit 3",
		"silent": "echo no version here",
		"slow":   "exec sleep 30",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	tests := []struct {
		binary string
		limit  time.Duration
		want   string
	}{
		{"good", time.Minute, "2.1.326"},
		{"fails", time.Minute, ""},
		{"silent", time.Minute, ""},
		{"missing", time.Minute, ""},
		{"slow", 50 * time.Millisecond, ""},
	}
	for _, tt := range tests {
		start := time.Now()
		if got := runClientVersion(context.Background(), tt.binary, tt.limit); got != tt.want {
			t.Errorf("runClientVersion(%s) = %q, want %q", tt.binary, got, tt.want)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("runClientVersion(%s) took %v", tt.binary, elapsed)
		}
	}

	// With no stub, the flags look the client up on PATH, with their limit.
	if got := (AgentInitFlags{}).clientVersion(context.Background(), "good"); got != "2.1.326" {
		t.Errorf("clientVersion = %q, want 2.1.326", got)
	}
	if got := (AgentInitFlags{versionLimit: 50 * time.Millisecond}).clientVersion(context.Background(), "slow"); got != "" {
		t.Errorf("clientVersion of a slow client = %q, want nothing", got)
	}
}

func TestAgentInitDryRun(t *testing.T) {
	const settings = "{\n  \"permissions\": {\n    \"deny\": [\"WebFetch\"]\n  }\n}\n"
	dir := importProject(t, map[string]string{"Runefile": agentRunefile, ".claude/settings.json": settings})
	stdout, stderr, err := runAgentInit(t, dir, AgentInitFlags{DryRun: true}, "claude")
	if err != nil {
		t.Fatalf("AgentInit: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"--- /dev/null\n+++ b/.mcp.json\n@@ -0,0 +1,9 @@\n",
		"--- a/.claude/settings.json\n+++ b/.claude/settings.json\n",
		"-    \"deny\": [\"WebFetch\"]\n+    \"deny\": [\"WebFetch\", \"Bash\", ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.HasSuffix(stderr, "dry run: nothing written\n") || !strings.Contains(stderr, ".mcp.json: created: ") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if got := dirNames(t, dir); got != ".claude Runefile" || readFile(t, filepath.Join(dir, ".claude", "settings.json")) != settings {
		t.Errorf("a dry run wrote something: %q", got)
	}

	// The exit status is the real run's.
	_, _, err = runAgentInit(t, dir, AgentInitFlags{DryRun: true, version: func(context.Context, string) string { return "" }}, "claude")
	wantErr[*CommandFailure](t, err, `agent init: the outcome is "Registered only"`)
}

func TestAgentInitCheck(t *testing.T) {
	never := func(context.Context, string) string {
		t.Error("--check asked the client for its version")
		return ""
	}
	check := AgentInitFlags{Check: true, version: never}
	dir := importProject(t, map[string]string{"Runefile": agentRunefile})

	stdout, stderr, err := runAgentInit(t, dir, check, "claude")
	wantErr[*CommandFailure](t, err, "agent init: the setup is not complete")
	if !strings.Contains(stderr, ".mcp.json: mcpServers.rune.args: missing\n") ||
		!strings.Contains(stderr, "incomplete: 10 of 10 entries are missing or different. Run: rune agent init claude\n") {
		t.Errorf("never set up:\n%s", stderr)
	}
	if stdout != "" || dirNames(t, dir) != "Runefile" {
		t.Errorf("a check wrote something: stdout %q, files %q", stdout, dirNames(t, dir))
	}

	if _, stderr, err = runAgentInit(t, dir, AgentInitFlags{}, "claude"); err != nil {
		t.Fatalf("setup: %v\n%s", err, stderr)
	}
	_, stderr, err = runAgentInit(t, dir, check, "claude")
	if err != nil || !strings.Contains(stderr, "complete: all 10 entries are present\ncheck: claude -p hi") || strings.Contains(stderr, "outcome:") {
		t.Errorf("complete setup: %v\n%s", err, stderr)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	settings := readFile(t, settingsPath)
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(settingsPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(strings.Replace(settings, `"Monitor", `, "", 1))
	_, stderr, err = runAgentInit(t, dir, check, "claude")
	wantErr[*CommandFailure](t, err, "agent init: the setup is not complete")
	if !strings.Contains(stderr, ".claude/settings.json: permissions.deny: Monitor: missing\n") ||
		!strings.Contains(stderr, "incomplete: 1 of 10 entries are missing or different.") {
		t.Errorf("one rule removed:\n%s", stderr)
	}

	// Every entry is there, and a deny rule makes the setup impossible: the
	// rule is shown as in a real run.
	write(strings.Replace(settings, `"Bash"`, `"mcp__rune", "Bash"`, 1))
	_, stderr, err = runAgentInit(t, dir, check, "claude")
	wantErr[*CommandFailure](t, err, "agent init: the setup is not complete")
	if !strings.Contains(stderr, ".claude/settings.json:3:14") || !strings.Contains(stderr, "which denies every Rune tool") ||
		!strings.Contains(stderr, "incomplete: the setup has a conflict\n") {
		t.Errorf("deny rule for every tool:\n%s", stderr)
	}
	write(settings)

	// Another value at an owned key is listed as different, not shown as a
	// conflict.
	mcpPath := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(strings.Replace(readFile(t, mcpPath), `"--no-reload"`, `"--deny", "x"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = runAgentInit(t, dir, check, "claude")
	wantErr[*CommandFailure](t, err, "agent init: the setup is not complete")
	if !strings.Contains(stderr, ".mcp.json: mcpServers.rune.args: differs\n") || strings.Contains(stderr, "error") {
		t.Errorf("another value:\n%s", stderr)
	}
}

func TestAgentInitUnpinned(t *testing.T) {
	dir := importProject(t, map[string]string{"Runefile": agentRunefile})
	_, stderr, err := runAgentInit(t, dir, AgentInitFlags{Unpinned: true}, "claude")
	if err != nil {
		t.Fatalf("AgentInit: %v\n%s", err, stderr)
	}
	if got := readFile(t, filepath.Join(dir, ".mcp.json")); !strings.Contains(got, `"args": ["mcp", "--no-reload"]`) {
		t.Errorf(".mcp.json holds a path:\n%s", got)
	}
	if !strings.Contains(stderr, "note: the server serves the Runefile found from the directory the client starts it in.\n") ||
		strings.Contains(stderr, "this checkout's path") {
		t.Errorf("stderr:\n%s", stderr)
	}

	// A run with the path after a run without it is a conflict, as for any
	// other value.
	_, _, err = runAgentInit(t, dir, AgentInitFlags{}, "claude")
	wantErr[*UsageError](t, err, "agent init: 1 conflict(s), nothing written")

	other := importProject(t, map[string]string{"tasks.rune": agentRunefile})
	var errb bytes.Buffer
	opts := Options{Cwd: other, File: filepath.Join(other, "tasks.rune"), Stdout: &errb, Stderr: &errb}
	err = AgentInit(opts, AgentInitFlags{Unpinned: true, version: checkedVersion}, "claude")
	wantErr[*UsageError](t, err, "--unpinned needs a Runefile that rune finds by its name; tasks.rune is not one")
	if got := dirNames(t, other); got != "tasks.rune" {
		t.Errorf("files were written: %q", got)
	}
}

func TestInsideProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(dir, "a", "b", "c.json"), filepath.Join(dir, "x.json"), filepath.Join(dir, "a")} {
		if err := insideProject(dir, target, agentInitScope); err != nil {
			t.Errorf("insideProject(%s) = %v", target, err)
		}
	}
	if err := insideProject(filepath.Join(dir, "missing"), filepath.Join(dir, "missing", "x"), agentInitScope); err == nil {
		t.Error("a project directory that does not exist is accepted")
	}
}

func TestMakeDirsAndWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b")
	made, err := makeDirs(deep)
	if err != nil || len(made) != 2 || made[0] != filepath.Join(dir, "a") || made[1] != deep {
		t.Errorf("makeDirs = %v, %v", made, err)
	}
	if made, err := makeDirs(deep); err != nil || len(made) != 0 {
		t.Errorf("makeDirs of an existing directory = %v, %v", made, err)
	}

	path := filepath.Join(deep, "f.json")
	for _, text := range []string{"one\n", "two\n"} {
		if err := writeFileAtomic(path, []byte(text)); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path); got != text {
			t.Errorf("file = %q, want %q", got, text)
		}
	}
	if got := dirNames(t, deep); got != "f.json" {
		t.Errorf("a temporary file was left behind: %q", got)
	}
	if err := writeFileAtomic(filepath.Join(dir, "missing", "f.json"), nil); err == nil {
		t.Error("a write into a missing directory succeeds")
	}
	// A path that cannot be renamed onto leaves no temporary file.
	if err := writeFileAtomic(deep, []byte("x")); err == nil {
		t.Error("a write over a non-empty directory succeeds")
	}
	if got := dirNames(t, filepath.Join(dir, "a")); got != "b" {
		t.Errorf("a temporary file was left behind: %q", got)
	}
}

// The planner's own errors are failures of the command, not usage errors.
func TestAgentPlanErrorOther(t *testing.T) {
	var errb bytes.Buffer
	err := agentPlanError(Options{Stderr: &errb}, nil, errors.New("did not verify"))
	wantErr[*CommandFailure](t, err, "did not verify")
	if errb.Len() != 0 {
		t.Errorf("stderr = %q", errb.String())
	}
	renderConflicts(Options{Stderr: &errb}, nil, nil)
	if errb.Len() != 0 {
		t.Errorf("no conflicts printed %q", errb.String())
	}
}

func TestErrorDiags(t *testing.T) {
	first := diag.New(token.Span{File: "a"}, "first")
	second := diag.New(token.Span{File: "b"}, "second")
	warning := diag.Diagnostic{Severity: diag.Warning, Message: "only a warning"}
	got := errorDiags(diag.List{first, warning, second})
	if len(got) != 2 || got[0].Message != "first" || got[1].Message != "second" {
		t.Errorf("errorDiags = %+v, want the two errors in their order", got)
	}
	if got := errorDiags(diag.List{warning}); got != nil {
		t.Errorf("errorDiags of warnings = %+v, want none", got)
	}
}

// writePlanned removes a file marked for removal, and a write that fails
// later puts it back with its bytes (spec 056).
func TestWritePlannedRemovesAndPutsBack(t *testing.T) {
	dir := t.TempDir()
	gone, kept := filepath.Join(dir, "gone.md"), filepath.Join(dir, "sub", "new.md")
	if err := os.WriteFile(gone, []byte("old text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []plannedFile{
		{target: kept, shown: "sub/new.md", new: []byte("new text\n")},
		{target: gone, shown: "gone.md", old: []byte("old text\n"), existed: true, remove: true},
	}
	if err := writePlanned(files, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file marked for removal is still there: %v", err)
	}
	if got := readFile(t, kept); got != "new text\n" {
		t.Errorf("the new file holds %q", got)
	}

	// The removal comes first now, and the write after it fails.
	if err := os.WriteFile(gone, []byte("old text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(files)
	err := writePlanned(files, func(string, []byte) error { return errors.New("disk is full") })
	wantErr[*CommandFailure](t, err, "sub/new.md: disk is full")
	if got := readFile(t, gone); got != "old text\n" {
		t.Errorf("the removed file was put back as %q", got)
	}
	if got := dirNames(t, dir); got != "gone.md" {
		t.Errorf("after the failed run the directory holds %q", got)
	}

	// A file that cannot be removed fails the run with its name.
	err = writePlanned([]plannedFile{{target: filepath.Join(dir, "absent.md"), shown: "absent.md", remove: true}}, nil)
	var failure *CommandFailure
	if !errors.As(err, &failure) || !strings.HasPrefix(err.Error(), "absent.md: ") {
		t.Errorf("err = %v, want a failure that names absent.md", err)
	}
}

// A file marked for removal that is a symbolic link loses the link and keeps
// the file behind it, so no link without a file is left for the next run. A
// write that fails later puts the link back (spec 056).
func TestWritePlannedRemovesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege on Windows")
	}
	dir := t.TempDir()
	behind, link := filepath.Join(dir, "behind.md"), filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(behind, []byte("old text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("behind.md", link); err != nil {
		t.Fatal(err)
	}
	remove := plannedFile{target: link, shown: "tasks.md", old: []byte("old text\n"), existed: true, remove: true}
	if err := writePlanned([]plannedFile{remove}, nil); err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, dir); got != "behind.md" {
		t.Errorf("after the removal the directory holds %q, want the file behind the link alone", got)
	}
	if got := readFile(t, behind); got != "old text\n" {
		t.Errorf("the file behind the link holds %q", got)
	}

	if err := os.Symlink("behind.md", link); err != nil {
		t.Fatal(err)
	}
	later := plannedFile{target: filepath.Join(dir, "new.md"), shown: "new.md", new: []byte("new text\n")}
	err := writePlanned([]plannedFile{remove, later}, func(string, []byte) error { return errors.New("disk is full") })
	wantErr[*CommandFailure](t, err, "new.md: disk is full")
	if dest, err := os.Readlink(link); err != nil || dest != "behind.md" {
		t.Errorf("the link was put back as %q, %v", dest, err)
	}
	if got := dirNames(t, dir); got != "behind.md tasks.md" {
		t.Errorf("after the failed run the directory holds %q", got)
	}
}

// A link whose file does not exist yet is written through like any other:
// the file appears where the link leads and the link stays a link. A write
// that fails later takes the file away again and leaves the link.
func TestWritePlannedWritesThroughALinkWithoutAFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege on Windows")
	}
	// The directory as the system resolves it, which is how a path that was
	// read through a link comes back.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join("docs", "agents.md")
	link, behind := filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, dest)
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}
	if got := writeTarget(link); got != behind {
		t.Errorf("writeTarget = %s, want %s", got, behind)
	}
	if got, plain := writeTarget(filepath.Join(dir, "new.md")), filepath.Join(dir, "new.md"); got != plain {
		t.Errorf("writeTarget of a path that is no link = %s, want %s", got, plain)
	}
	isLink := func() {
		t.Helper()
		if got, err := os.Readlink(link); err != nil || got != dest {
			t.Errorf("the link now leads to %q, %v", got, err)
		}
	}

	through := plannedFile{target: link, shown: "AGENTS.md", new: []byte("new text\n")}
	if err := writePlanned([]plannedFile{through}, nil); err != nil {
		t.Fatal(err)
	}
	isLink()
	if got := readFile(t, behind); got != "new text\n" {
		t.Errorf("the file behind the link holds %q", got)
	}

	if err := os.RemoveAll(filepath.Join(dir, "docs")); err != nil {
		t.Fatal(err)
	}
	later := plannedFile{target: filepath.Join(dir, "new.md"), shown: "new.md", new: []byte("new text\n")}
	err = writePlanned([]plannedFile{through, later}, func(path string, data []byte) error {
		if filepath.Base(path) == "new.md" {
			return errors.New("disk is full")
		}
		return writeFileAtomic(path, data)
	})
	wantErr[*CommandFailure](t, err, "new.md: disk is full")
	isLink()
	if got := dirNames(t, dir); got != "AGENTS.md" {
		t.Errorf("after the failed run the directory holds %q", got)
	}
}

// A removed file that a failed run puts back gets its permissions back too:
// the file is gone by then, so the write cannot copy them from it.
func TestWritePlannedPutsBackTheModeOfARemovedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to lose")
	}
	dir := t.TempDir()
	private := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(private, []byte("old text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []plannedFile{
		{target: private, shown: "tasks.md", old: []byte("old text\n"), existed: true, remove: true},
		{target: filepath.Join(dir, "new.md"), shown: "new.md", new: []byte("new text\n")},
	}
	err := writePlanned(files, func(string, []byte) error { return errors.New("disk is full") })
	wantErr[*CommandFailure](t, err, "new.md: disk is full")
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the removed file came back as %v, %v, want mode 0600", info.Mode().Perm(), err)
	}
	if got := readFile(t, private); got != "old text\n" {
		t.Errorf("the removed file came back holding %q", got)
	}
}

// The message of insideProject ends with the sentence its caller gives:
// which command it is, and what it writes.
func TestInsideProjectNamesItsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege on Windows")
	}
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "out", "AGENTS.md")
	for _, scope := range []string{agentInitScope, exportScope} {
		err := insideProject(dir, target, scope)
		if err == nil || !strings.HasPrefix(err.Error(), "out leads outside the project, to ") || !strings.HasSuffix(err.Error(), ". "+scope) {
			t.Errorf("insideProject with %q = %v", scope, err)
		}
	}
}
