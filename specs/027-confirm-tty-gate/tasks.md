---

description: "Task list for spec 027: interactive-only approval for [confirm] tasks"
---

# Tasks: Interactive-Only Approval for `[confirm]` Tasks

**Input**: Design documents from `/specs/027-confirm-tty-gate/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/confirm-cli.md](contracts/confirm-cli.md), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI makes development test-first, so every story writes its tests before the implementation and confirms they fail (US2 tests are regression guards and may pass as soon as Phase 2 lands).

**Organization**: Tasks are grouped by user story so each story can be implemented and tested on its own.

**Test runs**: Always in Docker: `docker-compose run --rm test go test ./internal/cli/... ./test/integration/...` (never on the host).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 to US4)

---

## Phase 1: Setup

**Purpose**: Branch and the one test-only dependency

- [X] T001 Create and switch to branch `027-confirm-tty-gate` from `main` (`git checkout -b 027-confirm-tty-gate`); the untracked artifacts in specs/027-confirm-tty-gate/ come along
- [X] T002 Add the test-only dependency `github.com/creack/pty` v1.1.24 to go.mod and go.sum with `docker-compose run --rm test go get github.com/creack/pty@v1.1.24` (research R8); it must only ever be imported from `_test.go` files

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Stdin classification, the engine field, and a three-way `confirm` that still behaves exactly as today

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 [P] Write failing unit tests in new internal/cli/terminal_test.go: `isTerminal(nil)`, `isTerminal(strings.NewReader("y"))`, an `*os.File` for a regular temp file, and the read end of `os.Pipe()` all return false (research R1)
- [X] T004 Create internal/cli/terminal.go: move `isTTY(fd uintptr) bool` out of internal/cli/choose.go and add `isTerminal(stream any) bool` (true only for an `*os.File` whose `Fd()` passes `isTTY`); rewrite `interactiveTerminal` in internal/cli/choose.go as `isTerminal(opts.Stdin) && isTerminal(opts.Stdout)` and drop the imports choose.go no longer needs; T003 and internal/cli/choose_test.go pass
- [X] T005 Add unexported field `stdinTTY bool` to the `engine` struct in internal/cli/run.go (doc comment: computed once per invocation; false for the MCP adapter, which passes no stdin) and set it in `execute()` as `stdinTTY: isTerminal(mopts.Stdin)` (research R2); do not touch internal/cli/mcp.go
- [X] T006 Create internal/cli/confirm_test.go with a helper `confirmEngine(t *testing.T, src string, stdin io.Reader, tty, yes bool) (*engine, *bytes.Buffer, *bytes.Buffer)` returning the engine plus stdout and stderr buffers, modeled on `availEngine` in internal/cli/availability_test.go, that sets `opts.Stdin`, `opts.Yes`, `stdinTTY`, and `plan: planRun`; plus a `failReader` type whose `Read` calls `t.Errorf("stdin was read")` and returns `0, io.EOF`
- [X] T007 Move `(*engine).confirm` from internal/cli/run.go into new internal/cli/confirm.go and make it return a `confirmOutcome` (`confirmApproved`, `confirmDeclined`, `confirmNoTerminal`) instead of `bool`, and extract `confirmQuestion(task *ast.Task, prompt string) string` (the prompt, or `Run "<task>"?` when empty). Behavior stays identical in this task: `nil` stdin → declined; otherwise prompt and read as today. Update both call sites (`Execute`, `ExecuteFailHook`) so every non-approved outcome keeps today's silent `task "<name>" was not confirmed` failure

**Checkpoint**: `docker-compose run --rm test go test ./internal/cli/... ./test/integration/...` is green with no behavior change

---

## Phase 3: User Story 1 - Piped input cannot approve a destructive task (Priority: P1) 🎯 MVP

**Goal**: Without `--yes`, a `[confirm]` task declines whenever stdin is not a terminal, without reading stdin, whatever it contains

**Independent Test**: `echo y | rune apply`, `yes | rune apply`, `rune apply < file-with-yes`, and `rune apply </dev/null` all exit 1 and never print the body's output

### Tests for User Story 1 ⚠️

> Write these first and confirm they FAIL before T011

- [X] T008 [US1] Add unit tests to internal/cli/confirm_test.go for the non-terminal branch using `confirmEngine` with `tty=false` and `failReader` stdin on `[confirm]\nt:\n    @echo body`: `Execute` returns a `*TaskFailure`, `errors.Is(err, scheduler.ErrBodyNotRun)` holds, stdout is empty, stderr does not contain `[y/N]`, stdin is never read; `ExecuteFailHook` declines the same way without reading; with `nil` stdin the error text stays exactly `task "t" was not confirmed` and `Silent` stays true (MCP parity, FR-010); with `yes=true` and `failReader` the body runs and stdin is never read (FR-006)
- [X] T009 [P] [US1] Create test/integration/confirm_test.go with a local helper `runWithInput(t *testing.T, dir string, stdin io.Reader, args ...string) result` (8s timeout, same shape as `runWithStdin` in test/integration/harness_test.go) and binary tests that each assert the body's output is absent and the exit code is 1: piped `y\n`; piped `YES\n`; an unbounded reader repeating `y\n`; an `*os.File` of a temp file containing `yes\n`; empty stdin. Also: `deploy: apply` with `[confirm]` on `apply` and piped `y\n` prints neither `would-apply` nor `deployed`; `release: build publish` with only `publish` marked `[confirm]` prints `build`'s output but not `publish`'s and exits 1 (Clarification Q1); `fails: || diag` where `fails` runs `exit 1` and `diag` is `[confirm]` never prints `diag`'s output and exits 1; `ship: && notify` where only `notify` is `[confirm]` prints `ship`'s output but not `notify`'s and exits 1 (success-hook path, FR-007)
- [X] T010 [US1] Add binary tests to test/integration/confirm_test.go for unchanged surfaces: piped `y\n` with `--dry-run deploy` and with `--summary deploy` exits 0 and stderr has no `rune:` banner (FR-010); `rune --yes echo-stdin` where `echo-stdin` is `[confirm]` running `cat` and stdin is `payload` prints exactly `payload` (SC-004; `t.Skip` when `exec.LookPath("cat")` fails, as test/integration/us3_test.go does for python3)

### Implementation for User Story 1

- [X] T011 [US1] In `(*engine).confirm` in internal/cli/run.go return `confirmNoTerminal` when `e.opts.Stdin != nil && !e.stdinTTY`, before printing the prompt or creating a reader (FR-003)
- [X] T012 [US1] Add `noTerminalError(task *ast.Task, prompt string) error` in internal/cli/run.go producing exactly `task "<name>" was not confirmed (<question>): stdin is not a terminal; pass --yes to approve`, where `<question>` comes from `confirmQuestion` (contracts/confirm-cli.md §2). Map `confirmNoTerminal` in `Execute` to `&TaskFailure{Err: &bodyNotRunError{err: noTerminalError(...)}, Silent: true}` and in `ExecuteFailHook` to `&TaskFailure{Err: noTerminalError(...), Silent: true}` (US3 makes the `Execute` one non-silent). T008 to T010 pass

**Checkpoint**: Issue #30 is fixed: piped input can no longer approve; `--yes`, plan modes, and MCP behave as before

---

## Phase 4: User Story 2 - A person at a terminal still approves interactively (Priority: P1)

**Goal**: Prove the interactive prompt is unchanged when stdin is a real terminal

**Independent Test**: In an interactive shell, `rune apply` answered `y` runs the task; answered `n`, an empty line, or EOF declines it; `rune apply > out.log` answered `y` still runs it

### Tests for User Story 2 ⚠️

> Regression guards: they may pass as soon as Phase 2 is in. A failure means the real stdin is not reaching `stdinTTY`

- [X] T013 [P] [US2] Create internal/cli/confirm_interactive_test.go using `confirmEngine` with `tty=true`: stdin `y\n`, `yes\n`, `YES\n`, and `  y  \n` run the body and stderr contains `Run "t"? [y/N] `; stdin `n\n`, `\n`, and empty (EOF) return a `*TaskFailure` with `Silent` true, `errors.Is(err, scheduler.ErrBodyNotRun)`, no body output, and error text `task "t" was not confirmed`; `[confirm("Sure?")]` prints `Sure? [y/N] `
- [X] T014 [P] [US2] Create test/integration/confirm_pty_test.go with build tag `//go:build !windows` and a helper `runWithPTY(t *testing.T, dir, answer string, args ...string) result` that opens a pseudo-terminal with `pty.Open()`, gives the child the tty as stdin and pipes for stdout and stderr, closes the tty in the parent after `Start`, writes `answer` to the master, drains the master into `io.Discard` until the child exits, and enforces an 8s timeout. Tests: `y\n` prints `would-apply` and exits 0; `n\n` exits 1 with no `rune:` banner; with `--color=never` stderr contains `Apply to live infrastructure? [y/N] ` (stdout is a pipe throughout, covering Story 2 scenario 3)
- [X] T015 [US2] Move `TestConfirmPromptStyled` from test/integration/status_styling_test.go into test/integration/confirm_pty_test.go, replacing `runWithStdin(t, dir, "n\n", ...)` with `runWithPTY(t, dir, "n\n", ...)` and keeping every assertion (prompt shape `Run "danger"? [y/N] `, ANSI when styled, stripped styled equals plain, plain `[y/N]` suffix, body not run)

