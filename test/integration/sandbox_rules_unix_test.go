//go:build unix

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/sandbox"
)

// The tests of this file start a real sandbox (spec 053). They are skipped on
// a host that has none, and fail there when RUNE_TEST_SANDBOX=require says
// the host is meant to have one.
//
// A test named TestSandboxRule... proves one rule of the sandbox. It starts
// its server through ruleServer, so that the same test can be run against a
// server started unsandboxed (RUNE_TEST_SANDBOX_MUTATE=off), where it must
// fail: a rule test that passes without the sandbox proves nothing.

var (
	sandboxProbeOnce sync.Once
	sandboxProbeErr  error
)

// requireSandbox skips the test unless bubblewrap can start a sandbox here.
func requireSandbox(t *testing.T) {
	t.Helper()
	sandboxProbeOnce.Do(func() {
		sandboxProbeErr = sandbox.Detect("linux", []string{runeBin, "--sandbox-probe"}).Probe(context.Background())
	})
	if sandboxProbeErr == nil {
		return
	}
	if os.Getenv("RUNE_TEST_SANDBOX") == "require" {
		t.Fatalf("a sandbox is required here: %v", sandboxProbeErr)
	}
	t.Skipf("no sandbox on this host: %v", sandboxProbeErr)
}

// sandboxFixtureDir is a directory of the test's own that is not under /tmp,
// /var/tmp or /run. The sandbox replaces those, so a marker placed there
// would be out of a body's reach whether or not a rule works.
func sandboxFixtureDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("RUNE_TEST_SANDBOX_DIR")
	if base == "" {
		root, err := moduleRoot()
		if err != nil {
			t.Fatal(err)
		}
		base = filepath.Join(root, ".rune", "sandbox-test")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"/tmp", "/var/tmp", "/run"} {
		if sandbox.Within(hidden, dir) {
			t.Fatalf("the fixture directory %s is under %s; set RUNE_TEST_SANDBOX_DIR", dir, hidden)
		}
	}
	return dir
}

var (
	probeOnce sync.Once
	probeBin  string
	probeErr  error
)

// installProbe puts the probe program into a project as ./probe. It is built
// once, beside the rune binary.
func installProbe(t *testing.T, dir string) {
	t.Helper()
	probeOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			probeErr = err
			return
		}
		probeBin = filepath.Join(filepath.Dir(runeBin), "sandboxprobe")
		build := exec.Command("go", "build", "-o", probeBin, "./test/integration/testdata/sandboxprobe")
		build.Dir = root
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			probeErr = fmt.Errorf("building the probe: %v\n%s", err, out)
		}
	})
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	data, err := os.ReadFile(probeBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// sandboxFixture is one test's project with what its bodies try to reach.
type sandboxFixture struct {
	root    string // the fixture directory
	dir     string // the project
	outside string // a directory beside the project, with the file "marker"
	home    string // the HOME of the server
	audit   string // RUNE_AUDIT_DIR of the server
	env     []string

	tcp, udp           string // listeners of the test on the host's loopback
	tcpCount, udpCount atomic.Int64
}

// sandboxProject makes a project under the fixture directory.
func sandboxProject(t *testing.T, runefile string) *sandboxFixture {
	t.Helper()
	root := sandboxFixtureDir(t)
	f := &sandboxFixture{
		root:    root,
		dir:     filepath.Join(root, "project"),
		outside: filepath.Join(root, "outside"),
		home:    filepath.Join(root, "home"),
		audit:   filepath.Join(root, "audit"),
	}
	for _, dir := range []string{f.dir, f.outside, f.home} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, "Runefile", runefile)
	if err := os.WriteFile(filepath.Join(f.outside, "marker"), []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, filepath.Join(f.dir, "link")); err != nil {
		t.Fatal(err)
	}
	installProbe(t, f.dir)

	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "RUNE_SANDBOX", "HOME", "XDG_CACHE_HOME", "RUNE_AUDIT_DIR", "RUNE_AUDIT_RETENTION_DAYS", "SSH_AUTH_SOCK", "DOCKER_HOST":
		default:
			f.env = append(f.env, entry)
		}
	}
	f.env = append(f.env,
		"HOME="+f.home,
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"RUNE_AUDIT_DIR="+f.audit,
		"RUNE_AUDIT_RETENTION_DAYS=",
	)
	f.listen(t)
	return f
}

