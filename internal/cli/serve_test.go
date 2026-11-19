package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/style"
	"github.com/rune-task-runner/rune/mcpserver"
)

// taskListFlags are the serve flags that take task names. They share one
// parser and one name check (spec 041 R4).
var taskListFlags = []string{"--allow-destructive", "--allow", "--deny"}

// Spec 031 contracts/cli-serve.md "Accepted forms": values split on commas,
// items trimmed, duplicates dropped, first-occurrence order kept.
func TestParseTaskNames(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"clean"}, []string{"clean"}},
		{[]string{"clean,reset"}, []string{"clean", "reset"}},
		{[]string{"clean", "reset"}, []string{"clean", "reset"}},
		{[]string{"clean, reset"}, []string{"clean", "reset"}},
		{[]string{"clean,clean"}, []string{"clean"}},
		{[]string{"reset", "clean,reset"}, []string{"reset", "clean"}},
		{[]string{"tools::clean"}, []string{"tools::clean"}},
	}
	for _, flag := range taskListFlags {
		for _, c := range cases {
			got, err := parseTaskNames(flag, c.in)
			if err != nil {
				t.Errorf("parseTaskNames(%s, %q): unexpected error %v", flag, c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseTaskNames(%s, %q) = %q, want %q", flag, c.in, got, c.want)
			}
		}
	}
}

// Spec 031 FR-009, spec 041 FR-010: an empty name, from an empty value or an
// empty item between commas, is a usage error naming the flag.
func TestParseTaskNamesRejectsEmptyName(t *testing.T) {
	wants := map[string]string{
		"--allow-destructive": "--allow-destructive: empty task name",
		"--allow":             "--allow: empty task name",
		"--deny":              "--deny: empty task name",
	}
	for _, flag := range taskListFlags {
		for _, in := range [][]string{{""}, {"clean,"}, {", clean"}, {"clean,,reset"}, {"  "}, {"clean", ""}} {
			_, err := parseTaskNames(flag, in)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Errorf("parseTaskNames(%s, %q) error = %v, want *UsageError", flag, in, err)
				continue
			}
			if got := err.Error(); got != wants[flag] {
				t.Errorf("parseTaskNames(%s, %q) error = %q, want %q", flag, in, got, wants[flag])
			}
		}
	}
}

// grantTasks is a task map with a destructive task, a plain one, a private
// destructive one, one restricted to another OS, and an imported destructive
// one, keyed the way indexTasks keys a composed file.
func grantTasks(t *testing.T) map[string]*ast.Task {
	t.Helper()
	src := "[confirm]\nclean:\n    @echo c\n\nbuild:\n    @echo b\n\n[private]\n[confirm]\n_wipe:\n    @echo w\n\n[windows]\n[confirm]\nwinonly:\n    @echo x\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	tasks := indexTasks(f)
	imported := *tasks["clean"]
	imported.Name = "tools::clean"
	tasks["tools::clean"] = &imported
	return tasks
}

// Spec 031 FR-008, spec 041 FR-009: an unknown name is a usage error naming
// the flag and the name, with the closest task suggested when one is near.
// Known names of every kind are accepted.
func TestCheckTaskNames(t *testing.T) {
	tasks := grantTasks(t)
	type want struct{ destructive, allow, deny string }
	cases := []struct {
		names []string
		want  want
	}{
		{[]string{"claen"}, want{
			`unknown task in --allow-destructive: claen (did you mean "clean"?)`,
			`unknown task in --allow: claen (did you mean "clean"?)`,
			`unknown task in --deny: claen (did you mean "clean"?)`,
		}},
		{[]string{"zzz"}, want{
			"unknown task in --allow-destructive: zzz",
			"unknown task in --allow: zzz",
			"unknown task in --deny: zzz",
		}},
		{[]string{"*"}, want{
			"unknown task in --allow-destructive: *",
			"unknown task in --allow: *",
			"unknown task in --deny: *",
		}},
		{[]string{"tools__clean"}, want{
			`unknown task in --allow-destructive: tools__clean (did you mean "tools::clean"?)`,
			`unknown task in --allow: tools__clean (did you mean "tools::clean"?)`,
			`unknown task in --deny: tools__clean (did you mean "tools::clean"?)`,
		}},
		{[]string{"clean", "nope"}, want{
			"unknown task in --allow-destructive: nope",
			"unknown task in --allow: nope",
			"unknown task in --deny: nope",
		}},
		{[]string{"clean", "build", "_wipe", "winonly", "tools::clean"}, want{}},
	}
	for _, c := range cases {
		for flag, wantMsg := range map[string]string{
			"--allow-destructive": c.want.destructive, "--allow": c.want.allow, "--deny": c.want.deny,
		} {
			for range 2 { // candidates are sorted, so the suggestion never varies
				err := checkTaskNames(flag, c.names, tasks)
				if wantMsg == "" {
					if err != nil {
						t.Errorf("checkTaskNames(%s, %q) = %v, want nil", flag, c.names, err)
					}
					continue
				}
				var ue *UsageError
				if !errors.As(err, &ue) || err.Error() != wantMsg {
					t.Errorf("checkTaskNames(%s, %q) = %v, want usage error %q", flag, c.names, err, wantMsg)
				}
			}
		}
	}
}

