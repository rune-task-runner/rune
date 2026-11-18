package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/planstore"
)

// Spec 052, issue #50: every Rune MCP server writes an audit log, and
// `rune log` reads it. These tests drive the built binary with an SDK
// client. The letters are those of specs/052-mcp-audit-log/quickstart.md.

// auditRunefile is the quickstart scratch Runefile. Every body writes a
// marker, so a test can show that a refused call ran nothing. The waits are
// written with shell builtins only, so they run on every host.
const auditRunefile = `# Build the project.
build target="debug":
    @echo "built {{target}} out-marker-text" && echo built >> build.done

# Run the checks.
check:
    @echo checking >> check.done; exit 3

# Deploy to an environment.
deploy env:enum("staging","prod") token="":
    @echo deployed >> deploy.done

# Remove build output.
[confirm("Remove build output?")]
clean:
    @echo cleaned >> clean.done

# Wait for a file, then fail.
wait:
    @echo started > wait.started; while [ ! -f go.flag ]; do :; done; echo done >> wait.done; exit 3

# Never ends.
slow:
    @echo started > slow.started; while :; do :; done
`

const auditFixture = "s3cr3t-fixture-value"

// auditProject writes the scratch Runefile and returns the project and the
// environment that puts its audit log in a directory of the test's own.
func auditProject(t *testing.T, runefile string) (dir string, env []string, base string) {
	t.Helper()
	dir = writeRunefile(t, runefile)
	base = t.TempDir()
	return dir, []string{"RUNE_AUDIT_DIR=" + base, "RUNE_AUDIT_RETENTION_DAYS=", "API_TOKEN=" + auditFixture}, base
}

// logActions reads the project's actions of the last hour through
// `rune log --json`.
func logActions(t *testing.T, dir string, env []string, args ...string) []audit.Action {
	t.Helper()
	res := run(t, dir, env, append([]string{"log", "--json", "--since", "1h"}, args...)...)
	if res.code != 0 {
		t.Fatalf("rune log exited %d: %s", res.code, res.stderr)
	}
	var actions []audit.Action
	for line := range strings.Lines(res.stdout) {
		var a audit.Action
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("a line of rune log --json is not JSON: %q", line)
		}
		actions = append(actions, a)
	}
	return actions
}

// logCalls is logActions without the agent runs and the other records.
func logCalls(t *testing.T, dir string, env []string, args ...string) []audit.Action {
	t.Helper()
	var calls []audit.Action
	for _, a := range logActions(t, dir, env, args...) {
		if a.Type == audit.TypeCall {
			calls = append(calls, a)
		}
	}
	return calls
}

func lastLogCall(t *testing.T, dir string, env []string) audit.Action {
	t.Helper()
	calls := logCalls(t, dir, env)
	if len(calls) == 0 {
		t.Fatal("no call in the log")
	}
	return calls[len(calls)-1]
}

// logDirOf asks the binary where the project's log is.
func logDirOf(t *testing.T, dir string, env []string) string {
	t.Helper()
	res := run(t, dir, env, "log", "--dir")
	if res.code != 0 {
		t.Fatalf("rune log --dir exited %d: %s", res.code, res.stderr)
	}
	return strings.TrimSpace(res.stdout)
}

func todayFile(logDir string) string {
	return filepath.Join(logDir, time.Now().UTC().Format("2006-01-02")+".jsonl")
}

// breakToday puts a directory where today's day file belongs. The tests run
// as root in Docker, where a read-only directory stops nothing.
func breakToday(t *testing.T, logDir string) {
	t.Helper()
	path := todayFile(logDir)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".kept"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not appear", path)
		}
	}
}

