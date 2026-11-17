package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/mcpserver"
)

// lockedBuffer is stderr for a reloader: its goroutine writes while the test
// reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// reloadWait bounds a wait that is not about timing. Waits that assert the
// 2-second requirement use their own deadline (spec 043 FR-018).
const reloadWait = 10 * time.Second

// reloadFixture is a server with a running reloader on a temp directory, as
// ServeMCP wires them, and a connected client.
type reloadFixture struct {
	t       *testing.T
	dir     string
	rl      *reloader
	srv     *mcpserver.Server
	stderr  *lockedBuffer
	cs      *mcp.ClientSession
	changed chan struct{}
}

const reloadBase = "# Builds.\nbuild:\n    @echo built\n"

// startReload writes files (paths relative to a new temp directory, which
// must include Runefile), builds the server, and starts the reloader. set
// adjusts the reloader before the first load is configured.
func startReload(t *testing.T, files map[string]string, set func(*reloader)) *reloadFixture {
	t.Helper()
	fx := &reloadFixture{t: t, dir: t.TempDir(), stderr: &lockedBuffer{}, changed: make(chan struct{}, 64)}
	for name, content := range files {
		fx.write(name, content)
	}
	opts := Options{Cwd: fx.dir, Stderr: fx.stderr, Version: "1.0.0"}
	runefile := filepath.Join(fx.dir, "Runefile")
	adapter, paths, err := newMCPAdapter(opts, runefile)
	if err != nil {
		t.Fatalf("first load: %v\n%s", err, fx.stderr.String())
	}
	fx.rl = &reloader{opts: opts, runefile: runefile}
	if set != nil {
		set(fx.rl)
	}
	fx.rl.configure(adapter)
	ctx := context.Background()
	fx.srv = mcpserver.New(adapter, mcpserver.Options{
		AllowDestructive:     fx.rl.granted,
		AllowDestructiveFlag: "--allow-destructive",
		AllowList:            fx.rl.allow,
		DenyList:             fx.rl.deny,
		OutputLimit:          adapter.limit(),
		Instructions:         adapter.gatherContext(ctx, fx.stderr),
		Reloadable:           true,
	})
	fx.rl.srv = fx.srv
	closeWatch := fx.rl.open(paths)
	stop := fx.rl.start(ctx)
	fx.cs = fx.connect()
	t.Cleanup(func() {
		stop()
		closeWatch()
	})
	return fx
}

// connect opens one more client session on the server.
func (fx *reloadFixture) connect() *mcp.ClientSession {
	fx.t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := fx.srv.MCP().Connect(context.Background(), st, nil); err != nil {
		fx.t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, &mcp.ClientOptions{
		// Never blocking: a blocked send here stalls the session of a test
		// that reads fewer signals than arrive.
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case fx.changed <- struct{}{}:
			default:
			}
		},
	})
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func (fx *reloadFixture) path(name string) string {
	return filepath.Join(fx.dir, filepath.FromSlash(name))
}

func (fx *reloadFixture) write(name, content string) {
	fx.t.Helper()
	p := fx.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		fx.t.Fatal(err)
	}
}

// waitNotified waits for one tool-list-changed notification.
func (fx *reloadFixture) waitNotified(within time.Duration) {
	fx.t.Helper()
	select {
	case <-fx.changed:
	case <-time.After(within):
		fx.t.Fatalf("no tool-list-changed notification within %v\nstderr:\n%s", within, fx.stderr.String())
	}
}

// notNotified fails when a notification arrived. The caller has already
// seen the reload finish; the SDK sends 10 ms after a change, so a short
// wait covers one that is on its way.
func (fx *reloadFixture) notNotified() {
	fx.t.Helper()
	select {
	case <-fx.changed:
		fx.t.Fatalf("a tool-list-changed notification arrived\nstderr:\n%s", fx.stderr.String())
	case <-time.After(200 * time.Millisecond):
	}
}

