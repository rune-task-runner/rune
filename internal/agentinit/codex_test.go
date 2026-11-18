package agentinit

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPlanCodexNewProject(t *testing.T) {
	p, err := planCodex(Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile"})
	if err != nil || len(p.conflicts) != 0 || len(p.files) != 1 {
		t.Fatalf("got %+v, %v", p, err)
	}
	f := p.files[0]
	if f.Path != ".codex/config.toml" || f.Status() != "created" {
		t.Errorf("file %s is %s", f.Path, f.Status())
	}
	if got, want := entryStates(f), "features.shell_tool=missing agents.enabled=missing mcp_servers.rune.command=missing mcp_servers.rune.args=missing"; got != want {
		t.Errorf("entries: %s", got)
	}
	wantChanges := []string{"features.shell_tool = false", "agents.enabled = false", `server "rune" runs: rune mcp --file /p/Runefile --no-reload`}
	if !slices.Equal(f.Changes, wantChanges) {
		t.Errorf("changes: %q", f.Changes)
	}
	// Settings that look as if they would help do nothing, and the one that
	// approves every tool would approve [confirm] tasks (FR-014).
	for _, never := range []string{"unified_exec", "multi_agent", "default_tools_approval_mode", "approval_mode", "cwd"} {
		if strings.Contains(string(f.New), never) {
			t.Errorf("the file holds %q:\n%s", never, f.New)
		}
	}
}

func TestPlanCodexPartialAndForce(t *testing.T) {
	doc := "[mcp_servers.rune]\ncommand = \"other\"\ntool_timeout_sec = 600\n\n[features]\nshell_tool = false\n"
	in := Inputs{Unpinned: true, Files: map[string][]byte{codexConfigPath: []byte(doc)}}
	p, err := planCodex(in)
	if err != nil || len(p.conflicts) != 1 || !p.conflicts[0].Forceable || p.conflicts[0].Line != 2 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if f := p.files[0]; string(f.New) != doc || f.Changes != nil {
		t.Errorf("the file changes although it has a conflict:\n%s", f.New)
	}
	if got, want := entryStates(p.files[0]), "features.shell_tool=present agents.enabled=missing mcp_servers.rune.command=differs mcp_servers.rune.args=missing"; got != want {
		t.Errorf("entries: %s", got)
	}

	in.Force = true
	p, err = planCodex(in)
	if err != nil || len(p.conflicts) != 0 {
		t.Fatalf("force: %+v, %v", p, err)
	}
	want := "[mcp_servers.rune]\ncommand = \"rune\"\ntool_timeout_sec = 600\nargs = [\"mcp\", \"--no-reload\"]\n\n[features]\nshell_tool = false\n\n[agents]\nenabled = false\n"
	if got := string(p.files[0].New); got != want {
		t.Errorf("force:\n%s\nwant\n%s", got, want)
	}
	wantChanges := []string{"agents.enabled = false", `added args to server "rune"`, `replaced command of server "rune"`}
	if !slices.Equal(p.files[0].Changes, wantChanges) {
		t.Errorf("changes: %q", p.files[0].Changes)
	}
}

func TestPlanCodexConflictsAndErrors(t *testing.T) {
	p, err := planCodex(Inputs{Files: map[string][]byte{
		codexConfigPath: []byte("[features]\nshell_tool = true\n[agents]\nenabled = true\n"),
	}})
	if err != nil || len(p.conflicts) != 2 || !p.conflicts[0].Forceable || !p.conflicts[1].Forceable {
		t.Errorf("got %+v, %v", p, err)
	}

	var manual *ManualError
	_, err = planCodex(Inputs{Files: map[string][]byte{codexConfigPath: []byte("mcp_servers = { rune = {} }\n")}})
	if !errors.As(err, &manual) || manual.Setting != `mcp_servers.rune.command = "rune"` {
		t.Errorf("an inline table gives %v, want a *ManualError", err)
	}

	var pe *ParseError
	if _, err = planCodex(Inputs{Files: map[string][]byte{codexConfigPath: []byte("[features\n")}}); !errors.As(err, &pe) {
		t.Errorf("an invalid file gives %v, want a *ParseError", err)
	}
}

// What stops a run is shown at its place in the file as it is: a key planned
// above it does not move it.
func TestPlanCodexPositionsAreOfTheFile(t *testing.T) {
	p, err := planCodex(Inputs{Files: map[string][]byte{
		codexConfigPath: []byte("[features]\nx = 1\n\n[mcp_servers.rune]\ncommand = \"other\"\n"),
	}})
	if err != nil || len(p.conflicts) != 1 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if c := p.conflicts[0]; c.Line != 5 || c.Col != 11 || !c.Forceable {
		t.Errorf("conflict at %d:%d, want 5:11: %+v", c.Line, c.Col, c)
	}

	var manual *ManualError
	_, err = planCodex(Inputs{Files: map[string][]byte{
		codexConfigPath: []byte("[features]\nx = 1\n\n[mcp_servers]\nrune.command = \"rune\"\n"),
	}})
	if !errors.As(err, &manual) || manual.Line != 5 || manual.Col != 1 {
		t.Errorf("dotted keys under a table give %v, want a *ManualError at 5:1", err)
	}
}
