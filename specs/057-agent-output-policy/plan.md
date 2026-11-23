# Implementation Plan: Per-Task Output Tails and Failure Summaries for Agents

**Branch**: `057-agent-output-policy` | **Date**: 2026-11-23 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/057-agent-output-policy/spec.md`

## Summary

Close issue #55. Two task attributes change what an agent receives over MCP, and nothing
else. `[agent-output(head="H", tail="T")]` keeps the first H (optional) and last T lines of
each stream with one omission line between them. `[agent-summary]` adds a section after
`[exit N]` of a failed call that names the task and step that failed, the reason, and the
last lines of each stream.

Lines are kept while the task writes, in a new `mcpserver.LineBuffer` that never holds more
than the output limit (R3, R4). The engine records what it observed about the failure from
the run error (R6). The server bounds the summary, adds the quoted lines, and renders the
text and two new optional structured fields from that one value (R6 to R9). A task with
neither attribute takes today's path unchanged.

KISS limits, all in the spec's Out of scope: no stored output, no model, no command-line
flag, no server-wide policy, one policy for both streams. Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Existing: `github.com/modelcontextprotocol/go-sdk`
(the tool result), `internal/ast`, `internal/parser`, `internal/analyzer`,
`internal/formatter`, `internal/language`, `internal/diag`, `internal/mask` (through the
existing masking writers), `internal/runtime/shell` and `internal/runtime/interp` (their
`ExecError`).

**Storage**: none. Every value lives for one call.

**Testing**: `go test` in Docker only. `mcpserver`: unit, property and race tests of
`LineBuffer`, summary bounding and rendering, schema and structured agreement. `internal/cli`:
the policy and the summary facts through the real engine and masking. Front end: parser,
analyzer (every RUNE2021 case), formatter round trip, language registry, corpus and fmt
fixtures. `test/integration`: the real `rune mcp` binary. The docs harness for the new
sections.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool with an embeddable MCP server library (existing layout)

**Performance Goals**: a write to a `LineBuffer` costs what a write to an `OutputBuffer`
costs: a copy into the ring and a count of line feeds. Rendering is linear in the limit.

**Constraints**: memory per stream at most the output limit while the task runs (FR-011).
The exit status and `isError` never change (FR-024). No secret in any kept text, summary or
field (FR-026). Results of tasks without the attributes byte for byte unchanged (FR-031). The
command line unchanged (FR-033). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 2 new attributes and 1 diagnostic code; 2 new files in `mcpserver`
(`linebuffer.go`, `summary.go`) and changes to `server.go`, `handler.go`, `structured.go`;
1 new file in `internal/cli` (`mcpoutput.go`) and a change to `mcp.go`; 1 new analyzer file;
small additions to `internal/ast`, `internal/parser`, `internal/formatter`,
`internal/language`, `internal/diag`; the editor grammar; corpus and fmt fixtures; 1 new
integration test file; docs: `runefile.md`, `GRAMMAR.md`, `diagnostics.md`, `mcp.md`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Not touched. |
| II. Errors are a feature | PASS | Every wrong attribute is a RUNE2021 error at the argument or value, with the fix in the message. The summary makes a failure clearer to an agent and never invents a cause. |
| III. Minimal, total DSL | PASS | Two declarative attributes through the normal spec process. Named string arguments already exist; no new syntax. Statically checked. Grammar docs and fixtures ship with them. |
| IV. Hand-written front end, idiomatic Go | PASS | The parser gains two cases in its attribute switch. No new package. |
| V. Boringly portable | PASS | Pure Go. A line ends at a line feed on every system. Paths in the summary use forward slashes. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer listed in Testing, corpus and fmt fixtures, the docs harness. |
| VII. AI-native, secure by default | PASS | Agents get compact results with the outcome intact. Masking stays before every cut. No attribute grants or relaxes anything. |
| VIII. Go engineering discipline | PASS | `LineBuffer` locks itself like `OutputBuffer`. No goroutine, no global state, no `init()`. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. |
| Constraint: locked package layout | PASS | New files in existing packages only. `LineBuffer` is public in `mcpserver` beside `OutputBuffer`, for embedders. |
| Constraint: backward compatibility | PASS | Not breaking. Both names were errors before. The output schema gains two optional properties, as spec 044 said this issue would do. |
| Constraint: surface changes carry their docs | PASS | `docs/GRAMMAR.md`, `docs/runefile.md`, corpus and fmt fixtures in the same PR. |

**Post-design re-check**: PASS. The contracts add two attributes, one diagnostic code, two
optional result fields and one exported buffer type. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/057-agent-output-policy/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R14
├── data-model.md        # Phase 1: policy, LineBuffer, reports, summary, result
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── runefile-attributes.md   # syntax, values, diagnostics, scope
│   └── mcp-results.md           # kept streams, omission line, summary, schema
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/ast/ast.go              # AttrAgentOutput, AttrAgentSummary, KnownAttributes,
                                 # AgentOutputLines, (*Task).AgentOutput
internal/ast/dump.go             # agent-output printed like agent-allow
internal/parser/attribute.go     # the two cases
internal/analyzer/agentoutput.go # checkAgentOutput (RUNE2021)
internal/analyzer/analyzer.go    # call it
internal/diag/codes.go           # CodeInvalidAgentOutput
internal/formatter/formatter.go  # agent-output printed like agent-allow
internal/language/builtin.go     # completion and hover entries
editors/vscode/syntaxes/runefile.tmLanguage.json   # the two names
testdata/corpus/agent-output.{rune,ast}, testdata/fmt/agent-output.{rune,fmt}

mcpserver/linebuffer.go          # LineBuffer, StreamReport
mcpserver/summary.go             # OutputReport, FailureSummary, bounding, tails, rendering
mcpserver/server.go              # Result.Output, Result.Failure
mcpserver/handler.go             # the kept result; renderText adds the summary section
mcpserver/structured.go          # output and failureSummary fields and schema

internal/cli/mcpoutput.go        # the policy of a task; the failure facts of a run
internal/cli/mcp.go              # call(): buffers by policy, Output and Failure set

test/integration/agent_output_mcp_test.go   # the real binary over stdio

docs/runefile.md, docs/GRAMMAR.md, docs/diagnostics.md, docs/mcp.md
```

**Structure Decision**: the existing layout. The kept-stream and summary rules live in the
public `mcpserver` package, so the server and any embedder apply the same ones;
`internal/cli` only chooses the buffer and reports the facts of the run it executed.

## Complexity Tracking

No violation to justify.
