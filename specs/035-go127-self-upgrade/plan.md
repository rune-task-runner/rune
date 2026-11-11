# Implementation Plan: Go 1.27 Toolchain and `rune --self-upgrade`

**Branch**: `035-go127-self-upgrade` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/035-go127-self-upgrade/spec.md`

## Summary

Close issue #84 in two parts that land in order.

**Part A, toolchain.** Move `go.mod`, the three workflows and both Dockerfiles from Go 1.25 to 1.27,
state one source-build requirement in the docs, and raise the linter to a build that understands Go
1.27 (R1 to R5). The current tree already builds, vets and formats cleanly on go1.27.4 (measured,
R3). Part A goes first because the verification library in Part B needs `go 1.27.0`.

**Part B, self-upgrade.** Add `--self-upgrade` to the root command. A new package,
`internal/selfupgrade`, decides who owns the install, finds the latest stable release, proves
`checksums.txt` was signed by the release workflow using `sigstore-go` in process, checks the
archive against it, extracts the one binary to a staging file beside the installed one, and swaps it
in with a rename (R6 to R16). Conflicting arguments are rejected before Cobra dispatches, and the
upgrade starts before Runefile discovery (R15). Nothing else in Rune touches the network (R19).

Two findings shaped the design. The published signature file is in cosign's older bundle form, so
the updater reads both forms through one verifier (R9). The signing certificate's subject is the
release workflow on `main`, so the updater pins that exact value, tighter than the pattern the docs
show today (R8). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`, built with the latest 1.27 patch, go1.27.4
today), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing ones, plus `github.com/sigstore/sigstore-go` v1.4.0 (new, with
about 67 modules behind it). No other new direct dependency: file replacement, locking, archive
reading and HTTP use the standard library.

**Storage**: N/A. No state between runs. During a run, a lock file and staging files sit beside the
binary and are removed before exit ([data-model.md](data-model.md)).

**Testing**: `go test` in Docker only. Package tests in `internal/selfupgrade` with an `httptest`
release server and sigstore-go's virtual Sigstore; root command tests in `cmd/rune`; binary-level
tests in `test/integration` built with `-tags runetest`; the docs harness in `test/docs` (R18).

**Target Platform**: Linux, macOS 13 or newer, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: One upgrade completes in under a minute on broadband (SC-001). No cost to any
other command: the updater is built only when the flag is set.

**Constraints**: Nothing is extracted or replaced before signature and checksum pass (FR-009 to
FR-011). The installed binary survives every failure (FR-018). No elevated rights, no other program
started (FR-019). Bounded waits and sizes (R16). No network outside the flag (FR-005). Release file
list unchanged (FR-036). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package of about 12 source files with tests, plus its test-support package
`upgradetest`; 5 touched files in `cmd/rune` plus 3 new ones; 1 new error type in `internal/cli`; 8
files for the version change; 2 Dockerfiles gain one line; 5 documents; 4 new integration test
files. Stripped binary grows from
about 11.6 MB to about 24 MB (R6).

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No change to how tasks run. The flag runs no task. |
| II. Errors are a feature | PASS | Every refusal and failure names what happened and the next step ([contracts/cli-self-upgrade.md](contracts/cli-self-upgrade.md)). These are run-time results, not static diagnostics, so no span applies. Runefile diagnostics are untouched because no Runefile is read. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | One new focused package, `internal/selfupgrade`, alongside the existing ones. Nothing moves, and `mcpserver/` is untouched. |
| V. Boringly portable | PASS with one recorded limit | Still pure Go, `CGO_ENABLED=0`, one static binary; the new dependency is pure Go (the prototype built with CGO off). Replacement works on all three systems. Windows cannot swap a running program's file atomically; see Complexity Tracking. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first at each layer (R18). Golden files change only if Go 1.27 alters output, and then by deliberate regeneration. |
| VII. AI-native, secure by default | PASS | The MCP surface is unchanged: the flag is not a task and is not exposed as a tool. No secret is read or sent; release lookups are anonymous. |
| VIII. Go engineering discipline | PASS | Every network call carries the run's context and a limit (R16), including the trust-root fetch, which gets Rune's own bounded fetcher because the library's default has no timeout (R7). No goroutine outlives the call that started it. Errors wrap with `%w`. Dependencies are injected through a constructor, not package globals. The linter gate moves to a build that supports Go 1.27 (R4). |
| Constraint: Docker-only testing | PASS | All new tests run in the existing harness. Tests needing a non-root user skip there and run in CI (R18). |
| Constraint: locked package layout | PASS | Additive only. |
| Constraint: backward compatibility | PASS | No DSL change. Every existing command line parses as before (FR-004, SC-008). |
| Constraint: surface changes carry their docs | PASS | No DSL surface change. The CLI flag ships with `docs/cli.md` and `docs/installation.md` in the same PR. |
| Quality gates | PASS | All seven run on Go 1.27. Gate 1 needs linter v2.13 (R4). |

One recorded limit and one cost, both in Complexity Tracking. No unjustified violation.

