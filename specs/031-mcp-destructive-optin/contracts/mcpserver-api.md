# Contract: `mcpserver` library API change

**Feature**: 031-mcp-destructive-optin | Covers FR-016

`github.com/rune-task-runner/rune/mcpserver` is public and embeddable
(constitution Principle IV). One field changes type and one field is added.
Nothing else in the package's exported API changes.

## Before (v0.6.0)

```go
type Options struct {
	// AllowDestructive permits calling tasks marked destructive ([confirm]).
	AllowDestructive bool
	AllowList        []string
	Version          string
	Instructions     string
}
```

## After

```go
type Options struct {
	// AllowDestructive names the destructive ([confirm]) tasks that may be
	// called. Any other destructive task is refused before the engine runs.
	// There is no value that permits all of them. The Engine must approve the
	// confirmation of exactly these tasks, wherever a run reaches them; the
	// rune CLI adapter does.
	AllowDestructive []string
	// AllowDestructiveFlag is the command-line flag that sets AllowDestructive,
	// such as "--allow-destructive". When set, a destructive refusal tells the
	// caller to add the task to it. Leave it empty where no grant is possible,
	// such as the endpoint of a rune agent task: the refusal then offers no
	// remedy.
	AllowDestructiveFlag string
	AllowList            []string
	Version              string
	Instructions         string
}
```

## Migration

| Embedder code | After the upgrade |
|---|---|
| `Options{}` or `Options{AllowDestructive: false}` | `Options{}` compiles; `false` is a compile error. Behavior: all destructive tasks refused, as before. |
| `Options{AllowDestructive: true}` | compile error at that line. Replace with the task names to allow, e.g. `Options{AllowDestructive: []string{"clean"}}`. |

The compile error is deliberate: no embedder keeps an approve-all server without
noticing.

## Refusal text

A refused destructive call returns an error tool result. With
`AllowDestructiveFlag: "--allow-destructive"` (as `rune serve` and `rune mcp`
set it) the text is
`task "NAME" is destructive; to permit it, add NAME to --allow-destructive and restart the server`.
With no flag (the agent-task endpoint, and any embedder that leaves it empty)
the text is `task "NAME" is destructive and is not allowed in this session`.
An `AllowList` refusal returns `task "NAME" is not authorized for this session`.
Both replace `task "NAME" requires explicit approval and is not authorized for this session`.

## Release note

The PR title carries `!` and `breaking:` because git-cliff prints only the
subject ([research R9](../research.md)).
