# Research: Per-Task Opt-In for Destructive Tasks over MCP

**Feature**: 031-mcp-destructive-optin | **Date**: 2026-11-04 | **Spec**: [spec.md](spec.md)

## Baseline (v0.6.0 plus spec 030, read from the code)

- **Two gates block a `[confirm]` task over MCP.** The tool handler
  (`mcpserver/handler.go`) calls `Server.authorized` (`mcpserver/authz.go`), which
  refuses a destructive task unless `Options.AllowDestructive` is true. Past that,
  the CLI engine runs the task through `engine.confirmFailure`
  (`internal/cli/confirm.go`). The MCP adapter builds the engine with
  `Options{Stdin: nil, Yes: false}`, so `confirm` returns `confirmDeclined` and
  the task fails silently.
- **Nothing sets the switch.** `cli.ServeMCP` passes `AllowDestructive: opts.Yes`,
  but `--yes` is a root-only flag and `serve`/`mcp` reject it. The agent-executor
  endpoint (`internal/cli/agentexec.go`) passes `AllowDestructive: false`.
- **One refusal text for two reasons.** `authorized` also enforces
  `Options.AllowList`, and the handler returns the same message, `task %q requires
  explicit approval and is not authorized for this session`, for both.
- **Every path reaches one gate.** `engine.Execute` (the called task, its
  dependencies, and its `&&` post-hooks) and `engine.ExecuteFailHook` (`||`
  failure hooks) both call `confirmFailure`. A declined failure hook already
  degrades to a one-line warning upstream (spec 022).
- **Task names.** `indexTasks(file)` keys every task by its full name: private
  tasks, OS-restricted tasks, the `[context]` hook, and imported tasks as
  `ns::task` (`internal/config/compose.go`). `mcpserver.toolName` maps `ns::task`
  to the tool name `ns__task`, but the handler keeps the task name for the engine
  and for messages.
- **Operator flags on `serve`.** `--ignore-version` is bound to
  `opts.MCPAllowIgnoreVersion` in `cmd/rune/serve.go`. `ServeMCP` validates the
  transport before running the `[context]` hook, with the comment "a usage error
  must never run Runefile code".
- **Suggestions.** `cli.nearest` returns the closest candidate within edit
  distance 2; `args.go` formats it as `unknown task: %s (did you mean %q?)`.
- **Startup line.** `rune MCP server on stdio`, or `rune MCP server on http://ADDR
  (token required)`, dimmed. `test/integration/status_styling_test.go` checks it
  with `strings.Contains`.
- **CLI reference test.** `TestCLIReferenceMatchesBinary` compares only root
  `--help` flags with the "Global flags" section of `docs/cli.md`. A flag local to
  `serve` is outside its scope.
- **Changelog.** git-cliff prints each commit subject (the squashed PR title)
  and nothing else. A `!` adds no marker, so a breaking change says `breaking:`
  in the title, as PR #75 did.

## R1: Flag shape and parsing

**Decision**: Register `--allow-destructive` on `serve` with
`f.StringArrayVar(&opts.MCPAllowDestructive, ...)`, beside `--ignore-version`.
`cli.ServeMCP` splits each value on `,`, trims surrounding spaces from each
item, rejects an empty item, and removes duplicates while keeping the first
occurrence's order.

**Rationale**: `StringArray` keeps each occurrence verbatim, so
`--allow-destructive ""` and `--allow-destructive clean,` are both visible as
empty items (FR-009). Splitting ourselves gives exactly one rule for commas.

**Alternatives considered**:
- `StringSliceVar`: parses each value as CSV. An empty value becomes an empty
  slice, so `--allow-destructive ""` is indistinguishable from not passing the
  flag, and CSV quoting rules would apply to task names.
- A bool switch plus a separate list: rejected by FR-007; there is no
  approve-all mode.

## R2: Where names are validated

**Decision**: In `cli.ServeMCP`, in this order:

1. Parse the flag values (R1). An empty item is a `UsageError` before the
   Runefile is even resolved.
