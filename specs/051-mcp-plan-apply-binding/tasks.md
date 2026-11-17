---

description: "Task list for spec 051: bind approval and apply to the exact reviewed plan"
---

# Tasks: Bind Approval and Apply to the Exact Reviewed Plan

**Input**: Design documents from `/specs/051-mcp-plan-apply-binding/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and FR-034 ask for them. A test is written and
seen failing before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US9)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
- "Nothing ran" is always checked from the outside: no marker file, and a client that counts
  the approval requests it received.
- The exact texts are in [contracts/mcp-tools.md](contracts/mcp-tools.md) and
  [contracts/plan-attributes.md](contracts/plan-attributes.md). Tests compare whole strings
  unless the task says otherwise.
- A Runefile with neither attribute must give the same tools and results as before, byte for
  byte, and no `.rune/plans`. No existing test is edited to make it pass.
- Goldens are regenerated with `-update`, never edited by hand.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `051-mcp-plan-apply-binding` and `specs/051-mcp-plan-apply-binding/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: `[plan]` and `[applies]` become known attributes in the language surface (FR-001,
FR-004, R1, R2, R4), the store package exists (R5, R6, R12, R15), and the server library has
its new fields (R11). No run changes yet.

### Tests first

- [X] T002 [P] Create `internal/parser/plan_binding_test.go`. `TestParsePlan`: `[plan(file="tfplan", summary="tfplan.txt")]` gives one attribute of kind `ast.AttrPlan` whose `Allow` holds the two pairs in source order with name and value spans; all four arguments in another order keep that order; `[applies("plan")]` gives kind `ast.AttrApplies` with `Str == "plan"`; `[confirm, applies("plan")]` gives both. `TestParsePlanErrors`: each of `[plan]`, `[plan()]`, `[plan(file)]`, `[plan(file=tfplan)]`, `[plan(file="a",)]`, `[applies]`, `[applies()]`, `[applies(plan)]` gives an error diagnostic with code `RUNE1001` or, for a single-string attribute without its string, the invalid-attribute code, and none whose message contains `unknown attribute`. Add the seed `"[plan(file=\"p\", summary=\"s\")]\nplan:\n    echo hi\n[applies(\"plan\")]\napply:\n    echo $RUNE_PLAN_FILE\n"` to `FuzzParser` in `internal/parser/fuzz_test.go`. Run and see it fail to compile.
- [X] T003 [P] Create `internal/ast/plan_binding_test.go`. `TestTaskPlanSpec`: no attribute gives nil; `[plan(file="a", summary="b", tool="tofu", expires="30m")]` gives `PlanSpec{File: "a", Summary: "b", Tool: "tofu", Expires: "30m"}`. `TestTaskAppliesPlan`: "" without the attribute, the name with it. `TestPlanExpiry`: "" gives one hour; "30m" gives 30 minutes; "59s", "169h", "soon" and "-1h" give an error. `TestPlanPath`: `ast.PlanPathError` accepts `tfplan`, `out/tfplan`, `./tfplan` and rejects "", `/tmp/x`, `\tmp\x`, `C:/x`, `out\x`, `../x`, `a/../x`. `TestDumpPlan`: the dump holds `    Attr plan(file="a", summary="b")` and `    Attr applies("plan")`. `TestKnownAttributesHasPlan`: `KnownAttributes` holds `plan` and `applies`. Run and see it fail to compile.
- [X] T004 [P] In `internal/formatter/formatter_test.go`, add `TestFormatPlan`: `[plan( file = "tfplan" ,summary="tfplan.txt" )]` formats to `[plan(file="tfplan", summary="tfplan.txt")]`, `[applies( "plan" )]` to `[applies("plan")]`, and formatting twice gives the same text. Create `testdata/fmt/plan-apply.rune` with a plan task and an apply task written with irregular spacing; its golden is generated in T011. In `internal/language`, add a test that completion of an attribute name offers `plan` and `applies` and that hover on each gives its registry documentation, in the file where `agent-allow` is tested. Run and see them fail.
- [X] T005 [P] In `internal/config/compose_test.go`, add `TestComposeNamespacesApplies`: a module `infra` whose file has `[plan(...)] plan:` and `[applies("plan")] apply:` composes to tasks `infra::plan` and `infra::apply` with `AppliesPlan() == "infra::plan"`. Run and see it fail.
- [X] T006 [P] Create `internal/planstore/store_test.go`, `attempt_test.go`, `journal_test.go` and `safefile_test.go` for [contracts/plan-store.md](contracts/plan-store.md), each test in a `t.TempDir()` with `Store.Now` set to a test clock. Cover: `Save` stores the source bytes unchanged, returns the SHA-256 and size of artifact and summary, writes `record.json` without any byte sequence of the artifact or summary, creates `.gitignore` with `*`, and on POSIX leaves directories `0700` and no file readable by group or others; `Save` refuses a missing source, a directory, a symbolic link (POSIX) and a source over the limit with a `*SourceError` whose `Detail` is the contract's, and leaves no record directory behind; a second `Save` with the same key returns the first in `replaced`, marks it `replaced` and removes its artifact and summary; a `Save` with another key replaces nothing; 20 concurrent `Save` calls for one key with distinct clock values: `Latest` returns the one with the greatest `created`, and after `Prune` exactly that one is ready; two records with equal `created` are ordered by id; `Latest` returns `false` for another key or a missing store, and creates nothing; every state of the contract's table is derived from the files with the stated precedence, including a claim beside a `replaced` marker (unfinished) and an unreadable `outcome.json` (unfinished); an unreadable `record.json` is invisible; `Verify` returns nil for an untouched record and an `*ArtifactError` with the right `What` and each `Detail` for a removed, replaced-by-directory, replaced-by-symlink (POSIX) and overwritten artifact, and the same for the summary; `Summary` returns the stored text and an `*ArtifactError` for an overwritten summary; `Claim` returns an attempt whose `Path` is a regular file with the record's bytes, mode `0400` on POSIX, in a directory other than the artifact's; a second `Claim` returns `ErrClaimed`; 50 goroutines calling `Claim` on one record give exactly one success and leave exactly one attempt directory; `Claim` on a replaced record returns `ErrReplaced`, on an expired one `ErrExpired`, on one whose artifact was overwritten an `*ArtifactError`, and each leaves no `claim`, no `outcome.json` and no attempt directory; `Finish(0)` writes an outcome with state `applied`, `Finish(2)` with `failed` and exit 2, and both remove the artifact, the summary and the private copy; `Finish` after the copy was rewritten returns tampered true and records `failed` with `tampered: true` even for exit 0; `LastAttempt` returns the newest claimed record of the key other than the given id with its state, takes `started` from the claim, or from the claim file's modification time when it cannot be read, and returns `false` when no record of the key was ever claimed; `Prune` removes the bytes of replaced, expired, applied and failed plans, keeps the bytes of a ready plan and of an unfinished one whose claim is younger than 24 hours, removes the bytes and the copy of an older unfinished one and keeps its record, and removes a record directory seven days after the latest of its creation, claim and outcome; `Log` appends one JSON line per event with the fields set, creates the journal with mode `0600`, and when the file passes the limit keeps only whole lines from the newer half; 8 goroutines logging 50 events each on two `Store` values of one directory leave 400 whole lines; a stale `journal.lock` older than ten seconds is removed, a fresh one makes `Log` return an error after its wait; the store returns an `*UnusableError` for a `.rune` or a `.rune/plans` that is a symbolic link (POSIX) and for a `plans` that is a file. Run and see it fail to compile.
- [X] T007 [P] In `mcpserver/server_test.go`, add `TestToolForAppliesPlan`: a `TaskInfo` with `AppliesPlan: "plan"` and `Destructive: false` gives a tool whose `DestructiveHint` is true and whose description ends with the contract's `Plan approval:` paragraph, after `Doc`, `Returns` and the agent policy paragraph; a `TaskInfo` without it gives the same JSON as before the field existed. In `mcpserver/approval_test.go`, add `TestSummaryLines`: an empty summary gives `  (empty)`; lines are indented by two spaces; a control character and a direction override are escaped; a summary whose rendering passes `head+tail` gives the head lines, the mark `  [summary cut: <n> bytes not shown]` with `n` the rendered bytes left out, and the tail lines, never more than `head+tail` plus the mark; a single line longer than its share is cut at a character boundary; the result never ends with a line break. Run and see them fail to compile.

