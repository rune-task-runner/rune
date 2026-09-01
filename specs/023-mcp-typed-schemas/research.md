# Research: Typed Parameter Schemas and Outcome Descriptions

**Feature**: 023-mcp-typed-schemas · **Date**: 2026-08-28

All NEEDS CLARIFICATION items: none remained in the spec (three were resolved in the
2026-08-28 clarification session). This document records the design decisions the spec
deferred to planning, grounded in the current code.

## R1. Inline constraint syntax and the colon ambiguity

**Decision**: `name:kind` and `name:enum("v1","v2")`, where the `:` must be **adjacent on
both sides** (no whitespace, verified via token span offsets since the lexer discards
whitespace). Grammar (delta in [contracts/grammar.md](contracts/grammar.md)):

```ebnf
Param      = Name [ TypeAnno ] [ "=" Expr ]
           | ("+" | "*") Name [ TypeAnno ] ;
TypeAnno   = ":" KindName [ "(" StringLit { "," StringLit } ")" ] ;   (* adjacent tokens *)
KindName   = "string" | "number" | "boolean" | "path" | "enum" ;
```

Kind names are **contextual keywords**, not reserved words — `string` remains a legal task,
parameter, or variable name everywhere else.

**The problem**: today `deploy env:build` (unspaced) is a valid header meaning "param `env`,
header colon, dependency `build`" — `parseParam` (internal/parser/task.go:103) exits at
`COLON`, and `p.expect(COLON)` (task.go:54) consumes it. Inline annotations reuse that
token, so the parser must decide which colon it is holding.

**Rule adopted**: in the parameter loop, `IDENT COLON IDENT` with both gaps
offset-adjacent (`prev.Span.End.Offset == cur.Span.Start.Offset`) is **always** a type
annotation. Kind-name validity is checked by the analyzer (RUNE2012), not the parser, so a
typo (`env:enun("a")`) yields "unknown parameter type \"enun\" (supported: string, number,
boolean, path, enum)" instead of cascading into dependency-parse errors — this is what
makes spec US4 scenario 1 satisfiable.

**Alternatives considered**:
- *Recognize only the five kind keywords, else fall back to the old parse*: keeps
  `env:build` working, but every misspelled kind silently re-parses as dependency syntax
  and fails with an unrelated error — fails FR-007/US4. Rejected.
- *A different sigil* (`env=enum(...)`, `env(enum ...)`, `env<path>`): `=` is taken by
  defaults, a paren group after a param collides with the executor clause
  (task.go:46-52), and angle brackets are new token surface for no readability gain.
  Rejected.
- *Unbounded lookahead to find the "real" header colon*: a header line has exactly one
  `COLON` today, but annotations add more; deciding by counting colons requires buffering
  the line and breaks the streaming recursive-descent style (Principle IV). Rejected.

