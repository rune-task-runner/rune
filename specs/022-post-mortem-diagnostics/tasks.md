# Tasks: Post-Mortem Diagnostics (Failure Hooks)

**Input**: Design documents from `/specs/022-post-mortem-diagnostics/`

**Prerequisites**: plan.md, spec.md, research.md (R1–R12), data-model.md, contracts/ (grammar, mcp-result, failure-env), quickstart.md

**Tests**: INCLUDED — constitution VI mandates test-first (Red-Green-Refactor). Every red task must fail before its green counterpart is implemented. All Go tests run in Docker: `docker-compose run --rm test go test ./...`.

**Organization**: Foundational grammar/analysis/scheduler work blocks everything; then one phase per user story (US1 = MCP fix suggestion, US2 = CLI presentation, US3 = failure-context env).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: US1/US2/US3 per spec.md

## Phase 1: Setup

**Purpose**: Confirm a green baseline so every red test below is attributable to this feature.

- [x] T001 Verify baseline: `docker-compose run --rm test go test ./...` and `rune lint` pass on branch `022-post-mortem-diagnostics` before any change

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: `||` exists in the language (token → AST → parser → analyzer → formatter/dump/LSP → corpus) and the scheduler orchestrates fail hooks. No user story can function without this phase.

**⚠️ CRITICAL**: Complete before starting any user story phase.

### Grammar front end

- [x] T002 [P] RED: lexer tests — `||` yields a single `PIPEPIPE` token; lone `|` still emits RUNE1001 — in `internal/lexer/lexer_test.go`; extend `testdata/lexer/scenario1.rune` with a `&& … || …` task line (goldens regenerate in T005)
- [x] T003 [P] RED: parser tests — sibling of `TestParsePostHook` asserting `Task.FailHooks` (bare + parenthesized-args forms, multiple hooks in order); parse error for `t: a || c && b` (clause order fixed, contracts/grammar.md); `t: || c` with no deps — in `internal/parser/parser_test.go`
- [x] T004 Add `PIPEPIPE` kind + `kindNames` entry in `internal/token/token.go`
- [x] T005 GREEN: lex `||` via new `case '|':` arm in `lexOperator` in `internal/lexer/lexer.go` (mirror the `&` arm at :367); regenerate lexer goldens (`testdata/lexer/scenario1.tokens`); T002 passes
- [x] T006 Add `FailHooks []*DepCall` field to `Task` in `internal/ast/ast.go` and `FailHooks:` line in `internal/ast/dump.go` (mirror PostHooks at :73-75)
- [x] T007 GREEN: parse `[ "||" FailHooks ]` after the `&&` clause in `internal/parser/task.go` (mirror :67-73); regenerate parser goldens in `testdata/parser/`; T003 passes

### Static analysis

- [x] T008 [P] RED: analyzer tests — unknown `||` target → RUNE2001; wrong arity → RUNE2005; cycle through `||` edge (incl. `a: || a`) → RUNE2003; hook arg expression resolution; agent-executor `||` target → RUNE2011; `[context]` closure walk crosses `||` edges (RUNE2007 for `[confirm]`/agent in closure) — in `internal/analyzer/analyzer_test.go` + `internal/analyzer/context_test.go`
- [x] T009 Add `CodeInvalidFailureHook = "RUNE2011"` to `internal/diag/codes.go`; register it in `docs/diagnostics.md` code table and `specs/011-rune-lsp/contracts/diagnostic-codes.md` (frozen public contract — same change)
- [x] T010 GREEN: fold `FailHooks` into `checkDependencies` (:238), `checkExpressions` (:183), `checkCycles` (:295), `checkContextClosure` (:139,155); add RUNE2011 agent-target check anchored at `DepCall.Sp` — in `internal/analyzer/analyzer.go`; T008 passes

### Secondary language surfaces (all parallel once T006–T007 land)

