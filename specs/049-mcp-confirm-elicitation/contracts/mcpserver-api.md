# Contract: `mcpserver` Library Changes

**Feature**: 049-mcp-confirm-elicitation | **Spec**: [spec.md](../spec.md) | Covers FR-012 to FR-014,
FR-023

For programs that embed the MCP server. `Engine`, `ListEngine`, `Result`, `TaskInfo`, `ParamInfo`,
`New`, `Replace` and the transports are unchanged. Everything below is an addition. A program that
sets none of it behaves exactly as before.

## `Options`

```go
// AskApproval asks the person at the client to approve a call that can run
// a destructive task AllowDestructive does not name, when the client stated
// MCP form elicitation and the Engine is an ApprovalEngine. The call runs
// once only on an explicit yes. Without both, and when this is false, such
// a call is refused.
AskApproval bool
// ApprovalTimeout is how long an approval request waits for an answer.
// Zero or less means DefaultApprovalTimeout.
ApprovalTimeout time.Duration
// RunCommand is the command a person runs a task of this server with,
// such as "rune" or "rune -f /repo/Runefile". When set, the refusal given
// to a client that cannot be asked, and the result of a request that was
// not approved, say to run it with the called task's name.
RunCommand string
```

```go
// DefaultApprovalTimeout is how long an approval request waits when
// Options.ApprovalTimeout is not set.
const DefaultApprovalTimeout = 5 * time.Minute
```

## `ApprovalEngine`

```go
// ApprovalEngine is an optional Engine extension. An engine that implements
// it can run a call a person approved through the client.
type ApprovalEngine interface {
	// CallApproved prepares the call as CallLists would, without running
	// anything, and calls approve with what the person must see. When
	// approve returns an error, CallApproved runs nothing and returns that
	// error unchanged. When it returns nil, CallApproved runs the call once,
	// with the [confirm] gate of exactly the tasks named in tasks passed for
	// this run only. It must not approve any other task, and must not keep
	// the approval after it returns.
	CallApproved(ctx context.Context, name string, args map[string]string,
		lists map[string][]string, tasks []string,
		approve func(context.Context, ApprovalRequest) error) (Result, error)
}
```

`tasks` is the call's ungranted destructive set, worked out by the server from `TaskInfo`. `approve`
is the server's: it writes the message, sends the request, waits, reads the answer and checks that
the server's tasks were not replaced meanwhile. The engine calls it at most once.

## `ApprovalRequest`

```go
// ApprovalRequest is what an engine reports about a call that needs
// approval. Every string must already be masked: this package escapes and
// cuts them but knows no secret.
type ApprovalRequest struct {
	// Args are the called task's parameters with the values it will run
	// with, defaults included, in declaration order.
	Args []ApprovalArg
	// Confirmations give the confirmation message of each task named in
	// CallApproved's tasks. A task with no entry is shown with a default
	// question.
	Confirmations []Confirmation
}

// ApprovalArg is one parameter and what it is bound to: one value, or a
// variadic's elements, which may be none.
type ApprovalArg struct {
	Name   string
	Values []string
}

// Confirmation is one destructive task and the message its author wrote.
type Confirmation struct{ Task, Message string }
```

The task names in the request come from the server, not from `Confirmations`. An entry for a task
that is not in `tasks` is ignored.

## Behavior

| Embedder | A call that can run an ungranted destructive task |
|---|---|
| sets nothing new | refused, with today's text |
| sets `AskApproval`, engine is not an `ApprovalEngine` | refused, with today's text |
| sets `AskApproval`, engine is an `ApprovalEngine`, client cannot be asked | refused; today's text, then `Rune could not ask for approval here: ...`. When `RunCommand` is set, `Or the operator can run "CMD TASK" themselves.` comes before it, or `The operator can run "CMD TASK" themselves.` when `AllowDestructiveFlag` is empty |
| sets `AskApproval`, engine is an `ApprovalEngine`, client can be asked | the person is asked. Every outcome but a cancelled call and a changed Runefile ends with the same remedies: `add NAMES to FLAG and restart the server` when `AllowDestructiveFlag` is set, `run "CMD TASK" themselves` when `RunCommand` is set, and no such sentence when neither is |

`Replace` during a pending request makes that request's approval unusable: the call is not run.

## What the `rune` CLI sets

| Server | `AskApproval` | `RunCommand` | `ApprovalTimeout` |
|---|---|---|---|
| `rune mcp`, `rune serve` | `true` | `"rune"`, or `"rune -f PATH"` with the absolute path of the Runefile when the server was started with `--file` (in single quotes when the path holds a space or another character a shell acts on) | zero (5 minutes) |
| the endpoint of an `agent` task | `false` | empty | zero (unused) |
