# Research: Build Rune's Container Image with ko

Decisions for [plan.md](plan.md). "Measured" means run on 2026-11-11 in a `golang:1.27-bookworm`
container (go1.27.4) with ko v0.19.1 and crane from go-containerregistry, against a throwaway local
registry. Nothing was pushed to GHCR.

## R1. Use ko through GoReleaser's `kos` section

**Decision**: Replace the `dockers` and `docker_manifests` sections of `.goreleaser.yaml` with one
`kos` entry that imports the existing `rune` build (`build: rune`). The image is built and pushed
inside the same `goreleaser release` run as everything else.

**Rationale**: FR-014 (one release run) and FR-015 (one place for build settings). `kos` takes
`main`, `flags`, `ldflags` and `env` from the build it names, so `-trimpath`, the `-X` stamps and
`CGO_ENABLED=0` are written once. A draft configuration passed `goreleaser check` on v2.20.0
(measured).

**Version support (FR-016)**: `base_image` needs GoReleaser v2.13, `platforms` needs v2.20. The
workflows request `~> v2`, which resolves to v2.20.1 today (latest stable, 2026-11-11). An older
GoReleaser rejects the unknown `platforms` key in `goreleaser check`, which is already a CI gate,
so a too-old version fails before a release, not during one.

**Alternatives considered**: Run `ko build` as a separate workflow step after GoReleaser (two
places for flags, signing wired by hand, image no longer part of the release artifact list). Keep
Buildx (does not meet the issue).

## R2. What ko produces by default (measured)

| Field | ko image on a stock base | Current image |
|---|---|---|
| Entrypoint | `/ko-app/rune` | `/rune` |
| Cmd | none | none |
| WorkingDir | inherited from the base (`/home/nonroot` on distroless nonroot, unset on Chainguard static) | `/work` |
| User | inherited (`65532`) | `65532` |
| Env | base's, plus `/ko-app` appended to `PATH`, plus `KO_DATA_PATH=/var/run/ko` | base's |
| Labels | base's, plus those passed in | the six set at build |
| Author | `github.com/ko-build/ko` | unset |
| Layers | base, an empty kodata layer, the binary | base, the binary |

ko has no setting for the entrypoint path, the working directory, or image environment variables.
It keeps whatever the base image's configuration says for WorkingDir, Env and User. So everything
Rune's contract needs beyond the binary has to come from the base image.

## R3. A Rune base image carries `/work`, `/rune` and the container marker

**Decision**: Publish a small base image, `ghcr.io/rune-task-runner/rune-base`, and build Rune's
image on it. It is `gcr.io/distroless/static-debian12:nonroot` (pinned by digest) plus:

- one layer holding the directory `/work` and the symbolic link `/rune -> /ko-app/rune`;
- configuration `WorkingDir=/work` and `Env += RUNE_CONTAINER=1`.

It is built without a Docker daemon by a script that uses `crane append`, `crane mutate` and
`crane index append` for both architectures. `.goreleaser.yaml` and `.ko.yaml` reference it by
digest (FR-010).

**Measured**: A ko image built on such a base has `WorkingDir=/work`, `User=65532`,
`RUNE_CONTAINER=1`, and Entrypoint `/ko-app/rune`. Against a mounted project: `--version`,
`--list`, a task with an argument (exit 0), a failing task (exit 7), `--entrypoint /rune`,
running with no mount, and `--self-upgrade` (declines, exit 1) all behave as today's image does.

**The one accepted configuration change**: the recorded Entrypoint is `/ko-app/rune`, not `/rune`.
`/rune` still works because of the link. `spec.md` FR-002 and SC-002 were amended to say this.
Also visible to someone who inspects the image, and accepted: `PATH` ends with `/ko-app`,
`KO_DATA_PATH` is set, the author field names ko, and there is one extra empty layer.

**Alternatives considered**:

- Rewrite the entrypoint after the build with `crane mutate`. Rejected: it changes the digests
  after GoReleaser has signed them, so signing, tagging and provenance would all move out of
  GoReleaser into hand-written steps, which is the drift R1 avoids.
- Build the base with a Dockerfile. Works, but brings back a daemon for one step. Kept as the
  fallback if the crane script proves fragile.
