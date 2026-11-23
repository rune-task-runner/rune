// Package ast defines the Runefile abstract syntax tree produced by the parser
// and consumed by the analyzer, evaluator, scheduler, cache, and MCP server.
// Every node carries a token.Span so any diagnostic can point at precise source
// (Principle II).
package ast

import (
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

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
	KindRaw                           // any value, split into shell words on purpose (spec 030)
)

// SupportedConstraintKinds lists the kind names accepted after `:` in a
// parameter annotation, in the order diagnostics cite them.
var SupportedConstraintKinds = []string{"string", "number", "boolean", "path", "raw", "enum"}

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
	case "raw":
		return KindRaw, true
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

// AgentPolicy returns the predicates an agent run of the task must meet, or
// nil when the task has no [agent-allow]. Only the first attribute counts: a
// second one is a static error (RUNE2019), and must never widen the first.
func (t *Task) AgentPolicy() []*AllowPredicate {
	if a := t.Attr(AttrAgentAllow); a != nil {
		return a.Allow
	}
	return nil
}

// PlanSpec is what [plan(...)] says about the plan a task makes (spec 051).
// Expires is the text as written. PlanExpiry reads it.
type PlanSpec struct {
	File, Summary, Tool, Expires string
}

// PlanSpec returns the arguments of the task's first [plan], or nil. The
// analyzer rejects a second one, an unknown argument and a repeated one.
func (t *Task) PlanSpec() *PlanSpec {
	a := t.Attr(AttrPlan)
	if a == nil {
		return nil
	}
	spec := &PlanSpec{}
	for _, arg := range a.Allow {
		switch arg.Name {
		case "file":
			spec.File = arg.Value
		case "summary":
			spec.Summary = arg.Value
		case "tool":
			spec.Tool = arg.Value
		case "expires":
			spec.Expires = arg.Value
		}
	}
	return spec
}

// AppliesPlan returns the plan task named by the task's first [applies], or
// "" when the task applies no plan.
func (t *Task) AppliesPlan() string {
	if a := t.Attr(AttrApplies); a != nil {
		return a.Str
	}
	return ""
}

// PlanExpiry reads the expires argument of [plan]: a duration from
// MinPlanExpiry to MaxPlanExpiry, or DefaultPlanExpiry when text is empty.
func PlanExpiry(text string) (time.Duration, error) {
	if text == "" {
		return DefaultPlanExpiry, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("not a duration: %w", err)
	}
	if d < MinPlanExpiry || d > MaxPlanExpiry {
		return 0, fmt.Errorf("%s is outside %s to %s", d, MinPlanExpiry, MaxPlanExpiry)
	}
	return d, nil
}

// MaxAgentOutputLines is the most lines [agent-output] can keep from the
// start or the end of a stream (spec 057).
const MaxAgentOutputLines = 10000

// AgentOutputLines reads a number of lines of [agent-output]: 1 to 5 decimal
// digits whose value is from 1 to MaxAgentOutputLines. The analyzer and the
// engine both read the text through it, so neither accepts what the other
// rejects.
func AgentOutputLines(text string) (int, error) {
	n := 0
	for _, c := range []byte(text) {
		if c < '0' || c > '9' {
			n = -1
			break
		}
		n = n*10 + int(c-'0')
	}
	if len(text) == 0 || len(text) > 5 || n < 1 || n > MaxAgentOutputLines {
		return 0, fmt.Errorf("must be a whole number of lines from 1 to %d", MaxAgentOutputLines)
	}
	return n, nil
}

// AgentOutput returns the [agent-output] policy of the task: the lines kept
// from the start (0 when head is not set) and from the end of each stream.
// ok is false when the task has none, and when the attribute is one the
// analyzer rejects, so a policy is never read in part.
func (t *Task) AgentOutput() (head, tail int, ok bool) {
	a := t.Attr(AttrAgentOutput)
	if a == nil {
		return 0, 0, false
	}
	for _, arg := range a.Allow {
		n, err := AgentOutputLines(arg.Value)
		if err != nil {
			return 0, 0, false
		}
		switch arg.Name {
		case "head":
			head = n
		case "tail":
			tail = n
		default:
			return 0, 0, false
		}
	}
	if tail == 0 {
		return 0, 0, false
	}
	return head, tail, true
}

// AgentSummary reports whether a failed call of the task from an agent gets
// a failure summary (spec 057).
func (t *Task) AgentSummary() bool { return t.Attr(AttrAgentSummary) != nil }

// PlanPathError says why p cannot be a file or summary path of [plan], or ""
// when it can. The rule reads the text only, so one Runefile is valid or not
// on every host: filepath.IsAbs would accept C:/x on Linux and /x on Windows.
func PlanPathError(p string) string {
	switch {
	case p == "":
		return "is empty"
	case strings.HasPrefix(p, "/"), strings.ContainsRune(p, '\\'):
		return "is not a relative path with forward slashes"
	case len(p) >= 2 && p[1] == ':':
		return "has a drive prefix"
	}
	for elem := range strings.SplitSeq(p, "/") {
		if elem == ".." {
			return "leaves the task's directory"
		}
	}
	return ""
}

// SamePlanPath reports whether two [plan] paths name one file.
func SamePlanPath(a, b string) bool { return path.Clean(a) == path.Clean(b) }

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
	AttrReadonly         = "readonly"        // claims read-only: sets MCP readOnlyHint
	AttrAgentAllow       = "agent-allow"     // limits the values an agent may run the task with (spec 050)
	AttrPlan             = "plan"            // the task makes a plan file that Rune stores (spec 051)
	AttrApplies          = "applies"         // the task applies the stored plan of the named plan task (spec 051)
	AttrAgentOutput      = "agent-output"    // the lines of each stream an agent's result keeps (spec 057)
	AttrAgentSummary     = "agent-summary"   // a failed call from an agent gets a failure summary (spec 057)
)

// The variables Rune gives the body of a task that applies a plan (spec 051).
const (
	PlanFileVar   = "RUNE_PLAN_FILE"
	PlanIDVar     = "RUNE_PLAN_ID"
	PlanDigestVar = "RUNE_PLAN_DIGEST"
)

// The expiry of a stored plan: the default, and the range [plan(expires=...)]
// may set.
const (
	DefaultPlanExpiry = time.Hour
	MinPlanExpiry     = time.Minute
	MaxPlanExpiry     = 7 * 24 * time.Hour
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
	AttrParamDoc, AttrReturns, AttrReadonly, AttrAgentAllow,
	AttrPlan, AttrApplies, AttrAgentOutput, AttrAgentSummary,
}

// Attribute is a `[name(args)]` annotation on a task. Most attributes carry a
// single string argument (Str); env carries two (Str, Str2); cache carries
// input/output glob lists; agent-allow and plan carry named string arguments.
type Attribute struct {
	Kind       string
	Str        string // confirm prompt, group name, doc, script cmd, working-directory, env name
	Str2       string // env value
	Inputs     []Expr // cache(inputs=[...])
	Outputs    []Expr // cache(outputs=[...])
	HasOutputs bool
	Allow      []*AllowPredicate // named string arguments of agent-allow(...) and plan(...), in source order
	Sp         token.Span
}

func (a *Attribute) Span() token.Span { return a.Sp }

// AllowPredicate is one name="value" of an attribute. In [agent-allow] it is
// a parameter and the one value an agent run of the task may have for it
// (spec 050). In [plan] it is one argument (spec 051). The two spans let a
// diagnostic point at the part that is wrong.
type AllowPredicate struct {
	Name    string
	Value   string
	NameSp  token.Span
	ValueSp token.Span
}

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