### Implementation

- [X] T008 In `internal/ast/ast.go`, add `AttrPlan = "plan"` and `AttrApplies = "applies"` with comments naming spec 051, add both to `KnownAttributes` after `AttrAgentAllow`, widen the comment of `Attribute.Allow` to named string arguments, add `type PlanSpec struct { File, Summary, Tool, Expires string }`, `func (t *Task) PlanSpec() *PlanSpec` (first `[plan]` attribute, nil without one), `func (t *Task) AppliesPlan() string` (the `Str` of the first `[applies]`), `func PlanExpiry(text string) (time.Duration, error)` with the constants `DefaultPlanExpiry = time.Hour`, `MinPlanExpiry = time.Minute`, `MaxPlanExpiry = 7 * 24 * time.Hour`, `func PlanPathError(p string) string` implementing the lexical rule of [data-model.md](data-model.md) ("" when the path is accepted), and the exported names `PlanFileVar = "RUNE_PLAN_FILE"`, `PlanIDVar = "RUNE_PLAN_ID"`, `PlanDigestVar = "RUNE_PLAN_DIGEST"`. In `internal/ast/dump.go`, render `AttrPlan` with `FormatAllow` as `AttrAgentAllow` is.
- [X] T009 In `internal/parser/attribute.go`, give `parseAllowPredicates` and `parseAgentAllowArgs` the attribute name and the two noun phrases for their messages (existing `agent-allow` messages unchanged, pinned by the existing tests), add `case ast.AttrPlan` that parses the named arguments the same way, and add `ast.AttrApplies` to the single-string case. In `internal/formatter/formatter.go`, render `ast.AttrPlan` as `ast.AttrAgentAllow` is. In `internal/language/builtin.go`, add the two entries of [contracts/plan-attributes.md](contracts/plan-attributes.md) after `agent-allow`. In `editors/vscode/syntaxes/runefile.tmLanguage.json`, add `plan` and `applies` to the attribute alternatives.
- [X] T010 In `internal/config/compose.go`, where a module's dependency names get the namespace prefix, give the `Str` of each `[applies]` attribute that names a task of the module the same prefix.
- [X] T011 Create `testdata/corpus/plan-apply.rune` (a leading comment naming spec 051, a plan task with all four arguments, an apply task, and an apply task with `[confirm]`) and generate `testdata/corpus/plan-apply.ast` and `testdata/fmt/plan-apply.fmt` with `-update`. Run T002 to T005 and the corpus, formatter, language and parser suites: all pass.
- [X] T012 Add `CodeInvalidPlanBinding = "RUNE2020"` to `internal/diag/codes.go` with a comment, and to whatever table `internal/analyzer/codes_test.go` and the diagnostics registry check, so the code suites pass.
- [X] T013 Create `internal/planstore/safefile.go` (`openRegular(path)`: `Lstat`, reject a link or a non-regular file, open, `Fstat`, `os.SameFile`; `ensureDir`: create `.rune` and `.rune/plans` with `0700`, refuse a symbolic link or a non-directory with `*UnusableError`, tighten the mode of `plans`, check the owner; `checkDir`: the same checks without creating) with `owner_unix.go` (`//go:build unix`, compares `Stat_t.Uid` with `os.Getuid()`) and `owner_other.go` (accepts).
- [X] T014 Create `internal/planstore/store.go`: `Store` with `Now`, `Open`, `Record`, `Binding`, `Digest`, `Git`, `State` and its constants, `SaveInput`, `Save`, `Latest`, `LastAttempt`, `AttemptInfo`, `Verify`, `Summary`, `Prune`, the limits of the contract as exported constants, `ErrReplaced`, `ErrExpired`, `ErrClaimed`, `*ArtifactError`, `*SourceError`, `*UnusableError`. Ids are 8 bytes from `crypto/rand`, hex. `record.json` is written to a temporary name and renamed. Records of a key are ordered by `created` in nanoseconds, then id. Every error is wrapped with `%w`.
- [X] T015 Create `internal/planstore/attempt.go`: `Attempt`, `Outcome`, `Claim` in the three steps of the contract (ready check, hashed copy into `attempt-<aid>/plan`, exclusive `claim`; any failure removes the copy and changes no state) and `Finish` (hash the copy, write `outcome.json`, remove the artifact, the summary and the copy directory).
- [X] T016 Create `internal/planstore/journal.go`: `Event`, `Log` returning an error, the lock file of the contract, `O_APPEND`, mode `0600`, and the size bound that keeps whole lines of the newer half. Run T006: passes, also with `-race`.
- [X] T017 In `mcpserver/server.go`, add `TaskInfo.AppliesPlan`, `ApprovalRequest.Plan` and `PlanApproval` with the comments of [contracts/mcpserver-api.md](contracts/mcpserver-api.md), make `toolFor` set the destructive hint and append the paragraph. In `mcpserver/approval.go`, add `SummaryLines`. Run T007: passes.
- [X] T018 In `.specify/memory/constitution.md`, amend Principle VII: destructiveness is declared with `[confirm]`, and a task that applies a reviewed plan, declared with `[applies]`, is also advertised as destructive and runs only when the person approves that plan, never on a grant. Bump the version to 1.4.1 (PATCH), update the Sync Impact Report and the last-amended date.

