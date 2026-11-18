package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/sandbox"
)

// Every body an agent's call runs is confined by the operating system, or
// the call is refused (spec 053). This file holds what a server knows about
// its sandbox: the operator's mode, whether the backend works, the texts the
// operator and the agent read, and the checks a call passes before anything
// of it runs.

const (
	sandboxFlag = "--sandbox"
	sandboxVar  = "RUNE_SANDBOX"

	// probeRetry is how often an unavailable backend is asked again, so that
	// one installed while the server runs is used without a restart.
	probeRetry = time.Second
	// staleTmp is the age at which a body's temporary directory is taken to
	// be left over from a server that was killed.
	staleTmp = 24 * time.Hour

	sandboxOffRemedy = "Turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
)

// homeCredentials are the entries of the home directory a confined body
// sees empty: stores of credentials and the sockets that go with them.
var homeCredentials = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker",
	".config/gcloud", ".config/gh",
	".netrc", ".git-credentials", ".npmrc", ".pypirc", ".password-store",
}

// replacedDirs are the directories the sandbox replaces whole. A program
// reached through the server's PATH under one of them is not there inside.
var replacedDirs = []string{"/run", "/tmp", "/var/tmp"}

// sandboxModeOf reads the operator's mode: the flag when it was given, else
// the variable, else enforce. An empty variable counts as absent, and an
// empty flag is a mistake to report.
func sandboxModeOf(flag *string, env string) (sandbox.Mode, error) {
	if flag != nil {
		mode, err := sandbox.ParseMode(*flag)
		if err != nil {
			return "", usagef("%s: %v", sandboxFlag, err)
		}
		return mode, nil
	}
	if env == "" {
		return sandbox.ModeEnforce, nil
	}
	mode, err := sandbox.ParseMode(env)
	if err != nil {
		return "", usagef("%s: %v", sandboxVar, err)
	}
	return mode, nil
}

// sandboxState is the sandbox of one server. Every adapter the server loads
// shares it, so a reload changes nothing about it.
type sandboxState struct {
	mode    sandbox.Mode
	backend sandbox.Backend
	// exe is this binary, which runs again as the worker of each body.
	exe       string
	workspace string // the resolved directory of the Runefile
	// runefile is the served Runefile. searchedFrom is the directory the
	// search for it started in when that is not its own: the next start
	// would search again, and a task could put a nearer Runefile there.
	runefile, searchedFrom string
	base                   string // where the private directories of the project are
	auditBase              string // hidden from every body
	home                   string
	getenv                 func(string) string
	now                    func() time.Time
	// evalSymlinks is filepath.EvalSymlinks, replaced in tests.
	evalSymlinks func(string) (string, error)
	// replaced are the directories the sandbox replaces whole. Tests, whose
	// fixtures live in a temporary directory, set their own.
	replaced []string

	mu      sync.Mutex
	probed  bool
	checked time.Time
	err     error // why the backend cannot confine; nil when it can
}

// newSandboxState builds the state. It creates nothing and asks nothing: the
// first ready does.
func newSandboxState(mode sandbox.Mode, backend sandbox.Backend, exe, workspace, base, auditBase, home string, getenv func(string) string, now func() time.Time) *sandboxState {
	return &sandboxState{
		mode: mode, backend: backend, exe: exe, workspace: workspace, base: base,
		auditBase: auditBase, home: home, getenv: getenv, now: now,
		evalSymlinks: filepath.EvalSymlinks,
		replaced:     replacedDirs,
	}
}

// hostSandboxState is the state of a server on this host for the Runefile in
// root. A cache directory that cannot be found is reported by the first call
// that needs it.
func hostSandboxState(opts Options, mode sandbox.Mode, root, runefile string) *sandboxState {
	exe, err := os.Executable()
	if err == nil {
		exe = sandbox.Resolve(exe)
	}
	base, _ := sandboxBase(runefile)
	if base != "" {
		// The cache directory is mounted at its own path, and bubblewrap
		// cannot mount onto a path that goes through an absolute link.
		base = sandbox.Resolve(base)
	}
	logs, _ := auditBase(opts)
	home, _ := os.UserHomeDir()
	backend := sandbox.Detect(runtime.GOOS, []string{exe, SandboxProbeArg})
	state := newSandboxState(mode, backend, exe, sandbox.Resolve(root), base, logs, home, os.Getenv, time.Now)
	state.runefile = runefile
	if opts.File == "" && opts.Cwd != "" {
		if from := sandbox.Resolve(opts.Cwd); from != state.workspace {
			state.searchedFrom = from
		}
	}
	return state
}

