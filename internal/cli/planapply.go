package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"mvdan.cc/sh/v3/expand"
	shinterp "mvdan.cc/sh/v3/interp"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/planstore"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/mcpserver"
)

// A task with [plan] makes a plan file that Rune takes into its store. A
// task with [applies] runs only with that stored file, after a person
// approved its digest, and once (spec 051). Everything in this file connects
// a run to the store: the capture, the lookup and its refusals, the claim,
// and the question on the command line.

const (
	surfaceMCP = "mcp"
	surfaceCLI = "cli"

	// applyQuestion is what a person at a terminal is asked. The request an
	// agent's client shows ends with the same promise.
	applyQuestion = "Apply this plan once?"

	// The terminal shows more of a summary than a client request can hold.
	cliSummaryHead = 48 << 10
	cliSummaryTail = 16 << 10

	gitTimeout = 3 * time.Second
)

// planIdentity is what one load of a Runefile contributes to a plan's
// binding. It is taken once per load: a running server keeps the environment
// it loaded, so it must keep the digest of that load too.
type planIdentity struct {
	runefile string // absolute path of the Runefile
	def      string // digest of the composed definition
	config   string // digest of the settings, the dotenv entries and the overrides
}

// newPlanIdentity computes the identity of a load.
func newPlanIdentity(file *ast.File, root string, settings config.Settings, dotenv []string, overrides map[string]string) planIdentity {
	return planIdentity{
		runefile: runefilePath(file, root),
		def:      runefileDigest(file),
		config:   configDigest(settings, dotenv, overrides),
	}
}

// runefilePath is the path that keeps the plans of two Runefiles in one
// directory apart.
func runefilePath(file *ast.File, root string) string {
	path := "Runefile"
	if file != nil && file.Path != "" {
		path = file.Path
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.Base(path))
	}
	return filepath.Join(realPath(filepath.Dir(path)), filepath.Base(path))
}

// realPath is a directory with its symbolic links resolved, so that one
// project reached by two paths is one target: a server started from the
// physical directory must find the plan a shell made through a link. A path
// that cannot be resolved is kept as written.
func realPath(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

func hexDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// runefileDigest identifies the composed definition: every task, variable and
// setting after imports. It is coarser than the task that applies a plan and
// cannot miss a change in something that task reaches.
func runefileDigest(file *ast.File) string {
	if file == nil {
		return hexDigest(nil)
	}
	return hexDigest([]byte(ast.Dump(file)))
}

// configDigest identifies what a load resolved beside the definition.
func configDigest(settings config.Settings, dotenv []string, overrides map[string]string) string {
	pairs := make([]string, 0, len(overrides))
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		pairs = append(pairs, name+"="+overrides[name])
	}
	// Marshalling these types cannot fail: strings, booleans and an integer.
	data, _ := json.Marshal(struct {
		Settings  config.Settings
		Dotenv    []string
		Overrides []string
	}{settings, dotenv, pairs})
	return hexDigest(data)
}

// targetKey names what a plan is for: one Runefile, one plan task, one set of
// effective arguments. An apply call computes the same key from its own
// arguments, which is the only way a plan is ever selected.
func targetKey(runefile, planTask string, args eval.Args) string {
	return hexDigest([]byte(runefile + "\x00" + planTask + "\x00" + args.Key()))
}

// argsText shows a task's effective arguments as name="value" pairs, each
// value masked and cut. It is what a record and the journal keep in place of
// the values themselves.
func argsText(task *ast.Task, args eval.Args, hide func(string) string) string {
	parts := make([]string, 0, len(task.Params))
	for _, p := range task.Params {
		if p.Kind == ast.ParamVariadicPlus || p.Kind == ast.ParamVariadicStar {
			elems := make([]string, len(args.Lists[p.Name]))
			for i, el := range args.Lists[p.Name] {
				elems[i] = renderPolicyValue(el, hide)
			}
			parts = append(parts, p.Name+"=["+strings.Join(elems, " ")+"]")
			continue
		}
		parts = append(parts, p.Name+"="+renderPolicyValue(args.Values[p.Name], hide))
	}
	return strings.Join(parts, ", ")
}

// errToolNotFound is the error of lookPath for a name that no directory of
// the path holds as an executable.
var errToolNotFound = errors.New("was not found on the path")

