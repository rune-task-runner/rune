# Implementation Plan: Cap Task Output Before It Reaches the Agent

**Branch**: `042-mcp-output-cap` | **Date**: 2026-11-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/042-mcp-output-cap/spec.md`

## Summary

Close issue #40. `mcpAdapter.CallLists` (`internal/cli/mcp.go`) captures stdout and stderr in two
`bytes.Buffer` values with no bound, and `formatResult` (`mcpserver/handler.go`) returns all of it.

This plan adds one bounded head-and-tail buffer, `mcpserver.OutputBuffer` (R1, R2). It holds at
most the limit in bytes while the task writes, and renders the whole stream when it fits or
`head + "\n[truncated N bytes]\n" + tail` when it does not, marker counted inside the limit (R3).
The CLI adapter writes both streams into it, behind the existing mask writer, so only masked text
is ever kept or dropped (R5). The handler caps every engine's result with the same code, which
covers embedders and is a no-op for the adapter's already-capped text (R4). The limit comes from
`rune mcp --output-limit`, else `set mcp_output_limit`, else 64 KiB; sizes are parsed in
`internal/config` and rejected before any Runefile code runs (R6, R7). Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk`,
`github.com/spf13/cobra`). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver`, `internal/config`, and
`internal/cli`; binary-level stdio tests in `test/integration`; the docs harness in `test/docs`
(R9).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: A write is a bounded copy into a fixed region. No profile-driven work.

**Constraints**: Each stream's capture holds at most the limit in bytes (FR-015). A stream within
the limit is returned byte for byte (FR-003). The result up to the exit line is at most twice the
limit plus 32 bytes (FR-009). Masking precedes capture (FR-013). A bad flag value runs no Runefile
code (FR-023). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 2 new Go source files (`mcpserver/output.go`, `internal/config/size.go`), changes to 9 (`mcpserver/server.go`,
`mcpserver/handler.go`, `internal/config/settings.go`, `internal/cli/mcp.go`,
`internal/cli/serve.go`, `internal/cli/agentexec.go`, `internal/cli/dispatch.go`, `internal/language/builtin.go`,
`cmd/rune/serve.go`), 3 new test files, changes to about 7 existing test files, and 4 documents.
`mcpserver` gains one type, three constants, and one `Options` field.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Tasks run as before. Only what is returned to an agent is bounded; a task is never stopped for printing (FR-017). |
| II. Errors are a feature | PASS | A bad `set mcp_output_limit` is a diagnostic with `file:line:col` and a caret on the value, before any task runs. A bad `--output-limit` is a usage error with exit 2, like the other `rune mcp` flags. |
| III. Minimal, total DSL | PASS | One new setting with a static string value. No expression-language change. It ships with `docs/GRAMMAR.md` and fixtures. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package. The buffer lives in the public `mcpserver` package, the size parser in `internal/config`. |
| V. Boringly portable | PASS | No OS-specific code. Limits fit a 32-bit `int` (maximum 1 GiB). |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer: buffer and handler in `mcpserver`, size parser in `internal/config`, adapter and flag in `internal/cli`, 3 MB runs at binary level, and the docs harness (R9). |
| VII. AI-native, secure by default | PASS | Smaller results, never more exposure. Masking stays upstream of capture, so a cut cannot expose part of a secret. The marker carries a byte count of masked output only. |
| VIII. Go engineering discipline | PASS | No goroutines, no `init()`, no globals. The buffer owns one mutex. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No existing Runefile changes meaning; the new setting is additive. The result change for outputs over 64 KiB ships as a `feat(mcp)!` commit so the changelog carries it (R10). |
| Constraint: surface changes carry their docs | PASS | `docs/GRAMMAR.md`, `docs/runefile.md`, `docs/mcp.md`, `docs/cli.md`, the help text, and the language registry change in the same PR (R10). |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contracts add one flag, one setting, one library type, and one
`Options` field. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/042-mcp-output-cap/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R10
├── data-model.md        # Phase 1: limit, buffer states, rendering
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli-and-setting.md  # --output-limit, set mcp_output_limit, size syntax, errors
│   ├── mcp-results.md      # the result text under the limit
│   └── mcpserver-api.md    # OutputBuffer, constants, Options.OutputLimit
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
mcpserver/
├── output.go            # new: OutputBuffer, DefaultOutputLimit, MinOutputLimit, MaxOutputLimit
├── output_test.go       # new: whole, cut, marker arithmetic, UTF-8, chunking, memory, race
├── server.go            # Options.OutputLimit; New resolves it
├── handler.go           # formatResult caps each stream with the server's limit
└── handler_test.go      # capped results from a plain Engine, exit line, fix suggestion

internal/config/
├── size.go              # new: ParseOutputLimit (size syntax and range)
├── size_test.go         # new
├── settings.go          # Settings.MCPOutputLimit, the mcp_output_limit case and diagnostic
└── settings_test.go     # valid, invalid, span

internal/cli/
├── dispatch.go          # Options.MCPOutputLimit (*string)
├── mcp.go               # mcpAdapter.outputLimit; CallLists captures into OutputBuffer
├── serve.go             # parse the flag first; flag over setting over default; pass to Options
├── agentexec.go         # agentMCPOptions carries the setting's limit
├── mcp_test.go          # bounded capture, masking at the cuts, parallel writers
└── serve_test.go        # flag errors with no side effects, precedence

internal/language/builtin.go   # the setting in the language registry (LSP completion, hover)

cmd/rune/serve.go        # --output-limit flag, Long and Example text

test/integration/
└── output_cap_mcp_test.go    # new: quickstart scenarios with the real binary

docs/mcp.md, docs/cli.md, docs/runefile.md, docs/GRAMMAR.md
```

**Structure Decision**: Existing layout. The buffer is in the public `mcpserver` package because
both the handler (every engine) and the CLI adapter (memory bound) need it, `internal/cli` already
imports `mcpserver`, and an embedder writing an `Engine` needs the same bound (R1).

## Complexity Tracking

No constitution violations to justify.
