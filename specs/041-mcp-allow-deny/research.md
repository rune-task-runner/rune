# Research: Operator Allowlist and Denylist for MCP Tasks

**Feature**: 041-mcp-allow-deny | **Date**: 2026-11-12 | **Spec**: [spec.md](spec.md)

The Technical Context has no open unknowns. These are the design decisions, each checked against
the code on `main` at `9697544`.

## R1. Filter where tools are registered, not where they are called

**Decision**: `mcpserver.New` skips a task that the filters hide, so it is never added as a tool.
The allowlist branch in `refusal` (`mcpserver/authz.go`) is removed.

**Rationale**: `New` registers each tool once from one `Tasks()` call
(`srv.mcp.AddTool(toolFor(t), srv.handler(t))`). A task that is not registered is absent from
`tools/list` and a `tools/call` for it is answered by the MCP SDK's own unknown-tool error. That is
the same code path as a name no task has, so FR-004 and SC-003 hold by construction, with no text of
ours to keep in step with the SDK. `TestServerNeverRegistersOSMismatchedTools` in
`internal/cli/mcp_test.go` already relies on this for tasks restricted to another OS.

**Alternatives considered**:

- Keep the tool registered and filter `tools/list` with SDK middleware, refusing the call in the
  handler. Rejected: two places must agree, and the handler would have to imitate the SDK's
  unknown-tool response to satisfy FR-004.
- Filter in the CLI adapter's `Tasks()`. Rejected: embedders would get nothing (FR-018), and the
  adapter's `Tasks()` also serves the `agent` task endpoint, which must not change (FR-017).

## R2. Library surface: `AllowList` changes meaning, `DenyList` is new

**Decision**: `Options.AllowList` keeps its name and now decides which tasks are tools.
`Options.DenyList []string` is added. A task is a tool when
`(len(AllowList) == 0 || AllowList contains name) && !DenyList contains name`. Names that match no
task are ignored by the library, as `AllowList` and `AllowDestructive` names are today. Validation
of names is the CLI's job.

**Rationale**: One field with one meaning is simpler for embedders than a second allowlist beside a
call-only one. The old behavior (listed but refused) has no use the issue or the specs name. Rune is
before 1.0 and spec 031 already made a breaking change to this struct.

**Alternatives considered**: a new `Expose` field leaving `AllowList` as it is. Rejected: it keeps a
field whose only effect is the confusing "listed but not authorized" state the issue removes.

An empty `AllowList` means "no allowlist", not "allow nothing". The CLI cannot produce an empty list
from a given `--allow`: every item must be a non-empty task name (R4), and names that can never be
tools are still passed through, so `--allow _setup` gives `AllowList: ["_setup"]` and zero tools.

## R3. The server reports how many tasks it exposed

**Decision**: Add `func (s *Server) Exposure() (exposed, exposable int)`: the number of tools
registered and the number of tasks the engine offered. `New` counts both in its registration loop.

**Rationale**: The startup line needs both numbers (FR-014) and the empty-surface warning needs the
first (FR-013). Counting where the filter is applied means the CLI cannot disagree with what was
registered. Calling `adapter.Tasks()` a second time in the CLI would repeat the reach walk and
duplicate the filter rule.

**Alternatives considered**: a CLI-side helper that re-applies the rule to `adapter.Tasks()`.
Rejected for the duplication above.

## R4. One parser and one name check for all three flags

**Decision**: Generalize the two helpers in `internal/cli/serve.go`:

- `parseAllowDestructive(values)` becomes `parseTaskNames(flag, values)`: split on commas, trim,
  keep the first of a repeated name, and reject an empty item with `<flag>: empty task name`.
- `checkGrant(names, tasks)` becomes `checkTaskNames(flag, names, tasks)`: reject the first name
  that is not a task, with `unknown task in <flag>: <name>` and the `suggest.Nearest` hint.

`--allow-destructive` calls them with its own flag name, so its messages stay byte-identical.

**Rationale**: The issue asks for the same usage error and suggestion `--allow-destructive` has. The
rules (comma list, repeatable, `ns::task` names, private and other-OS names count as known) are the
same for all three, so a second copy would only drift.

**Alternatives considered**: separate helpers per flag. Rejected as duplication.

## R5. Order of startup checks

**Decision**: In `ServeMCP`, in this order:

1. Parse `--allow-destructive`, then `--allow`, then `--deny` (empty-name errors). No Runefile is
   read yet.
2. Resolve and load the Runefile (unchanged static errors).
3. Check names against the loaded tasks, in the same flag order (unknown-name errors).
4. Validate the transport (unchanged).
5. Print warnings: non-destructive grants (unchanged), then names that can never be tools (R6).
6. Run the `[context]` hook, build the server.
7. Print the empty-surface warning if nothing is exposed (R7), then the startup line (R8).

**Rationale**: Steps 1 to 4 run no Runefile code, which FR-011 requires and spec 031 FR-010 already
established. The existing flag keeps first place so its error precedence does not change. The
empty-surface warning needs the registered count, so it follows `New`; it is a warning, so running
the hook first breaks no rule.

