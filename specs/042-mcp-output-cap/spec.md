# Feature Specification: Cap Task Output Before It Reaches the Agent

**Feature Branch**: `042-mcp-output-cap`

**Created**: 2026-11-13

**Status**: Draft

**Input**: GitHub issue [#40](https://github.com/rune-task-runner/rune/issues/40)
(backlog MCP-6): "Cap tool output before it reaches the agent"

## Overview

When an agent calls a task through `rune mcp`, the task's whole stdout and
stderr are held in memory and returned in the tool result. Nothing bounds
them. A task that prints 3 MB returns a result of 3,000,008 bytes. That fills
the agent's context with one call, and the server, which lives as long as the
agent session, holds every byte while the task runs.

Two other agent-facing texts already have a limit: the `[context]`
instructions and the `||` fix suggestion are cut at 8 KiB. Task output is the
one text with no limit, and it is the largest.

This feature gives each of stdout and stderr a byte limit per call, 64 KiB by
default. Output within the limit is returned unchanged. Longer output keeps
its beginning and its end, with one marker line between them that says how
much was left out. Shortened here to two lines on each side:

```text
step 1 of 9000
step 2 of 9000
[truncated 95312 bytes]
step 8999 of 9000
step 9000 of 9000
[exit 0]
```

The operator can change the limit for one server with a flag, and a Runefile
author can change it for every server of that Runefile with a setting:

```console
$ rune mcp --output-limit 256KiB
```

```text
set mcp_output_limit := "256KiB"
```

### Out of scope

- Per-task limits, keeping the last N lines, and failure summaries:
  [#55](https://github.com/rune-task-runner/rune/issues/55). That work builds
  on the bounded capture added here.
- Output of `rune` run from the command line. It streams to the terminal and
  is not captured.
- The 8 KiB limit on `[context]` instructions and the `||` fix suggestion.
  Both keep their limit and their `[truncated]` marker.
- Turning the limit off. An unlimited result is the defect this feature
  removes.
- Writing the full output to a file or offering it through another tool.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A large output no longer floods the agent (Priority: P1)

An agent calls `test`, and the test run prints 3 MB of log lines. Today the
agent receives all of it and has no context left for the work. With this
feature the agent receives the first lines, a marker, the last lines, and the
exit status. The start shows what ran, and the end shows how it finished,
which is where a failure is reported.

**Why this priority**: This is the defect the issue reports. One call can end
an agent session.

**Independent Test**: Start the server on a Runefile whose task prints 3 MB to
stdout, call it as an MCP client, and measure the result text. It is at most
64 KiB plus 32 bytes, it starts with the first bytes the task printed, it holds one
`[truncated N bytes]` line, and it ends with the last bytes the task printed
followed by `[exit 0]`.

**Acceptance Scenarios**:

1. **Given** a task that prints 3,000,000 bytes to stdout and a server with
   the default limit, **When** an agent calls the task, **Then** the result
   text is at most 65,568 bytes (the limit plus 32), begins with the first
   bytes of the output,
   holds exactly one line of the form `[truncated N bytes]`, and ends with
   the last bytes of the output followed by the `[exit 0]` line.
2. **Given** the same call, **When** the result is read, **Then** N in the
   marker equals the number of output bytes the result does not show, so the
   bytes shown plus N equal 3,000,000.
3. **Given** a task that prints 3,000,000 bytes to stdout and 3,000,000 bytes
   to stderr, **When** an agent calls it, **Then** the result text is at most
   131,104 bytes (twice the limit plus 32), holds one marker for each stream,
   and ends with the exit line.
4. **Given** a task that prints 3 MB and then fails with status 3, **When** an
   agent calls it, **Then** the result is marked as an error and its last line
   before any fix suggestion is `[exit 3]`.
5. **Given** a task whose output is 60,000 bytes on each stream, **When** an
   agent calls it, **Then** the result is byte for byte what it is today, with
   no marker.
6. **Given** a task that prints 3 MB and whose `||` failure hook prints a fix
   suggestion, **When** the task fails, **Then** the fix suggestion follows the
   exit line as today, with its own 8 KiB limit.

---

### User Story 2 - Secrets stay masked in a truncated result (Priority: P1)

A task prints a secret value in the middle of a long output. Today the value
is masked before the agent sees it. Truncation must not weaken that: a secret
that sits where the output is cut must not come through whole or in part.

**Why this priority**: A cut that exposed part of a secret would turn a
context fix into a leak. Masking is a locked security commitment.

**Independent Test**: Start the server with a secret in the environment and a
task that prints filler, then the secret, then filler, sized so the secret
falls on the edge of the kept beginning, and again so it falls on the edge of
the kept end. Search the result for the secret and for every substring of it
of four bytes or more. None is found.

**Acceptance Scenarios**:

1. **Given** a secret value in the server's environment and a task whose
   output places that value across the end of the kept beginning, **When** an
   agent calls the task, **Then** the result holds no part of the secret value.
2. **Given** the same secret placed across the start of the kept end, **When**
   an agent calls the task, **Then** the result holds no part of the secret
   value.
3. **Given** a secret placed inside the part that is left out, **When** an
   agent calls the task, **Then** the result holds no part of the secret
   value.
4. **Given** a secret placed inside the kept beginning or the kept end,
   **When** an agent calls the task, **Then** the result shows the mask
   placeholder there, as it does today.

---

### User Story 3 - The server's memory stays bounded while a task runs (Priority: P1)

An operator leaves `rune mcp` running for a long agent session. A task that
prints without end, or prints gigabytes before it exits, must not make the
server grow with it. The server keeps only what it will return.

**Why this priority**: The server is long-lived and shared by every call of
the session. Cutting the text only after the task ends would protect the
agent and still let one task exhaust the host.

**Independent Test**: Run a task that prints 200 MB through the server and
measure how much output the capture holds during and after the run. It never
holds more than the limit for each stream plus a small fixed amount.

**Acceptance Scenarios**:

1. **Given** a task that prints 200 MB to stdout, **When** it runs through the
   server, **Then** the memory the server uses to hold that output never
   exceeds the stdout limit plus a fixed overhead that does not depend on the
   output size.
2. **Given** a `[parallel]` task whose dependencies print large outputs at the
   same time, **When** it runs through the server, **Then** the same bound
   holds and the result is well formed.
3. **Given** a task that prints one line of 3 MB with no line break, **When**
   an agent calls it, **Then** the result obeys the same size limit and the
   same memory bound.

---

### User Story 4 - The limit can be changed (Priority: P2)

An operator whose agent has a large context wants more of each output. A
Runefile author whose tasks are noisy wants every agent to get less. Either
sets the limit, and a value that cannot be a limit is rejected before
anything runs.

**Why this priority**: The default fits most sessions. Changing it matters,
and the first three stories deliver the fix without it.

**Independent Test**: Start the server with `--output-limit 1KiB` and call a
task that prints 10,000 bytes. The stdout part of the result is at most 1,024
bytes. Repeat with `set mcp_output_limit := "1KiB"` and no flag. Start with a
malformed value in each place and check the error.

**Acceptance Scenarios**:

1. **Given** a server started with `--output-limit 256KiB` and a task that
   prints 3 MB to stdout, **When** an agent calls it, **Then** the stdout part
   of the result is at most 262,144 bytes and more than 65,536 bytes.
2. **Given** a Runefile with `set mcp_output_limit := "1KiB"` and a server
   started with no flag, **When** an agent calls a task that prints 10,000
   bytes, **Then** the stdout part of the result is at most 1,024 bytes.
3. **Given** a Runefile with `set mcp_output_limit := "1KiB"` and a server
   started with `--output-limit 4KiB`, **When** an agent calls a task that
   prints 10,000 bytes, **Then** the limit in force is 4 KiB. The flag wins.
4. **Given** any Runefile, **When** the operator runs
   `rune mcp --output-limit lots`, `--output-limit ""`, `--output-limit -1`,
   `--output-limit 0`, or `--output-limit 10`, **Then** the server exits with
   status 2 before any Runefile code runs, and the message names the flag, the
   value, and the accepted forms or range.
5. **Given** a Runefile with `set mcp_output_limit := "lots"`, **When**
   `rune build` or `rune mcp` loads it, **Then** it fails before any task runs with a
   diagnostic at the setting's `file:line:col` and a caret under the value,
   naming the accepted forms.
6. **Given** a Runefile with `set mcp_output_limit := "256KiB"`, **When** a
   task is run from the command line, **Then** its output is whole. The
   setting affects only output returned to agents.
7. **Given** `--output-limit 65536`, `--output-limit 64KiB`, and no flag,
   **When** the same task is called on each server, **Then** the three results
   are identical.

---

### Edge Cases

- Output exactly at the limit is returned whole. One byte more is truncated.
- Output a few bytes over the limit: the marker is longer than what it
  replaces. The result still fits the limit, so slightly more than the excess
  is left out and N reports the true count.
- A cut that would fall inside a multi-byte character moves to the character
  boundary, so the result is valid text. N still counts every byte left out.
- Output that is not valid text (binary data) is capped the same way. The cap
  is on bytes.
- Output with no line breaks is capped the same way. The marker still gets its
  own line.
- A task prints text that looks like the marker. It is returned as output and
  is not interpreted.
- Stdout is over the limit and stderr is empty, or the reverse: each stream is
  capped on its own, and only the long one gets a marker.
- The task is cancelled or times out while printing: the result, if one is
  returned, obeys the same limit.
- An `agent` task opens its own endpoint for the agent it starts. Calls on that
  endpoint are capped by the Runefile setting or the default. The
  `--output-limit` flag belongs to `rune mcp` and does not reach it.
- A dependency, a post-hook, and the called task all print: their output is one
  stream per kind, as today, and the limit applies to the combined stream.
- The same limit value given with the flag more than once: the last one wins,
  as for other single-value flags.

## Requirements *(mandatory)*

### Functional Requirements

**Capping**

- **FR-001**: For every tool call, the stdout text and the stderr text returned
  to the agent MUST each be at most the output limit in bytes. The limit
  applies to each stream separately.
- **FR-002**: The default output limit MUST be 64 KiB (65,536 bytes) for each
  stream.
- **FR-003**: A stream whose captured output is at most the limit MUST be
  returned unchanged, byte for byte, with no marker.
- **FR-004**: A stream whose captured output is over the limit MUST be returned
  as its beginning, one marker, and its end, in that order. The beginning and
  the end MUST be contiguous runs of the original output starting at its first
  byte and ending at its last byte.
- **FR-005**: The marker MUST be the text `[truncated N bytes]` on a line of
  its own, where N is the decimal count of that stream's captured bytes that
  the result does not show. Bytes shown plus N MUST equal the bytes captured.
- **FR-006**: The limit MUST count the marker, its line breaks, and the kept
  output together. A truncated stream MUST NOT exceed the limit because of the
  marker.
- **FR-007**: The kept beginning and the kept end MUST each take about half of
  the bytes left after the marker, so neither is empty when the limit is at
  its minimum.
- **FR-008**: A cut MUST NOT split a multi-byte UTF-8 character of valid text.
  The cut moves to the nearest boundary that keeps the stream within the
  limit.
- **FR-009**: The whole result text up to and including the `[exit N]` line
  MUST be at most twice the output limit plus 32 bytes. The 32 bytes cover
  the exit line and the line breaks that join the parts, and do not grow with
  the output.
- **FR-010**: The `[exit N]` line MUST always be present and complete, and MUST
  follow the captured output as it does today. Truncation never removes or
  shortens it.
- **FR-011**: The `||` fix suggestion MUST keep its current place after the
  exit line, its current 8 KiB limit, and its current marker. It is not part
  of the total in FR-009.
- **FR-012**: The error flag of a result MUST depend only on the exit status,
  as today. Truncation never changes it.

**Masking**

- **FR-013**: Secret masking MUST apply to the output before any byte is kept
  or dropped. The result MUST NOT contain a secret value, or a part of one,
  that masking would have removed from the untruncated output.
- **FR-014**: N in the marker counts bytes of the masked output, so it reveals
  nothing about the length of a secret beyond what the untruncated result
  already shows.

**Memory**

- **FR-015**: While a task runs, the memory used to hold each captured stream
  MUST NOT exceed the output limit plus a fixed overhead that is independent
  of how much the task prints.
- **FR-016**: The bound in FR-015 MUST hold when several tasks of one call
  write at the same time, and the capture MUST stay consistent under
  concurrent writes.
- **FR-017**: A task that prints more than the limit MUST NOT fail, block, or
  be stopped for that reason. Its writes succeed and the excess is dropped.

**Configuration**

- **FR-018**: `rune mcp` MUST accept `--output-limit <size>`, which sets the
  output limit for that server, on stdio and on HTTP.
- **FR-019**: A Runefile MUST accept `set mcp_output_limit := "<size>"`, which
  sets the output limit for every agent-facing endpoint served from that
  Runefile.
- **FR-020**: The limit in force MUST be the flag's value when the flag is
  given, otherwise the setting's value when the setting is present, otherwise
  the default.
- **FR-021**: A size MUST be a positive whole number of bytes with an optional
  unit: no unit or `B` for bytes, `KiB` for 1,024 bytes, `MiB` for 1,048,576
  bytes. The number and the unit are written together, as in `256KiB`.
  Leading zeros are allowed. No other form is accepted.
- **FR-022**: A size MUST be at least 1 KiB and at most 1 GiB (1,024 MiB).
  Values outside that range are rejected.
- **FR-023**: An invalid `--output-limit` value MUST stop `rune mcp` with exit
  status 2 before any Runefile code runs, with a message that names the flag,
  the value given, and the accepted forms or range.
- **FR-024**: An invalid `mcp_output_limit` value MUST be reported when the
  Runefile's settings are resolved, by `rune <task>` and by `rune mcp` alike,
  before any task runs, with the setting's
  `file:line:col`, a caret span on the value, and the accepted forms or range.
  A valid value has no effect outside agent-facing endpoints.
- **FR-025**: The setting and the flag MUST be documented with the other
  settings and `rune mcp` flags, and the grammar reference MUST list the new
  setting.

**Shared mechanism**

- **FR-026**: There MUST be one bounded head-and-tail capture mechanism, used
  for both stdout and stderr, that later per-task limits (issue #55) can reuse
  with a different limit. This feature MUST NOT add a second unbounded buffer.
- **FR-027**: The embeddable server library MUST apply the limit to results
  from any engine an embedder supplies, with the same default, and MUST let
  the embedder set the limit. An embedder's engine that returns large output
  gets a capped result; memory use inside that engine is the embedder's
  concern.
- **FR-028**: The release notes MUST state that results over 64 KiB per stream
  are now truncated by default and how to raise the limit.

### Key Entities

- **Output limit**: The largest number of bytes of one stream that a tool
  result may carry, marker included. It has a default, can come from a flag or
  a setting, and is fixed when the server starts.
- **Captured stream**: The stdout or the stderr of one tool call after
  masking. It is either whole or made of a kept beginning, a marker, and a
  kept end.
- **Truncation marker**: The line `[truncated N bytes]` that stands for the
  bytes of a captured stream that the result does not show.
- **Tool result**: The text an agent receives for one call: stdout, stderr,
  the exit line, and an optional fix suggestion.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A task that prints 3 MB on one stream returns a result of at
  most 64 KiB plus 32 bytes with the default limit, down from 3,000,008
  bytes, and the result still shows the first output, the last output, and
  the exit status.
- **SC-002**: A result never exceeds twice the limit in force plus 32 bytes
  (plus the fix suggestion's own 8 KiB), for any output size, on either
  stream or both.
- **SC-003**: Across test outputs that place a secret at every position
  relative to the two cuts, no result contains the secret or any part of it
  of four bytes or more.
- **SC-004**: While a task prints 200 MB, the output the server holds for it
  stays under the limit for each stream plus a fixed overhead of at most
  4 KiB.
- **SC-005**: Every call whose output is within the limit returns exactly the
  result it returned before this feature.
- **SC-006**: An operator can change the limit with one flag or one Runefile
  line, and every invalid value is rejected before any task runs.

## Assumptions

- **Limit per stream, total of twice the limit plus the exit line.** The
  issue asks for 64 KiB each and for at most 128 KiB of result from a 3 MB
  output. Each stream's marker is counted inside its 64 KiB. The exit line
  and the joining line breaks are not: they are at most 32 bytes, and taking
  them from a stream would truncate an output that is exactly at the limit.
  A 3 MB output on one stream gives about 64 KiB. Only when both streams are
  full does the result pass 128 KiB, by at most 32 bytes.
- **N counts bytes left out of the masked stream.** The agent sees masked
  output, so the count is of masked bytes.
- **Even split.** The beginning and the end each get about half of what is
  left after the marker. Issue #55 may change the split per task.
- **Fixed at server start.** The limit does not change while a server runs and
  is the same for every task. Per-task limits are issue #55.
- **Size syntax is small on purpose.** Bytes, `KiB`, and `MiB` cover the
  useful range. Decimal units (`KB`, `MB`), fractions, spaces, and lower-case
  units are not accepted, so one limit has one spelling per unit.
- **Range of 1 KiB to 1 GiB.** The minimum leaves room for the marker and for
  useful text on both sides of it. The maximum keeps a typo from removing the
  bound in practice.
- **The setting is the only way to change the limit on an `agent` task's
  endpoint.** That endpoint is opened by a task run, not by `rune mcp`, so no
  flag reaches it.
- **Behavior change.** Agents that relied on receiving more than 64 KiB per
  stream get truncated output until the operator raises the limit. This is
  called out in the release notes and marked as a breaking change.
- **The pending test named in the issue is absent.** The issue's
  `TestMCPE2E_Pending_OutputCapped` and its suite are not in this repository.
  An equivalent end-to-end test is written as part of this feature.
- **Existing masking is reused.** Masking already happens as output is
  written, before capture. This feature keeps that order.
