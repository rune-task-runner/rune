# Implementation Plan: Interactive-Only Approval for `[confirm]` Tasks

**Branch**: `027-confirm-tty-gate` | **Date**: 2026-10-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/027-confirm-tty-gate/spec.md`

## Summary

Close issue #30: a `[confirm]` task must not be approvable by piping `y` into
the CLI. The CLI classifies stdin once per invocation (terminal or not, via
the `go-isatty` helper the picker already uses) and stores it on the engine.
`(*engine).confirm` becomes a three-way decision: with a terminal it prompts
exactly as today; with a non-terminal stdin it declines without reading and
returns an explanatory error that `cmd/rune` prints as the standard `rune:`
failure banner (`... stdin is not a terminal; pass --yes to approve`); with no
stdin at all (the MCP adapter) it keeps today's silent decline, so MCP output
stays byte-identical. `--yes`, plan modes, exit codes, and failure-hook
semantics are unchanged. Interactive approval is proven end to end with a
pseudo-terminal integration test (`creack/pty`, test-only). Help text, hover
docs, and `docs/` state the terminal rule; the PR title carries the breaking
change callout for the release notes. Full decisions in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (repo toolchain), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only for the shipped binary
(`mattn/go-isatty`, `spf13/cobra`). Test-only addition:
`github.com/creack/pty` v1.1.24, imported from `_test.go` files under a
`!windows` build tag, never linked into `rune`.

**Storage**: N/A

**Testing**: `go test` in Docker only (`docker-compose run --rm test …`).
Unit tests in `internal/cli` (engine built directly), binary-level tests in
`test/integration` (pipes and a pseudo-terminal), docs harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (single static binary)

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: one terminal check per invocation; no measurable cost

**Constraints**: MCP results byte-identical (FR-010); `--yes` output unchanged
(FR-006); styled and plain banner identical after ANSI stripping; stdin never
read on the non-interactive path (FR-003, SC-004)

**Scale/Scope**: about 8 source files and 3 docs files; no new packages, no
DSL change, no new exported API

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.1.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | No skipping logic added; the decline is visible and explained. |
| II. Errors are a feature | ✅ PASS | The runtime decline gains an actionable message naming the task, its question, the reason, and the fix (`--yes`). Static analysis is untouched. |
| III. Minimal, total DSL | ✅ PASS | No grammar, attribute, or expression change. `[confirm]` keeps its syntax and meaning; only who may answer changes. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | Changes stay in `internal/cli`, `cmd/rune`, `internal/language`; no new packages. |
| V. Boringly portable | ✅ PASS | `go-isatty` covers the Windows console and MSYS/Cygwin terminals (FR-008). `creack/pty` is test-only, pure Go, excluded on Windows by build tag. |
| VI. Test-first, multi-layer verification | ✅ PASS | Unit, binary, and real-terminal layers (R8); docs harness covers the doc edits; the existing styling test moves to a real terminal instead of being weakened. |
| VII. AI-native, secure by default | ✅ PASS | Strengthens it: `[confirm]` now guards the CLI against agents that have a shell, matching the MCP gate. The MCP surface is untouched. |
| VIII. Go engineering discipline | ✅ PASS | Error stays wrapped (`bodyNotRunError` → `ErrBodyNotRun`, `errors.Is`); no goroutines added; the engine field replaces any package global. |

**Engineering constraints**: Docker-only testing respected. Package layout
unchanged. Backward compatibility: no DSL change, so the per-file opt-in rule
does not apply; the CLI behavior change is called out in the release notes
(FR-013, R10). Docs ship in the same PR.

**Post-design re-check (after Phase 1)**: no violations. One spec refinement
made during design: FR-011 now names the standard `rune:` failure banner
instead of warning styling (R4), since the run fails and the banner is the
existing convention. Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/027-confirm-tty-gate/
├── plan.md               # This file
├── spec.md               # Clarified spec (2 clarifications, 2026-10-28)
├── research.md           # Phase 0: decisions R1-R11
├── data-model.md         # Phase 1: stdin classification and decision table
├── quickstart.md         # Phase 1: automated gates and manual scenarios A-E
├── contracts/
│   └── confirm-cli.md    # User-visible prompt, banner, hook warning, help text
├── checklists/requirements.md
└── tasks.md              # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/cli/
├── terminal.go             # new: isTTY (moved from choose.go) + isTerminal(stream)
├── choose.go               # interactiveTerminal rewritten on isTerminal
├── run.go                  # engine.stdinTTY; confirm → three outcomes;
│                           # Execute / ExecuteFailHook map "no terminal" to the
│                           # explanatory error; execute() sets stdinTTY
├── confirm_test.go         # new: unit tests for the decision table
└── failhook_test.go        # unchanged (MCP nil-stdin decline must still pass)

cmd/rune/root.go            # --yes help text
internal/language/builtin.go  # [confirm] hover documentation

test/integration/
├── confirm_test.go         # new: piped/file/empty/unbounded stdin, dependency,
│                           # failure hook, banner styled vs plain, --yes
│                           # stdin pass-through, --dry-run/--summary
├── confirm_pty_test.go     # new, //go:build !windows: real-terminal approve,
│                           # decline, stdout redirected, prompt styling
└── status_styling_test.go  # TestConfirmPromptStyled moves to confirm_pty_test.go

go.mod / go.sum             # + github.com/creack/pty v1.1.24 (test-only use)

docs/cli.md                 # --yes row: terminal rule, piped input never approves
docs/runefile.md            # [confirm] line + short paragraph
docs/examples/ci-cd/README.md  # verify wording only
```

**Structure Decision**: No new packages. The decision stays in the CLI engine,
which already owns all I/O; the scheduler and `mcpserver/` are untouched. The
terminal helper moves into its own small file because two features (picker and
confirm) now share it.

## Out of Scope (tracked separately)

- **`rune mcp --yes` cannot run `[confirm]` tasks** (research R11): the
  adapter builds its engine without `Yes` and without stdin, so the engine
  declines after the server has allowed the call. This plan keeps that path
  byte-identical (FR-010). File a separate issue.
- Rendering a BREAKING marker in `cliff.toml` (R10).

## Complexity Tracking

No constitution violations to justify; table intentionally empty.
