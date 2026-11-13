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
// t is the descriptor the handler registered, so the check costs no Tasks()
// rebuild. The texts carry task names, not tool names, so they can be pasted
// into the flag they name. They say to add the names, because restarting with
// only the refused names would drop the existing grants.
func (s *Server) refusal(t TaskInfo) string {
	// One list, the called task first, so a single restart permits the call.
	var named []string
	own := t.Destructive && !s.granted[t.Name]
	if own {
		named = append(named, t.Name)
	}
	// An embedder's list may repeat a name or include t itself, and each task
	// is still named once (FR-005).
	var reached []string
	for _, name := range t.DestructiveReach {
		if name != t.Name && !s.granted[name] && !slices.Contains(reached, name) {
			reached = append(reached, name)
		}
	}
	named = append(named, reached...)
	if len(named) == 0 {
		return ""
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
