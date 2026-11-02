# Feature Specification: Propagate the Task Body's Exit Code

**Feature Branch**: `028-propagate-task-exit-code`

**Created**: 2026-11-01

**Status**: Draft

**Input**: GitHub issue [#33](https://github.com/rune-task-runner/rune/issues/33):
"Task body exit code is lost: rune always exits 1, MCP reports [exit 1]"

## Overview

When a task body fails, rune exits 1 no matter what code the body exited with, and
the MCP tool result ends in `[exit 1]`. A task that exits 2 to mean "refused" looks
the same as one that exits 1 to mean "failed". Scripts and CI jobs that branch on
exit codes cannot tell them apart without parsing rune's error text.

Repro on v0.6.0:

```rune
# Exit with a specific code.
fail:
    @exit 2
```

```console
$ rune fail; echo "rc=$?"
rune: task "fail" failed at "exit 2": exit status 2
rc=1
```

The failure banner already names the real status, and `||` failure hooks already
receive it as `RUNE_FAILED_EXIT_CODE`. Only the process exit code and the MCP result
discard it.

This feature makes rune exit with the failing body's own code, the way `just` does,
and makes the MCP result report that same code. Rune's own error classes (usage,
validation, interrupt) keep their dedicated codes.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A script branches on the task's own exit code (Priority: P1)

A developer writes a task whose body exits with a meaningful code: 0 for success, 1
for failure, 2 for "refused" (for example, a release gate that declines to proceed).
A CI job runs `rune <task>` and branches on the exit code. Today every failure
arrives as 1. After this change the job sees the body's own code.

**Why this priority**: This is the defect the issue reports. Without it the other
stories have nothing to report.

**Independent Test**: Run a one-line task that exits with a chosen code N and check
that rune's exit status is N. Repeat for several values of N.

**Acceptance Scenarios**:

1. **Given** a task whose shell body runs `exit 2`, **When** the user runs
   `rune <task>`, **Then** rune exits 2 and the failure banner still names the task,
   the failing line, and `exit status 2`.
2. **Given** a task whose shell body runs `exit 1`, **When** the user runs it,
   **Then** rune exits 1, unchanged from today.
3. **Given** a task with an interpreter body (for example Python) that exits 4,
   **When** the user runs it, **Then** rune exits 4.
4. **Given** a task whose body has several lines and the second line exits 5,
   **When** the user runs it, **Then** rune stops at the second line and exits 5.
5. **Given** a task with `[no-exit-message]` whose body exits 6, **When** the user
   runs it, **Then** the banner is suppressed and rune still exits 6.

---

### User Story 2 - An agent sees the same exit code over MCP (Priority: P1)

An AI agent calls a task as an MCP tool. The tool result is marked as an error and
ends with `[exit N]`, where N is the code the body exited with, the same code the
CLI would exit with for the same task and arguments.

**Why this priority**: MCP is a first-class surface (Principle VII). An agent that
reads `[exit 1]` for a refusal will retry or "fix" something that was a deliberate
decline.

**Independent Test**: Call the task from Story 1 through the MCP server and check
that the result is marked as an error and its exit marker reads `[exit 2]`.

**Acceptance Scenarios**:

1. **Given** a task whose body exits 2, **When** an agent calls it as a tool,
   **Then** the result is marked as an error and its text ends with `[exit 2]`
   (before any fix-suggestion section).
2. **Given** a task that succeeds, **When** an agent calls it, **Then** the result
   is not marked as an error and reads `[exit 0]`, unchanged from today.
3. **Given** a task whose call fails with a rune error while the task runs (for
   example an expression in the body that fails to evaluate), not a body failure,
   **When** an agent calls it, **Then** the result reports the same dedicated code
   the CLI would exit with, unchanged from today.
4. **Given** a task with a `||` failure hook whose body exits 3, **When** an agent
   calls it, **Then** the exit marker reads `[exit 3]` and the fix-suggestion
   section follows it as today.

---

### User Story 3 - A failing dependency's code reaches the caller (Priority: P2)

A developer runs a task that depends on others. A dependency's body fails with code
N, so the requested task never runs. Rune exits N, the code of the body whose
failure stopped the run, the same failure the banner names.

