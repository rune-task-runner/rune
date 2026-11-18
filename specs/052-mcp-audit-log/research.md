# Research: Record Agent Actions in a Durable Log and Show Them with `rune log`

Decisions for [spec.md](spec.md). Each has the decision, why, and what was set aside.

## R1. Capture in the server's receiving middleware

**Decision**: The record of a request is made in `Server.middleware`, the receiving middleware
the server already installs. For `tools/call` it writes the start, runs the rest of the chain
(the SDK's lookup and the task's handler), and writes the completion. For every other method it
runs the chain and writes one record.

**Rationale**: The SDK looks a tool up inside the method handler the middleware wraps, so a call
to a name no task has reaches the middleware before the SDK answers it. A hook inside
`Server.handler` would miss unknown tools, as the issue warns. The middleware sees every
transport, because all of them dispatch through the same `mcp.Server`.

**What it does not see**: a message the SDK rejects before dispatch: unreadable JSON, a method
the server does not have, parameters that do not decode, a request before `initialize`. The
spec puts these out of scope.

**Alternatives**: Wrapping the transport to see raw messages: it would see garbage too, at the
price of a second JSON-RPC decoder and of holding raw bodies, which the issue forbids storing.
A hook in the tool handler: misses unknown tools and removed tools.

## R2. One snapshot per call

**Decision**: The middleware loads the server's snapshot once, puts it in the call's context,
and `Server.handler` uses that snapshot when the context carries one.

**Rationale**: The record names the Runefile and configuration digests of the load the call ran
against (FR-007, User Story 8 scenario 7). If the middleware and the handler each loaded the
state, a reload between the two could record one load and run another.

## R3. Three layers

**Decision**:

- `mcpserver` owns what is observed. It defines the public `Auditor` interface, the event
  structs it hands to it, the optional `AuditEngine` extension an engine implements to give
  the project identity and a redactor, and the `CallAudit` handle an engine reaches through the
  context to add what only it knows (effective arguments, error kind, plan).
- `internal/audit` (new) owns the files: the record format, the directory, appending under a
  lock, retention, reading, pairing starts with completions, filtering. Standard library only.
- `internal/cli` joins them: an `auditor` type that implements `mcpserver.Auditor` on an
  `audit.Log`, the `AuditEngine` methods on `mcpAdapter`, the agent-run record in
  `executeAgent`, and the `rune log` command.

**Rationale**: `mcpserver` is public and embeddable, so it cannot expose `internal` types and
should not know a file format. The store has invariants (locking, torn tails, retention) that
need tests without a server, as `internal/planstore` has. The conversion between the two is one
small function.

**Alternatives**: The record type in `mcpserver` and the store importing it: the store would
depend on the MCP SDK. Everything in `internal/cli`: about 900 lines of file and protocol code
in the engine package.

## R4. Redaction happens before the Auditor sees anything

**Decision**: `mcpserver` hands an `Auditor` only redacted, bounded data. It decodes the
supplied arguments into a tree and walks it: every string, names included, goes through the
engine's `Redact`; the value under a name that `mask.SensitiveName` matches becomes the mask;
a string longer than 1 KiB is cut with `[N bytes cut]`; beyond 64 names the rest are counted.
Error texts go through `Redact` and are cut at 2 KiB. Effective arguments arrive from the
engine already masked, as `ApprovalRequest` values do, and get the same name rule and cut.

`mask.SensitiveName(name string) bool` is new: the built-in name patterns that `mask.NewSet`
already applies to environment variable names, exported for parameter names.

**Rationale**: An embedder's `Auditor` then cannot leak by forgetting a step. The secret set is
per load, so only the snapshot's engine can mask, which is why the redactor travels with the
engine and not with the Auditor.

**Alternatives**: Handing raw arguments and a redact function to the Auditor: one forgotten call
stores a secret. Storing only argument names: the issue asks for the values.

## R5. Classification is set where the decision is made

**Decision**: `CallAudit` carries the outcome parts. Each place that ends a call marks it:

