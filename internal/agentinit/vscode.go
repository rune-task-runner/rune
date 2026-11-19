package agentinit

import (
	"bytes"
)

// VS Code has no workspace setting that takes the terminal away. What it
// has is a custom agent whose tools list is an allowlist, so the restriction
// is that one agent's (design note). The server is registered for both ways
// VS Code runs an agent: .vscode/mcp.json for the Local harness and
// .mcp.json for the Copilot harness.

const (
	vscodeMCPPath   = ".vscode/mcp.json"
	vscodeAgentPath = ".github/agents/rune.agent.md"
)

// vscodeAgentFile is the custom agent. Its tools list is never empty: in the
// Copilot harness an empty list means every tool.
const vscodeAgentFile = `---
description: Works on this project through its Runefile only.
tools: ['rune/*']
agents: []
---

Run this project's tasks with the rune tools. You have no terminal.
`

func planVSCode(in Inputs) (planned, error) {
	local, conflicts, err := planServer(in, vscodeMCPPath, "servers")
	if err != nil {
		return planned{}, err
	}
	copilot, more, err := planServer(in, mcpJSONPath, "mcpServers")
	if err != nil {
		return planned{}, err
	}
	conflicts = append(conflicts, more...)
	agent, conflict := planVSCodeAgent(in)
	if conflict != nil {
		conflicts = append(conflicts, *conflict)
	}
	return planned{files: []File{local, copilot, agent}, conflicts: conflicts}, nil
}

// planVSCodeAgent plans the agent file, which Rune owns as a whole. Line
// endings are not part of the comparison: a checkout may have turned them
// into \r\n, and that is the same file.
func planVSCodeAgent(in Inputs) (File, *Conflict) {
	const path = vscodeAgentPath
	old, existed := in.Files[path]
	f := File{Path: path, Old: old, New: old, Existed: existed}
	switch {
	// An empty file is a file with nothing in it yet, as for the other
	// client files.
	case len(bytes.TrimSpace(old)) == 0:
		f.New = []byte(vscodeAgentFile)
		f.Entries = []Entry{{"content", StateMissing}}
		f.Changes = []string{`custom agent "rune" with tools rune/*`}
	case bytes.Equal(bytes.ReplaceAll(old, []byte("\r\n"), []byte("\n")), []byte(vscodeAgentFile)):
		f.Entries = []Entry{{"content", StatePresent}}
	default:
		f.Entries = []Entry{{"content", StateDiffers}}
		if !in.Force {
			firstLine, _, _ := bytes.Cut(old, []byte("\n"))
			return f, &Conflict{
				File: path, Line: 1, Col: 1, Width: max(1, len(bytes.TrimRight(firstLine, "\r"))),
				Msg:       path + " has other content. Run again with --force to replace it",
				Forceable: true,
			}
		}
		f.New = []byte(vscodeAgentFile)
		f.Changes = []string{`replaced the custom agent "rune"`}
	}
	return f, nil
}
