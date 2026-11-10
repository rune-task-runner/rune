# Contract: `tools/call` Refusals

**Feature**: 034-mcp-dependency-refusal | **Spec**: [spec.md](../spec.md)

`tools/list` and every hint are unchanged (FR-015). This contract covers what `tools/call` returns
when a call can run a destructive task the operator has not granted.

## Result form

A refusal is `isError: true` with one text content item holding only the refusal text. It has no
`[exit N]` line and no task output, the same form as today's direct refusal (FR-009). Nothing in the
call runs (FR-004).

## Refusal texts

`X` is the called task. The other names are the ungranted destructive tasks it can reach, in reach
order ([data model](../data-model.md#reach)). Every name is the task name, not the tool name, so
`tools::clean` stays `tools::clean`.

- In the subject, each name is double-quoted, and names are joined with `, `.
- In the flag value, names are bare and joined with `,` with no spaces, so they paste as one
  `--allow-destructive` value.
- `it` and `is` are used when exactly one task is named; `them` and `are` are used for two or more.

### With a flag (`rune mcp`, `rune serve`, an embedder that sets `AllowDestructiveFlag`)

| `X` refused | Reached refused | Text |
|---|---|---|
| yes | none | `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server` (today's, byte-identical) |
| no | `clean` | `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server` |
| no | `clean`, `wipe` | `task "full" can run destructive tasks "clean", "wipe"; to permit them, add clean,wipe to --allow-destructive and restart the server` |
| yes | `wipe` | `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server` |

### With no flag (the endpoint an `agent` task opens, an embedder that leaves the flag empty)

| `X` refused | Reached refused | Text |
|---|---|---|
| yes | none | `task "clean" is destructive and is not allowed in this session` (today's, byte-identical) |
| no | `clean` | `task "release" can run destructive task "clean"; it is not allowed in this session` |
| no | `clean`, `wipe` | `task "full" can run destructive tasks "clean", "wipe"; they are not allowed in this session` |
| yes | `wipe` | `task "reset" is destructive and can run destructive task "wipe"; they are not allowed in this session` |

### Precedence

1. An allow-list refusal (`task "X" is not authorized for this session`) comes first and is
   unchanged.
2. The destructive refusal above comes second.
3. Argument errors come after both (spec 032 FR-006). A call that is both refused and badly formed
   gets the refusal.

## Calls, by Runefile and grant

Rows use the quickstart's scratch Runefile ([quickstart.md](../quickstart.md)).

| Call | Grant | Result |
|---|---|---|
| `release: build clean` | none | Refused, naming `clean`. Nothing runs |
| `release` | `clean` | `build`, `clean`, `release` run. `[exit 0]` |
| `full: build clean wipe` | none | Refused, naming `clean`, `wipe` |
| `full` | `clean` | Refused, naming only `wipe` |
| `ordered: stage clean`, `stage: wipe` | none | Refused, naming `wipe`, `clean`, in that order |
| `publish: && clean` | none | Refused, naming `clean`. `publish` does not run |
| `deploy: \|\| wipe`, a body that succeeds | none | Refused, naming `wipe`. `deploy` does not run |
| `reset: wipe`, both `[confirm]` | none | Refused, naming `reset`, `wipe` |
| `reset` | `reset` | Refused, naming only `wipe` |
| `reset` | `reset,wipe` | `wipe`, `reset` run |
| `outer: inner`, `inner: clean` | none | Refused, naming `clean` |
| `ship: _scrub` (`_scrub` private) | none | Refused, naming `_scrub`. `_scrub` is still not a tool |
| `shipit: tools::clean` | none | Refused, naming `tools::clean` |
| `dispatch: wipe-win` (`[windows, confirm]`) | none | Off Windows: `dispatch` runs. On Windows: refused, naming `wipe-win` |
| `rollback: \|\| wipe` | none | Refused, naming `wipe` |
| `risky: \|\| rollback` | none | Not refused: `wipe` is only `rollback`'s own failure hook. `risky` fails with its own exit code, and `wipe` does not run |
| `clean` | none | Today's direct refusal |
| `lint` | none | Runs, as today |

## Unchanged

- `tools/list`: the same tools, schemas, and hints as on `main` at `840ebef`.
- The startup line and the `--allow-destructive` warnings (spec 031).
- CLI runs, including `[confirm]` prompts and `--yes` (spec 027).
