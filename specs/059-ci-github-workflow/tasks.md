---

description: "Task list for spec 059: generate GitHub Actions workflows from the Runefile"
---

# Tasks: Generate GitHub Actions Workflows from the Runefile

**Input**: Design documents from `/specs/059-ci-github-workflow/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI asks for them, and the issue's acceptance
criteria name them. In every phase the test tasks come first, and a test is written and seen
to fail before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on
  an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US6)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of the new code.
- Generation never runs a body: fixtures whose bodies would leave a trace (`touch SENTINEL`)
  show that nothing ran.
- Golden files are regenerated with `-update`, never edited by hand.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

**Purpose**: the new package and its fixtures.

- [X] T001 Create `internal/ci/doc.go` with a package comment: the package builds the job
  graph of CI root tasks, checks what a job cannot run faithfully, renders the GitHub Actions
  workflow, and packs and unpacks the bundles and cache keys `rune ci run` uses; it runs no
  task body (plan.md Structure Decision)
- [X] T002 [P] Create the fixture directory `internal/ci/testdata/` with a `README.md` line
  saying the `.yml` files are golden outputs regenerated with `go test ./internal/ci -update`

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the one-node execution primitive, the cache helpers bundles need, the hook point
in `execute`, and the `ci` command name.

- [X] T003 [P] Write failing tests for `scheduler.RunOnly` in
  `internal/runtime/scheduler/runonly_test.go`: runs the body of the invocation and none of
  its `Deps` or `PostHooks`; on a body error runs its `FailHooks` with the `Failure` context
  and returns the body error unchanged; on `context.Canceled` or `ErrBodyNotRun` runs no hook
- [X] T004 Implement `RunOnly(engine Engine, inv Invocation) error` in
  `internal/runtime/scheduler/scheduler.go` reusing `state` and `runFailHooks` (research R3);
  update the package comment to name it
- [X] T005 [P] Write failing tests in `internal/cache/cache_test.go` for exported
  `RecordPath(spec Spec) string` (the path `Store` writes, relative to nothing) and
  `OutputFiles(root string, outputs []string) ([]string, error)`: the sorted, de-duplicated
  relative paths of the files the outputs name, a glob by Rune's rules, a literal directory
  taken whole (recursively), a literal file as itself, a pattern that matches nothing
  contributing nothing
- [X] T006 Implement `RecordPath` (export the existing `recordPath`) and `OutputFiles` in
  `internal/cache/cache.go`, sharing `expandGlobs`
- [X] T007 Add an unexported field `nodeRun func(e *engine, invs []scheduler.Invocation) error`
  to `Options` in `internal/cli/dispatch.go`, and in `execute` (`internal/cli/run.go`) call it
  instead of `scheduler.Run` when set; add a test in `internal/cli/run_test.go` that a set
  `nodeRun` receives the bound invocations and that its error is classified like a scheduler
  error
- [X] T008 [P] Add `"ci"` to `taskimport.RuneCommands` in `internal/taskimport/finish.go` and
  extend `internal/taskimport/finish_test.go` with a task named `ci` (the review note
  `ci is also a rune command: run the task with rune -- ci`)
- [X] T009 Write failing tests in `cmd/rune/ci_test.go` (pattern of `cmd/rune/export_test.go`):
  `rune ci` with a Runefile task `ci` prints
  `note: "ci" is also a task of this Runefile; run it with: rune -- ci` and the help, exit 0;
  `rune ci deploy` is a usage error naming `rune -- ci`; `rune -- ci` still runs the task
- [X] T010 Implement `newCICmd` (parent, `Args: cobra.ArbitraryArgs`) in `cmd/rune/ci.go`,
  `CITaskNote(opts)` in `internal/cli/ci.go`, and register `ci` in `cmd/rune/main.go`

**Checkpoint**: `rune ci` exists, a task named `ci` runs with `rune -- ci`, and one node can
be run through the engine.

---

## Phase 3: User Story 1 - Generate a workflow for chosen root tasks (Priority: P1) MVP

**Goal**: `rune ci github ROOT...` prints a deterministic workflow with one job per node and
`needs:` from the local order.

**Independent Test**: generate from the diamond fixture, compare with a golden file, parse
the YAML, and check jobs, `needs:`, and byte identity across two runs.

### Tests for User Story 1

- [X] T011 [P] [US1] Write failing graph tests in `internal/ci/graph_test.go` with a fake
  `Resolver` that binds literal arguments: diamond (`lint`, `test` -> `build` -> `generate`)
  gives four nodes, `build` once, in topological order; `(build "release")` and
  `(build "debug")` give two nodes; a root that is also a dependency of another root, and a
  root named twice, give one node; a task without `[parallel]` on `a b c` gives needs
  `b:{a}`, `c:{b}`, `task:{c}`; with `[parallel]` no needs among them and `task:{a,b,c}`;
  `&& notify` gives `notify:{task}` and a dependent of `task` needs `notify`; a post-hook
  target that is already a prerequisite of the task adds no node and no need; an unavailable
  dependency is left out; two roots do not need each other; a cycle returns the scheduler's
  `CycleError` path; implied needs are reduced away (research R2)
- [X] T012 [P] [US1] Write failing tests in `internal/ci/jobid_test.go` for job IDs and
  labels (research R11): only `[a-z0-9_-]`, starts with a letter (`t-` prefix otherwise),
  at most 64 bytes, `api::build` -> `api-build`, clash -> `-2`, `-3` in order, OS suffix with
  several OSes; labels `build 'a b'`, `api::test`, ` (linux)` suffix; and for `ShellWord`:
  bare for `A-Za-z0-9_./:@%+=,-`, single-quoted otherwise with `'\''`, empty string `''`
- [X] T013 [P] [US1] Write failing render tests in `internal/ci/render_test.go`: golden
  `testdata/diamond.yml` and `testdata/two-os.yml` (`-update` flag); every golden parses
  with `go.yaml.in/yaml/v3`, every `needs:` entry is a job defined earlier, every `run:` step
  has `shell: bash`; rendering twice is byte-identical; the header has the marker and the
  regeneration line (contracts/workflow.md); `defaults.run.working-directory` appears only
  when `WorkDir != "."`
- [X] T014 [P] [US1] Write failing CLI tests in `internal/cli/ci_test.go` for `CIGithub`
  printing: diamond Runefile -> workflow on stdout, `4 jobs for 2 root tasks on linux` on
  stderr, no file created; no root -> usage error (exit 2) with the contracts/cli.md text;
  `NAME=value` -> usage error; unknown task -> usage error; module task `api::test` root;
  parameterized dependencies produce two jobs with their own arguments; a development
  version without `--rune-version` -> usage error; `--rune-version v0.11.0` and `0.11.0`
  render the same; a version below the Runefile's `minimum_version` -> usage error;
  `--os windows` -> usage error; `--branch` holding `${{` or a control character -> usage
  error; a root marked `[windows]` -> refused as `rune` refuses it (FR-011); a root default
  and a variable that call `env()` with the variable set to a sentinel value -> refused, and
  the value appears nowhere in the output; sentinel bodies never run

### Implementation for User Story 1

- [X] T015 [US1] Implement `Root`, `Node` (fields per data-model.md: Task, Args, Argv, Key,
  Needs, Done, Index, Outputs, Cached), `Graph` (with its `OS`), the `Resolver` interface
  (`BindRoot(task, args []string)`, `ResolveDep`, `Available`) and `Build` in
  `internal/ci/graph.go` per research R2; `Build` binds roots itself through `BindRoot`, so
  the checks of US6 run before any root default is evaluated; `Argv` lists every parameter in
  order, variadic elements as separate words
- [X] T016 [US1] Implement `JobID`, `Label` and `ShellWord` in `internal/ci/jobid.go` per
  research R11 and R10
- [X] T017 [US1] Implement `Options` (OSes, Version, Branch, WorkDir, Command) and
  `Render(opts Options, graphs []*Graph) []byte` in `internal/ci/render.go`: header,
  `name: rune`, triggers, `permissions: contents: read`, optional defaults, and per node a job
  with checkout (`persist-credentials: false`), the install script of research R6, and the
  run step `rune ci run --dir "$RUNNER_TEMP/rune-ci" -- TASK ARGS`; YAML scalars from the
  Runefile single-quoted with `''`; scripts as literal blocks
- [X] T018 [US1] Implement `CIGithub(opts Options, flags CIGithubFlags, args []string) error`
  in `internal/cli/ci.go`: resolve the Runefile, `loadStatic`, refuse overrides from
  `splitArgs`, validate `--os` and `--branch` (usage errors), resolve the version
  (`internal/semver`, `minimum_version`), and per OS build an `engine` as the `Resolver`
  with `goos` and `scope.GOOS` set to that OS and nothing else: no `buildEnv`, no dotenv, no
  `ResolveSettings` (FR-003); `BindRoot` refuses an unavailable root as `resolveRoots` does
  and binds with `bindParams`; then `ci.Build`, `ci.Render`, print to stdout and the summary
  to stderr
- [X] T019 [US1] Add `newCIGithubCmd` in `cmd/rune/ci.go` with flags `-f/--file`, `--os`
  (string array), `--rune-version`, `--branch` (default `main`), `--write`, `-o/--output`,
  `--check`, Long help with the exit statuses of contracts/cli.md, and wiring tests in
  `cmd/rune/ci_test.go`

**Checkpoint**: `rune ci github lint test` prints a valid, deterministic workflow.

---

## Phase 4: User Story 2 - Each job runs exactly one task (Priority: P1)

**Goal**: `rune ci run TASK ARGS` runs one body as a local run would, and the generated
workflow, simulated job by job, matches a local run.

**Independent Test**: simulate the workflow of the diamond fixture with the real binary, each
job in its own copy, and compare with `rune lint test`.

### Tests for User Story 2

- [X] T020 [P] [US2] Write failing tests in `internal/cli/ci_test.go` for `CIRun` without
  `--dir`: runs only the named body (dependency and post-hook sentinels absent); binds
  arguments as `rune TASK ARGS` does (variadic, defaults, constraints); a failing body with
  `|| diagnose` runs `diagnose` with `RUNE_FAILED_TASK` and `RUNE_FAILED_EXIT_CODE` and returns
  the body's exit code; `[cache]` hit logs `cached: NAME`; unknown task -> exit 2; an argument
  starting with `-` after `--` is passed through
- [X] T021 [P] [US2] Write the GitHub simulation in `test/integration/ci_github_test.go`
  (research R14) with fixture `test/integration/testdata/ci/diamond/Runefile`: parse the
  generated YAML, run jobs in `needs:` order each in a fresh copy with its own `RUNNER_TEMP`,
  `GITHUB_OUTPUT` and `PATH` holding the test binary, run `run:` scripts with `bash`, skip the
  install step, emulate `download-artifact`, `upload-artifact`, `cache/restore`, `cache/save`
  with exact-key maps and the save step's `if:`; skip on Windows with the reason. Assert:
  every body logged once; a failing job skips its dependents; the produced files equal a local
  `rune lint test` run in another copy (SC-001)
- [X] T022 [P] [US2] Write an integration test in `test/integration/ci_github_test.go` that an
  interrupted `rune ci run` (SIGINT) runs no failure hook and exits 130

### Implementation for User Story 2

- [X] T023 [US2] Implement `CIRun(opts Options, flags CIRunFlags, args []string) error` in
  `internal/cli/ci.go`: one task invocation required, `nodeRun` calling `scheduler.RunOnly`,
  everything else through `execute`
- [X] T024 [US2] Add `newCIRunCmd` in `cmd/rune/ci.go` (`SetInterspersed(false)`, flags
  `-f/--file`, `--dir`, `--key`) with Long help per contracts/cli.md, and wiring tests in
  `cmd/rune/ci_test.go`

**Checkpoint**: a workflow can be simulated end to end and matches a local run.

---

## Phase 5: User Story 3 - The workflow is safe for pull requests (Priority: P1)

**Goal**: least privilege, no secret, no deployment, no injection.

**Independent Test**: refusal fixtures exit 3 naming the task; every golden meets the
invariants of contracts/workflow.md and passes `actionlint`.

### Tests for User Story 3

- [X] T025 [P] [US3] Write failing tests in `internal/ci/check_test.go`: a node or failure
  hook with `[confirm]`, a node with `[applies]`, a node the `Agent` predicate marks, each
  refused with a diagnostic whose span is the attribute or task and whose message names the
  task and reason (research R5); text holding `${{`, a control character (tab included) or a
  line break in an argument or a label refused (`--branch` is a usage error in the CLI,
  T014)
- [X] T026 [P] [US3] Add an invariant test in `internal/ci/render_test.go` run over every
  golden: exactly one permission `contents: read`; triggers exactly `push` with `branches`
  and `pull_request`; no `secrets.`, `restore-keys`, `--yes`, `environment:`; no `${{`
  inside a `run:` value (SC-006)
- [X] T027 [P] [US3] Add a golden `testdata/quoting.yml` and a simulation case in
  `test/integration/ci_github_test.go`: arguments `it's`, `a b`, `$HOME` reach the body
  exactly as written

### Implementation for User Story 3

- [X] T028 [US3] Implement the security checks in `internal/ci/check.go`
  (`checkNode`, `checkText`), called by `Build` for every node and failure hook; a task is
  run by the agent executor when `task.Executor == ast.ExecAgent`, which needs no settings
- [X] T029 [US3] Make `CIGithub` render each refusal as a diagnostic (`renderDiags`) and
  return a `ValidationError` (exit 3) with nothing on stdout; extend `internal/cli/ci_test.go`

**Checkpoint**: no generated workflow can carry a secret, a write token, a deployment or an
injection.

---

## Phase 6: User Story 4 - Outputs travel and cache keys come from the fingerprint (Priority: P2)

**Goal**: bundles as artifacts and exact-key caches; a hit skips only when Rune's own
decision says so.

**Independent Test**: the simulation shows outputs reaching dependents only, a second run
hitting the cache, and a bundle without its output running the body.

### Tests for User Story 4

- [X] T030 [P] [US4] Write failing tests in `internal/ci/bundle_test.go`: `Pack` of files,
  a directory output, and the record; `Unpack` round trip keeps content and the exec bit;
  `Unpack` refuses an absolute path, `..`, a symlink, a hard link, a device, and leaves no
  partial file of a refused entry; `UnpackDir` order is name order of `DIR/in/*` then
  `DIR/bundle.tar`; `Pack` refuses an output outside the project and an output holding a
  link, naming it; `Unpack` refuses `dist/app` when `dist` in the project is a link to a
  directory outside it; entry names use `/` on every system; the exec-bit and link cases are
  skipped on Windows with the reason
- [X] T031 [P] [US4] Write failing tests in `internal/ci/key_test.go`: `Key` is
  `rune-<slug>-<64 hex>`, at most 512 characters, and changes with each input (fingerprint,
  node key, version, GOOS, GOARCH, image) and not otherwise
- [X] T032 [P] [US4] Write failing tests in `internal/cli/ci_test.go` for `CIRun --key`
  (stdout exactly `key=...\n`, nothing run, exit 2 without `[cache]`, exit 1 when the
  fingerprint fails) and key changes for: the body, an imported variable the task uses, an
  input file, the executor, the version, `GOOS`; and no change for a comment or another
  task (SC-004); and for `CIRun --dir`: unpacks `in/` then `bundle.tar`, writes `bundle.tar`
  after a successful `[cache]` body, writes none after a failure; a bundle holding `../x` or
  a corrupt bundle exits 1 before the body runs (FR-039)
- [X] T033 [P] [US4] Write failing render tests: golden `testdata/outputs.yml` with key,
  restore, save (with `if: steps.restore.outputs.cache-hit != 'true'`), upload
  (`retention-days: 1`, `if-no-files-found: error`, `overwrite: true`) and per-ancestor
  download steps into `in/<NNNN>-<job id>`; a non-ancestor job has no download step
- [X] T034 [P] [US4] Extend the simulation in `test/integration/ci_github_test.go`: the
  `test` copy has `build`'s output before its body; a job that does not depend on `build`
  gets none; a second run logs `cached: build` and no `build` body; after removing the output
  from the stored bundle, the third run runs `build` (SC-005)

### Implementation for User Story 4

- [X] T035 [US4] Implement `Pack(root string, files []string, w io.Writer) error`,
  `Unpack(root string, r io.Reader) error` and `UnpackDir(root, dir string) error` in
  `internal/ci/bundle.go` (gzip tar, research R7); `Unpack` writes through `os.OpenRoot(root)`
  and `Pack` reads with `os.Lstat` so no link is followed
- [X] T036 [US4] Implement `Key(slug string, ctx KeyInput) string` in `internal/ci/key.go`
  (research R8, data-model.md "Cache key")
- [X] T037 [US4] Extend `CIRun` in `internal/cli/ci.go`: `--dir` unpacks before `execute`;
  `nodeRun` for `--key` builds `e.cacheSpec`, `cache.Decide`, prints the key with
  `opts.Version`, `runtime.GOOS`, `runtime.GOARCH`, `os.Getenv("ImageOS")`; after a
  successful `[cache]` body packs `cache.OutputFiles` and `cache.RecordPath` into
  `DIR/bundle.tar` atomically
- [X] T038 [US4] Extend `Render` with the key, restore, save, upload and download steps
  (contracts/workflow.md) and `Node.Outputs`/`Node.Cached` from the `[cache]` attribute

**Checkpoint**: outputs and caches behave as the spec says in the simulation.

---

## Phase 7: User Story 5 - Write the workflow and detect drift (Priority: P2)

**Goal**: `--write`, `--output` and `--check` on one owned file.

**Independent Test**: write, write again, edit the Runefile, check, and try a hand-written
target.

### Tests for User Story 5

- [X] T039 [P] [US5] Write failing tests in `internal/ci/owned_test.go` for
  `PlanFile(existing []byte, existed bool, planned []byte) (State, error)`: missing,
  not generated (first line is not the marker), current, stale
- [X] T040 [P] [US5] Write failing tests in `internal/cli/ci_test.go`: `--write` creates
  `.github/workflows/rune.yml` next to the Runefile (directories created) and reports
  created; a second `--write` reports unchanged and keeps the modification time; `--output
  ci/rune.yml` writes there and its header says `--output ci/rune.yml`; `--check` exits 0 on
  current and 1 on stale or missing with `regenerate with:`; a hand-written target -> exit 2,
  bytes unchanged, for `--write` and `--check`; `--check --write` -> usage error; the header
  does not depend on the working directory or `-f`; a Runefile in a subdirectory of the
  directory holding `.github/workflows` gets `defaults.run.working-directory` (SC-008)

### Implementation for User Story 5

- [X] T041 [US5] Implement `Marker`, `State` and `PlanFile` in `internal/ci/owned.go`
  (research R12)
- [X] T042 [US5] Extend `CIGithub` in `internal/cli/ci.go`: the target (default or
  `--output`), the repository root (the directory holding the target's `.github/workflows`,
  else the Runefile's directory) and `WorkDir`, the regeneration command for the header,
  write with the existing atomic writer (`writePlanned`), check with the states and the
  `regenerate with:` line, and the exit statuses of contracts/cli.md

**Checkpoint**: the workflow can live in the repository and CI can check it is current.

---

## Phase 8: User Story 6 - Graphs CI cannot run faithfully are refused (Priority: P2)

**Goal**: refuse what a job-per-node workflow would run differently.

**Independent Test**: one fixture per reason exits 3, names the task, prints nothing on
stdout.

### Tests for User Story 6

- [X] T043 [P] [US6] Write failing tests in `internal/ci/check_test.go`: a prerequisite whose
  shell body has `server &` refused only when another node needs it (a root with no
  dependents is accepted); a body line with `{{x}} &` is still found; a line that does not
  parse is not judged; a failure hook with dependencies or post-hooks refused; a literal
  output `/abs` or `../out` refused; a dependency argument or an unsupplied default that
  calls `env`, `arch`, `num_cpus`, `path_exists`, `read`, `sha256_file`, `absolute_path`,
  `require`, `which`, `datetime` or `uuid`, directly or through a chain of variables,
  refused with the built-in named; `os()` and `os_family()` accepted and evaluated for the
  target
- [X] T044 [P] [US6] Write a test in `internal/ci/check_test.go` that every built-in in
  `internal/language` is classified as machine-dependent or pure, so a new built-in fails it
- [X] T045 [P] [US6] Write CLI tests in `internal/cli/ci_test.go` for each refusal with a real
  Runefile: exit 3, the task named, 0 bytes on stdout, sentinel absent (SC-007)

### Implementation for User Story 6

- [X] T046 [US6] Implement in `internal/ci/check.go` the machine-dependence walk over
  expressions and assignments (research R4), the background-statement check with
  `mvdan.cc/sh/v3/syntax` (research R5) for tasks whose executor is `sh` or empty, whatever
  `set shell` says, the failure-hook shape check and the literal output check; call them from
  `Build` before each `BindRoot` and `ResolveDep` and after the graph is complete (background
  needs "has dependents")
- [X] T047 [US6] Pass the tasks and assignments to `ci.Build` from `CIGithub`

**Checkpoint**: every refusal in research R5 is reported before anything is written.

---

## Phase 9: Polish & Cross-Cutting Concerns

- [X] T048 [P] Write `docs/how-to/ci-github.md`: choosing roots; the job and `needs:`
  contract, including dependency order without `[parallel]` and independent roots; what
  travels between jobs and how to declare it (regular files and directories, no links); cache
  keys and when a hit skips a body; runner
  and OS support (Linux, macOS; Windows not supported); toolchains; GitHub's artifact and
  cache limits; each refused construct with what to do; `--write`, `--check` and a drift
  check task; `rune ci run` to reproduce a job locally (FR-040)
- [X] T049 [P] Add `docs/examples/ci-github/` with a `Runefile` (diamond with a `[cache]`
  build, a `[parallel]` check task, a `workflow-check` task that runs
  `rune ci github --check ...`), a `README.md` whose "Run it" block is the `--check` command,
  and the generated `.github/workflows/rune.yml`; list it in `docs/examples/README.md`
- [X] T050 [P] Update `docs/examples/ci-cd/Runefile` and its `README.md`: the task `ci` is
  run with `rune -- ci`
- [X] T051 [P] Add `rune ci github` and `rune ci run` to `docs/cli.md` (commands, flags, exit
  statuses), the how-to to `docs/nav.yaml` and `docs/how-to/README.md` (and to
  `selfContainedPages` in `test/docs/codeblocks_test.go` when its rune blocks are complete
  Runefiles), and note the breaking `ci` command name where `agent`, `export` and `suggest`
  are noted
- [X] T052 Add an `actionlint` step to the lint job of `.github/workflows/ci.yml`
  (`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` on `internal/ci/testdata/*.yml`
  and `docs/examples/ci-github/.github/workflows/rune.yml`) and an `actionlint` task to the
  `Runefile` running the `rhysd/actionlint:1.7.12` image on the same files (research R13)
- [X] T053 Run `actionlint` on every golden and the example workflow and fix every finding
  in `internal/ci/render.go` (regenerate goldens with `-update`)
- [X] T054 Run `golangci-lint run`, the full test suite and `-race` in Docker, and the docs
  harness; check coverage of `internal/ci` and the new code in `internal/cli` is at least 90%
- [X] T055 Run the quickstart.md scenarios with a built binary and record the results

---

## Dependencies & Execution Order

- Phase 1 -> Phase 2 -> US1. US1 is the MVP and blocks every later story (they extend
  `Build`, `Render` and `CIGithub`).
- US2 needs US1 (it simulates a generated workflow). US3 needs US1. US4 needs US2 (the
  simulation) and US1. US5 needs US1. US6 needs US1 and US3 (the check plumbing).
- Polish needs all stories.

Within a phase: tests first (they fail), then implementation in the listed order.

## Parallel Opportunities

- Phase 2: T003, T005, T008 in parallel (different packages).
- US1: T011, T012, T013, T014 in parallel; T015 and T016 in parallel.
- US4: T030, T031, T032, T033, T034 in parallel; T035 and T036 in parallel.
- US5 and US6 test tasks can be written while US4 is implemented.
- Polish: T048 to T051 in parallel.

## Implementation Strategy

1. MVP: Phases 1 to 3. `rune ci github` prints a valid workflow for a dependency graph.
2. Add US2 so the workflow actually runs one node per job, and prove it by simulation.
3. Add US3 before anything is published: safety holds from the first generated file.
4. Add US4 (outputs and caches), US5 (write and check), US6 (refusals).
5. Polish: docs, example, actionlint in CI, coverage, quickstart.
