package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// allowlistRunefile is the spec 041 quickstart scratch Runefile: every body
// writes a marker file, so "nothing ran" is checkable from the outside. Seven
// tasks are exposable: build, test, deploy, clean, release, lint, report.
const allowlistRunefile = `# Builds the artifacts.
build:
    @echo built > build.done

# Runs the tests.
test: build _setup
    @echo tested > test.done

# Ships the build.
deploy:
    @echo deployed > deploy.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Cuts a release.
release: clean
    @echo released > release.done

# Lints, then reports.
lint: && report
    @echo linted > lint.done

# Writes the report.
report:
    @echo reported > report.done

_setup:
    @echo setup > setup.done

# Project health for agents.
[context]
health:
    @echo context > context.done
`

// toolEntries lists the tools of the Runefile in dir, on a server started
// with flags, and returns each tool's raw JSON by name.
func toolEntries(t *testing.T, dir string, flags ...string) map[string]string {
	t.Helper()
	reply := mcpToolsList(t, dir, flags...)
	var frame struct {
		Result *struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(reply), &frame); err != nil {
		t.Fatalf("decoding tools/list reply: %v\nreply: %s", err, reply)
	}
	// An error reply has no result. It must not read as an empty tool list.
	if frame.Result == nil {
		t.Fatalf("tools/list returned no result: %s", reply)
	}
	entries := make(map[string]string, len(frame.Result.Tools))
	for _, raw := range frame.Result.Tools {
		var tool struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatalf("decoding tool %s: %v", raw, err)
		}
		entries[tool.Name] = string(raw)
	}
	return entries
}

// toolNames is the sorted tool names of toolEntries.
func toolNames(t *testing.T, dir string, flags ...string) []string {
	t.Helper()
	names := []string{}
	for name := range toolEntries(t, dir, flags...) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// toolError calls task on a server started with flags and returns the
// JSON-RPC error of the reply. A reply that carries a result fails the test:
// the call was expected to be rejected before any handler ran.
func toolError(t *testing.T, dir string, flags []string, task string) (code int, message string) {
	t.Helper()
	args := append([]string{"mcp", "--color=never"}, flags...)
	line, _ := mcpServerCall(t, dir, args, task, "{}")
	var frame struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("decoding tools/call reply %q: %v", line, err)
	}
	if frame.Error == nil {
		t.Fatalf("call to %q returned a result, want a protocol error: %s", task, line)
	}
	return frame.Error.Code, frame.Error.Message
}

// serveStderr starts the server with flags, closes its stdin at once, and
// returns what it printed on stderr.
func serveStderr(t *testing.T, dir string, flags ...string) string {
	t.Helper()
	return runWithStdin(t, dir, "", append([]string{"--color=never", "mcp"}, flags...)...).stderr
}

// Quickstart A (US1 scenario 1, SC-001).
func TestAllowlistMCP_ListsOnlyAllowed(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	if got, want := toolNames(t, dir, "--allow", "build,test"), []string{"build", "test"}; !slices.Equal(got, want) {
		t.Errorf("tools = %q, want %q", got, want)
	}
	all := []string{"build", "clean", "deploy", "lint", "release", "report", "test"}
	if got := toolNames(t, dir); !slices.Equal(got, all) {
		t.Errorf("tools without flags = %q, want %q", got, all)
	}
}

// Quickstart B (US1 scenario 2).
func TestAllowlistMCP_AllowedTaskRuns(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	text, isError, _ := toolCall(t, dir, []string{"--allow", "build,test"}, "build")
	if isError || !exists(t, dir, "build.done") {
		t.Errorf("allowed build did not run: isError=%v text=%q", isError, text)
	}
}

// Quickstart C and D (US1 scenario 3, FR-004, SC-003): a hidden task is
// answered like a name no task has, and nothing runs.
func TestAllowlistMCP_HiddenEqualsMissing(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	flags := []string{"--allow", "build,test"}
	hiddenCode, hiddenMsg := toolError(t, dir, flags, "deploy")
	missingCode, missingMsg := toolError(t, dir, flags, "nosuch")
	if hiddenCode != missingCode {
		t.Errorf("error codes differ: hidden %d, missing %d", hiddenCode, missingCode)
	}
	if h, m := strings.ReplaceAll(hiddenMsg, "deploy", "NAME"), strings.ReplaceAll(missingMsg, "nosuch", "NAME"); h != m {
		t.Errorf("error messages differ:\nhidden:  %s\nmissing: %s", hiddenMsg, missingMsg)
	}
	if exists(t, dir, "deploy.done") {
		t.Error("the hidden task deploy ran")
	}
}

