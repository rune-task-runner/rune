# Contract: `[readonly]` and RUNE2018

**Feature**: 033-mcp-tool-hints | Covers FR-001, FR-007 to FR-009, FR-013 to FR-015

## Grammar

One new alternative in `AttrItem`, in `docs/GRAMMAR.md`:

```text
| "readonly"                 (* claims read-only: sets MCP readOnlyHint *)
```

It takes no arguments. `[readonly("x")]` is the syntax error `[network("x")]` gives today
(`RUNE1001`). `[readonly]` twice on one task is accepted, as `[network]` twice is. The formatter prints
it as `[readonly]` on its own line.

On the command line it changes nothing: not how the task runs, and not `--list`. `--dump` shows it
with the task's other attributes.

## RUNE2018

Severity: error. It stops the commands other analyzer errors stop, with exit status 3 and nothing run:
running a task, `--list`, `--dump`, `--summary`, `--dry-run`, and `rune mcp`. `rune analyze` prints it
as `error[RUNE2018]:`, and the run path as `error:`, with the same text. The language server reports
it. `--fmt` runs no analysis, so it formats the file and exits 0.

| Fault | Reported at | Text |
|---|---|---|
| `[readonly]` and `[confirm]` on one task | The `[readonly]` attribute | `[readonly] task "<task>" cannot use [confirm]: remove one of them` |
| A dependency or hook has `[confirm]`, and no `[readonly]` | The reference | `[readonly] task "<task>" runs "<target>", which uses [confirm]: remove [readonly] from "<task>"` |
| A dependency or hook has neither | The reference | `[readonly] task "<task>" runs "<target>", which is not [readonly]: add [readonly] to "<target>" or remove it from "<task>"` |

`<task>` and `<target>` are composed names, such as `tools::clean`.

Rules:

- Only direct references are checked. Each task in a chain answers for its own, so a broken chain gives
  one error, at the reference that needs the fix.
- A target with both attributes passes the reference check and gets the first row's error itself.
- A target restricted to another OS is checked like any other, so the result is the same on every host.
- A reference named twice is reported twice, once at each place.
- An unknown target gets `RUNE2001` only.
- `[readonly]` with `[network]`, `[private]`, or `[context]` is allowed, and so is `[readonly]` on an
  `agent`-executor task, whose run-time tool calls do not count (FR-014).

## `docs/diagnostics.md`

A row in the Semantic table:

```text
| `RUNE2018` | Invalid `[readonly]`: the task also uses `[confirm]`, or runs a dependency or hook that is not `[readonly]` |
```
