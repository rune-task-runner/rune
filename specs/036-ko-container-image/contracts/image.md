# Contract: The Published Image

What `ghcr.io/rune-task-runner/rune` guarantees after this feature. `scripts/check-image.sh`
asserts every row marked "checked". Decisions referenced as R*n* are in
[../research.md](../research.md).

## Configuration

| Field | Value | Checked | Compared with today |
|---|---|---|---|
| WorkingDir | `/work` | yes | same |
| User | `65532` | yes | same |
| Entrypoint | one element, the path of the Rune binary (`/ko-app/rune`) | yes | **changed** from `/rune` (R3) |
| Cmd | none | yes | same |
| Env `RUNE_CONTAINER` | `1` | yes | same |
| Env `SSL_CERT_FILE` | set by the base image | yes | same |
| Label `org.opencontainers.image.title` | `rune` | yes | same |
| Label `org.opencontainers.image.description` | `A shared task runner for humans and AI agents` | yes | same |
| Label `org.opencontainers.image.source` | `https://github.com/rune-task-runner/rune` | yes | same |
| Label `org.opencontainers.image.licenses` | `MIT` | yes | same |
| Label `org.opencontainers.image.version` | the release version | yes | same |
| Label `org.opencontainers.image.revision` | the full commit | yes | same |
| Platforms of the version tag | exactly linux/amd64 and linux/arm64 | no; the release workflow's per-architecture tag step fails when either is missing | same |

## Files

| Path | What | Checked |
|---|---|---|
| `/ko-app/rune` | the Rune binary, byte-identical to the archive binary for that architecture | yes (binary comparison, R7) |
| `/rune` | link to the binary; invocable directly | yes |
| `/work` | empty directory owned by the image's user (`65532`), the mount point for a project | yes |
| CA certificates | from the base image | yes (file exists at `SSL_CERT_FILE`) |

There is no shell and no package manager, as today.

## Behavior (mounted-project fixtures)

Run as `docker run --rm -v <fixture>:/work <image> ...`. The fixture Runefile has a task that
takes a parameter and uses only the built-in shell, and a task that exits 7.

| Invocation | Expected |
|---|---|
| `--version` | `rune version <version> (commit <short commit>)`, exit 0 |
| `--list` | both tasks listed, exit 0 |
| `<task> <argument>` | the task prints the argument, exit 0 |
| `<failing task>` | exit 7 |
| `--entrypoint /rune ... --version` | same as `--version` |
| no mount, `--list` | "no Runefile found", non-zero exit |
| `--self-upgrade` | declines and names `docker pull`, exit 1. A local build (version `dev`) declines as a development build instead, also exit 1 |

## Accepted differences from today's image

Visible only to someone who inspects the image; none changes a documented command.

- Entrypoint is `/ko-app/rune`, not `/rune`.
- `PATH` ends with `:/ko-app`.
- `KO_DATA_PATH=/var/run/ko` is set.
- The author field is `github.com/ko-build/ko`.
- One extra empty layer (ko's data layer), and one small layer from the base image.
- Media types are OCI (index and manifests). To confirm during implementation that this matches
  what Buildx publishes today, and that `docker pull` and `docker buildx imagetools inspect` in
  `docs/docker.md` still work as written.