2. Resolve and load the Runefile statically (`newMCPAdapter`, unchanged).
3. Check each name against the adapter's `tasks` map. An unknown name is a
   `UsageError`: `unknown task in --allow-destructive: NAME`, plus
   ` (did you mean "X"?)` when `nearest` finds one. Candidates are the task names
   sorted, so ties resolve the same way on every run.
4. Validate the HTTP transport (unchanged).
5. Print one warning per granted name whose task has no `[confirm]` (R6).
6. Run the `[context]` hook, build the server, print the startup line.

**Rationale**: Steps 1 to 4 run no Runefile code, which satisfies FR-010. The
`tasks` map already holds every name the CLI accepts, including private,
OS-restricted, and imported tasks, which the spec's edge cases accept. Reusing
`nearest` and the existing message shape keeps one style of unknown-task error.

**Alternatives considered**: Validating in `cmd/rune` (`validateServeFlags`)
cannot see task names without loading the Runefile twice. Validating against
`Engine.Tasks()` would reject private and OS-restricted names, which the spec
accepts.

## R3: Pre-approving the confirmation for granted tasks only

**Decision**: Add a `grants map[string]bool` field to `mcpAdapter` and to
`engine`. `ServeMCP` sets the adapter's field from the validated names;
`CallLists` copies it into each engine it builds. `confirmFailure` returns nil
when `e.grants[task.Name]` is true, right after the existing `e.opts.Yes` check.
`newAgentAdapter` never sets it, so the agent-executor endpoint grants nothing
(FR-015). The CLI path never sets it, so CLI behavior is unchanged (spec edge
case "CLI runs").

**Rationale**: The check is per task at the single gate every path reaches
(baseline), so a grant applies to the named task wherever it runs (FR-006) and
never to another task (FR-005). An ungranted task falls through to today's
`Stdin == nil` decline, which gives the bare failure the clarification chose for
dependencies and the warning spec 022 already gives for failure hooks.

**Alternatives considered**:
- Setting `opts.Yes = true` when the called task is granted: approves every
  `[confirm]` task in the run, which FR-005 forbids.
- A fake stdin that answers `y`: bypasses the terminal rule from spec 027 and
  approves every task that asks.
- Moving the grant into `mcpserver` and passing it through `Engine.Call`:
  changes the public `Engine` interface for every embedder, while the engine
  already has to know which task it is confirming.

## R4: Library API change

**Decision**: Change `mcpserver.Options.AllowDestructive` from `bool` to
`[]string`, the task names that may be called although destructive. `New`
builds a set from it. The doc comment says the engine must approve the task's
own confirmation for exactly these names, as the CLI adapter does.

**Rationale**: FR-016 removes the server-wide switch. Keeping the field name with
a new type makes every existing `AllowDestructive: true` a compile error at that
exact line, so no embedder silently loses or gains access. The name mirrors the
flag and sits next to `AllowList []string`.

**Alternatives considered**:
- A new field (`DestructiveTasks`) with the bool kept as deprecated: keeps an
  approve-all switch alive, which FR-016 rules out.
- Removing the bool and adding a differently named list: also a compile error,
  but the error ("unknown field") does not point embedders at the replacement.
- A predicate `func(name string) bool`: more general than any caller needs.

## R5: Refusal messages

**Decision**: Replace `authorized(name) bool` with `refusal(name) string`, which
returns "" when the call may proceed. Order and texts:

1. `AllowList` set and the name not in it: `task "NAME" is not authorized for this session`.
2. Destructive and not granted: `task "NAME" is destructive; to permit it, add NAME to --allow-destructive and restart the server`.
   With no `AllowDestructiveFlag` (the agent-task endpoint, embedders without
   such a flag): `task "NAME" is destructive and is not allowed in this session`.

`NAME` is the task name (`tools::clean`), which is what the flag accepts.

**Rationale**: FR-004 fixes the destructive text, and an allowlist refusal must
not tell the operator to add a flag that would not help. The allowlist check
stays first so a task outside it is refused the same way whether or not it is
destructive. The text says to add the name, since restarting with only the
refused name would drop the grants the server already holds. The flag name
comes from `Options.AllowDestructiveFlag`: the agent-task endpoint can never be
granted anything, so advice to restart would be wrong there (code review).

