---

description: "Task list for spec 049: approve a destructive call inside the agent client"
---

# Tasks: Approve a Destructive Call Inside the Agent Client

**Input**: Design documents from `/specs/049-mcp-confirm-elicitation/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and FR-028 ask for them. A test is written and
seen failing before the change that makes it pass. Where a test already passes before the
change, the task says so and says what it guards.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US7)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
- "Nothing ran" is always checked from the outside: no marker file, or a fake engine whose
  run counter is zero. A result text alone is not proof.
- The exact texts are in [contracts/mcp-tools.md](contracts/mcp-tools.md) and
  [contracts/approval-request.md](contracts/approval-request.md). Tests compare whole
  strings, not fragments, unless the task says otherwise.
- A server that does not ask (`Options.AskApproval` false) must answer as before, byte for
  byte. No existing `mcpserver` or `internal/cli` test is edited to make it pass.
- A test that waits for a person uses `Options.ApprovalTimeout` in milliseconds, never a
  sleep of seconds.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `049-mcp-confirm-elicitation` and `specs/049-mcp-confirm-elicitation/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The ungranted set as a list, the library's new types, and the test helpers every
story uses. Nothing here changes behavior.

### Tests first

- [X] T002 In `mcpserver/authz_test.go`, add `TestUngranted`: table cases for `(*Server).ungranted(TaskInfo)`: a non-destructive task with no reach gives nil; a destructive ungranted task gives its name; a granted one gives nil; reach `["wipe","clean"]` keeps that order; a reach entry equal to the task's name or repeated is listed once; a granted reach entry is dropped; a destructive task with reach lists itself first. Run it and see it fail to compile.

### Implementation

- [X] T003 In `mcpserver/authz.go`, add `func (s *Server) ungranted(t TaskInfo) []string` holding the list logic now inside `refusal` (own name first, then ungranted reach, each once), and make `refusal` build its text from it. The texts `refusal` returns must not change: run `go test ./mcpserver -run 'Refusal|Ungranted'` and see every existing refusal test still pass.
- [X] T004 In `mcpserver/server.go`, add to `Options` the fields `AskApproval bool`, `ApprovalTimeout time.Duration` and `RunCommand string`, the constant `DefaultApprovalTimeout = 5 * time.Minute`, the interface `ApprovalEngine` and the structs `ApprovalRequest`, `ApprovalArg{Name string; Values []string}` and `Confirmation{Task, Message string}`, each with the doc comment of [contracts/mcpserver-api.md](contracts/mcpserver-api.md). Nothing reads them yet.
- [X] T005 [P] Create `mcpserver/approval_test.go` with the helpers the stories use: `approvalEngine`, a fake `Engine` and `ApprovalEngine` that records each `CallApproved` (name, args, lists, tasks), calls `approve` with a configurable `ApprovalRequest`, counts runs only when `approve` returned nil, and returns `approve`'s error unchanged; `person`, an `ElicitationHandler` that records every request it receives and answers from a function; `connectClient(t, srv, *mcp.ClientOptions)`, which is `connect` with client options; and `httpClient(t, addr, token, *mcp.ClientOptions)`, the same for `httpSession`.
- [X] T006 [P] Create `test/integration/approval_mcp_test.go` with the quickstart scratch Runefile as `approvalRunefile`, and the helpers: `approvalSession(t, dir, flags, clientOptions)`, which starts `runeBin mcp --color=never flags...` in `dir` through `mcp.CommandTransport` and returns the client session; `approvalHTTPSession`, which starts `runeBin serve --http --addr ADDR --token-file FILE` the way `TestFileFlagMCP_HTTP` does (`freePort`, `writeToken`, `waitForText` for the startup line) and connects a client with the given options and the bearer token; both helpers keep the server's stderr in a `syncBuffer` the test can read; `answering(fn)`, client options with an `ElicitationHandler` that appends each request's message to a mutex-guarded slice and answers with `fn`; and `callText(t, cs, tool, args)`, returning text and `isError`. Reuse `doneFiles`, `exists` and `writeRunefile`.

**Checkpoint**: the package compiles, every existing test passes, and a test can play the person.

---

## Phase 3: User Story 1 - A person approves one destructive call without leaving the client (Priority: P1) MVP

**Goal**: With a client that can show a form, a call to an ungranted `[confirm]` task asks the
person and runs once on yes.

**Independent Test**: quickstart A, B, D and K.

### Tests for User Story 1 (write first, watch them fail)

