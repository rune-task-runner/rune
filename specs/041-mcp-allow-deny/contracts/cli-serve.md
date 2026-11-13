# Contract: `rune serve` / `rune mcp` with `--allow` and `--deny`

**Feature**: 041-mcp-allow-deny | Covers FR-001, FR-002, FR-009 to FR-014, FR-016, FR-019

## Synopsis

```text
rune serve [--http] [--addr <addr>] [--token-file <file>] [--ignore-version]
           [--allow-destructive <task>[,<task>...]]...
           [--allow <task>[,<task>...]]...
           [--deny <task>[,<task>...]]...
rune mcp   [same flags]
```

`--allow` and `--deny` are local to `serve` and its alias `mcp`. Each takes one or more task names
separated by commas and may be repeated. The names are the Runefile task names `rune <task>`
accepts, `ns::task` for imported tasks.

## Accepted forms

On a Runefile whose exposable tasks are `build`, `test`, `deploy`, and `tools::lint`:

| Command | Tools |
|---|---|
| `rune mcp` | `build`, `test`, `deploy`, `tools__lint` |
| `rune mcp --allow build,test` | `build`, `test` |
| `rune mcp --allow build --allow test` | `build`, `test` |
| `rune mcp --allow "build, test"` | `build`, `test` |
| `rune mcp --allow build,build` | `build` |
| `rune mcp --allow tools::lint` | `tools__lint` |
| `rune mcp --deny deploy` | `build`, `test`, `tools__lint` |
| `rune mcp --deny deploy,test` | `build`, `tools__lint` |
| `rune mcp --allow build,deploy --deny deploy` | `build` |
| `rune mcp --allow build --deny build` | none (warning below) |

## Errors

All exit with status 2, print `rune: <message>` on stderr, and run no Runefile code, including the
`[context]` hook.

| Command | Message |
|---|---|
| `rune mcp --allow ""` | `--allow: empty task name` |
| `rune mcp --allow build,` | `--allow: empty task name` |
| `rune mcp --deny ""` | `--deny: empty task name` |
| `rune mcp --allow biuld` (a task `build` exists) | `unknown task in --allow: biuld (did you mean "build"?)` |
| `rune mcp --deny biuld` | `unknown task in --deny: biuld (did you mean "build"?)` |
| `rune mcp --allow zzz` (nothing close) | `unknown task in --allow: zzz` |
| `rune mcp --allow '*'` | `unknown task in --allow: *` |
| `rune mcp --allow tools__lint` | `unknown task in --allow: tools__lint (did you mean "tools::lint"?)` |
| `rune mcp --allow` (no value) | the flag library's missing-argument error |

When several problems exist, the first one in this order is reported: flag parsing and
`--addr`/`--token-file` without `--http` (unchanged); empty name in `--allow-destructive`, then
`--allow`, then `--deny`; Runefile resolution and static errors (unchanged); unknown name in
`--allow-destructive`, then `--allow`, then `--deny`; HTTP transport errors (unchanged).

The `--allow-destructive` messages are byte-identical to spec 031.

## Warnings

In the shared warning style, on stderr. The server then starts normally.

For each name in `--allow` or `--deny` whose task is private or is the `[context]` hook, once,
before the `[context]` hook runs:

```text
warning: --allow _setup has no effect: task "_setup" is never exposed to agents
warning: --deny _setup has no effect: task "_setup" is never exposed to agents
```

A name whose task is restricted to another OS prints nothing.

When `--allow` or `--deny` was given and no task is exposed on this host, once, before the startup
line:

```text
warning: no tasks are exposed to agents; check --allow and --deny
```

## Startup line (stderr, dimmed as today)

With neither `--allow` nor `--deny`, the line is unchanged from spec 031. With either, it reports
the exposed count out of the exposable count:

| Transport | Filters only | Filters and grant `clean` |
|---|---|---|
| stdio | `rune MCP server on stdio (2 of 14 tasks exposed)` | `rune MCP server on stdio (2 of 14 tasks exposed; destructive tasks allowed: clean)` |
| HTTP | `rune MCP server on http://ADDR (token required; 2 of 14 tasks exposed)` | `rune MCP server on http://ADDR (token required; 2 of 14 tasks exposed; destructive tasks allowed: clean)` |

The count is printed whenever a filter flag was given, including `0 of 14` and `14 of 14`.

## Help text

`rune serve --help` lists, beside the existing flags:

```text
      --allow stringArray               expose only these tasks to agents (comma-separated, repeatable)
      --deny stringArray                hide these tasks from agents (comma-separated, repeatable)
```

The exact type column comes from the flag library. The `Long` text gains a paragraph: `--allow`
and `--deny` choose which tasks are tools; a task in both is hidden; the dependencies and hooks of
an exposed task still run; `[confirm]` tasks still need `--allow-destructive`. `Example` gains:

```text
  rune mcp --allow build,test                         # expose only 'build' and 'test'
  rune mcp --deny deploy                              # expose everything except 'deploy'
```

## Unchanged

- Root `--help` and the "Global flags" section of `docs/cli.md`.
- `--allow-destructive`: its values, errors, warning, and effect.
- `rune <task>`, `rune --list`, and every command other than `serve` / `mcp`.
- Every other `serve` flag and its validation.
