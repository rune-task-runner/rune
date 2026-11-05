---

description: "Task list for 031-mcp-destructive-optin"
---

# Tasks: Per-Task Opt-In for Destructive Tasks over MCP

**Input**: Design documents from `specs/031-mcp-destructive-optin/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI requires test-first, multi-layer
verification, and research R7 names the test files per layer. Write each test task
first and confirm it fails (or does not compile) before the implementation task it
covers.

**Organization**: Phase 2 changes the public `mcpserver.Options.AllowDestructive`
field and the refusal text, which every story compiles against. Then one phase per
user story in priority order. US2 needs the flag from US1 to start a granted server,
so it follows US1.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: the user story the task serves (US1 to US4)

## Commands

Tests run in Docker only. On this host the `docker compose` plugin works and the
standalone `docker-compose` binary may be missing; use whichever exists.

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

Check the golangci-lint pin in `.github/workflows/ci.yml` before linting.

## Fixed texts

Copy these exactly. Sources: [contracts/cli-serve.md](contracts/cli-serve.md),
[contracts/mcp-tools.md](contracts/mcp-tools.md), research R5 and R6.

| Use | Text |
|---|---|
| destructive refusal (tool result) | `task "NAME" is destructive; to permit it, add NAME to --allow-destructive and restart the server` |
| destructive refusal, agent-task endpoint | `task "NAME" is destructive and is not allowed in this session` |
| allowlist refusal (tool result) | `task "NAME" is not authorized for this session` |
| empty name (exit 2) | `--allow-destructive: empty task name` |
| unknown name (exit 2) | `unknown task in --allow-destructive: NAME` plus ` (did you mean "X"?)` when `nearest` finds one |
| warning (stderr) | `warning: --allow-destructive build has no effect: task "build" is not destructive` |
| startup, stdio, grant | `rune MCP server on stdio (destructive tasks allowed: clean, reset)` |
| startup, HTTP, grant | `rune MCP server on http://ADDR (token required; destructive tasks allowed: clean, reset)` |
| flag usage | `let agents run these [confirm] tasks (comma-separated, repeatable)` |

`NAME` is always the Runefile task name (`tools::clean`), never the tool name
(`tools__clean`). With no grant, both startup lines stay byte-identical to today.

---

## Phase 1: Setup

**Purpose**: Branch and baseline.

- [X] T001 Run `git fetch origin` and create branch `031-mcp-destructive-optin` from `origin/main` (`git switch -c 031-mcp-destructive-optin origin/main`); the untracked `specs/031-mcp-destructive-optin/` directory moves with it. The current branch `030-safe-param-quoting-analyzer` is not on `origin/main` yet; do not branch from it
- [X] T002 Run the full suite in Docker (first command under Commands) before any change and record any test that already fails, so later failures can be attributed

---

## Phase 2: Foundational (blocks all stories)

**Purpose**: Replace the server-wide switch in the embeddable library with a list of
task names, and split the refusal text in two (FR-004, FR-016, research R4 and R5).
After this phase no caller passes a grant yet, so every destructive call is refused
exactly as today, with the new text.

**Blocking**: Every later phase compiles against the new `Options` type.

### Tests

- [X] T003 [P] Rewrite `mcpserver/authz_test.go` for `refusal(name) string` (research R5, data-model "Direct-call refusal"): with `sampleEngine()` and `Options{}`, `refusal("logs")` is `""` and `refusal("clean")` is the destructive refusal text naming `clean`; with `Options{AllowDestructive: []string{"clean"}}`, `refusal("clean")` is `""`; with `Options{AllowList: []string{"logs"}}`, `refusal("greet")` is `task "greet" is not authorized for this session` and does not contain `--allow-destructive`; with `Options{AllowList: []string{"logs"}, AllowDestructive: []string{"clean"}}`, `refusal("clean")` is the allowlist text (allowlist is checked first). Add a destructive namespaced task `tools::clean` to a local fake and assert its refusal names `tools::clean`, not `tools__clean`. Update `TestDestructiveCallRefusedAtRuntime` to assert the exact destructive text instead of `strings.Contains(..., "approval")`
- [X] T004 [P] In `mcpserver/handler_test.go`, add: a server built with `Options{AllowDestructive: []string{"clean"}}` passes a `clean` call to the engine (`eng.lastCall == "clean"`) and returns its result; the same server refuses a second destructive fake task `reset` with the destructive text and the engine never sees it; calling the tool `tools__clean` on a server with no grant returns the destructive text naming `tools::clean`. In `mcpserver/server_test.go`, add a test that `ListTools` returns the same tools, descriptions, and annotations for `Options{}` and for `Options{AllowDestructive: []string{"clean"}}` (FR-014)

