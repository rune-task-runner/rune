package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// toolHintsRunefile is the spec 033 quickstart scratch Runefile: every body
// writes a marker file, so "nothing ran" is checkable.
const toolHintsRunefile = `# Deploys the service.
deploy:
    @echo deployed > deploy.done

# Reports status.
[readonly]
status:
    @echo status > status.done

# Probes a remote endpoint.
[readonly, network]
health:
    @echo healthy > health.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Fetches the data set.
[network]
fetch:
    @echo fetched > fetch.done

# Cuts a release.
release: clean
    @echo released > release.done

# Mirrors the data set.
mirror: fetch
    @echo mirrored > mirror.done

# Fetches after success.
post: && fetch
    @echo post > post.done

# Cleans up on failure.
bad: || clean
    @exit 1

# Reaches clean two levels down.
outer: inner
    @echo outer > outer.done

# Reaches clean one level down.
inner: clean
    @echo inner > inner.done

# Ships after a private wipe.
ship: _wipe
    @echo shipped > ship.done

[private]
[confirm("Wipe?")]
_wipe:
    @echo wiped > wipe.done

# Dispatches per OS.
dispatch: wipe-win
    @echo dispatched > dispatch.done

# Wipes on Windows.
[windows, confirm("Wipe?")]
wipe-win:
    @echo wiped > wipe-win.done

# Reports through a private reader.
[readonly]
report: _read-state
    @echo report > report.done

[readonly, private]
_read-state:
    @echo state > state.done
`

// toolHints lists the tools of the Runefile in dir, on a server started with
// flags, and returns each tool's raw annotations, so a missing readOnlyHint
// key stays distinct from false.
func toolHints(t *testing.T, dir string, flags ...string) map[string]map[string]any {
	t.Helper()
	reply := mcpToolsList(t, dir, flags...)
	var frame struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(reply), &frame); err != nil {
		t.Fatalf("decoding tools/list reply: %v\nreply: %s", err, reply)
	}
	hints := make(map[string]map[string]any, len(frame.Result.Tools))
	for _, tool := range frame.Result.Tools {
		hints[tool.Name] = tool.Annotations
	}
	return hints
}

// Quickstart rows A to E and M (FR-002, SC-001): only a task with [readonly]
// claims to be read-only, and every other tool leaves the hint out.
func TestToolHintsMCP_ReadOnlyOnlyWhenDeclared(t *testing.T) {
	hints := toolHints(t, writeRunefile(t, toolHintsRunefile))
	want := map[string]bool{"deploy": false, "status": true, "health": true, "clean": false, "fetch": false, "report": true}
	for tool, readOnly := range want {
		a, ok := hints[tool]
		if !ok {
			t.Errorf("%s is not listed", tool)
			continue
		}
		v, present := a["readOnlyHint"]
		if readOnly && v != true {
			t.Errorf("%s: readOnlyHint = %v, want true", tool, v)
		}
		if !readOnly && present {
			t.Errorf("%s: readOnlyHint = %v, want the key absent", tool, v)
		}
	}
}

// Quickstart Q (FR-013): [readonly] changes neither how a task runs, over MCP
// or on the command line, nor how --list shows it.
func TestToolHintsMCP_ReadonlyChangesNothingElse(t *testing.T) {
	dir := writeRunefile(t, toolHintsRunefile)
	text, isError, stderr := toolCall(t, dir, nil, "status")
	if isError || !strings.Contains(text, "[exit 0]") || !exists(t, dir, "status.done") {
		t.Errorf("status over MCP: isError=%v text=%q stderr=%q, want [exit 0] and status.done", isError, text, stderr)
	}

	dir = writeRunefile(t, toolHintsRunefile)
	if r := run(t, dir, nil, "status"); r.code != 0 || !exists(t, dir, "status.done") {
		t.Errorf("rune status: exit %d, stderr %q, want 0 and status.done", r.code, r.stderr)
	}

	unmarked := strings.NewReplacer("[readonly]\n", "", "[readonly, ", "[").Replace(toolHintsRunefile)
	if strings.Contains(unmarked, "readonly") {
		t.Fatalf("unmarked Runefile still has readonly:\n%s", unmarked)
	}
	marked := run(t, writeRunefile(t, toolHintsRunefile), nil, "--list")
	plain := run(t, writeRunefile(t, unmarked), nil, "--list")
	if marked.code != 0 || marked.stdout != plain.stdout {
		t.Errorf("--list changed with [readonly] (exit %d):\nwith:    %q\nwithout: %q", marked.code, marked.stdout, plain.stdout)
	}
	if !strings.Contains(marked.stdout, "status") || !strings.Contains(marked.stdout, "Reports status.") {
		t.Errorf("--list does not show status with its doc: %q", marked.stdout)
	}
}

