# Implementation Plan: Post-Mortem Diagnostics (Failure Hooks)

**Branch**: `022-post-mortem-diagnostics` | **Date**: 2026-08-25 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/022-post-mortem-diagnostics/spec.md`

## Summary

Add failure hooks to the Runefile DSL: a `||` clause on the task signature
line, mirroring the existing `&&` success-hook clause, that runs diagnostic
tasks when the declaring task's body fails. Hook output is streamed to the CLI
under a `diagnosing:` header and delivered to MCP agents as a masked,
8 KiB-capped `[fix suggestion …]` section inside the same tool response as the
failure — the original exit code is always preserved. Technical approach: a
new `PIPEPIPE` token and parser clause populate `Task.FailHooks []*DepCall`;
the analyzer validates `||` edges with the existing RUNE2001/2003/2005 checks
plus a new RUNE2011 (agent-executor hook rejection); the scheduler runs hooks
after body failure via a new `Engine.ExecuteFailHook` method that bypasses
memoization (once per failing task) and injects `RUNE_FAILED_TASK` /
`RUNE_FAILED_EXIT_CODE`; the MCP adapter captures hook stdout in a dedicated
mask-wrapped buffer and surfaces it via a new `mcpserver.Result.FixSuggestion`
field. Full decisions in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.x (repo toolchain), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: stdlib + existing deps only — `mvdan.cc/sh/v3`
(shell executor), `modelcontextprotocol/go-sdk` (MCP), `golang.org/x/sync`
(errgroup). No new dependencies.

**Storage**: N/A (no persistence; per-invocation in-memory scheduler state)

**Testing**: `go test` in Docker only (`docker-compose run --rm test …`);
layered per constitution VI — golden files (lexer/parser/fmt/corpus),
scheduler unit tests with fake engine, binary-level integration tests
(`test/integration`), docs harness (`test/docs`), existing fuzz targets

**Target Platform**: Linux, macOS, Windows (single static binary)

**Project Type**: CLI tool + embeddable MCP server library (existing layout)

**Performance Goals**: zero measurable overhead on the success path (SC-003);
hook execution cost is user-authored task cost

**Constraints**: byte-identical output for Runefiles without `||` (FR-013,
corpus-enforced); plain-output byte invariant when styling is disabled;
masking before truncation on the agent surface (FR-007); original exit code
preserved in all paths (FR-004)

**Scale/Scope**: ~12 packages touched (see R10 touch list); no new packages;
one new token, one new AST field, two new `Engine` methods, one new
diagnostic code, one new `Result` field

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.0.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | Hooks are explicit author-declared tasks; nothing is skipped or inferred. No new timeout/retry machinery invented (R3): failure classes, not timers. |
| II. Errors are a feature | ✅ PASS | All `\|\|` misuse is statically rejected with `file:line:col` + caret before execution (RUNE2001/2003/2005/2011); the feature itself upgrades runtime failures into actionable diagnostics. |
| III. Minimal, total DSL | ✅ PASS | The **expression sublanguage is untouched**. `\|\|` is task-structure syntax mirroring the existing `&&` clause — same precedent as specs 020/021 (attributes) which extended task declarations without touching the frozen expression surface. Hooks cannot chain and cycles are rejected, so termination is preserved. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | Hand-written lexer/parser extended in place; no codegen; locked package layout unchanged (no new packages). |
| V. Boringly portable | ✅ PASS | Pure Go; env-var contract identical across OSes and executors; Windows covered by existing CI matrix. |
| VI. Test-first, multi-layer verification | ✅ PASS | Red-green per layer mapped in R11; corpus guards grammar drift (SC-004); docs are tested fixtures (R12). |
| VII. AI-native, secure by default | ✅ PASS | The feature's core is the agent surface (FR-006). Masking before truncation (FR-007); agent-executor hooks statically rejected (FR-011, no recursive agent sessions); `[confirm]` semantics of hook tasks unchanged. |
| VIII. Go engineering discipline | ✅ PASS | Errors wrapped/classified via `errors.Is/As`; scheduler stays I/O-free (warnings via engine); no goroutine changes beyond existing errgroup use. |

**Engineering constraints**: Docker-only testing respected (quickstart gate
set); locked package layout respected; backward compatibility total (`||` is
lex-illegal today, R2); surface change ships with `docs/GRAMMAR.md` + fixtures
in the same PR (R12).

**Post-design re-check (after Phase 1)**: no new violations introduced by the
design artifacts. The single deliberate behavioral divergence — OS-skipped
*failure* hooks warn while OS-skipped deps/post-hooks stay silent (R6) — is
spec-pinned (FR-012), scoped, and documented; it strengthens Principle II
rather than violating any principle. Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/022-post-mortem-diagnostics/
├── plan.md              # This file
├── spec.md              # Clarified feature spec (4 clarifications, 2026-08-25)
├── research.md          # Phase 0 — 12 decisions (R1–R12)
├── data-model.md        # Phase 1 — entities, transitions, interface deltas
├── quickstart.md        # Phase 1 — runnable validation scenarios A–E
├── contracts/
│   ├── grammar.md       # EBNF + token delta, accepted/rejected forms
│   ├── mcp-result.md    # Tool-result layout with fix-suggestion section
│   └── failure-env.md   # RUNE_FAILED_TASK / RUNE_FAILED_EXIT_CODE contract
├── checklists/requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── token/token.go                    # + PIPEPIPE kind & name entry
├── lexer/lexer.go                    # + case '|' arm in lexOperator
├── ast/ast.go                        # + Task.FailHooks []*DepCall
├── ast/dump.go                       # + FailHooks: golden line
├── parser/task.go                    # + || clause after && clause
├── analyzer/analyzer.go              # + FailHooks in deps/expr/cycle checks; RUNE2011 check
├── diag/codes.go                     # + CodeInvalidFailureHook = "RUNE2011"
├── config/compose.go                 # + namespace rewrite of fail-hook refs
├── runtime/scheduler/scheduler.go    # + Failure type, hook orchestration, Engine methods
├── formatter/formatter.go            # + " || …" canonical emission
├── language/position.go              # + fail hooks in hover/go-to-def targets
└── cli/
    ├── run.go                        # + ExecuteFailHook/Warnf impl, diagnosing: header, env injection
    ├── dump.go                       # + "failHooks" DTO field
    └── mcp.go                        # + dedicated masked fix-suggestion buffer, cap logic

mcpserver/
├── server.go                         # + Result.FixSuggestion field
└── handler.go                        # + fix-suggestion section in formatResult

testdata/{lexer,parser,fmt,corpus}/   # golden fixtures gain || coverage
test/integration/                     # + failure-hook binary-level tests
test/docs/                            # existing harness validates doc updates
docs/                                 # GRAMMAR.md, runefile.md, how-to, mcp.md,
                                      # diagnostics.md, examples/dependencies/, indexes
```

**Structure Decision**: No new packages; every change lands in the existing
locked layout (constitution IV). The scheduler remains the orchestration
owner; the CLI engine remains the only I/O owner; `mcpserver/` stays a thin
protocol layer whose only change is one field plus its rendering.

## Complexity Tracking

No constitution violations to justify — table intentionally empty.