// waitStderr waits until stderr holds text n times and returns stderr.
func (fx *reloadFixture) waitStderr(text string, n int) string {
	fx.t.Helper()
	deadline := time.Now().Add(reloadWait)
	for {
		got := fx.stderr.String()
		if strings.Count(got, text) >= n {
			return got
		}
		if time.Now().After(deadline) {
			fx.t.Fatalf("stderr does not hold %q %d time(s):\n%s", text, n, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reloads is how many reloads have finished, in success or failure.
func (fx *reloadFixture) reloads() int {
	s := fx.stderr.String()
	return strings.Count(s, "reloaded Runefile:") + strings.Count(s, "reload failed:")
}

// waitReloads waits until n reloads have finished.
func (fx *reloadFixture) waitReloads(n int) {
	fx.t.Helper()
	deadline := time.Now().Add(reloadWait)
	for fx.reloads() < n {
		if time.Now().After(deadline) {
			fx.t.Fatalf("%d reload(s) finished, want %d:\n%s", fx.reloads(), n, fx.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fx *reloadFixture) tools() []string {
	fx.t.Helper()
	return sessionTools(fx.t, fx.cs)
}

func sessionTools(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	slices.Sort(names)
	return names
}

func (fx *reloadFixture) call(tool string) (text string, isError bool) {
	fx.t.Helper()
	res, err := fx.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool})
	if err != nil {
		fx.t.Fatalf("call %s: %v", tool, err)
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func (fx *reloadFixture) wantTools(want ...string) {
	fx.t.Helper()
	if got := fx.tools(); !slices.Equal(got, want) {
		fx.t.Errorf("tools = %v, want %v\nstderr:\n%s", got, want, fx.stderr.String())
	}
}

func TestLoadModulePaths(t *testing.T) {
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	_, paths, err := newMCPAdapter(Options{Stderr: &bytes.Buffer{}}, runefile)
	if err != nil || !slices.Equal(paths, []string{runefile}) {
		t.Errorf("paths = %v, %v, want only %s", paths, err, runefile)
	}

	// A Runefile that cannot be read is still the file to wait for.
	missing := filepath.Join(dir, "gone", "Runefile")
	_, paths, err = newMCPAdapter(Options{Stderr: &bytes.Buffer{}}, missing)
	var ue *UsageError
	if !errors.As(err, &ue) || !slices.Equal(paths, []string{missing}) {
		t.Errorf("missing Runefile: paths = %v, err = %v", paths, err)
	}
}

func TestLoadModulePathsGraph(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"Runefile":       "import \"ci.rune\"\nimport? \"local.rune\"\nmod deploy\n\n" + reloadBase,
		"ci.rune":        "import \"sub/inner.rune\"\n\n# Lints.\nlint:\n    @echo linted\n",
		"sub/inner.rune": "# Inner.\ninner:\n    @echo inner\n",
		"deploy.rune":    "# Pushes.\npush:\n    @echo pushed\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runefile := filepath.Join(dir, "Runefile")
	var stderr bytes.Buffer
	_, paths, err := newMCPAdapter(Options{Stderr: &stderr}, runefile)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, stderr.String())
	}
	if paths[0] != runefile {
		t.Errorf("first path = %s, want the Runefile", paths[0])
	}
	for _, name := range []string{"ci.rune", "sub/inner.rune", "deploy.rune", "local.rune"} {
		if want := filepath.Join(dir, filepath.FromSlash(name)); !slices.Contains(paths, want) {
			t.Errorf("paths lack %s: %v", want, paths)
		}
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) || !strings.HasPrefix(p, dir) || p != filepath.Clean(p) {
			t.Errorf("path %q is not an absolute clean path under the Runefile's directory", p)
		}
	}
	if sorted := slices.Compact(slices.Sorted(slices.Values(paths))); len(sorted) != len(paths) {
		t.Errorf("paths repeat a file: %v", paths)
	}

	// A load that fails inside an import still names that import.
	if err := os.WriteFile(filepath.Join(dir, "ci.rune"), []byte("lint: (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, paths, err = newMCPAdapter(Options{Stderr: &bytes.Buffer{}}, runefile)
	if err == nil || !slices.Contains(paths, filepath.Join(dir, "ci.rune")) {
		t.Errorf("failed load: paths = %v, err = %v, want an error and ci.rune", paths, err)
	}
}

func TestPathRecorder(t *testing.T) {
	rec := &pathRecorder{}
	src := rec.wrap(func(path string) ([]byte, bool) { return []byte(path), path == "/a/found" })
	if b, ok := src("/a/found"); !ok || string(b) != "/a/found" {
		t.Errorf("wrap changed the provider's answer: %q, %v", b, ok)
	}
	src("/a/missing")
	src("/a/./found")
	rec.add("/a/b/../missing")
	// Absolute as the host spells it: Windows adds the drive.
	want := make([]string, 2)
	for i, p := range []string{"/a/found", "/a/missing"} {
		abs, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = abs
	}
	if !slices.Equal(rec.paths, want) {
		t.Errorf("paths = %v, want %v", rec.paths, want)
	}
}

func TestReloadLine(t *testing.T) {
	cases := map[mcpserver.Change]string{
		{}:                                 "reloaded Runefile: 0 tools added, 0 removed, 0 changed",
		{Added: 1}:                         "reloaded Runefile: 1 tool added, 0 removed, 0 changed",
		{Added: 2, Removed: 1, Changed: 3}: "reloaded Runefile: 2 tools added, 1 removed, 3 changed",
	}
	for change, want := range cases {
		if got := reloadLine(change); got != want {
			t.Errorf("reloadLine(%+v) = %q, want %q", change, got, want)
		}
	}
}

func TestReloaderAddsTask(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.wantTools("build")

	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	fx.waitNotified(2 * time.Second)
	fx.wantTools("build", "lint")
	if text, isError := fx.call("lint"); isError || !strings.HasPrefix(text, "linted") {
		t.Errorf("call lint = %q, isError=%v", text, isError)
	}
	if got := fx.waitStderr("reloaded Runefile:", 1); got != "reloaded Runefile: 1 tool added, 0 removed, 0 changed\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestReloaderRemoveAndChange(t *testing.T) {
	two := reloadBase + "\n# Lints.\nlint:\n    @echo linted\n"
	fx := startReload(t, map[string]string{"Runefile": two}, nil)

	fx.write("Runefile", reloadBase)
	fx.waitStderr("reloaded Runefile: 0 tools added, 1 removed, 0 changed", 1)
	fx.waitNotified(reloadWait)
	fx.wantTools("build")

	fx.write("Runefile", strings.Replace(reloadBase, "# Builds.", "# Builds everything.", 1))
	fx.waitStderr("reloaded Runefile: 0 tools added, 0 removed, 1 changed", 1)
	fx.waitNotified(reloadWait)

	fx.write("Runefile", two+"\n# Tests.\ntest:\n    @echo tested\n")
	fx.waitStderr("reloaded Runefile: 2 tools added, 0 removed, 1 changed", 1)
	fx.wantTools("build", "lint", "test")
}

func TestReloaderBodyOnlyChange(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.write("Runefile", strings.Replace(reloadBase, "echo built", "echo rebuilt", 1))
	fx.waitStderr("reloaded Runefile: 0 tools added, 0 removed, 0 changed", 1)
	fx.notNotified()
	if text, _ := fx.call("build"); !strings.HasPrefix(text, "rebuilt") {
		t.Errorf("call build = %q, want the new body's output", text)
	}
}

func TestReloaderRenameOverSave(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	content := reloadBase
	for i := 1; i <= 2; i++ {
		content += fmt.Sprintf("\n# Extra.\nextra%d:\n    @echo e\n", i)
		fx.write("Runefile.tmp", content)
		if err := os.Rename(fx.path("Runefile.tmp"), fx.path("Runefile")); err != nil {
			t.Fatal(err)
		}
		fx.waitStderr("reloaded Runefile: 1 tool added", i)
	}
	fx.wantTools("build", "extra1", "extra2")
}

// open keeps a debounce that was set before it, so a test can make the
// window long enough that a slow machine cannot split one burst in two.
func TestReloaderKeepsASetDebounce(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, func(r *reloader) { r.debounce = time.Hour })
	if fx.rl.debounce != time.Hour {
		t.Errorf("debounce = %v, want the hour set before open", fx.rl.debounce)
	}
	fx = startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	if fx.rl.debounce != reloadDebounce {
		t.Errorf("debounce = %v, want the default %v", fx.rl.debounce, reloadDebounce)
	}
}

func TestReloaderCoalesces(t *testing.T) {
	// The writes below are 15ms apart. With the default 100ms window, a
	// runner that stalled for longer than that between two of them split the
	// burst, and the count below failed on the machine's speed, not on the
	// reloader. A one-second window leaves the burst whole.
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, func(r *reloader) { r.debounce = time.Second })
	content := reloadBase
	for i := range 10 {
		content += fmt.Sprintf("\n# Extra.\nextra%d:\n    @echo e\n", i)
		fx.write("Runefile", content)
		time.Sleep(15 * time.Millisecond)
	}
	// The last write is loaded when the tool list holds its task.
	deadline := time.Now().Add(reloadWait)
	for !slices.Contains(fx.tools(), "extra9") {
		if time.Now().After(deadline) {
			t.Fatalf("the final content was never loaded: %v", fx.tools())
		}
		time.Sleep(10 * time.Millisecond)
	}
	fx.waitReloads(1)
	if n := fx.reloads(); n > 2 {
		t.Errorf("ten writes within a burst caused %d reloads, want at most 2:\n%s", n, fx.stderr.String())
	}
	if got := len(fx.tools()); got != 11 {
		t.Errorf("%d tools, want 11", got)
	}
}

func TestReloaderStopsOnCancel(t *testing.T) {
	events := make(chan fsnotify.Event)
	rl := &reloader{events: events, errs: make(chan error), opts: Options{Stderr: &bytes.Buffer{}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(reloadWait):
		t.Fatal("run did not return after its context ended")
	}

	// A closed watcher ends the loop too.
	done = make(chan struct{})
	go func() { rl.run(context.Background()); close(done) }()
	close(events)
	select {
	case <-done:
	case <-time.After(reloadWait):
		t.Fatal("run did not return after the watcher closed")
	}
}

func TestReloaderWatchError(t *testing.T) {
	var stderr lockedBuffer
	errs := make(chan error)
	rl := &reloader{events: make(chan fsnotify.Event), errs: errs, opts: Options{Stderr: &stderr}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.run(ctx); close(done) }()
	errs <- errors.New("queue overflow")
	errs <- errors.New("again")
	cancel()
	<-done
	const want = "warning: cannot watch the Runefile for changes: queue overflow; tools will not reload\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q once", got, want)
	}

	// The error channel closing ends the loop.
	rl = &reloader{events: make(chan fsnotify.Event), errs: errs, opts: Options{Stderr: &stderr}}
	done = make(chan struct{})
	go func() { rl.run(context.Background()); close(done) }()
	close(errs)
	select {
	case <-done:
	case <-time.After(reloadWait):
		t.Fatal("run did not return after the error channel closed")
	}
}

func TestReloaderRelevant(t *testing.T) {
	rl := &reloader{files: map[string]bool{filepath.FromSlash("/p/Runefile"): true}}
	cases := []struct {
		name string
		op   fsnotify.Op
		want bool
	}{
		{"/p/Runefile", fsnotify.Write, true},
		{"/p/Runefile", fsnotify.Create, true},
		{"/p/Runefile", fsnotify.Rename, true},
		{"/p/Runefile", fsnotify.Remove, true},
		{"/p/./Runefile", fsnotify.Write, true},
		{"/p/Runefile", fsnotify.Chmod, false},
		{"/p/notes.txt", fsnotify.Write, false},
	}
	for _, c := range cases {
		ev := fsnotify.Event{Name: filepath.FromSlash(c.name), Op: c.op}
		if got := rl.relevant(ev); got != c.want {
			t.Errorf("relevant(%s %v) = %v, want %v", c.name, c.op, got, c.want)
		}
	}
}

func TestReloaderWatchDirectories(t *testing.T) {
	var added, removed []string
	missingDir := true
	rl := &reloader{
		addDir: func(dir string) error {
			if missingDir && strings.HasSuffix(dir, "missing") {
				return errors.New("no such directory")
			}
			added = append(added, dir)
			return nil
		},
		removeDir: func(dir string) error { removed = append(removed, dir); return nil },
	}
	root := t.TempDir()
	p := func(name string) string { return filepath.Join(root, filepath.FromSlash(name)) }
	paths := []string{p("a/Runefile"), p("a/ci.rune"), p("b/x.rune"), p("missing/y.rune")}
	rl.watch(paths)
	slices.Sort(added)
	// A directory that does not exist is waited for in the one above it.
	if !slices.Equal(added, []string{root, p("a"), p("b")}) || len(removed) != 0 {
		t.Errorf("added = %v, removed = %v", added, removed)
	}
	if rl.dirs[p("missing")] || !rl.dirs[root] || !rl.files[p("missing/y.rune")] {
		t.Errorf("a missing directory: dirs = %v, files = %v", rl.dirs, rl.files)
	}
	if !rl.relevant(fsnotify.Event{Name: p("missing"), Op: fsnotify.Create}) {
		t.Errorf("the missing directory being made is not relevant: files = %v", rl.files)
	}

	// Once it is made it is watched itself, and the one above it no longer.
	if err := os.Mkdir(p("missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	missingDir, added = false, nil
	rl.watch(paths)
	if !slices.Contains(added, p("missing")) || !slices.Equal(removed, []string{root}) {
		t.Errorf("after it was made: added = %v, removed = %v", added, removed)
	}
	if !rl.dirs[p("missing")] || rl.dirs[root] || rl.files[p("missing")] {
		t.Errorf("after it was made: dirs = %v, files = %v", rl.dirs, rl.files)
	}

	removed = nil
	rl.watch([]string{p("a/Runefile")})
	slices.Sort(removed)
	if !slices.Equal(removed, []string{p("b"), p("missing")}) || rl.files[p("b/x.rune")] || rl.dirs[p("b")] {
		t.Errorf("after narrowing: removed = %v, files = %v, dirs = %v", removed, rl.files, rl.dirs)
	}
}

// A directory that exists and cannot be watched is said once. Without the
// warning its files would stop reloading in silence (FR-008).
func TestReloaderWatchRefused(t *testing.T) {
	var stderr bytes.Buffer
	dir := t.TempDir()
	rl := &reloader{
		opts:      Options{Stderr: &stderr},
		addDir:    func(string) error { return errors.New("no space left on device") },
		removeDir: func(string) error { return nil },
	}
	rl.watch([]string{filepath.Join(dir, "Runefile"), filepath.Join(dir, "gone", "x.rune")})
	rl.watch([]string{filepath.Join(dir, "Runefile")})
	const want = "warning: cannot watch the Runefile for changes: no space left on device; tools will not reload\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q once", got, want)
	}
	if rl.dirs[dir] {
		t.Errorf("dirs = %v, want the refused directory left out", rl.dirs)
	}
}

// A file reached through a symlink is watched where it is: a write to it is
// an event in its own directory.
func TestReloaderWatchSymlinkTarget(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "shared", "ci.rune")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "ci.rune")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if got, ok := symlinkTarget(link); !ok || got != target {
		t.Errorf("symlinkTarget(link) = %q, %v, want %q", got, ok, target)
	}
	for _, p := range []string{target, filepath.Join(dir, "missing.rune")} {
		if got, ok := symlinkTarget(p); ok {
			t.Errorf("symlinkTarget(%s) = %q, want none: it is not a symlink", p, got)
		}
	}

	var added []string
	rl := &reloader{
		addDir:    func(d string) error { added = append(added, d); return nil },
		removeDir: func(string) error { return nil },
	}
	rl.watch([]string{link})
	slices.Sort(added)
	if !slices.Equal(added, []string{dir, filepath.Dir(target)}) {
		t.Errorf("watched directories = %v, want the link's and the target's", added)
	}
	if !rl.relevant(fsnotify.Event{Name: target, Op: fsnotify.Write}) || !rl.relevant(fsnotify.Event{Name: link, Op: fsnotify.Create}) {
		t.Errorf("files = %v, want the link and its target", rl.files)
	}
}

// The reload follows an edit of the file a symlinked import leads to.
func TestReloaderSymlinkedImport(t *testing.T) {
	ci := "# Lints.\nlint:\n    @echo linted\n"
	fx := startReload(t, map[string]string{
		"Runefile":       "import? \"ci.rune\"\n\n" + reloadBase,
		"shared/ci.rune": ci,
	}, nil)
	if err := os.Symlink(fx.path("shared/ci.rune"), fx.path("ci.rune")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "lint")

	fx.write("shared/ci.rune", ci+"\n# Formats.\nformat:\n    @echo formatted\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 2)
	fx.wantTools("build", "format", "lint")
}

// loadedReloader is a reloader with its server on a temp Runefile, neither
// watching nor running.
func loadedReloader(t *testing.T) (fx *reloadFixture, paths []string) {
	t.Helper()
	fx = &reloadFixture{t: t, dir: t.TempDir(), stderr: &lockedBuffer{}}
	fx.write("Runefile", reloadBase)
	opts := Options{Cwd: fx.dir, Stderr: fx.stderr, Version: "1.0.0"}
	runefile := fx.path("Runefile")
	adapter, paths, err := newMCPAdapter(opts, runefile)
	if err != nil {
		t.Fatalf("first load: %v\n%s", err, fx.stderr.String())
	}
	fx.rl = &reloader{opts: opts, runefile: runefile}
	fx.rl.configure(adapter)
	fx.srv = mcpserver.New(adapter, mcpserver.Options{Reloadable: true})
	fx.rl.srv = fx.srv
	return fx, paths
}

// A save made after open and before start is loaded when the reloader
// starts. ServeMCP opens before the [context] hook and starts after it.
func TestReloaderOpenKeepsChanges(t *testing.T) {
	fx, paths := loadedReloader(t)
	closeWatch := fx.rl.open(paths)
	defer closeWatch()
	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")

	stop := fx.rl.start(context.Background())
	defer stop()
	fx.waitStderr("reloaded Runefile: 1 tool added, 0 removed, 0 changed", 1)
}

// start without a watcher runs nothing, and its stop returns.
func TestReloaderStartWithoutWatcher(t *testing.T) {
	(&reloader{}).start(context.Background())()
}

// When the watcher drops events, one of them may have been for a watched
// file: the reloader loads again and does not say that reloading stopped.
func TestReloaderEventOverflow(t *testing.T) {
	fx, _ := loadedReloader(t)
	errs := make(chan error)
	fx.rl.events, fx.rl.errs = make(chan fsnotify.Event), errs
	fx.rl.addDir = func(string) error { return nil }
	fx.rl.removeDir = func(string) error { return nil }
	fx.rl.debounce = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { fx.rl.run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	errs <- fmt.Errorf("reading events: %w", fsnotify.ErrEventOverflow)
	if got := fx.waitStderr("reloaded Runefile:", 1); got != "reloaded Runefile: 1 tool added, 0 removed, 0 changed\n" {
		t.Errorf("stderr = %q, want only the reload line", got)
	}
}

func TestReloaderFlagsStillApply(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase + "\n# Tests.\ntest:\n    @echo tested\n"}, func(rl *reloader) {
		rl.allow = []string{"build", "wipe", "reset"}
		rl.granted = []string{"wipe"}
	})
	fx.wantTools("build")
	fx.write("Runefile", reloadBase+
		"\n# Tests.\ntest:\n    @echo tested\n"+
		"\n# Wipes.\n[confirm]\nwipe:\n    @echo wiped\n"+
		"\n# Resets.\n[confirm]\nreset:\n    @echo was-reset\n")
	fx.waitStderr("reloaded Runefile: 2 tools added", 1)
	fx.wantTools("build", "reset", "wipe")
	if text, isError := fx.call("wipe"); isError || !strings.HasPrefix(text, "wiped") {
		t.Errorf("granted wipe = %q, isError=%v, want it to run", text, isError)
	}
	text, isError := fx.call("reset")
	if want := `task "reset" is destructive; to permit it, add reset to --allow-destructive and restart the server`; !isError || text != want {
		t.Errorf("ungranted reset = %q, isError=%v, want %q", text, isError, want)
	}
}

func TestReloaderSettingLimit(t *testing.T) {
	noisy := "# Prints a lot.\nnoisy:\n    @printf '%05000d' 0\n"
	fx := startReload(t, map[string]string{"Runefile": noisy}, nil)
	if text, _ := fx.call("noisy"); strings.Contains(text, "[truncated ") {
		t.Fatalf("5,000 bytes were truncated at the default limit")
	}
	fx.write("Runefile", "set mcp_output_limit := \"1KiB\"\n\n"+noisy)
	fx.waitReloads(1)
	text, _ := fx.call("noisy")
	if stdout := strings.TrimSuffix(text, "\n[exit 0]"); len(stdout) > 1024 || !strings.Contains(stdout, "[truncated ") {
		t.Errorf("after the setting: stdout is %d bytes, want at most 1024 with a marker", len(stdout))
	}

	// The operator's flag still wins over a reloaded setting.
	flagged := startReload(t, map[string]string{"Runefile": noisy}, func(rl *reloader) { rl.flagLimit = 2048 })
	flagged.write("Runefile", "set mcp_output_limit := \"1KiB\"\n\n"+noisy)
	flagged.waitReloads(1)
	text, _ = flagged.call("noisy")
	if stdout := strings.TrimSuffix(text, "\n[exit 0]"); len(stdout) <= 1024 || len(stdout) > 2048 {
		t.Errorf("with a flag of 2048: stdout is %d bytes, want between 1025 and 2048", len(stdout))
	}
}

func TestReloaderStaticErrorKeepsTools(t *testing.T) {
	cases := []struct{ name, content, diag, line string }{
		{"syntax", "build: (\n", "Runefile:2:", "reload failed: 2 static error(s); keeping the previous tools"},
		{"unknown dependency", "# Builds.\nbuild: nope\n    @echo broken\n", "Runefile:2:", "reload failed: 1 static error(s); keeping the previous tools"},
		{"cycle", "a: b\n    @echo a\nb: a\n    @echo b\n", "Runefile:", "; keeping the previous tools"},
		{"setting", "set mcp_output_limit := \"lots\"\n\n" + reloadBase, "Runefile:1:", "reload failed: invalid settings; keeping the previous tools"},
		{"minimum version", "set minimum_version := \"99.0.0\"\n\n" + reloadBase, "Runefile:1:", "reload failed: this Runefile requires Rune >= 99.0.0; keeping the previous tools"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
			fx.write("Runefile", c.content)
			got := fx.waitStderr("reload failed:", 1)
			if !strings.Contains(got, c.diag) || !strings.Contains(got, "^") {
				t.Errorf("stderr lacks a diagnostic at %q with a caret:\n%s", c.diag, got)
			}
			if !strings.HasSuffix(got, c.line+"\n") {
				t.Errorf("stderr does not end with %q:\n%s", c.line, got)
			}
			fx.notNotified()
			fx.wantTools("build")
			if text, isError := fx.call("build"); isError || !strings.HasPrefix(text, "built") {
				t.Errorf("call build = %q, isError=%v, want the task as it was", text, isError)
			}
		})
	}
}

