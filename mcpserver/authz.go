package mcpserver

import (
	"fmt"
	"slices"
	"strings"
)

// refusal returns why a task may not be CALLED in this session, or "" when it
// may (Q3/FR-028, spec 031). Private tasks and tasks the operator's AllowList
// and DenyList hide are never registered as tools, so they never reach here
// (spec 041). The refusals, checked in this order:
//   - a destructive ([confirm]) task must be named in AllowDestructive. No
//     value grants every destructive task;
//   - every destructive task in t.DestructiveReach must be granted too, so a
//     call never runs part of its work and then stops at one (spec 034).
//
// t is the descriptor in the snapshot the call runs against, so the check
// costs no Tasks() rebuild. The texts carry task names, not tool names, so they can be pasted
// into the flag they name. They say to add the names, because restarting with
// only the refused names would drop the existing grants.
func (s *Server) refusal(t TaskInfo) string {
	named := s.ungranted(t)
	if len(named) == 0 {
		return ""
	}
	own := named[0] == t.Name
	reached := named
	if own {
		reached = named[1:]
	}
	var subject string
	switch {
	case own && len(reached) == 0:
		if s.opts.AllowDestructiveFlag == "" {
			return fmt.Sprintf("task %q is destructive and is not allowed in this session", t.Name)
		}
		subject = fmt.Sprintf("task %q is destructive", t.Name)
	case own:
		subject = fmt.Sprintf("task %q is destructive and can run %s", t.Name, destructiveTasks(reached))
	default:
		subject = fmt.Sprintf("task %q can run %s", t.Name, destructiveTasks(reached))
	}
	pronoun, verbPhrase := "it", "it is"
	if len(named) > 1 {
		pronoun, verbPhrase = "them", "they are"
	}
	if s.opts.AllowDestructiveFlag == "" {
		return subject + "; " + verbPhrase + " not allowed in this session"
	}
	return fmt.Sprintf("%s; to permit %s, add %s to %s and restart the server",
		subject, pronoun, strings.Join(named, ","), s.opts.AllowDestructiveFlag)
}

// ungranted lists the destructive tasks a call to t can run that the operator
// has not granted: t itself first, so a single restart permits the call, then
// what it reaches, in the descriptor's order. An embedder's list may repeat a
// name or include t itself, and each task is still named once (spec 034
// FR-005). A refusal names these tasks, and an approval covers exactly them
// (spec 049).
func (s *Server) ungranted(t TaskInfo) []string {
	var named []string
	if t.Destructive && !s.granted[t.Name] {
		named = append(named, t.Name)
	}
	for _, name := range t.DestructiveReach {
		if name != t.Name && !s.granted[name] && !slices.Contains(named, name) {
			named = append(named, name)
		}
	}
	return named
}

// destructiveTasks names reached tasks for a refusal's subject:
// `destructive task "a"` or `destructive tasks "a", "b"`.
func destructiveTasks(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	if len(names) == 1 {
		return "destructive task " + quoted[0]
	}
	return "destructive tasks " + strings.Join(quoted, ", ")
}

// planNames lists the tasks an approval of a call to a task that applies a
// plan covers: the task itself first, granted or not, then the destructive
// tasks it reaches that the operator has not granted (spec 051).
func planNames(task string, ungranted []string) []string {
	names := []string{task}
	for _, name := range ungranted {
		if name != task {
			names = append(names, name)
		}
	}
	return names
}

// planNeeds is the start of every refusal of a task that applies a plan. It
// never names the grant flag: a grant does not approve a plan.
func planNeeds(task string) string {
	return fmt.Sprintf("task %q applies a plan and runs only when the person at the client approves that plan", task)
}

// operatorCan is the sentence that names the command for the called task,
// or "" when the server has none.
func (s *Server) operatorCan(called string) string {
	if s.opts.RunCommand == "" {
		return ""
	}
	return " The operator can run " + s.runHint(called) + " themselves."
}

// planNobody refuses a task that applies a plan on a server or engine that
// asks nobody.
func (s *Server) planNobody(called string) string {
	return planNeeds(called) + "; nobody can be asked in this session." + s.operatorCan(called)
}

// planCannotAsk refuses a task that applies a plan when this client, or
// this request, cannot be put to the person.
func (s *Server) planCannotAsk(called, reason string) string {
	return planNeeds(called) + "." + s.operatorCan(called) + " Rune could not ask for approval here: " + reason + "."
}
