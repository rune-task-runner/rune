# Implementation Plan: Safe Quoting for Task Parameters

**Branch**: `030-safe-param-quoting` | **Date**: 2026-11-03 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/030-safe-param-quoting/spec.md`

## Summary

Close issue #35. Today a variadic parameter is joined with spaces at bind time, on
the command line and over MCP, so no later step can tell `["a b"]` from
`["a", "b"]`. `{{names}}` then pastes the joined text unquoted, and
`{{quote(names)}}` quotes it as one word.

The plan keeps the elements. A new `eval.Args` value carries `Values` (every
parameter, variadics joined as today) and `Lists` (the variadic's elements)
through binding, the scheduler, and the engine (R1). A new `quote_each()` built-in
reads `Lists` for a bare variadic reference and quotes each element; anything else
falls back to `quote()` (R2). MCP arrays reach the engine through an optional
`mcpserver.ListEngine` interface, so the public `Engine` is unchanged (R3).
Forwarding `(test pkgs)` spreads the caller's elements into the target's variadic
slot only (R4). Run-once keys and cache fingerprints include the elements (R5).

For safety by default, a new analyzer pass reports RUNE2017 when a string or path
parameter reaches a shell body outside `quote()`/`quote_each()` (R6, R7). It runs
in `rune analyze` and the language server only, never on the run path. A new
`raw` kind declares intended word splitting and silences the warning; MCP schemas
tell agents about it (R8). Docs and examples move to the safe forms, with a docs
test that fails on any RUNE2017 (R10).

Constitution Principle III freezes the expression sublanguage, so the plan amends
it to v1.2.0 to let the built-in library grow under stated conditions (R9). The
work ships as two PRs so the changelog, which reads PR titles only, names every
user-visible change (R11). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0`
builds

**Primary Dependencies**: existing only (`mvdan.cc/sh/v3` v3.13.1,
`github.com/modelcontextprotocol/go-sdk` for MCP). No new modules.

**Storage**: the existing `[cache]` fingerprint record; one extra `Vars` entry per
referenced variadic (R5). No format change.

**Testing**: `go test` in Docker only. Unit tests in `internal/eval`,
`internal/cli`, `internal/runtime/scheduler`, `internal/analyzer`,
`internal/language`, `mcpserver`. Goldens: formatter, corpus (new fixture only).
Binary-level tests in `test/integration` (CLI and MCP over stdio). Docs harness in
`test/docs`. Tests that need bash skip on Windows and when bash is missing, as in
spec 029.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. The analyzer pass walks each interpolation once;
binding copies one slice per variadic.

**Constraints**: Interpolated text for Runefiles that use neither `quote_each`
nor `raw` stays byte-identical (FR-011). `mcpserver.Engine` keeps its signature.
Schemas for tasks without a `raw` parameter stay byte-identical (023 SC-003).
New prose avoids the em dash (maintainer rule).

**Scale/Scope**: about 15 Go source files changed in existing packages
(`eval`, `cli`, `runtime`, `runtime/scheduler`, `analyzer`, `analysis`, `ast`,
`diag`, `language`, `mcpserver`), 2 new source files (`internal/eval/args.go`,
`internal/analyzer/quoting.go`), about 6 new test files,
2 new fixtures, about 20 docs and example files, and the constitution. No new
packages.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.1.0, with the
amendment in R9 applied in the same PR as `quote_each`.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | No skipping logic added. `[cache]` tasks that reference a variadic get a new fingerprint and run once, the safe direction (R5). |
| II. Errors are a feature | ✅ PASS | RUNE2017 carries `file:line:col`, a caret span, and the exact fix. The forwarding error is today's usage error, raised before the target body runs. |
| III. Minimal, total DSL | ✅ PASS after amendment | `quote_each` adds no syntax and is total and pure, but it grows the built-in library, which v1.1.0 freezes. R9 amends Principle III (v1.2.0, MINOR) so the library may grow under stated conditions; `quote_each` meets them. `raw` is a parameter annotation, which v1.1.0 already allows, and ships with grammar docs and fixtures. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | No new packages. `raw` reuses the hand-written annotation parsing. `mcpserver` stays public; the change is an additive optional interface (R3). |
| V. Boringly portable | ✅ PASS | `quote_each` emits POSIX quoting, which the built-in `mvdan.cc/sh` shell reads the same on all three OSes. No build tags. |
| VI. Test-first, multi-layer verification | ✅ PASS | Unit, golden (formatter, new corpus fixture), binary level (CLI, MCP stdio), and a docs test that fails on any RUNE2017 in docs. Existing corpus fixtures are untouched. |
| VII. AI-native, secure by default | ✅ PASS | Agent arrays keep their boundaries, so `quote_each` stops injection from agent input. The warning flags every unquoted string or path parameter. A `raw` parameter is announced in the tool schema. |
| VIII. Go engineering discipline | ✅ PASS | One value type instead of hidden map keys (R1). No goroutines, no globals. Optional-interface upgrade by type assertion is the standard library's pattern (R3). |

