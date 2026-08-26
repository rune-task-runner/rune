# Research: Post-Mortem Diagnostics (Failure Hooks)

**Feature**: 022-post-mortem-diagnostics · **Date**: 2026-08-25

Phase 0 output. Each entry resolves an unknown or a gap between the clarified
spec and the current codebase, with a decision, rationale, and rejected
alternatives. File/line references are to the tree at branch point `b5eb9a6`.

## R1. Clause grammar and ordering

**Decision**: Extend the task signature line to
`Task = { Attribute } Signature ":" [ Deps ] [ "&&" PostHooks ] [ "||" FailHooks ]`.
The `||` clause is optional, comes **after** the optional `&&` clause, appears
at most once, and reuses the `DepCall` production (bare name or parenthesized
argument-passing form). `a: b || c && d` is a parse error (tokens after the
`||` hook list that are not `NEWLINE` fail the existing `expect(NEWLINE)` at
`internal/parser/task.go:75`), which matches the fixed order shown in the spec
example.

**Rationale**: Mirrors the existing single, non-repeating `&&` clause
(`internal/parser/task.go:67-73`) exactly — same parsing shape, same AST
element type, no new concepts. A fixed clause order keeps the grammar
unambiguous, keeps `rune fmt` canonicalization trivial, and reads in
severity order: "then, on success … or, on failure …".

**Alternatives considered**: Free clause order (`|| … && …` also legal) —
rejected: two spellings of the same declaration, more formatter and corpus
surface for zero expressive gain. Attribute form `[on-failure(...)]` —
rejected in clarification session 2026-08-25 (Q1).

## R2. Lexer token

**Decision**: Add `token.PIPEPIPE` ("||") adjacent to `AMPAMP` in
`internal/token/token.go`, lexed by a new `case '|':` arm in `lexOperator`
(`internal/lexer/lexer.go:367` region) that mirrors the `&` arm: `||` →
`PIPEPIPE`, a lone `|` → `l.illegal(start)` (RUNE1001), exactly as a lone `&`
behaves today.

**Rationale**: Today `|` hits the `default:` arm and emits RUNE1001, so no
existing file lexes differently afterwards except files containing `||` —
which today are already errors. Backward compatibility (FR-013) holds: the
change only assigns meaning to previously-illegal input. `Kind` values are
never serialized numerically (golden token streams print names), so inserting
the constant is safe.

**Alternatives considered**: Lexing `|` as a one-char token with parser-level
pairing — rejected; no other two-char operator is split this way (`&&` is one
token) and a lone `|` has no meaning.

## R3. Failure detection — what fires hooks, and abort semantics

**Decision**: Failure hooks fire when the task's **body** returns a non-nil
error, **except** when the error is (or wraps) `context.Canceled` — i.e. user
cancellation. Concretely, in the scheduler's `execute`
(`internal/runtime/scheduler/scheduler.go:118-120`): capture `err` from
`Engine.Execute`; if `err != nil && !errors.Is(err, context.Canceled)`, run
the fail hooks; always return the **original** `err`. No new timeout
mechanism is introduced.

**Rationale**: Rune has **no task-level timeout today** — the only deadline in
the codebase is the `[context]` hook's own 10s budget
(`internal/cli/contextprep.go:17`). The spec's "timeout yes, Ctrl-C no"
clarification is therefore implemented as an error-class rule, not a timer:
`context.Canceled` (what Ctrl-C produces via `signal.NotifyContext` at
`cmd/rune/main.go:29`, returned unwrapped by
`internal/runtime/shell/shell.go:126-128`) suppresses hooks;
`context.DeadlineExceeded` and every other failure fires them. This is
forward-compatible: if a task-timeout feature ships later, its
`DeadlineExceeded` failures fire hooks with no further change.

**Alternatives considered**: Adding a per-hook fixed timeout (mirroring
`defaultContextTimeout`) — rejected for v1: a hanging *diagnostic* is no worse
than a hanging *task* (which is also unbounded today), the user's Ctrl-C
cancels both, and inventing a timeout channel just for hooks adds a knob the
spec explicitly avoids. Firing hooks on Ctrl-C too — rejected in
clarification (Q4).

## R4. Scheduler orchestration and the memoization exemption

**Decision**: The scheduler owns fail-hook orchestration. On body failure in
`(*state).execute`, for each `task.FailHooks` entry in order:

1. Resolve via the existing `Engine.ResolveDep`.
2. If `!Engine.Available(target)` → emit a warning through the engine (see
   R6) and skip.
3. Run the hook's **dependencies** through the normal memoized path
   (`s.runDep`-equivalent), so shared setup still runs at most once.
