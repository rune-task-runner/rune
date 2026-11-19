---

description: "Task list for spec 055: set up an agent client for a Rune-only workflow with rune agent init"
---

# Tasks: Set Up an Agent Client with `rune agent init`

**Input**: Design documents from `/specs/055-agent-init/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and the issue's acceptance criteria ask for
them. A test is written before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US6)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of `internal/agentinit`.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- Golden cases live in `internal/agentinit/testdata/<client>/<case>/`:
  - `flags`: always present, possibly empty. One option per line: `force`, `unpinned`, `crlf`,
    `runefile=<path>` (default `/work/demo/Runefile`), `version=<v>`. With no `version` line
    the version is the adapter's first checked version; `version=` with no value means that
    no version was found.
  - `in/`: the files that exist before the run. Absent for a new project.
  - `want/`: the files after the run. Absent when the run changes nothing.
  - `want.report`: `Report(result, false)`. When the plan has conflicts it holds one line per
    conflict, `conflict: <file>:<line>:<col>: <message>`, and nothing else. When `Plan`
    returns an error it holds `error: <message>`.
  - `Inputs.Dir` is the directory of the case's Runefile path.
  - With `crlf`, the test turns every `\n` of the `in/` files into `\r\n` before the run,
    requires that no output file holds a `\n` without `\r` before it, and compares with
    `want/` after turning them back; this holds for the files that `in/` has. The repository
    normalizes line endings, so a fixture
    cannot hold them itself.
  - Golden files are regenerated with `-update` and read, never edited by hand.
- "Nothing was written" and "nothing ran" are checked from the outside: the bytes of every
  file, the listing of the directory, a marker file.
- The exact texts are in [contracts/rune-agent-init.md](contracts/rune-agent-init.md) and the
  files in [contracts/client-files.md](contracts/client-files.md). Tests compare whole strings
  unless the task says otherwise.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

- [X] T001 Create `internal/agentinit/doc.go` with the package comment: the package plans the
  client files of `rune agent init` from bytes to bytes, reads no file, writes no file and
  starts no process. Add `github.com/pelletier/go-toml/v2` v2.4.3 to `go.mod` and `go.sum`
  with `go get` inside the test container when T007 first imports it

---

## Phase 2: Foundational (blocks every user story)

- [X] T002 [P] Write tests in `internal/agentinit/jsonedit_test.go` for `parseJSON` (objects,
  lists, strings with escapes and `\u` pairs, numbers, `true`, `false`, `null`; `//` and
  `/* */` comments; a trailing comma; an empty or white-space document is an empty object;
  each syntax error gives a `*ParseError` with the right line and column) and `decodeJSON`
  (the tree as plain values), and a fuzz target `FuzzParseJSON` that requires, for every
  input `encoding/json` accepts, the same decoded value
- [X] T003 [P] Write tests in `internal/agentinit/jsonedit_test.go` for `jsonSet`: a missing
  key in a multi-line object (own line, the indentation of the first member); in a one-line
  object (same line); in an empty object (opened over lines, the file's indentation unit, two
  spaces by default); missing objects on the path are created; an equal value changes
  nothing; another value is a `Conflict` with line, column, both values and
  `Forceable: true`; with `force` only the value's bytes change; a non-object on the path is
  a `Conflict` with `Forceable: false`; comments and a trailing comma before the insertion
  point survive; `\r\n` files get `\r\n`; a string value with a backslash and a quotation
  mark decodes to the same string
- [X] T004 [P] Write tests in `internal/agentinit/jsonedit_test.go` for `jsonAppend`: a
  missing list is created inline; missing strings are added after the last element of a
  one-line list and of a multi-line list; present strings are not added again; an empty list;
  a non-list is a `Conflict` with `Forceable: false`; nothing is ever removed or reordered;
  the returned slice names exactly the strings that were added; and for `verifyJSON(old, new,
  sets, appends)`: nil when the new text decodes to the old value with the keys set and the
  rules appended; an error when another key was dropped, a value differs, or the text does not
  parse
