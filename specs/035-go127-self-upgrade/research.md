# Research: Go 1.27 Toolchain and `rune --self-upgrade`

Decisions for [plan.md](plan.md). Items marked **Measured** were run on 2026-11-10 in a
`golang:1.27-bookworm` container (go1.27.4) with the repository mounted read-only.

## Part A: Toolchain

### R1. Target version

**Decision**: Go 1.27. `go.mod` declares `go 1.27.0`. Workflows select `"1.27"` with
`check-latest: true`, and both Dockerfiles default to `GO_VERSION=1.27`. No `toolchain` line and no
patch pin anywhere.

**Rationale**: The official download feed lists go1.27.4 as the latest stable patch today (the issue
recorded 1.27.3). A minor selector that floats is how the repository already works (`"1.25"`,
`golang:1.25-bookworm`), so every build picks up the newest patch without another edit, and FR-032's
"where a patch is pinned" clause has nothing to apply to. `go 1.27.0` is also the floor the
verification library needs (R6).

**Alternatives considered**: Pin `1.27.4` in workflows and Dockerfiles (goes stale with each patch
release, needs a commit per patch). Add `toolchain go1.27.4` to `go.mod` (the repository decided in
spec 002 that `go.mod` carries only the language baseline).

### R2. Places that select or state the Go version

**Decision**: Change exactly these.

| File | Today | After |
|---|---|---|
| `go.mod` | `go 1.25.0` | `go 1.27.0` |
| `.github/workflows/ci.yml` (6 jobs) | `go-version: "1.25"` | `"1.27"` |
| `.github/workflows/release.yml` | `go-version: "1.25"` | `"1.27"` |
| `.github/workflows/pages.yml` | `go-version: "1.25"` | `"1.27"` |
| `Dockerfile` | `ARG GO_VERSION=1.25` | `1.27` |
| `Dockerfile.test` | `ARG GO_VERSION=1.25`, comment names `golang:1.25-bookworm` | `1.27` in both |
| `docs/installation.md` | "Requires Go 1.24 or newer (the project builds on Go 1.25)" | "Requires Go 1.27 or newer" |
| `CONTRIBUTING.md` | "Go 1.24+ (the project builds on Go 1.25; CI derives the version from `go.mod`)" | "Go 1.27+", and the false "derives" clause goes |

**Rationale**: A search of the repository for `1.24` and `1.25` outside `specs/` and the changelog
finds only these. Older spec documents are history and stay as written.

### R3. Compatibility of the code with Go 1.27

**Decision**: No source change is expected for the migration itself. The full gate set decides.

**Measured**: `go build ./cmd/rune` succeeds (stripped linux/amd64 binary 11,563,168 bytes),
`go vet ./...` exits 0, and `gofmt -l .` prints nothing, all on go1.27.4 with the current tree.

**Watch list from the Go 1.27 release notes**:

- `go mod tidy` rewrites `go.mod` into at most two `require` blocks. Expect a diff.
- `encoding/json` is backed by the v2 implementation. Behaviour is kept but error text can differ, so
  any test or golden file that contains a JSON error string may need regenerating (deliberately, per
  Principle VI).
- `go test` runs the `stdversion` vet check, and `printf` flags `%w` on a pointer to an error type.
- `compress/flate` output bytes change. Nothing in Rune compares compressed bytes.
- macOS 13 Ventura is now the minimum. The installation guide states it.
- The removed `GODEBUG` settings are not used in this repository.

### R4. Tooling that must move with it

**Decision**: Raise the linter from `v2.12` to `v2.14` in `ci.yml` (first planned as `v2.13`, which
failed in CI on go1.27.4; see the implementation notes in tasks.md). Leave GoReleaser at `~> v2`,
the docs generator and everything else alone unless a gate fails.

**Measured**: `golangci/golangci-lint:v2.12` reports "version 2.12.2 built with go1.26.2".
golangci-lint refuses a module whose `go` directive is newer than the Go it was built with, so v2.12
cannot lint a `go 1.27.0` module. Public reports say v2.13.1 (2026-08-20) is built with go1.27. The
first migration task confirms this with `golangci-lint version` before relying on it.

**Rationale**: FR-035 allows tool changes only where compatibility needs them. This is the one known
case. GoReleaser builds with whatever Go is on the path, and the release-dryrun gate proves it.

### R5. Release output stays the same

**Decision**: No change to `.goreleaser.yaml` build, archive, checksum, signing, SBOM, image or
package-manager sections for the migration. Compare the file list of a snapshot run before and after.

**Rationale**: FR-036 and SC-010. The only release-side edits in this feature belong to the
self-upgrade half and are listed in R17.

