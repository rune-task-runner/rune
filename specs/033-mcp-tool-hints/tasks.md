---

description: "Task list for 033-mcp-tool-hints"
---

# Tasks: Truthful MCP Tool Hints

**Input**: Design documents from `specs/033-mcp-tool-hints/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/mcp-tools.md](contracts/mcp-tools.md),
[contracts/mcpserver-api.md](contracts/mcpserver-api.md),
[contracts/readonly-attribute.md](contracts/readonly-attribute.md), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI requires test-first, multi-layer verification, and
research R9 names the test files per layer. Write each test task first and confirm it fails before
the implementation task it covers. Tests that guard unchanged behavior pass before the change and
must stay green; those tasks say so.

**Organization**: One phase per user story, in priority order. The Foundational phase adds the
`[readonly]` attribute, which US1 and US3 both need.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: the user story the task serves (US1, US2, or US3)

## Commands

Tests run in Docker only.

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/diag ./internal/lexer ./internal/parser ./internal/cli ./test/corpus -update
```

Add `-run` and a package path to the first command for a focused run. Run `git` on the host, since
it cannot read the mounted repository inside the container. Check the golangci-lint pin in
`.github/workflows/ci.yml` before linting.

---

## Phase 1: Setup

**Purpose**: Confirm the starting point.

- [X] T001 Confirm the starting state: `git branch --show-current` prints `033-mcp-tool-hints`, and `git status --short` shows `?? specs/033-mcp-tool-hints/` and the unrelated `?? .docs/`, which stays out of every commit. Run `git fetch origin`; if `origin/main` has moved past `edd5208`, run `git merge --ff-only origin/main` (the branch has no commits of its own yet)
- [X] T002 Run the full suite in Docker (first command under Commands) before any change and record any test that already fails, so later failures can be attributed

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Make `[readonly]` a known attribute everywhere (FR-001, FR-015, research R1). It has no effect on hints yet.

**CRITICAL**: US1 and US3 cannot begin until this phase is complete. The integration fixture in T010 uses the attribute, so US2's binary-level tests need it too.

- [X] T003 [P] Add `TestParseReadonly` to `internal/parser/typed_params_test.go`: `[readonly]\nt:\n    @echo hi\n` parses with `task.Attr(ast.AttrReadonly) != nil`; `[readonly, network]` sets both attributes; `[readonly]` twice on one task parses without errors; and `[readonly("x")]` reports a `RUNE1001` error, as `[network("x")]` does. Watch it fail to compile because `ast.AttrReadonly` does not exist
- [X] T004 [P] Add `TestFmtReadonly` to `internal/formatter/formatter_test.go`: `[readonly, network]\nt:\n    @echo hi\n` formats with `[readonly]` and `[network]` on separate lines, and formatting the output again changes nothing. Watch it fail because the parser reports `unknown attribute "readonly"`
- [X] T005 Add `AttrReadonly = "readonly"` with the comment `// claims read-only: sets MCP readOnlyHint` to the attribute constants, and add it to `KnownAttributes`, in `internal/ast/ast.go`. Add `ast.AttrReadonly` to the no-argument case in `internal/parser/attribute.go`. Add `{Name: "readonly", Kind: BuiltinAttribute, Signature: "[readonly]", Documentation: "Claim the task changes nothing (sets the MCP readOnlyHint). Rune does not check the body."}` to `builtinAttributes` in `internal/language/builtin.go`. T003, T004, `TestParserKnowsAllASTAttributes`, and `TestRegistryMatchesASTAttributes` pass
- [X] T006 Create `testdata/corpus/readonly-attr.rune` with a leading comment naming spec 033 and three tasks: `[readonly] status`, `[readonly, network] health`, and `[readonly] report: status && health`. Generate `testdata/corpus/readonly-attr.ast` with `go test ./test/corpus -update` in Docker. Never edit the golden by hand. Confirm that `git status --short testdata/corpus` shows only the two new files
- [X] T007 [P] Add `readonly` to the attribute alternatives in `editors/vscode/syntaxes/runefile.tmLanguage.json`, after `network`. Confirm the file still parses with `python3 -m json.tool`

