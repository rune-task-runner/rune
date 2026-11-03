# Implementation Plan: Honor the `-c` Flag in `set shell`

**Branch**: `029-shell-c-flag` | **Date**: 2026-11-02 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/029-shell-c-flag/spec.md`

## Summary

Close issue #34. Under `set shell`, rune runs `<list...> <tmpfile>`. When the list
contains `-c`, the shell treats the temp-file path as its command string. bash then
runs the file as a separate child script, so `-e`, `-u`, and `pipefail` never reach
the body (research baseline table).

The fix removes a known shell's command-string flag from the command before it
runs, when the list gives the shell no code of its own. A lone `-c` is dropped,
and `c` is removed from a cluster such as `-cu` or `-euc`. A wrapper's `-c`
(`nix develop -c bash`), a list with its own code (`bash -c "<code>"`), and
programs that are not shells (`php -c`, `jq -c`, `node -c`) are left alone. The
body still runs from the temp file. This happens in `runtime.Select`, for the
`set shell` and `[script]` branches only, through one helper that returns a new
slice. `interp.Run` does not change.

A list with `-c` then runs exactly like the same list without it (FR-003), including
`$0`. The body is never placed on a command line, so interpolated secrets stay out
of the process list, and no size limit is added (research R1).

The docs examples gain `-e`, and a new "Shell override" subsection in the Runefile
reference explains how a `set shell` body runs. The change ships under a
`fix(runtime)!:` PR title that names the affected pattern. Full decisions are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25.0` in `go.mod`), pure Go, `CGO_ENABLED=0`
builds

**Primary Dependencies**: existing only (`mvdan.cc/sh/v3` v3.13.1 for the built-in
shell, standard `os/exec` for interpreters). No new modules.

**Storage**: N/A

**Testing**: `go test` in Docker only. Unit tests in `internal/runtime`.
Binary-level tests in `test/integration` (CLI and MCP over stdio). Docs harness in
`test/docs`. Behavior tests need bash, so they skip on Windows and when bash is not
on `PATH` (research R7).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: N/A. One pass over a short string slice per interpreter
body run.

**Constraints**: `settings.Shell` is shared across tasks and must not be changed in
place. `Selection.Display` and the failure banner stay unchanged. The python, node,
custom, and agent executors stay unchanged (FR-008). New prose avoids the em dash
(maintainer rule).

**Scale/Scope**: 3 Go source files (`internal/runtime/executor.go`,
`internal/language/builtin.go`, and a field comment in
`internal/config/settings.go`), 3 test files (2 new), 3 docs pages. No new
packages, no DSL change, no new exported API.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.1.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | ✅ PASS | No skipping logic added. `[cache]` tasks under a `-c` list get a new fingerprint and run once, the safe direction (R5). |
| II. Errors are a feature | ✅ PASS | A body that failed silently now fails with today's banner and a non-zero exit. Static analysis is untouched. |
| III. Minimal, total DSL | ✅ PASS | No grammar, attribute, or expression change. |
| IV. Hand-written front end, idiomatic Go | ✅ PASS | The change stays in `internal/runtime`. No new packages. `mcpserver/` is untouched. |
| V. Boringly portable | ✅ PASS | The default executor is still `mvdan.cc/sh`. The rule is pure string handling with no build tags. Behavior tests skip on Windows rather than rely on Git Bash or WSL (R7). |
| VI. Test-first, multi-layer verification | ✅ PASS | Unit (flag rule, `Select`), binary level (repro, flag forms, `[script]`, stdin, state, MCP, equivalence matrix), docs harness (every `set shell` example). No golden files change. The corpus needs no edit (R6). |
| VII. AI-native, secure by default | ✅ PASS | MCP results report the real failure. The body stays in a temp file readable only by the user. The rejected alternative would have exposed interpolated secrets on the command line (R1). |
| VIII. Go engineering discipline | ✅ PASS | One small unexported helper, no goroutines, no globals, and the input slice is not mutated. |

