# CLI Reference

> The complete command-line surface — every flag, subcommand, and exit code. New here? Start
> with [Getting started](getting-started.md); for the file format see the
> [language guide](runefile.md).

```text
rune [global flags] [VAR=VALUE ...] [TASK [ARGS...]] ...
```

Rune discovers a `Runefile` (or `.runefile`) by walking up from the current directory,
then parses and statically validates it before running anything. You can chain multiple
tasks and interleave `VAR=VALUE` variable overrides between them.

## Running tasks

```sh
rune                 # show the version + available tasks (runs nothing)
rune build           # run the `build` task
rune build test      # run `build`, then `test`
rune greet Ada       # run `greet` passing "Ada" as its first argument
rune build target=release   # override the variable `target` for this run
```

- Positional `VAR=VALUE` tokens override variables defined in the Runefile. They may be
  placed before or between task names.
- Arguments after a task name are bound to that task's parameters.
- Dependencies run before their dependent task, each at most once per invocation.

### Validating without running

There is **no separate `check` subcommand** — Rune always parses and statically validates a
Runefile before running anything. To validate *without* running, use a run-nothing flag:

```sh
rune --list            # validate + list tasks (no task argument needed)
rune --dry-run build   # validate + show the resolved plan for `build`
rune --dump            # validate + print the parsed Runefile
```

A clean exit `0` means the Runefile is valid; exit `3` means a static error (with a
`file:line:col` span — see [Troubleshooting](troubleshooting.md)).

## Global flags

