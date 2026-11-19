package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/agentinit"
	"github.com/rune-task-runner/rune/internal/analysis"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// `rune agent init <client>` writes the project files that set an agent
// client up to work through Rune (spec 055): the registration of `rune mcp`,
// the settings that remove the client's own command-running tools, and the
// rules that let Rune's tools run. internal/agentinit plans the files from
// bytes to bytes; this file finds the project, reads and writes the files,
// asks the client for its version and maps the result to an exit status.
//
// It runs nothing of the Runefile, and it writes only inside the project.

// agentTaskNote is printed when the Runefile has a task named agent, which
// the command's name hides on the command line.
const agentTaskNote = `note: "agent" is also a task of this Runefile; run it with: rune -- agent`

// clientVersionLimit is how long a client may take to print its version.
const clientVersionLimit = 5 * time.Second

// AgentInitFlags are the flags of `rune agent init`, as the command line
// gave them.
type AgentInitFlags struct {
	DryRun   bool // --dry-run
	Check    bool // --check
	Force    bool // --force
	Unpinned bool // --unpinned

	// The three below are nil or zero in the command and set by tests.
	// version looks the client's version up, versionLimit is how long that
	// may take, and write writes one file.
	version      func(ctx context.Context, binary string) string
	versionLimit time.Duration
	write        func(path string, data []byte) error
}

// AgentInit sets the project of the Runefile up for the client named. The
// report goes to standard error; standard output carries only the
// difference of a dry run.
func AgentInit(opts Options, flags AgentInitFlags, clientName string) error {
	client, err := agentinit.ParseClient(clientName)
	if err != nil {
		return &UsageError{Err: err}
	}
	if flags.Check && (flags.Force || flags.DryRun) {
		return usagef("--check cannot be combined with --force or --dry-run")
	}
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return &UsageError{Err: err}
	}
	// A client file is text in UTF-8, and a path that is not cannot be
	// written into one as it is.
	if !flags.Unpinned && !utf8.ValidString(runefile) {
		return usagef("the path of the Runefile is not valid UTF-8, so it cannot be written into a client file; register the server without it: --unpinned")
	}
	// A server registered without a path looks for the Runefile by name.
	if name := filepath.Base(runefile); flags.Unpinned && !config.IsRunefileName(name) {
		return usagef("--unpinned needs a Runefile that rune finds by its name; %s is not one", name)
	}
	// A server registered for a Runefile with errors would not start.
	if err := analyzeRunefile(opts, runefile); err != nil {
		return err
	}
	if hasTask(runefile, "agent") {
		fmt.Fprintln(opts.Stderr, agentTaskNote)
	}

	dir := filepath.Dir(runefile)
	files, err := readClientFiles(dir, agentinit.Paths(client))
	if err != nil {
		return err
	}
	in := agentinit.Inputs{
		Runefile:      runefile,
		ShownRunefile: shellWord(runefile, runtime.GOOS),
		ShownDir:      shellWord(dir, runtime.GOOS),
		Unpinned:      flags.Unpinned,
		Force:         flags.Force,
		Files:         files,
	}
	res, err := agentinit.Plan(client, in)
	if err != nil {
		return agentPlanError(opts, files, err)
	}

	if flags.Check {
		// A value at a key Rune owns is listed as different. What is left is
		// a conflict a run could not get past either.
		renderConflicts(opts, files, slices.DeleteFunc(slices.Clone(res.Conflicts), func(c agentinit.Conflict) bool { return c.Forceable }))
		text, complete := agentinit.CheckReport(res)
		fmt.Fprint(opts.Stderr, text)
		if !complete {
			return &CommandFailure{Err: errorf("agent init: the setup is not complete")}
		}
		return nil
	}
	// No outcome is named for a setup that was not written.
	if len(res.Conflicts) > 0 {
		renderConflicts(opts, files, res.Conflicts)
		return usagef("agent init: %d conflict(s), nothing written", len(res.Conflicts))
	}
	// The client is asked for its version only now: a check reads the files
	// and nothing else, and a run that stopped above starts no program.
	res.SetVersion(flags.clientVersion(opts.ctx(), agentinit.Binary(client)))
	if flags.DryRun {
		fmt.Fprint(opts.Stdout, agentinit.Diff(res))
	} else if err := writeClientFiles(dir, res.Files, flags.write); err != nil {
		return err
	}
	fmt.Fprint(opts.Stderr, agentinit.Report(res, flags.DryRun))
	if res.Outcome.Kind == agentinit.RegisteredOnly {
		return &CommandFailure{Err: errorf(`agent init: the outcome is "Registered only"`)}
	}
	return nil
}

// AgentTaskNote prints the note about a task named agent when the Runefile
// found from the working directory has one. `rune agent` alone prints its
// help, and a person who meant the task learns how to run it.
func AgentTaskNote(opts Options) {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err == nil && hasTask(runefile, "agent") {
		fmt.Fprintln(opts.Stderr, agentTaskNote)
	}
}

// analyzeRunefile checks the Runefile as `rune analyze` does and prints its
// errors. Nothing of it runs.
func analyzeRunefile(opts Options, runefile string) error {
	svc := analysis.NewService(analysis.DiskSourceStore{})
	snap, err := svc.Analyze(opts.ctx(), analysis.AnalyzeRequest{URI: runefile})
	if err != nil {
		return &UsageError{Err: err}
	}
	if !snap.Diagnostics.HasErrors() {
		return nil
	}
	errs := errorDiags(snap.Diagnostics)
	renderDiags(opts, errs, snap.Sources)
	return &ValidationError{Err: errorf("%d error(s)", len(errs))}
}

