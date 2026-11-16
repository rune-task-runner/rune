# Quickstart: Checking the Rune-Only Agent Mode Design Note

The verification target of issue #46 is design review. These steps are what a reviewer can
run and read.

## Prerequisites

- Docker, for the documentation checks.
- For step 4 only: Claude Code or Codex CLI, and a `rune` built from this branch.

## 1. The documentation checks pass

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
```

Expected: `ok`. This covers the links of the note and the guide, the navigation entry, the
generated site pages, the terminology and the secret scan (FR-028).

## 2. Nothing but documentation changed

```sh
git diff --stat main...HEAD -- . ':!docs' ':!specs'
```

Expected: no output (FR-025, SC-008).

## 3. Read the note against the spec

Open `docs/design/rune-only-agent-mode.md`.

| Check | Where | Requirement |
|---|---|---|
| The opening states the problem and defines a Rune-only session | first two sections | FR-002, FR-003 |
| The date and a version for each client | introduction and summary table | FR-004 |
| Five clients, each with one of three outcomes, no empty cell | summary table | FR-005, FR-007, FR-008 |
| Cursor, VS Code and Zed are said to be not tested before the table | paragraph above the table | FR-009 |
| Each client: register, command-running tools, remove, allow, file and level, scope | the client's part | FR-006 |
| Every statement about a client carries a label | all parts | FR-009 |
| Four approaches, one chosen, a reason for each of the others | "Decision" | FR-012, FR-015 |
| The reporting rule | "Decision" | FR-016 |
| What a hook can and cannot do, per client | "Hooks" | FR-013 |
| What the `(agent)` executor does today and what changes | "The agent executor" | FR-014 |
| At least seven boundaries, each with what closes it | "Enforcement boundaries" | FR-017, FR-018, FR-019 |

Then `docs/mcp.md`: the first bullet of "Security model" says that Rune's filters do not
restrict the client's shell, without following a link, and "Agent task bodies" says the
launched agent keeps its own tools (FR-020, SC-007).

## 4. Trace an observed statement

Pick a statement marked "observed" and its id, for example `c3`. Open
[observations/README.md](observations/README.md), find the id, and read the file it names.
The README gives the client version, the project files, the command and the prompt; the
file gives the tool list and the reply (FR-010, SC-005).

To repeat one, follow "Repeating a run" in that README. A run uses a model through your own
account. Use a new directory outside `~/.claude` for each run.

## 5. Check the kept files hold nothing of the machine

```sh
grep -rnE '/home/|/Users/|/tmp/rune-obs|session[_ ]id|toolu_|codex_apps' \
  specs/047-rune-only-agent-mode/observations/ || echo clean
```

Expected: `clean` (FR-027). The README's "What was replaced" lists each replacement.

## 6. Read the follow-up text against issue #53

Open [follow-up-issue-53.md](follow-up-issue-53.md) beside
<https://github.com/rune-task-runner/rune/issues/53>. Claude Code, Codex CLI, Cursor and VS
Code each have supported, partial or not supported, with a scope and a reason; every
correction names the statement of #53 it corrects (FR-021 to FR-024, SC-006).

It was posted on issue #53 as a comment on 2026-11-16.

## 7. The helper tests pass

```sh
python3 -B -m unittest discover -s specs/047-rune-only-agent-mode/observations/codex
```

Expected: `OK`, 25 tests.
