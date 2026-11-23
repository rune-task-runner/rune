package analyzer

import (
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// agentOutputArguments are the names [agent-output(...)] takes, in the order
// a diagnostic lists them.
var agentOutputArguments = []string{"head", "tail"}

// checkAgentOutput validates [agent-output] and [agent-summary] (spec 057,
// RUNE2021). A policy the engine would not read as written is an error, so a
// Runefile never reads as trimming output it then returns whole.
func (a *analyzer) checkAgentOutput() {
	for _, t := range a.file.Tasks {
		a.firstOf(t, ast.AttrAgentSummary, diag.CodeInvalidAgentOutput)
		if attr := a.firstOf(t, ast.AttrAgentOutput, diag.CodeInvalidAgentOutput); attr != nil {
			a.checkAgentOutputArgs(attr)
		}
	}
}

func (a *analyzer) checkAgentOutputArgs(attr *ast.Attribute) {
	seen := map[string]bool{}
	for _, arg := range attr.Allow {
		switch {
		case !slices.Contains(agentOutputArguments, arg.Name):
			a.agentOutputErrorf(arg.NameSp, "unknown agent-output argument %q (expected %s)", arg.Name, strings.Join(agentOutputArguments, ", "))
		case seen[arg.Name]:
			a.agentOutputErrorf(arg.NameSp, "duplicate agent-output argument %q", arg.Name)
		default:
			seen[arg.Name] = true
			if _, err := ast.AgentOutputLines(arg.Value); err != nil {
				a.agentOutputErrorf(arg.ValueSp, "agent-output %s %q %s", arg.Name, arg.Value, err)
			}
		}
	}
	if !seen["tail"] {
		a.agentOutputErrorf(attr.Sp, `agent-output(...) requires tail="<lines>"`)
	}
}

func (a *analyzer) agentOutputErrorf(span token.Span, format string, args ...any) {
	a.diags.Codef(diag.CodeInvalidAgentOutput, span, format, args...)
}
