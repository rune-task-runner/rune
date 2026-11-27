# CLI Reference

> The complete command-line surface — every flag, subcommand, and exit code. New here? Start
> with [Getting started](getting-started.md); for the file format see the
> [language guide](runefile.md).

```text
rune [global flags] [VAR=VALUE ...] [TASK [ARGS...]] ...
```

Rune discovers a `Runefile` (or `.runefile`) by walking up from the current directory,
then parses and statically validates it before running anything. You can chain multiple
tasks and interleave `VAR=VALUE` variable overrides between them. A directory that holds more than one of these names is an error:
Rune does not pick one, and `--file` names the one you mean.

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
| `--reuse-key <file>` | Skip a `[reuse]` task when a receipt signed by a public key in the file shows it passed, recently, on exactly these files, with the same arguments, Rune, OS and architecture. Repeatable. Only these keys are trusted. Needs `set receipts` and a git repository (else exit `2`). See [Reuse evidence in CI](how-to/receipts.md#reuse-evidence-in-ci). |
| `--reuse-max-age <duration>` | The oldest receipt `--reuse-key` may reuse, such as `72h`. Default `24h`. Only with `--reuse-key`. |
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
task named `serve` rather than the server. `log` is such a name: `rune log` shows the
audit log, with a note when the Runefile has a task named `log`, and `rune -- log` runs
that task. `agent` is another: `rune agent` is the command, and `rune -- agent` runs a task
named `agent`. So is `export`: `rune export` is the command, and `rune -- export` runs a
task named `export`. And `suggest`: `rune suggest` is the command, and `rune -- suggest`
runs a task named `suggest`. And `ci`: `rune ci` is the command, and `rune -- ci` runs a task
named `ci`. And `verify` and `receipt`: `rune verify` and `rune receipt` are commands, and
`rune -- verify` and `rune -- receipt` run tasks of those names. Run `rune <command> --help`
for full details on any command.

| Command | Description |
|---------|-------------|
| `rune serve [-f <path>] [--http] [--addr <addr>] [--token-file <file>] [--allow-destructive <task>[,<task>...]] [--allow <task>[,<task>...]] [--deny <task>[,<task>...]] [--output-limit <size>] [--no-reload] [--sandbox <mode>]` | Start the MCP server. `-f`/`--file` names the Runefile to serve, for a client that starts the server outside the project; tasks run in that Runefile's directory, and `--yes` is still not a server flag. Stdio by default; `--http` enables the Streamable HTTP transport (which requires `--token-file`), and `--addr` sets the bind address (default `127.0.0.1:7777`). The transport has no TLS: on an address that is not loopback the token is sent in cleartext, and the server prints a warning when it starts. `--allow-destructive` lets agents run the named `[confirm]` tasks, comma-separated or with the flag repeated; no option allows all of them. `--allow` exposes only the named tasks to agents and `--deny` hides the named tasks, each comma-separated or repeated; a task named in both is hidden, and the dependencies and hooks of an exposed task still run. `--output-limit` sets how many bytes of stdout and of stderr a tool result carries (default `64KiB`; bytes, or a `KiB` or `MiB` count, from `1KiB` to `1024MiB`); longer output keeps its beginning and end around a `[truncated N bytes]` line. A result holds the output as text and again as the structured fields `exitCode`, `stdout`, `stderr` and `fixSuggestion`, each stream under the same limit. The server reloads its tools when the Runefile or a file it imports changes, and keeps the previous tools if the change is invalid; `--no-reload` keeps the tasks read at start. `--sandbox` is `enforce` (the default) or `off`: the tasks agents call run in [a sandbox](mcp.md#the-sandbox), with no network unless a task is marked `[network]` and with writes only inside the project; where no sandbox can work every call is refused, and `off` runs tasks unconfined and says so at start and in the audit log. |
| `rune mcp` | Alias for `rune serve` over **stdio** (for local agents/IDEs). |
| `rune log [-f <path>] [--date <day>] [--since <when>] [--until <when>] [--tz <zone>] [--task <task>[,<task>...]] [--outcome <outcome>[,<outcome>...]] [--client <name>[,<name>...]] [--session <id>[,<id>...]] [--all] [--json] [--dir]` | Show what agents did through the MCP server, from the [audit log](mcp.md#the-audit-log) of this project: each tool call with its time, outcome, task, arguments, exit code, duration and client. Nothing is executed. With no flags it lists today's actions; `--date` picks a day, and `--since` and `--until` a range, each a day, an RFC 3339 time, or a duration ago such as `2h` or `7d`. Days are read in the display time zone, the machine's unless `--tz` names an IANA zone, `UTC` or `local`. `--task`, `--outcome`, `--client` and `--session` filter, comma-separated or repeated. `--all` adds the requests that are not tool calls, `--json` prints one JSON object per action, and `--dir` prints the directory of the log files. `-f` reads the log of a Runefile a server was given with `rune mcp -f`. Exit `0` (actions listed, or none), `1` (the log cannot be read), or `2` (usage error or no Runefile). |
| `rune import [source] [--from <format>] [-o <path>] [--dry-run] [--wrap] [--force]` | Write a Runefile from the task file of another runner: a justfile, a Makefile, a Taskfile (version 3) or the scripts of a `package.json`. The source is only read: nothing is run or fetched, and the source is never changed. `source` is a file or a directory to look in, the working directory by default; the format comes from the file name, or from `--from` (`justfile`, `make`, `task` or `npm`). Each task is reported on standard error with its source line as converted, review or unsupported, and an unsupported task stays in the Runefile as a task that fails with the reason. The Runefile goes next to the source, or to `--output`; an existing file is never replaced without `--force`. `--dry-run` prints the Runefile to standard output and writes nothing. `--wrap` turns each unsupported task into a wrapper that runs the original runner. Exit `0` (nothing is unsupported), `1` (something is; the Runefile was still written), `2` (usage error), or `3` (the source cannot be parsed). This is not the `import` statement of a Runefile. See [Import from another runner](how-to/import-from-other-runners.md). |
| `rune agent init <client> [-f <path>] [--unpinned] [--force] [--dry-run] [--check]` | Write the project files that set an agent client up to work through Rune: `claude` (`.mcp.json` and `.claude/settings.json`), `codex` (`.codex/config.toml`) or `vscode` (`.vscode/mcp.json`, `.mcp.json` and `.github/agents/rune.agent.md`). The files register `rune mcp` for this project, remove the client's own tools that run commands, and let Rune's tools run. Only files inside the project are written; other servers, other settings, comments and existing deny rules are kept, and no task runs. A value that is already there with another value stops the run with its file and line, and `--force` replaces it; a deny rule is never removed. The report on standard error ends with one of three outcomes, "Rune-only", "Rune-only within" a custom agent, or "Registered only", and with the steps left to you. "Rune-only" is said only for a client version that was checked. `-f`/`--file` names the Runefile of the project. `--unpinned` registers the server without the Runefile's path, for files you commit. `--dry-run` prints the difference to standard output and writes nothing. `--check` says whether the setup is in the project's files; it writes nothing and starts no program. Exit `0` ("Rune-only", or a check that found the setup complete), `1` ("Registered only", a check that did not, or a file that could not be read or written), `2` (usage error or a conflict), or `3` (the Runefile or a client file is not valid). See [Set up an agent client](how-to/agent-setup.md). |
| `rune export <format> [-f <path>] [--write] [-o <path>] [--check]` | Write guidance for agents from the Runefile, for an agent that has no MCP integration: `agents-md` is one Markdown section for `AGENTS.md`, and `claude-skill` is a Claude skill (`SKILL.md`, and `tasks.md` above 30 tasks). The text lists every public task with its description, its command line, its parameters with types, defaults and enum values, the operating systems it runs on, and what its author declared with `[confirm]`, `[agent-allow]`, `[plan]`, `[applies]`, `[readonly]` and `[network]`. Nothing of the Runefile runs or is evaluated, and the environment is not read. The export is printed on standard output. `--write` writes it into the project: `AGENTS.md`, or `.claude/skills/rune-tasks/`. `-o`/`--output` writes it to another file or skill directory. In a Markdown file only the lines from `rune:begin tasks` to `rune:end tasks` are replaced, and a file without them gains the section at its end; a skill file that Rune did not generate is never replaced. `--check` says whether the export there is what Rune would write now and writes nothing. The text is guidance: it does not limit what a shell can run. `-f`/`--file` names the Runefile. Exit `0` (printed, written, or current), `1` (a check found the export missing or stale, or a file could not be read or written), `2` (usage error, no Runefile, or a file Rune will not change), or `3` (the Runefile is not valid). See [Export instructions for agents](how-to/agent-instructions.md). |
| `rune agent observe <client> [-f <path>] [--remove] [--dry-run]` | Record the shell commands Claude Code runs in this project, for `rune suggest`. Only `claude` is supported. Adds three background hook handlers that run `rune agent record` to `.claude/settings.local.json`, keeping every other hook, setting and permission rule; `--remove` takes exactly those handlers out. No Runefile is needed. What is recorded stays outside the project, for up to 30 days: the command with its secrets removed, its directory, the time, the session and the outcome, never the output. `--dry-run` prints the difference and writes nothing. Exit `0`, `1` (a file could not be read or written), `2` (usage error or a value in the way), or `3` (a settings file is not valid JSON). See [Learn tasks from your agent's commands](how-to/learn-tasks-from-agent.md). |
| `rune suggest [-f <path>] [--min-runs <n>]` | List the shell commands an agent repeated in this project, as `rune agent observe` recorded them, each with an ID and the task Rune would add. A command is listed when it ran at least `--min-runs` times (default 3); a command that is already a task, or that runs `rune`, is left out; commands that differ in one plain word become one task with a parameter; a command Rune cannot write as a task safely is listed for review with the reason. Nothing is run. Exit `0`, `1` (the records could not be read), `2` (usage error), or `3` (the Runefile is not valid). |
| `rune suggest apply <id>... [-f <path>] [--min-runs <n>] [--dry-run]` | Add the tasks of the suggestions with these IDs at the end of the Runefile, or create it. Every byte already in the Runefile stays; the new Runefile must pass analysis, and it is written in one step. An unknown ID or a Runefile that changed meanwhile writes nothing. `--dry-run` prints the difference. Exit `0`, `1` (the Runefile could not be written), `2` (usage error, unknown ID, or the Runefile changed), or `3` (the Runefile is not valid). |
| `rune suggest forget [-f <path>]` | Delete the commands recorded for this project. |
| `rune ci github <task> [args]... [-f <path>] [--os linux\|macos]... [--rune-version <v>] [--branch <name>] [--write] [-o <path>] [--check]` | Write a GitHub Actions workflow for the root tasks you name: one job per task and arguments they reach, prerequisites as `needs:` in the order a local run keeps (a dependency of a task without `[parallel]` waits for the ones listed before it), the pinned Rune release installed and checked against its published checksums, declared `[cache]` outputs passed to later jobs, and exact cache keys from Rune's fingerprint. The workflow gets `contents: read` and no secret; `[confirm]`, `[applies]` and `agent` tasks, and anything a job could not run as a local run would, are refused with the reason. Nothing runs and the environment is not read. Prints the workflow; `--write` writes `.github/workflows/rune.yml` next to the Runefile, `-o` another file; only a file Rune generated is replaced. The jobs find the Runefile in its directory by name, so `-f` must name a file Rune would find there. `--check` reports `current`, `stale`, `missing` or `not generated by rune ci github`. Exit `0`, `1` (a check found the file missing or stale, or a file could not be written), `2` (usage error or a file Rune will not replace), or `3` (the Runefile is not valid or its tasks cannot run as a workflow). See [Generate a GitHub Actions workflow](how-to/ci-github.md). |
| `rune ci run [-f <path>] [--dir <dir>] [--key] [--reuse-key <file>]... [--reuse-max-age <duration>] [--] <task> [args]` | Run the body of exactly one task with these arguments, as `rune <task>` runs it, without its dependencies and post-hooks; its failure hooks run when it fails. What each job of a generated workflow runs. `--dir` unpacks the bundles of earlier jobs before the body and writes the task's own after a successful `[cache]` body; `--key` prints the cache key of a `[cache]` task and runs nothing; `--reuse-key` and `--reuse-max-age` skip a `[reuse]` task as on the command line. Exits as `rune <task>` exits; `1` when a bundle cannot be read or written. |
| `rune verify <revision> <task>... [-f <path>] [--key <file>]... [--json]` | Report, for each task, whether a trusted signed receipt shows it passed on exactly the files of the revision, from `.rune/receipts/` and the revision's note under `refs/notes/rune-receipts`. A failed, interrupted, refused or cached run never counts, nor one whose files changed during the run or ran on other files. Trusted keys are the key this machine signs with and the public keys in each `--key` file. Also says whether the working tree still matches the revision. Runs nothing and writes nothing. `--json` prints one JSON document. Exit `0` (every task verified), `1` (one is not), or `2` (usage error: no task, unknown revision, no repository, unreadable key file). See [Receipts](how-to/receipts.md). |
| `rune receipt key` | Print the public key receipts are signed with, in PEM: `RUNE_RECEIPT_KEY` when it is set, else this machine's local key, created when there is none. The key ID and its source go to stderr. |
| `rune receipt attach [revision] [-f <path>] [--key <file>]...` | Add the receipts that ran on exactly the revision's files (default `HEAD`), and that a trusted key signed, to its note under `refs/notes/rune-receipts`, one per line, sorted, each once. The commit and other notes do not change. Exit `0`, `1` (no receipt is bound to the revision, or git could not write the note), or `2` (usage error). |
| `rune analyze [path] [--json]` | Statically analyze a Runefile (and its transitive imports) and print diagnostics — **without running anything**. Exit `0` (no errors), `3` (error diagnostics), or `2` (no Runefile / unreadable). `--json` emits machine-readable output. See [Diagnostics](diagnostics.md). |
| `rune lsp [--log-file <path>] [--log-level <level>]` | Start the language server (JSON-RPC / LSP 3.17 over stdio) for editors: live diagnostics, completion, go-to-definition, hover, document symbols, and formatting. stdout is protocol-only; logs go to stderr or `--log-file`. See [editor setup](../editors/README.md). |
| `rune version` | Print the version (first line identical to `--version`) plus the Runefile language version. Add `--check` to report whether the installed binary satisfies the Runefile's `minimum_version`, and `--json` for machine-readable output. |
| `rune completion <bash\|zsh\|fish\|powershell>` | Print a shell-completion script for the given shell (a shell argument is required; an unsupported shell is an error). Completions include your Runefile's task names; run `rune completion --help` for install steps. |
| `rune help [command]` | Help about any command. |

See the [MCP guide](mcp.md) for exposing tasks to AI agents, [editor setup](../editors/README.md) for using `rune lsp`, and [Diagnostics](diagnostics.md) for the stable `RUNE####` codes.

## Environment

| Variable | Read by | Meaning |
|----------|---------|---------|
| `NO_COLOR` | every command | Turn color off; see [Output & color](#output--color). |
| `RUNE_AUDIT_DIR` | `rune serve`, `rune mcp`, an `agent` task, `rune log` | The directory the [audit logs](mcp.md#where-the-log-is-stored) live under, in place of the state directory of your user. An absolute path. A server refuses a directory inside the project. |
| `RUNE_AUDIT_RETENTION_DAYS` | `rune serve`, `rune mcp`, an `agent` task | How many days a day of the audit log is kept. Default `90`; `0` keeps every file. |
| `RUNE_SANDBOX` | `rune serve`, `rune mcp`, an `agent` task | The mode of [the sandbox](mcp.md#the-sandbox) for the tasks agents call: `enforce` (the default) or `off`. `--sandbox` wins over it. Any other value is a usage error. A task a person runs is never confined, whatever the value. |
| `XDG_CACHE_HOME` | the same commands | The cache directory: the private directories of the sandbox are in `$XDG_CACHE_HOME/rune/sandbox`. |
| `XDG_STATE_HOME` | the same commands, a run with `set receipts`, `rune verify`, `rune receipt` | The state directory, when `RUNE_AUDIT_DIR` is not set: the logs are in `$XDG_STATE_HOME/rune/audit`, and the local [receipt](how-to/receipts.md) signing key in `$XDG_STATE_HOME/rune/receipts`. A relative path is ignored. |
| `RUNE_RECEIPT_KEY` | every `rune` | A PKCS#8 PEM Ed25519 private key that [receipts](how-to/receipts.md) are signed with, in place of the local key, for CI. Rune removes it from its environment when it starts, so no task inherits it. An invalid value stops a run with `set receipts` before any task. |

A task that an agent's tool call runs sees `RUNE_INVOCATION_ID`, the id of that call in the
audit log.

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
