---

description: "Task list for spec 044: return structured tool results"
---

# Tasks: Return Structured Tool Results

**Input**: Design documents from `/specs/044-mcp-structured-results/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer). Every test that covers new
behavior is written and seen failing before the code that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US3)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- The schema and its description texts are fixed by [contracts/mcp-results.md](contracts/mcp-results.md).
  Tests pin them as literals and do not rebuild them with the production function.
- The text content stays byte-identical. Existing tests that pin result text or tool listings must
  pass with no change to their assertions.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/ ./internal/cli/ ./test/integration/` on branch `044-mcp-structured-results` and confirm it is green. If anything fails, report it and stop.

---

## Phase 2: Foundational (one capped result, two renderings)

**Purpose**: Split capping from rendering with no behavior change ([research R1](research.md)), so
every story builds the structured content from the value the text is built from.

- [X] T002 In new `mcpserver/structured_test.go` add tests that fail to compile until T003:
  - `TestCapResult`: for a result with 10,000 bytes on each stream and limit 1,024, both streams are at most 1,024 bytes and equal `capOutput(stream, 1024)`; `ExitCode` and `FixSuggestion` are untouched; a result within the limit is returned equal.
  - `TestRenderTextDoesNotCap`: `renderText` renders a result with a 3,000-byte stream whole, and the same result after `capResult(r, 1024)` shorter and with one marker. The text layout itself stays pinned by the existing `formatResult` tests, which now run through `renderText`.
- [X] T003 In `mcpserver/handler.go` and new `mcpserver/structured.go`: add `capResult(r Result, limit int) Result`; move the body of `formatResult` after the cap into `renderText(r Result) string`; make `formatResult(r, limit)` return `renderText(capResult(r, limit))`. No other change.
- [X] T004 Run `go test ./mcpserver/` in Docker. Every existing test passes unchanged and T002 is green.

**Checkpoint**: Same behavior, ready for a second rendering.

---

## Phase 3: User Story 1 - An agent reads the exit code and the streams as fields (Priority: P1) MVP

**Goal**: Every tool declares the output schema and every task run returns structured content.

**Independent Test**: A task that prints one line to each stream and exits 3: the structured
content has `exitCode` 3 and the two lines as `stdout` and `stderr`.

### Tests first

- [X] T005 [P] [US1] In `mcpserver/structured_test.go` add, failing until T007:
  - `TestOutputSchema`: `outputSchema()` marshals to exactly the JSON of the contract (compare after decoding both into `any`, with the four description strings written out as literals in the test); two calls return distinct maps.
  - `TestEveryToolDeclaresOutputSchema`: over an in-memory client, every tool of `sampleEngine()` has an `OutputSchema` whose JSON equals the contract's; also with `AllowList`, and for a destructive, a networked and a namespaced task.
  - `TestStructuredContent`: for an engine result `{Stdout: "out\n", Stderr: "err\n", ExitCode: 0}` the result's structured content, marshaled, is exactly `{"exitCode":0,"stdout":"out\n","stderr":"err\n"}`; with `ExitCode: 3` it has `"exitCode":3` and `IsError` is true; with empty streams both keys are present with `""`; with `FixSuggestion: "re-run\n"` the key `fixSuggestion` holds it; without, the raw JSON has no `fixSuggestion` key.
  - `TestStructuredExitNotParsedFromOutput`: stdout `"[exit 0]\n"` with `ExitCode: 1` gives `exitCode` 1 and `stdout` `"[exit 0]\n"`.
  - `TestStructuredValidatesAgainstSchema`: resolve the listed tool's output schema with `github.com/google/jsonschema-go/jsonschema` and validate the structured content of a table of results (success, failure, empty, fix suggestion, capped, invalid UTF-8 bytes, exit codes 130 and 255). Also assert the validator rejects an object with an extra key and one without `stdout`, so the schema is known to be strict.
  - `TestStructuredOverHTTP` (FR-003): over the HTTP transport, with the helpers of `mcpserver/transport_test.go`, a listed tool has the output schema and a call returns the same structured content as over the in-memory transport.
  - `TestStructuredListEngine`: an engine that implements `ListEngine` returns structured content the same way.
