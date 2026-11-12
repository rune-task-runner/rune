# Baseline and Recorded Results

Measurements for [tasks.md](tasks.md), taken on 2026-11-11 on linux/amd64. Tools: ko v0.19.1,
GoReleaser v2.20.0 (go1.27.3 inside its image), crane from go-containerregistry, Docker for
loading and running. A throwaway `registry:2` on `127.0.0.1:5005` stood in for GHCR. Nothing was
pushed to GHCR.

## Today's image (T002)

`ghcr.io/rune-task-runner/rune:latest`, which is v0.7.0:

| Field | Value |
|---|---|
| User | `65532` |
| WorkingDir | `/work` |
| Entrypoint | `["/rune"]` |
| Cmd | none |
| Env | `PATH=...`, `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt` |
| Labels | description, licenses, revision, source, title, version |
| Media types | Docker manifest list v2 with two Docker v2 manifests |

`scripts/check-image.sh` on it fails 6 of 32 checks, as intended: the Entrypoint, the two layout
files (`/ko-app/rune`, the `/rune` link), and three that only exist since #84 (`RUNE_CONTAINER`
and the `--self-upgrade` answer). Nothing else fails.

## Release file list (T002, T020)

`goreleaser release --snapshot --clean` lists the same 18 files in `dist/` before and after the
change (`diff` of the sorted lists is empty): 6 archives, 6 binaries, `checksums.txt`,
`artifacts.json`, `config.yaml`, `metadata.json`, the Homebrew cask and the Scoop manifest.

## Base image (T007)

| Item | Value |
|---|---|
| Distroless digest used | `sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab` (`gcr.io/distroless/static-debian12:nonroot` on 2026-11-11) |
| Resulting base index digest | `sha256:13f17425edcae59a09595d5b9280e0bfae0d0f52f34a8071bed8681a5ce10101` |
| Deterministic | yes: two runs of `scripts/build-base-image.sh` printed the same digest |
| Configuration, both architectures | WorkingDir `/work`, User `65532`, `RUNE_CONTAINER=1` |

The digest depends only on the distroless digest and the script, not on the registry, so the
same value is expected when the base is published to GHCR (T012).

## Snapshot through GoReleaser (T020, T021)

| Check | Result |
|---|---|
| Image loaded locally | `goreleaser.ko.local:0.7.1-next` (and `:latest`, locally only) |
| Platforms a snapshot loads | the host's only (amd64 here) |
| Pushes to `ghcr.io` | none (no occurrence in the log) |
| `scripts/check-image.sh <image> 0.7.1-next` | `OK 32 checks` |
| Labels | all six, version `0.7.1-next`, revision the full commit |
| Created time | the commit's time |
| ko's bill of materials with `--skip=sbom` | not determined: the snapshot log shows no bill of materials step for the image and a snapshot pushes nothing, so there is no registry to look in. The fork release (T027) settles it |
| Prerelease (`GORELEASER_CURRENT_TAG=v0.8.0-rc.1`) | image tagged with the version only; no `latest` tag produced. The empty-tag rule holds |
| Missing base (digest of 64 zeros) | run fails, exit 1: `build: fetching base image: ... MANIFEST_UNKNOWN`; no image loaded |

## Binary identity (T037, FR-024)

Compared with `scripts/compare-image-binary.sh`: the binary GoReleaser put in `dist/` against
`/ko-app/rune` in the image GoReleaser's ko step built in the same run.

| Architecture | Archive binary SHA-256 | Image binary SHA-256 | Result |
|---|---|---|---|
| amd64 | `855735acc2e60ff7b5d8d567fe1cf4a5e972e922a2b1ec07c5545a94c2619e18` | same | identical |
| arm64 | `0fec5310b926fb908f90255eec08e8fbdb206e7e1712894232e5848659fafed3` | same | identical |

The arm64 run used a copy of the configuration with only `linux/arm64` in `kos.platforms`, so the
snapshot would load that image. The script reports a difference when given the wrong
architecture's binary, so it does detect one.

A binary built by a different Go patch release differs (go1.27.4 against GoReleaser's go1.27.3),
which is expected: identity holds within one release run, where one toolchain builds both.

## Per-architecture tags and bill of materials (T024, T028, local registry)

- `crane digest --platform linux/<arch> <repo>:<version>` then `crane tag` gives
  `<version>-<arch>` the same digest as the index entry (checked for both).
- ko stores the SPDX documents as tags named `sha256-<digest>.sbom`, one for the index and one
  per image. Both retrieval commands in `docs/releasing.md` return the SPDX document:
  `crane manifest` plus `crane blob`, and `cosign download sbom` (cosign v3.1.4 prints a
  deprecation warning for the latter but works).
- These `.sbom` tags appear in the repository's tag list next to the version tags.

## Local workflow (T031 to T034)

| Check | Result |
|---|---|
| `rune docker` | loads `rune:local`; `scripts/check-image.sh rune:local dev` prints `OK 32 checks` |
| `rune docker-export` with no Docker daemon | writes `dist/image` (OCI layout, both architectures) |
| ko missing | `ko is not installed. Install it with: go install github.com/google/ko@latest`, non-zero exit |
| Go missing | `go is not installed. Install Go 1.27 or newer: https://go.dev/dl/`, non-zero exit |
| Docker missing for `rune docker` | says so and points to `rune docker-export` |
| Registry writes during local builds | 0 `PUT`/`POST`/`PATCH` requests in the test registry's log, with credentials for it present; only `GET` for the base |

A local image is a development build, so inside it `--self-upgrade` declines as a development
build rather than with the container message; the contract script accepts that for version `dev`.

## Guide examples, old image against new (T046, SC-001)

The four `docker run` examples of `docs/docker.md` plus two failing invocations, on a small
project, against `ghcr.io/rune-task-runner/rune:latest` (v0.7.0) and the snapshot ko image:
standard output, standard error and exit codes are identical for all six.

## Media types (T016)

The ko image is an OCI index with OCI manifests; today's is a Docker manifest list. `docker pull`
and `docker run` work on the ko image from a registry, and `docker buildx imagetools inspect`
reads OCI indexes.

## Base image on GHCR (T012, T013)

Published on 2026-11-11 as
`ghcr.io/rune-task-runner/rune-base@sha256:13f17425edcae59a09595d5b9280e0bfae0d0f52f34a8071bed8681a5ce10101`,
the digest the local builds predicted. Public since 2026-11-12: an anonymous `crane digest`
resolves it, and `rune docker-export` succeeds with no registry credentials.

## Not verified yet (needs the maintainer)

| Item | Why | Task |
|---|---|---|
| Tags on GHCR for a stable release and a prerelease | needs a real release run in a fork | T027 |
| `cosign verify` on the three digests; `gh attestation verify` | needs keyless signing in GitHub Actions | T027 |
| Bill of materials retrieval from GHCR | same; the commands are proven against the local registry | T028 |
| GitHub release asset list | same | T029 |
| ko picks up the `crane auth login` credential in the workflow | same | T030 |
| Signature on the base image | the workflow runs only from `main` | T049 |
