# Research: Restrict the Argument Values an Agent May Pass to a Task

Decisions for [plan.md](plan.md). Each was checked against the code on `main` at `808b99c`.

## R1. Where the attribute lives in the syntax tree

**Decision**: Add `ast.AttrAgentAllow = "agent-allow"` to the attribute constants and to
`KnownAttributes`. `ast.Attribute` gains one field, `Allow []*AllowPredicate`. A predicate holds
`Name`, `Value`, `NameSp` and `ValueSp`. `ast.Task` gains `AgentPolicy() []*AllowPredicate`, which
returns the predicates of the task's first `[agent-allow]`, or nil.

**Rationale**: `Attribute` already carries one field set per argument shape (`Str`, `Str2`,
`Inputs`, `Outputs`). Named string predicates fit none of them. The two spans let every `RUNE2019`
point at the name or the value that is wrong (Principle II). One accessor keeps the "first attribute
wins, a second is an error" rule in one place for the analyzer, the engine and the tool listing.

**Alternatives considered**: Reusing `Str`/`Str2` for one predicate. It cannot hold two, and the
issue requires `[agent-allow(env="staging", region="eu-west-1")]`. Storing predicates as `ast.Expr`
like `[cache]`. Values must be literals, and an expression would have to be rejected again later.

## R2. Parsing

**Decision**: `parseAttrItem` gets a case for `agent-allow` that calls `parseAgentAllowArgs`:
`(` , then one or more `IDENT "=" STRING` separated by `,`, then `)`. Each missing piece is reported
through `expect`, so it is `RUNE1001` at the token found. `[agent-allow]`, `[agent-allow()]`,
`[agent-allow(env)]`, `[agent-allow(env=prod)]` and a trailing comma are syntax errors.

**Rationale**: It is the shape `parseCacheArgs` already parses for `inputs = [...]`, with a string
in place of the list. `=` after a name lexes as `EQUALS` with or without spaces, as it does in a
parameter default. Parameter names may contain `-`, and the lexer's identifier rule already allows
it. `TestParserKnowsAllASTAttributes` feeds the bare name and only requires that the parser not call
it unknown, which holds.

**Alternatives considered**: Accepting the empty list and reporting it in the analyzer. A parse
error is earlier and needs no new rule.

## R3. Static validation and the diagnostic code

**Decision**: New code `CodeInvalidAgentAllow = "RUNE2019"` and a new analyzer pass
`checkAgentAllow`, run after `checkConstraints`. Per task, in source order:

| Condition | Position | Message |
|---|---|---|
| A second `[agent-allow]` on the task | the second attribute, related: the first | `duplicate [agent-allow] on task "<task>": put every predicate in one attribute` |
| Name is not a parameter of the task | the name | `agent-allow names unknown parameter "<name>" of task "<task>"`, plus ` (did you mean "<param>"?)` when one is close |
| Name already used in this attribute | the second name | `duplicate agent-allow predicate for parameter "<name>"` |
| Parameter is variadic | the name | `agent-allow cannot restrict variadic parameter "<name>": only string and enum parameters are supported` |
| Parameter kind is `number`, `boolean`, `path` or `raw` | the name | `agent-allow cannot restrict parameter "<name>" of type <kind>: only string and enum parameters are supported` |
| Parameter is an `enum` and the value is not in it | the value | `agent-allow value "<value>" is not an allowed value of parameter "<name>" (allowed values: "a", "b")` |
| The value holds a character that is not printable | the value | `agent-allow value for parameter "<name>" holds a character that is not printable` |

A predicate on a parameter whose kind name is unknown is left to `RUNE2012`. Predicates of a
second attribute are not checked further: the duplicate is the one error to fix.

**Rationale**: One code with distinct messages is how `RUNE2015` and `RUNE2018` cover several
conditions of one feature. `internal/suggest.Nearest` is what the server uses for a misspelled
argument, so the hint is the same kind an agent gets at call time.

**Alternatives considered**: One code per condition. Six codes for one attribute, with no reader
that would treat them differently. Reusing `RUNE2007` (invalid attribute). It is what the parser
emits for an unknown attribute name, and the issue asks for documented codes for these conditions.

