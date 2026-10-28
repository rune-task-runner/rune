# Research: Interactive-Only Approval for `[confirm]` Tasks

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md) | **Date**: 2026-10-28

Every decision below was made against the current code: `internal/cli/run.go`
(`(*engine).Execute`, `ExecuteFailHook`, `confirm`), `internal/cli/choose.go`
(`isTTY`, `interactiveTerminal`), `internal/cli/mcp.go` (adapter `Call`),
`internal/cli/banner.go`, and `cmd/rune/root.go`.

## R1. What counts as "interactive"

**Decision**: Standard input is interactive when `Options.Stdin` is an
`*os.File` whose descriptor is a terminal according to the existing `isTTY`
helper (`isatty.IsTerminal || isatty.IsCygwinTerminal`). The state of stdout
and stderr is ignored.

**Rationale**: The spec ties approval to stdin alone (FR-001, Story 2
scenario 3). `go-isatty` is already a direct dependency used by the picker and
by color resolution, and it covers the Windows console plus MSYS/Cygwin pty
pipes (FR-008). `cmd/rune` sets `opts.Stdin = cmd.InOrStdin()`, which is
`os.Stdin` (an `*os.File`), and `maskOptions` wraps only stdout/stderr, so the
type check holds on the real CLI path.

**Alternatives considered**:
- Require stderr to be a terminal too: rejected. It would break
  `rune deploy 2>&1 | tee deploy.log`, a common pattern where a person is
  present and sees the prompt through `tee`.
- `golang.org/x/term.IsTerminal`: rejected. Only an indirect dependency, and it
  does not detect MSYS/Cygwin terminals.

## R2. Where the decision is made

**Decision**: Detect once per invocation, when `execute()` builds the engine,
and store the result in a new unexported engine field `stdinTTY bool`. The
`isTTY` helper moves to a new `internal/cli/terminal.go` alongside a small
`isTerminal(stream any) bool` helper; `interactiveTerminal` (picker) is
rewritten on top of it so both features share one definition.

**Rationale**: One truth per run, cheap, and it gives tests a seam. Unit tests
in `internal/cli` already construct `&engine{...}` directly
(`availability_test.go`, `mcp_test.go`), so they can set `stdinTTY: true` with
a `strings.Reader` answer and exercise the interactive branch on every OS.

**Alternatives considered**:
- Detect inside `confirm` on every call: rejected. No test seam without a
  package-level variable, and repeated syscalls for no benefit.
- An exported `Options` field to force terminal mode: rejected. It would put a
  test hook on the public CLI options struct that embedders could misuse to
  re-open the bypass.

## R3. Decision outcomes and the MCP path

**Decision**: `confirm` returns one of three outcomes instead of a bool:

| Stdin | Outcome | Error |
|---|---|---|
| `nil` (MCP adapter, embedders) | declined | today's silent `task "X" was not confirmed` |
| non-nil, not a terminal | no terminal | new explanatory error (R4), stdin never read |
| terminal | prompt, then approved or declined | declined stays silent, as today |

**Rationale**: The MCP adapter always passes `Stdin: nil`. Keeping the `nil`
branch byte-identical guarantees FR-010: MCP results, including
`TestCallConfirmDeclineDoesNotFireFailHooks`, see no change. On the CLI,
`os.Stdin` is never `nil` (even when descriptor 0 is closed), so every
non-terminal CLI run takes the "no terminal" branch.

**Alternatives considered**: Treat `nil` as "no terminal" too: rejected. The
new message would appear in MCP failure-hook warnings, a visible MCP change the
spec forbids.

## R4. How the non-interactive decline is reported

**Decision**: Return a non-silent `TaskFailure` (still wrapped in
`bodyNotRunError`) whose message is:

```text
task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve
```

`cmd/rune` renders it through the existing failure banner, so the user sees one
line: `rune: task "apply" was not confirmed (...)...`. With no prompt text the
parenthesized part is the default question, `Run "apply"?`. No `[y/N]` prompt
is printed.

**Rationale**: The run fails with exit 1, and the `rune:` banner is the
established convention for a failed run. It reuses the banner's styling
contract (styled prefix, plain message, honors `--color`/`NO_COLOR`), the
masking path (`maskErr`), and the watch loop's banner handling
(`BannerMessage`). The MCP adapter never prints banners, which keeps MCP output
unchanged. `[no-exit-message]` does not suppress this banner: that attribute
governs body failures, and here the body never started.

**Spec impact**: FR-011 said "warning styling". It was a default chosen during
`/speckit-specify`, not a user decision, and the banner fits the existing
conventions better. FR-011 is updated to name the standard failure banner.

**Alternatives considered**:
- A separate `warning:` line plus a silent failure: rejected. That would add a
  second visual convention for "the run failed" and print mid-run instead of as
  the final word.