### Implementation for User Story 2

- [X] T016 [US2] Run T013 to T015 in Docker; if any fails, fix the stdin classification wiring in `execute()` in internal/cli/run.go (the decision must use the unwrapped `*os.File` stdin) rather than changing the tests

**Checkpoint**: Interactive approval proven on a real terminal (Linux in Docker; macOS in CI); Windows covered by T013 and the shared `isTTY`

---

## Phase 5: User Story 3 - A declined non-interactive run explains how to approve (Priority: P2)

**Goal**: A non-interactive decline ends with one `rune:` banner naming the task, its question, the reason, and `--yes`

**Independent Test**: `echo y | rune apply` prints exactly `rune: task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve` and no `[y/N]`

**Depends on**: US1 (T011, T012)

### Tests for User Story 3 ⚠️

> Write these first and confirm they FAIL before T019

- [X] T017 [P] [US3] Create internal/cli/confirm_message_test.go: with `tty=false`, `Execute` returns a `*TaskFailure` with `Silent == false`; `BannerMessage(err)` equals `task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve` for `[confirm("Apply to live infrastructure?")]` and `task "clean" was not confirmed (Run "clean"?): stdin is not a terminal; pass --yes to approve` for bare `[confirm]`; a task carrying both `[confirm]` and `[no-exit-message]` still yields a non-empty banner (research R4)
- [X] T018 [P] [US3] Create test/integration/confirm_message_test.go: piped `y\n` with `--color=never` gives stderr exactly equal to the contract §2 line plus a newline and containing no `[y/N]`; `--color=always` gives ANSI output with `stripANSI(styled.stderr) == plain.stderr` and equal exit codes (FR-011); `fails: || diag` with `[confirm]` on `diag` gives stderr containing `warning: failure hook diag failed: task "diag" was not confirmed (Run "diag"?): stdin is not a terminal; pass --yes to approve` exactly once (contract §2a)

