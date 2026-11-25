# Implementation Plan: Learn Rune Tasks from an Agent's Shell Commands

**Branch**: `058-agent-task-suggest` | **Date**: 2026-11-23 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/058-agent-task-suggest/spec.md`

## Summary

Close issue #61. Three steps, each explicit:

1. `rune agent observe claude` adds three background hook handlers to the project's
   `.claude/settings.local.json`, all running `rune agent record`, and `--remove` takes them
   out again. Everything else in the file stays as it was (R2).
2. `rune agent record` reads one hook event, removes secrets, and appends one record to a
   private store outside the project, bounded to 30 days and 4 MiB. It always exits 0 and
   prints nothing (R1, R4 to R6).
3. `rune suggest` reads the records, joins the events of each call, groups repeated commands,
   leaves out what is already a task or a Rune command, and lists suggestions with their task
   text and review items with their reason. `rune suggest apply ID...` appends the chosen tasks
   to the Runefile, or creates it, after analysis, without touching existing bytes.
   `rune suggest forget` deletes the records (R8 to R13).

No recorded command is ever run: commands are parsed as data with the shell parser Rune runs
task bodies with (R7). KISS limits, all in the spec's Out of scope: Claude Code's Bash tool
only, one parameter at most, one-line bodies, no attributes beyond `[working-directory]`.
Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Existing: `mvdan.cc/sh/v3/syntax` (parse and print
shell as data), `github.com/spf13/cobra` (commands), `internal/agentinit` (the JSON editor
and diff), `internal/audit` (state directory, private directory checks, lock),
`internal/mask` (secret names and values), `internal/taskimport` (string literal and brace
escaping, rune command names), `internal/config` (Runefile discovery), `internal/analysis`
(analysis of the new Runefile text), `internal/token` (keywords).

**Storage**: one JSON-lines file and one diagnostics file per project under
`<state>/rune/observe/<id>/`, plus a shared diagnostics file (R4).

**Testing**: `go test` in Docker only. Unit tests per package, hook fixtures from the
documented examples, a race test of concurrent appends, and an integration test with the
real binary that replays fixtures and checks counts, sentinels and secrets (R15). The docs
harness for the new pages.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool (existing layout)

**Performance Goals**: the recorder reads at most 4 MiB, writes at most one 8 KiB line and
waits at most 2 s for the lock; it runs as a background hook, so it never delays the agent.
`rune suggest` reads at most 4 MiB of records.

**Constraints**: no recorded command runs (FR-010, FR-026). No secret in the store, listing,
diff, Runefile or diagnostics (FR-015, FR-050). The recorder exits 0 and prints nothing
(FR-011). No permission rule changes (FR-002). Existing Runefile bytes never change
(FR-040). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 2 new packages (`internal/observe`, `internal/tasksuggest`); additions to
`internal/agentinit` (observation plan, `removeItem`, exported `UnifiedDiff`),
`internal/audit` (three exported helpers), `internal/taskimport` (two exported helpers,
`suggest` in `RuneCommands`); 2 new files in `internal/cli` (`observe.go`, `suggest.go`);
2 new files in `cmd/rune` (`suggest.go`, the `observe` and `record` commands in `agent.go`);
1 integration test file; docs: a how-to page, `cli.md`, `nav.yaml`, the how-to index, and a
link from `agent-setup.md`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Nothing is skipped or cached. Suggestions are only text until a person applies them. |
| II. Errors are a feature | PASS | `apply` analyzes the whole new Runefile before writing and prints errors with spans. A settings file that is not valid JSON is reported with its line and column, as `agent init` reports it. |
| III. Minimal, total DSL | PASS | No syntax, attribute or function is added. Generated tasks use existing forms: a doc comment, `[working-directory]`, one defaulted parameter, `{{quote()}}`. |
| IV. Hand-written front end, idiomatic Go | PASS | The front end is untouched. Two small engine packages are added under `internal/`, as `agentinit` and `taskimport` were. |
| V. Boringly portable | PASS | Pure Go. The shell parser is the one Rune already ships. The handler command has no path or quoting, so it is the same on every system. The state directory follows the audit log's rule on each system. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first in each package, fixtures from the documented hook input, an integration test with the real binary, and the docs harness. |
| VII. AI-native, secure by default | PASS | Recording is opt-in and per project, stores no output, and removes secrets before anything is written. It never changes a permission rule, so the Rune-only profile stays as strict as it is. A generated task makes no read-only claim and no grant; apply says that an agent can call it without approval and that `[confirm]` is the author's to add. |
| VIII. Go engineering discipline | PASS | No goroutine. Lock waits are bounded. Errors wrapped with `%w`. No `init()` or package state beyond constant tables. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. |
| Constraint: locked package layout | PASS | New packages are small focused `internal/` packages; `mcpserver/` is untouched. |
| Constraint: backward compatibility | PASS, noted | No DSL change. `suggest` becomes a built-in command, so a task named `suggest` runs with `rune -- suggest`, as `agent` and `export` did; the commit is marked breaking and the release notes say so. |
| Constraint: surface changes carry their docs | PASS | `docs/cli.md`, a how-to page and the help text ship in the same PR. No grammar change. |

**Post-design re-check**: PASS. The contracts add four commands, one settings-file shape and a
private store. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/058-agent-task-suggest/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R15
├── data-model.md        # Phase 1: event, record, store, diagnostics, suggestions
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli.md           # the commands, their output and exit status
│   └── hooks.md         # the handlers, the accepted input, records, files
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/mask/set.go             # SecretName exported, the rule NewSet applies to names
internal/audit/dir.go            # StateDir (DefaultBase built on it)
internal/audit/log.go            # PrivateDir and LockDir taken out of ensureDir and lockDir

internal/observe/event.go        # Event, DecodeEvent, DropError
internal/observe/redact.go       # omission and redaction (R6), Secrets
internal/observe/record.go       # Record, NewRecord, Encode
internal/observe/store.go        # Store: Append, Read, Prune, Forget, compaction
internal/observe/diagnostics.go  # Diagnostics, CountDrop, ReadDiagnostics
internal/observe/testdata/hooks/ # hook input fixtures

internal/tasksuggest/execution.go  # Executions (R8)
internal/tasksuggest/group.go      # normalization, groups, exclusions, task matching, review (R9)
internal/tasksuggest/family.go     # one varying word (R10)
internal/tasksuggest/suggest.go    # Build, Report, IDs, names, descriptions, order (R11)
internal/tasksuggest/render.go     # task text, Append (R12), the listing text

internal/taskimport/emit.go        # Quote exported (was quote)
internal/taskimport/shellline.go   # EscapeBraces exported (was escapeBraces)
internal/taskimport/finish.go      # "suggest" in RuneCommands

internal/agentinit/observe.go      # PlanObserve, ObserveCommand, ObserveEvents
internal/agentinit/jsonedit.go     # removeItem
internal/agentinit/diff.go         # UnifiedDiff exported

internal/cli/observe.go            # AgentObserve, AgentRecord, recordSecrets
internal/cli/suggest.go            # Suggest, SuggestApply, SuggestForget

cmd/rune/agent.go                  # observe and hidden record subcommands
cmd/rune/suggest.go                # suggest, suggest apply, suggest forget
cmd/rune/main.go                   # register suggest

test/integration/suggest_test.go   # the real binary: scenarios of quickstart.md

docs/how-to/learn-tasks-from-agent.md   # new how-to with the noisy session example
docs/cli.md, docs/nav.yaml, docs/how-to/README.md, docs/how-to/agent-setup.md
```

**Structure Decision**: recording (`internal/observe`) and suggesting
(`internal/tasksuggest`) are separate packages. The recorder runs on every shell call and
needs none of the grouping code, and the suggestion code reads records without knowing about
hooks. Settings editing joins `internal/agentinit`, which already owns Claude Code's project
files. Commands live in `internal/cli` and `cmd/rune` as every other command does.

## Complexity Tracking

No violation to justify.
