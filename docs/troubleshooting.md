# Troubleshooting

> What Rune does when something is wrong, and how to fix it. Most mistakes are caught
> **before any task runs** (Principle: *errors are a feature*), with a precise
> `file:line:column` and a caret-underlined span. New here? See [Getting started](getting-started.md).

## Exit codes at a glance

| Code | Meaning |
|------|---------|
| `0` | All requested tasks succeeded. |
| *N* | A task body failed with exit code *N*; Rune exits with that same code. |
| `1` | A task failed without a body exit code (for example a declined `[confirm]` or a missing interpreter). |
| `2` | Usage error — no Runefile found, unknown task at the command line, or bad arguments. |
| `3` | Validation error (parse/analyze, or a task not available on this OS) — **nothing executed**. |
| `130` | Interrupted (SIGINT / Ctrl-C). |

Static errors (`3`) are detected up front, so a broken Runefile never leaves your project
half-built. See the [CLI reference](cli.md#exit-codes).

## Common errors

### "unknown task" (as a dependency) — exit 3

A task depends on a name that doesn't exist. Caught during analysis; nothing runs.

```text
Runefile:1:4: error: unknown task: missing
1 | a: missing
  |    ^^^^^^^
```

**Fix:** define the task, or correct the typo in the dependency list.

### "unknown task" (at the command line) — exit 2

You asked to run a task that isn't defined.

```text
rune: unknown task: nope
```

**Fix:** run `rune --list` to see the available tasks.

### "undefined variable" — exit 3

An interpolation or expression references a name that was never defined.

```text
Runefile:2:11: error: undefined variable: nope
2 |     @echo {{nope}}
  |           ^^^^^^^^
```

**Fix:** define it with `name := "value"`, pass it as a parameter, or override it on the
command line (`rune task NAME=value`). See [Settings & dotenv](how-to/settings-and-dotenv.md).

### "dependency cycle" — exit 3

Tasks depend on each other in a loop. Rune reports the full cycle path.

```text
Runefile:3:1: error: dependency cycle: a → b → a
3 | b: a
  | ^
```

**Fix:** break the loop — extract the shared work into a third task both can depend on. See
[Dependencies & post-hooks](how-to/dependencies-and-hooks.md).

### Argument count mismatch (arity) — exit 3

A task (or a dependency) is called with the wrong number of arguments.

```text
Runefile:3:7: error: task "greet" expects at least 1 argument(s), got 0
3 | main: greet
  |       ^^^^^
```

**Fix:** pass the required argument — e.g. `(greet "Ada")` in the dependency, or give the
parameter a default. See [Parameters](how-to/parameters.md).

### "not available on ..." — exit 3

The task you invoked is restricted to another OS by a `[linux]`/`[macos]`/`[windows]`/
`[unix]` attribute. Unlike the other exit-3 errors this one is host-dependent: the same
Runefile runs fine on a matching platform.

```text
Runefile:4:1: error: task "setup-win" is not available on macos (requires windows)
4 | [windows]
  | ^^^^^^^^^
```

**Fix:** run the task on a platform it names, widen the attribute (e.g. `[unix]`, or
`[linux, windows]`), or call it via a cross-platform dispatcher task — as a dependency it
is skipped silently instead of failing. See [OS filtering](how-to/os-filtering.md).

### "no Runefile found" — exit 2

Rune walks up from the current directory looking for a `Runefile`; none was found.

```text
rune: no Runefile found in this directory or any parent
```

**Fix:** run from inside your project, or point at a file with `rune --file path/to/Runefile`.

**In an agent client's log:** the client started `rune mcp` outside your project. Name the
Runefile in the client's configuration, with an absolute path:
`rune mcp -f /path/to/project/Runefile`. See
[Starting the server](mcp.md#starting-the-server).

### A task body failed

A command in the body exited non-zero. Rune stops at the first failure, names the task, the
failing line, and the exit status, and exits with that same code (`7` here).

```text
rune: task "boom" failed at "exit 7": exit status 7
```

A failure with no exit status, such as a declined `[confirm]`, exits `1`. A body can also exit
`2`, `3`, or `130`, the codes Rune uses for its own errors; the message above tells them apart.
See [Exit codes](cli.md#exit-codes).

**Fix:** address the failing command. Prefix a line with `-` to deliberately ignore its error.

### Missing interpreter

A task with a `(python)`, `(node)`, or custom executor needs that runtime on `PATH`. If it's
absent, the task fails with an actionable error naming the missing interpreter (exit `1`).

**Fix:** install the interpreter, or run in an environment that has it. Note the
[container image](docker.md) ships only the static binary — see its caveat. See
[Executors](how-to/executors.md).

### "audit log: this call was not run" (in an agent's tool result)

An MCP server writes the start of every tool call to the [audit log](mcp.md#the-audit-log)
before the call runs. When it cannot, the call is refused and nothing runs. The message
names the reason and the log directory, and the server prints it on standard error too.

**Fix:** make the directory writable, or free the disk it is on. To keep the log somewhere
else, start the server with `RUNE_AUDIT_DIR` set to a directory outside the project.
`rune log --dir` prints the directory in use.

### "audit log: ... is inside the project" (when the server starts, exit 2)

`RUNE_AUDIT_DIR`, or the state directory of your user, is inside the Runefile's directory
or the tasks' working directory. The log must be where a task does not normally write.

**Fix:** set `RUNE_AUDIT_DIR` to a directory outside the project.

### "the sandbox is unavailable" (in an agent's tool result, and when the server starts)

The tasks an agent calls run in [a sandbox](mcp.md#the-sandbox). On Linux it needs
[bubblewrap](https://github.com/containers/bubblewrap). When the sandbox cannot work, the
server starts, prints a warning, and refuses every task call before anything runs. The
message names the reason.

**Fix:** by reason:

- *bubblewrap (bwrap) was not found on the PATH*: install it, for example
  `apt install bubblewrap`. The next call uses it, with no restart.
- *bubblewrap could not start a sandbox*: the host does not let bubblewrap create
  namespaces. A default container is one such host. Allow unprivileged user namespaces
  for it, or run the server where they are allowed.
- *this system has no sandbox backend*: macOS and Windows have none.
- *the PATH holds ..., which the sandbox hides*: a directory of the server's `PATH` is
  under `/run`, `/tmp` or `/var/tmp`, which a task does not see. Start the server with a
  `PATH` that does not need it.

To run tasks without a sandbox, start the server with `--sandbox off`, or set
`RUNE_SANDBOX=off`. The server then says so when it starts and in every record of the
audit log.

### "Read-only file system" or "Could not resolve host" in a task an agent ran

The task ran in [the sandbox](mcp.md#the-sandbox): it writes only inside the project, and
it has no network unless it is marked `[network]`. The output ends with a line that says
so: `note: task "build" ran in the sandbox: ...`.

**Fix:** mark a task that needs the network with `[network]`. For a tool that writes
under your home directory, point it into the project with `[env]`; a tool that follows
`XDG_CACHE_HOME` already has a private cache directory. A task that writes `.git`, such as
one that commits, is a task to run yourself.

### "the Runefile looks for it and it does not exist" (in an agent's tool result)

The Runefile names a file inside the project that is not there: an optional import
(`import? "local.rune"`), or a module that lives in a directory and is declared without
its path (`mod api`, found at `api/Runefile` after `api.rune` was looked for). A task
could create the missing file, and the next load would read it, so the call is refused.

**Fix:** create the file, give the module its path (`mod api "api/Runefile"`), or remove
the optional import.

### "holds more than one Runefile" (exit 2)

The directory has two files that are both a Runefile name, such as `Runefile` and
`.runefile`. Rune does not pick one: a file added beside the Runefile would otherwise
replace it in silence.

**Fix:** remove all but one, or name the one you mean with `--file`.

### `rune log` shows the log, not my `log` task

`log` is a built-in command. **Fix:** run the task with `rune -- log`.

### `rune agent` prints help, not my `agent` task

`agent` is a built-in command: `rune agent init` sets an agent client up. **Fix:** run the
task with `rune -- agent`.

### A cached task ran when you expected a skip

`[cache]` re-runs (rather than falsely skipping) when a declared **output is missing** or an
**input can't be resolved** — by design. Clear stale fingerprints with `rune --clear-cache`.
See [Caching](how-to/caching.md).

## Still stuck?

- `rune --dry-run <task>` shows the resolved plan without running anything.
- `rune --dump --format json` prints the parsed Runefile for inspection.
- Check the [CLI reference](cli.md) and the [language guide](runefile.md).
