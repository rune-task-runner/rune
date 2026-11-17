# Implementation Plan: Bind Approval and Apply to the Exact Reviewed Plan

**Branch**: `051-mcp-plan-apply-binding` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/051-mcp-plan-apply-binding/spec.md`

## Summary

Close issue #49. Today an approved `apply` can run a plan nobody reviewed. This plan adds two
task attributes: `[plan(file=..., summary=...)]` on the task that makes a plan and
`[applies("<plan task>")]` on the task that applies it (R1, R2). A wrong declaration is the
static error `RUNE2020` (R3).

When the body of a plan task ends, the engine moves the declared file and its summary into a
store under `.rune/plans/`, identified by the file's SHA-256 digest, with a record of what the
plan was made for: arguments, working directory, Runefile definition, configuration, tool
identity and host (R5 to R9). A new package, `internal/planstore`, owns the store.

An apply call finds the newest plan of its plan task with equal arguments, refuses when it is
missing, used, expired, bound to something that changed, or does not verify (R10), and then
always asks the person at the client through the approval request of spec 049, which now shows
the plan's id, digest and summary. A grant never stands in for that (R11). After approval the
engine claims the plan with an exclusively created marker, copies the artifact to a private
directory while hashing it, and hands that copy to the apply body as `RUNE_PLAN_FILE`. When the
run ends it verifies the copy again, records the outcome and deletes the bytes (R12). An apply
task can only be called itself (R13). The command line runs the same checks and asks at the
terminal (R14). Every step writes a journal line without plan contents (R15).

OpenTofu and Terraform are supported with examples. Helm and migration runners are documented
as unsupported (R16). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `github.com/modelcontextprotocol/go-sdk` for the server
and the test client. `crypto/sha256`, `os`, `encoding/json` from the standard library. No new
modules.

**Storage**: Files under `<runefile dir>/.rune/plans/`: one directory per plan with a record,
the artifact, the summary and marker files, plus a JSON-lines journal
([contracts/plan-store.md](contracts/plan-store.md)).

**Testing**: `go test` in Docker only. Unit tests in `internal/parser`, `internal/ast`,
`internal/analyzer`, `internal/formatter`, `internal/language`, `internal/config`,
`internal/planstore`, `mcpserver` and `internal/cli`; the compatibility corpus; binary-level
tests in `test/integration` on stdio and Streamable HTTP with marker files; the docs harness.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. A call on a Runefile with neither attribute does no store access.
A plan capture and an apply each hash the artifact once per copy, streaming.

**Constraints**: A refused call runs nothing and sends no approval request (FR-012, FR-021).
Exactly one of any concurrent claims succeeds (FR-014). The apply body receives bytes verified
while they were copied (FR-015). No plan or summary byte reaches a record, the journal or a
refusal (FR-008, FR-029). A grant never approves a plan (FR-020). A Runefile without the
attributes gives the same tools and results byte for byte and creates no store (FR-005). New
prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package (`internal/planstore`, about 5 files with tests), 1 new engine
file (`internal/cli/planapply.go`), changes to about 14 existing Go files, 6 new test files,
corpus and formatter fixtures with goldens, a fuzz seed, the VS Code grammar, two examples and
the documents listed under Project Structure.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.4.0, and amended to v1.4.1 with this spec.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Nothing is skipped silently. An apply either runs or returns a result that says why not. The plan task always runs. The attributes are opt-in and cannot be combined with `[cache]`. |
| II. Errors are a feature | PASS | Every invalid declaration is `RUNE2020` with a span, before anything runs. Every runtime refusal names the plan, the reason and the next step. |
| III. Minimal, total DSL | PASS | The expression language is untouched. The additions are declarative attributes with string literals, total and statically analyzable, shipped with grammar documentation and fixtures. |
| IV. Hand-written front end, idiomatic Go | PASS, with one justified addition | The parser change reuses the hand-written named-argument loop. One new package, `internal/planstore`, is added. The principle asks for small, focused `internal/` packages, and a file store with its own invariants is one, as `internal/cache` is. See Complexity Tracking. |
| V. Boringly portable | PASS | Pure Go. Exclusive file creation and rename-free marker files behave the same on the three systems. The owner check is POSIX only, behind a build tag, and file modes are documented as not restricting access on Windows. The end-to-end stand-in is written in Rune's own shell. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer (R18), goldens generated with `-update`, binary-level tests with marker files on both transports, the changed pages and the examples through the docs harness. |
| VII. AI-native, secure by default | PASS, with a PATCH amendment | The principle says destructiveness is declared with `[confirm]`. An apply task is also advertised as destructive, and a grant does not run it. The sentence is amended to name `[applies]` (1.4.0 to 1.4.1), proposed with this spec as 1.4.0 was with spec 049. |
| VII, continued | PASS | The feature only adds refusals and one more thing shown to the person. A destructive task still runs only when granted or approved, and an apply task is stricter: only when approved. The summary and arguments are masked before they reach a request, a refusal, a record or the journal. No secret reaches a description or schema. |
| VIII. Go engineering discipline | PASS | No goroutine, no global, no `init()`. The two `git` calls carry a context with a timeout. Errors are typed, wrapped with `%w`, and matched with `errors.Is` and `errors.As`. `golangci-lint` stays clean. |
| Constraint: backward compatibility | PASS | `plan` and `applies` were `unknown attribute` before, so no valid Runefile changes meaning. The server library gains optional fields. |
| Constraint: surface changes carry their docs | PASS | `docs/GRAMMAR.md`, `docs/runefile.md`, `docs/diagnostics.md`, `docs/mcp.md` and the fixtures change in the same PR (R17). |

**Post-design re-check**: PASS. The contracts add two attributes, one diagnostic code, two
optional fields and one type on the server library, one package, and one engine file. No
principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/051-mcp-plan-apply-binding/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R18
├── data-model.md        # Phase 1: record, state, attempt, journal, order of checks
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── plan-attributes.md   # grammar, RUNE2020, body variables
│   ├── mcp-tools.md         # tool, request, refusals, command line texts
│   ├── mcpserver-api.md     # TaskInfo.AppliesPlan, ApprovalRequest.Plan
│   └── plan-store.md        # layout, API, guarantees, limits, journal
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/ast/
├── ast.go               # AttrPlan, AttrApplies; PlanSpec; Task.PlanSpec, Task.AppliesPlan;
│                        #   PlanExpiry
├── dump.go              # dumpAttr renders plan arguments
└── plan_binding_test.go # new

internal/parser/
├── attribute.go         # plan(...) through the named-argument loop; applies("...")
└── plan_binding_test.go # new

internal/diag/codes.go   # CodeInvalidPlanBinding = "RUNE2020"

internal/analyzer/
├── analyzer.go          # calls checkPlanBinding
├── planbinding.go       # new: every RUNE2020 case
└── plan_binding_test.go # new

internal/config/compose.go        # namespaces the name in [applies] (and its test)
internal/formatter/formatter.go   # formatAttr renders plan arguments (and fixtures)
internal/language/builtin.go      # two registry entries

internal/planstore/      # new package
├── store.go             # Open, Save, Latest, LastAttempt, Verify, Summary, Prune, states
├── attempt.go           # Claim, Finish, the private copy
├── journal.go           # Log, the size bound
├── safefile.go          # opening without following a link; the store directory checks
├── owner_unix.go, owner_other.go   # the owner check
└── *_test.go

mcpserver/
├── server.go            # TaskInfo.AppliesPlan; PlanApproval; toolFor paragraph and hint
├── handler.go           # an apply task always goes to CallApproved
├── approval.go          # the plan in the request; SummaryLines; the approver that cannot ask
├── authz.go             # plan refusals, never naming the grant flag
└── *_test.go

internal/cli/
├── planapply.go         # new: capture, binding digests, lookup, refusal texts, claim,
│                        #   finish, the command line question, git information
├── planapply_test.go    # new
├── mcp.go               # Tasks() fills AppliesPlan; call() runs the apply steps
├── run.go               # engine fields; execute handles an apply root; runBodyTo captures;
│                        #   taskEnv adds the plan variables; Execute's second check
├── serve.go             # a grant naming an apply task warns that it approves no plan; the
│                        #   configuration digest is taken at load
├── mcpreload.go         # the same warning on a reload
└── agentexec.go         # the agent endpoint's adapter carries the digests

testdata/corpus/plan-apply.rune, plan-apply.ast    # new, golden generated with -update
testdata/fmt/plan-apply.rune, plan-apply.fmt       # new, golden generated with -update
internal/parser/fuzz_test.go                       # one seed
editors/vscode/syntaxes/runefile.tmLanguage.json   # highlight the two attributes

test/integration/
├── plan_apply_mcp_test.go   # new: quickstart A to Y, AB, AC
└── plan_apply_cli_test.go   # new: quickstart Z

test/docs/examples_test.go   # prerequisites opentofu and terraform

docs/examples/plan-apply-opentofu/   # Runefile, main.tf, README.md
docs/examples/plan-apply-terraform/  # Runefile, main.tf, README.md
docs/mcp.md, docs/runefile.md, docs/GRAMMAR.md, docs/diagnostics.md, docs/examples/README.md,
SECURITY.md, README.md

.specify/memory/constitution.md   # Principle VII names [applies], version 1.4.1
```

**Structure Decision**: Existing layout plus one package. The attributes travel the path every
attribute does: `ast`, `parser`, `analyzer`, `formatter`, `language`. The store is its own
package so that its file-system invariants are tested without an engine. The engine code that
connects a run to the store lives in `internal/cli`, where the bound arguments, the settings
and the secret set are. The public `mcpserver` package only learns to always ask for an apply
task and to show a plan. No change to the scheduler.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| A new package under `internal/` (locked layout) | The store has its own invariants: exclusive claim, verified copy, modes, pruning, a bounded journal. They need tests that do not build an engine. | Putting it in `internal/cli` would add about 600 lines of file-system code to the engine package. Putting it in `internal/cache` would mix a clearable fingerprint cache with secret-bearing records under different rules. |
