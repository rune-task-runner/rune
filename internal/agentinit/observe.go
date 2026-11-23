package agentinit

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// `rune agent observe claude` (spec 058) adds three background hook handlers
// to the person's own Claude Code settings of the project. Each runs
// `rune agent record` for one event of the Bash tool. Nothing else in the file
// changes: a handler is Rune's only when its command is exactly
// ObserveCommand, and permission rules are never touched.

const (
	// ObserveCommand is the command of Rune's handlers. It has no path and
	// no quoting, so it is the same on every shell and after a move.
	ObserveCommand = "rune agent record"
	// ObservePath is the settings file the handlers go into: the person's
	// own file for the project, not the one a project shares.
	ObservePath = ".claude/settings.local.json"
	// observeMatcher is the tool whose calls are recorded.
	observeMatcher = "Bash"
)

// ObserveEvents are the hook events Rune's handlers are on: before a Bash
// call, after it succeeds, after it fails.
func ObserveEvents() []string {
	return []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"}
}

// ObservePaths are the files a run reads: the one it edits, and the shared
// settings, read for their deny rules only.
func ObservePaths() []string {
	return []string{ObservePath, claudeSettingsPath}
}

// ObserveInputs is what an observation plan is made from.
type ObserveInputs struct {
	// Files is the content of the files of ObservePaths that exist.
	Files map[string][]byte
	// ShownDir is the project directory as the notes print it.
	ShownDir string
	// Remove takes Rune's handlers out instead of adding them.
	Remove bool
}

// ObserveResult is a plan for the settings file.
type ObserveResult struct {
	File      File
	Conflicts []Conflict
	Notes     []string
	removed   bool
}

// observeGroup is the matcher group added for one event.
func observeGroup() jsonObject {
	return jsonObject{
		{"matcher", observeMatcher},
		{"hooks", []any{jsonObject{{"type", "command"}, {"command", ObserveCommand}, {"async", true}}}},
	}
}

// PlanObserve works out what turning observation on does to the settings
// file. It returns a *ParseError for a file that is not valid JSON. With a
// conflict, the file's New equals its Old.
func PlanObserve(in ObserveInputs) (*ObserveResult, error) {
	old, existed := in.Files[ObservePath]
	body, marked := bytes.CutPrefix(old, byteOrderMark)
	res := &ObserveResult{File: File{Path: ObservePath, Old: old, New: old, Existed: existed}, removed: in.Remove}

	if in.Remove {
		return planRemove(res, body, marked)
	}
	notes, err := observeNotes(in, body)
	if err != nil {
		return nil, err
	}
	res.Notes = notes

	doc := body
	for _, event := range ObserveEvents() {
		present, conflict, err := observeHandler(doc, event)
		if err != nil {
			return nil, err
		}
		if conflict != nil {
			// A conflict is shown in the file as it was.
			if _, inFile, _ := observeHandler(body, event); inFile != nil {
				conflict = inFile
			}
			// The same value in the way of every event is reported once.
			if !slices.Contains(res.Conflicts, *conflict) {
				res.Conflicts = append(res.Conflicts, *conflict)
			}
			continue
		}
		if present {
			res.File.Entries = append(res.File.Entries, Entry{"hooks." + event, StatePresent})
			continue
		}
		if doc, err = jsonAppendValue(doc, []string{"hooks", event}, observeGroup()); err != nil {
			return nil, err
		}
		res.File.Entries = append(res.File.Entries, Entry{"hooks." + event, StateMissing})
		res.File.Changes = append(res.File.Changes, "added the handler on "+event)
	}
	if len(res.Conflicts) > 0 {
		res.File.Changes = nil
		return res, nil
	}
	if bytes.Equal(doc, body) {
		return res, nil
	}
	if err := verifyObserve(body, doc, true); err != nil {
		return nil, err
	}
	if marked {
		doc = append(slices.Clone(byteOrderMark), doc...)
	}
	res.File.New = doc
	return res, nil
}

// planRemove takes every handler of Rune's out of the file, then the groups
// and event lists that become empty, one splice at a time on a fresh parse,
// so that each step is the simplest case. The hooks object stays, and so
// does the file.
func planRemove(res *ObserveResult, body []byte, marked bool) (*ObserveResult, error) {
	doc := body
	for {
		root, err := parseJSON(ObservePath, doc)
		if err != nil {
			return nil, err
		}
		next, event, ok := removeOne(doc, root)
		if !ok {
			break
		}
		doc = next
		res.File.Changes = append(res.File.Changes, "removed the handler on "+event)
	}
	if bytes.Equal(doc, body) {
		return res, nil
	}
	if err := verifyObserve(body, doc, false); err != nil {
		return nil, err
	}
	if marked {
		doc = append(slices.Clone(byteOrderMark), doc...)
	}
	res.File.New = doc
	return res, nil
}

