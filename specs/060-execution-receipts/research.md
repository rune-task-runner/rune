# Research: Signed Execution Receipts

Decisions for spec 060. Each states what was chosen, why, and what was rejected. The scope
decisions (verification first, `set receipts`, a CI key from a secret) were made with the
maintainer before the spec was written.

## R1. Where receipts are recorded

**Decision**: In the CLI `engine`, by wrapping `Execute` and `ExecuteFailHook`. A small
`recorder` value hangs off the engine when `settings.Receipts` is set and the plan mode is
`planRun`. `Execute` asks the recorder to `begin` a receipt (allocating its ID and start time),
runs the existing body of `Execute`, and asks the recorder to `finish` it with the returned error.
The two places where `Execute` calls `runBody` (cache miss and plain run) first call
`rec.bodyStarting()`, which takes the snapshot before the body and marks that the body started.
A cache hit calls `rec.cachedHit(receiptID)` instead.

**Rationale**: Every surface reaches a body through `engine.Execute` or `engine.ExecuteFailHook`:
the command line (`scheduler.Run`), `rune ci run` (`scheduler.RunOnly`), MCP tool calls (an engine
per call in `mcpAdapter.call`), dependencies, post-hooks and failure hooks (scheduler), and the
agent executor's endpoint (an MCP adapter in the same process). One boundary gives one model
(issue: "Record at the shared engine boundary").

**Alternatives considered**: Recording in the scheduler (it does not know about caches, refusals
or exit-code classification, and the scheduler stays free of I/O). Recording in each command
(four copies that drift). Adding relationship callbacks to `scheduler.Engine` (an interface change
for every engine and test double, for information the engine can derive itself, see R6).

## R2. Status and exit code

**Decision**: `finish` derives the status from the error `Execute` returns, the run context, and
two flags the recorder holds (body started, cache hit):

1. `err == nil` and cache hit: `cached`, no exit code.
2. `err == nil`: `passed`, exit code 0.
3. The run context is cancelled: `interrupted`; exit code 130 when the body started.
4. `errors.Is(err, scheduler.ErrBodyNotRun)` or the body never started: `refused`, no exit code.
5. Otherwise: `failed`, exit code `e.ExitCode(err)`.

When a snapshot failed, a `passed` or `failed` status becomes `incomplete` and keeps its exit code.

**Rationale**: `e.ExitCode` is `CodeFor(classify(err))`, the function that sets Rune's own exit
code and the failure hooks' `RUNE_FAILED_EXIT_CODE` (spec 028). Reusing it keeps the receipt and
the caller in agreement (FR-005). Every pre-body refusal (`policyGate`, `applyGate`, a declined or
impossible `[confirm]`, a sandbox that did not start) is already wrapped in `bodyNotRunError`
or happens before `bodyStarting`.

**Alternatives considered**: Parsing banners or the audit outcome (indirect and lossy).

## R3. The snapshot

**Decision**: A snapshot is the git tree ID of the working tree as `git add -A` would stage it,
computed with a private index:

1. Once per recorder: `git rev-parse --show-toplevel --absolute-git-dir` gives the top and the
   git directory (a linked worktree has its own).
2. Copy `GITDIR/index` to a temporary file (an empty index when there is none).
3. `GIT_INDEX_FILE=TMP git -C TOP add -A -- :/ ':(top,exclude)REL/.rune'`, where REL is the
   Runefile directory relative to TOP.
4. `GIT_INDEX_FILE=TMP git -C TOP write-tree` prints the tree ID.
5. `git rev-parse -q --verify HEAD^{commit}` and `HEAD^{tree}` give the commit and whether the
   snapshot differs from it (`dirty`).

Every call uses `-c core.fsmonitor=false`, `GIT_OPTIONAL_LOCKS=0`, a context derived from the run
with a timeout, and captures standard error. The temporary index is removed afterwards.

