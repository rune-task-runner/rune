# Data Model: Interactive-Only Approval for `[confirm]` Tasks

**Feature**: [spec.md](spec.md) | **Research**: [research.md](research.md)

Nothing is persisted. The feature adds one per-invocation fact and one
three-way decision.

## Entities

### Stdin classification (per invocation)

Computed once when the CLI builds its engine (research R2).

| Value | When | Where it occurs |
|---|---|---|
| `no-stream` | `Options.Stdin == nil` | MCP adapter, embedders; never on the CLI, where a closed descriptor is still `not-terminal` |
| `terminal` | `*os.File` and `isTTY(fd)` | person at a console, MSYS/Cygwin terminal, pseudo-terminal |
| `not-terminal` | anything else | pipe, regular file, null device, closed descriptor, non-file reader |

Stored as engine field `stdinTTY bool`; `no-stream` is recognized from
`opts.Stdin == nil` at decision time. The spec's FR-003 and FR-004 apply to
the CLI only; `no-stream` keeps today's silent decline so MCP output is
unchanged (FR-010).

### Confirmation decision (per `[confirm]` task reached)

Inputs: plan mode, `--yes`, stdin classification, interactive answer.

| Plan mode | `--yes` | Stdin | Answer | Outcome | Body | Output | Exit |
|---|---|---|---|---|---|---|---|
| dry-run / summary | any | any | none | not evaluated | no | unchanged plan lines | unchanged |
| run | yes | any | none read | **approved** | runs | unchanged (no notice) | body's |
| run | no | terminal | `y` / `yes` (any case, trimmed) | **approved** | runs | prompt | body's |
| run | no | terminal | anything else, empty, EOF | **declined** | no | prompt | 1, no banner |
| run | no | not-terminal | none read | **no terminal** | no | `rune:` banner (contract) | 1 |
| run | no | no-stream | none read | **declined** | no | none (unchanged) | 1 |

Rules that hold for every non-approved outcome:

- The error chain contains `scheduler.ErrBodyNotRun`, so the task's own `||`
  failure hooks do not fire (spec 022 FR-003).
- Tasks depending on it do not run; tasks that ran before it are unaffected
  (Clarification Q1).

### Failure-hook variant

When the `[confirm]` task is running as a `||` failure hook, the same table
applies, except the **no terminal** outcome is reported as the scheduler's
one-line `warning: failure hook <name> failed: <message>` instead of a banner,
and the original failure's exit code stands.

## State transitions

```text
reached ──plan≠run──────────────────────────► (plan output)
   │
   ├─ --yes ─────────────────────────────────► approved ─► body
   │
   ├─ no stdin stream (MCP) ─────────────────► declined (silent)
   │
   ├─ stdin not-terminal ────────────────────► no terminal (banner / hook warning)
   │
   └─ stdin terminal ─► prompt ─► "y"/"yes" ─► approved ─► body
                                 └ other ────► declined (silent)
```
