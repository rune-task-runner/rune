# Quickstart: Validating the ko Image

Runnable checks for [spec.md](spec.md). Contracts: [contracts/](contracts/). Measured results so
far are in [research.md](research.md).

## Prerequisites

- Go 1.27, ko v0.19, crane, Docker (to run images), `goreleaser` v2.20 or newer for section 3.
- For section 4: a fork with GHCR write access, so nothing touches the production repository.
- One-time, maintainer: after the base image is first published (by hand, tasks.md T012), set
  the `rune-base` package to public.
- After the merge, maintainer: run `base-image.yml` once to sign the pinned base (tasks.md T049),
  then `cosign verify ghcr.io/rune-task-runner/rune-base@<pinned digest>` must succeed. No release
  is cut before this.

## 1. Tests

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/release/...
```

Passes when `.ko.yaml` and `.goreleaser.yaml` agree and the base image is pinned by digest.

## 2. Local build (User Stories 1 and 4)

```sh
rune docker            # loads rune:local
rune docker-check      # contract: configuration, files, fixtures
rune docker-export     # OCI layout in dist/image/, works with the Docker daemon stopped
```

Expected: `docker-check` prints no `FAIL` line and exits 0. With `ko` renamed away, `rune docker`
exits non-zero and names ko and the install command. During all three, the only registry
contacted is `ghcr.io/rune-task-runner/rune-base`, to pull the base image, and nothing is pushed
anywhere (check with registry credentials absent: the tasks still succeed).

## 3. Release dry run (User Stories 2 and 5)

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=sign,sbom
scripts/check-image.sh "$(docker images --format '{{.Repository}}:{{.Tag}}' | grep goreleaser.ko.local | head -1)"
```

Then compare binaries:

```sh
id=$(docker create <snapshot image>) && docker cp "$id":/ko-app/rune /tmp/rune.image && docker rm "$id"
sha256sum /tmp/rune.image dist/rune_linux_amd64*/rune
```

Expected: the two checksums are equal. `dist/` lists the same non-image files as on `main`.
Nothing was pushed: `crane ls ghcr.io/rune-task-runner/rune` shows no new tag.

## 4. Fork release (User Stories 2 and 3)

In the fork, with the image repository pointed at the fork's GHCR path, cut `v0.0.1-rc.1` and
then `v0.0.1`.

| Check | Command | Expected |
|---|---|---|
| Tags after the prerelease | `crane ls <repo>` | `0.0.1-rc.1`, `0.0.1-rc.1-amd64`, `0.0.1-rc.1-arm64`; no `latest` |
| Tags after the stable release | `crane ls <repo>` | adds `0.0.1`, `0.0.1-amd64`, `0.0.1-arm64`, `latest` |
| Platforms | `crane manifest <repo>:0.0.1` | two entries: linux/amd64, linux/arm64 |
| Per-arch tag is the index entry | `crane digest <repo>:0.0.1-amd64` vs `crane digest --platform linux/amd64 <repo>:0.0.1` | equal |
| Signature | `cosign verify` with the fork's workflow identity, on the index digest and both image digests | 3 of 3 succeed |
| Provenance | `gh attestation verify oci://<repo>@<index digest> --repo <fork>` | succeeds |
| Bill of materials | the command from `docs/releasing.md` | SPDX document for the digest |
| Contract | `scripts/check-image.sh <repo>:0.0.1` | exit 0 |
| Negative | the same verify commands on `rune:local` pushed by hand to the fork | fail |
| Release assets | compare the GitHub release's file list with v0.7.0's | same kinds, nothing added or removed |

## 5. Docs

Every container command in `docs/docker.md`, `docs/releasing.md` and `CONTRIBUTING.md` is run as
written. `grep -rn "buildx build\|Dockerfile.goreleaser" docs CONTRIBUTING.md Runefile .github`
finds nothing.

## Story coverage

| Story | Sections |
|---|---|
| US1 image behaves as before | 2, 3, 4 (contract script) |
| US2 same names from one release run | 3, 4 |
| US3 verification | 4 |
| US4 local build without publishing | 2 |
| US5 binary identity | 3 |
| US6 docs and cleanup | 5 |
