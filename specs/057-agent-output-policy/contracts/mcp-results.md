# Contract: Tool Results with an Output Policy and a Failure Summary

Extends [spec 042](../../042-mcp-output-cap/contracts/mcp-results.md) and
[spec 044](../../044-mcp-structured-results/contracts/mcp-results.md). `L` is the output
limit in force. A task with neither attribute gets exactly the results those contracts
define.

## Text shape

```text
<stdout, kept>
<stderr, kept>
[exit N]
[failure summary - from rune]          only with [agent-summary] and N != 0
...
[fix suggestion - from Runefile || failure hooks]   only when a || hook printed something
...
```

The streams, the line breaks between parts and `[exit N]` follow the text rule of spec 044.
The summary section starts on the line after `[exit N]` and has no final line feed; the
fix suggestion section, when present, follows it as it follows `[exit N]` today.

## A kept stream

With `[agent-output(head="H", tail="T")]` (`H = 0` when `head` is absent), for each of
stdout and stderr on its own:

- A line is the bytes up to and including a line feed. A last line with no line feed is a
  line. A carriage return is part of its line.
- When the stream has at most `H + T` lines and at most `L` bytes: the stream, unchanged.
- Otherwise: the head part, then the omission line, then the tail part.
  - Head part: the first `H` lines, at most `hb = (L - 64) / 2` bytes when `H > 0` (0
    otherwise). When longer, its first `hb` bytes, shortened by at most 3 so no UTF-8
    character is split.
  - Tail part: the last `T` lines that start after the head part, at most `L - 64 - hb`
    bytes. When longer, its last bytes within that size, starting at a character
    boundary.
  - Omission line: `[omitted N lines, M bytes]` and a line feed, with `1 line` and `1 byte`
    in the singular. A line feed precedes it when the head part is not empty and does not
    end in one.
  - `M` is the stream's bytes minus the bytes of the head and tail parts. `N` is the
    stream's lines minus the lines that have at least one byte in the head or tail part.
- The kept stream is at most `L` bytes.
- The same bytes in the same order always give the same kept stream.

Example, `tail="3"`, 2,000 lines `line 1` to `line 2000`:

```text
[omitted 1997 lines, 18863 bytes]
line 1998
line 1999
line 2000
```

Example, `head="2", tail="2"`, lines `a` to `f`:

```text
a
b
[omitted 2 lines, 4 bytes]
e
f
```

## The failure summary section

Present exactly when the called task has `[agent-summary]` and `N` is not 0. Lines, in
order:

| Line | When |
|---|---|
| `[failure summary - from rune]` | always |
| `task: <task>` | always: the task the agent called |
| `failed task: <failedTask>` | when Rune knows which task's body failed |
| `step: <file>:<line>: <command>` | kind `step`: a body line failed |
| `step: <file>:<line>: the task's script` | kind `script`: a `[script]` body failed |
| `step: none, the run stopped before a body line failed` | kind `error` |
| `reason: <reason>` | always |
| `fix suggestion: follows, from the task's \|\| failure hooks` | when the result has a fix suggestion |
| `last lines of stdout:` then the stdout tail | when stdout is not empty |
| `last lines of stderr:` then the stderr tail | when stderr is not empty |

- `<reason>` of kinds `step` and `script` is `exit status N` with the body's status, or the
  executor's error text when there is no status. Of kind `error`, it is the first line of
  Rune's message for the error that ended the run. Of kind `cancelled`, it is
  `the call was cancelled`. Of kind `unknown`, it is
  `not known, Rune has only the exit status`.
- `<file>` is relative to the directory of the Runefile, with forward slashes, or as Rune
  read it when outside that directory.
- A tail is the last up to 5 lines of the kept stream, as the stream holds them (an omission
  line among them is quoted like any line), followed by a line feed when it does not end in
  one.