// Quickstart E (US1 scenario 4) and the --deny forms (US2 scenario 4).
func TestAllowlistMCP_RepeatedFlagUnion(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	want := []string{"build", "test"}
	for _, flags := range [][]string{
		{"--allow", "build", "--allow", "test"},
		{"--allow", "build, test"},
		{"--allow", "build,build,test"},
	} {
		if got := toolNames(t, dir, flags...); !slices.Equal(got, want) {
			t.Errorf("%q: tools = %q, want %q", flags, got, want)
		}
	}
	want = []string{"build", "clean", "lint", "release", "report"}
	for _, flags := range [][]string{
		{"--deny", "deploy,test"},
		{"--deny", "deploy", "--deny", "test"},
	} {
		if got := toolNames(t, dir, flags...); !slices.Equal(got, want) {
			t.Errorf("%q: tools = %q, want %q", flags, got, want)
		}
	}
}

// Quickstart O (FR-008): an exposed tool's entry is byte-identical to the one
// listed without the flags.
func TestAllowlistMCP_ListedEntriesUnchanged(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	without := toolEntries(t, dir)
	with := toolEntries(t, dir, "--allow", "build,test,release")
	for _, name := range []string{"build", "test", "release"} {
		if with[name] == "" || with[name] != without[name] {
			t.Errorf("%s changed under --allow:\nwithout: %s\nwith:    %s", name, without[name], with[name])
		}
	}
}

// US1 scenario 6 and FR-014, FR-016: the startup line counts exposed tasks
// only when a filter is given.
func TestAllowlistMCP_StartupLine(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	cases := []struct {
		flags []string
		want  string
	}{
		{nil, "rune MCP server on stdio\n"},
		{[]string{"--allow", "build,test"}, "rune MCP server on stdio (2 of 7 tasks exposed)\n"},
		{[]string{"--deny", "deploy"}, "rune MCP server on stdio (6 of 7 tasks exposed)\n"},
		{
			[]string{"--allow", "build", "--allow-destructive", "clean"},
			"rune MCP server on stdio (1 of 7 tasks exposed; destructive tasks allowed: clean)\n",
		},
	}
	for _, c := range cases {
		if got := serveStderr(t, dir, c.flags...); got != c.want {
			t.Errorf("%q: stderr = %q, want %q", c.flags, got, c.want)
		}
	}
}

// Quickstart F and G (US2 scenarios 1 and 2).
func TestAllowlistMCP_DenyHidesOne(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	want := []string{"build", "clean", "lint", "release", "report", "test"}
	if got := toolNames(t, dir, "--deny", "deploy"); !slices.Equal(got, want) {
		t.Errorf("tools = %q, want %q", got, want)
	}
	toolError(t, dir, []string{"--deny", "deploy"}, "deploy")
	if exists(t, dir, "deploy.done") {
		t.Error("the denied task deploy ran")
	}
}

// Quickstart H (US2 scenario 3): a task named in both flags is hidden.
func TestAllowlistMCP_DenyWinsOverAllow(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	got := toolNames(t, dir, "--allow", "build,deploy", "--deny", "deploy")
	if want := []string{"build"}; !slices.Equal(got, want) {
		t.Errorf("tools = %q, want %q", got, want)
	}
}

// Quickstart I and J (US3 scenarios 1 to 3): dependencies of an exposed task
// run although they are not tools, private ones included.
func TestAllowlistMCP_HiddenDependenciesRun(t *testing.T) {
	for _, flags := range [][]string{{"--allow", "test"}, {"--deny", "build"}} {
		dir := writeRunefile(t, allowlistRunefile)
		text, isError, _ := toolCall(t, dir, flags, "test")
		if isError {
			t.Errorf("%q: test failed: %q", flags, text)
		}
		for _, marker := range []string{"build.done", "setup.done", "test.done"} {
			if !exists(t, dir, marker) {
				t.Errorf("%q: %s missing after calling test", flags, marker)
			}
		}
		toolError(t, dir, flags, "build")
	}
}

