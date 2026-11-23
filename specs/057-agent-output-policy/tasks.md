---

description: "Task list for spec 057: per-task output tails and failure summaries for agents"
---

# Tasks: Per-Task Output Tails and Failure Summaries for Agents

**Input**: Design documents from `/specs/057-agent-output-policy/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and spec FR-039 ask for them. In every phase
the test tasks come first, and a test is written and seen to fail before the change that
makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on
  an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US4)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of the new code.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- A task without `[agent-output]` and `[agent-summary]` must keep today's path: the engine
  still uses `mcpserver.OutputBuffer`, and `Result.Output` and `Result.Failure` stay nil.
- Golden and fixture files are regenerated with `-update` where the package supports it,
  never edited by hand.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

**Purpose**: a known-good baseline.

- [X] T001 Run `go test ./mcpserver/... ./internal/cli/... ./internal/parser/... ./internal/analyzer/... ./internal/formatter/... ./internal/language/... ./test/...` in Docker on the branch before any change and record that it passes, so later failures are known to be new

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the two attributes exist in the language, are validated, formatted and known to
the editor, and `mcpserver.Result` can carry the new values. Every story needs this.

### Tests for the foundation

- [X] T002 [P] Parser tests in `internal/parser/attribute_test.go` (or the file that tests `[agent-allow]` parsing): `[agent-output(tail="50")]` and `[agent-output(head="10", tail="40")]` give an attribute of kind `agent-output` whose `Allow` holds the pairs in source order; `[agent-summary]` gives a bare attribute; `[agent-summary("x")]` is a parse error like any bare attribute with an argument; `TestParserKnowsAllASTAttributes` passes with the two new names
- [X] T003 [P] Analyzer tests in `internal/analyzer/agentoutput_test.go`, one case per row of the diagnostics table in `contracts/runefile-attributes.md`, each asserting code `RUNE2021`, the exact message and the span (the name, the value, or the attribute): unknown argument `lines` (message lists `head, tail`), duplicate `tail`, no `tail` (`agent-output(...) requires tail="<lines>"`), each rejected value `""`, `"0"`, `"-1"`, `"+5"`, `"5k"`, `" 5"`, `"1.5"`, `"10001"`, `"999999"` (`agent-output tail "5k" must be a whole number of lines from 1 to 10000`), a second `[agent-output]` and a second `[agent-summary]` with the related location `first [...] declared here`; and valid cases `tail="1"`, `tail="10000"`, `tail="050"`, `tail="40", head="10"`, `[agent-summary]` alone, both attributes together, both on a private task (no diagnostic)
- [X] T004 [P] Unit tests in `internal/ast/agent_output_test.go`: `AgentOutputLines` accepts 1 to 5 decimal digits with a value from 1 to 10,000 (`"050"` is 50) and rejects every value of T003; `(*Task).AgentOutput()` returns `(0, 50, true)` for `tail="50"`, `(10, 40, true)` for `head="10", tail="40"`, and `ok == false` with no attribute; `(*Task).AgentSummary()` reports the bare attribute
- [X] T005 [P] Formatter and dump fixtures: `testdata/fmt/agent-output.rune` and `testdata/fmt/agent-output.fmt` (both attributes, `head` and `tail` in both orders, kept as written), `testdata/corpus/agent-output.rune` and `testdata/corpus/agent-output.ast` (the dump prints `Attr agent-output(head="10", tail="40")` and `Attr agent-summary`); generate the `.fmt` and `.ast` files with the packages' `-update` flag and read them before keeping them
- [X] T006 [P] Language registry tests in `internal/language/hover_test.go` and `internal/language/completion_test.go`: hover on `agent-output` and `agent-summary` shows the signature and documentation of `contracts/runefile-attributes.md`; attribute completion offers both; `TestRegistryMatchesASTAttributes` passes

### Implementation of the foundation

- [X] T007 Add `AttrAgentOutput = "agent-output"` and `AttrAgentSummary = "agent-summary"` with a comment naming spec 057, append both to `KnownAttributes`, add `AgentOutputLines(text string) (int, error)` (1 to 5 decimal digits, value 1 to 10,000), `(*Task).AgentOutput() (head, tail int, ok bool)` and `(*Task).AgentSummary() bool` in `internal/ast/ast.go`; print `agent-output` like `agent-allow` in `internal/ast/dump.go`
- [X] T008 Parse `agent-output` with `parseNamedStringArgs(a, "an argument name", "a number of lines")` and add `agent-summary` to the bare-attribute case in `internal/parser/attribute.go`
- [X] T009 Add `CodeInvalidAgentOutput = "RUNE2021"` with a comment that lists its cases in `internal/diag/codes.go`; add `checkAgentOutput` in `internal/analyzer/agentoutput.go` with the messages of `contracts/runefile-attributes.md` (reusing `firstOf`'s pattern for the duplicate attributes, with code RUNE2021), and call it from the analyzer's task checks in `internal/analyzer/analyzer.go`
- [X] T010 [P] Print `agent-output` with `ast.FormatAllow` like `agent-allow` in `internal/formatter/formatter.go`
- [X] T011 [P] Add the two entries of `contracts/runefile-attributes.md` (name, kind `BuiltinAttribute`, signature, documentation) to `internal/language/builtin.go`
- [X] T012 [P] Add `agent-output` and `agent-summary` to the attribute pattern of `editors/vscode/syntaxes/runefile.tmLanguage.json`
- [X] T013 Add `Output *OutputReport` and `Failure *FailureSummary` to `Result` in `mcpserver/server.go`, the type `StreamReport{Lines, Bytes, KeptLines, KeptBytes int64}` in `mcpserver/linebuffer.go`, and the types `OutputReport{Head, Tail int; Stdout, Stderr StreamReport}` and `FailureSummary{Kind, Task, FailedTask, File string; Line int; Command, Reason string; FixSuggestion bool; StdoutTail, StderrTail string}` with the JSON names of `data-model.md` (`omitempty` on every field that is not always present) in `mcpserver/summary.go`; doc comments say which fields the engine sets and which the server sets
- [X] T014 Run T002 to T006 and the existing tests of the five front-end packages; all pass

**Checkpoint**: the attributes parse, validate, format and complete; nothing reads them yet.

---

## Phase 3: User Story 1 - Keep only the last lines of a noisy task for agents (Priority: P1) MVP

**Goal**: `[agent-output(tail="T")]` makes each stream of an agent's result its last T lines
with one omission line, within the output limit, with the outcome intact.

**Independent Test**: a task that prints 2,000 numbered lines with `tail="50"`, called over
MCP: lines 1951 to 2000, `[omitted 1950 lines, N bytes]`, `[exit 0]`; from the command line,
all 2,000 lines.

### Tests for User Story 1

- [X] T015 [P] [US1] Table tests of a tail-only `LineBuffer` in `mcpserver/linebuffer_test.go`, each asserting `String()` and `Report()` against values written in the test: empty stream (`""`, zero report); fewer lines than `tail` (whole, no omission line); exactly `tail` lines; `tail` + 1 lines (`[omitted 1 line, N bytes]`); the 2,000-line example of `contracts/mcp-results.md` with `tail="3"` (`[omitted 1997 lines, 18863 bytes]`); no final line feed (the last line counts and is kept without a line feed added); CR LF lines (the CR stays in the line); a lone-CR progress line (one line); a single line of 1 MB with limit 1 KiB (result at most 1,024 bytes, ends with the last byte written, `keptLines == 1`, omitted bytes counted); a multi-byte UTF-8 character at the cut for each of 2, 3 and 4 byte characters (never split); bytes that are not valid UTF-8 (kept as they are); `total == L` with fewer lines than `tail` (whole); `total == L + 1`
- [X] T016 [P] [US1] Property tests in `mcpserver/linebuffer_test.go`: for random streams and random splits into writes, `String()` and `Report()` equal those of one write of the whole stream; `len(String()) <= L`; `Report().KeptBytes + len(omission line) == len(String())`; `Bytes - KeptBytes` and `Lines - KeptLines` are the numbers in the omission line, which exists exactly when `KeptBytes < Bytes`
- [X] T017 [P] [US1] Bound and concurrency tests in `mcpserver/linebuffer_test.go`: 50 MB written in 4 KiB chunks with the default limit, checking after every write that the memory the buffer holds (`len` of the head region plus `cap` of the ring) is at most the limit; 8 goroutines writing whole lines concurrently under `-race`, checking the line and byte totals of the report
- [X] T018 [P] [US1] Engine tests in `internal/cli/mcpoutput_test.go` through `mcpAdapter.Call`: a task with `[agent-output(tail="50")]` printing 2,000 lines on stdout and 300 on stderr gives each stream its own last 50 lines and omission line and `Result.Output` with `Head 0, Tail 50` and both reports; the same task printing 30 lines gives whole streams and no omission line; a failing task with exit 3 after 10,000 lines keeps `ExitCode == 3`; a task without the attribute gives the same `Result` as before this feature (`Output == nil`, streams equal to an `OutputBuffer` capture); a task without the attribute that depends on a task with `[agent-output(tail="1")]` gives that same unchanged `Result` (FR-006)
- [X] T019 [P] [US1] Masking tests in `internal/cli/mcpoutput_test.go`: with a secret in the environment, a task with `tail="2"` that prints the secret on the last line, on an omitted line, and split across the boundary between the omitted part and the kept part; no kept stream holds the secret or a part of it longer than 3 bytes
- [X] T020 [P] [US1] Command-line test in `internal/cli/mcpoutput_test.go` (or the run tests' file): `rune <task>` of a task with `[agent-output(tail="2")]` prints every line

### Implementation for User Story 1

- [X] T021 [US1] Implement `LineBuffer` for `head == 0` in `mcpserver/linebuffer.go`: `NewLineBuffer(limit, head, tail int) *LineBuffer` (limit clamped as `OutputBuffer`'s), a mutex, a ring of the last `L` bytes grown as needed and never past `L`, counters of bytes and line feeds and the last byte, `Write` that always returns `len(p), nil`, `String()` and `Report()` by the rule of `contracts/mcp-results.md` with the omission line `[omitted N lines, M bytes]` (singular `1 line`, `1 byte`) and the reserve of 64 bytes, cuts at character boundaries with the `trimPartialRune` and `utf8.RuneStart` rules `OutputBuffer` uses
- [X] T022 [US1] Add `outputPolicy(t *ast.Task) (head, tail int, ok bool)` (reading `t.AgentOutput()`) and `newStreamBuffer(limit int, t *ast.Task)` that returns a `*mcpserver.LineBuffer` for a task with a policy and a `*mcpserver.OutputBuffer` otherwise, in `internal/cli/mcpoutput.go`
- [X] T023 [US1] In `call` of `internal/cli/mcp.go`, capture both streams with `newStreamBuffer` for the called task `t`, and when it has a policy set `Result.Output` from the policy and the two buffers' `Report()` after the flush; nothing else in `call` changes
- [X] T024 [US1] Run T015 to T020 and `go test -race ./mcpserver/... ./internal/cli/...`; all pass

**Checkpoint**: US1 works alone: a tail policy trims each stream for agents and only for
agents.

---

## Phase 4: User Story 2 - Learn which step failed without reading all the output (Priority: P1)

**Goal**: `[agent-summary]` adds the failure summary section of `contracts/mcp-results.md`
to a failed call, built from what Rune observed, bounded, masked, never on success.

**Independent Test**: a task with `[agent-summary]` whose second body line runs `exit 4`:
the summary names the task, the Runefile line, `exit 4` and `reason: exit status 4`, and
quotes the last lines; a succeeding call has no summary.

### Tests for User Story 2

- [X] T025 [P] [US2] Rendering tests in `mcpserver/summary_test.go` for each kind of the contract table (`step`, `script`, `error`, `cancelled`, `unknown`), with and without `failedTask`, with and without a fix suggestion, with stdout or stderr or both empty: the exact section text, line by line, and its place between `[exit N]` and the fix suggestion section in `renderText`
- [X] T026 [P] [US2] Bounding tests in `mcpserver/summary_test.go`: a line feed, a NUL and DEL in `command` become spaces and a tab stays; invalid UTF-8 becomes U+FFFD; `task` of 300 bytes is at most 128 bytes in all and ends with ` [cut N bytes]` where `N` is the number of bytes not shown; `command` of 1,000 bytes with a 3-byte character across byte 256 is cut before it; `reason` capped at 512; a tail of 5 lines of 400 bytes each is at most 1,024 bytes, marker line included, and ends with the stream's last byte; a stream of 20 lines quotes the last 5; a stream ending in an omission line quotes it; the rendered section is at most 4,096 bytes with every field at its cap
- [X] T027 [P] [US2] Kept-result tests in `mcpserver/summary_test.go`: a `Result` with `ExitCode == 0` and a `Failure` renders no summary and no `failureSummary`; `FixSuggestion` of the summary is set by the server from `Result.FixSuggestion` whatever the engine set; tails are computed from the capped streams; `isError` follows the exit code
- [X] T028 [P] [US2] Failure fact tests in `internal/cli/mcpoutput_test.go` through `mcpAdapter.Call`: the third body line fails with exit 2 (kind `step`, file `Runefile`, line of that body line, the command as run, `reason exit status 2`, failed task the task); a dependency `build` fails (failed task `build`, task the called one); a `[script]` task fails (kind `script`, file and line of its span); a body that interpolates `env()` of a variable that is not set (kind `error`, reason `environment variable not set: <name>`, no step); a cancelled context while the body sleeps (kind `cancelled`, `isError`, the exit status of an interrupt); exit status not 0 with no run error (kind `unknown`); a task in an imported module (file relative to the Runefile's directory with forward slashes); a success (no `Failure`)
- [X] T029 [P] [US2] Masking tests in `internal/cli/mcpoutput_test.go`: a secret interpolated into the failing command and into an error message never reaches `Command`, `Reason`, the tails or the rendered text
- [X] T030 [P] [US2] Fix suggestion test in `internal/cli/mcpoutput_test.go`: a task with `[agent-summary]` and a `||` hook that prints a suggestion gives `fix suggestion: follows, from the task's || failure hooks` in the summary and the fix suggestion section after it, unchanged