func TestReloaderRecovers(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.write("Runefile", "build: (\n")
	fx.waitStderr("reload failed:", 1)
	fx.write("Runefile", "build: nope\n    @echo b\n")
	fx.waitStderr("reload failed:", 2)
	fx.notNotified()

	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	fx.waitNotified(2 * time.Second)
	fx.waitStderr("reloaded Runefile: 1 tool added, 0 removed, 0 changed", 1)
	fx.wantTools("build", "lint")
}

func TestReloaderDeletedRunefile(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	if err := os.Remove(fx.path("Runefile")); err != nil {
		t.Fatal(err)
	}
	got := fx.waitStderr("reload failed:", 1)
	if !strings.Contains(got, "Runefile") || !strings.HasSuffix(got, "; keeping the previous tools\n") {
		t.Errorf("stderr = %q, want the read error and the failure line", got)
	}
	fx.wantTools("build")

	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "lint")
}

// A failed reload runs no task code, the [context] hook included (FR-023).
func TestReloaderFailureRunsNothing(t *testing.T) {
	hook := "\n# Health.\n[context]\nhealth:\n    @echo run >> context.runs\n"
	fx := startReload(t, map[string]string{"Runefile": reloadBase + hook}, func(rl *reloader) { rl.useHTTP = true })
	before, err := os.ReadFile(fx.path("context.runs"))
	if err != nil {
		t.Fatalf("the hook did not run at start: %v", err)
	}
	fx.write("Runefile", "build: (\n"+hook)
	fx.waitStderr("reload failed:", 1)
	after, _ := os.ReadFile(fx.path("context.runs"))
	if !bytes.Equal(before, after) {
		t.Errorf("the hook ran during a failed reload: %q then %q", before, after)
	}
}

