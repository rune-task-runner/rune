# Running Rune in Docker

You can run Rune with no local Go toolchain using the official container image. The image
is a minimal, non-root, static-binary container (distroless base) — just the `rune` binary,
no shell or package manager.

## Quick start

Mount your project at `/work` (the image's working directory) and pass any normal Rune
arguments after the image name:

```sh
# List tasks in the current project:
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune --list

# Run a task:
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune build

# Pass arguments / variable overrides:
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune greet Ada
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune build target=release
```

The container's `ENTRYPOINT` is the Rune binary, so everything after the image name goes
straight to the CLI. Behavior for the default `sh` executor is identical to a native run: the shell is
the pure-Go `mvdan.cc/sh` interpreter baked into the binary.

## Tags

- `ghcr.io/rune-task-runner/rune:latest` — latest **stable** release (prereleases never move `latest`)
- `ghcr.io/rune-task-runner/rune:<version>` — a specific release (e.g. `:1.2.3`, `:1.3.0-rc.1`)

Each tag is a **multi-arch manifest** covering `linux/amd64` and `linux/arm64`; Docker pulls
the variant matching your host automatically.

```sh
docker buildx imagetools inspect ghcr.io/rune-task-runner/rune:latest   # see both platforms
```

## Upgrading

Upgrade by pulling a newer tag (`docker pull ghcr.io/rune-task-runner/rune:latest`). Inside the
image `rune --self-upgrade` changes nothing and says so: a container is replaced, not patched.

## Verifying the image

Images are signed with cosign (keyless) and carry build provenance — verify with public
material only, no pre-shared key:

```sh
cosign verify ghcr.io/rune-task-runner/rune:<version> \
  --certificate-identity-regexp 'https://github.com/rune-task-runner/rune/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

gh attestation verify oci://ghcr.io/rune-task-runner/rune:<version> --repo rune-task-runner/rune
```

## Building it yourself

The image is built with [ko](https://ko.build), which compiles Rune and assembles the image
without a Docker daemon. You need Go 1.27 or newer and ko:

```sh
go install github.com/google/ko@latest
```

A container runtime is needed only to load or run the result, not to build it.

```sh
rune docker           # build and load into Docker as rune:local
rune docker-check     # check rune:local against the image contract
rune docker-export    # build both architectures into dist/image (OCI layout); no daemon needed
```

None of these pushes anything. Each one says what to install if Go or ko is missing. Without
`rune` on your machine, run the script behind the tasks: `scripts/build-image.sh load` or
`scripts/build-image.sh export`.

A local image reports version `dev`. For one that reports a version, pass the stamps yourself.
This loads the image as `ko.local/rune:local`; without `--platform` ko builds both
architectures and loads the amd64 one, whatever your machine runs:

```sh
KO_DOCKER_REPO=ko.local/rune ko build --bare --tags local \
  --platform "linux/$(go env GOHOSTARCH)" \
  --ldflags "-s -w -X main.version=$(git describe --tags --always) -X main.commit=$(git rev-parse --short HEAD)" \
  ./cmd/rune
```

### Publishing to your own registry

Publishing is a separate, explicit step that names where the image goes:

```sh
KO_DOCKER_REPO=registry.example.com/you/rune ko build --bare ./cmd/rune
```

This pushes. ko uses the credentials of `docker login` (or `ko login`) for that registry; if
they are missing or wrong the push fails with `UNAUTHORIZED` or `DENIED` from the registry, and
nothing is published. `ghcr.io/rune-task-runner/rune` itself is published only by the release
workflow.

### What this build covers

ko builds one static Go binary onto a minimal base image. It cannot add system packages, a
shell, or anything that needs a C toolchain. For an image with more in it, see the second
option under [Limitations](#limitations-of-the-minimal-image) below.

### Image layout

| Path or setting | Value |
|---|---|
| Binary | `/ko-app/rune`, the same file as in the release archive for that platform |
| `/rune` | a link to the binary, so `--entrypoint /rune` keeps working |
| Entrypoint | `/ko-app/rune` (releases up to v0.7.0 recorded `/rune`; both start Rune) |
| Working directory | `/work` |
| User | `65532` (non-root) |

The base image is distroless static, plus `/work`, the `/rune` link and `RUNE_CONTAINER=1`.
It is published as `ghcr.io/rune-task-runner/rune-base` and pinned by digest in `.ko.yaml`
and `.goreleaser.yaml`.

## Limitations of the minimal image

The image intentionally contains **only** the `rune` binary — no system shell, interpreters,
or tools. That keeps it tiny and secure, but it means:

- **`sh`-executor tasks** that use only the built-in pure-Go shell work as usual.
- **Tasks that shell out to external programs** (e.g. `git`, `curl`, compilers) will fail
  with a clear "executable not found" error — those programs are not in the image.
- **`python` / `node` / `agent` executor tasks** require their runtimes, which are not
  present. They will fail with a clear error rather than crash.

If you need system tools or language runtimes, either:

1. Run Rune **natively** (see the [installation guide](installation.md)), or
2. Build a fuller image `FROM` a distro base (e.g. `debian:bookworm-slim`) and copy the
   `rune` binary into it alongside the tools you need.

## Running Rune's own test suite (contributors)

The repository ships a separate Docker-based **test harness** (not the production image).
Per project policy the Go test suite runs inside the container, never on the host:

```sh
docker-compose run --rm test go test ./...
docker-compose run --rm test go test -race ./...
docker-compose run --rm test go vet ./...
```

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full development workflow.