4. Run the hook's **body** directly via a new `Engine` method carrying the
   failure context (see R5) — **bypassing** the `done`/`inflight` memo maps,
   so the same hook task fires once per failing task (spec Q2). No memo entry
   is written for the hook body run.
5. Run the hook's own `&&` post-hooks normally on hook success; **ignore** the
   hook's own `||` fail hooks (no chaining, spec edge case). A hook body
   failure produces one warning (R6) and does not affect the return value.

The `chain` slice (already threaded through `run`/`execute` for cycle
protection) is extended with the hook's name for the duration of the hook run,
guarding against runtime re-entry.

**Rationale**: `memoKey` (`scheduler.go:145-164`) is
`namespace::name(sorted-params)`; a shared diagnostic hook attached to two
failing parallel tasks would otherwise run once with the wrong context for
the second failure — exactly what clarification Q2 rejected. Running deps
through the memoized path preserves the "shared build step isn't repeated"
guarantee where it matters, while the hook body itself is per-failure by
design. Keeping orchestration in the scheduler (not the engine) preserves the
existing division: the scheduler owns ordering/memoization, the engine owns
process execution.

**Alternatives considered**: Salting `memoKey` with the failing task's name so
the normal `run` path can be reused — rejected: it would also salt the hook's
dependency subtree, breaking dep memoization and violating the "hook's
dependencies still memoize normally" clarification. Engine-side hook
execution inside `Execute` — rejected: the engine cannot resolve/memoize the
hook's dependencies without reaching back into scheduler state.

## R5. Failure context delivery (FR-009)

**Decision**: Extend the `scheduler.Engine` interface with one method:

```go
ExecuteFailHook(task *ast.Task, params map[string]string, f Failure) error
```

where `Failure` is a small exported struct in the scheduler package:
`{ TaskName string; ExitCode int }`. The CLI engine implements it by running
the same `runBody` path with two extra environment entries appended after
`taskEnv(task)`:

- `RUNE_FAILED_TASK` — the failing task's name (namespaced form as printed to
  users, e.g. `mod::test`).
- `RUNE_FAILED_EXIT_CODE` — the decimal exit code of the failed body.

The exit code is derived where the error taxonomy already lives: from
`*shell.ExecError.Code` when available (`internal/runtime/shell/shell.go:47`),
else `1` (mirroring `ExitTaskFail`). The scheduler computes `Failure` once per
failing task and passes it to every hook run for that failure; the derivation
lives in the scheduler via `errors.As` on `*shell.ExecError` (the
`scheduler → shell` import is acyclic — shell does not import scheduler).

**Rationale**: `taskEnv` (`internal/cli/run.go:435-441`) is closed over
`*ast.Task` only and `Engine.Execute(task, params)` has no side channel — a
new method is the smallest honest extension, and it doubles as the
"bypass memoization" entry point (R4), keeping the exemption explicit in the
interface rather than hidden in a flag. Env delivery matches how Rune already
passes ambient facts (`[env(...)]` pairs, exported module vars) and works
identically across sh/python/node executors.

**Alternatives considered**: Injecting synthetic params — rejected: params are
user-declared, arity-checked (RUNE2005), and would collide with real
parameters. A context-valued side channel on `ctx` — rejected: hidden
coupling, unidiomatic for data the child *process* needs.

## R6. Warnings channel and OS-gated hooks

**Decision**: Add a second `Engine` method, `Warnf(format string, args
...any)`, implemented by the CLI engine as the existing one-line
`warning: …` convention on stderr (label styled `theme.Warning`, matching
`internal/cli/run.go:325` and `contextprep.go:55-61`). The scheduler uses it
for exactly two events: (a) an OS-unavailable fail hook is skipped —
`warning: failure hook <name> skipped (requires <os>)`; (b) a fail hook run
fails — `warning: failure hook <name> failed: <err>`. On the MCP path the
adapter's engine writes warnings into the captured stderr buffer, so agents
see them too.

**Rationale**: Dependency/post-hook OS-skips are **silent by design**
(`scheduler.go:6-8`, `docs/runefile.md:261`), but the spec pins a warning for
fail hooks (FR-012) — and rightly: a silently-skipped diagnostic hides *why no
diagnosis appeared* at the exact moment the user is debugging. The divergence
is deliberate, scoped to `||` hooks only, and documented (docs update in
scope). The scheduler stays I/O-free; the engine renders.

**Alternatives considered**: Reusing silent-skip for consistency — rejected by
spec FR-012. Returning warning strings from `Run` for the caller to print —
rejected: loses ordering relative to hook output.

