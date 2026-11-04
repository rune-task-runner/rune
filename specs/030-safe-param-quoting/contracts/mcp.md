# Contract: MCP Changes

**Feature**: 030-safe-param-quoting. Extends
[specs/023-mcp-typed-schemas/contracts/mcp-schema.md](../../023-mcp-typed-schemas/contracts/mcp-schema.md).

## Schema mapping delta

| Declaration | Schema property |
|---|---|
| `p:raw` | `{"type": "string", "description": "Split into shell words without quoting."}` |
| `p:raw` with `[param-doc("p", "Extra flags")]` | `{"type": "string", "description": "Extra flags. Split into shell words without quoting."}` |
| `p:raw` with `[param-doc("p", "Extra flags.")]` | `{"type": "string", "description": "Extra flags. Split into shell words without quoting."}` |
| `*p:raw` | `{"type": "array", "items": {"type": "string"}, "description": "Split into shell words without quoting."}` |

The author text comes first, capped as today. A `.` is added when it does not
already end in `.`, `!`, or `?`, then a space and the fixed sentence.

Every other row of the 023 mapping is unchanged, so schemas for tasks without a
`raw` parameter are byte-identical to v0.6.0.

## Call path

Public additions to package `mcpserver` (additive; `Engine` is unchanged):

```go
// ListEngine is an optional Engine extension. When the engine implements it,
// array arguments reach it with their elements intact (spec 030). args still
// holds every value, arrays joined by single spaces, exactly as Call receives it.
type ListEngine interface {
	CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (Result, error)
}
```

Server behavior per tool call:

| Argument for a variadic parameter | `args[p]` | `lists[p]` |
|---|---|---|
| `["a b", "c"]` | `"a b c"` | `["a b", "c"]` |
| `[]` | `""` | `[]` |
| `"a b"` (scalar) | `"a b"` | `["a b"]` |
| `""` (scalar) | `""` | `[]` |
| omitted | absent | absent |

- Per-element constraint checks run before either call, as today. Rune's
  engine checks the elements in `lists` again.
- When the engine implements `ListEngine`, the server calls `CallLists`;
  otherwise it calls `Call(ctx, name, args)` exactly as v0.6.0 did.
- Rune's own engine implements both. Its `Call` is `CallLists` with `nil` lists,
  under which each variadic value is one element.
- Arrays supplied for a parameter that is not variadic, and unknown parameters,
  are passed in `args` as today and never appear in `lists`.
