# Contract: Tools and Call Results Under a Value Policy

**Feature**: 050-mcp-agent-value-policy | Covers FR-009 to FR-031

Applies to `rune mcp`, `rune serve` over stdio and Streamable HTTP, and the endpoint an `agent`
task opens. Examples use the Runefile of [quickstart.md](../quickstart.md).

## `tools/list`

For a parameter a policy names, the property's `enum` is the policy's value alone:

```json
{
  "name": "deploy",
  "description": "Deploy the service.\n\nAgent policy: agents may run this task only with env=\"staging\".",
  "inputSchema": {
    "type": "object",
    "properties": {"env": {"type": "string", "enum": ["staging"]}},
    "required": ["env"],
    "additionalProperties": false
  }
}
```

- A string parameter with a policy gets `"type": "string"` and the same one-value `enum`.
- A required parameter stays required. A restricted parameter whose default is a string literal
  other than the allowed value is listed in `required` too. One whose default is the allowed
  value, or an expression, stays optional.
- The paragraph is the last one of the description, after `Returns:` when the task has one. With
  several predicates: `only with env="staging" and region="eu-west-1"`, in the attribute's order.
- A tool whose task has no policy is unchanged, byte for byte.
- Hints, the output schema and the tool name do not change.

## `tools/call`

A policy refusal is a result with `isError: true`, one text block, no structured content and no
exit code. Nothing ran and no approval request was sent.

| Case | Text |
|---|---|
| The called task breaks its policy | `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"` |
| Two refused values | `task "rollout" is not allowed for agents with env="prod", region="us-east-1"; agent policy requires env="staging", region="eu-west-1"` |
| One refused of two | `task "rollout" is not allowed for agents with region="us-east-1"; agent policy requires env="staging", region="eu-west-1"` |
| A reached task breaks its policy | `task "ship" runs "deploy", which is not allowed for agents with env="prod"; agent policy requires env="staging"` |
| A reached task's value is volatile | `task "nightly" runs "deploy" with a value of env that is not known before the run; agent policy requires env="staging"` |
| An edge toward a restricted task does not resolve | `task "broken" was not run: the arguments "broken" passes to "deploy" could not be worked out before the run (<reason>), and an agent policy has to be checked on that path` |

Rules:

- The first list holds the parameters whose value was refused, the second every predicate, both in
  the attribute's order.
- A value is masked with the secret set of task output, cut to 64 characters, and written as a Go
  quoted string, so it stays on one line. A cut value is followed by `...` after the closing quote.
- `<reason>` is the evaluation error: masked, on one line, at most 200 characters.
- The unresolved refusal is given whichever parameter the failing argument is for.
- The effective value is shown: the argument sent, or the default the task would use.
- One refusal per call: the first in run order, the called task first, then dependencies, `&&`
  hooks and `||` hooks, depth first.
- The same call on the same Runefile load gets the same text.

## Order

1. Unknown tool: protocol error, as today.
2. The call can run an ungranted `[confirm]` task and the person cannot be asked: the destructive
   refusal, before the arguments are read, as today.
3. Unknown argument names, as today.
4. Value types, missing required arguments and defaults, as today.
5. The value policy: the refusal above.
6. The approval request, when the call needs one. Its text and outcomes are those of spec 049.
7. The run.

A grant with `--allow-destructive` and an approval act at steps 2, 6 and 7. Neither is read at
step 5.

## A task body that runs `rune`

Every body of an agent run has `RUNE_AGENT_RUN=1` in its environment. A `rune` started with it
treats the tasks on its command line as an agent run: after binding their arguments and before
running anything, it checks them and what they reach. On a breach it runs nothing, prints the
refusal with the usual error banner, and exits with status 1:

```text
rune: task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"
```

The texts are those of the table above, with the task named on the command line as the called
task. The agent program of an `agent` task that runs inside an agent run inherits the variable
like any body. Rune does not set it for an `agent` task a person runs, and promises nothing for
the agent program's own shell, which can clear it.

## During the run

Each restricted task is checked again where its body would start. If that check fails, the body
does not run, the text of the first row goes to the call's stderr once, the task fails, and its
`||` hooks do not fire. For a restricted task run as a `||` hook, the text is in the scheduler's
warning that the hook failed. The walk before the run is meant to make this unreachable.

## Not changed

- A call on a Runefile with no `[agent-allow]`.
- Command-line runs, `--yes`, and the terminal prompt of `[confirm]`.
- Which tasks are tools.