**Checkpoint**: `[readonly]` parses, formats, and completes in editors, and changes nothing else.

---

## Phase 3: User Story 1 - Only a declared task claims to be read-only (Priority: P1) MVP

**Goal**: A tool claims to be read-only only when its task has `[readonly]` (FR-002, FR-012, FR-013).

**Independent Test**: List the tools of a Runefile with marked and unmarked tasks, and check that only the marked ones claim to be read-only.

### Tests for User Story 1

- [X] T008 [P] [US1] Add `TestToolHintsReadOnly` to `mcpserver/server_test.go`: table over `toolFor`. `TaskInfo{Name: "deploy"}` has `ReadOnlyHint` false and both pointer hints set to false; `{ReadOnly: true}` has `ReadOnlyHint` true; `{Destructive: true}` has `ReadOnlyHint` false and `DestructiveHint` true; `{Network: true}` has `ReadOnlyHint` false and `OpenWorldHint` true. Add `TestListToolsReadOnlyThroughClient`, which reads the same values after `connect` and `ListTools`. Watch both fail to compile because `TaskInfo.ReadOnly` does not exist
- [X] T009 [P] [US1] Add `TestAdapterReadOnlyFlag` to `internal/cli/mcp_test.go`, following `TestAdapterDestructiveFlag`: with `[readonly]` on `status` and none on `deploy` or `[confirm] clean`, `ReadOnly` is true only for `status`. Watch it fail to compile
- [X] T010 [US1] Create `test/integration/tool_hints_mcp_test.go` with `const toolHintsRunefile` holding the quickstart's scratch Runefile verbatim, and a helper `toolHints(t, dir string, flags ...string) map[string]map[string]any` that decodes each tool's raw `annotations` from the `tools/list` reply, so a missing `readOnlyHint` key is checkable. Add `TestToolHintsMCP_ReadOnlyOnlyWhenDeclared`: quickstart rows A to E and M, for `readOnlyHint` only, with "absent" meaning no key. Use `mcpToolsList` from `test/integration/typed_schema_mcp_test.go`, extended in T021 when US2 needs flags
- [X] T011 [US1] Add `TestToolHintsMCP_ReadonlyChangesNothingElse` to `test/integration/tool_hints_mcp_test.go` (quickstart Q, FR-013): calling `status` gives `[exit 0]` and writes `status.done`; `rune status` exits 0 and writes it too; and `rune --list` shows `status` with its doc comment and no other mark. Passes before T013: it guards unchanged behavior
- [X] T012 [US1] Run T008 to T011 in Docker. T008, T009, and T010 fail for the reasons above, with T010 failing because `deploy` advertises `readOnlyHint: true`. T011 passes

### Implementation for User Story 1

- [X] T013 [US1] In `mcpserver/server.go`, add `ReadOnly bool` to `TaskInfo` with the doc comment from `contracts/mcpserver-api.md`, and set `ReadOnlyHint: t.ReadOnly` in `toolFor`. T008 passes
- [X] T014 [US1] In `internal/cli/mcp.go`, set `ReadOnly: t.Attr(ast.AttrReadonly) != nil` in `Tasks()`. T009, T010, and T011 pass

**Checkpoint**: The false claim is gone. This alone closes the access problem issue #37 leads with.

---

## Phase 4: User Story 2 - Hints cover everything a call can run (Priority: P1)

**Goal**: A task inherits the destructive and network declarations of everything it can run on this host, and no call's outcome changes (FR-003 to FR-006, FR-010 to FR-012, FR-014, research R3 to R6).

**Independent Test**: List the tools of a Runefile where `[confirm]` and `[network]` sit only on dependencies and hooks, and check the hints of the tasks that reach them.

### Tests for User Story 2