// lookPath finds an executable on the PATH of env, the environment a body
// runs with, which a dotenv file or an [env] attribute can change. Nil means
// the process environment. cwd is the directory the body runs in: a body
// resolves a relative or empty entry of the path against it, not against the
// directory Rune was started in.
//
// The search is the one of the shell that runs a body, so the executable
// recorded is the one the body starts: a second rule here could name another
// file where the two differ, such as an empty entry or PATHEXT on Windows.
func lookPath(name, cwd string, env []string) (string, error) {
	if env == nil {
		env = os.Environ()
	}
	found, err := shinterp.LookPathDir(cwd, expand.ListEnviron(env...), name)
	if err != nil {
		return "", fmt.Errorf("%q %w", name, errToolNotFound)
	}
	// The digest reads the file to its end, which a pipe or a device never has.
	if info, err := os.Stat(found); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q %w", name, errToolNotFound)
	}
	return found, nil
}

// toolIdentity is the executable a tool name resolves to, and the digest of
// its bytes. Rune does not run the tool outside a body, so this stands for
// its version.
func toolIdentity(name, cwd string, env []string) (path, digest string, err error) {
	found, err := lookPath(name, cwd, env)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		return "", "", fmt.Errorf("resolving %s: %w", found, err)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", resolved, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", "", fmt.Errorf("reading %s: %w", resolved, err)
	}
	return resolved, hex.EncodeToString(h.Sum(nil)), nil
}

