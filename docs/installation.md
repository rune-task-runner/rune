# Installation

> Get the `rune` binary onto your machine. Already installed? Jump to
> [Getting started](getting-started.md).

Rune ships as a single, statically-linked binary with no runtime dependencies. Pick the
method that fits your platform.

## Install script (Linux / macOS)

Detects your OS/architecture, downloads the right archive, **verifies its checksum**, and
installs the `rune` binary:

```sh
curl -sSfL https://raw.githubusercontent.com/rune-task-runner/rune/main/scripts/install.sh | sh
```

Override the target directory or version with environment variables:

```sh
INSTALL_DIR="$HOME/.local/bin" VERSION=v0.4.0 \
  sh -c "$(curl -sSfL https://raw.githubusercontent.com/rune-task-runner/rune/main/scripts/install.sh)"
```

## Homebrew (macOS / Linux)

```sh
brew install rune-task-runner/tap/rune
```

`brew upgrade rune` keeps it current.

## Scoop (Windows)

```powershell
scoop bucket add rune https://github.com/rune-task-runner/scoop-bucket
scoop install rune
```

## Prebuilt binary (manual)

Download the archive for your OS/architecture from the
[GitHub Releases](https://github.com/rune-task-runner/rune/releases) page, extract it, and put the
`rune` binary on your `PATH`.

**Linux / macOS:**

```sh
# Replace VERSION, OS (linux|darwin), ARCH (amd64|arm64) as appropriate.
curl -sSfL -o rune.tar.gz \
  https://github.com/rune-task-runner/rune/releases/download/vVERSION/rune_VERSION_OS_ARCH.tar.gz
tar -xzf rune.tar.gz rune
sudo mv rune /usr/local/bin/
rune --version
```

**Windows (PowerShell):** download the `..._windows_amd64.zip` (or `..._windows_arm64.zip`)
archive, extract `rune.exe`, and add its folder to your `PATH`.

Every release publishes a `checksums.txt` plus a cosign signature and build provenance. To
verify your download, see [Verifying releases](releasing.md#verifying-a-release).

## From source (Go)

Requires Go 1.27 or newer. On macOS, Go 1.27 needs macOS 13 or newer:

```sh
go install github.com/rune-task-runner/rune/cmd/rune@latest
```

This installs `rune` into `$(go env GOPATH)/bin`. Ensure that directory is on your `PATH`.
The binary reports the release it was built from (`rune --version`), so Runefiles that pin
a [`minimum_version`](runefile.md) work the same as with a downloaded release binary.

To build from a checkout:

```sh
git clone https://github.com/rune-task-runner/rune
cd rune
go build -o rune ./cmd/rune
```

Rune is pure Go with `CGO` disabled, so cross-compilation is trivial:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o rune ./cmd/rune
```

## Container

Run Rune with no local toolchain using the official multi-arch image (linux/amd64 +
linux/arm64). See the [Docker guide](docker.md) for full details (mounting your project,
passing arguments, image verification, and the minimal-image limitations):

```sh
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune --list
```

## Upgrading

A standalone install (the install script or a prebuilt binary) upgrades itself:

```sh
rune --self-upgrade
```

It finds the latest stable release, downloads the archive for your OS and architecture, and
replaces the running binary. It runs no task, reads no Runefile, and works in any directory.
Rune never checks for updates on its own: no other command contacts the network or mentions
newer versions.

What the command does depends on how Rune was installed:

| Installed with | What `rune --self-upgrade` does | Upgrade with |
|----------------|---------------------------------|--------------|
| Install script or prebuilt binary | Upgrades in place | `rune --self-upgrade` |
| Homebrew | Changes nothing | `brew upgrade rune` |
| Scoop | Changes nothing | `scoop update rune` |
| `go install` | Changes nothing | `go install github.com/rune-task-runner/rune/cmd/rune@latest` |
| Container image | Changes nothing | `docker pull ghcr.io/rune-task-runner/rune:latest` |
| A build from a checkout (version `dev`) | Changes nothing | Install a release |

When it changes nothing it prints the command to use and exits with code 1. A binary that
matches none of these is treated as a standalone install. If Rune is started through a symbolic
link, the file the link points to is replaced and the link is left alone.

### What is verified

Nothing is extracted or installed until both checks pass:

1. The release's `checksums.txt` carries a valid Sigstore signature made by Rune's release
   workflow on GitHub Actions. A signature from any other identity is rejected. See
   [Verifying a release](releasing.md#verifying-a-release) for the same check done by hand.
2. The downloaded archive matches its SHA-256 in that list.

There is no option to skip verification. Rune needs no other tool for it.

### Requirements

- **Platforms:** Linux, macOS 13 or newer, and Windows, each on `amd64` and `arm64`.
- **Network:** HTTPS access to `api.github.com`, `github.com` and the host its downloads
  redirect to, and `tuf-repo-cdn.sigstore.dev`. The standard proxy environment variables are
  honored.
- **Permissions:** write access to the directory that holds the binary. Rune never asks for
  elevated rights. If the directory is not writable, rerun the command with the rights that
  directory needs, or reinstall with the install script.

### If it fails

A failed or interrupted upgrade leaves the installed binary as it was, and removes the
temporary files it created next to it. The command exits with code 1, or 130 when interrupted.

| Message starts with | Meaning | What to do |
|---------------------|---------|------------|
| `cannot reach` | No network route to the named host. | Check the connection or proxy, then retry. |
| `did not answer within` | A host stopped responding. | Retry later. |
| `the release service rate limit` | Too many anonymous requests from your address. | Retry after the time shown. |
| `could not be verified` | The signature check failed, or the release is still being published. | Retry later. If it persists, report it. Do not install that release by hand. |
| `does not match the signed checksum` | The download was corrupted or altered. | Retry. If it persists, report it. |
| `could not download` | A download failed or ended early. | Retry. |
| `has no archive for` | No release is published for your platform. | Build from source (below). |
| `cannot write to` | The binary's directory is not writable by you. | See Permissions above. |
| `another self-upgrade` | An upgrade of the same binary is already running. | Wait for it to finish. A leftover from a killed run is cleared by the next upgrade on the same machine, or after 15 minutes when the directory is shared between machines. |

**Windows.** Windows does not let a running program overwrite its own file, so the upgrade
renames the old binary to `rune.exe.old` and puts the new one in its place. The next
`rune --self-upgrade` removes `rune.exe.old`; you can also delete it yourself. A program still
running from the old binary (an editor's `rune lsp`, say) keeps `rune.exe.old` in use; the next
upgrade then sets the binary it replaces aside as `rune.exe.old.1`, and both are removed once
nothing runs from them. If the upgrade is killed at the exact moment between the two renames,
or reports that `rune.exe` is now missing, the old binary is still there under the name the
message gives (normally `rune.exe.old`): rename it back to `rune.exe`.

To upgrade by hand at any time, repeat the [install script](#install-script-linux--macos) or
download a [prebuilt binary](#prebuilt-binary-manual).

## Shell completions

Rune can generate completion scripts:

```sh
rune completion bash   # or: zsh | fish | powershell
```

Install the output per your shell's convention (for example, source it from your shell rc
file, or place it in your completions directory).

## Verify

```sh
rune --version
rune --help
```

Next: the [getting-started guide](getting-started.md).
