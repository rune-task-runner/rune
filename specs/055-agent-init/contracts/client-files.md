# Contract: the files of each client

What `rune agent init` writes, which entries are Rune's, and how an existing file is merged.
The client facts come from the
[design note](../../../docs/design/rune-only-agent-mode.md). `ARGS` below stands for

```text
["mcp", "--file", "<absolute path of the Runefile>", "--no-reload"]
```

or, with `--unpinned`, `["mcp", "--no-reload"]`.

## Merge rules, all clients

1. Every byte outside an owned entry stays as it was.
2. An owned key that is missing is added. One that holds the wanted value is left. One that
   holds another value is a conflict, replaced only with `--force`.
3. A rule in a list is added when missing. No rule is removed, moved or written twice.
4. A key the command does not own is never changed, with or without `--force`. That includes
   every other key inside the server's own entry, such as `env` or `tool_timeout_sec`.
5. A new file is the result of the same steps applied to an empty document.
6. A file that is empty or holds only white space is an empty document. That holds for the
   agent file of VS Code too: it gets the content a new project would.
7. A file that starts with a UTF-8 byte order mark is edited as if the mark were not there,
   and keeps it.

## Claude Code

Checked versions: recorded in the adapter and in the documentation. Strongest outcome:
Rune-only.

### `.mcp.json`

```json
{
  "mcpServers": {
    "rune": {
      "type": "stdio",
      "command": "rune",
      "args": ARGS
    }
  }
}
```

Owned: `mcpServers.rune.type`, `mcpServers.rune.command`, `mcpServers.rune.args`.

### `.claude/settings.json`

```json
{
  "permissions": {
    "deny": ["Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"],
    "allow": ["mcp__rune"]
  }
}
```

Owned: each of the six `permissions.deny` rules, and the `permissions.allow` rule
`mcp__rune`.

Never written: `ask` rules, `defaultMode`, hooks, `enableAllProjectMcpServers`, anything in
`.claude/settings.local.json`.

Read and reported, not changed:

- `permissions.deny` holds `mcp__rune` or `mcp__rune__*`, each of which denies every Rune
  tool: a conflict that `--force` does not override.
- `permissions.deny` holds a rule that starts with `mcp__rune__`: a note.
- `permissions.ask` holds `mcp__rune` or a rule that starts with `mcp__rune__`: a note.

Removes: `Bash`, `PowerShell`, `Monitor`, `Workflow`, `CronCreate`, `RemoteTrigger`.

## Codex CLI

Strongest outcome: Rune-only.

### `.codex/config.toml`

```toml
[features]
shell_tool = false

[agents]
enabled = false

[mcp_servers.rune]
command = "rune"
args = ARGS
```

Owned: `features.shell_tool`, `agents.enabled`, `mcp_servers.rune.command`,
`mcp_servers.rune.args`. Strings are written as TOML basic strings, in double quotes.

Never written: `unified_exec`, `multi_agent`, `default_tools_approval_mode`, any
`approval_mode`, `cwd`, `env`.

How a key is added: after the last key of its table when the table's header is in the file;
otherwise the header and the key are appended to the file, after one empty line. A file that
defines the table another way (an inline table, dotted keys at the top) cannot be extended in
place: the command writes nothing and prints the lines.

Removes: `exec_command`, `write_stdin`, and the tools that start a subagent. Their names
differ by version: `spawn_agent`, `followup_task`, `interrupt_agent`, `list_agents`,
`send_message` and `wait_agent` in 0.167.0, and `spawn_agent`, `wait_agent`, `send_input`,
`resume_agent` and `close_agent` in 0.169.0.

## VS Code

Strongest outcome: Rune-only within the custom agent `rune`. No version is checked, so the
outcome reported today is "Registered only".

### `.vscode/mcp.json`

```json
{
  "servers": {
    "rune": {
      "type": "stdio",
      "command": "rune",
      "args": ARGS
    }
  }
}
```

Owned: `servers.rune.type`, `servers.rune.command`, `servers.rune.args`. Comments in this
file are kept.

### `.mcp.json`

As for Claude Code. It is the file VS Code's Copilot harness reads. The entry is the one the
Claude Code adapter writes, so the two adapters never conflict over it.

### `.github/agents/rune.agent.md`

```markdown
---
description: Works on this project through its Runefile only.
tools: ['rune/*']
agents: []
---

Run this project's tasks with the rune tools. You have no terminal.
```

Owned: the whole file. Present with the same content: unchanged. Line endings are not part of
the comparison, so a checkout that turned them into `\r\n` is the same file and is left as it
is. Present with other content: a conflict, replaced with `--force`. In `--check` output the
entry is named `content`. The `tools` list is a constant of the adapter with one
entry; no code path writes it empty.

Keeps, in every other agent: `runInTerminal`, `runTask`, `createAndRunTask`,
`runNotebookCell`, `runSubagent`, `vscode/runCommand`.

## Removing a setup by hand

The documentation lists, per client, exactly the owned entries above. Removing them and
nothing else undoes a setup. A deny rule that was in the file before the first run is the
person's: the report of that run named only the rules it added.
