---

description: "Task list for spec 052: record agent actions in a durable log and show them with rune log"
---

# Tasks: Record Agent Actions in a Durable Log and Show Them with `rune log`

**Input**: Design documents from `/specs/052-mcp-audit-log/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and FR-041 ask for them. A test is written and
seen failing before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US9)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
- "Nothing ran" is always checked from the outside: no marker file.
- The exact texts and field names are in [contracts/rune-log.md](contracts/rune-log.md) and
  [contracts/audit-record.md](contracts/audit-record.md). Tests compare whole strings unless
  the task says otherwise.
- No test writes into a real home directory: every test that opens a log uses a
  `t.TempDir()`, and the test mains set the state directory to a temporary one.
- The tests run as root in Docker, so a write failure is made by putting a directory where the
  day file belongs, never by a read-only mode alone.
- A server built without `Options.Audit` must give the same tools and results as before, byte
  for byte. No existing test is edited to make it pass.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `052-mcp-audit-log` and `specs/052-mcp-audit-log/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The store package exists (R7 to R10), the server library can report what it
handles to an `Auditor` (R1 to R5, R12), and `internal/cli` joins the two for `rune mcp`
(R3, R6, R11, R16). After this phase a served call leaves a start and an end record.

### Tests first

- [X] T002 [P] In `internal/mask/set_test.go`, add `TestSensitiveName`: `token`, `API_TOKEN`, `password`, `passwd`, `apiKey`, `api_key`, `private_key`, `access_key`, `credential`, `authorization` and `author` are sensitive; `env`, `target`, `name`, "" are not; matching ignores case. Run and see it fail to compile.
- [X] T003 [P] Create `internal/audit/record_test.go`, `dir_test.go`, `log_test.go`, `read_test.go` and `filter_test.go` for [contracts/audit-record.md](contracts/audit-record.md), each in a `t.TempDir()` with a test clock. Cover: `Record` encodes to one line with `v` 1, the contract's field names, absent values left out, and `<`, `>`, `&` unescaped; `Encode` of a record over 64 KiB replaces `arguments.supplied` with `[N bytes not stored]` and fits; `ProjectID` is the first 16 hex digits of the SHA-256 of the path and differs for two paths; `DefaultBase` returns `$XDG_STATE_HOME/rune/audit` when set, `%LocalAppData%\rune\audit` for goos `windows`, else `<home>/.local/state/rune/audit`, and an error when none can be found; `Open` creates the base and project directory with mode `0700` on POSIX, writes `project.json` with the Runefile path and mode `0600`, refuses a project directory inside any of the given project directories with `*LocationError` whose text is the contract's, refuses a base or project directory that is a symbolic link (POSIX), refuses a project directory that belongs to another user (POSIX, made with `os.Chown` when the test runs as root, skipped otherwise), tightens a looser mode, and refuses a negative retention; `Append` writes the record to the file of the UTC day of the given action start (an end written after midnight goes to the start's file), creates the file with mode `0600`, starts on a new line when the file ends with a partial record, returns an error when the day file is a directory, and creates a removed project directory again; 8 goroutines on two `Log` values of one directory appending 50 records each leave 400 whole lines with distinct ids; a `.lock` older than ten seconds is taken over, a fresh one makes `Append` fail after its wait; `Prune` removes `YYYY-MM-DD.jsonl` files older than the retention, keeps newer ones, keeps every file with retention 0, and never touches `project.json` or a file with another name; `Append` on a new day prunes; a day file that cannot be removed (a non-empty directory with a day file's name) is reported once through `Options.Warn` and fails neither `Open` nor `Append`; `Read` over a range returns actions oldest first, pairs a start with its end, reports a start without an end as outcome `incomplete` with no end time and no exit code, counts as damaged and skips a line that is not JSON, a line with an unknown `v`, a line without `type`, `id` or `time`, an end without a start, a repeated start and a repeated end, and returns the damaged count per file; `Read` of a missing project directory returns no action and no error; `Filter.Match` implements each rule of [contracts/rune-log.md](contracts/rune-log.md): range on the start time (since inclusive, until exclusive), task or tool name, outcome, client name ignoring case, session id or prefix, and requests only with `All`. Run and see it fail to compile.
- [X] T004 [P] Create `mcpserver/audit_test.go` with a recording `Auditor` (keeps every call in memory, can be told to fail `CallStarted` or `CallEnded`) and a fake engine that implements `AuditEngine`. Through an in-memory client session, cover: with no `Options.Audit` a call's result and the tool list are byte for byte what the existing tests expect and `AuditFromContext` is nil; a call to a task that exits 0 reports one `CallStarted` (id of 16 hex digits, the tool, the task, the session with client name and version from the handshake, transport `stdio`, auth `process`, the engine's identity) before the engine runs, and one `CallEnded` with `AuditCompleted`, exit code 0 and a duration above zero; exit 3 gives `AuditFailed` and exit code 3; `initialize` and `tools/list` each report one `Request` with the method and no failure; two calls have distinct ids and the same session id; the session id is 16 hex digits and is not the protocol session id; `AuditFromContext(ctx).ID()` inside the engine equals the reported id; the handler runs against the snapshot the start was reported for (a `Replace` made between the start and the handler, forced by an `Auditor` that calls `Replace` in `CallStarted`, still runs the old engine and reports the old identity). Run and see it fail to compile.
- [X] T005 [P] Create `internal/cli/audit_test.go`. `TestAuditorWritesRecords`: an `auditor` on an `audit.Log` in a `t.TempDir()` turns `CallStarted` and `CallEnded` into a start and an end record with every field of the contract (digests prefixed `sha256:`, effective arguments as name to string and name to list for a variadic, `sandbox.enforced` false), and `Request` and `AuthFailed` into one record each. `TestAuditorStartFailure`: with a directory in place of the day file, `CallStarted` returns an error whose text is the contract's start message and the same text is on the operator writer. `TestAuditorEndFailure`: `CallEnded` returns an error whose text is the contract's `[audit log]` block and the warning is on the operator writer. `TestAuditorWarnsOnce`: two failing `Request` calls print one warning, and after a successful write a new failure prints again. `TestAuditorTransportAndParent`: an auditor built for an agent endpoint stores transport `agent-endpoint` and the parent id. `TestOpenAuditLog`: `Options.AuditRetention` values "", "0", "30" parse, and "soon", "-1", "1.5" give a usage error with the contract's text; a base inside the Runefile's directory is a usage error. Run and see it fail to compile.

