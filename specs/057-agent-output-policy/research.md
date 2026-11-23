# Research: Per-Task Output Tails and Failure Summaries for Agents

Decisions R1 to R14. Each states what was chosen, why, and what was rejected.

## R1. Syntax: two attributes with named string arguments

**Decision**: `[agent-output(tail="T")]`, `[agent-output(head="H", tail="T")]` and a bare
`[agent-summary]`. The parser reads `agent-output` with `parseNamedStringArgs`, as it reads
`[agent-allow]` and `[plan]`, and `agent-summary` with the attributes that take no
arguments.

**Rationale**: No new syntax (Principle III). The values are strings like
`[plan(expires="1h")]`. Two attributes make the two choices independent by construction
(spec FR-003). The `agent-` prefix says, like `[agent-allow]`, that only agents are
affected.

**Rejected**: one attribute with a `summary="on"` argument (a key with one possible value);
bare numbers `tail=50` (the attribute grammar has no number literal, so the lexer and the
parser would change for one attribute); a setting (`set agent_output_tail := ...`) (the
issue asks for per-task).

## R2. Validation: one new diagnostic code, RUNE2021

**Decision**: `CodeInvalidAgentOutput = "RUNE2021"`, in a new `internal/analyzer/agentoutput.go`.
Errors: an unknown argument (the message lists `head, tail`), a repeated argument, no
`tail`, a value that is not 1 to 5 decimal digits with a value from 1 to 10,000, a second
`[agent-output]`, a second `[agent-summary]`. The number rule lives in
`ast.AgentOutputLines(text) (int, error)`, used by the analyzer and by the engine, as
`ast.PlanExpiry` is.

**Rationale**: One code per attribute family, as RUNE2019 and RUNE2020. The engine reads the
values through the same function, so it cannot accept what the analyzer rejects.

**Rejected**: a warning for a policy on a private task. It has no effect, and nothing else
in the language warns about an attribute that has no effect on a private task.

## R3. Where lines are kept: at capture, in a new `mcpserver.LineBuffer`

**Decision**: The engine captures the streams of a task with an output policy in a
`LineBuffer` instead of an `OutputBuffer`. It is exported from `mcpserver` beside
`OutputBuffer`, so an embedder can use it too. A task without a policy keeps the
`OutputBuffer` path unchanged.

**Rationale**: Spec FR-011 requires bounded memory while the task runs. Reducing to lines
after capture would need the whole stream, and the `OutputBuffer` result has already lost
the line structure of the middle. Keeping the old path for tasks without a policy makes
FR-031 hold by construction.

**Rejected**: line retention inside `capResult` in the server (cannot be bounded); a
second pass over an `OutputBuffer` (it drops the bytes the tail lines can need).

## R4. The `LineBuffer` algorithm

**Decision**: With limit `L`, the omission line reserve `R = 64` (the longest omission line:
a line feed, `[omitted `, 19 digits, ` lines, `, 19 digits, ` bytes]`, a line feed; the
counts are signed 64-bit), and
`hb = (L - R) / 2` when `head` is set, else `0`:

- Memory: the first `hb` bytes written (the head region), and a ring of the last `L - hb`
  bytes. At most `L` bytes in all, allocated as needed, never more.
- Counters: total bytes, total line feeds, the last byte written.
- `String()`:
  1. `lines` = line feeds, plus 1 when the stream is not empty and does not end in a line
     feed.
  2. When `lines <= head + tail` and `total <= L`: the stream whole. The head region and the
     ring hold all of it.
  3. Otherwise the head part is the first `head` lines, at most `hb` bytes (cut at a
     character boundary when longer), and the tail part is the last `tail` lines that start
     after the head part, at most `L - R - hb` bytes (when longer, the bytes before a cut at a
     character boundary are dropped). The result is the head part, a line feed when the head
     part is not empty and does not end in one, the omission line, and the tail part.
- The result is at most `hb + R + (L - R - hb) = L` bytes.

**Rationale**: The ring is `L - hb` and not `L - R - hb` so that a stream of up to `L` bytes
that needs no omission is returned whole, as FR-008 says. Everything the result can contain
is in the head region or the ring, so memory never passes `L` (FR-011). The rendering reads
only the bytes and the counters, so the split into writes and the timing cannot change it
(FR-012). Fixed regions keep the rule simple: a short head leaves part of its half unused,
which costs nothing but kept bytes.

**Rejected**: adaptive regions that give the tail what the head did not use (the regions
must be sized before the head is known, or memory passes `L`); keeping whole lines only and
dropping a line longer than the budget (a single long line, often the error, would vanish).

## R5. Counting kept lines

**Decision**: A kept line is a line with at least one byte in the result. `keptLines` counts
the lines of the head part and of the tail part, and counts once a line that both parts cut
(a stream that is one long line). `omittedLines = lines - keptLines`,
`omittedBytes = bytes - keptBytes`. The omission line appears exactly when
`omittedBytes > 0`.

**Rationale**: Exact from the counters and the kept bytes, with no guess about lines in the
dropped middle: their line feeds are `total line feeds - line feeds kept`.

## R6. Summary facts come from the run error; the text comes from the server

**Decision**: The engine sets `Result.Failure` to the facts it observed:

