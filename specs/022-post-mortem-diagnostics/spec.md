# Feature Specification: Post-Mortem Diagnostics (Failure Hooks)

**Feature Branch**: `022-post-mortem-diagnostics`

**Created**: 2026-08-23

**Status**: Draft

**Input**: User description: "Post-Mortem Diagnostics (Task after): Implement
failure-specific hooks. If a test task fails, an after hook could automatically
run a diagnostic tool and provide the agent with a 'fix suggestion' instead of
just a raw error code."

## Overview

Rune tasks can already declare post-hooks (`&&`) that run after a task
succeeds. Nothing runs when a task **fails**: the caller — a human at the
terminal or an AI agent calling through MCP — receives the captured output and
a raw exit code, and must figure out what went wrong on their own.

This feature adds **failure hooks**: tasks that run automatically when the
main task fails. A failure hook is the natural place to run a diagnostic tool
(re-run the failing test verbosely, lint the offending files, summarize the
log, print the last migration applied). Its output is delivered back to the
caller alongside the original failure — for an agent, as a clearly labeled
**fix suggestion** section in the same tool response, so the agent can act
immediately instead of spending extra round trips rediscovering context Rune
already had.

The syntax mirrors the existing success-hook clause: success hooks follow
`&&`, failure hooks follow `||`.

```rune
test: build && notify || diagnose
    go test ./...

diagnose:
    @echo "Re-running failed packages verbosely…"
    go test -v ./... 2>&1 | tail -40
```

## Clarifications

### Session 2026-08-25

- Q: Should failure hooks be declared with a `||` clause mirroring the
  existing `&&` success-hook clause, or with a task attribute? (FR-001) →
  A: `||` clause — single syntax, mirrors `&&` including the parenthesized
  argument-passing form.
- Q: When several tasks fail in one invocation and share the same
  failure-hook task, should the hook run once per failing task or at most
  once per invocation? (FR-012) → A: Once per failure — failure hooks are
  exempt from once-per-invocation memoization; each failing task fires its
  own hook run with that failure's context.
- Q: If a task's dependency fails (so the task's own body never runs),
  should the task's failure hooks fire, or only the failed dependency's own
  hooks? (FR-003) → A: Body-only — a hook fires only when the declaring
  task's own body fails; a failed dependency fires its own hooks.
- Q: Should failure hooks still run when the task fails because of a
  timeout, and when the user cancels the run with Ctrl-C? (FR-002) →
  A: Timeout yes, Ctrl-C no — a timed-out body counts as a failure and
  fires hooks; user cancellation aborts everything immediately, hooks
  included.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent gets a fix suggestion with the failure (Priority: P1)

An AI agent invokes the `test` task through Rune's MCP server. The tests fail.
Because the Runefile declares a failure hook, Rune runs the diagnostic task and
returns a single tool response containing the original test output, the exit
code, **and** a labeled fix-suggestion section holding the diagnostic output.
The agent reads the suggestion and fixes the bug without issuing additional
exploratory tool calls.

**Why this priority**: This is the core value of the feature and the reason it
exists — Rune is AI-native, and turning a raw non-zero exit into actionable
context is the single biggest lever for agent loop efficiency. It is
independently shippable: even with only the MCP surface, the feature delivers
its promise.

**Independent Test**: Can be fully tested by calling a failing task with a
declared failure hook through the MCP server and asserting the tool result
contains both the original failure output/exit code and the delimited
diagnostic section.

**Acceptance Scenarios**:

1. **Given** a task with a failure hook, **When** an agent runs it via MCP and
   the task body fails, **Then** the tool response contains the task's own
   output, its exit code, and a clearly delimited fix-suggestion section with
   the failure hook's output.
2. **Given** a task with a failure hook, **When** an agent runs it via MCP and
   the task succeeds, **Then** the failure hook does not run and the response
   contains no fix-suggestion section.
3. **Given** a task with both success hooks and a failure hook, **When** the
   task fails, **Then** only the failure hook runs — success hooks are
   skipped, exactly as today.

---

### User Story 2 - Terminal users see the diagnosis at the point of failure (Priority: P2)

A developer runs `rune test` in the terminal. The tests fail. Rune runs the
declared failure hook and streams its output after the failure, under a
visible header, before exiting with the **original** task's exit code. The
developer sees "what broke and where to look" in one screen instead of
re-running commands by hand.

