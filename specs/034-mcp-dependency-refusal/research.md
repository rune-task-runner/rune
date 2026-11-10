# Research: Refuse a Destructive Dependency Before the Call Runs

**Feature**: 034-mcp-dependency-refusal | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

The spec has no open clarifications. Each decision below cites the code it rests on, as read on
`main` at `840ebef`.

## R1. Where the check runs

**Decision**: In the library's `refusal`, which the tool handler calls before it decodes arguments
(`mcpserver/handler.go:35`). It checks the called task and the destructive tasks its descriptor says
it can reach against `Server.granted`.

**Rationale**: The grants (`Server.granted`) and the flag name (`Options.AllowDestructiveFlag`)
already live there, so the message can name the flag without the CLI knowing about it. The check
then sits where the direct refusal sits, before argument checks (FR-010), and embedders get it
through the descriptor (FR-014). The `agent` task endpoint builds the same server with no grants and
no flag (`internal/cli/agentexec.go:48`), so it refuses the same calls with the no-flag text (FR-008)
without extra code.

**Alternatives considered**:

- Checking in `mcpAdapter.CallLists`. That runs after argument decoding and the unknown-argument
  check, does not know the flag name, and gives embedders nothing.
- A new `Engine` method that returns a task's closure. It widens the interface every embedder
  implements, for data the descriptor can carry.

## R2. The descriptor field

**Decision**: Replace `TaskInfo.ReachesDestructive bool` with `TaskInfo.DestructiveReach []string`:
the names of the destructive tasks a run of this task can reach, each once, excluding the task
itself, in the order of R3. The destructive hint becomes
`t.Destructive || len(t.DestructiveReach) > 0`.

**Rationale**: The refusal needs names, and the hint needs only whether there are any. One field
serves both, so they cannot disagree. `ReachesDestructive` arrived with spec 033 after `v0.6.0` and
is in no release (`git show v0.6.0:mcpserver/server.go` has no such field), so replacing it breaks no
released API. Against `v0.6.0`, the change is one new field, and an embedder that leaves it nil keeps
today's behavior (FR-014).

**Alternatives considered**:

- Keeping the bool and adding a list. Two fields could disagree: a bool set with an empty list would
  advertise the hint but never refuse.
- Keeping the name `ReachesDestructive` with the new type. A list named like a predicate reads as a
  bool at every use.

## R3. Reach order

**Decision**: Depth-first, a task before what it runs. Each task's dependencies come in declared
order, then its `&&` hooks, then its `||` hooks. A task is named at its first occurrence. The reach
set itself is spec 033's (FR-002): unavailable tasks stop the walk, private and imported tasks are
included, and a task reached only as a failure hook adds no `||` hooks of its own until an ordinary
edge reaches it too.

**Rationale**: This is the order in which the scheduler first takes up each task: a dependency before
the dependent's body, `&&` hooks after it, `||` hooks only on failure. So the message reads in the
order the Runefile does (FR-005), and declared order keeps it stable under `[parallel]`. Spec 033's
breadth-first walk gives a different order: for `ordered: stage clean` and `stage: wipe`, it names
`clean` before `wipe`, though a run reaches `wipe` first. `mcpAdapter.reach` becomes a recursive
depth-first walk with the same edge rules. Its 14 boolean cases in `TestAdapterReach` keep their
meaning and gain the names in order.

**Alternatives considered**:

- Keeping the breadth-first walk. Stable, but not the order a run takes.
- Sorting names alphabetically. Stable, but unrelated to the Runefile, so harder to check.

## R4. The message

**Decision**: One rule builds every destructive refusal. The full table is in
[contracts/mcp-tools.md](contracts/mcp-tools.md).

- Named tasks: the called task when it is destructive and ungranted, then each ungranted task from
  `DestructiveReach`, in order.
- Subject: `task "X" is destructive`, `task "X" can run destructive task "A"` (or `tasks "A", "B"`),
  or both joined by `and`.
- With a flag: the subject, then `; to permit it, add A to FLAG and restart the server`. Use `them`
  for two or more names, and join the names with commas so they paste as one value.
