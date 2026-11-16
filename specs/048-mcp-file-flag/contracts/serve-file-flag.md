# Contract: `rune serve --file`

## Synopsis

```text
rune serve [-f PATH | --file PATH] [other serve flags]
rune mcp   [-f PATH | --file PATH] [other serve flags]
rune [-f PATH | --file PATH] serve|mcp [other serve flags]
```

| Flag | Value | Default | Meaning |
|---|---|---|---|
| `-f`, `--file` | a path | none | Serve this Runefile instead of the one found from the working directory |

The usage string shown in `rune serve --help`:

```text
  -f, --file string   serve this Runefile instead of the one found from the working directory
```

## Behavior

1. With the flag, the server serves the named Runefile and looks nowhere else.
2. A relative path is read from the directory the server was started in.
3. Tasks, the `[context]` task included, run in the directory of the named Runefile.
   Imports, modules and the `set dotenv` file are found from there.
4. Reloading follows the named Runefile and the files it imports or loads.
5. The flag may stand after the command name or before it. Given more than once, the last
   value is used. The server's other flags stand after the command name, as before.
6. Without the flag, the server behaves as it did before, and prints what it printed
   before.

## Errors

Each is a usage error: one line on stderr starting with `rune: `, exit code 2, no task
run, no Runefile code run.

| Command line | Message |
|---|---|
| `rune mcp -f ""`, `rune mcp --file=` | `rune: --file needs a path` |
| `rune mcp -f` | `rune: flag needs an argument: 'f' in -f` |
| `rune mcp -f /no/such/Runefile` | the operating system's message, with the path in it, such as `rune: stat /no/such/Runefile: no such file or directory` |
| `rune mcp -f /a/directory` | `rune: --file points at a directory, not a Runefile` |
| `rune mcp --yes`, `rune --yes mcp`, with or without `-f` | `rune: unknown flag: --yes` |

A named Runefile with a static error gives the diagnostics and the exit code it gives when
served from its own directory.

## What the contract does not include

- `--yes`, `--quiet`, `--set`, `--watch`, `--list`, `--dry-run`, `--summary`, `--dump`,
  `--format`, `--choose`, `--fmt`, `--clear-cache` and `--self-upgrade` are not flags of
  `rune serve`.
- `rune analyze`, `rune lsp`, `rune version` and `rune completion` have no `--file` flag.
- `rune -f ""` on the command line, with a task or with `--list`, is unchanged.

## Compatibility

Every command line that was accepted before is accepted now with the same result. The
command lines that change are those that were usage errors only because the flag was
unknown.