// FR-011: a grant for a task without [confirm] warns once and has no effect.
func TestWarnNonDestructive(t *testing.T) {
	tasks := grantTasks(t)
	var buf bytes.Buffer
	warnNonDestructive(&buf, style.New(false, &buf), []string{"build", "clean"}, tasks)
	want := "warning: --allow-destructive build has no effect: task \"build\" is not destructive\n"
	if buf.String() != want {
		t.Errorf("warning = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	warnNonDestructive(&buf, style.New(false, &buf), []string{"_wipe", "winonly", "clean", "tools::clean"}, tasks)
	if buf.Len() != 0 {
		t.Errorf("destructive grants warned: %q", buf.String())
	}
}

// FR-010: a bad --allow-destructive value stops ServeMCP before any Runefile
// code runs, including the [context] hook.
func TestServeMCPUsageErrorRunsNoRunefileCode(t *testing.T) {
	const src = "# Health.\n[context]\nhealth:\n    @echo ok > context.done\n\n# Clean.\n[confirm]\nclean:\n    @echo c\n"
	for _, v := range [][]string{{""}, {"clean,"}, {"claen"}, {"*"}} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		err := ServeMCP(Options{Cwd: dir, Stderr: &stderr, MCPAllowDestructive: v}, false, "", "")
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("ServeMCP with %q: error = %v, want *UsageError", v, err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
			t.Errorf("ServeMCP with %q ran the [context] hook before failing", v)
		}
	}

	// The empty name is reported before the Runefile is even looked for.
	err := ServeMCP(Options{Cwd: t.TempDir(), Stderr: &bytes.Buffer{}, MCPAllowDestructive: []string{""}}, false, "", "")
	if err == nil || err.Error() != "--allow-destructive: empty task name" {
		t.Errorf("empty name without a Runefile: error = %v, want the empty-name usage error", err)
	}
}

// With no grant and no filter the line is byte-identical to before spec 031
// (spec 031 FR-012, spec 041 FR-016). A filter adds the exposed count (spec
// 041 FR-014).
func TestStartupLine(t *testing.T) {
	both := []string{"clean", "reset"}
	one := []string{"clean"}
	two := &exposure{exposed: 2, exposable: 14}
	cases := []struct {
		http    bool
		addr    string
		exp     *exposure
		granted []string
		want    string
	}{
		{false, "", nil, nil, "rune MCP server on stdio"},
		{false, "", nil, both, "rune MCP server on stdio (destructive tasks allowed: clean, reset)"},
		{true, "127.0.0.1:7777", nil, nil, "rune MCP server on http://127.0.0.1:7777 (token required)"},
		{true, "127.0.0.1:7777", nil, both, "rune MCP server on http://127.0.0.1:7777 (token required; destructive tasks allowed: clean, reset)"},
		{false, "", two, nil, "rune MCP server on stdio (2 of 14 tasks exposed)"},
		{false, "", two, one, "rune MCP server on stdio (2 of 14 tasks exposed; destructive tasks allowed: clean)"},
		{true, "127.0.0.1:7777", two, nil, "rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed)"},
		{true, "127.0.0.1:7777", two, one, "rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed; destructive tasks allowed: clean)"},
		{false, "", &exposure{exposed: 0, exposable: 14}, nil, "rune MCP server on stdio (0 of 14 tasks exposed)"},
	}
	for _, c := range cases {
		if got := startupLine(c.http, c.addr, c.exp, c.granted, ""); got != c.want {
			t.Errorf("startupLine(%v, %q, %v, %q) = %q, want %q", c.http, c.addr, c.exp, c.granted, got, c.want)
		}
	}
}

// Spec 031 FR-012: the startup line lists only grants that take effect on
// this host. A name whose task has no [confirm], or whose task is restricted
// to another OS, is left out; a private destructive task stays, since a
// dependency or hook can reach it.
func TestDestructiveOnly(t *testing.T) {
	f, diags := parser.Parse("Runefile", "[confirm]\nclean:\n    @echo c\n\nbuild:\n    @echo b\n\n[confirm]\nreset:\n    @echo r\n\n"+
		"[windows]\n[confirm]\nwinonly:\n    @echo w\n\n[linux]\n[confirm]\nlinuxonly:\n    @echo l\n\n[private]\n[confirm]\n_wipe:\n    @echo p\n")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	names := []string{"reset", "build", "winonly", "clean", "missing", "linuxonly", "_wipe"}
	got := destructiveOnly(names, indexTasks(f), "linux")
	if want := []string{"reset", "clean", "linuxonly", "_wipe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("destructiveOnly on linux = %q, want %q", got, want)
	}
	got = destructiveOnly(names, indexTasks(f), "windows")
	if want := []string{"reset", "winonly", "clean", "_wipe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("destructiveOnly on windows = %q, want %q", got, want)
	}
}

// Spec 041 FR-009 to FR-011: a bad --allow or --deny value stops ServeMCP
// before any Runefile code runs, and the flags are checked in a fixed order.
func TestServeMCPFilterUsageErrors(t *testing.T) {
	const src = "# Health.\n[context]\nhealth:\n    @echo ok > context.done\n\n# Build.\nbuild:\n    @echo b\n\n# Clean.\n[confirm]\nclean:\n    @echo c\n"
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"unknown allow", Options{MCPAllow: []string{"biuld"}}, `unknown task in --allow: biuld (did you mean "build"?)`},
		{"unknown deny", Options{MCPDeny: []string{"biuld"}}, `unknown task in --deny: biuld (did you mean "build"?)`},
		{"empty allow", Options{MCPAllow: []string{""}}, "--allow: empty task name"},
		{"empty deny item", Options{MCPDeny: []string{"build,"}}, "--deny: empty task name"},
		{"wildcard", Options{MCPAllow: []string{"*"}}, "unknown task in --allow: *"},
		{
			"destructive name first",
			Options{MCPAllowDestructive: []string{"claen"}, MCPAllow: []string{"biuld"}},
			`unknown task in --allow-destructive: claen (did you mean "clean"?)`,
		},
		{
			"allow before deny",
			Options{MCPAllow: []string{"biuld"}, MCPDeny: []string{"zzz"}},
			`unknown task in --allow: biuld (did you mean "build"?)`,
		},
		{
			"empty item before unknown name",
			Options{MCPAllow: []string{"biuld"}, MCPDeny: []string{""}},
			"--deny: empty task name",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			opts := c.opts
			opts.Cwd, opts.Stderr = dir, &bytes.Buffer{}
			err := ServeMCP(opts, false, "", "")
			var ue *UsageError
			if !errors.As(err, &ue) || err.Error() != c.want {
				t.Errorf("error = %v, want usage error %q", err, c.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
				t.Error("the [context] hook ran before the usage error")
			}
		})
	}
}