**Why this priority**: The CLI is the second consumer of the same mechanism.
It requires no extra machinery beyond running the hook and printing its
output, and it keeps CLI/MCP behavior consistent — but the agent surface is
where the feature changes outcomes most.

**Independent Test**: Run a failing task with a failure hook at the CLI;
assert the hook's output appears after the failure, the header labels it as
diagnostics, and the process exit code equals the failing task's exit code
(not the hook's).

**Acceptance Scenarios**:

1. **Given** a failing task with a failure hook, **When** run from the CLI,
   **Then** the hook runs, its output is printed under a diagnostics header,
   and the process exits with the original failing task's exit code.
2. **Given** a failing task whose failure hook *also* fails, **When** run from
   the CLI, **Then** Rune reports the hook failure as a one-line warning and
   still exits with the original task's exit code.

---

### User Story 3 - Diagnostic tasks know what failed (Priority: P3)

A Runefile author writes one shared `diagnose` task and attaches it as the
failure hook of several tasks (`test`, `lint`, `build`). Inside the hook, the
author reads the failed task's name and exit code from the environment and
tailors the diagnostic accordingly (e.g., verbose re-run for `test`, config
dump for `build`).

**Why this priority**: Failure context makes one diagnostic task reusable
across a Runefile instead of forcing one hook per task. Valuable, but the
feature is useful without it (a per-task hook already knows its context).

**Independent Test**: Attach the same hook to two different tasks, fail each,
and assert the hook observes the correct failed-task name and exit code in
each run.

**Acceptance Scenarios**:

1. **Given** a failure hook attached to a failing task, **When** the hook
   runs, **Then** it can read the failed task's name and the failing exit code
   from its environment.
2. **Given** the same hook task attached to two different failing tasks,
   **When** each is run, **Then** the hook observes the matching task name and
   exit code for each invocation.

---

### Edge Cases

- **Failure hook itself fails or hangs**: the hook runs like any task —
  unbounded, cancellable together with the invocation; a hook failure is
  reported as a warning, never replaces or masks the original failure, and
  never changes the exit code.
- **Dependency fails before the task body runs**: the task's own failure hook
  does not fire — the failed dependency's failure hooks (if any) fire instead.
  A hook fires only for the failure of the task that declares it.
- **Failure hook of a failure hook**: hooks do not chain. If a failure hook
  task declares its own failure hooks, they are ignored when it runs *as* a
  hook (no recursive post-mortems).
- **Both `&&` and `||` on one task**: exactly one set runs — success hooks on
  success, failure hooks on failure.
- **Task killed by timeout vs user interrupt**: a timed-out body is a failure
  and fires its hooks; Ctrl-C (user cancellation) aborts the whole invocation
  immediately — no hooks run.
- **Multiple failure hooks**: run sequentially in declaration order, like
  success hooks; a failure in one is warned about and the rest still run.
- **Hook references an unknown task, forms a cycle, or takes wrong arity**:
  rejected by static analysis before anything runs, with the standard
  `file:line:col` + caret diagnostic — identical treatment to `&&` hooks.
- **Failure hook is OS-gated** (e.g. `[linux]` on a macOS host): the hook is
  skipped with a warning; the original failure is still reported normally.
- **Agent-executor task as failure hook**: rejected by analysis — a
  post-mortem must not recursively require an agent session (same rule as the
  `[context]` hook).
- **Huge diagnostic output on the agent surface**: the fix-suggestion section
  is truncated at a fixed cap with a visible truncation marker, so one noisy
  diagnostic cannot flood the agent's context. CLI output is not capped.
- **Secrets in diagnostic output**: masked by the existing secret-masking
  pipeline before display or injection, on both surfaces.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A task MUST be able to declare one or more failure hooks —
  tasks that run only when the declaring task's own body fails — using a
  `||` clause that mirrors the existing `&&` success-hook clause, including
  the parenthesized argument-passing form.
- **FR-002**: Failure hooks MUST NOT run when the task succeeds, and success
  hooks MUST NOT run when the task fails (existing behavior preserved). A
  body that fails by timing out counts as a failure and fires the hooks;
  user cancellation (interrupt) aborts the invocation immediately and fires
  nothing.
- **FR-003**: A failure hook MUST fire only for a failure of the task's own
  body. A dependency's failure triggers that dependency's failure hooks, not
  the dependent's.
- **FR-004**: After failure hooks complete, the invocation MUST exit with the
  original failing task's exit code. Failure-hook outcomes (success, failure,
  timeout, skip) MUST NOT alter it.
