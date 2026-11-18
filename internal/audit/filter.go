package audit

import (
	"slices"
	"strings"
	"time"
)

// Filter selects actions. A zero field selects everything. An action that
// lacks the field a filter names does not match that filter.
type Filter struct {
	// Since and Until bound the start time: at or after Since, before Until.
	Since, Until time.Time
	// Tasks match the task name or the tool name.
	Tasks []string
	// Outcomes match the outcome.
	Outcomes []string
	// Clients match the client name, ignoring case.
	Clients []string
	// Sessions match the session id or a prefix of it.
	Sessions []string
	// All also selects the requests that are not tool calls.
	All bool
}

// Match reports whether a passes every part of the filter.
func (f Filter) Match(a Action) bool {
	if !f.All && a.Type == TypeRequest {
		return false
	}
	if !f.Since.IsZero() && a.Start.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !a.Start.Before(f.Until) {
		return false
	}
	if len(f.Tasks) > 0 && !f.matchTask(a) {
		return false
	}
	if len(f.Outcomes) > 0 && (a.Outcome == "" || !slices.Contains(f.Outcomes, a.Outcome)) {
		return false
	}
	if len(f.Clients) > 0 && !f.matchClient(a) {
		return false
	}
	return len(f.Sessions) == 0 || f.matchSession(a)
}

func (f Filter) matchTask(a Action) bool {
	return (a.Task != "" && slices.Contains(f.Tasks, a.Task)) ||
		(a.Tool != "" && slices.Contains(f.Tasks, a.Tool))
}

func (f Filter) matchClient(a Action) bool {
	if a.Client == nil || a.Client.Name == "" {
		return false
	}
	return slices.ContainsFunc(f.Clients, func(name string) bool {
		return strings.EqualFold(name, a.Client.Name)
	})
}

func (f Filter) matchSession(a Action) bool {
	if a.Session == "" {
		return false
	}
	return slices.ContainsFunc(f.Sessions, func(id string) bool {
		return id != "" && strings.HasPrefix(a.Session, id)
	})
}
