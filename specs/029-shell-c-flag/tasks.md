---

description: "Task list for spec 029: honor the -c flag in set shell"
---

# Tasks: Honor the `-c` Flag in `set shell`

**Input**: Design documents from `/specs/029-shell-c-flag/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/shell-override.md](contracts/shell-override.md), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI makes development test-first, so each story writes its tests before the implementation and confirms they fail. US2 adds no production code. Its equivalence matrix (T012) fails on `main` (research baseline table) and passes once US1's `Select` change (T009) lands; its other tests (T011, T013) already pass on `main` and guard the paths that must not change. US3 does not depend on US1: its docs examples drop `-c`, so they stop at a failing line with or without the fix.

**Organization**: Tasks are grouped by user story so each story can be implemented and tested on its own.

**Test runs**: Always in Docker, never on the host: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <packages>`. Add `-e CGO_ENABLED=1` and `-race` for the race run. Without `GOFLAGS=-buildvcs=false`, `test/integration` and `test/docs` fail to build with "error obtaining VCS status".

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 to US3)

---

## Phase 1: Setup

**Purpose**: Branch and a green baseline

- [X] T001 Create and switch to branch `029-shell-c-flag` from `main` (`git checkout -b 029-shell-c-flag`); the untracked artifacts in specs/029-shell-c-flag/ and the change to .specify/feature.json come along
- [X] T002 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/... ./internal/language/... ./test/integration/... ./test/docs/...` and confirm it is green before any change, so later failures are attributable to this feature

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The one helper that removes the command-string flag from a shell list (research R2, R3; data-model "Command-string flag" and "Effective command")

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 Add failing table test `TestCommandWithoutStringFlag` to internal/runtime/executor_test.go for `commandWithoutStringFlag(cmd []string) []string`, with every row of the research R3 table. Rows (input → want): `["bash", "-cu"]` → `["bash", "-u"]`; `["bash", "-euo", "pipefail", "-c"]` → `["bash", "-euo", "pipefail"]`; `["bash", "-euc"]` → `["bash", "-eu"]`; `["bash", "-c", "-e"]` → `["bash", "-e"]`; `["bash", "-c", "-o", "pipefail"]` → `["bash", "-o", "pipefail"]`; `["bash", "-c", "-euo", "pipefail"]` → `["bash", "-euo", "pipefail"]`; `["bash", "-c", "-c"]` → `["bash"]`; `["zsh", "-fc"]` → `["zsh", "-f"]`; `["sh", "-c"]` → `["sh"]`; `["bash", "--norc", "-c"]` → `["bash", "--norc"]`; `["/usr/bin/env", "bash", "-euc"]` → `["/usr/bin/env", "bash", "-eu"]`; `["busybox", "sh", "-c"]` → `["busybox", "sh"]`; `["BASH.EXE", "-cu"]` → `["BASH.EXE", "-u"]`. Unchanged: `["bash", "-o", "pipefail"]`, `["bash", "-O", "extglob"]`, `["bash", "-eu"]`, `["bash", "-O2"]` (not letters only), `["bash", "--rcfile=c"]` (starts with `--`), `["bash", "-c", "exec bash -eu $0"]` (own code), `["nix", "develop", "-c", "bash", "-eu"]` (the wrapper's `-c`), `["php", "-c", "/etc/php.ini"]`, `["jq", "-c", "-f"]`, `["node", "-c"]`, `["pwsh", "-c"]` (not known shells), `["-c"]`. Compare with `slices.Equal`. Also assert the input is not changed: copy each input with `slices.Clone` before the call and compare after (data-model: "`settings.Shell` is shared by every task in the module and must not be changed in place"). It fails to compile until T004 (red)
- [X] T004 Implement unexported `commandWithoutStringFlag` in internal/runtime/executor.go, following data-model "Known shell", "Command-string flag", "Operand", and the derivation. It always builds and returns a new slice. (1) Find the first element whose base name (the text after the last `/` or `\`, lowercased, with a `.exe` suffix removed) is one of `sh`, `bash`, `dash`, `zsh`, `ksh`, `mksh`, `ash`, `fish`; if none, return a copy. (2) After it, a command-string flag "starts with exactly one `-` (not `--`); has at least one character after the `-`; every character after the `-` is an ASCII letter; contains the letter `c`". If there is none, return a copy. (3) If any element after the first flag is an operand (does not start with `-` or `+`, and the element before it is not a `-` or `+` cluster of letters containing `o` or `O`), return a copy: the list gives the shell its own code. (4) Otherwise remove every `c` from each flag after the shell, and drop a flag when only `-` remains; copy all other elements unchanged. Doc comment says why, not what: with `-c` and no code, the shell reads the script path as its code and runs the file as a child script that ignores the list's flags (issue #34); other uses of `-c` work today and are left alone. Use only the standard library. T003 passes

**Checkpoint**: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/` is green; no user-visible behavior has changed yet