// sandboxBase is the directory of a project's private directories, under the
// user's cache directory and named as its audit log is.
func sandboxBase(runefile string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rune", "sandbox", audit.ProjectID(runefile)), nil
}

// enforcing reports whether bodies must be confined. A nil state, as an
// adapter built by a test has, confines nothing.
func (s *sandboxState) enforcing() bool {
	return s != nil && s.mode == sandbox.ModeEnforce
}

// unavailableError is why the sandbox cannot confine on this host.
type unavailableError struct {
	reason error
}

func (e *unavailableError) Error() string { return e.reason.Error() }
func (e *unavailableError) Unwrap() error { return e.reason }

// remedy is what the operator can do about it.
func (e *unavailableError) remedy() string {
	switch {
	case errors.Is(e.reason, sandbox.ErrNotFound):
		return "Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
	case errors.Is(e.reason, sandbox.ErrCannotStart):
		return "Allow unprivileged user namespaces for bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
	}
	return sandboxOffRemedy
}

// ready reports whether the backend can confine a body now: nil, or an
// *unavailableError. The answer is kept. A failure is asked again after
// probeRetry, and a success until a body's sandbox fails to start.
func (s *sandboxState) ready(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.probed && (s.err == nil || s.now().Sub(s.checked) < probeRetry) {
		return s.err
	}
	s.err = nil
	// The answer is kept for every call, so it must not be the answer to
	// one call's cancellation: a probe killed with its caller would read as
	// a host that cannot confine. The probe has a time limit of its own.
	if reason := s.check(context.WithoutCancel(ctx)); reason != nil {
		s.err = &unavailableError{reason: reason}
	}
	s.probed, s.checked = true, s.now()
	return s.err
}

// failed forgets a good answer: a body's sandbox did not start.
func (s *sandboxState) failed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probed = false
}

// check is one look at the host. Before it starts a sandbox it rules out
// what a sandbox that starts would still get wrong: a backend or a Rune
// binary a task could replace, and a PATH that points into a directory the
// sandbox empties.
func (s *sandboxState) check(ctx context.Context) error {
	if path := s.backend.Path(); path != "" {
		if s.taskWritable(path) {
			return s.replaceable(path)
		}
		if s.taskWritable(s.exe) {
			return fmt.Errorf("the Rune binary (%s) is inside the project, where a task could replace it", s.exe)
		}
		for _, dir := range filepath.SplitList(s.getenv("PATH")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			if slices.ContainsFunc(s.replaced, func(hidden string) bool { return sandbox.Within(hidden, filepath.Clean(dir)) }) {
				return fmt.Errorf("the PATH holds %s, which the sandbox hides", dir)
			}
		}
	}
	return s.backend.Probe(ctx)
}

// taskWritable reports whether path lies where a confined body can write.
func (s *sandboxState) taskWritable(path string) bool {
	if path == "" {
		return false
	}
	return sandbox.Within(s.workspace, path) || (s.base != "" && sandbox.Within(sandbox.Resolve(s.base), path))
}

