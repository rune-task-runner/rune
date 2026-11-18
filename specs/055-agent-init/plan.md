# Implementation Plan: Set Up an Agent Client for a Rune-only Workflow with `rune agent init`

**Branch**: `055-agent-init` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/055-agent-init/spec.md`

## Summary

Close issue #53. `rune agent init claude|codex|vscode` writes the project files that register
`rune mcp` with a client, remove the client's command-running tools and allow Rune's, and it
ends by naming one of the three outcomes of spec 047.

The design is one pure planner and a thin command (R1). The planner takes the existing bytes
of each client file and returns the new bytes, the conflicts, the state of each owned entry
and the outcome. It edits text in place: every change is a splice at a position a parser gave
(R2), with a small hand-written reader for JSON with comments (R3) and
`pelletier/go-toml/v2` for TOML (R4). Each planned file is verified by comparing the meaning
of the old and new text before anything is written (R6). The command reads the files, asks
the client for its version (R8), prints the report or the difference, and writes through a
temporary file, refusing any path that leaves the project (R7).

A real run, `--dry-run` and `--check` share the one plan (R11), so they cannot disagree.

KISS limits, all in the spec's Out of scope: no remove command, no server flag passthrough,
no reading of trust records, a text report only. Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: one new module, `github.com/pelletier/go-toml/v2` v2.4.3 (MIT, no
dependencies of its own), for reading `.codex/config.toml` with positions. Existing:
`encoding/json` (rendering values, test oracle), `internal/semver`, `internal/diag`,
`internal/analysis`, `internal/config`, `github.com/spf13/cobra`.

**Storage**: none. Up to three small files in the project, each written once through a
temporary file after every check passed.

**Testing**: `go test` in Docker only. In `internal/agentinit`: unit tests for every
function, golden tests per client over merge fixtures (existing files, expected files,
expected report), and a fuzz target for the JSON reader against `encoding/json`. Command
tests in `internal/cli` and `cmd/rune`. Binary-level tests in `test/integration` for the file
system rules (symbolic links, dry run, idempotence, exit codes, a fake client on `PATH`). The
docs harness for the new page. Recorded client runs in `observations/` for the versions the
adapters claim.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool (existing layout)

**Performance Goals**: none. The files are a few dozen lines; the one external call has a
five second limit.

**Constraints**: nothing of the Runefile runs (FR-004). Nothing outside the project is
written (FR-005). Bytes outside owned entries are unchanged (FR-017). No outcome stronger
than "Registered only" for a version without a recorded run (FR-030, FR-031). `--check`
starts no program (FR-026). New prose
avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package (`internal/agentinit`, 10 source files plus tests and
testdata), 1 new file in `internal/cli`, 1 new file in `cmd/rune`, a one-line change in
`cmd/rune/main.go`, one name added to `internal/taskimport/finish.go`, 1 new integration
test file, 1 new documentation page and edits to 8 existing pages, the CI golden gate and
fuzz-smoke job, one new module in `go.mod`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Not touched. The command runs no task. |
| II. Errors are a feature | PASS | A conflict and a client file that cannot be parsed are reported with `file:line:col`, the line and a caret, through `internal/diag`. The Runefile is analyzed before anything is written. Nothing is written when any file of the client has a conflict. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS, with two justified additions | No change to the lexer or parser. One new package and one new module; see Complexity Tracking. The JSON reader is hand-written. |
| V. Boringly portable | PASS | Pure Go. The new module is pure Go with no dependencies. Paths are written as the host spells them and escaped by the encoder; docs use forward slashes. `PowerShell` is in the deny list so the Windows client loses its shell too. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first: golden merge fixtures, unit tests, a fuzz target, binary-level tests, the docs harness. Golden files are regenerated with `-update`, never edited by hand. |
| VII. AI-native, secure by default | PASS | The setup writes no `--allow-destructive`, no approval setting that covers `[confirm]` tasks, no credential, and nothing outside the project. It reports an outcome no stronger than what was checked. The command is not exposed over MCP. |
| VIII. Go engineering discipline | PASS | No goroutine, no global state, no `init()`. The one external call has a context with a timeout. Errors are wrapped with `%w`. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. The recorded client runs are observations, not tests, as in spec 047. |
| Constraint: backward compatibility | PASS, with a recorded change | No DSL change. The command takes the name `agent` on the command line. A task named `agent` is still run with `rune -- agent` and is still an MCP tool; `rune agent` prints a note when the Runefile has one, and fails with a usage error for any argument that is not a subcommand, so no script succeeds while doing nothing (R12). The release notes call it out, as for `log`. |
| Constraint: surface changes carry their docs | PASS | No DSL surface changes. The CLI reference, help and the new how-to page ship in the same PR. |

**Post-design re-check**: PASS. The contracts add one command with five flags, one package
and one module. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/055-agent-init/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R13
├── data-model.md        # Phase 1: adapter, inputs, file, entry, conflict, outcome
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── rune-agent-init.md   # the command: flags, report, diagnostics, exit codes
│   └── client-files.md      # per client: files, owned entries, merge rules
├── observations/            # recorded client runs for the versions the adapters claim
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/agentinit/
├── agentinit.go         # Client, Inputs, File, Entry, Conflict, Outcome, Result, errors;
│                        # ParseClient, Plan
├── adapter.go           # the adapter value, the three adapters, checked versions, outcome
├── claude.go            # plan for .mcp.json and .claude/settings.json
├── codex.go             # plan for .codex/config.toml
├── vscode.go            # plan for .vscode/mcp.json, .mcp.json and the agent file
├── jsonedit.go          # JSON reader with positions; jsonSet, jsonAppend
├── tomledit.go          # tomlSet on go-toml's unstable parser
├── diff.go              # unified difference of two texts
├── report.go            # the report text and the check text
├── *_test.go            # unit tests per file, fuzz target, golden test over testdata
└── testdata/<client>/<case>/{in/..., want/..., want.report}

internal/cli/agentinit.go       # AgentInit(opts, flags, client): Runefile, files, version,
                                # report, difference, write, exit status
internal/cli/agentinit_test.go
cmd/rune/agent.go               # the cobra commands `agent` and `agent init`, flags,
                                # completion of the client name
cmd/rune/agent_test.go
cmd/rune/main.go                # register newAgentCmd
internal/taskimport/finish.go   # `agent` joins the names that are commands

test/integration/agent_init_test.go   # binary level: symbolic links, dry run, check,
                                      # idempotence, exit codes, a fake client on PATH

docs/how-to/agent-setup.md            # the command, per client files, outcomes, steps,
                                      # manual checks, removing a setup
docs/how-to/README.md, docs/nav.yaml  # list the new page
docs/cli.md                           # the command row and its exit status
docs/mcp.md                           # pointers from Client compatibility and Agent task
                                      # bodies; RUNE_MCP_URL and RUNE_MCP_TOKEN (FR-038)
docs/design/rune-only-agent-mode.md   # the command exists now (FR-039)
docs/use-cases/mcp-agents.md          # one pointer

docs/how-to/import-from-other-runners.md,
docs/troubleshooting.md               # `agent` in the lists of command names

.github/workflows/ci.yml              # add internal/agentinit to the golden gate and the
                                      # JSON fuzz target to fuzz-smoke
go.mod, go.sum                        # the new module
```

**Structure Decision**: the existing single-module CLI layout. The planner lives in one new
internal package that imports nothing of the runtime; `internal/cli` owns files, the external
call and exit codes; `cmd/rune` owns flags.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| One new package, `internal/agentinit`, in a locked layout (Principle IV) | Editing three clients' configuration files in place is a self-contained concern with its own fixtures. It imports the standard library, `internal/semver` and one TOML module, and nothing imports it but `internal/cli` | Putting it in `internal/cli` would add about 1,200 lines of file editing to the package that wires the command line. Specs 052 to 054 added `internal/audit`, `internal/sandbox` and `internal/taskimport` the same way |
| One new module, `github.com/pelletier/go-toml/v2` | `.codex/config.toml` has to be validated before and after an edit, and edited at exact positions so comments survive (FR-017) | A TOML reader of our own is several hundred lines of grammar with no other use in Rune. A line scanner is fooled by multi-line strings. `BurntSushi/toml` gives no positions (R4) |
| The module's `unstable` package is used | It is the only API of the module that gives byte ranges | The stable API decodes values only. The version is pinned and every call is under test, so a change in a later release is caught when the module is upgraded |