func hasMarker(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// gitRepo makes dir a repository with one commit, or skips the test.
func gitRepo(t *testing.T, dir string) (commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	// The markers the tasks write would make every later tree dirty.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.done\n*.started\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "Runefile", ".gitignore")
	git("commit", "-q", "-m", "first")
	return git("rev-parse", "HEAD")
}

// Quickstart A and B, on both transports: a call that ran is recorded with
// the task, the arguments, the exit code, the duration, the git identity
// and the client.
func TestAuditLogCompletedAndFailed(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, _ := auditProject(t, auditRunefile)
			commit := gitRepo(t, dir)
			cs, _ := approvalSession(t, transport, dir, nil, env, nil)

			if text, isErr := callText(t, cs, "build", nil); isErr || !strings.Contains(text, "[exit 0]") {
				t.Fatalf("build: %q", text)
			}
			if text, isErr := callText(t, cs, "check", nil); !isErr || !strings.Contains(text, "[exit 3]") {
				t.Fatalf("check: %q", text)
			}
			calls := logCalls(t, dir, env)
			if len(calls) != 2 {
				t.Fatalf("%d calls in the log, want 2", len(calls))
			}
			build, check := calls[0], calls[1]

			if build.Tool != "build" || build.Task != "build" || build.Outcome != "completed" || build.ExitCode == nil || *build.ExitCode != 0 {
				t.Errorf("build: %+v", build)
			}
			if build.Arguments == nil || build.Arguments.Supplied != nil || string(build.Arguments.Effective) != `{"target":"debug"}` {
				t.Errorf("arguments of build: %+v", build.Arguments)
			}
			if build.End == nil || !build.End.After(build.Start) || build.DurationMs == nil || build.Start.Location() != time.UTC {
				t.Errorf("times of build: start %s end %v duration %v", build.Start, build.End, build.DurationMs)
			}
			if len(build.ID) != 16 || len(build.Session) != 16 || build.ID == check.ID || build.Session != check.Session {
				t.Errorf("ids: %q %q, sessions %q %q", build.ID, check.ID, build.Session, check.Session)
			}
			if build.Client == nil || build.Client.Name != "it" || build.Client.Version != "v1" || !build.Client.Reported {
				t.Errorf("client: %+v", build.Client)
			}
			wantAuth := map[string]string{"stdio": "process", "http": "bearer-token"}[transport]
			if build.Transport != transport || build.Auth != wantAuth {
				t.Errorf("transport %q auth %q", build.Transport, build.Auth)
			}
			p := build.Project
			if p == nil || !strings.HasSuffix(p.Runefile, "Runefile") || !strings.HasPrefix(p.DefinitionDigest, "sha256:") || !strings.HasPrefix(p.ConfigDigest, "sha256:") {
				t.Fatalf("project: %+v", p)
			}
			if p.Git == nil || p.Git.State != "commit" || p.Git.Commit != commit || p.Git.Dirty == nil || *p.Git.Dirty {
				t.Errorf("git of the first call: %+v", p.Git)
			}
			if build.Sandbox == nil || build.Sandbox.Enforced {
				t.Errorf("sandbox: %+v", build.Sandbox)
			}
			if check.Outcome != "failed" || check.ExitCode == nil || *check.ExitCode != 3 {
				t.Errorf("check: %+v", check)
			}

			// An uncommitted change is recorded as a dirty tree.
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			callText(t, cs, "build", nil)
			if g := lastLogCall(t, dir, env).Project.Git; g == nil || g.Dirty == nil || !*g.Dirty || g.Commit != commit {
				t.Errorf("git after a change: %+v", g)
			}
		})
	}
}

// A project that is no repository says so, with no commit invented.
func TestAuditLogOutsideARepository(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", nil)
	g := lastLogCall(t, dir, env).Project.Git
	if g == nil || (g.State != "none" && g.State != "unavailable") || g.Commit != "" || g.Dirty != nil {
		t.Errorf("git: %+v", g)
	}
}

