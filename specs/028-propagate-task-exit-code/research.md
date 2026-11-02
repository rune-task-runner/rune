# Research: Propagate the Task Body's Exit Code

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

All decisions below were checked against the code on `main` at `db34a03` and the
vendored shell library `mvdan.cc/sh/v3@v3.13.1`.

## Current behavior (baseline)

- `cmd/rune/main.go` exits with `cli.CodeFor(err)`. `CodeFor` maps every
  `*TaskFailure` to `ExitTaskFail` (1), whatever it wraps.
- The MCP adapter (`internal/cli/mcp.go`) computes its result code the same way:
  `CodeFor(eng.classifyRunErr(runErr))`. `mcpserver.formatResult` prints it as
  `[exit N]` and sets `IsError` when it is non-zero.
- The real body code already exists on the error. `shell.ExecError.Code` and
  `interp.ExecError.Code` carry it, the banner prints it, and
  `scheduler.failureExitCode` extracts it for `RUNE_FAILED_EXIT_CODE` (spec 022).
- The scheduler returns the first failure unchanged. `scheduler.Run` stops at the
  first failing root; `[parallel]` uses `errgroup.Wait`, which returns the first
  non-nil error. The banner and `CodeFor` receive that same error.

So the code is lost in exactly one place, `CodeFor`, and both surfaces read it from
there.

## R1. Where the exit code is decided

Revised by R14: the extraction moved to `cli.bodyExitCode`, and hooks get their
code from `Engine.ExitCode`.

**Decision**: `CodeFor` maps a `*TaskFailure` to the body exit code it wraps,
using one exported extraction function shared with the failure-hook context.
`scheduler.failureExitCode` is renamed and exported as
`scheduler.BodyExitCode(err error) int`. `runFailHooks` and `cli.CodeFor` both
call it.

**Rationale**: One function computes both `RUNE_FAILED_EXIT_CODE` and the process
exit code, so FR-009 (the two are always equal) holds by construction. The CLI
package already imports `scheduler`. Because the MCP adapter goes through
`CodeFor`, FR-010 (MCP parity) needs no MCP-specific code.

**Alternatives considered**:

- *A `Code` field on `TaskFailure`, set where it is built.* There are 8
  construction sites (`run.go`, `confirm.go`), and a missed one silently reports 1.
  Rejected.
- *Match any error with an `ExitCode() int` method via `errors.As`.* Rejected:
  `*exec.ExitError` has that method too, and agent errors (`agent.AuthError`) wrap
  it. Agent CLI codes would leak through, contradicting FR-006 and the spec's
  agent-executor assumption.
- *A second copy of the extraction in `internal/cli`.* Two copies can drift and
  break FR-009. Rejected.

## R2. Which errors count as a body exit code

**Decision**: Only `*shell.ExecError` and `*interp.ExecError` with `Code > 0`.
Anything else, or a code of 0 or below, yields 1.

**Rationale**: These are the two executors that run author-written bodies. Every
other `TaskFailure` lacks a body status: a declined or non-interactive
`[confirm]`, `MissingInterpreterError`, agent errors, and shell parse errors
(`ExecError` with `Err` set and `Code` 0). That covers FR-006 with no special
cases. The existing check is `!= 0`. Tightening it to `> 0` turns the
interpreter's `-1` (see R3) into 1 instead of reporting `-1`, which today leaks
into `RUNE_FAILED_EXIT_CODE`.

Release targets are amd64 and arm64 only (`.goreleaser.yaml`), so `int` is 64-bit
and a Windows `uint32` exit code is always positive.

## R3. Interpreter bodies killed by a signal

**Decision**: In `interp.Run`, when the process was terminated by a signal, set
`Code = 128 + signal`. Detect it with an interface assertion on
`ExitError.Sys()` for `Signaled() bool` and `Signal() syscall.Signal`. No build
tags are needed: `syscall.WaitStatus` has both methods on Linux, macOS, and
Windows, and on Windows `Signaled()` is always false.

**Rationale**: `exec.ExitError.ExitCode()` returns -1 for a signal-killed process.
FR-007 asks for the shell convention, and the shell executor already follows it:
mvdan's `DefaultExecHandler` returns `ExitStatus(128 + status.Signal())`
(`interp/handler.go:165-169`). Fixing it at the source keeps `ExecError.Code`
correct for every reader: the banner, the hook context, and `CodeFor`.

**Alternatives considered**: mapping -1 in `BodyExitCode`. Rejected because the
signal number is gone by then.

## R4. Interpreter bodies cancelled by Ctrl-C

**Decision**: In `interp.Run`, check `ctx.Err()` after a failed `cmd.Run`. When
the context is done, return the context error rather than an `ExecError`.

