# Data Model: Agent Client Compatibility Matrix

No product data changes. These are the things the research is made of.

## Client

| Field | Values |
|---|---|
| name | Claude Code, Codex CLI, Cursor, VS Code Copilot agent mode, Zed |
| version | the version the statements are about; for Cursor, the version named by each source |
| tested | run with a recording (Claude Code, Codex CLI), or not tested (Cursor, VS Code, Zed) |
| mode | how it was run: `claude -p`, `codex exec` |

## Question

One per row of the table (FR-002): project configuration, user configuration, instructions
reach the model, hints change approval, name of `tools::lint`, name of `docs-check`,
`list_changed` honored, default call limit, cancellation at the limit, cancellation on a
user stop.

## Statement

An answer to a question for a client. It has a text and exactly one evidence label, or says
"not tested".

| Label | Meaning | Rule |
|---|---|---|
| recorded | seen in a session that has a recording in the repository: in the recording, or in the reply kept beside it | FR-008: never for a run that left no recording |
| source | read in the client's source code, not run | names repository and commit in the client's part |
| docs | stated in the vendor's documentation | linked in the client's part |
| staff | stated by the vendor's staff on the vendor's forum | linked, with the version of the thread |
| unknown | no evidence found | the cell says so; it is never left empty (FR-003) |

## Recording

A JSON Lines file, one session. Format in
[contracts/recording-format.md](contracts/recording-format.md).

| Field | Rule |
|---|---|
| path | `test/integration/testdata/clients/<client>/<question>.jsonl` |
| entries | time never runs backwards; one kind of content per entry |
| `initialize` | the client sends one, carrying its name and version; it need not be the first message |
| reply | `<recording>.reply.txt` beside it holds the client's reply to the session, word for word |
| last entry | how the server ended |
| content | no machine path, no user name, no vendor session identifier (FR-017) |

## Scenario

`test/integration/testdata/clients/scenario/`: `Runefile` and `tools.rune`. One task per
question; see research R4. Sessions run in a copy, one copy per session, because `grow`
edits the Runefile.

## Case

One entry of the table in `TestClientRecordings` per recording.

| Field | Meaning |
|---|---|
| file | the recording |
| client, version | what `initialize` must carry |
| called | every `tools/call` that reached Rune, in order |
| relists | whether a `tools/list` follows `notifications/tools/list_changed`; a recording that called `grow` must hold that notification |
| cancelAfter | the client's call limit in seconds: `notifications/cancelled` comes at that limit, less than 500 ms from it, or none |

## Relations

- A statement labelled recorded is shown by one or more recordings of that client.
- Every recording has exactly one case, one reply file, and one row in the README.
- The table, the README rows and the cases state the same facts and change together.
