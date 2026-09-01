// Package ast defines the Runefile abstract syntax tree produced by the parser
// and consumed by the analyzer, evaluator, scheduler, cache, and MCP server.
// Every node carries a token.Span so any diagnostic can point at precise source
// (Principle II).
package ast

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/token"
)

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
	Name       string
	Kind       ParamKind
	Default    Expr        // only for ParamDefaulted
	Constraint *Constraint // nil = unannotated (the pre-023 shape)
	Sp         token.Span
}

func (p *Param) Span() token.Span { return p.Sp }

// ConstraintKind classifies an inline parameter type annotation (spec 023).
type ConstraintKind int

const (
	KindString  ConstraintKind = iota // explicit spelling of the default
	KindNumber                        // integers and decimals (ParseFloat 64)
	KindBoolean                       // exactly "true" | "false"
	KindPath                          // non-empty string; role marker only
	KindEnum                          // closed set of static string literals
)

// SupportedConstraintKinds lists the kind names accepted after `:` in a
// parameter annotation, in the order diagnostics cite them.
var SupportedConstraintKinds = []string{"string", "number", "boolean", "path", "enum"}

// ConstraintKindFromName resolves a kind name (a contextual keyword, matched
// case-sensitively) to its ConstraintKind.
func ConstraintKindFromName(name string) (ConstraintKind, bool) {
	switch name {
	case "string":
		return KindString, true
	case "number":
		return KindNumber, true
	case "boolean":
		return KindBoolean, true
	case "path":
		return KindPath, true
	case "enum":
		return KindEnum, true
	default:
		return KindString, false
	}
}

// Constraint is an author-declared value rule on one parameter, written inline
// as `name:kind` or `name:enum("v1","v2")`. Kind is resolved from KindName when
// the name is a known kind; an unknown KindName is a semantic error (RUNE2012),
// reported before anything executes, so Check never runs for one.
type Constraint struct {
	Kind     ConstraintKind
	KindName string   // as written; unknown names are the analyzer's to reject
	Values   []string // KindEnum only; ≥1 (parser), unique (analyzer), source order
	Sp       token.Span
}

func (c *Constraint) Span() token.Span { return c.Sp }

// Check validates one bound value against the constraint. It is the single
// source of truth for every invocation path (spec 023 FR-005): CLI positional,
// MCP named, and dependency parenthesized arguments. The error text carries the
// accepted set; callers prefix the task, parameter, and offending value
// (FR-006). A nil constraint accepts anything.
func (c *Constraint) Check(value string) error {
	if c == nil {
		return nil
	}
	switch c.Kind {
	case KindNumber:
		if !IsDecimalNumber(value) {
			return fmt.Errorf("expected a number")
		}
	case KindBoolean:
		if value != "true" && value != "false" {
			return fmt.Errorf(`expected "true" or "false"`)
		}
	case KindPath:
		if value == "" {
			return fmt.Errorf("expected a non-empty path")
		}
	case KindEnum:
		for _, v := range c.Values {
			if value == v {
				return nil
			}
		}
		return fmt.Errorf("allowed values: %s", quoteJoin(c.Values))
	}
	return nil
}

// String renders the annotation in canonical source form, e.g. `:number` or
// `:enum("staging","prod")` — the one renderer shared by the formatter, the
// AST dumper, and editor signatures so the three can never drift.
func (c *Constraint) String() string {
	if c == nil {
		return ""
	}
	s := ":" + c.KindName
	if len(c.Values) > 0 {
		quoted := make([]string, len(c.Values))
		for i, v := range c.Values {
			quoted[i] = strconv.Quote(v)
		}
		s += "(" + strings.Join(quoted, ",") + ")"
	}
	return s
}

// IsDecimalNumber reports whether value is a plain decimal number — integers
// and decimals with an optional exponent, the documented `:number` contract.
// It rejects the extra spellings strconv.ParseFloat tolerates (NaN, ±Inf,
// hex floats, digit-separating underscores), none of which a JSON `number`
// can carry, so the CLI, dependency, and MCP paths accept the same set.
// mcpserver mirrors this rule; TestParamCheckMatchesASTConstraint pins the two.
func IsDecimalNumber(value string) bool {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return !strings.ContainsAny(value, "xXpP_")
}

// quoteJoin renders a value list as `"a", "b"` for diagnostics and errors.
func quoteJoin(vals []string) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Quote(v)
	}
	return strings.Join(parts, ", ")
}

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

// Returns reports the task's [returns("...")] outcome description, or ""
// (spec 023 FR-009). Advisory only: Rune never compares task output to it.
func (t *Task) Returns() string {
	if a := t.Attr(AttrReturns); a != nil {
		return a.Str
	}
	return ""
}

// ParamDoc returns the [param-doc("name","text")] description for the named
// parameter, or "" when the parameter carries none (spec 023 FR-014).
func (t *Task) ParamDoc(name string) string {
	for _, a := range t.Attributes {
		if a.Kind == AttrParamDoc && a.Str == name {
			return a.Str2
		}
	}
	return ""
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
	AttrParamDoc         = "param-doc"       // per-parameter description surfaced in agent tool schemas (spec 023)
	AttrReturns          = "returns"         // task outcome description surfaced to agents and listings (spec 023)
)

// KnownAttributes is the canonical accepted attribute set, in documentation
// order. The language registry is checked against it (language's
// TestRegistryMatchesASTAttributes) and so is the parser's attribute switch
// (parser's TestParserKnowsAllASTAttributes), so neither can drift silently.
var KnownAttributes = []string{
	AttrPrivate, AttrConfirm, AttrGroup, AttrParallel,
	AttrLinux, AttrMacos, AttrWindows, AttrUnix,
	AttrNoCD, AttrWorkingDirectory, AttrEnv, AttrDoc, AttrScript,
	AttrCache, AttrNetwork, AttrNoExitMessage, AttrContext,
	AttrParamDoc, AttrReturns,
}

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