## R4. Where a call is checked

**Decision**: In the engine adapter, `internal/cli/mcp.go`, inside `mcpAdapter.call`: after
`bindNamedParams` returns the effective arguments and before the approval request and
`scheduler.Run`. The check and its texts live in a new file, `internal/cli/agentpolicy.go`. The
handler in `mcpserver/handler.go` does not change.

The order a call meets, unchanged up to step 4:

1. The tool exists in the snapshot.
2. The destructive refusal, when the call can run an ungranted `[confirm]` task and the person
   cannot be asked (specs 031, 034, 049). Before the arguments are read, as today.
3. Unknown argument names (spec 032).
4. Supplied values against the parameter types (spec 023). In the engine: missing required
   arguments, defaults evaluated and checked.
5. **New**: the value policy of the called task and of everything the call can reach.
6. The approval request, when the call needs one (spec 049).
7. The run. **New**: each restricted task is checked again where its body would start.

**Rationale**: The policy needs the bound defaults, and only the engine evaluates them. The refusal
has to be masked, and only the adapter holds the secret set. `call` is the one function every agent
path goes through: `Call`, `CallLists`, `CallApproved`, and `gatherContext` for the `[context]`
hook. The adapter built by `newAgentAdapter` for an `agent` task's endpoint uses the same function,
so the nested case needs no extra wiring. Step 5 sits before step 6, so nobody is asked about a
call the policy refuses, and a grant never reaches a refused value because grants are read at step
7's `[confirm]` gate, after the policy.

Step 2 stays where it is: specs 031 and 049 pin that a destructive refusal comes before the
arguments are read, with texts held by tests. Moving it would change those results for no gain,
since in either order nothing runs.

**Alternatives considered**: Checking supplied values in `mcpserver/handler.go` too. The server
package knows no secret, so its refusal could not be masked, and it would fire before the engine's
masked one. It also cannot see defaults, so it would be a partial check that reads as a full one.
Checking in `mcpserver/authz.go`. Same limits.

## R5. Agent provenance

**Decision**: `engine` gains a field `agent bool`. `mcpAdapter.call` sets it on the engine it
builds. `execute`, the command-line path, never sets it. Nothing else builds an engine for a run.

**Rationale**: Every engine an agent's call runs on is built in `mcpAdapter.call`, and no
command-line run goes through it. The field is per engine, so it ends with the call, like
`approved`. Dependencies, hooks and module tasks run on the same engine, so they inherit it.

An `agent` task reached during an MCP call opens no endpoint: `call` leaves `engine.file` nil, and
`executeAgent` opens one only when it is set. An `agent` task run by a person opens an endpoint whose
adapter is an `mcpAdapter`, so its calls are agent runs. Both cases are covered by the one field.

## R6. Checking what a call can reach, before it starts

**Decision**: `engine.preflightPolicy(root, params)` walks what the scheduler would run and returns
the first denial, or nil. For a task and its bound arguments it:

1. Checks the task's own policy against the arguments.
2. For each edge, in the scheduler's order (dependencies, `&&` hooks, then `||` hooks unless the
   task is itself being walked as a `||` hook): skips a target that is unknown, unavailable on this
   host, or that cannot reach a restricted task. Otherwise it resolves the edge with
   `engine.ResolveDep`, the function the scheduler uses, and walks the target with the arguments
   that returned.

A visited set keyed by the scheduler's memo key, the walk mode and the set of volatile parameters
stops repeats. The volatile set is in the key because two edges can resolve to the same arguments
at walk time while only one of them is volatile. Whether a task can
reach a restricted task is a memoized walk over `Task.Edges()` by name. When the called task is not
restricted and reaches no restricted task, the walk is skipped entirely.

**Rationale**: `ResolveDep` evaluates the dependency's argument expressions in the caller's scope
and binds them with `bindParams`, so the walk sees the arguments the run would see, variadic
spreading and defaults included. The expression language runs no command (Constitution III), so
resolving has no side effect. Skipping subtrees with no restricted task keeps an unrelated
evaluation error where it is today, in the run, and keeps a Runefile without the attribute on its
old path with no new work (SC-005). The edge order and the hook rule are those of
`mcpAdapter.reach`, which spec 034 fixed.

