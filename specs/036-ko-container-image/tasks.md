# Tasks: Build Rune's Container Image with ko

**Input**: Design documents from `/specs/036-ko-container-image/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first) and the issue's acceptance criteria
ask for checks of the image, the tags and the binary. The checks here are one Go test, one
contract script, and CI steps; each is written before the thing it checks and seen to fail.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: the user story the task serves (US1 to US6)

## Conventions for every task

- Go tests run in Docker, never on the host:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`.
- ko, crane and GoReleaser are not installed on the host. Run them in a `golang:1.27-bookworm`
  container (`go install github.com/google/ko@v0.19.1`,
  `go install github.com/google/go-containerregistry/cmd/crane@latest`) or the
  `goreleaser/goreleaser` image, as the prototype in research.md did. A throwaway `registry:2`
  container on `127.0.0.1` stands in for GHCR in local checks.
- Nothing in these tasks pushes to `ghcr.io/rune-task-runner/rune`. The only push to GHCR is the
  base image in T012, which is a maintainer-approved step.
- Shell scripts are POSIX `sh`, start with `set -eu`, and pass `shellcheck` if it is available.
- No em dash in any new text. Comments say why, not what.
- Tasks marked **Maintainer** change outside state (GHCR, a fork). They need the maintainer's
  approval and, per the maintainer's rules, a saved script in `.docs/scripts/` shown first.
- Commit only when the maintainer asks.

## Phase order

Setup, then Foundational (base image, local ko config, the two checks), then the stories. US1 is
validated on a locally built image, so it comes first and is the MVP. US2 and US3 change the
release. US4, US5 and US6 follow in priority order.

---

## Phase 1: Setup

**Purpose**: A known baseline to compare against.

- [X] T001 Confirm the branch is `036-ko-container-image` and the tree is clean apart from
  `specs/036-ko-container-image/`. Run the Go tests in Docker and record that they pass.
- [X] T002 Record the baseline in `specs/036-ko-container-image/baseline.md`: (a) the
  configuration of today's image from `docker inspect ghcr.io/rune-task-runner/rune:latest`
  (User, WorkingDir, Entrypoint, Cmd, Env, Labels) and its manifest media types from
  `docker buildx imagetools inspect --raw`; (b) the sorted file list of `dist/` from
  `goreleaser release --snapshot --clean --skip=sign,sbom,docker` run in the `goreleaser/goreleaser`
  image on a copy of the tree. These are what T020 and T029 compare against.

---

## Phase 2: Foundational (base image, local ko config, checks)

**Purpose**: Everything a ko image of Rune needs before any story can be checked.

**CRITICAL**: No story work starts until T011 passes.

### Checks first (write, watch them fail)

- [X] T003 [P] Create `test/release/config_test.go` (package `release_test`). It reads
  `../../.ko.yaml` and `../../.goreleaser.yaml` with the YAML module already in `go.mod` and has
  these tests, per data-model.md "Settings that exist in two files":
  - `TestBaseImagePinned`: `.ko.yaml` `defaultBaseImage` and `.goreleaser.yaml` `kos[0].base_image`
    are equal, start with `ghcr.io/rune-task-runner/rune-base@sha256:` and end with 64 hex digits.
  - `TestPlatformsAgree`: `defaultPlatforms` and `kos[0].platforms` are both exactly
    `linux/amd64`, `linux/arm64`.
  - `TestBuildSettingsAgree`: for the build with `id: rune` in each file, `main`, `flags` and `env`
    are equal, and `ldflags` are equal after removing every `-X name=value` word.
  - `TestKoImageSettings`: `kos[0]` has `build: rune`, `bare: true`, `sbom: spdx`,
    `repositories` equal to `[ghcr.io/rune-task-runner/rune]`, `tags` equal to
    `["{{ .Version }}", "{{ if not .Prerelease }}latest{{ end }}"]`, and the six labels of
    contracts/image.md with the literal values for title, description, source and licenses.
  - `TestNoBuildxSections`: `.goreleaser.yaml` has no `dockers` and no `docker_manifests` key, and
    `docker_signs[0].artifacts` is `manifests` with `--recursive` among its `args`.
  Run it: it fails because `.ko.yaml` does not exist. `go mod tidy` if the YAML module moves from
  indirect to direct.
- [X] T004 [P] Create the fixture `test/image/fixture/Runefile` with two tasks that use only the
  built-in shell: `hello name="world"` that echoes `hi {{name}}`, and `fail` that runs `exit 7`.
