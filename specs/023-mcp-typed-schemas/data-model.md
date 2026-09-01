# Data Model: Typed Parameter Schemas and Outcome Descriptions

**Feature**: 023-mcp-typed-schemas · **Date**: 2026-08-28
**Decisions behind these shapes**: [research.md](research.md)

## 1. AST (`internal/ast`)

### ConstraintKind (new)

```go
type ConstraintKind int

const (
    KindString  ConstraintKind = iota // explicit spelling of the default
    KindNumber                        // integers + decimals (ParseFloat 64)
    KindBoolean                       // exactly "true" | "false"
    KindPath                          // non-empty string; role marker only
    KindEnum                          // closed set of static string literals
)
```

### Constraint (new)

```go
// Constraint is an author-declared value rule on one parameter.
// A nil *Constraint on Param means "unannotated" — the pre-feature shape.
type Constraint struct {
    Kind   ConstraintKind
    Values []string   // KindEnum only; ≥1 (parser), unique (analyzer), source order preserved
    Sp     token.Span // spans `:kind` through the closing `)` for enums
}

// Check validates a single bound value against the constraint.
// Returns nil for KindString. Used by every invocation path (FR-005).
func (c *Constraint) Check(value string) error
```

**Validation rules encoded in `Check`** (single source of truth for runtime enforcement):

| Kind | Rule |
|---|---|
| string / nil | always valid |
| number | `strconv.ParseFloat(value, 64)` succeeds |
| boolean | `value == "true" \|\| value == "false"` |
| path | `value != ""` |
| enum | exact, case-sensitive membership in `Values` |

Error text carries the accepted set (the caller prefixes parameter name and quoted value —
FR-006 message assembly happens at the binding site, which knows the invocation context).

### Param (extended)

```go
type Param struct {
    Name       string
    Kind       ParamKind    // existing: required/defaulted/variadic+/variadic*
    Default    Expr         // existing: only for ParamDefaulted
    Constraint *Constraint  // NEW: nil = unannotated
    Sp         token.Span   // widened to include the annotation (LSP paramNameSpan
                            // keeps pointing at the bare name — position.go:245)
}
```

Composition matrix (grammar-enforced):

| | required | defaulted | variadic `+`/`*` |
|---|---|---|---|
| may carry Constraint | yes | yes (default must satisfy it — RUNE2014/runtime) | yes (applies per element — FR-008) |
| may carry Default | — | yes | no (unchanged) |

### Task (attribute-carried additions — no struct change)

New attribute kinds and accessors:

```go
const (
    // ...existing...
    AttrReturns  // [returns("...")]   — Str = outcome description
    AttrParamDoc // [param-doc("p","...")] — Str = param name, Str2 = description
)

func (t *Task) Returns() string          // "" when absent
func (t *Task) ParamDoc(name string) string // "" when absent
```

`Attribute.Str`/`Str2` slots (ast.go:210) already fit both forms; no new fields.

## 2. Public MCP contract (`mcpserver`) — additive only

```go
type ParamInfo struct {
    Name        string
    Required    bool
    Variadic    bool
    Kind        string   // NEW: "", "string", "number", "boolean", "path", "enum"
    Enum        []string // NEW: KindEnum value list, source order
    Description string   // NEW: from [param-doc]
}

type TaskInfo struct {
    Name        string
    Doc         string
    Params      []ParamInfo
    Destructive bool
    Network     bool
    Returns     string // NEW: from [returns]
}
```

Populated by the engine adapter (`internal/cli/mcp.go:42-66`). `Kind == ""` produces a
byte-identical schema to today (SC-003).

## 3. JSON dump DTOs (`internal/cli/dump.go`)

```go
type paramDTO struct {
    Name        string   `json:"name"`
    Kind        string   `json:"kind"`                  // existing: required/defaulted/…
    Type        string   `json:"type,omitempty"`        // NEW: constraint kind
    Values      []string `json:"values,omitempty"`      // NEW: enum values
    Description string   `json:"description,omitempty"` // NEW: [param-doc]
}

type taskDTO struct {
    // ...existing fields (name, doc, executor, private, available, …)...
    Returns string `json:"returns,omitempty"` // NEW
}
```

`omitempty` is correct here (unlike `available`): absence reproduces the pre-feature
document exactly, which *is* the compatibility signal.

## 4. Diagnostics (`internal/diag/codes.go`) — append-only

| Code | Sev | Layer | Message shape |
|---|---|---|---|
| RUNE1006 | error | parser | `malformed parameter constraint: <detail>` |
| RUNE2012 | error | analyzer | `unknown parameter type "X" (supported: string, number, boolean, path, enum)` + spacing hint via Related when X names a task |
| RUNE2013 | error | analyzer | `duplicate enum value "v" for parameter "p"` (Related: first occurrence) |
| RUNE2014 | error | analyzer | `default "v" is not a valid <kind> for parameter "p" <accepted set>` |
| RUNE2015 | error | analyzer | `param-doc names unknown parameter "p"` / `duplicate param-doc for "p"` / `duplicate returns attribute` |
| RUNE2016 | warning | analyzer | `parameter type "string" shadows task "string"; if a dependency was intended, write "env: string"` |

Spans: RUNE1006/2012/2013/2014/2016 anchor on `Constraint.Sp` (2014 on the default expr
span); RUNE2015 on the attribute span. All built with `List.Codef` + `WithRelated` where
noted. RUNE2016 is warning-severity: it flags a *possible* legacy re-parse (constitution
v1.1.0 exception condition 3) without failing valid new code that legitimately annotates
a parameter while a task of the same name exists.

## 5. Language registry (`internal/language/builtin.go`)

`builtinAttributes` gains `returns`, `param-doc` — and the known drift (`doc`, `script`,
`context` missing) is closed, with `builtin_sync_test.go` extended to lock
registry ↔ parser attribute names the way it already locks functions.

## 6. State transitions

None — all data is static per parse; constraints have no lifecycle. The only ordering
rule: analyzer checks run before any execution (existing `Analyze` gate), and invocation
validation runs before scheduling (zero-side-effect guarantee).
