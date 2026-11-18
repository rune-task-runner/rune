# Data Model: Confine the Tasks an Agent Runs with an Operating-System Sandbox

Nothing new is stored, apart from two fields of the audit record and the private directories.
The model is the values that travel between the server, the backend and the worker.

## Mode

`sandbox.Mode`, a string: `enforce` or `off`.

| Source | Order |
|---|---|
| `--sandbox` on `rune mcp` and `rune serve` | first |
| `RUNE_SANDBOX` in the server's environment | when the flag is absent |
| default | `enforce` |

Validation: any other value, the empty string given explicitly to the flag included, is a usage
error (FR-034). The endpoint of an `(agent)` task reads the variable only.

## Backend

`sandbox.Backend`, an interface:

| Method | Meaning |
|---|---|
| `Name() string` | `bubblewrap`. Recorded in the audit log and shown in the startup line |
| `Probe(ctx) error` | Starts a real sandbox. Nil means it can confine |
| `Command(ctx, Spec, argv) (*exec.Cmd, error)` | The command that runs `argv` under the rules of `Spec` |

`sandbox.Detect(goos)` returns the bubblewrap backend on Linux and an `Unsupported` backend
elsewhere, whose `Probe` reports `this system (<os>) has no sandbox backend`.

## State of a server (`sandboxState` in `internal/cli`)

One per server, shared by every adapter the server loads (a reload keeps it).

| Field | Meaning |
|---|---|
| `mode` | The mode, fixed at start |
| `backend` | The detected backend |
| `exe` | The resolved path of the running Rune binary |
| `workspace` | The resolved directory of the Runefile |
| `base` | The private directory base of the project (R12) |
| `auditBase` | The audit log base, hidden from bodies |
| `home`, `getenv` | The home directory and the environment, for the hidden locations |
| `err`, `checked` | The last probe result and its time, behind a mutex |

Derived:

| State | Condition | A call |
|---|---|---|
| confining | mode `enforce`, last probe nil | runs each body in a sandbox |
| unavailable | mode `enforce`, last probe failed | is refused; the next call probes again |
| off | mode `off` | runs in the server process, as before |

An adapter with no state (a test, an embedder of the engine) runs bodies in the server process.

## Spec

`sandbox.Spec`, the rules for one body. All paths are absolute and resolved.

| Field | Meaning |
|---|---|
| `Workspace` | The project directory: writable, or read only with `ReadOnly` |
| `ReadOnly` | Nothing is writable: the spec of Rune's own `git` commands (R20) |
| `Protected` | Existing paths inside the workspace, bound read-only (FR-016) |
| `HiddenDirs`, `HiddenFiles` | Existing paths the body sees empty and read only (FR-023), classified by the caller |
| `Empty` | An empty file under the private base that stands in for each hidden file |
| `Tmp` | The body's private temporary directory, bound at `/tmp`; empty means an empty read-only `/tmp` |
| `Cache` | The project's private cache directory, bound at its own path; empty means none |
| `Network` | Whether the task has `[network]` |
| `ResolvConf` | The real path of the resolver file when it must be bound back (R13) |
| `VarRunDir` | `/var/run` is a real directory and is hidden too |
| `Exe` | The Rune binary, bound at its own path; empty means none |

Validation, before any body of a call runs:

| Rule | Refusal |
|---|---|
| A part of a protected path below the workspace is a symbolic link | names the path and the link |
| The Runefile, a loaded file or the dotenv file has a link count above one | names the file |
| The directory of a reachable task resolves outside the workspace | names the task and the directory |
| The private base resolves inside the workspace, or cannot be created | names the directory |
| A file the load looked for inside the workspace does not exist | names the path |

Reasons the state reports itself unavailable, beyond a failed probe: the backend program or
the Rune binary resolves inside the workspace or the private base; a directory of the server's
`PATH` resolves under a hidden location.

## Protected set of a load

Computed per call from the adapter's load:

