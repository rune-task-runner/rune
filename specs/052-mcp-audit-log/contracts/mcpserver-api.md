# Contract: `mcpserver` additions for embedders

Everything here is optional. A server built without `Options.Audit` records nothing and
behaves as before.

```go
type Options struct {
    // ...
    // Audit, when set, is told about every request the server handles. A
    // tool call is reported before anything of it runs and again when it
    // ends. Nil records nothing.
    Audit Auditor
}

// Auditor records what a server is asked to do. Every value it receives is
// already redacted and bounded. It is called from many goroutines.
type Auditor interface {
    // CallStarted records the start of a tool call. When it returns an
    // error the call is refused: nothing runs, and the error's text is the
    // tool result.
    CallStarted(ctx context.Context, call AuditCall) error
    // CallEnded records how the call ended. When it returns an error the
    // result of the call is unchanged, and one text block that carries the
    // error's text is added to it.
    CallEnded(ctx context.Context, call AuditCall, end AuditEnd) error
    // Request records a handled request that is not a tool call. Its error
    // is not reported to the client.
    Request(ctx context.Context, req AuditRequest)
    // AuthFailed records a request refused for its bearer token.
    AuthFailed(ctx context.Context, failure AuditAuthFailure)
}

// AuditSession is the session a request arrived on.
type AuditSession struct {
    ID            string // derived; never the protocol's own session id
    Transport     string // "stdio" or "http"
    Auth          string // "process" or "bearer-token"
    ClientName    string // as the client reported it, redacted
    ClientVersion string
}

// AuditCall is what is known about a tool call when it starts.
type AuditCall struct {
    ID       string    // unique invocation id, 16 hex digits
    Start    time.Time // carries the monotonic reading
    Session  AuditSession
    Tool     string // the name the call asked for
    Task     string // the task of that tool; "" for an unknown tool
    Supplied any    // decoded arguments, redacted and bounded; nil when none
    Identity AuditIdentity
}

// AuditEnd is how a tool call ended.
type AuditEnd struct {
    Time      time.Time
    Duration  time.Duration
    Outcome   string // AuditCompleted, AuditFailed, ...
    ExitCode  *int   // nil unless a run produced one
    ErrorKind string // AuditKindValidation, ...; "" when a task ran
    Error     string // redacted, bounded
    Effective []AuditArg
    Approval  *AuditApproval
    Grants    []string
    Plan      *AuditPlan
}

type AuditArg struct {
    Name     string
    Values   []string
    Variadic bool
}

type AuditApproval struct {
    Asked    bool
    Decision string
    Tasks    []string
}

type AuditPlan struct{ Role, ID, Digest string }

type AuditRequest struct {
    Time     time.Time
    Duration time.Duration
    Session  AuditSession
    Method   string
    Failed   bool
    Error    string // redacted, bounded; "" when it succeeded
}

type AuditAuthFailure struct {
    Time       time.Time
    Remote     string
    Suppressed int // failures not reported since the last one
}

// AuditIdentity is what a call ran against.
type AuditIdentity struct {
    Runefile         string
    DefinitionDigest string // hex SHA-256
    ConfigDigest     string // hex SHA-256
    GitState         string // "commit", "unborn", "none", "unavailable"
    GitCommit        string
    GitDirty         *bool
}

// AuditEngine is an optional Engine extension. The server asks the engine
// of the load a call runs against, so the answers belong to that load.
type AuditEngine interface {
    // AuditIdentity reports the project identity now.
    AuditIdentity(ctx context.Context) AuditIdentity
    // Redact masks the engine's secrets in s.
    Redact(s string) string
}

// CallAudit is the record of the call a context belongs to. An engine uses
// it to add what only it knows. Every method is safe on a nil receiver, so
// an engine need not check whether the server records.
type CallAudit struct{ /* ... */ }

// AuditFromContext returns the record of the tool call ctx belongs to, or
// nil when the server records nothing.
func AuditFromContext(ctx context.Context) *CallAudit

func (c *CallAudit) ID() string                       // "" on nil
func (c *CallAudit) SetEffective(args []AuditArg)     // values already masked
func (c *CallAudit) SetErrorKind(kind string)         // why the engine ran nothing
func (c *CallAudit) SetPlan(role, id, digest string)
```

Constants: outcomes `AuditCompleted`, `AuditFailed`, `AuditRefused`, `AuditInvalidArguments`,
`AuditUnknownTool`, `AuditCancelled`, `AuditTimedOut`, `AuditError`; kinds `AuditKindProtocol`,
`AuditKindValidation`, `AuditKindPolicy`, `AuditKindApproval`,
`AuditKindInternal`; plan roles `AuditPlanStored`, `AuditPlanApplied`; approval decisions
`AuditDecisionApproved`, `AuditDecisionDeclined`, `AuditDecisionDismissed`,
`AuditDecisionNotGiven`, `AuditDecisionTimedOut`, `AuditDecisionNoAnswer`,
`AuditDecisionCancelled`, `AuditDecisionChanged`, `AuditDecisionCannotAsk`.

## Rules for the server

- The start is reported from the receiving middleware, before the SDK looks the tool up.
- The snapshot loaded for the start is the one the handler runs against.
- With an engine that is not an `AuditEngine`, the identity is empty and nothing is masked:
  only the secret-name rule and the bounds apply.
- A context that was cancelled gives `AuditCancelled`, and one whose deadline passed gives
  `AuditTimedOut`, whatever else was marked.
- `ExitCode` is set only from a `Result` the engine returned.
- `Grants` lists the destructive tasks of the call that `Options.AllowDestructive` names.
- The session id is a digest of the server's instance id and the protocol session id.
- `AuthFailed` is called at most 10 times a minute.
- Without `Options.Audit` no context value is set and no extra work is done.

## Rules for an engine

- Set the effective arguments once binding succeeded, masked as `ApprovalRequest` values are.
- Mark `AuditKindValidation` when binding failed and `AuditKindPolicy` when a policy or a plan
  check refused the call. An unmarked error is recorded as `AuditKindInternal`.
- Do not record the exit code: the server reads it from the `Result`.

## Other additions

- `internal/mask`: `func SensitiveName(name string) bool`, the built-in secret-name patterns.
- `internal/planstore`: `Event.Invocation string` (`json:"invocation,omitempty"`).
