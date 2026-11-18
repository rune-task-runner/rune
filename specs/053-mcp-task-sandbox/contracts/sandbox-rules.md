# Contract: What a Confined Body Can Reach

This is the rule set every backend must provide, and how the bubblewrap backend provides it.
A test exists for each row.

## Network

| | Task without `[network]` | Task with `[network]` |
|---|---|---|
| Outbound connection, any protocol | fails | works |
| Name lookup | fails | works |
| Service on the host's loopback | fails | works |
| Socket in the host's abstract namespace | fails | works (documented limit) |
| Listener the body starts on its own loopback | works | works, on the host's loopback |
| Interfaces | `lo`, private | the host's |

`[network]` is read from the task whose body runs. Dependencies, hooks and callers have their
own.

## Filesystem

| Path | Access |
|---|---|
| The workspace (resolved directory of the Runefile) | read and write |
| Protected paths inside the workspace | read only; cannot be removed, renamed or replaced |
| Directories between the workspace and a protected file | read and write inside; cannot be renamed or removed |
| `/tmp` | the body's private directory, new for each body and removed at the end. It holds nothing of an earlier body or of the host. When something Rune binds lies under the host's `/tmp` (the project, or the Rune binary), its mount point is there |
| The project's private cache directory | read and write, persists |
| `/dev` | `null`, `zero`, `full`, `random`, `urandom`, `tty`, `ptmx`, a private `shm` and `pts`, and the links `core`, `fd`, `stdin`, `stdout`, `stderr` |
| `/proc` | the body's own processes |
| `/run`, `/var/tmp` | empty, read only |
| Hidden locations, inside the workspace or not | an empty read-only directory, or an empty read-only file |
| Everything else | read only, as the server's user |

Protected paths: the Runefile; each file the load read; the `set dotenv` file; `.git`; `.rune`.

Hidden locations, when they exist: the audit base directory; in the home directory `.ssh`,
`.gnupg`, `.aws`, `.azure`, `.kube`, `.docker`, `.config/gcloud`, `.config/gh`, `.netrc`,
`.git-credentials`, `.npmrc`, `.pypirc`, `.password-store`; the path of `SSH_AUTH_SOCK`; the
path of a `DOCKER_HOST` that starts with `unix://`.

## Process

| | Rule |
|---|---|
| Open files | standard input (empty), output and error only, whatever the server inherited |
| Terminal | none |
| Capabilities | none, also when the server runs as root |
| New privileges | not grantable (`no_new_privs`) |
| User | the server's user |
| Lifetime | every process ends when the body ends, when the call is cancelled, or when the server ends |

## Environment

The task's environment as today, with `TMPDIR=/tmp` and `XDG_CACHE_HOME=<private cache>` added
before the task's `[env]` attributes. The worker process itself runs with the server's
environment and `TMPDIR=/tmp`.

## What Rune reads for the call

In mode `enforce`, Rune's own `git` commands for a call (the audit record's git state, the
commit recorded with a plan) run under a read-only spec: no network, the repository bound read
only, nothing writable.

## bubblewrap arguments

Built by `sandbox.BwrapArgs(Spec, argv)`. The order is part of the contract: a later mount
covers an earlier one.

```text
--unshare-all
--share-net                           only when Spec.Network
--die-with-parent
--new-session
--cap-drop ALL
--ro-bind / /
--dev /dev
--proc /proc
--tmpfs /run
--tmpfs /var/run                      only when Spec.VarRunDir
--tmpfs /var/tmp
--bind <Spec.Tmp> /tmp                or --tmpfs /tmp when Spec.Tmp is empty
--tmpfs <dir>                         each hidden directory outside the workspace, sorted
--ro-bind <Spec.Empty> <file>         each hidden file outside the workspace, sorted
--bind <Spec.Workspace> <same>        or --ro-bind when Spec.ReadOnly
--bind <dir> <dir>                    each ancestor of a protected file, shallowest first
--ro-bind <path> <path>               each protected path, sorted
--tmpfs <dir>                         each hidden directory inside the workspace, sorted
--ro-bind <Spec.Empty> <file>         each hidden file inside the workspace, sorted
--bind <Spec.Cache> <Spec.Cache>      only when it is set
--ro-bind <Spec.ResolvConf> <same>    only when Spec.Network and it is set
--ro-bind <Spec.Exe> <Spec.Exe>       only when it is set
--remount-ro /run
--remount-ro /var/run                 only when Spec.VarRunDir
--remount-ro /var/tmp
--remount-ro /tmp                     only when Spec.Tmp is empty
--remount-ro <dir>                    each hidden directory, sorted
--chdir <Spec.Workspace>
--
<argv...>
```

