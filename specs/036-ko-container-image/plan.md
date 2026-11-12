# Implementation Plan: Build Rune's Container Image with ko

**Branch**: `036-ko-container-image` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/036-ko-container-image/spec.md`

## Summary

Close issue #73: Rune's container image is built and published by ko instead of Docker Buildx,
inside the same GoReleaser run, with no Docker daemon in the release.

The design rests on three measured facts ([research.md](research.md)):

1. **ko's binary is byte-identical** to a plain `go build` with the same flags, on both
   architectures (R7). The image keeps containing the release binary, and CI checks it on every
   pull request.
2. **ko cannot set a working directory, an environment variable or the entrypoint path**; it
   inherits the first two from the base image (R2). So Rune gets a small base image of its own,
   `ghcr.io/rune-task-runner/rune-base`: distroless static nonroot plus `/work`, a `/rune` link
   and `RUNE_CONTAINER=1`, built without a daemon and pinned by digest (R3).
3. **The recorded entrypoint becomes `/ko-app/rune`.** `/rune` keeps working through the link and
   every documented `docker run` line behaves as before (measured), but the configuration field
   differs. `spec.md` FR-002 was amended to accept this one change (R3).

GoReleaser's `kos` section imports the existing build settings, publishes the multi-platform
image under the version tag and, for stable releases, `latest`; the workflow adds the two
per-architecture tags with crane, and cosign signs the index and both images (R1, R4, R5). A
`.ko.yaml` serves local builds, guarded against drift by a test (R9). One script checks the image
contract locally, in CI and against published tags (R10).

## Technical Context

**Language/Version**: Go 1.27 for Rune itself (unchanged). The change is build configuration:
YAML (GoReleaser, ko, GitHub Actions), four POSIX shell scripts, one Go test.

**Primary Dependencies**: Build-time tools only, none linked into Rune: ko v0.19 (embedded in
GoReleaser for releases, installed by the contributor for local builds), GoReleaser v2.20 or newer
(`platforms` in `kos`), crane (go-containerregistry) for tags, digests, login and the base image,
cosign and syft as today. The drift test uses the YAML module already in the module graph.

**Storage**: N/A. Images live in GHCR: `ghcr.io/rune-task-runner/rune` (existing) and
`ghcr.io/rune-task-runner/rune-base` (new).

**Testing**: `go test` in Docker for the drift test. The image contract script and the binary
comparison need a container runtime, so they run as steps of the CI `release-dryrun` job and as
Runefile tasks (R10). Signature, provenance and SBOM retrieval are verified on a release in a fork.

**Target Platform**: Image for linux/amd64 and linux/arm64. Release run on GitHub-hosted Ubuntu.

**Project Type**: CLI tool; this feature touches only its packaging and release pipeline.

**Performance Goals**: None for users. The release image step no longer needs emulation or a
multi-platform builder (SC-010).

**Constraints**: Published image contract unchanged except the recorded entrypoint path (FR-001
to FR-010). One release run (FR-014). Build settings written once for the release, and the local
copy guarded by a test (FR-015). Nothing local or dry-run can push to production (FR-013, FR-028).
Non-container release assets unchanged (FR-023). Base image pinned by digest (FR-010). New prose
avoids the em dash (maintainer rule).

**Scale/Scope**: 1 release config section replaced, 1 workflow edited and 1 added, 1 CI job
extended, 1 new config file, 4 scripts, 1 test and its fixture, 3 Runefile tasks, 3 documents, 2 Dockerfiles
removed. No change to Rune's Go packages.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No change to Rune's behavior. |
| II. Errors are a feature | PASS | The local tasks name a missing tool and how to install it (FR-029). The contract script prints which field or fixture failed. |
| III. Minimal, total DSL | PASS | No DSL change. The new Runefile tasks use existing syntax. |
| IV. Hand-written front end, idiomatic Go | PASS | No Go package changes. One test is added. |
| V. Boringly portable | PASS | Still a static, CGO-free binary; the image binary is byte-identical to the archive binary (R7). |
| VI. Test-first, multi-layer verification | PASS | The drift test and the contract script are written first and fail against the current tree (no `.ko.yaml`; no ko image). Golden files are untouched. |
| VII. AI-native, secure by default | PASS | Keyless signing, provenance and bills of materials are kept for the image (R5, R6). The base image is pinned by digest and signed (R11). |
| VIII. Go engineering discipline | PASS | The one Go test follows existing conventions. The linter gate is unchanged. |
| Constraint: Docker-only testing | PASS | `go test` stays in the Docker harness. The contract script needs a runtime and runs in CI and as a Runefile task, not on the host test path (R10). |
| Constraint: locked package layout | PASS | Additive: `scripts/` gains four files, `test/` gains one package and one fixture. |
| Constraint: backward compatibility | PASS | Image name, tags, behavior and verification commands are unchanged; one configuration field differs and is recorded (R3). |
| Constraint: surface changes carry their docs | PASS | `docs/docker.md`, `docs/releasing.md` and `CONTRIBUTING.md` change in the same PR (FR-031). |
| Quality gates | PASS | All seven stay. Gate 7 (release dry run) now also builds the image and checks its contract and binary identity (R8). |

One cost and one accepted deviation, both in Complexity Tracking. No unjustified violation.

**Post-design re-check**: PASS. The contracts add one published image (the base), one
configuration file and three Runefile tasks. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/036-ko-container-image/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R14, with measurements
├── data-model.md        # Phase 1: images, tags, digests, release kinds, what attaches to what
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── image.md         # the published image: configuration, behavior, accepted differences
│   ├── release.md       # tags per release kind, signing, provenance, SBOM, assets, workflow order
│   └── local-workflow.md  # Runefile tasks, .ko.yaml, base image maintenance
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
.goreleaser.yaml                 # dockers + docker_manifests removed; kos added; docker_signs on manifests, recursive
.ko.yaml                         # new: base digest, platforms, the rune build, for local builds
Runefile                         # docker (ko, loads rune:local), docker-export, docker-check
Dockerfile                       # removed (last step)
Dockerfile.goreleaser            # removed (last step)
Dockerfile.test, docker-compose.yml, .dockerignore   # unchanged: test harness

scripts/
├── install.sh                   # unchanged
├── build-image.sh               # new: local ko build (load into Docker, or export an OCI layout);
│                                #   checks for go and ko, passes the labels; behind the Runefile tasks
├── build-base-image.sh          # new: distroless digest in, rune-base index out (crane, no daemon)
├── check-image.sh               # new: asserts contracts/image.md against an image reference
└── compare-image-binary.sh      # new: image binary against an archive binary, by checksum

.github/workflows/
├── release.yml                  # QEMU, Buildx, docker login removed; crane login; per-arch tags;
│                                #   digest via crane; image provenance unchanged in kind
├── base-image.yml               # new, manual: build, push and sign rune-base; print the digest
└── ci.yml                       # release-dryrun: stop skipping the image; run check-image.sh and
                                 #   the binary comparison on the snapshot image

test/image/fixture/Runefile      # new: the project the contract script mounts at /work

test/release/
└── config_test.go               # new: .ko.yaml and .goreleaser.yaml agree on base, platforms,
                                 #   flags and env; base is digest-pinned

docs/docker.md                   # build with ko; export; explicit publishing; limits; entrypoint note
docs/releasing.md                # image pipeline, verification, SBOM retrieval, base image update,
                                 #   recovery when the per-arch tag step fails
CONTRIBUTING.md                  # prerequisites (Go, ko), task table, file table
```