**Alternatives considered**: A static check by task name, as `DestructiveReach` does. It cannot
tell `(deploy "staging")` from `(deploy "prod")`. Checking only at run time. A wrapper would run its
earlier dependencies before the denial, which the issue rules out.

## R7. Arguments that can differ between the check and the run

**Decision**: The walk tracks, per parameter of each walked task, whether its value is volatile. A
value is volatile when the expression that produced it calls one of `uuid`, `datetime`,
`path_exists`, `read`, `sha256_file`, `which` or `require`, or refers to a volatile parameter of the
calling task. The called task's own parameters are never volatile. A restricted task whose
predicate names a volatile parameter is a denial: the call is refused with a text that names the
parameter and the policy (FR-018).

A module variable is volatile when its assignment calls one of those functions, directly or
through another variable, unless a run-time override replaces it.

An edge toward a restricted task whose arguments fail to resolve is also a denial, with the
evaluation error in the text. That holds whichever parameter the failing argument is for:
`ResolveDep` binds all of a target's parameters or none, and a target that is not bound cannot be
walked further.

The walk resolves on a fork of the call's scope: the same bindings, with a copy of the
variable cache as it stands after the called task was bound. What the walk reads is cached in the
fork and thrown away.

Every built-in function is in one of two lists, volatile or stable, and a test holds the two
lists to `eval.BuiltinNames()`, so a new function cannot be added without choosing.
`absolute_path` is volatile: it depends on the server's working directory.

**Rationale**: The called task is bound once in `call`, and the run uses those same arguments, so
they cannot differ. A dependency's arguments are evaluated again when the run reaches it. Each
function in the list can return something else then: a new random value, a later time, or the state
of a file an earlier task of the same run changed. `env`, `os`, `arch` and `num_cpus` read state a
task cannot change for the server process.

The scope caches a module variable the first time it is read. If the walk shared that cache, it
would fix `version := read("VERSION")` before a `bump` dependency rewrote the file, and an allowed
call would deploy the stale version (FR-018a). So the walk reads on a fork, and a variable whose
assignment is volatile counts as volatile: the run may read it later and get another value. A
variable already cached when the called task was bound has its value fixed for the call, but the
walk does not look: refusing is the safe answer and the case is rare.

Volatility is per parameter, so `wrapper id: (deploy "staging")` called with any `id` passes, and
`wrapper: (deploy if path_exists(".prod") == "true" { "prod" } else { "staging" })` is refused.

**Alternatives considered**: Caching the walk's results and making the run reuse them. Every
dependency argument of the call would then be evaluated before the run, which changes what
`path_exists` sees for tasks that have nothing to do with a policy. Refusing only on a breach found
by the walk. A volatile argument could pass the walk and breach at run time, after other tasks ran.

## R8. The second check, where a body would start

**Decision**: `engine.Execute` and `engine.ExecuteFailHook` check the task's policy against the
arguments they were given when `engine.agent` is set, before the `[confirm]` gate. On a breach the
body does not run: the text goes to the engine's stderr, which is the masked writer, and the task
fails with the body-not-run marker so `||` hooks do not fire for it.

**Rationale**: FR-020. The walk of R6 and R7 should make this unreachable. It is the guarantee that
a restricted task never runs with a refused value if the walk and the scheduler ever disagree. It
costs one map lookup per task of an agent run.

## R9. The refusal texts

**Decision**: A denial is an `error` returned from `call`, which the handler turns into an error
result with text only, as for every call that ran no task. The texts:

| Case | Text |
|---|---|
| The called task breaks its policy | `task "<task>" is not allowed for agents with <name>=<value>[, ...]; agent policy requires <name>=<value>[, ...]` |
| A reached task breaks its policy | `task "<called>" runs "<task>", which is not allowed for agents with <name>=<value>[, ...]; agent policy requires <name>=<value>[, ...]` |
| A reached task's value is volatile | `task "<called>" runs "<task>" with a value of <name>[, ...] that is not known before the run; agent policy requires <name>=<value>[, ...]` |
| An edge toward a restricted task does not resolve | `task "<called>" was not run: the arguments "<owner>" passes to "<target>" could not be worked out before the run (<reason>), and an agent policy has to be checked on that path` |

