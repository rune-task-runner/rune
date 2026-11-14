---

description: "Task list for spec 043: reload tools when the Runefile changes"
---

# Tasks: Reload Tools When the Runefile Changes

**Input**: Design documents from `/specs/043-mcp-runefile-reload/`

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
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- The stderr lines, the warnings and the flag help are fixed by [contracts/cli.md](contracts/cli.md).
  Tests pin them as literal strings.
- A server with `--no-reload`, and a server whose files do not change, stays byte-identical.
  Existing tests must pass with no change to their assertions.
- Tests that wait for a reload poll with a deadline. The deadline is 2 s only where a task says
  "within 2 s": those tests assert FR-018. Every other wait uses 10 s, so a slow CI runner does not
  fail a test that is not about timing. They
  never sleep a fixed time to assert that something happened. To assert that nothing happened,
  they first see the reload finish (its stderr line, or a later request's reply), then wait a
  short fixed time for a notification that must not come: the SDK sends 10 ms after a change.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` on branch `043-mcp-runefile-reload` and confirm it is green. If anything fails, report it and stop.

---

## Phase 2: Foundational (the swappable server)

**Purpose**: `mcpserver.Server` can replace its engine and tools while it runs
([research R1 to R4](research.md), [data-model.md](data-model.md),
[contracts/mcpserver-api.md](contracts/mcpserver-api.md)). FR-011, FR-014, FR-016, FR-017, FR-019,
FR-020, FR-035.

**CRITICAL**: No user story work begins until T004 is green.

### Tests first

- [X] T002 Create `mcpserver/reload_test.go` with tests that fail to compile until T003. Use the existing `fakeEngine`, `sampleEngine` and `connect` helpers of `mcpserver/server_test.go`, and a client created with a `ToolListChangedHandler` that signals a channel:
  - `TestReplaceAddsTool`: `Replace` with an engine that has one more task returns `Change{Added: 1}`, the client is notified within 2 s, `ListTools` holds the new tool, and a call to it reaches the new engine.
  - `TestReplaceRemovesTool`: returns `Change{Removed: 1}`, the client is notified, the tool is not listed, and a call to it fails with the same error as a call to a name that never existed (compare the two error strings with the name replaced).
  - `TestReplaceChangesTool`: a changed `Doc`, a changed parameter list, and a changed `ReadOnly` each return `Change{Changed: 1}` and the list shows the new value.
  - `TestReplaceSameToolsNoNotification`: `Replace` with an engine whose `Tasks()` are equal returns a zero `Change`; then a second `Replace` that adds a tool notifies exactly once (the channel holds one signal after the list shows the tool). The call after the first `Replace` reaches the new engine (body-only edit, FR-017).
  - `TestReplaceAppliesFiltersAndGrants`: with `AllowList`, `DenyList` and `AllowDestructive` in `Options`, the replaced engine's tasks are exposed, hidden and refused by the same rules as in `New`; `Exposure()` reports the new counts.
  - `TestReplaceInFlightCallKeepsEngine`: a call blocked inside the old engine's `Call` returns the old engine's result after `Replace`, and `Replace` returns while the call is still blocked (FR-011).
  - `TestReplaceNotifiesEverySession`: two connected clients both receive the notification for one `Replace` that adds a tool (US1 scenario 7).
  - `TestReplaceOutputLimit`: after `Replace(engine, 1024)` a result of 10,000 bytes is capped at 1,024 per stream; `Replace(engine, 0)` gives the default.
  - `TestReplaceConcurrentList`: 50 `Replace` calls alternating between two engines whose tool sets differ in name and schema, while another goroutine lists tools in a loop; every list equals one of the two full sets (FR-019). Runs under `-race`.
  - `TestReloadableCapabilities`: `New(&fakeEngine{}, Options{Reloadable: true})` advertises `Tools.ListChanged == true` with no tool; with `Reloadable: false` the capabilities are as before.
  - `TestSetInstructions`: a session connected before `SetInstructions("v2")` has `v1` in its initialize result, one connected after has `v2`, and `SetInstructions("")` gives later sessions none. `Options.Instructions` alone still reaches the first session.

### Implementation

- [X] T003 Implement the swap in `mcpserver/server.go`, `mcpserver/handler.go`, `mcpserver/authz.go` and new `mcpserver/reload.go`:
  - `snapshot{engine Engine; tasks map[string]TaskInfo; exposed, exposable, outputLimit int}` (tasks keyed by tool name, as [data-model.md](data-model.md)) held in `Server.state atomic.Pointer[snapshot]`; remove the `engine`, `exposed`, `exposable`, `outputLimit` fields. `Exposure()` reads the snapshot.
  - `Options.Reloadable bool`; `capabilities(opts)` returns the explicit capabilities when it is set or a filter is given.
  - `New` builds the server, installs the middleware, and calls the same internal `apply(engine, outputLimit)` that `Replace` uses, so start and reload share one path.
  - `apply`: under `swapMu` write lock, build the snapshot, store it, compute each exposed task's `mcp.Tool` JSON, `AddTool` for names whose JSON is new or different, `RemoveTools` for registered names no longer exposed, update `registered`, return `Change`. In `New` the returned `Change` is ignored.
  - `handler(name string)`: loads the snapshot at call start, looks the task up, answers a missing name with the SDK's own error for an unregistered tool, `&jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown tool %q", name)}`, then proceeds as today with the snapshot's engine, `TaskInfo` and `outputLimit`.
  - Receiving middleware: for `tools/list`, hold `swapMu` read lock around the next handler; for `initialize`, set `Instructions` on the `*mcp.InitializeResult` from `Server.instructions`.
  - `SetInstructions(text string)` stores the pointer. `Options.Instructions` is its initial value and is not passed to `mcp.ServerOptions`.
  - Doc comments as in [contracts/mcpserver-api.md](contracts/mcpserver-api.md).
- [X] T004 Run `go test ./mcpserver/` and `go test -race ./mcpserver/` in Docker. All existing tests pass unchanged and T002 is green under `goleak`.

**Checkpoint**: The library can be reloaded by an embedder.

---

## Phase 3: User Story 1 - A new task becomes a tool without a restart (Priority: P1) MVP

**Goal**: `rune mcp` watches the root Runefile, reloads on change, and notifies the client.

**Independent Test**: Start the binary on a one-task Runefile, connect over stdio, append a task,
receive `notifications/tools/list_changed` within 2 s, list both tools, call the new one.

### Tests first

- [X] T005 [P] [US1] In new `internal/cli/mcpreload_test.go` add tests that fail until T007 and T008:
  - `TestLoadModulePaths`: for a root file alone, `newMCPAdapter` returns its absolute cleaned path as the only path.
  - `TestReloaderAddsTask`: build a server and a `reloader` on a temp Runefile, connect an in-memory client, start `run`, append a task to the file; the client is notified within 2 s, the tool is listed, and stderr holds exactly `reloaded Runefile: 1 tool added, 0 removed, 0 changed`.
  - `TestReloaderRemoveAndChange`: removing a task prints `reloaded Runefile: 0 tools added, 1 removed, 0 changed`; changing a doc comment prints `0 tools added, 0 removed, 1 changed`; adding two prints `2 tools added`.
  - `TestReloaderBodyOnlyChange`: a changed body prints the line with three zeros, sends no notification, and the next call prints the new output.
  - `TestReloaderRenameOverSave`: writing `Runefile.tmp` and renaming it over `Runefile` reloads, twice in a row (FR-005).
  - `TestReloaderCoalesces`: ten writes within 200 ms produce at most two `reloaded` lines and the final tool list matches the last write (SC-004).
  - `TestReloaderStopsOnCancel`: cancelling the context makes `run` return; no goroutine is left (the package's leak test covers it).
  - `TestReloaderFlagsStillApply`: with `--allow build` and a grant, a reloaded Runefile exposes only `build`, and a `[confirm]` task added by the reload is refused unless granted (FR-012).
  - `TestReloaderSettingLimit`: a reload that adds `set mcp_output_limit := "1KiB"` caps the next call at 1,024 bytes; with a flag limit the flag still wins.
  - `TestReloaderWatchError`: an error delivered on the watcher's error channel twice prints `warning: cannot watch the Runefile for changes: <error>; tools will not reload` once, and the loop keeps running (FR-008). `newReloader` takes the watcher's event and error channels through a small seam so the test can feed them.
  - `TestReloadLine`: `reloadLine(mcpserver.Change{...})` for 0, 1 and 2 added tools gives the literal lines of the contract.
- [X] T006 [P] [US1] Create `test/integration/reload_mcp_test.go` with a long-lived stdio client helper (`startMCP(t, dir, flags...)` that starts the built binary with pipes, sends `initialize` and `notifications/initialized`, and offers `request(method, params)`, `waitNotification(method, timeout)`, `stderr()` and cleanup that closes stdin and waits) and these tests, failing until T008:
  - `TestReloadMCP_AddedTaskIsListed` (stands for `TestMCPE2E_Pending_RunefileReload`, FR-037): append a task; `notifications/tools/list_changed` arrives within 2 s; `tools/list` holds both; `tools/call` on the new tool returns its output.
  - `TestReloadMCP_RemovedTaskIsUnknown`: after removal the call's error equals the error for a name that never existed, with the name replaced.
  - `TestReloadMCP_InitializeAdvertisesListChanged`: on a Runefile whose only task is private, the initialize result has `tools.listChanged` true; adding a public task then notifies and lists it (acceptance 8).
  - `TestReloadMCP_StdoutCarriesOnlyProtocol`: every stdout line during a reload parses as a JSON-RPC message (FR-025).

### Implementation

- [X] T007 [US1] In `internal/cli/serve.go`, make `loadModule` return `(*loadedModule, []string, error)`: wrap the source provider in a recorder (new unexported `recordingProvider` in `internal/cli/mcpreload.go`) and return the absolute, cleaned, de-duplicated paths, root first, on success and on every failure path. `newMCPAdapter` returns `(*mcpAdapter, []string, error)`. Update the other callers (`internal/cli/agentexec.go` and any other) to ignore the paths.
- [X] T008 [US1] Create `internal/cli/mcpreload.go` with the `reloader` of [data-model.md](data-model.md): `newReloader(...)` (its first watched set is the paths the start's load returned), `run(ctx)` (fsnotify loop, 100 ms debounce, events filtered by the watched set, Chmod ignored), `reload(ctx)` (sequence of research R7, success path only for now), `watch(paths)` (add and remove directories, skip ones that do not exist), `reloadLine(Change) string`. In `ServeMCP`: unless `opts.MCPNoReload`, pass `Reloadable: true`, start the reloader in a goroutine with a cancellable context, and cancel and join it after the transport returns. If the watcher cannot be created or a watcher error arrives, print `warning: cannot watch the Runefile for changes: <error>; tools will not reload` once and keep serving (FR-008).
- [X] T009 [US1] Run `go test ./internal/cli/ ./mcpserver/ ./test/integration/ -run 'Reload|Replace|LoadModulePaths'` in Docker; T005 and T006 are green except the failure cases of later stories.

**Checkpoint**: US1 works end to end on a single Runefile.

---

## Phase 4: User Story 2 - A broken save keeps the last valid tools (Priority: P1)

**Goal**: A failed load changes nothing, prints the diagnostics and one line, and recovers.

**Independent Test**: Overwrite the Runefile with a static error: no notification, same tools, old
task still runs, diagnostics on stderr. Then save a valid file: notification and new tool.

### Tests first

- [X] T010 [P] [US2] In `internal/cli/mcpreload_test.go` add, failing until T012:
  - `TestReloaderStaticErrorKeepsTools`: for a syntax error, an unknown dependency, a dependency cycle, an invalid `set mcp_output_limit`, and a `minimum_version` above the running version: stderr holds a diagnostic with the file name and `line:col` and ends with `reload failed: <reason>; keeping the previous tools` (pin `reload failed: 1 static error(s); keeping the previous tools` for the syntax case), the tool list is unchanged, a call still returns the old output, and no notification was sent.
  - `TestReloaderRecovers`: after a failure, a valid save with a new task notifies and lists it; a second invalid save prints the failure line again.
  - `TestReloaderDeletedRunefile`: deleting the root file prints a failure line that names the read error; recreating it reloads.
  - `TestReloaderFailureRunsNothing`: a Runefile with a `[context]` task that writes a marker file; after a failed reload on HTTP mode the marker does not exist (FR-023).
  - `TestReloaderRestoreSameContent`: fail, then write back the original content: `reloaded Runefile: 0 tools added, 0 removed, 0 changed` and no notification.
  - `TestReloaderMasksReloadedSecrets`: with `SECRET_TOKEN=hunter2value` in the adapter's environment, a reload that adds a task printing `$SECRET_TOKEN` and a `[context]` task printing it: the call result and the new instructions (HTTP mode) hold the mask placeholder and never the value, and no stderr line of the reload holds it (FR-034).
  - `TestReloaderStaleFlagNames`: with `--allow build,lint`, `--deny old` and `--allow-destructive clean`, a reload that removes `lint`, `old` and `clean` prints the three literal warnings of the contract, keeps serving, and a later reload that brings `lint` back exposes it again with no warning for it. A grant on a task that lost `[confirm]` prints the existing no-effect warning.
- [X] T011 [P] [US2] In `test/integration/reload_mcp_test.go` add `TestReloadMCP_StaticErrorKeepsTools`: write a Runefile with `build: nope`; stderr holds `Runefile:` with a line and column, a caret line, and `reload failed: 1 static error(s); keeping the previous tools`; `tools/list` is unchanged; the old tool still runs; then a valid save with a new task notifies. The process is still running at the end and exits 0 when stdin closes.

### Implementation

- [X] T012 [US2] In `internal/cli/mcpreload.go` complete `reload`: on a load error print `reload failed: <err>; keeping the previous tools` (for a `UsageError` or `ValidationError` use the wrapped error's text) and set the watched set to the last good paths plus the failed load's paths; add `warnUnknownNames(w, theme, flag, names, tasks)` with the contract's text and call it, `warnNonDestructive` and `warnNeverExposed` on success before the success line.
- [X] T013 [US2] Run the US1 and US2 tests in Docker; all green.

**Checkpoint**: US1 and US2 together are the MVP the issue asks for.

---

## Phase 5: User Story 3 - Imported and module files are followed too (Priority: P2)

**Goal**: The watched set is the whole import and module graph and follows it.

**Independent Test**: A root file that imports one file and loads one module: adding a task to
either notifies within 2 s; the module task is listed as `deploy__push`.

### Tests first

- [X] T014 [P] [US3] In `internal/cli/mcpreload_test.go` add, failing until T016:
  - `TestLoadModulePathsGraph`: a root that imports `ci.rune` (which imports `sub/inner.rune`), loads `mod deploy` from `deploy.rune`, and has `import? "local.rune"` that does not exist: the paths hold all five absolute paths, plus nothing outside the temp directory. A load that fails because `ci.rune` has a syntax error still returns `ci.rune`.
  - `TestReloaderImportedFile`: adding a task to `ci.rune`, to `sub/inner.rune` and to `deploy.rune` each reloads; the module task is listed as `deploy__push`.
  - `TestReloaderGraphFollowsImports`: adding `import "extra.rune"` to the root lists its tasks and later edits to `extra.rune` reload; removing the import drops its tasks and a later edit to `extra.rune` prints no reload line (check by then editing the root and seeing exactly one new line).
  - `TestReloaderFixInFailedImport`: the root gains an import of a file with a static error (reload fails); fixing only that file reloads (FR-003).
  - `TestReloaderCreateMissingImport`: a required import of a missing file fails the reload; creating the file reloads. The optional import case: creating `local.rune` reloads and lists its task.
  - `TestReloaderIgnoresOtherFiles`: writing `notes.txt` and `other.rune` (not imported) in the same directory prints no reload line (FR-004).
- [X] T015 [P] [US3] In `test/integration/reload_mcp_test.go` add `TestReloadMCP_ImportAndModule`: a root with one import and one module; a task added to each produces a notification within 2 s and is listed, the module's as `deploy__push`.

### Implementation

- [X] T016 [US3] Make T014 and T015 pass. The recorder of T007 and the directory watching of T008 are expected to cover them; fix what does not (paths of probed module candidates, directories outside the root's, removal of unneeded directories).
- [X] T017 [US3] Run the US1 to US3 tests in Docker; all green.

---

## Phase 6: User Story 4 - New sessions get fresh project context (Priority: P3)

**Goal**: On HTTP, a successful reload reruns the `[context]` hook for later sessions.

**Independent Test**: HTTP server, `[context]` prints `v1`; first session gets `v1`; change to `v2`;
after the success line a second session gets `v2`.

### Tests first

- [X] T018 [P] [US4] In `internal/cli/mcpreload_test.go` add, failing until T020:
  - `TestReloaderContextHTTP`: with `useHTTP` true, after a reload that changes the `[context]` body from `v1` to `v2`, a session connected before has `v1` and one connected after has `v2`; a reload that removes the `[context]` task gives later sessions no instructions; one that adds it gives them its output.
  - `TestReloaderContextFailure`: a reloaded hook that exits 1 gives later sessions `(context hook "health" failed; proceeding without project context)`, stderr holds the matching warning, and the tools were still replaced.
  - `TestReloaderContextStdio`: with `useHTTP` false, the hook writes a marker file on each run; after a reload the marker count is still one (FR-029).
  - `TestReloaderContextKeptOnFailure`: after a failed reload a new session still gets the last valid instructions.
- [X] T019 [P] [US4] In `test/integration/reload_mcp_test.go` add `TestReloadMCP_HTTPInstructions`: start `rune serve --http --addr 127.0.0.1:<free port> --token-file`, initialize one session (instructions `v1`), change the hook, wait for the `reloaded` line on stderr, initialize a second session (instructions `v2`).

### Implementation

- [X] T020 [US4] In `reload`, after `Replace` and the watched-set update and before the success line (research R7), when `useHTTP` run `adapter.gatherContext(ctx, opts.Stderr)` and `srv.SetInstructions`. Run the US4 tests in Docker; green.

---

## Phase 7: User Story 5 - An operator can freeze the tasks (Priority: P3)

**Goal**: `--no-reload` gives the previous behavior.

**Independent Test**: Start with `--no-reload`, add a task: no notification, same list, no reload
line.

### Tests first

- [X] T021 [P] [US5] Add tests failing until T022: in `internal/cli/mcpreload_test.go`, `TestServeMCPNoReload` (with `MCPNoReload` no reloader is started: a file change produces no stderr output and no tool change); in `test/integration/reload_mcp_test.go`, `TestReloadMCP_NoReload` (add a task, then call an existing tool as a barrier: no notification was received, the list is unchanged, stderr holds only the startup line; on a Runefile with no exposable task the initialize result has no `tools` capability); in `cmd/rune` tests if a help golden exists, the new flag line; in `internal/cli`, `TestAgentEndpointNotReloadable` (the options `agentMCPOptions` returns leave `Reloadable` false, and `executeAgent` starts no reloader: FR-033).

### Implementation

- [X] T022 [US5] Add `MCPNoReload bool` to `Options` in `internal/cli/dispatch.go` and `--no-reload` to `cmd/rune/serve.go` with the contract's help text, a paragraph in `Long`, and an example line. Regenerate any help golden deliberately. Run the US5 tests in Docker; green.

---

## Phase 8: Polish

- [X] T023 [P] Document the feature: a "Reloading" section in `docs/mcp.md` (what is watched, the two stderr lines, failed reloads keep the tools, `[context]` on HTTP, flags stay fixed, `--no-reload` and when to use it), the flag in the `rune serve` row of `docs/cli.md`, and any place in `docs/` that tells users to restart the server after editing the Runefile. Keep examples valid for the docs harness. The release note of FR-036 is the commit body: the commit is `feat(mcp):` and its body says that a running server now follows the Runefile and that `--no-reload` turns it off.
- [X] T024 Run the full gate set in Docker: `go test ./...`, `go test -race ./...` with `CGO_ENABLED=1`, `golangci-lint run` (image `golangci/golangci-lint:v2.14`), and the docs harness `go test ./test/docs/...`. Fix what fails without weakening a test.
- [X] T025 Check coverage of the new code: `go test -cover ./mcpserver/ ./internal/cli/`; `mcpserver/reload.go` and `internal/cli/mcpreload.go` reach 90% of statements. Add tests for uncovered branches.
- [X] T026 Walk [quickstart.md](quickstart.md) steps 1 to 7 against the built binary with a scripted stdio client and record the observed lines.

---

## Dependencies & Execution Order

- Phase 1 then Phase 2. Phase 2 blocks every story.
- US1 (Phase 3) blocks US2 to US5: they extend the reloader it creates.
- US2, US3, US4 and US5 are independent of each other after US1. Their test tasks ([P]) touch
  the same two test files, so they are parallel only with implementation tasks, not with each other.
- Polish after all stories.

## Implementation Strategy

MVP is Phase 1 to Phase 4 (US1 and US2): the issue's first two acceptance criteria. US3 covers the
third and US1's T006 the fourth. US4 and US5 complete the proposal and the opt-out.