- [X] T005 Create `scripts/check-image.sh <image-ref> [expected-version]` per
  contracts/local-workflow.md and contracts/image.md. It uses `docker inspect` (pulling the
  reference first if it is not local) and `docker run`, and checks:
  - configuration: WorkingDir `/work`; User `65532`; Entrypoint has exactly one element and it is
    `/ko-app/rune`; Cmd empty; Env contains `RUNE_CONTAINER=1` and an `SSL_CERT_FILE=` entry; the
    labels title `rune`, description `A shared task runner for humans and AI agents`, source
    `https://github.com/rune-task-runner/rune`, licenses `MIT`; labels version and revision are
    non-empty, and version equals `expected-version` when given;
  - fixtures, with `test/image/fixture` mounted at `/work`: `--version` exits 0 and prints
    `rune version`; `--list` exits 0 and lists `hello` and `fail`; `hello there` exits 0 and
    prints `hi there`; `fail` exits 7; `--entrypoint /rune ... --version` exits 0; with no mount
    `--list` exits non-zero and prints `no Runefile found`; `--self-upgrade` exits 1 and prints
    `docker pull`.
  Every failed check prints `FAIL <check>: got <value>, want <value>`; the script runs all checks
  and exits 1 if any failed, else prints `OK <n> checks` and exits 0. It never pushes or tags.
  Run it against `ghcr.io/rune-task-runner/rune:latest`: it must fail the Entrypoint check,
  proving the script detects the layout difference. An image older than #84 also fails the
  `RUNE_CONTAINER` and `--self-upgrade` checks; nothing else may fail.

### Implementation

- [X] T006 Create `scripts/build-base-image.sh <distroless-digest> <destination-ref>` per R3 and
  contracts/local-workflow.md. Steps: make a temporary directory holding `work/` (mode 0755) and
  the link `rune -> /ko-app/rune`; tar it with `--owner=0 --group=0 --numeric-owner --mtime=@0
  --sort=name`; for `amd64` and `arm64` run `crane append --platform linux/<arch> -b
  gcr.io/distroless/static-debian12@<distroless-digest> -f <layer.tar> -t <destination>-<arch>`
  then `crane mutate --platform linux/<arch> --workdir /work --env RUNE_CONTAINER=1 -t
  <destination>-<arch> <destination>-<arch>`; then `crane index append -m <destination>-amd64 -m
  <destination>-arm64 -t <destination>`; print `crane digest <destination>` as the last line of
  standard output. Accept `CRANE_FLAGS` from the environment (for `--insecure` against a local
  registry). Fail with a usage message when an argument is missing or the digest does not start
  with `sha256:`, and with "crane is not installed" plus the install command when crane is absent.
- [X] T007 Run `scripts/build-base-image.sh` twice against a local `registry:2` with the current
  digest of `gcr.io/distroless/static-debian12:nonroot` (resolve it with `crane digest`). Confirm
  both runs print the same digest (deterministic), and that `crane config` of each architecture
  shows WorkingDir `/work`, `RUNE_CONTAINER=1` in Env and User `65532`. Record the distroless
  digest and the resulting base digest in `specs/036-ko-container-image/baseline.md`.
- [X] T008 Create `.ko.yaml` per contracts/local-workflow.md: `defaultBaseImage` (for now the
  digest reference of the local-registry base from T007, replaced in T012), `defaultPlatforms`
  `linux/amd64` and `linux/arm64`, and one build `id: rune`, `main: ./cmd/rune`,
  `env: [CGO_ENABLED=0]`, `flags: [-trimpath]`, `ldflags: [-s -w]`. Add a comment on top saying it
  serves local builds, that releases use `.goreleaser.yaml`, and that `test/release` keeps the two
  in step.