- [X] T015 [P] [US2] Add `TestToolHintsReachesDestructive` to `mcpserver/server_test.go`: `toolFor(TaskInfo{ReachesDestructive: true})` has `DestructiveHint` true, and `{Destructive: true, ReachesDestructive: true}` too. Add `TestListToolsSameWithGrantWhenReaching`, following `TestListToolsSameWithGrant`, with a `ReachesDestructive` task (FR-010). Add `TestListToolsSameWithAllowList`, following `TestListToolsSameWithGrant`: a server with `Options{AllowList: []string{"status"}}` over descriptors `{Name: "release", ReachesDestructive: true}` and `{Name: "status", ReadOnly: true}` lists the same tools as one with `Options{}` (FR-010). Watch them fail to compile because the fields do not exist
- [X] T016 [P] [US2] Add `TestRefusalIgnoresReachesDestructive` to `mcpserver/authz_test.go`: for `TaskInfo{Name: "release", ReachesDestructive: true}`, `refusal("release")` is empty with no grant, and a call through `connect` reaches the engine, so `fakeEngine.lastCall` is `release` (FR-010, research R4). Watch it fail to compile
- [X] T017 [P] [US2] Add `TestHTTPToolsListCarriesHints` to `mcpserver/transport_test.go`, following `TestHTTPToolsListMatchesStdio`, over descriptors using `ReadOnly`, `ReachesDestructive`, and `Network`: the HTTP list equals the direct list, and each value is as set (FR-011, quickstart AA). Watch it fail to compile
- [X] T018 [P] [US2] Add `TestAdapterReach` to `internal/cli/mcp_test.go`: table of Runefile source against each tool's `Destructive`, `ReachesDestructive`, and `Network`, using `adapterFor`. Cases: a dependency; a `&&` hook; a `||` hook; `a: b`, `b: && c`, `c: || d`, with `d` having `[confirm]` and, in a second case, `[network]` (SC-002); a private target; a diamond; a task with its own `[confirm]`, where `Destructive` is true and `ReachesDestructive` false; a task whose dependency is `[confirm]`, where `Destructive` stays false; a body that says `rune clean` and `rm -rf dist` with no attributes, which stays false (FR-014); and an `agent`-executor task with no attributes, which stays false. Watch it fail to compile
- [X] T019 [US2] Add `TestAdapterReachFollowsHostOS` to `internal/cli/mcp_test.go`, setting `a.goos` on the adapter (FR-006). `dispatch: wipe-win`, where `wipe-win` has `[windows, confirm("Wipe?")]`, gives false on `linux` and `darwin` and true on `windows`. `viawin: winonly`, where `[windows] winonly: clean` and `clean` has `[confirm]`, gives false on `linux` and true on `windows`. `both: winonly clean` gives true on `linux`
- [X] T020 [US2] Add `TestAgentEndpointHints` to `internal/cli/mcp_test.go`, following `TestAgentEndpointSchemasDisallowOtherNames` with `confirmEngine`, `newAgentAdapter`, and `serverSession`: `release: clean` has `DestructiveHint` true, `mirror: fetch` has `OpenWorldHint` true, and `[readonly] status` has `ReadOnlyHint` true (FR-011, quickstart AB)
- [X] T021 [US2] Change `mcpToolsList` in `test/integration/typed_schema_mcp_test.go` to `func mcpToolsList(t *testing.T, dir string, flags ...string) string`, appending `flags` after `mcp`. Its four existing callers keep working unchanged. Update `toolHints` from T010 to pass `flags` through
- [X] T022 [US2] Add `TestToolHintsMCP_CoverDependencies` to `test/integration/tool_hints_mcp_test.go`: every row A to N of the quickstart's tool list, for all three hints, with row N branching on `runtime.GOOS`. Also check that `_wipe` and `_read-state` are not listed, and that `wipe-win` is listed only on Windows. This test replaces `TestMCPE2E_Pending_HintsCoverDependencies`, which the issue names but this repository does not have
- [X] T023 [US2] Add `TestToolHintsMCP_ImportedTask` to `test/integration/tool_hints_mcp_test.go` (quickstart O), following `TestUnknownArgsMCP_ImportedTask`: `mod tools "tools.rune"`, a `[confirm]` task `clean` in `tools.rune`, and `shipit: tools::clean`. `shipit` advertises `destructiveHint: true`
- [X] T024 [US2] Add `TestToolHintsMCP_GrantChangesNothing` to `test/integration/tool_hints_mcp_test.go` (quickstart P): `toolHints` with `--allow-destructive clean` equals `toolHints` with no flags. Passes before T027: it guards unchanged behavior
- [X] T025 [US2] Add `TestToolHintsMCP_CallsUnchanged` to `test/integration/tool_hints_mcp_test.go` (quickstart R, S, T; FR-010), using `toolCall` and `exists` from `test/integration/destructive_mcp_test.go`. `clean` with no grant returns `refusedText("clean")` and writes no marker. `release` with no grant is an error, does not contain `is destructive`, and writes neither `clean.done` nor `release.done`. `release` with `--allow-destructive clean` writes both. With `--allow-destructive release`, the returned stderr has `task "release" is not destructive`, and calling `release` writes neither marker. Do not pin the bare `[exit 1]`: issue #38 will change it. Passes before T027: it guards unchanged behavior
- [X] T026 [US2] Run T015 to T025 in Docker. T015 to T020, T022, and T023 fail for the reasons above, with T022 and T023 failing because `release` advertises `destructiveHint: false`. T024 and T025 pass

