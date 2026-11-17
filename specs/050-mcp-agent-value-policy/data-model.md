# Data Model: Restrict the Argument Values an Agent May Pass to a Task

**Feature**: 050-mcp-agent-value-policy | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

Nothing here is stored. Every structure lives in the parsed Runefile or for the length of one call.

## Predicate (`ast.AllowPredicate`)

| Field | Meaning |
|---|---|
| `Name` | The parameter the predicate names |
| `Value` | The one value an agent run may have for it, decoded from the string literal |
| `NameSp` | Span of the name, for `RUNE2019` |
| `ValueSp` | Span of the literal, for `RUNE2019` |

Held in source order in `ast.Attribute.Allow` of an attribute whose `Kind` is `agent-allow`.

Static rules (analyzer, [contracts/agent-allow-attribute.md](contracts/agent-allow-attribute.md)):

| Rule | Otherwise |
|---|---|
| At least one predicate, each `name="literal"` | Syntax error (`RUNE1001`) |
| `Name` is a parameter of the task | `RUNE2019` |
| `Name` appears once in the attribute | `RUNE2019` |
| The parameter is not variadic | `RUNE2019` |
| The parameter is unannotated, `string` or `enum` | `RUNE2019` |
| For an `enum` parameter, `Value` is one of its values | `RUNE2019` |
| Every character of `Value` is printable | `RUNE2019` |
| One `[agent-allow]` per task | `RUNE2019` |

## Policy (`ast.Task.AgentPolicy()`)

The predicates of the task's first `[agent-allow]`, or nil. A task with a policy is a restricted
task. A run of it satisfies the policy when `args.Values[p.Name] == p.Value` for every predicate
(FR-007, FR-008). The comparison is on the raw strings.

## Agent run (`engine.agent`)

| Engine built by | `agent` |
|---|---|
| `execute` (command line) | false, or true when `RUNE_AGENT_RUN=1` is in its environment |
| `mcpAdapter.call` for `rune mcp` and `rune serve` | true |
| `mcpAdapter.call` for the endpoint of an `agent` task | true |
| `mcpAdapter.call` for the `[context]` hook | true |

The field belongs to one engine and so to one call. An agent engine puts `RUNE_AGENT_RUN=1` in the
environment of every body it runs, which is how a `rune` started by a body becomes an agent run.

## The walk (`engine.preflightPolicy`)

Input: the called task and its bound arguments. Output: the first denial, or nil.

State of one walk:

| State | Meaning |
|---|---|
| `seen` | Memo key of each walked run, its mode and its volatile parameters, so a run is walked once |
| scope | A fork of the call's scope: what the walk reads is not cached for the run |
| `reaches` | Per task name: whether it has a policy or can reach a task that has one |
| volatility | Per walked run: which of its parameters could have another value at run time |

Steps for a task, its arguments, and its volatility:

1. Check the task's policy (below).
2. For each edge in order: dependencies, `&&` hooks, and `||` hooks unless this task is walked as a
   `||` hook.
   - Target unknown, unavailable on this host, or `reaches` false: skip.
   - `ResolveDep` fails: denial of kind *unresolved*.
   - Otherwise walk the target with the resolved arguments and its volatility.

Volatility of a target's parameter:

| Parameter gets | Volatile when |
|---|---|
| A written dependency argument | The expression calls `uuid`, `datetime`, `path_exists`, `read`, `sha256_file`, `which`, `require` or `absolute_path`, or refers to a volatile parameter of the caller or to a volatile module variable |
| Its default | The default expression does, or refers to a volatile earlier parameter of the target |
| The variadic slot | Any of its argument expressions does |

Parameters of the called task are never volatile. A module variable is volatile when its
assignment calls one of those functions, directly or through another variable, and no run-time
override replaces it.

## Check of one run

| Finding | Result |
|---|---|
| No policy | Pass |
| A predicate's parameter is volatile | Denial of kind *volatile*, listing those parameters |
| A predicate's value differs | Denial of kind *breach*, listing those parameters with their values |
| Otherwise | Pass |

## Denial (`policyDenial`)

| Field | Meaning |
|---|---|
| kind | *breach*, *volatile* or *unresolved* |
| called | The task the agent called |
| task | The restricted task (breach, volatile), or the edge's target (unresolved) |
| owner | The task whose edge did not resolve (unresolved) |
| refused | The predicates not met, each with the effective value (breach) or without (volatile) |
| policy | Every predicate of the restricted task |
| reason | The evaluation error (unresolved) |

Its text is in [contracts/mcp-tools.md](contracts/mcp-tools.md). Values are masked, cut and quoted
when the text is written, never before a comparison.

## Order of checks for one call

| Step | Where | Result when it fails |
|---|---|---|
| 1. Tool exists | `mcpserver` handler | Protocol error, as today |
| 2. Destructive and cannot be asked | `mcpserver` handler | Destructive refusal, as today |
| 3. Unknown argument names | `mcpserver` handler | As today |
| 4. Value types, required arguments, defaults | `mcpserver` handler, then `bindNamedParams` | As today |
| 5. Value policy of the call and its reach | `mcpAdapter.call`, or `execute` when marked | Policy refusal |
| 6. Approval request | `mcpserver` approver | As today |
| 7. Run, with the policy checked again at each restricted body | scheduler, `engine.Execute` | The task fails and its body does not run |

## Agent-facing parameter (`mcpserver.ParamInfo.AgentValues`)

| Value | Schema property | Description paragraph |
|---|---|---|
| nil or empty | As today | None |
| `["staging"]` on an `enum` parameter | `enum` is `["staging"]` | Names `env="staging"` |
| `["staging"]` on a string parameter | `type: string` and `enum` is `["staging"]` | Names it |

`ParamInfo.check` ignores the field: the server validates a supplied value against the declared
type only.

`ParamInfo.Required` is also true for a restricted parameter whose default is a string literal
other than the policy's value.
