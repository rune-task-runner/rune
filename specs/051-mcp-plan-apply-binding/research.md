# Research: Bind Approval and Apply to the Exact Reviewed Plan

Decisions for [spec.md](spec.md). Each has the decision, why, and what was set aside.

## R1. Two attributes state the relationship

**Decision**: `[plan(file="...", summary="...", tool="...", expires="...")]` on the task that
makes a plan, `[applies("<plan task>")]` on the task that applies it. New constants
`ast.AttrPlan = "plan"` and `ast.AttrApplies = "applies"` join `KnownAttributes`.
`Task.PlanSpec()` returns the plan arguments or nil, `Task.AppliesPlan()` the plan task's name
or "".

**Rationale**: The issue forbids inferring the relationship from task names. The apply side
names the plan side, so one plan task can serve several apply tasks and the analyzer can check
the pair. Arguments are string literals: nothing to evaluate, so the binding is the same on
every host and in every check (Constitution III).

**Alternatives**: One attribute on the apply task holding the plan path: the plan task would
not be known, so Rune could not capture when the plan body ends. A header clause: attributes
are where task policy already lives. Interpolated paths: the store location would depend on
run-time values the analyzer cannot check.

## R2. Parsing and tree shape

**Decision**: `plan(...)` is parsed by the named-string loop that `agent-allow` uses
(`parseAllowPredicates`), generalized to take the attribute name for its messages. The pairs go
into `Attribute.Allow`, whose comment widens to "named string arguments". `applies("...")` uses
`parseSingleStringArg`. The formatter and the dumper already write `Allow` pairs and single
strings, so each gets one `case`.

**Rationale**: No new node type, no new formatter rule, source order kept.

**Alternatives**: A new `PlanArgs` struct on `Attribute`: more code for the same data.

## R3. One diagnostic code

**Decision**: `CodeInvalidPlanBinding = "RUNE2020"`, raised by a new analyzer pass
`checkPlanBinding` for every case of spec User Story 6. Messages are in
[contracts/plan-attributes.md](contracts/plan-attributes.md). Malformed syntax stays a parse
error.

**Rationale**: One code with distinct messages follows `RUNE2015`, `RUNE2018` and `RUNE2019`.

## R4. Modules

**Decision**: `config.Compose` prefixes the name in `[applies]` with the module's namespace, as
it does for dependency names, so `[applies("plan")]` inside module `infra` binds
`infra::plan`.

**Rationale**: A module file is written without knowing its namespace. Dependencies already
work this way.

## R5. A new package for the store

**Decision**: `internal/planstore` holds the store: records, artifacts, the claim, the private
copy, pruning and the journal. It imports only the standard library. `internal/cli` calls it.

**Rationale**: It is file-system code with its own invariants and tests, like `internal/cache`.
Keeping it out of `internal/cli` keeps the engine readable and lets the store be tested without
an engine.

**Alternatives**: Extending `internal/cache`: the cache is a fingerprint store that anyone may
clear, and its entries are not secret. Different rules.

## R6. Where the store is and how it is laid out

**Decision**: `<runefile dir>/.rune/plans/`:

```text
.rune/plans/
  .gitignore            "*"
  journal.jsonl
  <id>/record.json      written once, never rewritten
  <id>/artifact         the plan bytes, mode 0400
  <id>/summary          the summary bytes, mode 0400
  <id>/replaced         marker: a newer plan took its place
  <id>/claim            marker, created with O_EXCL: the one attempt
  <id>/outcome.json     written when the attempt ends
  <id>/attempt-<aid>/plan   the private copy, mode 0400 in a 0700 directory
```

State is derived from which files exist, so no file is rewritten and the claim needs no lock:
`outcome.json` wins (applied or failed), then `claim` alone (unfinished), then `replaced`, then
the expiry, else ready. A claim outranks a `replaced` marker, so a new plan stored at the same
moment as a claim cannot hide an attempt.

**Rationale**: The cache already lives under `.rune/`, which projects ignore. One place serves
the server and the command line. Marker files created exclusively are atomic on every platform
Rune supports, across processes.

**Alternatives**: The user's state directory: a server started with another `HOME`, as in a
container, would not see the operator's plans. One JSON state file: needs a cross-process lock.

## R7. Capturing the plan

