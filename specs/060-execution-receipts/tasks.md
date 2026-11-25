---

description: "Task list for spec 060: signed execution receipts, rune verify and Git notes"
---

# Tasks: Signed Execution Receipts, `rune verify` and Git Notes

**Input**: Design documents from `/specs/060-execution-receipts/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI asks for them, and the issue's acceptance
criteria name them. In every phase the test tasks come first, and a test is written and seen to
fail before the change that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US6)

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

**Purpose**: the new package.

- [X] T001 Create `internal/receipt/doc.go` with a package comment: the package defines Rune's
  receipt format version 1, signs and opens DSSE envelopes with Ed25519 keys, stores receipts,
  takes git snapshots of the working tree, reads and writes the receipts notes, and evaluates
  receipts against a revision; it knows nothing about tasks or the CLI (plan.md Structure
  Decision)

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the format, signing, keys, storage, snapshots, the setting, the cache field, the
MCP session accessor, the key variable and the reserved names. Every story needs them.

- [X] T002 [P] Write failing tests in `internal/receipt/receipt_test.go`: `NewID(now)` matches
  `^\d{8}T\d{6}Z-[0-9a-f]{16}$` with the UTC time of `now` and differs across 1000 calls; a
  `Receipt` marshals its fields in data-model.md order and omits `parentInvocation`,
  `argumentsRedacted`, `related` and its members, `exitCode`, `source`, `source.commit`,
  `uncoveredInputs`, `outputs` and `claims` when empty; `Statement(r)` gives `_type`
  `https://in-toto.io/Statement/v1`, `predicateType`
  `https://rune-task-runner.github.io/rune/receipt/v1`, subject
  `{"name":"worktree","digest":{"gitTree":before}}` with a source and
  `{"name":project,"digest":{"sha256":runefileDigest}}` without one
- [X] T003 Implement `internal/receipt/receipt.go`: `Version = 1`, `PredicateType`, the status
  constants (`passed`, `failed`, `interrupted`, `refused`, `cached`, `incomplete`), channels
  (`cli`, `ci-run`, `mcp`, `agent`), roles (`task`, `failure-hook`), the types `Receipt`,
  `Arg`, `Related`, `RuneInfo`, `RunefileEntry`, `Source`, `Outputs`, `OutputFile`, `Claims`,
  `Client` exactly as contracts/receipt.md, `NewID(now time.Time) string` (crypto/rand, 8
  bytes), and `Statement(r Receipt) ([]byte, error)`
- [X] T004 [P] Write failing tests in `internal/receipt/dsse_test.go`: `PAE` returns
  `DSSEv1 28 application/vnd.in-toto+json N BODY` byte for byte; `Sign` then `Open` with the
  signer's public key returns the statement bytes and the key ID; `Open` fails with
  `ErrBadSignature` for one changed byte of payload, signature or payloadType when the
  `keyid` names a trusted key, with `ErrUnknownSigner` when no trusted key has that `keyid`, and with `ErrMalformed` for non-JSON, a
  missing field, bad base64, zero or two signatures; the encoded envelope is one line ending in
  `\n` and the same for the same input
- [X] T005 Implement `internal/receipt/dsse.go`: `Envelope`, `PAE(payloadType string, body
  []byte) []byte`, `Sign(key ed25519.PrivateKey, statement []byte) ([]byte, error)`,
  `Decode(line []byte) (Envelope, []byte, error)` (envelope and raw payload, no signature
  check, so a caller can attribute an unsigned or untrusted receipt to its task) and
  `Open(line []byte, trusted []ed25519.PublicKey) (payload []byte, keyID string, err error)`,
  with the sentinel errors `ErrMalformed`, `ErrUnknownSigner`, `ErrBadSignature`
- [X] T006 [P] Write failing tests in `internal/receipt/key_test.go`: `ParsePrivateKey` accepts
  an `openssl genpkey -algorithm ed25519` style PKCS#8 PEM (fixture generated in the test with
  `x509.MarshalPKCS8PrivateKey`) and rejects an RSA key, an EC key, garbage and an empty
  string with a message naming the problem; `ParsePublicKeys` returns every `PUBLIC KEY` block
  of a file and fails when there is none or one is not Ed25519; `KeyID` equals `SHA256:` plus
  unpadded base64 of SHA-256 of the PKIX DER for a fixed key; `PublicPEM` round-trips;
  `LoadOrCreate(dir)` creates `dir` `0700` and `key.pem` `0600` (on Unix), reports created
  true then false, and 8 concurrent calls on an empty dir return the same key; `LoadExisting(dir)`
  returns `ok=false` without creating anything when the file is missing
