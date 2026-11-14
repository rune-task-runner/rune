package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/style"
	"github.com/rune-task-runner/rune/mcpserver"
)

// reloadDebounce is how long the reloader waits after a change for more of
// them, so one save that arrives as several events is one reload. `rune
// --watch` waits the same time before it runs again.
const reloadDebounce = 100 * time.Millisecond

// pathRecorder collects the files a load reads or looks for, each once, as
// absolute cleaned paths in first-seen order.
type pathRecorder struct {
	paths []string
	seen  map[string]bool
}

func (r *pathRecorder) add(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if r.seen[abs] {
		return
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[abs] = true
	r.paths = append(r.paths, abs)
}

// wrap returns src recording every path it is asked for, found or not.
// Composition reads each import and module through the provider and probes
// module candidates through it, so the record is the Runefile's file graph
// without a second walk that could disagree with composition.
func (r *pathRecorder) wrap(src diag.SourceProvider) diag.SourceProvider {
	return func(path string) ([]byte, bool) {
		r.add(path)
		return src(path)
	}
}

// reloader keeps a running MCP server in step with its Runefile (spec 043).
// It watches the files the Runefile is made of and, when one changes, loads
// the Runefile again and replaces the server's tools. A load that fails
// changes nothing. One goroutine runs it, so its fields need no lock.
type reloader struct {
	opts     Options
	runefile string // fixed at start; a reload never searches again
	srv      *mcpserver.Server

	// The operator's flags, fixed for the life of the server.
	granted, allow, deny []string
	flagLimit            int
	// useHTTP says new sessions can start, so a reload reruns the [context]
	// hook for them. On stdio the one session already has its instructions.
	useHTTP bool

	good []string // files of the last load that succeeded
	// files are the watched files: good, plus a failed load's, plus the
	// directories of those that do not exist yet.
	files map[string]bool
	dirs  map[string]bool // directories given to the watcher

	events <-chan fsnotify.Event
	errs   <-chan error
	// missed signals that relay dropped events. One of them may have been
	// for a watched file.
	missed <-chan struct{}
	// addDir and removeDir are the watcher's Add and Remove.
	addDir, removeDir func(string) error
	debounce          time.Duration
	warnedWatch       bool
}

// configure gives a freshly loaded adapter the operator's grants and output
// limit, as at start.
func (r *reloader) configure(a *mcpAdapter) {
	a.grants = make(map[string]bool, len(r.granted))
	for _, name := range r.granted {
		a.grants[name] = true
	}
	a.outputLimit = r.flagLimit
}

// open starts watching paths and returns a function that stops watching.
// Changes made from then on are kept until start reads them, so
// the caller opens before it runs Runefile code and starts once it has a
// server: a save made in between is not lost. Where files cannot be watched,
// open says so and the server keeps the tools it has.
func (r *reloader) open(paths []string) (closeWatch func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		r.warnWatch(err)
		return func() {}
	}
	events := make(chan fsnotify.Event, relayBuffer)
	errs := make(chan error, relayBuffer)
	missed := make(chan struct{}, 1)
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		relay(w.Events, w.Errors, events, errs, missed)
	}()
	r.events, r.errs, r.missed = events, errs, missed
	r.addDir, r.removeDir = w.Add, w.Remove
	r.debounce = reloadDebounce
	r.good = paths
	r.watch(paths)
	return func() {
		// Closing the watcher closes its channels, which ends relay.
		_ = w.Close()
		<-relayed
	}
}

// relayBuffer is how many events and errors wait for the reloader before
// relay drops one. A save is a handful of events.
const relayBuffer = 64

// relay moves the watcher's events and errors into channels that never
// block it, until the watcher closes both. On Windows the watcher serves Add
// and Remove on the thread that delivers events. The reloader calls them
// while it is not reading, so an event delivered straight to it at that
// moment would stop both for good. A dropped event is reported on missed,
// which leads to a reload like any change.
func relay(inEvents <-chan fsnotify.Event, inErrs <-chan error, events chan<- fsnotify.Event, errs chan<- error, missed chan<- struct{}) {
	defer close(events)
	for inEvents != nil || inErrs != nil {
		select {
		case ev, ok := <-inEvents:
			if !ok {
				inEvents = nil
				continue
			}
			select {
			case events <- ev:
			default:
				select {
				case missed <- struct{}{}:
				default:
				}
			}
		case err, ok := <-inErrs:
			if !ok {
				inErrs = nil
				continue
			}
			select {
			case errs <- err:
			default:
			}
		}
	}
}

// start runs the reloader on what open watches, until the returned stop is
// called or ctx ends. stop returns after the reloader's goroutine has exited.
// Without a watcher there is nothing to run.
func (r *reloader) start(ctx context.Context) (stop func()) {
	if r.events == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// run reloads after each burst of changes to a watched file, until ctx ends
// or the watcher closes.
func (r *reloader) run(ctx context.Context) {
	var due <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-r.events:
			if !ok {
				return
			}
			if r.relevant(ev) {
				due = time.After(r.debounce)
			}
		case <-r.missed:
			due = time.After(r.debounce)
		case <-due:
			due = nil
			r.reload(ctx)
		case err, ok := <-r.errs:
			if !ok {
				return
			}
			// The watcher dropped events and keeps working. One of them may
			// have been for a watched file, so load again.
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				due = time.After(r.debounce)
				continue
			}
			r.warnWatch(err)
		}
	}
}