| Where | Marks |
|---|---|
| Middleware, tool not in the snapshot | `unknown-tool`, kind `protocol` |
| `handler`: decode error, undeclared names, constraint violation | kind `validation` |
| `handler`: ungranted destructive task, nobody to ask | kind `policy` |
| `approver`: every end that is not an approval | kind `approval` with the decision |
| `mcpAdapter.call`: bind error | kind `validation` |
| `mcpAdapter.call`: `[agent-allow]` refusal, plan refusal, apply with nobody to ask | kind `policy` |
| `handler`: a `Result` came back | exit code; `completed` or `failed` |
| Middleware: the chain returned a protocol error for a known tool | kind `protocol` |
| Middleware: nothing marked and no result | kind `internal` |

The outcome follows from the marks: `validation` gives `invalid-arguments`; `policy` gives
`refused`; `approval` gives `refused`, or `timed-out` for an unanswered request, or `cancelled`
when the call ended; a context that was cancelled gives `cancelled` and one whose deadline
passed gives `timed-out`, whatever else was marked; otherwise `error`.

**Rationale**: The handler returns refusals as text today. Parsing text to classify would break
with the next wording change. Marks at the decision points are checked by the compiler.

## R6. Identity, git state and the redactor come from the engine

**Decision**: `mcpserver.AuditEngine` has two methods: `AuditIdentity(ctx)` returns the
Runefile path, the definition digest, the configuration digest and the git state; `Redact(s)`
masks. `mcpAdapter` implements both. The identity digests are computed once per load: the serve
loader always computes them now (it computed them only for a Runefile with plans), and the
agent endpoint's adapter computes them when it is built. The git state is read per call with a
new `gitState` function that tells four states apart:

| State | How it is found |
|---|---|
| `unavailable` | `git` could not be started, or timed out |
| `none` | `git rev-parse --is-inside-work-tree` fails |
| `unborn` | inside a work tree, `git rev-parse --verify HEAD` fails |
| `commit` | the commit id, and `git status --porcelain` for the dirty flag |

Each `git` call keeps the 3 second timeout and the `core.fsmonitor=false` guard of `gitInfo`.
A dirty flag that cannot be read is left out, not written as false.

**Rationale**: The existing `gitInfo` returns empty for every failure, which the issue forbids
("represent non-git and unborn repositories explicitly"). It stays as it is for the plan store.
The command-line run path does not compute the digests unless it opens an agent endpoint, so an
ordinary `rune build` pays nothing.

## R7. Storage layout

**Decision**:

```text
<base>/                       RUNE_AUDIT_DIR, else $XDG_STATE_HOME/rune/audit,
                              else %LocalAppData%\rune\audit on Windows,
                              else ~/.local/state/rune/audit            (0700)
└── <project id>/             first 16 hex digits of SHA-256 of the resolved Runefile path (0700)
    ├── project.json          {"runefile": "<path>"}, for a person browsing the directory (0600)
    ├── .lock                 exists only while a writer appends
    ├── 2026-11-17.jsonl      one file per UTC day (0600)
    └── 2026-11-18.jsonl
```

The Runefile path is resolved as the plan store resolves it (`runefilePath`), so one project
reached by two paths has one log.

The server refuses to start when the project directory, resolved, is inside the Runefile's
directory or the working directory, when the base or the project directory is a symbolic link,
or when the project directory belongs to another user (POSIX only, behind the build tags the
plan store uses).

**Rationale**: The issue asks for a place a task's normal writable filesystem does not reach.
`.rune/` beside the Runefile is exactly what a `clean` task removes. One rule for Linux and
macOS keeps the documentation short, and `XDG_STATE_HOME` is where logs and history belong.

**Alternatives**: `os.UserCacheDir`: a cache may be cleared at any time. `os.UserConfigDir`:
configuration directories are synced and backed up as settings. `.rune/audit/`: task-writable.

## R8. A day file holds the actions that started that day

**Decision**: A record is appended to the file of the UTC day its action started on. The
completion of a call goes to the same file as its start, also when the call ends after
midnight.

**Rationale**: Every day file is then complete by itself. A reader pairs starts and completions
within one file, and retention can remove a file without cutting an action in two.