// gitInfo asks git for the commit of dir and whether its tree is dirty. It
// is recorded with a plan, not compared: the definition digest is what
// identifies uncommitted work. Any failure leaves the answer empty.
//
// ignore are paths relative to dir, written with forward slashes, that do not
// make the tree dirty: the plan file and its summary are still in the
// workspace when a plan is recorded, and they are what the task makes, not a
// change to the tree.
func gitInfo(ctx context.Context, dir string, ignore ...string) (commit string, dirty *bool) {
	run := func(args ...string) (string, bool) {
		ctx, cancel := context.WithTimeout(ctx, gitTimeout)
		defer cancel()
		// core.fsmonitor can name a program, and a status call would start it.
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	commit, ok := run("rev-parse", "HEAD")
	if !ok {
		return "", nil
	}
	statusArgs := []string{"status", "--porcelain"}
	if len(ignore) > 0 {
		statusArgs = append(statusArgs, "--", ":(top)")
		for _, p := range ignore {
			statusArgs = append(statusArgs, ":(exclude,literal)"+path.Clean(p))
		}
	}
	status, ok := run(statusArgs...)
	if !ok {
		return commit, nil
	}
	isDirty := status != ""
	return commit, &isDirty
}

// errNoPlanIdentity is reported when an engine was built without the
// identity of its load. A constructor that forgets it must fail on the first
// plan, not bind plans to nothing.
var errNoPlanIdentity = errors.New("this load of the Runefile has no plan identity")

// usesPlans reports whether any task makes or applies a plan. A Runefile
// without one never touches the store.
func usesPlans(tasks map[string]*ast.Task) bool {
	for _, t := range tasks {
		if t.Attr(ast.AttrPlan) != nil || t.Attr(ast.AttrApplies) != nil {
			return true
		}
	}
	return false
}

// applyAttempt is the claimed attempt of a run: the invocation it belongs to
// and the private copy that invocation's body is handed.
type applyAttempt struct {
	task    string
	argsKey string
	ticket  *planTicket
	claim   *planstore.Attempt
}

// planTicket is a stored plan that passed every check for one apply call.
type planTicket struct {
	store   *planstore.Store
	rec     planstore.Record
	summary string
	notes   []string
	apply   *ast.Task
	args    string // masked display text of the call's arguments
}

// approval is what the person is shown about the plan. hide masks the
// summary, which the plan body wrote and may hold a secret.
func (t *planTicket) approval(hide func(string) string) *mcpserver.PlanApproval {
	if hide == nil {
		hide = func(s string) string { return s }
	}
	return &mcpserver.PlanApproval{
		ID:      t.rec.ID,
		Task:    t.rec.PlanTask,
		Created: planTime(t.rec.Created),
		Digest:  t.rec.Artifact.SHA256,
		Size:    t.rec.Artifact.Size,
		Summary: hide(t.summary),
		Notes:   t.notes,
	}
}

func planTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// planStore opens the store of this Runefile's directory. Opening touches no
// file.
func (e *engine) planStore() *planstore.Store {
	store := planstore.Open(e.root)
	store.Now = e.clock
	return store
}

// journal appends one plan event. A journal that cannot be written must not
// stop a run, and must not go unnoticed either.
func (e *engine) journal(store *planstore.Store, ev planstore.Event) {
	ev.Surface = e.surface
	err := store.Log(ev)
	switch {
	case err == nil:
	case e.operatorErr != nil:
		fmt.Fprintf(e.operatorErr, "warning: the plan journal was not written: %v\n", err)
	default:
		e.Warnf("the plan journal was not written: %v", err)
	}
}

func (t *planTicket) event(name string) planstore.Event {
	return planstore.Event{
		Event:     name,
		PlanTask:  t.rec.PlanTask,
		Plan:      t.rec.ID,
		Digest:    t.rec.Artifact.SHA256,
		Args:      t.args,
		ApplyTask: t.apply.Name,
	}
}

// applyTasksOf lists the tasks whose [applies] names plan, sorted.
func (e *engine) applyTasksOf(plan string) []string {
	var names []string
	for name, t := range e.tasks {
		if t.AppliesPlan() == plan {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// planBinding is what a plan of planTask is made for, as this load sees it.
// The tool is the executable its name resolves to for body, the task whose
// body runs it: the plan task when a plan is stored, the task that applies it
// when the plan is checked, so the apply is held to the executable the plan
// was made with. A declared tool that is not on the path gives an error and
// an empty identity, which an apply reads as a tool that changed.
func (e *engine) planBinding(planTask, body *ast.Task) (planstore.Binding, error) {
	b := planstore.Binding{
		WorkDir:  realPath(e.taskDir(planTask)),
		Runefile: e.planIdent.def,
		Config:   e.planIdent.config,
		Host:     e.goos + "/" + runtime.GOARCH,
	}
	spec := planTask.PlanSpec()
	if spec == nil || spec.Tool == "" {
		return b, nil
	}
	b.Tool = spec.Tool
	path, digest, err := toolIdentity(spec.Tool, e.taskDir(body), e.taskEnv(body))
	if err != nil {
		return b, err
	}
	b.ToolPath, b.ToolDigest = path, digest
	return b, nil
}

// planFailure fails a plan task after its body: the text is printed here,
// because a silent failure has no banner of its own on the MCP path.
func (e *engine) planFailure(format string, args ...any) error {
	err := errorf(format, args...)
	printErrorBanner(e.opts, e.theme, err.Error())
	return &TaskFailure{Err: err, Silent: true}
}

// capturePlan takes the plan a task's body just wrote into the store: the
// file and its summary are copied while they are hashed, recorded with what
// the plan was made for, and removed from the workspace, so that a body that
// reads the workspace path later fails and does not apply a file nobody
// reviewed.
func (e *engine) capturePlan(task *ast.Task, args eval.Args) error {
	if e.planIdent == (planIdentity{}) {
		return e.planFailure("the plan of task %q was not stored: %v", task.Name, errNoPlanIdentity)
	}
	spec := task.PlanSpec()
	expiry, err := ast.PlanExpiry(spec.Expires)
	if err != nil {
		return e.planFailure("plan expires %q of task %q: %v", spec.Expires, task.Name, err)
	}
	binding, err := e.planBinding(task, task)
	switch {
	case errors.Is(err, errToolNotFound):
		return e.planFailure("plan tool %q of task %q was not found on the path", spec.Tool, task.Name)
	case err != nil:
		// The tool is there and cannot be read or resolved, which is another
		// thing to fix than a path that lacks it.
		return e.planFailure("plan tool %q of task %q cannot be identified: %s", spec.Tool, task.Name, e.masked(err.Error()))
	}
	dir := e.taskDir(task)
	artifact := filepath.Join(dir, filepath.FromSlash(spec.File))
	summary := filepath.Join(dir, filepath.FromSlash(spec.Summary))
	commit, dirty := e.git(dir, spec.File, spec.Summary)

	store := e.planStore()
	rec, replaced, err := store.Save(planstore.SaveInput{
		Record: planstore.Record{
			Key:         targetKey(e.planIdent.runefile, task.Name, args),
			PlanTask:    task.Name,
			ApplyTasks:  e.applyTasksOf(task.Name),
			Args:        argsText(task, args, e.mask),
			Binding:     binding,
			RuneVersion: e.opts.Version,
			Git:         planstore.Git{Commit: commit, Dirty: dirty},
			Surface:     e.surface,
		},
		Expiry:       expiry,
		ArtifactPath: artifact,
		SummaryPath:  summary,
		MaxArtifact:  e.planMaxArtifact,
		MaxSummary:   e.planMaxSummary,
	})
	if err != nil {
		var source *planstore.SourceError
		if errors.As(err, &source) {
			what, path := "file", spec.File
			if source.Path == summary {
				what, path = "summary", spec.Summary
			}
			return e.planFailure("plan %s %q of task %q %s", what, path, task.Name, source.Detail)
		}
		return e.planFailure("the plan of task %q was not stored: %s", task.Name, e.masked(err.Error()))
	}

	created := planstore.Event{
		Event: planstore.EventPlanCreated, PlanTask: task.Name, Plan: rec.ID,
		Digest: rec.Artifact.SHA256, Args: rec.Args,
	}
	e.journal(store, created)
	for _, old := range replaced {
		e.journal(store, planstore.Event{
			Event: planstore.EventPlanReplaced, PlanTask: task.Name, Plan: old.ID,
			Digest: old.Artifact.SHA256, Args: old.Args, ReplacedBy: rec.ID,
		})
	}
	for _, path := range []string{artifact, summary} {
		if err := os.Remove(path); err != nil {
			e.Warnf("plan %s: could not remove %q from the workspace: %v", rec.ID, path, err)
		}
	}
	store.Prune()

	apply := "No task applies it."
	if len(rec.ApplyTasks) > 0 {
		quoted := make([]string, len(rec.ApplyTasks))
		for i, name := range rec.ApplyTasks {
			quoted[i] = fmt.Sprintf("%q", name)
		}
		apply = "Apply it with " + strings.Join(quoted, " or ") + "."
	}
	fmt.Fprintf(e.opts.Stderr, "plan %s: sha256:%s, %d bytes, expires %s. %s\n",
		rec.ID, rec.Artifact.SHA256, rec.Artifact.Size, planTime(rec.Expires), apply)
	return nil
}

// git reads the commit and the dirty flag through the engine's hook, or the
// real git when a caller set none. ignore is as for gitInfo.
func (e *engine) git(dir string, ignore ...string) (string, *bool) {
	if e.gitInfo != nil {
		return e.gitInfo(e.ctx, dir)
	}
	return gitInfo(e.ctx, dir, ignore...)
}

func (e *engine) masked(s string) string {
	if e.mask == nil {
		return s
	}
	return e.mask(s)
}

// changedParts names the bound parts of a plan that differ now, in the order
// a refusal lists them.
func changedParts(then, now planstore.Binding) []string {
	var parts []string
	if then.Runefile != now.Runefile {
		parts = append(parts, "Runefile definition")
	}
	if then.Config != now.Config {
		parts = append(parts, "configuration")
	}
	if then.WorkDir != now.WorkDir {
		parts = append(parts, "working directory")
	}
	if then.Host != now.Host {
		parts = append(parts, "host")
	}
	if then.Tool != now.Tool || then.ToolPath != now.ToolPath || then.ToolDigest != now.ToolDigest {
		name := then.Tool
		if name == "" {
			name = now.Tool
		}
		parts = append(parts, fmt.Sprintf("tool %q", name))
	}
	return parts
}

// storeReason words an error of the store for a refusal.
func storeReason(planTask string, rec planstore.Record, err error) string {
	var artifact *planstore.ArtifactError
	switch {
	case errors.As(err, &artifact):
		return fmt.Sprintf("the stored %s of plan %s %s; run %q again", artifact.What, rec.ID, artifact.Detail, planTask)
	case errors.Is(err, planstore.ErrClaimed):
		return fmt.Sprintf("plan %s was already used by another call", rec.ID)
	case errors.Is(err, planstore.ErrExpired):
		return fmt.Sprintf("plan %s expired at %s; run %q again", rec.ID, planTime(rec.Expires), planTask)
	}
	return "the plan store cannot be used: " + err.Error()
}

// findPlan finds the stored plan an apply call would use and checks
// everything that can be checked before a person is asked: that there is
// one, that it was not used, replaced or outlived, that what it was made for
// has not changed, and that the stored bytes are the recorded ones. Any
// failure is the call's refusal, already journaled.
func (e *engine) findPlan(apply *ast.Task, args eval.Args) (*planTicket, error) {
	planTask, ok := e.tasks[apply.AppliesPlan()]
	if !ok {
		return nil, errorf("task %q was not run: its plan task %q does not exist", apply.Name, apply.AppliesPlan())
	}
	// Without the load's identity every binding would compare equal.
	if e.planIdent == (planIdentity{}) {
		return nil, errorf("task %q was not run: the plan store cannot be used: %v", apply.Name, errNoPlanIdentity)
	}
	store := e.planStore()
	store.Prune()
	text := argsText(apply, args, e.mask)
	refuse := func(rec *planstore.Record, reason string) (*planTicket, error) {
		ev := planstore.Event{Event: planstore.EventApplyRefused, PlanTask: planTask.Name, Args: text, ApplyTask: apply.Name, Reason: reason}
		if rec != nil {
			ev.Plan, ev.Digest = rec.ID, rec.Artifact.SHA256
		}
		e.journal(store, ev)
		return nil, errorf("task %q was not run: %s", apply.Name, reason)
	}

	rec, state, found, err := store.Latest(targetKey(e.planIdent.runefile, planTask.Name, args))
	switch {
	case err != nil:
		return refuse(nil, e.masked(storeReason(planTask.Name, rec, err)))
	case !found && text == "":
		return refuse(nil, fmt.Sprintf("there is no plan; run %q first", planTask.Name))
	case !found:
		return refuse(nil, fmt.Sprintf("there is no plan for %s; run %q with the same arguments first", text, planTask.Name))
	}

	if reason := stateReason(store, rec, state, apply.Name); reason != "" {
		return refuse(&rec, reason)
	}

	// A tool that is gone has no identity now, so it differs from the one
	// recorded and is reported with the other parts.
	now, _ := e.planBinding(planTask, apply)
	if parts := changedParts(rec.Binding, now); len(parts) > 0 {
		return refuse(&rec, fmt.Sprintf("plan %s no longer matches: the %s changed since it was made; run %q again", rec.ID, strings.Join(parts, ", "), planTask.Name))
	}
	// Another call can apply the plan between the lookup and these reads,
	// and its finish deletes the bytes. That is a plan that was used, not a
	// stored file that went missing.
	stored := func(err error) (*planTicket, error) {
		if reason := stateReason(store, rec, store.StateOf(rec), apply.Name); reason != "" {
			return refuse(&rec, reason)
		}
		return refuse(&rec, e.masked(storeReason(planTask.Name, rec, err)))
	}
	if err := store.Verify(rec); err != nil {
		return stored(err)
	}
	summary, err := store.Summary(rec)
	if err != nil {
		return stored(err)
	}

	ticket := &planTicket{store: store, rec: rec, summary: summary, apply: apply, args: text}
	if last, ok := store.LastAttempt(rec.Key, rec.ID); ok {
		switch last.State {
		case planstore.StateFailed:
			ticket.notes = append(ticket.notes, fmt.Sprintf("the previous apply of this target failed at %s. Check the target before approving.", planTime(last.Finished)))
		case planstore.StateUnfinished:
			ticket.notes = append(ticket.notes, fmt.Sprintf("the previous apply of this target started at %s and did not finish. Check the target before approving.", planTime(last.Started)))
		}
	}
	return ticket, nil
}

// stateReason is why a plan in the given state cannot be applied, or "" for
// one that is ready.
func stateReason(store *planstore.Store, rec planstore.Record, state planstore.State, apply string) string {
	again := fmt.Sprintf("run %q again and review the new plan", rec.PlanTask)
	attempt, _ := store.AttemptOf(rec)
	switch state {
	case planstore.StateApplied:
		return fmt.Sprintf("plan %s was already applied at %s; %s", rec.ID, planTime(attempt.Finished), again)
	case planstore.StateFailed:
		return fmt.Sprintf("the apply of plan %s failed at %s; check the target, then %s", rec.ID, planTime(attempt.Finished), again)
	case planstore.StateUnfinished:
		return fmt.Sprintf("an apply of plan %s started at %s and did not finish, so its outcome is unknown; check the target, then %s", rec.ID, planTime(attempt.Started), again)
	case planstore.StateReplaced:
		return replacedReason(rec, apply)
	case planstore.StateExpired:
		return storeReason(rec.PlanTask, rec, planstore.ErrExpired)
	}
	return ""
}

func replacedReason(rec planstore.Record, apply string) string {
	return fmt.Sprintf("plan %s was replaced by a newer plan; call %q again", rec.ID, apply)
}

// claimPlan uses the plan up for this run: of any number of calls and
// processes one gets the claim, and with it a private copy of the artifact
// that was verified while it was copied. A claim that is refused changes
// nothing: a plan replaced or outlived during the wait for an answer is not
// a failed apply.
func (e *engine) claimPlan(ticket *planTicket, args eval.Args) error {
	claim, err := ticket.store.Claim(ticket.rec, ticket.apply.Name, e.surface)
	if err != nil {
		reason := storeReason(ticket.rec.PlanTask, ticket.rec, err)
		var artifact *planstore.ArtifactError
		switch {
		case errors.Is(err, planstore.ErrReplaced):
			reason = replacedReason(ticket.rec, ticket.apply.Name)
		case errors.As(err, &artifact):
			// The call that won the claim may have finished and deleted the
			// bytes while this one was copying them.
			switch ticket.store.StateOf(ticket.rec) {
			case planstore.StateUnfinished, planstore.StateApplied, planstore.StateFailed:
				reason = storeReason(ticket.rec.PlanTask, ticket.rec, planstore.ErrClaimed)
			case planstore.StateReplaced:
				reason = replacedReason(ticket.rec, ticket.apply.Name)
			}
		}
		reason = e.masked(reason)
		ev := ticket.event(planstore.EventApplyRefused)
		ev.Reason = reason
		e.journal(ticket.store, ev)
		return errorf("task %q was not run: %s", ticket.apply.Name, reason)
	}
	e.attempt = &applyAttempt{task: ticket.apply.Name, argsKey: args.Key(), ticket: ticket, claim: claim}
	ev := ticket.event(planstore.EventApplyStarted)
	ev.Attempt = claim.ID
	e.journal(ticket.store, ev)
	return nil
}

// finishApply ends the run's attempt: the copy is checked once more, the
// outcome is recorded and the bytes are deleted. It reports whether the copy
// changed while it was applied, which fails the call whatever the body's
// exit code was.
func (e *engine) finishApply(exit int) (tampered bool) {
	a := e.attempt
	if a == nil {
		return false
	}
	e.attempt = nil
	tampered, err := a.claim.Finish(exit)
	if err != nil {
		e.Warnf("plan %s: the outcome was not recorded: %v", a.ticket.rec.ID, err)
	}
	state := planstore.StateApplied
	if exit != 0 || tampered {
		state = planstore.StateFailed
	}
	ev := a.ticket.event(planstore.EventApplyFinished)
	ev.Attempt, ev.Exit, ev.State, ev.Tampered = a.claim.ID, &exit, state.String(), tampered
	e.journal(a.ticket.store, ev)
	if tampered {
		fmt.Fprintf(e.opts.Stderr, "plan %s: the plan file changed while it was being applied; recorded as failed and tampered\n", a.ticket.rec.ID)
	}
	return tampered
}

// planEnv is what the body of the run's claimed apply invocation gets, and
// nothing for any other body. It goes last in the environment, so no dotenv
// entry, exported variable or [env] attribute of these names is read in its
// place.
func (e *engine) planEnv(task *ast.Task, args eval.Args) []string {
	a := e.attempt
	if a == nil || a.task != task.Name || a.argsKey != args.Key() {
		return nil
	}
	return []string{
		ast.PlanFileVar + "=" + a.claim.Path,
		ast.PlanIDVar + "=" + a.ticket.rec.ID,
		ast.PlanDigestVar + "=sha256:" + a.ticket.rec.Artifact.SHA256,
	}
}

// applyGate is the check where the body of a task that applies a plan would
// start. The analyzer keeps such a task from being run by another task, and
// every caller claims a plan first: this holds the rule if they ever
// disagree with the scheduler.
func (e *engine) applyGate(task *ast.Task, args eval.Args) *TaskFailure {
	if task.AppliesPlan() == "" || e.planEnv(task, args) != nil {
		return nil
	}
	return &TaskFailure{Err: errorf("task %q applies a plan and was reached without an approved plan", task.Name)}
}

// The two refusals that have no plan to name.
func applyNobodyText(task string) string {
	return fmt.Sprintf("task %q applies a plan and runs only when a person approves that plan; nobody can be asked on this path", task)
}

func applyAgentRunText(task string) string {
	return fmt.Sprintf("task %q applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it", task)
}

// askApproval puts a plan to whoever can approve it and journals the
// question and its answer. ask returns nil for an approval, with the reason
// it was given without a question when there is one. Any error is the
// refusal, returned unchanged.
func (e *engine) askApproval(ticket *planTicket, ask func() (reason string, err error)) error {
	e.journal(ticket.store, ticket.event(planstore.EventApprovalRequested))
	reason, err := ask()
	if err != nil {
		ev := ticket.event(planstore.EventApprovalNotGiven)
		ev.Reason, _ = cutRunes(printableLine(e.masked(err.Error())), maxPolicyReasonRunes)
		e.journal(ticket.store, ev)
		return err
	}
	ev := ticket.event(planstore.EventApprovalGiven)
	ev.Reason = reason
	e.journal(ticket.store, ev)
	return nil
}

// prepareApplyRoot handles a task that applies a plan when a person names it
// on the command line: the same checks as for an agent's call, the plan's
// identity and summary on standard error, one question under the rules of
// the [confirm] gate, and the claim. It does nothing for other commands.
func (e *engine) prepareApplyRoot(invs []scheduler.Invocation) error {
	idx := slices.IndexFunc(invs, func(inv scheduler.Invocation) bool { return inv.Task.AppliesPlan() != "" })
	if idx < 0 {
		return nil
	}
	// The scheduler runs every root, and one approval covers one invocation.
	if len(invs) != 1 {
		return usagef("a task that applies a plan must be the only task named in a command")
	}
	task, args := invs[idx].Task, invs[idx].Params
	if e.agent {
		return &TaskFailure{Err: errors.New(applyAgentRunText(task.Name))}
	}
	ticket, err := e.findPlan(task, args)
	if err != nil {
		return &TaskFailure{Err: err}
	}
	e.printPlan(ticket)

	err = e.askApproval(ticket, func() (string, error) {
		if e.opts.Yes {
			return "--yes", nil
		}
		switch e.confirm(task, applyQuestion) {
		case confirmApproved:
			return "", nil
		case confirmNoTerminal:
			return "", &TaskFailure{Err: noTerminalError(task, applyQuestion)}
		}
		return "", &TaskFailure{Err: errorf("task %q was not confirmed", task.Name), Silent: true}
	})
	if err != nil {
		return err
	}
	if err := e.claimPlan(ticket, args); err != nil {
		return &TaskFailure{Err: err}
	}
	// The one answer covers the task's own [confirm], whose message was shown.
	e.approved = map[string]bool{task.Name: true}
	return nil
}

// printPlan writes what a person at a terminal reviews before the question.
func (e *engine) printPlan(ticket *planTicket) {
	w := e.opts.Stderr
	rec := ticket.rec
	fmt.Fprintf(w, "Plan %s made by %q at %s\n", rec.ID, rec.PlanTask, planTime(rec.Created))
	target := ticket.apply.Name
	if ticket.args != "" {
		target += " " + ticket.args
	}
	fmt.Fprintf(w, "Target: %s\n", target)
	fmt.Fprintf(w, "Artifact: sha256:%s (%d bytes)\n", rec.Artifact.SHA256, rec.Artifact.Size)
	for _, note := range ticket.notes {
		fmt.Fprintf(w, "Note: %s\n", note)
	}
	fmt.Fprintf(w, "Summary:\n%s\n", mcpserver.SummaryLines(e.masked(ticket.summary), cliSummaryHead, cliSummaryTail))
	if c := ticket.apply.Attr(ast.AttrConfirm); c != nil && c.Str != "" {
		fmt.Fprintf(w, "Confirmation: %s\n", oneLine(c.Str))
	}
}
