# Implementation Plan: Per-Task Opt-In for Destructive Tasks over MCP

**Branch**: `031-mcp-destructive-optin` | **Date**: 2026-11-04 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/031-mcp-destructive-optin/spec.md`

## Summary

Close issue #31. Over MCP, every `[confirm]` task is refused, and the
"explicit, per-task opt-in" the MCP guide promises does not exist. Two gates
block it: the tool handler's server-wide `AllowDestructive` switch, which no
flag sets, and the engine's confirmation step, which declines because an MCP
call has no stdin.

The plan adds `rune mcp --allow-destructive TASK[,TASK...]`, repeatable, bound
like `--ignore-version` (R1). `cli.ServeMCP` validates the names against the
statically loaded Runefile before any Runefile code runs: an empty or unknown
name is exit 2, with a suggestion for a near miss; a task without `[confirm]`
gets a warning (R2, R6). The validated names open both gates for those tasks
only. The handler refuses any other destructive call with
`task "X" is destructive; to permit it, add X to --allow-destructive and restart the server`
(R5). The engine approves the confirmation of a granted task wherever the run
reaches it, so dependencies, post-hooks, and failure hooks follow the same
per-task rule (R3). The agent-executor endpoint and the CLI path never receive
a grant.

The public `mcpserver.Options.AllowDestructive` changes from `bool` to
`[]string`, so no approve-all switch remains and existing embedders get a
compile error at the exact line (R4). Docs that promise a nonexistent opt-in or
point MCP users at `--yes` are corrected (R8). Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0`
builds

**Primary Dependencies**: existing only (`github.com/spf13/cobra` and `pflag`
for the flag, `github.com/modelcontextprotocol/go-sdk` v1.6.1 for MCP and the
HTTP test client). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `mcpserver` and
`internal/cli`; flag wiring in `cmd/rune`; binary-level stdio tests in
`test/integration`; docs harness in `test/docs` (R7).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. One map lookup per `[confirm]` gate and per tool
call.

**Constraints**: Usage errors run no Runefile code, including the `[context]`
hook (FR-010). With no grant, the startup line and `tools/list` are
byte-identical to today (FR-012, FR-014). The CLI confirmation path is unchanged
(spec 027). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 10 Go source files changed (`cmd/rune/serve.go`;
`internal/cli/dispatch.go`, `serve.go`, `mcp.go`, `run.go`, `confirm.go`,
`agentexec.go`; `mcpserver/server.go`, `authz.go`, `handler.go`), 1 new test
file each in `internal/cli` and `test/integration`, updates to 8 existing test
files, and 5 doc pages. No new packages.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.2.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | No skipping logic. A granted task runs every time it is called. |
| II. Errors are a feature | ✅ PASS | Bad names fail at startup with exit 2, a suggestion, and zero side effects. The refusal names the exact flag that fixes it. |
| III. Minimal, total DSL | ✅ PASS | No Runefile syntax, attribute, or built-in changes. `[confirm]` keeps its meaning. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | No new packages. `mcpserver` stays public; one field changes type (R4). |
| V. Boringly portable | ✅ PASS | No OS-specific code. Test bodies write marker files with shell redirection, which the built-in shell runs the same on all three OSes. |
| VI. Test-first, multi-layer verification | ✅ PASS | Library, engine, flag, binary, and docs layers (R7). No golden or corpus changes. |
| VII. AI-native, secure by default | ✅ PASS (deviation recorded) | Refusal stays the default. Only the operator, at server start, can grant; nothing an agent sends can. Each task is named individually and there is no wildcard. The agent-executor endpoint stays closed. HTTP stays token-gated. The HTTP endpoint is localhost-bound by default but accepts an explicit non-loopback `--addr`; that predates this feature, see Complexity Tracking. |
| VIII. Go engineering discipline | ✅ PASS | No goroutines or globals; the grant is a field set once at construction. Errors use the existing `UsageError` type. |

**Engineering constraints**: Docker-only testing respected. Package layout
unchanged. Backward compatibility: no DSL change; the CLI gains a flag and
removes nothing. The library change is breaking for embedders that set
`AllowDestructive`, which is acceptable before 1.0 with the release note in R9.
"Surface changes carry their docs": the flag ships with `docs/mcp.md`,
`docs/cli.md`, and help text in the same PR.

