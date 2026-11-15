# Implementation Plan: Return Structured Tool Results

**Branch**: `044-mcp-structured-results` | **Date**: 2026-11-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/044-mcp-structured-results/spec.md`

## Summary

Close issue #42. The tool handler (`mcpserver/handler.go`) returns one text block built by
`formatResult` from an engine `Result{Stdout, Stderr, ExitCode, FixSuggestion}`. `toolFor`
(`mcpserver/server.go`) declares no output schema.

This plan splits the two steps `formatResult` does today: `capResult` caps the two streams, and
`renderText` builds the text from the capped result. The handler caps once and builds both the
text and a `structuredResult` from the same capped value, so the fields are by construction the
strings the text is made of (R1, R2). `toolFor` sets one fixed `outputSchema()` on every tool
(R3). Calls that never reach the engine keep `errorResult`, which has no structured content
(R4). Nothing changes in `internal/cli`: masking and the bounded capture happen in the adapter
before the handler sees the result, and the agent task endpoint uses the same server (R5). Full
decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1).
`github.com/google/jsonschema-go`, already in the module graph through the SDK, becomes a direct
requirement for one test that validates results against the declared schema (R6). No module is
added to `go.sum`.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` (in-memory transport, under
`goleak`) and `internal/cli` (real adapter); binary-level stdio tests in `test/integration`; the
docs harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: One extra marshal of strings that are already capped. No profile-driven
work.

**Constraints**: Text content byte-identical (FR-014). Fields are the exact capped, masked
strings the text is built from (FR-007, FR-011, FR-012). Refusals unchanged, with no structured
content (FR-016). One schema for all tools (FR-003). A reload that changes nothing sends no
notification (FR-018). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new Go source file (`mcpserver/structured.go`), changes to 2
(`mcpserver/handler.go`, `mcpserver/server.go`), 3 new test files
(`mcpserver/structured_test.go`, `internal/cli/mcp_structured_test.go`,
`test/integration/structured_mcp_test.go`), `go.mod`, and 1 document (`docs/mcp.md`). No new
exported identifier.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Tasks run as before. Only the shape of what is returned grows. |
| II. Errors are a feature | PASS | No new error path. Refusals and invalid arguments keep their text. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package. The change stays in the public `mcpserver` package. |
| V. Boringly portable | PASS | No OS-specific code. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer: schema and results in `mcpserver`, masking and caps through the real adapter in `internal/cli`, the real binary in `test/integration`, and the docs harness. Existing result-text tests pass unchanged. |
| VII. AI-native, secure by default | PASS | The fields are the strings the text is built from, taken after masking and after the cap, so they cannot carry more than the text. The schema's descriptions are fixed text with nothing from the Runefile or the environment. |
| VIII. Go engineering discipline | PASS | No goroutines, no `init()`, no globals: the schema is built by a function. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. Text, error flag, names, descriptions, input schemas and hints are unchanged. |
| Constraint: surface changes carry their docs | PASS | No DSL surface. `docs/mcp.md` documents the result format in the same PR. |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contract adds one schema and one result field. No principle
verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/044-mcp-structured-results/
├── plan.md              # This file
├── research.md          # Decisions R1 to R8
├── data-model.md        # The structured result and its relation to the text
├── quickstart.md        # Manual validation
├── contracts/
│   └── mcp-results.md   # Output schema, structured content, text rule
├── checklists/requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
mcpserver/
├── structured.go        # NEW: outputSchema, structuredResult, capResult
├── handler.go           # handler builds text and structured content from one capped result;
│                        # formatResult = renderText(capResult(...))
├── server.go            # toolFor sets OutputSchema
└── structured_test.go   # NEW

internal/cli/mcp_structured_test.go        # NEW: masking and caps through the real adapter
test/integration/structured_mcp_test.go    # NEW: real binary over stdio
docs/mcp.md                                # "Structured results" section
go.mod                                     # jsonschema-go becomes a direct requirement
```

**Structure Decision**: The existing layout. The whole change is in `mcpserver`; `internal/cli`
gains only tests.

## Complexity Tracking

No violations to justify.
