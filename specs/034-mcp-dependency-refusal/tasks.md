---

description: "Task list for spec 034: refuse a destructive dependency before the call runs"
---

# Tasks: Refuse a Destructive Dependency Before the Call Runs

**Input**: Design documents from `/specs/034-mcp-dependency-refusal/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer) and FR-017. Every test that
covers new behavior is written and seen failing before the code that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1, US2, US3)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
- The refusal texts are fixed by [contracts/mcp-tools.md](contracts/mcp-tools.md#refusal-texts).
  Tests pin them as literal strings and do not rebuild them with the production rule.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass. The three changed expectations are listed in
  [research R7](research.md#r7-test-layers) and are intended behavior changes.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` on branch `034-mcp-dependency-refusal` and confirm it is green. If anything fails, report it and stop: do not start Phase 2 on a red baseline.

---

## Phase 2: Foundational (the descriptor field, and `refusal` takes the descriptor)

**Purpose**: Two changes that preserve today's behavior and that every story reads:

- The descriptor carries the ordered names of the destructive tasks a run can reach
  ([research R2, R3](research.md#r2-the-descriptor-field)).
- `refusal` takes the handler's descriptor in place of a name
  ([research R5](research.md#r5-the-handler-passes-its-descriptor)).

Hints and refusal texts do not change in this phase. Because the signature change lands here, the
story tests in Phase 3 compile and fail on their assertions, not on a compile error.

**CRITICAL**: No user story work begins until T011 is green.

### Tests first

- [X] T002 [P] In `internal/cli/mcp_test.go`, change the test type `reach` from `struct{ destructive, reaches, network bool }` to `struct { destructive bool; reaches []string; network bool }`. Fill `reaches` from `ti.DestructiveReach` in `reachOf`. In `TestAdapterReach`, compare with `g.destructive == want.destructive && g.network == want.network && slices.Equal(g.reaches, want.reaches)`, because a struct holding a slice cannot use `!=`. Replace each `reaches: true` with the names in reach order:
  - "dependency": `release: ["clean"]`
  - "post-hook": `post: ["clean"]`
  - "failure hook": `bad: ["clean"]`
  - "three levels, confirm": `a`, `b`, and `c` each `["d"]`
  - "private target": `ship: ["_wipe"]`
  - "diamond": `top`, `left`, and `right` each `["base"]`
  - "failure hook of a failure hook, confirm": `cleanup: ["wipe"]`
  - "what a failure hook runs": `bad: ["x"]`
  - "failure hook also a dependency": `top: ["wipe"]`

  Entries with no `reaches` keep a nil list.
- [X] T003 In `internal/cli/mcp_test.go` (after T002), add three cases to `TestAdapterReach`. Each one gives every task a body and gives each named task `[confirm]`:
  - "depth-first order": `ordered: stage clean` and `stage: wipe` give `ordered: ["wipe", "clean"]` and `stage: ["wipe"]`.
  - "edge kinds in order": `t: d && p || f` gives `t: ["d", "p", "f"]`. `main`'s parser accepts this syntax.
  - "failure hook first, dependency later": `top: a b`, `a: || cleanup`, `b: cleanup`, and `cleanup: || wipe` give `top: ["wipe"]`, `a: nil`, and `b: ["wipe"]`.

  Then change `TestAdapterReachFollowsHostOS` to expect names and compare them with `slices.Equal`. Its error message prints `DestructiveReach`.
  - On linux and darwin: `dispatch: nil`, `viawin: nil`, `both: ["clean"]`.
  - On windows: `dispatch: ["wipe-win"]`, `viawin: ["clean"]`, `both: ["clean"]`.
- [X] T004 [P] In `mcpserver/server_test.go`, rename `TestToolHintsReachesDestructive` to `TestToolHintsDestructiveReach`. Its descriptors become `{Name: "release", DestructiveReach: []string{"clean"}}` and `{Name: "clean", Destructive: true, DestructiveReach: []string{"wipe"}}`, and both still want `hintSet{destructive: true}`. Add `{Name: "logs", DestructiveReach: []string{}}`, which must give `hintSet{}`. In `TestListToolsSameWithGrantWhenReaching` and `TestListToolsSameWithAllowList`, replace `ReachesDestructive: true` with `DestructiveReach: []string{"clean"}`.
- [X] T005 [P] In `mcpserver/transport_test.go` `TestHTTPToolsListCarriesHints`, replace `{Name: "release", ReachesDestructive: true}` with `{Name: "release", DestructiveReach: []string{"clean"}}`.
- [X] T006 [P] In `mcpserver/authz_test.go`, make two changes and keep every `want` unchanged:
  - Change every `srv.refusal("<name>")` and `granted.refusal("<name>")` call to pass a descriptor literal matching the engine's entry, for example `srv.refusal(TaskInfo{Name: "clean", Destructive: true})`, `srv.refusal(TaskInfo{Name: "logs"})`, and `srv.refusal(TaskInfo{Name: "tools::clean", Destructive: true})`.
  - In `TestRefusalIgnoresReachesDestructive`, replace `ReachesDestructive: true` with `DestructiveReach: []string{"clean"}`, and pass `TaskInfo{Name: "release", DestructiveReach: []string{"clean"}}` to `refusal`. It keeps passing until T012 replaces it.
- [X] T007 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/...`. Confirm it fails to compile for two reasons: `TaskInfo` has no field `DestructiveReach`, and `refusal` takes a string.

### Implementation

- [X] T008 In `mcpserver/server.go`, make three changes:
  - In `TaskInfo`, replace `ReachesDestructive bool` and its comment with `DestructiveReach []string`. Copy its doc comment verbatim from [contracts/mcpserver-api.md](contracts/mcpserver-api.md#taskinfo).
  - In `toolFor`, set `destructive := t.Destructive || len(t.DestructiveReach) > 0`.
  - Replace the doc comment on `Options.AllowDestructive` with the contract's text, verbatim.
- [X] T009 In `internal/cli/mcp.go`, rewrite `reach` as a recursive depth-first walk that returns `(destructive []string, network bool)`. The edge rules are spec 033's. The order is new:
  - Mark `t` visited first, so it is never named.
  - From each task, follow its `Deps` in declared order, then its `PostHooks`, then its `FailHooks`.
  - Skip a name that is not in `a.tasks` or not `AvailableOn(a.goos)`, and do not walk past it.
  - A task reached through a `FailHooks` edge walks its own `Deps` and `PostHooks` but not its `FailHooks`, and is marked hook-only. If an ordinary edge reaches it later, walk its `FailHooks` at that point and clear the mark.
  - Append a task that has `ast.AttrConfirm` at its first visit only. Leave the list nil when no task is named.
  - Compute `network` as today.

  In `Tasks()`, set `DestructiveReach` from the walk in place of `ReachesDestructive`. Update the doc comment of `reach` to state the order (spec 034 FR-005).
- [X] T010 Change the signature of `refusal`, with no change to any text or behavior ([research R5](research.md#r5-the-handler-passes-its-descriptor)):
  - In `mcpserver/authz.go`, change `refusal(taskName string)` to `refusal(t TaskInfo)`. Use `t.Name` in the allow-list check and in the texts, and replace `s.isDestructive(taskName)` with `t.Destructive`. Delete `isDestructive`.
  - In `mcpserver/handler.go`, call `s.refusal(t)` in place of `s.refusal(taskName)`.
- [X] T011 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/...` and confirm it is green. Refusal behavior has not changed yet.

**Checkpoint**: Descriptors carry `DestructiveReach`, `refusal` reads the handler's descriptor, and
`tools/list` is unchanged (FR-015).

---

## Phase 3: User Story 1: A call that needs an unallowed destructive task does nothing and says why (Priority: P1) MVP

**Goal**: A call that can reach an ungranted `[confirm]` task is refused before anything runs. The
refusal names those tasks and the flag (FR-001 to FR-005, FR-009, FR-010).

**Independent Test**: Serve the quickstart Runefile with no grant and call `release`. The result is
`task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and
restart the server`, and no `*.done` file exists.

This phase also holds US3's route tests (T019 to T021). They test the code T023 writes, so they are
written here and seen failing in T022, as Principle VI requires.

### Tests for User Story 1 (write first, watch them fail)

- [X] T012 [P] [US1] In `mcpserver/authz_test.go`, replace `TestRefusalIgnoresReachesDestructive` with `TestRefusalCoversReach`. Use engine `{Name: "release", DestructiveReach: []string{"clean"}}` and `Options{AllowDestructiveFlag: grantFlag}`.
  - `refusal` returns `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`.
  - A `tools/call` of `release` over `connect` is `IsError` with that text, and `eng.lastCall == ""`.
- [X] T013 [US1] In `mcpserver/authz_test.go` (after T012), add the table test `TestRefusalNamesReachedTasks`, defining its cases at file level so T026 can reuse them. Each case is a descriptor, grants, a flag, and the wanted text. Use `--allow-destructive` as the flag unless the row says otherwise:

  | Descriptor | Grants | Want |
  |---|---|---|
  | `release`, reach `clean`, `wipe` | none | `task "release" can run destructive tasks "clean", "wipe"; to permit them, add clean,wipe to --allow-destructive and restart the server` |
  | `release`, reach `clean`, `wipe` | `clean` | `task "release" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server` |
  | `release`, reach `clean`, `wipe` | `clean`, `wipe` | `""` |
  | `shipit`, reach `tools::clean` | none | `task "shipit" can run destructive task "tools::clean"; to permit it, add tools::clean to --allow-destructive and restart the server` |
  | `release`, reach `clean`, no flag | none | `task "release" can run destructive task "clean"; it is not allowed in this session` |
  | `release`, reach `clean`, `wipe`, no flag | none | `task "release" can run destructive tasks "clean", "wipe"; they are not allowed in this session` |

  Also add `TestRefusalAllowListBeforeReach`. With `Options{AllowList: []string{"logs"}, AllowDestructiveFlag: grantFlag}` and `{Name: "release", DestructiveReach: []string{"clean"}}`, `refusal` returns `task "release" is not authorized for this session`.
- [X] T014 [P] [US1] In `mcpserver/handler_test.go`, add `TestHandlerReachRefusalBeforeArguments` (FR-010, quickstart R). Use a `fakeEngine` with `{Name: "release", Params: []ParamInfo{{Name: "target"}}, DestructiveReach: []string{"clean"}}` and `Options{AllowDestructiveFlag: "--allow-destructive"}`. `callTool(t, cs, "release", map[string]any{"bogus": 1})` is `IsError` with exactly `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`, not the unknown-argument error, and `eng.lastCall == ""`.
- [X] T015 [P] [US1] In `internal/cli/mcp_test.go`, add `TestServerRefusesReachOnHostOS` (FR-003, quickstart N, Windows half).
  - Build `a := adapterFor(t, "dispatch: wipe-win\n    @echo dispatch\n[windows, confirm(\"Wipe?\")]\nwipe-win:\n    @echo wipe\n")` and set `a.goos = "windows"`.
  - Connect with `serverSession(t, mcpserver.New(a, mcpserver.Options{Version: "test", AllowDestructiveFlag: "--allow-destructive"}))`.
  - Calling `dispatch` is `IsError` with `task "dispatch" can run destructive task "wipe-win"; to permit it, add wipe-win to --allow-destructive and restart the server`.
- [X] T016 [P] [US1] Create `test/integration/dependency_refusal_mcp_test.go`, package `integration`, starting with this setup:
  - A comment saying it covers spec 034 FR-017 in place of the issue's `TestMCPE2E_Pending_DependencyRefusalExplained`, which this repository does not have.
  - `const dependencyRefusalRunefile`, the quickstart scratch Runefile copied verbatim.
  - A helper `doneFiles(t, dir) []string` built on `filepath.Glob(filepath.Join(dir, "*.done"))`.

  Each row runs in a fresh `writeRunefile` directory through `toolCall(t, dir, flags, task)`. "Refused" means `isError`, exactly the given text, and `doneFiles` empty.
  - **Refused before anything runs.** `TestDependencyRefusalMCP_RefusedBeforeAnythingRuns` is a table over these rows:
    - A `release`, no grant: `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
    - C `full`, no grant: `task "full" can run destructive tasks "clean", "wipe"; to permit them, add clean,wipe to --allow-destructive and restart the server`
    - D `full`, `--allow-destructive clean`: `task "full" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`
    - E `ordered`, no grant: `task "ordered" can run destructive tasks "wipe", "clean"; to permit them, add wipe,clean to --allow-destructive and restart the server`
    - F `publish`, no grant: `task "publish" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
    - G `deploy`, no grant: `task "deploy" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`
    - K `outer`, no grant: `task "outer" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`
    - O `rollback`, no grant: `task "rollback" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`
  - **Private task.** `TestDependencyRefusalMCP_PrivateTask` (row L): `ship` is refused with `task "ship" can run destructive task "_scrub"; to permit it, add _scrub to --allow-destructive and restart the server`. The map from `toolHints(t, dir)` has no `_scrub` key.
  - **Imported task.** `TestDependencyRefusalMCP_ImportedTask` (row M) uses its own Runefile, `mod tools "tools.rune"` plus `shipit: tools::clean`, whose body writes `ship.done`. `tools.rune` holds a `[confirm("Delete dist?")]` task `clean` whose body writes `clean.done`, as in `TestToolHintsMCP_ImportedTask`. `shipit` is refused with `task "shipit" can run destructive task "tools::clean"; to permit it, add tools::clean to --allow-destructive and restart the server`.
  - **Host OS.** `TestDependencyRefusalMCP_HostOS` (row N): when `runtime.GOOS != "windows"`, `dispatch` is not an error, its text contains `[exit 0]`, and `dispatch.done` exists. On Windows it is refused with `task "dispatch" can run destructive task "wipe-win"; to permit it, add wipe-win to --allow-destructive and restart the server`.
  - **Failure hook of a failure hook.** `TestDependencyRefusalMCP_FailureHookOfFailureHook` (row P): `risky` is `isError`, its text contains `[exit 1]` and does not contain `destructive`, and `wipe.done` does not exist.
  - **Before arguments.** `TestDependencyRefusalMCP_BeforeArguments` (row R): run `line, _ := mcpServerCall(t, dir, []string{"mcp", "--color=never"}, "release", `{"bogus": 1}`)` and then `toolReply(t, line)`. The result is row A's text, and `doneFiles` is empty.
- [X] T017 [P] [US1] In `test/integration/destructive_mcp_test.go` `TestDestructiveMCP_DependencyFollowsGrant`, replace the check `!isError || !strings.Contains(text, "[exit 1]")` with `!isError || text != want`, where `want` is `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`. Update the error message and the comment to say that an ungranted dependency refuses the call before anything runs (spec 034). Keep the marker checks.
- [X] T018 [P] [US1] In `test/integration/tool_hints_mcp_test.go` `TestToolHintsMCP_CallsUnchanged`, change two calls and two comments:
  - **`release` with no grant.** Require `text ==` the text `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`, in place of the `strings.Contains(text, "is destructive")` check.
  - **`release` with only `--allow-destructive release`.** Require that same text as well. Keep the stderr warning check and the marker checks.
  - **Comments.** Rewrite the one saying the bare `[exit 1]` is not pinned because issue #38 will change it. Rewrite the one saying refusal reads only the task's own `[confirm]`: granting `release` permits nothing, and the refusal names `clean`.

### Route tests for User Story 3 (written here so they fail before T023)

- [X] T019 [US3] In `mcpserver/authz_test.go` (after T013), add `TestRefusalEmbedderFlag` (US3.2). With `Options{AllowDestructiveFlag: "--grant"}` and `{Name: "release", DestructiveReach: []string{"clean"}}`, `refusal` returns `task "release" can run destructive task "clean"; to permit it, add clean to --grant and restart the server`.
- [X] T020 [P] [US3] In `mcpserver/transport_test.go`, add `TestHTTPReachRefused` (quickstart T).
  - Use a `syncEngine` with tasks `{Name: "release", DestructiveReach: []string{"clean"}}` and `{Name: "clean", Destructive: true}`, and `Options{AllowDestructiveFlag: "--allow-destructive"}`.
  - Call `StartHTTP` with a token and register `t.Cleanup(stop)` before calling `httpSession`, as the file's comment requires.
  - Calling `release` is `IsError` with `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`, and `eng.called()` is empty.
- [X] T021 [US3] In `internal/cli/mcp_test.go` (after T015), add `TestAgentEndpointRefusesReach` (US3.1, quickstart S).
  - Build `src` with `release: clean` and `[confirm] clean`. Each body writes a marker under `t.TempDir()`, as `TestAgentEndpointRefusesUnknownArgument` does.
  - Call `eng, _, _ := confirmEngine(t, src, nil, false, false)` and set `eng.grants = map[string]bool{"clean": true}` to show that the endpoint ignores the engine's grant (spec 031 FR-015).
  - Connect with `serverSession(t, mcpserver.New(eng.newAgentAdapter(), mcpserver.Options{Version: "test"}))`.
  - Calling `release` is `IsError` with `task "release" can run destructive task "clean"; it is not allowed in this session`, and no marker exists.

### Red run

- [X] T022 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...`. Confirm every test from T012 to T021 fails on its assertion, for the missing behavior:
  - The library tests get `""` from `refusal`, or the call reaches the engine.
  - The adapter, agent endpoint, and integration tests get an `[exit 1]` result or a run in place of the refusal.

### Implementation for User Story 1

- [X] T023 [US1] Add the reach refusal:
  - In `mcpserver/authz.go` `refusal`, keep the allow-list check first and the own check `t.Destructive && !s.granted[t.Name]` with today's two texts. In this story the own check still returns early; US2 joins the two checks.
  - Then collect each name in `t.DestructiveReach` that is not in `s.granted`, in the given order, without checking or reordering the names.
  - When any names remain, build the subject: `task %q can run destructive task %q` for one name, or `task %q can run destructive tasks ` followed by the `%q` names joined with `, ` for two or more.
  - Follow the subject with `; to permit it, add <names joined with ","> to <AllowDestructiveFlag> and restart the server` when the flag is set, or `; it is not allowed in this session` when it is empty. Use `them` and `they are` for two or more names.
  - Update the doc comment of `refusal` to say what is refused and that the reached names come from the handler's descriptor.
  - In `mcpserver/handler.go`, update the handler's doc comment: authorization covers the destructive tasks the descriptor says the call can reach.
- [X] T024 [US1] Run the T022 command and confirm it is green, including US3's route tests T019 to T021.

**Checkpoint**: US1 works on its own. `reset` with no grant still gets today's text, which names only
`reset`. US2 fixes that.

---

## Phase 4: User Story 2: The operator follows the message once (Priority: P1)

**Goal**: One refusal names every task to add, the called task first, so one restart permits the
call (FR-005 to FR-007, FR-011, SC-003, SC-005). US2 scenario 2, several reached names, is already
covered by rows C and D in T013 and T016.

**Independent Test**: Call `reset` with no grant. It is refused with `task "reset" is destructive and
can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart
the server`. A restart with `reset,wipe` added lets the call run.

### Tests for User Story 2 (write first, watch them fail)

- [X] T025 [US2] In `mcpserver/authz_test.go`, add the table test `TestRefusalOwnAndReached`, defining its cases at file level next to T013's. The descriptor is `{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}}`:

  | Flag | Grants | Want |
  |---|---|---|
  | `--allow-destructive` | none | `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server` |
  | `--allow-destructive` | `reset` | `task "reset" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server` |
  | `--allow-destructive` | `wipe` | `task "reset" is destructive; to permit it, add reset to --allow-destructive and restart the server` |
  | `--allow-destructive` | `reset`, `wipe` | `""` |
  | none | none | `task "reset" is destructive and can run destructive task "wipe"; they are not allowed in this session` |
  | none | `wipe` | `task "reset" is destructive and is not allowed in this session` |

  Add one more row: `{Name: "full", Destructive: true, DestructiveReach: []string{"clean", "wipe"}}` with the flag and no grants wants `task "full" is destructive and can run destructive tasks "clean", "wipe"; to permit them, add full,clean,wipe to --allow-destructive and restart the server`.
- [X] T026 [US2] In `mcpserver/authz_test.go` (after T025), add `TestRefusalOneRestartIsEnough` (FR-006, SC-003). For every case from T013 and T025 that has a flag and a non-empty want:
  - Take the text between `add ` and ` to --allow-destructive` and split it on `,`.
  - Append those names to the case's grants and build a new server.
  - Require `refusal` to return `""`.
- [X] T027 [P] [US2] In `test/integration/dependency_refusal_mcp_test.go` (after T016), add `TestDependencyRefusalMCP_GrantsAndOrder`, a table over these rows:
  - B `release`, `--allow-destructive clean`: not an error. The text contains `[exit 0]`, and `build.done`, `clean.done`, and `release.done` exist.
  - H `reset`, no grant: refused with `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server`.
  - I `reset`, `--allow-destructive reset`: refused with `task "reset" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`.
  - J `reset`, `--allow-destructive reset,wipe`: not an error. `wipe.done` and `reset.done` exist.
  - Q `clean`, no grant: refused with `refusedText("clean")`, which is byte-identical to today's text (SC-005).

  Also add `TestDependencyRefusalMCP_OneRestartIsEnough`. For rows A, C, E, F, G, H, K, L, and O:
  - Call with no grant and take the names between `add ` and ` to --allow-destructive`.
  - Call again in a fresh directory with `--allow-destructive <names>`.
  - Require the second text not to contain `to permit`. Row O still fails with `[exit 1]` from its own body, which is not a refusal.
- [X] T028 [US2] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./test/integration/... -run 'Refusal|DependencyRefusalMCP'`. Confirm the `reset` and `full` own-and-reached cases fail with today's own-only text.

### Implementation for User Story 2

- [X] T029 [US2] In `mcpserver/authz.go`, replace the early own-only return with one named list: the called task first when `t.Destructive && !s.granted[t.Name]`, then the ungranted reached names.
  - **Subject.** Own only: `task %q is destructive`. Reached only: as in T023. Both: `task %q is destructive and can run destructive task(s) ...`.
  - **Pronouns.** Choose `it` and `is`, or `them` and `are`, by the length of the named list.
  - **Flag value.** The named list joined with `,`.
  - **No-flag exception.** The own-only case keeps `task %q is destructive and is not allowed in this session`.
  - **Byte-identity.** The own-only text with a flag must come out byte-identical to today's from the general rule, with no special case (FR-007, research R4).
- [X] T030 [US2] Run the T028 command, then `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`. Confirm both are green.

**Checkpoint**: US1 and US2 both work. Every refusal over `rune mcp` names everything to add.

---

## Phase 5: User Story 3: Every server Rune builds refuses the same way (Priority: P2)

**Goal**: The agent task's endpoint, `rune serve --http`, and embedders get the same refusal
(FR-008, FR-014, US3).

**Independent Test**: From an `agent` task's endpoint, call `release`. It is refused with `task
"release" can run destructive task "clean"; it is not allowed in this session`, and nothing ran.

US3's route tests (T019 to T021) were written in Phase 3 and seen failing in T022. These routes share
`refusal`, so no production code is expected ([research R1](research.md#r1-where-the-check-runs)). If
a route test fails, that route bypasses the shared refusal: fix the route, not the test. This phase
adds the guard for embedders that report nothing.

- [X] T031 [P] [US3] In `mcpserver/authz_test.go`, add `TestRefusalNilReachUnchanged` (US3.3, FR-014, quickstart U). For `{Name: "release"}` with a nil `DestructiveReach`, `refusal` returns `""`, and a `tools/call` over `connect` reaches the engine (`eng.lastCall == "release"`). This test guards today's behavior, so it passes before and after this feature.
- [X] T032 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/...` and confirm it is green.

**Checkpoint**: Every route refuses the same calls with the contract's texts.

---

## Phase 6: Polish and Cross-Cutting Concerns

**Purpose**: Documentation (FR-016, [research R8](research.md#r8-documentation)) and the full gates.

- [X] T033 In `docs/mcp.md`, Tool hints section, replace the bullet "A tool can advertise `destructiveHint: true` without being refused. Only a `[confirm]` task itself is refused, so `--allow-destructive` names that task, not a task that reaches it." The new bullet says two things:
  - A call to such a tool is refused unless every `[confirm]` task it can run is granted.
  - The refusal names those tasks, which are the names `--allow-destructive` takes, not the task that reaches them.
- [X] T034 In `docs/mcp.md` (after T033), Security model section, extend the bullet "**Destructive tasks are refused by default.**": a call that can run an ungranted `[confirm]` task through a dependency or hook is refused before anything runs, and the refusal names every task to add. Leave "Hints are advisory. They never change what Rune runs or refuses." as it is: refusal reads the same declarations, not the hints.
- [X] T035 [P] In `docs/runefile.md`, in the `[confirm]` paragraph, replace "advertises the same MCP hint, but only the `[confirm]` task itself is refused." with: advertises the same MCP hint, and over MCP a call that can run an ungranted `[confirm]` task is refused before anything runs.
- [X] T036 [P] In `SECURITY.md`, Security model section, extend the bullet "**Destructive tasks are refused by default.**" to say that a call that can reach an ungranted `[confirm]` task is refused too, before anything runs.
- [X] T037 Run the docs checks and confirm both pass:
  - `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` is green.
  - `grep -n -i 'itself is refused\|without being refused' docs/mcp.md docs/runefile.md SECURITY.md` has no hit.
- [X] T038 Run the full gates from [quickstart.md](quickstart.md#automated-gates-docker-only) and confirm all three are green: `go test ./...`, `go test -race ./...` with `CGO_ENABLED=1`, and `golangci-lint run` with the `golangci/golangci-lint:v2.12` image.
- [X] T039 Build the branch binary in the scratchpad, then spot-check it:
  - From a scratch directory holding the quickstart Runefile, call rows A, H, and P over stdio. Confirm the texts match [contracts/mcp-tools.md](contracts/mcp-tools.md) and that A and H leave no `*.done` file.
  - Compare with "Before and after" in the quickstart: on `main`, A returns a bare `[exit 1]`.
- [X] T040 Finish with two checks:
  - Scan for the em dash in every line this branch adds and in every new file, with `git diff main -U0 | grep -nP '^\+.*\x{2014}'` and `git ls-files --others --exclude-standard | xargs grep -lP '\x{2014}'`, and confirm neither has a hit. Scanning whole changed files would also hit em dashes already on `main`, which this feature does not touch.
  - Run `git status` and confirm only the files in plan.md's Project Structure, plus `tasks.md`, changed.

---

## Dependencies and Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: after T001. Blocks every story, because all of them read
  `DestructiveReach` and the new `refusal` signature.
- **US1 (Phase 3)**: after Phase 2. Phase 3 also holds US3's route tests T019 to T021.
- **US2 (Phase 4)**: after US1. T029 reworks the `refusal` branch that T023 writes, and T027 extends
  the file T016 creates.
- **US3 (Phase 5)**: after US1. It needs only US1's reach refusal, so it can run alongside US2.
  T031 shares `mcpserver/authz_test.go` with T025 and T026, so do not edit them at the same time.
- **Polish (Phase 6)**: after all stories. The docs describe the final behavior.

### Within each story

- Tests first, then run them red (T007, T022, T028), then the implementation, then run green.
- Tasks in the same file run in order:
  - T002 then T003
  - T012 then T013 then T019
  - T015 then T021
  - T025 then T026
  - T016 then T027
  - T033 then T034

### Parallel opportunities

- Phase 2 tests: T002, T004, T005, and T006 touch four different files.
- Phase 3 tests: T012, T014, T015, T016, T017, T018, and T020 touch seven different files.
- Polish: T035 and T036 can run with T033.

---

## Parallel Example: User Story 1

```text
Task: "T012 TestRefusalCoversReach in mcpserver/authz_test.go"
Task: "T014 TestHandlerReachRefusalBeforeArguments in mcpserver/handler_test.go"
Task: "T015 TestServerRefusesReachOnHostOS in internal/cli/mcp_test.go"
Task: "T016 Create test/integration/dependency_refusal_mcp_test.go"
Task: "T017 Pin the refusal in test/integration/destructive_mcp_test.go"
Task: "T018 Pin the refusal in test/integration/tool_hints_mcp_test.go"
Task: "T020 TestHTTPReachRefused in mcpserver/transport_test.go"
```

---

## Implementation Strategy

### MVP (User Story 1)

1. Phase 1, then Phase 2: the descriptor carries ordered reach, `refusal` reads it, and nothing
   visible changes.
2. Phase 3: calls that reach an ungranted `[confirm]` task are refused before anything runs.
3. Stop and validate: T024 green, and quickstart rows A, F, G, K, L, M, O, P, and R pass.

US1 alone already closes the side effects and the bare `[exit 1]` the issue reports. US2 is also P1,
because without it a destructive called task that reaches another one still needs two restarts.
Ship US1 and US2 together.

### Incremental delivery

1. Foundation, then US1: refusal for reached tasks, with the route tests for every server.
2. US2: one message for own and reached tasks, so one restart is always enough.
3. US3: the guard for embedders that report nothing, and a check of every route.
4. Polish: docs and the full gates.

---

## Notes

- Commit only when the maintainer asks: one `fix(mcp)!:` commit with the behavior change in its
  subject ([research R9](research.md#r9-release-notes)). Leave `.docs/` out of the commit.
- `internal/cli/confirm.go` does not change. The engine's run-time decline stays as the backstop
  (FR-013, [research R6](research.md#r6-the-run-time-decline-stays)).
- These tests keep passing unchanged and need no edit:
  - `TestDestructiveMCP_GrantDoesNotWiden`, `TestDestructiveMCP_UngrantedHooksDoNotRun`, and
    `TestDestructiveMCP_NoFlagRunsNothingDestructive`. Each checks `isError` and markers only.
  - `TestToolHintsMCP_CoverDependencies` and `TestToolHintsMCP_GrantChangesNothing` (FR-015).
