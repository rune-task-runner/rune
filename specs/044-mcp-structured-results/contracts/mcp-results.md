# Contract: MCP tool results

## `tools/list`

Every tool has, beside its existing fields:

```json
"outputSchema": {
  "type": "object",
  "properties": {
    "exitCode": {
      "type": "integer",
      "description": "Exit status of the run: 0 on success. The same number as the [exit N] line of the text result."
    },
    "stdout": {
      "type": "string",
      "description": "What the run wrote to standard output, with secrets masked. Output over the server's limit keeps its beginning and its end around a [truncated N bytes] line."
    },
    "stderr": {
      "type": "string",
      "description": "What the run wrote to standard error, with secrets masked. Output over the server's limit keeps its beginning and its end around a [truncated N bytes] line."
    },
    "fixSuggestion": {
      "type": "string",
      "description": "Output of the task's || failure hooks. Present only when a failed run produced one."
    }
  },
  "required": ["exitCode", "stdout", "stderr"],
  "additionalProperties": false
}
```

The schema is the same for every tool and every transport. Nothing else in a tool's entry
changes.

## `tools/call`, task ran

```json
{
  "content": [{"type": "text", "text": "running tests\nFAIL: fixtures out of date\ndiagnosing: hint\n[exit 1]\n[fix suggestion - from Runefile || failure hooks]\nre-run with -update"}],
  "isError": true,
  "structuredContent": {
    "exitCode": 1,
    "stdout": "running tests\n",
    "stderr": "FAIL: fixtures out of date\ndiagnosing: hint\n",
    "fixSuggestion": "re-run with -update"
  }
}
```

- `content` is one text block, byte for byte what it was before this feature.
- `isError` is `exitCode != 0`.
- `stdout` and `stderr` are the captured streams after masking and the output cap. They are
  always present. They do not include the line breaks the text adds between its parts.
- `fixSuggestion` is absent unless the text has a fix-suggestion section.
- The text can be rebuilt from the fields by the rule in [data-model.md](../data-model.md).

## `tools/call`, no task ran

A refusal (destructive task not granted), an undeclared argument name, an invalid or undecodable
argument, or an engine error before a run:

```json
{
  "content": [{"type": "text", "text": "task \"clean\" is destructive; to permit it, add clean to --allow-destructive and restart the server"}],
  "isError": true
}
```

No `structuredContent`. Unchanged from before this feature.
