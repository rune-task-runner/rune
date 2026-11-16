---

description: "Task list for spec 046: agent client compatibility matrix"
---

# Tasks: Agent Client Compatibility Matrix

**Input**: Design documents from `/specs/046-mcp-client-compat/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI, and FR-020 and FR-021 ask for them. A test
that covers new behavior is written and seen failing before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

**State**: The research, the guide section, the recorder, nine recordings and the tests were
made on this branch before this task list, and are not committed. Those tasks are marked
`[X]` and describe what exists. The open tasks are the identifier replacement of FR-017, the
reply files of FR-016, the definition of "recorded" in the guide (FR-008), and the final
verification.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US3)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- No change to the `rune` binary or to `mcpserver` (FR-022).
- A statement is labelled "recorded" only when a recording in the repository shows it
  (FR-008).
- The table in `docs/mcp.md`, the rows of `test/integration/testdata/clients/README.md` and
  the cases of `TestClientRecordings` state the same facts. Change them together.
- A recording is changed only as [contracts/recording-format.md](contracts/recording-format.md)
  allows, and the README lists the change.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline and what can be run.

- [X] T001 Create branch `046-mcp-client-compat` from `main` at `c5ee68e` and confirm `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/ ./test/docs/...` is green.
- [X] T002 Find which of the five clients can be run on the recording machine: Claude Code 2.1.325 and Codex CLI 0.167.0 are installed; Cursor, VS Code and Zed are not, and there is no display ([research R7](research.md)).

---

## Phase 2: Foundational (the recorder, the scenario, the sessions)

**Purpose**: Evidence every story builds on ([research R1 to R6](research.md)).

- [X] T003 Write the recorder in `test/integration/testdata/clients/recorder/main.go` per [contracts/recording-format.md](contracts/recording-format.md): `recorder -out FILE COMMAND [ARG...]`, pass-through of stdin, stdout and stderr, one entry per line with `ms`, signal forwarding, the command's exit code; a line is recorded before it is passed on, byte for byte (FR-013, FR-014, FR-018).
- [X] T004 [P] Write the scenario in `test/integration/testdata/clients/scenario/Runefile` and `test/integration/testdata/clients/scenario/tools.rune`: `health` (`[context]`), `status` (`[readonly]`), `build`, `clean` (`[confirm]`), `fetch` (`[network]`), `docs-check`, `tools::lint`, `grow`, `slow` (FR-015).
- [X] T005 Record Claude Code 2.1.325 with `claude -p` into `test/integration/testdata/clients/claude-code/`: `session.jsonl`, `approval.jsonl`, `timeout.jsonl`, `project-config.jsonl` (FR-011, FR-012).
- [X] T006 Record Codex CLI 0.167.0 with `codex exec` into `test/integration/testdata/clients/codex/`: `session.jsonl`, `approval-mode.jsonl`, `list-changed-flag.jsonl`, `timeout.jsonl`, `project-config.jsonl` (FR-011, FR-012).
- [X] T007 Research Cursor, VS Code Copilot agent mode and Zed from vendor documentation, vendor staff forum replies and source code, and check every statement again at the page, thread or file itself ([research R7](research.md)).

**Checkpoint**: Two clients have recordings; three have checked statements from reading.

---

## Phase 3: User Story 1 - A Runefile author learns what a client does (Priority: P1)

**Goal**: One section of the MCP guide answers the six questions for the five clients.

**Independent Test**: In `docs/mcp.md`, section "Client compatibility", every client has a
cell in every row, and a part of its own with a configuration.

- [X] T008 [US1] Add the section "Client compatibility" to `docs/mcp.md` before "See also", with the date of the research and the table of FR-002: five client columns, and rows for version, project configuration, user configuration, instructions, hints and approval, the two tool names, `list_changed`, the default call limit, and the two cancellation questions (FR-001 to FR-004).
- [X] T009 [US1] Add "What this means for a Runefile" to the section in `docs/mcp.md`: rules about `[context]`, `[confirm]` and `[network]` under Codex, `[readonly]`, module tasks, hyphens, name length, reload and long tasks (FR-006).
- [X] T010 [US1] Add a part for each of the five clients to the section in `docs/mcp.md`: a configuration that starts `rune mcp`, the details behind the cells, what was not tested, and sources; the VS Code part says which of the vendor's two ways to run a server its statements cover (FR-005).
- [X] T011 [US1] In "Starting the server" of `docs/mcp.md`, point to the section for each client's configuration file.

**Checkpoint**: The table is complete and no cell is empty.

---

## Phase 4: User Story 2 - A reader can tell what was seen from what was read (Priority: P1)

**Goal**: Every statement names its evidence, and a recorded one can be traced to a
recording that is fit to publish.

**Independent Test**: Pick a cell marked "recorded"; the README names the recording, the
command and the prompt; the recording shows it and holds nothing about the machine or the
account that made it.

- [X] T012 [US2] Define the labels recorded, source, docs, staff and unknown above the table in `docs/mcp.md`, put one in every cell and every statement of the client parts, and say in the introduction and in each part which clients were not tested (FR-007 to FR-009).
- [X] T013 [US2] Report both sides where documentation and behavior disagree, in `docs/mcp.md`: the Codex call limit, 60 seconds in the documentation and 300 in the source and the recording (FR-010).
- [X] T014 [US2] Write `test/integration/testdata/clients/README.md`: the format, the scenario, the main prompt, and for each recording the client version, the flags, the prompt and what the client reported; why Cursor, VS Code and Zed have no recording; the edits made to recordings (FR-016).
- [X] T015 [US2] Replace the workspace path Codex sent in `test/integration/testdata/clients/codex/project-config.jsonl` with `/work/rune`, and list the edit in the README.
- [X] T016 [US2] In `test/integration/clients_fixture_test.go` first list every key under `_meta` in every recording, with one example value each, and decide for each whether it is an identifier of an account or session; an identifier of a shape the rules below do not catch gets a rule and a test input of its own. Then add `TestClientRecordingsHoldNoPrivateData`, failing until T017. For every `testdata/clients/*/*.jsonl` and `testdata/clients/*/*.reply.txt` it fails on: `/home/`, `/root/`, `/Users/` or `\Users\`; a UUID that does not match `00000000-0000-0000-0000-[0-9]{12}`; `toolu_` not followed by exactly four digits; `ctc_` not followed by exactly four digits. Put the match in a helper `privateData(data []byte) []string` that returns what it found, and unit-test the helper in `TestPrivateData` with one input per rule and one clean input (FR-017, SC-006).
- [X] T017 [US2] Replace the vendor identifiers in all nine recordings under `test/integration/testdata/clients/` per [contracts/recording-format.md](contracts/recording-format.md) and [research R10](research.md): each distinct UUID becomes `00000000-0000-0000-0000-000000000001`, `...0002` and so on in order of first appearance within the file; each distinct `toolu_...` becomes `toolu_0001` and so on; each distinct `ctc_...` becomes `ctc_0001` and so on. The same identifier gets the same placeholder within a file. Change nothing else: after the replacement every line still parses, `ms`, `from`, methods, JSON-RPC ids and tool names are unchanged, and `TestClientRecordings` still passes.
- [X] T018 [US2] Update the "Edits" section of `test/integration/testdata/clients/README.md` to list the identifier replacement with the three placeholder forms and the `_meta` keys that were kept as sent, and change the last step of "Recording a client" to say that identifiers and paths are replaced before a recording is committed. Change the README's sentence about Codex `_meta` only if it no longer matches the recordings.
- [X] T019 [US2] In `TestClientRecordings` in `test/integration/clients_fixture_test.go`, require a file `NAME.reply.txt` that is not empty beside every `NAME.jsonl`, failing until T020 (FR-016).
- [X] T020 [US2] Add the client's final reply to each of the nine sessions, word for word, as `test/integration/testdata/clients/<client>/NAME.reply.txt`, with identifiers and paths replaced as in T017. In `test/integration/testdata/clients/README.md`, say what a reply file is, link each row to its reply, and keep the row's summary consistent with it (FR-008, FR-012, FR-016).
- [X] T021 [US2] In `docs/mcp.md`, make the definition of "recorded" say that a statement about what the model was given rests on the client's reply kept beside the recording (FR-007, FR-008).

**Checkpoint**: `TestClientRecordingsHoldNoPrivateData` passes, every recording has its reply, and the README lists every edit.

---

## Phase 5: User Story 3 - A contributor records another client (Priority: P2)

**Goal**: The research can be repeated, and a bad recording fails the tests.

**Independent Test**: Follow the README with a scripted client; the recording has the
format of the contract and `TestClientRecordings` rejects it until it has a case.

- [X] T022 [US3] In `test/integration/clients_fixture_test.go` add `TestClientRecordings`: one case per recording with client, version, the tool calls that reached Rune, whether tools were listed again, and the seconds to cancellation; a recording without a case fails; a recording that called `grow` must hold `notifications/tools/list_changed` (FR-020).
- [X] T023 [US3] In `test/integration/clients_fixture_test.go` add the recorder tests: a whole session, text that is not JSON, the server's exit code, wrong usage, a signal, a message recorded before its reply, a server that stops reading, a server killed by a signal; and `TestCancelledCall` for the helper that finds the cancelled call (FR-021).
- [X] T024 [US3] Add "Recording a client" to `test/integration/testdata/clients/README.md`: build steps, a new scenario copy and a new `-out` file per session, and a configuration for each of the five clients (FR-019).

**Checkpoint**: The recorder tests and the recording cases pass.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T025 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test sh -c 'go build ./... && go vet ./... && go vet ./test/integration/testdata/clients/recorder && go test ./...'` and confirm everything passes (SC-007).
- [X] T026 [P] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./test/integration/ -run 'TestClient|TestCancelledCall|TestPrivateData'` and confirm it passes.
- [X] T027 [P] Run golangci-lint v2.14 on `./...` and on `./test/integration/testdata/clients/recorder/`, and `golangci-lint fmt --diff`, and confirm 0 issues.
- [X] T028 Run the "By hand" steps 1, 2 and 4 of [quickstart.md](quickstart.md): no empty cell in the table of `docs/mcp.md`, one recorded cell traced to its recording, and the search for machine and account data returns nothing.
- [X] T029 Confirm `git diff --stat main -- . ':!docs' ':!specs' ':!test' ':!.specify'` and `git status --short` show no file outside `docs/`, `specs/`, `test/` and `.specify/feature.json` (FR-022, SC-007).
- [ ] T030 After the pull request is open, watch its checks to the end and confirm they are green on Linux and Windows. Four recorder tests skip on Windows; nothing else may. If a check fails, find the cause, fix it and watch again.

---

## Dependencies & Execution Order

- Phase 1, then Phase 2. Phases 3 to 5 build on Phase 2.
- US1 (table) and US2 (evidence) are both P1 and share `docs/mcp.md`: US2's labels go into
  the cells US1 creates, so T012 and T013 follow T008 to T010.
- US3's tests (T022, T023) need the recordings of T005 and T006 and the recorder of T003.
- Open tasks: T016, then T017, then T018; T019, then T020; T021. T017 must not start before
  T016 has been seen failing, and T020 not before T019. Phase 6 follows T021.

### Parallel opportunities

- T003 and T004 touch different files.
- T026 and T027 can run at the same time, after T025.

## Implementation Strategy

1. The table (US1) with honest labels (US2) is the result the issue asks for; it is done.
2. The recordings become fit to publish, and complete as evidence, with T016 to T021.
3. Phase 6 verifies the whole change before it is committed.