### Implementation for User Story 2

- [X] T031 [US2] Implement in `mcpserver/summary.go`: `boundSummary` (control characters other than tab to spaces, invalid UTF-8 to U+FFFD, then the caps 128, 128, 256, 256, 512 with ` [cut N bytes]`), `lastLines(stream string) string` (the last up to 5 lines, then at most 1,024 bytes from the end with the leading line `[cut N bytes]`), and `renderSummary(f FailureSummary) string` producing the lines of `contracts/mcp-results.md` in order, with no final line feed
- [X] T032 [US2] In `mcpserver/handler.go`, extend `capResult` to drop `Failure` when `ExitCode == 0` and otherwise bound it, set `FixSuggestion` from `Result.FixSuggestion` and the tails from the capped streams; make `renderText` write a line feed and the summary section after `[exit N]` and before the fix suggestion section
- [X] T033 [US2] Add `failureFacts(ctx context.Context, called *ast.Task, runErr error, code int, root string, mask func(string) string) *mcpserver.FailureSummary` in `internal/cli/mcpoutput.go`, deciding the kind in the order of the contract (cancelled run context, `*shell.ExecError`, `*interp.ExecError`, other error, none), masking `Command` and `Reason` with `mask` before returning, the file made relative to `root` with forward slashes when inside it
- [X] T034 [US2] In `call` of `internal/cli/mcp.go`, set `Result.Failure` from `failureFacts` when the called task has `[agent-summary]` and `code != 0`, using the run error before classification for the facts and `a.maskText` for masking
- [X] T035 [US2] Run T025 to T030 and `go test -race ./mcpserver/... ./internal/cli/...`; all pass