// Spec 041 FR-012: a name that can never be a tool on any host warns once per
// flag. A task restricted to another OS is silent.
func TestWarnNeverExposed(t *testing.T) {
	f, diags := parser.Parse("Runefile", "build:\n    @echo b\n\n_setup:\n    @echo s\n\n[private]\nhidden:\n    @echo h\n\n"+
		"[context]\nhealth:\n    @echo ok\n\n[windows]\nwinonly:\n    @echo w\n\n[linux]\nlinuxonly:\n    @echo l\n")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	tasks := indexTasks(f)
	names := []string{"build", "_setup", "winonly", "hidden", "health", "linuxonly"}
	for _, flag := range []string{"--allow", "--deny"} {
		var buf bytes.Buffer
		warnNeverExposed(&buf, style.New(false, &buf), flag, names, tasks)
		want := "warning: " + flag + " _setup has no effect: task \"_setup\" is never exposed to agents\n" +
			"warning: " + flag + " hidden has no effect: task \"hidden\" is never exposed to agents\n" +
			"warning: " + flag + " health has no effect: task \"health\" is never exposed to agents\n"
		if buf.String() != want {
			t.Errorf("%s warnings = %q, want %q", flag, buf.String(), want)
		}
	}
	var buf bytes.Buffer
	warnNeverExposed(&buf, style.New(false, &buf), "--allow", []string{"build", "winonly", "linuxonly"}, tasks)
	if buf.Len() != 0 {
		t.Errorf("exposable and other-OS names warned: %q", buf.String())
	}
}