// User Story 1: `rune log` lists the calls of today.
func TestRuneLogListsToday(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", map[string]any{"target": "release"})
	callText(t, cs, "check", nil)
	callText(t, cs, "clean", nil)

	res := run(t, dir, env, "log", "--tz", "UTC", "--since", "1h")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("exit %d, stderr %q", res.code, res.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(res.stdout, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "Agent actions from ") || !strings.Contains(lines[0], "(UTC) for ") || lines[1] != "" {
		t.Errorf("header: %q", lines[:2])
	}
	want := []struct{ outcome, what, exit string }{
		{"completed", `build target="release"`, "exit 0"},
		{"failed", "check", "exit 3"},
		{"refused", "clean", "-"},
	}
	var rows []string
	for _, line := range lines[2:] {
		if !strings.HasPrefix(line, " ") {
			rows = append(rows, line)
		}
	}
	if len(rows) != len(want) {
		t.Fatalf("%d action lines, want %d:\n%s", len(rows), len(want), res.stdout)
	}
	for i, w := range want {
		fields := strings.Fields(rows[i])
		if !strings.Contains(rows[i], "  "+w.outcome+"  ") || !strings.Contains(rows[i], "  "+w.what+"  ") || !strings.Contains(rows[i], "  "+w.exit+"  ") {
			t.Errorf("line %d: %q, want %s, %s, %s", i, rows[i], w.outcome, w.what, w.exit)
		}
		if !strings.Contains(rows[i], "  it v1  ") || len(fields[len(fields)-1]) != 16 {
			t.Errorf("line %d lacks the client or the id: %q", i, rows[i])
		}
	}
	if !strings.Contains(res.stdout, `policy: task "clean" is destructive`) {
		t.Errorf("the refusal has no detail line:\n%s", res.stdout)
	}

	// With no flags the listing is today's.
	today := run(t, dir, env, "log")
	if today.code != 0 || !strings.HasPrefix(today.stdout, "Agent actions on ") {
		t.Errorf("rune log: exit %d, %q", today.code, today.stdout)
	}
}

// Quickstart C, D and E: attempts that ran nothing are recorded, with no
// exit code.
func TestAuditLogAttempts(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, _ := auditProject(t, auditRunefile)
			cs, _ := approvalSession(t, transport, dir, nil, env, nil)

			if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nope"}); err == nil {
				t.Fatal("an unknown tool was called")
			}
			unknown := lastLogCall(t, dir, env)
			if unknown.Outcome != "unknown-tool" || unknown.Tool != "nope" || unknown.Task != "" || unknown.Error == nil || unknown.Error.Kind != "protocol" {
				t.Errorf("unknown tool: %+v", unknown)
			}

			if _, isErr := callText(t, cs, "clean", nil); !isErr {
				t.Fatal("clean was not refused")
			}
			refused := lastLogCall(t, dir, env)
			if refused.Outcome != "refused" || refused.Error == nil || refused.Error.Kind != "policy" || !strings.Contains(refused.Error.Message, "destructive") {
				t.Errorf("refused: %+v", refused)
			}

			if transport == "stdio" {
				callText(t, cs, "build", map[string]any{"colour": "red"})
				undeclared := lastLogCall(t, dir, env)
				if undeclared.Outcome != "invalid-arguments" || undeclared.Error.Kind != "validation" || string(undeclared.Arguments.Supplied) != `{"colour":"red"}` || undeclared.Arguments.Effective != nil {
					t.Errorf("an undeclared argument: %+v", undeclared)
				}
				callText(t, cs, "deploy", map[string]any{"env": "qa"})
				invalid := lastLogCall(t, dir, env)
				if invalid.Outcome != "invalid-arguments" || invalid.Error.Kind != "validation" || !strings.Contains(invalid.Error.Message, `"qa"`) {
					t.Errorf("a value outside the enum: %+v", invalid)
				}
			}

			for _, c := range logCalls(t, dir, env) {
				if c.ExitCode != nil {
					t.Errorf("%s (%s) has exit code %d", c.Tool, c.Outcome, *c.ExitCode)
				}
			}
			for _, marker := range []string{"build.done", "deploy.done", "clean.done"} {
				if hasMarker(dir, marker) {
					t.Errorf("%s exists: a refused call ran", marker)
				}
			}
			text := run(t, dir, env, "log", "--since", "1h").stdout
			if strings.Contains(text, "exit ") {
				t.Errorf("a refusal is listed with an exit code:\n%s", text)
			}
		})
	}
}

// Quickstart L: a call whose start cannot be written is refused.
func TestAuditLogStartFailure(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, _ := auditProject(t, auditRunefile)
			logDir := logDirOf(t, dir, env)
			cs, stderr := approvalSession(t, transport, dir, nil, env, nil)
			breakToday(t, logDir)

			text, isErr := callText(t, cs, "build", nil)
			want := fmt.Sprintf("audit log: this call was not run: the record of its start could not be written (%s is not a regular file). The log directory is %s. The operator must make it writable or set RUNE_AUDIT_DIR.", todayFile(logDir), logDir)
			if !isErr || text != want {
				t.Errorf("result:\n got %q\nwant %q", text, want)
			}
			if hasMarker(dir, "build.done") {
				t.Error("the task ran")
			}
			waitForText(t, stderr, want, 1)
		})
	}
}

// Quickstart M: a completion that cannot be written leaves the result the
// task's own.
func TestAuditLogCompletionFailure(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, _ := auditProject(t, auditRunefile)
			logDir := logDirOf(t, dir, env)
			cs, stderr := approvalSession(t, transport, dir, nil, env, nil)

			type reply struct {
				res *mcp.CallToolResult
				err error
			}
			done := make(chan reply, 1)
			go func() {
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait"})
				done <- reply{res, err}
			}()
			waitForFile(t, filepath.Join(dir, "wait.started"))
			breakToday(t, logDir)
			writeFile(t, filepath.Join(dir, "go.flag"), "go\n")

			r := <-done
			if r.err != nil {
				t.Fatal(r.err)
			}
			if !r.res.IsError || len(r.res.Content) != 2 {
				t.Fatalf("error flag %v, %d content blocks", r.res.IsError, len(r.res.Content))
			}
			first, _ := r.res.Content[0].(*mcp.TextContent)
			second, _ := r.res.Content[1].(*mcp.TextContent)
			if first == nil || !strings.HasSuffix(first.Text, "[exit 3]") {
				t.Errorf("the first block is not the task's result: %+v", first)
			}
			wantNote := "[audit log] The completion of this call was not recorded (" + todayFile(logDir) + " is not a regular file). The result above is the task's own. Do not retry the call because of this message."
			if second == nil || second.Text != wantNote {
				t.Errorf("the second block:\n got %+v\nwant %q", second, wantNote)
			}
			structured, _ := json.Marshal(r.res.StructuredContent)
			if !strings.Contains(string(structured), `"exitCode":3`) {
				t.Errorf("structured content: %s", structured)
			}
			waitForText(t, stderr, "warning: audit log: a record could not be written (", 1)
			if got := readDone(t, dir, "wait.done"); got != "done" {
				t.Errorf("wait.done = %q: the task did not run exactly once", got)
			}
		})
	}
}

