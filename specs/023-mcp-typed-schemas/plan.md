# Implementation Plan: Typed Parameter Schemas and Outcome Descriptions for Agents

**Branch**: `023-mcp-typed-schemas` | **Date**: 2026-08-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/023-mcp-typed-schemas/spec.md`

## Summary

Add an opt-in type system for task parameters — inline annotations `name:kind` with kinds
`string`, `number`, `boolean`, `path`, and `enum("v1","v2")` — plus a `[param-doc("name","…")]`
attribute for per-parameter descriptions and a `[returns("…")]` attribute for task outcome
descriptions. Constraints surface in the MCP tool input schema (typed properties, `enum`
value lists, per-parameter `description`) so agents compose valid calls on the first attempt,
and they are enforced on every invocation path (CLI positional, MCP named, dependency
parenthesized args) before anything executes. Static analysis validates the annotations
themselves (unknown kind, malformed enum, default violating its own constraint) with
caret diagnostics. No behavior change for unannotated Runefiles.

## Technical Context

**Language/Version**: Go 1.25 (pure Go, `CGO_ENABLED=0`)

**Primary Dependencies**: `github.com/modelcontextprotocol/go-sdk/mcp` v1.6.1 (MCP server),
`mvdan.cc/sh/v3` (default executor), `spf13/cobra` (CLI). No new dependencies.

**Storage**: N/A (Runefile is the only input; no persistence changes)

**Testing**: `go test` inside the Docker harness (`docker-compose run --rm test go test ./...`),
golden files with `-update` flags, compatibility corpus (`test/corpus`), lexer/parser fuzz
targets, binary-level integration tests (`test/integration`), docs tests (`test/docs`)

**Target Platform**: Linux, macOS, Windows — single static binary

**Project Type**: CLI tool + embeddable library (public `mcpserver/` package, `internal/` engine)

**Performance Goals**: No measurable change to parse/analyze/startup time; validation is
O(#args) string checks on the invocation path

**Constraints**: Zero behavior change for existing Runefiles (one narrow, documented
exception — see Complexity Tracking); public diagnostic-code contract is append-only;
`mcpserver` public API stays backward compatible

**Scale/Scope**: ~10 existing packages touched (`token`? no — no new tokens needed;
`ast`, `parser`, `analyzer`, `diag`, `formatter`, `language`, `cli`, `mcpserver`, `lsp`
surfaces, docs/fixtures). 6 new diagnostic codes (RUNE1006, RUNE2012–RUNE2016).

## Constitution Check

*GATE: evaluated pre-Phase-0, re-evaluated post-Phase-1 design, and re-evaluated
2026-08-31 against constitution v1.1.0 after /speckit-analyze. Result: PASS with one
justified item in Complexity Tracking.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command Runner, Not a Build System | PASS | No caching/skipping semantics touched. |
| II. Errors Are a Feature | PASS | All new authoring errors are coded diagnostics with `file:line:col` + caret spans (RUNE1006, RUNE2012–2015); invocation-time violations name the parameter, the value, and the accepted set (FR-006), and fail with zero side effects. |
| III. Minimal, Total DSL | PASS | The expression sublanguage is untouched — no loops, no recursion, no new expression forms. Additions are declarative annotations (an inline parameter kind, two attributes), which constitution v1.1.0 Principle III explicitly permits through the normal spec process: they are total, statically analyzable (enum values are static string literals), and ship with grammar docs + fixtures per the Engineering Constraints. |
| IV. Hand-Written Front End, Idiomatic Go | PASS | Extends the existing recursive-descent parser by hand; no codegen. No new packages; validation lives in `internal/cli/args.go` (binding choke point) + `internal/ast` (constraint model), preserving the locked layout. |
| V. Boringly Portable | PASS | Pure Go string validation; `path` kind performs no filesystem access. |
| VI. Test-First, Multi-Layer Verification | PASS | Plan mandates: parser/analyzer code tests (`codes_test.go` tables), formatter + corpus goldens regenerated deliberately, fuzz seed corpus entries for the new syntax, MCP in-memory schema tests (`TestInputSchema`) + stdio integration test, docs tests (GRAMMAR.md + runefile.md + mcp.md updated in same PR). |
| VII. AI-Native, Secure by Default | PASS | Constraints tighten the agent surface. New schema text comes only from static Runefile string literals — no environment values can enter a schema; the existing masking/size-cap guarantees are asserted by test. Destructive/network hint mapping unchanged. |
| VIII. Go Engineering Discipline | PASS | No goroutines, no globals, table-driven tests, `%w` wrapping, golangci-lint clean. |

**Engineering Constraints check**: Docker-only testing (unchanged) · locked package layout
(no new packages) · backward compatibility (one narrow documented exception, below) ·
surface change ships with GRAMMAR.md + fixtures in the same PR (mandated in quickstart).

## Project Structure

### Documentation (this feature)

```text
specs/023-mcp-typed-schemas/
├── plan.md              # This file
├── research.md          # Phase 0: decisions (syntax disambiguation, schema mapping, …)
├── data-model.md        # Phase 1: AST/DTO/contract-struct changes
├── quickstart.md        # Phase 1: end-to-end validation scenarios
├── contracts/
│   ├── grammar.md           # EBNF delta for Param + new attributes
│   ├── mcp-schema.md        # kind → JSON Schema mapping, worked examples
│   └── diagnostic-codes.md  # new RUNE codes (append-only contract delta)
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── ast/            # Param gains Constraint (kind + enum values); AttrReturns, AttrParamDoc;
│   │               # dump.go param rendering (corpus golden format)
├── parser/         # task.go parseParam(): inline `name:kind` / `name:enum(...)`;
│   │               # attribute.go: returns + param-doc; codes_test.go RUNE1006
├── analyzer/       # analyzer.go checkParams(): RUNE2012–2015; codes_test.go
├── diag/           # codes.go: RUNE1006, RUNE2012–RUNE2015
├── formatter/      # formatter.go formatParam(): canonical `name:kind=expr` form; goldens
├── language/       # builtin.go attribute registry (+returns, +param-doc, fix drift);
│   │               # hover.go varHover (type/desc), symbol.go TaskSignature
├── cli/            # args.go: constraint validation at all three binding sites;
│   │               # mcp.go: adapter maps constraints into mcpserver.ParamInfo/TaskInfo;
│   │               # dump.go: paramDTO {type, values, description}, taskDTO {returns}
└── lsp/            # (thin) completion Detail + hover text via internal/language

