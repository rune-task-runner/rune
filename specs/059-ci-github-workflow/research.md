# Research: Generate GitHub Actions Workflows from the Runefile

Decisions for spec 059. Each lists what was chosen, why, and what was rejected.

## R1. Roots come from the command line

**Decision**: `rune ci github ROOT [ARG...]...` reads roots and their arguments with the same
splitter `rune` uses for `rune TASK ARG... TASK ...` (`splitArgs`). A `NAME=value`
override is refused. No Runefile attribute marks a CI root.

**Rationale**: The issue requires explicit roots. The command line is explicit, needs no DSL
change (Principle III, no grammar/LSP/formatter work), and the regeneration line in the file
header records it.

**Alternatives**: a `[ci]` attribute (DSL change, grammar docs, analyzer, LSP, for no extra
safety); a config file (a second source of truth, which the feature exists to remove).

## R2. One job per node; `needs:` from a simulation of the scheduler

**Decision**: A node is a task with bound arguments, keyed as the scheduler keys run-once
(`name(args.Key())`). The graph is built by walking the roots the way `scheduler.Run` does,
without running anything:

- `visit(node, after)` returns the node's *done set*: the jobs whose completion means the
  node, with its post-hooks, is done.
- A node seen before returns its stored done set (run-once).
- Without `[parallel]`: `cur := after; for each dep: cur ∪= visit(dep, cur)`; with
  `[parallel]`: each dep gets `after`, results are joined.
- The node's job needs `cur` (or `after ∪ deps` for `[parallel]`), and is appended to the
  job list now, so the job list is in topological order.
- Post-hooks: `cur := {node}; for each hook: cur ∪= visit(hook, cur)`; the done set is
  `cur`.
- Roots are visited with `after = ∅`, so roots do not wait for each other.
- An unavailable target (OS filter) contributes nothing, as the scheduler skips it.
- Cycles are found by the chain of task names, as the scheduler finds them, and refused
  with the same path text.

A job only ever needs jobs created before it, so the result is acyclic by construction.
`needs:` lists are reduced: a need implied by another need is dropped.

**Rationale**: This reproduces the local happens-before order exactly: a sequential task's
dependencies stay in order (a failure stops the later ones, as locally), `[parallel]`
dependencies become concurrent jobs, a post-hook runs after its task, and a dependent waits
for the post-hooks. Parallelism in CI is then the same opt-in as locally: `[parallel]`.

**Alternatives**: `needs:` from declared dependencies only (faster CI, but runs `ci: lint
test build` in parallel when the Runefile says sequential, and changes what a failure stops);
roots ordered as on a local command line (serializes unrelated pipelines for no benefit).

## R3. The node-execution mode is `rune ci run`

**Decision**: `rune ci run [--dir DIR] [--key] -- TASK [ARG...]` loads and analyzes the
Runefile, binds the arguments as `rune TASK ARG...` does, and runs the body through the same
`engine` with a new `scheduler.RunOnly`: the body via `Engine.Execute` (so `[cache]`,
`[confirm]`, policy gates, masking and exit codes are unchanged), and on failure the
existing `runFailHooks`. Dependencies and post-hooks are not run. It is wired through the
existing `execute` pipeline with one hook point: an unexported `nodeRun` function in
`Options` replaces `scheduler.Run` when set.

**Rationale**: `rune TASK` in every job would run prerequisites again; `needs:` does not stop
that. One shared pipeline keeps CI and local behavior identical.

**Alternatives**: a public `--only` flag on the root command (a second, general meaning to
explain and support, for no CI benefit); copying the body text into the workflow (forbidden
by the issue).

## R4. Arguments are evaluated at generation time, and must not depend on the machine

**Decision**: Root arguments are bound, and dependency arguments and the defaults of
parameters a call does not supply are evaluated, at generation, with `scope.GOOS` set to the
target OS and no environment, dotenv or settings loaded. `Build` binds roots itself through
`Resolver.BindRoot`, so roots get the same check as dependencies. Before any evaluation, the
expression and every variable it references (transitively, through assignments) is walked;
a call to `env`, `arch`, `num_cpus`, `path_exists`, `read`, `sha256_file`, `absolute_path`,
`require`, `which`, `datetime` or `uuid` refuses generation with a diagnostic at that call.
A test fails when a built-in in `internal/language` is neither in this list nor in the pure
list, so a new built-in must be classified. The job passes every parameter explicitly, in
parameter order (variadic elements as separate words), so the job binds the same `Args`.

**Rationale**: A value read from the developer's environment would be baked into the
workflow and differ from what CI computes; refusing keeps generation free of reads (FR-003)
and the node identity stable. Body interpolation still happens at run time in CI, which is
the shared engine's job.