**Post-design re-check (after Phase 1)**: one deviation, recorded in Complexity
Tracking (Principle VII, non-loopback HTTP bind). Design changed the spec
in these places:

1. FR-017: the docs inventory found three more pages that send MCP users to
   `--yes` or call the gate absolute (`docs/examples/agent-driven/README.md`,
   `docs/use-cases/mcp-agents.md`, the `[confirm]` paragraph of
   `docs/runefile.md`). FR-017 now names them (R8).
2. FR-004: the `AllowList` refusal gets its own text so it no longer says
   "requires explicit approval" (R5). The allowlist stays unexposed (#39).

Complexity Tracking has one row: the non-loopback HTTP bind (Principle VII).

## Project Structure

### Documentation (this feature)

```text
specs/031-mcp-destructive-optin/
├── plan.md                    # This file
├── spec.md                    # Feature spec
├── research.md                # Phase 0: baseline and decisions R1-R9
├── data-model.md              # Phase 1: grant, confirmation gate, refusal, outcomes by path
├── quickstart.md              # Phase 1: automated gates and scenarios A-M
├── contracts/
│   ├── cli-serve.md           # flag forms, errors, warning, startup line, help
│   ├── mcp-tools.md           # tools/list and tools/call as a client sees them
│   └── mcpserver-api.md       # Options.AllowDestructive bool -> []string, migration
├── checklists/requirements.md
└── tasks.md                   # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
cmd/rune/
├── serve.go                   # --allow-destructive flag, Long text, Example
└── serve_test.go              # flag registered on serve/mcp; --yes still unknown

internal/cli/
├── dispatch.go                # Options.MCPAllowDestructive []string
├── serve.go                   # parse, validate, warn; pass grant to server and adapter; startup line
├── serve_test.go              # NEW: parsing, unknown/empty/*, suggestion, warning, startup line, no [context] run
├── mcp.go                     # mcpAdapter.grants; CallLists copies it into the engine
├── run.go                     # engine.grants field
├── confirm.go                 # confirmFailure approves granted task names
├── confirm_test.go            # gate rows 1-5 incl. grant vs dependency, post-hook, failure hook, private
├── mcp_test.go                # adapter with grant; agent adapter has none
└── agentexec.go               # Options literal drops `AllowDestructive: false` (no grant; it no longer compiles)

mcpserver/
├── server.go                  # Options.AllowDestructive []string; set built in New
├── authz.go                   # refusal(name) string replaces authorized(name) bool
├── handler.go                 # returns the refusal text
├── authz_test.go              # refusal order and texts
├── handler_test.go            # granted call reaches engine; tools/list unchanged
├── server_test.go             # Options literals updated
└── transport_test.go          # granted and ungranted calls over StartHTTP with a token

test/integration/
├── secret_masking_mcp_test.go # mcpCallArgs gains a server-args variant
└── destructive_mcp_test.go    # NEW: stories 1-3 over stdio with marker files

docs/
├── mcp.md                     # Starting the server, Security model, Agent task bodies
├── cli.md                     # rune serve row
├── runefile.md                # [confirm] paragraph
├── examples/agent-driven/README.md  # drop "use --yes" for MCP
└── use-cases/mcp-agents.md          # same callout
```

**Structure Decision**: Existing layout. The grant is parsed in `internal/cli`
next to the other operator flag, enforced at the two existing gates
(`mcpserver` handler, `internal/cli` confirmation), and nothing moves between
packages.

## Delivery

One PR from `031-mcp-destructive-optin`, branched from `main`, titled
`fix(mcp)!: breaking: rune mcp --allow-destructive TASK lets agents run named [confirm] tasks; mcpserver.Options.AllowDestructive is now a task list`,
closing #31 (R9). No Co-Authored-By trailer and no tool attribution. Wait for
every CI check to finish green before reporting the PR as done.

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Principle VII says a remote MCP endpoint is localhost-bound. `rune serve --http --addr :7777` already binds every interface (`mcpserver/transport.go`), and a grant applies there too (spec clarification 3). | Existing behavior, not introduced here. The token is still required on every request. #43 tracks a warning for non-loopback binds. | Refusing a grant on a non-loopback bind would make the grant depend on the transport, which clarification 3 rejected. Changing the bind rule itself is #43. |
