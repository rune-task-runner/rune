# Implementation Plan: A File Flag for the MCP Server

**Branch**: `048-mcp-file-flag` | **Date**: 2026-11-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/048-mcp-file-flag/spec.md`

## Summary

`rune serve` and `rune mcp` gain `-f`/`--file`. The server already loads the Runefile
that `Options.File` names and already runs tasks in that file's directory; what is
missing is the flag, which today is registered on the root command only. The change
registers a flag of the same name on the `serve` command, bound to the same field, and
rejects an empty value before anything is loaded. No other root flag is touched, so
`--yes` stays unknown to the server. Tests pin both: the flag in every form, and the list
of root flags the server must not have.

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: `github.com/spf13/cobra` and `pflag`, already used for every
command. No new dependency.

**Storage**: none.

**Testing**: `go test` inside Docker (`docker compose run --rm -e GOFLAGS=-buildvcs=false
test go test ./...`). Unit tests in `cmd/rune` (package `main`) for the command's flags.
End-to-end tests in `test/integration`, which builds the `rune` binary once and drives
`rune mcp` over stdio with the existing `mcpProc` helper. `test/docs` for the guide, the
command reference and the troubleshooting page.

**Target Platform**: Linux, macOS, Windows. The end-to-end tests avoid `pwd` and compare
no path text, so they run unchanged on Windows.

**Project Type**: CLI.

**Performance Goals**: none. One more flag is parsed at start.

**Constraints**: `--yes` and every other root flag stay unavailable to the server
(FR-010, FR-011). The flag reaches no other command (FR-013). A server started without the
flag behaves exactly as before (FR-005): its startup line, its tool list and every
existing test stay as they are.

**Scale/Scope**: about 15 lines in `cmd/rune/serve.go`, a five-line `addCommands` in
`cmd/rune/main.go` that the guard tests share with `main`, about 120 lines of unit tests,
about 250 lines of end-to-end tests, and about 50 lines across three documentation pages.
`internal/cli` and `mcpserver` are not changed.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.3.0.*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | No task behavior changes. |
| II. Errors are a feature | PASS | An empty `--file` is a usage error with its own message and exit code 2, before any Runefile is read. A missing file and a directory keep the messages `rune -f` gives. |
| III. Minimal, total DSL | PASS | No DSL change. |
| IV. Hand-written front end, idiomatic Go | PASS | No new package. The change is in `cmd/rune`, where the other `serve` flags are. |
| V. Boringly portable | PASS | Path handling is the existing `config.Resolve`, which uses `filepath`. The tests use no Unix-only command. |
| VI. Test-first, multi-layer verification | PASS | Each test is written and seen failing before the change that makes it pass: unit tests for the flag, binary-level tests for the behavior. The documentation is under `test/docs`. |
| VII. AI-native, secure by default | PASS | The reason for the design. The server gains one flag by name; it does not inherit the root command's flags, so no switch that approves all `[confirm]` tasks reaches the agent surface. A test lists every root flag and fails if one becomes a server flag unannounced. |
| VIII. Go engineering discipline | PASS | No goroutine, no global, no `init()`. Errors are `*cli.UsageError`, as the command's other usage errors are. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | All test runs are in the Docker harness. |
| Constraint: backward compatibility | PASS | No Runefile changes meaning. A command line that worked before works the same. Command lines that were usage errors and now work are `rune mcp -f PATH` and its three variants. |
| Constraint: surface changes carry their docs | PASS | The CLI surface changes, and `docs/cli.md`, `docs/mcp.md`, `docs/troubleshooting.md` and the command's help change in the same pull request. There is no DSL surface change, so `docs/GRAMMAR.md` is not involved. |

No violation. Complexity Tracking is empty.

**Post-design re-check**: unchanged. The design adds one flag registration and one check.

## Project Structure

### Documentation (this feature)

```text
specs/048-mcp-file-flag/
├── plan.md              # This file
├── research.md          # Decisions and the alternatives
├── data-model.md        # The three values the flag can have, and what follows
├── quickstart.md        # How to check the result by hand
├── contracts/
│   └── serve-file-flag.md   # The command-line contract of the flag
├── checklists/
│   └── requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/rune/
├── main.go              # the built-in commands are registered by addCommands, which the tests call too
├── serve.go             # + the flag, the empty-value check, help text and an example
└── serve_test.go        # + unit tests of the flag and of what the server must not accept

test/integration/
├── reload_mcp_test.go       # startMCP gains a variant that takes the whole argument list
└── file_flag_mcp_test.go    # new: end-to-end tests over stdio

docs/
├── mcp.md               # "Starting the server": the flag and a user-level example
├── cli.md               # the `rune serve` row
└── troubleshooting.md   # "no Runefile found": the fix for a server
```

**Structure Decision**: The flag is registered where every other `serve` flag is, in
`cmd/rune/serve.go`. `internal/cli.ServeMCP` already reads `Options.File`, so it needs no
change, and the tests of the behavior are binary-level tests beside the other MCP
end-to-end tests.

## Design

Details and alternatives are in [research.md](research.md); the command-line contract is
in [contracts/serve-file-flag.md](contracts/serve-file-flag.md).

1. **A flag local to `serve`, bound to `opts.File`.** `f.StringVarP(&opts.File, "file",
   "f", "", ...)` on the `serve` command's own flag set. The root command keeps its own
   `--file` for the task path. Both write the same field; only one command runs.
2. **The flag before the command name works without more code.** Cobra uses the root's
   flags to skip `-f PATH` while it looks for the command, then gives the whole argument
   list to the command it found. With the flag on `serve`, `rune -f PATH mcp` parses. A
   root flag that `serve` does not have, such as `--yes`, is still unknown there, in any
   position.
3. **An empty value is a usage error.** `RunE` checks `cmd.Flags().Changed("file")` with
   an empty `opts.File` and returns `--file needs a path`, before `ServeMCP` is called, so
   before any Runefile is read. Without this, `config.Resolve` treats an empty value as no
   flag and looks for a Runefile from the starting directory.
4. **Nothing changes in `internal/cli`.** `ServeMCP` calls `config.Resolve(opts.File,
   opts.Cwd)`: an absolute path is used as given, a relative one is joined to the
   starting directory, a missing path and a directory are usage errors with exit code 2.
   The adapter runs tasks in the Runefile's directory, and the reloader watches the paths
   the loader recorded.
5. **A guard against inheriting root flags.** A unit test walks the root command's local
   flags and fails for any that the `serve` command has, other than `file` and the
   already shared `ignore-version`. A second test walks the other commands and fails if
   one of them has a `file` flag. Both build the command tree with `addCommands`, the
   function `main` registers the commands with, so a command added later is checked
   without touching the tests.
6. **Help.** One paragraph in the command's long text, one example line, and the flag's
   usage string.
7. **Documentation.** "Starting the server" in the MCP guide gets a part on serving a
   Runefile from another directory, with a user-level example for Claude Code and for
   Codex CLI, and the rule of one server per project. The "Configuration" bullets of
   those two clients in "Client compatibility" are corrected where they describe a
   user-level server. The command reference and the troubleshooting entry name the flag.

## Complexity Tracking

No violation to justify.
