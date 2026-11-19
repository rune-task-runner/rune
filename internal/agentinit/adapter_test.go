package agentinit

import (
	"slices"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct{ output, want string }{
		{"2.1.326 (Claude Code)\n", "2.1.326"},
		{"codex-cli 0.169.0\n", "0.169.0"},
		{"1.146.0\n6e68549\nx64\n", "1.146.0"},
		{"codex-cli 0.169.0-alpha.2\n", "0.169.0-alpha.2"},
		{"1.147.0-insider\n6e68549\n", "1.147.0-insider"},
		{"2.1.326- (Claude Code)", "2.1.326"},
		{"version 12", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ParseVersion(tt.output); got != tt.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestIsChecked(t *testing.T) {
	a := adapter{checked: []string{"0.167.0", "0.169.0", "not a version"}}
	tests := []struct {
		version string
		want    bool
	}{
		{"0.167.0", true},
		{"0.169.0", true},
		{"0.168.0", false}, // between two checked versions: nobody ran it
		{"0.166.9", false},
		{"0.170.0", false},
		{"0.169.0-alpha.2", false}, // a pre-release of a checked version is another build
		{"", false},
		{"not a version", false},
	}
	for _, tt := range tests {
		if got := a.isChecked(tt.version); got != tt.want {
			t.Errorf("isChecked(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestCheckedText(t *testing.T) {
	tests := []struct {
		checked []string
		want    string
	}{
		{nil, "none"},
		{[]string{"2.1.325"}, "2.1.325"},
		{[]string{"2.1.325", "2.1.326"}, "2.1.325, 2.1.326"},
	}
	for _, tt := range tests {
		if got := (adapter{checked: tt.checked}).checkedText(); got != tt.want {
			t.Errorf("checkedText(%v) = %q, want %q", tt.checked, got, tt.want)
		}
	}
}

func TestOutcome(t *testing.T) {
	claude, _ := adapterOf(ClientClaude)
	codex, _ := adapterOf(ClientCodex)
	vscode, _ := adapterOf(ClientVSCode)
	tests := []struct {
		name    string
		a       adapter
		version string
		kind    OutcomeKind
		scope   string
		reason  string
		remains string
		steps   int
	}{
		{"claude, checked", claude, claude.checked[0], RuneOnly, "", "", "", 2},
		{"codex, checked", codex, codex.checked[0], RuneOnly, "", "", "", 1},
		{
			"claude, not checked", claude, "9.9.9", RegisteredOnly, "", "this version was not checked",
			"Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger (not checked for this version)", 2,
		},
		{
			"codex, no version", codex, "", RegisteredOnly, "", "no version was found",
			"exec_command, write_stdin, and the subagent tools spawn_agent, wait_agent, send_input, resume_agent, close_agent, followup_task, interrupt_agent, list_agents, send_message (not checked for this version)", 1,
		},
		{
			"vscode, a version", vscode, "1.146.0", RegisteredOnly, "", "no VS Code version has been checked with these files",
			`runInTerminal, runTask, createAndRunTask, runNotebookCell, runSubagent, vscode/runCommand (kept by every agent but "rune", and not checked for it)`, 2,
		},
		{"vscode, no version", vscode, "", RegisteredOnly, "", "no VS Code version has been checked with these files", vscode.remains, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.outcome(tt.version, "'/my proj'")
			if got.Kind != tt.kind || got.Scope != tt.scope || got.Reason != tt.reason || got.Remains != tt.remains || len(got.Steps) != tt.steps {
				t.Errorf("got %+v", got)
			}
			if !strings.Contains(got.Steps[0], "'/my proj'") {
				t.Errorf("the first step does not name the directory: %q", got.Steps[0])
			}
		})
	}

	// Once a VS Code version is checked, the outcome is the agent's scope.
	vscode.checked = []string{"1.146.0"}
	if got := vscode.outcome("1.146.0", "/p"); got.Kind != RuneOnlyWithin || got.Scope != `the custom agent "rune"` || got.Reason != "" {
		t.Errorf("checked VS Code: %+v", got)
	}
}

func TestAdapterOf(t *testing.T) {
	if _, ok := adapterOf("zed"); ok {
		t.Error("zed has an adapter")
	}
	for _, c := range []Client{ClientClaude, ClientCodex, ClientVSCode} {
		a, ok := adapterOf(c)
		if !ok || a.name == "" || a.binary == "" || len(a.paths) == 0 || len(a.check) == 0 || a.plan == nil || a.steps == nil {
			t.Errorf("adapter of %s is incomplete: %+v", c, a)
		}
	}
	// The VS Code adapter claims no version: nothing about it was run.
	if a, _ := adapterOf(ClientVSCode); len(a.checked) != 0 {
		t.Errorf("VS Code lists checked versions %v with no recorded run", a.checked)
	}
	if got := claudeDenied(); !slices.Equal(got, []string{"Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"}) {
		t.Errorf("claudeDenied() = %v", got)
	}
}
