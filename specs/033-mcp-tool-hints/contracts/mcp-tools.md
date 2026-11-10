# Contract: Tool Hints

**Feature**: 033-mcp-tool-hints | Covers FR-002 to FR-006, FR-010, FR-011

What a client sees in `annotations` on `tools/list`. It applies the same way on stdio, on HTTP, and
on the endpoint an `agent` task opens for its own agent. The rest of each tool does not change.

## The hints

| Hint | Value | True when |
|---|---|---|
| `readOnlyHint` | `true`, or left out | The task has `[readonly]` |
| `destructiveHint` | `true` or `false`, always present | The task or anything in its [reach](../data-model.md#reach) has `[confirm]` |
| `openWorldHint` | `true` or `false`, always present | The task or anything in its reach has `[network]` |

## Examples

| Task | `annotations` |
|---|---|
| `deploy` (no attributes) | `{"destructiveHint":false,"openWorldHint":false}` |
| `[readonly] status` | `{"destructiveHint":false,"openWorldHint":false,"readOnlyHint":true}` |
| `[readonly, network] health` | `{"destructiveHint":false,"openWorldHint":true,"readOnlyHint":true}` |
| `[confirm] clean` | `{"destructiveHint":true,"openWorldHint":false}` |
| `[network] fetch` | `{"destructiveHint":false,"openWorldHint":true}` |
| `release: clean` | `{"destructiveHint":true,"openWorldHint":false}` |
| `mirror: fetch` | `{"destructiveHint":false,"openWorldHint":true}` |
| `post: && fetch` | `{"destructiveHint":false,"openWorldHint":true}` |
| `bad: \|\| clean` | `{"destructiveHint":true,"openWorldHint":false}` |
| `dispatch: wipe-win`, where `wipe-win` has `[windows, confirm]`, on Linux or macOS | `{"destructiveHint":false,"openWorldHint":false}` |
| The same `dispatch`, on Windows | `{"destructiveHint":true,"openWorldHint":false}` |

## What does not change

- Which tasks are listed.
- Every `tools/call` result: refusals, declined confirmations, output, and exit status.
- Hints do not vary with `--allow-destructive` or an allowlist.
