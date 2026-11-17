package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Spec 048: `rune serve` and `rune mcp` take -f/--file, so a client that
// starts the server outside the project can name the Runefile. These tests
// start the real binary in one directory and name a Runefile in another.
//
// A task shows where it ran by the file it leaves there. No test compares
// path text, so the tests run the same on Windows.

const fileFlagRunefile = "# Leaves a file where it runs.\nmark:\n    @echo x > ran\n    @echo marked\n"

// fileFlagContext is a [context] task that leaves a file where it runs. No
// usage error may run it.
const fileFlagContext = "\n# Health.\n[context]\nhealth:\n    @echo x > context-ran\n    @echo from-the-project\n"

const fileFlagProtocol = "2024-11-05"

// fileFlagDirs returns a project directory whose Runefile holds content, the
// path of that Runefile, and an empty directory to start the server in.
func fileFlagDirs(t *testing.T, content string) (project, runefile, elsewhere string) {
	t.Helper()
	project = writeRunefile(t, content)
	return project, filepath.Join(project, "Runefile"), t.TempDir()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The acceptance test of issue #44, which names it
// TestMCPE2E_Pending_ServeAcceptsFileFlag: a server started in one directory
// with the path of a Runefile in another serves that Runefile, and a task
// runs in the Runefile's directory (FR-002, FR-006, FR-023).
func TestFileFlagMCP_ServesNamedRunefile(t *testing.T) {
	project, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	p := startMCP(t, elsewhere, "-f", runefile)
	p.wantTools("mark")
	if got := p.call("mark"); got != "marked\n[exit 0]" {
		t.Errorf("call mark = %q", got)
	}
	if !exists(t, project, "ran") {
		t.Error("mark did not run in the Runefile's directory")
	}
	if exists(t, elsewhere, "ran") {
		t.Error("mark ran in the directory the server was started in")
	}
}

// FR-001, FR-003, FR-004: both names of the flag, after and before the
// command name, give the same server.
func TestFileFlagMCP_Forms(t *testing.T) {
	_, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	other := filepath.Join(writeRunefile(t, reloadRunefile), "Runefile")
	forms := map[string][]string{
		"short after mcp":   {"mcp", "-f", runefile},
		"long with equals":  {"mcp", "--file=" + runefile},
		"long after serve":  {"serve", "--file", runefile},
		"short before mcp":  {"-f", runefile, "mcp"},
		"long before serve": {"--file", runefile, "serve"},
		"given twice":       {"-f", other, "mcp", "-f", runefile},
		"among other flags": {"mcp", "--no-reload", "-f", runefile, "--allow", "mark"},
		"before, with more": {"-f", runefile, "mcp", "--allow", "mark"},
	}
	for name, args := range forms {
		t.Run(name, func(t *testing.T) {
			startMCPArgs(t, elsewhere, fileFlagProtocol, args...).wantTools("mark")
		})
	}

	t.Run("relative path", func(t *testing.T) {
		parent := t.TempDir()
		writeFile(t, filepath.Join(parent, "proj", "Runefile"), fileFlagRunefile)
		start := filepath.Join(parent, "else")
		if err := os.Mkdir(start, 0o755); err != nil {
			t.Fatal(err)
		}
		p := startMCP(t, start, "-f", filepath.Join("..", "proj", "Runefile"))
		p.wantTools("mark")
		if p.call("mark"); !exists(t, filepath.Join(parent, "proj"), "ran") || exists(t, start, "ran") {
			t.Error("mark did not run in the directory of the Runefile a relative path names")
		}
	})

	t.Run("directory with a space", func(t *testing.T) {
		project := filepath.Join(t.TempDir(), "my project")
		writeFile(t, filepath.Join(project, "Runefile"), fileFlagRunefile)
		p := startMCP(t, elsewhere, "-f", filepath.Join(project, "Runefile"))
		p.wantTools("mark")
		if p.call("mark"); !exists(t, project, "ran") {
			t.Error("mark did not run in a directory with a space in its name")
		}
	})

	t.Run("file not named Runefile", func(t *testing.T) {
		project := t.TempDir()
		writeFile(t, filepath.Join(project, "tasks.rune"), fileFlagRunefile)
		p := startMCP(t, elsewhere, "-f", filepath.Join(project, "tasks.rune"))
		p.wantTools("mark")
		if p.call("mark"); !exists(t, project, "ran") {
			t.Error("mark did not run beside a Runefile with another name")
		}
	})

	// What was there before: a task named like the command is still reached
	// with --, and starts no server.
	t.Run("task named mcp", func(t *testing.T) {
		project := writeRunefile(t, "# Not the server.\nmcp:\n    @echo task-ran\n")
		r := run(t, elsewhere, nil, "-f", filepath.Join(project, "Runefile"), "--", "mcp")
		if r.code != 0 || r.stdout != "task-ran\n" {
			t.Errorf("rune -f PATH -- mcp: exit %d, stdout %q, stderr %q; want the task's output", r.code, r.stdout, r.stderr)
		}
	})
}

// FR-007: imports, modules and the `set dotenv` file are found from the named
// Runefile, not from the starting directory.
func TestFileFlagMCP_ImportsModulesAndDotenv(t *testing.T) {
	project, runefile, elsewhere := fileFlagDirs(t,
		"set dotenv := \".env\"\nimport \"shared.rune\"\nmod tools \"tools.rune\"\n\n# Prints a value of .env.\ngreet:\n    @echo \"greeting=${GREETING:-unset}\"\n")
	writeFile(t, filepath.Join(project, "shared.rune"), "# Shared.\nshared:\n    @echo shared\n")
	writeFile(t, filepath.Join(project, "tools.rune"), "# Lints.\nlint:\n    @echo linted\n")
	writeFile(t, filepath.Join(project, ".env"), "GREETING=from-the-project\n")
	writeFile(t, filepath.Join(elsewhere, ".env"), "GREETING=from-the-starting-directory\n")

	p := startMCP(t, elsewhere, "-f", runefile)
	p.wantTools("greet", "shared", "tools__lint")
	if got := p.call("greet"); got != "greeting=from-the-project\n[exit 0]" {
		t.Errorf("call greet = %q, want the value of the .env beside the Runefile", got)
	}
	if got := p.call("tools__lint"); got != "linted\n[exit 0]" {
		t.Errorf("call tools__lint = %q", got)
	}
}

// FR-008: the [context] task of the named Runefile runs in its directory, and
// its output is the server's instructions.
func TestFileFlagMCP_ContextRunsInRunefileDirectory(t *testing.T) {
	project, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile+fileFlagContext)
	p := startMCP(t, elsewhere, "-f", runefile)
	var init struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(p.init, &init); err != nil {
		t.Fatal(err)
	}
	if init.Instructions != "from-the-project" {
		t.Errorf("instructions = %q, want the [context] output of the named Runefile", init.Instructions)
	}
	if !exists(t, project, "context-ran") || exists(t, elsewhere, "context-ran") {
		t.Error("the [context] task did not run in the Runefile's directory")
	}
}