The first list holds only the parameters whose value was refused, in the order the attribute names
them. The second list is the whole policy, in the same order. A value is rendered by masking it with
the adapter's secret set, cutting it to 64 characters, and quoting it with `strconv.Quote`, with
`...` after the closing quote when it was cut. `<reason>` is the evaluation error, masked, escaped
to one line and cut to 200 characters.

**Rationale**: The first text is the issue's example. The others keep its two halves, what was
refused and what is required, and add the task the agent called, because the agent has to know which
of its calls to change. `strconv.Quote` keeps a value on one line and inside its quotes. Masking
uses `mask.Set.MaskString`, as the approval request does. Values are compared before any of this:
rendering never decides a check.

## R10. What the agent sees

**Decision**: `mcpserver.ParamInfo` gains `AgentValues []string`: the only values an agent may pass,
empty when the parameter has no restriction. `inputSchema` sets the property's `enum` to it when it
is not empty, for an `enum` parameter in place of the full list, for a string parameter as a new `enum`.
`toolFor` appends a paragraph to the description when any parameter has one:

```text
Agent policy: agents may run this task only with env="staging" and region="eu-west-1".
```

`mcpAdapter.Tasks()` fills the field from `Task.AgentPolicy()`. `ParamInfo.check` does not read it:
the server keeps validating a supplied value against the parameter's declared type, so `prod`
reaches the engine and gets the policy refusal, not a type error.

`Tasks()` also sets `Required` for a restricted parameter whose default is a string literal other
than the policy's value: an agent has to send it, and `required` is how a schema says so (SC-008).
A default that is an expression is left optional, since its value is not known when tools are
listed. `Required` only feeds the schema: the engine still answers a call without the argument
with the policy refusal.

**Rationale**: The issue asks for an agent-facing enum narrowed to the allowed value, with the full
enum kept for normal validation. A slice, not a single string, tells "no restriction" from "the
empty string", and leaves room for several values without a second field. An empty slice is no
restriction: an `enum` with no value is not a valid schema. `enum` on a string
property is plain JSON Schema that every client already reads for enum parameters. The paragraph
goes last so a tool without a policy is byte for byte what it was. Values are Runefile literals,
like enum values, so no environment value can reach the schema (Principle VII).

**Alternatives considered**: `const`. Clients that render forms from schemas handle `enum` more
widely. Putting the policy in each property's description. The issue names the tool description,
and the property already shows the one value.

## R11. Reload

**Decision**: No new code. `Server.Replace` compares the JSON of each tool, so a changed policy
changes the tool and sends `tools/list_changed`. A call reads its tasks from the adapter of the
snapshot it started with, so the walk and the run use one Runefile load.

## R12. Language tooling

**Decision**:

- `internal/language/builtin.go`: an `agent-allow` entry, signature
  `[agent-allow(param="value")]`, so completion and hover know it. `TestRegistryMatchesASTAttributes`
  holds it to `KnownAttributes`.
- `internal/formatter`: `[agent-allow(env="staging", region="eu-west-1")]`, predicates in source
  order, no spaces around `=`, as parameter defaults are written.
- `internal/ast/dump.go`: `Attr agent-allow(env="staging", region="eu-west-1")`.
- `editors/vscode/syntaxes/runefile.tmLanguage.json`: `agent-allow` joins the attribute names.
- The analyzer's diagnostics reach the editor through the existing analysis service.

**Rationale**: These are the places `[readonly]` and `[param-doc]` had to be added (specs 023, 033).

## R13. Tests

**Decision**: Test-first, by layer (Principle VI):