**Decision**: `engine.runBodyTo` calls `capturePlan` when the body of a task with `[plan]` ended
without error. It resolves `file` and `summary` against the task's directory, opens each
without following a final symbolic link (`Lstat`, open, `Fstat`, `os.SameFile`), requires a
regular file within the size limit (artifact 256 MiB, summary 1 MiB), streams it into the store
while hashing, writes the record, marks the earlier ready plan of the same task and arguments
as replaced, removes the two workspace files, and prints the plan line to standard error.
Plan modes never reach `runBodyTo`.

**Rationale**: `runBodyTo` is the one place every body goes through, including a failure hook.
Removing the workspace files means an apply body that reads the workspace path fails loudly
and never applies a file nobody reviewed.

**Alternatives**: Leaving the workspace file: it is a second copy of secret-bearing data and
the path a careless apply body would use. Running a summary command against the stored copy:
it needs a second task or a command outside a body. The plan body can write the summary
itself.

## R8. The binding

**Decision**: A record binds, each as a hex SHA-256 or a plain string:

| Part | Value |
|---|---|
| target key | digest of the Runefile's absolute path, the plan task and `eval.Args.Key()` of the effective arguments |
| working directory | the plan task's resolved directory |
| Runefile definition | digest of `ast.Dump` of the composed file |
| configuration | digest of the resolved settings, the dotenv entries and the command-line variable overrides, taken once per load |
| tool | path and digest of the executable `tool` resolves to on the `PATH` of the plan body's environment, when declared |
| host | `GOOS/GOARCH` |

Recorded but not compared: Rune's version, the git commit and dirty flag.

**Rationale**: `ast.Dump` is deterministic, covers every task, variable and setting after
imports are composed, and already exists. It is coarser than the apply task alone and cannot
miss a change in something the apply reaches. Rune does not run the tool outside a task body, so the
tool's identity is its executable's bytes, not the output of `--version`. The configuration
digest belongs to a load: a running server does not re-read the dotenv file, so it must not
re-hash it either, or a new plan would be recorded for a configuration the server is not
using. Values read with `env(...)` and scripts a body calls are in no digest, and the
documentation says so. The effective
arguments are the target: plan and apply have the same parameters (R3).

**Alternatives**: Hashing source files: the command-line path and the server path would need
the same file list. Binding the environment: it changes between any two shells and the tool's
saved plan carries its own configuration.

## R9. Git information

**Decision**: `git rev-parse HEAD` and `git -c core.fsmonitor=false status --porcelain` in the
Runefile's directory, each under the run's context with a three-second timeout and
`GIT_OPTIONAL_LOCKS=0`, so a repository setting cannot start a program. A missing `git`, a directory that
is not a checkout, or a timeout leaves the fields empty. The engine holds the function as a
field so tests replace it.

**Rationale**: The issue asks to record the commit and whether the tree was dirty. Reading
`.git` by hand would miss worktrees and packed refs.

## R10. Finding the plan for an apply call

**Decision**: `Store.Latest(key)` returns the newest record of the target key, ordered by
creation time in nanoseconds and then by id. The engine refuses, before anyone is asked, when
there is none, when its state is not ready, when a bound part differs, or when the stored
artifact or summary does not verify.
Nothing the agent sends selects a plan.

**Rationale**: FR-012 and FR-013. The newest record is the one a person would expect, and an
older ready one cannot exist: a new plan replaces it (R7).

## R11. Approval

**Decision**: `mcpserver.TaskInfo` gains `AppliesPlan string`. A task with it is advertised as
destructive, and the handler always calls `CallApproved` for it, whatever `AllowDestructive`
holds, once the arguments are valid. When the server does not ask or the client cannot show a
form, the `approve` function it passes returns that refusal. So the engine validates the
policy and the plan before anyone learns they cannot be asked, and the engine can journal it.
Its refusals do not offer the grant flag.
`mcpserver.ApprovalRequest` gains `Plan *PlanApproval`, which the request text shows. The
engine's `CallApproved` fills it. The engine journals the request and its answer around the
`approve` callback. `mcpAdapter.CallLists`, which has nobody to ask, refuses an apply task.

**Rationale**: The approval machinery of spec 049 already asks once per call, waits, and treats
everything but an explicit yes as a refusal. An approval that lives only inside one call cannot
be stored, replayed or go stale, which is what the issue asks of authorization.

**Alternatives**: A stored approval record with its own expiry: a second thing to protect and
to consume atomically, for no gain while the plan carries the expiry.

## R12. The claim and the private copy