// Quickstart K: a server killed during a call leaves the call incomplete.
func TestAuditLogIncompleteAfterKill(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cmd := exec.Command(runeBin, "mcp", "--color=never")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = &syncBuffer{}
	client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	go func() { _, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "slow"}) }()
	waitForFile(t, filepath.Join(dir, "slow.started"))
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()

	calls := logCalls(t, dir, env)
	if len(calls) != 1 {
		t.Fatalf("%d calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Outcome != "incomplete" || c.End != nil || c.ExitCode != nil || c.DurationMs != nil || c.Task != "slow" {
		t.Errorf("the killed call: %+v", c)
	}
	if got := logCalls(t, dir, env, "--outcome", "completed"); len(got) != 0 {
		t.Errorf("--outcome completed lists %d calls", len(got))
	}
	if got := logCalls(t, dir, env, "--outcome", "incomplete"); len(got) != 1 {
		t.Errorf("--outcome incomplete lists %d calls", len(got))
	}
	text := run(t, dir, env, "log", "--since", "1h").stdout
	if !strings.Contains(text, "  incomplete  slow") || !strings.Contains(text, "no completion was recorded: the call is still running or the server stopped") {
		t.Errorf("the listing:\n%s", text)
	}
}

// A request that is not a tool call is served when its record cannot be
// written, and the operator is told once.
func TestAuditLogRequestFailureWarnsOnce(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	logDir := logDirOf(t, dir, env)
	cs, stderr := approvalSession(t, "stdio", dir, nil, env, nil)
	breakToday(t, logDir)
	for range 3 {
		tools, err := cs.ListTools(context.Background(), nil)
		if err != nil || len(tools.Tools) == 0 {
			t.Fatalf("tools/list: %v", err)
		}
	}
	warning := "warning: audit log: a record could not be written ("
	waitForText(t, stderr, warning, 1)
	// The third request has been answered, so every warning is written.
	if got := strings.Count(stderr.String(), warning); got != 1 {
		t.Errorf("%d warnings, want 1:\n%s", got, stderr.String())
	}
}

// filesHolding lists the files under dir that hold needle.
func filesHolding(t *testing.T, dir, needle string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(needle)) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Quickstart I: no secret, token or output reaches the log or its readers.
func TestAuditLogHoldsNoSecret(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			dir, env, base := auditProject(t, auditRunefile)
			cs, _ := approvalSession(t, transport, dir, nil, env, nil)

			callText(t, cs, "build", map[string]any{"target": auditFixture})
			callText(t, cs, "deploy", map[string]any{"env": "staging", "token": "another-private-value"})
			if _, isErr := callText(t, cs, "deploy", map[string]any{"env": auditFixture}); !isErr {
				t.Fatal("a value outside the enum was accepted")
			}
			if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "tool-" + auditFixture}); err == nil {
				t.Fatal("an unknown tool was called")
			}

			for _, needle := range []string{auditFixture, "another-private-value", bindWarningToken, "out-marker-text"} {
				if found := filesHolding(t, base, needle); len(found) != 0 {
					t.Errorf("%q is in %v", needle, found)
				}
				for _, args := range [][]string{{"log", "--since", "1h", "--all"}, {"log", "--since", "1h", "--all", "--json"}} {
					res := run(t, dir, env, args...)
					if strings.Contains(res.stdout+res.stderr, needle) {
						t.Errorf("%q is in the output of rune %v", needle, args)
					}
				}
			}
			calls := logCalls(t, dir, env)
			if len(calls) != 4 {
				t.Fatalf("%d calls, want 4", len(calls))
			}
			if got := string(calls[0].Arguments.Effective); got != `{"target":"***"}` {
				t.Errorf("a masked value: %s", got)
			}
			if got := string(calls[1].Arguments.Supplied); got != `{"env":"staging","token":"***"}` {
				t.Errorf("a secret-named parameter, supplied: %s", got)
			}
			if got := string(calls[1].Arguments.Effective); got != `{"env":"staging","token":"***"}` {
				t.Errorf("a secret-named parameter, effective: %s", got)
			}
			if calls[2].Error == nil || !strings.Contains(calls[2].Error.Message, `"***"`) {
				t.Errorf("the error of an invalid value: %+v", calls[2].Error)
			}
			if calls[3].Tool != "tool-***" {
				t.Errorf("an unknown tool's name: %q", calls[3].Tool)
			}
		})
	}
}

