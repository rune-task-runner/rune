# Contract: Local Workflow

Decisions referenced as R*n* are in [../research.md](../research.md).

## Prerequisites

Go 1.27 or newer and ko v0.19 or newer on `PATH`. A container runtime only to run or load an
image, not to build or export one.

## Runefile tasks

| Task | Does | Needs a daemon | Pushes |
|---|---|---|---|
| `rune docker` | builds with ko and loads the image as `rune:local` | yes (to load) | never |
| `rune docker-export` | builds with ko and writes an OCI layout to `dist/image/` | no | never |
| `rune docker-check` | runs `scripts/check-image.sh rune:local` | yes | never |

`rune docker` and `rune docker-export` call `scripts/build-image.sh load` and
`scripts/build-image.sh export`, which also work without `rune` installed. The script starts by
checking for `go` and `ko`. When one is missing it exits non-zero and prints
which one and how to install it, for example:

```text
ko is not installed. Install it with: go install github.com/google/ko@latest
```

## `.ko.yaml`

| Key | Value |
|---|---|
| `defaultBaseImage` | `ghcr.io/rune-task-runner/rune-base@sha256:...`, equal to `kos[0].base_image` |
| `defaultPlatforms` | `linux/amd64`, `linux/arm64` |
| `builds[0]` | `id: rune`, `main: ./cmd/rune`, `env: [CGO_ENABLED=0]`, `flags: [-trimpath]`, `ldflags: [-s -w]` |

A local image reports version `dev`. The docs show the `--ldflags` form for a stamped one.

The export uses the placeholder repository `rune.invalid/rune` with `--push=false`: ko needs a
repository name even when it does not push, its `ko.local` name would make it look for a Docker
daemon, and a name under `.invalid` can never be reached.

ko's configuration file has no place for labels, so `scripts/build-image.sh` passes the six
labels with `--image-label`: the four fixed ones, version `dev`, and revision set to the current commit.

## Publishing by hand

Not a task. Documented as an explicit command that names its destination:

```sh
KO_DOCKER_REPO=<registry>/<path> ko build --bare ./cmd/rune
```

The docs state that this pushes, that it needs a login for that registry, what an authentication
failure looks like, and that the production repository is published only by the release workflow.

## `scripts/check-image.sh <image-ref>`

- Asserts [image.md](image.md): configuration, files, fixtures.
- Exit 0 when every check passes; otherwise exit 1 after printing each failed check as
  `FAIL <check>: got <value>, want <value>`.
- Needs `docker`. Reads only; never pushes or tags.

## `scripts/build-base-image.sh <distroless-digest> <destination>`

- Builds the base for both architectures with crane and pushes the index to `<destination>`.
- Prints the pushed index digest on the last line of standard output.
- Deterministic: the same input digest gives the same output digest.
- Needs `crane` and a login for `<destination>`. No Docker daemon.

## Drift test

`go test ./test/release/...` fails when `.ko.yaml` and `.goreleaser.yaml` disagree on base image,
platforms, main package, flags or environment, or when the base image is not referenced by digest.

## Limits stated in the docs

This build covers a static Go binary on a minimal base. An image that needs system packages, a
shell, or a C toolchain is out of its reach: write a Dockerfile `FROM` the Rune image or copy the
binary from it.