### Implementation for User Story 3

- [X] T019 [US3] In `Execute` in internal/cli/run.go set `Silent: false` on the `confirmNoTerminal` failure so cmd/rune/main.go prints it through `BannerLine`; leave the `ExecuteFailHook` variant as is (the scheduler's `Warnf` already renders it); note in the comment that `[no-exit-message]` does not apply because the body never started. T017 and T018 pass

**Checkpoint**: Callers that hit the decline learn the fix from one line

---

## Phase 6: User Story 4 - Documentation and release notes state the rule (Priority: P3)

**Goal**: Every CLI-facing description of `[confirm]` and `--yes` states the terminal rule; the release notes flag the breaking change

**Independent Test**: `rune --help`, docs/cli.md, docs/runefile.md, and the `[confirm]` editor hover each state that interactive approval needs a terminal and `--yes` approves otherwise

- [X] T020 [P] [US4] Change the `--yes` flag help in cmd/rune/root.go to `approve [confirm] tasks without prompting (required when stdin is not a terminal)` (contracts/confirm-cli.md §5)
- [X] T021 [P] [US4] Change the `confirm` entry's `Documentation` in internal/language/builtin.go to `Ask for confirmation at a terminal before running; --yes approves non-interactively (maps to the MCP destructive hint).`
- [X] T022 [P] [US4] Update the `--yes` row in docs/cli.md: it approves `[confirm]` tasks without prompting, it is required when stdin is not a terminal, and piped or redirected input never approves; keep the `rune --yes clean` example
- [X] T023 [P] [US4] In docs/runefile.md change the comment on `[confirm("Really clean?")]` to `# prompt at a terminal before running (--yes approves otherwise)` and add a short paragraph after the attribute block: the prompt is answered only from a terminal; with piped or redirected stdin the task is declined with a message naming `--yes`; the MCP server gates `[confirm]` separately (link to mcp.md)
- [X] T024 [US4] Re-read docs/examples/ci-cd/README.md, docs/examples/agent-driven/README.md, and docs/use-cases/mcp-agents.md and confirm none describes approving `[confirm]` by piping input (SC-006); edit only if one does
- [X] T025 [US4] Use `fix(cli)!: breaking: [confirm] needs a terminal, piped input no longer approves (use --yes)` as the squash-merge PR title. git-cliff (cliff.toml) renders only the description, so the line in CHANGELOG.md and the GitHub Release notes reads "Breaking: [confirm] needs a terminal, piped input no longer approves (use --yes)" (FR-013, research R10); check it with `git cliff --unreleased` before the release

**Checkpoint**: Docs, help, hover, and release notes all agree with the contract

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T026 Run `rune lint` (gofmt, go vet, golangci-lint) and fix findings in internal/cli/terminal.go, internal/cli/run.go, internal/cli/choose.go, and the new test files
- [X] T027 Run `rune test`, `rune test-race`, and `rune docs-check` (all in Docker) and confirm everything passes, including the untouched internal/cli/mcp_test.go and internal/cli/failhook_test.go (FR-010)
- [X] T028 Walk through manual scenarios A to E in specs/027-confirm-tty-gate/quickstart.md with a locally built binary. Windows (SC-002): automated coverage is T013 plus the shared `isTTY` helper; a manual run of scenario B in PowerShell, cmd.exe, and Git Bash is recommended before the release that ships this, and its result (done, or skipped with reason) goes in the PR description
- [X] T029 Draft a separate GitHub issue for the MCP `--yes` defect in specs/027-confirm-tty-gate/research.md R11 (`rune mcp --yes` allows the call, then `mcpAdapter.Call` in internal/cli/mcp.go declines it); file it only after the maintainer approves

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none
- **Foundational (Phase 2)**: after Setup; blocks every story
- **US1 (Phase 3)**: after Phase 2
- **US2 (Phase 4)**: after Phase 2; independent of US1
- **US3 (Phase 5)**: after US1 (reuses `confirmNoTerminal` and `noTerminalError`)
- **US4 (Phase 6)**: after Phase 2; independent of the code stories
- **Polish (Phase 7)**: after all stories

### Within Phases

- T003 before T004; T004 before T005; T005 before T006 and T007
- T008 to T010 written and failing before T011; T011 before T012
- T014 before T015 (`runWithPTY` must exist)
- T017 and T018 failing before T019
- T002 before T014 (pty dependency)

### Parallel Opportunities

- T003 alongside T001 and T002
- Once Phase 2 is done: US1, US2, and US4 can proceed at the same time
- Within US1: T009 alongside T008
- Within US2: T013 alongside T014
- Within US3: T017 alongside T018
- Within US4: T020, T021, T022, T023 all at once

---

## Parallel Example: after Phase 2

```text
Task: "T008 [US1] non-terminal unit tests in internal/cli/confirm_test.go"
Task: "T009 [US1] binary tests in test/integration/confirm_test.go"
Task: "T013 [US2] interactive unit tests in internal/cli/confirm_interactive_test.go"
Task: "T014 [US2] pty tests in test/integration/confirm_pty_test.go"
Task: "T020 [US4] --yes help text in cmd/rune/root.go"
Task: "T021 [US4] hover text in internal/language/builtin.go"
Task: "T022 [US4] docs/cli.md --yes row"
Task: "T023 [US4] docs/runefile.md [confirm] paragraph"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 and Phase 2
2. Phase 3 (US1): the security fix for issue #30
3. Validate with quickstart scenario A; at this point piped input can no longer approve

### Incremental Delivery

1. Setup + Foundational: no behavior change, all green
2. US1: piped approval closed (MVP)
3. US2: interactive approval proven on a real terminal
4. US3: the decline explains itself via the `rune:` banner
5. US4: docs, help, hover, release-note title
6. Polish: lint, full Docker suite, quickstart walk-through, separate MCP issue

Ship US1 to US4 in one PR: US1 alone changes behavior that US4 documents, and US3's message is the migration hint for broken scripts.

---

## Notes

- [P] tasks touch different files and have no unfinished dependencies
- Never run `go test` on the host; use the Docker commands above
- `creack/pty` may only appear in `_test.go` files with the `!windows` tag
- MCP code (internal/cli/mcp.go, mcpserver/) must not change in this feature
