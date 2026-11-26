# Contract: receipt commands, setting and environment

## Setting

```rune
set receipts
```

A bare boolean setting, read as `set export` is. With it, every task Rune runs in run mode writes
a receipt. Without it nothing in this contract happens during a run.

## Environment

| Variable | Read by | Meaning |
|---|---|---|
| `RUNE_RECEIPT_KEY` | every `rune` process, once at start | PKCS#8 PEM Ed25519 private key to sign with. Removed from the process environment before anything runs, so no task, hook or child sees it |
| `XDG_STATE_HOME`, `LocalAppData` | as for the audit log | where the local key lives: `STATE/receipts/key.pem` |

## During a run (`set receipts`)

- First use, local key created (standard error, once):

  ```text
  receipts: created signing key /home/u/.local/state/rune/receipts/key.pem (SHA256:Qm3x...)
  ```

- A receipt cannot be signed or written after a body ran (standard error; exit code unchanged):

  ```text
  warning: receipt for test not written: open .rune/receipts/...: permission denied
  ```

- `RUNE_RECEIPT_KEY` is not a usable key, or the local key cannot be read or created (no body
  runs; exit 2):

  ```text
  rune: RUNE_RECEIPT_KEY: not a PKCS#8 PEM Ed25519 private key
  rune: receipts: cannot create signing key /path/key.pem: permission denied
  ```

No other output changes. `--dry-run`, `--summary`, `--list`, `--fmt`, `--dump` write no receipt.

## `rune verify`

```text
rune verify [flags] REVISION TASK...
```

| Flag | Meaning |
|---|---|
| `-f, --file PATH` | use this Runefile (locates `.rune/receipts/`) |
| `--key FILE` | trust the public keys in FILE (PKIX PEM); repeatable |
| `--json` | print one JSON document on standard output instead of text |

### Text output (standard output)

One line per task, in the order given, then the working tree line, then the outputs of
verified tasks, then any problems. The parenthesis of a verified task lists its arguments
(`with NAME=VALUE`), its channel, the claims recorded (`client NAME VERSION`, `auth MODE`,
`NAME=VALUE`), and last the signer, the only authenticated identity:

```text
test: passed at a1b2c3d (cli, signed by SHA256:Qm3x...)
build: passed on a snapshot identical to a1b2c3d (ran at 9f8e7d6 with changes, mcp, signed by SHA256:Qm3x...)
lint: not verified: no receipt for a1b2c3d
fmt: not verified: files changed during the run (receipt 20261125T101500Z-1a2b3c4d5e6f7a8b)
working tree: matches a1b2c3d
working tree: differs from a1b2c3d
outputs of build: dist/app changed
outputs of test: all 3 match
problem: .rune/receipts/x.json: malformed
```

Reason texts (`not verified: ...`):

| Reason | Text |
|---|---|
| `no-receipt` | `no receipt for SHORT` |
| `unknown-signer` | `signed by an untrusted key SHA256:...` |
| `bad-signature` | `the signature does not match the receipt` |
| `unsupported-version` | `its format version or status is not supported by this rune` |
| `failed` | `the run failed with exit code N` |
| `interrupted` | `the run was interrupted` |
| `refused` | `the body did not run: it was refused` |
| `cached` | `a cache hit, not a run (reuses receipt ID)` |
| `incomplete` | `the run was not fully observed` |
| `no-snapshot` | `the run was not in a git repository` |
| `changed-during-run` | `files changed during the run` |
| `different-snapshot` | `the run was on other files than SHORT` |
| `uncovered-runefile` | `the Runefile or an import is outside the repository or ignored: PATH` |
| `uncovered-inputs` | `N declared inputs are ignored by git` |

Every reason line except `no-receipt` names the receipt ID it comes from.

### JSON output

```json
{
  "revision": "HEAD",
  "commit": "a1b2c3d4...",
  "tree": "4b825dc6...",
  "verified": false,
  "tasks": [
    {
      "task": "test",
      "verified": true,
      "receipt": {
        "id": "20261125T101500Z-1a2b3c4d5e6f7a8b",
        "origin": "local",
        "status": "passed",
        "arguments": [],
        "channel": "cli",
        "signer": "SHA256:Qm3x...",
        "finishedAt": "2026-11-25T10:15:01.123Z",
        "commit": "a1b2c3d4...",
        "dirty": false,
        "exact": true
      }
    },
    {"task": "lint", "verified": false, "reason": "no-receipt"}
  ],
  "workingTree": {
    "matches": true,
    "outputs": [{"task": "build", "path": "dist/app", "state": "match"}]
  },
  "problems": [{"origin": ".rune/receipts/x.json", "reason": "malformed"}]
}
```

Empty lists are omitted. Field names are stable within format version 1.

### Exit status

| Code | When |
|---|---|
| 0 | every named task verified |
| 1 | any task not verified |
| 2 | usage error: no task, unknown revision, not a git repository, unreadable or invalid `--key` file, no Runefile found |

Verification writes nothing: no receipt, key, git object, ref or note.

## `rune receipt`

```text
rune receipt            # the note below when the Runefile has a task named receipt, then help; exit 0
rune receipt other      # usage error: unknown command "other" for "rune receipt"; to run a task named receipt, use: rune -- receipt   (exit 2)
```

### `rune receipt key`

```text
rune receipt key
```

Standard output: the public key, PKIX PEM. Standard error, one line:

```text
key SHA256:Qm3x... (local: /home/u/.local/state/rune/receipts/key.pem)
key SHA256:Qm3x... (RUNE_RECEIPT_KEY)
```

Creates the local key when `RUNE_RECEIPT_KEY` is unset and no local key exists (with the
creation notice). Exit 0; 2 when the key is unusable.

### `rune receipt attach`

```text
rune receipt attach [flags] [REVISION]
```

| Flag | Meaning |
|---|---|
| `-f, --file PATH` | use this Runefile |
| `--key FILE` | also accept receipts signed by these public keys; repeatable |

Standard error:

```text
attached 2 receipts to a1b2c3d (1 already there) in refs/notes/rune-receipts
no receipt for a1b2c3d to attach
```

| Code | When |
|---|---|
| 0 | at least one receipt is bound to the revision (added or already present) |
| 1 | none is bound; or git could not write the note (the message names git's reason and, for a missing identity, says to set `user.name` and `user.email`) |
| 2 | usage error, as for verify |

Only `refs/notes/rune-receipts` changes. The commit, other refs and other notes are untouched.

## Reserved names

`verify` and `receipt` join Rune's built-in commands. A task named either runs with
`rune -- verify` or `rune -- receipt`, and the built-in prints:

```text
note: "verify" is also a task of this Runefile; run it with: rune -- verify
```

`rune import` treats both as Rune command names.