## Part B: Self-upgrade

### R6. How the signature is verified

**Decision**: Use `github.com/sigstore/sigstore-go` v1.4.0 in process. Build a verifier that requires
one signed certificate timestamp, one transparency log entry and one observer timestamp, with a
policy of the SHA-256 of `checksums.txt` plus an exact certificate identity (R8).

**Measured**: A prototype using exactly this configuration verified the real v0.7.0
`checksums.txt` against the live Sigstore trust root. It rejected the same file with one byte
appended ("artifact does not match digest") and rejected a different workflow identity ("no matching
CertificateIdentity found").

**Cost, measured**: A stripped linux/amd64 program that only does this is 18.9 MB against 6.1 MB for
a baseline that already includes `net/http`, `archive/tar`, `archive/zip` and `encoding/json`. That
is about 12.7 MB and 67 modules. Rune is 11.6 MB today, so expect roughly 24 MB.

**Rationale**: FR-012 rules out calling an installed `cosign`. Keyless verification is a chain of
checks (certificate chain to Fulcio, certificate transparency, Rekor entry and its signed timestamp,
validity at signing time, identity extensions). The library is the maintained reference
implementation of that chain in Go, it already handles both Rekor generations, and it needs Go 1.27,
which this feature brings.

**Alternatives considered**:

- *Hand-written verifier on the standard library.* No size cost, but several hundred lines of
  security-critical code that must follow Sigstore format changes. Rejected: this is the part of the
  feature where a subtle mistake installs a forged binary.
- *Verify GitHub's build-provenance attestation instead.* It exists for every archive, but the issue
  asks for the signed `checksums.txt`, and the attestation is verified with the same library anyway.
- *Skip signatures, check SHA-256 only.* Forbidden by FR-009 and FR-011.

The size cost is the one decision here a maintainer may want to overrule. It is recorded in the
plan's Complexity Tracking.

### R7. Where the trust root comes from

**Decision**: Fetch the Sigstore trust root through TUF on each run, with the local cache disabled.
The TUF root of trust is the one compiled into the library. Rune supplies its own TUF fetcher, built
on the same bounded downloader type as every other request (R16). The downloader stands alone,
holding only an HTTP client and the limits, so the trust source can be built before the updater is.

**Measured**: `tuf.DefaultOptions()` with `DisableLocalCache = true` fetched a 6,787-byte
`trusted_root.json` and wrote nothing to disk. TUF support adds about 0.3 MB.

**Rationale**: Sigstore rotates keys. A trust root baked into the binary would silently age until old
installs could no longer upgrade. Disabling the cache keeps the promise that Rune writes nothing
outside the install directory, and avoids creating `~/.sigstore`.

**Why a fetcher of our own**: Checked in the library source. The default fetcher uses a retrying
HTTP client with no timeout, the fetcher interface (`DownloadFile(url, maxLength, _)`) takes no
context, and `Options.Context` is not used for downloads. Left alone, a silent trust-root host would
hang the run and break FR-028 and Principle VIII. The replacement is a small adapter that holds the
run's context and calls the bounded download. It must return the TUF library's own HTTP-status error
for a non-200 answer, because the TUF client relies on a 404 to learn that no newer root exists.

**Alternatives considered**: Embed `trusted_root.json` (measured 18.5 MB, saves only 0.3 MB, goes
stale). Use the default on-disk cache (writes into the home directory for a command run a few times
a year).

### R8. Whose signature is accepted

**Decision**: Exact match on both values.

- Issuer: `https://token.actions.githubusercontent.com`
- Subject: `https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main`

**Measured**: This is the subject in the v0.7.0 signing certificate. The release workflow is
`workflow_dispatch` only and its job has `if: github.ref == 'refs/heads/main'`, so no legitimate
release can carry another subject.

**Rationale**: `docs/releasing.md` documents the pattern `https://github.com/rune-task-runner/rune/.*`,
which any workflow on any branch of the repository satisfies. An unattended installer should accept
only the release workflow. The exact value is a strict subset of the documented pattern, so FR-009
holds, and `docs/releasing.md` gains the exact value plus a note that renaming `release.yml` or
releasing from another ref strands existing installs.

**Alternatives considered**: The documented regular expression (too wide). A regular expression that
also allows `refs/tags/v*` (no release is built from a tag ref today).

### R9. Signature file format

**Decision**: Read `checksums.txt.sigstore.json` in either form. When the JSON has a `mediaType`
field it is a standard Sigstore bundle and is loaded as is. Otherwise it is the older cosign bundle
(`base64Signature`, `cert`, `rekorBundle`) and is mapped field by field onto a standard v0.1 bundle
before the same verification runs. Anything else fails as "could not be verified".

**Measured**: The v0.7.0 file is the older form (the release installs cosign through
`sigstore/cosign-installer@v3`). The prototype's field mapping of that file verified under R6 with
all three checks on.

**Rationale**: The spec assumes the release layout stays as it is today, so the older form must
work. cosign 3 writes the standard form by default, and the workflow floats on an action major, so a
tooling bump could switch forms without anyone deciding to. Accepting both costs one branch and
prevents every existing install from being stranded by that bump. Both forms pass through the same
verifier, so there is one set of checks.

**Alternatives considered**: Standard form only, and change the release to produce it (alters
release output, and the fate of `checksums.txt.pem` under the new form is unverified). Older form
only (breaks on the next cosign major).

### R10. Finding the latest stable release

**Decision**: `GET https://api.github.com/repos/rune-task-runner/rune/releases/latest`, anonymous,
with `Accept: application/vnd.github+json` and a `User-Agent` of `rune/<version>`. Take `tag_name`.
Refuse the answer if `draft` or `prerelease` is true or if the tag's version has a prerelease part.
Build the three download URLs from the tag:
`https://github.com/rune-task-runner/rune/releases/download/<tag>/<file>`.

**Rationale**: GitHub defines "latest" as the newest release that is neither draft nor prerelease,
which is FR-006, and `scripts/install.sh` already relies on it. The second check costs nothing and
covers a release mislabelled by hand. Building URLs from the tag means the only thing taken from the
API response is one version string, which is then proven by the signature.

**Rate limits**: Anonymous calls are limited per address. A 403 or 429 with
`x-ratelimit-remaining: 0` is reported as a rate limit with the reset time from `x-ratelimit-reset`.

**Alternatives considered**: Read the redirect of `/releases/latest` on github.com (no rate limit,
but it is a web page, not an interface). Send `GITHUB_TOKEN` when set (the spec says anonymous access
is enough, and it avoids handling a secret).

### R11. Order of work

**Decision**:

1. Refuse on conflicts (R15), print the installed version, then decide the install origin (R12).
   No network yet.
2. Resolve the real path, take the lock (R14). This also proves the directory is writable.
3. Ask for the latest release. Compare versions with `internal/semver`. Stop if current or newer.
4. Download `checksums.txt` and the signature file. Verify (R6 to R9).
5. Look up the archive name for this platform in the verified list. Absent means unsupported.
6. Download the archive to a staging file beside the binary, hashing as it streams. Compare.
7. Extract the one binary to a second staging file (R13). Copy the old permissions.
8. Replace (R14). Report.

**Rationale**: Every refusal that needs no network happens before the network. Nothing from the
archive is opened until the list it is checked against is proven. The archive name
`rune_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) matches `.goreleaser.yaml` and
`scripts/install.sh`.

### R12. Recognising who owns the install

**Decision**: First match wins, evaluated on the path after symbolic links are resolved.

| Order | Origin | Signal |
|---|---|---|
| 1 | Development build | Reported version is `dev` or does not parse as a version |
| 2 | `go install` | Version was not stamped at build time but resolves from module build info |
| 3 | Container image | Environment has `RUNE_CONTAINER=1`, set by both Dockerfiles |
| 4 | Homebrew | Path has a `Cellar` or `Caskroom` segment |
| 5 | Scoop | Path is under `<scoop root>/apps/`, where the roots are `$SCOOP`, `$SCOOP_GLOBAL`, or a `scoop` path segment followed by `apps` (case-insensitive) |
| 6 | Standalone | None of the above |

**Rationale**: Rows 1 and 2 reuse what `cmd/rune/buildinfo.go` already knows: release builds stamp
`main.version`, `go install` builds do not but still report a release. Homebrew installs Rune as a
cask, which lives in `Caskroom` and is linked into the `bin` directory, so resolving the link finds
it (US4 scenario 6). Scoop starts the real file under `apps\rune\current`. A container has no
reliable path signal across Docker, Podman and Kubernetes, so the image declares itself. A wrong
match in rows 3 to 5 only causes a refusal with guidance, never an unwanted overwrite. An image
without the variable falls through to standalone and stops at the permission check, because the image
runs as a non-root user and the binary is root-owned.

**Guidance printed**: `brew upgrade rune`, `scoop update rune`,
`go install github.com/rune-task-runner/rune/cmd/rune@latest`, and for the container a pointer to
pull `ghcr.io/rune-task-runner/rune:latest`.

**Alternatives considered**: Ask `brew` or `scoop` (starts another program and is slow). Detect a
container from `/.dockerenv` (Docker only).

### R13. Taking the binary out of the archive

**Decision**: Read entries in order and accept exactly one regular file named `rune` (`rune.exe`
in the zip). Copy its bytes to a staging file that Rune names itself. Ignore every other entry
without touching the disk. Fail if the binary is missing, appears twice, is not a regular file, or
is larger than the cap in R16.

**Rationale**: Release archives legitimately hold `LICENSE` and `README`, so other entries are
skipped, not errors. Because no path from the archive is ever used to build a destination, a
`../` entry or a link entry has nowhere to go (FR-013).

### R14. Replacing the file and keeping two upgrades apart

**Decision**: Standard library only, written in `internal/selfupgrade` with one file per platform
family.

- **Staging**: `os.CreateTemp` in the binary's own directory, names starting `.rune-upgrade-`.
  Same directory means same filesystem, so the final step is a rename. The staged binary is given
  the old file's permission bits and synced before the swap.
- **Unix**: `rename(staged, target)`, then sync the directory. Atomic.
- **Windows**: A running program's file cannot be overwritten but can be renamed. Rename the target
  to `rune.exe.old`, rename the staged file to the target, and rename back if the second step
  fails. A leftover `rune.exe.old` is deleted at the start of the next `--self-upgrade` run. A
  leftover that is still running cannot be deleted or renamed onto, so the target then moves to
  `rune.exe.old.1` (and so on); all of them are removed once free. If the move back fails, the
  message says the binary is missing and names the file to rename (code review, round 1).
- **Lock**: Create `.rune-upgrade.lock` beside the binary with exclusive-create. If it exists and
  is younger than 15 minutes, report that an upgrade is in progress and exit. If it is older, take
  it over: only the process that exclusively creates `.rune-upgrade.lock.takeover` may remove the
  stale lock and create a fresh one; everyone else backs off. (Renaming the stale lock away was
  rejected during implementation: a rename can just as well move a lock another process created a
  moment earlier.) The lock records its owner's process id and host name. A lock naming a process
  on this host that no longer exists is taken over at once, by the same marker procedure; a lock
  with no owner, or from another host, is judged by age alone (code review, round 1). A run
  removes the lock only while the file still names it, and checks that again just before the
  replacement, so a run that was paused past the limit and lost its lock neither replaces the
  binary nor deletes the new owner's lock (code review, round 2). The lock
  is held from before the first network call to the end and removed on every return path.
- **Cleanup**: Staging files are removed on every return path. Staging files left by a
  killed run are swept when the lock is taken: the lock holder owns the directory, so every such
  file is a leftover whatever its age.

**Rationale**: The whole run is bounded at 10 minutes (R16), so a lock older than 15 minutes cannot
belong to a live upgrade. That rule needs no process inspection and behaves the same on all three
systems; the owner check only shortens the wait after a killed run and never extends a lock's life. Failing to create the lock is also the earliest possible "not writable" answer, before any
download.

**Known limit on Windows**: Between the two renames there is no `rune.exe`. If the process is killed
in that instant the old binary survives as `rune.exe.old` and must be renamed back by hand. Windows
has no atomic replacement for a running program's file. This narrows FR-015 on one platform, is
documented in the installation guide, and is recorded in Complexity Tracking.

**Alternatives considered**: `github.com/minio/selfupdate` (same two-step on Windows, another
dependency for about a hundred lines). Advisory file locks through `golang.org/x/sys` (released
automatically on death, but leave a lock file behind for good or need delete-while-locked handling
that differs per system). Staging in the system temporary directory (may be another filesystem, so
no atomic rename).

### R15. Reading the flag

**Decision**: `--self-upgrade` is a boolean on the root command's local flags, next to `--list` and
the others. Before Cobra dispatches, `run` in `cmd/rune/main.go` parses a copy of the arguments with
a throwaway root built by the same constructor. If the flag is set and anything else is present
(a positional argument, or any changed flag other than `--color` and `--help`), it returns a usage
error (exit 2) that names the conflict. Otherwise the normal dispatch runs, and the root `RunE`
starts the upgrade before `cli.Run`, which is where Runefile discovery begins.

**Rationale**: Cobra answers `--version` before any hook runs, so a check inside `RunE` could not
reject `rune --self-upgrade --version`. Parsing a copy uses the real flag definitions, so there is no
second list of flags to keep in step, and the throwaway root means `--set` values are not collected
twice. Because the root flag set already stops at the first positional, `rune deploy --self-upgrade`
and `rune deploy -- --self-upgrade` never set the flag (FR-004) with no new code.

`--help` keeps its usual precedence and prints help. It contacts nothing, and people expect it to
work in any combination.

**Alternatives considered**: A `self-upgrade` subcommand (the issue asks for a flag, and a
subcommand would shadow a task of that name). Scanning `os.Args` by hand (needs to know which flags
take values).

### R16. Limits

**Decision**:

| Limit | Value |
|---|---|
| Connect | 10 s |
| TLS handshake | 10 s |
| Wait for response headers | 15 s |
| No bytes received on a body | 30 s |
| Whole run | 10 min |
| Release answer, `checksums.txt`, signature file | 1 MiB each |
| Archive | 100 MiB |
| Extracted binary | 200 MiB |
| Redirects | 5, HTTPS only |
| Retries | none |

**Rationale**: The largest archive today is 4.5 MB and will roughly double (R6), so 100 MiB is far
above any real release and far below a disk-filling download. With no retries, a machine with no
route fails in at most 10 seconds and a silent server in 15, inside SC-005's 60 seconds. The limits
are constants, not flags.

The same limits govern the trust-root fetch (R7). The connection settings keep
`http.ProxyFromEnvironment`, so the standard proxy variables work.

Hosts contacted: `api.github.com`, `github.com` and the host its downloads redirect to, and
`tuf-repo-cdn.sigstore.dev`. The installation guide lists them for people behind a proxy allow-list.

### R17. Release-side and exit-code changes the flag needs

**Decision**:

- `Dockerfile` and `Dockerfile.goreleaser` gain `ENV RUNE_CONTAINER=1` (R12).
- `internal/cli/exit.go` gains one error type that maps to exit 1 for "the command could not do what
  was asked" outside a task. Today exit 1 is reachable only through `TaskFailure`.
- Exit codes: 0 upgraded or already current, 1 not upgraded (failure or refusal, including any
  error without a known reason), 2 conflicting arguments, 130 cancelled. `docs/cli.md` gains the rows.

**Rationale**: These are the only edits outside the new package, the root command and the docs. The
image variable is additive and does not change the published file list (SC-010).

### R18. How it is tested

**Decision**: Four layers, all run in Docker locally and natively in CI.

1. **Package tests** in `internal/selfupgrade`. The package takes its endpoints, HTTP client, trust
   material, identity, executable path, platform, environment lookup and clock as fields, so tests
   use an `httptest` server and a private signing authority in `upgradetest` to sign fixtures.
   The library's virtual Sigstore was tried first and dropped: its certificates carry no
   certificate transparency timestamp, which the updater requires.
   One golden test verifies the real v0.7.0 `checksums.txt` and signature file from `testdata`
   against a committed snapshot of the trust root, with the production identity. Verification uses
   the logged signing time, so this stays valid offline.
2. **Root command tests** in `cmd/rune` for the conflict rules and the dispatch order.
3. **Binary tests** in `test/integration`. A new build-tagged hook file, in the style of
   `versionhook_runetest.go`, lets a binary built with `-tags runetest` read the endpoint base URL,
   a trust-root file and a test certificate authority from `RUNE_TEST_UPGRADE_*` variables. The
   untagged file returns the production constants, so a released binary has no such switch. The
   test binary is not stamped at build time, so the same tagged build treats a set
   `RUNE_TEST_VERSION` as a stamped version; otherwise origin detection (R12) would read every
   test install as a `go install` build. `RUNE_TEST_UNSTAMPED=1` turns that off for the one test
   that wants a `go install` build. The test builds
   a second binary stamped `9.9.9`, packs it into a signed fixture release, upgrades a copy of the
   test binary reporting `0.0.1`, and runs the replaced file with `--version`.
4. **Docs harness** in `test/docs` picks up the new flag rows and links.

Tests that need an unwritable directory skip when run as root, which is the case inside the Docker
harness. The CI matrix runs them as a normal user on all three systems. The Windows replacement path
is compiled only on Windows and is covered by the Windows CI job.

**Settled during implementation**: the fixture authority's trust material is written as a
trust-root file (`root.NewTrustedRoot` plus `MarshalJSON`), so layer 3 covers the success path too.

**Rationale**: Constitution Principle VI and the two stored review rules (every new function has a
unit test, target 90 percent coverage of the new package).

### R19. No network anywhere else

**Decision**: The updater is constructed only inside the `--self-upgrade` branch. An integration
test runs `--list`, `--version`, `--help`, `version` and a task with `HTTPS_PROXY` and `HTTP_PROXY`
pointing at a local listener that counts connections, and asserts zero.

**Rationale**: FR-005 and SC-007 need a check that fails if someone later adds an update notice.
