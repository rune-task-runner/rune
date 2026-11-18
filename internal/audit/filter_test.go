package audit

import (
	"testing"
	"time"
)

func TestFilterMatch(t *testing.T) {
	at := testDay
	call := Action{
		Type: TypeCall, ID: "aa", Start: at, Outcome: OutcomeFailed,
		Tool: "mod__deploy", Task: "mod::deploy", Session: "5f0e7a11c3d2b694",
		Client: &Client{Name: "Claude-Code"},
	}
	request := Action{Type: TypeRequest, ID: "rr", Start: at, Outcome: OutcomeOK, Session: "5f0e7a11c3d2b694"}
	authFailure := Action{Type: TypeAuthFailure, ID: "ff", Start: at}
	agentRun := Action{Type: TypeAgentRun, ID: "gg", Start: at, Task: "review", Outcome: OutcomeCompleted}

	tests := []struct {
		name   string
		filter Filter
		action Action
		want   bool
	}{
		{"zero filter takes a call", Filter{}, call, true},
		{"zero filter drops a request", Filter{}, request, false},
		{"all takes a request", Filter{All: true}, request, true},
		{"zero filter takes an authentication failure", Filter{}, authFailure, true},
		{"since is inclusive", Filter{Since: at}, call, true},
		{"since after the start", Filter{Since: at.Add(time.Nanosecond)}, call, false},
		{"until is exclusive", Filter{Until: at}, call, false},
		{"until after the start", Filter{Until: at.Add(time.Nanosecond)}, call, true},
		{"task by task name", Filter{Tasks: []string{"x", "mod::deploy"}}, call, true},
		{"task by tool name", Filter{Tasks: []string{"mod__deploy"}}, call, true},
		{"another task", Filter{Tasks: []string{"deploy"}}, call, false},
		{"task of an agent run", Filter{Tasks: []string{"review"}}, agentRun, true},
		{"task filter drops an action without one", Filter{Tasks: []string{"review"}}, authFailure, false},
		{"outcome", Filter{Outcomes: []string{OutcomeRefused, OutcomeFailed}}, call, true},
		{"another outcome", Filter{Outcomes: []string{OutcomeCompleted}}, call, false},
		{"outcome filter drops an action without one", Filter{Outcomes: []string{OutcomeFailed}}, authFailure, false},
		{"client ignores case", Filter{Clients: []string{"claude-code"}}, call, true},
		{"another client", Filter{Clients: []string{"codex"}}, call, false},
		{"client filter drops an action without one", Filter{Clients: []string{"claude-code"}}, agentRun, false},
		{"session id", Filter{Sessions: []string{"5f0e7a11c3d2b694"}}, call, true},
		{"session prefix", Filter{Sessions: []string{"5f0e"}}, call, true},
		{"another session", Filter{Sessions: []string{"aaaa"}}, call, false},
		{"an empty session value matches nothing", Filter{Sessions: []string{""}}, call, false},
		{"session filter drops an action without one", Filter{Sessions: []string{"5f0e"}}, agentRun, false},
		{"two filters, both match", Filter{Tasks: []string{"mod::deploy"}, Outcomes: []string{OutcomeFailed}}, call, true},
		{"two filters, one fails", Filter{Tasks: []string{"mod::deploy"}, Outcomes: []string{OutcomeCompleted}}, call, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Match(tt.action); got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
}