| Path | When |
|---|---|
| The Runefile | always |
| Each file the load read (imports, modules) | when it exists and lies inside the workspace |
| The `set dotenv` file | when it exists and lies inside the workspace |
| `<workspace>/.git` | when it exists |
| `<workspace>/.rune` | always; created when missing (FR-019) |

A loaded file outside the workspace needs no entry: everything outside is read-only.

The load's paths are as the load wrote them, and the project may have been reached through a
symbolic link. A path under the load's own root is therefore placed under the resolved
workspace by its relative part before it is checked, so that a link inside the workspace is
still seen. Any other path is resolved whole.

## Looked-for set of a load

The files the load asked for and did not find (`pathRecorder` keeps them): the candidate names
of a module that was found under a later name, and optional imports. Each one inside the
workspace is a refusal (FR-047). The dotenv file is not in this set.

## Job

Sent to the worker on descriptor 3 as one JSON object.

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `shell`, `interp` or `agent` |
| `task` | string | The task name, for error texts |
| `lines` | list | For `shell`: text, no-echo flag, continue-on-error flag and span of each line |
| `script` | string | For `interp`: the joined body |
| `command` | list of strings | For `interp`: the interpreter command; for `agent`: the agent command |
| `span` | span | For `interp`: the first body line |
| `prompt` | string | For `agent`: the prompt |
| `dir` | string | The task's directory |
| `env` | list of base64 strings | The task's environment, each entry as bytes because a value need not be UTF-8; null means inherit |
| `quiet` | bool | Suppress the command echo |

## Result

Sent by the worker on descriptor 4: the line `started`, then one JSON object.

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `` (success), `exec`, `missing-interpreter`, `interp-exec`, `agent-not-configured`, `agent-not-installed`, `agent-auth`, `cancelled` or `error` |
| `task`, `line`, `name` | string | The fields of the executor's typed error |
| `span` | span | The failing line |
| `code` | int | The exit status |
| `message` | string | The text of a wrapped error |
| `output` | string | For `agent-auth`: the text the agent printed before it failed |

The server turns the result back into the executor's error type:

| `kind` | Error |
|---|---|
| `exec` | `*shell.ExecError` |
| `missing-interpreter` | `*interp.MissingInterpreterError` |
| `interp-exec` | `*interp.ExecError` |
| `agent-not-configured`, `agent-not-installed`, `agent-auth` | the errors of `internal/runtime/agent` |
| `cancelled` | the call's context error |
| `error` | a plain error with the message |

The worker cuts `line` and `message` to 4 KiB each, so that a result fits the limit below
however long the line that failed is.

Without `started`: `*sandboxStartError`, which wraps `scheduler.ErrBodyNotRun`. With `started`
and anything but one JSON object within 64 KiB: a plain error with the worker's exit status.

## Lifecycle of a confined body

```text
call arrives -> bound -> value policy -> sandbox preflight (R10) -> plan and approval
  -> scheduler -> for each body:
       make tmp dir -> build Spec -> start bwrap + worker -> send job
       -> "started" -> body runs -> result -> remove tmp dir
     cancel at any point: kill bwrap -> the namespace ends -> remove tmp dir
```

## Audit record

`sandbox` object of a call record (start phase), an addition to spec 052:

| Field | Type | Meaning |
|---|---|---|
| `enforced` | bool | A sandbox confines the bodies of this call |
| `backend` | string | `bubblewrap` when enforced; absent otherwise |
| `mode` | string | `enforce` or `off` |

New error kind: `sandbox`, with outcome `refused` and no exit code.

## Private directories

```text
<user cache dir>/rune/sandbox/<project id>/
├── empty             # an empty file, bound over each hidden file
├── cache/            # XDG_CACHE_HOME of every body of the project; persists
├── tmp/<random>/         # /tmp of one body; removed when the body ends
└── tmp/<random>.lock     # held by the server while that body runs
```

Mode 0700. `<project id>` is the audit log's id of the Runefile.
