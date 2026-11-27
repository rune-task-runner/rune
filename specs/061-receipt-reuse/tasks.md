---

description: "Task list for spec 061: reuse of trusted receipts in CI"
---

# Tasks: Reuse of Trusted Receipts in CI

**Input**: Design documents from `/specs/061-receipt-reuse/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI asks for them, and the issue's acceptance
criterion names adversarial fixtures. In every phase the test tasks come first, and a test is
written and seen to fail before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US4)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
  The target is 90% statement coverage of the new code.
- Git fixtures are temporary repositories made with `git init`, with `user.name` and
  `user.email` set locally; tests never touch the repository they run in.
- Golden files are regenerated with `-update`, never edited by hand.
- Keep it simple: no abstraction, option or helper that a task below does not name.
- New prose and comments avoid the em dash.

---

## Phase 1: Setup

No setup: every change lands in existing packages (plan.md Structure Decision).

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the attribute's name, the receipt field, the decision function, the flags. Every
story needs them.

- [X] T001 [P] Write failing tests in `internal/parser/reuse_test.go`: `[reuse]` on a task parses
  to one attribute of kind `reuse` with no argument; `[reuse, cache(inputs=["a"])]` parses both;
  `[reuse(x)]` is a parse error. The existing `TestParserKnowsAllASTAttributes` must fail once
  `reuse` is in `ast.KnownAttributes` and not in the parser
- [X] T002 Add `AttrReuse = "reuse"` with a comment `// the task may be skipped on a trusted receipt (spec 061)` to the attribute constants in `internal/ast/ast.go`, append it to `KnownAttributes` after `AttrAgentSummary`, and add `ast.AttrReuse` to the bare-attribute case of `parseAttrItem` in `internal/parser/attribute.go`; T001 passes
- [X] T003 Add the registry entry `{Name: "reuse", Kind: BuiltinAttribute, Signature: "[reuse]", Documentation: "Let rune skip the task when --reuse-key is given and a receipt signed by one of those keys shows it passed, recently, on exactly these files, arguments and Rune. Its result must depend on nothing else."}` after `agent-summary` in `internal/language/builtin.go`, and `reuse` in the attribute pattern of `editors/vscode/syntaxes/runefile.tmLanguage.json`; `TestRegistryMatchesASTAttributes` and the completion and hover tests in `internal/language` pass, with one new hover case for `reuse` in `internal/language/hover_test.go`
- [X] T004 [P] Write a failing test in `internal/receipt/receipt_test.go`: a `Receipt` with
  `Overrides: []string{"a", "b"}` marshals `"overrides":["a","b"]` right after
  `argumentsRedacted`, a receipt without overrides omits the key, and `ParseStatement` reads it
  back. Then add `Overrides []string \`json:"overrides,omitempty"\`` after `ArgumentsRedacted`
  in `internal/receipt/receipt.go` (contracts/receipt.md: "sorted, masked names ...; omitted when
  there are none")
- [X] T005 Refactor `Evaluate` in `internal/receipt/verify.go`: move its candidate loop (dedupe by
  trimmed line, `judge`, problem collection, project filter, grouping by task) into
  `judgeAll(cands, trusted, project, commit, tree) (map[string][]judged, []Problem)`, and call it
  from `Evaluate`. Add `TestJudgeAll` in `internal/receipt/verify_test.go` (a duplicate line counts
  once, a malformed line is a problem, another project's receipt is dropped, two tasks are grouped
  apart); every existing test in `internal/receipt` passes unchanged
- [X] T006 Write failing tests in `internal/receipt/reuse_test.go` for `Reuse(cands, trusted, want)`
  using receipts signed in the test: a qualifying receipt is verified with its ID and signer; the
  newest of two qualifying receipts wins; one case per reason of data-model.md (spec 060's through
  `uncovered-inputs`, then `stale` for older than `MaxAge`, `stale` for `FinishedAt` after `Now`,
  `other-platform` for OS and for arch, `other-rune` for version and for commit,
  `other-definition`, `masked-arguments` for the receipt's flag and for `Want.ArgumentsMasked`,
  `other-arguments`, `overridden` for the receipt's field and for `Want.Overridden`,
  `outputs-differ` for a different digest, a receipt with outputs and none now, and none in the
  receipt with outputs now); no candidate gives `no-receipt`; a `cached` receipt never qualifies;
  a receipt of another project is ignored; the reason reported is that of the newest candidate
  whose snapshot is `Want.Tree`, else of the newest
- [X] T007 Implement `internal/receipt/reuse.go`: the reason constants `ReasonStale = "stale"`,
  `ReasonOtherPlatform = "other-platform"`, `ReasonOtherRune = "other-rune"`,
  `ReasonOtherDefinition = "other-definition"`, `ReasonMaskedArguments = "masked-arguments"`,
  `ReasonOtherArguments = "other-arguments"`, `ReasonOverridden = "overridden"`,
  `ReasonOutputsDiffer = "outputs-differ"`; `type Want struct` with the fields of data-model.md;
  `Reuse` calls `judgeAll(cands, trusted, want.Project, "", want.Tree)`, applies
  `reuseReason(r Receipt, want Want) string` in the data-model.md order to each candidate whose
  spec 060 reason is empty, and returns `verdictOf(want.Task, ...)`. Outputs compare
  `digest(r.Outputs) == digest(want.Outputs)` where a nil `*Outputs` has digest `""` (research R6).
  T006 passes
- [X] T008 [P] Write failing tests in `internal/receipt/notes_test.go` for
  `Repo.NotesForTree(ctx, tree)`: no notes ref gives none and no error; the lines of the notes of
  two commits that share a tree are both returned; a note on a commit with another tree is not;
  blank lines are dropped; the repository's refs, index and notes are unchanged afterwards. Then
  implement it in `internal/receipt/notes.go` with `git notes --ref=refs/notes/rune-receipts list`,
  one `git cat-file --batch-check=%(objectname)` fed `COMMIT^{tree}` lines, and `git cat-file blob`
  per matching note (research R3)
- [X] T009 [P] Write failing tests in `internal/cli/verify_test.go` for
  `publicKeyFiles(flag string, files []string) ([]ed25519.PublicKey, error)`: two files give their
  keys in order; an unreadable file and a file with no Ed25519 key are `*UsageError`s whose text
  starts with `FLAG FILE:`. Then extract it from `trustedKeys` in `internal/cli/verify.go` and call
  it there with `"--key"`; the existing verify and attach tests pass unchanged
- [X] T010 Add `ReuseKeys []string` and `ReuseMaxAge time.Duration` with a comment (the trusted
  keys and age of spec 061; never settable from a Runefile) to `Options` in
  `internal/cli/dispatch.go`
- [X] T011 Write failing tests in `cmd/rune/root_test.go` and `cmd/rune/ci_test.go`: both the run
  command and `ci run` accept `--reuse-key` twice and `--reuse-max-age 2h` and pass them in
  `Options`; the default age is 24h; `--reuse-max-age 1h` alone is a usage error
  `--reuse-max-age needs --reuse-key`; `--reuse-max-age 0` and `-1h` are usage errors
  `--reuse-max-age must be a positive duration, such as 24h`; `rune serve --reuse-key x` is an
  unknown flag. Then add one helper `reuseFlags(f *pflag.FlagSet, opts *cli.Options)` and one
  check `checkReuseFlags(cmd *cobra.Command, opts *cli.Options) error` in `cmd/rune/root.go`, used
  by the root command's `RunE` and by `newCIRunCmd` in `cmd/rune/ci.go`, with the help texts of
  contracts/cli.md

**Checkpoint**: the attribute parses, the decision function and note lookup are tested, and the
flags reach `Options`. Nothing is reused yet.

---

## Phase 3: User Story 1 - CI skips a task it already proved (Priority: P1) MVP

**Goal**: with `[reuse]` and `--reuse-key`, a task with a qualifying local receipt is not run, says
so, succeeds, and its receipt says `cached`.

**Independent Test**: record a passing receipt with a test key, run again with `--reuse-key` naming
it, and check that the body did not run, the notice, the exit code and the new receipt.

### Tests for User Story 1

- [X] T012 [P] [US1] Write failing tests in `internal/cli/reuse_test.go` for `newReuser(opts,
  settings, signer)`: nil with no `ReuseKeys`; a `*UsageError` with the contracts/cli.md text when
  receipts are off, when the signer has no repository, and when a key file is bad; the keys are
  exactly those of the files (this machine's key is not added even when it exists); `ReuseMaxAge`
  0 means 24h
- [X] T013 [US1] Write failing engine tests in `internal/cli/reuse_test.go` with
  `receiptProject` and `runReceipts`: a passing `[reuse]` task run once, then again with the local
  public key file in `ReuseKeys`, does not run its body the second time, prints
  `reused: TASK (receipt ID, signed by KEYID)`, returns nil, and its new receipt is `cached` with
  `related.reuses` set to the first ID; without `ReuseKeys` the body runs and stderr holds no
  `reuse`; a task without `[reuse]` runs and prints nothing about reuse; a `[reuse, cache(...)]`
  task with a current cache prints `cached:` and is not reused; after a cache miss with a
  qualifying receipt it is reused, its receipt lists its declared inputs, and no `running:` line
  is printed; a `[reuse]` dependency and its `[reuse]` dependent are both reused; `--dry-run`
  with `ReuseKeys` prints `would run`; a dependency body that edits the Runefile makes its
  `[reuse]` dependent print `not reused: TASK (the Runefile changed after it was loaded)` and
  run; a `[reuse]` failure hook runs its body after a failure (never reused); a `[reuse]`
  post-hook with a qualifying receipt is reused; an MCP call of a qualifying `[reuse]` task
  through the adapter runs its body; and, with `run.snapErr` set before `tryReuse` decides (a
  unit test of the notice path), the notice is
  `not reused: TASK (the files could not be snapshotted: ...)`; and with an `inputsErr` given to
  `tryReuse`, the notice is `not reused: TASK (the declared inputs could not be listed: ...)`
  and no snapshot is taken
- [X] T014 [P] [US1] Write a failing test in `internal/cli/receipts_test.go`: `bodyStarting` after
  a snapshot was taken keeps it (no second `takeBefore`), and still takes one when none was taken
- [X] T015 [US1] Write a failing test in `internal/cli/receipts_test.go` for
  `currentOutputs(task, params)`: a `[cache]` task with two output files gives the same digest as
  the receipt a passed run records, writes no manifest even above 64 files, and a task without
  `[cache]` gives nil
- [X] T016 [P] [US1] Write failing binary tests in `test/integration/reuse_test.go` (helpers from
  `receipts_test.go`, a CI key in `RUNE_RECEIPT_KEY` and its public key file outside the project):
  run, then `rune --reuse-key ci.pub TASK` skips the body (a counter file), prints `reused:`,
  exits 0, and `rune verify --key ci.pub HEAD TASK` still verifies the first run; after the first
  receipt's file is removed, `rune verify --key ci.pub HEAD TASK` reports `cached` and exits 1
  (SC-001); `rune ci run --reuse-key ci.pub TASK` reuses the same way

### Implementation for User Story 1

- [X] T017 [US1] In `internal/cli/receipts.go`: make `bodyStarting` call `takeBefore` only when
  `!r.snapped`; split `receiptOutputs` into `currentOutputs(task, params)` (files, masked paths,
  `receipt.BuildOutputs`, no manifest write) and the manifest write it keeps; T014 and T015 pass
  and every existing receipts test passes
- [X] T018 [US1] Create `internal/cli/reuse.go`: `type reuser struct` (keys, maxAge, overridden;
  the note cache comes in T031); `newReuser(opts Options, settings config.Settings, signer
  *receiptSigner) (*reuser, error)` with the usage errors of contracts/cli.md, keys read with
  `publicKeyFiles("--reuse-key", ...)`; `(e *engine) tryReuse(task *ast.Task, params eval.Args,
  run *receiptRun, inputs []string, inputsErr error) bool` that returns false unless
  `e.reuse != nil`, `run != nil` and the task has `[reuse]`; on `inputsErr` prints `not reused`
  with `the declared inputs could not be listed: ERROR` and returns false without a snapshot;
  takes the snapshot with `e.takeBefore(run)` (no second snapshot later, T017); on a snapshot
  error or `run.stale`
  prints `not reused` with the contracts/cli.md text and returns false; else lists
  `.rune/receipts/` with `receipt.List`, builds `receipt.Want` (project from the signer, tree
  `run.before`, `s.rune`, `definitionDigest`, `receiptArgs`, `Overridden: e.reuse.overridden`,
  `currentOutputs`, `Now`, `MaxAge`), calls `receipt.Reuse`; on a verdict sets `run.cached = true`,
  `run.inputs = inputs` and `run.related.Reuses` to the receipt's ID itself (not through
  `cachedHit`, which would snapshot again; research R9), prints `reused: TASK (receipt ID, signed
  by KEYID)` muted, and returns true; otherwise prints `not reused: TASK (TEXT)` muted with
  `reuseReasonText(v)` and returns false
- [X] T019 [US1] Add `reuseReasonText(v receipt.Verdict) string` in `internal/cli/reuse.go`:
  the texts of contracts/cli.md for `no-receipt` and the eight new reasons (the age printed with
  `time.Duration.String`), and `reasonText(v, "these files")` for spec 060's others; a unit test
  per text in `internal/cli/reuse_test.go`
- [X] T020 [US1] In `internal/cli/run.go`: add `reuse *reuser` to `engine` with a comment; in
  `execute`, after the receipt recorder is set up and only when `plan == planRun`, set
  `eng.reuse` from `newReuser` and return its error; in `executeTask`, call
  `tryReuse(task, params, run, d.InputFiles, derr)` after a `[cache]` decision that did not hit,
  before the `running:` line, and `tryReuse(task, params, run, nil, nil)` before `bodyStarting`
  for a task without `[cache]`, returning nil when it reused (FR-010, FR-016).
  T012, T013 and T016 pass

**Checkpoint**: US1 works with local receipts: the MVP.

---

## Phase 4: User Story 2 - Evidence that does not qualify never skips a task (Priority: P1)

**Goal**: every forged, stale, incompatible or incomplete receipt leaves the task to run, with the
reason printed.

**Independent Test**: one adversarial fixture per reason; the body runs and the notice names it.

### Tests for User Story 2

- [X] T021 [P] [US2] Write failing tests in `internal/cli/receipts_test.go`: a run with
  `name=value` overrides writes `"overrides":["name"]` (sorted, masked) in every receipt of the
  invocation; a run without writes no `overrides`; an MCP call's receipt has none
- [X] T022 [P] [US2] Write failing binary tests in `test/integration/reuse_test.go`, one subtest per
  fixture, each asserting the body ran and `not reused: TASK (` with the reason's text: changed
  payload byte and changed signature byte (`rcptRewrite`); signed by an untrusted key; signed by
  this machine's own local key when only `ci.pub` is given; `RUNE_RECEIPT_KEY`'s own key not given;
  finished 25h ago and in the future (`rcptResign` with `FinishedAt` moved); `failed`,
  `interrupted`, `refused`, `cached` and `incomplete` statuses (re-signed); other files (an
  untracked file added); `changed-during-run`; uncovered Runefile and uncovered inputs; other OS,
  other arch, other Rune version, other definition, other arguments, masked arguments (a secret
  argument); `overridden` in the receipt and now; another project's receipt in the same
  repository; changed `[cache]` output. Also: a secret fixture value appears 0 times in stderr and
  in `.rune/receipts/` (SC-008)

### Implementation for User Story 2

- [X] T023 [US2] In `internal/cli/receipts.go`: add `overrides []string` to `recorder` and to
  `newRecorder`'s parameters; `finishReceipt` sets `rc.Overrides` from it; in
  `internal/cli/run.go` pass the sorted, masked names of `overrides` (nil when none) and set
  `reuser.overridden = len(overrides) > 0`; the MCP adapter in `internal/cli/mcp.go` passes nil.
  T021 passes
- [X] T024 [US2] Make every subtest of T022 pass; fix any reason ordering or text the fixtures
  expose in `internal/receipt/reuse.go` or `internal/cli/reuse.go`, with a unit test for each fix

**Checkpoint**: SC-002 to SC-005 and SC-008 hold.

---

## Phase 5: User Story 3 - The author decides which tasks may be reused (Priority: P1)

**Goal**: `[reuse]` is refused where a skip would hide an effect or an uncovered input.

**Independent Test**: analyze Runefiles with each combination and check the diagnostics.

### Tests for User Story 3

- [X] T025 [P] [US3] Write failing tests in `internal/analyzer/reuse_test.go`: `[reuse]` alone is
  clean; with `[confirm]`, `[plan(...)]`, `[applies(...)]`, `[network]` each is one RUNE2022 error at
  the `[reuse]` span with the contracts/runefile.md message; on an agent-executor task it is one
  error; a second `[reuse]` is a duplicate error with a related location; `[reuse]` with `[cache]`,
  `[parallel]`, `[readonly]` and an OS attribute is clean
- [X] T026 [P] [US3] Add fixtures `testdata/corpus/reuse.rune` (a `set receipts` file with
  `[reuse]` alone, with `[cache(...)]`, and in a multi-attribute list) and
  `testdata/fmt/reuse.rune`; generate `testdata/corpus/reuse.ast` and `testdata/fmt/reuse.fmt` with
  `-update` in `test/corpus` and `internal/formatter`, and check the output by eye

### Implementation for User Story 3

- [X] T027 [US3] Add `CodeInvalidReuse = "RUNE2022"` with a comment to `internal/diag/codes.go`,
  add a `[reuse, confirm]` row with `diag.CodeInvalidReuse` and `diag.Error` to
  `TestSemanticCodes` in `internal/analyzer/codes_test.go`, create `internal/analyzer/reuse.go`
  with `checkReuse()` (uses `firstOf`, the conflict list with the reasons of
  contracts/runefile.md, `t.Executor == ast.ExecAgent`), and call it from `Analyze` after
  `checkAgentOutput` in `internal/analyzer/analyzer.go`. T025 passes
- [X] T028 [US3] Document the attribute: the `bare_attr` production and a `[reuse]` paragraph in
  `docs/GRAMMAR.md`; an entry in the attribute list and a short section in `docs/runefile.md`
  linking to `how-to/receipts.md#reuse-evidence-in-ci`; a `RUNE2022` row in `docs/diagnostics.md`

**Checkpoint**: US3 is independently complete.

---

## Phase 6: User Story 4 - Evidence from a pull request counts after the merge (Priority: P2)

**Goal**: receipts in notes are found by tree, so a merge commit with the same files reuses them.

**Independent Test**: attach on commit A, create commit B with A's tree, push and fetch the notes
through a bare remote, reuse on B in a clone; a commit C with another tree does not.

### Tests for User Story 4

- [X] T029 [P] [US4] Write failing engine tests in `internal/cli/reuse_test.go`: a receipt only in
  the note of commit A is reused at commit B with A's tree; the same receipt in the directory and
  the note counts once; a note line that is not a receipt is ignored and the other candidates
  decide; at a commit with another tree the task runs with `no receipt for these files`
- [X] T030 [P] [US4] Write a failing binary test in `test/integration/reuse_test.go`: run with the CI
  key and attach on commit A, push the notes ref to a bare remote, clone, fetch
  `refs/notes/rune-receipts`, make an empty commit (same tree), and `rune --reuse-key ci.pub TASK`
  in the clone is reused with no local receipt; a clone at a commit with another tree runs the
  task (SC-007)

### Implementation for User Story 4

- [X] T031 [US4] In `internal/cli/reuse.go`: add `mu sync.Mutex` and `notes map[string][][]byte` to
  `reuser` and `(u *reuser) noteLines(ctx, repo, tree) [][]byte`, which calls
  `repo.NotesForTree` once per tree and prints one warning and uses none when it fails; append
  those lines to the candidates in `tryReuse` with origin `receipt.OriginNote`. T029 and T030 pass

**Checkpoint**: every story works.

---

## Phase 7: Polish and cross-cutting concerns

- [X] T032 Write the section "Reuse evidence in CI" in `docs/how-to/receipts.md` (research R14):
  `[reuse]` and what the author vouches for; `--reuse-key` and `--reuse-max-age`; candidates and
  `git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts`; the reason table of
  contracts/cli.md; a GitHub Actions example (pull request job signs with the secret, attaches and
  pushes the notes with `contents: write`; the `main` job fetches the notes and runs with
  `--reuse-key` and a key file from the base branch or a repository variable); limits added to
  "What a receipt proves": trust is in the key's holder, not the code; keys must come from where
  the contributor cannot change them; environment, tools and ignored files are not covered; a
  reused run is not evidence; tasks without `[reuse]` always run. Also replace the line in "Sign
  receipts in CI" that only mentions verification, if it now reads wrong
- [X] T033 [P] Add `--reuse-key` and `--reuse-max-age` rows to the global flags table of
  `docs/cli.md`, and both flags to the `rune ci run` row
- [X] T034 Run the docs harness in `test/docs` (`docker compose run --rm -e
  GOFLAGS=-buildvcs=false test go test ./test/docs/...`) and fix what it reports in
  `docs/how-to/receipts.md`, `docs/cli.md`, `docs/GRAMMAR.md`, `docs/runefile.md` and
  `docs/diagnostics.md`
- [X] T035 Run the full suite, the race suite and golangci-lint as `.github/workflows/ci.yml` pins
  it (memory: docker-test-invocation); fix every failure in the files of this feature; give
  root-owned files back with `chown`
- [X] T036 Measure coverage of `internal/receipt`, `internal/cli`, `internal/analyzer` and
  `cmd/rune` with `-coverprofile`; add unit tests in their `_test.go` files until each new or
  changed function is covered branch by branch, with the target of 90% for the new code
- [X] T037 Walk through `specs/061-receipt-reuse/quickstart.md` with a binary built in Docker and
  fix any step that does not behave as written

---

## Dependencies and execution order

- Phase 2 blocks every story. Inside it: T001 before T002; T002 before T003; T004, T005, T008,
  T009 are independent; T005 before T006 and T007; T010 before T011.
- US1 (Phase 3) needs T002, T007, T009, T010, T011.
- US2 (Phase 4) needs US1.
- US3 (Phase 5) needs only T002; it can run beside US1.
- US4 (Phase 6) needs US1 and T008.
- Polish needs every story.

## Parallel examples

```text
Phase 2: T001, T004, T005, T008, T009 together (different files)
US1:     T012, T014, T015, T016 together; then T017 -> T018 -> T019 -> T020
US3:     T025 and T026 together, beside US1
US4:     T029 and T030 together; then T031
```

## Implementation strategy

1. Phase 2, then US1: reuse from local receipts with a trusted key. Stop and validate: this is
   the MVP.
2. US2: the adversarial fixtures; the feature is safe to ship only after this.
3. US3: the analyzer rules (can be done beside US1).
4. US4: notes found by tree.
5. Polish: docs, full gate set, coverage, quickstart.
