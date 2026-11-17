# Implementation Plan: Approve a Destructive Call Inside the Agent Client

**Branch**: `049-mcp-confirm-elicitation` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/049-mcp-confirm-elicitation/spec.md`

## Summary

Close issue #47. Today the tool handler in `mcpserver/handler.go` refuses a call that can run a
`[confirm]` task the operator has not granted, before it looks at the arguments. This plan adds a
second way through: when the server is told to ask (`Options.AskApproval`, set by `rune mcp` and
`rune serve`) and the calling session stated form elicitation, the handler validates the arguments
and hands the call to the engine through a new optional interface, `ApprovalEngine`. The engine binds
the parameters, describes the call, and calls back into the server, which sends one
`elicitation/create` form request on the calling session and waits up to five minutes (R1 to R5).

Only an `accept` whose `approved` field is the boolean `true` counts (R4). The approved task names
then live in a field of the engine built for that one call, where `confirmFailure` reads them beside
the operator's grants, so the approval reaches the engine's own gate and is gone when the call
returns (R6). Nothing is stored between calls, which is what makes a repeated call a new call (R7).
A request is tied to the snapshot it was built from: if `Replace` stored a new one while the person
was deciding, the call is not run (R8).

A client that stated no form support gets today's refusal at today's place, with two sentences
added: the command to run and why Rune could not ask (R9). The agent task endpoint and embedders
that do not opt in are unchanged, byte for byte (R10). Constitution Principle VII is amended to
name per-call approval (R14). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `github.com/modelcontextprotocol/go-sdk` v1.6.1 provides
`ServerSession.Elicit`, `ServerSession.InitializeParams` and `ClientOptions.ElicitationHandler`. No
new modules.

**Storage**: N/A. An approval is never stored.

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` and `internal/cli`; binary-level
tests in `test/integration` on stdio and Streamable HTTP with an SDK client that has an elicitation
handler; recorded client sessions in `test/integration/testdata/clients`; the docs harness in
`test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. A call that needs no approval does one more slice-length check. A call
that needs one waits for a person.

**Constraints**: No task body, hook or dependency runs before an approval (FR-001). Anything but
`accept` with `approved: true` runs nothing (FR-016). No approval outlives its call (FR-012). The
request holds no secret and no raw control character, shows every argument whole, and is at most
8 KiB (FR-010, FR-011). Servers that do
not ask keep their texts byte for byte (FR-022, FR-023). New prose avoids the em dash (maintainer
rule).

**Scale/Scope**: 1 new Go source file (`mcpserver/approval.go`), changes to 7
(`mcpserver/server.go`, `mcpserver/authz.go`, `mcpserver/handler.go`, `internal/cli/mcp.go`,
`internal/cli/confirm.go`, `internal/cli/run.go`, `internal/cli/serve.go`), 3 new test files, text
changes in the existing tests that pin the `rune mcp` refusal, new client recordings, and the
documents listed under Project Structure.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0, and v1.4.0 as this PR amends it (R14).*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | A call either runs or returns a result that says why it did not. Nothing is skipped silently. |
| II. Errors are a feature | PASS | Each non-approval has its own text naming the task and the reason. The fallback names the command to run. These are run-time policy results, as the existing refusal is. |
| III. Minimal, total DSL | PASS | No DSL change. The plan relies on this principle: resolving parameter defaults before asking runs no command (R6). |
| IV. Hand-written front end, idiomatic Go | PASS | Changes stay in `internal/cli` and the public `mcpserver` package. No new package. |
| V. Boringly portable | PASS | No OS-specific code. The request travels over the transports Rune already has. |
| VI. Test-first, multi-layer verification | PASS | Each layer gets failing tests first: message and outcome units in `mcpserver`, the engine gate in `internal/cli`, marker files at binary level on both transports, recordings, and the docs harness (R12). |
| VII. AI-native, secure by default | PASS with the amendment of R14 | As written in v1.3.0, "Destructive tasks are refused on the agent surface by default" stops being true for a client that can be asked: the call is put to the person. This PR amends the sentence (v1.4.0, R14) to name the two ways a destructive task can run, the operator's grant and the person's approval of one call, and to keep refusal for everything else. The amendment takes effect when the maintainer merges it. Rune trusts the client to carry the answer, which the guide states (R11). The request masks secrets with the same set as task output. The remote endpoint is unchanged. |
| VIII. Go engineering discipline | PASS | The one new external call, `ServerSession.Elicit`, carries the call's context and a timeout (R5). No new goroutine: the handler's own goroutine waits. Errors are wrapped or compared with `errors.Is`. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | No DSL change. On `rune mcp` and `rune serve` a call that was always refused may now be approved by a person, and the refusal text for other clients grows. Ships pre-1.0 as a `feat(mcp)!` commit whose subject says so (R13). |
| Constraint: surface changes carry their docs | PASS | No DSL surface change. The MCP guide, the compatibility table and the other pages that describe the refusal change in the same PR (R11). |

No unjustified violation. Principle VII is amended, not deviated from, so Complexity Tracking is
empty.

**Post-design re-check**: PASS. The contracts add three option fields, one optional interface and
three small types to the library, and one field to the per-call engine. No principle verdict
changes.

## Project Structure

### Documentation (this feature)

```text
specs/049-mcp-confirm-elicitation/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R15
├── data-model.md        # Phase 1: decision table, request, outcomes, lifetimes
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── approval-request.md  # the elicitation request and how each answer is read
│   ├── mcp-tools.md         # tools/call results and texts
│   └── mcpserver-api.md     # Options, ApprovalEngine and types for embedders
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
mcpserver/
├── approval.go          # new: formSupport, approvalMessage, approvalSchema, readAnswer,
│                        #   Server.approver, Server.cannotAsk, escaping and cutting helpers
├── server.go            # Options.AskApproval, ApprovalTimeout, RunCommand; ApprovalEngine,
│                        #   ApprovalRequest, ApprovalArg, Confirmation
├── authz.go             # ungranted(TaskInfo) split out of refusal; refusal text unchanged
├── handler.go           # the ask path: capability check, arguments first, CallApproved
├── approval_test.go     # new: units for every function above; handler cases over the
│                        #   in-memory transport with a fake ApprovalEngine
├── authz_test.go        # ungranted order; refusal texts unchanged without AskApproval
└── transport_test.go    # the ask path over Streamable HTTP; two sessions

