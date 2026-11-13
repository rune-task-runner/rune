# Quickstart: Validating `--allow` and `--deny`

**Feature**: 041-mcp-allow-deny | **Date**: 2026-11-12 | **Spec**: [spec.md](spec.md)

The flag forms and texts are in [contracts/cli-serve.md](contracts/cli-serve.md), what a client sees
in [contracts/mcp-tools.md](contracts/mcp-tools.md), and the library changes in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md). This guide only says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run
```

Scenarios A to P below are automated in `test/integration/allowlist_mcp_test.go`. Q is automated in
`internal/cli/mcp_test.go` with an injected OS, R in `mcpserver/transport_test.go`, and S in
`mcpserver/server_test.go`. The docs harness (`test/docs`) validates the changed pages.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Builds the artifacts.
build:
    @echo built > build.done

# Runs the tests.
test: build _setup
    @echo tested > test.done

# Ships the build.
deploy:
    @echo deployed > deploy.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Cuts a release.
release: clean
    @echo released > release.done

# Lints, then reports.
lint: && report
    @echo linted > lint.done

# Writes the report.
report:
    @echo reported > report.done

_setup:
    @echo setup > setup.done

[context]
health:
    @echo context > context.done
```

Exposable tasks: `build`, `test`, `deploy`, `clean`, `release`, `lint`, `report` (7).

## Scenarios

Each starts `rune mcp` with the flags shown, in a fresh directory, and speaks MCP over stdio.

| # | Flags | Action | Expected |
|---|---|---|---|
| A | `--allow build,test` | list tools | exactly `build`, `test` |
| B | `--allow build,test` | call `build` | runs; `build.done` exists |
| C | `--allow build,test` | call `deploy` | unknown-tool error; no `deploy.done` |
| D | `--allow build,test` | call `deploy`, call `nosuch` | the two responses differ only in the echoed name |
| E | `--allow build --allow test` | list tools | same as A |
| F | `--deny deploy` | list tools | the other 6 tasks, not `deploy` |
| G | `--deny deploy` | call `deploy` | unknown-tool error; no `deploy.done` |
| H | `--allow build,deploy --deny deploy` | list tools | exactly `build` |
| I | `--allow test` | call `test` | `build.done`, `setup.done`, `test.done` exist |
| J | `--allow test` | call `build` | unknown-tool error |
| K | `--allow lint` | call `lint` | `lint.done` and `report.done` exist |
| L | `--allow release` | call `release` | refused with the spec 034 text naming `clean`; no marker |
| M | `--allow release --allow-destructive clean` | call `release`, list tools | `clean.done`, `release.done` exist; list is exactly `release` |
| N | `--allow clean` | call `clean` | refused with the spec 031 text; no `clean.done` |
| O | `--allow build,test` | list tools with and without the flags | `build` and `test` entries are identical in both |
| P | see below | start only | exit status, stderr, and no `context.done` for errors |
| Q | `--allow win-only` on Linux (task marked `[windows]`) | start, list tools | starts; empty list; the empty-surface warning; no never-exposed warning |
| R | `--http --token-file tok --allow build` | list tools over HTTP | exactly `build` |
| S | library `Options{AllowList, DenyList}` | `Exposure()` | counts match the registered tools |

### P: startup checks

| Flags | Exit | Stderr | `context.done` |
|---|---|---|---|
| `--allow biuld` | 2 | `rune: unknown task in --allow: biuld (did you mean "build"?)` | absent |
| `--deny biuld` | 2 | `rune: unknown task in --deny: biuld (did you mean "build"?)` | absent |
| `--allow ""` | 2 | `rune: --allow: empty task name` | absent |
| `--deny build,` | 2 | `rune: --deny: empty task name` | absent |
| `--allow '*'` | 2 | `rune: unknown task in --allow: *` | absent |
| `--allow _setup` | starts | `warning: --allow _setup has no effect: task "_setup" is never exposed to agents`, the empty-surface warning, `(0 of 7 tasks exposed)` | present |
| `--deny health` | starts | the never-exposed warning for `health`; `(7 of 7 tasks exposed)` | present |
| `--allow build --deny build` | starts | `warning: no tasks are exposed to agents; check --allow and --deny`; `(0 of 7 tasks exposed)` | present |
| `--allow build,test` | starts | `rune MCP server on stdio (2 of 7 tasks exposed)` | present |
| `--allow build --allow-destructive clean` | starts | `rune MCP server on stdio (1 of 7 tasks exposed; destructive tasks allowed: clean)` | present |
| none | starts | `rune MCP server on stdio` | present |

## Regression checks

- With neither flag, the existing MCP integration tests pass unchanged
  (`destructive_mcp_test.go`, `dependency_refusal_mcp_test.go`, `tool_hints_mcp_test.go`,
  `unknown_args_mcp_test.go`, `typed_schema_mcp_test.go`).
- A task with an `agent` body exposes the same tools to its agent as before
  (`internal/cli/mcp_test.go`).

## Manual check

```sh
rune serve --help          # lists --allow and --deny with one example each
```

`docs/mcp.md` and the `rune serve` row of `docs/cli.md` describe the same two flags.