### Implementation

- [X] T005 In `mcpserver/server.go`, change `Options.AllowDestructive` from `bool` to `[]string` with the doc comment from [contracts/mcpserver-api.md](contracts/mcpserver-api.md) "After" verbatim. Add an unexported `granted map[string]bool` field to `Server`, built in `New` from `opts.AllowDestructive`. Nothing else in the exported API changes
- [X] T006 In `mcpserver/authz.go`, replace `authorized(taskName string) bool` with `refusal(taskName string) string`: return the allowlist text when `AllowList` is non-empty and the name is not in it; else the destructive text when `isDestructive(taskName)` and `!s.granted[taskName]`; else `""`. Update the doc comment to describe the two refusals and that no value grants every destructive task
- [X] T007 In `mcpserver/handler.go`, replace the `authorized` check with `if msg := s.refusal(taskName); msg != "" { return errorResult(msg), nil }`. The old "requires explicit approval" text is gone
- [X] T008 Update the existing `mcpserver` test literals the compiler reports: `mcpserver/server_test.go` near lines 100 and 124 and `mcpserver/handler_test.go` near line 62 call no destructive task, so they become `Options{}`
- [X] T009 [P] In `internal/cli/agentexec.go`, drop `AllowDestructive: false` from the `mcpserver.Options` literal in `executeAgent`, leaving `mcpserver.Options{Version: e.opts.Version}`. Add a one-line comment saying why: an `agent` task's own endpoint never permits destructive tasks (spec 031 FR-015)
- [X] T010 [P] In `internal/cli/serve.go` `ServeMCP`, drop `AllowDestructive: opts.Yes` from the `mcpserver.Options` literal for now (US1 T024 passes the grant). `opts.Yes` is never set on this path, so behavior is unchanged
- [X] T011 Run the full suite in Docker. Every test that passed in T002 passes; T003 and T004 pass. `grep -rn "explicit approval" --include='*.go' .` prints nothing

**Checkpoint**: The library takes a list of names. No CLI path grants anything yet.

---

## Phase 3: User Story 1 - Operator lets an agent run one named destructive task (Priority: P1) MVP

**Goal**: `rune mcp --allow-destructive clean` lets an agent call `clean` and get its
real output and exit status, with no prompt (FR-001, FR-002, FR-012, FR-013).

**Independent Test**: Start the server with `--allow-destructive clean` on a Runefile
where `clean` is `[confirm]` and writes a marker file; call `clean` over stdio. The
marker exists and the result carries the body's output and exit status (quickstart
scenarios B and G).

### Tests for User Story 1