**Why this priority**: The issue asks for propagation "at least for the task named
on the command line". Dependencies run as part of the same invocation, and the
banner already names whichever body failed. Reporting that body's code keeps the
code and the message consistent. It is P2 because the root-task case alone already
fixes the reported repro.

**Independent Test**: Define `a: b` where `b` exits 7. Run `rune a` and check that
rune exits 7 and the banner names `b`.

**Acceptance Scenarios**:

1. **Given** task `a` depends on task `b` and `b`'s body exits 7, **When** the user
   runs `rune a`, **Then** rune exits 7, `a`'s body never runs, and the banner names
   `b`.
2. **Given** a `[parallel]` task whose dependencies fail with different codes,
   **When** the user runs it, **Then** rune exits with the code of the failure the
   banner reports.
3. **Given** the user runs `rune x y` and `x` succeeds while `y`'s body exits 9,
   **When** the run stops, **Then** rune exits 9.

---

### User Story 4 - Documentation and release notes state the new rule (Priority: P3)

A user reading the CLI reference, the troubleshooting guide, or the MCP guide learns
that a body failure exits with the body's own code, which codes rune reserves for
its own errors, and that a body may exit with a number that overlaps a reserved one.
The release notes flag the change as breaking for scripts that test for exactly 1.

**Why this priority**: The behavior change is useless if the documented exit-code
table still says "1 = a task body failed". It is P3 because it describes the
behavior delivered by Stories 1 to 3.

**Independent Test**: Read the exit-code tables in the CLI reference and the
troubleshooting guide and the exit-marker text in the MCP guide. Each states the
propagation rule and the overlap note.

**Acceptance Scenarios**:

1. **Given** the CLI reference, **When** a user reads the exit-code table, **Then**
   it says a body failure exits with the body's own code (1 when the failure has no
   exit status) and lists 2, 3, and 130 as rune's own codes.
2. **Given** the troubleshooting guide, **When** a user reads "A task body failed",
   **Then** the example shows rune exiting with the body's code, not 1.
3. **Given** the release notes for the version that ships this change, **When** a
   user reads them, **Then** the change is marked breaking and names the affected
   pattern (scripts that test for exit code 1 specifically).

---

### Edge Cases

- **Body code overlaps a rune code.** A body that exits 2, 3, or 130 makes rune exit
  2, 3, or 130, the same numbers rune uses for usage, validation, and interrupt
  errors. Rune passes the body's code through unchanged; it does not remap it. The
  stderr banner tells the two cases apart, and the documentation says so. Remapping
  would break the issue's own example, which uses exit 2 to mean "refused".
- **Failure with no exit status.** Some failures have no body exit code: a declined
  `[confirm]` prompt, `[confirm]` with no terminal, a missing interpreter, an
  agent-executor failure (not installed, not authenticated, or a failed agent run),
  or a shell error recorded without a status. These keep exit 1.
- **Body killed by a signal.** When the platform reports that a body was terminated
  by signal S, rune exits 128 + S, the shell convention. When the platform reports
  no usable code, rune exits 1.
- **User interrupt.** Ctrl-C (SIGINT) still makes rune exit 130, whatever the body's
  own status was when it was cancelled. This holds for interpreter bodies and for
  tasks marked `[no-exit-message]` too.
- **Continue-on-error lines.** A body line prefixed with `-` that fails does not stop
  the task and does not affect the exit code.
- **Failure hooks.** `||` hooks run as today. Their outcome never changes the exit
  code, so rune exits with the original body's code even when a hook fails.
  `RUNE_FAILED_EXIT_CODE` and rune's exit code are now always equal.
- **Large codes on Windows.** Windows allows exit codes above 255. Interpreter
  bodies pass the full code through. Shell bodies report 0 to 255 on every OS,
  because the bundled shell stores exit statuses as 8-bit values; this predates
  the feature and is out of scope. On Linux and macOS all body codes are already
  0 to 255.
- **Dry-run, list, and cache hits.** No body runs, so there is no body code. These
  paths are unchanged.
- **Watch mode.** Watch mode reports each failure and keeps watching. It does not
  exit on a task failure, so this change does not affect it.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When a task body fails with exit code N (N ≠ 0), rune MUST exit with
  N. This applies to shell bodies and interpreter bodies.
- **FR-002**: When the failing body is a dependency or a later command-line task, not
  the first requested task, rune MUST exit with the code of the body whose failure
  stopped the run. That is the same failure the error banner names.
