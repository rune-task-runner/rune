# Feature Specification: Confine the Tasks an Agent Runs with an Operating-System Sandbox

**Feature Branch**: `053-mcp-task-sandbox`

**Created**: 2026-11-18

**Status**: Draft

**Input**: GitHub issue [#51](https://github.com/rune-task-runner/rune/issues/51)
(backlog MCP-17): "Enforce network and filesystem sandboxing for agent-invoked tasks"

## Overview

Today `[network]` is a hint. It sets a flag on the tool an agent sees, and it stops nothing: a
task without it can open any connection, and any task an agent calls can write wherever the
server's user can write. Approval in the client and tool hints cannot change that, because the
task runs as an ordinary process of that user.

This feature makes the operating system enforce two rules for every task an agent runs:

1. A task has no network unless its author marked it `[network]`.
2. A task writes only inside the project, and not to the files that decide what it may do.

```text
$ rune mcp
rune MCP server on stdio (sandbox: bubblewrap)

# a task without [network], called by an agent
$ curl -sS https://example.com
curl: (6) Could not resolve host: example.com
[exit 6]

# any task, called by an agent
$ echo x > ~/.bashrc
/home/dev/.bashrc: Read-only file system
[exit 1]
```

| Question | Today | After |
|---|---|---|
| Can a task without `[network]` reach the internet, DNS or a service on the host's loopback? | Yes | No. The call fails inside the task |
| Can a task write outside the project? | Yes, anywhere the user can | No |
| Can a task change the Runefile, `.git`, the stored plans or the audit log? | Yes | No |
| Can a task reach the Docker socket or the SSH agent? | Yes | No |
| Does a process a task left in the background outlive the call? | Yes | No |
| What happens on a host with no sandbox? | Tasks run unconfined | Every call is refused, unless the operator started the server unsandboxed |
| Can the operator see which mode a call ran in? | No | The startup line, the audit record and `rune log` say it |
| Does anything change for a person who runs `rune build`? | - | No |

### What the sandbox is and is not

The sandbox limits what the code of a task can reach once Rune runs it for an agent. It does
not decide which tasks an agent may call: grants, approvals, value policies and plans do that,
and they keep working as they do.

It promises that a task cannot write outside its workspace and cannot use the network without
`[network]`. It does not promise that a task cannot read a secret: a task still reads the files
its user can read, apart from the locations this spec hides, and what a task prints goes to the
agent.

### Out of scope

- Sandboxing a task a person runs on the command line.
- A sandbox on macOS or Windows. Both are stated as unsupported, and calls there are refused
  unless the operator starts the server unsandboxed.
- A second layer of restriction with Landlock or system-call filters. The research records the
  evaluation.
- Per-task filesystem rules in the Runefile, such as extra writable or readable paths.
- Mounting credential files or agent sockets into the sandbox by configuration.
- Network rules finer than all or nothing: no allowlist of hosts, ports or protocols.
- Limits on CPU, memory or the number of processes.
- Protecting the configuration files of an agent client inside the project, such as
  `.mcp.json`. The documentation names this boundary.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A task has no network unless it is marked `[network]` (Priority: P1)

An agent calls a task that builds the project. The task, or a script it runs, tries to download
something, resolve a name, or talk to a service on the machine's loopback address. None of it
works. Another task of the same Runefile is marked `[network]` and fetches a file: that works.

**Why this priority**: This is the first of the two guarantees the issue asks for, and the
meaning of `[network]` that authors already assume.

**Independent Test**: On a supported host, serve a Runefile with a plain task and a `[network]`
task that both connect to a test service listening on the host's loopback address and resolve a
host name. Call both from a real client session. The plain task fails to connect and to
resolve, and the service saw no connection from it. The `[network]` task connects.

**Acceptance Scenarios**:

1. **Given** a task without `[network]`, **When** an agent calls it and its body connects to an
   external address, **Then** the connection fails inside the task and no packet leaves the
   host.
2. **Given** the same task, **When** its body resolves a host name, **Then** the lookup fails.
3. **Given** a service listening on the host's loopback address, **When** the body of a task
   without `[network]` connects to it, **Then** the connection fails and the service sees
   nothing.
4. **Given** a task marked `[network]`, **When** an agent calls it and its body connects to
   that service, **Then** the connection succeeds.
5. **Given** a task without `[network]` that starts its own listener and connects to it inside
   one body, **When** an agent calls it, **Then** that works: the task has a loopback of its
   own, with nothing of the host on it.
6. **Given** a task without `[network]` whose environment holds proxy variables, **When** its
   body uses them, **Then** the connection to the proxy fails: the denial does not depend on
   variables or on the name of the program.

---

### User Story 2 - A task writes only inside the project (Priority: P1)

An agent calls a task. Its body writes build output into the project, which works. It also
tries to write to the home directory, to `/etc`, to a path that leaves the project through
`..`, and through a symbolic link that points outside. Every one of those writes fails, for a
plain task and for a `[network]` task alike.

**Why this priority**: This is the second guarantee. Without it a task an agent can call may
change the user's shell profile, credentials or any other project.

**Independent Test**: Serve a Runefile whose task writes a file inside the project and then
attempts each escape. Call it. The file inside the project exists, and a marker directory
outside the project is byte for byte what it was.

**Acceptance Scenarios**:

1. **Given** a task an agent calls, **When** its body creates and changes files inside the
   project, **Then** the writes succeed.
2. **Given** the same task, **When** its body writes to a file outside the project by an
   absolute path, **Then** the write fails and the file is unchanged.
3. **Given** a path that leaves the project with `..`, **When** the body writes to it, **Then**
   the write fails.
4. **Given** a symbolic link inside the project that points outside it, **When** the body
   writes through the link, **Then** the write fails.
5. **Given** a file outside the project, **When** the body tries to give it a second name
   inside the project with a hard link, **Then** the link is not created.
6. **Given** a task marked `[network]`, **When** its body attempts the same writes, **Then**
   they fail the same way: `[network]` changes the network and nothing else.
7. **Given** a task whose working directory setting names a directory outside the project,
   **When** an agent calls it or a task that reaches it, **Then** the call is refused before
   anything runs, and the refusal names the task and the directory.
8. **Given** a task that writes a temporary file and a cache file the usual way, **When** an
   agent calls it, **Then** both writes succeed in private directories Rune provides, the
   temporary files are gone when the body ends, and the cache is there on the next call.

---

### User Story 3 - A task cannot change what decides its own rights (Priority: P1)

Some files inside the project decide what an agent may do next: the Runefile and the files it
loads, the stored plans a person approved, and the control data of the repository. A task an
agent runs can read them. It cannot change, remove, rename or replace them. The audit log,
which lives outside the project, cannot be reached at all.

**Why this priority**: A task that can rewrite the Runefile grants itself `[network]` for the
next call. Protection that a task can switch off is not protection.

**Independent Test**: Serve a Runefile that imports a file from a subdirectory, in a git
repository with a stored plan. Call a task that tries to append to the Runefile, replace the
imported file by renaming its directory, write into `.git`, change a stored plan, and write
into the audit log directory. Every attempt fails, the digests of those files are unchanged,
and the server did not reload.

**Acceptance Scenarios**:

1. **Given** a served Runefile, **When** a task's body writes to it, removes it or renames
   another file over it, **Then** the attempt fails and the file is unchanged.
2. **Given** a file the Runefile imports from a subdirectory, **When** the body renames that
   subdirectory and creates a new file under the old name, **Then** the rename fails.
3. **Given** a project in a git repository, **When** the body writes into the repository's
   control directory, **Then** the write fails. Reading it, as `git status` and `git diff` do,
   works.
4. **Given** a plan stored for the project, **When** the body of any task writes into the plan
   store, **Then** the write fails. A task that applies a plan still reads the approved file.
5. **Given** the directory of the audit log, **When** the body lists or writes it, **Then** it
   finds nothing there and the write fails.
6. **Given** the file named by `set dotenv`, **When** the body writes to it, **Then** the write
   fails.
7. **Given** a Runefile that has a second name through a hard link, **When** an agent calls any
   task, **Then** the call is refused before anything runs, and the refusal names the file and
   says that its other name must be removed.
8. **Given** a Runefile, or a file it loads, that is reached through a symbolic link inside the
   project, **When** an agent calls any task, **Then** the call is refused before anything
   runs, and the refusal names the path and the link.
9. **Given** a Runefile that looks for a file inside the project that does not exist, such as
   an optional import or the first candidate of a module found in a directory, **When** an
   agent calls any task, **Then** the call is refused before anything runs, and the refusal
   names the path: a task could create it and the next load would read it.
10. **Given** a project with no `.git`, **When** a task creates one whose configuration names a
    command, and an agent then makes another call, **Then** that command does not run outside
    a sandbox: Rune reads the repository state inside one.
11. **Given** a person who edits the Runefile in an editor while the server runs, **When** they
   save, **Then** the server reloads as it does today: only a task's own writes are stopped.

---

### User Story 4 - No call runs unconfined without the operator saying so (Priority: P1)

Rune is started for an agent on a host where the sandbox cannot work: another operating
system, a Linux host without the backend, or a kernel that does not permit it. Every task call
is refused before anything runs, and the refusal says why and what the operator can do. An
operator who accepts the risk starts the server unsandboxed. That choice is stated when the
server starts and in every record of a call.

**Why this priority**: A sandbox that silently turns into ordinary execution gives a false
guarantee, which is worse than none.

**Independent Test**: Start a server on a host with no usable backend. Call a task that writes
a marker file. The call is refused, the marker does not exist, and the refusal names the reason
and `--sandbox off`. Start the server again with `--sandbox off`. The startup output states that
tasks run unsandboxed, the call runs, and its audit record states that no sandbox was enforced.

**Acceptance Scenarios**:

1. **Given** a host where the backend is missing, **When** a server starts, **Then** it starts,
   prints a warning that every task call will be refused and why, and its startup line says the
   sandbox is unavailable.
2. **Given** that server, **When** an agent calls any task, **Then** the result is an error
   that names the sandbox, the reason, and the two remedies (install the backend, or start the
   server with `--sandbox off`), and no command of the task, its dependencies or its hooks ran.
3. **Given** that server and a Runefile with a `[context]` task, **When** the server starts,
   **Then** the task is not run and the instructions say that the project context is missing.
4. **Given** an unsupported operating system, **When** an agent calls a task, **Then** the
   refusal says that this system has no sandbox.
5. **Given** a server started with `--sandbox off`, **When** it starts, **Then** it prints a
   warning that tasks run without a sandbox, and its startup line says so.
6. **Given** that server, **When** an agent calls a task, **Then** the task runs as it did
   before this feature.
7. **Given** `RUNE_SANDBOX=off` in the server's environment and no `--sandbox` flag, **When**
   the server starts, **Then** it behaves as with `--sandbox off`.
8. **Given** a value other than `enforce` or `off`, in the flag or in the variable, **When** the
   server starts, **Then** it fails with a usage error before any Runefile code runs.
9. **Given** a backend that stopped working after the server started, **When** an agent calls a
   task, **Then** the body does not run unconfined: the task fails with an error that names the
   sandbox.
10. **Given** a host where the backend is installed while the server runs, **When** the next
    call arrives, **Then** it runs confined without a restart.

---

### User Story 5 - Every way a task runs code is confined, each task with its own rights (Priority: P1)

A task's code can run in several ways: the built-in shell with its redirections and built-in
commands, an interpreter such as Python or Node, a custom shell, a program the body starts, and
the tasks it depends on or runs as hooks. All of them are confined. A task's `[network]` gives
the network to that task's body and the processes it starts, and to no other task of the same
call. Calls that run at the same time do not share rights.

**Why this priority**: The built-in shell runs inside the server process today, so confining
only the programs a body starts would leave redirections such as `echo x > /etc/passwd` free.

**Independent Test**: Serve a Runefile with five tasks that write outside the project: one by
shell redirection, one with a shell built-in, one in Python, one with a `set shell` override,
and one through a child process. Add a plain task that depends on a `[network]` task, and a
`[network]` task with a plain failure hook. Call each. No write lands outside, the dependency
reaches the test service, and neither the plain task nor the plain hook does.

**Acceptance Scenarios**:

1. **Given** a default-shell task, **When** its body redirects output to a file outside the
   project, **Then** the redirection fails.
2. **Given** a default-shell task, **When** its body changes directory outside the project
   with the built-in `cd` and writes there, **Then** the write fails.
3. **Given** a Python task, a Node task, a `[script]` task and a task under a `set shell`
   override, **When** each writes outside the project or opens a connection without
   `[network]`, **Then** each attempt fails.
4. **Given** a plain task that depends on a `[network]` task, **When** an agent calls it,
   **Then** the dependency has the network and the plain task's own body does not.
5. **Given** a `[network]` task with a plain `&&` hook and a plain `||` hook, **When** they
   run, **Then** neither hook has the network.
6. **Given** a task in an imported module, **When** an agent calls it, **Then** it is confined
   like any other task.
7. **Given** a `[network]` call and a plain call that run at the same time, **When** each
   connects to the test service, **Then** the `[network]` call connects and the plain call does
   not, in whichever order they start.
8. **Given** any call an agent makes, **When** it ends, **Then** the server itself can still
   use the network and write its own files: a call changes nothing for the server process.
9. **Given** a body that runs `rune` to start another task, **When** that task is marked
   `[network]` and the body is not, **Then** the inner task has no network: a body cannot widen
   its own rights by starting Rune.

---

### User Story 6 - Sockets, agents and devices of the host are out of reach (Priority: P2)

The machine has a container daemon socket, an SSH agent, a session bus and devices. A task an
agent runs sees none of them, with or without `[network]`. It does not inherit any open
connection or file of the server.

**Why this priority**: A task that can talk to the container daemon controls the machine, so
filesystem rules alone would not hold.

**Independent Test**: Start listeners on Unix sockets in the user's runtime directory and in
the system temporary directory, set `SSH_AUTH_SOCK` and `DOCKER_HOST` to sockets in another
directory, and hold an extra open file in the server. Call a plain and a `[network]` task that
try to connect to each socket and list their open files. No connection succeeds, and each task
has only its standard input, output and error.

**Acceptance Scenarios**:

1. **Given** a Unix socket in the system runtime directory or the user's runtime directory,
   **When** a task's body connects to it, **Then** the socket is not there.
2. **Given** a Unix socket in the system temporary directory, **When** a task's body connects
   to it, **Then** the socket is not there.
3. **Given** `SSH_AUTH_SOCK`, or a `DOCKER_HOST` that names a Unix socket, in the server's
   environment, **When** a task's body connects to that socket, **Then** the connection fails,
   wherever the socket is.
4. **Given** the server's own open files and connections, including its client connection and
   its listening socket, **When** a task's body lists its open files, **Then** it has only
   standard input, output and error.
5. **Given** the devices of the host, **When** a task's body lists the device directory,
   **Then** it sees only the null, zero, full, random and terminal devices, a private
   shared-memory and pseudo-terminal directory and the links to its standard streams.
6. **Given** the credential stores of the user's home directory that this spec lists, **When**
   a task's body reads them, **Then** they are empty.
7. **Given** a server started from a terminal, **When** a task's body tries to write input
   into that terminal, **Then** it has no terminal to write to.

---

### User Story 7 - Cancelling a call stops everything it started (Priority: P2)

An agent calls a long task. The task starts children, and one of them detaches itself to keep
running in the background. The client cancels the call. Every process of the call ends,
detached ones included, and the server answers the next call. A task that ends normally leaves
no process behind either.

**Why this priority**: A helper that survives its call is code that keeps running with nobody
watching, and a detached one could outlive the sandbox's purpose.

**Independent Test**: Call a task that starts a detached child which writes a heartbeat file in
the project every 100 ms, then sleeps. Cancel the call. Within two seconds the heartbeat stops.
Call another task: it answers. Repeat with a task that ends normally right after it detached
the child: the heartbeat stops when the body ends.

**Acceptance Scenarios**:

1. **Given** a running call with child processes, **When** the client cancels it, **Then** all
   of its processes end within two seconds, including one that started a new session.
2. **Given** that cancellation, **When** the agent calls another task, **Then** it runs.
3. **Given** a body that leaves a process in the background and ends, **When** the body ends,
   **Then** that process ends too.
4. **Given** a server that is stopped while a call runs, **When** the server process ends,
   **Then** the processes of the call end.
5. **Given** a cancelled call, **When** the audit log is read, **Then** the call has outcome
   `cancelled`, as today.

---

### User Story 8 - The record of a call says how it was confined (Priority: P2)

An operator reads the audit log after an incident. Each call states whether a sandbox was
enforced, which backend enforced it, and which mode the server was in. `rune log` marks the
calls that ran unsandboxed and the calls that were refused because no sandbox was available.

**Why this priority**: An investigation has to know whether the guarantees held for the call in
question. Spec 052 left the field in place for this.

**Independent Test**: Make one call on a confining server, one on a server started with
`--sandbox off`, and one on a server with no backend. Read the log as JSON and as text. The
three actions state enforced with the backend name, not enforced with mode `off`, and refused
with error kind `sandbox`.

**Acceptance Scenarios**:

1. **Given** a call that ran confined, **When** its record is read, **Then** it states that a
   sandbox was enforced, the name of the backend, and mode `enforce`.
2. **Given** a call on a server started unsandboxed, **When** its record is read, **Then** it
   states that no sandbox was enforced and mode `off`.
3. **Given** a call refused because the sandbox was unavailable, **When** its record is read,
   **Then** it has outcome `refused`, error kind `sandbox`, no exit code, mode `enforce` and no
   backend.
4. **Given** those three calls, **When** the operator runs `rune log`, **Then** the unsandboxed
   call carries a line that says it ran without a sandbox, the refused call carries its
   reason, and the confined call carries no extra line.
5. **Given** the start of a call was written and the server was killed, **When** the action is
   read as incomplete, **Then** it still states the mode and the backend.

---

### User Story 9 - Hooks and agent tasks follow the same rule (Priority: P2)

Some task code runs without an agent asking for a tool: the `[context]` task when a server
starts, and the tasks an agent calls through the endpoint that an `(agent)` task opens. Both
are confined like any call. An `(agent)` task that an agent calls runs its agent program
inside the sandbox.

**Why this priority**: The issue asks that no startup or preparation hook runs task code
before protection starts, and that changing the executor is not a way out.

**Independent Test**: Serve a Runefile whose `[context]` task writes a marker outside the
project and prints a line. Start the server: the marker is absent and the line is in the
instructions. Run an `(agent)` task as a person with a stand-in agent that calls a tool through
the endpoint: the tool's body cannot write outside the project.

**Acceptance Scenarios**:

1. **Given** a `[context]` task, **When** a server starts, **Then** the task runs confined,
   with the network only if it is marked `[network]`.
2. **Given** an `(agent)` task a person runs, **When** its agent calls a task through the
   endpoint, **Then** that task runs confined, under the mode `RUNE_SANDBOX` sets.
3. **Given** an `(agent)` task that an agent calls, **When** it runs, **Then** its agent
   program runs inside the sandbox, with the network only if the task is marked `[network]`,
   and Rune opens no endpoint and grants no loopback access for it.
4. **Given** an `(agent)` task a person runs on a host with no backend, **When** its agent
   calls a task through the endpoint, **Then** the call is refused as on any server, and Rune
   printed the warning when the endpoint opened.

---

### User Story 10 - The documentation states the rules and the limits (Priority: P3)

An operator who decides whether to trust the sandbox finds, in one place, what a confined task
can read, where it can write, how credentials reach it, what is protected, what `[network]`
gives, on which systems the sandbox works, and what it does not stop.

**Why this priority**: A security boundary nobody can check from the documentation gets
assumed to be wider than it is.

**Independent Test**: Read the sandbox section of the MCP guide. Each of the nine points of
FR-041 is answered there, and the support table names every operating system Rune ships for.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader looks for what a confined task can write,
   **Then** one list names the project, the temporary directory and the cache directory.
2. **Given** the guide, **When** a reader looks for a platform, **Then** a table says supported,
   unavailable or unsupported for it, and what each means for a call.
3. **Given** the guide, **When** a reader looks for the limits, **Then** it states that reading
   is not confined beyond the hidden locations, that a `[network]` task shares the host's
   network including its loopback services, and that a file with a second name outside the
   project is not detected.
4. **Given** the design note on Rune-only agent sessions, **When** a reader looks at the
   boundary "a task is not sandboxed", **Then** it says what this feature closes and on which
   systems.

---

### Edge Cases

- The project path contains a symbolic link, or the server was started with `--file` through
  one. The workspace is the resolved directory, and a task reaches it by either path.
- The same directory is mounted at a second path. Writes through the second path fail.
- The Runefile is in a subdirectory of a git repository. The workspace is the Runefile's
  directory. The repository's control directory is above it and is read-only like everything
  outside.
- The project is a git worktree, where `.git` is a file. The file is protected, and the
  directory it names is outside the project.
- The project has no `.git` yet. A task may create one. From the next body on it is protected.
- `.rune` does not exist. Rune creates it before the first confined body, so a task cannot
  put its own files where plans are stored.
- A file the Runefile loads is removed by a person while the server runs. The reload reports
  it as today. Nothing is protected under a name that does not exist.
- A task marked `[no-cd]`. It runs in the server's working directory for the project, as today.
- A task writes more to its temporary directory than the disk holds. The write fails as a full
  disk does anywhere. The directory is removed when the body ends.
- The temporary or cache directory cannot be created. The call is refused before anything
  runs, with the directory and the reason.
- The body reads standard input. It gets end of file, as today.
- A body starts 1,000 processes. All of them end with the body.
- A task prints without end. The output limit of spec 042 applies as today.
- The server runs inside a container that does not permit a sandbox in a sandbox. The backend
  is unavailable and calls are refused unless the server runs with `--sandbox off`.
- A body runs `rune` for a task that applies a plan, or for an `(agent)` task. The inner Rune
  can write neither the plan store nor the audit log, and reports that as it does today.
- A Runefile is saved while a confined call runs. The call ends under the rules it started
  with, and the reload protects the new set of loaded files from the next body on.
- A file named `.runefile` appears beside the `Runefile`. The next `rune` or `rune mcp` there
  fails with both names, and loads neither.
- The server's `PATH` holds a directory under a hidden location, as on NixOS, where the tools
  live under `/run`. The sandbox is unavailable there, and the reason names the directory.
- The Runefile is in the user's home directory. The credential stores of FR-023 are inside the
  workspace and stay hidden.
- The Runefile is in one of the hidden locations itself, such as `~/.docker`. The call is
  refused with that reason: hiding the location would leave the task an empty project.
- The server runs as root. A confined body has no capability.
- An embedder uses the server library with its own engine. Nothing changes for it.
- `--sandbox` is given twice. The last value is used.
- A task named `sandbox`. It is unaffected: the feature adds no command.

## Requirements *(mandatory)*

### Functional Requirements

**Where the sandbox applies**

- **FR-001**: Rune MUST run every task body that starts from an agent inside a sandbox the
  operating system enforces, unless the operator chose the unsandboxed mode. A body starts from
  an agent when it runs for a tool call on `rune mcp` or `rune serve` (stdio and Streamable
  HTTP), for a tool call on the endpoint of an `(agent)` task, or for the `[context]` task:
  the one those servers run when they start, and the one an `(agent)` task runs to prepare
  its prompt.
- **FR-002**: A task a person runs on the command line MUST run as it does today, with no
  sandbox, whatever `RUNE_SANDBOX` holds. The two exceptions belong to an `(agent)` task: the
  `[context]` task it runs for its prompt, and the calls it receives on its endpoint.
- **FR-003**: The sandbox MUST confine the whole body: the built-in shell with its built-in
  commands and redirections, interpreter bodies (Python, Node, `[script]`, a custom executor),
  bodies under a `set shell` override, `(agent)` bodies, and every process any of them starts.
  No body of an agent's call may run in the server process.
- **FR-004**: Each body of a call MUST be confined on its own: the called task, each
  dependency, each `&&` hook and each `||` hook, in the root file and in imported modules.
- **FR-005**: A call MUST NOT change what the server process can reach, and MUST NOT change
  what another call, running at the same time or later, can reach.

**Network**

- **FR-006**: A body of a task without `[network]` MUST have no network access to anything
  outside its own sandbox: no connection to any address by any protocol, no name resolution,
  no connection to a service on the host's loopback interface, and no connection to a socket in
  the host's abstract namespace. The denial MUST NOT depend on environment variables or on the
  name of a program.
- **FR-007**: A body of a task without `[network]` MUST have a loopback interface of its own,
  so that a body which starts a listener and connects to it works.
- **FR-008**: A body of a task marked `[network]` MUST have the network of the host: outbound
  connections, name resolution and the host's loopback services.
- **FR-009**: `[network]` MUST apply to the body of the task that carries it and to the
  processes that body starts. It MUST NOT apply to that task's dependencies, hooks or callers,
  and it MUST NOT change any filesystem rule.
- **FR-010**: A body that starts `rune` MUST NOT gain rights through it: the inner run stays
  inside the sandbox of the body that started it.

**Filesystem writes**

- **FR-011**: The workspace of a served Runefile is its directory, with symbolic links
  resolved. A confined body MUST be able to create, change and remove files inside the
  workspace, except the protected paths of FR-016.
- **FR-012**: A confined body MUST NOT be able to create, change or remove anything outside
  the workspace, except in the two private directories of FR-014 and on the devices of FR-024.
  This MUST hold for absolute paths, paths with `..`, symbolic links, a second mount of the
  same directory, and paths through the process filesystem.
- **FR-013**: A confined body MUST NOT be able to create a hard link from the workspace to a
  file outside it or to a protected file.
- **FR-014**: Rune MUST give each confined body a private temporary directory that is new for
  that body, holds nothing of the host or of another body, and is removed when the body ends, and each project a private cache directory
  that persists between calls. Both MUST be outside the project tree, and the body's
  environment MUST name them in `TMPDIR` and `XDG_CACHE_HOME`. The system temporary directory
  the body sees MUST be that private directory. When they cannot be created, or would lie
  inside the project, the call MUST be refused before anything runs.
- **FR-015**: When the working directory of the called task, or of any task its call can
  reach, resolves outside the workspace, the call MUST be refused before anything runs, with
  the task and the directory named.

**Protected paths**

- **FR-016**: A confined body MUST be able to read, and MUST NOT be able to change, remove,
  rename or replace: the served Runefile; every file the current load read through imports and
  modules; the file named by `set dotenv`, when it exists; `.git` in the workspace, whether a
  directory or a file; and `.rune` in the workspace, which holds Rune's cache and the stored
  plans with their approved copies.
- **FR-017**: Renaming or replacing a directory above a protected file inside the workspace
  MUST fail.
- **FR-018**: When the Runefile, a loaded file or the dotenv file has more than one name (a
  hard link count above one), or when a protected path is reached through a symbolic link
  inside the workspace, the call MUST be refused before anything runs, with the path named and
  the remedy stated. A task could change the first through its other name and replace the
  second.
- **FR-019**: Rune MUST create `.rune` in the workspace before the first confined body when it
  does not exist.
- **FR-047**: When the load looked for a file of the Runefile inside the workspace that does
  not exist (an optional import, or a candidate name of a module that was found under another
  name), the call MUST be refused before anything runs, with the path named and the remedy
  stated. A task could create the file, and the next load would read it.
- **FR-051**: A directory that holds more than one Runefile name MUST be an error wherever a
  Runefile is searched for, on the command line and for a server, with the names listed. A
  task could otherwise write a second one beside the Runefile, and the next start would
  load it.
- **FR-052**: When a server found its Runefile by searching up from a directory below the
  Runefile's, the call MUST be refused before anything runs, with both directories named: a
  task could create a nearer Runefile where the search starts. A Runefile named with `--file`
  is not searched for and is not refused.
- **FR-048**: What Rune itself reads from the repository for a call in mode `enforce` (the
  commit and whether the tree is dirty) MUST be read inside a sandbox with no network and
  nothing writable, so that a repository a task prepared cannot make Rune run a command
  outside one.
- **FR-049**: The backend program and the Rune binary MUST be used by their resolved absolute
  paths. When either lies inside the workspace or a private directory, where a task could
  replace it, the sandbox is unavailable and the reason names the path.
- **FR-020**: The directory that holds the audit logs MUST NOT be visible to a confined body.
- **FR-021**: Rune's own work for a call (the task cache, capturing and claiming a plan, the
  plan journal, the audit record) MUST keep working: it happens in the server, not in the
  body.