// Spec 041 FR-013.
func TestWarnNoTasksExposed(t *testing.T) {
	var buf bytes.Buffer
	warnNoTasksExposed(&buf, style.New(false, &buf))
	if want := "warning: no tasks are exposed to agents; check --allow and --deny\n"; buf.String() != want {
		t.Errorf("warning = %q, want %q", buf.String(), want)
	}
}

// Spec 042 FR-023: a bad --output-limit value stops ServeMCP before any
// Runefile code runs, and before the Runefile is looked for.
func TestServeMCPRejectsBadOutputLimit(t *testing.T) {
	const src = "# Health.\n[context]\nhealth:\n    @echo ok > context.done\n\n# Build.\nbuild:\n    @echo b\n"
	for _, v := range []string{"lots", "", "-1", "0", "10"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		err := ServeMCP(Options{Cwd: dir, Stderr: &bytes.Buffer{}, MCPOutputLimit: &v}, false, "", "")
		want := `--output-limit: invalid size "` + v + `": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`
		var ue *UsageError
		if !errors.As(err, &ue) || err.Error() != want {
			t.Errorf("ServeMCP with %q: error = %v\nwant usage error %q", v, err, want)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
			t.Errorf("ServeMCP with %q ran the [context] hook before failing", v)
		}
	}

	bad := "lots"
	err := ServeMCP(Options{Cwd: t.TempDir(), Stderr: &bytes.Buffer{}, MCPOutputLimit: &bad}, false, "", "")
	if err == nil || !strings.HasPrefix(err.Error(), "--output-limit: ") {
		t.Errorf("bad size without a Runefile: error = %v, want the size usage error", err)
	}
}

// cleartextLine is the stderr line of spec 045, with colors off.
const cleartextLine = "warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext\n"

// Spec 045 FR-006: the transport's warning is printed like the other startup
// warnings, with only the word "warning" styled.
func TestWarnTransport(t *testing.T) {
	var buf bytes.Buffer
	warnTransport(&buf, style.New(false, &buf))("message")
	if got := buf.String(); got != "warning: message\n" {
		t.Errorf("plain = %q, want %q", got, "warning: message\n")
	}

	buf.Reset()
	theme := style.New(true, &buf)
	warnTransport(&buf, theme)("message")
	styled := theme.Warning.Render("warning")
	if styled == "warning" {
		t.Fatal("the colored theme does not style the word")
	}
	if got, want := buf.String(), styled+": message\n"; got != want {
		t.Errorf("styled = %q, want %q", got, want)
	}
}

// The address used when --addr is not given must never be one that warns
// (spec 045 FR-003).
func TestDefaultHTTPAddrIsLoopback(t *testing.T) {
	if defaultHTTPAddr != "127.0.0.1:7777" {
		t.Errorf("defaultHTTPAddr = %q, want 127.0.0.1:7777", defaultHTTPAddr)
	}
	addr, err := net.ResolveTCPAddr("tcp", defaultHTTPAddr)
	if err != nil || !addr.IP.IsLoopback() {
		t.Errorf("defaultHTTPAddr resolves to %v (%v), want a loopback address", addr, err)
	}
}

