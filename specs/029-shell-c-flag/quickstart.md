# Quickstart: Validate the `-c` Fix

**Feature**: [spec.md](spec.md) | **Contract**: [contracts/shell-override.md](contracts/shell-override.md)

## Prerequisites

- Docker with the compose plugin. On this host, use `docker compose` with
  `GOFLAGS=-buildvcs=false`. Without it, `test/integration` and `test/docs` fail to
  build with "error obtaining VCS status".
- Tests run in the container, never on the host (constitution, Engineering
  Constraints). The test image is Debian-based and has bash.

## Automated gates

```sh
# Unit: the flag rule and Select (all OSes).
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/runtime/...

# Binary level: repro, flag forms, [script], stdin, state, MCP, equivalence (SC-001, SC-002).
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/ -run ShellOverride

# Docs: every `set shell` example stops at a failing line (SC-004).
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/ -run ShellOverride

# Full suite, then the race detector (SC-003).
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
```

`golangci-lint` is not in the test image. CI runs it. Report it as not run locally.

## Manual scenarios

Build once in the container, then run each scenario from a scratch directory with
its Runefile. Expected results come from the contract.

### A. The issue's repro (Story 1, SC-001)

```rune
set shell := ["bash", "-euo", "pipefail", "-c"]

# Should stop at false.
t:
    @echo one
    @false
    @echo two
```

`rune t; echo "rc=$?"` prints `one`, the banner
`rune: task "t" (bash) failed: exit status 1`, and `rc=1`. `two` does not appear.

### B. Flag inside a cluster (Story 1, scenario 3)

`set shell := ["bash", "-euc"]` with a body of `echo a`, `(exit 3)`, `echo b`.
Expected: `a` only, `rc=3`.

### C. Old docs example, no `-e` (Story 2, scenario 2)

`set shell := ["bash", "-cu"]` with a body of `echo a`, `false`, `echo b`.
Expected: `a` and `b`, `rc=0`, identical to `["bash", "-u"]`.

### D. Unset variable under `-cu` (Story 1, scenario 2)

`set shell := ["bash", "-cu"]` with a body of `echo "$NOPE_NOT_SET"`.
Expected: bash's `unbound variable` error on stderr and a non-zero `rc`.

### E. `[script]` (Story 1, scenario 5)

A task with `[script("bash -euc")]` and a body of `echo a`, `false`, `echo b`.
Expected: `a` only, `rc=1`.

### F. MCP (Story 1, scenario 6)

Call task `t` from scenario A as an MCP tool. Expected: the result is marked as an
error and ends with `[exit 1]`.

### G. Unaffected paths (FR-008)

Run the scenario A body with no `set shell`. Expected: the built-in shell echoes
nothing (lines have `@`), stops at `false`, and exits 1, as on `main`. Run a
`(python)` task with `set python := ["python3"]` and check that its output is
unchanged.

### H. Lists left as written (FR-001, Story 2 scenario 4)

`set shell := ["bash", "-c", "exec bash -eu $0"]` with a body of `echo a`, `false`,
`echo b`. Expected: `a` only, `rc=1`, the same as on `main`: the list's own code
runs the script, which arrives as `$0`. The wrapper and non-shell cases
(`nix develop -c`, `php -c`, `jq -c`, `node -c`) are covered by the unit table
(research R3).

## Docs check

Read the "Shell override" subsection of `docs/runefile.md` and check that it states
each point in contract §3, and that every `set shell` example in `docs/` includes
`-e`.
