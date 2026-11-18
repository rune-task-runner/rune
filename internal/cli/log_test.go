package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/audit"
)

// Spec 052: `rune log` reads the audit log of the project. The texts are
// those of specs/052-mcp-audit-log/contracts/rune-log.md.

// logNow is "now" in these tests: 12:30 in Kyiv on 2026-11-18.
var logNow = time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)

// logFixture is a project with a Runefile and an audit log outside it.
type logFixture struct {
	dir     string // the project
	base    string // RUNE_AUDIT_DIR
	project string // the resolved Runefile path
	log     *audit.Log
}

func newLogFixture(t *testing.T, runefile string) *logFixture {
	t.Helper()
	f := &logFixture{dir: t.TempDir(), base: t.TempDir()}
	path := filepath.Join(f.dir, "Runefile")
	if err := os.WriteFile(path, []byte(runefile), 0o644); err != nil {
		t.Fatal(err)
	}
	f.project = runefilePath(&ast.File{Path: path}, f.dir)
	log, err := audit.Open(audit.Options{Base: f.base, Runefile: f.project})
	if err != nil {
		t.Fatal(err)
	}
	f.log = log
	return f
}

// run runs `rune log` in the project with the fixture's clock.
func (f *logFixture) run(flags LogFlags) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	flags.now = func() time.Time { return logNow }
	err = Log(Options{Cwd: f.dir, Stdout: &out, Stderr: &errOut, AuditDir: f.base}, flags)
	return out.String(), errOut.String(), err
}

// logCall is a call to write into a fixture's log.
type logCall struct {
	id       string
	at       time.Time
	task     string
	outcome  string // "" leaves the call without an end
	exit     *int
	ms       int64
	client   string
	session  string
	supplied string
	bound    string
	change   func(start, end *audit.Record)
}

func (f *logFixture) add(t *testing.T, c logCall) {
	t.Helper()
	start := audit.Record{
		Type: audit.TypeCall, Phase: audit.PhaseStart, ID: c.id, Time: c.at,
		Tool: c.task, Task: c.task, Session: c.session, Transport: "stdio", Auth: "process",
	}
	if c.client != "" {
		start.Client = &audit.Client{Name: c.client, Version: "2.1.0"}
	}
	if c.supplied != "" {
		start.Arguments = &audit.Arguments{Supplied: json.RawMessage(c.supplied)}
	}
	ms := c.ms
	end := audit.Record{
		Type: audit.TypeCall, Phase: audit.PhaseEnd, ID: c.id, Time: c.at.Add(time.Duration(c.ms) * time.Millisecond),
		Outcome: c.outcome, ExitCode: c.exit, DurationMs: &ms,
	}
	if c.bound != "" {
		end.Arguments = &audit.Arguments{Effective: json.RawMessage(c.bound)}
	}
	if c.change != nil {
		c.change(&start, &end)
	}
	if err := f.log.Append(c.at, start, false); err != nil {
		t.Fatal(err)
	}
	if c.outcome == "" {
		return
	}
	if err := f.log.Append(c.at, end, false); err != nil {
		t.Fatal(err)
	}
}

func (f *logFixture) addRecord(t *testing.T, rec audit.Record) {
	t.Helper()
	if err := f.log.Append(rec.Time, rec, false); err != nil {
		t.Fatal(err)
	}
}

const plainRunefile = "build:\n    @echo hi\n"

// twoDays writes the calls most tests list: two today, one yesterday.
func twoDays(t *testing.T) *logFixture {
	t.Helper()
	f := newLogFixture(t, plainRunefile)
	f.add(t, logCall{
		id: "3fa9c2d1e07b5a44", at: time.Date(2026, 11, 18, 8, 4, 11, 0, time.UTC), task: "build",
		outcome: "completed", exit: new(0), ms: 12400, client: "claude-code", session: "5f0e7a11c3d2b694",
		bound: `{"target":"debug"}`,
	})
	f.add(t, logCall{
		id: "0c7a91e3b5d2f648", at: time.Date(2026, 11, 18, 8, 8, 15, 0, time.UTC), task: "check",
		outcome: "failed", exit: new(3), ms: 48000, client: "claude-code", session: "5f0e7a11c3d2b694",
	})
	f.add(t, logCall{
		id: "aaaaaaaaaaaaaaaa", at: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), task: "deploy",
		outcome: "refused", ms: 0, client: "Codex", session: "9c00000000000000",
		supplied: `{"env":"prod"}`,
	})
	return f
}

func listedIDs(t *testing.T, f *logFixture, flags LogFlags) string {
	t.Helper()
	flags.JSON = true
	out, _, err := f.run(flags)
	if err != nil {
		t.Fatalf("rune log: %v", err)
	}
	var ids []string
	for line := range strings.Lines(out) {
		var a audit.Action
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("a line is not JSON: %q", line)
		}
		ids = append(ids, a.ID[:2])
	}
	return strings.Join(ids, ",")
}

