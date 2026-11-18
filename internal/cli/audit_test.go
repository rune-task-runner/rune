package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/runtime/agent"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 052, issue #50: the audit log of a project, as the rune command
// writes it.

var auditStart = time.Date(2026, 11, 18, 10, 4, 11, 0, time.UTC)

// testAuditor is an auditor on a log in a temporary directory, with the
// operator's standard error in a buffer.
func testAuditor(t *testing.T) (*auditor, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	log, err := audit.Open(audit.Options{Base: t.TempDir(), Runefile: "/repo/Runefile"})
	if err != nil {
		t.Fatal(err)
	}
	return &auditor{log: log, warn: newAuditWarnings(&stderr, "warning")}, &stderr
}

func auditActions(t *testing.T, dir string) []audit.Action {
	t.Helper()
	actions, damage, err := audit.Read(dir, time.Time{}, time.Time{})
	if err != nil || len(damage) != 0 {
		t.Fatalf("read the log: damage %v, err %v", damage, err)
	}
	return actions
}

func dayFileOf(dir string, at time.Time) string {
	return filepath.Join(dir, at.UTC().Format("2006-01-02")+".jsonl")
}

// breakDay puts a directory where the day file of at belongs. The tests run
// as root in Docker, where a read-only directory stops nothing.
func breakDay(t *testing.T, dir string, at time.Time) {
	t.Helper()
	path := dayFileOf(dir, at)
	if err := os.Rename(path, path+".kept"); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func sampleCall() mcpserver.AuditCall {
	dirty := true
	return mcpserver.AuditCall{
		ID:    "b81e04f7a2c9d310",
		Start: auditStart,
		Session: mcpserver.AuditSession{
			ID: "5f0e7a11c3d2b694", Transport: "http", Auth: "bearer-token",
			ClientName: "claude-code", ClientVersion: "2.1.0",
		},
		Tool:     "mod__deploy",
		Task:     "mod::deploy",
		Supplied: map[string]any{"env": "prod", "note": "a<b>&c"},
		Identity: mcpserver.AuditIdentity{
			Runefile: "/repo/Runefile", DefinitionDigest: "41aa", ConfigDigest: "77c0",
			GitState: "commit", GitCommit: "07afc00", GitDirty: &dirty,
		},
	}
}

func TestAuditorWritesRecords(t *testing.T) {
	a, stderr := testAuditor(t)
	call := sampleCall()
	if err := a.CallStarted(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	exit := 3
	end := mcpserver.AuditEnd{
		Time: auditStart.Add(2500 * time.Millisecond), Duration: 2500 * time.Millisecond,
		Outcome: mcpserver.AuditFailed, ExitCode: &exit,
		Effective: []mcpserver.AuditArg{
			{Name: "env", Values: []string{"prod"}},
			{Name: "files", Values: []string{"a b", "c"}, Variadic: true},
			{Name: "none", Variadic: true},
			{Name: "empty"},
		},
		Approval: &mcpserver.AuditApproval{Asked: true, Decision: "approved", Tasks: []string{"mod::deploy"}},
		Grants:   []string{"clean"},
		Plan:     &mcpserver.AuditPlan{Role: "applied", ID: "7f3c", Digest: "9b1f"},
	}
	if err := a.CallEnded(context.Background(), call, end); err != nil {
		t.Fatal(err)
	}
	a.Request(context.Background(), mcpserver.AuditRequest{
		Time: auditStart.Add(time.Minute), Duration: 3 * time.Millisecond, Session: call.Session, Method: "tools/list",
	})
	a.Request(context.Background(), mcpserver.AuditRequest{
		Time: auditStart.Add(2 * time.Minute), Session: call.Session, Method: "prompts/get", Failed: true, Error: "unknown prompt",
	})
	a.AuthFailed(context.Background(), mcpserver.AuditAuthFailure{Time: auditStart.Add(3 * time.Minute), Remote: "127.0.0.1:5123", Suppressed: 4})
	if stderr.Len() != 0 {
		t.Errorf("standard error: %q", stderr.String())
	}

	actions := auditActions(t, a.log.Dir())
	if len(actions) != 4 {
		t.Fatalf("%d actions, want 4", len(actions))
	}
	c := actions[0]
	if c.Type != audit.TypeCall || c.ID != call.ID || !c.Start.Equal(auditStart) || c.End == nil || !c.End.Equal(end.Time) {
		t.Errorf("call: %+v", c)
	}
	if c.DurationMs == nil || *c.DurationMs != 2500 || c.Outcome != "failed" || c.ExitCode == nil || *c.ExitCode != 3 {
		t.Errorf("end of the call: %+v", c)
	}
	if c.Session != "5f0e7a11c3d2b694" || c.Transport != "http" || c.Auth != "bearer-token" || c.Parent != "" {
		t.Errorf("session of the call: %+v", c)
	}
	if c.Client == nil || c.Client.Name != "claude-code" || c.Client.Version != "2.1.0" || !c.Client.Reported {
		t.Errorf("client: %+v", c.Client)
	}
	if c.Tool != "mod__deploy" || c.Task != "mod::deploy" {
		t.Errorf("tool %q task %q", c.Tool, c.Task)
	}
	if got := string(c.Arguments.Supplied); got != `{"env":"prod","note":"a<b>&c"}` {
		t.Errorf("supplied %s", got)
	}
	if got := string(c.Arguments.Effective); got != `{"env":"prod","files":["a b","c"],"none":[],"empty":""}` {
		t.Errorf("effective %s", got)
	}
	wantProject := audit.Project{Runefile: "/repo/Runefile", DefinitionDigest: "sha256:41aa", ConfigDigest: "sha256:77c0"}
	if c.Project == nil || c.Project.Runefile != wantProject.Runefile || c.Project.DefinitionDigest != wantProject.DefinitionDigest || c.Project.ConfigDigest != wantProject.ConfigDigest {
		t.Errorf("project: %+v", c.Project)
	}
	if g := c.Project.Git; g == nil || g.State != "commit" || g.Commit != "07afc00" || g.Dirty == nil || !*g.Dirty {
		t.Errorf("git: %+v", c.Project.Git)
	}
	if c.Sandbox == nil || c.Sandbox.Enforced {
		t.Errorf("sandbox: %+v", c.Sandbox)
	}
	if c.Approval == nil || !c.Approval.Asked || c.Approval.Decision != "approved" || !reflect.DeepEqual(c.Approval.Tasks, []string{"mod::deploy"}) {
		t.Errorf("approval: %+v", c.Approval)
	}
	if !reflect.DeepEqual(c.Grants, []string{"clean"}) || c.Plan == nil || *c.Plan != (audit.Plan{Role: "applied", ID: "7f3c", Digest: "sha256:9b1f"}) {
		t.Errorf("grants %v plan %+v", c.Grants, c.Plan)
	}
	if c.Error != nil {
		t.Errorf("a run has an error: %+v", c.Error)
	}

	list := actions[1]
	if list.Type != audit.TypeRequest || list.Method != "tools/list" || list.Outcome != "ok" || list.Error != nil || list.Session != call.Session.ID || len(list.ID) != 16 {
		t.Errorf("request: %+v", list)
	}
	if list.Arguments != nil || list.ExitCode != nil || list.DurationMs == nil || *list.DurationMs != 3 {
		t.Errorf("a request has arguments or an exit code: %+v", list)
	}
	failed := actions[2]
	if failed.Outcome != "error" || failed.Error == nil || failed.Error.Kind != "protocol" || failed.Error.Message != "unknown prompt" {
		t.Errorf("failed request: %+v", failed)
	}
	auth := actions[3]
	if auth.Type != audit.TypeAuthFailure || auth.Remote != "127.0.0.1:5123" || auth.Suppressed != 4 || auth.Transport != "http" || auth.Client != nil || auth.Session != "" {
		t.Errorf("authentication failure: %+v", auth)
	}
}

func TestAuditorRefusalHasNoExitCode(t *testing.T) {
	a, _ := testAuditor(t)
	call := sampleCall()
	call.Supplied = nil
	call.Identity = mcpserver.AuditIdentity{}
	call.Session.ClientName, call.Session.ClientVersion = "", ""
	if err := a.CallStarted(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	end := mcpserver.AuditEnd{Time: auditStart, Outcome: mcpserver.AuditRefused, ErrorKind: mcpserver.AuditKindPolicy, Error: "task is destructive"}
	if err := a.CallEnded(context.Background(), call, end); err != nil {
		t.Fatal(err)
	}
	c := auditActions(t, a.log.Dir())[0]
	if c.ExitCode != nil || c.Arguments != nil || c.Client != nil || c.Project != nil || c.Approval != nil || c.Plan != nil {
		t.Errorf("absent parts were written: %+v", c)
	}
	if c.Error == nil || *c.Error != (audit.Error{Kind: "policy", Message: "task is destructive"}) || c.Outcome != "refused" {
		t.Errorf("error %+v outcome %q", c.Error, c.Outcome)
	}
	data, err := os.ReadFile(dayFileOf(a.log.Dir(), auditStart))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "exitCode") {
		t.Errorf("a refused call stores an exit code: %s", data)
	}
}

func TestAuditorStartFailure(t *testing.T) {
	a, stderr := testAuditor(t)
	breakDay(t, a.log.Dir(), auditStart)
	err := a.CallStarted(context.Background(), sampleCall())
	want := fmt.Sprintf("audit log: this call was not run: the record of its start could not be written (%s is not a regular file). The log directory is %s. The operator must make it writable or set RUNE_AUDIT_DIR.",
		dayFileOf(a.log.Dir(), auditStart), a.log.Dir())
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	if stderr.String() != want+"\n" {
		t.Errorf("standard error %q", stderr.String())
	}
}

func TestAuditorEndFailure(t *testing.T) {
	a, stderr := testAuditor(t)
	call := sampleCall()
	if err := a.CallStarted(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	breakDay(t, a.log.Dir(), auditStart)
	reason := dayFileOf(a.log.Dir(), auditStart) + " is not a regular file"
	for range 2 {
		err := a.CallEnded(context.Background(), call, mcpserver.AuditEnd{Time: auditStart, Outcome: mcpserver.AuditCompleted})
		want := "[audit log] The completion of this call was not recorded (" + reason + "). The result above is the task's own. Do not retry the call because of this message."
		if err == nil || err.Error() != want {
			t.Fatalf("got  %v\nwant %s", err, want)
		}
	}
	line := "warning: audit log: a record could not be written (" + reason + "); the log directory is " + a.log.Dir() + "\n"
	if stderr.String() != line+line {
		t.Errorf("standard error %q, want the warning for each call", stderr.String())
	}
}

func TestAuditorWarnsOnce(t *testing.T) {
	a, stderr := testAuditor(t)
	breakDay(t, a.log.Dir(), auditStart)
	request := mcpserver.AuditRequest{Time: auditStart, Method: "tools/list"}
	a.Request(context.Background(), request)
	a.Request(context.Background(), request)
	a.AuthFailed(context.Background(), mcpserver.AuditAuthFailure{Time: auditStart})
	if got := strings.Count(stderr.String(), "a record could not be written"); got != 1 {
		t.Fatalf("%d warnings, want 1: %q", got, stderr.String())
	}
	// A write that succeeds, on another day, arms the warning again.
	other := auditStart.AddDate(0, 0, 1)
	a.Request(context.Background(), mcpserver.AuditRequest{Time: other, Method: "tools/list"})
	a.Request(context.Background(), request)
	if got := strings.Count(stderr.String(), "a record could not be written"); got != 2 {
		t.Errorf("%d warnings after a success, want 2: %q", got, stderr.String())
	}
}

func TestAuditWarnings(t *testing.T) {
	var buf bytes.Buffer
	w := newAuditWarnings(&buf, "warning")
	w.prune(errors.New("busy"))
	if got := buf.String(); got != "warning: audit log: an old day file could not be removed (busy)\n" {
		t.Errorf("prune warning %q", got)
	}
	quiet := newAuditWarnings(nil, "warning")
	quiet.line("dropped")
	quiet.notWritten(errors.New("x"), "/dir")
	quiet.wrote()
}

func TestAuditorTransportAndParent(t *testing.T) {
	a, _ := testAuditor(t)
	a.transport, a.parent = auditTransportAgent, "0123456789abcdef"
	call := sampleCall()
	if err := a.CallStarted(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	a.Request(context.Background(), mcpserver.AuditRequest{Time: auditStart, Session: call.Session, Method: "tools/list"})
	stdio := call
	stdio.ID = "ffffffffffffffff"
	stdio.Session.Transport = "stdio"
	if err := a.CallStarted(context.Background(), stdio); err != nil {
		t.Fatal(err)
	}
	actions := auditActions(t, a.log.Dir())
	for _, got := range actions[:2] {
		if got.Transport != "agent-endpoint" || got.Parent != "0123456789abcdef" {
			t.Errorf("%s: transport %q parent %q", got.Type, got.Transport, got.Parent)
		}
	}
	if actions[2].Transport != "stdio" {
		t.Errorf("a transport that is not http was replaced: %q", actions[2].Transport)
	}
}

func TestAuditRetention(t *testing.T) {
	for value, want := range map[string]int{"": 90, "0": 0, "30": 30} {
		if got, err := auditRetention(value); err != nil || got != want {
			t.Errorf("auditRetention(%q) = %d, %v, want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"soon", "-1", "1.5", "7d"} {
		_, err := auditRetention(value)
		want := fmt.Sprintf("RUNE_AUDIT_RETENTION_DAYS: %q is not a whole number of days, zero or more", value)
		if err == nil || err.Error() != want {
			t.Errorf("auditRetention(%q) = %v, want %s", value, err, want)
		}
	}
}

func TestAuditBase(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "rune-audit")
	if got, err := auditBase(Options{AuditDir: abs}); err != nil || got != abs {
		t.Errorf("the operator's directory: %q, %v", got, err)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got, err := auditBase(Options{}); err != nil || got != filepath.Join(state, "rune", "audit") {
		t.Errorf("the state directory: %q, %v", got, err)
	}
	// A relative path would name another directory in every process.
	_, err := auditBase(Options{AuditDir: filepath.Join("..", "audit")})
	var usage *UsageError
	if !errors.As(err, &usage) || err.Error() != fmt.Sprintf("RUNE_AUDIT_DIR must be an absolute path, not %q", filepath.Join("..", "audit")) {
		t.Errorf("a relative directory: %v", err)
	}
}

func TestOpenAuditLog(t *testing.T) {
	project := t.TempDir()
	runefile := filepath.Join(project, "Runefile")

	t.Run("opens under the base", func(t *testing.T) {
		base := t.TempDir()
		log, err := openAuditLog(Options{AuditDir: base, AuditRetention: "0"}, runefile, nil, project)
		if err != nil {
			t.Fatal(err)
		}
		if log.Dir() != audit.ProjectDir(base, runefile) {
			t.Errorf("directory %q", log.Dir())
		}
	})
	t.Run("a bad retention", func(t *testing.T) {
		_, err := openAuditLog(Options{AuditDir: t.TempDir(), AuditRetention: "soon"}, runefile, nil, project)
		var usage *UsageError
		want := `audit log: RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more`
		if !errors.As(err, &usage) || err.Error() != want {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a relative base", func(t *testing.T) {
		_, err := openAuditLog(Options{AuditDir: "audit"}, runefile, nil, project)
		var usage *UsageError
		if !errors.As(err, &usage) || err.Error() != `audit log: RUNE_AUDIT_DIR must be an absolute path, not "audit"` {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a base inside the project", func(t *testing.T) {
		base := filepath.Join(project, ".audit")
		_, err := openAuditLog(Options{AuditDir: base}, runefile, nil, project)
		var usage *UsageError
		want := fmt.Sprintf("audit log: %s is inside the project (%s); set RUNE_AUDIT_DIR to a directory outside it", audit.ProjectDir(base, runefile), project)
		if !errors.As(err, &usage) || err.Error() != want {
			t.Errorf("got  %v\nwant %s", err, want)
		}
		if _, statErr := os.Stat(base); statErr == nil {
			t.Error("the refused directory was created")
		}
	})
	t.Run("no state directory", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		if runtime.GOOS == "windows" {
			t.Setenv("LocalAppData", "")
			t.Setenv("USERPROFILE", "")
		}
		_, err := openAuditLog(Options{}, runefile, nil, project)
		var usage *UsageError
		if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), "audit log: no state directory") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("one project reached by two paths has one log", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic links need a privilege on Windows")
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(project, link); err != nil {
			t.Fatal(err)
		}
		direct := runefilePath(nil, project)
		through := runefilePath(nil, link)
		if direct != through {
			t.Errorf("paths %q and %q", direct, through)
		}
	})
}

func TestEffectiveJSON(t *testing.T) {
	if got := effectiveJSON(nil); got != nil {
		t.Errorf("no arguments: %s", got)
	}
	got := effectiveJSON([]mcpserver.AuditArg{{Name: "z", Values: []string{"<1>"}}, {Name: "a", Values: []string{"x", "y"}, Variadic: true}})
	if string(got) != `{"z":"<1>","a":["x","y"]}` {
		t.Errorf("got %s", got)
	}
}

func TestJSONText(t *testing.T) {
	if got := string(jsonText(map[string]any{"a": "<&>"})); got != `{"a":"<&>"}` {
		t.Errorf("got %s", got)
	}
	if got := jsonText(func() {}); got != nil {
		t.Errorf("a value that does not marshal: %s", got)
	}
}

func TestDigestText(t *testing.T) {
	if digestText("") != "" || digestText("ab") != "sha256:ab" {
		t.Errorf("digestText: %q %q", digestText(""), digestText("ab"))
	}
}

func TestAuditProject(t *testing.T) {
	if got := auditProject(mcpserver.AuditIdentity{}); got != nil {
		t.Errorf("an empty identity: %+v", got)
	}
	got := auditProject(mcpserver.AuditIdentity{Runefile: "/r"})
	if got == nil || got.Runefile != "/r" || got.Git != nil || got.DefinitionDigest != "" {
		t.Errorf("an identity with no git state: %+v", got)
	}
}

func TestAuditClient(t *testing.T) {
	if got := auditClient(mcpserver.AuditSession{}); got != nil {
		t.Errorf("no client: %+v", got)
	}
	if got := auditClient(mcpserver.AuditSession{ClientVersion: "1"}); got == nil || got.Version != "1" {
		t.Errorf("a version alone: %+v", got)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()

	t.Run("a commit and a clean tree, then a change", func(t *testing.T) {
		dir := t.TempDir()
		gitCheckout(t, dir)
		state, commit, dirty := gitState(ctx, dir)
		if state != "commit" || len(commit) < 40 || dirty == nil || *dirty {
			t.Errorf("clean: %q %q %v", state, commit, dirty)
		}
		if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte("build:\n    @echo changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		state, again, dirty := gitState(ctx, dir)
		if state != "commit" || again != commit || dirty == nil || !*dirty {
			t.Errorf("changed: %q %q %v", state, again, dirty)
		}
	})
	t.Run("a repository with no commit", func(t *testing.T) {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q")
		state, commit, dirty := gitState(ctx, dir)
		if state != "unborn" || commit != "" || dirty == nil || *dirty {
			t.Errorf("unborn: %q %q %v", state, commit, dirty)
		}
	})
	t.Run("a plain directory", func(t *testing.T) {
		// Above the temporary directory there may be a repository only when
		// the tests run inside one, which a temporary directory is not.
		state, commit, dirty := gitState(ctx, t.TempDir())
		if state != "none" || commit != "" || dirty != nil {
			t.Errorf("plain: %q %q %v", state, commit, dirty)
		}
	})
	t.Run("inside the git directory", func(t *testing.T) {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q")
		if state, _, _ := gitState(ctx, filepath.Join(dir, ".git")); state != "none" {
			t.Errorf("inside .git: %q", state)
		}
	})
	t.Run("no git on the path", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		state, commit, dirty := gitState(ctx, t.TempDir())
		if state != "unavailable" || commit != "" || dirty != nil {
			t.Errorf("no git: %q %q %v", state, commit, dirty)
		}
	})
	t.Run("a context that ended", func(t *testing.T) {
		ended, cancel := context.WithCancel(ctx)
		cancel()
		if state, _, _ := gitState(ended, t.TempDir()); state != "unavailable" {
			t.Errorf("an ended context: %q", state)
		}
	})
}

// agentAuditEngine is an engine for a Runefile with a task and an agent
// task, in a project whose audit log goes to a temporary directory.
func agentAuditEngine(t *testing.T) (*engine, *bytes.Buffer, string) {
	t.Helper()
	eng, _, stderr := confirmEngine(t, "build target=\"debug\":\n    @echo built {{target}} > build.done\n", nil, false, false)
	base := t.TempDir()
	eng.opts.AuditDir = base
	eng.opts.AuditRetention = "0"
	eng.workDir = eng.root
	eng.planIdent = planIdentity{runefile: filepath.Join(eng.root, "Runefile"), def: "aa", config: "bb"}
	return eng, stderr, audit.ProjectDir(base, eng.planIdent.runefile)
}

type bearerToken struct{ token string }

func (b bearerToken) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// An agent task's run is the parent of the calls its agent makes through
// the endpoint.
func TestAgentRunRecorded(t *testing.T) {
	eng, stderr, dir := agentAuditEngine(t)
	eng.invocation = "aaaaaaaaaaaaaaaa"
	adapter := eng.newAgentAdapter()
	adapter.gitState = func(context.Context, string) (string, string, *bool) { return "none", "", nil }

	var opts agent.Options
	stop, finish := eng.openAgentEndpoint("review", adapter, &opts)
	if opts.MCPURL == "" || opts.MCPToken == "" {
		t.Fatalf("no endpoint: %q", stderr.String())
	}
	started := auditActions(t, dir)
	if len(started) != 1 || started[0].Type != audit.TypeAgentRun || started[0].Outcome != audit.OutcomeIncomplete {
		t.Fatalf("before the agent starts: %+v", started)
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint:             opts.MCPURL,
		HTTPClient:           &http.Client{Transport: bearerToken{opts.MCPToken}},
		DisableStandaloneSSE: true,
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "stub-agent", Version: "0.1"}, nil).Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "build"})
	if err != nil || res.IsError {
		t.Fatalf("call: %v, %s", err, resultText(res))
	}
	_ = cs.Close()
	stop()
	finish(nil)

	var run, call *audit.Action
	for _, a := range auditActions(t, dir) {
		switch a.Type {
		case audit.TypeAgentRun:
			run = &a
		case audit.TypeCall:
			call = &a
		}
	}
	if run == nil || call == nil {
		t.Fatal("the run or the call is not in the log")
	}
	if run.Task != "review" || run.Parent != "aaaaaaaaaaaaaaaa" || run.Outcome != "completed" || run.ExitCode == nil || *run.ExitCode != 0 || run.End == nil {
		t.Errorf("run: %+v", run)
	}
	if run.Project == nil || run.Project.DefinitionDigest != "sha256:aa" || run.Project.Git == nil || run.Project.Git.State != "none" {
		t.Errorf("project of the run: %+v", run.Project)
	}
	if call.Parent != run.ID || call.Transport != "agent-endpoint" || call.Task != "build" || call.Outcome != "completed" {
		t.Errorf("call: %+v", call)
	}
	if call.Client == nil || call.Client.Name != "stub-agent" {
		t.Errorf("client of the call: %+v", call.Client)
	}
	if got := string(call.Arguments.Effective); got != `{"target":"debug"}` {
		t.Errorf("effective %s", got)
	}
}

func TestAgentRunFinish(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	failing := exec.Command("sh", "-c", "exit 4")
	exitErr := failing.Run()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell for the exit code")
	}
	hide := func(s string) string { return strings.ReplaceAll(s, "s3cr3t", "***") }
	tests := []struct {
		name    string
		ctx     context.Context
		err     error
		outcome string
		exit    *int
		message string
	}{
		{"completed", context.Background(), nil, "completed", new(int), ""},
		{"an exit code", context.Background(), &agent.AuthError{Name: "agent", Err: exitErr}, "failed", new(4), ""},
		{"an error with no exit code", context.Background(), errors.New("agent s3cr3t is not installed"), "failed", nil, "agent *** is not installed"},
		{"cancelled", cancelled, context.Canceled, "cancelled", nil, ""},
		{"a long error", context.Background(), errors.New(strings.Repeat("é", 2000)), "failed", nil, strings.Repeat("é", 1024) + "[1952 bytes cut]"},
		{"a long error cut inside a character", context.Background(), errors.New("x" + strings.Repeat("é", 2000)), "failed", nil, "x" + strings.Repeat("é", 1023) + "[1954 bytes cut]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, _, dir := agentAuditEngine(t)
			run, _, err := eng.startAgentRun("review", eng.newAgentAdapter())
			if err != nil {
				t.Fatal(err)
			}
			run.finish(tt.ctx, tt.err, hide)
			a := auditActions(t, dir)[0]
			if a.Outcome != tt.outcome || !reflect.DeepEqual(a.ExitCode, tt.exit) {
				t.Errorf("outcome %q exit %v", a.Outcome, a.ExitCode)
			}
			if tt.message != "" && (a.Error == nil || a.Error.Message != tt.message) {
				t.Errorf("error %+v", a.Error)
			}
			if a.Parent != "" || a.DurationMs == nil {
				t.Errorf("parent %q duration %v", a.Parent, a.DurationMs)
			}
		})
	}
	t.Run("an end that cannot be written is a warning", func(t *testing.T) {
		eng, stderr, dir := agentAuditEngine(t)
		run, _, err := eng.startAgentRun("review", eng.newAgentAdapter())
		if err != nil {
			t.Fatal(err)
		}
		breakDay(t, dir, run.start)
		run.finish(context.Background(), nil, nil)
		if !strings.Contains(stderr.String(), "audit log: a record could not be written") {
			t.Errorf("standard error %q", stderr.String())
		}
	})
}

// When the run cannot be recorded, the agent gets no endpoint.
func TestAgentRunNotRecorded(t *testing.T) {
	t.Run("the log cannot be opened", func(t *testing.T) {
		eng, stderr, _ := agentAuditEngine(t)
		eng.opts.AuditRetention = "soon"
		var opts agent.Options
		stop, finish := eng.openAgentEndpoint("review", eng.newAgentAdapter(), &opts)
		stop()
		finish(nil)
		if opts.MCPURL != "" || opts.MCPToken != "" {
			t.Error("an endpoint was opened")
		}
		want := `warning: audit log: the agent run could not be recorded (RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more); the agent runs without task tools` + "\n"
		if stderr.String() != want {
			t.Errorf("standard error %q\nwant %q", stderr.String(), want)
		}
	})
	t.Run("the start cannot be written", func(t *testing.T) {
		eng, stderr, dir := agentAuditEngine(t)
		if err := os.MkdirAll(dayFileOf(dir, time.Now()), 0o700); err != nil {
			t.Fatal(err)
		}
		var opts agent.Options
		eng.openAgentEndpoint("review", eng.newAgentAdapter(), &opts)
		if opts.MCPURL != "" {
			t.Error("an endpoint was opened")
		}
		if !strings.Contains(stderr.String(), "the agent run could not be recorded (") || !strings.Contains(stderr.String(), "is not a regular file") {
			t.Errorf("standard error %q", stderr.String())
		}
	})
}

func TestLoadIdentity(t *testing.T) {
	want := planIdentity{runefile: "/r", def: "d", config: "c"}
	if got := (&engine{identity: func() planIdentity { return want }}).loadIdentity(); got != want {
		t.Errorf("from the function: %+v", got)
	}
	if got := (&engine{planIdent: want}).loadIdentity(); got != want {
		t.Errorf("from the plan identity: %+v", got)
	}
}

func TestInvocationEnv(t *testing.T) {
	base := []string{"A=1"}
	if got := invocationEnv(base, ""); !reflect.DeepEqual(got, base) {
		t.Errorf("no id: %v", got)
	}
	if got := invocationEnv(base, "abc"); !reflect.DeepEqual(got, []string{"A=1", "RUNE_INVOCATION_ID=abc"}) {
		t.Errorf("with an id: %v", got)
	}
}

// contextRunefile has a [context] hook that writes a marker, so a test can
// show that nothing of the Runefile ran.
const contextRunefile = "# Gather.\n[context]\nhealth:\n    @echo ran > context.done\n\nbuild:\n    @echo hi\n"

func serveProject(t *testing.T, runefile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(runefile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A server that cannot record does not start, and runs no Runefile code.
func TestServeRefusesLogInsideProject(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		runefile string
		base     func(dir string) string
	}{
		{"inside the Runefile's directory", contextRunefile, func(dir string) string { return filepath.Join(dir, ".audit") }},
		{"inside the working directory", "set working-directory := \"" + filepath.ToSlash(work) + "\"\n" + contextRunefile, func(string) string { return filepath.Join(work, "logs") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := serveProject(t, tt.runefile)
			base := tt.base(dir)
			err := ServeMCP(Options{Cwd: dir, Stderr: &bytes.Buffer{}, AuditDir: base, MCPNoReload: true}, false, "", "")
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.HasPrefix(err.Error(), "audit log: ") || !strings.HasSuffix(err.Error(), "set RUNE_AUDIT_DIR to a directory outside it") {
				t.Fatalf("got %v", err)
			}
			if !strings.Contains(err.Error(), "is inside the project (") {
				t.Errorf("text: %s", err)
			}
			for _, marker := range []string{filepath.Join(dir, "context.done"), filepath.Join(work, "context.done")} {
				if _, statErr := os.Stat(marker); statErr == nil {
					t.Error("the [context] hook ran")
				}
			}
			if _, statErr := os.Stat(base); statErr == nil {
				t.Error("the refused directory was created")
			}
		})
	}
}

func TestServeRetention(t *testing.T) {
	t.Run("a value that is not a number of days", func(t *testing.T) {
		dir := serveProject(t, contextRunefile)
		err := ServeMCP(Options{Cwd: dir, Stderr: &bytes.Buffer{}, AuditDir: t.TempDir(), AuditRetention: "soon", MCPNoReload: true}, false, "", "")
		var usage *UsageError
		if !errors.As(err, &usage) || err.Error() != `audit log: RUNE_AUDIT_RETENTION_DAYS: "soon" is not a whole number of days, zero or more` {
			t.Fatalf("got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
			t.Error("the [context] hook ran")
		}
	})

	for _, tt := range []struct {
		retention string
		oldKept   bool
	}{{"30", false}, {"0", true}, {"", true}} {
		t.Run("retention "+tt.retention, func(t *testing.T) {
			dir := serveProject(t, "build:\n    @echo hi\n")
			base := t.TempDir()
			project := runefilePath(nil, dir)
			logDir := audit.ProjectDir(base, project)
			if err := os.MkdirAll(logDir, 0o700); err != nil {
				t.Fatal(err)
			}
			old := dayFileOf(logDir, time.Now().AddDate(0, 0, -31))
			recent := dayFileOf(logDir, time.Now().AddDate(0, 0, -1))
			for _, path := range []string{old, recent} {
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// A server that is stopped before it starts still opened its log.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = ServeMCP(Options{Cwd: dir, Stderr: &bytes.Buffer{}, Ctx: ctx, AuditDir: base, AuditRetention: tt.retention, MCPNoReload: true}, true, "127.0.0.1:0", tokenFile(t))
			if _, err := os.Stat(old); (err == nil) != tt.oldKept {
				t.Errorf("the file 31 days old: kept %v, want %v", err == nil, tt.oldKept)
			}
			if _, err := os.Stat(recent); err != nil {
				t.Error("yesterday's file was removed")
			}
		})
	}
}

// executeAgent records the run, offers the endpoint, and records how the
// agent ended.
func TestExecuteAgentRecordsTheRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in agent needs a POSIX shell")
	}
	agentSrc := func(script string) string {
		return `set agent_cmd := ["sh", "-c", "` + script + `", "stub"]` + "\n\nbuild:\n    @echo hi\n\nreview (agent):\n    Review.\n"
	}
	setup := func(t *testing.T, script string) (*engine, *bytes.Buffer, string) {
		t.Helper()
		eng, stdout, _ := confirmEngine(t, agentSrc(script), nil, false, false)
		base := t.TempDir()
		eng.opts.AuditDir, eng.opts.AuditRetention = base, "0"
		eng.planIdent = planIdentity{runefile: filepath.Join(eng.root, "Runefile"), def: "aa", config: "bb"}
		return eng, stdout, audit.ProjectDir(base, eng.planIdent.runefile)
	}
	lines := []shell.Line{{Text: "Review."}}

	t.Run("an agent that ends well", func(t *testing.T) {
		eng, stdout, dir := setup(t, `printf 'url=%s' \"$RUNE_MCP_URL\"`)
		if err := eng.executeAgent(eng.tasks["review"], lines, eng.workDir, os.Environ()); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stdout.String(), "url=http://127.0.0.1:") || !strings.HasSuffix(stdout.String(), "\n") {
			t.Errorf("the agent's output: %q", stdout.String())
		}
		actions := auditActions(t, dir)
		if len(actions) != 1 || actions[0].Type != audit.TypeAgentRun || actions[0].Task != "review" || actions[0].Outcome != "completed" || actions[0].ExitCode == nil {
			t.Errorf("the run: %+v", actions)
		}
	})
	t.Run("an agent that fails", func(t *testing.T) {
		eng, _, dir := setup(t, "exit 4")
		if err := eng.executeAgent(eng.tasks["review"], lines, eng.workDir, os.Environ()); err == nil {
			t.Fatal("no error")
		}
		a := auditActions(t, dir)[0]
		if a.Outcome != "failed" || a.ExitCode == nil || *a.ExitCode != 4 || a.Error == nil {
			t.Errorf("the run: %+v, error %+v", a, a.Error)
		}
	})
	t.Run("an engine with no Runefile offers no endpoint", func(t *testing.T) {
		eng, stdout, dir := setup(t, `printf 'url=%s' \"$RUNE_MCP_URL\"`)
		eng.file = nil
		if err := eng.executeAgent(eng.tasks["review"], lines, eng.workDir, os.Environ()); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != "url=\n" {
			t.Errorf("the agent's output: %q", stdout.String())
		}
		if got := auditActions(t, dir); len(got) != 0 {
			t.Errorf("a run with no endpoint was recorded: %+v", got)
		}
	})
}

// The server names the outcomes of a call and the log lists the ones a
// reader may filter on. A new outcome on one side must reach the other, or
// `rune log --outcome` would refuse a value the records hold.
func TestOutcomesMatchTheServer(t *testing.T) {
	for server, stored := range map[string]string{
		mcpserver.AuditCompleted:        audit.OutcomeCompleted,
		mcpserver.AuditFailed:           audit.OutcomeFailed,
		mcpserver.AuditRefused:          audit.OutcomeRefused,
		mcpserver.AuditInvalidArguments: audit.OutcomeInvalidArguments,
		mcpserver.AuditUnknownTool:      audit.OutcomeUnknownTool,
		mcpserver.AuditCancelled:        audit.OutcomeCancelled,
		mcpserver.AuditTimedOut:         audit.OutcomeTimedOut,
		mcpserver.AuditError:            audit.OutcomeError,
	} {
		if server != stored {
			t.Errorf("the server says %q and the log %q", server, stored)
		}
		if !slices.Contains(audit.Outcomes, server) {
			t.Errorf("%q is not an outcome rune log accepts", server)
		}
	}
	if len(audit.Outcomes) != 10 {
		t.Errorf("%d outcomes, want the eight of a call, incomplete and ok", len(audit.Outcomes))
	}
}
