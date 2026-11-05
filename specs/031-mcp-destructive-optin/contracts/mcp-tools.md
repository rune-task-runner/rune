# Contract: MCP tool behavior with a grant

**Feature**: 031-mcp-destructive-optin | Covers FR-002 to FR-006, FR-013 to FR-015

What an MCP client observes. The decision tables behind it are in
[data-model.md](../data-model.md).

## `tools/list`

Identical with and without a grant, byte for byte: every non-private task
available on this host is listed, destructive ones with `destructiveHint: true`
(FR-014).

## `tools/call` on a destructive task

| Server started with | Result |
|---|---|
| `--allow-destructive clean`, call `clean` | the task's normal result: output, `[exit N]` marker when non-zero, `isError` when non-zero. No prompt text appears anywhere. |
| no grant, call `clean` | `isError: true`, text `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`. Nothing runs. |
| `--allow-destructive clean`, call `reset` | `isError: true`, text `task "reset" is destructive; to permit it, add reset to --allow-destructive and restart the server`. Nothing runs. |
| no grant, call `tools__clean` (tool for `tools::clean`) | `isError: true`, text `task "tools::clean" is destructive; to permit it, add tools::clean to --allow-destructive and restart the server` |

The text names the task, not the tool, so it can be pasted into the flag.

## `tools/call` on a non-destructive task that reaches a destructive one

| Runefile | Grant | Result |
|---|---|---|
| `release: clean` | `clean` | `clean` runs, then `release` |
| `release: clean` | none | `isError: true`, `[exit 1]`, no explanation (unchanged; #38) |
| `build: && wipe` | none | `build` runs; `wipe` does not; `isError: true`, `[exit 1]` |
| `build: \|\| wipe`, `build` fails | none | `build`'s own exit code; `wipe` does not run; a one-line warning in stderr (unchanged, spec 022) |
| `release: _wipe` (`_wipe` private) | `_wipe` | `_wipe` runs as a dependency; it is still not a tool |
| `clean: wipe`, both `[confirm]` | `clean` | `isError: true`, `[exit 1]`; neither body runs |

## Transports and sessions

The grant applies to every session on stdio and Streamable HTTP alike, for the
server's lifetime. A call never consumes it. HTTP still requires the bearer
token on every request (FR-013).

## Agent-executor endpoint

The endpoint Rune opens for an `agent` task's own agent refuses every
destructive task, whatever flags started any other process (FR-015). It sets no
`AllowDestructiveFlag`, so the text suggests no restart:
`task "clean" is destructive and is not allowed in this session`.
