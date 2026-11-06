---

description: "Task list for 032-mcp-unknown-arguments"
---

# Tasks: Reject Unknown MCP Tool Arguments

**Input**: Design documents from `specs/032-mcp-unknown-arguments/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/mcp-tools.md](contracts/mcp-tools.md),
[quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI requires test-first, multi-layer
verification, and research R6 names the test files per layer. Write each test task first and
confirm it fails before the implementation task it covers. Tests that guard unchanged
behavior pass on main and must stay green; those tasks say so.

**Organization**: One phase per user story, in priority order. There is no foundational
phase, because nothing blocks both stories: only US1 needs the shared suggestion rule, and
US2 is one schema key plus its tests.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: the user story the task serves (US1 or US2)

## Commands

Tests run in Docker only.

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

Add `-run` and a package path to the first command for a focused run. Check the golangci-lint
pin in `.github/workflows/ci.yml` before linting.

## Fixed texts

Copy these exactly. Source: [contracts/mcp-tools.md](contracts/mcp-tools.md) and research R2.

| Use | Text |
|---|---|
| unknown name with a close parameter | `unknown argument "NAME" for task "TASK" (did you mean "PARAM"?)` |
| unknown name with none | `unknown argument "NAME" for task "TASK"` |
| several names | one line per name, sorted with `slices.Sort`, joined by `\n`, no trailing newline |
| over 20 names | a final line `unknown arguments not shown for task "TASK": COUNT` |
| name over 64 characters | cut off at 64 with `...`, no suggestion |
| null value | `invalid arguments: parameter "PARAM": null is not a valid value` |
| object value | `invalid arguments: parameter "PARAM": an object is not a valid value` |
| nested array | `invalid arguments: parameter "PARAM": a nested array is not a valid value` |

`TASK` is the Runefile task name (`tools::build`), never the tool name (`tools__build`). The
format verbs are `%q`, so `NAME` is quoted and escaped. No `invalid arguments:` prefix and no
`[exit N]` line. Existing texts that must not change: `task "NAME" is not authorized for this
session`, the spec 031 destructive refusal, `task "NAME" is missing required argument "PARAM"`,
and `invalid arguments: ...`.

---

## Phase 1: Setup

**Purpose**: Confirm the starting point.

- [X] T001 Confirm the starting state: `git branch --show-current` prints `032-mcp-unknown-arguments` and `git status --short` shows only `?? specs/032-mcp-unknown-arguments/`. Run `git fetch origin`; if `origin/main` has moved past `48e26c8`, run `git merge --ff-only origin/main` (the branch has no commits of its own yet)
- [X] T002 Run the full suite in Docker (first command under Commands) before any change and record any test that already fails, so later failures can be attributed

---

## Phase 2: User Story 1 - A misspelled argument is refused, not ignored (Priority: P1) MVP

**Goal**: A tool call naming an undeclared argument gets an error result with the exact text
and a suggestion, and nothing runs: no body, dependency, or hook.

**Independent Test**: Start the server on the quickstart Runefile, whose bodies write marker
files. Call `build(targte="release")`. The result is an error with the exact text above, and
no marker file exists.

### The shared suggestion rule

Moves first because `mcpserver` cannot import `internal/cli`, which imports it (research R3).
This is the one Complexity Tracking row in the plan.

- [X] T003 [US1] Move the test: `git mv internal/cli/suggest_test.go internal/suggest/suggest_test.go`, set `package suggest`, and call `Nearest`. Keep every `TestNearest` case and the `//nolint:misspell` comment on the `versoin` row, change the failure message to `Nearest(%q) = ...`, and reword the leading comment, which describes `nearest` as a helper of the "unknown task" error. Add the row `{"tesxyz", "", false}` to `TestNearest`: it is 3 edits from `test`, so it pins the two-edit limit, which the other miss, 10 edits away, does not. Add `TestNearestTieGoesToFirstCandidate` (`Nearest("tad", []string{"tag", "tab"})` is `tag`; with the list reversed it is `tab`) and `TestNearestNoCandidates` (`Nearest("x", nil)` is `"", false`). Confirm `go test ./internal/suggest/` fails to compile with `undefined: Nearest`
- [X] T004 [US1] Move the code: `git mv internal/cli/suggest.go internal/suggest/suggest.go`, set `package suggest` with a one-line package comment, rename `nearest` to `Nearest`, and leave `levenshtein` unexported and both bodies unchanged. Update the callers at `internal/cli/args.go:39` and `internal/cli/serve.go:195` to `suggest.Nearest`, importing `github.com/rune-task-runner/rune/internal/suggest` in the local import group. Confirm `go test ./internal/suggest/ ./internal/cli/` passes and `grep -rn "nearest(" --include='*.go' internal mcpserver` prints nothing