## R7. MCP surface — capturing and delivering the fix suggestion (FR-006/007)

**Decision**:

- `mcpserver.Result` gains a field: `FixSuggestion string` (already masked,
  like the `Instructions` contract at `mcpserver/server.go:51-56`).
- `formatResult` (`mcpserver/handler.go:63-79`) appends, when the field is
  non-empty, a delimited section after the `[exit %d]` line:

  ```text
  [fix suggestion - from Runefile || failure hooks]
  <text>
  ```

- Capture: the CLI engine gets an optional `failHookStdout io.Writer`. When
  set (MCP path only), `ExecuteFailHook` routes the hook body's **stdout**
  there instead of the shared `outBuf`; hook **stderr** continues to the
  shared stderr buffer. The adapter (`internal/cli/mcp.go` `Call`) supplies a
  dedicated buffer wrapped in its own `mask.NewWriter` (same `maskSet`),
  flushes it after `scheduler.Run` returns, trims trailing `\r\n`, and applies
  the `[context]` hook's exact cap discipline: 8 KiB cap, cut backed up to a
  UTF-8 rune boundary, `\n[truncated]` marker appended after the cut and
  excluded from the cap (`internal/cli/contextprep.go:63-72` is the template;
  the cap constant is shared or duplicated with the same value).
- The fix suggestion is **stdout only**, matching the `[context]` precedent
  (spec 021 treats hook stdout as the payload).

**Rationale**: `Call` today funnels body and hook output into one
`outBuf`/`errBuf` pair (`mcp.go:81`) — a delimited section requires a separate
sink, and a writer-level mask wrapper is the only ordering that satisfies
"mask before truncate" (FR-007). Extending `Result` keeps `formatResult` the
single formatting choke point that existing tests assert.

**Alternatives considered**: Marker lines written inline into the shared
buffer and parsed apart later — rejected: fragile, and hook/body output can
interleave under parallel failures. Structured MCP content blocks (a second
`TextContent`) — rejected for v1: agents uniformly read the first text block;
one block with a labeled section is the simplest thing that works, and the
`[exit %d]` marker precedent is already line-based.

## R8. CLI presentation (FR-008)

**Decision**: Before each fail-hook run, the engine prints one header line to
stderr: `diagnosing: <hook-name>` with the label styled `theme.Warning` and
the name plain — the exact shape of the existing `running:` / `cached:` /
`warning:` label convention (`internal/cli/run.go:315-325`). Hook body output
then streams to the normal stdout/stderr as for any task. No output capping
on the CLI (spec edge case: "CLI output is not capped"). With styling
disabled the line must be byte-identical to the plain form (invariant tested
by `test/integration/error_banner_test.go:41` pattern).

**Rationale**: Reuses the established label style; no new `style.Theme` role
is needed. `Warning` (yellow) is the right register: the run has already
failed, and the diagnostics are advisory.

**Alternatives considered**: A boxed/heading banner via `theme.Heading` —
rejected: headings are used for listings, not run progress, and a heavier
banner violates the "styling is additive" convention. A new theme role —
rejected: nothing else would use it.

## R9. Analyzer coverage and the new diagnostic code (FR-010/011)

**Decision**:

- Fold `FailHooks` into every place that today combines `Deps + PostHooks`:
  `checkDependencies` (`analyzer.go:238` — unknown task RUNE2001, arity
  RUNE2005), `checkExpressions` (`:183` — hook arg resolution),
  `checkCycles` (`:295` — cycle edges RUNE2003), and the `[context]` closure
  walk `checkContextClosure` (`:139,155`).
- Mint one new semantic code: `CodeInvalidFailureHook = "RUNE2011"` (next free
  slot after RUNE2010 in `internal/diag/codes.go`), emitted when a `||` target
  resolves to a task with the `agent` executor (FR-011), anchored at the
  `DepCall.Sp` span. Update the two code contracts in the same change:
  `docs/diagnostics.md` table and
  `specs/011-rune-lsp/contracts/diagnostic-codes.md`.

**Rationale**: Including `||` edges in the cycle graph makes `a: … || a`
statically illegal — acceptable and spec-pinned (FR-010), and it prevents the
confusing runtime shape where a hook's dependency is the already-failed task
(whose memoized error would immediately fail the hook). The agent-executor
rejection can't reuse RUNE2007 (`CodeInvalidAttribute`) honestly — there is no
attribute involved — and code meanings are a frozen public contract, so a new
code is the correct move.

