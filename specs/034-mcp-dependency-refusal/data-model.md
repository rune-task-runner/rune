# Data Model: Refuse a Destructive Dependency Before the Call Runs

**Feature**: 034-mcp-dependency-refusal | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

No storage and no file formats. The model is one descriptor field, the order it lists names in, and
the rule that turns it into a refusal.

## Reach

The set of tasks is spec 033's, unchanged. See the
[reach table in spec 033's data model](../033-mcp-tool-hints/data-model.md#reach). This feature
adds an order ([research R3](research.md#r3-reach-order)).

| Step | Rule |
|---|---|
| Walk | Depth-first from the called task. Each task's dependencies in declared order, then its `&&` hooks, then its `\|\|` hooks |
| Name | A task with `[confirm]` is named at its first occurrence, and never again |
| Exclude | The called task itself. Its own `[confirm]` is `Destructive` |
| Grants | Ignored. The descriptor lists reach, and the server filters by grant |

Example: `ordered: stage clean` and `stage: wipe`, both `[confirm]`, give `["wipe", "clean"]`.

## `TaskInfo.DestructiveReach`

| Field | Type | Meaning |
|---|---|---|
| `DestructiveReach` | `[]string` | The destructive tasks a run of this task can reach, in reach order, each once, excluding the task itself. Nil when there are none, or when the engine does not report reach |

It replaces `ReachesDestructive bool`, which no release has shipped
([research R2](research.md#r2-the-descriptor-field)). Uses:

| Reader | Use |
|---|---|
| `toolFor` | `destructiveHint` is `Destructive \|\| len(DestructiveReach) > 0`. Hints are the same as spec 033's |
| `refusal` | The source of the reached tasks in the refused set |

## Refused set

Computed per call from the handler's descriptor and `Server.granted`.

| Part | Contents |
|---|---|
| Own | The called task, when `Destructive` is true and the task is not granted |
| Reached | Each name in `DestructiveReach` that is not granted, in order |
| Named | Own first, then reached |

The call is refused when the named list is not empty. Grants never widen: a granted task drops out of
the list, and nothing else does (spec 031 FR-005).

## Refusal order in the handler

| Step | Check | Result when it fails |
|---|---|---|
| 1 | Allow-list (`Options.AllowList`) | `task "X" is not authorized for this session` (unchanged) |
| 2 | Refused set | The destructive refusal ([contract](contracts/mcp-tools.md#refusal-texts)) |
| 3 | Argument decoding, unknown names, values | Unchanged (specs 023, 030, 032) |
| 4 | Engine call | The run's result |

## State

None. Grants are fixed when the server starts, and descriptors when it registers tools, so a call's
refusal depends only on the task called (spec edge case "Concurrent calls").
