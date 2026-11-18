# Feature Specification: Bind Approval and Apply to the Exact Reviewed Plan

**Feature Branch**: `051-mcp-plan-apply-binding`

**Created**: 2026-11-17

**Status**: Draft

**Input**: GitHub issue [#49](https://github.com/rune-task-runner/rune/issues/49)
(backlog MCP-15): "Bind human approval and apply to the exact reviewed plan artifact"

## Overview

An agent runs `plan`, a person looks at the result and approves, and the agent then runs `apply`.
Today nothing ties the three together. `apply` can plan again and change things nobody looked
at, and a file left at the plan's path can be swapped between the review and the apply.

This feature adds two task attributes. `[plan(...)]` marks the task that makes a plan and names
the file it writes. `[applies("...")]` marks the task that applies the plan of a named plan task.
Rune takes the plan file into a store it manages, identifies it by its SHA-256 digest, and lets
the apply task run only with that stored file, only after a person approved that digest, and only
once.

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

| Step | Today | After |
|---|---|---|
| Agent calls `plan` with `env=prod` | Runs. `tfplan` stays in the workspace | Runs. Rune moves `tfplan` and `tfplan.txt` into its store and reports the plan's id and digest |
| Agent calls `apply` with `env=prod` | Runs whatever the body does | The person at the client is shown the target, the digest and the summary, and asked |
| The person approves | Not asked | `apply` runs once with `RUNE_PLAN_FILE` set to a private copy of the stored plan |
| Agent calls `apply` again | Runs again | Refused: the plan was already used |
| Agent calls `apply` with no plan made | Runs | Refused: there is no plan for these arguments |
| The stored plan or its summary was changed on disk | Not detected | Refused: the file does not match its digest |
| The Runefile changed after the plan | Not detected | Refused: the plan was made for another definition |
| Agent calls `plan` again, then `apply` | Runs | The person is asked about the new plan. The old one is gone |
| Client cannot show a request | Runs | Refused, with the command the operator can run |
| A person runs `rune plan prod`, then `rune apply prod` | Runs | The terminal shows the same identity and summary and asks |

What the person is asked for the second row:

```text
Approve this Rune task call?

Task: apply
Arguments:
- env: "prod"
Plan: 7f3c9a1e52b84d06 made by "plan" at 2026-11-17T10:04:11Z
Artifact: sha256:9b1f...c2e0 (48213 bytes)
Summary:
  ...
  Plan: 2 to add, 1 to change, 0 to destroy.
Destructive tasks it can run:
- apply: Run "apply"?

Approving applies this plan once.
```

### What the binding does and does not prove

A matching digest proves that the bytes the apply task receives are the bytes that were stored
when the plan task finished, and that the summary shown is the one stored with them: both are
checked against their recorded digests before the person is asked. It does not prove
that the remote system is unchanged since the plan: the tool's own state lock and stale-plan
check stay in force, and a stale plan fails in the tool. It does not undo a partial apply.

The store is protected from other users of the machine by file permissions. It is not protected
from a process of the same user: an agent that has its own shell can write there, as it can edit
the Runefile. That is the separate problem of issues #46 and #51.

### Out of scope

- Parsing plan formats. Rune does not read a Terraform plan. The summary is what the plan task
  wrote.
- An exact-plan integration for Helm and for database migration runners. They are documented as
  unsupported, with the reason (User Story 8).
- An approval given in advance and stored for a later call. An approval covers the apply call it
  was given in and nothing else.
- An operator flag that approves plans, and any way for `--allow-destructive` to stand in for the
  approval of a plan.
- An apply task reached through a dependency or a hook. It is a static error.
- A general action audit log (issue #50) and enforced sandboxing (issue #51). This feature keeps
  its own journal of plan events.
- Rolling back a failed apply.
- A remote or shared store. The store is local to the Runefile's directory.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The apply task runs the plan the person approved (Priority: P1)

An agent calls `plan`. Rune runs it, takes the declared file and its summary into the store, and
reports the plan's id and digest in the result. The agent calls `apply` with the same arguments.
The person at the client sees the target, the digest and the summary, and approves. `apply` runs
once and receives the stored bytes. Planning does not run again.

**Why this priority**: This is the feature. Without it an approval is of a description, not of
what runs.

**Independent Test**: Serve the Overview Runefile with a stand-in for `tofu` that writes a plan
file and, on apply, records the bytes it was given. Call `plan`, then `apply` with a client that
approves. The recorded bytes equal the plan file's, the stand-in was run for `plan` once, and the
request the client received holds the target, the digest of those bytes and the summary.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile served over stdio, **When** an agent calls `plan` with
   `env=prod`, **Then** the task runs, the result reports a plan id and `sha256:` digest, the
   store holds the file's bytes unchanged, and the workspace no longer holds `tfplan` or
   `tfplan.txt`.
2. **Given** that plan, **When** the agent calls `apply` with `env=prod`, **Then** the client
   receives one approval request that names the task, its arguments, the plan id, the plan task,
   the digest and size of the artifact, and the summary.
3. **Given** the person approves, **When** the call continues, **Then** the body of `apply` runs
   once with `RUNE_PLAN_FILE` naming a file whose bytes have the approved digest, and the body of
   `plan` does not run.
4. **Given** the same Runefile served over Streamable HTTP, **When** the same calls are made,
   **Then** the results are the same.
5. **Given** a summary that holds a secret of the server's environment, **When** the request is
   written, **Then** the secret is masked in it.
6. **Given** a summary longer than the request can show, **When** the request is written,
   **Then** it shows the start of the summary and a mark that says how much is left out.
7. **Given** the apply ended, **When** the store is read, **Then** the plan is recorded as
   applied with the exit code, and its artifact bytes are gone.

---

### User Story 2 - Anything other than the reviewed plan is refused (Priority: P1)

The agent calls `apply` when no plan exists, when the stored file was replaced, when the store
entry is a symbolic link, when the Runefile, the tool or the configuration changed since the
plan, when the arguments differ from the plan's, or when the plan is too old. Each call is
refused before the person is asked, with the reason, and nothing runs.

**Why this priority**: The digest is only worth approving if nothing else can run under it.

**Independent Test**: For each case, prepare the store or the Runefile accordingly and call
`apply` with a client that would approve. No approval request arrives, no marker of `apply`
exists, and the result is an error naming the case.

**Acceptance Scenarios**:

1. **Given** no plan was made for `env=prod`, **When** an agent calls `apply` with `env=prod`,
   **Then** it is refused with a text that says there is no plan for these arguments and names
   the plan task to run.
2. **Given** a plan for `env=staging` only, **When** an agent calls `apply` with `env=prod`,
   **Then** it is refused as in scenario 1.
3. **Given** a plan whose stored artifact was overwritten with other bytes, **When** `apply` is
   called, **Then** it is refused: the artifact does not match its digest.
4. **Given** a plan whose stored artifact was replaced by a symbolic link, or is not a regular
   file, **When** `apply` is called, **Then** it is refused.
5. **Given** a plan, and a Runefile edited afterwards, **When** `apply` is called, **Then** it is
   refused: the plan was made for another Runefile definition.
6. **Given** a plan declared with `tool="tofu"`, and a different `tofu` executable on the path
   afterwards, **When** `apply` is called, **Then** it is refused: the tool changed.
7. **Given** a plan, and a dotenv file or setting changed afterwards, **When** `apply` is called
   through a load of the Runefile that read the change (the command line, or a server started
   or reloaded since), **Then** it is refused: the configuration changed. A running server
   keeps the configuration it loaded, so its own plans stay valid for it.
8. **Given** a plan older than its expiry, **When** `apply` is called, **Then** it is refused:
   the plan expired.
9. **Given** a plan, **When** the agent calls `plan` again with the same arguments, **Then** the
   first plan can no longer be applied and its artifact bytes are gone. `apply` asks about the
   second.
10. **Given** a file placed at the workspace path `tfplan` after the plan, **When** `apply` is
    approved and runs, **Then** the body receives the stored bytes, not that file's.
11. **Given** a plan task whose declared file is a symbolic link or is missing when its body
    ends, **When** the plan task runs, **Then** it fails, and no plan is stored.
12. **Given** a plan whose stored summary was overwritten, removed or replaced by a link,
    **When** `apply` is called, **Then** it is refused before the person is asked.
13. **Given** a tool whose own check finds the remote state changed since the plan, **When** the
    approved apply runs, **Then** the tool refuses, the apply fails, and the plan is recorded as
    failed.

---

### User Story 3 - One approval is one attempt (Priority: P1)

Two `apply` calls arrive together and the person approves both. One runs. The other is refused.
An apply that fails is recorded as failed. A later `apply` call is refused until a new plan is
made, and the request for that new plan tells the person that the last apply of this target
failed. Nothing replays an apply by itself.

**Why this priority**: An approval that can be used twice is an approval of something the person
did not see: the second run meets a system the first run changed.

**Independent Test**: Start two `apply` calls with a client that approves both: the marker of
`apply` exists once and one result is the refusal. Make `apply` fail, call it again: refused
with no request. Run `plan` again and call `apply`: the request holds the note about the failure.

**Acceptance Scenarios**:

1. **Given** one plan, **When** two `apply` calls run at once and both are approved, **Then**
   the body runs once and the other call is refused: the plan was already used.
2. **Given** a plan that was applied, **When** `apply` is called again, **Then** it is refused
   with no approval request.
3. **Given** an approved apply whose body fails, **When** it ends, **Then** the plan is recorded
   as failed with the exit code, and the result is the task's failure.
4. **Given** that failed plan, **When** `apply` is called again, **Then** it is refused, and the
   text says the apply failed and a new plan has to be made and reviewed.
5. **Given** a new plan after a failed apply of the same target, **When** `apply` is called,
   **Then** the approval request says that the previous apply of this target failed and when.
6. **Given** an apply that was stopped before it ended, such as by a killed server, **When**
   `apply` is called again, **Then** it is refused: the earlier attempt did not finish, and its
   outcome is unknown.
7. **Given** an approval request that is declined, dismissed or times out, **When** it ends,
   **Then** nothing ran and the plan can still be applied by a later approved call.
8. **Given** a call sent again by the client after a connection loss, **When** it arrives,
   **Then** it is a new call: it asks again, and it can run only if the plan is still unused.
9. **Given** an approved apply whose dependency fails before the body starts, **When** it ends,
   **Then** the plan is recorded as used and failed. The attempt is not given back.
10. **Given** a request that is pending, **When** a new plan for the same arguments is stored and
    the person then approves, **Then** nothing runs, the call is refused because the plan was
    replaced, no failure is recorded, and the next call asks about the new plan.

---

### User Story 4 - Approval cannot be supplied, widened or skipped (Priority: P1)

The agent cannot name a digest or pass an "approved" argument: the apply task takes the plan from
the store by its own arguments. `--allow-destructive apply` does not approve a plan. A value the
`[agent-allow]` policy refuses stays refused, and the person is not asked about it. A client that
cannot show a request gets a refusal with the command the operator can run. A task body that
runs `rune apply` inside an agent run is refused.

**Why this priority**: Every one of these is a way to apply without a person seeing the digest.

**Independent Test**: Serve with `--allow-destructive apply` and a client that can be asked: the
request still arrives. Serve to a client without form elicitation: refused, with the command.
Add `[agent-allow(env="staging")]` to `apply` and call with `prod`: refused for the value, no
request.

**Acceptance Scenarios**:

1. **Given** the tool for `apply`, **When** a client lists tools, **Then** its input schema has
   only the task's parameters, the description says the task applies the plan of `plan` and
   that every call is put to the person, and the tool is marked destructive.
2. **Given** `--allow-destructive apply`, **When** an agent calls `apply`, **Then** the person is
   still asked.
3. **Given** a client that did not state form elicitation, **When** an agent calls `apply` for
   a plan that could be applied, **Then** it is refused, nothing runs, and the text names the
   reason and the command the operator can run themselves. A call that could not run anyway is
   refused for its own reason first.
4. **Given** `apply` also has `[agent-allow(env="staging")]`, **When** an agent calls it with
   `env=prod`, **Then** it is refused for the value and no request is sent.
5. **Given** the endpoint an `agent` task opens for its own agent, **When** that agent calls
   `apply`, **Then** it is refused: nobody can be asked there.
6. **Given** a task whose body runs `rune --yes apply prod`, **When** an agent calls that task,
   **Then** the inner `rune` refuses to apply and exits with an error.
7. **Given** `apply` also has `[confirm("Really?")]`, **When** an agent calls it, **Then** one
   request is sent, and it holds both the plan and the confirmation message.
8. **Given** `apply` has a dependency with `[confirm]` the operator did not grant, **When** an
   agent calls `apply`, **Then** the one request names that task too, as spec 049 defines.
9. **Given** the Runefile is reloaded while a request is pending, **When** the person approves,
   **Then** nothing runs, as spec 049 defines.

---

### User Story 5 - A person at a terminal gets the same flow (Priority: P2)

A person runs `rune plan prod`. Rune stores the plan and prints its id and digest. They run
`rune apply prod`. Rune prints the target, the digest and the summary and asks. On `y` the apply
runs with the stored plan. This is also the manual way forward for an operator whose client
cannot show a request: the plan the agent made is in the store, and the operator applies it.

**Why this priority**: A person needs the same guarantee, and the refusal given to an agent has
to name a way forward that works.

**Independent Test**: Run `rune plan prod` and `rune apply prod` in a terminal session. The
question shows the digest of the stored file. Answer `n`: nothing runs and the plan is still
usable. Answer `y`: the body receives the stored bytes.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** a person runs `rune plan prod`, **Then** the plan
   is stored and a line on standard error gives its id, digest, size and expiry.
2. **Given** that plan, **When** a person runs `rune apply prod` at a terminal, **Then** the
   identity and the masked summary are printed and one question is asked. `y` runs the apply
   with the stored plan. Any other answer runs nothing and leaves the plan usable.
3. **Given** `--yes`, **When** a person runs `rune --yes apply prod`, **Then** the identity is
   printed and the apply runs without a question.
4. **Given** standard input is not a terminal and `--yes` is absent, **When** `rune apply prod`
   runs, **Then** it is refused, as a `[confirm]` task is, and the text names `--yes`.
5. **Given** a plan an agent made through the server, **When** a person runs `rune apply prod`
   in the same directory, **Then** that plan is the one offered.
6. **Given** `--dry-run` or `--summary`, **When** `plan` or `apply` is named, **Then** nothing
   is stored, asked or used.
7. **Given** any refusal of User Stories 2 and 3, **When** the same state is met from the
   command line, **Then** the same refusal is given.
8. **Given** a command that names an apply task and any other task, or an apply task twice,
   **When** it is run, **Then** it is a usage error and nothing runs.

---

### User Story 6 - A wrong declaration is a static error (Priority: P2)

An author names a plan task that does not exist, points `[applies]` at a task without `[plan]`,
gives the two tasks different parameters, writes an apply body that never reads the stored plan,
makes the apply task depend on its plan task, or runs the apply task from another task. Each is
an error with a code and a position before anything runs.

**Why this priority**: A binding that is silently not applied leaves the gap open while the
Runefile reads as if it were closed.

**Independent Test**: Check each invalid declaration. It fails with `RUNE2020` at the offending
text and runs nothing.

**Acceptance Scenarios**:

1. **Given** the Overview Runefile, **When** it is checked, **Then** it has no error.
2. **Given** `[applies("paln")]` and no task `paln`, **When** it is checked, **Then** the result
   is `RUNE2020` at the name, with the nearest task name as a hint.
3. **Given** `[applies("build")]` where `build` has no `[plan]`, **When** it is checked,
   **Then** the result is `RUNE2020`.
4. **Given** an apply task whose parameters are not the plan task's (the same number and,
   position by position, the same name, parameter kind, constraint kind and enum values),
   **When** it is checked, **Then** the result is `RUNE2020` naming the difference.
5. **Given** an apply task whose body does not mention `RUNE_PLAN_FILE`, **When** it is checked,
   **Then** the result is `RUNE2020`: the body would not use the reviewed plan.
6. **Given** an apply task that reaches its plan task through a dependency or hook, **When** it
   is checked, **Then** the result is `RUNE2020`.
7. **Given** any task that names an apply task as a dependency or hook, **When** it is checked,
   **Then** the result is `RUNE2020` at that edge.
8. **Given** `[plan]` with no `file`, no `summary`, an unknown argument, a repeated argument, an
   absolute path, a path with `..` or a backslash, an empty value, `file` equal to `summary`, or an `expires`
   that is not a duration between one minute and seven days, **When** it is checked, **Then**
   the result is an error at that argument.
9. **Given** `[plan]` or `[applies]` twice on one task, both on one task, either with `[cache]`,
   `[readonly]` or `[context]`, or either on an `agent` task, **When** it is checked, **Then**
   the result is `RUNE2020`.
10. **Given** an apply task with `[env("RUNE_PLAN_FILE", ...)]`, or the same for `RUNE_PLAN_ID`
    or `RUNE_PLAN_DIGEST`, **When** it is checked, **Then** the result is `RUNE2020`.
11. **Given** a Runefile with one of these errors, **When** `rune mcp` is started on it,
    **Then** the server does not start.

---

### User Story 7 - Stored plans are protected, bounded and traceable (Priority: P2)

A plan can hold secrets. Its bytes are stored readable by the owner only, are deleted when the
plan is used, replaced or expired, and never appear in a journal. The journal holds one line per
event and links a plan's creation, each approval request and its answer, and each apply attempt
by the plan id, the artifact digest and the attempt id.

**Why this priority**: The store is a new place where secrets rest, and the question "what was
approved and what ran" has to have an answer afterwards.

**Independent Test**: Run a plan and an approved apply. Read the store: directories are owner
only, the artifact is gone after the apply, the journal's lines for plan, approval and apply
carry the same plan id and digest, and no line holds a byte sequence of the plan or the summary.

**Acceptance Scenarios**:

1. **Given** a stored plan, **When** its files are listed on a POSIX host, **Then** directories
   are mode `0700` and files are not readable by group or others.
2. **Given** the store, **When** a version control status is taken, **Then** nothing in it is
   offered for commit.
3. **Given** a plan that was applied, failed, replaced or expired, **When** the store is next
   used, **Then** its artifact and summary bytes are gone. An attempt that never finished
   keeps its bytes for 24 hours, because an apply may still be reading them, and loses them
   then.
4. **Given** records whose last event is older than the retention period, **When** the store is
   next used, **Then** they are removed.
5. **Given** a plan, an approval and an apply, **When** the journal is read, **Then** the events
   `plan-created`, `approval-requested`, `approval-given` and `apply-finished` share the plan id
   and digest, and the apply events share an attempt id.
6. **Given** an apply refused by a plan check or at the claim, **When** the journal is read,
   **Then** it holds an `apply-refused` event with the reason. A person who could not be asked
   is recorded as an approval that was requested and not given, with the reason.
7. **Given** arguments or a summary that hold a secret, **When** the journal is read, **Then**
   the secret's bytes do not occur in it.
8. **Given** a journal that reached its size limit, **When** an event is written, **Then** the
   older half of the history is dropped and the journal stays bounded.

---

### User Story 8 - The documentation states what is supported and what is not (Priority: P2)

A reader finds a working OpenTofu and Terraform example with fixtures, the lifecycle of a plan,
retention, recovery after a failed apply, what the digest does not prove, and what Rune trusts.
The reader also finds that Helm and database migration runners are not supported as exact-plan
integrations, and why.

**Why this priority**: The feature is a security control. A reader who takes a Helm dry run for
a saved plan would trust it for more than it gives.

**Independent Test**: Read the plan and apply section of the MCP guide, the attribute entries of
the Runefile reference, the grammar and the diagnostics table. Run the example with the stand-in
tool in the documentation test harness.

**Acceptance Scenarios**:

1. **Given** the examples, **When** the documentation tests run, **Then** an OpenTofu example and
   a Terraform example validate, and an end-to-end test runs plan, approval and apply against a
   stand-in tool that enforces a stale-plan check.
2. **Given** the MCP guide, **When** a reader reads the section, **Then** it gives the flow, the
   request, every refusal, the lifecycle and states of a plan, retention, recovery, the limits on
   remote state, and the trusted execution assumptions.
3. **Given** the same guide, **When** a reader looks for Helm or migrations, **Then** it says
   each is unsupported as an exact-plan integration, what would have to be bound, and that an
   author must not present a dry run or rendered manifests as a saved plan.
4. **Given** the Runefile reference, the grammar and the diagnostics table, **When** a reader
   looks up the attributes or `RUNE2020`, **Then** each is there.

---

### User Story 9 - Runefiles without the attributes see no change (Priority: P1)

A Runefile that uses neither attribute is served and run exactly as before: same tools, same
results, no store directory.

**Why this priority**: The feature must not reach anyone who did not ask for it.

**Independent Test**: Serve a Runefile with no `[plan]` and no `[applies]`. The tool list and
every result are byte for byte what they were, and no store directory is created.

**Acceptance Scenarios**:

1. **Given** a Runefile with neither attribute, **When** it is served, **Then** every tool and
   result is the same as before this feature.
2. **Given** the same Runefile, **When** tasks run from the command line or the server, **Then**
   no store directory exists afterwards.

---

### Edge Cases

- **Arguments are wrong.** Unknown names, invalid values and missing required arguments are
  answered as today, before the store is read.
- **Order of checks for an apply call.** Argument validation, then the `[agent-allow]` policy,
  then the plan checks, then the approval request or the refusal that says why the person
  cannot be asked, then the one-time claim, then the run.
- **The plan body fails.** Nothing is stored and an earlier plan for the same arguments stays as
  it was. A plan is stored when the body ends, so a hook of the plan task that fails afterwards
  does not undo it.
- **Rune's plan variables are set elsewhere.** The values Rune gives the apply body win over
  the process environment, a dotenv file and exported variables.
- **A variable override on the command line.** `rune region=eu plan` and `rune region=us apply`
  are different configurations: the apply is refused.
- **What the binding does not see.** Values read by `env(...)`, and scripts a body calls, are
  not part of any digest.
- **Two Runefiles in one directory.** Their plans are kept apart by the Runefile's path.
- **The plan task succeeds but the file is too large.** The task fails with the limit named.
- **The workspace file cannot be removed after it was stored.** The plan stands, and a warning
  names the file.
- **The summary is empty.** The request says so. The plan can still be approved.
- **A plan with a variadic parameter.** The elements are part of the arguments compared.
- **A secret in an argument.** The store keeps a digest of the arguments for comparison and the
  masked text for display.
- **Two plan runs at once for the same arguments.** Both are stored. Only the newest plan of a
  task and arguments can ever be applied, so the earlier one is replaced whichever finished
  storing first.
- **The store directory is a symbolic link or is not owned by the user.** Rune refuses to use it.
- **Windows.** File modes do not restrict access there. The documentation says so. Every other
  rule holds.
- **A Runefile reload during an apply.** The call runs against the load it started with. Its
  plan was checked against that load.
- **The private copy is changed while the apply runs.** Rune checks it when the run of the
  apply call ends, records the plan as failed and tampered, says so in the result, and the
  call fails.
- **An embedded server.** A program that embeds the server library can mark a task as applying
  a plan. Storing, checking and consuming the plan is the work of its engine.

## Requirements *(mandatory)*

### Functional Requirements

**The attributes**

- **FR-001**: Runefiles MUST accept a task attribute `[plan(file="...", summary="...")]` with the
  optional arguments `tool="..."` and `expires="..."`, and a task attribute
  `[applies("<task>")]`. The relationship is only what these attributes state. Rune MUST NOT
  infer it from task names.
- **FR-002**: `file` and `summary` MUST be relative paths written with forward slashes and
  without `..`, resolved against the plan task's working directory. `expires` MUST be a duration from one minute to seven days and
  defaults to one hour. `tool` names an executable looked up on the path.
- **FR-003**: Each invalid declaration of User Story 6 MUST be a static error with code
  `RUNE2020` or a syntax error, a position, and a message that names the fix. A Runefile with
  such an error runs nothing and is not served.
- **FR-004**: The attributes MUST be known to the grammar reference, the formatter, the syntax
  tree dump, editor completion and hover, and editor syntax highlighting.
- **FR-005**: A task with neither attribute MUST behave as it does today for every caller.

**Storing a plan**

- **FR-006**: When the body of a plan task ends successfully in a real run, Rune MUST copy the
  declared file and summary into a store under the Runefile's directory, compute the SHA-256
  digest of the file while copying, and remove both from the workspace. The stored bytes MUST
  equal the bytes read. A plan mode that runs no body stores nothing.
- **FR-007**: Rune MUST refuse to store a file or summary that is missing, is not a regular
  file, is a symbolic link, or exceeds the size limit: 256 MiB for the file, 1 MiB for the summary.
  The plan task then fails.
- **FR-008**: Each stored plan MUST have a record with: a random id, the artifact digest and
  size, the summary digest, the plan task and the apply tasks bound to it, a digest of the
  effective arguments and their masked text, the working directory, a digest of the Runefile
  definition, a digest of the configuration (resolved settings and the dotenv file), the
  identity of the declared tool (the executable its name resolves to in the plan body's
  environment: path and digest), the host
  operating system and architecture, the Rune version, the creation and expiry times, and the
  git commit and whether the tree was dirty when known. The record MUST NOT hold plan bytes.
- **FR-009**: A new plan of the same plan task with the same effective arguments MUST replace
  the earlier unused one, which can then not be applied.
- **FR-010**: The result of a plan task MUST report the plan id, digest, size and expiry.

**Applying a plan**

- **FR-011**: An apply task MUST run only as a task named in a tool call or on the command line.
- **FR-012**: Before an apply task runs, Rune MUST find the newest plan of its plan task whose
  effective arguments equal the call's, and MUST refuse the call, with the reason and before any
  approval request, when: there is none; it was applied, failed, replaced, or its attempt did
  not finish; it expired; the Runefile definition, the configuration, the working directory,
  the host, or the tool identity differs from the record; or the stored artifact or the stored
  summary is missing, is not a regular file, or does not have its recorded digest.
- **FR-013**: The plan to apply MUST be resolved from the store by the task and its effective
  arguments. No argument, environment variable or file the agent supplies MAY select or approve
  a plan.
- **FR-014**: After approval Rune MUST claim the plan atomically, so that of any number of
  calls and processes exactly one proceeds. A claimed plan MUST NOT be claimable again, whatever
  the outcome of the attempt. A claim that is refused, because the plan expired or was replaced
  while the request was pending or its artifact no longer verifies, MUST run nothing and MUST
  NOT record a failed apply.
- **FR-015**: The apply body MUST receive the plan as a private copy made after the claim, read
  once from the store and verified against the recorded digest while it is copied, in a
  directory only the owner can enter, read-only. `RUNE_PLAN_FILE` names it. `RUNE_PLAN_ID` and
  `RUNE_PLAN_DIGEST` identify it. Only the apply task's own body receives them, and Rune's
  values MUST win over any other source of those names.
- **FR-016**: When the run of the apply call ends, Rune MUST verify the private copy again, record the plan as
  applied or failed with the exit code, mark it tampered when the copy changed, and delete the
  artifact, the summary and the copy.
- **FR-017**: Rune MUST NOT run an apply again by itself. A plan that failed or did not finish
  can never be applied. The approval request for a later plan of the same plan task and
  arguments MUST state that the previous attempt failed or did not finish, and when.
- **FR-018**: Rune MUST NOT replace or weaken the tool's own state locking and stale-plan
  checks. A tool that refuses a plan fails the apply.

**Approval**

- **FR-019**: Every apply call from an agent MUST be put to the person at the client through
  the approval request of spec 049. The request MUST show the called task, its arguments, the
  plan id, the plan task, the creation time, the artifact digest and size, and the summary,
  masked, with characters that are not printable escaped. A summary too long for the request
  MUST show its start and its end, with a visible mark between them that states the number of
  bytes left out.
- **FR-020**: A grant with `--allow-destructive` MUST NOT approve a plan. An apply task MUST be
  advertised as destructive.
- **FR-021**: A call the `[agent-allow]` policy refuses MUST be refused for the value first, and
  the person MUST NOT be asked. This holds for a client that cannot be asked too.
- **FR-022**: Where the person cannot be asked (no form elicitation, an `agent` task's endpoint,
  an engine called without approval), the call MUST be refused, run nothing, and name the reason
  and, when the server has one, the command the operator can run. A request that would show no
  plan MUST be refused.
- **FR-023**: An approval covers the one call it was given in. A request that is declined,
  dismissed, timed out, cancelled, or answered after a Runefile reload MUST run nothing and MUST
  leave the plan usable.
- **FR-024**: In a run marked as an agent run, the command line MUST refuse to run an apply
  task, with or without `--yes`.

**The command line**

- **FR-025**: A person's `rune <apply task>` MUST apply the same checks, print the identity and
  masked summary, and ask one question at a terminal. An apply task MUST be the only task named
  in its command. `--yes` answers it. Without a terminal and
  without `--yes` the call is refused as a `[confirm]` task is.
- **FR-026**: Plans made through the server and from the command line MUST share one store.

**Protection, retention and the journal**

- **FR-027**: The store's directories MUST be created for the owner only and its files MUST NOT
  be readable by group or others. Rune MUST refuse a store path that is a symbolic link. The
  store MUST carry an ignore file so that version control does not offer its contents.
- **FR-028**: Artifact and summary bytes MUST be deleted when a plan is applied, failed,
  replaced or expired, at the latest when the store is next used. An attempt that did not
  finish keeps its bytes for 24 hours and loses them then. Records MUST be removed seven days
  after their last event.
- **FR-029**: Rune MUST append one journal line for each of: plan created, plan replaced,
  approval requested, approval given, approval not given, apply refused by a plan check or at
  the claim, apply started, apply finished, on the server and on the command line. Each line
  MUST carry the time, the task names, the surface (server or command line) and, whenever a
  plan is known, its id and artifact digest. When a line cannot be written Rune MUST warn and
  go on. Apply events MUST carry an attempt id. Lines MUST NOT
  hold plan or summary bytes, and argument values MUST be masked.
- **FR-030**: The journal MUST stay within a fixed size of about 1 MiB: when it passes that, the
  older half of its lines is dropped.

**What the agent sees**

- **FR-031**: The tool of an apply task MUST state in its description which plan task it applies
  and that every call is put to the person. Its input schema MUST be the task's parameters only.
- **FR-032**: Every refusal MUST be an error result with no exit code and no task output, MUST
  give the same text for the same state, and MUST mask and bound every value it shows.

**Documentation and tests**

- **FR-033**: The documentation MUST cover the attributes, `RUNE2020`, the flow, the request,
  each refusal, the plan's lifecycle, retention, recovery, the limits on remote state, the
  trusted execution assumptions, an OpenTofu and a Terraform example, and the explicit statement
  that Helm and database migration runners are unsupported as exact-plan integrations.
- **FR-034**: Automated tests MUST cover every acceptance criterion of the issue through a real
  client session on stdio and on Streamable HTTP for the flow of User Story 1, for every agent row of the
  Overview table and for at least one refusal of each of User Stories 2 to 4, and MUST show by
  marker files that a refused call ran nothing. The remaining refusals, which do not depend on
  the transport, MAY be covered on one.

### Key Entities

- **Plan task**: A task with `[plan]`. Its body writes the plan file and its summary.
- **Apply task**: A task with `[applies]`. Its body applies the stored plan it is handed.
- **Plan artifact**: The file a plan task declared, as stored, identified by its SHA-256 digest.
- **Summary**: The text file a plan task wrote beside the artifact for a person to read.
- **Plan record**: What Rune keeps about one stored plan: identity, binding and times.
- **Binding**: The parts a plan was made for: arguments, working directory, Runefile definition,
  configuration, tool identity and host.
- **Plan state**: Ready, unfinished, applied, failed, replaced or expired.
- **Attempt**: One claimed run of an apply task with one plan, with its own id.
- **Journal**: The append-only list of plan events.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In every approved apply of the acceptance scenarios, the bytes the apply body
  reads are identical to the bytes the plan body wrote, and the plan body ran zero times during
  the apply.
- **SC-002**: For every refused call in the acceptance scenarios, zero marker files of the apply
  task exist after the call and zero approval requests were sent.
- **SC-003**: Out of any number of concurrent approved calls for one plan, exactly one runs.
- **SC-004**: Every agent row of the Overview table gives the stated result on stdio and on
  Streamable HTTP, and the command-line row gives the stated result.
- **SC-005**: Every invalid declaration of User Story 6 fails the static check and runs nothing.
- **SC-006**: In the journal, the store's records and every refusal of the secret scenarios, the
  number of occurrences of the secret value is zero.
- **SC-007**: After a plan is applied, failed, replaced or expired, zero bytes of its artifact
  remain in the store once the store is next used. An attempt that never finished keeps none
  after 24 hours.
- **SC-008**: On a Runefile with neither attribute, the tool list and every call result are byte
  for byte what they were before the feature, and no store exists.
- **SC-009**: A reader can find, from one documentation section, what an approved digest proves,
  what it does not, and how to recover from a failed apply.

## Assumptions

- **Approval is given in the apply call and never stored.** The issue asks that authorization be
  consumed once and that an expired approval be refused. An approval that exists only inside one
  call cannot be reused or go stale. What can go stale is the plan, so the plan carries the
  expiry, and the approval request carries the wait of spec 049.
- **The manual workflow is the command line.** A client without form elicitation gets a refusal
  that names the command. The operator runs it and is asked at the terminal about the same
  stored plan.
- **The target is the arguments.** The plan and apply tasks have the same parameters, and an
  apply uses the plan made for equal arguments. An author who needs the workspace or the
  database in the identity makes it a parameter.
- **The execution definition is the whole Runefile.** Any change to the composed Runefile after
  a plan refuses the apply. That is coarser than the apply task alone and cannot miss a change.
- **The configuration snapshot is Rune's.** It is the resolved settings and the dotenv file.
  The tool's own configuration is inside its saved plan and under its own checks.
- **The tool version is the executable's identity.** Rune does not run the tool outside a task
  body, so it records the path and digest of the executable the name resolves to. A version
  manager's shim keeps one identity across the versions it selects.
- **The summary is the author's.** The plan body writes it, for example with `tofu show`. Rune
  stores it with the artifact in one record and shows it. It does not parse the artifact.
- **An attempt uses up the plan whatever stops it.** A dependency that fails before the apply
  body starts still consumes the plan. Giving a claim back would need a second atomic step, and
  a new plan costs one more review.
- **The store lives under `.rune/` beside the Runefile**, where the cache already is, so the
  server and the command line see the same plans.
- **Same-user processes are trusted.** File modes keep other users out. A process of the same
  user is covered by issues #46 and #51. The private copy is checked again after the apply so
  that a change is at least recorded.
- **Recording git information is best effort.** Rune asks `git` for the commit and whether the
  tree is dirty, with a short timeout, and leaves the fields empty when that fails. It is not part of the binding: the Runefile digest is.
- **Helm and migrations are marked unsupported.** The issue allows this until their input
  binding can be shown.
- **Constitution Principle VII gets a clarification.** It says a destructive task runs only when
  granted or approved, and that destructiveness is declared with `[confirm]`. An apply task is
  also advertised as destructive and is stricter: it runs only when approved. The sentence is
  amended to name `[applies]` (version 1.4.1, proposed with this spec).
- **The configuration is the one a load read.** A server computes the configuration digest when
  it loads the Runefile. The dotenv file is not watched, so editing it changes nothing for a
  running server until it restarts.
- Rune is before 1.0. New optional fields on the server library's descriptors do not break an
  embedder that sets fields by name.
