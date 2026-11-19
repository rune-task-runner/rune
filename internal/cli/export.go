package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rune-task-runner/rune/internal/agentdocs"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/taskimport"
)

// `rune export <format>` writes, from the Runefile, the text an agent with
// no MCP integration needs to find and call the tasks (spec 056): a section
// for AGENTS.md, or a Claude skill. internal/agentdocs builds the catalog,
// renders it and plans the files from bytes to bytes; this file finds the
// Runefile, reads and writes the targets and maps the result to an exit
// status.
//
// It runs nothing of the Runefile and evaluates nothing of it, and it reads
// neither the environment nor a dotenv file.

// exportTaskNote is printed when the Runefile has a task named export,
// which the command's name hides on the command line.
const exportTaskNote = `note: "export" is also a task of this Runefile; run it with: rune -- export`

// exportTaskHint ends every error about the arguments: a person who typed
// `rune export deploy` may have meant the task.
const exportTaskHint = "; to run a task named export, use: rune -- export"

// exportScope is what insideProject says for a documented location.
const exportScope = "rune export --write writes only inside the project; name the path with --output"

// The documented locations, relative to the directory of the Runefile.
const (
	agentsFile = "AGENTS.md"
	skillsDir  = ".claude/skills"
)

// ExportFlags are the flags of `rune export`, as the command line gave them.
type ExportFlags struct {
	Write  bool   // --write
	Output string // --output
	Check  bool   // --check

	// write writes one file. Nil in the command; tests set it.
	write func(path string, data []byte) error
}

// Export prints, writes or checks the export of the Runefile in the format
// args names. The export goes to standard output unless it is written or
// checked; notes and the report go to standard error.
func Export(opts Options, flags ExportFlags, args []string) error {
	runefile, resolveErr := config.Resolve(opts.File, opts.Cwd)
	if resolveErr == nil && hasTask(runefile, "export") {
		fmt.Fprintln(opts.Stderr, exportTaskNote)
	}
	format, err := exportFormat(args)
	if err != nil {
		return &UsageError{Err: err}
	}
	if resolveErr != nil {
		return &UsageError{Err: resolveErr}
	}
	if flags.Check && flags.Write {
		return usagef("--check cannot be combined with --write: a check writes nothing")
	}

	file, _, err := loadStatic(opts, runefile, false, &pathRecorder{})
	if err != nil {
		return err
	}
	catalog := exportCatalog(file)

	if !flags.Write && !flags.Check && flags.Output == "" {
		return printExport(opts, format, catalog)
	}
	target, err := resolveExportTarget(opts.Cwd, filepath.Dir(runefile), format, flags.Output)
	if err != nil {
		return err
	}
	files, err := planExport(opts, format, catalog, target)
	if err != nil {
		return err
	}
	if flags.Check {
		return checkExport(opts, flags, format, catalog, target, files)
	}
	return writeExport(opts, flags, target, files)
}

// exportFormat reads the one argument of the command. It is the one place
// that knows the formats, so the cobra command accepts any arguments.
func exportFormat(args []string) (agentdocs.Format, error) {
	if len(args) != 1 {
		return "", errors.New("rune export needs one format: " + strings.Join(agentdocs.Formats(), " or ") + exportTaskHint)
	}
	format, ok := agentdocs.ParseFormat(args[0])
	if !ok {
		return "", fmt.Errorf(`unknown format %q for "rune export" (%s)%s`, args[0], strings.Join(agentdocs.Formats(), " or "), exportTaskHint)
	}
	return format, nil
}

// exportCatalog builds the catalog of a loaded Runefile. Every task counts
// as available, so what a run can reach is stated the same on every host.
func exportCatalog(file *ast.File) agentdocs.Catalog {
	tasks := indexTasks(file)
	everywhere := func(*ast.Task) bool { return true }
	return agentdocs.Build(agentdocs.Source{
		Tasks:    file.Tasks,
		Reach:    func(t *ast.Task) []*ast.Task { return reachTasks(tasks, t, everywhere) },
		Commands: taskimport.RuneCommands,
	})
}

// printExport prints the export on standard output: the section, or
// SKILL.md of a skill.
func printExport(opts Options, format agentdocs.Format, c agentdocs.Catalog) error {
	if format == agentdocs.AgentsMD {
		fmt.Fprint(opts.Stdout, agentdocs.Section(c))
		return nil
	}
	skill, tasks := agentdocs.SkillFiles(c, agentdocs.DefaultSkillName)
	fmt.Fprint(opts.Stdout, skill)
	if tasks != "" {
		fmt.Fprintf(opts.Stderr, "note: %s holds the entries of the %d tasks and is part of this skill; it is not printed. Write both files with --write\n", agentdocs.TasksFile, len(c.Entries))
	}
	return nil
}

// exportTarget is where an export is written or checked: a Markdown file
// for agents-md, a skill directory for claude-skill.
type exportTarget struct {
	path string // absolute
	// skill is the name of the skill, which is the name of its directory.
	// Empty for agents-md.
	skill string
}

// file is the path of one planned file of the target.
func (t exportTarget) file(rel string) string {
	if rel == "" {
		return t.path
	}
	return filepath.Join(t.path, rel)
}

