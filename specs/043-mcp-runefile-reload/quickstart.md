# Quickstart: validating the reload

Automated: `docker compose run --rm test go test ./mcpserver/ ./internal/cli/ ./test/integration/ -run 'Reload|Replace|Instructions'`
(see the project memory for the exact Docker invocation).

Manual, with the built binary and any MCP client (the MCP Inspector works):

1. In an empty directory, create `Runefile`:

   ```text
   # Builds.
   build:
       @echo built
   ```

2. Start `rune mcp` from the client. List tools: `build`.
3. Append a task and save:

   ```text
   # Lints.
   lint:
       @echo linted
   ```

   Expected: the client shows a tool-list-changed notification within 2 seconds, the list holds
   `build` and `lint`, and stderr holds `reloaded Runefile: 1 tool added, 0 removed, 0 changed`.
4. Change `build:` to `build: nope` and save. Expected: no notification, both tools still
   listed and callable, stderr shows the unknown-task diagnostic with `Runefile:line:col` and
   `reload failed: 1 static error(s); keeping the previous tools`.
5. Undo the change and save. Expected: `reloaded Runefile: 0 tools added, 0 removed, 0 changed`
   and no notification.
6. Add `import "ci.rune"` and create `ci.rune` with a task. Expected: its task is listed. Edit
   `ci.rune`: the list follows.
7. Restart with `rune mcp --no-reload` and repeat step 3. Expected: nothing happens.

Contracts: [cli.md](contracts/cli.md), [mcp-protocol.md](contracts/mcp-protocol.md),
[mcpserver-api.md](contracts/mcpserver-api.md).