### Implementation for User Story 2

- [X] T027 [US2] In `mcpserver/server.go`, add `ReachesDestructive bool` to `TaskInfo`, set the doc comments on `Destructive`, `ReachesDestructive`, and `Network` from `contracts/mcpserver-api.md`, and in `toolFor` set the destructive hint to `t.Destructive || t.ReachesDestructive`. Do not touch `isDestructive` in `mcpserver/authz.go`. T015, T016, and T017 pass
- [X] T028 [US2] In `internal/cli/mcp.go`, add `func (a *mcpAdapter) reach(t *ast.Task) (destructive, network bool)`, which walks `t.Edges()` breadth-first with a visited set seeded with `t`, as `checkContextClosure` does in `internal/analyzer/analyzer.go`. It does not follow a target missing from `a.tasks` or not `AvailableOn(a.goos)`, with a short comment saying a run skips those. In `Tasks()`, set `ReachesDestructive` from it and `Network` to the task's own `[network]` or the reached one. Leave `Destructive` as the task's own `[confirm]`. T018, T019, T020, T022, T023, T024, and T025 pass
- [X] T029 [US2] In `internal/language/builtin.go`, update the `confirm` and `network` documentation to say the hint also covers tasks that run them: `(maps to the MCP destructive hint, on this task and any task that runs it)` and `(sets the MCP openWorldHint, on this task and any task that runs it)`. In `internal/ast/ast.go`, leave the `AttrNetwork` comment as it is. Run `go test ./internal/language ./internal/lsp` in Docker

**Checkpoint**: All three hints are accurate on every route, and every call behaves as before.

---

## Phase 5: User Story 3 - A false `[readonly]` claim stops at analysis (Priority: P2)

**Goal**: `RUNE2018` rejects `[readonly]` with `[confirm]`, and a `[readonly]` task running one without the attribute (FR-007 to FR-009, research R2).

**Independent Test**: Run `rune analyze` on Runefiles with each contradiction, and check the code, the text, the location, and that nothing ran.

### Tests for User Story 3

