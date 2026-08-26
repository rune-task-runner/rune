# Using Rune with AI Agents (MCP)

> The agents capability guide. New here? Read [What is Rune?](overview.md) first, and see the
> runnable **[agent-driven example](examples/agent-driven/README.md)**.

Rune is AI-native: the same tasks you run from the CLI can be exposed to AI agents and IDEs
through the [Model Context Protocol (MCP)](https://modelcontextprotocol.io). An agent calls
your tasks as tools — it never needs to learn a bespoke interface.

## Starting the server

**Stdio (local agents / IDE integrations):**

```sh
rune mcp
```

Most local agent tooling launches the server this way and talks to it over stdin/stdout.

**Streamable HTTP (opt-in, for networked clients):**

```sh
rune serve --http --addr 127.0.0.1:8765 --token-file ./mcp-token.txt
```

- `--http` selects the Streamable HTTP transport (stdio is the default otherwise).
- `--addr` sets the bind address.
- `--token-file` supplies a bearer token required on every request.

A remote endpoint is **opt-in, intended to be localhost-bound, and token-gated** — it is
never enabled by default.

## How tasks become tools

- Every **non-private** task **available on the host OS** is exposed as an MCP tool. Add
  `[private]` to hide a task (it remains callable only as a dependency of another task).
  A task restricted to another platform (`[linux]`/`[macos]`/`[windows]`/`[unix]`) is not
  registered at all, so an agent can never see — or attempt — a platform-incompatible
  command; calling such a task by a remembered name is rejected at the protocol layer as
  an unknown tool.
- A task's **parameters** define the tool's input schema. Defaults and variadic
  parameters carry over.
- The task's **doc comment** (the comment directly above it) becomes the tool description.

```rune
# Build the project for a target.
build target="release":
    go build -tags {{target}} ./...

[private]
_internal-helper:
    @echo "not exposed to agents"
```

Here `build` is offered to the agent as a tool with a `target` argument; `_internal-helper`
is not.

## Project context for agents

Mark one task with `[context]` to hand agents the project's current state — branch,
dirty files, lint findings — *before* they choose a task:

```rune
# Gather project health for agents.
[context]
health:
    @git branch --show-current
    @git status --short
    -golangci-lint run
```

- **MCP sessions** receive the task's output as the server `instructions` in the
  initialize result, computed once at server start.
- **`(agent)` tasks** get the output prepended to their prompt, fresh on every
  invocation from the CLI (directly or as a dependency). An agent task called
  *as an MCP tool* runs without the prefix — the caller's session already
  received the context as instructions, and the omission doubles as the guard
  against agent-in-agent recursion.

The hook is best-effort and can never block agent access: it runs under a 10-second
timeout, and on failure or timeout the agent proceeds with a one-line notice instead.
Output is capped at 8 KiB and passes through the same
[secret masking](how-to/secret-masking.md) as every other agent-facing surface. A
failing health check (like `golangci-lint` finding errors) fails the task under
normal semantics — prefix the line with `-` (continue on error) to deliver the
findings as context instead. The hook itself is never exposed as a tool; it stays
runnable by name from the CLI for debugging.

## Fix suggestions on failure

Give a task a `||` [failure hook](how-to/dependencies-and-hooks.md) and a failing
tool call returns more than a raw exit code — the hook runs a diagnostic and its
output arrives in the **same tool response** as the failure, under a delimited
section:

```text
running tests
[exit 1]
[fix suggestion - from Runefile || failure hooks]
SUGGESTION: re-run go test -v ./pkg/... — 2 fixtures out of date
```

```rune
test: || diagnose
    go test ./...

diagnose:
    @echo "post-mortem for $RUNE_FAILED_TASK (exit $RUNE_FAILED_EXIT_CODE)"
    -go vet ./...
```

The agent needs no extra round trip to learn what broke. The suggestion is the
hooks' standard output only, passed through the same
[secret masking](how-to/secret-masking.md) as every other agent-facing surface and
capped at 8 KiB (a `[truncated]` marker closes an over-long suggestion). Hook
outcomes never change the reported exit code, and a hook that fails or is
OS-skipped degrades to a one-line warning in the response's stderr portion. An
`agent`-executor task cannot be a failure hook — the analyzer rejects it
(RUNE2011) so a post-mortem can never spawn an agent session.

## Security model (secure by default)

Exposing tasks to an agent grants execution capability, so Rune is conservative by default:

- **Read-only by default.** Agent access defaults to non-destructive tasks. Access to
  destructive tasks is an explicit, per-task opt-in.
- **Destructive tasks are gated.** A task marked `[confirm("…")]` is annotated with the
  MCP `DestructiveHint`, so clients can warn or require approval before invoking it.
- **Network tasks are flagged.** `[network]` sets the MCP `openWorldHint`.
- **Secrets come from the environment only.** API keys and secrets are read from the
  environment (or the agent CLI's own session) — **never** from the Runefile, and they are
  never included in any tool description, schema, or log.
- **Task output is masked.** Even if a task prints a sensitive environment variable, the
  tool result the agent receives shows `***` in place of the value — identical to what a
  terminal user sees — so credentials never enter an agent's chat history or memory. This
  is always on, with no agent-facing off switch; see
  [secret masking](how-to/secret-masking.md).
- **Vendor-neutral.** The agent/LLM layer sits behind a provider interface; no single
  vendor is hard-coded.

```rune
# Safe: read-only, exposed to agents by default.
status:
    @git status --short

# Destructive: gated with confirm → DestructiveHint for agents.
[confirm("Delete all build output?")]
clean:
    rm -rf dist
```

## Agent task bodies

A task can itself be driven by an AI agent using the `agent` executor, which runs an
installed agent CLI (e.g. `claude`, `codex`, `copilot`) behind the vendor-neutral provider
interface:

```rune
# Summarize recent changes using the configured agent provider.
summarize (agent):
    Summarize the latest git changes in three bullet points.
```

Agent tasks default to **read-only** tool access; granting them destructive capability is
an explicit opt-in.

## See also

- [agent-driven example](examples/agent-driven/README.md) — a runnable agent task + gating
- [CLI reference](cli.md) — `rune mcp` / `rune serve` flags
- [Runefile language guide](runefile.md) — `[private]`, `[confirm]`, `[network]`, executors
- [Guides](how-to/README.md) — the rest of the capability deep dives
