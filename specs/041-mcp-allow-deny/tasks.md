---

description: "Task list for spec 041: operator allowlist and denylist for MCP tasks"
---

# Tasks: Operator Allowlist and Denylist for MCP Tasks

**Input**: Design documents from `/specs/041-mcp-allow-deny/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer). Every test that covers new
behavior is written and seen failing before the code that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US5)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
- The flag errors, warnings, and startup lines are fixed by
  [contracts/cli-serve.md](contracts/cli-serve.md). Tests pin them as literal strings and do not
  rebuild them with the production rule.
- The `--allow-destructive` messages and the no-filter startup lines stay byte-identical. The
  existing tests that pin them must pass unchanged in assertion, even where a helper is renamed.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass. The five tests whose expectations change are listed in
  [research R10](research.md#r10-tests) and are intended behavior changes.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` on branch `041-mcp-allow-deny` and confirm it is green. If anything fails, report it and stop: do not start Phase 2 on a red baseline.

---

## Phase 2: Foundational (shared helpers, no behavior change)

**Purpose**: Three refactors that every story builds on and that change no output:

- One parser and one name check that take the flag name ([research R4](research.md#r4-one-parser-and-one-name-check-for-all-three-flags)).
- A startup line built from parts ([research R8](research.md#r8-startup-line)).
- The server counts what it registers ([research R3](research.md#r3-the-server-reports-how-many-tasks-it-exposed)).

**CRITICAL**: No user story work begins until T008 is green.

### Tests first

- [X] T002 [P] In `internal/cli/serve_test.go`, rename `TestParseAllowDestructive` to `TestParseTaskNames`, `TestParseAllowDestructiveRejectsEmptyName` to `TestParseTaskNamesRejectsEmptyName`, and `TestCheckGrant` to `TestCheckTaskNames`. Change their calls to `parseTaskNames("--allow-destructive", values)` and `checkTaskNames("--allow-destructive", names, tasks)`, keeping every expected string as it is. Add to each a table dimension over the flags `--allow-destructive`, `--allow`, `--deny`, expecting `<flag>: empty task name`, `unknown task in <flag>: claen (did you mean "clean"?)`, and `unknown task in <flag>: zzz` as literal strings per flag. The file must fail to compile until T005.
- [X] T003 In `internal/cli/serve_test.go` (same file as T002, do after it), change `TestStartupLine` to call the new signature `startupLine(useHTTP bool, addr string, exposure *[2]int, granted []string)` with `exposure` nil for every existing case, keeping the four expected strings unchanged.
- [X] T004 [P] In `mcpserver/server_test.go`, add `TestExposureUnfiltered`: `New(sampleEngine(), Options{}).Exposure()` returns `exposed == exposable == len(sampleEngine().Tasks())`. It fails to compile until T007.

### Implementation

- [X] T005 In `internal/cli/serve.go`, rename `parseAllowDestructive(values)` to `parseTaskNames(flag string, values []string)` and `checkGrant(names, tasks)` to `checkTaskNames(flag string, names []string, tasks map[string]*ast.Task)`. Build the messages from `flag`: `usagef("%s: empty task name", flag)`, `usagef("unknown task in %s: %s (did you mean %q?)", flag, name, s)`, `usagef("unknown task in %s: %s", flag, name)`. Update the doc comments to say the helpers serve every task-list flag. Update the two call sites in `ServeMCP` to pass `"--allow-destructive"`. Update any other caller found with `grep -rn "parseAllowDestructive\|checkGrant" --include='*.go' .`.
- [X] T006 In `internal/cli/serve.go`, rewrite `startupLine` to the signature in T003. Build the parenthesized text from parts joined with `"; "`, in this order: `token required` when `useHTTP`; `fmt.Sprintf("%d of %d tasks exposed", exposure[0], exposure[1])` when `exposure != nil`; `destructive tasks allowed: ` plus the names joined with `", "` when `granted` is non-empty. With no parts the line has no parentheses. The prefix is `rune MCP server on http://ADDR` or `rune MCP server on stdio`. Update the call in `ServeMCP` to pass `nil` for `exposure`.
- [X] T007 In `mcpserver/server.go`, add unexported fields `exposed, exposable int` to `Server`. In `New`, increment `exposable` for every task from `engine.Tasks()` and `exposed` for every task registered (all of them in this phase). Add `// Exposure reports how many tasks are exposed as tools and how many the Engine offered.` and `func (s *Server) Exposure() (exposed, exposable int)`.
- [X] T008 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...` and confirm green with no assertion changed outside T002 to T004.

**Checkpoint**: Helpers take a flag name, the startup line can carry counts, the server counts. No output differs from `main`.

---

## Phase 3: User Story 1 - Operator exposes only the tasks one agent needs (Priority: P1) MVP

**Goal**: `rune mcp --allow build,test` lists exactly `build` and `test`, and a call to any other
task is the unknown-tool error.

**Independent Test**: Quickstart scenarios A to E, O, and R, and the startup-line rows of P for
`--allow build,test`.

### Tests first

- [X] T009 [P] [US1] In `mcpserver/server_test.go`, add `TestAllowListRegistersOnlyNamed`: with `Options{AllowList: []string{"logs"}}` on `sampleEngine()`, `listedTools` returns exactly the `logs` tool; `Exposure()` returns `1` and the unfiltered count. Add `TestHiddenCallEqualsMissingCall`: on the same server, `connect(t, srv).CallTool` for a hidden task name and for `"nosuch"` both return a non-nil error, and the two error strings are equal after replacing each tool name with a placeholder; the engine's `lastCall` stays empty. Rewrite `TestListToolsSameWithAllowList` as `TestAllowListKeepsListedToolsUnchanged`: with `Options{AllowList: []string{"status"}}`, the list holds one tool and it is `reflect.DeepEqual` to the `status` entry of the unfiltered list (spec FR-008, replacing spec 033 FR-010's allowlist clause).
- [X] T010 [P] [US1] In `mcpserver/authz_test.go`, rewrite the three allowlist tests so they assert registration, not a refusal text, and delete the `notAuthGreet` constant: `TestRefusalAllowListNarrows` becomes `TestAllowListHidesFromList` (with `AllowList: ["logs"]`, `greet` is absent from `listedTools` and `logs` present); `TestRefusalAllowListBeforeGrant` becomes `TestAllowListHidesGrantedDestructive` (with `AllowList: ["logs"]`, `AllowDestructive: ["clean"]`, the tool `clean` is absent and calling it returns an error without reaching the engine); `TestRefusalAllowListBeforeReach` becomes `TestAllowListHidesReachingTask` (a task with `DestructiveReach: ["clean"]` outside the allowlist is absent). Add `TestRefusalIgnoresAllowList`: `New(sampleEngine(), Options{AllowList: []string{"logs"}}).refusal(TaskInfo{Name: "greet"})` returns `""`, because the filter no longer lives in `refusal`.
- [X] T011 [P] [US1] In `mcpserver/handler_test.go`, remove the `"allowlist"` case from the refusal-before-arguments table near line 405 (the destructive case stays) and add `TestHandlerHiddenToolNeverReachesEngine`: with `Options{AllowList: []string{"get"}}` on `unknownArgsEngine()`, a call to `build` with arguments returns a protocol error and `eng.lastCall == ""`.
- [X] T012 [P] [US1] In `mcpserver/transport_test.go`, add `TestHTTPAllowListFiltersList` beside `TestHTTPToolsListMatchesStdio`: a server started over HTTP with `Options{AllowList: []string{<one task of the engine>}}` lists exactly that tool to an authenticated `httpSession` (quickstart R).
- [X] T013 [P] [US1] In `internal/cli/serve_test.go`, add cases to `TestStartupLine` with a non-nil `exposure`, expecting these literal strings: `rune MCP server on stdio (2 of 14 tasks exposed)`; `rune MCP server on stdio (2 of 14 tasks exposed; destructive tasks allowed: clean)`; `rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed)`; `rune MCP server on http://127.0.0.1:7777 (token required; 2 of 14 tasks exposed; destructive tasks allowed: clean)`; `rune MCP server on stdio (0 of 14 tasks exposed)`.
- [X] T014 [P] [US1] Create `test/integration/allowlist_mcp_test.go` with the scratch Runefile from [quickstart.md](quickstart.md#scratch-runefile) as a constant, a helper `toolNames(t, dir, flags...)` that decodes `mcpToolsList` into sorted tool names (model it on `toolHints` in `tool_hints_mcp_test.go`), a helper `toolEntries(t, dir, flags...)` returning each tool's raw JSON by name, and a helper `toolError(t, dir, flags, task)` that calls `mcpServerCall(t, dir, append([]string{"mcp", "--color=never"}, flags...), task, "{}")` and decodes the JSON-RPC `error.code` and `error.message` (failing the test when the frame has a `result`). Add tests for quickstart A (`TestAllowlistMCP_ListsOnlyAllowed`), B (`..._AllowedTaskRuns`, checking `build.done` with `exists`), C (`..._HiddenTaskIsUnknownTool`, no `deploy.done`), D (`..._HiddenEqualsMissing`: same `error.code`, and messages equal after replacing `deploy` and `nosuch` with a placeholder), E (`..._RepeatedFlagUnion`), O (`..._ListedEntriesUnchanged`: `build` and `test` raw entries equal with and without the flag), and the startup line `rune MCP server on stdio (2 of 7 tasks exposed)` in stderr (`..._StartupLineCounts`).
- [X] T015 [US1] Run the tests of T009 to T014 and confirm each new or rewritten test fails for the expected reason (the task is still listed, the old refusal text, or an unknown flag), not on a typo.

### Implementation

- [X] T016 [US1] In `mcpserver/server.go`, change the `AllowList` doc comment to the text in [contracts/mcpserver-api.md](contracts/mcpserver-api.md#options). In `New`, skip a task with `continue` (after counting it in `exposable`, before `AddTool` and the `exposed` increment) when `len(opts.AllowList) > 0 && !slices.Contains(opts.AllowList, t.Name)`. Put the rule in an unexported method `func (s *Server) exposes(name string) bool` so T025 extends one place.
- [X] T017 [US1] In `mcpserver/authz.go`, delete the allowlist branch at the top of `refusal` and the comment bullet `an operator AllowList, when set, narrows callable tasks;`. Reword the comment's lead so it says a hidden task is never registered and so never reaches `refusal`. Remove the `slices` import only if nothing else in the file uses it (the reach loop does).
- [X] T018 [US1] In `internal/cli/dispatch.go`, add beside `MCPAllowDestructive`: `MCPAllow []string` and `MCPDeny []string`, each with a one-line comment naming its flag and that only `ServeMCP` reads it. In `cmd/rune/serve.go`, register `f.StringArrayVar(&opts.MCPAllow, "allow", nil, "expose only these tasks to agents (comma-separated, repeatable)")`.
- [X] T019 [US1] In `internal/cli/serve.go` `ServeMCP`: after parsing `--allow-destructive`, call `allow, err := parseTaskNames("--allow", opts.MCPAllow)` and return its error. Pass `AllowList: allow` in `mcpserver.Options`. After `mcpserver.New`, when `len(opts.MCPAllow) > 0 || len(opts.MCPDeny) > 0`, call `srv.Exposure()` and pass `&[2]int{exposed, exposable}` to `startupLine`; otherwise pass `nil`.
- [X] T020 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...` and confirm T009 to T014 pass and nothing else regressed.

**Checkpoint**: `--allow` works end to end on both transports. Issue acceptance criteria 1 and 2 hold.

---

## Phase 4: User Story 2 - Operator hides a few tasks and keeps the rest (Priority: P1)

**Goal**: `rune mcp --deny deploy` exposes everything except `deploy`, and a task in both flags is
hidden.

**Independent Test**: Quickstart scenarios F, G, and H.

### Tests first

- [X] T021 [P] [US2] In `mcpserver/server_test.go`, add `TestDenyListHides` as a table over `Options`: `DenyList: ["logs"]` lists every `sampleEngine()` task except `logs`; `AllowList: ["logs", "greet"], DenyList: ["logs"]` lists exactly `greet`; `AllowList: ["logs"], DenyList: ["logs"]` lists nothing and `Exposure()` returns `0` exposed; `DenyList: ["nosuch"]` lists everything (unknown names are ignored). Each case also checks `Exposure()` against the listed count. Add `TestExposes` calling `exposes` directly for the four combinations of in-allowlist and in-denylist plus the empty-lists case.
- [X] T022 [P] [US2] In `test/integration/allowlist_mcp_test.go`, add tests for quickstart F (`TestAllowlistMCP_DenyHidesOne`: the six other exposable tasks listed), G (`..._DeniedTaskIsUnknownTool`: `toolError`, no `deploy.done`), H (`..._DenyWinsOverAllow`: `--allow build,deploy --deny deploy` lists exactly `build`), and the repeated and comma forms of `--deny` listing the same tools.
- [X] T023 [US2] Run T021 and T022 and confirm they fail because `DenyList` and `--deny` do not exist yet.

### Implementation

- [X] T024 [US2] In `mcpserver/server.go`, add `DenyList []string` to `Options` after `AllowList` with the doc comment from [contracts/mcpserver-api.md](contracts/mcpserver-api.md#options), and extend `exposes` to `(len(AllowList) == 0 || contains(AllowList, name)) && !contains(DenyList, name)`.
- [X] T025 [US2] In `cmd/rune/serve.go`, register `f.StringArrayVar(&opts.MCPDeny, "deny", nil, "hide these tasks from agents (comma-separated, repeatable)")`. In `internal/cli/serve.go` `ServeMCP`, after parsing `--allow`, call `deny, err := parseTaskNames("--deny", opts.MCPDeny)`, return its error, and pass `DenyList: deny` in `mcpserver.Options`.
- [X] T026 [US2] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...` and confirm green.

**Checkpoint**: Both flags filter the list and direct calls.

---

## Phase 5: User Story 3 - An exposed task still runs its dependencies (Priority: P1)

**Goal**: Prove the filters touch only the tool surface: dependencies and hooks run, destructive
rules hold, and the `agent` endpoint is unchanged. No production code is expected to change here.
If a test in this phase fails, fix the cause in the code from Phases 3 and 4, not the test.

**Independent Test**: Quickstart scenarios I to N.

- [X] T027 [P] [US3] In `test/integration/allowlist_mcp_test.go`, add tests for quickstart I (`TestAllowlistMCP_HiddenDependenciesRun`: `--allow test`, call `test`, then `build.done`, `setup.done`, `test.done` exist), J (`..._DependencyNotCallable`: `toolError` for `build`), the `--deny build` form of I (call `test`, `build.done` exists), and K (`..._HiddenHookRuns`: `--allow lint`, `lint.done` and `report.done` exist).
- [X] T028 [P] [US3] In `test/integration/allowlist_mcp_test.go` (same file as T027, do after it), add tests for quickstart L (`TestAllowlistMCP_ReachRefusalUnchanged`: `--allow release`, call `release` with `toolCall`, `isError` true, text is the literal `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`, no marker), M (`..._GrantedHiddenDependencyRuns`: `--allow release --allow-destructive clean`, `clean.done` and `release.done` exist, `toolNames` is exactly `release`), N (`..._AllowDoesNotGrant`: `--allow clean`, text is the literal `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`, no `clean.done`), and the `--deny clean --allow-destructive clean` row of the table in [contracts/mcp-tools.md](contracts/mcp-tools.md#interaction-with---allow-destructive).
- [X] T029 [P] [US3] In `internal/cli/mcp_test.go`, add `TestAgentEndpointIgnoresFilters`: build the engine options with `MCPAllow: []string{"build"}` and `MCPDeny: []string{"test"}` set, construct the server the way `agentexec.go` does (`mcpserver.New(adapter, mcpserver.Options{Version: ...})`), and assert its tool list equals the list with both fields empty (FR-017). Before writing it, confirm with `grep -rn "MCPAllow\b\|MCPDeny" --include='*.go' internal cmd` that only `internal/cli/serve.go` reads the two fields.
- [X] T030 [P] [US3] In `mcpserver/server_test.go`, add `TestFilterKeepsHintsOfReachingTask`: an engine with `{Name: "release", DestructiveReach: []string{"clean"}}` and `{Name: "clean", Destructive: true}`, `Options{AllowList: []string{"release"}}`: the single listed tool has `DestructiveHint` true, and calling it returns the spec 034 refusal naming `clean`.
- [X] T031 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...` and confirm T027 to T030 pass.

**Checkpoint**: Issue proposal bullet 3 holds: dependencies run without being tools, and destructive dependencies still need their own grant.

---

## Phase 6: User Story 4 - Mistakes in the flags stop the server at startup (Priority: P2)

**Goal**: Unknown names exit 2 with a suggestion before any Runefile code runs; names that can
never be tools warn; an empty surface warns.

**Independent Test**: Quickstart table P and scenario Q.

### Tests first

- [X] T032 [P] [US4] In `internal/cli/serve_test.go`, extend `TestServeMCPUsageErrorRunsNoRunefileCode` (or add a sibling table test) with `MCPAllow: ["biuld"]`, `MCPDeny: ["biuld"]`, `MCPAllow: [""]`, `MCPDeny: ["build,"]`, and `MCPAllow: ["*"]`, each on a Runefile with a `[context]` hook that writes a marker: the error is a `*UsageError` with the literal message from [contracts/cli-serve.md](contracts/cli-serve.md#errors) and the marker is absent. Add a precedence case: `MCPAllowDestructive: ["claen"]` with `MCPAllow: ["biuld"]` reports the `--allow-destructive` name first; `MCPAllow: ["biuld"]` with `MCPDeny: ["zzz"]` reports `--allow` first; an empty `--deny` item with an unknown `--allow` name reports the empty item first.
- [X] T033 [P] [US4] In `internal/cli/serve_test.go` (same file, after T032), add `TestWarnNeverExposed` modeled on `TestWarnNonDestructive`: for a task map with a private `_setup`, a `[context]` task `health`, a `[windows]` task, and a plain `build`, `warnNeverExposed(w, theme, "--allow", names, tasks)` writes exactly `warning: --allow _setup has no effect: task "_setup" is never exposed to agents` and the same line for `health`, and nothing for the other two; with `"--deny"` the lines name `--deny`. Add `TestWarnNoTasksExposed`: `warnNoTasksExposed(w, theme)` writes exactly `warning: no tasks are exposed to agents; check --allow and --deny`.
- [X] T034 [P] [US4] In `test/integration/allowlist_mcp_test.go`, add `TestAllowlistMCP_BadFlagStopsServer` as a table over the five error rows of [quickstart P](quickstart.md#p-startup-checks): exit status 2, stderr equals `rune: <message>`, `context.done` absent. Add `..._NeverExposedWarns` for `--allow _setup` (both warnings and `(0 of 7 tasks exposed)` in stderr, server answers `tools/list` with an empty list) and `--deny health` (one warning, `(7 of 7 tasks exposed)`), and `..._EmptySurfaceWarns` for `--allow build --deny build`. Add `..._NoFlagsUnchanged`: with no filter flag stderr holds `rune MCP server on stdio` with no parentheses and no warning.
- [X] T035 [P] [US4] In `internal/cli/mcp_test.go`, add `TestAllowOtherOSTaskStartsEmpty` for quickstart Q beside `TestServerNeverRegistersOSMismatchedTools`: adapter with `goos = "linux"` and a `[windows]` task `win-only`, `Options{AllowList: []string{"win-only"}}`: zero tools, `Exposure()` exposed is `0`; and `warnNeverExposed` writes nothing for `win-only`.
- [X] T036 [US4] Run T032 to T035 and confirm they fail because the name check is not wired for the new flags and the two warning helpers do not exist.

### Implementation

- [X] T037 [US4] In `internal/cli/serve.go` `ServeMCP`, after the existing `checkTaskNames("--allow-destructive", granted, adapter.tasks)`, add `checkTaskNames("--allow", allow, adapter.tasks)` then `checkTaskNames("--deny", deny, adapter.tasks)`, returning the first error. Keep all three before the transport validation and the `[context]` hook, and update the comment above the parse step to cite spec 041 FR-011 beside spec 031 FR-010.
- [X] T038 [US4] In `internal/cli/serve.go`, add `warnNeverExposed(w io.Writer, theme style.Theme, flag string, names []string, tasks map[string]*ast.Task)`: one line per name whose task satisfies `t.IsPrivate() || t.Attr(ast.AttrContext) != nil`, in the format `%s: %s %s has no effect: task %q is never exposed to agents\n` with `theme.Warning.Render("warning")`. Its comment says why a task restricted to another OS is silent (one client configuration may serve several hosts). Call it for `--allow` then `--deny` right after `warnNonDestructive`.
- [X] T039 [US4] In `internal/cli/serve.go`, add `warnNoTasksExposed(w io.Writer, theme style.Theme)` writing `warning: no tasks are exposed to agents; check --allow and --deny`. In `ServeMCP`, call it after `mcpserver.New` and before the startup line when a filter flag was given and `Exposure()` reports `0` exposed.
- [X] T040 [US4] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/cli/... ./test/integration/...` and confirm green.

**Checkpoint**: Issue acceptance criterion 3 holds: unknown names are a usage error, exit 2, with a close-name suggestion.

---

## Phase 7: User Story 5 - The documentation describes both flags (Priority: P3)

**Goal**: The MCP guide, the CLI reference, and `rune serve --help` describe the same two flags.

**Independent Test**: The `test/docs` harness passes and `rune serve --help` shows both flags with
an example each.

- [X] T041 [P] [US5] In `cmd/rune/serve.go`, add to `Long`, after the `[confirm]` paragraph: `--allow` and `--deny` choose which tasks are tools; a task named in both is hidden; the dependencies and hooks of an exposed task still run; `[confirm]` tasks still need `--allow-destructive`. Add to `Example` the two lines from [contracts/cli-serve.md](contracts/cli-serve.md#help-text), aligned with the existing comment column.
- [X] T042 [P] [US5] In `docs/mcp.md`, add after the "Letting agents run destructive tasks" block a block "Choosing which tasks agents see" showing `rune mcp --allow build,test` and `rune mcp --deny deploy`, the comma and repeated forms, that a task in both is hidden, that a hidden task is absent from the tool list and a call to it fails like an unknown tool, and that unknown names stop the server. In the security model section, add a bullet stating that `--allow` and `--deny` cover direct calls only: a hidden task still runs as a dependency or hook of an exposed task, and to stop a task running at all the operator hides the tasks that depend on it or the author marks it `[confirm]`. State that `--allow` never grants a `[confirm]` task. In the `agent` task section, add one sentence that neither flag reaches an `agent` task's own endpoint.
- [X] T043 [P] [US5] In `docs/cli.md`, extend the `rune serve` row's synopsis with `[--allow <task>[,<task>...]] [--deny <task>[,<task>...]]` and its description with one sentence on each flag, matching the help text.
- [X] T044 [US5] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` and fix any failure in the docs, not in the harness. If the harness pins the `serve` flag set or help text in a golden file, regenerate it deliberately and review the diff.

**Checkpoint**: Issue acceptance criterion 4's intent is met by `test/integration/allowlist_mcp_test.go`; the pending test it names is not in this repository (spec Assumptions).

---

## Phase 8: Polish & Cross-Cutting Concerns

- [X] T045 Run the full gate set and confirm each is green: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`; `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...`; `docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run`.
- [X] T046 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test -cover ./mcpserver/... ./internal/cli/...` and confirm every function added or changed in `mcpserver/server.go`, `mcpserver/authz.go`, and `internal/cli/serve.go` is covered, with 90% as the target for the changed code. Add unit tests for any gap.
- [X] T047 Walk [quickstart.md](quickstart.md) by hand with a built binary for scenarios A, C, F, I, L and the startup rows of P, and run `rune serve --help`. Report any difference from the contracts.
- [X] T048 Confirm `grep -rn "not authorized for this session" --include='*.go' .` returns nothing outside `specs/`, and that no line added by this branch contains an em dash (U+2014): search the added lines of `git diff main` for that character in `specs/041-mcp-allow-deny`, `docs/mcp.md`, `docs/cli.md`, `cmd/rune/serve.go`, `internal/cli/serve.go`, `mcpserver/server.go`, and `mcpserver/authz.go`.
- [X] T049 First code review round on `git diff main`. Fix every finding, and cover every bug found with a test that fails before the fix. Rerun T045.
- [X] T050 Second code review round on the updated diff, with the same rule. Rerun T045.
- [X] T051 Prepare the commit as `feat(mcp)!:` with the breaking change in its subject: `rune mcp --allow` and `--deny` choose which tasks are tools, and `mcpserver.Options.AllowList` now hides tasks from the tool list instead of refusing their calls. Do not commit or push until the maintainer asks.

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1** has no dependency.
- **Phase 2** depends on Phase 1 and blocks every story.
- **US1 (Phase 3)** depends on Phase 2.
- **US2 (Phase 4)** depends on US1: it extends `exposes` (T016) and the `ServeMCP` wiring (T019).
- **US3 (Phase 5)** depends on US1 and US2: it tests both flags.
- **US4 (Phase 6)** depends on US2: it validates both lists. It is independent of US3.
- **US5 (Phase 7)** depends on US1 and US2 for the flags, and reads best after US4 so the docs can
  cite the warnings.
- **Phase 8** depends on every story.

### Within each story

Tests are written and seen failing before the implementation. Tasks naming the same file run in
the order given: `internal/cli/serve_test.go` (T002, T003, T013, T032, T033),
`test/integration/allowlist_mcp_test.go` (T014, T022, T027, T028, T034),
`mcpserver/server_test.go` (T004, T009, T021, T030), `internal/cli/serve.go` (T005, T006, T019,
T025, T037, T038, T039), `mcpserver/server.go` (T007, T016, T024).

### Parallel opportunities

- Phase 2: T002 and T004 (different files); T007 beside T005.
- US1: T009, T010, T011, T012 are four files in `mcpserver`; T013 and T014 are two more. All six
  can be written together. T016 and T017 (library) beside T018 (CLI options).
- US2: T021 beside T022.
- US3: T027, T029, T030 are three files.
- US4: T032 or T033, T034, T035 are three files.
- US5: T041, T042, T043 are three files.

### Parallel example: User Story 1

```text
Write together: T009 (server_test.go), T010 (authz_test.go), T011 (handler_test.go),
                T012 (transport_test.go), T013 (serve_test.go), T014 (allowlist_mcp_test.go)
Then:           T015 (see them fail)
Then together:  T016 + T017 (mcpserver), T018 (dispatch.go, cmd/rune/serve.go)
Then:           T019 (ServeMCP wiring), T020 (green)
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phase 1 and Phase 2.
2. Phase 3. Stop and validate: `rune mcp --allow build,test` lists two tools and rejects the rest.

An MVP without US4 accepts a mistyped `--allow` name silently (it hides everything else), so do
not release before Phase 6.

### Incremental delivery

1. US1: the allowlist works.
2. US2: the denylist works, and the two combine.
3. US3: proof that nothing in the run path changed.
4. US4: mistakes are caught at startup.
5. US5: the docs match.
6. Phase 8: gates, coverage, two review rounds.

This feature ships as one pull request closing #39.
