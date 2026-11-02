# Contract: Exit codes on the CLI and over MCP

**Feature**: [../spec.md](../spec.md) | **Decision table**: [../data-model.md](../data-model.md)

This is the user-visible contract the tests pin. It replaces the exit-code table
of spec 001 (`contracts/cli.md`, FR-021) for task failures. All other rows carry
over unchanged.

## 1. Process exit code

| Code | Meaning |
|---|---|
| `0` | All requested tasks succeeded. |
| *N* | A task body failed with exit code *N*. The task can be the one requested, a dependency, or a later task on the command line. It is the failure the banner names. |
| `1` | A task failed without a body exit code: `[confirm]` declined or no terminal, missing interpreter, a missing or failing agent CLI, or a shell error with no status. |
| `2` | Usage error: no Runefile, unknown task, bad arguments, an `agent` task with no `agent_cmd`. |
| `3` | Validation error (parse or analyze). Nothing was executed. |
| `130` | Interrupted (SIGINT or SIGTERM). |

*N* is never remapped. A body may exit 2, 3, or 130, and rune then exits with
that same number. The banner tells a body failure apart from rune's own error:

```text
rune: task "gate" failed at "exit 2": exit status 2      <- body exited 2
rune: unknown task: gaet                                 <- rune usage error, also 2
```

A body terminated by signal *S* reports 128 + *S* (for example 143 for SIGTERM)
on Linux and macOS.

Shell bodies report 0 to 255 on every OS. Interpreter bodies on Windows report the
full code the process returned.

Once the run is interrupted, the code is 130 whatever the body then reported,
including a child that traps SIGINT and exits with its own code.

## 2. Banner

Unchanged byte for byte:

```text
rune: task "<task>" failed at "<line>": exit status <N>
```

`[no-exit-message]` suppresses this line. The exit code is still *N*, and in
general the code the task exits with without the attribute.

## 3. Failure hooks

- `RUNE_FAILED_EXIT_CODE` equals the process exit code for the same failure.
- A hook's own outcome never changes the exit code.

## 4. MCP tool result

For a task call, the text content ends with `[exit N]` (followed by the
fix-suggestion section when `||` hooks produced output). *N* is the code
section 1 gives for the same task and arguments. `isError` is `true` exactly when
*N* is not 0.

```text
gate: refused
[exit 2]
```

Errors raised before the engine runs (unknown or unauthorized task, invalid
arguments) keep today's error-text results with no `[exit N]` marker. Errors
raised while the task runs carry the marker, so a usage or validation error at
that stage reads `[exit 2]` or `[exit 3]`, the same as the CLI (spec SC-002).
