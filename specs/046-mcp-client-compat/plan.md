# Implementation Plan: Agent Client Compatibility Matrix

**Branch**: `046-mcp-client-compat` | **Date**: 2026-11-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/046-mcp-client-compat/spec.md`

## Summary

Report how Claude Code, Codex CLI, Cursor, VS Code Copilot agent mode and Zed handle what
`rune mcp` sends, in a "Client compatibility" section of `docs/mcp.md`, with every statement
labelled by its evidence. Back the two clients that can be run here with sessions recorded
on the wire, and keep the recorder, the scenario and a README in the repository so the
research can be repeated. Tests hold each recording to a case that repeats the table.

The research, the section, the recorder, nine recordings and the tests were made before this
plan, on this branch, and are not committed. This plan records their design and adds what
they do not meet yet: FR-017, no identifier of a vendor session in a committed recording;
FR-016, the client's reply to each session kept beside its recording; and FR-008, the
guide's definition of "recorded" naming that reply.

## Technical Context

**Language/Version**: Go 1.27 (the repository's toolchain), for the recorder and the tests.
Markdown for the guide and the README.

**Primary Dependencies**: the Go standard library only. No new module dependency.

**Storage**: files. Recordings are JSON Lines under `test/integration/testdata/clients/`.

**Testing**: `go test` inside Docker (`docker compose run --rm -e GOFLAGS=-buildvcs=false
test go test ./...`). The integration package builds the `rune` binary and the recorder once
and runs both as processes. `test/docs` checks the guide.

**Target Platform**: Linux for recording. The tests run on Linux and Windows in CI; the
tests that need `sh`, `cat` or a signal skip on Windows.

**Project Type**: CLI. This feature adds documentation, test data and a test-only tool.

**Performance Goals**: none. The recorder adds one file write per message.

**Constraints**: no change to the `rune` binary or to `mcpserver` (FR-022). A recording
holds no machine path, user name or vendor session identifier (FR-017). The recorder is not
part of the module's build (`go build ./...` skips `testdata`).

**Scale/Scope**: five clients, six questions, nine recordings (about 190 KB) with nine reply files, one section of
about 250 lines in `docs/mcp.md`, one recorder of about 200 lines, one test file.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No task behavior changes. |
| II. Errors are a feature | PASS | No diagnostic changes. The recorder's own errors are one line each on stderr with an exit code. |
| III. Minimal, total DSL | PASS | No DSL change. The scenario Runefile uses existing syntax only. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package in the module. The recorder is a `main` package under `testdata`, built only by the tests. |
| V. Boringly portable | PASS | No product code. Tests that need Unix tools or signals skip on Windows; the recorder itself builds and runs there. |
| VI. Test-first, multi-layer verification | DEVIATION, recorded below | The recorder was written before its tests, during the research and before this plan. Every defect found since was fixed test-first, and the changes this plan adds start with a failing test. |
| VII. AI-native, secure by default | PASS | Nothing Rune sends changes. The guide documents how each client treats the hints and the destructive opt-in, which makes the security model easier to use correctly. Recordings carry no secret. |
| VIII. Go engineering discipline | PASS | The recorder's goroutines all end with the session; no `init()`, no globals. `golangci-lint` is clean on the test package and on the recorder when it is named explicitly. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. |
| Constraint: surface changes carry their docs | PASS | No surface change. The change is documentation. |

One deviation, on Principle VI, recorded in Complexity Tracking. No other violation.

**Post-design re-check**: unchanged. The design adds no product code, and the recording format is
a test-data format with one consumer in the repository.

## Project Structure

### Documentation (this feature)

```text
specs/046-mcp-client-compat/
├── plan.md              # This file
├── research.md          # How the research was done and why
├── data-model.md        # Recording, scenario, case, evidence label
├── quickstart.md        # How to validate the result
├── contracts/
│   └── recording-format.md   # The recording file and the recorder's command line
├── checklists/
│   └── requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
docs/
└── mcp.md                               # + "Client compatibility" section, one pointer in "Starting the server"

test/integration/
├── clients_fixture_test.go              # recorder tests, one case per recording
└── testdata/clients/
    ├── README.md                        # what was recorded, how, and how to record a client
    ├── recorder/main.go                 # the recorder
    ├── scenario/Runefile, tools.rune    # the Runefile every session ran against
    ├── claude-code/*.jsonl, *.reply.txt # four recordings, each with the client's reply
    └── codex/*.jsonl, *.reply.txt       # five recordings, each with the client's reply
```

**Structure Decision**: Everything lives where the issue puts it: the table in
`docs/mcp.md`, the recordings under `test/integration/testdata/clients/`. The recorder and
the scenario sit beside the recordings they made, so the directory is complete on its own.
The tests go in the existing `test/integration` package, which already builds the binary
and has the MCP process helpers.

## Design

Details and alternatives are in [research.md](research.md); the file format is in
[contracts/recording-format.md](contracts/recording-format.md).

1. **Evidence labels.** Every statement carries one of recorded, source, docs, staff,
   unknown. "recorded" means seen in a session that has a recording in the repository: on
   the wire, or in the client's reply to that session, kept beside the recording (FR-008).
2. **Recording on the wire.** The client is configured to start the recorder, which starts
   `rune mcp` and writes each message, stderr line and event to a JSON Lines file with the
   time since it started. A message is recorded before it is passed on, byte for byte.
3. **One scenario.** One Runefile has a task per question: a `[context]` task that names a
   codename, a `[readonly]`, a plain, a `[confirm]` and a `[network]` task, a task with a
   hyphen, a module task, a task that adds a task to the Runefile, and a task that sleeps.
4. **Several sessions per client.** A main session answers instructions, names, approval
   and reload. Separate sessions answer the call limit, approval without pre-approved tools,
   and project configuration.
5. **Clients that cannot be run** are reported from vendor documentation, vendor staff
   replies and source code, each checked against the page or file itself, and marked as not
   tested.
6. **Cases.** `TestClientRecordings` has one case per recording: client name and version,
   the tool calls that reached Rune, whether the client listed tools again, and when it
   cancelled. A recording without a case, or without a reply file, fails the test.
7. **Replies (new).** The client's reply to each session is kept word for word in
   `<recording>.reply.txt`. It is the evidence for what the model was given: the
   instructions and the tool names.
8. **Identifier replacement (new).** Vendor session, thread, turn, call, item and tool-use
   identifiers in the recordings are replaced with numbered placeholders, the same
   identifier always with the same placeholder within a file. A test rejects a recording
   or a reply that holds a home path or an identifier that is not a placeholder. The README
   lists the replacement, and what else the clients sent that was kept.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle VI: the recorder (`testdata/clients/recorder/main.go`) was written before its tests | The research needed a recorder to find out what there was to test: the entries worth recording (signals, stdin close, stderr) became known only from the first sessions. | Rewriting it test-first now would reproduce the same program. Instead every behavior of the contract has a test (FR-021), the three defects two reviews found were each fixed after a failing test, and it is test data tooling, not product code. |
