package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/audit"
	"github.com/rune-task-runner/rune/internal/cache"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/receipt"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

// receiptClaimVars are the variables a receipt records as claims when they
// are set. They say where a run may have happened; none of them proves it.
var receiptClaimVars = []string{
	"CI", "GITHUB_ACTIONS", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_WORKFLOW", "GITHUB_REPOSITORY",
}

// receiptKeyDir is where the local signing key lives: receipts under the
// user's state directory, beside the audit logs.
func receiptKeyDir(getenv func(string) string, goos string, home func() (string, error)) (string, error) {
	dir, err := audit.StateDir(getenv, goos, home)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "receipts"), nil
}

// errNoStateDir is the failure to find where the local key lives.
var errNoStateDir = errors.New("no local key directory")

// signingKey returns the key receipts are signed with and where it comes
// from: RUNE_RECEIPT_KEY when it was set, else the local key. With create,
// a missing local key is created and created says so; without it, a missing
// key is a nil key and no error.
func signingKey(opts Options, create bool) (key ed25519.PrivateKey, source string, created bool, err error) {
	if opts.ReceiptKey != "" {
		key, err := receipt.ParsePrivateKey(opts.ReceiptKey)
		if err != nil {
			return nil, "", false, fmt.Errorf("RUNE_RECEIPT_KEY: %w", err)
		}
		return key, "RUNE_RECEIPT_KEY", false, nil
	}
	dir, err := receiptKeyDir(os.Getenv, runtime.GOOS, os.UserHomeDir)
	if err != nil {
		return nil, "", false, fmt.Errorf("receipts: %w: %w", errNoStateDir, err)
	}
	source = "local: " + filepath.Join(dir, receipt.KeyFile)
	if !create {
		key, _, err := receipt.LoadExisting(dir)
		if err != nil {
			return nil, "", false, fmt.Errorf("receipts: %w", err)
		}
		return key, source, false, nil
	}
	key, created, err = receipt.LoadOrCreate(dir)
	if err != nil {
		return nil, "", false, fmt.Errorf("receipts: %w", err)
	}
	return key, source, created, nil
}

// printKeyCreated tells the person once that a signing key now exists.
func printKeyCreated(w io.Writer, key ed25519.PrivateKey) {
	dir, _ := receiptKeyDir(os.Getenv, runtime.GOOS, os.UserHomeDir)
	fmt.Fprintf(w, "receipts: created signing key %s (%s)\n", filepath.Join(dir, receipt.KeyFile), receipt.KeyID(key.Public().(ed25519.PublicKey)))
}

// receiptSigner is what every receipt of one load shares: the key, the
// project's place in its repository and the files the load read.
type receiptSigner struct {
	key  ed25519.PrivateKey
	dir  string // the project's receipts directory
	root string // the Runefile's directory
	// repo is nil outside a repository. prefix is the Runefile's directory
	// relative to the top, "" or ending in "/"; exclude is its .rune.
	repo      *receipt.Repo
	prefix    string
	exclude   string
	project   string
	runefiles []receipt.RunefileEntry
	rune      receipt.RuneInfo
	// read holds each file the load read and the digest of what it read.
	read []loadedFile
}

type loadedFile struct{ path, sha256 string }

