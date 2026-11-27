package cli

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/receipt"
)

// Reuse of trusted receipts (spec 061): with --reuse-key, a [reuse] task is
// not run when a receipt signed by one of those keys shows it passed,
// recently, on exactly the files it would run on now, in the same context.

// DefaultReuseMaxAge is the oldest receipt --reuse-key reuses when
// --reuse-max-age is not given.
const DefaultReuseMaxAge = 24 * time.Hour

// reuser decides, for each [reuse] task of one run, whether a trusted
// receipt stands in for its body.
type reuser struct {
	// keys are the --reuse-key keys only: the key this machine signs with
	// can be read by the bodies Rune runs, so it is never trusted here.
	keys   []ed25519.PublicKey
	maxAge time.Duration
	// overridden is set when the run overrode a variable on the command
	// line: a receipt records no value to compare.
	overridden bool

	// notes holds the note lines found for each snapshot tree, read once
	// per tree; tasks running in parallel share it.
	mu    sync.Mutex
	notes map[string][][]byte
}

// newReuser prepares reuse for a run, or returns nil when --reuse-key was not
// given. receiptsOn says whether the Runefile sets receipts, and signer is
// the run's receipt signer. A problem is a usage error, found before any
// task runs.
func newReuser(opts Options, receiptsOn bool, signer *receiptSigner, overridden bool) (*reuser, error) {
	if len(opts.ReuseKeys) == 0 {
		return nil, nil
	}
	// A receipt's files hold the Runefile that wrote it, so without the
	// setting here no receipt can match.
	if !receiptsOn || signer == nil {
		return nil, usagef(`--reuse-key: the Runefile does not set receipts, so no receipt can be reused; add "set receipts"`)
	}
	if signer.repo == nil {
		return nil, usagef("--reuse-key: no git repository could be opened for the Runefile, so no receipt can match its files")
	}
	keys, err := publicKeyFiles("--reuse-key", opts.ReuseKeys)
	if err != nil {
		return nil, err
	}
	maxAge := opts.ReuseMaxAge
	if maxAge <= 0 {
		maxAge = DefaultReuseMaxAge
	}
	return &reuser{keys: keys, maxAge: maxAge, overridden: overridden}, nil
}

// noteLines are the lines of the receipts notes of every commit whose tree
// is tree, read once per tree. A failure to read them is a warning, and
// the local receipts still count.
func (u *reuser) noteLines(ctx context.Context, repo *receipt.Repo, tree string, warn func(string, ...any)) [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	if lines, ok := u.notes[tree]; ok {
		return lines
	}
	lines, err := repo.NotesForTree(ctx, tree)
	if err != nil {
		warn("receipt notes not read: %v", err)
	}
	if u.notes == nil {
		u.notes = map[string][][]byte{}
	}
	u.notes[tree] = lines
	return lines
}