// Quickstart K (US3 scenario 4): a hidden post-hook runs.
func TestAllowlistMCP_HiddenHookRuns(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	text, isError, _ := toolCall(t, dir, []string{"--allow", "lint"}, "lint")
	if isError || !exists(t, dir, "lint.done") || !exists(t, dir, "report.done") {
		t.Errorf("lint or its hidden hook did not run: isError=%v text=%q", isError, text)
	}
}

const (
	releaseReachesClean = `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
	cleanIsDestructive  = `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`
)

// Quickstart L, M, N and contracts/mcp-tools.md "Interaction with
// --allow-destructive" (US3 scenarios 5 to 7, FR-007): the filters grant
// nothing and take no grant away.
func TestAllowlistMCP_DestructiveRulesUnchanged(t *testing.T) {
	t.Run("hidden ungranted dependency refuses the call (L)", func(t *testing.T) {
		dir := writeRunefile(t, allowlistRunefile)
		text, isError, _ := toolCall(t, dir, []string{"--allow", "release"}, "release")
		if !isError || text != releaseReachesClean {
			t.Errorf("isError=%v text=%q, want %q", isError, text, releaseReachesClean)
		}
		if exists(t, dir, "clean.done") || exists(t, dir, "release.done") {
			t.Error("part of the refused call ran")
		}
	})
	for _, flags := range [][]string{
		{"--allow", "release", "--allow-destructive", "clean"},
		{"--deny", "clean", "--allow-destructive", "clean"},
	} {
		t.Run("granted hidden dependency runs (M) "+flags[0], func(t *testing.T) {
			dir := writeRunefile(t, allowlistRunefile)
			text, isError, _ := toolCall(t, dir, flags, "release")
			if isError || !exists(t, dir, "clean.done") || !exists(t, dir, "release.done") {
				t.Errorf("release did not run with its granted dependency: isError=%v text=%q", isError, text)
			}
			if slices.Contains(toolNames(t, dir, flags...), "clean") {
				t.Error("the grant made the hidden task clean a tool")
			}
			toolError(t, dir, flags, "clean")
		})
	}
	t.Run("--allow does not grant (N)", func(t *testing.T) {
		dir := writeRunefile(t, allowlistRunefile)
		text, isError, _ := toolCall(t, dir, []string{"--allow", "clean"}, "clean")
		if !isError || text != cleanIsDestructive {
			t.Errorf("isError=%v text=%q, want %q", isError, text, cleanIsDestructive)
		}
		if exists(t, dir, "clean.done") {
			t.Error("--allow let the destructive task run")
		}
	})
	t.Run("exposed and granted runs", func(t *testing.T) {
		dir := writeRunefile(t, allowlistRunefile)
		text, isError, _ := toolCall(t, dir, []string{"--allow", "clean", "--allow-destructive", "clean"}, "clean")
		if isError || !exists(t, dir, "clean.done") {
			t.Errorf("exposed and granted clean did not run: isError=%v text=%q", isError, text)
		}
	})
}

// Quickstart P, error rows (US4 scenarios 1 to 4, SC-005): a bad value ends
// the server with exit 2 before any Runefile code runs.
func TestAllowlistMCP_BadFlagStopsServer(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"allow typo", []string{"--allow", "biuld"}, `rune: unknown task in --allow: biuld (did you mean "build"?)`},
		{"deny typo", []string{"--deny", "biuld"}, `rune: unknown task in --deny: biuld (did you mean "build"?)`},
		{"allow empty", []string{"--allow", ""}, "rune: --allow: empty task name"},
		{"deny trailing comma", []string{"--deny", "build,"}, "rune: --deny: empty task name"},
		{"wildcard", []string{"--allow", "*"}, "rune: unknown task in --allow: *"},
	}
	for _, c := range cases {
		dir := writeRunefile(t, allowlistRunefile)
		r := run(t, dir, nil, append([]string{"--color=never", "mcp"}, c.args...)...)
		if r.code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", c.name, r.code, r.stderr)
		}
		if strings.TrimSpace(r.stderr) != c.want {
			t.Errorf("%s: stderr %q, want %q", c.name, r.stderr, c.want)
		}
		if exists(t, dir, "context.done") {
			t.Errorf("%s: the [context] hook ran before the usage error", c.name)
		}
	}
}

// Quickstart P, warning rows (US4 scenarios 5 and 6, FR-012, FR-013).
func TestAllowlistMCP_Warnings(t *testing.T) {
	const noTasks = "warning: no tasks are exposed to agents; check --allow and --deny\n"
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{
			"private name in --allow",
			[]string{"--allow", "_setup"},
			"warning: --allow _setup has no effect: task \"_setup\" is never exposed to agents\n" +
				noTasks + "rune MCP server on stdio (0 of 7 tasks exposed)\n",
		},
		{
			"context hook in --deny",
			[]string{"--deny", "health"},
			"warning: --deny health has no effect: task \"health\" is never exposed to agents\n" +
				"rune MCP server on stdio (7 of 7 tasks exposed)\n",
		},
		{
			"allow and deny cancel out",
			[]string{"--allow", "build", "--deny", "build"},
			noTasks + "rune MCP server on stdio (0 of 7 tasks exposed)\n",
		},
	}
	for _, c := range cases {
		dir := writeRunefile(t, allowlistRunefile)
		if got := serveStderr(t, dir, c.flags...); got != c.want {
			t.Errorf("%s: stderr = %q, want %q", c.name, got, c.want)
		}
		if !exists(t, dir, "context.done") {
			t.Errorf("%s: the server did not start (no [context] hook marker)", c.name)
		}
	}
	dir := writeRunefile(t, allowlistRunefile)
	if got := toolNames(t, dir, "--allow", "build", "--deny", "build"); len(got) != 0 {
		t.Errorf("tools = %q, want none", got)
	}
}

// A Runefile with nothing to expose is not the filters' doing, so the warning
// that points at them stays quiet.
func TestAllowlistMCP_NothingExposableDoesNotBlameFilters(t *testing.T) {
	dir := writeRunefile(t, "_setup:\n    @echo s\n\n# Project health.\n[context]\nhealth:\n    @echo ok\n")
	want := "warning: --deny health has no effect: task \"health\" is never exposed to agents\n" +
		"rune MCP server on stdio (0 of 0 tasks exposed)\n"
	if got := serveStderr(t, dir, "--deny", "health"); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// A space-separated task list is a usage error, not a silently shorter list.
func TestAllowlistMCP_SpaceSeparatedListRejected(t *testing.T) {
	dir := writeRunefile(t, allowlistRunefile)
	r := run(t, dir, nil, "--color=never", "mcp", "--deny", "build", "deploy")
	if r.code != 2 {
		t.Errorf("exit %d, want 2 (stderr %q)", r.code, r.stderr)
	}
	if exists(t, dir, "context.done") {
		t.Error("the [context] hook ran before the usage error")
	}
}

// Edge case "Imported task": names use the Runefile form, and the tool-name
// form is an unknown task with a suggestion.
func TestAllowlistMCP_ImportedTask(t *testing.T) {
	dir := writeRunefile(t, "mod tools \"tools.rune\"\n\n# Builds.\nbuild:\n    @echo built\n")
	module := "# Lints the code.\nlint:\n    @echo linted > lint.done\n"
	if err := os.WriteFile(filepath.Join(dir, "tools.rune"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := toolNames(t, dir, "--allow", "tools::lint"), []string{"tools__lint"}; !slices.Equal(got, want) {
		t.Errorf("tools = %q, want %q", got, want)
	}
	if got, want := toolNames(t, dir, "--deny", "tools::lint"), []string{"build"}; !slices.Equal(got, want) {
		t.Errorf("tools = %q, want %q", got, want)
	}
	r := run(t, dir, nil, "--color=never", "mcp", "--allow", "tools__lint")
	want := `rune: unknown task in --allow: tools__lint (did you mean "tools::lint"?)`
	if r.code != 2 || strings.TrimSpace(r.stderr) != want {
		t.Errorf("exit %d stderr %q, want exit 2 and %q", r.code, r.stderr, want)
	}
}