**Checkpoint**: Both attributes parse, dump, format and complete. The store passes its tests
alone. No run changes.

---

## Phase 3: User Story 1 - The apply task runs the plan the person approved (Priority: P1) MVP

**Goal**: plan, request, approval, exact bytes (FR-006 to FR-010, FR-015, FR-016, FR-019).

**Independent Test**: quickstart A and B on stdio and HTTP.

### Tests first

- [X] T019 [P] [US1] In `mcpserver/approval_test.go`, add `TestApprovalMessagePlan`: a request with `Plan` gives exactly the text of [contracts/mcp-tools.md](contracts/mcp-tools.md) for a one-line summary; a `Notes` entry is shown as `Note: <text>` above `Summary:`; a long summary shows head, mark and tail and the whole message stays within 8192 bytes; the last line is `Approving applies this plan once.`; a request without `Plan` is byte for byte what it was. In `mcpserver/handler_test.go`, add `TestApplyAlwaysAsks`: with an `ApprovalEngine` fake, `AskApproval` and a client that accepts, a call to a task with `AppliesPlan` reaches `CallApproved` with names starting with the task, both when the task is in `AllowDestructive` and when it is not, and an ungranted destructive task in its `DestructiveReach` follows in the names while a granted one does not. Run and see them fail.
- [X] T020 [P] [US1] Create `internal/cli/planapply_test.go` with a helper that builds an `mcpAdapter` from Runefile text in a temp directory, with a test clock and a fake `gitInfo`. `TestCapturePlan`: calling the plan task stores a record whose artifact equals the bytes the body wrote, removes the two workspace files, writes the plan line of the contract to the result's stderr, and journals `plan-created`; the record holds the key, the plan task, the apply tasks sorted, the masked arguments text, the working directory, the host, the Rune version, both digests and sizes, the expiry from `expires`, the surface `mcp` and the fake git fields; with a `gitInfo` that knows nothing the git fields are empty. `TestCapturePlanWorkingDirectory`: `file` is resolved against a plan task's `[working-directory]`. `TestApplyApproved`: `CallApproved` on the apply task calls `approve` once with a `Plan` holding the id, plan task, creation time, digest, size and summary; on nil it runs the body once with `RUNE_PLAN_FILE` naming a file with the stored bytes, `RUNE_PLAN_ID` and `RUNE_PLAN_DIGEST` set, and the plan body not run again; afterwards the record's state is applied with exit 0, the artifact is gone, and the journal holds `approval-requested`, `approval-given`, `apply-started` and `apply-finished`, the last two with one attempt id. `TestApplyEnvOnlyForApplyBody`: a dependency and an `&&` hook of the apply task do not see `RUNE_PLAN_FILE`. `TestApplyEnvWins`: with `RUNE_PLAN_FILE=decoy` in the adapter's base environment the body still gets the private copy. `TestApplySummaryMasked`: a summary holding a secret of the adapter's environment reaches `approve` masked. `TestTargetKey`: `targetKey` differs by Runefile path, by plan task, by argument value, and between the variadic lists `["a b"]` and `["a","b"]`, and is equal for equal inputs. `TestBindingDigests`: `runefileDigest` changes when a body changes and is stable otherwise; `configDigest` changes with a dotenv entry, with a setting and with a variable override; `toolIdentity` returns the resolved path and digest of an executable found on the `PATH` of a given environment, follows a symbolic link (POSIX), and returns an error for a name that is not there; `argsText` masks a secret value and cuts a long one at 64 characters. `TestGitInfo`: in a directory that is not a checkout, or with no `git` on the path, the result is empty and no error; skipped when `git` is absent for the positive case. Run and see it fail to compile.
- [X] T021 [P] [US1] Create `test/integration/plan_apply_mcp_test.go` with the scratch Runefile of [quickstart.md](quickstart.md), using the helpers of `approval_mcp_test.go` (`asker`, `approvalSession`, `callText`), and helpers that read the store. Add scenarios A and B on both transports. Run and see them fail.