- **FR-003**: When several bodies fail concurrently under `[parallel]`, rune MUST
  exit with the code of the failure it reports in the banner.
- **FR-004**: Rune MUST NOT remap a body's exit code, including codes that equal one
  of rune's own codes (2, 3, 130).
- **FR-005**: Rune's own error classes MUST keep their codes: 0 for success, 2 for
  usage errors, 3 for validation errors, 130 for user interrupt.
- **FR-006**: A task failure that has no body exit status MUST exit 1. This covers a
  declined or non-interactive `[confirm]`, a missing interpreter, agent-executor
  failures, and a shell error recorded without a status.
- **FR-007**: When the platform reports that a body was terminated by signal S, rune
  MUST exit 128 + S. When no usable code is reported, rune MUST exit 1.
- **FR-008**: `[no-exit-message]` MUST suppress only the banner. The exit code MUST
  still be the body's code.
- **FR-009**: `||` failure-hook outcomes MUST NOT change the exit code. The value
  exposed to hooks as `RUNE_FAILED_EXIT_CODE` MUST equal the code rune exits with.
- **FR-010**: The MCP tool result for a task call MUST report, in its `[exit N]`
  marker, the same code the CLI would exit with for the same task and arguments.
  The result MUST be marked as an error for any non-zero code. A call the MCP
  server rejects before the task runs (unknown or unauthorized task, invalid
  arguments) keeps today's error result with no `[exit N]` marker.
- **FR-011**: The failure banner text MUST stay as it is today. It already names the
  task, the failing line, and the exit status.
- **FR-012**: The CLI reference, the troubleshooting guide, and the MCP guide MUST
  describe the propagation rule, list rune's reserved codes, and note that a body
  code can overlap a reserved code.
- **FR-013**: The release notes for the version that ships this change MUST mark it
  as breaking and name the affected pattern: scripts that test for exit code 1
  specifically.

### Key Entities

- **Body exit code**: the status a task body's process reported when it failed. It
  exists only for shell and interpreter bodies that ran and exited non-zero.
- **Rune error code**: a code rune assigns to its own failures (usage 2, validation
  3, interrupt 130), plus 1 for task failures that have no body exit code.
- **Reported failure**: the single failure that stopped a run. Its message is the
  banner. Its code is the exit code and the MCP `[exit N]` value.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For each body exit code in a test set covering 1, 2, 3, 42, 126, 127,
  130, and 255, `rune <task>` exits with that same code on Linux, macOS, and
  Windows. 8 of 8 match on each OS.
- **SC-002**: For every case in that set plus success, a usage error, and a
  validation error, each raised while the task runs, the MCP result's exit marker
  equals the CLI exit code for the same task and arguments. Every case matches.
  Calls rejected before the task runs carry no marker (FR-010).
- **SC-003**: Every existing test of usage (2), validation (3), and interrupt (130)
  exits passes unchanged.
- **SC-004**: A documented example script tells "refused" (exit 2) from "failed"
  (exit 1) with an exit-code check alone, no output parsing, and it runs green in
  the documentation test harness.
- **SC-005**: No documentation page still describes 1 as the only exit code for a
  task body failure.

## Assumptions

- **No opt-in.** The change is default behavior with no flag or per-file switch. It
  changes the CLI exit-code contract, not the Runefile language, so the
  constitution's per-file opt-in rule for breaking DSL changes does not apply. Rune
  is pre-1.0, and the previous breaking CLI fix (spec 027) shipped the same way: a
  breaking-change marker plus release notes.
- **The overlap is acceptable.** The issue states that rune's own errors "can keep
  their dedicated codes". A caller that needs to tell a body's exit 2 from a usage
  error reads the stderr banner, as with `just`.
- **Agent-executor tasks keep exit 1.** Their body is a prompt, and the exit status
  belongs to the third-party agent CLI, not to the Runefile author. Propagating
  their codes is out of scope.
- **Agent context hooks are unaffected.** They already treat any non-zero result as
  a failure.
- **Banner and failure-hook context exist already.** The banner and
  `RUNE_FAILED_EXIT_CODE` already carry the real body code (spec 022). Only the
  process exit code and the MCP result change.
- **Shell signal convention.** The shell executor already reports a signal-killed
  command as 128 + S. Interpreter bodies follow the same convention where the
  platform exposes the signal.