// FR-009: reloading follows the Runefile the flag names.
func TestFileFlagMCP_ReloadFollowsNamedRunefile(t *testing.T) {
	_, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	p := startMCP(t, elsewhere, "-f", runefile)
	p.wantTools("mark")

	writeFile(t, runefile, fileFlagRunefile+reloadLint)
	p.waitNotification("notifications/tools/list_changed", reloadTimeout)
	p.wantTools("lint", "mark")
	if got := p.call("lint"); got != "linted\n[exit 0]" {
		t.Errorf("call lint = %q", got)
	}
}

func TestFileFlagMCP_NoReload(t *testing.T) {
	_, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	p := startMCP(t, elsewhere, "-f", runefile, "--no-reload")
	writeFile(t, runefile, fileFlagRunefile+reloadLint)
	// Longer than the debounce and a load take together.
	time.Sleep(time.Second)
	if p.notified("notifications/tools/list_changed") {
		t.Error("--no-reload: the client was notified")
	}
	p.wantTools("mark")
}

// FR-002: a Runefile in the starting directory is not served when the flag
// names another.
func TestFileFlagMCP_NamedWinsOverLocal(t *testing.T) {
	_, runefile, _ := fileFlagDirs(t, fileFlagRunefile)
	local := writeRunefile(t, reloadRunefile)
	startMCP(t, local, "-f", runefile).wantTools("mark")
}

