# Quickstart: Validating Safe Quoting for Task Parameters

**Feature**: 030-safe-param-quoting. Runs end to end on a build from this branch.
Expected outputs come from the [contracts](contracts/); this guide only says how
to check them.

## Prerequisites

- Docker with `docker-compose` (tests never run on the host).
- `bash` on `PATH` for scenario G.
- A binary for the manual scenarios:
  `docker-compose run --rm test go build -o /work/bin/rune ./cmd/rune`, or
  `go run ./cmd/rune` where noted.

## Automated gates

```sh
docker-compose run --rm test go test ./...
docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...
docker-compose run --rm test go test ./test/corpus ./test/docs ./test/integration
```

Lint with the repository's golangci-lint image, as in `CONTRIBUTING.md`.

What the suites cover:

| Area | Tests |
|---|---|
| `quote_each` evaluation, `Args.Key()` | `internal/eval` unit tests |
| Binding elements from CLI and MCP, forwarding | `internal/cli` unit tests (`typed_binding_test.go` and a new `elements_test.go`) |
| Run-once identity | `internal/runtime/scheduler` tests |
| Cache fingerprint with elements | `internal/cli` cache tests |
| `ListEngine`, `decodeArgs` lists, `raw` schema | `mcpserver` tests |
| RUNE2017 rule table | `internal/analyzer/quoting_test.go`, fixture rows 1 to 27 of [diagnostic-rune2017.md](contracts/diagnostic-rune2017.md) |
| `raw` parse, format, hover | parser, formatter golden, `internal/language` hover tests, new corpus fixture |
| Binary-level behavior | `test/integration/quoting_test.go` (CLI, MCP over stdio, forwarding, `rune analyze --json`) |
| Docs and examples are warning-free | `test/docs/quoting_test.go` |

## Manual scenarios

Use a scratch directory with this `Runefile`:

```rune
a *names:
    @printf "[%s]\n" {{names}}

b *names:
    @printf "[%s]\n" {{quote(names)}}

c *names:
    @printf "[%s]\n" {{quote_each(names)}}

n *names:
    @set -- {{quote_each(names)}}; echo "$#"

ci *pkgs: (n pkgs)
    @echo done

plus *pkgs: (need pkgs)
    @echo after

need +pkgs:
    @set -- {{quote_each(pkgs)}}; echo "$#"

test flags:raw="-v -race":
    @printf "[%s]\n" {{flags}}
```

**A. Injection is stopped.** `rune c yq 'x; echo pwned' tofu` prints the three
lines in the [quote_each contract](contracts/runefile-surface.md#quote_eachx); no
line reads `pwned` alone.

**B. Boundaries survive.** `rune c 'a b' '' "it's"` prints `[a b]`, `[]`, `[it's]`.
`rune n` prints `0`. `rune n 'a b' c` prints `2`.

**C. Old forms unchanged.** `rune a yq 'x; echo pwned' tofu` and
`rune b yq 'x; echo pwned' tofu` print what v0.6.0 prints (see the issue).

**D. Forwarding.** `rune ci 'a b' c` prints `2`, then `done`. `rune plus` prints
`0` (`need` still runs, as in v0.6.0, and gets no elements), then `after`, and
exits 0.

**E. Analyzer.** `rune analyze` reports RUNE2017 for `a` (fix `quote_each`) and
for nothing else in this file, and exits 0. (RUNE2010 warnings for the
undocumented tasks also appear; they are unrelated.) `rune analyze --json` shows the same
entry with `"code": "RUNE2017"`. `rune a x` prints no warning.

**F. `raw`.** `rune test` prints `[-v]` and `[-race]`. `rune --fmt` followed by
`git diff` (or a before and after copy) shows `flags:raw="-v -race"` kept.

**G. `set shell`.** Add `set shell := ["bash", "-eu"]` at the top. Scenario A
prints the same three lines, and `rune analyze` still reports `a`.

**H. MCP.** Start `rune mcp` and call tool `c` with
`{"names": ["yq", "x; echo pwned", "tofu"]}`: the result is scenario A's three
lines and `[exit 0]`. `tools/list` shows `test.flags` as a string whose
description is "Split into shell words without quoting."

**I. Docs.** `docker-compose run --rm test go test ./test/docs -run Quoting`
passes; every example Runefile and the repository `Runefile` analyze with no
RUNE2017.
