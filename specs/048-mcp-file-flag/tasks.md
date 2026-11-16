---

description: "Task list for spec 048: a file flag for the MCP server"
---

# Tasks: A File Flag for the MCP Server

**Input**: Design documents from `/specs/048-mcp-file-flag/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI, and FR-023 and FR-024 ask for them. A test
is written and seen failing before the change that makes it pass. Where a test already
passes before the change, the task says so and says what it guards.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US4)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- `internal/cli` and `mcpserver` are not changed. The product change is in
  `cmd/rune/serve.go`, plus `addCommands` in `cmd/rune/main.go` (T035).
- An end-to-end test starts the built binary with the `mcpProc` helper of
  `test/integration/reload_mcp_test.go`. It shows where a task ran by a file the task
  wrote, never by comparing path text, so it runs the same on Windows. A test that needs a
  symbolic link skips on Windows.
- Every usage error is checked for exit code 2 and for its message.
- A server started without the flag must print and do what it did before (FR-005). No
  existing test is edited to make it pass.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `048-mcp-file-flag` and `specs/048-mcp-file-flag/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/serve-file-flag.md

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The end-to-end tests need to start the server with the flag before the
command name, which the existing helper cannot do.

- [X] T002 In `test/integration/reload_mcp_test.go`, add `startMCPArgs(t, dir, args...)`, which starts `rune args...` in `dir` and completes the handshake, and make `startMCPVersion` call it with `mcp` put first; run the existing `TestReloadMCP_*` tests to show they still pass
- [X] T003 Create `test/integration/file_flag_mcp_test.go` with the fixtures every test of this feature uses: a project directory with a Runefile whose task `mark` writes a file named `ran` in its working directory and prints `marked`, and a separate empty starting directory; plus a helper that says in which of the two directories `ran` exists

**Checkpoint**: a test can start `rune -f PATH mcp` and tell where a task ran.

---

## Phase 3: User Story 1 - An operator serves one project's Runefile from anywhere (Priority: P1)

**Goal**: `rune mcp -f PATH` serves the named Runefile, and tasks run in its directory.

**Independent Test**: From an empty directory, `rune mcp -f` with the absolute path of a
Runefile elsewhere lists that Runefile's tasks, and a called task leaves its file in the
Runefile's directory.

### Tests for User Story 1 (write first, see them fail)

