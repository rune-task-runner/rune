# Set up an agent client

> Give Claude Code, Codex CLI or VS Code this project's tasks as tools and take the client's
> own shell away, with one command that says what it reached.

Registering `rune mcp` with an agent client adds your tasks to the agent's tools. It takes
nothing away: the client's shell is still there, and an agent that is offered a `plan` task
can still type `tofu apply`. `rune agent init` writes the project files that register the
server, remove the client's command-running tools and let Rune's tools run.

```sh
rune agent init claude            # Claude Code
rune agent init codex             # Codex CLI
rune agent init vscode            # VS Code, Copilot agent mode
rune agent init claude --dry-run  # print the difference, write nothing
```

```text
$ rune agent init claude
.mcp.json: created: server "rune" runs: rune mcp --file /home/me/shop/Runefile --no-reload
.claude/settings.json: created: deny Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger; allow mcp__rune
client: Claude Code 2.1.326 (checked: 2.1.325, 2.1.326)
outcome: Rune-only, pending 2 steps
step: trust the project: start claude in /home/me/shop and accept the trust question. Until then the deny rules apply and the allow rule does not: the session has no shell and Rune's tools are refused.
step: approve the server "rune" when Claude Code asks, and start a new session. Until then the session has no Rune tools.
note: the server keeps the tasks it read at start (--no-reload). After an edit to the Runefile, start a new session.
note: [confirm] tasks are not approved by this setup. Rune refuses them, or asks you for each call.
note: the server is registered with this checkout's path. For a file you commit, use --unpinned.
note: a task an agent calls runs in Rune's sandbox, which needs bubblewrap on Linux. Where there is none, each call is refused until the server runs with --sandbox off.
check: claude -p hi --output-format stream-json --verbose
check: the "tools" list in the first line of its output has names that start with mcp__rune__ and none of: Bash, PowerShell, Monitor, Workflow, CronCreate, RemoteTrigger
```

The command reads your Runefile and the client's files, and asks the client for its version.
It runs no task, starts no server, and writes only inside the project: never your user
settings.

## The three outcomes

Every run ends by naming one of three outcomes. No other word describes a setup.

| Outcome | What it says |
|---------|--------------|
| **Rune-only** | Every session the client starts in this project has Rune's tools and no tool that runs a command. |
| **Rune-only within** a scope | The same, inside one named custom agent. Sessions outside it keep their command-running tools. |
| **Registered only** | Rune's tools are there, and so are the client's command-running tools. |

A step that is left to you, such as trusting the project, is part of the outcome: it is
reported as pending, and each step says what a session has until you take it.

"Rune-only" is said only for a client version that was checked: a recorded run with exactly
that version showed Rune's tools in the client's own tool list, no command-running tool in
it, a Rune call that ran, and a subagent without a command-running tool. For any other
version the files are written all the same, the outcome is "Registered only", and the report
names the tools that were not checked. The `check:` lines at the end of the report are how
you look for yourself.

| Client | Files in the project | Strongest outcome | Versions checked |
|--------|----------------------|-------------------|------------------|
| Claude Code | `.mcp.json`, `.claude/settings.json` | Rune-only | 2.1.325, 2.1.326 |
| Codex CLI | `.codex/config.toml` | Rune-only | 0.167.0, 0.169.0 |
| VS Code | `.vscode/mcp.json`, `.mcp.json`, `.github/agents/rune.agent.md` | Rune-only within the custom agent `rune` | none |

VS Code was not run, so `rune agent init vscode` reports "Registered only" for every version
today. Cursor and Zed have no adapter: neither can remove its shell from a project file.
[Rune-only agent mode](../design/rune-only-agent-mode.md) has the research behind this table.

The exit status follows the outcome: `0` for "Rune-only" and "Rune-only within", `1` for
"Registered only". A script must not take "Registered only" for a setup without a shell.

## What is written

The server is the same for every client: `rune mcp` with the path of this project's Runefile
and `--no-reload`. Open the client in the project's directory, which is where it reads these
files; the path then makes the server serve this project whatever directory the client starts
it in.

### Claude Code

`.mcp.json`:

```json
{
  "mcpServers": {
    "rune": {
      "type": "stdio",
      "command": "rune",
      "args": ["mcp", "--file", "/home/me/shop/Runefile", "--no-reload"]
    }
  }
}
```

`.claude/settings.json`:

```json
{
  "permissions": {
    "deny": ["Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"],
    "allow": ["mcp__rune"]
  }
}
```

