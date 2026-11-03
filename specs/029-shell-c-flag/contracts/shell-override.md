# Contract: Running a Body Under `set shell` or `[script]`

**Feature**: [../spec.md](../spec.md) | **Data model**: [../data-model.md](../data-model.md)

This is the user-visible behavior after the change. Anything not listed here stays
as it is on `main`.

## 1. Which tasks are affected

A task is affected when its body runs through the interpreter executor with a
command from:

- `set shell := [...]`, for a task with no executor or `(sh)`; or
- a `[script("...")]` attribute, for any task.

Tasks under the built-in shell, `(python)`, `(node)`, a custom `(name)`, or
`(agent)` are not affected.

## 2. The command-string flag

A command names a known shell when an element's base name is `sh`, `bash`, `dash`,
`zsh`, `ksh`, `mksh`, `ash`, or `fish` (ignoring case and any `.exe` suffix). After
that element, `-c` or a single-dash cluster of letters that contains `c` (`-cu`,
`-euc`, `-fc`) is a command-string flag, unless a later element gives the shell its
own code or the list has `-s` (spec FR-001). Rune removes the `c` and keeps the
rest of the cluster. The body then runs exactly as it would under the same list
written without `-c`.

| Written | Runs as |
|---|---|
| `set shell := ["bash", "-euo", "pipefail", "-c"]` | `bash -euo pipefail <script>` |
| `set shell := ["bash", "-cu"]` | `bash -u <script>` |
| `[script("bash -euc")]` | `bash -eu <script>` |
| `[script("/usr/bin/env bash -euc")]` | `/usr/bin/env bash -eu <script>` |
| `set shell := ["bash", "-euo", "pipefail"]` | unchanged |
| `set shell := ["nix", "develop", "-c", "bash", "-eu"]` | unchanged (the wrapper's `-c`) |
| `set shell := ["bash", "-c", "exec bash -eu $0"]` | unchanged (own code; `$0` is `<script>`) |
| `set shell := ["bash", "-sc"]` | unchanged (without `-c`, `-s` would read stdin) |
| `[script("node -c")]`, `[script("php -c php.ini")]` | unchanged (not a known shell) |

`<script>` is the temporary file that holds the interpolated body.

## 3. How an affected body runs

These hold with or without `-c`:

1. The whole body runs as one script in one shell process. Variables and the
   current directory set on one line are visible on later lines.
2. The shell's own flags decide what happens when a line fails. With `-e`, the
   task stops at the first failing line and rune exits with that line's status.
   Without `-e`, later lines still run, and the task's status is the last
   command's status.
3. Lines are not echoed. The `@` prefix changes nothing.
4. The `-` prefix has no effect. Under `-e`, a `-` line that fails stops the task.
5. Standard input, the environment, the working directory, secret masking,
   Ctrl-C (exit 130), exit-code propagation, `||` failure hooks, and
   `RUNE_FAILED_EXIT_CODE` behave as they do for any interpreter body today.
6. A program that is not on `PATH` produces today's missing-interpreter error.

## 4. Failure output

A body that fails under an honored flag produces today's interpreter banner, built
from `interp.ExecError`, which names the task and the program. For the issue's
repro:

```console
$ rune t
one
rune: task "t" (bash) failed: exit status 1
```

The MCP tool result is marked as an error and ends with `[exit 1]`, the same code
the CLI exits with.

## 5. Cache

A `[cache]` task whose effective command changed under §2 has a new fingerprint.
It runs once on the first invocation after upgrade, then caches again.

## 6. Release notes

The changelog entry comes from the squash-merge PR title alone. It is marked
breaking (`fix(runtime)!:`) and states both points of spec FR-010: shell lists
with `-c` now apply their flags, so a failing line can fail the task. The PR body
names the lists that change: those where a known shell's `-c` has no code after
it.