func TestReloaderRestoreSameContent(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.write("Runefile", "build: (\n")
	fx.waitStderr("reload failed:", 1)
	fx.write("Runefile", reloadBase)
	fx.waitStderr("reloaded Runefile: 0 tools added, 0 removed, 0 changed", 1)
	fx.notNotified()
}

func TestReloaderMasksReloadedSecrets(t *testing.T) {
	const secret = "hunter2value-0123456789"
	t.Setenv("RELOAD_API_TOKEN", secret)
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, func(rl *reloader) { rl.useHTTP = true })
	fx.write("Runefile", reloadBase+
		"\n# Leaks.\nleak:\n    @echo token=$RELOAD_API_TOKEN\n"+
		"\n# Health.\n[context]\nhealth:\n    @echo health=$RELOAD_API_TOKEN\n")
	stderr := fx.waitStderr("reloaded Runefile: 1 tool added", 1)

	text, _ := fx.call("leak")
	instructions := fx.connect().InitializeResult().Instructions
	if !strings.Contains(text, "token=***") || !strings.Contains(instructions, "health=***") {
		t.Errorf("result = %q, instructions = %q, want the mask placeholder in both", text, instructions)
	}
	for name, s := range map[string]string{"result": text, "instructions": instructions, "stderr": stderr} {
		if strings.Contains(s, secret) {
			t.Errorf("%s holds the secret: %q", name, s)
		}
	}
}