A deny rule that is a bare tool name takes the tool away from the model. The allow rule lets
every tool of the server run without a question.

Steps left to you:

1. **Trust the project.** Start `claude` in the project and accept the trust question. Until
   then Claude Code applies the deny rules and ignores the allow rule: the session has no
   shell, and Rune's tools are refused.
2. **Approve the server** `rune` when Claude Code asks, and start a new session.

Check: run `claude -p hi --output-format stream-json --verbose` in the project. The `tools`
list in the first line of its output has names that start with `mcp__rune__` and none of the
six denied names.

### Codex CLI

`.codex/config.toml`:

```toml
[features]
shell_tool = false

[agents]
enabled = false

[mcp_servers.rune]
command = "rune"
args = ["mcp", "--file", "/home/me/shop/Runefile", "--no-reload"]
```

`shell_tool = false` removes the tools that run a command, and `[agents] enabled = false`
the tools that start a subagent. Approval is left at its default: Codex asks before a tool
that is marked destructive.

Step left to you: **trust the project.** Start `codex` in the project and accept the trust
question, then start a new session. Until then Codex ignores the whole file: the session has
its shell and no Rune tools.

Check: `codex exec "Call the rune tool of one task. Then say whether you have a tool that
runs a shell command, and whether you have a tool that starts a subagent."` Codex prints no
tool list, so the answer is the model's.

An option on the command line outranks the project file: `codex --enable shell_tool` gives
the shell back.

### VS Code

`.vscode/mcp.json`, which VS Code's Local harness reads:

```json
{
  "servers": {
    "rune": {
      "type": "stdio",
      "command": "rune",
      "args": ["mcp", "--file", "/home/me/shop/Runefile", "--no-reload"]
    }
  }
}
```

`.mcp.json`, which the Copilot harness reads, as for Claude Code. And a custom agent in
`.github/agents/rune.agent.md`:

```markdown
---
description: Works on this project through its Runefile only.
tools: ['rune/*']
agents: []
---

Run this project's tasks with the rune tools. You have no terminal.
```

VS Code has no workspace setting that removes the terminal. The restriction is this one
agent's tool list, and you can add tools to it in the tools picker. Every other agent keeps
the terminal tools.

Steps left to you:

1. Open the project in VS Code, **trust the workspace** and start the server `rune` when VS
   Code asks.
2. **Choose the agent `rune`** in the Chat view.

Check: in the Chat view choose the agent `rune` and open its tools. Only the rune tools are
selected, and no terminal tool is.

## Existing files are kept

When a file is already there, the command edits it in place. Other servers, other settings,
keys it does not know, key order, indentation, line endings and comments stay as they were.

- **Rules are only added.** No existing rule is removed, moved or written twice. A deny rule
  is never removed, not even with `--force`.
- **A value that is already there is not changed.** When a key the command sets holds another
  value, the command stops, names the file, the line, the value found and the value it would
  write, and writes nothing. Edit the file, or run again with `--force`, which replaces that
  value and nothing else.
- **A second run writes nothing.** Every file is reported as `unchanged`.
- **All or nothing.** A conflict in one file of a client leaves the others as they were.

```text
$ rune agent init claude
.mcp.json:6:15: error: mcpServers.rune.args is ["mcp"]; rune agent init writes ["mcp", "--file", "/home/me/shop/Runefile", "--no-reload"]. Edit the file, or run again with --force
6 |       "args": ["mcp"]
  |               ^^^^^^^
rune: agent init: 1 conflict(s), nothing written
```

A deny rule `mcp__rune` or `mcp__rune__*` in `.claude/settings.json` denies every Rune tool.
The command reports it and stops: remove the rule yourself if Rune's tools should run. A deny
or ask rule for one tool, such as `mcp__rune__deploy`, is kept and named in a note.

A `.codex/config.toml` that defines `mcp_servers` or `features` as an inline table, or with
dotted keys at the top of the file, cannot be added to in place. The command writes nothing
and prints the setting to add by hand.

A link that leads out of the project is refused. If `.claude` is a symbolic link to the
settings directory of your user, the command stops and names where the link leads.

## Flags

| Flag | Effect |
|------|--------|
| `-f`, `--file <path>` | Set up the project of this Runefile instead of the one found from the working directory. |
| `--dry-run` | Print the difference for each file on standard output, in unified format, and the report on standard error. Nothing is written. |
| `--check` | Say whether the setup is in the project's files. Nothing is written and no program is started. |
| `--force` | Replace a value the command owns that is already there with another value. |
| `--unpinned` | Register the server without the Runefile's path. |

