# Implementation Plan: Export AGENTS.md Guidance and a Claude Skill from the Runefile with `rune export`

**Branch**: `056-agent-docs-export` | **Date**: 2026-11-19 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/056-agent-docs-export/spec.md`

## Summary

Close issue #54. `rune export agents-md|claude-skill` writes, from the Runefile, the text an
agent without MCP needs: how tasks are called, an entry per public task, and the limits the
author declared. It prints by default, writes with `--write` or `--output`, and finds stale
text with `--check`.

The design is one pure package and a thin command (R1). `internal/agentdocs` builds one
catalog from the analyzed Runefile (R3, R4), renders the same body for both formats, and
plans file changes from the existing bytes of the targets (R12, R13, R15). The command loads
the Runefile statically, with no evaluation and no environment (R2), and takes the reach of
a task from the scheduler's own walk, made host independent (R5).

Text from the Runefile reaches the output through three renderers only (R8). No backtick and
no `$` from the Runefile is ever written, because Claude Code runs `` !`cmd` `` and replaces
`$` placeholders when it loads a skill (R9), and `@` in prose is written as an entity
because instruction files import `@path` (R10).

KISS limits, all in the spec's Out of scope: two formats, fixed limits, no `--force`, no
evaluation, a text report. Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Existing: `crypto/sha256`, `encoding/json` (the
fingerprint), `internal/ast`, `internal/formatter` (canonical text of a default expression),
`internal/parser`, `internal/config`, `internal/analyzer`, `internal/diag`,
`github.com/spf13/cobra`. In tests only: `mvdan.cc/sh/v3/syntax` (examples parse as POSIX
shell) and `go.yaml.in/yaml/v3` (frontmatter parses), both already in `go.mod`.

**Storage**: none. One Markdown file, or up to two files in a skill directory, each written
once through a temporary file after every check passed.

**Testing**: `go test` in Docker only. In `internal/agentdocs`: unit tests for every
function, on values built in the test, with no Runefile and no disk. In `internal/cli`: a
golden test over fixture Runefiles through the real load and the real reach walk
(`testdata/export/<case>/Runefile` with `want.agents.md`, `want.SKILL.md` and, above the
inline limit, `want.tasks.md`), every example of every fixture bound by `splitArgs` and
`bindParams`, the static load, and the write and check paths. Flag tests in `cmd/rune`. Binary-level tests
in `test/integration` (nothing runs, no environment value in the output, symbolic links,
exit codes, idempotence). The docs harness for the new page.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool (existing layout)

**Performance Goals**: none. A Runefile of a thousand tasks renders in milliseconds.

**Constraints**: nothing of the Runefile runs or is evaluated (FR-004). The bytes do not
depend on the host, the directory or the environment (FR-040). Bytes outside Rune's section
are unchanged (FR-029). No `--yes` and no workaround in any output (FR-021). No backtick and
no `$` from the Runefile in any output (FR-024a). New prose avoids the em dash (maintainer
rule).

**Scale/Scope**: 1 new package (`internal/agentdocs`, 6 source files plus tests and
testdata), 1 new file in `internal/cli`, 1 new file in `cmd/rune`, small refactors of
`internal/cli/serve.go` (the static half of the load), `internal/cli/mcp.go` (the reach walk
as a function) and `internal/cli/agentinit.go` (the write helper takes planned files), one
name added to `internal/taskimport/finish.go`, 1 new integration test file, 1 new
documentation page and edits to about 8 existing pages. The CI golden gate already covers
`internal/cli`, so the workflow does not change.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Not touched. The command runs no task. |
| II. Errors are a feature | PASS | The Runefile is analyzed before anything is produced, and its diagnostics are the usual ones. Broken marker lines are reported with `file:line`, the line and a caret, through `internal/diag`. |
| III. Minimal, total DSL | PASS | No DSL change. The export evaluates no expression. |
| IV. Hand-written front end, idiomatic Go | PASS, with one justified addition | No change to the lexer or parser. One new package; see Complexity Tracking. |
| V. Boringly portable | PASS | Pure Go, no new module. The output is the same bytes on the three systems. Examples are POSIX shell, the same on every host, and the text says so. Paths in reports use forward slashes. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first: golden fixtures, unit tests, command tests, binary-level tests, the docs harness. Golden files are regenerated with `-update`, never edited by hand. |
| VII. AI-native, secure by default | PASS | The export states the author's `[confirm]`, `[agent-allow]`, `[plan]` and `[applies]` and never offers a way around them. It says that it is guidance. No secret can reach it: nothing of the environment is read. Text from the Runefile cannot run a command or import a file when a client loads the output (R9, R10). The command is not exposed over MCP. |
| VIII. Go engineering discipline | PASS | No goroutine, no global state beyond constants, no `init()`. No external call. Errors are wrapped with `%w`. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. |
| Constraint: backward compatibility | PASS, with a recorded change | No DSL change. The command takes the name `export` on the command line. A task named `export` is still run with `rune -- export` and is still an MCP tool; `rune export` prints a note when the Runefile has one, and fails with a usage error for an argument that is not a format, so no script succeeds while doing nothing (R16). The release notes call it out, as for `agent` and `log`. |
| Constraint: surface changes carry their docs | PASS | No DSL surface changes. The CLI reference, the help and the new how-to page ship in the same PR. |

**Post-design re-check**: PASS. The contracts add one command with four flags and one
package, and no module. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/056-agent-docs-export/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R17
├── data-model.md        # Phase 1: source, catalog, entry, param, reached, file, refusal
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── rune-export.md   # the command: flags, streams, reports, refusals, exit codes
│   └── export-text.md   # the exact text: renderers, body, entry, section, skill, fingerprint
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/agentdocs/
├── doc.go               # package comment
├── catalog.go           # Source, Catalog, Entry, Param, Reached; Build, Fingerprint,
│                        # Usage, Example
├── text.go              # prose, value, code, shQuote
├── render.go            # body, entry, section, skill files, description
├── merge.go             # marker scanning, mergeSection, skill file ownership, Refusal
├── plan.go              # Format, ParseFormat, File, Plan, Status, CheckState
└── *_test.go            # unit tests per file, on values built in the test

internal/cli/export.go        # Export(opts, flags, format): static load, catalog, targets,
                              # print, write, check, exit status
internal/cli/export_test.go   # command tests, and the golden test over
internal/cli/testdata/export/<case>/{Runefile, want.agents.md, want.SKILL.md, want.tasks.md}
internal/cli/serve.go         # loadStatic: the static half of loadModuleFrom, shared
internal/cli/mcp.go           # reachTasks as a function of the task index and a predicate
internal/cli/agentinit.go     # the write-with-rollback helper takes planned files and can
                              # remove one
internal/formatter/formatter.go  # Expr: the canonical text of an expression, exported
cmd/rune/export.go            # the cobra command, flags, completion of the format
cmd/rune/export_test.go
cmd/rune/main.go              # register newExportCmd
internal/taskimport/finish.go # `export` joins the names that are commands

test/integration/export_test.go   # binary level: nothing runs, no environment value,
                                  # stdout only by default, write and check exit codes,
                                  # symbolic link, a task named export

docs/how-to/agent-instructions.md     # the command, both formats, markers, check in CI,
                                      # how each client reads the output, guidance against
                                      # enforcement, removing an export
docs/how-to/README.md, docs/nav.yaml  # list the new page
docs/cli.md                           # the command row, `export` among the command names
docs/mcp.md                           # one pointer: agents without MCP
docs/use-cases/mcp-agents.md          # one pointer
docs/how-to/agent-setup.md            # one pointer: the export is guidance, the setup
                                      # removes the shell
docs/how-to/import-from-other-runners.md,
docs/troubleshooting.md               # `export` in the lists of command names
```

**Structure Decision**: the existing single-module CLI layout. The catalog, the text and the
merge live in one new internal package that imports `internal/ast` and
`internal/formatter` and nothing of the runtime; `internal/cli` owns files and exit codes;
`cmd/rune` owns flags.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| One new package, `internal/agentdocs`, in a locked layout (Principle IV) | Rendering a catalog as Markdown and merging it into files is a self-contained concern with its own fixtures. It imports the standard library, `internal/ast` and `internal/formatter`, and nothing imports it but `internal/cli` | Putting it in `internal/cli` would add about 900 lines of text rules to the package that wires the command line. Specs 052 to 055 added `internal/audit`, `internal/sandbox`, `internal/taskimport` and `internal/agentinit` the same way |
| Three small refactors of existing code (the static load, the reach walk, the write helper) | Each removes a second copy the feature would otherwise add: of what a valid Runefile is, of which tasks a run reaches, and of how files are written with rollback | Copying the three would leave two definitions of each, and the reach walk has a rule (a `\|\|` hook does not run its own `\|\|` hooks) that a copy would get wrong over time |