- [X] T009 In `Runefile` replace the `docker` task and add two tasks, per
  contracts/local-workflow.md. Each starts with a check that prints, when the tool is missing,
  `go is not installed. Install Go 1.27 or newer: https://go.dev/dl/` or
  `ko is not installed. Install it with: go install github.com/google/ko@latest` and exits 1.
  - `docker`: `KO_DOCKER_REPO=ko.local/rune ko build --bare --local --tags local <labels>
    ./cmd/rune`, then `docker tag ko.local/rune:local rune:local`. Comment: builds the image with
    ko and loads it as `rune:local`; pushes nothing.
  - `docker-export`: `KO_DOCKER_REPO=ko.local/rune ko build --bare --push=false
    --oci-layout-path dist/image <labels> ./cmd/rune`. Comment: writes an OCI layout, needs no
    Docker daemon.
  `<labels>` is one `--image-label` argument carrying the six labels of contracts/image.md,
  because `.ko.yaml` cannot hold labels: title `rune`, the description, source and licenses with
  the literal values of T017, version `dev`, and revision from `git rev-parse HEAD` (`none` when
  Git is unavailable). Define the label list once in the Runefile and use it in both tasks.
  For the tool check use a construct Rune's built-in shell supports (try `command -v ko`); if it
  supports none, put the check in a small `scripts/require-tool.sh <tool>` and call that.
  - `docker-check`: `scripts/check-image.sh rune:local`.
  Confirm `go run ./cmd/rune --list` still parses the Runefile and lists the three tasks.
- [X] T010 Build the image locally the way `rune docker` does (ko in a container, pushing to the
  local registry instead of `--local` if the container has no daemon socket, then `docker pull`
  and tag `rune:local`).
- [X] T011 Run `scripts/check-image.sh rune:local`. It must print `OK` and exit 0. Run the drift
  test: `TestBaseImagePinned` still fails (local registry reference, no `kos` yet); the others
  that only need `.ko.yaml` now load it.

**Checkpoint**: A ko image of Rune built on Rune's own base passes the image contract locally.

---

## Phase 3: User Story 1: The published image behaves exactly as before (Priority: P1) MVP

**Goal**: The ko image keeps the contract in contracts/image.md, with the base image published
and pinned so anyone can build it.

**Independent Test**: `scripts/check-image.sh` exits 0 on an image built from a clean checkout with
only `.ko.yaml` and the public base.

- [X] T012 [US1] **Maintainer.** Publish the base image: write
  `.docs/scripts/2026-11-11-publish-rune-base.sh` that logs crane in to GHCR and runs
  `scripts/build-base-image.sh <distroless-digest from T007> ghcr.io/rune-task-runner/rune-base:<first 12 hex of the distroless digest>`,
  skipping the push when that tag already resolves. Show it, wait for approval, run it, then ask
  the maintainer to set the `rune-base` package to public. Confirm the printed digest equals the
  one recorded in T007 (same inputs, same digest).
- [X] T013 [US1] Put `ghcr.io/rune-task-runner/rune-base@sha256:<digest from T012>` in `.ko.yaml`
  `defaultBaseImage`. Confirm an anonymous `crane manifest` of that reference succeeds.
- [X] T014 [US1] Rebuild `rune:local` from the public base (as T010) and run
  `scripts/check-image.sh rune:local`: exit 0.
- [X] T015 [P] [US1] Check the certificate scenario (spec US1 scenario 8): in the image, confirm
  the file named by `SSL_CERT_FILE` exists (`docker run --entrypoint /rune` cannot list files, so
  export the image with `crane export` and look for the path in the tar listing). Add this as a
  check in `scripts/check-image.sh` using `docker export` of a created container.
- [X] T016 [P] [US1] Compare media types: `crane manifest` of the ko index and of its two images
  against the baseline from T002. Record any difference in
  `specs/036-ko-container-image/baseline.md` and confirm `docker pull` and
  `docker buildx imagetools inspect` work on the ko image in the local registry.

**Checkpoint**: MVP. The image contract holds on a ko image built from the pinned public base.

---

## Phase 4: User Story 2: A release publishes the image under the same names (Priority: P1)

**Goal**: GoReleaser builds and publishes the image through ko in the one release run, with the
same tags, and dry runs publish nothing.

**Independent Test**: `go test ./test/release/...` passes, `goreleaser check` passes, and a
snapshot run loads an image that passes the contract script while `dist/` lists the same
non-image files as the baseline.

- [X] T017 [US2] In `.goreleaser.yaml` delete the `dockers` and `docker_manifests` sections and
  their comment block, and add a `kos` section per contracts/release.md: `id: rune`,
  `build: rune`, `base_image` (the digest from T013), `repositories:
  [ghcr.io/rune-task-runner/rune]`, `platforms: [linux/amd64, linux/arm64]`, `tags:
  ["{{ .Version }}", "{{ if not .Prerelease }}latest{{ end }}"]`, `bare: true`, `sbom: spdx`,
  `creation_time: "{{ .CommitTimestamp }}"`, and `labels` with the six keys: title
  `{{ .ProjectName }}`, description `A shared task runner for humans and AI agents`, version
  `{{ .Version }}`, revision `{{ .FullCommit }}`, source
  `https://github.com/rune-task-runner/rune`, licenses `MIT`. Comment above it: built by ko from
  the `rune` build's settings, no Docker daemon; `/work`, `/rune` and `RUNE_CONTAINER` come from
  the base image; the per-architecture tags are added by the release workflow.
