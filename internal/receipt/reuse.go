package receipt

import (
	"crypto/ed25519"
	"slices"
	"time"
)

// The reasons a receipt is not reused, beyond those of Evaluate (spec 061),
// in the order they are checked.
const (
	ReasonFailureHook     = "failure-hook"
	ReasonStale           = "stale"
	ReasonOtherPlatform   = "other-platform"
	ReasonOtherRune       = "other-rune"
	ReasonOtherDefinition = "other-definition"
	ReasonMaskedArguments = "masked-arguments"
	ReasonOtherArguments  = "other-arguments"
	ReasonOverridden      = "overridden"
	ReasonOutputsDiffer   = "outputs-differ"
)

// Want is what a receipt must match to be reused for one run of a task.
type Want struct {
	Project    string // the Runefile's path from the top of the repository
	Task       string
	Tree       string // the snapshot the body would run on
	Rune       RuneInfo
	Definition string
	Arguments  []Arg
	// ArgumentsMasked and Overridden describe the run that wants to reuse:
	// masked arguments cannot be compared, and an override is not recorded
	// by value.
	ArgumentsMasked bool
	Overridden      bool
	Outputs         *Outputs // the declared outputs now; nil when there are none
	Now             time.Time
	MaxAge          time.Duration
}

// Reuse picks the receipt that lets a run of want.Task be skipped: signed by
// a trusted key, of a pass on exactly want.Tree, recent, and recorded in the
// same context. Without one, the verdict has the reason of the most relevant
// candidate, as Evaluate's does. A candidate that is not a receipt is passed
// over.
func Reuse(cands []Candidate, trusted []ed25519.PublicKey, want Want) Verdict {
	byTask, _ := judgeAll(cands, trusted, want.Project, "", want.Tree)
	js := byTask[want.Task]
	for i := range js {
		if js[i].reason == "" {
			js[i].reason = reuseReason(js[i].r, want)
		}
	}
	return verdictOf(want.Task, js)
}

// reuseReason is why a receipt that is evidence for want.Tree may still not
// stand in for this run, or "" when it may.
func reuseReason(r Receipt, want Want) string {
	switch {
	// A failure hook's body ran with the failed run's variables, so its
	// result says nothing about a run of the task itself.
	case r.Role != RoleTask:
		return ReasonFailureHook
	case r.FinishedAt.Before(want.Now.Add(-want.MaxAge)) || r.FinishedAt.After(want.Now):
		return ReasonStale
	case r.Rune.OS != want.Rune.OS || r.Rune.Arch != want.Rune.Arch:
		return ReasonOtherPlatform
	case r.Rune.Version != want.Rune.Version || r.Rune.Commit != want.Rune.Commit:
		return ReasonOtherRune
	case r.Definition != want.Definition:
		return ReasonOtherDefinition
	case r.ArgumentsRedacted || want.ArgumentsMasked:
		return ReasonMaskedArguments
	case !slices.EqualFunc(r.Arguments, want.Arguments, sameArg):
		return ReasonOtherArguments
	case len(r.Overrides) > 0 || want.Overridden:
		return ReasonOverridden
	case outputsDigest(r.Outputs) != outputsDigest(want.Outputs):
		return ReasonOutputsDiffer
	}
	return ""
}

func sameArg(a, b Arg) bool { return a.Name == b.Name && slices.Equal(a.Values, b.Values) }

// outputsDigest binds the whole list of output files, inline or in a
// manifest, so the list itself is not needed to compare.
func outputsDigest(o *Outputs) string {
	if o == nil {
		return ""
	}
	return o.Digest
}
