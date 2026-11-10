# Research: Truthful MCP Tool Hints

**Feature**: 033-mcp-tool-hints | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

## Baseline (main at `edd5208`, read from the code and run on a binary built in Docker)

- **The hints.** `toolFor` (`mcpserver/server.go`) sets `DestructiveHint` from `TaskInfo.Destructive`,
  `OpenWorldHint` from `TaskInfo.Network`, and `ReadOnlyHint` from `!destructive`. The adapter
  (`internal/cli/mcp.go`) fills both fields from the task's own attributes.
- **Observed results.** `tools/list` gives `readOnlyHint: true` for every task without `[confirm]`,
  and `destructiveHint: false` and `openWorldHint: false` for tasks whose only `[confirm]` or
  `[network]` is on a dependency, `&&` hook, or `||` hook. The spec's Overview shows the table.
- **The SDK's types.** `ToolAnnotations.ReadOnlyHint` is a `bool` with `omitempty`, so false is left
  out of the reply. `DestructiveHint` and `OpenWorldHint` are pointers, documented as defaulting to
  true. The SDK documents `DestructiveHint: false` as "performs only additive updates".
- **Who reads the descriptor.** `Destructive` has two readers: `toolFor` and `isDestructive`
  (`mcpserver/authz.go`), which refuses the call. `Network` has one reader: `toolFor`.
- **The edge set.** `ast.Task.Edges()` returns dependencies, `&&` hooks, and `||` hooks.
  `checkContextClosure` and `checkFailHookClosure` already walk it.
- **Host OS skipping.** The scheduler skips a dependency or post-hook whose task is not `Available`,
  and warns and skips such a failure hook. A skipped task's own edges never run. The adapter holds
  the injected `goos` that `Tasks()` and the engine both use.
- **Module names.** `namespaceInto` rewrites intra-module references to `mod::task`, and the
  adapter's `tasks` map holds the composed names, so a reference resolves the same way everywhere.
- **What static errors stop.** A `RUNE2007` file exits 3 on running a task, `--list`, `--dump`,
  `--summary`, `--dry-run`, and `rune mcp`. `--fmt` runs no analysis, so it exits 0 and formats the
  file. `rune analyze` prints `error[RUNE2007]:`, and the run path prints `error:` with the same text.
- **The attribute plumbing.** `[readonly]` is `unknown attribute "readonly"` (`RUNE2007`) today. A
  flag attribute is one case in the parser's switch (`internal/parser/attribute.go`), guarded by
  `TestParserKnowsAllASTAttributes` and `TestRegistryMatchesASTAttributes` against
  `ast.KnownAttributes`. `[network("x")]` is `RUNE1001`, and `[network]` twice is accepted.
  `formatAttr` prints a bare attribute by its kind, one per line, with no per-kind case needed.
- **Tests.** Nothing pins `ReadOnlyHint`. `TestToolAnnotations` pins the other two for a task's own
  flags. `TestHTTPToolsListMatchesStdio` compares whole tool structs, annotations included.
- **The pending test.** `TestMCPE2E_Pending_HintsCoverDependencies` is not in this repository.
- **The stale claim.** "Read-only by default", or the same claim in other words, is in `docs/mcp.md`,
  `docs/use-cases/mcp-agents.md`, `docs/user-guide/README.md`, `docs/examples/agent-driven/Runefile`
  and its `README.md`, `README.md`, `SECURITY.md`, and `website/src/landing/index.mdx`, and in
  Principle VII.

## R1: The attribute

**Decision**: Add `ast.AttrReadonly = "readonly"` to `KnownAttributes`, to the parser's no-argument
case, to the language registry, and to the VS Code grammar's attribute list. The name is the one
issue #37 gives. The formatter needs no change.

**Rationale**: FR-001 and FR-015. This is the path `[network]` took, and the two guard tests fail
until the parser and the registry both know it.

**Alternatives considered**: `[read-only]`, which matches hyphenated attributes like `[no-cd]`. The
issue names `[readonly]`, and the MCP hint is one word.

## R2: The static rule

**Decision**: A new `checkReadonly` in `internal/analyzer/analyzer.go`, run after `checkContext`, with
a new code `CodeInvalidReadonly = "RUNE2018"`. For each task with `[readonly]`:

1. With `[confirm]` on the same task, report at the `[readonly]` attribute.
2. For each entry in `Edges()` whose target exists and has no `[readonly]`, report at that reference,
   with the `[confirm]` text when the target has `[confirm]` and the other text otherwise.

It checks direct edges only and does not walk. Unknown targets are left to `checkDependencies`.

**Rationale**: Every `[readonly]` task's edges must lead to `[readonly]` tasks, so by induction its
whole closure is `[readonly]`. Each diagnostic sits at the one reference that needs the fix, and a long
chain gets one error rather than one per ancestor (spec US3 scenario 5). It ignores OS attributes,
so the result is the same on every host (FR-009). A target carrying both `[readonly]` and `[confirm]`
passes the edge check and gets its own error from step 1, which avoids a second message about one
fault.

