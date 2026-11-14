# Contract: command line and stderr

## Flag

`rune serve` and `rune mcp` accept `--no-reload` (boolean, default off).

- Off: the server watches the Runefile graph and reloads (this feature).
- On: no file is watched. Behavior, startup output and capabilities are those of the previous
  release.

Help text: `--no-reload   keep the tasks read at start; do not reload when the Runefile changes`

The startup line is unchanged in both cases.

## Lines on stderr

All are written to stderr, never stdout. Warnings use the `warning` label styled as existing
warnings; the two reload lines are muted like the startup line.

Success, after the tools are replaced:

```text
reloaded Runefile: 1 tool added, 0 removed, 0 changed
```

The first count is followed by `tool` when it is 1 and `tools` otherwise. A reload that changes
no tool prints the line with three zeros.

Failure, after the diagnostics a start would print:

```text
reload failed: 1 static error(s); keeping the previous tools
```

The reason is the load error's text: `N static error(s)`, `invalid settings`, the minimum
version message, or the file read error.

Warnings on a successful reload, one per name, before the success line:

```text
warning: --allow lint has no effect: the Runefile has no task "lint"
warning: --deny lint has no effect: the Runefile has no task "lint"
warning: --allow-destructive clean has no effect: the Runefile has no task "clean"
```

plus the existing start warnings for a grant on a task without `[confirm]` and for a filter
naming a task that is never exposed, with their existing text.

Watching unavailable, once:

```text
warning: cannot watch the Runefile for changes: <error>; tools will not reload
```

## Exit status

A reload never changes the exit status. A start on an invalid Runefile still exits 3.
