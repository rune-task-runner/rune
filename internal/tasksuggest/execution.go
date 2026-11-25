package tasksuggest

import (
	"time"

	"github.com/rune-task-runner/rune/internal/observe"
)

// Outcome is how one call of a command ended, as far as the records say.
type Outcome int

const (
	// Attempt: only the event before the call was recorded. The call was
	// denied, blocked or is still running; it is not a run.
	Attempt Outcome = iota
	// Succeeded: the call succeeded.
	Succeeded
	// Started: a background command started; how it ended is not known.
	Started
	// Interrupted: the call was interrupted.
	Interrupted
	// Failed: the call failed, with its exit code when the client gave one.
	Failed
)

// Execution is one call of a command: the records that share a tool-use id.
type Execution struct {
	ToolUse     string
	Session     string
	Cwd         string
	Command     string
	Description string
	Outcome     Outcome
	Exit        *int
	Last        time.Time
}

// Executions joins the records of each call, in the order the calls first
// appear (research R8). The first record of each kind counts; a repeated
// delivery of an event adds nothing.
func Executions(recs []observe.Record) []Execution {
	type kinds struct{ pre, success, failure *observe.Record }
	byID := map[string]*kinds{}
	var order []string
	last := map[string]time.Time{}
	for i := range recs {
		r := &recs[i]
		k := byID[r.ToolUse]
		if k == nil {
			k = &kinds{}
			byID[r.ToolUse] = k
			order = append(order, r.ToolUse)
		}
		switch r.Event {
		case observe.KindPre:
			k.pre = cmpFirst(k.pre, r)
		case observe.KindSuccess:
			k.success = cmpFirst(k.success, r)
		case observe.KindFailure:
			k.failure = cmpFirst(k.failure, r)
		}
		if r.Time.After(last[r.ToolUse]) {
			last[r.ToolUse] = r.Time
		}
	}
	out := make([]Execution, 0, len(order))
	for _, id := range order {
		k := byID[id]
		e := Execution{ToolUse: id, Last: last[id]}
		switch {
		case k.failure != nil && k.failure.Interrupted:
			e.Outcome = Interrupted
		case k.failure != nil:
			e.Outcome, e.Exit = Failed, k.failure.Exit
		case k.success != nil && k.success.Background:
			e.Outcome = Started
		case k.success != nil:
			e.Outcome = Succeeded
		}
		// The outcome's record says what ran; the one before the call
		// fills in what it lacks.
		base := k.pre
		for _, r := range []*observe.Record{k.success, k.failure} {
			if r != nil {
				base = r
			}
		}
		e.Session, e.Cwd, e.Command, e.Description = base.Session, base.Cwd, base.Command, base.Description
		if e.Description == "" && k.pre != nil {
			e.Description = k.pre.Description
		}
		out = append(out, e)
	}
	return out
}

func cmpFirst(have, r *observe.Record) *observe.Record {
	if have != nil {
		return have
	}
	return r
}