| Flag | Description |
|------|-------------|
| `-f`, `--file <path>` | Use a specific Runefile instead of upward discovery. |
| `--list` | List non-private tasks with their docs, then exit (runs nothing). |
| `--dry-run` | Print the resolved execution plan, then exit (runs nothing). |
| `--summary` | Print the task names that would run, one per line. |
| `--dump` | Emit the parsed Runefile as canonical text (or JSON with `--format json`). |
| `--format <fmt>` | Output format for `--dump` (currently `json`). |
| `--set <NAME=VALUE>` | Override a variable. The positional `NAME=VALUE` form (above) is the primary mechanism. |
| `--watch` | Re-run the requested task(s) when files change. |
| `--choose` | Open the interactive task picker (requires a terminal). |
| `--yes` | Approve `[confirm]` tasks without prompting. Required when stdin is not a terminal: piped or redirected input never approves. |
| `--quiet` | Suppress command echo. |
| `--fmt` | Rewrite the Runefile in canonical formatting. |
| `--clear-cache` | Remove the project-local `.rune/cache` directory. |
| `--ignore-version` | Bypass the Runefile's `minimum_version` check for this run, printing a warning. Cannot be enabled from a Runefile. |
| `--self-upgrade` | Upgrade this `rune` to the latest stable release, after verifying its signature and checksum. Runs no task and reads no Runefile. Cannot be combined with other flags or task arguments, except `--color`. See [Upgrading](installation.md#upgrading). |
| `--color <when>` | When to colorize output: `auto` (default; color only on a terminal), `always` (force color, even through a pipe), or `never`. Under `auto`, `NO_COLOR` disables color; an explicit `always`/`never` takes precedence over `NO_COLOR`. |
| `--version` | Print version and commit. |
| `-h`, `--help` | Show help. |

### Examples

```sh
rune --list                      # what can I run here?
rune --dry-run deploy            # what would `deploy` do?
rune --summary deploy            # just the task names, for scripting
rune --dump --format json        # machine-readable parse of the Runefile
rune -f ci/Runefile test         # use an explicit file
rune --fmt                       # canonically reformat the Runefile in place
rune --watch test                # re-run tests on file changes
rune --yes clean                 # run a [confirm] task without prompting
rune --clear-cache               # drop the [cache] fingerprints
rune --self-upgrade              # upgrade rune itself to the latest stable release
```

### Interactive task picker

`rune --choose` opens a full-screen, styled picker that lists the non-private
tasks in your Runefile. Move the highlight with the arrow keys (or `j`/`k`), type
to filter by task name or description, read the highlighted task's documentation
in the detail pane, and press `Enter` to run it; press `q` or `Ctrl-C` to cancel
without running anything.

If your Runefile assigns tasks to groups with `[group("...")]`, the picker
organizes them into the same labeled sections `rune --list` shows, so a large
task set stays easy to scan; Runefiles with no groups render as a plain list,
just as before.

The picker is opt-in and interactive-only: bare `rune` never opens it, and it
activates only when standard input and output are a terminal. In a pipe,
redirect, or CI environment, `--choose` reports
`--choose requires an interactive terminal` (exit `2`) rather than emitting UI.
On selection the picker exits and hands the terminal to the task, so output,
colors, and `Ctrl-C` behave exactly as a direct `rune <task>` run. Set
`NO_COLOR` to render the picker without color.

## Subcommands

Built-in commands take precedence over a task of the same name. A task whose name
collides with a built-in stays reachable with the `--` separator — `rune -- serve` runs a
task named `serve` rather than the server. Run `rune <command> --help` for full details on
any command.

| Command | Description |
|---------|-------------|
| `rune serve [--http] [--addr <addr>] [--token-file <file>] [--allow-destructive <task>[,<task>...]] [--allow <task>[,<task>...]] [--deny <task>[,<task>...]]` | Start the MCP server. Stdio by default; `--http` enables the Streamable HTTP transport (which requires `--token-file`), and `--addr` sets the bind address. `--allow-destructive` lets agents run the named `[confirm]` tasks, comma-separated or with the flag repeated; no option allows all of them. `--allow` exposes only the named tasks to agents and `--deny` hides the named tasks, each comma-separated or repeated; a task named in both is hidden, and the dependencies and hooks of an exposed task still run. |
| `rune mcp` | Alias for `rune serve` over **stdio** (for local agents/IDEs). |
| `rune analyze [path] [--json]` | Statically analyze a Runefile (and its transitive imports) and print diagnostics — **without running anything**. Exit `0` (no errors), `3` (error diagnostics), or `2` (no Runefile / unreadable). `--json` emits machine-readable output. See [Diagnostics](diagnostics.md). |
| `rune lsp [--log-file <path>] [--log-level <level>]` | Start the language server (JSON-RPC / LSP 3.17 over stdio) for editors: live diagnostics, completion, go-to-definition, hover, document symbols, and formatting. stdout is protocol-only; logs go to stderr or `--log-file`. See [editor setup](../editors/README.md). |
| `rune version` | Print the version (first line identical to `--version`) plus the Runefile language version. Add `--check` to report whether the installed binary satisfies the Runefile's `minimum_version`, and `--json` for machine-readable output. |
| `rune completion <bash\|zsh\|fish\|powershell>` | Print a shell-completion script for the given shell (a shell argument is required; an unsupported shell is an error). Completions include your Runefile's task names; run `rune completion --help` for install steps. |
| `rune help [command]` | Help about any command. |

See the [MCP guide](mcp.md) for exposing tasks to AI agents, [editor setup](../editors/README.md) for using `rune lsp`, and [Diagnostics](diagnostics.md) for the stable `RUNE####` codes.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | All requested tasks succeeded, or `--self-upgrade` upgraded or found nothing to upgrade. |
| *N* | A task body failed with exit code *N*. The body can belong to the task you named, a dependency, or a later task on the command line; it is the failure the error message names. |
| `1` | A task failed without a body exit code: a `[confirm]` prompt was declined or had no terminal, the interpreter is missing, or an `agent` task's CLI is missing or failed. Also `--self-upgrade` when it could not upgrade. |
| `2` | Usage error: no Runefile found, unknown task, bad arguments, or an `agent` task with no `agent_cmd` set. Also `--self-upgrade` combined with another flag or argument. |
| `3` | Static validation error (parse/analyze). Nothing was executed. |
| `130` | Interrupted (SIGINT / Ctrl-C). |

Rune passes a body's code through unchanged, even when it equals one of Rune's own codes (`2`,
`3`, `130`). The message on stderr tells the two apart:

```text
rune: task "gate" failed at "exit 2": exit status 2   <- the body exited 2
rune: unknown task: gaet                              <- Rune's usage error, also 2
```

A body killed by signal *S* exits `128 + S` on Linux and macOS (for example `143` for SIGTERM).
Shell bodies report codes 0 to 255 on every OS; interpreter bodies (`python`, `node`, custom) on
Windows pass through the full code. `||` failure hooks never change the exit code, and the
`RUNE_FAILED_EXIT_CODE` they receive always equals it.

Rune's own messages go to **stderr**, so a task's `stdout` stays clean for piping.

## Output & color

Color is emitted on a TTY unless `NO_COLOR` is set or output is redirected, and
`--color always|never` forces the decision either way. The flag works on every
command (`rune analyze --color=always` and `rune --color=always <task>` are both
valid), and the decision is made per stream: with stdout piped but stderr on a
terminal, status lines stay colored while the piped data stays plain.

Every human-facing message shares one restrained palette:

- **Errors** — the failure banner (`rune: …`), watch-mode failures, and
  diagnostic severities render in the shared error style; warnings (cache
  writes, `--ignore-version`) in the shared warning style.
- **Status** — `running:`, `formatted:`, and `cleared:` labels are green;
  command echo, cache/dry-run notices, watch and server chrome are dimmed;
  `[confirm]` prompts use the warning style.
- **Diagnostics** — validation errors are rendered with a dimmed
  `file:line:col` locator and a caret-underlined source span; `rune analyze`
  presents the same rendering (plus its `error[RUNE1234]` code tokens).
- **Help** — `--help` on the root command and every subcommand shares the same
  grouped, heading-styled layout.

Machine-readable output — `--dump`, `analyze --json`, `version --check --json`,
completion scripts, and everything an MCP agent or the language server receives
— never contains color escapes, regardless of `--color`. When color is off,
output is byte-for-byte plain text.

## See also

- [Runefile language guide](runefile.md) — the file format these commands run.
- [Guides](how-to/README.md) — task-oriented deep dives per capability.
- [Troubleshooting](troubleshooting.md) — what each error and exit code means.
