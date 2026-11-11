---

description: "Task list for spec 035: Go 1.27 toolchain and rune --self-upgrade"
---

# Tasks: Go 1.27 Toolchain and `rune --self-upgrade`

**Input**: Design documents from `/specs/035-go127-self-upgrade/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer) and the issue's acceptance
criteria. Every test that covers new behavior is written and seen failing before the code that makes
it pass. Every new function gets a unit test, and `internal/selfupgrade` reaches 90 percent coverage.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US6)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Lint runs in the linter image: `docker run --rm -v "$PWD":/src -w /src golangci/golangci-lint:v2.14 golangci-lint run`.
- Message texts, exit codes and file names are fixed by
  [contracts/cli-self-upgrade.md](contracts/cli-self-upgrade.md) and
  [data-model.md](data-model.md). Tests pin them as literal strings.
- No test may contact the network. Every test builds its release with the `upgradetest` package
  (T014) or uses the committed `testdata`.
- A test that needs an unwritable directory calls `t.Skip` when `os.Geteuid() == 0` (the Docker
  harness runs as root). CI runs it as a normal user.
- No em dash in code, comments or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass. Golden files change only by deliberate regeneration.
- `Rn` means decision *n* in [research.md](research.md).

## Phase order

The spec ranks the toolchain story (US6) as P2, but the verification library needs `go 1.27.0`
(R6), so US6 runs first and lands as its own commit. Among the P1 stories, US2 (verification) is
built before US1 (the upgrade itself) so that no version of the upgrade ever exists without the
checks.

---

## Phase 1: Setup

**Purpose**: A branch and a known baseline.

- [X] T001 Create and switch to branch `035-go127-self-upgrade` from `main` (`git switch -c 035-go127-self-upgrade`). The spec directory `specs/035-go127-self-upgrade/` is untracked and comes along.
- [X] T002 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...` on the untouched tree and confirm it is green on Go 1.25. If anything fails, report it and stop.
- [X] T003 Record the release file list before any change: run `goreleaser release --snapshot --clean` (or `rune release-dryrun`) and save `ls dist | sed -E 's/[0-9]+\.[0-9]+\.[0-9]+-next/VERSION/g' | sort` to a scratch file outside the repository. If `goreleaser` is not installed on the host, say so and take the list from the `release-dryrun` job log of the latest `main` CI run with `gh run view --log`.

---

## Phase 2: User Story 6: Rune builds, tests and releases on Go 1.27 (Priority: P2, done first)

**Goal**: Every place that selects or states the Go version says 1.27, all seven gates pass, and the
release file list is unchanged ([contracts/toolchain.md](contracts/toolchain.md)).

**Independent Test**: Run the gate commands in [quickstart.md](quickstart.md) section 1 and the
search in [contracts/toolchain.md](contracts/toolchain.md); compare the release file list with T003.

- [X] T004 [US6] In `go.mod` change `go 1.25.0` to `go 1.27.0`. Add no `toolchain` line (R1).
- [X] T005 [P] [US6] In `Dockerfile` change `ARG GO_VERSION=1.25` to `ARG GO_VERSION=1.27`. In `Dockerfile.test` change `ARG GO_VERSION=1.25` to `1.27` and the comment `golang:1.25-bookworm` to `golang:1.27-bookworm`.
- [X] T006 [P] [US6] In `.github/workflows/ci.yml` change all six `go-version: "1.25"` to `"1.27"` (keep `check-latest: true`). In the same file change the golangci-lint `version: v2.12` to `v2.13`. First confirm the image is built with Go 1.27: `docker run --rm golangci/golangci-lint:v2.14 golangci-lint version` must print `built with go1.27`. If it does not, find the lowest v2 release that does, use it here and in the conventions above, and report the change (R4).
- [X] T007 [P] [US6] In `.github/workflows/release.yml` and `.github/workflows/pages.yml` change `go-version: "1.25"` to `"1.27"`.
- [X] T008 [P] [US6] In `docs/installation.md` replace "Requires Go 1.24 or newer (the project builds on Go 1.25):" with "Requires Go 1.27 or newer:" and add one sentence where platforms are described that macOS 13 or newer is required. In `CONTRIBUTING.md` replace the bullet "**Go 1.24+** (the project builds on Go 1.25; CI derives the version from `go.mod`)." with "**Go 1.27+**."
- [X] T009 [US6] Rebuild the test image (`docker compose build test`), confirm `docker compose run --rm test go version` prints `go1.27.`, then run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go mod tidy`. Expect `go.mod` to be rewritten into at most two `require` blocks. No dependency version may change; if one does, report it and stop.
- [X] T010 [US6] Run the gates on Go 1.27 and fix only what the version change breaks: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...` with `-e CGO_ENABLED=1`, `go test ./test/docs/...`, the lint command from the conventions, and the golden check `go test ./internal/diag ./internal/lexer ./internal/parser ./internal/cli ./test/corpus`. If a golden file or an assertion differs only because Go 1.27 changed an error text (for example from `encoding/json`), regenerate it with `-update` and list each regenerated file in the commit message. Any other failure is a real incompatibility: fix the code, never the test, and change a dependency only if nothing else works (FR-035).
- [X] T011 [US6] Run the search from `specs/035-go127-self-upgrade/contracts/toolchain.md` ("Checks") and confirm it prints nothing. Repeat T003's command on this tree and `diff` the two lists; they must be identical (SC-010). Run `goreleaser check`.
- [X] T012 [US6] Stop and report that Part A is ready, with the output of T010 and T011. Commit only when the maintainer asks. When asked, commit Part A alone with subject `build: move to Go 1.27` and a body listing the linter bump and any regenerated golden files. No Co-Authored-By trailer.

**Checkpoint**: The repository is on Go 1.27 with every gate green. This commit can be merged on its
own.

---

## Phase 3: Foundational (shared pieces of the self-upgrade)

**Purpose**: The dependency, the failure vocabulary, the exit code, and the release fixture that
every later test uses.

**CRITICAL**: No user story work in Phases 4 to 8 begins until T018 is green.

### Tests first