**What a body can read and reach**

- **FR-022**: A confined body MUST see the rest of the host's filesystem read-only, as the
  server's user can read it, except the hidden locations of FR-023.
- **FR-023**: These locations MUST be empty and read-only, or absent, for a confined body,
  also when they lie inside the workspace: the system runtime
  directory with the per-user runtime directories under it; the system temporary directories;
  the audit log directory; the socket named by `SSH_AUTH_SOCK`; the socket named by a
  `DOCKER_HOST` of the form `unix://`; and these entries of the user's home directory, when
  they exist: `.ssh`, `.gnupg`, `.aws`, `.azure`, `.kube`, `.docker`, `.config/gcloud`,
  `.config/gh`, `.netrc`, `.git-credentials`, `.npmrc`, `.pypirc` and `.password-store`.
- **FR-024**: A confined body MUST see only the null, zero, full, random, urandom and terminal
  devices with a private shared-memory and pseudo-terminal directory and the links to its own
  standard streams, and a process list that holds only its own processes. It MUST hold no
  capability, also when the server runs as root, and MUST NOT be able to gain privileges.
- **FR-025**: A confined body MUST inherit no open file or connection of the server other than
  its standard input, output and error, including files the server itself inherited from the
  program that started it. Its standard input MUST be empty, as today.
