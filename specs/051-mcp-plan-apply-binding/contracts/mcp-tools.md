# Contract: tools, the approval request and the refusals

`<plan>` is the plan task, `<apply>` the apply task, `<id>` a plan id, `<args>` the effective
arguments as `name="value"` pairs joined by `, `, each value masked, cut to 64 characters with
`...`, and quoted. A task with no parameters has no `<args>`. `<time>` is RFC 3339 in UTC, to
the second.

## tools/list

The tool of an apply task:

- `annotations.destructiveHint` is true.
- The input schema is the task's parameters, as for any task.
- The description ends with the paragraph
  `Plan approval: applies the plan made by task "<plan>". Every call asks the person at the client to approve that plan.`

The tool of a plan task and of every other task is unchanged.

## The plan line

After a plan task's body, on standard error, so it is in the tool result and on the terminal:

```text
plan <id>: sha256:<hex>, <n> bytes, expires <time>. Apply it with "<apply>".
```

With several apply tasks the names are joined by ` or `. With none the last sentence is
`No task applies it.`

## The approval request

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

- The `Note:` line appears only when the newest earlier attempt for the same plan task and
  arguments failed or did not finish. For an unfinished one it reads
  `Note: the previous apply of this target started at <time> and did not finish. Check the target before approving.`
  `PlanApproval.Notes` holds the text without `Note: `. The server writes the prefix.
- The summary is rendered by `SummaryLines`: each line indented by two spaces, with characters
  that are not printable escaped. An empty summary is the one line `  (empty)`.
- The rendered summary is at most 4096 bytes. A longer one shows its head and its tail: the
  first lines that fit in 3072 bytes, the cut mark, and the last lines that fit in 1024 bytes,
  because a plan tool prints its totals last. A single line longer than its share is cut inside
  the line, at a character boundary. `<n>` counts the rendered bytes left out.
- The whole request is at most 8192 bytes, as in spec 049. A call whose request would be larger
  is refused as too large. The summary can never cause that by itself.

## Refusals from the server

| When | Text |
|---|---|
| The engine cannot take an approval (an embedder's engine that is not an `ApprovalEngine`) | `task "<apply>" applies a plan and runs only when the person at the client approves that plan; nobody can be asked in this session.` then, with a run command, ` The operator can run "<cmd> <apply>" themselves.` |
| The server does not ask (`AskApproval` is false), found after the engine's checks | the same text |
| The client stated no form elicitation, found after the engine's checks | `task "<apply>" applies a plan and runs only when the person at the client approves that plan.` then, with a run command, ` The operator can run "<cmd> <apply>" themselves.` then ` Rune could not ask for approval here: the client does not support MCP form elicitation.` |
| The request would be too large | as the row above, with the reason `the approval request would be too large to show` |
| The engine's request holds no plan | `task "<apply>" was not run: the engine reported no plan to approve` |
| Not approved | the texts of spec 049, with no mention of the grant flag |

The first row is the only refusal given before the arguments are read. For every other row the
engine has already validated the arguments, checked the `[agent-allow]` policy and checked the
plan, so a call that could not run is refused for that reason first.

## Refusals from the engine

Each starts with `task "<apply>" was not run: `.

| When | Rest of the text |
|---|---|
| No plan, task has parameters | `there is no plan for <args>; run "<plan>" with the same arguments first` |
| No plan, no parameters | `there is no plan; run "<plan>" first` |
| Applied | `plan <id> was already applied at <time>; run "<plan>" again and review the new plan` |
| Failed | `the apply of plan <id> failed at <time>; check the target, then run "<plan>" again and review the new plan` |
| Unfinished | `an apply of plan <id> started at <time> and did not finish, so its outcome is unknown; check the target, then run "<plan>" again and review the new plan` |
| Expired, at the lookup or at the claim | `plan <id> expired at <time>; run "<plan>" again` |
| Replaced, at the claim: a newer plan was stored while the request was pending | `plan <id> was replaced by a newer plan; call "<apply>" again` |
| A bound part differs | `plan <id> no longer matches: the <parts> changed since it was made; run "<plan>" again` |
| The artifact or summary does not verify, at the lookup or at the claim | `the stored <what> of plan <id> <detail>; run "<plan>" again` |
| The claim was lost | `plan <id> was already used by another call` |
| The store cannot be used | `the plan store cannot be used: <reason>` |

`<parts>` is one or more of `Runefile definition`, `configuration`, `working directory`,
`host`, `tool "<name>"`, in this order, joined by `, `. A declared tool that is no longer on
the path counts as changed. `<what>` is `artifact` or `summary`. `<detail>` is `is missing`,
`is not a regular file` or `does not have the recorded digest`.

Two refusals are whole texts of their own:

| When | Text |
|---|---|
| The engine is called with nobody to ask (`Call`, `CallLists`) | `task "<apply>" applies a plan and runs only when a person approves that plan; nobody can be asked on this path` |
| An agent run on the command line | `task "<apply>" applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it` |

## Failures of a plan task

After the body ended without error, as the task's failure:

| When | Text |
|---|---|
| The file or summary cannot be stored | `plan <file\|summary> "<path>" of task "<plan>" <detail>`, with `is missing`, `is not a regular file`, `is a symbolic link` or `is larger than <limit>` |
| The declared tool is not on the path | `plan tool "<name>" of task "<plan>" was not found on the path` |
| The declared tool is on the path and cannot be read | `plan tool "<name>" of task "<plan>" cannot be identified: <reason>` |
| The store cannot be used | `the plan of task "<plan>" was not stored: <reason>` |

A workspace file that cannot be removed after it was stored is a warning,
`plan <id>: could not remove "<path>" from the workspace: <reason>`, and the plan stands.

## The command line

`rune <apply> ...` prints to standard error and asks:

```text
Plan <id> made by "plan" at <time>
Target: apply env="prod"
Artifact: sha256:<hex> (<n> bytes)
Note: <as in the request>
Summary:
  <line>
Apply this plan once? [y/N]
```

The summary is rendered by `SummaryLines` with a head of 48 KiB and a tail of 16 KiB. With
`--yes` the question is not asked. Without a terminal and without `--yes`:
`task "<apply>" was not confirmed (Apply this plan once?): stdin is not a terminal; pass --yes to approve`.
A `[confirm("...")]` message of the apply task is printed as `Confirmation: <message>` above
the question, and the one answer covers it.

An apply task must be the only task named in a command. Otherwise it is the usage error
`a task that applies a plan must be the only task named in a command`.

## Starting a server with a grant for an apply task

`--allow-destructive <apply>` is accepted and has no effect on the plan approval. The server
prints `warning: --allow-destructive <apply> approves no plan: task "<apply>" asks the person at the client on every call`
at startup and on a reload, and the startup line does not list the task among the granted ones.

## A tampered copy

When the private copy's digest differs after the run of the apply call ends, standard error
gets `plan <id>: the plan file changed while it was being applied; recorded as failed and tampered`
and the call fails. A call that would have ended with exit code 0 ends with 1.