- [X] T030 [P] [US3] Create `internal/analyzer/readonly_test.go`, using `analyzeDiags` and `hasCode` from `internal/analyzer/codes_test.go`, with `TestReadonlyRejected`: a table of source against each exact message from `contracts/readonly-attribute.md`, `Code` `diag.CodeInvalidReadonly`, severity `diag.Error`, and the spanned text from `src[d.Span.Start.Offset:d.Span.End.Offset]`. Cases: `[readonly, confirm("x?")] wipe`, spanning `readonly`; `[readonly] report: build`, spanning `build`; `[readonly] report: wipe` where `wipe` has `[confirm]`, with the `[confirm]` text; a `&&` hook; a `||` hook; `[readonly] a: b`, `[readonly] b: c` with one error, for `b` running `c`; a target with both attributes, with one error, at the target's own attribute; a target with `[windows]` and no `[readonly]`; and `[readonly] a: b b`, with two errors. Add `TestReadonlyAllowed` with no `RUNE2018` for: two `[readonly]` tasks in a chain; `[readonly, network]`; `[readonly, private]`; `[readonly, context]`; `[readonly]\nask (agent):\n    Summarize the logs.\n`, an `agent`-executor task (FR-014); an unmarked task depending on a `[readonly]` one; and `[readonly] a: nosuch`, which gets `RUNE2001` only. Watch them fail to compile because the code does not exist
- [X] T031 [P] [US3] Add `TestServiceReportsInvalidReadonly` to `internal/analysis/service_test.go`, following `TestServiceReportsUnquotedParam`: a fault in an imported `common.rune` reaches the service as an error with code `diag.CodeInvalidReadonly`, at that file's path (FR-009)
- [X] T032 [P] [US3] Add `TestToolHintsMCP_StaticErrors` to `test/integration/tool_hints_mcp_test.go` (quickstart U to Z) using `run` from `test/integration/harness_test.go`. U, V, W: `rune analyze` exits 3, and its output has `error[RUNE2018]:` and the exact contract text. X: `rune analyze` reports one `RUNE2018`, for `b` running `c`. Y: with a marker-writing `build`, running `report`, `--list`, `--dump`, `--summary report`, `--dry-run report`, and `mcp` each exit 3 with the text, and no marker exists afterward. Z: `--fmt` exits 0 and the file still has `[readonly]`. A module case: `mod tools "tools.rune"`, where `tools.rune` has `[readonly] report: build` and `build` is unmarked; `rune analyze` exits 3, and the text names `"tools::report"` and `"tools::build"`
- [X] T033 [US3] Run T030, T031, and T032 in Docker and confirm they fail for the reasons above, with T032 failing because the file passes analysis

### Implementation for User Story 3

- [X] T034 [US3] Add `CodeInvalidReadonly = "RUNE2018"` to `internal/diag/codes.go` with a comment naming spec 033. Add `checkReadonly` to `internal/analyzer/analyzer.go` and call it after `checkContext` in `Analyze`. For each task in `a.file.Tasks` with `[readonly]`: if it also has `[confirm]`, report the first text at the attribute's span. For each entry in `t.Edges()` whose target is in `a.tasks` and has no `[readonly]`, report at `e.Sp`, with the `[confirm]` text when the target has `[confirm]` and the other text otherwise. Use the exact texts from `contracts/readonly-attribute.md`. It does not walk: a comment says each task answers for its own references, so a chain gives one error. T030, T031, and T032 pass

**Checkpoint**: A false `[readonly]` cannot reach a client. All three stories work.

---

## Phase 6: Docs and delivery

**Purpose**: Docs, the amendment, gates, quickstart validation, and the PR (FR-015 to FR-017, research R7 and R8).