- [X] T007 [US1] In `mcpserver/approval_test.go`, add `TestApprovalSchema`: `approvalSchema()` marshals to exactly the schema of contracts/approval-request.md (type object, one property `approved` of type boolean with title `Approve this call`, description `Run this task call once.`, default false, required `["approved"]`), and two calls return maps that do not share state.
- [X] T008 [US1] In `mcpserver/approval_test.go`, add `TestApprovalMessageLayout`: `approvalMessage("apply", []string{"apply"}, ApprovalRequest{Args: [{env [prod]}], Confirmations: [{apply "Apply to prod? 3 resources change."}]})` returns the first example of contracts/approval-request.md byte for byte, and true; a task with no `Args` has no `Arguments:` block; a variadic with `Values` `["a b","c"]` is `- files: "a b" "c"` and with none is `- files: (none)`; a name with no `Confirmation` entry, and one whose `Message` is empty, gets `Run "NAME"?`; a `Confirmation` for a task not in the names is ignored; the names appear in the order given.
- [X] T009 [US1] In `mcpserver/approval_test.go`, add `TestFormSupport`: a session whose client stated no elicitation is false; `{}` true; form true; form and URL true; URL only false. Build each session with `connectClient` and `ClientOptions.Capabilities` (set `Elicitation` there to state a mode without a handler), and take the server's side of it from `srv.MCP().Sessions()`.
- [X] T010 [US1] In `mcpserver/approval_test.go`, add `TestReadAnswerApproved`: `readAnswer(&mcp.ElicitResult{Action: "accept", Content: {"approved": true}})` is `outcomeApproved`. The other rows come in T029.
- [X] T011 [US1] In `mcpserver/approval_test.go`, add `TestHandlerAsksThenRuns`: a server with `AskApproval` and an `approvalEngine` whose `apply` is destructive with parameter `env`; a client whose `person` approves. Call `apply` with `env=prod`. Check: the person got one request whose message holds `Task: apply`, `- env: "prod"` and the task's confirmation line; the request arrived while the engine's run counter was zero; `CallApproved` got `tasks == ["apply"]` and the decoded args; the engine ran once; the result is the engine's, with structured content. Then call again and check a second request arrived. A last case: the engine's result has exit code 3; the tool result is the task's failure (`[exit 3]`, `isError`), with no approval text.
- [X] T012 [US1] In `mcpserver/approval_test.go`, add `TestHandlerGrantSkipsApproval`: with `AllowDestructive: ["apply"]` and `AskApproval`, the call runs through `CallLists` or `Call` with no request to the person. And `TestHandlerNoApprovalNeeded`: a non-destructive task with `AskApproval` runs with no request (FR-003).
- [X] T013 [P] [US1] In `mcpserver/transport_test.go`, add `TestHTTPApprovedCall`: T011's first half over `StartHTTP` with `httpClient` (FR-001, both transports).
- [X] T014 [P] [US1] In `internal/cli/confirm_test.go`, add `TestConfirmFailureApproved`: an `engine` with nil `opts.Stdin`, no grants and `approved: {"clean": true}` returns nil from `confirmFailure` for `clean` and a non-nil failure for another `[confirm]` task (FR-014).
- [X] T015 [P] [US1] Create `internal/cli/mcp_approval_test.go` with `TestCallApprovedRunsConfirmBody`: an adapter loaded from a Runefile with `[confirm("Apply to prod?")] apply env region="eu":` whose body writes a marker; `CallApproved(ctx, "apply", {"env":"prod"}, nil, ["apply"], approve)` where `approve` records its `ApprovalRequest` and returns nil. Check: the marker did not exist when `approve` was called; the request's `Args` are `[{env [prod]} {region [eu]}]` in declaration order; `Confirmations` is `[{apply "Apply to prod?"}]`; the marker exists afterwards; exit code 0. A second case: `[confirm]` with no text gives an empty `Message`. Build the adapter with the package's existing helper (`adapterFor` in `mcp_test.go`), which sets no mask set: T059 has to keep that safe.
- [X] T016 [US1] In `internal/cli/mcp_approval_test.go`, add `TestCallApprovedStopsOnError`: `approve` returns a sentinel error; `CallApproved` returns that same error value (`errors.Is` and equal text), an empty result, and no marker exists. And `TestCallApprovedBindsFirst`: a missing required argument returns the bind error and `approve` is never called.
- [X] T017 [US1] In `internal/cli/mcp_approval_test.go`, add `TestApprovalRequest` for `(*mcpAdapter).approvalRequest` on its own: declaration order; a scalar gives one value; a variadic gives its elements from `params.Lists`, and none gives an empty `Values`; a task name not in `a.tasks` gets no `Confirmation` entry.
- [X] T018 [P] [US1] In `internal/cli/serve_test.go`, add `TestServeMCPOptions`: `serveMCPOptions(...)` (extracted in T024) returns `AskApproval` true, `RunCommand` `"rune"`, `AllowDestructiveFlag` `"--allow-destructive"`, and passes its arguments through to the other fields.
- [X] T019 [US1] In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_ApprovedCallRuns` (quickstart A on stdio and on HTTP: asked before any marker, message lines, `apply.done` holds `prod-eu`, `[exit 0]`; no restart and no terminal command were needed, SC-004), `TestApprovalMCP_AskedEveryCall` (B), `TestApprovalMCP_DefaultQuestion` (D) and `TestApprovalMCP_GrantedNotAsked` (K).
- [X] T020 [US1] Run `go test ./mcpserver ./internal/cli ./test/integration -run 'Approv|FormSupport|ReadAnswer|ConfirmFailureApproved|CallApproved|ServeMCPOptions'` in Docker and record that each new test fails, and why (undefined symbol, or refused instead of asked).

### Implementation for User Story 1

- [X] T021 [US1] Create `mcpserver/approval.go` with: the type `outcome` and its constants `outcomeApproved`, `outcomeNotGiven`, `outcomeDeclined`, `outcomeDismissed`, `outcomeTimedOut`, `outcomeNoAnswer`, `outcomeCancelled`, `outcomeChanged`; `approvalSchema() map[string]any`; `formSupport(*mcp.ServerSession) bool` per research R2; `approvalMessage(called string, names []string, req ApprovalRequest) (string, bool)` with the layout of contracts/approval-request.md, each value written with `strconv.Quote`, and the default question, returning true for now (T058 adds the limits); `readAnswer(*mcp.ElicitResult) outcome` for the approved row, everything else `outcomeNoAnswer` for now; and `func (s *Server) approver(session *mcp.ServerSession, snap *snapshot, t TaskInfo, names []string) func(context.Context, ApprovalRequest) error`, which sends `session.Elicit` with mode `form`, the message and the schema, and returns nil only for `outcomeApproved`. Any other outcome returns an error for now with the text `task "NAME" was not approved`; T034 gives each its text. `snap` and `t` are in the signature from the start: T045 uses `snap`, T058 uses `t`.
- [X] T022 [US1] In `mcpserver/handler.go`, after loading the task: compute `needs := s.ungranted(t)`. When `needs` is not empty and `s.opts.AskApproval` and the snapshot's engine is an `ApprovalEngine` and `formSupport(req.Session)`: skip the refusal, validate the arguments as today, then call `CallApproved(ctx, taskName, args, lists, needs, s.approver(...))` in place of `CallLists`/`Call`. In every other case with `needs` not empty, return `errorResult(s.refusal(t))` at the place the refusal is today. Update the handler's doc comment.
- [X] T023 [US1] In `internal/cli/run.go`, add the field `approved map[string]bool` to `engine` with a comment saying it is set for one MCP call after a person approved it; in `internal/cli/confirm.go`, make `confirmFailure` return nil when `e.approved[task.Name]`, after the grants check.
- [X] T024 [US1] In `internal/cli/serve.go`, move the `mcpserver.Options` literal that `ServeMCP` builds into `func serveMCPOptions(version string, granted, allow, deny []string, outputLimit int, instructions string, reloadable bool) mcpserver.Options`, and set `AskApproval: true` and `RunCommand: "rune"` there. `ServeMCP` calls it.
- [X] T025 [US1] In `internal/cli/mcp.go`, add `func (a *mcpAdapter) CallApproved(...)` and the assertion `var _ mcpserver.ApprovalEngine = (*mcpAdapter)(nil)`. Give `call` an extra parameter carrying the task names and the `approve` function, nil from `CallLists` and from its other caller, `gatherContext` in `internal/cli/contextprep.go`. After `bindNamedParams` succeeds and before `scheduler.Run`: when it is set, build the request with `func (a *mcpAdapter) approvalRequest(t *ast.Task, params eval.Args, tasks []string) mcpserver.ApprovalRequest` (every declared parameter in order; a variadic's elements from `params.Lists`, any other parameter's one value from `params.Values`; each named task's `[confirm]` text from `a.tasks`), call `approve`, return its error unchanged, and on nil set `eng.approved`.
- [X] T026 [US1] Run the tests of T007 to T019 and the whole of `./mcpserver`, `./internal/cli` and `./test/integration` in Docker. All pass. Existing tests that call a `[confirm]` task without a grant still get today's text, because their client states no elicitation.

**Checkpoint**: quickstart A, B, D and K pass on the built binary.

---

## Phase 4: What the recorded clients do when asked (part of User Story 7, done early)

**Purpose**: Claude Code run as `claude -p` and Codex run as `codex exec` both state form
elicitation, so a headless run of either now takes the ask path. What they answer decides whether
the outcome texts of Phase 5 need anything more. This is evidence for the compatibility table
too.

- [X] T027 [US7] Build this branch's `rune` and the recorder as `test/integration/testdata/clients/README.md` says. In a fresh copy of `test/integration/testdata/clients/scenario/`, with the server run with no `--allow-destructive`, record one session each of Claude Code (`claude -p`, with `--allowedTools mcp__rune`) and Codex CLI (`codex exec`, with `default_tools_approval_mode="approve"` so the call reaches Rune), with a prompt that asks only for a call to `clean` and a report of the result word for word. Save each as `elicitation.jsonl` with its `elicitation.reply.txt` under `claude-code/` and `codex/`, with the edits the README lists (machine paths, identifiers). If a client cannot be run (not signed in, no network), do not invent a recording: say so in the README and leave the table cell "not tested".
- [X] T028 [US7] Read the two recordings: did Rune send `elicitation/create`; what did the client answer, and after how long; did `clean` run; what text did the model get. Write the findings into `specs/049-mcp-confirm-elicitation/research.md` as "R15. What the recorded clients answer". If either client's answer falls outside the outcomes of data-model.md, or leaves the model with no way forward, change the contract texts before Phase 5 and say so in R15.

---

## Phase 5: User Story 2 - Anything but an explicit yes runs nothing (Priority: P1)

**Goal**: Every reply that is not `accept` with `approved: true` runs nothing and says which case
it was, and where nobody said no, how to run the task another way.

**Independent Test**: quickstart F, L, M, N, O and Y.

### Tests for User Story 2 (write first, watch them fail)

- [X] T029 [US2] In `mcpserver/approval_test.go`, add `TestReadAnswer`: one case per row of research R4 that has a reply: accept with `approved: false` is `outcomeNotGiven`; `decline` is `outcomeDeclined`; `cancel` is `outcomeDismissed`; accept with nil content, with no `approved` key, with `"true"`, with `1`, and with `nil` is `outcomeNoAnswer`; an action `sure` and an empty action are `outcomeNoAnswer`; extra content keys beside `approved: true` are still `outcomeApproved`; a nil result is `outcomeNoAnswer`.
- [X] T030 [US2] In `mcpserver/approval_test.go`, add `TestOutcomeText` and `TestRemedy`: for each outcome, the text for task `apply` with names `["apply"]` equals the row of "Outcome texts" in contracts/mcp-tools.md, the timeout one with the configured duration (`50ms`, and `5m0s` for the default); for `release` with names `["clean","wipe"]` the remedy reads `add clean,wipe to --allow-destructive` and `run "rune release" themselves`; with no `AllowDestructiveFlag` only the command part; with no `RunCommand` only the flag part; with neither, no sentence. Call cancelled and Runefile changed have no remedy; every other outcome has it, a decline included (research R15).
- [X] T031 [US2] In `mcpserver/approval_test.go`, add `TestHandlerNotApproved`: table over the person's answers (decline; cancel; accept false; accept with no content; action `sure`; a handler that returns an error). For each: the engine's run counter stays zero, `isError` is true, the text is the contract's, the result has one text block and no structured content, and the person got exactly one request (FR-019: no second ask).
- [X] T032 [US2] In `mcpserver/approval_test.go`, add `TestHandlerApprovalTimeout` (quickstart Y): `ApprovalTimeout` 50 ms and a person that answers approve after 500 ms; the result is the timeout text with `50ms`, the engine never runs, also not after the late answer has been sent (wait for the person's handler to return, then check the counter again). Add `TestHandlerApprovalAtDeadline`: an `approver` whose `Elicit` result is approved but whose wait context is already done returns `outcomeTimedOut`, and one whose call context is done returns `outcomeCancelled` (test the check through a small function, `settled(callCtx, waitCtx context.Context, o outcome) outcome`, so no timing is involved). Add `TestHandlerCallCancelled`: cancel the `CallTool` context while the person is deciding; the engine never runs and the person's handler sees its context end. Add `TestHandlerClientDisconnects`: the person's handler signals receipt and waits on its context; the test goroutine then closes the client session; the engine never runs. Add `TestApprovalTimeoutDefault`: `(*Server).approvalTimeout()` is `DefaultApprovalTimeout` for zero and for a negative value, and the configured value otherwise.
- [X] T033 [P] [US2] In `mcpserver/transport_test.go`, add `TestHTTPNotApproved`: over `StartHTTP`, a declined request, a timeout (`ApprovalTimeout` 50 ms) and a cancelled call each leave the engine's run counter at zero and, for the first two, return the contract text (FR-028 on HTTP). In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_NotApprovedRunsNothing`: quickstart F (on stdio and on HTTP), L, M, N and O against the binary with `release`; each leaves no marker file, `build.done` included, and gives its contract text.

