# Data Model: `rune --self-upgrade`

Nothing is stored between runs. These are the values one run passes between its steps. Names are
descriptive, not final identifiers. Decisions referenced as R*n* are in [research.md](research.md).

## Install

The binary that is running.

| Field | Meaning | Rule |
|---|---|---|
| Path | Location after symbolic links are resolved | The file that is replaced (FR-014) |
| Version | The version `rune --version` reports | Must parse as a semantic version, else Origin is development |
| Stamped | Whether the version was set at build time | False plus a parseable version means `go install` (R12) |
| Mode | Permission bits of the file at Path | Copied to the replacement (FR-016) |
| Origin | See below | Decided before any network use |

## Origin

One of: `development`, `go-install`, `container`, `homebrew`, `scoop`, `standalone`.

- Decided in that order; first match wins (R12).
- Only `standalone` continues. Every other value ends the run with outcome `refused`, the reason,
  and the command to use instead.

## Release

The upgrade target.

| Field | Source | Rule |
|---|---|---|
| Tag | `tag_name` from the latest-release answer | Must be `v` plus a semantic version with no prerelease part |
| Version | Tag without the `v` | Compared with Install.Version (FR-007) |
| Draft, Prerelease | Same answer | Either true means the answer is refused (FR-006) |

Derived names, all under `releases/download/<Tag>/`:

- Checksum list: `checksums.txt`
- Signature file: `checksums.txt.sigstore.json`
- Archive: `rune_<Version>_<os>_<arch>.tar.gz`, or `.zip` when the system is Windows

## Checksum list

Lines of `<64 hex characters><two spaces><file name>`.

- Trusted only after the signature file verifies against it with the identity in R8.
- Exactly one line must name the Archive. None means the platform is unsupported (FR-008). A
  malformed line for the Archive is a verification failure.

## Signature file

Either a standard Sigstore bundle (has `mediaType`) or the older cosign bundle (has
`base64Signature`, `cert`, `rekorBundle`). Both are verified by the same checks (R9). No other shape
is accepted.

## Outcome

| Value | Exit | When |
|---|---|---|
| `upgraded` | 0 | The file at Install.Path is now Release.Version |
| `current` | 0 | Install.Version is equal to or newer than Release.Version |
| `refused` | 1 | Origin is not `standalone` |
| `failed` | 1 | Any step after the origin check failed; Install is unchanged |
| `usage` | 2 | The flag was combined with something else; nothing started |
| `cancelled` | 130 | Interrupted; Install is unchanged or fully replaced |

Every outcome except `usage` reports Install.Version, on the first line of output. `upgraded`,
`current` and any `failed` that happened after the release was found also report Release.Version
(FR-026).

In code, only `upgraded` and `current` are returned as a result value. `refused`, `failed` and
`cancelled` are returned as errors (a refusal type, the failure type with its reason, and the
context's cancellation), and `usage` is decided in the command layer before the updater exists.

An error that carries no reason from the list below (a defect, not an expected failure) is still
reported as `failed` with exit 1, never as a usage error.

## Failure reasons

Each has its own message (FR-030). The wording is in
[contracts/cli-self-upgrade.md](contracts/cli-self-upgrade.md).

| Reason | Raised by |
|---|---|
| `offline` | No route, name lookup failure, connection refused |
| `timeout` | A limit in R16 was reached |
| `rate-limited` | Release service answered 403 or 429 with no remaining quota |
| `release-lookup` | Any other bad answer from the release service |
| `unsupported-platform` | No archive for this system and processor in the verified list |
| `unverified` | Checksum list or signature file not published, malformed, wrong signer, or the two do not match |
| `checksum-mismatch` | Archive bytes do not match the verified list |
| `download-failed` | A file could not be fetched: bad status, or the transfer ended early |
| `too-large` | A download or the extracted binary passed its cap |
| `bad-archive` | Binary missing, duplicated, or not a regular file |
| `not-writable` | Lock, staging file or replacement could not be created |
| `in-progress` | A lock exists whose owner may be alive: younger than 15 minutes and not naming a dead process on this machine |
| `replace-failed` | The final swap failed; on Windows the old file was put back, or the message says where it is |

## Files beside the binary during a run

| Name | Lifetime |
|---|---|
| `.rune-upgrade.lock` | From before the first network call until the run ends |
| `.rune-upgrade.lock.takeover` | Only while a stale lock is being replaced |
| `.rune-upgrade-*` | Staged archive and staged binary; removed on every return path |
| `rune.exe.old`, `rune.exe.old.N` | Windows only; left by a successful upgrade, removed by the next `--self-upgrade`. The numbered name is used while an earlier one is still running |

## States of one run

```text
start
  -> usage                      (flag combined with anything else)
  -> refused                    (origin is not standalone)
  -> failed: in-progress | not-writable        (lock)
locked
  -> failed: offline | timeout | rate-limited | release-lookup
  -> current
target known
  -> failed: unverified | unsupported-platform | download-failed | offline | timeout
list trusted
  -> failed: checksum-mismatch | download-failed | too-large | bad-archive | not-writable
staged
  -> failed: replace-failed
  -> upgraded
any state after "locked" -> cancelled
```

The file at Install.Path changes only on the edge `staged -> upgraded`.
