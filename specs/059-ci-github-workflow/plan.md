# Implementation Plan: Generate GitHub Actions Workflows from the Runefile

**Branch**: `059-ci-github-workflow` | **Date**: 2026-11-25 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/059-ci-github-workflow/spec.md`

## Summary

Close issue #62 with two commands:

1. `rune ci github ROOT...` builds the node graph of the named roots on each target OS by
   walking it the way the scheduler does, without running anything (R2), refuses what a job
   cannot run faithfully or safely (R4, R5), and renders a deterministic workflow: one job
   per node, `needs:` in local order, a pinned and checksum-verified Rune install, artifacts
   for declared outputs, exact-key caches (R6 to R10). `--write`, `--output` and `--check`
   handle one owned file (R12).
2. `rune ci run TASK ARG...` is what each job runs: the body of exactly that task through the
   normal engine, failure hooks included, dependencies and post-hooks not (R3). With `--dir`
   it unpacks the bundles of earlier jobs and writes its own; with `--key` it prints the
   cache key from Rune's fingerprint (R7, R8).

`ci` becomes a built-in command (R15). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: no new module. Existing: `github.com/spf13/cobra` (commands),
`mvdan.cc/sh/v3/syntax` (background statements in bodies), `go.yaml.in/yaml/v3` (tests parse
the generated YAML), `archive/tar` and `compress/gzip` (bundles), `internal/cache`
(fingerprint, globs), `internal/runtime/scheduler` (new `RunOnly`), `internal/eval`,
`internal/semver` (version and `minimum_version`), `internal/taskimport` (`RuneCommands`).

**Storage**: none beyond files: the workflow file, `DIR/bundle.tar`, the existing
`.rune/cache` records.

**Testing**: `go test` in Docker only. Unit tests in `internal/ci` (graph, refusals, IDs,
quoting, render goldens, bundles, keys), `internal/runtime/scheduler` (`RunOnly`),
`internal/cache` (new exported helpers), `internal/cli` (commands, exit codes, key changes),
`cmd/rune` (wiring); an integration test that simulates GitHub with the real binary (R14);
the docs harness for the example; `actionlint` on goldens in CI (R13).

**Target Platform**: Rune on Linux, macOS, Windows; generated workflows for GitHub-hosted
Linux and macOS runners.

**Project Type**: CLI tool (existing layout)

**Performance Goals**: generation is linear in nodes and edges; graphs of hundreds of tasks
generate in well under a second. No goal beyond that.

**Constraints**: generation runs no body, reads no environment value, file or network
(FR-003). Output byte-identical for the same inputs (FR-036). Least privilege and no secrets
in the workflow (FR-028 to FR-031). New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 1 new package `internal/ci` (graph, checks, IDs, render, bundle, key,
owned-file plan); `scheduler.RunOnly`; exported helpers in `internal/cache`; 1 new file in
`internal/cli` (`ci.go`) plus a hook point in `execute`; 1 new file in `cmd/rune` (`ci.go`);
`ci` in `RuneCommands`; 1 integration test; docs: a how-to page, `cli.md`, `nav.yaml`, the
how-to index, a new example, the `ci-cd` example update; CI and Runefile `actionlint` steps.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.5.0. No amendment.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Jobs run every body unless the task opted into `[cache]`. A CI cache hit is Rune's own decision (`cache.Decide`: fingerprint equal and every output present), logged as `cached: NAME`. Restores use exact keys only. |
| II. Errors are a feature | PASS | Every refusal is a diagnostic at the task, attribute, expression or body line, rendered like any Runefile error, before anything is written. |
| III. Minimal, total DSL | PASS | No syntax, attribute or function is added. Roots are command-line arguments. |
| IV. Hand-written front end, idiomatic Go | PASS | Front end untouched. One small `internal/ci` package; the scheduler gains one function. |
| V. Boringly portable | PASS | Pure Go; `rune ci run` and generation work on all three systems. The generated workflow targets Linux and macOS runners and says so; that is a property of the output, not of Rune. |
| VI. Test-first, multi-layer verification | PASS | Failing tests first; golden workflows compared byte for byte and linted; a binary-level simulation of the workflow; the docs example kept current by its own check. |
| VII. AI-native, secure by default | PASS | The workflow gets `contents: read` only, no secret, no `--yes`, no deployment; `[confirm]` and `[applies]` tasks are refused; checkout keeps no credentials; no `pull_request_target`; no expression in a shell script. |
| VIII. Go engineering discipline | PASS | No goroutines added. Errors wrapped with `%w`. File writes atomic. No package state beyond constant tables. |
| Constraint: Docker-only testing | PASS | All Go tests run in the test container; `actionlint` runs from its image locally and with `go run` in CI. |
| Constraint: locked package layout | PASS | `internal/ci` is a focused engine package, as `agentdocs` and `taskimport` are; `mcpserver/` untouched. |
| Constraint: backward compatibility | PASS, noted | No DSL change. `ci` becomes a built-in command, so a task named `ci` runs with `rune -- ci`, as for `agent`, `export` and `suggest`; the commit is marked breaking and the `ci-cd` example is updated. |
| Constraint: surface changes carry their docs | PASS | `docs/cli.md`, a how-to page, an example and help text ship in the same PR. No grammar change. |
| Quality gates | PASS, extended | The lint job gains an `actionlint` step on the golden workflows (R13). `ci.yml` is not replaced. |

**Post-design re-check**: PASS. The contracts add two commands, a workflow shape and a bundle
format. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/059-ci-github-workflow/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R15
├── data-model.md        # Phase 1: root, node, graph, job, bundle, key, owned file
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli.md           # rune ci, rune ci github, rune ci run
│   └── workflow.md      # the generated file, byte layout and invariants
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/runtime/scheduler/scheduler.go  # RunOnly: body and failure hooks of one invocation

internal/cache/cache.go          # RecordPath, OutputFiles exported for bundles

internal/ci/doc.go               # package overview
internal/ci/graph.go             # Resolver, Root, Node, Graph, Build (R2)
internal/ci/check.go             # refusals (R5), machine-dependent expressions (R4)
internal/ci/jobid.go             # job IDs and labels (R11), shell words
internal/ci/render.go            # Options, Render: the workflow text (R6, R9, R10)
internal/ci/owned.go             # Marker, Plan of the owned file, check states (R12)
internal/ci/bundle.go            # Pack, Unpack, UnpackDir (R7)
internal/ci/key.go               # Key (R8)
internal/ci/testdata/            # golden workflows

internal/cli/ci.go               # CIGithub, CIRun, CITaskNote
internal/cli/run.go              # nodeRun hook point in execute
internal/cli/dispatch.go         # Options.nodeRun field

internal/taskimport/finish.go    # "ci" in RuneCommands

cmd/rune/ci.go                   # ci, ci github, ci run commands
cmd/rune/main.go                 # register ci

test/integration/ci_github_test.go   # GitHub simulation with the real binary (R14)

docs/how-to/ci-github.md         # new how-to
docs/examples/ci-github/         # Runefile, README, .github/workflows/rune.yml
docs/examples/ci-cd/             # task ci run with rune -- ci
docs/cli.md, docs/nav.yaml, docs/how-to/README.md, docs/examples/README.md
.github/workflows/ci.yml         # actionlint step in the lint job
Runefile                         # actionlint task
```

**Structure Decision**: the graph, checks and rendering live in `internal/ci`, which knows
nothing about the CLI: it asks a `Resolver` (implemented by the CLI's `engine` with the
target OS) to resolve dependency calls, as the scheduler does. Bundles and keys are in the
same package because only `rune ci run` uses them. The commands live in `internal/cli` and
`cmd/rune` as every other command does.

## Complexity Tracking

No violation to justify.