- [x] T011 [P] Rewrite `mod::` prefixes on fail-hook refs during import namespacing in `internal/config/compose.go` (extend the `rewrite` closure at :139-153) + test in `internal/config/`
- [x] T012 [P] Emit canonical `" || …"` in the task formatter in `internal/formatter/formatter.go` (:104-110); add fixture pair to `testdata/fmt/` (covered by `TestFmtGolden` + `TestFmtIdempotent`)
- [x] T013 [P] Add `"failHooks"` field to the `--dump` JSON DTO in `internal/cli/dump.go` (:40, :78-80) + assertion in its test
- [x] T014 [P] Include fail hooks in hover/go-to-definition targets in `internal/language/position.go` (:93 combined slice) + test in `internal/language/`
- [x] T015 [P] Add a `||` task to `testdata/corpus/full.rune`, regenerate `testdata/corpus/full.ast` (`go test ./test/corpus -update`), and update the `Task`/`FailHooks` productions in `docs/GRAMMAR.md` per contracts/grammar.md (corpus drift message demands they move together)

### Scheduler orchestration

- [x] T016 RED: scheduler fake-engine tests — fire on body failure; no fire on success; no fire when error wraps `context.Canceled`; a failing dependency does NOT fire the dependent's hooks while the dependency's own hooks do fire (FR-003); original error always returned; hooks run in declaration order; hook-body failure → `Warnf` once, remaining hooks still run, hook's own `||` ignored; OS-unavailable hook → `Warnf` + skip; two failing roots sharing one hook → two hook body runs (memo bypass) while the hook's dependency runs once (deps memoize); hook's `&&` post-hooks run on hook success — in `internal/runtime/scheduler/scheduler_test.go`
- [x] T017 GREEN: implement in `internal/runtime/scheduler/scheduler.go` — exported `Failure{TaskName, ExitCode}` value object; extend `Engine` with `ExecuteFailHook(task, params, Failure) error` and `Warnf(format, ...any)`; the scheduler builds `Failure`, deriving `ExitCode` via `errors.As` on `*shell.ExecError` (importing `internal/runtime/shell` — acyclic, shell does not import scheduler) with fallback `1`; orchestrate per data-model.md §3 state machine at the body-failure point in `(*state).execute` (:118-120), extending `chain` during hook runs; T016 passes
- [x] T018 Implement the new `Engine` methods on the CLI engine in `internal/cli/run.go`: `ExecuteFailHook` runs the hook body via the existing `runBody` path (failure env lands in US3, header line in US2); `Warnf` prints the one-line `warning: …` convention to stderr (`theme.Warning` label) — plus compile-fix any other `Engine` implementers (scheduler test fakes); `Failure.ExitCode` derivation lives in the scheduler (T017)

**Checkpoint**: `||` parses, validates, formats, dumps, and executes hooks with correct semantics — verifiable entirely through unit/golden tests. User stories can now proceed (in parallel if desired).

---

## Phase 3: User Story 1 — Agent gets a fix suggestion with the failure (Priority: P1) 🎯 MVP

**Goal**: An MCP tool call whose task fails returns the task output, `[exit N]`, and a delimited, masked, 8 KiB-capped `[fix suggestion — from Runefile || failure hooks]` section in the same response (contracts/mcp-result.md).

**Independent Test**: quickstart Scenario C — call a failing hooked task over MCP stdio; assert the response contains the original output, the original exit code, and the delimited suggestion; a succeeding task's response is byte-identical to today's format.

### Tests for User Story 1

- [x] T019 [P] [US1] RED: `formatResult` tests — section rendered after `[exit N]` only when `FixSuggestion` non-empty; empty → byte-identical legacy layout; `IsError` still driven by `ExitCode` — in `mcpserver/server_test.go` (or a new `mcpserver/handler_test.go`)
- [x] T020 [P] [US1] RED: adapter capture tests mirroring `internal/cli/contextprep_test.go` — hook stdout lands in `Result.FixSuggestion` masked; 8 KiB cap with `\n[truncated]` marker; cut backs up to UTF-8 rune boundary; trailing `\r\n` trimmed; hook stderr goes to `Result.Stderr`, not the suggestion; no hooks / success → empty field — in `internal/cli/failhook_test.go` (new file)