---

## Phase 3: User Story 1 - Shell flags apply to the body when the list has `-c` (Priority: P1) MVP

**Goal**: A `set shell` list or `[script("...")]` command that contains a command-string flag runs the body with the list's other flags applied, so a failing line stops the task (spec FR-001, FR-002, FR-004, FR-005)

**Independent Test**: The issue's repro (`set shell := ["bash", "-euo", "pipefail", "-c"]`, body `@echo one`, `@false`, `@echo two`) prints `one` only and `rune t` exits 1

### Tests for User Story 1

> Write these first and confirm they fail before T009

- [X] T005 [P] [US1] In internal/runtime/executor_test.go: change `TestSelectShellOverride` to assert `sel.Command` equals `["bash", "-u"]` (via `slices.Equal`) and `sel.Display == "shell:bash"`, and update its comment to say the `-c` is removed (spec 029); add `TestSelectShellOverrideRemovesStringFlag` (`config.Settings{Shell: []string{"bash", "-euo", "pipefail", "-c"}}` → `Command` `["bash", "-euo", "pipefail"]`, `Kind == KindInterp`) and `TestSelectScriptAttributeRemovesStringFlag` (`ast.Attribute{Kind: ast.AttrScript, Str: "bash -euc"}` → `Command` `["bash", "-eu"]`, `Display == "bash -euc"`, research R2: Display unchanged)
- [X] T006 [P] [US1] Create test/integration/shell_override_test.go with a helper `requireBash(t *testing.T)` that calls `t.Skip` when `runtime.GOOS == "windows"` (message: bash on Windows CI may be the WSL launcher or Git Bash, spec 029 research R7) or when `exec.LookPath("bash")` fails. Add, each calling `requireBash` first and using `writeRunefile` and `run(t, dir, nil, "--color=never", "t")` from test/integration/harness_test.go:
  - `TestShellOverride_IssueRepro`: the Runefile from the spec Overview (`set shell := ["bash", "-euo", "pipefail", "-c"]`, a `# Should stop at false.` doc comment, task `t` with body `@echo one`, `@false`, `@echo two`); assert `r.stdout == "one\n"`, `r.code == 1`, and `r.stderr` contains `task "t" (bash) failed: exit status 1` (contract §4, SC-001)
  - `TestShellOverride_UnsetVariable`: `set shell := ["bash", "-cu"]`, body `@echo "x=$RUNE_029_UNSET"` then `@echo after`; assert `r.code != 0`, `r.stderr` contains `unbound variable`, `r.stdout` lacks `after` (Story 1 scenario 2)
  - `TestShellOverride_FlagInCluster`: `set shell := ["bash", "-euc"]`, body `@echo a`, `@(exit 3)`, `@echo b`; assert `r.stdout == "a\n"` and `r.code == 3` (Story 1 scenario 3)
  - `TestShellOverride_Pipefail`: `set shell := ["bash", "-eo", "pipefail", "-c"]`, body `@false | true`, `@echo after`; assert `r.code == 1` and `r.stdout` lacks `after` (Story 1 scenario 4)
