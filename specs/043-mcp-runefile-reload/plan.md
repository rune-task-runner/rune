# Implementation Plan: Reload Tools When the Runefile Changes

**Branch**: `043-mcp-runefile-reload` | **Date**: 2026-11-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/043-mcp-runefile-reload/spec.md`

## Summary

Close issue #41. `ServeMCP` (`internal/cli/serve.go`) loads the Runefile once, builds one
`mcpAdapter`, and `mcpserver.New` registers one tool per task. Nothing reads the files again.

This plan keeps the adapter immutable and makes the server swappable. `mcpserver.Server` holds
its engine and task table in one snapshot behind an atomic pointer; each tool handler reads the
snapshot once per call, so a call uses one consistent set of definitions (R1). A new
`Server.Replace(engine, outputLimit)` builds the next snapshot, registers only the tools whose
advertised form differs, removes the ones that are gone, and returns the counts; the SDK then
sends one `notifications/tools/list_changed` (R2, R3). `Server.SetInstructions` changes the text
new sessions receive, applied by a receiving middleware on `initialize` (R4).

In the CLI, `loadModule` records every path the load read or probed (R5). A new reloader
(`internal/cli/mcpreload.go`) watches the directories of those paths with the existing `fsnotify`
dependency, debounces events for 100 ms, loads a fresh adapter, and calls `Replace`; a failed
load prints the diagnostics and one line and changes nothing (R6, R7). `--no-reload` skips the
reloader (R8). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`github.com/modelcontextprotocol/go-sdk` v1.6.1,
`github.com/fsnotify/fsnotify` v1.10.1, `github.com/spf13/cobra`). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` (in-memory transport, under
`goleak`) and `internal/cli`; binary-level stdio and HTTP tests in `test/integration`; the docs
harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: Notification within 2 s of a save (FR-018): 100 ms debounce, one load,
10 ms SDK notification delay. No profile-driven work.

**Constraints**: A failed reload changes nothing and runs no task code (FR-021, FR-023). A call
uses one snapshot (FR-011). `tools/list` never shows a half-applied swap (FR-019). Nothing is
written to stdout on stdio (FR-025). A server with `--no-reload` or unchanged files behaves as
before (FR-030, FR-031). One reloader goroutine, owned by `ServeMCP`, that exits before
`ServeMCP` returns (Principle VIII). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 2 new Go source files (`mcpserver/reload.go`, `internal/cli/mcpreload.go`),
changes to 6 (`mcpserver/server.go`, `mcpserver/handler.go`, `mcpserver/authz.go`,
`internal/cli/serve.go`, `internal/cli/dispatch.go`, `cmd/rune/serve.go`), 3 new test files
(`mcpserver/reload_test.go`, `internal/cli/mcpreload_test.go`,
`test/integration/reload_mcp_test.go`), and 3 documents (`docs/mcp.md`, `docs/cli.md`,
`docs/troubleshooting.md` if it lists the restart advice). `mcpserver` gains two methods, one
type, and one `Options` field.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | A reload runs no task. Tasks still run on every call. |
| II. Errors are a feature | PASS | A failed reload prints the same `file:line:col` diagnostics with caret spans as a start, and has no side effects. The analyzer still runs before any execution. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package. The swap lives in the public `mcpserver` package, the watcher in `internal/cli` beside `watch.go`. |
| V. Boringly portable | PASS | `fsnotify` is already a dependency and covers the three OSes. Directories are watched, not files, so rename-over saves work everywhere. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer: swap and notification in `mcpserver`, loader paths and reloader in `internal/cli`, the real binary on stdio and HTTP in `test/integration`, and the docs harness. |
| VII. AI-native, secure by default | PASS | Filters and grants apply to reloaded tasks by the same rules. `[confirm]` tasks still need an operator grant by name. Masking uses the reloaded secrets. `--no-reload` keeps the tasks fixed for operators who want that. The trade-off is recorded in the spec's assumptions. |
| VIII. Go engineering discipline | PASS | One goroutine with a clear owner (`ServeMCP`) and exit (context cancel, then joined). No `init()`, no globals. The `[context]` hook keeps its timeout. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. A server whose files do not change behaves as before. |
| Constraint: surface changes carry their docs | PASS | No DSL surface. `docs/mcp.md`, `docs/cli.md`, and the help text change in the same PR. |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contracts add one flag, two stderr lines, three warnings,
two library methods, one result type, and one `Options` field. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/043-mcp-runefile-reload/
├── plan.md              # This file
├── research.md          # Decisions R1 to R10
├── data-model.md        # Snapshot, load result, reloader state
├── quickstart.md        # Manual validation
├── contracts/
│   ├── cli.md           # Flag, stderr lines, warnings
│   ├── mcp-protocol.md  # Capabilities, notification, instructions
│   └── mcpserver-api.md # Library additions
├── checklists/requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
mcpserver/
├── server.go            # snapshot behind atomic pointer; Options.Reloadable; capabilities
├── reload.go            # NEW: Replace, Change, SetInstructions, list and initialize middleware
├── handler.go           # handler reads the snapshot per call
├── authz.go             # refusal takes its data from the snapshot's task
└── reload_test.go       # NEW

internal/cli/
├── serve.go             # loadModule returns the paths it touched; ServeMCP starts the reloader
├── mcpreload.go         # NEW: reloader (watch, debounce, load, replace, report)
├── mcpreload_test.go    # NEW
└── dispatch.go          # Options.MCPNoReload

cmd/rune/serve.go        # --no-reload flag and help text

test/integration/reload_mcp_test.go   # NEW: real binary, stdio and HTTP

docs/mcp.md, docs/cli.md              # reload behavior and --no-reload
```

**Structure Decision**: The existing layout. The library change stays in `mcpserver`, the
file watching in `internal/cli`, and the flag in `cmd/rune`.

## Complexity Tracking

No violations to justify.
