# Implementation Plan: Rune-Only Agent Mode Design Note

**Branch**: `047-rune-only-agent-mode` | **Date**: 2026-11-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/047-rune-only-agent-mode/spec.md`

## Summary

Write a design note, `docs/design/rune-only-agent-mode.md`, that says for Claude Code,
Codex CLI, Cursor, VS Code Copilot agent mode and Zed how `rune mcp` is registered, which
client tools run commands, how each is removed, and how Rune's tools are allowed; which
approach Rune takes to produce that configuration; and where the restriction ends. Correct
the security model in `docs/mcp.md` so it says that Rune's filters do not reach the client's
shell. Keep, in this directory, the observations the note rests on and the text to add to
issue #53.

The research was done in this plan's Phase 0: five reading passes, one per client, and 26
runs of the two installed clients. [research.md](research.md) records the method and the
decisions. The findings themselves go in the note, written in the implementation phase.

## Technical Context

**Language/Version**: Markdown for the note, the guide and the follow-up text. Python 3
(standard library) for one 20-line helper that stands in for a model endpoint, and one that
lists the tools in what it captured; both are evidence tooling under `observations/`.

**Primary Dependencies**: none added. The observations used the installed clients, Claude
Code 2.1.325 and Codex CLI 0.167.0, and a `rune` built from this checkout.

**Storage**: files. Observations are text files under
`specs/047-rune-only-agent-mode/observations/`.

**Testing**: `test/docs` inside Docker (`docker compose run --rm -e GOFLAGS=-buildvcs=false
test go test ./test/docs/...`): links resolve, every page is in the navigation, generated
site pages pass their checks, terminology and secret scans pass. The full suite runs once
at the end to show nothing else moved. No new Go test: the feature adds no code, and the
issue's verification target is design review.

**Target Platform**: the documentation site and GitHub. Observations were made on Linux
x86_64.

**Project Type**: CLI. This feature adds documentation and research evidence only.

**Performance Goals**: none.

**Constraints**: no change to the `rune` binary, `mcpserver` or the `(agent)` executor
(FR-025). No change to the research machine's user configuration (FR-026). No machine
path, user name or vendor account identifier in a kept file (FR-027). The note and the
guide pass `test/docs` (FR-028).

**Scale/Scope**: five clients; one note of about 450 lines; about 12 lines changed in
`docs/mcp.md`; one navigation entry; 26 observation files with a README; one follow-up
text of about 120 lines.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No task behavior changes. |
| II. Errors are a feature | PASS | No diagnostic changes. The note asks #53 for a diagnostic where a client cannot be restricted; that is #53's work. |
| III. Minimal, total DSL | PASS | No DSL change. Observation Runefiles use existing syntax. |
| IV. Hand-written front end, idiomatic Go | PASS | No Go code. No package added. |
| V. Boringly portable | PASS | No product code. The note names where a control differs by OS (`PowerShell` on Windows). |
| VI. Test-first, multi-layer verification | PASS | "Documentation is a tested fixture": the note and the guide are under `test/docs`. There is no code to test first. The observations are evidence, kept with the commands that made them. |
| VII. AI-native, secure by default | PASS | The feature exists to state this principle's limit honestly: Rune refuses destructive tasks on its own surface and cannot speak for the client's shell. The guide's security model gains that statement. Nothing Rune sends changes. Kept evidence holds no secret and no account identifier. |
| VIII. Go engineering discipline | PASS | No Go code. |
| Constraint: Docker-only testing | PASS | `test/docs` and the full suite run in the Docker harness. Client runs are research, not the Go test suite. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. |
| Constraint: surface changes carry their docs | PASS | No surface change. |

No violation. Complexity Tracking is empty.

**Post-design re-check**: unchanged. The design adds one documentation page, one navigation
group and files under this directory.

## Project Structure

### Documentation (this feature)

```text
specs/047-rune-only-agent-mode/
├── plan.md                  # This file
├── research.md              # How the research was done, and the decisions
├── data-model.md            # Client, control, outcome, boundary, observation
├── quickstart.md            # How to check the result
├── contracts/
│   └── setup-outcome.md     # The three outcomes a setup may report, and what proves each
├── observations/
│   ├── README.md            # Every run: version, configuration, command, prompt, result
│   ├── claude-code/*.txt    # Tool list and reply of each Claude Code run
│   ├── codex/*.txt          # Tool list or reply of each Codex run
│   ├── codex/capture.py     # Stands in for a model endpoint and logs the request
│   ├── codex/tools.py       # Lists the tools in a captured request
│   └── agent-executor/*.txt # The two runs of an `(agent)` task
├── follow-up-issue-53.md    # The text to add to issue #53
├── checklists/
│   └── requirements.md
└── tasks.md                 # Created by /speckit-tasks
```

### Source Code (repository root)

```text
docs/
├── design/
│   └── rune-only-agent-mode.md   # new: the design note
├── mcp.md                        # security model: Rune's filters do not reach the client's shell
└── nav.yaml                      # new group "Design notes" with the note
```

**Structure Decision**: The note is a page of the documentation, under a new `docs/design/`
directory, because an operator needs it as much as the maintainer does and the guide links
to it. The evidence and the follow-up text stay in this spec directory: they are records of
this research, not documentation to keep current. Links from the note to them resolve in
the repository, and the documentation site turns them into links to GitHub, as it does for
the compatibility table's link to the client recordings.

## Design

Details and alternatives are in [research.md](research.md).

1. **One definition.** A Rune-only session is one whose agent has Rune's tools and no tool
   of the client that runs a command. A file-writing tool is not a command-running tool;
   what it makes possible is an enforcement boundary.
2. **Evidence labels.** The compatibility table's labels are reused: recorded, source,
   docs, staff, unknown. One is added, **observed**: seen in a run on 2026-11-16 whose
   version, configuration, command and output are in `observations/`. "recorded" keeps its
   meaning and is used only when the note cites the compatibility table's recordings.
3. **Two kinds of observation.** For Claude Code the tool list comes from the client's own
   `system/init` event, so it does not depend on what a model says. For Codex, which has no
   such event, the request the client would send a model is captured by a stand-in endpoint
   that logs it and answers with an error; a second run with the real account shows what
   the model then did.
4. **Observations run in a temporary project**, with project-level files or command-line
   options, never with a change to the user's configuration. Kept files have the project
   path, the binary path and the home directory replaced, and the tools of the account's
   connected apps cut. The README lists each replacement.
5. **The note's order follows the reader's questions**: the problem and the definition; a
   summary table with one outcome per client; the decision; a part per client; hooks; the
   `(agent)` executor; the enforcement boundaries; what #53 must do.
6. **Three outcomes**, defined in [contracts/setup-outcome.md](contracts/setup-outcome.md):
   Rune-only, Rune-only within a named scope, registered only. The note's summary table and
   the follow-up text use the same three.
7. **The decision** is made in the note and summarized in research.md R6: Rune writes
   project configuration through issue #53's `rune agent init`, with printing as its
   dry run. Hooks are rejected as the restriction. Launching the restricted agent is
   deferred, with the launch lines documented now.
8. **The guide** gains one bullet at the top of the security model, one sentence in
   "Agent task bodies", each linking to the note, and an entry in "See also".
9. **The follow-up text** is a file here, not an edit of issue #53. The maintainer posts
   it. The pull request links the two issues.

## Complexity Tracking

No violation to justify.
