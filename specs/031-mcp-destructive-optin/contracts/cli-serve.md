# Contract: `rune serve` / `rune mcp` with `--allow-destructive`

**Feature**: 031-mcp-destructive-optin | Covers FR-001, FR-007 to FR-013, FR-017

## Synopsis

```text
rune serve [--http] [--addr <addr>] [--token-file <file>] [--ignore-version]
           [--allow-destructive <task>[,<task>...]]...
rune mcp   [same flags]
```

`--allow-destructive` is local to `serve` and its alias `mcp`. It takes one or
more task names separated by commas and may be repeated. The names are the
Runefile task names `rune <task>` accepts, `ns::task` for imported tasks.

## Accepted forms

| Command | Granted, in order |
|---|---|
| `rune mcp` | none |
| `rune mcp --allow-destructive clean` | `clean` |
| `rune mcp --allow-destructive clean,reset` | `clean`, `reset` |
| `rune mcp --allow-destructive clean --allow-destructive reset` | `clean`, `reset` |
| `rune mcp --allow-destructive "clean, reset"` | `clean`, `reset` |
| `rune mcp --allow-destructive clean,clean` | `clean` |
| `rune mcp --allow-destructive tools::clean` | `tools::clean` |

## Errors

All exit with status 2, print `rune: <message>` on stderr, and run no Runefile
code, including the `[context]` hook.

| Command | Message |
|---|---|
| `rune mcp --allow-destructive ""` | `--allow-destructive: empty task name` |
| `rune mcp --allow-destructive clean,` | `--allow-destructive: empty task name` |
| `rune mcp --allow-destructive claen` (a task `clean` exists) | `unknown task in --allow-destructive: claen (did you mean "clean"?)` |
| `rune mcp --allow-destructive zzz` (nothing close) | `unknown task in --allow-destructive: zzz` |
| `rune mcp --allow-destructive '*'` | `unknown task in --allow-destructive: *` |
| `rune mcp --allow-destructive` (no value) | the flag library's missing-argument error |
| `rune mcp --yes` | `unknown flag: --yes` (unchanged) |
| `rune --yes mcp` | `unknown flag: --yes` (unchanged) |

When several problems exist, the first one in this order is reported: flag
parsing and `--addr`/`--token-file` without `--http` (unchanged), empty name,
Runefile resolution and static errors (unchanged), unknown name, HTTP transport
errors (unchanged).

## Warning

For each granted name whose task has no `[confirm]`, once, before the server
starts, in the shared warning style:

```text
warning: --allow-destructive build has no effect: task "build" is not destructive
```

The server then starts normally.

## Startup line (stderr, dimmed as today)

| Transport | No grant (unchanged) | Grant `clean`, `reset` |
|---|---|---|
| stdio | `rune MCP server on stdio` | `rune MCP server on stdio (destructive tasks allowed: clean, reset)` |
| HTTP | `rune MCP server on http://ADDR (token required)` | `rune MCP server on http://ADDR (token required; destructive tasks allowed: clean, reset)` |

Only granted names whose task has `[confirm]` and can run on this host are
listed. `--allow-destructive build`
alone leaves the line unchanged.

## Help text

`rune serve --help` lists:

```text
      --allow-destructive stringArray   let agents run these [confirm] tasks (comma-separated, repeatable)
```

The exact type column comes from the flag library. The `Long` text gains one
sentence: tasks marked `[confirm]` are refused unless named with
`--allow-destructive`. `Example` gains:

```text
  rune mcp --allow-destructive clean                  # let agents run the [confirm] task 'clean'
```

## Unchanged

- Root `--help` and the "Global flags" section of `docs/cli.md`.
- `rune <task>`, `rune --yes <task>`, and the terminal rule from spec 027.
- Every other `serve` flag and its validation.
