# Quickstart: validating exit-code propagation

**Feature**: [spec.md](spec.md) | **Contract**: [contracts/exit-codes.md](contracts/exit-codes.md)

## Prerequisites

- Docker. Tests never run on the host. On this machine the `docker compose`
  plugin works and test builds need `GOFLAGS=-buildvcs=false`. The repo
  Runefile's tasks call standalone `docker-compose`.
- A binary for the manual checks, built in Docker into the gitignored `dist/`:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go build -o dist/rune ./cmd/rune`.
  The container builds with `CGO_ENABLED=0`, so the Linux binary runs on the
  host. Below, `rune` means `dist/rune` (or put `dist/` on `PATH`).

## Automated gates

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
rune lint
```

All must pass. CI repeats them on Linux, macOS, and Windows. The signal tests
are skipped on Windows by build tag.

## Manual scenarios

Use this Runefile in an empty directory:

```rune
refuse:
    @echo "gate: refused" >&2
    @exit 2

fail:
    @exit 1

dep: refuse
    @echo never

pyfail (python):
    import sys; sys.exit(4)

[no-exit-message]
quiet:
    @exit 6

slow:
    sleep 30

hooked: || diagnose
    @exit 3

diagnose:
    @echo "code=$RUNE_FAILED_EXIT_CODE"
```

### A. The body's code reaches the shell (Story 1)

```console
$ rune refuse; echo "rc=$?"
gate: refused
rune: task "refuse" failed at "exit 2": exit status 2
rc=2
$ rune fail; echo "rc=$?"       # rc=1
$ rune pyfail; echo "rc=$?"     # rc=4
$ rune quiet; echo "rc=$?"      # no banner, rc=6
```

### B. Dependencies and multiple tasks (Story 3)

```console
$ rune dep; echo "rc=$?"        # banner names "refuse", "never" not printed, rc=2
$ rune fail refuse; echo "rc=$?"   # stops at fail, rc=1
```

### C. Rune's own codes are unchanged (FR-005)

```console
$ rune nope; echo "rc=$?"       # rune: unknown task: nope, rc=2
$ rune slow                     # press Ctrl-C: rc=130
```

Ctrl-C during `pyfail`-style interpreter bodies also gives 130, and `||` hooks do
not fire (research R4). Ctrl-C during a `[no-exit-message]` task whose body runs
`sleep 30` gives 130 and prints no banner (research R13).

### D. Failure hooks see the same code (FR-009)

```console
$ rune hooked; echo "rc=$?"     # prints code=3, rc=3
```

### E. MCP parity (Story 2)

Start `rune mcp` in the directory and call `refuse` from an MCP client. The result
has `isError: true` and its text ends with `[exit 2]`. Calling `hooked` ends with
`[exit 3]` followed by the fix-suggestion section.

### F. Documented example (SC-004)

```console
$ rune --file docs/examples/ci-cd/Runefile release-gate closed; echo "rc=$?"   # rc=2
$ rune --file docs/examples/ci-cd/Runefile release-gate; echo "rc=$?"          # rc=0
```