// newReceiptSigner prepares the signer of a load. A key that cannot be had
// is an error, and no body may run then (spec 060 FR-021).
func newReceiptSigner(ctx context.Context, opts Options, runefile string, loaded []string, mask func(string) string) (*receiptSigner, error) {
	key, _, created, err := signingKey(opts, true)
	if err != nil {
		return nil, err
	}
	if created {
		printKeyCreated(opts.Stderr, key)
	}
	root := filepath.Dir(runefile)
	s := &receiptSigner{
		key:     key,
		dir:     receipt.Dir(root),
		root:    root,
		project: filepath.Base(runefile),
		rune:    receipt.RuneInfo{Version: opts.Version, Commit: opts.Commit, OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	repo, err := receipt.OpenRepo(ctx, root)
	switch {
	case err == nil:
		rel, relErr := filepath.Rel(repo.Top, resolvedPath(root))
		if relErr == nil && rel != "." {
			s.prefix = filepath.ToSlash(rel) + "/"
		}
		s.repo, s.exclude, s.project = repo, s.prefix+".rune", s.prefix+s.project
	case !errors.Is(err, receipt.ErrNotRepository):
		fmt.Fprintf(opts.Stderr, "warning: receipts have no snapshot: %v\n", err)
	}
	s.runefiles = s.runefileEntries(ctx, loaded, mask)
	return s, nil
}

// resolvedPath is path with its links followed, or path when that fails.
// git names the top of a repository by its resolved path.
func resolvedPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// runefileEntries digests the files the load read. A file outside the
// repository, or one git ignores, is not covered: no snapshot holds it.
func (s *receiptSigner) runefileEntries(ctx context.Context, loaded []string, mask func(string) string) []receipt.RunefileEntry {
	entries := []receipt.RunefileEntry{}
	var inside []string
	seen := map[string]bool{}
	for _, path := range loaded {
		if seen[path] {
			continue
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		entry := receipt.RunefileEntry{SHA256: hex.EncodeToString(sum[:])}
		s.read = append(s.read, loadedFile{path: path, sha256: entry.SHA256})
		if s.repo == nil {
			if rel, err := filepath.Rel(s.root, path); err == nil {
				entry.Path = filepath.ToSlash(rel)
			} else {
				entry.Path = filepath.Base(path)
			}
		} else if rel, err := filepath.Rel(s.repo.Top, resolvedPath(path)); err != nil {
			entry.Path = filepath.Base(path)
		} else {
			entry.Path = filepath.ToSlash(rel)
			entry.Covered = !strings.HasPrefix(entry.Path, "../") && entry.Path != ".."
			if entry.Covered {
				inside = append(inside, entry.Path)
			}
		}
		entries = append(entries, entry)
	}
	if s.repo != nil && len(inside) > 0 {
		ignored, err := s.repo.CheckIgnore(ctx, inside)
		for i := range entries {
			if err != nil || slices.Contains(ignored, entries[i].Path) {
				entries[i].Covered = false
			}
		}
	}
	for i := range entries {
		entries[i].Path = mask(entries[i].Path)
	}
	return entries
}

// snapshot takes a snapshot of the working tree with HEAD at that moment. It
// is not cut short by an interrupt: an interrupted run still records what it
// left behind.
func (s *receiptSigner) snapshot(ctx context.Context) (tree, commit, headTree string, err error) {
	ctx = context.WithoutCancel(ctx)
	if tree, err = s.repo.Snapshot(ctx, s.exclude); err != nil {
		return "", "", "", err
	}
	commit, headTree, err = s.repo.Head(ctx)
	return tree, commit, headTree, err
}

// runefilesChanged reports whether a file the load read no longer holds what
// it read. A snapshot then holds other definitions than the ones that run.
func (s *receiptSigner) runefilesChanged() bool {
	for _, f := range s.read {
		data, err := os.ReadFile(f.path)
		if err != nil {
			return true
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.sha256 {
			return true
		}
	}
	return false
}

// receiptSigner is the signer of this engine's receipts, or nil when it
// records none. The endpoint of an (agent) task records with it too.
func (e *engine) receiptSigner() *receiptSigner {
	if e.receipts == nil {
		return nil
	}
	return e.receipts.signer
}

// recorder writes the receipts of one invocation and remembers how they
// relate (research R6).
type recorder struct {
	signer     *receiptSigner
	channel    string
	invocation string
	parent     string
	claims     *receipt.Claims
	overrides  []string // the names overridden on the command line (spec 061)

	mu      sync.Mutex
	byKey   map[string]string // the last receipt of a task and its arguments
	follows map[string]string // a post-hook's key to the task it follows
	failed  map[string]string // a task name to the receipt of its last failed body
}

func newRecorder(s *receiptSigner, channel, invocation, parent string, claims *receipt.Claims, overrides []string) *recorder {
	return &recorder{
		signer: s, channel: channel, invocation: invocation, parent: parent, claims: claims, overrides: overrides,
		byKey: map[string]string{}, follows: map[string]string{}, failed: map[string]string{},
	}
}

// receiptKey is the scheduler's run-once key of a task and its arguments.
func receiptKey(task *ast.Task, params eval.Args) string {
	return task.Name + "(" + params.Key() + ")"
}

// receiptRun is one receipt being made, from begin to finish.
type receiptRun struct {
	id      string
	task    *ast.Task
	params  eval.Args
	role    string
	related receipt.Related
	start   time.Time

	started bool // the body was about to start
	cached  bool
	snapped bool
	before  string
	commit  string
	head    string // HEAD's tree at the snapshot before
	snapErr error
	// stale is set when the Runefile or an import changed after the load
	// and before the snapshot: the snapshot does not hold what ran.
	stale  bool
	inputs []string // declared inputs, relative to the Runefile's directory
	// inputsErr is set when the cache could not list the declared inputs,
	// so whether git ignores one cannot be told.
	inputsErr bool
}

// inputsUnknown marks a run whose declared inputs the cache could not list.
func (r *receiptRun) inputsUnknown() {
	if r != nil {
		r.inputsErr = true
	}
}

// ID is the receipt's ID, or "" when nothing is recorded.
func (r *receiptRun) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// beginReceipt starts the receipt of a body Rune was asked to run. It returns
// nil when this run records nothing.
func (e *engine) beginReceipt(task *ast.Task, params eval.Args, role, failureOf string) *receiptRun {
	rec := e.receipts
	if rec == nil || e.plan != planRun {
		return nil
	}
	start := time.Now()
	r := &receiptRun{id: receipt.NewID(start), task: task, params: params, role: role, start: start}
	var deps []string
	for _, dep := range task.Deps {
		target, args, err := e.ResolveDep(task, params, dep)
		if err != nil || !e.Available(target) {
			continue
		}
		deps = append(deps, receiptKey(target, args))
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, key := range deps {
		if id, ok := rec.byKey[key]; ok && !slices.Contains(r.related.Dependencies, id) {
			r.related.Dependencies = append(r.related.Dependencies, id)
		}
	}
	r.related.Follows = rec.follows[receiptKey(task, params)]
	if failureOf != "" {
		r.related.FailureOf = rec.failed[failureOf]
	}
	return r
}

// bodyStarting takes the snapshot before the body, right before it starts,
// unless reuse took it a moment ago (spec 061). inputs are the declared
// [cache] inputs the cache matched.
func (e *engine) bodyStarting(r *receiptRun, inputs []string) {
	if r == nil {
		return
	}
	r.started, r.inputs = true, inputs
	if !r.snapped {
		e.takeBefore(r)
	}
}

// cachedHit marks a run that a cache hit skipped, and the run it reuses.
func (e *engine) cachedHit(r *receiptRun, source string, inputs []string) {
	if r == nil {
		return
	}
	r.cached, r.inputs, r.related.Reuses = true, inputs, source
	e.takeBefore(r)
}

func (e *engine) takeBefore(r *receiptRun) {
	s := e.receipts.signer
	r.snapped = true
	if s.repo == nil {
		return
	}
	r.before, r.commit, r.head, r.snapErr = s.snapshot(e.ctx)
	// Checked after the snapshot, so a change that the snapshot holds is
	// seen here.
	r.stale = s.runefilesChanged()
}

// finishReceipt writes the receipt of a run that ended with runErr. A receipt
// that cannot be made or written is a warning: the run's own result stands
// (FR-024).
func (e *engine) finishReceipt(r *receiptRun, runErr error) {
	if r == nil {
		return
	}
	rec, s := e.receipts, e.receipts.signer
	finished := time.Now()
	if !r.snapped {
		e.takeBefore(r)
	}
	mask := e.maskFunc()
	rc := receipt.Receipt{
		Version:          receipt.Version,
		ID:               r.id,
		Invocation:       rec.invocation,
		ParentInvocation: rec.parent,
		Task:             r.task.Name,
		Overrides:        rec.overrides,
		Role:             r.role,
		StartedAt:        r.start.UTC(),
		FinishedAt:       finished.UTC(),
		DurationMs:       finished.Sub(r.start).Milliseconds(),
		Rune:             s.rune,
		Executor:         e.executorID(r.task),
		Project:          s.project,
		Runefiles:        s.runefiles,
		Channel:          rec.channel,
		Claims:           rec.claims,
	}
	rc.Definition = definitionDigest(r.task, rc.Executor)
	rc.Arguments, rc.ArgumentsRedacted = receiptArgs(r.task, r.params, mask)
	if r.related.Dependencies != nil || r.related.Follows != "" || r.related.FailureOf != "" || r.related.Reuses != "" {
		related := r.related
		rc.Related = &related
	}
	observed := r.snapErr == nil && !r.stale
	if s.repo != nil {
		after := r.before
		if r.started && r.snapErr == nil {
			var err error
			if after, _, _, err = s.snapshot(e.ctx); err != nil {
				observed = false
			}
		}
		rc.Source = &receipt.Source{Commit: r.commit, Dirty: r.before != r.head, Before: r.before, After: after}
		if uncovered, err := e.uncoveredInputs(r.inputs, mask); err != nil || r.inputsErr {
			observed = false
		} else {
			rc.UncoveredInputs = uncovered
		}
	}
	rc.Status, rc.ExitCode = receiptStatus(e.ctx, runErr, r.cached, r.started, e.ExitCode, observed)
	if rc.Status == receipt.StatusPassed {
		outputs, err := e.receiptOutputs(r, mask)
		if err != nil {
			rc.Status = receipt.StatusIncomplete
		}
		rc.Outputs = outputs
	}
	if err := writeReceipt(s, rc); err != nil {
		e.Warnf("receipt for %s not written: %v", r.task.Name, err)
		return
	}
	rec.remember(e, r, runErr)
}

// remember records how later receipts of the invocation relate to this one.
func (rec *recorder) remember(e *engine, r *receiptRun, runErr error) {
	var follows []string
	if runErr == nil {
		for _, hook := range r.task.PostHooks {
			if target, args, err := e.ResolveDep(r.task, r.params, hook); err == nil {
				follows = append(follows, receiptKey(target, args))
			}
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.byKey[receiptKey(r.task, r.params)] = r.id
	if runErr != nil && r.role == receipt.RoleTask {
		rec.failed[r.task.Name] = r.id
	}
	for _, key := range follows {
		if _, ok := rec.follows[key]; !ok {
			rec.follows[key] = r.id
		}
	}
}

func writeReceipt(s *receiptSigner, rc receipt.Receipt) error {
	statement, err := receipt.Statement(rc)
	if err != nil {
		return err
	}
	line, err := receipt.Sign(s.key, statement)
	if err != nil {
		return err
	}
	return receipt.Write(s.dir, rc.ID, line)
}

// uncoveredInputs are the declared inputs git ignores: no snapshot holds
// them, so the receipt cannot be evidence for a revision (FR-013).
func (e *engine) uncoveredInputs(inputs []string, mask func(string) string) ([]string, error) {
	s := e.receipts.signer
	if len(inputs) == 0 {
		return nil, nil
	}
	paths := make([]string, len(inputs))
	for i, in := range inputs {
		paths[i] = s.prefix + in
	}
	ignored, err := s.repo.CheckIgnore(context.WithoutCancel(e.ctx), paths)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ignored {
		out = append(out, mask(strings.TrimPrefix(p, s.prefix)))
	}
	return out, nil
}

// receiptOutputs digests the declared [cache] outputs of a passed run, and
// writes their list next to the receipt when it is too long for it.
func (e *engine) receiptOutputs(r *receiptRun, mask func(string) string) (*receipt.Outputs, error) {
	out, manifest, err := e.currentOutputs(r.task, r.params, mask)
	if err != nil || manifest == nil {
		return out, err
	}
	name, err := receipt.WriteManifest(e.receipts.signer.dir, r.id, manifest)
	if err != nil {
		return nil, err
	}
	out.Manifest = name
	return out, nil
}

// currentOutputs digests the declared [cache] outputs of task as they are
// now, as a passed run's receipt records them, and writes nothing. The
// manifest is the list to write when it does not fit in the receipt. Nil
// without [cache] or without output files.
func (e *engine) currentOutputs(task *ast.Task, params eval.Args, mask func(string) string) (*receipt.Outputs, []byte, error) {
	attr := task.Attr(ast.AttrCache)
	if attr == nil {
		return nil, nil, nil
	}
	spec := e.cacheSpec(task, params, attr)
	rel, err := cache.OutputFiles(e.root, spec.Outputs)
	if err != nil {
		return nil, nil, err
	}
	files, err := receipt.OutputFiles(e.root, rel)
	if err != nil {
		return nil, nil, err
	}
	for i := range files {
		files[i].Path = mask(files[i].Path)
	}
	return receipt.BuildOutputs(files)
}

// maskFunc is the engine's secret mask, or one that changes nothing.
func (e *engine) maskFunc() func(string) string {
	if e.mask == nil {
		return func(s string) string { return s }
	}
	return e.mask
}

// receiptStatus is the status and exit code of a run (research R2). The
// exit code is the one Rune exits with for that error, so a receipt never
// disagrees with what the caller saw. observed is false when a snapshot or a
// digest failed; a passed or failed run is then incomplete.
func receiptStatus(ctx context.Context, err error, cached, started bool, exitCode func(error) int, observed bool) (string, *int) {
	var status string
	var code *int
	switch {
	case err == nil && cached:
		return receipt.StatusCached, nil
	case err == nil:
		status, code = receipt.StatusPassed, new(0)
	case ctx.Err() != nil:
		if started {
			code = new(CodeFor(&Interrupted{Err: err}))
		}
		return receipt.StatusInterrupted, code
	case errors.Is(err, scheduler.ErrBodyNotRun) || !started:
		return receipt.StatusRefused, nil
	default:
		status, code = receipt.StatusFailed, new(exitCode(err))
	}
	if !observed {
		status = receipt.StatusIncomplete
	}
	return status, code
}

// receiptChannel names the path a command-line run started from.
func receiptChannel(opts Options, agentRun bool) string {
	switch {
	case opts.nodeRun != nil:
		return receipt.ChannelCIRun
	case agentRun:
		return receipt.ChannelAgent
	}
	return receipt.ChannelCLI
}

// overrideNames are the names of the overridden variables, masked and
// sorted, never their values. Nil when there are none.
func overrideNames(overrides map[string]string, mask func(string) string) []string {
	var names []string
	for name := range overrides {
		names = append(names, mask(name))
	}
	slices.Sort(names)
	return names
}

// envClaims are the claim variables that are set, masked. Nil when none is.
func envClaims(getenv func(string) string, mask func(string) string) map[string]string {
	var claims map[string]string
	for _, name := range receiptClaimVars {
		if v := getenv(name); v != "" {
			if claims == nil {
				claims = map[string]string{}
			}
			claims[name] = mask(v)
		}
	}
	return claims
}

// receiptArgs lists the task's parameters in declaration order with the
// values they were bound to, masked, and whether masking changed any.
func receiptArgs(task *ast.Task, params eval.Args, mask func(string) string) ([]receipt.Arg, bool) {
	args := make([]receipt.Arg, 0, len(task.Params))
	redacted := false
	for _, p := range task.Params {
		values := []string{params.Values[p.Name]}
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			values = slices.Clone(params.Lists[p.Name])
			if values == nil {
				values = []string{}
			}
		}
		for i, v := range values {
			if m := mask(v); m != v {
				values[i], redacted = m, true
			}
		}
		args = append(args, receipt.Arg{Name: p.Name, Values: values})
	}
	return args, redacted
}

// definitionDigest identifies what the task is: its definition as the
// Runefile states it, and the executor that runs it.
func definitionDigest(task *ast.Task, executor string) string {
	sum := sha256.Sum256([]byte(ast.Dump(&ast.File{Tasks: []*ast.Task{task}}) + "\x00" + executor))
	return hex.EncodeToString(sum[:])
}
