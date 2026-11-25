package tasksuggest

import (
	"slices"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/observe"
)

func rec(id, event string, at time.Time) observe.Record {
	return observe.Record{V: 1, Time: at, Session: "s1", ToolUse: id, Event: event, Command: "go test ./..."}
}

func TestExecutions(t *testing.T) {
	two := 2
	t.Run("joined in either order", func(t *testing.T) {
		got := Executions([]observe.Record{
			rec("a", observe.KindSuccess, t0.Add(time.Second)),
			rec("b", observe.KindPre, t0),
			rec("a", observe.KindPre, t0),
			rec("b", observe.KindSuccess, t0.Add(2*time.Second)),
		})
		if len(got) != 2 || got[0].ToolUse != "a" || got[1].ToolUse != "b" {
			t.Fatalf("got %+v", got)
		}
		for _, e := range got {
			if e.Outcome != Succeeded {
				t.Errorf("%s: %v", e.ToolUse, e.Outcome)
			}
		}
		if !got[1].Last.Equal(t0.Add(2 * time.Second)) {
			t.Errorf("last %v", got[1].Last)
		}
	})
	t.Run("a repeated event counts once", func(t *testing.T) {
		got := Executions([]observe.Record{rec("a", observe.KindSuccess, t0), rec("a", observe.KindSuccess, t0)})
		if len(got) != 1 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("outcomes", func(t *testing.T) {
		failure := rec("f", observe.KindFailure, t0)
		failure.Exit = &two
		noExit := rec("n", observe.KindFailure, t0)
		interrupt := rec("i", observe.KindFailure, t0)
		interrupt.Interrupted = true
		background := rec("b", observe.KindSuccess, t0)
		background.Background = true
		both := rec("x", observe.KindFailure, t0)
		bothInterrupt := rec("y", observe.KindFailure, t0)
		bothInterrupt.Interrupted = true
		got := Executions([]observe.Record{
			failure, noExit, interrupt, background, rec("p", observe.KindPre, t0),
			rec("x", observe.KindSuccess, t0), both, rec("y", observe.KindSuccess, t0), bothInterrupt,
		})
		want := map[string]Outcome{"f": Failed, "n": Failed, "i": Interrupted, "b": Started, "p": Attempt, "x": Failed, "y": Interrupted}
		for _, e := range got {
			if e.Outcome != want[e.ToolUse] {
				t.Errorf("%s: %v, want %v", e.ToolUse, e.Outcome, want[e.ToolUse])
			}
		}
		if got[0].Exit == nil || *got[0].Exit != 2 || got[1].Exit != nil {
			t.Errorf("exit codes %v %v", got[0].Exit, got[1].Exit)
		}
	})
	t.Run("the outcome's record says what ran", func(t *testing.T) {
		pre := rec("a", observe.KindPre, t0)
		pre.Command, pre.Cwd, pre.Description = "old", "x", "Run it"
		post := rec("a", observe.KindSuccess, t0)
		post.Command, post.Cwd = "new", "web"
		got := Executions([]observe.Record{pre, post})
		if got[0].Command != "new" || got[0].Cwd != "web" || got[0].Description != "Run it" {
			t.Errorf("got %+v", got[0])
		}
	})
	t.Run("the 14-run set", func(t *testing.T) {
		got := Executions(records(times(14, call{cmd: "go test -run TestX ./pkg/..."})...))
		ids := make([]string, len(got))
		for i, e := range got {
			ids[i] = e.ToolUse
		}
		if len(got) != 14 || len(slices.Compact(ids)) != 14 {
			t.Errorf("%d executions", len(got))
		}
	})
}