**Alternatives considered**:

- `RUNE2007`, the code `[context]` conflicts use. The issue asks for a new code.
- A closure walk like `checkContextClosure`. It reports the same fault once per ancestor and needs
  text for the path between them.
- Only rejecting `[readonly]` with `[confirm]`, as the issue states, with `readOnlyHint` computed from
  the closure. A task whose author wrote `[readonly]` would silently lose the claim, or the static
  rule would have to become a warning that only `rune analyze` shows. The spec's Assumptions cover
  this.

## R3: Where reach is computed

**Decision**: A `reach(t)` method on `mcpAdapter`. It walks `t.Edges()` breadth-first from `t` with a
visited set, as `checkContextClosure` does. It does not follow a target that is missing from
`a.tasks` or not `AvailableOn(a.goos)`. It returns whether any reached task has `[confirm]` and
whether any has `[network]`. `Tasks()` calls it once per task.

**Rationale**: FR-005 and FR-006. The walk needs the task graph and the host OS. The adapter has both,
and the library has neither, since embedders' engines may have no graph at all. `Edges()` keeps this
in step with the scheduler if a new clause kind is added. A body is never read (FR-014).

The `||` edge is included though the issue names only dependencies and post-hooks, because a failure
hook runs as part of a call. The spec's Assumptions cover this.

**Alternatives considered**:

- Computing it in the analyzer. Hints depend on the host OS, and analysis must not.
- Caching the result on the adapter. `Tasks()` runs at server start and once per call, through
  `isDestructive`. Principle VIII says no optimization without a profile, and a typical Runefile has
  tens of tasks.
- Ignoring OS attributes, so hints match on every host. `dispatch: wipe-win` would claim to be
  destructive on Linux, where it runs no such task, against FR-006.

## R4: The descriptor

**Decision**: `TaskInfo` gains two fields:

- `ReadOnly bool`: the task declares itself read-only. It sets `ReadOnlyHint`.
- `ReachesDestructive bool`: a run of this task can reach a destructive task through its dependencies
  or hooks. It sets `DestructiveHint`, and authorization ignores it.

`Destructive` stays "the task itself is destructive". `Network` widens to "the task, or anything a run
of it can reach, uses the network", with no new field. See the
[API contract](contracts/mcpserver-api.md).

**Rationale**: FR-010 and FR-012. `isDestructive` refuses a call when `Destructive` is true, so widening
that field would refuse `release` outright and change the result the issue says stays with spec 031.
`Network` has no such reader, so a second field for it would add API with nothing to distinguish.
Existing keyed descriptors keep compiling. The one behavior change for an embedder is the intended
one: no read-only claim without `ReadOnly`.

**Alternatives considered**:

- Widening `Destructive` and adding a separate field for authorization. Every embedder's existing
  `Destructive: true` would stop being refused unless it set the new field, which fails open.
- A nested `Hints` struct. It moves `Destructive` and `Network` for no behavior gain, and breaks
  every embedder at compile time.
- `ReachesNetwork`, for symmetry. No reader needs the split.

## R5: Deriving the hints

**Decision**: `toolFor` sets `ReadOnlyHint: t.ReadOnly`, `DestructiveHint` to
`t.Destructive || t.ReachesDestructive`, and `OpenWorldHint` to `t.Network`. Both pointer hints stay
set on every tool, so undeclared tasks keep an explicit `false`.

**Rationale**: FR-002 to FR-004. Leaving the pointer hints unset would mean the MCP default of true.
The spec's Assumptions say why they stay explicit. The library trusts a descriptor and does not
reject `ReadOnly` with `Destructive`: the analyzer guards Rune's own path, and authorization still
refuses a destructive task whatever `ReadOnly` says.

## R6: Every route gets the same hints

**Decision**: No code beyond R3 to R5. stdio, HTTP, and the agent endpoint all build tools through
`mcpserver.New`, `Tasks()`, and `toolFor`. Tests cover each route (R9).

**Rationale**: FR-011. Grants live in `Options` and the adapter's `grants`, and neither is read by
`toolFor` or `reach`, so a grant cannot change a hint (FR-010).

## R7: Docs and release note

**Decision**:

- `docs/mcp.md`: a "Tool hints" part under "How tasks become tools" with one row per hint, and text
  saying hints are advisory, that `[readonly]` is the author's claim and the body is not checked, and
  that a task without it makes no read-only claim. Update the "Network tasks are flagged" bullet.
  `status` in the security example gets `[readonly]` and a comment that does not call it safe by
  default.
- `docs/GRAMMAR.md`: the attribute. `docs/runefile.md`: the attribute, and the wider meaning of
  `[confirm]` and `[network]` for hints.
- `docs/diagnostics.md`: a `RUNE2018` row.
- `docs/use-cases/mcp-agents.md`, `docs/examples/agent-driven/Runefile` and `README.md`: `list-tasks`
  gets `[readonly]`, and the header and callouts say destructive tasks are refused unless the operator
  allows them.
