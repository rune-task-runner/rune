# Implementation Plan: Reject Unknown MCP Tool Arguments

**Branch**: `032-mcp-unknown-arguments` | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/032-mcp-unknown-arguments/spec.md`

## Summary

Close issue #36. A tool call that misspells an argument name succeeds with the parameter's
default, because `decodeArgs` passes unknown keys through and `bindNamedParams` never reads them.
The agent receives a success for the wrong operation.

The plan adds one check to the tool handler, after the authorization refusal and before value
decoding (R1). A call naming any undeclared parameter gets an error result, nothing runs, and the
text names each unknown argument and the closest declared parameter (R2). The suggestion rule moves
from `internal/cli` to a new `internal/suggest` package, because `mcpserver` cannot import
`internal/cli` (R3). `inputSchema` adds `"additionalProperties": false` to every tool (R5). Code
review added a refusal for values a task has no way to receive, such as null (R8).

The check lives in the library, so stdio, HTTP, the agent-task endpoint, and every embedder's engine
get it from one place. Calls that use only declared names and values a task can receive behave as
before. Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1). No new
modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `internal/suggest`, `mcpserver`, and
`internal/cli`; binary-level stdio tests in `test/integration`; docs harness in `test/docs` (R6).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. An edit distance per unknown name, only on a call that is refused, with
the refusal's size bounded (FR-005).

**Constraints**: Nothing executes for a refused call (FR-001). A call using only declared names is
unchanged (FR-012). No argument value appears in the text (FR-007). The text is the same on every
run (FR-005). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 5 Go source files (`mcpserver/handler.go`, `mcpserver/server.go`,
`internal/cli/args.go`, `internal/cli/serve.go`, and `internal/cli/suggest.go` moved to
`internal/suggest/suggest.go`). One moved test file, changes to 5 existing test files, 1 new
integration test file, and 1 doc page. No exported API changes.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.2.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No skipping logic. A refused call was never going to run the operation the agent asked for. |
| II. Errors are a feature | PASS | A silent wrong result becomes an exact error with a suggestion and zero side effects. |
| III. Minimal, total DSL | PASS | No Runefile syntax, attribute, or built-in changes. |
| IV. Hand-written front end, idiomatic Go | PASS (deviation recorded) | One new small `internal/` package, see Complexity Tracking. `mcpserver` stays public, with no exported change. |
| V. Boringly portable | PASS | No OS-specific code. Test bodies write marker files with shell redirection, which the built-in shell runs the same on every OS. |
| VI. Test-first, multi-layer verification | PASS | Helper, library, transport, adapter, binary, and docs layers (R6). No golden or corpus changes. |
| VII. AI-native, secure by default | PASS | Agents get accurate results, and the schema states what is allowed. Argument values never appear in the error (FR-007). The HTTP token check comes first and is unchanged. |
| VIII. Go engineering discipline | PASS | No goroutines or globals. Decode errors are handled, and `decodeArgs` reports them as it does today (R1). |

**Engineering constraints**: Docker-only testing respected. Backward compatibility: no DSL change, so
every Runefile keeps its meaning. A client sending extra names now gets an error, which is acceptable
before 1.0 with the release note in R7. "Surface changes carry their docs": `docs/mcp.md` changes in the
same PR.

**Post-design re-check (after Phase 1)**: one deviation, recorded below. Design changed the spec in one
place: the "No arguments" edge case and FR-012 now include `null`, which runs the task today and must
keep doing so.

## Project Structure

### Documentation (this feature)

```text
specs/032-mcp-unknown-arguments/
├── plan.md                    # This file
├── spec.md                    # Feature spec
├── research.md                # Phase 0: baseline and decisions R1-R7
├── data-model.md              # Phase 1: declared parameters, order of checks, suggestion
├── quickstart.md              # Phase 1: automated gates and scenarios A-W
├── contracts/
│   └── mcp-tools.md           # tools/list and tools/call as a client sees them
├── checklists/requirements.md
└── tasks.md                   # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/suggest/              # NEW package
├── suggest.go                 # Nearest and levenshtein, moved from internal/cli/suggest.go
└── suggest_test.go            # TestNearest moved with every case; limit, tie, and no-candidate cases added

internal/cli/
├── args.go                    # "unknown task" suggestion calls suggest.Nearest
├── serve.go                   # --allow-destructive suggestion calls suggest.Nearest
└── mcp_test.go                # the agent-task endpoint refuses and runs nothing

mcpserver/
├── handler.go                 # decode once, then the name check, then the value checks
├── server.go                  # inputSchema sets additionalProperties false
├── handler_test.go            # texts, order, nothing reaches the engine
├── server_test.go             # the keyword on every parameter shape
└── transport_test.go          # the same refusal over HTTP

test/integration/
├── destructive_mcp_test.go    # toolReply, shared with the new file
└── unknown_args_mcp_test.go   # NEW: quickstart scenarios over stdio with marker files

docs/
└── mcp.md                     # parameters bullet, and the sentence on rejected calls below it
```

**Structure Decision**: Existing layout, plus `internal/suggest`. The check goes in `mcpserver`, the one
place every transport and engine passes through.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| New package `internal/suggest` (Principle IV locks the package layout; structural changes need justification) | `mcpserver` needs the rule FR-004 requires, and it cannot import `internal/cli`, which imports it. Issue #36 asks for a shared helper in this case. | A copy in `mcpserver` lets the two rules drift apart. `internal/ast` has nothing to do with edit distance. Exporting it from `mcpserver` widens the public API for an internal helper (R3). |
