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

## Interactive approval (spec 049)

Spec 049 adds a second way for a destructive call to run on `rune mcp` and `rune serve`:
the person at the client approves that one call. It changes the rows above whose result is
a refusal or a bare `[exit 1]` for want of a grant. Spec 034 had already replaced the bare
`[exit 1]` rows with an up-front refusal.

| Client | No grant, call `clean` | No grant, call `release` (`release: clean`) |
|---|---|---|
| States MCP form elicitation | Rune sends one approval request naming `clean` and its `[confirm]` message. Approved: `clean` runs once. Anything else: `isError: true`, `task "clean" was not approved: REASON. Nothing ran.`, and nothing runs | One request names `clean`, before anything runs. Approved: `clean`, then `release`, run once |
| States no elicitation, or the URL mode only | `isError: true`, the refusal text above followed by `. Or the operator can run "rune clean" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.` Nothing runs | The up-front refusal of spec 034 with the same two sentences, naming `rune release` |

- A granted task is never asked about, and a call whose destructive tasks are all granted
  runs with no request, as in the tables above.
- An approval covers the tasks its request named, for that call, once. It is not a grant:
  the next call is asked about again, and no session or later call can use it.
- The agent-executor endpoint never asks. Its texts are unchanged.

The request, every outcome text and the library contract are in
[specs/049-mcp-confirm-elicitation/contracts](../../049-mcp-confirm-elicitation/contracts/mcp-tools.md).

## Transports and sessions

The grant applies to every session on stdio and Streamable HTTP alike, for the
server's lifetime. A call never consumes it. HTTP still requires the bearer
token on every request (FR-013).

## Agent-executor endpoint

The endpoint Rune opens for an `agent` task's own agent refuses every
destructive task, whatever flags started any other process (FR-015). It sets no
`AllowDestructiveFlag`, so the text suggests no restart:
`task "clean" is destructive and is not allowed in this session`.
