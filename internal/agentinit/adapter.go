package agentinit

import (
	"regexp"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/semver"
)

// adapter is what Rune knows about one client. The three differ in data and
// in one planning function.
type adapter struct {
	name   string // as the report names the client
	binary string
	paths  []string
	// checked lists the exact versions with a recorded run that showed
	// Rune's tools and no command-running tool (spec 055, FR-031): runs c12
	// and y5 of spec 047, and c13 and y6 of spec 055. A version between two
	// of them was not run and is not checked.
	checked []string
	best    OutcomeKind
	scope   string
	// remains names the command-running tools the files are meant to
	// remove, with why they may still be there.
	remains string
	steps   func(dir string) []string
	check   []string
	plan    func(in Inputs) (planned, error)
}

func adapterOf(c Client) (adapter, bool) {
	switch c {
	case ClientClaude:
		return adapter{
			name:    "Claude Code",
			binary:  "claude",
			paths:   []string{".mcp.json", ".claude/settings.json"},
			checked: []string{"2.1.325", "2.1.326"},
			best:    RuneOnly,
			remains: strings.Join(claudeDenied(), ", ") + " (not checked for this version)",
			steps: func(dir string) []string {
				return []string{
					"trust the project: start claude in " + dir + " and accept the trust question. Until then the deny rules apply and the allow rule does not: the session has no shell and Rune's tools are refused.",
					`approve the server "rune" when Claude Code asks, and start a new session. Until then the session has no Rune tools.`,
				}
			},
			check: []string{
				"claude -p hi --output-format stream-json --verbose",
				`the "tools" list in the first line of its output has names that start with mcp__rune__ and none of: ` + strings.Join(claudeDenied(), ", "),
			},
			plan: planClaude,
		}, true
	case ClientCodex:
		return adapter{
			name:    "Codex CLI",
			binary:  "codex",
			paths:   []string{".codex/config.toml"},
			checked: []string{"0.167.0", "0.169.0"},
			best:    RuneOnly,
			// The tools that start a subagent changed their names between
			// the two checked versions; both sets are named.
			remains: "exec_command, write_stdin, and the subagent tools spawn_agent, wait_agent, send_input, resume_agent, close_agent, followup_task, interrupt_agent, list_agents, send_message (not checked for this version)",
			steps: func(dir string) []string {
				return []string{
					"trust the project: start codex in " + dir + " and accept the trust question, then start a new session. Until then Codex ignores .codex/config.toml: the session has its shell and no Rune tools.",
				}
			},
			check: []string{
				`codex exec "Call the rune tool of one task. Then say whether you have a tool that runs a shell command, and whether you have a tool that starts a subagent."`,
				"Codex prints no tool list, so the answer is the model's: a Rune task ran, and it has neither tool",
			},
			plan: planCodex,
		}, true
	case ClientVSCode:
		return adapter{
			name:    "VS Code",
			binary:  "code",
			paths:   []string{".vscode/mcp.json", ".mcp.json", ".github/agents/rune.agent.md"},
			best:    RuneOnlyWithin,
			scope:   `the custom agent "rune"`,
			remains: `runInTerminal, runTask, createAndRunTask, runNotebookCell, runSubagent, vscode/runCommand (kept by every agent but "rune", and not checked for it)`,
			steps: func(dir string) []string {
				return []string{
					"open " + dir + ` in VS Code, trust the workspace and start the server "rune" when VS Code asks. Until then the session has no Rune tools.`,
					`choose the agent "rune" in the Chat view. Every other agent keeps the terminal.`,
				}
			},
			check: []string{
				`in the Chat view choose the agent "rune" and open its tools: only the rune tools are selected, and no terminal tool is`,
			},
			plan: planVSCode,
		}, true
	}
	return adapter{}, false
}

// versionPattern is N.N.N with the pre-release part that may follow it. The
// part is kept so that 0.169.0-alpha.2 is not taken for the checked 0.169.0.
var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?`)

// ParseVersion takes the first version out of what a client prints for
// --version, such as "2.1.326 (Claude Code)" or "codex-cli 0.169.0". It
// returns "" when there is none.
func ParseVersion(output string) string {
	return versionPattern.FindString(output)
}

// isChecked reports whether version is one of the versions the adapter was
// checked with.
func (a adapter) isChecked(version string) bool {
	v, err := semver.Parse(version)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(a.checked, func(c string) bool {
		checked, err := semver.Parse(c)
		return err == nil && v.Compare(checked) == 0
	})
}

// checkedText lists the checked versions for the report.
func (a adapter) checkedText() string {
	if len(a.checked) == 0 {
		return "none"
	}
	return strings.Join(a.checked, ", ")
}

// outcome is what a setup for the client reaches when the client reported
// version. Only a checked version earns the adapter's best outcome: for any
// other, the strongest thing Rune may say is "Registered only".
func (a adapter) outcome(version, dir string) Outcome {
	out := Outcome{Kind: RegisteredOnly, Remains: a.remains, Steps: a.steps(dir)}
	switch {
	case len(a.checked) == 0:
		out.Reason = "no " + a.name + " version has been checked with these files"
	case version == "":
		out.Reason = "no version was found"
	case !a.isChecked(version):
		out.Reason = "this version was not checked"
	default:
		return Outcome{Kind: a.best, Scope: a.scope, Steps: out.Steps}
	}
	return out
}