// Quickstart J: a large value is cut, and a record stays small.
func TestAuditLogBoundsValues(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", map[string]any{"target": strings.Repeat("x", 1<<20)})

	data, err := os.ReadFile(todayFile(logDirOf(t, dir, env)))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		if len(line) > audit.MaxRecordBytes {
			t.Errorf("a line of %d bytes", len(line))
		}
	}
	c := lastLogCall(t, dir, env)
	want := `{"target":"` + strings.Repeat("x", 1024) + `[1047552 bytes cut]"}`
	if string(c.Arguments.Supplied) != want || string(c.Arguments.Effective) != want {
		t.Errorf("the stored value was not cut to 1024 bytes: supplied %d bytes, effective %d bytes", len(c.Arguments.Supplied), len(c.Arguments.Effective))
	}
}

// Quickstart P: two server processes and many sessions write one log of
// whole records with distinct ids.
func TestAuditLogConcurrentProcesses(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	sessions := []*mcp.ClientSession{}
	for range 2 {
		cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
		sessions = append(sessions, cs)
	}
	var wg sync.WaitGroup
	for _, cs := range sessions {
		for range 10 {
			wg.Go(func() {
				for range 10 {
					if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "build"}); err != nil {
						t.Errorf("call: %v", err)
					}
				}
			})
		}
	}
	wg.Wait()

	data, err := os.ReadFile(todayFile(logDirOf(t, dir, env)))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		if !json.Valid([]byte(line)) {
			t.Fatalf("a line does not parse: %q", line)
		}
	}
	res := run(t, dir, env, "log", "--json", "--since", "1h")
	if res.stderr != "" {
		t.Errorf("rune log reported: %s", res.stderr)
	}
	calls := logCalls(t, dir, env)
	ids := map[string]bool{}
	sessionsSeen := map[string]bool{}
	for _, c := range calls {
		ids[c.ID] = true
		sessionsSeen[c.Session] = true
		if c.Outcome != "completed" {
			t.Errorf("call %s is %s", c.ID, c.Outcome)
		}
	}
	if len(calls) != 200 || len(ids) != 200 || len(sessionsSeen) != 2 {
		t.Errorf("%d calls, %d ids, %d sessions, want 200, 200 and 2", len(calls), len(ids), len(sessionsSeen))
	}
}

// Quickstart Q: a damaged record hides nothing else.
func TestAuditLogDamagedRecord(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", nil)
	path := todayFile(logDirOf(t, dir, env))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"type":"call","phase":"sta`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	callText(t, cs, "check", nil)

	res := run(t, dir, env, "log", "--json", "--since", "1h")
	if res.code != 0 {
		t.Fatalf("exit %d", res.code)
	}
	if want := "warning: 1 record in " + path + " could not be read and was skipped\n"; res.stderr != want {
		t.Errorf("standard error %q\nwant %q", res.stderr, want)
	}
	calls := logCalls(t, dir, env)
	if len(calls) != 2 || calls[0].Task != "build" || calls[1].Task != "check" || calls[1].Outcome != "failed" {
		t.Errorf("calls around the damage: %+v", calls)
	}
}

// Quickstart F, G and H: the approval and the grant belong to their call.
func TestAuditLogApprovalAndGrant(t *testing.T) {
	t.Run("approved at the client", func(t *testing.T) {
		dir, env, _ := auditProject(t, auditRunefile)
		cs, _ := approvalSession(t, "stdio", dir, nil, env, (&asker{answer: yes}).options())
		if _, isErr := callText(t, cs, "clean", nil); isErr {
			t.Fatal("an approved call was refused")
		}
		c := lastLogCall(t, dir, env)
		if c.Outcome != "completed" || c.Approval == nil || !c.Approval.Asked || c.Approval.Decision != "approved" || strings.Join(c.Approval.Tasks, ",") != "clean" || c.Grants != nil {
			t.Errorf("approved: %+v, approval %+v", c, c.Approval)
		}
		if text := run(t, dir, env, "log", "--since", "1h").stdout; !strings.Contains(text, "approved at the client") {
			t.Errorf("the listing:\n%s", text)
		}
	})
	t.Run("declined", func(t *testing.T) {
		dir, env, _ := auditProject(t, auditRunefile)
		cs, _ := approvalSession(t, "stdio", dir, nil, env, (&asker{answer: no}).options())
		callText(t, cs, "clean", nil)
		c := lastLogCall(t, dir, env)
		if c.Outcome != "refused" || c.ExitCode != nil || c.Approval == nil || c.Approval.Decision != "declined" || c.Error == nil || c.Error.Kind != "approval" {
			t.Errorf("declined: %+v, approval %+v, error %+v", c, c.Approval, c.Error)
		}
		if hasMarker(dir, "clean.done") {
			t.Error("the task ran")
		}
	})
	t.Run("a client that cannot be asked", func(t *testing.T) {
		dir, env, _ := auditProject(t, auditRunefile)
		cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
		callText(t, cs, "clean", nil)
		c := lastLogCall(t, dir, env)
		if c.Outcome != "refused" || c.Approval == nil || c.Approval.Asked || c.Approval.Decision != "cannot-ask" || c.Error.Kind != "policy" {
			t.Errorf("cannot ask: %+v, approval %+v", c, c.Approval)
		}
	})
	t.Run("granted by the operator", func(t *testing.T) {
		dir, env, _ := auditProject(t, auditRunefile)
		cs, _ := approvalSession(t, "stdio", dir, []string{"--allow-destructive", "clean"}, env, nil)
		if _, isErr := callText(t, cs, "clean", nil); isErr {
			t.Fatal("a granted call was refused")
		}
		c := lastLogCall(t, dir, env)
		if c.Outcome != "completed" || strings.Join(c.Grants, ",") != "clean" || c.Approval != nil {
			t.Errorf("granted: %+v", c)
		}
		if text := run(t, dir, env, "log", "--since", "1h").stdout; !strings.Contains(text, "granted by the operator: clean") {
			t.Errorf("the listing:\n%s", text)
		}
	})
}

