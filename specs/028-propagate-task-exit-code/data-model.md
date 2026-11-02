# Data Model: Propagate the Task Body's Exit Code

**Feature**: [spec.md](spec.md) | **Research**: [research.md](research.md)

No persisted data. The model is the mapping from the error that stopped a run to
the code both surfaces report.

## Entities

### Body exit code

The status a body's process reported when it failed.

| Source | Field | Value |
|---|---|---|
| Shell body (`internal/runtime/shell`) | `ExecError.Code` | mvdan `ExitStatus`: 1 to 255. A missing command is 127, a signal-killed command is 128 + S (set by the library). |
| Interpreter body (`internal/runtime/interp`) | `ExecError.Code` | `ExitError.ExitCode()`, or 128 + S when the process was signal-killed (R3). On Windows it can exceed 255. |

Validation rule: only a value above 0 counts as a body exit code. A shell parse
error records `Err` with `Code` 0, so it has none.

### Reported failure

The one error returned by `scheduler.Run`, after `classifyRunErr`. It drives the
banner, the process exit code, and the MCP `[exit N]` value. With several
failures (`[parallel]`, or several command-line tasks), it is the first one
returned. That is already the one the banner names.

### `cli.bodyExitCode(err) int`

Moved from `scheduler.failureExitCode` (R14).

- `*shell.ExecError` in the chain with `Code > 0` → `Code`
- `*interp.ExecError` in the chain with `Code > 0` → `Code`
- otherwise → 1

Caller: `cli.CodeFor` (the process and MCP code). Failure hooks get
`Failure.ExitCode`, exported as `RUNE_FAILED_EXIT_CODE`, from the scheduler's
`Engine.ExitCode`, which the CLI implements as `CodeFor(classify(err))`. The two
always agree (FR-009).

## Decision table: reported failure → code

`CodeFor` checks in this order. Rows 1 to 3 are unchanged.

| # | Reported failure (after `classifyRunErr`) | Exit / `[exit N]` | `\|\|` hooks fire? | Change |
|---|---|---|---|---|
| 1 | `nil` | 0 | no | none |
| 2 | `UsageError` | 2 | no | none |
| 3 | `ValidationError` | 3 | no | none |
| 4 | `Interrupted`: the run context was cancelled (SIGINT, SIGTERM), whatever error the body returned | 130 | no | Interpreter and agent bodies, a child that traps SIGINT, and a cancelled `[no-exit-message]` task now land here instead of rows 5 and 6 (R4, R14). |
| 5 | `TaskFailure` wrapping a shell or interpreter `ExecError` with `Code > 0` | `Code` | yes | was 1 |
| 6 | `TaskFailure` with no such `ExecError`: `[confirm]` declined or no terminal, missing interpreter, agent not installed or not authenticated, agent run failure, shell parse error | 1 | yes, except `[confirm]` (body never ran) | none |
| 7 | Anything else | 2 | n/a | none |

`[no-exit-message]` wraps the task's error in a silent `TaskFailure`. `classify`
classifies the wrapped error and keeps the result silent, so the code is the one
the task exits with without the attribute and only the banner is suppressed
(FR-008, R14). An `agent` task with no `agent_cmd` is a `UsageError` (row 2)
either way.

## Interpreter executor error states (`interp.Run`)

| `cmd.Run` outcome | Context | Returned | Code |
|---|---|---|---|
| success | any | `nil` | n/a |
| `*ExitError`, exited normally | not done | `ExecError` | `ExitCode()` |
| `*ExitError`, signal-killed | not done | `ExecError` | 128 + S (new) |
| any error | done | the context error, wrapped (new) | n/a, maps to row 4 |
| other error (did not start) | not done | `ExecError{Err}` | 0, maps to row 6 |
