# Feature Specification: Restrict the Argument Values an Agent May Pass to a Task

**Feature Branch**: `050-mcp-agent-value-policy`

**Created**: 2026-11-17

**Status**: Draft

**Input**: GitHub issue [#48](https://github.com/rune-task-runner/rune/issues/48)
(backlog MCP-14): "Enforce argument-value policies for agent task calls"

## Overview

Rune decides whether an agent may call a task from the task's name and from whether the task is
destructive. That is all or nothing per task: an agent that may call `deploy` may call it with
every value `deploy` accepts. A team that wants agents to deploy staging, and people to deploy
production, has to write two tasks or keep `deploy` away from agents.

This feature adds one task attribute, `[agent-allow(...)]`. It names parameters of the task and,
for each, the one value an agent may run the task with. The parameter's type still says what the
task accepts from anyone. The attribute says which of those values an agent may use.

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

# Deploy where the caller says.
promote target: (deploy target)
    @echo promoted
```

Served with `rune mcp`:

| Call | Today | After |
|---|---|---|
| Agent calls `deploy` with `env=staging` | Runs | Runs |
| Agent calls `deploy` with `env=prod` | Runs | Refused. Nothing runs |
| Agent calls `rollout` with `env=staging`, `region=us-east-1` | Runs | Refused. Nothing runs |
| Agent calls `rollout` with `env=staging` and no `region` | Runs | Runs: the default is `eu-west-1` |
| Agent calls `publish` with no `env` | Runs with `prod` | Refused: the default is `prod`. Rune does not swap in `staging` |
| Agent calls `publish` with `env=staging` | Runs | Runs |
| Agent calls `preview` | Runs | Runs |
| Agent calls `ship` | Runs `test`, then `deploy` with `prod` | Refused before `test` runs |
| Agent calls `promote` with `target=prod` | Runs `deploy` with `prod` | Refused. Nothing runs |
| Agent calls `deploy` with `evn=prod` | Refused: unknown argument | Unchanged |
| A person runs `rune deploy prod` | Runs | Unchanged |
| A person runs `rune ship` | Runs | Unchanged |

The refusal an agent gets for the second row is:

```text
task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"
```

The tool an agent sees for `deploy` lists `staging` as the only value of `env`, and its
description says so. The check does not depend on that: a client that ignores the tool's schema
and sends `prod` gets the refusal.

### What the attribute does and does not cover

It governs task runs that start from an agent through Rune: a tool call on `rune mcp` or
`rune serve`, on the endpoint an `agent` task opens for its own agent, and everything such a call
runs. That includes a task body that runs `rune` itself: Rune marks the environment of an agent
run, and a `rune` started inside it applies the policy too.

It does not stop an agent that has its own shell from running `rune deploy prod` there, or from
editing the Runefile. Closing that is the separate problem of issue #46 and spec 047. It also
does not follow a task body that clears the mark or runs another tool: the body is the author's.

### Out of scope

- Patterns of any kind: globs, regular expressions, lists of several allowed values, ranges,
  negation, or an expression language.
- An external policy service, a policy file outside the Runefile, or a new dependency.
- Predicates on `number`, `boolean`, `path` or `raw` parameters, and on variadic parameters.
- An operator flag that sets, widens or turns off a task's policy when the server starts.
- A policy for command-line runs by a person.
- Changes to which tasks are tools (spec 041), to tool hints (spec 033), to the destructive
  refusal (specs 031, 034) or to the approval request (spec 049), beyond the order in which a
  call meets them.
- Restricting an agent's own shell or its ability to edit the Runefile (issue #46).
- A static error or warning for a default, or a dependency argument, that the policy forbids.
  A person may run those from the command line, so they are valid Runefiles.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An agent may run a task with the allowed value and no other (Priority: P1)

A Runefile author marks `deploy` with `[agent-allow(env="staging")]`. An agent that calls `deploy`
with `env=staging` gets the deploy. An agent that calls it with `env=prod` gets an error that says
which value was refused and which value the policy requires, and nothing runs.

**Why this priority**: This is the feature. Without it the author has to choose between giving
agents production and giving them no deploy at all.

**Independent Test**: Serve the Overview Runefile, with bodies that write a marker file. Call
`deploy` with `env=staging`: the marker exists and the result is the task's. Call it with
`env=prod`: no marker exists and the result is an error with the refusal text.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile served over stdio, **When** an agent calls `deploy` with
   `env=staging`, **Then** `deploy` runs once and the agent receives its output and exit code.
2. **Given** the same server, **When** an agent calls `deploy` with `env=prod`, **Then** nothing
   runs and the result is an error whose text is
   `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"`.
3. **Given** the same Runefile served over Streamable HTTP, **When** the two calls are made,
   **Then** the results are the same as over stdio.
4. **Given** `rollout`, **When** an agent calls it with `env=staging` and `region=us-east-1`,
   **Then** nothing runs, and the refusal shows `region="us-east-1"` and the whole policy,
   `env="staging", region="eu-west-1"`.
5. **Given** `rollout`, **When** an agent calls it with `env=prod` and `region=us-east-1`,
   **Then** the refusal shows both refused values.
6. **Given** `rollout`, **When** an agent calls it with `env=staging` and `region=eu-west-1`,
   **Then** it runs.
7. **Given** `deploy`, **When** an agent calls it with `evn=prod`, **Then** the call is refused
   for the unknown argument, as today, and nothing runs.
8. **Given** a task with a policy on `env` and a second parameter `tag` the policy does not
   name, **When** an agent calls it with an allowed `env` and any valid `tag`, **Then** it runs.

---

### User Story 2 - An omitted argument is checked against the default the task would use (Priority: P1)

An agent calls `publish` and sends no `env`. The task would run with its default, `prod`. Rune
refuses the call. It does not run `publish` with `staging` in place of the default, because the
agent would then believe it had run the task as written. When the default is the allowed value,
the call runs.

**Why this priority**: A policy that looks only at what the agent sent is passed by sending
nothing.

**Independent Test**: Call `publish` with no arguments: no marker, and the refusal shows
`env="prod"`. Call `rollout` with `env=staging` only: it runs, and the body received `eu-west-1`.

**Acceptance Scenarios**:

1. **Given** `publish`, whose default for `env` is `prod`, **When** an agent calls it with no
   arguments, **Then** nothing runs and the refusal shows `env="prod"`.
2. **Given** `rollout`, whose default for `region` is the allowed value, **When** an agent calls
   it with `env=staging` only, **Then** it runs with `region` set to `eu-west-1`.
3. **Given** a default that is an expression, such as one that reads the environment, **When** an
   agent omits the argument, **Then** the policy is checked against the value the expression
   gives for this call.
4. **Given** a task with a default for `env` and a misspelled argument `evn=staging`, **When** an
   agent calls it, **Then** the call is refused for the unknown argument and the default is not
   used.
5. **Given** any refused call, **When** it returns, **Then** no task ran with a value the agent
   did not send and the task does not declare as its default.

---

### User Story 3 - A task that runs a restricted task is not a way around it (Priority: P1)

An agent calls `ship`, which runs `test` and then `(deploy "prod")`. Rune works out what the call
would run before it starts, finds that `deploy` would run with `prod`, and refuses. `test` does
not run. The same holds when the restricted task is reached through a `&&` hook, a `||` hook, a
task in a module, or a call made by the agent of an `agent` task. A call whose arguments for a
restricted task cannot be known before the run is refused with the reason.

**Why this priority**: If `ship` ran, the policy on `deploy` would hold only for agents that call
`deploy` by name.

**Independent Test**: Call `ship`: no marker of `test` or `deploy` exists, and the refusal names
`ship`, `deploy` and `env="prod"`. Call `preview`: the markers of `test`, `deploy` and `preview`
exist. Call `promote` with `target=prod`, then with `target=staging`: refused, then runs.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** an agent calls `ship`, **Then** nothing runs, `test`
   included, and the refusal names the called task `ship`, the restricted task `deploy`, the
   value `env="prod"` and the policy.
2. **Given** the same Runefile, **When** an agent calls `preview`, **Then** `test`, `deploy` with
   `staging`, and `preview` run.
3. **Given** `promote`, **When** an agent calls it with `target=prod`, **Then** nothing runs.
   With `target=staging`, `deploy` runs with `staging`.
4. **Given** a task whose `&&` hook is `(deploy "prod")`, **When** an agent calls it, **Then** it
   is refused before the task's own body runs.
5. **Given** a task whose `||` hook is `(deploy "prod")`, **When** an agent calls it, **Then** it
   is refused whether or not the task would fail.
6. **Given** a module `infra` whose task `deploy` carries the policy, **When** an agent calls the
   tool for `infra::deploy` with `prod`, or a root task that runs `(infra::deploy "prod")`,
   **Then** each is refused.
7. **Given** a person runs an `agent` task from the command line, **When** that task's agent
   calls `deploy` with `prod` on the endpoint the task opened, **Then** the call is refused.
8. **Given** a task that runs `deploy` with an argument whose value can differ between the check
   and the run, such as one built from the current time or from a file's existence, **When** an
   agent calls it, **Then** nothing runs, and the refusal says the value of `env` cannot be known
   before the run and names the policy.
9. **Given** a task that reaches a restricted task through an argument that fails to evaluate,
   **When** an agent calls it, **Then** nothing runs and the refusal says the arguments for the
   restricted task could not be worked out.
10. **Given** a task that reaches `(deploy "staging")` and `(deploy "prod")`, **When** an agent
    calls it, **Then** it is refused for the `prod` run.
11. **Given** a restricted task that is unavailable on this host, **When** an agent calls a task
    that would run it elsewhere, **Then** the call is not refused for it: the run skips that
    task, as it does today.
12. **Given** a task whose body runs `rune deploy prod`, **When** an agent calls it, **Then** the
    inner `rune` refuses to run `deploy` with `prod` and exits with an error, so the body fails.
    The same body run by a person from the command line deploys production.
13. **Given** a task that runs a restricted task with an allowed value and passes it a second
    argument built from a module variable that reads a file, **When** an agent calls it,
    **Then** the variable is read where the run reads it, as it is without the policy: checking
    the call does not fix the variable's value early.

---

### User Story 4 - Approval and operator settings cannot lift a refused value (Priority: P1)

`deploy` also has `[confirm]`. The operator started the server with `--allow-destructive deploy`,
or the person at the client would approve the call. An agent calls `deploy` with `prod`. The call
is refused for the value. The person is not asked about it. With `staging`, the call goes on to
the destructive check: it runs under the grant, or the person is asked.

**Why this priority**: The value policy is the author's rule about what agents may do. A grant or
an approval answers a different question, whether a destructive task may run, and must not be
read as an answer to this one.

**Independent Test**: Serve a Runefile where `deploy` has both attributes. With the grant, call
`deploy` with `prod`: refused for the value, no marker. With no grant and a client that approves
every request, call `deploy` with `prod`: refused for the value, and the client received no
request. Call with `staging`: one request, then the run.

**Acceptance Scenarios**:

1. **Given** `deploy` with `[confirm]` and the policy, and `--allow-destructive deploy`, **When**
   an agent calls `deploy` with `prod`, **Then** it is refused for the value and nothing runs.
2. **Given** the same Runefile, no grant, and a client that can be asked, **When** an agent calls
   `deploy` with `prod`, **Then** it is refused for the value and no approval request is sent.
3. **Given** the same server, **When** an agent calls `deploy` with `staging`, **Then** the
   person is asked, as spec 049 defines, and the call runs only on approval.
4. **Given** `--allow deploy` or any `--deny` list that leaves `deploy` a tool, **When** an agent
   calls `deploy` with `prod`, **Then** it is refused for the value.
5. **Given** a task with the policy that is private, or hidden by `--allow` or `--deny`, **When**
   a client lists tools, **Then** it is not a tool. The attribute exposes nothing.
6. **Given** a hidden or private task with the policy, **When** an exposed task would run it with
   a refused value, **Then** the call to the exposed task is refused.

---

### User Story 5 - A wrong policy is an error in the Runefile, not a silent gap (Priority: P2)

An author misspells a parameter name in `[agent-allow]`, names the same parameter twice, writes
the attribute twice, gives a value the parameter's enum does not have, or names a `number` or
variadic parameter. Each is a static error with a code and a position, before anything runs, on
the command line and in the editor. Rune never drops or reinterprets a policy it cannot apply.

**Why this priority**: A policy that is silently ignored leaves production open while the
Runefile reads as if it were closed.

**Independent Test**: For each invalid declaration, run the static check. It fails with the
documented code at the offending text, and no task runs.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** it is checked, **Then** it has no error.
2. **Given** `[agent-allow(evn="staging")]` on a task with no parameter `evn`, **When** the
   Runefile is checked, **Then** the result is error `RUNE2019` at the name, saying the task has
   no such parameter.
3. **Given** `[agent-allow(env="staging", env="prod")]`, **When** it is checked, **Then** the
   result is `RUNE2019` at the second `env`.
4. **Given** two `[agent-allow]` attributes on one task, **When** it is checked, **Then** the
   result is `RUNE2019` at the second, pointing to the first.
5. **Given** `[agent-allow(env="qa")]` where `env` is `enum("staging","prod")`, **When** it is
   checked, **Then** the result is `RUNE2019` at the value, naming the values `env` accepts.
6. **Given** a predicate on a `number`, `boolean`, `path` or `raw` parameter, or on a variadic
   parameter, **When** it is checked, **Then** the result is `RUNE2019` saying which kinds of
   parameter a policy can name.
7. **Given** `[agent-allow]` with no parentheses, with empty parentheses, with a name and no
   value, or with a value that is not a string literal, **When** it is parsed, **Then** it is an
   error at that position.
8. **Given** any Runefile with one of these errors, **When** `rune mcp` is started on it,
   **Then** the server does not start, as for any static error.

---

### User Story 6 - The tool tells the agent what it may send (Priority: P2)

An agent lists tools. The tool for `deploy` gives `staging` as the only value of `env`, and its
description says agents may call it only with `env="staging"`. The agent can choose a call that
will be accepted without trying `prod` first.

**Why this priority**: A refusal is correct but costs a round trip. The narrowed tool prevents
most of them.

**Independent Test**: List tools for the Overview Runefile. The schema of `deploy` gives the one
value for `env`, and the description holds the policy. The check of `prod` on the command line
still accepts it.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** a client lists tools, **Then** the `env` property of
   `deploy` allows only `staging`.
2. **Given** a policy on an unannotated or `string` parameter, **When** a client lists tools,
   **Then** that property allows only the policy's value.
3. **Given** `rollout`, **When** a client lists tools, **Then** the description names both
   predicates.
4. **Given** a task with no policy, **When** a client lists tools, **Then** its tool is the same,
   byte for byte, as before this feature.
5. **Given** a client that ignores the schema, **When** it sends `env=prod` to `deploy`, **Then**
   it is refused.
6. **Given** a running server, **When** the author changes or removes the policy and the server
   reloads the Runefile, **Then** the tool list changes with it and later calls follow the new
   policy.

---

### User Story 7 - People, and tasks without the attribute, see no change (Priority: P1)

A person runs `rune deploy prod`, `rune ship` and `rune publish` at a terminal. Each behaves as it
did, confirmation prompts included. An agent calls a task that has no policy and reaches none:
the call behaves as it did.

**Why this priority**: The feature narrows what agents may do. It must not reach anyone else.

**Independent Test**: Run the three commands on the Overview Runefile from the command line: each
runs with `prod`. Serve a Runefile with no `[agent-allow]`: every result and every tool is
identical to the same server built before the feature.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** a person runs `rune deploy prod`, **Then** `deploy`
   runs with `prod`.
2. **Given** the same Runefile, **When** a person runs `rune ship` and `rune publish`, **Then**
   each runs `deploy`, or itself, with `prod`.
3. **Given** `deploy` with `[confirm]` and the policy, **When** a person runs `rune deploy prod`,
   **Then** they are asked to confirm as today, and `--yes` works as today.
4. **Given** a Runefile with no `[agent-allow]`, **When** it is served, **Then** every tool, every
   result and every refusal is the same as before this feature.

---

### User Story 8 - The documentation says what the policy covers and where it stops (Priority: P2)

A reader of the Runefile reference finds the attribute, its syntax, which parameters it can name
and the errors for a wrong one. A reader of the MCP guide finds how a call is checked, in what
order relative to the destructive refusal and the approval request, how defaults and dependencies
are treated, and that an agent with its own shell is not covered.

**Why this priority**: The attribute is a security control. An author who misreads its limits
would trust it for more than it does.

**Independent Test**: Read the attribute's entry in the Runefile reference, the grammar, the
diagnostics table and the policy section of the MCP guide. Each states the behavior above.

**Acceptance Scenarios**:

1. **Given** the Runefile reference, **When** a reader looks up `[agent-allow]`, **Then** it gives
   the syntax, the supported parameters, that predicates are combined with "and", and the code of
   the static error.
2. **Given** the MCP guide, **When** a reader reads the policy section, **Then** it gives the
   refusal, the handling of defaults, of dependencies and hooks, the order of checks, and the
   limits.
3. **Given** the diagnostics reference and the grammar, **When** a reader looks up the code or
   the attribute, **Then** each is there.

---

### Edge Cases

- **A call that is refused as destructive.** A call that can run a destructive task the operator
  has not granted, from a client that cannot be asked, is refused as today, before its arguments
  are read. The value policy is checked on calls that get past that point.
- **Arguments are wrong.** An unknown argument name, a value that breaks the parameter's type,
  or a missing required argument is answered with that error, as today. The policy is checked
  only on a call whose arguments are valid.
- **An empty value.** A policy may require the empty string. For a required parameter that can
  never be met, because an empty required argument is a missing one.
- **A value with characters that are not printable.** It is a static error. The formatter
  could not write such a value back unchanged, and a policy must not change when a file is
  formatted.
- **A default the policy forbids.** When the default is a literal other than the allowed value,
  the tool marks the parameter as required, since an agent has to send it. The call without it
  is still answered with the policy refusal.
- **An argument on the way to a restricted task that cannot be worked out yet.** A dependency
  argument that reads a file an earlier task of the same run creates fails before the run. The
  call is refused, whichever parameter the argument is for, because Rune cannot bind the
  restricted task without it. The author can move that read into the task's body.
- **A task body that runs `rune`.** The inner run is an agent run and is checked when it
  starts. What the outer task ran before that point has run: Rune cannot see into a body before
  it runs.
- **The same restricted task reached twice.** Each run of it with different arguments is checked
  on its own.
- **Several refusals in one call.** The refusal reports the first restricted run that breaks its
  policy, in the order a run would reach them, the called task first.
- **A secret as a value.** A refused value that holds a secret from the server's environment is
  shown masked, as task output is.
- **A very long or multi-line value.** It is shown on one line, with characters that are not
  printable escaped, and cut to a fixed length with a mark.
- **The `[context]` hook.** Rune runs it on the agent surface. A restricted task it reaches is
  checked like any other, and a refusal ends the hook the way any failure of the hook does.
- **A failure hook that reaches a restricted task.** It is checked before the call starts, since
  a hook may run. A hook's own failure hooks never run, so they are not counted.
- **A task unavailable on this host.** It is not counted, and neither is what it would run.
- **A Runefile reload during a call.** The call is checked against the Runefile load it started
  with, as its run is (spec 043).
- **An embedded server.** A program that embeds the server library can state a parameter's
  allowed value for the tool it advertises. Refusing a call is the work of the program's engine.

## Requirements *(mandatory)*

### Functional Requirements

**The attribute**

- **FR-001**: Runefiles MUST accept a task attribute `[agent-allow(name="value", ...)]` holding
  one or more predicates, each a parameter name, `=`, and a string literal. It MAY share a
  bracket list with other attributes.
- **FR-002**: A predicate MUST name a parameter of the same task that is unannotated, `string`
  or `enum`, and not variadic. The value of a predicate on an `enum` parameter MUST be one of the
  enum's values.
- **FR-003**: Each of the following MUST be a static error with code `RUNE2019`, a position, and
  a message that names the fix: a name that is not a parameter of the task; a parameter named
  twice in one attribute; a second `[agent-allow]` on one task; a value an `enum` parameter does
  not accept; a parameter of kind `number`, `boolean`, `path` or `raw`; a variadic parameter; a
  value that holds a character that is not printable.
- **FR-004**: An attribute with no predicate, and a predicate that is not `name="value"`, MUST be
  a syntax error at its position.
- **FR-005**: Rune MUST NOT ignore, widen, narrow or coerce a policy it reports as invalid. A
  Runefile with an invalid policy runs nothing and is not served.
- **FR-006**: The attribute MUST be known to every part of the language surface: the grammar
  reference, the formatter, the syntax tree dump, editor completion and hover, and editor syntax
  highlighting.

**What a policy means**

- **FR-007**: A run of a task with a policy satisfies it when, for every predicate, the value the
  task would run with for that parameter is exactly the predicate's value. Comparison is on the
  whole string, case-sensitive, with no trimming.
- **FR-008**: Predicates of one attribute MUST all hold. A parameter the attribute does not name
  keeps its existing validation and no other limit.
- **FR-009**: The value checked MUST be the effective one: the argument that was sent, or, when
  none was sent, the value the task's default gives for this call.
- **FR-010**: Rune MUST NOT run a task with a value other than the one the call or the task's
  default gives. It MUST NOT replace a refused value with an allowed one.
- **FR-011**: A task with no `[agent-allow]` MUST behave as it does today for every caller.

**Where a policy applies**

- **FR-012**: The policy MUST apply to every task run that starts from an agent: a tool call on
  `rune mcp` and `rune serve` over stdio and Streamable HTTP, a tool call on the endpoint an
  `agent` task opens for its own agent, the `[context]` hook run for an agent, and every
  dependency, `&&` hook and `||` hook such a run reaches, in a module or not.
- **FR-012a**: Rune MUST mark the environment of every task body it runs in an agent run, and a
  `rune` command started with that mark MUST treat its own run as an agent run: it checks the
  tasks it was asked to run and everything they reach before it starts, and exits with an error
  and the refusal text when a policy is broken.
- **FR-013**: The policy MUST NOT apply to a run a person starts from the command line, whatever
  it reaches.
- **FR-014**: Each restricted task MUST be checked against its own effective arguments, wherever
  in a run it is reached.
- **FR-015**: The attribute MUST NOT make a task a tool, and MUST NOT change the effect of
  `[private]`, `--allow`, `--deny`, `--allow-destructive` or the approval request on whether a
  task is listed, granted or asked about.

**When a call is checked**

- **FR-016**: Before a call runs any task body, dependency or hook, Rune MUST have: rejected
  unknown argument names; validated the supplied values; bound the supplied values and the
  defaults; and checked the policy of the called task and of every restricted task the call can
  reach.
- **FR-017**: The tasks a call can reach MUST be the ones a run can reach through dependencies,
  `&&` hooks and `||` hooks, with the arguments the Runefile passes along each, skipping tasks
  unavailable on this host. A `||` hook counts whether or not its task would fail.
- **FR-018**: When a restricted task would be reached with a value for a predicate's parameter
  that can differ between the check and the run, Rune MUST refuse the call, run nothing, and say
  which task and parameter it could not check and what the policy requires. When the arguments
  of a dependency or hook on the way to a restricted task cannot be worked out before the run,
  Rune MUST refuse the call, run nothing, and say which arguments and why.
- **FR-018a**: Checking a call MUST NOT change what an allowed call then does. In particular it
  MUST NOT fix the value of a module variable earlier than the run would read it.
- **FR-019**: A refused call MUST run nothing: no task body, no dependency, no hook, no failure
  hook, no approval request.
- **FR-020**: The policy MUST also be enforced at the point where a restricted task's body would
  start in an agent run, so that a restricted task never runs with a refused value even if the
  earlier check missed it.
- **FR-021**: The value policy and the destructive check MUST be independent. A grant with
  `--allow-destructive` and a person's approval MUST NOT allow a refused value. A value the policy
  allows MUST still meet the destructive check. The person MUST NOT be asked to approve a call
  the policy refuses.
- **FR-022**: A call refused as destructive before its arguments are read, as today, MUST keep
  that refusal and its text.

**The refusal**

- **FR-023**: A refused call MUST return an error result with no exit code and no task output.
  For the called task the text MUST be
  `task "<task>" is not allowed for agents with <name>="<value>"[, ...]; agent policy requires <name>="<value>"[, ...]`,
  listing each parameter whose value was refused with that value, and then every predicate of
  the policy.
- **FR-024**: When the refused run is of a task the call reaches, the text MUST name the called
  task and the restricted task, with the same values and policy.
- **FR-025**: Every value in a refusal MUST be masked with the same secret set as task output,
  shown on one line with characters that are not printable escaped, and cut to a fixed length
  with a visible mark.
- **FR-026**: The same call on the same Runefile MUST get the same refusal text.

**What the agent sees**

- **FR-027**: For each parameter a policy names, the tool's input schema MUST allow only the
  policy's value. The validation of that parameter for other callers MUST NOT change. A
  parameter whose default is a literal other than the policy's value MUST be listed as required
  in the tool's schema.
- **FR-028**: The tool's description MUST state the policy: each predicate's parameter and value.
- **FR-029**: A tool whose task has no policy MUST be the same as before this feature.
- **FR-030**: The check MUST NOT depend on the client honoring the schema.
- **FR-031**: After a Runefile reload, the tool list and the checks MUST follow the new policy.

**Documentation and tests**

- **FR-032**: The Runefile reference MUST document the attribute's syntax, supported parameters,
  "and" semantics and static error. The grammar reference MUST include it. The diagnostics
  reference MUST document `RUNE2019`.
- **FR-033**: The MCP guide MUST document the refusal, the handling of defaults, dependencies,
  hooks, modules and `agent` tasks, the order of checks relative to the destructive refusal and
  the approval request, the narrowed tool, and the limits.
- **FR-034**: Automated tests MUST cover each acceptance criterion of the issue through a real
  client session, on stdio and on Streamable HTTP for every row of the Overview table and for
  hooks, modules, grants and approval, and MUST show by marker files that a refused call ran
  nothing. The volatile and unresolved refusals, which do not depend on the transport, MAY be
  covered on one.

### Key Entities

- **Agent value policy**: The `[agent-allow]` attribute of one task: a set of predicates that
  must all hold for an agent run of that task.
- **Predicate**: One parameter name and the one value an agent run may have for it.
- **Restricted task**: A task that has a policy.
- **Effective arguments**: The values a task would run with: what was sent or passed, and the
  defaults for the rest.
- **Agent run**: A task run that starts from an agent's tool call, and everything it reaches.
- **Reach of a call**: The tasks a call can run through dependencies and hooks, each with the
  arguments the Runefile passes to it.
- **Policy refusal**: The error an agent gets for a call that would run a restricted task
  against its policy.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every refused call in the acceptance scenarios, zero marker files exist after
  the call, including those of dependencies that come before the restricted task.
- **SC-002**: For every allowed call in the acceptance scenarios, each task of the call ran
  exactly once with the values the call and the defaults gave.
- **SC-003**: Every agent row of the Overview table gives the stated result on stdio and on
  Streamable HTTP, and every command-line row gives the stated result.
- **SC-004**: Every invalid declaration in User Story 5 fails the static check with the
  documented code, and runs nothing.
- **SC-005**: On a Runefile with no `[agent-allow]`, the tool list and every call result are
  byte for byte what they were before the feature.
- **SC-006**: In every refusal of the secret scenario, the number of occurrences of the secret
  value is zero.
- **SC-007**: An author can let agents deploy staging and keep production for people with one
  attribute on one task, with no second task and no server flag.
- **SC-008**: An agent that reads the tool list can make an accepted call to a restricted task on
  its first attempt: the schema names the allowed value.

## Assumptions

- **The attribute is enforced by default and has no switch.** The issue describes the policy as
  the author's and names no operator flag. A task without the attribute is unchanged, so no
  existing Runefile changes behavior.
- **One code covers every invalid declaration.** `RUNE2019` is the next free semantic code. One
  code with distinct messages follows `RUNE2015` and `RUNE2018`. Malformed syntax is a parse
  error, as for other attributes.
- **Unannotated parameters count as strings.** A parameter with no type accepts any string, so a
  policy can name it.
- **A forbidden default or dependency argument is not a static error.** The issue lists a default
  that resolves to `prod` as a refusal at call time. A person can still run the task, so the
  Runefile is valid.
- **The destructive refusal keeps its place.** A call from a client that cannot be asked is
  refused as destructive before its arguments are read (specs 031 and 049, with pinned texts).
  The value check comes after it and before the approval request. In either order nothing runs.
- **The `[context]` hook is an agent run.** Rune starts it, but only to serve an agent, and the
  safe reading is that a restricted task must not run with a refused value because an agent
  connected.
- **A call is refused for the first breach found.** Listing every breach would add text and no
  control.
- **Values that can change during a run.** Arguments built from the time, a random value, or the
  state of files can differ between the check and the run. Rune refuses those for a restricted
  parameter and does not guess. The plan fixes the list.
- **The mark on an agent run is for the author's bodies, not against the agent.** A task body is
  written by the Runefile's author, and the agent supplies only arguments. So a mark in the
  body's environment is enough to carry the agent run into a `rune` the body starts. The agent
  program an `agent` task starts has its own shell and could clear the mark, so nothing is
  promised for it: that is the problem of issue #46. It inherits the mark when the `agent` task
  itself runs in an agent run, and has none when a person runs the task.
- **Resolving arguments before a run has no side effect.** Defaults and dependency arguments are
  expressions, and Rune's expression language runs no command (Constitution III).
- **The schema narrows, the engine enforces.** The embeddable server library advertises the
  narrowed tool. Enforcement needs the bound defaults and the secret set, which the engine has,
  so the engine refuses. An embedding program's engine has to do the same.
- **Constitution Principle VII needs no amendment.** It says destructive tasks are refused unless
  granted or approved, and that secrets never reach descriptions or schemas. A policy value is a
  Runefile literal, like an enum value, and the feature only adds refusals.
- Rune is before 1.0. A new optional field on the server library's parameter descriptor does not
  break an embedder that sets fields by name.
