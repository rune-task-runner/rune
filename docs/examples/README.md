# Examples

> Runnable starting points you can copy into your own project. Every example is a
> self-contained directory (a `Runefile` plus a short README) and is verified on every CI
> run, so it always reflects working syntax.

New to Rune? Read **[What is Rune?](../overview.md)** and **[Getting started](../getting-started.md)**
first, then pick the example closest to your project below.

> [!TIP]
> Want a guided walkthrough instead of a bare example? See the
> **[use-case walkthroughs](../use-cases/README.md)** for [Python](../use-cases/python-project.md),
> [Node](../use-cases/node-project.md), and [AI agents (MCP)](../use-cases/mcp-agents.md) —
> each explains *why* the Runefile is written the way it is.

## Basics

| Example | What it shows |
|---------|---------------|
| [getting-started](getting-started/README.md) | Your first Runefile: a task, a parameter, a dependency. |

## Project shapes

Start here if you want a layout for a particular kind of project.

| Example | What it shows | Needs |
|---------|---------------|-------|
| [go-service](go-service/README.md) | Fetch → build → test → run for a Go service. | — |
| [node-project](node-project/README.md) | npm workflow + a JavaScript task body. | node |
| [python-project](python-project/README.md) | venv/pytest workflow + a Python task body. | python3 |
| [monorepo](monorepo/README.md) | Shared tasks via `import`, per-service `mod` namespaces. | — |
| [ci-cd](ci-cd/README.md) | A CI gate (lint/test/build) + a gated deploy + exit codes. | — |
| [docker-workflow](docker-workflow/README.md) | Build/run/push lifecycle for a container image. | docker |
| [polyglot](polyglot/README.md) | One pipeline across shell, Python, and Node. | python3, node |
| [agent-driven](agent-driven/README.md) | An AI-agent task + MCP exposure + destructive gating. | agent CLI |

## Capability spotlights

Each isolates one feature so you can see exactly how it works.

| Example | Capability |
|---------|-----------|
| [dependencies](dependencies/README.md) | Dependencies, post-hooks, and failure hooks. |
| [parameters](parameters/README.md) | Defaulted, required, and variadic parameters. |
| [caching](caching/README.md) | Opt-in content-hash caching (`[cache(...)]`). |
| [parallel](parallel/README.md) | Running independent prerequisites concurrently. |
| [settings-dotenv](settings-dotenv/README.md) | Settings, `set export`, and `.env` loading. |
| [secret-masking](secret-masking/README.md) | Sensitive values masked as `***` in all output. |
| [os-filtering](os-filtering/README.md) | Tasks restricted to a specific OS. |

## How to run any example

```sh
# From inside an example directory:
rune --list          # see its tasks
rune <task>          # run one

# Or point at it without changing directory:
rune --file <path>/Runefile --list
```

Each example's README states its **prerequisites** (most need nothing beyond Rune) and the
**expected output**, so you can confirm it works. Examples needing an interpreter, container
runtime, or agent CLI say so up front.
