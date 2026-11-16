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
[Client compatibility](#client-compatibility) has the configuration file for each client.

**Letting agents run destructive tasks:**

```sh
rune mcp --allow-destructive clean
```

This lets agents run the `[confirm]` task `clean`. Name each task you allow, separated by
commas (`--allow-destructive clean,reset`) or with the flag repeated. Every other
`[confirm]` task stays refused; see [Security model](#security-model-secure-by-default).

**Choosing which tasks agents see:**

```sh
rune mcp --allow build,test
rune mcp --deny deploy
```

`--allow` exposes only the tasks you name. `--deny` hides the tasks you name and keeps the
rest, including tasks added to the Runefile later. Both take names separated by commas
(not spaces) or the flag repeated, and a task named in both is hidden. A hidden task is not in the tool
list, and a call to it fails the same way as a call to a tool that does not exist. A name
that is not a task in the Runefile stops the server with exit code 2 and suggests the
closest task. Imported tasks use the Runefile form, `tools::lint`.

**Setting how much output a result carries:**

```sh
rune mcp --output-limit 256KiB
```

A tool result carries at most 64 KiB of stdout and 64 KiB of stderr; see
[Output limit](#output-limit).

**Keeping the tasks read at start:**

```sh
rune mcp --no-reload
```

A running server follows the Runefile; see [Reloading](#reloading).

**Streamable HTTP (opt-in, for networked clients):**

```sh
rune serve --http --addr 127.0.0.1:8765 --token-file ./mcp-token.txt
```

- `--http` selects the Streamable HTTP transport (stdio is the default otherwise).
- `--addr` sets the bind address. The default is `127.0.0.1:7777`.
- `--token-file` supplies a bearer token required on every request.

A remote endpoint is **opt-in, bound to loopback unless you name another address, and
token-gated**. It is never enabled by default.

The HTTP transport has no TLS. On a loopback address the token never leaves the machine. On
any other address it crosses the network in cleartext, and anyone who can read the traffic can
use it to run your tasks. The server still starts, and says so once on stderr:

```text
warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext
```

What decides is the address the server bound, not how `--addr` is written:

- An address in `127.0.0.0/8`, or `::1`, is loopback. No warning.
- `--addr :7777`, `--addr 0.0.0.0:7777` and `--addr [::]:7777` bind every interface. Warning.
- The address of a network interface, such as `192.168.1.5:7777`. Warning.
- A host name counts as the address it resolves to. `localhost` is normally loopback.

For a client on another machine, keep the server on loopback and reach it through something
that encrypts, such as an SSH tunnel or a reverse proxy that terminates TLS.

## How tasks become tools

- Every **non-private** task **available on the host OS** is exposed as an MCP tool. Add
  `[private]` to hide a task (it remains callable only as a dependency of another task).
  The operator can narrow this further for one server with `--allow` and `--deny`; see
  [Starting the server](#starting-the-server).
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

### Structured results

A client does not have to parse the text. Every tool declares an `outputSchema`, and the
result of every task run carries the same information as `structuredContent`:

```json
{
  "exitCode": 1,
  "stdout": "running tests\n",
  "stderr": "FAIL: fixtures out of date\ndiagnosing: hint\n",
  "fixSuggestion": "re-run with -update"
}
```

| Field | Type | Meaning |
|---|---|---|
| `exitCode` | integer | The *N* of the `[exit N]` line. |
| `stdout` | string | What the run wrote to standard output. Always present; empty when it wrote nothing. |
| `stderr` | string | What the run wrote to standard error, including Rune's own lines about the run. Always present. |
| `fixSuggestion` | string | Output of the task's `||` [failure hooks](#fix-suggestions-on-failure). Present only when a failed run produced one. |

- The text is unchanged, so a client that ignores structured content sees what it always
  saw. The fields are the strings the text is made of: `stdout`, then `stderr`, then the
  `[exit N]` line, then the fix-suggestion header and `fixSuggestion`. The text adds a line
  break after a stream that does not end with one; the fields do not.
- The fields are masked and limited exactly as the text is. Each of `stdout` and `stderr`
  obeys the [output limit](#output-limit) and holds the same `[truncated N bytes]` line.
- The exit code comes from the run, never from the output: a task that prints `[exit 0]`
  and fails has `exitCode` set to its real status.
- A call that runs no task has no structured content. That is a refused `[confirm]` task,
  an undeclared argument, a missing required argument, an invalid value, or any other call
  rejected before the task starts. Such a result is always marked as an error and carries
  its explanation as text.
- The schema is the same for every tool and allows no other field.

### Tool hints

Each tool carries three MCP hints that tell a client what its task claims:

| Hint | Value | True when |
|---|---|---|
| `readOnlyHint` | `true`, or left out | The task has `[readonly]` |
| `destructiveHint` | `true` or `false`, always present | The task, or anything it can run, has `[confirm]` |
| `openWorldHint` | `true` or `false`, always present | The task, or anything it can run, has `[network]` |

What a task can run is its dependencies, its `&&` and `||` hooks, and everything those run in
turn, including private tasks and tasks in imported modules. A failure hook's own `||` hooks
do not count, because failure hooks do not chain. A task restricted to another OS does not
count on this host, because a run skips it.

- Hints are advisory. They never change what Rune runs or refuses.
- `[readonly]` is the author's claim. Rune does not read the body, so a `[readonly]` task
  whose body writes still claims to be read-only.
- A task without `[readonly]` makes no read-only claim, so a client may ask before running it.
- A `false` hint means only that nothing was declared.
- `RUNE2018` rejects a false `[readonly]` claim: a `[readonly]` task cannot use `[confirm]`,
  and every task it runs must be `[readonly]` too.
- A tool whose `destructiveHint` comes from a task it can run is refused unless every
  `[confirm]` task it can run is granted. The refusal names those tasks, and they are the
  names `--allow-destructive` takes, not the task that reaches them.
- `[readonly]` on an `agent` task covers what the task runs, not the tools its agent picks at
  run time.

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
capped at 8 KiB (a `[truncated]` marker closes an over-long suggestion). The same
text is the `fixSuggestion` field of the [structured result](#structured-results). Hook
outcomes never change the reported exit code, and a hook that fails or is
OS-skipped degrades to a one-line warning in the response's stderr portion. An
`agent`-executor task cannot be a failure hook — the analyzer rejects it
(RUNE2011) so a post-mortem can never spawn an agent session.

## Output limit

A tool result carries the task's stdout, its stderr, and an `[exit N]` line. Each of
stdout and stderr is limited to 64 KiB (65,536 bytes) per call. Output within the limit is
returned unchanged. Longer output keeps its beginning and its end, with one line between
them that says how many bytes were left out:

```text
BEGIN build
compiling pkg/a
[truncated 2934504 bytes]
linking
build finished
[exit 0]
```

- The limit counts the marker line too, so a stream never exceeds it. The beginning and the
  end each get about half of the rest.
- The `[exit N]` line is always present and complete. The text of a result is at most twice
  the limit plus 32 bytes, before any fix suggestion.
- A result carries the output twice: as text and as
  [structured fields](#structured-results). The two stream fields together are at most twice
  the limit, so the output in a whole result is at most four times the limit plus 32 bytes,
  plus the fix suggestion (at most 8 KiB) in each form. The field names, the fix-suggestion
  header and the protocol's JSON encoding come on top of that. A client gives a model one of
  the two forms.
- The server keeps only what it will return while the task runs, so a task that prints
  gigabytes does not grow the server. The task itself is never stopped or slowed for
  printing.
- Secret masking happens before truncation, so a cut never exposes part of a secret.
- The [fix suggestion](#fix-suggestions-on-failure) and the
  [project context](#project-context-for-agents) keep their own 8 KiB limit.

Change the limit for one server with `--output-limit`, or for every server of a Runefile
with a setting. The flag wins when both are given:

```sh
rune mcp --output-limit 256KiB
```

```rune
set mcp_output_limit := "256KiB"
```

A size is a whole number of bytes, or a count with a `KiB` or `MiB` unit written right
after it: `65536`, `256KiB`, `1MiB`. It must be from `1KiB` to `1024MiB`. Any other value
stops the server with exit code 2 (the flag) or a diagnostic at the setting, before any
task runs. The limit cannot be turned off.

The setting also applies to the endpoint an [agent task](#agent-task-bodies) opens for its
own agent, which no `rune mcp` flag reaches. It has no effect on tasks run from the command
line: their output goes to the terminal whole.

## Reloading

A running server follows its Runefile. It watches the Runefile and every file it imports or
loads as a module. When one of them is saved, the server loads the Runefile again, replaces
its tools, and sends connected clients a `notifications/tools/list_changed` message, within
about two seconds. No restart is needed. The server reports each reload on stderr:

```text
reloaded Runefile: 1 tool added, 0 removed, 0 changed
```

- **A broken save keeps the last valid tools.** If the Runefile has a static error, the
  server prints the same diagnostics as `rune` does, then
  `reload failed: 1 static error(s); keeping the previous tools`. Nothing is replaced and no
  task runs. The next valid save is loaded.
- **A call uses one version of the Runefile.** A call that is running during a reload
  finishes with the tasks it started with. Calls that start afterwards use the new ones.
- **Clients are notified only when the tool list differs.** Editing only a task's body
  changes what the next call runs, and sends no notification.
- **Flags keep their values.** `--allow`, `--deny`, `--allow-destructive` and
  `--output-limit` apply to the reloaded tasks as they did at start. If a reload removes a
  task that a flag names, the server keeps running and prints a warning; the name applies
  again if the task comes back. Settings such as `mcp_output_limit` are part of the Runefile
  and reload with it.
- **Project context.** On the HTTP transport, the `[context]` task runs again after each
  successful reload, and sessions that start later receive its new output. A session that is
  already open keeps the instructions it was given, so on stdio the task is not run again.
- **What is not watched.** `.env` files and the scripts your tasks call. A reload reads
  `.env` files again, but a change to one does not start a reload.

With reloading, whoever can write the Runefile can change what a tool runs while the server
is up. `[confirm]` tasks still need `--allow-destructive` by name, and `--allow` still
limits which names are tools. To keep exactly the tasks read at start, run
`rune mcp --no-reload`: the server then watches no file.

## Security model (secure by default)

Exposing tasks to an agent grants execution capability, so Rune is conservative by default:

- **Destructive tasks are refused by default.** A task marked `[confirm("…")]` is still
  listed, annotated with the MCP `DestructiveHint`, but a call to it is refused and nothing
  runs. A call that can run an ungranted `[confirm]` task through a dependency or hook is
  refused too, before anything runs. The refusal names every task to add and the flag that
  permits them, for example:
  `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`.
- **The operator allows destructive tasks one by one.** Start the server with
  `--allow-destructive clean,reset` (or the flag repeated) to let agents run those tasks.
  A granted task also runs when another task reaches it as a dependency or hook. A grant
  never covers any other task, including a destructive dependency of the granted task.
- **There is no blanket switch.** No option allows all destructive tasks, and
  `rune serve` and `rune mcp` do not accept `--yes`.
- **The operator chooses which tasks are tools.** `--allow` and `--deny` narrow the tool
  list. They never add to it: a private task, the `[context]` hook, and a task restricted
  to another OS stay unexposed even when `--allow` names them.
- **`--allow` and `--deny` cover direct calls only.** The dependencies and hooks of an
  exposed task still run, whether or not they are exposed. With `release: deploy`,
  `--deny deploy` stops an agent calling `deploy` but not `release`. To stop a task running
  at all, hide the tasks that depend on it too, or mark it `[confirm]`: a `[confirm]` task
  the operator has not named in `--allow-destructive` is refused wherever a run reaches it.
- **`--allow` grants nothing destructive.** A `[confirm]` task needs `--allow-destructive`
  whichever of the two filters is set, and a grant does not make a hidden task a tool.
- **Network tasks are flagged.** `[network]` sets the MCP `openWorldHint` on the task and on
  every task that can reach it.
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
# Claims to be read-only, so a client may run it without asking.
[readonly]
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
destructive task. Neither `--allow-destructive` nor `--yes` changes that. `--allow` and
`--deny` do not reach that endpoint either: they apply only to `rune serve` and `rune mcp`.

## Client compatibility

How five agent clients handle what Rune sends them, as of 2026-11-15. Clients change
often, so check the version before you rely on a row.

Every cell names its evidence:

- **recorded**: seen in a session recorded against `rune mcp`: in the messages the client
  and Rune sent, or, for what the model was given, in the client's reply to that session.
  The recordings, the replies, and the commands and prompts that made them are in
  [test/integration/testdata/clients](../test/integration/testdata/clients/README.md).
- **source**: read in the client's source code, not run.
- **docs**: stated in the vendor's documentation.
- **staff**: stated by the vendor's staff on the vendor's forum.
- **unknown**: no evidence found.

Only Claude Code and Codex CLI were run. Cursor, VS Code and Zed were not tested: every
cell in their columns comes from reading, and none of them has a recording.

| | Claude Code | Codex CLI | Cursor | VS Code, Copilot agent mode | Zed |
|---|---|---|---|---|---|
| Version | 2.1.325 | 0.167.0 | several, named in [Cursor](#cursor) | 1.146 | 1.28.1 |
| Project configuration | `.mcp.json` (recorded) | `.codex/config.toml`, in a trusted project (recorded) | `.cursor/mcp.json` (docs) | `.vscode/mcp.json` (docs) | `.zed/settings.json` (source) |
| User configuration | `~/.claude.json` (docs) | `~/.codex/config.toml` (docs) | `~/.cursor/mcp.json` (docs) | `mcp.json` in the user profile (docs) | `~/.config/zed/settings.json` (docs) |
| [Project context](#project-context-for-agents) reaches the model | Yes (recorded) | Yes (recorded) | Unknown | Yes (source) | No (source) |
| [Tool hints](#tool-hints) change what the client asks approval for | No (recorded) | Yes (recorded) | Unknown | Yes (source) | No (source) |
| Name the model sees for `tools::lint` | `mcp__rune__tools__lint` (recorded) | `mcp__rune__tools__lint` (recorded) | Unknown | `mcp_rune_tools__lint` (source) | `tools__lint` (source) |
| Name the model sees for `docs-check` | `mcp__rune__docs-check` (recorded) | `mcp__rune__docs_check` (recorded) | Unknown. Hyphens are accepted (staff) | `mcp_rune_docs-check` (source) | `docs-check` (source) |
| Lists the tools again after [`list_changed`](#reloading) | Yes (recorded) | No, unless a feature under development is enabled (recorded) | Not in 3.12.17, and no fix is announced (staff) | Yes (source) | Yes (docs, source) |
| Default limit on one call | About 28 hours, or 30 minutes without a reply (docs) | 300 seconds (recorded) | 60 seconds in the CLI, longer in the editor (staff) | None found (source) | 60 seconds (source) |
| Sends `notifications/cancelled` at the limit | Yes (recorded) | Yes (recorded) | Unknown | Has no limit | No (source) |
| Sends `notifications/cancelled` when the user stops a call | Not tested | Not tested | Missing in 1.7.54 (staff). A user reported it fixed in 2.x | Yes (source) | No (source) |

### What this means for a Runefile

- **Do not rely on `[context]` alone.** Zed does not read the instructions, and Cursor is
  unknown. Put what an agent must know in the doc comments of your tasks too. Claude Code
  cuts instructions at 4,096 characters (docs), half of Rune's own 8 KiB limit.
- **Codex asks before it calls a `[confirm]` or `[network]` task.** It asks for a tool
  whose `destructiveHint` is true, and for a tool whose `openWorldHint` is true unless it
  also has `readOnlyHint` (source). `codex exec`, which cannot ask, refuses the call.
  `--allow-destructive` does not change that: it is Rune's permission, not the client's.
  To let Codex call every tool of the server, set
  `default_tools_approval_mode = "approve"` in its `[mcp_servers.rune]` table (recorded).
- **`[readonly]` saves a confirmation in VS Code, and in Codex on a `[network]` task.** VS
  Code runs a tool with `readOnlyHint` without asking (source). Codex does not ask for a
  `[network]` tool that has `readOnlyHint` (source). Claude Code and Zed decide the same
  way for every tool, whatever its hints.
- **Module tasks work.** Claude Code and Codex both called `tools__lint` (recorded). No
  client was found that splits a tool name at its double underscore.
- **A hyphen is an underscore in Codex.** It shows `docs-check` as `docs_check` and calls
  Rune with the real name (recorded). Use the name the client shows in that client's
  permission rules.
- **Keep task names short.** With the module prefix, stay under about 50 characters. VS
  Code and Zed cut the name the model sees at 64 characters (source), and Cursor reports a
  server name and tool name of more than 60 characters together as a naming issue (staff).
- **Adding or removing a task needs a new session in Codex and a restart of Cursor.**
  Neither lists the tools again. The same goes for a change to a task's parameters, doc
  comment or attributes: until then the model sees the old tool. Editing only a task's
  body needs nothing in any client: the next call runs the new body.
- **A long task outlives some clients.** Zed stops waiting after 60 seconds, Cursor's CLI
  after 60 seconds and Codex after 300 seconds. Claude Code drops a call after 30 minutes
  without a reply or a progress notification (docs), and Rune sends none while a task
  runs. Each section below names the setting that raises the limit, where there is one.
  When Claude Code or Codex reached its call limit, it told Rune, and Rune stopped the
  task and answered with `[exit 130]` (recorded). What Claude Code sends at the 30-minute
  cut was not tested. Zed does not tell Rune (source), so the task runs to its end with
  nobody waiting for the result.

### Claude Code

Tested: version 2.1.325 on Linux, as `claude -p`.

```json
{
  "mcpServers": {
    "rune": { "type": "stdio", "command": "rune", "args": ["mcp"] }
  }
}
```

- **Configuration.** The block above is `.mcp.json` at the project root. `claude -p` loads
  it without asking (recorded). An interactive session asks you to approve the server
  first (docs). `claude mcp add --scope user rune -- rune mcp` adds the server for every
  project, in `~/.claude.json` (docs).
- **Context.** The model named the `[context]` output as the server's instructions before
  it called a tool (recorded).
- **Approval.** With no permission rule, `status`, `build` and `clean` were all refused
  the same way, although `status` has `readOnlyHint` (recorded). The documented use of
  `readOnlyHint` is running read-only calls in parallel. Allow tools by name:
  `mcp__rune` for every task, or `mcp__rune__build` for one (docs).
- **Reload.** It listed the tools again 7 ms after the notification, and the model called
  the new tool in the same session (recorded).
- **Limits.** `MCP_TOOL_TIMEOUT` is the limit on one call in milliseconds, 100,000,000 by
  default. A call to a stdio server is also dropped after 30 minutes without a reply or a
  progress notification, and Rune sends no progress notifications;
  `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT` changes that (docs). With `MCP_TOOL_TIMEOUT=5000`,
  it sent `notifications/cancelled` 5 seconds after the call (recorded).
- **Not tested.** Stopping a call with Esc, and an interactive session.

Sources: [MCP](https://code.claude.com/docs/en/mcp),
[environment variables](https://code.claude.com/docs/en/env-vars),
[permissions](https://code.claude.com/docs/en/permissions).

### Codex CLI

Tested: version 0.167.0 on Linux, as `codex exec`.

```toml
[mcp_servers.rune]
command = "rune"
args = ["mcp"]
```

- **Configuration.** The block above goes in `~/.codex/config.toml` (docs), or in
  `.codex/config.toml` in a project. Codex loaded the project file in a project the user
  configuration trusts (recorded). In a directory it does not trust, it ignored the file
  in three runs and never started Rune, so those runs have no recording.
- **Context.** The model named the `[context]` output as the description of the
  `mcp__rune` tool namespace (recorded).
- **Approval.** `status` (`readOnlyHint`) and `build` (`destructiveHint` and
  `openWorldHint` both `false`) ran. `clean` (`destructiveHint`) and `fetch`
  (`openWorldHint`) were refused with "MCP tool call requires approval, but approval
  policy is never", and neither call reached Rune (recorded). The rule in 0.167.0: a tool needs approval when `destructiveHint` is true,
  or when `openWorldHint` is true and it has no `readOnlyHint` (source).
- **Names.** Every character of a tool name that is not a letter, a digit or `_` becomes
  `_`. A name of more than 128 bytes with its prefix is cut and given a hash (source).
- **Reload.** Codex ignored the notification, and the new tool never appeared. Started
  with `--enable mcp_tool_list_changed`, it listed the tools again before the model's next
  request and called the new tool (recorded). That feature is marked as under development
  and is off by default (source).
- **Limits.** It sent `notifications/cancelled` 300 seconds after the call (recorded). The
  documentation gives the default as 60 seconds; 0.167.0 uses 300 (source). Set
  `tool_timeout_sec` in the server's table to choose (docs).
- **Not tested.** An interactive session, where Codex asks for approval and does not
  refuse.

Sources: [MCP](https://developers.openai.com/codex/mcp), and
[`openai/codex`](https://github.com/openai/codex/tree/rust-v0.167.0/codex-rs) at tag
`rust-v0.167.0`: `core/src/mcp_tool_call.rs`, `codex-mcp/src/tools.rs`,
`codex-mcp/src/rmcp_client.rs`, `features/src/lib.rs`,
`rmcp-client/src/logging_client_handler.rs`.

### Cursor

Not tested. Cursor is closed source, so every statement is from its documentation or from
its staff on the Cursor forum, and each forum statement is about the version named.

```json
{
  "mcpServers": {
    "rune": { "type": "stdio", "command": "rune", "args": ["mcp"] }
  }
}
```

- **Configuration.** The block above is `.cursor/mcp.json` in a project, or
  `~/.cursor/mcp.json` for every project (docs).
- **Context.** Unknown. Nothing found says whether the server instructions reach the model.
- **Approval.** Cursor asks before it uses an MCP tool, unless the run mode or an
  allowlist lets the tool run (docs). Nothing found says that hints change this.
- **Names.** Hyphens are accepted, 3.0.16
  ([staff](https://forum.cursor.com/t/cursor-incorrectly-filters-out-mcp-tool-names-containing-dots-and-hyphens-despite-being-valid-per-mcp-spec/157635)).
  A server name and tool name of more than 60 characters together are reported as a naming
  issue, but the tool still works, 2.6.19
  ([staff](https://forum.cursor.com/t/posthog-mcp-tool-names-too-long/155408)). The name
  the model sees is unknown.
- **Reload.** `notifications/tools/list_changed` stopped causing a new tool list in
  3.12.17, and only quitting Cursor loads one. No fix was announced by 2026-08-25
  ([staff](https://forum.cursor.com/t/mcp-server-regression-does-not-reload-tools-disconnect-does-nothing-ignores-mcp-json-changes-etc/166216)).
- **Limits.** The CLI gives up on a call after 60 seconds, with no setting to change it,
  and the editor waits longer, CLI 2026.06.19
  ([staff](https://forum.cursor.com/t/agent-acp-mcp-tools-call-times-out-at-60s-with-no-way-to-configure-it/163925)).
  Stopping a call did not send `notifications/cancelled` in 1.7.54
  ([staff](https://forum.cursor.com/t/cursor-doesnt-send-mcp-cancellation-notifications-when-user-clicks-cancel-button/138669)).
  The reporter wrote a week later that 2.x seemed to fix it.

Source: [MCP](https://cursor.com/docs/context/mcp).

### VS Code, Copilot agent mode

Not tested. The statements are from the documentation as of VS Code 1.146 and from the
source of [`microsoft/vscode`](https://github.com/microsoft/vscode) at commit `9893f28`.

```json
{
  "servers": {
    "rune": { "type": "stdio", "command": "rune", "args": ["mcp"] }
  }
}
```

- **Configuration.** The block above is `.vscode/mcp.json` in a workspace. The command
  **MCP: Open User Configuration** opens the `mcp.json` of your user profile, which takes
  the same content (docs).
- **Two ways to run a server.** Everything below is about servers that VS Code itself
  starts from those two files. The documentation calls this the Local harness, says it is
  deprecated, and plans its removal for February 2027. Its replacement, the Copilot
  harness, reads `~/.copilot/mcp-config.json` and a `.mcp.json` at the workspace root,
  both with the `mcpServers` key. How that harness handles instructions, hints,
  notifications and limits is unknown.
- **Context.** The server instructions are put in the agent's prompt, marked as applying
  to the tools whose names start with `mcp_rune` (source).
- **Approval.** A tool with `readOnlyHint` gets no confirmation. Every other tool gets a
  "Run" confirmation, which the user can choose to skip from then on. `openWorldHint` adds
  a confirmation of the tool's result. `destructiveHint` is not read (source). The
  documentation warns that `readOnlyHint` does not replace an approval policy, so a policy
  can still ask.
- **Names.** The model sees `mcp_`, the server name, `_` and the tool name. A character
  outside letters, digits, `_` and `-` becomes `_`, and the whole name is cut at 64
  characters (source).
- **Reload.** It lists the tools again on the notification (source).
- **Limits.** No limit on a call was found. Cancelling a call sends
  `notifications/cancelled` (source).

Sources: [MCP configuration reference](https://code.visualstudio.com/docs/agents/reference/mcp-configuration),
and in `src/vs/workbench/contrib/mcp/common/`: `mcpLanguageModelToolContribution.ts`,
`mcpServer.ts`, `mcpServerRequestHandler.ts`, `mcpTypes.ts`. The prompt is built in
`extensions/copilot/src/extension/prompts/node/agent/defaultAgentInstructions.tsx`.

### Zed

Not tested. The statements are from the documentation as of Zed 1.28.1 and from the
source of [`zed-industries/zed`](https://github.com/zed-industries/zed) at commit `24d3772`, and
they are about Zed's own agent, not an external agent that Zed runs.

```json
{
  "context_servers": {
    "rune": { "command": "rune", "args": ["mcp"] }
  }
}
```

- **Configuration.** The block above goes in `~/.config/zed/settings.json` (docs). The
  code that starts servers also reads the settings of each open project, which are in
  `.zed/settings.json` (source). The documentation does not mention the project file.
- **Context.** Not delivered. Zed's type for the `initialize` result has no field for
  the instructions, so they are dropped when the reply is read (source).
- **Approval.** Zed reads the hints and uses none of them. Whether it asks is set by
  `agent.tool_permissions`, where a Rune tool has the key `mcp:rune:<tool>`, for example
  `mcp:rune:build` (docs, source).
- **Names.** The model sees the tool name alone. Zed adds the server name only when two
  tools have the same name. A character outside letters, digits, `_` and `-` becomes
  `_`, and the name is cut at 64 characters (source).
- **Reload.** It lists the tools again on the notification (docs, source).
- **Limits.** 60 seconds for one call. `context_server_timeout`, or `timeout` in the
  server's entry, sets another number of seconds, up to 600. At the limit, and when the
  user stops a call, Zed stops waiting and sends Rune nothing (source).

Sources: [MCP](https://zed.dev/docs/ai/mcp), and in `crates/`:
`context_server/src/types.rs`, `context_server/src/client.rs`,
`agent/src/tools/context_server_registry.rs`, `agent/src/thread.rs`,
`project/src/context_server_store.rs`, `settings_content/src/project.rs`.

## See also

- [agent-driven example](examples/agent-driven/README.md) — a runnable agent task + gating
- [CLI reference](cli.md) — `rune mcp` / `rune serve` flags
- [Runefile language guide](runefile.md) — `[private]`, `[confirm]`, `[network]`, executors
- [Guides](how-to/README.md) — the rest of the capability deep dives