**Rationale**: `exec.CommandContext` kills the child on cancellation, and
`Cmd.Wait` prefers the resulting `*ExitError` over the context error. Today's
`errors.Is(err, context.Canceled)` check in `interp.Run` therefore never fires for
a killed child. A Ctrl-C'd Python body is reported as a task failure: exit 1, and
its `||` hooks fire, contrary to spec 022 FR-002. After R1 and R3 it would exit
137 (128 + SIGKILL). The spec requires 130. mvdan's handler already does this
check (`if ctx.Err() != nil { return ctx.Err() }`), so this aligns the two
executors. `classifyRunErr` then maps the error to `Interrupted` (130), and the
scheduler skips failure hooks for it.

## R5. Shell bodies

**Decision**: No change to `internal/runtime/shell`.

**Rationale**: mvdan already reports `exit N` as `ExitStatus(N)`, a missing
command as 127, a signal-killed command as 128 + S, and a cancelled context as
`ctx.Err()`. `shell.Run` turns these into `ExecError{Code}` or the context error.

**Limitation found**: mvdan's `ExitStatus` is `uint8`, so a shell body's code is
0 to 255 on every OS. A Windows program that exits with a larger code is reported
modulo 256, and a low byte of 0 reads as success. This predates the feature and
lives in the upstream library. The spec's Windows edge case is refined to say so.
Interpreter bodies on Windows report the full code.

## R6. Codes that overlap rune's own codes

**Decision**: Pass them through unchanged (FR-004). No remapping, no extra output.

**Rationale**: The issue's example uses exit 2 for "refused", which equals
`ExitUsage`. Remapping would defeat the feature. The banner already distinguishes
the cases (`rune: task "x" failed at "exit 2": exit status 2` versus
`rune: unknown task: x`). The docs gain an overlap note (FR-012).

## R7. MCP

**Decision**: No change in `mcpserver/`. The public `Result.ExitCode int` field
and `formatResult` already carry any code.

**Rationale**: The adapter derives the code via `CodeFor` (R1). The public package
API stays byte-identical, and so does the result layout. Only the number in
`[exit N]` changes.

## R8. Existing tests that pin exit 1

These tests assert 1 for a body that exits with another code. They encode the
defect, so they are updated to the exact body code (a stricter assertion, not a
weaker one):

| Test | Body | Today | After |
|---|---|---|---|
| `test/integration/us1_test.go` `TestUS1_FailingTaskExitsNonZero` | `exit 7` | 1 | 7 |
| `test/integration/error_banner_test.go` (styled/plain, plain-only, `[no-exit-message]`) | `exit 7` | 1 | 7 |
| `test/integration/failure_hooks_cli_test.go` (header, hook-fails case) | `exit 7` | 1 | 7 |
| `test/integration/failure_hooks_mcp_test.go` | `exit 7` | `[exit ` | `[exit 7]` |
| `internal/cli/failhook_test.go` `TestCallFailHookFixSuggestion` | `exit 7` | `!= 0` | `== 7` |

`test/integration/confirm_test.go` uses `exit 1` and stays as is. The watch tests
assert banner styling, not the process code, because watch mode does not exit on
a failure (`internal/cli/watch.go` only returns usage errors or `Interrupted`).

## R9. Documentation

**Decision**: Update the pages that state exit 1 for a body failure:

- `docs/cli.md` exit-code table: body failure exits with the body's code (1 when
  the failure has no exit status). Reserved codes 2, 3, 130. Overlap note.
- `docs/troubleshooting.md`: the "Exit codes at a glance" table and the
  "A task body failed" section, whose example already shows `exit status 7` but
  claims exit 1. The missing-interpreter line keeps exit 1.
- `docs/mcp.md`: a short paragraph saying the `[exit N]` marker carries the
  body's own code, the same as the CLI; that 2, 3, and 130 are rune's own codes
  for usage, validation, and interrupt; and that a body may exit with one of
  those numbers, so the marker alone does not tell them apart. FR-012 asks each of the
  three pages to state this, so the MCP guide does not rely on a link alone.
- `docs/examples/ci-cd/`: the README line "`1` task failure" and the new
  `release-gate` task (R10).

`docs/runefile.md` and `docs/how-to/dependencies-and-hooks.md` describe failure
hooks ("never change the exit code"), which stays true. They need no edit. The
`[no-exit-message]` hover text already says "exit code is unaffected".

## R10. A documented, tested example (SC-004)

**Decision**: Add a `release-gate` task to `docs/examples/ci-cd/Runefile`. It
exits 2 when given a closed window and 0 otherwise. Its README shows a CI snippet
that branches on `$?`. A new docs-harness test, `test/docs/exitcode_test.go`,
runs `rune --file docs/examples/ci-cd/Runefile release-gate closed` and asserts
exit 2, then runs the default and asserts exit 0.

