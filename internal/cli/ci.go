package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/cache"
	"github.com/rune-task-runner/rune/internal/ci"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/semver"
)

// `rune ci github` writes a GitHub Actions workflow from the Runefile, and
// `rune ci run` is what each of its jobs runs (spec 059). internal/ci builds
// the graph and the text; this file finds the Runefile, binds tasks through
// the engine, writes and checks the file, and runs one node.

// ciTaskNote is printed when the Runefile has a task named ci, which the
// command's name hides on the command line.
const ciTaskNote = `note: "ci" is also a task of this Runefile; run it with: rune -- ci`

// ciDefaultBranch is the branch whose pushes run the workflow by default.
const ciDefaultBranch = "main"

// CIGithubFlags are the flags of `rune ci github`.
type CIGithubFlags struct {
	OS          []string // --os, repeatable
	RuneVersion string   // --rune-version
	Branch      string   // --branch
	Write       bool     // --write
	Output      string   // --output
	Check       bool     // --check

	// write writes one file. Nil in the command; tests set it.
	write func(path string, data []byte) error
}

// CIRunFlags are the flags of `rune ci run`.
type CIRunFlags struct {
	Dir string // --dir: the job's state directory for bundles
	Key bool   // --key: print the node's cache key and run nothing
}

// CITaskNote prints the note about a task named ci, when the Runefile found
// from the working directory has one.
func CITaskNote(opts Options) {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err == nil && hasTask(runefile, "ci") {
		fmt.Fprintln(opts.Stderr, ciTaskNote)
	}
}

// CIGithub prints, writes or checks the workflow of the root tasks args
// names. It runs no task and reads nothing of the machine: no environment,
// no dotenv file, no setting.
func CIGithub(opts Options, flags CIGithubFlags, args []string) error {
	runefile, resolveErr := config.Resolve(opts.File, opts.Cwd)
	if resolveErr == nil && hasTask(runefile, "ci") {
		fmt.Fprintln(opts.Stderr, ciTaskNote)
	}
	if flags.Check && flags.Write {
		return usagef("--check cannot be combined with --write: a check writes nothing")
	}
	if len(args) == 0 {
		return usagef("rune ci github needs the tasks CI runs: rune ci github TASK...")
	}
	oses, names, err := ciOSes(flags.OS)
	if err != nil {
		return err
	}
	branch := flags.Branch
	if branch == "" {
		branch = ciDefaultBranch
	}
	if err := ci.CheckText(branch); err != nil {
		return usagef("--branch: %v", err)
	}
	if resolveErr != nil {
		return &UsageError{Err: resolveErr}
	}
	// Each job runs `rune ci run` without -f in the Runefile's directory, so
	// it must find this file there by itself.
	if !foundInItsDir(runefile) {
		return usagef("the jobs find the Runefile in its directory by name, and would not find %s; name it Runefile", showPath(opts.Cwd, runefile))
	}

	file, src, err := loadStatic(opts, runefile, false, &pathRecorder{})
	if err != nil {
		return err
	}
	tasks, assigns := indexTasks(file), indexAssignments(file)
	overrides, raw, err := splitArgs(args, tasks, opts.Commands)
	if err != nil {
		return err
	}
	if len(overrides) > 0 {
		return usagef("rune ci github does not take variable overrides: a job could not repeat %s", firstOverride(overrides))
	}
	version, err := ciVersion(flags.RuneVersion, opts.Version, file)
	if err != nil {
		return err
	}

	graphs := make([]*ci.Graph, 0, len(oses))
	jobs := 0
	for _, goos := range oses {
		g, err := ciGraph(opts, src, tasks, assigns, goos, raw)
		if err != nil {
			return err
		}
		graphs = append(graphs, g)
		jobs += len(g.Nodes)
	}

	project := filepath.Dir(runefile)
	target := filepath.Join(project, ".github", "workflows", "rune.yml")
	targetOpt := "--write"
	if flags.Output != "" {
		target = absIn(opts.Cwd, flags.Output)
		rel, err := filepath.Rel(project, target)
		if err != nil {
			rel = target
		}
		// The path goes into the header comment, where a line break would
		// start a line of YAML.
		if err := ci.CheckText(rel); err != nil {
			return usagef("--output: %v", err)
		}
		targetOpt = "--output " + ci.ShellWord(filepath.ToSlash(rel))
	}
	workDir, err := ciWorkDir(opts, project, target)
	if err != nil {
		return err
	}
	data := ci.Render(ci.Options{
		Version: version,
		Branch:  branch,
		WorkDir: workDir,
		Command: ciCommand("", names, branch, version, targetOpt, raw),
	}, graphs)
	summary := fmt.Sprintf("%s for %s on %s", plural(jobs, "job"), plural(len(graphs[0].Roots), "root task"), strings.Join(names, ", "))

	if !flags.Write && !flags.Check && flags.Output == "" {
		if _, err := opts.Stdout.Write(data); err != nil {
			return &CommandFailure{Err: err}
		}
		fmt.Fprintln(opts.Stderr, summary)
		return nil
	}

	shown := showPath(opts.Cwd, target)
	existing, readErr := os.ReadFile(target)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return &CommandFailure{Err: fmt.Errorf("%s: %w", shown, readErr)}
	}
	state := ci.PlanFile(existing, readErr == nil, data)
	if state == ci.StateNotGenerated {
		return usagef("ci github: %s was not generated by rune ci github, so it is not replaced; nothing written", shown)
	}
	if flags.Check {
		fmt.Fprintf(opts.Stderr, "%s: %s\n", shown, state)
		if state == ci.StateCurrent {
			return nil
		}
		typedOpt := targetOpt
		if flags.Output != "" {
			typedOpt = "--output " + shellWord(flags.Output, runtime.GOOS)
		}
		fileOpt := ""
		if opts.File != "" {
			fileOpt = "-f " + shellWord(opts.File, runtime.GOOS)
		}
		fmt.Fprintln(opts.Stderr, "regenerate with: "+ciCommand(fileOpt, names, branch, version, typedOpt, raw))
		return &CommandFailure{Err: fmt.Errorf("ci github: %s is not current", shown)}
	}
	if state == ci.StateCurrent {
		fmt.Fprintf(opts.Stderr, "%s: unchanged: %s\n", shown, summary)
		return nil
	}
	planned := []plannedFile{{target: target, shown: shown, old: existing, new: data, existed: readErr == nil}}
	if err := writePlanned(planned, flags.write); err != nil {
		return &CommandFailure{Err: fmt.Errorf("%s: %w", shown, err)}
	}
	status := "updated"
	if state == ci.StateMissing {
		status = "created"
	}
	fmt.Fprintf(opts.Stderr, "%s: %s: %s\n", shown, status, summary)
	return nil
}