**Post-design re-check**: PASS. The contracts add one flag, three exit-code meanings for it, one
environment variable in the images and one error type. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/035-go127-self-upgrade/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R19
├── data-model.md        # Phase 1: install, origin, release, outcome, failure reasons, run states
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli-self-upgrade.md  # flag, combinations, output, exit codes, guarantees
│   ├── release-layout.md    # what a release must provide; signer identity
│   └── toolchain.md         # Go selections, docs statements, checks
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
go.mod, go.sum                   # go 1.27.0; sigstore-go v1.4.0
Dockerfile                       # GO_VERSION=1.27; ENV RUNE_CONTAINER=1
Dockerfile.test                  # GO_VERSION=1.27 and the comment that names the base image
Dockerfile.goreleaser            # ENV RUNE_CONTAINER=1
.github/workflows/ci.yml         # go-version "1.27" in six jobs; golangci-lint v2.13
.github/workflows/release.yml    # go-version "1.27"
.github/workflows/pages.yml      # go-version "1.27"

cmd/rune/
├── main.go                      # conflict check on a parsed copy of the arguments, before dispatch
├── root.go                      # --self-upgrade flag; RunE starts the upgrade before cli.Run
├── selfupgrade.go               # builds the updater from build info and prints the outcome
├── upgradehook.go               # production endpoints, trust source and identity (no overrides)
├── upgradehook_runetest.go      # -tags runetest: RUNE_TEST_UPGRADE_* overrides for binary tests
├── versionhook.go               # installedStamped: production passes the build's value through
├── versionhook_runetest.go      # -tags runetest: a set RUNE_TEST_VERSION counts as stamped
├── root_test.go                 # conflicts, --help precedence, task-flag passthrough
└── selfupgrade_test.go          # outcome to message and exit code

internal/selfupgrade/            # new package
├── upgrade.go                   # Config, constructor, Run: the ordered steps of R11
├── origin.go                    # install origin from version, path and environment (R12)
├── release.go                   # latest stable release, names and URLs (R10)
├── fetch.go                     # bounded, size-capped, cancellable downloads (R16)
├── trust.go                     # public trust root through TUF with a bounded fetcher (R7)
├── verify.go                    # signature over checksums.txt; identity; both bundle forms (R6-R9)
├── checksum.go                  # parse the list; match the archive
├── extract.go                   # the one binary from tar.gz or zip (R13)
├── lock.go                      # lock file, stale takeover, sweep of old staging files (R14)
├── replace.go                   # staging file beside the binary
├── replace_unix.go              # rename over the target; sync the directory
├── replace_windows.go           # rename aside, rename in, roll back; remove rune.exe.old
├── errors.go                    # failure reasons from data-model.md
├── *_test.go                    # one per file above
├── testdata/                    # v0.7.0 checksums.txt and signature file; trust-root snapshot
└── upgradetest/                 # test-support package: signed fixture release on a local server,
                                 #   with its own signing authority (signer.go)

internal/cli/
├── exit.go                      # one error type that maps to exit 1 outside a task
└── exit_test.go

test/integration/
├── self_upgrade_test.go         # signed fixture release; upgrade; failures; refusals
├── self_upgrade_cli_test.go     # conflicts, passthrough, help
├── self_upgrade_windows_test.go # rune.exe.old left and later removed
└── no_network_test.go           # other commands make zero connections (R19)

docs/cli.md                      # flag row; exit-code rows
docs/installation.md             # Go 1.27; macOS 13; "Upgrading" section
docs/releasing.md                # exact signer identity; what self-upgrade depends on
docs/docker.md                   # one line: upgrade by pulling a newer image
CONTRIBUTING.md                  # Go 1.27+
```

**Structure Decision**: Existing layout plus one package. All upgrade logic lives in
`internal/selfupgrade` behind a constructor that takes its endpoints, HTTP client, trust material,
identity, executable path, platform, environment lookup and clock, so it is testable without the
network and holds no globals. `cmd/rune` owns flag parsing and message printing, as it does for
every other flag. The two hook files follow the existing `versionhook.go` and
`versionhook_runetest.go` pair, so a released binary has no way to redirect the upgrade.

**Order of work**: Part A as its own commit with all gates green, then Part B: the package bottom-up
(errors, origin, checksum, extract, fetch, verify, lock, replace, upgrade), then the root command,
then integration tests, then docs.

## Complexity Tracking

| Item | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Binary roughly doubles, from about 11.6 MB to about 24 MB, and about 67 modules join the build (`sigstore-go`) | FR-009 and FR-012 require proving the release signature inside Rune with no other tool installed | A hand-written verifier adds nothing to the binary but puts several hundred lines of signature-chain checking in Rune's own hands, where a mistake installs a forged binary. Checking SHA-256 alone is forbidden by FR-011 (R6) |
| On Windows there is an instant between two renames in which `rune.exe` does not exist (the exception written into FR-015) | Windows refuses to overwrite the file of a running program | Refusing self-upgrade on Windows was allowed by the issue but drops a first-class platform. A helper process that waits for Rune to exit and then swaps the file means starting another program and leaving the outcome unreported. The old file is kept as `rune.exe.old` and the recovery is documented (R14) |
