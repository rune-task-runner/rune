package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

var (
	fixtureOnce sync.Once
	fixtureBin  []byte
	fixtureErr  error
)

// fixtureBinary builds, once, a rune that reports version 9.9.9: the binary a
// fixture release carries.
func fixtureBinary(t *testing.T) []byte {
	t.Helper()
	fixtureOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			fixtureErr = err
			return
		}
		out := filepath.Join(filepath.Dir(runeBin), "rune-fixture"+exeSuffix())
		build := exec.Command("go", "build", "-tags", "runetest",
			"-ldflags", "-X main.version=9.9.9 -X main.commit=fixture", "-o", out, "./cmd/rune")
		build.Dir = root
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := build.CombinedOutput(); err != nil {
			fixtureErr = &buildError{out: string(b), err: err}
			return
		}
		fixtureBin, fixtureErr = os.ReadFile(out)
	})
	if fixtureErr != nil {
		t.Fatalf("build fixture binary: %v", fixtureErr)
	}
	return fixtureBin
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// upgradeInstall is a scratch standalone install of the test binary plus the
// environment that points it at a fixture release.
type upgradeInstall struct {
	dir  string
	bin  string
	body []byte
	env  []string
}

// newUpgradeInstall copies the test binary to <tmp>/<sub>/rune and wires it
// to rel. The test binary is unstamped; with RUNE_TEST_VERSION set, the
// runetest build treats it as a stamped release build.
func newUpgradeInstall(t *testing.T, rel *upgradetest.Release, sub string) *upgradeInstall {
	t.Helper()
	body, err := os.ReadFile(runeBin)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	dir := filepath.Join(base, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := &upgradeInstall{dir: dir, bin: filepath.Join(dir, "rune"+exeSuffix()), body: body}
	if err := os.WriteFile(in.bin, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if rel != nil {
		rootFile := filepath.Join(base, "trusted_root.json")
		caFile := filepath.Join(base, "ca.pem")
		if err := os.WriteFile(rootFile, rel.TrustRootJSON(t), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(caFile, rel.CACertPEM(), 0o600); err != nil {
			t.Fatal(err)
		}
		in.env = []string{
			"RUNE_TEST_UPGRADE_API=" + rel.URL,
			"RUNE_TEST_UPGRADE_DOWNLOAD=" + rel.URL,
			"RUNE_TEST_UPGRADE_ROOT=" + rootFile,
			"RUNE_TEST_UPGRADE_CA=" + caFile,
		}
	}
	return in
}

// exec runs the installed binary in dir with the fixture environment plus env.
func (in *upgradeInstall) exec(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	return runBin(t, in.bin, dir, append(append([]string{}, in.env...), env...), args...)
}

func (in *upgradeInstall) unchanged(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(in.bin)
	if err != nil || !bytes.Equal(got, in.body) {
		t.Errorf("installed binary changed (err %v)", err)
	}
	in.onlyBinary(t)
}

func (in *upgradeInstall) onlyBinary(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(in.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(in.bin) && e.Name() != filepath.Base(in.bin)+".old" {
			t.Errorf("install directory holds a leftover: %s", e.Name())
		}
	}
}

// runBin executes an arbitrary rune binary; run only knows the shared one.
func runBin(t *testing.T, bin, dir string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s %v failed to start: %v", bin, args, err)
		}
	}
	return result{stdout: out.String(), stderr: errb.String(), code: code}
}

func hostRelease(t *testing.T, o upgradetest.Options) *upgradetest.Release {
	t.Helper()
	o.Binary = fixtureBinary(t)
	o.Platforms = [][2]string{{runtime.GOOS, runtime.GOARCH}}
	return upgradetest.New(t, o)
}

func TestSelfUpgradeReplacesBinary(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	// An empty PATH proves no other tool is needed to verify the release.
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1", "PATH=" + t.TempDir()}, "--self-upgrade")
	if r.code != 0 {
		t.Fatalf("exit %d; stderr:\n%s", r.code, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	lines := strings.Split(strings.TrimSpace(r.stderr), "\n")
	if lines[0] != "rune: installed 0.0.1" {
		t.Errorf("first line = %q, want %q", lines[0], "rune: installed 0.0.1")
	}
	real, err := filepath.EvalSymlinks(in.bin)
	if err != nil {
		t.Fatal(err)
	}
	if want := "upgraded: 0.0.1 -> 9.9.9 (" + real + ")"; lines[len(lines)-1] != want {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], want)
	}
	in.onlyBinary(t)

	v := runBin(t, in.bin, t.TempDir(), nil, "--version")
	if v.code != 0 || !strings.Contains(v.stdout, "9.9.9") {
		t.Errorf("replaced binary --version: exit %d, stdout %q", v.code, v.stdout)
	}
}

func TestSelfUpgradeAlreadyCurrent(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=9.9.9"}, "--self-upgrade")
	if r.code != 0 || !strings.HasSuffix(strings.TrimSpace(r.stderr), "current: nothing to upgrade") {
		t.Errorf("exit %d; stderr:\n%s", r.code, r.stderr)
	}
	in.unchanged(t)
}

func TestSelfUpgradeIgnoresRunefile(t *testing.T) {
	runefiles := map[string]string{
		"none":        "",
		"broken":      "}{\n",
		"needs newer": "set minimum_version := \"99.0.0\"\n\nbuild:\n    echo hi\n",
	}
	for name, content := range runefiles {
		t.Run(name, func(t *testing.T) {
			rel := hostRelease(t, upgradetest.Options{})
			in := newUpgradeInstall(t, rel, "bin")
			cwd := t.TempDir()
			if content != "" {
				if err := os.WriteFile(filepath.Join(cwd, "Runefile"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := in.exec(t, cwd, []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")
			if r.code != 0 || !strings.Contains(r.stderr, "upgraded: 0.0.1 -> 9.9.9") {
				t.Errorf("exit %d; stderr:\n%s", r.code, r.stderr)
			}
			// Exactly the five upgrade lines: any Runefile diagnostic would add more.
			if n := len(strings.Split(strings.TrimSpace(r.stderr), "\n")); n != 5 || strings.Contains(r.stderr, "minimum_version") {
				t.Errorf("unexpected output besides the upgrade lines (%d lines):\n%s", n, r.stderr)
			}
		})
	}
}

func TestSelfUpgradeTamperedArchive(t *testing.T) {
	flip := func(b []byte) []byte { c := append([]byte(nil), b...); c[len(c)/2] ^= 0xff; return c }
	rel := hostRelease(t, upgradetest.Options{MutateArchive: flip})
	in := newUpgradeInstall(t, rel, "bin")
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")
	if r.code != 1 {
		t.Errorf("exit %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "does not match the signed checksum; nothing was installed") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	in.unchanged(t)
}

// start launches the installed binary without waiting for it.
func (in *upgradeInstall) start(t *testing.T, env []string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(in.bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(append(os.Environ(), in.env...), env...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, &errb
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if err == nil {
		return 0
	}
	if asExit(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func TestSelfUpgradeCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test cannot deliver an interrupt to a child process on Windows")
	}
	serving := make(chan struct{})
	var once sync.Once
	rel := hostRelease(t, upgradetest.Options{
		OnArchive:       func() { once.Do(func() { close(serving) }) },
		LatencyPerChunk: 20 * time.Millisecond,
	})
	in := newUpgradeInstall(t, rel, "bin")
	cmd, stderr := in.start(t, []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")

	select {
	case <-serving:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the download never started; stderr:\n%s", stderr)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if code := exitCode(cmd.Wait()); code != 130 {
		t.Errorf("exit %d, want 130; stderr:\n%s", code, stderr)
	}
	in.unchanged(t)
	if v := runBin(t, in.bin, t.TempDir(), nil, "--version"); v.code != 0 {
		t.Errorf("the binary no longer runs after an interrupted upgrade: exit %d", v.code)
	}
}

func TestSelfUpgradeConcurrentProcesses(t *testing.T) {
	hold := make(chan struct{})
	rel := hostRelease(t, upgradetest.Options{HoldArchive: hold})
	in := newUpgradeInstall(t, rel, "bin")

	const n = 4
	type done struct {
		code   int
		stderr string
	}
	results := make(chan done, n)
	for range n {
		cmd, stderr := in.start(t, []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")
		go func() {
			err := cmd.Wait()
			results <- done{exitCode(err), stderr.String()}
		}()
	}

	var upgraded, busy int
	collect := func() {
		select {
		case d := <-results:
			switch {
			case d.code == 0 && strings.Contains(d.stderr, "upgraded: 0.0.1 -> 9.9.9"):
				upgraded++
			case d.code == 1 && strings.Contains(d.stderr, "another self-upgrade of") && strings.Contains(d.stderr, "is running"):
				busy++
			default:
				t.Errorf("unexpected result: exit %d\n%s", d.code, d.stderr)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("a process did not finish")
		}
	}
	// The losers exit while the winner waits for its archive.
	for range n - 1 {
		collect()
	}
	close(hold)
	collect()

	if upgraded != 1 || busy != n-1 {
		t.Errorf("upgraded %d, busy %d; want 1 and %d", upgraded, busy, n-1)
	}
	in.onlyBinary(t)
	if v := runBin(t, in.bin, t.TempDir(), nil, "--version"); v.code != 0 || !strings.Contains(v.stdout, "9.9.9") {
		t.Errorf("after concurrent upgrades --version: exit %d, stdout %q", v.code, v.stdout)
	}
}

func TestSelfUpgradeOffline(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	began := time.Now()
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1", "RUNE_TEST_UPGRADE_API=https://127.0.0.1:1"}, "--self-upgrade")
	if r.code != 1 {
		t.Errorf("exit %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "rune: self-upgrade: cannot reach 127.0.0.1:1:") ||
		!strings.Contains(r.stderr, "check the network connection and try again") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if d := time.Since(began); d > 60*time.Second {
		t.Errorf("took %v to report being offline, want under 60s", d)
	}
	in.unchanged(t)
}

func TestSelfUpgradeUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root user on a system that enforces directory permissions")
	}
	rel := hostRelease(t, upgradetest.Options{})
	in := newUpgradeInstall(t, rel, "bin")
	if err := os.Chmod(in.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(in.dir, 0o755) })
	r := in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade")
	if r.code != 1 {
		t.Errorf("exit %d, want 1", r.code)
	}
	// The message names the resolved directory; on macOS the temp dir is a link.
	dir, err := filepath.EvalSymlinks(in.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.stderr, "cannot write to "+dir) || !strings.Contains(r.stderr, "rune does not ask for elevated rights") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made before the write check", rel.Requests())
	}
	in.unchanged(t)
}

func TestSelfUpgradeRefusals(t *testing.T) {
	rel := hostRelease(t, upgradetest.Options{})
	check := func(t *testing.T, in *upgradeInstall, env []string, installed, wantLine string) {
		t.Helper()
		r := in.exec(t, t.TempDir(), env, "--self-upgrade")
		if r.code != 1 {
			t.Errorf("exit %d, want 1", r.code)
		}
		lines := strings.Split(strings.TrimSpace(r.stderr), "\n")
		if lines[0] != "rune: installed "+installed {
			t.Errorf("first line = %q, want %q", lines[0], "rune: installed "+installed)
		}
		if !strings.Contains(r.stderr, wantLine) {
			t.Errorf("stderr lacks %q:\n%s", wantLine, r.stderr)
		}
		in.unchanged(t)
	}

	t.Run("container", func(t *testing.T) {
		in := newUpgradeInstall(t, rel, "bin")
		check(t, in, []string{"RUNE_TEST_VERSION=0.0.1", "RUNE_CONTAINER=1"}, "0.0.1",
			"rune: self-upgrade: this rune runs from the container image; pull a newer one: docker pull ghcr.io/rune-task-runner/rune:latest")
	})
	t.Run("development build", func(t *testing.T) {
		in := newUpgradeInstall(t, rel, "bin")
		check(t, in, nil, "dev",
			`rune: self-upgrade: this is a development build (version "dev") and is not upgraded; install a release: https://github.com/rune-task-runner/rune/releases/latest`)
	})
	t.Run("go install", func(t *testing.T) {
		in := newUpgradeInstall(t, rel, "bin")
		check(t, in, []string{"RUNE_TEST_VERSION=0.0.1", "RUNE_TEST_UNSTAMPED=1"}, "0.0.1",
			"rune: self-upgrade: this rune was built with go install; run: go install github.com/rune-task-runner/rune/cmd/rune@latest")
	})
	t.Run("homebrew", func(t *testing.T) {
		in := newUpgradeInstall(t, rel, filepath.Join("Caskroom", "rune", "0.7.0"))
		check(t, in, []string{"RUNE_TEST_VERSION=0.0.1"}, "0.0.1", "; run: brew upgrade rune")
		if !strings.Contains(in.exec(t, t.TempDir(), []string{"RUNE_TEST_VERSION=0.0.1"}, "--self-upgrade").stderr, "this rune was installed with Homebrew (") {
			t.Error("the Homebrew refusal does not name Homebrew")
		}
	})
	t.Run("homebrew through a link", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic links need extra rights on Windows")
		}
		in := newUpgradeInstall(t, rel, filepath.Join("Caskroom", "rune", "0.7.0"))
		linkDir := t.TempDir()
		link := filepath.Join(linkDir, "rune")
		if err := os.Symlink(in.bin, link); err != nil {
			t.Fatal(err)
		}
		r := runBin(t, link, t.TempDir(), append(append([]string{}, in.env...), "RUNE_TEST_VERSION=0.0.1"), "--self-upgrade")
		if r.code != 1 || !strings.Contains(r.stderr, "; run: brew upgrade rune") {
			t.Errorf("exit %d; stderr:\n%s", r.code, r.stderr)
		}
		in.unchanged(t)
	})

	if rel.Requests() != 0 {
		t.Errorf("%d requests made by refused upgrades, want 0", rel.Requests())
	}
}
