# Contract: Go Toolchain

What the repository states about Go after this feature. Decisions referenced as R*n* are in
[../research.md](../research.md).

## Selections

| Place | Value |
|---|---|
| `go.mod` | `go 1.27.0`, no `toolchain` line |
| `.github/workflows/ci.yml`, every `setup-go` step | `go-version: "1.27"`, `check-latest: true` |
| `.github/workflows/release.yml` | same |
| `.github/workflows/pages.yml` | same |
| `Dockerfile` | `ARG GO_VERSION=1.27` |
| `Dockerfile.test` | `ARG GO_VERSION=1.27` |
| `.github/workflows/ci.yml`, linter | `version: v2.13` (R4) |

The selector names the minor only, so each build uses the newest 1.27 patch.

## Documentation

| Place | Statement |
|---|---|
| `docs/installation.md`, "From source" | Requires Go 1.27 or newer |
| `docs/installation.md`, platforms | macOS 13 or newer |
| `CONTRIBUTING.md`, prerequisites | Go 1.27+ |

No other document states a Go requirement.

## Checks

```sh
# Finds nothing in the scoped files.
grep -nE '(^go |GO_VERSION=|go-version: "|golang:|Go )1\.2[45]' go.mod Dockerfile Dockerfile.test \
  CONTRIBUTING.md docs/installation.md \
  .github/workflows/ci.yml .github/workflows/release.yml .github/workflows/pages.yml
```

The seven constitution gates pass: lint, test with the race detector, build, golden, fuzz-smoke,
docs-verify, release-dryrun.

## Release output

A snapshot release run lists the same files before and after: six archives, `checksums.txt`, one
SBOM per archive, two container images and their manifests, the Homebrew cask and the Scoop
manifest. Binaries are stamped with `main.version` and `main.commit` as before.
