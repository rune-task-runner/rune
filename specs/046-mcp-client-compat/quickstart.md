# Quickstart: validating the client compatibility research

## Automated

The recorder and every recording:

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/integration/ -run 'TestClient|TestCancelledCall|TestPrivateData' -v
```

Expected: `TestClientRecordings` passes with one subtest per recording, nine in all, and
the `TestClientRecorder_*`, `TestCancelledCall`, `TestPrivateData` and
`TestClientRecordingsHoldNoPrivateData` tests pass.

The guide, its links and the site pages made from it:

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...
```

The recorder is under `testdata`, so name it to vet and lint it:

```sh
go vet ./test/integration/testdata/clients/recorder
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run ./test/integration/testdata/clients/recorder/
```

Nothing but documentation, tests and test data changed:

```sh
git diff --stat main -- . ':!docs' ':!specs' ':!test' ':!.specify'
```

Expected: no output.

## By hand

1. Open `docs/mcp.md`, section "Client compatibility". For each of the five columns, every
   row has a cell, and every cell ends with an evidence label or says "unknown" or "not
   tested". Spec: FR-002, FR-003, FR-007.
2. Pick a cell marked "recorded", for example "Lists the tools again" for Claude Code. In
   `test/integration/testdata/clients/README.md`, find the recording that shows it
   (`claude-code/session.jsonl`). In that file, a `tools/list` from the client follows the
   server's `notifications/tools/list_changed`. Then pick "Project context reaches the
   model": `claude-code/session.reply.txt` names the codename and says it came from the
   server instructions. Spec: US2.
3. Record a session with a scripted client, as a stand-in for a real one:

   ```sh
   go build -o /tmp/rune-rec/rune ./cmd/rune
   go build -o /tmp/rune-rec/recorder ./test/integration/testdata/clients/recorder
   cp -r test/integration/testdata/clients/scenario /tmp/rune-rec/project
   cd /tmp/rune-rec/project
   (printf '%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
     '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"grow","arguments":{}}}'; sleep 3) \
     | /tmp/rune-rec/recorder -out /tmp/rune-rec/session.jsonl /tmp/rune-rec/rune mcp --allow-destructive clean
   ```

   Expected in `/tmp/rune-rec/session.jsonl`: the three client messages, the server's
   replies, the stderr line `reloaded Runefile: 1 tool added, 0 removed, 0 changed`, a
   `notifications/tools/list_changed` from the server, `client closed stdin` and
   `server exited with code 0`. Spec: US3, FR-013.
4. Search the recordings for the recording machine and the account:

   ```sh
   grep -rnE '/home/|/Users/|toolu_[0-9A-Za-z]{8,}|ctc_[0-9a-f]{8,}' test/integration/testdata/clients --include='*.jsonl' --include='*.reply.txt'
   ```

   Expected: no output. `TestClientRecordingsHoldNoPrivateData` is the full check: it
   also rejects a UUID that is not a placeholder. Spec: FR-017, SC-006.
