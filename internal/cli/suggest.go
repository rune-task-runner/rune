package cli

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/agentinit"
	"github.com/rune-task-runner/rune/internal/analysis"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/observe"
	"github.com/rune-task-runner/rune/internal/tasksuggest"
)

// SuggestFlags are the flags of `rune suggest` and `rune suggest apply`.
type SuggestFlags struct {
	MinRuns int
	DryRun  bool
	// beforeWrite runs between the last read of the Runefile and the write.
	// Tests use it to change the file in between.
	beforeWrite func()
}

// suggestTaskNote is printed when the Runefile has a task named suggest.
const suggestTaskNote = `note: "suggest" is also a task of this Runefile; run it with: rune -- suggest`

func (f SuggestFlags) check() error {
	if f.MinRuns < 1 || f.MinRuns > 1000 {
		return usagef("--min-runs must be a whole number from 1 to 1000, not %d", f.MinRuns)
	}
	return nil
}

// Suggest lists what the agent repeated and the tasks Rune would add for it
// (spec 058). It reads the records and the Runefile, and runs nothing.
func Suggest(opts Options, flags SuggestFlags) error {
	if err := flags.check(); err != nil {
		return err
	}
	root, runefile, err := observeRoot(opts.File, opts.Cwd)
	if err != nil {
		return err
	}
	tasks, defaultDir, err := suggestTasks(opts, runefile)
	if err != nil {
		return err
	}
	if runefile != "" && hasTask(runefile, "suggest") {
		fmt.Fprintln(opts.Stderr, suggestTaskNote)
	}
	rep, err := suggestReport(root, tasks, tasksuggest.Options{MinRuns: flags.MinRuns, DefaultDir: defaultDir})
	if err != nil {
		return err
	}
	fmt.Fprint(opts.Stdout, rep.Listing(root, flags.MinRuns))
	return nil
}

// SuggestApply adds the tasks of the suggestions with the given IDs to the
// Runefile, or creates it (research R13). Nothing is written unless every ID
// is known and the new Runefile passes analysis, and nothing is written over
// a Runefile that changed since it was read.
func SuggestApply(opts Options, flags SuggestFlags, ids []string) error {
	if len(ids) == 0 {
		return usagef("rune suggest apply needs the ID of a suggestion; list them with: rune suggest")
	}
	if err := flags.check(); err != nil {
		return err
	}
	root, runefile, err := observeRoot(opts.File, opts.Cwd)
	if err != nil {
		return err
	}
	target, existed := runefile, runefile != ""
	if !existed {
		target = filepath.Join(root, "Runefile")
	}
	shown := showPath(opts.Cwd, target)
	if info, err := os.Lstat(target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		dest, _ := filepath.EvalSymlinks(target)
		return usagef("%s is a symbolic link to %s; edit that file, or apply in its directory", shown, filepath.ToSlash(dest))
	}
	old, err := readIfExists(target)
	if err != nil {
		return &CommandFailure{Err: err}
	}
	tasks, defaultDir, err := suggestTasks(opts, runefile)
	if err != nil {
		var invalid *ValidationError
		if errors.As(err, &invalid) {
			return &ValidationError{Err: errorf("%v; fix the Runefile first", invalid.Err)}
		}
		return err
	}
	rep, err := suggestReport(root, tasks, tasksuggest.Options{MinRuns: flags.MinRuns, DefaultDir: defaultDir})
	if err != nil {
		return err
	}
	var picked []*tasksuggest.Suggestion
	for _, id := range ids {
		s := rep.Find(id)
		if s == nil {
			return usagef("no suggestion has the ID %s; list them with: rune suggest (and the same --min-runs)", id)
		}
		if !slices.Contains(picked, s) {
			picked = append(picked, s)
		}
	}
	text := tasksuggest.Append(old, picked)
	if err := checkSuggested(opts, target, text); err != nil {
		return err
	}
	names := make([]string, len(picked))
	for i, s := range picked {
		names[i] = s.Name
	}
	status := "updated"
	if !existed {
		status = "created"
	}
	if flags.DryRun {
		fmt.Fprint(opts.Stdout, agentinit.UnifiedDiff(shown, old, text, existed))
		fmt.Fprintf(opts.Stderr, "%s: would be %s, adding %s\n", shown, status, strings.Join(names, ", "))
		return nil
	}
	if flags.beforeWrite != nil {
		flags.beforeWrite()
	}
	now, err := readIfExists(target)
	if err != nil {
		return &CommandFailure{Err: err}
	}
	if (now != nil) != existed || !bytes.Equal(now, old) {
		return usagef("%s changed while the tasks were being added; nothing written. Run the command again", shown)
	}
	if existed {
		err = writeFileAtomic(target, text)
	} else {
		err = createFile(target, text)
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return usagef("%s changed while the tasks were being added; nothing written. Run the command again", shown)
		}
		return &CommandFailure{Err: fmt.Errorf("%s: %w", shown, err)}
	}
	fmt.Fprintf(opts.Stderr, "%s: %s, added %s\n", shown, status, strings.Join(names, ", "))
	fmt.Fprintln(opts.Stderr, "note: these tasks have no attributes, so an agent can call them through rune mcp without\n  approval. Add [confirm] to a task that changes state.")
	return nil
}