**Engineering constraints**: Docker-only testing respected. Package layout
unchanged; `internal/analyzer` newly imports `internal/runtime` for
`IsKnownShell`, with no cycle (R7). Backward compatibility: no existing syntax
changes meaning. `x:raw` was a RUNE2012 error, so the new kind turns an error into
a valid file. `{{names}}`, `{{quote(names)}}`, and scalar interpolation produce the
same text (FR-010, FR-011). Forwarding keeps v0.6.0's text for the target's
variadic, and any dependency call v0.6.0 accepted, including an empty `*` list
forwarded into a `+` parameter, still binds, so no Runefile that ran before fails
or changes text (R4).
Docs and grammar ship with the surface changes in each PR.

**Post-design re-check (after Phase 1)**: no violations. Design changed the spec
in these places:

1. FR-012: a reference inside a `quote()` or `quote_each()` call at any depth is
   safe, not only when the call is outermost, so `{{"--env=" + quote(env)}}` is
   not warned (R6). Story 3 gains that example.
2. FR-018 and Story 4: the language server has no kind completion for any kind,
   so the completion clause is dropped; hover explains `raw` (R8).
3. FR-022: the changelog is built from PR titles only, so the requirement now
   applies to the two PR titles (R11).
4. Assumptions: the constitution change is a MINOR amendment to v1.2.0, following
   spec 023's precedent, not a PATCH (R9).
5. SC-003: "no edits" applies to existing corpus fixtures; the `raw` kind adds a
   new fixture.
6. Edge cases: `[cache]` tasks that reference a variadic run once after the
   upgrade (R5).

Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/030-safe-param-quoting/
├── plan.md                          # This file
├── spec.md                          # Feature spec
├── research.md                      # Phase 0: baseline and decisions R1-R11
├── data-model.md                    # Phase 1: Args, kinds, shell body, RUNE2017, cache delta
├── quickstart.md                    # Phase 1: automated gates and manual scenarios A-I
├── contracts/
│   ├── runefile-surface.md          # quote_each, raw, forwarding, identity
│   ├── diagnostic-rune2017.md       # rule, message, 28-row fixture
│   └── mcp.md                       # schema delta, ListEngine, element mapping
├── checklists/requirements.md
└── tasks.md                         # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

PR 1, elements and `quote_each` (US1, US2, US5):

```text
internal/eval/
├── args.go                 # new: Args{Values, Lists}, Key() (R1, R5)
├── eval.go                 # Scope.Lists, WithArgs
└── builtins.go             # quote_each registration and list case in callBuiltin (R2)

internal/cli/
├── args.go                 # bindParams/bindNamedParams return eval.Args; lists from MCP
├── run.go                  # engine methods take eval.Args; ResolveDep forwarding (R4);
│                           # collectVars adds <name>[] entries (R5)
└── mcp.go                  # mcpAdapter.CallLists; Call delegates

internal/runtime/scheduler/
├── scheduler.go            # Engine interface, Invocation, memo key via Args.Key()
└── parallel.go             # signature follow-through

internal/language/
└── builtin.go              # quote_each entry

mcpserver/
├── server.go               # ListEngine interface
└── handler.go              # decodeArgs returns lists; handler picks CallLists (R3)

.specify/memory/constitution.md   # v1.2.0, Principle III built-in library clause (R9)
docs/runefile.md                  # quote_each in the built-in table; forwarding note
docs/how-to/parameters.md         # variadic example uses quote_each
```

PR 2, warning and `raw` (US3, US4):

```text
internal/ast/ast.go               # KindRaw, "raw" in SupportedConstraintKinds (R8)
internal/diag/codes.go            # CodeUnquotedParam = "RUNE2017"
internal/runtime/executor.go      # export IsKnownShell (R7)
internal/analyzer/quoting.go      # new: CheckQuoting pass (R6, R7)
internal/analysis/service.go      # append CheckQuoting
internal/language/hover.go        # raw explanation line
mcpserver/server.go               # raw description in inputSchema (R8)

testdata/corpus/raw-kind.rune     # new corpus fixture + .ast golden
testdata/fmt/typed-params.rune    # raw lines + .fmt golden
testdata/lsp/basic/Runefile       # unchanged; tests asserting its diagnostics updated

test/integration/quoting_test.go  # new (PR 1 part: CLI, MCP, forwarding;
                                  # PR 2 part: analyze --json, raw over MCP)
test/docs/quoting_test.go         # new: no RUNE2017 in examples, pages, Runefile

docs/GRAMMAR.md, docs/diagnostics.md, docs/mcp.md, docs/runefile.md,
docs/how-to/parameters.md, README.md, docs/getting-started.md,
docs/troubleshooting.md, docs/how-to/{os-filtering,dependencies-and-hooks,settings-and-dotenv}.md,
docs/examples/*/Runefile and README.md   # safe forms, raw, RUNE2017
```

**Structure Decision**: No new packages. `eval` owns `Args` because `Scope` reads
it; the scheduler only passes it through and asks for its key. The warning is a
separate analyzer pass, the pattern `CheckDocumentation` set, so it reaches
`rune analyze` and the editor through `analysis.Service` and stays off the run
path.

## Out of Scope (tracked separately)

- Quoting for PowerShell or `cmd`. `quote()` and `quote_each()` stay POSIX.
- Kind completion in the language server, for any kind.
- A setting that makes `rune analyze` exit non-zero on warnings.
- Evaluating non-literal `set shell` elements in the analyzer (R7).
- Argument injection through values that start with `-`.

## Complexity Tracking

No constitution violations to justify; table intentionally empty.
