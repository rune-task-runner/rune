# Use case: exposing tasks to AI agents (MCP)

> Let an AI agent run your tasks as tools over the Model Context Protocol — and write a task
> whose body *is* a natural-language instruction — safely. This walkthrough assumes you have
> an agent CLI (e.g. `claude`, `codex`) available for the agent-executor task.

**Backing example:** [`examples/agent-driven`](../examples/agent-driven/README.md) ·
**Features:** [executors (agent)](../how-to/executors.md), [MCP exposure & security](../mcp.md) ·
**Prerequisites:** an agent CLI for the `summarize` task

## The Runefile

```rune
# An agent-driven workflow. The `summarize` task body is a natural-language
# instruction run by an installed agent CLI (the agent executor). Tasks are also
# exposed to agents over MCP; destructive tasks are refused unless the operator
# allows them, and only [readonly] tasks claim to be read-only. Secrets always
# come from the environment, never this file.

# Drive an AI agent with a natural-language instruction (needs an agent CLI).
summarize (agent):
    Summarize the most recent git changes in one short paragraph.

# A read-only task an agent may call as a tool.
[readonly]
[doc("List the project's documented tasks")]
list-tasks:
    @echo "rune --list"

# A destructive task: gated, so an agent cannot run it unattended.
[confirm("Really reset the local database?")]
[doc("Reset the local database (destructive)")]
reset-db:
    @echo "dropping and recreating the local database"
```

## Run it

```sh
rune summarize
```

A short, agent-written summary of recent changes (the exact text depends on your agent and
repository). `list-tasks` is marked `[readonly]`, so it tells clients it changes nothing.

## How agents see your tasks — and why it's safe

- **`summarize (agent)`** uses the **agent executor**: its body is an instruction handed to an
  installed agent CLI. See [Executors](../how-to/executors.md).
- Exposed over MCP, tasks become agent-callable tools. The security posture is the point:

  > [!IMPORTANT]
  > Destructive tasks are **refused by default**. They are marked with `[confirm(...)]`
  > (which maps to the MCP *destructive* hint), so an agent can't run `reset-db` unless the
  > server is started with `--allow-destructive reset-db`. **Secrets come from the environment only**, never
  > the Runefile, and are never shown in any task description exposed to an agent.

- **`[doc(...)]`** gives each tool a clear description an agent can reason about, without
  leaking anything sensitive.

For the full model — how to start the MCP server, the trust boundary, and remote endpoints —
see the [AI agents (MCP) reference](../mcp.md).

---

**Next:** [MCP reference](../mcp.md) · [Executors](../how-to/executors.md) · [Examples](../examples/README.md)
