# How-to guides

> Task-oriented guides — one capability per page. Each explains the concept, shows the
> syntax, links a runnable example, and calls out the common pitfalls. Come here when you
> already know what you want to do and need to know *how*.

New to Rune? Start with **[What is Rune?](../overview.md)** and **[Getting started](../getting-started.md)**.
For the full syntax in one place, see the **[Runefile language reference](../runefile.md)**;
for a guided tour, see the **[user guide](../user-guide/README.md)**.

| How-to | Capability | Example |
|--------|-----------|---------|
| [Dependencies & post-hooks](dependencies-and-hooks.md) | Order work; run things before/after/on failure | [dependencies](../examples/dependencies/README.md) |
| [Parameters](parameters.md) | Defaulted, required, variadic inputs | [parameters](../examples/parameters/README.md) |
| [Caching](caching.md) | Opt-in content-hash skipping | [caching](../examples/caching/README.md) |
| [Parallelism](parallelism.md) | Run independent prerequisites concurrently | [parallel](../examples/parallel/README.md) |
| [Executors](executors.md) | Shell, Python, Node, agent bodies | [polyglot](../examples/polyglot/README.md) |
| [Settings & dotenv](settings-and-dotenv.md) | Project settings and `.env` | [settings-dotenv](../examples/settings-dotenv/README.md) |
| [Secret masking](secret-masking.md) | Credentials masked as `***` in all output | [secret-masking](../examples/secret-masking/README.md) |
| [Imports & modules](imports-and-modules.md) | Compose and namespace task files | [monorepo](../examples/monorepo/README.md) |
| [OS filtering](os-filtering.md) | Tasks scoped to an operating system | [os-filtering](../examples/os-filtering/README.md) |
| [AI agents (MCP)](../mcp.md) | Expose tasks to agents; security model | [agent-driven](../examples/agent-driven/README.md) |

Working on a whole project? See the **[use-case walkthroughs](../use-cases/README.md)** for
Python, Node, and AI-agent setups.

Hit an error? See **[Troubleshooting](../troubleshooting.md)**.

---

**Next:** [User guide](../user-guide/README.md) · [Use cases](../use-cases/README.md) · [CLI reference](../cli.md) · [Runefile language](../runefile.md)