- [X] T004 [P] [US1] In `cmd/rune/serve_test.go`, add `TestServeFileFlag`: the `serve` command has a flag `file` with shorthand `f`, default empty, type `string` and the usage string of contracts/serve-file-flag.md; `ParseFlags` with `-f x` and with `--file=y` sets `opts.File`; given twice, the last value is kept (FR-001)
- [X] T005 [US1] In `test/integration/file_flag_mcp_test.go`, add `TestFileFlagMCP_ServesNamedRunefile`, the test that takes the place of the issue's `TestMCPE2E_Pending_ServeAcceptsFileFlag`: started in the empty directory with `-f` and the absolute path, the tools are the project's, `mark` returns `marked`, and `ran` is in the project directory and not in the starting directory (FR-002, FR-006, FR-023, SC-001)
- [X] T006 [US1] Add `TestFileFlagMCP_Forms` to `test/integration/file_flag_mcp_test.go`: `mcp -f PATH`, `mcp --file=PATH`, `serve --file PATH`, `-f PATH mcp`, `--file PATH serve`, a relative path from a sibling directory, a project directory with a space in its name, a Runefile named `tasks.rune`, and `-f OTHER mcp -f PATH` (last value wins) each give the project's tool list. The paths are those of temporary directories, which on the Windows run of CI have a drive letter. A last case guards what was there before: with a task named `mcp` in the Runefile, `rune -f PATH -- mcp` runs the task and starts no server (FR-001, FR-003, FR-004, SC-002)
- [X] T007 [US1] Add `TestFileFlagMCP_ImportsModulesAndDotenv` to `test/integration/file_flag_mcp_test.go`: a Runefile with `import "shared.rune"`, `mod tools "tools.rune"` and `set dotenv := ".env"` with that file beside it, served from elsewhere, lists the imported task and `tools__lint`, and a task prints the value from `.env` (FR-007)
- [X] T008 [US1] Add `TestFileFlagMCP_ContextRunsInRunefileDirectory` to `test/integration/file_flag_mcp_test.go`: the `[context]` task's output is the `instructions` of the initialize result, and the file it writes is in the project directory (FR-008)
- [X] T009 [US1] Add `TestFileFlagMCP_ReloadFollowsNamedRunefile` and `TestFileFlagMCP_NoReload` to `test/integration/file_flag_mcp_test.go`: saving the named Runefile with a new task sends `notifications/tools/list_changed` and lists the task; with `--no-reload` it does not (FR-009)
- [X] T010 [US1] Add `TestFileFlagMCP_NamedWinsOverLocal` and `TestFileFlagMCP_WithoutFlagUnchanged` to `test/integration/file_flag_mcp_test.go`: in a starting directory that has its own Runefile, `-f` serves the named one; with no flag the local one is served and stderr is exactly `rune MCP server on stdio\n`. The second passes before the change and guards FR-005 (FR-002, FR-005)
- [X] T011 [US1] Add `TestFileFlagMCP_HTTP` to `test/integration/file_flag_mcp_test.go`: `rune serve --http --addr 127.0.0.1:PORT --token-file TOKEN -f PATH`, started in the empty directory, lists the project's tools to an MCP client over HTTP, following the pattern of `TestReloadMCP_HTTPInstructions`
- [X] T012 [US1] Add `TestFileFlagMCP_SymlinkedRunefile` to `test/integration/file_flag_mcp_test.go`, skipped on Windows: a symbolic link to the Runefile in a third directory is served, and `mark` leaves `ran` in the link's directory, as `rune -f` does

### Implementation for User Story 1

- [X] T013 [US1] In `cmd/rune/serve.go`, register `f.StringVarP(&opts.File, "file", "f", "", "serve this Runefile instead of the one found from the working directory")` on the `serve` command's flag set, and run T004 to T012 until they pass

**Checkpoint**: a user-level client configuration with an absolute path works.

---

## Phase 4: User Story 2 - The server still refuses blanket approval (Priority: P1)

**Goal**: The server has one new flag and no other.

**Independent Test**: `--yes` in every position, with and without `-f`, stops the server
with exit code 2 and `unknown flag: --yes`, and nothing runs.

### Tests for User Story 2

- [X] T014 [P] [US2] In `cmd/rune/serve_test.go`, add `TestServeHasNoRootFlags`: for every flag registered on the root command's own flag set, the `serve` command has no flag of that name, except `file` and `ignore-version`; and add `TestOnlyServeHasFileFlag`: `analyze`, `lsp`, `version` and `completion` have no `file` flag. The root's flags are read with `LocalNonPersistentFlags`, so `--color`, which every command inherits, is not among them. Both fail if a later change makes root flags persistent (FR-011, FR-013)
- [X] T015 [US2] Add `TestFileFlagMCP_YesStaysUnknown` to `test/integration/file_flag_mcp_test.go`: `mcp --yes`, `serve --yes`, `--yes mcp`, `mcp -f PATH --yes`, `mcp --yes -f PATH`, `-f PATH --yes mcp`, `-f PATH mcp --yes` and `--yes -f PATH serve`, against a Runefile whose `[context]` task writes a file, started in an empty directory and in the project, each exit 2 with stderr `rune: unknown flag: --yes` and leave no file. Also `--quiet mcp -f PATH`, `mcp -f PATH --dry-run`, `mcp -f PATH --set=A` and `-f PATH --watch mcp`, which name their own flag. `--set A B mcp -f PATH` is not among them: Cobra reads `B` as a task name, so the root command would run tasks instead of starting the server (FR-010, FR-011, FR-017, SC-003)
- [X] T016 [US2] Add `TestFileFlagMCP_ConfirmStillNeedsOptIn` to `test/integration/file_flag_mcp_test.go`: a `[confirm]` task of a Runefile served with `-f` is refused without `--allow-destructive` and runs with it by name (FR-012)
- [X] T017 [US2] In `cmd/rune/serve_test.go`, extend the table of `TestServeRejectsYes` with `{"mcp", "-f", "x", "--yes"}` and `{"-f", "x", "--yes", "mcp"}`