- [X] T035 [P] In `docs/GRAMMAR.md`, add the `"readonly"` alternative from `contracts/readonly-attribute.md` after `"network"`. In `docs/runefile.md`, add `[readonly]                      # claims the task changes nothing (MCP readOnlyHint)` to the attribute list after `[network]`, and add a paragraph after the `[confirm]` paragraph: `[readonly]` is the author's claim and Rune does not check the body, it changes nothing on the command line, and it cannot be used with `[confirm]` or on a task that runs one without `[readonly]`, with a link to the MCP guide's tool hints. Append to the `[confirm]` paragraph in `docs/runefile.md`: `Over MCP, a task that runs a [confirm] or [network] task through a dependency or hook advertises the same hint, but only the [confirm] task itself is refused.`
- [X] T036 [P] In `docs/mcp.md`, add a `### Tool hints` part at the end of "How tasks become tools", with a table of the three hints and when each is true from `contracts/mcp-tools.md`, what "can run" means, and these points: hints are advisory and never change what Rune runs or refuses; `[readonly]` is the author's claim and Rune does not read the body; a task without it makes no read-only claim; a `false` hint means only that nothing was declared; `RUNE2018` rejects a false `[readonly]`; a tool can advertise `destructiveHint: true` without being refused, and `--allow-destructive` names the `[confirm]` task itself, not a task that reaches it; and `[readonly]` on an `agent` task covers what the task runs, not the tools its agent picks at run time. Change the "Network tasks are flagged" bullet to say `[network]` sets `openWorldHint` on the task and on every task that can reach it. In the security example, add `[readonly]` to `status` and replace its comment with `# Claims to be read-only, so a client may run it without asking.`
- [X] T037 [P] Add the `RUNE2018` row from `contracts/readonly-attribute.md` to the Semantic table in `docs/diagnostics.md`
- [X] T038 [P] In `docs/examples/agent-driven/Runefile` and the identical block in `docs/use-cases/mcp-agents.md`: change the header to `exposed to agents over MCP; destructive tasks are refused unless the operator allows them, and only [readonly] tasks claim to be read-only.`, change the `list-tasks` comment to `# A read-only task an agent may call as a tool.`, and add `[readonly]` above its `[doc(...)]`. In `docs/examples/agent-driven/README.md`, say `list-tasks` is marked `[readonly]`, so it tells clients it changes nothing, and replace `tasks are **read-only by default**;` with the same statement. In `docs/use-cases/mcp-agents.md`, replace `rune list-tasks` is a safe, read-only tool with the same statement, and change the callout to start `Destructive tasks are **refused by default**. They are marked with `[confirm(...)]``
- [X] T039 [P] Replace the claim, with no em dash on any changed line. `README.md`: `Destructive tasks are refused until the operator allows them, only `[readonly]` tasks claim to be read-only, and secrets come from the environment only, never the Runefile.` `SECURITY.md`: `**Destructive tasks are refused by default.** The operator allows them one by one, a task that can reach a `[confirm]` task carries the MCP `DestructiveHint`, and only `[readonly]` tasks claim to be read-only.` `docs/user-guide/README.md`: `tasks are first-class MCP tools, and destructive tasks are refused until you allow them.` `website/src/landing/index.mdx`: `Every task is an MCP tool, and destructive tasks are refused until you allow them.`
- [X] T040 Confirm `.specify/memory/constitution.md` has the Principle VII wording from research R8, version 1.3.0, and the matching Sync Impact Report. The maintainer approved the amendment on 2026-11-10, and it was applied before implementation. If any of it is missing, stop and ask the maintainer
- [X] T041 Run every command under Commands in Docker: the full suite, the race suite, lint, and the golden regeneration, followed by `git diff --exit-code` on the host limited to files outside this feature's intended changes. Confirm `git diff origin/main --stat` and `git status --short` together list only the plan's files, since new files stay untracked until committed. Confirm that `git diff origin/main | grep -P '^\+.*\x{2014}'` and `grep -nP '\x{2014}'` over the four new files print nothing. Report any failure with its output
- [X] T042 Build a fresh binary in Docker and run `specs/033-mcp-tool-hints/quickstart.md` by hand: the tool list, other setups, calls, static errors, routes, and the docs `grep`. Every result must match. Record anything that does not, and fix it test-first
- [ ] T043 When the maintainer asks for the PR: run `git config user.name`, `gh auth status`, switch with `gh auth switch --user <name>` if the active account differs, and confirm with `gh auth status`. Open one PR from `033-mcp-tool-hints` titled exactly `fix(mcp)!: breaking: only tasks marked with the new [readonly] attribute claim to be read-only, so add it where true, and set mcpserver.TaskInfo.ReadOnly in embedders; destructive and network hints now cover dependencies and hooks`, with a body that starts `Closes #37.`, says a client that relied on the old claim now asks for approval, describes the `[readonly]` rule beyond the issue (FR-008), `||` hooks counting, and the constitution amendment, and says `TestToolHintsMCP_CoverDependencies` replaces `TestMCPE2E_Pending_HintsCoverDependencies`, which the issue lists but this repository does not have (FR-017, research R7). Add no `Co-Authored-By` trailer or "Generated with" line. Wait for the PR checks to finish green before reporting done. Use `gh api` with PATCH for edits, since `gh pr edit` fails in this repository

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: after Setup. Blocks US1 and US3. US2's unit tests do not use the attribute, but its binary-level tests share the fixture that does.
- **US1 (Phase 3)**: after Foundational.
- **US2 (Phase 4)**: after Foundational. Its code is independent of US1, except that T013 and T027 edit the same struct and T014 and T028 the same function, so do them in sequence. T022 to T025 reuse the fixture and helper from T010; if US2 goes first, create those in T022. T020's `[readonly] status` assertion needs T014.
- **US3 (Phase 5)**: after Foundational. Independent of US1 and US2 in code. T032 reuses `test/integration/tool_hints_mcp_test.go`; if US3 goes first, create the file there.
- **Docs and delivery (Phase 6)**: after all stories. T040 before T041. T043, the PR, comes last.