- [X] T007 [US1] Add to test/integration/shell_override_test.go (after T006, same file):
  - `TestShellOverride_ScriptAttribute`: no `set shell`; task `t` under `[script("bash -euc")]` with body `echo a`, `false`, `echo b`; assert `r.stdout == "a\n"` and `r.code == 1` (Story 1 scenario 5, FR-004)
  - `TestShellOverride_Stdin`: `set shell := ["bash", "-euc"]`, body `@read -r line`, `@echo "got=$line"`; `runWithStdin(t, dir, "hello\n", "--color=never", "t")`; assert `r.stdout == "got=hello\n"` and `r.code == 0` (Story 1 scenario 7)
  - `TestShellOverride_FailureHook`: `set shell := ["bash", "-euc"]`; task `t: || diagnose` with body `@echo a`, `@(exit 3)`, `@echo b`; task `diagnose` with body `@echo "code=$RUNE_FAILED_EXIT_CODE"`. Assert `r.stdout` contains `code=3` and has no line `b`, and `r.code == 3` (spec edge case "Failure hooks", FR-005; the hook form follows `TestExitCode_HookSeesSameCode` in test/integration/exit_code_test.go)
  - `TestShellOverride_MCP`: the T006 repro Runefile; `result, _ := mcpCall(t, dir, "t")` (helper in test/integration/secret_masking_mcp_test.go); assert the result contains `[exit 1]` and `"isError":true` (accept the JSON-escaped form `\"isError\":true` too, as test/integration/failure_hooks_mcp_test.go does), and does not contain `two` (Story 1 scenario 6)