// foundInItsDir reports whether discovery from the Runefile's directory
// finds the Runefile itself.
func foundInItsDir(runefile string) bool {
	found, err := config.Discover(filepath.Dir(runefile))
	if err != nil {
		return false
	}
	a, errA := os.Stat(found)
	b, errB := os.Stat(runefile)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// ciOSes reads --os: the GOOS of each runner, and the names as --os takes
// them, in the order given, without repeats. The default is linux.
func ciOSes(values []string) (gooses, names []string, err error) {
	if len(values) == 0 {
		values = []string{"linux"}
	}
	for _, v := range values {
		var goos string
		switch v {
		case "linux":
			goos = "linux"
		case "macos":
			goos = "darwin"
		case "windows":
			return nil, nil, usagef("--os: windows runners are not supported; use linux or macos")
		default:
			return nil, nil, usagef("--os: unknown OS %q; use linux or macos", v)
		}
		if !slices.Contains(gooses, goos) {
			gooses = append(gooses, goos)
			names = append(names, v)
		}
	}
	return gooses, names, nil
}

// pseudoVersion matches the date and commit of a Go pseudo-version, which a
// build from source reports.
var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// ciVersion is the release the jobs install, without a leading v: the one
// --rune-version names, else this rune's own when it is a release. It must
// satisfy the Runefile's minimum_version.
func ciVersion(flag, own string, file *ast.File) (string, error) {
	v := strings.TrimPrefix(flag, "v")
	if flag == "" {
		v = strings.TrimPrefix(own, "v")
	}
	parsed, err := semver.Parse(v)
	release := err == nil && len(parsed.Build) == 0 && !pseudoVersion.MatchString(v)
	switch {
	case flag == "" && !release:
		return "", usagef("this rune is a development build; name the release the jobs install with --rune-version")
	case !release:
		return "", usagef("--rune-version: %q is not a release version such as 0.11.0", flag)
	}
	if req, diags := config.MinimumVersion(file); !diags.HasErrors() && req.Present && !parsed.Satisfies(req.Version) {
		return "", usagef("--rune-version %s does not satisfy minimum_version %s of the Runefile", v, req.Raw)
	}
	return v, nil
}

// firstOverride names one override, the first by name, as it was typed.
func firstOverride(overrides map[string]string) string {
	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	slices.Sort(names)
	return names[0] + "=" + overrides[names[0]]
}

// ciGraph builds the graph of one OS. The engine that resolves the calls
// has the OS and the Runefile's variables, and nothing of this machine.
func ciGraph(opts Options, src diag.SourceProvider, tasks map[string]*ast.Task, assigns map[string]*ast.Assignment, goos string, raw []rawInvocation) (*ci.Graph, error) {
	scope := eval.NewScope(assigns, map[string]string{})
	scope.GOOS = goos
	scope.Arch = runtime.GOARCH
	eng := &engine{
		tasks:   tasks,
		assigns: assigns,
		scope:   scope,
		goos:    goos,
		opts:    opts,
		theme:   opts.themeStderr(),
		ctx:     opts.ctx(),
		src:     src,
	}
	roots := make([]ci.Root, len(raw))
	for i, r := range raw {
		roots[i] = ci.Root{Task: tasks[r.name], Args: r.args}
	}
	g, err := ci.Build(ci.Input{OS: goos, Roots: roots, Resolver: ciResolver{eng}, Tasks: tasks, Assigns: assigns})
	var refusal *ci.Refusal
	if errors.As(err, &refusal) {
		renderDiags(opts, refusal.Diags, src)
		return nil, &ValidationError{Err: errorf("%s why the tasks cannot run as a GitHub Actions workflow", plural(len(refusal.Diags), "reason"))}
	}
	if err != nil {
		return nil, eng.classifyRunErr(err)
	}
	return g, nil
}

// ciResolver binds tasks for ci.Build through the engine, as a run would.
type ciResolver struct{ e *engine }

func (r ciResolver) BindRoot(t *ast.Task, args []string) (eval.Args, error) {
	if !t.AvailableOn(r.e.goos) {
		d := diag.New(osAttrSpan(t), availabilityErr(t.Name, t, r.e.goos).Error())
		return eval.Args{}, &ci.Refusal{Diags: diag.List{d}}
	}
	return bindParams(t, args, r.e.scope)
}

func (r ciResolver) ResolveDep(cur *ast.Task, args eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error) {
	return r.e.ResolveDep(cur, args, dep)
}

func (r ciResolver) Available(t *ast.Task) bool { return r.e.Available(t) }

// ciWorkDir is the Runefile's directory relative to the repository root:
// the directory that holds the target's .github/workflows, or the
// Runefile's own directory when the target is elsewhere.
func ciWorkDir(opts Options, project, target string) (string, error) {
	workflows := filepath.Dir(target)
	if filepath.Base(workflows) != "workflows" || filepath.Base(filepath.Dir(workflows)) != ".github" {
		return ".", nil
	}
	repo := filepath.Dir(filepath.Dir(workflows))
	rel, err := filepath.Rel(repo, project)
	if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return "", usagef("the Runefile is not inside the repository of %s, so its jobs could not find it", showPath(opts.Cwd, target))
	}
	return filepath.ToSlash(rel), nil
}