func TestLogToday(t *testing.T) {
	f := twoDays(t)
	out, errOut, err := f.run(LogFlags{TZ: "Europe/Kyiv"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Agent actions on 2026-11-18 (Europe/Kyiv, UTC+02:00) for " + f.project + "\n" +
		"\n" +
		`10:04:11  completed  build target="debug"  exit 0  12.4s  claude-code 2.1.0  3fa9c2d1e07b5a44` + "\n" +
		`10:08:15  failed     check` + strings.Repeat(" ", 17) + `exit 3  48.0s  claude-code 2.1.0  0c7a91e3b5d2f648` + "\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if errOut != "" {
		t.Errorf("standard error %q", errOut)
	}
}

func TestLogDefaultZoneIsLocal(t *testing.T) {
	out, _, err := twoDays(t).run(LogFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Agent actions on ") || !strings.Contains(strings.SplitN(out, "\n", 2)[0], "(local, UTC") {
		t.Errorf("header: %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestLogEmpty(t *testing.T) {
	f := newLogFixture(t, plainRunefile)
	if err := os.RemoveAll(f.log.Dir()); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := f.run(LogFlags{TZ: "UTC"})
	want := "Agent actions on 2026-11-18 (UTC) for " + f.project + "\n\nNo agent actions.\n"
	if err != nil || out != want || errOut != "" {
		t.Errorf("out %q, standard error %q, err %v", out, errOut, err)
	}
	if _, statErr := os.Stat(f.log.Dir()); statErr == nil {
		t.Error("rune log created the log directory")
	}
}

func TestLogOtherProject(t *testing.T) {
	f := twoDays(t)
	other := filepath.Join(t.TempDir(), "Runefile")
	log, err := audit.Open(audit.Options{Base: f.base, Runefile: other})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	if err := log.Append(at, audit.Record{Type: audit.TypeCall, Phase: audit.PhaseStart, ID: "ffffffffffffffff", Time: at, Task: "other"}, false); err != nil {
		t.Fatal(err)
	}
	if got := listedIDs(t, f, LogFlags{TZ: "UTC"}); got != "3f,0c" {
		t.Errorf("listed %s", got)
	}
}

func TestLogFindsServerLog(t *testing.T) {
	f := twoDays(t)
	run := func(opts Options) string {
		var out bytes.Buffer
		opts.Stdout, opts.Stderr, opts.AuditDir = &out, &bytes.Buffer{}, f.base
		if err := Log(opts, LogFlags{Dir: true}); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	if got := run(Options{Cwd: f.dir}); got != f.log.Dir() {
		t.Errorf("from the project: %q, want %q", got, f.log.Dir())
	}
	sub := filepath.Join(f.dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(Options{Cwd: sub}); got != f.log.Dir() {
		t.Errorf("from a subdirectory: %q", got)
	}
	// A relative -f is read from the directory the process is in.
	t.Chdir(sub)
	if got := run(Options{Cwd: sub, File: filepath.Join("..", "Runefile")}); got != f.log.Dir() {
		t.Errorf("through a relative -f: %q", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(f.dir, link); err != nil {
		t.Fatal(err)
	}
	if got := run(Options{Cwd: link}); got != f.log.Dir() {
		t.Errorf("through a symbolic link: %q", got)
	}
}

func TestLogTaskNamedLog(t *testing.T) {
	note := `note: "log" is also a task of this Runefile; run it with: rune -- log` + "\n"
	tests := []struct {
		name, runefile, want string
	}{
		{"a task named log", "log:\n    @echo hi\n", note},
		{"no such task", plainRunefile, ""},
		{"a Runefile that does not parse", "this is not ((( a Runefile\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLogFixture(t, tt.runefile)
			f.add(t, logCall{id: "3fa9c2d1e07b5a44", at: logNow.Add(-time.Hour), task: "log", outcome: "completed", exit: new(0)})
			out, errOut, err := f.run(LogFlags{TZ: "UTC"})
			if err != nil || errOut != tt.want {
				t.Errorf("standard error %q, err %v", errOut, err)
			}
			if !strings.Contains(out, "3fa9c2d1e07b5a44") {
				t.Errorf("the log is not listed: %q", out)
			}
		})
	}
}

func TestHasLogTask(t *testing.T) {
	if hasLogTask(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a missing Runefile has a log task")
	}
	write := func(dir, name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Run("in the Runefile", func(t *testing.T) {
		if !hasLogTask(write(t.TempDir(), "Runefile", "log:\n    @echo log\n")) {
			t.Error("the task was not found")
		}
	})
	t.Run("in an imported file", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, "other.rune", "log:\n    @echo log\n")
		if !hasLogTask(write(dir, "Runefile", "import \"other.rune\"\n\nbuild:\n    @echo build\n")) {
			t.Error("the imported task was not found")
		}
	})
	t.Run("no such task", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, "other.rune", "logs:\n    @echo logs\n")
		if hasLogTask(write(dir, "Runefile", "import \"other.rune\"\n\nbuild:\n    @echo build\n")) {
			t.Error("a Runefile without the task has it")
		}
	})
	t.Run("an import that is missing", func(t *testing.T) {
		if hasLogTask(write(t.TempDir(), "Runefile", "import \"missing.rune\"\n\nbuild:\n    @echo build\n")) {
			t.Error("a Runefile without the task has it")
		}
	})
}

func TestLogNoRunefile(t *testing.T) {
	var out bytes.Buffer
	err := Log(Options{Cwd: t.TempDir(), Stdout: &out, Stderr: &bytes.Buffer{}, AuditDir: t.TempDir()}, LogFlags{})
	var usage *UsageError
	if !errors.As(err, &usage) || out.Len() != 0 {
		t.Errorf("err %v, out %q", err, out.String())
	}
}

func TestLogRunsNothing(t *testing.T) {
	f := newLogFixture(t, "build:\n    @echo ran > ran.done\n\nlog:\n    @echo ran > log.done\n")
	f.add(t, logCall{id: "3fa9c2d1e07b5a44", at: logNow.Add(-time.Hour), task: "build", outcome: "completed", exit: new(0)})
	before, err := os.ReadFile(dayFileOf(f.log.Dir(), logNow))
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range []LogFlags{{}, {JSON: true}, {All: true}, {Dir: true}} {
		if _, _, err := f.run(flags); err != nil {
			t.Fatal(err)
		}
	}
	for _, marker := range []string{"ran.done", "log.done"} {
		if _, err := os.Stat(filepath.Join(f.dir, marker)); err == nil {
			t.Errorf("%s exists: a task ran", marker)
		}
	}
	after, err := os.ReadFile(dayFileOf(f.log.Dir(), logNow))
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("the log changed: %v", err)
	}
	entries, err := os.ReadDir(f.log.Dir())
	if err != nil || len(entries) != 2 {
		t.Errorf("%d files in the log directory, want the day file and project.json", len(entries))
	}
}

func TestRowOf(t *testing.T) {
	at := time.Date(2026, 11, 18, 8, 7, 40, 0, time.UTC)
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	long := `{"q":"` + strings.Repeat("x", 100) + `"}`
	tests := []struct {
		name    string
		action  audit.Action
		cols    [7]string
		details []string
	}{
		{
			"a refused call",
			audit.Action{
				Type: audit.TypeCall, ID: "5d20", Start: at, Outcome: "refused", Tool: "clean", Task: "clean", DurationMs: new(int64(0)),
				Error: &audit.Error{Kind: "policy", Message: "task \"clean\" is destructive\nsecond line"},
			},
			[7]string{"10:07:40", "refused", "clean", "-", "0.0s", "-", "5d20"},
			[]string{`policy: task "clean" is destructive`},
		},
		{
			"an approved apply",
			audit.Action{
				Type: audit.TypeCall, ID: "b81e", Start: at, Outcome: "completed", Task: "apply", ExitCode: new(0), DurationMs: new(int64(22100)),
				Client:    &audit.Client{Name: "claude-code", Version: "2.1.0"},
				Arguments: &audit.Arguments{Supplied: json.RawMessage(`{"env":"x"}`), Effective: json.RawMessage(`{"env":"prod","files":["a","b"]}`)},
				Approval:  &audit.Approval{Asked: true, Decision: "approved"},
				Plan:      &audit.Plan{Role: "applied", ID: "7f3c9a1e52b84d06", Digest: "sha256:9b1f8c2e0d41aabbcc"},
				Parent:    "0123456789abcdef",
			},
			[7]string{"10:07:40", "completed", `apply env="prod" files=["a","b"]`, "exit 0", "22.1s", "claude-code 2.1.0", "b81e"},
			[]string{"approved at the client; plan 7f3c9a1e52b84d06 sha256:9b1f8c2e0d41", "parent 0123456789abcdef"},
		},
		{
			"a granted call that stored a plan",
			audit.Action{
				Type: audit.TypeCall, ID: "1", Start: at, Outcome: "completed", Task: "plan", ExitCode: new(0),
				Grants: []string{"clean", "wipe"}, Plan: &audit.Plan{Role: "stored", ID: "p1", Digest: "short"},
			},
			[7]string{"10:07:40", "completed", "plan", "exit 0", "-", "-", "1"},
			[]string{"granted by the operator: clean, wipe; plan p1 sha256:short stored"},
		},
		{
			"a declined approval",
			audit.Action{Type: audit.TypeCall, ID: "2", Start: at, Outcome: "refused", Task: "clean", Approval: &audit.Approval{Asked: true, Decision: "declined"}, Error: &audit.Error{Kind: "approval"}},
			[7]string{"10:07:40", "refused", "clean", "-", "-", "-", "2"},
			[]string{"approval", "approval declined"},
		},
		{
			"an incomplete call with supplied arguments only",
			audit.Action{Type: audit.TypeCall, ID: "e4b6", Start: at, Outcome: "incomplete", Tool: "deploy", Arguments: &audit.Arguments{Supplied: json.RawMessage(`{"env":"staging"}`)}, Client: &audit.Client{Name: "codex"}},
			[7]string{"10:07:40", "incomplete", `deploy env="staging"`, "-", "-", "codex", "e4b6"},
			[]string{"no completion was recorded: the call is still running or the server stopped"},
		},
		{
			"long arguments are cut",
			audit.Action{Type: audit.TypeCall, ID: "3", Start: at, Outcome: "completed", Task: "t", Arguments: &audit.Arguments{Effective: json.RawMessage(long)}},
			[7]string{"10:07:40", "completed", `t q="` + strings.Repeat("x", 77) + "...", "-", "-", "-", "3"},
			nil,
		},
		{
			"arguments that are not an object",
			audit.Action{Type: audit.TypeCall, ID: "4", Start: at, Outcome: "invalid-arguments", Tool: "build", Arguments: &audit.Arguments{Supplied: json.RawMessage(`[ 1, "a" ]`)}},
			[7]string{"10:07:40", "invalid-arguments", `build [1,"a"]`, "-", "-", "-", "4"},
			nil,
		},
		{
			"a control character and a direction override are escaped",
			audit.Action{Type: audit.TypeCall, ID: "5", Start: at, Outcome: "unknown-tool", Tool: "a\x1b[31mb\u202ec"},
			[7]string{"10:07:40", "unknown-tool", `a\x1b[31mb\u202ec`, "-", "-", "-", "5"},
			nil,
		},
		{
			// The files belong to the user, so a task can write a record of
			// its own: no field of one reaches the terminal as it is.
			"an outcome, an id and an error kind are escaped too",
			audit.Action{Type: audit.TypeCall, ID: "5\x1b[2J", Start: at, Outcome: "ok\nfake", Task: "t", Error: &audit.Error{Kind: "k\x1b[0m", Message: "m"}},
			[7]string{"10:07:40", `ok\nfake`, "t", "-", "-", "-", `5\x1b[2J`},
			[]string{`k\x1b[0m: m`},
		},
		{
			"an agent run",
			audit.Action{Type: audit.TypeAgentRun, ID: "6", Start: at, Outcome: "incomplete", Task: "review"},
			[7]string{"10:07:40", "incomplete", "agent review", "-", "-", "-", "6"},
			[]string{"no completion was recorded: the run is still running or the server stopped"},
		},
		{
			"an authentication failure with a count",
			audit.Action{Type: audit.TypeAuthFailure, ID: "7", Start: at, Remote: "127.0.0.1:5123", Suppressed: 20},
			[7]string{"10:07:40", "auth-failure", "127.0.0.1:5123 (+20 not recorded)", "-", "-", "-", "7"},
			nil,
		},
		{
			"a failed request",
			audit.Action{Type: audit.TypeRequest, ID: "8", Start: at, Outcome: "error", Method: "prompts/get", DurationMs: new(int64(3)), Error: &audit.Error{Kind: "protocol", Message: "unknown prompt"}},
			[7]string{"10:07:40", "request", "prompts/get", "-", "0.0s", "-", "8"},
			[]string{"protocol: unknown prompt"},
		},
		{
			"a type this version does not know",
			audit.Action{Type: "future", ID: "9", Start: at},
			[7]string{"10:07:40", "future", "", "-", "-", "-", "9"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := rowOf(tt.action, kyiv, logTimeLayout)
			if row.cols != tt.cols {
				t.Errorf("columns\n got %q\nwant %q", row.cols, tt.cols)
			}
			if strings.Join(row.details, "|") != strings.Join(tt.details, "|") {
				t.Errorf("details\n got %q\nwant %q", row.details, tt.details)
			}
		})
	}
}

func TestLogRange(t *testing.T) {
	f := twoDays(t)
	// 23:30 UTC on the 16th is the 17th in Kyiv.
	f.add(t, logCall{id: "bbbbbbbbbbbbbbbb", at: time.Date(2026, 11, 16, 23, 30, 0, 0, time.UTC), task: "late", outcome: "completed", exit: new(0)})
	f.add(t, logCall{id: "cccccccccccccccc", at: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), task: "old", outcome: "completed", exit: new(0)})

	tests := []struct {
		name  string
		flags LogFlags
		want  string
	}{
		{"today", LogFlags{TZ: "UTC"}, "3f,0c"},
		{"a day", LogFlags{TZ: "UTC", Date: "2026-11-17"}, "aa"},
		{"a day in a zone ahead of UTC", LogFlags{TZ: "Europe/Kyiv", Date: "2026-11-17"}, "bb,aa"},
		{"the same instant is the day before in UTC", LogFlags{TZ: "UTC", Date: "2026-11-16"}, "bb"},
		{"since a day", LogFlags{TZ: "UTC", Since: "2026-11-17"}, "aa,3f,0c"},
		{"until a day includes it", LogFlags{TZ: "UTC", Until: "2026-11-16"}, "cc,bb"},
		{"since and until days", LogFlags{TZ: "UTC", Since: "2026-11-16", Until: "2026-11-17"}, "bb,aa"},
		{"since a time", LogFlags{Since: "2026-11-18T08:08:15Z"}, "0c"},
		{"until a time is exclusive", LogFlags{Until: "2026-11-18T08:08:15Z", Since: "2026-11-18T00:00:00Z"}, "3f"},
		{"a time with an offset", LogFlags{Since: "2026-11-18T10:05:00+02:00"}, "0c"},
		{"since a duration", LogFlags{Since: "2h25m"}, "0c"},
		{"since days", LogFlags{Since: "2d"}, "bb,aa,3f,0c"},
		{"until a duration", LogFlags{Since: "3d", Until: "24h"}, "bb,aa"},
		{"local is accepted", LogFlags{TZ: "LOCAL", Since: "9d"}, "cc,bb,aa,3f,0c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listedIDs(t, f, tt.flags); got != tt.want {
				t.Errorf("listed %s, want %s", got, tt.want)
			}
		})
	}

	out, _, err := f.run(LogFlags{TZ: "Europe/Kyiv", Since: "2026-11-16", Until: "2026-11-17"})
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := "Agent actions from 2026-11-16 00:00 to 2026-11-18 00:00 (Europe/Kyiv, UTC+02:00) for " + f.project
	lines := strings.Split(out, "\n")
	if lines[0] != wantHeader {
		t.Errorf("header %q\nwant   %q", lines[0], wantHeader)
	}
	if !strings.HasPrefix(lines[2], "2026-11-17 01:30:00  completed  late") {
		t.Errorf("a range line carries the day: %q", lines[2])
	}
}

