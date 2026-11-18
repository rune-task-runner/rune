//go:build unix

package integration

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/sandbox"
)

const hostRunefile = `# Try the host's sockets and look around.
sockets run tmp ssh docker abstract home:
    -@./probe unix {{run}}
    -@./probe unix {{tmp}}
    -@./probe unix {{ssh}}
    -@./probe unix {{docker}}
    -@./probe abstract {{abstract}}
    -@./probe ls /dev
    -@./probe ls {{home}}/.ssh
    -@./probe write {{home}}/.ssh/planted
    -@./probe read {{home}}/.netrc
    -@./probe ls /proc

# The same with the network.
[network]
sockets-net run tmp ssh docker abstract home:
    -@./probe unix {{run}}
    -@./probe unix {{tmp}}
    -@./probe unix {{ssh}}
    -@./probe unix {{docker}}
    -@./probe abstract {{abstract}}
    -@./probe ls /dev
    -@./probe ls {{home}}/.ssh
    -@./probe write {{home}}/.ssh/planted
    -@./probe read {{home}}/.netrc
    -@./probe ls /proc

# List the open files.
fds:
    @./probe fds

# Open the terminal.
tty:
    -@./probe tty

# Report what the process may do.
status:
    @./probe status

# Write where the caller says.
write path:
    -@./probe write {{path}}

# Connect where the caller says.
connect addr:
    -@./probe connect {{addr}}

# Leave a process behind and wait.
hold:
    @./probe daemon hb
    @./probe spawn 50
    @./probe sleep 60

# Leave a process behind and end.
leave:
    @./probe daemon hb
    @./probe sleep 0.3

# Succeed.
fine:
    @echo fine
`

// unixListener listens on a Unix socket for the life of the test.
func unixListener(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
}

