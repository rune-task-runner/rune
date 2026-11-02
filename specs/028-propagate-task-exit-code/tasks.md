---

description: "Task list for spec 028: propagate the task body's exit code"
---

# Tasks: Propagate the Task Body's Exit Code

**Input**: Design documents from `/specs/028-propagate-task-exit-code/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/exit-codes.md](contracts/exit-codes.md), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI makes development test-first, so every story writes its tests before the implementation and confirms they fail. US2 and US3 add no production code: their tests go red until US1's `CodeFor` change (T017) lands.

**Organization**: Tasks are grouped by user story so each story can be implemented and tested on its own.

**Test runs**: Always in Docker, never on the host: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/... ./test/integration/...` (add `-e CGO_ENABLED=1` and `-race` for the race run). The repo Runefile tasks `rune test`, `rune test-race`, `rune docs-check`, and `rune lint` wrap the same harness.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 to US4)

---

## Phase 1: Setup

**Purpose**: Branch and a green baseline

- [X] T001 Create and switch to branch `028-propagate-task-exit-code` from `main` (`git checkout -b 028-propagate-task-exit-code`); the untracked artifacts in specs/028-propagate-task-exit-code/ come along
- [X] T002 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/cli/... ./internal/runtime/... ./test/integration/... ./test/docs/...` and confirm it is green before any change, so later failures are attributable to this feature

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: One exported function that decides a failure's body exit code, shared by the hook context and the process exit code (research R1, R2)

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 Write failing table test `TestBodyExitCode` in internal/runtime/scheduler/scheduler_test.go for `BodyExitCode(err error) int`: `&shell.ExecError{Code: 2}` → 2; `&interp.ExecError{Code: 4}` → 4; `&interp.ExecError{Code: 300}` → 300 (Windows can exceed 255, data-model "Body exit code"); `&interp.ExecError{Code: -1}` → 1; `&shell.ExecError{Err: errors.New("parse error")}` (Code 0) → 1; each ExecError wrapped once with `fmt.Errorf("run: %w", err)` → its code; `errors.New("not confirmed")` → 1; `&agent.NotInstalledError{Name: "x"}` → 1. It fails to compile until T004 (red)
- [X] T004 In internal/runtime/scheduler/scheduler.go rename `failureExitCode` (line 242) to exported `BodyExitCode(err error) int`, change both checks from `Code != 0` to `Code > 0` (data-model validation rule: "only a value above 0 counts as a body exit code"), rewrite its doc comment to say it is the single source for both `RUNE_FAILED_EXIT_CODE` and the CLI/MCP exit code (spec 028 FR-009), and update the call in `runFailHooks` (line 175). T003 and internal/runtime/scheduler/failhook_test.go pass

**Checkpoint**: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/scheduler/...` is green; no user-visible behavior has changed yet

---

## Phase 3: User Story 1 - A script branches on the task's own exit code (Priority: P1) MVP

**Goal**: `rune <task>` exits with the failing body's own code for shell and interpreter bodies; rune's own codes 0, 2, 3, 130 are unchanged; failures without a body status still exit 1

**Independent Test**: A task whose body runs `exit N` makes `rune <task>` exit N for N in 1, 2, 3, 42, 126, 127, 130, 255; `rune quiet` (`[no-exit-message]`, `exit 6`) prints no banner and exits 6

### Tests for User Story 1

> Write these first and confirm they fail before T017 and T018