// write puts a file into the project.
func (f *sandboxFixture) write(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// listen starts a TCP and a UDP echo listener on the host's loopback and
// counts what reaches them.
func (f *sandboxFixture) listen(t *testing.T) {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tcp.Close()
		_ = udp.Close()
	})
	f.tcp, f.udp = tcp.Addr().String(), udp.LocalAddr().String()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			f.tcpCount.Add(1)
			_ = conn.Close()
		}
	}()
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			f.udpCount.Add(1)
			_, _ = udp.WriteTo(buf[:n], from)
		}
	}()
}

// server starts `rune mcp` for the project, confined.
func (f *sandboxFixture) server(t *testing.T, opts *mcp.ClientOptions, flags ...string) (*mcp.ClientSession, *syncBuffer, *exec.Cmd) {
	t.Helper()
	return sandboxServer(t, f.dir, f.env, opts, flags...)
}

// ruleServer is server for a test of a rule: with
// RUNE_TEST_SANDBOX_MUTATE=off the server runs unsandboxed, and the test is
// expected to fail.
func (f *sandboxFixture) ruleServer(t *testing.T, opts *mcp.ClientOptions, flags ...string) (*mcp.ClientSession, *syncBuffer, *exec.Cmd) {
	t.Helper()
	if os.Getenv("RUNE_TEST_SANDBOX_MUTATE") == "off" {
		flags = append(flags, "--sandbox", "off")
	}
	return f.server(t, opts, flags...)
}

// logEnv is what `rune log` needs to read this project's log.
func (f *sandboxFixture) logEnv() []string {
	return []string{"RUNE_AUDIT_DIR=" + f.audit, "RUNE_AUDIT_RETENTION_DAYS=", "HOME=" + f.home}
}

// treeDigest is a digest of every file under dir: its path, its mode, and
// its content or link target.
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(h, "%s %v\n", rel, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(h, target)
		case info.Mode().IsRegular():
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			if _, err := io.Copy(h, file); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("digest of %s: %v", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// outLines splits a task's standard output into its lines.
func outLines(stdout string) []string {
	return strings.Split(strings.TrimRight(stdout, "\n"), "\n")
}

// wantLines checks a body's output line by line: "ok" wants a line that
// starts with it, "fail" one that starts with "error:", anything else the
// line itself.
func wantLines(t *testing.T, what string, res taskResult, want ...string) {
	t.Helper()
	got := outLines(res.stdout)
	if len(got) != len(want) {
		t.Fatalf("%s: %d lines, want %d:\n%s\nstderr:\n%s\ntext:\n%s", what, len(got), len(want), res.stdout, res.stderr, res.text)
	}
	for i, w := range want {
		ok := got[i] == w
		switch w {
		case "ok":
			ok = strings.HasPrefix(got[i], "ok")
		case "fail":
			ok = strings.HasPrefix(got[i], "error:")
		}
		if !ok {
			t.Errorf("%s: line %d is %q, want %s", what, i+1, got[i], w)
		}
	}
}

const sandboxRulesRunefile = `# Try the network without [network].
net addr udp:
    -@./probe connect {{addr}}
    -@./probe udp {{udp}}
    -@./probe resolve example.com

# Try the network with [network].
[network]
net-on addr udp:
    -@./probe connect {{addr}}
    -@./probe udp {{udp}}
    -@./probe read /etc/resolv.conf

# Serve and connect inside one body.
loop:
    @./probe selfserve

# Try to write outside the project.
writes outside:
    -@./probe read {{outside}}/marker
    -@./probe write inside.txt
    -@./probe write {{outside}}/absolute
    -@./probe write ../outside/relative
    -@./probe write link/through-link
    -@./probe write /proc/self/root{{outside}}/through-proc
    -@./probe link {{outside}}/marker hard-link
    -@read line || echo eof

# The same with the network.
[network]
writes-net outside:
    -@./probe read {{outside}}/marker
    -@./probe write inside-net.txt
    -@./probe write {{outside}}/absolute
    -@./probe write ../outside/relative
    -@./probe write link/through-link
    -@./probe write /proc/self/root{{outside}}/through-proc
    -@./probe link {{outside}}/marker hard-link-net
    -@read line || echo eof

# Use the private directories.
private:
    @echo "$TMPDIR $XDG_CACHE_HOME"
    @test -e "$TMPDIR/a" && echo stale || echo fresh
    @: > "$TMPDIR/a"
    @: > /tmp/also
    @test -e "$XDG_CACHE_HOME/b" && echo cached || echo cold
    @: > "$XDG_CACHE_HOME/b"

# Fail on purpose.
broken:
    @exit 3

# Fail on purpose, with the network.
[network]
broken-net:
    @exit 3

# Succeed.
fine:
    @echo fine
`

// Quickstart A: a task without [network] reaches nothing outside its
// sandbox: no connection, no datagram, no name lookup, also through a proxy
// variable.
func TestSandboxRulePlainHasNoNetwork(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	f.env = append(f.env, "HTTP_PROXY=http://"+f.tcp, "HTTPS_PROXY=http://"+f.tcp, "ALL_PROXY=http://"+f.tcp)
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "net", map[string]any{"addr": f.tcp, "udp": f.udp})
	wantLines(t, "a task without [network]", res, "fail", "fail", "fail")
	if tcp, udp := f.tcpCount.Load(), f.udpCount.Load(); tcp != 0 || udp != 0 {
		t.Errorf("the host's listeners saw %d connections and %d datagrams", tcp, udp)
	}
}

// Quickstart B: a task marked [network] has the host's network, and reads
// the host's resolver file.
func TestSandboxNetworkTaskConnects(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "net-on", map[string]any{"addr": f.tcp, "udp": f.udp})
	want := []string{"ok", "ok", "ok"}
	if _, err := os.Stat("/etc/resolv.conf"); err != nil {
		want[2] = "fail"
	}
	wantLines(t, "a [network] task", res, want...)
	if tcp, udp := f.tcpCount.Load(), f.udpCount.Load(); tcp != 1 || udp != 1 {
		t.Errorf("the host's listeners saw %d connections and %d datagrams, want 1 and 1", tcp, udp)
	}
}

