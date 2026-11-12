# Data Model: Build Rune's Container Image with ko

No program data. These are the published objects and how they relate. Decisions referenced as
R*n* are in [research.md](research.md).

## Base image

`ghcr.io/rune-task-runner/rune-base`, a multi-platform index (linux/amd64, linux/arm64).

| Part | Value | Rule |
|---|---|---|
| Parent | `gcr.io/distroless/static-debian12:nonroot` at a recorded digest | Never a floating tag (FR-010) |
| Added layer | directory `/work` owned by 65532; link `/rune -> /ko-app/rune` owned by root | Fixed timestamps, so the digest is reproducible. `/work` belongs to the image's user so a volume mounted there is writable, as in v0.7.0 |
| WorkingDir | `/work` | FR-003 |
| Env | parent's, plus `RUNE_CONTAINER=1` | FR-007 |
| Label | `org.opencontainers.image.source`, the repository | Links the GHCR package to the repository |
| User | parent's (`65532`) | FR-004 |
| Reference | by digest in `.goreleaser.yaml` and `.ko.yaml` | The two must be equal (drift test, R9) |

Changes only when a maintainer rebuilds it (R11). Signed with cosign.

## Product image

`ghcr.io/rune-task-runner/rune`. One image per architecture, listed by one index.

| Field | Value | Source |
|---|---|---|
| Layers | base image's, an empty ko data layer, the binary at `/ko-app/rune` | ko |
| Entrypoint | `/ko-app/rune` | ko (accepted change, R3) |
| `/rune` | link to the binary | base image |
| WorkingDir | `/work` | base image |
| User | `65532` | base image |
| Env | base's, `PATH` with `/ko-app` appended, `KO_DATA_PATH=/var/run/ko`, `RUNE_CONTAINER=1` | base image and ko |
| Labels | title, description, source, licenses, version, revision | `kos.labels` (FR-005) |
| Binary | built by ko from the `rune` build's settings; byte-identical to the archive binary | R1, R7 |

## Tags

| Tag | Points to | Written by | When |
|---|---|---|---|
| `<version>` | the index | GoReleaser (ko) | every release |
| `latest` | the index | GoReleaser (ko) | stable releases only |
| `<version>-amd64` | the amd64 image the index lists | release workflow (crane) | every release |
| `<version>-arm64` | the arm64 image the index lists | release workflow (crane) | every release |

`<version>` is the Git tag without its leading `v`.

## Digests and what attaches to them

Three digests per release: the index and the two images.

| Attached thing | Index | Each image | Made by |
|---|---|---|---|
| cosign signature | yes | yes | GoReleaser `docker_signs`, recursive (R5) |
| Provenance attestation | yes | no (as today) | workflow, `attest-build-provenance` |
| SPDX bill of materials | yes | yes | ko (R6) |

## Release kinds

| Kind | Tags pushed to production | Image destination |
|---|---|---|
| Stable | `<version>`, `<version>-amd64`, `<version>-arm64`, `latest` | GHCR |
| Prerelease | `<version>`, `<version>-amd64`, `<version>-arm64` | GHCR |
| Snapshot or dry run | none | local daemon as `goreleaser.ko.local/...` (R8) |
| Local `rune docker` | none | local daemon as `rune:local` |
| Local `rune docker-export` | none | OCI layout in `dist/image/` |

## Settings that exist in two files

`.ko.yaml` (local builds) repeats these from `.goreleaser.yaml` (releases). A test fails when
they differ (R9).

| Setting | `.goreleaser.yaml` | `.ko.yaml` |
|---|---|---|
| Base image digest | `kos[0].base_image` | `defaultBaseImage` |
| Platforms | `kos[0].platforms` | `defaultPlatforms` |
| Build flags | `builds[rune].flags` | `builds[rune].flags` |
| Build environment | `builds[rune].env` | `builds[rune].env` |
| Main package | `builds[rune].main` | `builds[rune].main` |

Linker flags are compared without the `-X` stamps: a release stamps version and commit, a local
build does not.

## Order of one release run

```text
goreleaser release
  build binaries -> archives -> checksums -> archive SBOMs -> sign checksums
  ko: build both images on the pinned base -> push index as <version> (and latest if stable)
  cosign: sign index and both images
  publish GitHub release, Homebrew, Scoop
workflow
  attest binary provenance
  crane: resolve index digest
  attest image provenance on the index digest
  crane: tag <version>-amd64 and <version>-arm64
extension job (stable only), unchanged
```