### Tests for User Story 1

- [X] T005 [US1] Add a fixture and the exact-text table to `mcpserver/handler_test.go` (FR-001, FR-002, SC-002). Fixture `unknownArgsEngine()` returns a `*fakeEngine` with tasks `build` (`target`), `deploy` (`env` required enum `staging`/`prod`, then `replicas`), `each` (`names` required variadic), `clean_all` (none), `wipe` (destructive, none), `tools::build` (`target`), `tie` (`tag`, then `tab`), `get` (`id`), and `result` `Stdout: "ran"`. Test `TestHandlerRefusesUnknownArgument` connects with `connect` and, per row, asserts `IsError`, that `contentText` equals the text exactly, and that `eng.lastCall` stays empty. Rows: all seven in the contract's "`tools/call` with an undeclared name" table; `build(target="a", targte="b")` suggests `target`; `deploy(env="nope", replicaz="5")` gives only the `replicaz` line; `deploy(replicaz="5", evn="prod")` gives the two contract lines, `evn` first; `each(namez=["a","b"])` suggests `names`; `tie(tad="1")` suggests `tag`, the parameter declared first, where alphabetical order would give `tab` (FR-004); `get(x="1")` suggests `id` (the loose short-name case in the spec's edge cases); `get(targte="1")` has no suggestion, because `target` belongs to another task (FR-004). Escaping (FR-003): the name `a<newline>b` gives `unknown argument "a\nb" for task "build"` with a literal backslash-n, the empty name gives `unknown argument "" for task "build"`, and the name `a"b` gives `unknown argument "a\"b" for task "build"`
- [X] T006 [US1] Add `TestHandlerUnknownArgumentIgnoresValue` to `mcpserver/handler_test.go` (FR-007, FR-008): `build` with `zzzzzz` set to each of `"x"`, `1`, `true`, `nil`, `[]any{"a"}`, and `map[string]any{"k": "v"}` returns the same text and never reaches the engine. `build` with `targte` set to `"s3cret"` and to `[]any{"s3cret"}` returns text that does not contain `s3cret`
- [X] T007 [US1] Add `TestHandlerUnknownArgumentAfterRefusals` to `mcpserver/handler_test.go` (FR-006). This guards behavior that holds on main; it fails only if the new check is placed before `refusal`, so confirm that by trying it once in T018. With `Options{AllowDestructiveFlag: "--allow-destructive"}`, `wipe(forcee=true)` returns exactly the destructive refusal text. With `Options{AllowList: []string{"get"}}`, `build(targte="x")` returns exactly `task "build" is not authorized for this session`. Neither text mentions the argument, and the engine does not run
- [X] T008 [US1] Add `TestHandlerUnknownArgumentGrantedDestructive` to `mcpserver/handler_test.go` (FR-001, FR-006). With `Options{AllowDestructive: []string{"wipe"}, AllowDestructiveFlag: "--allow-destructive"}`, `wipe(forcee=true)` returns exactly `unknown argument "forcee" for task "wipe"`, `IsError` is set, and `eng.lastCall` stays empty. `wipe` with no arguments then runs, which shows the grant is in effect. Unlike T007, this fails on main, where the task runs
- [X] T009 [US1] Add `TestHandlerUnknownArgumentSkipsListEngine` to `mcpserver/handler_test.go` (FR-011): with a `fakeListEngine` for a task with variadic `names`, a call with `namez=["a","b"]` is refused, `viaLists` stays false, and `lastCall` stays empty
- [X] T010 [US1] Add `TestHandlerUnknownArgumentStableText` to `mcpserver/handler_test.go` (FR-005, SC-005): on one session, call `deploy` 100 times with `replicaz`, `evn`, and `zzzzzz`. Every result equals the same three lines, in the order `evn`, `replicaz`, `zzzzzz`
- [X] T011 [US1] Add `TestHandlerRawArguments` to `mcpserver/handler_test.go` (FR-012). This guards behavior that holds on main. Call the handler directly so the raw bytes are exact: `srv.handler(task)(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "build", Arguments: json.RawMessage(raw)}})`. A nil `Arguments`, `null`, and `{}` reach the engine with no error. `[]` and `"x"` return text starting with `invalid arguments: json: cannot unmarshal` and do not reach the engine
- [X] T012 [P] [US1] Add helper `httpSession(t, addr, token)` and `TestHTTPUnknownArgumentRefused` to `mcpserver/transport_test.go` (FR-010). The helper is the `session` closure from `TestHTTPGrantedDestructiveCall`, including its cleanup; leave that existing test as it is. With a `syncEngine` holding `build` (`target`), `build(targte="release")` over `StartHTTP` with a token returns the exact text and `IsError`, and `eng.called()` is empty. `build(target="release")` then runs, and `eng.called()` is `["build"]`
- [X] T013 [P] [US1] Add helper `agentSession(t, adapter)` and `TestAgentEndpointRefusesUnknownArgument` to `internal/cli/mcp_test.go` (FR-010). The helper is the in-memory client setup from `TestAgentAdapterGrantsNothing`, which stays as it is. Build the engine with `confirmEngine(t, src, nil, false, false)`, using a body that writes to an absolute marker path (`filepath.ToSlash(filepath.Join(t.TempDir(), "build.done"))`, single-quoted, since the body's working directory is not what this test is about). `build(targte="release")` returns the exact text and `IsError`, and the marker does not exist. `build(target="release")` then creates the marker, which proves the marker can detect a run
- [X] T014 [P] [US1] Create `test/integration/unknown_args_mcp_test.go` with const `unknownArgsRunefile` copied from the quickstart's scratch Runefile, helper `argCall(t, dir, flags, task, arguments) (text string, isError bool)` built on `mcpServerCall` with `append([]string{"mcp"}, flags...)` and decoding `result.content[].text` and `result.isError` as `toolCall` does, and `TestUnknownArgsMCP_Refused` (FR-001, SC-001): quickstart scenarios A to L as a table, each in a fresh `writeRunefile` directory. Assert `isError`, the exact text (scenario I is the destructive refusal for `wipe`), no `[exit` in the text, and that `filepath.Glob(filepath.Join(dir, "*.done"))` is empty. Scenario D proves no dependency or hook ran, and scenario K also asserts the text lacks `s3cret`
- [X] T015 [US1] Add `TestUnknownArgsMCP_DeclaredUnchanged` to `test/integration/unknown_args_mcp_test.go` (FR-012, SC-004). This guards behavior that holds on main. Quickstart scenarios M to R, each in a fresh directory: M, `build.done` holds `built release`; N with `{}` and with `null`, `build.done` holds `built debug` (the no-arguments form is T011, because `mcpServerCall` always sends the key); O, `each.done` exists; P, the exact missing-argument text; Q, text starting with `invalid arguments: json: cannot unmarshal` and no marker; R, `prep.done`, `release.done`, and `notify.done` exist, `recover.done` does not, and `release.done` holds `released 2`. Compare file contents after `strings.TrimSpace`
- [X] T016 [US1] Add two tests to `test/integration/unknown_args_mcp_test.go`. `TestUnknownArgsMCP_GrantedDestructive` (FR-001, quickstart scenario U): `argCall` with flags `--allow-destructive wipe` and `wipe` with `{"forcee":true}` returns `isError` and exactly `unknown argument "forcee" for task "wipe"`, and `wipe.done` does not exist. `TestUnknownArgsMCP_ImportedTask` (FR-003, quickstart scenario V): after `writeRunefile` with `mod tools "tools.rune"`, write `tools.rune` beside it with `build target="debug"` and a body that writes `tool.done`; `tools__build` with `{"targte":"x"}` returns `isError` and exactly `unknown argument "targte" for task "tools::build" (did you mean "target"?)`, and `tool.done` does not exist
- [X] T017 [US1] Run the new tests in `mcpserver/`, `internal/cli/`, and `test/integration/` and confirm they fail for the expected reason: `TestHandlerRefusesUnknownArgument`, `TestHandlerUnknownArgumentIgnoresValue`, `TestHandlerUnknownArgumentGrantedDestructive`, `TestHandlerUnknownArgumentSkipsListEngine`, `TestHandlerUnknownArgumentStableText`, `TestHTTPUnknownArgumentRefused`, `TestAgentEndpointRefusesUnknownArgument`, `TestUnknownArgsMCP_Refused`, `TestUnknownArgsMCP_GrantedDestructive`, and `TestUnknownArgsMCP_ImportedTask` report a wrong result (a call that succeeded, a run that happened, or a different error text), not a compile or setup error. Row I of `TestUnknownArgsMCP_Refused` passes, since it is the destructive refusal. Confirm the guards `TestHandlerUnknownArgumentAfterRefusals`, `TestHandlerRawArguments`, and `TestUnknownArgsMCP_DeclaredUnchanged` pass

### Implementation for User Story 1

- [X] T018 [US1] Add `unknownArgs(task string, raw json.RawMessage, params []ParamInfo) string` to `mcpserver/handler.go` and call it in `handler` after `refusal` and before `decodeArgs`, returning `errorResult(msg)` when non-empty (research R1, R2). Decode with `json.NewDecoder(bytes.NewReader(raw)).Decode(&m)` into `map[string]json.RawMessage`, so values are never read (FR-007) and a number that only `UseNumber` can hold is not a decode error. On any decode error return `""`, so `decodeArgs` reports today's error. Collect the names not in `params`, using exact comparison, then `slices.Sort` them; build each line from Fixed texts, passing the parameter names in declaration order to `suggest.Nearest`; join with `\n`. Do not change `decodeArgs`. Add the new step to the `handler` doc comment. To confirm T007 can fail, move the call above `refusal` once, watch T007 fail, and move it back
- [X] T019 [US1] In `mcpserver/server.go`, add a comment to `TaskInfo.Params` saying these are the only argument names a call may use and that a call naming any other is refused before the engine runs (FR-011). No exported name or signature changes
- [X] T020 [US1] Run `go test ./internal/suggest/ ./mcpserver/ ./internal/cli/ ./test/integration/` in Docker. All pass, including `TestDecodeArgsLists` with its `unknown` key assertion and every other existing test, with no existing assertion changed

**Checkpoint**: Quickstart scenarios A to R, U, and V pass. Issue #36's defect is closed on every
transport.

---

## Phase 3: User Story 2 - Tool schemas say that no other names are allowed (Priority: P2)

**Goal**: Every tool's input schema has `additionalProperties: false` and is otherwise
unchanged, so a client can catch a typo before calling.

**Independent Test**: List the tools for a Runefile with tasks of every parameter shape plus a
destructive task. Every input schema has the keyword, and nothing else in it has changed.

### Tests for User Story 2

- [X] T021 [US2] Add `TestInputSchemaDisallowsOtherNames` to `mcpserver/server_test.go` (FR-009). Assert `additionalProperties` is `false` for no parameters and for defaulted, required, each typed kind (`enum`, `number`, `boolean`, `path`, `raw`), and variadic parameters. Assert with `reflect.DeepEqual` that the three schemas in the contract's `tools/list` table match in full, with `required` as `[]string`, so nothing else in a schema changed
- [X] T022 [US2] Add `TestListToolsDisallowOtherNames` to `mcpserver/server_test.go` (US2 scenarios 1 to 3). Through `connect` and `ListTools` on `sampleEngine()`, every tool's `InputSchema` decodes to a `map[string]any` with `additionalProperties` false, including the destructive `clean` and the namespaced `docker__push`. Assert `logs`, which has no parameters, has an empty `properties` map. Assert the tool count is 5, so an empty list cannot pass
- [X] T023 [P] [US2] Add `TestHTTPToolsListMatchesStdio` to `mcpserver/transport_test.go` (FR-010, US2 scenario 4), using `httpSession` from T012: `reflect.DeepEqual` between the `ListTools` tools over HTTP and over `connect` for the same tasks, and at least one tool shows `additionalProperties` false, since matching alone would pass with the key missing from both
- [X] T024 [P] [US2] Add `TestAgentEndpointSchemasDisallowOtherNames` to `internal/cli/mcp_test.go` (FR-010, US2 scenario 4), using `agentSession` from T013: every tool the agent endpoint lists has `additionalProperties` false
- [X] T025 [P] [US2] Add `TestUnknownArgsMCP_ToolsList` to `test/integration/unknown_args_mcp_test.go` (SC-003, quickstart scenario S), using `unknownArgsRunefile` from T014. Decode the `mcpToolsList` reply; all nine tools (`build`, `deploy`, `each`, `clean_all`, `prep`, `notify`, `recover`, `release`, `wipe`) are present and each `inputSchema.additionalProperties` is `false`; `clean_all`'s whole schema equals `{"additionalProperties":false,"properties":{},"type":"object"}` after decoding
- [X] T026 [US2] Run the T021 to T025 tests in `mcpserver/`, `internal/cli/`, and `test/integration/` and confirm each fails on the missing key, not on a compile or setup error

### Implementation for User Story 2

- [X] T027 [US2] In `inputSchema` in `mcpserver/server.go`, add `"additionalProperties": false` to the `schema` map literal, beside `type` and `properties` (research R5), and note it in the function's doc comment
- [X] T028 [US2] Run `go test ./mcpserver/ ./internal/cli/ ./test/integration/` in Docker. All pass, with no existing assertion changed

**Checkpoint**: Quickstart scenario S passes. Both stories work together.

---

## Phase 4: Docs and delivery

**Purpose**: The MCP guide, full gates, and a manual quickstart pass (FR-013).

- [X] T029 In `docs/mcp.md`, under "How tasks become tools", replace the parameters bullet with: ``- A task's **parameters** define the tool's input schema. Defaults and variadic parameters carry over, and the schema allows no other names (`additionalProperties: false`).`` In the paragraph below it, after the sentence `A call with invalid arguments is rejected before the task runs and returns an error without the marker.`, add: ``A call that names an argument the task does not declare is refused the same way: `unknown argument "targte" for task "build" (did you mean "target"?)`.`` Wrap both to the page's line width. Leave the rest of the page unchanged (FR-013)
- [X] T030 Run the full suite, the race suite, and lint in Docker (Commands); all green, and `./test/docs` is part of the first. Lint's `misspell` flags deliberate typos such as `versoin`: mark only the flagged lines with `//nolint:misspell // intentional typo: ...`, as `internal/suggest/suggest_test.go` does, and do not reword a test to avoid it
- [X] T031 Build the binary in Docker and walk `specs/032-mcp-unknown-arguments/quickstart.md` by hand: scenarios A to S, U, and V on stdio, N with no `arguments` key at all, and T with `rune serve --http --token-file tok`. Record any mismatch and fix it before continuing
- [X] T032 [P] Check that no added line uses an em dash: `git diff main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` and `grep -P '\x{2014}' internal/suggest/*.go test/integration/unknown_args_mcp_test.go specs/032-mcp-unknown-arguments/*.md specs/032-mcp-unknown-arguments/*/*.md` both print nothing
- [X] T033 [P] Check the scope of the diff: `git status --short` lists only the files in the plan's Project Structure, plus `specs/032-mcp-unknown-arguments/`, and `git diff main -- internal/cli/args.go mcpserver/handler.go` shows no change to `decodeArgs` or `candidateNames`

---

## Phase 5: Code review fixes

**Purpose**: Fix all eight code review findings, including the four first dismissed, at the
maintainer's request. Every bug gets a test that fails against it first. These tasks supersede
T018's "do not change `decodeArgs`" and the "no existing assertion changed" in T020 and T028.

- [X] T034 Add failing tests to `mcpserver/handler_test.go` and `test/integration/unknown_args_mcp_test.go` and watch them fail: `TestHandlerRefusesNullAndObjectValues` and `TestUnknownArgsMCP_NullAndObjectValues` (FR-015); `TestHandlerUnknownArgumentCapsEcho` (FR-005); `TestHandlerInvalidValuesReportFirstDeclared` (FR-006); new `TestHandlerRefusesUnknownArgument` rows for a case-only name and a tie with one parameter already set (FR-004); and `TestDecodeArgsLists`, whose `unknown` key assertion now says an undeclared name does not reach the task's values. `TestHandlerRawArguments` gains `1e999` rows as a guard for the single decode
- [X] T035 In `mcpserver/handler.go`, decode once with `decodeArgs`, run `unknownArgs` on the decoded map, and replace the value loop with `stringArgs` and `scalarArg`, going over the task's parameters so the `known` branches are gone and the first declared invalid value is reported (findings 1, 4, 8, research R1, R8). Build the parameter names once in the `handler` closure
- [X] T036 In `mcpserver/handler.go`, order suggestion candidates with unset parameters first, add `closestParam` for a case-only difference, and cap the refusal at 20 lines and 64 characters per name (findings 5, 6, 7, research R2, R3)
- [X] T037 Share test setup with no assertion changes: `TestHTTPGrantedDestructiveCall` uses `httpSession` in `mcpserver/transport_test.go`; `agentSession` becomes `serverSession` and `resultText` is added in `internal/cli/mcp_test.go`, used by `TestServerNeverRegistersOSMismatchedTools` and `TestAgentAdapterGrantsNothing`; `toolCall` and `argCall` share `toolReply` in `test/integration/destructive_mcp_test.go` (finding 3)
- [X] T038 Update `spec.md`, `data-model.md`, `contracts/mcp-tools.md`, `research.md`, `plan.md`, `quickstart.md`, and `docs/mcp.md` to match, and put the embedder change and the value refusal in the PR title (finding 2, FR-014)
- [X] T039 Run the full suite, the race suite, and lint in Docker, walk the quickstart again on a binary built in Docker, including scenario W, and repeat the review's reproductions
- [ ] T040 When the maintainer asks for the PR: run `git config user.name`, `gh auth status`, switch with `gh auth switch --user <name>` if the active account differs, and confirm with `gh auth status`. Open one PR from `032-mcp-unknown-arguments` titled exactly `fix(mcp)!: breaking: a tool call with an unknown argument or a null or object value now fails instead of running with the wrong value; mcpserver.TaskInfo.Params now lists the accepted names`, with a body that starts `Closes #36.` and says that a call with an undeclared argument or a null or object value, which used to succeed, now fails, that an embedder's task descriptors now define the accepted names, and that `TestUnknownArgsMCP_Refused` replaces `TestMCPE2E_Pending_UnknownArgumentRejected`, which the issue lists but this repository does not have (FR-014, research R7). Add no `Co-Authored-By` trailer or "Generated with" line. Wait for the PR checks to finish green before reporting done. Use `gh api` with PATCH for edits, since `gh pr edit` fails in this repository

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **US1 (Phase 2)**: after Setup.
- **US2 (Phase 3)**: after Setup. Its code is independent of US1. T023, T024, and T025 reuse helpers from T012, T013, and T014; if US2 goes first, create those helpers in the US2 tasks instead.
- **Docs and delivery (Phase 4)**: after both stories.
- **Code review fixes (Phase 5)**: after Phase 4. T040, the PR, comes last.

### Within each phase

- T003 before T004: the moved test fails to compile, then the moved code makes it pass.
- T004 before T018, which calls `suggest.Nearest`.
- T005 before T006 to T011, which reuse its fixture. All seven edit `mcpserver/handler_test.go`, so they run in sequence.
- T014 before T015 and T016: same new file.
- T017 before T018: watch the failures first.
- T021 before T022: same file.

### Parallel opportunities

- Phase 2: T012, T013, and T014 touch three different files and can run beside T005 to T011.
- Phase 3: T023, T024, and T025 touch three different files and can run beside T021 and T022.
- Phase 4: T032 and T033.

---

## Parallel Example: User Story 1

```text
Task: "T012 Refusal over StartHTTP in mcpserver/transport_test.go"
Task: "T013 Agent endpoint refusal with the real adapter in internal/cli/mcp_test.go"
Task: "T014 Quickstart scenarios A to L over stdio in test/integration/unknown_args_mcp_test.go"
```

## Parallel Example: User Story 2

```text
Task: "T023 tools/list over HTTP matches stdio in mcpserver/transport_test.go"
Task: "T024 Agent endpoint schemas in internal/cli/mcp_test.go"
Task: "T025 Every listed schema over stdio in test/integration/unknown_args_mcp_test.go"
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phase 1: confirm the start and record the baseline.
2. Phase 2: move the suggestion rule, write the failing tests, add the check.
3. Stop and validate with quickstart scenarios A to R, U, and V. This closes the defect in issue #36:
   a misspelled argument can no longer produce a success for the wrong operation.

### Incremental delivery

All phases ship in one PR. Within the branch:

1. Phases 1 and 2: MVP.
2. Phase 3: the schema key, which lets a client catch a typo before calling.
3. Phase 4: docs, gates, quickstart.
4. Phase 5: code review fixes, then the PR.

---

## Notes

- `[P]` tasks touch different files and depend on no incomplete task.
- Command-line arguments must not change. `rune build ...` still takes positional values and `NAME=VALUE` overrides; T004 moves the suggestion helper without changing its behavior, and the existing `internal/cli` tests guard that.
- The `candidateNames` map-order item under "Observed, not changed" in research.md is separate work.
- Any bug a code review finds in this work gets a test first: write it, watch it fail against the bug, fix, watch it pass.
- Out of scope, tracked elsewhere: value policies (#48), structured tool results (#42).