### Implementation for User Story 2

- [X] T018 [US2] Run T014 to T017 against the code of T013 and confirm they pass with no further product change. Then show that they can fail: in a scratch edit of `cmd/rune/root.go`, register `--yes` and `--file` as persistent flags, see `TestServeHasNoRootFlags`, `TestOnlyServeHasFileFlag` and `TestFileFlagMCP_YesStaysUnknown` fail, and restore the file

**Checkpoint**: the security property of spec 031 is pinned by tests that name every root
flag.

---

## Phase 5: User Story 3 - A mistake in the path is reported, not worked around (Priority: P2)

**Goal**: A bad value stops the server with its own message and never serves another
Runefile.

**Independent Test**: In a directory with its own Runefile, `-f` with a missing path, a
directory and an empty value each exit 2 with a message, and the local Runefile's
`[context]` task never runs.

### Tests for User Story 3 (write first, see them fail)

- [X] T019 [P] [US3] In `cmd/rune/serve_test.go`, add `TestServeFileFlagEmptyValue`: executing the `serve` command with `--file ""`, with `-f ""` and with `--file=` returns a `*cli.UsageError` whose message is `--file needs a path` (FR-016)
- [X] T020 [US3] Add `TestFileFlagMCP_Mistakes` to `test/integration/file_flag_mcp_test.go`, started in a directory whose own Runefile has a `[context]` task that writes a file: an empty value gives `rune: --file needs a path`; a path that names nothing gives a message that holds the path; a directory gives `rune: --file points at a directory, not a Runefile`; `-f` with no value gives `flag needs an argument`. Each exits 2, prints nothing on stdout, and leaves no file (FR-014 to FR-017, SC-004)
- [X] T021 [US3] Add `TestFileFlagMCP_StaticErrorSameAsLocal` to `test/integration/file_flag_mcp_test.go`: a Runefile with an unknown dependency gives the same exit code and the same stderr when served with `-f` from elsewhere as when served from its own directory (FR-018)
- [X] T022 [US3] Add `TestFileFlagMCP_UnknownTaskNamesTheNamedRunefile` to `test/integration/file_flag_mcp_test.go`: `mcp -f PATH --allow nosuch` exits 2 and suggests a task of the named Runefile, not of the local one

### Implementation for User Story 3

- [X] T023 [US3] In `cmd/rune/serve.go`, at the top of `RunE`, return `&cli.UsageError{Err: errors.New("--file needs a path")}` when `cmd.Flags().Changed("file")` and `opts.File` is empty, and run T019 to T022 until they pass

**Checkpoint**: a stale or empty path is visible in the client's server log.

---

## Phase 6: User Story 4 - The documentation shows a user-level setup (Priority: P2)

**Goal**: The flag is in the help, the guide, the command reference and the
troubleshooting page.

**Independent Test**: Each of the four mentions the flag, and the guide has a user-level
example.

### Tests for User Story 4 (write first, see them fail)

- [X] T024 [P] [US4] In `cmd/rune/serve_test.go`, add `TestServeHelpMentionsFile`: the command's long text holds `--file` and says tasks run in the Runefile's directory, and its examples hold `rune mcp -f /path/to/project/Runefile` (FR-019)
- [X] T025 [P] [US4] In `test/integration/cli_help_test.go`, add `-f, --file string` to the strings `rune serve --help` must hold

