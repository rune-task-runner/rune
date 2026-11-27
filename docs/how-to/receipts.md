# Prove a task ran: receipts and `rune verify`

> Make every task run leave a small signed receipt, check receipts against a git revision
> without running anything, and let CI skip what a run it trusts already proved. Part of the
> [how-to guides](README.md).

An agent can say "all tests pass" without running them. A receipt is evidence that Rune's own
execution path wrote: which task ran, on which files, with what result, started from where,
signed by a key the task cannot use. `rune verify` accepts a receipt for a revision only when
the task passed on exactly that revision's files.

```sh
rune test                     # writes .rune/receipts/<id>.json
rune verify HEAD test lint    # one line per task; exits 0 only when both are verified
```

## Turn receipts on

Add `set receipts` to the Runefile:

```rune
set receipts

# Run the test suite.
test:
    go test ./...
```

From then on every task Rune runs writes one receipt, whoever started it: you on the command
line, `rune ci run` in a CI job, an agent through `rune mcp` or `rune serve`, a dependency, a
post-hook or a failure hook. `--dry-run`, `--summary` and `--list` write none. Without the
setting nothing changes: no receipt, no key, no extra git call.

The first run creates a signing key and says where:

```text
receipts: created signing key /home/you/.local/state/rune/receipts/key.pem (SHA256:Qm3x...)
```

## What a receipt records

- The task, its arguments after defaults, and the receipts it relates to: the dependencies
  that ran before it, the task a post-hook follows, the failed run a failure hook ran for,
  the run a cache hit reuses. All receipts of one run share an invocation ID; for an agent's
  call it is the call's ID in the [audit log](../mcp.md#the-audit-log).
- The status: `passed`, `failed` (with the exit code Rune exited with), `interrupted`,
  `refused` (the body did not run: a declined `[confirm]`, an agent policy, a sandbox that
  could not start), `cached` (a cache hit; the body did not run) or `incomplete` (Rune could
  not observe the run in full).
- A snapshot of the working tree right before the body and right after it: every file git
  does not ignore, tracked or not, identified as git identifies a tree. Also HEAD's commit and
  whether the tree differed from it.
- The SHA-256 of the Runefile and each file it imports, the declared `[cache]` inputs git
  ignores, and the size and SHA-256 of each declared `[cache]` output of a passed run.