**Rationale**: The ci-cd example already claims to demonstrate exit codes, so this
extends it instead of adding a new example directory with its own README contract
and coverage-matrix entry. The task uses only mvdan builtins (`[`, `echo`,
`exit`), so it runs on all three CI operating systems. Tier B keeps running
`rune ci`, which still exits 0.

## R11. Release notes

**Decision**: The PR title uses the breaking form already used by `db34a03`:
`fix(cli)!: breaking: a failed task exits with its body's exit code, not always 1`.
The squash merge appends the PR number, as it did for `(#66)` on `db34a03`, so
the issue number stays out of the title; the PR body starts with "Closes #33".
git-cliff renders only the title into the changelog and the release notes
(`Breaking: a failed task exits with its body's exit code, not always 1 (#NN)`,
checked with `git cliff --unreleased --with-commit`), so the title itself must
name the affected pattern, scripts that expect exactly 1, to satisfy FR-013.
The PR body repeats it with an example.

## R12. Out of scope

- Exit codes of agent-executor tasks (spec assumption).
- Windows shell-body codes above 255 (R5, upstream library type).
- Watch mode, which never exits on a task failure.

## R13. `[no-exit-message]` bodies cancelled by Ctrl-C

Found while generating tasks, after R1 to R12 were written. Superseded by R14,
which moves the check out of `CodeFor`.

**Decision**: `CodeFor` returns `ExitInterrupt` (130) for any error whose chain
holds `context.Canceled`. The check sits right after the `Interrupted` check and
before the `TaskFailure` check.

**Rationale**: `runBody` (`internal/cli/run.go:473`) wraps every error of a
`[no-exit-message]` task in `&TaskFailure{Err: err, Silent: true}`.
`classifyRunErr` passes an existing `TaskFailure` through unchanged, before its
`context.Canceled` check, so a Ctrl-C'd `[no-exit-message]` task is classified
as a task failure and exits 1. This is true today for shell bodies, and for
interpreter bodies once R4 lands. The spec's "User interrupt" edge case and
FR-005 require 130. The CLI and the MCP adapter both get their code from
`CodeFor`, so one check fixes both. `||` hooks are already skipped, because the
scheduler's `errors.Is(bodyErr, context.Canceled)` sees through the wrapper.
The banner stays suppressed, as `[no-exit-message]` asks.

**Alternatives considered**: skipping the silent wrap in `runBody` for a
cancelled context, so `classifyRunErr` returns `Interrupted`. Rejected: an
`Interrupted` error is not silent, so `BannerMessage` would start printing
`rune: context canceled` for a task that asked for no banner. That changes
banner output, which FR-011 forbids.

## R14. Code review fixes: interrupts, silent failures, and the hook's code

Found by code review after implementation.

**Problems**:

- The bundled shell returns the context error only when the child dies from the
  signal. A child that traps SIGINT and exits with its own code (common for test
  runners) made rune exit with that code, not 130. On Windows mvdan kills the
  child outright, which reads as exit 1. The agent executor had the same
  `CommandContext` gap R4 fixed for interpreter bodies, so an interrupted agent
  task exited 1 with an authentication message.
- `[no-exit-message]` wraps every error of the task in a silent `TaskFailure`,
  and `classifyRunErr` passed it through unclassified. An `agent` task with no
  `agent_cmd` exited 1 with the attribute and 2 without it (FR-008).
- `runFailHooks` computed `RUNE_FAILED_EXIT_CODE` with `BodyExitCode`, which says
  1 for errors the CLI maps to 2 or 3: a run-time validation error or an `agent`
  task with no `agent_cmd`. The hook saw 1 while rune exited 3 or 2 (FR-009).

**Decision**:

- `classifyRunErr` returns `Interrupted` whenever the run context was cancelled,
  wrapping the original error. The wrapper keeps the banner text, and
  `BannerMessage` still finds a silent `TaskFailure` in the chain, so a
  `[no-exit-message]` task stays quiet. This replaces R13's check in `CodeFor`.
- The classification moves into `classify(err)`, which prints nothing.
  `classifyRunErr` keeps the diagnostic and cycle-banner output and calls it. A
  silent `TaskFailure` is classified by the error it wraps and stays silent.
- The scheduler's `Engine` gains `ExitCode(err) int`. The CLI engine implements
  it as `CodeFor(classify(err))`, and `runFailHooks` uses it for
  `Failure.ExitCode`, so the hook's code and the exit code come from one mapping.
  `BodyExitCode` moves to `internal/cli` as `bodyExitCode`, its only caller.
- `agent.CLIProvider.Run` returns the context error when the context is done,
  like `interp.Run`.

**Alternatives considered**: checking the context in each executor only. That
cannot cover a child that traps the signal and exits normally, and it leaves
three places to keep in step. Skipping `||` hooks for usage and validation errors
raised before the body runs would also make FR-009 hold, but it changes failure
hook behavior from spec 022, which is out of scope.