// agentPlanError prints why a plan could not be made and maps it to an exit
// status: a client file that is not valid is a static error like one of a
// Runefile, a file that needs a setting added by hand is the person's to
// change, and anything else is a failure of the command.
func agentPlanError(opts Options, files map[string][]byte, err error) error {
	var bad *agentinit.ParseError
	if errors.As(err, &bad) {
		renderConflicts(opts, files, []agentinit.Conflict{{File: bad.File, Line: bad.Line, Col: bad.Col, Width: 1, Msg: bad.Msg}})
		return &ValidationError{Err: errorf("%s is not valid", bad.File)}
	}
	var manual *agentinit.ManualError
	if errors.As(err, &manual) {
		renderConflicts(opts, files, []agentinit.Conflict{{File: manual.File, Line: manual.Line, Col: manual.Col, Width: 1, Msg: manual.Message()}})
		return usagef("agent init: %s needs a setting added by hand, nothing written", manual.File)
	}
	return &CommandFailure{Err: err}
}

// renderConflicts prints each conflict as a Runefile error is printed: the
// message, file:line:col, the line and a caret under the value found.
func renderConflicts(opts Options, files map[string][]byte, conflicts []agentinit.Conflict) {
	if len(conflicts) == 0 {
		return
	}
	list := make(diag.List, len(conflicts))
	for i, c := range conflicts {
		list[i] = diag.New(token.Span{
			File:  c.File,
			Start: token.Position{Line: c.Line, Col: c.Col},
			End:   token.Position{Line: c.Line, Col: c.Col + max(1, c.Width)},
		}, c.Msg)
	}
	renderDiags(opts, list, func(name string) ([]byte, bool) {
		data, ok := files[name]
		return data, ok
	})
}

// readClientFiles reads the client files that exist, by their path relative
// to the project directory.
func readClientFiles(dir string, paths []string) (map[string][]byte, error) {
	files := make(map[string][]byte, len(paths))
	for _, rel := range paths {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := insideProject(dir, target); err != nil {
			return nil, &UsageError{Err: err}
		}
		data, err := os.ReadFile(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, &CommandFailure{Err: err}
		}
		files[rel] = data
	}
	return files, nil
}

// insideProject refuses a target that a symbolic link takes out of the
// project directory. `.claude` linked to the settings directory of the user
// is a real setup, and writing through it would change settings that are
// not the project's.
func insideProject(dir, target string) error {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	// The nearest part of the path that exists is what a write would go
	// through. The project directory exists, so the walk ends there at the
	// latest.
	existing := target
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		existing = filepath.Dir(existing)
	}
	shown := showPath(dir, existing)
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("%s: %w", shown, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s leads outside the project, to %s. rune agent init writes only inside the project", shown, filepath.ToSlash(resolved))
	}
	return nil
}

// writeClientFiles writes the files a plan changed, in its order. When one
// cannot be written, the files this run already wrote are put back as they
// were, so a failure never leaves half a setup. write is the function that
// writes one file; nil is the real one.
func writeClientFiles(dir string, files []agentinit.File, write func(string, []byte) error) error {
	if write == nil {
		write = writeFileAtomic
	}
	type written struct {
		target string
		file   agentinit.File
		made   []string // the directories this run created for it
	}
	var done []written
	removeDirs := func(dirs []string) {
		for _, d := range slices.Backward(dirs) {
			_ = os.Remove(d)
		}
	}
	for _, f := range files {
		if f.Status() == "unchanged" {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		// A link inside the project is written through, not replaced.
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			target = resolved
		}
		made, err := makeDirs(filepath.Dir(target))
		if err == nil {
			err = write(target, f.New)
		}
		if err != nil {
			removeDirs(made)
			for _, w := range slices.Backward(done) {
				if w.file.Existed {
					_ = writeFileAtomic(w.target, w.file.Old)
				} else {
					_ = os.Remove(w.target)
				}
				removeDirs(w.made)
			}
			return &CommandFailure{Err: fmt.Errorf("%s: %w", f.Path, err)}
		}
		done = append(done, written{target: target, file: f, made: made})
	}
	return nil
}

// makeDirs creates dir and its missing parents, and returns the ones it
// set out to create, outermost first.
func makeDirs(dir string) ([]string, error) {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || d == filepath.Dir(d) {
			break
		}
		missing = append(missing, d)
	}
	slices.Reverse(missing)
	return missing, os.MkdirAll(dir, 0o755)
}

// writeFileAtomic writes data to a temporary file beside path and renames
// it over path, so that a failed write leaves the file as it was. A file
// that exists keeps its permissions. A new file gets the ones any new file
// gets: 0644 less the umask of the process.
func writeFileAtomic(path string, data []byte) error {
	name := filepath.Join(filepath.Dir(path), ".rune-agent-init-"+rand.Text())
	tmp, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if info, statErr := os.Stat(path); err == nil && statErr == nil {
		err = os.Chmod(name, info.Mode().Perm())
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// clientVersion is the version the client reports, or "" when it reports
// none.
func (f AgentInitFlags) clientVersion(ctx context.Context, binary string) string {
	if f.version != nil {
		return f.version(ctx, binary)
	}
	limit := f.versionLimit
	if limit == 0 {
		limit = clientVersionLimit
	}
	return runClientVersion(ctx, binary, limit)
}

// runClientVersion runs `<binary> --version` and reads the version from its
// output. A program that is not found, fails, prints no version or takes
// longer than limit gives "": the setup is then reported as not checked,
// never as failed.
func runClientVersion(ctx context.Context, binary string, limit time.Duration) string {
	path, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// A child that keeps the output open after the program was stopped does
	// not hold the command for long.
	cmd.WaitDelay = min(limit, time.Second)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return agentinit.ParseVersion(string(out))
}
