# Feature Specification: Approve a Destructive Call Inside the Agent Client

**Feature Branch**: `049-mcp-confirm-elicitation`

**Created**: 2026-11-17

**Status**: Draft

**Input**: GitHub issue [#47](https://github.com/rune-task-runner/rune/issues/47)
(backlog MCP-13): "Approve [confirm] tasks inside the agent client with MCP elicitation"

## Overview

An agent calls a `[confirm]` task through Rune's MCP server. Unless the operator named that task in
`--allow-destructive` when the server started, Rune refuses the call. The person has to leave the
agent conversation, open a terminal and run the task there, or restart the server with a wider grant
that then covers every later call too.

MCP has a way for a server to ask the person a question through the client, called elicitation. This
feature uses it. When a call can run a destructive task the operator has not allowed, Rune asks the
person, in their client, to approve that one call. The request shows the task, its arguments, and the
confirmation message the Runefile author wrote. If the person approves, the call runs once. Anything
else runs nothing.

```rune
# Builds the artifacts.
build:
    ./build.sh

# Applies the plan to an environment.
[confirm("Apply to prod? 3 resources change.")]
apply env:
    ./apply.sh {{env}}

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    rm -rf dist

# Cuts a release.
release version: build clean
    ./release.sh {{version}}

# Publishes, then cleans.
publish: && clean
    ./publish.sh

# Ships after a private scrub.
ship: _scrub
    ./ship.sh

[private]
[confirm("Scrub?")]
_scrub:
    ./scrub.sh
```

Served with `rune mcp` and no `--allow-destructive`:

| Call | Client | Today | After |
|---|---|---|---|
| `apply` with `env=prod` | can show a form | Refused | The person is asked. Approved: `apply` runs once. Otherwise nothing runs |
| `apply` again | can show a form | Refused | The person is asked again |
| `release` | can show a form | Refused, naming `clean` | One request names `clean` and its message. Nothing runs before the answer, `build` included |
| `ship` | can show a form | Refused, naming `_scrub` | One request names `_scrub`. Approved: `_scrub` and `ship` run |
| `apply` with a misspelled argument | can show a form | Refused as destructive | The argument error. The person is not asked |
| `apply` | cannot show a form | Refused | Refused. The text also says the operator can run `rune apply` themselves, and why Rune could not ask |
| `build` | any | Runs | Unchanged. Nobody is asked |

With `rune mcp --allow-destructive clean`, `release` and `clean` run without a request, as today. The
operator's grant and the person's approval are separate: a grant is a standing policy for the
server's lifetime, an approval is one answer for one call.

### What approval does and does not prove

Rune sends the request to the client and trusts the client to show it to a person and to send back
that person's answer. Rune cannot check that a person clicked. A client that answers such requests
by itself approves on the person's behalf. This feature is as strong as the client the operator
chose to connect, and the documentation says so. An operator who wants a task never to run from an
agent session uses `--deny` on that task and on every exposed task that can run it. Hiding only the
task itself is not enough: a task that reaches it is still a tool, and its request names the hidden
task.

### Out of scope

- A `--yes` flag, or any switch that approves every destructive task, on `rune mcp` or `rune serve`.
- Changes to `--allow-destructive`. It keeps its meaning (spec 031), and a granted task is not asked
  about.
- An operator flag that turns the request off or changes how long Rune waits for an answer.
- Asking through a web page (URL elicitation), a terminal, a desktop notification, or any channel
  other than the client's form request.
- Asking on the endpoint an `agent` task opens for its own agent. That endpoint keeps refusing every
  destructive task (spec 031 FR-015).
- Working out what a task will change. Rune shows the author's `[confirm]` text and the arguments. It
  does not run the task, or anything else, to describe its effect.
- The protocol revision that carries elicitation as a retried tool call (2026-07-28). Rune's MCP
  library does not negotiate it. See FR-024 and Assumptions.
- Remembering an approval: "always allow", per-session approval, or approval of a later call.
- CLI runs. `rune apply` at a terminal prompts as spec 027 defined.
- Which tasks are tools, and their hints (specs 033, 041).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A person approves one destructive call without leaving the client (Priority: P1)

An agent calls `apply` with `env=prod`. The person's client shows a request from Rune that names the
task `apply`, shows `env` is `prod`, and shows the author's message "Apply to prod? 3 resources
change." The person approves. `apply` runs once and the agent gets its result. When the agent calls
`apply` again, the person is asked again.

**Why this priority**: This is the feature. Today the person has to leave the conversation or give
the agent a standing grant.

**Independent Test**: Serve the Overview Runefile with no grant to a test client that can show a form
and approves. Call `apply` with `env=prod`. The client received one request before any marker file
existed, the request held the task name, the argument and the message, and after the call the marker
of `apply` exists once. Call again: a second request arrives.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, no grant, and a client that can show a form, **When** an agent
   calls `apply` with `env=prod`, **Then** the client receives one approval request before any task
   body runs, and the request shows the task name `apply`, the argument `env` with the value `prod`,
   and the text `Apply to prod? 3 resources change.`
2. **Given** that request, **When** the person approves it, **Then** `apply` runs exactly once and
   the agent receives the task's normal result: its output and its exit code.
3. **Given** an approved call that has finished, **When** the agent calls `apply` again with the
   same arguments, **Then** the person is asked again, and nothing runs until they answer.
4. **Given** a `[confirm]` task with no message text, **When** it is called, **Then** the request
   shows the same default question the command line asks for that task.
5. **Given** the same server over Streamable HTTP and over stdio, **When** the call is made on
   either, **Then** the request, the approval and the run behave the same.
6. **Given** a task with a defaulted parameter the agent did not send, **When** the person is asked,
   **Then** the request shows the value the task will run with.
7. **Given** the operator granted `apply` with `--allow-destructive apply`, **When** an agent calls
   `apply`, **Then** it runs with no request, as today.

---

### User Story 2 - Anything but an explicit yes runs nothing (Priority: P1)

The person declines the request, closes it, leaves the approval unticked, or does not answer. The
client crashes, or answers with something that is not an approval. In every case nothing runs and
the agent gets an error that says the call was not approved and why. Rune does not try another way
to get an answer.

**Why this priority**: A confirmation that can be passed by accident is worse than a refusal. Every
path that is not a clear approval has to end with no side effect.

**Independent Test**: For each kind of non-approval, call `release` on a server with no grant. After
the call no marker file exists, `build` included, and the result is an error naming the reason.

**Acceptance Scenarios**:

1. **Given** a pending request, **When** the person declines it, **Then** nothing runs and the
   result is an error saying the call was declined.
2. **Given** a pending request, **When** the person dismisses it without choosing, **Then** nothing
   runs and the result is an error saying the request was dismissed.
3. **Given** a pending request, **When** the client accepts it with the approval set to false,
   **Then** nothing runs and the result is an error saying approval was not given.
4. **Given** a pending request, **When** the client answers with an action Rune does not know, or
   accepts with no approval value or one that is not a boolean, **Then** nothing runs and the result
   is an error saying the client returned no valid answer.
5. **Given** a pending request, **When** no answer arrives within the time Rune waits, **Then**
   nothing runs, the result is an error saying so, and an approval that arrives later runs nothing.
6. **Given** a pending request, **When** the client disconnects or cancels the call, **Then**
   nothing runs.
7. **Given** a pending request, **When** the client reports an error for it, **Then** nothing runs
   and the result is the same error as for an answer that is not valid. Rune cannot tell the two
   apart reliably, and the agent can do nothing different with them.
8. **Given** any of the above, **When** the call has returned, **Then** Rune has not asked through
   any other channel and has not run a shell command or another task in its place.
9. **Given** a request that was declined, dismissed, answered without approval, got no answer in
   time, or got no valid answer, **When** the call returns, **Then** the result also says what the
   operator can do: add the named tasks to `--allow-destructive`, or run `rune <task>` themselves.
   A client run with nobody at it answers by itself, and its operator has to learn how to
   proceed.

---

### User Story 3 - One request covers everything destructive the call can run (Priority: P1)

An agent calls `release`, which runs `build` and then the destructive `clean`. Rune asks before
`build` runs. The request names `clean` with its own message, so the person knows what the approval
covers. Approval covers exactly the tasks the request named, for this call. A destructive task the
request did not name is still declined if a run reaches it.

**Why this priority**: Spec 034 made a call that reaches a destructive task do nothing until every
such task is allowed. Approval has to keep that: no earlier work may run while the person is still
deciding, and no destructive task may run because a different one was approved.

**Independent Test**: Call `release` with no grant. The request names `clean` and "Delete dist?"
before any marker exists. Decline: no marker. Approve: the markers of `build`, `clean` and `release`
exist.

**Acceptance Scenarios**:

1. **Given** no grant, **When** an agent calls `release`, **Then** one request names the called
   task `release` and the destructive task `clean` with the message `Delete dist?`, and neither
   `build` nor `clean` nor `release` has run when it arrives.
2. **Given** that request, **When** the person declines, **Then** `build` did not run.
3. **Given** `publish`, whose `&&` hook `clean` is destructive, **When** it is called, **Then** the
   person is asked before `publish` runs, and the request names `clean`.
4. **Given** a task whose `||` failure hook is destructive, **When** it is called, **Then** the
   person is asked before it runs, and the request names the hook's task, whether or not the task
   would fail.
5. **Given** a destructive task `reset` that depends on a destructive task `wipe`, **When** `reset`
   is called, **Then** one request names both, `reset` first, each with its own message.
6. **Given** `ship`, which depends on the private destructive task `_scrub`, **When** it is called
   and approved, **Then** the request named `_scrub`, `_scrub` and `ship` ran, and `_scrub` is still
   not a tool.
7. **Given** `--allow-destructive clean` and a task that can run `clean` and the ungranted `wipe`,
   **When** it is called, **Then** the request names only `wipe`. A granted task is never listed.
8. **Given** an approved call, **When** it finishes, **Then** a later call to any task named in that
   request is asked about again.

---

### User Story 4 - A client that cannot ask gets a refusal it can act on (Priority: P2)

A client that states no support for elicitation, or support only for the web-page kind, calls
`apply`. Rune does not send it a request it cannot show. The call is refused as today, and the text
adds that the person can run `rune apply` themselves and that Rune could not ask because the client
has no form support.

**Why this priority**: Clients in this state must lose nothing, and the refusal should name the
way out that needs no restart. It is not the common case among the clients recorded so far: Claude
Code and Codex CLI both state form support, also when run without a display, so a headless run of
either is asked and answers by itself: recorded, Claude Code dismisses the request and Codex
declines it. User Story 2 covers that: the result says nothing ran and what the operator can do.

**Independent Test**: Connect a test client that states no elicitation support, then one that states
URL support only. Each calls `apply`. No request is sent, nothing runs, and the error names
`rune apply` and `--allow-destructive`.

**Acceptance Scenarios**:

1. **Given** a client that states no elicitation support, **When** it calls `apply`, **Then** Rune
   sends no approval request, nothing runs, and the result is an error that names the task, says to
   add `apply` to `--allow-destructive` or that the operator can run `rune apply` themselves, and says the client cannot be
   asked.
2. **Given** a client that states only URL elicitation, **When** it calls `apply`, **Then** the
   result is the same.
3. **Given** a client that states elicitation with no mode, **When** it calls `apply`, **Then** it
   is asked. The protocol defines that statement as form support.
4. **Given** a client that cannot be asked and a call that reaches `clean` through `release`,
   **When** it calls `release`, **Then** the error names `clean` for the flag and `rune release` as
   the command to run.
5. **Given** the endpoint an `agent` task opens for its own agent, **When** that agent calls a
   destructive task, **Then** no request is sent whatever the agent's client states, and the refusal
   is the one that endpoint gives today.

---

### User Story 5 - An approval is good for one call and nothing else (Priority: P1)

Two agents, or one agent twice, call destructive tasks at the same time. Each call gets its own
request, and approving one leaves the other waiting for its own answer. An approval is tied to the
call that asked: its session, its arguments, and the Runefile as it was when the person was asked. A
call sent again, with the same or different arguments, starts over with a new request. If the
Runefile is reloaded while a request is pending, the approval the person gives is not used.

**Why this priority**: The value of asking per call is lost if one yes can be spent twice or spent on
something the person did not see.

**Independent Test**: Start two calls to `apply` with different arguments. Approve the first request
and decline the second. Exactly one marker exists, for the first call's arguments. Separately, hold
a request open, save a change to the Runefile, wait for the reload, then approve: nothing runs.

**Acceptance Scenarios**:

1. **Given** two calls in progress on one session, **When** the person approves one request and
   declines the other, **Then** only the approved call runs.
2. **Given** two sessions on one server over HTTP, **When** each calls `apply`, **Then** each
   session receives its own request and no session receives the other's.
3. **Given** a call that was approved and has run, **When** the same call is sent again with the
   same request content, **Then** the person is asked again and nothing runs before the answer.
4. **Given** a call that was approved with `env=staging`, **When** a call follows with `env=prod`,
   **Then** the person is asked again, and the new request shows `prod`.
5. **Given** a pending request, **When** the Runefile changes and the server reloads it before the
   person approves, **Then** the approval runs nothing, and the result is an error saying the
   Runefile changed and the call must be made again.
6. **Given** a call whose arguments include a field that claims approval, **When** the task declares
   no such parameter, **Then** the call is refused for the unknown argument, as today, and the person
   is not asked.

---

### User Story 6 - The request shows no secret and cannot be forged by an argument (Priority: P2)

An agent passes an argument whose value is a secret from the environment, or a value with line
breaks made to look like part of Rune's request. The request shows the secret masked, as task output
is. The line breaks are shown as visible escapes, so the value stays on its line. An argument value
is never shortened: the task would still run with all of it. A call whose request would be too
large is refused. Only the author's `[confirm]` message, which describes the task and is not run,
may be cut, with a visible mark.

**Why this priority**: The request is shown to a person who decides on what it says, and it passes
through the client, which may log it.

**Independent Test**: Serve a Runefile whose environment holds a secret. Call a destructive task with
that secret as an argument, and with an argument that holds a line break. The request received by
the client holds no secret value and no raw line break inside a value. Then call it with a
10,000-character value: no request is sent, and the call is refused.

**Acceptance Scenarios**:

1. **Given** a secret value in the server's environment, **When** a call passes it as an argument,
   **Then** the request shows it masked the way task output is masked, and no result or error for
   that call holds the value.
2. **Given** an argument value with line breaks or other control characters, **When** the person is
   asked, **Then** the value is shown on one line with the characters escaped.
3. **Given** a `[confirm]` message longer than the limit, **When** the person is asked, **Then**
   it is cut and marked as cut. An argument value is shown whole, whatever its length, as long as
   the request fits.
4. **Given** a call whose request would be over the size limit, **When** it is called, **Then**
   the person is not asked and the call is refused. A destructive task is never approved without
   being named, and an argument is never left out.

---

### User Story 7 - The documentation says who can approve and what happens when they cannot (Priority: P2)

A reader of the MCP guide learns that a destructive call can be approved in the client, what the
request shows, what an approval covers, what happens in each case that is not an approval, and that
the answer is only as trustworthy as the client. The client compatibility table says, per client and
version, whether it states form support and what was seen or read about how it shows the request.
The test contract of spec 031 describes the request and the fallback.

**Why this priority**: The security model changes from "refused unless the operator restarts" to
"asked, where the client can ask". An operator has to be able to find that out before connecting a
client that answers by itself.

**Independent Test**: Read the security model, the new section on approval and the compatibility
table in the MCP guide, and the spec 031 contract. Each states the behavior above, and every
compatibility cell names its evidence.

**Acceptance Scenarios**:

1. **Given** the MCP guide, **When** a reader reads the security model, **Then** it says a
   destructive call is refused unless the operator granted it or the person approves that call in
   the client, and that Rune trusts the client for the answer.
2. **Given** the guide, **When** a reader looks for what happens with a client that cannot ask,
   **Then** it gives the refusal and the two ways out.
3. **Given** the compatibility table, **When** a reader looks up a client, **Then** it gives the
   version, the elicitation modes that version states, and how it shows and answers the request,
   each with its evidence, and "unknown" or "not tested" where there is none.
4. **Given** the spec 031 contract for tool calls, **When** a reader reads it, **Then** it describes
   the approval request and the fallback beside the grant.

---

### Edge Cases

- **Arguments are wrong.** A call with an unknown argument name, a value that breaks a declared
  constraint, or a missing required argument is answered with that error. The person is not asked
  about a call that could not run. With a client that cannot be asked, the refusal still comes
  first, as it does today.
- **Task hidden or private.** A task that `--deny`, `--allow` or `[private]` keeps from being a tool
  cannot be called, so it is never asked about as the called task. A private or hidden destructive
  task that an exposed task runs is named in the request, as it is named in today's refusal.
- **A task unavailable on this host.** It is not counted and not named, as in spec 034.
- **The same destructive task reached twice.** It is named once.
- **The Runefile changes on disk but does not load.** The server keeps the tasks it had (spec 043),
  and a pending request stays valid: what the person was shown is still what runs.
- **A server started with `--no-reload`.** Its tasks never change, so no pending request is
  invalidated by an edit.
- **The call is cancelled while Rune waits.** The request is withdrawn and nothing runs.
- **The answer arrives after Rune stopped waiting.** It is ignored.
- **The person approves, and the task then fails.** The result is the task's failure, as for any
  call. The approval is spent.
- **A destructive task reached at run time that the request did not name.** Rune declines it when
  the run gets there, as it does today.
- **A client that states form support and then rejects the request.** Nothing runs, and the result
  says the client returned no valid answer.
- **An embedded server.** A program that embeds the server library gets no request unless it turns
  the feature on, and its engine has to support carrying an approval. Otherwise it refuses as today.
- **Several requests at once in one client.** Each is its own request with its own answer. How the
  client shows them is the client's choice.
- **An agent that keeps calling.** Nothing limits how often an agent can call a destructive task,
  so a declined agent can make the client ask again at once. Each request still needs its own yes.
  The guide says so. A limit on requests is not part of this feature.

## Requirements *(mandatory)*

### Functional Requirements

**When Rune asks**

- **FR-001**: On `rune mcp` and `rune serve`, over stdio and Streamable HTTP, when a call can run a
  destructive task the operator has not granted and the calling client can show a form request, Rune
  MUST ask the person through that client to approve the call, and MUST NOT run any task body, hook
  or dependency of the call before it has the answer.
- **FR-002**: The set of destructive tasks a call can run MUST be the one spec 034 defines: the
  called task when it has `[confirm]`, and every `[confirm]` task reached through dependencies, `&&`
  hooks and `||` hooks, less the tasks the operator granted. The order is the called task first,
  then the order a run first reaches them.
- **FR-003**: A call that can run no ungranted destructive task MUST behave exactly as today, and
  nobody is asked.
- **FR-004**: Before asking, Rune MUST have checked that the called task is a tool of this server
  and that the arguments are valid: no undeclared name, every declared constraint met, every
  required argument present. A call that fails a check gets that check's error and no request.
- **FR-005**: Approval MUST NOT make callable a task that is private, hidden by `--allow` or
  `--deny`, the `[context]` hook, or unavailable on this host.
- **FR-006**: Rune MUST decide whether the client can show a form request from what the client
  stated when the session was set up, and from nothing else: form support stated, or elicitation
  stated with no mode, which the protocol defines as form support. A client that stated no
  elicitation, or URL elicitation only, MUST NOT be sent a request.

**What the request says and how it is answered**

- **FR-007**: The request MUST show: the name of the called task; its arguments as the task will
  receive them, defaults included; and each destructive task from FR-002 by name with its
  `[confirm]` message, or the default question the command line uses when it has none.
- **FR-008**: The request MUST ask for one required true-or-false answer that defaults to false.
  Rune MUST treat the call as approved only when the client reports that the person accepted the
  request and that answer is the boolean true.
- **FR-009**: Rune MUST leave the presentation to the client. It MUST NOT depend on a particular
  widget, button label or layout, and MUST NOT treat any text from the agent, in arguments or
  elsewhere, as approval.
- **FR-010**: Every argument value and every `[confirm]` message in the request MUST be masked
  with the same secret set as task output. Every name, value and message MUST have characters that
  are not printable escaped, so it stays on its line. An argument value MUST be shown whole, and
  each element of a variadic argument on its own. A `[confirm]` message MAY be cut to a fixed limit
  with a visible mark. The whole request MUST stay within a fixed size.
- **FR-011**: Every destructive task covered by an approval MUST be named in the request. If the
  request, with every name and every argument, does not fit within the size limit, Rune MUST refuse
  the call and not ask. It MUST NOT drop a name or an argument to make it fit.

**What an approval covers**

- **FR-012**: An approval MUST cover exactly the destructive tasks named in its request, for the one
  call that asked, and MUST be used for one run. It MUST NOT be stored, MUST NOT apply to another
  call, another session or a later retry, and MUST NOT change any server-wide setting or grant.
- **FR-013**: The run MUST use the arguments the request showed and the Runefile load the request
  was built from. If the server replaced that load while the request was pending, Rune MUST NOT run
  the call and MUST report that the Runefile changed.
- **FR-014**: An approved call MUST pass the engine's own confirmation gate for the named tasks and
  only for them. A `[confirm]` task the request did not name MUST still be declined where a run
  reaches it. Approval MUST NOT be implemented by turning on blanket approval for the run.
- **FR-015**: Each call MUST get its own request. Concurrent calls MUST NOT share a request or an
  answer, and a request MUST be sent only to the session that made the call.

**Everything that is not an approval**

- **FR-016**: Decline, dismissal, an accepted request whose answer is false or missing, an answer of
  the wrong type, an unknown action, a client error, a disconnect, a cancelled call, and no answer
  within the wait limit MUST each run nothing.
- **FR-017**: Each case in FR-016 that still has a caller to answer MUST return an error result that
  names the called task, says it was not approved, and says which case it was. An answer that is
  not valid and a client error are one case. Every such result, except for a cancelled call and for
  a Runefile that changed, MUST also name the other ways to run the task, as things the operator can
  do: the grant flag with the tasks to add, and the command. A decline is included: a client run
  with nobody at it can decline by itself, so a "no" is not always a person's. Like every result of a call that ran no task, it
  carries no exit code and no task output.
- **FR-018**: Rune MUST wait for an answer for a fixed, documented time and then stop waiting. An
  answer that arrives afterwards MUST have no effect.
- **FR-019**: Rune MUST NOT fall back to approval, to another way of asking, or to running anything
  when the request cannot be made or is not approved.
- **FR-020**: No approval request and no result that reports the outcome of one may contain a
  secret value. Text supplied by the client in an error MUST NOT be passed on to the agent. The
  argument errors a call can get before the person is asked are today's and are not changed.

**Clients that cannot be asked**

- **FR-021**: For a client that cannot show a form request, the call MUST be refused before anything
  runs, at the point where it is refused today. The text MUST name the same tasks and the same flag
  as today's refusal and MUST add the command a person can run for the called task themselves and
  the reason Rune could not ask.
- **FR-022**: The endpoint an `agent` task opens MUST NOT ask, and its refusal text MUST stay as it
  is today.
- **FR-023**: The embeddable server library MUST keep today's behavior unless the embedding program
  turns the request on. With it on, the library MUST ask only when the program's engine can carry an
  approval into its run, and otherwise refuse as today.

**Protocol range**

- **FR-024**: The request MUST be the one the revisions Rune's MCP library negotiates (up to
  2025-11-25) define: a request from the server during the tool call. Rune MUST NOT accept an
  approval carried by a repeated tool call: a call sent again, with whatever content, is a new call
  and is asked about again. The documentation MUST name the supported revisions and say that the
  retried-call flow of 2026-07-28 is not supported.