// Quickstart I: the host's sockets, agents, devices and credential stores
// are out of a body's reach, with or without [network].
func TestSandboxRuleHidesHostSockets(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)

	// A socket in the runtime directory, where the container daemon, the
	// session bus and the desktop live. Without a writable place under /run
	// the path is left dangling, and the line still has to fail.
	runSock := "/run/rune-probe-missing.sock"
	for _, dir := range []string{os.Getenv("XDG_RUNTIME_DIR"), "/run"} {
		if dir == "" || !sandbox.Within("/run", dir) {
			continue
		}
		candidate := filepath.Join(dir, "rune-probe-"+strconv.Itoa(os.Getpid())+".sock")
		if l, err := net.Listen("unix", candidate); err == nil {
			_ = l.Close()
			_ = os.Remove(candidate)
			runSock = candidate
			unixListener(t, runSock)
			break
		}
	}
	if strings.HasSuffix(runSock, "missing.sock") {
		t.Log("no writable directory under /run: the runtime socket is not exercised here")
	}
	tmpDir, err := os.MkdirTemp("/tmp", "rune-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	tmpSock := filepath.Join(tmpDir, "s.sock")
	unixListener(t, tmpSock)
	sshSock := filepath.Join(f.home, "agent", "ssh.sock")
	unixListener(t, sshSock)
	dockerSock := filepath.Join(f.home, "docker", "docker.sock")
	unixListener(t, dockerSock)
	abstract := "rune-probe-" + strconv.Itoa(os.Getpid())
	al, err := net.Listen("unix", "@"+abstract)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = al.Close() })

	if err := os.Mkdir(filepath.Join(f.home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{".ssh/id_test": "PRIVATE KEY\n", ".netrc": "machine example.com password hunter2\n"} {
		if err := os.WriteFile(filepath.Join(f.home, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.env = append(f.env, "SSH_AUTH_SOCK="+sshSock, "DOCKER_HOST=unix://"+dockerSock)
	cs, _, _ := f.ruleServer(t, nil)
	args := map[string]any{"run": runSock, "tmp": tmpSock, "ssh": sshSock, "docker": dockerSock, "abstract": abstract, "home": f.home}

	const devices = "ok core fd full null ptmx pts random shm stderr stdin stdout tty urandom zero"
	check := func(task string, abstractLine string) {
		res := callTask(t, cs, task, args)
		lines := outLines(res.stdout)
		if len(lines) != 10 {
			t.Fatalf("%s: %d lines:\n%s\n%s", task, len(lines), res.stdout, res.stderr)
		}
		for i, what := range []string{"the runtime socket", "the socket under /tmp", "the SSH agent's socket", "the container daemon's socket"} {
			if !strings.HasPrefix(lines[i], "error:") {
				t.Errorf("%s: %s is reachable: %q", task, what, lines[i])
			}
		}
		if !strings.HasPrefix(lines[4], abstractLine) {
			t.Errorf("%s: the abstract socket: %q, want %q", task, lines[4], abstractLine)
		}
		if lines[5] != devices {
			t.Errorf("%s: devices %q, want %q", task, lines[5], devices)
		}
		if lines[6] != "ok " {
			t.Errorf("%s: the credential directory is not empty: %q", task, lines[6])
		}
		if !strings.HasPrefix(lines[7], "error:") {
			t.Errorf("%s: the credential directory is writable: %q", task, lines[7])
		}
		if lines[8] != "ok 0 bytes" {
			t.Errorf("%s: the credential file is readable: %q", task, lines[8])
		}
		// The body's own: the sandbox's first process, the worker and the
		// probe that looks. A host has many more.
		pids := 0
		for _, name := range strings.Fields(strings.TrimPrefix(lines[9], "ok")) {
			if _, err := strconv.Atoi(name); err == nil {
				pids++
			}
		}
		if pids == 0 || pids > 4 {
			t.Errorf("%s: the body sees %d processes: %q", task, pids, lines[9])
		}
	}
	check("sockets", "error:")
	// The host's abstract sockets come with the host's network: the stated
	// limit of [network].
	check("sockets-net", "ok")
	if _, err := os.Stat(filepath.Join(f.home, ".ssh", "planted")); err == nil {
		t.Error("a body wrote into the credential directory")
	}
}

// A Runefile in the home directory makes the credential stores part of the
// workspace. They stay hidden.
func TestSandboxRuleHidesCredentialsInsideWorkspace(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	home := f.home
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_test"), []byte("PRIVATE KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Runefile"), []byte("# Look.\nlook:\n    -@./probe ls .ssh\n    -@./probe write .ssh/planted\n    -@./probe write inside.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installProbe(t, home)
	f.dir = home
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "look", nil)
	lines := outLines(res.stdout)
	if len(lines) != 3 || lines[0] != "ok " || !strings.HasPrefix(lines[1], "error:") || lines[2] != "ok" {
		t.Errorf("a credential store inside the workspace: %q\n%s", lines, res.stderr)
	}
}

// Quickstart Z: a body holds nothing the server holds, also what the server
// inherited from the program that started it.
func TestSandboxRuleNoInheritedFiles(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	flags := []string{"mcp", "--color=never"}
	if os.Getenv("RUNE_TEST_SANDBOX_MUTATE") == "off" {
		flags = append(flags, "--sandbox", "off")
	}
	cmd := exec.Command(runeBin, flags...)
	cmd.Dir, cmd.Env = f.dir, f.env
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	for range 3 {
		extra, err := os.Open(filepath.Join(f.outside, "marker"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = extra.Close() })
		cmd.ExtraFiles = append(cmd.ExtraFiles, extra)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("the server did not start: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = cs.Close() })
	if res := callTask(t, cs, "fds", nil); res.stdout != "ok 0 1 2\n" {
		t.Errorf("the body's open files: %q", res.stdout)
	}
}

// httpServer starts `rune serve --http` for the project and connects.
// prepare, when set, starts the command itself.
func (f *sandboxFixture) httpServer(t *testing.T, start func(*exec.Cmd) error) (*mcp.ClientSession, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	args := []string{"serve", "--color=never", "--http", "--addr", addr, "--token-file", writeToken(t, f.root)}
	if os.Getenv("RUNE_TEST_SANDBOX_MUTATE") == "off" {
		args = append(args, "--sandbox", "off")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, runeBin, args...)
	cmd.Dir, cmd.Env = f.dir, f.env
	stderr := &syncBuffer{}
	if start == nil {
		cmd.Stderr = stderr
		start = func(cmd *exec.Cmd) error { return cmd.Start() }
	}
	if err := start(cmd); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	cs := connectHTTP(t, ctx, addr, stderr)
	t.Cleanup(func() { _ = cs.Close() })
	return cs, addr
}

// A server started from a terminal: a body has no terminal to write into.
func TestSandboxRuleNoTerminal(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _ := f.httpServer(t, func(cmd *exec.Cmd) error {
		terminal, err := pty.Start(cmd)
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = terminal.Close() })
		// The server writes its startup line into the terminal.
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, err := terminal.Read(buf); err != nil {
					return
				}
			}
		}()
		return nil
	})
	if res := callTask(t, cs, "tty", nil); !strings.HasPrefix(res.stdout, "error:") {
		t.Errorf("the body opened the server's terminal: %q", res.stdout)
	}
}

// A body has no capability and cannot gain one, also when the server runs
// as root, and runs as the server's user.
func TestSandboxRuleNoCapabilities(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	want := "ok CapEff:0000000000000000 CapBnd:0000000000000000 NoNewPrivs:1 uid:" + strconv.Itoa(os.Getuid()) + "\n"
	if res := callTask(t, cs, "status", nil); res.stdout != want {
		t.Errorf("status = %q, want %q", res.stdout, want)
	}
}

// heartbeat reads the file a `probe daemon` keeps writing, or "".
func heartbeat(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

// waitBeating waits until the heartbeat file changes, which shows the
// detached process is alive.
func waitBeating(t *testing.T, path string) {
	t.Helper()
	first := ""
	for deadline := time.Now().Add(reloadTimeout); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		now := heartbeat(path)
		switch {
		case now == "":
		case first == "":
			first = now
		case now != first:
			return
		}
	}
	t.Fatalf("the detached process never started writing %s", path)
}

// wantStopped checks that the heartbeat stops within two seconds.
func wantStopped(t *testing.T, path, when string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		before := heartbeat(path)
		time.Sleep(400 * time.Millisecond)
		if heartbeat(path) == before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: the detached process is still running", when)
			return
		}
	}
}

