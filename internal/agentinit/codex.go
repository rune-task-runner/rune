package agentinit

import (
	"fmt"
	"slices"
)

// Codex CLI reads everything from .codex/config.toml in a project its user
// trusts. shell_tool = false takes the shell away, agents.enabled = false
// the tools that start a subagent (design note, runs x7 and y5). Approval is
// left at its default: the setting that approves every tool of a server
// would approve [confirm] tasks too.

const codexConfigPath = ".codex/config.toml"

func planCodex(in Inputs) (planned, error) {
	const path = codexConfigPath
	old, existed := in.Files[path]
	f := File{Path: path, Old: old, New: old, Existed: existed}

	server := []string{"mcp_servers", "rune"}
	owned := []struct {
		table []string
		key   string
		value any
	}{
		{[]string{"features"}, "shell_tool", false},
		{[]string{"agents"}, "enabled", false},
		{server, "command", "rune"},
		{server, "args", registration(in)},
	}

	doc := old
	var conflicts []Conflict
	var changes, added, replaced []string
	for _, o := range owned {
		name := dotted(append(o.table[:len(o.table):len(o.table)], o.key))
		next, state, conflict, err := tomlSet(path, doc, o.table, o.key, o.value, in.Force)
		if conflict != nil || err != nil {
			// What stops the run is shown in the file as it is, and the keys
			// planned so far have moved what follows them in doc.
			if _, _, c, e := tomlSet(path, old, o.table, o.key, o.value, in.Force); c != nil || e != nil {
				conflict, err = c, e
			}
		}
		if err != nil {
			return planned{}, err
		}
		f.Entries = append(f.Entries, Entry{name, state})
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
			continue
		}
		doc = next
		isServer := slices.Equal(o.table, server)
		switch {
		case state == StateMissing && isServer:
			added = append(added, o.key)
		case state == StateDiffers && isServer:
			replaced = append(replaced, o.key)
		case state == StateMissing:
			changes = append(changes, fmt.Sprintf("%s = %s", name, tomlValue(o.value)))
		case state == StateDiffers:
			changes = append(changes, "replaced "+name)
		}
	}
	if len(conflicts) == 0 {
		f.New = doc
		f.Changes = append(changes, serverChanges(in, added, replaced, 2)...)
	}
	return planned{files: []File{f}, conflicts: conflicts}, nil
}
