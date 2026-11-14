---

description: "Task list for spec 042: cap task output before it reaches the agent"
---

# Tasks: Cap Task Output Before It Reaches the Agent

**Input**: Design documents from `/specs/042-mcp-output-cap/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer). Every test that covers new
behavior is written and seen failing before the code that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US4)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- The marker, the size error text, and the flag help are fixed by [contracts/](contracts/). Tests
  pin them as literal strings and do not rebuild them with the production rule.
- A result whose streams are within the limit stays byte-identical. Existing tests that pin result
  text must pass with no change to their assertions.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` on branch `042-mcp-output-cap` and confirm it is green. If anything fails, report it and stop: do not start Phase 2 on a red baseline.

---

## Phase 2: Foundational (the shared buffer)

**Purpose**: `mcpserver.OutputBuffer`, the one bounded head-and-tail capture every story uses
([research R1 to R3](research.md), [data-model.md](data-model.md),
[contracts/mcpserver-api.md](contracts/mcpserver-api.md)). FR-026.

**CRITICAL**: No user story work begins until T004 is green.

### Tests first

- [X] T002 Create `mcpserver/output_test.go` with tests that fail to compile until T003:
  - `TestOutputBufferWhole`: writes of 0 bytes, 1 byte, `limit-1`, and exactly `limit` bytes return the input unchanged (FR-003).
  - `TestOutputBufferTruncates`: 3,000,000 bytes of distinguishable content with limit 65,536 give a string of at most 65,536 bytes that starts with the first 32,748 input bytes, ends with the last 32,748 input bytes, and has `"\n[truncated 2934504 bytes]\n"` between them (FR-004 to FR-007; `headMax = (limit - 40) / 2`, `tailMax = limit - 40 - headMax`).
  - `TestOutputBufferOneByteOver`: `limit+1` bytes are truncated, the result is at most `limit`, and shown bytes plus N equal `limit+1`.
  - `TestOutputBufferMarkerArithmetic`: for totals `limit+1`, `limit+39`, `limit+41`, `2*limit`, `10*limit+7` and limits 1,024, 1,025, 4,096, 65,536: `len(head)+len(tail)+N == total`, rendered length `<= limit`, head is a prefix and tail a suffix of the input.
  - `TestOutputBufferChunkingInvariant`: the same input written at once, byte by byte, and in seeded random chunk sizes (including chunks larger than the limit) gives identical strings.
  - `TestOutputBufferUTF8`: input of 3-byte and 4-byte characters sized so each cut falls inside a character: the result is valid UTF-8 (`utf8.ValidString`) and the arithmetic above still holds (FR-008).
  - `TestOutputBufferBinary`: input of `0xff` and `0x80` bytes is capped to at most `limit` with the arithmetic holding.
  - `TestOutputBufferLooksLikeMarker`: input that contains `[truncated 5 bytes]` and fits the limit is returned unchanged.
  - `TestOutputBufferMemoryBound`: in a loop writing 200 MB in 32 KiB chunks, after each write the internal slice capacity is at most the limit (FR-015, SC-004). Also for a small output the capacity is well under the limit (no up-front allocation).
  - `TestOutputBufferConcurrent`: 8 goroutines each write 1 MB; the result is at most `limit` and the arithmetic holds; run under `-race` (FR-016).
  - `TestOutputBufferStringRepeatable`: `String()` called twice, and again after a further write, is consistent.
  - `TestOutputBufferWriteReturns`: every `Write` returns `len(p), nil` (FR-017).
  - `TestNewOutputBufferClamps`: limits `0` and `-5` give 65,536; `10` gives 1,024; `1<<31` gives `1<<30` (research R8). Pin the constants `DefaultOutputLimit == 65536`, `MinOutputLimit == 1024`, `MaxOutputLimit == 1073741824`.

### Implementation