- [X] T012 [P] [US1] In `internal/cli/confirm_test.go`, add `TestConfirmGrantApprovesNamedTask`: build `confirmEngine(t, confirmSrc, nil, false, false)`, set `e.grants = map[string]bool{"t": true}`, run `t` through `scheduler.Run`; it succeeds and stdout contains `body`. With a terminal stdin holding `n` and the grant set, the body runs and no prompt appears (a reader that fails on any read cannot tell the gate from the body, which also gets stdin). Without the grant and with nil stdin, the existing silent decline is unchanged (`TestConfirmNoStdinKeepsSilentDecline` still passes)
- [X] T013 [P] [US1] In `internal/cli/mcp_test.go`, add `TestAdapterGrantRunsDestructiveTask`: `a := adapterFor(t, src)` with `[confirm("Delete?")]` task `clean` (`@echo cleaned`) and `[confirm]` task `fail3` (`@exit 3`); set `a.grants = map[string]bool{"clean": true, "fail3": true}`. `a.Call(ctx, "clean", nil)` returns `ExitCode` 0 and `Stdout` containing `cleaned`; `fail3` returns `ExitCode` 3; neither `Stdout` nor `Stderr` contains `Delete?` or `[y/N]`
- [X] T014 [P] [US1] Create `internal/cli/serve_test.go` with table tests for `parseAllowDestructive` (T023) over the "Accepted forms" table of [contracts/cli-serve.md](contracts/cli-serve.md): `nil` gives no names; `["clean"]`; `["clean,reset"]`; `["clean", "reset"]`; `["clean, reset"]` (trimmed); `["clean,clean"]` gives `["clean"]`; `["tools::clean"]`. Order is first occurrence. Add table tests for `startupLine` (T023) over the four rows of the contract's "Startup line" table, comparing exact strings
- [X] T015 [P] [US1] In `cmd/rune/serve_test.go`, add `TestServeAllowDestructiveFlag`: `newServeCmd(&opts)` has a flag `allow-destructive` whose `Value.Type()` is `stringArray` and whose `Usage` is the flag usage text from Fixed texts; `cmd.ParseFlags([]string{"--allow-destructive", "clean,reset", "--allow-destructive", "x"})` leaves `opts.MCPAllowDestructive` equal to `["clean,reset", "x"]` (raw values; splitting happens in `internal/cli`)
- [X] T016 [P] [US1] In `mcpserver/transport_test.go`, add `TestHTTPGrantedDestructiveCall` (FR-013): start `New(eng, Options{AllowDestructive: []string{"clean"}})` with `StartHTTP(HTTPConfig{Addr: "127.0.0.1:0", Token: "secret"})`; connect an `mcp.Client` through `mcp.StreamableClientTransport` whose `HTTPClient` adds `Authorization: Bearer secret` on every request (a small `http.RoundTripper` in the test file); `clean` reaches the engine; a second destructive task added to the fake returns the destructive text and does not reach it. Open two client sessions and call `clean` from both; both reach the engine (grant is not consumed)
- [X] T017 [US1] In `test/integration/secret_masking_mcp_test.go`, add `mcpServerCall(t *testing.T, dir string, serverArgs []string, task, arguments string) (toolResult, stderr string)` holding today's body of `mcpCallArgs` with `exec.Command(runeBin, serverArgs...)`, and make `mcpCallArgs` call it with `[]string{"mcp"}`. No behavior change; the existing MCP integration tests still pass
- [X] T018 [US1] Create `test/integration/destructive_mcp_test.go` with the quickstart scratch Runefile (every body writes a `*.done` marker) and US1 tests using `mcpServerCall` (T017): scenario 1, `--allow-destructive clean` then call `clean`: `clean.done` exists, the result is not `isError`, and the result does not contain `Delete build output?`; scenario 2, add a `[confirm]` task `fail3` with body `@exit 3` and start with `--allow-destructive fail3`: the result has `[exit 3]` and `isError: true`; scenario 3, both `--allow-destructive clean,reset` and `--allow-destructive clean --allow-destructive reset` run `clean` and `reset` (one call per server start); scenario 5, the stderr of a `--allow-destructive clean,reset` server contains `rune MCP server on stdio (destructive tasks allowed: clean, reset)`. Use a fresh `t.TempDir()` per server start so markers never leak between cases

### Implementation for User Story 1