- The Rune version, OS, executor and a digest of the task's definition.
- Where the run came from: `cli`, `ci-run`, `mcp` or `agent` (a `rune` started by the body of
  an agent's call), and claims: the name and version an MCP client reported, and `CI`,
  `GITHUB_ACTIONS`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`, `GITHUB_WORKFLOW` and
  `GITHUB_REPOSITORY` when set. Claims are recorded, never trusted: only the signing key
  is authenticated.

A receipt never holds task output, an environment value, a variable's value, or a digest of
one. Arguments pass through [secret masking](secret-masking.md), and a receipt whose arguments
were masked says so.

## Verify a revision

```sh
rune verify HEAD test lint
```

```text
test: passed at a1b2c3d (cli, signed by SHA256:Qm3x...)
lint: not verified: no receipt for a1b2c3d
working tree: matches a1b2c3d
```

The revision is any git revision; nothing is checked out. Rune reads the receipts in
`.rune/receipts/` and in the revision's note (see below), and accepts one for a task only
when all of these hold:

- a trusted key signed it and its format version is known;
- the task passed;
- its snapshots before and after the body are both the revision's tree;
- the Runefile and every import lie in the repository and are not ignored;
- no declared `[cache]` input is ignored by git.

Among accepted receipts the newest is reported. "passed at" means it ran at that commit on a
clean tree; "passed on a snapshot identical to" means it ran on uncommitted work whose files
are exactly the revision's, for example before you committed them.

When no receipt is accepted, the line gives the reason:

| Reason (JSON) | Text |
|---|---|
| `no-receipt` | no receipt for the revision |
| `unknown-signer` | signed by an untrusted key |
| `bad-signature` | the signature does not match the receipt |
| `unsupported-version` | its format version or status is not supported by this rune |
| `failed` | the run failed with exit code N |
| `interrupted` | the run was interrupted |
| `refused` | the body did not run: it was refused |
| `cached` | a cache hit, not a run |
| `incomplete` | the run was not fully observed |
| `no-snapshot` | the run was not in a git repository |
| `changed-during-run` | files changed during the run |
| `different-snapshot` | the run was on other files than the revision |
| `uncovered-runefile` | the Runefile or an import is outside the repository or ignored |
| `uncovered-inputs` | declared inputs are ignored by git |

The last lines say whether the working tree still matches the revision, and, for each
verified task that declared outputs, whether those files are unchanged. They never change a
verdict. A file in `.rune/receipts/` or a note line that is not a receipt is listed as a
problem.

`--json` prints the same report as one JSON document. The exit status is `0` when every task
is verified, `1` when one is not, and `2` for a usage error. Verification runs no task and
writes nothing.

One passing receipt is not "all checks passed": name every task that needs evidence. A task
can hold that list for the project:

```rune
# Fail unless lint and test passed on exactly the committed files.
evidence:
    rune verify HEAD lint test
```

## Keys and trust

`rune verify` trusts the key this machine signs with and each public key you give with
`--key FILE`. A receipt signed by any other key is `unknown-signer`, however valid its
signature.

```sh
rune receipt key > me.pub          # print your public key; its ID goes to stderr
rune verify --key me.pub HEAD test # a teammate trusts it
```

The local key lives in `rune/receipts/key.pem` under your state directory
(`$XDG_STATE_HOME`, `%LocalAppData%`, or `~/.local/state`), readable only by you. On Windows
the file permissions are advisory.

## Sign receipts in CI

CI signs with a key from a secret instead of a local one. Make a key once:

```sh
openssl genpkey -algorithm ed25519 -out ci-receipts.pem
RUNE_RECEIPT_KEY="$(cat ci-receipts.pem)" rune receipt key > ci-receipts.pub
```

Store `ci-receipts.pem` as a repository secret, commit `ci-receipts.pub`, and pass the
secret to the step that runs the tasks:

```yaml
- name: Test
  env:
    RUNE_RECEIPT_KEY: ${{ secrets.RUNE_RECEIPT_KEY }}
  run: rune test
```

Rune reads `RUNE_RECEIPT_KEY` once when it starts and removes it from its environment, so
no task body, hook or child process inherits it. An invalid value stops the run before any
task. Anyone can then check CI's evidence:

```sh
rune verify --key ci-receipts.pub HEAD test
```

A workflow from `rune ci github` passes no secret to its jobs, so their receipts are signed
by each runner's own new key; add the variable to a hand-written step as above to sign with
the CI key. GitHub does not give secrets to workflows of pull requests from forks.

## Share receipts with Git notes

`rune receipt attach [REVISION]` adds the receipts that ran on exactly the revision's files,
and that a trusted key signed, to the note of its commit under `refs/notes/rune-receipts`. The
commit does not change, and neither do other notes. Failed runs are attached too: they are
evidence as well.

```sh
rune receipt attach                      # attached 2 receipts to a1b2c3d (0 already there) ...
git push origin refs/notes/rune-receipts
```

A note is a commit on the notes ref, so git needs `user.name` and `user.email`. A CI job that
pushes the notes needs `contents: write` permission.

Someone else fetches the notes and verifies, with no local receipts:

```sh
git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts
rune verify --key ci-receipts.pub HEAD test
```

A note holds one receipt per line, sorted, each once. When two machines attached to the same
commit, merge their notes with git's `cat_sort_uniq` strategy, which keeps every line:

```sh
git fetch origin refs/notes/rune-receipts:refs/notes/origin/rune-receipts
git notes --ref=rune-receipts merge -s cat_sort_uniq refs/notes/origin/rune-receipts
```

Moving a note to another commit makes nothing valid there: a receipt counts only for a
revision whose tree is the snapshot it signed.

## Reuse evidence in CI

CI can skip a task that a run it trusts already proved on exactly the same files. Two opt-ins
are needed, and neither alone skips anything:

- The author marks the task `[reuse]`: its result depends only on the files git does not
  ignore, its arguments, and Rune's version, OS and architecture. Not on the environment, the
  network, installed tools or ignored files.
- The CI step names the public keys it trusts with `--reuse-key FILE` (repeatable).

```rune
set receipts

# Lint the code.
[reuse]
lint:
    golangci-lint run

# Run the tests.
[reuse]
test:
    go test ./...
```

```sh
git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts
rune --reuse-key ci-receipts.pub lint test
```

```text
reused: lint (receipt 20261127T101500Z-1a2b3c4d5e6f7a8b, signed by SHA256:Qm3x...)
not reused: test (the receipt is older than 24h0m0s)
```

A reused task runs no body and counts as a success. `rune ci run` takes the same flags.

Rune reads the receipts in `.rune/receipts/` and in the notes of every commit whose tree is
the files the task would run on now, so receipts attached to a pull request's merge preview
count after a squash merge that produces the same files. Fetching the notes ref does not fetch
the commits it annotates; from a note on a commit the clone lacks, Rune takes the receipts that
name those files. It reuses the newest receipt for
which all of these hold:

- a key given with `--reuse-key` signed it. This machine's own key and `RUNE_RECEIPT_KEY` are
  not trusted unless you give their public key;
- the task passed, on exactly these files before and after its body, with the Runefile, its
  imports and the declared `[cache]` inputs covered, as `rune verify` requires, in its run and
  in this one;
- it is a run of the task itself, not of a failure hook, whose body saw the failed run;
- it finished within `--reuse-max-age` (default `24h`), and not in the future;
- the OS, architecture, Rune version and commit, the task's definition and its arguments are
  this run's;
- no variable was overridden on the command line, in its run or in this one, and no argument
  was masked;
- for a task with declared `[cache]` outputs, the output files here are exactly the ones it
  recorded. In CI they come from the job's restored bundle; when they are missing, the task
  runs.

When none qualifies, the task runs and Rune says why, with the reason of the most relevant
receipt: the texts of `rune verify` above, plus:

| Reason | Text |
|---|---|
| no receipt | `no receipt for these files` |
| failure hook | `it ran as a failure hook` |
| stale | `the receipt is older than 24h0m0s`, or `the receipt finished in the future` |
| other platform | `it ran on another OS or architecture` |
| other Rune | `it ran with another rune` |
| other definition | `the task's definition changed` |
| masked arguments | `masked arguments cannot be compared` |
| other arguments | `it ran with other arguments` |
| overridden | `a variable was overridden on the command line` |
| outputs differ | `the declared outputs are not the ones it made` |

The receipt of a reused run says `cached` and names the receipt it reused. It is never evidence
that the task passed: `rune verify` reports it as `cached`, and it is never reused itself.

`[reuse]` cannot be combined with `[confirm]`, `[plan]`, `[applies]` or `[network]`, or be used
on an `agent` task. Without `set receipts`, or outside a git repository, `--reuse-key` is a
usage error. `rune mcp` and `rune serve` never reuse: an agent's call always runs the task.

### In GitHub Actions

The pull request signs with the CI key, attaches its receipts and pushes the notes; `main`
fetches them and reuses. Keep the public key where a pull request cannot change it, such as a
repository variable, since a key file in the commit under test is the contributor's to edit:

```yaml
# On pull requests from this repository (forks get neither the secret nor write access).
permissions:
  contents: write
steps:
  - uses: actions/checkout@v4
  - name: Test
    env:
      RUNE_RECEIPT_KEY: ${{ secrets.RUNE_RECEIPT_KEY }}
    run: rune lint test
  - name: Share the receipts
    run: |
      git config user.name ci && git config user.email ci@users.noreply.github.com
      git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts || true
      rune receipt attach
      git push origin refs/notes/rune-receipts
```

```yaml
# On pushes to main.
steps:
  - uses: actions/checkout@v4
  - name: Test, reusing what the pull request proved
    env:
      REUSE_KEY: ${{ vars.RUNE_REUSE_PUBLIC_KEY }}
    run: |
      git fetch origin refs/notes/rune-receipts:refs/notes/rune-receipts || true
      printf '%s\n' "$REUSE_KEY" > "$RUNNER_TEMP/ci.pub"
      rune --reuse-key "$RUNNER_TEMP/ci.pub" lint test
```

When two jobs push notes at the same time, one push is refused; fetch and merge the notes as
shown above, then push again.

## What a receipt proves, and what it does not

A receipt attests to what Rune observed when it ran the task, signed by a key. It does not
attest that the test suite is any good, and a valid signature from a key you do not trust is
not evidence. Know the limits before you rely on it:

- **The key is your user's.** A task runs as you. A body Rune runs on the command line can
  read the local key file, and so can an agent with its own shell access as your user. Run
  such an agent through Rune only (see [Set up an agent client](agent-setup.md)): the tasks
  an agent calls through `rune serve` run in [the sandbox](../mcp.md#the-sandbox), which hides
  the key directory.
- **A CI key is visible to the job.** Rune removes `RUNE_RECEIPT_KEY` from its environment, but
  another process of the same job, or a task reading its parent's initial environment on
  Linux, can still reach it. Use one key per repository, and give the secret only to trusted
  branches.
- **A `rune` started by a task body signs with the local key**, never the CI key. Run the tasks
  that need CI evidence directly from the workflow step.
- **Ignored files and the environment are not covered.** The snapshot holds what git does not
  ignore. Installed tools, environment variables, network resources and ignored files (a
  `node_modules`, a build cache) are not part of it. A declared `[cache]` input that git
  ignores makes the receipt unverifiable.
- **Changes undone within a run are not seen.** Rune compares the snapshot before the body
  with the one after it. A file changed and changed back while the task ran looks unchanged.
- **Submodules** count as the commit they point to; uncommitted changes inside one are not
  covered.
- **Hidden index entries are not trusted.** In a repository where a file is marked
  assume-unchanged or skip-worktree (a sparse checkout, for example), Rune snapshots the files
  as they are and never takes the index's word for them. Files the checkout leaves out are then
  missing from the snapshot, so such a run does not verify against a full commit.
- **Rune's own `.rune` directory** is left out of the snapshot: receipts and cache records are
  written there during a run.
- **A task can delete receipts.** They live in the project. A deleted receipt is missing
  evidence, which never verifies; a changed one fails its signature.
- **Snapshots store objects.** Taking a snapshot stores unreachable blobs and trees in the
  repository's object database, as `git stash create` does; git's own maintenance removes
  them. The index, HEAD, refs and working tree are never touched.
- **A reuse trusts the key's holder, not the code.** `--reuse-key` skips work because someone
  who holds that key ran it. Give it only keys whose holders you trust to run the task, and
  take the key file from somewhere the contributor under test cannot change.
- **`[reuse]` is the author's word.** Rune does not check that a task's result depends only on
  its files. A task that reads the environment, the network, installed tools or ignored files
  can pass on one machine and fail on another; leave it without `[reuse]`.
- **Receipts do not replace CI.** Tasks without `[reuse]` always run, and a reused run is not
  evidence that the task passed.

## The receipt format

A receipt file is one line of JSON: a [DSSE](https://github.com/secure-systems-lab/dsse)
envelope signed with Ed25519, whose payload is an
[in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md).
The statement's subject is the snapshot before the body (`gitTree`), or the Runefile's
SHA-256 outside a repository. Its predicate type is
`https://rune-task-runner.github.io/rune/receipt/v1`, and the predicate holds the fields
above with `"version": 1`. The envelope's `keyid` is `SHA256:` and the unpadded base64 of the
SHA-256 of the public key's PKIX encoding. Above 64 output files, their list moves to
`ID.outputs.json` next to the receipt and the receipt keeps its digest.

---

**Next:** [Caching](caching.md) · [Generate a GitHub Actions workflow](ci-github.md) · [CLI reference](../cli.md)
