# Data Model: Truthful MCP Tool Hints

**Feature**: 033-mcp-tool-hints | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

No storage and no file formats. The model is one new attribute, two new descriptor fields, and the
rules that connect them to a tool's hints.

## The `[readonly]` attribute

A flag on a task, parsed like `[network]`. See the
[attribute contract](contracts/readonly-attribute.md).

| Combination | Result |
|---|---|
| Alone, or with any attribute but `[confirm]` | Allowed |
| With `[confirm]` on the same task | `RUNE2018` at the `[readonly]` attribute |
| A dependency, `&&` hook, or `||` hook without `[readonly]` | `RUNE2018` at that reference |
| An unknown target | `RUNE2001` only, as today |

## Reach

The tasks a call to a task can run on this host. It is computed for each listed task from the
composed Runefile ([research R3](research.md)).

| Step | Rule |
|---|---|
| Start | The task's dependencies, `&&` hooks, and `||` hooks |
| Follow | The same three edge kinds of each task reached, once each, except the `||` hooks of a task reached only as a failure hook: the scheduler does not chain failure hooks |
| Stop at | A task unavailable on the host OS. It and anything reached only through it do not count |
| Includes | Private tasks and tasks in imported modules |
| Excludes | What a body runs, and what an `agent` task's agent might call |

## Task descriptor (`mcpserver.TaskInfo`)

| Field | Set by Rune's adapter from | Read by | Status |
|---|---|---|---|
| `Destructive` | The task's own `[confirm]` | `toolFor`, `isDestructive` (refuses the call) | Unchanged |
| `ReachesDestructive` | `[confirm]` on anything in its reach | `toolFor` only | New |
| `Network` | `[network]` on the task or anything in its reach | `toolFor` only | Wider |
| `ReadOnly` | The task's own `[readonly]` | `toolFor` only | New |

## From descriptor to hints

| Hint | Value |
|---|---|
| `readOnlyHint` | `ReadOnly`, left out of the reply when false |
| `destructiveHint` | `Destructive` or `ReachesDestructive`, always present |
| `openWorldHint` | `Network`, always present |

A grant or an allowlist is not an input to any row.

## What a call does (unchanged)

| Task | Call result |
|---|---|
| Has `[confirm]`, no grant | The destructive refusal (spec 031). Nothing runs |
| Has `[confirm]`, granted | Runs |
| Reaches a `[confirm]` task, no grant for that task | Not refused up front. The run stops when it reaches that task, as it does today |
| Has `[readonly]` | Runs as if the attribute were absent |