**Alternatives**: re-resolving the graph in each job (node identity could change between
machines); allowing host values with a warning (a workflow that runs something else).

## R5. Refusals

**Decision**: generation fails with exit 3 and a spanned diagnostic for:

| Construct | Why |
|---|---|
| `[confirm]` on a node or failure hook | CI has no person to ask; `--yes` is never emitted |
| `[applies]` | needs a person's approval of a plan |
| `agent` executor | needs agent credentials |
| failure hook with dependencies or post-hooks | they would run again inside the failing job, breaking run-once |
| shell body with a background statement (`cmd &`) in a node that another job needs | the process dies with its job |
| literal `[cache]` output that is absolute or contains `..` leaving the project | a bundle holds project files only |
| machine-dependent argument or default (R4) | see R4 |
| `${{`, control characters or line breaks in any emitted Runefile text | expression injection, broken quoting |
| root unavailable on a target OS | as `rune` refuses it |

Background statements are found by parsing each body line of a task whose executor is `sh` or
empty (whether or not `set shell` runs it through a system shell) with
`mvdan.cc/sh/v3/syntax`, after replacing each `{{...}}` with a plain word; a line that does
not parse is not judged. The agent executor is `task.Executor == "agent"`; no setting is
resolved at generation, so no setting expression is evaluated (FR-003).
Outputs that are not literals are checked when the bundle is packed (R7).

**Rationale**: Each is a case where the workflow would run differently from a local run or
unsafely. Reporting with a span follows Principle II.

## R6. Installing Rune in a job

**Decision**: one `bash` step downloads `rune_VERSION_OS_ARCH.tar.gz` and `checksums.txt`
from the release `vVERSION` with `curl`, checks the archive with `shasum -a 256 -c`,
extracts `rune` into `$RUNNER_TEMP/rune-bin` and appends that directory to `$GITHUB_PATH`.
OS and architecture are read with `uname` at run time. VERSION is a literal in the script.

The version is `--rune-version`, or the running binary's version when it is a release
(`X.Y.Z` with an optional pre-release, with or without a leading `v`); `dev` and
pseudo-versions require the flag. It must satisfy the Runefile's `minimum_version`.

**Rationale**: Same steps as `scripts/install.sh`, no token, no extra action, works on
`ubuntu-latest` and `macos-latest` (both have `curl`, `tar`, `shasum`).

**Alternatives**: `go install ...@vX` (needs a Go toolchain, slow); `gh attestation verify`
(needs a token and permissions we cannot test here); Windows runners (archive is a zip,
untested; out of scope, `--os windows` is a usage error).

## R7. Bundles carry outputs and cache records

**Decision**: A bundle is a gzip-compressed tar of paths relative to the Runefile's
directory: the files matched by the task's evaluated `[cache]` outputs (Rune's own glob
rules; a directory output is taken whole) and its cache record `.rune/cache/<name>.json`.
`rune ci run --dir DIR` writes `DIR/bundle.tar` after a successful body of a `[cache]` task.
Packing refuses an output that resolves outside the project and an output that holds a link
(a link is not followed). Unpacking accepts only regular files and directories with
relative, clean paths that stay inside the project, and writes them through `os.Root`
opened on the project, so a link already in the checkout cannot carry an entry outside it;
anything else fails the job (exit 1) before the body runs. Unpack order: `DIR/in/*` in name order (the
generator prefixes each download directory with the producing job's index, so name order is
topological), then `DIR/bundle.tar`.

**Rationale**: GitHub's artifact upload strips paths to a common ancestor and skips hidden
files, and its glob rules differ from Rune's; one Rune-made archive avoids all three and
keeps output expansion identical to the cache's own rules.

**Alternatives**: uploading output globs directly (path and glob mismatches); `tar` in
shell (no `**` globbing in POSIX shells).

## R8. Cache keys

**Decision**: `rune ci run --key` unpacks `DIR/in`, builds the task's `cache.Spec` exactly as
`Execute` does, computes the fingerprint with `cache.Decide`, and prints
`key=rune-<job slug>-<hex>` where `<hex>` is SHA-256 over: the fingerprint, the node key
(`name(args.Key())`), the Rune version, `GOOS`, `GOARCH`, and the `ImageOS` environment
variable when set. A fingerprint error fails the step. Restore uses that exact key, with no
`restore-keys`. Save runs only when every earlier step succeeded and the restore was not an
exact hit (`if: steps.restore.outputs.cache-hit != 'true'`, which GitHub joins with
`success()`).