### Within each phase

- T003 and T004 before T005: watch the failures first. T005 before T006, whose fixture must parse.
- T010 before T011, T022, T023, T024, T025: same new file, and they reuse its fixture.
- T012 before T013; T026 before T027; T033 before T034.
- T013 before T014; T027 before T028: the adapter sets a field the library must define first.
- T018, T019, T020, and T009 all edit `internal/cli/mcp_test.go`, so they run in sequence.
- T008 and T015 both edit `mcpserver/server_test.go`, so they run in sequence.
- T021 before T024, which passes a flag.

### Parallel opportunities

- Phase 2: T003, T004, and T007.
- Phase 3: T008 and T009, beside T010.
- Phase 4: T015, T016, T017, and T018 touch four different files.
- Phase 5: T030, T031, and T032 touch three different files.
- Phase 6: T035 to T039 touch different documents.

---

## Parallel Example: User Story 2

```text
Task: "T015 Hint derivation for ReachesDestructive in mcpserver/server_test.go"
Task: "T016 ReachesDestructive is not refused in mcpserver/authz_test.go"
Task: "T017 Hints over HTTP in mcpserver/transport_test.go"
Task: "T018 Reach on every edge kind in internal/cli/mcp_test.go"
```

## Parallel Example: User Story 3

```text
Task: "T030 RUNE2018 texts and spans in internal/analyzer/readonly_test.go"
Task: "T031 RUNE2018 reaches the editor in internal/analysis/service_test.go"
Task: "T032 Quickstart U to Z on the binary in test/integration/tool_hints_mcp_test.go"
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phases 1 and 2: confirm the start, record the baseline, add the attribute.
2. Phase 3: write the failing tests, then drop the default read-only claim.
3. Stop and validate with quickstart rows A to E and M, and scenario Q. This closes the access problem
   issue #37 leads with: a client can no longer treat `deploy` as read-only.

### Incremental delivery

All phases ship in one PR. Within the branch:

1. Phases 1 to 3: MVP.
2. Phase 4: hints cover dependencies and hooks, and calls stay the same.
3. Phase 5: the static rule.
4. Phase 6: docs, the amendment, gates, quickstart, and the PR when asked.

---

## Notes

- `[P]` tasks touch different files and depend on no incomplete task.
- `isDestructive` in `mcpserver/authz.go` must not change. A call's outcome stays with spec 031, and T016 and T025 guard that.
- Stage this feature's files by name. `.docs/` is unrelated and stays out of every commit.
- The other items under "Observed, not changed" in research.md are separate work.
- Any bug a code review finds in this work gets a test first: write it, watch it fail against the bug, fix, watch it pass.
- Out of scope, tracked elsewhere: the bare `[exit 1]` on a declined dependency (#38), run-time enforcement (#51).
