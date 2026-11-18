# Implementation Plan: Record Agent Actions in a Durable Log and Show Them with `rune log`

**Branch**: `052-mcp-audit-log` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/052-mcp-audit-log/spec.md`

## Summary

Close issue #50. Today nothing durable says what an agent asked Rune to do. This plan makes
every Rune MCP server write an audit log and adds `rune log` to read it.

The server's receiving middleware is the capture point (R1). For a tool call it writes a start
record before the SDK looks the tool up, runs the call against the snapshot it loaded (R2), and
writes a completion with the outcome, the exit code when a run produced one, the arguments the
task ran with, the approval decision and the plan (R5). Other requests and authentication
failures get one record each (R15). Everything handed to the recorder is redacted and bounded
first (R4).

A new package, `internal/audit`, owns the files: versioned JSON lines, one file per UTC day in
a per-project directory under the user's state directory, appended under a cross-process lock,
with torn-tail recovery and 90-day retention (R7 to R10). `mcpserver` gains an optional
`Auditor` interface and an engine extension for identity and masking (R3, R6). `internal/cli`
joins the two, records the run of an `(agent)` task as the parent of the calls made through its
endpoint (R13), and implements `rune log` with day, range, timezone, task, outcome, client and
session filters and a stable JSON output (R14).

A call whose start cannot be written is refused. A completion that cannot be written is
reported and never changes the result (R11). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `github.com/modelcontextprotocol/go-sdk` for the server
and the test client, `github.com/spf13/cobra` for the command. `crypto/sha256`, `crypto/rand`,
`encoding/json`, `os`, `time` and `time/tzdata` from the standard library. No new modules.

**Storage**: Files under `<state dir>/rune/audit/<project id>/`: one JSON-lines file per UTC
day, a lock file while a writer appends, and `project.json`
([contracts/audit-record.md](contracts/audit-record.md)).

**Testing**: `go test` in Docker only. Unit tests in `internal/audit`, `internal/mask`,
`mcpserver`, `internal/cli` and `cmd/rune`; binary-level tests in `test/integration` on stdio
and Streamable HTTP with marker files; the docs harness.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: Recording adds under 50 ms to a tool call on a local disk (SC-008): two
appends with a flush and three short `git` calls. `rune log` prints a day of 10,000 actions in
under 2 seconds (SC-007). A server built without an `Auditor` does no extra work.

**Constraints**: The start of a call is on disk before anything of it runs (FR-002, FR-014). A
refusal has no exit code (FR-006). No secret, token, header, output or plan byte reaches a
record (FR-018 to FR-020). A record is at most 64 KiB (FR-021). Tool lists, results and refusal
texts are unchanged except for the two audit messages (FR-038). An embedder without a log sees
no change (FR-039). The log is never inside the project (FR-024). New prose avoids the em dash
(maintainer rule).

**Scale/Scope**: 1 new package (`internal/audit`, about 6 files with tests), 2 new files in
`mcpserver`, 3 new files in `internal/cli`, 1 new file in `cmd/rune`, changes to about 12
existing Go files, 2 new integration test files, and the documents listed under Project
Structure.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.4.1. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Nothing is skipped or cached. A call either runs or returns a result that says why not. `rune log` runs no task. |
| II. Errors are a feature | PASS | No DSL change, so no new static diagnostic. Every runtime failure of the log names the directory, the reason and the variable that fixes it. Usage errors of `rune log` name the accepted values. |
| III. Minimal, total DSL | PASS | The Runefile language is untouched. Configuration is the server's environment, not a setting. |
| IV. Hand-written front end, idiomatic Go | PASS, with one justified addition | No parser change. One new package, `internal/audit`, is added: a file store with its own invariants, as `internal/planstore` and `internal/cache` are. See Complexity Tracking. `mcpserver` stays public and gains only optional API. |
| V. Boringly portable | PASS | Pure Go. The lock is an exclusively created file, as in `internal/planstore`. The owner check is POSIX only, behind build tags. `time/tzdata` is embedded so `--tz` works on a host without a zone database. Paths in the docs use forward slashes, with the Windows default named once. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer (R18): the store, the server with a recording auditor, the adapter, the command, and the built binary on both transports with marker files. The changed pages go through the docs harness. |
| VII. AI-native, secure by default | PASS | The feature only adds a record and one refusal (a call whose start cannot be recorded). Authorization is untouched. Secrets are masked before a value leaves the server, secret-named parameters are never stored, and tokens, headers and output never reach a record. The log is outside the project, owner-only, and its limits are documented. No client-reported name is presented as an identity. |
| VIII. Go engineering discipline | PASS | No goroutine, no global, no `init()`. The `git` calls carry a context with a timeout. Errors are typed where a caller branches on them, wrapped with `%w`, matched with `errors.Is` and `errors.As`. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. Write failures are made with a directory in place of a file, because the container runs as root. |
| Constraint: backward compatibility | PASS, with a note | No DSL change. `log` becomes a built-in command name: a Runefile task named `log` is now run with `rune -- log`, as the CLI reference already says for every built-in. `rune log` prints a note when such a task exists, and the release notes call it out, so the change is not silent. |
| Constraint: surface changes carry their docs | PASS | No DSL surface changes. The CLI and MCP pages change in the same PR (R17). |

**Post-design re-check**: PASS. The contracts add one command, two environment variables for
servers, one variable for tasks, one optional interface and its types on the server library,
one exported function in `internal/mask`, one optional field on the plan journal's event, and
one package. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/052-mcp-audit-log/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R18
├── data-model.md        # Phase 1: record, action, outcome, lifecycle of a call
├── quickstart.md        # Phase 1: validation scenarios A to T
├── contracts/
│   ├── audit-record.md      # files, record fields, the JSON object, stability
│   ├── rune-log.md          # the command, flags, outputs, exit codes, every message
│   └── mcpserver-api.md     # Options.Audit, Auditor, AuditEngine, CallAudit
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/audit/              # new package, standard library only
├── record.go                # Record and its parts; version; encode with size bound
├── log.go                   # Open (directory checks), Append, the lock, torn-tail repair, Prune
├── dir.go                   # DefaultBase, ProjectDir, ProjectID, inside-project check
├── read.go                  # Read: day files of a range, pairing into Actions, damage count
├── filter.go                # Filter: range, task, outcome, client, session, kinds
├── owner_unix.go, owner_other.go   # the owner check
└── *_test.go

internal/mask/set.go         # SensitiveName, exported from the existing name patterns

internal/planstore/journal.go   # Event.Invocation

mcpserver/
├── audit.go                 # new: Auditor, the event types, AuditEngine, CallAudit,
│                            #   AuditFromContext, redaction and bounds, session id, limiter
├── audit_test.go            # new
├── server.go                # Options.Audit; the instance id
├── reload.go                # middleware: audited tools/call and requests; snapshot in context
├── handler.go               # uses the context's snapshot; marks kinds, grants and the result
├── approval.go              # the approver records the decision
├── transport.go             # tokenMiddleware reports failures
└── *_test.go

internal/cli/
├── audit.go                 # new: auditor (mcpserver.Auditor on audit.Log), record conversion,
│                            #   opening the log for a project, the warn-once writer, gitState
├── audit_test.go            # new
├── log.go                   # new: Log, flag parsing, the range, text and JSON rendering
├── log_test.go              # new
├── dispatch.go              # Options.AuditDir, Options.AuditRetention
├── mcp.go                   # AuditIdentity and Redact; call() sets effective arguments, kinds,
│                            #   the plan, RUNE_INVOCATION_ID
├── serve.go                 # the identity is computed for every load; ServeMCP opens the log
│                            #   and passes the auditor
├── planapply.go             # journal events carry the invocation id; the plan reaches the record
├── run.go                   # engine.invocation from RUNE_INVOCATION_ID
├── agentexec.go             # the agent-run record; the endpoint's auditor with its parent
└── leak_test.go             # sets XDG_STATE_HOME to a temporary directory

cmd/rune/
├── log.go                   # new: the log command
├── log_test.go              # new
├── main.go                  # registers it; imports time/tzdata
└── root.go                  # reads RUNE_AUDIT_DIR and RUNE_AUDIT_RETENTION_DAYS into Options

test/integration/
├── harness_test.go          # RUNE_AUDIT_DIR for every binary run
├── audit_log_mcp_test.go    # new: quickstart A to Q, S, T
└── audit_log_cli_test.go    # new: quickstart R

docs/mcp.md, docs/cli.md, docs/troubleshooting.md, SECURITY.md, README.md, CONTRIBUTING.md
```

**Structure Decision**: Existing layout plus one package. The store is its own package so that
its file-system invariants are tested without a server, as the plan store is. `mcpserver` owns
what is observed and stays free of the file format. `internal/cli` is where the secret set, the
bound arguments, the plan ticket and the operator's standard error are, so the glue lives
there. No change to the scheduler, the parser or the analyzer.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| A new package under `internal/` (locked layout) | The log has its own invariants: serialized appends across processes, torn-tail repair, whole-file retention, pairing and filtering. They need tests that build no server. | Putting it in `internal/cli` would add about 700 lines of file code to the engine package. Putting it in `internal/planstore` would mix a per-project plan store under `.rune/` with a log that must live outside the project. Putting it in `mcpserver` would make the public package own a file format. |
