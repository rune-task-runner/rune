package agentinit

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func entryStates(f File) string {
	parts := make([]string, len(f.Entries))
	for i, e := range f.Entries {
		parts[i] = e.Name + "=" + string(e.State)
	}
	return strings.Join(parts, " ")
}

func TestPlanClaudeNewProject(t *testing.T) {
	p, err := planClaude(Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile"})
	if err != nil || len(p.conflicts) != 0 || len(p.notes) != 0 || len(p.files) != 2 {
		t.Fatalf("got %+v, %v", p, err)
	}
	server, settings := p.files[0], p.files[1]
	if server.Path != ".mcp.json" || settings.Path != ".claude/settings.json" {
		t.Errorf("files are %s and %s", server.Path, settings.Path)
	}
	if got, want := entryStates(server), "mcpServers.rune.type=missing mcpServers.rune.command=missing mcpServers.rune.args=missing"; got != want {
		t.Errorf("server entries: %s", got)
	}
	want := "permissions.deny: Bash=missing permissions.deny: PowerShell=missing permissions.deny: Monitor=missing " +
		"permissions.deny: Workflow=missing permissions.deny: CronCreate=missing permissions.deny: RemoteTrigger=missing " +
		"permissions.allow: mcp__rune=missing"
	if got := entryStates(settings); got != want {
		t.Errorf("settings entries: %s", got)
	}
	if !slices.Equal(server.Changes, []string{`server "rune" runs: rune mcp --file /p/Runefile --no-reload`}) {
		t.Errorf("server changes: %q", server.Changes)
	}
	if !slices.Equal(settings.Changes, []string{"deny Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger", "allow mcp__rune"}) {
		t.Errorf("settings changes: %q", settings.Changes)
	}
	if server.Existed || settings.Existed || server.Status() != "created" || settings.Status() != "created" {
		t.Errorf("status: %s, %s", server.Status(), settings.Status())
	}
}

// A file that holds only a comment is added to after the comment.
func TestPlanClaudeFilesWithOnlyAComment(t *testing.T) {
	p, err := planClaude(Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile", Files: map[string][]byte{
		mcpJSONPath:        []byte("// servers\n"),
		claudeSettingsPath: []byte("/* rules */"),
	}})
	if err != nil || len(p.conflicts) != 0 || len(p.files) != 2 {
		t.Fatalf("got %+v, %v", p, err)
	}
	server, settings := p.files[0], p.files[1]
	if !strings.HasPrefix(string(server.New), "// servers\n{\n  \"mcpServers\": {") || server.Status() != "updated" {
		t.Errorf(".mcp.json: %s: %q", server.Status(), server.New)
	}
	if !strings.HasPrefix(string(settings.New), "/* rules */\n{\n  \"permissions\": {") || settings.Status() != "updated" {
		t.Errorf("settings: %s: %q", settings.Status(), settings.New)
	}
}

func TestPlanServer(t *testing.T) {
	in := Inputs{Unpinned: true}
	partial := `{"mcpServers": {"rune": {"type": "stdio", "command": "rune", "env": {"A": "b"}}}}`
	in.Files = map[string][]byte{".mcp.json": []byte(partial)}
	f, conflicts, err := planServer(in, ".mcp.json", "mcpServers")
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("%v %v", conflicts, err)
	}
	if got, want := entryStates(f), "mcpServers.rune.type=present mcpServers.rune.command=present mcpServers.rune.args=missing"; got != want {
		t.Errorf("entries: %s", got)
	}
	if !slices.Equal(f.Changes, []string{`added args to server "rune"`}) || !strings.Contains(string(f.New), `"env": {"A": "b"}, "args": ["mcp", "--no-reload"]`) {
		t.Errorf("changes %q, file %s", f.Changes, f.New)
	}

	// Another value at two owned keys: two conflicts, and with force only
	// those two values change.
	other := `{"mcpServers": {"rune": {"type": "http", "command": "rune", "args": ["mcp"], "url": "x"}}}`
	in.Files[".mcp.json"] = []byte(other)
	f, conflicts, err = planServer(in, ".mcp.json", "mcpServers")
	if err != nil || len(conflicts) != 2 || !conflicts[0].Forceable || conflicts[0].Line != 1 || conflicts[0].Col != 34 {
		t.Fatalf("conflicts = %+v, err = %v", conflicts, err)
	}
	if string(f.New) != other || f.Changes != nil {
		t.Errorf("the file changes although it has a conflict: %s", f.New)
	}
	if got, want := entryStates(f), "mcpServers.rune.type=differs mcpServers.rune.command=present mcpServers.rune.args=differs"; got != want {
		t.Errorf("entries: %s", got)
	}
	in.Force = true
	f, conflicts, err = planServer(in, ".mcp.json", "mcpServers")
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("force: %v %v", conflicts, err)
	}
	if want := `{"mcpServers": {"rune": {"type": "stdio", "command": "rune", "args": ["mcp", "--no-reload"], "url": "x"}}}`; string(f.New) != want {
		t.Errorf("force: %s\nwant   %s", f.New, want)
	}
	if !slices.Equal(f.Changes, []string{`replaced type of server "rune"`, `replaced args of server "rune"`}) {
		t.Errorf("force changes: %q", f.Changes)
	}

	// A value of another type where the servers belong: one conflict that
	// force does not replace, and every entry counts as different.
	in.Files[".mcp.json"] = []byte(`{"mcpServers": []}`)
	f, conflicts, err = planServer(in, ".mcp.json", "mcpServers")
	if err != nil || len(conflicts) != 1 || conflicts[0].Forceable {
		t.Fatalf("wrong type: %+v %v", conflicts, err)
	}
	if got, want := entryStates(f), "mcpServers.rune.type=differs mcpServers.rune.command=differs mcpServers.rune.args=differs"; got != want {
		t.Errorf("entries: %s", got)
	}

	in.Files[".mcp.json"] = []byte(`{"mcpServers": `)
	var pe *ParseError
	if _, _, err = planServer(in, ".mcp.json", "mcpServers"); !errors.As(err, &pe) {
		t.Errorf("an invalid file gives %v, want a *ParseError", err)
	}
}