internal/cli/
├── mcp.go               # mcpAdapter.CallApproved; approvalRequest; call takes an approval
├── run.go               # engine.approved
├── confirm.go           # confirmFailure reads engine.approved
├── serve.go             # ServeMCP sets AskApproval and RunCommand
├── mcp_approval_test.go # new: the gate runs a [confirm] body with no stdin; unnamed tasks
│                        #   still declined; defaults resolved; secrets masked
├── confirm_test.go      # confirmFailure with approved
└── mcp_test.go          # the agent endpoint still sets no AskApproval

test/integration/
├── approval_mcp_test.go            # new: quickstart scenarios, stdio and HTTP, marker files
├── clients_fixture_test.go         # cases for the new recordings and the stated capabilities
├── testdata/clients/               # new recordings and replies; README rows
├── destructive_mcp_test.go         # refusal text for a client that cannot ask
├── dependency_refusal_mcp_test.go  # same
└── (other files that pin that text) # same, through one helper

docs/mcp.md                          # security model, new section, compatibility table
docs/runefile.md, docs/use-cases/mcp-agents.md, docs/user-guide/README.md,
docs/examples/agent-driven/README.md, docs/examples/agent-driven/Runefile,
docs/design/rune-only-agent-mode.md, SECURITY.md, README.md   # sentences that say "refused unless granted"
specs/031-mcp-destructive-optin/contracts/mcp-tools.md        # the request and the fallback
cmd/rune/serve.go                    # help text that describes the refusal
.specify/memory/constitution.md      # Principle VII, v1.4.0 (R14)
```

**Structure Decision**: Existing layout. The request is built and sent in the public `mcpserver`
package, where the session, the grants and the snapshot are, so an embedder gets it by setting one
option and implementing one interface. The CLI adapter binds and describes the call and carries the
approval into the engine it builds for that call.

## Complexity Tracking

No constitution violations to justify.
