# Contract: `mcpserver` Library Changes

**Feature**: 034-mcp-dependency-refusal | **Spec**: [spec.md](../spec.md)

For programs that embed the MCP server. `Engine`, `ListEngine`, `Result`, `ParamInfo`, `New`, and the
transports are unchanged.

## `TaskInfo`

```go
type TaskInfo struct {
	Name   string
	Doc    string
	Params []ParamInfo
	// Destructive marks the task itself destructive. A call is refused unless
	// the task is granted, and the tool advertises DestructiveHint.
	Destructive bool
	// DestructiveReach names the destructive tasks a run of this task can
	// reach through its dependencies and hooks: each once, not the task
	// itself, in the order a run first reaches them. A call is refused unless
	// every one is granted, and a non-empty list sets DestructiveHint. Nil
	// leaves the engine alone to decline such a task when a run reaches it.
	DestructiveReach []string
	Network  bool
	ReadOnly bool
	Returns  string
}
```

| Change | Against `main` at `840ebef` | Against `v0.6.0` |
|---|---|---|
| `DestructiveReach []string` | Replaces `ReachesDestructive bool` | New field |
| `ReachesDestructive bool` | Removed | Never released |

An embedder that sets `DestructiveReach` gets the up-front refusal and the destructive hint for
reaching tasks. One that leaves it nil gets the refusal and the destructive hint from `Destructive`
alone, as it does today.

## `Options`

No new fields. The `AllowDestructive` doc comment changes to say what is now refused:

```go
// AllowDestructive names the destructive ([confirm]) tasks that may be
// called or run. A call is refused before the engine runs when the task
// itself, or any task in its DestructiveReach, is destructive and not named
// here. There is no value that permits all of them. The Engine must still
// approve the confirmation of exactly these tasks, wherever a run reaches
// them; the rune CLI adapter does.
AllowDestructive []string
```

`AllowDestructiveFlag` keeps its meaning: when set, the refusal tells the caller to add the names to
it. When empty, the refusal names the tasks and offers no remedy. The texts are in
[mcp-tools.md](mcp-tools.md#refusal-texts).

## Behavior

- The refusal reads the descriptor the server registered for the tool, not a fresh `Tasks()` result.
  `New` already registers each tool once from one `Tasks()` call.
- The allow-list check still comes first, then the destructive refusal, then argument checks.
- Names in `DestructiveReach` are used in the given order. The server does not check that they are
  tasks and does not reorder them, but it names each task once and skips the called task's own name,
  which `Destructive` covers.