### Implementation for User Story 1

- [x] T021 [US1] Add `FixSuggestion string` to `Result` in `mcpserver/server.go` (document the pre-masked contract like `Options.Instructions`); append the conditional section in `formatResult` in `mcpserver/handler.go` (:63-79); T019 passes
- [x] T022 [US1] Wire capture: optional fail-hook stdout sink (`io.Writer`) on the engine in `internal/cli/run.go` (nil on the CLI path); in `(*mcpAdapter).Call` in `internal/cli/mcp.go`, supply a dedicated buffer wrapped in `mask.NewWriter(a.maskSet)`, flush after `scheduler.Run`, trim/cap per data-model.md §4 (reuse or mirror the `contextMaxBytes` discipline in `internal/cli/contextprep.go:63-72`), and populate `Result.FixSuggestion`; T020 passes
- [x] T023 [US1] Integration test: MCP stdio round-trip — failing hooked task returns the delimited section with original exit code; succeeding task returns legacy-identical text; suggestion carries no ANSI — in `test/integration/failure_hooks_mcp_test.go` (new file, reuse the harness + existing MCP test plumbing)

**Checkpoint**: MVP shippable — agents get fix suggestions in-band.

---

## Phase 4: User Story 2 — Terminal users see the diagnosis at the point of failure (Priority: P2)

**Goal**: CLI failure runs the hooks, streams their output under a `diagnosing: <task>` header, and exits with the original task's exit code (research R8).

**Independent Test**: quickstart Scenario A — failing task prints the header + hook output and `$?` equals the task's own exit code; with the failure removed, no header appears.

### Tests for User Story 2

- [x] T024 [P] [US2] RED: binary-level integration tests — failing task shows `diagnosing: diagnose` on stderr before hook output and process exit code equals the body's code (7, not the hook's 0); hook-failure prints `warning: failure hook … failed` and exit code still original; success prints no header; `--color never` (or `NO_COLOR`) output is byte-identical to the styled run stripped of ANSI (mirror `test/integration/error_banner_test.go:41` invariant) — in `test/integration/failure_hooks_cli_test.go` (new file)

### Implementation for User Story 2

- [x] T025 [US2] Print the `diagnosing: <hook-name>` header (label styled `theme.Warning`, name plain) to stderr at the top of `ExecuteFailHook` in `internal/cli/run.go`, following the `running:`/`cached:` label convention (:315-325); T024 passes

**Checkpoint**: Both surfaces deliver diagnostics; US1 and US2 independently verifiable.

---

## Phase 5: User Story 3 — Diagnostic tasks know what failed (Priority: P3)

**Goal**: Hook bodies see `RUNE_FAILED_TASK` and `RUNE_FAILED_EXIT_CODE` (contracts/failure-env.md), so one diagnostic task serves many tasks.

**Independent Test**: quickstart Scenario B — one `diagnose` hook attached to `test` (exit 2) and `lint` (exit 5) echoes the matching name/code for each run.

### Tests for User Story 3

- [x] T026 [P] [US3] RED: unit test in `internal/cli/failhook_test.go` — hook body observes both variables with correct values, appended after `[env(...)]` pairs (failure context wins name collisions); integration test in `test/integration/failure_hooks_env_test.go` (new file) — shared hook across two separately-run failing tasks echoes per-failure values (Scenario B)

### Implementation for User Story 3

- [x] T027 [US3] Append `RUNE_FAILED_TASK` / `RUNE_FAILED_EXIT_CODE` from the `scheduler.Failure` argument to the hook body's env in `ExecuteFailHook` in `internal/cli/run.go` (after `taskEnv(task)` output); T026 passes

**Checkpoint**: All three user stories independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Docs-as-fixtures, examples, and the full gate set (constitution: surface changes carry their docs in the same PR).

