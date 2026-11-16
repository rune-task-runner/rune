---

description: "Task list for spec 047: Rune-only agent mode design note"
---

# Tasks: Rune-Only Agent Mode Design Note

**Input**: Design documents from `/specs/047-rune-only-agent-mode/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: No new test. The feature adds no code, and the issue's verification target is
design review. The existing documentation harness (`test/docs`) covers the note and the
guide (FR-028), and the full suite runs once at the end (SC-008).

**Organization**: Tasks are grouped by user story. Paths are from the repository root.
`NOTE` stands for `docs/design/rune-only-agent-mode.md` and `OBS` for
`specs/047-rune-only-agent-mode/observations`.

**State**: The reading passes and the 26 client runs were done in the plan's Phase 0. Their
outputs are in a temporary directory, not in the repository. Those tasks are marked `[X]`.
Everything that puts a file in the repository is open.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US4)

## Conventions for every task

- No change to the `rune` binary, `mcpserver` or the `(agent)` executor (FR-025).
- Every statement about what a client does carries one label: observed, recorded, source,
  docs, staff or unknown (FR-009). "observed" is used only with an id that has a file in
  `OBS` (FR-010).
- A tool is "removed" when the model is not given it and "refused" when it is given and
  each call is refused. Never write one for the other
  ([contracts/setup-outcome.md](contracts/setup-outcome.md)).
- A Rune flag is described as what Rune then does, never as a restriction of the client
  (FR-019).
- A client's part names every command-running tool that was found, direct and indirect,
  and says of each one: removed, refused, or not covered by the configuration shown
  (spec, User Story 1, scenario 2).
- A kept file holds no path of the research machine, no user name, and no identifier of a
  vendor account or session (FR-027).
- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
- No em dash in docs. Plain sentences, one idea each. The documentation harness forbids the
  word "recipe" and the spelling "Runfile".

## Phase 1: Setup

**Purpose**: The branch, the spec directory and the places the files go.

- [X] T001 Create branch `047-rune-only-agent-mode` and `specs/047-rune-only-agent-mode/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/setup-outcome.md
- [X] T002 Create the directories `docs/design/`, `specs/047-rune-only-agent-mode/observations/claude-code/`, `specs/047-rune-only-agent-mode/observations/codex/` and `specs/047-rune-only-agent-mode/observations/agent-executor/`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The evidence. No statement of the note may be labelled observed before its
file is in the repository.

- [X] T003 Read each of the five clients: vendor documentation, and source at a named commit or tag for Codex CLI, VS Code and Zed; check every statement the decision rests on at the page or file itself (research.md R1)
- [X] T004 Run the 26 observations of research.md R3 and R4: Claude Code `c1` to `c12`, Codex captures `x1` to `x7`, Codex runs `y1` to `y5`, `(agent)` executor `a1` `a2`
- [X] T005 Write one file per Claude Code run in `specs/047-rune-only-agent-mode/observations/claude-code/` (`c1.txt` to `c12.txt`): the `tools` array of the `system/init` event, the permission denials, the stderr line about workspace trust where there was one, and the reply word for word; replace the project path with `PROJECT` and the home directory with `~`
- [X] T006 [P] Write one file per Codex run in `specs/047-rune-only-agent-mode/observations/codex/` (`x1.txt` to `x7.txt` with the top-level and nested tool names, `y1.txt` to `y5.txt` with the items of the run and the reply), cutting the tools of the account's connected apps, and add `capture.py` and `tools.py` beside them
- [X] T007 [P] Write `a1.txt` and `a2.txt` in `specs/047-rune-only-agent-mode/observations/agent-executor/`: the Runefile, the MCP configuration file of `a2`, the command and the output
- [X] T008 Write `specs/047-rune-only-agent-mode/observations/README.md`: the date, the versions, the models, the scenario, a table of all 26 ids with project files, command, prompt and result, the prompts in full, "How the runs were kept apart from the user's configuration" (FR-026), "What was replaced", "What was discarded" (the runs under `~/.claude`, and the body-edit run that called before the reload), and "Repeating a run" (depends on T005 to T007)
- [X] T009 Scan `specs/047-rune-only-agent-mode/observations/` with the command of quickstart.md step 5 and fix every hit (FR-027)

**Checkpoint**: every id the note will cite has a file and a README row.

---

## Phase 3: User Story 1 - An operator learns what makes a session Rune-only (Priority: P1)

**Goal**: For each of the five clients: how Rune is registered, which tools run commands,
how each is removed, how Rune's tools are allowed, where each setting lives, and the scope.

**Independent Test**: Pick any client in `NOTE`. Its part answers register, remove and
allow with a configuration or a plain "not possible" or "unknown", each with a label.

