# Agent-driven workflow

> **Use case:** let an AI agent run your tasks — and write a task whose body *is* a
> natural-language instruction — safely.

**Demonstrates:** agent executor, MCP exposure, destructive gating  ·  **Guide:** [AI agents (MCP)](../../mcp.md)

**Prerequisites:** an agent CLI (e.g. `claude`, `codex`) for the `summarize` task

## Run it

```sh
rune summarize
```

## Expected output

A short, agent-written summary of recent changes (the exact text depends on your agent and
repository). The `list-tasks` task is a safe, read-only tool an agent may call.

## How it works

- `summarize (agent)` runs its body as an instruction via an installed agent CLI.
- Exposed over MCP, tasks are **read-only by default**; `reset-db` is gated with `[confirm]`,
  so an agent can't run it unless the server is started with `--allow-destructive reset-db`.
- **Secrets come from the environment only** — never written in the Runefile, and never shown
  in any task description exposed to an agent.

Tier-B verification skips this example when no agent CLI is available; the Runefile is still
statically validated.
