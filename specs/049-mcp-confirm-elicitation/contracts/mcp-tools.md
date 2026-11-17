# Contract: MCP Tool Calls That Need Approval

**Feature**: 049-mcp-confirm-elicitation | **Spec**: [spec.md](../spec.md) | Covers FR-001 to FR-005,
FR-012 to FR-023

What an MCP client observes from `rune mcp` and `rune serve`. The request itself is in
[approval-request.md](approval-request.md), the decision table in
[data-model.md](../data-model.md#decision-for-one-call).

## `tools/list`

Unchanged, byte for byte. A destructive task is listed with `destructiveHint: true` whether or not
the client can be asked.

## `tools/call` with nothing to approve

Unchanged: a task that can run no `[confirm]` task, or only granted ones, runs with no request.

## `tools/call` that can run an ungranted `[confirm]` task

### A client that can be asked

Order of checks: the tool exists, the arguments are valid, then the request.

| Step | Result when it fails |
|---|---|
| The tool is registered | JSON-RPC error `unknown tool`, as today |
| No undeclared argument name | `unknown argument "x" for task "apply"`, as today; no request |
| Every value has a valid type and meets its constraint | `invalid arguments: ...`, as today; no request |
| Every required argument is present | `task "apply" is missing required argument "env"`, as today; no request |
| The request, with every argument whole, fits in 8192 bytes | the fallback text below with the size reason; no request |
| The person approves | see the outcome texts |

When the person approves, the result is the task's normal result: text, structured content,
`isError` from the exit code. Nothing in it mentions the approval.

### Outcome texts

`isError: true`, one text block, no structured content.

| Outcome | Text |
|---|---|
| not given | `task "apply" was not approved: approval was not given. Nothing ran. To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.` |
| declined | `task "apply" was not approved: the request was declined. Nothing ran. To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.` |
| dismissed | `task "apply" was not approved: the request was dismissed. Nothing ran. To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.` |
| no answer in time | `task "apply" was not approved: no answer arrived within 5m0s. Nothing ran. To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.` |
| no valid answer | `task "apply" was not approved: the client returned no valid answer. Nothing ran. To run it without a request, the operator can add apply to --allow-destructive and restart the server, or run "rune apply" themselves.` |
| call cancelled | `task "apply" was not approved: the call was cancelled. Nothing ran.` |
| Runefile changed | `task "apply" was not approved: the Runefile changed while the request was pending; call again. Nothing ran.` |

The task named first is the called task, also when the destructive task is one it reaches. Every
outcome but a cancelled call and a changed Runefile ends with what the operator can do. A decline
has it too: a client run with nobody at it can decline by itself (Codex run as `codex exec` does),
so a "no" is not always a person's. The sentence names the operator because the agent reads it as
well. It names the tasks to add, as the refusal does, and the called task for the command: for
`release`, which reaches `clean`,
`... To run it without a request, the operator can add clean to --allow-destructive and restart the server, or run "rune release" themselves.`
With several tasks it reads `add clean,wipe to`.

### A client that cannot be asked

A client that stated no elicitation, or URL elicitation only. The check order is today's: the
refusal comes before the argument checks.

`isError: true`, one text block, no structured content. The text is today's refusal followed by two
sentences:

| Call | Text |
|---|---|
| `clean`, no grant | `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune clean" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.` |
| `release`, which reaches `clean` | `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune release" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.` |
| `reset`, destructive, reaches `wipe` | `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server. Or the operator can run "rune reset" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.` |
| `tools__clean` (task `tools::clean`) | `task "tools::clean" is destructive; to permit it, add tools::clean to --allow-destructive and restart the server. Or the operator can run "rune tools::clean" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.` |

For a client that can be asked but whose request would be too large, the last sentence ends
`: the approval request would be too large to show.`

A server started with `--file` names its Runefile in the command, here and in the outcome texts:
`run "rune -f /repo/Runefile release" themselves`. A bare `rune release` in the operator's own
directory would find no Runefile, or another project's.

## What an approval covers

| Situation | Result |
|---|---|
| `apply` approved, then `apply` called again | a new request |
| `apply env=staging` approved, then `apply env=prod` | a new request showing `prod` |
| two calls in progress, one approved and one declined | only the approved one runs |
| two HTTP sessions each call `apply` | each session gets its own request and no other |
| `release` approved, where the request named `clean` | `build`, `clean`, `release` run |
| a run reaches a `[confirm]` task the request did not name | that task is declined by the engine, as today |
| `--deny clean`, then `release` is called | the person is asked, and the request names `clean`: `--deny` hides a tool, not what other tools run |
| `--deny release`, then `release` is called | unknown tool, as today; nobody is asked |
| the Runefile reloads while a request is pending, then the person approves | nothing runs: Runefile changed |
| a reload fails to load while a request is pending, then the person approves | the call runs: the server still serves what was shown |

## Transports

The same on stdio and Streamable HTTP. HTTP still requires the bearer token on every request. On
HTTP the request travels on the response stream of the `tools/call` it belongs to.

## Grants

`--allow-destructive` is unchanged (spec 031). A granted task is not asked about and is not named
in a request. A call whose destructive tasks are all granted runs with no request.

## Agent-executor endpoint

Unchanged. The endpoint Rune opens for an `agent` task's own agent never asks, whatever that agent's
client states. Its texts are spec 034's:
`task "clean" is destructive and is not allowed in this session`.
