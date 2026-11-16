# Feature Specification: A File Flag for the MCP Server

**Feature Branch**: `048-mcp-file-flag`

**Created**: 2026-11-16

**Status**: Draft

**Input**: GitHub issue [#44](https://github.com/rune-task-runner/rune/issues/44)
(backlog MCP-12): "Accept -f/--file on rune mcp without enabling --yes"

## Overview

`rune mcp` finds its Runefile by looking in the directory it was started in
and then in each parent. That works when an agent client starts the server
inside the project. It does not work for a client configured once for the
user: such a client starts its servers from the user's home directory, or
from wherever the client itself was launched, and the server stops with
`no Runefile found`.

The `rune` command has had a way to name the Runefile for a long time,
`-f` or `--file`. The server does not accept it. `rune mcp -f
/repo/Runefile` and `rune -f /repo/Runefile mcp` both stop with an
unknown-flag error. The only workaround is a wrapper script that changes
directory first.

This feature lets an operator name the Runefile when starting the server.
It does not open the server to the other flags of the `rune` command. In
particular `--yes`, which approves every `[confirm]` task on the command
line, stays unavailable to the server: destructive tasks are allowed there
one by one, by name.

### Out of scope

- Any other flag of the `rune` command on the server: `--yes`, `--quiet`,
  `--set`, `--watch`, `--dry-run` and the rest.
- A file flag on `rune analyze`, `rune lsp` or `rune version`.
- Changing how a Runefile is found when no file is named.
- Changing the message a missing or unreadable Runefile produces. The
  server reports it as `rune -f` does today.
- Naming more than one Runefile, or serving several projects from one
  server. A second project is a second server.
- A setting, environment variable or client-side option that names the
  Runefile. The flag is the one way.
- The command that writes client configuration (issue #53). It will use
  this flag; it is not built here.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An operator serves one project's Runefile from anywhere (Priority: P1)

An operator registers Rune once, in a client's user-level configuration,
with the path of a project's Runefile. The client starts the server from
some other directory. The agent gets that project's tasks as tools. A task
it calls runs in the project, exactly as it would if the server had been
started there: in the Runefile's directory, with its `set dotenv` file, its
imports and its modules.

**Why this priority**: It is the bug. Without it a user-level configuration
cannot work, and every client that starts servers outside the project is
unusable without a wrapper.

**Independent Test**: From an empty directory, start `rune mcp -f` with the
absolute path of a Runefile elsewhere. List the tools and call a task that
prints its working directory. The tools are that Runefile's, and the
directory is the Runefile's.

**Acceptance Scenarios**:

1. **Given** a Runefile in one directory and a server started in another
   with `-f` and the Runefile's absolute path, **When** a client lists the
   tools, **Then** it gets the tasks of that Runefile.
2. **Given** that server, **When** the client calls a task, **Then** the
   task runs in the Runefile's directory, not in the directory the server
   was started in.
3. **Given** the same Runefile, **When** the server is started with the
   long form `--file`, or as `rune serve`, or with the flag before the
   command name (`rune -f PATH mcp`), **Then** it behaves the same in each
   case.
4. **Given** a Runefile that imports a file and loads a module beside it,
   and names a `set dotenv` file beside it, **When** the server is started elsewhere
   with `-f`, **Then** the imported and module tasks are tools, and a task
   sees the variables of that file.
5. **Given** a Runefile with a `[context]` task and a server started
   elsewhere with `-f`, **When** a client connects, **Then** the
   instructions it receives are that task's output, and the task ran in
   the Runefile's directory.
6. **Given** a server started elsewhere with `-f`, **When** the Runefile it
   names is saved with a new task, **Then** the server reloads it and the
   new task is a tool, as it would for a server started in the project.
7. **Given** a server started in a directory that has its own Runefile,
   **When** `-f` names a different one, **Then** the named one is served
   and the local one is ignored.
8. **Given** a server started with no `-f`, **When** it starts, **Then** it
   finds its Runefile as before, and nothing about it changes.

---

### User Story 2 - The server still refuses blanket approval (Priority: P1)

A security-minded operator checks that giving the server a file flag did
not give it anything else. `rune mcp --yes` is still a usage error. So is
every other flag of the `rune` command that the server did not have
before. Destructive tasks are still allowed only by name.

**Why this priority**: The issue's title says it: "without enabling
`--yes`". The easy way to accept `-f` is to let the server inherit every
flag of the `rune` command, and that would add a switch that approves all
destructive tasks at once.

**Independent Test**: Start the server with `--yes`, in every position the
flag can take, with and without `-f`. Each start stops with exit code 2 and
names `--yes` as unknown, and no task runs.

**Acceptance Scenarios**:

1. **Given** any Runefile, **When** the server is started as
   `rune mcp --yes`, `rune serve --yes` or `rune --yes mcp`, **Then** it
   stops with exit code 2, says the flag is unknown, and runs nothing,
   the `[context]` task included.
2. **Given** a Runefile named with `-f`, **When** `--yes` is added,
   **Then** the result is the same usage error.
3. **Given** a `[confirm]` task and a server started with `-f` and no
   `--allow-destructive`, **When** a client calls the task, **Then** the
   call is refused as before.
4. **Given** the flags of the `rune` command other than `-f`, `--file` and
   `--color`, **When** any of them is given to the server, **Then** it is
   a usage error unless the server already had a flag of that name before
   this feature.

---

### User Story 3 - A mistake in the path is reported, not worked around (Priority: P2)

An operator's configuration names a Runefile that has moved, names a
directory, or passes an empty value because a variable in the client's
configuration did not expand. The server stops with a usage error that
says what is wrong. It never falls back to looking for some other Runefile:
a server that silently serves a different project than the one configured
is worse than one that does not start.

**Why this priority**: A user-level configuration holds an absolute path,
and paths go stale. The failure has to be visible in the client's server
log.

**Independent Test**: Start the server with `-f` and each of: a path that
does not exist, a directory, an empty value. Do it in a directory that has
a Runefile of its own. Each start stops with exit code 2 and a message, and
the local Runefile is never served.

**Acceptance Scenarios**:

1. **Given** a path that names no file, **When** the server is started with
   it, **Then** it stops with exit code 2 and a message that holds the
   path.
2. **Given** a path that names a directory, **When** the server is started
   with it, **Then** it stops with exit code 2 and says that the flag
   points at a directory.
3. **Given** an empty value for the flag, **When** the server is started in
   a directory that has a Runefile, **Then** it stops with exit code 2,
   says that the flag needs a path, and does not serve the local
   Runefile.
4. **Given** a Runefile with a static error, **When** the server is started
   with `-f` naming it, **Then** it reports the same diagnostics, with the
   same exit code, as a server started in that Runefile's directory.

---

### User Story 4 - The documentation shows a user-level setup (Priority: P2)

A reader of the MCP guide finds how to register Rune for the user and not
for one project: the flag, an example configuration with an absolute path,
and what to do for a second project. The command reference lists the flag,
and the troubleshooting entry for `no Runefile found` names it as the fix
for a server.

**Why this priority**: The fix is invisible without it. A reader who meets
`no Runefile found` in a client's log has to be told that the server can be
given a path.

**Independent Test**: Read "Starting the server" in the MCP guide, the
`rune serve` row of the command reference, the output of
`rune serve --help`, and the "no Runefile found" entry of the
troubleshooting page. Each mentions the flag, and the guide has a
user-level example.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader looks for how to start the
   server outside the project, **Then** "Starting the server" shows the
   flag with an absolute path and says tasks run in the Runefile's
   directory.
2. **Given** the guide, **When** a reader wants a user-level
   configuration, **Then** it has an example of one, and says to register
   one server under its own name for each project.
3. **Given** `rune serve --help`, **When** a reader reads it, **Then** the
   flag is listed with its short and long form, and an example uses it.
4. **Given** the command reference, **When** a reader reads the
   `rune serve` row, **Then** the flag is there.
5. **Given** the troubleshooting page, **When** a reader looks up
   `no Runefile found`, **Then** the entry says that a server started by a
   client outside the project needs the flag.

---

### Edge Cases

- The path is relative. It is resolved against the directory the server
  was started in, as `rune -f` resolves it. The documentation recommends
  an absolute path, because a client's starting directory is the thing
  that cannot be relied on.
- The flag is given twice. The last value is used, as with `rune -f`.
- The named file is not called `Runefile`. Any file name is accepted, as
  with `rune -f`.
- The path has spaces, or is a Windows path with a drive letter.
- The path is a symbolic link to a Runefile. Tasks run in the directory of
  the path as given, as with `rune -f`.
- The server is started with `--http` and `-f`. The flag works the same on
  both transports.
- The server is started with `-f` and `--no-reload`. The named Runefile is
  read once.
- The named Runefile is deleted while the server runs. Reloading works on
  the path of the Runefile, however the server came by it, so the server
  keeps its tools and reports the failed reload as it already does.
- `--allow`, `--deny` or `--allow-destructive` name a task that the named
  Runefile does not have. The server stops as it does today, with the
  closest task of the named Runefile as the suggestion.
- A task in the named Runefile is called `mcp` or `serve`.
  `rune -f PATH mcp` starts the server; `rune -f PATH -- mcp` runs the
  task, as before.

## Requirements *(mandatory)*

### Functional Requirements

**The flag**

- **FR-001**: `rune serve` and its alias `rune mcp` MUST accept a flag that
  names the Runefile to serve, in the short form `-f` and the long form
  `--file`, each taking a path.
- **FR-002**: With the flag, the server MUST serve the tasks of the named
  Runefile, whatever directory the server was started in, and MUST NOT
  look for a Runefile in that directory or its parents.
- **FR-003**: The flag MUST be accepted after the command name
  (`rune mcp -f PATH`) and before it (`rune -f PATH mcp`), with the same
  result.
- **FR-004**: A relative path MUST be resolved against the directory the
  server was started in.
- **FR-005**: Without the flag, the server MUST find its Runefile as it did
  before this feature, and its output and behavior MUST NOT change.

**Where tasks run**

- **FR-006**: A task called through a server started with the flag MUST run
  in the directory of the named Runefile.
- **FR-007**: Files that a Runefile refers to by relative path, which are
  its imports, its modules and its `set dotenv` file, MUST be found relative to
  the named Runefile, as they are for `rune -f`.
- **FR-008**: The `[context]` task of the named Runefile MUST run, in that
  Runefile's directory, when the server starts, and its output MUST be the
  server's instructions.
- **FR-009**: Reloading MUST follow the named Runefile and the files it
  imports or loads: a change to one of them reloads the server's tools.

**What stays closed**

- **FR-010**: `--yes` MUST remain an unknown flag for `rune serve` and
  `rune mcp`, in every position, with or without the file flag. The server
  MUST stop with exit code 2 and run no task.
- **FR-011**: No flag of the `rune` command other than the file flag MUST
  become available to the server through this feature.
- **FR-012**: A `[confirm]` task MUST still need `--allow-destructive` by
  name on a server started with the file flag.
- **FR-013**: The file flag MUST NOT become available to any command other
  than `rune serve` and its alias through this feature.

**Mistakes**

- **FR-014**: A path that names no file MUST stop the server with exit code
  2 and a message that holds the path.
- **FR-015**: A path that names a directory MUST stop the server with exit
  code 2 and a message saying so.
- **FR-016**: An empty value for the flag MUST stop the server with exit
  code 2 and a message saying that the flag needs a path. The server MUST
  NOT fall back to finding a Runefile by itself.
- **FR-017**: No mistake in the flag's value may run a task, the
  `[context]` task included.
- **FR-018**: A named Runefile with a static error MUST produce the same
  diagnostics and exit code as the same Runefile served from its own
  directory.

**Documentation**

- **FR-019**: `rune serve --help` MUST list the flag in both forms and
  MUST have an example that uses it.
- **FR-020**: The MCP guide's "Starting the server" MUST show the flag with
  an absolute path, say that tasks run in the Runefile's directory, give an
  example of a user-level client configuration that uses it, and say that
  each project is registered as its own server.
- **FR-021**: The command reference MUST list the flag in the `rune serve`
  row.
- **FR-022**: The troubleshooting entry for `no Runefile found` MUST say
  that a server started by a client outside the project is given the
  Runefile with the flag.

**Tests**

- **FR-023**: An end-to-end test MUST start the built `rune` binary as a
  server in one directory with the flag naming a Runefile in another, and
  MUST show over the protocol that the tools are that Runefile's and that
  a called task ran in the Runefile's directory.
- **FR-024**: Tests MUST cover: both forms and both positions of the flag;
  `--yes` refused in each position; each mistake of FR-014 to FR-016;
  reload of a Runefile named with the flag; and a server started without
  the flag behaving as before.

### Key Entities

- **Server**: one running `rune serve` or `rune mcp`, on stdio or HTTP.
- **Named Runefile**: the file the flag points at. Its directory is where
  tasks run and where relative paths in it start.
- **Starting directory**: the directory the server process was started in.
  With the flag it decides nothing but how a relative path is read.
- **User-level configuration**: a client's configuration that applies to
  every project of a user, as opposed to one kept in a project.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A server started in an empty directory with the absolute path
  of a Runefile elsewhere serves that Runefile's tasks, and a task that
  prints its working directory prints the Runefile's directory.
- **SC-002**: The four ways to give the flag (`-f` and `--file`, each after
  and before the command name) give the same tool list.
- **SC-003**: `--yes` given to the server stops it with exit code 2 in
  every one of the positions tested, and in none of them does a task run.
- **SC-004**: Each of the three mistakes (no such file, a directory, an
  empty value) stops the server with exit code 2 and its own message, in a
  directory that has a Runefile, and that Runefile is never served.
- **SC-005**: A server started without the flag passes every test it
  passed before, unchanged.
- **SC-006**: A reader finds the flag in the server's help, in the MCP
  guide with a user-level example, in the command reference and in the
  troubleshooting entry.
- **SC-007**: The full test suite, the linter and the documentation checks
  pass on Linux and Windows.

## Assumptions

- "Tasks execute in the Runefile's directory" is what `rune -f` already
  does on the command line, and the server already runs tasks in the
  directory of the Runefile it loaded. The feature gives the server a way
  to be told which Runefile that is; it does not change where tasks run.
- Accepting the flag before the command name (`rune -f PATH mcp`) is in
  scope because the issue lists that form as broken, and because it is
  what a user who knows `rune -f PATH task` will type.
- The issue names a test, `TestMCPE2E_Pending_ServeAcceptsFileFlag`, from a
  suite that is not in this repository. The end-to-end test of FR-023 is
  written here and takes its place.
- The issue asks the guide to "remove the unsupported `-f` note". The
  guide in this repository has no such note, so there is nothing to
  remove; the guide gains the flag and the example.
- An empty value is treated as a mistake on the server, although
  `rune -f ""` on the command line quietly looks for a Runefile. On the
  command line the user sees which Runefile ran. A server started by a
  client would serve a project nobody named, with nobody watching.
- Messages for a missing file and for a directory are the ones `rune -f`
  gives today. Improving their wording is not part of this feature.
- The user-level example in the guide uses Claude Code and Codex CLI, the
  two clients that were run in the compatibility research. Their user-level
  configuration is described there from their documentation; it was not
  run. The other clients are pointed at their own configuration files,
  which the guide already names.
