# Data Model: Approve a Destructive Call Inside the Agent Client

**Feature**: 049-mcp-confirm-elicitation | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

Nothing here is stored. Every value below lives for one tool call at most.

## Ungranted destructive set

`ungranted(t TaskInfo) []string`, in `mcpserver/authz.go`.

| Rule | Source |
|---|---|
| `t.Name` first, when `t.Destructive` and it is not in `Options.AllowDestructive` | spec 034 FR-005 |
| Then each name in `t.DestructiveReach` that is not granted and not `t.Name`, once, in order | spec 034 FR-005 |
| Empty means the call needs no approval and no grant | FR-003 |

`refusal(t)` builds its text from the same list, so the request and the refusal name the same
tasks.

## Decision for one call

Read top to bottom; the first row that matches decides.

| Ungranted set | `Options.AskApproval` | Engine is an `ApprovalEngine` | Session states form support | Arguments valid | Result |
|---|---|---|---|---|---|
| empty | any | any | any | no | the argument error (unchanged) |
| empty | any | any | any | yes | the call runs (unchanged) |
| not empty | false | any | any | any | `refusal(t)` (unchanged) |
| not empty | true | no | any | any | `refusal(t)` (unchanged) |
| not empty | true | yes | no | any | `refusal(t)` + `cannotAsk` |
| not empty | true | yes | yes | no | the argument error; nobody is asked |
| not empty | true | yes | yes | yes | the person is asked; see Outcomes |

A missing required argument and a value that breaks a constraint at bind time are found by the
engine inside `CallApproved`, before it calls `approve`. They return the engine's error, as today.

## Form support

`formSupport(session) bool`, from the capabilities the client sent in `initialize`.

| `capabilities.elicitation` | Form support |
|---|---|
| absent | no |
| `{}` | yes |
| `{"form":{}}` | yes |
| `{"form":{},"url":{}}` | yes |
| `{"url":{}}` | no |

## Approval request

`ApprovalRequest`, built by the engine, and the names the library adds.

| Field | From | Rules |
|---|---|---|
| called task name | the library: `TaskInfo.Name` | as written in the Runefile, `mod::task` for a module task |
| `Args []ApprovalArg` | the engine: the bound parameters, in declaration order | every declared parameter, defaults resolved; `Values` holds one value, or a variadic's elements (possibly none); values masked by the engine |
| destructive task names | the library: `ungranted(t)` | every name, in order; never taken from the engine |
| `Confirmations []Confirmation` | the engine: `[confirm]` text of each named task | masked by the engine; a missing entry or empty message becomes `Run "NAME"?` |

Limits the library applies when it writes the message:

| Item | Limit | When over |
|---|---|---|
| one argument value | none of its own | never cut: the run uses all of it |
| one `[confirm]` message | 500 characters | cut, ` [truncated]` appended |
| any character that is not printable, in a name, a value or a message | none | written as an escape |
| the whole message | 8192 bytes | Rune does not ask; `refusal(t)` + `cannotAsk` with the size reason |

## Outcomes

One request has exactly one outcome. Only the first runs anything.

| Outcome | Cause | Text after `task "NAME" was not approved: ` |
|---|---|---|
| approved | `accept` and `approved` is `true`, and the snapshot is still current | (the call runs; the result is the task's) |
| Runefile changed | approved, but `Replace` stored a new snapshot meanwhile | `the Runefile changed while the request was pending; call again` |
| not given | `accept` and `approved` is `false` | `approval was not given` (+ remedy) |
| declined | `decline` | `the request was declined` (+ remedy) |
| dismissed | `cancel` | `the request was dismissed` (+ remedy) |
| no answer in time | Rune's wait ended, also when an approval arrived as it ended | `no answer arrived within 5m0s` (+ remedy) |
| no valid answer | another action, a missing or non-boolean `approved`, a client error, a lost connection | `the client returned no valid answer` (+ remedy) |
| call cancelled | the call's own context ended, also when an approval arrived as it ended | `the call was cancelled` |

Every text continues with `. Nothing ran.` Those marked "+ remedy" then add
` To run it without a request, the operator can add NAMES to FLAG and restart the server, or run "CMD TASK" themselves.`
NAMES is the ungranted set joined by commas, TASK the called task. The flag part is left out with
no `AllowDestructiveFlag`, the command part with no `RunCommand`, and the sentence with neither.
The result is an error with text only, like every result of a call that ran no task (spec 044).

## Approval

| Property | Value |
|---|---|
| Holder | `engine.approved map[string]bool`, on the `engine` value `mcpAdapter.call` builds for the call |
| Set | once, after `approve` returned nil, to the names in `tasks` |
| Read | by `confirmFailure`, for the task whose `[confirm]` gate is being passed |
| Covers | exactly the tasks the request named |
| Ends | when `call` returns; the engine value is not reused |
| Never | written to `Options.Yes`, to `mcpAdapter.grants`, to `Server.granted`, or anywhere a second call can read |

## Lifetimes

```text
tools/call arrives
  load snapshot S                      (one load, as today)
  ungranted(t) not empty, can ask
  validate arguments
  engine: bind parameters, build ApprovalRequest
  library: write message, check size
  library: Elicit on the calling session, wait <= ApprovalTimeout
  library: read the answer
  library: both contexts still live ?  (an answer at the deadline does not count)
  library: state.Load() == S ?         (a reload invalidates)
  engine: approved = tasks; run once
tools/call returns                     (approval is gone)
```

## Library additions

| Name | Kind | Purpose |
|---|---|---|
| `Options.AskApproval` | `bool` | turn the request on |
| `Options.ApprovalTimeout` | `time.Duration` | how long to wait; zero means `DefaultApprovalTimeout` |
| `Options.RunCommand` | `string` | the command a person runs a task with, for the fallback text |
| `DefaultApprovalTimeout` | constant, 5 minutes | the default wait |
| `ApprovalEngine` | interface | an engine that can carry an approval into its run |
| `ApprovalRequest`, `ApprovalArg`, `Confirmation` | structs | what the engine reports about the call |