- [X] T010 [US1] Create `docs/design/rune-only-agent-mode.md` with the opening: the problem (FR-002), the definition of a Rune-only session and of a command-running tool (FR-003), the date 2026-11-16, the six evidence labels with a link to `specs/047-rune-only-agent-mode/observations/README.md`, and the statement that Cursor, VS Code and Zed were not run (FR-004, FR-009)
- [X] T011 [US1] Add the summary table to `docs/design/rune-only-agent-mode.md`: one row per client with version, outcome (one of the three of contracts/setup-outcome.md), what removes the shell, what allows Rune's tools, and the scope; no empty cell (FR-005, FR-007, FR-008)
- [X] T012 [US1] Add the "Claude Code" part to `docs/design/rune-only-agent-mode.md`: `.mcp.json`; `Bash`, `PowerShell` and `Monitor` as direct tools and `Agent`, `Workflow`, `Skill`, `CronCreate`, `RemoteTrigger` as indirect; `permissions.deny` with bare names in `.claude/settings.json` (c1, c2); `permissions.allow` with `mcp__rune` and its being ignored in an untrusted workspace (c2, c3); `--restricted` (c4); settings levels; scope; what was not tested; sources (FR-006)
- [X] T013 [US1] Add the "Codex CLI" part to `docs/design/rune-only-agent-mode.md`: `[mcp_servers.rune]` and `cwd`; `exec_command` and `write_stdin` nested in `exec` (x1); `features.shell_tool = false` (x2, y2); `agents.enabled = false` (x3); `features.unified_exec` having no effect (x5), and, under FR-011, where reading and running disagree: the client's own feature list marks `code_mode` as off while the model is given one `exec` JavaScript tool that holds the others (x1); `default_tools_approval_mode = "approve"`; project file only in a trusted project; layer order; `requirements.toml`; what was not tested; sources (FR-006, FR-011)
- [X] T014 [US1] Add the "Cursor" part to `docs/design/rune-only-agent-mode.md`: `.cursor/mcp.json`; the shell tool; no removal since custom modes left in 2.1 (staff); `.cursor/hooks.json` refusing `Shell` with `failClosed`; `.cursor/cli.json` permissions for the CLI; subagents and cloud agents; marked not tested; sources (FR-006, FR-007)
- [X] T015 [US1] Add the "VS Code, Copilot agent mode" part to `docs/design/rune-only-agent-mode.md`, keeping the Local harness and the Copilot harness apart: registration files; the tools that run commands; no workspace setting that removes them; a custom agent's `tools` list as an allowlist the user can widen; an empty list meaning all tools in the Copilot harness (source) where GitHub's documentation says it disables all tools, both reported (FR-011); marked not tested; sources (FR-006, FR-007)
- [X] T016 [US1] Add the "Zed" part to `docs/design/rune-only-agent-mode.md`: `context_servers` in `.zed/settings.json` in a trusted worktree; `terminal` and `spawn_agent`; profiles and `tool_permissions` being user settings only; external agents ignoring them; marked not tested; sources (FR-006, FR-007)
- [X] T017 [US1] In `docs/mcp.md`, add a first bullet to "Security model" saying that Rune decides what Rune runs, that registering Rune or filtering its tasks does not remove or restrict the client's shell, with a link to the note; add to "Agent task bodies" that the launched agent keeps the tools its own configuration gives it, with a link; add the note to "See also" (FR-020)
- [X] T018 [US1] Add a group "Design notes" with `design/rune-only-agent-mode.md` to `docs/nav.yaml`, after "Reference" (FR-001, FR-028)

**Checkpoint**: an operator can configure Claude Code or Codex by hand from the note, and
knows that the other three cannot be made Rune-only for a project.

---

## Phase 4: User Story 2 - A maintainer knows which approach Rune takes (Priority: P1)

**Goal**: One chosen approach, a reason for each of the others, and the reporting rule.

**Independent Test**: Read "Decision" alone. It names one approach, gives a status and a
reason for each of the four, and states what is reported when a client cannot be
restricted.

- [X] T019 [US2] Add the "Decision" section to `docs/design/rune-only-agent-mode.md`, after the summary table: the four approaches (print, write, hook, launch) each with chosen, rejected or deferred and a reason, per research.md R6; exactly one chosen (FR-012, FR-015)
- [X] T020 [US2] Add the reporting rule to "Decision" in `docs/design/rune-only-agent-mode.md`: the three outcomes, registration never reported as restriction, a pending trust step named, with a link to `specs/047-rune-only-agent-mode/contracts/setup-outcome.md` (FR-016)
- [X] T021 [US2] Add the "Hooks" section to `docs/design/rune-only-agent-mode.md`: a table of what a hook can do to a shell call in Claude Code, Codex, Cursor and VS Code (refuse with a message, change the input, replace the call), that Zed has none found, the observed refusal that led to a Rune call (c5, c6), and why matching a command to a task is unsound (FR-013)
- [X] T022 [US2] Add "The agent executor" section to `docs/design/rune-only-agent-mode.md`: what `(agent)` passes to `agent_cmd` today (`RUNE_MCP_URL`, `RUNE_MCP_TOKEN`), that the agent has a shell and no Rune tools with the documented command (a1), the `agent_cmd` and MCP file that give a Rune-only agent (a2), and what the chosen approach leaves alone (FR-014)

**Checkpoint**: issue #53 has a decision to build on.