**Accepted residual break** (Complexity Tracking in plan.md; sanctioned by the
constitution's v1.1.0 re-parse exception): unspaced legacy headers. `env:build` now
errors with RUNE2012 including the hint *"if `build` was meant as a dependency, add a
space: `env: build`"* — self-healing. The would-be-silent case — an unspaced dep named
exactly one of the five kinds (`env:string`) — re-parses as a valid annotation; a
dedicated warning, **RUNE2016**, fires whenever an annotated kind name is also an
existing task name, telling the author both readings and the spaced spelling that
restores the dependency. (RUNE2012 cannot carry this — it fires only for *unknown*
kinds, and `string` is a valid one.) This keeps the re-parse loud rather than silent,
satisfying exception condition (3). Formatter output has always been spaced; corpus
contains no unspaced instance (conditions 1–2); release notes cover condition (4).

## R2. Constraint kinds and their runtime semantics

**Decision** (per spec + clarifications):

| Kind | Accepts | Rejects | Notes |
|---|---|---|---|
| `string` | anything | — | explicit spelling of the default; identical to no annotation |
| `number` | integers and decimals (`2`, `-1.5`), validated with `strconv.ParseFloat(s, 64)` | non-numeric text, empty string | single kind (clarified); no int-only kind in v1 |
| `boolean` | exactly `true` / `false` | anything else incl. `yes`, `1`, `True` | case-sensitive canonical pair |
| `path` | any non-empty string | empty string | role marker only; **no** existence/containment checks |
| `enum(...)` | exact, case-sensitive member match | non-members | ≥1 value (parse-enforced), unique values (analyzer), values are static string literals |

**Enum values are string literals only** — not expressions. Rationale: the value set is
part of the *contract shown to agents*; if it could reference variables it would not be
statically dumpable into a tool schema, and analyzability (Principle III) would be lost.
Alternative (allow exprs, evaluate at schema-build time) rejected: schema would depend on
evaluation order and environment.

## R3. Where each annotation lives

**Decision**:
- **Type constraint**: inline on the parameter (clarified in spec session).
- **Parameter description**: task attribute `[param-doc("<param>", "<text>")]` — one per
  parameter, repeatable. Follows the existing two-string attribute precedent
  (`[env("K","V")]`, parsed at internal/parser/attribute.go:59-70). Analyzer verifies the
  named parameter exists (RUNE2015).
- **Outcome description**: task attribute `[returns("<text>")]` — single-string attribute
  group (attribute.go:55-57); formats correctly with zero formatter changes
  (formatter.go:170-175 default case).

**Alternative for param descriptions considered**: inline prose in the header — rejected,
headers with 2–3 described params become unreadable one-liners and fight the formatter's
dense `name:kind=expr` form. A `cache`-style single attribute with `param=desc` keys
rejected: dynamic keys don't fit `parseCacheArgs`'s fixed-key pattern and read worse when
descriptions contain commas.

## R4. AST and public contract shapes

**Decision** (full detail in [data-model.md](data-model.md)):
- `ast.Param` gains `Constraint *ast.Constraint` (`Kind ConstraintKind`, `Values []string`,
  `Sp token.Span`); nil means unannotated (FR-002 zero-change guarantee is structural).
- `ast.Task` annotations stay attribute-based: `AttrReturns`, `AttrParamDoc` new attribute
  kinds; `Task.Returns()` / `Task.ParamDoc(name)` helper methods mirror `Task.Attr`.
- `mcpserver.ParamInfo` gains `Kind string`, `Enum []string`, `Description string`;
  `mcpserver.TaskInfo` gains `Returns string`. Additive struct fields — no breaking change
  to the public package.

## R5. MCP schema mapping

**Decision** (worked examples in [contracts/mcp-schema.md](contracts/mcp-schema.md)):

| Kind | JSON Schema property |
|---|---|
| none / `string` | `{"type":"string"}` (byte-identical to today — SC-003) |
| `number` | `{"type":"number"}` |
| `boolean` | `{"type":"boolean"}` |
| `path` | `{"type":"string","format":"path"}` (unknown formats are legal annotation keywords; description also states "filesystem path") |
| `enum` | `{"type":"string","enum":["v1","v2"]}` |
| variadic + kind | `{"type":"array","items":{<mapped kind>}}` |

Per-parameter `description` is set from `[param-doc]`. `[returns]` is appended to the tool
`Description` as a `Returns: <text>` trailer — MCP `outputSchema` is for structured JSON
results, which task output is not; prose in the description is what agents actually read.
Built in `inputSchema()` / `toolFor()` (mcpserver/server.go:124-148, :107-120).

Defaults are **not** embedded in the schema in v1: they are expressions that may reference
variables/earlier params (evaluated at bind time, internal/cli/args.go:96), so a static
`default` field could lie. Optionality is already conveyed via the `required` list.

## R6. Enforcement placement (FR-005, all three invocation paths)

**Decision**: one validation function, `ast.Constraint.Check(value string) error`, called
from the existing binding choke points:

| Path | Site | Notes |
|---|---|---|
| CLI positional | `bindParams` (internal/cli/args.go:80) | variadic values validated per element on the `pos[i:]` slice **before** the space-join at args.go:106/:109 |
| MCP named | `bindNamedParams` (internal/cli/args.go:121) + per-element variadic validation in `mcpserver` before `decodeArgs` joins arrays (handler.go:50-55) | the join is lossy (element boundaries destroyed), so variadic per-value checks must run on the JSON array in the server; scalar params are re-validated engine-side (defense in depth) |
| Dependency args | `engine.ResolveDep` (internal/cli/run.go:262) after arg evaluation | OS-unavailable targets keep returning early unvalidated (run.go:271-273) — they never run, and validating against a hidden task would leak its existence into errors |

`Engine.Call(ctx, name, args map[string]string)` keeps its signature (public API);
`mcpserver` validates from `ParamInfo` (schema-equivalent data), the engine from
`ast.Constraint` — same rules, two renderings of the same contract, covered by a shared
test table. Rejected alternative: changing `Call` to `map[string]any` to thread JSON types
through — cleaner but a breaking public-API change not needed for correctness, since all
kinds validate on the canonical string form (`fmt.Sprint` of a JSON number/bool is exactly
the accepted spelling).

**Error styles (FR-006)**: CLI/dep → `usagef`-style message (exit 2, zero side effects)
naming parameter, quoted value, accepted set; MCP → tool error result with the same
sentence. Static default-vs-constraint contradictions → analyzer diagnostic (RUNE2014)
when the default is a literal; non-literal defaults (rare) are covered by the same runtime
check at bind time.

## R7. Static analysis and diagnostic codes

**Decision** (delta contract in [contracts/diagnostic-codes.md](contracts/diagnostic-codes.md)):

| Code | Layer | Condition |
|---|---|---|
| RUNE1006 | parser | malformed constraint syntax: `enum` without `( … )`, empty `enum()`, non-string enum value, value list on a non-enum kind |
| RUNE2012 | analyzer | unknown constraint kind (lists supported kinds; spacing hint when the kind matches a task name) |
| RUNE2013 | analyzer | duplicate enum values |
| RUNE2014 | analyzer | literal default violates its parameter's constraint |
| RUNE2015 | analyzer | annotation-attribute misuse: `[param-doc]` names an unknown parameter, duplicates a previous `[param-doc]` for the same parameter, or a task carries more than one `[returns]` |
| RUNE2016 | analyzer (warning) | annotated kind name shadows an existing task name — possible silent re-parse of an unspaced legacy dependency (see R1) |

Next free codes confirmed: parser RUNE1006, semantic RUNE2012+ (internal/diag/codes.go).
The code contract is append-only and requires lockstep updates to `docs/diagnostics.md`
and `specs/011-rune-lsp/contracts/diagnostic-codes.md`.

`checkParams` (internal/analyzer/analyzer.go:71) is the insertion point; it already walks
params and has the duplicate/ordering checks these sit beside.

## R8. Text renderings that must stay in sync

Four independent renderers print parameters; all four learn the annotated form in the
same change, guarded by goldens:

1. `formatter.formatParam` (internal/formatter/formatter.go:134) — canonical
   `name:kind=expr` / `+name:kind`, dense like today's `name=expr`.
2. `ast.dumpTask` (internal/ast/dump.go:63) — corpus golden format; regen is a deliberate
   reviewed `-update` (corpus test message mandates GRAMMAR.md update).
3. `language.TaskSignature` (internal/language/symbol.go:67) — LSP hover.
4. `cli/dump.go` JSON: `paramDTO` gains `type`, `values`, `description`; `taskDTO` gains
   `returns` (omitempty — absent means unannotated, `available`-style explicitness not
   needed since absence is the pre-feature shape).

LSP enrichment (cheap, in scope): `varHover` (internal/language/hover.go:63) renders kind +
description; completion `Detail` (internal/language/completion.go:190) shows
`parameter (enum: staging|prod)`. The attribute registry
(internal/language/builtin.go:84-97) gains `returns` + `param-doc` — and the pre-existing
drift (missing `doc`, `script`, `context`) is fixed in passing with a sync test extension.

## R9. Masking and size caps (FR-011)

**Decision**: schema text (kinds, enum values, param descriptions, returns) originates
exclusively from static Runefile string literals — attribute/annotation arguments are
`StringLit` tokens, never interpolated, so no environment value can reach a schema *by
construction*. This is asserted (not just assumed) by an integration test: a Runefile
whose masked env var appears verbatim as an enum value string still serves it (author
wrote it literally), but a tool description/schema never contains values sourced from the
environment. `[returns]`/`[param-doc]` text flows through the same description fields
already covered by the masking tests; the existing 8 KiB agent-text cap discipline
(`capAgentText`, internal/cli/mcp.go:149) is applied to the composed tool description.

## R10. Test strategy per layer (Principle VI)

- **Parser**: table entries in `internal/parser/codes_test.go` (RUNE1006) + AST goldens
  (`testdata/parser/`) + fuzz seed corpus entries for `name:kind`, `name:enum("a","b")="a"`,
  `+files:path`.
- **Analyzer**: `internal/analyzer/codes_test.go` table rows for RUNE2012–2015.
- **Formatter**: new fixture pair `testdata/fmt/typed-params.{rune,fmt}` (idempotency
  comes free from the existing harness).
- **Corpus**: extend `testdata/corpus/full.rune` with annotated params + `[returns]` +
  `[param-doc]`; regenerate `.ast` deliberately.
- **mcpserver unit**: extend `TestInputSchema` (mcpserver/server_test.go:48) for every
  kind; new handler test: variadic array element failing enum → tool error, no execution.
- **Integration (stdio)**: copy the `mcpCall` harness
  (test/integration/secret_masking_mcp_test.go:22) — assert `tools/list` schema contains
  `enum`/`type`/`description`/`Returns:`; assert `tools/call` with an out-of-enum value
  returns an error and the task body never ran (SC-002, SC-004 proxy).
- **CLI integration**: invalid positional arg → exit 2, stderr names param/value/allowed;
  dependency `(deploy "nope")` → analyzer or bind-time rejection.
- **Docs tests**: GRAMMAR.md productions updated; `docs/examples/parameters/` README
  gains the typed form (test/docs coverage matrix requires the dir).