### `--unpinned`: files you commit

The registration holds the absolute path of this checkout's Runefile, which is wrong on every
other machine. For files you commit, register the server without the path:

```sh
rune agent init claude --unpinned
```

The server then serves the Runefile found from the directory the client starts it in. It
needs a Runefile that Rune finds by its name.

### `--check`: is the setup still there?

```text
$ rune agent init codex --check
.codex/config.toml: features.shell_tool: present
.codex/config.toml: agents.enabled: missing
.codex/config.toml: mcp_servers.rune.command: present
.codex/config.toml: mcp_servers.rune.args: present
incomplete: 1 of 4 entries are missing or different. Run: rune agent init codex
```

`--check` reads the files and nothing else, so it gives the same answer in CI as on your
machine. It exits `0` when every entry is present and `1` otherwise. It names no outcome,
because it does not read the client's version. Check with the options the setup was made
with: a setup made with `--unpinned` is checked with `--unpinned`.

## Exit status

| Status | Meaning |
|--------|---------|
| `0` | The outcome is "Rune-only" or "Rune-only within". With `--check`: every entry is present. |
| `1` | The outcome is "Registered only"; or `--check` found the setup not complete; or a file could not be read or written. |
| `2` | A usage error, no Runefile, a conflict, a file that needs a setting added by hand, or a path that leaves the project. |
| `3` | The Runefile has errors, or a client file is not valid JSON or TOML. |

Files were written only with status `0`, and with status `1` for "Registered only".

## Removing a setup

The command owns these entries and nothing else. Take them out and the setup is gone.

| Client | File | Rune's entries |
|--------|------|----------------|
| Claude Code, VS Code | `.mcp.json` | `type`, `command` and `args` of `mcpServers.rune` |
| Claude Code | `.claude/settings.json` | The six names in `permissions.deny` and `mcp__rune` in `permissions.allow` |
| Codex CLI | `.codex/config.toml` | `shell_tool` under `[features]`, `enabled` under `[agents]`, `command` and `args` under `[mcp_servers.rune]` |
| VS Code | `.vscode/mcp.json` | `type`, `command` and `args` of `servers.rune` |
| VS Code | `.github/agents/rune.agent.md` | The whole file |

A rule that was in the file before the first run is yours: the report of that run named only
the rules it added.

## What a setup does not do

- **It does not approve `[confirm]` tasks.** The registration has no `--allow-destructive`,
  so Rune refuses a destructive task or
  [asks you for each call](../mcp.md#approving-a-destructive-call-in-the-client). If you add
  `--allow-destructive clean` to the registration yourself, add an `ask` rule for
  `mcp__rune__clean` to `.claude/settings.json` too: with `mcp__rune` allowed, Claude Code
  would otherwise run it without a question. The command then reports the registration as
  different from its own.
- **It does not turn the sandbox off.** A task an agent calls runs in
  [Rune's sandbox](../mcp.md#the-sandbox). On a system without one, each call is refused
  until you run the server with `--sandbox off`.
- **It is not a control over you.** Every file it writes is yours to edit, and each client
  has a way to start without it.
- **It does not stop an agent that can edit files.** An agent that keeps a file-writing tool
  can change what a task runs. `--no-reload` keeps the running server to the tasks it read
  at start; the next session reads the changed Runefile.
  [Enforcement boundaries](../design/rune-only-agent-mode.md#enforcement-boundaries) lists
  what a session without a shell can still do.

## Agents that are not set up

No Runefile yet? Before you take the shell away, let [`rune agent observe`](learn-tasks-from-agent.md)
record the commands the agent repeats, and add them as tasks with `rune suggest`.

A client this command does not cover, or a session started without the setup, still reads
instruction files. [`rune export`](agent-instructions.md) writes your tasks, their
parameters and their limits into `AGENTS.md` or a Claude skill. That text is guidance: it
tells an agent what to do and stops nothing.

## A task named `agent`

`rune agent` is the command, so a Runefile task named `agent` is run with `rune -- agent`.
The command prints a note when your Runefile has one. The task is still an MCP tool named
`agent`.

---

**Next:** [AI agents (MCP)](../mcp.md) · [Rune-only agent mode](../design/rune-only-agent-mode.md) · [CLI reference](../cli.md)
