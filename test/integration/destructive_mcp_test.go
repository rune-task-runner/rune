package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// destructiveRunefile is the spec 031 quickstart scratch Runefile: every body
// writes a marker file, so "nothing ran" is checkable from the outside.
const destructiveRunefile = `# Project health for agents.
[context]
health:
    @echo ok > context.done

# Removes build output.
[confirm("Delete build output?")]
clean:
    @echo cleaned > clean.done

# Resets local state.
[confirm("Reset local state?")]
reset:
    @echo reset > reset.done

# Cuts a release.
release: clean
    @echo released > release.done

# Ships after a private wipe.
ship: wipe
    @echo shipped > ship.done

# Builds the project.
build:
    @echo built

# Fails with status 3 once approved.
[confirm]
fail3:
    @exit 3

[private]
[confirm("Wipe caches?")]
wipe:
    @echo wiped > wipe.done
`

// toolCall starts `rune mcp` with flags in dir, calls task once, and returns
// the tool result's text, its isError flag, and the server's stderr.
func toolCall(t *testing.T, dir string, flags []string, task string) (text string, isError bool, stderr string) {
	t.Helper()
	args := append([]string{"mcp", "--color=never"}, flags...)
	line, stderr := mcpServerCall(t, dir, args, task, "{}")
	text, isError = toolReply(t, line)
	return text, isError, stderr
}

// toolReply decodes a tools/call reply into the result's text and isError flag.
func toolReply(t *testing.T, line string) (text string, isError bool) {
	t.Helper()
	var frame struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("decoding tools/call reply %q: %v", line, err)
	}
	var b strings.Builder
	for _, c := range frame.Result.Content {
		b.WriteString(c.Text)
	}
	return b.String(), frame.Result.IsError
}