| Layer | File | Covers |
|---|---|---|
| Parser | `internal/parser/agent_allow_test.go` | The predicate list, spans, each syntax error |
| AST | `internal/ast/agent_allow_test.go` | `AgentPolicy`, dump |
| Corpus | `testdata/corpus/agent-allow.rune` and `.ast` | The attribute's tree, generated with `-update` |
| Formatter | `internal/formatter/formatter_test.go`, `testdata/fmt` | Canonical form, idempotence |
| Analyzer | `internal/analyzer/agent_allow_test.go` | Every `RUNE2019` text and span, valid cases |
| Language | `internal/language` registry test | The registry entry |
| Server library | `mcpserver/server_test.go` | Narrowed schema, description paragraph, unchanged tool |
| Engine | `internal/cli/agentpolicy_test.go` | Texts, masking, cutting, defaults, the walk, volatility, the second check, the command-line path |
| Binary | `test/integration/agent_policy_mcp_test.go` | The quickstart rows on stdio and HTTP with marker files, approval and grant, a body that runs `rune`, the command line, `rune analyze` |
| Docs | `test/docs` | Links and code blocks of the changed pages |

The suite runs in Docker only. Goldens are regenerated with `-update`, never edited by hand.

## R14. Documentation

**Decision**:

- `docs/runefile.md`: the attribute in the list under "Attributes", and a paragraph on what it
  does, which parameters it can name, the "and" rule and `RUNE2019`.
- `docs/GRAMMAR.md`: the `AttrItem` alternative.
- `docs/diagnostics.md`: the `RUNE2019` row.
- `docs/mcp.md`: a new section "Restricting the values an agent may pass" after "Approving a
  destructive call in the client", a sentence in the security model, and the narrowed schema in
  "Typed tool schemas".
- `SECURITY.md`: one sentence naming the control and its limit.

## R15. A task body that runs `rune`

**Decision**: `mcpAdapter.call` adds `RUNE_AGENT_RUN=1` to the environment of the engine it builds,
so every body of an agent run has it. `execute`, the command-line path, sets `engine.agent` when
its own environment has that value, and then runs `preflightPolicy` on each task it was asked to
run, after binding and before `scheduler.Run`. A denial is a task failure: the text on stderr
through the usual banner, masked, and exit status 1. The second check of R8 applies there too.

The agent program of an `agent` task that runs inside an agent run inherits the mark like any
body: removing it there would loosen the check for no gain. An `agent` task a person runs has no
mark. Either way nothing is promised for that program's own shell.

**Rationale**: `ship:` with the body `rune deploy prod` has no edge to `deploy`, so the walk cannot
see it, and the inner `rune` would otherwise be a command-line run. The body is the author's text
and the agent controls only argument values, so an environment variable the body inherits is a
sound carrier: the agent has no way to clear it. `[confirm]` already survives this shape, because
the inner run has no terminal. The outer task's earlier work has run by then, which no check can
prevent: Rune does not read bodies.

An agent CLI started by an `agent` task has its own shell and could clear the variable, so the mark
is no promise there. That is issue #46.

**Alternatives considered**: Documenting the shape as a limit. It is the most likely way a real
Runefile calls a task with arguments chosen in a body, and the issue asks that a permitted wrapper
not be a workaround. Scanning bodies for `rune`. Bodies are shell, Python or Node text, and Rune
does not parse them.

## R16. Values the formatter cannot write back

**Decision**: A policy value with a character that is not printable, or that is not valid UTF-8, is
`RUNE2019`. The formatter and the dumper write a policy value with their own quoting rule,
`ast.FormatAllow`, which uses only the escapes the lexer reads back (`\\`, `\"`, `\n`, `\t`, `\r`,
`\0`) and writes every other byte as it is.

**Rationale**: `strconv.Quote`, which the formatter uses for other attributes, escapes such
characters as `\x..` or `\u....`. The lexer decodes only `\n`, `\t`, `\r` and `\0`, so `rune --fmt`
would change the value. For other attributes that changes a message. Here it would change who may
run what. Rejecting the value is not enough by itself: `--fmt` and the editor's formatter run
without the analyzer, so they would turn a value the analyzer rejects into another one that
passes. With the quoting rule, formatting never changes a policy value, and the error stays until
the author fixes it. A policy on an invisible character has no use, so it stays an error.

## R17. Commit

**Decision**: One commit, type `feat(mcp)`. Not breaking: a Runefile without the attribute behaves
as before, and the new field of the server library is optional. The subject follows the changelog
style of the repository: what a user can now do.