Rules of the builder:
- A protected path that lies under another protected directory is left out, and so are its
  ancestors: a writable bind inside a read-only directory would open it again.
- An ancestor is listed once.
- With `Spec.ReadOnly` there are no ancestor, protected or cache binds.
- The read-only remounts come last, after every mount that needs a mount point inside one of
  those directories.
- `Spec.Empty` is an empty file under the private base. A hidden file is then a file that
  reads as empty, so a tool that insists on reading its configuration reads nothing. The null
  device stands in when it is not set; under a mount that allows no devices it cannot be
  opened.
- The builder reads no file. `Spec` holds only paths that exist, chosen and classified by the
  caller.

## Refusals before anything runs

Each is the error of the tool call, with audit outcome `refused` and kind `sandbox`. `<call>` is
the called task. No text starts with the word "sandbox": `rune log` prints the kind before it.

| Condition | Text |
|---|---|
| Unavailable | `task "<call>" was not run: the sandbox is unavailable: <reason>. <remedy>` |
| Directory outside | `task "<call>" was not run: the sandbox cannot confine it: the working directory of task "<task>" (<dir>) is outside the project (<workspace>)` |
| Second name | `task "<call>" was not run: the sandbox cannot protect <file>: it has <n> names (hard links), and a task could change it through another one. Remove the other names, or replace the file with a copy of itself.` |
| Symbolic link | `task "<call>" was not run: the sandbox cannot protect <path>: it is reached through the symbolic link <link>, which a task could replace. Use the real path.` |
| Missing file | `task "<call>" was not run: the sandbox cannot protect <path>: the Runefile looks for it and it does not exist, so a task could create it. Create the file, or change the Runefile so that it is not looked for (give a module its path, or remove the optional import).` |
| Private directory | `task "<call>" was not run: the sandbox cannot use its private directory <dir>: <reason>` |
| The project is a hidden location | `task "<call>" was not run: the sandbox cannot confine it: the project (<workspace>) is a location the sandbox hides from tasks` |
| Runefile found by searching up | `task "<call>" was not run: the sandbox cannot protect the choice of the Runefile: rune found <runefile> by searching up from <dir>, where a task could create a nearer one. Start rune in <workspace>, or name the Runefile with --file.` |

Outside a call, wherever a Runefile is searched for, a directory with more than one is an
error with exit code 2:
`<dir> holds more than one Runefile (<names>); remove all but one, or name one with --file`

Reasons and remedies of "unavailable":

| Reason | Remedy |
|---|---|
| `bubblewrap (bwrap) was not found on the PATH` | `Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.` |
| `bubblewrap could not start a sandbox: <line>` | `Allow unprivileged user namespaces for bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.` |
| `this system (<os>) has no sandbox backend` | `Turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.` |
| `bubblewrap (<path>) is inside the project, where a task could replace it` | as the row above |
| `the Rune binary (<path>) is inside the project, where a task could replace it` | as the row above |
| `the PATH holds <dir>, which the sandbox hides` | as the row above |

`<os>` is written in the Runefile's words: `macos` for darwin. `<line>` is the first line of
what bubblewrap printed, cut to 200 bytes.

## A body whose sandbox does not start

`the body of task "<task>" did not run: the sandbox could not start (<detail>)`

The line is printed as an error banner into the call's standard error, so the agent reads it.
The task fails with exit code 1, no `||` hook of it runs, what bubblewrap printed is in the
call's standard error as well, and the next call probes the backend again.

## The hint line of a failed body

Printed on the call's standard error after a confined body failed, once per failed body:

| Task | Text |
|---|---|
| Without `[network]` | `note: task "<task>" ran in the sandbox: it has no network (it is not marked [network]) and can write only inside <workspace>.` |
| With `[network]` | `note: task "<task>" ran in the sandbox: it can write only inside <workspace>.` |

## The worker's result

The worker writes `started\n` and then one JSON line. The server reads at most 64 KiB from the
result pipe. Anything but that line and one JSON object is a failed worker: the task fails with
`the worker of task "<task>" ended without a result (<exit status>)`.