// resolveExportTarget finds the target: the path of --output against the
// working directory, else the documented location in the project. Only the
// documented location is held inside the project: a path a person names is
// that person's choice.
func resolveExportTarget(cwd, project string, format agentdocs.Format, output string) (exportTarget, error) {
	var t exportTarget
	switch {
	case output != "":
		t.path = absIn(cwd, output)
	case format == agentdocs.AgentsMD:
		t.path = filepath.Join(project, agentsFile)
	default:
		t.path = filepath.Join(project, filepath.FromSlash(skillsDir), agentdocs.DefaultSkillName)
	}
	info, statErr := os.Stat(t.path)
	if format == agentdocs.AgentsMD {
		if output != "" && statErr == nil && info.IsDir() {
			return t, usagef("--output %s is a directory; agents-md is written to a file", filepath.ToSlash(output))
		}
	} else {
		t.skill = filepath.Base(t.path)
		if err := agentdocs.CheckSkillName(t.skill); err != nil {
			return t, usagef("--output %s: %q cannot name a skill: %v", filepath.ToSlash(output), t.skill, err)
		}
		if statErr == nil && !info.IsDir() {
			return t, usagef("%s is a file; a skill is written to a directory", showPath(cwd, t.path))
		}
	}
	if output == "" {
		for _, rel := range exportFiles(format) {
			if err := insideProject(project, t.file(rel), exportScope); err != nil {
				return t, &UsageError{Err: fmt.Errorf("export: %w", err)}
			}
		}
	}
	return t, nil
}

// exportFiles lists the files a format can have at its target.
func exportFiles(format agentdocs.Format) []string {
	if format == agentdocs.AgentsMD {
		return []string{""}
	}
	return []string{agentdocs.SkillFile, agentdocs.TasksFile}
}

// planExport reads the files of the target and plans the export into them.
// A target Rune will not change is reported here, for a write and a check
// alike, before anything is written.
func planExport(opts Options, format agentdocs.Format, c agentdocs.Catalog, t exportTarget) ([]agentdocs.File, error) {
	existing := map[string][]byte{}
	for _, rel := range exportFiles(format) {
		data, err := os.ReadFile(t.file(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, &CommandFailure{Err: fmt.Errorf("%s: %w", showPath(opts.Cwd, t.file(rel)), err)}
		}
		existing[rel] = data
	}

	var files []agentdocs.File
	var err error
	if format == agentdocs.AgentsMD {
		old, existed := existing[""]
		var f agentdocs.File
		f, err = agentdocs.PlanSection(c, old, existed)
		files = []agentdocs.File{f}
	} else {
		files, err = agentdocs.PlanSkill(c, t.skill, existing)
	}
	var refusal *agentdocs.Refusal
	if !errors.As(err, &refusal) {
		return files, err
	}
	shown := showPath(opts.Cwd, t.file(refusal.Path))
	if refusal.Path != "" {
		return nil, usagef("export: %s was not generated by rune export, so it is not replaced; move it away or write the skill to another directory with --output, nothing written", shown)
	}
	renderMarkers(opts, shown, existing[""], refusal.Markers)
	return nil, usagef(`export: %s has marker lines that are not one "rune:begin tasks" line followed by one "rune:end tasks" line; fix them by hand, nothing written`, shown)
}

// renderMarkers prints each marker line of a refused file as a Runefile
// error is printed: the file and line, the line itself and a caret under it.
func renderMarkers(opts Options, shown string, data []byte, markers []agentdocs.Marker) {
	// A byte order mark is not part of the first line, and must not widen
	// the caret under it.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	list := make(diag.List, len(markers))
	for i, m := range markers {
		msg := `a "rune:end tasks" line`
		if m.Begin {
			msg = `a "rune:begin tasks" line`
		}
		list[i] = diag.New(lineSpan(shown, data, m.Line), msg)
	}
	renderDiags(opts, list, func(string) ([]byte, bool) { return data, true })
}

// writeExport writes the files that changed and reports each file.
func writeExport(opts Options, flags ExportFlags, t exportTarget, files []agentdocs.File) error {
	var planned []plannedFile
	for _, f := range files {
		if f.Status() == "unchanged" {
			continue
		}
		planned = append(planned, plannedFile{
			target:  t.file(f.Path),
			shown:   showPath(opts.Cwd, t.file(f.Path)),
			old:     f.Old,
			new:     f.New,
			existed: f.Existed,
			remove:  f.Remove,
		})
	}
	if err := writePlanned(planned, flags.write); err != nil {
		return err
	}
	for _, f := range files {
		line := showPath(opts.Cwd, t.file(f.Path)) + ": " + f.Status()
		if status := f.Status(); status == "created" || status == "updated" {
			line += fmt.Sprintf(": %s, %d bytes", plural(f.Tasks, "task"), f.Size)
		}
		fmt.Fprintln(opts.Stderr, line)
	}
	return nil
}

// checkExport reports whether each file of the target is what a write would
// leave, and names the command that regenerates the export when one is not.
// It writes nothing.
func checkExport(opts Options, flags ExportFlags, format agentdocs.Format, c agentdocs.Catalog, t exportTarget, files []agentdocs.File) error {
	fingerprint := c.Fingerprint()
	current := true
	for _, f := range files {
		state := f.CheckState(fingerprint)
		current = current && state == agentdocs.StateCurrent
		fmt.Fprintf(opts.Stderr, "%s: %s\n", showPath(opts.Cwd, t.file(f.Path)), state)
	}
	if current {
		return nil
	}
	command := "rune export " + string(format)
	if opts.File != "" {
		command += " -f " + shellWord(opts.File, runtime.GOOS)
	}
	if flags.Output != "" {
		command += " --output " + shellWord(flags.Output, runtime.GOOS)
	} else {
		command += " --write"
	}
	fmt.Fprintln(opts.Stderr, "regenerate with: "+command)
	return &CommandFailure{Err: errorf("export: %s is not current", showPath(opts.Cwd, t.path))}
}
