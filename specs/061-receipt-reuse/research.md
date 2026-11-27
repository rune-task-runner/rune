# Research: Reuse of Trusted Receipts in CI

Decisions for spec 061. Each names what was chosen, why, and what else was weighed. Code
references are to the tree at the start of the feature (spec 060 merged as #117).

## R1. Two opt-ins: `[reuse]` on the task, `--reuse-key` on the run

**Decision**: A task is skipped by reuse only when the Runefile marks it `[reuse]` and the person
running Rune passes at least one `--reuse-key FILE`.

**Rationale**: Issue #56 asks for "an explicit opt-in policy for eligible tasks, not a consequence
of signature validity alone". Only the author knows whether a task's result depends only on the
repository's files; only the person running CI knows which keys to trust. Principle I requires
skipping to be per-task opt-in and visible; the attribute is the per-task opt-in.

**Alternatives considered**:
- *Flag only, naming tasks* (`--reuse test --reuse lint`): moves the author's knowledge into
  every CI step, and a step that names `deploy` by mistake would skip a deploy.
- *Reuse every `[cache]` task*: `[cache]` means "skip on a matching local fingerprint", and
  marking a test `[cache]` to get CI reuse would also cache it locally. The meanings differ.
- *A Runefile setting listing trusted keys*: the contributor under test can edit the Runefile
  (R2).

## R2. Trust only the keys given on the command line

**Decision**: The trusted keys are exactly the public keys in the `--reuse-key` files. Unlike
`rune verify`, the key this machine signs with is not added, and neither is `RUNE_RECEIPT_KEY`.

**Rationale**: On a self-hosted runner, or a developer machine, the local key is readable by
every task body Rune runs from the command line (spec 060's documented limit). A receipt signed
by it says "some process of this user signed this", which is enough for a reviewer to look at
but not enough to skip CI work. Naming the key is the explicit trust decision the issue asks for
("CI must not accept self-signed evidence from an arbitrary contributor"). A CI that wants to
reuse its own receipts passes its own public key, which is one line.

**Alternatives considered**: verify's rule (own key plus `--key`): rejected for the reason above.
A key in an environment variable: a variable reaches nested `rune` processes started by bodies,
which would then reuse without the step saying so.

## R3. Candidates: the receipts directory and notes found by tree

**Decision**: For a snapshot T, candidates are every file in `.rune/receipts/` and every line of
the `refs/notes/rune-receipts` notes of every commit whose tree is T. A new
`Repo.NotesForTree(ctx, tree)` runs `git notes --ref=... list`, resolves `COMMIT^{tree}` for each
annotated commit with one `git cat-file --batch-check=%(objectname)`, and reads the matching note
blobs. A note on a commit the clone does not have (the pull request's merge preview is never
pushed, and fetching the notes ref does not fetch it) is read too, keeping the lines whose
receipt names T as its snapshot; the signature and every other condition are still checked. The
result is cached per tree for the run.

**Rationale**: A receipt is evidence for a tree, not a commit (spec 060 R12). The useful flow is a
pull request's CI attaching receipts to the merge preview, then `main`'s CI after a squash merge:
another commit, the same tree. Looking only at HEAD's note would never find those. `attach`
already guarantees that a note's receipts have the commit's tree, so finding notes by the
annotated commit's tree is exact; the receipt's own snapshot is checked again anyway.

**Alternatives considered**:
- *HEAD's note only, as verify reads*: misses every squash or rebase merge.
- *Read every note blob and filter by subject*: reads the whole history of notes on every run.
- *Notes on tree objects instead of commits*: changes spec 060's notes format; `rune verify` and
  attach would need a migration.

## R4. Qualification: spec 060's acceptance, then the reuse checks

**Decision**: `receipt.Reuse` shares spec 060's candidate judging (signature, payload type,
version, status, snapshots before and after equal to T, covered Runefiles and inputs) by
factoring the loop of `Evaluate` into `judgeAll`. A candidate that passes those is then checked,
in order, for: failure hook (its role is not `task`: a hook's body ran with the failed run's
variables), stale (finished more than the maximum age ago, or after now), other platform (OS or
architecture), other Rune (version or commit), other definition (digest), masked arguments (its
`argumentsRedacted` or the current run's), other arguments, overridden (its `overrides` or the
current run's), and outputs differ (R6). Project and task filter candidates as in `Evaluate`. The
verdict is `verdictOf`'s: the newest that qualifies, else the reason of the newest whose snapshot
is T, else of the newest for the task.

**Rationale**: One code path for "is this a valid receipt of a pass on these files" keeps verify
and reuse from drifting. The order puts the cheapest-to-explain reasons first and keeps reasons
about the receipt's own validity ahead of reasons about context.

**Alternatives considered**: a separate judge for reuse: duplicates the security-relevant checks.

## R5. Overrides are recorded by name

**Decision**: `Receipt.Overrides` (`overrides`, optional) lists the sorted, masked names of the
variables overridden with `NAME=VALUE` on the command line of the receipt's invocation. Reuse
refuses a receipt with overrides, and refuses to reuse at all in a run with overrides.

**Rationale**: `rune mode=release build` and `rune build` run on the same files with different
results, and the receipt did not say which. Values cannot be recorded or digested (spec 060
FR-015: a digest of a low-entropy secret reveals it). Names are enough to refuse. Being optional,
the field keeps format version 1 and spec 060 receipts verify unchanged; reuse needs the same Rune
version anyway, so older receipts without the field never qualify.

**Alternatives considered**: a boolean (names are free and help a reviewer); comparing names on
both sides (two values under one name would match); only checking the current run (misses a
receipt recorded with an override).

## R6. Outputs compared through the receipt's output digest

**Decision**: For the current run, Rune computes the outputs the recorder would record (the
declared `[cache]` outputs through `cache.OutputFiles`, `receipt.OutputFiles`, masked paths,
`receipt.BuildOutputs`) without writing a manifest, and compares its `Digest` with the receipt's.
Both absent is a match; one absent is a difference.

**Rationale**: The digest binds the full list of path, size and SHA-256, inline or in a manifest.
Comparing digests needs no manifest file, so a receipt from a note (which carries no manifest)
can be reused. A missing, added or changed output changes the digest. This is the "matching or
restorable outputs" of the issue: in CI the outputs come from the job's restored bundle (spec
059); when they are absent, the task runs.

**Alternatives considered**: `CompareOutputs` with the manifest: fails for notes, and does not see
an added file.

## R7. Freshness: 24 hours by default

**Decision**: `--reuse-max-age` takes a Go duration, default `24h`, and must be positive. A
receipt whose `finishedAt` is earlier than now minus the maximum age, or later than now, is
stale.

**Rationale**: Freshness is the issue's guard against drift Rune does not record: runner images,
toolchains, dependency mirrors. A day covers a pull request merged the same day; a longer window
is one flag away. A finish time in the future cannot come from an honest clock older than the
reuse; rejecting it costs only a re-run.

**Alternatives considered**: no default limit (unbounded drift); a per-task age in the attribute
(adds attribute arguments for a value the CI owner sets better).

## R8. A reused run's receipt says `cached`

**Decision**: The receipt of a reused run has status `cached` and `related.reuses` set to the
reused receipt's ID, as a cache hit's does. A `cached` receipt never qualifies (R4 status check),
so reuse never chains, and `rune verify` reports it as `cached`.

**Rationale**: The status set of spec 060 already means "the body did not run because earlier
evidence said it need not". A new status would need every verifier to learn it; old verifiers
would call it unsupported. Never chaining keeps freshness tied to a real run.

**Alternatives considered**: a `reused` status (no gain, new reader work); following `reuses` in
verify (would turn reuse into evidence, which the issue forbids).

## R9. One snapshot for both reuse and the receipt

**Decision**: `tryReuse` takes the before snapshot through `takeBefore`, the function the receipt
already uses. `bodyStarting` takes it only when no snapshot was taken yet. A reuse marks the run
itself (`cached`, its declared inputs, `related.reuses`) instead of calling `cachedHit`, which
would take the snapshot again. For a `[cache]` task reuse is tried only after a cache decision
that succeeded and missed: when the cache cannot list the declared inputs, the receipt could not
say whether git ignores them, so the task runs.

**Rationale**: The receipt needs the snapshot right before the body; reuse needs the same moment.
Nothing runs between the two, so one snapshot serves both and the cost of reuse is no extra
snapshot.

**Alternatives considered**: a separate snapshot for reuse: doubles git work for no gain.

## R10. Analyzer rules for `[reuse]`

**Decision**: New code `RUNE2022` (`CodeInvalidReuse`). Errors: a second `[reuse]` on a task
(`firstOf`); `[reuse]` with `[confirm]` (its run is its effect), `[plan]` (the plan file is made
by the run), `[applies]` (its run applies a plan), `[network]` (its result depends on the
network); `[reuse]` on a task run by the agent executor (its result is a model's answer). An
argument, `[reuse(x)]`, is a parse error because the parser reads `reuse` as a bare attribute,
as it does `[readonly]`.

**Rationale**: Each refused combination is a task whose skip would hide an effect or an input no
snapshot covers. Errors, not warnings: a Runefile must not read as reusable when it cannot be.

**Alternatives considered**: a warning for `[reuse]` without `set receipts`: warnings appear only
in the editor and `rune analyze`, and the run already refuses `--reuse-key` without receipts
(R11), so the case is covered where it matters. Reading `set receipts := false` statically would
need evaluation.

## R11. When `--reuse-key` is a usage error

**Decision**: In run mode, before any task: a key file that cannot be read or has no Ed25519 public
key; a Runefile without `set receipts`; a Runefile outside a git repository. In `cmd/rune`:
`--reuse-max-age` given without `--reuse-key`, or not a positive duration. `--dry-run`,
`--summary` and `--list` ignore the reuse flags, as they run no body.

**Rationale**: Each case would make every `[reuse]` task run with a reason the operator could have
been told once: with receipts off no receipt can match (the receipt's tree would hold
`set receipts`), and outside a repository no snapshot exists.

**Alternatives considered**: warnings with every task running: a CI step would silently lose the
reuse it asked for.

## R12. Notices

**Decision**: A reused task prints, muted like `cached:`,
`reused: TASK (receipt ID, signed by KEYID)`. A `[reuse]` task that runs while reuse is on prints,
muted, `not reused: TASK (REASON TEXT)` before its body. The texts reuse `rune verify`'s for the
shared reasons. A failed snapshot or a Runefile changed after load gives its own text. Nothing new
is printed for tasks without `[reuse]` or without the flag.

**Rationale**: Principle I: every skip is visible. The reason for a run is what an operator needs
to make reuse work, printed once per task.

## R13. Surfaces

**Decision**: Reuse is wired in `execute` (the run command, `--watch`, `--choose`, and `rune ci
run`, which calls `execute`). The MCP adapters and the agent executor build their own engines and
never set a reuser.

**Rationale**: An agent's call must run the task (spec non-goal); CI and people use the command
line.

## R14. Documentation

**Decision**: A section "Reuse evidence in CI" in `docs/how-to/receipts.md`: the attribute and what
the author vouches for, the flags, candidates and the notes fetch, the reason table, a GitHub
Actions example (pull request signs, attaches and pushes notes; `main` fetches notes and reuses),
and the trust model and limits. Rows in `docs/cli.md`, the attribute in `docs/GRAMMAR.md` and
`docs/runefile.md`, the code in `docs/diagnostics.md`.

**Rationale**: Receipts and reuse share their trust model; one page keeps the limits in one place.
