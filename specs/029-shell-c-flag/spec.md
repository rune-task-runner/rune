# Feature Specification: Honor the `-c` Flag in `set shell`

**Feature Branch**: `029-shell-c-flag`

**Created**: 2026-11-02

**Status**: Draft

**Input**: GitHub issue [#34](https://github.com/rune-task-runner/rune/issues/34):
"`set shell` ending in -c (as in the docs example) silently ignores failing lines"

## Overview

With a `set shell` override, rune writes the task body to a temporary file and runs
the configured shell with that file's path appended. When the list contains a
command-string flag such as `-c`, the shell treats the path as the command to run,
not as a script to read. bash then runs the file as a separate script, so the flags
in the list (`-e`, `-u`, `-o pipefail`) never reach the body. Failing lines are
ignored and the task exits 0.

`-c` is common in practice. `just` users write it out of habit, and rune's own docs
example (`set shell := ["bash", "-cu"]`) contains it.

Repro on v0.6.0:

```rune
set shell := ["bash", "-euo", "pipefail", "-c"]

# Should stop at false.
t:
    @echo one
    @false
    @echo two
```

```console
$ rune t; echo "rc=$?"
one
two
rc=0
```

The same body stops at `false` and exits non-zero under the built-in shell, and
under `["bash", "-euo", "pipefail"]` (no `-c`).

This feature makes rune honor a command-string flag: the body runs under the flags
the list declares, so the repro stops at `false`. It also fixes the docs examples and
documents how a `set shell` body runs differently from a built-in shell body.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Shell flags apply to the body when the list has `-c` (Priority: P1)

A developer sets `set shell := ["bash", "-euo", "pipefail", "-c"]` to get strict bash
for every task. A line in a task body fails. Rune stops the task at that line and
exits non-zero, as it already does when the list has no `-c`.

**Why this priority**: This is the defect the issue reports. Today the task reports
success while a line failed, so a CI job passes on a broken step.

**Independent Test**: Run the issue's repro and check that only `one` is printed and
rune exits with a non-zero code.

**Acceptance Scenarios**:

1. **Given** the repro Runefile above, **When** the user runs `rune t`, **Then**
   stdout shows `one` only, `echo two` never runs, rune exits 1 (the status of
   `false`), and the failure banner names task `t`.
2. **Given** `set shell := ["bash", "-cu"]` and a body that reads an unset variable,
   **When** the user runs the task, **Then** the task fails with the shell's
   unset-variable error and rune exits non-zero.
3. **Given** `set shell := ["bash", "-euc"]` (the flag inside a cluster) and a body
   whose second line is `(exit 3)`, a command that fails with status 3 without
   ending the shell, **When** the user runs the task, **Then** later lines do not
   run and rune exits 3.
4. **Given** `set shell := ["bash", "-eo", "pipefail", "-c"]` and a body line
   `false | true`, **When** the user runs the task, **Then** the task fails because
   the pipeline fails.
5. **Given** a task with `[script("bash -euc")]` whose second line fails, **When**
   the user runs it, **Then** the task stops at that line and rune exits non-zero,
   the same rule as for `set shell`.
6. **Given** the repro task, **When** an AI agent calls it as an MCP tool, **Then**
   the result is marked as an error and its exit marker reads `[exit 1]`.
7. **Given** a `-c` shell list and a body that reads standard input, **When** the
   user pipes input into `rune <task>`, **Then** the body receives that input, as it
   does with the same list without `-c`.

---

### User Story 2 - Adding or removing `-c` does not change what a task does (Priority: P1)

A developer porting a Runefile from `just` keeps `-c` in the shell list. A teammate
writes the same list without it. Both get the same result for the same body.

**Why this priority**: Users need one rule they can rely on: a list with the flag
behaves like the same list without it. Without that rule, the fix in Story 1 would
trade one surprise for another.

**Independent Test**: For a set of shell lists and bodies, run each body under the
list with and without the command-string flag and compare stdout and exit status.

**Acceptance Scenarios**:

1. **Given** a body and a shell list L that names a known shell and gives it no
   code of its own, **When** the body runs under L and under L with `-c` added
   after the shell's name, **Then** both runs print the same stdout and end with
   the same exit status.
2. **Given** `set shell := ["bash", "-cu"]` (the current docs example) and a body
   whose middle line fails and whose last line succeeds, **When** the user runs the
   task, **Then** it behaves as `["bash", "-u"]` does today: all lines run, because
   the list has no exit-on-error flag, and rune exits 0. Story 3 documents this.
3. **Given** a Runefile with no `set shell`, **When** any task runs, **Then** its
   behavior is unchanged from today.
4. **Given** a `set shell` list with no command-string flag, including a wrapper's
   `-c` (`["nix", "develop", "-c", "bash", "-eu"]`) or a list that gives the shell
   its own code (`["bash", "-c", "exec bash -eu $0"]`), **When** any task runs,
   **Then** its behavior is unchanged from today.
5. **Given** a `-c` shell list and a body whose first line sets a shell variable and
   changes directory, **When** later lines read the variable and print the current
   directory, **Then** they see the value and directory set on the first line, as
   they do without `-c`.

---

### User Story 3 - The docs show a working shell override and explain how it runs (Priority: P2)

A user copies the `set shell` example from the Runefile reference. Failing lines stop
their tasks. The same page explains that a `set shell` body runs as one script, so
stopping on a failing line depends on the shell's own exit-on-error flag, lines are
not echoed, and the `@` and `-` line prefixes have no effect.

**Why this priority**: The current example, `["bash", "-cu"]`, has no `-e`. Even with
Story 1 fixed, a copied example would still let a failing middle line pass. It is P2
because Stories 1 and 2 fix the behavior; this story makes the documented example
match it.

**Independent Test**: Run each `set shell` example from the docs against a body with
a failing middle line in the documentation test harness and check that the task
stops at that line. Read the `set shell` section and check it states each
difference listed in FR-009.

**Acceptance Scenarios**:

1. **Given** the Runefile reference, **When** the user reads the settings example,
   **Then** its `set shell` value includes the shell's exit-on-error flag (for
   example `["bash", "-euo", "pipefail"]`), and a task run under it stops at a
   failing line.