### Implementation for User Story 4

- [X] T026 [US4] In `cmd/rune/serve.go`, add a paragraph on the flag to `Long` and the example line to `Example`, and run T024 and T025 until they pass (FR-019)
- [X] T027 [P] [US4] In `docs/mcp.md`, "Starting the server", add "Serving a Runefile from another directory": the flag with an absolute path, where tasks run, the two kinds of user-level setup of research.md R7 with an example for Claude Code and for Codex CLI, one server per project under its own name, and that an empty or wrong path stops the server; correct the Claude Code and Codex CLI "Configuration" bullets of "Client compatibility" where they describe a user-level server (FR-020, SC-006)
- [X] T028 [P] [US4] In `docs/cli.md`, add `[-f <path>]` and one sentence to the `rune serve` row (FR-021)
- [X] T029 [P] [US4] In `docs/troubleshooting.md`, "no Runefile found", add the case of a server started by a client outside the project, with `rune mcp -f` as the fix and a link to the guide (FR-022)

**Checkpoint**: a reader who meets `no Runefile found` in a client's log finds the fix.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T030 Run `gofmt -l cmd test` and the linter CI uses on the whole module, and fix what they report
- [X] T031 Run the tests of the three touched packages with the race detector: `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./cmd/rune/... ./test/integration/... ./test/docs/...`
- [X] T032 Measure coverage of `cmd/rune` and report the figure for `newServeCmd`
- [X] T033 Walk quickstart.md steps 2 to 5 by hand with a binary built from the branch
- [X] T034 Run the full suite, `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`, and report the result (SC-005, SC-007)
- [X] T035 From the first review: move the registration of the built-in commands in `cmd/rune/main.go` into `addCommands(root, opts)` and build the tree of `TestServeHasNoRootFlags` and `TestOnlyServeHasFileFlag` with it, so a command added later is checked too; wait for the startup line before comparing stderr in `TestFileFlagMCP_WithoutFlagUnchanged`; share `connectHTTP` between the two HTTP tests; say in `docs/mcp.md` and `docs/cli.md` that `--yes` is not a server flag, where they said `-f` is the only shared flag

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: done.
- **Foundational (Phase 2)**: blocks every end-to-end test.
- **US1 (Phase 3)**: after Foundational. Its implementation task, T013, is the flag
  itself, so US2 and US3 come after it.
- **US2 (Phase 4)**: after T013. It adds tests and no product code.
- **US3 (Phase 5)**: after T013. T023 is the second and last product change in `RunE`.
- **US4 (Phase 6)**: T024 to T026 after T013. T027 to T029 are documentation and can be
  written any time after the contract.
- **Polish (Phase 7)**: after all stories.

### Within Each Story

- Tests before the implementation task of the story.
- The end-to-end tests of one story share `test/integration/file_flag_mcp_test.go`, so
  they are written in order.

### Parallel Opportunities

- T004, T014, T019 and T024 are unit tests in `cmd/rune/serve_test.go`. Each is marked
  [P] against the end-to-end tests of its story, which are in another file.
- T027, T028 and T029 are three documentation files.

## Parallel Example: User Story 4

```text
Task: "docs/mcp.md: Serving a Runefile from another directory"
Task: "docs/cli.md: the rune serve row"
Task: "docs/troubleshooting.md: no Runefile found"
```

## Implementation Strategy

### MVP First (User Stories 1 and 2)

1. Phase 2: the helper.
2. Phase 3: the tests of the flag, then the one line that adds it.
3. Phase 4: the tests that the server gained nothing else.
4. Stop and check: `rune mcp -f PATH` works and `rune mcp --yes` is still an error. This
   is the issue's first three acceptance criteria.

### Incremental Delivery

1. US1 and US2: the flag, and its limit.
2. US3: the empty value.
3. US4: help and documentation.
4. Polish: lint, race, coverage, the whole suite.

All four stories ship in one pull request.
