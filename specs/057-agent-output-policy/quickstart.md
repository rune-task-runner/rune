# Quickstart: validating `[agent-output]` and `[agent-summary]`

The contracts are [runefile-attributes.md](contracts/runefile-attributes.md) and
[mcp-results.md](contracts/mcp-results.md).

## Prerequisites

- Docker, for the test suite.
- A built binary for the manual scenarios: `go build -o /tmp/rune ./cmd/rune` inside the
  test container.

## 1. The test suite

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/... ./internal/... ./test/...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/... ./internal/cli/...
```

Expected: all packages pass.

## 2. A noisy task keeps its last lines for agents

```sh
mkdir -p /tmp/policy-demo && cd /tmp/policy-demo
cat > Runefile <<'EOF'
# Run the tests.
[agent-output(tail="3")]
[agent-summary]
test: || hint
    @for i in $(seq 1 2000); do echo "ok $i"; done
    @echo "FAIL: TestParse" >&2
    @exit 1

hint:
    @echo "re-run with -update"
EOF
rune test; echo "exit $?"
```

Expected on the command line: all 2,000 `ok` lines, `FAIL: TestParse`, the usual failure
banner, exit 1. Nothing is trimmed.

Over MCP (any client, or the integration test helper), calling `test` returns:

- stdout: `[omitted 1997 lines, ... bytes]` then `ok 1998`, `ok 1999`, `ok 2000`;
- stderr: `FAIL: TestParse` and the `diagnosing: hint` line, whole (2 lines, within `tail`);
- `[exit 1]`, then the failure summary with `step: Runefile:7: exit 1` and
  `reason: exit status 1`;
- `fix suggestion: follows, ...` in the summary, then the fix suggestion section with
  `re-run with -update`;
- `isError: true`, and structured content with `output` and `failureSummary`.

## 3. A task without the attributes is unchanged

Remove both attribute lines and call `test` again. The text and the structured content are
what spec 044 defines: the 2,000 lines (well under 64 KiB), no omission line, no summary, no
`output` or `failureSummary` field.

## 4. Errors in the Runefile

```sh
mkdir -p /tmp/policy-bad && cd /tmp/policy-bad
printf '[agent-output(head="5")]\nbad:\n    @true\n' > Runefile
rune --list; echo "exit $?"
```

Expected: `Runefile:1:2: error: agent-output(...) requires tail="<lines>"` with a caret under
the attribute, and exit 3. The diagnostic's code, shown by the language server and in JSON
output, is `RUNE2021`.

## 5. Bounds

The `mcpserver` tests write 50 MB to a `LineBuffer` with the default limit and check that the
kept stream and the memory held stay within 64 KiB. The integration test calls a task that
prints several megabytes on each stream and checks each structured stream field against the
limit.

## 6. Documentation

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
```

Expected: `docs/runefile.md` and `docs/mcp.md` pass the code block and link checks, and the
noisy example in `docs/mcp.md` shows sizes measured from a real run.