- **FR-026**: A confined body MUST have no controlling terminal of the server.
- **FR-027**: The environment of a confined body MUST be what the task gets today, with
  `TMPDIR` and `XDG_CACHE_HOME` set as in FR-014. A task's `[env]` attribute still wins over
  both.
- **FR-028**: When name resolution on the host depends on a file under a hidden location, a
  `[network]` body MUST still be able to resolve names.

**Agent tasks**

- **FR-029**: An `(agent)` task that an agent calls MUST run its agent program inside the
  sandbox, under the task's own `[network]` marking. Rune MUST NOT open a callback endpoint
  for it and MUST NOT grant it access to a loopback service.
- **FR-030**: The endpoint that an `(agent)` task opens when a person runs it MUST confine the
  calls it receives exactly as `rune mcp` does, under the mode of FR-033.

**Lifetime**

- **FR-031**: When a call is cancelled, when its deadline passes, or when the server stops,
  every process of its bodies MUST end, including processes that detached themselves. The
  server MUST stay usable after a cancellation.
- **FR-032**: When a body ends, every process it started MUST end with it.

**Modes and refusal**

- **FR-033**: The sandbox has two modes: `enforce`, the default, and `off`. `rune mcp` and
  `rune serve` MUST take the mode from `--sandbox`, or from `RUNE_SANDBOX` in the server's
  environment when the flag is absent. The endpoint of an `(agent)` task MUST take it from
  `RUNE_SANDBOX`. No Runefile setting, task attribute, dotenv entry or tool argument may change
  it. A value an `(agent)` task cannot read as a mode MUST fail that task before its agent
  program starts.