- [X] T018 [US2] In `.goreleaser.yaml` change `docker_signs[0]` to `artifacts: manifests` and add
  `"--recursive"` after `sign` in `args`, with a comment that this signs the index and both
  images.
- [X] T019 [US2] Run `goreleaser check` (in the `goreleaser/goreleaser` image) and the drift test:
  all five tests in `test/release` pass.
- [X] T020 [US2] Run `goreleaser release --snapshot --clean --skip=sign,sbom` in the
  `goreleaser/goreleaser` image with the Docker socket mounted, on a copy of the tree. Confirm:
  (a) an image appears locally under `goreleaser.ko.local`; (b) nothing was pushed (the run's log
  shows no push to `ghcr.io`); (c) the sorted `dist/` file list equals the baseline from T002
  apart from image-related entries; (d) `scripts/check-image.sh <that image> <snapshot version>`
  exits 0. Record in `baseline.md` which platforms the snapshot loaded (R8) and whether
  `--skip=sbom` also turned off ko's own bill of materials. Then (e), for FR-018 and the pinned
  base edge case: on a second copy, change `kos[0].base_image` to a digest that does not exist
  (64 zeros) and rerun; the run must exit non-zero with an error that names the base image, and
  no image may be loaded.
- [X] T021 [US2] Check the prerelease rule without publishing: run the snapshot again with a
  prerelease version (create a temporary local tag `v0.0.0-rc.1` on a copy of the tree, or use
  `GORELEASER_CURRENT_TAG`), and confirm from the log and the loaded image list that no `latest`
  tag is produced, while a stable version produces one. Record the result in `baseline.md`. If the
  empty tag is not ignored, switch `tags` to a form GoReleaser documents for this and update
  `TestKoImageSettings` and contracts/release.md to match.