- The kinds are decided in this order: the call was cancelled; a body line failed; a script
  failed; another error ended the run; no error is known. An executor that fails before it
  runs a body line (a missing working directory, for example) has no Runefile location, so
  its failure is kind `error`, with the failed task when the error names it.

Bounds, applied after masking:

- In `task`, `failedTask`, `file`, `command` and `reason`, each control character other than
  tab becomes a space and each invalid UTF-8 sequence becomes U+FFFD.
- Then each is cut to its size: `task` and `failedTask` 128 bytes, `file` 256, `command`
  256, `reason` 512. A cut field keeps its beginning, at a character boundary, followed by
  ` [cut N bytes]`, and the whole, marker included, is at most the size. `N` counts the bytes
  of the field that are not shown.
- A tail longer than 1,024 bytes keeps its end, starting at a character boundary, preceded by
  the line `[cut N bytes]`, and the whole, marker line included, is at most 1,024 bytes.
- The section is at most 4,096 bytes.

Example:

```text
[exit 1]
[failure summary - from rune]
task: test
failed task: test
step: Runefile:4: go test ./...
reason: exit status 1
fix suggestion: follows, from the task's || failure hooks
last lines of stdout:
--- FAIL: TestParse (0.00s)
    parse_test.go:12: want 2, got 3
FAIL
FAIL	example.com/app	0.004s
FAIL
[fix suggestion - from Runefile || failure hooks]
re-run with -update
```

## Structured content

Two optional properties join the output schema of every tool:

```json
"output": {
  "type": "object",
  "description": "Present when the task keeps only some lines of its output for agents: the lines kept from the start and the end of each stream, and for each stream how many lines and bytes it had and how many are in the result.",
  "properties": {
    "head": {"type": "integer"},
    "tail": {"type": "integer"},
    "stdout": {"$ref": "#/$defs/stream"},
    "stderr": {"$ref": "#/$defs/stream"}
  },
  "required": ["head", "tail", "stdout", "stderr"],
  "additionalProperties": false
},
"failureSummary": {
  "type": "object",
  "description": "Present when a failed run of a task with [agent-summary] has a failure summary: the same facts as the summary section of the text.",
  "properties": {
    "kind": {"type": "string", "enum": ["step", "script", "error", "cancelled", "unknown"]},
    "task": {"type": "string"},
    "failedTask": {"type": "string"},
    "file": {"type": "string"},
    "line": {"type": "integer"},
    "command": {"type": "string"},
    "reason": {"type": "string"},
    "fixSuggestion": {"type": "boolean"},
    "stdoutTail": {"type": "string"},
    "stderrTail": {"type": "string"}
  },
  "required": ["kind", "task", "reason", "fixSuggestion"],
  "additionalProperties": false
}
```

`stream` is `{"lines", "bytes", "keptLines", "keptBytes"}`, all integers, all required, no
other property. The schema is written out in full in each place: no `$ref` is emitted, so a
client that does not resolve references reads it.

Rules:

- `output` is present exactly when the called task has `[agent-output]`. `stdout` and
  `stderr` of the structured content are the kept streams, omission lines included, equal to
  what the text shows.
- `keptBytes` plus the length of the omission line is the length of the kept stream;
  `bytes - keptBytes` and `lines - keptLines` are the `M` and `N` of its omission line.
- `failureSummary` is present exactly when the text has a summary section, and every field
  holds the value its text line shows, without the label. `stdoutTail` and `stderrTail` hold
  the tails without the line feed the text adds.
- `exitCode` and `isError` are what they would be without the attributes.

## Whole result

- Each kept stream is at most `L` bytes, so everything up to and including `[exit N]` stays
  within `2 * L + 32` bytes (spec 042).
- The summary section adds at most one line feed and 4,096 bytes. The fix suggestion keeps its
  8 KiB limit.
- The structured content holds each of these once more, as spec 044 documents.

## Unchanged

- A task with neither attribute: text and structured content byte for byte as before.
- A call that runs no task: an error result with text only.
- `isError` is `exitCode != 0`.
- The audit record of the call.
