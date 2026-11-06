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

**Letting agents run destructive tasks:**

```sh
rune mcp --allow-destructive clean
```

This lets agents run the `[confirm]` task `clean`. Name each task you allow, separated by
commas (`--allow-destructive clean,reset`) or with the flag repeated. Every other
`[confirm]` task stays refused; see [Security model](#security-model-secure-by-default).

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
  parameters carry over, and the schema allows no other names
  (`additionalProperties: false`).
- The task's **doc comment** (the comment directly above it) becomes the tool description.

```rune
# Build the project for a target.
build target="release":
    go build -tags {{quote(target)}} ./...

[private]
_internal-helper:
    @echo "not exposed to agents"
```

Here `build` is offered to the agent as a tool with a `target` argument; `_internal-helper`
is not.

A tool call returns the task's output followed by an `[exit N]` line, and the result is
marked as an error whenever *N* is not `0`. *N* is the code the CLI exits with for the same
call: a failed body's own exit code, so an agent can tell a deliberate refusal (say, exit `2`)
from a failure; `1` for a failure without a body exit code; and Rune's own `2` (usage), `3`
(validation), or `130` (interrupted). A body can exit with one of those numbers too, so the
marker alone does not tell a body's exit `2` from a usage error. See
[Exit codes](cli.md#exit-codes). A call with invalid arguments is rejected before the task
runs and returns an error without the marker. A call that names an argument the task does
not declare is refused the same way:
`unknown argument "targte" for task "build" (did you mean "target"?)`. A `null` or object
value for a parameter is rejected too, since a task has no way to receive one.

## Typed tool schemas

Inline [parameter type annotations](runefile.md#typed-parameters) become a
**strictly typed** input schema, so an agent knows exactly what inputs are valid
before its first call — no malformed-argument round trips:

```rune
# Deploy the service.
[returns("URL of the deployed environment on stdout")]
[param-doc("env", "Target environment")]
deploy env:enum("staging","prod") replicas:number="2":
    ./deploy.sh {{env}} {{replicas}}
```

The `deploy` tool advertises `env` as `{"type":"string","enum":["staging","prod"],
"description":"Target environment"}` and `replicas` as `{"type":"number"}`;
`path`-typed parameters are format-tagged strings, and a typed variadic becomes
an array of the mapped type. Constraints are **enforced**, not just advertised:
a violating call returns a tool error naming the parameter, the rejected value,
and the accepted set — before anything executes — and array (variadic) values
are validated per element. The same rules bind CLI and dependency invocations,
so a constraint is a property of the task, not of the transport.

A `raw` parameter is a plain string whose description ends with "Split into
shell words without quoting.", after any `[param-doc]` text, so the agent knows
the value becomes several shell words.

`[returns("…")]` gives the agent a success criterion: it is appended to the tool
description as a `Returns:` trailer (and shown in `--list` and the JSON dump),
so the agent can verify a task's output against the expected shape. Both
annotations are static Runefile string literals — no environment value can ever
reach a schema.

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

- **Destructive tasks are refused by default.** A task marked `[confirm("…")]` is still
  listed, annotated with the MCP `DestructiveHint`, but a call to it is refused and nothing
  runs. The refusal names the flag that permits it.
- **The operator allows destructive tasks one by one.** Start the server with
  `--allow-destructive clean,reset` (or the flag repeated) to let agents run those tasks.
  A granted task also runs when another task reaches it as a dependency or hook. A grant
  never covers any other task, including a destructive dependency of the granted task.
- **There is no blanket switch.** No option allows all destructive tasks, and
  `rune serve` and `rune mcp` do not accept `--yes`.
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

# Destructive: refused over MCP unless allowed with --allow-destructive clean.
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

The endpoint an `agent` task opens for its own agent never lets that agent run a
destructive task. Neither `--allow-destructive` nor `--yes` changes that.

## See also

- [agent-driven example](examples/agent-driven/README.md) — a runnable agent task + gating
- [CLI reference](cli.md) — `rune mcp` / `rune serve` flags
- [Runefile language guide](runefile.md) — `[private]`, `[confirm]`, `[network]`, executors
- [Guides](how-to/README.md) — the rest of the capability deep dives