- [X] T008 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/ ./test/integration/ -run 'Select|ShellOverride'` and confirm T005 to T007 fail for the expected reason: `Command` still holds `-c`, `two`/`after`/`b` appear in stdout, exit codes are 0, and the failure hook never runs (no `code=3`). `TestShellOverride_Stdin` may already pass, since `read` and `echo` succeed under the old behavior; it is a regression guard

### Implementation for User Story 1

- [X] T009 [US1] In internal/runtime/executor.go `Select`: in the `[script("...")]` branch use `Command: commandWithoutStringFlag(splitCommand(attr.Str))`, and in the `set shell` branch use `Command: commandWithoutStringFlag(settings.Shell)`. Leave `Display` values, the python, node, custom, and agent branches, and internal/runtime/interp/interp.go unchanged (FR-008, research R2). Update the `Select` doc comment and the package comment's `set shell` wording to say a shell's command-string flag is removed so the body runs from the script file with the list's other flags (spec 029). Also change the `Shell` field comment in internal/config/settings.go from `// set shell := ["bash", "-cu"]` to `// set shell := ["bash", "-euo", "pipefail"]`, so the code no longer shows a list without `-e`. Makes T005 to T007 pass
- [X] T010 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/... ./test/integration/...` and confirm green, including the existing `TestSelectPythonOverride`, `TestSelectScriptAttribute` (`ruby -w` unchanged), and the interpreter tests in test/integration/us3_test.go and exit_code_test.go (FR-005, FR-008)

**Checkpoint**: Issue #34's repro stops at `false` and exits 1, on the CLI and over MCP

---

## Phase 4: User Story 2 - Adding or removing `-c` does not change what a task does (Priority: P1)

**Goal**: For any body and shell list, the list with a command-string flag behaves like the same list without it; shell state carries across lines; paths without the flag are unchanged (spec FR-003, FR-006, FR-008)

**Independent Test**: The equivalence matrix in T012 matches on stdout and exit code for every list and body pair

### Tests for User Story 2

- [X] T011 [P] [US2] Add `TestSelectInterpreterOverridesKeepStringFlag` to internal/runtime/executor_test.go (after T005, same file): `Select` for `Executor: "python"` with `config.Settings{Python: []string{"python3", "-c"}}` returns `Command` `["python3", "-c"]`, and for `Executor: "node"` with `Node: []string{"node", "-c"}` returns `["node", "-c"]` (FR-008: python and node lists are not covered). Also add `TestSelectScriptAttributeKeepsNonShellFlag`: `[script("node -c")]` returns `Command` `["node", "-c"]` and `[script("php -c /etc/php.ini")]` returns `["php", "-c", "/etc/php.ini"]`, each with `Display` equal to the attribute string (spec edge case "Other programs")
- [X] T012 [US2] Add `TestShellOverride_EquivalenceMatrix` to test/integration/shell_override_test.go (after T007, same file; calls `requireBash`). List pairs (with flag, without flag): `["bash", "-c"]`/`["bash"]`, `["bash", "-cu"]`/`["bash", "-u"]`, `["bash", "-euc"]`/`["bash", "-eu"]`, `["bash", "-euo", "pipefail", "-c"]`/`["bash", "-euo", "pipefail"]`, `["bash", "-c", "-eu"]`/`["bash", "-eu"]`. Bodies: all succeed (`@echo a`, `@echo b`); middle line fails (`@echo a`, `@false`, `@echo b`); unset variable (`@echo "x=$RUNE_029_UNSET"`, `@echo b`); failing pipeline (`@false | true`, `@echo b`). For each pair and body, in a subtest named `<pair index>/<body name>`, write two Runefiles that differ only in the `set shell` line, run both with `run(t, dir, nil, "--color=never", "t")`, and `t.Errorf` when stdout or the exit code differ. Do not compare stderr: bash's own messages name the random temp-file path (SC-002)
- [X] T013 [US2] Add to test/integration/shell_override_test.go (after T012, same file; each calls `requireBash`):
  - `TestShellOverride_ListWithoutErrexit`: `set shell := ["bash", "-cu"]`, body `@echo a`, `@false`, `@echo b`; assert `r.stdout == "a\nb\n"` and `r.code == 0`, the same as `["bash", "-u"]` (Story 2 scenario 2, contract §3 point 2)
  - `TestShellOverride_StateAcrossLines`: create `sub` inside the Runefile directory with `os.Mkdir`; `set shell := ["bash", "-euc"]`, body `@greeting=hello`, `@cd sub`, `@echo "$greeting $(basename "$PWD")"`; assert `r.stdout == "hello sub\n"` and `r.code == 0` (Story 2 scenario 5, FR-006)
  - `TestShellOverride_OwnCommandString`: `set shell := ["bash", "-c", "exec bash -eu $0"]`, body `@echo a`, `@false`, `@echo b`; assert `r.stdout == "a\n"` and `r.code == 1`: the list's own code runs the script, which arrives as `$0`, exactly as on `main` (Story 2 scenario 4, spec edge case "Own command string")
- [X] T014 [US2] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/ ./test/integration/ -run 'Select|ShellOverride'` and confirm green. Runefiles with no `set shell`, and lists with no command-string flag, are covered by the unchanged suite from T010 (Story 2 scenarios 3 and 4, SC-003)

**Checkpoint**: `-c` no longer changes what a task does; every no-flag path is unchanged

---

## Phase 5: User Story 3 - The docs show a working shell override and explain how it runs (Priority: P2)

**Goal**: Every `set shell` example in the docs stops at a failing line; the Runefile reference explains how a `set shell` body runs; the release notes name the change (spec FR-009, FR-010)

**Independent Test**: T015 passes, and the "Shell override" subsection of docs/runefile.md states each point in contract §3

### Tests for User Story 3