### Implementation

- [X] T022 [US1] In `mcpserver/approval.go`, write the plan section in `approvalMessage` (the `Plan:`, `Artifact:`, `Note:` and `Summary:` lines with `SummaryLines(summary, 3072, 1024)`, the last line). In `mcpserver/handler.go`, give a task with `AppliesPlan` its own path: when the engine is an `ApprovalEngine`, validate the arguments, then call `CallApproved` with the task's name first in the names, whatever `granted` holds, and with the approver.
- [X] T023 [US1] Create `internal/cli/planapply.go`: `targetKey(runefile, planTask, args)`, `runefileDigest(file)`, `configDigest(settings, dotenvPairs, overrides)`, `toolIdentity(name, env)`, `argsText(task, args, mask)`, `gitInfo(ctx, dir)` with the two timed `git` calls of R9, `(e *engine) planStore()`, `(e *engine) planBinding(planTask)`, `(e *engine) capturePlan(task, args)` (R7), `(e *engine) findPlan(applyTask, args)` returning a ticket with the record, the summary and the note, `(e *engine) claimPlan(ticket)` setting `e.attempt`, `(e *engine) finishApply(exit)`, `planApproval(ticket, mask)`, and `(e *engine) journal(event)` that warns on standard error when `Store.Log` fails. `capturePlan` journals `plan-created` and one `plan-replaced` for each plan it replaced, `claimPlan` journals `apply-started`, `finishApply` journals `apply-finished`. Add the engine fields of [data-model.md](data-model.md) in `internal/cli/run.go`, call `capturePlan` at the end of `runBodyTo` when the body succeeded and the task has `[plan]`, and make `runBodyTo` append the three variables last for the invocation of `e.attempt` (task name and arguments key).
- [X] T024 [US1] In `internal/cli/serve.go`, split the dotenv entries out of `buildEnv` so that a load keeps them, and compute the definition and configuration digests once in `loadModuleFrom`. Carry them on `mcpAdapter` (and through `newAgentAdapter` in `internal/cli/agentexec.go`, and on the command-line engine in `execute`, with its variable overrides). In `internal/cli/mcp.go`: `Tasks()` sets `AppliesPlan` and leaves `Destructive` to `[confirm]`; `call` runs the apply steps in the order of [data-model.md](data-model.md): after the policy check, `findPlan`; then the approval with `Plan` set; then `claimPlan`; then the run; then `finishApply` with the exit code. Run T019 to T021: pass.