- [X] T022 [US2] In `.github/workflows/ci.yml`, job `release-dryrun`: change the snapshot step's
  args to `release --snapshot --clean --skip=sign,sbom`, update the comment above it (the image is
  now built by ko into the runner's local daemon and never pushed), and add a step
  "Check image contract" that finds the `goreleaser.ko.local` image with `docker images` and runs
  `scripts/check-image.sh` on it.
- [X] T023 [US2] In `.github/workflows/release.yml`: remove the "Set up QEMU", "Set up Buildx" and
  "Log in to GHCR" (`docker/login-action`) steps; add "Install crane"
  (`imjasonh/setup-crane@v0.4`, pinned to a released version found at implementation time) and
  "Log in to GHCR" running `crane auth login ghcr.io -u "${{ github.actor }}" --password-stdin`
  with the token on standard input. Keep cosign and syft. Update the job's comments that mention
  Docker.
- [X] T024 [US2] In `.github/workflows/release.yml` replace the "Resolve published image digest"
  step: compute `ver` as today, then `digest=$(crane digest "ghcr.io/rune-task-runner/rune:${ver}")`
  and write it to `GITHUB_OUTPUT`. Add a step "Tag per-architecture images" before the image
  attestation: for `arch` in `amd64 arm64`, `d=$(crane digest --platform "linux/${arch}"
  "ghcr.io/rune-task-runner/rune:${ver}")` then `crane tag
  "ghcr.io/rune-task-runner/rune@${d}" "${ver}-${arch}"`. Comment: ko publishes one index; these
  tags name the images that index lists, and the step is safe to rerun.
- [X] T025 [US2] Confirm the workflow files are valid: `actionlint` if available (run from its
  Docker image), otherwise parse both with a YAML loader. Confirm `grep -n "buildx\|qemu\|docker/"
  .github/workflows/release.yml` finds nothing.

**Checkpoint**: The release configuration builds the image with ko; dry runs load it locally and
push nothing.

---

## Phase 5: User Story 3: The image can be verified exactly as before (Priority: P1)

**Goal**: Signatures on all three digests, provenance on the index, and a retrievable bill of
materials, proven on a real release in a fork.

**Independent Test**: The table in quickstart.md section 4 passes in a fork.

- [X] T026 [US3] Add `.github/workflows/base-image.yml`: `workflow_dispatch` with an input
  `distroless_digest`; runs only on `main`; permissions `packages: write`, `id-token: write`;
  steps: checkout, install crane and cosign, `crane auth login`, run
  `scripts/build-base-image.sh "$digest" ghcr.io/rune-task-runner/rune-base:<first 12 hex>`,
  `cosign sign --yes ghcr.io/rune-task-runner/rune-base@<printed digest> --recursive`, and write
  the digest to the job summary with the two files to update.
- [X] T027 [US3] **Maintainer.** Fork verification, part 1. In a fork (or a scratch repository),
  with `repositories` in `.goreleaser.yaml` and the image name in `release.yml` pointed at the
  fork's GHCR path on a throwaway branch, cut `v0.0.1-rc.1` then `v0.0.1`. The fork has none of
  the publishing secrets, so on that branch also: set `skip_upload: true` on the Homebrew cask
  and the Scoop manifest in `.goreleaser.yaml`, drop `TAP_GITHUB_TOKEN` from the GoReleaser step,
  and add `if: false` to the `publish-extension` job. Only `GITHUB_TOKEN` (packages, id-token and
  attestations permissions, as on `main`) is needed. None of these branch edits is merged. Record in
  `baseline.md` the outcome of each row of quickstart.md section 4: tags after each release,
  platforms, per-architecture tag equals the index entry, `cosign verify` on 3 of 3 digests,
  `gh attestation verify` on the index digest, the contract script on the published tag, and the
  negative check on a hand-pushed image.
- [X] T028 [US3] From the fork release, find the command that retrieves the image's SPDX document
  for a digest (try `cosign download sbom <repo>@<digest>`, then an OCI referrers query with
  `crane` or `oras`). Record the working command and a sample of its output in `baseline.md`. If
  no supported command retrieves it, set `sbom_directory` in `kos` and decide with the maintainer
  whether the SPDX files become release assets, which would amend SC-009.
- [X] T029 [US3] From the fork release, compare the GitHub release's asset list with v0.7.0's:
  six archives, `checksums.txt`, its `.sigstore.json` and `.pem`, six SPDX files. Record the
  result (SC-009).
- [X] T030 [US3] Confirm from the fork run's log that GoReleaser's ko pipe used the credential
  written by `crane auth login` (R12) and that no step used the Docker daemon for the image
  (SC-010). If the login was not picked up, switch the login step to the form that works and
  record it.

**Checkpoint**: All three P1 stories hold. This is the first state that may be merged.

---

## Phase 6: User Story 4: A contributor builds locally without publishing (Priority: P2)

**Goal**: The three Runefile tasks work as documented, name a missing tool, and never push.

**Independent Test**: quickstart.md section 2 on a machine with no registry credentials.

- [X] T031 [US4] Run `rune docker`, `rune docker-check` and `rune docker-export` (through
  `go run ./cmd/rune <task>` in a container that has Go, ko and a Docker socket) with no registry
  credentials present. Confirm `rune:local` exists, the check exits 0, and `dist/image/index.json`
  exists.
- [X] T032 [P] [US4] Confirm `rune docker-export` works with no Docker daemon: run it in a
  container without the socket mounted. Confirm `dist/` is ignored by Git (it is today) so the
  layout is never committed.
- [X] T033 [P] [US4] Confirm the missing-tool messages: run each of the three tasks with `ko`
  absent from `PATH`, then with `go` absent. Each exits non-zero and prints the exact line from
  T009.
- [X] T034 [US4] Prove no push happens (SC-007): run `rune docker` and `rune docker-export` with
  `KO_DOCKER_REPO` unset and with a `~/.docker/config.json` holding credentials for the local test
  registry only, while watching that registry's log: no `PUT` or `POST` request appears. Record
  the result in `baseline.md`.

**Checkpoint**: The local workflow is daemon-free for building and exporting and cannot publish.

---

## Phase 7: User Story 5: The image binary is the release binary (Priority: P2)

**Goal**: Identity is checked on every pull request and recorded for both architectures.

**Independent Test**: The CI step fails when the two binaries differ and passes when they match.

- [X] T035 [US5] Create `scripts/compare-image-binary.sh <image-ref> <binary-path>`: create a
  container from the image, copy `/ko-app/rune` out with `docker cp`, remove the container, print
  both SHA-256 values, exit 0 when equal and 1 with `FAIL binary differs` otherwise. Prove it
  detects a difference by running it against `rune:local` and an unrelated file.
- [X] T036 [US5] In `.github/workflows/ci.yml`, job `release-dryrun`, add a step "Compare image
  binary with the archive binary" after the contract check: run
  `scripts/compare-image-binary.sh <goreleaser.ko.local image> dist/<linux amd64 build dir>/rune`,
  resolving the build directory with a glob (`dist/rune_linux_amd64*/rune`).
- [X] T037 [US5] Run the same comparison locally on the snapshot from T020 for amd64, and for
  arm64 by extracting the arm64 image's binary with `crane export --platform linux/arm64` from
  the fork release (T027) or a local-registry ko build made with GoReleaser's flags. Record both
  results in `baseline.md` as the FR-024 record: architecture, the two checksums, identical or
  not. If they differ, stop and bring the difference, its cause and the options to the maintainer
  (FR-025) before touching the docs.

**Checkpoint**: Binary identity is on record for 2 of 2 architectures and guarded in CI.

---

## Phase 8: User Story 6: Documentation and old build paths are consistent (Priority: P3)

**Goal**: One current way to build, publish and verify the image in the docs; old files gone.

**Independent Test**: quickstart.md section 5.

- [X] T038 [P] [US6] Rewrite the build sections of `docs/docker.md`: prerequisites (Go 1.27, ko;
  a runtime only to run or load); `rune docker` and what it produces; `rune docker-export` and that
  it needs no daemon; a stamped local build with `--ldflags`; explicit publishing with
  `KO_DOCKER_REPO=<registry>/<path> ko build --bare ./cmd/rune`, that it pushes, the login it
  needs and what an authentication failure looks like; that the production repository is
  published only by the release workflow; limits (no system packages, no shell, no C toolchain,
  and what to do instead); a short note that the entrypoint is `/ko-app/rune` and `/rune` is kept
  as a link. Keep every `docker run` usage example, the multi-arch paragraph and the test harness
  section. Keep the sentence that the image contains the release binary only if T037 recorded
  identical.
- [X] T039 [P] [US6] Update `docs/releasing.md`: the image is built by ko inside GoReleaser; the
  local dry-run tool list (no Buildx; crane added); how tags are produced, including the
  per-architecture step and that it is safe to rerun if it fails; the verification commands for
  the image; the bill of materials retrieval command from T028; a section "Updating the base
  image" (resolve the new distroless digest, run `base-image.yml`, update the digest in
  `.goreleaser.yaml` and `.ko.yaml` in one pull request, the drift test fails if only one
  changes); that the `rune-base` package must stay public.
