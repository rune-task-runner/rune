# Tasks: Typed Parameter Schemas and Outcome Descriptions for Agents

**Input**: Design documents from `/specs/023-mcp-typed-schemas/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ (grammar, mcp-schema, diagnostic-codes), quickstart.md

**Tests**: INCLUDED — Constitution Principle VI mandates test-first (Red-Green-Refactor). Every story phase opens with failing tests. All Go tests run inside the Docker harness: `docker-compose run --rm test go test ./...` (never on the host).

**Organization**: Grouped by user story (spec.md priorities). US1+US2 are both P1 — US1 (schema surfacing) is the MVP cut line; US2 (enforcement) completes the P1 contract.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: US1 (typed schema), US2 (enforcement), US3 (returns), US4 (authoring diagnostics)

## Phase 1: Setup

**Purpose**: Baseline before touching the grammar — every later golden/corpus diff must be attributable to this feature.

- [X] T001 Verify green baseline on branch `023-mcp-typed-schemas`: `docker-compose run --rm test go test ./...` and `go run ./cmd/rune --list` both succeed; capture a pre-feature `rune --dump --format json` of `testdata/corpus/full.rune` to scratch for the SC-003 diff in T031

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The inline `name:kind` grammar, the `Constraint` model, and every parameter renderer kept in sync. No story can land before this — all four stories consume the parse result.

**⚠️ CRITICAL**: T002–T003 are the RED step for this phase; write them first and watch them fail.

- [X] T002 [P] RED: add parser fixtures + test rows for typed params — `testdata/parser/typed-params.rune` + expected `.ast` (required/defaulted/variadic × each kind, `enum` values, adjacency: spaced `env: build` still a dep), table rows in `internal/parser/parser_test.go`, and RUNE1006 rows (empty `enum()`, non-string enum value, value list on non-enum kind) in `internal/parser/codes_test.go`
- [X] T003 [P] RED: add formatter fixture pair `testdata/fmt/typed-params.rune` + `testdata/fmt/typed-params.fmt` covering `name:kind`, `name:enum("a","b")="a"`, `+files:path` (canonical dense form per contracts/grammar.md)
- [X] T004 [P] Implement `ConstraintKind`, `Constraint{Kind, KindName, Values, Sp}`, `Param.Constraint`, and `Constraint.Check(value string) error` with the R2 rule table in `internal/ast/ast.go`; unit-test `Check` for every kind (valid/invalid/empty-string cases) in `internal/ast/ast_test.go`
- [X] T005 [P] Register diagnostic codes RUNE1006, RUNE2012–RUNE2016 in `internal/diag/codes.go` and document them in lockstep in `docs/diagnostics.md` and `specs/011-rune-lsp/contracts/diagnostic-codes.md` (append-only contract; wording per contracts/diagnostic-codes.md)
- [X] T006 Implement inline `TypeAnno` parsing in `parseParam` in `internal/parser/task.go`: both-sides span-offset adjacency rule, contextual kind keywords, `enum` value-list parsing, RUNE1006 for malformed syntax; store raw `KindName` (membership is the analyzer's job); make T002 green; add fuzz seed corpus entries for the new syntax under `internal/parser/testdata/fuzz/` and lexer seeds if the lexer fuzzer has a seed dir
- [X] T007 Analyzer gate for grammar soundness: RUNE2012 (unknown kind, message lists supported kinds) in `checkParams` in `internal/analyzer/analyzer.go`, with a table row in `internal/analyzer/codes_test.go` — every parsed constraint downstream is now valid-kind-or-error
- [X] T008 Update `formatParam` in `internal/formatter/formatter.go` to print annotations canonically; make T003 green (idempotency comes from the existing harness)
- [X] T009 Update `ast.Dump` param rendering in `internal/ast/dump.go`, extend `testdata/corpus/full.rune` with annotated params, and deliberately regenerate `testdata/corpus/full.ast` via `go test ./test/corpus -update` (review the diff — GRAMMAR.md update lands in T033)
- [X] T010 Update `TaskSignature` in `internal/language/symbol.go` for annotated params and verify `paramNameSpan` (`internal/language/position.go`) still anchors hover/rename on the bare name now that `Param.Sp` widens; adjust `testdata/lsp/` goldens if any capture signatures

**Checkpoint**: annotated Runefiles parse, format, dump, and analyze (unknown kinds rejected); unannotated files are AST-identical. Stories can proceed — US1/US3 in parallel if staffed.

---

## Phase 3: User Story 1 — Agent receives a strictly typed tool schema (Priority: P1) 🎯 MVP

**Goal**: Constraints and per-parameter descriptions appear machine-readably in the MCP tool definition (contracts/mcp-schema.md mapping); unannotated schemas stay byte-identical.

**Independent Test**: quickstart §1 — `tools/list` over stdio shows `"enum":["staging","prod"]`, `"type":"number"`, `format:"path"` array items, and `param-doc` text as `description`.

### Tests for User Story 1 (RED first)

- [X] T011 [P] [US1] Extend `TestInputSchema` in `mcpserver/server_test.go`: one case per kind + variadic-of-kind + `description` from ParamInfo + a byte-identical golden for an unannotated task (SC-003 guard); watch it fail
- [X] T012 [P] [US1] Integration RED: new `test/integration/typed_schema_mcp_test.go` using the `mcpCall` harness pattern from `test/integration/secret_masking_mcp_test.go` — `tools/list` on the quickstart fixture asserts enum values, number type, path format, and param description in the served JSON

### Implementation for User Story 1

- [X] T013 [P] [US1] Parse `[param-doc("p","text")]` (two-string form, `[env]` precedent) in `internal/parser/attribute.go`, add `AttrParamDoc` + `Task.ParamDoc(name)` helper in `internal/ast/ast.go`; parser test row in `internal/parser/parser_test.go`
- [X] T014 [P] [US1] Add `Kind`, `Enum`, `Description` fields to `ParamInfo` in `mcpserver/server.go` (additive; public API) and implement the kind → JSON Schema mapping in `inputSchema()` per contracts/mcp-schema.md
- [X] T015 [US1] Map `ast.Constraint` + `param-doc` into `ParamInfo` in the engine adapter `Tasks()` in `internal/cli/mcp.go` (depends on T013, T014)
- [X] T016 [US1] Extend `paramDTO` with `type`/`values`/`description` (omitempty) and wire in `toDTO` in `internal/cli/dump.go`; assert in `internal/cli/dump_test.go` (or nearest existing dump test) that an unannotated file's JSON is unchanged
- [X] T017 [US1] Make T011 + T012 green; verify quickstart §1 manually against the fixture

**Checkpoint**: agents see typed schemas — MVP demonstrable. Enforcement not yet active.

---

## Phase 4: User Story 2 — Invalid arguments are rejected before anything runs (Priority: P1)

**Goal**: `Constraint.Check` enforced at all three binding choke points + MCP pre-join variadic elements; FR-006 error sentences; zero side effects.

**Independent Test**: quickstart §2 — CLI exit 2 with param/value/allowed-set in stderr and no task output; MCP `tools/call` error result with the same sentence.

### Tests for User Story 2 (RED first)

- [X] T018 [P] [US2] Handler RED in `mcpserver/handler_test.go`: scalar out-of-enum arg → tool error result naming param/value/allowed, task never executes; variadic JSON array with one bad element → per-element rejection BEFORE the space-join
- [X] T019 [P] [US2] Binding RED in `internal/cli/args_test.go`: `bindParams` rejects invalid positional (incl. per-element on the `pos[i:]` variadic slice pre-join), `bindNamedParams` rejects invalid named, non-literal default that evaluates to an invalid value is caught at bind time
- [X] T020 [P] [US2] Integration RED: `test/integration/typed_enforcement_test.go` — CLI invalid arg → exit 2 + FR-006 stderr + no body output; valid decimal for `number` runs; dependency `(deploy "nope")` parenthesized arg rejected before execution
- [X] T021 [US2] Implement enforcement: call `Constraint.Check` from `bindParams` and `bindNamedParams` in `internal/cli/args.go` (variadic per-element before join) and from `ResolveDep` in `internal/cli/run.go` after arg evaluation (preserve the OS-unavailable early return); FR-006 message assembly with `usagef`/`ValidationError` per research R6
- [X] T022 [US2] Implement MCP-side validation in `mcpserver/handler.go`: validate scalars from `ParamInfo` after `decodeArgs`, validate variadic array elements inside `decodeArgs` before joining; return tool error results; make T018–T020 green
- [X] T023 [US2] Masking/size-cap assertion (FR-011): extend `test/integration/typed_schema_mcp_test.go` (or the secret-masking test) proving no environment-sourced value can appear in a schema and the composed description respects the existing cap discipline

**Checkpoint**: the P1 contract is complete — schemas advertised AND enforced on every path.

---

## Phase 5: User Story 3 — Agent learns the expected outcome of a task (Priority: P2)

**Goal**: `[returns("…")]` surfaces as a `Returns:` trailer in the tool description, in `--list`, and in the JSON dump; absent attribute changes nothing.

**Independent Test**: quickstart §1 (description trailer) + §5 (`jq '.tasks[0].returns'`).

### Tests for User Story 3 (RED first)

- [X] T024 [P] [US3] RED in `mcpserver/server_test.go`: `toolFor` composes `<doc>\n\nReturns: <text>` when `TaskInfo.Returns` set, emits no trailer when absent; RED in `internal/cli/dump_test.go` for `taskDTO.returns`

### Implementation for User Story 3

- [X] T025 [P] [US3] Parse `[returns("…")]` in `internal/parser/attribute.go` (single-string group), add `AttrReturns` + `Task.Returns()` helper in `internal/ast/ast.go`; parser test row; formatter needs no change (default `[kind("str")]` path) — extend `testdata/fmt/typed-params.rune`/`.fmt` to prove it
- [X] T026 [US3] Add `Returns` to `TaskInfo` in `mcpserver/server.go`, compose the description trailer in `toolFor()` with the existing agent-text cap; map it in `internal/cli/mcp.go`; add `returns` (omitempty) to `taskDTO` in `internal/cli/dump.go`; make T024 green
- [X] T027 [US3] Show the outcome description in human listings: render a `returns:` line for annotated tasks in the `--list` output path (`internal/cli/`, wherever task docs render — follow the existing doc-line style); golden/integration assertion alongside the existing `--list` tests

**Checkpoint**: agents get success criteria; humans see the same text in listings and dumps.

---

## Phase 6: User Story 4 — Author gets immediate feedback on annotation mistakes (Priority: P3)

**Goal**: The full static-diagnostic suite for annotations (RUNE2013–2015 + RUNE2012 hint machinery), quickstart §3 table green.

### Tests for User Story 4 (RED first)

- [X] T028 [P] [US4] RED: table rows in `internal/analyzer/codes_test.go` for RUNE2013 (duplicate enum values, Related span on first occurrence), RUNE2014 (literal default violates constraint, span on default expr), RUNE2015 (param-doc unknown param; duplicate param-doc; duplicate `[returns]` on one task), RUNE2016 warning (fixture: task named `string` + header `deploy env:string`), and the RUNE2012 spacing-hint case for an unknown kind matching an existing task name

### Implementation for User Story 4

- [X] T029 [US4] Implement RUNE2013 + RUNE2014 in `checkParams`, RUNE2015 (incl. duplicate `[returns]`) as an attribute check, and the RUNE2016 shadow warning (annotated kind name matches an existing task name — warning severity, message per contracts/diagnostic-codes.md) in `internal/analyzer/analyzer.go` (pattern: existing duplicate-`[context]` Related usage); add the RUNE2012 spacing hint ("if `X` was meant as a dependency, add a space"); make T028 green
- [X] T030 [US4] Verify quickstart §3 end-to-end: each malformed snippet fails `rune --list` with the right code, caret span, and zero execution — add any missing case to `internal/analyzer/codes_test.go` or `test/integration/`

**Checkpoint**: all four stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: LSP enrichment, docs-as-fixtures (Engineering Constraints: same PR), registry drift fix, compatibility proof.

- [X] T031 [P] SC-003 proof: diff the T001 pre-feature dump/schema captures against post-feature output for an unannotated Runefile (byte-identical); `rune fmt` no-op check on an unannotated file
- [X] T032 [P] Add `returns` + `param-doc` to `builtinAttributes` in `internal/language/builtin.go`, fix the pre-existing drift (`doc`, `script`, `context`), and extend `internal/language/builtin_sync_test.go` to lock registry ↔ parser attribute names
- [X] T033 [P] Docs: update `docs/GRAMMAR.md` (Param/TypeAnno/AttrItem productions per contracts/grammar.md), `docs/runefile.md` (Parameters + Attributes sections), `docs/mcp.md` (typed-schema + Returns section), `docs/examples/parameters/README.md` (typed form; test/docs coverage matrix requires it)
- [X] T034 [P] LSP enrichment: `varHover` renders kind/enum values/param-doc text in `internal/language/hover.go`; completion `Detail` shows the kind in `internal/language/completion.go`; tests beside the existing hover/completion tables
- [X] T035 Release-notes/CHANGELOG entry documenting the unspaced-header break and its RUNE2012 hint (plan.md Complexity Tracking obligation)
- [X] T036 Full gate run per quickstart §6: `docker-compose run --rm test go test ./...`, `docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...`, `rune lint`, `rune docs-check`, corpus test — all green; run every quickstart scenario once against the built binary

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 → Phase 2**: baseline capture before grammar changes.
- **Phase 2 blocks everything**: all stories consume the parse result. Within Phase 2: T002/T003 (RED) and T004/T005 first; T006 needs T002+T004+T005; T007 needs T005+T006; T008 needs T003+T006; T009–T010 need T006.
- **US1 (Phase 3)**: after Phase 2. T013/T014 parallel; T015 needs both; T016 needs T006; T017 closes.
- **US2 (Phase 4)**: after Phase 2; T022 additionally needs US1's T014/T015 (`ParamInfo` fields carry the validation data) — start T018–T021 in parallel with late US1 if staffed.
- **US3 (Phase 5)**: after Phase 2 only — independent of US1/US2 except sharing `mcpserver` files (serialize edits to `server.go` with T014).
- **US4 (Phase 6)**: after Phase 2; RUNE2015 additionally needs T013 (`param-doc` must exist).
- **Polish (Phase 7)**: after all selected stories; T031 needs T001.

### Parallel Opportunities

- Phase 2: T002 ∥ T003 ∥ T004 ∥ T005 (four different file sets).
- US1: T011 ∥ T012, then T013 ∥ T014.
- US2: T018 ∥ T019 ∥ T020 (three test files).
- Polish: T031 ∥ T032 ∥ T033 ∥ T034.
- Cross-story: US3 and US4 can run in parallel with US2 after Phase 2 (watch shared files: `server.go` [T014/T026], `attribute.go` [T013/T025], `analyzer.go` [T007/T029]).

### Parallel Example: Phase 2 kickoff

```bash
# Four independent RED/model tasks at once:
Task: "T002 parser fixtures testdata/parser/typed-params.* + codes_test rows"
Task: "T003 formatter fixture pair testdata/fmt/typed-params.{rune,fmt}"
Task: "T004 Constraint model + Check() in internal/ast/ast.go"
Task: "T005 diagnostic codes in internal/diag/codes.go + both docs (lockstep)"
```

---

## Implementation Strategy

### MVP First

1. Phase 1 + Phase 2 (foundation: grammar parses everywhere, renderers in sync).
2. Phase 3 (US1) → **STOP and VALIDATE**: quickstart §1 — agents see typed schemas. Demoable MVP.
3. Phase 4 (US2) completes the P1 contract (advertised AND enforced) — this is the sensible first release cut.
4. Phase 5 (US3), Phase 6 (US4) as independent increments; Phase 7 before merge (docs are same-PR obligations, not optional).

### Notes

- Every story phase is Red → Green: write the listed tests first, watch them fail inside the Docker harness, then implement.
- Golden/corpus regens (`-update`) are deliberate, reviewed diffs — never hand-edited (Constitution VI).
- Commit after each task or logical group; keep the corpus regen (T009) its own commit for reviewability.
- Single-PR obligations regardless of story cuts: T033 docs + T005 diagnostics docs travel with the surface change.