**Rationale**: The fingerprint already covers inputs, body, used variables and executor
(Principle I). The extra context covers the platform and Rune itself. Whether a hit skips the
body is still decided by `cache.Decide` at run time (stored hash equals current, every output
exists), so a bundle without its outputs runs the body.

## R9. Artifacts

**Decision**: A node whose task declares `[cache(outputs=...)]` uploads `DIR/bundle.tar` as
artifact `rune-<job id>` with `retention-days: 1`, `if-no-files-found: error` and
`overwrite: true` (re-runs). Every job downloads, one step each, the artifacts of all its
transitive needs that have outputs, into `DIR/in/<NNNN>-<job id>`.

**Rationale**: Locally a task sees every output written before it; in CI the jobs before it
are exactly its transitive needs (R2).

## R10. Workflow shape and safety

**Decision**: triggers `push` (branches: `--branch`, default `main`) and `pull_request`;
`permissions: contents: read`; `actions/checkout@v4` with `persist-credentials: false`;
actions pinned by major tag (`actions/checkout@v4`, `actions/cache/restore@v4`,
`actions/cache/save@v4`, `actions/upload-artifact@v4`, `actions/download-artifact@v4`), as
Rune's own workflows pin them. Every `run:` step sets `shell: bash`. Runner values reach
scripts only as environment variables (`$RUNNER_TEMP`, `$GITHUB_OUTPUT`, `$GITHUB_PATH`);
`${{ }}` appears only in `with:` and `if:`. When the Runefile is below the repository root,
the workflow sets `defaults.run.working-directory`.

Arguments are single shell words (bare when made only of `A-Za-z0-9_./:@%+=,-`, single-quoted
otherwise, `'` written as `'\''`); every YAML scalar from the Runefile is single-quoted with
`''` for `'`. Run scripts are literal blocks (`|`).

## R11. Job IDs and labels

**Decision**: slug = task name and arguments joined by `-`, lowercased, every run of other
characters than `a-z0-9_` replaced by `-`, trimmed of `-`, prefixed with `t-` when it does
not start with a letter, cut so that the whole ID, with its suffixes, stays within 64 bytes;
with more than one OS, `-linux` or `-macos` is appended; a clash gets `-2`, `-3`... in job
order. Label: `TASK ARG...` with arguments as
shell words, and ` (OS)` with more than one OS.

## R12. Owned file, check and determinism

**Decision**: The file starts with
`# Generated by rune ci github. Do not edit: change the Runefile, then run:` and
`#   rune ci github [--os OS]... [--branch B] --rune-version V (--write | --output REL) ROOTS`.
REL is the target relative to the Runefile's directory; `-f` is not recorded, so the bytes do
not depend on the working directory. Write and check plan the file the same way: an existing
file whose first line is not that marker is refused (exit 2); a file equal to the plan is
current (no write, mtime kept); otherwise write atomically (temp file and rename, as `rune
export` writes). Check reports `current`, `stale`, `missing` or `not generated by rune ci
github`, and prints `regenerate with:` and the command.

## R13. Validation with a workflow linter

**Decision**: golden workflows live in `internal/ci/testdata/`. CI's lint job runs
`actionlint` (`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`) on them and on the
example workflow; a Runefile task `actionlint` runs the `rhysd/actionlint:1.7.12` image on the
same files for local use. Unit tests also parse every golden file with `go.yaml.in/yaml/v3`
(already a dependency) and check its structure.

**Rationale**: no new module in `go.mod`; actionlint knows the inputs of the pinned actions
and runs shellcheck on `run:` scripts.

## R14. Testing the contract end to end

**Decision**: `test/integration/ci_github_test.go` generates a workflow with the real binary,
parses it, and simulates GitHub: jobs in `needs:` order, each in a fresh copy of the project
with its own `RUNNER_TEMP`; `run:` scripts run with `bash`; the install step is skipped
(the test binary is on `PATH`); artifacts and caches are in-memory maps with exact keys; a
failed step skips dependent jobs. Bodies append to a log outside the project, so runs are
counted. The test compares with a local `rune ROOTS` run, repeats the workflow to see cache
hits, and strips an output from a stored bundle to see the body run again. Skipped on Windows
(no supported runner).

## R15. `ci` becomes a built-in command

**Decision**: `rune ci` is a cobra parent with `github` and `run`. With no subcommand it
prints the note about a task named `ci` and the help; an unknown argument is a usage error
that names `rune -- ci`. `ci` joins `taskimport.RuneCommands`. The example
`docs/examples/ci-cd` (which has a task named `ci`) is updated to `rune -- ci`.

**Rationale**: same pattern as `agent`, `export` and `suggest`; the commit is marked
breaking.