**Documentation and tests**

- **FR-025**: The MCP guide MUST describe the request, what approval covers, every non-approval
  outcome, the fallback for clients that cannot ask, the trust placed in the client, and the
  supported protocol range.
- **FR-026**: The client compatibility table MUST gain, per client and version, the elicitation
  modes the client states and what is known about how it shows the request and what it does on
  timeout and cancellation, each cell with its evidence. It MUST NOT claim support that was neither
  recorded nor read.
- **FR-027**: The spec 031 contract for tool calls MUST describe the approval request and the
  fallback.
- **FR-028**: Automated tests MUST cover the issue's acceptance criteria through a real client
  session on both transports and through the engine's confirmation gate, not only the server's
  handler: an approved call has to run a `[confirm]` body with no terminal attached.

### Key Entities

- **Approval request**: What Rune sends to the client for one call: the called task, its arguments,
  the destructive tasks the call can run with their messages, and one true-or-false question.
- **Approval**: The person's yes to one approval request. It covers the tasks that request named,
  for that call, once. It exists only while that call is in progress.
- **Ungranted destructive set**: The `[confirm]` tasks a call can run that are not in
  `--allow-destructive` (spec 034). It is what the request names and what the approval covers.
- **Grant**: The operator's `--allow-destructive` entry (spec 031). Standing, per task, unchanged.
- **Runefile load**: The tasks the server serves between two reloads (spec 043). A request belongs
  to the load that was current when the call started.
