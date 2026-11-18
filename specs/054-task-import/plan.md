# Implementation Plan: Import Tasks from a justfile, Makefile, Taskfile or package.json with `rune import`

**Branch**: `054-task-import` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/054-task-import/spec.md`

## Summary

Close issue #52. `rune import` reads one task file of another runner and writes a Runefile.

The design is one small pipeline (R1). A reader per format turns the source text into a list
of plain task records and file-level items; it reads text only and starts nothing (R2). One
shared step maps the names, resolves dependencies, applies the file-wide downgrade and turns
every unsupported task into a failing task or, with `--wrap`, a wrapper (R6, R7, R8). One
emitter writes the Runefile text and the report (R9). The command then checks the text with
Rune's own parser and analyzer and writes it only when it has no error (R10).

Behavior is kept with three rules, not with per-format machinery: a line that changes shell
state is wrapped in a subshell when the source runner starts a shell per line (R4); a line
Rune's shell cannot parse makes the task unsupported (R4); text the source passes through
unchanged is escaped so Rune prints it unchanged (R5).

KISS limits, all in the spec's Out of scope: no merge mode, one source per run, a text report
only. Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `go.yaml.in/yaml/v3` reads the Taskfile with line
numbers, `mvdan.cc/sh/v3/syntax` parses command lines (no execution), `encoding/json` reads
`package.json`, `github.com/spf13/cobra` for the command. No new modules.

**Storage**: none. One output file, written once, only after every check passed.

**Testing**: `go test` in Docker only. Golden tests per format in `internal/taskimport`
(source, expected Runefile, expected report), unit tests for every function, command tests in
`internal/cli` and `cmd/rune`, binary-level parity and safety tests in `test/integration`, and
the docs harness for the new page.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool (existing layout)

**Performance Goals**: none beyond "instant" for a task file of a few hundred lines. No
profile, no optimization.

**Constraints**: import is static: no process is started and nothing but the source file is
read (FR-006). The source is never written (FR-007). Nothing is written unless the Runefile
analyzes without error (FR-010). Output and report are deterministic (FR-014). New prose
avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package (`internal/taskimport`, 11 source files plus tests and
testdata), 1 new file in `internal/cli`, 1 new file in `cmd/rune`, a one-line change in
`cmd/rune/main.go`, 1 new integration test file with fixtures, 1 new documentation page and
edits to 6 existing pages, and one line in the CI golden gate.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Make's timestamp rules are reported, never imitated. Task's `sources` become an explicit `[cache]`, which is opt-in and logged, with status review. |
| II. Errors are a feature | PASS, with one recorded limit | A source that cannot be parsed fails with `file:line:col`, the line and a caret, rendered by `internal/diag`, and exit 3. Every unsupported construct is reported with `file:line` and a reason; see Complexity Tracking. The Runefile is analyzed before it is written, so the command never produces a file that fails static analysis. |
| III. Minimal, total DSL | PASS | No DSL change. The emitter uses existing syntax only. |
| IV. Hand-written front end, idiomatic Go | PASS, with one justified addition | No change to the lexer or parser. The readers for just and make are hand-written line readers. One new package, `internal/taskimport`. See Complexity Tracking. |
| V. Boringly portable | PASS | Pure Go, no new module. Imported tasks run in the built-in shell. The npm path separator is chosen per OS in the Runefile. Paths in the docs use forward slashes. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first: golden files per format, unit tests, binary-level parity and safety tests, docs harness. Golden files are regenerated with the existing `-update` convention, never edited by hand. |
| VII. AI-native, secure by default | PASS | Import executes nothing and fetches nothing. No secret handling changes. The command is not exposed over MCP. |
| VIII. Go engineering discipline | PASS | No goroutine, no global state, no `init()`. Errors are wrapped with `%w`. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | All gates run in the test container. |
| Constraint: backward compatibility | PASS | No DSL change. `import` is already a reserved word of the Runefile language, so no task can have that name and the new command hides no task (R11). |
| Constraint: surface changes carry their docs | PASS | No DSL surface changes. The CLI reference, help and the new how-to page ship in the same PR. |

**Post-design re-check**: PASS. The contracts add one command with five flags and one
package. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/054-task-import/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R14
├── data-model.md        # Phase 1: source, task record, item, status, result
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── rune-import.md       # the command: flags, detection, outputs, exit codes, messages
│   └── support-matrix.md    # per format: what converts, what is review, what is unsupported
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/taskimport/
├── taskimport.go        # Format, Status, Task, Item, Result, Options; ParseFormat, FormatOf,
│                        # Candidates, Convert
├── vars.go              # which variables convert (shared by just, make and Task)
├── finish.go            # shared step: names, dependencies, downgrade, stubs, wrappers
├── names.go             # the name mapping (FR-020)
├── shellline.go         # parse a command line, wrap state changes, escape "{{"
├── emit.go              # Runefile text and report text
├── just.go              # justfile reader
├── make.go              # Makefile reader
├── taskfile.go          # Taskfile reader
├── npm.go               # package.json reader
├── *_test.go            # unit tests per file, golden test over testdata
└── testdata/<format>/<case>/{source, want.Runefile, want.report}

internal/cli/import.go        # Import(opts, flags): resolve source and output, convert,
                              # self-check, write, exit status
internal/config/discover.go   # export the Runefile name matcher (IsRunefileName)
internal/cli/import_test.go
cmd/rune/import.go            # the cobra command, flags, --from completion
cmd/rune/import_test.go
cmd/rune/main.go              # register newImportCmd

test/integration/import_test.go            # parity, safety, dry-run, exit codes, completion
test/integration/testdata/import/<format>/ # one small project per format

docs/how-to/import-from-other-runners.md   # support matrix, statuses, wrappers, walkthrough
docs/how-to/README.md, docs/nav.yaml       # list the new page
docs/cli.md                                # the command row, exit status note
docs/how-to/imports-and-modules.md,
docs/runefile.md                           # one sentence each: the statement is not the command
docs/getting-started.md                    # one pointer for people coming from another runner

.github/workflows/ci.yml                   # add internal/taskimport to the golden gate
```

**Structure Decision**: the existing single-module CLI layout. Conversion logic lives in one
new internal package with no dependency on the runtime; `internal/cli` owns files and exit
codes; `cmd/rune` owns flags.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Report lines carry `file:line`, not `file:line:col` with a caret (Principle II) | A report line names a construct that was not converted, such as a recipe or a setting. It is a listing of 1 to 50 lines that a person scans, and it is not an error of the file | A caret block under every line would triple the report's length and point at the start of a line the person can already find. Errors of the source do get the column and the caret |
| One new package, `internal/taskimport`, in a locked layout (Principle IV) | Reading four foreign formats is a self-contained concern with its own fixtures. It imports only `mvdan.cc/sh/v3/syntax`, `go.yaml.in/yaml/v3` and the standard library, and nothing imports it but `internal/cli` | Putting it in `internal/cli` would add about 1,500 lines of format reading to the package that wires the command line, and its golden fixtures would sit beside unrelated ones. Earlier specs added `internal/audit`, `internal/planstore` and `internal/sandbox` the same way |