// state is what the last look found, for the texts below.
func (s *sandboxState) state() (unavailable error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// startupPart is the sandbox's part of a server's startup line.
func (s *sandboxState) startupPart() string {
	switch {
	case s.mode == sandbox.ModeOff:
		return "sandbox: off, tasks run unsandboxed"
	case s.state() != nil:
		return "sandbox: unavailable, calls are refused"
	}
	return "sandbox: " + s.backend.Name()
}

// startupWarning is what the operator is told when bodies are not confined,
// or "" when they are. The caller writes the word "warning" before it.
func (s *sandboxState) startupWarning() string {
	if s.mode == sandbox.ModeOff {
		return "sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can."
	}
	var unavailable *unavailableError
	if errors.As(s.state(), &unavailable) {
		return fmt.Sprintf("sandbox: %v. Every task call will be refused. %s", unavailable, unavailable.remedy())
	}
	return ""
}

// refusal is the error of a call the sandbox preflight stopped. It does not
// start with the word "sandbox": the audit log prints the kind before it.
func (s *sandboxState) refusal(task string, err error) string {
	var unavailable *unavailableError
	if errors.As(err, &unavailable) {
		return fmt.Sprintf("task %q was not run: the sandbox is unavailable: %v. %s", task, unavailable, unavailable.remedy())
	}
	return fmt.Sprintf("task %q was not run: %v", task, err)
}

// record is the sandbox part of a call's audit record. A nil state records
// what a server did before spec 053: nothing was enforced.
func (s *sandboxState) record() *audit.Sandbox {
	switch {
	case s == nil:
		return &audit.Sandbox{}
	case s.mode == sandbox.ModeOff:
		return &audit.Sandbox{Mode: string(sandbox.ModeOff)}
	case s.state() != nil:
		return &audit.Sandbox{Mode: string(sandbox.ModeEnforce)}
	}
	return &audit.Sandbox{Enforced: true, Backend: s.backend.Name(), Mode: string(sandbox.ModeEnforce)}
}

// auditRecord asks the backend and returns what a call that starts now is
// recorded with. Asking here is what lets a call find a backend that was
// installed since the last failure, and the preflight a moment later reads
// the same answer.
func (s *sandboxState) auditRecord(ctx context.Context) *audit.Sandbox {
	if s.enforcing() {
		_ = s.ready(ctx)
	}
	return s.record()
}

// privateDirError reports a private directory that cannot be used.
type privateDirError struct {
	dir    string
	reason string
}

func (e *privateDirError) Error() string {
	return fmt.Sprintf("the sandbox cannot use its private directory %s: %s", e.dir, e.reason)
}

// emptyFile is an empty file under the private base that stands in for each
// hidden file, or "" when the base cannot hold one. It is made on first use.
func (s *sandboxState) emptyFile() string {
	if s.base == "" || sandbox.Within(s.workspace, sandbox.Resolve(s.base)) {
		return ""
	}
	path := filepath.Join(s.base, "empty")
	usable := func() bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode().IsRegular() && info.Size() == 0
	}
	if usable() {
		return path
	}
	if err := os.MkdirAll(s.base, 0o700); err != nil {
		return ""
	}
	// Two bodies can come here at once. The one that is second cannot write
	// the read-only file the first made, and finds it as it should be.
	if err := os.WriteFile(path, nil, 0o400); err != nil && !usable() {
		return ""
	}
	return path
}

// cacheDir is the project's private cache directory.
func (s *sandboxState) cacheDir() string { return filepath.Join(s.base, "cache") }

// checkBase makes the private base and its cache directory. It is the part
// of privateDirs a call does once, before anything of it runs.
func (s *sandboxState) checkBase() error {
	if s.base == "" {
		return &privateDirError{dir: "(none)", reason: "this user has no cache directory; set XDG_CACHE_HOME"}
	}
	if sandbox.Within(s.workspace, sandbox.Resolve(s.base)) {
		return &privateDirError{dir: s.base, reason: "it is inside the project; set XDG_CACHE_HOME to a directory outside it"}
	}
	for _, dir := range []string{s.cacheDir(), filepath.Join(s.base, "tmp")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return &privateDirError{dir: dir, reason: reasonOf(err)}
		}
	}
	return nil
}

// reasonOf is the cause of a path error without the path, which the caller
// has already named.
func reasonOf(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// privateDirs gives one body its temporary directory and the project's
// cache directory. cleanup removes the temporary one.
func (s *sandboxState) privateDirs() (tmp, cache string, cleanup func(), err error) {
	cleanup = func() {}
	if err := s.checkBase(); err != nil {
		return "", "", cleanup, err
	}
	parent := filepath.Join(s.base, "tmp")
	s.pruneTmp(parent)
	tmp, err = os.MkdirTemp(parent, "")
	if err != nil {
		return "", "", cleanup, &privateDirError{dir: parent, reason: reasonOf(err)}
	}
	// The lock says the directory is in use for as long as this process
	// holds it, and no longer: it ends with the process, however that ends.
	release, err := holdTmp(tmp + tmpLockSuffix)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", cleanup, &privateDirError{dir: tmp, reason: reasonOf(err)}
	}
	return tmp, s.cacheDir(), func() {
		_ = os.RemoveAll(tmp)
		release()
	}, nil
}

// tmpLockSuffix names the lock file beside a body's temporary directory.
const tmpLockSuffix = ".lock"

// pruneTmp removes what a server that is gone left behind. A directory whose
// lock is held belongs to a body that runs, however long it has run: its age
// says nothing, because a directory's time changes only when an entry is
// added to it or removed. A lock nobody holds is a server that ended without
// cleaning up. A process id would not do in its place: ids are reused, and
// two servers that share the directory may not share their ids. A directory
// with no lock is judged by age.
func (s *sandboxState) pruneTmp(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if dir, isLock := strings.CutSuffix(path, tmpLockSuffix); isLock {
			// A lock whose directory is gone and that nobody holds.
			if _, err := os.Lstat(dir); err != nil && !tmpHeld(path) {
				_ = os.Remove(path)
			}
			continue
		}
		lock := path + tmpLockSuffix
		if _, err := os.Lstat(lock); err == nil {
			if !tmpHeld(lock) {
				_ = os.RemoveAll(path)
				_ = os.Remove(lock)
			}
			continue
		}
		info, err := entry.Info()
		if err == nil && s.now().Sub(info.ModTime()) > staleTmp {
			_ = os.RemoveAll(path)
		}
	}
}

