# Quickstart: Validating the Destructive-Task Opt-In

**Feature**: 031-mcp-destructive-optin | **Date**: 2026-11-04 | **Spec**: [spec.md](spec.md)

Expected texts are defined in [contracts/cli-serve.md](contracts/cli-serve.md)
and [contracts/mcp-tools.md](contracts/mcp-tools.md); this guide only says how to
check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

The binary-level scenarios below are automated in
`test/integration/destructive_mcp_test.go`; the docs harness (`test/docs`)
validates the changed doc pages.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Project health for agents.
[context]
health:
    @echo ok > context.done

# Removes build output.
[confirm("Delete build output?")]
clean:
    @echo cleaned > clean.done

# Resets local state.
[confirm("Reset local state?")]
reset:
    @echo reset > reset.done

# Cuts a release.
release: clean
    @echo released > release.done

# Ships after a private wipe.
ship: wipe
    @echo shipped > ship.done

# Builds the project.
build:
    @echo built

[private]
[confirm("Wipe caches?")]
wipe:
    @echo wiped > wipe.done
```

## Driving a tool call by hand

Write the three frames to `frames.jsonl`, replacing `TASK`:

```text
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"qs","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"TASK","arguments":{}}}
```

Then run `(cat frames.jsonl; sleep 2) | rune mcp [FLAGS]` and read the `"id":2`
line. The `sleep` keeps stdin open until the reply arrives. Delete `*.done`
between scenarios.

## Scenarios

| # | Flags | Call | Expect |
|---|---|---|---|
| A | none | `clean` | destructive refusal naming `--allow-destructive clean`; no `clean.done` |
| B | `--allow-destructive clean` | `clean` | `clean.done` exists; result not an error |
| C | `--allow-destructive clean` | `reset` | destructive refusal naming `reset`; no `reset.done` |
| D | `--allow-destructive clean` | `release` | `clean.done` and `release.done` exist |
| E | none | `release` | `[exit 1]`, `isError`; no `clean.done`, no `release.done` |
| F | `--allow-destructive wipe` | `ship` | `wipe.done` and `ship.done` exist; `tools/list` has no `wipe` |
| G | `--allow-destructive clean,reset` | (start only) | stderr startup line lists `clean, reset` |

Startup checks, no frames needed (`rune mcp ... </dev/null`):

| # | Command | Expect |
|---|---|---|
| H | `rune mcp --allow-destructive claen` | exit 2, suggests `clean`; no `context.done` |
| I | `rune mcp --allow-destructive ""` and `--allow-destructive clean,` | exit 2, empty task name; no `context.done` |
| J | `rune mcp --allow-destructive '*'` | exit 2, unknown task `*` |
| K | `rune mcp --allow-destructive build` | one warning line naming `build`, then the unchanged startup line `rune MCP server on stdio` |
| L | `rune mcp --yes` and `rune --yes mcp` | exit 2, unknown flag |

HTTP (scenario M): start
`rune serve --http --addr 127.0.0.1:8765 --token-file tok --allow-destructive clean`
and call `clean` with an MCP client that sends the bearer token. `clean.done`
appears. Without the token the request gets 401, as before. This scenario is
automated at the library level in `mcpserver/transport_test.go`.

## Docs check

Read `docs/mcp.md` ("Starting the server", "Security model", "Agent task
bodies"), the `rune serve` row of `docs/cli.md`, and `rune serve --help`. All
three describe `--allow-destructive` the same way, and no page says `--yes`
approves anything over MCP.
