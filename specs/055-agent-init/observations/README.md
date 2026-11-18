# Observations

These are the runs behind the client versions that `rune agent init` lists as checked, beyond
the two that spec 047 recorded. Each run has an id, a row below, and a file with what the
client reported. The ids continue those of
[spec 047](../../047-rune-only-agent-mode/observations/README.md), whose prompts and helper
scripts these runs use.

Made on 2026-11-18, on Linux x86_64, with `rune` built from this branch and first on `PATH`.

| Directory | Client | Model |
|---|---|---|
| `claude-code/` | Claude Code 2.1.326, run as `claude -p` | `claude-sonnet-5-5` |
| `codex/` | Codex CLI 0.169.0, run as `codex exec` | `gpt-6.2-astra` |

VS Code has no run. It is not installed on the machine, and the machine has no display. Its
adapter lists no checked version.

## What a version needs before it is listed

The [setup outcome contract](../../047-rune-only-agent-mode/contracts/setup-outcome.md) of
spec 047 asks for four things, seen in a session started the way the outcome claims:

1. The client's tool list holds Rune's tools.
2. The client's own listing holds no command-running tool.
3. A call to a Rune tool runs.
4. A subagent has no command-running tool, or cannot be started.

## The project

A new copy of the scenario of the compatibility research,
[`test/integration/testdata/clients/scenario`](../../../test/integration/testdata/clients/scenario/Runefile),
in a directory inside this repository, which both clients already trust. The only other files
in it were the ones the command wrote:

```sh
rune agent init claude
rune agent init codex
```

Both runs reported "Registered only" with the reason "this version was not checked", as they
must for a version that had no run yet. The files were:

`.mcp.json`

```json
{
  "mcpServers": {
    "rune": {
      "type": "stdio",
      "command": "rune",
      "args": ["mcp", "--file", "PROJECT/Runefile", "--no-reload"]
    }
  }
}
```

`.claude/settings.json`

```json
{
  "permissions": {
    "deny": ["Bash", "PowerShell", "Monitor", "Workflow", "CronCreate", "RemoteTrigger"],
    "allow": ["mcp__rune"]
  }
}
```

`.codex/config.toml`

```toml
[features]
shell_tool = false

[agents]
enabled = false

[mcp_servers.rune]
command = "rune"
args = ["mcp", "--file", "PROJECT/Runefile", "--no-reload"]
```

The host has bubblewrap, so the task ran in Rune's sandbox, which is the default of the
registered server.

## Claude Code

Started in the project as run c12 of spec 047 was, with prompt B of spec 047:

```sh
claude -p PROMPT --strict-mcp-config --mcp-config .mcp.json --model sonnet \
  --setting-sources project --output-format stream-json --verbose
```

None of these options sets a tool. `--setting-sources project` keeps the user's own settings
out, and `--strict-mcp-config` starts only the server of the file.

| Id | Result |
|---|---|
| [c13](claude-code/c13.txt) | `Bash`, `Monitor`, `Workflow`, `CronCreate` and `RemoteTrigger` are not in the tool list of the `system/init` event; `PowerShell` is in no list on Linux. The eight `mcp__rune__` tools are in it. `status` ran, allowed by the project file. The subagent had no shell tool. No call was refused. The one line on stderr is about standard input, which the run did not redirect. |

All four points hold. Claude Code 2.1.326 is listed as checked.

## Codex CLI

### Captures

The request Codex would send a model, captured as in spec 047 with
[`capture.py`](../../047-rune-only-agent-mode/observations/codex/capture.py) and listed with
[`tools.py`](../../047-rune-only-agent-mode/observations/codex/tools.py). No model is called.

```sh
python3 -I capture.py 18455 x8.cap &
codex exec --ephemeral --disable enable_request_compression \
  -c model_provider=cap \
  -c 'model_providers.cap={name="cap", base_url="http://127.0.0.1:18455/v1", wire_api="responses", request_max_retries=0, stream_max_retries=0}' \
  "say hi"
python3 -I tools.py x8.cap
```

| Id | `.codex/config.toml` | Result |
|---|---|---|
| [x8](codex/x8.txt) | The file the command wrote | No `exec_command`, no `write_stdin` and no subagent tool. The eight `mcp__rune.` tools are in the list. |
| [x9](codex/x9.txt) | The `[mcp_servers.rune]` table only, for comparison | `exec_command` and `write_stdin` are there, and five subagent tools: `multi_agent_v1.spawn_agent`, `wait_agent`, `send_input`, `resume_agent` and `close_agent`. |

Two things differ from the captures of 0.167.0. The subagent tools have other names
(`multi_agent_v1.*`, where 0.167.0 had `collaboration.*`). And Codex has no model metadata for
the capture's provider, so it sent every tool at the top level and no `exec` tool; x9 shows
that the command-running tools are still in such a request when nothing removes them.

### Run

With the account's model, the file the command wrote, and nothing on the command line but
the prompt:

```sh
codex exec --ephemeral --json PROMPT
```

| Id | Prompt | Result |
|---|---|---|
| [y6](codex/y6.txt) | B | "SHELL: NO TOOL". `rune/status` completed: "RUNE: RAN status-ok". "SUBAGENT: NO SUBAGENT TOOL". The model listed the eight `mcp__rune__` tools and no `exec_command` or `write_stdin`. |

All four points hold: the listing is x8, the call is y6. Codex CLI 0.169.0 is listed as
checked. Codex CLI 0.168 was not run and is not listed.

## How the runs were kept apart from the user's configuration

No file of the user account was changed. The project directory was inside this repository,
because both clients read a project's files only where the user already trusts the project,
and it was removed afterwards. Claude Code read settings from the project only. Codex read
the user's configuration, where the trust is recorded, and so its replies list tools of the
account's other servers.

## What was replaced

- The path of the project directory, with `PROJECT`.
- The home directory, with `~`.
- The names of the tools of other MCP servers of the account, with a count.

Nothing else was changed. Replies are word for word.

## Repeating a run

As in [spec 047](../../047-rune-only-agent-mode/observations/README.md#repeating-a-run), with
one difference: the project files are not copied from a table. They are written by
`rune agent init claude` and `rune agent init codex` in the project.
