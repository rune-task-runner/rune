package agentdocs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/formatter"
)

// Source is what a catalog is built from.
type Source struct {
	// Tasks are the tasks of the composed, analyzed Runefile, in file order.
	Tasks []*ast.Task
	// Reach lists the tasks a run of a task can reach through dependencies
	// and hooks, the task itself first. Nil reaches nothing.
	Reach func(*ast.Task) []*ast.Task
	// Commands are the names Rune takes as its own commands: a task of such
	// a name is run with `rune -- NAME`.
	Commands map[string]bool
}

// Catalog is the public tasks of one Runefile in listing order, with the
// facts an export states about each. Both formats are rendered from it.
type Catalog struct {
	Entries []Entry
}

// Entry is one public task. Every field is a fact the text states, and the
// fingerprint covers exactly these fields. The author's texts are held as
// the text shows them, with their white space collapsed: a comment that is
// wrapped at another column is the same description.
type Entry struct {
	Name      string
	Doc       string
	Separator bool // the name is a command of Rune, so the usage needs `--`
	Params    []Param
	Group     string
	OS        []string // linux, macos, windows, unix: in source order, each once
	Returns   string
	Confirm   bool
	Question  string // the author's [confirm] question
	Reaches   []Reached
	Plan      bool
	Applies   string // the plan task of [applies]
	ReadOnly  bool
	Network   bool
}

// ParamKind says how many values a parameter takes.
type ParamKind int

// The four kinds of parameter.
const (
	Required ParamKind = iota
	Optional
	OneOrMore
	ZeroOrMore
)

// Param is one parameter of an entry.
type Param struct {
	Name       string
	Kind       ParamKind
	Type       string   // the declared type, or ""
	Enum       []string // the values of an enum, in source order
	HasDefault bool
	Literal    bool   // the default is a string literal
	Default    string // the literal's value, or the canonical text of the expression
	Doc        string
	AgentValue *string // the one value the author allows agents, or nil
}

// Reached is a task other than the entry's own that a run can reach and
// that carries a limit.
type Reached struct {
	Name    string
	Confirm bool
	Allow   []Allowed
}

// Allowed is one predicate of [agent-allow]: a parameter and its one value.
type Allowed struct {
	Param, Value string
}

// Build makes the catalog of the tasks that are public: not private, and not
// the [context] hook. Nothing is evaluated. The host is not consulted, so a
// task for another operating system has its entry, and the catalog is the
// same on every machine.
func Build(src Source) Catalog {
	// The order of `rune --list`: groups as they first appear, and the
	// tasks of a group as they are declared.
	var order []string
	groups := map[string][]*ast.Task{}
	for _, t := range src.Tasks {
		if t.IsPrivate() || t.Attr(ast.AttrContext) != nil {
			continue
		}
		g := groupOf(t)
		if _, seen := groups[g]; !seen {
			order = append(order, g)
		}
		groups[g] = append(groups[g], t)
	}
	var c Catalog
	for _, g := range order {
		for _, t := range groups[g] {
			c.Entries = append(c.Entries, entryOf(t, src))
		}
	}
	return c
}

func groupOf(t *ast.Task) string {
	if a := t.Attr(ast.AttrGroup); a != nil {
		return a.Str
	}
	return ""
}

func entryOf(t *ast.Task, src Source) Entry {
	e := Entry{
		Name:      t.Name,
		Doc:       collapse(t.Doc),
		Separator: src.Commands[t.Name],
		Group:     groupOf(t),
		Returns:   collapse(t.Returns()),
		Plan:      t.Attr(ast.AttrPlan) != nil,
		Applies:   t.AppliesPlan(),
		ReadOnly:  t.Attr(ast.AttrReadonly) != nil,
		Network:   t.Attr(ast.AttrNetwork) != nil,
	}
	for _, os := range t.OSFilters() {
		if !slices.Contains(e.OS, os) {
			e.OS = append(e.OS, os)
		}
	}
	if c := t.Attr(ast.AttrConfirm); c != nil {
		e.Confirm, e.Question = true, c.Str
	}
	for _, p := range t.Params {
		e.Params = append(e.Params, paramOf(t, p))
	}
	if src.Reach != nil {
		for _, next := range src.Reach(t) {
			if next == t {
				continue
			}
			r := Reached{Name: next.Name, Confirm: next.Attr(ast.AttrConfirm) != nil, Allow: allowed(next)}
			if r.Confirm || len(r.Allow) > 0 {
				e.Reaches = append(e.Reaches, r)
			}
		}
	}
	return e
}