- [X] T006 [P] [US1] Create `test/integration/structured_mcp_test.go`, failing until T007, using the `startMCP` client of `test/integration/reload_mcp_test.go` (add a `callRaw(tool)` helper that returns the raw `result` JSON):
  - `TestStructuredMCP_ResultFields` (stands for `TestMCPE2E_Pending_StructuredResult`, FR-021): Runefile of [quickstart.md](quickstart.md); `tools/list` shows `outputSchema` on every tool, equal to the contract's JSON; calling `test` returns the text of quickstart step 3 and structured content `{"exitCode":1,"stdout":"running tests\n","stderr":"FAIL: fixtures out of date\ndiagnosing: hint\n","fixSuggestion":"re-run with -update"}` with `isError` true (the text and these values were captured from the binary built from `main` on 2026-11-14); calling `hint` returns `exitCode` 0, `stderr` `""`, and no `fixSuggestion` key.
  - `TestStructuredMCP_OldProtocolClient`: a client that initializes with protocol version `2024-11-05` (the existing `toolCall` helper) calls `hint` and gets exactly the text `re-run with -update\n[exit 0]`, with `isError` false.

### Implementation

- [X] T007 [US1] In `mcpserver/structured.go` add `outputSchema()` and `structuredResult` with the JSON tags of [research R2](research.md) and a constructor from a capped `Result`; in `mcpserver/server.go` set `OutputSchema: outputSchema()` in `toolFor`; in `mcpserver/handler.go` cap once with `capResult`, build the text with `renderText` and set `StructuredContent`. Update the handler's doc comment. In `go.mod` move `github.com/google/jsonschema-go` from the indirect block to the direct one, confirm `go mod tidy -diff` prints nothing, and check that `go.sum` is unchanged.
- [X] T008 [US1] Run `go test ./mcpserver/ ./test/integration/ -run 'Structured|OutputSchema|CapResult|RenderText'` in Docker; green.

**Checkpoint**: The issue's first two acceptance criteria hold on the happy path.

---

## Phase 4: User Story 2 - Existing clients see no difference (Priority: P1)

**Goal**: Text, error flag, listing and refusals are what they were.

**Independent Test**: The existing suites pass unchanged, and the text can be rebuilt from the
fields.

### Tests first

- [X] T009 [P] [US2] In `mcpserver/structured_test.go` add:
  - `TestTextRebuiltFromStructured` (SC-006): for a table covering every joining case of the text rule in [data-model.md](data-model.md) (each stream empty or not, ending with a line break or not, with and without a fix suggestion, over the limit), call the tool, decode the structured content from JSON, rebuild the text with a function written in the test from the rule, and compare with the text content byte for byte.
  - `TestResultHasOneTextBlock`: every result kind (ran, failed, refused, bad arguments) has exactly one content block, of type text, and `IsError` equals what it was (`ExitCode != 0`, or true for the error kinds).
  - `TestNoStructuredContentWithoutARun` (FR-016): a destructive task not granted, a reached destructive task not granted, an undeclared argument name, an invalid enum value, undecodable arguments, and an engine that returns an error each return `StructuredContent == nil` and the same text as before (pin one literal per kind from the existing tests).
  - `TestToolListingOnlyGainsOutputSchema` (FR-017): marshal each listed tool, delete the `outputSchema` key, and compare with a literal of the listing as it was before this feature for `sampleEngine()` (name, description, inputSchema, annotations).
  - `TestReplaceSameToolsStillSilent` (FR-018): with the output schema in place, `Replace` with equal tasks returns a zero `Change`.
- [X] T010 [P] [US2] In `test/integration/structured_mcp_test.go` add `TestStructuredMCP_RefusalHasNone`: calling the `[confirm]` task `clean` without a grant returns `isError` true, the refusal text, and a raw result with no `structuredContent` key; an undeclared argument does the same.

### Implementation

- [X] T011 [US2] Make T009 and T010 pass. The design of T003 and T007 is expected to cover them; fix what does not.
- [X] T012 [US2] Run the full `go test ./...` in Docker. Every test that existed before this branch passes with no change to its assertions. If one compares a whole result or tool value and now sees the new fields, report it before touching it.

