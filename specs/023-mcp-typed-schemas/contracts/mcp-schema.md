# MCP Tool Schema Contract: Kind → JSON Schema Mapping

**Feature**: 023-mcp-typed-schemas · consumed by `mcpserver.inputSchema` / `toolFor`
(mcpserver/server.go). SDK: `github.com/modelcontextprotocol/go-sdk/mcp` v1.6.1.

## Property mapping

| Declaration | Schema property |
|---|---|
| `p` (unannotated) / `p:string` | `{"type": "string"}` |
| `p:number` | `{"type": "number"}` |
| `p:boolean` | `{"type": "boolean"}` |
| `p:path` | `{"type": "string", "format": "path"}` |
| `p:enum("a","b")` | `{"type": "string", "enum": ["a", "b"]}` |
| `+p:kind` / `*p:kind` | `{"type": "array", "items": { <mapped kind> }}` |
| `[param-doc("p","text")]` | adds `"description": "text"` to `p`'s property |

- `required` list semantics unchanged: required and `+` variadic params are listed;
  defaulted and `*` are not.
- Defaults are **not** embedded (they are expressions evaluated at bind time; a static
  `default` could lie). Optionality is conveyed by `required` alone.
- Unannotated parameters produce a schema **byte-identical** to the pre-feature output
  (SC-003 regression guard: golden assertion in `TestInputSchema`).

## Tool description composition

```
<task doc comment or [doc] override>

Returns: <text of [returns], when present>
```

No `Returns:` trailer is emitted when the attribute is absent (no invented placeholders —
spec US3 scenario 2). The composed description passes the existing agent-text cap
(8 KiB, `capAgentText`) and, like all agent-facing text, the masking pipeline.

## Worked example

```rune
# Deploy the service.
[returns("URL of the deployed environment on stdout")]
[param-doc("env", "Target environment")]
[param-doc("replicas", "Desired replica count")]
deploy env:enum("staging","prod") replicas:number="2":
    ./deploy.sh {{env}} {{replicas}}
```

`tools/list` entry (fields elided to the contract-relevant subset):

```json
{
  "name": "deploy",
  "description": "Deploy the service.\n\nReturns: URL of the deployed environment on stdout",
  "inputSchema": {
    "type": "object",
    "properties": {
      "env": {
        "type": "string",
        "enum": ["staging", "prod"],
        "description": "Target environment"
      },
      "replicas": {
        "type": "number",
        "description": "Desired replica count"
      }
    },
    "required": ["env"]
  },
  "annotations": { "readOnlyHint": true }
}
```

## Invocation-time enforcement (server side)

- Scalar arguments: validated post-`decodeArgs` against `ParamInfo` (string-form rules
  identical to `ast.Constraint.Check`); violation → MCP tool **error result** (not a
  protocol error) with the FR-006 sentence: parameter, quoted value, accepted set. The
  engine re-validates at bind time (defense in depth).
- Variadic arguments: JSON arrays are validated **per element before** the space-join in
  `decodeArgs` (handler.go:50-55) — the join destroys element boundaries, so this is the
  only point where FR-008 per-value semantics exist on the MCP path.
- Unknown/extra argument names: rejected (existing behavior, unchanged).
- No violation ever reaches the scheduler: zero side effects (SC-002).

## Compatibility

`ParamInfo`/`TaskInfo` gain fields additively; `Engine.Call`'s signature is unchanged.
Existing embedders of `mcpserver` recompile without edits and serve unchanged schemas
until their Runefiles adopt annotations.