### Implementation

- [X] T006 In `internal/mask/set.go`, add `func SensitiveName(name string) bool` that matches the built-in `namePatterns` case-insensitively as substrings, without the environment-only `builtinUnmasked` exceptions. Run T002: passes.
- [X] T007 Create `internal/audit/record.go`: the package comment, `const Version = 1`, the type and phase constants, the outcome constants (including `OutcomeIncomplete` and `OutcomeOK`), `Record` with the JSON field names of the contract and `omitempty` on every optional field (`Client`, `Arguments`, `Project`, `Git`, `Sandbox`, `Error`, `Approval`, `Plan` as pointer structs), `const MaxRecordBytes = 64 << 10`, and `func (r Record) Encode() ([]byte, error)` that writes one line with HTML escaping off and applies the size rule.
- [X] T008 Create `internal/audit/dir.go` (`DefaultBase(getenv func(string) string, goos string, home func() (string, error)) (string, error)`, `ProjectID(runefile string) string`, `ProjectDir(base, runefile string) string`, `inside(dir string, parents ...string) (string, bool)` on resolved paths) and `owner_unix.go` / `owner_other.go` with the owner check of `internal/planstore`.
- [X] T009 Create `internal/audit/log.go`: `Log` with `Dir()`, `Options{Base, Runefile string; Outside []string; RetentionDays int; Now func() time.Time; Warn func(error)}`, `Open(Options) (*Log, error)` (creates and checks the directories, writes `project.json`, prunes), `*LocationError`, `Append(start time.Time, rec Record, sync bool) error` (mutex, lock file with the wait, stale takeover and move-aside step of `planstore.lockJournal`, regular-file check, torn-tail repair, one `Write`, optional `Sync`, prune when the day changed), `Prune() error`, and `DefaultRetentionDays = 90`. Every error is wrapped with `%w`.
- [X] T010 Create `internal/audit/read.go` (`Action` with the fields of the action object, `Damage{File string; Count int}`, `Read(dir string, from, to time.Time) ([]Action, []Damage, error)` reading the day files from `from`'s UTC day to `to`'s, a zero `from` meaning the oldest file, lines up to 1 MiB) and `internal/audit/filter.go` (`Filter{Since, Until time.Time; Tasks, Outcomes, Clients, Sessions []string; All bool}` and `Match(Action) bool`). Run T003: passes.
- [X] T011 Create `mcpserver/audit.go` with everything of [contracts/mcpserver-api.md](contracts/mcpserver-api.md): `Auditor`, `AuditSession`, `AuditCall`, `AuditEnd`, `AuditArg`, `AuditApproval`, `AuditPlan`, `AuditRequest`, `AuditAuthFailure`, `AuditIdentity`, `AuditEngine`, the constants, `CallAudit` (mutex, nil-safe `ID`, `SetEffective`, `SetErrorKind`, `SetPlan`, and unexported setters for the result, the error text, the approval and the grants), `AuditFromContext`, `newAuditID()` (8 random bytes, hex), `sessionID(instance, protocol string) string`, and the unexported context keys for the call and the snapshot. Add `Audit Auditor` to `Options` and an `instance string` to `Server`, set in `New`, in `mcpserver/server.go`.
- [X] T012 In `mcpserver/reload.go`, make `middleware` record when `s.opts.Audit != nil`: for `tools/call`, load the snapshot, build the `AuditCall` (session from the request's `*mcp.ServerSession`, transport `http` when the request's extra carries a header and `stdio` otherwise, identity from the snapshot engine when it is an `AuditEngine`), call `CallStarted` and return an error result with the error's text when it fails, run `next` with the call and the snapshot in the context, report `AuditCompleted` or `AuditFailed` from the exit code of a returned `Result` and `AuditError` otherwise (the other outcomes of R5 come in T025), call `CallEnded`, and on its error append one `mcp.TextContent` with the error's text to the result when there is one; for every other method, time `next` and call `Request`. In `mcpserver/handler.go`, take the snapshot from the context when it carries one, and record the exit code of a returned `Result`. Without an `Auditor` the function is what it was. Run T004: passes.
- [X] T013 Add `AuditDir string` and `AuditRetention string` to `Options` in `internal/cli/dispatch.go` with comments naming the two variables. In `cmd/rune/root.go`, set them from `RUNE_AUDIT_DIR` and `RUNE_AUDIT_RETENTION_DAYS` in `PersistentPreRunE`, with a test in `cmd/rune/root_test.go`.
- [X] T014 Create `internal/cli/audit.go`: `openAuditLog(opts Options, runefile string, outside ...string) (*audit.Log, error)` (base from `opts.AuditDir` or `audit.DefaultBase`, retention parsing, usage errors with the contract's texts), `auditor` implementing `mcpserver.Auditor` (fields: log, operator writer, transport override, parent; `CallStarted`, `CallEnded`, `Request`, `AuthFailed`; the warn-once state under a mutex), and the conversion from the `mcpserver` types to `audit.Record`. Run T005: passes.
- [X] T015 In `internal/cli/serve.go`, compute the load's identity for every load (not only when a task uses plans) into a new `loadedModule.ident` and `mcpAdapter.ident`, leaving `planIdent` as it is; in `ServeMCP`, open the log after the transport checks and before the `[context]` hook, return its error, and pass `Audit` through `serveMCPOptions`. In `internal/cli/mcp.go`, implement `AuditIdentity` (identity plus the git state, which is `unavailable` until T048 adds `gitState`) and `Redact` on `mcpAdapter`. In `internal/cli/leak_test.go` and `cmd/rune` tests, set `XDG_STATE_HOME` to a temporary directory in `TestMain`; in `test/integration/harness_test.go`, set `RUNE_AUDIT_DIR` to a directory under the build's temporary directory in `TestMain`. Run the `mcpserver`, `internal/cli`, `cmd/rune` and `test/integration` suites: all pass.

**Checkpoint**: `rune mcp` writes a start and an end record for a call.

---

## Phase 3: User Story 1 - See what the agent did today (Priority: P1) MVP

**Goal**: Calls that ran are recorded with every field of FR-003 to FR-008, and `rune log`
lists today's actions of the project.

**Independent Test**: Quickstart A and B, then `rune log` lists both.

### Tests first

- [X] T016 [P] [US1] In `mcpserver/audit_test.go`, add `TestAuditEffectiveArguments`: an engine that calls `AuditFromContext(ctx).SetEffective` gives an end with those arguments; a call with no arguments has a nil `Supplied`; a call with `{"target":"release"}` has that tree. In `internal/cli/mcp_test.go` (or a new `internal/cli/mcp_audit_test.go`), add `TestAdapterReportsEffectiveArguments`: a call through `mcpserver` with a recording auditor on a task `build target="debug"` with no arguments reports effective `target=debug`, and a variadic reports its elements apart.
- [X] T017 [P] [US1] Create `internal/cli/log_test.go`. `TestLogToday`: with a log holding a completed call and a failed call today and a call yesterday (written through `audit.Log` with a test clock), `Log` prints the header and the two lines of today, oldest first, in the contract's text format with the `(local, UTC+hh:mm)` zone of a fixed test location, and exits 0. `TestLogEmpty`: no log directory prints the header and `No agent actions.`. `TestLogOtherProject`: actions of another Runefile are not listed. `TestLogFindsServerLog`: a log written for a Runefile is found when `Log` is given that Runefile through a relative `-f` path and, on POSIX, through a symbolic link to its directory. `TestLogRunsNothing`: a Runefile whose every task writes a marker has no marker after `Log`, and the log directory holds the same bytes as before. `TestLogTaskNamedLog`: a Runefile with a task `log` prints the note on standard error; one without prints none; a Runefile that does not parse prints none and still lists. `TestLogNoRunefile`: a directory with no Runefile gives a usage error. `TestRenderAction`: one case per row shape of the contract (completed with exit and duration, refused with a detail line, incomplete, agent run, authentication failure with a count, request), argument text cut at 80 characters, a missing client shown as `-`.
- [X] T018 [P] [US1] Create `cmd/rune/log_test.go`: `rune log` is a registered command named in `subcommandNames`, `rune log --help` describes it, and `-f` is accepted. Add `log` with a phrase of its help to the subcommand tables of `test/integration/cli_help_test.go`. Create `test/integration/audit_log_mcp_test.go` with the quickstart scratch Runefile, an SDK client helper for stdio and for `rune serve --http`, and a helper that reads the project's actions from the day files with `audit.Read`; add `TestAuditLogCompletedAndFailed` (quickstart A and B on both transports, in a git repository made by the test when git is present: every field of acceptance scenario 1, exit codes 0 and 3) and `TestRuneLogListsToday` (the text listing holds both calls and the exit column).

### Implementation

- [X] T019 [US1] In `internal/cli/mcp.go`, after `bindNamedParams` succeeds in `call`, set the effective arguments on `mcpserver.AuditFromContext(ctx)` from the task's parameters in declaration order, masked with `a.maskText`, a variadic's elements from `params.Lists`. Run T016: passes.
- [X] T020 [US1] Create `internal/cli/log.go`: `LogFlags` (every flag of the contract), `Log(opts Options, flags LogFlags) error` (resolve the Runefile, find the project directory with `audit.ProjectDir` on the path `runefilePath` gives, the same resolution `ServeMCP` uses, without creating it, compute the range for today in the display zone, read, filter, render), the text renderer of [contracts/rune-log.md](contracts/rune-log.md), the damage warnings, and the note for a task named `log`. Only the default range and the text output in this task; the other flags are parsed in T033.
- [X] T021 [US1] Create `cmd/rune/log.go` (`newLogCmd`, with its own `-f/--file`, `Args: cobra.NoArgs`, the long help and examples) and register it in `addCommands` in `cmd/rune/main.go`. Run T017 and T018: pass.

**Checkpoint**: US1 is demonstrable: call a task, run `rune log`.

---

## Phase 4: User Story 2 - Attempts that ran nothing are recorded too (Priority: P1)

**Goal**: Every way a call ends without running is recorded with its outcome and kind, and
never with an exit code (FR-005, FR-006).

**Independent Test**: Quickstart C, D, E and G.

### Tests first

- [X] T022 [P] [US2] In `mcpserver/audit_test.go`, add `TestAuditOutcomes`, table-driven over R5: an unknown tool (`AuditUnknownTool`, kind `protocol`, empty task); arguments that are not an object, an undeclared name, a value outside an enum (`AuditInvalidArguments`, kind `validation`); an ungranted destructive task with no approval possible (`AuditRefused`, kind `policy`); an engine error after `SetErrorKind(AuditKindValidation)` and after `SetErrorKind(AuditKindPolicy)`; an engine error with no mark (`AuditError`, kind `internal`); a call whose context is cancelled before the engine returns (`AuditCancelled`); a context whose deadline passed (`AuditTimedOut`). Every such end has a nil exit code, a redacted error text and no effective arguments unless the engine set them.
- [X] T023 [P] [US2] In `mcpserver/approval_test.go`, add `TestAuditApprovalDecision`: with a client that approves, declines, dismisses, accepts with `approved` false, never answers (short `ApprovalTimeout`), answers after a `Replace`, and a client without form support, the end's `Approval` has `Asked` and the decision of the contract (`approved`, `declined`, `dismissed`, `not-given`, `timed-out`, `runefile-changed`, `cannot-ask`), the tasks covered, and the outcome `AuditCompleted`, `AuditRefused`, `AuditRefused`, `AuditRefused`, `AuditTimedOut`, `AuditRefused`, `AuditRefused`; a call cancelled while the request is open gives decision `cancelled` and `AuditCancelled`.
- [X] T024 [P] [US2] In `internal/cli/mcp_audit_test.go`, add `TestAdapterMarksKinds`: through `mcpserver` with a recording auditor, a missing required argument gives kind `validation`, an `[agent-allow]` breach gives kind `policy`, a default that fails to evaluate gives kind `validation`, an apply task with no stored plan gives kind `policy`. In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogAttempts` (quickstart C, D, E, with C and E on both transports and D on stdio: outcomes, kinds, no exit code, marker files absent, and the exit column of `rune log` reads `-`).

### Implementation

- [X] T025 [US2] In `mcpserver/reload.go`, mark an unknown tool before `next`, and after it derive the outcome from the marks, the returned error and the context (cancelled and deadline win). In `mcpserver/handler.go`, mark `AuditKindValidation` at the decode, unknown-argument and constraint returns and `AuditKindPolicy` at the refusal returns, and store the redacted, bounded error text of every error result. Run T022: passes.
- [X] T026 [US2] In `mcpserver/approval.go`, make `approver` record the approval on the call's `CallAudit`: asked, the decision for each `outcome` and for the two cannot-ask paths, the tasks; and mark `AuditKindApproval` when it returns an error. In `mcpserver/handler.go`, record `cannot-ask` on the two early refusals that name a missing form. Run T023: passes.
- [X] T027 [US2] In `internal/cli/mcp.go`, mark `AuditKindValidation` when binding fails and `AuditKindPolicy` for the `[agent-allow]` refusal, the unavailable-on-this-host refusal, `applyNobodyText` and a `findPlan` error. Run T024: passes.

**Checkpoint**: Refused, invalid, unknown, cancelled and timed-out calls are in the log.

---

## Phase 5: User Story 3 - No call runs unrecorded, and a crash stays visible (Priority: P1)

**Goal**: A start that cannot be written refuses the call, a missing completion reads
`incomplete`, and a completion that cannot be written changes nothing (FR-014 to FR-016).

**Independent Test**: Quickstart K, L and M.

### Tests first

- [X] T028 [P] [US3] In `mcpserver/audit_test.go`, add `TestAuditStartFailureRefuses` (a failing `CallStarted`: the result is an error whose only text is the auditor's error, the engine is never called, and `CallEnded` is not called), `TestAuditEndFailureKeepsResult` (a failing `CallEnded` after exit 2: the first content block, the structured content and `IsError` equal those of the same call without an auditor, a second text block holds the auditor's error, and the engine ran once; the same for exit 0 with `IsError` false), and `TestAuditFailingAuditorServesRequests` (with an auditor whose `CallStarted` and `CallEnded` fail, `initialize` and `tools/list` answer exactly as they do without an auditor).
- [X] T029 [P] [US3] In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogStartFailure` (quickstart L: the result text is the contract's, `build.done` is absent, the server's standard error holds the same text), `TestAuditLogCompletionFailure` (quickstart M on both transports, with a task that waits for a file and exits 3: exit code 3 and the error flag in the result, the `[audit log]` block, the warning on standard error, one marker line), `TestAuditLogIncompleteAfterKill` (quickstart K: after the server is killed, `rune log --json` shows outcome `incomplete` with no `end` and no `exitCode`, `--outcome completed` lists nothing, `--outcome incomplete` lists it), and `TestAuditLogRequestFailureWarnsOnce` (with the day file replaced by a directory, two `tools/list` requests answer and standard error holds one warning).

### Implementation

- [X] T030 [US3] Make T028 and T029 pass: in `mcpserver/reload.go` confirm the refusal path returns before `next` and the completion path appends only a text block; in `internal/cli/audit.go` confirm the texts and the warn-once rule. Fix whatever the tests show. No new behavior beyond the contracts.

**Checkpoint**: The log cannot be skipped, and a killed call is never a success.

---

## Phase 6: User Story 4 - No secret reaches the log (Priority: P1)

**Goal**: Every stored string is masked, secret-named values are never stored, and values are
bounded (FR-018 to FR-021).

**Independent Test**: Quickstart I and J.

### Tests first

- [X] T031 [P] [US4] In `mcpserver/audit_test.go`, add `TestRedactSupplied`: with a redactor that masks a fixture, a fixture value at the top level, in an array, in a nested object and as a name is masked; the value under `token`, `Password` and `api_key` is `***` whether it is a string, a number, an array or an object; a 1 MiB string is cut to 1024 bytes at a character boundary plus `[N bytes cut]`; an object with 100 names lists 64 and counts 36 under `[more]`; an array of 100 elements lists 64 and ends with `[36 more]`; nesting past 8 levels is `[nested]`; numbers keep their spelling; `null` and booleans stay. `TestRedactText`: an error text holding the fixture is masked and cut at 2 KiB. `TestAuditClientNameRedacted`: a client that reports the fixture as its name is stored masked. `TestAuditEffectiveSensitiveName`: an effective argument named `token` is `***` even when the engine did not mask it. `TestAuditPlainEngine`: with an engine that is not an `AuditEngine`, the identity is empty, a value under `token` is still `***` and a long value is still cut.
- [X] T032 [P] [US4] In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogHoldsNoSecret` (quickstart I on both transports: the fixture and the bearer token occur zero times in every file of the log directory, in `rune log` and in `rune log --json`; `token` reads `***`; the task's standard output text is in no record) and `TestAuditLogBoundsValues` (quickstart J: the stored value ends with the cut mark and every line is under 64 KiB).

### Implementation

- [X] T033 [US4] In `mcpserver/audit.go`, implement `redactSupplied(raw json.RawMessage, redact func(string) string) any` and `redactText(s string, redact func(string) string, limit int) string` with the bounds of the contract as constants, apply the secret-name rule and the cut to effective arguments in `SetEffective`, and use them for the supplied arguments, the error text and the client name and version in `mcpserver/reload.go`. Run T031 and T032: pass.

**Checkpoint**: All P1 stories pass. The feature is usable.

---

## Phase 7: User Story 5 - Filter the log and read it from a script (Priority: P2)

**Goal**: The flags of [contracts/rune-log.md](contracts/rune-log.md) and the JSON output
(FR-029 to FR-035).

**Independent Test**: Quickstart R.

### Tests first

- [X] T034 [P] [US5] In `internal/cli/log_test.go`, add `TestLogRange` (with a fixed clock: `--date`, `--since` and `--until` as a day, an RFC 3339 time and a duration such as `2h` and `7d`; `--until` with a day includes that day; an action at 23:30 UTC is listed for the next day with `--tz Europe/Kyiv` and not with `--tz UTC`; the header names the zone with its UTC offset as the contract writes it, `(UTC)` for UTC, and, for a range, both ends), `TestLogFilters` (`--task` by task and by tool name, `--outcome failed,refused`, `--client` ignoring case, `--session` by id and by prefix, repeated and comma-separated values, and two different flags both required), `TestLogAll` (requests listed only with `--all`), `TestLogJSON` (one object per line, `schemaVersion` 1, `start` and `end` in UTC, `client.reported` true, an incomplete action without `end`, no header and nothing else on standard output, an empty result printing nothing), `TestLogDir` (prints the project directory, creates nothing), and `TestLogUsageErrors` (every row of the contract's usage error table, whole message, exit code 2, no action printed).
- [X] T035 [P] [US5] Create `test/integration/audit_log_cli_test.go`: `TestRuneLogFlags` runs the built binary over a log prepared by real calls plus day files written by the test, and checks one case of each flag, the JSON lines against the contract's field names, the exit codes 0 and 2, and that `rune log` creates no file.

### Implementation

- [X] T036 [US5] In `internal/cli/log.go`, implement the range parsing (`parseWhen`, `parseDay`, durations with `d`), `--tz` through `time.LoadLocation` with `local` and `UTC`, the list flags, `--all`, `--dir`, the JSON renderer (the action object with `schemaVersion` and `client.reported`), and every usage error. In `cmd/rune/log.go`, declare the flags. In `cmd/rune/main.go`, import `time/tzdata`. Run T034 and T035: pass.

---

## Phase 8: User Story 6 - Many sessions and processes write one readable log (Priority: P2)

**Goal**: Concurrent sessions and processes leave whole records with unique ids, and a damaged
record hides nothing else (FR-026, FR-027).

**Independent Test**: Quickstart P and Q.

- [X] T037 [P] [US6] In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogConcurrentProcesses` (quickstart P: two server processes for one project, 100 concurrent calls each; every line of every day file parses, `rune log --json` gives 200 `call` actions with 200 distinct ids, all `completed`) and `TestAuditLogDamagedRecord` (quickstart Q: half an object appended to the newest file, one more call, then `rune log` lists every earlier action and the new one, and standard error holds the contract's warning with the count 1). In `mcpserver/audit_test.go`, add `TestAuditConcurrentCalls`: 50 concurrent calls on one server report 50 distinct ids, each start before its end.
- [X] T038 [US6] Make T037 pass. Fix whatever it shows in `internal/audit/log.go` or `internal/audit/read.go`. No new behavior beyond the contracts.

---

## Phase 9: User Story 7 - Approvals, plans and the sandbox state belong to their call (Priority: P2)

**Goal**: The record links the approval, the grant, the plan and the sandbox state to the call
(FR-009, FR-013).

**Independent Test**: Quickstart F, G and H, and the plan and apply flow of spec 051.

### Tests first

- [X] T039 [P] [US7] In `mcpserver/audit_test.go`, add `TestAuditGrants`: a granted destructive task and a task that reaches a granted one report `Grants` with those names and no approval; an ungranted call approved at the client reports the approval and no grant. In `internal/planstore/journal_test.go`, add a case that `Event.Invocation` is written as `invocation` and left out when empty. In `internal/cli/planapply_test.go`, add `TestPlanEventsCarryInvocation`: every journal line written during an audited plan call and apply call holds the call's invocation id, and the end records hold plan role `stored` and `applied` with the plan's id and artifact digest; a journal line written from the command line has no invocation. In `internal/cli/mcp_audit_test.go`, add `TestInvocationIDInTaskEnvironment`: a task body that writes `$RUNE_INVOCATION_ID` to a file gets the id of the call's record, and a call on a server without an auditor gets no such variable.
- [X] T040 [P] [US7] In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogApprovalAndGrant` (quickstart F, G, H: the approval object, the two refusals without exit code, the grant) and `TestAuditLogPlanApply` (the plan and apply tasks of spec 051's test Runefile with a client that approves: the plan call's action has plan role `stored`, the apply call's has `applied` with the same id and the digest of the stored artifact and the approval `approved`, the journal lines of the apply hold its invocation id, `sandbox.enforced` is false on every action, and no action has a field named `user`, `person` or `principal`).

### Implementation

- [X] T041 [US7] In `mcpserver/handler.go`, record the grants: the names of `t` and `t.DestructiveReach` that are destructive and granted. Add `Invocation string` with `json:"invocation,omitempty"` to `planstore.Event` in `internal/planstore/journal.go`. In `internal/cli/mcp.go`, give the engine the invocation id (`engine.invocation`) and add `RUNE_INVOCATION_ID` to the environment of the call's tasks; in `internal/cli/planapply.go`, set `ev.Invocation` in `engine.journal`, call `SetPlan(AuditPlanStored, ...)` on the engine's `CallAudit` when `capturePlan` stored a plan and `SetPlan(AuditPlanApplied, ...)` when `claimPlan` claimed one (the engine keeps the `*mcpserver.CallAudit` it was built with); in `internal/cli/run.go`, add the `invocation` and `audit` fields. Run T039 and T040: pass.

---

## Phase 10: User Story 8 - Every way an agent reaches Rune is covered (Priority: P2)

**Goal**: Both transports, authentication failures, the agent endpoint with its parent, and
protocol requests (FR-001, FR-010 to FR-012).

**Independent Test**: Quickstart N, O and S.

### Tests first

- [X] T042 [P] [US8] In `mcpserver/transport_test.go`, add `TestAuditAuthFailure` (a request with no token and one with a wrong token each report one `AuthFailed` with the remote address; a request with the right token reports none; nothing of the request's headers is in the report), `TestAuditAuthFailureLimit` (with a test clock, 30 failures in one minute report 10, and the first report of the next minute carries `Suppressed` 20), and `TestAuditHTTPTransport` (a call over the HTTP transport reports transport `http` and auth `bearer-token`). In `mcpserver/audit_test.go`, add `TestAuditRequestFields` (a failing request reports `Failed` and a redacted error; no request report carries arguments).
- [X] T043 [P] [US8] In `internal/cli/audit_test.go`, add `TestAgentRunRecorded`: an engine that executes an `(agent)` task with a stand-in agent command that calls a tool on `RUNE_MCP_URL` writes an `agent-run` start before the agent starts and an end with outcome `completed` and exit code 0, the call's record has transport `agent-endpoint` and the run's id as `parent`; with `RUNE_INVOCATION_ID` set in the engine the run's `parent` is that id; a stand-in that exits 4 gives `failed`; a log that cannot be opened prints the contract's warning and the agent runs with no `RUNE_MCP_URL`. In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogHTTPAuthFailure` (quickstart N, and O for the bound of 10 records only), `TestAuditLogAgentEndpoint` (quickstart S with a stand-in agent written as a Runefile task or a small script the test builds), and `TestAuditLogRequests` (`rune log --all --json` holds `initialize` and `tools/list` with the session id and no arguments).

### Implementation

- [X] T044 [US8] In `mcpserver/transport.go`, make `tokenMiddleware` report a failure through an `authLimiter` (10 per minute, counts the rest) defined in `mcpserver/audit.go`, with the server's clock injectable for the test. Run T042: passes.
- [X] T045 [US8] In `internal/cli/agentexec.go`, before the endpoint starts: open the log with `openAuditLog`, write the `agent-run` start (id, task, parent from `e.invocation`, identity), and on failure print the warning and skip the endpoint; pass an `auditor` with transport `agent-endpoint` and the run's id as parent through `agentMCPOptions`; write the end when the agent returns (`completed` with exit code 0, `failed` with the exit code when the error carries one, `cancelled` when the context ended). In `internal/cli/run.go`, read `RUNE_INVOCATION_ID` into `engine.invocation` where the engine of a command-line run is built. Give `newAgentAdapter` the identity, computing it when the engine has none. Run T043: passes.

---

## Phase 11: User Story 9 - The log is stored safely and does not grow forever (Priority: P3)

**Goal**: Location, permissions, retention and honest git states (FR-007, FR-023 to FR-025,
FR-028).

**Independent Test**: Quickstart T and the git states.

### Tests first

- [X] T046 [P] [US9] In `internal/cli/audit_test.go`, add `TestGitState`: in a repository with a commit and a clean tree (`commit`, the id, dirty false), the same with an uncommitted change (dirty true), a repository with no commit (`unborn`), a plain directory (`none`), and with a `PATH` that has no git (`unavailable`, no commit, nil dirty); the test skips the git cases when git is not installed. `TestServeRefusesLogInsideProject`: `ServeMCP` with `AuditDir` inside the Runefile's directory, and inside a `set working-directory` directory, returns a usage error with the contract's text before the `[context]` hook runs (marker file absent). `TestServeRetention`: `AuditRetention` "soon" is a usage error; with "30" a day file dated 31 days ago is gone after start and one dated yesterday remains; with "0" both remain.
- [X] T047 [P] [US9] In `test/integration/audit_log_mcp_test.go`, add `TestAuditLogLocation` (quickstart T: `rune log --dir` is outside the project and equals the directory the records are in; on POSIX the directory has mode `0700` and the day file `0600`; `RUNE_AUDIT_DIR` inside the project makes `rune mcp` exit 2 with the message; `RUNE_AUDIT_RETENTION_DAYS=soon` exits 2; an old day file is removed at start) and `TestAuditLogGitIdentity` (in a repository the test makes: `commit` with the head id, dirty after an edit; in a plain directory: `none`).

### Implementation

- [X] T048 [US9] In `internal/cli/audit.go`, implement `gitState(ctx context.Context, dir string) mcpserver.AuditIdentity` parts by the table of R6, with the timeout and the `core.fsmonitor=false` guard of `gitInfo`, and use it in `mcpAdapter.AuditIdentity` through an injectable field (as `gitInfo` is). Confirm `ServeMCP` passes the Runefile's directory and the working directory to `openAuditLog` as the places the log may not be inside. Run T046 and T047: pass.

---

## Phase 12: Polish & Cross-Cutting Concerns

- [X] T049 [P] Document the feature in `docs/mcp.md`: a section "The audit log" before "Agent task bodies" covering what is recorded, the outcomes, `rune log` with examples, the timezone rule, the JSON contract with the field table, where the log is stored and how to move it, retention, what happens when a record cannot be written, secrets and secret-named parameters, what is never stored, parents and `RUNE_INVOCATION_ID`, the limits of a local log's integrity and how to export records, with a link from the "Security model" section and from "Retention and the journal" (the journal's new `invocation` field).
- [X] T050 [P] Update `docs/cli.md` (the `rune log` row in the subcommand table, an "Environment" entry for `RUNE_AUDIT_DIR`, `RUNE_AUDIT_RETENTION_DAYS` and `RUNE_INVOCATION_ID`, and the note that a task named `log` is run with `rune -- log`), `docs/troubleshooting.md` (the audit error and its fix), `SECURITY.md` (what the log proves and does not), `README.md` (one line in the MCP feature list) and `CONTRIBUTING.md` only if it lists packages or test environment variables.
- [X] T051 Run the docs harness (`go test ./test/docs/...`) and fix every block and link it reports.
- [X] T052 Run the full gates of [quickstart.md](quickstart.md): the whole suite, the race suite, `golangci-lint`, and `gofumpt`/`goimports` cleanliness. Check coverage of `internal/audit`, the new files of `mcpserver` and `internal/cli` is at least 90% and add the missing cases.
- [X] T053 [P] In `internal/audit/read_test.go`, add `TestReadTenThousandActions` (a day file with 10,000 paired calls is read and filtered in under 2 seconds, SC-007) and in `internal/audit/log_test.go` add `BenchmarkAppend` and `TestAppendIsFast` (100 synced appends average under 20 ms each on the test disk, so that two appends stay inside the 50 ms of SC-008; the test logs the average).
- [X] T054 Walk [quickstart.md](quickstart.md) A to T against the built binary once by hand or through the integration tests, and check every acceptance criterion of issue #50 against a named test in the PR description.

---

## Dependencies & Execution Order

- Phase 2 blocks every story. Inside it: T002 to T005 first (tests), then T006, T007 to T010
  (the store, in order), T011, T012, T013, T014, T015.
- US1 (Phase 3) needs Phase 2. US2, US3 and US4 need Phase 2 and are independent of each other
  and of US1's command: their integration tests read the day files with `audit.Read`. Tests
  that check `rune log --json` or `--outcome` (T029, T032, T037) need T036.
- US5 needs US1 (the command). US6 needs Phase 2 only. US7 needs US2 (the approval record).
  US8 needs Phase 2. US9 needs Phase 2.
- Polish needs every story.

## Parallel Examples

- Phase 2 tests: T002, T003, T004 and T005 touch four packages and can be written together.
- After Phase 2: T016, T017 and T018 (US1 tests) together; T022, T023 and T024 (US2 tests)
  together; T031 and T032 (US4 tests) together.
- Documentation tasks T049 and T050 touch different files.

## Implementation Strategy

1. Phase 2, then US1: the MVP. A call leaves a record and `rune log` shows it.
2. US2, US3, US4: the other P1 stories. Together they make the log trustworthy: attempts,
   failure policy, secrets.
3. US5 to US8: filters, concurrency, approvals and plans, the other surfaces.
4. US9 and Polish: location and retention checks, documentation, full gates.
