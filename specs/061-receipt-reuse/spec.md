# Feature Specification: Reuse of Trusted Receipts in CI

**Feature Branch**: `061-receipt-reuse`

**Created**: 2026-11-27

**Status**: Draft

**Input**: GitHub issue [#56](https://github.com/rune-task-runner/rune/issues/56), section "CI
reuse", the follow-up that spec 060 (signed execution receipts, `rune verify`, Git notes) left
out: "Add skipping only as an explicit opt-in policy for eligible tasks, not as a consequence of
signature validity alone." Related: receipts (spec 060), caching (spec 001), CI workflows
(spec 059), the agent sandbox (spec 053).

## Overview

Spec 060 made every task run leave a signed receipt and let anyone check receipts against a git
revision. CI still runs every task again, even when a run it trusts already passed on exactly
the same files. This feature lets a CI step skip such a task, under two explicit opt-ins: the
Runefile's author marks the task `[reuse]`, and the step names the public keys it trusts with
`--reuse-key`. A task is skipped only when a receipt signed by one of those keys says the task
passed, recently, on exactly the files it would run on now, in the same context.

```text
$ git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts
$ rune --reuse-key ci-receipts.pub lint test
reused: lint (receipt 20261127T101500Z-1a2b3c4d5e6f7a8b, signed by SHA256:Qm3x...)
not reused: test (the receipt is older than 24h0m0s)
go test ./...
ok  	example/pkg	0.412s
```

A skipped task's own receipt says `cached` and names the receipt it reused, so reuse never
produces new evidence that a task passed: `rune verify` still accepts only receipts of runs.

| Question | Answer |
|---|---|
| Who turns it on? | Both the author, with `[reuse]` on a task, and the person running Rune, with `--reuse-key FILE`. Neither alone skips anything |
| Whose receipts count? | Only those signed by a key given with `--reuse-key`. Not this machine's key, not a key named in the Runefile, not a key a note or an environment variable names |
| On which files? | Exactly the files the task would run on now: the receipt's snapshot before and after the body must both be the current working tree's snapshot |
| What else must match? | Task, project, definition, arguments, Rune version, OS and architecture; no variable overridden on the command line; declared outputs present and identical |
| How old may it be? | 24 hours by default; `--reuse-max-age` changes that |
| Where does Rune look? | `.rune/receipts/`, and the Git notes of every commit whose tree is the current snapshot |
| Is a skipped task evidence that it passed? | No. Its receipt says `cached`, and a cached receipt is never reused or verified |
| Can an agent skip a task this way? | Not through `rune mcp` or `rune serve`: they never reuse |

### Out of scope

- **Keyless signing** (Sigstore OIDC certificates). Still a follow-up of #56.
- **Reuse in workflows `rune ci github` writes.** Their jobs receive no signing key and no trusted
  key; a hand-written step adds `--reuse-key`. A later spec may generate it.
- **Reuse on the agent surface.** `rune mcp` and `rune serve` always run the task.
- **Fetching or pushing notes.** The documentation gives the git commands, as in spec 060.
- **Following a reused run in `rune verify`.** A `cached` receipt stays unverifiable, with the
  receipt it reuses named, as in spec 060.
- **Recording the environment and tool versions.** Environment variables, dotenv values,
  installed tools, network resources and ignored files are not recorded. Marking a task
  `[reuse]` is the author's statement that its result depends on none of them; the
  documentation says so.
- **Skipping the dependencies of a reused task.** Dependencies run, or are reused, by their own
  attributes, as they do for a `[cache]` hit.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - CI skips a task it already proved (Priority: P1)

A maintainer marks `test` with `[reuse]`. A CI run signs its receipts with the CI key and
attaches them to the commit's note. A later CI run on a commit with exactly the same files
fetches the notes and runs `rune --reuse-key ci-receipts.pub test`. Rune prints that it reused
the receipt, does not run the body, and exits 0. The receipt it writes for this run says
`cached` and names the receipt it reused.

**Why this priority**: This is the feature: work CI already proved is not repeated.

**Independent Test**: In a temporary repository, record a passing receipt with a test key, then
run the same task again with `--reuse-key` naming that key, and check that the body did not run,
the notice, the exit code, and the new receipt.

**Acceptance Scenarios**:

1. **Given** a `[reuse]` task with a passing receipt signed by key K on the current snapshot,
   **When** `rune --reuse-key K.pub TASK` runs, **Then** the body does not run, standard error
   says `reused: TASK` with the receipt's ID and signer, and the exit status is 0.
2. **Given** that reuse, **When** the new receipt is read, **Then** its status is `cached` and
   it names the reused receipt; **When** it is the only receipt for the task, **Then**
   `rune verify HEAD TASK` reports `cached`, not passed.
3. **Given** the same receipt and the same task, **When** Rune runs without `--reuse-key`,
   **Then** the body runs, and nothing about reuse is printed.
4. **Given** a task without `[reuse]` and a receipt that would otherwise qualify, **When** Rune
   runs with `--reuse-key`, **Then** the body runs and nothing about reuse is printed for it.
5. **Given** `rune ci run --reuse-key K.pub TASK` in a job, **When** a qualifying receipt exists,
   **Then** the task is reused as on the command line.
6. **Given** a `[reuse]` task that depends on another `[reuse]` task, **When** both have
   qualifying receipts, **Then** both are reused, each with its own notice and receipt.
7. **Given** a `[reuse]` task that also has `[cache]`, **When** its local cache is current,
   **Then** the cache hit wins as today; **When** it is not, **Then** reuse is tried next.

---

### User Story 2 - Evidence that does not qualify never skips a task (Priority: P1)

A contributor forges a receipt, signs one with their own key, replays an old one, or brings one
from another platform or another set of files. In every case the task runs, and the notice says
why the evidence was not used.

**Why this priority**: A skip on bad evidence is worse than no feature: it hides a failing test.

**Independent Test**: Build one adversarial fixture per reason and check that the body runs and
the notice names the reason.

**Acceptance Scenarios**:

1. **Given** a qualifying receipt with one byte of its payload or signature changed, **When**
   reuse is tried, **Then** the task runs and the reason is a bad signature.
2. **Given** a receipt signed by a key not given with `--reuse-key`, including this machine's own
   signing key and the key in `RUNE_RECEIPT_KEY`, **When** reuse is tried, **Then** the task runs
   and the reason is an untrusted key.
3. **Given** a receipt that finished longer ago than the maximum age, or later than now, **When**
   reuse is tried, **Then** the task runs and the reason is that the receipt is stale.
4. **Given** a receipt whose status is `failed`, `interrupted`, `refused`, `cached` or
   `incomplete`, **When** reuse is tried, **Then** the task runs and the reason is that status.
5. **Given** a receipt recorded on other files, on files that changed during its run, outside a
   repository, or with an uncovered Runefile, import or declared input, **When** reuse is tried,
   **Then** the task runs and the reason names that.
6. **Given** a receipt from another OS or architecture, another Rune version, another task
   definition, other arguments, or another project of the same repository, **When** reuse is
   tried, **Then** the task runs and the reason names the difference.
7. **Given** a receipt whose arguments were masked, or a current run whose arguments are masked,
   **When** reuse is tried, **Then** the task runs and the reason is that the arguments cannot be
   compared.
8. **Given** a receipt of a run with a variable overridden on the command line, or a current run
   with one, **When** reuse is tried, **Then** the task runs and the reason is the override.
9. **Given** a `[reuse]` task that declares `[cache]` outputs, **When** an output file is
   missing, added, or differs from the receipt's, **Then** the task runs and the reason is that
   the outputs differ.

---

### User Story 3 - The author decides which tasks may be reused (Priority: P1)

The author marks `lint` and `test` with `[reuse]`, and never `deploy`. Rune refuses `[reuse]` on
a task whose run is its effect, whose result depends on the network, or whose result is a
model's answer.

**Why this priority**: Only the author knows whether a task's result depends only on the files.
A signature from a trusted key must not be enough to skip a deploy.

**Independent Test**: Analyze Runefiles with each combination and check the diagnostics.

**Acceptance Scenarios**:

1. **Given** `[reuse]` on a task with `[confirm]`, `[plan]`, `[applies]` or `[network]`, **When**
   the Runefile is analyzed, **Then** it is an error at the attribute that names the conflict,
   and no task runs.
2. **Given** `[reuse(...)]` with an argument, **When** it is analyzed, **Then** it is an error.
3. **Given** `[reuse]` on a task run by the agent executor, **When** it is analyzed, **Then** it
   is an error.
4. **Given** a Runefile with `[reuse]`, **When** it is formatted, listed, dumped or opened in the
   editor, **Then** the attribute is kept, completed and described like other attributes.

---

### User Story 4 - Evidence from a pull request counts after the merge (Priority: P2)

CI on a pull request runs `test` on the merge preview, attaches its receipts and pushes the
notes ref. The squash merge to `main` creates another commit with exactly the same files. CI on
`main` fetches the notes and reuses `test`, because Rune finds receipts by the files, not by the
commit.

**Why this priority**: Without it reuse only helps a re-run of the same commit.

**Independent Test**: Attach a receipt to commit A, create commit B with A's tree, push the notes
through a bare remote, and run with `--reuse-key` on B in a clone; repeat with a commit C whose
tree differs.

**Acceptance Scenarios**:

1. **Given** a qualifying receipt in the note of commit A, **When** reuse is tried at commit B
   whose tree is A's tree, **Then** the task is reused.
2. **Given** that note, **When** reuse is tried at a commit C with another tree, **Then** the task
   runs and the reason is that no receipt was found for these files.
3. **Given** the same receipt both in `.rune/receipts/` and in a note, **When** reuse is tried,
   **Then** it counts once.
4. **Given** a note line that is not a receipt, **When** reuse is tried, **Then** it is ignored
   and the task's verdict is decided by the other candidates.

---

### Edge Cases

- **`--reuse-key` and the Runefile has no `set receipts`**: usage error before any task: no
  receipt can match, because the receipt's files would hold that setting.
- **`--reuse-key` outside a git repository**: usage error before any task.
- **A key file that cannot be read or holds no Ed25519 public key**: usage error before any task.
- **`--reuse-max-age` without `--reuse-key`, or a value that is not a positive duration**: usage
  error.
- **`--dry-run`, `--summary`, `--list`**: reuse is not tried; dry-run prints what it prints today.
- **The snapshot for reuse cannot be taken**: the task runs, and the notice says so.
- **The Runefile or an import changed after Rune loaded it**: the task runs; the snapshot would
  not hold what runs.
- **Several qualifying receipts**: the newest is reused.
- **A failure hook**: never reused, as it never uses the cache.
- **A post-hook marked `[reuse]`**: reused like any task Rune runs.
- **A dependency that changed tracked files before a `[reuse]` task starts**: the task is judged
  on the snapshot taken after that change, so it is reused only when a receipt ran on exactly
  those files.
- **`--watch`**: each re-run tries reuse again.
- **A receipt with an output list too large for the receipt itself**: the receipt holds the
  list's digest, which is compared with the digest of the current outputs, so the list file
  itself is not needed and a receipt from a note can still be reused.
- **A `[reuse]` task in a Runefile without `set receipts`, run without `--reuse-key`**: runs as
  always; the attribute has no effect.

## Requirements *(mandatory)*

### Functional Requirements

**Opting in**

- **FR-001**: A task attribute `[reuse]`, without arguments, MUST mark a task whose run may be
  skipped on trusted evidence. Without it a task is never skipped by reuse.
- **FR-002**: `[reuse]` with an argument MUST be a parse error, and the analyzer MUST report an
  error for `[reuse]` together with `[confirm]`, `[plan]`, `[applies]` or `[network]`, naming the
  conflict.
- **FR-003**: The analyzer MUST report an error for `[reuse]` on a task run by the agent
  executor: its result is a model's answer, not a function of the files.
- **FR-004**: The run command and `rune ci run` MUST take `--reuse-key FILE`, repeatable. Reuse
  is tried only when it is given. `rune mcp`, `rune serve` and the agent executor MUST never
  try reuse.
- **FR-005**: `--reuse-key` MUST be a usage error, before any task runs, when a file cannot be
  read or holds no Ed25519 public key in PEM, when the Runefile does not set receipts, or when
  the Runefile is not in a git repository.
- **FR-006**: `--reuse-max-age DURATION` MUST set the maximum age of a reusable receipt, default
  24 hours. A value that is not a positive duration, or the flag without `--reuse-key`, MUST be a
  usage error.

**Trust**

- **FR-007**: The trusted keys for reuse MUST be exactly the keys given with `--reuse-key`. This
  machine's signing key, `RUNE_RECEIPT_KEY`, the Runefile, notes, claims and the environment MUST
  NOT add a trusted key.

**Candidates**

- **FR-008**: The candidates for a task MUST be the receipts in the project's `.rune/receipts/`
  and the lines of the notes under `refs/notes/rune-receipts` of every commit whose tree is the
  current snapshot, each counted once. For a note on a commit the repository does not have
  (fetching the notes ref does not fetch the commits it annotates), the candidates are its lines
  whose receipt names the current snapshot.
- **FR-009**: Reading candidates MUST NOT fetch, write a ref, a note or the index, or change the
  working tree.

**Qualification**

- **FR-010**: Reuse MUST be tried for a `[reuse]` task in run mode right before its body would
  start, after the `[confirm]` and policy gates and, for a `[cache]` task, after a cache check
  that succeeded and did not hit, against the snapshot of the working tree at that moment. A
  `[cache]` task whose cache check failed runs without trying reuse.
- **FR-011**: A candidate MUST qualify only when all of these hold: its signature verifies with
  a trusted key; its format version is supported; its project and task are the task's; its
  status is `passed`; it ran as the task itself, not as a failure hook; its snapshots before
  and after the body are both the current snapshot;
  no Runefile, import or declared input is uncovered; it finished no longer ago than the maximum
  age and not later than now; its OS, architecture, Rune version and Rune commit are this run's;
  its definition digest is the task's current one; neither its arguments nor the current
  arguments were masked, and they are equal; neither its run nor the current run overrode a
  variable on the command line; and, for a task with declared `[cache]` outputs, the files the
  outputs name now are exactly the receipt's, each with the same size and SHA-256, compared
  through the digest the receipt holds of its output list.
- **FR-012**: When candidates qualify, the newest MUST be reused. When none does, the task MUST
  run, and the reason MUST be the one of the most relevant candidate (the newest whose snapshot
  is the current one, else the newest for the task) from a fixed list: no receipt, untrusted
  key, bad signature, unsupported version, failed, interrupted, refused, cached, incomplete, no
  snapshot, changed during the run, different snapshot, uncovered Runefile, uncovered inputs,
  failure hook, stale, other platform, other Rune, other definition, masked arguments, other arguments,
  overridden variables, outputs differ.
- **FR-013a**: Reuse MUST NOT be tried when the current run itself reads what no snapshot holds:
  a Runefile or import outside the repository or ignored, or a declared `[cache]` input git
  ignores. The task runs and the notice names it.
- **FR-013**: Signature validity alone MUST NOT skip a task: every condition of FR-011 and the
  task's `[reuse]` attribute are required.

**What a reuse records and prints**

- **FR-014**: A reused task MUST NOT run its body, and its run MUST count as a success for the
  exit status, as a cache hit does.
- **FR-015**: Its receipt MUST have status `cached` and name the reused receipt as the run it
  reuses. A `cached` receipt MUST never qualify for reuse, so reuse never chains.
- **FR-016**: A reused task MUST print one line on standard error naming the task, the reused
  receipt's ID and its signer's key ID. A `[reuse]` task that runs while reuse is on MUST print
  one line naming the task and the reason: the candidate's reason of FR-012, or the condition of
  the current run that kept reuse from being tried (the snapshot failed, the Runefile changed
  after it was loaded, the cache could not list the declared inputs). Tasks without `[reuse]`
  MUST print nothing new.
- **FR-017**: Every receipt MUST record the names of the variables overridden on the command
  line for its invocation, sorted, when there are any. The field is optional, so the format
  version stays 1 and receipts of spec 060 still verify.
- **FR-018**: A notice MUST NOT print a value from the environment, an argument, or a variable;
  everything it prints from a receipt MUST pass through the run's secret mask.

**Unchanged behavior**

- **FR-019**: Without `--reuse-key`, every run MUST behave and print exactly as before this
  feature, including runs of `[reuse]` tasks; the only change is the `overrides` field of
  FR-017 in receipts of runs that overrode a variable.
- **FR-020**: `rune verify` and `rune receipt attach` MUST behave as in spec 060.

**Documentation**

- **FR-021**: The documentation MUST cover: marking tasks `[reuse]` and what the author vouches
  for; `--reuse-key` and `--reuse-max-age`; where candidates come from and how to fetch notes in
  CI; every reason; a GitHub Actions example that signs on pull requests and reuses after the
  merge; and the trust model and its limits: the keys must come from somewhere the contributor
  under test cannot change, a skip trusts the key's holder and not the code, the environment
  and tools are not covered, a reused run is not evidence, and receipts do not replace CI
  execution for tasks that are not `[reuse]`.
- **FR-022**: `docs/GRAMMAR.md` MUST describe `[reuse]` and its rules, with fixtures, and the
  editor support MUST know the attribute.

### Key Entities

- **Reuse attribute**: the author's statement that a task's result depends only on the
  repository's files, its arguments and Rune's own context.
- **Reuse keys**: the public keys the person running Rune trusts for reuse, given per run.
- **Candidate**: a receipt from the receipts directory or from a note of a commit with the
  current tree.
- **Reuse decision**: for one task in one run: reused (with the receipt) or run (with the
  reason).
- **Overrides**: the names of the variables a run overrode on the command line, now recorded in
  each receipt.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a qualifying fixture, the body runs 0 times, the exit status is 0, and the new
  receipt's status is `cached` naming the reused receipt; with only that receipt present,
  `rune verify` reports `cached` and exits 1.
- **SC-002**: For each of 4 forged fixtures (changed payload, changed signature, signed by an
  untrusted key, signed by this machine's own key when not given), the body runs every time.
- **SC-003**: For each of 2 stale fixtures (older than the maximum age, finished in the future),
  the body runs every time.
- **SC-004**: For each of 10 context fixtures (other files, another OS, another architecture,
  another Rune version, another definition, other arguments, another project, overridden
  variables in the receipt, overridden variables now, changed outputs), the body runs every
  time.
- **SC-005**: For each of 9 incomplete-evidence fixtures (`failed`, `interrupted`, `refused`,
  `cached`, `incomplete`, changed during the run, uncovered Runefile, uncovered inputs, masked
  arguments), the body runs every time.
- **SC-006**: Without `--reuse-key`, or without `[reuse]`, 0 tasks are skipped by reuse, and every
  existing test and golden file passes unchanged.
- **SC-007**: After attach on commit A, push and fetch through a bare remote, reuse succeeds on a
  commit with A's tree and fails on a commit with another tree.
- **SC-008**: A secret fixture value appears 0 times in reuse notices and new receipts.

## Assumptions

- Spec 060 is in place: receipts, snapshots, signing keys, notes and verification work as it
  describes.
- The person who writes the CI step chooses the trusted keys. A key file in the commit under
  test can be changed by its author, so the documentation recommends taking the key from the
  base branch or a repository variable. A pull request can change its own workflow too, so this
  feature gives such a run no power it did not already have.
- Twenty-four hours is a safe default age: CI images and dependency mirrors change on a scale of
  days, and a re-run is always the safe fallback.
- Requiring the same Rune version and commit is acceptable: CI pins the Rune version, and a
  developer whose receipts CI should reuse uses the same release.
- Recording override names is safe: names come from the command line's `NAME=VALUE` form, carry
  no value, and pass through the secret mask.
- Notes are read with local git commands only; reading every note of the ref is fast enough for
  the number of commits a repository attaches receipts to.
- Adding the `[reuse]` attribute is declarative surface allowed by Principle III; Principle I's
  "skipping is per-task opt-in and logged" holds because both the attribute and the flag are
  required and every reuse is printed.
