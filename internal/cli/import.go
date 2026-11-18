package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/analysis"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/taskimport"
	"github.com/rune-task-runner/rune/internal/token"
)

// `rune import` writes a Runefile from the task file of another runner (spec
// 054). It reads that one file and runs nothing. It is not the `import`
// statement of a Runefile.

// ImportFlags are the flags of `rune import`, as the command line gave them.
type ImportFlags struct {
	From   string // --from
	Output string // --output
	DryRun bool   // --dry-run
	Wrap   bool   // --wrap
	Force  bool   // --force
}

// Import converts the task file that source names, or the one found in the
// directory it names (the working directory when it is empty), and writes
// the Runefile. The report goes to standard error; standard output carries
// only the Runefile of a dry run.
func Import(opts Options, flags ImportFlags, source string) error {
	path, shown, format, err := importSource(opts.Cwd, source, flags.From)
	if err != nil {
		return &UsageError{Err: err}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &UsageError{Err: err}
	}
	out, err := importOutput(opts.Cwd, path, shown, flags)
	if err != nil {
		return &UsageError{Err: err}
	}

	res, err := taskimport.Convert(data, taskimport.Options{
		Format:     format,
		SourceName: shown,
		RunnerPath: filepath.Base(path),
		Wrap:       flags.Wrap,
	})
	var bad *taskimport.ParseError
	if errors.As(err, &bad) {
		renderDiags(opts, diag.List{diag.New(lineSpan(shown, data, bad.Line), bad.Msg)}, func(string) ([]byte, bool) { return data, true })
		return &ValidationError{Err: errorf("%s cannot be imported", shown)}
	}
	if err != nil {
		return &UsageError{Err: err}
	}
	if len(res.Tasks) == 0 {
		return usagef("%s has no tasks to import", shown)
	}
	if err := checkGenerated(opts, out, res.Runefile); err != nil {
		return err
	}

	fmt.Fprint(opts.Stderr, res.Report)
	if filepath.Dir(out) != filepath.Dir(path) {
		fmt.Fprintf(opts.Stderr, "note: tasks run from %s, the directory of the Runefile\n", showPath(opts.Cwd, filepath.Dir(out)))
	}
	if flags.DryRun {
		fmt.Fprint(opts.Stdout, res.Runefile)
		fmt.Fprintln(opts.Stderr, "dry run: nothing written")
	} else {
		if err := os.WriteFile(out, []byte(res.Runefile), 0o644); err != nil {
			return &CommandFailure{Err: err}
		}
		fmt.Fprintf(opts.Stderr, "wrote %s\n", showPath(opts.Cwd, out))
	}

	if res.Unsupported > 0 {
		return &CommandFailure{Err: errorf("import: %d of %d items were not converted", res.Unsupported, res.Lines)}
	}
	return nil
}

// importSource finds the file to import. arg is a file, a directory to look
// in, or empty for the working directory. shown is the path as the report
// prints it.
func importSource(cwd, arg, from string) (path, shown string, format taskimport.Format, err error) {
	if from != "" {
		if format, err = taskimport.ParseFormat(from); err != nil {
			return "", "", "", err
		}
	}
	target := absIn(cwd, arg)
	info, err := os.Stat(target)
	if err != nil {
		return "", "", "", err
	}
	if !info.IsDir() {
		if format == "" {
			var known bool
			if format, known = taskimport.FormatOf(filepath.Base(target)); !known {
				return "", "", "", errorf("cannot tell the format of %s; pass --from justfile, make, task or npm", filepath.ToSlash(arg))
			}
		}
		return target, filepath.ToSlash(arg), format, nil
	}

	// The listing is read, so that a file counts once on a file system that
	// would also open it under a name of another case.
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", "", "", err
	}
	var found []string
	names := taskimport.Candidates(format)
	for _, name := range names {
		if slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !e.IsDir() && e.Name() == name }) {
			found = append(found, name)
		}
	}
	dir := "."
	if arg != "" {
		dir = filepath.ToSlash(arg)
	}
	switch len(found) {
	case 0:
		return "", "", "", errorf("no task file found in %s (looked for %s); name one: rune import <path>", dir, strings.Join(names, ", "))
	case 1:
		format, _ = taskimport.FormatOf(found[0])
		return filepath.Join(target, found[0]), filepath.ToSlash(filepath.Join(arg, found[0])), format, nil
	}
	return "", "", "", errorf("more than one task file in %s: %s; name one: rune import <path>", dir, strings.Join(found, ", "))
}

