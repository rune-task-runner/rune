# Quickstart: Checking the File Flag of the MCP Server

## Prerequisites

- Docker, for the tests.
- A `rune` built from this branch, for the checks by hand:
  `go build -o /tmp/rune ./cmd/rune`.

## 1. The tests pass

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./cmd/rune/... ./test/integration/... ./test/docs/...
```

Expected: `ok` for each package. The tests of this feature are `TestServeFileFlag*`,
`TestServeHasNoRootFlags`, `TestOnlyServeHasFileFlag`, `TestServeHelpMentionsFile` and
the `-f` cases of `TestServeRejectsYes` in `cmd/rune`, and `TestFileFlagMCP_*` in
`test/integration`.

## 2. By hand: serve a Runefile from another directory

```sh
mkdir -p /tmp/ff/proj /tmp/ff/else
printf '# Say where it runs.\nwhere:\n    @pwd\n' > /tmp/ff/proj/Runefile
cd /tmp/ff/else
(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"where","arguments":{}}}'; sleep 1) \
  | /tmp/rune mcp -f /tmp/ff/proj/Runefile
```

Expected: the reply to id 2 holds `/tmp/ff/proj`, not `/tmp/ff/else` (SC-001). The same
with `/tmp/rune -f /tmp/ff/proj/Runefile mcp`, `/tmp/rune serve --file /tmp/ff/proj/Runefile`
and `/tmp/rune --file /tmp/ff/proj/Runefile serve` (SC-002).

## 3. By hand: what stays closed

```sh
cd /tmp/ff/else
/tmp/rune mcp --yes; echo "exit=$?"
/tmp/rune mcp -f /tmp/ff/proj/Runefile --yes; echo "exit=$?"
/tmp/rune --yes mcp; echo "exit=$?"
```

Expected, three times: `rune: unknown flag: --yes` and `exit=2` (SC-003).

## 4. By hand: mistakes

```sh
cd /tmp/ff/proj
/tmp/rune mcp -f ""; echo "exit=$?"            # rune: --file needs a path
/tmp/rune mcp -f /tmp/ff/nope; echo "exit=$?"  # the path, and "no such file or directory"
/tmp/rune mcp -f /tmp/ff/else; echo "exit=$?"  # rune: --file points at a directory, not a Runefile
```

Expected: `exit=2` each time, although `/tmp/ff/proj` has a Runefile of its own (SC-004).

## 5. The documentation

- `rune serve --help` lists `-f, --file` and has an example with it.
- `docs/mcp.md`, "Starting the server": the flag, a user-level example, one server per
  project.
- `docs/cli.md`: `[-f <path>]` in the `rune serve` row.
- `docs/troubleshooting.md`, "no Runefile found": the fix for a server.

## 6. The whole suite

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
```

Expected: every package `ok` (SC-005, SC-007).