- Printing the prompt and then declining (today's empty-stdin behavior):
  rejected by FR-004. The prompt asks a question nobody can answer.

## R5. Failure hooks

**Decision**: `ExecuteFailHook` returns the same explanatory error. The
scheduler's existing `Warnf("failure hook %s failed: %v")` prints it as the
single line:

```text
warning: failure hook diagnose failed: task "diagnose" was not confirmed (Run "diagnose"?): stdin is not a terminal; pass --yes to approve
```

**Rationale**: It matches the spec's edge case ("surfaces as today's one-line
hook warning") and keeps exactly one message. The original failure's exit code
is preserved by the scheduler, as today.

## R6. Exit status and failure semantics

**Decision**: Unchanged. A non-interactive decline is a `TaskFailure`, so the
exit code is 1 (`ExitTaskFail`). The `bodyNotRunError` wrapper keeps
`scheduler.ErrBodyNotRun` in the chain, so the task's own `||` hooks do not
fire, and the scheduler stops dependents as it does for any failed task.
Timing is "decline when reached" (Clarification Q1): no up-front scan.

**Note on `[parallel]`**: if several `[confirm]` dependencies run
concurrently, the first error cancels the group, so the banner names one task.
No bodies run either way, which satisfies the spec's edge case.

## R7. `--yes`

**Decision**: No change. `Execute` and `ExecuteFailHook` test `e.opts.Yes`
before calling `confirm`, so stdin is never touched and no notice is printed
(FR-006, Clarification Q2). A regression test pins the stdin pass-through
(SC-004): `printf 'payload' | rune --yes t` where `t` is `[confirm]` and runs
`cat`, and the full payload reaches stdout.

## R8. Test strategy

**Decision**: Three layers, test-first:

1. **Unit (`internal/cli`, all OSes)**: engine built directly with
   `stdinTTY` true or false. Covers: approve on `y`/`yes`/`YES`, decline on
   `n`/empty/EOF, no-terminal error text with and without prompt text,
   no stdin read in the no-terminal branch (a reader that fails the test if
   read), `nil` stdin keeps the old silent error, the failure-hook path, and
   `ErrBodyNotRun` stays in the chain.
2. **Binary integration (`test/integration`, all OSes)**: piped `y`, piped
   `YES`, an unbounded `y\n` stream, a file containing `y`, empty stdin, a
   `[confirm]` dependency, a `[confirm]` failure hook, the banner styled vs
   plain (`--color=always`/`never`), `--yes` stdin pass-through, and
   `--dry-run`/`--summary` (no banner, exit 0).
3. **Real terminal (`test/integration`, `//go:build !windows`)**: the binary
   runs with stdin on a pseudo-terminal from `github.com/creack/pty` v1.1.24
   (test-only), stdout/stderr on pipes. Covers: `y` approves, `n` declines,
   stdout redirected still approves, and the migrated
   `TestConfirmPromptStyled` (prompt shape and styling now need a terminal).

Windows interactive approval is covered by layer 1 plus the shared `isTTY`
helper the picker already relies on; the quickstart lists a manual Windows
check.

**Rationale**: Layer 3 is the only one that proves the real binary still sees
`os.Stdin` as a terminal. If a future change wrapped stdin in a non-file
reader, interactive approval would silently break and only a real terminal
test would catch it (SC-002).

**Alternatives considered**:
- `script(1)`: rejected. GNU and BSD argument syntax differ and it is not
  guaranteed in every CI image.
- Opening `/dev/ptmx` by hand with `golang.org/x/sys/unix`: rejected. Linux
  and macOS need different ioctls; that is what `creack/pty` already packages.
- No real terminal test: rejected for the reason above.

`creack/pty` is imported only from `_test.go` files, so it is never linked into
the shipped binary, and it needs no cgo.

## R9. Docs and help text

**Decision**: Update every place that describes `[confirm]` or `--yes` for the
CLI (FR-012):

| Surface | Change |
|---|---|
| `cmd/rune/root.go` `--yes` help | "approve [confirm] tasks without prompting (required when stdin is not a terminal)" |
| `docs/cli.md` flag table | same meaning, plus "piped or redirected input never approves" |
| `docs/runefile.md` `[confirm]` line and a short paragraph | prompt needs a terminal; `--yes` otherwise |
| `internal/language/builtin.go` hover | "Ask for confirmation at a terminal before running; --yes approves non-interactively (maps to the MCP destructive hint)." |
| `docs/examples/ci-cd/README.md` | already says unattended runs need `--yes`; verify only |

The two MCP-context mentions (`docs/examples/agent-driven/README.md`,
`docs/use-cases/mcp-agents.md`) describe `rune mcp --yes` and stay as they are.
The docs site is generated from `docs/` (spec 024), so it needs no separate
change.

## R10. Release notes

**Decision**: The squash-merge PR title carries the callout, because
`cliff.toml` renders only the commit description (the `!` is dropped) and has
no breaking-change marker. The description therefore starts with the word
itself. Title: `fix(cli)!: breaking: [confirm] needs a terminal, piped input
no longer approves (use --yes)`, which renders under "Fixed" as "Breaking:
[confirm] needs a terminal, piped input no longer approves (use --yes)". The
`!` still drives the pre-1.0 minor bump in release-guru.

**Alternatives considered**: Teaching `cliff.toml` to render a BREAKING
marker: rejected here. It changes release tooling owned by spec 006 and would
also re-style past entries; worth its own change.

## R11. Pre-existing defect found (out of scope)

`rune mcp --yes` sets `mcpserver.Options.AllowDestructive`, so the server
accepts a call to a `[confirm]` task, but `mcpAdapter.Call` builds the engine
with `Options{Stdin: nil, ...}` and no `Yes`. `Execute` then calls `confirm`,
which declines on `nil` stdin. Net effect: over MCP, `[confirm]` tasks can never
run, even with `--yes`. This feature leaves the `nil`-stdin branch untouched
(FR-010), so it neither fixes nor worsens this. It should be tracked as a
separate issue.