- **FR-034**: A value other than `enforce` or `off` MUST be a usage error that names the flag
  or the variable, before any Runefile code runs.
- **FR-035**: In mode `enforce`, when no backend can confine a body on this host, every tool
  call MUST be refused before any command of the call runs. The refusal MUST name the sandbox,
  the reason, and both remedies: make the backend available, or start the server with
  `--sandbox off`. The `[context]` task MUST NOT run, and the instructions MUST carry the
  notice a failed context task gives today.
- **FR-036**: Rune MUST decide availability by starting a real sandbox, not from the name or
  the version of a program. It MUST check when the server starts, and again on a call while the
  backend is unavailable, so that a backend installed later is used without a restart.
- **FR-037**: When a sandbox cannot be started for one body after the call began, that body
  MUST NOT run. The task MUST fail with an error that names the sandbox, the error MUST be in
  the result the agent gets, and no `||` hook of it runs. The next call MUST check the backend
  again.
- **FR-050**: When a confined body fails, the result MUST carry one line that says the task
  ran in the sandbox, whether it had the network, and where it could write, so that a failure
  caused by the sandbox names its cause. A body that succeeds gets no such line.
- **FR-038**: Rune MUST NOT run a body unconfined in mode `enforce`, and MUST NOT report a call
  as confined when it was not.
