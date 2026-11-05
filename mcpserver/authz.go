package mcpserver

import (
	"fmt"
	"slices"
)

// refusal returns why a task may not be CALLED in this session, or "" when it
// may (Q3/FR-028, spec 031). Private tasks are never registered as tools, so
// they never reach here. Two refusals exist, checked in this order:
//   - an operator AllowList, when set, narrows callable tasks;
//   - a destructive ([confirm]) task must be named in AllowDestructive. No
//     value grants every destructive task.
//
// Both texts carry the task name, not the tool name, so the destructive one
// can be pasted into the flag it names. It says to add the name, because
// restarting with only the refused name would drop the existing grants.
func (s *Server) refusal(taskName string) string {
	if len(s.opts.AllowList) > 0 && !slices.Contains(s.opts.AllowList, taskName) {
		return fmt.Sprintf("task %q is not authorized for this session", taskName)
	}
	if s.isDestructive(taskName) && !s.granted[taskName] {
		if s.opts.AllowDestructiveFlag == "" {
			return fmt.Sprintf("task %q is destructive and is not allowed in this session", taskName)
		}
		return fmt.Sprintf("task %q is destructive; to permit it, add %s to %s and restart the server",
			taskName, taskName, s.opts.AllowDestructiveFlag)
	}
	return ""
}

// isDestructive looks up whether the named task is marked destructive.
func (s *Server) isDestructive(taskName string) bool {
	for _, t := range s.engine.Tasks() {
		if t.Name == taskName {
			return t.Destructive
		}
	}
	return false
}