## R6. Names that exist but can never be tools

**Decision**: For each name in `--allow` or `--deny` whose task is private or is the `[context]`
hook, print once, before the server starts:

```text
warning: --allow _setup has no effect: task "_setup" is never exposed to agents
```

A task restricted to another OS is accepted silently. The test is
`t.IsPrivate() || t.Attr(ast.AttrContext) != nil`, the two host-independent conditions that
`mcpAdapter.Tasks` and `visibleOn` use to exclude a task.

**Rationale**: Matches `warnNonDestructive` from spec 031: a no-op name is worth a line but must not
stop a shared client configuration. Other-OS names are silent for the reason `destructiveOnly`
gives: one configuration may serve several hosts.

**Alternatives considered**: a usage error for private names. Rejected: making a task private in the
Runefile would then break every client configuration that names it.

## R7. Empty tool surface

**Decision**: When `--allow` or `--deny` was given and `Exposure()` reports zero exposed tasks,
print:

```text
warning: no tasks are exposed to agents; check --allow and --deny
```

and start the server. With neither flag, no warning is printed even for a Runefile with no
exposable task, so FR-016 holds.

**Rationale**: `--allow win-task` on Linux is valid and must start. `--allow build --deny build` is
probably a mistake, but the two are not worth telling apart: one line tells the operator either way.

## R8. Startup line

**Decision**: `startupLine` gains the exposure counts, used only when a filter flag was given. The
parenthesized parts are joined with `; ` in a fixed order: `token required` (HTTP),
`N of M tasks exposed` (filters), `destructive tasks allowed: ...` (grants). Forms are in
[contracts/cli-serve.md](contracts/cli-serve.md#startup-line-stderr-dimmed-as-today).

**Rationale**: A count confirms the filter took effect without printing a long list for a denylist
on a large Runefile. Building the line from parts replaces the four-way `switch`, which would
otherwise grow to eight cases. With no filter flag the output is byte-identical to today, which the
existing `startupLine` tests pin.

## R9. What does not change

- **Destructive authorization.** `refusal` keeps its destructive checks unchanged. A listed
  `[confirm]` task is still refused with the spec 031 text, and a listed task that reaches an
  ungranted one with the spec 034 text. `checkTaskNames` for `--allow-destructive` still accepts a
  hidden task, and `mcpAdapter.grants` still approves it when a run reaches it.
- **Dependencies and hooks.** The engine runs a called task's whole graph. The filters are applied
  only at tool registration, so nothing in the run path reads them (FR-006).
- **Hints.** `toolFor` reads the unchanged `TaskInfo`, so an exposed task's hints still cover hidden
  dependencies (FR-008).
- **The `agent` task endpoint.** `agentexec.go` builds its server with `Options{Version: ...}` only.
  `MCPAllow` and `MCPDeny` are read only by `ServeMCP` (FR-017).

## R10. Tests

**Decision**: Test-first at each layer.

- `mcpserver`: registration filter (allow, deny, both, neither), hidden call equals missing-name
  call, `Exposure()` counts, exposed tools' descriptors equal the unfiltered ones. The tests that
  pinned the old behavior are rewritten, not deleted: `TestRefusalAllowListNarrows`,
  `TestRefusalAllowListBeforeGrant`, `TestRefusalAllowListBeforeReach`
  (`mcpserver/authz_test.go`), the `allowlist` case of the refusal-before-arguments table
  (`mcpserver/handler_test.go`), and `TestListToolsSameWithAllowList`
  (`mcpserver/server_test.go`), which becomes the test that an allowlist changes the list and
  nothing else about the tools left in it.
- `internal/cli`: `parseTaskNames` and `checkTaskNames` per flag, the never-exposed warning, the
  empty-surface warning, every `startupLine` form.
- `test/integration`: a new `allowlist_mcp_test.go` driving the binary over stdio with marker
  files, reusing the `toolCall`, `toolReply`, and `exists` helpers and a tools/list helper like
  `toolHints`. It covers the quickstart scenarios and stands in for the issue's
  `TestMCPE2E_Pending_AllowList`, which is not in this repository.
- `test/docs`: the CLI reference check covers the new flags.

Every new or changed function gets a unit test, with 90% coverage of the changed packages as the
target (maintainer rule).

## R11. Documentation and release note

**Decision**: Update `docs/mcp.md` (a "Choosing which tasks agents see" block beside the
destructive one, and the security model list), `docs/cli.md` (the `rune serve` row), and the
`serve` command's `Long`, `Example`, and flag help in `cmd/rune/serve.go`. The commit is
`feat(mcp)!:` with the breaking change for embedders in its subject, which the changelog carries:
`mcpserver.Options.AllowList` now hides tasks from the tool list instead of refusing their calls.

**Rationale**: FR-019 and the constitution's "surface changes carry their docs". `docs/mcp.md` must
say plainly that `--deny` does not stop a task running as a dependency, since that is the point an
operator is most likely to misread.