2. **Given** the settings how-to, **When** the user reads its `set shell` example,
   **Then** the same holds.
3. **Given** the `set shell` section of the Runefile reference, **When** a user reads
   it, **Then** it states that the body runs as one script in one shell process,
   that a failing line stops the task only when the shell's exit-on-error flag is
   set, that lines are not echoed, that the `@` and `-` prefixes have no effect (so
   a `-` line that fails under `-e` stops the task), and that a shell's `-c` with
   no code after it is accepted and the list's other flags apply to the body.
4. **Given** the release notes for the version that ships this change, **When** a
   user reads them, **Then** they state that shell lists containing `-c` now apply
   their flags to the body, so a task that used to pass while a line failed can now
   fail.

---

### Edge Cases

- **Flag position and form.** The command-string flag may appear anywhere after the
  shell's name, alone (`-c`) or inside a cluster of single-letter flags (`-cu`,
  `-euc`). Every form is treated the same.
- **Long options.** Elements that start with `--` (such as `--norc`) are never
  command-string flags, even when they contain the letter `c`.
- **Option values.** A value that follows an option, such as `pipefail` after `-o`,
  does not start with `-` and is not a flag.
- **Wrappers.** A `-c` before the shell's name belongs to the wrapper, as in
  `["nix", "develop", "-c", "bash", "-eu"]`, and is not touched. Such a list keeps
  working as it does today.
- **Own command string.** In a list such as `["bash", "-c", "exec bash -eu $0"]`
  the shell already has its code, and the script path becomes `$0`. Rune leaves
  the list unchanged.
- **Other programs.** Programs that are not known shells use `-c` for other
  things: `php -c <ini>`, `jq -c`, and `node -c` (syntax check). Their lists are
  left unchanged, including in `[script("...")]`.
- **List without exit-on-error.** The body runs as one script, so without `-e` the
  shell continues past a failing line and the task's status is the last command's.
  This matches the same list without `-c` today. It is documented, not changed.
- **Continue-on-error prefix.** A `-` prefix has no effect under `set shell`. With
  `-e`, a failing `-` line stops the task. This matches lists without `-c` today and
  is documented.
- **Script name.** A body that prints `$0`, and the shell's own error messages, show
  the same script name with `-c` as without it.
- **Large bodies.** The body reaches the shell the same way as today, so there is no
  new size limit. If handing it over fails, rune fails the task with an error that
  says so, as today. It never runs a partial body and never falls back to running
  the body without the list's flags.
- **Failure hooks.** `||` hooks run for a body that fails under the honored flags,
  and `RUNE_FAILED_EXIT_CODE` is the failing command's status.
- **Interrupt.** Ctrl-C makes rune exit 130, as today.
- **Missing shell.** A shell that is not on `PATH` produces today's
  missing-interpreter error.
- **Other flag syntax.** Lists that use a different syntax for the command string,
  such as `cmd /C` or PowerShell's `-Command` (or its short form `-c`), are not
  covered, because those programs are not known shells. Their behavior is
  unchanged.
- **Other executors.** `set python`, `set node`, and custom executors are not
  covered. Their behavior is unchanged.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: An element of a shell list is a command-string flag when all of these
  hold: (1) an earlier element names a known shell, meaning its base name, ignoring
  case and any `.exe` suffix, is `sh`, `bash`, `dash`, `zsh`, `ksh`, `mksh`, `ash`,
  or `fish`; (2) the element is `-c`, or starts with a single `-` followed only by
  letters that include `c` (for example `-cu` or `-euc`); (3) no later element is
  an operand, meaning an element that does not start with `-` or `+` and is not
  the value that follows an option cluster containing `o` or `O` (as `pipefail`
  follows `-o` or `-euo`); (4) no element after the shell is a single-dash
  cluster of letters that includes `s`, since without `-c` that flag makes the
  shell read its commands from stdin. Elements that start with `--` MUST NOT
  count. A list with no known shell, whose shell is given its code in the list,
  or with such an `s` flag MUST NOT be changed.
