# Feature Specification: Set Up an Agent Client for a Rune-only Workflow with `rune agent init`

**Feature Branch**: `055-agent-init`

**Created**: 2026-11-18

**Status**: Draft

**Input**: GitHub issue [#53](https://github.com/rune-task-runner/rune/issues/53)
(backlog ADOPT-2): "Add rune agent init claude|codex|vscode for guided MCP setup", with the
findings of the Rune-only agent mode research (issue #46, spec 047) posted on that issue.

## Overview

Registering `rune mcp` with an agent client adds Rune's tasks to the agent's tools and takes
nothing away: the client's own shell is still there. Removing the shell, allowing Rune's tools
and starting the server for the right project are client-specific settings that a person
merges by hand today. This feature adds `rune agent init <client>`, which writes those
settings into the project's own files, shows the change first when asked, and ends by saying
what the setup reached.

```text
$ rune agent init claude
.mcp.json: created: server "rune" runs: rune mcp --file /home/me/shop/Runefile --no-reload
.claude/settings.json: created: deny Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger; allow mcp__rune
client: Claude Code 2.1.326 (checked: 2.1.325, 2.1.326)
outcome: Rune-only, pending 2 steps
step: trust the project: start claude in /home/me/shop and accept the trust question. Until then the deny rules apply and the allow rule does not: the session has no shell and Rune's tools are refused.
step: approve the server "rune" when Claude Code asks, and start a new session. Until then the session has no Rune tools.
```

The notes and the manual check that follow are left out here. The exact text of every line is
in [contracts/rune-agent-init.md](contracts/rune-agent-init.md).

| Question | Today | After |
|---|---|---|
| How do I give an agent Rune's tasks and no shell? | Read the design note and merge JSON or TOML by hand | `rune agent init claude`, `codex` or `vscode` |
| Will it damage the configuration I already have? | A hand merge can | Other servers, other settings, comments and existing deny rules are kept. A value that is already there is never changed without `--force` |
| What will it change? | Unknown until it is done | `--dry-run` prints the difference and writes nothing |
| Is the agent really without a shell now? | Easy to assume, hard to know | The command names one of three outcomes and every step that is left. It never says "Rune-only" for a client or version nobody checked |
| Does it touch my user-wide settings? | A hand setup often does | Never. It writes only inside the project |

The three clients are Claude Code, Codex CLI and VS Code with Copilot agent mode. What each can
reach is taken from the research of spec 047, recorded in
[the design note](../../docs/design/rune-only-agent-mode.md):

| Client | Files written in the project | Strongest outcome |
|---|---|---|
| Claude Code | `.mcp.json`, `.claude/settings.json` | Rune-only |
| Codex CLI | `.codex/config.toml` | Rune-only |
| VS Code | `.vscode/mcp.json`, `.mcp.json`, `.github/agents/rune.agent.md` | Rune-only within the custom agent `rune`, once a VS Code version has been checked. Until then, Registered only |

### The three outcomes

The words are those of the
[setup outcome contract](../047-rune-only-agent-mode/contracts/setup-outcome.md) of spec 047,
and no other word is used for a setup:

- **Rune-only**: every session the client starts in the project has Rune's tools and no
  command-running tool.
- **Rune-only within SCOPE**: the same, inside one named custom agent. Sessions outside it keep
  their command-running tools.
- **Registered only**: Rune's tools are there, and so are the client's command-running tools.

A step the person still has to take, such as trusting the project, is part of the outcome: it
is reported as pending that step.

### Out of scope

- A command that removes a setup. The documentation says which entries are Rune's and how to
  take out only those.
- Cursor and Zed. Neither can remove its shell from a project file.
- Changing any file outside the project: user settings, the client's trust records, an
  organization's managed settings.
- Starting the client, starting the server, or calling a model. The command reads and writes
  files and asks the client for its version.
- Passing `--allow`, `--deny` or `--allow-destructive` to the registered server. A person who
  wants them edits the registration; the command then reports it as different from its own.
- Generating an `agent_cmd` for the `(agent)` executor. The documentation gains the working
  example from the research.
- Reading the client's trust state to decide whether a step is still open. The step is always
  named.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Set up Claude Code or Codex CLI as Rune-only in one command (Priority: P1)

A developer in a project with a Runefile runs `rune agent init claude` or
`rune agent init codex`. The command writes the client's project files: the server is
registered for this project, the client's command-running tools are removed, and Rune's tools
are allowed. It then names the outcome and the steps left to the developer.

**Why this priority**: these are the two clients that can remove their shell from a project
file, and both were seen doing it. This story alone delivers the feature.

**Independent Test**: In a project with a Runefile and no client files, run the command for
each client. The files exist with the documented content, they are the files the research
observed working (runs c12 and y5) apart from the server's arguments, and the report names the
outcome and the pending steps.

**Acceptance Scenarios**:

1. **Given** a project with a Runefile and no `.mcp.json` or `.claude` directory, **When** the
   person runs `rune agent init claude`, **Then** `.mcp.json` registers a stdio server named
   `rune`, `.claude/settings.json` denies `Bash`, `PowerShell`, `Monitor`, `Workflow`,
   `CronCreate` and `RemoteTrigger` and allows `mcp__rune`, and the report says "Rune-only"
   pending the trust and approval steps.
2. **Given** a project with a Runefile and no `.codex` directory, **When** the person runs
   `rune agent init codex`, **Then** `.codex/config.toml` sets `shell_tool = false` under
   `[features]` and `enabled = false` under `[agents]` and registers the server under
   `[mcp_servers.rune]`, and the report says "Rune-only" pending the trust step.
3. **Given** either client, **When** the files are written, **Then** the registered command
   is `rune mcp` with the absolute path of this project's Runefile and `--no-reload`, so the
   server serves this project whatever directory the client starts it in.
4. **Given** a project directory whose path contains spaces, **When** the command runs,
   **Then** the written files are valid and the registered path is that directory's Runefile.
5. **Given** the person runs the command in a subdirectory of the project, **When** the
   Runefile is found in a parent directory, **Then** the files are written in the Runefile's
   directory.
6. **Given** a directory with no Runefile in it or above it, **When** the command runs,
   **Then** nothing is written and the command exits 2 with the usual "no Runefile found"
   message.
7. **Given** a Runefile that does not pass analysis, **When** the command runs, **Then** the
   diagnostics are printed, nothing is written and the command exits 3: a server registered
   for it would not start.

---

### User Story 2 - Keep the configuration that is already there (Priority: P1)

A developer whose project already has client files runs the command. Other servers, other
settings, comments, key order and formatting stay as they were. Existing deny rules stay. A
second run changes nothing. Where the file already holds a different value for something the
command sets, the command stops and says where.

**Why this priority**: a setup command that damages a team's configuration, or weakens a
restriction, will not be run twice. Merging is the reason the command writes files and does
not print a block to paste.

**Independent Test**: Run the command against fixtures with existing servers, unknown keys,
comments, deny rules and conflicting values. Compare the result byte for byte outside the
entries the command owns, run it again, and check that the second run writes nothing.

**Acceptance Scenarios**:

1. **Given** a `.mcp.json` with another server and keys the command does not know, **When**
   the command runs, **Then** the server `rune` is added and every other byte of the file is
   unchanged.
2. **Given** a `.claude/settings.json` whose `permissions.deny` already holds `Bash(rm *)` and
   `WebFetch`, **When** the command runs, **Then** both rules stay, the six tool names are
   added after them, and no rule is removed or moved to another list.
3. **Given** a `.claude/settings.json` whose `permissions.deny` already holds some of the six
   names, **When** the command runs, **Then** only the missing ones are added and none is
   written twice.
4. **Given** a `.codex/config.toml` with comments, other servers, and other keys under
   `[features]`, **When** the command runs, **Then** every comment and every other key is
   unchanged and the new keys are inside the tables they belong to.
5. **Given** a `.vscode/mcp.json` with comments, **When** the command runs, **Then** the
   comments are kept.
6. **Given** a setup the command already wrote, **When** the command runs again with the same
   options, **Then** every file is reported as unchanged, no file is written, and the outcome
   and exit status are those of the first run.
7. **Given** a `.mcp.json` whose server `rune` has other arguments, **When** the command runs,
   **Then** nothing is written, the message names the file, the line, the value found and the
   value the command would write, and the command exits 2.
8. **Given** the same file and `--force`, **When** the command runs, **Then** the command,
   type and arguments of the server `rune` are replaced, and any other key of that entry, such
   as `env`, is kept.
9. **Given** a `.codex/config.toml` with `shell_tool = true` under `[features]`, **When** the
   command runs without `--force`, **Then** nothing is written and the message names the line.
10. **Given** a `.claude/settings.json` whose `permissions.deny` holds `mcp__rune`, **When**
    the command runs, with or without `--force`, **Then** nothing is written and the message
    says that the rule denies every Rune tool and that the command never removes a deny rule.
11. **Given** a client file that cannot be parsed, **When** the command runs, **Then** nothing
    is written, the message names the file, line and column, and the command exits 3.
12. **Given** a conflict in one of the two files a client needs, **When** the command runs,
    **Then** neither file is written.

---

### User Story 3 - Be told what the setup reached, and nothing more (Priority: P1)

A developer reads the last lines of the report to learn whether the agent has a shell. The
report names one of the three outcomes. It names each step that is left. For a client version
that was not checked, and for VS Code until a version has been checked, it says "Registered
only" and names the tools that may remain.

**Why this priority**: a false "Rune-only" is the failure this feature exists to prevent. A
person who believes the shell is gone stops watching for it.

**Independent Test**: Run the command with the client's version reported as a checked version,
an unchecked version, and not found. The outcome line is "Rune-only" only in the first case.
The words "restricted", "locked down" and "safe" appear in no report and not in the command's
help.

**Acceptance Scenarios**:

1. **Given** Claude Code in a checked version, **When** the setup is written, **Then** the
   outcome is "Rune-only" pending two named steps: trusting the project, and approving the
   server, and the report says what a session has before each: until the project is trusted
   Rune's tools are refused, and until the server is approved the session has none.
2. **Given** Codex CLI in a checked version, **When** the setup is written, **Then** the
   outcome is "Rune-only" pending one named step: trusting the project, and the report says
   that until then Codex ignores the file, so the session has its shell and no Rune tools.
3. **Given** a client version that is not one of the checked versions, **When** the setup is
   written, **Then** the files are still written, the outcome is "Registered only", the
   report names the version found, the checked versions and each command-running tool that
   was not checked, and the command exits 1.
4. **Given** the client is not installed or does not report a version, **When** the setup is
   written, **Then** the outcome is "Registered only" for the same reason and the report says
   that no version was found.
5. **Given** `rune agent init vscode`, **When** the setup is written, **Then** the outcome is
   "Registered only", the report says that no VS Code version has been checked with these
   files, that the custom agent `rune` lists only Rune's tools, and that every other agent
   keeps the terminal tools, which it names.
6. **Given** any outcome, **When** the report is printed, **Then** it says that the server
   keeps the tasks read at start and that a person who edits the Runefile starts a new
   session, and it says that `[confirm]` tasks are not approved by this setup.
7. **Given** any outcome, **When** the report is printed, **Then** it ends with the manual
   check for that client: the command to run and what to look for in its output.

---

### User Story 4 - See the change before it is made, and check a setup later (Priority: P2)

A developer wants to review what the command would do. `--dry-run` prints the difference for
each file and the report, and writes nothing. Later, `--check` says whether the project's
files still hold the setup. It reads the files and nothing else: it writes nothing and starts
no program, so it gives the same answer on a machine without the client.

**Why this priority**: the files are reviewed like code and committed. A reviewable change and
a check that can run in CI make the setup maintainable, but the setup works without them.

**Independent Test**: Run with `--dry-run` on a fixture and compare the directory before and
after: identical. Apply the printed difference by hand and compare with a real run: identical.
Run `--check` on a complete setup and on one with a rule removed.

**Acceptance Scenarios**:

1. **Given** any project, **When** the command runs with `--dry-run`, **Then** a difference
   for each file that would change is printed on standard output, the report is printed on
   standard error, and no file or directory is created or changed.
2. **Given** `--dry-run` and a conflict, **When** the command runs, **Then** the conflict is
   reported as in a real run, with the same exit status.
3. **Given** a complete setup, **When** the command runs with `--check`, **Then** each entry
   is reported as present, the manual check is printed, nothing is written, no program is
   started, and the command exits 0, whether or not the client is installed.
4. **Given** a setup from which one deny rule was removed, **When** the command runs with
   `--check`, **Then** the missing rule is named with its file, nothing is written, and the
   command exits 1.
5. **Given** a project the command never set up, **When** the command runs with `--check`,
   **Then** every entry is reported as missing and the command exits 1.
6. **Given** a setup whose `permissions.deny` gained the rule `mcp__rune`, **When** the command
   runs with `--check`, **Then** the rule is reported as in a real run and the command exits 1.

---

### User Story 5 - Set up VS Code with a custom agent that has only Rune's tools (Priority: P2)

A developer who uses Copilot agent mode in VS Code runs `rune agent init vscode`. The command
registers the server for both ways VS Code runs an agent and writes a custom agent named
`rune` whose tool list holds only Rune's tools. The report is plain that this is one agent's
setting and that it has not been checked in any VS Code version.

**Why this priority**: VS Code has no workspace setting that removes the terminal, so the best
it offers is narrower than the other two and is not verified. It is still the documented
registration plus the one restriction the client has.

**Independent Test**: Run the command in an empty project and read the three files: the server
entry in `.vscode/mcp.json` under `servers`, the same server in `.mcp.json` under
`mcpServers`, and an agent file whose `tools` list has exactly one entry and whose `agents`
list is empty.

**Acceptance Scenarios**:

1. **Given** a project with a Runefile, **When** the person runs `rune agent init vscode`,
   **Then** `.vscode/mcp.json` and `.mcp.json` register the server `rune`, and
   `.github/agents/rune.agent.md` has `tools: ['rune/*']` and `agents: []`.
2. **Given** the agent file is written, **When** its `tools` list is read, **Then** it is
   never empty and never missing.
3. **Given** a `.github/agents/rune.agent.md` with other content, **When** the command runs
   without `--force`, **Then** nothing is written and the message names the file.
4. **Given** a project already set up for Claude Code, **When** the person runs
   `rune agent init vscode` with the same options, **Then** the server in `.mcp.json` is
   reported as unchanged, and the Claude Code settings are untouched.

---

### User Story 6 - Commit the setup for a team (Priority: P2)

A maintainer wants the client files in the repository so that every checkout has the setup.
An absolute path would be wrong on every other machine. `--unpinned` registers `rune mcp`
without the Runefile's path, and the report says what that relies on.

**Why this priority**: project files are meant to be shared. The default pins the server to
this checkout, which the issue asks for; a team needs the other form, and needs to be told its
cost.

**Independent Test**: Run with `--unpinned` and read the registered arguments: no path. The
report has the line that says where the server looks for the Runefile.

**Acceptance Scenarios**:

1. **Given** `--unpinned`, **When** the setup is written, **Then** the registered command is
   `rune mcp --no-reload` with no path, and the report says that the server serves the
   Runefile found from the directory the client starts it in.
2. **Given** a setup written with the path, **When** the command runs with `--unpinned` and
   no `--force`, **Then** the difference is reported as a conflict, as for any other value.
3. **Given** no `--unpinned`, **When** the setup is written, **Then** the report says that the
   path is this checkout's, and names `--unpinned` for a file that is committed.

---

### Edge Cases

- A client file exists and is empty: it is treated as a file with no settings, and the result
  is the file a new project would get.
- A client file uses Windows line endings: the lines the command adds use them too.
- A client file starts with a UTF-8 byte order mark: it is edited as any other file and keeps
  the mark.
- The path of the Runefile is not valid UTF-8: it cannot be written into a JSON or TOML file
  as it is. A usage error that names `--unpinned`.
- `.claude`, `.codex`, `.vscode` or `.github` is a symbolic link to a directory outside the
  project, or a client file is such a link: nothing is written through it. The message names
  the path and where it leads. This is what keeps the command from changing user-wide
  settings by accident.
- A client file cannot be read, or cannot be written because its directory cannot be created
  or written: no file of that client is left changed, and the message names the file.
- `.github/agents/rune.agent.md` has the content the command writes with other line endings:
  it is the same file, and it is left as it is.
- `--unpinned` with a Runefile whose name Rune does not find by itself, such as
  `--file tasks.rune`: a usage error, because a server registered without a path would not
  find it.
- The value where the command expects a table or a list has another type, such as
  `"permissions": "none"` or `"mcpServers": []`: a conflict, with the line.
- A `.codex/config.toml` sets the values in a form the command cannot extend in place, such as
  an inline table for `mcp_servers`: the command writes nothing and prints the setting to add
  by hand.
- `permissions.deny` holds a rule for one Rune tool, such as `mcp__rune__deploy`: it is kept,
  and the report notes that the tool stays denied. The outcome is not changed by it.
- `permissions.ask` holds a rule for Rune's tools: it is kept, and the report notes that the
  client will ask before those calls.
- The Runefile has a task named `agent`: `rune agent` is the command. `rune agent` and
  `rune agent init` print a note that the task is run with `rune -- agent`. `rune agent`
  followed by anything but `init` is a usage error that names `rune -- agent`: it never
  succeeds without doing what was asked.
- `--check` with `--force`, or `--check` with `--dry-run`: a usage error.
- An unknown client name, or none: a usage error that lists the three names.
- The client's version command hangs: it is stopped after a short time and the version is
  reported as not found.
- The project is on a drive or path with characters that need escaping in JSON or TOML, such
  as a backslash or a quotation mark: the written files are valid and hold the same path.
- Two runs for different clients share `.mcp.json`: the second finds the entry the first
  wrote and reports it as unchanged.

## Requirements *(mandatory)*

### Functional Requirements

**The command**

- **FR-001**: Rune MUST provide `rune agent init <client>`, where the client is `claude`,
  `codex` or `vscode`. Any other name, or none, MUST be a usage error that lists the three.
- **FR-002**: The command MUST find the Runefile as other commands do: from the working
  directory upward, or the one named with `--file`. The directory of that Runefile is the
  project, and every file the command writes is inside it.
- **FR-003**: The command MUST stop before writing when no Runefile is found (exit 2) or the
  Runefile does not pass analysis (exit 3, with the diagnostics).
- **FR-004**: The command MUST NOT run a task, a hook, a `[context]` task or any part of a
  Runefile, MUST NOT start the server or the client's agent, and MUST NOT use the network. The
  only program it starts is the client, asked for its version, with a time limit.
- **FR-005**: The command MUST NOT create or change any file outside the project. A target
  path that leads outside the project through a symbolic link MUST be refused, with the path
  named.
- **FR-006**: When the Runefile defines a task named `agent`, `rune agent` and
  `rune agent init` MUST print a note on standard error that the task is run with
  `rune -- agent`. `rune agent` with an argument that is not a subcommand MUST be a usage
  error that names `rune -- agent`.

**What is registered**

- **FR-007**: For every client the registered server MUST be named `rune`, use the stdio
  transport, and run the command `rune` with the arguments `mcp`, `--file`, the absolute path
  of the project's Runefile, and `--no-reload`.
- **FR-008**: With `--unpinned` the arguments MUST be `mcp` and `--no-reload`, with no path.
  The report MUST then say that the server serves the Runefile found from the directory the
  client starts it in. `--unpinned` with a Runefile that discovery would not find by its name
  MUST be a usage error.
- **FR-009**: The command MUST NOT write `--allow-destructive`, `--sandbox off`, an approval setting that lets
  every tool of the server run without a question in a client that asks by default, a mode
  that skips permission checks, an environment variable, a token or any other credential.

**Claude Code**

- **FR-010**: `rune agent init claude` MUST set the server in `.mcp.json` under `mcpServers`,
  and in `.claude/settings.json` MUST add to `permissions.deny` the bare tool names `Bash`,
  `PowerShell`, `Monitor`, `Workflow`, `CronCreate` and `RemoteTrigger`, and to
  `permissions.allow` the rule `mcp__rune`.
- **FR-011**: Rules MUST only be added to a list. No existing rule is removed, reordered or
  moved to another list, and a rule that is already present is not written again.
- **FR-012**: A `permissions.deny` rule that denies every Rune tool (`mcp__rune` or
  `mcp__rune__*`) MUST be a
  conflict that `--force` does not override. A deny or ask rule for one Rune tool MUST be kept
  and reported as a note.

**Codex CLI**

- **FR-013**: `rune agent init codex` MUST set, in `.codex/config.toml`, `shell_tool = false`
  under `[features]`, `enabled = false` under `[agents]`, and `command` and `args` under
  `[mcp_servers.rune]`.
- **FR-014**: The command MUST NOT write `unified_exec`, `multi_agent`,
  `default_tools_approval_mode` or any approval mode.

**VS Code**

- **FR-015**: `rune agent init vscode` MUST set the server in `.vscode/mcp.json` under
  `servers` and in `.mcp.json` under `mcpServers`, and MUST write
  `.github/agents/rune.agent.md` with a `tools` list that holds exactly `rune/*` and an empty
  `agents` list.
- **FR-016**: The agent file MUST never be written with an empty or missing `tools` list.

**Merging**

- **FR-017**: In an existing file, every byte outside the entries the command adds or
  replaces MUST be unchanged: other servers, other settings, unknown keys, key order,
  indentation, line endings, and comments in the formats that have them.
- **FR-018**: The entries the command owns are: the `type`, `command` and `args` of the
  server named `rune`; the seven rules of FR-010; the two Codex settings and the `command` and
  `args` of FR-013; and the agent file of FR-015. Any other key inside the server's entry is
  the person's and MUST be kept.
- **FR-019**: When an owned key already holds a different value, or a value of another type
  is where the command needs a table or a list, the command MUST report a conflict with the
  file, the line and column, the value found and the value it would write, MUST write no file
  of that client, and MUST exit 2.
- **FR-020**: With `--force`, the command MUST replace the value of an owned key that
  conflicts, and the whole agent file. `--force` MUST NOT remove or change a deny rule, an ask
  rule, or any key the command does not own.
- **FR-021**: A file that cannot be parsed MUST stop the command before any write, with the
  file, line and column, and exit 3.
- **FR-022**: Where a file holds the settings in a form the command cannot extend in place,
  the command MUST write nothing and print the setting to add by hand, as a key and its value.
- **FR-023**: All files of a client MUST be checked before the first is written: each new
  text parses, the owned entries hold the intended values, and nothing else changed in
  meaning. When a file cannot be written, the files already written in that run MUST be put
  back as they were, a file the run created MUST be removed, and the file that failed MUST be
  named.
- **FR-024**: A run that would change nothing MUST write nothing and report each file as
  unchanged.

**Review and validation**

- **FR-025**: `--dry-run` MUST print the difference for each file that would change, in
  unified format, on standard output, MUST print the report on standard error, and MUST NOT
  create or change any file or directory. Its exit status MUST be that of the real run.
- **FR-026**: `--check` MUST report, for each owned entry, whether it is present, missing or
  different, MUST write nothing and MUST start no program, the client included. It MUST exit 0
  when every entry is present and 1 otherwise, whatever client is installed. It names no
  outcome, because it did not read the client's version. A value that a real run would report
  as a conflict is reported as different; a conflict that is not about an owned entry, such as
  the deny rule of FR-012, is reported as in a real run and counts as not complete. `--check`
  with `--force` or `--dry-run` MUST be a usage error.
- **FR-027**: Every report MUST end with the manual check for the client: the command a person
  runs and what its output has to show, namely Rune's tools present and no command-running
  tool.

**The report**

- **FR-028**: The report MUST name each file as created, updated or unchanged, with what was
  set in it.
- **FR-029**: The report MUST name exactly one of the three outcomes, in those words. The
  words "restricted", "locked down" and "safe" MUST NOT describe a setup.
- **FR-030**: The command MUST read the client's version and compare it with the list of
  versions the adapter was checked with, which it names. "Rune-only" and "Rune-only within
  SCOPE" MUST be reported only for a version in that list. For any other version, an
  unreadable version, or a client that is not found, the outcome MUST be "Registered only",
  and the report MUST name the version found or say that none was, and name each
  command-running tool that was not checked. The files are written all the same.
- **FR-031**: A version is in an adapter's list only when a run with exactly that version has
  been recorded as the contract of spec 047 requires: Rune's tools in the client's tool list,
  no command-running tool in the client's own listing, a Rune call that ran, and no subagent
  with a command-running tool. A version between two checked versions is not checked. The VS
  Code adapter has no such run, so its list is empty.
- **FR-032**: Every outcome MUST be reported as pending the steps that are left, and each step
  named: trusting the project, approving the server where the client asks, and starting a new
  session. Each step MUST say what a session has before it is taken.
- **FR-033**: For VS Code the report MUST say that the restriction is the custom agent `rune`,
  that every other agent keeps the terminal, and name the tools they keep.
- **FR-034**: The report MUST say that the server does not follow Runefile changes and that a
  new session is needed after an edit, that `[confirm]` tasks are not approved by the setup,
  and that a task an agent calls runs in Rune's sandbox and is refused where the sandbox
  cannot work, until the server runs with `--sandbox off`.
- **FR-035**: The exit status MUST be 0 when the outcome is "Rune-only" or "Rune-only within
  SCOPE", or when `--check` found every entry present; 1 when the outcome is "Registered
  only", when `--check` found the setup not complete, or when a file could not be read or
  written; 2 for a usage error or a conflict; 3 for a Runefile or a client file that cannot be
  read as its format. The full table is in
  [contracts/rune-agent-init.md](contracts/rune-agent-init.md).

**Documentation**

- **FR-036**: The documentation MUST describe the command and each flag, the files written for
  each client, the client versions checked, the trust, approval and restart steps, the manual
  checks, and which entries are Rune's and how to remove only those.
- **FR-037**: The documentation MUST state what a setup does not prevent, by reference to the
  enforcement boundaries of the design note; that the client is opened in the project's
  directory; that a task named `agent` is run with `rune -- agent`; and that a person who adds
  `--allow-destructive` to the registration also adds an `ask` rule for each such tool in
  Claude Code, because the allow rule `mcp__rune` would otherwise let it run without a
  question.
- **FR-038**: The documentation of the `(agent)` executor MUST name `RUNE_MCP_URL` and
  `RUNE_MCP_TOKEN` and show the `agent_cmd` of the research that gives the launched agent
  Rune's tools and no shell.
- **FR-039**: The design note MUST no longer say that the command does not exist.

### Key Entities

- **Client adapter**: what Rune knows about one client: its name, the files it uses, the
  entries to write in each, the command-running tools those entries remove, the versions it
  was checked with, the strongest outcome it can reach, and the steps left to the person.
- **Owned entry**: one value in one file that the command sets and may later replace: a key
  with a value, or a rule in a list. Everything else in the file is the person's.
- **Conflict**: an owned key that already holds another value, a value of the wrong type where
  the command must add, or a deny rule that makes the setup impossible.
- **Setup outcome**: one of the three outcomes, the scope when there is one, the pending
  steps, and the tools that remain when the outcome is "Registered only".
- **Report**: the per-file results, the client and version, the outcome, the steps, the notes
  and the manual check.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a project with no client files, one command per client produces a complete
  setup: no file has to be edited by hand before the pending steps.
- **SC-002**: In every merge fixture, 100% of the bytes outside the owned entries are
  identical before and after.
- **SC-003**: A second run with the same options writes nothing in 100% of fixtures.
- **SC-004**: With `--dry-run` and with `--check`, the project directory is identical before
  and after in 100% of fixtures, and applying the printed difference gives the files of a real
  run.
- **SC-005**: No fixture run reports "Rune-only" or "Rune-only within SCOPE" for a client
  version that was not checked, for a client that was not found, or for VS Code.
- **SC-006**: Every conflict and every parse failure names a file and a line, and leaves every
  file of the project unchanged.
- **SC-007**: For Claude Code and Codex CLI, the files the command writes, used alone in a
  trusted project with a checked version, give a session whose tool list has Rune's tools and
  no command-running tool. This is recorded for each checked version: by the runs of this
  feature, or, for Claude Code 2.1.325 and Codex CLI 0.167.0, by runs c12, x7 and y5 of spec
  047, whose files differ from the command's only in the server's arguments.
- **SC-008**: After any run, no file outside the project has changed, and no task of the
  Runefile has run.
- **SC-009**: A person can go from a project with a Runefile to a session with Rune's tools
  and no shell by following only the command's output, in under five minutes.

## Assumptions

- The research of spec 047 holds for the versions it names: Claude Code 2.1.325 and Codex CLI
  0.167.0. The versions installed today are newer (2.1.326 and 0.169.0). A version is added to
  an adapter's list only after the run of FR-031 is repeated and recorded for it. This work
  repeated the run for both installed versions; the records are in
  [observations/](observations/README.md).
- The project is the directory of the Runefile. A client that is opened in another directory
  of a larger repository does not read files written there; the documentation says to open
  the client in the project.
- `rune` is on the `PATH` of the client's process. The registration uses the name `rune`, not
  the path of the binary, so that the file does not name one machine's install location.
- The default registration holds an absolute path and is for one checkout. `--unpinned` is the
  form for a committed file.
- `--no-reload` is registered for every client. Claude Code and Codex CLI keep a tool that
  writes files, and one registration that is the same for all three keeps the shared
  `.mcp.json` free of conflicts. The cost is that an edit to the Runefile needs a new session.
- `[confirm]` tasks need no client rule: with no `--allow-destructive` in the registration,
  Rune refuses them or puts each call to the person. The Claude Code rule `mcp__rune` therefore
  approves nothing destructive.
- The trust and approval steps are always named, because whether they were already taken is
  recorded in user-wide files that the command does not read.
- VS Code is written for both of its harnesses because the Local harness is deprecated and a
  person cannot easily tell which one runs.
- The report is text for a person. A machine-readable report is not part of this feature; a
  script uses the exit status.
- Adding the command takes the name `agent` on the command line, as `log` and `import` took
  theirs. A task named `agent` stays a task and an MCP tool, and is run with `rune -- agent`.