- [x] T028 [P] Docs — language reference: `docs/runefile.md` §Dependencies-and-post-hooks gains the `||` clause (+ :261 silent-skip sentence gains the fail-hook warning exception); `docs/how-to/dependencies-and-hooks.md` covers failure hooks, fixes the ":42 hooks don't run on failure" pitfall, and amends the "each task runs at most once per invocation" pitfall with the once-per-failure exception for `||` hooks; `docs/how-to/os-filtering.md:11` notes the fail-hook warning exception; `docs/troubleshooting.md` cross-link
- [x] T029 [P] Docs — agent surface: `docs/mcp.md` documents the fix-suggestion section (modeled on the `[context]` section, citing cap/masking/best-effort semantics per contracts/mcp-result.md)
- [x] T030 [P] Example: extend `docs/examples/dependencies/Runefile` + `README.md` with a failure-hook walkthrough (README contract fields per `test/docs/contract_test.go`); update index rows in `docs/examples/README.md`, `docs/how-to/README.md`, `docs/user-guide/README.md`
- [x] T031 Run the docs harness: `docker-compose run --rm test go test ./test/docs/...` (links, code blocks, example contract, terminology) — fix fallout
- [x] T032 Full gate set: `docker-compose run --rm test go test ./...`, `docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...`, `rune lint`, `rune fmt` (idempotent), fuzz seed with `||` added to `internal/parser` corpus
- [x] T033 Manual quickstart validation: execute Scenarios A–E from `quickstart.md` (incl. Ctrl-C Scenario E, which has no portable automated test) and check off the SC spot-check table

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)** → **Foundational (P2)** → user stories → **Polish (P6)**
- **US1 (Phase 3)**, **US2 (Phase 4)**, **US3 (Phase 5)**: each depends only on Phase 2 — they touch disjoint code (US1: mcpserver + mcp.go; US2: run.go header; US3: run.go env) and can run in parallel; note US2's T025 and US3's T027 both edit `internal/cli/run.go` (`ExecuteFailHook`) — sequence those two tasks if worked concurrently.

### Within Phase 2

- T002, T003 (red) first — parallel
- T004 → T005 (lexer green needs token) ; T006 → T007 (parser green needs AST field)
- T008 ∥ with T004–T007; T009 → T010 (analyzer green needs code + parser output)
- T011–T015 all parallel after T007
- T016 → T017 → T018 (scheduler red → scheduler green → CLI engine impl)

### Parallel Opportunities

- Phase 2: {T002, T003, T008} together; then {T011, T012, T013, T014, T015} together
- Story phases: US1 ∥ US2 ∥ US3 after Phase 2 (mind the run.go note above)
- Phase 6: {T028, T029, T030} together

## Parallel Example: User Story 1

```bash
# Red tests together (different files):
Task: "formatResult section tests in mcpserver/server_test.go"          # T019
Task: "adapter capture/cap/mask tests in internal/cli/failhook_test.go" # T020
# Then green: T021 (mcpserver) ∥ nothing, T022 depends on T021's field.
```

## Implementation Strategy

**MVP first**: Phases 1–3 only (T001–T023) deliver the feature's core promise — agents receive fix suggestions in-band. Stop, validate quickstart Scenario C, demo.

**Incremental delivery**: add Phase 4 (CLI, small), then Phase 5 (env, small), then Phase 6 polish. Each checkpoint leaves the tree green and shippable; commit after each task or logical group (run tests in Docker before every commit, per global policy).

## Notes

- Every red task must be observed failing before its green counterpart starts.
- Golden regeneration is deliberate only: package `-update` flags and `go test ./test/corpus -update`; never hand-edit goldens.
- FR traceability: T002–T007 → FR-001/013; T008–T010 → FR-010/011; T016–T018 → FR-002/003/004/005/012; T019–T023 → FR-006/007; T024–T025 → FR-008; T026–T027 → FR-009.
