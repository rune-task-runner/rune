# Feature Specification: Truthful MCP Tool Hints

**Feature Branch**: `033-mcp-tool-hints`

**Created**: 2026-11-06

**Status**: Draft

**Input**: GitHub issue [#37](https://github.com/rune-task-runner/rune/issues/37)
(backlog MCP-3): "Tool hints claim read-only and ignore dependencies"

## Overview

Rune tells agent clients what each tool does through three hints. Those hints are wrong in
two ways today.

**Every task without `[confirm]` claims to be read-only**, including one that deploys or
deletes. A client that skips approval for read-only tools can therefore run `deploy` without
asking anyone.

**Hints look only at the task itself.** `release: clean` claims to be non-destructive even
when `clean` has `[confirm]`, and a task that depends on a `[network]` task claims to stay
off the network.

```rune
# Deploys the service.
deploy:
    ./deploy.sh

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    rm -rf dist

# Cuts a release.
release: clean
    ./release.sh

# Fetches the data set.
[network]
fetch:
    ./fetch.sh

# Mirrors the data set.
mirror: fetch
    ./mirror.sh
```

This table shows the hints from a binary built on `main` at `edd5208`, and what this feature
changes them to. `absent` means the hint is left out, which clients read as not read-only.

| Tool | Today: read-only, destructive, open-world | After: read-only, destructive, open-world |
|---|---|---|
| `deploy` | `true`, `false`, `false` | absent, `false`, `false` |
| `clean` | absent, `true`, `false` | absent, `true`, `false` |
| `release` | `true`, `false`, `false` | absent, `true`, `false` |
| `fetch` | `true`, `false`, `true` | absent, `false`, `true` |
| `mirror` | `true`, `false`, `false` | absent, `false`, `true` |

An author who knows a task is read-only says so with a new `[readonly]` attribute. A task
cannot claim that and also use `[confirm]`, or run a task that makes no such claim. Rune
rejects either one before anything runs.

Hints stay advisory. They inform a client's decisions and never decide whether Rune runs a
call.

### Out of scope

- Authorization. Which calls are refused or run does not change (spec 031). The bare
  `[exit 1]` a call returns when a destructive dependency is declined is
  [#38](https://github.com/rune-task-runner/rune/issues/38) (MCP-4).
- Enforcing `[readonly]` or `[network]` at run time:
  [#51](https://github.com/rune-task-runner/rune/issues/51) (MCP-17).
- Working out hints from what a task body contains. Hints come only from what authors
  declare (Principle VII).
- The other tool annotations, such as the idempotent hint and the title.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Only a declared task claims to be read-only (Priority: P1)

An operator connects an agent client that skips approval for read-only tools. Today it would
run `deploy` without asking, because Rune calls every task without `[confirm]` read-only.
After this change, only tasks whose authors declared them read-only make that claim, and the
client asks before running anything else.

**Why this priority**: This false claim is how a client ends up with more access than the
operator intended. It is the most serious defect in the issue.

**Independent Test**: List the tools of a Runefile with marked and unmarked tasks, and check
that only the marked ones claim to be read-only.

**Acceptance Scenarios**:

1. **Given** a task `deploy` with no hint attributes, **When** a client lists tools, **Then**
   `deploy` does not advertise `readOnlyHint: true`.
2. **Given** a task `status` with `[readonly]`, **When** a client lists tools, **Then**
   `status` advertises `readOnlyHint: true`, `destructiveHint: false`, and
   `openWorldHint: false`.
3. **Given** a task `clean` with `[confirm]`, **When** a client lists tools, **Then** `clean`
   does not advertise `readOnlyHint: true` and advertises `destructiveHint: true`.
4. **Given** a task `status` with `[readonly]`, **When** it runs from the command line or
   through a tool call, **Then** its output and exit status are the same as without the
   attribute.

---

### User Story 2 - Hints cover everything a call can run (Priority: P1)

A client deciding whether to approve `release` needs to know that calling it can reach a
destructive task or the network. After this change, a task inherits the destructive and
network declarations of everything it can run.

**Why this priority**: Issue #37 reports this as the second half of the defect, and a client
approving `release` today is told it is safe when it is not.

**Independent Test**: List the tools of a Runefile where `[confirm]` and `[network]` sit only
on dependencies and hooks, and check the hints of the tasks that reach them.

**Acceptance Scenarios**:

1. **Given** `release: clean` and `clean` has `[confirm]`, **When** a client lists tools,
   **Then** `release` advertises `destructiveHint: true`.
2. **Given** `mirror: fetch` and `fetch` has `[network]`, **When** a client lists tools,
   **Then** `mirror` advertises `openWorldHint: true`.
3. **Given** `post: && fetch` and `bad: || clean`, **When** a client lists tools, **Then**
   `post` advertises `openWorldHint: true` and `bad` advertises `destructiveHint: true`.
4. **Given** `a: b`, `b: c`, and `c` has `[confirm]`, **When** a client lists tools, **Then**
   `a` and `b` both advertise `destructiveHint: true`.
5. **Given** `[confirm]` on a private task that `release` depends on, **When** a client lists
   tools, **Then** `release` advertises `destructiveHint: true` and the private task is not
   listed.
6. **Given** `release: tools::clean` and `clean` has `[confirm]` in the imported `tools`
   module, **When** a client lists tools, **Then** `release` advertises
   `destructiveHint: true`.
7. **Given** `dispatch: wipe-win` and `wipe-win` has `[windows, confirm("Wipe?")]`, **When**
   a client lists tools on Linux, **Then** `dispatch` advertises `destructiveHint: false`,
   because that dependency is skipped there. **When** a client lists tools on Windows,
   **Then** it advertises `destructiveHint: true`.
8. **Given** a task whose dependencies and hooks carry neither attribute, **When** a client
   lists tools, **Then** it advertises `destructiveHint: false` and `openWorldHint: false`.
9. **Given** `release: clean`, **When** an agent calls `release` with no grant for `clean`,
   **Then** the result is the same as before this change. **When** the operator starts the
   server with `--allow-destructive clean`, **Then** the call runs `clean`, and `release`
   still advertises `destructiveHint: true`.

---

### User Story 3 - A false `[readonly]` claim stops at analysis (Priority: P2)

A Runefile author adds `[readonly]` to a task that also asks for confirmation, or that runs
something not marked read-only. Rune reports the contradiction when it analyzes the file, so
a false claim never reaches a client.

**Why this priority**: The hints are only as good as the declarations behind them, but
stories 1 and 2 already remove the claims Rune makes on its own.

**Independent Test**: Run `rune analyze` on Runefiles with each contradiction, and check the
code, the text, the location, and that nothing ran.

**Acceptance Scenarios**:

1. **Given** a task with both `[readonly]` and `[confirm]`, **When** the Runefile is analyzed,
   **Then** the result is error `RUNE2018`, at the `[readonly]` attribute, with the text
   `[readonly] task "wipe" cannot use [confirm]: remove one of them`.
2. **Given** `[readonly] report: build` and `build` has no `[readonly]`, **When** the Runefile
   is analyzed, **Then** the result is error `RUNE2018`, at the reference to `build`, with the
   text `[readonly] task "report" runs "build", which is not [readonly]: add [readonly] to
   "build" or remove it from "report"`.
3. **Given** `[readonly] report: wipe` and `wipe` has `[confirm]`, **When** the Runefile is
   analyzed, **Then** the result is error `RUNE2018`, at the reference to `wipe`, with the text
   `[readonly] task "report" runs "wipe", which uses [confirm]: remove [readonly] from
   "report"`.
4. **Given** a `[readonly]` task with a `&&` or `||` hook that has no `[readonly]`, **When**
   the Runefile is analyzed, **Then** the result is the same error, at the reference to the
   hook.
5. **Given** `[readonly] a: b`, `[readonly] b: c`, and `c` has no `[readonly]`, **When** the
   Runefile is analyzed, **Then** there is one error, for `b` running `c`.
6. **Given** `[readonly] report: read-state` and `read-state` also has `[readonly]`, **When**
   the Runefile is analyzed, **Then** it passes.
7. **Given** a task with `[readonly]` and `[network]`, **When** the Runefile is analyzed,
   **Then** it passes, and the tool advertises `readOnlyHint: true` and `openWorldHint: true`.
8. **Given** a Runefile with any `RUNE2018` error, **When** the user runs any task or starts
   `rune mcp`, **Then** the command exits with status 3 and reports the error, and nothing
   runs.

### Edge Cases

- **The same dependency by two paths**: counted like one path. The hint is the same.
- **A skipped dependency's own dependencies**: On a host where a dependency is skipped,
  anything reached only through it does not count. Anything also reached another way does.
- **A `[readonly]` task that reaches a task for another OS**: Still an error on every host, so
  a Runefile's validity does not depend on where it is checked.
- **`[readonly]` on a private task**: Allowed. It lists no tool, but a public `[readonly]` task
  can then depend on it.
- **`[readonly]` on a `[context]` task**: Allowed. The hook is never a tool, so the attribute
  has no effect on hints. Its closure follows the same static rule as any other `[readonly]`
  task.
- **`[readonly]` with arguments**: `[readonly("x")]` is the syntax error `[network("x")]` gives
  today.
- **`[readonly]` twice on one task**: Accepted, with no added effect, as `[network]` twice is
  today.
- **A body that writes under `[readonly]`**: Rune does not read bodies, so the task still
  claims to be read-only. The docs say this is the author's claim.
- **A body that runs `rune other`**: Not followed. Only dependencies and hooks count.
- **An `agent` task**: The tasks its agent might call are chosen at run time, so they do not
  count toward its hints. Its own attributes and its dependencies and hooks do.
- **A dependency cycle**: Rejected by analysis before tools are listed, as it is today.
- **A grant or an allowlist**: Neither changes a hint. A listed tool advertises the same hints
  whichever tasks the operator permits.
- **A client that relied on the old claim**: It now asks for approval, or refuses, on tools
  it used to run freely. This is the intended result, and the release notes say so.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Runefiles MUST accept a `[readonly]` attribute that takes no arguments and
  combines with other attributes the way `[network]` does.
- **FR-002**: A tool MUST advertise `readOnlyHint: true` only when its task has `[readonly]`.
  Every other tool MUST leave the hint absent or false.
- **FR-003**: A tool MUST advertise `destructiveHint: true` when its task, or any task it
  reaches, has `[confirm]`, and `destructiveHint: false` otherwise.
- **FR-004**: A tool MUST advertise `openWorldHint: true` when its task, or any task it
  reaches, has `[network]`, and `openWorldHint: false` otherwise.
- **FR-005**: A task reaches its dependencies, its `&&` post-hooks, its `||` failure hooks, and
  everything those reach in turn, including private tasks and tasks in imported modules. A
  task run only as a failure hook does not reach its own `||` hooks, because a run does not
  chain failure hooks.
- **FR-006**: A task unavailable on the host OS MUST NOT count toward another task's hints,
  and neither MUST anything reached only through it, because a run skips it there.
- **FR-007**: A task with both `[readonly]` and `[confirm]` MUST be a static error with the
  new code `RUNE2018`, reported at the `[readonly]` attribute, with the text
  `[readonly] task "<task>" cannot use [confirm]: remove one of them`.
- **FR-008**: A `[readonly]` task whose dependency or hook lacks `[readonly]` MUST be a static
  error with code `RUNE2018`, reported at that reference, with the text
  `[readonly] task "<task>" runs "<target>", which is not [readonly]: add [readonly] to
  "<target>" or remove it from "<task>"`. When the target has `[confirm]`, the text MUST
  instead be `[readonly] task "<task>" runs "<target>", which uses [confirm]: remove
  [readonly] from "<task>"`.
- **FR-009**: `RUNE2018` MUST behave like other static errors: nothing runs, the exit status is
  3, it stops the commands other analyzer errors stop (such as running a task, `--list`,
  `--dry-run`, and `rune mcp`), and `rune analyze` and editors report it. `--fmt`, which does
  not analyze, MUST still format the file. The result MUST NOT depend on the host OS.
- **FR-010**: Hints MUST NOT change what a call does. Every refusal, declined confirmation,
  body, hook, output, and exit status MUST be the same as before this change. A task with only
  a destructive dependency MUST NOT be refused the way a `[confirm]` task is, and `[readonly]`
  MUST NOT permit anything a call could not already do. Grants and allowlists MUST NOT change
  a hint.
- **FR-011**: FR-002 to FR-006 MUST hold the same way on stdio, on HTTP, and on the endpoint an
  `agent` task opens for its own agent.
- **FR-012**: The embeddable MCP server library MUST advertise `readOnlyHint: true` only for a
  task whose descriptor declares it read-only. It MUST let a descriptor state what a task
  reaches separately from the task's own destructive flag, which authorization reads. A
  descriptor that sets neither MUST get no read-only claim and hints from its own flags alone.
- **FR-013**: Outside hints and `RUNE2018`, `[readonly]` MUST change nothing on the command
  line: not how the task runs, and not how `--list` shows it. `--dump` shows it with the
  task's other attributes.
- **FR-014**: Hints and `RUNE2018` MUST come only from attributes, dependencies, and hooks,
  never from the text of a body or the choice of executor.
- **FR-015**: `[readonly]` MUST ship with the rest of the language surface: the grammar
  document, the attribute reference, editor completion, hover, and highlighting, the
  formatter, and the compatibility corpus.
- **FR-016**: The MCP guide MUST say how each hint is derived, that hints are advisory, that
  `[readonly]` is the author's claim and Rune does not check the body, and that a task without
  `[readonly]` makes no read-only claim. The diagnostics reference MUST document `RUNE2018`.
  No user-facing document may keep saying that tasks or agent access are read-only by default.
  Today that claim is in the MCP guide, the MCP walkthrough, the user guide, the agent-driven
  example, `README.md`, `SECURITY.md`, and the website landing page. An example task described
  as read-only MUST use `[readonly]`.
- **FR-017**: The release notes MUST call out that a tool without `[readonly]` no longer claims
  to be read-only, that authors should add the attribute where it is true, and that an
  embedder's task descriptors must now declare it.

### Key Entities

- **Tool hints**: The three claims a listed tool makes about itself: read-only, destructive,
  and open-world. Clients read them. Rune's authorization does not.
- **Reach**: The set of tasks a call to a task can run on this host, per FR-005 and FR-006.
- **Task descriptor**: The library's description of one task, which an embedder's engine
  supplies and from which the library builds the tool.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In any Runefile, 100% of listed tools without `[readonly]` make no read-only
  claim, and 100% of those with it do.
- **SC-002**: For `[confirm]` and `[network]` placed three levels deep, on one chain that uses
  a dependency, a post-hook, and a failure hook, 100% of the tasks that reach them advertise
  the matching hint.
- **SC-003**: Every contradictory `[readonly]` in this spec fails analysis with zero side
  effects, and its message names the task, the target where there is one, and a fix.
- **SC-004**: Every tool call in this spec's scenarios gives the same result, including its
  output, exit status, and any refusal, as before this change.
- **SC-005**: A server lists identical hints for the same Runefile on every transport and
  endpoint.

## Assumptions

- `||` failure hooks count toward reach. The issue names dependencies and post-hooks, but a
  failure hook also runs as part of a call, as `bad: || clean` does, and Rune's existing
  `[context]` closure check walks all three.
- The rule that a `[readonly]` task can run only `[readonly]` tasks (FR-008) is this spec's
  addition. The issue asks to reject only `[readonly]` with `[confirm]` on one task. Without
  the rule, `[readonly] report: build` would either claim to be read-only while `build` writes,
  or silently lose the claim its author wrote. The attribute is new, so no existing Runefile
  can hit this error.
- Tasks with no declaration keep an explicit `destructiveHint: false` and
  `openWorldHint: false`, as they do today. The MCP defaults for those two hints are `true`, and
  `false` strictly claims only additive updates and a closed domain, which Rune cannot know.
  The issue asks to change only the read-only default, Principle VII makes destructiveness
  author-declared, and calling every task destructive would erase the signal `[confirm]` gives.
- Principle VII says "The agent surface is read-only by default". This spec reads that as the
  refusal posture, where destructive tasks are refused until the operator allows them, which
  does not change. Read literally, the sentence conflicts with FR-002, so the plan rewords it
  through a constitution amendment ([research R8](research.md)).
- This is a breaking change for a client that relied on the read-only claim, and for an
  embedder whose tasks really are read-only. Rune is before 1.0, so it ships with a
  release-notes callout (FR-017), as specs 029, 031, and 032 did.
- The pending end-to-end test the issue names (`TestMCPE2E_Pending_HintsCoverDependencies`) is
  not in this repository. Equivalent binary-level integration tests will cover the acceptance
  scenarios.
- `RUNE2018` is the next free code. `RUNE2017` is the highest in use.