// ciCommand is the command that writes the workflow again: the options that
// change its text, the target, and the roots with their arguments.
func ciCommand(fileOpt string, names []string, branch, version, targetOpt string, raw []rawInvocation) string {
	parts := []string{"rune ci github"}
	if fileOpt != "" {
		parts = append(parts, fileOpt)
	}
	if !slices.Equal(names, []string{"linux"}) {
		for _, n := range names {
			parts = append(parts, "--os", n)
		}
	}
	if branch != ciDefaultBranch {
		parts = append(parts, "--branch", ci.ShellWord(branch))
	}
	parts = append(parts, "--rune-version", version, targetOpt)
	// Without --, a root argument such as -race would be read as a flag.
	if slices.ContainsFunc(raw, func(r rawInvocation) bool {
		return slices.ContainsFunc(r.args, func(a string) bool { return strings.HasPrefix(a, "-") })
	}) {
		parts = append(parts, "--")
	}
	for _, r := range raw {
		parts = append(parts, ci.ShellWord(r.name))
		for _, a := range r.args {
			parts = append(parts, ci.ShellWord(a))
		}
	}
	return strings.Join(parts, " ")
}

// CIRun runs the body of the one task args names, as `rune TASK ARG...`
// runs it, without its dependencies or post-hooks: the jobs before this one
// ran those. With a state directory it first unpacks the bundles of earlier
// jobs, and after a successful [cache] body it writes the node's own.
func CIRun(opts Options, flags CIRunFlags, args []string) error {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return &UsageError{Err: err}
	}
	if len(args) == 0 {
		return usagef("rune ci run needs one task: rune ci run TASK [ARG...]")
	}
	root := filepath.Dir(runefile)
	dir := ""
	if flags.Dir != "" {
		dir = absIn(opts.Cwd, flags.Dir)
		if err := ci.UnpackDir(root, dir, !flags.Key); err != nil {
			return &CommandFailure{Err: fmt.Errorf("ci run: %w", err)}
		}
	}
	stdout, version := opts.Stdout, opts.Version
	opts.nodeRun = func(e *engine, invs []scheduler.Invocation) error {
		inv := invs[0]
		if flags.Key {
			return ciKey(e, inv, stdout, version)
		}
		if err := scheduler.RunOnly(e, inv); err != nil {
			return err
		}
		if dir == "" || inv.Task.Attr(ast.AttrCache) == nil {
			return nil
		}
		return ciPack(e, inv, dir)
	}
	return execute(opts, runefile, args)
}

