# Feature Specification: Interactive-Only Approval for `[confirm]` Tasks

**Feature Branch**: `027-confirm-tty-gate`

**Created**: 2026-10-28

**Status**: Draft

**Input**: GitHub issue [#30](https://github.com/rune-task-runner/rune/issues/30):
"`[confirm]` accepts piped stdin, so `echo y | rune <task>` approves a
destructive task"

## Overview

`[confirm]` exists to stop a task from running unless someone approves it.
Today the CLI reads the answer from standard input whenever standard input
exists, without checking that a person is behind it. Any caller with a shell
(a script, a CI job, an AI agent) can therefore approve a destructive task by
piping `y`:

```rune
[confirm("Apply to live infrastructure?")]
apply:
    @echo would-apply
```

```console
$ rune apply </dev/null; echo "rc=$?"
Apply to live infrastructure? [y/N] rc=1            # declined, good
$ echo y | rune apply; echo "rc=$?"
Apply to live infrastructure? [y/N] would-apply
rc=0                                                # body ran
```

The MCP server already refuses `[confirm]` tasks unless it was started with
`--yes`, but the CLI has no equivalent, so `[confirm]` cannot act as a guard for
an agent that has a shell.

This feature makes an interactive answer count only when a person can give it:
standard input must be a terminal. With any other standard input and no
`--yes`, the task is declined and the caller is told how to approve it
deliberately. `--yes` remains the single, explicit, auditable way to approve
non-interactively.

## Clarifications

### Session 2026-10-28

- Q: When a run without a terminal and without `--yes` includes a `[confirm]`
  task, should Rune refuse the whole run before any task starts, or let earlier
  tasks run and decline only when it reaches the `[confirm]` task? → A: Decline
  when reached (today's timing). Tasks that run before the gate run normally;
  there is no up-front check of the whole run.
- Q: When `--yes` approves a `[confirm]` task, should Rune print a one-line
  notice saying so, or stay silent as it does today? → A: Stay silent; `--yes`
  output is unchanged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Piped input cannot approve a destructive task (Priority: P1)

A script, CI job, or AI agent runs a `[confirm]` task and feeds standard input
that happens to contain (or deliberately contains) `y`. The task is declined,
its body never runs, and the run exits non-zero, exactly as if the operator had
answered "no".

**Why this priority**: This is the defect. Until it is fixed, `[confirm]` gives
a false sense of safety: the guard is bypassed by the most ordinary shell idiom.

**Independent Test**: Run a `[confirm]` task with standard input piped from
`echo y`, from `yes`, and from a file containing `y`, without `--yes`. In every
case the body's output is absent and the exit status is non-zero.

**Acceptance Scenarios**:

1. **Given** a task marked `[confirm("Apply to live infrastructure?")]`,
   **When** it is run as `echo y | rune apply`, **Then** the body does not run
   and the exit status is non-zero.
2. **Given** the same task, **When** it is run as `yes | rune apply` or
   `rune apply < answer.txt` where the file contains `yes`, **Then** the body
   does not run and the exit status is non-zero.
3. **Given** the same task, **When** it is run with standard input redirected
   from an empty source (`rune apply </dev/null`), **Then** it is declined as it
   is today.
4. **Given** a task `deploy: apply` whose dependency `apply` is marked
   `[confirm]`, **When** `echo y | rune deploy` is run, **Then** `apply`'s body
   does not run and neither does `deploy`'s.

---

### User Story 2 - A person at a terminal still approves interactively (Priority: P1)

An operator runs a `[confirm]` task from an interactive terminal. They see the
prompt, type `y`, and the task runs; typing anything else declines it. Nothing
about the interactive experience changes.

**Why this priority**: The fix must not break the reason `[confirm]` exists. A
change that blocks the person at the keyboard would be unshippable.

**Independent Test**: Run a `[confirm]` task attached to a real terminal,
answer `y`: the body runs. Repeat answering `n`, pressing Enter on an empty
line, and sending end-of-input: each declines.

**Acceptance Scenarios**:

1. **Given** a `[confirm]` task and standard input attached to a terminal,
   **When** the operator answers `y` or `yes` (any letter case), **Then** the
   body runs.
2. **Given** the same setup, **When** the operator answers `n`, an empty line,
   or ends input, **Then** the task is declined with the same outcome as today.
3. **Given** standard input is a terminal but standard output is redirected to
   a file (`rune apply > out.log`), **When** the operator answers `y`, **Then**
   the body runs; only standard input decides whether the run is interactive.

---

### User Story 3 - A declined non-interactive run explains how to approve (Priority: P2)

A CI author or an agent hits the new decline. Instead of a `[y/N]` prompt that
nobody could answer followed by a silent non-zero exit, they get one clear
message: which task needed confirmation, what it asked, why it could not be
asked here, and that `--yes` approves it deliberately.

**Why this priority**: Without guidance the new behavior looks like a mystery
failure, and callers who relied on piping `y` would not know the supported fix.
It is P2 because the safety outcome (Story 1) holds even without the message.

**Independent Test**: Run a `[confirm]` task with piped standard input and no
`--yes`; read the error stream and check it names the task, shows the prompt
text, says no interactive terminal is attached, and names `--yes`.

**Acceptance Scenarios**:

1. **Given** a `[confirm("Apply to live infrastructure?")]` task `apply`,
   **When** it is run non-interactively without `--yes`, **Then** the error
   stream contains a single message naming `apply`, quoting "Apply to live
   infrastructure?", stating that no interactive terminal is attached, and
   naming `--yes`.
2. **Given** a bare `[confirm]` task with no prompt text, **When** it is run
   non-interactively, **Then** the message uses the same default question the
   interactive prompt would have shown.
3. **Given** a non-interactive decline, **When** the error stream is
   inspected, **Then** no `[y/N]` prompt appears, since no answer is being
   awaited.
4. **Given** `--color=always` and `--color=never` runs of the same declined
   task, **When** the outputs are compared, **Then** they differ only in
   styling, following the existing CLI color conventions.

---

### User Story 4 - Documentation and release notes state the rule (Priority: P3)

A maintainer upgrading Rune, or a new user reading the docs, learns that
interactive approval requires a terminal, that piped answers never approve,
and that `--yes` is how unattended runs approve `[confirm]` tasks.

**Why this priority**: Scripts that piped `y` will start failing after upgrade.
The release notes and docs are how those owners find the one-line fix. The
runtime message (Story 3) already covers the moment of failure, so this is P3.

**Independent Test**: Read the CLI reference entry for `--yes`, the Runefile
guide entry for `[confirm]`, the editor hover text for `[confirm]`, the
`--yes` flag help, and the release notes for the version that ships this
change; each states the terminal requirement or the migration.

**Acceptance Scenarios**:

1. **Given** the CLI reference, **When** a reader looks up `--yes`, **Then** it
   says `--yes` is the way to approve `[confirm]` tasks when standard input is
   not a terminal.
2. **Given** the release notes for the shipping version, **When** a reader
   scans the breaking changes, **Then** this behavior change is listed with the
   migration "pass `--yes`".

---

### Edge Cases

- **`--yes` with piped input**: the task runs without prompting, and all of
  standard input is left untouched for the task body (the confirmation step
  reads nothing).
- **Non-interactive decline reads nothing**: when standard input is not a
  terminal, Rune does not consume any of it while deciding, so content meant
  for a later reader is not swallowed.
- **Standard input closed** (`rune apply <&-`): treated as non-interactive and
  declined. Having no stdin stream at all only happens inside the MCP server,
  which keeps today's silent decline (FR-010).
- **Tasks ahead of the gate**: in `release: build upload publish` with only
  `publish` marked `[confirm]`, a non-interactive run without `--yes` runs
  `build` and `upload`, then declines `publish` and exits non-zero. The timing
  matches the interactive prompt.
- **Several `[confirm]` tasks in one non-interactive run**: none of their
  bodies run; each one that is reached is declined under the same rule.
- **`[confirm]` task used as a `||` failure hook**: a non-interactive run
  declines the hook; it surfaces as today's one-line hook warning and does not
  change the outcome of the original failure.
- **Declined task with its own failure hooks**: the body never started, so its
  `||` failure hooks do not fire (unchanged from spec 022).
- **Plan modes**: `--dry-run` and `--summary` neither prompt nor print the
  decline message; they run nothing, as today.
- **Interactive task picker (`--choose`)**: it already requires a terminal, so a
  `[confirm]` task chosen there prompts interactively as before.
- **MCP server and the agent executor's callback endpoint**: unchanged. The
  server refuses `[confirm]` tasks unless started with `--yes`; the agent
  endpoint never exposes them.
- **Windows**: a person in the native console or in an MSYS/Cygwin-style
  terminal counts as interactive and can approve.
- **A program that drives a pseudo-terminal** (for example `script`, `expect`,
  or an agent harness that runs commands in a terminal): Rune cannot tell it
  apart from a person and treats it as interactive. See Assumptions.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Before running a `[confirm]` task without `--yes`, Rune MUST
  determine whether its standard input is an interactive terminal.
- **FR-002**: When standard input is an interactive terminal, Rune MUST show
  the confirmation prompt and approve only on `y` or `yes` (letter case and
  surrounding whitespace ignored). Any other answer, an empty line, or end of
  input MUST decline. This matches today's interactive behavior.
- **FR-003**: On the CLI, when standard input is not an interactive terminal
  (a pipe, a regular file, a null device, or a closed descriptor) and `--yes`
  is not given, Rune MUST decline the task regardless of what standard input
  contains, and MUST NOT read from standard input to reach that decision.
- **FR-004**: On the CLI, a non-interactive decline MUST NOT print the `[y/N]`
  prompt.
  Instead it MUST write one message to the error stream that names the task,
  shows the task's prompt text (or the default question used when none is
  given), states that no interactive terminal is attached, and names `--yes`
  as the way to approve.
- **FR-005**: A non-interactive decline MUST have the same outcome as an
  interactive decline: the body does not run, the exit status is the same
  non-zero status a declined confirmation produces today, the task's `||`
  failure hooks do not fire, and tasks depending on it do not run. The
  decision is made when the `[confirm]` task is reached, exactly as an
  interactive prompt is: tasks that run before it (including its own
  dependencies) run normally, and Rune performs no up-front check of the
  whole run.
- **FR-006**: `--yes` MUST continue to approve every `[confirm]` task in the
  invocation without prompting and without reading standard input, whether or
  not standard input is a terminal. Its output MUST stay as it is today: no
  approval notice is printed.
- **FR-007**: The rule MUST apply to every `[confirm]` task the CLI runs,
  whether it was requested by name, reached as a dependency, or run as a
  success or failure hook.
- **FR-008**: Terminal detection MUST work on every supported OS, including
  the Windows native console and MSYS/Cygwin-style terminals, so a person on
  any of them can still approve interactively.
- **FR-009**: Rune MUST NOT add any other non-interactive approval channel
  (environment variable, Runefile setting, or configuration file). `--yes`
  stays the only one, so every unattended approval is visible on the command
  line.
- **FR-010**: MCP server behavior and plan modes (`--dry-run`, `--summary`)
  MUST be unchanged.
- **FR-011**: The decline message MUST follow the existing CLI output
  conventions for a failed run: it is delivered as the standard `rune:`
  failure banner, honoring `--color` and `NO_COLOR`, with identical text when
  color is off.
- **FR-012**: The CLI reference entry for `--yes`, the Runefile guide entry for
  `[confirm]`, the editor hover text for `[confirm]`, and the `--yes` flag help
  text MUST state that interactive approval requires a terminal and that
  `--yes` approves non-interactively.
- **FR-013**: The release notes for the version that ships this change MUST
  list it as a breaking behavior change for callers that approved by piping an
  answer, with the migration: pass `--yes`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every non-terminal input form tested (piped `y`, piped `yes`
  in any case, an endless stream of `y`, a file containing `y`, empty input,
  closed input), the task body runs 0 times and the exit status is non-zero.
- **SC-002**: A person at a terminal on Linux, macOS, and Windows approves a
  `[confirm]` task by answering `y` and declines it with any other answer, with
  no change in prompt text or answer vocabulary from the previous release.
  Linux and macOS are verified by automated tests on a real terminal; Windows
  by automated tests of the decision logic plus the terminal detection the
  task picker already relies on, with a manual check recommended before
  release.
- **SC-003**: Every CLI run that ends in a non-interactive decline shows a
  message naming a declined task and `--yes`, so a caller can fix the run from
  that message alone.
- **SC-004**: With `--yes` and piped input, the task body receives the piped
  input in full: 0 bytes are consumed by the confirmation step.
- **SC-005**: MCP tool calls, `--dry-run`, and `--summary` produce the same
  results as before the change for `[confirm]` tasks.
- **SC-006**: Each surface named in FR-012 states the terminal requirement,
  and no user-facing documentation describes approving a `[confirm]` task by
  piped input.

## Assumptions

- **"A person can answer" means standard input is a terminal.** The state of
  standard output and standard error does not matter: an operator who
  redirects output to a log can still approve.
- **This is a guard, not a sandbox.** Checking for a terminal stops piped,
  redirected, and accidental approvals. A caller that deliberately allocates a
  pseudo-terminal and types into it is indistinguishable from a person and can
  still answer. Isolating an agent that has unrestricted shell access is out of
  scope; the server-side gate in the MCP path remains the hard boundary for
  agents.
- **No deprecation period.** The change ships immediately because it closes a
  safety hole. Rune is pre-1.0, so a behavior change with a release-note
  callout and a runtime message naming the fix is the accepted path.
- **No DSL change.** Runefiles are not affected; the constitution's per-file
  DSL compatibility rule does not apply. The release-note callout (FR-013)
  covers the affected callers, who are scripts and CI jobs.
- **Exit status and answer vocabulary are unchanged.** A declined confirmation
  keeps today's exit status, and `y`/`yes` remain the only approving answers.
- **Interactive approval needs a terminal in automated tests.** Verifying
  Story 2 requires driving Rune through a real or pseudo terminal; how that is
  done per OS is a planning decision.