// Quickstart rows A to N (FR-003 to FR-006, SC-001, SC-002): every hint of
// every tool, with dispatch following the host OS. This replaces
// TestMCPE2E_Pending_HintsCoverDependencies, which issue #37 names but this
// repository does not have.
func TestToolHintsMCP_CoverDependencies(t *testing.T) {
	onWindows := runtime.GOOS == "windows"
	rows := []struct {
		tool                             string
		readOnly, destructive, openWorld bool
	}{
		{"deploy", false, false, false},
		{"status", true, false, false},
		{"health", true, false, true},
		{"clean", false, true, false},
		{"fetch", false, false, true},
		{"release", false, true, false},
		{"mirror", false, false, true},
		{"post", false, false, true},
		{"bad", false, true, false},
		{"outer", false, true, false},
		{"inner", false, true, false},
		{"ship", false, true, false},
		{"report", true, false, false},
		{"dispatch", false, onWindows, false},
	}
	hints := toolHints(t, writeRunefile(t, toolHintsRunefile))
	for _, r := range rows {
		a, ok := hints[r.tool]
		if !ok {
			t.Errorf("%s is not listed", r.tool)
			continue
		}
		v, present := a["readOnlyHint"]
		if r.readOnly && v != true {
			t.Errorf("%s: readOnlyHint = %v, want true", r.tool, v)
		}
		if !r.readOnly && present {
			t.Errorf("%s: readOnlyHint = %v, want the key absent", r.tool, v)
		}
		if a["destructiveHint"] != r.destructive {
			t.Errorf("%s: destructiveHint = %v, want %v", r.tool, a["destructiveHint"], r.destructive)
		}
		if a["openWorldHint"] != r.openWorld {
			t.Errorf("%s: openWorldHint = %v, want %v", r.tool, a["openWorldHint"], r.openWorld)
		}
	}
	for _, private := range []string{"_wipe", "_read-state"} {
		if _, ok := hints[private]; ok {
			t.Errorf("private task %s is listed", private)
		}
	}
	if _, ok := hints["wipe-win"]; ok != onWindows {
		t.Errorf("wipe-win listed = %v on %s", ok, runtime.GOOS)
	}
}