### Implementation for User Story 2

- [X] T034 [US2] In `mcpserver/approval.go`: add `func (s *Server) remedy(called string, names []string) string` and `func (s *Server) outcomeText(o outcome, called string, names []string) string`; complete `readAnswer`; add `func (s *Server) approvalTimeout() time.Duration`; add `settled`; in `approver`, wrap the context with `context.WithTimeout(ctx, s.approvalTimeout())`, and when `Elicit` returns an error choose the outcome from the contexts (the call's context done: `outcomeCancelled`; the wait's deadline passed: `outcomeTimedOut`; otherwise `outcomeNoAnswer`), and pass an approved reply through `settled` before returning nil. Never put the client's error text into the result (FR-020). `approver` returns `errors.New(s.outcomeText(...))` for every outcome but approved.
- [X] T035 [US2] Run T029 to T033 in Docker. All pass.

**Checkpoint**: no reply but an explicit yes can run a task.

---

## Phase 6: User Story 3 - One request covers everything destructive the call can run (Priority: P1)

**Goal**: The request names every ungranted destructive task the call can run, nothing runs
before the answer, and the approval covers exactly those names.

**Independent Test**: quickstart E, G, H, I and J.

### Tests for User Story 3 (write first; say which already pass and what they guard)

- [X] T036 [US3] In `mcpserver/approval_test.go`, add `TestHandlerAsksForReach`: tasks `release` (reach `["clean"]`), `reset` (destructive, reach `["wipe"]`), `full` (reach `["clean","wipe"]`). Check `CallApproved`'s `tasks` and the request's `Destructive tasks it can run:` lines: `release` names `clean`; `reset` names `reset` then `wipe`; `full` with `AllowDestructive: ["clean"]` names only `wipe`. The message's `Task:` line is the called task in each.
- [X] T037 [P] [US3] In `internal/cli/mcp_approval_test.go`, add `TestCallApprovedCoversOnlyNamed`: a Runefile with `release: build clean`, `clean` and `wipe` both `[confirm]`, `both: clean wipe`. `CallApproved("release", ..., ["clean"], approve)`: at the time `approve` is called `build`'s marker does not exist; afterwards `build`, `clean` and `release` ran. `CallApproved("both", ..., ["clean"], approve)`: `clean` ran, `wipe` did not, and the exit code is not zero (the engine's own gate declined `wipe`). `CallApproved("clean", ..., nil, approve)` with an empty list: `clean` does not run. Add `TestCallApprovedHooksAndPrivate`: `publish: && clean` asks before `publish`'s marker exists and runs both; `deploy: || wipe` with `["wipe"]` approved runs `deploy` and not `wipe` when `deploy` succeeds, and runs `wipe` as the failure hook when `deploy` fails; `ship: _scrub` with `["_scrub"]` runs the private task; a `[parallel]` task whose two dependencies are both named runs both. Each request's `Confirmations` holds the named task's message. Add `TestApprovedDoesNotOutliveCall`: after an approved `CallApproved("clean", ...)`, a plain `CallLists("clean", ...)` on the same adapter does not run `clean` (FR-012), and `a.grants` is unchanged.
- [X] T038 [US3] In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_CoversReach`: quickstart E (also on HTTP), G, H (also on HTTP), I, J and S3 (`--deny clean`: `release` is asked about `clean` and runs it on approval).

### Implementation for User Story 3

- [X] T039 [US3] Run T036 to T038. Fix whatever fails in `mcpserver/handler.go`, `mcpserver/approval.go` or `internal/cli/mcp.go`. If all pass with the code of Phase 3, change nothing and note in the PR description that these tests guard FR-002, FR-012 and FR-014.

**Checkpoint**: a dependency or hook is never run on an approval that did not name it.

---

## Phase 7: User Story 5 - An approval is good for one call and nothing else (Priority: P1)

**Goal**: Concurrent calls, other sessions, repeated calls and a reload cannot use an approval
that was not given for them.

**Independent Test**: quickstart C, S, T, U, W, W2 and W3.

### Tests for User Story 5 (write first, watch them fail)

- [X] T040 [US5] In `mcpserver/approval_test.go`, add `TestHandlerConcurrentApprovals`: 50 pairs of concurrent calls to a destructive task with a parameter `id`; the person approves a request whose message holds an even `id` and declines the others. Exactly the 50 approved ids ran, each once (SC-003). Run it with `-race`.
- [X] T041 [US5] In `mcpserver/approval_test.go`, add `TestHandlerReloadInvalidatesApproval`: the person's handler calls `srv.Replace(engine, 0)` before answering approve; the result is the "Runefile changed" text and the engine never ran. A second case answers approve without a `Replace` and runs. A third calls `Replace` after the run started and checks the run finishes (a call in progress is not affected).
- [X] T042 [US5] In `mcpserver/approval_test.go`, add `TestHandlerApprovalArgumentIsUnknown`: calling the destructive task with an extra argument `approved: true` gives the unknown-argument refusal and no request (FR-009); `TestHandlerInvalidValueNotAsked`: a value that breaks the parameter's enum, and a JSON object as a value, give the invalid-arguments error and no request (FR-004); and `TestHandlerRepeatedCallAsksAgain`: the same call sent twice with the same arguments gets two requests, and when the person approves the first and declines the second the engine ran once (FR-024).
- [X] T043 [P] [US5] In `mcpserver/transport_test.go`, add `TestHTTPApprovalStaysOnSession`: two HTTP sessions with their own `person`; each calls the destructive task with a different argument; each person received exactly one request, the one showing its own argument (FR-015).
- [X] T044 [US5] In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_OneCallOnly`: quickstart C, S, T, U, W, W2 and W3. For U, `note.done` holds exactly `a`. For W, the person's handler appends a task to the Runefile, waits for `reloaded Runefile` on the server's stderr, then approves. For W2 it saves a Runefile with a syntax error, waits for `reload failed`, then approves, and the call runs. For W3 the server has `--no-reload`, the handler appends a task and approves, and the call runs.

