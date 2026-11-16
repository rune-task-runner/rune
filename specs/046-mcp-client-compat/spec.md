# Feature Specification: Agent Client Compatibility Matrix

**Feature Branch**: `046-mcp-client-compat`

**Created**: 2026-11-16

**Status**: Draft

**Input**: GitHub issue [#45](https://github.com/rune-task-runner/rune/issues/45)
(backlog MCP-10): "Research agent client compatibility matrix"

## Overview

`rune mcp` sends every client the same things: the output of the `[context]`
task as the server instructions, three hints on every tool, tool names such
as `tools__lint` for a module task, a `notifications/tools/list_changed`
message when the Runefile changes, and an `[exit 130]` answer to a call the
client cancels. What an agent then sees and does depends on the client. One
client hands the instructions to the model and another drops them. One asks
for approval because of a hint and another ignores every hint. One waits a
day for a call and another gives up after a minute.

Nobody has written down which client does what. A Runefile author cannot
tell whether a `[context]` task is worth writing for the client the team
uses, why a `[network]` task is refused there, or how long a task may run.

This feature is research with three results:

- a compatibility table in `docs/mcp.md` for Claude Code, Codex CLI, Cursor,
  VS Code Copilot agent mode and Zed;
- the evidence for it: a recorded session per client where a session can be
  recorded, and for every statement the kind of evidence behind it;
- the means to repeat it: the program that records a session, the Runefile
  the sessions ran against, and the steps to record a client.

### Out of scope

- Changing what `rune mcp` sends, or anything else Rune does. No production
  code changes.
- Working around a client's behavior in Rune, for example sending the
  `[context]` output a second way for a client that drops instructions.
- Clients other than the five named, and the HTTP transport.
- Recording a client that is not installed where the research is done, or
  that needs a display the machine does not have. Such a client is reported
  from reading, and marked as not tested.
- A pending end-to-end test. The issue specifies none for MCP-10.
- Keeping the table current as clients release new versions. It is dated.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A Runefile author learns what a client does with Rune's tools (Priority: P1)

A team uses one of the five clients. Its Runefile author opens the MCP guide
and finds, for that client: where the server is configured, whether the
`[context]` output reaches the model, whether the tool hints change what the
client asks approval for, what name the model sees for a module task and for
a task with a hyphen, whether the client notices a changed tool list, and
how long a call may take. The author also finds what follows from it for a
Runefile, in plain rules.

**Why this priority**: This is the deliverable the issue names first, and
the only one most readers will use.

**Independent Test**: Read the "Client compatibility" section of the MCP
guide. For each of the five clients and each of the six questions of the
issue there is an answer, or a plain statement that the answer is unknown or
was not tested.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader looks up any of the five
   clients, **Then** the guide gives the client's project and user
   configuration files and a configuration that starts `rune mcp`.
2. **Given** the table, **When** a reader looks up whether the server
   instructions reach the model, whether hints change approval, the tool
   names the model sees, whether `list_changed` is honored, the default call
   limit and whether cancellation is sent, **Then** each of the five clients
   has a cell for each, and no cell is empty.
3. **Given** the guide, **When** an author asks what to do about a
   difference, **Then** a list of rules for a Runefile says so: for example
   that a client drops the `[context]` output, or that a client refuses a
   `[network]` task until it is approved.
4. **Given** a statement that concerns one version of a client, **When** a
   reader reads it, **Then** the version is named, and the section gives the
   date of the research.

---

### User Story 2 - A reader can tell what was seen from what was read (Priority: P1)

A maintainer deciding how MCP-7 (reload) or the hints should work needs to
know how far to trust a cell. Every statement names its evidence: seen in a
recorded session, read in the client's source code, stated in the vendor's
documentation, stated by the vendor's staff, or unknown. A client that was
not run is said to be not tested. A recorded statement can be traced to the
recording that shows it.

**Why this priority**: The issue asks for it outright: distinguish observed
behavior from documentation and from untested cases. A table without it
would be read as all observed.

**Independent Test**: Pick any statement marked as recorded. The recordings'
README names the file, the command and the prompt that made it. The file
holds the messages that show the statement, or, for a statement about what
the model was given, the reply kept beside the file says it.

**Acceptance Scenarios**:

1. **Given** any cell of the table, **When** a reader reads it, **Then** it
   carries one of the evidence labels, or says "unknown" or "not tested".
2. **Given** a client that was not run, **When** a reader opens its section,
   **Then** the section says so first, and names the documentation and the
   source files or forum threads its statements come from.
3. **Given** a statement marked as recorded, **When** a reader opens the
   recordings, **Then** a recording of that client shows it, and the README
   gives the client version, the command and the prompt, and the client's
   reply to that session is in a file beside the recording.
4. **Given** a client's documentation and its behavior disagree, **When**
   the guide reports the point, **Then** it gives both and says which one
   was seen.

---

### User Story 3 - A contributor records another client (Priority: P2)

A contributor has Cursor, VS Code or Zed installed, or a newer version of a
recorded client. They build the recorder, copy the scenario Runefile,
configure the client to start the recorder in place of `rune`, give the
client the prompt, and get a recording in the same format as the existing
ones. A test tells them when a recording is malformed or has no case.

**Why this priority**: Three of the five clients have no recording, and
every recorded one will change. The research is only repeatable if the tools
that made it are in the repository and work.

**Independent Test**: Follow the README's steps with any MCP client, or with
a scripted one. The recorder passes the session through unchanged and writes
a recording that the test accepts.

**Acceptance Scenarios**:

1. **Given** the repository, **When** a contributor follows the README,
   **Then** they have a recorder, a scenario project and a client
   configuration for each of the five clients.
2. **Given** a session through the recorder, **When** it ends, **Then** the
   recording holds every message each side sent, in the order they were
   sent, with the sender and the time, the server's stderr lines, the moment
   the client closed stdin or sent a signal, and how the server ended.
3. **Given** a recording added to the fixtures without a test case,
   **When** the tests run, **Then** they fail and name the file.
4. **Given** an existing recording, **When** the tests run, **Then** they
   check it against a case that repeats what the table says was recorded:
   the client and version, which tool calls reached Rune, whether the client
   listed the tools again, and when it cancelled a call. The tests do not
   read the table.

---

### Edge Cases

- A client is not installed, or cannot run without a display. It has no
  recording, and the README says why.
- What the model was told and what it answered is not on the wire. The
  client's reply to each session is kept, word for word, in a file beside
  the recording.
- A recorded statement holds only for the mode that was run. A client run
  without a terminal cannot ask for approval, so it refuses where an
  interactive session would ask. The guide names the mode and lists what was
  not tested.
- A recording carries data about the machine or the account that made it: a
  path, or an identifier of a vendor session. Such data is replaced before
  the recording is committed, and the README lists every replacement.
- A client's documentation gives one default and the client uses another.
  Both are reported.
- A vendor documents two ways to run a server and only one was researched.
  The guide says which statements cover which.
- The scenario's task that changes the tool list edits the Runefile, so a
  second session in the same copy starts from a changed Runefile. The README
  says to use a new copy for each session.
- A client starts the server twice, so two sessions end up in one recording.
  The README says to give each session its own file, and the test rejects a
  recording in which time runs backwards.
- A server answers so fast that its reply could be written to the recording
  before the message it answers. The recorder records a message before it
  passes it on, so this cannot happen.
- A server is killed by a signal and has no exit code. The recording names
  the signal.
- The server stops reading its input while the client still writes. The
  recording keeps the client's message and does not claim the client closed
  stdin.

## Requirements *(mandatory)*

### Functional Requirements

**The table**

- **FR-001**: The MCP guide MUST have a "Client compatibility" section with
  a table whose columns are Claude Code, Codex CLI, Cursor, VS Code Copilot
  agent mode and Zed.
- **FR-002**: The table MUST answer, for every client: the project
  configuration file; the user configuration file; whether the server
  instructions reach the model; whether tool hints change what the client
  asks approval for; the name the model sees for a module task
  (`tools::lint`) and for a task with a hyphen; whether the client lists the
  tools again after `notifications/tools/list_changed`; the default limit on
  one call; whether `notifications/cancelled` is sent at that limit; and
  whether it is sent when the user stops a call.
- **FR-003**: No cell may be empty. A cell with no evidence MUST say
  "unknown", and a cell for a case that was not run MUST say "not tested".
- **FR-004**: The section MUST name the version of each client its
  statements are about, and the date of the research.
- **FR-005**: The section MUST have a part for each client with a
  configuration that starts `rune mcp`, the details behind the table's
  cells, what was not tested, and its sources.
- **FR-006**: The section MUST list what the differences mean for a
  Runefile, as rules an author can act on.

**Evidence**

- **FR-007**: Every statement in the table and in the client parts MUST
  name its evidence as one of: recorded, source, docs, staff, unknown. The
  section MUST define these labels.
- **FR-008**: "recorded" MUST be used only for behavior seen in a session
  that has a recording in the repository: on the wire, in the recording, or
  in the client's reply to that session, kept beside the recording.
  Behavior seen in a run that left no recording MUST NOT be labelled
  recorded.
- **FR-009**: A client that was not run MUST be reported as not tested, in
  the section's introduction and in its own part.
- **FR-010**: Where a client's documentation and its recorded behavior or
  source disagree, the section MUST report both.

**Recordings**

- **FR-011**: The repository MUST hold, under
  `test/integration/testdata/clients/`, at least one recorded session for
  each client that could be run, in a directory named after the client.
- **FR-012**: The recordings of one client, with the replies kept beside
  them, MUST together show an answer to each question that can be answered
  by running it: instructions, approval, tool names, `list_changed`, and a
  call that outlasts the call limit. The wire shows what was sent; the
  reply shows what the model was given, such as the instructions and the
  tool names.
- **FR-013**: A recording MUST hold every JSON-RPC message of the session
  byte for byte, with its sender and the time since the session started;
  every line the server wrote to stderr; the moment the client closed stdin
  or sent a signal; and the server's exit code, or the signal that killed
  it.
- **FR-014**: A recording MUST show a message above every reply to it.
- **FR-015**: All sessions MUST run against one scenario Runefile kept in
  the repository, with a task for each question.
- **FR-016**: A README beside the recordings MUST give, for each recording,
  the client version, the command and the prompt, and MUST say why a client
  has no recording. The client's reply to each session MUST be kept word
  for word in a file beside the recording, under the same rules as a
  recording (FR-017).
- **FR-017**: A committed recording MUST NOT contain a path of the
  recording machine, a user name, or an identifier of a vendor account or
  session. The README MUST list every change made to a recording after it
  was recorded.

**Repeating the research**

- **FR-018**: The program that records a session MUST be in the repository.
  It MUST pass the client's input, the server's output and the server's
  stderr through unchanged, pass a signal on to the server, and end with the
  server's exit code.
- **FR-019**: The README MUST give the steps to record a client, with a
  configuration for each of the five clients.
- **FR-020**: The tests MUST check every recording against a case that
  repeats what the table reports as recorded for it, and MUST fail for a
  recording that has no case, that has no reply file, or that breaks
  FR-017, and for a reply file that breaks FR-017.
- **FR-021**: The tests MUST cover the recorder: a whole session, text that
  is not JSON, the server's exit code, a signal, wrong usage, the order of a
  message and its reply, a server that stops reading, and a server killed by
  a signal.

**Limits**

- **FR-022**: The feature MUST NOT change the `rune` binary, the MCP server
  library, or any message Rune sends.
- **FR-023**: The documentation harness (`test/docs`) MUST pass with the
  new section: links resolve, and the site pages generated from the guide
  pass their checks.

### Key Entities

- **Client**: one of the five agent programs, with a version and a mode in
  which it was run, or the statement that it was not run.
- **Question**: one thing the issue asks about a client, such as whether
  instructions reach the model.
- **Evidence label**: recorded, source, docs, staff or unknown.
- **Recording**: one session between a client and `rune mcp`, as a file of
  timed entries: messages, stderr lines and events.
- **Reply**: what the client answered at the end of a session, word for
  word, in a file beside the recording.
- **Scenario**: the Runefile the sessions ran against, with one task per
  question.
- **Case**: what the tests expect of one recording.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For each of the five clients, a reader finds an answer to
  each of the six questions of the issue in one section of the MCP guide,
  and every one of those answers names its evidence or says that there is
  none.
- **SC-002**: Every statement marked as recorded can be traced, through the
  README, to a recording that shows it or to the reply kept beside that
  recording. At least two of the five clients have recordings.
- **SC-003**: Every client without a recording is named as not tested where
  a reader first meets the table.
- **SC-004**: Following only the README's steps, a contributor with a
  client installed produces a recording that the tests accept once it has a
  case.
- **SC-005**: A recording that is added without a case, or changed so that
  it no longer matches its case, fails the test suite.
- **SC-006**: No committed recording contains the home directory or user
  name of the recording machine, or a vendor session identifier.
- **SC-007**: The full test suite and the documentation checks pass, and no
  file outside the documentation, the tests and the test data changes.

## Assumptions

- The research was done on 2026-11-15 on one Linux x86_64 machine. Claude
  Code 2.1.325 and Codex CLI 0.167.0 are installed there. Cursor, VS Code
  and Zed are not, and the machine has no display.
- Both recorded clients were run without a terminal session (`claude -p`,
  `codex exec`), because the research ran unattended. Interactive behavior,
  such as an approval dialog or stopping a call by hand, is not tested.
- A session with a client uses a model through the maintainer's own
  accounts. The scenario holds nothing private.
- Cursor is closed source, so its statements come from its documentation
  and from its staff's replies on its forum. Each forum statement is about
  the version named in its thread.
- For VS Code and Zed the source code read is the main branch on the day of
  the research, named by commit. It may be ahead of the released version.
- "One session fixture per client" in the issue is read as at least one.
  One session cannot answer every question: a call that outlasts the limit
  ends a session's usefulness, and approval needs different settings.
- The recorder is a small Go program kept with the test data, so the
  repository's own toolchain builds it and the integration tests run it.
- The existing recordings were made before two corrections to the recorder
  (message bytes and entry order). They were checked for both: no message
  has an escaped character and no reply is above its message.
