# Research: Reject Unknown MCP Tool Arguments

**Feature**: 032-mcp-unknown-arguments | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

## Baseline (main at `48e26c8`, read from the code and run on a binary built in Docker)

- **Unknown names pass straight through.** `decodeArgs` (`mcpserver/handler.go`) copies
  every key into its result and checks a value only when the key is a declared parameter.
  `bindNamedParams` (`internal/cli/args.go`) loops over the task's parameters, so an extra
  key is never read.
- **Observed results.** `build(targte="release")` returns `built debug` and `[exit 0]`.
  `deploy(env="prod", replicaz="5")` returns `deployed prod x2` and `[exit 0]`.
  `deploy(evn="prod")` returns `task "deploy" is missing required argument "env"`.
- **Schemas never restrict names.** `inputSchema` (`mcpserver/server.go`) sets `type`,
  `properties`, and `required`. It builds a plain map, and the repository has no other
  mention of `additionalProperties`.
- **The SDK does not validate.** The handlers are registered with `Server.AddTool`, whose
  documentation leaves unmarshaling and validation to the caller. `ClientSession.CallTool`
  does not validate either. So nothing upstream rejects a name, and adding the keyword to a
  schema changes no server behavior by itself.
- **One route for agent-supplied arguments.** The handler is the only code that calls the
  engine with arguments from a client (`mcpserver/handler.go`). The only other engine call
  outside tests is the `[context]` hook, with an empty map (`internal/cli/contextprep.go`).
  stdio, HTTP, and the agent-task endpoint all build the same handler through `mcpserver.New`.
- **The check order today.** The handler runs `refusal`, then `decodeArgs` (value checks),
  then the engine (missing-required check in `bindNamedParams`).
- **Non-object arguments.** `[]` and `"x"` return `invalid arguments: json: cannot unmarshal
  ...`. `null`, `{}`, and absent arguments run the task.
- **The suggestion helper.** `nearest` (`internal/cli/suggest.go`) allows at most two edits and
  resolves ties to the first candidate seen. `internal/cli` imports `mcpserver`, so `mcpserver`
  cannot import `internal/cli`.
- **The pending test.** `TestMCPE2E_Pending_UnknownArgumentRejected` is not in this repository.

## R1: Where the check runs

**Decision**: The handler runs `refusal`, then decodes the arguments once (`decodeArgs`), then
checks the names (`unknownArgs`), then converts and checks the values (`stringArgs`). A decode error
gets today's `invalid arguments:` text.

**Rationale**: This position gives the order FR-006 requires without moving any existing check. It
sits in the library, so it holds for every engine (FR-011). The handler closes over the task's own
`TaskInfo`, so the schema and the check read the same parameter list and cannot disagree.

`stringArgs` goes over the task's parameters, not the decoded map. An undeclared name has already
been refused, so nothing needs to handle one, and the same call always reports the same invalid
value.

**Alternatives considered**:

- Decoding separately for the name check, which the first version of this plan did to leave the old
  `decodeArgs` unchanged. Code review pointed out that it parsed every call twice and left branches
  for undeclared names that nothing could reach, so one decode replaced it.
- Inside the value checks. They would need the task name and an exception to the
  `invalid arguments:` prefix the handler adds.
- Inside `bindNamedParams`. It runs after the value checks and only for Rune's own adapter, so
  embedders would stay unprotected and FR-006 would not hold.

## R2: Text and ordering

**Decision**: One line per unknown name, sorted with `slices.Sort`, joined by `\n`, with no
trailing newline. Each line is `unknown argument %q for task %q`, followed by
` (did you mean %q?)` when a close parameter exists. The task name is the Runefile name the
handler already holds.

A name over 64 characters is cut off at a character boundary with `...` and gets no suggestion. After
20 lines, a final line reads `unknown arguments not shown for task %q: %d`.

**Rationale**: FR-002, FR-003, FR-005. The text goes into the calling agent's context, so its size must
not grow with the call. `%q` keeps an empty name or one holding quotes or
newlines on one line. Sorting is needed because JSON objects decode to a Go map, whose order
varies between runs. Only the name is ever formatted, so no value can appear (FR-007). The text
is returned directly through `errorResult`, which adds no `[exit N]` line.

**Alternatives considered**: Reporting only the first name costs one round trip per typo. A
single line listing all names loses the per-name suggestion.

## R3: Sharing the suggestion rule

**Decision**: Move `internal/cli/suggest.go` and its test to a new package, `internal/suggest`,
exporting `Nearest(token string, candidates []string) (string, bool)`. `internal/cli/args.go` and
`internal/cli/serve.go` call it. `mcpserver` passes the task's parameter names in declaration
order, with the parameters the call left out first, so a tie resolves to an unset parameter and then
to the one declared first (FR-004). A local step before `Nearest` handles a case-only difference, since
`ENV` is three edits from `env`. It stays in `mcpserver`, so task-name suggestions on the command line
do not change.