- **FR-005**: A failure hook that itself fails MUST produce a one-line warning
  and MUST NOT trigger further failure hooks (no chaining); remaining declared
  hooks still run in order.
- **FR-006**: When the failing task was invoked through the agent (MCP)
  surface, the failure hooks' captured standard output MUST be included in
  the same tool response as the original failure, in a clearly delimited
  fix-suggestion section, alongside — never instead of — the task's own
  output and exit code.
- **FR-007**: On the agent surface the fix-suggestion section MUST be capped
  at a fixed size with an explicit truncation marker; masking MUST be applied
  before truncation so a secret cannot leak through a truncated suffix.
- **FR-008**: When invoked from the CLI, failure-hook output MUST be printed
  after the failure under a visible diagnostics header, following existing
  styling conventions.
- **FR-009**: Failure hooks MUST run with the failed task's name and failing
  exit code available in their environment, so one diagnostic task can serve
  multiple tasks.
- **FR-010**: Static analysis MUST validate failure hooks exactly as it
  validates success hooks — unknown task, arity mismatch, and cycles through
  `||` edges are all reported with `file:line:col` + caret span before any
  execution.
- **FR-011**: Static analysis MUST reject an `agent`-executor task used as a
  failure hook.
- **FR-012**: Failure hooks MUST respect existing task semantics — OS
  availability gating (skipped with a warning when unavailable), secret
  masking of their output, and normal dependency resolution for their own
  dependencies — with one exception: a failure hook runs **once per failing
  task**, not once per invocation. When several tasks fail in one invocation
  and share a hook task, each failure fires its own hook run carrying that
  failure's context. (The hook's *dependencies* still memoize normally.)
- **FR-013**: Existing Runefiles without `||` clauses MUST behave exactly as
  before; the feature is purely additive.

### Key Entities

- **Failure hook**: a reference from a task to another task, declared to run
  only on the declaring task's failure; ordered, optionally parameterized,
  validated statically.
- **Failure context**: the facts about the failure made available to the hook
  — the failed task's name and its exit code.
- **Fix suggestion**: the combined, masked, size-capped standard output of
  the failure hooks as delivered to an agent in the same tool response as the
  failure.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An agent whose task fails receives the diagnostic output in the
  same tool response as the failure — zero additional tool calls are needed to
  obtain it.
- **SC-002**: The invocation's exit code equals the original failing task's
  exit code in 100% of failure-hook scenarios, including hook failure, hook
  timeout, and hook skip.
- **SC-003**: Runs of succeeding tasks are unaffected: no failure hook
  executes and no measurable overhead is added to the success path.
- **SC-004**: Every existing Runefile in the compatibility corpus parses and
  behaves identically after the feature ships (zero regressions).
- **SC-005**: A malformed failure-hook declaration (unknown task, cycle, wrong
  arity) is reported before any task runs, with a diagnostic pointing at the
  offending source span, in 100% of cases.

## Assumptions

- Grammar and docs (`docs/GRAMMAR.md`, hooks guide) ship in the same change
  as the `||` clause, per the constitution's surface-change rule. (Syntax
  choice confirmed in Clarifications, Session 2026-08-25.)
- Failure hooks fire on the **task body's** failure only. A failed dependency
  fires its own hooks. This keeps the rule local and predictable; a
  "fire on any downstream failure" mode is out of scope for this version.
- Hooks do not chain (a hook's own `||` hooks are ignored when running as a
  hook) to guarantee termination and avoid post-mortem recursion.
- Failure context is delivered through the hook's environment (failed task
  name, exit code), consistent with how Rune already passes ambient facts to
  tasks; the exact variable names are a planning decision.
- The agent-surface size cap reuses the precedent set by the `[context]` hook
  (fixed cap, masking before truncation, no configuration knob in v1).
- Retry/auto-remediation ("hook fixes the problem and re-runs the task") is
  explicitly out of scope — this feature diagnoses, it does not re-execute.
- LSP/editor awareness of the new clause follows automatically from the
  analyzer; no dedicated editor work is in scope beyond keeping existing
  tooling green.
