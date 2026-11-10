# Quickstart: Validating Truthful Tool Hints

**Feature**: 033-mcp-tool-hints | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

The hint rules are in [contracts/mcp-tools.md](contracts/mcp-tools.md), and the error texts are in
[contracts/readonly-attribute.md](contracts/readonly-attribute.md). This guide only says how to check
them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/diag ./internal/lexer ./internal/parser ./internal/cli ./test/corpus -update
git diff --exit-code   # on the host, after committing the corpus golden: git cannot read the mounted repo inside the container
```

Scenarios A to Z below are automated in `test/integration/tool_hints_mcp_test.go`. Scenario AA is
automated at library level in `mcpserver/transport_test.go`, AB in `internal/cli/mcp_test.go`, and the
Windows half of N in `internal/cli/mcp_test.go` with an injected OS. The docs harness (`test/docs`)
validates the changed pages and runs the changed example.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Deploys the service.
deploy:
    @echo deployed > deploy.done

# Reports status.
[readonly]
status:
    @echo status > status.done

# Probes a remote endpoint.
[readonly, network]
health:
    @echo healthy > health.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Fetches the data set.
[network]
fetch:
    @echo fetched > fetch.done

# Cuts a release.
release: clean
    @echo released > release.done

# Mirrors the data set.
mirror: fetch
    @echo mirrored > mirror.done

# Fetches after success.
post: && fetch
    @echo post > post.done

# Cleans up on failure.
bad: || clean
    @exit 1

# Reaches clean two levels down.
outer: inner
    @echo outer > outer.done

# Reaches clean one level down.
inner: clean
    @echo inner > inner.done

# Ships after a private wipe.
ship: _wipe
    @echo shipped > ship.done

[private]
[confirm("Wipe?")]
_wipe:
    @echo wiped > wipe.done

# Dispatches per OS.
dispatch: wipe-win
    @echo dispatched > dispatch.done

# Wipes on Windows.
[windows, confirm("Wipe?")]
wipe-win:
    @echo wiped > wipe-win.done

# Reports through a private reader.
[readonly]
report: _read-state
    @echo report > report.done

[readonly, private]
_read-state:
    @echo state > state.done
```

## Tool list

Start `rune mcp` in that directory, send the `initialize` and `notifications/initialized` frames, and
then `tools/list`. "Absent" means the reply has no `readOnlyHint` key.

| # | Tool | `readOnlyHint` | `destructiveHint` | `openWorldHint` |
|---|---|---|---|---|
| A | `deploy` | absent | `false` | `false` |
| B | `status` | `true` | `false` | `false` |
| C | `health` | `true` | `false` | `true` |
| D | `clean` | absent | `true` | `false` |
| E | `fetch` | absent | `false` | `true` |
| F | `release` | absent | `true` | `false` |
| G | `mirror` | absent | `false` | `true` |
| H | `post` | absent | `false` | `true` |
| I | `bad` | absent | `true` | `false` |
| J | `outer` | absent | `true` | `false` |
| K | `inner` | absent | `true` | `false` |
| L | `ship` | absent | `true` | `false` |
| M | `report` | `true` | `false` | `false` |
| N | `dispatch` | absent | `false` off Windows, `true` on Windows | `false` |

Neither `_wipe` nor `_read-state` is listed, and `wipe-win` is listed only on Windows.

## Other server setups

| # | Setup | Check | Expected |
|---|---|---|---|
| O | Add `mod tools "tools.rune"`, `tools.rune` with a `[confirm]` task `clean`, and `shipit: tools::clean` | `tools/list` | `shipit` has `destructiveHint: true` |
| P | `rune mcp --allow-destructive clean` | `tools/list` | Rows A to N are unchanged |

## Calls behave as they do today

Remove the markers between scenarios.

| # | Call | Expected |
|---|---|---|
| Q | `status` | `[exit 0]`, `status.done` exists, the same as `rune status` |
| R | `clean`, no grant | The destructive refusal text, no marker |
| S | `release`, no grant | An error result without the destructive refusal text, and no `clean.done` or `release.done` |
| T | `release`, with `--allow-destructive clean` | `clean.done` and `release.done` exist |

## Static errors

Each row uses a separate Runefile. Each text is the contract's.

| # | Runefile | Command | Expected |
|---|---|---|---|
| U | `[readonly, confirm("x?")] wipe` | `rune analyze` | Exit 3, `error[RUNE2018]:`, first text, at `[readonly]` |
| V | `[readonly] report: build`, `build` unmarked | `rune analyze` | Exit 3, third text, at the reference to `build` |
| W | `[readonly] report: wipe`, `wipe` with `[confirm]` | `rune analyze` | Exit 3, second text, at the reference to `wipe` |
| X | `[readonly] a: b`, `[readonly] b: c`, `c` unmarked | `rune analyze` | One error, for `b` running `c` |
| Y | Scenario V's file, with a marker-writing `build` | Running `report`, `--list`, `--dump`, `--summary report`, `--dry-run report`, and `rune mcp` | Each exits 3 with the text, and no marker |
| Z | Scenario V's file | `rune --fmt` | Exit 0, and the file is formatted |

## Routes other than stdio

| # | Check | Expected |
|---|---|---|
| AA | `rune serve --http --token-file tok`, then `tools/list` with the token | The same hints as on stdio |
| AB | The endpoint an `agent` task opens for its own agent, `tools/list` | The same hints as on stdio |

## Docs

```sh
grep -rIn -i 'read-only' README.md SECURITY.md docs/mcp.md docs/user-guide/README.md \
  docs/use-cases/mcp-agents.md docs/examples/agent-driven website/src/landing/index.mdx
```

Every hit must be about `[readonly]` or `readOnlyHint`, or describe a task that has `[readonly]`.
None may say tasks or agent access are read-only by default.