- [X] T019 [P] [US1] In `internal/cli/dispatch.go`, add `MCPAllowDestructive []string` to `Options` after `MCPAllowIgnoreVersion`, with a doc comment: the raw `--allow-destructive` values, an operator-only `rune serve` flag, never settable from a Runefile; `ServeMCP` splits and validates them
- [X] T020 [P] [US1] In `cmd/rune/serve.go`, register `f.StringArrayVar(&opts.MCPAllowDestructive, "allow-destructive", nil, "let agents run these [confirm] tasks (comma-separated, repeatable)")` right after `--ignore-version`. Do not add `--yes` or any other flag (FR-007)
- [X] T021 [US1] Add the per-task confirmation grant (research R3, data-model "Confirmation gate" row 2): in `internal/cli/run.go`, add `grants map[string]bool` to `engine` with a comment saying it is set only on the MCP server path, holds the operator's `--allow-destructive` names, and is nil on the CLI path and the agent endpoint; in `internal/cli/confirm.go` `confirmFailure`, right after the `e.opts.Yes` check, `if e.grants[task.Name] { return nil }`. Keep the check per task name so a grant never approves another task (FR-005)
- [X] T022 [US1] In `internal/cli/mcp.go`, add `grants map[string]bool` to `mcpAdapter` and set `grants: a.grants` in the `engine` literal in `CallLists`. `newMCPAdapter` leaves it nil; `newAgentAdapter` in `internal/cli/agentexec.go` must not set it
- [X] T023 [US1] In `internal/cli/serve.go`, add `parseAllowDestructive(values []string) []string`: split each value on `,`, trim surrounding spaces from each item, drop duplicates keeping first-occurrence order (research R1, data-model "Grant"). US3 T037 adds the empty-name error. Add `startupLine(useHTTP bool, addr string, granted []string) string` returning the unstyled line from the Fixed texts: today's text when `granted` is empty, otherwise the names joined by `, ` inside the parentheses as shown
- [X] T024 [US1] Wire the grant in `ServeMCP` (`internal/cli/serve.go`): call `parseAllowDestructive(opts.MCPAllowDestructive)` first; after `newMCPAdapter` and the transport check, and before `adapter.gatherContext`, set `adapter.grants` to a set of the names; pass the names as `mcpserver.Options{AllowDestructive: names, ...}`; print `startupLine(useHTTP, addr, shown)`, where `shown` keeps the granted names whose task has `ast.AttrConfirm`, in order, through `opts.themeStderr().Muted.Render` for both transports in place of the two inline strings. The analyzer already rejects `[confirm]` anywhere in the `[context]` hook's closure (`checkContextClosure`), so the grant never applies at startup
- [X] T025 [US1] Run the full suite in Docker. T012 to T018 pass, `TestServeBannerStyled` in `test/integration/status_styling_test.go` still passes, and every test that passed in T002 passes

**Checkpoint**: Quickstart scenarios B and G pass. A granted `[confirm]` task runs over stdio and HTTP.

---

## Phase 4: User Story 2 - Destructive tasks not named stay refused (Priority: P1)

**Goal**: A grant never widens: ungranted destructive tasks stay refused as direct
calls, dependencies, post-hooks, and failure hooks, and a server without the flag
behaves as today (FR-003 to FR-006, FR-015).

**Independent Test**: Start servers with and without the flag on a Runefile with
several `[confirm]` tasks, each writing its own marker, wired as direct tasks,
dependencies, post-hooks, and failure hooks. Call each entry point. Only markers of
named tasks appear (SC-002, SC-003).

These tests guard the design from US1. Most should pass once T021 and T022 are in;
run them before T021 (or with T021 reverted) to confirm they detect a grant that
widens, for example by temporarily setting `opts.Yes = true` in `CallLists`.

### Tests for User Story 2

- [X] T026 [P] [US2] In `internal/cli/confirm_test.go`, add `TestConfirmGrantIsPerTask`, a table over the "Outcomes by path" table of [data-model.md](data-model.md). Each row builds `confirmEngine(t, src, nil, false, false)`, sets `e.grants`, runs the called task with `scheduler.Run`, and checks stdout and the error: (a) `release: clean`, grant `clean`: `clean` then `release` bodies run; (b) same Runefile, no grant: neither body runs, run fails; (c) `clean: wipe`, both `[confirm]`, grant `clean`: neither body runs, run fails; (d) non-destructive `build: && wipe`, `wipe` `[confirm]`, no grant: `build` runs, `wipe` does not, run fails; (e) `build: || wipe` with `build` exiting 1, `wipe` `[confirm]`, no grant: `wipe` does not run, the error carries `build`'s exit code 1, stderr has exactly one warning line about the hook; (f) `ship: _wipe` with `[private]` `[confirm]` `_wipe`, grant `_wipe`: both bodies run (FR-006); (g) rows (d) and (e) with grant `wipe`: `wipe` runs. Check the hook syntax against `docs/how-to/dependencies-and-hooks.md`
- [X] T027 [P] [US2] In `internal/cli/mcp_test.go`, add `TestAgentAdapterGrantsNothing` (FR-015): build an engine with `confirmEngine(t, src, nil, false, false)` (it sets `file`, as the CLI run does), set its `grants` to `{"clean": true}` to simulate a leak, and assert `e.newAgentAdapter().grants` is empty; then `mcpserver.New(e.newAgentAdapter(), mcpserver.Options{Version: "test"})` refuses `clean` with the destructive text through `mcp.NewInMemoryTransports`, as `TestServerNeverRegistersOSMismatchedTools` connects
- [X] T028 [US2] In `test/integration/destructive_mcp_test.go`, add US2 tests with marker files, one fresh directory per server start: scenario 1, no flag, call `clean`: result is `isError` with the destructive text naming `--allow-destructive clean`, no `clean.done`; scenario 2, `--allow-destructive clean`, call `reset`: destructive text naming `reset`, no `reset.done`; scenario 3, `--allow-destructive clean`, call `release`: `clean.done` and `release.done`; scenario 4, no flag, call `release`: `isError`, `[exit 1]`, no `clean.done`, no `release.done`; scenario 5, `clean: wipe` both `[confirm]`, `--allow-destructive clean`, call `clean`: no `wipe.done`, no `clean.done`, `isError`; scenario 6, `build: && wipe` and `build2: || wipe` (with `build2` failing), no grant for `wipe`: no `wipe.done` in either case. Add one test that starts with no flag and calls every entry point in turn, then asserts zero `*.done` files from `[confirm]` tasks (SC-003)

