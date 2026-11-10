# Implementation Plan: Refuse a Destructive Dependency Before the Call Runs

**Branch**: `034-mcp-dependency-refusal` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/034-mcp-dependency-refusal/spec.md`

## Summary

Close issue #38. Today `refusal` in `mcpserver/authz.go` reads only the called task's own
`[confirm]`. A call that reaches an ungranted `[confirm]` task runs until the scheduler gets there,
and the engine then declines it silently, so the agent sees a bare `[exit 1]` after earlier work has
run.

Spec 033 already works out what each task can run, in `mcpAdapter.reach`, and reports it as the
unreleased `TaskInfo.ReachesDestructive bool`. This plan replaces that bool with
`DestructiveReach []string`: the destructive tasks the run can reach, in the order a run first
reaches them (R2, R3). The library's `refusal` then checks the called task and that list against the
grants, before argument checks, and builds one message that names every ungranted task and the flag
(R1, R4). The handler passes its own descriptor to `refusal`, so the per-call `Tasks()` rebuild in
`isDestructive` goes away (R5). The engine's run-time decline stays as the backstop (R6). Docs that
say only the `[confirm]` task itself is refused change (R8). Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1). No new
modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` and `internal/cli`; binary-level
stdio tests in `test/integration`; the docs harness in `test/docs` (R7).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. The reach walk already runs once per listed task when the server is
built (spec 033). This plan adds no work per call and removes one `Tasks()` rebuild per call (R5).

**Constraints**: Nothing in a refused call runs (FR-004). The direct refusal text stays
byte-identical (FR-007). Calls that reach no ungranted destructive task behave as today (FR-012).
Hints do not change (FR-015). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 4 Go source files (`mcpserver/server.go`, `mcpserver/authz.go`,
`mcpserver/handler.go`, `internal/cli/mcp.go`), 1 new test file, changes to 7 existing test files,
and 3 documents. `mcpserver.TaskInfo` swaps one unreleased field for another.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | A refused call runs nothing and says why. No work is skipped silently. |
| II. Errors are a feature | PASS | A refusal that was a bare exit code now names the tasks and the fix, with zero side effects. It is a run-time policy result, not a static diagnostic, as the direct refusal is. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | Changes stay in `internal/cli` and the public `mcpserver` package. No new package. |
| V. Boringly portable | PASS | Reach uses the adapter's injected host OS, as spec 033 does. No OS-specific code. |
| VI. Test-first, multi-layer verification | PASS | Each layer gets failing tests first: refusal texts and order in `mcpserver`, reach order in `internal/cli`, marker files at binary level, and the docs harness (R7). |
| VII. AI-native, secure by default | PASS | Strengthens "destructive tasks are refused on the agent surface by default": a call that can run one is now refused before anything runs. The refusal carries only task names from the Runefile, never an environment value. |
| VIII. Go engineering discipline | PASS | No goroutines, no new external calls. The per-call `Tasks()` removal is a simplification the descriptor change needs, not an optimization claim (R5). `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No DSL change. The MCP behavior change ships pre-1.0 as a `fix(mcp)!` commit with the change in its subject, which the changelog carries (R9). |
| Constraint: surface changes carry their docs | PASS | No DSL surface change. The MCP docs change in the same PR (R8). |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contracts add one descriptor field and one message rule, and
remove one unreleased field. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/034-mcp-dependency-refusal/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R9
├── data-model.md        # Phase 1: reach order, refused set, message forms
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── mcp-tools.md     # tools/call results and refusal texts
│   └── mcpserver-api.md # TaskInfo and Options changes for embedders
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
mcpserver/
├── server.go            # TaskInfo.DestructiveReach replaces ReachesDestructive; toolFor's hint;
│                        #   Options.AllowDestructive doc
├── authz.go             # refusal(TaskInfo): own and reached tasks, one message; isDestructive removed
├── handler.go           # passes the handler's TaskInfo to refusal
├── authz_test.go        # message forms, grant filtering, allow-list first
├── handler_test.go      # dependency refusal before argument checks; engine not called
├── server_test.go       # hint tests move to DestructiveReach
└── transport_test.go    # field rename; one dependency refusal over HTTP

internal/cli/
├── mcp.go               # reach returns ordered destructive names; Tasks() sets DestructiveReach
└── mcp_test.go          # reach order cases; agent endpoint refuses release with the no-flag text

test/integration/
├── dependency_refusal_mcp_test.go  # new: quickstart scenarios with marker files
├── destructive_mcp_test.go         # ungranted dependency now expects the refusal, not [exit 1]
└── tool_hints_mcp_test.go          # CallsUnchanged pins the new release text

docs/mcp.md, docs/runefile.md, SECURITY.md   # what is refused, and the message
```

**Structure Decision**: Existing layout. The check lives in the public `mcpserver` package, where the
grants and the flag name already are, so embedders get it through the descriptor (FR-014). The CLI
adapter only reports reach.

## Complexity Tracking

No constitution violations to justify.