// Quickstart C: a task without [network] has a loopback of its own.
func TestSandboxPlainOwnLoopback(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	cs, _, _ := f.server(t, nil)
	if res := callTask(t, cs, "loop", nil); res.exit != 0 || res.stdout != "ok\n" {
		t.Errorf("a listener and a connection inside one body: %+v", res)
	}
}

// Quickstart D: a body writes inside the project and nowhere else, with or
// without [network].
func TestSandboxRuleWritesStayInside(t *testing.T) {
	requireSandbox(t)
	for task, inside := range map[string]string{"writes": "inside.txt", "writes-net": "inside-net.txt"} {
		t.Run(task, func(t *testing.T) {
			f := sandboxProject(t, sandboxRulesRunefile)
			before := treeDigest(t, f.outside)
			cs, _, _ := f.ruleServer(t, nil)
			res := callTask(t, cs, task, map[string]any{"outside": f.outside})
			// The marker is readable, so each failed write below is the
			// rule and not a path that is missing.
			wantLines(t, task, res, "ok", "ok", "fail", "fail", "fail", "fail", "fail", "eof")
			if _, err := os.Stat(filepath.Join(f.dir, inside)); err != nil {
				t.Errorf("the write inside the project did not land: %v", err)
			}
			if after := treeDigest(t, f.outside); after != before {
				entries, _ := os.ReadDir(f.outside)
				t.Errorf("the directory outside the project changed: %v", entries)
			}
		})
	}
}

