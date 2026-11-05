# Feature Specification: Per-Task Opt-In for Destructive Tasks over MCP

**Feature Branch**: `031-mcp-destructive-optin`

**Created**: 2026-11-04

**Status**: Draft

**Input**: GitHub issue [#31](https://github.com/rune-task-runner/rune/issues/31)
(backlog MCP-1): "MCP: documented per-task opt-in for destructive tasks does not
exist (serve rejects --yes, AllowList unreachable)"

## Overview

The MCP guide's security section says agent access to destructive tasks is "an
explicit, per-task opt-in". No such opt-in exists. The server refuses every
`[confirm]` task, and nothing on the command line changes that:

```console
$ rune mcp --yes
rune: unknown flag: --yes
$ rune --yes mcp
rune: unknown flag: --yes
```

An agent that calls a `[confirm]` task always gets:

```text
task "clean" requires explicit approval and is not authorized for this session
```

There are two gaps. The server's authorization check has a server-wide switch,
but no flag sets it. Even with the switch set, the task's confirmation step
would still decline, because an MCP call has no person at a terminal to answer
it.

Refusing by default is correct and stays. This feature adds the missing opt-in.
The operator who starts the server names each destructive task an agent may
run:

```console
$ rune mcp --allow-destructive clean
```

Only the named tasks become callable, and the operator's grant stands in for
their confirmation. Every other `[confirm]` task stays refused, including when
a granted task reaches it as a dependency or hook. No switch grants all
destructive tasks at once, and `--yes` stays unavailable on `rune mcp` and
`rune serve`.

### Out of scope

These are tracked as separate issues:

- Operator allowlist and denylist for which tasks are listed and callable at
  all (`--allow`, `--deny`): [#39](https://github.com/rune-task-runner/rune/issues/39) (MCP-5).
- Refusing a call before any part of its dependency closure runs, with a message
  naming the refused destructive dependency: [#38](https://github.com/rune-task-runner/rune/issues/38) (MCP-4).
- Accepting `-f/--file` on `rune mcp`: [#44](https://github.com/rune-task-runner/rune/issues/44) (MCP-12).
- Per-call human approval inside the agent client: [#47](https://github.com/rune-task-runner/rune/issues/47) (MCP-13).
  This feature is a standing operator policy, not a per-call approval.
- Tool hints that account for dependencies: [#37](https://github.com/rune-task-runner/rune/issues/37) (MCP-3).

## Clarifications

### Session 2026-11-04

- Q: When an agent calls a task whose run includes a destructive task the
  operator did not name, should Rune refuse the call before anything runs, or
  only when the run reaches that task? → A: Only when the run reaches it, with
  the existing bare failure. Up-front refusal and the explanatory message stay
  in #38.
- Q: Should a destructive task the operator did not name still appear in the
  agent's tool list, and should its description mention the permission? → A:
  List it unchanged, with the destructive hint. Only a call reveals the
  refusal and the flag to pass on (FR-014). Filtering the list stays in #39.
- Q: Should `--allow-destructive` be accepted when the HTTP server listens on
  an address other than localhost? → A: Yes, the same as on localhost. The
  token is still required. A warning for binds other than localhost is #43.
- Q: When `--allow-destructive` names a task that exists but is not
  destructive, should the server still start? → A: Yes, with one warning line
  saying the grant has no effect (FR-011).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operator lets an agent run one named destructive task (Priority: P1)

An operator wants their coding agent to be able to run `clean`, which is marked
`[confirm]`, without leaving the agent conversation. They add
`--allow-destructive clean` to the server command in their agent client's
configuration. The agent can now call `clean` and gets its real output and exit
status. Nothing prompts and nothing waits for input.

**Why this priority**: This is the defect. The documented opt-in does not
exist, so destructive tasks are unreachable over MCP.

**Independent Test**: Start the server with `--allow-destructive clean` on a
Runefile where `clean` is `[confirm]` and its body writes a marker file. Call
`clean` as an MCP client. The marker exists and the result reports the body's
output and exit status.

**Acceptance Scenarios**:

1. **Given** a Runefile with a `[confirm]` task `clean` and a server started
   with `--allow-destructive clean`, **When** an agent calls `clean`, **Then**
   the body runs, the result carries its output and exit status 0, and no
   confirmation prompt is shown or awaited.
2. **Given** the same server, **When** `clean`'s body exits with status 3,
   **Then** the result reports exit status 3, as for any other task.
3. **Given** a server started with `--allow-destructive clean,reset`, and
   another started with `--allow-destructive clean --allow-destructive reset`,
   **When** an agent calls `clean` or `reset` on either, **Then** both run.
4. **Given** a server started with `--http --token-file tok
   --allow-destructive clean`, **When** an authenticated agent calls `clean`,
   **Then** the body runs, as on stdio.
5. **Given** a server started with `--allow-destructive clean,reset`, **When**
   it starts, **Then** its startup line on stderr names the granted tasks, for
   example `rune MCP server on stdio (destructive tasks allowed: clean, reset)`.

---

### User Story 2 - Destructive tasks not named stay refused (Priority: P1)

The grant is narrow. An operator who allowed `clean` has not allowed `reset`,
and has not allowed a destructive task that `clean` or any other task happens
to depend on. A server started without the flag behaves exactly as today:
every destructive task is refused and nothing runs.

**Why this priority**: The opt-in is only safe if it cannot widen. Without
this story, User Story 1 could expose more than the operator granted.

**Independent Test**: Start servers with and without the flag on a Runefile
with several `[confirm]` tasks, each writing its own marker file, wired as
direct tasks, dependencies, post-hooks, and failure hooks. Call each entry
point. Only markers of named tasks appear.

**Acceptance Scenarios**:

1. **Given** a server started without `--allow-destructive`, **When** an agent
   calls the `[confirm]` task `clean`, **Then** the call is refused, the body
   never runs, and the error reads
   `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`.
2. **Given** a server started with `--allow-destructive clean`, **When** an
   agent calls the `[confirm]` task `reset`, **Then** the call is refused with
   the same message naming `reset`, and `reset`'s body never runs.
3. **Given** `release: clean`, where `release` is not destructive, and a
   server started with `--allow-destructive clean`, **When** an agent calls
   `release`, **Then** `clean` runs as a dependency and then `release` runs.
4. **Given** `release: clean` and a server started without the flag, **When**
   an agent calls `release`, **Then** `clean` does not run, `release`'s body
   does not run, and the call reports a failure.
5. **Given** `clean: wipe`, where both are `[confirm]`, and a server started
   with `--allow-destructive clean`, **When** an agent calls `clean`, **Then**
   `wipe` does not run, `clean`'s body does not run, and the call reports a
   failure.
6. **Given** a non-destructive task declared `build: && wipe` (post-hook) or
   `build: || wipe` (failure hook), where `wipe` is `[confirm]` and not named,
   **When** the hook would fire, **Then** `wipe` does not run.

---

### User Story 3 - Mistakes in the flag stop the server at startup (Priority: P2)

An operator who mistypes a task name in the flag finds out immediately, not
when an agent's call is refused hours later. The server exits before it runs
anything from the Runefile and names the problem.

**Why this priority**: A typo would otherwise leave the operator believing they
granted access they did not. It does not block the core opt-in.

**Independent Test**: Start the server with each malformed value below and
check the exit status and stderr. Use a `[context]` hook that writes a marker
file to prove nothing from the Runefile ran.

**Acceptance Scenarios**:

1. **Given** a Runefile with a task `clean`, **When** the operator runs
   `rune mcp --allow-destructive claen`, **Then** the server exits with
   status 2, the message names the unknown task `claen` and suggests `clean`,
   and no Runefile code (including the `[context]` hook) runs.
2. **Given** any Runefile, **When** the operator runs
   `rune mcp --allow-destructive ""` or `rune mcp --allow-destructive clean,`,
   **Then** the server exits with status 2 and says a task name is empty.
3. **Given** a non-destructive task `build`, **When** the operator runs
   `rune mcp --allow-destructive build`, **Then** the server starts and prints
   one warning line saying `build` is not destructive, so the grant has no
   effect.
4. **Given** any Runefile, **When** the operator runs `rune mcp --yes` or
   `rune --yes mcp`, **Then** the command fails with status 2 as an unknown
   flag, as today.
5. **Given** any Runefile, **When** the operator runs
   `rune mcp --allow-destructive '*'`, **Then** the server exits with status 2
   because `*` is not a task name. No wildcard exists.

---

### User Story 4 - The documentation describes the opt-in that exists (Priority: P3)

A reader of the MCP guide and the CLI reference learns how to grant an agent a
destructive task, that each task must be named, and that there is no blanket
switch. No page claims an opt-in that the binary does not have.

**Why this priority**: The doc claim is how the defect was found. Once the
flag exists, the docs must show how to use it.

**Independent Test**: Read the security section of the MCP guide and the
`serve` entry of the CLI reference, then run `rune serve --help`. All three
name `--allow-destructive` with the same meaning. The automated CLI reference
check passes.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader opens its security section,
   **Then** it shows `rune mcp --allow-destructive clean`, states that each
   destructive task must be named, and states that no option grants all of
   them.
2. **Given** the CLI reference, **When** a reader looks up `rune serve` /
   `rune mcp`, **Then** `--allow-destructive` is listed with its
   comma-separated and repeatable forms.
3. **Given** `rune serve --help`, **When** it is printed, **Then** it lists
   the flag and includes an example.
4. **Given** the MCP guide's section on `agent` task bodies, **When** a reader
   reads it, **Then** it says that the tasks an `agent` task exposes to its own
   agent never include destructive tasks, instead of describing an opt-in.

---

### Edge Cases

- **Private destructive task.** `--allow-destructive _wipe`, where `_wipe` is
  private and `[confirm]`, is accepted. `_wipe` still never becomes a callable
  tool, since private tasks never do. It runs when a callable task reaches it
  as a dependency or hook.
- **Task restricted to another OS.** A named task that exists in the Runefile
  but is restricted to another OS is accepted without a warning. It is not
  exposed on this host, so the grant has no effect here, and the startup
  line does not list it. One client configuration may be shared across hosts.
- **Imported task.** Names use the Runefile form, `tools::clean`, the same form
  `rune tools::clean` accepts. The refusal message uses that form so it can be
  copied into the flag. The MCP tool-name form (`tools__clean`) is an unknown
  task name, and the usual close-name suggestion applies.
- **Repeated name.** `--allow-destructive clean,clean` grants `clean` once and
  is not an error.
- **Concurrent calls.** Two sessions on one HTTP server may both call a
  granted task. Each call runs. A grant is not used up by a call; it lasts for
  the server's lifetime.
- **HTTP bind other than localhost.** `rune serve --http --addr :7777
  --token-file tok --allow-destructive clean` starts like any other server.
  Every caller still needs the token. This feature adds no warning about the
  bind address; [#43](https://github.com/rune-task-runner/rune/issues/43)
  covers that.
- **Refused dependency after other work.** For `release: build clean deploy`
  with `clean` not granted, `build` may already have run when `clean` is
  refused. The call reports the same bare failure as today, without naming
  `clean`. Refusing the whole run before it starts, with a message naming the
  dependency, is [#38](https://github.com/rune-task-runner/rune/issues/38).
- **CLI runs.** `rune clean`, `rune --yes clean`, and the terminal prompt rules
  from spec 027 are unchanged. The new flag exists only on `serve` / `mcp`.
- **`agent` task endpoint.** When an `agent` task runs, Rune exposes tasks to
  that agent over a private endpoint. That endpoint keeps refusing every
  destructive task. Neither `--allow-destructive` nor the CLI's `--yes`
  reaches it.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `rune serve` and its alias `rune mcp` MUST accept
  `--allow-destructive` with one or more task names separated by commas, and
  MUST accept the flag more than once. The granted set is the union of all
  names given.
- **FR-002**: When an agent calls a destructive task that is in the granted
  set, its body MUST run. The grant counts as the task's confirmation: no
  prompt is shown and nothing waits for input.
- **FR-003**: Without the flag, every destructive task MUST be refused and
  nothing MUST execute for that call, as today.
- **FR-004**: A direct call to a destructive task that is not in the granted
  set MUST be refused before anything executes. The error result MUST name the
  task and the exact flag that permits it:
  `task "<name>" is destructive; to permit it, add <name> to --allow-destructive and restart the server`.
  It says to add the name because the server may already hold other grants.
  A refusal by the operator allowlist (unexposed until #39) MUST use its own
  text, `task "<name>" is not authorized for this session`, and MUST NOT
  mention the flag.
- **FR-005**: A destructive task that is not in the granted set MUST NOT run,
  however the run reaches it: as a dependency, a post-hook, or a failure hook.
  This holds even when the called task is itself granted. A grant never
  extends to another task.
- **FR-006**: A destructive task that is in the granted set MUST run when a
  callable task reaches it as a dependency or hook, including when the granted
  task is private.
- **FR-007**: No flag, value, or wildcard MAY grant all destructive tasks.
  `--yes` MUST remain an unknown flag for `serve` and `mcp` in both positions
  (`rune mcp --yes`, `rune --yes mcp`), exiting with status 2.
- **FR-008**: A name that is not a task in the loaded Runefile (including its
  imports) MUST be a usage error with exit status 2. The message MUST name the
  unknown task and suggest the closest known task when one exists.
- **FR-009**: An empty task name, from an empty value or an empty item between
  commas, MUST be a usage error with exit status 2.
- **FR-010**: Usage errors from FR-008 and FR-009 MUST occur before any
  Runefile code runs, including the `[context]` hook.
- **FR-011**: A name of a known task that is not destructive MUST NOT stop the
  server. The server MUST print one warning line on stderr naming the task and
  saying the grant has no effect.
- **FR-012**: When the granted set contains at least one destructive task that
  can run on this host, the server's startup line on stderr MUST list those
  tasks in the order given.
  Otherwise the startup line is unchanged.
- **FR-013**: The granted set MUST apply the same way on the stdio and HTTP
  transports and to every session, for the server's lifetime.
- **FR-014**: The tool list MUST NOT change. Destructive tasks stay listed with
  the destructive hint whether or not they are granted.
- **FR-015**: The endpoint that an `agent` task exposes to its agent MUST keep
  refusing every destructive task. Its refusal MUST NOT suggest a restart, since
  no flag can permit the task there:
  `task "<name>" is destructive and is not allowed in this session`.
- **FR-016**: The embeddable MCP server library MUST express destructive
  authorization only as a set of task names. Its server-wide switch MUST be
  removed, and no replacement may grant all destructive tasks. The release
  notes MUST call out this breaking change for embedders.
- **FR-017**: The MCP guide's security section, the CLI reference, and
  `rune serve --help` MUST document `--allow-destructive`: per-task naming, the
  comma-separated and repeatable forms, and the absence of a blanket switch.
  The MCP guide's `agent` task section MUST state that an `agent` task's own
  endpoint never permits destructive tasks. The `[confirm]` paragraph of the
  language guide MUST name `--allow-destructive` as the MCP opt-in, and neither
  the agent-driven example's README nor `docs/use-cases/mcp-agents.md` MAY tell
  readers that `--yes` approves a task over MCP. The automated CLI reference
  check MUST pass.

### Key Entities

- **Destructive task**: A task the Runefile author marked `[confirm]`. Its
  meaning comes from the author and does not change with this feature.
- **Granted set**: The task names the operator passed with
  `--allow-destructive` when starting the server. It is fixed for the server's
  lifetime and cannot be changed by an agent.
- **Operator**: The person who writes the command that starts the server,
  usually in an agent client's configuration. The operator is trusted to
  grant. The agent is not.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With one destructive task granted, every agent call to that task
  in the test suite returns the body's real output and exit status, and none
  shows or waits on a confirmation prompt.
- **SC-002**: Across direct calls, dependencies, post-hooks, and failure
  hooks, no destructive task outside the granted set executes in any test (zero
  marker files from ungranted tasks).
- **SC-003**: With no flag, zero destructive tasks execute across the same
  call paths, matching the current release.
- **SC-004**: Every refusal of a directly called destructive task contains the
  exact `--allow-destructive <name>` text an operator can copy to permit it.
- **SC-005**: Every invalid flag value tested (unknown name, empty name,
  wildcard) ends the server with exit status 2 before any Runefile code runs.
- **SC-006**: The MCP guide, the CLI reference, and `rune serve --help` all
  describe the same flag, and the automated CLI reference check passes. No page
  claims an opt-in the binary lacks.

## Assumptions

- The flag is named `--allow-destructive`, as in the issue's backlog proposal.
  The issue's first suggestion, `--allow`, is reserved for the operator
  allowlist in [#39](https://github.com/rune-task-runner/rune/issues/39).
- A grant is a standing operator policy fixed at server start. Approving each
  call is a separate feature
  ([#47](https://github.com/rune-task-runner/rune/issues/47)) and can coexist
  with this one.
- Unknown names fail with exit status 2 and a suggestion, matching the
  convention #39 proposes for `--allow` and `--deny`.
- A non-destructive name warns instead of failing. A shared client
  configuration then keeps working when the author removes `[confirm]` from a
  task.
- When an ungranted destructive dependency is refused, the call reports a
  failure. Making that failure name the dependency and refusing before any
  part of the run starts is
  [#38](https://github.com/rune-task-runner/rune/issues/38).
- Rune is before 1.0, so removing the library's server-wide switch is
  acceptable with a release-notes callout.
- The pending end-to-end test the issue names
  (`TestMCPE2E_Pending_DestructiveOptIn`) is not in this repository.
  Equivalent binary-level integration tests will cover the acceptance
  scenarios.
- The operator allowlist that the issue title calls unreachable stays
  unexposed here. #39 exposes it.