func TestLogRangeHeader(t *testing.T) {
	plus := time.FixedZone("plus", 5*3600+30*60)
	minus := time.FixedZone("minus", -8*3600)
	now := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		rng  logRange
		want string
	}{
		{
			"one day, local",
			logRange{loc: plus, zone: "local", now: now, since: time.Date(2026, 11, 18, 0, 0, 0, 0, plus), oneDay: true},
			"Agent actions on 2026-11-18 (local, UTC+05:30) for /r",
		},
		{
			"a zone behind UTC",
			logRange{loc: minus, zone: "America/Los_Angeles", now: now, since: time.Date(2026, 11, 17, 0, 0, 0, 0, minus), oneDay: true},
			"Agent actions on 2026-11-17 (America/Los_Angeles, UTC-08:00) for /r",
		},
		{
			"UTC",
			logRange{loc: time.UTC, zone: "UTC", now: now, since: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), oneDay: true},
			"Agent actions on 2026-11-18 (UTC) for /r",
		},
		{
			"an open start",
			logRange{loc: time.UTC, zone: "UTC", now: now, until: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)},
			"Agent actions from the start of the log to 2026-11-17 00:00 (UTC) for /r",
		},
		{
			"an open end runs to now",
			logRange{loc: plus, zone: "local", now: now, since: time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)},
			"Agent actions from 2026-11-18 13:30 to 2026-11-18 16:00 (local, UTC+05:30) for /r",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rng.header("/r"); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestLogFilters(t *testing.T) {
	f := twoDays(t)
	f.add(t, logCall{
		id: "dddddddddddddddd", at: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), task: "mod::lint",
		outcome: "refused", client: "codex", session: "9c00000000000000",
		change: func(start, _ *audit.Record) { start.Tool = "mod__lint" },
	})
	f.add(t, logCall{id: "eeeeeeeeeeeeeeee", at: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), task: "slow", client: "codex", session: "9c00000000000000"})
	f.addRecord(t, audit.Record{Type: audit.TypeAuthFailure, ID: "ffffffffffffffff", Time: time.Date(2026, 11, 18, 9, 45, 0, 0, time.UTC), Remote: "127.0.0.1:1"})

	tests := []struct {
		name  string
		flags LogFlags
		want  string
	}{
		{"no filter", LogFlags{}, "3f,0c,dd,ee,ff"},
		{"a task", LogFlags{Tasks: []string{"check"}}, "0c"},
		{"a task by its tool name", LogFlags{Tasks: []string{"mod__lint"}}, "dd"},
		{"a task by its task name", LogFlags{Tasks: []string{"mod::lint"}}, "dd"},
		{"tasks, comma-separated and repeated", LogFlags{Tasks: []string{"check, build", "slow"}}, "3f,0c,ee"},
		{"outcomes", LogFlags{Outcomes: []string{"failed,refused"}}, "0c,dd"},
		{"incomplete", LogFlags{Outcomes: []string{"incomplete"}}, "ee"},
		{"completed does not list a call with no end", LogFlags{Outcomes: []string{"completed"}}, "3f"},
		{"a client, ignoring case", LogFlags{Clients: []string{"CODEX"}}, "dd,ee"},
		{"a session", LogFlags{Sessions: []string{"5f0e7a11c3d2b694"}}, "3f,0c"},
		{"a session prefix", LogFlags{Sessions: []string{"9c"}}, "dd,ee"},
		{"two flags must both match", LogFlags{Clients: []string{"codex"}, Outcomes: []string{"refused"}}, "dd"},
		{"two flags that exclude each other", LogFlags{Clients: []string{"codex"}, Tasks: []string{"check"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.flags.TZ = "UTC"
			if got := listedIDs(t, f, tt.flags); got != tt.want {
				t.Errorf("listed %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLogAll(t *testing.T) {
	f := twoDays(t)
	f.addRecord(t, audit.Record{Type: audit.TypeRequest, ID: "9999999999999999", Time: time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC), Method: "tools/list", Outcome: "ok"})
	if got := listedIDs(t, f, LogFlags{TZ: "UTC"}); got != "3f,0c" {
		t.Errorf("without --all: %s", got)
	}
	if got := listedIDs(t, f, LogFlags{TZ: "UTC", All: true}); got != "99,3f,0c" {
		t.Errorf("with --all: %s", got)
	}
	out, _, err := f.run(LogFlags{TZ: "UTC", All: true})
	if err != nil || !strings.Contains(out, "08:00:00  request    tools/list") {
		t.Errorf("the request line: %q, %v", out, err)
	}
}

func TestLogJSON(t *testing.T) {
	f := twoDays(t)
	f.add(t, logCall{id: "eeeeeeeeeeeeeeee", at: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), task: "slow", client: "codex", supplied: `{"q":"a<b>&c"}`})
	out, errOut, err := f.run(LogFlags{TZ: "Europe/Kyiv", JSON: true})
	if err != nil || errOut != "" {
		t.Fatalf("err %v, standard error %q", err, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3:\n%s", len(lines), out)
	}
	want := `{"schemaVersion":1,"type":"call","id":"3fa9c2d1e07b5a44","start":"2026-11-18T08:04:11Z","end":"2026-11-18T08:04:23.4Z","durationMs":12400,"outcome":"completed","exitCode":0,"session":"5f0e7a11c3d2b694","transport":"stdio","auth":"process","client":{"name":"claude-code","version":"2.1.0","reported":true},"tool":"build","task":"build","arguments":{"effective":{"target":"debug"}}}`
	if lines[0] != want {
		t.Errorf("first line\n got %s\nwant %s", lines[0], want)
	}
	var incomplete map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &incomplete); err != nil {
		t.Fatal(err)
	}
	if incomplete["outcome"] != "incomplete" || incomplete["end"] != nil || incomplete["exitCode"] != nil || incomplete["durationMs"] != nil {
		t.Errorf("an incomplete action: %s", lines[2])
	}
	if !strings.Contains(lines[2], `"q":"a<b>&c"`) {
		t.Errorf("the arguments were escaped: %s", lines[2])
	}

	empty := newLogFixture(t, plainRunefile)
	if out, _, err := empty.run(LogFlags{JSON: true}); err != nil || out != "" {
		t.Errorf("no action: %q, %v", out, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestPrintLogJSONError(t *testing.T) {
	err := printLogJSON(failingWriter{}, []audit.Action{{ID: "a"}})
	var failure *CommandFailure
	if !errors.As(err, &failure) {
		t.Errorf("got %v", err)
	}
}

func TestLogDir(t *testing.T) {
	f := newLogFixture(t, plainRunefile)
	if err := os.RemoveAll(f.base); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := f.run(LogFlags{Dir: true})
	if err != nil || out != f.log.Dir()+"\n" || errOut != "" {
		t.Errorf("out %q, standard error %q, err %v", out, errOut, err)
	}
	if _, statErr := os.Stat(f.base); statErr == nil {
		t.Error("--dir created the directory")
	}
}

func TestLogUsageErrors(t *testing.T) {
	outcomes := "completed, failed, refused, invalid-arguments, unknown-tool, cancelled, timed-out, error, incomplete, ok"
	tests := []struct {
		flags LogFlags
		want  string
	}{
		{LogFlags{Outcomes: []string{"nope"}}, `--outcome: unknown outcome "nope" (one of ` + outcomes + `)`},
		{LogFlags{TZ: "Mars/Olympus"}, `--tz: unknown time zone "Mars/Olympus" (an IANA name such as Europe/Kyiv, UTC, or local)`},
		{LogFlags{Date: "2026-13-01"}, `--date: "2026-13-01" is not a day (YYYY-MM-DD)`},
		{LogFlags{Date: "2026-11-18", Since: "2h"}, `--date cannot be combined with --since or --until`},
		{LogFlags{Date: "2026-11-18", Until: "2h"}, `--date cannot be combined with --since or --until`},
		{LogFlags{Since: "soon"}, `--since: "soon" is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)`},
		{LogFlags{Until: "-2h"}, `--until: "-2h" is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)`},
		{LogFlags{Since: "xd"}, `--since: "xd" is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)`},
		{LogFlags{Since: "1h", Until: "2h"}, `--since is after --until`},
		{LogFlags{Dir: true, JSON: true}, `--dir cannot be combined with other flags`},
		{LogFlags{Dir: true, Tasks: []string{"x"}}, `--dir cannot be combined with other flags`},
		{LogFlags{Tasks: []string{"a,,b"}}, `--task: empty value`},
		{LogFlags{Outcomes: []string{""}}, `--outcome: empty value`},
		{LogFlags{Clients: []string{" "}}, `--client: empty value`},
		{LogFlags{Sessions: []string{","}}, `--session: empty value`},
	}
	f := twoDays(t)
	for _, tt := range tests {
		out, _, err := f.run(tt.flags)
		var usage *UsageError
		if !errors.As(err, &usage) || err.Error() != tt.want {
			t.Errorf("%+v:\n got %v\nwant %s", tt.flags, err, tt.want)
		}
		if CodeFor(err) != ExitUsage || out != "" {
			t.Errorf("%+v: exit %d, out %q", tt.flags, CodeFor(err), out)
		}
	}
}

func TestLogDamage(t *testing.T) {
	f := twoDays(t)
	path := dayFileOf(f.log.Dir(), logNow)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"v":1,"type":"call","pha`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	f.add(t, logCall{id: "eeeeeeeeeeeeeeee", at: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), task: "after", outcome: "completed", exit: new(0)})

	out, errOut, err := f.run(LogFlags{TZ: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "warning: 1 record in " + path + " could not be read and was skipped\n"; errOut != want {
		t.Errorf("standard error %q\nwant %q", errOut, want)
	}
	for _, id := range []string{"3fa9c2d1e07b5a44", "0c7a91e3b5d2f648", "eeeeeeeeeeeeeeee"} {
		if !strings.Contains(out, id) {
			t.Errorf("%s is not listed:\n%s", id, out)
		}
	}
	if got := damageWarning(audit.Damage{File: "/f", Count: 3}); got != "warning: 3 records in /f could not be read and were skipped" {
		t.Errorf("plural: %q", got)
	}
}

func TestLogReadError(t *testing.T) {
	f := newLogFixture(t, plainRunefile)
	if err := os.Mkdir(dayFileOf(f.log.Dir(), logNow), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.run(LogFlags{TZ: "UTC"})
	var failure *CommandFailure
	if !errors.As(err, &failure) || CodeFor(err) != ExitTaskFail {
		t.Errorf("err %v, exit %d", err, CodeFor(err))
	}
}

func TestLogNoStateDirectory(t *testing.T) {
	f := newLogFixture(t, plainRunefile)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", "")
		t.Setenv("USERPROFILE", "")
	}
	err := Log(Options{Cwd: f.dir, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, LogFlags{})
	var failure *CommandFailure
	if !errors.As(err, &failure) || !strings.HasPrefix(err.Error(), "audit log: no state directory") {
		t.Errorf("got %v", err)
	}
}

func TestLogRelativeAuditDir(t *testing.T) {
	f := newLogFixture(t, plainRunefile)
	var out bytes.Buffer
	err := Log(Options{Cwd: f.dir, Stdout: &out, Stderr: &bytes.Buffer{}, AuditDir: "audit"}, LogFlags{})
	var usage *UsageError
	if !errors.As(err, &usage) || err.Error() != `audit log: RUNE_AUDIT_DIR must be an absolute path, not "audit"` || CodeFor(err) != ExitUsage || out.Len() != 0 {
		t.Errorf("err %v, out %q", err, out.String())
	}
}

func TestParseAgo(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"2h": 2 * time.Hour, "90m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "0d": 0, "1h30m": 90 * time.Minute, "0s": 0,
	} {
		if got, ok := parseAgo(value); !ok || got != want {
			t.Errorf("parseAgo(%q) = %s, %v, want %s", value, got, ok, want)
		}
	}
	// 106752 days and more do not fit a duration: they wrap to another time.
	for _, value := range []string{"", "d", "-1d", "1.5d", "soon", "-2h", "7", "106752d", "9000000000000000d"} {
		if _, ok := parseAgo(value); ok {
			t.Errorf("parseAgo(%q) was accepted", value)
		}
	}
}

func TestDurationText(t *testing.T) {
	for ms, want := range map[int64]string{
		0: "0.0s", 49: "0.0s", 150: "0.1s", 12400: "12.4s", 59900: "59.9s",
		60000: "1m00s", 125000: "2m05s", 3600000: "1h00m", 5400000: "1h30m",
	} {
		if got := durationText(ms); got != want {
			t.Errorf("durationText(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestArgPairs(t *testing.T) {
	tests := []struct{ raw, want string }{
		{`{"b":"x","a":1,"c":[ "p", "q" ],"d":{"k":true},"e":null}`, `b="x" a=1 c=["p","q"] d={"k":true} e=null`},
		{`{}`, ``},
		{`"text"`, `"text"`},
		{`[1, 2]`, `[1,2]`},
		{`{"a":`, `{"a":`},
		{`{"a" 1}`, `{"a" 1}`},
		{`{`, `{`},
	}
	for _, tt := range tests {
		if got := argPairs(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("argPairs(%s) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestLogArgsText(t *testing.T) {
	if got := logArgsText(nil); got != "" {
		t.Errorf("no arguments: %q", got)
	}
	if got := logArgsText(&audit.Arguments{}); got != "" {
		t.Errorf("empty arguments: %q", got)
	}
}

func TestCutLine(t *testing.T) {
	if got := cutLine("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := cutLine("ééééé", 3); got != "ééé..." {
		t.Errorf("got %q", got)
	}
}

func TestClientText(t *testing.T) {
	for want, c := range map[string]*audit.Client{
		"-":             nil,
		"claude-code 2": {Name: "claude-code", Version: "2"},
		"codex":         {Name: "codex"},
		"1.0":           {Version: "1.0"},
	} {
		if got := clientText(c); got != want {
			t.Errorf("clientText(%+v) = %q, want %q", c, got, want)
		}
	}
	if got := clientText(&audit.Client{}); got != "-" {
		t.Errorf("an empty client: %q", got)
	}
}

func TestListValues(t *testing.T) {
	got, err := listValues("--task", []string{"a, b", "c"})
	if err != nil || strings.Join(got, "|") != "a|b|c" {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := listValues("--task", nil); err != nil || got != nil {
		t.Errorf("no values: %v, %v", got, err)
	}
}

// Spec 053: a call that ran a task on a server started unsandboxed says so.
// A call that ran nothing does not: nothing ran, sandboxed or not.
func TestLogUnsandboxedLine(t *testing.T) {
	at := time.Date(2026, 11, 18, 8, 7, 40, 0, time.UTC)
	const line = "ran without a sandbox (the server was started with the sandbox off)"
	call := func(sandbox *audit.Sandbox, exit *int, err *audit.Error) audit.Action {
		outcome := "completed"
		if err != nil {
			outcome = "refused"
		}
		return audit.Action{Type: audit.TypeCall, ID: "1", Start: at, Outcome: outcome, Task: "build", ExitCode: exit, Sandbox: sandbox, Error: err}
	}
	refusal := &audit.Error{Kind: "sandbox", Message: `task "build" was not run: the sandbox is unavailable: bubblewrap (bwrap) was not found on the PATH.`}
	cases := []struct {
		name   string
		action audit.Action
		want   []string
	}{
		{"off and ran", call(&audit.Sandbox{Mode: "off"}, new(0), nil), []string{line}},
		{"off and failed", call(&audit.Sandbox{Mode: "off"}, new(3), nil), []string{line}},
		{"off and refused", call(&audit.Sandbox{Mode: "off"}, nil, &audit.Error{Kind: "policy", Message: "no"}), []string{"policy: no"}},
		{"enforced", call(&audit.Sandbox{Enforced: true, Backend: "bubblewrap", Mode: "enforce"}, new(0), nil), nil},
		{"a record from before the sandbox", call(&audit.Sandbox{}, new(0), nil), nil},
		{"no sandbox object", call(nil, new(0), nil), nil},
		{"refused for the sandbox", call(&audit.Sandbox{Mode: "enforce"}, nil, refusal), []string{`sandbox: task "build" was not run: the sandbox is unavailable: bubblewrap (bwrap) was not found on the PATH.`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := rowOf(c.action, time.UTC, logTimeLayout)
			if strings.Join(row.details, "|") != strings.Join(c.want, "|") {
				t.Errorf("details\n got %q\nwant %q", row.details, c.want)
			}
		})
	}
}
