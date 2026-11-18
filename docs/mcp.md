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
| `destructiveHint` | `true` or `false`, always present | The task, or anything it can run, has `[confirm]`, or the task has `[applies]` |
| `openWorldHint` | `true` or `false`, always present | The task, or anything it can run, has `[network]`. The hint is for the client; [the sandbox](#the-sandbox) is what gives or denies the network |

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
- **A task that applies a plan runs only the plan a person approved.** `[applies("plan")]`
  runs the task with the stored plan of `plan`, identified by its SHA-256 digest, only after
  the person at the client approved that plan, and only once. A grant with
  `--allow-destructive` approves no plan; see
  [Applying a reviewed plan](#applying-a-reviewed-plan).
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
- **A task is confined by the operating system.** A task an agent calls has no network
  unless it is marked `[network]`, and writes only inside the project. Where no sandbox
  can do that, the call is refused, unless the operator started the server with
  `--sandbox off`; see [The sandbox](#the-sandbox).
- **Network tasks are flagged.** `[network]` also sets the MCP `openWorldHint` on the task
  and on every task that can reach it.
- **Secrets come from the environment only.** API keys and secrets are read from the
  environment (or the agent CLI's own session) — **never** from the Runefile, and they are
  never included in any tool description, schema, or log.
- **Task output is masked.** Even if a task prints a sensitive environment variable, the
  tool result the agent receives shows `***` in place of the value — identical to what a
  terminal user sees — so credentials never enter an agent's chat history or memory. This
  is always on, with no agent-facing off switch; see
  [secret masking](how-to/secret-masking.md).
- **Every call is recorded before it runs.** The server writes the start of a tool call to
  the [audit log](#the-audit-log) before anything of the call runs, and refuses the call
  when it cannot. Refused and invalid calls are recorded too. `rune log` reads the log.
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

## Applying a reviewed plan

An agent runs `plan`, a person looks at the result and approves, and the agent then runs
`apply`. By themselves the three are not tied together: `apply` can plan again and change
things nobody looked at, and a file left at the plan's path can be swapped between the
review and the apply.

Two task attributes tie them together. `[plan(...)]` marks the task that makes a plan and
names the file it writes. `[applies("...")]` marks the task that applies the plan of a named
plan task. Rune takes the plan file into a store it manages, identifies it by its SHA-256
digest, and lets the apply task run only with that stored file, only after a person approved
that digest, and only once.

```rune
# Plan the infrastructure change.
[plan(file="tfplan", summary="tfplan.txt", tool="tofu")]
plan env:enum("staging","prod"):
    tofu plan -var env={{quote(env)}} -out=tfplan
    tofu show -no-color tfplan > tfplan.txt

# Apply the reviewed plan.
[applies("plan")]
apply env:enum("staging","prod"):
    tofu apply "$RUNE_PLAN_FILE"
```

Served with `rune mcp`:

| Call | Result |
|---|---|
| An agent calls `plan` with `env=prod` | Runs. Rune moves `tfplan` and `tfplan.txt` into its store and reports the plan's id and digest |
| An agent calls `apply` with `env=prod` | The person at the client is shown the target, the digest and the summary, and asked |
| The person approves | `apply` runs once with `RUNE_PLAN_FILE` set to a private copy of the stored plan |
| An agent calls `apply` again | Refused: the plan was already used |
| An agent calls `apply` with no plan made | Refused: there is no plan for these arguments |
| The stored plan or its summary was changed on disk | Refused: the file does not match its digest |
| The Runefile changed after the plan | Refused: the plan was made for another definition |
| An agent calls `plan` again, then `apply` | The person is asked about the new plan. The old one is gone |
| The client cannot show a request | Refused, with the command the operator can run |
| A person runs `rune plan prod`, then `rune apply prod` | The terminal shows the same identity and summary and asks |

The relationship is only what the attributes state. Rune does not infer it from task names.
The syntax, the arguments of `[plan]` and the static rules are in the
[Runefile guide](runefile.md#plan-and-apply). A Runefile with neither attribute is served and
run as before, and no store is created. Two runnable examples are
[plan-apply-opentofu](examples/plan-apply-opentofu/README.md) and
[plan-apply-terraform](examples/plan-apply-terraform/README.md).

### The flow

1. **Plan.** The agent calls `plan`. When the body ends without error, Rune copies the
   declared file and its summary into the store under `.rune/plans/` beside the Runefile,
   computes the SHA-256 digest of the file while copying, and removes both from the
   workspace. The stored bytes equal the bytes read. The result reports the plan:

   ```text
   plan <id>: sha256:<hex>, <n> bytes, expires <time>. Apply it with "<apply>".
   ```

2. **Request.** The agent calls `apply` with the same arguments. Rune finds the newest plan
   of the plan task whose arguments equal the call's, and checks it. No argument, environment
   variable or file the agent supplies selects or approves a plan: the tool's input schema
   holds the task's parameters only.
3. **Approval.** The person at the client is shown the task, its arguments, the plan's id,
   the digest and size of the artifact, and the summary, and approves that one call.
4. **Apply.** Rune claims the plan, so that of any number of calls and processes exactly one
   proceeds. It makes a private copy of the stored plan, verified against the recorded digest
   while it is copied, read-only, in a directory only the owner can enter. The body of `apply`
   runs once with `RUNE_PLAN_FILE` naming that copy. The body of `plan` does not run. When
   the run ends, Rune verifies the copy again, records the plan as applied or failed with the
   exit code, and deletes the artifact, the summary and the copy.

The plan line is on standard error, so it is in the tool result and on the terminal. `<id>`
is 16 hex characters, and `<time>` is RFC 3339 in UTC, to the second. With several apply
tasks the names are joined by ` or `. With none the last sentence is `No task applies it.`

- **A plan is stored when the body ends.** If the body fails, nothing is stored and an
  earlier plan for the same arguments stays as it was. A hook of the plan task that fails
  afterwards does not undo the plan.
- **`--dry-run` and `--summary` store nothing.** When `plan` or `apply` is named with one of
  them, nothing is stored, asked or used.
- **A file at the workspace path is not used.** A file placed at `tfplan` after the plan is
  not what `apply` receives. The body gets the stored bytes.
- **The tool of an apply task says so.** Its `destructiveHint` is true, and its description
  ends with
  `Plan approval: applies the plan made by task "plan". Every call asks the person at the client to approve that plan.`
  The tool of a plan task is unchanged.

A plan task fails, after its body ended without error, when the plan cannot be stored:

| When | The task fails with |
|---|---|
| The file or the summary cannot be stored | `plan file "<path>" of task "plan" <detail>`, or `plan summary "<path>" ...`, with `is missing`, `is not a regular file`, `is a symbolic link` or `is larger than <limit>` |
| The declared tool is not on the path | `plan tool "<name>" of task "plan" was not found on the path` |
| The declared tool is on the path and cannot be read | `plan tool "<name>" of task "plan" cannot be identified: <reason>` |
| The store cannot be used | `the plan of task "plan" was not stored: <reason>` |

The limit is 256 MiB for the file and 1 MiB for the summary. A workspace file that cannot
be removed after it was stored is a warning,
`plan <id>: could not remove "<path>" from the workspace: <reason>`, and the plan stands.

### The approval request

Every `apply` call from an agent is put to the person at the client, through the request of
[Approving a destructive call in the client](#approving-a-destructive-call-in-the-client).
The form, the five-minute wait and what counts as yes are the same. The request also shows
the plan:

```text
Approve this Rune task call?

Task: apply
Arguments:
- env: "prod"
Plan: <id> made by "plan" at <time>
Artifact: sha256:<hex> (<n> bytes)
Note: the previous apply of this target failed at <time>. Check the target before approving.
Summary:
  <line>
  [summary cut: <n> bytes not shown]
  <line>
Destructive tasks it can run:
- apply: Run "apply"?

Approving applies this plan once.
```

- **The `Note:` line.** It appears only when the newest earlier attempt for the same plan
  task and arguments failed or did not finish. For an unfinished one it reads
  `Note: the previous apply of this target started at <time> and did not finish. Check the target before approving.`
- **The summary.** It is what the plan body wrote to the `summary` file. Each line is
  indented by two spaces, secrets are masked like task output, and a character that is not
  printable is shown as an escape. An empty summary is the one line `  (empty)`, and the plan
  can still be approved.
- **A long summary.** The summary takes at most 4096 bytes of the request. A longer one
  shows its head and its tail: the first lines that fit in 3072 bytes, the cut mark, and the
  last lines that fit in 1024 bytes, because a plan tool prints its totals last. A single
  line longer than its share is cut inside the line. `<n>` counts the rendered bytes left
  out.
- **The size of the request.** The whole request is at most 8 KiB. A call whose request
  would be larger is refused as too large. The summary can never cause that by itself.
- **With `[confirm]`.** When `apply` also has `[confirm("Really?")]`, one request is sent,
  and it holds both the plan and the confirmation message. When `apply` has a dependency with
  a `[confirm]` the operator did not grant, the one request names that task too.
- **What an approval covers.** The one call it was given in, and that plan, once. It is
  never stored, so it cannot be reused or go stale. What can go stale is the plan, which
  carries the expiry.

A request that is declined, dismissed, timed out or cancelled, or answered after a Runefile
[reload](#reloading), runs nothing and leaves the plan usable: a later approved call can
still apply it. The result is one of the texts of
[Approving a destructive call in the client](#approving-a-destructive-call-in-the-client),
and what follows it names the command only, never `--allow-destructive`:

```text
task "apply" was not approved: the request was declined. Nothing ran. To run it without a request, the operator can run "rune apply" themselves.
```

### What an agent is refused

A refusal is an error result with no exit code and no task output, and nothing ran. The same
state gives the same text. Every value shown is masked and bounded: the arguments are written
as `name="value"` pairs joined by `, `, each value cut to 64 characters with `...`.

The refusals of the plan checks and of the claim start with `task "apply" was not run: `.

| When | The text goes on |
|---|---|
| No plan was made for these arguments | `there is no plan for env="prod"; run "plan" with the same arguments first` |
| No plan was made, and the task has no parameters | `there is no plan; run "plan" first` |
| The plan was applied | `plan <id> was already applied at <time>; run "plan" again and review the new plan` |
| The apply of the plan failed | `the apply of plan <id> failed at <time>; check the target, then run "plan" again and review the new plan` |
| An apply of the plan did not finish | `an apply of plan <id> started at <time> and did not finish, so its outcome is unknown; check the target, then run "plan" again and review the new plan` |
| The plan expired, before the request or while it was pending | `plan <id> expired at <time>; run "plan" again` |
| A newer plan was stored while the request was pending | `plan <id> was replaced by a newer plan; call "apply" again` |
| A part the plan is bound to changed | `plan <id> no longer matches: the <parts> changed since it was made; run "plan" again` |
| The stored artifact or summary does not verify, before the request or after the approval | `the stored <what> of plan <id> <detail>; run "plan" again` |
| Two calls were approved and the other one got the plan | `plan <id> was already used by another call` |
| The store cannot be used | `the plan store cannot be used: <reason>` |

`<parts>` is one or more of `Runefile definition`, `configuration`, `working directory`,
`host` and `tool "<name>"`, in this order, joined by `, `. A declared tool that is no longer
on the path counts as changed. `<what>` is `artifact` or `summary`. `<detail>` is
`is missing`, `is not a regular file` or `does not have the recorded digest`. A stored file
that was replaced by a symbolic link is not a regular file.

**The order of checks.** For one `apply` call from an agent:

1. Unknown argument names, then value types, missing arguments and defaults.
2. The [`[agent-allow]` policy](#restricting-the-values-an-agent-may-pass). A value it
   refuses is refused for the value, and the person is not asked. This holds for a client
   that cannot be asked too.
3. The plan: it is found, it is ready, the parts it is bound to are unchanged, and the stored
   artifact and summary have their recorded digests.
4. The approval request, with the plan and every `[confirm]` message in it. Where the person
   cannot be asked, the refusal that says why.
5. The claim and the private copy.
6. The run, and then the record of how it ended.

A refusal at steps 1 to 3 sends no request. A refusal at step 4 or 5 leaves the plan's state
as it was. A `[confirm]` call from a client that cannot be asked is refused before its
arguments are read. An `apply` call is checked first, so a call that could not run anyway is
refused for its own reason.

**A grant approves no plan.** `--allow-destructive apply` is accepted and has no effect on
the plan approval: the person is still asked on every call. No server flag approves a
plan. The server prints a warning when it starts and on a reload, and its startup line does
not list the task among the granted ones:

```text
warning: --allow-destructive apply approves no plan: task "apply" asks the person at the client on every call
```

**A client that cannot be asked.** A client that stated no form elicitation is refused, and
nothing runs. The text names the command the operator can run and the reason:

```text
task "apply" applies a plan and runs only when the person at the client approves that plan. The operator can run "rune apply" themselves. Rune could not ask for approval here: the client does not support MCP form elicitation.
```

A call whose request would be too large is refused with the same text and the reason
`the approval request would be too large to show`. As for a `[confirm]` call, the command
names the task only, so add the arguments the task needs, and a server started with
[`-f`](#starting-the-server) names its Runefile in it.

**The command line is the manual way.** The plan the agent made is in the store, and plans
made through the server and from the command line share that store. The operator runs
`rune apply prod` in the same directory and is asked at the terminal about the same plan:

```text
Plan <id> made by "plan" at <time>
Target: apply env="prod"
Artifact: sha256:<hex> (<n> bytes)
Note: <as in the request>
Summary:
  <line>
Apply this plan once? [y/N]
```

- `y` runs the apply with the stored plan. Any other answer runs nothing and leaves the plan
  usable.
- With `--yes` the identity is printed and the apply runs without a question.
- Without a terminal and without `--yes` the call is refused, as a `[confirm]` task is:
  `task "apply" was not confirmed (Apply this plan once?): stdin is not a terminal; pass --yes to approve`.
- A `[confirm("...")]` message of the apply task is printed as `Confirmation: <message>`
  above the question, and the one answer covers it.
- The terminal shows more of a long summary than the request: a head of 48 KiB and a tail
  of 16 KiB.
- An apply task must be the only task named in a command. Otherwise it is the usage error
  `a task that applies a plan must be the only task named in a command`.
- The command line makes the same checks and gives the same refusals as the table above.

**A task body that runs `rune`.** In an agent run, the command line refuses to run an apply
task, with or without `--yes`. When an agent calls a task whose body runs
`rune --yes apply prod`, the inner `rune` exits with an error and
`task "apply" applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it`.

**`agent` tasks.** The endpoint an [`agent` task](#agent-task-bodies) opens for its own
agent never asks, so an `apply` call there is refused:
`task "apply" applies a plan and runs only when the person at the client approves that plan; nobody can be asked in this session.`
Where the server has a command to name, the text goes on with
` The operator can run "rune apply" themselves.`

### The states of a plan

| State | Meaning | Can be applied |
|---|---|---|
| `ready` | Stored, and not claimed, replaced or expired | Yes |
| `unfinished` | A call claimed it and no outcome is recorded | No |
| `applied` | The apply ended without failure. Its exit code is recorded | No |
| `failed` | The apply failed, or the plan file changed while it was being applied. Its exit code is recorded | No |
| `replaced` | A newer plan of the same task and arguments was stored | No |
| `expired` | The time is after its expiry | No |

A plan goes from `ready` to `replaced`, to `expired`, or, at the claim, to `unfinished`, and
from `unfinished` to `applied` or `failed`. There is no way back to `ready`.

- **The target is the arguments.** A plan belongs to the Runefile's path, the plan task and
  the effective arguments, defaults included. The plan and apply tasks have the same
  parameters, and `apply` uses the plan made for equal arguments. A plan for `env=staging`
  is no plan for `env=prod`. The elements of a variadic parameter are part of the arguments
  compared. Two Runefiles in one directory keep their plans apart.
- **Only the newest plan can be applied.** A new plan of the same plan task with the same
  arguments replaces the earlier unused one, and its artifact bytes are deleted. When two
  plan runs for the same arguments end at once, both are stored and the newer one replaces
  the other.
- **A plan expires.** One hour after it was made, or after the time `expires` sets, from one
  minute to seven days.
- **A claim uses up the plan, whatever stops the attempt.** A dependency of `apply` that
  fails before the body starts still consumes the plan. It is recorded as used and failed,
  and the attempt is not given back.
- **What a plan is bound to.** The arguments, the working directory of the plan task, the
  Runefile definition, the configuration, the host's operating system and architecture, and,
  when `tool` is declared, the tool. A change to any of them refuses the apply.
- **The Runefile definition is the whole Runefile**, composed with what it imports. Any
  change to it after a plan refuses the apply. That is coarser than the apply task alone and
  cannot miss a change.
- **The configuration is Rune's.** It is the resolved settings, the dotenv file and the
  variable overrides of the command line: `rune region=eu plan` and `rune region=us apply`
  are different configurations, and the apply is refused. The tool's own configuration is
  inside its saved plan and under its own checks.
- **The tool is an executable.** Rune does not run the tool outside a task body, so it
  records the path and the digest of the executable that the `tool` name resolves to in the
  plan body's environment and directory. At the apply it resolves the name again, in the
  apply body's environment and directory, and that has to be the same executable: an apply
  task whose own `PATH` finds another one is refused.
- **Recorded, not compared.** The Rune version, and the git commit and whether the tree was
  dirty, when `git` can tell. The plan file and its summary are what the task makes, so
  they do not make the tree dirty.
- **A reload during an apply.** The call runs against the load of the Runefile it started
  with. Its plan was checked against that load.

### Retention and the journal

The store is `.rune/plans/` in the Runefile's directory, where the cache already is, so the
server and the command line see the same plans. It is local: there is no remote or shared
store. It is created when the first plan is stored.

- **Modes.** Directories are created with mode `0700` and files with `0600` or `0400`, so no
  file is readable by group or others.
- **Links.** `.rune` and `.rune/plans` must be real directories. A symbolic link is refused,
  and on POSIX hosts `.rune/plans` must belong to the current user.
- **Version control.** `.rune/plans/.gitignore` holds `*`, so nothing in the store is
  offered for commit.
- **A plan can hold secrets.** Its bytes are in the store only: in the artifact, the summary
  and the private copy. The record of a plan holds digests, the masked text of the arguments
  and times, never plan or summary bytes.

| What | Limit |
|---|---|
| Artifact | 256 MiB |
| Summary | 1 MiB |
| Default expiry | 1 hour |
| Artifact and summary bytes | Deleted when the plan is applied, failed, replaced or expired, at the latest when the store is next used |
| Bytes of an unfinished attempt | Kept for 24 hours after the claim, because an apply may still be reading them, and deleted then |
| Record | Removed 7 days after its last event |
| A plan directory with no record, left by a process that was killed while storing | Removed once it is 24 hours old |
| Journal | 1 MiB, then the older half of the lines is dropped |

Rune cleans the store when a plan is stored and when an `apply` call looks one up. Nothing
runs in the background.

The journal is `.rune/plans/journal.jsonl`, with one JSON object per line and mode `0600`.
It links a plan's creation, each approval request and its answer, and each apply attempt:

```json
{"time":"2026-11-17T10:04:11Z","event":"plan-created","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:02Z","event":"approval-requested","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:09Z","event":"approval-given","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:09Z","event":"apply-started","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\"","attempt":"c41d0e7a9b3355f2"}
{"time":"2026-11-17T10:05:31Z","event":"apply-finished","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\"","attempt":"c41d0e7a9b3355f2","exit":0,"state":"applied"}
```

| Event | Written when | It also carries |
|---|---|---|
| `plan-created` | A plan was stored | |
| `plan-replaced` | A newer plan took a plan's place | `replacedBy`, the id of the newer plan |
| `approval-requested` | An apply call reached the approval, at the client or at the terminal | `applyTask` |
| `approval-given` | The person approved | `applyTask`, and `reason` when `--yes` answered |
| `approval-not-given` | The person did not approve, or could not be asked | `applyTask`, `reason` |
| `apply-refused` | A plan check or the claim refused the call | `applyTask`, `reason` |
| `apply-started` | The plan was claimed | `applyTask`, `attempt` |
| `apply-finished` | The run ended | `applyTask`, `attempt`, `exit`, `state`, `tampered` |

- Every line carries the time, the event, the surface (`mcp` or `cli`), the plan task and
  the arguments, and the plan's id and artifact digest whenever a plan is known. Only an
  `apply-refused` for no plan has none.
- A line never holds plan or summary bytes, and argument values are masked.
- A call refused for its arguments or by the `[agent-allow]` policy involves no plan and is
  not in the journal.
- A person who could not be asked is recorded as `approval-requested` followed by
  `approval-not-given` with the reason.
- When a line cannot be written, Rune prints a warning and goes on.
- A line written during an agent's tool call also carries `invocation`, the id of that call
  in [the audit log](#the-audit-log). A line written from the command line has none.

### Recovery

Rune never runs an apply again by itself. A plan whose apply failed or did not finish can
never be applied, and a new plan costs one more review.

| What happened | State of the plan | What to do |
|---|---|---|
| The body of `apply` failed | `failed`, with the exit code. The result is the task's failure | Check the target. Run `plan` again and review the new plan |
| The tool refused the plan because the remote state changed | `failed` | Run `plan` again and review the new plan |
| A dependency of `apply` failed before the body started | `failed` | Run `plan` again and review the new plan |
| The apply was stopped before it ended, such as by a killed server | `unfinished`. The outcome is unknown | Check the target. Run `plan` again and review the new plan |
| The plan file changed while it was being applied | `failed`, marked as tampered | Check the target. Run `plan` again and review the new plan |
| The request was declined, dismissed, timed out or cancelled | `ready` | Call `apply` again |
| The Runefile was reloaded while the request was pending | `ready` | Call `apply` again. If the Runefile definition changed, that call is refused and `plan` has to run again |
| A new plan was stored while the request was pending | The old plan is `replaced`. No failure is recorded | Call `apply` again. The request is about the new plan |
| The client sent the call again after a connection loss | Unchanged | It is a new call: it asks again, and runs only if the plan is still unused |

- **After a failed or unfinished apply, the next request says so.** The request for a later
  plan of the same plan task and arguments carries the `Note:` line with the time of that
  attempt.
- **A changed plan file.** Rune checks the private copy when the run of the apply call ends.
  If its digest differs, standard error gets
  `plan <id>: the plan file changed while it was being applied; recorded as failed and tampered`
  and the call fails. A call that would have ended with exit code 0 ends with 1.
- **There is no rollback.** Rune does not undo a failed or partial apply.

### What a matching digest proves

A matching digest proves that the bytes the apply task receives are the bytes that were
stored when the plan task finished, and that the summary shown is the one stored with them.
Both are checked against their recorded digests before the person is asked.

It does not prove that the remote system is unchanged since the plan. The tool's own state
lock and stale-plan check stay in force, Rune does not replace or weaken them, and a stale
plan fails in the tool. It does not undo a partial apply.

**What the binding does not see.**

- Values read by `env(...)` are not part of any digest.
- Scripts a body calls are not part of any digest.
- A version manager's shim keeps one identity across the versions it selects, because the
  tool's identity is the executable the name resolves to.
- A dotenv file edited under a running server changes nothing for that server until it
  restarts. The file is not watched, and the server keeps the configuration it loaded, so its
  own plans stay valid for it. The command line, and a server started or reloaded since, read
  the changed file and refuse the apply: the configuration changed.

**Trusted execution assumptions.**

- **Processes of the same user are trusted.** File modes keep other users of the machine
  out of the store. They do not keep out a process of the same user: an agent that has its
  own shell can write there, as it can edit the Runefile.
  [Rune-only agent mode](design/rune-only-agent-mode.md) is about closing that. The private
  copy is checked again after the apply, so that a change is at least recorded.
- **On Windows, file modes do not restrict access.** Every other rule holds.
- **The summary is the author's.** The plan body writes it, for example with `tofu show`.
  Rune stores it with the artifact in one record and shows it. Rune does not parse the
  artifact, so the summary is what the plan task wrote.
- **The client reports the answer.** As for every approval request, Rune trusts the client
  to show the request and to report your answer.

### Tool compatibility

| Workflow | Exact-plan integration | How |
|---|---|---|
| OpenTofu | Supported | Save the plan with `tofu plan -out=tfplan`, apply it with `tofu apply "$RUNE_PLAN_FILE"`. See [plan-apply-opentofu](examples/plan-apply-opentofu/README.md) |
| Terraform | Supported | Save the plan with `terraform plan -out=tfplan`, apply it with `terraform apply "$RUNE_PLAN_FILE"`. See [plan-apply-terraform](examples/plan-apply-terraform/README.md) |
| Helm | Not supported | See below |
| Database migration runners | Not supported | See below |

**OpenTofu and Terraform.** `plan -out` writes one file, and `apply <file>` consumes that
file with the tool's own stale-plan check. That file is the artifact Rune stores, and the
output of `show` is the summary.

**Helm is not supported.** Helm has no saved plan: a dry run renders manifests. An
exact-plan integration would have to bind the chart or package, the values, the rendered
output, the hooks and post-renderers, the target and the release state, and verify the
reviewed payload before anything is changed. Do not present a Helm dry run or rendered
manifests as a saved plan. A dry run is not a saved plan that Helm replays, and applying
rendered manifests directly does not keep Helm's release and hook semantics.

**Database migration runners are not supported.** A migration runner validates the checksums
of a directory against a live database and has no single artifact to bind. An exact-plan
integration would have to bind the ordered migration bundle and its checksums, the runner
and its options, the database identity and the schema-history baseline, and validate them
under the runner's locking rules. Checksums alone do not freeze the target database.

## The sandbox

A task an agent calls runs in a sandbox that the operating system enforces. It has two
rules:

1. A task has no network unless its author marked it `[network]`.
2. A task writes only inside the project, and not to the files that decide what it may do.

```text
$ rune mcp
rune MCP server on stdio (sandbox: bubblewrap)
```

The sandbox applies to every task body that runs for an agent: a tool call on `rune mcp`
or `rune serve`, on either transport; a tool call on the endpoint an `(agent)` task opens;
and the `[context]` task, at a server's start and before an `(agent)` task's prompt. It
covers the whole body: the built-in shell with its redirections and built-in commands, an
interpreter, a shell override, an agent program, and every process any of them starts. Each
body has a sandbox of its own, so a task's dependencies and hooks get their own rules.

A task a person runs on the command line is not confined.

### What it does not do

- It does not decide which tasks an agent may call. `--allow`, `--deny`,
  `--allow-destructive`, approvals, `[agent-allow]` and plans do that, as before.
- It does not keep a task from reading. A task reads what its user can read, apart from
  the [hidden locations](#what-a-task-can-read). What a task prints goes to the agent.
- It does not limit CPU, memory or disk use.

### The mode

The sandbox has two modes. `enforce` is the default.

| Set with | Form |
|---|---|
| A flag of `rune mcp` and `rune serve` | `--sandbox enforce` or `--sandbox off` |
| The environment of the server | `RUNE_SANDBOX=enforce` or `RUNE_SANDBOX=off` |

The flag wins over the variable. The endpoint of an `(agent)` task has no flag and reads
the variable. A Runefile cannot set the mode: a task could edit a Runefile, and it cannot
change the flags or the environment of the server that runs it. Any other value is a usage
error before any Runefile code runs.

In mode `off` tasks run as they did before there was a sandbox, and the server says so:

```text
warning: sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can.
rune MCP server on stdio (sandbox: off, tasks run unsandboxed)
```

There is no mode that confines where it can and runs unconfined elsewhere. That would be a
silent fallback.

### Where it works

| System | State | A call |
|---|---|---|
| Linux with [bubblewrap](https://github.com/containers/bubblewrap) and user namespaces for it | supported | runs confined |
| Linux without bubblewrap on the `PATH` | unavailable | is refused |
| Linux where bubblewrap cannot create namespaces, such as a default container | unavailable | is refused |
| Linux where the `PATH` holds a directory under `/run`, `/tmp` or `/var/tmp`, such as NixOS | unavailable | is refused |
| macOS, Windows and every other system | unsupported | is refused |

Tested with bubblewrap 0.8.0 on Debian 12 in a privileged container, and with bubblewrap
0.11.1 on Linux 7.0 under Ubuntu's AppArmor profile for it. Install it with your package
manager, for example `apt install bubblewrap`.

Rune decides by starting a sandbox, not by asking for a version. It looks when the server
starts, and again on a call while the sandbox is unavailable, so a backend installed later
is used without a restart.

A server whose sandbox is unavailable still starts, so that the tool list and `rune log`
work. It says what is wrong and refuses every call before anything of it runs:

```text
warning: sandbox: bubblewrap (bwrap) was not found on the PATH. Every task call will be refused. Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.
rune MCP server on stdio (sandbox: unavailable, calls are refused)
```

The agent gets the same reason and remedy as the error of its call:
`task "build" was not run: the sandbox is unavailable: bubblewrap (bwrap) was not found on the PATH. Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed.`

The [container image](docker.md) has no backend. There the container is the boundary:
start the server with `--sandbox off`.

### What `[network]` gives

| | Without `[network]` | With `[network]` |
|---|---|---|
| Outbound connections, any protocol | fail | work |
| Name lookup | fails | works |
| Services on the host's loopback address | out of reach | reachable |
| Sockets in the host's abstract namespace | out of reach | reachable |
| A listener the body starts and connects to | works, on a loopback of the body's own | works |

A task without `[network]` has a network of its own with one loopback interface and
nothing on it. The denial does not depend on proxy variables or on the name of a program.

`[network]` gives the task the host's network as a whole. It applies to the body of the
task that carries it and to the processes that body starts. It does not apply to the
task's dependencies, hooks or callers, and it changes no filesystem rule. A body that
starts `rune` stays in its own sandbox: an inner `[network]` task gains nothing.

### What a task can write

- The project: the directory of the Runefile, with symbolic links resolved.
- `/tmp`: a directory of the body's own, new for each body and removed when the body
  ends. `TMPDIR` names it.
- A cache directory of the project's own that stays between calls. `XDG_CACHE_HOME` names
  it. It lives under your cache directory, in `rune/sandbox/`, outside the project.

Everything else is read-only. A write by an absolute path, through `..`, through a symbolic
link or through a second mount of the project fails with `Read-only file system`.

A task whose `[working-directory]`, or whose `set working-directory`, names a directory
outside the project is refused before anything runs. So is every task of a project that
is itself one of the [hidden locations](#what-a-task-can-read), such as a Runefile kept in
`~/.docker`.

### What a task cannot change

A task reads these and cannot change, remove, rename or replace them:

| Path | Why |
|---|---|
| The Runefile, and every file it imports or loads as a module | They decide which tasks exist and what each may do |
| The file named by `set dotenv` | It is part of the configuration a plan is bound to |
| `.git` in the project | The repository's own state and configuration |
| `.rune` in the project | Rune's task cache and the stored plans with their approved copies |

A person who edits the Runefile while the server runs is not a task: the server reloads as
before.

Four cases cannot be protected by a mount, and Rune refuses the call instead, with the
path named:

| Case | What to do |
|---|---|
| The Runefile, a file it loads or the dotenv file has a second name (a hard link) | Remove the other name, or replace the file with a copy of itself |
| One of the protected paths is reached through a symbolic link inside the project | Use the real path |
| The Runefile looks for a file inside the project that does not exist: an optional import, or a module that lives in a directory and is declared without its path | Create the file, give the module its path (`mod api "api/Runefile"`), or remove the optional import |
| The server was started in a directory below the Runefile's and found the Runefile by searching up | Start the server in the Runefile's directory, or name the Runefile with `--file` |

The last two matter because a task could create the missing file, or a nearer Runefile,
and the next load or the next start would read it. For the same reason a directory that
holds more than one Runefile name, such as `Runefile` and `.runefile`, is an error for every
`rune` command: Rune does not pick one.

Rune's own work for a call happens in the server and keeps working: the task cache,
capturing and applying a plan, and the audit record. When Rune asks `git` for the commit
and whether the tree is dirty, it does so inside a sandbox too, with no network and
nothing writable, so a repository that a task prepared cannot run a command outside one.

### What a task can read

A task sees the rest of the filesystem read-only, as your user does. These locations are
empty for it, and read-only:

- `/run` with the per-user runtime directories under it, and `/var/tmp`. This is where
  the container daemon's socket, the session bus and the desktop's sockets are.
- The host's `/tmp`, which the body's own directory replaces.
- The directory of the audit logs.
- The socket named by `SSH_AUTH_SOCK`, and the socket of a `DOCKER_HOST` that starts with
  `unix://`.
- In your home directory, when they exist: `.ssh`, `.gnupg`, `.aws`, `.azure`, `.kube`,
  `.docker`, `.config/gcloud`, `.config/gh`, `.netrc`, `.git-credentials`, `.npmrc`,
  `.pypirc` and `.password-store`. This holds when the Runefile is in the home directory
  itself.

A task sees only the null, zero, full, random and terminal devices, and only its own
processes. It inherits no open file of the server, has no terminal, and holds no
capability, also when the server runs as root.

### Credentials

A credential reaches a task through the environment, as before, and is masked in what the
task prints. Credential files and agent sockets in the hidden locations are not available.
A task that needs one is a task for a person to run, or for a server started with
`--sandbox off`.

### Processes end with their body

Every process a body starts ends when the body ends, when the call is cancelled, and when
the server stops. This includes a process that detached itself. A task that starts a
service for later calls does not work when confined.

### When a task fails in the sandbox

A program that fails because of the sandbox names no cause: it says
`Could not resolve host` or `Read-only file system`. Rune adds one line to the output of a
confined body that failed:

```text
note: task "build" ran in the sandbox: it has no network (it is not marked [network]) and can write only inside /repo.
```

A body that succeeds gets no such line.

### Making a Runefile work in the sandbox

- Mark a task that downloads, publishes or calls a service with `[network]`.
- Give a module in a directory its path, and create or remove an optional import.
- Point a tool that writes under your home directory into the project with `[env]`. A
  tool that follows `XDG_CACHE_HOME` already writes to the private cache directory.
  For Go, a module that is not in the module cache yet needs `[network]` and a writable
  `GOMODCACHE`, for example `[env("GOMODCACHE", ".cache/gomod")]`.
- A task that commits, tags or fetches writes `.git`. Run it yourself.

### Limits

- A task reads what your user can read outside the hidden locations. The sandbox is not
  secret confidentiality.
- A `[network]` task shares the host's network: its loopback services and its
  abstract-namespace sockets included.
- A file inside the project that already has a second name outside it is the same file,
  and a write changes both. Package managers such as pnpm and uv make such links to a
  store outside the project. Rune detects this only for the Runefile, the files it loads
  and the dotenv file.
- A Unix socket in a readable location that is not hidden stays reachable.
- The configuration files of an agent client inside the project, such as `.mcp.json`, are
  writable like the rest of the project. See
  [Rune-only agent mode](design/rune-only-agent-mode.md).
- An `(agent)` task that an agent calls runs its agent program inside the sandbox. It
  needs `[network]` to reach its model, and an agent program that writes its own state
  under your home directory fails there.

## The audit log

Every Rune MCP server records what agents ask of it: `rune mcp`, `rune serve` and the
endpoint an `(agent)` task opens for its agent. The record outlives the client's
conversation. `rune log` reads it.

```text
$ rune log
Agent actions on 2026-11-18 (local, UTC+02:00) for /repo/Runefile

10:04:11  completed   plan env="prod"       exit 0  12.4s  claude-code 2.1.0  3fa9c2d1e07b5a44
          plan 7f3c9a1e52b84d06 sha256:9b1f8c2e0d41 stored
10:05:02  completed   apply env="prod"      exit 0  22.1s  claude-code 2.1.0  b81e04f7a2c9d310
          approved at the client; plan 7f3c9a1e52b84d06 sha256:9b1f8c2e0d41
10:07:40  refused     clean                 -       0.0s   claude-code 2.1.0  5d20e1b9c4a7f863
          policy: task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server
10:08:15  failed      test pkg="./cli"      exit 1  48.0s  claude-code 2.1.0  0c7a91e3b5d2f648
10:09:00  incomplete  deploy env="staging"  -       -      codex 0.9.2        e4b6a8d0127c95f3
          no completion was recorded: the call is still running or the server stopped
```

### What is recorded

A tool call is recorded twice: when it arrives, before anything of it runs, and when it
ends. Both records carry the same invocation id. A call that was refused, named a tool that
does not exist or had invalid arguments is recorded like one that ran, so the log holds the
attempts as well as the runs.

| Recorded | When |
|---|---|
| The invocation id, the session id and the UTC start time | At the start |
| The tool name, and the task it names | At the start |
| The arguments as the agent sent them | At the start |
| The client name and version, the transport and how the session was authenticated | At the start |
| The Runefile path, the digests of the Runefile and of the configuration, and the git state | At the start |
| Whether a sandbox was enforced, the backend that enforced it, and the server's mode | At the start |
| The outcome, the UTC end time and the duration | At the end |
| The exit code, when a task ran | At the end |
| The arguments the task ran with, defaults included | At the end |
| The kind and text of the error, when no task ran | At the end |
| The approval decision and the tasks it covered | At the end |
| The tasks the operator's `--allow-destructive` allowed | At the end |
| The id and digest of the plan the call stored or applied | At the end |

A request that is not a tool call, such as `initialize` or `tools/list`, gets one record
with its method, session, client and duration, and nothing of its content. A request
refused for its bearer token gets one record with the time and the remote address. At most
10 of those are written per minute, and the next one says how many were left out.

The run of an `(agent)` task that opens an endpoint is recorded too, with its start, end
and exit code. Each call the agent makes through the endpoint names that run as its
parent.

| Outcome | Meaning | Exit code |
|---|---|---|
| `completed` | The task ran and exited 0 | 0 |
| `failed` | The task ran and exited with another code | The code |
| `refused` | A policy, a missing grant, an approval that was not given or a sandbox that could not confine the call stopped it | None |
| `invalid-arguments` | The arguments did not fit the task | None |
| `unknown-tool` | No task has the name | None |
| `cancelled` | The call was cancelled | Only if a task ran |
| `timed-out` | An approval request got no answer in time, or the call's deadline passed | Only if a task ran |
| `error` | Something else stopped the call | None |
| `incomplete` | There is a start and no end: the call is still running, or the server stopped | None |

A refused call never has an exit code, in the files or in any output. When no task ran, the
record names the kind of error: `protocol`, `validation`, `policy`, `approval`, `sandbox`
or `internal`. `sandbox` is a call refused because [the sandbox](#the-sandbox) could not
confine it.

The git state is `commit` with the commit id and whether the tree was dirty, `unborn` for a
repository with no commit yet, `none` outside a repository, or `unavailable` when git could
not be asked. A dirty flag that git did not give is left out. It is never written as clean.

The client name and version are what the client sent in the handshake. They say which
program asked, not which person. Rune records no person: an approval is recorded as given
at the client, and the bearer token of the HTTP transport is one shared secret that
identifies nobody.

### Reading the log

`rune log` lists the tool calls, agent runs and authentication failures of the project it
is run in, oldest first. With no flags it lists today's.

| Flag | Meaning |
|---|---|
| `--date 2026-11-17` | One day |
| `--since <when>`, `--until <when>` | A range. Each takes a day, an RFC 3339 time, or a duration counted back from now, such as `2h` or `7d`. A day given to `--until` includes that day |
| `--tz <zone>` | The display time zone: an IANA name such as `Europe/Kyiv`, `UTC`, or `local` |
| `--task <name>` | Calls of these tasks or tools |
| `--outcome <outcome>` | Actions with these outcomes |
| `--client <name>` | Actions of these clients, ignoring case |
| `--session <id>` | Actions of these sessions. A prefix of the id is enough |
| `--all` | Also the requests that are not tool calls |
| `--json` | One JSON object per action |
| `--dir` | Print the directory that holds the project's log files |
| `-f <Runefile>` | Read the log of this Runefile, as `rune mcp -f` serves it |

The list flags take comma-separated values and can be repeated. Values of one flag are
alternatives, and different flags must all match.

**What "today" means.** Stored times are UTC. `rune log` reads "today" and every day you
name in the display time zone, which is the zone of the machine it runs on unless `--tz`
names another. The first line of the listing names the zone and its offset. A call that
started at 23:30 UTC on the 16th is listed for the 17th in `Europe/Kyiv`, and for the 16th
with `--tz UTC`.

The command exits 0 when it listed actions or found none, 2 for a wrong flag or a missing
Runefile, and 1 when the log cannot be read. It runs no task and writes nothing.

`log` is a built-in command, so a Runefile task named `log` is run with `rune -- log`.
`rune log` prints a note when the Runefile has such a task. The task is still an MCP tool
named `log`.

### JSON output

`rune log --json` prints one object per action on standard output and nothing else there.
The times are UTC, whatever `--tz` says.

```json
{"schemaVersion":1,"type":"call","id":"b81e04f7a2c9d310","start":"2026-11-18T08:05:02.114Z","end":"2026-11-18T08:05:24.214Z","durationMs":22100,"outcome":"completed","exitCode":0,"session":"5f0e7a11c3d2b694","transport":"stdio","auth":"process","client":{"name":"claude-code","version":"2.1.0","reported":true},"tool":"apply","task":"apply","arguments":{"supplied":{"env":"prod"},"effective":{"env":"prod"}},"approval":{"asked":true,"decision":"approved","tasks":["apply"]},"plan":{"role":"applied","id":"7f3c9a1e52b84d06","digest":"sha256:9b1f..."},"sandbox":{"enforced":true,"backend":"bubblewrap","mode":"enforce"},"project":{"runefile":"/repo/Runefile","definitionDigest":"sha256:41aa...","configDigest":"sha256:77c0...","git":{"state":"commit","commit":"07afc00...","dirty":false}}}
```

| Field | Meaning |
|---|---|
| `schemaVersion` | `1` |
| `type` | `call`, `agent-run`, `request` or `auth-failure` |
| `id` | The invocation id: 16 hex digits |
| `start`, `end` | UTC times. `end` is absent for an incomplete action |
| `durationMs` | Whole milliseconds, measured on a monotonic clock |
| `outcome` | As in the table above. A request has `ok` or `error` |
| `exitCode` | Only when a task ran |
| `session` | The session id. It is derived by Rune and is not the protocol's own session id |
| `parent` | The id of the agent run a call was made in, or of the call an agent run was started in |
| `transport` | `stdio`, `http` or `agent-endpoint` |
| `auth` | `process` for stdio, `bearer-token` for HTTP |
| `client` | `name` and `version` as the client reported them, and `reported: true` |
| `tool`, `task` | The tool name the call asked for, and the task it names |
| `arguments.supplied` | The arguments as received |
| `arguments.effective` | The arguments the task ran with. A variadic parameter is an array |
| `error` | `kind` and `message`, when no task ran |
| `approval` | `asked`, `decision` and `tasks` |
| `grants` | The destructive tasks the operator's grant allowed for this call |
| `plan` | `role` (`stored` or `applied`), `id` and `digest` |
| `sandbox` | `enforced`; `backend`, when a sandbox was enforced; and `mode`, `enforce` or `off`. A record written before there was a sandbox has `enforced` only |
| `project` | `runefile`, `definitionDigest`, `configDigest` and `git` |
| `method` | The method of a request |
| `remote`, `suppressed` | The address of an authentication failure, and how many failures were left out before it |

The approval decisions are `approved`, `declined`, `dismissed`, `not-given`, `timed-out`,
`no-answer`, `cancelled`, `runefile-changed` and `cannot-ask`.

The contract is stable within `schemaVersion` 1: fields may be added, and none is removed
or given another meaning. New outcome, kind and decision values may appear, so a script
must not read an unknown value as success. A field with no value is left out.

### Where the log is stored

The log is outside the project, so a task that writes in its working directory does not
reach it by a relative path, and it cannot be committed or cleaned by accident.

| Order | Base directory |
|---|---|
| 1 | `$RUNE_AUDIT_DIR`, when set. It must be an absolute path |
| 2 | `$XDG_STATE_HOME/rune/audit`, when `XDG_STATE_HOME` is set to an absolute path. A relative one is ignored |
| 3 | `%LocalAppData%/rune/audit` on Windows |
| 4 | `~/.local/state/rune/audit` |

Each project has a directory of its own under the base, named by the first 16 hex digits
of the SHA-256 of its Runefile's resolved path. `rune log --dir` prints it. Inside are one
file per UTC day, `2026-11-18.jsonl`, and `project.json`, which names the Runefile. A
record is in the file of the day its call started on, so every day file is complete by
itself.

- **Modes.** Directories are created with mode `0700` and files with `0600`. On Windows,
  where file modes do not restrict access, the directory has the protection of your user
  profile.
- **A server refuses to start** when the log directory is inside the Runefile's directory
  or the tasks' working directory, is a symbolic link, or belongs to another user.
- **Retention.** A day file is removed 90 days after its day, when a server starts and when
  it first writes on a new day. `RUNE_AUDIT_RETENTION_DAYS` changes the period, and `0`
  keeps every file. A file is removed whole. No record is ever rewritten.
- **The variables are the server's.** They are read from the environment the server was
  started in, not from the Runefile, which an agent's tasks can edit. Give `rune log` the
  same `RUNE_AUDIT_DIR` to read the log you moved.

### When a record cannot be written

| What could not be written | What happens |
|---|---|
| The start of a tool call | The call is refused and nothing runs. The result is an error that names the log directory and `RUNE_AUDIT_DIR`, and the server prints the same on standard error |
| The end of a tool call | The agent gets the task's own result, with the same exit code and error flag, plus one text block that says the completion was not recorded. The server prints a warning. The task is not run again, and the call reads as `incomplete` |
| Any other record | The request is served. The server prints one warning until a write succeeds again |
| The run of an `(agent)` task | The agent runs without its endpoint, and Rune prints a warning |
| An old day file that could not be removed | The server prints a warning |

There is no switch that turns the log off. A host with a read-only home directory sets
`RUNE_AUDIT_DIR` to a directory the server can write.

### Secrets

- **Masked like task output.** Every argument name and value, error text and client name
  passes the [secret masking](how-to/secret-masking.md) of the Runefile the call ran
  against. A secret reads `***`.
- **Secret-named parameters are never stored.** A value whose name contains `token`,
  `secret`, `password`, `passwd`, `apikey`, `api_key`, `private_key`, `access_key`,
  `credential` or `auth`, ignoring case, is stored as `***` whatever it is. The rule errs
  on the side of hiding: a parameter named `author` is hidden too.
- **Bounded.** A value longer than 1 KiB is cut and ends with `[N bytes cut]`. A record
  lists at most 64 names of an object and 64 elements of an array, and counts the rest. A
  record is at most 64 KiB.
- **Never stored.** Bearer tokens, request headers, the protocol's session id, environment
  variables, task output, fix suggestions, plan and summary bytes, and the bodies of
  protocol messages.

### Parents and `RUNE_INVOCATION_ID`

The tasks of a call run with `RUNE_INVOCATION_ID` set to the call's invocation id. A task
can write it into its own logs to tie them to the record.

When a task of a call starts `rune` on an `(agent)` task, that agent run names the call as
its parent, and the calls its agent makes name the run. The variable is how the id crosses
the process boundary, so a task body can overwrite it: read a parent as attribution, like a
client name.

The plan journal of [Applying a reviewed plan](#retention-and-the-journal) carries the same
id in its `invocation` field, for every event a tool call caused.

### What the log proves, and how to keep it elsewhere

The log is operational evidence on your machine. Its files belong to your user, so a
process that runs as you can read and change them: an agent that has its own shell, or a
task that runs on a server started with `--sandbox off`. A task that runs in
[the sandbox](#the-sandbox) cannot reach the log. Rune does not sign the records. Treat
the log as a record of what happened, not as proof against a party that could edit it.

For retention you control, ship the records off the machine:

- Point `RUNE_AUDIT_DIR` at a directory your log shipper collects. The files are plain
  JSON lines and are only ever appended to.
- Or export on a schedule with `rune log --json --since 24h --all` and send the output to
  your storage.

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

The run of an `agent` task and every call its agent makes through that endpoint are in
[the audit log](#the-audit-log), with the run as the parent of the calls. When the run
cannot be recorded, the agent runs without the endpoint and Rune prints a warning.

The `[context]` task that prepares the prompt, and every task the agent calls through
that endpoint, run in [the sandbox](#the-sandbox) under the mode of `RUNE_SANDBOX`. A
value that is no mode stops the task before its agent starts. An `agent` task that an
agent calls through `rune mcp` runs its agent program inside the sandbox, and gets no
endpoint.

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
- [CLI reference](cli.md): `rune mcp` and `rune serve` flags, and `rune log`
- [Runefile language guide](runefile.md) — `[private]`, `[confirm]`, `[network]`, executors
- [Rune-only agent mode](design/rune-only-agent-mode.md): a design note on removing a
  client's shell, and what that does not prevent
- [Guides](how-to/README.md) — the rest of the capability deep dives