- [X] T007 Implement `internal/receipt/key.go`: `ParsePrivateKey`, `ParsePublicKeys`, `KeyID`,
  `PublicPEM`, `LoadOrCreate(dir string) (key ed25519.PrivateKey, created bool, err error)`
  writing a temporary file then `os.Link` to `key.pem` (an existing file wins and is read), and
  `LoadExisting(dir string) (ed25519.PrivateKey, bool, error)` (research R10)
- [X] T008 [P] Write failing tests in `internal/receipt/store_test.go`: `Write(dir, id, line)`
  creates `dir` with a `.gitignore` holding `*\n`, writes `ID.json` holding exactly `line`,
  fails without changing anything when `ID.json` exists, leaves no temporary file behind on
  success or failure; `WriteManifest(dir, id, data)` writes `ID.outputs.json` the same way;
  `List(dir)` returns `(name, bytes)` for every `*.json` except `*.outputs.json`, sorted by
  name, and an empty result for a missing directory
- [X] T009 Implement `internal/receipt/store.go`: `Dir(root string) string`
  (`root/.rune/receipts`), `Write`, `WriteManifest`, `List`, `ReadManifest(dir, name string)`
  (research R11)
- [X] T010 [P] Write failing tests in `internal/receipt/git_test.go` with temporary
  repositories: `Open(ctx, dir)` returns the top and absolute git directory from a
  subdirectory and from a linked worktree, and `ErrNotRepository` outside one;
  `Snapshot(ctx, exclude)` equals the tree `git add -A && git write-tree` gives in a scratch
  copy, includes untracked files, excludes ignored files and the excluded `REL/.rune`, and
  leaves the bytes of `.git/index`, `HEAD`, every ref and the working tree unchanged; it works
  with no commit and with no index file; `Head(ctx)` returns the commit and its tree, and empty
  values with no commit; `Resolve(ctx, rev)` returns commit and tree and `ErrUnknownRevision`
  for a missing one; `WorktreeMatches(ctx, rev)` is true for a clean checkout and false after
  a modification, a deletion, a staged new file and an untracked file; `CheckIgnore(ctx,
  paths)` returns the ignored, untracked ones; every git call fails cleanly when its context is
  cancelled
- [X] T011 Implement `internal/receipt/git.go`: `Repo{Top, GitDir}`, `Open`, `Snapshot`,
  `Head`, `Resolve`, `WorktreeMatches`, `CheckIgnore`, one `run` helper that sets
  `-c core.fsmonitor=false`, `GIT_OPTIONAL_LOCKS=0`, `LC_ALL=C`, a timeout of one minute on
  the caller's context, and wraps git's standard error into the returned error (research R3,
  R12)
- [X] T012 [P] Write failing tests in `internal/cache/cache_test.go`: `Store(spec, hash,
  createdAt, receipt)` writes `"receipt"` when not empty and omits it when empty; `Decide`
  returns the stored receipt ID in `Decision.Receipt` on a hit; a record written without the
  field is still a hit with an empty `Receipt`
- [X] T013 Add `Receipt` to `cache.Fingerprint` (`json:"receipt,omitempty"`) and to
  `cache.Decision`, add the `receipt string` parameter to `cache.Store` in
  `internal/cache/cache.go`, and pass `""` from its caller in `internal/cli/run.go` for now
  (research R8)
- [X] T014 [P] Write failing tests: `internal/config/settings_test.go` (`set receipts` sets
  `Settings.Receipts`; absent leaves it false), `internal/language/builtin_test.go` (`receipts`
  is a valid `BuiltinSetting` with signature `set receipts`), and an analyzer case that
  `set receipts` raises no diagnostic
- [X] T015 Add `Receipts bool` to `config.Settings` and `case "receipts"` in `ResolveSettings`
  in `internal/config/settings.go`; add the `receipts` entry to `builtinSettings` in
  `internal/language/builtin.go` with documentation "Write a signed receipt for every task run
  (see rune verify)." (research R15)