// FR-005: without the flag the server finds its Runefile as before, from a
// subdirectory too, and prints what it printed before.
func TestFileFlagMCP_WithoutFlagUnchanged(t *testing.T) {
	local := writeRunefile(t, reloadRunefile)
	sub := filepath.Join(local, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{local, sub} {
		p := startMCP(t, dir)
		p.wantTools("build")
		// The stderr pipe is copied by its own goroutine, which can trail the
		// handshake on stdout.
		if got := waitForText(t, p.stderr, "rune MCP server on stdio\n", 1); got != "rune MCP server on stdio\n" {
			t.Errorf("stderr = %q, want only the startup line", got)
		}
	}
}

// The flag works the same on the HTTP transport.
func TestFileFlagMCP_HTTP(t *testing.T) {
	_, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	addr := net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, runeBin, "serve", "--http", "--addr", addr, "--token-file", writeToken(t, elsewhere), "-f", runefile)
	cmd.Dir = elsewhere
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	})
	waitForText(t, stderr, "rune MCP server on http://", 1)

	cs := connectHTTP(t, ctx, addr, stderr)
	defer func() { _ = cs.Close() }()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, []string{"mark"}) {
		t.Errorf("tools over HTTP = %v, want [mark]", names)
	}
}

// A symbolic link is served as the path it is given by: tasks run in the
// link's directory, as they do for `rune -f`.
func TestFileFlagMCP_SymlinkedRunefile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows")
	}
	project, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile)
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "Runefile")
	if err := os.Symlink(runefile, link); err != nil {
		t.Fatal(err)
	}
	p := startMCP(t, elsewhere, "-f", link)
	p.wantTools("mark")
	if p.call("mark"); !exists(t, linkDir, "ran") || exists(t, project, "ran") || exists(t, elsewhere, "ran") {
		t.Error("mark did not run in the directory of the link")
	}
}

// FR-010, FR-011, FR-017: the server gained the file flag and no other. --yes
// is unknown to it in every position, with and without -f, and nothing runs.
func TestFileFlagMCP_YesStaysUnknown(t *testing.T) {
	project, runefile, elsewhere := fileFlagDirs(t, fileFlagRunefile+fileFlagContext)
	cases := []struct {
		flag string
		args []string
	}{
		{"--yes", []string{"mcp", "--yes"}},
		{"--yes", []string{"serve", "--yes"}},
		{"--yes", []string{"--yes", "mcp"}},
		{"--yes", []string{"mcp", "-f", runefile, "--yes"}},
		{"--yes", []string{"mcp", "--yes", "-f", runefile}},
		{"--yes", []string{"-f", runefile, "--yes", "mcp"}},
		{"--yes", []string{"-f", runefile, "mcp", "--yes"}},
		{"--yes", []string{"--yes", "-f", runefile, "serve"}},
		{"--quiet", []string{"--quiet", "mcp", "-f", runefile}},
		{"--dry-run", []string{"mcp", "-f", runefile, "--dry-run"}},
		{"--set", []string{"mcp", "-f", runefile, "--set=A"}},
		{"--watch", []string{"-f", runefile, "--watch", "mcp"}},
	}
	for _, c := range cases {
		// In the project too, where a server that started would find the
		// Runefile by itself.
		for _, dir := range []string{elsewhere, project} {
			r := runWithStdin(t, dir, "", c.args...)
			if want := "rune: unknown flag: " + c.flag + "\n"; r.code != 2 || r.stderr != want || r.stdout != "" {
				t.Errorf("rune %s: exit %d, stderr %q, stdout %q; want exit 2 and %q",
					strings.Join(c.args, " "), r.code, r.stderr, r.stdout, want)
			}
		}
	}
	if exists(t, project, "context-ran") {
		t.Error("a usage error ran the [context] task")
	}
}

// FR-012: a [confirm] task of a Runefile named with the flag needs
// --allow-destructive by name, as before.
func TestFileFlagMCP_ConfirmStillNeedsOptIn(t *testing.T) {
	const runefile = "# Wipes.\n[confirm(\"Sure?\")]\nwipe:\n    @echo x > wiped\n"
	project, path, elsewhere := fileFlagDirs(t, runefile)

	text, isError, _ := toolCall(t, elsewhere, []string{"-f", path}, "wipe")
	// The command names the Runefile: a bare `rune wipe` where the server was
	// started would find none.
	want := refusedText("wipe") + cannotAskWith("rune -f "+path+" wipe")
	// The server quotes a path a shell would split, which a temporary
	// directory under a user name with a space is.
	quoted := refusedText("wipe") + cannotAskWith("rune -f '"+path+"' wipe")
	if !isError || (text != want && text != quoted) {
		t.Errorf("wipe without a grant: isError=%v text=%q, want %q", isError, text, want)
	}
	if exists(t, project, "wiped") {
		t.Fatal("an ungranted [confirm] task ran")
	}

	text, isError, _ = toolCall(t, elsewhere, []string{"-f", path, "--allow-destructive", "wipe"}, "wipe")
	if isError || !exists(t, project, "wiped") {
		t.Errorf("wipe with --allow-destructive wipe: isError=%v text=%q, want it to run", isError, text)
	}
}

