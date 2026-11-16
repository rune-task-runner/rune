# Contract: the recorder and the recording file

Neither is part of the product. They are the interface between whoever records a client
and the tests that read the result.

## The recorder

```text
recorder -out FILE COMMAND [ARG...]
```

- Starts `COMMAND` with its arguments, in the recorder's working directory.
- Passes its own stdin to the command, and the command's stdout and stderr to its own,
  line by line and unchanged.
- Appends one entry per line to `FILE`, which it creates when missing.
- Passes an interrupt or terminate signal on to the command.
- Exits with the command's exit code.

| Situation | Stderr | Exit code |
|---|---|---|
| no `-out`, or no command | `usage: recorder -out FILE COMMAND [ARG...]` | 2 |
| `FILE` cannot be opened, or the command cannot be started | `recorder: <reason>` | 1 |
| the command was killed by a signal | nothing added | 1 |
| otherwise | nothing added | the command's |

Limits, none of which the recorded clients reached:

- Only interrupt and terminate are recorded and passed on. Another signal, such as hangup,
  ends the recorder without an entry.
- A client that closes its end of stdout without a signal ends the recorder on its next
  write, without an exit entry.
- The exit entry is written when the server's stdout and stderr have closed. A process the
  server started that keeps them open delays it.
- Passing a signal on is not supported on Windows. Record on Linux or macOS.

## The recording

JSON Lines, UTF-8, one entry per line. Every entry has `ms` and exactly one of `msg`, `raw`,
`stderr` and `event`.

| Field | Type | Meaning |
|---|---|---|
| `ms` | integer | Milliseconds since the recorder started. Never lower than on the line above. |
| `from` | `"client"` or `"server"` | Who sent the line. Present with `msg`, `raw` and `stderr`. |
| `msg` | JSON object | A line that is a JSON object, byte for byte as it was sent. Always the last field. |
| `raw` | string | A line on stdin or stdout that is not a JSON object. |
| `stderr` | string | A line the server wrote to stderr. |
| `event` | string | One of the events below. No `from`. |

Events:

| Text | When |
|---|---|
| `client closed stdin` | The client's input ended. Not written when the server stopped reading first. |
| `client sent signal: NAME` | The recorder received an interrupt or terminate signal. |
| `server exited with code N` | The command ended with exit code N. Always the last entry of such a session. |
| `server ended without an exit code: signal: NAME` | A signal killed the command. The last entry. |

Order:

- A line is recorded before it is passed on, so a reply is never above the message it
  answers.
- Entries from the three streams are in the order the recorder read them.

Example:

```json
{"ms":1,"from":"client","msg":{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}}
{"ms":13,"from":"server","stderr":"rune MCP server on stdio"}
{"ms":14,"from":"server","msg":{"jsonrpc":"2.0","id":0,"result":{}}}
{"ms":4003,"event":"client closed stdin"}
{"ms":4006,"event":"server exited with code 0"}
```

## A committed recording

- Holds one session. A file with two sessions has `ms` going back to 0 and is rejected.
- Holds an `initialize` request from the client that carries `clientInfo.name` and
  `clientInfo.version`. It need not be the first message.
- Ends with `server exited with code N`.
- Holds no path of the recording machine (`/home/`, `/root/`, `/Users/`, `\Users\`), no user name,
  and no vendor session identifier.
  Identifiers are replaced with numbered placeholders, the same identifier with the same
  placeholder within a file:

  | Identifier | Placeholder |
  |---|---|
  | a UUID, alone or inside a longer value | `00000000-0000-0000-0000-000000000001`, `...0002`, and so on |
  | `toolu_` and what follows | `toolu_0001`, `toolu_0002`, and so on |
  | `ctc_` and what follows | `ctc_0001`, `ctc_0002`, and so on |
  | the workspace path | `/work/rune` |

- Has a case in `TestClientRecordings` and a row in the README.
- Has a reply file beside it, `NAME.reply.txt` for `NAME.jsonl`: the client's final reply
  to the session, word for word, under the same rules about paths and identifiers. It is
  the evidence for what the model was given, which is not on the wire.