**Checkpoint**: An approved apply runs the stored bytes on both transports.

---

## Phase 4: User Story 2 - Anything other than the reviewed plan is refused (Priority: P1)

**Goal**: every refusal of FR-007 and FR-012, before any request.

**Independent Test**: quickstart D to O and AD.

- [X] T025 [P] [US2] In `internal/cli/planapply_test.go`, add `TestApplyRefusals`, table-driven, one row per engine refusal of [contracts/mcp-tools.md](contracts/mcp-tools.md) that is found at the lookup: no plan with and without parameters, applied, failed, unfinished, expired, each bound part alone (`Runefile definition`, `configuration`, `working directory`, `host`, `tool`), two parts together in the contract's order, a tool that is gone, and each `<detail>` for the artifact and for the summary. Each row: the whole text, `approve` never called, no marker of the apply body, and an `apply-refused` journal event with the reason. Add `TestRefusalMasksArguments`: the no-plan text for a secret argument value holds the mask and not the value, and a 200-character value is cut at 64. Add `TestCaptureRefusals`: a missing plan file, a missing summary, a symbolic link (POSIX), a directory, and a file over a lowered limit each fail the plan task with the contract's text and store no record; a declared tool that is not on the path fails the plan task with the contract's text; a store that cannot be used fails it with the contract's text; a workspace file that cannot be removed after it was stored (its directory made read-only, POSIX, skipped as root) leaves the plan stored and prints the contract's warning. Add `TestReplanReplaces`: two plan calls leave one ready record, the first has no artifact, and the journal holds `plan-replaced` with the first id in `plan` and the second in `replacedBy`. Add `TestPlanBodyFailureStoresNothing`. Run and see them fail.
- [X] T026 [P] [US2] In `test/integration/plan_apply_mcp_test.go`, add scenarios D, E, F, G, H, L and AD on both transports and I, J, K, M, N, O on stdio. G and N are skipped on Windows with a logged reason. Run and see them fail.
- [X] T027 [US2] In `internal/cli/planapply.go`, write `planRefusal` with every text of the contract, compare each bound part in `findPlan` and name the parts that differ in the contract's order, call `Store.Verify`, journal `apply-refused`, turn `*planstore.SourceError` and `*planstore.UnusableError` into the contract's texts, and warn when a workspace file cannot be removed. Run T025 and T026: pass.

---

## Phase 5: User Story 3 - One approval is one attempt (Priority: P1)

**Goal**: FR-014, FR-016, FR-017, FR-023.

**Independent Test**: quickstart C, P to T and AE.