- [X] T005 [P] [US1] Create internal/cli/exit_test.go with table test `TestCodeFor` covering data-model rows 1 to 7: `nil` → 0; `&UsageError{}` → 2; `&ValidationError{}` → 3; `&Interrupted{}` → 130; `&TaskFailure{Err: fmt.Errorf("slow: %w", context.Canceled), Silent: true}` → 130 (row 4, research R13); `&TaskFailure{Err: &shell.ExecError{Code: 2}}` → 2; `&TaskFailure{Err: &interp.ExecError{Code: 4}}` → 4; `&TaskFailure{Err: &shell.ExecError{Code: 130}}` → 130 (FR-004, no remap); `&TaskFailure{Err: &shell.ExecError{Code: 6}, Silent: true}` → 6 (FR-008); `&TaskFailure{Err: errors.New("task \"x\" was not confirmed")}` → 1; `&TaskFailure{Err: &agent.NotInstalledError{Name: "x"}}` → 1; `&TaskFailure{Err: &shell.ExecError{Err: errors.New("parse")}}` → 1; `errors.New("other")` → 2
- [X] T006 [P] [US1] Add `TestRunCancelledReturnsContextError` to internal/runtime/interp/interp_test.go: `Run` with `[]string{"sh"}` and script `sleep 5\n` under a context cancelled after 100ms (`context.WithCancel` plus `time.AfterFunc`) returns an error where `errors.Is(err, context.Canceled)` is true and `errors.As(err, new(*ExecError))` is false (research R4); skip with `t.Skip` when `exec.LookPath("sleep")` fails, matching the existing `sh`-based tests in that file
- [X] T007 [P] [US1] Create internal/runtime/interp/interp_signal_test.go with `//go:build !windows`: `Run(context.Background(), "sig", "kill -TERM $$\n", []string{"sh"}, token.Span{}, Options{})` returns `*ExecError` with `Code == 143` (128 + SIGTERM, research R3, FR-007)
- [X] T008 [US1] Add `TestNoExitMessageInterruptExits130` to internal/cli/exit_test.go (after T005, same file): write a temp Runefile `set shell := ["sh"]\n\n[no-exit-message]\nslow:\n    exec sleep 5\n` (the `set shell` override runs the body under the interpreter executor, because the bundled shell keeps a goroutine asleep for 2s after a cancel, which this package's goleak check reports; `exec` leaves no orphaned `sleep` holding the stdout pipe), call `Run(Options{Cwd: dir, Ctx: ctx, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, []string{"slow"})` (see internal/cli/choose_test.go for building `Options`) with `ctx` cancelled after 100ms, and assert `CodeFor(err) == ExitInterrupt` and `BannerMessage(err) == ""` (banner still suppressed, FR-011); skip when `sh` or `sleep` is not on PATH. Today it exits 1 because internal/cli/run.go:473 wraps the cancelled body in a silent `TaskFailure` (research R13)
- [X] T009 [US1] Create test/integration/exit_code_test.go with `TestExitCode_BodyCodePassesThrough`: for each N in 1, 2, 3, 42, 126, 127, 130, 255 (SC-001), `writeRunefile(t, "t:\n    @exit N\n")`, `run(t, dir, nil, "--color=never", "t")`, assert `r.code == N` and that `r.stderr` contains `rune: task "t" failed at "exit N": exit status N` (FR-001, FR-004, FR-011)
- [X] T010 [US1] Add to test/integration/exit_code_test.go: `TestExitCode_StopsAtFailingLine` (body `@echo first`, `@exit 5`, `@echo never` → code 5, stdout has `first`, lacks `never`); `TestExitCode_NoExitMessage` (`[no-exit-message]` body `@exit 6` → code 6, empty stderr, FR-008); `TestExitCode_ContinueOnError` (body `-exit 3` then `@echo ok` → code 0, stdout has `ok`); `TestExitCode_InterpreterBody` (`pyfail (python):\n    import sys; sys.exit(4)\n` → code 4; skip when `exec.LookPath("python3")` fails, as test/integration/us3_test.go does)
- [X] T011 [US1] Add `TestExitCode_HookSeesSameCode` to test/integration/exit_code_test.go: Runefile `hooked: || diagnose\n    @exit 3\ndiagnose:\n    @echo "code=$RUNE_FAILED_EXIT_CODE"\n`; `rune hooked` exits 3 and stdout contains `code=3` (FR-009)
- [X] T012 [P] [US1] Create test/integration/exit_code_signal_test.go with `//go:build !windows` (FR-007 end to end): `TestExitCode_ShellBodyKilledBySignal` runs a task whose body is `@sh -c 'kill -TERM $$'` (single quotes, so the inner `sh` expands `$$` to its own PID) and asserts exit 143; mvdan already reports 128 + S (research R5), so this needs only T017. `TestExitCode_InterpreterBodyKilledBySignal` runs `sigpy (python):\n    import os, signal; os.kill(os.getpid(), signal.SIGTERM)\n` and asserts exit 143, skipping when `exec.LookPath("python3")` fails; it fails until T018
- [X] T013 [P] [US1] In test/integration/us1_test.go `TestUS1_FailingTaskExitsNonZero` (line 133), assert `r.code == 7` for the `exit 7` body, message `exit = %d, want 7 (the body's code)` (research R8)
- [X] T014 [P] [US1] In test/integration/error_banner_test.go change the three exit assertions for `exit 7` bodies (lines 21 to 22, 44 to 45, 57 to 58) from 1 to 7, and update the `TestBannerPlainInvariant` doc comment ("with exit 1") to say exit 7 (research R8)
- [X] T015 [P] [US1] In test/integration/failure_hooks_cli_test.go change `TestFailureHooks_CLIHeaderAndExitCode` (line 24) and the hook-fails test (line 49) to want 7, keeping the reasons in the messages ("unchanged by hooks", "original failure, not the hook's") (research R8, FR-009)
- [X] T016 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/cli/... ./internal/runtime/interp/... ./test/integration/...` and confirm T005 to T015 fail for the expected reason (code 1 instead of N, -1 or 137 instead of 143 or 130), not for compile errors other than missing symbols. Only `TestExitCode_ContinueOnError` (T010) is expected to pass already; it is a regression guard

### Implementation for User Story 1

- [X] T017 [US1] In internal/cli/exit.go change `CodeFor` to check, in order: nil → 0, `UsageError` → 2, `ValidationError` → 3, `Interrupted` → 130, then `errors.Is(err, context.Canceled)` → 130 (research R13, with one comment saying a `[no-exit-message]` task's silent `TaskFailure` can hold the cancellation), then `*TaskFailure` → `scheduler.BodyExitCode(err)` instead of `ExitTaskFail`, then default 2 (data-model decision table). Update comments: the constant block cites spec 028 contracts/exit-codes.md alongside spec 001; `ExitTaskFail` reads "a task failed without a body exit code"; the `TaskFailure` doc says it exits with the wrapped body's code, or 1 when there is none. Import `context` and `github.com/rune-task-runner/rune/internal/runtime/scheduler`. Makes T005, T008, T009 to T011, T013 to T015 pass
- [X] T018 [P] [US1] In internal/runtime/interp/interp.go `Run` (lines 93 to 102): after `cmd.Run` fails, return `fmt.Errorf("%s: %w", taskName, ctx.Err())` when `ctx.Err() != nil`, replacing the `errors.Is(err, context.Canceled)` check that never fires for a killed child (research R4); for `*exec.ExitError`, when `ee.Sys()` satisfies `interface{ Signaled() bool; Signal() syscall.Signal }` and `Signaled()` is true, set `Code: 128 + int(Signal())`, otherwise `ee.ExitCode()` (research R3). No build tags. Add one comment on why the context check comes first (`Cmd.Wait` prefers the `ExitError` over the context error). Makes T006, T007, and the interpreter case of T012 pass
- [X] T019 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/... ./test/integration/...` and confirm green, including the unchanged tests that pin 1 for failures without a body status: test/integration/confirm_test.go, confirm_message_test.go, confirm_pty_test.go, us3_test.go `TestUS3_MissingInterpreter`, us4_test.go (FR-006, SC-003). Then walk quickstart scenarios A, C, and D with the Docker-built `dist/rune` (quickstart Prerequisites)

**Checkpoint**: Issue #33's repro exits 2. The CLI reports every body code, and RUNE_FAILED_EXIT_CODE equals the exit code

---

## Phase 4: User Story 2 - An agent sees the same exit code over MCP (Priority: P1)

**Goal**: The MCP tool result's `[exit N]` marker equals the CLI exit code for the same task, and `isError` is true exactly when N is not 0

**Independent Test**: Calling a task whose body exits 2 over `rune mcp` returns `isError: true` and text ending in `[exit 2]`

**Depends on**: T017 (no MCP code changes; the adapter in internal/cli/mcp.go:154 already computes its code through `CodeFor`, research R7)

### Tests for User Story 2

- [X] T020 [P] [US2] In internal/cli/failhook_test.go `TestCallFailHookFixSuggestion` (line 20), replace `res.ExitCode == 0` with `res.ExitCode != 7` and the message `exit = %d, want 7 (the body's code)`
- [X] T021 [P] [US2] In test/integration/failure_hooks_mcp_test.go (line 38) assert the result contains `[exit 7]` instead of `[exit `, and that the index of `[exit 7]` is before the index of `[fix suggestion - from Runefile || failure hooks]` (US2 scenario 4)
- [X] T022 [US2] Add `TestExitCode_MCPMatchesCLI` to test/integration/exit_code_test.go, reusing `mcpCall` from test/integration/secret_masking_mcp_test.go: for each N in the T009 set, the CLI code is N and the MCP result contains `[exit N]` and `"isError":true` (accept the JSON-escaped form, as failure_hooks_mcp_test.go does); a succeeding task gives CLI 0 and `[exit 0]` without `isError` true; a validation error reached at run time (`v:\n    @echo {{ env("RUNE_EXITCODE_TEST_UNSET") }}\n`, an `eval.Error` that `classifyRunErr` maps to `ValidationError`) gives CLI 3 and `[exit 3]`; a usage error reached at run time (`triage (agent):\n    summarize\n` with no `set agent_cmd`, the `agent.NotConfiguredError` path that `classifyRunErr` maps to `UsageError`) gives CLI 2 and `[exit 2]`. Assert the CLI code first so a wrong fixture fails clearly (SC-002, FR-010)
- [X] T023 [US2] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/cli/... ./mcpserver/... ./test/integration/...` and confirm green; confirm `git diff --stat main -- mcpserver/ internal/cli/mcp.go` is empty (research R7); walk quickstart scenario E

**Checkpoint**: CLI and MCP report the same code for body failures, success, and rune's own errors

---

## Phase 5: User Story 3 - A failing dependency's code reaches the caller (Priority: P2)

**Goal**: The exit code is the code of the body whose failure stopped the run, the same failure the banner names, for dependencies, `[parallel]`, and multiple command-line tasks

**Independent Test**: With `a: b` where `b` exits 7, `rune a` exits 7 and the banner names `b`

**Depends on**: T017 (no production code; the scheduler already returns the first failure unchanged, research "Current behavior")

### Tests for User Story 3

- [X] T024 [US3] Add to test/integration/exit_code_test.go: `TestExitCode_Dependency` (`a: b\n    @echo never\nb:\n    @exit 7\n` → code 7, stdout lacks `never`, stderr contains `task "b" failed`); `TestExitCode_MultipleRoots` (`rune x y` with `x` echoing `ok` and `y` exiting 9 → code 9; `rune y x` → code 9 and `x` never runs); `TestExitCode_Parallel` (`[parallel]\np: d1 d2\n    @echo done\nd1:\n    @exit 4\nd2:\n    @exit 5\n` → code is 4 or 5, and equals the number captured by `exit status (\d+)` in stderr, FR-003)
- [X] T025 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/...` and confirm green; walk quickstart scenario B

**Checkpoint**: Every way a run can stop on a body failure reports that body's code

---

## Phase 6: User Story 4 - Documentation and release notes state the new rule (Priority: P3)

**Goal**: The CLI reference, troubleshooting guide, MCP guide, and ci-cd example state the propagation rule, the reserved codes, and the overlap; the release notes flag the change as breaking

**Independent Test**: docs/cli.md and docs/troubleshooting.md exit-code tables and docs/mcp.md each state the rule and the overlap note; `rune --file docs/examples/ci-cd/Runefile release-gate closed` exits 2

### Tests for User Story 4

- [X] T026 [P] [US4] Create test/docs/exitcode_test.go with `TestCICDReleaseGateExitCodes`: using `runRune` from test/docs/harness_test.go with `dir = filepath.Join(repoRoot, "docs", "examples", "ci-cd")`, `release-gate closed` exits 2 with stderr containing `refused`, and `release-gate` (default) exits 0 (SC-004, research R10). It fails until T027

### Implementation for User Story 4

- [X] T027 [US4] Add a `release-gate window="open":` task to docs/examples/ci-cd/Runefile, with a comment line above it saying it exits 2 ("refused") when the window is closed so CI can branch on the code. Body uses only mvdan builtins (`[`, `echo`, `exit`): `@[ "{{window}}" != "closed" ] || { echo "release gate: refused (window closed)" >&2; exit 2; }` then `@echo "release gate: open"`. Do not change the README's "Run it" block (`rune ci`), which `TestExamplesRun` runs and expects 0. T026 passes
- [X] T028 [P] [US4] In docs/examples/ci-cd/README.md replace "Exit codes: `0` success, `1` task failure, `2` usage, `3` validation." (line 32) with the rule from contracts/exit-codes.md §1 (a body failure exits with the body's code, `1` when there is none, `2` usage, `3` validation, `130` interrupt), and add a short section "Branching on the exit code" with a `sh` block that runs `rc=0; rune release-gate "$WINDOW" || rc=$?` and a `case "$rc"` mapping 0 to proceed, 2 to "refused, skip the release", anything else to fail
- [X] T029 [P] [US4] Rewrite the "Exit codes" table in docs/cli.md (lines 120 to 129) per contracts/exit-codes.md §1: rows `0`, *N* (body exit code, including dependencies and later command-line tasks), `1` (failure with no body exit code: `[confirm]` declined or no terminal, missing interpreter, agent failure), `2`, `3`, `130`. Below it add the overlap note with the two banner lines from contract §1, the 128 + S signal rule on Linux and macOS, and the 0 to 255 limit for shell bodies on every OS (research R5)
- [X] T030 [P] [US4] In docs/troubleshooting.md change the "Exit codes at a glance" row for `1` (line 12) and add an *N* row, matching T029; retitle the "A task body failed ... exit 1" heading (line 108) to "### A task body failed" and say rune exits with the body's own code (7 in the shown example, 1 when the failure has no exit status), plus a one-line overlap note linking to cli.md#exit-codes. Leave the "Missing interpreter" section's exit `1`. Then `grep -rn "a-task-body-failed" docs/ README.md` and fix any anchor that pointed at the old heading
- [X] T031 [P] [US4] In docs/mcp.md "Fix suggestions on failure" (around line 121), add a short paragraph (FR-012, research R9): the `[exit N]` marker carries the body's own exit code, the same code the CLI exits with, so an agent can tell a deliberate refusal (for example exit 2) from a failure; `2`, `3`, and `130` are rune's own codes for usage, validation, and interrupt; a body may exit with one of those numbers, so the marker alone does not tell a body's exit 2 from a rune usage error; link to cli.md#exit-codes for the full table. Keep the `[exit 1]` example, since `go test` exits 1
- [X] T032 [US4] SC-005 sweep: run `grep -rniE --exclude='*_test.go' 'exit code|exit status|task (body )?fail' docs/ README.md CONTRIBUTING.md editors/ cmd/ internal/cli/ internal/language/` and review each hit (docs pages, CLI help text, editor hover docs); fix any place that still says 1 is the only code for a body failure. docs/runefile.md and docs/how-to/dependencies-and-hooks.md ("hooks never change the exit code") stay as they are (research R9)
- [X] T033 [US4] Use `fix(cli)!: breaking: a failed task exits with its body's exit code, not always 1` as the squash-merge PR title (the merge appends the PR number, so leave `#33` out of the title; git-cliff renders only the title, so it must name the pattern itself) and start the PR body with "Closes #33" plus one sentence naming the affected pattern: scripts that test for exit code 1 exactly (for example `[ $? -eq 1 ]`) now see the body's own code (FR-013, research R11). Check the rendered line with `git cliff --unreleased` against cliff.toml before release

**Checkpoint**: Docs, the tested example, and the release-note title all agree with contracts/exit-codes.md

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T034 Run `rune lint` (gofmt, go vet, golangci-lint) and fix findings in internal/runtime/scheduler/scheduler.go, internal/runtime/interp/interp.go, internal/cli/exit.go, and the new test files; if golangci-lint is not installed locally, run gofmt and `go vet ./...` in Docker and let CI's lint gate cover golangci-lint
- [X] T035 Run `rune test`, `rune test-race`, and `rune docs-check` (all in Docker) and confirm everything passes, including the unchanged golden tests (no golden file changes in this feature, plan Constitution Check VI)
- [X] T036 Check that no added line in the diff contains an em dash: `git diff main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` prints nothing (maintainer rule)
- [X] T037 Walk quickstart scenarios A to F in specs/028-propagate-task-exit-code/quickstart.md with a binary built in Docker. Windows and macOS coverage (SC-001) comes from CI's three-OS test matrix; T007 and T012 are skipped on Windows by build tag. Note the result in the PR description

---

## Phase 8: Code review fixes (research R14)

- [X] T038 Add `TestExitCode_InterruptedTrappingChildExits130` to test/integration/exit_code_signal_test.go: a body whose child traps SIGINT and exits 2, SIGINT sent to rune only, expects exit 130. Confirmed failing (exit 2) before T041
- [X] T039 Extend `TestExitCode_NoExitMessage` and `TestExitCode_HookSeesSameCode` in test/integration/exit_code_test.go to tables: body code, run-time validation error (3), `agent` task with no `agent_cmd` (2). Confirmed failing (exit 1, hook `code=1`) before T041 and T042
- [X] T040 Add `TestBodyExitCode` (moved from the scheduler), `TestClassifySilentFailureKeepsCode`, and `TestClassifyRunErrCancelledRunIsInterrupt` to internal/cli/exit_test.go; add `TestRunCancelledReturnsContextError` to internal/runtime/agent/agent_test.go
- [X] T041 In internal/cli/run.go, have `classifyRunErr` return `Interrupted` when the run context is cancelled, and split the classification into `classify`, which prints nothing and classifies a silent `TaskFailure` by its wrapped error; remove the `context.Canceled` check from `CodeFor` in internal/cli/exit.go
- [X] T042 Add `ExitCode(err) int` to the `Engine` interface in internal/runtime/scheduler/scheduler.go and use it in `runFailHooks`; implement it on the CLI engine as `CodeFor(classify(err))`; move `BodyExitCode` to internal/cli/exit.go as `bodyExitCode`; replace the three hook exit-code tests in internal/runtime/scheduler/failhook_test.go with `TestFailureExitCodeComesFromEngine`
- [X] T043 [P] In internal/runtime/agent/agent.go return the context error when the context is done after `cmd.Run` fails
- [X] T044 [P] In internal/runtime/interp/interp.go assert `syscall.WaitStatus` directly in `exitCode`
- [X] T045 [P] Correct the `agent` rows of the exit-code tables in docs/cli.md and contracts/exit-codes.md: a missing or failing agent CLI exits 1, no `agent_cmd` exits 2

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none
- **Foundational (Phase 2)**: after Setup; blocks every story
- **US1 (Phase 3)**: after Phase 2
- **US2 (Phase 4)**: tests can be written after Phase 2; they pass only after T017
- **US3 (Phase 5)**: tests can be written after Phase 2; they pass only after T017
- **US4 (Phase 6)**: after Phase 2; T026 and T027 are independent of the code stories, the prose in T028 to T031 describes behavior from US1 to US3
- **Polish (Phase 7)**: after all stories

### Within Phases

- T003 before T004
- T005 to T015 written before T016; T016 (red) before T017 and T018
- T017 and T018 before T019; T005 before T008 (same file)
- T009 creates test/integration/exit_code_test.go, so T010, T011, T022, and T024 come after it and run one at a time (same file)
- T026 before T027

### Parallel Opportunities

- T005, T006, T007, T009, T012, T013, T014, T015 at once; T008 follows T005 (same file, internal/cli/exit_test.go)
- T017 and T018 touch different files and can be done at once after T016
- T020 and T021 at once, alongside US1's tests
- T026, T028, T029, T030, T031 at once

---

## Parallel Example: User Story 1 tests

```text
Task: "T005 [US1] TestCodeFor decision table in internal/cli/exit_test.go"
Task: "T006 [US1] cancelled-context test in internal/runtime/interp/interp_test.go"
Task: "T007 [US1] signal test in internal/runtime/interp/interp_signal_test.go"
Task: "T009 [US1] code pass-through table in test/integration/exit_code_test.go"
Task: "T012 [US1] signal tests in test/integration/exit_code_signal_test.go"
Task: "T013 [US1] exit 7 in test/integration/us1_test.go"
Task: "T014 [US1] exit 7 in test/integration/error_banner_test.go"
Task: "T015 [US1] exit 7 in test/integration/failure_hooks_cli_test.go"
```

## Parallel Example: User Story 4

```text
Task: "T026 [US4] release-gate test in test/docs/exitcode_test.go"
Task: "T028 [US4] exit-code rule and branching snippet in docs/examples/ci-cd/README.md"
Task: "T029 [US4] exit-code table in docs/cli.md"
Task: "T030 [US4] table and section in docs/troubleshooting.md"
Task: "T031 [US4] [exit N] sentence in docs/mcp.md"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 and Phase 2
2. Phase 3 (US1): the fix for issue #33's repro on the CLI
3. Validate with quickstart scenario A: `rune refuse` exits 2

### Incremental Delivery

1. Setup + Foundational: `BodyExitCode` exported, no behavior change
2. US1: the CLI exits with the body's code; interpreter signal and Ctrl-C fixed (MVP)
3. US2: MCP parity proven, no MCP code change
4. US3: dependencies, `[parallel]`, and multiple roots proven
5. US4: docs, tested example, release-note title
6. Polish: lint, full Docker suite, quickstart walk-through

Ship US1 to US4 in one PR: US1 changes the exit-code contract that US4 documents, and the release note is the migration hint for scripts that test for exactly 1.

---

## Notes

- [P] tasks touch different files and have no unfinished dependencies
- Never run `go test` on the host; use the Docker commands above
- mcpserver/ and internal/cli/mcp.go must not change in this feature (research R7)
- The banner text must stay byte-identical (FR-011); no golden files change
- T008 and the `context.Canceled` check in T017 fix research R13: a `[no-exit-message]` task cancelled by Ctrl-C exits 1 today instead of 130. The fix sits in `CodeFor`, not `runBody`, so the banner stays suppressed
