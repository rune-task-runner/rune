# Data Model: Return Structured Tool Results

No persistent data.

## `mcpserver.Result` (existing, unchanged)

What an engine returns: `Stdout`, `Stderr`, `ExitCode`, `FixSuggestion`.

## Capped result

`capResult(r, limit)`: the same value with `Stdout` and `Stderr` each at most `limit` bytes
(spec 042 rules). `ExitCode` and `FixSuggestion` are untouched. Both renderings below read only
this value.

## `structuredResult` (unexported)

| JSON field | Go field | Source | Presence |
|---|---|---|---|
| `exitCode` | `ExitCode int` | capped result | always |
| `stdout` | `Stdout string` | capped result | always, may be `""` |
| `stderr` | `Stderr string` | capped result | always, may be `""` |
| `fixSuggestion` | `FixSuggestion string` | capped result | only when not `""` |

## Text rule

`renderText` of the capped result, unchanged from today:

1. `stdout`.
2. If `stderr` is not empty: a line break when the text so far is not empty and `stdout` does
   not end with one, then `stderr`.
3. A line break when the text so far is not empty and does not end with one.
4. `[exit N]` with `N = exitCode`.
5. If `fixSuggestion` is not empty: a line break, the line
   `[fix suggestion - from Runefile || failure hooks]`, a line break, then `fixSuggestion`.

## Result kinds

| Kind | Text | `isError` | Structured content |
|---|---|---|---|
| Task ran, exit 0 | rendered | false | yes |
| Task ran, exit not 0 | rendered | true | yes |
| Refusal, bad arguments, engine error before a run | message | true | no |