// The plan and apply calls of spec 051 name their plan, and the plan
// journal carries their ids. No record names a person.
func TestAuditLogPlanApply(t *testing.T) {
	dir := planProject(t, planApplyRunefile)
	base := t.TempDir()
	env := []string{"RUNE_AUDIT_DIR=" + base}
	cs, _ := approvalSession(t, "stdio", dir, nil, env, (&asker{answer: yes}).options())
	if text, isErr := callText(t, cs, "plan", prodArgs); isErr {
		t.Fatalf("plan: %s", text)
	}
	if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
		t.Fatalf("apply: %s", text)
	}
	calls := logCalls(t, dir, env)
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2", len(calls))
	}
	planCall, applyCall := calls[0], calls[1]
	rec := storedPlans(t, dir)[0]
	digest := "sha256:" + rec.Artifact.SHA256
	if planCall.Plan == nil || *planCall.Plan != (audit.Plan{Role: "stored", ID: rec.ID, Digest: digest}) {
		t.Errorf("plan of the plan call: %+v", planCall.Plan)
	}
	if applyCall.Plan == nil || *applyCall.Plan != (audit.Plan{Role: "applied", ID: rec.ID, Digest: digest}) {
		t.Errorf("plan of the apply call: %+v", applyCall.Plan)
	}
	if applyCall.Approval == nil || applyCall.Approval.Decision != "approved" {
		t.Errorf("approval of the apply call: %+v", applyCall.Approval)
	}

	journal, err := os.ReadFile(filepath.Join(plansDir(dir), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(journal)) {
		var ev planstore.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		want := applyCall.ID
		if ev.Event == planstore.EventPlanCreated {
			want = planCall.ID
		}
		if ev.Invocation != want {
			t.Errorf("%s carries invocation %q, want %q", ev.Event, ev.Invocation, want)
		}
	}

	raw := run(t, dir, env, "log", "--json", "--since", "1h", "--all").stdout
	for _, field := range []string{`"user"`, `"person"`, `"principal"`, `"human"`} {
		if strings.Contains(raw, field) {
			t.Errorf("a record has a field %s", field)
		}
	}
	if strings.Count(raw, `"sandbox":{"enforced":false}`) != 2 {
		t.Errorf("not every call states the sandbox:\n%s", raw)
	}
	for _, needle := range []string{"plan env=prod serial=1", "Plan: 1 to add for prod"} {
		if found := filesHolding(t, base, needle); len(found) != 0 {
			t.Errorf("plan or summary bytes are in %v", found)
		}
	}
}

