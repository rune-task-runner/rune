# Data Model: Post-Mortem Diagnostics (Failure Hooks)

**Feature**: 022-post-mortem-diagnostics · **Date**: 2026-08-25

Entities introduced or extended by this feature, their fields, validation
rules, and state transitions. References: [spec.md](spec.md),
[research.md](research.md).

## 1. Failure hook (AST)

A reference from a task to another task, run only on the declaring task's
body failure.

**Representation**: new field on the existing task node.

| Field | Type | Meaning |
|---|---|---|
| `Task.FailHooks` | `[]*DepCall` | Ordered `\|\|` targets; empty slice = no hooks (today's behavior) |

`DepCall` is reused unchanged: `{ Name string; Args []Expr; Sp token.Span }`.
The parenthesized form passes arguments exactly as for dependencies and
success hooks.

**Validation rules** (static, before any execution):

| Rule | Diagnostic | Source |
|---|---|---|
| Target task must exist | RUNE2001 | FR-010 |
| Argument count must match target's parameters | RUNE2005 | FR-010 |
| Hook argument expressions resolve against declaring task's params/vars | RUNE2004 | FR-010 |
| No cycle through `\|\|` edges (combined with dep/`&&` edges) | RUNE2003 | FR-010 |
| Target must not use the `agent` executor | **RUNE2011** (new) | FR-011 |
| Target participates in `[context]` closure restrictions when reachable from a `[context]` task | RUNE2007 | spec 021 parity |

**Relationships**: `Task 1 —— * DepCall (FailHooks)`; each `DepCall` resolves
to exactly one `Task` post-import (module-qualified names rewritten during
compose, same as `Deps`/`PostHooks`).

## 2. Failure (scheduler value object)

The facts about a body failure handed to each hook run.

| Field | Type | Meaning |
|---|---|---|
| `TaskName` | `string` | User-visible (namespaced) name of the failed task |
| `ExitCode` | `int` | Exit code of the failed body; from the shell executor's exit status when available, else `1` |

**Lifecycle**: created once per failing task body, immediately after the body
returns a qualifying error (any error not wrapping `context.Canceled`);
passed by value to every hook run for that failure; never stored.

**Environment projection** (contract for task authors, FR-009):

| Variable | Value |
|---|---|
| `RUNE_FAILED_TASK` | `Failure.TaskName` |
| `RUNE_FAILED_EXIT_CODE` | `strconv.Itoa(Failure.ExitCode)` |

Appended to the hook body's environment after all existing env sources
(process env, dotenv, exported vars, `[env(...)]` pairs) so they cannot be
shadowed by static declarations.

## 3. Hook run (runtime state transitions)

States of one failure-hook execution for one failing task:

```text
                      body succeeded ──────────────► (no hook runs at all)
body failed
  │  error wraps context.Canceled? ─── yes ────────► ABORTED (nothing runs)
  ▼ no
for each FailHook, in declaration order:
  RESOLVED ─ target OS-unavailable ─── yes ──► SKIPPED (one warning line)
  │ no
  ▼
  DEPS-RUN (hook's deps + its && hooks: normal memoized semantics)
  │ dep failed? ── yes ──► FAILED (one warning line; next hook continues)
  ▼ no
  BODY-RUN (memoization bypassed; failure env injected)
  │ body failed? ── yes ──► FAILED (one warning line; hook's own || IGNORED)
  ▼ no
  COMPLETED
finally: return the ORIGINAL body error unchanged (exit code preserved)
```

**Invariants**:

- The declaring task's original error is returned in every path (FR-004).
- A hook body run writes **no memo entry**; the same hook task fires once per
  failing task within one invocation (clarification Q2). Its dependency
  subtree memoizes normally.
- Hooks never chain: a hook's own `FailHooks` are ignored during a hook run
  (spec edge case); its `Deps` and `PostHooks` run normally.
- Cancellation (`context.Canceled`) at any point aborts everything, hooks
  included (clarification Q4).

## 4. Fix suggestion (MCP result extension)

The combined diagnostic payload delivered to an agent.

| Field | Type | Meaning |
|---|---|---|
| `mcpserver.Result.FixSuggestion` | `string` | Masked, trimmed, capped stdout of all hook runs for this call; `""` = no section rendered |

**Construction pipeline** (order is normative, FR-007):

1. Hook body stdout streams into a dedicated buffer wrapped in the secret-mask
   writer (masking happens as bytes arrive — before anything else).
2. After the run completes, flush the mask writer.
3. Trim trailing `\r\n`.
4. If longer than the 8 KiB cap: cut at the cap, back the cut up to a UTF-8
   rune boundary, append `\n[truncated]` (marker excluded from the cap) —
   byte-identical discipline to the `[context]` hook.

Hook **stderr** is not part of the fix suggestion; it joins the call's normal
captured stderr (and carries the warning lines).

**Rendering** (in the tool result text, after the `[exit N]` line, only when
non-empty):

```text
[fix suggestion - from Runefile || failure hooks]
<FixSuggestion>
```

## 5. Diagnostic code (public contract extension)

| Code | Name | Severity | Condition |
|---|---|---|---|
| `RUNE2011` | `CodeInvalidFailureHook` | error | A `\|\|` hook target resolves to a task with the `agent` executor |

Anchored at the offending `DepCall` span with the standard
`file:line:col` + caret rendering. Registered in `internal/diag/codes.go`,
`docs/diagnostics.md`, and `specs/011-rune-lsp/contracts/diagnostic-codes.md`
in the same change (frozen-contract rule).

## 6. Extended interfaces (summary)

| Interface | Addition | Purpose |
|---|---|---|
| `scheduler.Engine` | `ExecuteFailHook(task, params, Failure) error` | Run a hook body with failure env, bypassing memoization |
| `scheduler.Engine` | `Warnf(format, ...any)` | Scheduler-originated one-line warnings (skip / hook-failure) |
| `mcpserver.Result` | `FixSuggestion string` | Delivery of §4 |
| CLI engine options | optional fail-hook stdout sink (`io.Writer`) | MCP path routes hook stdout to the dedicated masked buffer; CLI path leaves it nil (stream to terminal) |