---

## Phase 5: User Story 3 - Structured fields are as safe and as bounded as the text (Priority: P1)

**Goal**: Masking and caps hold in the fields through the real engine.

**Independent Test**: A secret and 3 MB on both streams: the secret is nowhere in the result and
each field is within the limit.

### Tests first

- [X] T013 [P] [US3] Create `internal/cli/mcp_structured_test.go` with tests through the real adapter, a real `mcpserver.New` server and an in-memory client (follow `internal/cli/mcp_output_test.go` and `internal/cli/mcpreload_test.go` for helpers):
  - `TestMCPStructuredMasksSecrets`: with a secret in the environment, a task prints it to stdout and to stderr and fails, and its `||` hook prints it: the marshaled whole result does not contain the secret; `stdout`, `stderr` and `fixSuggestion` each hold `***` where the text does.
  - `TestMCPStructuredCapsStreams`: a task prints 3 MB to stdout and to stderr (the `fill` snippet): each field is at most 65,536 bytes, has exactly one `[truncated N bytes]` marker, and the text rebuilt from the fields equals the text content.
  - `TestMCPStructuredHonorsLimit`: with `set mcp_output_limit := "1KiB"` and again with a flag limit of 2,048 over that setting, a task printing 10,000 bytes per stream gives fields of at most 1,024 and 2,048 bytes.
  - `TestMCPStructuredFixSuggestionCap`: a hook that prints more than 8 KiB gives a `fixSuggestion` equal to the text's section, ending with the existing `[truncated]` marker.
  - `TestMCPStructuredDependencies`: a task with a dependency and a post-hook that print gives fields holding the combined streams.
  - `TestAgentEndpointDeclaresOutputSchema`: a server built with `(&engine{}).agentMCPOptions(0)` lists tools with the output schema and returns structured content.
- [X] T014 [P] [US3] In `mcpserver/structured_test.go` add `TestStructuredCappedForAnyEngine` (FR-019): an engine that returns 200,000 bytes per stream uncapped, on a server with `OutputLimit: 4096`, gives fields of at most 4,096 bytes that equal the stream parts of the text.

### Implementation

- [X] T015 [US3] Make T013 and T014 pass. No production change is expected ([research R5](research.md)); fix what does not hold.
- [X] T016 [US3] Run `go test -race ./mcpserver/ ./internal/cli/` in Docker with `CGO_ENABLED=1`; green.

---

## Phase 6: Polish

- [X] T017 [P] Document the feature in `docs/mcp.md`: a "Structured results" section after the paragraph that explains `[exit N]`, with the four fields, a JSON example, the rule that rebuilds the text, which results carry no structured content, and that each form obeys the output limit. In the "Output limit" section state the bound on a whole result (FR-013a). Link it from the "Output limit" and "Fix suggestions on failure" sections where they describe the result. Keep examples valid for the docs harness. The release note of FR-020 is the commit: `feat(mcp):` with a body that names the fields.
- [X] T018 Run the full gate set in Docker: `go test ./...`, `go test -race ./...` with `CGO_ENABLED=1`, `golangci-lint run` (image `golangci/golangci-lint:v2.14`), `GOOS=windows go vet` on the changed packages, and the docs harness `go test ./test/docs/...`. Fix what fails without weakening a test.
- [X] T019 Check coverage: `go test -cover ./mcpserver/`; `mcpserver/structured.go` reaches 90% of statements.
- [X] T020 Walk [quickstart.md](quickstart.md) against the built binary through the integration test of T006 and confirm the observed values match steps 2 to 5.

---

## Dependencies & Execution Order

- Phase 1, then Phase 2. Phase 2 blocks every story.
- US1 (Phase 3) blocks US2 and US3: they assert properties of the content it adds.
- US2 and US3 are independent of each other. Their [P] test tasks touch different files except
  T009 and T014, which share `mcpserver/structured_test.go`.
- Polish after all stories.

## Implementation Strategy

MVP is Phase 1 to Phase 3. US2 and US3 add no production code by design: they are the proof
that the text is unchanged and that masking and caps hold, which are the issue's second and third
acceptance criteria.
