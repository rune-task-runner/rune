# Quickstart: Validating the Up-Front Refusal

**Feature**: 034-mcp-dependency-refusal | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

The texts are in [contracts/mcp-tools.md](contracts/mcp-tools.md), and the library changes in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md). This guide only says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

Scenarios A to R below are automated in `test/integration/dependency_refusal_mcp_test.go`. The
Windows half of N is automated in `internal/cli/mcp_test.go` with an injected OS. S is automated in
`internal/cli/mcp_test.go`, T in `mcpserver/transport_test.go`, and U in `mcpserver/authz_test.go`.
The docs harness (`test/docs`) validates the changed pages.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Builds the artifacts.
build:
    @echo built > build.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Wipes everything.
[confirm("Wipe?")]
wipe:
    @echo wiped > wipe.done

# Cuts a release.
release: build clean
    @echo released > release.done

# Cuts a full release.
full: build clean wipe
    @echo full > full.done

# Stages, then cleans.
ordered: stage clean
    @echo ordered > ordered.done

# Stages after a wipe.
stage: wipe
    @echo staged > stage.done

# Publishes, then cleans.
publish: && clean
    @echo published > publish.done

# Deploys, wiping on failure.
deploy: || wipe
    @echo deployed > deploy.done

# Resets, which also wipes.
[confirm("Reset?")]
reset: wipe
    @echo reset > reset.done

# Reaches clean two levels down.
outer: inner
    @echo outer > outer.done

# Reaches clean one level down.
inner: clean
    @echo inner > inner.done

# Ships after a private scrub.
ship: _scrub
    @echo shipped > ship.done

[private]
[confirm("Scrub?")]
_scrub:
    @echo scrubbed > scrub.done

# Dispatches per OS.
dispatch: wipe-win
    @echo dispatched > dispatch.done

# Wipes on Windows.
[windows, confirm("Wipe?")]
wipe-win:
    @echo wiped > wipe-win.done

# Rolls back, wiping if that fails.
rollback: || wipe
    @exit 1

# Fails, then rolls back.
risky: || rollback
    @exit 1

# Checks the tree.
lint:
    @echo linted > lint.done
```

## Calls over stdio

Start `rune mcp` in that directory with the grant shown, send `initialize` and
`notifications/initialized`, then `tools/call` with no arguments. Remove the markers between rows.
"Refused" means `isError: true` with exactly the contract text for those names, and no marker file.

| # | Call | Grant | Expected |
|---|---|---|---|
| A | `release` | none | Refused: `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server` |
| B | `release` | `clean` | `[exit 0]`; `build.done`, `clean.done`, `release.done` |
| C | `full` | none | Refused, naming `"clean", "wipe"`, with `add clean,wipe` |
| D | `full` | `clean` | Refused, naming only `wipe` |
| E | `ordered` | none | Refused, naming `"wipe", "clean"` in that order, with `add wipe,clean` |
| F | `publish` | none | Refused, naming `clean`; no `publish.done` |
| G | `deploy` | none | Refused, naming `wipe`; no `deploy.done` |
| H | `reset` | none | Refused: `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server` |
| I | `reset` | `reset` | Refused, naming only `wipe` |
| J | `reset` | `reset,wipe` | `wipe.done`, `reset.done` |
| K | `outer` | none | Refused, naming `clean`; no `inner.done` |
| L | `ship` | none | Refused, naming `_scrub`; `_scrub` is not in `tools/list` |
| M | Add `mod tools "tools.rune"`, a `tools.rune` with a `[confirm]` task `clean`, and `shipit: tools::clean`. Call `shipit` | none | Refused, naming `tools::clean`, with `add tools::clean` |
| N | `dispatch` | none | Off Windows: `[exit 0]`, `dispatch.done`. On Windows: refused, naming `wipe-win` |
| O | `rollback` | none | Refused, naming `wipe` |
| P | `risky` | none | Not refused: `isError: true` with `[exit 1]`, and no `wipe.done` |
| Q | `clean` | none | Today's direct refusal, byte-identical |
| R | `release` with arguments `{"bogus": 1}` | none | The refusal of row A, not an argument error |

`tools/list` is unchanged (FR-015): spec 033's `TestToolHintsMCP_CoverDependencies` and
`TestToolHintsMCP_GrantChangesNothing` keep passing with no edit.

## Other routes

| # | Check | Expected |
|---|---|---|
| S | The endpoint an `agent` task opens for its own agent; call `release` | `task "release" can run destructive task "clean"; it is not allowed in this session`, and nothing ran |
| T | `rune serve --http --token-file tok`; call `release` with the token | The same text as row A |
| U | An embedder's engine whose `release` descriptor leaves `DestructiveReach` nil | The call reaches the engine, as today |

## Before and after

On a binary built from `main` at `840ebef`:

- Rows A, C, F, and I return a bare `[exit 1]`.
- Rows A and C leave `build.done` behind, and row F leaves `publish.done`.
- Row H names only `reset`, so restarting with that grant leads to row I's bare `[exit 1]`.

## Docs

```sh
grep -n -i 'itself is refused\|without being refused' docs/mcp.md docs/runefile.md SECURITY.md
```

No hit may remain. Each page in [research R8](research.md#r8-documentation) says that a call that can
run an ungranted `[confirm]` task is refused before anything runs.
