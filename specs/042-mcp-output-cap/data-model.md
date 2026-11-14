# Data Model: Cap Task Output Before It Reaches the Agent

**Feature**: 042-mcp-output-cap | **Spec**: [spec.md](spec.md) | **Research**: [research.md](research.md)

No stored data. These are the values and the one stateful type.

## Output limit

| Field | Value |
|---|---|
| Unit | bytes, per stream, marker included |
| Default | 65,536 (`DefaultOutputLimit`) |
| Minimum | 1,024 (`MinOutputLimit`) |
| Maximum | 1,073,741,824 (`MaxOutputLimit`) |
| Sources, first wins | `--output-limit`, `set mcp_output_limit`, default |
| Lifetime | fixed when the server (or the agent endpoint) is built |

Size text: `DIGITS` then nothing, `B`, `KiB`, or `MiB`. See
[contracts/cli-and-setting.md](contracts/cli-and-setting.md).

Where it is held:

| Holder | Field | Zero means |
|---|---|---|
| `cli.Options` | `MCPOutputLimit *string` | flag not given (nil) |
| `config.Settings` | `MCPOutputLimit int` | setting absent |
| `mcpAdapter` | `outputLimit int` | use the setting, then the default |
| `mcpserver.Options` | `OutputLimit int` | default |

## OutputBuffer

One per stream per call.

| Field | Meaning |
|---|---|
| `limit` | the output limit, clamped to the range |
| `headMax` | `(limit - 40) / 2` |
| `tailMax` | `limit - 40 - headMax` |
| `buf` | at most `limit` bytes |
| `total` | every byte written, `int64` |
| `start` | index of the oldest byte in the ring, once truncated |

States:

| State | Condition | `buf` holds |
|---|---|---|
| Whole | `total <= limit` | every byte written |
| Truncated | `total > limit` | `buf[:headMax]` is the first `headMax` bytes; `buf[headMax:headMax+tailMax]` is a ring of the last `tailMax` bytes |

Transition: Whole to Truncated on the first write that makes `total > limit`. Never back.

Invariants:

- `cap(buf) <= limit` at all times.
- `Write` returns `len(p), nil` always.
- Rendered length is at most `limit`.
- Truncated rendering is `head + "\n[truncated N bytes]\n" + tail` with
  `len(head) + len(tail) + N == total`, `len(head) <= headMax`, `len(tail) <= tailMax`.
- The rendering depends only on the bytes written, not on how they were split into writes.

## Tool result

`stdout`, a line break if needed, `stderr`, a line break if needed, `[exit N]`, then the optional
fix suggestion section. Unchanged in shape. Each of `stdout` and `stderr` is at most the limit;
everything up to and including the exit line is at most `2 * limit + 32`.
