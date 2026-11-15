# Quickstart: validating structured results

Automated: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/ ./internal/cli/ ./test/integration/ -run 'Structured|OutputSchema'`.

Manual, with the built binary and any MCP client (the MCP Inspector works):

1. In an empty directory, create `Runefile`:

   ```text
   # Runs the tests.
   test: || hint
       @echo running tests
       @echo "FAIL: fixtures out of date" >&2
       @exit 1

   hint:
       @echo "re-run with -update"

   # Deletes things.
   [confirm]
   clean:
       @echo cleaned
   ```

2. Start `rune mcp` from the client and list the tools. Expected: `test`, `hint` and `clean`
   each show the output schema of [contracts/mcp-results.md](contracts/mcp-results.md).
3. Call `test`. Expected: the text is

   ```text
   running tests
   FAIL: fixtures out of date
   diagnosing: hint
   [exit 1]
   [fix suggestion - from Runefile || failure hooks]
   re-run with -update
   ```

   and the structured content is `exitCode` 1, `stdout` `"running tests\n"`, `stderr`
   `"FAIL: fixtures out of date\ndiagnosing: hint\n"` (Rune reports the failure hook on stderr),
   `fixSuggestion` `"re-run with -update"`.
4. Call `hint`. Expected: `exitCode` 0, `stdout` `"re-run with -update\n"`, `stderr` `""`, and
   no `fixSuggestion` field.
5. Call `clean`. Expected: an error result with the refusal text and no structured content.