func TestReloaderStaleFlagNames(t *testing.T) {
	full := reloadBase +
		"\n# Lints.\nlint:\n    @echo linted\n" +
		"\n# Old.\nold:\n    @echo old\n" +
		"\n# Cleans.\n[confirm]\nclean:\n    @echo cleaned\n"
	fx := startReload(t, map[string]string{"Runefile": full}, func(rl *reloader) {
		rl.allow = []string{"build", "lint"}
		rl.deny = []string{"old"}
		rl.granted = []string{"clean"}
	})
	fx.wantTools("build", "lint")

	fx.write("Runefile", reloadBase)
	got := fx.waitStderr("reloaded Runefile: 0 tools added, 1 removed, 0 changed", 1)
	const want = "warning: --allow-destructive clean has no effect: the Runefile has no task \"clean\"\n" +
		"warning: --allow lint has no effect: the Runefile has no task \"lint\"\n" +
		"warning: --deny old has no effect: the Runefile has no task \"old\"\n" +
		"reloaded Runefile: 0 tools added, 1 removed, 0 changed\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	fx.wantTools("build")

	// The names take effect again when the tasks come back, and a grant on a
	// task that lost [confirm] warns as it does at start.
	fx.write("Runefile", strings.Replace(full, "[confirm]\n", "", 1))
	got = fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	if rest := strings.TrimPrefix(got, want); rest != "warning: --allow-destructive clean has no effect: task \"clean\" is not destructive\n"+
		"reloaded Runefile: 1 tool added, 0 removed, 0 changed\n" {
		t.Errorf("second reload printed %q", rest)
	}
	fx.wantTools("build", "lint")
}