mcpserver/          # server.go: ParamInfo {Kind, Enum, Description}, TaskInfo {Returns};
                    # inputSchema() typed properties; toolFor() description composition;
                    # handler.go decodeArgs(): per-element variadic validation pre-join

docs/               # GRAMMAR.md, runefile.md, mcp.md, diagnostics.md,
                    # examples/parameters/ (docs tests enforce)
specs/011-rune-lsp/contracts/diagnostic-codes.md   # append new codes (public contract)

testdata/           # lexer/, parser/, fmt/typed-params.{rune,fmt}, corpus/full.{rune,ast}
test/
├── corpus/         # compatibility corpus regen (-update) — deliberate, reviewed
├── integration/    # MCP stdio tools/list schema assertion + invalid-arg rejection
└── docs/           # examples/README coverage
```

**Structure Decision**: No new packages. The constraint model lives on `ast.Param` (data),
enforcement at the existing three binding sites in `internal/cli` (single file,
`args.go` + `run.go` dep path), schema surfacing in the public `mcpserver` package via the
existing `ParamInfo`/`TaskInfo` contract structs. This keeps the locked layout intact and
gives FR-005 a small, auditable set of choke points.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Narrow parse change: adjacent `param:ident` in a task header becomes a type annotation. An unformatted legacy header like `deploy env:build` (no spaces, meaning "dep `build`") now parses as a constraint and errors as unknown kind (RUNE2012) with an explicit "add a space if you meant a dependency" hint; an unspaced dep named exactly a kind word (`env:string`) re-parses validly and draws the RUNE2016 shadow warning. Ships under the constitution v1.1.0 backward-compat re-parse exception, all four conditions met: (1) the formatter has only ever emitted the spaced form, (2) the compat corpus contains no unspaced instance, (3) every affected file fails (RUNE2012) or warns (RUNE2016) loudly with the exact fix — nothing is silent, (4) release-notes entry mandated (T035). | Unconditional adjacency parsing is what makes FR-007/US4 satisfiable: a typo like `env:enun("a")` must produce "unknown parameter type" with the supported list, not a baffling downstream dep-parse error. | Recognizing only the five kind keywords and falling back to the old parse otherwise keeps `env:build` working but silently mis-parses every typo into dependency syntax, failing spec US4 scenario 1. A `rune_version` pragma gate would put the entire feature behind per-file opt-in, defeating its zero-config value for agents. |