- **FR-002**: When the `set shell` list contains a command-string flag, rune MUST run
  the task body so that every other flag in the list applies to every line of the
  body.
- **FR-003**: For any body and any shell list L that names a known shell and gives
  it no code of its own, running the body under L with `-c` added after the shell's
  name MUST produce the same stdout, exit status, side effects, and script name
  (`$0`) as running it under L.
- **FR-004**: The command in a `[script("...")]` attribute MUST follow the same rule
  as a `set shell` list.
- **FR-005**: Everything else about how the body runs MUST stay as it is for a list
  without the flag: interpolation, environment (including `set export`, `[env]`, and
  `.env`), working directory, standard input, output streaming, secret masking,
  cancellation, exit-code propagation, `||` failure hooks with
  `RUNE_FAILED_EXIT_CODE`, and the missing-interpreter error.
- **FR-006**: The body MUST still run as one script in one shell process, so shell
  state (variables, current directory) carries across lines as it does today.
- **FR-007**: The body MUST reach the shell in full, with the list's flags, or the
  task MUST fail with an error that states the reason. Rune MUST NOT run a partial
  body or run the body without the list's flags.
- **FR-008**: Lists without a command-string flag, Runefiles without `set shell`,
  and the python, node, and custom executors MUST behave exactly as today.
- **FR-009**: Every `set shell` example in the documentation MUST include the
  shell's exit-on-error flag. The Runefile reference MUST state that a `set shell`
  body runs as one script, that stopping on a failing line depends on the shell's
  exit-on-error flag, that lines are not echoed, that the `@` and `-` prefixes have
  no effect, and that a shell's `-c` with no code after it is accepted.
- **FR-010**: The release notes for the version that ships this change MUST state
  that shell lists containing `-c` now apply their flags to the body, and that a
  task which passed while a line failed can now fail.

### Key Entities

- **Shell list**: the program and arguments given to `set shell`, or the
  whitespace-separated command in `[script("...")]`, used to run a task body.
- **Command-string flag**: an element of a shell list, defined in FR-001, that tells
  the shell its next non-option argument is the code to run rather than a file to
  read.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The issue's repro prints `one` only and rune exits 1, on Linux and
  macOS.
- **SC-002**: Across a test matrix of shell lists (`-c`, `-cu`, `-euc`,
  `-euo pipefail -c`, and `-c` placed before other flags) and bodies (all lines
  succeed, a middle line fails, an unset variable is read, a pipeline fails), runs
  with and without the command-string flag match on stdout and exit status in every
  case.
- **SC-003**: The existing test suite passes without changes to tests that do not
  use a command-string flag, and the compatibility corpus needs no edits.
- **SC-004**: Every `set shell` example in the documentation, run through the
  documentation test harness with a failing middle line, stops at that line.

## Assumptions

- **Approach chosen.** Rune honors the flag. The issue also offered an analyzer
  warning or error for `-c`. A warning would leave the silent failure in place for
  every run until the file is edited. An error would break the docs example and the
  compatibility corpus. The user chose to honor the flag on 2026-11-02. On
  2026-11-03, after `/speckit-analyze`, the rule was narrowed to a known shell's
  `-c` with no code after it, so wrapper lists, lists with their own code, and
  non-shell programs keep working as they do today.
- **Bug fix, not a DSL change.** The grammar does not change and there is no
  opt-in. The Runefile already declares the flags; rune starts applying them. A task
  that newly fails does so loudly, with a banner and a non-zero exit, so there is no
  silent meaning change. The release notes call it out (FR-010), and the plan's
  Constitution Check records the reasoning.
- **Single-script model kept.** `just` runs each line as a separate shell call. Rune
  keeps running the whole body as one script, because switching would stop shell
  state from carrying across lines. Per-line runs are out of scope.
- **No new diagnostic.** Rune does not warn about a shell list without `-e`. The docs
  explain the effect (FR-009). A warning can be proposed separately.
- **Only a shell's own `-c` counts.** Whether an element is a command-string flag
  is decided from the list's text (FR-001). Other programs use `-c` for other
  things (`php -c <ini>`, `jq -c`, `node -c`, `nix develop -c`, PowerShell's `-c`),
  so a `-c` counts only after a known shell's name. A shell missing from the
  FR-001 list keeps today's behavior.
- **Test environment.** Tests that need bash run only where bash is on `PATH`, in
  line with Principle V (no Git-Bash or WSL workarounds).