// importOutput decides where the Runefile goes and refuses a destination
// that would lose a file or leave a directory with two Runefiles.
func importOutput(cwd, source, shown string, flags ImportFlags) (string, error) {
	srcDir := filepath.Dir(source)
	out := filepath.Join(srcDir, "Runefile")
	if flags.Output != "" {
		out = absIn(cwd, flags.Output)
	} else if existing := runefilesIn(srcDir, ""); len(existing) > 0 {
		out = filepath.Join(srcDir, existing[0])
	}
	outDir := filepath.Dir(out)

	if config.IsRunefileName(filepath.Base(out)) {
		if others := runefilesIn(outDir, out); len(others) > 0 {
			return "", errorf("%s already has %s; a directory holds one Runefile", showPath(cwd, outDir), others[0])
		}
	}
	if flags.Wrap && outDir != srcDir {
		return "", errorf("--wrap needs the Runefile next to %s, because a wrapper runs the original runner there", shown)
	}
	// The source is never changed, not even with --force. The files are
	// compared, so that another spelling of the path or a link is caught.
	if !flags.DryRun {
		srcInfo, srcErr := os.Stat(source)
		outInfo, outErr := os.Stat(out)
		if srcErr == nil && outErr == nil && os.SameFile(srcInfo, outInfo) {
			return "", errorf("%s is the source, which an import never changes; write to another file with --output <path>", showPath(cwd, out))
		}
	}
	if !flags.DryRun && !flags.Force {
		if _, err := os.Stat(out); err == nil {
			return "", errorf("%s already exists; write to another file with --output <path>, or replace it with --force", showPath(cwd, out))
		}
	}
	return out, nil
}

// runefilesIn lists the files of dir that Rune takes for a Runefile, other
// than the file at except.
func runefilesIn(dir, except string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	skip, _ := os.Stat(except)
	var names []string
	for _, e := range entries {
		if e.IsDir() || !config.IsRunefileName(e.Name()) {
			continue
		}
		// The same file can have two spellings where case is ignored.
		if info, err := e.Info(); err == nil && skip != nil && os.SameFile(info, skip) {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// checkGenerated analyzes the text of the Runefile before it is written. An
// error here is a defect of the importer, never of the source, so nothing is
// written. Warnings are expected: an imported task may have no description.
func checkGenerated(opts Options, path, text string) error {
	svc := analysis.NewService(analysis.DiskSourceStore{})
	snap, err := svc.Analyze(opts.ctx(), analysis.AnalyzeRequest{URI: path, Content: text})
	if err != nil {
		return &CommandFailure{Err: err}
	}
	if !snap.Diagnostics.HasErrors() {
		return nil
	}
	var errs diag.List
	for _, d := range snap.Diagnostics {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	renderDiags(opts, errs, snap.Sources)
	return &CommandFailure{Err: errorf("import: the generated Runefile is not valid; this is a bug in rune import")}
}

// lineSpan is the span of one whole line of a source, so that an error in a
// file of another format is shown as a Runefile error is: with the line and
// a caret under it.
func lineSpan(file string, data []byte, line int) token.Span {
	lines := strings.Split(string(data), "\n")
	width := 1
	if line >= 1 && line <= len(lines) {
		width = max(1, len(strings.TrimRight(lines[line-1], "\r")))
	}
	return token.Span{
		File:  file,
		Start: token.Position{Line: line, Col: 1},
		End:   token.Position{Line: line, Col: 1 + width},
	}
}

// absIn resolves a path the person gave against the working directory.
func absIn(cwd, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}

// showPath prints a path as the person would type it: relative to the
// working directory when it is inside it, with forward slashes.
func showPath(cwd, path string) string {
	if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		path = rel
	}
	return filepath.ToSlash(path)
}
