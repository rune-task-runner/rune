# Feature Specification: Rune-Only Agent Mode Design Note

**Feature Branch**: `047-rune-only-agent-mode`

**Created**: 2026-11-16

**Status**: Draft

**Input**: GitHub issue [#46](https://github.com/rune-task-runner/rune/issues/46)
(backlog MCP-11): "Research Rune-only agent mode"

## Overview

Registering `rune mcp` with an agent client adds Rune's tasks to the tools
the agent has. It takes nothing away. The client's own shell tool is still
there, so an agent that is offered a `plan` task and no `apply` task can
still type `tofu apply` into the shell. `--allow`, `--deny` and
`--allow-destructive` decide what Rune will run. They say nothing about what
the client will run by itself.

A session is limited to the Runefile only when the client is also told to
give the agent no shell. Each client has its own way to say that, some have
none, and nobody has written down which is which. An operator who reads
"secure by default" in the MCP guide can take a Rune allowlist for more than
it is.

This feature is research with three results:

- a design note that says, for each client, what configuration registers
  Rune, what removes the client's shell, and what allows Rune's tools; which
  approach Rune should take to produce that configuration; and where the
  restriction ends;
- a correction to the MCP guide, so its security model says plainly that
  Rune's filters do not restrict the client's shell, and points to the note;
- the content for the follow-up implementation issue,
  [#53](https://github.com/rune-task-runner/rune/issues/53), which already
  proposes `rune agent init claude|codex|vscode`: what this research changes
  or adds to it.

### Out of scope

- Implementing a command that writes or prints client configuration. That
  is issue #53.
- Changing what `rune mcp`, `rune serve` or the `(agent)` executor do. No
  production code changes.
- Sandboxing: limiting what a task itself can reach once Rune runs it. That
  is a separate issue (#51). The note names the boundary and stops there.
- Clients other than the five of the compatibility table, and
  `rune serve --http` as a way for a client to reach Rune. The endpoint an
  `(agent)` task opens for its own agent is in scope, as part of that
  executor.
- Running a client that is not installed where the research is done. Such a
  client is reported from reading and marked as not tested, as in the
  compatibility table.
- A pending end-to-end test. The issue specifies none for MCP-11.
- Keeping the note current as clients release new versions. It is dated.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An operator learns what makes a session Rune-only, and what does not (Priority: P1)

An operator wants an agent to act on a project only through its Runefile.
They open the design note, find their client, and read: the configuration
that registers `rune mcp`; the configuration that removes the client's shell
and every other tool that runs commands; the configuration that lets Rune's
tools run; where each of those lives; and whether anyone has seen it work.
For a client that cannot remove its shell, or cannot do so for a whole
project, the note says so first.

**Why this priority**: This is the question the issue starts from. Without
it an operator believes a Rune allowlist constrains a session that it does
not constrain.

**Independent Test**: Pick any of the five clients. The note answers, for
that client, the three questions (register, remove shell, allow Rune tools)
with a configuration or a plain "not possible" or "unknown", and each answer
names its evidence.

**Acceptance Scenarios**:

1. **Given** the note, **When** an operator looks up any of the five
   clients, **Then** they find a configuration that registers `rune mcp`,
   one that removes the client's command-running tools, and one that allows
   Rune's tools, or a statement that the client has no such control.
2. **Given** a client that has more than one tool able to run a command,
   **When** the operator reads its part, **Then** every such tool that was
   found is named, and the configuration covers each or says which it does
   not cover.
3. **Given** a client whose restriction holds only for a named profile,
   mode or way of starting it, **When** the operator reads its part,
   **Then** the note names that scope and says what stays unrestricted
   outside it.
4. **Given** the MCP guide, **When** an operator reads its security model,
   **Then** it says that registering Rune and filtering its tasks do not
   remove the client's shell, and links to the note.
5. **Given** any statement about what a client does, **When** a reader reads
   it, **Then** it names its evidence, and a client that was not run is said
   to be not tested.

---

### User Story 2 - A maintainer knows which approach Rune takes, and why (Priority: P1)

The maintainer who will build issue #53 needs a decision, not a survey. The
note compares the approaches the issue lists: a command that prints
configuration to merge; a command that writes it; a client hook that turns a
shell command into the matching Rune tool call; and having Rune launch the
restricted agent itself, as the `(agent)` executor already launches agents.
It chooses one, says what the others cost, and says what the chosen approach
must report when a client cannot be restricted.

**Why this priority**: The issue's first deliverable is "the chosen
approach". A note that lists options and stops leaves #53 where it was.

**Independent Test**: Read the decision section alone. It names one
approach, gives the reason for each approach not chosen, and states the rule
for a client or profile that cannot be restricted.

**Acceptance Scenarios**:

1. **Given** the note, **When** the maintainer reads the decision, **Then**
   one approach is chosen, and each other approach the issue lists has a
   stated reason for not being chosen or for being deferred.
2. **Given** the question whether a client hook can redirect a shell command
   to the matching Rune tool, **When** the maintainer reads the note,
   **Then** it answers for each client that has hooks: what a hook can do to
   a shell call (refuse it, change its input, replace it), what it cannot,
   and the evidence.
3. **Given** the `(agent)` executor, **When** the maintainer reads the note,
   **Then** it says what that executor does today to the agent it launches,
   whether that agent has a shell, and what the chosen approach means for
   it.
4. **Given** a client or profile where the shell cannot be removed, **When**
   the maintainer reads the decision, **Then** it states that the outcome
   there must be reported as registration only, never as a Rune-only
   session.

---

### User Story 3 - A reader knows where the restriction ends (Priority: P1)

A security-minded reader asks what a Rune-only session still lets an agent
do. The note lists the boundaries: what an agent can still reach with the
shell gone (for example writing a file that a task later runs, or changing
the Runefile so a new task appears), what a user or another profile of the
same client can still do, and what only a sandbox would stop. For each
boundary it says whether a setting closes it and which.

**Why this priority**: The issue asks for "enforcement boundaries" by name.
A configuration that removes the shell and leaves a second way to run a
command is worse than none, because it is trusted.

**Independent Test**: Read the boundaries section. Each boundary states what
the agent can do, why the shell restriction does not stop it, and either the
setting that closes it or that nothing in scope closes it.

**Acceptance Scenarios**:

1. **Given** the note, **When** a reader looks for what a Rune-only session
   does not guarantee, **Then** one section lists every boundary found, and
   none is left to be inferred from a client's part.
2. **Given** an agent that keeps a tool for writing files, **When** the
   reader reads the boundaries, **Then** the note says whether that agent
   can make Rune run a command of its choosing, by which route, and what
   closes the route.
3. **Given** a restriction that is a setting in a file, **When** the reader
   reads the boundaries, **Then** the note says who can change that file,
   including the agent itself.
4. **Given** the difference between restricting the client and sandboxing a
   task, **When** the reader reads the boundaries, **Then** the note says
   which of the two it covers and names the issue that covers the other.

---

### User Story 4 - The follow-up issue carries the findings (Priority: P2)

Issue #53 was written before this research, from documentation and help
text. The maintainer finds, in the repository, the text to add to it: for
each client it names, the adapter's facts (files, keys, tool names, scope);
which client or profile combinations are unsupported or partial; the merge
rules the findings require; and what the verification must show. The
difference between the clients the two issues name (`cursor` in one,
`vscode` in the other) is settled.

**Why this priority**: It is the issue's second deliverable, and it depends
on the first three stories.

**Independent Test**: Read the follow-up text beside issue #53. Every
acceptance criterion of #53 that this research touches has a finding, and
every client #46 or #53 names is either supported, partial, or not supported
with a reason.

**Acceptance Scenarios**:

1. **Given** the follow-up text, **When** the maintainer compares it with
   issue #53, **Then** each client named in #46 or #53 is listed as
   supported, partial or not supported, with the reason and the scope.
2. **Given** a finding that contradicts or sharpens a statement in #53,
   **When** the maintainer reads the follow-up text, **Then** the text names
   the statement and gives the finding and its evidence.
3. **Given** the follow-up text, **When** the maintainer reads its
   verification part, **Then** it says what a check must show before a
   client's setup may be called Rune-only: Rune's tools are present and no
   command-running tool is.

---

### Edge Cases

- A client has several tools that run commands, and its documentation names
  one. The note lists each one found and says how it was found.
- A client removes the shell only inside a named profile or custom agent.
  Another profile in the same project has the shell. The note says the
  restriction is the profile's, not the project's.
- A client has settings at several levels: project, user, organization,
  command line. The note says which level wins, and whether a project
  setting can be undone from a level the agent can write.
- The agent keeps a file-writing tool and Rune reloads the Runefile. The
  agent adds a task and calls it.
- The agent keeps a file-writing tool and a task runs a script from the
  project. The agent rewrites the script and calls the task.
- The agent can write the client's own configuration file and so remove its
  restriction, in this session or the next.
- A client can start subagents or other agents that do not inherit the
  restriction.
- A client's hook can refuse a shell command but cannot replace it with a
  call to another tool.
- A shell command "matches" a task only loosely: the task takes parameters,
  runs in another directory, or has dependencies the command does not.
- A client applies a restriction when started one way and not another, for
  example with a terminal and without one.
- A client's documentation says a setting removes the shell and a run shows
  it does not, or the reverse. Both are reported.
- A client is not installed, so its part comes from reading. It is marked
  as not tested.
- The `(agent)` executor launches an agent with whatever tools that agent's
  own configuration gives it. A Rune-only project configuration may or may
  not reach an agent started that way.
- The two issues name different third clients. The note covers both.

## Requirements *(mandatory)*

### Functional Requirements

**The note**

- **FR-001**: The repository's documentation MUST hold a design note on
  Rune-only agent mode, linked from the MCP guide.
- **FR-002**: The note MUST state the problem in its opening: registering
  Rune's tools does not remove a client's shell, and Rune's own filters do
  not reach it.
- **FR-003**: The note MUST define what it means by a Rune-only session: the
  agent has Rune's tools and has no tool of the client that runs a command.
- **FR-004**: The note MUST give the date of the research and, for every
  client, the version its statements are about.

**Per client**

- **FR-005**: The note MUST cover Claude Code, Codex CLI, Cursor, VS Code
  Copilot agent mode and Zed.
- **FR-006**: For every client the note MUST answer: how `rune mcp` is
  registered for a project; which of the client's tools can run a command;
  how each is removed or denied; how Rune's tools are allowed to run without
  a question, by server and by tool; in which file and at which level each
  setting lives; and the scope the restriction holds for (project, profile,
  mode, way of starting).
- **FR-007**: Where a client cannot remove a command-running tool, or cannot
  do it for a project, the note MUST say so in that client's part and in a
  summary table of all five clients.
- **FR-008**: The summary table MUST give each client one of three outcomes:
  Rune-only, when it can be had for a project; Rune-only within a named
  scope; or registered only, when the client's command-running tools cannot
  be removed or it is unknown whether they can. No cell may be empty.
- **FR-009**: Every statement about a client's behavior MUST name its
  evidence with the labels the compatibility table defines (recorded,
  source, docs, staff, unknown), or with a label the note defines for a run
  that was observed and whose command and output are kept in the
  repository. A client that was not run MUST be reported as not tested.
- **FR-010**: A statement labelled as observed MUST name the run it rests
  on. An index in the repository MUST give, for every run, the client
  version, the configuration, the command and the prompt, and MUST name the
  file that holds the run's output.
- **FR-011**: Where a client's documentation and an observed run disagree,
  the note MUST report both.

**Approaches and decision**

- **FR-012**: The note MUST evaluate four approaches. Three come from the
  issue: a command that prints client configuration to merge; a client hook
  that redirects a shell command to the matching Rune tool; and Rune
  launching the restricted agent itself, which is how the issue's point
  about the `(agent)` executor becomes an approach. The fourth is a command
  that writes the configuration, as issue #53 proposes.
- **FR-013**: For hooks, the note MUST say for each client that has them
  what a hook can do to a shell call and what it cannot, and whether
  redirection to a Rune tool is possible.
- **FR-014**: For the `(agent)` executor, the note MUST say how it starts
  its agent today, what it gives that agent, whether that agent's shell is
  restricted, and what the chosen approach changes or leaves alone.
- **FR-015**: The note MUST choose one approach and give, for each approach
  not chosen, the reason it was rejected or deferred.
- **FR-016**: The note MUST state the reporting rule: an outcome that
  registers Rune without removing every command-running tool MUST NOT be
  described as Rune-only, and MUST name what remains.

**Enforcement boundaries**

- **FR-017**: The note MUST have one section that lists every enforcement
  boundary found. It MUST cover at least: file-writing tools combined with
  Runefile reload; file-writing tools combined with scripts a task runs; the
  agent changing the client's own configuration; other profiles, modes or
  subagents of the same client; settings levels above the project; a user
  who overrides the configuration; and the difference between restricting
  the client and sandboxing a task.
- **FR-018**: For each boundary the note MUST say what the agent or user can
  do, why the shell restriction does not prevent it, and either the setting
  that closes it or that nothing in scope does.
- **FR-019**: The note MUST say what Rune's own flags contribute to each
  boundary, where they do, and MUST NOT present a Rune flag as restricting
  the client.

**The MCP guide**

- **FR-020**: The MCP guide's security model MUST say that registering Rune
  and filtering its tasks do not remove or restrict the client's shell, and
  MUST link to the note. Its part on agent task bodies MUST say that the
  agent such a task launches keeps the tools its own configuration gives
  it, and MUST link to the note.

**The follow-up issue**

- **FR-021**: The repository MUST hold the text to add to issue #53. It MUST
  list each client named in #46 or #53 as supported, partial or not
  supported, with the scope and the reason.
- **FR-022**: The follow-up text MUST carry, for each supported or partial
  client, what an adapter needs: files, keys, tool names, the scope, and
  what must be preserved when merging into an existing file.
- **FR-023**: The follow-up text MUST name each statement of #53 that the
  research contradicts or sharpens, with the finding and its evidence.
- **FR-024**: The follow-up text MUST state what a verification has to show
  before a setup is reported as Rune-only.

**Limits**

- **FR-025**: The feature MUST NOT change the `rune` binary, the MCP server
  library, the `(agent)` executor, or any message Rune sends.
- **FR-026**: No configuration of the research machine's user account may be
  changed to make an observation. An observation uses a project in a
  temporary directory with project-level configuration, or options given on
  the command line.
- **FR-027**: A file kept as evidence MUST NOT contain a path of the
  research machine, a user name, or an identifier of a vendor account or
  session.
- **FR-028**: The documentation checks MUST pass with the note and the
  changed guide: links resolve, code blocks validate, and the site pages
  generated from them pass their checks.

### Key Entities

- **Client**: one of the five agent programs, with a version, or the
  statement that it was not run.
- **Command-running tool**: a tool of the client, not of Rune, through which
  an agent can run a command of its choosing.
- **Control**: a setting, option, profile or hook of a client that
  registers a server, removes or denies a tool, or allows a tool.
- **Scope**: where a control holds: a project, a user, an organization, a
  profile or custom agent, a mode, or one way of starting the client.
- **Rune-only session**: a session whose agent has Rune's tools and no
  command-running tool.
- **Approach**: one way for Rune to bring a client to a Rune-only session.
- **Enforcement boundary**: something a Rune-only session does not prevent,
  with what closes it or the statement that nothing in scope does.
- **Evidence label**: how a statement is known: recorded, observed, source,
  docs, staff or unknown.
- **Follow-up text**: what this research adds to issue #53.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For each of the five clients, a reader finds in one document
  how to register Rune, how to remove every command-running tool found, and
  how to allow Rune's tools, or a plain statement that it cannot be done,
  and every one of those answers names its evidence.
- **SC-002**: The summary table gives all five clients an outcome, and every
  client that was not run is named as not tested where a reader first meets
  the table.
- **SC-003**: The decision names exactly one chosen approach, and each of
  the four approaches evaluated has a stated status: chosen, rejected or
  deferred, with a reason.
- **SC-004**: The boundaries section lists at least the seven boundaries of
  FR-017, and each has a closing setting or the statement that nothing in
  scope closes it.
- **SC-005**: Every statement labelled as observed can be traced to a file
  in the repository that shows it. At least one client has an observed
  answer to "is the shell gone, and do Rune's tools still run".
- **SC-006**: Every client named in issue #46 or #53 appears in the
  follow-up text as supported, partial or not supported.
- **SC-007**: A reader of the MCP guide's security model meets, without
  following a link, the statement that Rune's filters do not restrict the
  client's shell.
- **SC-008**: The documentation checks and the full test suite pass, and no
  file outside the documentation, the specification directory and the
  documentation site's navigation changes.

## Assumptions

- The research is done on 2026-11-16 on one Linux x86_64 machine, the one
  the compatibility table was made on. Claude Code 2.1.325 and Codex CLI
  0.167.0 are installed there. Cursor, VS Code and Zed are not, and the
  machine has no display.
- Installed clients are run without a terminal session (`claude -p`,
  `codex exec`), because the research runs unattended. Interactive behavior,
  such as an approval dialog, is not tested.
- A run of a client uses a model through the maintainer's own accounts. The
  project it runs in holds nothing private.
- The compatibility table in the MCP guide (issue #45) is taken as given for
  configuration files, tool names and approval behavior. This research adds
  to it and does not repeat its recordings.
- "Design note" is read as one Markdown document in the repository's
  documentation, reviewed in the pull request. The issue's verification
  target is design review.
- The second deliverable, "create a follow-up implementation issue", is met
  by issue #53, which the issue itself names as the concrete follow-up. The
  text to add to #53 is kept in the repository and posted by the maintainer,
  so that the pull request holds everything that is reviewed.
- Issue #46 names `claude|codex|cursor` and a command called
  `rune agent-config`. Issue #53 names `claude|codex|vscode` and a command
  called `rune agent init`. The note covers all four clients, and Zed for
  completeness with the table, and recommends which the first
  implementation supports. The command's name is #53's to settle; the note
  uses #53's name.
- A client's tool that writes files is not counted as a command-running
  tool. What it makes possible is covered as an enforcement boundary.
