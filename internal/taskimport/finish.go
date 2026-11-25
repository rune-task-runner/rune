package taskimport

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// RuneCommands are the names rune takes as its own commands: `rune help`
// runs the command, and a task of that name is run with `rune -- help`. No
// task can be named import, which is a keyword.
var RuneCommands = map[string]bool{
	"help": true, "version": true, "serve": true, "mcp": true, "log": true,
	"agent": true, "export": true, "analyze": true, "lsp": true, "completion": true,
	"suggest": true,
}

// finish applies the rules that hold for every format: a task that is
// referred to and not defined exists as a failing task, every task has a
// distinct Rune name, a file-wide construct lowers every converted task to
// review, and an unsupported task stays in the Runefile as a task that fails
// or, with opts.Wrap, as a wrapper around the original runner. A dependency
// cycle is an error of the source: no runner and no Runefile can run it.
func finish(src *source, opts Options) error {
	defined := make(map[string]bool, len(src.tasks))
	for _, t := range src.tasks {
		defined[t.Source] = true
	}
	for _, t := range slices.Clone(src.tasks) {
		if t.Status == Unsupported {
			continue
		}
		for _, c := range slices.Concat(t.Deps, t.Post) {
			if defined[c.Name] {
				continue
			}
			defined[c.Name] = true
			src.tasks = append(src.tasks, &Task{
				Source: c.Name, Line: t.Line, Status: Unsupported,
				Notes: []string{"not defined in " + opts.SourceName}, NoWrap: true,
			})
		}
	}

	if err := findCycle(src.tasks); err != nil {
		return err
	}

	sources := make([]string, len(src.tasks))
	for i, t := range src.tasks {
		sources[i] = t.Source
	}
	names := make(map[string]string, len(sources))
	for i, name := range mapNames(sources) {
		src.tasks[i].Name = name
		names[sources[i]] = name
	}

	var global []string
	for _, it := range src.items {
		if it.Global {
			global = append(global, fmt.Sprintf("see %s:%d", opts.SourceName, it.Line))
		}
	}

	for _, t := range src.tasks {
		if t.Status == Unsupported {
			stub(t, opts)
			continue
		}
		for i := range t.Deps {
			t.Deps[i].Name = names[t.Deps[i].Name]
		}
		for i := range t.Post {
			t.Post[i].Name = names[t.Post[i].Name]
		}
		for _, note := range global {
			t.review(note)
		}
	}
	for _, t := range src.tasks {
		if RuneCommands[t.Name] {
			note := fmt.Sprintf("%s is also a rune command: run the task with rune -- %s", t.Name, t.Name)
			if t.Status == Converted || t.Status == Review {
				t.review(note)
			} else {
				t.Notes = append(t.Notes, note)
			}
		}
		asks := slices.ContainsFunc(t.Attrs, func(a string) bool { return a == "confirm" || strings.HasPrefix(a, "confirm(") })
		if asks && len(t.Deps) > 0 && t.Status != Unsupported && t.Status != Wrapper {
			t.review("the source runner asks before the dependencies run; Rune asks after them")
		}
	}
	return nil
}

// findCycle reports the first dependency cycle among the tasks that
// convert. An unsupported task runs nothing, so it ends every path.
func findCycle(tasks []*Task) error {
	byName := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		byName[t.Source] = t
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[*Task]int{}
	var path []string
	var visit func(t *Task) error
	visit = func(t *Task) error {
		state[t] = visiting
		path = append(path, t.Source)
		for _, c := range slices.Concat(t.Deps, t.Post) {
			next := byName[c.Name]
			if next == nil || next.Status == Unsupported || state[next] == done {
				continue
			}
			if state[next] == visiting {
				cycle := append(path[slices.Index(path, next.Source):], next.Source)
				return &ParseError{next.Line, "dependency cycle: " + strings.Join(cycle, " -> ")}
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[t] = done
		return nil
	}
	for _, t := range tasks {
		if t.Status == Unsupported || state[t] != 0 {
			continue
		}
		if err := visit(t); err != nil {
			return err
		}
	}
	return nil
}

// stub replaces an unsupported task with a task that fails with the reason,
// or with a wrapper. It takes any arguments, so a call with arguments from a
// converted task still analyzes.
func stub(t *Task, opts Options) {
	if slices.Contains(t.Attrs, "private") {
		t.Attrs = []string{"private"}
	} else {
		t.Attrs = nil
	}
	t.Params = []string{"*args"}
	t.Deps, t.Post = nil, nil

	wrap := opts.Wrap && !t.NoWrap
	// A wrapper names the task on a body line, and a line break in the name
	// would start a new line of the Runefile.
	if wrap && strings.ContainsFunc(t.Source, unicode.IsControl) {
		t.Notes = append(t.Notes, "its name has a control character, so no wrapper can name it")
		wrap = false
	}
	if wrap {
		runner := opts.Format.Runner()
		command := runner + " " + t.Source
		if opts.Format == FormatNPM {
			command = "npm run " + t.Source
		}
		doc := fmt.Sprintf("Runs %q (wrapper, needs %s).", command, runner)
		if t.Doc != "" {
			doc += " " + t.Doc
		}
		t.Doc = doc
		t.Body = []string{wrapperCommand(t.Source, opts) + " {{quote_each(args)}}"}
		t.Status = Wrapper
		return
	}

	where := fmt.Sprintf("%s:%d", opts.SourceName, t.Line)
	why := strings.Join(t.Notes, "; ")
	t.Doc = fmt.Sprintf("Not imported (%s): %s", where, why)
	msg := fmt.Sprintf("rune import: %q (%s) was not imported: %s", t.Source, where, why)
	t.Body = []string{"@echo " + EscapeBraces("'"+strings.ReplaceAll(msg, "'", `'\''`)+"'") + " >&2; exit 1"}
}

// wrapperCommand is the command that runs one task of the source with its
// original runner. The source file is always named, so the command does not
// depend on which file names the runner looks for.
func wrapperCommand(name string, opts Options) string {
	file, name := shellQuote(opts.RunnerPath), shellQuote(name)
	switch opts.Format {
	case FormatJust:
		return "just --justfile " + file + " " + name
	case FormatMake:
		return "make -f " + file + " " + name
	case FormatTask:
		return "task --taskfile " + file + " " + name + " --"
	default:
		return "npm run " + name + " --"
	}
}
