# Quickstart: validating the cleartext warning

Automated:

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/ ./internal/cli/ ./cmd/rune/ ./test/integration/ -run 'Loopback|Cleartext|WarnTransport|BindWarning|NilWarn|FailedStart|Default(HTTP)?Addr'
```

Manual, with the built binary:

1. In an empty directory, create `Runefile` and a token:

   ```text
   # Says hello.
   hello:
       @echo hello
   ```

   ```sh
   printf 'secret\n' > token
   ```

2. `rune serve --http --addr 0.0.0.0:7777 --token-file ./token`. Expected stderr:

   ```text
   rune MCP server on http://0.0.0.0:7777 (token required)
   warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext
   ```

   The server keeps running. `curl -i -X POST http://127.0.0.1:7777` answers `401`.
3. Stop it and run the same with `--addr :7777`. Expected: the same warning.
4. Run it with `--addr 127.0.0.1:7777`, then with no `--addr`. Expected: the startup line only.
5. Start a second server on the port of a running one with `--addr 0.0.0.0:7777`. Expected: the
   bind error and no warning.
6. `rune serve --help`. Expected: the HTTP example binds `127.0.0.1:7777`, and the text mentions
   the warning.

See [contracts/http-bind-warning.md](contracts/http-bind-warning.md) for the exact output.