- **FR-039**: In mode `off`, bodies MUST run as they did before this feature, with none of the
  rules of this spec.

**What the operator sees**

- **FR-040**: The startup line of `rune mcp` and `rune serve` MUST state the sandbox:
  `sandbox: <backend>` when it confines, `sandbox: unavailable, calls are refused` when it
  cannot, and `sandbox: off, tasks run unsandboxed` in mode `off`. In the last two cases the
  server MUST also print a warning on standard error with the reason and the remedy. An
  `(agent)` task MUST print the warning, with `RUNE_SANDBOX` as the remedy, before it runs its
  `[context]` task and opens its endpoint, in either case.
- **FR-041**: The documentation MUST state, in one section: (1) what a confined body can read;
  (2) where it can write; (3) how credentials reach a task and which stores are hidden; (4)
  which paths are protected; (5) what `[network]` gives and what a task without it has; (6) the
  support table by operating system, kernel feature and backend, with what was tested; (7) the
  limits of FR-044; (8) the modes, the flag and the variable; (9) what an author changes in a
  Runefile whose tasks now fail.
- **FR-042**: The record of every tool call in the audit log MUST state whether a sandbox was
  enforced, the name of the backend that enforced it, and the mode. It MUST be written with the
  start of the call. A call refused because the sandbox was unavailable MUST have outcome
  `refused` and error kind `sandbox`.