- [X] T013 [P] In `internal/cli/exit_test.go` add a case to the `CodeFor` tests: `CodeFor(&CommandFailure{Err: errors.New("x")}) == ExitTaskFail` (1), and that `errors.Unwrap` returns the inner error. It fails to compile until T016.
- [X] T014 Create package `internal/selfupgrade/upgradetest` in `internal/selfupgrade/upgradetest/release.go` (a test-support package like `internal/styletest`; it is imported only by tests). First add the dependency: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go get github.com/sigstore/sigstore-go@v1.4.0`. Export:
  - `const Issuer = "https://token.actions.githubusercontent.com"` and `const Subject = "https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main"`.
  - `type Options struct { Version string; Platforms [][2]string; Binary []byte; Subject string; Prerelease, Draft bool; OmitSignature, OmitChecksums bool; HoldArchive chan struct{}; MutateArchive, MutateChecksums func([]byte) []byte; ExtraEntries []Entry; LatencyPerChunk time.Duration; CutAfter int64; Status int; Header http.Header }`, with `type Entry struct { Name string; Body []byte; Typeflag byte }`. Zero values mean: version `9.9.9`, all six platform pairs, `Subject` above, a small fake binary.
  - `func New(t testing.TB, o Options) *Release`, which builds, for each platform, `rune_<Version>_<os>_<arch>.tar.gz` (zip with `rune.exe` for windows) holding `LICENSE`, `README.md` and the binary with mode 0755, then `checksums.txt` in the form "64 lowercase hex, two spaces, file name", then signs `checksums.txt` with `ca.NewVirtualSigstore()` from `github.com/sigstore/sigstore-go/pkg/testing/ca` using `Sign(Subject, Issuer, checksums)`, and serializes the result as a standard Sigstore bundle named `checksums.txt.sigstore.json` (build a `protobundle.Bundle` from the entity's certificate, message signature and `TlogEntries()[i].TransparencyLogEntry()`, wrap with `bundle.NewBundle`, `MarshalJSON`). It starts an `httptest.NewTLSServer` that serves `/repos/rune-task-runner/rune/releases/latest` as `{"tag_name":"v<Version>","draft":<Draft>,"prerelease":<Prerelease>}` and the files under `/rune-task-runner/rune/releases/download/v<Version>/`, and registers `t.Cleanup`.
  - Fields and methods on `Release`: `URL string` (use it for both API and download base), `Client *http.Client` (trusts the test server), `Trust root.TrustedMaterial` (the virtual Sigstore), `TrustRootJSON() []byte` (via `root.NewTrustedRoot(root.TrustedRootMediaType01, vs.FulcioCertificateAuthorities(), vs.CTLogs(), vs.TimestampingAuthorities(), vs.RekorLogs())` then `MarshalJSON`), `Requests() int`, `Archive(os, arch string) []byte`, `Checksums() []byte`, `Bundle() []byte`.
  - The mutation fields are applied after signing, so a mutated archive or list no longer matches. `CutAfter` closes the connection after that many body bytes of an archive. `Status` and `Header` override the answer to the latest-release request.
  If `TrustRootJSON` cannot be produced with this API, keep the rest, make the method call `t.Skip`, and report it: R18 then keeps the binary-level success path out of `test/integration`.
- [X] T015 [P] In `internal/selfupgrade/upgradetest/release_test.go` test the fixture itself: the served `checksums.txt` lists all six archives with the SHA-256 of what `Archive` returns; each archive holds exactly `LICENSE`, `README.md` and the binary; `MutateArchive` changes the served bytes but not the list; `TrustRootJSON()` parses with `root.NewTrustedRootFromJSON`.

### Implementation

- [X] T016 In `internal/cli/exit.go` add `type CommandFailure struct{ Err error }` with `Error` and `Unwrap`, documented as "a built-in command could not do what was asked (exit 1)", and map it to `ExitTaskFail` in `CodeFor` before the default branch (R17). Run T013.
- [X] T017 [P] Create `internal/selfupgrade/errors.go` with the package comment, `type Reason string` and one constant per row of "Failure reasons" in `data-model.md`, spelled exactly: `offline`, `timeout`, `rate-limited`, `release-lookup`, `unsupported-platform`, `unverified`, `checksum-mismatch`, `download-failed`, `too-large`, `bad-archive`, `not-writable`, `in-progress`, `replace-failed`. Add `type Error struct { Reason Reason; Installed, Target string; Subject string; Limit string; Until string; Status int; Err error }` whose `Error()` returns the text of the matching row in the "Failed" table of `contracts/cli-self-upgrade.md` (the part after `rune: self-upgrade: `), and `Unwrap`. Placeholders are filled as follows: `Subject` fills `<host>`, `<archive>`, `<file>`, `<directory>` or `<path>`, and `<os>/<arch>` for `unsupported-platform`; `Limit` fills `<limit>`; `Until` fills `<time>` for `rate-limited`; `Target` fills `<version>`; `Status` fills `<status>` for `release-lookup` and is appended to `<cause>` as `HTTP <status>` for `download-failed` when no `Err` is set; `Err` fills `<cause>`; the two URLs are package constants (`https://github.com/rune-task-runner/rune/releases/latest` and the installation guide's "From source" anchor). `Status` is also what callers read to tell a 404 from other answers. Create `internal/selfupgrade/errors_test.go` first, with one table row per reason asserting the literal contract text and that `errors.As` finds `*Error`.
- [X] T018 Create `internal/selfupgrade/upgrade.go` with the types only: `type Outcome string` (`upgraded`, `current`), `type Result struct { Outcome Outcome; Installed, Target, Path string }`, and `type Config struct { Version string; Stamped bool; ExePath string; GOOS, GOARCH string; Getenv func(string) string; Now func() time.Time; HTTP *http.Client; APIBase, DownloadBase string; Trust func(context.Context) (root.TrustedMaterial, error); Issuer, Subject string; Logf func(format string, args ...any) }`, `type Upgrader struct{ cfg Config; dl *downloader }` (the `dl` field is added in T037, built by `New` from `cfg.HTTP`), and `func New(cfg Config) (*Upgrader, error)` that rejects an empty `ExePath`, `GOOS`, `GOARCH`, `APIBase`, `DownloadBase`, `Issuer` or `Subject` and a nil `HTTP`, `Trust`, `Getenv`, `Now` or `Logf`. Add `internal/selfupgrade/upgrade_test.go` with a table test for `New` (written first). Run `go mod tidy`, then `go build ./...` and `go test ./internal/selfupgrade/... ./internal/cli/...`.

**Checkpoint**: The dependency is in, the fixture works, and the shared types compile.

---

## Phase 4: User Story 2: A download that cannot be proven genuine is never installed (Priority: P1)

**Goal**: Three pure functions that decide whether bytes may be installed: signature over the
checksum list, archive against the list, and the one binary out of the archive.

**Independent Test**: `go test ./internal/selfupgrade/ -run 'Verify|Checksum|Extract'` passes with
fixtures that are each wrong in exactly one way, and the real v0.7.0 files verify.

### Tests for User Story 2 (write first, watch them fail)

- [X] T019 [P] [US2] Add real-release test data under `internal/selfupgrade/testdata/v0.7.0/`: download `checksums.txt` and `checksums.txt.sigstore.json` from `https://github.com/rune-task-runner/rune/releases/download/v0.7.0/`. Add `internal/selfupgrade/testdata/trusted_root.json`: a snapshot of the public Sigstore trust root, obtained once with a throwaway program run in the test container and not committed (`opts := tuf.DefaultOptions(); opts.DisableLocalCache = true; c, _ := tuf.New(opts); b, _ := c.GetTarget("trusted_root.json")`). Add `internal/selfupgrade/testdata/README.md` saying where each file came from and on what date. This is the only step in the feature that uses the network, and it is not a test.
- [X] T020 [P] [US2] Create `internal/selfupgrade/verify_test.go` with tests for `verifyChecksums(trust root.TrustedMaterial, issuer, subject string, checksums, bundleJSON []byte) error`:
  - fixture from `upgradetest.New`: passes with `upgradetest.Issuer` and `upgradetest.Subject`;
  - one byte appended to `checksums`: fails;
  - `Options{Subject: "https://github.com/rune-task-runner/rune/.github/workflows/ci.yml@refs/heads/main"}` verified against the production subject: fails;
  - right subject, issuer `https://example.com`: fails;
  - bundle JSON that is empty, `{}`, not JSON, or a JSON array: fails;
  - a second, unrelated `upgradetest.New` release's trust material: fails;
  - the real `testdata/v0.7.0` pair (older cosign form) with `testdata/trusted_root.json` and the production constants: passes; the same pair with a byte appended to `checksums.txt`: fails; the same pair with subject `.../workflows/ci.yml@refs/heads/main`: fails.
  Every failure must satisfy `errors.As` to `*Error` with `Reason == "unverified"`.
- [X] T021 [P] [US2] Create `internal/selfupgrade/checksum_test.go` with tests for `parseChecksums(b []byte) (map[string][32]byte, error)` and `archiveName(version, goos, goarch string) string`:
  - a table over all six published pairs (SC-002): `linux`, `darwin` and `windows`, each with `amd64` and `arm64`, giving `rune_0.8.0_<os>_<arch>.tar.gz` for linux and darwin and `rune_0.8.0_windows_<arch>.zip` for windows; each name must also be present in the real `testdata/v0.7.0/checksums.txt` when the version is `0.7.0`;
  - the real `testdata/v0.7.0/checksums.txt` parses to 12 entries;
  - a line whose hash is not 64 lowercase hex characters, a line without two spaces, and a file name listed twice are errors with `Reason == "unverified"`;
  - blank lines and a missing final newline are accepted.
