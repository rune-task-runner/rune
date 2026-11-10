# Contract: `mcpserver` API

**Feature**: 033-mcp-tool-hints | Covers FR-012, FR-017

`mcpserver.TaskInfo` gains two fields, and one existing field widens. No other exported name changes.

```go
type TaskInfo struct {
	// ...existing fields...

	// Destructive marks the task itself destructive. A call is refused unless
	// the task is granted, and the tool advertises DestructiveHint.
	Destructive bool
	// ReachesDestructive says a run of this task can reach a destructive task
	// through its dependencies or hooks. The tool advertises DestructiveHint.
	// Authorization ignores it: the engine decides when the run reaches that task.
	ReachesDestructive bool
	// Network says the task, or anything a run of it can reach, uses the
	// network. The tool advertises OpenWorldHint.
	Network bool
	// ReadOnly says the task declares itself read-only. The tool advertises
	// ReadOnlyHint. A descriptor that leaves it false makes no read-only claim.
	ReadOnly bool
}
```

## For embedders

| Before this change | After |
|---|---|
| A task without `Destructive` advertised `readOnlyHint: true` | It makes no read-only claim. Set `ReadOnly` on tasks where the claim is true |
| `Destructive` set the hint and the refusal | Unchanged |
| Hints came from the task's own flags | Unchanged unless the engine also sets `ReachesDestructive` or widens `Network` |

The library takes a descriptor as given. It does not reject `ReadOnly` with `Destructive`, and a
`Destructive` task is refused whatever `ReadOnly` says.

Existing descriptors written with field names keep compiling. Only descriptors written without field
names break, as they do whenever `TaskInfo` gains a field.
