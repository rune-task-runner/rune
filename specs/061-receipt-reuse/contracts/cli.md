# Contract: Reuse Flags and Notices

## Flags

On the run command (`rune [flags] TASK...`) and on `rune ci run [flags] TASK [ARG...]`:

| Flag | Value | Meaning |
|---|---|---|
| `--reuse-key FILE` | path, repeatable | Skip `[reuse]` tasks that have a qualifying receipt signed by a key in FILE. FILE holds one or more Ed25519 public keys in PEM, as `rune receipt key` prints |
| `--reuse-max-age DURATION` | Go duration, default `24h` | The oldest receipt that may be reused |

Help text:

```text
--reuse-key FILE          skip [reuse] tasks that a receipt signed by the key in FILE shows passed on these files (repeatable)
--reuse-max-age DURATION  the oldest receipt --reuse-key may reuse (default 24h0m0s)
```

`rune mcp` and `rune serve` do not take these flags.

## Usage errors (exit 2, before any task)

| Condition | Message |
|---|---|
| `--reuse-max-age` without `--reuse-key` | `--reuse-max-age needs --reuse-key` |
| `--reuse-max-age` not positive | `--reuse-max-age must be a positive duration, such as 24h` |
| FILE unreadable | `--reuse-key FILE: <error>` |
| FILE has no Ed25519 public key | `--reuse-key FILE: <error>` |
| Runefile without `set receipts` | `--reuse-key: the Runefile does not set receipts, so no receipt can be reused; add "set receipts"` |
| No git repository could be opened for the Runefile | `--reuse-key: no git repository could be opened for the Runefile, so no receipt can match its files` |

In `--dry-run`, `--summary` and `--list` the reuse flags are read but not used (the
`--reuse-max-age` rules above still apply).

## Notices (standard error, muted like `cached:`)

A reused task, in place of its body:

```text
reused: TASK (receipt ID, signed by KEYID)
```

A `[reuse]` task that runs while `--reuse-key` is given, before its body (and before `running:`
for a `[cache]` task):

```text
not reused: TASK (REASON TEXT)
```

| Reason | Text |
|---|---|
| `no-receipt` | `no receipt for these files` |
| `unknown-signer` | `signed by an untrusted key KEYID` |
| `bad-signature` | `the signature does not match the receipt` |
| `unsupported-version` | `its format version or status is not supported by this rune` |
| `failed` | `the run failed with exit code N` (or `the run failed`) |
| `interrupted` | `the run was interrupted` |
| `refused` | `the body did not run: it was refused` |
| `cached` | `a cache hit, not a run` (with ` (reuses receipt ID)` when known) |
| `incomplete` | `the run was not fully observed` |
| `no-snapshot` | `the run was not in a git repository` |
| `changed-during-run` | `files changed during the run` |
| `different-snapshot` | `the run was on other files than these` |
| `uncovered-runefile` | `the Runefile or an import is outside the repository or ignored: PATH` |
| `uncovered-inputs` | `N declared inputs are ignored by git` |
| `failure-hook` | `it ran as a failure hook` |
| `stale` | `the receipt is older than DURATION` or `the receipt finished in the future` |
| `other-platform` | `it ran on another OS or architecture` |
| `other-rune` | `it ran with another rune` |
| `other-definition` | `the task's definition changed` |
| `masked-arguments` | `masked arguments cannot be compared` |
| `other-arguments` | `it ran with other arguments` |
| `overridden` | `a variable was overridden on the command line` |
| `outputs-differ` | `the declared outputs are not the ones it made` |

Conditions of the current run, with no candidate involved:

| Condition | Text |
|---|---|
| The snapshot failed | `the files could not be snapshotted: ERROR` |
| The Runefile or an import changed after it was loaded | `the Runefile changed after it was loaded` |
| A `[cache]` task's cache could not list its declared inputs | `the declared inputs could not be listed: ERROR` |
| The Runefile or an import is outside the repository or ignored | `the Runefile or an import is outside the repository or ignored: PATH` |
| Declared `[cache]` inputs that git ignores exist now | `N declared inputs are ignored by git` |
| Whether git ignores the declared inputs could not be checked | `the declared inputs could not be checked: ERROR` |
| The declared outputs could not be read | `the declared outputs could not be read: ERROR` |
| The receipts directory could not be read | `read receipts: ERROR` |

## Exit status

Unchanged: a reused task counts as a success, as a cache hit does. Usage errors exit 2.

## Unchanged

Without `--reuse-key`, output is byte-identical to before. `rune verify` and `rune receipt
attach` are unchanged; a reused run's receipt verifies as `cached`.