- [X] T005 Implement `internal/agentinit/jsonedit.go`: `parseJSON`, `decodeJSON`, `jsonSet`,
  `jsonAppend`, `verifyJSON` and the value renderer (objects over lines, lists of strings
  inline, strings through `encoding/json` without HTML escaping) per research R3 and R6,
  until T002 to T004 pass
- [X] T006 [P] Write tests in `internal/agentinit/tomledit_test.go` for `tomlValue` (`false`,
  a basic string in double quotes with `\\`, `\"` and a control character escaped, a list of
  strings on one line; each decodes back to the same value) and `tomlSet`: a key added after
  the last key of an existing table, also when that key has a trailing comment or a
  multi-line value; a table appended at the end of the file after one empty line, also when
  the file does not end in a line break; an empty file; an equal value changes nothing;
  another value is a `Conflict` with line, column and both values; with `force` only the
  value changes and a trailing comment stays; a quoted header `[mcp_servers."rune"]` is found;
  comments and other tables are byte-identical; `\r\n` files get `\r\n`; an inline table
  `mcp_servers = { rune = {} }` and a dotted key `features.x = 1` at the top give a
  `*ManualError` with the line and column of that construct and the setting to add; a file that
  is not TOML gives a `*ParseError` with line and column
- [X] T007 Implement `internal/agentinit/tomledit.go`: `tomlValue`, `tomlSet` on
  `unstable.Parser{KeepComments: true}`, and the check that the new text decodes to the old
  value with the one key set (research R4, R6), until T006 passes
- [X] T008 [P] Write tests in `internal/agentinit/diff_test.go` for `unifiedDiff(path, old,
  new, existed)`: equal texts give an empty string; a new file has `--- /dev/null` and
  `+++ b/<path>`; a changed file has `--- a/<path>`, `+++ b/<path>` and hunks with three
  lines of context and correct `@@` ranges; two changes far apart give two hunks; a text
  without a final line break gets `\ No newline at end of file`; then implement
  `internal/agentinit/diff.go`
- [X] T009 Write tests in `internal/agentinit/agentinit_test.go` for `ParseClient` (the three
  names; any other name gives `unknown client "x": the clients are claude, codex and
  vscode`), `File.Status` (`created`, `updated`, `unchanged`), `registration(runefile,
  unpinned)` (the pinned and the unpinned arguments), `(*ParseError).Error`,
  `(*ManualError).Error`, and a test that reads the package's Go files and fails when a
  non-test file imports `os`, `os/exec` or a package under `net` (FR-004); then implement the
  types of [data-model.md](data-model.md) and these functions in
  `internal/agentinit/agentinit.go`
- [X] T010 Write tests in `internal/agentinit/adapter_test.go` for `parseVersion` (takes the
  first `N.N.N` of `2.1.326 (Claude Code)`, `codex-cli 0.169.0` and a multi-line `code`
  output; empty for text without one), `adapter.isChecked` (an equal version; a version
  between two checked ones is not checked; one below and one above; an empty version),
  `adapter.checkedText` (`none`, one version, several joined with `, `) and
  `adapter.outcome(version, dir)`: the adapter's best outcome for a checked version;
  `RegisteredOnly` with the reason `this version was not checked` for another,
  `no version was found` for an empty version, and
  `no VS Code version has been checked with these files` for an adapter with no checked
  version, whatever the version; `Remains` and `Steps` as the contract lists them, with the
  directory quoted when it has a space; then implement `internal/agentinit/adapter.go` with
  the three adapter values (Claude Code checked `2.1.325`, Codex CLI checked `0.167.0`, VS
  Code checked none)