// Quickstart N and O: a refused bearer token leaves a minimal record, and
// a flood leaves a bounded number of them.
func TestAuditLogHTTPAuthFailure(t *testing.T) {
	dir, env, base := auditProject(t, auditRunefile)
	addr := net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &syncBuffer{}
	cmd := exec.CommandContext(ctx, runeBin, "serve", "--color=never", "--http", "--addr", addr, "--token-file", writeToken(t, t.TempDir()))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	waitUnauthorized(t, addr, stderr)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for range 30 {
		req, err := http.NewRequest(http.MethodPost, "http://"+addr, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wrong-token-value")
		req.Header.Set("X-Client", "asserted-client-name")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}

	var failures []audit.Action
	for _, a := range logActions(t, dir, env) {
		if a.Type == audit.TypeAuthFailure {
			failures = append(failures, a)
		}
	}
	if len(failures) == 0 || len(failures) > 10 {
		t.Fatalf("%d authentication failures recorded, want 1 to 10", len(failures))
	}
	for _, f := range failures {
		if !strings.HasPrefix(f.Remote, "127.0.0.1:") || f.Client != nil || f.Session != "" || f.Transport != "http" || f.Outcome != "" {
			t.Errorf("record: %+v", f)
		}
	}
	for _, needle := range []string{"wrong-token-value", "asserted-client-name", bindWarningToken} {
		if found := filesHolding(t, base, needle); len(found) != 0 {
			t.Errorf("%q is in %v", needle, found)
		}
	}
	if text := run(t, dir, env, "log", "--since", "1h").stdout; !strings.Contains(text, "  auth-failure  127.0.0.1:") {
		t.Errorf("the listing:\n%s", text)
	}
}

// Requests that are not tool calls are recorded, and listed with --all.
func TestAuditLogRequests(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	callText(t, cs, "build", nil)
	if got := logActions(t, dir, env); len(got) != 1 || got[0].Type != audit.TypeCall {
		t.Errorf("without --all: %+v", got)
	}
	methods := map[string]audit.Action{}
	session := ""
	for _, a := range logActions(t, dir, env, "--all") {
		if a.Type == audit.TypeRequest {
			methods[a.Method] = a
		} else {
			session = a.Session
		}
	}
	for _, method := range []string{"initialize", "tools/list"} {
		a, ok := methods[method]
		if !ok {
			t.Errorf("no record of %s", method)
			continue
		}
		if a.Session != session || a.Outcome != "ok" || a.Arguments != nil || a.ExitCode != nil || a.Client == nil || a.Client.Name != "it" {
			t.Errorf("%s: %+v", method, a)
		}
	}
}

// helperAgentVar marks a run of this test binary as the stand-in agent of
// TestAuditLogAgentEndpoint.
const helperAgentVar = "RUNE_IT_HELPER_AGENT"

// helperAgent is the stand-in agent: it calls one tool on the endpoint the
// agent task opened for it and prints what the tool returned.
func helperAgent() int {
	transport := &mcp.StreamableClientTransport{
		Endpoint:             os.Getenv("RUNE_MCP_URL"),
		HTTPClient:           &http.Client{Transport: reloadBearer{os.Getenv("RUNE_MCP_TOKEN")}},
		DisableStandaloneSSE: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "stub-agent", Version: "0.1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub agent:", err)
		return 1
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "build"})
	if err != nil || res.IsError {
		fmt.Fprintln(os.Stderr, "stub agent: the call failed:", err)
		return 1
	}
	fmt.Println("agent done")
	return 0
}

// Quickstart S: the run of an agent task is the parent of the calls its
// agent makes through the endpoint.
func TestAuditLogAgentEndpoint(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runefile := "set agent_cmd := [\"" + filepath.ToSlash(self) + "\"]\n\n" +
		"# Build.\nbuild:\n    @echo built >> build.done\n\n" +
		"# Review with the agent.\nreview (agent):\n    Review the project.\n"
	dir, env, _ := auditProject(t, runefile)
	env = append(env, helperAgentVar+"=1")

	res := run(t, dir, env, "review")
	if res.code != 0 || !strings.Contains(res.stdout, "agent done") {
		t.Fatalf("rune review: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
	}
	if !hasMarker(dir, "build.done") {
		t.Fatal("the agent's call did not run the task")
	}
	var runAction, call *audit.Action
	for _, a := range logActions(t, dir, env) {
		switch a.Type {
		case audit.TypeAgentRun:
			runAction = &a
		case audit.TypeCall:
			call = &a
		}
	}
	if runAction == nil || call == nil {
		t.Fatalf("the run or the call is not in the log: %v, %v", runAction, call)
	}
	if runAction.Task != "review" || runAction.Outcome != "completed" || runAction.ExitCode == nil || *runAction.ExitCode != 0 || runAction.End == nil || runAction.Parent != "" {
		t.Errorf("run: %+v", runAction)
	}
	if call.Parent != runAction.ID || call.Transport != "agent-endpoint" || call.Task != "build" || call.Outcome != "completed" || call.Client == nil || call.Client.Name != "stub-agent" {
		t.Errorf("call: %+v", call)
	}
	text := run(t, dir, env, "log", "--since", "1h").stdout
	if !strings.Contains(text, "  completed  agent review  ") || !strings.Contains(text, "parent "+runAction.ID) {
		t.Errorf("the listing:\n%s", text)
	}

	// A body of an agent's call that starts the agent task names that call
	// as the parent of the run.
	nested := append(env, "RUNE_INVOCATION_ID=0123456789abcdef")
	if res := run(t, dir, nested, "review"); res.code != 0 {
		t.Fatalf("nested review: exit %d, %s", res.code, res.stderr)
	}
	var parents []string
	for _, a := range logActions(t, dir, env) {
		if a.Type == audit.TypeAgentRun {
			parents = append(parents, a.Parent)
		}
	}
	if len(parents) != 2 || parents[1] != "0123456789abcdef" {
		t.Errorf("parents of the runs: %q", parents)
	}
}

// When the run cannot be recorded, the agent task runs without its
// endpoint and says so.
func TestAuditLogAgentRunNotRecorded(t *testing.T) {
	runefile := "set agent_cmd := [\"sh\", \"-c\", \"printf 'url=%s' \\\"$RUNE_MCP_URL\\\"\", \"stub\"]\n\n" +
		"build:\n    @echo built\n\nreview (agent):\n    Review.\n"
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in agent needs a POSIX shell")
	}
	dir, env, _ := auditProject(t, runefile)
	res := run(t, dir, append(env, "RUNE_AUDIT_RETENTION_DAYS=soon"), "review")
	if res.code != 0 || res.stdout != "url=\n" {
		t.Errorf("exit %d, stdout %q", res.code, res.stdout)
	}
	want := `warning: audit log: the agent run could not be recorded (RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more); the agent runs without task tools`
	if !strings.Contains(res.stderr, want) {
		t.Errorf("standard error %q", res.stderr)
	}
}

