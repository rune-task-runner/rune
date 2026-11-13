# Implementation Plan: Operator Allowlist and Denylist for MCP Tasks

**Branch**: `041-mcp-allow-deny` | **Date**: 2026-11-12 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/041-mcp-allow-deny/spec.md`

## Summary

Close issue #39. `mcpserver.Options.AllowList` exists but no flag sets it, and it only refuses
calls: `refusal` in `mcpserver/authz.go` returns `task "X" is not authorized for this session`
while the task stays in `tools/list`.

This plan adds `--allow` and `--deny` to `rune serve` / `rune mcp` and moves the filter to where
tools are registered. `mcpserver.New` skips a task the filters hide, so it is absent from the list
and a call to it gets the SDK's own unknown-tool error, the same as a name no task has (R1).
`AllowList` takes that meaning and `DenyList` is added (R2). The server reports how many tasks it
exposed, for the startup line and the empty-surface warning (R3, R7, R8). The CLI reuses the
`--allow-destructive` parser and name check for all three flags, so unknown and empty names are
usage errors with exit 2 before any Runefile code runs (R4, R5). Names that can never be tools warn
(R6). Destructive authorization, dependency and hook execution, hints, and the `agent` task endpoint
do not change (R9). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1,
`github.com/spf13/cobra` v1.10.2). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` and `internal/cli`; binary-level
stdio tests in `test/integration`; the docs harness in `test/docs` (R10).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. The filter is two slice lookups per task, once, when the server is
built. No work is added per call.

**Constraints**: A usage error runs no Runefile code (FR-011). With neither flag, every listing,
call, refusal, and startup line is unchanged (FR-016). The `--allow-destructive` messages stay
byte-identical. A hidden task's response is the unknown-tool response (FR-004). New prose avoids the
em dash (maintainer rule).

**Scale/Scope**: 5 Go source files (`mcpserver/server.go`, `mcpserver/authz.go`,
`internal/cli/serve.go`, `internal/cli/dispatch.go`, `cmd/rune/serve.go`), 1 new test file, changes
to about 6 existing test files, and 2 documents. `mcpserver.Options` gains one field and changes
the meaning of one; `mcpserver.Server` gains one method.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | The filters choose which tasks are tools. A called task runs its whole graph; nothing is skipped. |
| II. Errors are a feature | PASS | A mistyped name in either flag is a usage error naming the flag, the name, and the closest task, with zero side effects. It is a command-line error, not a Runefile diagnostic, so it has no source span, as for `--allow-destructive`. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | Changes stay in `internal/cli`, `cmd/rune`, and the public `mcpserver` package. No new package. |
| V. Boringly portable | PASS | No OS-specific code. A name restricted to another OS is accepted silently so one client configuration works on every host. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer: registration filter in `mcpserver`, flag parsing and startup texts in `internal/cli`, marker files at binary level, and the docs harness (R10). |
| VII. AI-native, secure by default | PASS | Narrows the agent surface and never widens it: private tasks, the `[context]` hook, and other-OS tasks cannot be added, and `--allow` grants no destructive task. Destructive tasks stay refused by default. No secret reaches a listing or message; the texts carry task names only. |
| VIII. Go engineering discipline | PASS | No goroutines, no new external calls, no `init()` or globals. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No DSL change. The library change ships pre-1.0 as a `feat(mcp)!` commit with the change in its subject, which the changelog carries (R11). |
| Constraint: surface changes carry their docs | PASS | No DSL surface change. The MCP guide, CLI reference, and help text change in the same PR (R11). |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contracts add two flags, one `Options` field, and one `Server`
method, and remove one refusal text. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/041-mcp-allow-deny/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R11
├── data-model.md        # Phase 1: the sets, validation, start states
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli-serve.md     # flags, errors, warnings, startup line, help
│   ├── mcp-tools.md     # tools/list and tools/call under the filters
│   └── mcpserver-api.md # Options and Server changes for embedders
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
mcpserver/
├── server.go            # Options.AllowList doc, Options.DenyList; New skips hidden tasks and
│                        #   counts; Server.Exposure()
├── authz.go             # refusal loses its allowlist branch and comment line
├── authz_test.go        # the three allowlist refusal tests become registration tests
├── handler_test.go      # the allowlist case leaves the refusal-before-arguments table
├── server_test.go       # filter cases, hidden equals missing, Exposure, list entries unchanged
└── transport_test.go    # one filtered list over HTTP

internal/cli/
├── dispatch.go          # Options.MCPAllow, Options.MCPDeny
├── serve.go             # parseTaskNames, checkTaskNames (generalized), warnNeverExposed,
│                        #   startupLine with counts, ServeMCP wiring
├── serve_test.go        # parser and name check per flag, both warnings, every startupLine form
└── mcp_test.go          # other-OS allowlist case; agent endpoint unchanged

cmd/rune/
└── serve.go             # --allow and --deny flags, Long and Example text

test/integration/
└── allowlist_mcp_test.go   # new: quickstart scenarios A to P with marker files

docs/mcp.md, docs/cli.md    # the two flags, how they combine, what --deny does not do
```

**Structure Decision**: Existing layout. The filter lives in the public `mcpserver` package, where
tools are registered, so embedders get it and the unknown-tool response comes from the SDK (FR-018,
R1). The CLI validates names and prints the operator-facing lines, as it does for
`--allow-destructive`.

## Complexity Tracking

No constitution violations to justify.