// removeOne takes out the first handler of Rune's it finds: the handler
// alone when its group has others, the group when its event has others, and
// the event otherwise.
func removeOne(doc []byte, root *jsonNode) ([]byte, string, bool) {
	hooks := root.member("hooks")
	if hooks == nil || hooks.kind != jsonObjectKind {
		return nil, "", false
	}
	for _, event := range ObserveEvents() {
		groups := hooks.member(event)
		if groups == nil || groups.kind != jsonListKind {
			continue
		}
		for gi, group := range groups.elems {
			if group.kind != jsonObjectKind {
				continue
			}
			handlers := group.member("hooks")
			if handlers == nil || handlers.kind != jsonListKind {
				continue
			}
			hi := slices.IndexFunc(handlers.elems, isObserveHandler)
			switch {
			case hi < 0:
				continue
			case len(handlers.elems) > 1:
				return removeItem(doc, handlers, hi), event, true
			case len(groups.elems) > 1:
				return removeItem(doc, groups, gi), event, true
			}
			// The member that member() reads: the last one of that name.
			mi := len(hooks.members) - 1
			for hooks.members[mi].key != event {
				mi--
			}
			return removeItem(doc, hooks, mi), event, true
		}
	}
	return nil, "", false
}

// observeHandler reports whether the event already has a handler of Rune's,
// or a value in the way of adding one.
func observeHandler(doc []byte, event string) (present bool, conflict *Conflict, err error) {
	root, err := parseJSON(ObservePath, doc)
	if err != nil {
		return false, nil, err
	}
	if root.end == 0 {
		return false, nil, nil
	}
	if root.kind != jsonObjectKind {
		return false, conflictAt(ObservePath, doc, root, ObservePath+" is not an object; rune agent observe cannot add to it", false), nil
	}
	hooks := root.member("hooks")
	if hooks == nil {
		return false, nil, nil
	}
	if hooks.kind != jsonObjectKind {
		return false, conflictAt(ObservePath, doc, hooks, "hooks is not an object; rune agent observe cannot add to it", false), nil
	}
	groups := hooks.member(event)
	if groups == nil {
		return false, nil, nil
	}
	if groups.kind != jsonListKind {
		return false, conflictAt(ObservePath, doc, groups, "hooks."+event+" is not a list; rune agent observe cannot add to it", false), nil
	}
	for _, group := range groups.elems {
		if group.kind != jsonObjectKind {
			continue
		}
		handlers := group.member("hooks")
		if handlers == nil {
			continue
		}
		if handlers.kind != jsonListKind {
			return false, conflictAt(ObservePath, doc, handlers, "a group of hooks."+event+" has hooks that is not a list; rune agent observe cannot read it", false), nil
		}
		if slices.ContainsFunc(handlers.elems, isObserveHandler) {
			present = true
		}
	}
	return present, nil, nil
}

// isObserveHandler reports whether a handler is Rune's: an object whose
// command is ObserveCommand, whatever else it holds.
func isObserveHandler(n *jsonNode) bool {
	if n.kind != jsonObjectKind {
		return false
	}
	cmd := n.member("command")
	return cmd != nil && cmd.kind == jsonScalarKind && cmd.value == ObserveCommand
}

// jsonAppendValue adds value at the end of the list at path, creating the
// list and the objects on the way. The values on the way have been checked:
// each is an object, and the last is a list.
func jsonAppendValue(doc []byte, path []string, value any) ([]byte, error) {
	doc, root, conflict, err := openJSON(ObservePath, doc)
	if err != nil {
		return nil, err
	}
	if conflict != nil {
		return nil, errors.New(conflict.Msg)
	}
	node, rest, conflict := descend(ObservePath, doc, root, path)
	if conflict != nil {
		return nil, errors.New(conflict.Msg)
	}
	nl, unit := lineBreak(doc), indentUnit(doc, root)
	if len(rest) > 0 {
		item := memberItem(unit, nl, rest[0], nested(rest[1:], []any{value}))
		return insertItems(doc, root, node, []func(string, bool) string{item}), nil
	}
	render := func(indent string, inline bool) string { return renderJSON(value, indent, unit, nl, inline) }
	return insertItems(doc, root, node, []func(string, bool) string{render}), nil
}

// verifyObserve checks a planned edit against the meaning of the old text:
// decoded, the new text must be the old one with exactly one group of
// Rune's added to each event that lacked a handler (added), or with Rune's
// handlers and what they leave empty taken out (removed).
func verifyObserve(old, edited []byte, added bool) error {
	before, err := parseJSON(ObservePath, old)
	if err != nil {
		return err
	}
	after, err := parseJSON(ObservePath, edited)
	if err != nil {
		return fmt.Errorf("%s: the planned text does not parse: %w", ObservePath, err)
	}
	want, ok := decodeJSON(before).(map[string]any)
	if !ok {
		return fmt.Errorf("%s is not an object", ObservePath)
	}
	if added {
		addObserveGroups(want)
	} else {
		removeObserveHandlers(want)
	}
	if !reflect.DeepEqual(decodeJSON(after), any(want)) {
		return errors.New(ObservePath + ": the planned change did not verify; this is a bug in rune agent observe")
	}
	return nil
}

