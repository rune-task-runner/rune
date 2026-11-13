# Data Model: Operator Allowlist and Denylist for MCP Tasks

**Feature**: 041-mcp-allow-deny | **Date**: 2026-11-12 | **Spec**: [spec.md](spec.md)

Nothing is stored. The model is three sets of task names fixed at server start.

## Sets

| Set | Source | Lifetime |
|---|---|---|
| Exposable | `Engine.Tasks()`: not private, not the `[context]` hook, runs on this host | server |
| Allowlist | `--allow` values, `Options.AllowList` | server |
| Denylist | `--deny` values, `Options.DenyList` | server |
| Exposed | derived, below | server |
| Granted | `--allow-destructive` values, `Options.AllowDestructive` (spec 031) | server |

```text
exposed = { t in exposable : (allowlist is empty or t in allowlist) and t not in denylist }
```

- The exposed set is a subset of the exposable set. No flag adds to it (FR-005).
- A name in both lists is not exposed (FR-002).
- The granted set is independent. It is not filtered by the exposed set and does not add to it.

## What each set decides

| Question | Decided by |
|---|---|
| Is the task in `tools/list`? | exposed |
| Can an agent call it by name? | exposed |
| Does it run as a dependency or hook of a called task? | the Runefile (always) |
| May a `[confirm]` task run, called or reached? | granted |
| What are an exposed task's hints, schema, description? | the task (unchanged) |

## Flag value validation

Applied to each of `--allow-destructive`, `--allow`, `--deny`, in that order.

| Value | Result |
|---|---|
| Empty item (`""`, `a,`, `,a`) | usage error, exit 2, before the Runefile is read |
| Not a task of the Runefile or its imports | usage error, exit 2, with a suggestion, before any Runefile code runs |
| Repeated name | kept once |
| Private task or `[context]` hook (`--allow`, `--deny` only) | accepted, one warning: no effect |
| Task restricted to another OS | accepted, silent |
| Exposable task | takes effect |

## Server start states

```text
parse flags -> load Runefile -> check names -> validate transport
   -> warn (no-op names) -> [context] hook -> build server (register exposed)
   -> warn if exposed is empty and a filter was given -> startup line -> serve
```

Any error before the `[context]` hook exits with status 2 and runs no Runefile code. After the
server is built the exposed set never changes.

## Exposure counts

`exposed` and `exposable` are counted once while tools are registered. They appear in the startup
line only when `--allow` or `--deny` was given.

| Filters given | Exposed | Startup line part | Warning |
|---|---|---|---|
| no | any | none (unchanged) | none |
| yes | 1 or more | `N of M tasks exposed` | none |
| yes | 0 | `0 of M tasks exposed` | no tasks are exposed |
