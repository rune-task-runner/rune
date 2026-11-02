# Implementation Plan: Propagate the Task Body's Exit Code

**Branch**: `028-propagate-task-exit-code` | **Date**: 2026-11-01 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/028-propagate-task-exit-code/spec.md`

## Summary

Close issue #33: a failing task body must make rune exit with the body's own code,
and the MCP result must report that same code. The code already exists on the
executor errors and in `RUNE_FAILED_EXIT_CODE`. It is lost only in `cli.CodeFor`,
which maps every `TaskFailure` to 1. The fix exports the scheduler's existing
extraction as `scheduler.BodyExitCode` and has `CodeFor` use it for task failures.
That one change fixes the CLI and MCP together, because the MCP adapter computes
its code through `CodeFor`. It also keeps the hook variable and the exit code equal
by construction.

Two interpreter-executor fixes keep the result correct. A signal-killed
interpreter body reports 128 + S instead of -1. A body cancelled by Ctrl-C returns
the context error, so it exits 130 and skips `||` hooks. Today it is reported as
a task failure. Shell bodies need no change, since the bundled shell already
follows both conventions. After code review (R14), `classifyRunErr` decides an
interrupt from the run context, whatever the body returned, a `[no-exit-message]`
failure keeps the code of the error it wraps, and failure hooks get their code
from the same mapping as the exit code. Tests that pin exit 1 for `exit 7` bodies are tightened
to the exact code. The CLI reference, troubleshooting guide, MCP guide, and the
ci-cd example document the rule. The ci-cd example gains a tested `release-gate`
task that exits 2. Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (repo toolchain), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only (`mvdan.cc/sh/v3` v3.13.1 shell, standard
`os/exec`). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `internal/cli`,
`internal/runtime/scheduler`, and `internal/runtime/interp`. Binary-level tests in
`test/integration` (CLI exit codes, MCP over stdio). Docs harness in `test/docs`.

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. One `errors.As` walk per failed run.

**Constraints**: banner text byte-identical (FR-011). `mcpserver` public API and
result layout unchanged (R7). Rune's own codes 0, 2, 3, 130 unchanged (FR-005).
New prose avoids the em dash (maintainer rule).

**Scale/Scope**: 3 Go source files (`scheduler.go`, `interp.go`, `exit.go`),
12 test files (5 new), 3 docs pages plus the ci-cd example. No new packages, no DSL change, no new exported API beyond the renamed
`scheduler.BodyExitCode`.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.1.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | No skipping or caching logic touched. |
| II. Errors are a feature | ✅ PASS | The banner is unchanged and still names task, line, and status. Static analysis untouched, so validation keeps exit 3 with zero side effects. |
| III. Minimal, total DSL | ✅ PASS | No grammar, attribute, or expression change. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | Changes stay in `internal/cli`, `internal/runtime/scheduler`, `internal/runtime/interp`. No new packages. `mcpserver/` untouched. |
| V. Boringly portable | ✅ PASS | Signal detection uses an interface assertion that compiles on all three OSes with no build tags (R3). Shell semantics come from mvdan, not `/bin/sh`. The example task uses only mvdan builtins. |
| VI. Test-first, multi-layer verification | ✅ PASS | Unit (`BodyExitCode`, `CodeFor`, interp signal and cancel), binary-level (CLI codes, dependency, `[parallel]`, MCP `[exit N]`), docs harness (SC-004). Existing tests are tightened to exact codes (R8), not weakened. No golden files change. |
| VII. AI-native, secure by default | ✅ PASS | Strengthens the MCP surface: agents see the real code. No change to authorization, schemas, or masking. |
| VIII. Go engineering discipline | ✅ PASS | `errors.As` on concrete types (R1 explains why not an interface). Context errors wrapped with `%w`. No goroutines or globals added. |

**Engineering constraints**: Docker-only testing respected. Package layout
unchanged. Backward compatibility: no DSL change, so the per-file opt-in rule does
not apply. The CLI contract change ships with a breaking marker and release-notes
callout (FR-013, R11). Docs ship in the same PR.

**Post-design re-check (after Phase 1)**: no violations. Two refinements came out
of design. (1) The spec's Windows edge case now says shell bodies report 0 to 255
on every OS, an upstream library limit found in R5. (2) Research R4 found that
Ctrl-C on an interpreter body is misreported today. Fixing it is in scope because
FR-005 requires 130 and the propagation change would otherwise turn it into 137.

**Post-tasks re-check (after `/speckit-analyze`)**: no violations. (3) Research
R13 found that a `[no-exit-message]` task cancelled by Ctrl-C exits 1 today. It
is fixed in `CodeFor`, not in `runBody`, so the banner stays suppressed (FR-011).
(4) FR-010 and SC-002 now state that MCP calls rejected before the task runs
carry no `[exit N]` marker, matching contracts/exit-codes.md §4. (5) A
binary-level signal test covers FR-007 end to end on Linux and macOS.
Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/028-propagate-task-exit-code/
├── plan.md               # This file
├── spec.md               # Feature spec
├── research.md           # Phase 0: decisions R1-R12
├── data-model.md         # Phase 1: BodyExitCode and the decision table
├── quickstart.md         # Phase 1: automated gates and manual scenarios A-F
├── contracts/
│   └── exit-codes.md     # User-visible exit codes, banner, hooks, MCP marker
├── checklists/requirements.md
└── tasks.md              # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/runtime/scheduler/
├── scheduler.go            # Engine.ExitCode gives the hook's code (R14);
│                           # failureExitCode moves to cli.bodyExitCode
└── failhook_test.go        # the hook's ExitCode comes from the engine

internal/runtime/interp/
├── interp.go               # ctx.Err() check before ExitError (R4);
│                           # signal-killed → Code = 128 + S (R3)
├── interp_test.go          # cancelled context returns context error
└── interp_signal_test.go   # new, //go:build !windows: kill -TERM $$ → Code 143

internal/cli/
├── exit.go                 # CodeFor: TaskFailure → bodyExitCode;
│                           # ExitTaskFail / TaskFailure comments updated
├── run.go                  # classifyRunErr: cancelled run → Interrupted;
│                           # classify: silent failure keeps its code;
│                           # engine.ExitCode for hooks (R14)
├── exit_test.go            # new: CodeFor and bodyExitCode tables; classify;
│                           # Ctrl-C'd [no-exit-message] task → 130
└── failhook_test.go        # exit 7 body → ExitCode == 7; hook env equals code

test/integration/
├── exit_code_test.go       # new: codes 1,2,3,42,126,127,130,255; python body;
│                           # dependency; [parallel]; multiple roots;
│                           # [no-exit-message]; || hook env == exit code;
│                           # MCP [exit N] parity incl. usage and validation
├── exit_code_signal_test.go  # new, //go:build !windows: shell and python
│                           # bodies killed by SIGTERM → exit 143
├── us1_test.go             # exit 7 → 7
├── error_banner_test.go    # exit 7 → 7 (styled/plain, plain, [no-exit-message])
├── failure_hooks_cli_test.go  # exit 7 → 7 (header, hook-fails)
└── failure_hooks_mcp_test.go  # "[exit " → "[exit 7]"

test/docs/
└── exitcode_test.go        # new: ci-cd release-gate closed → 2, default → 0

docs/cli.md                 # exit-code table + overlap note
docs/troubleshooting.md     # table + "A task body failed" section
docs/mcp.md                 # [exit N] carries the body's code
docs/examples/ci-cd/Runefile   # + release-gate task
docs/examples/ci-cd/README.md  # exit-code line + CI branching snippet
```

**Structure Decision**: No new packages. The code is decided once, in
`cli.CodeFor`, and failure hooks get theirs through the scheduler's `Engine`.
Both executors stay the source of truth for the number. The interpreter executor
gains the two conventions the shell executor already has.

## Out of Scope (tracked separately)

- Exit codes of agent-executor tasks (spec assumption, R12).
- Windows shell-body codes above 255: mvdan's `ExitStatus` is `uint8` (R5).
  Worth an upstream issue if a user hits it.

## Complexity Tracking

No constitution violations to justify; table intentionally empty.
