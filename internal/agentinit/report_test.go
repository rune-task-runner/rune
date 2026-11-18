package agentinit

import (
	"regexp"
	"strings"
	"testing"
)

func planFor(t *testing.T, client Client, in Inputs) *Result {
	t.Helper()
	if in.Runefile == "" {
		in.Runefile, in.ShownRunefile, in.ShownDir = "/p/Runefile", "/p/Runefile", "/p"
	}
	res, err := Plan(client, in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReportLineOrder(t *testing.T) {
	res := planFor(t, ClientClaude, Inputs{Version: "9.9.9", Files: map[string][]byte{
		claudeSettingsPath: []byte(`{"permissions": {"ask": ["mcp__rune__clean"]}}`),
	}})
	var kinds []string
	for line := range strings.Lines(Report(res, true)) {
		kind, _, _ := strings.Cut(line, ":")
		if len(kinds) == 0 || kinds[len(kinds)-1] != kind {
			kinds = append(kinds, kind)
		}
	}
	want := ".mcp.json .claude/settings.json client outcome reason remains step note check dry run"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("line kinds = %q, want %q", got, want)
	}
}

func TestReportForms(t *testing.T) {
	tests := []struct {
		name   string
		client Client
		in     Inputs
		dryRun bool
		has    []string
		hasNot []string
	}{
		{
			"claude, checked", ClientClaude,
			Inputs{Version: "2.1.325"},
			false,
			[]string{
				".mcp.json: created: server \"rune\" runs: rune mcp --file /p/Runefile --no-reload\n",
				"client: Claude Code 2.1.325 (checked: 2.1.325",
				"outcome: Rune-only, pending 2 steps\n",
				"step: trust the project: start claude in /p and accept",
				"note: [confirm] tasks are not approved by this setup.",
				"note: a task an agent calls runs in Rune's sandbox, which needs bubblewrap on Linux.",
				"check: claude -p hi --output-format stream-json --verbose\n",
			},
			[]string{"reason:", "remains:", "dry run"},
		},
		{
			"codex, checked, dry run", ClientCodex,
			Inputs{Version: "0.167.0"},
			true,
			[]string{"client: Codex CLI 0.167.0 (checked: 0.167.0", "outcome: Rune-only, pending 1 step\n", "dry run: nothing written\n"},
			[]string{"pending 1 steps", "reason:"},
		},
		{
			"codex, no version", ClientCodex,
			Inputs{},
			false,
			[]string{
				"client: Codex CLI, no version found (checked: 0.167.0",
				"outcome: Registered only, pending 1 step\n",
				"reason: no version was found\n",
				"remains: exec_command, write_stdin,",
			},
			nil,
		},
		{
			"vscode", ClientVSCode,
			Inputs{Version: "1.146.0"},
			false,
			[]string{
				"client: VS Code 1.146.0 (checked: none)\n",
				"outcome: Registered only, pending 2 steps\n",
				"reason: no VS Code version has been checked with these files\n",
				".github/agents/rune.agent.md: created: custom agent \"rune\" with tools rune/*\n",
				"step: choose the agent \"rune\" in the Chat view. Every other agent keeps the terminal.\n",
			},
			nil,
		},
		{
			"unchanged files", ClientCodex,
			Inputs{Version: "0.167.0", Files: map[string][]byte{
				codexConfigPath: planFor(t, ClientCodex, Inputs{}).Files[0].New,
			}},
			false,
			[]string{".codex/config.toml: unchanged\n"},
			[]string{"unchanged:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Report(planFor(t, tt.client, tt.in), tt.dryRun)
			for _, s := range tt.has {
				if !strings.Contains(got, s) {
					t.Errorf("the report lacks %q:\n%s", s, got)
				}
			}
			for _, s := range tt.hasNot {
				if strings.Contains(got, s) {
					t.Errorf("the report has %q:\n%s", s, got)
				}
			}
		})
	}
}

func TestOutcomeText(t *testing.T) {
	tests := []struct {
		out  Outcome
		want string
	}{
		{Outcome{Kind: RuneOnly}, "Rune-only"},
		{Outcome{Kind: RuneOnlyWithin, Scope: `the custom agent "rune"`}, `Rune-only within the custom agent "rune"`},
		{Outcome{Kind: RegisteredOnly}, "Registered only"},
	}
	for _, tt := range tests {
		if got := outcomeText(tt.out); got != tt.want {
			t.Errorf("outcomeText(%+v) = %q, want %q", tt.out, got, tt.want)
		}
	}
}

// The setup outcome contract of spec 047 allows three outcomes and no other
// word for a setup.
func TestReportsUseNoOtherWordForASetup(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(restricted|locked down|safe)\b`)
	for _, client := range []Client{ClientClaude, ClientCodex, ClientVSCode} {
		a, _ := adapterOf(client)
		versions := append([]string{"", "9.9.9"}, a.checked...)
		for _, version := range versions {
			res := planFor(t, client, Inputs{Version: version})
			check, _ := CheckReport(res)
			for _, text := range []string{Report(res, false), check} {
				if word := forbidden.FindString(text); word != "" {
					t.Errorf("%s %q: the report says %q:\n%s", client, version, word, text)
				}
			}
		}
	}
}

func TestCheckReport(t *testing.T) {
	never := planFor(t, ClientCodex, Inputs{})
	text, complete := CheckReport(never)
	want := ".codex/config.toml: features.shell_tool: missing\n" +
		".codex/config.toml: agents.enabled: missing\n" +
		".codex/config.toml: mcp_servers.rune.command: missing\n" +
		".codex/config.toml: mcp_servers.rune.args: missing\n" +
		"incomplete: 4 of 4 entries are missing or different. Run: rune agent init codex\n" +
		"check: codex exec \"Call the rune tool of one task. Then say whether you have a tool that runs a shell command, and whether you have a tool that starts a subagent.\"\n" +
		"check: Codex prints no tool list, so the answer is the model's: a Rune task ran, and it has neither tool\n"
	if text != want || complete {
		t.Errorf("never set up: complete = %v\n%s\nwant\n%s", complete, text, want)
	}

	done := planFor(t, ClientCodex, Inputs{Files: map[string][]byte{codexConfigPath: never.Files[0].New}})
	text, complete = CheckReport(done)
	if !complete || !strings.Contains(text, "complete: all 4 entries are present\n") || strings.Contains(text, "missing") {
		t.Errorf("complete setup: complete = %v\n%s", complete, text)
	}
	for _, line := range []string{"client:", "outcome:"} {
		if strings.Contains(text, line) {
			t.Errorf("a check names no client and no outcome, but has %q:\n%s", line, text)
		}
	}

	differs := planFor(t, ClientCodex, Inputs{Files: map[string][]byte{
		codexConfigPath: []byte(strings.Replace(string(never.Files[0].New), "shell_tool = false", "shell_tool = true", 1)),
	}})
	text, complete = CheckReport(differs)
	if complete || !strings.Contains(text, "features.shell_tool: differs\n") ||
		!strings.Contains(text, "incomplete: 1 of 4 entries are missing or different.") {
		t.Errorf("another value: complete = %v\n%s", complete, text)
	}

	// Every entry is there, and a deny rule makes the setup impossible.
	claude := planFor(t, ClientClaude, Inputs{})
	denied := strings.Replace(string(claude.Files[1].New), `"Bash"`, `"mcp__rune", "Bash"`, 1)
	blocked := planFor(t, ClientClaude, Inputs{Files: map[string][]byte{
		mcpJSONPath: claude.Files[0].New, claudeSettingsPath: []byte(denied),
	}})
	text, complete = CheckReport(blocked)
	if complete || !strings.Contains(text, "incomplete: the setup has a conflict\n") {
		t.Errorf("deny rule for every tool: complete = %v\n%s", complete, text)
	}

	// A value of the wrong type stops a run too, so the summary names no run,
	// though the entries behind the value are listed as different.
	wrongType := planFor(t, ClientClaude, Inputs{Files: map[string][]byte{mcpJSONPath: []byte(`{"mcpServers": []}`)}})
	text, complete = CheckReport(wrongType)
	if complete || !strings.Contains(text, ".mcp.json: mcpServers.rune.args: differs\n") ||
		!strings.Contains(text, "incomplete: the setup has a conflict\n") || strings.Contains(text, "Run:") {
		t.Errorf("a value of the wrong type: complete = %v\n%s", complete, text)
	}
}

func TestDiff(t *testing.T) {
	res := planFor(t, ClientClaude, Inputs{Files: map[string][]byte{mcpJSONPath: []byte("{}\n")}})
	got := Diff(res)
	for _, s := range []string{"--- a/.mcp.json\n+++ b/.mcp.json\n", "--- /dev/null\n+++ b/.claude/settings.json\n", "+    \"allow\": [\"mcp__rune\"]\n"} {
		if !strings.Contains(got, s) {
			t.Errorf("the difference lacks %q:\n%s", s, got)
		}
	}
	again := planFor(t, ClientClaude, Inputs{Files: map[string][]byte{
		mcpJSONPath: res.Files[0].New, claudeSettingsPath: res.Files[1].New,
	}})
	if got := Diff(again); got != "" {
		t.Errorf("a run that changes nothing has a difference:\n%s", got)
	}
}

// The run a failed check names takes the options of the check: a setup made
// without the path is completed without it.
func TestCheckReportNamesTheOptions(t *testing.T) {
	text, _ := CheckReport(planFor(t, ClientCodex, Inputs{Unpinned: true}))
	if !strings.Contains(text, "Run: rune agent init codex --unpinned\n") {
		t.Errorf("the hint does not carry --unpinned:\n%s", text)
	}
	text, _ = CheckReport(planFor(t, ClientCodex, Inputs{}))
	if !strings.Contains(text, "Run: rune agent init codex\n") {
		t.Errorf("the hint of a pinned check:\n%s", text)
	}
}

// The outcome follows the version that is set after planning.
func TestSetVersion(t *testing.T) {
	res := planFor(t, ClientClaude, Inputs{})
	if res.Outcome.Kind != RegisteredOnly || res.Outcome.Reason != "no version was found" {
		t.Fatalf("with no version: %+v", res.Outcome)
	}
	res.SetVersion("2.1.326")
	if res.Version != "2.1.326" || res.Outcome.Kind != RuneOnly || len(res.Outcome.Steps) != 2 || !strings.Contains(res.Outcome.Steps[0], " in /p ") {
		t.Errorf("after a checked version: %q %+v", res.Version, res.Outcome)
	}
	res.SetVersion("9.9.9")
	if res.Outcome.Kind != RegisteredOnly || res.Outcome.Reason != "this version was not checked" {
		t.Errorf("after an unchecked version: %+v", res.Outcome)
	}
}
