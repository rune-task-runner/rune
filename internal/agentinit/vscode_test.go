package agentinit

import (
	"errors"
	"strings"
	"testing"
)

func TestPlanVSCodeNewProject(t *testing.T) {
	p, err := planVSCode(Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile"})
	if err != nil || len(p.conflicts) != 0 || len(p.files) != 3 {
		t.Fatalf("got %+v, %v", p, err)
	}
	local, copilot, agent := p.files[0], p.files[1], p.files[2]
	if local.Path != ".vscode/mcp.json" || copilot.Path != ".mcp.json" || agent.Path != ".github/agents/rune.agent.md" {
		t.Errorf("files: %s, %s, %s", local.Path, copilot.Path, agent.Path)
	}
	if !strings.Contains(string(local.New), "\"servers\": {\n    \"rune\": {") || !strings.Contains(string(copilot.New), "\"mcpServers\": {\n    \"rune\": {") {
		t.Errorf("the server is not under the key each harness reads:\n%s\n%s", local.New, copilot.New)
	}
	want := "---\ndescription: Works on this project through its Runefile only.\ntools: ['rune/*']\nagents: []\n---\n\n" +
		"Run this project's tasks with the rune tools. You have no terminal.\n"
	if string(agent.New) != want {
		t.Errorf("agent file:\n%s", agent.New)
	}
	if got := entryStates(agent); got != "content=missing" {
		t.Errorf("agent entries: %s", got)
	}
}

// In the Copilot harness an empty or missing tools list means every tool.
// The list the command writes has exactly one entry.
func TestVSCodeAgentToolsListIsNeverEmpty(t *testing.T) {
	var tools, agents int
	for line := range strings.Lines(vscodeAgentFile) {
		switch strings.TrimSpace(line) {
		case "tools: ['rune/*']":
			tools++
		case "agents: []":
			agents++
		}
		if strings.HasPrefix(line, "tools:") && strings.TrimSpace(line) != "tools: ['rune/*']" {
			t.Errorf("tools line is %q", line)
		}
	}
	if tools != 1 || agents != 1 {
		t.Errorf("the agent file has %d tools lines and %d agents lines, want one of each", tools, agents)
	}
}

func TestPlanVSCodeAgent(t *testing.T) {
	other := "---\ntools: []\n---\n"
	f, conflict := planVSCodeAgent(Inputs{Files: map[string][]byte{vscodeAgentPath: []byte(other)}})
	want := Conflict{
		File: vscodeAgentPath, Line: 1, Col: 1, Width: 3, Forceable: true,
		Msg: ".github/agents/rune.agent.md has other content. Run again with --force to replace it",
	}
	if conflict == nil || *conflict != want || string(f.New) != other || entryStates(f) != "content=differs" {
		t.Errorf("conflict = %+v, file %q, entries %s", conflict, f.New, entryStates(f))
	}

	f, conflict = planVSCodeAgent(Inputs{Force: true, Files: map[string][]byte{vscodeAgentPath: []byte(other)}})
	if conflict != nil || string(f.New) != vscodeAgentFile || f.Changes[0] != `replaced the custom agent "rune"` || f.Status() != "updated" {
		t.Errorf("force: conflict = %+v, changes %q", conflict, f.Changes)
	}

	// A checkout that turned the line endings into \r\n has the same file.
	crlf := strings.ReplaceAll(vscodeAgentFile, "\n", "\r\n")
	f, conflict = planVSCodeAgent(Inputs{Files: map[string][]byte{vscodeAgentPath: []byte(crlf)}})
	if conflict != nil || string(f.New) != crlf || f.Status() != "unchanged" || entryStates(f) != "content=present" {
		t.Errorf("crlf: conflict = %+v, status %s, entries %s", conflict, f.Status(), entryStates(f))
	}

	// An empty file is filled in, as an empty .mcp.json is.
	for _, empty := range []string{"", " \n\t"} {
		f, conflict = planVSCodeAgent(Inputs{Files: map[string][]byte{vscodeAgentPath: []byte(empty)}})
		if conflict != nil || string(f.New) != vscodeAgentFile || f.Status() != "updated" || entryStates(f) != "content=missing" {
			t.Errorf("an agent file holding %q: conflict = %+v, status %s, entries %s", empty, conflict, f.Status(), entryStates(f))
		}
	}

	_, conflict = planVSCodeAgent(Inputs{Files: map[string][]byte{vscodeAgentPath: []byte("\nx")}})
	if conflict == nil || conflict.Width != 1 {
		t.Errorf("an agent file with an empty first line: conflict = %+v", conflict)
	}
}

func TestPlanVSCodeConflictsAndErrors(t *testing.T) {
	p, err := planVSCode(Inputs{Files: map[string][]byte{
		vscodeMCPPath:   []byte(`{"servers": 1}`),
		mcpJSONPath:     []byte(`{"mcpServers": {"rune": {"type": "http"}}}`),
		vscodeAgentPath: []byte("x"),
	}})
	if err != nil || len(p.conflicts) != 3 {
		t.Fatalf("got %+v, %v", p, err)
	}
	for i, file := range []string{vscodeMCPPath, mcpJSONPath, vscodeAgentPath} {
		if p.conflicts[i].File != file {
			t.Errorf("conflict %d is in %s, want %s", i, p.conflicts[i].File, file)
		}
	}
	var pe *ParseError
	if _, err := planVSCode(Inputs{Files: map[string][]byte{vscodeMCPPath: []byte(`{`)}}); !errors.As(err, &pe) || pe.File != vscodeMCPPath {
		t.Errorf("an invalid .vscode/mcp.json gives %v", err)
	}
	if _, err := planVSCode(Inputs{Files: map[string][]byte{mcpJSONPath: []byte(`{`)}}); !errors.As(err, &pe) || pe.File != mcpJSONPath {
		t.Errorf("an invalid .mcp.json gives %v", err)
	}
}