### Implementation for User Story 5

- [X] T045 [US5] In `mcpserver/approval.go`, make `approver` use its `snap`: after an approved answer that passed `settled`, return `outcomeChanged` when `s.state.Load()` is not that snapshot.
- [X] T046 [US5] Run T040 to T044 in Docker, the `mcpserver` ones also with `-race`. All pass.

**Checkpoint**: an approval cannot be spent twice or on another call.

---

## Phase 8: User Story 4 - A client that cannot ask gets a refusal it can act on (Priority: P2)

**Goal**: No request goes to a client without form support, and its refusal names the command
to run and why Rune could not ask. Servers that do not ask are unchanged.

**Independent Test**: quickstart P, Q, R, S2 and X.

### Tests for User Story 4 (write first, watch them fail)

- [X] T047 [US4] In `mcpserver/approval_test.go`, add `TestCannotAsk`: with `RunCommand: "rune"` and `AllowDestructiveFlag: "--allow-destructive"`, `cannotAsk("release", reasonNoForm)` is exactly `. Or the operator can run "rune release" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.`, so that `refusal(t) + cannotAsk(...)` is the text of contracts/mcp-tools.md; with an empty `AllowDestructiveFlag` the middle sentence starts `Run`; with an empty `RunCommand` it is absent; `reasonTooLarge` gives `the approval request would be too large to show`.
- [X] T048 [US4] In `mcpserver/approval_test.go`, add `TestHandlerClientCannotBeAsked`: with `AskApproval`, `RunCommand: "rune"` and `AllowDestructiveFlag: "--allow-destructive"`, a client with no elicitation and one with URL only each call `clean`, `release` (reach `clean`), `reset` (destructive, reach `wipe`) and `tools__clean`: each text equals its row in contracts/mcp-tools.md, the engine never ran, and for the URL-only client the person's handler received nothing. A client that states `{}` is asked (quickstart R). A call with a bogus argument from a client that cannot be asked still gets the refusal, not the argument error (spec 034 FR-010). Add `TestHandlerDoesNotAskByDefault`: with zero `Options` and an `approvalEngine`, and with `AskApproval` and a plain `fakeEngine` that is not an `ApprovalEngine`, a form-capable client calling a destructive task gets exactly `refusal(t)` and no request (FR-023). Add `TestHandlerHiddenTaskNeverAsked`: with `AskApproval` and `DenyList: ["apply"]`, a form-capable client calling `apply` gets the unknown-tool error and no request (FR-005, quickstart S2).
- [X] T049 [P] [US4] In `internal/cli/mcp_test.go`, add a test that `agentMCPOptions` leaves `AskApproval` false and `RunCommand` empty, and that a form-capable client calling `clean` on a server built with those options and `newAgentAdapter` gets `task "clean" is destructive and is not allowed in this session` and no request (quickstart X, FR-022).
- [X] T050 [US4] In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_ClientCannotBeAsked`: quickstart P (also on HTTP), Q, R and S2.

### Implementation for User Story 4

- [X] T051 [US4] In `mcpserver/approval.go`, add the constants `reasonNoForm` and `reasonTooLarge` and `func (s *Server) cannotAsk(called, reason string) string`; in `mcpserver/handler.go`, when `needs` is not empty, the server asks, the engine is an `ApprovalEngine`, and `formSupport` is false, return `errorResult(s.refusal(t) + s.cannotAsk(t.Name, reasonNoForm))` at the place of today's refusal.
- [X] T052 [US4] The existing binary-level tests pin today's text on `rune mcp`, which this story lengthens. In `test/integration`, add `func cannotAskSuffix(task string) string` next to `refusedText` in `destructive_mcp_test.go`, and append it to the expected text in every test there, and in `cmd/`, that pins a destructive refusal from the `rune mcp` or `rune serve` binary: find them with `grep -rn "restart the server" test/integration cmd`. Each expected string gains the suffix and nothing else. The tests in `internal/cli` and `mcpserver` that hold the same text build servers that do not ask: they must pass unchanged.
- [X] T053 [US4] Run the whole of `./mcpserver`, `./internal/cli`, `./cmd/...` and `./test/integration` in Docker. All pass.

**Checkpoint**: clients without form support lose nothing and are told the way out.

---

## Phase 9: User Story 6 - The request shows no secret and cannot be forged by an argument (Priority: P2)

**Goal**: Values in the request are masked and escaped and shown whole; a request that does not
fit is not sent. Depends on Phase 8 for `cannotAsk`.

**Independent Test**: quickstart V and V2, plus the unit cases below.

### Tests for User Story 6 (write first, watch them fail)

- [X] T054 [US6] In `mcpserver/approval_test.go`, add `TestApprovalMessageEscapes`: an argument value with a line feed, a carriage return, a tab, ESC, a NUL and U+202E (written in the test source as escapes, never as raw characters) appears on one line as escapes; a value with `"` and a backslash is escaped; an empty value is `""`; a value of 5,000 characters is shown whole; a `[confirm]` message with a line break is escaped onto one line; one of 501 characters is cut to 500 with ` [truncated]`, one of exactly 500 is not, and one of multi-byte characters is cut on a character boundary; a task name and a parameter name with a line break are escaped. Add `TestEscapeLine` and `TestCutRunes` for the two helpers on their own.
- [X] T055 [US6] In `mcpserver/approval_test.go`, add `TestApprovalMessageTooLarge`: a value that takes the message past 8192 bytes, and enough names and messages to do so, make `approvalMessage` return false; a message of exactly 8192 bytes returns true. And `TestHandlerOversizeRequestRefused`: a call with a 10,000-character argument is not asked about, the engine does not run, and the text is the refusal plus `cannotAsk` with `reasonTooLarge`.
- [X] T056 [P] [US6] In `internal/cli/mcp_approval_test.go`, add `TestApprovalRequestMasksSecrets`: an adapter whose `baseEnv` has `API_TOKEN=s3cr3t-value` and whose `maskSet` is `deriveMaskSet(...)` of it; `CallApproved` with an argument equal to that value, with a variadic element equal to it, and with a `[confirm("...")]` message that contains it. The `ApprovalRequest` given to `approve` holds `***` and never the value, in `Args` and in `Confirmations`. A second case: an adapter with a nil `maskSet` does not panic and passes values through.
- [X] T057 [US6] In `test/integration/approval_mcp_test.go`, add `TestApprovalMCP_SecretNeverShown` (quickstart V): the server's environment holds a secret; the call passes it as `text`; neither the request message nor any result text contains it. `TestApprovalMCP_ForgedLinesStayInValue`: an argument of `ok`, two line feeds and `Task: build` appears in the request as one line holding the escapes. `TestApprovalMCP_OversizeNotAsked` (V2).