**Checkpoint**: US2 works alone or with US1.

---

## Phase 5: User Story 3 - Keep the beginning and the end (Priority: P2)

**Goal**: `head="H"` keeps the first H lines too, within half of the stream's budget.

**Independent Test**: 500 numbered lines with `head="10", tail="40"`: lines 1 to 10,
`[omitted 450 lines, N bytes]`, lines 461 to 500.

### Tests for User Story 3

- [X] T036 [P] [US3] Table tests with `head > 0` in `mcpserver/linebuffer_test.go`: the `head="2", tail="2"` example of the contract (lines `a` to `f`, `[omitted 2 lines, 4 bytes]`); 500 lines with `head="10", tail="40"`; 45 lines (whole); exactly `head + tail` lines (whole); a head line longer than `hb` (head cut at a character boundary, a line feed before the omission line); one long line with `head="1", tail="1"` (the line counted once in `keptLines`); `total` between `L - 64` and `L` with more than `head + tail` lines (result at most `L`)
- [X] T037 [P] [US3] Extend the property and bound tests of T016 and T017 to random `head` values, checking the memory bound `len(head region) + cap(ring) <= L`
- [X] T038 [P] [US3] Engine test in `internal/cli/mcpoutput_test.go`: `[agent-output(head="10", tail="40")]` on a 500-line task gives the expected stdout and `Result.Output` with `Head 10, Tail 40`

