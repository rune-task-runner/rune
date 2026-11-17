# Recorded client sessions

These are sessions real agent clients had with `rune mcp`, recorded on the wire. They are
the evidence for the rows marked "recorded" in the
[client compatibility table](../../../../docs/mcp.md#client-compatibility).
`TestClientRecordings` in `test/integration/clients_fixture_test.go` holds each recording
to a case that repeats what the table says about it. The test does not read the table, so
change the two together.

Recorded on 2026-11-15, on Linux x86_64, against `rune` built from commit `c5ee68e`.

| Directory | Contents |
|---|---|
| `recorder/` | The program that records a session. |
| `scenario/` | The Runefile every session ran against. |
| `claude-code/` | Claude Code 2.1.325, run as `claude -p`: recordings and the client's replies. |
| `codex/` | Codex CLI 0.167.0, run as `codex exec`: recordings and the client's replies. |

Cursor, VS Code and Zed have no recording. None of them was installed on the recording
machine, and it had no display to run them on. [Recording a client](#recording-a-client)
gives the configuration to record each of them.

## Recording format

A recording is a JSON Lines file. Each line has `ms`, the time since the recorder started,
and one of:

| Field | Meaning |
|---|---|
| `msg`, with `from` | A JSON-RPC message, verbatim. `from` is `client` or `server`. |
| `raw`, with `from` | A line on stdin or stdout that is not a JSON object. |
| `stderr`, with `from` | A line the server wrote to stderr. |
| `event` | `client closed stdin`, `client sent signal: NAME`, `server exited with code N`, or `server ended without an exit code: signal: NAME` for a server that a signal killed. |

A recording shows what the client and Rune sent each other. It does not show what the
model was given or what it answered. For that, the client's final reply to each session is
kept word for word beside the recording, as `NAME.reply.txt` for `NAME.jsonl`. A statement
about what the model was given, such as the server instructions or a tool name, rests on
that reply. The tables below link each reply and say in short what it reports.

## The scenario

`scenario/Runefile` has one task for each question of the table:

| Task | Question |
|---|---|
| `health`, the `[context]` task | Do the server `instructions` reach the model? They name a project codename that Rune sends nowhere else. It is also written in the Runefile, so it proves nothing for a client that puts workspace files in the model's context. |
| `status` `[readonly]`, `build`, `clean` `[confirm]`, `fetch` `[network]` | Do `readOnlyHint`, `destructiveHint` or `openWorldHint` change what the client approves? |
| `docs-check`, `tools::lint` | Which tool names does the model see, and can it call them? `tools::lint` is the tool `tools__lint`. |
| `grow` | Does the client list the tools again after `notifications/tools/list_changed`? `grow` adds the task `late` to the Runefile. |
| `slow` | When does the client give up on a call, and does it send `notifications/cancelled`? |

The server ran as `rune mcp --allow-destructive clean`, so Rune itself refused nothing. A
call that did not run was stopped by the client. The two `elicitation.jsonl` recordings
are the exception: there the server ran as `rune mcp`, with no grant, so that a call to
`clean` made Rune ask the client for approval.

## The recordings

The main prompt, given to both clients unchanged:

```text
This is a compatibility test of an MCP server named rune. Follow these steps in order. Use no tools other than the rune tools, plus a tool-loading or tool-search tool if you need one to reach them.
1. Before you call any tool: does anything already in your context state a "project codename"? Answer "CODENAME: <value>" and say where in your context it appears, or "CODENAME: NONE".
2. List the exact full names of every rune tool available to you now, as you would write them in a tool call.
3. Call these rune tools one at a time: status, build, clean, fetch, docs-check, and the lint tool of the tools module. For each, write one line: the full tool name, then RAN with its output, or BLOCKED with the reason you were given.
4. Call grow. Then call slow with seconds set to 4.
5. List again the exact full names of every rune tool available to you now. If there is now a tool for a task named late, call it and report its output. If there is not, write "LATE: NOT AVAILABLE".
Report the results as a numbered list, one item per step.
```

The elicitation prompt, given to both clients unchanged, for the two `elicitation.jsonl`
recordings:

```text
This is a compatibility test of an MCP server named rune. Use no tools other than the rune tools, plus a tool-loading or tool-search tool if you need one to reach them. Call the rune tool clean exactly once. Then report: the full tool name; RAN or BLOCKED; and, word for word, the complete text the tool call returned. Do not call it a second time.
```

### Claude Code 2.1.325

Every session ran in a copy of `scenario/` as
`claude -p PROMPT --setting-sources "" --strict-mcp-config --mcp-config mcp.json`, with the
flags in the table added. `mcp.json` is the file shown under
[Recording a client](#recording-a-client). The model was `claude-opus-5-5`.

| Recording | Added flags and prompt | What the client reported, in short |
|---|---|---|
| `session.jsonl`, [reply](claude-code/session.reply.txt) | `--allowedTools mcp__rune`, the main prompt | "CODENAME: heliotrope-41. It appears in the MCP server instructions for `rune`". The tools were `mcp__rune__build`, `mcp__rune__clean`, `mcp__rune__docs-check`, `mcp__rune__fetch`, `mcp__rune__grow`, `mcp__rune__slow`, `mcp__rune__status` and `mcp__rune__tools__lint`. All six calls of step 3 ran. After `grow`, `mcp__rune__late` was listed and returned `late-ok`. |
| `approval.jsonl`, [reply](claude-code/approval.reply.txt) | No added flag, so no tool was approved. The prompt asked for `status`, `build` and `clean`. | All three were blocked with the same reason: "Claude requested permissions to use mcp__rune__status, but you haven't granted it yet." No call reached Rune. |
| `timeout.jsonl`, [reply](claude-code/timeout.reply.txt) | `--allowedTools mcp__rune`, with `MCP_TOOL_TIMEOUT=5000` in the environment. The prompt asked for `slow` with `seconds` set to 30. | `MCP server "rune" tool "slow" timed out after 5s` |
| `elicitation.jsonl`, [reply](claude-code/elicitation.reply.txt) | Recorded on 2026-11-17 with Claude Code 2.1.326, against the branch of spec 049. `--allowedTools mcp__rune`, and the server run as `rune mcp` with no grant. The elicitation prompt below. | "BLOCKED", with the text `task "clean" was not approved: the request was dismissed. Nothing ran.` and the sentence naming `--allow-destructive` and `rune clean`. The recording shows Rune sent `elicitation/create` 1 ms after the call and Claude Code answered `{"action":"cancel"}` 9 ms later. |
| `project-config.jsonl`, [reply](claude-code/project-config.reply.txt) | `--setting-sources project --allowedTools mcp__rune`, without `--strict-mcp-config` and `--mcp-config`. The same server entry was in `.mcp.json` in the working directory. The prompt asked for `status`. | `status` returned `status-ok`. The recording shows the server was started and called with nothing asking to approve it. The client's start-up event, which is not kept, listed the server `rune` as connected with the source `project`. |

Also in these recordings:

- Before `initialize`, Claude Code sends a `server/discover` request for protocol revision
  `2026-07-28`. Rune answers with an error, and Claude Code then sends `initialize` for
  `2025-11-25`.
- Claude Code lists the tools again 7 ms after `notifications/tools/list_changed`.
- On a call timeout it sends `notifications/cancelled` with the reason
  `SdkError: Request timed out`, 5.05 s after the call. Rune stops the task and answers the
  call with `[exit 130]` 2 ms later.
- It ends a session by sending the server an interrupt signal, without closing stdin
  first. Rune exits with code 2 and `rune: context canceled`.
- It states `"elicitation":{"form":{},"url":{}}` in `initialize`. Run as `claude -p`, it
  answers a form request with `cancel` by itself (`elicitation.jsonl`).

### Codex CLI 0.167.0

Every session ran as
`codex exec --ephemeral --skip-git-repo-check --ignore-user-config -C DIR PROMPT`, with the
server given as `-c 'mcp_servers.rune.command="..."'` and `-c 'mcp_servers.rune.args=[...]'`,
which are the keys of a `[mcp_servers.rune]` table. `codex exec` cannot ask for approval,
so a call that needs approval is refused. The model was `gpt-6.1-sol`.

| Recording | Added flags and prompt | What the client reported, in short |
|---|---|---|
| `session.jsonl`, [reply](codex/session.reply.txt) | The main prompt | "CODENAME: heliotrope-41. Present before tool calls in the developer context's `mcp__rune` namespace description." The tools were `mcp__rune__build`, `mcp__rune__clean`, `mcp__rune__docs_check`, `mcp__rune__fetch`, `mcp__rune__grow`, `mcp__rune__slow`, `mcp__rune__status` and `mcp__rune__tools__lint`. `status`, `build`, `docs_check` and `tools__lint` ran. `clean` and `fetch` were blocked: "MCP tool call requires approval, but approval policy is never". After `grow` the list was unchanged: "LATE: NOT AVAILABLE". |
| `approval-mode.jsonl`, [reply](codex/approval-mode.reply.txt) | `-c 'mcp_servers.rune.default_tools_approval_mode="approve"'`. The prompt asked for `clean` and `fetch`. | Both ran: `clean-ok` and `fetch-ok`. |
| `elicitation.jsonl`, [reply](codex/elicitation.reply.txt) | Recorded on 2026-11-17, against the branch of spec 049. `-c 'mcp_servers.rune.default_tools_approval_mode="approve"'`, so that the call reached Rune, and the server run as `rune mcp` with no grant. The elicitation prompt below. | "BLOCKED", with the text `task "clean" was not approved: the request was declined. Nothing ran.` and the sentence naming `--allow-destructive` and `rune clean`. The recording shows Rune sent `elicitation/create` 1 ms after the call and Codex answered `{"action":"decline"}` 1 ms later. |
| `list-changed-flag.jsonl`, [reply](codex/list-changed-flag.reply.txt) | `--enable mcp_tool_list_changed`. The prompt asked for `grow`, then `slow` with `seconds` set to 4, then `late` if it existed. | `mcp__rune__late` was listed and returned `late-ok`. |
| `timeout.jsonl`, [reply](codex/timeout.reply.txt) | The prompt asked for `slow` with `seconds` set to 400. | "tool call failed for `rune/slow`. Caused by: timed out awaiting tools/call after 300s" |
| `project-config.jsonl`, [reply](codex/project-config.reply.txt) | Without `--ignore-user-config` and the `-c` server keys. The server was a `[mcp_servers.rune]` table in `.codex/config.toml` in the working directory, inside a project the user configuration trusts. The prompt asked for `status`. | `status-ok` |

Also in these recordings and runs:

- Codex shows the model `docs-check` as `mcp__rune__docs_check`, and calls Rune with the
  real name `docs-check`.
- By default it does nothing on `notifications/tools/list_changed`. With the
  `mcp_tool_list_changed` feature it lists the tools again before the model's next request,
  5.7 s after the notification in `list-changed-flag.jsonl`.
- On a call timeout it sends `notifications/cancelled` with the reason
  `MCP request cancelled by caller`, 300.0 s after the call. Rune answers the call with
  `[exit 130]` 2 ms later.
- It ends a session by closing stdin and sending a terminate signal within 1 ms.
- It states `"elicitation":{"form":{},"url":{}}` in `initialize`, on revision 2025-06-18.
  Run as `codex exec`, it answers a form request with `decline` by itself
  (`elicitation.jsonl`). Rune sends `"mode":"form"` in the request, a field that revision
  does not have, and Codex took the request all the same.
- Every call carries `_meta` with the session, the model and the sandbox mode. In a Git
  repository it also carries the workspace path, the remote URL and the commit.
- The same `.codex/config.toml` in a directory that is not a trusted project was ignored
  in three runs, which made no recording. Trust cannot be given with `-c`: Codex reports
  `projects` as an unrecognized setting there.

## Edits

A path and three kinds of identifier were replaced in the recordings after they were
made. They name the machine and the vendor sessions that made the recordings, and show
nothing about the client:

| Replaced | With |
|---|---|
| The workspace path Codex sent, in `codex/project-config.jsonl` | `/work/rune` |
| Every UUID: the Codex session, thread, turn and window identifiers, and the UUID in each `callId` | `00000000-0000-0000-0000-000000000001`, `...0002` and so on |
| Every Claude Code tool-use identifier (`claudecode/toolUseId`) | `toolu_0001`, `toolu_0002` and so on |
| Every Codex item identifier (`itemId`) | `ctc_0001`, `ctc_0002` and so on |

Within one file the same identifier always has the same placeholder, so a recording still
shows which messages carried the same one. Everything else is what the recorder wrote,
including the rest of `_meta`: the client name and version, the protocol revision, progress
tokens, the model, the sandbox mode, the time a turn started, and the remote URL and commit
of this public repository. The reply files needed no replacement.
`TestClientRecordingsHoldNoPrivateData` rejects a recording or a reply with a home path or
an identifier that is not a placeholder.

The recordings were made before the recorder kept each message byte for byte. That
version wrote a message without the spaces between its parts, and wrote `<`, `>` and `&`
as escapes. No message in these recordings has such an escape, so they differ from what
the clients and Rune sent only in those spaces, if the messages had any.

## Recording a client

Record on Linux or macOS: the recorder cannot pass a signal on to the server on Windows.
Build the two programs, and copy the scenario to a scratch directory, because `grow`
edits the Runefile:

```sh
go build -o /tmp/rune-rec/rune ./cmd/rune
go build -o /tmp/rune-rec/recorder ./test/integration/testdata/clients/recorder
cp -r test/integration/testdata/clients/scenario /tmp/rune-rec/project
```

Use a new copy for each session. In a copy where `grow` already ran, `late` is a tool from
the start, and the next `grow` adds a second `late`, which Rune rejects as a duplicate
task: the reload fails and no `notifications/tools/list_changed` is sent.

Then configure the client to start the recorder in place of `rune`. The recorder appends
to the `-out` file and runs the command that follows. Give each session its own `-out`
file. A second session, or a client that starts the server twice, is appended to the same
file with `ms` starting again at 0, and `TestClientRecordings` rejects such a file.

Claude Code, in `.mcp.json` at the project root. Cursor takes the same file as
`.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "rune": {
      "type": "stdio",
      "command": "/tmp/rune-rec/recorder",
      "args": ["-out", "/tmp/rune-rec/session.jsonl", "/tmp/rune-rec/rune", "mcp", "--allow-destructive", "clean"]
    }
  }
}
```

Codex, in `~/.codex/config.toml`, or `.codex/config.toml` in a trusted project:

```toml
[mcp_servers.rune]
command = "/tmp/rune-rec/recorder"
args = ["-out", "/tmp/rune-rec/session.jsonl", "/tmp/rune-rec/rune", "mcp", "--allow-destructive", "clean"]
```

VS Code, in `.vscode/mcp.json`:

```json
{
  "servers": {
    "rune": {
      "type": "stdio",
      "command": "/tmp/rune-rec/recorder",
      "args": ["-out", "/tmp/rune-rec/session.jsonl", "/tmp/rune-rec/rune", "mcp", "--allow-destructive", "clean"]
    }
  }
}
```

Zed, in `.zed/settings.json` or `~/.config/zed/settings.json`:

```json
{
  "context_servers": {
    "rune": {
      "command": "/tmp/rune-rec/recorder",
      "args": ["-out", "/tmp/rune-rec/session.jsonl", "/tmp/rune-rec/rune", "mcp", "--allow-destructive", "clean"]
    }
  }
}
```

Open the scratch directory in the client, give it the main prompt, and end the session.
Put the recording in a directory named after the client, and the client's final reply
beside it as `NAME.reply.txt`, word for word. Replace every path that names your machine
and every session identifier as under [Edits](#edits). Then add a case for the recording
to `TestClientRecordings` and a row to the tables above.
