# Contract: Tool Results Under the Output Limit

**Feature**: 042-mcp-output-cap | Covers FR-001 to FR-017

`L` is the output limit in force.

## Shape

Unchanged:

```text
<stdout>
<stderr>
[exit N]
[fix suggestion - from Runefile || failure hooks]
<fix suggestion>
```

A line break is inserted after stdout and after stderr only when the part does not end in one. The
fix suggestion section appears only when a `||` hook printed something.

## Each stream

| Captured bytes | Returned |
|---|---|
| at most `L` | the bytes, unchanged |
| more than `L` | `head`, `"\n[truncated N bytes]\n"`, `tail` |

For a truncated stream:

- `head` starts at the first captured byte. `tail` ends at the last captured byte.
- `len(head) <= (L - 40) / 2` and `len(tail) <= L - 40 - (L - 40) / 2`. Each is shorter by at most
  3 bytes when the cut would split a UTF-8 character.
- `N = captured - len(head) - len(tail)`, in decimal.
- The returned stream is at most `L` bytes.
- The same captured bytes always give the same text.

With `L = 65536`: head 32,748 bytes, tail 32,748 bytes.

## Whole result

- Everything up to and including `[exit N]` is at most `2 * L + 32` bytes.
- `[exit N]` is always complete and comes after the output.
- `isError` is true exactly when N is not 0.
- The fix suggestion keeps its 8 KiB limit and its `[truncated]` marker.

## Masking

The captured bytes are the masked bytes. A secret value never appears in `head`, `tail`, or
anywhere else in the result, wherever it sat in the output.

## While the task runs

The server holds at most `L` bytes for each stream of a call. Writes from the task always succeed.