**Rationale**: Git's own staging rules decide what is covered: ignore files, `info/exclude`,
`core.excludesFile`, file modes, symbolic links, line-ending and clean filters, submodules as the
commit they point to. The result is directly comparable with `REV^{tree}` (FR-030), so a revision
is checked without a checkout, and a run on a dirty tree can be bound to a later commit only when
that commit holds exactly the same files (issue: "rebound only by proving the executed snapshot
matches"). Copying the index keeps git's stat cache, so only changed files are hashed. Nothing
touches the real index, HEAD, refs or the working tree (FR-010); `git add` and `write-tree` do
store unreachable blobs and trees, as `git stash create` does (spec Assumptions).

Rune's own `.rune` directory of the project is excluded: receipts and cache records are written
there during a run and would otherwise change the snapshot of the next task. The receipt directory
also gets a `.gitignore` holding `*`, as the plan store does (spec 051).

**Alternatives considered**:
- Hashing files in Go and building tree objects by hand: misses filters, gitattributes and
  `core.fileMode`, and reimplements ignore rules.
- `git stash create`: ignores untracked files, and with `-u` it is not available as a pure
  create.
- `git status --porcelain` plus HEAD: says whether the tree is dirty, not what it held, so a dirty
  run could never be bound to a later commit, and two different dirty states look the same.
- An isolated copy of the project per task: breaks tasks that rely on the real tree and costs a
  full copy. Out of scope by the spec.

## R4. Mutation during the run

**Decision**: The snapshot is taken right before the body starts and right after it ends. A
receipt is evidence for a revision only when both equal the revision's tree. `[parallel]` tasks
that write non-ignored files see each other's writes and are reported as changed during the run.

**Rationale**: A persistent change during the run, by the task or by anything else, is caught. A
change made and undone within the run is not, and the documentation says so (spec Out of scope).
Taking the "before" snapshot right before the body, not when `Execute` starts, keeps a slow
`[confirm]` prompt from turning into a false "changed during the run".

## R5. Coverage beyond the tree

**Decision**:
- Runefile and imports: the recorder hashes every file the load read (`pathRecorder.found()` on
  the command line, the adapter's loaded paths on the MCP path) once per invocation. Each entry
  holds the path relative to the repository top, its SHA-256 and `covered`: false when the file
  lies outside the repository or git ignores it (`git check-ignore`).
- Declared `[cache]` inputs: the files `cache.Decide` matched are passed to `git check-ignore
  --stdin -z`; the ignored ones are listed as `uncoveredInputs`.
- Declared `[cache]` outputs of a passed body: `cache.OutputFiles` lists them; each regular file
  gets path, size and SHA-256. Above 64 files the list goes to `ID.outputs.json` next to the
  receipt, and the receipt keeps its SHA-256 and count (issue: "keep large manifests separate").
- Task definition: SHA-256 of `ast.Dump` of the task alone plus the executor identity string used
  by the cache (`sel.Display` and its command).

**Rationale**: The tree already binds every non-ignored file, Runefile and imports included. The
extra lists exist to say what the tree cannot cover, so verification can refuse instead of
over-claiming (FR-030).

**Alternatives considered**: Recording every input file digest (the tree is already a Merkle
root of them). Digests of resolved variables (they may hold secrets, and a SHA-256 of a short
secret can be guessed offline; FR-015).

## R6. Relationships between receipts

**Decision**: The recorder of an invocation keeps three maps under a mutex:
- `byKey`: scheduler-style key (`name(args.Key())`) to the receipt ID of the last finished run.
- `follows`: key of a post-hook target to the receipt ID of the task it follows, filled when a
  task passes, by resolving its `PostHooks` with `ResolveDep`.
- `failed`: task name to the receipt ID of its last failed body, read by `ExecuteFailHook`, whose
  `scheduler.Failure` carries the name.

`begin` resolves the task's `Deps` with `ResolveDep` and records the IDs found in `byKey`. A cache
hit records the receipt ID stored in the cache record (R8) as `reuses`.

**Rationale**: No scheduler change (R1). Dependency arguments are evaluated a second time; the
same values come back except for non-deterministic built-ins (`uuid()`, `datetime()`), where a
reference is then missing, never wrong.

## R7. Caller channel and claims

**Decision**: The channel comes from the path: `mcp` for an engine built by the MCP adapter;
`ci-run` when `Options.nodeRun` is set; `agent` when `RUNE_AGENT_RUN=1` marks a `rune` started
by a body of an agent's call (the existing `agentRunVar`); otherwise `cli`. Claims: on MCP, the
client name and version and the session's auth mode (`process` or `bearer-token`) from the
call's audit record, through a new nil-safe `CallAudit.Session()` accessor in `mcpserver`; on
every path, the values of `CI`, `GITHUB_ACTIONS`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`,
`GITHUB_WORKFLOW` and `GITHUB_REPOSITORY` when set, masked.

The invocation ID is the MCP call's audit ID on the MCP path and a new 16-hex ID otherwise; the
call ID that started a `rune` from an agent's body (`invocationVar`) is the parent invocation.

**Rationale**: The issue: "Caller classification must come from the invocation path ... A Git
username, environment variable or MCP client name alone is not proof." `rune serve` always sets
an auditor, so the call record and its session exist on every real MCP call.

## R8. Cache records name their receipt

**Decision**: `cache.Fingerprint` gains `Receipt string \`json:"receipt,omitempty"\``,
`cache.Decision` gains `Receipt`, and `cache.Store` takes the receipt ID (empty when receipts are
off). Old records without the field read as before.

**Rationale**: A cache hit must reference its source evidence (issue) and never pose as a run.

## R9. Envelope and signature

**Decision**: A receipt file is one line of JSON, a DSSE envelope:

```json
{"payloadType":"application/vnd.in-toto+json","payload":"<base64>","signatures":[{"keyid":"SHA256:...","sig":"<base64>"}]}
```

The payload is an in-toto Statement v1 (`_type` `https://in-toto.io/Statement/v1`) whose subject
is `{"name":"worktree","digest":{"gitTree":"<before>"}}` (or, outside a repository,
`{"name":"Runefile","digest":{"sha256":"..."}}`), whose `predicateType` is
`https://rune-task-runner.github.io/rune/receipt/v1`, and whose predicate is the receipt
(data-model.md). The signature is Ed25519 over DSSE's pre-authentication encoding
`DSSEv1 SP len(type) SP type SP len(body) SP body`. Implemented with the standard library
(`crypto/ed25519`, `encoding/base64`, `encoding/json`); about forty lines.

**Rationale**: DSSE with an in-toto statement is the envelope Sigstore and SLSA use for
attestations (issue: "reuse cosign/Sigstore ... formats where practical"). DSSE signs the exact
payload bytes, so no JSON canonicalization is needed: verification checks the bytes, then
parses. Ed25519 keys are small, fast and deterministic.

**Alternatives considered**: `sigstore-go` (already a dependency for self-upgrade) for keyed
signing: heavier API for the same envelope, and keyless signing is out of scope. A bespoke
signed JSON with canonicalization: more code, less interoperable. `go-securesystemslib/dsse`
(indirect dependency): would become direct for forty lines of code.

## R10. Keys

**Decision**:
- Format: PKCS#8 PEM (`PRIVATE KEY`) for private keys, PKIX PEM (`PUBLIC KEY`) for public keys,
  as `openssl genpkey -algorithm ed25519` and `openssl pkey -pubout` write them.
- Key ID: `SHA256:` plus unpadded standard base64 of the SHA-256 of the PKIX DER (FR-025).
- CI key: `RUNE_RECEIPT_KEY` holds the PEM text. `cmd/rune` reads it into `Options.ReceiptKey`
  and calls `os.Unsetenv` before anything else runs, so no `os.Environ()` in the process
  carries it to a task, a hook, an agent executor or a sandbox (FR-022).
- Local key: `STATE/receipts/key.pem`, where STATE is `audit.StateDir` (`$XDG_STATE_HOME/rune`,
  `%LocalAppData%\rune`, or `~/.local/state/rune`). The directory is made `0700`, the file is
  written `0600` to a temporary name and linked into place, so a concurrent first use either
  wins or reads the winner's complete file. Creation prints one notice with the path and key ID.
- The sandbox hides `STATE/receipts` from confined bodies, beside the audit directory it already
  hides (spec 053, FR-023).
- An invalid `RUNE_RECEIPT_KEY`, or a local key that cannot be read or created, stops the run
  before any body with a usage error (FR-021).

**Rationale**: No interactive login, no network, works offline (issue). Unsetting the variable at
the start is one point that covers every way a child gets an environment.

**Limits, documented**: A task runs as the same user, so an unconfined body can read the local
key file and a CI body can read its parent's initial environment through `/proc` on Linux. A
`rune` started by a task body gets no CI key and signs with the local key, which CI does not
trust: the tasks that need CI evidence are run directly by the workflow step.

## R11. Storage

**Decision**: `ROOT/.rune/receipts/ID.json`, where ROOT is the Runefile's directory. ID is
`YYYYMMDDTHHMMSSZ-` plus 16 hex digits from `crypto/rand` (64 bits). Write: temporary file in the
same directory, `fsync`, `os.Link` to the final name (fails if it exists, so nothing is ever
replaced), remove the temporary file. The directory is created `0755` with a `.gitignore` of `*`.

**Rationale**: Atomic, collision-safe (FR-026), sortable by name, portable (`os.Link` works on
NTFS, APFS and Linux file systems).

## R12. Verification

**Decision**: `rune verify REVISION TASK... [--key FILE]... [--json]`:

1. `git rev-parse --verify -q REVISION^{commit}` and `^{tree}`: unknown revision is a usage error.
2. Candidates: every `*.json` in `.rune/receipts/` (not `*.outputs.json`) and every line of the
   note of the commit under `refs/notes/rune-receipts` (found with `git notes --ref=... list`,
   read with `git cat-file blob`). Identical envelopes count once.
3. Each candidate is decoded. A file that is not an envelope, or whose payload is not a statement
   with a receipt, is `malformed` and reported apart, since its task is unknown. Otherwise the
   unverified task name attributes it to a task; then the signature is checked: the envelope's
   `keyid` selects the trusted key with that ID; none gives `unknown-signer`, a failed check
   gives `bad-signature`; then the version (`unsupported-version`). An envelope whose payload
   is not a receipt is reported as a problem with the signature reason when it is not signed by
   a trusted key, and `malformed` otherwise.
4. A signed candidate for TASK is accepted when status is `passed`, `before == after == tree`, no
   Runefile entry is uncovered and `uncoveredInputs` is empty. Otherwise its reason is the first
   failing check in this order: status (`failed`, `interrupted`, `refused`, `cached`,
   `incomplete`), `no-snapshot`, `changed-during-run`, `different-snapshot`,
   `uncovered-runefile`, `uncovered-inputs`.
5. Per task: the newest accepted receipt (by `finishedAt`, then ID); else the reason of the newest
   candidate whose snapshot matches, else of the newest candidate for the task, else
   `no-receipt`.
6. Working tree: `git diff --quiet REVISION --` and `git ls-files --others --exclude-standard -z`
   (both read-only, with `GIT_OPTIONAL_LOCKS=0`) say whether it matches. For an accepted receipt
   with outputs, each current file is compared: `match`, `changed`, `missing`, or `unknown` when
   a manifest is not available locally.
7. Exit 0 when every task is verified, 1 otherwise, 2 for a usage error.

Trusted keys: the key Rune would sign with (`RUNE_RECEIPT_KEY`, else the local key if its file
exists; never created) plus each `--key` file (one or more `PUBLIC KEY` blocks).

**Rationale**: Read-only by construction (FR-034). The spec's reason list maps one to one.

**Alternatives considered**: Recomputing the snapshot of the working tree in verify (writes
objects; the diff and untracked listing answer the only question asked). Matching tasks by
arguments (out of scope; the arguments are reported).

## R13. Git notes

**Decision**: `rune receipt attach [REVISION] [--key FILE]...` selects local receipts with a
trusted signature and `before == after == REVISION^{tree}` (any status: a failed run is evidence
too), reads the existing note, takes the sorted union of lines, and writes it with
`git notes --ref=refs/notes/rune-receipts add -f -F TMP COMMIT`. It then reads the note back;
when a concurrent writer replaced it, the merge repeats, at most three times. It reports the
number added and the number already present, and exits 1 when nothing is bound to the revision.

One envelope per line, sorted and unique, means `git notes merge -s cat_sort_uniq` combines notes
written on two machines without loss. The documentation gives:

```sh
git push origin refs/notes/rune-receipts
git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts
git fetch origin refs/notes/rune-receipts:refs/notes/origin/rune-receipts
git notes --ref=rune-receipts merge -s cat_sort_uniq refs/notes/origin/rune-receipts
```

`git notes add` makes a commit on the notes ref and needs a committer identity; when git refuses
for lack of one, the error says to set `user.name` and `user.email`.

**Rationale**: Notes never change the annotated commit and live in a ref no other tool uses. A
note moved to another commit binds nothing there, because verification compares the receipt's
own signed snapshot with that commit's tree (FR-030).

## R14. Command names

**Decision**: `verify` (as the issue shows it) and `receipt` (key and attach) become built-in
commands. Each prints `note: "NAME" is also a task of this Runefile; run it with: rune -- NAME`
when the Runefile has such a task, as `ci` does. `taskimport.RuneCommands` gains both.

**Rationale**: Same handling as `agent`, `export`, `suggest` and `ci`. The commit is marked
breaking.

**Alternatives considered**: Only `rune receipt verify` (one reserved name, but the issue's
primary verb becomes two words).

## R15. The setting

**Decision**: `set receipts` is a boolean setting like `set export`: `config.Settings.Receipts`,
a `case "receipts"` in `ResolveSettings`, an entry in `language.builtinSettings` (completion,
hover, analyzer validity), and a `GRAMMAR.md` row.

**Rationale**: Project-level opt-in that applies to every caller (maintainer's choice). Principle
III allows growing declarative surface that is total and statically analyzable.

## R16. Secrets

**Decision**: Effective arguments, output paths, uncovered input paths, Runefile paths and claim
values pass through the run's masker (`maskerFor(set)` on the CLI, `a.maskText` on MCP) before
the predicate is marshalled. When any argument changed under masking, `argumentsRedacted` is
true. No environment value, variable value, task output or error text is stored, and no digest
of one. Verification prints receipt fields, which were masked when written, and never reads the
environment except `RUNE_RECEIPT_KEY`, whose value it never prints.

**Rationale**: Notes are pushed to shared remotes (FR-042, FR-043). A redacted argument makes the
receipt's arguments non-identifying; that is stated, not hidden (issue: "do not replace
secret-bearing inputs with a common redacted value and then claim matching fingerprints").

## R17. Testing

**Decision**:
- `internal/receipt`: DSSE sign and open (tamper each part), key parsing and IDs, local key
  creation under a race, ID format, atomic no-replace writes, listing, snapshots in temporary
  repositories (clean, dirty, untracked, ignored, `.rune` excluded, index and HEAD untouched,
  linked worktree, no commit, not a repository), notes read, attach merge and unrelated notes,
  evaluation reasons table-driven.
- `internal/cache`: the receipt field round-trips; old records still read.
- `internal/config`, `internal/language`, `internal/analyzer`: the setting.
- `internal/cli`: recorder statuses and relationships through engine fixtures; channels on each
  path; `RUNE_RECEIPT_KEY` absent from a body's environment; the sandbox's hidden list; the
  `verify`, `receipt key` and `receipt attach` commands, their output, JSON and exit codes.
- `mcpserver`: `CallAudit.Session()`.
- `test/integration/receipts_test.go`: the real binary end to end for SC-001 to SC-008, with a
  bare remote for the notes round trip and a secret fixture.
- Docs harness: the new how-to page's examples validate.

All tests run in the Docker test container, which has git (Debian bookworm).