- [X] T040 [P] [US6] Update `CONTRIBUTING.md`: prerequisites gain ko (for image work only); the
  task table row for `rune docker` and new rows for `docker-export` and `docker-check`; the file
  table row that lists `Dockerfile` becomes `.ko.yaml`, `scripts/build-base-image.sh`,
  `scripts/check-image.sh` for the production image and `Dockerfile.test`, `docker-compose.yml`
  for the test harness.
- [X] T041 [US6] Run the docs harness in Docker (`go test ./test/docs/...`) and fix what it flags.
- [X] T042 [US6] Delete `Dockerfile` and `Dockerfile.goreleaser`. This is the last change, made
  only after T014, T020, T027 and T037 have passed (FR-033). Keep `Dockerfile.test`,
  `docker-compose.yml` and `.dockerignore`; update the comment at the top of `.dockerignore` to
  say it serves the test image.
- [X] T043 [US6] Search for stale references: `grep -rn "buildx build\|Dockerfile.goreleaser\|
  docker_manifests\|setup-qemu\|setup-buildx" --exclude-dir=specs --exclude-dir=.git .` finds
  nothing, and `grep -rn "Dockerfile\b" docs CONTRIBUTING.md README.md Runefile .github` names
  only `Dockerfile.test`.

**Checkpoint**: All six stories are complete.

---

## Phase 9: Polish and Cross-Cutting Concerns

- [X] T044 [P] Run `shellcheck` (from its Docker image) on `scripts/build-base-image.sh`,
  `scripts/check-image.sh` and `scripts/compare-image-binary.sh`, and fix what it reports.
