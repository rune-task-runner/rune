# Contract: `mcpserver` Library Changes

**Feature**: 041-mcp-allow-deny | Covers FR-018

For programs that embed the MCP server. `Engine`, `ListEngine`, `Result`, `TaskInfo`, `ParamInfo`,
and the transports are unchanged.

## `Options`

```go
type Options struct {
	AllowDestructive     []string // unchanged
	AllowDestructiveFlag string   // unchanged
	// AllowList, when non-empty, exposes only these tasks as tools. A task
	// outside it is not listed and cannot be called. It never adds a task the
	// Engine does not offer.
	AllowList []string
	// DenyList names tasks that are not exposed as tools, whether or not
	// AllowList names them.
	DenyList []string
	Version      string // unchanged
	Instructions string // unchanged
}
```

| Change | Before | After |
|---|---|---|
| `AllowList` | Task stays in `tools/list`; a call returns the tool error `task "NAME" is not authorized for this session` | Task is not registered: absent from `tools/list`, a call gets the unknown-tool error |
| `DenyList` | none | New field |

This is a breaking change for an embedder that sets `AllowList` and relies on the refused tasks
being listed or on the refusal text.

## `Server`

```go
// Exposure reports how many tasks are exposed as tools and how many the
// Engine offered.
func (s *Server) Exposure() (exposed, exposable int)
```

New method. With no `AllowList` and no `DenyList` the two numbers are equal.

## Behavior

- A task is a tool when `(len(AllowList) == 0 || AllowList contains Name) && !(DenyList contains
  Name)`. Names are `TaskInfo.Name` values (`ns::task`), not tool names (`ns__task`).
- Names in either list that match no task are ignored. The library does not validate them.
- The filter is applied once, in `New`. It does not affect what `Engine.Call` runs: dependencies
  and hooks of an exposed task are the engine's business.
- The filter comes before every other check, because a hidden task has no handler. For an exposed
  task the order is unchanged: destructive refusal, then argument checks.
- `AllowDestructive` is independent. A name there need not be exposed, and exposing a destructive
  task does not grant it.