**Engineering constraints**: Docker-only testing respected. Package layout
unchanged. Backward compatibility: no grammar change, so the per-file opt-in rule
does not apply. The Runefile already declares the flags. Only lists in which the
shell would read rune's script path as its code change. A `-c` that belongs to a
wrapper or a non-shell program, or that is followed by the list's own code, is left
alone (R3), so no working list changes meaning. A task that newly fails does so
loudly. The release notes call the change out through a breaking commit (FR-010,
R6). Docs ship in the same PR.

**Post-design re-check (after Phase 1)**: no violations. Design made three changes
to the spec:

1. FR-002 now says every *other* flag applies, because the command-string flag
   itself is removed (R1).
2. SC-001 is narrowed to Linux and macOS. On Windows CI runners, `bash` on `PATH`
   can be the WSL launcher or Git Bash (R7).
3. The "Script name" edge case now says `$0` and the shell's own messages match the
   list without `-c`, since the design makes them identical (R4).

`/speckit-analyze` (2026-11-03) found that the first rule, which read any `-c`
after index 0 as the flag, would change working lists: wrappers such as
`nix develop -c bash`, lists that give the shell its own code, and `[script]`
programs such as `php -c <ini>` or `node -c`. FR-001 now requires a known shell
and no code after the flag (R3). FR-003 and FR-007 were aligned with the design,
and FR-010's second point moved into the PR title (R6).

Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/029-shell-c-flag/
├── plan.md                    # This file
├── spec.md                    # Feature spec
├── research.md                # Phase 0: baseline and decisions R1-R8
├── data-model.md              # Phase 1: shell list, flag, effective command
├── quickstart.md              # Phase 1: automated gates and manual scenarios A-H
├── contracts/
│   └── shell-override.md      # User-visible behavior under set shell and [script]
├── checklists/requirements.md
└── tasks.md                   # Phase 2 (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/runtime/
├── executor.go             # commandWithoutStringFlag; Select calls it in the
│                           # set shell and [script] branches (R2, R3)
└── executor_test.go        # rule table (R3), input not mutated, Select for
                            # set shell / [script] with -c, python -c,
                            # [script("node -c")], [script("php -c ...")] unchanged

internal/config/
└── settings.go             # Shell field comment shows a list with -e

internal/language/
└── builtin.go              # `shell` hover text names one-script runs and -e (R8)

test/integration/
└── shell_override_test.go  # new: repro, -cu unset var, -euc (exit 3), pipefail,
                            # [script("bash -euc")], stdin, || failure hook,
                            # state across lines, own command string, MCP
                            # [exit 1], with/without -c equivalence matrix

test/docs/
└── shell_override_test.go  # new: every `set shell` line in docs/ has -e and
                            # stops at a failing middle line; the Shell override
                            # heading and links to it exist

docs/runefile.md            # settings example gains -e; new "Shell override"
                            # subsection under Executors (FR-009)
docs/how-to/settings-and-dotenv.md  # example gains -e; pitfall links the subsection
docs/how-to/executors.md    # set shell pitfall links the subsection
```

**Structure Decision**: No new packages. `runtime.Select` already turns
`set shell` and `[script]` into a command, so it is the one place that applies the
rule. `interp.Run` stays a generic "run this command on this file" helper shared
with python and node.

## Out of Scope (tracked separately)

- Wrapper shell lists that run the body elsewhere, such as
  `["docker", "exec", "ctr", "sh", "-c"]`. The temp file is on the host, so these
  never worked. Supporting them needs the body on the command line, which R1
  rejects. A wrapper that runs the body on the same host
  (`["nix", "develop", "-c", "bash", "-eu"]`) keeps working, because its `-c`
  comes before the shell's name.
- Windows command-string syntax (`cmd /C`, PowerShell `-Command`).
- A warning for a shell list without `-e` (spec assumption).

## Complexity Tracking

No constitution violations to justify; table intentionally empty.
