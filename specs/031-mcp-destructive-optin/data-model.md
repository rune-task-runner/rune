# Data Model: Per-Task Opt-In for Destructive Tasks over MCP

**Feature**: 031-mcp-destructive-optin | **Date**: 2026-11-04 | **Spec**: [spec.md](spec.md)

No storage, no file formats, no Runefile syntax. The model is one value built at
server start and the decisions that read it.

## Grant

The task names the operator passed with `--allow-destructive`, after parsing and
validation ([research R1, R2](research.md)).

| Field | Type | Rule |
|---|---|---|
| names | ordered list of task names | first-occurrence order, no duplicates; passed as `mcpserver.Options.AllowDestructive`; the startup line lists those with `[confirm]` that can run on this host |
| set | membership set of the same names | used by the tool handler and by the engine's confirmation gate |

**Built from** every `--allow-destructive` value: split on `,`, each item trimmed
of surrounding spaces.

**Validation** (all before any Runefile code runs, FR-010):

| Input | Result |
|---|---|
| item empty after trimming (`""`, `clean,`, `, clean`) | usage error, exit 2 |
| name not a task in the loaded Runefile or its imports (`claen`, `*`, `tools__clean`) | usage error, exit 2, with a suggestion when one is within edit distance 2 |
| name of a task without `[confirm]` | accepted; one warning line; no effect |
| name of a private, OS-restricted, or imported (`ns::task`) task with `[confirm]` | accepted silently |
| same name twice | accepted once |

**Lifecycle**: built once in `cli.ServeMCP`, never changed. No tool, argument, or
Runefile content can modify it. Empty when the flag is absent.

**Holders**:

- `mcpserver.Server` receives `names` through `Options.AllowDestructive` and
  keeps the set (direct-call refusal).
- `mcpAdapter.grants` holds the set; each `engine` built by `CallLists` gets it
  (confirmation gate).
- The agent-executor adapter (`newAgentAdapter`) and the CLI engine never hold
  one.

## Confirmation gate

`engine.confirmFailure(task)` for a task with `[confirm]`, evaluated in order.
The first matching row decides.

| # | Condition | Outcome | Reached on |
|---|---|---|---|
| 1 | `--yes` given | approved | CLI only |
| 2 | `task.Name` in the engine's grant set | approved | MCP server only (new) |
| 3 | stdin is nil | declined, silent failure | MCP (server and agent endpoint) |
| 4 | stdin is not a terminal | declined, message naming `--yes` | CLI |
| 5 | otherwise | prompt; `y`/`yes` approves | CLI at a terminal |

The gate runs for the called task, each dependency, each `&&` post-hook (through
`Execute`), and each `||` failure hook (through `ExecuteFailHook`).

## Direct-call refusal

`Server.refusal(name)` in the tool handler, before arguments are decoded.

| # | Condition | Result |
|---|---|---|
| 1 | `AllowList` non-empty and `name` not in it | error result: `task "NAME" is not authorized for this session` |
| 2 | task is destructive and `name` not in the grant set | error result: `task "NAME" is destructive; to permit it, add NAME to --allow-destructive and restart the server` (no `AllowDestructiveFlag`: `task "NAME" is destructive and is not allowed in this session`) |
| 3 | otherwise | call proceeds to the engine |

"Destructive" means the tool's task has `[confirm]`, as `TaskInfo.Destructive`
reports today.

## Outcomes by path

What happens to a destructive task `D`, by how the run reaches it and whether
`D` is granted. Non-destructive tasks are unaffected in every row.

| Path to `D` | `D` granted | `D` not granted |
|---|---|---|
| agent calls `D` | body runs (gate row 2) | refused before anything runs (refusal row 2) |
| dependency of the called task | body runs | gate row 3: `D` and the dependent's body do not run; tasks that ran earlier in the run stay done; call reports a failure |
| `&&` post-hook | body runs | gate row 3: hook does not run; call reports a failure |
| `||` failure hook | body runs | gate row 3: hook does not run; one-line warning, exit status unchanged (spec 022) |
| reached inside an `agent` task's own endpoint | refused (that endpoint has no grant) | refused |