- [X] T011 Write tests in `internal/agentinit/report_test.go` for `Report(result, dryRun)`,
  `CheckReport(result)` and `Diff(result)`: the line order of the contract; each form of the
  file line, the client line and the outcome line, `Registered only, pending 2 steps`
  included; `pending 1 step` and `pending 2 steps`; `reason:` and `remains:` only for
  `Registered only`; the steps; the notes; the manual check; `dry run: nothing written`; the
  check lines with `present`, `missing`, `differs`, the `complete:` line, the `incomplete:`
  line with its counts, and no `client:` or `outcome:` line in a check; and a test that no
  report of any adapter contains the whole words `restricted`, `locked down` or `safe`; then
  implement `internal/agentinit/report.go`

**Checkpoint**: the editors, the difference, the adapters and the report work on bytes. No
command exists yet.

---

## Phase 3: User Story 1 - Set up Claude Code or Codex CLI in one command (P1) 🎯 MVP

**Goal**: `rune agent init claude` and `rune agent init codex` write the files of a new
project and report the outcome.

**Independent Test**: in a project with a Runefile and no client files, each command writes
the files of the contract and reports "Rune-only" pending the steps.

- [X] T012 [P] [US1] Add golden cases `internal/agentinit/testdata/claude/new/`,
  `claude/path-with-spaces/` (`runefile=/work/my demo/Runefile`) and `claude/path-escapes/`
  (a Runefile path with a backslash and a quotation mark), and the golden test `TestGolden`
  in `internal/agentinit/golden_test.go` that runs `Plan` for every case directory as the
  conventions say, compares `want/` and `want.report`, runs `Plan` again on the result and
  requires every file `unchanged`, requires that no `want/` file holds `allow-destructive`,
  `approve`, `bypassPermissions`, `defaultMode`, `env`, `token` or `Authorization` unless its
  `in/` file did (FR-009), and supports `-update`
- [X] T013 [P] [US1] Add golden cases `internal/agentinit/testdata/codex/new/`,
  `codex/path-with-spaces/` and `codex/path-escapes/`
- [X] T014 [US1] Write tests in `internal/agentinit/claude_test.go` for `planClaude`: the two
  files in the contract's order, the entries of FR-018 with state `missing`, the `Changes`
  text; and for `Plan`: an unknown client is an error, a planned JSON file is verified with
  `verifyJSON` and a failure is returned as an error that names the file; then implement
  `internal/agentinit/claude.go` and `Plan` in `internal/agentinit/agentinit.go` (plan every
  file, verify it, collect conflicts, reset every `New` to `Old` when there is a conflict,
  attach the outcome and the notes). `Plan` for `vscode` returns an error until T032
- [X] T015 [US1] Write tests in `internal/agentinit/codex_test.go` for `planCodex`: the one
  file, the four entries, the `Changes` text, and that the result never holds
  `unified_exec`, `multi_agent`, `default_tools_approval_mode`, `approval_mode` or `cwd`
  (FR-014); then implement `internal/agentinit/codex.go`
- [X] T016 [US1] Write tests in `internal/cli/agentinit_test.go` for `AgentInit` with a stub
  version lookup: a new project gets the files of the contract and the report on stderr; run
  from a subdirectory the files go to the Runefile's directory (US1 scenario 5); `opts.File`
  names another project; no Runefile is a `UsageError`; a Runefile with an error prints its
  diagnostics, writes nothing and is a `ValidationError`; a Runefile whose task, hook and
  `[context]` task would each create a marker file leaves no marker (FR-004); a Runefile with
  a task named `agent` prints the note (FR-006); an unknown client is a `UsageError`; new
  files have mode 0644 and new directories 0755; a target directory that is a symbolic link to
  a directory outside the project, and a target file that is such a link, are refused with
  the contract's message and the directory outside stays empty (FR-005, skipped where links
  cannot be made); a link to a directory inside the project is followed; with a `write`
  function that fails on the second file, an existing first file has its old bytes and mode
  again and a created first file and its created directory are gone, and the error is a
  `CommandFailure` that names the second file (FR-023); a client file that is a directory is
  a `CommandFailure` that names it