- **FR-043**: `rune log` MUST show a line under each call that ran unsandboxed, and
  `rune log --json` MUST carry the three fields of FR-042 for every call. The line is shown only
  for a call that ran a task: a refusal ran nothing, sandboxed or not.

**Stated limits**

- **FR-044**: The documentation MUST state these limits: a confined body reads what its user
  can read outside the hidden locations; a `[network]` body shares the host's network, its
  loopback services and its abstract-namespace sockets included; a file inside the workspace
  that already has a second name outside it is the same file, and Rune does not detect it,
  except for the files of FR-018; a Unix socket in a readable location that is not hidden stays
  reachable; client configuration files inside the project are writable; and the sandbox does
  not limit CPU, memory or disk use.

**Compatibility**

- **FR-045**: The server library MUST keep working for an embedder with its own engine with no
  change in tool lists or results.
- **FR-046**: Tool lists, schemas, hints, approvals, grants, value policies, plans, output
  limits, masking and the audit log MUST behave as today for a confined call, apart from the
  rules of this spec.

### Key Entities

- **Sandbox**: The confinement the operating system applies to one task body and everything it
  starts.
- **Hint line**: The line added to the result of a confined body that failed.
- **Backend**: The mechanism that provides the sandbox on a host. On Linux it is bubblewrap
  with user namespaces. A host has a backend that works, one that is unavailable, or none.