func exists(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// US1 scenario 1: the granted task runs, and no prompt is shown or awaited.
func TestDestructiveMCP_GrantedTaskRuns(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	text, isError, stderr := toolCall(t, dir, []string{"--allow-destructive", "clean"}, "clean")
	if isError {
		t.Fatalf("granted clean failed: %q (stderr %q)", text, stderr)
	}
	if !exists(t, dir, "clean.done") {
		t.Errorf("clean did not run: result %q", text)
	}
	if strings.Contains(text, "Delete build output?") || strings.Contains(stderr, "Delete build output?") {
		t.Errorf("confirmation prompt shown over MCP: result %q stderr %q", text, stderr)
	}
}

// US1 scenario 2: a granted task's exit status reaches the agent as for any task.
func TestDestructiveMCP_GrantedExitStatus(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	text, isError, _ := toolCall(t, dir, []string{"--allow-destructive", "fail3"}, "fail3")
	if !isError || !strings.Contains(text, "[exit 3]") {
		t.Errorf("fail3: isError=%v text=%q, want isError and [exit 3]", isError, text)
	}
}

// US1 scenario 3: comma-separated and repeated flags grant the same tasks.
func TestDestructiveMCP_GrantForms(t *testing.T) {
	forms := map[string][]string{
		"comma":    {"--allow-destructive", "clean,reset"},
		"repeated": {"--allow-destructive", "clean", "--allow-destructive", "reset"},
	}
	for name, flags := range forms {
		for _, task := range []string{"clean", "reset"} {
			dir := writeRunefile(t, destructiveRunefile)
			text, isError, _ := toolCall(t, dir, flags, task)
			if isError || !exists(t, dir, task+".done") {
				t.Errorf("%s form, %s: isError=%v text=%q, marker missing or call failed", name, task, isError, text)
			}
		}
	}
}

// US1 scenario 5: the startup line names the granted tasks.
func TestDestructiveMCP_StartupLineListsGrant(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	_, _, stderr := toolCall(t, dir, []string{"--allow-destructive", "clean,reset"}, "build")
	want := "rune MCP server on stdio (destructive tasks allowed: clean, reset)"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

func refusedText(task string) string {
	return `task "` + task + `" is destructive; to permit it, add ` + task + ` to --allow-destructive and restart the server`
}

// US2 scenarios 1 and 2: a direct call to an ungranted destructive task is
// refused before anything runs, and the text names the flag that permits it.
func TestDestructiveMCP_UngrantedDirectCallRefused(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		task  string
	}{
		{"no flag", nil, "clean"},
		{"other task granted", []string{"--allow-destructive", "clean"}, "reset"},
	}
	for _, c := range cases {
		dir := writeRunefile(t, destructiveRunefile)
		text, isError, _ := toolCall(t, dir, c.flags, c.task)
		if !isError || text != refusedText(c.task) {
			t.Errorf("%s: isError=%v text=%q, want %q", c.name, isError, text, refusedText(c.task))
		}
		if exists(t, dir, c.task+".done") {
			t.Errorf("%s: %s ran although refused", c.name, c.task)
		}
	}
}

// US2 scenarios 3 and 4: a granted dependency runs; an ungranted one stops
// the run before it or its dependent does anything.
func TestDestructiveMCP_DependencyFollowsGrant(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	text, isError, _ := toolCall(t, dir, []string{"--allow-destructive", "clean"}, "release")
	if isError || !exists(t, dir, "clean.done") || !exists(t, dir, "release.done") {
		t.Errorf("granted dependency: isError=%v text=%q; want clean.done and release.done", isError, text)
	}

	dir = writeRunefile(t, destructiveRunefile)
	text, isError, _ = toolCall(t, dir, nil, "release")
	if !isError || !strings.Contains(text, "[exit 1]") {
		t.Errorf("ungranted dependency: isError=%v text=%q, want isError and [exit 1]", isError, text)
	}
	if exists(t, dir, "clean.done") || exists(t, dir, "release.done") {
		t.Error("ungranted dependency: clean or release ran")
	}
}

// Quickstart scenario F (FR-006): a granted private task runs when a callable
// task reaches it, though it is never a tool itself.
func TestDestructiveMCP_GrantedPrivateDependency(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	text, isError, _ := toolCall(t, dir, []string{"--allow-destructive", "wipe"}, "ship")
	if isError || !exists(t, dir, "wipe.done") || !exists(t, dir, "ship.done") {
		t.Errorf("isError=%v text=%q; want wipe.done and ship.done", isError, text)
	}
}

// US2 scenario 5: granting a task does not grant its destructive dependency.
func TestDestructiveMCP_GrantDoesNotWiden(t *testing.T) {
	dir := writeRunefile(t, `# Wipes caches.
[confirm]
wipe:
    @echo wiped > wipe.done

# Cleans after a wipe.
[confirm]
clean: wipe
    @echo cleaned > clean.done
`)
	text, isError, _ := toolCall(t, dir, []string{"--allow-destructive", "clean"}, "clean")
	if !isError {
		t.Errorf("clean succeeded although its dependency wipe is not granted: %q", text)
	}
	if exists(t, dir, "wipe.done") || exists(t, dir, "clean.done") {
		t.Error("wipe or clean ran")
	}
}

// US2 scenario 6: an ungranted destructive post-hook or failure hook never runs.
func TestDestructiveMCP_UngrantedHooksDoNotRun(t *testing.T) {
	const hooks = `# Wipes caches.
[confirm]
wipe:
    @echo wiped > wipe.done

# Builds, then wipes.
build: && wipe
    @echo built

# Fails, then wipes.
build2: || wipe
    @exit 1
`
	for _, task := range []string{"build", "build2"} {
		dir := writeRunefile(t, hooks)
		text, isError, _ := toolCall(t, dir, nil, task)
		if !isError {
			t.Errorf("%s: want a failure, got %q", task, text)
		}
		if exists(t, dir, "wipe.done") {
			t.Errorf("%s: the ungranted hook wipe ran", task)
		}
	}
}

// SC-003: with no flag, no destructive task runs from any entry point.
func TestDestructiveMCP_NoFlagRunsNothingDestructive(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	for _, task := range []string{"clean", "reset", "release", "ship", "build", "fail3"} {
		toolCall(t, dir, nil, task)
	}
	for _, marker := range []string{"clean.done", "reset.done", "wipe.done", "release.done", "ship.done"} {
		if exists(t, dir, marker) {
			t.Errorf("%s exists: a destructive task (or its dependent) ran without a grant", marker)
		}
	}
}

// US3 (quickstart H to L): a bad --allow-destructive value ends the server
// with exit 2 before any Runefile code runs, including the [context] hook.
func TestDestructiveMCP_BadFlagStopsServer(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"typo (H)", []string{"mcp", "--allow-destructive", "claen"}, `rune: unknown task in --allow-destructive: claen (did you mean "clean"?)`},
		{"empty value (I)", []string{"mcp", "--allow-destructive", ""}, "rune: --allow-destructive: empty task name"},
		{"trailing comma (I)", []string{"mcp", "--allow-destructive", "clean,"}, "rune: --allow-destructive: empty task name"},
		{"wildcard (J)", []string{"mcp", "--allow-destructive", "*"}, "rune: unknown task in --allow-destructive: *"},
		{"--yes after (L)", []string{"mcp", "--yes"}, "unknown flag: --yes"},
		{"--yes before (L)", []string{"--yes", "mcp"}, "unknown flag: --yes"},
	}
	for _, c := range cases {
		dir := writeRunefile(t, destructiveRunefile)
		r := run(t, dir, nil, append([]string{"--color=never"}, c.args...)...)
		if r.code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", c.name, r.code, r.stderr)
		}
		if !strings.Contains(r.stderr, c.want) {
			t.Errorf("%s: stderr %q lacks %q", c.name, r.stderr, c.want)
		}
		if exists(t, dir, "context.done") {
			t.Errorf("%s: the [context] hook ran before the usage error", c.name)
		}
	}
}

// US3 scenario 3 (quickstart K): granting a task without [confirm] warns once
// and starts the server; the startup line lists no destructive grant.
func TestDestructiveMCP_NonDestructiveGrantWarns(t *testing.T) {
	dir := writeRunefile(t, destructiveRunefile)
	r := runWithStdin(t, dir, "", "--color=never", "mcp", "--allow-destructive", "build")
	want := "warning: --allow-destructive build has no effect: task \"build\" is not destructive\n" +
		"rune MCP server on stdio\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}