- With no flag: the subject, then `; it is not allowed in this session` (or `they are`). The one
  exception is the own-only case, which keeps today's `task "X" is destructive and is not allowed in
  this session`.

**Rationale**: The own-only case with a flag gives today's text byte for byte, so FR-007 holds
without a special case there. "Add" keeps spec 031's reason: restarting with only the named tasks
would drop the operator's other grants (spec Assumptions). Naming every task makes one restart
enough (FR-006, SC-003). Names come from the Runefile, so no cap is needed. Spec 032 capped
unknown-argument text because those names come from the agent.

**Alternatives considered**:

- The issue's `restart the server with --allow-destructive clean`, which drops existing grants.
- Naming only the first ungranted task, which can take several restarts.
- Repeating the full grant list in the message, which tells the agent the operator's whole
  configuration for no gain.
- `, which is not allowed in this session` for the no-flag form. It reads well for one name but has
  no clear form when the called task is destructive too.

## R5. The handler passes its descriptor

**Decision**: `refusal` takes the handler's `TaskInfo` instead of a task name, and `isDestructive`
is removed.

**Rationale**: `New` registers each tool once with the descriptor from one `Tasks()` call
(`mcpserver/server.go`, `New`). The handler already holds it, and the new check needs its
`DestructiveReach`. `isDestructive` rebuilt the whole `Tasks()` list on every call, which for the CLI
adapter means a reach walk for every task. Spec 033's research recorded that under "Observed, not
changed". Reading both fields from one descriptor keeps a single source.

**Alternatives considered**: Keeping `isDestructive` and adding a second lookup for the list. That is
two full rebuilds per call, from data the handler already has.

## R6. The run-time decline stays

**Decision**: `confirmFailure` and `confirm` in `internal/cli/confirm.go` do not change. The engine
still declines an ungranted destructive task when a run reaches it.

**Rationale**: FR-013. A descriptor can be incomplete, for example from an embedder that leaves
`DestructiveReach` nil, and the engine's grant check is keyed by the task's own name (spec 031
FR-005). For the CLI adapter, the up-front check covers every task a run can reach, so the decline
is only a backstop.

## R7. Test layers

**Decision**:

| Layer | File | Covers |
|---|---|---|
| Library | `mcpserver/authz_test.go` | Every message form, grant filtering of own and reached tasks, allow-list first, a nil list refusing nothing new |
| Library | `mcpserver/handler_test.go` | A dependency refusal comes before argument checks, and the engine is not called |
| Library | `mcpserver/server_test.go` | Hint tests move from `ReachesDestructive` to `DestructiveReach` |
| Transport | `mcpserver/transport_test.go` | One dependency refusal over Streamable HTTP |
| Adapter | `internal/cli/mcp_test.go` | Reach order, including depth-first order, both hook kinds, and the failure-hook rule; the agent endpoint's no-flag text for `release` |
| Binary | `test/integration/dependency_refusal_mcp_test.go` (new) | Quickstart scenarios over stdio, with marker files proving nothing ran |
| Binary | `test/integration/destructive_mcp_test.go`, `tool_hints_mcp_test.go` | Expectations that pinned or tolerated the bare `[exit 1]` now pin the refusal |
| Docs | `test/docs` | Changed pages still validate |

**Rationale**: Principle VI. The issue names `TestMCPE2E_Pending_DependencyRefusalExplained`, which
this repository does not have. The new integration file covers its acceptance criteria instead
(FR-017), as spec 033 did for its pending test.

Changed expectations are intended behavior changes, not weakened tests:

- `TestDestructiveMCP_DependencyFollowsGrant` expected `[exit 1]` for an ungranted dependency. It now
  expects the refusal text.
- `TestToolHintsMCP_CallsUnchanged` left the `release` text unpinned "because issue #38 will change
  it". It now pins the text.
- `TestRefusalIgnoresReachesDestructive` pinned that refusal ignores reach. FR-004 reverses that, so
  it becomes a test that the refusal covers reach.

## R8. Documentation

**Decision**:

| Page | Change |
|---|---|
| `docs/mcp.md`, Tool hints | Rewrite "A tool can advertise `destructiveHint: true` without being refused. Only a `[confirm]` task itself is refused": such a tool is refused unless every `[confirm]` task it can run is granted, and `--allow-destructive` names those tasks |
| `docs/mcp.md`, Security model | The destructive bullet: a call that can run an ungranted destructive task is refused before anything runs, and the refusal names what to add |
| `docs/runefile.md` | Rewrite "but only the `[confirm]` task itself is refused" to match |
| `SECURITY.md` | The destructive bullet says a task that can reach a `[confirm]` task is refused too |

The bullet "Hints are advisory. They never change what Rune runs or refuses." stays true: refusal
reads the same declarations, not the hints. The startup warning `--allow-destructive release has no
effect: task "release" is not destructive` stays, because granting `release` still permits nothing.

**Rationale**: FR-016. `README.md`, `docs/use-cases/mcp-agents.md`, `docs/user-guide/README.md`, and
`website/src/landing/index.mdx` say only that destructive tasks are refused until allowed, which stays
true.

## R9. Release notes

**Decision**: One commit, `fix(mcp)!:`, with the behavior change in its subject, as specs 031 to 033
did.

**Rationale**: Agents see a different result for calls that reach an ungranted destructive task, and
a task with a destructive `||` hook is now refused even when it would succeed. That is breaking for
MCP users. For embedders, the only change against `v0.6.0` is one new field.