**Decision**: After approval the engine calls `Store.Claim`. It checks that the plan is still
ready, copies `<id>/artifact` into `<id>/attempt-<aid>/plan`, read through one handle and
hashed while it is copied, and only then creates `<id>/claim` with `O_CREATE|O_EXCL`. A check
that fails, a digest that differs, or a claim that already exists removes the copy and changes
no state, so a plan replaced or expired during the wait is not recorded as a failed apply. The copy's path goes to the apply task's body
as `RUNE_PLAN_FILE`, with `RUNE_PLAN_ID` and `RUNE_PLAN_DIGEST`, through `engine.taskEnv`.
When the run ends, `Attempt.Finish` hashes the copy again, writes `outcome.json`, and removes
the artifact, the summary and the copy.

**Rationale**: Exclusive creation gives exactly one winner among any number of calls and
processes. Hashing while copying leaves no window between the check and the bytes used. The
copy sits in a directory only the owner can enter. A process of the same user is trusted
(spec Assumptions), and the second hash records a change if one happens.

**Alternatives**: Passing a file descriptor: the tools take a path, and the in-process shell
and Windows have no portable way to hand one over. Claiming when the apply body starts:
a dependency could then run between approval and claim, and a failed dependency would leave
the approval question open.

## R13. An apply task is a root

**Decision**: The analyzer rejects any dependency or hook edge that names an apply task, and an
apply task that reaches its plan task. `engine.Execute` also refuses an apply task for which
the run holds no claimed attempt.

**Rationale**: The approval names one plan for one call. An apply reached in the middle of a
run would need its own request there. The second check keeps the rule if the analyzer and the
scheduler ever disagree, as `policyGate` does for spec 050.

## R14. The command line

**Decision**: `execute` handles an apply task named on the command line after the roots are
bound and the agent policy is checked: refuse in an agent run, find and verify the plan, print
the identity and the summary to standard error, ask `Apply this plan once? [y/N]` under the
rules of the `[confirm]` gate (`--yes`, terminal required), journal the question and its
answer, claim, run, finish. An apply task must be the only task named in its command: the
scheduler runs every root, and one approval covers one invocation.

**Rationale**: The same checks from the same functions (FR-025), and the manual way forward
the agent's refusal names.

## R15. Retention and the journal

**Decision**: `Store.Prune` runs on every capture and on every apply lookup. It deletes the
artifact and summary of every plan that is replaced, expired, applied or failed, leaves an
unfinished attempt alone for 24 hours because an apply may still be reading its copy, and
removes a record directory seven days after its last event. The journal is JSON lines, mode
0600, appended under a lock file so two processes cannot lose a line. When it passes 1 MiB the
older half of its lines is dropped. Events and fields are in
[contracts/plan-store.md](contracts/plan-store.md).

**Rationale**: Bounded retention of secret-bearing bytes (FR-028) and a bounded journal
(FR-030), with no background goroutine.

## R16. Tool compatibility

**Decision**: OpenTofu and Terraform are supported and shipped as two examples with a
`main.tf` fixture that uses the built-in `terraform_data` resource, so no provider is
downloaded. The end-to-end test uses a stand-in written in the Runefile's own shell, with a
state serial that makes a stale plan fail, so it runs on every CI operating system. Helm and
database migration runners are documented as unsupported.

**Rationale**: `tofu plan -out` and `terraform plan -out` write one file that `apply` consumes
with the tool's own stale check. Helm has no saved plan: a dry run renders manifests, and
applying rendered manifests drops release and hook semantics. A migration runner validates
checksums of a directory against a live database and has no single artifact to bind.

## R17. Documentation and fixtures

**Decision**: `docs/mcp.md` gets the section "Applying a reviewed plan". `docs/runefile.md`,
`docs/GRAMMAR.md` and `docs/diagnostics.md` get the attributes and `RUNE2020`. `SECURITY.md` and
`README.md` get one sentence each. New corpus and formatter fixtures, a fuzz seed, the VS Code
grammar, and `docs/examples/plan-apply-opentofu` and `docs/examples/plan-apply-terraform`.
The docs harness learns the prerequisites `opentofu` and `terraform`.

## R18. Tests

**Decision**: Test-first at each layer: parser, analyzer, corpus and formatter goldens
(generated with `-update`), `internal/planstore` unit tests (capture, symlink refusal, states,
claim race with goroutines, prune, journal bound, modes), `mcpserver` unit tests (tool,
request text, always-ask, refusals), `internal/cli` engine tests, and binary-level tests in
`test/integration` on stdio and Streamable HTTP with marker files.
