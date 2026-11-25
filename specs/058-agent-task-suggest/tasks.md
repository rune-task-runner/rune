---

description: "Task list for spec 058: learn Rune tasks from an agent's shell commands"
---

# Tasks: Learn Rune Tasks from an Agent's Shell Commands

**Input**: Design documents from `/specs/058-agent-task-suggest/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI asks for them, and the issue's acceptance
criteria name them. In every phase the test tasks come first, and a test is written and seen
to fail before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on
  an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US6)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of the new code.
- No test, fixture or tool ever runs a recorded command. A command that would leave a trace
  (`touch SENTINEL`) is used where the absence of a run must be shown.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- Golden files are regenerated with `-update` where the package supports it, never edited by
  hand.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

**Purpose**: the two new packages and the hook fixtures.

- [X] T001 Create `internal/observe/doc.go` and `internal/tasksuggest/doc.go` with package
  comments that say what each package does and that neither runs a recorded command
  (plan.md Structure Decision)
- [X] T002 [P] Add hook input fixtures under `internal/observe/testdata/hooks/`, built from
  the documented examples in contracts/hooks.md: `pre.json`, `success.json`,
  `failure-exit.json` (`Exit code 1`), `failure-no-exit.json` (no exit-code line),
  `interrupt.json` (`is_interrupt: true`), `background.json` (`run_in_background: true`),
  `other-tool.json` (`tool_name: "Write"`), `other-event.json` (`SessionStart`),
  `no-tool-use.json`, `not-json.txt`

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the shared helpers, and the recorder's core: decoding, omission, redaction,
records, the store and diagnostics. Nothing is stored without redaction at any point.

**CRITICAL**: no user story starts before this phase is complete.

### Tests for the foundation

- [X] T003 [P] Tests for `audit.StateDir`, `audit.PrivateDir` and `audit.LockDir` in
  `internal/audit/dir_test.go` and `internal/audit/log_test.go`: the XDG, Windows and home
  cases of `StateDir`; `DefaultBase` still returns `<state>/rune/audit` with its message;
  `PrivateDir` creates 0700, refuses a symbolic link and a file, fixes the mode; `LockDir`
  waits, times out with its message, and steals a stale lock; `StateDir` returns
  `<state>/rune` (research R4)
- [X] T004 [P] Tests for `taskimport.Quote` and `taskimport.EscapeBraces` (renamed, behavior
  unchanged) in `internal/taskimport/emit_test.go` and `internal/taskimport/shellline_test.go`,
  and that `RuneCommands` holds `suggest` in `internal/taskimport/finish_test.go`
- [X] T005 [P] Test for `agentinit.UnifiedDiff` (exported, behavior unchanged) in
  `internal/agentinit/diff_test.go`, and for `mask.SecretName` in `internal/mask/set_test.go`:
  a pattern name, a declared name, an exempt name, `GIT_AUTHOR_NAME` exempt, case ignored, and
  `NewSet` unchanged (research R6)
- [X] T006 [P] Tests for `observe.DecodeEvent` in `internal/observe/event_test.go`: every
  fixture of T002; input of exactly 4 MiB is read and 4 MiB plus one byte is `too-large`;
  `not-json`; unknown fields ignored; `tool_response` never kept (data-model
  `observe.Event`)
- [X] T007 [P] Tests for omission and redaction in `internal/observe/redact_test.go`, one
  table row per rule of research R6: omission for a parse error, `<<` and `<<-`, 4097 bytes,
  NUL and other control characters (tab and line feed allowed); replacement for a prefix
  assignment, `export`/`declare`/`local`/`readonly`/`typeset` assignments, `--flag value`,
  `--flag=value`, `-flag value`, `--api-key` turned into `API_KEY`, `Authorization:`,
  `Proxy-Authorization:`, `X-Api-Token:` headers in single and double quotes, URL passwords;
  a declared name and an exempt name through `Secrets.Names`; `GIT_AUTHOR_NAME=x` kept and
  `--author x` replaced; values from `Secrets.Values`
  anywhere in the text and in the description; the text outside replacements unchanged
  byte for byte; the description cut to one line and 200 bytes at a character boundary
  (FR-015, FR-016)
- [X] T008 [P] Tests for `observe.NewRecord` and `Record.Encode` in
  `internal/observe/record_test.go`: ignored events (`ErrIgnored`); `invalid-event` for a
  missing or 129-byte or control-character id and a missing command; `outside-project` for a
  `cwd` outside the root and the root itself giving `""`; `cwd` with forward slashes; `exit`
  from `Exit code 1`, none from another first line and none for a success; `interrupted` and
  `background`; an encoded line over 8 KiB fails (FR-013, FR-014, FR-017)
- [X] T009 [P] Tests for `observe.Store` in `internal/observe/store_test.go`: `Append` then
  `Read` oldest first; a damaged line skipped and counted; directories 0700 and files 0600 on
  Unix; a line that would take the file past 4 MiB first compacts it to the newest records
  within 3 MiB; compaction when the first record is older than 30 days; after 100,000
  appends the file was never larger than 4 MiB; `Read` hides records older than 30 days;
  `Forget` counts and removes everything; a held lock gives `store-busy` after the wait; a
  base directory inside the root is refused; 8 goroutines appending 200 records each lose
  and tear none (run with `-race`) (FR-019 to FR-021, SC-008)
- [X] T010 [P] Tests for `observe.CountDrop` and `observe.ReadDiagnostics` in
  `internal/observe/diagnostics_test.go`: counts add up per reason with the latest time; a
  missing file reads as empty; the file holds no text beyond reason names, counts and times
  (FR-050)

### Implementation of the foundation

- [X] T011 Export `StateDir` from `internal/audit/dir.go` and build `DefaultBase` on it;
  take `PrivateDir` out of `ensureDir` and add `LockDir` beside `lockDir` in
  `internal/audit/log.go`, with the audit log's behavior unchanged (research R4)
- [X] T012 [P] Rename `quote` to `Quote` in `internal/taskimport/emit.go` and `escapeBraces`
  to `EscapeBraces` in `internal/taskimport/shellline.go`, update their callers, and add
  `"suggest"` to `RuneCommands` in `internal/taskimport/finish.go`
- [X] T013 [P] Export `UnifiedDiff` in `internal/agentinit/diff.go` and use it from `Diff`;
  export `SecretName(name string, declared, exempt []string) bool` in `internal/mask/set.go`
  and have `NewSet` use the same rule
- [X] T014 Implement `Event`, `DecodeEvent` and `DropError` in `internal/observe/event.go`
  (`MaxEventBytes = 4 << 20`)
- [X] T015 Implement `Secrets`, omission and redaction in `internal/observe/redact.go`
  with `mvdan.cc/sh/v3/syntax` as data only: replacements spliced by parser offsets, then
  `Secrets.Values.MaskString` over the command and the description (research R6, R7)
- [X] T016 Implement `Record`, `NewRecord` and `Encode` in `internal/observe/record.go`
  (`MaxRecordBytes = 8 << 10`, the reasons of data-model)
- [X] T017 Implement `Store` in `internal/observe/store.go` on `audit.StateDir`,
  `audit.PrivateDir` and `audit.LockDir`: refusal of a directory inside the root, compaction
  before a line that would pass 4 MiB, by temporary file and rename under the lock, one
  `O_APPEND` write per record, `Read` without the lock, `Prune`, `Forget`
  (`MaxStoreBytes = 4 << 20`, `CompactTarget = 3 << 20`, `Retention = 30 days`)
- [X] T018 Implement `Diagnostics`, `CountDrop` and `ReadDiagnostics` in
  `internal/observe/diagnostics.go`

**Checkpoint**: `go test ./internal/audit/... ./internal/taskimport/... ./internal/agentinit/... ./internal/observe/...`
passes, with `-race` for the store.

---

## Phase 3: User Story 1 - See which commands the agent repeats (Priority: P1) MVP

**Goal**: `rune agent observe claude` turns recording on, `rune agent record` records the
replayed events, and `rune suggest` lists the 14-run command with its outcomes and task
text.

**Independent Test**: replay the 14-run fixture set into the real binary in a project with no
Runefile, then `rune suggest` lists one suggestion with 14 runs (quickstart scenario 1, first
half, and scenario 2).

### Tests for User Story 1

- [X] T019 [P] [US1] Tests for installing in `internal/agentinit/observe_test.go`: a new
  file gets the three groups of contracts/hooks.md; a file with comments, a trailing comma,
  other settings and the person's own `PostToolUse` group keeps them all byte for byte
  outside the insertion; a second plan is unchanged; an existing handler with Rune's command
  and extra keys counts as present; `hooks`, an event value or a group's `hooks` of the wrong
  type is a conflict with its position; a file that is not JSON is a `*ParseError`; a deny of
  `Bash` or `Bash(*)` in either file gives the note, and a deny of `Bash(git *)` does not;
  the start-in-root note; `permissions` decodes the same before and after, and
  `.claude/settings.json` is never among the planned files (FR-001 to FR-003, FR-006,
  FR-007)
- [X] T020 [P] [US1] Tests for `Executions` in `internal/tasksuggest/execution_test.go`: pre
  and success joined in either order; a repeated success counts once; failure with and
  without an exit code; interrupt; background success is `Started`; pre alone is `Attempt`;
  failure wins over interrupt and both over success; command and directory taken from the
  outcome record (FR-022 to FR-024)
- [X] T021 [P] [US1] Tests for grouping in `internal/tasksuggest/group_test.go`: commands
  differing only in spacing share a group, ones differing in quoting do not; different
  directories do not; any call of `rune` or `rune.exe`, also after `&&`, leaves the group out;
  the threshold with `MinRuns` 1, 3 and 1000; review items ordered like suggestions
  (FR-027 to FR-029, FR-037)
- [X] T022 [P] [US1] Tests for `Build` in `internal/tasksuggest/suggest_test.go`: the ID is
  8 hex digits and stable when other records are added, and 16 on a forced clash; names
  (`go-test`, `npm-run-lint`, `task` for `./x`, a keyword, a rune command, an existing task and
  an earlier suggestion get suffixes); the description with most runs, the latest on a tie,
  and `Run go test.` with none; the order by runs, latest run and ID (FR-031 to FR-034,
  FR-037)
- [X] T023 [P] [US1] Tests for the listing and the task text in
  `internal/tasksuggest/render_test.go`: the exact text of contracts/cli.md for one
  suggestion, the header with dates and sessions, the attempts line, singular forms, the
  outcome list order, `, in DIR`, the `Not recorded` line with reasons, the damaged-lines
  line, the empty-store and nothing-listed messages; the task text of a suggestion with and
  without `[working-directory]`, and a body holding `{{` that the analyzer reads back as the
  same text; for each fixture command, the body line parsed by the shell parser gives the
  same words as the recorded command (FR-035, FR-038, FR-050, SC-007)
- [X] T024 [P] [US1] Tests for `AgentObserve` and `AgentRecord` in
  `internal/cli/observe_test.go`: observe creates `.claude/settings.local.json` in a project
  without a Runefile and reports `created`; `--dry-run` prints the diff and writes nothing; a
  client other than `claude` is a usage error; a file that is not JSON exits 3 with its
  position; record appends one record for each fixture that should be recorded, writes
  nothing to either stream and returns nil for every fixture including the broken ones; an
  empty `CLAUDE_PROJECT_DIR` counts `no-project` in the shared file; a held store lock counts
  `store-busy` and an unwritable store `store-error`; a `.claude` link leading outside the
  project is refused and nothing written; input that never ends counts `slow-input` and the
  command returns within the limit (the limit set short through a test hook); a project
  reached through a symbolic link records into the same store that `rune suggest` reads from
  the resolved path (FR-001, FR-005, FR-007, FR-008, FR-009, FR-011, FR-012, research R3)
- [X] T025 [P] [US1] Tests for `Suggest` in `internal/cli/suggest_test.go`: no records gives
  the two-line message and exit 0; the 14-run set gives 14 runs, not 28, and one attempt
  without an outcome when a pre event has no outcome; `--min-runs 0` and `1001` are usage
  errors; a Runefile with errors exits 3 and lists nothing; the note for a task named
  `suggest`; from a subdirectory the root is the Runefile's directory, with no Runefile it is
  the current directory, and `--file` names it (FR-025, FR-026, FR-028, FR-047)
- [X] T026 [P] [US1] Tests for the commands in `cmd/rune/agent_test.go` and
  `cmd/rune/suggest_test.go`: `rune agent observe` takes one client; `rune agent record` is
  hidden from `rune agent --help` and exits 0 with arguments; `rune suggest WORD` names
  `rune -- suggest`; the Long help of `suggest` holds the task sentence;
  `TestImporterKnowsTheCommands` in `cmd/rune/import_test.go` passes with `suggest`
  (FR-047, FR-048)
- [X] T027 [US1] Integration test `TestSuggestColdStart` in
  `test/integration/suggest_test.go` with the real binary, `XDG_STATE_HOME` and
  `CLAUDE_PROJECT_DIR` in a temporary directory: `rune agent observe claude` with no
  Runefile, the 14-run set and 20 runs of `touch SENTINEL` piped into `rune agent record`,
  then `rune suggest` lists 14 runs and the sentinel suggestion, and `SENTINEL` does not exist
  (SC-001)

### Implementation for User Story 1

- [X] T028 [US1] Implement installing in `internal/agentinit/observe.go`: `ObserveCommand`,
  `ObserveEvents`, `ObservePath`, `ObserveInputs`, `PlanObserve` for `Remove == false`, with
  groups inserted through `insertItems`, the deny-rule note, and a check that the decoded new
  document equals the old one plus Rune's groups (research R2)
- [X] T029 [P] [US1] Implement `Execution`, `Outcome` and `Executions` in
  `internal/tasksuggest/execution.go` (research R8)
- [X] T030 [US1] Implement parsing, canonical text, grouping, the `rune` exclusion and the
  threshold in `internal/tasksuggest/group.go` (research R7, R9 steps 2, 3 and 5)
- [X] T031 [US1] Implement `Options`, `TaskInfo`, `Report`, `Suggestion` and `Build` with
  IDs, names, descriptions and order in `internal/tasksuggest/suggest.go` (research R11)
- [X] T032 [US1] Implement `(*Suggestion).Task` and the listing text in
  `internal/tasksuggest/render.go`, with `taskimport.Quote` and `taskimport.EscapeBraces`
  (research R12, contracts/cli.md)
- [X] T033 [US1] Implement `AgentObserve` (install and `--dry-run`) and `AgentRecord` in
  `internal/cli/observe.go`: the settings files read with `readClientFiles` and written with
  `writeClientFiles` (atomic, links out of the project refused); root from
  `CLAUDE_PROJECT_DIR` or the current directory as research R3 says, resolved with
  `filepath.EvalSymlinks` on both sides; standard input read in a goroutine with a 5-second
  limit, `recordSecrets` from the recorder's environment only for now, every
  failure a `CountDrop`, nothing printed, nil returned
- [X] T034 [US1] Implement `Suggest` in `internal/cli/suggest.go`: root, store read and
  prune, the Runefile's tasks from analysis, `tasksuggest.Build`, diagnostics from the
  project and the shared file, the listing on standard output, notes on standard error
- [X] T035 [US1] Add `observe` (with `--remove`, `--dry-run` and `-f/--file`; `--remove`
  answers a usage error until T067) and the hidden `record` subcommands in
  `cmd/rune/agent.go`,
  `newSuggestCmd` in `cmd/rune/suggest.go` with `--min-runs` and `-f/--file`, and register it
  in `cmd/rune/main.go`

**Checkpoint**: US1 tests and `TestSuggestColdStart` pass.

---

## Phase 4: User Story 2 - Apply a chosen suggestion to the Runefile (Priority: P1)

**Goal**: `rune suggest apply ID...` creates or extends the Runefile safely, and an applied
command is no longer suggested.

**Independent Test**: quickstart scenario 1, second half: dry run, apply, `rune analyze`,
list again.

### Tests for User Story 2

- [X] T036 [P] [US2] Tests for `tasksuggest.Append` in `internal/tasksuggest/render_test.go`:
  empty old text and old text of only white space (no separator); old text with and without
  a final line feed; `\r\n` text gets `\r\n`
  lines; two tasks separated by a blank line; the old bytes are a prefix of the result
  (FR-040, SC-006)
- [X] T037 [P] [US2] Tests for matching existing tasks in `internal/tasksuggest/group_test.go`:
  a one-line task whose body is the same canonical command in the same directory covers the
  group, with `@` and `-` prefixes, through `[working-directory]`; a different directory, a
  two-line body or another command does not; covered groups are in `Report.Covered` with the
  task name and runs (FR-029, FR-038)
- [X] T038 [P] [US2] Tests for `SuggestApply` in `internal/cli/suggest_test.go`: creates
  `Runefile` with no Runefile and `rune analyze` finds no error or warning; appends to a
  Runefile with comments and imports leaving every old byte; `--dry-run` prints the diff and
  writes nothing; no ID, an unknown ID, and one unknown among two write nothing and exit 2;
  a symbolic link exits 2; a Runefile changed between read and write (through a test hook)
  exits 2 and keeps the other program's bytes; a file that appears where there was none is not
  replaced; a Runefile with an error exits 3; an existing `go-test` with another body gives
  `go-test-2`; after apply, `Suggest` lists the command under `Already tasks`; the report and
  the note of contracts/cli.md (FR-039 to FR-045, FR-049, SC-002, SC-006)
- [X] T039 [US2] Extend `test/integration/suggest_test.go` with `TestSuggestApply`: dry run,
  apply, `rune analyze` exit 0 with no warning, `rune suggest` shows `Already tasks`
  (SC-002)

### Implementation for User Story 2

- [X] T040 [US2] Implement `Append` in `internal/tasksuggest/render.go` (research R12)
- [X] T041 [US2] Implement matching by equal canonical text and directory, and
  `Report.Covered`, in `internal/tasksuggest/group.go` and `internal/tasksuggest/suggest.go`
  (research R9 step 4, without placeholders)
- [X] T042 [US2] Implement `SuggestApply` in `internal/cli/suggest.go` following research R13
  steps 1 to 8, with `writeFileAtomic` for an existing file, a temporary file and `os.Link`
  for a new one, `agentinit.UnifiedDiff` for `--dry-run`, and a test hook between the second
  read and the write
- [X] T043 [US2] Add `suggest apply` with `--dry-run` in `cmd/rune/suggest.go`

**Checkpoint**: US1 and US2 together are the MVP.

---

## Phase 5: User Story 3 - Secrets never reach storage or output (Priority: P1)

**Goal**: the project's declared secrets are masked too, a command holding a removed value is
only a review item, and no secret fixture shows up anywhere.

**Independent Test**: quickstart scenario 3.

### Tests for User Story 3

- [X] T044 [P] [US3] Tests for `recordSecrets` in `internal/cli/observe_test.go`: with no
  Runefile, names by pattern and values from the environment; with a Runefile, its
  `set secrets` names and their values, `set unmasked` exemptions, `[env]` values and dotenv
  values; a Runefile that does not load gives `runefile-unreadable` and no record (FR-015
  rule 5, FR-018)
- [X] T045 [P] [US3] Test in `internal/tasksuggest/group_test.go` that a group whose command
  holds `***` is a review item with the reason `a value was removed as a secret; write this
  task by hand`, with no ID, and in `internal/tasksuggest/render_test.go` that the listing
  shows review items under `For review, not turned into a task:` with runs, command and
  reason (FR-030, FR-031, US3 scenario 5)
- [X] T046 [US3] Extend `test/integration/suggest_test.go` with `TestSuggestSecrets`: every
  secret command of quickstart scenario 3 replayed through the binary, then each secret value
  searched for in every file under `XDG_STATE_HOME`, in the output of `rune suggest` and
  `rune suggest apply --dry-run` for every listed ID, and in the Runefile after applying the
  applicable ones: zero matches; the here-document counted under `Not recorded` (SC-003)

### Implementation for User Story 3

- [X] T047 [US3] Implement `recordSecrets` in `internal/cli/observe.go`: when there is a
  Runefile, load it as the MCP server loads it with output discarded, and build
  `Secrets.Names` from its declared and exempt names and `Secrets.Values` with
  `deriveMaskSet`; otherwise from the environment alone; a load failure is a
  `runefile-unreadable` drop (research R6 step 3)
- [X] T048 [US3] Add the review reason for `***` to the classification in
  `internal/tasksuggest/group.go` and show review items in the listing in
  `internal/tasksuggest/render.go` (contracts/cli.md)

---

## Phase 6: User Story 4 - One task for a command that varies in one value (Priority: P2)

**Goal**: commands that differ in one plain word become one task with a quoted parameter.

**Independent Test**: quickstart scenario 4.

### Tests for User Story 4

- [X] T049 [P] [US4] Tests for families in `internal/tasksuggest/family_test.go`: the
  TestX/TestY/TestZ set merges into 9 runs with `run`, default `TestX`; a tie picks the
  latest; `--run=TestX` forms take only the value; no flag gives `value`; a keyword or invalid
  flag name gives `value`; a differing first word (`go test` and `npm test`), two differing
  words, a value with quotes, `$`, `*`, a leading `-`,
  or 257 bytes, another directory, other assignments or redirections, and a pipeline do not
  merge; one value alone does not merge; merged groups are not listed alone; a group joins
  one family only (FR-036)
- [X] T050 [P] [US4] Tests for matching a task whose body word is `{{quote(run)}}` or
  `--run={{quote(run)}}` in `internal/tasksuggest/group_test.go`, so an applied family is
  covered (FR-029)
- [X] T051 [P] [US4] Tests for the family listing (values with runs, at most 5 and
  `and N more`) and task text (`go-test run="TestX":` and the quoted body) in
  `internal/tasksuggest/render_test.go`; the body with the default in place of
  `{{quote(run)}}` parses into the same words as the recorded command (SC-007)
- [X] T052 [US4] Extend `test/integration/suggest_test.go` with `TestSuggestFamily`: replay,
  list, apply, then `rune --dry-run go-test 'Test A;rm x'` shows the value as one quoted word
  and `rune analyze` has no warning

### Implementation for User Story 4

- [X] T053 [US4] Implement families in `internal/tasksuggest/family.go` and call them from
  `Build` (research R10)
- [X] T054 [US4] Implement placeholder matching in `internal/tasksuggest/group.go`
  (research R9 step 4)
- [X] T055 [US4] Render `Param` in the listing and the task text in
  `internal/tasksuggest/render.go`

---

## Phase 7: User Story 5 - Only real gaps, and compound commands as review items (Priority: P2)

**Goal**: `cd DIR &&` becomes `[working-directory]`, unsupported shapes are review items with
their reason, and hand-written tasks cover their commands.

**Independent Test**: quickstart scenario 5.

### Tests for User Story 5

- [X] T056 [P] [US5] Tests for normalization in `internal/tasksuggest/group_test.go`:
  `cd web && npm test` runs `npm test` in `web`; `cd a && cd b && x` drops only the first;
  `cd ../x &&`, `cd /abs &&`, `cd $D &&` and `cd` without `&&` are not normalized (FR-027)
- [X] T057 [P] [US5] Tests for classification in `internal/tasksuggest/group_test.go`, one
  case per review reason of contracts/cli.md, and supported cases: pipelines, `&&`, `||`,
  `|&`, redirections, `$NAME` and `${NAME}`, quotes, a prefix assignment (FR-030)
- [X] T058 [P] [US5] Test in `internal/cli/suggest_test.go` with a hand-written `vet` task
  and recorded `go vet ./...`, `cd web && npm test`, `go test $(go list ./...)`: `vet` under
  `Already tasks`, `npm test` with `[working-directory("web")]`, the substitution for review;
  and in `internal/cli/observe_test.go` that an event with `cwd` outside the root counts
  `outside-project` (US5 scenarios 1 to 4)

### Implementation for User Story 5

- [X] T059 [US5] Implement the `cd DIR &&` rule in `internal/tasksuggest/group.go`
  (research R9 step 1)
- [X] T060 [US5] Implement the full classification with the reasons of contracts/cli.md in
  `internal/tasksuggest/group.go` (research R9 step 6)

---

## Phase 8: User Story 6 - Stop observing and delete the records (Priority: P2)

**Goal**: `--remove` takes out exactly Rune's handlers, and `rune suggest forget` deletes
the project's records.

**Independent Test**: quickstart scenario 6.

### Tests for User Story 6

- [X] T061 [P] [US6] Tests for `removeItem` in `internal/agentinit/jsonedit_test.go`: the
  only, first, middle and last item of a list and of an object, on one line and on several,
  with a trailing comma, with comments around; the result parses and decodes to the old value
  without the item
- [X] T062 [P] [US6] Tests for `PlanObserve` with `Remove` in
  `internal/agentinit/observe_test.go`: install then remove leaves the person's hooks and
  settings, decoded, as before (an empty `hooks` may stay); Rune's handler inside the
  person's group removes only that handler; nothing to remove is unchanged; the file is never
  removed (FR-004, SC-005)
- [X] T063 [P] [US6] Tests in `internal/cli/observe_test.go` and
  `internal/cli/suggest_test.go`: `rune agent observe claude --remove` report and
  `--dry-run`; `SuggestForget` reports the count, removes the project's directory, leaves
  the settings and the Runefile, and says `nothing recorded` the second time (FR-046)
- [X] T064 [US6] Extend `test/integration/suggest_test.go` with `TestSuggestOff`: observe
  twice (unchanged, identical bytes), remove with a hook of the person's kept, forget

### Implementation for User Story 6

- [X] T065 [US6] Implement `removeItem` in `internal/agentinit/jsonedit.go`
- [X] T066 [US6] Implement `PlanObserve` with `Remove` in `internal/agentinit/observe.go`:
  one removal per fresh parse until no handler of Rune's is left, then the decoded check
  (research R2)
- [X] T067 [US6] Implement `--remove` in `AgentObserve` in `internal/cli/observe.go`,
  `SuggestForget` in `internal/cli/suggest.go`, and `suggest forget` in
  `cmd/rune/suggest.go`

---

## Phase 9: Polish and cross-cutting concerns

- [X] T068 [P] Write `docs/how-to/learn-tasks-from-agent.md`: turning observation on and
  off, what is stored, where, for how long and how to delete it, the noisy session example of
  the spec's Overview, applying a suggestion, the strict Rune-only profile note, and starting
  the client in the project root (FR-051)
- [X] T069 [P] Add the `rune suggest`, `rune suggest apply`, `rune suggest forget` and
  `rune agent observe` rows and the `rune -- suggest` sentence to `docs/cli.md`; add the page
  to `docs/nav.yaml` and `docs/how-to/README.md`; link it from `docs/how-to/agent-setup.md`
- [X] T070 Run the docs harness `go test ./test/docs/...` and regenerate the site with
  `rune docs-site-gen` if the harness asks for it
- [X] T071 Run `golangci-lint run` in Docker and fix every finding
- [X] T072 Run the full suite and the race suite in Docker, and the coverage of the new and
  changed functions; add tests until each is at 90% or more
- [X] T073 Walk through quickstart.md scenarios 1 to 7 with the built binary and record the
  results
- [X] T074 Check `git status --short --ignored` for fixtures that `.gitignore` hides, and
  chown files Docker created as root

---

## Dependencies and execution order

### Phase dependencies

- Setup (Phase 1): none.
- Foundational (Phase 2): after Setup. Blocks every story.
- US1 (Phase 3): after Foundational.
- US2 (Phase 4): after US1 (it applies what `Build` lists).
- US3 (Phase 5): after US1; independent of US2 except T046, which applies suggestions.
- US4 (Phase 6): after US2 (matching and apply).
- US5 (Phase 7): after US1; T058 uses apply-free listing only.
- US6 (Phase 8): after US1 (installing exists).
- Polish (Phase 9): after the stories it documents.

### Within each story

Tests first and failing, then the implementation in the order listed. Tasks in one file run
one after another.

### Parallel opportunities

- T003 to T010 are in different files.
- T012 and T013 beside T011.
- Within US1, T019 to T026 are in different files; T029 beside T028.
- US3, US5 and US6 can proceed in parallel once US1 is done, except where they share
  `internal/tasksuggest/group.go` or `internal/cli/observe.go`.

## Parallel example: User Story 1

```text
T019 internal/agentinit/observe_test.go
T020 internal/tasksuggest/execution_test.go
T021 internal/tasksuggest/group_test.go
T022 internal/tasksuggest/suggest_test.go
T023 internal/tasksuggest/render_test.go
T024 internal/cli/observe_test.go
T025 internal/cli/suggest_test.go
T026 cmd/rune/agent_test.go, cmd/rune/suggest_test.go
```

## Implementation strategy

### MVP first

Phases 1 to 4: recording with redaction, listing, applying. That alone closes the cold-start
problem for exact repetitions.

### Incremental delivery

Then US3 (declared secrets, review of removed values), US4 (one parameter), US5 (gaps and
review items), US6 (removal and deletion), and the docs.