// tryReuse reports whether a trusted receipt stands in for the body of task,
// which is about to run as run, and prints why or why not. inputs are the
// declared [cache] inputs the cache listed, and inputsErr its failure to
// list them. When it reuses, run is marked as a cache hit of that receipt.
func (e *engine) tryReuse(task *ast.Task, params eval.Args, run *receiptRun, inputs []string, inputsErr error) bool {
	u := e.reuse
	if u == nil || run == nil || task.Attr(ast.AttrReuse) == nil {
		return false
	}
	// The receipt could not say whether git ignores an input.
	if inputsErr != nil {
		e.notReused(task, "the declared inputs could not be listed: "+inputsErr.Error())
		return false
	}
	// The body's own snapshot, so a run that is not reused takes no other.
	e.takeBefore(run)
	switch {
	case run.snapErr != nil:
		e.notReused(task, "the files could not be snapshotted: "+run.snapErr.Error())
		return false
	case run.stale:
		e.notReused(task, "the Runefile changed after it was loaded")
		return false
	}
	s := e.receipts.signer
	mask := e.maskFunc()
	// The snapshot does not hold an ignored or outside file, so a receipt of
	// the same snapshot says nothing about what this run would read.
	if i := slices.IndexFunc(s.runefiles, func(f receipt.RunefileEntry) bool { return !f.Covered }); i >= 0 {
		e.notReused(task, "the Runefile or an import is outside the repository or ignored: "+s.runefiles[i].Path)
		return false
	}
	switch uncovered, err := e.uncoveredInputs(inputs, mask); {
	case err != nil:
		e.notReused(task, "the declared inputs could not be checked: "+err.Error())
		return false
	case len(uncovered) > 0:
		e.notReused(task, fmt.Sprintf("%d declared inputs are ignored by git", len(uncovered)))
		return false
	}
	stored, err := receipt.List(s.dir)
	if err != nil {
		e.notReused(task, err.Error())
		return false
	}
	cands := make([]receipt.Candidate, 0, len(stored))
	for _, st := range stored {
		cands = append(cands, receipt.Candidate{Line: st.Data, Origin: receipt.OriginLocal, Name: st.Name})
	}
	for _, line := range u.noteLines(e.ctx, s.repo, run.before, e.Warnf) {
		cands = append(cands, receipt.Candidate{Line: line, Origin: receipt.OriginNote, Name: "note"})
	}
	now := time.Now
	if e.clock != nil {
		now = e.clock
	}
	want := receipt.Want{
		Project:    s.project,
		Task:       task.Name,
		Tree:       run.before,
		Rune:       s.rune,
		Definition: definitionDigest(task, e.executorID(task)),
		Overridden: u.overridden,
		Now:        now(),
		MaxAge:     u.maxAge,
	}
	want.Arguments, want.ArgumentsMasked = receiptArgs(task, params, mask)
	if want.Outputs, _, err = e.currentOutputs(task, params, mask); err != nil {
		e.notReused(task, "the declared outputs could not be read: "+err.Error())
		return false
	}
	v := receipt.Reuse(cands, u.keys, want)
	if !v.Verified {
		e.notReused(task, reuseReasonText(v, want.Now, u.maxAge))
		return false
	}
	run.cached, run.inputs, run.related.Reuses = true, inputs, v.Receipt.ID
	// Dimmed like a cache hit: the decision is visible, real output stands out.
	fmt.Fprintf(e.opts.Stderr, "%s\n", e.theme.Muted.Render(fmt.Sprintf("reused: %s (receipt %s, signed by %s)", task.Name, v.Receipt.ID, v.Receipt.Signer)))
	return true
}

// notReused says why a [reuse] task runs.
func (e *engine) notReused(task *ast.Task, why string) {
	fmt.Fprintf(e.opts.Stderr, "%s\n", e.theme.Muted.Render(fmt.Sprintf("not reused: %s (%s)", task.Name, why)))
}

// reuseReasonText says why no receipt stands in for a run
// (contracts/cli.md). Reasons rune verify knows read as verify says them.
func reuseReasonText(v receipt.Verdict, now time.Time, maxAge time.Duration) string {
	switch v.Reason {
	case receipt.ReasonNoReceipt:
		return "no receipt for these files"
	case receipt.ReasonStale:
		if v.Receipt.FinishedAt.After(now) {
			return "the receipt finished in the future"
		}
		return "the receipt is older than " + maxAge.String()
	case receipt.ReasonOtherPlatform:
		return "it ran on another OS or architecture"
	case receipt.ReasonOtherRune:
		return "it ran with another rune"
	case receipt.ReasonOtherDefinition:
		return "the task's definition changed"
	case receipt.ReasonMaskedArguments:
		return "masked arguments cannot be compared"
	case receipt.ReasonOtherArguments:
		return "it ran with other arguments"
	case receipt.ReasonOverridden:
		return "a variable was overridden on the command line"
	case receipt.ReasonOutputsDiffer:
		return "the declared outputs are not the ones it made"
	case receipt.ReasonFailureHook:
		return "it ran as a failure hook"
	}
	return reasonText(v, "these")
}
