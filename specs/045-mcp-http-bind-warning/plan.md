# Implementation Plan: Warn on Non-Loopback MCP HTTP Binds

**Branch**: `045-mcp-http-bind-warning` | **Date**: 2026-11-15 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/045-mcp-http-bind-warning/spec.md`

## Summary

Close issue #43. `ServeHTTP` (`mcpserver/transport.go`) holds a guard that can never fire, and
a bind beyond loopback sends the bearer token over plain HTTP with no word to the operator.

This plan deletes the guard and reports the bind. The loopback decision moves from the text of
the address to the address the listener bound (R1), which also fixes `:7777` and host names.
`HTTPConfig` gains a `Warn` callback; `ServeHTTP` and `StartHTTP` call it once, after a
successful bind, when that address is not loopback (R2, R3). The `rune` command passes a
callback that prints `warning: ...` on stderr in the style of its other startup warnings (R5);
the endpoint of an `agent` task passes the same callback and stays on loopback.
Help and documentation stop showing a non-loopback example and describe the warning (R6). Full
decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`net`, `net/http`,
`github.com/modelcontextprotocol/go-sdk`). No module is added.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` (under `goleak`), `internal/cli`
and `cmd/rune`; a binary-level test in `test/integration`; the docs harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: None. One type assertion per server start.

**Constraints**: The decision uses the bound address and adds no lookup (FR-009). One warning
per start, none for a failed bind (FR-002). The library writes nothing on its own (FR-012). No
command that starts today is refused (FR-010). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: Changes to 4 Go source files (`mcpserver/transport.go`,
`internal/cli/serve.go`, `internal/cli/agentexec.go`, `cmd/rune/serve.go`), tests in 4 existing test files
(`mcpserver/transport_test.go`, `internal/cli/serve_test.go`, `internal/cli/mcp_test.go`,
`cmd/rune/serve_test.go`), 1 new
test file (`test/integration/http_bind_warning_test.go`), and 3 documents (`docs/mcp.md`,
`docs/cli.md`, `SECURITY.md`). One new exported identifier: the field `mcpserver.HTTPConfig.Warn`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No task behavior changes. |
| II. Errors are a feature | PASS | An error that could never be returned is removed. The new output is a warning with a fixed, specific text. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package. `mcpserver` stays public and embeddable; it gains one optional field. |
| V. Boringly portable | PASS | `net.IP.IsLoopback` on the bound address behaves the same on every OS. No OS-specific code. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer: the decision and both entry points in `mcpserver`, the printed line in `internal/cli`, the help in `cmd/rune`, the real binary in `test/integration`, and the docs harness. |
| VII. AI-native, secure by default | PASS | The default bind stays loopback and the token stays required. A bind beyond loopback was already possible as an explicit choice; it is now announced. The warning carries no secret and nothing from the Runefile. |
| VIII. Go engineering discipline | PASS | No new goroutine, no `init()`, no globals beyond one constant. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. Every server that started still starts. |
| Constraint: surface changes carry their docs | PASS | No DSL surface. `docs/mcp.md`, `docs/cli.md` and the command help change in the same PR. |

No violations. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contract adds one callback field and one stderr line. No
principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/045-mcp-http-bind-warning/
├── plan.md              # This file
├── research.md          # Decisions R1 to R8
├── data-model.md        # HTTPConfig, the bound address, the warning
├── quickstart.md        # Manual validation
├── contracts/
│   └── http-bind-warning.md   # stderr of `rune serve --http`, HTTPConfig.Warn, help
├── checklists/requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
mcpserver/
├── transport.go         # HTTPConfig.Warn, cleartextWarning, warnCleartext, isLoopback(net.Addr);
│                        # dead guard removed; ServeHTTP and StartHTTP report after Listen
└── transport_test.go    # isLoopback table, StartHTTP and ServeHTTP warn/quiet/failed bind

internal/cli/
├── serve.go             # warnTransport; ServeMCP passes it as HTTPConfig.Warn; defaultHTTPAddr
├── agentexec.go         # agentHTTPConfig: the agent task endpoint passes the same callback
├── mcp_test.go          # the agent task endpoint binds loopback
└── serve_test.go        # warnTransport, ServeMCP warns on 0.0.0.0 and is quiet on loopback,
                         # the default address is loopback

cmd/rune/
├── serve.go             # help: loopback example, --addr default, cleartext paragraph
└── serve_test.go        # help text

test/integration/http_bind_warning_test.go   # NEW: real binary, both binds
docs/mcp.md                                  # HTTP transport: no TLS, loopback, the warning
docs/cli.md                                  # `rune serve` row
SECURITY.md                                  # the remote endpoint line matches docs/mcp.md
```

**Structure Decision**: The existing layout. The decision and the text live in `mcpserver`; the
`rune` command only prints.

## Complexity Tracking

No violations to justify.
