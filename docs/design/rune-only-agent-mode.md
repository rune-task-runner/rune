# Rune-only agent mode

> A design note. It records research done on 2026-11-16 for
> [issue #46](https://github.com/rune-task-runner/rune/issues/46) and the decision that
> follows from it. Nothing here is a feature of Rune yet: the command it describes is
> [issue #53](https://github.com/rune-task-runner/rune/issues/53). To use Rune with an
> agent today, read [AI agents (MCP)](../mcp.md).

## The problem

Registering `rune mcp` with an agent client adds Rune's tasks to the tools the agent has.
It takes nothing away. The client's own shell tool is still there. An agent that is offered
a `plan` task and no `apply` task can still type `tofu apply` into the shell.

`--allow`, `--deny` and `--allow-destructive` decide what Rune will run when it is asked.
They do not reach the client. A Rune allowlist limits a session only when the client is
also told to give the agent no shell, and each client has its own way to say that. Some
have none.

This was seen, not assumed. With the server registered and no tool removed, Claude Code
and Codex each ran a shell command and a Rune task in the same session (observed: c1, y1).

## What "Rune-only" means

A **command-running tool** is a tool of the client, not of Rune, through which an agent
can run a command of its choosing: a shell, a terminal, a task runner that takes a command.

A session is **Rune-only** when its agent has Rune's tools and no command-running tool.

Two words are kept apart in this note. A tool is **removed** when the client does not give
it to the model. It is **refused** when the model has it and the client refuses each call.
Only removal makes a session Rune-only.

A tool that writes files is not a command-running tool. What it lets an agent do is in
[Enforcement boundaries](#enforcement-boundaries), and it is a lot.

## Evidence

Every statement about what a client does names its evidence:

- **observed**: seen in a run on 2026-11-16. The id after it, such as c3, is a run in
  [the observations](../../specs/047-rune-only-agent-mode/observations/README.md), which
  give the client version, the configuration, the command, the prompt and the output.
- **recorded**: seen in a session of the
  [client compatibility](../mcp.md#client-compatibility) research.
- **source**: read in the client's source code, not run.
- **docs**: stated in the vendor's documentation, or in the client's own `--help`.
- **staff**: stated by the vendor's staff on the vendor's forum.
- **unknown**: no evidence found.

Only Claude Code and Codex CLI were run, and only without a terminal session (`claude -p`,
`codex exec`). Cursor, VS Code and Zed were not tested: everything about them comes from
reading.

## Summary

| | Outcome | What removes the shell | What lets Rune's tools run | Scope |
|---|---|---|---|---|
| Claude Code 2.1.325 | **Rune-only** | `permissions.deny` with the tools' names, in `.claude/settings.json` (observed: c2, c11) | `permissions.allow` with `mcp__rune`, in a trusted workspace (observed: c12). It is ignored until the workspace is trusted (observed: c2), and `--allowedTools mcp__rune` on the command line does it then (observed: c3) | The project |
| Codex CLI 0.167.0 | **Rune-only** | `shell_tool = false` under `[features]` and `enabled = false` under `[agents]`, in `.codex/config.toml` (observed: x7, y5) | Nothing for a plain or `[readonly]` task (recorded). A setting per tool for a `[network]` task (source) | The project, once the user's configuration trusts it (recorded) |
| VS Code 1.146, Copilot agent mode | **Rune-only within one custom agent**, not tested | Nothing for a workspace (source, docs). A custom agent whose `tools` list names only Rune's (source, docs) | The user's own approval. No workspace file found (docs) | One named custom agent. The user can add tools to it (source) |
| Cursor | **Registered only**, not tested | Nothing since 2.1 (staff). A hook can refuse shell calls (docs) | The user's approval in the editor. `Mcp(rune:*)` in the CLI's permissions (docs) | None |
| Zed 1.28.1 | **Registered only**, not tested | Nothing in a project file (source). A profile in the user's settings (source) | A setting per tool in the user's settings (source) | None in a project. One profile of one user |

The three outcomes are defined in
[Setup outcome](../../specs/047-rune-only-agent-mode/contracts/setup-outcome.md):

- **Rune-only**: every session the client starts in the project has Rune's tools and no
  command-running tool.
- **Rune-only within a scope**: the same, inside one named profile, custom agent or way of
  starting the client. Sessions outside it keep their shell.
- **Registered only**: Rune's tools are there, and so is the shell.

No outcome says that the person at the keyboard cannot undo the configuration, and none
says anything about what a task can reach once Rune runs it. Both limits are in
[Enforcement boundaries](#enforcement-boundaries).

## Decision

Four approaches were weighed. One is chosen.

| Approach | Status | Reason |
|---|---|---|
| **Write the configuration.** A command writes project-scoped client files and shows the change first. This is issue #53's `rune agent init <client>`. | **Chosen** | In both clients that can remove their shell, the control is a setting a project file can carry. A project file covers every session started in the project and is reviewed like code. A command that merges and shows a diff does not leave the merge to the user. |
| **Print the configuration.** A command prints a block to merge by hand. This is issue #46's `rune agent-config <client>`. | Rejected as its own command, kept as the dry run of the chosen one | Merging by hand into a JSON file that already has `deny` rules, or a TOML file with comments, is where it goes wrong. A command that only prints cannot say what the outcome is, because it does not know what the file will hold. |
| **Redirect with a hook.** A client hook turns a shell command into the matching Rune tool call. | Rejected as the restriction | No client's hook can replace a call with a call to another tool. It can refuse the call. See [Hooks](#hooks). |
| **Launch the agent.** Rune starts the client with options that remove its shell, as the `(agent)` executor already starts agents. | Deferred | Command-line options outrank project files in both installed clients and need no trust step. But this covers only clients started from a command line, and only sessions started through Rune. The launch lines are in this note; a launcher command is left to a later issue. See [The agent executor](#the-agent-executor). |

### What the command reports

The command ends by naming one of the three outcomes, and these rules hold:

1. **Registration is not restriction.** A setup that leaves a command-running tool
   available is reported as "Registered only", and the message names each tool that
   remains.
2. **Refused is not removed.** A shell that a hook refuses does not make a setup
   Rune-only. The message says "refused, not removed".
3. **A step left to the user is part of the outcome.** If the project has to be trusted or
   the server approved before the files apply, the outcome is reported as pending that
   step. Until then a Codex session has a shell and no Rune tools at all.
4. **An unknown client version gets no more than "Registered only".**

## Clients

### Claude Code

Run: version 2.1.325 on Linux, as `claude -p`.

**Register.** `.mcp.json` at the project root (recorded):

```json
{
  "mcpServers": {
    "rune": { "type": "stdio", "command": "rune", "args": ["mcp"] }
  }
}
```

An interactive session asks the user to approve a project's server before it starts it.
`claude -p` starts it without asking, even in a folder that was never trusted (docs).

**Remove the shell and allow Rune.** `.claude/settings.json` in the project:

```json
{
  "permissions": {
    "deny": ["Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"],
    "allow": ["mcp__rune"]
  }
}
```

A `deny` rule that is a bare tool name removes the tool: the model is never given it
(docs). With this file, none of the six was in the client's tool list (observed: c11).
With `Bash` and `Monitor` alone denied, those two were gone and the other three stayed
(observed: c2, c3). Without the file, all were there, and the shell command ran (observed:
c1).

The last three names are in the list because the vendor's own restricted mode removes
them (observed: c4), and because what they start cannot be vouched for.

| Tool | What it runs | With the file above |
|---|---|---|
| `Bash` | A shell command (docs) | Removed (observed: c11) |
| `Monitor` | A command in the background (docs) | Removed (observed: c11). A rule that denies `Bash` denies it too (docs) |
| `PowerShell` | A PowerShell command, on Windows (docs) | Removed by the same kind of rule (docs). Not tested: the run was on Linux, where the client has no such tool |
| `Workflow` | A script that starts subagents (docs) | Removed (observed: c11) |
| `CronCreate` | A prompt, later, in this session (docs) | Removed (observed: c11) |
| `RemoteTrigger` | A scheduled agent in the vendor's cloud (docs). What tools that agent has is unknown | Removed (observed: c11) |
| `Agent`, listed as `Task` | A subagent | Kept. The subagent had no shell tool (observed: c11). Deny rules apply to subagents (docs) |
| `Skill` | A skill, which can carry shell commands of its own (docs) | Kept. A deny rule stops such a command (docs). Not tested |

**The allow rule needs a trusted workspace.** `mcp__rune` allows every tool of the server,
and `mcp__rune__build` one (docs). In a workspace the user has not trusted, the client
applied the project's `deny` rules and ignored its `allow` rules, and said so on stderr
(observed: c2). The session then had no shell and could not call Rune either: each call
was refused for lack of permission. With `--allowedTools mcp__rune` on the command line
the Rune call ran (observed: c3). In a workspace the user had trusted, the same two files
were enough: no shell tool, and the Rune call ran with nothing on the command line
(observed: c12). So the same file gives a different session before and after the user
trusts the project, and a setup must say which it left behind.

**The allow rule covers `[confirm]` tasks.** Claude Code decides the same way for every
tool, whatever its hints (recorded). With `mcp__rune` allowed, a `[confirm]` task that the
operator named in `--allow-destructive` runs without a question from the client. To keep
the question, add an `ask` rule for that tool, such as `mcp__rune__clean`: rules are
evaluated deny, then ask, then allow (docs). Not tested.

**Starting it restricted.** `claude --restricted` "removes the built-in tools that run
commands or code", ignores user, project and local settings files, and refuses the mode
that skips permission checks (docs: the client's `--help`). Started that way with
`--allowedTools mcp__rune`, the client listed no `Bash`, `Monitor`, `Workflow`,
`CronCreate`, `RemoteTrigger` or `WebFetch`, its subagent had no shell tool, and the Rune
call ran (observed: c4). That list is the vendor's own view of what to remove. It also
holds `WebFetch`, which runs nothing and is left out of the `deny` list above.

**Levels.** Settings come from managed settings, the command line, the project's local
file, the project's shared file and the user's file, in that order. A tool denied at any
level cannot be allowed at another, and deny rules hold in every permission mode,
including the one that skips checks (docs). A user can still leave the project file out,
for example with `--setting-sources user`, or edit it. Only managed settings bind the user
(docs). None of this was run.

**The agent and its own configuration.** `.claude/` and `.mcp.json` are protected paths: a
write to them asks in the default mode, is denied in `dontAsk` mode, and is allowed in the
mode that skips checks (docs). Not tested.

**Not tested.** An interactive session and its trust and approval dialogs. Windows.
Managed settings. The `Skill`, `Workflow` and `RemoteTrigger` paths.

Sources: [permissions](https://code.claude.com/docs/en/permissions),
[permission modes](https://code.claude.com/docs/en/permission-modes),
[tools reference](https://code.claude.com/docs/en/tools-reference),
[headless mode](https://code.claude.com/docs/en/headless),
[MCP](https://code.claude.com/docs/en/mcp),
[sandboxing](https://code.claude.com/docs/en/sandboxing).

### Codex CLI

Run: version 0.167.0 on Linux, as `codex exec`.

**Register, remove the shell.** `.codex/config.toml` in the project:

```toml
[features]
shell_tool = false

[agents]
enabled = false

[mcp_servers.rune]
command = "rune"
args = ["mcp"]
```

Codex starts a server in the directory the session was started in, and a `cwd` key in the
server's table names another (source, docs).

| Tool | What it runs | With the file above |
|---|---|---|
| `exec_command`, `write_stdin` | A shell command, and input to one that is running | Removed by `shell_tool = false` (observed: x7) |
| `spawn_agent` and five more `collaboration` tools | A subagent | Removed by `[agents] enabled = false` (observed: x7). Whether a subagent would inherit `shell_tool = false` was not tested |
| `exec` | JavaScript with "no Node, no file system, no network access", which calls the other tools (observed: x1, in the tool's own description) | Kept. It reaches only the tools that remain |
| `apply_patch` | Nothing. It writes files | Kept (observed: x7) |

`shell_tool` is documented as "Enable command execution tools" (docs). With it off, the
request Codex builds for the model holds no `exec_command` and no `write_stdin` (observed:
x2), and the model reported that it had no shell tool (observed: y2). With it on, the
shell command ran (observed: y1).

The file was read from a project the user's configuration trusts. With only the server in
it, the request held the shell and subagent tools (observed: x6). With the file above, it
held neither (observed: x7). A capture lists no MCP tool, so a run with the model was made
from the same file and no option: the model reported no shell tool and no subagent tool,
and the Rune call ran (observed: y5). The other Codex runs were made in new directories,
which Codex does not trust and where it ignores the file, so they gave the same settings
as options: `--disable shell_tool`, which is "equivalent to `-c features.shell_tool=false`"
(docs: the client's `--help`), and `-c agents.enabled=false` (observed: x2, x3, y2).

Two settings that look as if they would help do not. `unified_exec = false` changes nothing
(observed: x5), and neither does `multi_agent = false`: the subagent tools stay (observed:
x4).

Reading and running disagree in one place. Codex's own feature list marks `code_mode` as
off, yet the model is given one `exec` tool that holds the others (observed: x1). The shell
is therefore not in the top-level tool list, with the feature on or off. It has to be
looked for inside `exec`.

**Allow Rune.** Nothing is needed for most tasks. Codex asks before it calls a tool whose
`destructiveHint` is true, or whose `openWorldHint` is true without `readOnlyHint`
(source). A plain task and a `[readonly]` task run without a question, and `codex exec`
refuses a `[confirm]` or `[network]` task because it cannot ask (recorded). To let one
`[network]` task run, set `approval_mode = "approve"` in a
`[mcp_servers.rune.tools.<name>]` table (source). `default_tools_approval_mode = "approve"`
on the server allows every tool, `[confirm]` tasks included (recorded), which is more than
a setup should grant.

**The project file needs a trusted project.** Codex reads `.codex/config.toml` only in a
project the user's configuration trusts (docs, recorded). In any other directory the whole
file is ignored: the session has its shell and has no Rune tools.

**Levels.** From strongest to weakest: an organization's managed configuration, options on
the command line, the project file, the user's profile, the user's file (source). Two
things follow. A project file's `shell_tool = false` beats the user's own setting. And
`codex --enable shell_tool` beats the project file, so a user can always have the shell
back. An administrator can pin `shell_tool = false` under `[features]` in
`/etc/codex/requirements.toml`, which nothing below overrides (source, docs). Not run.

**Starting it restricted.** The same settings as options:
`codex --disable shell_tool -c agents.enabled=false` (observed: y2, which also set
`default_tools_approval_mode = "approve"` on the server). They need no trusted project.

**The agent and its own configuration.** In the `workspace-write` sandbox, `.codex` is
read-only to the agent, as `.git` is (source, docs). In `danger-full-access` it is not
(source). Not tested.

**Not tested.** An interactive session. Hooks. `requirements.toml`. A profile.

Sources: [configuration reference](https://developers.openai.com/codex/config-reference),
[MCP](https://developers.openai.com/codex/mcp), and
[`openai/codex`](https://github.com/openai/codex/tree/rust-v0.167.0/codex-rs) at tag
`rust-v0.167.0`: `core/src/tools/spec_plan.rs`, `features/src/lib.rs`,
`config/src/loader/README.md`, `config/src/config_requirements.rs`,
`core/src/mcp_tool_call.rs`, `protocol/src/permissions.rs`,
`rmcp-client/src/stdio_server_launcher.rs`, `hooks/src/engine/output_parser.rs`.

### Cursor

Not tested. Cursor is closed source: every statement is from its documentation as read on
2026-11-16, or from its staff on the Cursor forum.

**Register.** `.cursor/mcp.json` in the project (docs), as in
[client compatibility](../mcp.md#cursor). The CLI uses the same file and keeps its own list
of approved servers per user (docs).

**The shell cannot be removed.** The agent's "Run shell commands" tool is the one
command-running tool the documentation lists (docs). Custom modes, which chose a mode's
tools, "were removed in Cursor 2.1", and their replacement does not cover "tool control"
([staff](https://forum.cursor.com/t/what-happened-to-custom-modes/147068), 2025-12-23).
The run modes decide when a command needs approval, and the sandbox decides what a command
can reach. Neither takes the tool away (docs).

**It can be refused.** Two controls refuse shell calls. Neither removes the tool, so
neither makes a session Rune-only.

- In the editor and in cloud agents, a project hook in `.cursor/hooks.json`. A
  `beforeShellExecution` hook, or a `preToolUse` hook with the matcher `Shell`, can answer
  `"permission": "deny"` with an `agent_message` for the model (docs). The hook runs only
  in a trusted workspace. By default a hook that crashes or times out lets the command
  through; `"failClosed": true` reverses that (docs).
- In the CLI, `.cursor/cli.json` in the project, with `permissions.deny` entries of the
  form `Shell(commandBase)`, where glob patterns are supported and deny wins over allow
  (docs). The documentation shows no entry that denies every command. Whether `Shell(*)`
  does is unknown.

**Allow Rune.** In the CLI, `Mcp(rune:*)` in `permissions.allow` (docs). In the editor the
user approves, and no project file was found that does it for them (docs).

**Subagents and cloud agents.** A subagent has the parent's tools (docs). A cloud agent
takes its MCP servers from the team's configuration, not from the project's file (docs).
Whether a shell hook holds inside a subagent is unknown.

Sources: [hooks](https://cursor.com/docs/hooks),
[CLI permissions](https://cursor.com/docs/cli/reference/permissions),
[MCP](https://cursor.com/docs/context/mcp).

### VS Code, Copilot agent mode

Not tested. The statements are from the documentation as of VS Code 1.146 and from the
source of [`microsoft/vscode`](https://github.com/microsoft/vscode) at commit `6e68549`.
VS Code has two ways to run an agent, and they differ here as they do in
[client compatibility](../mcp.md#vs-code-copilot-agent-mode): the Local harness, which is
deprecated and planned for removal in February 2027, and the Copilot harness.

**Register.** `.vscode/mcp.json` for the Local harness, and `.mcp.json` at the workspace
root for the Copilot harness (docs). A workspace's server starts only in a trusted
workspace (docs).

**Nothing removes the terminal for a workspace.** The tools that run something are the
terminal tools (`runInTerminal` and its companions), the task tools (`runTask`,
`createAndRunTask`), `runNotebookCell`, `runSubagent`, and `vscode/runCommand`, which runs
any editor command (source, docs). No workspace setting was found that removes any of them
(source, docs). `chat.tools.terminal.autoApprove` cannot: "A false rule requires approval.
It does not block the command", and the feature is "not a security boundary" (docs).
`chat.agent.enabled` switches agent mode off altogether, Rune's tools with it (docs).

**A custom agent can be Rune-only.** `.github/agents/rune.agent.md` with a `tools` list
that names only Rune's:

```markdown
---
description: Works on this project through its Runefile only.
tools: ['rune/*']
agents: []
---
```

In the Local harness the list is an allowlist: a tool that is not named is not enabled for
that agent (source), and `agents: []` prevents subagents (docs). Three limits:

- It holds for that agent only. The default agent and every other custom agent keep the
  terminal.
- The user can add tools to the agent in the tools picker, which for a workspace agent
  writes the change into the file (source).
- In the Copilot harness an empty or missing `tools` list means every tool (source), where
  GitHub's documentation for the same file says an empty list disables all tools. Both are
  reported. A list with one entry, as above, is passed on as written (source). Whether the
  harness then still offers its own shell tool is unknown.

**Allow Rune.** The user approves a tool, and VS Code keeps that choice in its own storage
for the workspace or the profile (source). No workspace file was found that approves an
MCP server's tools (docs).

Sources:
[custom agent file reference](https://code.visualstudio.com/docs/agents/reference/custom-agent-file-reference),
[approvals](https://code.visualstudio.com/docs/agents/run/approvals),
[MCP configuration reference](https://code.visualstudio.com/docs/agents/reference/mcp-configuration),
and in the source: `src/vs/workbench/contrib/chat/browser/widget/input/chatSelectedTools.ts`,
`src/vs/workbench/contrib/chat/common/tools/languageModelToolsService.ts`,
`src/vs/platform/agentHost/node/copilot/copilotPluginConverters.ts`.

### Zed

Not tested. The statements are from the documentation as of Zed 1.28.1 and from the source
of [`zed-industries/zed`](https://github.com/zed-industries/zed) at commit `430a781`, and
they are about Zed's own agent.

**Register.** `context_servers` in `.zed/settings.json` in the project (source), which Zed
reads only for a worktree the user trusts (docs).

**A project file can do nothing more.** The tool that runs commands is `terminal`, and
`spawn_agent` starts a subagent with the parent's profile (source). Which tools an agent
has is decided by its profile, and whether a call needs approval by
`agent.tool_permissions`. Both are under `agent`, and the settings a project file may
carry have no `agent` key (source). So the restriction can only live in the user's own
settings:

- A profile under `agent.profiles` is an allowlist. A tool is offered only when the
  profile sets it to `true`, so a profile that does not name `terminal` has none, and a
  disabled tool is not sent to the model at all (source).
- The profile enables Rune's tools one by one under `context_servers.rune.tools`, or all
  servers at once with `enable_all_context_servers` (source). There is no "every tool of
  this server".
- `agent.tool_permissions` lets a Rune tool run without a question with a `default` of
  `"allow"` under the key `mcp:rune:<tool>` (docs, source).

The user can switch profile in the agent panel at any time (docs). An external agent that
Zed runs, such as Claude Code or Codex, ignores Zed's profiles and uses its own
configuration (docs).

Sources: [agent profiles](https://zed.dev/docs/ai/agent-profiles),
[tool permissions](https://zed.dev/docs/ai/tool-permissions), and in `crates/`:
`settings_content/src/project.rs`, `agent_settings/src/agent_profile.rs`,
`agent/src/thread.rs`.

## Hooks

Issue #46 asks whether a client hook can redirect a shell command that matches a task to
that task's tool. It cannot, in any of the five clients.

| | Refuse the call, with a message for the model | Change the call's input | Replace it with a call to another tool |
|---|---|---|---|
| Claude Code, `PreToolUse` | Yes (docs; observed: c5, c6) | Yes, for the same tool (docs) | No (docs describe none) |
| Codex CLI, `PreToolUse` | Yes (source) | Yes, for the same tool (source) | No (source) |
| Cursor, `beforeShellExecution` and `preToolUse` | Yes (docs) | `preToolUse` only (docs) | No (docs describe none) |
| VS Code | Yes, in both harnesses (docs) | In the Local harness (docs). Unknown in the Copilot harness | No (docs describe none) |
| Zed | No hooks found (source) | | |

What a hook can do is refuse and say where to go. That worked: a Claude Code hook refused
`rune build` in the shell with the message "For `rune <task>`, call the tool
`mcp__rune__<task>`", and the model called `mcp__rune__build` next (observed: c6).

It is still not the restriction, for three reasons.

- Where the shell can be removed, a hook adds nothing, and it leaves the shell tool in
  front of the model.
- Where the shell cannot be removed, the hook is a second process that must never fail
  open. In Cursor a hook that crashes lets the command through unless it is told
  otherwise (docs).
- "Matching" a command to a task is sound only for the literal `rune <task>`. A task has
  parameters, a working directory, dependencies and hooks that a command line does not
  show. `go test ./...` typed into a shell is not the `test` task. A hook that changed the
  command's input to `rune test` would also run the task through Rune's command line, where
  the refusals of the agent surface do not apply.

A refusal message that names Rune's tools is a courtesy a setup may add where a client's
shell cannot be removed. It is reported as "refused, not removed".

## The agent executor

A task with the `agent` executor starts the command in `agent_cmd` with the task's body as
the prompt. Rune opens an MCP endpoint for that agent, on loopback and behind a token,
that never lets it run a destructive task, and passes the endpoint in two environment
variables, `RUNE_MCP_URL` and `RUNE_MCP_TOKEN`. That is all it does. It does not register
the endpoint with the agent, and it does not touch the agent's tools.

So with the `agent_cmd` that Rune's own error message suggests, the agent Rune launches
has a shell and has no Rune tools:

```rune
set agent_cmd := ["claude", "-p"]
```

Asked to run a shell command and to call the `status` task, that agent ran the command and
found no Rune tool (observed: a1, with the user's own settings left out).

The settings that exist today are enough to make the launched agent Rune-only. The command
line removes the shell, and a small file hands the endpoint to the agent:

```rune
set agent_cmd := ["claude", "--restricted", "--strict-mcp-config", "--mcp-config", "agent-mcp.json", "--allowedTools", "mcp__rune", "-p"]
```

```json
{
  "mcpServers": {
    "rune": {
      "type": "http",
      "url": "${RUNE_MCP_URL}",
      "headers": { "Authorization": "Bearer ${RUNE_MCP_TOKEN}" }
    }
  }
}
```

With these, the same agent reported that it had no shell tool, and called `status`
(observed: a2). That run kept no tool list. The client's own list for the same options is
in c4.

What the chosen approach means for the executor:

- **A project's Rune-only files reach the launched agent only in part.** Claude Code
  started with `-p` in the project applies the project's `deny` rules, so the agent loses
  its shell, and ignores the `allow` rules of an untrusted workspace (observed: c2). The
  project's `.mcp.json` would then give it a second, separate `rune mcp`, with the
  operator's filters and not the executor's endpoint.
- **The executor is left as it is.** This note changes no behavior. That `RUNE_MCP_URL`
  and `RUNE_MCP_TOKEN` exist is written nowhere in the user documentation, and the lines
  above belong there.
- **A launcher is the natural next step**, and it is the deferred approach of the
  [decision](#decision): Rune already builds this command line once, for its own agent.

## Enforcement boundaries

A Rune-only session narrows what an agent can start. It is not a sandbox. Each entry says
what can still happen, why removing the shell does not stop it, and what closes it.

1. **The agent writes the Runefile.** With the shell denied and file edits allowed, an
   agent added a task and called it (observed: c7), and rewrote the body of an existing
   task and called that (observed: c9 in Claude Code, y3 in Codex). The command it chose
   ran. *Why*: `rune mcp` follows its Runefile while it runs. *What closes it*: for the
   running server, `rune mcp --no-reload`, which Rune then keeps to the tasks read at
   start: the new task never became a tool (observed: c8) and the old body ran (observed:
   c10, y4). The next server to start reads the changed file, so beyond one session only a
   person reading the diff closes it, or a client rule that denies writes to the Runefile
   and every file it imports.

2. **The agent writes what a task runs.** A `test` task runs the project's tests, and a
   `build` task its build scripts. An agent that can edit code and call `test` runs code it
   wrote. *Why*: that is what the task is for. *What closes it*: nothing in scope. Taking
   the file-writing tools away closes it and leaves an agent that can only read and run.
   Limiting what the code can reach is sandboxing, below.

3. **The agent writes the client's configuration.** The restriction is a setting in a file
   inside the project. *What closes it*: Claude Code asks before a write to `.claude/` or
   `.mcp.json` unless permission checks are skipped (docs). Codex keeps `.codex` read-only
   in its `workspace-write` sandbox and not in `danger-full-access` (source). Zed asks
   before an edit to its settings files (source). Cursor protects "sensitive Cursor config
   files" without naming them (docs). VS Code is unknown. None was tested.

4. **Other profiles, modes and subagents.** A restriction that is one agent's is not the
   project's. In VS Code every other agent keeps the terminal, and a named subagent uses
   its own tool list (source). In Zed the user switches profile (docs). In Cursor a cloud
   agent takes its servers from the team's configuration (docs). In Claude Code a
   subagent is held to the same deny rules (docs; observed: c11), and `RemoteTrigger`,
   which starts an agent in the vendor's cloud whose tools are unknown, is on the deny
   list for that reason. In Codex, subagents are turned off because their inheritance was
   not tested. *What closes it*: per client, as stated. In VS Code and Zed, nothing a
   project file can carry.

5. **A level above the project.** In Codex an option on the command line beats the project
   file, so `codex --enable shell_tool` gives the shell back (source). In Claude Code no
   level can allow what another denies (docs), but a session can be started without the
   project's settings at all (docs). *What closes it*: an organization's managed settings,
   in both (docs, source). They are outside a project and outside what Rune writes.

6. **The user.** Everything a setup writes is a file the user can edit, and every client
   has a way to start without it. *What closes it*: nothing, and nothing should. Rune-only
   mode limits the agent. It is not a control over the person.

7. **A task is not sandboxed.** Both installed clients run an MCP server outside their
   sandbox: Claude Code's sandbox "covers shell commands only", and MCP servers run outside
   it (docs); Codex starts a server as a plain process, with no sandbox (source). A command
   that the client's shell would have run inside a sandbox runs, as a Rune task, with the
   user's full access. A Rune-only session trades an arbitrary command in a sandbox for a
   fixed set of commands outside one. *What closes it*: enforced sandboxing of tasks, which
   is [issue #51](https://github.com/rune-task-runner/rune/issues/51).

8. **A project that is not trusted.** The same files give different sessions. Claude Code
   applies the `deny` rules and ignores the `allow` rules (observed: c2). Codex ignores
   the whole project file, so the shell is there and Rune is not (recorded). VS Code and
   Zed do not start a workspace's server (docs). *What closes it*: the user trusting the
   project. A setup names that step and does not report an outcome it has not reached.

9. **Other servers and connected apps.** A Rune-only setup removes the client's shell, not
   the tools of other MCP servers. A Codex session with the shell removed still listed 408
   tools of other servers of the account (observed: y2, as the model listed them). Any of
   them may run a command or act somewhere else. *What closes it*: at launch, `--strict-mcp-config` in Claude Code
   (docs). In a project file, nothing found.

10. **A task that takes a command.** A task such as `run cmd:` with the body `{{cmd}}` is
    a shell with one more step. *What closes it*: the Runefile's author. `--allow` and
    `--deny` choose which tasks are tools, and Rune then offers only those. They do not
    look inside a task.

What Rune's own flags do in all of this: `--allow` and `--deny` decide which tasks Rune
offers as tools, `--allow-destructive` which `[confirm]` tasks Rune will run, and
`--no-reload` whether Rune follows a changed Runefile. Each is about what Rune does when
asked. None of them restricts a client.

## What issue #53 must do

The full text for the issue is in
[follow-up-issue-53.md](../../specs/047-rune-only-agent-mode/follow-up-issue-53.md). In
short:

- **Support Claude Code and Codex CLI as Rune-only.** Both remove the shell with a project
  file, and both were seen doing it with the project files alone, in a trusted project: no
  command-running tool, and a Rune call that ran (observed: c12, y5).
- **Treat VS Code as Rune-only within one custom agent**, and ship it only after someone
  has seen it work. Nothing about it was run.
- **Do not offer Cursor or Zed as Rune-only.** Neither can remove its shell from a project
  file. Registration alone may be offered, reported as "Registered only".
- **Report one of the three outcomes**, and report a trust step that is still open.
- **Do not allow more than the operator did.** Leave Codex's approval at its default, and
  in Claude Code keep `[confirm]` tasks behind a question.
- **Register `rune mcp --no-reload` in a Rune-only setup** when the agent keeps a tool that
  writes files, and say what that costs: a person who edits the Runefile restarts the
  session.
- **Prove it before claiming it.** A client and version is listed as Rune-only when its own
  tool list has been seen to hold Rune's tools and no command-running tool.
