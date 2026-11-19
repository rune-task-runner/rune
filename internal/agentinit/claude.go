package agentinit

import (
	"fmt"
	"slices"
	"strings"
)

// Claude Code reads the server from .mcp.json and its permission rules from
// .claude/settings.json. A deny rule that is a bare tool name takes the tool
// away from the model, and an allow rule lets Rune's tools run without a
// question once the workspace is trusted (design note, runs c11 and c12).

const (
	claudeSettingsPath = ".claude/settings.json"
	mcpJSONPath        = ".mcp.json"
	// claudeAllowed allows every tool of the server named rune.
	claudeAllowed = "mcp__rune"
)

// claudeDenied names the tools of Claude Code that run a command or start
// something that can: the ones its own restricted mode removes, without
// WebFetch, which runs nothing.
func claudeDenied() []string {
	return []string{"Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"}
}

func planClaude(in Inputs) (planned, error) {
	server, conflicts, err := planServer(in, mcpJSONPath, "mcpServers")
	if err != nil {
		return planned{}, err
	}
	settings, more, notes, err := planClaudeSettings(in)
	if err != nil {
		return planned{}, err
	}
	return planned{files: []File{server, settings}, conflicts: append(conflicts, more...), notes: notes}, nil
}

// planServer plans the entry of the server named rune in a JSON file that
// keeps its servers under the key container. Rune owns three keys of the
// entry; any other key in it is the person's.
func planServer(in Inputs, path, container string) (File, []Conflict, error) {
	old, existed := in.Files[path]
	f := File{Path: path, Old: old, New: old, Existed: existed}
	owned := jsonObject{{"type", "stdio"}, {"command", "rune"}, {"args", registration(in)}}

	doc := old
	var conflicts []Conflict
	var changes []jsonChange
	var added, replaced []string
	for i, field := range owned {
		keyPath := []string{container, "rune", field.key}
		next, state, conflict, err := jsonSet(path, doc, keyPath, field.value, in.Force)
		if err != nil {
			return f, nil, err
		}
		f.Entries = append(f.Entries, Entry{dotted(keyPath), state})
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
			if !conflict.Forceable {
				// The entry cannot be reached at all: the same conflict
				// would repeat for every key.
				for _, rest := range owned[i+1:] {
					f.Entries = append(f.Entries, Entry{dotted([]string{container, "rune", rest.key}), StateDiffers})
				}
				break
			}
			continue
		}
		doc = next
		changes = append(changes, jsonChange{path: keyPath, value: field.value})
		switch state {
		case StateMissing:
			added = append(added, field.key)
		case StateDiffers:
			replaced = append(replaced, field.key)
		}
	}
	if len(conflicts) > 0 {
		return f, conflicts, nil
	}
	if len(added)+len(replaced) == 0 {
		return f, nil, nil
	}
	if err := verifyJSON(path, old, doc, changes); err != nil {
		return f, nil, err
	}
	f.New = doc
	f.Changes = serverChanges(in, added, replaced, len(owned))
	return f, nil, nil
}

func planClaudeSettings(in Inputs) (File, []Conflict, []string, error) {
	const path = claudeSettingsPath
	old, existed := in.Files[path]
	f := File{Path: path, Old: old, New: old, Existed: existed}

	conflicts, notes, err := claudeExistingRules(path, old)
	if err != nil {
		return f, nil, nil, err
	}
	doc := old
	var changes []jsonChange
	for _, list := range []struct {
		key   string
		rules []string
	}{{"deny", claudeDenied()}, {"allow", []string{claudeAllowed}}} {
		listPath := []string{"permissions", list.key}
		next, added, conflict, err := jsonAppend(path, doc, listPath, list.rules)
		if err != nil {
			return f, nil, nil, err
		}
		if conflict != nil {
			// A conflict is shown in the file as it is, and the rules
			// planned so far have moved what follows them in doc.
			if _, _, inFile, _ := jsonAppend(path, old, listPath, list.rules); inFile != nil {
				conflict = inFile
			}
		}
		for _, rule := range list.rules {
			state := StatePresent
			switch {
			case conflict != nil:
				state = StateDiffers
			case slices.Contains(added, rule):
				state = StateMissing
			}
			f.Entries = append(f.Entries, Entry{dotted(listPath) + ": " + rule, state})
		}
		if conflict != nil {
			// The same value in the way is reported once.
			if !slices.Contains(conflicts, *conflict) {
				conflicts = append(conflicts, *conflict)
			}
			continue
		}
		doc = next
		changes = append(changes, jsonChange{path: listPath, items: list.rules})
		if len(added) > 0 {
			f.Changes = append(f.Changes, list.key+" "+strings.Join(added, ", "))
		}
	}
	if len(conflicts) > 0 {
		f.Changes = nil
		return f, conflicts, notes, nil
	}
	if len(f.Changes) == 0 {
		return f, nil, notes, nil
	}
	if err := verifyJSON(path, old, doc, changes); err != nil {
		return f, nil, nil, err
	}
	f.New = doc
	return f, nil, notes, nil
}

// claudeExistingRules reads the rules that are already there and name Rune's
// tools. A deny rule for the whole server, written as mcp__rune or as
// mcp__rune__*, makes the setup impossible, and Rune never removes a deny
// rule, so it is a conflict that --force does not override. A rule for one
// tool is the person's choice and becomes a note.
func claudeExistingRules(path string, doc []byte) ([]Conflict, []string, error) {
	root, err := parseJSON(path, doc)
	if err != nil {
		return nil, nil, err
	}
	var conflicts []Conflict
	var notes []string
	rules := func(key string) []*jsonNode {
		n := root
		for _, k := range []string{"permissions", key} {
			if n = n.member(k); n == nil {
				return nil
			}
		}
		return n.elems
	}
	for _, rule := range rules("deny") {
		switch name, _ := rule.value.(string); {
		case name == claudeAllowed, name == claudeAllowed+"__*":
			conflicts = append(conflicts, *conflictAt(path, doc, rule,
				"permissions.deny holds "+name+", which denies every Rune tool. rune agent init never removes a deny rule", false))
		case strings.HasPrefix(name, claudeAllowed+"__"):
			notes = append(notes, fmt.Sprintf("%s keeps the deny rule %s: that tool stays denied.", path, name))
		}
	}
	for _, rule := range rules("ask") {
		if name, _ := rule.value.(string); name == claudeAllowed || strings.HasPrefix(name, claudeAllowed+"__") {
			notes = append(notes, fmt.Sprintf("%s keeps the ask rule %s: Claude Code asks before those calls.", path, name))
		}
	}
	return conflicts, notes, nil
}