## R9. Serialized appends

**Decision**: `audit.Log.Append` takes a process-wide mutex, then the lock file `.lock`,
created exclusively, as `planstore` locks its journal: wait up to 2 seconds, treat a lock older
than 10 seconds as left by a dead process and take it over with the move-aside step. Under the
lock it opens the day file for appending, checks the last byte, writes a line break first when
the file does not end with one, writes the record as one line with one `Write`, and for a call
or an agent run calls `Sync`. A lock that cannot be taken is a write failure.

**Rationale**: The issue asks for serialized writers across processes and for recovery from a
truncated final record. An exclusive lock file works the same on the three systems and needs no
new module. `Sync` on call records makes "the start is on disk before the task runs" true after
a machine crash too, at the cost of one flush per record.

**Alternatives**: `flock` and `LockFileEx`: two code paths behind build tags, or a new module.
Relying on `O_APPEND` alone: a short write on a full disk could interleave two records.

## R10. Retention

**Decision**: `audit.Log.Prune` removes files named `YYYY-MM-DD.jsonl` whose day is more than
the retention period before today (UTC). It runs when the log is opened and when an append
finds that the day changed. The period is 90 days, or `RUNE_AUDIT_RETENTION_DAYS`; `0` keeps
everything. Only names of that exact form are touched. A file that cannot be removed is a
warning, never a refusal: `Prune` returns the error to `Open`'s caller through
`audit.Options.Warn`, a function the `auditor` points at its warn-once writer, and `Append`
never fails because of it.

**Rationale**: Whole files only, so nothing is rewritten and no reader sees a file shrink.

## R11. Failure policy

**Decision**:

| What failed | Effect |
|---|---|
| Opening the log at server start (bad location, bad retention value) | The server does not start. Usage error |
| Start of a tool call | The call is refused with the audit error as its result. Nothing runs |
| Completion of a tool call | The result is the task's own plus one text block. Not retried |
| Any other record, or pruning | Served as before. One warning on standard error until a write succeeds |
| Agent-run start | The agent task runs without its endpoint, with a warning |

The `auditor` in `internal/cli` prints to the operator's standard error, because `mcpserver`
does not own it. The refusal text is the error the `Auditor` returns:

```text
audit log: this call was not run: the record of its start could not be written (<reason>).
The log directory is <dir>. The operator must make it writable or set RUNE_AUDIT_DIR.
```

The completion line is a second text content block, so the first block and the structured
content stay byte for byte the task's:

```text
[audit log] The completion of this call was not recorded (<reason>). The result above is the task's own. Do not retry the call because of this message.
```

**Rationale**: The issue's rules, one to one. A second content block keeps FR-038: the result's
own text, structured content and error flag are untouched.

## R12. Sessions

**Decision**: The session id in a record is the first 16 hex digits of SHA-256 of the server's
instance id (random, one per `Server`) and the protocol session id. A stdio session has no
protocol session id, so its id is derived from the instance id alone. The transport is `http`
when the request carries HTTP headers and `stdio` otherwise; the agent endpoint's auditor
replaces `http` with `agent-endpoint`. The client name and version are read from the session's
`InitializeParams`.

**Rationale**: The Streamable HTTP session id is a handle a client presents to resume a
session. Storing it would put a credential-like value in the log. A digest correlates equally
well.

## R13. Parents and `RUNE_INVOCATION_ID`

**Decision**: `mcpAdapter.call` adds `RUNE_INVOCATION_ID=<invocation id>` to the environment of
the tasks it runs, and gives the id to the engine for plan journal events
(`planstore.Event.Invocation`, new, `omitempty`). `executeAgent` writes an `agent-run` start
before it opens the endpoint and a completion when the agent ends. Its parent is the
`RUNE_INVOCATION_ID` the process inherited, if any. The endpoint's auditor sets the run's id as
the parent of every call.

**Rationale**: The agent endpoint is the one place where an agent's calls happen inside another
action. The variable carries the link across the process boundary of a body that runs `rune`.
It is attribution a body can overwrite, and the documentation says so.