- [X] T022 [P] [US2] Create `internal/selfupgrade/extract_test.go` with tests for `extractBinary(archivePath, goos string, dst io.Writer, limit int64) error`, using archives built in the test:
  - a tar.gz with `LICENSE`, `README.md` and `rune`: writes exactly the bytes of `rune`;
  - a zip with `rune.exe` for `goos == "windows"`: same;
  - no `rune` entry: `bad-archive`;
  - two `rune` entries: `bad-archive`;
  - `rune` as a symbolic link or a directory entry: `bad-archive`;
  - entries named `../rune`, `/rune`, `./rune` and `dir/rune` are not the binary: with only those present, `bad-archive`;
  - an archive that also holds `../../etc/passwd` and a symlink entry next to a valid `rune`: succeeds and creates no file anywhere (assert the temp directory holds only the archive);
  - a `rune` entry larger than `limit`: `too-large`, and no more than `limit` bytes are written;
  - a truncated gzip stream: `bad-archive`.
- [X] T023 [US2] Run `go test ./internal/selfupgrade/ -run 'Verify|Checksum|Extract'` and confirm the new tests fail to compile or fail. Do not continue until they do.

### Implementation for User Story 2

- [X] T024 [US2] Create `internal/selfupgrade/verify.go` with `verifyChecksums` per R6 to R9: load the bundle with `bundle.Bundle.UnmarshalJSON` when the JSON object has a `mediaType` key; otherwise require the keys `base64Signature`, `cert` and `rekorBundle` and map them onto a v0.1 bundle (`mediaType` `application/vnd.dev.sigstore.bundle+json;version=0.1`; certificate DER from the base64 PEM in `cert`; one tlog entry with `logIndex`, `logId.keyId` from the hex `logID`, `kindVersion` `hashedrekord`/`0.0.1`, `integratedTime`, `inclusionPromise.signedEntryTimestamp`, `canonicalizedBody` from `Payload.body`; `messageSignature` with `SHA2_256` digest of `checksums` and the signature). Build the verifier with `verify.WithSignedCertificateTimestamps(1)`, `verify.WithTransparencyLog(1)`, `verify.WithObserverTimestamps(1)`, the identity with `verify.NewShortCertificateIdentity(issuer, "", subject, "")`, and the policy with `verify.WithArtifactDigest("sha256", sum[:])`. Wrap every error as `&Error{Reason: "unverified", Err: err}`. Make T020 pass.
- [X] T025 [P] [US2] Create `internal/selfupgrade/checksum.go` with `parseChecksums` and `archiveName`. Make T021 pass.
- [X] T026 [P] [US2] Create `internal/selfupgrade/extract.go` with `extractBinary` per R13: accept only an entry whose name is exactly `rune` (`rune.exe` for windows) and whose type is a regular file, copy through `io.LimitReader(limit+1)` to detect overflow, and never build a path from an entry name. Make T022 pass.

**Checkpoint**: Tampered inputs are rejected at the function level, and the real release verifies
with the production identity.

---

## Phase 5: User Story 1: Upgrade a standalone install with one command (Priority: P1) MVP

**Goal**: `rune --self-upgrade` finds the latest stable release, runs the Phase 4 checks, and
replaces the running binary on Linux and macOS.

**Independent Test**: `go test ./test/integration/ -run SelfUpgrade` upgrades a copy of the test
binary from a signed fixture and the replaced file reports the fixture version with `--version`.

### Tests for User Story 1 (write first, watch them fail)

- [X] T027 [P] [US1] Create `internal/selfupgrade/release_test.go` with tests for `(u *Upgrader) latest(ctx) (tag, version string, err error)` against `upgradetest`:
  - default fixture: returns `v9.9.9`, `9.9.9`; the request carries `Accept: application/vnd.github+json` and a `User-Agent` starting `rune/`;
  - `Options{Prerelease: true}` or `Options{Draft: true}`: `release-lookup` error;
  - `Options{Version: "9.9.9-rc.1"}`: `release-lookup` error (tag has a prerelease part);
  - `Options{Status: 500}`: `release-lookup` error naming the status;
  - a body that is not JSON, or has no `tag_name`, or a tag without the leading `v`: `release-lookup`.
- [X] T028 [P] [US1] Create `internal/selfupgrade/fetch_test.go` with tests for a standalone `downloader` built by `newDownloader(client *http.Client) *downloader`, with methods `fetchBytes(ctx, url string, limit int64) ([]byte, error)` and `fetchFile(ctx, url string, dst *os.File, limit int64) (sum [32]byte, err error)`. It is a type of its own, not methods on `Upgrader`, because the trust-root fetcher is built before any `Upgrader` exists and must share the same bounded download. Both methods return the served bytes; `fetchFile` returns the SHA-256 of what it wrote; a 404 or 500 is `download-failed` naming the file, with `Status` set to the code; a cancelled context returns an error satisfying `errors.Is(err, context.Canceled)`. In the same task create `internal/selfupgrade/trust_test.go` with tests for `tufFetcher`, the adapter that implements go-tuf's `fetcher.Fetcher` (`DownloadFile(urlPath string, maxLength int64, _ time.Duration) ([]byte, error)`) over a `*downloader` and a held context: it returns the served bytes; a 404 (read from `Error.Status`) returns go-tuf's `*metadata.ErrDownloadHTTP` with `StatusCode == 404` (the TUF client needs this to learn that no newer root exists); a body longer than `maxLength` is an error; a cancelled held context is an error satisfying `errors.Is(err, context.Canceled)`.
- [X] T029 [P] [US1] Create `internal/selfupgrade/replace_test.go` (no build tag) with tests for `replaceBinary(staged, target string) error`: the target then has the staged bytes; the staged path no longer exists; a second file in the directory is untouched. Add, in the same file, a test for `stage(dir string) (*os.File, error)`: the file is created in `dir` with a name starting `.rune-upgrade-`.
- [X] T030 [US1] In `internal/selfupgrade/upgrade_test.go` add `Run` tests. A helper installs a fake binary at `<tmp>/bin/rune` with mode 0750 and returns a `Config` wired to an `upgradetest` release (`HTTP: rel.Client`, `APIBase` and `DownloadBase: rel.URL`, `Trust` returning `rel.Trust`, the production issuer and subject, `GOOS`/`GOARCH` `linux`/`amd64`, `Stamped: true`, `Logf` collecting lines). Cases:
  - `Version: "0.7.0"`: `Result{Outcome: "upgraded", Installed: "0.7.0", Target: "9.9.9", Path: <resolved path>}`; the file equals the fixture binary; its mode is still 0750; the log lines are exactly the first four lines of the "Upgraded" block in `contracts/cli-self-upgrade.md` with these versions (`installed 0.7.0`, `latest 9.9.9`, `downloading rune_9.9.9_linux_amd64.tar.gz`, `verified signature and checksum`); the directory holds only `rune`;
  - the same upgrade once per published pair, with `GOOS` and `GOARCH` injected for each of the six (the binary is named `rune.exe` for windows): each picks its own archive and installs the fixture binary (SC-002; on a non-Windows host the Windows rows exercise selection and extraction, and the replacement itself is covered by T048);
  - `Version: "9.9.9"`: `current`, file unchanged, and no request beyond the latest-release one (`rel.Requests() == 1`);
  - `Version: "10.0.0"` and `Version: "10.0.0-rc.1"`: `current`, file unchanged;
  - `ExePath` is a symbolic link `<tmp>/link` to the binary: `Path` is the real file, the real file is replaced, and `os.Lstat(link)` still reports a symlink with the same target;
  - `GOOS: "windows"` with a fixture limited to linux platforms: `unsupported-platform` naming `windows/amd64`, file unchanged.