// hidden lists the locations a body sees empty, as the directories and the
// files that exist now. What lies under a directory the sandbox replaces
// whole is hidden already.
func (s *sandboxState) hidden() (dirs, files []string) {
	candidates := []string{s.auditBase, s.getenv("SSH_AUTH_SOCK")}
	if socket, ok := strings.CutPrefix(s.getenv("DOCKER_HOST"), "unix://"); ok {
		candidates = append(candidates, socket)
	}
	if s.home != "" {
		for _, entry := range homeCredentials {
			candidates = append(candidates, filepath.Join(s.home, filepath.FromSlash(entry)))
		}
	}
	for _, candidate := range candidates {
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		// A mount follows a link, so the target is what is hidden.
		path := sandbox.Resolve(candidate)
		if slices.ContainsFunc(s.replaced, func(dir string) bool { return sandbox.Within(dir, path) }) {
			continue
		}
		info, err := os.Lstat(path)
		switch {
		case err != nil:
		case info.Mode()&os.ModeSymlink != 0:
			// A link that is still one leads nowhere: there is nothing to
			// hide, and bubblewrap cannot mount onto it.
		case info.IsDir():
			dirs = append(dirs, path)
		default:
			files = append(files, path)
		}
	}
	slices.Sort(dirs)
	slices.Sort(files)
	return slices.Compact(dirs), slices.Compact(files)
}

// command is the backend's command for argv under spec. The backend finds
// its program on the PATH again for every command, and the look that ready
// keeps saw only the one of that moment: a program a task has put into a
// PATH directory of the project since then must not run.
func (s *sandboxState) command(ctx context.Context, spec sandbox.Spec, argv []string) (*exec.Cmd, error) {
	cmd, err := s.backend.Command(ctx, spec, argv)
	if err != nil {
		return nil, err
	}
	if s.taskWritable(cmd.Path) {
		return nil, s.replaceable(cmd.Path)
	}
	return cmd, nil
}

// replaceable is why a backend program at path is not used.
func (s *sandboxState) replaceable(path string) error {
	return fmt.Errorf("%s (%s) is inside the project, where a task could replace it", s.backend.Name(), path)
}

// gitRunner builds the commands Rune itself runs git with for one look at a
// repository. Where bodies are confined, git is too: `git status` runs the
// programs a repository's configuration names, and a task may have written
// that configuration. The command sees the repository read-only and has no
// network. A look is a few git commands in one directory, and the rules they
// run under are worked out once for all of them.
func (s *sandboxState) gitRunner() gitCommandFunc {
	if !s.enforcing() {
		return plainGit
	}
	// look is what the commands of one directory share. Git is pointed at
	// the resolved directory, as a body is: the path as the load wrote it
	// may go through a link that lies where the sandbox hides everything.
	type look struct {
		dir  string
		spec sandbox.Spec
	}
	var (
		looks = map[string]look{}
		mu    sync.Mutex
	)
	lookAt := func(dir string) look {
		mu.Lock()
		defer mu.Unlock()
		if seen, ok := looks[dir]; ok {
			return seen
		}
		resolved := sandbox.Resolve(dir)
		dirs, files := s.hidden()
		made := look{dir: resolved, spec: sandbox.Spec{
			Workspace:   sandbox.RepoRoot(resolved),
			ReadOnly:    true,
			HiddenDirs:  dirs,
			HiddenFiles: files,
			Empty:       s.emptyFile(),
			VarRunDir:   sandbox.VarRunDir(),
		}}
		looks[dir] = made
		return made
	}
	return func(ctx context.Context, dir string, args ...string) *exec.Cmd {
		if err := s.ready(ctx); err != nil {
			// The call is about to be refused. Nothing runs outside a
			// sandbox for it, git included.
			return exec.CommandContext(ctx, "")
		}
		at := lookAt(dir)
		cmd, err := s.command(ctx, at.spec, append([]string{"git", "-C", at.dir}, args...))
		if err != nil {
			return exec.CommandContext(ctx, "")
		}
		return cmd
	}
}

// preflightError is a call the sandbox cannot confine or protect.
type preflightError struct{ text string }

