package agentinit

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// byteOrderMark is what some editors put at the start of a UTF-8 file.
var byteOrderMark = []byte("\xEF\xBB\xBF")

// Client is an agent client `rune agent init` can set up.
type Client string

const (
	ClientClaude Client = "claude"
	ClientCodex  Client = "codex"
	ClientVSCode Client = "vscode"
)

// Clients lists the clients, in the order the help names them.
func Clients() []Client {
	return []Client{ClientClaude, ClientCodex, ClientVSCode}
}

// ClientList writes the clients as a sentence does: "claude, codex or
// vscode", with word before the last one.
func ClientList(word string) string {
	names := make([]string, len(Clients()))
	for i, c := range Clients() {
		names[i] = string(c)
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " " + word + " " + names[last]
}

// ParseClient reads a client name as the command line gives it.
func ParseClient(name string) (Client, error) {
	c := Client(name)
	if _, ok := adapterOf(c); !ok {
		return "", fmt.Errorf("unknown client %q: the clients are %s", name, ClientList("and"))
	}
	return c, nil
}

// Paths lists the files of a client, relative to the project directory,
// with forward slashes.
func Paths(c Client) []string {
	a, _ := adapterOf(c)
	return a.paths
}

// Binary is the program that reports a client's version.
func Binary(c Client) string {
	a, _ := adapterOf(c)
	return a.binary
}

// Inputs is what a plan is made from.
type Inputs struct {
	// Runefile is the absolute path of the project's Runefile, as it is
	// written into the registration.
	Runefile string
	// ShownRunefile and ShownDir are the Runefile and its directory as the
	// report prints them: quoted for a shell where they need it.
	ShownRunefile string
	ShownDir      string
	// Unpinned registers the server without the Runefile's path.
	Unpinned bool
	// Force replaces a value Rune owns that holds another value.
	Force bool
	// Files is the content of the client files that exist, by the path
	// Paths gives. A file that does not exist has no key.
	Files map[string][]byte
	// Version is what the client reported, or empty when it reported none
	// or was not asked yet; Result.SetVersion sets it after planning.
	Version string
}

// State is what a file held for one owned entry before the run.
type State string

const (
	StatePresent State = "present"
	StateMissing State = "missing"
	StateDiffers State = "differs"
)

// Entry is one value Rune owns in a client file.
type Entry struct {
	Name  string
	State State
}

// File is one client file after planning.
type File struct {
	Path     string // relative to the project directory, forward slashes
	Old, New []byte
	Existed  bool
	Changes  []string // what was set, for the report
	Entries  []Entry
}

// Status says what a run does to the file: "created", "updated" or
// "unchanged".
func (f File) Status() string {
	switch {
	case bytes.Equal(f.Old, f.New):
		return "unchanged"
	case !f.Existed:
		return "created"
	}
	return "updated"
}

// Conflict is something in a client file that stops a run: a value Rune
// would write differently, or a value it cannot add to.
type Conflict struct {
	File      string
	Line, Col int // where the value found starts, from 1
	Width     int // how many characters it covers on that line
	Msg       string
	// Forceable is true when --force replaces the value.
	Forceable bool
}

// ParseError reports a client file that is not valid JSON or TOML.
type ParseError struct {
	File      string
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// ManualError reports a file that holds its settings in a form Rune cannot
// add to in place, such as an inline table. Setting is what the person sets
// by hand, written as a dotted key and its value.
type ManualError struct {
	File      string
	Line, Col int // the construct that is in the way, or 1:1
	Setting   string
}

func (e *ManualError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Message())
}

// Message is the error without its position.
func (e *ManualError) Message() string {
	return "rune agent init cannot add to this file in place. Set this yourself: " + e.Setting
}

// OutcomeKind is one of the three outcomes a setup may be given (the setup
// outcome contract of spec 047). There is no fourth.
type OutcomeKind int

const (
	// RegisteredOnly: Rune's tools are there, and so are the client's
	// command-running tools.
	RegisteredOnly OutcomeKind = iota
	// RuneOnlyWithin: Rune's tools and no command-running tool, inside one
	// named scope.
	RuneOnlyWithin
	// RuneOnly: every session in the project has Rune's tools and no
	// command-running tool.
	RuneOnly
)

// Outcome is what a setup reached.
type Outcome struct {
	Kind    OutcomeKind
	Scope   string   // for RuneOnlyWithin
	Reason  string   // for RegisteredOnly: why
	Remains string   // for RegisteredOnly: the tools that were not checked
	Steps   []string // what is left to the person
}

// Result is a plan for one client.
type Result struct {
	Client    Client
	Version   string
	Files     []File
	Conflicts []Conflict
	Notes     []string
	Outcome   Outcome

	adapter  adapter
	shownDir string
	unpinned bool
}

// SetVersion records the version the client reported and works the outcome
// out for it. The command plans first and asks the client only when the
// plan can be carried out, so a run that stops on a conflict starts no
// program.
func (r *Result) SetVersion(version string) {
	r.Version = version
	r.Outcome = r.adapter.outcome(version, r.shownDir)
}

// planned is what an adapter's planner returns.
type planned struct {
	files     []File
	conflicts []Conflict
	notes     []string
}

// Plan works out what a run for client does to its files. It returns a
// *ParseError for a file that is not valid, and a *ManualError for one it
// cannot add to in place. With a conflict, no file is changed: every File
// has New equal to Old.
func Plan(client Client, in Inputs) (*Result, error) {
	if _, err := ParseClient(string(client)); err != nil {
		return nil, err
	}
	a, _ := adapterOf(client)
	// A file that starts with a byte order mark is planned without it and
	// keeps it: the mark is not part of the JSON or TOML text.
	marked := map[string]bool{}
	files := make(map[string][]byte, len(in.Files))
	for path, data := range in.Files {
		files[path], marked[path] = bytes.CutPrefix(data, byteOrderMark)
	}
	in.Files = files
	p, err := a.plan(in)
	if err != nil {
		return nil, err
	}
	for i := range p.files {
		if f := &p.files[i]; marked[f.Path] {
			f.Old, f.New = slices.Concat(byteOrderMark, f.Old), slices.Concat(byteOrderMark, f.New)
		}
	}
	if len(p.conflicts) > 0 {
		for i := range p.files {
			p.files[i].New, p.files[i].Changes = p.files[i].Old, nil
		}
	}
	notes := []string{
		"the server keeps the tasks it read at start (--no-reload). After an edit to the Runefile, start a new session.",
		"[confirm] tasks are not approved by this setup. Rune refuses them, or asks you for each call.",
		"the server is registered with this checkout's path. For a file you commit, use --unpinned.",
		"a task an agent calls runs in Rune's sandbox, which needs bubblewrap on Linux. Where there is none, each call is refused until the server runs with --sandbox off.",
	}
	if in.Unpinned {
		notes[2] = "the server serves the Runefile found from the directory the client starts it in."
	}
	return &Result{
		Client:    client,
		Version:   in.Version,
		Files:     p.files,
		Conflicts: p.conflicts,
		Notes:     append(notes, p.notes...),
		Outcome:   a.outcome(in.Version, in.ShownDir),
		adapter:   a,
		shownDir:  in.ShownDir,
		unpinned:  in.Unpinned,
	}, nil
}

// registration is the arguments of the registered server command.
func registration(in Inputs) []string {
	if in.Unpinned {
		return []string{"mcp", "--no-reload"}
	}
	return []string{"mcp", "--file", in.Runefile, "--no-reload"}
}

// serverRuns is the registered command as the report shows it.
func serverRuns(in Inputs) string {
	if in.Unpinned {
		return `server "rune" runs: rune mcp --no-reload`
	}
	return `server "rune" runs: rune mcp --file ` + in.ShownRunefile + " --no-reload"
}

// serverChanges words what was done to the keys of the server's entry. all
// is how many keys the entry has.
func serverChanges(in Inputs, added, replaced []string, all int) []string {
	var out []string
	if len(added) == all {
		out = append(out, serverRuns(in))
	} else {
		for _, key := range added {
			out = append(out, fmt.Sprintf(`added %s to server "rune"`, key))
		}
	}
	for _, key := range replaced {
		out = append(out, fmt.Sprintf(`replaced %s of server "rune"`, key))
	}
	return out
}