func TestWarnUnknownNames(t *testing.T) {
	a := adapterFor(t, reloadBase)
	var out bytes.Buffer
	warnUnknownNames(&out, Options{Stderr: &out}.themeStderr(), "--deny", []string{"build", "gone"}, a.tasks)
	if want := "warning: --deny gone has no effect: the Runefile has no task \"gone\"\n"; out.String() != want {
		t.Errorf("warnUnknownNames printed %q, want %q", out.String(), want)
	}
}

func TestReloaderConfigure(t *testing.T) {
	a := adapterFor(t, reloadBase)
	(&reloader{granted: []string{"clean"}, flagLimit: 4096}).configure(a)
	if !a.grants["clean"] || len(a.grants) != 1 || a.outputLimit != 4096 {
		t.Errorf("configure: grants = %v, outputLimit = %d", a.grants, a.outputLimit)
	}
}

const reloadGraphRoot = "import \"ci.rune\"\nimport? \"local.rune\"\nmod deploy\n\n" + reloadBase

func reloadGraph() map[string]string {
	return map[string]string{
		"Runefile":       reloadGraphRoot,
		"ci.rune":        "import \"sub/inner.rune\"\n\n# Lints.\nlint:\n    @echo linted\n",
		"sub/inner.rune": "# Inner.\ninner:\n    @echo inner\n",
		"deploy.rune":    "# Pushes.\npush:\n    @echo pushed\n",
	}
}

func TestReloaderImportedFile(t *testing.T) {
	files := reloadGraph()
	fx := startReload(t, files, nil)
	fx.wantTools("build", "deploy__push", "inner", "lint")

	fx.write("ci.rune", files["ci.rune"]+"\n# Formats.\nformat:\n    @echo formatted\n")
	fx.waitNotified(2 * time.Second)
	fx.write("sub/inner.rune", files["sub/inner.rune"]+"\n# Deep.\ndeep:\n    @echo deep\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 2)
	fx.write("deploy.rune", files["deploy.rune"]+"\n# Rolls back.\nrollback:\n    @echo back\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 3)
	fx.wantTools("build", "deep", "deploy__push", "deploy__rollback", "format", "inner", "lint")
}

func TestReloaderGraphFollowsImports(t *testing.T) {
	extra := "# Extra.\nextra:\n    @echo extra\n"
	fx := startReload(t, map[string]string{"Runefile": reloadBase, "more/extra.rune": extra}, nil)

	fx.write("Runefile", "import \"more/extra.rune\"\n\n"+reloadBase)
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "extra")

	fx.write("more/extra.rune", extra+"\n# Second.\nsecond:\n    @echo second\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 2)
	fx.wantTools("build", "extra", "second")

	fx.write("Runefile", reloadBase)
	fx.waitStderr("reloaded Runefile: 0 tools added, 2 removed", 1)
	fx.wantTools("build")

	// The file is out of the graph: changing it reloads nothing. The edit of
	// the Runefile after it is the next reload, the fourth.
	fx.write("more/extra.rune", extra)
	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	fx.waitReloads(4)
	fx.notNotifiedAfterDrain()
	if n := fx.reloads(); n != 4 {
		t.Errorf("%d reloads, want 4:\n%s", n, fx.stderr.String())
	}
}

// notNotifiedAfterDrain drops the notifications already received, then
// checks that no further one arrives.
func (fx *reloadFixture) notNotifiedAfterDrain() {
	fx.t.Helper()
	time.Sleep(200 * time.Millisecond)
	for len(fx.changed) > 0 {
		<-fx.changed
	}
	fx.notNotified()
}

func TestReloaderFixInFailedImport(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase, "new.rune": "lint: (\n"}, nil)
	fx.write("Runefile", "import \"new.rune\"\n\n"+reloadBase)
	got := fx.waitStderr("reload failed:", 1)
	if !strings.Contains(got, "new.rune:2:") {
		t.Errorf("stderr does not point into the imported file:\n%s", got)
	}
	// Only the imported file changes.
	fx.write("new.rune", "# Lints.\nlint:\n    @echo linted\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "lint")
}

func TestReloaderCreateMissingImport(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": "import? \"local.rune\"\n\n" + reloadBase}, nil)

	// An optional import that appears.
	fx.write("local.rune", "# Local.\nlocal:\n    @echo local\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "local")

	// A required import of a file that does not exist yet.
	fx.write("Runefile", "import? \"local.rune\"\nimport \"needed.rune\"\n\n"+reloadBase)
	fx.waitStderr("reload failed:", 1)
	fx.write("needed.rune", "# Needed.\nneeded:\n    @echo needed\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 2)
	fx.wantTools("build", "local", "needed")
}

// A module named before its directory is made loads once the directory and
// its Runefile arrive, and that directory is watched from then on.
func TestReloaderCreateMissingDirectory(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.write("Runefile", "mod ops\n\n"+reloadBase)
	fx.waitStderr("reload failed:", 1)

	push := "# Pushes.\npush:\n    @echo pushed\n"
	fx.write("ops/Runefile", push)
	fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	fx.wantTools("build", "ops__push")

	fx.write("ops/Runefile", push+"\n# Pulls.\npull:\n    @echo pulled\n")
	fx.waitStderr("reloaded Runefile: 1 tool added", 2)
	fx.wantTools("build", "ops__pull", "ops__push")
}

func TestReloaderIgnoresOtherFiles(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadBase}, nil)
	fx.write("notes.txt", "x")
	fx.write("other.rune", "# Other.\nother:\n    @echo other\n")
	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n")
	fx.waitReloads(1)
	fx.notNotifiedAfterDrain()
	if n := fx.reloads(); n != 1 {
		t.Errorf("%d reloads, want 1:\n%s", n, fx.stderr.String())
	}
	fx.wantTools("build", "lint")
}

