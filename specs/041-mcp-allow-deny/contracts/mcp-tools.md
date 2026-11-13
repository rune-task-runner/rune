# Contract: MCP Tool Surface under `--allow` and `--deny`

**Feature**: 041-mcp-allow-deny | Covers FR-003 to FR-008, FR-015 to FR-017

What an MCP client sees. The exposed set is defined in [data-model.md](../data-model.md#sets).

## `tools/list`

- Returns exactly the exposed set, in the order tasks appear without the flags.
- Each tool's `name`, `description`, `inputSchema`, and `annotations` are byte-identical to the
  entry the same server would list without the flags. Hints still account for dependencies and
  hooks that are hidden.
- The list is the same for every session and transport and never changes while the server runs. No
  `notifications/tools/list_changed` is sent because of the filters.

## `tools/call`

| Called name | Result |
|---|---|
| Exposed task | As without the flags: destructive refusals (spec 031, spec 034), argument checks (spec 032, spec 023), then the run with its full dependency and hook graph |
| Hidden task (exposable, filtered out) | The server's unknown-tool error |
| Private task, `[context]` hook, other-OS task | The server's unknown-tool error (unchanged) |
| Name no task has | The server's unknown-tool error (unchanged) |

The last three rows produce the same response apart from the tool name it echoes. Nothing in it
says whether a task of that name exists. A hidden task's body, dependencies, and hooks do not run.

## Refusal texts

No new refusal text. The text `task "<name>" is not authorized for this session` is no longer
produced. The destructive refusals are unchanged:

```text
task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server
task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server
```

The second form may name a hidden task. It stays hidden: the name cannot be called.

## Interaction with `--allow-destructive`

| Server flags | `clean` (`[confirm]`) listed | Call `clean` | Call `release` (`release: clean`) |
|---|---|---|---|
| none | yes | refused, destructive | refused, names `clean` |
| `--allow release` | no | unknown tool | refused, names `clean` |
| `--allow release --allow-destructive clean` | no | unknown tool | `clean` runs, then `release` |
| `--allow clean` | yes | refused, destructive | unknown tool |
| `--allow clean --allow-destructive clean` | yes | runs | unknown tool |
| `--deny clean --allow-destructive clean` | no | unknown tool | `clean` runs, then `release` |

## `agent` task endpoint

Unchanged. It lists and runs the same tasks as before this feature, whatever flags started the
process.
