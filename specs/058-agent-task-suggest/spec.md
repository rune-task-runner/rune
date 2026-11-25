# Feature Specification: Learn Rune Tasks from an Agent's Shell Commands

**Feature Branch**: `058-agent-task-suggest`

**Created**: 2026-11-23

**Status**: Draft

**Input**: GitHub issue [#61](https://github.com/rune-task-runner/rune/issues/61)
(backlog ADOPT-5): "Learn reusable Rune tasks from observed agent shell commands".
Related: guided client setup (#53, spec 055 `rune agent init`), task import (#52, spec 054),
the audit log (#50, spec 052), the Rune-only agent profile (#46, spec 047).

## Overview

A project without a Runefile gets nothing from Rune until someone writes the task catalog.
Meanwhile a coding agent runs the same shell commands again and again: the test command,
the linter, a build with a flag. Those repetitions are the tasks the project needs.

This feature lets a person turn on a local recorder for the shell commands that Claude Code
runs, then ask Rune which commands are worth a task:

```text
$ rune agent observe claude
.claude/settings.local.json: created
observing: the shell commands Claude Code runs in this project are recorded on this
  machine, without their output, for up to 30 days. See them with: rune suggest

$ rune suggest
Recorded 212 commands in 4 sessions, 2026-11-02 to 2026-11-23.

[3f9a1c2e] the agent ran this 14 times (12 succeeded, 2 failed) in 3 sessions, last on 2026-11-23:
    go test -run TestX ./pkg/...
  suggested task:
    # Run the parser tests.
    go-test:
        go test -run TestX ./pkg/...

Apply with: rune suggest apply ID...

$ rune suggest apply 3f9a1c2e
Runefile: created, added go-test
```

Recording never changes the Runefile. Only `rune suggest apply`, run by a person with the
suggestions they chose, writes to it.

| Question | Answer |
|---|---|
| Is anything recorded without the person asking? | No. Recording starts only after `rune agent observe claude`, and `--remove` stops it |
| Does it need a Runefile? | No. Recording, suggestions and the first task all work in a project without one |
| Is a recorded command ever run? | No. Commands are read as text. Nothing is run to record, rank, preview or apply a suggestion |
| What is stored? | The command with secrets removed, its working directory, the time, the session and the outcome. Never its output |
| Where, and for how long? | In the person's state directory, outside the project, for at most 30 days and 4 MiB per project. `rune suggest forget` deletes it |
| Can recording change what the agent's command does? | No. The recorder runs in the background, always succeeds and prints nothing |
| Can applying a suggestion damage the Runefile? | No. New tasks are added at the end, every existing byte stays, and the result must pass analysis before it is written |

### Out of scope

- Clients other than Claude Code. Codex and VS Code are not observed by this feature.
- Commands from Claude Code's PowerShell tool. Only its Bash tool is recorded.
- Storing what a command printed. No option stores stdout or stderr.
- Reading transcripts, shell history or any other source than the hook events.
- Suggestions from calls to Rune's own MCP tools. Those are already tasks.
- Applying a suggestion automatically, or showing suggestions to an agent.
- Tasks with more than one parameter, dependencies, multi-line bodies or attributes other
  than `[working-directory]`.
- Recording for a whole team. The hook is installed in the person's own settings file for
  one project, never in a shared file or the person's user-wide settings.
- Changing the Rune-only profile (spec 047). Observation never adds, removes or changes a
  permission rule.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See which commands the agent repeats (Priority: P1)

A developer works with Claude Code in a project that has no Runefile. They run
`rune agent observe claude` once. Over the next days the agent runs `go test -run TestX
./pkg/...` fourteen times, among many other commands. The developer runs `rune suggest` and
sees that command with its count, its outcomes and a proposed task.

**Why this priority**: This is the feature. Without recording and a list of repeated
commands there is nothing to apply.

**Independent Test**: Replay recorded hook event fixtures into the recorder, including 14
executions of one command, then run `rune suggest` and check the listed counts. No recorded
command runs.

**Acceptance Scenarios**:

1. **Given** a project with no Runefile and no `.claude` directory, **When** the developer
   runs `rune agent observe claude`, **Then** `.claude/settings.local.json` is created with
   Rune's three hook handlers and the report says observation is on.
2. **Given** observation is on, **When** the 14 pre-execution events and 14 matching
   completion events for `go test -run TestX ./pkg/...` are replayed, **Then**
   `rune suggest` reports that command as run 14 times, not 28.
3. **Given** the same completion event is delivered twice, **When** `rune suggest` runs,
   **Then** that execution counts once.
4. **Given** 12 executions succeeded, 2 failed with `Exit code 1`, and 1 attempt has no
   recorded outcome, **When** `rune suggest` runs, **Then** it reports 14 runs, 12
   succeeded, 2 failed, and 1 attempt without an outcome.
5. **Given** a fixture command `touch $SENTINEL` replayed 20 times, **When** recording,
   listing, previewing and applying are done, **Then** the sentinel file does not exist.
6. **Given** the agent ran `rune test` and `rune -- lint` many times through the shell,
   **When** `rune suggest` runs, **Then** neither appears as a suggestion.
7. **Given** the recorded commands include one that ran in `web/` under the project root,
   **When** `rune suggest` lists it, **Then** it shows `web` as its working directory.

---

### User Story 2 - Apply a chosen suggestion to the Runefile (Priority: P1)

The developer picks a suggestion, previews the exact change, and applies it. Rune creates
the Runefile if there is none, or adds the task at the end of the existing one. Hand-written
tasks are untouched. The new task passes analysis.

**Why this priority**: A suggestion is only useful once it becomes a task. Together with
User Story 1 this is the smallest useful feature.

**Independent Test**: With fixture records in the store, apply one suggestion to a project
without a Runefile and to one with hand-written tasks. Check the diff, the written file, its
analysis, and that the original bytes are a prefix of the new file.

**Acceptance Scenarios**:

1. **Given** no Runefile, **When** the developer runs `rune suggest apply 3f9a1c2e`,
   **Then** a `Runefile` is created in the project root holding the task, and
   `rune analyze` reports no errors and no warnings for it.
2. **Given** a Runefile with hand-written tasks, comments and imports, **When** a
   suggestion is applied, **Then** every existing byte is unchanged and the task is added
   after a blank line at the end.
3. **Given** any suggestion, **When** the developer runs `rune suggest apply ID --dry-run`,
   **Then** the unified diff is printed and no file changes.
4. **Given** the Runefile already has a task named `go-test` with another body, **When**
   `rune suggest` lists the `go test` suggestion, **Then** its proposed name is `go-test-2`, and applying it
   leaves the existing `go-test` task unchanged.
5. **Given** the Runefile is edited by another program after Rune read it and before Rune
   replaced it, **When** apply detects the change, **Then** nothing is written and the
   developer is told to run the command again.
6. **Given** two IDs where the second is unknown, **When** both are applied in one command,
   **Then** nothing is written and the unknown ID is named.
7. **Given** a suggestion was applied, **When** `rune suggest` runs again with the same
   records, **Then** that command is no longer suggested, and the listing says one recorded
   command already has a task.
8. **Given** the existing Runefile has an analysis error, **When** a suggestion is applied,
   **Then** nothing is written and the error is shown with its location.

---

### User Story 3 - Secrets never reach storage or output (Priority: P1)

The agent sometimes runs commands with tokens in them: `GITHUB_TOKEN=ghp_x gh api ...`,
`curl -H "Authorization: Bearer x"`, `deploy --api-key=x`, `psql postgres://u:pw@db`.
None of these values is stored, listed, previewed, written or reported.

**Why this priority**: Recording commands is only acceptable if it cannot leak a secret.
This must hold from the first release.

**Independent Test**: Replay fixtures holding each secret form, then search the store,
`rune suggest` output, `--dry-run` diffs, written Runefiles and diagnostics for each secret
value. None is found.

**Acceptance Scenarios**:

1. **Given** a command `API_TOKEN=abc123xyz deploy`, **When** it is recorded, **Then** the
   stored command reads `API_TOKEN=*** deploy`.
2. **Given** commands with `--token VALUE`, `--password=VALUE`, `-H "Authorization: Bearer
   VALUE"`, and a URL `https://user:VALUE@host/x`, **When** they are recorded, **Then** each
   VALUE is replaced with `***` before anything is written.
3. **Given** the recorder's environment holds `DEPLOY_SECRET=s3cr3tvalue`, **When** a command
   contains `s3cr3tvalue` anywhere, **Then** the stored command holds `***` in its place.
4. **Given** a command with a here-document, a command that cannot be parsed, or one longer
   than 4096 bytes, **When** it is observed, **Then** nothing about its text is stored and
   the diagnostics count it by reason.
5. **Given** a stored command that holds `***`, **When** `rune suggest` lists it, **Then**
   it is a review item that says a value was removed, with no task text and no ID to apply.
6. **Given** a hook event whose Bash output contains a secret, **When** it is recorded,
   **Then** the output is not stored at all.

---

### User Story 4 - One task for a command that varies in one value (Priority: P2)

The agent runs `go test -run TestX ./pkg/...`, then `-run TestY`, then `-run TestZ`. Rune
suggests one task with a parameter for the value after `-run`, its default the value used
most, passed to the shell as one quoted word.

**Why this priority**: Many repeated commands differ only in a test name, a package or a
file. One parameterized task covers them. Exact repetitions already give value without it.

**Independent Test**: Replay fixtures with commands that differ in one word, in two words,
in a word holding quotes or `$`, and in working directory. Only the first set merges.

**Acceptance Scenarios**:

1. **Given** `go test -run TestX ./pkg/...` 5 times, `-run TestY` 3 times and `-run TestZ`
   once, **When** `rune suggest` runs, **Then** one suggestion covers 9 runs with a
   parameter `run` whose default is `TestX`, and its body passes the value with
   `{{quote(run)}}`.
2. **Given** the same commands where one variant ran in another working directory, **When**
   `rune suggest` runs, **Then** that variant is not merged.
3. **Given** variants that differ in two words, or where the differing word holds quotes, a
   `$`, or a glob character, **When** `rune suggest` runs, **Then** they are not merged.
4. **Given** a merged suggestion is applied, **When** the task runs with `run="Test A;
   rm x"`, **Then** the shell receives that value as one argument.

---

### User Story 5 - Only real gaps, and compound commands as review items (Priority: P2)

The listing leaves out what is already a task and what Rune cannot turn into a task safely.
A command that is already the body of a task is counted, not suggested. A command with a
subshell, a loop or a command substitution is listed for review without a generated task.

**Why this priority**: A list full of duplicates and unsafe guesses would be ignored.

**Independent Test**: With a Runefile whose tasks match some recorded commands, and fixtures
with compound commands, check what is suggested, what is counted, and what is a review item.

**Acceptance Scenarios**:

1. **Given** a task whose one body line is `go vet ./...`, **When** the agent ran
   `go vet ./...` 10 times, **Then** it is not suggested and the listing counts it under
   "already a task: vet".
2. **Given** `cd web && npm test` ran 6 times, **When** `rune suggest` runs, **Then** the
   suggestion is `npm test` with `[working-directory("web")]`.
3. **Given** `for f in *.go; do gofmt -l $f; done` or `go test $(go list ./...)` ran 5
   times, **When** `rune suggest` runs, **Then** it is a review item naming the reason, with
   no task text.
4. **Given** a command ran in a directory outside the project root, **When** it is observed,
   **Then** it is not recorded and the diagnostics count it.

---

### User Story 6 - Stop observing and delete the records (Priority: P2)

The developer has their tasks and wants recording to stop. They remove the hook and delete
what was recorded. Other hooks in the same settings file keep working.

**Why this priority**: An opt-in recorder must be as easy to turn off and erase as to turn
on.

**Independent Test**: Install into a settings file that already holds other hooks and
settings, remove, and compare. Delete the records and check the store directory.

**Acceptance Scenarios**:

1. **Given** a settings file with the developer's own hooks on the same events, **When**
   observation is turned on and then removed, **Then** the developer's hooks and settings
   are unchanged and no handler that runs `rune agent record` remains.
2. **Given** observation is on, **When** `rune agent observe claude` runs again, **Then**
   the report says unchanged and the file's bytes are identical.
3. **Given** records exist, **When** the developer runs `rune suggest forget`, **Then**
   every record and diagnostic for this project is deleted, the count is reported, and the
   settings and Runefile are untouched.
4. **Given** records older than 30 days, **When** the recorder or `rune suggest` next runs,
   **Then** they are gone.

---

### Edge Cases

- **The strict Rune-only profile is on**: `rune agent init claude` denies the Bash tool, so
  the agent runs no shell commands and nothing is recorded. `rune agent observe claude`
  says so in a note and does not change the deny rule. The documentation says observation
  is for projects that have not turned that profile on yet.
- **Events arrive out of order**: background hooks can finish in any order. A completion
  recorded before its pre-execution event still forms one execution.
- **Only a pre-execution event**: the call was denied by a rule, blocked, or is still
  running. It is an attempt, not a run, and does not count toward a suggestion.
- **A failure without an exit code**: the shell could not be started. It counts as a
  failed run with no exit code.
- **An interrupted command**: it counts as a run that was interrupted.
- **A background command**: the success event only says it started. It counts as a run
  whose outcome is not known.
- **An event without a tool-use id**: it cannot be matched or de-duplicated. It is not
  recorded, and the diagnostics count it.
- **Events from other tools or other hook events**: ignored without a record.
- **The client's project directory is not given**: the event cannot be attributed. It is not
  recorded and is counted in diagnostics shared by all projects.
- **The project has a Runefile that cannot be read**: the recorder cannot know the secret
  names it declares, so the event is not recorded and the diagnostics count it.
- **Two recorders write at once**: both records are stored whole. A recorder that cannot get
  the store within 2 seconds drops its event and counts it.
- **The store holds a damaged line**: it is skipped and counted, and the rest is read.
- **A multi-line command**: stored, but listed as a review item because a task body line is
  one line.
- **A command holding `{{`**: the generated body escapes it so the task runs the same text.
- **A suggested name is a Rune keyword or a rune command**: it gets a prefix or suffix that
  makes it a valid task name, as `rune import` does.
- **The Runefile has Windows line endings or no final line feed**: the added text uses the
  file's line ending, and a missing final line feed is added before the blank line.
- **The Runefile is a symbolic link**: apply refuses and names the link target to edit
  instead.
- **The settings file has comments or trailing commas**: it is edited in place like
  `rune agent init` edits it, keeping both.
- **The settings file is not valid JSON**: nothing is written, and the error names the file
  and position.
- **A task named `suggest` exists**: `rune suggest` runs the built-in command; the task runs
  with `rune -- suggest`, and the built-in says so.
- **The project moved**: records stay with the old path and expire. A new `rune agent
  observe claude` is not needed because the handler names no path.

## Requirements *(mandatory)*

### Functional Requirements

**Turning observation on and off**

- **FR-001**: `rune agent observe claude` MUST add to `.claude/settings.local.json` in the
  project root (FR-025) one handler on each of the three Claude Code hook events for the Bash tool:
  before the call, after it succeeds, and after it fails. Each handler MUST run the command
  `rune agent record` in the background. The file and the `.claude` directory MUST be
  created when missing. It MUST work in a project without a Runefile.
- **FR-002**: Installing and removing MUST keep everything else in the settings file as it
  was: other handlers on the same events, other events, other settings, comments, key order
  and layout. Observation MUST NOT add, remove or change any permission rule.
- **FR-003**: Installing MUST be idempotent. An event that already has a handler running
  `rune agent record` counts as installed. A second run MUST report the file unchanged and
  leave its bytes identical.
- **FR-004**: `rune agent observe claude --remove` MUST remove every handler whose command
  is `rune agent record` from those three events, then any matcher group and event list that
  became empty because of it, and nothing else. It MUST NOT delete the file. With no such
  handler, it MUST report the file unchanged.
- **FR-005**: `--dry-run` MUST print the unified diff of the change and write nothing.
- **FR-006**: The report MUST give the file's status (created, updated, unchanged) and MUST
  add a note when the project's Claude Code settings deny the Bash tool, saying that nothing
  will be recorded while the rule stands. When turning observation on, it MUST also note that
  Claude Code is to be started in the project root.
- **FR-007**: A settings file that is not valid JSON MUST be reported with its path and
  position, and nothing written. A client other than `claude` MUST be a usage error.
- **FR-008**: Writes to the settings file MUST be atomic: a reader sees the old file or the
  new one, never a mix.

**Recording**

- **FR-009**: `rune agent record` MUST read one hook event from standard input and record it
  only when the tool is Bash and the event is one of the three in FR-001. It MUST ignore
  every other event without writing anything.
- **FR-010**: The recorder MUST NOT run, evaluate or expand the recorded command. It MAY
  parse it as shell syntax, as data only.
- **FR-011**: The recorder MUST exit with status 0 and MUST write nothing to standard output
  or standard error, whatever the input. It MUST stop within 5 seconds even when its input
  never ends. Problems MUST go to the diagnostics (FR-050).
- **FR-012**: The recorder MUST find the project root from the client's project directory,
  given to the hook process by Claude Code, with the rule of FR-025. An event without that
  directory MUST NOT be recorded and MUST be counted in diagnostics shared by all projects.
- **FR-013**: A record MUST hold only: the time, the client (`claude`), the session id, the
  tool-use id, the event kind, the exit code when a failure event states one, whether the
  command ran in the background, the working directory relative to the project root, the
  command after redaction, and the agent's short description of the command after
  redaction. It MUST NOT hold the command's output, the error text beyond the exit code, the
  transcript path or any environment value.
- **FR-014**: An event whose working directory is outside the project root MUST NOT be
  recorded and MUST be counted in diagnostics.
- **FR-015**: Before anything is written, the recorder MUST replace with `***`:
  1. the value of a leading or exported assignment `NAME=value` whose name Rune treats as a
     secret name (the same names, with the same exemptions, it masks in task output);
  2. the value of a flag whose name, with dashes as underscores, is such a name, whether
     written `--flag value`, `--flag=value` or `-flag value`;
  3. the value of an HTTP header argument `Authorization: ...`, `Proxy-Authorization: ...`,
     or one whose header name is such a name;
  4. the password in a URL of the form `scheme://user:password@host`;
  5. every occurrence of the value of an environment variable that Rune masks for this
     project: by name in the recorder's environment and, when the project has a Runefile,
     the names it lists in `set secrets`, less those in `set unmasked`.
  The description field MUST get rule 5 and be cut to one line of at most 200 bytes.
- **FR-016**: The recorder MUST NOT store any text of a command that cannot be parsed,
  that holds a here-document, that is longer than 4096 bytes, or that holds a control
  character other than tab and line feed. It MUST count each by reason.
- **FR-017**: An event with no tool-use id or no command, or larger than 4 MiB, MUST NOT be
  recorded and MUST be counted.
- **FR-018**: When the project has a Runefile that cannot be parsed, the recorder MUST NOT
  record the event and MUST count it.
- **FR-019**: Concurrent recorders MUST NOT lose, merge or tear records. A recorder that
  cannot get the store within 2 seconds MUST drop its event and count it.
- **FR-020**: Records MUST be kept outside the project, in the person's state directory,
  one directory per project, readable only by the person who wrote them. A state directory
  that lies inside the project MUST be refused, and the event counted.
- **FR-021**: Records older than 30 days MUST be removed. A project's records MUST NOT take
  more than 4 MiB at any time; the oldest are removed first, before a record that would pass
  the bound is added. One record MUST NOT exceed 8 KiB.

**Counting executions**

- **FR-022**: Events with the same tool-use id MUST form one execution, in whatever order
  they arrive. The same event kind for the same id MUST count once.
- **FR-023**: An execution's outcome MUST be: failed when a failure event says the command
  failed, with its exit code when stated; interrupted when the failure event says it was
  interrupted; started, outcome not known, for a background command with a success event;
  succeeded for a success event otherwise; and attempt when only the pre-execution event
  exists. When events disagree, failed wins over interrupted, and both over succeeded.
- **FR-024**: A run is an execution with an outcome event. Attempts MUST NOT count as runs
  and MUST be reported apart.

**Listing suggestions**

- **FR-025**: The project root MUST be the directory of the Runefile Rune finds from the
  starting directory, as for any `rune` command, or the starting directory itself when there
  is none. For commands the starting directory is the current one, or the directory of the
  file given with `--file`; for the recorder it is the client's project directory.
- **FR-026**: `rune suggest` MUST read only the records and the Runefile. It MUST NOT run
  any command or task. It MUST work with no Runefile. A Runefile with an analysis error MUST
  be reported with its diagnostics, with nothing listed.
- **FR-027**: Two runs MUST belong to the same group when their commands are the same after
  parsing, ignoring only the spacing between words, and they ran in the same working
  directory. A command `cd DIR && COMMAND`, where DIR is one literal relative path that stays
  inside the project, MUST be treated as COMMAND run in DIR.
- **FR-028**: A group MUST be listed when it has at least 3 runs. `--min-runs N` MUST change
  that, for N from 1 to 1000.
- **FR-029**: A group MUST be left out when any command in it runs `rune`, and MUST be left
  out and counted when it matches an existing task: a task whose body is one line that is
  the same command, where a body word that is only a parameter interpolation matches any one
  plain word, and whose working directory is the same.
- **FR-030**: A listed group MUST become a suggestion when its command is supported, and a
  review item with its reason otherwise. Supported means: one line; one or more simple
  commands joined by `&&`, `||` or `|`; with redirections and `$NAME` or `${NAME}`
  expansions allowed; and with no command or process substitution, arithmetic expansion,
  subshell, brace group, compound command (`if`, `for`, `while`, `until`, `case`,
  `select`), function definition, `;`, `&`, here-document, stored `***`, or built-in that
  changes the shell's state (`cd` outside the form of FR-027, `pushd`, `popd`, `export`,
  `unset`, `set`, `source`, `.`, `eval`, `exec`, `alias`, `unalias`, `shopt`, `trap`,
  `umask`, `ulimit`, `declare`, `typeset`, `local`, `readonly`).
- **FR-031**: Each suggestion MUST show: an ID, the runs and their outcomes, the number of
  sessions, the last day it ran, its working directory when not the root, the command, and
  the task text it would add. Each review item MUST show the runs, the
  command, and the reason, with no ID and no task text.
- **FR-032**: The ID MUST be derived from the suggestion's content (its command or command
  pattern and working directory), so it stays the same while the records and Runefile
  change, and differs between suggestions.
- **FR-033**: The proposed name MUST be built from the command's first words, up to three,
  joined with `-`, stopping at the first word that is not a lowercase letter followed by
  lowercase letters, digits, `-` or `_` (`go test -run X` gives `go-test`, `npm run lint`
  gives `npm-run-lint`). It MUST be a valid task name that is not a keyword or a rune command, and MUST NOT
  equal an existing task's name or another suggestion's; a clash gets `-2`, `-3`, and so on.
- **FR-034**: The description MUST be the agent's description used most often in the group,
  or, when there is none, `Run` followed by the words the name was built from (`Run go
  test.`).
- **FR-035**: The task text MUST be: the description as a doc comment;
  `[working-directory("DIR")]` when the working directory is not the root; the name and any
  parameter; and one body line that is the command, with `{{` written so the task runs the
  same text. It MUST NOT carry any other attribute (in particular no `[readonly]`,
  `[confirm]`, `[agent-allow]` or `[network]`), any dependency, or any reference to a secret.
- **FR-036**: Groups with the same working directory and the same number of words, that
  differ only in one word position other than the first, MUST merge into one suggestion with
  a parameter when: each command is one simple command; every differing word is plain (letters, digits and
  `_ . / : @ % + , = -`, not starting with `-`, at most 256 bytes); there are at least two
  different values; and their runs together reach the threshold. The parameter MUST be named
  after the flag right before the word (`-run` gives `run`), or after the flag in a
  `--flag=value` word where only the value differs, and `value` otherwise. Its default MUST
  be the value run most often, the latest on a tie. The body MUST pass it as
  `{{quote(NAME)}}`. The merged groups MUST NOT also be listed alone.
- **FR-037**: Suggestions and review items MUST be ordered by runs, most first, then by the
  latest run, then by ID.
- **FR-038**: The listing MUST start with how many commands were recorded, in how many
  sessions, and over which dates, and MUST say how many groups were left out because they
  are already tasks, how many events were not recorded and why, and how many damaged lines
  of the store were skipped. With no records it MUST say so and how to turn observation on.

**Applying**

- **FR-039**: `rune suggest apply ID...` MUST add the tasks of the given suggestions, as
  `rune suggest` would list them at that moment. When an ID is unknown or is a review item,
  nothing MUST be written and the ID MUST be named. All given IDs are applied together or
  none is.
- **FR-040**: With no Runefile, apply MUST create `Runefile` in the project root. Otherwise
  it MUST add the tasks at the end of the Runefile, after a blank line, leaving every
  existing byte unchanged. It MUST use the file's line ending and add a missing final line
  feed first.
- **FR-041**: Before writing, the complete new Runefile MUST pass analysis with its
  imports. A Runefile that already has an error MUST be refused with the diagnostics, and
  nothing written.
- **FR-042**: Apply MUST read the Runefile again right before replacing it. When it changed
  since it was first read, nothing MUST be written and the person MUST be told to run the
  command again.
- **FR-043**: The Runefile MUST be replaced atomically and keep its permissions. A Runefile
  that is a symbolic link MUST be refused.
- **FR-044**: `--dry-run` MUST print the unified diff and write nothing.
- **FR-045**: After writing, apply MUST name the tasks it added and note that they carry no
  attributes, so an agent can call them through `rune mcp` without approval, and that a task
  that changes state needs `[confirm]` added by hand.

**Deleting records**

- **FR-046**: `rune suggest forget` MUST delete every record and diagnostic of the project
  and report how many records it deleted. It MUST NOT touch the settings file or the
  Runefile.

**Command names**

- **FR-047**: `suggest` MUST become a built-in command. A task named `suggest` MUST run with
  `rune -- suggest`, and `rune suggest` MUST say so when the Runefile has such a task.
  `rune import` MUST treat `suggest` as a rune command name.
- **FR-048**: `rune agent observe` and `rune agent record` MUST be subcommands of
  `rune agent`. `rune agent record` is meant for the hook and MUST NOT be listed in help.

**Exit status**

- **FR-049**: The commands MUST exit 0 on success; 2 for usage errors, unknown IDs and
  conflicts (a changed Runefile, a settings value of the wrong type); 3 for a settings file or Runefile that cannot be read or
  analyzed; and 1 for other failures. `rune agent record` always exits 0 (FR-011).

**Diagnostics and documentation**

- **FR-050**: The recorder MUST keep, per project and shared, a count of events not recorded
  per reason with the time of the latest. `rune suggest` MUST show non-zero counts. No
  diagnostic MUST hold command text, a description, or any value from an event.
- **FR-051**: The documentation MUST cover: turning observation on and off; what is stored,
  where, for how long, and how to delete it; how to apply a suggestion; that the strict
  Rune-only profile leaves nothing to observe and is not weakened; that the client must be
  started in the project root; and a worked example with a noisy agent session.

### Key Entities

- **Hook event**: one JSON document Claude Code passes to a hook handler. The recorder uses
  its event name, tool name, tool input (command, description, background flag), tool-use
  id, session id, working directory, and for a failure its error's first line and interrupt
  flag.
- **Record**: what the recorder stores for one hook event, as listed in FR-013.
- **Execution**: the records that share a tool-use id, with one outcome (FR-023).
- **Group**: runs of the same command in the same working directory (FR-027).
- **Suggestion**: a group, or merged groups (FR-036), with an ID, evidence, a name, a
  description and the task text to add.
- **Review item**: a listed group that Rune does not turn into a task, with its reason.
- **Diagnostics**: per-reason counts of events that were not recorded, per project and
  shared.
- **Observation handler**: an entry in `.claude/settings.local.json` that runs
  `rune agent record` for one hook event.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Replaying the 14-run fixture yields exactly one suggestion reporting 14 runs,
  and across every fixture no recorded command is ever run: sentinel files that a command
  would create never appear.
- **SC-002**: In an empty directory, four commands (`rune agent observe claude`, a replay of
  the fixtures, `rune suggest`, `rune suggest apply ID`) produce a Runefile that `rune
  analyze` accepts with no errors and no warnings.
- **SC-003**: For every secret fixture, each secret value appears 0 times in the store
  files, the `rune suggest` output, the `--dry-run` diffs, the written Runefile and the
  diagnostics.
- **SC-004**: For 100% of recorder inputs in the test corpus, including malformed, oversized,
  foreign and duplicate events, the recorder exits 0 and prints nothing.
- **SC-005**: Installing twice leaves the settings file byte-identical after the second run,
  and installing then removing leaves every other hook and setting unchanged.
- **SC-006**: In 100% of apply tests, including name clashes and a concurrent change, the
  original Runefile's bytes are a prefix of the file afterwards, or the file is unchanged.
- **SC-007**: For every applicable suggestion in the fixture corpus, the generated body line
  parses into the same words as the recorded command, apart from the parameter, so argument
  boundaries are preserved.
- **SC-008**: After 100,000 recorded events, one project's records take at most 4 MiB, and
  none is older than 30 days.

## Assumptions

- The recorder relies on Claude Code's documented hook input: `hook_event_name`,
  `tool_name`, `tool_input.command`, `tool_input.description`,
  `tool_input.run_in_background`, `tool_use_id`, `session_id`, `cwd`, and on failure
  `error` (first line `Exit code N` when the command ran) and `is_interrupt`, with the
  project directory in the `CLAUDE_PROJECT_DIR` environment variable. The fixtures are
  built from the documented examples. Fields that are missing are handled as in the Edge
  Cases, not assumed.
- Background hook handlers (`async`) never block or change a tool call, so the recorder
  cannot affect the agent's command even when it is slow.
- `rune` is on the PATH of the hook process, as `rune agent init` already assumes for the
  MCP server.
- `.claude/settings.local.json` is the person's own file. The documentation tells them not
  to commit it; Rune does not edit `.gitignore`.
- Secret names are the ones Rune already masks in task output. Rune does not try to
  recognize secret values by their shape.
- The 3-run threshold, 30-day and 4 MiB bounds, and 4096-byte command limit are defaults
  chosen for a single developer's project. Only the threshold is adjustable.
- Reserving `suggest` as a command name is a breaking change for a Runefile with a task of
  that name, handled like `agent` and `export` were.