// With no --addr the server uses the default address and prints no cleartext
// warning (spec 045 US2 scenario 1). The context is done from the start, so
// the server stops as soon as it is up, and the test holds whether or not
// the default port is free on this machine.
func TestServeMCPDefaultAddrIsQuiet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	err := ServeMCP(Options{Cwd: dir, Stderr: &stderr, Ctx: ctx, MCPNoReload: true, SandboxEnv: "off"}, true, "", tokenFile(t))
	if err == nil {
		t.Error("ServeMCP returned nil for a server that was stopped")
	}
	got := stderr.String()
	if !strings.Contains(got, "rune MCP server on http://127.0.0.1:7777 (token required; sandbox: off, tasks run unsandboxed)\n") {
		t.Errorf("stderr lacks the startup line of the default address:\n%s", got)
	}
	if strings.Contains(got, "cleartext") {
		t.Errorf("the default address printed the cleartext warning:\n%s", got)
	}
}

// serveHTTPForTest runs ServeMCP over HTTP on addr in a temp directory. stop
// cancels it and returns what ServeMCP returned.
func serveHTTPForTest(t *testing.T, addr string) (dir string, stderr *lockedBuffer, stop func() error) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr = &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	opts := Options{Cwd: dir, Stderr: stderr, Ctx: ctx, SandboxEnv: "off"}
	token := tokenFile(t)
	done := make(chan error, 1)
	go func() { done <- ServeMCP(opts, true, addr, token) }()
	stopped := false
	stop = func() error {
		stopped = true
		cancel()
		return <-done
	}
	t.Cleanup(func() {
		if !stopped {
			_ = stop()
		}
	})
	return dir, stderr, stop
}

// Spec 045 FR-001, FR-002, FR-004: a bind beyond loopback prints the warning
// once, after the startup line, and the server runs and stops as any other.
// A reload does not repeat it: the listener is not reopened.
func TestServeMCPWarnsOnNonLoopbackBind(t *testing.T) {
	dir, stderr, stop := serveHTTPForTest(t, "0.0.0.0:0")
	fx := &reloadFixture{t: t, stderr: stderr}
	fx.waitStderr(cleartextLine, 1)
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(reloadBase+"\n# Lints.\nlint:\n    @echo linted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.waitStderr("reloaded Runefile: 1 tool added, 0 removed, 0 changed", 1)
	if err := stop(); !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ServeMCP = %v, want http.ErrServerClosed as for a loopback server", err)
	}
	got := stderr.String()
	if n := strings.Count(got, "cleartext"); n != 1 {
		t.Errorf("stderr holds the warning %d times, want once:\n%s", n, got)
	}
	startup := strings.Index(got, "rune MCP server on http://0.0.0.0:0 (token required; sandbox: off, tasks run unsandboxed)\n")
	if startup < 0 || startup > strings.Index(got, cleartextLine) {
		t.Errorf("want the startup line, then the warning:\n%s", got)
	}
}

// Spec 045 FR-003: a loopback server prints no cleartext warning.
func TestServeMCPQuietOnLoopbackBind(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	_, stderr, stop := serveHTTPForTest(t, addr)
	// An answer proves the bind happened, so the absence below means
	// something.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for deadline := time.Now().Add(reloadWait); ; time.Sleep(10 * time.Millisecond) {
		resp, err := client.Post("http://"+addr, "application/json", nil)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status without a token = %d, want 401", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer from %s: %v\n%s", addr, err, stderr.String())
		}
	}
	if err := stop(); !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ServeMCP = %v, want http.ErrServerClosed", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "rune MCP server on http://"+addr+" (token required; sandbox: off, tasks run unsandboxed)\n") {
		t.Errorf("stderr lacks the startup line:\n%s", got)
	}
	if strings.Contains(got, "cleartext") {
		t.Errorf("a loopback server printed the cleartext warning:\n%s", got)
	}
}