| Run error (after `classifyRunErr`) | Kind | Facts |
|---|---|---|
| run context cancelled | `cancelled` | failed task when a `shell.ExecError` or `interp.ExecError` names one; the server sets the reason `the call was cancelled` |
| `*shell.ExecError` with a span | `step` | failed task, file, line, command (`ExecError.Line`), reason (`exit status N`, or the error text) |
| `*interp.ExecError` with a span | `script` | failed task, file, line of the span, reason |
| any other error | `error` | reason: the first line of the error's message |
| no error, exit status not 0 | `unknown` | none; the server sets the reason `not known, Rune has only the exit status` |

The command and the reason are masked by the engine with the call's mask set before they
leave it. The server then bounds every field, adds the last lines of each kept stream and
whether a fix suggestion is present, drops the summary of a result whose exit status is 0,
and renders the text section and the structured object from that one value.

**Rationale**: Only the engine knows the run error. Only the server has the final streams, so
it computes the evidence; one value feeds both representations, so they agree (FR-030), and
an embedder's engine gets the same bounds (FR-036). The file is shown relative to the
directory of the Runefile with forward slashes, as diagnostics are, and as given when it is
outside.

**Rejected**: capturing a snapshot of the streams at the moment of failure (it would need a
flush of the masking writer in the middle of a run, which can leave part of a secret
unmasked); quoting the failing line's own output (output of `[parallel]` dependencies is
mixed into one stream).

## R7. Bounds of the summary

**Decision**: Before rendering, the server replaces each control character other than tab
in `task`, `failedTask`, `file`, `command` and `reason` with a space and each invalid UTF-8
sequence with U+FFFD, then caps them: task names 128 bytes, file 256, command 256, reason
512, each quoted tail 1,024, each size counting its marker. A capped field keeps its
beginning and ends with ` [cut N bytes]`. A capped tail keeps its end and starts with the
line `[cut N bytes]`.
The last 5 lines of each non-empty kept stream are quoted. The section is at most 4 KiB.

**Rationale**: The fields are on one line each, so a line feed inside a command must not
break the text. The sum of the caps plus the fixed labels is under 4 KiB (FR-022), so no cut
of the rendered section is needed and the structured object stays equal to it.

## R8. The summary section and its place in the text

**Decision**: After `[exit N]` and before the fix suggestion section:

```text
[exit 2]
[failure summary - from rune]
task: test
failed task: build
step: Runefile:12: go build ./...
reason: exit status 2
fix suggestion: follows, from the task's || failure hooks
last lines of stdout:
...
last lines of stderr:
...
[fix suggestion - from Runefile || failure hooks]
...
```

The full rules per kind are in [contracts/mcp-results.md](contracts/mcp-results.md).

**Rationale**: The fix suggestion section has no end marker and runs to the end of the text,
so anything after it would read as hook output. The header names its source, as the fix
suggestion header does.

## R9. Structured content and the schema

**Decision**: Two optional properties in the one output schema: `output` (the policy and the
counts per stream) and `failureSummary` (the facts of R6 and R7). Each is a closed object.
`output` is present exactly when `Result.Output` is set; `failureSummary` exactly when the
text has a summary section.

**Rationale**: Spec 044 kept one schema for every tool and said this issue would extend it.
Optional properties keep every earlier result valid. Results of tasks without the attributes
do not carry them, so their structured content is unchanged.

**Rejected**: a schema per tool (every tool's listing would depend on its attributes, and a
reload that changes one attribute would change the tool list).

## R10. Masking

**Decision**: Unchanged for the streams: the masking writer sits before the buffers, so the
`LineBuffer` only ever sees masked bytes and every cut falls in masked text. The command and
the reason are masked by the engine before they reach the server, which caps them after.

**Rationale**: FR-026. Masking before cutting is the order spec 042 uses.

## R11. The policy of the called task

**Decision**: The engine reads `[agent-output]` and `[agent-summary]` of the task the call
names, not of the tasks it reaches.

**Rationale**: One stream holds the output of every task of a run. A policy can apply to the
stream, not to the part a dependency wrote.

## R12. The command line does not change

**Decision**: Only the MCP adapter reads the two attributes. `rune <task>` and every other
command ignore them.

**Rationale**: FR-033 and the issue. A person can scroll; an agent's context is the scarce
resource.

## R13. Documentation

**Decision**: `docs/runefile.md` (both attributes), `docs/GRAMMAR.md`,
`docs/diagnostics.md` (RUNE2021), `docs/mcp.md` (the omission line, the summary, the two
fields, the bound on a result, the noisy test task), the editor grammar
(`editors/vscode/syntaxes/runefile.tmLanguage.json`) and the language server's attribute
list. The sizes in the noisy example are measured with a real run of the built binary
(FR-038).

## R14. Tests

**Decision**:

- `mcpserver`: `LineBuffer` table tests (empty, whole, tail only, head and tail, no final
  line feed, CR LF, lone CR, one long line, Unicode at every cut, invalid UTF-8, exactly
  `head + tail` lines, `total = L`), a property test that any split into writes gives the
  same result, a concurrency test with `-race`, a 50 MB test that checks the capacity never
  passes `L`; summary bounding and rendering; schema and structured content agreement.
- `internal/cli`: the policy and the summary facts through the real engine: a failing body
  line, a dependency that fails, a script, an interpolation error, cancellation, an unknown
  cause, a `||` suggestion, a secret in the output, across a cut, and in the failed command.
- `internal/parser`, `internal/analyzer`, `internal/formatter`, `internal/language`: the
  attributes, every RUNE2021 error, the formatter round trip, completion and hover, the
  corpus and fmt fixtures.
- `test/integration`: the real `rune mcp` binary: a noisy task with both attributes, the
  structured content validated against the listed schema, and a task without the attributes
  whose result is unchanged.