**Alternatives considered**: The flag hard-coded in the library, which gives
the agent-task endpoint and embedders advice that cannot work. A function
field for a fully custom text, which no caller needs.

## R6: Operator-facing output

**Decision**:

- Warning, once per name, before the `[context]` hook runs, in the shared
  warning style: `warning: --allow-destructive build has no effect: task "build" is not destructive`.
- Startup line when the grant names at least one `[confirm]` task that can
  run on this host: append those names in the order given,
  joined by `, `:
  - stdio: `rune MCP server on stdio (destructive tasks allowed: clean, reset)`
  - HTTP: `rune MCP server on http://ADDR (token required; destructive tasks allowed: clean, reset)`
- With no grant, both lines are byte-identical to today.

**Rationale**: FR-011 and FR-012. The order the operator typed is the order they
will look for. Existing `Contains` checks on the startup line keep passing.

## R7: Test strategy

**Decision**: Test-first, at four layers. All run in Docker.

| Layer | File | Covers |
|---|---|---|
| Library | `mcpserver/authz_test.go`, `handler_test.go` | refusal order and texts (R5); grant set from `Options`; tool list identical with and without grants (FR-014) |
| Library, HTTP | `mcpserver/transport_test.go` | a granted and an ungranted destructive call through `StartHTTP` with the go-sdk `StreamableClientTransport` and a bearer-token `http.Client` (FR-013) |
| CLI engine | `internal/cli/confirm_test.go`, `mcp_test.go` | grant approves only the named task as called task, dependency, post-hook, failure hook, and private dependency; ungranted ones decline; agent adapter grants nothing; CLI path unchanged |
| CLI flags | `internal/cli/serve_test.go` (new), `cmd/rune/serve_test.go` | parsing (R1), unknown name with suggestion, empty item, `*`, warning text, startup line, `--yes` still unknown in both positions |
| Binary | `test/integration/destructive_mcp_test.go` (new) | stories 1 to 3 end to end over stdio with marker files; `[context]` marker absent after a usage error |

`mcpCallArgs` in `test/integration/secret_masking_mcp_test.go` hardcodes
`rune mcp`; add a variant that takes server arguments rather than copying the
session code.

## R8: Documentation inventory

**Decision**: Change these, and nothing else:

| File | Change |
|---|---|
| `docs/mcp.md` "Starting the server" | one example with `--allow-destructive clean` |
| `docs/mcp.md` "Security model" | rewrite the first two bullets: refused by default, named per task, no blanket switch, `--yes` not accepted |
| `docs/mcp.md` "Agent task bodies" | the endpoint an `agent` task exposes never permits destructive tasks |
| `docs/cli.md` commands table | `rune serve` row lists `--allow-destructive <task>[,<task>...]` |
| `docs/runefile.md` `[confirm]` paragraph | "Over MCP, `[confirm]` tasks are refused unless the operator names them with `--allow-destructive`" |
| `docs/examples/agent-driven/README.md` | replace "use `--yes` to auto-approve deliberately", which does not work over MCP |
| `docs/use-cases/mcp-agents.md` callout | replace "use `--yes` to auto-approve deliberately", which does not work over MCP |
| `cmd/rune/serve.go` help | flag usage line, a `Long` sentence, one `Example` line |

`README.md` ("destructive tasks are gated") and the website landing page ("gated
when destructive") stay accurate and are unchanged. `docs/cli.md`'s "Global
flags" table is unchanged (baseline: the flag is local to `serve`).

## R9: Release and PR title

**Decision**: One PR, closing #31, titled
`fix(mcp)!: breaking: rune mcp --allow-destructive TASK lets agents run named [confirm] tasks; mcpserver.Options.AllowDestructive is now a task list`.

**Rationale**: For CLI users this fixes a documented capability that did not
exist, so `fix`. For embedders the `Options` field changes type, so `!` and the
`breaking:` text, which is the only part git-cliff prints (baseline, FR-016).
Rune is before 1.0, so the release is a minor bump (the release-guru rules
decide the exact version).