// Spec 049: `rune mcp` and `rune serve` ask the person at the client, and
// name rune as the command to run a task with.
func TestServeMCPOptions(t *testing.T) {
	got := serveMCPOptions("1.2.3", "rune -f /repo/Runefile", []string{"clean"}, []string{"build"}, []string{"deploy"}, 4096, "hello", true)
	want := mcpserver.Options{
		AllowDestructive:     []string{"clean"},
		AllowDestructiveFlag: "--allow-destructive",
		AskApproval:          true,
		RunCommand:           "rune -f /repo/Runefile",
		AllowList:            []string{"build"},
		DenyList:             []string{"deploy"},
		OutputLimit:          4096,
		Version:              "1.2.3",
		Instructions:         "hello",
		Reloadable:           true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serveMCPOptions = %+v, want %+v", got, want)
	}
}

// The command a refusal names has to reach the Runefile the server serves:
// with --file, a bare `rune clean` in the operator's directory would not.
func TestRunCommand(t *testing.T) {
	cases := []struct {
		flag, runefile, want string
	}{
		{"", "/repo/Runefile", "rune"},
		{"Runefile", "/repo/Runefile", "rune -f /repo/Runefile"},
		{"x", `C:\repo\Runefile`, `rune -f C:\repo\Runefile`},
		{"x", "/my project/Runefile", "rune -f '/my project/Runefile'"},
		{"x", "/srv/R&D/Runefile", "rune -f '/srv/R&D/Runefile'"},
	}
	for _, c := range cases {
		if got := runCommand(c.flag, c.runefile); got != c.want {
			t.Errorf("runCommand(%q, %q) = %q, want %q", c.flag, c.runefile, got, c.want)
		}
	}
}

// A path is pasted into a shell with the command, so every character a shell
// would act on is inside quotes, and a quote in the path does not end them.
func TestShellWord(t *testing.T) {
	cases := []struct {
		path, goos, want string
	}{
		{"/repo/Runefile", "linux", "/repo/Runefile"},
		{`C:\Users\RUNNER~1\repo\Runefile`, "windows", `C:\Users\RUNNER~1\repo\Runefile`},
		{"/my project/Runefile", "linux", "'/my project/Runefile'"},
		{"/srv/a;b/Runefile", "linux", "'/srv/a;b/Runefile'"},
		{"/srv/$HOME/Runefile", "darwin", "'/srv/$HOME/Runefile'"},
		{"/srv/copy (2)/Runefile", "linux", "'/srv/copy (2)/Runefile'"},
		{"/home/kim/Kim's work/Runefile", "linux", `'/home/kim/Kim'\''s work/Runefile'`},
		{"/home/kim/Kims'/Runefile", "darwin", `'/home/kim/Kims'\''/Runefile'`},
		{`C:\Users\Kim's work\Runefile`, "windows", `'C:\Users\Kim''s work\Runefile'`},
	}
	for _, c := range cases {
		if got := shellWord(c.path, c.goos); got != c.want {
			t.Errorf("shellWord(%q, %q) = %q, want %q", c.path, c.goos, got, c.want)
		}
	}
}

// Spec 053: every server states its sandbox, last in the startup line.
func TestStartupLineSandbox(t *testing.T) {
	cases := []struct {
		http    bool
		addr    string
		granted []string
		part    string
		want    string
	}{
		{false, "", nil, "sandbox: bubblewrap", "rune MCP server on stdio (sandbox: bubblewrap)"},
		{true, "127.0.0.1:7777", nil, "sandbox: bubblewrap", "rune MCP server on http://127.0.0.1:7777 (token required; sandbox: bubblewrap)"},
		{false, "", []string{"clean"}, "sandbox: off, tasks run unsandboxed", "rune MCP server on stdio (destructive tasks allowed: clean; sandbox: off, tasks run unsandboxed)"},
		{false, "", nil, "sandbox: unavailable, calls are refused", "rune MCP server on stdio (sandbox: unavailable, calls are refused)"},
	}
	for _, c := range cases {
		if got := startupLine(c.http, c.addr, nil, c.granted, c.part); got != c.want {
			t.Errorf("startupLine = %q, want %q", got, c.want)
		}
	}
}

