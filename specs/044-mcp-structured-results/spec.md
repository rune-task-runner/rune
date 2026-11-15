# Feature Specification: Return Structured Tool Results

**Feature Branch**: `044-mcp-structured-results`

**Created**: 2026-11-14

**Status**: Draft

**Input**: GitHub issue [#42](https://github.com/rune-task-runner/rune/issues/42)
(backlog MCP-8): "Return structured MCP tool results"

## Overview

When an agent calls a task through `rune mcp`, the result is one text: the
task's stdout, then its stderr, then an `[exit N]` line, then an optional fix
suggestion. To learn the exit code a client has to find and parse that line.
It cannot tell where stdout ends and stderr begins, and a task that prints
`[exit 0]` itself can mislead a parser.

```text
running tests
FAIL: fixtures out of date
diagnosing: hint
[exit 1]
[fix suggestion - from Runefile || failure hooks]
re-run with -update
```

The protocol has a place for machine-readable results: a tool can declare an
output schema, and a result can carry structured content that matches it. This
feature uses both. Every tool declares the same output object, and every
result of a task run carries it beside the unchanged text:

```json
{
  "exitCode": 1,
  "stdout": "running tests\n",
  "stderr": "FAIL: fixtures out of date\ndiagnosing: hint\n",
  "fixSuggestion": "re-run with -update"
}
```

A client that understands structured content reads the fields. A client that
does not sees exactly the text it sees today.

### Out of scope

- Fixing exit codes that a task body loses before the result is built:
  [#33](https://github.com/rune-task-runner/rune/issues/33). The structured
  exit code is the number the `[exit N]` line shows, whatever that is.
- Retention and truncation metadata and summary provenance in the structured
  result: they belong to per-task output policies,
  [#55](https://github.com/rune-task-runner/rune/issues/55).
- Changing the text of a result, the `[exit N]` line, the truncation marker,
  or the fix-suggestion header.
- A way to turn structured content off, or to return only structured content.
- Structured content for calls that never run a task (see User Story 2 and
  FR-016).
- Output of `rune` run from the command line.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An agent reads the exit code and the streams as fields (Priority: P1)

A client calls `test`. It needs to know whether the run passed, what the task
printed to stdout, and what it printed to stderr. Today it parses one text.
With this feature it reads `exitCode`, `stdout` and `stderr` from the result's
structured content.

**Why this priority**: This is what the issue asks for. The other stories keep
it safe and compatible.

**Independent Test**: Start the server on a Runefile whose task prints one
line to stdout, one to stderr, and exits with status 3. Call it as an MCP
client. The structured content is an object with `exitCode` 3, `stdout` equal
to the stdout line, and `stderr` equal to the stderr line.

**Acceptance Scenarios**:

1. **Given** a task that prints `out` to stdout and `err` to stderr and
   succeeds, **When** an agent calls it, **Then** the result's structured
   content is `{"exitCode": 0, "stdout": "out\n", "stderr": "err\n"}`.
2. **Given** a task whose body exits with status 3, **When** an agent calls
   it, **Then** `exitCode` is 3, the same number the `[exit 3]` line shows,
   and the result is marked as an error as today.
3. **Given** a task that prints nothing, **When** an agent calls it, **Then**
   `stdout` and `stderr` are present and are empty strings.
4. **Given** a failing task with a `||` failure hook that prints a
   suggestion, **When** an agent calls it, **Then** `fixSuggestion` holds the
   suggestion text exactly as the text result shows it after the
   fix-suggestion header.
5. **Given** a task that succeeds, or a failing task with no `||` hook or a
   hook that prints nothing, **When** an agent calls it, **Then** the
   structured content has no `fixSuggestion` field.
6. **Given** a task that prints the text `[exit 0]` and then fails with
   status 1, **When** an agent calls it, **Then** `exitCode` is 1 and
   `stdout` holds the printed `[exit 0]` as ordinary output.
7. **Given** a task whose dependency and post-hook also print, **When** an
   agent calls it, **Then** `stdout` and `stderr` each hold the combined
   stream of the whole run, as the text result does.
8. **Given** any tool, **When** an agent lists the tools, **Then** the tool
   declares an output schema that describes exactly these fields, and it is
   the same for every tool.

---

### User Story 2 - Existing clients see no difference (Priority: P1)

A client written before this feature reads the text of each result and
ignores fields it does not know. It must keep working with no change: same
text, same error flag, same tool names, descriptions, input schemas and hints.

**Why this priority**: Agents and scripts depend on the current text. A
structured result that changed it would break them to help others.

**Independent Test**: Run the existing suite of result-text tests with no
change to their assertions. Compare, for a set of calls covering success,
failure, both streams, truncation and fix suggestions, the text content
before and after this feature: it is byte for byte the same.

**Acceptance Scenarios**:

1. **Given** any call that runs a task, **When** the result is returned,
   **Then** its text content is byte for byte what it was before this
   feature for the same captured output and exit status.
2. **Given** any call, **When** the result is returned, **Then** it has
   exactly one text content block and its error flag is what it was before.
3. **Given** the tool list before and after this feature, **When** the two
   are compared, **Then** each tool differs only by the added output schema.
4. **Given** a call that never runs a task (a destructive task that is not
   granted, an undeclared argument name, an invalid argument value),
   **When** the result is returned, **Then** it is exactly what it was
   before this feature: an error with the explanatory text and no structured
   content.

---

### User Story 3 - Structured fields are as safe and as bounded as the text (Priority: P1)

A task prints a secret, or prints 3 MB. The text result masks the secret and
caps each stream. The structured fields must not offer a way around either:
they carry the same masked, capped strings.

**Why this priority**: A second representation of the output is a second
place for a leak or an unbounded result. Masking is a locked security
commitment and the cap protects the agent's context.

**Independent Test**: With a secret in the server's environment, call a task
that prints the secret and 3 MB of filler on both streams. Search the whole
result for the secret: it is nowhere. The `stdout` and `stderr` fields are
each at most the output limit and hold the same truncation marker as the
text.

**Acceptance Scenarios**:

1. **Given** a secret value in the server's environment and a task that
   prints it to stdout and to stderr, **When** an agent calls the task,
   **Then** the `stdout` and `stderr` fields show the mask placeholder where
   the text does, and the result holds the secret value nowhere.
2. **Given** a task that prints 3,000,000 bytes to stdout with the default
   limit, **When** an agent calls it, **Then** the `stdout` field is at most
   65,536 bytes, holds the same `[truncated N bytes]` line as the text, and
   equals the stdout part of the text.
3. **Given** a server started with `--output-limit 1KiB`, or a Runefile with
   `set mcp_output_limit := "1KiB"`, **When** an agent calls a task that
   prints 10,000 bytes on each stream, **Then** each of `stdout` and `stderr`
   is at most 1,024 bytes.
4. **Given** a `||` failure hook that prints a secret and more than 8 KiB,
   **When** the task fails, **Then** `fixSuggestion` is the same masked text
   with the same 8 KiB cap and marker that the text result shows.
---

### Edge Cases

- A task prints bytes that are not valid text. The fields hold the same
  characters the text content does: both pass through the protocol's text
  encoding in the same way.
- Output that ends without a line break. The text result adds line breaks to
  join its parts. The fields do not: they hold the stream as captured.
- `stdout` over the limit and `stderr` empty, or the reverse. Each field is
  capped on its own, as in the text.
- A task is cancelled or times out. If a result is returned, it carries the
  structured content for the output captured and the exit status reported.
- The `[context]` hook is not a tool, so it has no result. Its output stays
  in the session instructions.
- The endpoint an `agent` task opens for its own agent returns the same
  structured results and declares the same output schema.
- A server reloads its Runefile. Reloaded and new tools declare the same
  output schema. A reload that changes nothing else sends no notification.
- A client that validates results against the declared schema. Every result
  that carries structured content passes. A result without structured content
  is always marked as an error.
- For `rune mcp` the exit code is the one the command line would exit with,
  from 0 to 255. An embedder's engine may report any integer, and it is
  returned as given.

## Requirements *(mandatory)*

### Functional Requirements

**Output schema**

- **FR-001**: Every tool MUST declare an output schema that describes an
  object with these properties: `exitCode` (integer), `stdout` (string),
  `stderr` (string), and `fixSuggestion` (string).
- **FR-002**: The schema MUST mark `exitCode`, `stdout` and `stderr` as
  required and `fixSuggestion` as optional, and MUST allow no other
  property.
- **FR-003**: The schema MUST be identical for every tool, on stdio, on HTTP,
  and on the endpoint an `agent` task opens.
- **FR-004**: Each property MUST carry a one-sentence description, so a
  client or model can tell what the field means without other documentation.
  The descriptions are fixed text and hold nothing from the Runefile or the
  environment.

**Structured content**

- **FR-005**: Every result of a call that ran a task MUST carry structured
  content that is valid against the declared schema, whether the task
  succeeded or failed.
- **FR-006**: `exitCode` MUST be the number shown in the result's `[exit N]`
  line.
- **FR-007**: `stdout` MUST be the task's captured stdout after masking and
  after the output cap, and `stderr` the same for stderr. Each MUST be the
  exact string from which the text content is built, before the line breaks
  that join the parts of the text.
- **FR-008**: `stdout` and `stderr` MUST always be present. A stream with no
  output is the empty string.
- **FR-009**: `fixSuggestion` MUST be present exactly when the text content
  has a fix-suggestion section, and MUST be the text of that section after
  its header line.
- **FR-010**: The structured content MUST hold no field beyond the four
  declared.

**Masking and limits**

- **FR-011**: No structured field MUST contain a secret value, or part of
  one, that the text content does not contain. Masking applies before any
  field is built.
- **FR-012**: Each of `stdout` and `stderr` MUST be at most the output limit
  in force, with the same truncation marker, at the same position, as in the
  text content. `fixSuggestion` MUST keep its 8 KiB limit and marker.
- **FR-013**: The limit MUST come from the same place as for the text: the
  `--output-limit` flag, else `set mcp_output_limit`, else the default.
- **FR-013a**: The documentation of the output limit MUST state the bound on
  a whole result now that the output appears twice: the text is at most
  twice the limit plus 32 bytes, the two stream fields together are at most
  twice the limit, and the fix suggestion, at most 8 KiB, appears in each.

**Compatibility**

- **FR-014**: The text content of a result MUST be byte for byte what it was
  before this feature for the same captured output, exit status and fix
  suggestion. A result MUST still hold exactly one text content block.
- **FR-015**: The error flag of a result MUST depend only on the exit status,
  as today.
- **FR-016**: A call that does not run a task (a refusal, an undeclared
  argument name, an invalid argument, a task the engine rejects before
  running) MUST return what it returns today: an error result with text and
  no structured content.
- **FR-017**: Tool names, descriptions, input schemas and hints MUST be
  unchanged. The output schema is the only addition to a tool's listing.
- **FR-018**: The reload behavior of spec 043 MUST be unchanged: a reload
  that leaves the tools as they were sends no notification.

**Library, documentation and tests**

- **FR-019**: The embeddable server library MUST declare the schema and
  return the structured content for any engine an embedder supplies, built
  from the result the engine returns, with the library's output cap applied.
- **FR-020**: The result format MUST be documented with the other `rune mcp`
  behavior: the fields, their relation to the text, and which results carry
  no structured content. The release notes MUST mention the addition.
- **FR-021**: An automated end-to-end test MUST start the real `rune mcp`
  binary, list the tools, call a task, and assert the output schema and the
  structured content against the text. It stands for
  `TestMCPE2E_Pending_StructuredResult` of the issue, which is not in this
  repository.

### Key Entities

- **Tool result**: What an agent receives for one call. It has text content,
  an error flag, and now structured content.
- **Structured content**: An object with the exit code, the two captured
  streams and, when there is one, the fix suggestion. Built from the same
  values as the text.
- **Output schema**: The description of that object that every tool
  declares. One schema for all tools.
- **Captured stream**: The stdout or the stderr of one call after masking
  and the output cap (spec 042).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A client can read the exit code, stdout and stderr of any task
  run from named fields, with no parsing of the text.
- **SC-002**: For every call that runs a task, the structured content is
  valid against the schema the tool declares.
- **SC-003**: For every call, the text content is byte for byte what it was
  before this feature. Every existing test of result text passes with no
  change to its assertions.
- **SC-004**: Across test outputs that include a secret on each stream and
  in the fix suggestion, no part of the result contains the secret.
- **SC-005**: For a task that prints 3 MB on each stream, each structured
  stream field is at most the output limit in force.
- **SC-006**: Rebuilding the text from the structured fields by the
  documented rule gives the text content exactly, for every call that runs a
  task.

## Assumptions

- **Refusals carry no structured content.** No task ran, so there is no exit
  code or stream to report, and inventing one would mislead. Such results
  are always marked as errors, which is the case in which clients do not
  expect structured content.
- **The text stays the human format.** The protocol suggests repeating the
  structured content as JSON in the text block. The issue requires the text
  to stay byte-identical, and existing agents read it, so it stays as it is.
- **The schema is closed.** No property beyond the four is allowed, so a
  client can validate strictly. Issue #55 will add fields and extend the
  schema in the same release.
- **Fields hold the stream, not the joined text.** `stdout` and `stderr` are
  the captured streams without the line breaks the text adds between parts.
  That makes them exact, and the rule to rebuild the text from them is
  documented.
- **The result is larger on the wire.** The output appears in the text and
  in the fields, so a result is up to about twice its earlier size: at most
  four times the output limit plus 32 bytes, plus the fix suggestion twice,
  before protocol encoding. Each representation obeys the output limit on
  its own. The issue asks for both, and clients are expected to give a model
  one of the two. The bound is documented (FR-013a).
- **The limit counts stream bytes.** As for the text, the cap applies to the
  stream before it is encoded for the protocol. Encoding control or invalid
  bytes can make the encoded form longer.
- **Older clients ignore what they do not know.** A client that predates
  structured content skips the two new fields, as the protocol requires of
  unknown fields.
- **Not a breaking change.** Nothing existing changes. The commit is a
  feature, and the release notes describe the addition.