- Use a stock base and drop `/work` and `/rune`. Breaks every documented `docker run` line.

**Cost**: a second published image and a short maintenance procedure (R11). Recorded in Complexity
Tracking.

## R4. Tags

**Decision**: `kos.tags` is `{{ .Version }}` and `{{ if not .Prerelease }}latest{{ end }}`; ko
ignores an empty tag, so a prerelease gets no `latest` (FR-012). `bare: true` publishes to the
repository path with no suffix (FR-001). `platforms` is `linux/amd64`, `linux/arm64` (FR-009).

ko publishes one multi-platform index and no per-architecture tags. The release workflow adds
them after GoReleaser: for each architecture, `crane digest --platform linux/<arch>` on the version
tag, then `crane tag` that digest as `<version>-<arch>` (FR-011). The per-architecture tag
therefore names the very image the index lists, not a second build.

**To verify during implementation**: that the empty-tag rule holds for a prerelease in a snapshot
run, and what GoReleaser's own `.Prerelease` is for `-rc.N` tags (the current config relies on the
same automatic rule for `latest`).

**Alternatives considered**: three `kos` entries, one per tag shape (three builds, three sets of
digests). Dropping the per-architecture tags (changes what consumers can pull).

## R5. Signing and provenance

**Decision**: `docker_signs` switches from `artifacts: all` to `artifacts: manifests` (what the ko
pipe registers) and adds `--recursive`, so cosign signs the index and both per-architecture
images in one call (FR-019). Provenance stays a workflow step: resolve the index digest with
`crane digest`, pass it to `actions/attest-build-provenance` with `push-to-registry: true`
(FR-020). This replaces `docker buildx imagetools inspect`.

**To verify during implementation**: the documented `cosign verify` and `gh attestation verify`
commands succeed against the index digest and both per-architecture digests of a test release in
a fork (SC-005).

## R6. Image bill of materials

**Decision**: Keep ko's default `sbom: spdx`. ko generates SPDX for the index and for each
architecture and uploads them to the registry next to the image (measured: three documents are
produced). No file is added to the GitHub release, so the list of release assets is unchanged
(SC-009). `docs/releasing.md` documents the retrieval command.

**To verify during implementation**: the exact retrieval command for ko v0.19's storage form
(`cosign download sbom` against the digest, or an OCI referrers query), tested against a fork
release before it is written into the docs (FR-021).

## R7. Binary identity (measured)

With the same flags (`-trimpath`, `-s -w`, the two `-X` stamps, `CGO_ENABLED=0`) and the same
toolchain, the binary inside the ko image is byte-identical to a plain `go build`:

| Architecture | SHA-256 of `go build` output | SHA-256 of `/ko-app/rune` in the image |
|---|---|---|
| amd64 | `71c64470c7b7...4894706` | `71c64470c7b7...4894706` |
| arm64 | `f6217362ef13...9c3b3ba` | `f6217362ef13...9c3b3ba` |

**Decision**: Identity is kept (FR-024, FR-025), and it is checked on every pull request, not
once: the CI release dry run compares the binary in the snapshot image with the Linux binary
GoReleaser put in `dist/` and fails on a difference. The docs keep saying the image contains the
release binary.

**Caveat**: the measurement used ko's CLI and `go build`. In a release the comparison is between
GoReleaser's build and GoReleaser's embedded ko, both using the runner's Go. The CI check is what
proves that pairing; the first run of it is the recorded result FR-024 asks for.

## R8. Snapshots and dry runs

**Decision**: In snapshot mode GoReleaser's ko pipe publishes to `goreleaser.ko.local`, which
means "load into the local Docker daemon", never a registry (FR-013). The CI dry run stops
skipping the image (`--skip=sign,sbom` instead of `--skip=sign,sbom,docker`) and then runs the
image contract check (R10) and the binary comparison (R7) on the loaded image. CI runners have a
daemon; FR-017 is about the release run, which does not use one.

**To verify during implementation**: which platforms a snapshot loads (a local daemon holds one
platform). If only the host platform loads, CI checks amd64 and the arm64 result stays the
measurement in R7 plus the fork release.

## R9. Local workflow