func allowed(t *ast.Task) []Allowed {
	var out []Allowed
	for _, p := range t.AgentPolicy() {
		out = append(out, Allowed{Param: p.Name, Value: p.Value})
	}
	return out
}

func paramOf(t *ast.Task, p *ast.Param) Param {
	out := Param{Name: p.Name, Doc: collapse(t.ParamDoc(p.Name))}
	switch p.Kind {
	case ast.ParamRequired:
		out.Kind = Required
	case ast.ParamDefaulted:
		out.Kind = Optional
	case ast.ParamVariadicPlus:
		out.Kind = OneOrMore
	case ast.ParamVariadicStar:
		out.Kind = ZeroOrMore
	}
	if c := p.Constraint; c != nil {
		out.Type, out.Enum = c.KindName, c.Values
	}
	if p.Default != nil {
		out.HasDefault = true
		if lit, ok := p.Default.(*ast.StringLit); ok {
			out.Literal, out.Default = true, lit.Value
		} else {
			out.Default = formatter.Expr(p.Default)
		}
	}
	// A policy cannot name a variadic parameter (RUNE2019).
	if out.Kind == Required || out.Kind == Optional {
		for _, a := range t.AgentPolicy() {
			if a.Name == p.Name {
				out.AgentValue = &a.Value
				break
			}
		}
	}
	return out
}

// Fingerprint is a digest of the catalog: it changes when a fact an entry
// states changes, and with nothing else.
func (c Catalog) Fingerprint() string {
	// Structs and slices only, so the encoding is the same on every run.
	data, err := json.Marshal(struct {
		V       int
		Entries []Entry
	}{1, c.Entries})
	if err != nil {
		// No field of an entry is of a kind that cannot be encoded.
		panic(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// command is how the task is named on the command line.
func (e Entry) command() []string {
	if e.Separator {
		return []string{"rune", "--", e.Name}
	}
	return []string{"rune", e.Name}
}

// Usage is the command line of the task in the notation of help texts:
// <p> is required, [p] is optional, and "..." takes several values.
func (e Entry) Usage() string {
	words := e.command()
	for _, p := range e.Params {
		switch p.Kind {
		case Required:
			words = append(words, "<"+p.Name+">")
		case Optional:
			words = append(words, "["+p.Name+"]")
		case OneOrMore:
			words = append(words, "<"+p.Name+">...")
		case ZeroOrMore:
			words = append(words, "["+p.Name+"]...")
		}
	}
	return strings.Join(words, " ")
}

// Example is one command a POSIX shell can run, built only from values the
// Runefile states, or "" when there is none to show: no parameter needs a
// value, a parameter that has to be given has no known value, or a value is
// not safe to show as a command.
func (e Entry) Example() string {
	last := -1
	for i, p := range e.Params {
		if p.needsValue() {
			last = i
		}
	}
	if last < 0 {
		return ""
	}
	words := e.command()
	for _, p := range e.Params[:last+1] {
		v, ok := p.exampleValue()
		if !ok || !exampleSafe(v) {
			return ""
		}
		words = append(words, shQuote(v))
	}
	return strings.Join(words, " ")
}

// needsValue reports whether a caller has to pass the parameter: it has no
// default, or its default is not the value the author allows agents.
func (p Param) needsValue() bool {
	switch p.Kind {
	case Required, OneOrMore:
		return true
	case Optional:
		return p.AgentValue != nil && (!p.Literal || p.Default != *p.AgentValue)
	}
	return false
}

// exampleValue is a value Rune knows for the parameter without evaluating
// anything. The default comes before the enum and the boolean, so that an
// example never sets an option the caller could have left alone.
func (p Param) exampleValue() (string, bool) {
	switch {
	case p.AgentValue != nil:
		return *p.AgentValue, true
	case p.Kind == Optional && p.Literal:
		return p.Default, true
	case len(p.Enum) > 0:
		return p.Enum[0], true
	case p.Type == "boolean":
		return "true", true
	}
	return "", false
}
