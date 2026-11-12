# Contract: Release Pipeline for the Image

Decisions referenced as R*n* are in [../research.md](../research.md).

## Tags in `ghcr.io/rune-task-runner/rune`

| Release kind | `<version>` | `<version>-amd64` | `<version>-arm64` | `latest` |
|---|---|---|---|---|
| Stable (`vX.Y.Z`) | pushed | pushed | pushed | moved |
| Prerelease (`vX.Y.Z-rc.N`) | pushed | pushed | pushed | untouched |
| Snapshot, dry run, pull request | none | none | none | untouched |

- `<version>` and `latest` point to the same index.
- `<version>-<arch>` points to the image the index lists for that architecture.
- The repository path has no suffix.

## `.goreleaser.yaml`

| Section | After |
|---|---|
| `builds` | unchanged; the single source of main package, flags, linker flags and environment |
| `dockers`, `docker_manifests` | removed |
| `kos` | one entry: `build: rune`; `base_image` by digest; `repositories: [ghcr.io/rune-task-runner/rune]`; `platforms: [linux/amd64, linux/arm64]`; `tags: ["{{ .Version }}", "{{ if not .Prerelease }}latest{{ end }}"]`; `bare: true`; `sbom: spdx`; `creation_time` from the commit; the six labels |
| `docker_signs` | `artifacts: manifests`; `cosign sign --recursive <image>@<digest> --yes` |
| everything else | unchanged |

Requires GoReleaser v2.20 or newer. `goreleaser check` fails on an older one.

## Release workflow, image-related steps in order

1. Install crane, cosign, syft. Log in to GHCR with `crane auth login`. No QEMU, no Buildx, no
   Docker daemon use.
2. `goreleaser release`: builds, pushes and signs the image along with everything else.
3. Attest binary provenance (unchanged).
4. Resolve the index digest of `<version>` with crane.
5. Attest image provenance on the index digest and push it to the registry.
6. For amd64 and arm64: resolve that platform's digest and tag it `<version>-<arch>`. Last, so a
   failed tag cannot cost the release its attestation.

A failure in step 2 fails the run. If step 6 fails, the two tags are added by hand with the
`crane tag` commands in `docs/releasing.md`; the job is not rerun, because it would compute a
new version.

## Supply chain

| Guarantee | Subject | Verify with |
|---|---|---|
| Signed by `release.yml` on `main`, keyless | index digest and both image digests | `cosign verify` with the identity in `docs/releasing.md` |
| Provenance attestation | index digest | `gh attestation verify oci://ghcr.io/rune-task-runner/rune@<digest>` |
| SPDX bill of materials | index and both images, stored in the registry | command documented in `docs/releasing.md` (R6) |

## Release assets that must not change

Six archives, `checksums.txt`, `checksums.txt.sigstore.json`, `checksums.txt.pem`, one SPDX file
per archive, the Homebrew cask, the Scoop manifest, binary provenance, and the editor extension
for stable releases. A snapshot's `dist/` file list is compared before and after, ignoring image
entries.

## CI dry run (`release-dryrun` job)

1. `goreleaser check`.
2. `goreleaser release --snapshot --clean --skip=sign,sbom`: builds the image into the local
   daemon under `goreleaser.ko.local`. Pushes nothing.
3. `scripts/check-image.sh` on that image.
4. Compare the image's binary with `dist/`'s Linux binary for the runner's architecture; fail on
   any difference.

## Base image

| Item | Value |
|---|---|
| Repository | `ghcr.io/rune-task-runner/rune-base`, public |
| Built by | `scripts/build-base-image.sh`, run by the manual workflow `base-image.yml` on `main` |
| Signed | cosign keyless, by that workflow. The first publication is made by hand before the workflow exists on `main`, and is signed by running the workflow once after the merge (same inputs, same digest) |
| Tags | `<first 12 hex of the distroless digest>` for the index, plus `-amd64` and `-arm64` helper tags the build script pushes; consumers reference the index by digest only |
| Referenced | by digest in `.goreleaser.yaml` and `.ko.yaml` |
| Updated | maintainer runs the workflow with a new distroless digest, then changes both references in a pull request |