// relevant reports whether an event changes the content of a watched file.
// Directories are watched, so most events are for files that are not ours.
func (r *reloader) relevant(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	return r.files[filepath.Clean(ev.Name)]
}

// watch makes paths the watched files. It watches their directories, not
// the files: an editor that saves by renaming a new file into place would
// otherwise leave the watch on the old one, and a file that does not exist
// yet can only be seen arriving in its directory. A directory that does not
// exist yet is waited for the same way, in the one above it.
func (r *reloader) watch(paths []string) {
	r.files = make(map[string]bool, len(paths))
	needed := map[string]bool{}
	for _, p := range paths {
		r.files[p] = true
		needed[filepath.Dir(p)] = true
		// A write through a symlink is an event in the directory of the file
		// it leads to, not in the symlink's.
		if target, ok := symlinkTarget(p); ok {
			r.files[target] = true
			needed[filepath.Dir(target)] = true
		}
	}
	if r.dirs == nil {
		r.dirs = map[string]bool{}
	}
	// Added before any is removed: a directory waited for in its parent must
	// not lose that watch in between.
	wanted := make(map[string]bool, len(needed))
	for dir := range needed {
		wanted[r.watchDir(dir)] = true
	}
	for dir := range r.dirs {
		if !wanted[dir] {
			_ = r.removeDir(dir)
			delete(r.dirs, dir)
		}
	}
}

// watchDir watches dir, or the nearest directory above it that exists when
// dir does not, and returns the one it settled on. The missing directories on
// the way count as watched files: one being made is then an event, and the
// reload it starts watches it in turn. Without that, a module named before
// its directory is made, or a directory removed and made again, would never
// reload.
func (r *reloader) watchDir(dir string) string {
	for {
		// Added again each time: a directory that was removed and made
		// again has lost its watch.
		err := r.addDir(dir)
		if err == nil {
			r.dirs[dir] = true
			return dir
		}
		if info, statErr := os.Stat(dir); statErr == nil {
			// A directory that is there and still cannot be watched has hit
			// the system's watch limit or a permission. Its files would stop
			// reloading with nothing said.
			if info.IsDir() {
				r.warnWatch(err)
			}
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		r.files[dir] = true
		dir = parent
	}
}

// symlinkTarget returns the file that a symlink at path leads to.
func symlinkTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := filepath.EvalSymlinks(path)
	return target, err == nil
}

// reload loads the Runefile and, when it is valid, makes it what the server
// serves. Nothing is replaced and no task code runs unless the whole load
// succeeded (FR-021, FR-023).
func (r *reloader) reload(ctx context.Context) {
	stderr, th := r.opts.Stderr, r.opts.themeStderr()
	adapter, paths, err := newMCPAdapter(r.opts, r.runefile)
	if err != nil {
		fmt.Fprintln(stderr, th.Muted.Render(fmt.Sprintf("reload failed: %v; keeping the previous tools", err)))
		// The failed load's files join the watch, so fixing or creating the
		// one that broke it starts the next reload.
		r.watch(append(append([]string{}, r.good...), paths...))
		return
	}
	r.configure(adapter)
	// At start an unknown name is a usage error. Here it is a warning: the
	// author renaming a task must not take every tool away (FR-013).
	warnUnknownNames(stderr, th, "--allow-destructive", r.granted, adapter.tasks)
	warnUnknownNames(stderr, th, "--allow", r.allow, adapter.tasks)
	warnUnknownNames(stderr, th, "--deny", r.deny, adapter.tasks)
	warnNonDestructive(stderr, th, r.granted, adapter.tasks)
	warnNeverExposed(stderr, th, "--allow", r.allow, adapter.tasks)
	warnNeverExposed(stderr, th, "--deny", r.deny, adapter.tasks)

	change := r.srv.Replace(adapter, adapter.limit())
	r.good = paths
	r.watch(paths)
	// After the tools, so the hook cannot delay the notification, and before
	// the line below, so the line means new sessions get the new text.
	if r.useHTTP {
		r.srv.SetInstructions(adapter.gatherContext(ctx, stderr))
	}
	fmt.Fprintln(stderr, th.Muted.Render(reloadLine(change)))
}

// reloadLine is the unstyled line a successful reload prints.
func reloadLine(c mcpserver.Change) string {
	noun := "tools"
	if c.Added == 1 {
		noun = "tool"
	}
	return fmt.Sprintf("reloaded Runefile: %d %s added, %d removed, %d changed", c.Added, noun, c.Removed, c.Changed)
}

// warnUnknownNames prints one warning per name given to flag that no task of
// the reloaded Runefile has. The name takes effect again if a later reload
// brings the task back.
func warnUnknownNames(w io.Writer, theme style.Theme, flag string, names []string, tasks map[string]*ast.Task) {
	for _, name := range names {
		if _, ok := tasks[name]; !ok {
			fmt.Fprintf(w, "%s: %s %s has no effect: the Runefile has no task %q\n",
				theme.Warning.Render("warning"), flag, name, name)
		}
	}
}

// warnWatch says, once, that the tools will not follow the Runefile.
func (r *reloader) warnWatch(err error) {
	if r.warnedWatch {
		return
	}
	r.warnedWatch = true
	fmt.Fprintf(r.opts.Stderr, "%s: cannot watch the Runefile for changes: %v; tools will not reload\n",
		r.opts.themeStderr().Warning.Render("warning"), err)
}