- `README.md`, `SECURITY.md`, `docs/user-guide/README.md`, `website/src/landing/index.mdx`: replace the
  claim with the refusal posture.
- PR title, since git-cliff prints only the subject, so everything the release notes must say (FR-017)
  has to be in it:

```text
fix(mcp)!: breaking: only tasks marked with the new [readonly] attribute claim to be read-only, so add it where true, and set mcpserver.TaskInfo.ReadOnly in embedders; destructive and network hints now cover dependencies and hooks
```

The PR body starts with `Closes #37.` and names `TestToolHintsMCP_CoverDependencies` as the replacement
for `TestMCPE2E_Pending_HintsCoverDependencies`, which the issue lists but this repository does not
have.

**Rationale**: FR-015 to FR-017. Specs 029, 031, and 032 set the `!` and `breaking:` convention.

## R8: Principle VII

**Decision**: A MINOR amendment, v1.2.0 to v1.3.0, in the same PR. In Principle VII, replace

> The agent surface is read-only by default; destructiveness is author-declared via the `[confirm]`
> attribute, which maps to the MCP destructive hint.

with

> Destructive tasks are refused on the agent surface by default, and a task makes no read-only claim
> unless its author declares one. Destructiveness is author-declared via the `[confirm]` attribute
> and read-only via `[readonly]`, which map to the MCP destructive and read-only hints.

Update the Sync Impact Report and the footer. `plan-template.md` references the principles generally and
needs no change.

**Rationale**: The commitment stays: secure by default, with authors declaring. The old sentence was
never true of a task without `[confirm]`, which can run and write through the agent surface today.
It adds `[readonly]` as a second author-declared mapping beside `[confirm]`, the same kind of added
clause that versions 1.1.0 and 1.2.0 classed as MINOR. Leaving it would keep a
locked document contradicting FR-002, and `/speckit-analyze` treats a constitution conflict as
critical.

**Alternatives considered**: Recording a deviation in Complexity Tracking, which leaves the conflict
for the next reader. PATCH, as a clarification. The sentence's commitment changes, and both earlier
added clauses were MINOR.

## R9: Test layers

Every new test of changed behavior fails on main first (constitution Principle VI). Tests that guard
unchanged behavior (FR-010, FR-013) pass on main and must stay green. No existing assertion changes.

| Layer | File | Covers |
|---|---|---|
| Analyzer | `internal/analyzer/readonly_test.go` (new) | `RUNE2018` severity, exact texts, and spans for FR-007 and FR-008; each edge kind; one error for a chain; a target with both attributes; another OS's target; the passing forms |
| Parser | `internal/parser/typed_params_test.go` | The guard covers the name; `[readonly("x")]` is a syntax error; `[readonly]` twice is accepted |
| Registry | `internal/language/builtin_sync_test.go` | The existing guard covers the name |
| Formatter | `internal/formatter/formatter_test.go` | `[readonly, network]` formats to one attribute per line and round-trips |
| Editor | `internal/analysis/service_test.go` | `RUNE2018` reaches the editor as an error at the declaring file |
| Library | `mcpserver/server_test.go` | Each descriptor field gives its hint, an empty descriptor makes no read-only claim, and the wire form through a client |
| Library | `mcpserver/authz_test.go` | `ReachesDestructive` alone is not refused; `Destructive` still is; a grant changes no hint |
| Transport | `mcpserver/transport_test.go` | The same hints over HTTP |
| Adapter | `internal/cli/mcp_test.go` | Reach through each edge kind, transitive, private, and diamond; OS skipping with an injected `goos`, including what is reached only through a skipped task; `Destructive` stays own-attribute; the agent endpoint's list |
| Binary | `test/integration/tool_hints_mcp_test.go` (new) | Quickstart scenarios on stdio, an imported module's task, static errors on each command that stops, `--fmt`, and unchanged call results with marker files |
| Corpus | `testdata/corpus/readonly-attr.rune` (new) | The attribute's AST, with its golden generated by `-update` |
| Docs | `test/docs` | Changed pages and the example still validate and run |

## Observed, not changed

- A call to `release` with no grant for `clean` returns a bare `[exit 1]` with no reason. That is
  [#38](https://github.com/rune-task-runner/rune/issues/38), and this feature keeps the result.
- `rune mcp --allow-destructive release` warns `task "release" is not destructive`, though `release`
  now advertises `destructiveHint: true`. The warning is right: refusal reads the task's own
  `[confirm]`, so the operator grants `clean`. The text stays; `internal/cli/serve_test.go` and
  `test/integration/destructive_mcp_test.go` pin it.
- `isDestructive` rebuilds the whole `Tasks()` list on every call. `refusal` could read the handler's
  own `TaskInfo` instead.
- The VS Code grammar's attribute list also lacks `doc`, `script`, `context`, `param-doc`, and
  `returns`. This feature adds only `readonly`.