### Implementation for User Story 2

- [X] T029 [US2] Confirm `internal/cli/agentexec.go` builds the agent endpoint's `mcpserver.Options` with no `AllowDestructive` and `newAgentAdapter` sets no `grants`; fix whatever T026 to T028 expose in `internal/cli/confirm.go`, `internal/cli/mcp.go`, or `mcpserver/authz.go`. Do not weaken any assertion
- [X] T030 [US2] Run the full suite and the race suite in Docker; T026 to T028 pass

**Checkpoint**: Quickstart scenarios A, C, D, E, and F pass.

---

## Phase 5: User Story 3 - Mistakes in the flag stop the server at startup (Priority: P2)

**Goal**: An empty, unknown, or wildcard name ends the server with exit 2 before any
Runefile code runs; a non-destructive name warns; `--yes` stays unknown (FR-007 to
FR-011).

**Independent Test**: Start the server with each malformed value and check exit
status and stderr; a `[context]` hook writing a marker proves nothing ran (SC-005;
quickstart scenarios H to L).

### Tests for User Story 3

- [X] T031 [P] [US3] In `internal/cli/serve_test.go`, extend the `parseAllowDestructive` tests: `[""]`, `["clean,"]`, `[", clean"]`, `["clean,,reset"]`, and `["  "]` each return a `*UsageError` whose message is exactly `--allow-destructive: empty task name`; valid inputs return a nil error. Update the T014 cases to the new `([]string, error)` signature
- [X] T032 [P] [US3] In `internal/cli/serve_test.go`, add table tests for `checkGrant` (T038) over a task map built with `indexTasks` from a Runefile with `clean` (`[confirm]`), `build`, `[private] [confirm] _wipe`, `[windows] [confirm] winonly` (in the map whatever the host OS, so accepted silently), and an imported `tools::clean` (add it to the map by hand if composing an import is awkward): `claen` gives `unknown task in --allow-destructive: claen (did you mean "clean"?)`; `zzz` gives the message with no suggestion; `*` gives `unknown task in --allow-destructive: *`; `tools__clean` is unknown and suggests `tools::clean`; `clean`, `build`, `_wipe`, `winonly`, `tools::clean` give nil. Run each case twice and assert the same suggestion (candidates are sorted)
- [X] T033 [P] [US3] In `internal/cli/serve_test.go`, add tests for `warnNonDestructive` (T039), with color off: names `["build", "clean"]` write exactly one line, `warning: --allow-destructive build has no effect: task "build" is not destructive`; names `["_wipe", "winonly", "clean", "tools::clean"]` write nothing
- [X] T034 [P] [US3] In `internal/cli/serve_test.go`, add `TestServeMCPUsageErrorRunsNoRunefileCode` (FR-010): in a temp dir, write a Runefile whose `[context]` hook runs `@echo ok > context.done` plus a task `clean`; call `ServeMCP` with `Options{Cwd: dir, MCPAllowDestructive: v, ...}` for `v` in `[""]`, `["clean,"]`, `["claen"]`, `["*"]`; each returns a `*UsageError` and `context.done` does not exist. Also call it with `[""]` in a temp dir with no Runefile: the error is the empty-name `*UsageError`, not the missing-Runefile error (empty name is checked first, per the contract's error order)
- [X] T035 [P] [US3] In `cmd/rune/serve_test.go`, add `TestServeRejectsYes` (FR-007): `run` from `cmd/rune/main.go` (it takes the arguments after the program name) returns 2 for `run([]string{"mcp", "--yes"})` and `run([]string{"--yes", "mcp"})`, and so does `run([]string{"mcp", "--allow-destructive"})` (missing argument). Capture or discard stderr the way other tests in `cmd/rune` do
- [X] T036 [US3] In `test/integration/destructive_mcp_test.go`, add the startup checks from quickstart scenarios H to L against the binary, each in a fresh directory with the scratch Runefile: H, `mcp --allow-destructive claen` exits 2, stderr contains `rune: unknown task in --allow-destructive: claen (did you mean "clean"?)`, no `context.done`; I, `mcp --allow-destructive ""` and `mcp --allow-destructive clean,` exit 2 with `rune: --allow-destructive: empty task name`, no `context.done`; J, `mcp --allow-destructive '*'` (pass `*` as one argument, no shell) exits 2 naming `*`; K, `runWithStdin(t, dir, "", "mcp", "--allow-destructive", "build", "--color=never")`: stderr has exactly one line equal to the warning text, followed by the unchanged startup line `rune MCP server on stdio`; L, `mcp --yes` and `--yes mcp` exit 2 with `unknown flag: --yes`

### Implementation for User Story 3

- [X] T037 [US3] In `internal/cli/serve.go`, change `parseAllowDestructive` to return `([]string, error)` and reject an item that is empty after trimming with `&UsageError{Err: errors.New("--allow-destructive: empty task name")}` (or the package's `usagef` helper if it yields the same type and text). Move the call in `ServeMCP` to the very top, before `config.Resolve`
- [X] T038 [US3] In `internal/cli/serve.go`, add `checkGrant(names []string, tasks map[string]*ast.Task) error`: for the first name not in `tasks`, return a `*UsageError` with `unknown task in --allow-destructive: NAME`, appending ` (did you mean %q?)` when `nearest(name, candidates)` finds one; `candidates` is the sorted keys of `tasks`. Call it in `ServeMCP` right after `newMCPAdapter` returns, before the HTTP token is read
- [X] T039 [US3] In `internal/cli/serve.go`, add `warnNonDestructive(w io.Writer, theme style.Theme, names []string, tasks map[string]*ast.Task)` that prints, for each name whose task has no `ast.AttrConfirm`, `%s: --allow-destructive %s has no effect: task %q is not destructive` with `theme.Warning.Render("warning")`, the shared style used in `internal/cli/version_gate.go`. Call it in `ServeMCP` after the transport check and before `adapter.gatherContext`, with `opts.Stderr` and `opts.themeStderr()`. Use the actual `style` package import path already used by `internal/cli/run.go`
- [X] T040 [US3] Run the full suite in Docker; T031 to T036 pass and the US1 and US2 tests still pass

**Checkpoint**: Quickstart scenarios H, I, J, K, and L pass.

---

## Phase 6: User Story 4 - The documentation describes the opt-in that exists (Priority: P3)

**Goal**: The MCP guide, the CLI reference, the language guide, the agent example, and
`rune serve --help` describe `--allow-destructive` the same way, and no page says
`--yes` approves anything over MCP (FR-017, SC-006; research R8).

**Independent Test**: Read the changed sections and `rune serve --help`; the docs
harness in `test/docs` passes.

New prose must not use the em dash, even where the surrounding text does.

### Tests for User Story 4

- [X] T041 [P] [US4] In `cmd/rune/serve_test.go`, add `TestServeHelpMentionsAllowDestructive`: `newServeCmd(&opts).Long` contains `--allow-destructive` and `[confirm]`; `.Example` contains the line `  rune mcp --allow-destructive clean                  # let agents run the [confirm] task 'clean'` from [contracts/cli-serve.md](contracts/cli-serve.md)

### Implementation for User Story 4

- [X] T042 [P] [US4] In `cmd/rune/serve.go`, add one sentence to `Long`: tasks marked `[confirm]` are refused unless the operator names each one with `--allow-destructive`, and no option allows all of them. Add the Example line from T041 after the `rune serve` line, aligned with the existing comments
- [X] T043 [P] [US4] In `docs/mcp.md` "Starting the server", add a stdio example `rune mcp --allow-destructive clean` with one sentence: it lets agents run the `[confirm]` task `clean`; name each task, comma-separated or with the flag repeated
- [X] T044 [US4] In `docs/mcp.md` "Security model (secure by default)", replace the first two bullets ("Read-only by default", "Destructive tasks are gated") with: destructive (`[confirm]`) tasks are refused by default and still listed with the MCP `DestructiveHint`; the operator allows them one by one with `--allow-destructive clean,reset` (or the flag repeated) when starting the server; a grant also lets the named task run when another task reaches it as a dependency or hook, and never covers any other task; there is no option that allows all destructive tasks, and `--yes` is not accepted by `rune serve` or `rune mcp`. Update the `clean` comment in the code block below to say it is refused over MCP unless allowed with `--allow-destructive clean`
- [X] T045 [US4] In `docs/mcp.md` "Agent task bodies", replace "Agent tasks default to **read-only** tool access; granting them destructive capability is an explicit opt-in." with: the tasks an `agent` task exposes to its own agent never include destructive tasks; neither `--allow-destructive` nor `--yes` changes that
- [X] T046 [P] [US4] In `docs/cli.md`, change the `rune serve` row of the commands table (near line 110) to `rune serve [--http] [--addr <addr>] [--token-file <file>] [--allow-destructive <task>[,<task>...]]` and add to its description: `--allow-destructive` lets agents run the named `[confirm]` tasks, comma-separated or repeated, with no option that allows all of them. Leave the "Global flags" section unchanged
- [X] T047 [P] [US4] In `docs/runefile.md`, in the `[confirm]` paragraph near line 369, replace "Over MCP, `[confirm]` tasks are gated separately; see" with "Over MCP, `[confirm]` tasks are refused unless the operator names them with `--allow-destructive`; see", keeping the existing link
- [X] T048 [P] [US4] In `docs/examples/agent-driven/README.md` near line 25, replace "so an agent can't run it unattended (use `--yes` to auto-approve deliberately)" with wording that says an agent can't run it unless the server is started with `--allow-destructive reset-db`; in `docs/use-cases/mcp-agents.md`, rewrite the callout near lines 51-53 the same way, naming `--allow-destructive reset-db`. The sentence being replaced contains an em dash, and the new one must not
- [X] T049 [US4] Run `go test ./test/docs` in Docker (`TestCLIReferenceMatchesBinary`, code blocks, links) and the T041 test; all pass

**Checkpoint**: Quickstart "Docs check" passes.

---

## Phase 7: Polish and delivery

**Purpose**: Full gates, manual quickstart pass, and the PR.

- [X] T050 Run the full suite, the race suite, and lint in Docker (Commands); all green
- [X] T051 Build the binary in Docker and walk quickstart scenarios A to M of `specs/031-mcp-destructive-optin/quickstart.md` by hand, including M over HTTP with a token file; record any mismatch and fix it before continuing
- [X] T052 [P] Check that no stale references remain: `grep -rn "AllowDestructive: true\|AllowDestructive: false\|AllowDestructive: opts.Yes\|explicit approval" --include='*.go' .` prints nothing, and `grep -rn "auto-approve" docs/` and `grep -n "per-task opt-in" docs/mcp.md` print nothing (`docs/runefile.md:416` uses that phrase for caching, correctly)
- [X] T053 [P] Check that no added line uses an em dash: `git diff origin/main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` prints nothing
- [ ] T054 When the maintainer asks for the PR: run `git config user.name`, `gh auth status`, switch with `gh auth switch --user <name>` if the active account differs, and confirm with `gh auth status`. Open one PR from `031-mcp-destructive-optin` titled exactly `fix(mcp)!: breaking: rune mcp --allow-destructive TASK lets agents run named [confirm] tasks; mcpserver.Options.AllowDestructive is now a task list`, with `Closes #31` and the embedder migration table from [contracts/mcpserver-api.md](contracts/mcpserver-api.md) in the body. No Co-Authored-By trailer and no tool attribution in commits or the PR. Wait for every check to finish green before reporting the PR as done

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: after Setup. Blocks every story, since `Options.AllowDestructive` changes type.
- **US1 (Phase 3)**: after Phase 2.
- **US2 (Phase 4)**: after US1. Its tests start servers with `--allow-destructive` and set `engine.grants`, both added in US1.
- **US3 (Phase 5)**: after US1 (it extends `parseAllowDestructive` and `ServeMCP`). Independent of US2; US2 and US3 can run in parallel.
- **US4 (Phase 6)**: after US1 for T041 and T042 (the flag must exist). T043 to T048 are prose and can start any time after Phase 2; T049 runs last.
- **Polish (Phase 7)**: after all stories.

### Within each phase

- Tests are written first and fail (or fail to compile) before the implementation task.
- `mcpserver/server.go` (T005) before `authz.go` (T006) before `handler.go` (T007).
- `dispatch.go` and `cmd/rune/serve.go` (T019, T020) before `ServeMCP` wiring (T024).
- `engine.grants` and `confirmFailure` (T021) before the adapter (T022) before `ServeMCP` (T024).
- `parseAllowDestructive` error (T037) before `checkGrant` (T038) before `warnNonDestructive` (T039), all in one file, so in sequence.

### Parallel opportunities

- Phase 2: T003 and T004; then T009 and T010 beside T005 to T008.
- Phase 3: T012 to T016 (five different files); T019 and T020.
- Phase 4: T026 and T027.
- Phase 5: T031 to T035 (T031 to T034 share `internal/cli/serve_test.go`; split them between people only if each adds separate test functions).
- Phase 6: T042, T043, T046, T047, T048 (different files). T044 and T045 edit `docs/mcp.md` after T043.
- Phase 7: T052 and T053.

---

## Parallel Example: User Story 1

```text
Task: "T012 Grant approves the named task in internal/cli/confirm_test.go"
Task: "T013 Adapter with grants runs clean and fail3 in internal/cli/mcp_test.go"
Task: "T014 parseAllowDestructive and startupLine tables in internal/cli/serve_test.go"
Task: "T015 allow-destructive flag registered in cmd/rune/serve_test.go"
Task: "T016 Granted call over StartHTTP with a bearer token in mcpserver/transport_test.go"

# After the tests fail as expected:
Task: "T019 Options.MCPAllowDestructive in internal/cli/dispatch.go"
Task: "T020 --allow-destructive flag in cmd/rune/serve.go"
```

## Parallel Example: User Story 4

```text
Task: "T042 Long and Example in cmd/rune/serve.go"
Task: "T043 Starting the server example in docs/mcp.md"
Task: "T046 rune serve row in docs/cli.md"
Task: "T047 [confirm] paragraph in docs/runefile.md"
Task: "T048 agent-driven example README"
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phase 1, then Phase 2 (library takes a list of names; new refusal texts).
2. Phase 3: the flag, the per-task confirmation grant, and the startup line.
3. Stop and validate with quickstart scenarios B and G. This closes the defect in
   issue #31: a named `[confirm]` task is callable over MCP.

### Incremental delivery

All phases ship in one PR (plan "Delivery"). Within the branch:

1. Phases 1 to 3: MVP.
2. Phase 4: proof that the grant never widens (the safety half of the P1 pair).
   Do not open the PR without it.
3. Phase 5: startup validation.
4. Phase 6: docs and help text, which the constitution requires in the same PR.
5. Phase 7: gates, quickstart, PR.

---

## Notes

- `[P]` tasks touch different files and depend on no incomplete task.
- The CLI path (`rune clean`, `rune --yes clean`) must not change; T002's baseline and
  the existing `internal/cli/confirm_test.go` tests guard it.
- Out of scope, tracked elsewhere: `--allow`/`--deny` (#39), refusing a run before its
  closure starts (#38), `-f` on `rune mcp` (#44), per-call approval (#47),
  dependency-aware hints (#37), a warning for non-localhost binds (#43).
