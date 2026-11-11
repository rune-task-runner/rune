# Quickstart: Validating Go 1.27 and `rune --self-upgrade`

How to prove the feature works once it is built. Message wording is in
[contracts/cli-self-upgrade.md](contracts/cli-self-upgrade.md); toolchain facts are in
[contracts/toolchain.md](contracts/toolchain.md).

## Prerequisites

- Docker with the compose plugin. Go tests run in the container, never on the host.
- The test image rebuilt after the version change: `docker compose build test`.

## 1. Toolchain

```sh
docker compose run --rm test go version                      # go1.27.x
docker compose run --rm -e GOFLAGS=-buildvcs=false test go build ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/src -w /src golangci/golangci-lint:v2.13 golangci-lint run
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
goreleaser check && goreleaser release --snapshot --clean
```

Expected: every command exits 0. Then run the search in
[contracts/toolchain.md](contracts/toolchain.md); it prints nothing.

Release output: save `ls dist` from a snapshot run on `main` and from this branch, and compare.
Only version strings in file names differ.

## 2. Self-upgrade, automated

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/selfupgrade/... -cover
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./cmd/rune/...
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/ -run SelfUpgrade
```

Expected: all pass, and `internal/selfupgrade` reports at least 90 percent coverage. Tests that need
an unwritable directory report SKIP inside the container (it runs as root) and run in CI.

What the scenarios cover, by spec story:

| Story | Scenarios |
|---|---|
| US1 | Upgrade, then `--version` reports the fixture version; already current; installed newer; prerelease and draft ignored; no Runefile, broken Runefile, Runefile needing a newer Rune; symbolic link kept; permissions kept |
| US2 | Altered archive; altered list; missing signature file; wrong signer; archive with extra and `../` entries; real v0.7.0 files verify with the production identity |
| US3 | Download cut off; cancel during download; unwritable directory; two upgrades at once; no route; rate limit answer; oversized download; stale lock and stale staging files |
| US4 | Homebrew path; Scoop path; `go install` build; `RUNE_CONTAINER=1`; `dev` version; link into a Homebrew path; exit 1 for each |
| US5 | Flag with a task; with each other flag; after a task name; after `--`; listed in `--help` |
| FR-005 | `--list`, `--version`, `--help`, `version` and a task make zero connections |

## 3. Self-upgrade, by hand against the real release service

Do this on a machine where a scratch copy can be thrown away. It uses the public release and the
public Sigstore trust root, so it needs the network.

```sh
tmp=$(mktemp -d)

# Stamped builds stand in for release binaries: one older and one newer than the latest release.
go build -ldflags "-X main.version=0.0.1 -X main.commit=test" -o "$tmp/old" ./cmd/rune
go build -ldflags "-X main.version=99.0.0 -X main.commit=test" -o "$tmp/new" ./cmd/rune
cp "$tmp/old" "$tmp/rune" && ln -s "$tmp/rune" "$tmp/link"
```

Refusals and no-ops first, while every binary still has the flag:

```sh
"$tmp/new" --self-upgrade; echo $?                                  # current: nothing to upgrade, 0
"$tmp/old" --self-upgrade build; echo $?                            # usage error, 2
"$tmp/old" --self-upgrade --list; echo $?                           # usage error, 2
RUNE_CONTAINER=1 "$tmp/old" --self-upgrade; echo $?                 # container, 1
go build -o "$tmp/dev" ./cmd/rune && "$tmp/dev" --self-upgrade; echo $?   # development build, 1
```

Read-only directory, as a normal user:

```sh
chmod a-w "$tmp" && "$tmp/old" --self-upgrade; echo $?    # not-writable, 1; no sudo prompt
chmod u+w "$tmp"
```

Then the upgrade itself, through the symbolic link:

```sh
"$tmp/link" --self-upgrade        # upgraded: 0.0.1 -> <latest>, 0
"$tmp/rune" --version             # reports <latest>
ls -la "$tmp"                     # link still a link; no .rune-upgrade files
```

Until a release that contains this feature is published, `<latest>` is a release without the flag
(v0.7.0 today), so the replaced binary cannot be asked to upgrade again. The download, the
signature check on the real release and the replacement are still exercised.

## 4. Windows

Run in the Windows CI job, or by hand in PowerShell with a scratch copy:

```powershell
.\rune.exe --self-upgrade      # upgraded
.\rune.exe --version           # new version
dir                            # rune.exe and rune.exe.old
.\rune.exe --self-upgrade      # current; rune.exe.old is gone
```

## 5. Docs

`rune --help` lists the flag. `docs/cli.md` has the flag row and the exit-code rows.
`docs/installation.md` has an "Upgrading" section with the per-method table, what is verified, the
hosts contacted, and recovery steps. `docs/releasing.md` shows the exact signer identity and the
list from [contracts/release-layout.md](contracts/release-layout.md).
