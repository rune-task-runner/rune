# Feature Specification: Record Agent Actions in a Durable Log and Show Them with `rune log`

**Feature Branch**: `052-mcp-audit-log`

**Created**: 2026-11-18

**Status**: Draft

**Input**: GitHub issue [#50](https://github.com/rune-task-runner/rune/issues/50)
(backlog MCP-16): "Record agent actions in a durable audit log and expose rune log"

## Overview

An operator cannot answer "what did the agent do today?" from Rune. The result of a tool call
exists in the client's conversation and nowhere else. An investigation needs a record that
outlives the conversation: what was attempted, with which arguments, what was approved, what
failed, and which code and configuration ran.

This feature makes every Rune MCP server write an audit log, and adds the `rune log` command
that reads it.

```text
$ rune log
Agent actions on 2026-11-18 (local, UTC+02:00) for /repo/Runefile

10:04:11  completed    plan env="prod"       exit 0   12.4s  claude-code 2.1.0  3fa9c2d1e07b5a44
10:05:02  completed    apply env="prod"      exit 0   22.1s  claude-code 2.1.0  b81e04f7a2c9d310
          approved at the client; plan 7f3c9a1e52b84d06 sha256:9b1f...c2e0
10:07:40  refused      clean                 -        0.0s   claude-code 2.1.0  5d20e1b9c4a7f863
          task "clean" is destructive; to permit it, add clean to --allow-destructive ...
10:08:15  failed       test pkg="./cli"      exit 1   48.0s  claude-code 2.1.0  0c7a91e3b5d2f648
10:09:00  incomplete   deploy env="staging"  -        -      codex 0.9.2        e4b6a8d0127c95f3
          no completion was recorded: the call is still running or the server stopped
```

| Question | Today | After |
|---|---|---|
| What did the agent run today? | Only the client's conversation knows | `rune log` lists every call with its time, outcome, task, arguments, exit code, duration and client |
| Did the agent try something that was refused? | Not recorded | Recorded with the reason, and no exit code |
| What code ran? | Not recorded | Each call records the commit, whether the tree was dirty, and the digests of the Runefile and configuration |
| The server was killed during a call | Nothing remains | The call is in the log as incomplete, never as a success |
| Was a secret written anywhere? | Tool results are masked | The log is masked the same way, and values of secret-named parameters are not stored |
| Can a script read the history? | No | `rune log --json` prints one stable JSON object per action |

### What the log is and is not

The log is operational evidence on the operator's machine. Its files can be read and changed by
a process that runs as the same user, so it is not tamper-proof and is not compliance storage by
itself. The documentation says so and describes how to ship the records to storage the operator
controls.

### Out of scope

- Actions a person takes on the command line. Only what arrives through an MCP server is
  recorded, plus the run of an `(agent)` task that opens such a server.
- Storing task output, plan contents, environment variables or protocol message bodies.
- Tamper-proof or signed logs, and a built-in remote sink. The export path is the JSON output
  and the files themselves.
- Enforced sandboxing (issue #51). The log states that no sandbox was enforced.
- Verified human identity. A client name is what the client reported.
- Messages the protocol layer rejects before they are a request: unreadable JSON, a method the
  server does not have, and requests sent before the session is initialized.
- A switch that turns the log off for `rune mcp`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See what the agent did today (Priority: P1)

An agent works through `rune mcp` during the day. In the evening the operator runs `rune log` in
the project and sees each call: when it started, how it ended, which task ran with which
arguments, its exit code, how long it took, which client made it, and which commit it ran on.

**Why this priority**: This is the question the feature exists to answer.

**Independent Test**: Serve a Runefile with one task that succeeds and one that exits with 3.
Call both from a real client session. Run `rune log`. Both calls are listed with the task, the
arguments, exit codes 0 and 3, a duration, the client name and the commit.

**Acceptance Scenarios**:

1. **Given** a served Runefile in a git repository, **When** an agent calls a task that exits 0,
   **Then** the log holds one action with the tool name, the task name, the supplied arguments,
   the arguments the task ran with (defaults included), outcome `completed`, exit code 0, UTC
   start and end times, a duration, a unique invocation id, the session id, the client name and
   version, the transport, the commit, the dirty flag, and the Runefile and configuration
   digests.
2. **Given** the same server, **When** an agent calls a task whose body exits 3, **Then** the
   action has outcome `failed` and exit code 3.
3. **Given** those two calls made today, **When** the operator runs `rune log` in the project,
   **Then** both are listed, oldest first, and nothing from another project is listed.
4. **Given** a call made yesterday, **When** the operator runs `rune log` with no flags,
   **Then** it is not listed.
5. **Given** a project where no agent has called anything, **When** the operator runs
   `rune log`, **Then** the command says there are no actions and exits 0.
6. **Given** a Runefile that defines a task named `log`, **When** the operator runs `rune log`,
   **Then** the log is shown with a note that the task is run with `rune -- log`.

---

### User Story 2 - Attempts that ran nothing are recorded too (Priority: P1)

An agent calls a tool that does not exist, sends an argument the task does not declare, calls a
destructive task that was not granted, or the call is cancelled before anything starts. Each of
these is in the log with what was attempted and why nothing ran. None of them has an exit code.

**Why this priority**: An investigation needs the attempts, not only the runs. A log that keeps
only what a task handler saw misses what was refused before it.

**Independent Test**: From a real client session, call an unknown tool, a known tool with an
undeclared argument, a known tool with a value outside its enum, and an ungranted `[confirm]`
task. Read the log. Four actions exist with outcomes `unknown-tool`, `invalid-arguments`,
`invalid-arguments` and `refused`, each with no exit code, and the marker file of each task is
absent.

**Acceptance Scenarios**:

1. **Given** a served Runefile, **When** an agent calls a tool name no task has, **Then** an
   action is recorded with that name, outcome `unknown-tool`, no task name and no exit code.
2. **Given** a task with an enum parameter, **When** an agent sends a value outside it,
   **Then** the action has outcome `invalid-arguments`, error kind `validation`, the supplied
   arguments, no effective arguments and no exit code.
3. **Given** a `[confirm]` task the operator did not grant and a client that cannot be asked,
   **When** an agent calls it, **Then** the action has outcome `refused`, error kind `policy`
   and no exit code.
4. **Given** a task with an `[agent-allow]` policy, **When** an agent calls it with a value the
   policy refuses, **Then** the action has outcome `refused` and error kind `policy`.
5. **Given** a call the client cancels while an approval request is open, **When** the call
   ends, **Then** the action has outcome `cancelled` and no exit code.
6. **Given** an approval request nobody answers within the wait, **When** the call ends,
   **Then** the action has outcome `timed-out` and no exit code.
7. **Given** a call cancelled while the task body runs, **When** the call ends, **Then** the
   action has outcome `cancelled`.
8. **Given** any action of this story, **When** `rune log` shows it, **Then** the exit column is
   empty: a refusal is never shown with exit code 0.

---

### User Story 3 - No call runs unrecorded, and a crash stays visible (Priority: P1)

Before a task can do anything, the start of the call is on disk. If it cannot be written, the
call is refused with an error that says what to fix. If the server dies during a call, the start
remains without a completion, and `rune log` shows the call as incomplete. If the completion
cannot be written, the operator is told, and the result the agent receives is the task's own.

**Why this priority**: A log that can be skipped, or that reports a killed call as a success, is
worse than none: it is trusted and wrong.

**Independent Test**: Serve a Runefile whose task writes a marker file. Make the log directory
unwritable and call the task: the result is an audit error and no marker exists. Restore it,
call a task that sleeps, kill the server, and run `rune log`: the call is listed as
`incomplete`.

**Acceptance Scenarios**:

1. **Given** a log that cannot be written, **When** an agent calls any tool, **Then** the result
   is an error that names the audit log, its directory and the variable that moves it, the task
   does not run, and the same message is printed on the server's standard error.
2. **Given** a call whose start was recorded, **When** the server process is killed before the
   call ends, **Then** a later `rune log` shows the action with outcome `incomplete`, no exit
   code and no end time, and `rune log --outcome completed` does not list it.
3. **Given** a task that fails with exit 2 and a log that becomes unwritable while it runs,
   **When** the call ends, **Then** the agent receives the task's failure with exit code 2
   unchanged, with a line that says the completion was not recorded, the same is printed on the
   server's standard error, and the task is not run again.
4. **Given** a task that succeeds and a completion that cannot be written, **When** the call
   ends, **Then** the result is still the task's success with the same added line.
5. **Given** a log that cannot be written, **When** a client sends a request that is not a tool
   call, **Then** the request is answered as before and the server prints one warning on
   standard error until a write succeeds again.

---

### User Story 4 - No secret reaches the log (Priority: P1)

An agent passes a value that is a secret of the server's environment, or a task refuses a call
with a message that quotes one. A parameter is named `token`. None of these values is in the
stored records, in what `rune log` prints, or in its JSON output.

**Why this priority**: A log that outlives the conversation is the worst place for a credential.

**Independent Test**: Start a server with `API_TOKEN` set to a fixture value. Call a task with
that value as an argument, once valid and once with an invalid enum value that the error quotes,
and call a task with a parameter named `password`. Search the log files and both outputs of
`rune log`: the fixture and the password value occur zero times.

**Acceptance Scenarios**:

1. **Given** a secret of the server's environment, **When** an agent supplies it as an argument
   value, **Then** the stored supplied and effective arguments hold the mask, not the value.
2. **Given** a refusal or validation error whose text quotes a secret, **When** it is recorded,
   **Then** the stored error text holds the mask.
3. **Given** a parameter whose name matches Rune's secret-name rule, such as `token`,
   `password` or `api_key`, **When** a call supplies any value for it, **Then** the stored value
   is the mask, whatever the value is.
4. **Given** a call over Streamable HTTP, **When** it is recorded, **Then** no record holds the
   bearer token or any request header.
5. **Given** a task that prints output, **When** the call is recorded, **Then** no record holds
   the task's output.
6. **Given** an apply call, **When** it is recorded, **Then** the record holds the plan's id and
   digest and no byte of the plan or of its summary.
7. **Given** an argument value of 1 MiB, **When** the call is recorded, **Then** the stored
   value is cut to a fixed size with a mark that states how many bytes were left out.

---

### User Story 5 - Filter the log and read it from a script (Priority: P2)

The operator narrows the log to one task, one outcome, one client, one session or a time range,
chooses the timezone that defines a day, and gets the same data as JSON for a script.

**Why this priority**: The default view answers "today". An investigation asks narrower
questions and feeds other tools.

**Independent Test**: Record calls across two days, two tasks, two clients and several outcomes.
Run `rune log` with each filter and check the listed invocation ids. Run with `--json` and check
that every line parses and carries the documented fields.

**Acceptance Scenarios**:

1. **Given** actions on several days, **When** the operator runs `rune log --date 2026-11-17`,
   **Then** only actions that started on that day in the display timezone are listed.
2. **Given** an action that started at 23:30 UTC, **When** the operator runs
   `rune log --date` for the next day with `--tz Europe/Kyiv`, **Then** it is listed, and with
   `--tz UTC` it is not.
3. **Given** `--since 2h`, **When** the command runs, **Then** only actions that started in the
   last two hours are listed. `--since` and `--until` also accept a date and an RFC 3339 time.
4. **Given** `--task deploy`, `--outcome failed,refused`, `--client claude-code` or
   `--session <id>`, **When** the command runs, **Then** only matching actions are listed, and
   filters given together must all match.
5. **Given** `--json`, **When** the command runs, **Then** standard output holds one JSON object
   per action, each with a schema version and UTC times, and nothing else.
6. **Given** an unknown outcome name, an unknown timezone or `--date` together with `--since`,
   **When** the command runs, **Then** it fails with a usage error that names the accepted
   values, and prints no actions.
7. **Given** `--all`, **When** the command runs, **Then** protocol requests that are not tool
   calls are listed too. Without it they are not.
8. **Given** `--dir`, **When** the command runs, **Then** it prints the directory that holds the
   project's log files and nothing else.

---

### User Story 6 - Many sessions and processes write one readable log (Priority: P2)

Two clients call tools at the same time through one HTTP server, while a second `rune mcp`
process serves the same project over stdio. Every action is a whole record with its own id. A
record cut short by a crash does not hide the records around it.

**Why this priority**: Concurrent agents are normal use. A log with merged or lost lines cannot
be trusted for any of them.

**Independent Test**: Run two server processes for one project and make 200 concurrent calls
across them. Every line of the log parses, the number of actions is 200, and their ids are
distinct. Then append half a record to the newest file, make one more call, and run `rune log`:
all 201 actions are listed and one damaged record is reported.

**Acceptance Scenarios**:

1. **Given** concurrent calls on one server, **When** they end, **Then** every stored line is a
   whole record, and each action has a unique invocation id.
2. **Given** two server processes for one project, **When** both record calls at once,
   **Then** no line holds parts of two records and no record is lost.
3. **Given** a log file whose last record was cut short, **When** a server writes the next
   record, **Then** the new record starts on its own line.
4. **Given** a log file with a damaged record, **When** `rune log` reads it, **Then** every
   other record is shown, and the command reports on standard error how many records it could
   not read and in which file.

---

### User Story 7 - Approvals, plans and the sandbox state belong to their call (Priority: P2)

A destructive call was approved in the client, granted by the operator, or declined. An apply
call ran an approved plan. The record of each call says which, names the plan and its digest,
and states that no sandbox was enforced. It names no person.

**Why this priority**: "Who allowed this?" is the first question after "what ran?". The answer
must be as precise as Rune's knowledge and no more.

**Independent Test**: With a client that approves, call an ungranted `[confirm]` task, a granted
one, and the plan and apply tasks of spec 051. Read the four actions: the first says approved at
the client and lists the covered tasks, the second says granted by the operator, the third names
the stored plan, the fourth names the approved plan and digest. The plan journal's lines for the
apply carry the action's invocation id.

**Acceptance Scenarios**:

1. **Given** an ungranted `[confirm]` task and a client that approves, **When** the call runs,
   **Then** the action records that approval was asked, that it was given, and the tasks it
   covered.
2. **Given** the same call declined, dismissed or left unanswered, **When** it ends, **Then**
   the action records that decision and has no exit code.
3. **Given** a `[confirm]` task named in `--allow-destructive`, **When** it is called,
   **Then** the action records that the operator's grant allowed it.
4. **Given** a plan task, **When** its call stores a plan, **Then** the action records the plan
   id and artifact digest.
5. **Given** an apply call a person approved, **When** it ends, **Then** the action records the
   approval, the plan id and the approved artifact digest, and every line the plan journal wrote
   for that call carries the action's invocation id.
6. **Given** any action, **When** it is read, **Then** it states that no sandbox was enforced,
   and holds no field that names a person: the client name and version are marked as reported
   by the client.

---

### User Story 8 - Every way an agent reaches Rune is covered (Priority: P2)

The log covers `rune mcp` on stdio, `rune serve --http`, and the endpoint an `(agent)` task
opens for the agent it drives. A request with a missing or wrong bearer token leaves a minimal
record. Calls made through an agent task's endpoint point at the run of that task, so they do
not read as unrelated top-level calls.

**Why this priority**: A log that covers one transport gives a false sense of completeness.

**Independent Test**: Make the same call through stdio and through Streamable HTTP and compare
the actions: they differ only in transport, session and times. Send an HTTP request with a wrong
token: one authentication-failure record exists, with no client name. Run an `(agent)` task
whose agent calls a tool: the log holds a record of the agent run and a call whose parent is
that run.

**Acceptance Scenarios**:

1. **Given** the same Runefile served over stdio and over Streamable HTTP, **When** the same
   call is made on each, **Then** both are recorded with the same fields and their own
   transport.
2. **Given** the HTTP transport, **When** a request arrives without the right bearer token,
   **Then** one record of an authentication failure is written with the time, the transport and
   the remote address, and with no client name, no header and no token.
3. **Given** a flood of unauthenticated requests, **When** they arrive, **Then** the number of
   records written per minute is bounded, and the next record states how many were left out.
4. **Given** an `(agent)` task run from the command line, **When** its agent calls a tool
   through the endpoint, **Then** the log holds a record of the agent run with its start, end
   and exit code, and the call's record names that run as its parent.
5. **Given** an agent call whose task body runs `rune` on an `(agent)` task, **When** that inner
   agent calls a tool, **Then** the inner agent run names the outer call as its parent.
6. **Given** any session, **When** it sends `initialize`, `tools/list` or another request that
   is not a tool call, **Then** one record is written with the method, the session id, the
   client, whether it succeeded and its duration, and with no arguments, no exit code and no
   message body.
7. **Given** a server whose Runefile is reloaded between two calls, **When** both are recorded,
   **Then** each carries the Runefile digest of the load it ran against.

---

### User Story 9 - The log is stored safely and does not grow forever (Priority: P3)

The log lives in the operator's state directory, not in the project, so a task that writes in
its working directory cannot reach it by a relative path. Its files are private to the user.
Old days are removed after a documented period. The operator can move the log, change the
period, and ship the records elsewhere.

**Why this priority**: Without it the feature still answers the question, but the log could be
committed by accident, removed by a `clean` task, or fill a disk.

**Independent Test**: Record a call and check that the log directory is outside the Runefile's
directory and working directory, that its mode allows only the owner, and that a file dated
before the retention period is gone after the next server start. Point the log variable at a
directory inside the project and start the server: it refuses to start.

**Acceptance Scenarios**:

1. **Given** default settings, **When** a server records its first action, **Then** the log is
   in the user's state directory, in a directory of its own per project, created for the owner
   only, with files nobody else can read.
2. **Given** `RUNE_AUDIT_DIR` set to a directory inside the Runefile's directory or the tasks'
   working directory, **When** a server starts, **Then** it refuses to start and says why.
3. **Given** a log directory that is a symbolic link or belongs to another user, **When** a
   server starts, **Then** it refuses to start.
4. **Given** log files older than the retention period, **When** a server starts or first
   writes on a new day, **Then** they are removed, and newer files are untouched.
5. **Given** `RUNE_AUDIT_RETENTION_DAYS=0`, **When** a server runs, **Then** no file is removed.
6. **Given** a repository with no commit yet, a directory that is not a repository, and a host
   without git, **When** calls are recorded in each, **Then** the git state reads `unborn`,
   `none` and `unavailable`, with no commit invented.
7. **Given** a tree with uncommitted changes, **When** a call is recorded, **Then** the action
   says the tree was dirty.

---

### Edge Cases

- A call's arguments are not a JSON object, or hold a nested object or null. The supplied
  arguments are recorded as received, masked and bounded, with outcome `invalid-arguments`.
- A call names 10,000 undeclared arguments. The record lists a bounded number of names and
  counts the rest.
- A client sends no client information, or an empty name. The client fields are empty, and
  `rune log` shows a dash.
- A stdio session has no protocol session id. Rune gives each server process its own session id
  for it.
- The clock moves backwards during a call. The duration is measured on a monotonic clock and is
  never negative.
- A call starts before midnight and ends after it. It belongs to the day it started on.
- The log directory is removed while the server runs. It is created again on the next write. A
  call whose start cannot be written is refused.
- The disk fills between the start and the completion of a call. The call's result is returned,
  the failure is reported, and the action reads as incomplete.
- `rune log` is run outside any project. It fails with the usual no-Runefile error.
- Two Runefiles in one directory, or one project reached through a symbolic link. Each Runefile
  has its own log, and one Runefile reached by two paths has one.
- A task named `log` is still an MCP tool named `log`. Only the command line name is taken.
- The server runs with a mask set that is empty. Values are stored as given, except for
  secret-named parameters.
- An embedder uses the server library without configuring a log. Nothing is recorded and
  nothing changes for it.

## Requirements *(mandatory)*

### Functional Requirements

**What is recorded**

- **FR-001**: Every Rune MCP server started by `rune mcp`, `rune serve` (stdio and Streamable
  HTTP) and by an `(agent)` task for its agent MUST record every request it handles.
- **FR-002**: For every tool call, Rune MUST record a start before anything of the call runs
  and a completion when the call ends. Both carry the same invocation id, which MUST be unique
  across sessions and processes.
- **FR-003**: The record of a tool call MUST hold: the UTC start time; the UTC end time and the
  duration measured on a monotonic clock, once it ended; the invocation id; the session id; the
  parent id when the call was made through an agent task's endpoint; the tool name; and the
  task name when the tool names a task.
- **FR-004**: The record MUST hold the arguments as supplied and, when binding succeeded, the
  arguments the task ran with, defaults included, with a variadic's elements kept apart.
- **FR-005**: The record MUST hold one outcome: `completed` (the task ran and exited 0),
  `failed` (it ran and exited non-zero), `refused` (a policy, a missing grant or an approval
  that was not given stopped it), `invalid-arguments`, `unknown-tool`, `cancelled`,
  `timed-out` or `error` (anything else that stopped it). A reader MUST report an action with a
  start and no completion as `incomplete`.
- **FR-006**: The record MUST hold the task's exit code only when a run produced one. A call
  that ran nothing MUST have no exit code, in storage and in every output. When no task ran,
  the record MUST hold an error kind, one of `protocol`, `validation`, `policy`, `approval` or `internal`, and the masked, bounded text of the error. A call whose start could not be recorded has no record, so no kind names the log itself.
- **FR-007**: The record MUST hold the project identity (the resolved path of the Runefile), the
  digest of the composed Runefile and the digest of the configuration of the load the call ran
  against, and the git state at the call: `commit` with the commit id and whether the tree was
  dirty, `unborn` for a repository with no commit, `none` outside a repository, or
  `unavailable` when git could not be asked.
- **FR-008**: The record MUST hold the client name and version from the session's handshake,
  the transport (`stdio`, `http` or `agent-endpoint`) and how the session was authenticated
  (the local process, or the server's bearer token). It MUST NOT hold a field that claims a
  verified person. Rune has no authenticated principal today, and the field is absent.
- **FR-009**: The record MUST hold the approval decision when approval was needed: whether the
  person was asked, the answer (approved, declined, dismissed, not given, timed out, no valid
  answer, cancelled, Runefile changed, could not be asked), and the tasks it covered. It MUST
  name the tasks the operator's grant allowed. It MUST hold the id and artifact digest of a plan
  the call stored or applied. It MUST state the sandbox as not enforced.
- **FR-010**: A request that is not a tool call MUST be recorded once, with the method, the
  time, the session id, the client, the transport, whether it succeeded and its duration, and
  MUST NOT hold arguments, an exit code, a message body or a response.
- **FR-011**: A request refused for a missing or wrong bearer token MUST be recorded as an
  authentication failure with the time, the transport and the remote address only. No more
  than 10 such records are written per minute per server, and the next record states how many
  were left out.
- **FR-012**: The run of an `(agent)` task that opens an endpoint MUST be recorded with a start
  and a completion (exit code included), and every call made through that endpoint MUST name
  the run as its parent. When the agent task itself runs inside an agent's call, the run MUST
  name that call as its parent. The invocation id of a call MUST be available to the task's
  processes as `RUNE_INVOCATION_ID`.
- **FR-013**: Every plan journal line (spec 051) written during a tool call MUST carry that
  call's invocation id.

**When a record cannot be written**

- **FR-014**: When the start of a tool call cannot be written, the call MUST be refused before
  anything runs. The result MUST be an error that names the audit log, its directory, the
  reason and `RUNE_AUDIT_DIR`, and the same MUST be printed on the server's standard error.
- **FR-015**: When the completion of a tool call cannot be written, the result MUST be the
  task's own: the same exit code, output and error flag, plus one line that says the completion
  was not recorded. The failure MUST be printed on the server's standard error. Rune MUST NOT
  run the task again.
- **FR-016**: When the record of a request that is not a tool call, of an authentication
  failure, or the removal of an old file fails, the request MUST be served as before and Rune
  MUST print a warning on the server's standard error, once until a write succeeds again.
- **FR-017**: When the record of an agent run cannot be written, the agent task MUST run
  without its endpoint and Rune MUST print a warning that names the audit log.

**Secrets and size**

- **FR-018**: Every string stored in a record MUST pass through the masking of the load the call
  ran against: argument names and values, error texts, approval details and client-reported
  names.
- **FR-019**: The value of a parameter or supplied argument whose name matches Rune's
  secret-name rule MUST be stored as the mask, whatever it is.
- **FR-020**: Records MUST NOT hold bearer tokens, request headers, environment variables, task
  output, fix suggestions, plan or summary bytes, or protocol message bodies.
- **FR-021**: A stored value longer than 1 KiB MUST be cut, with a mark that states the bytes
  left out. A record lists at most 64 names of a supplied object and 64 elements of a supplied
  array and counts the rest, and replaces nesting deeper than 8 levels with a mark. A stored
  error text is at most 2 KiB. A record MUST NOT exceed 64 KiB.

**Storage**

- **FR-022**: The log MUST be stored as versioned, line-delimited JSON records, appended and
  never rewritten, one file per UTC day, in a directory per project.
- **FR-023**: By default the log MUST live in the user's state directory: `$XDG_STATE_HOME/rune/audit`
  when that variable is set, else `%LocalAppData%\rune\audit` on Windows, else
  `~/.local/state/rune/audit`. `RUNE_AUDIT_DIR` MUST replace that base directory. It MUST be an
  absolute path: a relative one would name a different directory for a server, for `rune log`
  and for a `rune` a task starts, and is refused.
- **FR-024**: A server MUST refuse to start when the log's directory is inside the Runefile's
  directory or the tasks' working directory, is a symbolic link, or belongs to another user.
  The endpoint of an `(agent)` task is not opened in that case, as FR-017 says.
- **FR-025**: The log's directories MUST be created for the owner only and its files MUST NOT be
  readable or writable by group or others. On Windows, where file modes do not restrict access,
  the directory inherits the protection of the user's profile, and the documentation says so.
- **FR-026**: Writers MUST be serialized within a process and across processes, so that no
  stored line holds parts of two records and none is lost.
- **FR-027**: A writer that finds the newest file ending in a partial record MUST start its
  record on a new line. A reader MUST skip a record it cannot read, keep every other record,
  and report the number of skipped records and their file on standard error.
- **FR-028**: Files older than 90 days MUST be removed when a server starts and when it first
  writes on a new day. `RUNE_AUDIT_RETENTION_DAYS` MUST change the period, and `0` MUST keep
  every file. A value that is not a whole number of days, zero or more, MUST stop the server
  with a usage error.

**`rune log`**

- **FR-029**: `rune log` MUST list the tool calls, agent runs and authentication failures of the
  current project that started today, oldest first. The project is the Runefile found from the
  working directory, or the one `-f` names.
- **FR-030**: "Today" and every date MUST be read in the display timezone: the local zone of
  the machine by default, or the zone `--tz` names (an IANA name, `UTC` or `local`). The command
  MUST print the zone it used and its UTC offset. Times in text output are in that zone.
- **FR-031**: `--date DAY` MUST select one day. `--since` and `--until` MUST select a range and
  accept a day, an RFC 3339 time, or a duration counted back from now such as `2h` or `7d`. A
  day given to `--until` includes that day. `--date` MUST NOT be combined with the other two.
- **FR-032**: `--task`, `--outcome`, `--client` and `--session` MUST filter by task or tool
  name, by outcome, by client name without regard to case, and by session id. Each accepts
  several values, comma-separated or repeated. Filters given together MUST all match.
- **FR-033**: `--json` MUST print one JSON object per action on standard output and nothing
  else there: a schema version, UTC times, and every field of the record contract, with absent
  values left out. The fields and their meaning MUST be documented as a stable contract.
- **FR-034**: `--all` MUST add the requests that are not tool calls. `--dir` MUST print the
  project's log directory and nothing else.
- **FR-035**: `rune log` MUST exit 0 when it listed actions or found none, 2 for a usage error
  or a missing Runefile, and 1 when the log cannot be read.
- **FR-036**: When the Runefile defines a task named `log`, `rune log` MUST still show the log
  and MUST print a note on standard error that the task is run with `rune -- log`.
- **FR-037**: `rune log` MUST run no task and MUST NOT write to the log.

**Compatibility, documentation and tests**

- **FR-038**: Tool lists, tool results and refusal texts MUST be what they were before the
  feature, except for the audit error of FR-014 and the added line of FR-015.
- **FR-039**: A program that embeds the server library and configures no log MUST see no change.
- **FR-040**: The documentation MUST cover the command and its flags, the timezone rule, the
  record fields and outcomes, the JSON contract, where the log is stored and how to move it,
  retention, what happens when a record cannot be written, how secrets and secret-named
  parameters are handled, what is never stored, the limits of a local log's integrity, how to
  export records to storage the operator controls, and that a task named `log` is now run with
  `rune -- log`.
- **FR-041**: Automated tests MUST cover every acceptance criterion of the issue through a real
  client session, on stdio and on Streamable HTTP for User Story 1 and for at least one
  scenario of User Stories 2, 3 and 4, and MUST show by marker files that a refused call ran
  nothing.

### Key Entities

- **Action**: One thing an agent asked a Rune server to do, as `rune log` shows it: a tool call,
  an agent run, a protocol request or an authentication failure.
- **Record**: One stored line. A tool call and an agent run have two, a start and a completion.
- **Invocation id**: The unique id that joins the start and the completion of one action.
- **Session**: One client connection to one server, with its id, client name and transport.
- **Parent**: The action another action happened inside: an agent run for the calls made
  through its endpoint, a tool call for an agent run started by its task.
- **Outcome**: How an action ended, or `incomplete` when no completion was recorded.
- **Project identity**: The resolved Runefile path, with the digests of the Runefile and
  configuration and the git state at the time of the call.
- **Log directory**: The per-project directory of day files, outside the project.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every call in the acceptance scenarios that ran a task, the log holds exactly
  one action with the task, arguments, exit code, duration, git identity and client name the
  scenario expects.
- **SC-002**: For every attempt in the acceptance scenarios that ran nothing, the log holds
  exactly one action with no exit code, and zero marker files of the task exist.
- **SC-003**: In every scenario where the start cannot be written, zero tasks run.
- **SC-004**: After a server is killed during a call, the action is reported as `incomplete` by
  every output of `rune log`, and by none as `completed`.
- **SC-005**: After 200 concurrent calls across two server processes, 100% of stored lines
  parse, and the log holds 200 actions with 200 distinct invocation ids.
- **SC-006**: In the stored files and in both outputs of `rune log` for the secret scenarios,
  the number of occurrences of each secret fixture is zero.
- **SC-007**: An operator can list one day's actions for a project with one command and no
  flags, and the listing of a day with 10,000 actions is printed in under 2 seconds.
- **SC-008**: Recording adds less than 50 ms to a tool call on a local disk.
- **SC-009**: For a server library embedder that configures no log, tool lists and results are
  byte for byte what they were, and no file is created.
- **SC-010**: A reader can find, from one documentation section, what the log proves, what it
  does not, and how to ship it to storage they control.

## Assumptions

- **The log is outside the project.** The issue asks that agent tasks cannot edit the log
  through their normal writable filesystem. A task's normal writable place is its working
  directory, so the log lives in the user's state directory and a location inside the project
  is refused. A task that runs as the same user can still write to an absolute path until a
  sandbox exists (issue #51). The documentation states this.
- **Configuration is the server's environment, not the Runefile.** The Runefile is editable by
  the agent's tasks. `RUNE_AUDIT_DIR` and `RUNE_AUDIT_RETENTION_DAYS` are read by the server
  when it starts, and a task cannot change the environment of the server that runs it.
- **There is no off switch.** The issue asks that a call whose start cannot be written is
  refused. A host with a read-only home sets `RUNE_AUDIT_DIR`.
- **Only tool calls are gated on the log.** Refusing `initialize` or `tools/list` would make a
  client fail in ways that do not point at the log. Those requests are served and the failure
  is reported to the operator.
- **A completion that cannot be written is reported, not retried.** The agent gets the task's
  result with a line about the log. The action then reads as incomplete, which is true: the log
  does not know how it ended.
- **`incomplete` covers both a running call and a crashed one.** The log alone cannot tell them
  apart, and guessing would invent a state.
- **`timed-out` is an approval wait or a deadline on the call.** Rune has no task timeout
  today. The outcome is recorded when an approval request got no answer in time, and when the
  call's own deadline passed.
- **Retention is 90 days by default**, one file per UTC day. Day files are removed whole, so the
  log is never rewritten and a reader never sees a file change under it.
- **The display timezone is the reader's.** Records are in UTC. `rune log` converts to the zone
  of the machine it runs on unless `--tz` names another.
- **The client name is attribution, not identity.** It is what the client sent in the handshake.
  The HTTP bearer token is one shared secret and identifies no principal.
- **Sandbox state is recorded as not enforced** until issue #51 adds a sandbox. The field is in
  the contract now so that readers need not change later.
- **Git information is best effort and honest.** Rune asks `git` with a short timeout before
  the call runs. A failure is recorded as `unavailable`, never as a clean tree.
- **Protocol garbage is not an action.** A message the protocol layer rejects before it is a
  request of a known method is not recorded. Unknown tools and malformed tool arguments are
  requests and are recorded.
- **A new built-in command takes the name `log`.** Built-in commands already take precedence
  over tasks, and `rune -- <task>` already reaches a task of the same name. The note of FR-036
  keeps the change from being silent, and the release notes call it out.
- **The plan journal stays.** Spec 051's journal keeps its place and format and gains the
  invocation id. The audit log does not replace it.
- Rune is before 1.0. New optional fields on the server library's options do not break an
  embedder that sets fields by name.
