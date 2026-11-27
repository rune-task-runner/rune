# Data Model: Reuse of Trusted Receipts in CI

## Reuse attribute

`[reuse]` on a task. No arguments. Stored as an `ast.Attribute` of kind `reuse`.

| Rule | Source |
|---|---|
| At most one per task | RUNE2022 |
| Not with `[confirm]`, `[plan]`, `[applies]`, `[network]` | RUNE2022 |
| Not on a task run by the agent executor | RUNE2022 |
| No argument | parse error |

## Reuse options (`cli.Options`)

| Field | Flag | Rule |
|---|---|---|
| `ReuseKeys []string` | `--reuse-key FILE`, repeatable | each file holds at least one Ed25519 public key in PEM |
| `ReuseMaxAge time.Duration` | `--reuse-max-age DURATION`, default `24h` | positive; only with `--reuse-key` |

## Reuser (per run of `execute`, `internal/cli`)

| Field | Meaning |
|---|---|
| `keys []ed25519.PublicKey` | the trusted keys, from `ReuseKeys` only |
| `maxAge time.Duration` | from `ReuseMaxAge` |
| `overridden bool` | the run has command-line overrides |
| `notes map[string][][]byte` + mutex | note lines found per snapshot tree |

Created only in run mode when `ReuseKeys` is not empty, after the receipt signer, which must
exist (`set receipts`) and have a repository.

## Want (`internal/receipt`)

What a candidate must match for one task run.

| Field | From |
|---|---|
| `Project` | the signer's project path (Runefile from the top of the repository) |
| `Task` | the task's canonical name |
| `Tree` | the snapshot taken right before the body would start |
| `Rune RuneInfo` | this binary: version, commit, OS, architecture |
| `Definition` | `definitionDigest(task, executor)` |
| `Arguments []Arg`, `ArgumentsMasked bool` | `receiptArgs(task, params, mask)` |
| `Overridden bool` | the run's overrides |
| `Outputs *Outputs` | the current declared outputs, digest only (nil when none) |
| `Now time.Time`, `MaxAge time.Duration` | the clock and the flag |

## Decision

`receipt.Reuse(cands, trusted, want) Verdict` returns spec 060's `Verdict` (a candidate that is not a receipt is passed over):
`Verified` true means reuse, with `Receipt` (ID, signer key ID, ...). Otherwise `Reason` is one
of the reasons below and `Receipt` the most relevant candidate, when there is one.

### Reasons

Spec 060's: `no-receipt`, `unknown-signer`, `bad-signature`, `unsupported-version`, `failed`,
`interrupted`, `refused`, `cached`, `incomplete`, `no-snapshot`, `changed-during-run`,
`different-snapshot`, `uncovered-runefile`, `uncovered-inputs`.

New, checked in this order after those:

| Reason | Condition |
|---|---|
| `failure-hook` | `role` is not `task`: a failure hook's body ran with the failed run's variables |
| `stale` | `finishedAt` before `Now - MaxAge`, or after `Now` |
| `other-platform` | `rune.os` or `rune.arch` differs |
| `other-rune` | `rune.version` or `rune.commit` differs |
| `other-definition` | `definition` differs |
| `masked-arguments` | `argumentsRedacted`, or the current arguments are masked |
| `other-arguments` | `arguments` differ |
| `overridden` | `overrides` not empty, or the current run has overrides |
| `outputs-differ` | `outputs.digest` differs from the current one (absent counts as `""`) |

## Receipt (change to spec 060's format)

| Field | Type | Rule |
|---|---|---|
| `overrides` | `[]string`, optional | sorted, masked names of the variables overridden on the command line of the invocation; omitted when none. Placed after `argumentsRedacted`. Format version stays 1 |

A reused run's receipt: `status: "cached"`, no `exitCode`, `related.reuses` = the reused
receipt's ID, `source.before` = `source.after` = the snapshot it was reused on.

## State: one `[reuse]` task run with reuse on

```text
gates ([agent-allow], [applies], [confirm]) pass
  -> [cache] hit?            yes -> cached (as today)
  -> [cache] check failed?   yes -> "not reused (inputs could not be listed)" -> body runs
  -> snapshot T (shared with the receipt)
       failed / Runefile changed after load -> "not reused" -> body runs
  -> candidates(T) -> Reuse
       verified -> "reused" -> receipt cached, reuses = ID -> success
       not      -> "not reused (reason)" -> body runs -> receipt as today
```