- **Client form support**: What the client stated at session setup about elicitation: none, form,
  URL, or both.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every approved call in the acceptance scenarios, each task of the call ran exactly
  once, and the request reached the client before any marker file existed.
- **SC-002**: For every non-approval case in User Story 2 and every case in User Story 4, no marker
  file exists after the call, including those of non-destructive dependencies.
- **SC-003**: In a run of 50 pairs of concurrent calls where one of each pair is approved and the
  other declined, exactly the 50 approved calls ran.
- **SC-004**: A person can approve a destructive call without leaving the agent client and without
  restarting the server: zero restarts and zero terminal commands for the approved path.
- **SC-005**: In every request and result of the secret scenarios, the number of occurrences of a
  secret value is zero.
- **SC-006**: Calls that can run no ungranted destructive task, and every call on a server that does
  not ask, give the same result text as before this feature.
- **SC-007**: Every cell added to the compatibility table names its evidence, and every "recorded"
  cell is held to its recording by a test.

## Assumptions

- **The request is on by default on `rune mcp` and `rune serve`.** The issue asks for approval "for
  each call" with supported elicitation and names no switch. An operator who must rule a task out
  uses `--deny` on it and on the exposed tasks that can run it, and the guide says so.
- **A grant still means no request.** `--allow-destructive` exists (spec 031). The issue calls it a
  future, separate operator policy. It stays separate: a granted task is the operator's standing
  decision and is not asked about.
