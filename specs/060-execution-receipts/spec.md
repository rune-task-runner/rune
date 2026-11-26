# Feature Specification: Signed Execution Receipts, `rune verify` and Git Notes

**Feature Branch**: `060-execution-receipts`

**Created**: 2026-11-25

**Status**: Draft

**Input**: GitHub issue [#56](https://github.com/rune-task-runner/rune/issues/56): "Receipts:
signed proof of task execution with rune verify and Git notes". Related: action audit log
(#50, spec 052), approved plan artifacts (#49, spec 051), sandboxing (#51, spec 053), accurate
exit codes (#33, spec 028), CI workflows (#62, spec 059).

## Overview

An agent can say "all tests pass" without running them. A reviewer needs evidence that Rune's
own execution path produced, not a sentence in a chat. This feature makes Rune write a small
signed receipt for every task it runs, and lets anyone check those receipts against a git
revision without running anything.

```text
$ rune test
running: test
ok  	example/pkg	0.412s

$ rune verify HEAD test lint
test: passed at a1b2c3d (cli, signed by SHA256:Qm3x...)
lint: not verified: no receipt for a1b2c3d
working tree: matches a1b2c3d
$ echo $?
1
```

A receipt names what ran, on which files, with what result, started from where, and is signed
by a key the task cannot read. `rune verify` accepts a receipt as evidence for a revision only
when the files the task ran on are exactly that revision's files. Receipts travel with a commit
in a dedicated Git notes ref, so a reviewer or CI can check them without changing the commit.

| Question | Answer |
|---|---|
| Who turns it on? | The project, with `set receipts` in the Runefile. Without it nothing changes |
| What is covered? | Every file git does not ignore, tracked or not, before and after the body, plus the Runefile and its imports |
| Does a git SHA alone prove anything? | No. A receipt is evidence for a revision only when the snapshot the task ran on is that revision's tree |
| Who signs? | This machine's key, created on first use outside the project, or a CI key from a secret |
| Can a task read the key? | A CI key is removed from every task's environment; the local key directory is hidden from sandboxed agent tasks. Limits are documented |
| Is a failed, cancelled, refused or cached run evidence of a pass? | Never |
| Does verification run anything? | No. It reads receipts, notes and git objects, and writes nothing |
| Does one passing task mean all checks passed? | No. `rune verify` checks exactly the tasks it is given and reports each one |

### Out of scope

- **CI reuse**: skipping a CI task because trusted evidence exists. The issue asks for
  verification first; reuse is a separate, explicitly gated follow-up.
- **Keyless signing** (Sigstore OIDC certificates, transparency logs). CI signs with a key held
  in a CI secret; keyless signing is a follow-up.
- **Isolated snapshots**: running a task on a copy of the project. Rune detects mutation by
  comparing snapshots before and after the body; a change made and undone inside one run is not
  detected, and the documentation says so.
- **Coverage of ignored files and the environment**: files git ignores, environment variables,
  network resources and installed tools are not part of the snapshot. A receipt records when a
  declared input is ignored, and verification then refuses it.
- **Digests of standard output and standard error**. No task output is kept or digested.
- **Commands that fetch or push notes**. The documentation gives the git commands.
- **Pruning old receipts**. Deleting `.rune/receipts/` is always safe.
- **Selecting a receipt by task arguments** in `rune verify`. Verification matches the task name
  and reports the arguments of the receipt it accepted.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Every task run leaves a signed receipt (Priority: P1)

A maintainer adds `set receipts` to the Runefile and runs `rune test`. `test` depends on
`build`. Two receipts appear under `.rune/receipts/`, one per task, both signed, sharing one
invocation ID, and the receipt of `test` refers to the receipt of `build`. When `test` fails,
its receipt says failed with the exit code Rune exited with, and the receipt of its failure hook
refers to it.

**Why this priority**: Without receipts there is nothing to verify.

**Independent Test**: Run fixtures for each outcome in a temporary git repository and decode
each receipt: check its fields, its signature, and that the stored status and exit code match
what `rune` reported.

**Acceptance Scenarios**:

1. **Given** a Runefile with `set receipts` and a passing `test` that depends on `build`,
   **When** `rune test` runs, **Then** exactly two receipts are written, each with a valid
   signature, the same invocation ID, status `passed`, exit code 0, and the receipt of `test`
   names the receipt of `build` as a dependency.
2. **Given** a Runefile without `set receipts`, **When** any task runs, **Then** no receipt,
   no key and no snapshot is made, and the output is byte-identical to the output before this
   feature.
3. **Given** a body that exits 3, **When** it runs, **Then** its receipt has status `failed`
   and exit code 3, and the receipt of its `||` failure hook names it.
4. **Given** a `[cache]` task whose cache is current, **When** it is skipped, **Then** its
   receipt has status `cached` and names the receipt of the run that stored the cache record.
5. **Given** a body interrupted with Ctrl-C, **When** Rune exits 130, **Then** the receipt has
   status `interrupted`.
6. **Given** a `[confirm]` task that is declined, **When** the run stops, **Then** its receipt
   has status `refused` and records that the body did not run.
7. **Given** two `rune` processes run tasks at the same time in one project, **When** both
   finish, **Then** every receipt belongs to exactly one invocation and no file is overwritten.
8. **Given** a task with arguments, **When** it runs, **Then** its receipt records the task's
   canonical name and its effective arguments after defaults are bound.

---

### User Story 2 - Verify a revision without running anything (Priority: P1)

A reviewer checks out a pull request and runs `rune verify HEAD test lint`. Rune says which of
the two tasks has a valid passing receipt for exactly the files of `HEAD`, and why the others
do not. It exits non-zero unless every named task is verified. With `--json` a bot reads the
same answer.

**Why this priority**: This is what turns a receipt into evidence.

**Independent Test**: Record receipts in a fixture repository, then apply one change per
fixture (edit the payload, the signature, the Runefile, an input file; commit something else;
dirty the tree before the run; edit a file during the run) and check the verdict and reason.

**Acceptance Scenarios**:

1. **Given** a passing `test` receipt recorded on a clean checkout of commit C, **When**
   `rune verify C test` runs, **Then** it reports `test: passed at C` and exits 0.
2. **Given** the same receipt, **When** one byte of its payload or signature is changed,
   **Then** verification reports a bad signature and exits 1.
3. **Given** a receipt recorded at C, **When** `rune verify D test` runs for a commit D whose
   tree differs (another input file, a changed Runefile or import), **Then** it reports that the
   receipt ran on a different snapshot and exits 1.
4. **Given** a run on a dirty tree, **When** the changes are then committed as C2 with exactly
   the files the task ran on, **Then** `rune verify C2 test` accepts it and says it ran on a
   snapshot identical to C2, not at C2.
5. **Given** a run on a dirty tree or with an untracked file, **When** `rune verify HEAD test`
   runs against the clean commit, **Then** it is not verified.
6. **Given** a file that changes while the body runs, **When** the run finishes, **Then** the
   receipt says the files changed during the run and it never verifies.
7. **Given** a `failed`, `interrupted`, `refused`, `cached` or `incomplete` receipt, **When**
   it is the only receipt for the task, **Then** verification reports that status as the reason
   and exits 1.
8. **Given** no task names, **When** `rune verify HEAD` runs, **Then** it is a usage error that
   asks for the tasks that need evidence.
9. **Given** the working tree differs from the verified revision, **When** verification runs,
   **Then** it reports that the working tree differs, and never says the inputs are unchanged.
10. **Given** `--json`, **When** verification runs, **Then** standard output holds one JSON
    document with a verdict, reason and accepted receipt per task, and the working tree status.

---

### User Story 3 - Signing keys a task cannot use (Priority: P1)

The first run with receipts on creates a signing key in the user's state directory and says
where. `rune receipt key` prints its public key for teammates and CI to trust. In CI, the
workflow passes a private key through a secret in `RUNE_RECEIPT_KEY`; Rune signs with it and
hands it to no task. A receipt signed by a key nobody trusts is refused.

**Why this priority**: A signature from a key the task can use proves nothing.

**Independent Test**: Run a task that prints its environment and lists the key directory, with
and without the sandbox, and verify receipts with and without the signer's public key.

**Acceptance Scenarios**:

1. **Given** no key exists, **When** the first task runs with receipts on, **Then** a key is
   created readable only by the user, outside the project, and a one-line notice names its path
   and key ID.
2. **Given** a key exists, **When** `rune receipt key` runs, **Then** it prints the public key in
   PEM on standard output and the key ID and source on standard error.
3. **Given** `RUNE_RECEIPT_KEY` holds a valid private key, **When** a task runs, **Then** the
   receipt is signed with it, and the task's environment does not contain `RUNE_RECEIPT_KEY`.
4. **Given** `RUNE_RECEIPT_KEY` holds something that is not a valid key, **When** a run starts,
   **Then** no task runs, and the error names the variable and the problem.
5. **Given** a receipt signed by a key that is neither this machine's key nor given with
   `--key`, **When** it is verified, **Then** it is reported as signed by an unknown key and is
   not verified.
6. **Given** `--key ci.pub`, **When** a receipt signed by that key is verified, **Then** it is
   accepted and the report names that key's ID.
7. **Given** an agent calls a task through `rune serve` with the sandbox enforced, **When** the
   task lists the key directory, **Then** it sees it empty.

---

### User Story 4 - Receipts travel with the commit in Git notes (Priority: P2)

After the run, the maintainer runs `rune receipt attach HEAD` and pushes the notes ref. CI or a
reviewer fetches it and runs `rune verify` against the same commit. The commit is unchanged, and
notes other tools keep are untouched. Moving a note to another commit makes nothing valid there.

**Why this priority**: Local receipts help the person who ran them; notes let others check.

**Independent Test**: Attach, push to a bare remote, clone, fetch the notes ref, verify; then
copy the note to another commit and verify that commit.

**Acceptance Scenarios**:

1. **Given** local receipts bound to HEAD's tree, **When** `rune receipt attach HEAD` runs,
   **Then** they are added to the note of HEAD under `refs/notes/rune-receipts`, and the report
   names how many were added and how many were already there.
2. **Given** a note in `refs/notes/commits` on the same commit, **When** attach runs, **Then**
   that note is byte-identical afterwards, and the commit ID is unchanged.
3. **Given** attach runs twice, **When** the note is read, **Then** each receipt appears once.
4. **Given** a clone that fetched `refs/notes/rune-receipts`, **When** `rune verify HEAD test
   --key signer.pub` runs, **Then** it verifies from the note alone, with no local receipts.
5. **Given** the note copied onto a commit with a different tree, **When** that commit is
   verified, **Then** the receipt is reported as a different snapshot and is not verified.
6. **Given** no local receipt is bound to the revision, **When** attach runs, **Then** it
   attaches nothing, says so and exits 1.

---

### User Story 5 - Agent and CI runs say where they came from (Priority: P2)

A receipt says which path started it: the command line, `rune ci run`, an agent's MCP call, or a
`rune` started by the body of an agent's call. For an MCP call it also records what the client
said about itself and the call's ID in the audit log. None of that is presented as proof of who
the caller is: only the signing key is authenticated.

**Why this priority**: A reviewer must see whether an agent or a person ran the task, without
mistaking a claim for an identity.

**Independent Test**: Run a task through each path and compare the channel and claims recorded.

**Acceptance Scenarios**:

1. **Given** an MCP tool call, **When** its task runs, **Then** the receipt's channel is `mcp`,
   its invocation ID is the call's audit ID, and the client name and version are recorded as
   claims.
2. **Given** `rune ci run test`, **When** it runs, **Then** the channel is `ci-run`.
3. **Given** `CI=true` and `GITHUB_RUN_ID` in the environment, **When** a task runs from the
   command line, **Then** the channel stays `cli` and those values are recorded only as claims.
4. **Given** any receipt, **When** `rune verify` reports it, **Then** the signer is shown as the
   authenticated identity and the channel and claims are shown as recorded, not authenticated.

---

### User Story 6 - Secrets never leave in a receipt (Priority: P2)

A task takes a token as an argument, and the run has secrets in its environment. Neither value
appears in any receipt, note or verification message.

**Why this priority**: Notes are pushed to a shared remote.

**Independent Test**: Run with a secret fixture in an argument, a dotenv file and the
environment; search every file under `.rune/receipts/`, the note, and the output of verify.

**Acceptance Scenarios**:

1. **Given** an argument that holds a masked secret, **When** the receipt is written, **Then**
   the argument is masked and the receipt says its arguments were redacted.
2. **Given** secrets in the environment and a dotenv file, **When** receipts are written,
   attached and verified, **Then** no secret value, and no digest of one, appears in any of them.

---

### Edge Cases

- **Not a git repository**: receipts are written without a snapshot; verification reports that
  the run had no snapshot.
- **No commit yet**: the snapshot is recorded; there is no commit to name; a later commit with
  that tree can be verified.
- **A task changes non-ignored files on purpose** (a formatter, a generator): its receipt
  records the tree before and after; it is evidence only for a revision where nothing changed.
- **Tasks under `[parallel]` that change non-ignored files**: each may see the other's change;
  those receipts say the files changed during the run.
- **A Runefile import outside the repository, or one git ignores**: recorded as not covered;
  verification refuses the receipt because the revision cannot cover it.
- **A declared `[cache]` input that git ignores**: recorded as uncovered; verification refuses.
- **A dependency skipped because it is not available on this OS**: no receipt, as no body ran.
- **`--dry-run`, `--summary`, `--list`**: no receipt.
- **`--watch`**: every re-run is an invocation with its own receipts.
- **A task named `verify` or `receipt`**: run with `rune -- verify`; the built-in says so.
- **Submodules**: recorded by the commit they point to; uncommitted changes inside a submodule
  are not covered, and the documentation says so.
- **The receipt directory cannot be written, or signing fails after a body ran**: a warning names
  the task and reason; the exit code stays the task's.
- **A corrupt or foreign file in `.rune/receipts/`**: verification reports it as malformed and
  ignores it for every task.
- **A receipt with an unknown format version**: reported as unsupported, never accepted.
- **More than one receipt for a task at the revision**: the newest valid one is accepted.
- **More than 64 declared output files**: their list moves to a manifest file next to the
  receipt, bound by its digest; a note carries only the digest.
- **A revision that does not exist**: usage error.

## Requirements *(mandatory)*

### Functional Requirements

**Turning receipts on**

- **FR-001**: A Runefile setting `set receipts` MUST turn recording on for runs of that
  Runefile. It MUST be a boolean setting, read as `set export` is.
- **FR-002**: Without the setting, Rune MUST NOT write a receipt, create a key, take a snapshot
  or change any output.

**Recording**

- **FR-003**: With receipts on, every task Rune is asked to run in run mode MUST produce exactly
  one receipt, whichever path asked: a root on the command line, `rune ci run`, an MCP tool call,
  a dependency, a post-hook, a failure hook, or a body run by the agent executor. `--dry-run`,
  `--summary` and `--list` MUST NOT produce receipts.
- **FR-004**: A receipt MUST record one status: `passed` (the body ran and succeeded), `failed`
  (the body ran and failed), `interrupted` (the run was cancelled), `refused` (the body did not
  run: a declined or impossible confirmation, an agent policy, a plan gate or a sandbox that
  could not start), `cached` (a cache hit skipped the body), or `incomplete` (the recorder could
  not observe the run in full, for example a snapshot failed).
- **FR-005**: The status and exit code MUST come from the same classification that sets Rune's
  exit code, so a receipt never disagrees with what the caller saw. A tool call the server
  refuses before any task starts (an unknown tool, invalid arguments, the agent policy checked
  before the call, a sandbox found unavailable before the call) writes no receipt: no task ran,
  and the audit log records the call.
- **FR-006**: A receipt MUST record: a unique receipt ID; the invocation ID shared by every
  receipt of one run; the task's canonical name (with its module prefix); its effective
  arguments; its role (`task`, `failure-hook`); the IDs of the receipts it relates to (direct
  dependencies that ran or were reused earlier in the invocation, the task a failure hook ran for,
  the task a post-hook follows, the run a cache hit reuses); start and finish times in UTC and a
  duration from a monotonic clock; the Rune version, commit, OS and architecture; the executor
  identity; and a digest of the task's definition (body text, attributes, executor).
- **FR-007**: On the MCP path the invocation ID MUST be the call's audit ID (spec 052). A `rune`
  started by the body of an agent's call MUST record that call's ID as its parent invocation.
- **FR-008**: A `cached` receipt MUST name the receipt of the run that stored the cache record
  when that run recorded one, and MUST NOT be presented as a run of the body.

**Snapshot and coverage**

- **FR-009**: Before the body runs and after it ends, the receipt MUST record the identity of the
  working tree snapshot: every file git does not ignore, tracked or untracked, identified as git
  identifies a tree object. It MUST also record HEAD's commit (when there is one) and whether the
  working tree differed from it.
- **FR-010**: Taking a snapshot MUST NOT change the repository's index, HEAD, refs, notes or
  working tree.
- **FR-011**: When the snapshot after the body differs from the one before, the receipt MUST say
  the files changed during the run.
- **FR-012**: The receipt MUST record the SHA-256 of every Runefile and import the load read, by
  path relative to the repository root, and mark a file outside the repository, or one git
  ignores, as not covered.
- **FR-013**: The receipt MUST list the declared `[cache]` inputs that git ignores, as uncovered.
- **FR-014**: For a task that declares `[cache]` outputs and passed, the receipt MUST record each
  output file's path, size and SHA-256. With more than 64 files, the list MUST be written to a
  manifest file next to the receipt and the receipt MUST hold its digest and file count.
- **FR-015**: A receipt MUST NOT hold task output, environment values, variable values, or a
  digest of any of them.
- **FR-016**: Outside a git repository the receipt MUST be written without a snapshot.

**Caller**

- **FR-017**: The receipt MUST record the channel from the path that started the run: `cli`,
  `ci-run`, `mcp`, or `agent` (a `rune` started by a body of an agent's call).
- **FR-018**: Claims MUST be recorded apart from the channel: for MCP, the client's reported name
  and version and the server's authentication mode; for every path, the values of a fixed list of
  CI variables (`CI`, `GITHUB_ACTIONS`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`, `GITHUB_WORKFLOW`,
  `GITHUB_REPOSITORY`) when set. Claims MUST NOT change the channel or be shown as authenticated.

**Signing and keys**

- **FR-019**: A receipt MUST be a DSSE envelope over an in-toto Statement v1 whose subject is the
  snapshot before the body (the Runefile's digest outside a repository) and whose predicate is
  the receipt, signed with Ed25519. The predicate
  MUST carry a format version.
- **FR-020**: When `RUNE_RECEIPT_KEY` is set, it MUST hold a PKCS#8 PEM Ed25519 private key, and
  Rune MUST sign with it. Otherwise Rune MUST sign with the local key under the user's state
  directory, creating it on first use readable only by the user, and print one notice with its
  path and key ID when it creates it.
- **FR-021**: When the signer cannot be prepared (an invalid `RUNE_RECEIPT_KEY`, a key that cannot
  be read or created), the run MUST stop before any body with a usage error that names the cause.
- **FR-022**: `RUNE_RECEIPT_KEY` MUST NOT reach any task body, hook, agent executor or sandboxed
  body.
- **FR-023**: The local key directory MUST be hidden from bodies the sandbox confines (spec 053).
- **FR-024**: Rune MUST NOT write an unsigned receipt. When signing or writing fails after a body
  ran, a warning MUST name the task and the cause, and the exit code MUST stay the task's.
- **FR-025**: The key ID MUST be `SHA256:` followed by the unpadded base64 of the SHA-256 of the
  public key's PKIX encoding.

**Storage**

- **FR-026**: Receipts MUST be written to `.rune/receipts/ID.json` next to the Runefile,
  atomically, never replacing an existing file. IDs MUST start with the UTC start time and end in
  at least 64 random bits.

**Verification: `rune verify`**

- **FR-027**: `rune verify REVISION TASK...` MUST report, for each named task, whether a valid
  passing receipt exists for that revision. Without a task it MUST be a usage error.
- **FR-028**: Candidates MUST be the receipts in `.rune/receipts/` and in the note of the
  revision's commit under `refs/notes/rune-receipts`, each counted once.
- **FR-029**: The trusted keys MUST be the key this machine would sign with (`RUNE_RECEIPT_KEY` if
  set, else the local key if it exists) and every public key given with `--key FILE`.
  Verification MUST NOT create a key.
- **FR-030**: A receipt MUST be accepted for TASK at REVISION only when: its signature verifies
  with a trusted key; its format version is supported; its task is TASK; its status is `passed`;
  its snapshots before and after are both the revision's tree; no Runefile or import is uncovered;
  and no declared input is uncovered.
- **FR-031**: Among accepted receipts the newest MUST be reported, with its arguments, channel,
  signer key ID and time, and whether it ran at the revision's commit on a clean tree or on an
  identical snapshot of another state.
- **FR-032**: When no receipt is accepted, the reason MUST be the one of the most relevant
  candidate (newest whose snapshot matches, else newest for the task), from a fixed list: no
  receipt, unknown signer, bad signature, unsupported version, failed, interrupted, refused,
  cached, incomplete, changed during the run, different snapshot, uncovered Runefile, uncovered
  inputs, no snapshot. A candidate that cannot be decoded names no task: it MUST be reported
  apart as malformed.
- **FR-033**: Verification MUST report whether the working tree matches the revision, and for
  each accepted receipt with outputs, whether the current output files match, changed or are
  missing. This report MUST NOT change the verdict.
- **FR-034**: Verification MUST NOT run a task and MUST NOT write any file, object, ref or note.
- **FR-035**: `--json` MUST print one JSON document holding, per task, the verdict, reason and
  accepted receipt summary, and the working tree report.
- **FR-036**: Exit status MUST be 0 when every named task is verified, 1 when any is not, and 2
  for a usage error (no task, unknown revision, not a git repository, no Runefile found,
  unreadable or invalid key file).

**Git notes: `rune receipt attach`**

- **FR-037**: `rune receipt attach [REVISION]` (default `HEAD`) MUST add to the note of the
  revision's commit under `refs/notes/rune-receipts` every local receipt whose snapshots before
  and after are that revision's tree and whose signature verifies with a trusted key (as
  FR-029). It MUST report how many it added and how many were present.
- **FR-038**: A note MUST hold one compact envelope per line, sorted and without duplicates, so
  git's `cat_sort_uniq` strategy merges concurrent notes. Attach MUST merge with the existing
  note and MUST NOT change any other note, ref or commit.
- **FR-039**: When no local receipt is bound to the revision, attach MUST write nothing and exit 1.

**Commands and names**

- **FR-040**: `rune receipt key` MUST print the public key of the signer Rune would use, creating
  the local key when none exists and `RUNE_RECEIPT_KEY` is not set.
- **FR-041**: `verify` and `receipt` MUST become built-in commands. A task named `verify` or
  `receipt` MUST run with `rune -- NAME`, the built-in MUST say so when the Runefile has such a
  task, and `rune import` MUST treat both names as Rune command names.

**Secrets**

- **FR-042**: Every text in a receipt that comes from the run (arguments, output paths, claims)
  MUST pass through the run's secret mask. A receipt whose arguments were masked MUST say so.
- **FR-043**: Verification and attach diagnostics MUST NOT print a value from the environment.

**Documentation**

- **FR-044**: The documentation MUST cover: turning receipts on; what a receipt proves and what it
  does not (the trust model and its limits: same-user access to the local key, agents with
  unrestricted shell access, CI key exposure to the job's processes, ignored files, the
  environment, changes made and undone within a run, submodules); local and CI key setup with a
  GitHub Actions secret; verifying, its reasons, JSON output and exit codes; attaching, fetching,
  pushing and merging notes; and the receipt format.

### Key Entities

- **Receipt**: the signed record of one task run: what ran, on which snapshot, with what result,
  from which path. Stored as a signed envelope.
- **Snapshot**: the identity of every non-ignored file in the working tree at one moment, in git's
  tree form, so it can be compared with a revision without a checkout.
- **Invocation**: one run of Rune; all receipts it writes share its ID.
- **Signing key**: the Ed25519 key that signs receipts: the local key or the CI key.
- **Trusted keys**: the keys whose signatures verification accepts.
- **Receipts note**: the note on a commit under `refs/notes/rune-receipts`: one envelope per line.
- **Verdict**: for one task at one revision, verified or not, with the reason and the receipt.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For each of 7 tamper fixtures (payload, signature, Runefile at the revision, import
  at the revision, input file at the revision, another target revision, a note replayed onto a
  commit with another tree), verification exits 1 and names the matching reason; for an output
  file changed after the run, the working tree report says changed.
- **SC-002**: Across fixtures with a dirty tree, an untracked file, and a file changed during the
  run, 0 receipts verify as passed at the clean commit.
- **SC-003**: Across fixtures for `failed`, `interrupted`, `refused`, `cached` and `incomplete`,
  0 receipts verify as passed.
- **SC-004**: A secret fixture value appears 0 times in the receipt directory, the note, and the
  text and JSON output of verify and attach.
- **SC-005**: A task that dumps its environment sees `RUNE_RECEIPT_KEY` 0 times; a sandboxed task
  sees the key directory empty.
- **SC-006**: After attach, push to a bare remote, clone and fetch of the notes ref, verification
  in the clone passes, and an unrelated note on the same commit is byte-identical.
- **SC-007**: With receipts off, every existing integration and golden test passes unchanged, and
  0 files appear under `.rune/receipts/` or the key directory.
- **SC-008**: For a parallel fixture of N tasks across 2 concurrent `rune` processes, exactly one
  receipt per task run is written, and each names only its own invocation.

## Assumptions

- `git` is on the PATH where receipts are recorded or verified, as the plan journal (spec 051)
  already assumes. Without git, receipts carry no snapshot.
- Taking a snapshot may store blob and tree objects in the repository's object database, as
  `git stash create` does; they are unreachable and git's own maintenance removes them. The index,
  refs and working tree are untouched.
- Ed25519 keys in PKCS#8 PEM, as `openssl genpkey -algorithm ed25519` writes them.
- A task runs as the same operating system user as Rune. The local key protects against a task
  only where the sandbox hides it; the documentation states this plainly.
- GitHub Actions does not pass repository secrets to workflows of pull requests from forks, so a
  CI key held in a secret signs only trusted runs.
- The project expresses which tasks need evidence as an ordinary Runefile task that runs
  `rune verify REVISION TASK...`; no new policy file is introduced.
- Reserving `verify` and `receipt` as command names is a breaking change for Runefiles with tasks
  of those names, handled as `ci`, `export` and `suggest` were.
- A receipt attests to what the recorder observed, not to the quality of the test suite.
