# Contract: What a Release Must Provide for Self-Upgrade

`rune --self-upgrade` in every installed binary depends on these facts about a published release.
Changing any of them strands existing installs: they refuse to upgrade and people must reinstall by
hand. `docs/releasing.md` carries the same list for maintainers. Decisions referenced as R*n* are in
[../research.md](../research.md).

## Where

| What | Location |
|---|---|
| Latest stable release | `GET https://api.github.com/repos/rune-task-runner/rune/releases/latest`, field `tag_name` |
| Files of a release | `https://github.com/rune-task-runner/rune/releases/download/<tag>/<file>` |

The tag is `v` followed by a semantic version. A stable release has no prerelease part and is not
marked draft or prerelease.

## Files

| File | Requirement |
|---|---|
| `checksums.txt` | One line per archive: SHA-256 in lowercase hex, two spaces, file name |
| `checksums.txt.sigstore.json` | Signature over `checksums.txt`, as a standard Sigstore bundle or the older cosign bundle (R9) |
| `rune_<version>_<os>_<arch>.tar.gz` | For `linux` and `darwin`, `amd64` and `arm64`. Holds a regular file named `rune` at the top level |
| `rune_<version>_windows_<arch>.zip` | For `amd64` and `arm64`. Holds a regular file named `rune.exe` at the top level |

`<version>` is the tag without the `v`. Other files in the release and other entries in an archive
are ignored.

## Signer

| Field | Value |
|---|---|
| Issuer | `https://token.actions.githubusercontent.com` |
| Subject | `https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main` |

Both must match exactly (R8). The signature must carry a certificate transparency timestamp and a
transparency log entry that the public Sigstore trust root accepts.

Consequences for maintainers:

- The release workflow file keeps its name and path.
- Releases are cut from `main`, as the workflow already enforces.
- Signing stays keyless through GitHub Actions and the public Sigstore instance.

## Size

No single archive may exceed 100 MiB and no binary 200 MiB (R16).

## Checked by

- A golden test that verifies the real v0.7.0 `checksums.txt` and signature file with the values
  above.
- The integration test that builds a release fixture in this layout.
- The release-dryrun gate, whose archive names the tests derive the same way.