func TestPlanClaudeSettingsConflicts(t *testing.T) {
	plan := func(doc string, force bool) (File, []Conflict, []string) {
		t.Helper()
		f, conflicts, notes, err := planClaudeSettings(Inputs{Force: force, Files: map[string][]byte{claudeSettingsPath: []byte(doc)}})
		if err != nil {
			t.Fatal(err)
		}
		return f, conflicts, notes
	}

	// The deny rule for the whole server: never removed, with or without
	// force.
	denyAll := "{\n  \"permissions\": {\n    \"deny\": [\"WebFetch\", \"mcp__rune\"]\n  }\n}\n"
	for _, force := range []bool{false, true} {
		f, conflicts, _ := plan(denyAll, force)
		want := Conflict{
			File: claudeSettingsPath, Line: 3, Col: 26, Width: 11,
			Msg: "permissions.deny holds mcp__rune, which denies every Rune tool. rune agent init never removes a deny rule",
		}
		if len(conflicts) != 1 || conflicts[0] != want {
			t.Errorf("force %v: conflicts = %+v", force, conflicts)
		}
		if string(f.New) != denyAll || f.Changes != nil {
			t.Errorf("force %v: the file changes", force)
		}
	}

	// The same rule with a wildcard denies every tool too.
	_, conflicts, notes := plan(`{"permissions": {"deny": ["mcp__rune__*"]}}`, true)
	if len(conflicts) != 1 || conflicts[0].Forceable || len(notes) != 0 ||
		conflicts[0].Msg != "permissions.deny holds mcp__rune__*, which denies every Rune tool. rune agent init never removes a deny rule" {
		t.Errorf("wildcard deny: conflicts = %+v, notes = %q", conflicts, notes)
	}

	// A string where the rules belong is reported once, though both lists
	// run into it.
	f, conflicts, _ := plan(`{"permissions": "none"}`, true)
	if len(conflicts) != 1 || conflicts[0].Forceable || conflicts[0].Msg != "permissions is not an object; rune agent init cannot add to it" {
		t.Errorf("conflicts = %+v", conflicts)
	}
	if !strings.Contains(entryStates(f), "permissions.allow: mcp__rune=differs") {
		t.Errorf("entries: %s", entryStates(f))
	}

	_, conflicts, _ = plan(`{"permissions": {"deny": "all", "allow": []}}`, false)
	if len(conflicts) != 1 || conflicts[0].Msg != "permissions.deny is not a list; rune agent init cannot add to it" {
		t.Errorf("conflicts = %+v", conflicts)
	}

	_, conflicts, notes = plan(`{"permissions": {"deny": ["mcp__rune__deploy", 7], "ask": ["mcp__rune", "mcp__rune__clean", "Edit"]}}`, false)
	wantNotes := []string{
		".claude/settings.json keeps the deny rule mcp__rune__deploy: that tool stays denied.",
		".claude/settings.json keeps the ask rule mcp__rune: Claude Code asks before those calls.",
		".claude/settings.json keeps the ask rule mcp__rune__clean: Claude Code asks before those calls.",
	}
	if len(conflicts) != 0 || !slices.Equal(notes, wantNotes) {
		t.Errorf("conflicts = %+v, notes = %q", conflicts, notes)
	}

	var pe *ParseError
	if _, _, _, err := planClaudeSettings(Inputs{Files: map[string][]byte{claudeSettingsPath: []byte(`{"a" 1}`)}}); !errors.As(err, &pe) {
		t.Errorf("an invalid file gives %v, want a *ParseError", err)
	}
}

// A conflict in the second file of a client leaves the first as it was.
func TestPlanClaudeConflictInSecondFile(t *testing.T) {
	res, err := Plan(ClientClaude, Inputs{Runefile: "/p/Runefile", Files: map[string][]byte{
		claudeSettingsPath: []byte(`{"permissions": {"deny": ["mcp__rune"]}}`),
	}})
	if err != nil || len(res.Conflicts) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if first := res.Files[0]; first.Path != ".mcp.json" || first.New != nil || first.Status() != "unchanged" {
		t.Errorf(".mcp.json would be written: %s", first.New)
	}
}

// A conflict is shown at its place in the file as it is: the rules planned
// for the list before it do not move it.
func TestPlanClaudeSettingsConflictPosition(t *testing.T) {
	tests := []struct {
		name, doc string
		line, col int
	}{
		{"a list over several lines", "{\n  \"permissions\": {\n    \"deny\": [\n      \"Read(./.env)\"\n    ],\n    \"allow\": \"nope\"\n  }\n}\n", 6, 14},
		{"one line", `{"permissions": {"deny": [], "allow": null}}`, 1, 39},
	}
	for _, tt := range tests {
		_, conflicts, _, err := planClaudeSettings(Inputs{Files: map[string][]byte{claudeSettingsPath: []byte(tt.doc)}})
		if err != nil || len(conflicts) != 1 {
			t.Fatalf("%s: conflicts = %+v, %v", tt.name, conflicts, err)
		}
		if c := conflicts[0]; c.Line != tt.line || c.Col != tt.col || c.Msg != "permissions.allow is not a list; rune agent init cannot add to it" {
			t.Errorf("%s: conflict at %d:%d (%s), want %d:%d", tt.name, c.Line, c.Col, c.Msg, tt.line, tt.col)
		}
	}
}
