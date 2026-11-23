package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rune-task-runner/rune/internal/agentinit"
	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/mask"
	"github.com/rune-task-runner/rune/internal/observe"
)

// AgentObserveFlags are the flags of `rune agent observe`.
type AgentObserveFlags struct {
	Remove bool
	DryRun bool
	// write writes one file; nil is the real, atomic write. Tests replace it.
	write func(string, []byte) error
}

// observeScope is what insideProject says for `rune agent observe`.
const observeScope = "rune agent observe writes only inside the project"

// recordWait is how long `rune agent record` waits for its input. A hook in
// the background has no timeout, so a client that never closed the input
// would otherwise leave a process behind for every shell call.
const recordWait = 5 * time.Second

// observeRoot finds the project root (research R3): the directory of the
// Runefile found from start, or of the one file names, or start itself when
// there is none. The root comes back resolved, so that two names of one
// directory give one store.
func observeRoot(file, start string) (root, runefile string, err error) {
	runefile, err = config.Resolve(file, start)
	switch {
	case err == nil:
		root = filepath.Dir(runefile)
	case errors.Is(err, config.ErrNotFound) && file == "":
		root, runefile = start, ""
	default:
		return "", "", &UsageError{Err: err}
	}
	if root, err = filepath.Abs(root); err != nil {
		return "", "", &CommandFailure{Err: err}
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return root, runefile, nil
}

// observeBase is the directory the stores of every project live under.
func observeBase() (string, error) {
	state, err := audit.StateDir(os.Getenv, runtime.GOOS, os.UserHomeDir)
	if err != nil {
		return "", err
	}
	return observe.Base(state), nil
}

// AgentObserve turns the recording of an agent's shell commands on for the
// project, or off with --remove, in the person's own Claude Code settings
// (spec 058). It works without a Runefile and runs nothing.
func AgentObserve(opts Options, flags AgentObserveFlags, clientName string) error {
	if clientName != string(agentinit.ClientClaude) {
		return usagef("rune agent observe supports claude only, not %q", clientName)
	}
	root, _, err := observeRoot(opts.File, opts.Cwd)
	if err != nil {
		return err
	}
	files, err := readClientFiles(root, agentinit.ObservePaths(), observeScope)
	if err != nil {
		return err
	}
	res, err := agentinit.PlanObserve(agentinit.ObserveInputs{
		Files: files, ShownDir: shellWord(root, runtime.GOOS), Remove: flags.Remove,
	})
	if err != nil {
		return agentPlanError(opts, files, err)
	}
	if len(res.Conflicts) > 0 {
		renderConflicts(opts, files, res.Conflicts)
		return usagef("agent observe: %d conflict(s), nothing written", len(res.Conflicts))
	}
	if flags.DryRun {
		fmt.Fprint(opts.Stdout, agentinit.ObserveDiff(res))
	} else if err := writeClientFiles(root, []agentinit.File{res.File}, flags.write); err != nil {
		return err
	}
	fmt.Fprint(opts.Stderr, agentinit.ObserveReport(res, flags.DryRun))
	return nil
}

// AgentRecord records one hook event of Claude Code (spec 058). It is the
// command of the handlers `rune agent observe` installs. It never fails and
// prints nothing, whatever its input: an event that is not recorded is
// counted in the diagnostics, which `rune suggest` shows.
func AgentRecord(opts Options) error {
	recordEvent(opts, recordWait, time.Now())
	return nil
}

// recordEvent does the work of AgentRecord, waiting at most wait for the
// input.
func recordEvent(opts Options, wait time.Duration, now time.Time) {
	base, err := observeBase()
	if err != nil {
		// No state directory: there is nowhere to record or count.
		return
	}
	var store *observe.Store
	var root, runefile string
	var rootErr error
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); filepath.IsAbs(dir) {
		root, runefile, rootErr = observeRoot("", dir)
		if rootErr != nil {
			// Resolved as observeRoot resolves it, so the count lands in the
			// store rune suggest reads for this directory.
			root = filepath.Clean(dir)
			if real, err := filepath.EvalSymlinks(root); err == nil {
				root = real
			}
		}
		store = observe.OpenStore(base, root)
	}
	drop := func(reason string) {
		if store != nil {
			_ = store.CountDrop(reason)
			return
		}
		_ = observe.CountShared(base, reason, now)
	}

	in := opts.Stdin
	if in == nil {
		in = os.Stdin
	}
	ev, err := readEvent(in, wait)
	if err != nil {
		drop(dropReason(err))
		return
	}
	if !ev.Wanted() {
		return
	}
	if store == nil {
		drop(observe.ReasonNoProject)
		return
	}
	if rootErr != nil {
		drop(observe.ReasonRunefileUnreadable)
		return
	}
	secrets, err := recordSecrets(opts, runefile)
	if err != nil {
		drop(observe.ReasonRunefileUnreadable)
		return
	}
	rec, err := observe.NewRecord(ev, root, secrets, now)
	if err != nil {
		drop(dropReason(err))
		return
	}
	switch err := store.Append(rec); {
	case err == nil, errors.Is(err, observe.ErrInsideProject):
		// A store inside the project is refused, and so is a count there.
	case errors.Is(err, observe.ErrBusy):
		drop(observe.ReasonStoreBusy)
	default:
		drop(dropReason(err))
	}
}

// dropReason is the diagnostics reason of an error of the recorder.
func dropReason(err error) string {
	var drop *observe.DropError
	if errors.As(err, &drop) {
		return drop.Reason
	}
	return observe.ReasonStoreError
}

// readEvent decodes one event from in, giving up after wait. The goroutine
// that reads ends with the process when the wait runs out.
func readEvent(in io.Reader, wait time.Duration) (observe.Event, error) {
	type result struct {
		ev  observe.Event
		err error
	}
	done := make(chan result, 1)
	go func() {
		ev, err := observe.DecodeEvent(in)
		done <- result{ev, err}
	}()
	select {
	case r := <-done:
		return r.ev, r.err
	case <-time.After(wait):
		return observe.Event{}, &observe.DropError{Reason: observe.ReasonSlowInput}
	}
}

// recordSecrets is what the recorder knows of the project's secrets: the
// names and values Rune masks in task output. With a Runefile they come
// from it as a run loads it, with its declared and exempt names, its [env]
// values and its dotenv files; without one, from the environment alone.
func recordSecrets(opts Options, runefile string) (observe.Secrets, error) {
	if runefile == "" {
		return observe.Secrets{
			Names:  func(name string) bool { return mask.SecretName(name, nil, nil) },
			Values: mask.NewSet(os.Environ(), nil, nil),
		}, nil
	}
	quiet := opts
	quiet.Stdout, quiet.Stderr = io.Discard, io.Discard
	mod, err := loadModule(quiet, runefile, false)
	if err != nil {
		return observe.Secrets{}, err
	}
	declared, exempt := mod.settings.Secrets, mod.settings.Unmasked
	return observe.Secrets{
		Names:  func(name string) bool { return mask.SecretName(name, declared, exempt) },
		Values: deriveMaskSet(mod.baseEnv, mod.tasks, declared, exempt),
	}, nil
}
