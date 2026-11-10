package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Spec 034 FR-017: these tests cover issue #38's acceptance criteria, in place
// of the TestMCPE2E_Pending_DependencyRefusalExplained the issue names, which
// this repository does not have. A call that can reach an ungranted [confirm]
// task is refused before anything runs, and the refusal names what to add.

// dependencyRefusalRunefile is the spec 034 quickstart scratch Runefile: every
// body writes a marker file, so "nothing ran" is checkable from the outside.
const dependencyRefusalRunefile = `# Builds the artifacts.
build:
    @echo built > build.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Wipes everything.
[confirm("Wipe?")]
wipe:
    @echo wiped > wipe.done

# Cuts a release.
release: build clean
    @echo released > release.done

# Cuts a full release.
full: build clean wipe
    @echo full > full.done

# Stages, then cleans.
ordered: stage clean
    @echo ordered > ordered.done

# Stages after a wipe.
stage: wipe
    @echo staged > stage.done

# Publishes, then cleans.
publish: && clean
    @echo published > publish.done

# Deploys, wiping on failure.
deploy: || wipe
    @echo deployed > deploy.done

# Resets, which also wipes.
[confirm("Reset?")]
reset: wipe
    @echo reset > reset.done

# Reaches clean two levels down.
outer: inner
    @echo outer > outer.done

# Reaches clean one level down.
inner: clean
    @echo inner > inner.done

# Ships after a private scrub.
ship: _scrub
    @echo shipped > ship.done

[private]
[confirm("Scrub?")]
_scrub:
    @echo scrubbed > scrub.done

# Dispatches per OS.
dispatch: wipe-win
    @echo dispatched > dispatch.done

# Wipes on Windows.
[windows, confirm("Wipe?")]
wipe-win:
    @echo wiped > wipe-win.done

# Rolls back, wiping if that fails.
rollback: || wipe
    @exit 1

# Fails, then rolls back.
risky: || rollback
    @exit 1

# Checks the tree.
lint:
    @echo linted > lint.done
`