- [ ] T045 Run all gates: Go tests and the race suite in Docker, `go vet` for Linux, Windows and
  macOS, lint with the CI linter version on Go 1.27.4, the docs harness, `goreleaser check`, and
  the snapshot with the contract and binary checks.
- [X] T046 Walk quickstart.md sections 1, 2, 3 and 5 as written and fix any command that does not
  work as documented. For SC-001, run every `docker run` example in `docs/docker.md` against a
  small project on both the last released image and the ko image (the snapshot or `rune:local`)
  and record in `baseline.md` that output and exit code match, apart from the version string.
  Confirm no em dash in the diff.
- [ ] T047 Remove the temporary notes that should not ship: decide with the maintainer whether
  `specs/036-ko-container-image/baseline.md` stays as the record FR-024 asks for (recommended:
  keep). Remove prototype images and the local registry container from the machine.
- [X] T048 Commit when the maintainer asks, as one commit
  (`build(release): build the container image with ko`), with no AI attribution. After the pull
  request: wait for every check, including the extended `release-dryrun` job.
- [X] T049 **Maintainer.** After the merge, sign the base image: run the `base-image.yml`
  workflow on `main` with the distroless digest recorded in T007 and the pinned base digest as
  `expected_digest`. The build is deterministic, so
  it must print the digest already pinned in `.goreleaser.yaml` and `.ko.yaml`; if it prints a
  different one, stop and report. Then confirm
  `cosign verify ghcr.io/rune-task-runner/rune-base@<digest>` succeeds with that workflow as the
  signer. Until this is done the pinned base is unsigned; do not cut a release before it.

---

## Dependencies

- Phase 2 blocks everything. Inside it: T003, T004 in parallel; T005 needs T004; T006, T007, T008,
  T009 in order; T010 needs T008; T011 needs T005 and T010.
- US1 (T012 to T016) needs Phase 2. T012 is a maintainer step and blocks T013, which blocks T017.
- US2 (T017 to T025) needs T013. T019 needs T017 and T018. T020 needs T019. T022 needs T005.
- US3 (T026 to T030) needs US2 complete. T027 is a maintainer step and blocks T028 to T030.
- US4 (T031 to T034) needs Phase 2 only; it can run alongside US2.
- US5 (T035 to T037) needs T020; the arm64 half of T037 needs T027 or a local-registry build.
- US6: T038 needs T037 (identity sentence); T039 needs T028; T042 needs T014, T020, T027, T037.
- Polish needs everything. T049 needs the merge (a manually started workflow runs only from
  `main`) and must finish before the first release built with ko.

## Parallel examples

- Phase 2: T003 and T004 together.
- US1: T015 and T016 together after T014.
- US4: T032 and T033 together after T031.
- US6: T038, T039 and T040 together (three different files).

## Implementation strategy

1. **MVP (Phases 1 to 3)**: a ko image on Rune's own base that passes the image contract, with
   the base published and pinned. Nothing about releases has changed yet, so this is safe to
   stop at.
2. **Release switch (Phases 4 and 5)**: GoReleaser builds the image with ko; verified in a fork
   before it is trusted. Merge only after Phase 5.
3. **Local workflow, identity guard, docs and cleanup (Phases 6 to 8)**, then polish.

Three tasks need the maintainer and cannot be finished by an agent alone: T012 (publishing the
base image to GHCR), T027 (the fork release) and T049 (signing the base image after the merge). Everything that depends only on local runs can be
done and verified without them, using the local registry.

## Implementation notes (2026-11-11)

Status: 42 of 49 tasks done. The open ones wait on the maintainer or follow from that.
Measured results are in [baseline.md](baseline.md).

- **T009**: the three Runefile tasks call `scripts/build-image.sh load|export` instead of holding
  the ko command lines. The script carries the tool checks and the six labels (a description with
  spaces does not survive a Runefile variable), and works without `rune` installed. This is the
  fallback T009 allowed, used as the main form.
- **T009, T032**: `rune docker-export` uses `KO_DOCKER_REPO=rune.invalid/rune`, not
  `ko.local/rune`. With `ko.local`, ko tries to reach a Docker daemon even with `--push=false`.
- **T009**: `rune docker` builds only the host's architecture, because a Docker daemon loads one
  platform. `rune docker-export` builds both.
- **T005**: inside a local image `--self-upgrade` declines as a development build (version
  `dev`), not with the container message. The contract script and contracts/image.md accept
  that for version `dev`.