// A mode that cannot be read is a usage error before any Runefile code runs.
func TestServeMCPSandboxMode(t *testing.T) {
	src := "[context]\nhealth:\n    @touch context.done\n\nbuild:\n    @echo hi\n"
	bad := "maybe"
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"the flag", Options{MCPSandbox: &bad, SandboxEnv: "off"}, `--sandbox: "maybe" is not a mode; use enforce or off`},
		{"the variable", Options{SandboxEnv: "maybe"}, `RUNE_SANDBOX: "maybe" is not a mode; use enforce or off`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			opts := c.opts
			opts.Cwd, opts.Stderr = dir, &bytes.Buffer{}
			err := ServeMCP(opts, false, "", "")
			var usage *UsageError
			if !errors.As(err, &usage) || err.Error() != c.want {
				t.Errorf("error = %v, want usage error %q", err, c.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "context.done")); statErr == nil {
				t.Error("the [context] hook ran before the usage error")
			}
		})
	}
}

// A server that cannot confine starts, says so, and runs no [context] task.
func TestServeMCPUnavailableSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook's marker needs a POSIX touch")
	}
	// An empty PATH has no backend on Linux, and other systems have none.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	src := "[context]\nhealth:\n    @: > context.done\n\nbuild:\n    @echo hi\n"
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	_ = ServeMCP(Options{Cwd: dir, Stderr: &stderr, Ctx: ctx, MCPNoReload: true, AuditDir: t.TempDir()}, true, "", tokenFile(t))
	got := stderr.String()
	for _, want := range []string{
		"warning: sandbox: ", "Every task call will be refused.",
		"(token required; sandbox: unavailable, calls are refused)\n",
		"context hook \"health\" failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "warning: sandbox: ") > strings.Index(got, "rune MCP server on") {
		t.Errorf("the warning came after the startup line:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "context.done")); err == nil {
		t.Error("the [context] hook ran with no sandbox")
	}
}

// loadStatic is the half of a load that evaluates nothing (spec 056): it
// reads, parses, composes and analyzes, and stops before the settings.
func TestLoadStatic(t *testing.T) {
	write := func(t *testing.T, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "Runefile")
	}
	load := func(runefile string) (*ast.File, string, error) {
		var stderr bytes.Buffer
		file, _, err := loadStatic(Options{Stderr: &stderr}, runefile, false, &pathRecorder{})
		return file, stderr.String(), err
	}

	t.Run("a valid Runefile comes back composed", func(t *testing.T) {
		runefile := write(t, map[string]string{
			"Runefile": "mod ops\n\n# Build.\nbuild:\n    @echo build\n",
			"ops.rune": "# Restart.\nrestart:\n    @echo restart\n",
		})
		file, _, err := load(runefile)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, task := range file.Tasks {
			names = append(names, task.Name)
		}
		if want := []string{"build", "ops::restart"}; !slices.Equal(names, want) {
			t.Errorf("tasks = %v, want %v", names, want)
		}
	})

	t.Run("a parse error is a validation error with its diagnostic", func(t *testing.T) {
		_, stderr, err := load(write(t, map[string]string{"Runefile": "build\n    @echo build\n"}))
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(stderr, "Runefile:1") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})

	t.Run("an analyzer error is a validation error with its diagnostic", func(t *testing.T) {
		_, stderr, err := load(write(t, map[string]string{"Runefile": "build: missing\n    @echo build\n"}))
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(stderr, "missing") {
			t.Errorf("err = %v, stderr = %q", err, stderr)
		}
	})

	t.Run("a Runefile that cannot be read is a usage error", func(t *testing.T) {
		_, _, err := load(filepath.Join(t.TempDir(), "Runefile"))
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("err = %v, want a usage error", err)
		}
	})

	// The setting fails when it is evaluated. A full load evaluates it and
	// stops; the static load does not look at it.
	t.Run("nothing is evaluated", func(t *testing.T) {
		runefile := write(t, map[string]string{
			"Runefile": "set working-directory := error(\"evaluated\")\n\n# Build.\nbuild:\n    @echo build\n",
		})
		if _, stderr, err := load(runefile); err != nil {
			t.Errorf("loadStatic: %v\n%s", err, stderr)
		}
		var stderr bytes.Buffer
		if _, err := loadModule(Options{Stderr: &stderr}, runefile, false); err == nil || !strings.Contains(stderr.String(), "evaluated") {
			t.Errorf("a full load of the same Runefile: err = %v, stderr = %q; want the setting's error", err, stderr.String())
		}
	})
}