// runServer runs `rune mcp` to its end with an empty standard input.
func runServer(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, runeBin, append([]string{"mcp", "--color=never"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader("")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

// Quickstart T: where the log is, who may read it, and how long it is kept.
func TestAuditLogLocation(t *testing.T) {
	dir, env, base := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", nil)

	logDir := logDirOf(t, dir, env)
	if filepath.Dir(logDir) != base || strings.HasPrefix(logDir, dir) {
		t.Errorf("the log is in %q, want a directory under %q and outside the project", logDir, base)
	}
	if _, err := os.Stat(todayFile(logDir)); err != nil {
		t.Fatalf("the day file is not in the directory rune log --dir names: %v", err)
	}
	if hasMarker(dir, ".rune") {
		t.Error("the call wrote under .rune in the project")
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{logDir: 0o700, todayFile(logDir): 0o600, filepath.Join(logDir, "project.json"): 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Errorf("%s has mode %o, want %o", path, info.Mode().Perm(), want)
			}
		}
	}

	t.Run("a log inside the project", func(t *testing.T) {
		dir := writeRunefile(t, "[context]\nhealth:\n    @echo ran > context.done\n\nbuild:\n    @echo hi\n")
		inside := filepath.Join(dir, ".audit")
		res := runServer(t, dir, []string{"RUNE_AUDIT_DIR=" + inside})
		if res.code != 2 || !strings.Contains(res.stderr, "rune: audit log: ") || !strings.Contains(res.stderr, "is inside the project (") || !strings.Contains(res.stderr, "set RUNE_AUDIT_DIR to a directory outside it") {
			t.Errorf("exit %d, stderr %q", res.code, res.stderr)
		}
		if hasMarker(dir, "context.done") || hasMarker(dir, ".audit") {
			t.Error("the server ran Runefile code or created the directory")
		}
	})
	t.Run("a retention that is not a number of days", func(t *testing.T) {
		dir := writeRunefile(t, "build:\n    @echo hi\n")
		res := runServer(t, dir, []string{"RUNE_AUDIT_DIR=" + t.TempDir(), "RUNE_AUDIT_RETENTION_DAYS=soon"})
		want := `rune: audit log: RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more`
		if res.code != 2 || !strings.Contains(res.stderr, want) {
			t.Errorf("exit %d, stderr %q", res.code, res.stderr)
		}
	})
	t.Run("a relative log directory", func(t *testing.T) {
		dir := writeRunefile(t, "build:\n    @echo hi\n")
		want := `rune: audit log: RUNE_AUDIT_DIR must be an absolute path, not "audit"`
		if res := runServer(t, dir, []string{"RUNE_AUDIT_DIR=audit"}); res.code != 2 || !strings.Contains(res.stderr, want) {
			t.Errorf("rune mcp: exit %d, stderr %q", res.code, res.stderr)
		}
		if res := run(t, dir, []string{"RUNE_AUDIT_DIR=audit"}, "log"); res.code != 2 || !strings.Contains(res.stderr, want) {
			t.Errorf("rune log: exit %d, stderr %q", res.code, res.stderr)
		}
		if hasMarker(dir, "audit") {
			t.Error("the relative directory was created")
		}
	})
	t.Run("old files are removed at start", func(t *testing.T) {
		old := filepath.Join(logDir, time.Now().UTC().AddDate(0, 0, -100).Format("2006-01-02")+".jsonl")
		yesterday := filepath.Join(logDir, time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")+".jsonl")
		for _, path := range []string{old, yesterday} {
			writeFile(t, path, "{}\n")
		}
		if res := runServer(t, dir, env); res.code != 0 {
			t.Fatalf("exit %d, stderr %q", res.code, res.stderr)
		}
		if _, err := os.Stat(old); err == nil {
			t.Error("a file 100 days old was kept")
		}
		if _, err := os.Stat(yesterday); err != nil {
			t.Error("yesterday's file was removed")
		}
	})
}
