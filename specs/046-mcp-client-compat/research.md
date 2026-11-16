# Research: Agent Client Compatibility Matrix

The findings themselves are in `docs/mcp.md`, section "Client compatibility", and in
`test/integration/testdata/clients/README.md`. This file records how the research was done
and the choices behind it.

## R1. Record on the wire, with a recorder in front of `rune mcp`

**Decision**: A small program is started by the client in place of `rune`. It starts
`rune mcp`, passes stdin, stdout and stderr through, and appends every line to a JSON Lines
file with its sender and the time since it started.

**Rationale**: What a client sends is the only evidence that does not depend on what a model
says about itself. The wire shows whether a `tools/list` followed
`notifications/tools/list_changed`, whether a refused call ever reached Rune, and how many
milliseconds passed before `notifications/cancelled`. Order and time matter for all three,
so a single file with one clock is needed.

**Alternatives considered**: `tee` on each stream: two files, no common order and no time.
A debug log inside `rune mcp`: changes the product for a research task (FR-022). The
client's own logs: a different format per client, and not available for all.

## R2. The recorder is a Go program under `testdata`

**Decision**: `test/integration/testdata/clients/recorder/main.go`, standard library only,
built by the integration tests with `go build ./test/integration/testdata/clients/recorder`.

**Rationale**: It is built with the toolchain every contributor already has, on every OS.
Under `testdata` it is not part of `go build ./...`, of the released module or of coverage,
which is right for a tool that only makes test data. The tests build it and run it as a
process, so it cannot rot.

**Alternatives considered**: A shell or Python script: no signal forwarding worth trusting,
and a second language to run. A package under `internal/`: ships research tooling in the
module. A `cmd/` binary: implies a supported tool.

**Cost**: `go vet ./...` and CI's linter do not see `testdata`. The recorder is linted by
naming its directory, and a compile error is caught by the tests that build it.

## R3. A message is recorded before it is passed on, byte for byte

**Decision**: `relay` hands a line to the log, then writes it to the peer. The log writes
the message bytes unchanged as the last field of the entry.

**Rationale**: A peer cannot answer a line it has not been given, so recording first means a
reply is never above its message, however fast the server is. Re-encoding a message through
`encoding/json` would drop spaces and escape `<`, `>` and `&`, which is not what was sent.

Both rules were added after the nine recordings were made, when a review found the
opposite order and the re-encoding. The recordings were then checked: no reply is above its
message, and no message holds an escape, so they differ from the wire at most in spaces
between JSON tokens.

## R4. One scenario Runefile, one task per question

**Decision**: `scenario/Runefile` with `health` (`[context]`, prints a codename), `status`
(`[readonly]`), `build`, `clean` (`[confirm]`), `fetch` (`[network]`), `docs-check`, the
module task `tools::lint`, `grow` (appends a task named `late` to the Runefile) and `slow`
(sleeps). The server runs as `rune mcp --allow-destructive clean`.

**Rationale**: One Runefile makes sessions comparable between clients. The codename exists
only in the `[context]` output among what Rune sends, so a model that names it was given the
instructions. With `clean` granted, Rune refuses nothing, so a call that does not reach Rune
was stopped by the client. `grow` makes the session change its own tool list, with no
second actor to time.

**Limit**: The codename is also in the Runefile on disk. A client that puts workspace files
in the model's context could name it without the instructions. Both recorded models said
where they found it; their replies are kept (R11).

## R5. Several sessions per client

**Decision**: A main session (instructions, names, calls, reload), and separate sessions
for approval with nothing pre-approved, a call that outlasts the limit, and project
configuration. Codex has two more: the per-server approval mode and the reload feature
flag.

**Rationale**: The issue asks for "one session fixture per client". One session cannot hold
every answer: a timed-out call says nothing more after it, and approval behavior needs a
session with different permissions. Reading the issue as "at least one" keeps each
recording about one question.

## R6. Clients are run without a terminal

**Decision**: `claude -p` and `codex exec`, with isolation flags so the user's own
configuration does not change the result (`--setting-sources ""`, `--strict-mcp-config`;
`--ignore-user-config`, `--ephemeral`).

**Rationale**: The research ran unattended. The cost is stated in the guide: a client that
cannot ask refuses where an interactive session would ask, and stopping a call by hand was
not tested.

## R7. Clients that cannot be run are reported from reading, checked at the source

**Decision**: For Cursor, VS Code and Zed, use vendor documentation, vendor staff replies on
the vendor's forum, and source code. Every statement taken from a first reading was checked
again against the page, the thread or the file itself before it went into the guide. Source
statements name the repository and commit.

**Rationale**: None of the three is installed on the recording machine, and it has no
display. A row of "unknown" for three of five clients would not help an author; a row that
says "read, not run" does, as long as it is labelled.

**Alternatives considered**: Leaving them out: the issue names them. Installing and driving
a GUI editor unattended: changes the machine and still cannot drive agent mode without an
account.

## R8. Evidence labels in every cell

**Decision**: recorded, source, docs, staff, unknown, defined once above the table and
written in each cell.

**Rationale**: The issue asks to distinguish observed behavior from documentation and from
untested cases. A label per cell survives a reader who looks at one row only. The Codex
call limit shows why: its documentation says 60 seconds, its source says 300, and the
recording shows 300.

## R9. Cases repeat the table; they do not read it

**Decision**: `TestClientRecordings` holds, per recording, the expected client, version,
tool calls, reload behavior and cancellation time, written in the test. A recording without
a case fails.

**Rationale**: Parsing a Markdown table from a test couples the test to the guide's wording.
A case that states the same fact fails when a recording is replaced by one that shows
something else, which is the event that must not pass silently. The README says the cases
and the table must be changed together.

## R10. Vendor session identifiers are replaced before commit

**Decision**: In every recording, replace each tool-use identifier (`toolu_...`), each
Codex item identifier (`ctc_...`) and each UUID (session, thread, turn, window and call
identifiers) with a numbered placeholder: `toolu_0001`, `ctc_0001`,
`00000000-0000-0000-0000-000000000001`. Within one file the same identifier always gets the
same placeholder. A test rejects a recording with a home path or an identifier that is not
a placeholder.

**Rationale**: The identifiers name sessions of the account that made the recordings. They
prove nothing about client behavior, and a public repository is not the place for them
(FR-017). Numbering keeps what could matter: whether two messages carry the same
identifier. Timestamps, model names and the public repository's remote and commit stay.

**Alternatives considered**: Committing them as recorded: publishing is not reversible.
Removing `_meta` altogether: hides the fact that the clients send it, which the README
reports.

## R11. The client's reply is kept beside the recording

**Decision**: For each recording `NAME.jsonl`, the client's final reply to that session is
kept word for word in `NAME.reply.txt`, with identifiers and paths replaced as in R10. The
test requires the file.

**Rationale**: Two rows of the table are about what the model was given: whether the
instructions reached it, and which tool names it sees. Neither is on the wire. The only
evidence is what the client said when asked, so that text must be in the repository for
the label "recorded" to mean something a reader can check (FR-008). A summary in the README
is the author's reading of the reply, not the reply.

**Alternatives considered**: Quoting replies only in the README: summaries drift and cannot
be told from quotes. Keeping the client's whole event log: large, different per client, and
full of identifiers and paths.