### Implementation for User Story 6

- [X] T058 [US6] In `mcpserver/approval.go`: add `escapeLine(s string) string` (every rune for which `strconv.IsPrint` is false written as its Go escape), `cutRunes(s string, n int) (string, bool)`, the constants `maxApprovalMessageRunes = 500` and `maxApprovalBytes = 8192`; make `approvalMessage` escape names, cut and escape each `[confirm]` message, and return false when the whole message is over `maxApprovalBytes`; in `approver`, return an error whose text is `s.refusal(t) + s.cannotAsk(t.Name, reasonTooLarge)` for a message that does not fit.
- [X] T059 [US6] In `internal/cli/mcp.go`, make `approvalRequest` pass every argument value and every `[confirm]` message through the adapter's mask set, guarded as `maskOptions` guards it: `(*mask.Set).MaskString` is not safe on a nil set, so skip masking when `a.maskSet.Empty()`.
- [X] T060 [US6] Run T054 to T057 and the earlier approval tests in Docker. All pass.

**Checkpoint**: the request is safe to show and safe to log, and nothing in it is shortened that
the run would use.

---

## Phase 10: User Story 7 - The documentation says who can approve and what happens when they cannot (Priority: P2)

**Goal**: The guide, the compatibility table and the spec 031 contract describe the request and
its fallback, with evidence for every client cell.