- **T012**: done on 2026-11-11 with `.docs/scripts/2026-11-11-publish-rune-base.sh`, after the
  maintainer added the `write:packages` scope. Published
  `ghcr.io/rune-task-runner/rune-base@sha256:13f17425edcae59a09595d5b9280e0bfae0d0f52f34a8071bed8681a5ce10101`,
  the digest the local builds predicted. The package was created private; visibility can only be
  changed in the package settings page (done, see T013).
- **T013**: both files carry the GHCR reference and the drift test passes. The maintainer made
  the package public on 2026-11-12; an anonymous pull and a build with no credentials succeed.
- **T014**: done with a logged-in ko; `rune:local` on the GHCR base passes 33 checks.
- **T023**: `imjasonh/setup-crane@v0.7` (latest release, 2026-06-26).
- **T028**: both retrieval commands were proven against the local registry; GHCR is still to do.
- **T037**: arm64 was compared through GoReleaser itself, using a copy of the configuration with
  only `linux/arm64`, not through the fork release.
- **T042**: the two Dockerfiles were removed now, ahead of T014 and T027, so the change is
  complete in one piece. Every check that can run locally passed first. If the fork release
  (T027) fails, the removal is reverted with the rest.
- **Code review, round 1 (2026-11-11)**: the per-architecture tag step now runs after the image
  attestation (a failed tag must not cost the provenance) and the recovery is two `crane tag`
  commands, not a rerun; `base-image.yml` takes an optional `expected_digest` and refuses to
  sign a different image (use it in T049); `rune docker-export` clears `dist/image` first, since
  ko appends to an existing layout; local builds use `GOHOSTARCH`; the binary comparison removes
  its container on every exit and prints `FAIL` when the binary is missing; the base script
  rejects a destination whose only colon is a registry port; the CI step that finds the snapshot
  image prints its diagnostic.
- **Code review, round 2 (2026-11-11)**: the base created `/work` owned by root, so the image's
  user could not write to a volume mounted there (it can in v0.7.0). `/work` is now owned by
  65532, the contract script checks the owner (33 checks), and writing to a volume at `/work`
  was confirmed on both images. This changed the base digest to `sha256:13f17425...`; the two
  configuration files, baseline.md and the publish script carry the new value. Also: the base
  gets the `org.opencontainers.image.source` label so GHCR links the package to the repository;
  crane is pinned to v0.22.1 in `base-image.yml` and the publish script, because the base digest
  is reproducible only with the same crane; the contract script mounts a world-readable copy of
  the fixture; `scripts/build-image.sh` unsets `GOOS`/`GOARCH`, which ko rejects alongside
  platform settings. Not verified: whether the workflow's token may write to a package first
  pushed by hand (T049 will show; if denied, grant the repository write access in the package
  settings).
- **T049, first attempts (2026-11-12)**: the workflow's first run was denied write access to
  the hand-pushed package until the maintainer granted the repository Actions write access. The
  second run pushed, but built `sha256:44efecf4...`, not the pinned `sha256:13f17425...`, and
  refused to sign. The two have identical content (same uncompressed layer, same configuration);
  only the compressed added layer differs (146 against 142 bytes), because the runner's crane
  release binary and the locally built crane compress differently. The workflow's build is the
  canonical one, so both configuration files now pin `sha256:44efecf4...`; the determinism claim
  in research.md R11 holds per crane binary, not across machines.
- **One package (2026-11-12, after v0.8.0-rc.1)**: the maintainer found two packages (`rune`
  and `rune-base`) confusing. The base now lives in the `rune` package under the tag
  `base-<id>` and is still referenced only by digest, which did not change
  (`sha256:44efecf4...`). The base script no longer leaves per-architecture helper tags. This
  supersedes the `rune-base` references in research.md R3 and R11, data-model.md and the
  contracts. The `rune-base` package is deleted once the base is signed in its new place.
- **v0.8.0-rc.1 (2026-11-12)**: the first release built with ko. Verified on GHCR: the three
  tags and an unmoved `latest`; `cosign verify` on the index and both image digests;
  `gh attestation verify`; bill of materials retrieval; 33 contract checks on the published tag;
  image binary equal to the archive binary for both architectures; 15 release assets of the
  same kinds as before. This settles T027 to T030 without a fork.
- **T047**: the local test registry container is removed. Prototype images and two Docker
  volumes (tools, build cache) remain on the machine.

