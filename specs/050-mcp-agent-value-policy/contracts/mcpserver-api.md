# Contract: `mcpserver` Library Additions

**Feature**: 050-mcp-agent-value-policy | Covers FR-027 to FR-030, and the embedded server edge case

One field is added. Nothing is removed or renamed, and no signature changes.

## `ParamInfo.AgentValues`

```go
type ParamInfo struct {
	// ... existing fields ...

	// AgentValues, when not empty, lists the only values an agent may pass for
	// this parameter. The tool's schema enumerates them in place of Enum, and
	// the tool's description states them. The server does not refuse a call by
	// them: the Engine must, because only it knows the value a default gives.
	// Not for a variadic parameter.
	AgentValues []string
}
```

| Aspect | Behavior |
|---|---|
| nil or empty | The parameter is advertised as before |
| One or more values | `inputSchema` sets the property's `enum` to it. `toolFor` appends `Agent policy: agents may run this task only with <name>="<value>"[ and ...].` as the last paragraph of the description |
| Several values | Written as `<name>="a" or "b"`. Rune's own adapter always sets one |
| Variadic parameter | The field is ignored |
| `ParamInfo.check` | Does not read it. A supplied value is validated against `Kind` and `Enum` only |

## What an embedding program has to do

The server advertises the restriction. It does not enforce it. An `Engine` that sets
`AgentValues` must refuse, in `Call`, `CallLists` and `CallApproved`, a call whose effective value
for that parameter is not one of them, including a value that comes from a default and a value a
task passes to another task. Rune's own adapter does this in `internal/cli`.

## Compatibility

`ParamInfo` is a struct that programs fill by field name. A program that does not set the field
gets the tools it got before.