- [X] T017 [US1] Implement `internal/cli/agentinit.go`: `AgentInitFlags`, `AgentInit`,
  `readClientFiles`, `insideProject` (research R7), `writeClientFiles` (temporary file and
  rename, in the adapter's order; when one write fails, the files already written are put
  back and what the run created is removed) and `clientVersion` (run `<binary> --version`
  with the time limit; any failure gives an empty version), until T016 passes. `--dry-run`
  and `--check` return a usage error that says they are not available until T029 implements
  them
- [X] T018 [US1] Write tests in `cmd/rune/agent_test.go` for `newAgentCmd`: `rune agent`
  prints help and exits 0; `rune agent deploy` is a usage error with the contract's message
  that names `rune -- agent`; `rune agent init` with no client is a usage error that lists
  the three names, and with two clients a usage error; the flags `--unpinned`, `--force`,
  `--dry-run`, `--check` reach `cli.AgentInitFlags` and `--file` reaches `opts.File`;
  completion of the first argument offers `claude`, `codex`, `vscode`; the help text of both
  commands holds none of the whole words `restricted`, `locked down`, `safe`; then implement
  `cmd/rune/agent.go` and register it in `addCommands` in `cmd/rune/main.go`. Add `agent` to
  `RuneCommands` in `internal/taskimport/finish.go` with its case in
  `internal/taskimport/finish_test.go`, so that `TestImporterKnowsTheCommands` in
  `cmd/rune/import_test.go` passes, and regenerate any golden file that lists the commands
- [X] T019 [US1] Write tests in `test/integration/agent_init_test.go` with the built binary;
  every run sets `PATH` to a directory the test made, so the host's clients are never found:
  `rune agent init claude` and `codex` in a new project write the files, exit 1 with
  "Registered only" when that directory is empty, and exit 0 with "Rune-only" when a script
  named `claude` or `codex` in it prints a checked version (skipped on Windows); a second run
  leaves every modification time unchanged; `rune -- agent` still runs a task named `agent`,
  `rune agent` prints the note and exits 0, and `rune agent x` exits 2; and the server
  started from another directory with the `args` read from the written `.mcp.json` lists the
  project's tasks as tools, with the helpers of `test/integration/file_flag_mcp_test.go`
  (issue acceptance criterion 2)

**Checkpoint**: the MVP. Both clients can be set up in a project without client files.

---

## Phase 4: User Story 2 - Keep the configuration that is already there (P1)

**Goal**: an existing file keeps everything the command does not own; conflicts stop the run.

**Independent Test**: the merge fixtures are byte-identical outside the owned entries, and a
second run writes nothing.

- [X] T020 [P] [US2] Add golden cases under `internal/agentinit/testdata/claude/`:
  `other-servers` (another server, unknown keys, odd indentation), `existing-deny`
  (`Bash(rm *)` and `WebFetch` stay, the six names follow), `some-rules-present`,
  `one-line-file`, `crlf` (flag `crlf`), `empty-file`, `single-tool-deny`
  (`mcp__rune__deploy` gives a note), `ask-rule` (a note), `force-args` (flag `force`: only
  `type`, `command`, `args` are replaced and `env` stays), `conflict-args` (other `args`: the
  report is the conflict line, no `want/`), `conflict-deny-all` (flag `force`,
  `permissions.deny` with `mcp__rune`)
- [X] T021 [P] [US2] Add golden cases under `internal/agentinit/testdata/codex/`:
  `comments-and-servers` (comments, other servers, other keys under `[features]`),
  `existing-tables` (keys added inside the tables), `crlf` (flag `crlf`), `empty-file`,
  `force-shell-tool` (flag `force`: `shell_tool = true   # why` becomes `false` and the
  comment stays), `conflict-shell-tool`, `manual-inline-table` (the report is the error with
  the setting to add)
