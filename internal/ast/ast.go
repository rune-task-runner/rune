// Package ast defines the Runefile abstract syntax tree produced by the parser
// and consumed by the analyzer, evaluator, scheduler, cache, and MCP server.
// Every node carries a token.Span so any diagnostic can point at precise source
// (Principle II).
package ast

import "github.com/rune-task-runner/rune/internal/token"

// Node is implemented by every AST node.
type Node interface {
	Span() token.Span
}

// File is the root of one parsed Runefile. A project may be a tree of Files via
// import (spliced) and mod (namespaced).
type File struct {
	Path        string
	Settings    []*Setting
	Assignments []*Assignment
	Tasks       []*Task
	Imports     []*Import
	Mods        []*Mod
	Sp          token.Span
}

func (f *File) Span() token.Span { return f.Sp }

// Setting is a `set NAME [:= VALUE]` directive. The bare form (`set export`) is
// boolean true (Bool=true, Value=nil). List-valued settings (e.g. `set shell`)
// keep their elements in List.
type Setting struct {
	Name  string
	Value Expr   // nil for the bare boolean form
	List  []Expr // populated for list-valued settings
	Bool  bool   // true for the bare form
	Sp    token.Span
}

func (s *Setting) Span() token.Span { return s.Sp }

// Assignment is a module-level variable binding `NAME := EXPR`.
type Assignment struct {
	Name string
	Expr Expr
	Sp   token.Span
}

func (a *Assignment) Span() token.Span { return a.Sp }

// ParamKind classifies a task parameter.
type ParamKind int

const (
	ParamRequired     ParamKind = iota // name
	ParamDefaulted                     // name=expr
	ParamVariadicPlus                  // +name (one or more)
	ParamVariadicStar                  // *name (zero or more)
)

// Param is a positional task parameter.
type Param struct {
	Name    string
	Kind    ParamKind
	Default Expr // only for ParamDefaulted
	Sp      token.Span
}

func (p *Param) Span() token.Span { return p.Sp }

// Executor names for the built-in body languages. The empty string means the
// default shell executor; any other non-built-in string is a custom executor.
const (
	ExecSh     = "sh"
	ExecPython = "python"
	ExecNode   = "node"
	ExecAgent  = "agent"
)

// Task is a named recipe.
type Task struct {
	Name       string
	Doc        string // from the preceding comment run or [doc("...")]
	Params     []*Param
	Executor   string // "" => default sh
	Deps       []*DepCall
	PostHooks  []*DepCall // run after, on success (&&)
	FailHooks  []*DepCall // run after, on failure (||)
	Attributes []*Attribute
	Body       []*BodyLine
	Sp         token.Span
}

func (t *Task) Span() token.Span { return t.Sp }

// Edges returns every outgoing task reference of t: dependencies, && post-
// hooks, and || failure hooks — the single edge-set definition shared by
// dependency resolution, cycle detection, [context] closure walks, and
// editor navigation, so a new clause kind cannot be missed by one of them.
func (t *Task) Edges() []*DepCall {
	edges := make([]*DepCall, 0, len(t.Deps)+len(t.PostHooks)+len(t.FailHooks))
	edges = append(edges, t.Deps...)
	edges = append(edges, t.PostHooks...)
	edges = append(edges, t.FailHooks...)
	return edges
}

// IsPrivate reports whether the task carries the [private] attribute or a name
// beginning with '_'.
func (t *Task) IsPrivate() bool {
	if len(t.Name) > 0 && t.Name[0] == '_' {
		return true
	}
	for _, a := range t.Attributes {
		if a.Kind == AttrPrivate {
			return true
		}
	}
	return false
}

// AvailableOn reports whether the task may run on the given GOOS. A task
// with no OS attribute is available everywhere; multiple OS attributes
// combine as OR; "unix" matches every GOOS except "windows". This is the
// single availability rule shared by listing, completion, MCP exposure,
// root resolution, and dependency scheduling.
func (t *Task) AvailableOn(goos string) bool {
	filters := t.OSFilters()
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		switch f {
		case AttrLinux:
			if goos == "linux" {
				return true
			}
		case AttrMacos:
			if goos == "darwin" {
				return true
			}
		case AttrWindows:
			if goos == "windows" {
				return true
			}
		case AttrUnix:
			if goos != "windows" {
				return true
			}
		}
	}
	return false
}

