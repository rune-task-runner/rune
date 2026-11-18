package taskimport

import (
	"fmt"
	"slices"
	"strings"
)

// Format is a task file format that can be imported.
type Format string

// The formats, named as `rune import --from` takes them.
const (
	FormatJust Format = "justfile"
	FormatMake Format = "make"
	FormatTask Format = "task"
	FormatNPM  Format = "npm"
)

// formats lists every format with the file names it is found by, in the order
// the candidates are reported.
var formats = []struct {
	format Format
	names  []string
}{
	{FormatJust, []string{"justfile", "Justfile", ".justfile"}},
	{FormatMake, []string{"GNUmakefile", "makefile", "Makefile"}},
	{FormatTask, []string{"Taskfile.yml", "Taskfile.yaml", "taskfile.yml", "taskfile.yaml"}},
	{FormatNPM, []string{"package.json"}},
}

// ParseFormat reads a `--from` value.
func ParseFormat(s string) (Format, error) {
	for _, f := range formats {
		if string(f.format) == s {
			return f.format, nil
		}
	}
	return "", fmt.Errorf("unknown format %q (want justfile, make, task or npm)", s)
}

// FormatOf tells the format of a file from its base name.
func FormatOf(name string) (Format, bool) {
	for _, f := range formats {
		if slices.Contains(f.names, name) {
			return f.format, true
		}
	}
	return "", false
}

// Candidates lists the file names a directory is searched for: those of one
// format, or of all four when format is empty.
func Candidates(format Format) []string {
	var names []string
	for _, f := range formats {
		if format == "" || f.format == format {
			names = append(names, f.names...)
		}
	}
	return names
}

// Runner is the program that runs a source of this format.
func (f Format) Runner() string {
	if f == FormatJust {
		return "just"
	}
	return string(f)
}

// Status says what happened to one source task.
type Status int

// The statuses. A task only ever moves away from Converted.
const (
	Converted   Status = iota // behaves as the source task did
	Review                    // converted, with a difference the notes name
	Unsupported               // not converted: the task fails with the reason
	Wrapper                   // not converted: the task runs the original runner
)

func (s Status) String() string {
	return [...]string{"converted", "review", "unsupported", "wrapper"}[s]
}

// Call is a task that another task runs before or after its own body.
type Call struct {
	Name string // the source name of the called task
	Args string // its arguments as Runefile text, or ""
}

// Task is one recipe, target, task or script of the source.
type Task struct {
	Source string   // the name in the source file
	Name   string   // the Rune name, set by the shared step
	Line   int      // 1-based line in the source
	Doc    string   // one line of description
	Attrs  []string // Runefile attributes without brackets
	Params []string // Runefile parameters
	Deps   []Call   // run before the body
	Post   []Call   // run after the body succeeds
	Body   []string // Runefile body lines, prefixes included
	Status Status
	Notes  []string // why the status is not Converted
	NoWrap bool     // the runner cannot run this task by name
}

// unsupported marks the task as not converted, for the given reason.
func (t *Task) unsupported(reason string) {
	t.Status = Unsupported
	t.Notes = append(t.Notes, oneLine(reason))
}

// review keeps the task converted and records a difference from the source.
func (t *Task) review(note string) {
	if t.Status == Converted {
		t.Status = Review
	}
	t.Notes = append(t.Notes, oneLine(note))
}

// oneLine keeps the first line of a note. A note can quote source text, and
// it is written on one line of the report and of the Runefile: a line break
// in it would start a new line of the Runefile.
func oneLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}

// Var is a top-level variable that converted.
type Var struct {
	Name string
	Expr string // a Runefile expression
}

// Item is a construct outside a task that was not converted.
type Item struct {
	Line   int
	Name   string
	Note   string
	Global bool // it changes how every task of the source runs
}

// source is what a reader returns.
type source struct {
	vars  []Var
	tasks []*Task
	items []Item
}

// Options are the inputs of one conversion besides the source text.
type Options struct {
	Format     Format
	SourceName string // the source path as the report prints it
	RunnerPath string // the source path as a wrapper passes it to the runner
	Wrap       bool   // turn unsupported tasks into wrappers
}

// Result is a finished conversion.
type Result struct {
	Tasks    []*Task
	Items    []Item
	Runefile string
	Report   string
	Lines    int // report lines: one per task and per item

	// Unsupported is how many of the lines are unsupported. Above zero, the
	// import is partial.
	Unsupported int
}

// ParseError says that the source is not valid in its format.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// Convert turns the text of a source file into a Runefile and a report. It
// reads nothing but data and starts nothing.
func Convert(data []byte, opts Options) (*Result, error) {
	var (
		src *source
		err error
	)
	// Some editors start a file with a byte order mark. It is not text of the
	// first line, and encoding/json refuses it where npm does not.
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	switch opts.Format {
	case FormatJust:
		src, err = readJust(text)
	case FormatMake:
		src, err = readMake(text)
	case FormatTask:
		src, err = readTaskfile(text)
	case FormatNPM:
		src, err = readNPM(text)
	default:
		return nil, fmt.Errorf("unknown format %q (want justfile, make, task or npm)", opts.Format)
	}
	if err != nil {
		return nil, err
	}
	if err := finish(src, opts); err != nil {
		return nil, err
	}
	res := &Result{Tasks: src.tasks, Items: src.items}
	res.Runefile = emitRunefile(src, opts)
	res.Report, res.Lines, res.Unsupported = emitReport(src, opts)
	return res, nil
}
