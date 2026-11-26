//go:build unix

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/sandbox"
)

// realTempDir is a temporary directory with its links followed, as a
// workspace is.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// confinedAdapter is an adapter whose calls go through the stub backend:
// the preflight, the launcher and the worker all run, and only the
// confinement itself is left out. The worker is this test binary (see
// TestMain).
func confinedAdapter(t *testing.T, src string) (*mcpAdapter, *stubBackend) {
	t.Helper()
	a := adapterFor(t, src)
	a.root = realTempDir(t)
	a.workDir = a.root
	runefile := filepath.Join(a.root, "Runefile")
	if err := os.WriteFile(runefile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	a.loaded = []string{runefile}
	stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap-stub"}
	base := filepath.Join(realTempDir(t), "base")
	a.sandbox = newSandboxState(sandbox.ModeEnforce, stub, os.Args[0], a.root, base, "", "", func(string) string { return "" }, time.Now)
	return a, stub
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCallConfined(t *testing.T) {
	a, stub := confinedAdapter(t, "greet name=\"world\":\n    @echo hi {{name}}\n    @echo err >&2\n\n[network]\nfetch:\n    @echo fetched\n")
	res, err := a.Call(context.Background(), "greet", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Stdout != "hi Ada\n" || res.Stderr != "err\n" {
		t.Errorf("result = %+v", res)
	}
	spec := stub.lastSpec()
	if spec.Workspace != a.root || spec.Network || spec.Exe != os.Args[0] || spec.ReadOnly {
		t.Errorf("spec = %+v", spec)
	}
	if spec.Tmp == "" || !strings.HasPrefix(spec.Tmp, a.sandbox.base) || spec.Cache != a.sandbox.cacheDir() {
		t.Errorf("private directories: tmp %q, cache %q", spec.Tmp, spec.Cache)
	}
	want := []string{filepath.Join(a.root, ".rune"), filepath.Join(a.root, "Runefile")}
	if !slices.Equal(spec.Protected, want) {
		t.Errorf("protected = %v, want %v", spec.Protected, want)
	}
	if exists(spec.Tmp) {
		t.Errorf("the temporary directory %s was not removed", spec.Tmp)
	}

	if _, err := a.Call(context.Background(), "fetch", nil); err != nil {
		t.Fatal(err)
	}
	if !stub.lastSpec().Network {
		t.Error("a [network] task ran without the network flag")
	}
}

func TestCallCreatesRuneDir(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @echo hi\n")
	if exists(filepath.Join(a.root, ".rune")) {
		t.Fatal(".rune exists before the call")
	}
	if _, err := a.Call(context.Background(), "build", nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(a.root, ".rune")); err != nil || !info.IsDir() {
		t.Errorf(".rune was not created: %v", err)
	}
}

func TestCallRefusedUnavailable(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @touch marker\n")
	stub.setProbe(sandbox.ErrNotFound)
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox is unavailable: bubblewrap (bwrap) was not found on the PATH. Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if exists(filepath.Join(a.root, "marker")) {
		t.Error("the body ran")
	}
	if len(stub.specs) != 0 {
		t.Error("a command was built for a refused call")
	}
}

func TestCallOffRunsInProcess(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	a.sandbox.mode = sandbox.ModeOff
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil || res.Stdout != "hi\n" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if len(stub.specs) != 0 || stub.probeCount() != 0 {
		t.Error("mode off used the backend")
	}
	if exists(a.sandbox.base) {
		t.Error("mode off created the private base")
	}
}

func TestCallNoStateRunsInProcess(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	a.sandbox = nil
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil || res.Stdout != "hi\n" || len(stub.specs) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestCallRefusedDirOutside(t *testing.T) {
	src := "[working-directory(\"/\")]\nelsewhere:\n    @touch " + "rune-marker\n\nbuild: elsewhere\n    @echo hi\n"
	for _, task := range []string{"elsewhere", "build"} {
		a, stub := confinedAdapter(t, src)
		_, err := a.Call(context.Background(), task, nil)
		want := `task "` + task + `" was not run: the sandbox cannot confine it: the working directory of task "elsewhere" (/) is outside the project (` + a.root + `)`
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", task, err, want)
		}
		if len(stub.specs) != 0 {
			t.Errorf("%s: a body ran", task)
		}
	}
}

func TestCallRefusedLinked(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @touch marker\n")
	runefile := filepath.Join(a.root, "Runefile")
	if err := os.Link(runefile, filepath.Join(a.root, "other-name")); err != nil {
		t.Fatal(err)
	}
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot protect ` + runefile + `: it has 2 names (hard links), and a task could change it through another one. Remove the other names, or replace the file with a copy of itself.`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if exists(filepath.Join(a.root, "marker")) {
		t.Error("the body ran")
	}
}

func TestCallRefusedSymlink(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @touch marker\n")
	if err := os.Mkdir(filepath.Join(a.root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.root, "real", "t.rune"), []byte("x:\n    @true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(a.root, "sub")
	if err := os.Symlink(filepath.Join(a.root, "real"), link); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(link, "t.rune")
	a.loaded = append(a.loaded, imported)
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot protect ` + imported + `: it is reached through the symbolic link ` + link + `, which a task could replace. Use the real path.`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestCallRefusedMissingFile(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @echo hi\n")
	missing := filepath.Join(a.root, "local.rune")
	a.missing = []string{filepath.Join(filepath.Dir(a.root), "outside.rune"), missing}
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot protect ` + missing + `: the Runefile looks for it and it does not exist, so a task could create it. Create the file, or change the Runefile so that it is not looked for (give a module its path, or remove the optional import).`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	// A file that exists by now is the reloader's business, not a refusal.
	if err := os.WriteFile(missing, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Call(context.Background(), "build", nil); err != nil {
		t.Errorf("with the file created: %v", err)
	}
}

// A load records a module's earlier candidate names and an absent optional
// import as looked for, and nothing for an explicit path or a dotenv file.
func TestLoadRecordsMissingFiles(t *testing.T) {
	dir := realTempDir(t)
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Runefile", "set dotenv := \".env\"\nimport? \"local.rune\"\nimport \"shared.rune\"\nmod api\nmod web \"services/web.rune\"\n\nbuild:\n    @echo hi\n")
	write("shared.rune", "shared:\n    @true\n")
	write("api/Runefile", "serve:\n    @true\n")
	write("services/web.rune", "start:\n    @true\n")
	a, _, err := newMCPAdapter(Options{Stderr: &bytes.Buffer{}}, filepath.Join(dir, "Runefile"))
	if err != nil {
		t.Fatal(err)
	}
	wantLoaded := []string{"Runefile", "api/Runefile", "services/web.rune", "shared.rune"}
	wantMissing := []string{"api.rune", "local.rune"}
	rel := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			r, err := filepath.Rel(dir, p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		slices.Sort(out)
		return out
	}
	if got := rel(a.loaded); !slices.Equal(got, wantLoaded) {
		t.Errorf("loaded = %v, want %v", got, wantLoaded)
	}
	if got := rel(a.missing); !slices.Equal(got, wantMissing) {
		t.Errorf("missing = %v, want %v", got, wantMissing)
	}
}

func TestCallRefusedPrivateDir(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @touch marker\n")
	if err := os.WriteFile(a.sandbox.base, []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot use its private directory ` + a.sandbox.cacheDir() + `: not a directory`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	a.sandbox.base = filepath.Join(a.root, "cache")
	_, err = a.Call(context.Background(), "build", nil)
	want = `task "build" was not run: the sandbox cannot use its private directory ` + a.sandbox.base + `: it is inside the project; set XDG_CACHE_HOME to a directory outside it`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	a.sandbox.base = ""
	if _, err = a.Call(context.Background(), "build", nil); err == nil || !strings.Contains(err.Error(), "this user has no cache directory") {
		t.Fatalf("error = %v", err)
	}
	if exists(filepath.Join(a.root, "marker")) {
		t.Error("the body ran")
	}
}

func TestCallEnvHasPrivateDirs(t *testing.T) {
	src := "show:\n    @echo $TMPDIR $XDG_CACHE_HOME\n\n[env(\"TMPDIR\", \"mine\")]\nown:\n    @echo $TMPDIR\n"
	a, _ := confinedAdapter(t, src)
	res, err := a.Call(context.Background(), "show", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp " + a.sandbox.cacheDir() + "\n"; res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
	res, err = a.Call(context.Background(), "own", nil)
	if err != nil || res.Stdout != "mine\n" {
		t.Errorf("a task's own [env]: %q, %v", res.Stdout, err)
	}
}

func TestProtectedSet(t *testing.T) {
	a, _ := confinedAdapter(t, "set dotenv := \".env\"\n\nbuild:\n    @echo hi\n")
	ws := a.root
	outside := filepath.Join(realTempDir(t), "shared.rune")
	a.loaded = append(a.loaded, filepath.Join(ws, "sub", "t.rune"), outside, filepath.Join(ws, "Runefile"))

	all, policy := a.protectedPaths(ws)
	wantPolicy := []string{filepath.Join(ws, ".env"), filepath.Join(ws, "Runefile"), filepath.Join(ws, "sub", "t.rune")}
	if !slices.Equal(policy, wantPolicy) {
		t.Errorf("policy = %v, want %v", policy, wantPolicy)
	}
	wantAll := []string{filepath.Join(ws, ".env"), filepath.Join(ws, ".git"), filepath.Join(ws, ".rune"), filepath.Join(ws, "Runefile"), filepath.Join(ws, "sub", "t.rune")}
	if !slices.Equal(all, wantAll) {
		t.Errorf("all = %v, want %v", all, wantAll)
	}

	// What a body's sandbox gets is what exists: no dotenv file, no .git and
	// no imported file here.
	confined, err := a.sandboxPreflight(context.Background(), &engine{workDir: ws, root: ws}, a.tasks["build"])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(ws, ".rune"), filepath.Join(ws, "Runefile")}; !slices.Equal(confined.protected, want) {
		t.Errorf("protected = %v, want %v", confined.protected, want)
	}
	// A .git file, as a worktree has, and a dotenv file are protected once
	// they exist.
	for _, name := range []string{".git", ".env"} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	confined, err = a.sandboxPreflight(context.Background(), &engine{workDir: ws, root: ws}, a.tasks["build"])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(ws, ".env"), filepath.Join(ws, ".git"), filepath.Join(ws, ".rune"), filepath.Join(ws, "Runefile")}; !slices.Equal(confined.protected, want) {
		t.Errorf("protected = %v, want %v", confined.protected, want)
	}
}

// A project reached through a link keeps its protected set: the load wrote
// its paths through the link, and the workspace is the real directory.
func TestProtectedSetThroughSymlink(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @echo hi\n")
	ws := a.root
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	a.root = link
	a.loaded = []string{filepath.Join(link, "Runefile"), filepath.Join(link, "sub", "t.rune")}
	_, policy := a.protectedPaths(ws)
	want := []string{filepath.Join(ws, "Runefile"), filepath.Join(ws, "sub", "t.rune")}
	if !slices.Equal(policy, want) {
		t.Errorf("policy = %v, want %v", policy, want)
	}
	// A path that reaches the workspace another way is resolved whole.
	other := filepath.Join(realTempDir(t), "alias")
	if err := os.Symlink(filepath.Join(ws, "Runefile"), other); err != nil {
		t.Fatal(err)
	}
	if got := a.inWorkspace(ws, other); got != filepath.Join(ws, "Runefile") {
		t.Errorf("an alias resolved to %q", got)
	}
	if got := a.inWorkspace(ws, "/etc/hosts"); got != "" {
		t.Errorf("a path outside placed at %q", got)
	}
}

func TestPrivateDirs(t *testing.T) {
	ws := realTempDir(t)
	base := filepath.Join(realTempDir(t), "base")
	clock := time.Now()
	s := testState(sandbox.ModeEnforce, &stubBackend{name: "bubblewrap"}, ws, base, nil, &clock)

	old := filepath.Join(base, "tmp", "old")
	fresh := filepath.Join(base, "tmp", "fresh")
	left := filepath.Join(base, "tmp", "left")
	running := filepath.Join(base, "tmp", "running")
	for _, dir := range []string{old, fresh, left, running} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A lock nobody holds is what a server that was killed leaves behind,
	// beside its directory or alone.
	orphan := filepath.Join(base, "tmp", "gone"+tmpLockSuffix)
	for _, lock := range []string{left + tmpLockSuffix, orphan} {
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A lock that is held is a body that runs.
	release, err := holdTmp(running + tmpLockSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// A body that has run for more than a day still owns its directory: its
	// age says nothing while its server lives.
	for _, dir := range []string{old, running} {
		if err := os.Chtimes(dir, clock.Add(-25*time.Hour), clock.Add(-25*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	tmp, cache, cleanup, err := s.privateDirs()
	if err != nil {
		t.Fatal(err)
	}
	if cache != filepath.Join(base, "cache") || filepath.Dir(tmp) != filepath.Join(base, "tmp") {
		t.Errorf("tmp %q, cache %q", tmp, cache)
	}
	for _, dir := range []string{tmp, cache} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, mode %v", dir, err, info.Mode().Perm())
		}
	}
	if exists(old) || !exists(fresh) {
		t.Errorf("stale removed: %v, fresh kept: %v", !exists(old), exists(fresh))
	}
	if exists(left) || exists(left+tmpLockSuffix) || exists(orphan) {
		t.Errorf("what a dead server left is still there: directory %v, its lock %v, a lone lock %v", exists(left), exists(left+tmpLockSuffix), exists(orphan))
	}
	if !exists(running) {
		t.Error("the directory of a body that runs was removed")
	}
	if !tmpHeld(tmp + tmpLockSuffix) {
		t.Error("the new temporary directory is not marked as in use")
	}
	cleanup()
	if exists(tmp) || exists(tmp+tmpLockSuffix) || !exists(cache) {
		t.Errorf("after cleanup: tmp exists %v, its lock exists %v, cache exists %v", exists(tmp), exists(tmp+tmpLockSuffix), exists(cache))
	}

	inside := testState(sandbox.ModeEnforce, &stubBackend{}, ws, filepath.Join(ws, ".cache"), nil, nil)
	var dirErr *privateDirError
	if _, _, _, err := inside.privateDirs(); !errors.As(err, &dirErr) || !strings.Contains(err.Error(), "it is inside the project") {
		t.Errorf("a base inside the workspace: %v", err)
	}
	file := filepath.Join(realTempDir(t), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	taken := testState(sandbox.ModeEnforce, &stubBackend{}, ws, file, nil, nil)
	if _, _, _, err := taken.privateDirs(); !errors.As(err, &dirErr) {
		t.Errorf("a base that is a file: %v", err)
	}
	if got := reasonOf(errors.New("plain")); got != "plain" {
		t.Errorf("reasonOf = %q", got)
	}
}

func TestHiddenLocations(t *testing.T) {
	root := realTempDir(t)
	home := filepath.Join(root, "home")
	for _, dir := range []string{".ssh", ".config/gh", "agent", "docker", "audit"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{".netrc", "agent/ssh.sock", "docker/docker.sock"} {
		if err := os.WriteFile(filepath.Join(home, file), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A link that leads nowhere hides nothing, and no mount can go onto it.
	if err := os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(home, ".npmrc")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SSH_AUTH_SOCK": filepath.Join(home, "agent", "ssh.sock"),
		"DOCKER_HOST":   "unix://" + filepath.Join(home, "docker", "docker.sock"),
	}
	s := newSandboxState(sandbox.ModeEnforce, &stubBackend{}, "/opt/rune", "/ws", "/base", filepath.Join(home, "audit"), home, func(key string) string { return env[key] }, time.Now)
	// The fixture is in a temporary directory, which a real state leaves out.
	s.replaced = nil
	dirs, files := s.hidden()
	wantDirs := []string{filepath.Join(home, ".config", "gh"), filepath.Join(home, ".ssh"), filepath.Join(home, "audit")}
	wantFiles := []string{filepath.Join(home, ".netrc"), filepath.Join(home, "agent", "ssh.sock"), filepath.Join(home, "docker", "docker.sock")}
	if !slices.Equal(dirs, wantDirs) {
		t.Errorf("dirs = %v, want %v", dirs, wantDirs)
	}
	if !slices.Equal(files, wantFiles) {
		t.Errorf("files = %v, want %v", files, wantFiles)
	}

	env["DOCKER_HOST"] = "tcp://127.0.0.1:2375"
	env["SSH_AUTH_SOCK"] = "relative.sock"
	_, files = s.hidden()
	if want := []string{filepath.Join(home, ".netrc")}; !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}

	// What lies under a directory the sandbox replaces is hidden with it.
	s.replaced = []string{root}
	if dirs, files := s.hidden(); len(dirs) != 0 || len(files) != 0 {
		t.Errorf("locations under a replaced directory were listed: %v %v", dirs, files)
	}
}

// The cache directory is mounted at its own path, so the state of a host
// holds it with its links followed, as it holds the workspace.
func TestHostSandboxStateResolvesBase(t *testing.T) {
	real := realTempDir(t)
	link := filepath.Join(realTempDir(t), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(link, ".cache"))
	root := realTempDir(t)
	s := hostSandboxState(Options{}, sandbox.ModeEnforce, root, filepath.Join(root, "Runefile"))
	if s.base == "" || !sandbox.Within(real, s.base) {
		t.Errorf("base = %q, want it under %q", s.base, real)
	}
	if !sandbox.Within(s.base, s.cacheDir()) {
		t.Errorf("cache = %q, outside %q", s.cacheDir(), s.base)
	}
}

// gitRepo makes a repository with one commit.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := realTempDir(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestGitStateUsesBuilder(t *testing.T) {
	repo := gitRepo(t)
	var calls [][]string
	builder := func(ctx context.Context, dir string, args ...string) *exec.Cmd {
		calls = append(calls, args)
		return plainGit(ctx, dir, args...)
	}
	state, commit, dirty := gitStateWith(context.Background(), repo, builder)
	wantState, wantCommit, wantDirty := gitState(context.Background(), repo)
	if state != wantState || commit != wantCommit || dirty == nil || wantDirty == nil || *dirty != *wantDirty {
		t.Errorf("state %q %q %v, want %q %q %v", state, commit, dirty, wantState, wantCommit, wantDirty)
	}
	if state != gitStateCommit || len(calls) != 3 {
		t.Errorf("state %q after %d git commands", state, len(calls))
	}
	for _, args := range calls {
		if len(args) < 2 || args[0] != "-c" || args[1] != "core.fsmonitor=false" {
			t.Errorf("a git command without the fsmonitor guard: %v", args)
		}
	}
}

func TestGitInfoUsesBuilder(t *testing.T) {
	repo := gitRepo(t)
	calls := 0
	builder := func(ctx context.Context, dir string, args ...string) *exec.Cmd {
		calls++
		return plainGit(ctx, dir, args...)
	}
	commit, dirty := gitInfoWith(context.Background(), repo, builder)
	wantCommit, wantDirty := gitInfo(context.Background(), repo)
	if commit != wantCommit || commit == "" || dirty == nil || *dirty != *wantDirty || calls != 2 {
		t.Errorf("commit %q, dirty %v, %d commands", commit, dirty, calls)
	}
}

// The state builds git's command: plain when nothing is enforced, through
// the backend with a read-only spec when it is, and one that cannot run when
// the backend is unavailable.
func TestSandboxGitCommand(t *testing.T) {
	repo := gitRepo(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	off := testState(sandbox.ModeOff, &stubBackend{name: "bubblewrap"}, repo, "/base", nil, nil)
	if cmd := off.gitRunner()(ctx, repo, "status"); filepath.Base(cmd.Path) != "git" || !slices.Equal(cmd.Args[1:], []string{"-C", repo, "status"}) {
		t.Errorf("mode off: %v", cmd.Args)
	}
	var none *sandboxState
	if cmd := none.gitRunner()(ctx, repo, "status"); filepath.Base(cmd.Path) != "git" {
		t.Errorf("no state: %v", cmd.Args)
	}

	stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap-stub"}
	confining := testState(sandbox.ModeEnforce, stub, sub, "/base", nil, nil)
	state, commit, _ := gitStateWith(ctx, sub, confining.gitRunner())
	if state != gitStateCommit || commit == "" {
		t.Errorf("state %q, commit %q", state, commit)
	}
	spec := stub.lastSpec()
	if !spec.ReadOnly || spec.Workspace != repo || spec.Tmp != "" || spec.Cache != "" || spec.Network || len(spec.Protected) != 0 {
		t.Errorf("git's spec = %+v", spec)
	}

	stub.setProbe(sandbox.ErrNotFound)
	unavailable := testState(sandbox.ModeEnforce, stub, sub, "/base", nil, nil)
	before := len(stub.specs)
	if state, _, _ := gitStateWith(ctx, sub, unavailable.gitRunner()); state != gitStateUnavailable {
		t.Errorf("with no sandbox git answered %q", state)
	}
	if len(stub.specs) != before {
		t.Error("git ran for a call that will be refused")
	}
}

func TestAuditIdentityUsesSandbox(t *testing.T) {
	repo := gitRepo(t)
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	a.root = repo
	id := a.AuditIdentity(context.Background())
	if id.GitState != gitStateCommit || id.GitCommit == "" {
		t.Errorf("identity = %+v", id)
	}
	if spec := stub.lastSpec(); !spec.ReadOnly || spec.Workspace != repo {
		t.Errorf("git ran outside the sandbox: %+v", spec)
	}
	a.gitState = func(context.Context, string) (string, string, *bool) { return "none", "", nil }
	if id := a.AuditIdentity(context.Background()); id.GitState != "none" {
		t.Errorf("the test hook was not used: %+v", id)
	}
	a.gitState, a.sandbox = nil, nil
	if id := a.AuditIdentity(context.Background()); id.GitState != gitStateCommit {
		t.Errorf("with no state: %+v", id)
	}
}

func TestCloseInheritedOnExec(t *testing.T) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("no /proc/self/fd on this system")
	}
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Close(fds[0])
		_ = syscall.Close(fds[1])
	}()
	flags := func(fd int) uintptr {
		got, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno != 0 {
			t.Fatal(errno)
		}
		return got
	}
	if flags(fds[0])&syscall.FD_CLOEXEC != 0 {
		t.Fatal("the pipe already has the flag")
	}
	closeInheritedOnExec()
	for _, fd := range fds {
		if flags(fd)&syscall.FD_CLOEXEC == 0 {
			t.Errorf("descriptor %d was left open across exec", fd)
		}
	}
}

// agentEngine is an engine for an (agent) task a person runs: a Runefile
// with a [context] task that leaves a marker, a tool, and a stand-in agent
// that leaves a marker of its own.
func agentTaskEngine(t *testing.T) (*engine, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	src := `set agent_cmd := ["sh", "-c", ": > agent.ran; printf done", "stub"]` + "\n\n" +
		"[context]\nhealth:\n    @: > context.ran\n    @echo healthy\n\n" +
		"build:\n    @: > build.ran\n\n" +
		"review (agent):\n    Review.\n"
	eng, stdout, stderr := confirmEngine(t, src, nil, false, false)
	eng.root = realTempDir(t)
	eng.workDir = eng.root
	eng.opts.AuditDir, eng.opts.AuditRetention = t.TempDir(), "0"
	eng.planIdent = planIdentity{runefile: filepath.Join(eng.root, "Runefile"), def: "aa", config: "bb"}
	eng.loaded = []string{filepath.Join(eng.root, "Runefile")}
	eng.missing = []string{filepath.Join(eng.root, "local.rune")}
	return eng, stdout, stderr
}

func runAgentTask(eng *engine) error {
	return eng.executeAgent(eng.tasks["review"], []shell.Line{{Text: "Review."}}, eng.workDir, os.Environ())
}

// Spec 053: the [context] task and the endpoint of an (agent) task a person
// runs are confined under the mode of RUNE_SANDBOX, and the person is told
// first when they are not.
func TestAgentTaskSandbox(t *testing.T) {
	const offWarning = "warning: sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can.\n"

	t.Run("off", func(t *testing.T) {
		eng, stdout, stderr := agentTaskEngine(t)
		eng.opts.SandboxEnv = "off"
		if err := runAgentTask(eng); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stderr.String(), offWarning) || strings.Count(stderr.String(), "warning: sandbox:") != 1 {
			t.Errorf("stderr = %q", stderr.String())
		}
		if stdout.String() != "done\n" || !exists(filepath.Join(eng.root, "context.ran")) || !exists(filepath.Join(eng.root, "agent.ran")) {
			t.Errorf("stdout %q, context ran %v", stdout.String(), exists(filepath.Join(eng.root, "context.ran")))
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		eng, _, stderr := agentTaskEngine(t)
		stub := &stubBackend{name: "bubblewrap", probeErr: sandbox.ErrNotFound}
		eng.endpointSandbox = newSandboxState(sandbox.ModeEnforce, stub, os.Args[0], eng.root, filepath.Join(realTempDir(t), "base"), "", "", func(string) string { return "" }, time.Now)
		if err := runAgentTask(eng); err != nil {
			t.Fatal(err)
		}
		want := "warning: sandbox: bubblewrap (bwrap) was not found on the PATH. Every task call will be refused. Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.\n"
		if !strings.HasPrefix(stderr.String(), want) {
			t.Errorf("stderr = %q", stderr.String())
		}
		if exists(filepath.Join(eng.root, "context.ran")) {
			t.Error("the [context] task ran with no sandbox")
		}
		if !exists(filepath.Join(eng.root, "agent.ran")) {
			t.Error("the agent did not run")
		}
	})

	t.Run("confining", func(t *testing.T) {
		eng, _, stderr := agentTaskEngine(t)
		stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap-stub"}
		eng.endpointSandbox = newSandboxState(sandbox.ModeEnforce, stub, os.Args[0], eng.root, filepath.Join(realTempDir(t), "base"), "", "", func(string) string { return "" }, time.Now)
		// The load's files are part of what the endpoint protects.
		if err := os.WriteFile(filepath.Join(eng.root, "Runefile"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		eng.missing = nil
		if err := runAgentTask(eng); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr.String(), "warning: sandbox:") {
			t.Errorf("a confining endpoint warned: %q", stderr.String())
		}
		// The specs hold git's read-only one as well: the body's is writable.
		body := slices.IndexFunc(stub.specs, func(spec sandbox.Spec) bool { return !spec.ReadOnly })
		if !exists(filepath.Join(eng.root, "context.ran")) || body < 0 {
			t.Fatalf("the [context] task did not run confined: %+v", stub.specs)
		}
		if spec := stub.specs[body]; spec.Workspace != eng.root || !slices.Contains(spec.Protected, filepath.Join(eng.root, "Runefile")) {
			t.Errorf("the [context] task's sandbox: %+v", spec)
		}
	})

	t.Run("a mode that cannot be read", func(t *testing.T) {
		eng, _, _ := agentTaskEngine(t)
		eng.opts.SandboxEnv = "maybe"
		err := runAgentTask(eng)
		var usage *UsageError
		if !errors.As(err, &usage) || err.Error() != `RUNE_SANDBOX: "maybe" is not a mode; use enforce or off` {
			t.Fatalf("error = %v", err)
		}
		for _, marker := range []string{"agent.ran", "context.ran"} {
			if exists(filepath.Join(eng.root, marker)) {
				t.Errorf("%s exists", marker)
			}
		}
	})
}

// The endpoint's adapter gets the state and the load's files, and its calls
// are refused when the state cannot confine.
func TestConfineEndpoint(t *testing.T) {
	eng, _, _ := agentTaskEngine(t)
	stub := &stubBackend{name: "bubblewrap", probeErr: sandbox.ErrNotFound}
	eng.endpointSandbox = newSandboxState(sandbox.ModeEnforce, stub, os.Args[0], eng.root, filepath.Join(realTempDir(t), "base"), "", "", func(string) string { return "" }, time.Now)
	adapter := eng.newAgentAdapter()
	if err := eng.confineEndpoint(adapter); err != nil {
		t.Fatal(err)
	}
	if adapter.sandbox != eng.endpointSandbox || !slices.Equal(adapter.loaded, eng.loaded) || !slices.Equal(adapter.missing, eng.missing) {
		t.Errorf("adapter: sandbox %p, loaded %v, missing %v", adapter.sandbox, adapter.loaded, adapter.missing)
	}
	_, err := adapter.Call(context.Background(), "build", nil)
	if err == nil || !strings.HasPrefix(err.Error(), `task "build" was not run: the sandbox is unavailable: `) {
		t.Errorf("call = %v", err)
	}
	if exists(filepath.Join(eng.root, "build.ran")) {
		t.Error("the tool ran with no sandbox")
	}
	// The endpoint's auditor records the state.
	_, aud, err := eng.startAgentRun("review", adapter)
	if err != nil {
		t.Fatal(err)
	}
	if got := aud.sandboxOf(context.Background()); got.Enforced || got.Mode != "enforce" {
		t.Errorf("recorded sandbox = %+v", got)
	}
}

// An adapter from newAgentAdapter alone has no state and runs in process,
// as every adapter did before spec 053.
func TestNewAgentAdapterHasNoState(t *testing.T) {
	eng, _, _ := agentTaskEngine(t)
	adapter := eng.newAgentAdapter()
	if adapter.sandbox != nil {
		t.Fatal("the adapter has a state")
	}
	if _, err := adapter.Call(context.Background(), "build", nil); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(eng.root, "build.ran")) {
		t.Error("the tool did not run")
	}
}

func TestEmptyFile(t *testing.T) {
	ws := realTempDir(t)
	base := filepath.Join(realTempDir(t), "base")
	s := testState(sandbox.ModeEnforce, &stubBackend{}, ws, base, nil, nil)
	path := s.emptyFile()
	info, err := os.Stat(path)
	if err != nil || path != filepath.Join(base, "empty") || info.Size() != 0 || !info.Mode().IsRegular() {
		t.Fatalf("empty file %q: %v", path, err)
	}
	// It is made once and found again.
	if again := s.emptyFile(); again != path {
		t.Errorf("second call = %q", again)
	}
	// A file that is no longer empty is put right, not handed out.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.emptyFile(); got != path {
		t.Fatalf("after the file was filled: %q", got)
	}
	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Errorf("the file still holds %q", data)
	}

	for name, state := range map[string]*sandboxState{
		"no base":                    testState(sandbox.ModeEnforce, &stubBackend{}, ws, "", nil, nil),
		"a base inside the project":  testState(sandbox.ModeEnforce, &stubBackend{}, ws, filepath.Join(ws, "cache"), nil, nil),
		"a base that cannot be made": testState(sandbox.ModeEnforce, &stubBackend{}, ws, filepath.Join(path, "under-a-file"), nil, nil),
	} {
		if got := state.emptyFile(); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// Bodies that start at once all get the file: the one that comes second
// cannot write the read-only file the first made, and takes it as it is.
func TestEmptyFileConcurrent(t *testing.T) {
	ws := realTempDir(t)
	base := filepath.Join(realTempDir(t), "base")
	s := testState(sandbox.ModeEnforce, &stubBackend{}, ws, base, nil, nil)
	const bodies = 16
	got := make([]string, bodies)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() { got[i] = s.emptyFile() })
	}
	wg.Wait()
	for i, path := range got {
		if path != filepath.Join(base, "empty") {
			t.Errorf("body %d got %q", i, path)
		}
	}
}

func TestTaskWritable(t *testing.T) {
	s := testState(sandbox.ModeEnforce, &stubBackend{}, "/ws", "/cache/base", nil, nil)
	for path, want := range map[string]bool{
		"":                  false,
		"/ws/bin/tool":      true,
		"/ws":               true,
		"/cache/base/x":     true,
		"/usr/bin/bwrap":    false,
		"/wsx/tool":         false,
		"/cache/basement/x": false,
	} {
		if got := s.taskWritable(path); got != want {
			t.Errorf("taskWritable(%q) = %v, want %v", path, got, want)
		}
	}
	noBase := testState(sandbox.ModeEnforce, &stubBackend{}, "/ws", "", nil, nil)
	if noBase.taskWritable("/cache/base/x") {
		t.Error("a state with no base counts a path outside the project as writable")
	}
}

func TestSandboxBase(t *testing.T) {
	cache := realTempDir(t)
	t.Setenv("XDG_CACHE_HOME", cache)
	a, err := sandboxBase("/repo/Runefile")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := sandboxBase("/other/Runefile")
	if !strings.HasPrefix(a, filepath.Join(cache, "rune", "sandbox")+string(filepath.Separator)) || a == b {
		t.Errorf("bases %q and %q", a, b)
	}
	if again, _ := sandboxBase("/repo/Runefile"); again != a {
		t.Errorf("one Runefile has two bases: %q and %q", a, again)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := sandboxBase("/repo/Runefile"); err == nil {
		t.Error("no cache directory gave no error")
	}
}

// A server that found its Runefile by searching up from a directory below it
// would search again at its next start, and a task could have put a nearer
// Runefile there. The call is refused.
func TestCallRefusedSearchedRunefile(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @: > marker\n")
	below := filepath.Join(a.root, "pkg")
	if err := os.Mkdir(below, 0o755); err != nil {
		t.Fatal(err)
	}
	a.sandbox.runefile = filepath.Join(a.root, "Runefile")
	a.sandbox.searchedFrom = below
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot protect the choice of the Runefile: rune found ` + a.sandbox.runefile + ` by searching up from ` + below + `, where a task could create a nearer one. Start rune in ` + a.root + `, or name the Runefile with --file.`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if exists(filepath.Join(a.root, "marker")) {
		t.Error("the body ran")
	}
}

// The state of a host notes where the search for the Runefile started, only
// when a search happened and started somewhere else.
func TestHostSandboxStateSearchedFrom(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", realTempDir(t))
	root := realTempDir(t)
	below := filepath.Join(root, "pkg")
	if err := os.Mkdir(below, 0o755); err != nil {
		t.Fatal(err)
	}
	runefile := filepath.Join(root, "Runefile")
	for _, c := range []struct {
		name string
		opts Options
		want string
	}{
		{"started in the Runefile's directory", Options{Cwd: root}, ""},
		{"started below it", Options{Cwd: below}, below},
		{"started below it with --file", Options{Cwd: below, File: runefile}, ""},
		{"no working directory", Options{}, ""},
	} {
		s := hostSandboxState(c.opts, sandbox.ModeEnforce, root, runefile)
		if s.searchedFrom != c.want || s.runefile != runefile {
			t.Errorf("%s: searched from %q, want %q", c.name, s.searchedFrom, c.want)
		}
	}
}

// Git is pointed at the resolved directory, for the reason a body is: a
// link the path goes through may lie where the sandbox hides everything.
func TestGitRunnerResolvesDir(t *testing.T) {
	repo := gitRepo(t)
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap-stub"}
	s := testState(sandbox.ModeEnforce, stub, repo, "/base", nil, nil)
	cmd := s.gitRunner()(context.Background(), link, "status")
	if !slices.Equal(cmd.Args, []string{"git", "-C", repo, "status"}) {
		t.Errorf("git's command = %v", cmd.Args)
	}
	if got := stub.lastSpec().Workspace; got != repo {
		t.Errorf("git's workspace = %q, want %q", got, repo)
	}
}

// The rules git runs under are worked out once for a look at a repository,
// not once for each git command of it.
func TestGitRunnerBuildsItsSpecOnce(t *testing.T) {
	repo := gitRepo(t)
	stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap-stub"}
	asked := 0
	s := newSandboxState(sandbox.ModeEnforce, stub, os.Args[0], repo, "", "", "", func(key string) string {
		if key == "SSH_AUTH_SOCK" {
			asked++
		}
		return ""
	}, time.Now)
	state, _, _ := gitStateWith(context.Background(), repo, s.gitRunner())
	if state != gitStateCommit || len(stub.specs) != 3 {
		t.Fatalf("state %q after %d commands", state, len(stub.specs))
	}
	if asked != 1 {
		t.Errorf("the hidden locations were worked out %d times for one look", asked)
	}
	if got := (*sandboxState)(nil).gitRunner(); got == nil {
		t.Error("a nil state has no runner")
	}
}

// Calls that start at the same time each keep their own temporary directory:
// the sweep one call makes for what a dead server left must not take the
// directory another call has just made. A lock file that exists and is not
// held yet reads as left behind, so the lock is held before it has its name.
func TestPrivateDirsConcurrentCalls(t *testing.T) {
	base := filepath.Join(realTempDir(t), "base")
	s := testState(sandbox.ModeEnforce, &stubBackend{name: "bubblewrap"}, realTempDir(t), base, nil, nil)
	const calls, rounds = 16, 200
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			for range rounds {
				tmp, _, cleanup, err := s.privateDirs()
				if err != nil {
					t.Errorf("privateDirs: %v", err)
					return
				}
				// What a body would find when it starts.
				_, statErr := os.Stat(tmp)
				held := tmpHeld(tmp + tmpLockSuffix)
				cleanup()
				if statErr != nil || !held {
					t.Errorf("a call lost its temporary directory to another call's sweep: %v, lock held %v", statErr, held)
					return
				}
			}
		})
	}
	wg.Wait()
	if left, err := os.ReadDir(filepath.Join(base, "tmp")); err != nil || len(left) != 0 {
		t.Errorf("after every call cleaned up, the directory holds %d entries (%v)", len(left), err)
	}
}

func TestHoldTmp(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "body"+tmpLockSuffix)
	if tmpHeld(lock) {
		t.Error("a lock that does not exist is held")
	}
	release, err := holdTmp(lock)
	if err != nil {
		t.Fatal(err)
	}
	if !tmpHeld(lock) {
		t.Error("a held lock reads as free")
	}
	// Another body cannot take the same lock.
	if _, err := holdTmp(lock); err == nil {
		t.Error("a held lock was taken a second time")
	}
	release()
	if exists(lock) || tmpHeld(lock) {
		t.Error("a released lock is still there")
	}
	if _, err := holdTmp(filepath.Join(lock, "under-a-missing-directory")); err == nil {
		t.Error("a lock in a missing directory was taken")
	}
}

// A project that is itself a location the sandbox hides cannot be worked in:
// the call is refused with the reason, instead of a body that runs in an
// empty directory.
func TestCallRefusedHiddenProject(t *testing.T) {
	a, _ := confinedAdapter(t, "build:\n    @: > marker\n")
	a.sandbox.auditBase = a.root
	a.sandbox.replaced = nil
	_, err := a.Call(context.Background(), "build", nil)
	want := `task "build" was not run: the sandbox cannot confine it: the project (` + a.root + `) is a location the sandbox hides from tasks`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if exists(filepath.Join(a.root, "marker")) {
		t.Error("the body ran")
	}
}

// Spec 060: the directory of the local receipt signing key is hidden, where
// the state directory says it is.
func TestHiddenReceiptKeyDirectory(t *testing.T) {
	root := realTempDir(t)
	home := filepath.Join(root, "home")
	state := filepath.Join(root, "state")
	for _, dir := range []string{filepath.Join(home, ".local", "state", "rune", "receipts"), filepath.Join(state, "rune", "receipts")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{}
	s := newSandboxState(sandbox.ModeEnforce, &stubBackend{}, "/opt/rune", "/ws", "/base", "", home, func(key string) string { return env[key] }, time.Now)
	s.replaced = nil
	if dirs, _ := s.hidden(); !slices.Equal(dirs, []string{filepath.Join(home, ".local", "state", "rune", "receipts")}) {
		t.Errorf("dirs = %v", dirs)
	}
	env["XDG_STATE_HOME"] = state
	if dirs, _ := s.hidden(); !slices.Equal(dirs, []string{filepath.Join(state, "rune", "receipts")}) {
		t.Errorf("with XDG_STATE_HOME: dirs = %v", dirs)
	}
	s = newSandboxState(sandbox.ModeEnforce, &stubBackend{}, "/opt/rune", "/ws", "/base", "", "", func(string) string { return "" }, time.Now)
	s.replaced = nil
	if dirs, _ := s.hidden(); len(dirs) != 0 {
		t.Errorf("no home, no state: dirs = %v", dirs)
	}
}