// Quickstart O (FR-005): a [confirm] task in an imported module counts.
func TestToolHintsMCP_ImportedTask(t *testing.T) {
	dir := writeRunefile(t, "mod tools \"tools.rune\"\n\n# Ships after cleaning.\nshipit: tools::clean\n    @echo shipped > ship.done\n")
	module := "# Deletes the build output.\n[confirm(\"Delete dist?\")]\nclean:\n    @echo cleaned > clean.done\n"
	if err := os.WriteFile(filepath.Join(dir, "tools.rune"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	hints := toolHints(t, dir)
	if got := hints["shipit"]["destructiveHint"]; got != true {
		t.Errorf("shipit: destructiveHint = %v, want true (tools: %v)", got, hints)
	}
}

// Quickstart P (FR-010): a grant changes no hint.
func TestToolHintsMCP_GrantChangesNothing(t *testing.T) {
	without := toolHints(t, writeRunefile(t, toolHintsRunefile))
	with := toolHints(t, writeRunefile(t, toolHintsRunefile), "--allow-destructive", "clean")
	if !reflect.DeepEqual(without, with) {
		t.Errorf("hints changed with a grant:\nwithout: %v\nwith:    %v", without, with)
	}
}

// Quickstart R, S, T (FR-010, SC-004): calls give the results they gave
// before this change. The bare [exit 1] of a declined dependency is not
// pinned: issue #38 will change it.
func TestToolHintsMCP_CallsUnchanged(t *testing.T) {
	dir := writeRunefile(t, toolHintsRunefile)
	text, isError, _ := toolCall(t, dir, nil, "clean")
	if !isError || text != refusedText("clean") || exists(t, dir, "clean.done") {
		t.Errorf("clean without a grant: isError=%v text=%q, want the refusal and no marker", isError, text)
	}

	dir = writeRunefile(t, toolHintsRunefile)
	text, isError, _ = toolCall(t, dir, nil, "release")
	if !isError || strings.Contains(text, "is destructive") {
		t.Errorf("release without a grant: isError=%v text=%q, want an error without the destructive refusal", isError, text)
	}
	if exists(t, dir, "clean.done") || exists(t, dir, "release.done") {
		t.Error("release without a grant ran clean or itself")
	}

	dir = writeRunefile(t, toolHintsRunefile)
	text, isError, stderr := toolCall(t, dir, []string{"--allow-destructive", "clean"}, "release")
	if isError || !exists(t, dir, "clean.done") || !exists(t, dir, "release.done") {
		t.Errorf("release with clean granted: isError=%v text=%q stderr=%q, want both markers", isError, text, stderr)
	}

	// Granting the task that reaches [confirm] does nothing: refusal reads the
	// task's own [confirm], so the operator grants clean.
	dir = writeRunefile(t, toolHintsRunefile)
	text, _, stderr = toolCall(t, dir, []string{"--allow-destructive", "release"}, "release")
	if !strings.Contains(stderr, `task "release" is not destructive`) {
		t.Errorf("--allow-destructive release: stderr %q, want the no-effect warning", stderr)
	}
	if exists(t, dir, "clean.done") || exists(t, dir, "release.done") {
		t.Errorf("release with only release granted ran clean or itself: %q", text)
	}
}

// Quickstart U to Z (FR-007 to FR-009, SC-003): a false [readonly] claim stops
// at analysis with exit 3, the contract's text, and nothing run.
func TestToolHintsMCP_StaticErrors(t *testing.T) {
	const (
		withConfirm  = `[readonly] task "wipe" cannot use [confirm]: remove one of them`
		runsUnmarked = `[readonly] task "report" runs "build", which is not [readonly]: add [readonly] to "build" or remove it from "report"`
		runsConfirm  = `[readonly] task "report" runs "wipe", which uses [confirm]: remove [readonly] from "report"`
		chainBroken  = `[readonly] task "b" runs "c", which is not [readonly]: add [readonly] to "c" or remove it from "b"`
		moduleBroken = `[readonly] task "tools::report" runs "tools::build", which is not [readonly]: add [readonly] to "tools::build" or remove it from "tools::report"`
	)
	const v = "# Reports.\n[readonly]\nreport: build\n    @echo report > report.done\n\n# Builds.\nbuild:\n    @echo built > build.done\n"
	analyzeCases := []struct {
		name, src, want string
	}{
		{"U", "# Wipes.\n[readonly, confirm(\"x?\")]\nwipe:\n    @echo wiped > wipe.done\n", withConfirm},
		{"V", v, runsUnmarked},
		{"W", "# Reports.\n[readonly]\nreport: wipe\n    @echo report > report.done\n\n# Wipes.\n[confirm]\nwipe:\n    @echo wiped > wipe.done\n", runsConfirm},
		{"X", "[readonly]\na: b\n    @echo a\n\n[readonly]\nb: c\n    @echo b\n\nc:\n    @echo c\n", chainBroken},
	}
	for _, c := range analyzeCases {
		r := run(t, writeRunefile(t, c.src), nil, "analyze")
		out := r.stdout + r.stderr
		if r.code != 3 || !strings.Contains(out, "error[RUNE2018]: "+c.want) {
			t.Errorf("%s: rune analyze exit %d, output %q, want exit 3 and error[RUNE2018]: %s", c.name, r.code, out, c.want)
		}
		if n := strings.Count(out, "error[RUNE2018]"); n != 1 {
			t.Errorf("%s: %d RUNE2018 errors, want 1: %q", c.name, n, out)
		}
	}

	for _, args := range [][]string{{"report"}, {"--list"}, {"--dump"}, {"--summary", "report"}, {"--dry-run", "report"}, {"mcp"}} {
		dir := writeRunefile(t, v)
		r := run(t, dir, nil, args...)
		if r.code != 3 || !strings.Contains(r.stdout+r.stderr, runsUnmarked) {
			t.Errorf("rune %v: exit %d, stdout %q, stderr %q, want exit 3 and the RUNE2018 text", args, r.code, r.stdout, r.stderr)
		}
		if exists(t, dir, "report.done") || exists(t, dir, "build.done") {
			t.Errorf("rune %v ran a task", args)
		}
	}

	dir := writeRunefile(t, v)
	if r := run(t, dir, nil, "--fmt"); r.code != 0 {
		t.Errorf("rune --fmt: exit %d, stderr %q, want 0", r.code, r.stderr)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "Runefile")); err != nil || !strings.Contains(string(b), "[readonly]") {
		t.Errorf("after --fmt the Runefile is %q (err %v), want it to keep [readonly]", b, err)
	}

	dir = writeRunefile(t, "mod tools \"tools.rune\"\n")
	if err := os.WriteFile(filepath.Join(dir, "tools.rune"), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, nil, "analyze"); r.code != 3 || !strings.Contains(r.stdout+r.stderr, moduleBroken) {
		t.Errorf("module: rune analyze exit %d, output %q, want exit 3 and %s", r.code, r.stdout+r.stderr, moduleBroken)
	}
}
