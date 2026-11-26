# Data Model: Signed Execution Receipts

The on-disk and on-wire forms are in [contracts/receipt.md](contracts/receipt.md). This file
names the entities, their fields, their rules and how they relate.

## Receipt (predicate)

The signed record of one task run. Fields, in the order they are written:

| Field | Type | Rule |
|---|---|---|
| `version` | integer | `1`. Any other value is `unsupported-version` |
| `id` | string | `YYYYMMDDTHHMMSSZ-` + 16 lowercase hex; equals the file name stem |
| `invocation` | string | 16 hex; shared by every receipt of one run; the MCP call's audit ID on `mcp` |
| `parentInvocation` | string, optional | the agent call ID that started this `rune` (`RUNE_INVOCATION_ID`) |
| `task` | string | canonical name, `mod::name` for a module task |
| `arguments` | list of `{name, values[]}` | effective, after defaults; masked; empty for a task without parameters |
| `argumentsRedacted` | bool, optional | true when masking changed any value |
| `role` | `task` or `failure-hook` | |
| `related` | object, optional | `dependencies` (IDs), `follows` (ID), `failureOf` (ID), `reuses` (ID) |
| `status` | enum | `passed`, `failed`, `interrupted`, `refused`, `cached`, `incomplete` |
| `exitCode` | integer, optional | present when the body ran |
| `startedAt`, `finishedAt` | RFC 3339 UTC, nanoseconds | |
| `durationMs` | integer | from the monotonic clock |
| `rune` | `{version, commit, os, arch}` | the recording binary |
| `executor` | string | executor identity as the cache uses it, e.g. `sh`, `python:python3` |
| `definition` | hex SHA-256 | of the task's definition dump plus the executor identity |
| `project` | string | Runefile path relative to the repository top, forward slashes; the file name outside a repository |
| `runefiles` | list of `{path, sha256, covered}` | every file the load read; `covered` false when outside the repository or ignored |
| `source` | object, optional | absent outside a repository: `commit` (optional), `dirty`, `before`, `after` |
| `uncoveredInputs` | list of paths, optional | declared `[cache]` inputs git ignores |
| `outputs` | object, optional | `count`, `digest`, and `files` (`{path, size, sha256}`) or `manifest` |
| `channel` | enum | `cli`, `ci-run`, `mcp`, `agent` |
| `claims` | object, optional | `client` `{name, version}`, `auth`, `env` (map) |

**Validation (on write)**: every string from the run is masked; no environment value, variable
value or task output is stored; `source.before` and `source.after` are 40 or 64 lowercase hex.

**State transitions**: a receipt is created once by `finish` and never changed. A run has one
receipt per body Rune was asked to run (FR-003). The status is final.

```text
begin ──► (cache hit) ───────────────────────────► cached
  │
  ├──► (refusal before body) ───────────────────► refused
  │
  └──► bodyStarting ──► body ──► ok ─────────────► passed
                          │      └ error ────────► failed
                          └ context cancelled ───► interrupted
(any snapshot failure turns passed or failed into incomplete)
```

## Snapshot

The tree ID git would write for the working tree staged with `git add -A`, Rune's `.rune`
directory of the project excluded. Equal snapshots mean equal non-ignored files. Fields in a
receipt: `before`, `after` (and `commit`, `dirty` = snapshot differs from `HEAD^{tree}` or there
is no HEAD).

## Envelope

A DSSE envelope: `payloadType`, base64 `payload` (an in-toto Statement v1 holding the receipt as
its predicate), `signatures` (one, `{keyid, sig}`). The `keyid` selects which trusted key is
tried: no trusted key with that ID is `unknown-signer`, a failed check with it is
`bad-signature`. The key that verifies the signature is the authenticated signer.

## Signing key

| Source | Where | Created by Rune |
|---|---|---|
| CI | `RUNE_RECEIPT_KEY` (PKCS#8 PEM), read once and removed from the environment | no |
| Local | `STATE/receipts/key.pem`, dir `0700`, file `0600` | on first use with receipts on, or by `rune receipt key` |

Key ID: `SHA256:` + unpadded base64 of SHA-256 of the PKIX DER of the public key.

## Trusted keys

The set verification and attach accept: the signing key this machine would use (never created by
verification) and every public key given with `--key`.

## Receipts note

The note of one commit in `refs/notes/rune-receipts`: one compact envelope per line, sorted,
unique, newline-terminated. Union-merged by attach and by `git notes merge -s cat_sort_uniq`.

## Verdict

Per requested task at one revision:

| Field | Rule |
|---|---|
| `task` | as requested |
| `verified` | true only for an accepted receipt (R12 step 4) |
| `reason` | empty when verified; else one of `no-receipt`, `unknown-signer`, `bad-signature`, `unsupported-version`, `failed`, `interrupted`, `refused`, `cached`, `incomplete`, `no-snapshot`, `changed-during-run`, `different-snapshot`, `uncovered-runefile`, `uncovered-inputs` |
| `receipt` | summary of the accepted receipt, or of the candidate the reason comes from: `id`, `status`, `arguments`, `channel`, `signer`, `finishedAt`, `commit`, `dirty`, `exact` (ran at the revision's commit on a clean tree), `origin` (`local` or `note`) |

Malformed candidates are listed apart as `problems` (`origin`, `reason: malformed`), since their
task is unknown.

## Working tree report

`matches` (bool) and, for each accepted receipt with outputs, `outputs` entries `{task, path,
state}` with state `match`, `changed`, `missing` or `unknown`. It never changes a verdict.

## Relationships

- Invocation 1..n Receipt (by `invocation`).
- Receipt n..n Receipt through `related` (dependencies, follows, failureOf, reuses).
- Commit 1..1 Receipts note (by notes ref); a note's receipts are evidence for that commit only
  when their snapshots equal its tree.
- Cache record 0..1 Receipt (`receipt` field), referenced by `reuses` on a cache hit.