- [X] T016 [P] Write a failing test in `mcpserver/audit_test.go`: inside a tool call,
  `AuditFromContext(ctx).Session()` returns the session the call was recorded with (transport,
  auth, client name and version); on a nil `*CallAudit` it returns the zero value
- [X] T017 Store the session in `CallAudit` when `auditCall` creates it and add
  `func (c *CallAudit) Session() AuditSession` in `mcpserver/audit.go` (research R7)
- [X] T018 [P] Write failing tests in `cmd/rune/root_test.go`: with `RUNE_RECEIPT_KEY` set, the
  pre-run hook copies it into `Options.ReceiptKey` and the process environment no longer holds
  it; `Options.Commit` holds the build commit
- [X] T019 Add `ReceiptKey string` and `Commit string` to `Options` in
  `internal/cli/dispatch.go`; in `cmd/rune/root.go` read `RUNE_RECEIPT_KEY` into
  `opts.ReceiptKey` and `os.Unsetenv` it first thing in `PersistentPreRunE`, and set
  `opts.Commit = commit` (research R10)
- [X] T020 [P] Add `"verify"` and `"receipt"` to `taskimport.RuneCommands` in
  `internal/taskimport/finish.go` and extend `internal/taskimport/finish_test.go` with tasks of
  those names (research R14)

**Checkpoint**: the package can sign, store and snapshot; the setting parses; nothing records yet.

---

## Phase 3: User Story 1 - Every task run leaves a signed receipt (Priority: P1) MVP

**Goal**: with `set receipts`, every body run writes one signed receipt with the right status,
exit code, snapshot, coverage and relations.

**Independent Test**: run fixtures for each outcome in a temporary repository and decode each
receipt (spec US1).

### Tests for User Story 1

- [X] T021 [P] [US1] Write failing tests in `internal/receipt/outputs_test.go`:
  `OutputFiles(root, rel)` returns `{path, size, sha256}` for regular files only, sorted by
  path; `BuildOutputs(files)` with at most 64 files gives `files` inline and `digest` = SHA-256
  of the canonical JSON array; with 65 it gives no `files`, a `manifest` name and the manifest
  bytes whose SHA-256 is `digest`; `CompareOutputs(root, outputs, manifest)` returns `match`,
  `changed`, `missing` per file and `unknown` when the manifest is absent
