# Data Model: `rune agent init`

All types live in `internal/agentinit` unless noted. Nothing is stored: the model exists for
one run.

## Client

One of `claude`, `codex`, `vscode`. `ParseClient(name)` returns it or an error that lists the
three.

## Adapter

What Rune knows about one client. A value, not an interface: the three differ in data and in
one planning function.

| Field | Meaning |
|---|---|
| `Name` | The name in the report: "Claude Code", "Codex CLI", "VS Code" |
| `Binary` | The program asked for its version: `claude`, `codex`, `code` |
| `Checked` | The exact versions with a recorded run (FR-031). Empty for VS Code |
| `Best` | The strongest outcome: Rune-only, or Rune-only within a scope |
| `Scope` | For VS Code: `the custom agent "rune"` |
| `Removed` | The command-running tools the files remove, named in "Registered only" |
| `Kept` | For VS Code: the tools every other agent keeps |
| `Steps` | The steps left to the person, in order |
| `Check` | The lines of the manual check |
| `plan` | The function that turns existing files into planned files |

## Inputs

| Field | Meaning |
|---|---|
| `Runefile` | Absolute path of the project's Runefile, as written into the registration |
| `ShownRunefile`, `ShownDir` | The Runefile and its directory as the report prints them, quoted for a shell where needed |
| `Unpinned` | `--unpinned`: the registration holds no path |
| `Force` | `--force` |
| `Files` | Existing content by project-relative path with forward slashes. A missing key is a file that does not exist |
| `Version` | The version the client reported, or empty |

## File

One client file after planning.

| Field | Meaning |
|---|---|
| `Path` | Project-relative, forward slashes |
| `Old`, `New` | The bytes before and after. Equal when nothing changes |
| `Existed` | Whether the file was there |
| `Changes` | Human text of what was set, for the file line of the report |
| `Entries` | The owned entries of this file with their state |

`Status()` is `created` (not there before), `updated` (bytes differ) or `unchanged`.

## Entry

One owned entry (FR-018): `Name` (such as `mcpServers.rune.args` or `permissions.deny: Bash`)
and `State`: `present`, `missing` or `differs`. The state describes the file before the run,
which is what `--check` prints.

## Conflict

| Field | Meaning |
|---|---|
| `File`, `Line`, `Col` | Where the value found starts |
| `Width` | How many columns the caret covers |
| `Msg` | The value found, the value wanted, and what to do |
| `Forceable` | Whether `--force` replaces it |

Kinds: another value at an owned key (forceable); another type where a table or a list is
needed (not forceable); the deny rule `mcp__rune` (not forceable); an agent file with other
content (forceable).

## Errors

- `*ParseError{File, Line, Col, Msg}`: a client file is not valid JSON or TOML. Exit 3.
- `*ManualError{File, Line, Col, Setting}`: the file holds the settings in a form that cannot
  be extended in place. The position is that of the construct that defines the table another
  way, or 1:1 when it cannot be named. `Setting` is the key and value to set by hand. Exit 2.

## Outcome

| Field | Meaning |
|---|---|
| `Kind` | `RuneOnly`, `RuneOnlyWithin` or `RegisteredOnly` |
| `Scope` | Set for `RuneOnlyWithin` |
| `Reason` | For `RegisteredOnly`: why |
| `Remains` | For `RegisteredOnly`: the tools that were not checked or are kept |
| `Steps` | The pending steps |

Rule: `Kind` is the adapter's `Best` only when `Version` equals a version in `Checked`.
Otherwise it is `RegisteredOnly`. An adapter with no checked version gives its own reason
whatever the version.

## Result

`Plan(client, inputs)` returns `Result{Client, Version, Files, Conflicts, Notes, Outcome}` or
one of the two errors. `Notes` are the lines about `--no-reload`, `[confirm]` tasks, the
pinned path, and kept deny or ask rules for single Rune tools. When `Conflicts` is not empty,
every file's `New` equals its `Old`: a conflict in one file changes none.

`Report(result, dryRun)` is the text of a run, `CheckReport(result)` the text of `--check`,
and `Diff(result)` the unified differences of a dry run.

## In `internal/cli`

`AgentInitFlags{DryRun, Check, Force, Unpinned}` with three unexported fields that are nil
in the real command and set by tests: `version` (the lookup of the client's version),
`versionLimit` (its time limit, five seconds when zero) and `write` (the function that writes
one file). `AgentInit(opts, flags, client)` maps the result to output, files and an exit
status. The Runefile comes from `opts.File`, as for other commands.

## State

A project is in one of three states for a client, and the command moves it only forward:

```text
not set up  --run-->  set up            (files created or updated)
set up      --run-->  set up            (nothing written)
any         --run with conflict-->  unchanged   (nothing written, exit 2)
```
