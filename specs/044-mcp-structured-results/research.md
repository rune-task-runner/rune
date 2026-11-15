# Research: Return Structured Tool Results

## R1. One capped result, two renderings

**Decision**: Add `capResult(r Result, limit int) Result`, which returns `r` with `Stdout` and
`Stderr` replaced by `capOutput(..., limit)`. Rename the body of `formatResult` to
`renderText(r Result) string`, which renders a result that is already capped. Keep
`formatResult(r, limit)` as `renderText(capResult(r, limit))`. The handler calls `capResult`
once and passes the value to `renderText` and to the structured content.

**Rationale**: FR-007 and FR-012 say the fields are the strings the text is built from. Capping
once and rendering twice makes that true by construction, not by two code paths that agree.
`formatResult` keeps its signature and behavior, so the existing tests that pin the text pass
unchanged (FR-014).

**Alternatives considered**: Parsing the text back into fields. The text is ambiguous (a task
can print `[exit 0]`), which is the reason for the feature.

## R2. The structured value

**Decision**: An unexported struct:

```go
type structuredResult struct {
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	FixSuggestion string `json:"fixSuggestion,omitempty"`
}
```

set as `CallToolResult.StructuredContent`. `omitempty` gives FR-009: the text has a
fix-suggestion section exactly when `FixSuggestion` is not empty.

**Rationale**: A struct fixes the field set (FR-010) and the JSON names at compile time. The SDK
marshals `StructuredContent` as is. The engine already delivers `FixSuggestion` masked and
capped at 8 KiB, and the text prints it unchanged, so the field equals the section (FR-009,
FR-012).

**Alternatives considered**: `map[string]any`. Field names would be strings checked only by
tests. An exported type: no embedder needs it to receive results, and it can be exported later.

## R3. The output schema

**Decision**: `outputSchema() map[string]any` returns a new map on each call:

```json
{
  "type": "object",
  "properties": {
    "exitCode": {"type": "integer", "description": "..."},
    "stdout": {"type": "string", "description": "..."},
    "stderr": {"type": "string", "description": "..."},
    "fixSuggestion": {"type": "string", "description": "..."}
  },
  "required": ["exitCode", "stdout", "stderr"],
  "additionalProperties": false
}
```

with the description texts of [contracts/mcp-results.md](contracts/mcp-results.md). `toolFor`
sets it as `Tool.OutputSchema`.

**Rationale**: The same form as `inputSchema`, which is a `map[string]any` with
`additionalProperties: false`. A function, not a package variable, follows Principle VIII and
keeps tools from sharing a mutable map. SDK v1.6.1 `Server.AddTool` accepts any value that
marshals to a JSON object of type `object`. The schema is the same for every tool, so the tool
JSON that `Replace` compares (spec 043) changes for no tool between loads (FR-018).

**Alternatives considered**: `*jsonschema.Schema` from the SDK's schema package. It would make
`mcpserver` import that package in production code for no gain over a literal.

## R4. Results without structured content

**Decision**: `errorResult` is unchanged. It is used for refusals, undeclared argument names,
invalid arguments, undecodable arguments, and an error returned by the engine before a run. None
of them has structured content.

**Rationale**: FR-016. No task ran, so there is no exit code. These results are marked as
errors. The protocol requires structured content to match the schema when it is present, and
the reference clients skip the requirement for error results. A task that ran and failed is
different: it has an exit code and carries structured content with `isError` set.

**Alternatives considered**: An invented exit code such as -1 or 2 for refusals. A client could
not tell it from a task that exited with that number.

## R5. No change in `internal/cli`

**Decision**: The adapter is not changed. Tests are added there.

**Rationale**: `mcpAdapter.call` writes both streams through the mask writer into bounded
buffers and returns `Result` with masked, capped strings and a masked, capped `FixSuggestion`.
The handler's `capResult` is a no-op for them, as `formatResult`'s cap is today. The agent task
endpoint builds its server with `mcpserver.New`, so it gets the schema and the structured
content with no change (FR-003).

## R6. Validating against the schema in tests

**Decision**: `mcpserver/structured_test.go` resolves the declared schema with
`github.com/google/jsonschema-go/jsonschema` and validates each structured result with it. The
module moves from the indirect to the direct block of `go.mod`.

**Rationale**: SC-002 is "valid against the schema the tool declares". A real validator checks
that claim, including `required` and `additionalProperties`, where a hand-written check would
restate the schema. The module is already downloaded and verified through the SDK, so no code
enters the build that was not there.

**Alternatives considered**: A hand-written validator in the test. It would test the test.

## R7. Tests

**Decision**:

- `mcpserver/structured_test.go`: schema literal; every listed tool carries it; results for
  success, failure, empty output, fix suggestion, and a printed `[exit 0]`; no `fixSuggestion`
  key when empty (raw JSON); schema validation for a table of results; rebuild-the-text property
  (SC-006) over a table that covers every joining case; cap with `OutputLimit`; no structured
  content for each `errorResult` path; exactly one text block; `capResult` and `renderText`
  units; unchanged notification behavior after `Replace`.
- `internal/cli/mcp_structured_test.go`: through the real adapter and server: a secret on both
  streams and in the fix suggestion; 3 MB on both streams; `mcp_output_limit`; the agent
  endpoint's options produce the schema.
- `test/integration/structured_mcp_test.go`: the real binary over stdio with the `startMCP`
  client of spec 043: output schema on every tool; a call's structured content against its text
  (stands for `TestMCPE2E_Pending_StructuredResult`); a refusal has none; with `--allow` the
  listed entries differ from before only by the schema (already covered by
  `TestAllowlistMCP_ListedEntriesUnchanged`, which compares raw entries across flags).

**Rationale**: Principle VI layers, and the maintainer rule that every new function has a test.

## R8. Docs and release note

**Decision**: `docs/mcp.md` gets a "Structured results" section after the paragraph that
explains `[exit N]`: the four fields, the JSON example, the rule that rebuilds the text, which
results have none, and that both forms obey the output limit. The commit is `feat(mcp):`, so the
changelog carries it.

**Rationale**: FR-020. Not breaking: nothing existing changes.