// ciKey prints the cache key of a [cache] node and runs nothing.
func ciKey(e *engine, inv scheduler.Invocation, out io.Writer, version string) error {
	attr := inv.Task.Attr(ast.AttrCache)
	if attr == nil {
		return usagef("--key needs a task with [cache]; %s has none", inv.Task.Name)
	}
	d, err := cache.Decide(e.cacheSpec(inv.Task, inv.Params, attr))
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("ci run: the cache fingerprint of %s: %w", inv.Task.Name, err)}
	}
	key := ci.Key(ci.Slug(inv.Task.Name, ci.Words(inv.Task, inv.Params)), ci.KeyInput{
		Fingerprint: d.Hash,
		Node:        inv.Task.Name + "(" + inv.Params.Key() + ")",
		Version:     version,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		Image:       os.Getenv("ImageOS"),
	})
	_, err = fmt.Fprintf(out, "key=%s\n", key)
	return err
}

// ciPack writes the bundle of a [cache] node that succeeded: its outputs
// and its cache record, replacing dir/bundle.tar in one step.
func ciPack(e *engine, inv scheduler.Invocation, dir string) error {
	spec := e.cacheSpec(inv.Task, inv.Params, inv.Task.Attr(ast.AttrCache))
	for _, o := range spec.Outputs {
		if err := ci.CheckOutput(o); err != nil {
			return &CommandFailure{Err: fmt.Errorf("ci run: %s: %w", inv.Task.Name, err)}
		}
	}
	files, err := cache.OutputFiles(e.root, spec.Outputs)
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("ci run: %w", err)}
	}
	if _, err := os.Stat(cache.RecordPath(spec)); err == nil {
		rel, err := filepath.Rel(e.root, cache.RecordPath(spec))
		if err != nil {
			return &CommandFailure{Err: err}
		}
		files = append(files, filepath.ToSlash(rel))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &CommandFailure{Err: fmt.Errorf("ci run: %w", err)}
	}
	if err := writeBundle(e.root, files, filepath.Join(dir, ci.BundleFile)); err != nil {
		return &CommandFailure{Err: fmt.Errorf("ci run: %s: %w", inv.Task.Name, err)}
	}
	return nil
}

// writeBundle packs files into a temporary file next to path and renames it
// over path, so a reader never sees half a bundle.
func writeBundle(root string, files []string, path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bundle-*")
	if err != nil {
		return err
	}
	err = ci.Pack(root, files, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