**Structure Decision**: Configuration and scripts only, in the places the repository already uses
(`scripts/`, `.github/workflows/`, `test/`). No Go package of Rune changes. The base image has its
own script and workflow because it is built rarely and by hand, while the product image is built
on every release by GoReleaser.

**Order of work**:

1. Drift test and contract script, failing.
2. Base image script; build the base into a local registry; `.ko.yaml`; Runefile tasks; contract
   script passes on a local ko image.
3. Publish the base image (maintainer runs the new workflow once), pin its digest.
4. `.goreleaser.yaml` (`kos`, signing); CI dry run builds the image, checks the contract and the
   binary identity.
5. Release workflow; a test release in a fork to verify tags, signatures, provenance and SBOM
   retrieval for stable and prerelease.
6. Docs, then remove the two Dockerfiles.
7. After the merge: run the base image workflow once to sign the pinned base, before any release.

## Complexity Tracking

| Item | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| A second published image, `rune-base`, with its own script, workflow and update procedure | ko cannot set `/work`, `RUNE_CONTAINER=1` or provide `/rune`; it only inherits them from the base (R2, R3) | A stock base breaks every documented `docker run` line and lets `--self-upgrade` run inside the container. Rewriting the image after the build changes digests GoReleaser already signed |
| The recorded entrypoint changes from `/rune` to `/ko-app/rune` (spec FR-002 amended) | ko fixes the entrypoint path; there is no setting for it | Restoring `/rune` in the configuration needs the post-build rewrite rejected above. Behavior is unchanged: `/rune` exists and every fixture passes (measured) |