- **One request per call, with one answer.** A call that can run several destructive tasks shows all
  of them in one request and gets one yes or no for the set. Each task is named, so none is approved
  unseen. One request per task would multiply prompts for no added control, since refusing any one
  of them refuses the call.
- **Rune waits five minutes for an answer.** Long enough to read the request, short enough that an
  abandoned call ends. Clients with a shorter call limit cancel first, which runs nothing. The plan
  fixes the value and the guide states it.
- **Only the called task's arguments are shown.** A dependency's arguments are written in the
  Runefile, and the author's `[confirm]` message is the description of what it does.
- **The refusal for a client that cannot ask changes its text on `rune mcp` and `rune serve`.** It
  keeps today's sentence and adds to it. Tests that pin today's text for those servers change with
  it, because this feature intends the change. Servers that do not ask keep today's text byte for
  byte.
- **Resolving arguments before asking has no side effect.** Parameter defaults are expressions, and
  Rune's expression language runs no command (Constitution III).
- **The retried-call flow is not built.** The 2026-07-28 revision is not negotiated by the MCP
  library Rune pins, so there is nothing to follow yet. FR-024 fixes the property that flow would
  need: no approval outlives its call. Supporting that revision is a later change with the library
  upgrade.
- **Compatibility evidence is limited to what can be recorded or read.** Claude Code and Codex CLI
  can be run here without a display. Cursor, VS Code and Zed cannot, so their cells come from
  documentation or source, or say unknown.
- **"Destructive" means the task has `[confirm]`**, as in specs 031, 033 and 034.
- **Constitution Principle VII is amended in the same change.** Its sentence "Destructive tasks are
  refused on the agent surface by default" stops being literally true for a client that can be
  asked. The intent stays: a destructive task does not run on the agent surface unless someone with
  authority says yes, the operator at start or the person for one call. The amendment is proposed
  with this feature and takes effect when the maintainer merges it, as spec 033's did.
- Rune is before 1.0. New optional fields and an optional interface on the server library do not
  break an embedder that sets fields by name.