**Rationale**: FR-004 requires the same rule as task-name suggestions, and issue #36 asks to
factor it into a shared helper if needed. It is needed: `mcpserver` cannot import `internal/cli`.
The move changes no behavior, and `TestNearest` moves with it, keeping every case. The new package
is recorded in the plan's Complexity Tracking table, since the constitution locks the package
layout.

**Alternatives considered**:

- A copy in `mcpserver`. Two copies of the rule can drift apart.
- Adding it to `internal/ast`, which `mcpserver` already imports. Edit distance has nothing to do
  with the syntax tree.
- Exporting it from `mcpserver` for `internal/cli` to use. That widens the public library API for
  an internal helper.
- An `Options` callback for suggestions. An embedder that leaves it unset would get no suggestion,
  against FR-011.

## R4: No second check in the engine

**Decision**: `bindNamedParams` does not change.

**Rationale**: The handler is the only route for agent-supplied arguments (see Baseline), and
`mcpAdapter` is unexported, so no embedder can reach it directly. A second check could never fire
in a real run, so it could only be tested by calling it directly.

**Alternatives considered**: Defense in depth, as the engine does for values (spec 023). It adds
unreachable code and a second copy of the message.

## R5: The schema keyword

**Decision**: `inputSchema` sets `"additionalProperties": false` on every schema, in the same map
as `type` and `properties`. Nothing else in the schema changes.

**Rationale**: FR-009. It is one line in the one function that builds every tool's schema, and the
SDK's `AddTool` accepts a plain map schema as long as its `type` is `object`.

**Alternatives considered**: Moving to the SDK's `jsonschema.Schema` type is a larger rewrite with
no benefit for this feature.

## R6: Test layers

Every new test of changed behavior fails on main first (constitution Principle VI). Tests that
guard unchanged behavior (the refusal order in FR-006, and FR-012) pass on main and must stay
green. One existing assertion changes: `TestDecodeArgsLists` pinned an undeclared name reaching the
task's values, which R1 removes on purpose.

| Layer | File | Covers |
|---|---|---|
| Helper | `internal/suggest/suggest_test.go` (moved) | `TestNearest` with every case kept, plus a case past the two-edit limit; ties pick the first candidate; no candidates |
| Library | `mcpserver/handler_test.go` | Exact texts, several names, order against refusal and value checks, a granted destructive task, limits on the text, null and object values, the first declared of two invalid values, no engine call on either engine interface, value never echoed, value shapes, escaped names, `tools::build`, same text over 100 calls, non-object and empty arguments unchanged |
| Library | `mcpserver/server_test.go` | The keyword on every parameter shape; the other schema keys unchanged; `tools/list` through a client |
| Transport | `mcpserver/transport_test.go` | The same refusal over HTTP |
| Adapter | `internal/cli/mcp_test.go` | The agent-task endpoint's server refuses and runs nothing, using the real adapter |
| Binary | `test/integration/unknown_args_mcp_test.go` (new) | Quickstart scenarios over stdio with marker files, including dependencies and hooks, a granted destructive task, an imported task, null and object values, and `tools/list` |
| Docs | `test/docs` | The changed page still validates |

## R7: Docs and release note

**Decision**: In `docs/mcp.md`, extend the "How tasks become tools" bullet on parameters to say the
schema allows no other names. In the paragraph below it, beside the sentence on invalid arguments,
say that a call naming an undeclared argument is refused the same way, and show the refusal text.
That paragraph is where the page introduces the `[exit N]` marker. Use this PR title, since git-cliff
prints only the subject, so everything the release notes must say (FR-014) has to be in it:

```text
fix(mcp)!: breaking: a tool call with an unknown argument or a null or object value now fails instead of running with the wrong value; mcpserver.TaskInfo.Params now lists the accepted names
```

The PR body starts with `Closes #36.` and says an embedder's task descriptors now define the
accepted names. It also names `TestUnknownArgsMCP_Refused` as the replacement for
`TestMCPE2E_Pending_UnknownArgumentRejected`, which the issue lists but this repository does not
have.

**Rationale**: FR-013, FR-014. Spec 029 and spec 031 set the `!` and `breaking:` convention for
behavior that used to succeed.

## R8: Values a task has no way to receive

**Decision**: `stringArgs` refuses null, objects, and nested arrays, alone or as array elements, with
`parameter %q: null is not a valid value` and the matching texts. Strings, numbers, booleans, and
arrays of those keep working. The texts do not echo the value.

**Rationale**: FR-015. `fmt.Sprint` turned these into `<nil>` and `map[k:v]`, and the task ran on that.
A required parameter passed its check too, since `<nil>` is not empty. Code review found this, and
reproducing it on a binary built from this branch confirmed it. It is the same silent wrong result as
issue #36, so it is fixed here.

**Alternatives considered**: Treating null as an absent argument would be friendlier to a client that
sends null for optional fields, but the task would then run with a default the agent never chose, and
the schema does not allow null. Refusing costs one retry.

## Observed, not changed

- `candidateNames` (`internal/cli/args.go`) builds its list from a map, so a tie in the "unknown
  task" suggestion can vary between runs. This feature does not change that.