- [X] T015 [P] [US3] Create test/docs/shell_override_test.go with `TestShellOverride_DocsExamplesStopOnFailure`. Skip on Windows (same reason as T006). For every file from `docMarkdownFiles(t)` (test/docs/harness_test.go), find each match of the regexp `set shell := (\[[^\]]*"[^\]]*\])` (a quoted list, so prose such as `set shell := [...]` in docs/how-to/executors.md is not matched); decode group 1 with `encoding/json` into `[]string` and `t.Errorf` when that fails or the list is empty. Then check the text: `t.Errorf` unless some element after index 0 is a single-dash cluster of letters containing `e` (`-e`, `-eu`, `-euo`), so an example is checked even where its program is not installed. When `exec.LookPath(list[0])` fails, `t.Logf` and move on without running it. Otherwise write a Runefile of the whole match followed by `\n\nt:\n    @echo before\n    @false\n    @echo after\n` into `t.TempDir()`, run `runRune(t, dir, "t")`, and `t.Errorf` naming `relPath(file)` and the match when the exit code is 0 or stdout contains `after`. After the loop, `t.Fatal` when no match was found at all, so a reworded doc cannot silently empty the test (SC-004). Add `TestShellOverride_DocsSection` in the same file: `t.Errorf` unless docs/runefile.md has a line equal to `### Shell override`, and unless docs/how-to/settings-and-dotenv.md and docs/how-to/executors.md each contain `runefile.md#shell-override`. The link checker skips anchors (test/docs/links_test.go:16-17), so this is the only check on them. Both tests fail until T017 to T019: the pages show `["bash", "-cu"]` and have no section or links yet
- [X] T016 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/ -run ShellOverride` and confirm T015's tests fail: the examples in docs/runefile.md and docs/how-to/settings-and-dotenv.md lack `-e` and do not stop, and the heading and links are missing

### Implementation for User Story 3

- [X] T017 [US3] In docs/runefile.md:
  - In the settings block of "Variables and settings" (line 155), replace `set shell := ["bash", "-cu"]  # override the shell for (sh) bodies` with `set shell := ["bash", "-euo", "pipefail"]  # override the shell for (sh) bodies`
  - Under "## Executors", after the paragraph that ends "for the container caveat).", add a `### Shell override` subsection (anchor `#shell-override`). It says `set shell := [...]` runs default bodies with a system shell instead of the built-in one, by writing the interpolated body to a temporary script and running the list with the script's path appended. Then a short list with the points of contract §3: the whole body runs as one script in one shell process, so variables and the current directory carry across lines; a failing line stops the task only when the shell's exit-on-error flag (`-e`) is set, and without it later lines still run and the task's status is the last command's; lines are not echoed, and the `@` and `-` prefixes have no effect, so under `-e` a `-` line that fails stops the task; a shell's `-c` with no code after it (a habit from `just`) is accepted and dropped, so `["bash", "-euo", "pipefail", "-c"]` runs the same as `["bash", "-euo", "pipefail"]`, and the same holds for a `[script("...")]` command; a list that gives the shell its own code, such as `["bash", "-c", "exec bash -eu $0"]`, runs as written with the script path as `$0`, and a `-c` that belongs to a wrapper or to a program that is not a shell (`nix develop -c`, `node -c`) is passed through unchanged. Close with one line recommending the built-in shell for portability, linking to how-to/executors.md
  - Constraint: T015 checks every `set shell := [` line under docs/. Do not write that prefix in front of a list that lacks `-e`; write the bare list, as in the bullet above
- [X] T018 [P] [US3] In docs/how-to/settings-and-dotenv.md line 23, replace `set shell := ["bash", "-cu"]` with `set shell := ["bash", "-euo", "pipefail"]` (keep the comment above it), and add a Pitfalls bullet: **`set shell` runs the body as one script.** Include `-e` so a failing line stops the task; the `@` and `-` prefixes have no effect. Link to `../runefile.md#shell-override`
- [X] T019 [P] [US3] In docs/how-to/executors.md, extend the "Stick to the default `sh` for portability" pitfall (line 45) with one sentence linking to `../runefile.md#shell-override` for how an override runs the body
- [X] T020 [P] [US3] In internal/language/builtin.go line 71, change the `shell` setting's `Documentation` from "Command used to run shell task bodies." to "Command that runs shell task bodies as one script. Include -e to stop at a failing line." (research R8; no test pins the old string)
- [X] T021 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/... ./internal/language/...` and confirm green: both T015 tests, `TestRuneCodeBlocksValidate` (settings-and-dotenv.md is a self-contained page), and the link checker (it checks that runefile.md exists; T015 checks the heading and the links)
- [ ] T022 [US3] Use `fix(runtime)!: breaking: shell lists with -c now apply their flags, so a failing line can fail the task` as the squash-merge PR title (the merge appends the PR number, so leave `#34` out; git-cliff renders only the title, so it carries both points of FR-010, research R6). Start the PR body with "Closes #34." and one sentence naming the lists that change: those where a known shell's `-c` has no code after it; wrapper lists, lists with their own code, and non-shell programs are unchanged (contract §2, §6). Check the rendered line with `git cliff --unreleased` before release