- **Mode**: The operator's choice, `enforce` or `off`, fixed when a server starts.
- **Workspace**: The resolved directory of the served Runefile. The only project area a
  confined body can write.
- **Protected path**: A path inside the workspace that a confined body can read and cannot
  change.
- **Hidden location**: A path outside the workspace that a confined body sees as empty or
  absent.
- **Private directories**: The temporary directory of one body and the cache directory of one
  project.
- **Enforcement record**: The part of a call's audit record that states enforced or not, the
  backend and the mode.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the network scenarios, a task without `[network]` completes zero connections
  to the test service and zero name lookups, and a `[network]` task completes every one.
- **SC-002**: After every write-escape scenario, the content of the marker directory outside
  the project is byte for byte what it was, and the digest of every protected file is
  unchanged.
- **SC-003**: Each of the five ways to run code (shell redirection, shell built-in,
  interpreter, shell override, child process) is stopped in its scenario: five of five.
- **SC-004**: In every scenario where the backend is unavailable in mode `enforce`, zero
  commands of the called task run, and the refusal reaches the agent in under one second.
- **SC-005**: After a cancellation, zero processes of the call remain after two seconds, and
  the next call succeeds.
- **SC-006**: With 20 calls running at once, half of them `[network]`, every `[network]` call
  connects and no plain call does.
