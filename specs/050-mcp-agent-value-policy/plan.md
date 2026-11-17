# Implementation Plan: Restrict the Argument Values an Agent May Pass to a Task

**Branch**: `050-mcp-agent-value-policy` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/050-mcp-agent-value-policy/spec.md`

## Summary

Close issue #48. Today an agent that may call a task may call it with every value the task
accepts. This plan adds a task attribute, `[agent-allow(name="value", ...)]`, that names string or
enum parameters and the one value an agent run may have for each (R1, R2). A wrong declaration is
the static error `RUNE2019` (R3).

The check runs in the engine adapter, `mcpAdapter.call`, after the arguments and defaults are bound
and before the approval request and the run (R4). Every engine that function builds is marked as an
agent run (R5). Before the run starts, the engine walks what the scheduler would run, resolving each
dependency and hook with the scheduler's own `ResolveDep`, and checks every restricted task against
the arguments it would get (R6). A restricted parameter whose value could differ between the walk
and the run, or an edge toward a restricted task that does not resolve, refuses the call (R7). The
same check runs again where a restricted task's body would start (R8). A refusal is an error result
with a masked text that names the refused values and the policy (R9).

A task body that runs `rune` is covered by a mark in the environment of every body of an agent
run, which the inner `rune` reads (R15). The walk reads on a fork of the call's scope, so it does
not fix a module variable's value before the run reads it (R7).

The tool an agent sees is narrowed: the property's `enum` holds only the allowed value, and the
description states the policy (R10). The command line and tasks without the attribute do not
change. Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `github.com/modelcontextprotocol/go-sdk` v1.6.1 for the
server and the test client. No new modules.

**Storage**: N/A. A policy is Runefile text. Nothing is stored between calls.

**Testing**: `go test` in Docker only. Unit tests in `internal/parser`, `internal/ast`,
`internal/analyzer`, `internal/formatter`, `internal/language`, `mcpserver` and `internal/cli`; the
compatibility corpus in `test/corpus`; binary-level tests in `test/integration` on stdio and
Streamable HTTP with marker files; the docs harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. A call on a Runefile with no `[agent-allow]` does one memoized
name walk per called task and nothing else. A call that reaches a restricted task evaluates the
dependency arguments on the way to it once more than the run does.

**Constraints**: A refused call runs no task body, dependency or hook and sends no approval request
(FR-019). No refused value is replaced (FR-010). A grant or an approval never allows a refused
value (FR-021). Refusals hold no secret (FR-025). A Runefile without the attribute gives the same
tools and results byte for byte (FR-011, FR-029). The destructive refusal keeps its place and text
(FR-022). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new Go source file (`internal/cli/agentpolicy.go`), changes to 11
(`internal/ast/ast.go`, `internal/ast/dump.go`, `internal/parser/attribute.go`,
`internal/diag/codes.go`, `internal/analyzer/analyzer.go`, `internal/formatter/formatter.go`,
`internal/language/builtin.go`, `internal/eval/eval.go`, `mcpserver/server.go`,
`internal/cli/mcp.go`, `internal/cli/run.go`), 5 new test files plus additions to existing ones,
one corpus fixture and one formatter fixture with their goldens, a fuzz seed, the VS Code grammar,
and the documents listed under Project Structure.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.4.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | A call either runs or returns a result that says why it did not. Nothing is skipped silently and no value is substituted. |
| II. Errors are a feature | PASS | Every invalid declaration is `RUNE2019` with a span at the name, the value or the attribute. The analyzer runs before any execution, so a Runefile with one runs nothing and is not served. The call-time refusal names the task, the values and the policy. |
| III. Minimal, total DSL | PASS | The expression language is untouched. The addition is declarative surface, which the principle lets grow through the spec process: an attribute with literal arguments, total and statically analyzable, shipped with grammar documentation and fixtures. The plan relies on this principle: resolving defaults and dependency arguments before a run executes no command (R6). |
| IV. Hand-written front end, idiomatic Go | PASS | One hand-written parse function beside `parseCacheArgs`. Changes stay in the existing `internal/` packages and the public `mcpserver` package. No new package. |
| V. Boringly portable | PASS | No OS-specific code. Availability checks use the adapter's host OS value, as the scheduler does. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer (R13): parser, analyzer, corpus golden generated with `-update`, formatter, server library, engine, and binary-level marker-file tests on both transports. The changed doc pages run through the docs harness. |
| VII. AI-native, secure by default | PASS | The feature only adds refusals on the agent surface. Destructive tasks are still refused unless granted or approved, and neither lifts a refused value. Policy values are Runefile literals, like enum values, so no environment value reaches a description or schema. Refusal texts are masked with the secret set of task output. |
| VIII. Go engineering discipline | PASS | No goroutine, no global, no `init()`. The walk is plain recursion over a graph the analyzer has already proved acyclic, with a visited set as a guard. Errors are returned and wrapped. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | `agent-allow` was `unknown attribute` (`RUNE2007`) before, so no valid Runefile changes meaning. A Runefile without it behaves as before for every caller. The server library gains one optional field. |
| Constraint: surface changes carry their docs | PASS | `docs/GRAMMAR.md`, `docs/runefile.md`, `docs/diagnostics.md`, `docs/mcp.md` and the corpus and formatter fixtures change in the same PR (R14). |

No violation. Complexity Tracking is empty.

**Post-design re-check**: PASS. The contracts add one attribute, one diagnostic code, one field on
the library's parameter descriptor, one field on the per-call engine and one file of engine code.
No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/050-mcp-agent-value-policy/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R17
├── data-model.md        # Phase 1: predicate, policy, walk, denial, order of checks
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── agent-allow-attribute.md  # grammar, formatting, RUNE2019
│   ├── mcp-tools.md              # tools/list and tools/call results and texts
│   └── mcpserver-api.md          # ParamInfo.AgentValues for embedders
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/ast/
├── ast.go               # AttrAgentAllow in KnownAttributes; AllowPredicate; Attribute.Allow;
│                        #   Task.AgentPolicy
├── dump.go              # dumpAttr renders the predicates
└── agent_allow_test.go  # new: AgentPolicy, dump

internal/parser/
├── attribute.go         # parseAgentAllowArgs
└── agent_allow_test.go  # new: predicates, spans, syntax errors

internal/diag/codes.go   # CodeInvalidAgentAllow = "RUNE2019"

internal/analyzer/
├── analyzer.go          # checkAgentAllow
└── agent_allow_test.go  # new: every RUNE2019 text and span

internal/formatter/formatter.go   # formatAttr renders the predicates (and its test and fixtures)
internal/language/builtin.go      # registry entry

mcpserver/
├── server.go            # ParamInfo.AgentValues; inputSchema narrows; toolFor adds the paragraph
└── server_test.go       # schema, description, unchanged tool

internal/cli/
├── agentpolicy.go       # new: policy check, the walk, volatility, denial texts
├── agentpolicy_test.go  # new
├── mcp.go               # Tasks() fills AgentValues; call() runs the walk and sets engine.agent
└── run.go               # engine.agent and engine.mask; execute reads RUNE_AGENT_RUN and runs
                         #   the walk; ResolveDep takes its scope from a helper; Execute and
                         #   ExecuteFailHook run the second check

internal/eval/eval.go    # Scope.Fork: the same bindings with its own variable cache

testdata/corpus/
├── agent-allow.rune     # new
└── agent-allow.ast      # new, generated with -update

test/integration/
└── agent_policy_mcp_test.go   # new: quickstart rows, stdio and HTTP, marker files

editors/vscode/syntaxes/runefile.tmLanguage.json   # highlight [agent-allow]

testdata/fmt/agent-allow.rune, agent-allow.fmt     # new, golden generated with -update
internal/parser/fuzz_test.go                       # one seed

docs/runefile.md, docs/GRAMMAR.md, docs/diagnostics.md, docs/mcp.md, SECURITY.md, README.md
```

**Structure Decision**: Existing layout. The attribute travels the path every attribute does:
`ast`, `parser`, `analyzer`, `formatter`, `language`. The check lives in `internal/cli`, where the
bound arguments, the scheduler's `ResolveDep` and the secret set are. The public `mcpserver` package
only learns how to advertise a restricted parameter. `internal/eval` gains one method. No new
package and no change to the scheduler.

## Complexity Tracking

No violations.
