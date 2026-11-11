# Test data

Fetched on 2026-11-10. Tests read these files and never contact the network.

| File | Source |
|---|---|
| `v0.7.0/checksums.txt` | `https://github.com/rune-task-runner/rune/releases/download/v0.7.0/checksums.txt` |
| `v0.7.0/checksums.txt.sigstore.json` | Same release. It is in cosign's older bundle form, which the updater must keep reading. |
| `trusted_root.json` | The public Sigstore trust root, fetched through TUF (`trusted_root.json` target of `https://tuf-repo-cdn.sigstore.dev`). |

The signature stays verifiable offline: verification uses the signing time recorded in the
transparency log, not the current time, and the trust root lists the keys that were valid then.