// Quickstart J: a body has a temporary directory of its own and the project
// has a cache directory that stays, both outside the project.
func TestSandboxPrivateDirs(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	cs, _, _ := f.server(t, nil)
	first := callTask(t, cs, "private", nil)
	if first.exit != 0 {
		t.Fatalf("first call: %+v", first)
	}
	lines := outLines(first.stdout)
	tmp, cache, _ := strings.Cut(lines[0], " ")
	if tmp != "/tmp" || !strings.HasPrefix(cache, filepath.Join(f.root, "cache", "rune", "sandbox")+string(filepath.Separator)) {
		t.Errorf("TMPDIR %q, XDG_CACHE_HOME %q", tmp, cache)
	}
	if sandbox.Within(f.dir, cache) {
		t.Errorf("the cache directory %s is inside the project", cache)
	}
	if lines[1] != "fresh" || lines[2] != "cold" {
		t.Errorf("the first body: %q", lines)
	}
	second := callTask(t, cs, "private", nil)
	if lines := outLines(second.stdout); lines[1] != "fresh" || lines[2] != "cached" {
		t.Errorf("the second body did not start with a temporary directory of its own and a kept cache: %q", lines)
	}
	if _, err := os.Stat(filepath.Join(cache, "b")); err != nil {
		t.Errorf("the cache file is not on the host: %v", err)
	}
	// Nothing of a body's temporary directory is left, on the host's /tmp
	// or under the private base.
	if _, err := os.Stat("/tmp/also"); err == nil {
		t.Error("the body wrote into the host's /tmp")
	}
	if left, err := os.ReadDir(filepath.Join(filepath.Dir(cache), "tmp")); err != nil || len(left) != 0 {
		t.Errorf("temporary directories left behind: %v, %v", left, err)
	}
}

// Quickstart Y: a confined body that fails gets one line that says it ran in
// the sandbox. A body that succeeds gets none.
func TestSandboxHintLine(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	cs, _, _ := f.server(t, nil)
	plain := callTask(t, cs, "broken", nil)
	if want := `note: task "broken" ran in the sandbox: it has no network (it is not marked [network]) and can write only inside ` + f.dir + ".\n"; plain.exit != 3 || !strings.HasSuffix(plain.stderr, want) {
		t.Errorf("exit %d, stderr %q, want it to end with %q", plain.exit, plain.stderr, want)
	}
	network := callTask(t, cs, "broken-net", nil)
	if want := `note: task "broken-net" ran in the sandbox: it can write only inside ` + f.dir + ".\n"; network.exit != 3 || !strings.HasSuffix(network.stderr, want) {
		t.Errorf("exit %d, stderr %q, want it to end with %q", network.exit, network.stderr, want)
	}
	if fine := callTask(t, cs, "fine", nil); fine.exit != 0 || strings.Contains(fine.stderr, "sandbox") {
		t.Errorf("a body that succeeds: %+v", fine)
	}
}

// Quickstart R: a call the sandbox cannot confine is refused before anything
// runs: a working directory outside the project, and a private directory
// that cannot be made.
func TestSandboxRefusesOutsideWorkdir(t *testing.T) {
	requireSandbox(t)
	runefile := "[working-directory(\"/\")]\nelsewhere:\n    @: > ran.elsewhere\n\n# Build.\nbuild: elsewhere\n    @: > ran.build\n"
	f := sandboxProject(t, runefile)
	cs, _, _ := f.server(t, nil)
	for _, task := range []string{"elsewhere", "build"} {
		res := callTask(t, cs, task, nil)
		want := `task "` + task + `" was not run: the sandbox cannot confine it: the working directory of task "elsewhere" (/) is outside the project (` + f.dir + `)`
		if !res.isError || !strings.Contains(res.text, want) {
			t.Errorf("%s: isError %v, text %q, want %q in it", task, res.isError, res.text, want)
		}
	}
	if hasMarker(f.dir, "ran.build") {
		t.Error("a body ran")
	}
	call := lastLogCall(t, f.dir, f.logEnv())
	if call.Outcome != "refused" || call.Error == nil || call.Error.Kind != "sandbox" || call.ExitCode != nil {
		t.Errorf("the record: %+v", call)
	}
}

func TestSandboxRefusesPrivateDir(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, sandboxRulesRunefile)
	taken := filepath.Join(f.root, "a-file")
	if err := os.WriteFile(taken, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "XDG_CACHE_HOME="+taken)
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "fine", nil)
	if want := `task "fine" was not run: the sandbox cannot use its private directory `; !res.isError || !strings.Contains(res.text, want) || !strings.Contains(res.text, "not a directory") {
		t.Errorf("isError %v, text %q", res.isError, res.text)
	}
}
