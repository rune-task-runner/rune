# Feature Specification: Reject Unknown MCP Tool Arguments

**Feature Branch**: `032-mcp-unknown-arguments`

**Created**: 2026-11-05

**Status**: Draft

**Input**: GitHub issue [#36](https://github.com/rune-task-runner/rune/issues/36)
(backlog MCP-2): "Misspelled arguments silently fall back to defaults"

## Overview

An agent that misspells an argument name gets a success for the wrong operation.
Rune ignores the name, uses the parameter's default, and reports success:

```rune
# Build the project.
build target="debug":
    @echo "built {{target}}"
```

```text
call:   build(targte="release")
result: built debug
        [exit 0]
```

The agent believes it built `release`. A typo in a deployment does the same:
`deploy(env="prod", replicaz="5")` runs with the default replica count and reports
success.

A misspelled required argument does fail, but the error points at the wrong thing.
`deploy(evn="prod")` returns `task "deploy" is missing required argument "env"`, and
nothing says that `evn` was ignored.

The tool schemas do not help. They never say that other names are disallowed, so a
client that checks a call against the schema lets the typo through. The contract for
spec 023 lists unknown argument names as "rejected (existing behavior, unchanged)",
but no version of Rune has rejected them.

This feature makes the server refuse any argument name the task does not declare,
before anything runs:

```text
unknown argument "targte" for task "build" (did you mean "target"?)
```

Every tool's input schema also declares that no other names are allowed.

Code review of this feature found the same silent result for values. `build(target=null)`
ran with the text `<nil>`, and `rm(path=null)` passed the required-argument check and ran
too. The feature refuses those values as well (FR-015).

### Out of scope

- Command-line arguments. `rune build ...` takes positional values and `NAME=VALUE`
  overrides, and neither changes.
- Policies on argument values:
  [#48](https://github.com/rune-task-runner/rune/issues/48) (MCP-14). Beyond names, this
  feature refuses only values a task has no way to receive (FR-015). Other checks on
  declared parameters' values stay as they are.
- Structured tool results:
  [#42](https://github.com/rune-task-runner/rune/issues/42) (MCP-8). The rejection is
  a plain error result, like every other refusal today.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A misspelled argument is refused, not ignored (Priority: P1)

An agent calls a tool with an argument name the task does not declare. The call is
refused before anything runs. The error names the argument and suggests the
parameter the agent probably meant, so the agent can correct the call on its next
try.

**Why this priority**: A success for the wrong operation is the worst result an agent
call can have. The agent cannot detect it and goes on to act on it. This story alone
closes the bug.

**Independent Test**: Start the server on a Runefile whose task bodies write marker
files. Call `build(targte="release")`. The result is an error with the exact text
below, and no marker file exists.

**Acceptance Scenarios**:

1. **Given** the task `build target="debug"`, **When** an agent calls it with
   `targte="release"`, **Then** the result is marked as an error, its text is exactly
   `unknown argument "targte" for task "build" (did you mean "target"?)`, it has no
   `[exit N]` line, and the body never runs.
2. **Given** the task `deploy env replicas="2"`, **When** an agent calls it with
   `env="prod"` and `replicaz="5"`, **Then** the error reads
   `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)` and the
   body never runs.
3. **Given** the same task, **When** an agent calls it with only `evn="prod"`, **Then**
   the error reads `unknown argument "evn" for task "deploy" (did you mean "env"?)`,
   not a complaint about the missing `env`.
4. **Given** a task with dependencies, post-hooks, and failure hooks, **When** an agent
   calls it with an unknown argument, **Then** none of them runs.
5. **Given** the task `build target="debug"`, **When** an agent calls it with
   `zzzzzz="1"`, which is close to no parameter, **Then** the error reads
   `unknown argument "zzzzzz" for task "build"` with no suggestion.
6. **Given** a task with no parameters, **When** an agent calls it with `force=true`,
   **Then** the error reads `unknown argument "force" for task "clean_all"` and the
   body never runs.
7. **Given** a call with two unknown names, `replicaz` and `evn`, **When** the server
   refuses it, **Then** the error has one line per name, sorted by name, so the agent
   can fix both in one retry:
   ```text
   unknown argument "evn" for task "deploy" (did you mean "env"?)
   unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)
   ```
8. **Given** the task `build target="debug"`, **When** an agent calls it with
   `target="release"` or with no arguments, **Then** it runs exactly as before, printing
   `built release` or `built debug`.

---

### User Story 2 - Tool schemas say that no other names are allowed (Priority: P2)

An agent or client reads a tool's input schema and can tell, before the first call,
that only the listed names are accepted. A client that checks calls against the schema
catches a typo without a round trip to the server.

**Why this priority**: The schema is what agents build their calls from, so a complete
schema prevents some typos in the first place. The refusal in User Story 1 still
protects every client that does not check.

**Independent Test**: List the tools for a Runefile that has tasks with no parameters,
defaulted, required, typed, and variadic parameters, plus a destructive task. Every
input schema has `additionalProperties: false`, and nothing else in it has changed.

**Acceptance Scenarios**:

1. **Given** a Runefile with tasks of every parameter shape, **When** a client lists
   the tools, **Then** every tool's input schema has `additionalProperties: false`, and
   its `type`, `properties`, and `required` are the same as before.
2. **Given** a task with no parameters, **When** a client lists the tools, **Then** its
   schema is an object with empty `properties` and `additionalProperties: false`.
3. **Given** a destructive task the operator has not allowed, **When** a client lists
   the tools, **Then** its schema has `additionalProperties: false` like any other.
4. **Given** the HTTP transport or the endpoint an `agent` task opens for its own agent,
   **When** a client lists the tools, **Then** the schemas are the same as on stdio.

---

### Edge Cases

- **A correct and a misspelled name together.** `build(target="a", targte="b")` is
  refused. The suggestion still names `target`.
- **An unknown name and a bad value together.** `deploy(env="nope", replicaz="5")`, where
  `env` is an enum that does not accept `nope`, reports only the unknown name. Values are
  checked once every name is known, so the result does not depend on which check ran
  first.
- **A refused task.** A call to a destructive task the operator has not allowed, or to a
  task outside the allowlist, gets that refusal and nothing about its arguments. The
  rule for who may call a task comes before the rule for how to call it.
- **A granted destructive task.** A destructive task the operator has allowed gets no
  exception. `wipe(forcee=true)` is refused for the unknown name and does not run.
- **Case.** Names match exactly. `build(Target="release")` is refused, with the suggestion
  `target`. A name that differs only in case gets that suggestion however many letters
  differ, so `deploy(ENV="prod")` suggests `env`.
- **Value shape.** A name is refused whatever its value is: a string, number, boolean,
  null, or array.
- **No arguments.** Absent arguments, `null`, and `{}` run the task as before. Arguments
  that are not a JSON object keep today's `invalid arguments:` error.
- **Imported tasks.** The message uses the Runefile form, `tools::build`, the same form as
  the destructive refusal, not the tool name `tools__build`.
- **Unusual names.** An empty name or one holding quotes or newlines is shown quoted and
  escaped, so each name stays on one line.
- **Values are private.** The error shows the name only. `build(targte="s3cret")` never
  echoes `s3cret`, because an agent may put a secret in a misnamed argument.
- **Ties.** When two parameters are equally close, the suggestion is one the call did not
  supply, because following a hint to a supplied name would overwrite its value. Among
  those it is the one declared first, so the same call always gets the same text.
- **Long names and many names.** The text goes into the calling agent's context, so its
  size is bounded. A name over 64 characters is cut off with `...` and gets no suggestion.
  After 20 lines, a final line gives the count not shown.
- **Null and objects.** `build(target=null)`, `build(target={"k":"v"})`, and
  `each(names=[null])` are refused, and nothing runs. Strings, numbers, booleans, and arrays
  of those keep working, so `build(target=true)` still runs with `true`.
- **Short names.** The closeness rule is the one Rune already uses for task names: at most
  two edits. A short name can therefore get a loose suggestion, such as `x` suggesting
  `id`. It is only advice, since the call is refused either way.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A tool call that names an argument the task does not declare MUST be refused
  before anything executes: no body, dependency, or hook.
- **FR-002**: The refusal MUST be an error result, without an `[exit N]` line, whose text
  is `unknown argument "<name>" for task "<task>" (did you mean "<parameter>"?)` when a
  close parameter exists, and `unknown argument "<name>" for task "<task>"` when none does.
- **FR-003**: `<task>` MUST be the Runefile task name, such as `tools::build`, not the tool
  name. `<name>` MUST be quoted and escaped so that it cannot break the line.
- **FR-004**: A suggestion MUST come only from the called task's declared parameters. A name
  that differs from a parameter only in case MUST suggest that parameter. Otherwise it MUST
  use the same closeness rule as Rune's existing task-name suggestions. When several
  parameters are equally close, it MUST be one the call did not supply, and among those the
  one declared first.
- **FR-005**: When a call has several unknown names, the text MUST have one line for each,
  sorted by name, up to 20 lines, followed by
  `unknown arguments not shown for task "<task>": <count>` when there are more. A name over
  64 characters MUST be cut off at 64 with `...` and get no suggestion. The same call MUST
  always produce the same text.
- **FR-006**: The unknown-name check MUST run after the existing authorization refusals and
  before value checks and the missing-required-argument check. A refused task reports only
  its refusal. A call with an unknown name reports only unknown names. With several invalid
  values, the error MUST name the parameter declared first.
- **FR-007**: The refusal MUST NOT include any argument's value.
- **FR-008**: Names MUST match declared parameter names exactly, including case. A name MUST
  be refused whatever the type of its value.
- **FR-009**: Every tool's input schema MUST have `additionalProperties: false`, whatever
  parameters the task has, and MUST otherwise be unchanged.
- **FR-010**: FR-001 to FR-009 MUST apply the same way on stdio, on HTTP, and on the
  endpoint an `agent` task opens for its own agent.
- **FR-011**: The embeddable MCP server library MUST apply FR-001 to FR-009 for every
  engine. The parameters a task's descriptor lists are the names it accepts.
- **FR-012**: A call that names only declared parameters MUST behave exactly as it does
  today, including defaults, variadic values, value checks, and the missing-required-argument
  error, apart from the values FR-015 refuses. Absent arguments, `null`, and `{}` as the
  whole arguments MUST still run the task.
- **FR-013**: The MCP guide MUST say that a call naming an undeclared argument is refused
  before the task runs, and that tool schemas disallow other names.
- **FR-014**: The release notes MUST call out that a call with an undeclared argument or a
  null or object value, which used to succeed, now fails, and that an embedder's task
  descriptors define the accepted names.
- **FR-015**: A null, object, or nested-array value for a declared parameter, alone or as an
  array element, MUST be refused before anything executes, with the text
  `invalid arguments: parameter "<name>": <reason>`, where `<reason>` is
  `null is not a valid value`, `an object is not a valid value`, or
  `a nested array is not a valid value`. Strings, numbers, booleans, and arrays of those
  MUST keep working.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Across every scenario in this spec, a call with at least one undeclared name
  runs zero bodies, dependencies, and hooks, and is reported as an error in 100% of cases.
- **SC-002**: For any typo within two edits of a declared parameter's name, the error names
  that parameter or an equally close one, so an agent can correct the call with one retry.
- **SC-003**: 100% of the tools a server lists declare that no other argument names are
  allowed, on every transport and endpoint.
- **SC-004**: Every call that names only declared parameters gives the same output and exit
  status as before this change, apart from the values FR-015 refuses.
- **SC-005**: A call with several unknown names, repeated 100 times, returns identical text
  each time.

## Assumptions

- This is a breaking change for any client that sends extra names and relied on Rune
  ignoring them. Rune is before 1.0, so it ships with a release-notes callout (FR-014), as
  spec 029 did for a task that used to pass and can now fail.
- An embedder whose task descriptors leave out a parameter its engine reads will see those
  calls refused. The descriptor is the contract, and the release note covers this case too.
- The refusal text has no `invalid arguments:` prefix, because issue #36 gives the exact
  text and it already says what is wrong. Value violations keep their prefix.
- Issue #36 shows only a single unknown name. Reporting all of them at once is this spec's
  choice, so an agent does not need one round trip per typo.
- The pending end-to-end test the issue names (`TestMCPE2E_Pending_UnknownArgumentRejected`)
  is not in this repository. Equivalent binary-level integration tests will cover the
  acceptance scenarios.
- A null value for a parameter is refused, not treated as absent. The schema gives the
  parameter a type that null does not satisfy, and an agent can omit the argument on its
  next try.
- The line in spec 023's contract that calls this "existing behavior" was wrong when written.
  This feature makes the behavior true, and the spec 023 record stays as it is.
