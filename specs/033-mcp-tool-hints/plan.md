# Implementation Plan: Truthful MCP Tool Hints

**Branch**: `033-mcp-tool-hints` | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/033-mcp-tool-hints/spec.md`

## Summary

Close issue #37. `toolFor` sets `ReadOnlyHint: !destructive`, so every task without `[confirm]`
claims to be read-only, and the destructive and open-world hints look only at the task itself.

The plan adds a `[readonly]` attribute (R1) and a static rule, `RUNE2018`, that rejects
`[readonly]` with `[confirm]` and a `[readonly]` task running one without the attribute (R2). The
CLI's MCP adapter walks each task's dependencies and hooks on the host OS (R3) and reports the
result through two new `TaskInfo` fields, `ReadOnly` and `ReachesDestructive` (R4). `Destructive`
keeps its meaning, because authorization reads it, so no call's outcome changes. Docs drop the
"read-only by default" claim everywhere it appears (R7), and Principle VII gets the same rewording
through a constitution amendment (R8). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1). No new
modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `internal/analyzer`, `internal/parser`,
`internal/formatter`, `internal/analysis`, `mcpserver`, and `internal/cli`; binary-level stdio tests
in `test/integration`; the corpus guard in `test/corpus`; the docs harness in `test/docs` (R9).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. One walk over a task's dependencies and hooks per listed task, with no
caching until a profile shows a need (R3, Principle VIII).

**Constraints**: No call's outcome changes (FR-010). Nothing is inferred from a body or an executor
(FR-014). The static rule gives the same result on every host (FR-009), and hints follow the host
OS (FR-006). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 7 Go source files (`internal/ast/ast.go`, `internal/parser/attribute.go`,
`internal/language/builtin.go`, `internal/diag/codes.go`, `internal/analyzer/analyzer.go`,
`internal/cli/mcp.go`, `mcpserver/server.go`), the VS Code grammar, 2 new test files, 1 new corpus
fixture with its golden, changes to 8 existing test files, 11 documents, and the constitution.
`mcpserver.TaskInfo` gains two fields.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.2.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Hints and `RUNE2018` change no decision about whether a task runs, apart from the static error itself. |
| II. Errors are a feature | PASS | A false claim becomes a located error with a stated fix and zero side effects. |
| III. Minimal, total DSL | PASS | Principle III lets task attributes grow through the normal spec process. `[readonly]` takes no arguments, is total and statically analyzable, and ships with grammar docs and fixtures. The expression language does not change. |
| IV. Hand-written front end, idiomatic Go | PASS | One new case in the hand-written attribute switch. No new package. |
| V. Boringly portable | PASS | Reach uses the adapter's injected host OS, the value availability checks already use. No OS-specific code. |
| VI. Test-first, multi-layer verification | PASS | Analyzer, parser, formatter, library, adapter, transport, binary, corpus, and docs layers (R9). The corpus golden is regenerated with `-update`, never edited by hand. |
| VII. AI-native, secure by default | PASS (amended to v1.3.0) | Hints become accurate and stay author-declared, the refusal default is unchanged, and the new fields carry no values. The sentence "The agent surface is read-only by default" conflicts with FR-002 when read literally, so R8 rewords it. The maintainer approved the amendment before implementation. |
| VIII. Go engineering discipline | PASS | No goroutines or globals. No optimization without a profile (R3). |

**Engineering constraints**: Docker-only testing respected. Backward compatibility: `[readonly]` is
an unknown attribute today (`RUNE2007`), so no existing Runefile changes meaning, and a client that
relied on the old claim gets the release note in R7. "Surface changes carry their docs":
`docs/GRAMMAR.md` and a corpus fixture change in the same PR. The package layout does not change.

**Post-design re-check (after Phase 1)**: no deviations. Design corrected the spec in two places.
FR-009 said `RUNE2018` stops every command that loads the Runefile, but `--fmt` runs no analysis, so
it now names the commands analyzer errors really stop. FR-016 named two documents, but the "read-only
by default" claim is in eight files, so it now covers all of them.

## Project Structure

### Documentation (this feature)

```text
specs/033-mcp-tool-hints/
├── plan.md                       # This file
├── spec.md                       # Feature spec
├── research.md                   # Phase 0: baseline and decisions R1-R9
├── data-model.md                 # Phase 1: attribute, descriptor, reach, hint derivation
├── quickstart.md                 # Phase 1: automated gates and validation scenarios
├── contracts/
│   ├── mcp-tools.md              # tools/list hints as a client sees them
│   ├── mcpserver-api.md          # TaskInfo fields for embedders
│   └── readonly-attribute.md     # the attribute and RUNE2018
├── checklists/requirements.md
└── tasks.md                      # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/ast/ast.go               # AttrReadonly, added to KnownAttributes
internal/parser/
├── attribute.go                  # readonly joins the no-argument case
└── typed_params_test.go          # TestParserKnowsAllASTAttributes guards it; arguments rejected
internal/language/builtin.go      # registry entry: completion and hover; confirm and network docs
internal/formatter/formatter_test.go  # [readonly, network] formats and round-trips
internal/diag/codes.go            # CodeInvalidReadonly = "RUNE2018"
internal/analyzer/
├── analyzer.go                   # checkReadonly, per edge
└── readonly_test.go              # NEW: codes, texts, spans, passes
internal/analysis/service_test.go # RUNE2018 reaches the editor, at the declaring file
internal/cli/
├── mcp.go                        # Tasks() fills ReadOnly, ReachesDestructive, Network; reach()
└── mcp_test.go                   # reach on every edge kind, private, injected OS; agent endpoint
mcpserver/
├── server.go                     # TaskInfo fields; toolFor derives the three hints
├── server_test.go                # hint derivation, including through a client
├── authz_test.go                 # ReachesDestructive is not refused
└── transport_test.go             # the same hints over HTTP
test/integration/
├── typed_schema_mcp_test.go      # mcpToolsList takes server flags
└── tool_hints_mcp_test.go        # NEW: quickstart scenarios on the binary
testdata/corpus/
├── readonly-attr.rune            # NEW
└── readonly-attr.ast             # NEW, generated with -update
editors/vscode/syntaxes/runefile.tmLanguage.json  # highlight [readonly]
docs/
├── GRAMMAR.md                    # attribute production
├── runefile.md                   # attribute reference
├── mcp.md                        # tool hints section; security model; example
├── diagnostics.md                # RUNE2018
├── user-guide/README.md          # drop the claim
├── use-cases/mcp-agents.md       # drop the claim; list-tasks uses [readonly]
└── examples/agent-driven/        # Runefile and README: same
README.md, SECURITY.md, website/src/landing/index.mdx  # drop the claim
.specify/memory/constitution.md   # Principle VII wording, v1.3.0
```

**Structure Decision**: Existing layout. The static rule goes in the analyzer with the other
attribute checks. The walk goes in the adapter because it needs the host OS and the task graph, and
the library has neither (R3).

## Complexity Tracking

No deviations to justify. Principle VII's wording changes through an amendment (R8), not a recorded
exception.
