# Implementation Plan: Reuse of Trusted Receipts in CI

**Branch**: `061-receipt-reuse` | **Date**: 2026-11-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/061-receipt-reuse/spec.md`

## Summary

Close the CI reuse part of issue #56 on top of spec 060:

1. A bare task attribute `[reuse]` marks a task whose run may be skipped on trusted evidence. The
   analyzer refuses it with `[confirm]`, `[plan]`, `[applies]`, `[network]` and the agent
   executor (R1, R10).
2. The run command and `rune ci run` take `--reuse-key FILE` (repeatable) and
   `--reuse-max-age DURATION` (default 24h). Only the keys given count (R2). Without
   `set receipts` or outside a git repository the flag is a usage error (R11).
3. Right before a `[reuse]` task's body, after a `[cache]` miss, the engine takes the snapshot
   the receipt needs anyway (R9), gathers candidates from `.rune/receipts/` and from the notes of
   every commit whose tree is that snapshot (R3), and asks `internal/receipt` for a decision: the
   060 acceptance rules plus freshness, platform, Rune, definition, arguments, overrides and
   output digest (R4, R6, R7).
4. A reused task prints `reused: TASK (...)`, runs no body, and its receipt says `cached` naming
   the reused receipt (R8, R12). A `[reuse]` task that runs prints `not reused: TASK (reason)`.
5. Every receipt records the names of the variables overridden on the command line, an optional
   field, so the format stays version 1 (R5).

Decisions R1 to R14 are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Existing: `internal/receipt` (judging, keys, notes,
outputs), `internal/cache` (output files), `github.com/spf13/cobra` (flags), the analyzer,
parser and language registry. External program: `git`, already required by receipts.

**Storage**: none new. Reads `.rune/receipts/` and `refs/notes/rune-receipts`; writes only the
receipt the run writes anyway.

**Testing**: `go test` in Docker only. Unit tests in `internal/receipt` (decision, notes by tree),
`internal/analyzer`, `internal/parser`, `internal/language`, `internal/cli` (flag checks,
engine hook, notices), `cmd/rune` (flags); fixtures in `testdata/corpus` and `testdata/fmt`; a
binary-level integration test `test/integration/reuse_test.go` with the adversarial fixtures and
a notes round trip through a bare remote; the docs harness.

**Target Platform**: Linux, macOS, Windows (receipts need git on the PATH).

**Project Type**: CLI tool (existing layout)

**Performance Goals**: without `--reuse-key`, no added work. With it, per `[reuse]` task: one
snapshot (shared with the receipt's own, so no extra snapshot), one listing of the receipts
directory, and, once per distinct snapshot, two git calls to find the notes whose commit has
that tree plus one per matching note to read it.

**Constraints**: reuse never writes a ref, a note, the index or the working tree (FR-009). A
reused run is never evidence of a pass (FR-015). No secret in notices or receipts (FR-018). Every
git call carries a context and a timeout (Principle VIII). New prose avoids the em dash
(maintainer rule).

**Scale/Scope**: 1 attribute (`internal/ast`, `internal/parser`, `internal/analyzer`,
`internal/diag`, `internal/language`, the VS Code grammar); 1 new file `internal/receipt/reuse.go`
and one function in `internal/receipt/notes.go`; a field in `receipt.Receipt`; 1 new file
`internal/cli/reuse.go` and hooks in `run.go`, `receipts.go`, `dispatch.go`, `verify.go` (shared
key reading); flags in `cmd/rune/root.go` and `cmd/rune/ci.go`; 1 integration test; docs:
`docs/how-to/receipts.md`, `docs/cli.md`, `docs/GRAMMAR.md`, `docs/runefile.md`,
`docs/diagnostics.md`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Tasks still run by default. Skipping by reuse is per-task opt-in (`[reuse]`), needs a second opt-in from the person running Rune (`--reuse-key`), and every reuse and every refusal to reuse is printed on stderr, as a cache hit is. No timestamp-based skipping: freshness only narrows which receipts count. |
| II. Errors are a feature | PASS | `[reuse]` conflicts are analyzer errors with a code (RUNE2022) and a span; an argument is a parse error. Flag problems are usage errors before any task. Reasons are a fixed, documented list. |
| III. Minimal, total DSL | PASS | One bare attribute: declarative, total, statically analyzable, shipped with `GRAMMAR.md`, registry entry and corpus and format fixtures. No syntax, setting or function. |
| IV. Hand-written front end, idiomatic Go | PASS | The parser gains one name in its bare-attribute case. Decision logic in `internal/receipt`, wiring in `internal/cli`. |
| V. Boringly portable | PASS | Pure Go; git as spec 060 already requires. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first per task; unit, corpus and format fixtures, binary-level adversarial fixtures, notes round trip with a bare remote, docs harness. |
| VII. AI-native, secure by default | PASS | Off by default. Trust only from the command line, never from the Runefile, notes, the environment or this machine's key. `rune mcp`, `rune serve` and the agent executor never reuse. A reused run is recorded as `cached` and never verifies or chains. `[reuse]` refused on destructive, plan, network and agent tasks. |
| VIII. Go engineering discipline | PASS | No new goroutine. The per-snapshot note cache sits under one mutex because parallel tasks share the engine. Errors wrapped with `%w`; git calls carry a context with a timeout. |
| Constraint: Docker-only testing | PASS | All Go tests run in the test container, which has git. |
| Constraint: locked package layout | PASS | No new package. |
| Constraint: backward compatibility | PASS | No existing Runefile changes meaning: `[reuse]` was an unknown attribute (a parse error) before. The receipt gains an optional field that spec 060 readers ignore. Not a breaking change. |
| Constraint: surface changes carry their docs | PASS | `GRAMMAR.md`, `runefile.md`, `diagnostics.md`, `cli.md`, the receipts how-to and help text ship in the same PR. |
| Quality gates | PASS | No gate changes. |

**Post-design re-check**: PASS. The contracts add one attribute, two flags, one diagnostic code,
one optional receipt field and two notice lines. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/061-receipt-reuse/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R14
├── data-model.md        # Phase 1: attribute, reuse options, wanted context, decision, receipt field
├── quickstart.md        # Phase 1: validation walk-through
├── contracts/
│   ├── cli.md           # --reuse-key, --reuse-max-age, notices, reasons, exit status
│   ├── runefile.md      # [reuse] grammar and diagnostics
│   └── receipt.md       # the optional overrides field
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/ast/ast.go              # AttrReuse, KnownAttributes
internal/parser/attribute.go     # reuse in the bare-attribute case
internal/diag/codes.go           # CodeInvalidReuse = "RUNE2022"
internal/analyzer/reuse.go       # checkReuse: duplicates, conflicts, agent executor
internal/analyzer/analyzer.go    # call checkReuse
internal/language/builtin.go     # reuse attribute entry
editors/vscode/syntaxes/runefile.tmLanguage.json  # reuse in the attribute pattern

internal/receipt/receipt.go      # Receipt.Overrides
internal/receipt/verify.go       # shared candidate judging (judgeAll) used by Evaluate and Reuse
internal/receipt/reuse.go        # Want, Reuse, the new reasons
internal/receipt/notes.go        # Repo.NotesForTree

internal/cli/dispatch.go         # Options.ReuseKeys, Options.ReuseMaxAge
internal/cli/reuse.go            # reuser: setup checks, candidates, tryReuse, notices, reason texts
internal/cli/receipts.go         # recorder.overrides; bodyStarting keeps an earlier snapshot; outputs digest without a manifest write
internal/cli/run.go              # reuser setup in execute; tryReuse in executeTask
internal/cli/verify.go           # public key files read by one helper shared with reuse

cmd/rune/root.go                 # --reuse-key, --reuse-max-age on the run command
cmd/rune/ci.go                   # the same flags on ci run

testdata/corpus/reuse.rune, reuse.ast
testdata/fmt/reuse.rune, reuse.fmt
test/integration/reuse_test.go   # end to end with the real binary

docs/how-to/receipts.md          # "Reuse evidence in CI": setup, reasons, GitHub example, trust and limits
docs/cli.md, docs/GRAMMAR.md, docs/runefile.md, docs/diagnostics.md
```

**Structure Decision**: as in spec 060, `internal/receipt` decides from values and knows nothing
of tasks or the CLI: `Reuse` takes candidates, trusted keys and the wanted context and returns a
verdict. `internal/cli/reuse.go` builds the wanted context from the engine, gathers candidates,
prints the notices and marks the receipt run.

## Complexity Tracking

No violation to justify.
