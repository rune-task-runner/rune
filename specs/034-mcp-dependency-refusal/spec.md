# Feature Specification: Refuse a Destructive Dependency Before the Call Runs

**Feature Branch**: `034-mcp-dependency-refusal`

**Created**: 2026-11-10

**Status**: Draft

**Input**: GitHub issue [#38](https://github.com/rune-task-runner/rune/issues/38)
(backlog MCP-4): "Dependency refusal returns a bare [exit 1]"

## Overview

An agent calls a task through Rune's MCP server. If the call reaches a destructive (`[confirm]`) task
that the operator has not allowed, Rune declines that task only when the run gets to it. Two things go
wrong:

- **Earlier work has already happened.** Dependencies listed before the destructive one have run, and
  so has the task body when the destructive task is an `&&` hook.
- **The agent learns nothing.** The result is a bare `[exit 1]`, with no reason and no way to fix it.

```rune
# Builds the artifacts.
build:
    ./build.sh

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    rm -rf dist

# Cuts a release.
release: build clean
    ./release.sh

# Publishes, then cleans.
publish: && clean
    ./publish.sh

# Wipes everything.
[confirm("Wipe?")]
wipe:
    ./wipe.sh

# Resets, which also wipes.
[confirm("Reset?")]
reset: wipe
    ./reset.sh

# Deploys, wiping on failure.
deploy: || wipe
    ./deploy.sh

# Ships after a private scrub.
ship: _scrub
    ./ship.sh

[private]
[confirm("Scrub?")]
_scrub:
    ./scrub.sh
```

These results come from a binary built on `main` at `840ebef`. Each body wrote a marker file, so
"ran" means the marker existed after the call.

| Call | Grant | Today | After |
|---|---|---|---|
| `release` | none | `[exit 1]`; `build` ran | Refused; nothing runs; names `clean` and the flag |
| `release` | `clean` | All three run, `[exit 0]` | Unchanged |
| `publish` | none | `[exit 1]`; `publish` ran | Refused; nothing runs; names `clean` |
| `reset` | `reset` | `[exit 1]`; nothing ran | Refused; names `wipe` |
| `ship` | none | `[exit 1]`; nothing ran | Refused; names `_scrub` |
| `deploy`, which fails | none | `deploy` ran; its own failure, with the warning `failure hook wipe failed: task "wipe" was not confirmed` | Refused; nothing runs; names `wipe` |
| `clean` | none | `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server` | Unchanged |

After this feature, Rune works out everything a call can run before running any of it. If that
includes a destructive task the operator has not allowed, Rune refuses the whole call. The refusal
names each such task and tells the operator what to add to `--allow-destructive`.

### Out of scope

- Which tasks an agent sees, and their hints. `tools/list` and every hint stay as spec 033 left them.
- What a grant covers. A grant still covers only its own task (spec 031 FR-005).
- CLI runs. `rune release` still prompts at a terminal, and without one still fails when it reaches
  `clean`, as spec 027 defined.
- An operator list of callable tasks (`--allow`, `--deny`):
  [#39](https://github.com/rune-task-runner/rune/issues/39) (MCP-5).
- Per-call human approval inside the agent client:
  [#47](https://github.com/rune-task-runner/rune/issues/47) (MCP-13).
- Enforcing `[readonly]` or `[network]` at run time:
  [#51](https://github.com/rune-task-runner/rune/issues/51) (MCP-17).
- A task body that starts Rune itself (`rune clean` inside a recipe). Only declared dependencies and
  hooks count, as in spec 033.

## Clarifications

### Session 2026-11-10

- Q: Does a `||` failure hook count for the up-front check, though it runs only when something
  fails? → A: Yes. The check counts the same tasks spec 033 uses for the destructive hint:
  dependencies, `&&` hooks, and `||` hooks. `deploy: || wipe` is refused until `wipe` is granted,
  even when `deploy` would succeed, so a task never runs without its cleanup being able to run.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A call that needs an unallowed destructive task does nothing and says why (Priority: P1)

An agent calls `release`. Its dependency `clean` is destructive, and the operator has not allowed it.
Rune refuses the call before `build` or anything else runs. The agent gets a message naming `clean`
and the flag, which it can pass on to its operator.

**Why this priority**: This is the issue. Today the agent leaves side effects behind and gets no
reason. Both make an agent retry blindly or give up.

**Independent Test**: Serve the Overview Runefile with no grant and call `release`. The result is an
error naming `clean` and `--allow-destructive`, and no marker file exists.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile and no grant, **When** an agent calls `release`, **Then** the
   result is an error whose text is `task "release" can run destructive task "clean"; to permit it, add
   clean to --allow-destructive and restart the server`, and neither `build` nor `clean` nor
   `release` ran.
2. **Given** no grant, **When** an agent calls `publish`, **Then** the call is refused the same way,
   naming `clean`, and `publish` did not run.
3. **Given** a chain `outer: inner` and `inner: clean`, **When** an agent calls `outer`, **Then** the
   refusal names `clean`, and neither `inner` nor `outer` ran.
4. **Given** no grant, **When** an agent calls `ship`, **Then** the refusal names the private task
   `_scrub`, which is still not listed as a tool.
5. **Given** a module task `tools::clean` with `[confirm]` and `shipit: tools::clean`, **When** an
   agent calls `shipit`, **Then** the refusal names `tools::clean`, the name the flag accepts.
6. **Given** no grant, **When** an agent calls `deploy`, whose `||` hook `wipe` is destructive,
   **Then** the refusal names `wipe`, and `deploy` did not run, whether or not it would have failed.

---

### User Story 2 - The operator follows the message once (Priority: P1)

The operator reads the refusal and restarts the server with the names it gives. The same call then
runs. One restart is always enough, even when the call needs several destructive tasks or the called
task is destructive too.

**Why this priority**: A refusal that leads to another refusal after the restart wastes the operator's
time. Without this story, the message only moves the problem.

**Independent Test**: For each refused call in the Overview, restart with the names the refusal gives
added to the existing grants, and call again. The call runs.

**Acceptance Scenarios**:

1. **Given** `release` was refused naming `clean`, **When** the operator restarts with
   `--allow-destructive clean` and the agent calls `release` again, **Then** `build`, `clean`, and
   `release` run.
2. **Given** `release: build clean wipe` with both `clean` and `wipe` destructive and no grant,
   **When** an agent calls `release`, **Then** one refusal names both tasks, and its flag text reads
   `add clean,wipe to --allow-destructive`.
3. **Given** `reset` (destructive) depends on `wipe` (destructive) and no grant, **When** an agent
   calls `reset`, **Then** one refusal names `reset` and `wipe`, `reset` first, so a single restart
   permits the call.
4. **Given** `--allow-destructive reset` and no grant for `wipe`, **When** an agent calls `reset`,
   **Then** the refusal names only `wipe`. A granted task is never listed.
5. **Given** `clean` reaches no other destructive task and is not granted, **When** an agent calls
   `clean`, **Then** the text is byte-identical to today's direct refusal.

---

### User Story 3 - Every server Rune builds refuses the same way (Priority: P2)

An `agent` task opens a private MCP endpoint for its own agent. That endpoint never has grants and has
no flag to name. It refuses the same calls before anything runs, and says the task is not allowed in
the session. A program that embeds Rune's server library gets the same refusal when its engine says
which destructive tasks each task can run.

**Why this priority**: These routes are used less than `rune mcp` and `rune serve`, but a route that
still runs earlier work before failing would bring the problem back.

**Independent Test**: From an `agent` task's endpoint, call `release`. The result is an error naming
`clean`, with no flag, and nothing ran. Through the library, serve a test engine whose `release`
descriptor names `clean` as reachable, and check the same.

**Acceptance Scenarios**:

1. **Given** the endpoint an `agent` task opens, **When** its agent calls `release`, **Then** the
   result is an error whose text is `task "release" can run destructive task "clean"; it is not
   allowed in this session`, and nothing ran.
2. **Given** an embedded server with a flag name configured, **When** a call reaches an ungranted
   destructive task the engine reported, **Then** the refusal names that task and the embedder's flag.
3. **Given** an embedder's engine that reports nothing about reachable tasks, **When** a call runs,
   **Then** it behaves exactly as it does today.

---

### Edge Cases

- **Called task outside the allow-list.** An embedder's allow-list refusal still comes first. A
  dependency never has to be on the allow-list.
- **Bad arguments too.** The refusal comes before argument checks, where today's direct refusal
  already sits. A call that is both refused and badly formed gets the refusal.
- **Task unavailable on this host.** A dependency the run would skip because of its OS attribute does
  not count, and neither does anything reached only through it. `dispatch: wipe-win`, with
  `[windows, confirm(...)]` on `wipe-win`, is refused on Windows and runs elsewhere.
- **A failure hook's own failure hooks.** For `deploy: || rollback` and `rollback: || wipe`, `wipe`
  does not count when `rollback` is reached only as a failure hook, since a failing post-mortem fires
  no further hooks. It counts if another edge also runs `rollback` normally.
- **A task reached twice.** A destructive task reached by several paths is named once.
- **Many destructive tasks.** The refusal names all of them. The names come from the Runefile, not
  from the agent, so the text is bounded by what the author wrote.
- **Order of names.** The called task comes first when it is one of them. The rest follow in the order
  a run would first reach them, so the text is the same on every call.
- **A destructive task reached at run time anyway.** If a run reaches an ungranted destructive task
  the up-front check did not see (an embedder that reports nothing), Rune declines it as it does
  today.
- **Concurrent calls.** Each call is checked on its own. Grants are fixed for the server's lifetime,
  so two calls to the same task get the same answer.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Before a tool call runs any task, Rune MUST work out every task the call can run,
  following edges through dependencies, through tasks that themselves run others, and through private
  and imported tasks.
- **FR-002**: The tasks counted MUST be the ones spec 033 counts for the destructive hint: the called
  task's dependencies, `&&` hooks, and `||` hooks, and theirs in turn, except that a task run as a
  `||` hook does not add its own `||` hooks, because failure hooks do not chain. A tool whose
  `destructiveHint` comes from a task it can run is therefore refused unless every destructive task
  it can run is granted.
- **FR-003**: A task the run would skip on this host because of its OS attribute MUST NOT count, and
  neither MUST anything reached only through it.
- **FR-004**: When the counted tasks include a destructive task the operator has not granted, Rune
  MUST refuse the call, and no task body or hook of that call may run.
- **FR-005**: The refusal MUST name every such task once, by the task name the flag accepts
  (`tools::clean` for an imported task), the called task first when it is one of them, and the rest in
  the order a run would first reach them.
- **FR-006**: When the server has an opt-in flag, the refusal MUST name the flag and tell the operator
  to add the listed names, written as one comma-separated value they can paste, and restart. Adding
  exactly those names MUST be enough for the same call to pass this check.
- **FR-007**: When the called task is destructive, ungranted, and can run no other ungranted
  destructive task, the refusal text MUST stay byte-identical to today's.
- **FR-008**: A server with no opt-in flag, such as the endpoint an `agent` task opens, MUST refuse
  the same calls. Its text names the tasks, says they are not allowed in this session, and names no
  flag.
- **FR-009**: The refusal MUST have the same form as today's direct refusal: an error result whose
  text is only the refusal, with no exit code line and no task output.
- **FR-010**: The refusal MUST be checked at the point where today's direct refusal is, after an
  embedder's allow-list check and before argument checks.
- **FR-011**: A grant MUST still cover only its own task. Granting the called task never permits a
  destructive task it can run.
- **FR-012**: A call whose counted tasks include no ungranted destructive task MUST behave exactly as
  today: the same tasks run, and the result is the same.
- **FR-013**: If a run reaches an ungranted destructive task anyway, Rune MUST still decline it when
  it gets there, as it does today.
- **FR-014**: The embeddable server library MUST apply the same refusal, using what the engine's task
  descriptors say about the destructive tasks each task can run. An engine that says nothing MUST get
  today's behavior.
- **FR-015**: Tool listing, tool hints, and CLI runs MUST NOT change.
- **FR-016**: The documentation of the MCP security model MUST describe the up-front refusal and its
  message.
- **FR-017**: An automated test in this repository MUST cover the issue's acceptance criteria. It
  replaces `TestMCPE2E_Pending_DependencyRefusalExplained`, which the issue names but this repository
  does not have.

### Key Entities

- **Call closure**: Every task one tool call can run, worked out from declared edges (FR-001 to
  FR-003). The called task is part of it.
- **Ungranted destructive set**: The tasks in the call closure that have `[confirm]` and are not
  named in `--allow-destructive`, in the order FR-005 sets.
- **Refusal**: The error result that names the ungranted destructive set and, where the server has
  one, the flag.
- **Task descriptor**: The library's description of one task, which an embedder's engine supplies.
  It carries what the server needs to know about the destructive tasks that task can run.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every refused call in the Overview and the acceptance scenarios, no marker file
  exists after the call.
- **SC-002**: Every refusal names each ungranted destructive task the call can run, and on `rune mcp`
  and `rune serve` it also names `--allow-destructive`.
- **SC-003**: For every refused call in the scenarios, one restart with the named tasks added makes
  the same call run.
- **SC-004**: Calls that reach no ungranted destructive task give the same result text and leave the
  same marker files as on `main` at `840ebef`.
- **SC-005**: The direct refusal for a destructive task that can run no other destructive task is
  byte-identical to the text on `main` at `840ebef`.

## Assumptions

- The text says "add ... and restart", not the issue's "restart the server with
  --allow-destructive clean". Restarting with only the named tasks would drop the operator's other
  grants. Spec 031 chose the same wording for the direct refusal for that reason.
- The texts in the acceptance scenarios show the intended shape. The plan's contract fixes the exact
  wording for one name, several names, a destructive called task, and a server with no flag.
- Naming a private task in a refusal is acceptable. A task name is not a secret, the flag already
  accepts private names (spec 031), and the operator cannot permit the call without it.
- "Destructive" means the task has `[confirm]`, as in specs 031 and 033.
- Hints stay advisory (spec 033). This feature makes refusal look at what a call can run, but it does
  not make hints decide anything.
- Test `TestToolHintsMCP_CallsUnchanged` (spec 033) checks that `release` with no grant is an error
  without the direct refusal text, and deliberately does not pin the bare `[exit 1]`. Its expectation
  for `release` changes to the new refusal, because this feature intends that change.
- Rune is before 1.0. If the library's task descriptor needs a new field, adding it is not a breaking
  change for an embedder that sets fields by name, and its absence keeps today's behavior.
