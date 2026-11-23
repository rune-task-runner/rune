# Data Model: Per-Task Output Tails and Failure Summaries for Agents

Nothing is stored. Every value lives for one call.

## Output policy (Runefile)

| Attribute | Argument | Rule |
|---|---|---|
| `[agent-output]` | `tail` | required; 1 to 5 decimal digits, value 1 to 10,000 |
| `[agent-output]` | `head` | optional; same rule; absent means 0 |
| `[agent-summary]` | none | |

Read through `ast.AgentOutputLines(text) (int, error)` and `(*ast.Task).AgentOutput()
(head, tail int, ok bool)`. At most one of each attribute per task (RUNE2021).

## `mcpserver.LineBuffer`

One per stream per call of a task with an output policy. Safe for concurrent writers.

| Field | Meaning |
|---|---|
| `limit` | `L`, clamped like `OutputBuffer`'s |
| `head`, `tail` | the policy's line counts |
| `hb` | `(L - 64) / 2` when `head > 0`, else `0` |
| `first` | the first `hb` bytes written |
| `ring` | the last `L - hb` bytes written, as a ring |
| `total` | bytes written, `int64` |
| `feeds` | line feeds written, `int64` |
| `last` | the last byte written |

Invariants:

- `len(first) + cap(ring) <= L` at all times.
- `Write` returns `len(p), nil` always.
- `String()` is at most `L` bytes and depends only on the bytes written in order.

Methods: `Write`, `String`, `Report() StreamReport`.

## `mcpserver.StreamReport`

| Field | JSON | Meaning |
|---|---|---|
| `Lines` | `lines` | lines the stream had (R4) |
| `Bytes` | `bytes` | bytes the stream had, after masking |
| `KeptLines` | `keptLines` | lines with at least one byte in the kept stream |
| `KeptBytes` | `keptBytes` | bytes of the stream in the kept stream, omission line excluded |

`len(kept stream) == KeptBytes + len(omission line)`. The omission line exists exactly when
`KeptBytes < Bytes`.

## `mcpserver.OutputReport`

| Field | JSON | Meaning |
|---|---|---|
| `Head` | `head` | the policy's `head`, 0 when absent |
| `Tail` | `tail` | the policy's `tail` |
| `Stdout` | `stdout` | `StreamReport` |
| `Stderr` | `stderr` | `StreamReport` |

## `mcpserver.FailureSummary`

Set by the engine for a failed call of a task with `[agent-summary]`; bounded and completed
by the server.

| Field | JSON | Set by | Presence |
|---|---|---|---|
| `Kind` | `kind` | engine | always: `step`, `script`, `error`, `cancelled`, `unknown` |
| `Task` | `task` | engine | always: the called task |
| `FailedTask` | `failedTask` | engine | when known |
| `File` | `file` | engine | kinds `step` and `script` |
| `Line` | `line` | engine | kinds `step` and `script` |
| `Command` | `command` | engine | kind `step` |
| `Reason` | `reason` | engine for `step`, `script`, `error`; server for `cancelled` (`the call was cancelled`) and `unknown` (`not known, Rune has only the exit status`) | always |
| `FixSuggestion` | `fixSuggestion` | server | always: whether the result has a fix suggestion |
| `StdoutTail` | `stdoutTail` | server | when stdout is not empty |
| `StderrTail` | `stderrTail` | server | when stderr is not empty |

Bounds, applied by the server after the engine masked: control characters other than tab
become spaces and invalid UTF-8 becomes U+FFFD in the one-line fields; then `Task` and
`FailedTask` 128 bytes, `File` 256, `Command` 256, `Reason` 512, each tail 1,024 and at most
5 lines. See [contracts/mcp-results.md](contracts/mcp-results.md).

## `mcpserver.Result` (extended)

| Field | New | Meaning |
|---|---|---|
| `Stdout`, `Stderr`, `ExitCode`, `FixSuggestion` | no | as spec 044 |
| `Output` | yes | `*OutputReport`; nil when the task has no output policy |
| `Failure` | yes | `*FailureSummary`; nil when there is no summary |

## The kept result

The handler builds one value from the engine's `Result`: streams capped (spec 042), the
summary dropped when the exit status is 0, bounded and completed. The text and the structured
content are both rendered from it.

## States of a call's summary

```text
task without [agent-summary] ──────────────────────────────> no summary
task with [agent-summary] ── exit 0 ───────────────────────> no summary
                          └─ exit not 0 ── run error kind ─> summary of that kind
```
