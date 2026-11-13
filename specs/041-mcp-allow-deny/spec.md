# Feature Specification: Operator Allowlist and Denylist for MCP Tasks

**Feature Branch**: `041-mcp-allow-deny`

**Created**: 2026-11-12

**Status**: Draft

**Input**: GitHub issue [#39](https://github.com/rune-task-runner/rune/issues/39)
(backlog MCP-5): "Expose the operator allowlist on rune mcp"

## Overview

`rune mcp` exposes every non-private task of the Runefile as a tool. The only
way to expose fewer is to edit the Runefile and mark tasks private, which
changes them for every user of that Runefile, not for one agent session.

The embeddable server already has an allowlist, but no flag sets it, and it
only refuses calls. The refused tasks stay in the tool list, so an agent still
sees them, tries them, and gets:

```text
task "deploy" is not authorized for this session
```

This feature lets the operator who starts the server choose the tool surface:

```console
$ rune mcp --allow build,test
$ rune mcp --deny deploy,publish
```

With `--allow`, only the named tasks are tools. With `--deny`, the named tasks
are not tools. A task that is not a tool is absent from the tool list and a
call to it gets the same answer as a call to a name that never existed. The
filters decide what an agent can call directly. They do not change what a
called task runs: its dependencies and hooks run as the Runefile says, and
destructive tasks still need `--allow-destructive` (spec 031, spec 034).

### Out of scope

- Wildcards or patterns in either flag. Each task is named.
- Stopping a task from running as a dependency or hook of an exposed task.
  The filters cover direct calls only. See Assumptions.
- Changing the filters while the server runs, or per session on one HTTP
  server. They are fixed at server start.
- Filters on the endpoint an `agent` task opens for its own agent.
- Per-call human approval:
  [#47](https://github.com/rune-task-runner/rune/issues/47) (MCP-13).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operator exposes only the tasks one agent needs (Priority: P1)

An operator wants a coding agent to build and test, and nothing else. They add
`--allow build,test` to the server command in the agent client's
configuration. The agent's tool list holds exactly `build` and `test`. It
cannot find or call `deploy`, and the Runefile is unchanged for everyone else.

**Why this priority**: This is the feature the issue asks for. Today the
operator has no way to narrow the surface without editing the Runefile.

**Independent Test**: Start the server with `--allow build,test` on a Runefile
with `build`, `test`, and `deploy`, each writing its own marker file. List the
tools and call each name as an MCP client. The list holds two tools, `build`
and `test` run, and `deploy` is rejected as an unknown tool with no marker.

**Acceptance Scenarios**:

1. **Given** a Runefile with tasks `build`, `test`, and `deploy`, and a server
   started with `--allow build,test`, **When** an agent lists the tools,
   **Then** the list holds exactly `build` and `test`, each with the same
   description, schema, and hints as without the flag.
2. **Given** the same server, **When** an agent calls `build`, **Then** the
   body runs and the result carries its output and exit status, as without the
   flag.
3. **Given** the same server, **When** an agent calls `deploy`, **Then** the
   call is rejected as an unknown tool, with the same response as a call to a
   name no task has, and `deploy`'s body never runs.
4. **Given** servers started with `--allow build,test` and with
   `--allow build --allow test`, **When** an agent lists the tools on either,
   **Then** both lists are the same.
5. **Given** a server started with `--http --token-file tok --allow build`,
   **When** an authenticated agent lists the tools, **Then** the list holds
   exactly `build`, as on stdio.
6. **Given** a server started with `--allow build,test` on a Runefile that
   would expose 14 tasks, **When** it starts, **Then** its startup line on
   stderr reads `rune MCP server on stdio (2 of 14 tasks exposed)`.

---

### User Story 2 - Operator hides a few tasks and keeps the rest (Priority: P1)

An operator is happy with most of the Runefile but does not want the agent to
call `deploy` or `publish`. Naming every other task in `--allow` would break
each time someone adds a task. They pass `--deny deploy,publish`. Every other
exposable task stays a tool, including tasks added later.

**Why this priority**: The issue asks for both flags. A denylist is the only
form that keeps working as the Runefile grows.

**Independent Test**: Start the server with `--deny deploy` on a Runefile with
`build`, `test`, and `deploy`. List the tools and call `deploy`. The list
holds `build` and `test`, and `deploy` is rejected as an unknown tool.

**Acceptance Scenarios**:

1. **Given** a Runefile with tasks `build`, `test`, and `deploy`, and a server
   started with `--deny deploy`, **When** an agent lists the tools, **Then**
   the list holds `build` and `test` and not `deploy`.
2. **Given** the same server, **When** an agent calls `deploy`, **Then** the
   call is rejected as an unknown tool and `deploy`'s body never runs.
3. **Given** a server started with `--allow build,test,deploy --deny deploy`,
   **When** an agent lists the tools, **Then** the list holds `build` and
   `test`. A task named in both flags is not a tool.
4. **Given** a server started with `--deny deploy --deny publish` and another
   with `--deny deploy,publish`, **When** an agent lists the tools on either,
   **Then** both lists are the same.

---

### User Story 3 - An exposed task still runs its dependencies (Priority: P1)

The operator allowed `test`, and `test` depends on `build` and on a private
`_setup`. They did not list those, and should not have to. When the agent
calls `test`, the whole run happens as it would from the command line. The
filters decide what the agent can call, not what a task is made of.

Destructive tasks keep their own rule. If `test` reaches a `[confirm]` task
the operator did not name in `--allow-destructive`, the call is refused before
anything runs, as today.

**Why this priority**: Without this, `--allow` would be unusable on any
Runefile whose tasks have dependencies, which is most of them.

**Independent Test**: Start the server with `--allow test` on a Runefile where
`test: build`, each writing a marker file. Call `test`. Both markers exist.
Call `build` directly. It is rejected as an unknown tool.

**Acceptance Scenarios**:

1. **Given** `test: build` and a server started with `--allow test`, **When**
   an agent calls `test`, **Then** `build` runs as a dependency and then
   `test` runs.
2. **Given** the same server, **When** an agent calls `build`, **Then** the
   call is rejected as an unknown tool.
3. **Given** `test: build` and a server started with `--deny build`, **When**
   an agent calls `test`, **Then** `build` runs as a dependency and then
   `test` runs.
4. **Given** `test: && report` (post-hook) or `test: || cleanup` (failure
   hook) and a server started with `--allow test`, **When** the hook fires,
   **Then** it runs, as without the flag.
5. **Given** `release: clean`, where `clean` is `[confirm]`, and a server
   started with `--allow release`, **When** an agent calls `release`, **Then**
   the call is refused before anything runs with the existing message naming
   `clean` and `--allow-destructive`.
6. **Given** the same Runefile and a server started with
   `--allow release --allow-destructive clean`, **When** an agent calls
   `release`, **Then** `clean` runs and then `release` runs, and `clean` is
   still not a tool.
7. **Given** a `[confirm]` task `clean` and a server started with
   `--allow clean`, **When** an agent calls `clean`, **Then** the call is
   refused with the existing destructive message. `--allow` exposes a task and
   never stands in for `--allow-destructive`.

---

### User Story 4 - Mistakes in the flags stop the server at startup (Priority: P2)

An operator who mistypes a name in `--deny` would otherwise believe a task is
hidden while the agent can still call it. The server exits before it runs
anything from the Runefile and names the problem.

**Why this priority**: A silent typo in `--deny` exposes what the operator
meant to hide. It does not block the core filtering.

**Independent Test**: Start the server with each malformed value below and
check the exit status and stderr. Use a `[context]` hook that writes a marker
file to prove nothing from the Runefile ran.

**Acceptance Scenarios**:

1. **Given** a Runefile with a task `build`, **When** the operator runs
   `rune mcp --allow biuld`, **Then** the server exits with status 2, the
   message reads `unknown task in --allow: biuld (did you mean "build"?)`, and
   no Runefile code (including the `[context]` hook) runs.
2. **Given** the same Runefile, **When** the operator runs
   `rune mcp --deny biuld`, **Then** the server exits with status 2 and the
   message names `--deny`, the unknown task, and the suggestion.
3. **Given** any Runefile, **When** the operator runs `rune mcp --allow ""`,
   `rune mcp --allow build,`, or the same with `--deny`, **Then** the server
   exits with status 2 and says a task name is empty, naming the flag.
4. **Given** any Runefile, **When** the operator runs `rune mcp --allow '*'`,
   **Then** the server exits with status 2 because `*` is not a task name.
5. **Given** a private task `_setup`, **When** the operator runs
   `rune mcp --allow _setup` or `rune mcp --deny _setup`, **Then** the server
   starts and prints one warning line saying the flag has no effect for
   `_setup` because it is never exposed to agents.
6. **Given** a Runefile with a task `build`, **When** the operator runs
   `rune mcp --allow build --deny build`, **Then** the server starts with no
   tools and prints one warning line saying no tasks are exposed.

---

### User Story 5 - The documentation describes both flags (Priority: P3)

A reader of the MCP guide and the CLI reference learns how to narrow the tool
surface, how the two flags combine, and that dependencies of an exposed task
still run.

**Why this priority**: The flags are useless if operators cannot find them or
misread what `--deny` promises.

**Independent Test**: Read the security section of the MCP guide and the
`serve` entry of the CLI reference, then run `rune serve --help`. All three
name `--allow` and `--deny` with the same meaning. The automated CLI reference
check passes.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader opens its security section,
   **Then** it shows `rune mcp --allow build,test` and `rune mcp --deny deploy`,
   states that a task in both is hidden, and states that the dependencies and
   hooks of an exposed task still run.
2. **Given** the CLI reference, **When** a reader looks up `rune serve` /
   `rune mcp`, **Then** both flags are listed with their comma-separated and
   repeatable forms.
3. **Given** `rune serve --help`, **When** it is printed, **Then** it lists
   both flags and includes an example of each.
4. **Given** the MCP guide, **When** a reader looks for how `--allow` relates
   to `--allow-destructive`, **Then** it says the first chooses which tasks
   are tools and the second permits `[confirm]` tasks to run, and that a
   `[confirm]` task needs the second whichever filters are set.

---

### Edge Cases

- **Task restricted to another OS.** A name that exists in the Runefile but is
  restricted to another OS is accepted in either flag without a warning. It is
  not exposed on this host. One client configuration may be shared across
  hosts.
- **`[context]` hook.** The `[context]` hook is never a tool. Naming it in
  either flag is accepted with the same warning as for a private task. Its
  output is still delivered as server instructions.
- **Imported task.** Names use the Runefile form, `tools::build`, the same
  form `rune tools::build` accepts. The MCP tool-name form (`tools__build`) is
  an unknown task name, and the usual close-name suggestion applies.
- **Repeated name.** `--allow build,build` is the same as `--allow build` and
  is not an error.
- **Nothing left to expose.** When the filters leave no task exposed on this
  host, the server starts, serves an empty tool list, and prints one warning.
  It does not exit: an allowlist of tasks restricted to another OS is a valid
  shared configuration.
- **Hidden task named in a refusal.** When an exposed task reaches an
  ungranted `[confirm]` task that is hidden, the refusal still names that
  task, because the operator needs the name to grant it. The agent learns the
  name and still cannot call it.
- **Hidden task granted as destructive.** `--allow-destructive clean` with
  `clean` hidden is accepted without a new warning. The grant takes effect
  when an exposed task reaches `clean`.
- **Hints of an exposed task.** A task's destructive, network, and read-only
  hints describe what a run of it can do, including through hidden
  dependencies. The filters do not change them.
- **CLI runs.** `rune build`, `rune --list`, and every other command are
  unchanged. The flags exist only on `serve` / `mcp`.
- **`agent` task endpoint.** The endpoint an `agent` task opens for its own
  agent exposes the same tasks as today. Neither flag reaches it.
- **Tool list over time.** The exposed set is fixed at server start. The
  server never announces a tool list change because of the filters.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `rune serve` and its alias `rune mcp` MUST accept `--allow` and
  `--deny`, each with one or more task names separated by commas, and MUST
  accept each flag more than once. Each flag's set is the union of all names
  given to it.
- **FR-002**: The exposed set MUST be computed once at server start: the tasks
  that would be tools without the flags, kept only if named in `--allow` when
  `--allow` is given, and then without every task named in `--deny`. A task
  named in both flags is not exposed.
- **FR-003**: The tool list MUST hold exactly the exposed set. A task outside
  it MUST NOT appear in any listing the server gives an agent.
- **FR-004**: A call to a task outside the exposed set MUST be rejected as an
  unknown tool before anything executes. The response MUST be the one the
  server gives for a name no task has, so an agent cannot tell a hidden task
  from a missing one by calling it. The text
  `task "<name>" is not authorized for this session` is no longer produced by
  `rune serve` or `rune mcp`.
- **FR-005**: The filters MUST NOT widen the surface. A private task, the
  `[context]` hook, and a task restricted to another OS MUST NOT become tools
  because `--allow` names them.
- **FR-006**: A call to an exposed task MUST run its dependencies, post-hooks,
  and failure hooks exactly as without the flags, whether or not those tasks
  are exposed.
- **FR-007**: The filters MUST NOT change destructive authorization. A
  `[confirm]` task, called directly or reached by an exposed task, MUST still
  be named in `--allow-destructive` to run, with the refusals of spec 031 and
  spec 034 unchanged. `--allow` MUST NOT grant a destructive task.
- **FR-008**: The description, input schema, and hints of an exposed task MUST
  be the same as without the flags.
- **FR-009**: A name in either flag that is not a task in the loaded Runefile
  (including its imports) MUST be a usage error with exit status 2. The
  message MUST name the flag and the unknown task, and suggest the closest
  known task when one exists: `unknown task in --allow: <name> (did you mean
  "<task>"?)`, and the same with `--deny`.
- **FR-010**: An empty task name in either flag, from an empty value or an
  empty item between commas, MUST be a usage error with exit status 2 that
  names the flag.
- **FR-011**: Usage errors from FR-009 and FR-010 MUST occur before any
  Runefile code runs, including the `[context]` hook.
- **FR-012**: A name of a known task that can never be a tool on any host (a
  private task or the `[context]` hook) MUST NOT stop the server. The server
  MUST print one warning line on stderr naming the flag and the task and
  saying it has no effect. A task restricted to another OS MUST be accepted
  without a warning.
- **FR-013**: When either flag is given and no task is exposed on this host,
  the server MUST start with an empty tool list and print one warning line on
  stderr saying no tasks are exposed.
- **FR-014**: When either flag is given, the startup line on stderr MUST
  report how many tasks are exposed out of how many would be without the
  flags, for example `rune MCP server on stdio (2 of 14 tasks exposed)`, and
  with a destructive grant
  `rune MCP server on stdio (2 of 14 tasks exposed; destructive tasks allowed: clean)`.
  With neither flag the startup line MUST be unchanged.
- **FR-015**: The exposed set MUST apply the same way on the stdio and HTTP
  transports and to every session, for the server's lifetime.
- **FR-016**: With neither flag, every listing, call, refusal, and startup
  line MUST be the same as before this feature.
- **FR-017**: The endpoint that an `agent` task exposes to its agent MUST be
  unchanged.
- **FR-018**: The embeddable MCP server library MUST offer the same filtering
  to embedders: an allowlist and a denylist of task names that decide which
  tasks are tools, applied to listing and calling alike. Its allowlist no
  longer leaves a refused task listed. This replaces the rule in spec 033
  FR-010 that an allowlist does not change the tool list. The release notes
  MUST call out the change for embedders.
- **FR-019**: The MCP guide's security section, the CLI reference, and
  `rune serve --help` MUST document `--allow` and `--deny`: the
  comma-separated and repeatable forms, that a task in both is hidden, that
  dependencies and hooks of an exposed task still run, and that
  `--allow-destructive` is still needed for `[confirm]` tasks. The automated
  CLI reference check MUST pass.

### Key Entities

- **Exposable task**: A task that is a tool when no filter is given: not
  private, not the `[context]` hook, and able to run on this host.
- **Allowlist**: The task names the operator passed with `--allow`. When
  given, only these exposable tasks are tools.
- **Denylist**: The task names the operator passed with `--deny`. These tasks
  are never tools.
- **Exposed set**: The exposable tasks left after the allowlist and denylist.
  It is fixed for the server's lifetime and cannot be changed by an agent.
- **Granted set**: The `[confirm]` tasks named with `--allow-destructive`
  (spec 031). It is independent of the exposed set.
- **Operator**: The person who writes the command that starts the server,
  usually in an agent client's configuration.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With `--allow build,test`, an agent's tool list holds exactly
  those two tasks in every test, on both transports.
- **SC-002**: Across every filter combination tested, zero tasks outside the
  exposed set appear in a tool list, and zero run from a direct call (no
  marker files from hidden tasks called by name).
- **SC-003**: A call to a hidden task and a call to a name no task has produce
  the same response in every test.
- **SC-004**: Every dependency, post-hook, and failure hook of an exposed task
  runs in the tests whether or not it is exposed, and every ungranted
  `[confirm]` task stays refused under every filter combination.
- **SC-005**: Every invalid flag value tested (unknown name, empty name,
  wildcard) ends the server with exit status 2 before any Runefile code runs.
- **SC-006**: With neither flag, the tool list, call results, and startup line
  match the current release in the existing test suite, with no test changed
  except those that pinned the old allowlist behaviour.
- **SC-007**: The MCP guide, the CLI reference, and `rune serve --help` all
  describe the same two flags, and the automated CLI reference check passes.

## Assumptions

- The flags are named `--allow` and `--deny`, as the issue proposes. Spec 031
  reserved `--allow` for this feature.
- The filters cover direct calls only. A hidden or denied task still runs when
  an exposed task reaches it as a dependency or hook. The issue states this
  for `--allow`. This spec applies the same rule to `--deny`, so both flags
  mean one thing: what an agent can call. An operator who must stop a task
  from running at all hides the tasks that depend on it too, or marks it
  `[confirm]`, which is refused wherever a run reaches it. The MCP guide says
  so.
- A task named in both flags is hidden. Deny wins, because hiding too much is
  the safer mistake.
- Unknown names fail with exit status 2 and a suggestion, as the issue's
  acceptance criteria require and as `--allow-destructive` does.
- A private or `[context]` name warns instead of failing, matching the
  non-destructive warning of spec 031. A shared client configuration then
  keeps working when an author makes a task private.
- The startup line reports a count, not the task names. A denylist on a large
  Runefile would otherwise print every remaining task.
- A refusal may name a hidden destructive task. The operator needs the name to
  grant it, and knowing a name does not let an agent call it.
- Rune is before 1.0, so changing what the library's allowlist does to the
  tool list is acceptable with a release-notes callout.
- The pending end-to-end test the issue names
  (`TestMCPE2E_Pending_AllowList`) is not in this repository, as the issue
  itself notes. Equivalent binary-level integration tests cover the
  acceptance scenarios.