// Quickstart K: cancelling a call ends every process it started, also one
// that left its session, and the server answers the next call.
func TestSandboxRuleCancelStopsTree(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "hold"})
	}()
	hb := filepath.Join(f.dir, "hb")
	waitBeating(t, hb)
	cancel()
	<-done
	wantStopped(t, hb, "after the call was cancelled")
	if res := callTask(t, cs, "fine", nil); res.exit != 0 || res.stdout != "fine\n" {
		t.Errorf("the next call: %+v", res)
	}
	var held bool
	for deadline := time.Now().Add(reloadTimeout); time.Now().Before(deadline) && !held; time.Sleep(50 * time.Millisecond) {
		for _, call := range logCalls(t, f.dir, f.logEnv()) {
			held = held || (call.Task == "hold" && call.Outcome == "cancelled")
		}
	}
	if !held {
		t.Errorf("the cancelled call is not recorded as cancelled: %+v", logCalls(t, f.dir, f.logEnv()))
	}
}

// Quickstart L: a process a body leaves behind ends with the body.
func TestSandboxRuleBackgroundEndsWithBody(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	if res := callTask(t, cs, "leave", nil); res.exit != 0 {
		t.Fatalf("leave: %+v", res)
	}
	if heartbeat(filepath.Join(f.dir, "hb")) == "" {
		t.Fatal("the detached process never wrote")
	}
	wantStopped(t, filepath.Join(f.dir, "hb"), "after the body ended")
}