// FR-014 to FR-017: a mistake in the value stops the server. It never falls
// back to the Runefile of the starting directory, and runs nothing.
func TestFileFlagMCP_Mistakes(t *testing.T) {
	local := writeRunefile(t, reloadRunefile+fileFlagContext)
	missing := filepath.Join(t.TempDir(), "nope", "Runefile")
	const needsPath = "rune: --file needs a path\n"
	cases := []struct {
		name string
		args []string
		want func(stderr string) bool
	}{
		{"empty value", []string{"mcp", "-f", ""}, func(s string) bool { return s == needsPath }},
		{"empty long value", []string{"mcp", "--file="}, func(s string) bool { return s == needsPath }},
		{"empty value before the command", []string{"-f", "", "mcp"}, func(s string) bool { return s == needsPath }},
		{"empty value with serve", []string{"serve", "--file", ""}, func(s string) bool { return s == needsPath }},
		{"no such file", []string{"mcp", "-f", missing}, func(s string) bool {
			return strings.HasPrefix(s, "rune: ") && strings.Contains(s, missing) && strings.Count(s, "\n") == 1
		}},
		{"a directory", []string{"mcp", "-f", t.TempDir()}, func(s string) bool {
			return s == "rune: --file points at a directory, not a Runefile\n"
		}},
		{"no value", []string{"mcp", "-f"}, func(s string) bool {
			return s == "rune: flag needs an argument: 'f' in -f\n"
		}},
	}
	for _, c := range cases {
		r := runWithStdin(t, local, "", c.args...)
		if r.code != 2 || !c.want(r.stderr) || r.stdout != "" {
			t.Errorf("%s: exit %d, stderr %q, stdout %q; want exit 2 and its own message", c.name, r.code, r.stderr, r.stdout)
		}
	}
	if exists(t, local, "context-ran") {
		t.Error("a mistake in --file served the Runefile of the starting directory")
	}
}

// FR-018: a named Runefile with a static error is reported as it is when
// served from its own directory.
func TestFileFlagMCP_StaticErrorSameAsLocal(t *testing.T) {
	project, runefile, elsewhere := fileFlagDirs(t, "build: missing\n    @echo x\n")
	inPlace := runWithStdin(t, project, "", "mcp")
	named := runWithStdin(t, elsewhere, "", "mcp", "-f", runefile)
	if inPlace.code != 3 || !strings.Contains(inPlace.stderr, "unknown task: missing") {
		t.Fatalf("served in place: exit %d, stderr %q; want the static error", inPlace.code, inPlace.stderr)
	}
	if named.code != inPlace.code || named.stderr != inPlace.stderr || named.stdout != "" {
		t.Errorf("named with -f: exit %d, stderr %q, stdout %q\nserved in place: exit %d, stderr %q",
			named.code, named.stderr, named.stdout, inPlace.code, inPlace.stderr)
	}
}

// The task names --allow accepts are those of the named Runefile.
func TestFileFlagMCP_UnknownTaskNamesTheNamedRunefile(t *testing.T) {
	_, runefile, _ := fileFlagDirs(t, fileFlagRunefile)
	local := writeRunefile(t, reloadRunefile)
	r := runWithStdin(t, local, "", "mcp", "-f", runefile, "--allow", "marc")
	if want := "rune: unknown task in --allow: marc (did you mean \"mark\"?)\n"; r.code != 2 || r.stderr != want {
		t.Errorf("exit %d, stderr %q; want exit 2 and %q", r.code, r.stderr, want)
	}
	// build is a task of the starting directory's Runefile only.
	r = runWithStdin(t, local, "", "mcp", "-f", runefile, "--allow", "build")
	if r.code != 2 || !strings.HasPrefix(r.stderr, "rune: unknown task in --allow: build") {
		t.Errorf("exit %d, stderr %q; want build to be unknown to the named Runefile", r.code, r.stderr)
	}
}