- [X] T028 [P] [US3] In `internal/cli/planapply_test.go`, add `TestApplyOnce` (a second approved call is refused with `already applied`, no request), `TestApplyConcurrent` (20 goroutines calling `CallApproved` with an approving callback: the body ran once, the others got `already used by another call` or `already applied`), `TestApplyFailedIsFinal` (a failing body records failed with the exit code; the next call is refused with the failed text; after a new plan the request's `Notes` holds the contract's failed line without `Note: `), `TestApplyUnfinished` (a `claim` without outcome refuses with the unfinished text, and the note after a new plan is the unfinished one), `TestApprovalNotGivenLeavesPlanReady` (an `approve` error returns that error unchanged, runs nothing, journals `approval-not-given` with the reason, and a later approved call runs), `TestClaimRefusedAfterApproval` (an `approve` callback that stores a new plan, moves the clock past the expiry, or overwrites the artifact before it returns nil: nothing runs, the text is the contract's replaced, expired or artifact refusal, an `apply-refused` event follows `approval-given`, no outcome exists, and the next request has no note), `TestDependencyFailureConsumesPlan`, `TestApplyTampered` (a body that rewrites the file at `RUNE_PLAN_FILE` after making it writable and exits 0: the result's stderr holds the contract's tampered line, the exit code is 1, and the outcome is failed with `tampered: true`). Run and see them fail.
- [X] T029 [P] [US3] In `test/integration/plan_apply_mcp_test.go`, add scenarios C, P, Q, R, S, T on both transports and AE on stdio. Run and see them fail.
- [X] T030 [US3] In `internal/cli/planapply.go`, map `planstore.ErrClaimed`, `ErrReplaced`, `ErrExpired` and `*ArtifactError` from `Claim` to the contract's texts and journal `apply-refused` for them, build the note from `Store.LastAttempt`, journal `approval-requested`, `approval-given` and `approval-not-given` around the callback, make sure `finishApply` runs on every path after a claim, including a run error before the body, and make a tampered copy print the contract's line and fail the call with exit code 1. Run T028 and T029: pass, also with `-race`.

---

## Phase 6: User Story 4 - Approval cannot be supplied, widened or skipped (Priority: P1)

**Goal**: FR-013, FR-020 to FR-024, FR-031.

**Independent Test**: quickstart U to Y.

- [X] T031 [P] [US4] In `mcpserver/handler_test.go` and `mcpserver/approval_test.go`, add for a task with `AppliesPlan`: an engine that is not an `ApprovalEngine` gets the first server refusal of the contract before its arguments are read, with and without `RunCommand`; with `AskApproval` false the engine's `CallApproved` is still called and its `approve` returns the nobody-can-be-asked text; a client without form elicitation gets the no-form text from `approve`, and a fake engine that returns its own error before calling `approve` wins over it; invalid arguments are refused before the engine is called; a request too large gets the too-large text; a request whose `Plan` is nil is refused with the contract's text and nothing runs; a declined, dismissed and timed-out request gives the texts of spec 049 without the grant flag, also when `AllowDestructiveFlag` is set; a `Replace` while the request is pending gives the changed-Runefile text and runs nothing; every existing refusal text of a task without `AppliesPlan` is unchanged. Run and see them fail.
- [X] T032 [P] [US4] In `internal/cli/planapply_test.go`, add `TestCallListsRefusesApply` (the nobody-to-ask text, nothing runs, no journal line), `TestPolicyBeforePlan` (an `[agent-allow]` breach is refused with the policy text, `approve` is not called, and the store is not read), `TestApplyWithConfirm` (the request holds the plan and the `[confirm]` message, and the body runs without a second gate), `TestApplyAsksForUngrantedDependency` (through a real server: an ungranted `[confirm]` dependency is named in the one request and a granted one is not), `TestExecuteRefusesUnclaimedApply` (calling `engine.Execute` on an apply task with no attempt, or with an attempt for other arguments, fails with body not run). In `internal/cli/serve_test.go` and `internal/cli/mcpreload_test.go`, add tests that `--allow-destructive apply` for an apply task starts the server, prints the contract's warning at startup and on a reload, never prints the `is not destructive` warning for it, and leaves it out of the startup line. Run and see them fail.
- [X] T033 [P] [US4] In `test/integration/plan_apply_mcp_test.go`, add scenarios U, V, W, Y on both transports and X on stdio, plus a tools/list check of the apply tool's schema, hint and description, and a call through the endpoint of an `agent` task's adapter options (`AskApproval` false) at unit level in `internal/cli/planapply_test.go`. Run and see them fail.
- [X] T034 [US4] In `mcpserver/authz.go`, `mcpserver/approval.go` and `mcpserver/handler.go`, add the plan refusals, the approver that cannot ask, the nil-plan check, and keep the grant flag out of every text for a task with `AppliesPlan`. In `internal/cli/mcp.go`, refuse an apply task when no approval is pending. In `internal/cli/run.go`, add the second check in `Execute`. In `internal/cli/serve.go` and `internal/cli/mcpreload.go`, accept an apply task in `--allow-destructive` with the warning and keep it out of the startup line. Run T031 to T033: pass.

---

## Phase 7: User Story 9 - Runefiles without the attributes see no change (Priority: P1)

- [X] T035 [P] [US9] In `test/integration/plan_apply_mcp_test.go`, add scenario AC: a Runefile with neither attribute, served and called, leaves no `.rune/plans`. In `internal/cli/planapply_test.go`, add `TestNoStoreWithoutAttributes`: a call on such a Runefile never touches the store directory. Both must pass with the code of phases 3 to 6. Run the whole existing suite: no existing test changed.

---

## Phase 8: User Story 5 - A person at a terminal gets the same flow (Priority: P2)

**Goal**: FR-024 to FR-026.

**Independent Test**: quickstart Z and J.

- [X] T036 [P] [US5] Create `test/integration/plan_apply_cli_test.go` with scenario Z: `rune plan prod` prints the plan line; `rune --yes apply prod` prints the identity and applies; without a terminal and without `--yes` the contract's refusal with exit 1 and no marker; on a pty (as `confirm_pty_test.go` does) `n` runs nothing and leaves the plan ready and `y` applies; `--dry-run apply prod`, `--dry-run plan prod` and `--summary plan prod` store, ask and use nothing; `rune apply prod apply staging`, `rune plan prod apply prod` and `rune apply prod other` are each the contract's usage error and run nothing; with `RUNE_AGENT_RUN=1` the agent-run refusal, with and without `--yes`; a `[confirm("Really?")]` on the apply task prints `Confirmation: Really?` and asks once; a plan made through `rune mcp` with an SDK client is the one `rune --yes apply prod` applies in the same directory (FR-026); `rune --yes apply prod` gives the server's text for no plan, for an applied plan and for an expired plan; two `rune --yes apply prod` processes started together leave `apply.done` with one line (FR-014 across processes); `rune region=eu plan prod` then `rune region=us --yes apply prod` is refused for the configuration; the journal of an approved command-line apply holds `approval-requested` and `approval-given`, with the reason `--yes` when the flag answered, and `approval-not-given` after `n`. In `internal/cli/planapply_test.go`, add engine-level tests of the same branches of the new function. Run and see them fail.
- [X] T037 [US5] In `internal/cli/run.go` and `internal/cli/planapply.go`, add `(e *engine) prepareApplyRoot(invs)`, called in `execute` after the policy check when the plan mode is a real run: the usage error, the agent-run refusal, `findPlan`, the identity and `SummaryLines(summary, 48<<10, 16<<10)` on standard error, the question under the `[confirm]` rules, the three approval journal events, `claimPlan`, marking the task approved for its own `[confirm]`, and `finishApply` after `scheduler.Run`. Run T036: pass.

---

## Phase 9: User Story 6 - A wrong declaration is a static error (Priority: P2)

**Goal**: FR-002, FR-003, FR-011.

**Independent Test**: quickstart AA.

- [X] T038 [P] [US6] Create `internal/analyzer/plan_binding_test.go`: the Overview Runefile of the spec has no diagnostic; one test row per row of the static error table in [contracts/plan-attributes.md](contracts/plan-attributes.md) with the code `RUNE2020`, the whole message and the span's text, with one row for each rejected path form of `TestPlanPath` and for `summary="./tfplan"` beside `file="tfplan"`; the unknown-task row carries the hint; defaults that differ between plan and apply are accepted; parameters that differ only in enum values are rejected; an apply task in a module that names its module's plan task is accepted. In `test/integration/plan_apply_cli_test.go`, add one binary-level row: `rune analyze` on a Runefile with `[applies("paln")]` exits 3 with `error[RUNE2020]`, and `rune mcp` on it does not start. Run and see them fail.
- [X] T039 [US6] Create `internal/analyzer/planbinding.go` with `checkPlanBinding`, called from `Analyze` after the agent policy check, raising every row. Run T038: pass. Run the whole analyzer suite and the corpus: no new diagnostic on any existing fixture.

---

## Phase 10: User Story 7 - Stored plans are protected, bounded and traceable (Priority: P2)

**Goal**: FR-027 to FR-030.

**Independent Test**: quickstart AB.

- [X] T040 [P] [US7] In `test/integration/plan_apply_mcp_test.go`, add scenario AB after a plan and an approved apply: modes on POSIX, no artifact left, the five journal events with one plan id and digest and one attempt id on the apply events, an `apply-refused` event after a refused call, and no occurrence of the plan's bytes, the summary's bytes or a secret argument value in the journal or in any `record.json`. Add a check that `git status --porcelain` in a temporary repository lists nothing under `.rune/plans` (skipped when `git` is absent). In `internal/cli/planapply_test.go`, add `TestJournalMasksArguments`, `TestPruneOnUse` (an expired plan's artifact is gone after the next plan call) and `TestJournalFailureWarns` (a journal that cannot be written prints one warning and the call still runs). Run and see what fails.
- [X] T041 [US7] Call `Store.Prune` from `capturePlan` and `findPlan`, and fix whatever T040 shows. Run T040: pass.

---

## Phase 11: User Story 8 - The documentation states what is supported and what is not (Priority: P2)

**Goal**: FR-033.

- [X] T042 [P] [US8] In `docs/mcp.md`, add the section "Applying a reviewed plan": the two attributes and the Overview example, the flow, the approval request, the table of refusals, the plan's states and lifecycle, retention and the journal, recovery after a failed or unfinished apply, what a digest proves and what it does not (remote state, rollback), what the binding does not see (`env(...)` values, scripts a body calls, a version manager's shim, a dotenv file edited under a running server), the trusted execution assumptions (same-user processes, Windows modes, the summary is the author's), the order of checks with `[agent-allow]` and `[confirm]`, the command line as the manual way, and "Tool compatibility" stating OpenTofu and Terraform as supported and Helm and database migration runners as unsupported exact-plan integrations with the reason and what would have to be bound.
- [X] T043 [P] [US8] In `docs/runefile.md`, add the two attributes to the attribute list and a subsection with the syntax, the arguments, the body variables and `RUNE2020`. In `docs/GRAMMAR.md`, add the `plan` and `applies` alternatives and `PlanArg`. In `docs/diagnostics.md`, add the `RUNE2020` row. In `SECURITY.md` and `README.md`, add one sentence beside the one on `[agent-allow]`.
- [X] T044 [P] [US8] Create `docs/examples/plan-apply-opentofu/` and `docs/examples/plan-apply-terraform/`, each with a `Runefile` (the Overview pair with `tool="tofu"` or `tool="terraform"`, an `init` task, and `-input=false`), a `main.tf` using `terraform_data` and a variable `env`, a `.gitignore` for the tool's working files, and a `README.md` in the format of the other examples with `**Prerequisites:** opentofu` or `terraform` and a "Run it" block of `rune plan staging`. Add both to `docs/examples/README.md` and to `docs/nav.yaml` if examples are listed there. In `test/docs/examples_test.go`, map the prerequisites `opentofu` to `tofu` and `terraform` to `terraform`.
- [X] T045 [US8] Run the docs harness: `go test ./test/docs/...`. Every example validates, code blocks validate, links resolve.

---

## Phase 12: Polish & Cross-Cutting Concerns

- [X] T046 Run the full suite and the race suite in Docker, and `golangci-lint` in its image. Fix what they show without weakening a test.
- [X] T047 Measure coverage of the new code with `go test -coverprofile` for `./internal/planstore`, `./internal/cli`, `./internal/analyzer`, `./mcpserver`: at least 90% of the statements in the new and changed functions. Add tests for uncovered branches.
- [X] T048 Check the goldens: `git status` shows no changed golden other than the new corpus and formatter files. Check the new Go code against current Go idioms (range over integers, `slices` and `maps`, `errors.Is` and `errors.As`, `t.Context()` in tests where a context is needed).
- [X] T049 Walk [quickstart.md](quickstart.md) once against the built binary for A, B and Z by hand, and confirm every scenario letter has an automated test.

---

## Dependencies & Execution Order

- Phase 2 blocks everything. Within it: T008 before T009 to T011; T013 before T014 before T015; T017 and T018 are independent of the store.
- US1 (phase 3) needs phase 2. US2, US3 and US4 need US1 and are independent of each other in their tests, but all three change `internal/cli/planapply.go`, so their implementation tasks run in order.
- US9 is a check on phases 3 to 6.
- US5 needs US1 to US3 (the same functions). US6 needs only phase 2. US7 needs US1. US8 needs the texts of all the others.
- Polish is last.

## Parallel Opportunities

- T002 to T007 are six test files in six packages.
- In each story, the unit test task and the integration test task are different files.
- T042, T043 and T044 are different documents.

## Not covered by a test, on purpose

- The owner check of the store directory rejects only another user's directory, which a test
  cannot make without privileges. The accepting branch is covered.
- A call sent again after a connection loss (User Story 3, scenario 8) is a new call and is
  covered by "call again" in scenarios C and T.
- A Runefile reload during a running apply changes nothing for that call (spec 043), and has no
  test of its own here.

## Implementation Strategy

MVP is phases 1 to 3: a plan is stored and an approved apply runs its exact bytes. Phases 4 to
6 close every way around it and are needed before the feature can be called a control. Phases
7 to 11 follow in priority order. Each phase ends with its tests green before the next starts.