// addObserveGroups adds Rune's group, as plain values, to each event of a
// decoded settings file that has no handler of Rune's.
func addObserveGroups(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	for _, event := range ObserveEvents() {
		groups, _ := hooks[event].([]any)
		if !slices.ContainsFunc(groups, hasPlainHandler) {
			hooks[event] = append(groups, plainValue(observeGroup()))
		}
	}
}

// removeObserveHandlers takes Rune's handlers out of a decoded settings
// file, then the groups and event lists they leave empty.
func removeObserveHandlers(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	for _, event := range ObserveEvents() {
		groups, ok := hooks[event].([]any)
		if !ok {
			continue
		}
		var kept []any
		removed := false
		for _, g := range groups {
			group, isObject := g.(map[string]any)
			handlers, isList := group["hooks"].([]any)
			if !isObject || !isList {
				kept = append(kept, g)
				continue
			}
			left := slices.DeleteFunc(slices.Clone(handlers), isPlainHandler)
			switch {
			case len(left) == len(handlers):
				kept = append(kept, g)
			case len(left) > 0:
				removed = true
				group["hooks"] = left
				kept = append(kept, group)
			default:
				removed = true
			}
		}
		// A list that was empty before is the person's, and stays.
		switch {
		case !removed:
		case len(kept) == 0:
			delete(hooks, event)
		default:
			hooks[event] = kept
		}
	}
}

func hasPlainHandler(g any) bool {
	group, _ := g.(map[string]any)
	handlers, _ := group["hooks"].([]any)
	return slices.ContainsFunc(handlers, isPlainHandler)
}

func isPlainHandler(h any) bool {
	handler, _ := h.(map[string]any)
	return handler != nil && handler["command"] == ObserveCommand
}

// plainValue turns a value to write into the form decodeJSON gives,
// through lists too.
func plainValue(v any) any {
	switch v := v.(type) {
	case jsonObject:
		m := make(map[string]any, len(v))
		for _, f := range v {
			m[f.key] = plainValue(f.value)
		}
		return m
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = plainValue(e)
		}
		return out
	}
	return plain(v)
}

// observeNotes reads the deny rules of both settings files: a rule that
// takes the Bash tool away means the agent runs no shell command, so there
// is nothing to record. Rune never changes that rule.
func observeNotes(in ObserveInputs, local []byte) ([]string, error) {
	var notes []string
	denied := false
	for _, path := range []string{ObservePath, claudeSettingsPath} {
		doc := in.Files[path]
		if path == ObservePath {
			doc = local
		} else {
			doc = bytes.TrimPrefix(doc, byteOrderMark)
		}
		root, err := parseJSON(path, doc)
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"permissions", "deny"} {
			if root = root.member(key); root == nil {
				break
			}
		}
		if root == nil {
			continue
		}
		for _, rule := range root.elems {
			if name, _ := rule.value.(string); name == observeMatcher || strings.ReplaceAll(name, " ", "") == observeMatcher+"(*)" {
				denied = true
			}
		}
	}
	if denied {
		notes = append(notes, "Claude Code's settings deny the Bash tool here, so the agent runs no shell command and nothing is recorded. Rune does not change that rule.")
	}
	notes = append(notes, fmt.Sprintf("start Claude Code in %s, the project this file belongs to; commands are recorded for that project.", in.ShownDir))
	return notes, nil
}

// ObserveReport is what a run prints: the file's status, what observing
// means, and the notes.
func ObserveReport(res *ObserveResult, dryRun bool) string {
	var b strings.Builder
	status := res.File.Status()
	if dryRun && status != "unchanged" {
		status = "would be " + status
	}
	fmt.Fprintf(&b, "%s: %s\n", res.File.Path, status)
	if res.removed {
		b.WriteString("not observing: what was recorded stays until it expires; delete it with: rune suggest forget\n")
	} else {
		b.WriteString("observing: the shell commands Claude Code runs in this project are recorded on this\n" +
			"  machine, without their output, for up to 30 days. See them with: rune suggest\n")
	}
	for _, note := range res.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	if dryRun {
		b.WriteString("dry run: nothing written\n")
	}
	return b.String()
}

// ObserveDiff is the unified difference of the settings file.
func ObserveDiff(res *ObserveResult) string {
	f := res.File
	return UnifiedDiff(f.Path, f.Old, f.New, f.Existed)
}