// OSFilters returns the task's OS attribute kinds in source order, or nil
// when the task is unrestricted.
func (t *Task) OSFilters() []string {
	var filters []string
	for _, a := range t.Attributes {
		switch a.Kind {
		case AttrLinux, AttrMacos, AttrWindows, AttrUnix:
			filters = append(filters, a.Kind)
		}
	}
	return filters
}

// Attr returns the first attribute of the given kind, or nil.
func (t *Task) Attr(kind string) *Attribute {
	for _, a := range t.Attributes {
		if a.Kind == kind {
			return a
		}
	}
	return nil
}

// DepCall is a dependency or post-hook invocation.
type DepCall struct {
	Name string // may be namespaced (mod::task)
	Args []Expr
	Sp   token.Span
}

func (d *DepCall) Span() token.Span { return d.Sp }

// Attribute kinds.
const (
	AttrPrivate          = "private"
	AttrConfirm          = "confirm"
	AttrGroup            = "group"
	AttrParallel         = "parallel"
	AttrLinux            = "linux"
	AttrMacos            = "macos"
	AttrWindows          = "windows"
	AttrUnix             = "unix"
	AttrNoCD             = "no-cd"
	AttrWorkingDirectory = "working-directory"
	AttrEnv              = "env"
	AttrDoc              = "doc"
	AttrScript           = "script"
	AttrCache            = "cache"
	AttrNetwork          = "network"         // sets MCP openWorldHint
	AttrNoExitMessage    = "no-exit-message" // suppress the trailing error banner
	AttrContext          = "context"         // project-health hook injected into agent context (spec 021)
)

// Attribute is a `[name(args)]` annotation on a task. Most attributes carry a
// single string argument (Str); env carries two (Str, Str2); cache carries
// input/output glob lists.
type Attribute struct {
	Kind       string
	Str        string // confirm prompt, group name, doc, script cmd, working-directory, env name
	Str2       string // env value
	Inputs     []Expr // cache(inputs=[...])
	Outputs    []Expr // cache(outputs=[...])
	HasOutputs bool
	Sp         token.Span
}

func (a *Attribute) Span() token.Span { return a.Sp }

// BodyLine is one line of a task body, with leading-sigil flags stripped. Raw
// retains {{ ... }} interpolation placeholders for the evaluator.
type BodyLine struct {
	Raw             string
	NoEcho          bool // leading @
	ContinueOnError bool // leading -
	Sp              token.Span
}

func (b *BodyLine) Span() token.Span { return b.Sp }

// Import splices another file's definitions into the current namespace.
type Import struct {
	Path     string // decoded string-literal path
	Optional bool   // import?
	Sp       token.Span
}

func (i *Import) Span() token.Span { return i.Sp }

// Mod loads another file as a child namespace addressable as name::task.
type Mod struct {
	Name string
	Path string // optional explicit path; "" => derive from name
	Sp   token.Span
}

func (m *Mod) Span() token.Span { return m.Sp }

// ---- Expression sublanguage (total, non-Turing-complete) ----

// Expr is implemented by every expression node.
type Expr interface {
	Node
	exprNode()
}

// StringLit is a decoded string literal.
type StringLit struct {
	Value string
	Sp    token.Span
}

func (*StringLit) exprNode()          {}
func (e *StringLit) Span() token.Span { return e.Sp }

// Binary is a concatenation (+) or path-join (/) expression.
type Binary struct {
	Op    token.Kind // PLUS or SLASH
	Left  Expr
	Right Expr
	Sp    token.Span
}

func (*Binary) exprNode()          {}
func (e *Binary) Span() token.Span { return e.Sp }

// CondBranch is one `if/else if` clause: Left Op Right { Result }.
type CondBranch struct {
	Left   Expr
	Op     token.Kind // EQ, NEQ, MATCH
	Right  Expr
	Result Expr
}

// Conditional is an if/else-if/else expression. It always has a final Else.
type Conditional struct {
	Branches []CondBranch
	Else     Expr
	Sp       token.Span
}

func (*Conditional) exprNode()          {}
func (e *Conditional) Span() token.Span { return e.Sp }

// FuncCall is a built-in function call.
type FuncCall struct {
	Name string
	Args []Expr
	Sp   token.Span
}

func (*FuncCall) exprNode()          {}
func (e *FuncCall) Span() token.Span { return e.Sp }

// VarRef is a bare name reference. Resolution (param vs module variable) is
// performed by the analyzer/evaluator; params shadow variables.
type VarRef struct {
	Name string
	Sp   token.Span
}

func (*VarRef) exprNode()          {}
func (e *VarRef) Span() token.Span { return e.Sp }
