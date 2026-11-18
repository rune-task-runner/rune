package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/audit"
)

// Spec 052, quickstart R: the flags of `rune log`, on the built binary, over
// a log made by real calls and by day files the test writes.

// writeDay writes records into the day file of at in logDir.
func writeDay(t *testing.T, logDir string, at time.Time, records ...audit.Record) {
	t.Helper()
	var data []byte
	for _, rec := range records {
		line, err := rec.Encode()
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
	}
	path := filepath.Join(logDir, at.UTC().Format("2006-01-02")+".jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cliIDs(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	res := run(t, dir, env, append([]string{"log", "--json"}, args...)...)
	if res.code != 0 {
		t.Fatalf("rune log %v exited %d: %s", args, res.code, res.stderr)
	}
	var ids []string
	for line := range strings.Lines(res.stdout) {
		var a audit.Action
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		ids = append(ids, a.ID[:2])
	}
	return strings.Join(ids, ",")
}

func TestRuneLogFlags(t *testing.T) {
	dir, env, _ := auditProject(t, auditRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, env, nil)
	callText(t, cs, "build", nil)
	live := lastLogCall(t, dir, env)
	logDir := logDirOf(t, dir, env)

	// Two fixed days, far from today, so no flag depends on the clock.
	day1 := time.Date(2026, 3, 10, 23, 30, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC)
	exit3 := 3
	ms := int64(1500)
	writeDay(t, logDir, day1,
		audit.Record{Type: audit.TypeCall, Phase: audit.PhaseStart, ID: "aa00000000000000", Time: day1, Tool: "deploy", Task: "deploy", Session: "5f0e7a11c3d2b694", Client: &audit.Client{Name: "Claude-Code", Version: "2.1.0"}},
		audit.Record{Type: audit.TypeCall, Phase: audit.PhaseEnd, ID: "aa00000000000000", Time: day1.Add(1500 * time.Millisecond), Outcome: "failed", ExitCode: &exit3, DurationMs: &ms},
	)
	writeDay(t, logDir, day2,
		audit.Record{Type: audit.TypeCall, Phase: audit.PhaseStart, ID: "bb00000000000000", Time: day2, Tool: "mod__lint", Task: "mod::lint", Session: "9c00000000000000", Client: &audit.Client{Name: "codex"}},
		audit.Record{Type: audit.TypeCall, Phase: audit.PhaseEnd, ID: "bb00000000000000", Time: day2, Outcome: "refused", Error: &audit.Error{Kind: "policy", Message: "no"}},
		audit.Record{Type: audit.TypeRequest, ID: "cc00000000000000", Time: day2.Add(time.Minute), Method: "tools/list", Outcome: "ok", Session: "9c00000000000000"},
		audit.Record{Type: audit.TypeCall, Phase: audit.PhaseStart, ID: "dd00000000000000", Time: day2.Add(time.Hour), Tool: "slow", Task: "slow", Session: "9c00000000000000"},
	)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a day", []string{"--tz", "UTC", "--date", "2026-03-12"}, "bb,dd"},
		{"a day in another zone", []string{"--tz", "Europe/Kyiv", "--date", "2026-03-11"}, "aa"},
		{"the same instant in UTC", []string{"--tz", "UTC", "--date", "2026-03-11"}, ""},
		{"a range of days", []string{"--tz", "UTC", "--since", "2026-03-10", "--until", "2026-03-12"}, "aa,bb,dd"},
		{"a range of times", []string{"--since", "2026-03-12T09:00:00Z", "--until", "2026-03-12T10:00:00Z"}, "bb"},
		{"with requests", []string{"--tz", "UTC", "--date", "2026-03-12", "--all"}, "bb,cc,dd"},
		{"a task", []string{"--until", "2026-03-31", "--task", "deploy"}, "aa"},
		{"a tool name", []string{"--until", "2026-03-31", "--task", "mod__lint"}, "bb"},
		{"outcomes", []string{"--until", "2026-03-31", "--outcome", "failed,incomplete"}, "aa,dd"},
		{"a client", []string{"--until", "2026-03-31", "--client", "claude-code"}, "aa"},
		{"a session prefix", []string{"--until", "2026-03-31", "--session", "9c", "--session", "ffff"}, "bb,dd"},
		{"two flags", []string{"--until", "2026-03-31", "--session", "9c", "--outcome", "refused"}, "bb"},
		{"a duration", []string{"--since", "10m"}, live.ID[:2]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cliIDs(t, dir, env, tt.args...); got != tt.want {
				t.Errorf("rune log %v listed %s, want %s", tt.args, got, tt.want)
			}
		})
	}

	t.Run("the JSON object", func(t *testing.T) {
		res := run(t, dir, env, "log", "--json", "--tz", "UTC", "--date", "2026-03-10")
		want := `{"schemaVersion":1,"type":"call","id":"aa00000000000000","start":"2026-03-10T23:30:00Z","end":"2026-03-10T23:30:01.5Z","durationMs":1500,"outcome":"failed","exitCode":3,"session":"5f0e7a11c3d2b694","client":{"name":"Claude-Code","version":"2.1.0","reported":true},"tool":"deploy","task":"deploy"}` + "\n"
		if res.stdout != want || res.stderr != "" || res.code != 0 {
			t.Errorf("exit %d, stderr %q\n got %s\nwant %s", res.code, res.stderr, res.stdout, want)
		}
	})

	t.Run("the text listing of a day", func(t *testing.T) {
		res := run(t, dir, env, "log", "--tz", "Europe/Kyiv", "--date", "2026-03-11")
		project := lastLogCall(t, dir, env).Project.Runefile
		want := "Agent actions on 2026-03-11 (Europe/Kyiv, UTC+02:00) for " + project + "\n\n" +
			"01:30:00  failed  deploy  exit 3  1.5s  Claude-Code 2.1.0  aa00000000000000\n"
		if res.stdout != want || res.code != 0 {
			t.Errorf("exit %d\n got %q\nwant %q", res.code, res.stdout, want)
		}
	})

	t.Run("no action", func(t *testing.T) {
		res := run(t, dir, env, "log", "--tz", "UTC", "--date", "2020-01-01")
		if res.code != 0 || !strings.HasSuffix(res.stdout, "\n\nNo agent actions.\n") {
			t.Errorf("exit %d, %q", res.code, res.stdout)
		}
		if res := run(t, dir, env, "log", "--json", "--date", "2020-01-01"); res.code != 0 || res.stdout != "" {
			t.Errorf("--json: exit %d, %q", res.code, res.stdout)
		}
	})

	t.Run("usage errors exit 2 and list nothing", func(t *testing.T) {
		for args, want := range map[string]string{
			"--outcome nope":                        `rune: --outcome: unknown outcome "nope" (one of completed, failed, refused, invalid-arguments, unknown-tool, cancelled, timed-out, error, incomplete, ok)`,
			"--tz Mars/Olympus":                     `rune: --tz: unknown time zone "Mars/Olympus" (an IANA name such as Europe/Kyiv, UTC, or local)`,
			"--date 2026-13-01":                     `rune: --date: "2026-13-01" is not a day (YYYY-MM-DD)`,
			"--date 2026-03-10 --since 2h":          `rune: --date cannot be combined with --since or --until`,
			"--since soon":                          `rune: --since: "soon" is not a day (YYYY-MM-DD), a time (RFC 3339) or a duration (such as 2h or 7d)`,
			"--since 2026-03-12 --until 2026-03-10": `rune: --since is after --until`,
			"--dir --json":                          `rune: --dir cannot be combined with other flags`,
			"--task a,,b":                           `rune: --task: empty value`,
		} {
			res := run(t, dir, env, append([]string{"log"}, strings.Fields(args)...)...)
			if res.code != 2 || strings.TrimSpace(res.stderr) != want || res.stdout != "" {
				t.Errorf("rune log %s: exit %d, stdout %q\n got %q\nwant %q", args, res.code, res.stdout, strings.TrimSpace(res.stderr), want)
			}
		}
	})

	t.Run("no Runefile", func(t *testing.T) {
		if res := run(t, t.TempDir(), env, "log"); res.code != 2 || res.stdout != "" {
			t.Errorf("exit %d, stdout %q", res.code, res.stdout)
		}
	})

	t.Run("a day file that cannot be read", func(t *testing.T) {
		broken := filepath.Join(logDir, "2026-03-15.jsonl")
		if err := os.Mkdir(broken, 0o700); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(broken) }()
		if res := run(t, dir, env, "log", "--tz", "UTC", "--date", "2026-03-15"); res.code != 1 || res.stdout != "" {
			t.Errorf("exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
		}
	})

	t.Run("a task named log", func(t *testing.T) {
		logTaskDir := writeRunefile(t, "log:\n    @echo task > log.done\n")
		res := run(t, logTaskDir, env, "log")
		if res.code != 0 || !strings.Contains(res.stderr, `note: "log" is also a task of this Runefile; run it with: rune -- log`) || hasMarker(logTaskDir, "log.done") {
			t.Errorf("exit %d, stderr %q", res.code, res.stderr)
		}
		if res := run(t, logTaskDir, env, "--", "log"); res.code != 0 || !hasMarker(logTaskDir, "log.done") {
			t.Errorf("rune -- log did not run the task: exit %d, %s", res.code, res.stderr)
		}
	})

	t.Run("reading writes nothing", func(t *testing.T) {
		entries, err := os.ReadDir(logDir)
		if err != nil {
			t.Fatal(err)
		}
		// Today's file, the two written days and project.json.
		if len(entries) != 4 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("files in the log directory: %v", names)
		}
		if hasMarker(dir, "check.done") || hasMarker(dir, "deploy.done") {
			t.Error("rune log ran a task")
		}
	})
}
