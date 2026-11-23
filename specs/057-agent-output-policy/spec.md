# Feature Specification: Per-Task Output Tails and Failure Summaries for Agents

**Feature Branch**: `057-agent-output-policy`

**Created**: 2026-11-23

**Status**: Draft

**Input**: GitHub issue [#55](https://github.com/rune-task-runner/rune/issues/55)
(backlog ADOPT-4): "Add per-task output tails and concise failure summaries for agents".
Builds on the output limit (#40, spec 042) and structured results (#42, spec 044). Related:
`||` failure hooks (spec 022), the audit log (#50, spec 052).

## Overview

A tool result carries what a task wrote, capped at the server's output limit (64 KiB per
stream by default, spec 042). The cap keeps an agent's context from filling up, but it cannot
know what matters for a given task. A test task that prints 2,000 lines needs its last 50.
A failed deployment needs to say which step failed. Today an agent gets up to 64 KiB of
noise, and the failed step is not in the result at all.

This feature adds two task attributes, each optional and independent of the other:

```rune
# Run the test suite.
[agent-output(tail="50")]
[agent-summary]
test: || diagnose
    go test ./...
```

| Attribute | What an agent's result gets |
|---|---|
| `[agent-output(tail="N")]` | The last N lines of each of stdout and stderr, and one line that says how many lines and bytes were left out |
| `[agent-output(head="H", tail="T")]` | The first H and the last T lines of each stream, with that line between them |
| `[agent-summary]` | When the run fails: a short section that names the task and the step that failed, quotes the last lines of output, and says whether a fix suggestion follows |

Both change only what an agent receives over MCP. A person who runs the task from the
command line sees what they see today.

| Question | Today | After |
|---|---|---|
| How much of a noisy task's output reaches the agent? | Up to the output limit, cut in the middle by bytes | What the author chose, in whole lines, never more than the output limit |
| Does the agent learn which step failed? | No. The result has the output and `[exit N]` | With `[agent-summary]`, the task, the Runefile line and the command |
| Can trimming hide a failure? | Not applicable | No. The exit status, `[exit N]` and the error flag are never changed |
| Can a secret reach the agent through the new text? | Not applicable | No. Everything is masked before it is kept, cut or quoted |
| Does a task without the attributes change? | Not applicable | No. Its text and structured content are byte for byte as today |

### Out of scope

- Retrieving the full output later. Nothing is stored beyond the call. The audit log keeps
  recording metadata only, as spec 052 defines it.
- Summaries written by a model or fetched over the network. Every summary is built by Rune
  from facts it has: the task, the step, the exit status, the output and the `||` hooks.
- Changing what the command line prints. There is no flag to apply these attributes to a
  person's terminal.
- A server-wide or per-call policy. The author of the Runefile decides per task, as for
  `[agent-allow]`.
- Different policies for stdout and stderr. One policy applies to each stream separately.
- Filtering lines by content (patterns, "only errors"). Retention is by position only.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Keep only the last lines of a noisy task for agents (Priority: P1)

A maintainer's test task prints thousands of lines, and an agent that runs it through MCP
fills its context with passing tests. The maintainer adds `[agent-output(tail="50")]`. The
agent now receives the last 50 lines of each stream, a line that says how many lines were
left out, and the exit status. The person who runs `rune test` sees every line.

**Why this priority**: It is the core of the issue: compact evidence that keeps the outcome.
It is useful alone, without a summary.

**Independent Test**: Give a task that prints 2,000 numbered lines `[agent-output(tail="50")]`,
call it over MCP, and check that the text holds lines 1951 to 2000, one omission line that
says 1,950 lines were left out, and `[exit 0]`; then run it from the command line and check
that all 2,000 lines print.

**Acceptance Scenarios**:

1. **Given** a task with `[agent-output(tail="50")]` that prints 2,000 lines on stdout,
   **When** an agent calls it, **Then** the stdout part of the result is one omission line
   followed by the last 50 lines, and the omission line states 1,950 lines and the number of
   bytes left out.
2. **Given** the same task printing 30 lines, **When** an agent calls it, **Then** stdout is
   returned whole, with no omission line.
3. **Given** a task with the attribute that writes 2,000 lines to stdout and 300 to stderr,
   **When** an agent calls it, **Then** each stream keeps its own last 50 lines and has its
   own omission line, and no line moves from one stream to the other.
4. **Given** a task with the attribute that fails with exit status 3 after printing 10,000
   lines, **When** an agent calls it, **Then** the result ends with `[exit 3]` and the result
   is marked as an error, as it would be without the attribute.
5. **Given** a task with the attribute, **When** a person runs it with `rune <task>`, **Then**
   every line is printed, as today.

---

### User Story 2 - Learn which step failed without reading all the output (Priority: P1)

An agent calls a deployment task, and the task fails. With `[agent-summary]` on the task, the
result has a failure summary after the exit line: the task that failed (which can be a
dependency of the one called), the Runefile line and the command of the step that failed,
the last few lines of output, and a note when the task's `||` failure hooks produced a fix
suggestion. When Rune does not know the failed step, the summary says so and still quotes
the last lines. It never guesses a cause.

**Why this priority**: The failed step is not in any agent result today. It is the second
half of the issue and works without the first.

**Independent Test**: A task with `[agent-summary]` whose second body line runs `exit 4`;
call it and check the summary names the task, the Runefile line of `exit 4`, the command
`exit 4`, and quotes the last lines of output; then a succeeding call has no summary.

**Acceptance Scenarios**:

1. **Given** a task with `[agent-summary]` whose third body line fails with exit status 2,
   **When** an agent calls it, **Then** the result has `[exit 2]` followed by a failure
   summary that names the task, the Runefile and line number of that body line, and the
   command as run, with secrets masked.
2. **Given** a task with `[agent-summary]` that depends on `build`, and `build` fails,
   **When** an agent calls the task, **Then** the summary names `build` as the task that
   failed and the task that was called.
3. **Given** a task with `[agent-summary]` and a `||` hook that prints a suggestion, **When**
   the task fails, **Then** the summary says a fix suggestion follows, and the fix suggestion
   section follows the summary, unchanged.
4. **Given** a task with `[agent-summary]` that fails for a reason with no body line (an
   environment variable that `env()` reads and that is not set), **When** an
   agent calls it, **Then** the summary gives Rune's own message for that reason and says
   that no step of a body failed.
5. **Given** a run whose failure Rune cannot explain (it knows only the exit status),
   **When** the summary is built, **Then** it says that the cause is not known and quotes the
   last lines of output, and states nothing else as a cause.
6. **Given** a task with `[agent-summary]` that succeeds, **When** an agent calls it,
   **Then** the result has no summary, and its text is what it would be without the
   attribute.
7. **Given** a call that is cancelled while the task runs, **When** the result is built,
   **Then** the summary says the run was cancelled and, when Rune knows it, names the task
   that was running, and the result is marked as an error.

---

### User Story 3 - Keep the beginning and the end (Priority: P2)

A build task prints its configuration first and its errors last. The maintainer writes
`[agent-output(head="10", tail="40")]`. The agent receives the first 10 lines, one omission
line, and the last 40 lines of each stream.

**Why this priority**: The issue asks for head and tail. It extends Story 1 with one more
value.

**Independent Test**: A task printing 500 numbered lines with `head="10", tail="40"`; check
lines 1 to 10, an omission line for 450 lines, lines 461 to 500.

**Acceptance Scenarios**:

1. **Given** `[agent-output(head="10", tail="40")]` and 500 lines of output, **When** an
   agent calls the task, **Then** the stream holds lines 1 to 10, one omission line for 450
   lines, and lines 461 to 500.
2. **Given** the same attribute and 45 lines, **When** an agent calls the task, **Then** the
   stream is returned whole.
3. **Given** `[agent-output(head="10")]` with no `tail`, **When** the Runefile is checked,
   **Then** it is an error at the attribute that says `tail` is required.

---

### User Story 4 - Read what was kept from structured fields (Priority: P2)

A client reads results as structured content (spec 044). For a task with an output policy,
the structured content says which policy applied and, for each stream, how many lines and
bytes it had and how many were kept. For a failed task with `[agent-summary]`, the summary is
a structured object with the same facts as the text section. The client never parses the
omission line or the summary text.

**Why this priority**: The issue asks for text and structured forms built from the same
result. It adds no new behavior beyond Stories 1 to 3.

**Independent Test**: Call a task with both attributes that prints 2,000 lines and fails;
validate the structured content against the tool's output schema, and check that every count
and fact in the structured fields matches the text.

**Acceptance Scenarios**:

1. **Given** a task with an output policy, **When** an agent calls it, **Then** the
   structured content has an output object with the policy and, for each stream, the total
   lines and bytes and the kept lines and bytes, and the numbers match the omission lines in
   the text.
2. **Given** a failed task with `[agent-summary]`, **When** an agent calls it, **Then** the
   structured content has a failure summary object with the same task, step, cause and
   quoted lines as the text section.
3. **Given** a task with neither attribute, **When** an agent calls it, **Then** the
   structured content has exactly the fields it has today and no other.
4. **Given** any tool, **When** a client lists the tools, **Then** the output schema
   declares the two new fields as optional, and every result validates against it.

---

### Edge Cases

- **A single line larger than the output limit.** A line count does not bound memory. The
  stream is still at most the output limit, omission line included: the kept text ends at the
  last byte written, and the part of the line that does not fit is counted in the omitted
  bytes. Memory held for a stream never passes the output limit while the task runs.
- **Multi-megabyte output.** A task that prints 50 MB holds at most the output limit per
  stream in memory at any time, and the result obeys the bounds of spec 042 and 044, plus the
  bounded summary.
- **No trailing line break.** A last line without a line break counts as a line and is kept
  as written. No line break is added to the stream.
- **CR LF and lone CR.** A line ends at a line feed. A carriage return is part of the line. A
  progress bar that redraws with carriage returns is one long line.
- **Unicode.** A cut never splits a UTF-8 character. Bytes that are not valid UTF-8 are kept
  as they are.
- **Empty stream.** It stays the empty string, with no omission line.
- **Exactly the kept number of lines.** No omission line: nothing was left out.
- **`[parallel]` dependencies.** Their writes reach the stream in the order they arrive. The
  policy counts lines in the stream as received; the same bytes in the same order always give
  the same result, however they were split into writes.
- **Output of `||` hooks.** Hook stdout is the fix suggestion and keeps its 8 KiB limit
  (spec 022). Hook stderr is part of stderr and is under the policy like any other line.
- **A secret that sat across the cut.** Masking happens before anything is kept, so a cut
  falls in masked text, never inside a secret.
- **A secret in the failed command.** The command line quoted in the summary is masked with
  the same secrets as the output, before it is shortened.
- **A very long failed command.** It is shortened to a fixed length, with a marker, after
  masking.
- **A policy on a private task, or on a dependency.** The policy of the task the agent called
  governs the whole result. A policy on a task that is not called as a tool has no effect.
- **The same attribute twice on one task.** An error, like a second `[agent-allow]`.
- **Values out of range.** `tail="0"`, `tail="-1"`, `tail="5k"`, `tail=""` and values above
  the maximum are errors at the value, before any task runs.
- **Reload.** A changed policy applies to calls that start after the reload (spec 043). The
  tool list does not change, because the output schema is the same for every tool.
- **A call that runs no task.** A refusal or an invalid argument returns what it returns
  today: text only, no structured content, no summary.
- **Output limit raised or lowered.** The policy's lines are kept within whatever limit is in
  force. A lower limit can keep fewer lines than the policy names, and the omission line and
  the structured counts say so.

## Requirements *(mandatory)*

### Functional Requirements

**The output policy**

- **FR-001**: A task MUST be able to declare `[agent-output(tail="T")]` or
  `[agent-output(head="H", tail="T")]`. `tail` is required and `head` is optional, in either
  order. Each value MUST be a whole number in decimal from 1 to 10,000.
- **FR-002**: A task MUST be able to declare `[agent-summary]`, with no arguments.
- **FR-003**: The two attributes MUST be independent: a task can have either, both, or
  neither.
- **FR-004**: An `[agent-output]` with an unknown argument, a repeated argument, no `tail`,
  or a value that is not a whole number in range, and a second `[agent-output]` or
  `[agent-summary]` on one task, MUST be a Runefile error with its own diagnostic code,
  pointing at the attribute or the value, reported before any task runs.
- **FR-005**: Both attributes MUST be accepted by `rune fmt`, which keeps them as written in
  canonical form, and known to the language server (completion and hover) and to the grammar
  in the documentation.
- **FR-006**: The policy that applies to a call MUST be the one of the task the agent called.
  The attributes of tasks the call reaches MUST NOT change the result.

**Kept lines**

- **FR-007**: With an output policy, each of stdout and stderr MUST be reduced on its own to
  its first H lines (when `head` is set) and its last T lines. When lines are left out, one
  omission line MUST stand where they were, stating the number of lines and the number of
  bytes left out.
- **FR-008**: A stream with no more than H + T lines, that fits the output limit, MUST be
  returned whole, with no omission line.
- **FR-009**: A line MUST end at a line feed, which belongs to the line. A last line with no
  line feed MUST count as a line. A carriage return MUST be part of its line.
- **FR-010**: Each kept stream, omission line included, MUST be at most the output limit in
  force (spec 042). When the chosen lines do not fit, the kept text MUST be cut at a UTF-8
  character boundary, the bytes cut MUST be counted in the omission line, and the kept text
  MUST still end with the last byte the stream received.
- **FR-011**: While a task runs, the memory held for each stream of a call MUST stay at most
  the output limit, whatever the policy and however long a line is.
- **FR-012**: The kept text MUST depend only on the bytes of the stream in their order, never
  on how they were split into writes or on timing.
- **FR-013**: Stdout and stderr MUST stay separate: no line moves between them, and each has
  its own counts.

**Failure summary**

- **FR-014**: With `[agent-summary]`, a result whose exit status is not 0 MUST have a failure
  summary section after the `[exit N]` line and before the fix suggestion section. A result
  with exit status 0 MUST NOT have one.
- **FR-015**: The summary MUST name the task that was called and, whenever Rune knows it,
  the task whose body failed, which can be a task the call reached.
- **FR-016**: When a body line failed, the summary MUST give the Runefile path and line
  number of that line and the command as it ran, masked and shortened to a fixed length with
  a marker.
- **FR-017**: When the run failed for a reason other than a body line (an interpolation or
  evaluation error, a plan that could not be applied), the summary MUST give Rune's own one-line message for that reason, masked, and
  MUST say that no body step failed.
- **FR-018**: When the run was cancelled, the summary MUST say so and, when Rune knows it,
  name the task that was running.
- **FR-019**: When Rune knows only the exit status, the summary MUST say that the cause is
  not known. A summary MUST NOT state a cause Rune did not observe.
- **FR-020**: Every summary MUST quote up to the last 5 lines of each non-empty stream, taken
  from the kept streams, each block labelled with its stream.
- **FR-021**: When the result has a fix suggestion, the summary MUST say that one follows and
  that it comes from the task's `||` failure hooks. The fix suggestion section MUST be
  unchanged.
- **FR-022**: The summary MUST be at most 4 KiB, cut at a character boundary with a marker
  when needed.
- **FR-023**: The summary MUST be built without a model, a network call, or running anything.
  The same run facts MUST always give the same summary.

**Outcome**

- **FR-024**: The attributes MUST NOT change the exit status, the `[exit N]` line, or the
  error flag of a result. The `[exit N]` line MUST always be present and complete.
- **FR-025**: Rune MUST never present a failed or cancelled run as a success, in the text or
  in the structured content.

**Masking**

- **FR-026**: No secret value, or part of one, MUST appear in a kept stream, an omission
  line, the summary or any structured field. Streams are masked before lines are kept or cut;
  the quoted command and every message are masked before they are shortened.

**Structured content**

- **FR-027**: Every tool's output schema MUST declare two more optional properties: an
  output object and a failure summary object, each with a one-sentence description and a
  closed set of fields. The schema MUST stay identical for every tool.
- **FR-028**: The output object MUST be present exactly when the called task has an output
  policy. It MUST hold the policy (head and tail) and, for each stream, the total lines, total
  bytes, kept lines and kept bytes.
- **FR-029**: The failure summary object MUST be present exactly when the text has a summary
  section, and MUST hold the same facts: the called task, the failed task, the step (path,
  line, command) when there is one, the reason, the kind (which says whether the run was
  cancelled), whether a fix suggestion follows, and the quoted lines per stream.
- **FR-030**: The text and the structured content MUST be built from one kept result: the
  `stdout` and `stderr` fields MUST be the kept streams the text shows, omission lines
  included, and every count in the output object MUST match its omission line.

**Compatibility**

- **FR-031**: For a task with neither attribute, the text and the structured content of every
  result MUST be byte for byte what they are today.
- **FR-032**: A call that runs no task (a refusal, an undeclared argument, an invalid value)
  MUST return what it returns today.
- **FR-033**: Output printed for a person, by `rune <task>` or any other command, MUST NOT
  change.
- **FR-034**: Audit records MUST stay metadata. No kept output and no summary text is written
  to the audit log.
- **FR-035**: The behavior MUST be the same on stdio, on HTTP (`rune serve`), and on the
  endpoint an `agent` task opens.
- **FR-036**: The embeddable server library MUST render the kept streams, the omission lines
  and the summary from what an engine returns, and keep the output limit as a hard ceiling for
  any engine.

**Documentation and tests**

- **FR-037**: The Runefile reference MUST document both attributes. The MCP documentation
  MUST document the omission line, the summary section, the new structured fields, and the
  bound on a whole result.
- **FR-038**: The documentation MUST show a noisy test task: its full output size, the
  smaller result an agent receives with the attributes, and the evidence that result keeps.
  The numbers MUST come from a real run.
- **FR-039**: Tests MUST cover: kept lines for each stream with a missing trailing line feed,
  long lines, Unicode, CR LF and concurrent writers; output of several megabytes within the
  limits; exit status, cancellation and the error flag; secrets in output, across a cut and in
  the failed command; a summary with a `||` suggestion, with a body step, without one, and
  with an unknown cause; text and structured agreement; and unchanged results for tasks with
  neither attribute.

### Key Entities

- **Output policy**: Per task. Optional `head` and required `tail`, line counts. Declared by
  `[agent-output]`.
- **Kept stream**: Stdout or stderr of one call after masking, the policy and the output
  limit. Has total and kept line and byte counts, and at most one omission line.
- **Omission line**: One line where lines were left out, with the number of lines and bytes.
- **Failure summary**: Built by Rune for a failed or cancelled call of a task with
  `[agent-summary]`. Names the called and failed tasks, the step when known, the reason, the
  cancellation, the presence of a fix suggestion, and the last lines of each stream.
- **Tool result**: Text, error flag and structured content (spec 044), now with the optional
  output object and failure summary object.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a test task that prints 2,000 lines, an agent's result with
  `[agent-output(tail="50")]` is under 10% of the size of the result without it, and still
  holds the last 50 lines and the exit status.
- **SC-002**: For a task that prints 50 MB on each stream, every kept stream is at most the
  output limit, and the memory held per stream during the run never passes it.
- **SC-003**: Across every test of a failing or cancelled task, with and without the
  attributes, the exit status and the error flag are the same.
- **SC-004**: Across tests that place a secret in each stream, across a cut, and in the failed
  command, no part of any result contains the secret.
- **SC-005**: For a failed body line, an agent learns the task, the Runefile line and the
  command from the result alone, with no other call.
- **SC-006**: For every call that runs a task, the structured content validates against the
  tool's schema, and every count and fact in it matches the text.
- **SC-007**: Every existing test of result text and structured content passes with no change
  to its assertions.

## Assumptions

- **Two attributes, named for agents.** `[agent-output]` and `[agent-summary]` sit beside
  `[agent-allow]`: they change what an agent receives and nothing else. Named string
  arguments follow `[agent-allow]` and `[plan(expires="1h")]`, so the parser needs no new
  syntax.
- **`tail` is required.** The end of the output is where the outcome is. A head-only policy
  would drop it, so it is not offered.
- **10,000 lines is the maximum.** More lines than that exceed the default output limit at
  any realistic line length, and the limit wins anyway.
- **Five quoted lines per stream.** Enough for the error and its context, small enough that
  the summary stays a summary. The full kept streams are in the same result.
- **The summary quotes the end of the streams.** Lines that `||` hooks write to stderr come
  after the failure and can be among them. The summary states which stream and that these are
  its last lines, so nothing is presented as more than it is.
- **The failed step is what Rune already knows.** A body line that failed carries its
  Runefile line and command. A script run as a whole (`[script]`) has no line within the
  script; the summary names the task and says so.
- **The policy of the called task governs.** A dependency's policy would apply to output that
  is mixed with other tasks' output in one stream, which cannot be done line by line.
- **The schema grows for every tool.** Spec 044 kept one schema for all tools and said this
  issue would extend it. The two fields are optional and absent for tasks without the
  attributes, so existing results stay valid and unchanged.
- **Not a breaking change.** The attribute names were errors before, the schema change is
  additive, and nothing else changes for a task without the attributes.