// A server that ends during a call takes the call's processes with it.
func TestSandboxRuleServerExitStopsTree(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _, cmd := f.ruleServer(t, nil)
	go func() { _, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hold"}) }()
	hb := filepath.Join(f.dir, "hb")
	waitBeating(t, hb)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	wantStopped(t, hb, "after the server was killed")
}

// Quickstart T: over Streamable HTTP a body is confined the same way, cannot
// reach the server's own listener, and the server keeps what it needs.
func TestSandboxRuleOverHTTP(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, addr := f.httpServer(t, nil)
	if res := callTask(t, cs, "connect", map[string]any{"addr": f.tcp}); !strings.HasPrefix(res.stdout, "error:") {
		t.Errorf("a plain body reached the test's listener: %q", res.stdout)
	}
	if res := callTask(t, cs, "connect", map[string]any{"addr": addr}); !strings.HasPrefix(res.stdout, "error:") {
		t.Errorf("a plain body reached the server's own listener: %q", res.stdout)
	}
	if res := callTask(t, cs, "fds", nil); res.stdout != "ok 0 1 2\n" {
		t.Errorf("the body's open files: %q", res.stdout)
	}
	if got := f.tcpCount.Load(); got != 0 {
		t.Errorf("the listener saw %d connections", got)
	}
	// The server still listens, still answers, and still writes its log.
	if tools, err := cs.ListTools(context.Background(), nil); err != nil || len(tools.Tools) == 0 {
		t.Errorf("the server stopped answering: %v", err)
	}
	if res := callTask(t, cs, "fine", nil); res.stdout != "fine\n" {
		t.Errorf("a later call: %+v", res)
	}
	if calls := logCalls(t, f.dir, f.logEnv()); len(calls) != 4 {
		t.Errorf("%d calls in the log, want 4", len(calls))
	}
}

// The record of a confined call says how it was confined, and so does the
// record of a call that never ended.
func TestSandboxAuditRecord(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, hostRunefile)
	cs, _, cmd := f.server(t, nil)
	if res := callTask(t, cs, "fine", nil); res.exit != 0 {
		t.Fatalf("fine: %+v", res)
	}
	raw := run(t, f.dir, f.logEnv(), "log", "--json", "--since", "1h").stdout
	if want := `"sandbox":{"enforced":true,"backend":"bubblewrap","mode":"enforce"}`; !strings.Contains(raw, want) {
		t.Errorf("the record lacks %s:\n%s", want, raw)
	}
	if text := run(t, f.dir, f.logEnv(), "log", "--since", "1h").stdout; strings.Contains(text, "ran without a sandbox") || strings.Contains(text, "sandbox:") {
		t.Errorf("a confined call has a line about the sandbox:\n%s", text)
	}

	go func() { _, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hold"}) }()
	waitBeating(t, filepath.Join(f.dir, "hb"))
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	calls := logCalls(t, f.dir, f.logEnv())
	last := calls[len(calls)-1]
	if last.Outcome != "incomplete" || last.Sandbox == nil || !last.Sandbox.Enforced || last.Sandbox.Backend != "bubblewrap" || last.Sandbox.Mode != "enforce" {
		t.Errorf("the call that never ended: %+v, sandbox %+v", last, last.Sandbox)
	}
}

// A host where bubblewrap is installed and cannot start a sandbox, as a
// default container is: the refusal says so and names the remedy.
func TestSandboxCannotStart(t *testing.T) {
	err := sandbox.Detect("linux", []string{runeBin, "--sandbox-probe"}).Probe(context.Background())
	if !errors.Is(err, sandbox.ErrCannotStart) {
		t.Skipf("this host is not one where bubblewrap is installed and cannot start: %v", err)
	}
	dir := writeRunefile(t, sandboxBasicRunefile)
	cs, stderr, _ := sandboxServer(t, dir, serverEnv(t), nil)
	got := waitForText(t, stderr, "rune MCP server on stdio", 1)
	const remedy = "Allow unprivileged user namespaces for bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
	if !strings.Contains(got, "warning: sandbox: bubblewrap could not start a sandbox: ") || !strings.Contains(got, "Every task call will be refused. "+remedy) {
		t.Errorf("stderr:\n%s", got)
	}
	res := callTask(t, cs, "build", nil)
	if !res.isError || !strings.Contains(res.text, `task "build" was not run: the sandbox is unavailable: bubblewrap could not start a sandbox: `) || !strings.Contains(res.text, remedy) {
		t.Errorf("result: %+v", res)
	}
	if hasMarker(dir, "build.ran") {
		t.Error("the task ran with no sandbox")
	}
}

// A backend installed while the server runs is used by the next call, with
// no restart, and that call's record says it was confined.
func TestSandboxBackendInstalledLater(t *testing.T) {
	requireSandbox(t)
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	f := sandboxProject(t, hostRunefile)
	// Not a temporary directory: a PATH under /tmp is one the sandbox hides.
	bin := filepath.Join(f.root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "PATH="+bin)
	cs, stderr, _ := f.server(t, nil)
	waitForText(t, stderr, "sandbox: unavailable, calls are refused", 1)
	if res := callTask(t, cs, "fine", nil); !res.isError || !strings.Contains(res.text, "bubblewrap (bwrap) was not found on the PATH") {
		t.Fatalf("before the backend exists: %+v", res)
	}
	if err := os.Symlink(bwrap, filepath.Join(bin, "bwrap")); err != nil {
		t.Fatal(err)
	}
	// An unavailable backend is asked again after a second.
	time.Sleep(1200 * time.Millisecond)
	if res := callTask(t, cs, "fine", nil); res.isError || res.stdout != "fine\n" {
		t.Fatalf("after the backend was installed: %+v", res)
	}
	call := lastLogCall(t, f.dir, f.logEnv())
	if call.Sandbox == nil || !call.Sandbox.Enforced || call.Sandbox.Backend != "bubblewrap" {
		t.Errorf("the record of the confined call: %+v", call.Sandbox)
	}
}

// SC-008: confinement adds less than 150 ms to a body.
func TestSandboxOverhead(t *testing.T) {
	requireSandbox(t)
	const calls = 20
	mean := func(flags ...string) time.Duration {
		f := sandboxProject(t, hostRunefile)
		cs, _, _ := f.server(t, nil, flags...)
		callTask(t, cs, "fine", nil) // the first call pays for the probe
		var total time.Duration
		for range calls {
			total += callTask(t, cs, "fine", nil).duration
		}
		return total / calls
	}
	confined, unconfined := mean(), mean("--sandbox", "off")
	t.Logf("a confined body takes %v, an unconfined one %v", confined, unconfined)
	if confined-unconfined > 150*time.Millisecond {
		t.Errorf("confinement adds %v to a body", confined-unconfined)
	}
}