- [X] T031 [US2] In `internal/selfupgrade/upgrade_test.go` add the tamper table through `Run` (US2's end-to-end check): `MutateArchive` flipping a byte gives `checksum-mismatch`; `MutateChecksums` appending a line gives `unverified`; `OmitSignature` gives `unverified`; a fixture whose `checksums.txt` answers 404 gives `unverified` (the spec edge case of a release still being published); `Subject` set to the `ci.yml` identity gives `unverified`; `ExtraEntries` with `../../evil` and a symlink still upgrades and creates nothing outside `<tmp>/bin`; `ExtraEntries` with a second `rune` gives `bad-archive`. After every failing case the installed file is byte-identical, its mode is unchanged, and the directory holds only `rune`.
- [X] T032 [P] [US1] In `cmd/rune/root_test.go` (new file) add tests that build the root command with `newRootCmd` and a stub upgrade function: `--self-upgrade` calls the stub once and does not call `cli.Run` (run it in an empty temp directory and in one containing a Runefile with the single line `}{`; both succeed with no Runefile diagnostic on stderr); the root help text contains the line from `contracts/cli-self-upgrade.md`: `--self-upgrade` followed by `upgrade this rune to the latest stable release; runs no task`.
- [X] T033 [P] [US1] Create `cmd/rune/selfupgrade_test.go` with tests for `reportUpgrade(w io.Writer, theme, res selfupgrade.Result, err error) error`: an `upgraded` result prints `upgraded: 0.7.0 -> 9.9.9 (/x/rune)` and returns nil; `current` prints `current: nothing to upgrade` and returns nil; a `*selfupgrade.Error` returns a `*cli.CommandFailure` whose message is `self-upgrade: ` plus the error text; any other non-nil error (for example `errors.New("boom")`) also returns `*cli.CommandFailure` with message `self-upgrade: boom`, so `cli.CodeFor` gives 1 and never 2; an error wrapping `context.Canceled` returns `*cli.Interrupted`.
- [X] T034 [US1] Create `test/integration/self_upgrade_test.go`. In a `sync.Once` helper build a second binary from `./cmd/rune` with `-tags runetest -ldflags "-X main.version=9.9.9 -X main.commit=fixture"`, pass its bytes as `upgradetest.Options.Binary` with `Platforms` set to the host pair, write `rel.TrustRootJSON()` and the test server's CA certificate to temp files, and copy `runeBin` to `<tmp>/bin/rune`. The test binary is unstamped, so the `runetest` build treats a set `RUNE_TEST_VERSION` as a stamped version (T042); without that, T062 would classify every install here as `go-install`. Run the copy with `RUNE_TEST_VERSION=0.0.1`, `RUNE_TEST_UPGRADE_API` and `RUNE_TEST_UPGRADE_DOWNLOAD` set to `rel.URL`, `RUNE_TEST_UPGRADE_ROOT` and `RUNE_TEST_UPGRADE_CA` set to the two files. Tests:
  - `TestSelfUpgradeReplacesBinary`: run with `PATH` set to an empty directory, so no other tool can be used (FR-012): exit 0, stderr starts with `rune: installed 0.0.1` and ends with `upgraded: 0.0.1 -> 9.9.9 (<path>)`, stdout is empty, and running the replaced file with `--version` (no test variables) prints `9.9.9`;
  - `TestSelfUpgradeAlreadyCurrent`: `RUNE_TEST_VERSION=9.9.9` gives exit 0 and `current: nothing to upgrade`;
  - `TestSelfUpgradeIgnoresRunefile`: the working directory is, in turn, empty, one with a Runefile `}{`, and one with `minimum_version "99.0.0"`; each upgrades with no diagnostic;
  - `TestSelfUpgradeTamperedArchive`: `MutateArchive` gives exit 1, the message for `checksum-mismatch`, and a byte-identical binary.
  Skip the file's tests on Windows until T050 (`runtime.GOOS == "windows"`), with a comment naming T050.
- [X] T035 [US1] Run `go test ./internal/selfupgrade/... ./cmd/rune/... ./test/integration/ -run 'Release|Fetch|Replace|Stage|Run|SelfUpgrade|Root|Report'` and confirm the new tests fail to compile or fail.

### Implementation for User Story 1

- [X] T036 [P] [US1] Create `internal/selfupgrade/release.go` with `latest` per R10: `GET <APIBase>/repos/rune-task-runner/rune/releases/latest` with the two headers, body read through the upgrader's `downloader.fetchBytes` with a 1 MiB limit, any `download-failed` from that call remapped to `release-lookup` keeping its `Status` (a bad answer from the release service is not a failed file download; T027 expects `release-lookup` for a 500), `tag_name` parsed with `internal/semver` after trimming one leading `v`, and the draft, prerelease and prerelease-part refusals. Add `(u *Upgrader) assetURL(tag, name string) string` returning `<DownloadBase>/rune-task-runner/rune/releases/download/<tag>/<name>`, with a unit test. Make T027 pass.
- [X] T037 [P] [US1] Create `internal/selfupgrade/fetch.go` with `downloader`, `newDownloader`, `fetchBytes` and `fetchFile`: `http.NewRequestWithContext`, body always closed, non-200 is `download-failed` with the file name and `Status`, a transfer that ends before the declared length is `download-failed`, and the body is read through a limit of `limit+1` bytes so an overflow is detected (the `too-large` classification and the timeouts arrive in T053). In the same task create `internal/selfupgrade/trust.go` with `tufFetcher` and `func PublicTrust(client *http.Client) func(context.Context) (root.TrustedMaterial, error)`, which builds `tuf.DefaultOptions()` with `DisableLocalCache = true` and `Fetcher` set to a `tufFetcher` holding the call's context and `newDownloader(client)`, then returns `root.GetTrustedRoot(tufClient)` (R7). The library's default fetcher has no timeout and retries, so it must not be used. Make T028 pass.
- [X] T038 [P] [US1] Create `internal/selfupgrade/replace_unix.go` with `//go:build !windows` holding `replaceBinary` (`os.Rename`, then open and `Sync` the directory) and, in `internal/selfupgrade/replace.go`, the shared `stage(dir)` using `os.CreateTemp(dir, ".rune-upgrade-*")`. Make T029 pass.
- [X] T039 [US1] In `internal/selfupgrade/upgrade.go` implement `(u *Upgrader) Run(ctx context.Context) (Result, error)` in the order of R11, without the lock and origin steps (they arrive in T054 and T062): log `installed <Version>` as the very first statement, so every later exit (refusal, lock, network) has already printed it; then resolve `ExePath` with `filepath.EvalSymlinks`; `os.Stat` it for the mode; parse `Version` with `internal/semver` (an unparseable version is a plain error for now); `latest`; log `latest <v>`; return `current` when `installed.Compare(target) >= 0`; fetch `checksums.txt` and `checksums.txt.sigstore.json` (1 MiB each; a 404 on either is `unverified` with cause `<file> is not published`, any other fetch failure keeps its own reason); `verifyChecksums` with `cfg.Trust(ctx)`; `parseChecksums`; look up `archiveName` (absent is `unsupported-platform`); log `downloading <archive>`; `fetchFile` into a staged file with a 100 MiB limit and compare the returned sum (`checksum-mismatch`); `extractBinary` into a second staged file with a 200 MiB limit; `Chmod` it to the old permission bits; `Sync` and close; log `verified signature and checksum`; `replaceBinary`. Remove both staged files on every return path with `defer`. Fill `Installed` and `Target` on every `*Error` returned after the release is known. Make T030 and T031 pass.
- [X] T040 [P] [US1] Create `cmd/rune/upgradehook.go` with `//go:build !runetest` and `func upgradeConfig(cfg selfupgrade.Config) (selfupgrade.Config, error)` that sets `APIBase` `https://api.github.com`, `DownloadBase` `https://github.com`, the issuer and subject from R8, a plain `&http.Client{}`, and `Trust` as `selfupgrade.PublicTrust(client)` (T037, R7). Create `cmd/rune/upgradehook_runetest.go` with `//go:build runetest` that starts from the same values and overrides them from `RUNE_TEST_UPGRADE_API`, `RUNE_TEST_UPGRADE_DOWNLOAD`, `RUNE_TEST_UPGRADE_ROOT` (a trust-root JSON path loaded with `root.NewTrustedRootFromPath`) and `RUNE_TEST_UPGRADE_CA` (a PEM file added to the client's root pool) when set. Mirror the header comment of `cmd/rune/versionhook_runetest.go`: these overrides are never compiled into a released binary.
- [X] T041 [US1] Create `cmd/rune/selfupgrade.go` with `reportUpgrade` (T033) and `func runSelfUpgrade(ctx context.Context, opts *cli.Options, version string, stamped bool) error` that builds the `Config` (`ExePath` from `os.Executable()`, `runtime.GOOS`/`GOARCH`, `os.Getenv`, `time.Now`, `Logf` writing `rune: <line>` to `opts.Stderr`), passes it through `upgradeConfig`, calls `New` and `Run`, and returns `reportUpgrade`'s result. `reportUpgrade` maps every non-nil error that is not a cancellation to `*cli.CommandFailure`. Use the existing status style that `--clear-cache` uses for its `cleared:` label for `upgraded:` and `current:`. Make T033 pass.
- [X] T042 [US1] In `cmd/rune/root.go` add `var selfUpgrade bool`, register `f.BoolVar(&selfUpgrade, "self-upgrade", false, "upgrade this rune to the latest stable release; runs no task")` after `--ignore-version`, and make `RunE` call the upgrade function and return when the flag is set, before `cli.Run`. Give `newRootCmd` a parameter for the upgrade function so T032 can pass a stub; in `cmd/rune/main.go` pass a closure over `runSelfUpgrade` with `installedVersion(resolveVersion(version))` and `stamped := installedStamped(version != devVersion)`. Add `installedStamped(stamped bool) bool` to `cmd/rune/versionhook.go` (returns its argument) and to `cmd/rune/versionhook_runetest.go` (returns true when `RUNE_TEST_VERSION` is set and `RUNE_TEST_UNSTAMPED` is not `1`, otherwise its argument), with the same kind of comment the existing hook carries. Update every existing caller of `newRootCmd` (`cmd/rune/main.go` and any test in `cmd/rune`) for the new parameter. Make T032 pass. If a help golden or `test/integration/cli_help_test.go` assertion changes because of the new flag line, regenerate it deliberately and name it in the commit.
- [X] T043 [US1] Run `go test ./internal/selfupgrade/... ./cmd/rune/... ./test/integration/ -run SelfUpgrade` and make T034 pass. Then run the full suite: `go test ./...`.

**Checkpoint**: A standalone install on Linux or macOS upgrades end to end, and tampered releases
are refused through the real command. Do not release yet: failures, locking and Windows are Phase 6.

---

## Phase 6: User Story 3: A failed or interrupted upgrade leaves a working Rune (Priority: P1)

**Goal**: Bounded waits and sizes, distinct failure messages, one upgrade at a time, clean
directories after every exit, and replacement on Windows.

**Independent Test**: `go test ./internal/selfupgrade/ -run 'Lock|Limit|Fail|Cancel|Concurrent|Windows'`
and `go test ./test/integration/ -run 'SelfUpgrade.*(Cancel|Concurrent|Offline)'` pass, and after
each case the installed binary still runs.

### Tests for User Story 3 (write first, watch them fail)

- [X] T044 [P] [US3] Create `internal/selfupgrade/lock_test.go` with tests for `acquireLock(dir string, now time.Time) (release func(), err error)` and `sweepStale(dir string, now time.Time)`:
  - first call creates `<dir>/.rune-upgrade.lock`; `release` removes it;
  - a second call while held: `in-progress`;
  - an existing lock whose modification time is 14 minutes old: `in-progress`; 16 minutes old: taken over, and exactly one lock file remains;
  - 20 goroutines calling it at once on a 16-minute-old lock: exactly one succeeds;
  - `dir` unwritable (skip as root): `not-writable` with `Subject == dir`;
  - `sweepStale` removes `.rune-upgrade-*` files older than 15 minutes and keeps younger ones and unrelated files.
- [X] T045 [P] [US3] In `internal/selfupgrade/fetch_test.go` add limit and classification tests. Make the limits fields on `downloader`, set by `newDownloader` from named constants in `fetch.go`, so tests can shorten them on a downloader they build:
  - a server that accepts and never answers: `timeout` within the shortened header limit;
  - a body that stalls after the first chunk (`LatencyPerChunk` above the shortened idle limit): `timeout`;
  - a closed port (`APIBase` `https://127.0.0.1:1`): `offline`;
  - a host that does not resolve (transport `DialContext` returning `*net.DNSError`): `offline`;
  - `Options{Status: 403, Header: {"X-Ratelimit-Remaining": ["0"], "X-Ratelimit-Reset": ["<unix time>"]}}` and the same with 429: `rate-limited`, message containing that time formatted in local time;
  - a body larger than `limit`: `too-large` with `Limit` set, and at most `limit+1` bytes read;
  - `Content-Length` above `limit`: `too-large` before any body byte is read;
  - a redirect to an `http://` URL and a chain of 6 redirects: error;
  - `Options{CutAfter: 1000}`: an error, not a short success;
  - assert no request is retried (`rel.Requests()` equals the number of distinct fetches).
  - in `internal/selfupgrade/trust_test.go`: a trust-root host that accepts and never answers makes `tufFetcher.DownloadFile` return within the shortened header limit, a stalled body returns within the shortened idle limit, and the fixture counts exactly one request per call (no retries); through `Run`, a `Trust` function that returns a `timeout` or `offline` `*Error` surfaces with that reason and leaves the binary unchanged.
  Also assert the production constants equal the table in R16: 10 s, 10 s, 15 s, 30 s, 10 min, 1 MiB, 100 MiB, 200 MiB, 5 redirects.
- [X] T046 [US3] In `internal/selfupgrade/upgrade_test.go` add failure tests through `Run`. After each, assert the binary is byte-identical, still mode 0750, and the directory holds only `rune`:
  - `CutAfter` on the archive: `download-failed` naming the archive (US3 scenario 1);
  - an error with no reason injected through the unexported hook: `Run` returns it unchanged, and the binary and directory are untouched;
  - a context cancelled from inside the fixture handler while the archive is being served: an error satisfying `errors.Is(err, context.Canceled)`;
  - a context whose deadline passes during the download: `timeout`;
  - the binary's directory unwritable (skip as root): `not-writable` naming the directory, and `rel.Requests() == 0`;
  - a 14-minute-old `.rune-upgrade.lock` present: `in-progress`, zero requests, and the foreign lock file is still there;
  - a 16-minute-old lock and a 16-minute-old `.rune-upgrade-abc` file present: upgrade succeeds and neither remains;
  - an archive larger than a shortened archive limit: `too-large`;
  - `replaceBinary` failing (inject through an unexported field on `Upgrader`): `replace-failed`.
- [X] T047 [US3] In `internal/selfupgrade/upgrade_test.go` add `TestRunConcurrent`: 8 goroutines, released together from one start gate (a closed channel), call `Run` on the same install. Add `HoldArchive chan struct{}` to `upgradetest.Options`: while it is open the fixture does not answer archive requests. The test closes it only after the fixture has seen the winner's archive request and the other 7 goroutines have returned, so no late starter can take the lock after the winner finishes. Exactly one returns `upgraded`; every other returns `in-progress`; the final file equals the fixture binary; the directory holds only `rune`. Run it with `-race -count=20`.
- [X] T048 [P] [US3] Create `internal/selfupgrade/replace_windows_test.go` with `//go:build windows`: `replaceBinary` on a target that is the running test executable's copy held open by a child process succeeds, leaves `<target>.old` with the old bytes, and the target has the new bytes; when the second rename is made to fail (staged path missing) the target is restored with the old bytes; `removeOld(target)` deletes a leftover `<target>.old` and is a no-op when none exists.
- [X] T049 [P] [US3] In `test/integration/self_upgrade_test.go` add:
  - `TestSelfUpgradeCancel`: start the copy with `LatencyPerChunk` set, send it an interrupt once the fixture has served the first archive chunk, and assert exit 130, a byte-identical binary that still runs `--version`, and a directory holding only the binary (skip on Windows, where the test cannot deliver the signal);
  - `TestSelfUpgradeConcurrentProcesses`: start four processes on the same install at once with the fixture's `HoldArchive` open; wait until three have exited, then close it; exactly one prints `upgraded:`, the rest exit 1 with the `in-progress` message, and the file runs `--version` as `9.9.9`;
  - `TestSelfUpgradeOffline`: `RUNE_TEST_UPGRADE_API=https://127.0.0.1:1` gives exit 1 and the `offline` message within 60 seconds (SC-005);
  - `TestSelfUpgradeUnwritable` (skip as root and on Windows): exit 1, the `not-writable` message containing `rune does not ask for elevated rights`, an unchanged binary, and a run that ends without waiting for input (stdin is closed).
- [X] T050 [US3] Remove the Windows skip added in T034 for the tests that do not send signals, and add `TestSelfUpgradeWindowsLeavesOld` with `//go:build windows` in `test/integration/self_upgrade_windows_test.go`: after an upgrade `rune.exe` reports `9.9.9` and `rune.exe.old` exists; a second `--self-upgrade` (version now current) removes `rune.exe.old`.
- [X] T051 [US3] Run the Phase 6 tests and confirm they fail to compile or fail.

### Implementation for User Story 3

- [X] T052 [P] [US3] Create `internal/selfupgrade/lock.go` with `acquireLock` and `sweepStale` per R14: `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0o600)`; on `fs.ErrExist` stat the file and return `in-progress` when younger than 15 minutes, otherwise rename it to a unique `.rune-upgrade-stale-*` name (only one renamer succeeds; a rename error means another process won, so return `in-progress`), remove that, and create again; on a permission error return `not-writable`. Make T044 pass.
- [X] T053 [US3] In `internal/selfupgrade/fetch.go` add the limits and the classification per R16: named constants; a transport that keeps `Proxy: http.ProxyFromEnvironment` (people behind a proxy, and T068, rely on it) with dial 10 s, TLS handshake 10 s and response-header 15 s where the client has no transport of its own; `CheckRedirect` allowing at most 5 hops and only `https`; an idle timer that cancels the request when no body bytes arrive for 30 s; a `Content-Length` check before reading; `too-large` on overflow; and a function `classify(err error, host string) error` mapping `context.DeadlineExceeded` and timeout `net.Error`s to `timeout`, `*net.DNSError` and connection-refused or unreachable `*net.OpError`s to `offline`, and passing `context.Canceled` through unwrapped. In `internal/selfupgrade/release.go` map 403 and 429 with `X-Ratelimit-Remaining: 0` to `rate-limited`. No retries anywhere. Because `tufFetcher` already goes through `downloader`, confirm with T045 that it now has the same limits and `classify`, so the trust-root host is bounded like every other (FR-028, Principle VIII). Make T045 pass.
- [X] T054 [US3] In `internal/selfupgrade/upgrade.go` wrap `Run` in `context.WithTimeout(ctx, 10*time.Minute)`; right after resolving the path call `acquireLock(dir, cfg.Now())` and `defer` its release, then `sweepStale`; map staging-file creation and `Chmod` errors to `not-writable` and `replaceBinary` errors to `replace-failed`. Keep cleanup in `defer` so it runs on cancel. In `cmd/rune/selfupgrade.go` make sure an error that `errors.Is` `context.Canceled` becomes `*cli.Interrupted` (exit 130). Make T046, T047 and T049 pass.
- [X] T055 [P] [US3] Create `internal/selfupgrade/replace_windows.go` with `//go:build windows` holding `replaceBinary` per R14 (remove a leftover `<target>.old`; rename target to `<target>.old`; rename staged to target; on failure rename `<target>.old` back and return the error) and `removeOld(target string)`. In `internal/selfupgrade/replace_unix.go` add a no-op `removeOld`. Call `removeOld` in `Run` right after the lock is taken. Make T048 and T050 pass in the Windows CI job; locally confirm `GOOS=windows go build ./...` and `GOOS=windows go vet ./internal/selfupgrade/` succeed in the container.

**Checkpoint**: All three P1 stories are complete. This is the first state that is safe to release.

---

## Phase 7: User Story 4: Installs owned by something else are pointed at the right upgrade path (Priority: P2)

**Goal**: Homebrew, Scoop, `go install`, container and development builds are recognised before any
network use, left unchanged, and told what to run.

**Independent Test**: `go test ./internal/selfupgrade/ -run Origin` and
`go test ./test/integration/ -run 'SelfUpgradeRefus'` pass; each refusal exits 1 with zero requests.

### Tests for User Story 4 (write first, watch them fail)

- [X] T056 [P] [US4] Create `internal/selfupgrade/origin_test.go` with a table test for `detectOrigin(version string, stamped bool, resolvedPath, goos string, getenv func(string) string) Origin`, first match wins in the order of R12:
  - version `dev`, or `not-a-version`, any path: `development`;
  - version `0.7.0`, `stamped == false`: `go-install`;
  - `RUNE_CONTAINER=1`: `container`; `RUNE_CONTAINER=0` or empty: not container;
  - `/opt/homebrew/Caskroom/rune/0.7.0/rune`, `/usr/local/Cellar/rune/0.7.0/bin/rune`, `/home/linuxbrew/.linuxbrew/Caskroom/rune/0.7.0/rune`: `homebrew`;
  - `C:\Users\me\scoop\apps\rune\0.7.0\rune.exe` and `C:\Users\me\Scoop\Apps\rune\current\rune.exe` with goos `windows`: `scoop`; `D:\tools\apps\rune\0.7.0\rune.exe` with `SCOOP=D:\tools`: `scoop`; the same with `SCOOP_GLOBAL=D:\tools`: `scoop`;
  - `/usr/local/bin/rune`, `/home/me/.local/bin/rune`, `/home/me/cellar-door/rune`, `C:\tools\rune.exe`: `standalone`;
  - a development version inside a Homebrew path: `development` (order).
  Add a table test for `(*Refused).Error()`: one row per origin, asserting the literal text of the matching line in the "Refused" block of `contracts/cli-self-upgrade.md` (the part after `rune: self-upgrade: `).
- [X] T057 [US4] In `internal/selfupgrade/upgrade_test.go` add: for each non-standalone origin, `Run` returns a `*Refused` with that origin and the resolved path, `rel.Requests() == 0`, no lock file was created, and the binary is unchanged. Add the link case from US4 scenario 6: `ExePath` is `<tmp>/home/bin/rune`, a symbolic link to `<tmp>/opt/homebrew/Caskroom/rune/0.7.0/rune`: `homebrew`. Change the T039 expectation for an unparseable version from a plain error to `*Refused` with `development`.
- [X] T058 [P] [US4] In `cmd/rune/selfupgrade_test.go` add: `reportUpgrade` with a `*selfupgrade.Refused` returns `*cli.CommandFailure` (exit 1) with message `self-upgrade: ` plus the refusal text.
- [X] T059 [P] [US4] In `test/integration/self_upgrade_test.go` add `TestSelfUpgradeRefusals`, each asserting exit 1, a first stderr line `rune: installed <version>`, the contract line, an unchanged binary and zero fixture requests: `RUNE_CONTAINER=1`; no `RUNE_TEST_VERSION` (the test binary reports `dev`); `RUNE_TEST_VERSION=0.0.1` with `RUNE_TEST_UNSTAMPED=1` (reads as a `go install` build and prints the `go install` line); the copy placed at `<tmp>/Caskroom/rune/0.7.0/rune` with `RUNE_TEST_VERSION=0.0.1`; and the same reached through a symbolic link from `<tmp>/bin/rune`.
- [X] T060 [US4] Run the Phase 7 tests and confirm they fail to compile or fail.

### Implementation for User Story 4

- [X] T061 [US4] Create `internal/selfupgrade/origin.go` with `type Origin string` (`development`, `go-install`, `container`, `homebrew`, `scoop`, `standalone`), `detectOrigin` per R12 (split the path on both separators; `Cellar` and `Caskroom` segments are matched exactly; Scoop matching is case-insensitive), and `type Refused struct { Origin Origin; Path, Version string }` with the contract texts. Make T056 pass.
- [X] T062 [US4] In `internal/selfupgrade/upgrade.go` call `detectOrigin` right after the path is resolved and before `acquireLock`; return `*Refused` for anything but `standalone`. In `cmd/rune/selfupgrade.go` map `*Refused` in `reportUpgrade`. Make T057, T058 and T059 pass.
- [X] T063 [P] [US4] Add `ENV RUNE_CONTAINER=1` to the final stage of `Dockerfile` and to `Dockerfile.goreleaser`, each with a one-line comment that `rune --self-upgrade` reads it and declines. Verify with `docker buildx build --build-arg VERSION=0.0.1 -t rune:local --load . && docker run --rm rune:local --self-upgrade; echo $?`: prints `rune: installed 0.0.1`, the container line and `1`. The build argument matters: the image defaults to `VERSION=dev`, and a development build is refused before the container check is reached.

**Checkpoint**: No package-manager, `go install`, container or development install can be
overwritten.

---

## Phase 8: User Story 5: The flag cannot be confused with running a task (Priority: P2)

**Goal**: `--self-upgrade` stands alone. Any other argument or flag except `--color` and `--help` is
a usage error before anything starts, and existing command lines are read exactly as before.

**Independent Test**: `go test ./cmd/rune/ -run Conflict` and
`go test ./test/integration/ -run 'SelfUpgrade(Conflict|Passthrough)'` pass.

### Tests for User Story 5 (write first, watch them fail)

- [X] T064 [P] [US5] In `cmd/rune/root_test.go` add a table test for `selfUpgradeConflict(args []string) error`:
  - nil for `["--self-upgrade"]`, `["--self-upgrade","--color","never"]`, `["--color=always","--self-upgrade"]`, `["--self-upgrade","--self-upgrade"]`, `["--self-upgrade","--help"]`, `["--self-upgrade","-h"]`;
  - nil when the flag is not set at root level: `["build"]`, `["deploy","--self-upgrade"]`, `["deploy","--","--self-upgrade"]`, `["--","--self-upgrade"]`, `["serve","--self-upgrade"]`, `["--list"]`, `[]`, `["--no-such-flag"]`;
  - `*cli.UsageError` with message `--self-upgrade cannot be combined with task arguments` for `["--self-upgrade","build"]`, `["--self-upgrade","X=1"]`, `["--self-upgrade","--","build"]`, `["--self-upgrade","serve"]`;
  - `*cli.UsageError` with message `--self-upgrade cannot be combined with <flag>` for each of `--list`, `--version`, `--dry-run`, `--summary`, `--dump`, `--format json`, `--fmt`, `--watch`, `--choose`, `--clear-cache`, `--file x` (and `-f x`, reported as `--file`), `--set A=1`, `--yes`, `--quiet`, `--ignore-version`, in both orders relative to `--self-upgrade`.
  Add a test that iterates every flag registered on the root command and asserts each one other than `self-upgrade`, `color` and `help` conflicts, so a flag added later cannot be forgotten.
- [X] T065 [P] [US5] Create `test/integration/self_upgrade_cli_test.go` with:
  - `TestSelfUpgradeConflict`: `--self-upgrade build`, `--self-upgrade --list` and `--self-upgrade --version` each exit 2 with the banner line `rune: --self-upgrade cannot be combined with ...`, print no version, and make zero fixture requests;
  - `TestSelfUpgradePassthrough`: a Runefile whose task `deploy` echoes its arguments; `rune deploy --self-upgrade` and `rune deploy -- --self-upgrade` exit 0, echo `--self-upgrade`, and make zero fixture requests;
  - `TestSelfUpgradeHelp`: `rune --help` lists the flag line, and `rune --self-upgrade --help` exits 0, prints help and makes zero fixture requests.
- [X] T066 [US5] Run the Phase 8 tests and confirm they fail to compile or fail.

### Implementation for User Story 5

- [X] T067 [US5] In `cmd/rune/root.go` add `selfUpgradeConflict(args []string) error` per R15: build a throwaway root with `newRootCmd(&cli.Options{}, "", "", nil)`, call `InitDefaultHelpFlag` and `InitDefaultVersionFlag`, and `ParseFlags(args)`; on a parse error return nil (the real dispatch reports it); when `self-upgrade` is not set return nil; when `help` is set return nil; when `Flags().NArg() > 0` return the `task arguments` usage error; otherwise `Visit` the changed flags in order and return the usage error for the first one that is not `self-upgrade` or `color`, named `--<long name>`. In `cmd/rune/main.go` call it before `root.ExecuteContext`; on error print it through the same banner path as other errors and return `cli.CodeFor(err)`. Make T064 and T065 pass.

**Checkpoint**: All six stories are functional.

---

## Phase 9: Polish and Cross-Cutting Concerns

**Purpose**: The offline guarantee, the docs, and the full gate set.

- [X] T068 [P] Create `test/integration/no_network_test.go` with `TestNoNetworkOutsideSelfUpgrade` (FR-005, SC-007, R19): start a local TCP listener that counts accepted connections; run `--list`, `--version`, `--help`, `version`, `version --check`, `--dry-run build` and a task that echoes, each in a directory with a small Runefile and with `HTTPS_PROXY`, `HTTP_PROXY` and `ALL_PROXY` set to the listener and `NO_PROXY` empty; assert the count stays 0. As a control in the same test, run `--self-upgrade` with `RUNE_TEST_VERSION=0.0.1` and no `RUNE_TEST_UPGRADE_*` variables and assert the count becomes at least 1, which proves the probe works.
- [X] T069 [P] In `docs/cli.md` add a row to the "Global flags" table after `--ignore-version`: `--self-upgrade` with "Upgrade this `rune` to the latest stable release, after verifying its signature and checksum. Runs no task and reads no Runefile. Cannot be combined with other flags or task arguments. See [Upgrading](installation.md#upgrading)." Add an example line `rune --self-upgrade` to "Examples". In "Exit codes", extend the rows: `0` also "or `--self-upgrade` upgraded or found nothing to upgrade"; `1` also "or `--self-upgrade` could not upgrade"; `2` also "or `--self-upgrade` combined with another argument".
- [X] T070 [P] In `docs/installation.md` add a section `## Upgrading` before "Shell completions" containing: the command; the per-method table from the Overview of `spec.md` with the exact commands from R12; what is verified (the signature on `checksums.txt` by the release workflow, then the archive's SHA-256) and that there is no way to skip it; supported platforms (the six pairs, macOS 13 or newer); the hosts contacted from R16; that Rune never checks for updates on its own (FR-038); that Rune never asks for elevated rights, with the manual path (rerun with the rights the directory needs, or reinstall with the install script); Windows behaviour including `rune.exe.old` and the recovery step from R14 (rename `rune.exe.old` back to `rune.exe`); and a short failure table keyed by the first words of each message in `contracts/cli-self-upgrade.md`. Do not reproduce a Windows path with backslashes anywhere in `docs/` (Principle V); name the Scoop case without a path. Change the existing sentence "`brew upgrade rune` keeps it current." only if it now contradicts the table.
- [X] T071 [P] In `docs/releasing.md`, under "Verifying a release", change step 2 to the exact identity (`--certificate-identity 'https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main'`, same issuer) and add a subsection "What `rune --self-upgrade` depends on" with the three maintainer consequences and the file table from `specs/035-go127-self-upgrade/contracts/release-layout.md`. In `docs/docker.md` add one sentence that the image is upgraded by pulling a newer tag and that `--self-upgrade` declines inside it.
- [X] T072 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` and fix any link or code-block failure the new docs cause. Add the new pages' anchors to whatever index `test/docs` checks only if the harness requires it.
- [X] T073 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/selfupgrade/... -coverprofile=/tmp/su.cover && go tool cover -func=/tmp/su.cover` in the container. Statement coverage of `internal/selfupgrade` (excluding `upgradetest`) must be at least 90 percent, and every function in the package must have a test that calls it. Add tests for anything missing; do not lower the target.
- [X] T074 Run the full gate set on the finished tree: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...` with `CGO_ENABLED=1`, the lint command, the golden check from T010, `go test ./test/docs/...`, both fuzz smokes from `.github/workflows/ci.yml`, `GOOS=windows go build ./...`, `GOOS=darwin go build ./...`, and `goreleaser check` plus `goreleaser release --snapshot --clean`. Repeat T003's list command and `diff` against T003: identical (SC-010). Report the stripped linux/amd64 binary size from `dist/` next to the 11.6 MB baseline in R6.
- [X] T075 Walk `specs/035-go127-self-upgrade/quickstart.md` sections 3 and 5 by hand on the host with a scratch directory (this uses the real release service and the public trust root). Report each command's output and exit code, and the elapsed time of the upgrade command measured with `time` (SC-001: under one minute). Section 4 is covered by the Windows CI job; say so.
- [X] T076 Stop and report that Part B is ready, with the output of T074 and T075. Commit only when the maintainer asks. When asked, commit Part B as `feat(cli): add --self-upgrade` with a body that names the new dependency and its size cost, the pinned signer identity, and the Windows limit from R14. No Co-Authored-By trailer.

---

## Dependencies and Execution Order

### Phase dependencies

| Phase | Depends on | Why |
|---|---|---|
| 1 Setup | nothing | |
| 2 US6 | 1 | |
| 3 Foundational | 2 | `sigstore-go` v1.4.0 needs `go 1.27.0` |
| 4 US2 | 3 | uses the fixture and `Error` |
| 5 US1 | 4 | `Run` calls `verifyChecksums`, `parseChecksums`, `extractBinary` |
| 6 US3 | 5 | hardens `Run` and `fetch` |
| 7 US4 | 5 | adds a step to `Run`; independent of Phase 6 |
| 8 US5 | 5 | needs the flag from T042; independent of Phases 6 and 7 |
| 9 Polish | 6, 7, 8 | |

### Within each story

- Tests are written and seen failing before the implementation tasks of that phase.
- In Phase 5: `release`, `fetch` and `replace` (T036 to T038) before `Run` (T039); `Run` before the
  command wiring (T041, T042); wiring before the integration test passes (T043).
- T039, T046, T054, T057 and T062 all edit `internal/selfupgrade/upgrade.go` or its test file and
  run in sequence.

### Parallel opportunities

- Phase 2: T005, T006, T007, T008 touch different files.
- Phase 3: T013 and T017 alongside T014; then T015 and T016.
- Phase 4: T019 to T022 together, then T025 and T026 together alongside T024.
- Phase 5: T027, T028, T029, T032, T033 together; then T036, T037, T038, T040 together.
- Phase 6: T044, T045, T048, T049 together; then T052 and T055 alongside T053.
- Phases 6, 7 and 8 can be worked by different people once Phase 5 is done, merging through
  `upgrade.go` in the order 6, 7.
- Phase 9: T068 to T071 together.

---

## Parallel Example: User Story 2

```bash
# Tests first, all in different files:
Task: "T020 verify_test.go: signature cases incl. real v0.7.0 files"
Task: "T021 checksum_test.go: list parsing and archive names"
Task: "T022 extract_test.go: one binary out of tar.gz and zip"

# After T023 confirms they fail:
Task: "T024 verify.go"
Task: "T025 checksum.go"
Task: "T026 extract.go"
```

---

## Implementation Strategy

### First mergeable step (US6)

1. Phases 1 and 2.
2. Open a pull request for Part A alone. It changes no behaviour and unblocks Part B.

### MVP (US2 + US1 + US3)

1. Phases 3, 4, 5, 6.
2. **Stop and validate**: quickstart section 2 for US1 to US3, and section 3 by hand.
3. US1 alone (end of Phase 5) is a demo checkpoint, not a release point: without Phase 6 a failed
   upgrade is not yet guaranteed to clean up, two upgrades can collide, and Windows cannot replace
   its binary.

### Incremental delivery

1. US6: Go 1.27 everywhere.
2. US2 then US1: a verified upgrade on Linux and macOS.
3. US3: safe under failure, and Windows. First releasable state.
4. US4: package-manager, `go install`, container and development installs are protected. Ship this
   with step 3 if Homebrew and Scoop users will have the flag, which they will.
5. US5: the flag cannot be combined with anything.
6. Polish: docs, the offline guarantee, coverage and the full gates.

Steps 3 to 5 should go out in the same release: each closes a way the flag could surprise someone.

---

## Implementation notes (2026-11-10)

Where the work differs from the task text above:

- **T014**: the fixture does not use the library's `VirtualSigstore`. Its certificates carry no
  certificate transparency timestamp, which the updater requires, so `upgradetest/signer.go` holds a
  small private authority that embeds one. `TrustRootJSON` works, so the binary-level success path
  is tested.
- **T034, T050**: no temporary Windows skip was added; the Windows replacement landed before the
  first CI run.
- **T046**: the "error with no reason" case is covered by `TestRunMissingExecutable` (a plain error
  from locating the executable) instead of a hook, because every hooked step now maps to a reason.
- **T052**: a stale lock is taken over under a second exclusive marker file
  (`.rune-upgrade.lock.takeover`), not by renaming it. A rename can also move a fresh lock.
- **T053**: rate-limit detection lives in `release.go` (`rateLimited`) and is called by the
  downloader for every host.
- **T003, T011, T074**: `goreleaser` is not installed on the host, so the snapshot ran in the
  `goreleaser/goreleaser` image on a copy of the tree, with the flags CI uses.
- **T075**: run in a `golang:1.27-bookworm` container as a non-root user, not on the host, against
  the real release service. Section 4 (Windows) is left to the Windows CI job.
- **Code review, round 1 (2026-11-11)**: the lock records its owner and a dead owner on the same
  host is taken over at once; `sweepStale` became `sweepStaged` and removes every staging file once
  the lock is held; Windows sets the old binary aside under a numbered name when `rune.exe.old` is
  still running, and a failed rollback says the binary is missing; a write failure during
  extraction is `not-writable`, not `bad-archive`; the header limit applies per redirect hop; the
  root command enforces the combination rule itself, and the pre-dispatch check returns early when
  the flag is absent.
- **Code review, round 2 (2026-11-11)**: an unreachable proxy is `offline` and names the proxy; a
  write failure while the archive downloads is `not-writable`; Scoop paths are looked for only on
  Windows and Homebrew paths only elsewhere; the conflict message names the first flag as typed;
  only `rune.exe.old.<number>` and `.rune-upgrade-<number>` regular files are removed; the archive
  is extracted from the same open file that was hashed; a run releases only a lock it still owns
  and checks ownership again before replacing the binary. Not adopted: keeping a lock alive for as
  long as its recorded process exists, because a reused process id would then block upgrades
  with no time limit.
- **T006, CI (2026-11-11)**: the linter is `v2.14`, not `v2.13`. The v2.13 binary lints a Go 1.27
  module on go1.27.0 but cannot read the standard library that go1.27.4 builds (`export data
  version 5 is greater than maximum supported version 4`), which is what CI installs. v2.14 reads
  it and reports 0 issues; v2.15 also works but adds an errcheck rule that flags six existing
  lines in `internal/lsp/protocol_test.go`, left for a separate change.
- **T012, T076**: committed on 2026-11-11 when the maintainer asked.
- Adding `sigstore-go` raised `golang.org/x/sys`, `x/term` and `x/text` (required by its module
  graph).

## Notes

- [P] tasks touch different files and depend on no unfinished task.
- The integration tests build the binary with `-tags runetest`; a released binary has none of the
  `RUNE_TEST_UPGRADE_*` switches.
- Commits happen only when the maintainer asks (T012, T076).
- After opening the pull request, wait for every CI check to finish green before reporting the work
  done. The Windows and macOS replacement paths are proven only there.
- Every bug a code review finds gets a test before the fix.