### Implementation for User Story 3

- [X] T039 [US3] Add the head region to `LineBuffer` in `mcpserver/linebuffer.go`: `hb = (L - 64) / 2` when `head > 0`, the first `hb` bytes kept, the ring sized `L - hb`, and `String()` and `Report()` extended with the head part, the line feed before the omission line, and the single count of a line both parts cut
- [X] T040 [US3] Run T036 to T038 and the US1 tests again; all pass

**Checkpoint**: US1 to US3 work together.

---

## Phase 6: User Story 4 - Read what was kept from structured fields (Priority: P2)

**Goal**: `output` and `failureSummary` in the structured content and in the one output
schema, equal to the text.

**Independent Test**: a task with both attributes that prints 2,000 lines and fails: the
structured content validates against the listed schema, and every count and fact matches the
text.

### Tests for User Story 4

- [X] T041 [P] [US4] Schema tests in `mcpserver/structured_test.go`: the schema of `contracts/mcp-results.md` with `output` and `failureSummary` written out in full (no `$ref`), closed objects, the `kind` enum, required fields, one schema for every tool (a tool whose task has both attributes and one whose task has neither list identical schemas, so a reload that changes an attribute changes no listing); update the existing exact-schema assertion to the new schema, which is the change FR-027 asks for, and keep every assertion about results unchanged
- [X] T042 [P] [US4] Agreement tests in `mcpserver/structured_test.go`: for results with `Output` and `Failure` set, the structured content validates against the schema, `stdout` and `stderr` equal the text's streams, every count in `output` matches the omission line it describes, every field of `failureSummary` equals the value its text line shows, and a result with neither has exactly the four fields of spec 044
- [X] T043 [P] [US4] Integration test in `test/integration/agent_output_mcp_test.go` with the real `rune mcp` binary: list the tools and check the schema has the two optional properties; call a task with both attributes that prints 2,000 lines on stdout, a secret on stderr and fails with a `||` suggestion: `isError`, `[exit 1]`, the omission line, the summary before the fix suggestion section, structured `output` and `failureSummary` matching the text, no secret anywhere; call a task that prints 3 MB on each stream with `tail="10"` and check each structured stream against the limit; call a task without the attributes and check its result has no new field and its text is the spec 044 text; make the failing call once more over HTTP with `rune serve` (the `connectHTTP` helper of `test/integration/reload_mcp_test.go`) and check the same summary and `output` (FR-035; the endpoint an `agent` task opens uses the same handler); run the server with an audit log and check that the record of the failing call holds no line of the kept streams and no summary text (FR-034)