- [X] T022 [P] [US1] Write failing tests in `internal/cli/receipts_test.go` for the recorder's
  pure parts: `receiptStatus` over the table of research R2 (nil + cache hit, nil, cancelled
  context with and without a started body, `ErrBodyNotRun`, not started, a failing body with
  exit 3, each passed or failed with a snapshot failure becoming `incomplete`); `channelFor`
  (`cli`, `ci-run` when `nodeRun` is set, `agent` when `RUNE_AGENT_RUN=1`, `mcp`);
  `envClaims` keeps only `CI`, `GITHUB_ACTIONS`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`,
  `GITHUB_WORKFLOW`, `GITHUB_REPOSITORY`, masked; `receiptArgs` lists parameters in
  declaration order with list values for variadics and sets `argumentsRedacted` only when the
  mask changed a value; `definitionDigest` changes with the body, an attribute or the executor
  and not with another task; `runefileEntries` marks a file outside the repository and an
  ignored file `covered: false`
- [X] T023 [US1] Write failing engine tests in `internal/cli/receipts_engine_test.go`, each in
  a temporary repository with `set receipts`, `XDG_STATE_HOME` in a temporary directory and
  `execute` called directly: `test: build` passing writes 2 receipts with one invocation ID,
  status `passed`, exit code 0, `source.before == source.after == HEAD^{tree}` on a clean
  tree, and `test` lists `build` in `related.dependencies`; a post-hook's receipt has
  `related.follows`; a body `exit 3` gives `failed` with exit code 3 and its `||` hook's
  receipt has role `failure-hook` and `related.failureOf`; a current `[cache]` task gives
  `cached` with `related.reuses` equal to the ID stored in the cache record; a cancelled
  context gives `interrupted`; a declined `[confirm]` gives `refused` with no exit code; a body
  that appends to a tracked file gives `before != after`; a `[cache]` input that git ignores
  is in `uncoveredInputs`; declared outputs are recorded; without the setting no
  `.rune/receipts` directory and no key exist; `--dry-run` and `--summary` write none; an
  unwritable receipt directory prints `warning: receipt for NAME not written: ...` and the
  exit code stays the task's; an invalid `Options.ReceiptKey` returns a usage error and the
  body's sentinel file is not created; the first run prints
  `receipts: created signing key PATH (SHA256:...)` once; outside a git repository the receipt
  is written without `source` and its subject is the Runefile digest (FR-016); in a repository
  with no commit the receipt has `source.before` and no `source.commit`; `--list` writes none

### Implementation for User Story 1

- [X] T024 [US1] Implement `internal/receipt/outputs.go`: `OutputFiles`, `BuildOutputs`,
  `CompareOutputs` (research R5; contracts/receipt.md "outputs")
- [X] T025 [US1] Implement the recorder in `internal/cli/receipts.go`: `newRecorder(opts,
  root, settings, loaded []string, mask func(string) string, channel string, invocation,
  parent string, env []string) (*recorder, error)` that prepares the signer (`opts.ReceiptKey`
  through `receipt.ParsePrivateKey`, else `receipt.LoadOrCreate` under
  `audit.StateDir(...)/receipts` with the creation notice), opens the repository once, and
  hashes the loaded Runefiles; per run a `*receiptRun` from `begin(task, params, role)` with
  `bodyStarting()`, `cachedHit(source string)` and `finish(err error)`; the relation maps of
  research R6 under one mutex; `finish` takes the after snapshot, builds the `receipt.Receipt`
  with every text masked (research R16), signs, writes (and writes the manifest when needed),
  and on any failure prints the warning of contracts/cli.md without changing the error it
  returns; the helpers tested in T022
- [X] T026 [US1] Wire the recorder into `internal/cli/run.go`: in `execute`, when
  `settings.Receipts` and `plan == planRun`, build it after the mask set and before
  `resolveRoots` (a signer error returns `&UsageError{...}` before any body); add a `receipts
  *recorder` field to `engine`; rename the current `Execute` body to `executeTask(task,
  params, run *receiptRun)` and `ExecuteFailHook` body to `executeFailHook(..., run)`, with
  `Execute` and `ExecuteFailHook` calling `begin`/`finish` around them when `e.receipts` is
  set; call `run.bodyStarting()` right before each `runBody`/`runBodyTo` of a body and
  `run.cachedHit(d.Receipt)` on a cache hit; pass the run's receipt ID to `cache.Store`;
  channel from `channelFor`, invocation `audit.NewID()` on the command line and the
  `RUNE_INVOCATION_ID` value as parent; plus the `rec.found()` files
- [X] T027 [US1] Write and pass `test/integration/receipts_test.go` part 1 with the real
  binary: a passing and a failing run in a temporary repository produce verifiable files
  (decode with `internal/receipt`); two `rune` processes running `[parallel]` fixtures at
  once write exactly one receipt per task run, each naming only its own invocation (SC-008);
  without `set receipts` no file appears under `.rune/receipts/` or `XDG_STATE_HOME` (SC-007)

**Checkpoint**: receipts are recorded on the command line; the MVP is usable with manual
inspection.

---

## Phase 4: User Story 2 - Verify a revision without running anything (Priority: P1)

**Goal**: `rune verify REVISION TASK...` with verdicts, reasons, working tree report, JSON and
exit codes (contracts/cli.md).

**Independent Test**: record, apply one change per fixture, verify (spec US2).

### Tests for User Story 2

- [X] T028 [P] [US2] Write failing tests in `internal/receipt/verify_test.go` with signed
  fixtures built in the test: `Evaluate(candidates, trusted, task, commit, tree)` accepts a
  `passed` receipt with `before == after == tree` and marks it `exact` when its commit is
  `commit` and `dirty` is false; returns each reason of data-model.md "Verdict" for a candidate
  built to fail only that check, in the order of research R12 step 4; picks the newest
  accepted receipt by `finishedAt` then ID; without an accepted one, reports the newest
  candidate whose snapshot matches, else the newest for the task, else `no-receipt`; puts a
  malformed candidate in `problems` and never in a task's verdict; counts an envelope present
  both locally and in the note once, with origin `local`
- [X] T029 [P] [US2] Write failing tests in `internal/receipt/notes_test.go` for the read side:
  `ReadNote(ctx, repo, commit)` returns no lines when `refs/notes/rune-receipts` does not exist
  or has no note for the commit, and the non-blank lines of the note otherwise
- [X] T030 [P] [US2] Write failing tests in `internal/cli/verify_test.go`: text lines of
  contracts/cli.md for passed exact, passed on an identical snapshot, each reason, the working
  tree matching and differing, an output `changed`, a malformed problem; `--json` prints one
  document with the fields of contracts/cli.md and nothing else on standard output; exit 0 when all
  named tasks verify, 1 otherwise, 2 for no task, an unknown revision, outside a repository, a
  missing or invalid `--key` file; the trusted set holds `Options.ReceiptKey`'s public key or
  the existing local key and never creates one; after any verify the repository's refs, object
  count (`git count-objects -v`), index bytes and the receipts directory are unchanged; with a
  task named `verify` in the Runefile the note
  `note: "verify" is also a task of this Runefile; run it with: rune -- verify` is printed

### Implementation for User Story 2

- [X] T031 [US2] Implement `internal/receipt/verify.go`: `Candidate{Line []byte, Origin
  string}`, `Verdict`, `Problem`, `Summary`, the reason constants, and `Evaluate` (research R12)
- [X] T032 [US2] Implement `ReadNote` in `internal/receipt/notes.go` (`git notes
  --ref=refs/notes/rune-receipts list` to find the blob, `git cat-file blob` to read it)
- [X] T033 [US2] Implement `Verify(opts Options, flags VerifyFlags, args []string) error` and
  `VerifyTaskNote(opts Options)` in `internal/cli/verify.go`: resolve the Runefile with
  `config.Resolve` (no parse), open the repository, resolve the revision, gather candidates
  (`receipt.List` and `ReadNote`), build the trusted set, call `Evaluate`, compute the working
  tree report (`WorktreeMatches`, `CompareOutputs`), print text or JSON, return the exit class
  of contracts/cli.md
- [X] T034 [US2] Add `cmd/rune/verify.go` (`verify [flags] REVISION TASK...` with `-f`,
  `--key` repeatable, `--json`; help text with the exit statuses) and register it in
  `cmd/rune/main.go`; extend `cmd/rune` tests for flag wiring
- [X] T035 [US2] Extend `test/integration/receipts_test.go`: the tamper fixtures of SC-001
  (a payload field value changed and re-encoded, a signature byte, Runefile changed in a new
  commit, an import changed in a new
  commit, an input file changed in a new commit, another revision) each exit 1 with the named
  reason; an output file changed after the run shows `changed`; the dirty, untracked and
  changed-during-run fixtures of SC-002 and the `failed`, `interrupted`, `refused`, `cached`,
  `incomplete` fixtures of SC-003 never verify (the `incomplete` one, which the binary cannot be
  made to produce on demand, is signed by the test with the local key under its
  `XDG_STATE_HOME`); a run on a dirty tree committed afterwards verifies "on a snapshot
  identical to"

**Checkpoint**: receipts are evidence; US1 and US2 together are the issue's core.

---

## Phase 5: User Story 3 - Signing keys a task cannot use (Priority: P1)

**Goal**: key setup and trust: `rune receipt key`, the CI key, the hidden key directory,
`--key` trust.

**Independent Test**: run a task that prints its environment and lists the key directory;
verify with and without the signer's public key (spec US3).

### Tests for User Story 3

- [X] T036 [P] [US3] Write failing tests in `internal/cli/receipts_key_test.go`: a body
  running `env` with `Options.ReceiptKey` set prints no `RUNE_RECEIPT_KEY` and its receipt's
  key ID is the CI key's; `ReceiptKey` prints the public PEM on standard output and
  `key SHA256:... (RUNE_RECEIPT_KEY)` or `key SHA256:... (local: PATH)` on standard error,
  creates the local key when needed with the notice, and exits 2 for an unusable key; a
  receipt signed by another key is `unknown-signer` without `--key` and verified with it; with
  a task named `receipt` the `rune receipt` note is printed
- [X] T037 [P] [US3] Write a failing test in `internal/cli/sandbox_test.go`: `hidden()` lists
  `STATE/receipts` when it exists, beside the audit directory

### Implementation for User Story 3

- [X] T038 [US3] Implement `ReceiptKey(opts Options) error` and `ReceiptTaskNote(opts Options)`
  in `internal/cli/verify.go`; add `cmd/rune/receipt.go` with `receipt` (help, unknown
  subcommand usage error naming `rune -- receipt`) and `receipt key`, registered in
  `cmd/rune/main.go`
- [X] T039 [US3] Add the receipts key directory to the candidates of `hidden()` in
  `internal/cli/sandbox.go` (research R10)
- [X] T040 [US3] Add `test/integration/receipts_sandbox_unix_test.go` with
  `TestSandboxReceiptKeyHidden` (runs in the `test-sandbox` service): an agent's call to a task
  that lists the key directory sees it empty (SC-005)

**Checkpoint**: keys are set up and protected as the trust model says.

---

## Phase 6: User Story 4 - Receipts travel with the commit in Git notes (Priority: P2)

**Goal**: `rune receipt attach` and a working notes round trip.

**Independent Test**: attach, push to a bare remote, clone, fetch, verify; replay a note onto
another commit (spec US4).

### Tests for User Story 4

- [X] T041 [P] [US4] Extend `internal/receipt/notes_test.go`: `Attach(ctx, repo, commit,
  lines)` creates the note with the lines sorted, unique and newline-terminated, merges with an
  existing note, returns added and present counts, leaves `refs/notes/commits` and its note
  byte-identical and the commit ID unchanged; a note replaced between write and read-back is
  merged again (at most three tries); a missing committer identity gives an error that says to
  set `user.name` and `user.email`
- [X] T042 [P] [US4] Write failing tests in `internal/cli/receipt_attach_test.go`: only
  receipts with a trusted signature and `before == after == REVISION^{tree}` are attached (any
  status); output `attached N receipts to SHORT (M already there) in refs/notes/rune-receipts`;
  `no receipt for SHORT to attach` and exit 1 when none is bound; `--key` widens the trusted
  set

### Implementation for User Story 4

- [X] T043 [US4] Implement `Attach` in `internal/receipt/notes.go` (research R13)
- [X] T044 [US4] Implement `ReceiptAttach(opts Options, flags AttachFlags, args []string) error`
  in `internal/cli/verify.go` and the `receipt attach [REVISION]` command in
  `cmd/rune/receipt.go`
- [X] T045 [US4] Extend `test/integration/receipts_test.go`: attach, push to a bare remote,
  clone, fetch `refs/notes/rune-receipts`, verify with `--key` from the note alone; the
  unrelated note on the commit is byte-identical; a note copied onto a commit with another
  tree gives `different-snapshot` (SC-006)

**Checkpoint**: evidence travels with commits.

---

## Phase 7: User Story 5 - Agent and CI runs say where they came from (Priority: P2)

**Goal**: receipts from MCP calls, the agent executor and `rune ci run`, with channel, call ID
and claims.

**Independent Test**: run a task through each path and compare channel and claims (spec US5).

### Tests for User Story 5

- [X] T046 [P] [US5] Write failing tests in `internal/cli/mcp_receipts_test.go`: a tool call
  to a task with receipts on writes a receipt whose channel is `mcp`, whose invocation is the
  call's audit ID, and whose `claims.client` and `claims.auth` match the session; a dependency
  reached by the call shares the invocation; `CI=true` and `GITHUB_RUN_ID` on the command line
  keep channel `cli` and appear only in `claims.env`; `rune ci run` writes channel `ci-run`; a
  `rune` with `RUNE_AGENT_RUN=1` and `RUNE_INVOCATION_ID` writes channel `agent` and that
  `parentInvocation`; a call refused because the enforced sandbox cannot start writes a
  `refused` receipt with no exit code (FR-004); verify's text names the signer and shows
  channel and claims as recorded

### Implementation for User Story 5

- [X] T047 [US5] In `internal/cli/mcp.go`, build one recorder per adapter load when its
  settings have receipts on (failing the load with the signer error), and give each per-call
  engine a recorder view with channel `mcp`, invocation `rec.ID()` (a new ID when empty) and
  claims from `rec.Session()`; the loaded Runefile paths come from the adapter's load; the
  agent executor's endpoint adapter in `internal/cli/agentexec.go` gets the same
- [X] T048 [US5] Extend `test/integration/receipts_test.go`: `rune ci run test` with the real
  binary writes a receipt with channel `ci-run` and no dependency receipts (the job runs one
  body)

**Checkpoint**: every surface records with its channel.

---

## Phase 8: User Story 6 - Secrets never leave in a receipt (Priority: P2)

**Goal**: no secret in receipts, notes or verification output.

**Independent Test**: secret fixture in an argument, a dotenv file and the environment; search
everything (spec US6).

### Tests for User Story 6

- [X] T049 [P] [US6] Extend `test/integration/receipts_test.go`: with `TOKEN` set to a fixture
  value in the environment, in a dotenv file, and passed as an argument, the fixture appears
  0 times in every file under `.rune/receipts/`, in `git notes --ref=rune-receipts show`, and
  in the text and `--json` output of verify and attach; the receipt has
  `argumentsRedacted: true` (SC-004)

### Implementation for User Story 6

- [X] T050 [US6] Close any gap T049 finds in `internal/cli/receipts.go`: argument values,
  output paths, uncovered input paths, Runefile paths and claim values all go through the mask
  before marshalling (research R16); add a unit test for each gap to
  `internal/cli/receipts_test.go`

**Checkpoint**: receipts are safe to push.

---

## Phase 9: Polish & Cross-Cutting Concerns

- [X] T051 [P] Write `docs/how-to/receipts.md`: turning receipts on; what a receipt records;
  what it proves and does not (the trust model and its limits: same-user access to the local
  key, agents with unrestricted shell access and rune-only mode, the CI key visible to the
  job's processes, a `rune` started by a task body signs with the local key, a task can
  delete receipts inside the project (which only removes evidence), workflows generated by
  `rune ci github` pass no secret so their receipts use a local key, ignored files, the
  environment, changes made and undone within a run, submodules, `.rune` excluded); local
  key setup and `rune receipt key`; CI setup with `openssl genpkey -algorithm ed25519`, a
  GitHub Actions secret in `RUNE_RECEIPT_KEY`, trusting the public key with `--key`; verifying,
  the reason list, JSON and exit codes; a project policy task running `rune verify HEAD lint
  test`; attaching, pushing, fetching and merging notes with `cat_sort_uniq`; the receipt
  format (FR-044)
- [X] T052 [P] Update `docs/cli.md` (the `verify` and `receipt` commands, `RUNE_RECEIPT_KEY` in
  Environment, exit codes, the breaking command names beside `ci`), `docs/GRAMMAR.md` (the
  `receipts` setting), `docs/how-to/settings-and-dotenv.md` (the setting),
  `docs/how-to/README.md` and `docs/nav.yaml` (the new page), and add the page to
  `selfContainedPages` in `test/docs/codeblocks_test.go` when its rune blocks are complete
  Runefiles
- [X] T053 Regenerate every golden that lists built-in commands (help and completion) with
  `-update` and review the diff
- [X] T054 Run `golangci-lint run`, the full test suite and `-race` in Docker, the sandbox
  tests in `test-sandbox`, and the docs harness; check coverage of `internal/receipt` and the
  new code in `internal/cli` is at least 90%
- [X] T055 Run the quickstart.md scenarios with a built binary and record the results

---

## Dependencies & Execution Order

- Phase 1 -> Phase 2 -> US1. US1 is the MVP and blocks every later story (they read or extend
  what the recorder writes).
- US2 needs US1. US3 needs US1 (key use) and US2 (`--key` trust in verify). US4 needs US2
  (trusted-set and candidate code). US5 needs US1 and US2. US6 needs US1, US2 and US4.
- Polish needs all stories.

Within a phase: tests first (they fail), then implementation in the listed order.

## Parallel Opportunities

- Phase 2: T002, T004, T006, T008, T010, T012, T014, T016, T018, T020 are in different files
  and can be written together; each implementation task follows its own test.
- US1: T021 and T022 in parallel; T023 after them.
- US2: T028, T029, T030 in parallel.
- US3: T036 and T037 in parallel.
- US4: T041 and T042 in parallel.
- Polish: T051 and T052 in parallel.

## Implementation Strategy

1. MVP: Phases 1 to 3. Receipts are written and signed for command-line runs.
2. Add US2 so receipts become evidence for a revision.
3. Add US3 before relying on receipts: the CI key and hidden key directory.
4. Add US4 (notes), US5 (MCP, agent and CI channels), US6 (secret fixtures).
5. Polish: docs, goldens, lint, race, coverage, quickstart.
