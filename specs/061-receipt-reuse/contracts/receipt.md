# Contract: Receipt Format Change

Spec 060's format version 1 gains one optional predicate field. The version stays 1: readers of
spec 060 ignore the field, and its absence means no override.

| Field | Type | Placement | Rule |
|---|---|---|---|
| `overrides` | array of strings | after `argumentsRedacted` | the names of the variables overridden with `NAME=VALUE` on the command line of the invocation, sorted, each passed through the secret mask; omitted when there are none |

Example predicate excerpt:

```json
{
  "version": 1,
  "task": "build",
  "arguments": [],
  "overrides": ["mode"],
  "role": "task",
  "status": "passed"
}
```

A run started through `rune mcp` or `rune serve` has no command-line overrides and never writes
the field.

A reused run's receipt follows spec 060's `cached` form: no `exitCode`, `related.reuses` names
the reused receipt, `source.before` and `source.after` are the snapshot it was reused on.
