# Contract: `mcpserver` additions for embedders

```go
type TaskInfo struct {
    // ...
    // AppliesPlan, when not empty, names the task whose stored plan this task
    // applies. The tool is advertised as destructive and says so in its
    // description. Every call is put to the person at the client through the
    // ApprovalEngine, whatever Options.AllowDestructive names: a grant does
    // not approve a plan. Without Options.AskApproval, an ApprovalEngine and a
    // client that can show a form, the call is refused.
    AppliesPlan string
}

type ApprovalRequest struct {
    Args          []ApprovalArg
    Confirmations []Confirmation
    // Plan describes the stored plan the call would apply. The engine of a
    // task with AppliesPlan sets it. Nil for every other call.
    Plan *PlanApproval
}

// PlanApproval is what a person is shown about a plan. Every string must
// already be masked: this package escapes and cuts them but knows no secret.
type PlanApproval struct {
    ID      string // the plan's id
    Task    string // the task that made it
    Created string // RFC 3339, UTC
    Digest  string // hex SHA-256 of the artifact
    Size    int64  // bytes of the artifact
    Summary string // the stored summary, in full; the server cuts it
    Notes   []string // one line each, shown above the summary, without "Note: "
}

// SummaryLines renders a summary for a person: each line indented by two
// spaces with characters that are not printable escaped, "  (empty)" for an
// empty one, and, when the result would pass head+tail bytes, its head, a cut
// mark with the number of bytes left out, and its tail.
func SummaryLines(summary string, head, tail int) string
```

Rules for the server:

- `ungranted` semantics are unchanged for every other task.
- A call to a task with `AppliesPlan` always goes to `CallApproved` when the engine is an
  `ApprovalEngine`, after the arguments are validated. When the server does not ask, or the
  client cannot show a form, the `approve` function it passes returns the refusal, so the
  engine's own checks come first.
- A request for such a task whose `Plan` is nil is refused.
- For a task with `AppliesPlan`, the names passed to `CallApproved` always start with the task
  itself, then the ungranted destructive tasks it reaches.
- The refusals for such a task never name `Options.AllowDestructiveFlag`.
- A request with `Plan` ends with `Approving applies this plan once.`

Rules for an engine:

- `CallApproved` must resolve the plan itself. Nothing in `args` selects one.
- `Call` and `CallLists` must refuse a task that applies a plan.
- `TaskInfo.Destructive` stays the `[confirm]` flag. An engine does not set it for `AppliesPlan`.

Both fields are optional. An embedder that sets fields by name is not affected.