- **SC-007**: 100% of the audit records of tool calls state the mode, and every call that ran
  confined states its backend.
- **SC-008**: Confinement adds less than 150 ms to a task body on a supported host.
- **SC-009**: A person's command-line run of every example in the documentation produces the
  output it produced before this feature.
- **SC-010**: A reader finds the answer to each of the nine points of FR-041 in one
  documentation section.
- **SC-011**: Every rule test of the sandbox fails when the same test is run against a server
  started unsandboxed, so no test passes because of where its fixture lies.

## Assumptions

- **The workspace is the Runefile's directory.** It is where tasks run by default and what the
  issue calls the project. A Runefile in a subdirectory of a repository that writes to a
  sibling directory fails when confined. Widening the workspace is left to a later change.
- **Linux first.** The issue accepts it. macOS and Windows have no backend in this release, and
  a call there is refused unless the operator runs unsandboxed. This breaks `rune mcp` on those
  systems until the operator adds `--sandbox off`, and the release notes say so.
- **The backend is a separate program.** bubblewrap is the mature runner the issue names. Rune
  stays one static binary and does not bundle it: a host without it has no backend. This is a
  runtime dependency of the agent surface on Linux only.
- **The server starts when the sandbox is unavailable.** A server that refuses to start gives a
  client an error that does not reach the agent. A server that starts and refuses each call
  puts the reason and the remedy in the agent's hands and keeps the tool list and `rune log`
  working. This differs from the audit log of spec 052, which stops the start.
- **There are two modes and no automatic one.** A mode that confines where it can and runs
  unconfined elsewhere would be the silent fallback the issue forbids. A team with mixed
  systems sets `RUNE_SANDBOX=off` on the machines that need it.
- **The mode lives in the server's start, not in the Runefile.** The Runefile is what a task
  could try to edit. A task cannot change the arguments or the environment of the server that
  runs it.
- **Reading is not confined beyond a fixed list.** Build tools live all over the filesystem,
  and a narrow read list would break most tasks. The spec hides sockets, runtime directories
  and a fixed list of credential stores, and says that this is not secret confidentiality.
- **Credentials reach a task through the environment**, as the constitution already requires.
  A task that needs a credential file or an agent socket is run by a person, or on a server the
  operator started unsandboxed.
- **The home directory stays read-only.** Tools that write state under it fail when confined.
  The private cache directory covers tools that follow `XDG_CACHE_HOME`. For others the author
  points the tool into the workspace with `[env]`.
- **`.git` is read-only for a confined task.** The issue lists Git control data as protected.
  A task that commits, tags or fetches is run by a person.
- **A `[network]` task gets the host's network as a whole.** It is the only form of network
  access that needs no extra program. Loopback services and abstract-namespace sockets come
  with it and are documented.
- **A missing file the Runefile looks for is a refusal, a missing dotenv file is not.** A file
  of the Runefile that a task creates changes which tasks exist and what they may do. A dotenv
  file changes only the environment of bodies that are confined anyway, and a server reads its
  mode from its own environment.
- **Pre-existing hard links are detected for policy files only.** Package managers such as
  pnpm and uv link files of the project to a store outside it, so refusing every linked file
  would refuse most projects. The files that decide rights are few and are checked. The rest is
  a documented limit. This is narrower than the issue's wording and is called out for review.
- **A process ends with its body.** The issue asks that no helper is left behind. A task that
  starts a long-lived service for later calls stops working when confined.
- **An `(agent)` task called by an agent needs `[network]`** to reach its model, and an agent
  program that writes its own state under the home directory fails when confined.
- **A new error kind `sandbox` is added to the audit record**, and the sandbox object gains two
  fields. Both are additions to the contract of spec 052, whose readers ignore unknown fields.
- **The constitution gains one sentence in Principle VII** that states the default. It is
  proposed with this spec, and the maintainer's merge is the approval.
- Rune is before 1.0. The change in what `[network]` means, from a hint to a grant, is a
  breaking change for tasks that used the network without it, and the release notes call it
  out.