- [X] T022 [US2] Extend `internal/agentinit/golden_test.go`: for every case with an `in/`
  directory, a `want/` directory and no `force` flag, check that the bytes of each `in/` file
  are a subsequence of the bytes of its `want/` file (the command only inserted), and that
  the decoded `want/` file contains the decoded `in/` file: every key with an equal value,
  every list as an ordered subsequence (SC-002)
- [X] T023 [US2] Write tests in `internal/agentinit/claude_test.go` and
  `internal/agentinit/codex_test.go` for conflicts, each returning no changed file: other
  `args` for the server `rune` (forceable, with line and column); `"permissions": "none"` and
  `"mcpServers": []` (not forceable); `permissions.deny` with `mcp__rune`, with and without
  `force` (not forceable, the contract's message); `shell_tool = true` and
  `[agents] enabled = true` (forceable); a conflict in the second file of a client leaves the
  first file's `New` equal to its `Old` (US2 scenario 12); an invalid file gives a
  `*ParseError`; an inline table gives a `*ManualError`; then implement what fails in
  `claude.go`, `codex.go` and `Plan`
- [X] T024 [US2] Write tests in `internal/cli/agentinit_test.go`: a conflict is printed with
  `file:line:col` and a caret, no file line and no outcome line is printed, nothing is
  written, and the error is a `UsageError` with `agent init: 1 conflict(s), nothing written`;
  a `*ParseError` is a `ValidationError` with the position; a `*ManualError` prints its
  position and the setting and is a `UsageError`; an existing file keeps its mode after an
  update; then implement the rendering in `internal/cli/agentinit.go`
- [X] T025 [US2] Add to `test/integration/agent_init_test.go`: a project with the `in/` files
  of `claude/other-servers` on disk: after the run the other server is still there, the
  bytes of each `in/` file are a subsequence of the file on disk, and a second run changes
  nothing; the conflict run exits 2 and leaves every file as it was; the `--force` run exits
  as a clean run; with `.codex` present as a regular file the run exits 1 and the listing of
  the project is unchanged

**Checkpoint**: the command is safe to run on a project that already has client files.

---

## Phase 5: User Story 3 - Be told what the setup reached (P1)

**Goal**: the outcome line is never stronger than what was checked, and every step is named.

**Independent Test**: the three version cases give "Rune-only" only for a checked version.

- [X] T026 [P] [US3] Add golden cases `internal/agentinit/testdata/claude/version-newer`
  (`version=9.9.9`), `claude/version-none` (`version=`), `codex/version-older`
  (`version=0.1.0`) and `codex/version-between` (a version between two checked ones once the
  list has two; until then `version=0.168.0`), whose `want.report` hold the outcome, reason,
  remains, steps, notes and manual check of the contract. The cases without a `version` line
  already cover a checked version
- [X] T027 [US3] Write tests in `internal/cli/agentinit_test.go`: a checked version returns
  nil; an unchecked version and no version write the same files and return a
  `CommandFailure` with `agent init: the outcome is "Registered only"`; `clientVersion`
  returns empty for a program that is not found, one that exits non-zero, one that prints no
  version, and one that sleeps past the limit (the test sets `versionLimit` to a few
  milliseconds); then implement what fails
- [X] T028 [US3] Add to `test/integration/agent_init_test.go`: in one directory, a run with a
  `claude` script that prints a checked version and, after the files are removed, a run with
  one that prints `9.9.9` write byte-identical files; the second exits 1 (SC-005)

**Checkpoint**: all P1 stories done.

---

## Phase 6: User Story 4 - See the change first, and check a setup later (P2)

**Goal**: `--dry-run` and `--check` write nothing and tell the truth.

**Independent Test**: the directory is identical before and after both, and the printed
difference applied by hand gives the files of a real run.

- [X] T029 [US4] Write tests in `internal/cli/agentinit_test.go` for `--dry-run`: the unified
  difference of each changing file is on stdout, the report with `dry run: nothing written`
  on stderr, no file or directory is created, a conflict is reported as in a real run, and
  the exit status equals the real run's; for `--check`: the version lookup is never called
  (the stub fails the test when it is); a complete setup prints every entry `present`, the
  `complete:` line and the manual check, and returns nil; a setup with one deny rule removed
  prints that entry `missing` and the `incomplete:` line and returns a `CommandFailure` with
  `agent init: the setup is not complete`; a project never set up prints every entry
  `missing`; a conflicting value at an owned key prints `differs`; `permissions.deny` with
  `mcp__rune` prints the diagnostic and `incomplete: the setup has a conflict` and returns
  the `CommandFailure`; a `*ManualError` stays a `UsageError`; `--check` with `--force` or
  `--dry-run` is a `UsageError`; then implement both modes in `internal/cli/agentinit.go`
- [X] T030 [US4] Add to `test/integration/agent_init_test.go`: the difference printed by
  `--dry-run` on a merge fixture, applied with `git apply` in a copy of the project, gives
  the files of a real run (skipped when `git` is not found); the directory listing and file
  bytes are identical before and after `--dry-run` and `--check`; `--check` on a complete
  setup exits 0 with an empty `PATH` directory (SC-004, FR-026)

---

## Phase 7: User Story 5 - Set up VS Code with a custom agent (P2)

**Goal**: `rune agent init vscode` writes the two registrations and the agent file, and says
plainly that nothing was checked.

**Independent Test**: the three files of the contract exist; the outcome is "Registered only".

- [X] T031 [P] [US5] Add golden cases under `internal/agentinit/testdata/vscode/`: `new`,
  `comments` (a `.vscode/mcp.json` with `//` comments and another server), `after-claude`
  (`in/.mcp.json` as the Claude Code adapter wrote it: reported `unchanged`),
  `force-agent-file` (flag `force`), `conflict-agent-file`, `agent-file-crlf` (flag `crlf`,
  an `in/` agent file with the command's content: reported `unchanged`), `version-found`
  (`version=1.146.0`: still "Registered only" with the VS Code reason)
- [X] T032 [US5] Write tests in `internal/agentinit/vscode_test.go` for `planVSCode`: the
  three files in the contract's order; the agent file's bytes; its `tools` line is exactly
  `tools: ['rune/*']` and `agents: []` (FR-016); an agent file with other content is a
  forceable conflict at line 1; one with the same content and `\r\n` line endings is
  unchanged; its entry is named `content`; then implement `internal/agentinit/vscode.go` and
  remove the `vscode` error from `Plan`
- [X] T033 [US5] Add to `test/integration/agent_init_test.go`: `rune agent init claude` then
  `rune agent init vscode` leaves `.mcp.json` and `.claude/settings.json` byte-identical and
  exits 1

---

## Phase 8: User Story 6 - Commit the setup for a team (P2)

**Goal**: `--unpinned` registers the server without a path and says what that relies on.

**Independent Test**: the registered arguments hold no path and the report has the unpinned
note.

- [X] T034 [P] [US6] Add golden cases `internal/agentinit/testdata/claude/unpinned`,
  `codex/unpinned` (flag `unpinned`) and `claude/pinned-then-unpinned` (an `in/` written with
  the path, flag `unpinned`: the report is the conflict line and there is no `want/`)
- [X] T035 [US6] Write tests in `internal/cli/agentinit_test.go`: `--unpinned` gives a
  registration without a path and the report has the unpinned note and not the pinned one;
  without it the pinned note names `--unpinned`; `--unpinned` with `opts.File` naming
  `tasks.rune` is a `UsageError` with the contract's message; then implement what fails

---

## Phase 9: Client runs, documentation and polish

- [X] T036 Record the client runs of FR-031 for the installed versions in
  `specs/055-agent-init/observations/README.md` and one file per run: build `rune` from the
  branch, copy `test/integration/testdata/clients/scenario` to a directory inside this
  repository (a trusted project), run `rune agent init claude` and `rune agent init codex`
  there, then start each client with prompt B of spec 047 and nothing on the command line
  that sets tools. For Claude Code keep the `tools` list of the `system/init` event. For
  Codex also capture the request in the same directory with
  `specs/047-rune-only-agent-mode/observations/codex/capture.py` and list its tools with
  `tools.py`: the client's own listing must hold no `exec_command`, `write_stdin` or
  collaboration tool. Keep the client version, the files, the command and the output. A
  version is added to the adapter's list in `internal/agentinit/adapter.go` only when its run
  shows Rune's tools, no command-running tool in the client's listing, a Rune call that ran
  and a subagent without a command-running tool; then update the tests and golden files that
  name the list. Where a run cannot be made or fails, leave the list as it is, say so in the
  README, and name it in the pull request as an acceptance criterion that is still open.
  Remove the working directory afterwards
- [X] T037 [P] Write `docs/how-to/agent-setup.md` after T036: what the command does and does
  not do, the flags, per client the files written with their content, the versions checked,
  the three outcomes, the trust, approval and restart steps, that the client is opened in the
  project's directory, the manual checks, `--dry-run`, `--check`, `--force`, `--unpinned`,
  the entries that are Rune's and how to remove only those, the `ask` rule to add in Claude
  Code for a task a person names in `--allow-destructive`, what a setup does not prevent with
  a link to the enforcement boundaries of the design note, and that a task named `agent` is
  run with `rune -- agent` (FR-036, FR-037); add it to `docs/nav.yaml` and
  `docs/how-to/README.md`
- [X] T038 [P] Update `docs/cli.md` (a row for `rune agent init` with its flags and exit
  statuses, and `agent` wherever the command names are listed), `docs/mcp.md` (a pointer from
  Client compatibility; in Agent task bodies, `RUNE_MCP_URL`, `RUNE_MCP_TOKEN` and the
  `agent_cmd` of run a2: FR-038), `docs/use-cases/mcp-agents.md` (one pointer),
  `docs/how-to/import-from-other-runners.md` and `docs/troubleshooting.md` (`agent` in the
  lists of command names) and `docs/design/rune-only-agent-mode.md` (the opening note, the
  sentence of the executor section that says the variables are documented nowhere, and "What
  issue #53 must do" now say the command exists: FR-039); add
  `TestCLIReferenceAgentInitFlags` to `test/docs/cli_test.go`, as
  `TestCLIReferenceImportFlags` does for `rune import`
- [X] T039 Add `./internal/agentinit` to the golden gate and `FuzzParseJSON` to the
  fuzz-smoke job in `.github/workflows/ci.yml`, and run the docs harness:
  `go test ./test/docs/...`
- [X] T040 Run the whole suite, the race run, `golangci-lint` (the image CI pins) and the
  coverage of `internal/agentinit` (target 90%); fix what they find
- [X] T041 Walk through [quickstart.md](quickstart.md) scenarios 2 to 8 with the built binary
  and compare with the expected results

---

## Dependencies

- Phase 1 before everything. Phase 2 before every story.
- US1 (Phase 3) before US2 to US6: they extend its files.
- US2, US3, US4, US5 and US6 do not depend on each other.
- T036 needs US1 and US3. T037 needs T036 and the final texts of all stories; T038 needs the
  final texts. T039 to T041 come last.

## Parallel opportunities

- T002, T003, T004, T006 and T008 are different concerns; T002 to T004 share one test file
  and are written together.
- Within a story, the golden case tasks marked [P] can be prepared while the tests of the
  planner are written.
- T037 and T038 are different files.

## Implementation strategy

1. **MVP**: Phases 1 to 3. Two clients, new projects only.
2. **Safe on real projects**: Phase 4, then Phase 5 for the honest outcome. After Phase 5 all
   P1 stories are done and the command can ship.
3. **Review and the third client**: Phases 6 to 8, in any order.
4. **Evidence and documentation**: Phase 9.