**Checkpoint**: Docs, the hover text, and the PR title all agree with contracts/shell-override.md

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T023 Run `gofmt -l internal/runtime internal/language test/integration test/docs` (prints nothing) and `go vet ./...` in Docker; golangci-lint (and gofumpt, Principle VIII) is not in the test image, so CI's lint gate covers it, confirmed in T027; say so in the PR description
- [X] T024 Run the full suite and the race run in Docker: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` and `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...`; confirm green, including test/corpus with `testdata/corpus/full.rune` unedited and no golden file changes (SC-003, research R6)
- [X] T025 Check that no added line in the diff contains an em dash: `git diff main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` prints nothing (maintainer rule)
- [X] T026 Walk quickstart scenarios A to H in specs/029-shell-c-flag/quickstart.md with a binary built in Docker. macOS coverage (SC-001) comes from CI's test matrix, confirmed in T027; the bash tests skip on Windows (research R7). Note the result in the PR description
- [ ] T027 When the user asks to push: read `git config user.name`, run `gh auth status`, run `gh auth switch --user <name>` if the active account differs, and confirm with `gh auth status`. Push the branch, open the PR with the T022 title and body, and run `gh pr checks --watch` until every check passes. This confirms golangci-lint (T023) and the macOS run for SC-001 (T026). If a check fails, report it with its log

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none
- **Foundational (Phase 2)**: after Setup; blocks US1 and US2
- **US1 (Phase 3)**: after Phase 2
- **US2 (Phase 4)**: after Phase 2; its tests pass only after T009 (US1)
- **US3 (Phase 5)**: after Setup only; it does not use the helper, and its docs examples drop `-c`
- **Polish (Phase 6)**: after all stories

### Within Phases

- T003 before T004
- T005 to T007 written before T008; T008 (red) before T009; T009 before T010
- T006 creates test/integration/shell_override_test.go, so T007, T012, and T013 come after it and run one at a time (same file)
- T005 before T011 (same file, internal/runtime/executor_test.go)
- T015 before T016; T016 (red) before T017 to T020; T017 to T020 before T021
- T023 to T026 before T027; T027 runs only when the user asks to push

### Parallel Opportunities

- T005 and T006 (different files)
- T011 alongside T012 (different files)
- US3 can run alongside US1 and US2: T015 to T021 touch only docs/, test/docs/, and internal/language/
- T018, T019, and T020 (different files)

---

## Parallel Example: User Story 1

```bash
# Write the US1 tests together (different files):
Task: "T005 Select tests for set shell and [script] with -c in internal/runtime/executor_test.go"
Task: "T006 Binary-level repro, unset variable, cluster, pipefail tests in test/integration/shell_override_test.go"
```

## Parallel Example: User Story 3

```bash
# After T017, the remaining doc and hover edits touch different files:
Task: "T018 Example and pitfall in docs/how-to/settings-and-dotenv.md"
Task: "T019 Pitfall link in docs/how-to/executors.md"
Task: "T020 Hover text in internal/language/builtin.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 and Phase 2 (T001 to T004)
2. Phase 3 (T005 to T010)
3. Stop and validate: the issue's repro prints `one`, exits 1, and MCP reports `[exit 1]`

### Incremental Delivery

1. Setup and Foundational: the helper exists, no behavior change
2. US1: the reported defect is fixed
3. US2: equivalence, state, and unchanged paths are pinned by tests
4. US3: docs examples and explanation match the behavior; PR title carries the release note
5. Polish: lint, full and race suites, em dash check, quickstart walk, then the PR with green checks

---

## Notes

- [P] tasks touch different files and have no dependency on incomplete tasks
- Comments explain why, not what, and match the surrounding code
- No added prose or code uses the em dash
- Commit after each phase or logical group; never add a Co-Authored-By trailer or mention an AI agent in commits or the PR