**Independent Test**: read the changed sections; `test/docs` and `TestClientRecordings` pass.

- [X] T061 [US7] In `docs/mcp.md`, under "Security model": change the bullet "Destructive tasks are refused by default" to say a call that can run an ungranted `[confirm]` task is refused unless the person approves that call in the client, and that nothing runs before the answer; make its example text the one a client that cannot be asked gets now; add a bullet that Rune trusts the client to show the request and report the person's answer, that a client which answers by itself approves for the person, that nothing limits how often an agent can make the client ask, and that to rule a task out the operator denies it and every exposed task that can run it (a later edit to the Runefile can add a new path to it); keep the grant bullets and say a granted task is not asked about; correct the `--deny` bullet that says a `[confirm]` task the operator has not named "is refused wherever a run reaches it".
- [X] T062 [US7] In `docs/mcp.md`, add the section "Approving a destructive call in the client" before "Agent task bodies": when Rune asks; an example request (from contracts/approval-request.md); the answer that counts; what an approval covers (one call, the named tasks, once, not after a reload); a table of the outcomes and their texts; the five-minute wait; the fallback text for a client that cannot be asked; that a request too large to show is refused, and that argument values are never shortened; that the `agent` task endpoint never asks; and the supported MCP revisions (2025-06-18 and 2025-11-25, as a request from the server during the call; the retried-call flow of 2026-07-28 is not supported). Add the new outcome texts to the list of calls with no structured content under "Structured results". Under "Starting the server", where `--allow-destructive` is introduced, add one sentence with a link to the section.
- [X] T063 [P] [US7] Change the sentences that say an ungranted `[confirm]` task is always refused over MCP in `docs/runefile.md` (the `[confirm]` paragraph), `docs/use-cases/mcp-agents.md`, `docs/user-guide/README.md`, `docs/examples/agent-driven/README.md`, `docs/examples/agent-driven/Runefile` (its header comment), `docs/design/rune-only-agent-mode.md`, `SECURITY.md` and `README.md`: each says "refused unless the operator granted it or the person approves the call in the client", with a link to the new section where the file has links. Change nothing else in those files. In `cmd/rune/serve.go`, change the help text that states the old rule the same way, and its test in `cmd/rune/serve_test.go`.
- [X] T064 [P] [US7] In `specs/031-mcp-destructive-optin/contracts/mcp-tools.md`, add a section "Interactive approval (spec 049)": with no grant, a client that states form elicitation is asked and the call runs once on approval; a client that does not gets the refusal with the two added sentences; link to `specs/049-mcp-confirm-elicitation/contracts/`. Mark the table rows whose result this changes.
- [X] T065 [US7] In `.specify/memory/constitution.md`, amend Principle VII as research R14 words it, bump the version to 1.4.0 with today's date as "Last Amended", and replace the Sync Impact Report with one for this amendment: 1.3.0 to 1.4.0, MINOR, prompted by spec 049 (issue #47), proposed with the feature and approved by the maintainer's merge of the PR, templates checked with no structural change needed.
- [X] T066 [US7] In `test/integration/clients_fixture_test.go`, add cases: each existing `session.jsonl` states `elicitation` with `form` and `url` in its `initialize`; each `elicitation.jsonl` of T027 shows what the table will say (Rune sent `elicitation/create` with `mode` `form`; the client's reply; `clean` did not run). Update `test/integration/testdata/clients/README.md` with the new recordings, their commands, prompt and what each reply reports.
- [X] T067 [US7] For Cursor, VS Code (Copilot agent mode) and Zed, write down from the vendor's documentation, forum or source whether the client supports MCP elicitation and how it shows a form request, keeping the page each statement comes from. Where nothing is found, the cell is "Unknown".
- [X] T068 [US7] In `docs/mcp.md` "Client compatibility": add the rows "Elicitation modes stated", "A `[confirm]` call with no grant" and "When nobody answers" to the table, every cell with its evidence word; give the version each new row was recorded at where it differs from the version row; add a paragraph "Approval in the client" to each client section and a bullet to "What this means for a Runefile"; say that Claude Code can be configured to answer requests by itself (its `Elicitation` hook), as an example of the trust paragraph. Update the "as of" date. Run `go test ./test/docs ./test/integration -run 'Docs|ClientRecordings'` in Docker, or the packages whole if the pattern matches nothing. All pass, links included.

**Checkpoint**: a reader can find out, per client, whether a destructive call can be approved there.

---

## Phase 11: Polish and Cross-Cutting Concerns

- [X] T069 Run the full suite: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`. All pass.
- [X] T070 Run the race suite: `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/... ./internal/cli/... ./test/integration/...`. All pass, and `goleak` in `mcpserver` reports no goroutine left by a timed-out or cancelled request.
- [X] T071 Run the linter the way CI does (the `golangci/golangci-lint` image at the version pinned in `.github/workflows/ci.yml`). Zero issues, including gofumpt and goimports.
- [X] T072 Measure coverage of `./mcpserver` and `./internal/cli` with `-coverprofile`; every function added or changed by this feature is covered, and the packages stay at or above their figure on `main`. Add unit tests for any branch left uncovered.
- [X] T073 Check each quickstart row A to Y, with S2, S3, V2, W2 and W3, maps to a passing test, by name. Fix the quickstart's file references if a test landed elsewhere.
- [X] T074 Check that no line this feature added holds an em dash: `git diff main... | grep -P '^\+.*\x{2014}'` prints nothing (run it after the files are staged or committed, and on the untracked files with `grep -rnP '\x{2014}'`).
- [X] T075 Commit and open the PR with a title of type `feat(mcp)!` whose subject states the change (research R13), and a description that lists the constitution amendment as its own item.

---

## Dependencies and Execution Order

### Phase dependencies

- Phase 2 blocks everything.
- Phase 3 (US1) blocks Phases 4 to 9: they all extend the ask path it builds.
- Phase 4 (the recordings) comes before Phase 5, because it can change the outcome texts.
- Phases 5 (US2), 6 (US3), 7 (US5) and 8 (US4) each depend on Phase 3. Phase 7 uses `settled`
  from Phase 5. They touch the same two files (`mcpserver/approval.go`,
  `mcpserver/approval_test.go`), so they are done in the order written, not at once.
- Phase 9 (US6) depends on Phase 8 for `cannotAsk` and its reasons.
- Phase 10 (US7) depends on the texts being final: after Phases 5, 8 and 9.
- Phase 11 is last.

### Within each story

- Tests before implementation, seen failing (or, where the task says so, seen passing as a guard).
- `mcpserver` before `internal/cli` before `test/integration`.

### Parallel opportunities

- T005 and T006 (different files).
- Within a story, the `[P]` test tasks, which sit in a different file from the
  `mcpserver/approval_test.go` tasks beside them: for US1, T013, T014, T015 and T018.
- T063 and T064 beside T061 and T062.

## Parallel Example: User Story 1

```text
T013 mcpserver/transport_test.go        TestHTTPApprovedCall
T014 internal/cli/confirm_test.go       TestConfirmFailureApproved
T015 internal/cli/mcp_approval_test.go  TestCallApprovedRunsConfirmBody
T018 internal/cli/serve_test.go         TestServeMCPOptions
```

## Implementation Strategy

### MVP (User Story 1)

Phases 1 to 3 give a working approval: a person at a form-capable client can approve one call.
It is not safe to ship alone: the non-approval texts, the reload check and the escaping come in
US2, US5 and US6.

### Incremental delivery

1. Phases 2 and 3: the ask path, approved case.
2. Phase 4: what the two recorded clients answer.
3. Phase 5: every other answer.
4. Phases 6 and 7: scope and lifetime of an approval.
5. Phases 8 and 9: clients that cannot be asked; what the request may show.
6. Phase 10: documents, recordings in the table, the constitution.
7. Phase 11: gates, commit, PR.

One PR carries all of it: the feature is not safe in parts.

## Notes

- FR to task map: FR-001 T011, T013, T019, T022; FR-002 T002, T003, T036; FR-003 T012; FR-004
  T016, T022, T042, T044 (S); FR-005 T038 (I, S3), T048 (S2); FR-006 T009, T048; FR-007 T008,
  T015, T017, T025; FR-008 T007, T010, T029; FR-009 T042; FR-010 T054, T056, T058, T059; FR-011
  T055, T057 (V2); FR-012 T037, T042; FR-013 T041, T045, T055; FR-014 T014, T037; FR-015 T040,
  T043; FR-016 T031, T032, T033; FR-017 T030; FR-018 T032, T034; FR-019 T031; FR-020 T034, T056,
  T057; FR-021 T047, T048, T051, T052; FR-022 T049; FR-023 T048; FR-024 T042, T062; FR-025 T061,
  T062; FR-026 T027, T066 to T068; FR-027 T064; FR-028 T015, T019, T033, T037, T038.
- SC to task map: SC-001 T019, T038; SC-002 T033, T050; SC-003 T040; SC-004 T019; SC-005 T056,
  T057; SC-006 T012, T048, T053; SC-007 T066, T068.
- The edge case "a task unavailable on this host" needs no new test: the request names what
  `ungranted` returns, which reads `TaskInfo.DestructiveReach`, and spec 034's tests hold that
  list to the host OS.