func (e *preflightError) Error() string { return e.text }

// protectedPaths lists what a body may read and not change: the Runefile
// and the files this load read, the dotenv file, and the two directories
// that hold the repository's and Rune's own state. policy holds the first
// group, whose files decide what an agent may do and are checked for a
// second name.
func (a *mcpAdapter) protectedPaths(workspace string) (all, policy []string) {
	candidates := slices.Clone(a.loaded)
	if a.settings.Dotenv != "" {
		dotenv := a.settings.Dotenv
		if !filepath.IsAbs(dotenv) {
			dotenv = filepath.Join(a.root, dotenv)
		}
		candidates = append(candidates, dotenv)
	}
	for _, candidate := range candidates {
		if path := a.inWorkspace(workspace, candidate); path != "" {
			policy = append(policy, path)
		}
	}
	slices.Sort(policy)
	policy = slices.Compact(policy)
	all = append(slices.Clone(policy), filepath.Join(workspace, ".git"), filepath.Join(workspace, runeDir))
	slices.Sort(all)
	return slices.Compact(all), policy
}

// runeDir is Rune's own directory in a project: the task cache and the
// stored plans.
const runeDir = ".rune"

// inWorkspace places a path of the load under the resolved workspace, or
// returns "" when it lies outside. The load wrote its paths as it was
// started, perhaps through a link to the project. A path under the load's
// root keeps its relative part, so that a link inside the workspace is
// still there to be seen. Any other path is resolved whole.
func (a *mcpAdapter) inWorkspace(workspace, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.root, path)
	}
	if root, err := filepath.Abs(a.root); err == nil && sandbox.Within(root, path) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return ""
		}
		return filepath.Join(workspace, rel)
	}
	if resolved := sandbox.Resolve(path); sandbox.Within(workspace, resolved) {
		return resolved
	}
	return ""
}

// sandboxPreflight is what a call passes before anything of it runs, in
// mode enforce. It returns what the call's engine confines its bodies with.
// Every refusal is one of the texts of the contract.
func (a *mcpAdapter) sandboxPreflight(ctx context.Context, eng *engine, t *ast.Task) (*bodySandbox, error) {
	s := a.sandbox
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if err := s.checkBase(); err != nil {
		return nil, err
	}
	workspace := s.workspace
	// A project that is itself one of the locations a body may not see has
	// nothing left to work in.
	if dirs, _ := s.hidden(); slices.Contains(dirs, workspace) {
		return nil, &preflightError{text: fmt.Sprintf("the sandbox cannot confine it: the project (%s) is a location the sandbox hides from tasks", workspace)}
	}
	if s.searchedFrom != "" {
		return nil, &preflightError{text: fmt.Sprintf("the sandbox cannot protect the choice of the Runefile: rune found %s by searching up from %s, where a task could create a nearer one. Start rune in %s, or name the Runefile with --file.", s.runefile, s.searchedFrom, workspace)}
	}
	for _, task := range a.reachTasks(t) {
		if dir := sandbox.Resolve(eng.taskDir(task)); !sandbox.Within(workspace, dir) {
			return nil, &preflightError{text: fmt.Sprintf("the sandbox cannot confine it: the working directory of task %q (%s) is outside the project (%s)", task.Name, dir, workspace)}
		}
	}
	for _, looked := range a.missing {
		path := a.inWorkspace(workspace, looked)
		if path == "" {
			continue
		}
		if _, err := os.Lstat(path); err != nil {
			return nil, &preflightError{text: fmt.Sprintf("the sandbox cannot protect %s: the Runefile looks for it and it does not exist, so a task could create it. Create the file, or change the Runefile so that it is not looked for (give a module its path, or remove the optional import).", path)}
		}
	}
	// A task must not be the one to create Rune's directory: the stored
	// plans live in it.
	if err := os.MkdirAll(filepath.Join(workspace, runeDir), 0o755); err != nil {
		return nil, &preflightError{text: fmt.Sprintf("the sandbox cannot protect %s: %s", filepath.Join(workspace, runeDir), reasonOf(err))}
	}
	all, policy := a.protectedPaths(workspace)
	var protected []string
	for _, path := range all {
		if err := sandbox.CheckProtected(workspace, path, slices.Contains(policy, path)); err != nil {
			return nil, &preflightError{text: "the sandbox cannot protect " + err.Error()}
		}
		if _, err := os.Lstat(path); err == nil {
			protected = append(protected, path)
		}
	}
	return &bodySandbox{state: s, workspace: workspace, protected: protected}, nil
}