**Decision**: A `.ko.yaml` at the repository root holds the base image digest, the two platforms
and the `rune` build (`./cmd/rune`, `CGO_ENABLED=0`, `-trimpath`, `-s -w`). Runefile tasks:

- `rune docker`: builds with ko and loads into the local daemon as `rune:local` (the name used
  today). Publishes nothing.
- `rune docker-export`: builds and writes an OCI layout under `dist/image/`, with no daemon and no
  registry (`--push=false --oci-layout-path`; measured to work with no daemon).
- `rune docker-check`: runs the image contract check (R10) against `rune:local`.

Each task first checks that `go` and `ko` are on `PATH` and otherwise prints what is missing and
the install command (FR-029). Publishing to a registry is not a task: the docs show the explicit
`KO_DOCKER_REPO=<your registry> ko build --bare ./cmd/rune` command (FR-028).

A local build is not version-stamped (it reports `dev`), like today's `docker buildx build .`
without build arguments. The docs show how to pass `--ldflags` for a stamped local image.

**Drift guard (FR-015)**: `.ko.yaml` necessarily repeats four values from `.goreleaser.yaml`: the
base image digest, the platforms, the flags and the environment. A Go test parses both files and
fails if they differ, so the two cannot drift silently.

**Alternatives considered**: build locally with `goreleaser release --snapshot` (one source of
settings, but needs GoReleaser and builds all six platforms for a local try).

## R10. Image contract check

**Decision**: One script, `scripts/check-image.sh <image-ref>`, asserts the contract in
[contracts/image.md](contracts/image.md): configuration fields, then the mounted-project fixtures
(`--version`, `--list`, a built-in-shell task with an argument, a failing task's exit code,
`--entrypoint /rune`, `--self-upgrade` declining). It runs in CI after the snapshot, locally via
`rune docker-check`, and by hand against a published tag. It needs a container runtime, so it is a
CI job step and a Runefile task, not part of `go test` (the test harness has no runtime inside).

## R11. Base image maintenance

**Decision**: `scripts/build-base-image.sh` builds the base (R3) from a distroless digest given as
an argument and prints the resulting digest. A manually started workflow, `base-image.yml`, runs
it on `main`, pushes to `ghcr.io/rune-task-runner/rune-base`, signs the digest with cosign, and
prints it. The maintainer then updates the digest in `.goreleaser.yaml` and `.ko.yaml` in a pull
request; the drift test (R9) fails if only one is changed. `docs/releasing.md` documents the
steps (FR-032).

The first base image is published by hand, because a manually started workflow can run only once
it is on `main`. The script is deterministic, so running the workflow after the merge with the
same distroless digest produces the same digest and adds the signature. No release is cut before
that.

The base package must be public on GHCR so anonymous builds can pull it; this is a one-time
maintainer action, listed in the quickstart.

## R12. Registry login and tools in the release run

**Decision**: The release workflow drops QEMU, Buildx and `docker/login-action`. It installs crane
(`imjasonh/setup-crane`) and logs in with `crane auth login ghcr.io`, which writes the credential
file ko, crane and cosign all read, with no daemon (FR-017, SC-010). cosign and syft stay as they
are (syft still makes the per-archive bills of materials).

**To verify during implementation**: that GoReleaser's ko pipe picks up that credential file on
the runner.

## R13. What is removed, and when

**Decision**: `Dockerfile` and `Dockerfile.goreleaser` are deleted in the same change, as its last
step, after the contract check and the binary comparison pass on the ko image (FR-033).
`Dockerfile.test`, `docker-compose.yml` and `.dockerignore` stay: they belong to the test harness.

## R14. Failure and rerun behavior

- A failed ko build or push fails `goreleaser release`, so the run is red (FR-018).
- The per-architecture tag step runs after GoReleaser. If it fails, archives and the version tag
  are already published; the step is safe to rerun because it only points tags at existing
  digests. `docs/releasing.md` gets this recovery note.
- Rerunning a release for the same version rebuilds the image. With `creation_time` set from the
  commit and identical inputs the digest is expected to repeat; signatures and provenance are
  always made for the digest the rerun publishes.
- A base digest that no longer resolves fails the build with ko's "cannot fetch base image" error
  naming it; there is no unpinned fallback.