---

## Phase 5: User Story 3 - A reader knows where the restriction ends (Priority: P1)

**Goal**: One section with every boundary found.

**Independent Test**: Read "Enforcement boundaries". Each entry says what can still be
done, why removing the shell does not stop it, and what closes it or that nothing in scope
does.

- [X] T023 [US3] Add the "Enforcement boundaries" section to `docs/design/rune-only-agent-mode.md` with the seven boundaries of FR-017: file writing with Runefile reload (c7, c8, c9, c10, y3, y4; closed for the session by `rune mcp --no-reload`); file writing with the scripts and code a task runs; the agent writing the client's own configuration; other profiles, modes and subagents; settings levels above the project; a user who overrides the configuration; restricting the client against sandboxing a task, naming issue #51 (FR-017, FR-018)
- [X] T024 [US3] Add to "Enforcement boundaries" in `docs/design/rune-only-agent-mode.md` the further boundaries found: an untrusted project; other MCP servers and connected apps; a task that takes a command as a parameter; tasks run by Rune outside the client's sandbox (FR-017, FR-018)
- [X] T025 [US3] Read every mention of `--allow`, `--deny`, `--allow-destructive` and `--no-reload` in `docs/design/rune-only-agent-mode.md` and `docs/mcp.md` and correct any that presents a Rune flag as restricting the client (FR-019)

**Checkpoint**: no boundary is left to be inferred from a client's part.

---

## Phase 6: User Story 4 - The follow-up issue carries the findings (Priority: P2)

**Goal**: The text to add to issue #53.

**Independent Test**: Read `follow-up-issue-53.md` beside issue #53. Each client of #46 or
#53 is supported, partial or not supported, and each correction names the statement it
corrects.

- [X] T026 [US4] Write `specs/047-rune-only-agent-mode/follow-up-issue-53.md`: the client list with supported, partial or not supported, scope and reason for Claude Code, Codex CLI, VS Code, Cursor and Zed (FR-021); adapter facts for Claude Code, Codex CLI and VS Code: files, keys, tool names, scope, what a merge must keep (FR-022); corrections to #53 with evidence (FR-023); a finding for each acceptance criterion of #53 that the research touches; what a verification must show, as in `specs/047-rune-only-agent-mode/contracts/setup-outcome.md` (FR-024)
- [X] T027 [US4] Add the closing section "What issue #53 must do" to `docs/design/rune-only-agent-mode.md`: the short list of consequences, with links to `specs/047-rune-only-agent-mode/follow-up-issue-53.md` and to issues #53 and #51

**Checkpoint**: both deliverables of issue #46 exist.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T028 Walk the table of quickstart.md step 3 against `docs/design/rune-only-agent-mode.md` and `docs/mcp.md`; fix every row that does not hold, and check that every "observed" id exists in `specs/047-rune-only-agent-mode/observations/README.md` (SC-001 to SC-007)
- [X] T029 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` and fix what fails (FR-028)
- [X] T030 Run `git status --short` and `git diff --stat main -- . ':!docs' ':!specs'` and confirm nothing outside `docs/` and `specs/047-rune-only-agent-mode/` changed; search the changed files for an em dash (SC-008)
- [X] T031 Run the full suite, `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`, and report the result (SC-008)
- [X] T032 Delete the temporary observation directory under `/tmp` and the downloaded pages in the job's temporary directory

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: after Setup. Blocks every story: the note cites observation
  ids.
- **US1 (Phase 3)**: after Foundational. Creates the note, so US2, US3 and the second task
  of US4 come after T010.
- **US2 (Phase 4)**: after T011, because "Decision" follows the summary table and uses its
  outcomes.
- **US3 (Phase 5)**: after US1's client parts, which it refers to.
- **US4 (Phase 6)**: T026 after US1 and US2. T027 after T026.
- **Polish (Phase 7)**: after all stories.

### Within Each Story

- All tasks of US1 to US3 edit one file, `docs/design/rune-only-agent-mode.md`, except T017
  and T018. They run in order.

### Parallel Opportunities

- T006 and T007 beside T005: three directories.
- T017 and T018 beside the client parts T012 to T016: different files.
- T026 beside US3: a different file.

## Parallel Example: Foundational

```text
Task: "Write one file per Claude Code run in observations/claude-code/"
Task: "Write one file per Codex run in observations/codex/, with capture.py and tools.py"
Task: "Write a1.txt and a2.txt in observations/agent-executor/"
```

## Implementation Strategy

### MVP First (User Story 1)

1. Phases 1 and 2: the evidence is in the repository.
2. Phase 3: the note's opening, summary table and five client parts, and the guide's
   correction.
3. Stop and check: an operator can restrict Claude Code or Codex by hand, and the guide no
   longer lets a Rune allowlist be read as a restriction of the client.

### Incremental Delivery

1. US1: what each client can do.
2. US2: what Rune will do about it.
3. US3: where it ends.
4. US4: what #53 inherits.
5. Polish: the checks.

All four stories ship in one pull request, because the note is one document under one
review.