func reloadHook(text string) string {
	return reloadBase + "\n# Health.\n[context]\nhealth:\n    @echo " + text + "\n    @echo run >> context.runs\n"
}

func TestReloaderContextHTTP(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadHook("v1")}, func(rl *reloader) { rl.useHTTP = true })
	if got := fx.cs.InitializeResult().Instructions; got != "v1" {
		t.Fatalf("first session instructions = %q, want v1", got)
	}

	fx.write("Runefile", reloadHook("v2"))
	fx.waitReloads(1)
	if got := fx.connect().InitializeResult().Instructions; got != "v2" {
		t.Errorf("session after the reload: instructions = %q, want v2", got)
	}
	if got := fx.cs.InitializeResult().Instructions; got != "v1" {
		t.Errorf("the open session's instructions became %q", got)
	}

	fx.write("Runefile", reloadBase)
	fx.waitReloads(2)
	if got := fx.connect().InitializeResult().Instructions; got != "" {
		t.Errorf("after the hook was removed: instructions = %q, want none", got)
	}

	fx.write("Runefile", reloadHook("v3"))
	fx.waitReloads(3)
	if got := fx.connect().InitializeResult().Instructions; got != "v3" {
		t.Errorf("after the hook was added: instructions = %q, want v3", got)
	}
}

func TestReloaderContextFailure(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadHook("v1")}, func(rl *reloader) { rl.useHTTP = true })
	fx.write("Runefile", reloadBase+"\n# Lints.\nlint:\n    @echo linted\n\n# Health.\n[context]\nhealth:\n    @exit 1\n")
	got := fx.waitStderr("reloaded Runefile: 1 tool added", 1)
	const notice = `context hook "health" failed; proceeding without project context`
	if !strings.Contains(got, "warning: "+notice+"\n") {
		t.Errorf("stderr lacks the hook warning:\n%s", got)
	}
	if got := fx.connect().InitializeResult().Instructions; got != "("+notice+")" {
		t.Errorf("instructions = %q, want the failure notice", got)
	}
	fx.wantTools("build", "lint")
}

func TestReloaderContextStdio(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadHook("v1")}, nil)
	fx.write("Runefile", reloadHook("v2"))
	fx.waitReloads(1)
	runs, err := os.ReadFile(fx.path("context.runs"))
	if err != nil || string(runs) != "run\n" {
		t.Errorf("context.runs = %q, %v, want one run: a stdio reload must not rerun the hook", runs, err)
	}
	if got := fx.connect().InitializeResult().Instructions; got != "v1" {
		t.Errorf("instructions = %q, want the ones computed at start", got)
	}
}

func TestReloaderContextKeptOnFailure(t *testing.T) {
	fx := startReload(t, map[string]string{"Runefile": reloadHook("v1")}, func(rl *reloader) { rl.useHTTP = true })
	fx.write("Runefile", "build: (\n")
	fx.waitStderr("reload failed:", 1)
	if got := fx.connect().InitializeResult().Instructions; got != "v1" {
		t.Errorf("instructions after a failed reload = %q, want v1", got)
	}
}