// SuggestForget deletes the project's records and diagnostics.
func SuggestForget(opts Options) error {
	root, _, err := observeRoot(opts.File, opts.Cwd)
	if err != nil {
		return err
	}
	base, err := observeBase()
	if err != nil {
		return &CommandFailure{Err: err}
	}
	n, err := observe.OpenStore(base, root).Forget()
	if err != nil {
		return &CommandFailure{Err: err}
	}
	if n == 0 {
		fmt.Fprintf(opts.Stderr, "nothing recorded for %s\n", root)
		return nil
	}
	records := "records"
	if n == 1 {
		records = "record"
	}
	fmt.Fprintf(opts.Stderr, "deleted %d %s for %s\n", n, records, root)
	return nil
}

// suggestReport builds the report of the project's records.
func suggestReport(root string, tasks []tasksuggest.TaskInfo, opts tasksuggest.Options) (*tasksuggest.Report, error) {
	base, err := observeBase()
	if err != nil {
		return nil, &CommandFailure{Err: err}
	}
	store := observe.OpenStore(base, root)
	// Records past their time go now, as the next event would take them.
	_ = store.Prune()
	recs, stats, err := store.Read()
	if err != nil {
		return nil, &CommandFailure{Err: err}
	}
	rep := tasksuggest.Build(recs, tasks, opts)
	rep.Damaged = stats.Damaged
	var all observe.Diagnostics
	for _, dir := range []string{store.Dir(), base} {
		// The counts are a hint: one that cannot be read is left out.
		d, _ := observe.ReadDiagnostics(dir)
		all = all.Merge(d)
	}
	rep.Dropped = make(map[string]int64, len(all.Reasons))
	for reason, c := range all.Reasons {
		rep.Dropped[reason] = c.N
	}
	return rep, nil
}

// suggestTasks reads the tasks of the Runefile that matching needs, and the
// directory a task without [working-directory] runs in, relative to the
// root with forward slashes ("" for the root). A Runefile with errors is
// reported as `rune analyze` reports it.
func suggestTasks(opts Options, runefile string) (tasks []tasksuggest.TaskInfo, defaultDir string, err error) {
	if runefile == "" {
		return nil, "", nil
	}
	file, src, err := loadStatic(opts, runefile, opts.IgnoreVersion, &pathRecorder{})
	if err != nil {
		return nil, "", err
	}
	// set working-directory moves every task that names no directory, so
	// it is read as a run reads it.
	scope := eval.NewScope(indexAssignments(file), map[string]string{})
	scope.GOOS, scope.Arch = runtime.GOOS, runtime.GOARCH
	settings, sdiags := config.ResolveSettings(file, eval.New(scope))
	if sdiags.HasErrors() {
		renderDiags(opts, sdiags, src)
		return nil, "", &ValidationError{Err: errorf("invalid settings")}
	}
	root := filepath.Dir(runefile)
	defaultDir = "."
	if rel, err := filepath.Rel(root, resolveWorkDir(runefile, settings.WorkingDir)); err == nil {
		defaultDir = path.Clean(filepath.ToSlash(rel))
	}
	if defaultDir == "." {
		defaultDir = ""
	}
	byName := indexTasks(file)
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		tasks = append(tasks, taskInfo(byName[name], root, defaultDir))
	}
	return tasks, defaultDir, nil
}

// taskInfo is what matching needs of one task: its name, its directory and
// its only body line when it has exactly one in Rune's shell. defaultDir is
// the directory of a task without [working-directory], as suggestTasks
// gives it.
func taskInfo(t *ast.Task, root, defaultDir string) tasksuggest.TaskInfo {
	info := tasksuggest.TaskInfo{Name: t.Name, Dir: defaultDir}
	if t.Attr(ast.AttrNoCD) != nil || t.Attr(ast.AttrScript) != nil || (t.Executor != "" && t.Executor != ast.ExecSh) || len(t.Body) != 1 {
		return info
	}
	if wd := t.Attr(ast.AttrWorkingDirectory); wd != nil && wd.Str != "" {
		dir := filepath.Clean(wd.Str)
		if filepath.IsAbs(dir) {
			rel, err := filepath.Rel(root, dir)
			if err != nil {
				return info
			}
			dir = rel
		}
		info.Dir = path.Clean(filepath.ToSlash(dir))
		if info.Dir == "." {
			info.Dir = ""
		}
	}
	if info.Dir == ".." || strings.HasPrefix(info.Dir, "../") {
		return info
	}
	info.Body = strings.TrimSpace(t.Body[0].Raw)
	return info
}

// checkSuggested analyzes the new text of the Runefile before it is
// written. The Runefile had no error, so an error here is in the added
// tasks: a defect of rune suggest, and nothing is written.
func checkSuggested(opts Options, target string, text []byte) error {
	svc := analysis.NewService(analysis.DiskSourceStore{})
	snap, err := svc.Analyze(opts.ctx(), analysis.AnalyzeRequest{URI: target, Content: string(text)})
	if err != nil {
		return &CommandFailure{Err: err}
	}
	if !snap.Diagnostics.HasErrors() {
		return nil
	}
	renderDiags(opts, errorDiags(snap.Diagnostics), snap.Sources)
	return &CommandFailure{Err: errorf("the added tasks are not valid; this is a bug in rune suggest")}
}

// readIfExists reads a file, or returns nil for one that does not exist.
func readIfExists(name string) ([]byte, error) {
	// ReadFile returns a non-nil slice for a file that exists, also an
	// empty one, so nil means the file is not there.
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// createFile writes a new file without ever replacing one: the text goes to
// a temporary file that is then linked to the name, which fails with
// fs.ErrExist when a file appeared there meanwhile.
func createFile(name string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(name), ".rune-tmp-"+rand.Text())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Link(tmp, name)
}
