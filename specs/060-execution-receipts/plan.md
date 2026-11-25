# Implementation Plan: Signed Execution Receipts, `rune verify` and Git Notes

**Branch**: `060-execution-receipts` | **Date**: 2026-11-25 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/060-execution-receipts/spec.md`

## Summary

Close the verification part of issue #56:

1. `set receipts` turns recording on. The CLI engine wraps `Execute` and `ExecuteFailHook` with a
   recorder (R1), so every body Rune runs, from any surface, gets one receipt: status and exit
   code from Rune's own classification (R2), git tree snapshots right before and after the body
   through a private index (R3, R4), Runefile, input and output coverage (R5), relationships to
   dependency, post-hook, failure-hook and cache-source receipts (R6, R8), channel and claims
   (R7), all masked (R16).
2. Each receipt is a DSSE envelope over an in-toto statement, signed with Ed25519 (R9), by a key
   from `RUNE_RECEIPT_KEY` (removed from the environment at start) or a local key created on
   first use in the user's state directory and hidden from sandboxed bodies (R10). It is written
   atomically to `.rune/receipts/ID.json` (R11).
3. `rune verify REVISION TASK...` checks local receipts and the revision's note against trusted
   keys and the revision's tree, reports one verdict per task with a fixed reason list, the state
   of the working tree, text or JSON, and exits 0 only when every task is verified (R12).
4. `rune receipt attach` merges receipts bound to a revision into `refs/notes/rune-receipts`;
   `rune receipt key` prints the public key (R13). `verify` and `receipt` become built-ins (R14).

Decisions R1 to R17 are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Standard library: `crypto/ed25519`, `crypto/x509`,
`encoding/pem`, `encoding/base64`, `crypto/sha256`, `crypto/rand`, `os/exec` (git). Existing:
`github.com/spf13/cobra` (commands), `internal/cache` (input globs, output files, record field),
`internal/audit` (`StateDir`, `NewID`), `internal/mask` (via the CLI's masker), `mcpserver`
(`CallAudit.Session()`), `internal/taskimport` (`RuneCommands`), `internal/language` (setting
registry). External program: `git`, only when receipts are on or a receipt command runs.

**Storage**: files: `.rune/receipts/` in the project, `STATE/receipts/key.pem` for the local key,
git objects (unreachable blobs and trees from snapshots) and the `refs/notes/rune-receipts` ref.

**Testing**: `go test` in Docker only. Unit tests in `internal/receipt`, `internal/cache`,
`internal/config`, `internal/language`, `internal/cli`, `mcpserver`, `cmd/rune`; a binary-level
integration test `test/integration/receipts_test.go`; the docs harness for the new page.

**Target Platform**: Linux, macOS, Windows (receipts need git on the PATH).

**Project Type**: CLI tool (existing layout)

**Performance Goals**: with receipts off, no added work (no git call, no key read). With them on,
two snapshots per body; a snapshot reuses git's stat cache through the copied index, so its cost
is close to `git status` for an unchanged tree. Verification is linear in candidate receipts.

**Constraints**: snapshots never change the index, HEAD, refs, notes or working tree (FR-010).
Verification writes nothing (FR-034). No secret, environment value or output in a receipt
(FR-015, FR-042). Signer failures never change a task's exit code (FR-024). Every git call has a
context and a timeout (Principle VIII). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package `internal/receipt` (format, DSSE, keys, store, git snapshot and
notes, evaluation); 1 accessor in `mcpserver`; a field in `internal/cache`; 1 setting in
`internal/config` and `internal/language`; new files `internal/cli/receipts.go` (recorder) and
`internal/cli/verify.go` (commands); hooks in `run.go`, `mcp.go`, `sandbox.go`, `dispatch.go`;
new `cmd/rune/verify.go` and `cmd/rune/receipt.go`; 2 names in `RuneCommands`; 1 integration
test; docs: a how-to page, `cli.md`, `GRAMMAR.md`, `settings-and-dotenv.md`, `nav.yaml`, the
how-to index.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Receipts never skip a body. A cache hit is still Rune's own decision and logged; its receipt says `cached` and names the run it reuses. Verification runs nothing. |
| II. Errors are a feature | PASS | `set receipts` is validated by the analyzer like other settings. Signer problems stop the run before any body with one clear error; verify reasons are a fixed, documented list. |
| III. Minimal, total DSL | PASS | One boolean setting, total and statically known, shipped with `GRAMMAR.md`, registry entry and fixtures. No syntax, attribute or function. |
| IV. Hand-written front end, idiomatic Go | PASS | Front end untouched. One focused `internal/receipt` package. `mcpserver` gains one nil-safe accessor, an additive public change. |
| V. Boringly portable | PASS, noted | Pure Go; Ed25519 from the standard library. `git` is required where receipts are recorded or verified, as the plan journal (spec 051) already uses it. Key file permissions are advisory on Windows; documented. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first per task; unit, binary-level integration (tamper, notes round trip with a bare remote, secrets), docs harness. |
| VII. AI-native, secure by default | PASS | Off unless the project opts in. Keys outside the project; the local key directory hidden from sandboxed agent tasks; the CI key removed from every environment; claims never treated as identity; secrets masked; notes pushed only by explicit git commands. |
| VIII. Go engineering discipline | PASS | No new goroutines. Recorder state under one mutex. Errors wrapped with `%w`. Every git call carries a context with a timeout. Writes atomic. |
| Constraint: Docker-only testing | PASS | All Go tests run in the test container, which has git. |
| Constraint: locked package layout | PASS | `internal/receipt` is a focused package like `audit`, `planstore` and `ci`; `mcpserver/` stays public. |
| Constraint: backward compatibility | PASS, noted | No existing Runefile changes meaning. `verify` and `receipt` become built-in commands, so tasks of those names run with `rune -- NAME`, as for `ci`; the commit is marked breaking. The cache record gains an optional field that old readers ignore. |
| Constraint: surface changes carry their docs | PASS | `GRAMMAR.md`, `cli.md`, a how-to page and help text ship in the same PR. |
| Quality gates | PASS | No gate changes. |

**Post-design re-check**: PASS. The contracts add one setting, two commands, one environment
variable, a file format and a notes ref. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/060-execution-receipts/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R17
├── data-model.md        # Phase 1: receipt, snapshot, envelope, keys, note, verdict
├── quickstart.md        # Phase 1: validation walk-through
├── contracts/
│   ├── cli.md           # setting, environment, rune verify, rune receipt key|attach
│   └── receipt.md       # receipt format version 1, envelope, notes
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/receipt/doc.go          # package overview and trust model in brief
internal/receipt/receipt.go      # Receipt, Source, Outputs, Claims...; statuses, channels, roles; NewID
internal/receipt/dsse.go         # Envelope, PAE, Sign, Open (signature + statement decode)
internal/receipt/key.go          # Signer, ParsePrivateKey, ParsePublicKeys, KeyID, LoadOrCreate, PublicPEM
internal/receipt/store.go        # Dir, Write (atomic, no replace), WriteManifest, List
internal/receipt/git.go          # Repo, Open, Snapshot, Head, Resolve, WorktreeMatches, CheckIgnore
internal/receipt/notes.go        # ReadNote, Attach (merge, retry)
internal/receipt/verify.go       # Candidate, Evaluate, Verdict, reasons
internal/receipt/outputs.go      # output digests, manifest digest, comparison with current files

internal/cache/cache.go          # Fingerprint.Receipt, Decision.Receipt, Store(..., receipt)
internal/config/settings.go      # Settings.Receipts, case "receipts"
internal/language/builtin.go     # receipts setting entry

internal/cli/receipts.go         # recorder: prepare signer, begin, bodyStarting, cachedHit, finish, relations, channel, claims
internal/cli/verify.go           # Verify, ReceiptKey, ReceiptAttach, task notes
internal/cli/run.go              # Execute / ExecuteFailHook wrappers, recorder setup in execute, Store receipt ID
internal/cli/mcp.go              # recorder per adapter, invocation = call ID, claims from CallAudit.Session()
internal/cli/sandbox.go          # hide STATE/receipts
internal/cli/dispatch.go         # Options.ReceiptKey, Options.Commit

mcpserver/audit.go               # CallAudit.Session()

internal/taskimport/finish.go    # "verify", "receipt" in RuneCommands

cmd/rune/verify.go               # verify command
cmd/rune/receipt.go              # receipt, receipt key, receipt attach
cmd/rune/root.go                 # read and unset RUNE_RECEIPT_KEY; Options.Commit
cmd/rune/main.go                 # register commands

test/integration/receipts_test.go  # end to end with the real binary

docs/how-to/receipts.md          # new: setup, trust model, CI, notes, verify reference
docs/cli.md, docs/GRAMMAR.md, docs/how-to/settings-and-dotenv.md, docs/how-to/README.md, docs/nav.yaml
```

**Structure Decision**: format, signing, storage, git plumbing and evaluation live in
`internal/receipt`, which knows nothing about tasks, schedulers or the CLI: it takes values and
returns values. The CLI's recorder turns an engine's execution into a `receipt.Receipt`, signs
and stores it; the commands in `internal/cli/verify.go` wire `receipt` functions to flags and
output, as every other command does.

## Complexity Tracking

No violation to justify.
