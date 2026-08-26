# User guide

> A guided tour of Rune, in reading order. This page is the connective tissue: it explains the
> shape of the tool and points you to the how-to guide or reference for each capability, rather
> than repeating them. Read it top to bottom once; come back to the links as you need them.

## 1. Start here

If you're brand new, do these two first — they're the fastest path to a working task:

1. **[What is Rune?](../overview.md)** — the idea, the mental model, and when to reach for it.
2. **[Getting started](../getting-started.md)** — install, write a tiny `Runefile`, run a task.

## 2. The core model

A `Runefile` is **named blocks of commands** you run by name, with **declared dependencies**
between them. Everything else builds on that. For the complete syntax in one place, keep the
**[Runefile language reference](../runefile.md)** open.

## 3. Composing work

- **[Dependencies & post-hooks](../how-to/dependencies-and-hooks.md)** — order work; run things
  before, after, and on failure.
- **[Parameters](../how-to/parameters.md)** — defaulted, required, and variadic inputs.
- **[Parallelism](../how-to/parallelism.md)** — run independent prerequisites concurrently.
- **[Imports & modules](../how-to/imports-and-modules.md)** — split and namespace task files
  across a repo.

## 4. Controlling how tasks run

- **[Executors](../how-to/executors.md)** — shell, Python, Node, or an AI agent as the task body.
- **[Caching](../how-to/caching.md)** — opt-in, content-hash skipping for expensive tasks.
- **[OS filtering](../how-to/os-filtering.md)** — scope a task to an operating system.
- **[Settings & dotenv](../how-to/settings-and-dotenv.md)** — project settings and `.env` files.

## 5. Sharing tasks with AI agents

Rune is AI-native: tasks are first-class MCP tools, read-only by default and gated when
destructive. See the **[AI agents (MCP) reference](../mcp.md)** and the
**[MCP walkthrough](../use-cases/mcp-agents.md)**.

## 6. Putting it together

When you're wiring up a real project, jump to the **[use-case walkthroughs](../use-cases/README.md)**
(Python, Node, AI agents) — each shows a complete Runefile built from the pieces above.

> [!TIP]
> Hit an error? Rune's diagnostics point at the exact `file:line:col`. The
> [Troubleshooting guide](../troubleshooting.md) maps common messages to fixes.

---

**Next:** [How-to guides](../how-to/README.md) · [Use cases](../use-cases/README.md) · [CLI reference](../cli.md) · [Runefile language](../runefile.md)
