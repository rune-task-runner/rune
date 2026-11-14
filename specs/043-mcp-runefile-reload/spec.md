# Feature Specification: Reload Tools When the Runefile Changes

**Feature Branch**: `043-mcp-runefile-reload`

**Created**: 2026-11-14

**Status**: Draft

**Input**: GitHub issue [#41](https://github.com/rune-task-runner/rune/issues/41)
(backlog MCP-7): "Reload tools when the Runefile changes"

## Overview

`rune mcp` reads the Runefile once, when the server starts. The tasks it finds
become the tools of the session. When the author then adds, removes or edits a
task, the server keeps serving the old ones. The agent is not told that its
tool list is stale, and the only fix is to restart the server from the client.

This feature makes a running server follow the Runefile. The server watches the
root Runefile and every file it imports or loads as a module. When one of them
changes, the server loads the Runefile again, replaces its tools, and tells
connected agents that the tool list changed:

```console
$ rune mcp
rune MCP server on stdio
reloaded Runefile: 1 tool added, 0 removed, 0 changed
```

A change that leaves the Runefile invalid does not take tools away. The server
prints the diagnostics, keeps the last valid tools, and picks up the next valid
save:

```console
Runefile:7:9: error: unknown task "biuld"
reload failed: 1 static error(s); keeping the previous tools
```

An operator who wants the tasks fixed for the life of the server starts it with
`--no-reload`.

### Out of scope

- Watching files that are not Runefiles: `.env` files, scripts a task calls,
  and cache inputs. A reload reads `.env` files again as a start does, but a
  change to one does not start a reload.
- Changing the server's flags while it runs. `--allow`, `--deny`,
  `--allow-destructive` and `--output-limit` keep the values given at start.
- The endpoint an `agent` task opens for the agent it starts. It lives for one
  task run and keeps the tasks it started with.
- Starting a server on an invalid Runefile and waiting for it to become valid.
  A server still refuses to start on static errors, as today.
- Sending changed instructions to a session that is already open. The protocol
  delivers instructions once, when a session starts.
- Resource and prompt lists. The server exposes tools only.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A new task becomes a tool without a restart (Priority: P1)

An author works with an agent connected to `rune mcp`. They add a `lint` task
to the Runefile and save. Today the agent cannot call `lint` until the client
restarts the server. With this feature the agent receives a tool-list-changed
notification within two seconds, asks for the list, and finds `lint` there.

**Why this priority**: This is the defect the issue reports. Every other story
builds on the reload it adds.

**Independent Test**: Start the server on a Runefile with one task, connect as
an MCP client, append a second task to the file, and wait for the
notification. It arrives within two seconds, the tool list then holds both
tasks, and a call to the new one runs it.

**Acceptance Scenarios**:

1. **Given** a running server and a connected client, **When** a task is added
   to the root Runefile and saved, **Then** the client receives a
   tool-list-changed notification within 2 seconds and the next tool list holds
   the new task with its description, schema and hints.
2. **Given** the same server, **When** the client calls the new tool after the
   notification, **Then** the task runs and its result is returned as for any
   other tool.
3. **Given** a running server, **When** a task is removed from the Runefile,
   **Then** the client is notified, the tool is gone from the list, and a call
   to it is answered as a call to a name no task has.
4. **Given** a running server, **When** a task's doc comment, parameters or
   attributes change so that its tool description, schema or hints differ,
   **Then** the client is notified and the list shows the new values.
5. **Given** a running server, **When** only a task's body changes, **Then**
   no notification is sent, and the next call to that tool runs the new body.
6. **Given** a running server, **When** the Runefile is saved with no change
   to its content, or with a change to comments and blank lines only, **Then**
   no notification is sent.
7. **Given** a server on the HTTP transport with two connected sessions,
   **When** a task is added, **Then** both sessions receive the notification.
8. **Given** a server started on a Runefile with no exposable task, **When** a
   task is added, **Then** the client is notified and the task is listed. The
   server states tool-list-change support from the start.
9. **Given** an editor that saves by writing a temporary file and renaming it
   over the Runefile, **When** the author saves, **Then** the reload happens as
   for an in-place write, and later saves keep working.

---

### User Story 2 - A broken save keeps the last valid tools (Priority: P1)

The author saves the Runefile half-way through an edit, with a syntax error or
a dependency on a task that does not exist yet. The agent must not lose its
tools because of it. The server keeps the tools of the last valid Runefile,
shows the author what is wrong on stderr, and applies the next valid save.

**Why this priority**: Authors save broken files all the time while editing. A
reload that emptied the tool list on each of those saves would be worse than no
reload.

**Independent Test**: Start the server on a valid Runefile, connect, then
overwrite the file with one that has a static error. No notification arrives,
the tool list is unchanged, calls still run the old tasks, and stderr holds the
diagnostic with its `file:line:col`. Then save a valid file with a new task:
the notification arrives and the new task is listed.

**Acceptance Scenarios**:

1. **Given** a running server, **When** the Runefile is saved with a syntax
   error, **Then** no notification is sent, the tool list is what it was, and
   a call to an existing tool runs the task as it was before the save.
2. **Given** the same save, **When** stderr is read, **Then** it holds the
   same diagnostics a start on that file prints, each with `file:line:col` and
   a caret span, followed by one line saying the reload failed and the
   previous tools are kept.
3. **Given** a save with an error only the analyzer finds (an unknown
   dependency, a dependency cycle, an arity mismatch) or an invalid setting,
   **When** the server reloads, **Then** the outcome is the same as for a
   syntax error.
4. **Given** a save that raises the Runefile's minimum version above the
   running Rune, **When** the server reloads, **Then** the reload fails the
   same way and the previous tools are kept.
5. **Given** a failed reload, **When** the author saves a valid Runefile,
   **Then** the server reloads it, notifies the client if the tool list
   differs from the last valid one, and prints the success line.
6. **Given** a failed reload, **When** the author saves another invalid
   Runefile, **Then** the new diagnostics and the failure line are printed
   again and the previous tools are still kept.
7. **Given** a running server, **When** the root Runefile is deleted or cannot
   be read, **Then** the reload fails, the previous tools are kept, stderr says
   why, and the server reloads when the file is back.
8. **Given** a failed reload, **When** it is over, **Then** no task code has
   run because of it: neither a task nor the `[context]` hook.

---

### User Story 3 - Imported and module files are followed too (Priority: P2)

A project splits its tasks across files: the root Runefile imports
`ci.rune` and loads a `deploy` module. The author adds a task to one of
those files. The agent must see it, as it would for a task added to the root
file.

**Why this priority**: Projects that use imports and modules get no benefit
from the first two stories without this. It is second only because a single
Runefile is the common case.

**Independent Test**: Start the server on a Runefile that imports one file and
loads one module, connect, and add a task to the imported file, then to the
module file. Each save produces a notification within two seconds and the new
tool is listed, the module's task under its namespaced name.

**Acceptance Scenarios**:

1. **Given** a root Runefile that imports `ci.rune`, **When** a task is
   added to `ci.rune`, **Then** the client is notified within 2 seconds and
   the task is listed.
2. **Given** a root Runefile that loads module `deploy`, **When** a task
   `push` is added to the module's file, **Then** the client is notified and
   the tool `deploy__push` is listed.
3. **Given** an imported file that itself imports another file, **When** the
   innermost file changes, **Then** the server reloads.
4. **Given** a running server, **When** the root Runefile gains an import of a
   file that was not part of the graph, **Then** the tasks of that file are
   listed after the reload, and later changes to that file start a reload.
5. **Given** a running server, **When** an import is removed from the root
   Runefile, **Then** the tasks of that file leave the tool list, and later
   changes to that file do not start a reload.
6. **Given** a save that adds an import of a file with a static error,
   **When** the reload fails and the author then fixes only the imported file,
   **Then** the server reloads and lists its tasks.
7. **Given** a save that adds a required import of a file that does not exist,
   **When** the reload fails and the author then creates that file in an
   existing directory, **Then** the server reloads.
8. **Given** an optional import (`import?`) of a file that does not exist,
   **When** the author creates that file in an existing directory, **Then** the
   server reloads and lists its tasks.
9. **Given** a file in the Runefile's directory that is not part of the graph,
   **When** it changes, **Then** the server does not reload.

---

### User Story 4 - New sessions get fresh project context (Priority: P3)

A Runefile has a `[context]` task whose output the server hands to each agent
as instructions when a session starts. On the HTTP transport, sessions start
at any time. After a reload, a session that starts must get instructions from
the current Runefile, not from the one the server started with.

**Why this priority**: It matters only for servers with a `[context]` hook that
serve more than one session. The reload is correct for tools without it.

**Independent Test**: Start an HTTP server on a Runefile whose `[context]` task
prints `v1`. Connect one session and read its instructions: `v1`. Change the
task to print `v2` and wait for the success line. Connect a second session: its
instructions are `v2`.

**Acceptance Scenarios**:

1. **Given** an HTTP server and a successful reload, **When** a new session
   starts, **Then** its instructions are the output of the `[context]` hook of
   the reloaded Runefile, masked and capped as at start.
2. **Given** a reload that adds a `[context]` task where there was none,
   **When** a new session starts, **Then** it receives that task's output as
   instructions. A reload that removes the `[context]` task leaves new sessions
   with no instructions.
3. **Given** a reload after which the `[context]` hook fails or times out,
   **When** a new session starts, **Then** it receives the same one-line
   failure notice a start gives, stderr holds the same warning, and the tools
   are still replaced.
4. **Given** a failed reload, **When** a new session starts, **Then** it
   receives the instructions of the last valid Runefile, and the hook was not
   run.
5. **Given** a server on stdio, **When** the Runefile is reloaded, **Then**
   the `[context]` hook is not run again: the one session already has its
   instructions and no other session can start.

---

### User Story 5 - An operator can freeze the tasks (Priority: P3)

An operator runs an agent that can write files in the project. They chose
`--allow` and `--allow-destructive` after reading the Runefile, and they do not
want what those names run to change while the server is up. They start the
server with `--no-reload` and get the behavior of earlier versions.

**Why this priority**: Without the flag there is no way to keep the earlier
guarantee that the tasks a server runs are the ones it read at start. Most
users want the reload, so it is the default and this is last.

**Independent Test**: Start the server with `--no-reload`, connect, add a task
to the Runefile, and wait three seconds. No notification arrives, the tool list
is unchanged, and stderr holds no reload line.

**Acceptance Scenarios**:

1. **Given** a server started with `--no-reload`, **When** the Runefile or a
   file it imports changes, **Then** the server does not reload, sends no
   notification, and calls run the tasks as read at start.
2. **Given** a server started with `--no-reload` on either transport, **When**
   it starts, **Then** its startup output is what it is without the flag.

---

### Edge Cases

- Several saves in quick succession, or one save that the system reports as
  several file events, lead to one reload of the final content.
- A call that is running when a reload happens finishes with the task
  definitions it started with. The reload neither waits for it nor cancels it.
  Calls that start after the reload use the new definitions.
- A file changes while a reload is in progress. The server reloads again
  afterwards, so the tools end up matching the latest content.
- `--allow`, `--deny` or `--allow-destructive` names a task that a reload
  removed. The server keeps running, the name has no effect, and stderr says
  so once for that reload. If a later reload brings the name back, the flag
  applies to it again.
- A reload leaves no task exposed (all removed, or all hidden by the filters).
  The tool list is empty and the server keeps running.
- A task named by `--allow-destructive` loses `[confirm]` in a reload. The
  grant has no effect, as at start, and stderr says so.
- A reload changes `set mcp_output_limit`. Calls that start after the reload
  use the new value unless `--output-limit` was given. A reload changes other
  settings (shell, working directory, `.env` loading, secrets to mask): calls
  that start after it use the new values.
- The watched directory is removed, or the system cannot watch files at all.
  The server reports the problem on stderr once and keeps serving the tools it
  has. It does not exit.
- Environment variables of the server process do not change with a reload.
  Values a reload reads from the environment are the ones the server started
  with.
- The client does not support the tool-list-changed notification. The server
  still replaces its tools, and the client sees them the next time it asks for
  the list.
- A reload fails, then the file is saved back to exactly the last valid
  content. The reload succeeds, the success line is printed, and no
  notification is sent because the tool list did not change.

## Requirements *(mandatory)*

### Functional Requirements

**Watching**

- **FR-001**: A running `rune mcp` server, on stdio and on HTTP, MUST watch the
  root Runefile it was started on and every file that Runefile imports or loads
  as a module, directly or through other imported or module files.
- **FR-002**: The watched set MUST follow the Runefile: after a successful
  reload it is the set of files the reloaded Runefile is made of.
- **FR-003**: After a failed reload the watched set MUST also hold every file
  the failed load read or tried to read, so that fixing or creating one of
  them starts a reload. A named file that does not exist is covered too, also
  when its directory does not exist yet.
- **FR-004**: A change to a file outside the watched set MUST NOT start a
  reload.
- **FR-005**: A save made by replacing the file (write to a temporary file,
  then rename) MUST be detected like an in-place write, and the file MUST stay
  watched afterwards.
- **FR-006**: Changes that arrive close together MUST be combined into one
  reload. A change that arrives while a reload runs MUST lead to another
  reload after it.
- **FR-007**: The root Runefile's path is fixed when the server starts. A
  reload reads that path and does not search for a Runefile again.
- **FR-008**: If watching cannot start or stops working, the server MUST say
  so once on stderr and keep serving its current tools.

**Reloading**

- **FR-009**: A reload MUST load the Runefile through the same steps as a
  start: minimum-version check, parsing, composition of imports and modules,
  static analysis, and resolution of settings.
- **FR-010**: When those steps succeed, the server MUST replace its task
  definitions, settings and derived state (working directory, environment from
  `.env` files, secrets to mask) with the reloaded ones. Calls that start
  after the replacement MUST use them.
- **FR-011**: A call in progress during a reload MUST complete with the
  definitions it started with. A reload MUST NOT cancel or delay a call, and a
  call MUST NOT observe a mix of old and new definitions.
- **FR-012**: The values of `--allow`, `--deny`, `--allow-destructive` and
  `--output-limit` MUST apply to the reloaded tasks exactly as they apply at
  start: the same tasks are exposed, hidden, granted and refused for the same
  names.
- **FR-013**: A name given to `--allow`, `--deny` or `--allow-destructive`
  that no task of the reloaded Runefile has MUST NOT fail the reload or stop
  the server. The server MUST print one warning per such name for that reload.
  The warnings a start prints for a grant on a task without `[confirm]` and
  for a filter naming a task that is never exposed MUST be printed for a
  reload too.
- **FR-014**: A tool whose task a reload removed or hid MUST be answered as a
  call to a name no task has.
- **FR-015**: A successful reload MUST print one line on stderr that says the
  Runefile was reloaded and how many tools were added, removed and changed.

**Notification**

- **FR-016**: When a successful reload changes the tool list, the server MUST
  send a tool-list-changed notification to every connected session. The tool
  list is changed when a tool is added, a tool is removed, or a tool's name,
  description, input schema or hints differ.
- **FR-017**: When a successful reload does not change the tool list, the
  server MUST NOT send the notification.
- **FR-018**: The notification MUST be sent within 2 seconds of the save that
  caused it, for a Runefile that loads in under one second.
- **FR-019**: After the notification, a request for the tool list MUST return
  the reloaded tools. A tool list MUST be either the
  list before the reload or the list after it, never part of each.
- **FR-020**: The server MUST state in its capabilities that its tool list can
  change, from the start of every session, including when it starts with no
  tool and when no filter is given. With `--no-reload` the capabilities MUST
  be what they are today.

**Failed reloads**

- **FR-021**: When a reload finds static errors, an unmet minimum version, an
  invalid setting, or cannot read a file it needs, the server MUST keep the
  tools, definitions, settings and instructions of the last successful load,
  and MUST NOT send a notification.
- **FR-022**: A failed reload MUST print to stderr the diagnostics a start on
  the same files prints, with `file:line:col` and caret spans, then one line
  saying the reload failed and the previous tools are kept.
- **FR-023**: A failed reload MUST NOT run any task code, including the
  `[context]` hook.
- **FR-024**: A failed reload MUST NOT stop the server or change its exit
  status. The next change to a watched file MUST start a new reload.
- **FR-025**: Nothing a reload prints MUST go to stdout on the stdio transport.
  Stdout carries the protocol.

**Instructions**

- **FR-026**: On the HTTP transport, a successful reload MUST run the
  `[context]` hook of the reloaded Runefile once, with the same timeout,
  masking and size cap as at start, and sessions that start after it MUST
  receive that output as their instructions.
- **FR-027**: A `[context]` hook that fails or times out during a reload MUST
  degrade as at start: the failure notice becomes the instructions, a warning
  goes to stderr, and the tools are still replaced.
- **FR-028**: A session that started before a reload MUST NOT be sent new
  instructions.
- **FR-029**: On stdio the `[context]` hook MUST NOT be run by a reload.

**Control and compatibility**

- **FR-030**: `rune mcp` MUST accept `--no-reload`. With it the server MUST
  NOT watch any file and MUST behave as before this feature.
- **FR-031**: A server whose Runefile files do not change MUST list the same
  tools and return the same results as before this feature. Its startup output
  MUST be unchanged.
- **FR-032**: A server MUST still refuse to start on a Runefile with static
  errors, with the same output and exit status as today.
- **FR-033**: The endpoint an `agent` task opens MUST NOT watch or reload.
- **FR-034**: Secrets MUST stay masked in everything a reload prints or
  exposes: diagnostics, tool descriptions, schemas and instructions, by the
  same rules as at start, using the secrets of the reloaded Runefile.
- **FR-035**: The embeddable server library MUST let an embedder replace the
  tasks and the instructions of a running server, with the notification rules
  of FR-016, FR-017 and FR-019. Watching files is done by `rune mcp`, not by
  the library.
- **FR-036**: The reload, the stderr lines and `--no-reload` MUST be
  documented with the other `rune mcp` behavior and flags, and the release
  notes MUST say that a running server now follows the Runefile and how to
  turn that off.
- **FR-037**: An automated end-to-end test MUST start the real `rune mcp`
  binary, connect as a client, change the Runefile, and assert the
  notification and the new tool list. It stands for
  `TestMCPE2E_Pending_RunefileReload` of the issue, which is not in this
  repository.

### Key Entities

- **Watched graph**: The root Runefile plus every file it imports or loads as
  a module, transitively. After a failed reload it also holds the files that
  load reached.
- **Loaded Runefile**: The result of one successful load: task definitions,
  settings, working directory, environment and secrets. A server holds exactly
  one at a time, and each call uses the one that was current when it started.
- **Tool list**: What an agent sees: for each exposed task a name, a
  description, an input schema and hints. Derived from the loaded Runefile and
  the server's filters and grants.
- **Reload**: One attempt to replace the loaded Runefile after a change. It
  either succeeds and replaces everything, or fails and replaces nothing.
- **Instructions**: The `[context]` hook's output given to a session when it
  starts.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After a task is added to any file of the watched graph, a
  connected agent is notified within 2 seconds and finds the task in its tool
  list, with no restart.
- **SC-002**: Across saves with a syntax error, an analysis error, an invalid
  setting, an unmet minimum version and a deleted file, the tool list and the
  behavior of existing tools stay exactly as they were, in every case.
- **SC-003**: Every failed reload shows the author the same diagnostics as
  running Rune on that file, and the server recovers on the next valid save
  without being restarted.
- **SC-004**: Ten saves within one second cause at most two reloads and at
  most two notifications, and the final tool list matches the final content.
- **SC-005**: A server whose files do not change behaves exactly as before
  this feature: same startup output, same tool list, same results.
- **SC-006**: With `--no-reload`, no file change causes a reload or a
  notification for the life of the server.
- **SC-007**: After a reload on HTTP, every new session receives instructions
  produced from the current Runefile.

## Assumptions

- **Reload is on by default.** The issue asks for a server that follows the
  Runefile, and a stale tool list is the reported defect. `--no-reload`
  restores the earlier behavior.
- **Why `--no-reload` exists.** Before this feature, the tasks a server ran
  were the ones it read at start, even if an agent with write access edited
  the Runefile afterwards. Reload removes that property: an agent that can
  write the Runefile can change what an allowed or granted name runs, or add a
  task and call it. `[confirm]` tasks still need an operator grant by name,
  and `--allow` still limits which names are tools. Operators who rely on the
  earlier property use the flag. An agent that can write files could already
  change the scripts a task calls, so the default follows the issue.
- **A notification is sent only when the tool list differs.** A body-only
  edit changes what a call runs but not what the agent sees, so notifying
  would only cost the agent a list request.
- **Unknown names in flags do not fail a reload.** At start they are usage
  errors, because the operator is there to fix the command. During a session,
  stopping the server because the author renamed a task would take every tool
  away, which is what story 2 forbids. A warning is printed instead.
- **Flags are fixed, settings follow the file.** Flags are the operator's
  choice for this server. Settings are part of the Runefile, so they reload
  with it.
- **The `[context]` hook reruns on HTTP only.** On stdio there is one session
  and it already has its instructions, so rerunning the hook on every save
  would run task code for nothing.
- **`.env` files are not watched.** The issue names the Runefile and its
  import and module graph. A reload reads `.env` files again because it
  repeats the start's load.
- **Two seconds assumes a normal Runefile.** The bound covers the wait that
  combines close changes plus the load. A Runefile that takes longer than a
  second to load is notified as soon as its load finishes. The `[context]`
  hook on HTTP runs after the tools are replaced and does not delay the
  notification. The success line is printed after the hook, so it also means
  that new sessions get the new instructions.
- **A missing file in a missing directory is waited for.** The server
  watches the nearest directory that exists above it, so creating the
  directory starts a reload, and that reload watches the new directory.
- **Not a breaking change.** A server whose files do not change behaves as
  before. The release notes still describe the new behavior and the flag.