### Implementation for User Story 4

- [X] T044 [US4] In `mcpserver/structured.go`, add `Output *OutputReport` (`json:"output,omitempty"`) and `FailureSummary *FailureSummary` (`json:"failureSummary,omitempty"`) to `structuredResult`, fill them in `structuredFrom` from the kept result, and add the two properties with their one-sentence descriptions to `outputSchema()`, written out in full
- [X] T045 [US4] Run T041 to T043 and `go test ./mcpserver/... ./test/integration/...`; all pass

**Checkpoint**: every story works and agrees across text and structured content.

---

## Phase 7: Polish and cross-cutting concerns

- [X] T046 [P] Document both attributes in `docs/runefile.md` under Attributes (syntax, values, scope: agents only, the called task's policy governs, command line unchanged) and add the grammar lines of `contracts/runefile-attributes.md` to `docs/GRAMMAR.md`
- [X] T047 [P] Add `RUNE2021` to `docs/diagnostics.md` with its cases
- [X] T048 Document in `docs/mcp.md`, next to the output limit and structured results sections: the omission line, the failure summary section and its kinds, the `output` and `failureSummary` fields, that a task without the attributes is unchanged, and the bound on a whole result (each stream at most the limit, the summary at most 4 KiB)
- [X] T049 Build the binary in the test container, run a noisy test task over MCP with and without `[agent-output(tail="40")]` and `[agent-summary]`, and write the measured sizes of both results and the kept evidence into the example in `docs/mcp.md` (FR-038, SC-001); no number in the docs that was not measured
- [X] T050 Run `go test ./test/docs/...` for the docs harness; it passes
- [X] T051 Run the whole suite `go test ./...` and `go test -race ./...` (with `CGO_ENABLED=1`) in Docker, and `golangci-lint run` with the v2.14 image; all pass with 0 issues
- [X] T052 Measure coverage of `mcpserver/linebuffer.go`, `mcpserver/summary.go`, `internal/cli/mcpoutput.go` and `internal/analyzer/agentoutput.go`; at least 90% each, with a test added for any function below
- [X] T053 Walk the scenarios of `quickstart.md` with the built binary and fix any difference from the contracts
- [X] T054 Run `git status --short --ignored` and check that every new fixture under `testdata/` is tracked, and that no file written by the test container is left owned by root

---

## Dependencies and execution order

### Phase dependencies

- Phase 1 has no dependency.
- Phase 2 depends on Phase 1 and blocks every story.
- US1 (Phase 3) and US2 (Phase 4) depend on Phase 2 only. They touch different functions of
  `internal/cli/mcp.go` (`call` sets `Output` in T023 and `Failure` in T034), so run T023
  and T034 one after the other.
- US3 (Phase 5) depends on US1: it extends `LineBuffer`.
- US4 (Phase 6) depends on Phase 2; its agreement and integration tests need US1 to US3 to
  pass.
- Phase 7 depends on every story.

### Within each story

Tests first, seen to fail; then the `mcpserver` code; then the `internal/cli` code; then the
story's test run.

### Parallel opportunities

- T002 to T006 are in different files.
- T010, T011, T012 are in different files after T007.
- In US1, T015 to T020 are independent test files or independent tests.
- In US2, T025 to T030 likewise.
- US2's `mcpserver/summary.go` work (T031, T032) can proceed while US1's `LineBuffer` (T021)
  is in progress.
- T046 and T047 are independent docs.

## Parallel example: User Story 1

```text
T015 LineBuffer table tests        mcpserver/linebuffer_test.go
T018 engine tests                  internal/cli/mcpoutput_test.go
T020 command-line test             internal/cli/mcpoutput_test.go (after T018, same file)
```

## Implementation strategy

### MVP first

1. Phase 1 and Phase 2.
2. Phase 3 (US1): the tail policy. Stop and validate: an agent's result of a noisy task is
   small and keeps its outcome.

### Incremental delivery

1. US1: tail.
2. US2: failure summary.
3. US3: head.
4. US4: structured fields and the schema.
5. Polish: docs with measured numbers, the full gates.

Each step leaves every earlier test passing and tasks without the attributes unchanged.