**Alternatives considered**: Reusing RUNE2007 as the `[context]` agent check
does — rejected: that check *is* about an attribute; this one is about a
clause target. Skipping cycle checks over `||` edges since hooks don't chain
at runtime — rejected by FR-010's explicit "cycles through `||` edges".

## R10. Touch list for the parallel `FailHooks []*ast.DepCall` field

**Decision**: Add `FailHooks` to `ast.Task` (`internal/ast/ast.go:80-90`) and
update every `PostHooks` consumer identified in exploration:

| Surface | File | Change |
|---|---|---|
| Parser | `internal/parser/task.go` | parse `\|\|` clause after `&&` |
| Import namespacing | `internal/config/compose.go:139-153` | rewrite `mod::` prefixes on fail-hook refs |
| Analyzer | `internal/analyzer/analyzer.go` | R9 |
| Scheduler | `internal/runtime/scheduler/scheduler.go` | R4 |
| Formatter | `internal/formatter/formatter.go:104-110` | emit `" || …"` canonically |
| AST dump | `internal/ast/dump.go:73-75` | `FailHooks:` golden line |
| `--dump` JSON | `internal/cli/dump.go:40,78-80` | `"failHooks"` DTO field |
| LSP targets | `internal/language/position.go:93` | include fail hooks in hover/go-to-def |

**Rationale**: This is the complete consumer set found by exhaustive search;
missing any one silently drops the feature from that surface (the LSP one is
the easiest to miss and would break go-to-definition on `||` targets).

## R11. Test strategy mapping (constitution VI)

**Decision**: Red-green per layer, extending the harnesses that already cover
the closest neighbor:

- **Lexer**: `||` and lone-`|` cases in `internal/lexer` tests + add `||` to
  `testdata/lexer/scenario1.rune` golden (note: no `&&` golden exists today —
  add both operators while touching the fixture).
- **Parser**: `TestParsePostHook` sibling for fail hooks; parse-error case for
  `|| … && …`; goldens in `testdata/parser/`.
- **Analyzer**: siblings of the existing code tests (RUNE2001/2003/2005 via
  `||` edges; RUNE2011 agent rejection; `[context]` closure through `||`).
- **Scheduler unit**: fake-engine tests for fire-on-failure,
  no-fire-on-success, no-fire-on-Canceled, per-failure exemption with two
  failing roots sharing one hook, dep memoization inside hooks, original
  error preserved, OS-skip warning path
  (`internal/runtime/scheduler/scheduler_test.go` fake engine).
- **CLI/MCP unit**: `contextprep_test.go`-style tests for cap/rune-boundary/
  masking of the fix suggestion; `formatResult` section rendering; env vars
  present in hook body.
- **Integration** (`test/integration/`): failing task fires hook and exit
  code preserved; success does not fire; Ctrl-C does not fire (signal test if
  portable — else scheduler unit only); MCP tool result contains the
  delimited section; plain-bytes styling invariant for `diagnosing:` line.
- **Corpus**: add a `||` task to `testdata/corpus/full.rune` + regenerate
  `full.ast` (SC-004 guard).
- **Fuzz**: existing `FuzzLexer`/`FuzzParser`/`FuzzParseRecover` pick up the
  new token automatically; seed corpus entry with `||`.
- **Docs harness**: update `docs/examples/dependencies` (statically validated
  by `TestExamplesStaticallyValidate`; README contract enforced by
  `TestExampleContract`).

**Rationale**: Every listed harness exists and is cited from exploration; no
new test infrastructure is required.

## R12. Documentation set

**Decision**: Update in the same PR (constitution: "surface changes carry
their docs"): `docs/GRAMMAR.md` (Task production + FailHooks production),
`docs/runefile.md` §Dependencies-and-post-hooks (+ the `:261` silent-skip
sentence gains a fail-hook exception), `docs/how-to/dependencies-and-hooks.md`
(retitle scope to include failure hooks; fix the ":42 hooks don't run on
failure" pitfall to name the `||` alternative), `docs/mcp.md` (fix-suggestion
section, modeled on the `[context]` section), `docs/diagnostics.md`
(RUNE2011), `docs/examples/dependencies/` (Runefile + README walkthrough),
index rows (`docs/how-to/README.md`, `docs/user-guide/README.md`,
`docs/examples/README.md`), `docs/how-to/os-filtering.md:11` (silent-skip
exception). VS Code TextMate grammar: no change required (`&&` is not
highlighted today either); optional enhancement explicitly out of scope.

**Rationale**: List derived from the docs-harness contracts
(`test/docs/links_test.go`, `contract_test.go`, `codeblocks_test.go`) so
`docs-check` stays green.