- [X] T003 Create `mcpserver/output.go` with the constants `DefaultOutputLimit = 64 << 10`, `MinOutputLimit = 1 << 10`, `MaxOutputLimit = 1 << 30`, an unexported `clampLimit(int) int` ("zero or less means `DefaultOutputLimit`; other values are raised to `MinOutputLimit` or lowered to `MaxOutputLimit`"), and `OutputBuffer` with `NewOutputBuffer(limit int) *OutputBuffer`, `Write`, and `String` exactly as in [contracts/mcpserver-api.md](contracts/mcpserver-api.md). Storage per [research R2](research.md#r2-storage-one-region-of-at-most-limit-bytes-head-fixed-tail-a-ring): one slice whose capacity never passes `limit`, growth doubling but capped at `limit`, in-place compaction on the first overflow, the tail region a ring, a 40-byte marker reserve, `total` as `int64`, and one mutex. Rendering per [research R3](research.md#r3-rendering).
- [X] T004 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/` and confirm T002's tests pass.

**Checkpoint**: The buffer exists and nothing uses it. All other tests are unchanged.

---

## Phase 3: User Story 1 - A large output no longer floods the agent (Priority: P1) MVP

**Goal**: Every tool result carries at most the limit per stream, head and tail kept, marker
between, exit line intact (FR-001 to FR-012, FR-027).

**Independent Test**: [quickstart](quickstart.md) scenarios A to D.

### Tests first

- [X] T005 [P] [US1] In `mcpserver/handler_test.go`, add tests that fail until T007, using a plain `Engine` that returns fixed strings (the embedder case, FR-027):
  - `TestFormatResultCapsStdout`: 3,000,000 bytes of stdout, default options: the tool result text is at most 65,568 bytes, starts with the first input bytes, holds exactly one line matching `^\[truncated \d+ bytes\]$`, and ends with the last input bytes, a line break, and `[exit 0]`.
  - `TestFormatResultCapsBothStreams`: 3,000,000 bytes on each: at most 131,104 bytes, two marker lines, ends `[exit 0]` (FR-009).
  - `TestFormatResultExitAndErrorFlag`: 3 MB with exit code 3: `IsError` true, text ends `[exit 3]`; exit code 0: `IsError` false (FR-010, FR-012).
  - `TestFormatResultFixSuggestionAfterExit`: 3 MB plus a `FixSuggestion`: the text ends with `[exit 1]\n[fix suggestion - from Runefile || failure hooks]\n<suggestion>` unchanged (FR-011).
  - `TestFormatResultWithinLimitUnchanged`: 60,000 bytes on each stream gives exactly the text `formatResult` gave before this feature (FR-003, SC-005).
  - `TestOptionsOutputLimit`: `Options{OutputLimit: 1024}` caps stdout to at most 1,024 bytes; `Options{OutputLimit: 262144}` returns more than 65,536 bytes of a 3 MB stream; `Options{}` uses 65,536; `Options{OutputLimit: 10}` behaves as 1,024.
  - In `mcpserver/transport_test.go`, add `TestHTTPResultIsCapped`: a server with `Options{OutputLimit: 1024}` over the HTTP transport returns a tool result of at most 1,056 bytes for an engine that returns 10,000 bytes of stdout (FR-018: the limit holds on HTTP as on stdio).
- [X] T006 [P] [US1] In `internal/cli/mcp_test.go`, add tests that fail until T008, calling `mcpAdapter.Call` on a Runefile built in the test:
  - `TestMCPCallCapsStdout` and `TestMCPCallCapsStderr`: a task printing about 3 MB (the shell-builtin doubling loop from [quickstart](quickstart.md)) returns `Result.Stdout` (or `Stderr`) of at most 65,536 bytes that starts with `BEGIN`, ends with `END\n`, and holds one marker whose N plus the shown bytes equals the bytes printed.
  - `TestMCPCallSmallOutputUnchanged`: a task printing 60,000 bytes returns exactly those bytes.
  - `TestMCPCallCapsDependencyOutput`: a task with a dependency and a post-hook that each print 1 MB returns one capped stream starting with the dependency's first bytes and ending with the hook's last bytes (spec edge case: combined stream).

### Implementation

- [X] T007 [US1] In `mcpserver/server.go`, add `Options.OutputLimit int` with the doc comment from [contracts/mcpserver-api.md](contracts/mcpserver-api.md) and store the clamped value on `Server` in `New`. In `mcpserver/handler.go`, give `formatResult` the limit and pass `r.Stdout` and `r.Stderr` through an unexported `capOutput(s string, limit int) string` that returns `s` itself when `len(s) <= limit` and otherwise the `OutputBuffer` rendering ([research R4](research.md#r4-the-handler-caps-every-result-the-total-is-twice-the-limit-plus-32-bytes)). Add a unit test for `capOutput` in `mcpserver/output_test.go`. Update existing `formatResult` callers in tests to pass a limit without changing their expected strings.
- [X] T008 [US1] In `internal/cli/mcp.go`, add `outputLimit int` to `mcpAdapter` and an unexported method `limit() int` returning `outputLimit` when positive, else `settings.MCPOutputLimit` when positive, else `mcpserver.DefaultOutputLimit` (the settings field arrives in T023; until then read only `outputLimit`). In `CallLists`, replace the two `bytes.Buffer` values and their `syncWriter` wrappers with `mcpserver.NewOutputBuffer(a.limit())` for each stream, keeping `maskOptions` above them and `flushMask()` before `String()` ([research R5](research.md#r5-masking-stays-upstream)). Fix the comment above them. Remove the `bytes` import if unused. Add a unit test for `limit()`.
- [X] T009 [US1] Create `test/integration/output_cap_mcp_test.go` with the scratch Runefile of [quickstart](quickstart.md) and `TestOutputCap_FloodIsCapped` (scenario A), `TestOutputCap_BothStreams` (B), `TestOutputCap_FailKeepsExitLine` (C), and `TestOutputCap_SmallUnchanged` (D), using the existing `mcpCall` and `mcpServerCall` helpers in `test/integration/secret_masking_mcp_test.go`. This is the equivalent of the issue's `TestMCPE2E_Pending_OutputCapped`.
- [X] T010 [US1] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/ ./internal/cli/ ./test/integration/` and confirm it is green.

**Checkpoint**: Scenarios A to D pass with the default limit.

---

## Phase 4: User Story 2 - Secrets stay masked in a truncated result (Priority: P1)

**Goal**: No part of a secret reaches the result, wherever the cuts fall (FR-013, FR-014).

**Independent Test**: [quickstart](quickstart.md) scenario K.

- [X] T011 [P] [US2] In `internal/cli/mcp_test.go`, add `TestMCPCallMasksAcrossCuts`: with a 24-byte secret in the adapter's environment under a name the mask patterns match, and a small `outputLimit` (1,024), run a table of tasks that print `pad` filler bytes, the secret, then filler to about 20 KiB, for every `pad` from `headMax-30` to `headMax+6` (secret across the end of the head), the matching range for the start of the tail, one with the secret inside the dropped middle, and one each inside the head and the tail. Assert the result holds neither the secret nor any 4-byte substring of it, and that the head and tail cases show `***`. The filler must not contain any 4-byte substring of the secret.
- [X] T012 [P] [US2] In `test/integration/output_cap_mcp_test.go`, add `TestOutputCap_SecretAtTheCuts` (scenario K) with the real binary and the default limit: size the filler so the secret straddles the end of the head (byte 32,748) and, in a second task, the start of the tail (32,748 bytes before the end). Assert the secret and every 4-byte part of it are absent.
- [X] T013 [US2] Run the two tests in Docker. They are expected to pass with no production change, because masking is upstream of the buffer (research R5). If either fails, fix the capture order in `internal/cli/mcp.go`, never the test.

**Checkpoint**: Scenario K passes.

---

## Phase 5: User Story 3 - The server's memory stays bounded while a task runs (Priority: P1)

**Goal**: The capture never holds more than the limit per stream, also under concurrent writers
(FR-015 to FR-017).

**Independent Test**: The memory test in `mcpserver/output_test.go` (T002) plus the tests below.

- [X] T014 [P] [US3] In `internal/cli/mcp_test.go`, add `TestMCPCallParallelWritersCapped`: a `[parallel]` task whose three dependencies each print about 1 MB returns a stream of at most the limit with one marker and correct arithmetic; run under `-race` (FR-016).
- [X] T015 [P] [US3] In `internal/cli/mcp_test.go`, add `TestMCPCallSingleLongLine`: a task printing one 3 MB line with no line break returns at most the limit, with the marker on its own line (spec US3 scenario 3).
- [X] T016 [US3] In `internal/cli/mcp_test.go`, add `TestMCPCallLongRunStaysCapped`: a task that prints a 1 MiB string 64 times in a loop (67,108,864 bytes, never held as one string) completes and returns a stream of at most the limit whose marker N plus the shown bytes equals 67,108,864. Skip under `-short`. The memory bound itself is proven at the buffer (`TestOutputBufferMemoryBound`, T002); this test proves the adapter writes through that buffer for a long run. Also add `TestMCPCallCancelledStaysCapped`: a context cancelled while a task prints in an endless loop gives a `Result` (or an error) and, when a result, streams of at most the limit (spec edge case).
- [X] T017 [US3] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/ ./internal/cli/` and confirm it is green.

**Checkpoint**: Memory is bounded in the buffer and through the adapter.

---

## Phase 6: User Story 4 - The limit can be changed (Priority: P2)

**Goal**: `--output-limit` and `set mcp_output_limit`, flag over setting over default, invalid
values rejected before any task runs (FR-018 to FR-025).

**Independent Test**: [quickstart](quickstart.md) scenarios E to J.

### Tests first

- [X] T018 [P] [US4] Create `internal/config/size_test.go` with `TestParseOutputLimit`: accepted `65536`, `065536`, `65536B`, `64KiB` (65,536), `256KiB` (262,144), `1MiB` and `1024KiB` (1,048,576), `1024` (1,024), `1024MiB` (1,073,741,824); rejected `""`, `lots`, `-1`, `+5KiB`, `0`, `10`, `1023`, `1.5MiB`, `64 KiB`, ` 64KiB`, `64kib`, `64KB`, `64K`, `1GiB`, `1025MiB`, `KiB`, `99999999999999999999`, `9999999999999MiB`. Every rejection's error text is exactly `invalid size "<value>": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`.
- [X] T019 [P] [US4] In `internal/config/settings_test.go`, add `TestResolveSettingsMCPOutputLimit`: `set mcp_output_limit := "256KiB"` gives `Settings.MCPOutputLimit == 262144` and no diagnostics; absent gives 0; `"lots"` gives one diagnostic whose message is `invalid mcp_output_limit: invalid size "lots": use ...` (the full text of T018) and whose span is the value's span; a repeated setting is the analyzer's `duplicate setting` error, so it is not tested here.
- [X] T020 [P] [US4] In `internal/cli/serve_test.go`, add:
  - `TestServeMCPRejectsBadOutputLimit`: for `lots`, `""`, `-1`, `0`, `10`, `ServeMCP` returns a `*UsageError` whose text is `--output-limit: invalid size "<value>": use ...`, and a `[context]` hook that writes a marker file has not run (FR-023).
  - `TestResolveOutputLimit`: the unexported helper from T022 returns the flag's value when given, else the setting's, else 65,536 (FR-020).
- [X] T021 [P] [US4] In `internal/cli/mcp_test.go`, add `TestAgentEndpointUsesSettingLimit`: `agentMCPOptions()` on an engine whose settings hold `MCPOutputLimit: 2048` returns `OutputLimit: 2048`, the adapter from `newAgentAdapter()` has `limit() == 2048`, and with no setting both give the default.

### Implementation

- [X] T022 [US4] Create `internal/config/size.go` with `ParseOutputLimit(s string) (int, error)` per [research R6](research.md#r6-size-syntax-and-range-live-in-internalconfig) and [contracts/cli-and-setting.md](contracts/cli-and-setting.md): digits only, then nothing, `B`, `KiB`, or `MiB`; range `mcpserver.MinOutputLimit` to `mcpserver.MaxOutputLimit`; overflow is a rejection, not a panic or wrap.
- [X] T023 [US4] In `internal/config/settings.go`, add `MCPOutputLimit int` to `Settings` with the comment `// set mcp_output_limit := "256KiB" (0 when absent)` and a `case "mcp_output_limit"` that evaluates the value, parses it, and on error adds `diag.New(set.Value.Span(), "invalid mcp_output_limit: "+err.Error())`. In `internal/language/builtin.go`, add the entry `{Name: "mcp_output_limit", Kind: BuiltinSetting, Signature: "set mcp_output_limit := \"256KiB\"", Documentation: "Largest stdout and stderr, each, that a tool result returns to an agent (default 64KiB)."}` and update any test or golden that lists the settings. In `internal/cli/mcp.go`, complete `limit()` to read the setting (T008).
- [X] T024 [US4] In `internal/cli/dispatch.go`, add `MCPOutputLimit *string` with a comment that nil means the flag was not given and that only `ServeMCP` reads it. In `internal/cli/serve.go`, parse it at the top of `ServeMCP` beside the task-list flags (`usagef("--output-limit: %v", err)`), then after the adapter is built set `adapter.outputLimit` through an unexported `resolveOutputLimit(flag int, settings config.Settings) int` and pass the same number as `mcpserver.Options.OutputLimit`. In `internal/cli/agentexec.go`, make `agentMCPOptions` return `OutputLimit: e.settings.MCPOutputLimit` and extend its comment.
- [X] T025 [US4] In `cmd/rune/serve.go`, add a string flag `--output-limit` with the help text `cap each of stdout and stderr in a tool result (default 64KiB; such as 256KiB or 1MiB)`, set `opts.MCPOutputLimit` only when `cmd.Flags().Changed("output-limit")`, add a paragraph to `Long` and the example line `rune mcp --output-limit 256KiB                      # return up to 256 KiB of each stream`. Add or extend the command test in `cmd/rune` that covers serve flags so it checks the flag reaches `opts`, including the given-but-empty case and the flag given twice (the last value is used).
- [X] T026 [US4] In `test/integration/output_cap_mcp_test.go`, add `TestOutputCap_FlagRaisesLimit` (E), `TestOutputCap_SettingLowersLimit` (F), `TestOutputCap_FlagBeatsSetting` (G), `TestOutputCap_BadFlagRunsNothing` (H, with a `[context]` hook marker file and exit status 2), `TestOutputCap_BadSettingIsDiagnosed` (I, asserting `Runefile:<line>:<col>` and a caret line), `TestOutputCap_CommandLineIsNotCapped` (J), and `TestOutputCap_EquivalentSpellings` (`65536`, `64KiB`, and no flag give identical results).
- [X] T027 [US4] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/config/ ./internal/cli/ ./internal/language/ ./cmd/... ./test/integration/` and confirm it is green.

**Checkpoint**: Scenarios E to J pass.

---

## Phase 7: Polish and cross-cutting

- [X] T028 [P] In `docs/mcp.md`, add an "Output limit" section: the default, what a truncated result looks like (the example from [spec.md](spec.md) Overview), the flag, the setting, precedence, the size syntax and range, that the `agent` task endpoint follows the setting, and that the fix suggestion keeps its own 8 KiB limit. Add one line to the security or behavior list that masking applies before truncation.
- [X] T029 [P] In `docs/cli.md`, add `[--output-limit <size>]` to the `rune serve` row and one sentence describing it.
- [X] T030 [P] In `docs/runefile.md` (settings block and a short paragraph) and `docs/GRAMMAR.md` (a "MCP output limit setting" section with the size grammar from [contracts/cli-and-setting.md](contracts/cli-and-setting.md)), document `set mcp_output_limit` (FR-025).
- [X] T031 Search for other places that list settings or `rune serve` flags (`grep -rn "allow-destructive\|\"unmasked\"\|set unmasked" --include='*.go' --include='*.md' --include='*.json' --include='*.yaml' . --exclude-dir=specs --exclude-dir=node_modules --exclude-dir=dist`): editor grammars, completion scripts, website sources, golden files, and the compatibility corpus in `test/corpus`. Update each that enumerates them, and add a corpus or docs fixture that uses `set mcp_output_limit` if settings have fixtures there.
- [X] T032 Run the full gate set in Docker: `go test ./...`, `go test -race ./...` (with `CGO_ENABLED=1`), `go vet ./...`, the docs harness (`go test ./test/docs/...`), and `golangci-lint run` with the version CI pins. Regenerate goldens only if a listing legitimately changed, and say which.
- [X] T033 Walk [quickstart](quickstart.md) scenarios A, E, and H by hand with a built binary over stdio and record the measured sizes. Measured on 2026-11-13: A is 65,531 bytes with `[truncated 3211315 bytes]`; E is 262,139 bytes with `[truncated 3014707 bytes]`; H prints the size error and exits 2.

---

## Dependencies

- Phase 2 blocks everything.
- US1 (Phase 3) needs Phase 2. US2 and US3 need T008 (the adapter capture). US4 needs T007 and T008.
- Polish needs US4 for the names it documents.

## Parallel examples

- T005 and T006 (different packages).
- T011, T014, T015 are in one file: write them in sequence, run together.
- T018, T019, T020, T021 (tests in different files, except T021 which shares `mcp_test.go`).
- T028, T029, T030 (different documents).

## Implementation strategy

1. MVP: Phases 1 to 3. The default 64 KiB cap alone closes the defect in the issue.
2. Phases 4 and 5 prove the two P1 guarantees (masking, memory) that the MVP already provides by
   construction.
3. Phase 6 adds configuration. Phase 7 documents it and runs every gate.

## Requirement coverage

| Requirement | Tasks |
|---|---|
| FR-001, FR-002 | T005, T006, T007, T008, T009 |
| FR-003 | T002, T005, T006, T009 |
| FR-004 to FR-008 | T002, T003 |
| FR-009 to FR-012 | T005, T007, T009 |
| FR-013, FR-014 | T011, T012, T013 |
| FR-015 to FR-017 | T002, T003, T014, T015, T016 |
| Edge cases (cancelled run, repeated flag, combined stream) | T006, T016, T025 |
| FR-018 | T005, T024, T025, T026 |
| FR-019 | T019, T023, T026 |
| FR-020 | T020, T024, T026 |
| FR-021, FR-022 | T018, T022 |
| FR-023 | T020, T024, T026 |
| FR-024 | T019, T023, T026 |
| FR-025 | T028, T029, T030, T031 |
| FR-026 | T003, T008 |
| FR-027 | T005, T007 |
| FR-028 | commit subject (`feat(mcp)!`), noted in the PR |
