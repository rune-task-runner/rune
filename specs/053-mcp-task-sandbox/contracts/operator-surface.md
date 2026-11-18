# Contract: What the Operator Sets and Sees

## The mode

| Surface | Form |
|---|---|
| Flag of `rune mcp` and `rune serve` | `--sandbox enforce` or `--sandbox off` |
| Environment of any Rune that serves agents | `RUNE_SANDBOX=enforce` or `RUNE_SANDBOX=off` |

The flag wins. With neither, the mode is `enforce`. An empty `RUNE_SANDBOX` counts as absent.
The endpoint of an `(agent)` task reads the variable.

Flag help: `confine the tasks agents call: enforce (default) or off`

Usage errors, before any Runefile code runs, exit code 2:

| Condition | Text |
|---|---|
| Flag | `--sandbox: "<value>" is not a mode; use enforce or off` |
| Variable | `RUNE_SANDBOX: "<value>" is not a mode; use enforce or off` |

## The startup line

One more part in the parentheses of the existing line, after the others:

| State | Part |
|---|---|
| confining | `sandbox: bubblewrap` |
| unavailable | `sandbox: unavailable, calls are refused` |
| off | `sandbox: off, tasks run unsandboxed` |

Examples:

```text
rune MCP server on stdio (sandbox: bubblewrap)
rune MCP server on http://127.0.0.1:7777 (token required; sandbox: bubblewrap)
rune MCP server on stdio (destructive tasks allowed: clean; sandbox: off, tasks run unsandboxed)
```

## Warnings on standard error at start

Printed before the startup line, in the style of the other startup warnings. An `(agent)` task
prints the same line before it runs its `[context]` task and opens its endpoint.

| State | Text after `warning: ` |
|---|---|
| unavailable | `sandbox: <reason>. Every task call will be refused. <remedy>` |
| off | `sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can.` |

`<reason>` and `<remedy>` are the rows of "unavailable" in
[sandbox-rules.md](sandbox-rules.md). The remedy names the flag and the variable, so one text
serves a server and the endpoint of an agent task.

## A mode an agent task cannot read

An `(agent)` task run with a `RUNE_SANDBOX` that is not a mode fails before its agent program
starts, with the usage error of the variable and exit code 2. Every other task of a
command-line run ignores the variable.

## The audit record

The `sandbox` object of a call, written with its start:

```json
{"enforced":true,"backend":"bubblewrap","mode":"enforce"}
{"enforced":false,"mode":"enforce"}
{"enforced":false,"mode":"off"}
```

The second form is a server whose backend is unavailable: its calls end with outcome
`refused` and `"error":{"kind":"sandbox","message":"task ... was not run: the sandbox is unavailable: ..."}`.

Records written before this feature hold `{"enforced":false}`. A reader treats a missing mode
as unknown.

## `rune log`

Text output, under a call that ran a task (it has an exit code) and whose record has mode
`off`. A refused or invalid call ran nothing and gets no such line:

```text
10:05:02  completed    build                 exit 0   1.2s   claude-code 2.1.0  b81e04f7a2c9d310
          ran without a sandbox (the server was started with the sandbox off)
```

A refused call shows its error line as every refusal does, the kind and then the message:
`sandbox: task "build" was not run: the sandbox is unavailable: ...`.

`rune log --json` carries the `sandbox` object as stored.

## The server library

`mcpserver.AuditKindSandbox = "sandbox"`: the kind an engine records when it refuses a call
because no sandbox can confine it. `CallAudit.SetErrorKind` accepts it, and the call ends with
outcome `refused`. Nothing else in the public package changes.

## Internal entry points of the binary

| Arguments | Meaning |
|---|---|
| `rune --sandbox-worker` | Run one job from descriptor 3 and report on descriptor 4. Started only by Rune, inside a sandbox |
| `rune --sandbox-probe` | Exit 0. Started only by Rune, to prove a sandbox starts |

Both are handled before the command line is parsed and are not listed in the help. Run by
hand, the worker fails with `rune: --sandbox-worker is started by rune itself` and exit code 2.