// ServeMCP starts the reloader, and stops it before it returns.
func TestServeMCPReloads(t *testing.T) {
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	serve := func(noReload bool) (stderr *lockedBuffer, stop func()) {
		stderr = &lockedBuffer{}
		ctx, cancel := context.WithCancel(context.Background())
		opts := Options{Cwd: dir, Stderr: stderr, Ctx: ctx, MCPNoReload: noReload}
		token := tokenFile(t)
		done := make(chan error, 1)
		go func() { done <- ServeMCP(opts, true, "127.0.0.1:0", token) }()
		return stderr, func() {
			cancel()
			<-done
		}
	}
	waitFor := func(stderr *lockedBuffer, text string) {
		t.Helper()
		deadline := time.Now().Add(reloadWait)
		for !strings.Contains(stderr.String(), text) {
			if time.Now().After(deadline) {
				t.Fatalf("stderr does not hold %q:\n%s", text, stderr.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	stderr, stop := serve(false)
	waitFor(stderr, "rune MCP server on http://")
	if err := os.WriteFile(runefile, []byte(reloadBase+"\n# Lints.\nlint:\n    @echo linted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(stderr, "reloaded Runefile: 1 tool added, 0 removed, 0 changed")
	stop()

	stderr, stop = serve(true)
	waitFor(stderr, "rune MCP server on http://")
	if err := os.WriteFile(runefile, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	stop()
	if got := stderr.String(); strings.Contains(got, "reload") {
		t.Errorf("--no-reload: stderr = %q, want no reload line", got)
	}
}

// A save made while the [context] hook runs at start is loaded once the
// server is up.
func TestServeMCPKeepsChangeDuringContextHook(t *testing.T) {
	dir := t.TempDir()
	// The hook holds the start until the test has saved the Runefile.
	body := reloadBase + "\n# Health.\n[context]\nhealth:\n" +
		"    @touch hook.started; while [ ! -e hook.release ]; do sleep 0.05; done\n"
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	opts := Options{Cwd: dir, Stderr: stderr, Ctx: ctx}
	token := tokenFile(t)
	done := make(chan error, 1)
	go func() { done <- ServeMCP(opts, true, "127.0.0.1:0", token) }()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(reloadWait)
	for {
		if _, err := os.Stat(filepath.Join(dir, "hook.started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the [context] hook did not start:\n%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(runefile, []byte(body+"\n# Lints.\nlint:\n    @echo linted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	(&reloadFixture{t: t, stderr: stderr}).waitStderr("reloaded Runefile: 1 tool added, 0 removed, 0 changed", 1)
}

func tokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The endpoint an agent task opens lives for one task run and never reloads
// (spec 043 FR-033).
func TestAgentEndpointNotReloadable(t *testing.T) {
	if (&engine{}).agentMCPOptions(0).Reloadable {
		t.Error("the agent task endpoint is reloadable")
	}
}

// The watcher must never wait on the reloader: on Windows that stops the
// watcher's Add and Remove, which the reloader is calling at that moment.
// relay takes every event at once, keeps what fits, and says when it dropped
// one.
func TestRelayNeverBlocksTheWatcher(t *testing.T) {
	inEvents := make(chan fsnotify.Event)
	inErrs := make(chan error)
	events := make(chan fsnotify.Event, 2)
	errs := make(chan error, 1)
	missed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(inEvents, inErrs, events, errs, missed)
	}()

	// Nothing reads events or errs. Every send below must still go through.
	send := func(what string, deliver func() bool) {
		t.Helper()
		sent := make(chan bool, 1)
		go func() { sent <- deliver() }()
		select {
		case <-sent:
		case <-time.After(reloadWait):
			t.Fatalf("relay did not take %s while the reloader was not reading", what)
		}
	}
	for i := range 5 {
		name := fmt.Sprintf("f%d", i)
		send("event "+name, func() bool { inEvents <- fsnotify.Event{Name: name, Op: fsnotify.Write}; return true })
	}
	for _, msg := range []string{"first", "second"} {
		send("error "+msg, func() bool { inErrs <- errors.New(msg); return true })
	}

	if got := []string{(<-events).Name, (<-events).Name}; !slices.Equal(got, []string{"f0", "f1"}) {
		t.Errorf("kept events = %v, want the first two", got)
	}
	select {
	case <-missed:
	default:
		t.Error("relay dropped events without reporting them")
	}
	if err := <-errs; err.Error() != "first" {
		t.Errorf("kept error = %v, want the first", err)
	}

	close(inEvents)
	close(inErrs)
	select {
	case <-done:
	case <-time.After(reloadWait):
		t.Fatal("relay did not end when the watcher closed its channels")
	}
	if _, open := <-events; open {
		t.Error("relay left its event channel open")
	}
}

// Dropped events lead to a reload, like a change.
func TestReloaderMissedEvents(t *testing.T) {
	missed := make(chan struct{}, 1)
	var stderr lockedBuffer
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Cwd: dir, Stderr: &stderr, Version: "1.0.0"}
	adapter, _, err := newMCPAdapter(opts, runefile)
	if err != nil {
		t.Fatal(err)
	}
	rl := &reloader{
		opts: opts, runefile: runefile, srv: mcpserver.New(adapter, mcpserver.Options{Reloadable: true}),
		events: make(chan fsnotify.Event), errs: make(chan error), missed: missed,
		addDir: func(string) error { return nil }, removeDir: func(string) error { return nil },
		debounce: time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.run(ctx); close(done) }()
	missed <- struct{}{}
	deadline := time.Now().Add(reloadWait)
	for !strings.Contains(stderr.String(), "reloaded Runefile: 0 tools added, 0 removed, 0 changed") {
		if time.Now().After(deadline) {
			t.Fatalf("dropped events did not lead to a reload:\n%s", stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestPathRecorderChanged(t *testing.T) {
	dir := t.TempDir()
	present, absent := filepath.Join(dir, "present.rune"), filepath.Join(dir, "sub", "absent.rune")
	if err := os.WriteFile(present, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := func() *pathRecorder {
		rec := &pathRecorder{}
		rec.add(present)
		rec.add(absent)
		return rec
	}
	rec := record()
	if rec.changed() {
		t.Error("changed with no file touched")
	}
	if err := os.WriteFile(present, []byte("longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !rec.changed() {
		t.Error("a written file is not reported")
	}

	rec = record()
	if err := os.MkdirAll(filepath.Dir(absent), 0o755); err != nil {
		t.Fatal(err)
	}
	if rec.changed() {
		t.Error("changed when only a directory was made")
	}
	if err := os.WriteFile(absent, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !rec.changed() {
		t.Error("a created file is not reported")
	}

	rec = record()
	if err := os.Remove(present); err != nil {
		t.Fatal(err)
	}
	if !rec.changed() {
		t.Error("a removed file is not reported")
	}
}

// A load learns which files to watch by reading them, so a save can land
// after the read and before the watch, where no event reports it. The
// reloader notices and loads again. Here the save happens inside the call
// that sets up the watch, and no event is ever sent for it.
func TestReloaderChangeBeforeWatch(t *testing.T) {
	var stderr lockedBuffer
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	write := func(content string) {
		if err := os.WriteFile(runefile, []byte(content), 0o644); err != nil {
			t.Error(err)
		}
	}
	write(reloadBase)
	opts := Options{Cwd: dir, Stderr: &stderr, Version: "1.0.0"}
	adapter, _, err := newMCPAdapter(opts, runefile)
	if err != nil {
		t.Fatal(err)
	}
	lint := reloadBase + "\n# Lints.\nlint:\n    @echo linted\n"
	events := make(chan fsnotify.Event)
	saved := false
	rl := &reloader{
		opts: opts, runefile: runefile, srv: mcpserver.New(adapter, mcpserver.Options{Reloadable: true}),
		events: events, errs: make(chan error),
		files: map[string]bool{runefile: true},
		addDir: func(string) error {
			if !saved {
				saved = true
				write(lint + "\n# Tests.\ntest:\n    @echo tested\n")
			}
			return nil
		},
		removeDir: func(string) error { return nil },
		debounce:  time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	write(lint)
	events <- fsnotify.Event{Name: runefile, Op: fsnotify.Write}
	deadline := time.Now().Add(reloadWait)
	for strings.Count(stderr.String(), "reloaded Runefile: 1 tool added, 0 removed, 0 changed") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the save made before the watch was never loaded:\n%s", stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ServeMCP checks the same gap for its first load.
func TestReloaderRunStartsPending(t *testing.T) {
	var stderr lockedBuffer
	dir := t.TempDir()
	runefile := filepath.Join(dir, "Runefile")
	if err := os.WriteFile(runefile, []byte(reloadBase), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Cwd: dir, Stderr: &stderr, Version: "1.0.0"}
	adapter, _, err := newMCPAdapter(opts, runefile)
	if err != nil {
		t.Fatal(err)
	}
	rl := &reloader{
		opts: opts, runefile: runefile, srv: mcpserver.New(adapter, mcpserver.Options{Reloadable: true}),
		events: make(chan fsnotify.Event), errs: make(chan error),
		addDir: func(string) error { return nil }, removeDir: func(string) error { return nil },
		debounce: time.Millisecond,
		pending:  true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(reloadWait)
	for !strings.Contains(stderr.String(), "reloaded Runefile: 0 tools added, 0 removed, 0 changed") {
		if time.Now().After(deadline) {
			t.Fatalf("a pending reloader did not reload:\n%s", stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
