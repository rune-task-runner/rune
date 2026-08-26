# Diagnostics & the `RUNE####` codes

Rune analyzes a Runefile — parsing, resolving imports, and running the semantic
analyzer — **before it runs anything**. Every statically detectable problem is
reported with a `file:line:col` location, a caret-underlined span, and a stable
`RUNE####` code. The same analysis powers three surfaces:

- **execution** — `rune <task>` refuses to run a Runefile with error diagnostics;
- **[`rune analyze`](cli.md)** — reports diagnostics on demand, for CI or scripts;
- **the [language server](../editors/README.md)** (`rune lsp`) — publishes them live as you type.

Because all three share one analysis service, they report identical diagnostics.

## `rune analyze`

```sh
rune analyze                 # analyze the discovered Runefile
rune analyze path/to/Runefile
rune analyze --json          # machine-readable output
```

Human output is one line per diagnostic plus a summary:

```text
Runefile:8:13: error[RUNE2001]: unknown task: missing
1 error, 0 warnings
```

Exit codes: `0` (no error diagnostics), `3` (error diagnostics present), `2`
(no Runefile found or it could not be read). Warnings never change the exit
code on their own.

## The codes

The codes are a **stable contract**: each condition maps to exactly one code,
and a code's meaning never changes once shipped. You can rely on them in output
filtering and CI.

### Parser (RUNE1xxx) — errors

| Code | Condition |
|------|-----------|
| `RUNE1001` | Unexpected token |
| `RUNE1002` | Invalid indentation |
| `RUNE1003` | Unterminated string |
| `RUNE1004` | Incomplete expression |
| `RUNE1005` | Malformed task declaration |

### Semantic (RUNE2xxx) — errors, except `RUNE2010` (warning)

| Code | Condition |
|------|-----------|
| `RUNE2001` | Unknown dependency |
| `RUNE2002` | Duplicate task |
| `RUNE2003` | Dependency cycle (lists every task in the cycle) |
| `RUNE2004` | Undefined variable |
| `RUNE2005` | Wrong argument count |
| `RUNE2006` | Duplicate parameter |
| `RUNE2007` | Invalid attribute |
| `RUNE2008` | Invalid setting |
| `RUNE2009` | Invalid executor *(reserved; executors are open-ended custom interpreters)* |
| `RUNE2010` | Public task lacks documentation — **warning**, never gates execution or exit code |
| `RUNE2011` | Invalid failure hook (a `\|\|` hook targets — or reaches through its dependencies — an `agent`-executor task) |

### Project (RUNE3xxx) — errors

| Code | Condition |
|------|-----------|
| `RUNE3001` | Unresolved import |
| `RUNE3002` | Import cycle |
| `RUNE3003` | Duplicate imported namespace |
| `RUNE3004` | Incompatible Rune version |

## Notes

- `RUNE2008` (invalid setting) and `RUNE2010` (undocumented public task) are
  surfaced by `rune analyze` and the language server. They are **not** raised on
  the execution path, so an existing Runefile keeps running — the editor still
  flags them.
- Dependency- and import-cycle diagnostics attach every participating task/file
  as a related location, which editors show as clickable jumps.

## See also

- [CLI reference](cli.md) — `rune analyze` and `rune lsp` flags and exit codes.
- [Editor setup](../editors/README.md) — using `rune lsp` in VS Code, Neovim, Helix, and Zed.
- [Troubleshooting](troubleshooting.md) — resolving common errors.
