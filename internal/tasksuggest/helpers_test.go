package tasksuggest

import (
	"fmt"
	"time"

	"github.com/rune-task-runner/rune/internal/observe"
)

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// call is one recorded call for a test: the events a hook would have
// recorded for it.
type call struct {
	cmd, cwd, desc, session string
	outcome                 Outcome
	exit                    int
	at                      time.Time
}

// records turns calls into the records of their events, ids t0, t1, ...
// Each call has its pre event, and its outcome event unless it is an
// attempt.
func records(calls ...call) []observe.Record {
	var out []observe.Record
	for i, c := range calls {
		at := c.at
		if at.IsZero() {
			at = t0.Add(time.Duration(i) * time.Minute)
		}
		session := c.session
		if session == "" {
			session = "s1"
		}
		base := observe.Record{V: 1, Time: at, Client: "claude", Session: session, ToolUse: fmt.Sprintf("t%d", i), Cwd: c.cwd, Command: c.cmd, Description: c.desc}
		pre := base
		pre.Event = observe.KindPre
		out = append(out, pre)
		post := base
		switch c.outcome {
		case Attempt:
			continue
		case Succeeded:
			post.Event = observe.KindSuccess
		case Started:
			post.Event, post.Background = observe.KindSuccess, true
		case Failed:
			post.Event = observe.KindFailure
			if c.exit != 0 {
				post.Exit = &c.exit
			}
		case Interrupted:
			post.Event, post.Interrupted = observe.KindFailure, true
		}
		out = append(out, post)
	}
	return out
}

// times repeats a call n times, succeeded unless the call says otherwise.
func times(n int, c call) []call {
	if c.outcome == Attempt {
		c.outcome = Succeeded
	}
	out := make([]call, n)
	for i := range out {
		out[i] = c
	}
	return out
}

func join(lists ...[]call) []call {
	var out []call
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
