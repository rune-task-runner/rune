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

**Serving a Runefile from another directory:**

```sh
rune mcp -f /path/to/project/Runefile
```

`rune mcp` serves the Runefile of the directory it is started in, or of the nearest parent
that has one. A client that starts the server somewhere else gets
`no Runefile found in this directory or any parent`. `-f`, or `--file`, names the Runefile.
Tasks then run in the Runefile's directory, and its imports, modules and `set dotenv` file
are found from there, whatever directory the server was started in.
`rune -f /path/to/project/Runefile mcp` is the same command.

- **Give an absolute path.** A relative one is read from the directory the server was
  started in, which is the directory you could not rely on.
- **A wrong path stops the server.** A path that names no file, a directory, or an empty
  value is a usage error with exit code 2. The server never falls back to another
  Runefile.
- **`--yes` is not a server flag**, before or after `-f`; see
  [Security model](#security-model-secure-by-default).

This is what a server registered for the user, and not for one project, needs. Such a
server comes in two kinds:

- **It follows the project you are in.** Registered as plain `rune mcp`, it works where
  the client starts its servers inside the project you opened, and serves whichever
  project that is. Where the client starts it elsewhere, it stops with `no Runefile found`.
- **It is pinned to one project.** Registered with `-f`, it serves that project wherever
  the client starts it. Register each project as its own server, under its own name. The
  tools carry that name: `mcp__myproject__build` in Claude Code, for example.

A pinned server in Claude Code:

```sh
claude mcp add --scope user myproject -- rune mcp -f /path/to/myproject/Runefile
```

And in Codex CLI, in `~/.codex/config.toml`:

```toml
[mcp_servers.myproject]
command = "rune"
args = ["mcp", "-f", "/path/to/myproject/Runefile"]
```

**Letting agents run destructive tasks:**

```sh
rune mcp --allow-destructive clean
```

This lets agents run the `[confirm]` task `clean`. Name each task you allow, separated by
commas (`--allow-destructive clean,reset`) or with the flag repeated. A call that can run
any other `[confirm]` task is refused, or, with a client that can ask, put to you for
approval one call at a time; see
[Approving a destructive call in the client](#approving-a-destructive-call-in-the-client)
and [Security model](#security-model-secure-by-default).

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
  a `[confirm]` call that was not approved, an undeclared argument, a missing required
  argument, an invalid value, or any other call rejected before the task starts. Such a result is always marked as an error and carries
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
- A tool whose `destructiveHint` comes from a task it can run is refused, or put to the
  person for approval, unless every `[confirm]` task it can run is granted. The refusal
  and the approval request name those tasks, and they are the names `--allow-destructive`
  takes, not the task that reaches them.
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

A parameter that `[agent-allow]` names is advertised with its one allowed value in
place of the full list; see
[Restricting the values an agent may pass](#restricting-the-values-an-agent-may-pass).

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
is up. `[confirm]` tasks still need `--allow-destructive` by name or your approval of the
call, an approval given before a reload is not used after it, and `--allow` still
limits which names are tools. To keep exactly the tasks read at start, run
`rune mcp --no-reload`: the server then watches no file.

## Security model (secure by default)

Exposing tasks to an agent grants execution capability, so Rune is conservative by default:

- **Rune decides what Rune runs, and nothing else.** Registering `rune mcp` adds tools to a
  client. It does not remove the client's own shell, and `--allow`, `--deny` and
  `--allow-destructive` do not restrict it: an agent that is refused a task can still type
  the command into the client's shell. A session is limited to the Runefile only when the
  client is also told to give the agent no shell.
  [Rune-only agent mode](design/rune-only-agent-mode.md) says which clients can do that
  and how, and where the limit ends.
- **Destructive tasks are refused by default.** A task marked `[confirm("…")]` is still
  listed, annotated with the MCP `DestructiveHint`, but a call to it runs only if the
  operator granted the task or the person at the client approves that call. With neither,
  it is refused and nothing runs. The same holds for a call that can run an ungranted
  `[confirm]` task through a dependency or hook: nothing runs before the grant or the
  approval. The refusal names every task to add and the flag that permits them, for
  example, from a client that cannot be asked:
  `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune release" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.`
- **The person can approve one call.** A client that can show a form request is asked,
  and the call runs once on an explicit yes; see
  [Approving a destructive call in the client](#approving-a-destructive-call-in-the-client).
  Rune trusts the client to show the request and to report your answer. A client set up to
  answer by itself approves for you, so connect only clients whose answer you trust.
- **The operator allows destructive tasks one by one.** Start the server with
  `--allow-destructive clean,reset` (or the flag repeated) to let agents run those tasks.
  A granted task also runs when another task reaches it as a dependency or hook, and is
  never asked about. A grant never covers any other task, including a destructive
  dependency of the granted task.
- **There is no blanket switch.** No option allows all destructive tasks, and
  `rune serve` and `rune mcp` do not accept `--yes`.
- **A task can limit the values an agent may run it with.** `[agent-allow(env="staging")]`
  refuses any other value before anything runs, for the called task and for every task the
  call can reach. Neither a grant nor an approval lifts it; see
  [Restricting the values an agent may pass](#restricting-the-values-an-agent-may-pass).
- **The operator chooses which tasks are tools.** `--allow` and `--deny` narrow the tool
  list. They never add to it: a private task, the `[context]` hook, and a task restricted
  to another OS stay unexposed even when `--allow` names them.
- **`--allow` and `--deny` cover direct calls only.** The dependencies and hooks of an
  exposed task still run, whether or not they are exposed. With `release: deploy`,
  `--deny deploy` stops an agent calling `deploy` but not `release`. To stop a task running
  at all, hide the tasks that depend on it too. Marking it `[confirm]` is not enough by
  itself: a `[confirm]` task the operator has not named in `--allow-destructive` runs when
  the person approves a call that reaches it, and the approval request names it. A later
  edit to the Runefile can add a new path to a task, so check `--deny` when dependencies
  change.
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

# Destructive: over MCP it runs only when allowed with --allow-destructive clean,
# or when the person approves that call in the client.
[confirm("Delete all build output?")]
clean:
    rm -rf dist
```

## Approving a destructive call in the client

When a call can run a `[confirm]` task you did not name in `--allow-destructive`, `rune mcp`
and `rune serve` ask you, in your client, to approve that one call. This uses MCP
[elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation):
Rune sends a form request while the call waits, and the client shows it.

```text
Approve this Rune task call?

Task: release
Arguments:
- version: "1.2.3"
Destructive tasks it can run:
- clean: Delete all build output?

Approving runs this call once.
```

The form has one checkbox, `approved`, which starts unticked. The client decides how the
request looks.

- **When Rune asks.** After it has checked that the tool exists and the arguments are
  valid, and before any task, dependency or hook of the call runs. A call with a
  misspelled argument gets the argument error and no request.
- **What the request shows.** The called task, each argument with the value the task will
  receive (defaults included, each element of a variadic on its own), and every `[confirm]`
  task the call can run that is not granted, with the message its author wrote. A task
  whose `[confirm]` has no message shows `Run "NAME"?`, as the command line does.
- **What counts as yes.** Accepting the form with `approved` ticked. Nothing the agent
  sends counts: an argument named `approved` is an unknown argument like any other.
- **What an approval covers.** The tasks the request named, for that call, once. The next
  call is asked about again, with the same or other arguments. Two calls at once get two
  requests. A `[confirm]` task the request did not name is still declined if a run reaches
  it. If the server [reloads](#reloading) the Runefile while you are deciding, the approval
  is not used and the agent is told to call again.
- **Granted tasks are not asked about.** `--allow-destructive clean` is your standing
  decision for `clean`, and a request never lists it.
- **Rune waits five minutes.** After that the request is withdrawn and nothing runs. A
  client with a shorter limit on a call ends it first.
- **Secrets and long values.** Values and messages are masked like task output. A line
  break or other unprintable character in a value is shown as an escape, so a value cannot
  pass for a line of the request. An argument value is never shortened, because the task
  would run with all of it. A call whose request would be over 8 KiB is refused, not asked
  about.

Anything but that yes runs nothing. The result is an error with text only:

| What happened | The result says |
|---|---|
| You declined | `task "release" was not approved: the request was declined. Nothing ran.` |
| You closed the request without choosing | `... the request was dismissed. Nothing ran.` |
| You accepted with `approved` unticked | `... approval was not given. Nothing ran.` |
| No answer in five minutes | `... no answer arrived within 5m0s. Nothing ran.` |
| The client sent something else, or an error, or went away | `... the client returned no valid answer. Nothing ran.` |
| The Runefile was reloaded meanwhile | `... the Runefile changed while the request was pending; call again. Nothing ran.` |
| The client cancelled the call | `... the call was cancelled. Nothing ran.` |

The first five end with what the operator can do, because a "no" is not always yours: a
client run with nobody at it answers by itself.

```text
task "release" was not approved: the request was declined. Nothing ran. To run it without a request, the operator can add clean to --allow-destructive and restart the server, or run "rune release" themselves.
```

The command names the task only: add the arguments the task needs. A server started with
[`-f`](#starting-the-server) names its Runefile in that command, as
`rune -f /path/to/project/Runefile release`: a bare `rune release` in another directory
would not find it.

**A client that cannot ask.** Rune asks only a client that stated, when it connected, that
it can show a form. A client that stated no elicitation, or only the kind that opens a web
page, is refused as before, and the text adds the command and the reason:

```text
task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server. Or the operator can run "rune release" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.
```

**Rune trusts the client for the answer.** It cannot see who answered. A client that
answers such requests by itself approves for you: Claude Code can be configured to do that
with an [`Elicitation` hook](https://code.claude.com/docs/en/hooks#elicitation). Nothing
limits how often an agent can make the client ask, either. To keep a task from ever running
in an agent session, see [Security model](#security-model-secure-by-default).
[Client compatibility](#client-compatibility) says what each client does with the request.

**Protocol revisions.** The request is the `elicitation/create` a server sends during a
tool call, as MCP revisions 2025-06-18 and 2025-11-25 define it. Revision 2026-07-28
carries elicitation as a repeated tool call. Rune does not negotiate that revision, and
never accepts an approval from a repeated call: a call sent again is a new call.

## Restricting the values an agent may pass

Allowing a task for agents allows every value the task accepts. `[agent-allow(...)]`
narrows that: it names parameters of the task and, for each, the one value an agent may run
the task with. The parameter's type still says what the task accepts from anyone, so a
person at a terminal is not limited.

```rune
# Deploy the service.
[agent-allow(env="staging")]
deploy env:enum("staging","prod"):
    @./scripts/deploy.sh {{quote(env)}}

# Roll out to one region.
[agent-allow(env="staging", region="eu-west-1")]
rollout env:enum("staging","prod") region="eu-west-1":
    @./scripts/rollout.sh {{quote(env)}} {{quote(region)}}

# Deploy to production by default.
[agent-allow(env="staging")]
publish env:enum("staging","prod")="prod":
    @./scripts/deploy.sh {{quote(env)}}

# Run the tests.
test:
    @./scripts/test.sh

# Preview on staging.
preview: test (deploy "staging")
    @echo previewed

# Ship to production.
ship: test (deploy "prod")
    @echo shipped
```

| Call | Result |
|---|---|
| An agent calls `deploy` with `env=staging` | Runs |
| An agent calls `deploy` with `env=prod` | Refused. Nothing runs |
| An agent calls `rollout` with `env=staging`, `region=us-east-1` | Refused: every predicate must hold |
| An agent calls `rollout` with `env=staging` and no `region` | Runs: the default is the allowed value |
| An agent calls `publish` with no `env` | Refused: the default is `prod` |
| An agent calls `preview` | Runs |
| An agent calls `ship` | Refused before `test` runs |
| A person runs `rune deploy prod` or `rune ship` | Runs, as it always did |

A refused call returns an error result with no exit code, and nothing ran:

```text
task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"
```

It shows each refused value and then the whole policy. A value is masked like task output,
kept on one line, and cut at 64 characters. When the refused run is of a task the call
reaches, the text names both: `task "ship" runs "deploy", which is not allowed for agents
with env="prod"; agent policy requires env="staging"`.

**Defaults are checked, never replaced.** The value compared is the one the task would run
with: the argument the agent sent, or the task's default when it sent none. Rune does not
run the task with the allowed value in place of a refused one. An argument name the task
does not declare is still the unknown-argument error, so a misspelled name cannot fall back
to a default.

**Everything the call can reach is checked first.** Before a call starts, Rune works out
what it would run through dependencies, `&&` hooks and `||` hooks, with the arguments the
Runefile passes along each, and checks every task that has a policy against the arguments
it would get. That includes tasks in modules, private tasks, tasks hidden with `--deny`,
and a `||` hook whether or not its task would fail. A task that is unavailable on this
host is skipped, as the run would skip it. The same check runs again where a restricted
task's body would start.

**A value Rune cannot know before the run is refused.** A dependency's arguments are
evaluated again when the run reaches it. If the argument for a restricted parameter is
built from `uuid()`, `datetime()`, `path_exists()`, `read()`, `sha256_file()`, `which()`,
`require()` or `absolute_path()`, or from a variable assigned from one of them, its value
can differ by then, so the call is refused:

```text
task "nightly" runs "deploy" with a value of env that is not known before the run; agent policy requires env="staging"
```

If an argument on the way to a restricted task cannot be evaluated before the run, for
example because it reads a file an earlier task of the same run creates, the call is
refused too, whichever parameter the argument is for. Move that read into the task's body.

**A task body that runs `rune` is covered.** Rune sets `RUNE_AGENT_RUN=1` in the
environment of every body of an agent run. A `rune` started with it checks the tasks on its
command line the same way before it runs anything, and exits with status 1 and the refusal
when a policy is broken. So `ship:` with the body `rune deploy prod` does not deploy
production for an agent. The outer task has started by then: Rune does not read bodies.

**`agent` tasks and the `[context]` hook.** The endpoint an [`agent` task](#agent-task-bodies)
opens for its own agent applies the policy to that agent's calls. The `[context]` hook
runs because an agent connected, so a restricted task it reaches is checked too.

**The policy is separate from the destructive check.** A grant with `--allow-destructive`
and an approval in the client decide whether a `[confirm]` task may run. Neither allows a
refused value, and the person is not asked about a call the policy refuses. A value the
policy allows still needs the grant or the approval. The order for one call:

1. A call that can run an ungranted `[confirm]` task, from a client that cannot be asked,
   is refused as destructive, before its arguments are read.
2. Unknown argument names, then value types, missing arguments and defaults.
3. The value policy.
4. The approval request, when the call needs one.
5. The run.

**The tool names the allowed value.** For a parameter the policy names, the tool's schema
lists the one value, `{"type":"string","enum":["staging"]}`, and the description ends with
`Agent policy: agents may run this task only with env="staging".` A parameter whose default
is a literal the policy refuses is listed as required. The check does not depend on any of
this: a client that ignores the schema and sends `prod` is refused. After a
[reload](#reloading), the tools and the checks follow the new Runefile.

**What it does not cover.** The policy governs runs that start from an agent through Rune.
An agent with its own shell can run `rune deploy prod` there, and an agent that can edit the
Runefile can remove the attribute; [Rune-only agent mode](design/rune-only-agent-mode.md)
is about closing that. A task body that clears `RUNE_AGENT_RUN`, or that deploys with
another tool, is the author's own. The first version compares one exact value per
parameter: there are no patterns and no lists, and a policy cannot name a `number`,
`boolean`, `path`, `raw` or variadic parameter. A wrong declaration is the static error
[`RUNE2019`](diagnostics.md).

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
destructive task, and never asks for approval, whatever that agent's client states.
Neither `--allow-destructive` nor `--yes` changes that. `--allow` and
`--deny` do not reach that endpoint either: they apply only to `rune serve` and `rune mcp`.

The agent a task launches keeps the tools its own configuration gives it, a shell among
them. Rune does not restrict them.
[Rune-only agent mode](design/rune-only-agent-mode.md#the-agent-executor) shows an
`agent_cmd` that does.

## Client compatibility

How five agent clients handle what Rune sends them, as of 2026-11-17. Clients change
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
| Elicitation modes stated when it connects | Form and URL (recorded) | Form and URL (recorded) | Unknown. Elicitation is supported (staff) | Unknown. Form and URL are supported (docs) | Unknown. Elicitation is not supported (docs) |
| A [`[confirm]` call with no grant](#approving-a-destructive-call-in-the-client) | Without a terminal: asked, and the client dismisses the request by itself (recorded, 2.1.326). In a terminal: a dialog (docs), not tested | Without a terminal: asked, and the client declines by itself (recorded). In a terminal: not tested | Not tested. The request is shown (staff), but not when several windows are open, 3.6.31 (staff) | Not tested. The request is shown during an active turn (docs) | Not tested. Without elicitation, Rune refuses and does not ask |
| When nobody answers the request | Answers `cancel` in 9 ms (recorded, 2.1.326) | Answers `decline` in 1 ms (recorded) | A request that is not shown waits until the call times out, 3.6.31 (staff) | Cancelled in Autopilot, declined when there is no active turn (docs) | Does not apply |

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
- **A `[confirm]` task needs a grant in a session with nobody at it.** Claude Code and
  Codex both tell Rune they can show a form, also when they run without a terminal. Rune
  then asks, and the client answers by itself: `claude -p` dismisses the request and
  `codex exec` declines it (recorded). Nothing runs, and the result says what the operator
  can do. For such a session, name the task in `--allow-destructive`. Where a person is at
  the client, they can [approve the call](#approving-a-destructive-call-in-the-client)
  there: documented for Claude Code and VS Code, stated by Cursor's staff, and not tested
  in any client.
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

Tested: version 2.1.325 on Linux, as `claude -p`, and 2.1.326 for the approval request.

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
  project, in `~/.claude.json` (docs). Such a server finds a Runefile only when it is
  started inside a project; [`-f`](#starting-the-server) pins it to one.
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
- **Approval in the client.** It states the form and the URL mode (recorded), which needs
  2.1.287 or later (docs). Run as `claude -p` in 2.1.326, it answered Rune's request with
  `cancel` after 9 ms, and the model was given
  `task "clean" was not approved: the request was dismissed. Nothing ran.` with the two
  ways out (recorded). In a terminal session it shows a dialog with the form's fields
  (docs). An [`Elicitation` hook](https://code.claude.com/docs/en/hooks#elicitation)
  answers requests with no dialog (docs): with one that accepts, Claude Code approves for
  you.
- **Not tested.** Stopping a call with Esc, and an interactive session, the approval
  dialog included.

Sources: [MCP](https://code.claude.com/docs/en/mcp),
[environment variables](https://code.claude.com/docs/en/env-vars),
[permissions](https://code.claude.com/docs/en/permissions),
[hooks](https://code.claude.com/docs/en/hooks).

### Codex CLI

Tested: version 0.167.0 on Linux, as `codex exec`.

```toml
[mcp_servers.rune]
command = "rune"
args = ["mcp"]
```

- **Configuration.** The block above goes in `~/.codex/config.toml` (docs), or in
  `.codex/config.toml` in a project. Codex starts a server in the directory the session
  was started in (source), so from the user's file `rune mcp` serves the project you
  started Codex in; [`-f`](#starting-the-server) pins it to one. Codex loaded the project
  file in a project the user configuration trusts (recorded). In a directory it does not
  trust, it ignored the file in three runs and never started Rune, so those runs have no
  recording.
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
- **Approval in the client.** It states the form and the URL mode, on revision 2025-06-18
  (recorded). With `default_tools_approval_mode = "approve"`, so that the call reached
  Rune, `codex exec` answered Rune's request with `decline` after 1 ms, and the model was
  given `task "clean" was not approved: the request was declined. Nothing ran.` with the
  two ways out (recorded). So under `codex exec` a `[confirm]` task runs only when it is
  named in `--allow-destructive`. The documentation says nothing about elicitation.
- **Not tested.** An interactive session, where Codex asks for approval and does not
  refuse, and what it shows for Rune's request there.

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
- **Approval in the client.** Cursor supports elicitation "in both the Editor Window and
  the Agents Window", but with several windows open the request can go to the wrong one
  and is not shown, 3.6.31; the call then waits until it times out. No fix was announced
  by 2026-07-09
  ([staff](https://forum.cursor.com/t/mcp-elicitation-does-not-render-in-the-cursor-agents-window-on-macos/162243)).
  Which modes it states, and what the form looks like, are unknown.

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
- **Approval in the client.** "Copilot supports form and URL elicitation during an active
  turn", and VS Code draws the input. A request is cancelled in Autopilot and declined
  when there is no active turn (docs). Either way Rune runs nothing. What it states when
  it connects was not read.

Sources: [MCP configuration reference](https://code.visualstudio.com/docs/agents/reference/mcp-configuration),
[MCP developer guide](https://code.visualstudio.com/api/extension-guides/ai/mcp#elicitation),
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
- **Approval in the client.** Zed "currently supports MCP's Tools and Prompts features",
  and lists elicitation among the features it does not have (docs). A client without it
  is not asked: Rune refuses the call and names `--allow-destructive`. What Zed states
  when it connects was not read.

Sources: [MCP](https://zed.dev/docs/ai/mcp), and in `crates/`:
`context_server/src/types.rs`, `context_server/src/client.rs`,
`agent/src/tools/context_server_registry.rs`, `agent/src/thread.rs`,
`project/src/context_server_store.rs`, `settings_content/src/project.rs`.

## See also

- [agent-driven example](examples/agent-driven/README.md) — a runnable agent task + gating
- [CLI reference](cli.md) — `rune mcp` / `rune serve` flags
- [Runefile language guide](runefile.md) — `[private]`, `[confirm]`, `[network]`, executors
- [Rune-only agent mode](design/rune-only-agent-mode.md): a design note on removing a
  client's shell, and what that does not prevent
- [Guides](how-to/README.md) — the rest of the capability deep dives