**Note**: An `(agent)` task called through `rune mcp` opens no endpoint today (the adapter
leaves the engine's file unset to prevent recursion), so no parent chain exists there.

## R14. `rune log`

**Decision**: A Cobra subcommand `log` with its own `-f/--file`, in `cmd/rune/log.go`, calling
`cli.Log`. It resolves the Runefile as other commands do, computes the project directory, reads
the day files that can hold actions of the range (the UTC days from the range's start to its
end), pairs records into actions, filters, and prints.

- The range defaults to the display zone's today. `--date`, `--since`, `--until` as in the
  spec. Durations accept Go's units and `d` for days.
- `--tz` is loaded with `time.LoadLocation`; `local` is `time.Local`. `cmd/rune` imports
  `time/tzdata`, so a host without a zone database, such as a minimal container or Windows,
  still knows IANA names.
- Text output: a header line, then one line per action and an indented detail line for an
  error, an approval or a plan. Plain text, no color.
- `--json`: one object per action, `schemaVersion: 1`
  ([contracts/audit-record.md](contracts/audit-record.md)).
- To print the FR-036 note it parses the Runefile and looks for a task named `log`. A Runefile
  that does not parse does not stop the command.
- `log` has its own `-f`. Spec 048 pinned `--file` to the server alone
  (`TestOnlyServeHasFileFlag`); that test now names both commands, because the operator of
  `rune mcp -f <path>` needs `rune log -f <path>` to read the same project's log.

**Rationale**: `time/tzdata` adds about 450 KiB to the binary and removes a host dependency
(Constitution V). The command reads only, so it takes no lock: a line being written is either
complete or reported as the one damaged record, which the next read no longer sees.

## R15. Authentication failures

**Decision**: `tokenMiddleware` calls `Auditor.AuthFailed` with the time, the remote address
and the count of failures left out since the last record. A small limiter in the server allows
10 records per minute.

**Rationale**: FR-011. The request's headers and the asserted client name are never read.

## R16. Configuration reaches the engine through `cli.Options`

**Decision**: `cmd/rune` reads `RUNE_AUDIT_DIR` and `RUNE_AUDIT_RETENTION_DAYS` once and stores
them in `cli.Options.AuditDir` and `cli.Options.AuditRetention`. `ServeMCP`, `executeAgent` and
`Log` use them. With `AuditDir` empty, `audit.DefaultBase` reads `XDG_STATE_HOME`,
`LocalAppData` and the home directory.

**Rationale**: The engine package stays free of ambient environment reads, as it is for color
and version. Tests set `Options.AuditDir`, and the test mains of `internal/cli`,
`cmd/rune` and `test/integration` set `XDG_STATE_HOME` or `RUNE_AUDIT_DIR` to a temporary
directory so no test writes into a real home.

## R17. Documentation

**Decision**: A new section "The audit log" in `docs/mcp.md`, a `rune log` row and the two
variables in `docs/cli.md`, a troubleshooting entry for the audit error, the note about a task
named `log`, a paragraph in `SECURITY.md` on what a local log proves, the README feature list,
and `CHANGELOG`-relevant wording in the PR title. The docs harness checks the new blocks.

## R18. Tests

**Decision**: Test first at each layer.

- `internal/audit`: append and read, pairing, torn tail, damaged line, two logs on one
  directory writing at once, stale lock, retention, directory checks, range and filters.
- `internal/mask`: `SensitiveName`.
- `mcpserver`: an in-memory client session against a server with a recording `Auditor`: every
  outcome of R5, redaction and bounds, snapshot identity across a `Replace`, start failure
  refuses, completion failure keeps the result, authentication failures and the limiter,
  no Auditor means no change.
- `internal/cli`: the `auditor`, `gitState` in a commit, dirty, unborn and non-repository
  directory and without git, the adapter's marks, `executeAgent`'s run record, `Log` with every
  flag.
- `test/integration`: the built binary with an SDK client on stdio and on HTTP for the issue's
  acceptance criteria, a killed server, two processes writing at once, secret fixtures, marker
  files for refused calls, and `rune log` text and JSON.