// doneFiles lists the marker files in dir, so a refused call can be checked to
// have run nothing at all.
func doneFiles(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "*.done"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// checkRefused fails the test unless the call was refused with exactly want
// and left no marker file.
func checkRefused(t *testing.T, label, dir, text string, isError bool, want string) {
	t.Helper()
	if !isError || text != want {
		t.Errorf("%s: isError=%v text=%q, want %q", label, isError, text, want)
	}
	if ran := doneFiles(t, dir); len(ran) != 0 {
		t.Errorf("%s: refused, but these ran: %q", label, ran)
	}
}

// Quickstart rows A, C, D, E, F, G, K, O (US1, FR-004, FR-005): each call is
// refused before any task body or hook runs, naming every ungranted task.
func TestDependencyRefusalMCP_RefusedBeforeAnythingRuns(t *testing.T) {
	cases := []struct {
		row   string
		task  string
		flags []string
		want  string
	}{
		{"A", "release", nil, `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`},
		{"C", "full", nil, `task "full" can run destructive tasks "clean", "wipe"; to permit them, add clean,wipe to --allow-destructive and restart the server`},
		{"D", "full", []string{"--allow-destructive", "clean"}, `task "full" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`},
		{"E", "ordered", nil, `task "ordered" can run destructive tasks "wipe", "clean"; to permit them, add wipe,clean to --allow-destructive and restart the server`},
		{"F", "publish", nil, `task "publish" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`},
		{"G", "deploy", nil, `task "deploy" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`},
		{"K", "outer", nil, `task "outer" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`},
		{"O", "rollback", nil, `task "rollback" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`},
	}
	for _, c := range cases {
		dir := writeRunefile(t, dependencyRefusalRunefile)
		text, isError, _ := toolCall(t, dir, c.flags, c.task)
		checkRefused(t, c.row+" "+c.task, dir, text, isError, c.want)
	}
}

// Quickstart row L (US1.4): a private task is named so the operator can grant
// it, and it is still not a tool.
func TestDependencyRefusalMCP_PrivateTask(t *testing.T) {
	dir := writeRunefile(t, dependencyRefusalRunefile)
	text, isError, _ := toolCall(t, dir, nil, "ship")
	checkRefused(t, "ship", dir, text, isError, `task "ship" can run destructive task "_scrub"; to permit it, add _scrub to --allow-destructive and restart the server`)
	if _, listed := toolHints(t, dir)["_scrub"]; listed {
		t.Error("_scrub is listed as a tool")
	}
}

// Quickstart row M (US1.5): an imported task is named the way the flag
// accepts it.
func TestDependencyRefusalMCP_ImportedTask(t *testing.T) {
	dir := writeRunefile(t, "mod tools \"tools.rune\"\n\n# Ships after cleaning.\nshipit: tools::clean\n    @echo shipped > ship.done\n")
	module := "# Deletes the build output.\n[confirm(\"Delete dist?\")]\nclean:\n    @echo cleaned > clean.done\n"
	if err := os.WriteFile(filepath.Join(dir, "tools.rune"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isError, _ := toolCall(t, dir, nil, "shipit")
	checkRefused(t, "shipit", dir, text, isError, `task "shipit" can run destructive task "tools::clean"; to permit it, add tools::clean to --allow-destructive and restart the server`)
}

// Quickstart row N (FR-003): a dependency the host skips does not count.
func TestDependencyRefusalMCP_HostOS(t *testing.T) {
	dir := writeRunefile(t, dependencyRefusalRunefile)
	text, isError, _ := toolCall(t, dir, nil, "dispatch")
	if runtime.GOOS == "windows" {
		checkRefused(t, "dispatch", dir, text, isError, `task "dispatch" can run destructive task "wipe-win"; to permit it, add wipe-win to --allow-destructive and restart the server`)
		return
	}
	if isError || !strings.Contains(text, "[exit 0]") || !exists(t, dir, "dispatch.done") {
		t.Errorf("dispatch: isError=%v text=%q, want it to run", isError, text)
	}
}

// Quickstart row P (FR-002): wipe is only rollback's own failure hook, which
// never fires when rollback itself runs as a failure hook, so risky is not
// refused. It fails with its own exit code, and wipe does not run.
func TestDependencyRefusalMCP_FailureHookOfFailureHook(t *testing.T) {
	dir := writeRunefile(t, dependencyRefusalRunefile)
	text, isError, _ := toolCall(t, dir, nil, "risky")
	if !isError || !strings.Contains(text, "[exit 1]") || strings.Contains(text, "destructive") {
		t.Errorf("risky: isError=%v text=%q, want its own [exit 1] and no refusal", isError, text)
	}
	if exists(t, dir, "wipe.done") {
		t.Error("risky: wipe ran")
	}
}

// Quickstart row R (FR-010): the refusal comes before argument checks.
func TestDependencyRefusalMCP_BeforeArguments(t *testing.T) {
	dir := writeRunefile(t, dependencyRefusalRunefile)
	line, _ := mcpServerCall(t, dir, []string{"mcp", "--color=never"}, "release", `{"bogus": 1}`)
	text, isError := toolReply(t, line)
	checkRefused(t, "release with bogus", dir, text, isError, `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`)
}

// Quickstart rows B, H, I, J, Q (US2, FR-005 to FR-007, FR-011): the called
// task comes first, a grant drops only its own task, and the direct refusal
// is today's text.
func TestDependencyRefusalMCP_GrantsAndOrder(t *testing.T) {
	cases := []struct {
		row   string
		task  string
		flags []string
		want  string   // the refusal, or "" when the call runs
		ran   []string // the markers a call that runs leaves
	}{
		{"B", "release", []string{"--allow-destructive", "clean"}, "", []string{"build.done", "clean.done", "release.done"}},
		{"H", "reset", nil, `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server`, nil},
		{"I", "reset", []string{"--allow-destructive", "reset"}, `task "reset" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`, nil},
		{"J", "reset", []string{"--allow-destructive", "reset,wipe"}, "", []string{"wipe.done", "reset.done"}},
		{"Q", "clean", nil, refusedText("clean"), nil},
	}
	for _, c := range cases {
		dir := writeRunefile(t, dependencyRefusalRunefile)
		text, isError, _ := toolCall(t, dir, c.flags, c.task)
		label := c.row + " " + c.task
		if c.want != "" {
			checkRefused(t, label, dir, text, isError, c.want)
			continue
		}
		if isError || !strings.Contains(text, "[exit 0]") {
			t.Errorf("%s: isError=%v text=%q, want it to run", label, isError, text)
		}
		for _, marker := range c.ran {
			if !exists(t, dir, marker) {
				t.Errorf("%s: %s missing", label, marker)
			}
		}
	}
}

// SC-003: for each refused row, restarting with the names the refusal gives
// lets the same call through the check. Row O still fails with its own body's
// exit code, which is not a refusal.
func TestDependencyRefusalMCP_OneRestartIsEnough(t *testing.T) {
	for _, task := range []string{"release", "full", "ordered", "publish", "deploy", "reset", "outer", "ship", "rollback"} {
		text, _, _ := toolCall(t, writeRunefile(t, dependencyRefusalRunefile), nil, task)
		_, after, ok := strings.Cut(text, "; to permit ")
		if !ok {
			t.Errorf("%s: not refused: %q", task, text)
			continue
		}
		_, after, _ = strings.Cut(after, "add ")
		names, _, _ := strings.Cut(after, " to --allow-destructive")
		text, _, _ = toolCall(t, writeRunefile(t, dependencyRefusalRunefile), []string{"--allow-destructive", names}, task)
		if strings.Contains(text, "to permit") {
			t.Errorf("%s with --allow-destructive %s: still refused: %q", task, names, text)
		}
	}
}
