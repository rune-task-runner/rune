# Research: A File Flag for the MCP Server

## R1. What happens today

Checked on a build of `main` at `7beab48`, in a directory with no Runefile, with a
Runefile at `PROJ/Runefile`:

| Command | Result |
|---|---|
| `rune mcp -f PROJ/Runefile` | `unknown shorthand flag: 'f' in -f`, exit 2 |
| `rune -f PROJ/Runefile mcp` | the same |
| `rune serve --file PROJ/Runefile` | `unknown flag: --file`, exit 2 |
| `rune mcp` | `no Runefile found in this directory or any parent`, exit 2 |
| `rune mcp --yes` | `unknown flag: --yes`, exit 2 |
| `rune -f PROJ/Runefile where` | runs the task in `PROJ` |

The root command registers `--file` with `root.Flags()`, which makes it local to the root.
Cobra finds the `mcp` command, then parses the whole argument list with that command's
flags, where `-f` does not exist. `internal/cli.ServeMCP` already calls
`config.Resolve(opts.File, opts.Cwd)`: the server can use a named Runefile, it just cannot
be given one.

## R2. A flag local to `serve`

**Decision**: Register `-f`/`--file` on the `serve` command's own flag set, bound to
`opts.File`.

**Rationale**: It adds exactly one flag to the server. A build with this one line was
tried in every form:

| Command | Result |
|---|---|
| `rune mcp -f PROJ/Runefile` | serves `PROJ`, a task runs in `PROJ` |
| `rune -f PROJ/Runefile mcp` | the same |
| `rune serve --file=PROJ/Runefile` | the same |
| `rune --file PROJ/Runefile serve` | the same |
| `rune mcp -f ../proj/Runefile` | the same, from a sibling directory |
| `rune -f A mcp -f B` | serves `B` |
| `rune --yes mcp`, `rune mcp -f PROJ/Runefile --yes`, `rune -f PROJ/Runefile --yes mcp` | `unknown flag: --yes`, exit 2 |
| `rune --quiet mcp`, `rune --list mcp` | unknown flag, exit 2 |
| `rune mcp -f` | `flag needs an argument`, exit 2 |
| `rune mcp -f ''` | serves the Runefile of the starting directory |

The flag before the command name works because Cobra reads the root's flags to know that
`-f` takes a value while it looks for the command name, and then parses with the command
it found. The last row is the one thing the line alone gets wrong; see R3.

**Alternatives considered**:

- *Make the root's `--file` persistent.* Every command would inherit it: `analyze`, `lsp`,
  `version` and `completion` would accept a flag they do not read. `analyze` takes its
  file as an argument, so `rune analyze -f X` would be accepted and ignored.
- *Make all root flags persistent, or parse root flags before the command
  (`TraverseChildren`).* The server would accept `--yes`, `--quiet`, `--set`, `--watch`
  and the rest. `--yes` on the server is the blanket approval that spec 031 removed.
- *An environment variable or a Runefile setting.* A setting cannot name the file it is
  in. An environment variable is one more thing a client configuration has to carry, and
  `rune` has no other variable that changes which Runefile is read.
- *A separate flag name for the server.* A second name for what `rune -f` already means.

## R3. An empty value is a usage error

**Decision**: When the flag was given and its value is empty, the command returns the
usage error `--file needs a path` with exit code 2, before `ServeMCP` runs.

**Rationale**: `config.Resolve` reads an empty value as "no flag" and looks for a Runefile
upward from the starting directory. On the command line that is harmless: the user sees
which Runefile ran. A server is started by a client, often from a configuration that
builds the path from a variable. If the variable is empty, the server would serve whatever
Runefile is above the client's starting directory, and nothing would say so. The command
already has `givenFlag`, written for `--output-limit` for the same reason: an empty value
is a mistake to report, not an absent flag.

**Alternatives considered**: Changing `config.Resolve` to reject an empty value: it would
also change `rune -f ""` on the command line, which is outside this feature. Leaving it:
a silent wrong project on the agent surface.

## R4. The messages for a missing file and for a directory stay

**Decision**: No change. A path that names no file gives the operating system's message
with the path in it, for example `stat /x/Runefile: no such file or directory`. A
directory gives `--file points at a directory, not a Runefile`. Both are usage errors with
exit code 2, from `config.Resolve`.

**Rationale**: They are the messages of `rune -f`, and they hold the path, which is what a
reader of a client's server log needs. Rewording them is a change to the root command.

## R5. Where tasks run, and what is watched

**Decision**: No change in `internal/cli`.

**Rationale**: The adapter takes its root from the directory of the Runefile it loaded,
and the prototype's task ran in `PROJ`, not in the starting directory. The reloader
watches the paths the loader recorded, which are those of the named Runefile and its
imports. The end-to-end tests assert both, so a later change that broke either would
fail.

## R6. Tests

**Decision**: Unit tests in `cmd/rune/serve_test.go` for the command's flag set, and
end-to-end tests in `test/integration/file_flag_mcp_test.go` that start the built binary.

- The end-to-end tests reuse `mcpProc` from `reload_mcp_test.go`. `startMCP` always puts
  `mcp` first, so a variant that takes the whole argument list is added for
  `rune -f PATH mcp` and `rune serve`.
- A task shows where it ran by writing a file, not by printing `pwd`: the test then looks
  for the file in the Runefile's directory and in the starting directory. That needs no
  path comparison and behaves the same on Windows.
- The guard tests of design item 5 are unit tests, because they are about the flag sets
  and need no process.
- The issue names `TestMCPE2E_Pending_ServeAcceptsFileFlag`. That suite is not in this
  repository; `TestFileFlagMCP_ServesNamedRunefile` is the test that takes its place, and
  its comment says so, as `TestReloadMCP_AddedTaskIsListed` does for issue #41.

**Alternatives considered**: Testing `cli.ServeMCP` directly with `Options.File` set: it
already works there, so such a test would pass before the change and prove nothing about
the flag.

## R7. Documentation

**Decision**: The flag goes in "Starting the server" of `docs/mcp.md`, with two kinds of
user-level setup told apart:

- A server that follows the project the client is in. It needs no flag, and works only in
  a client that starts its servers in the project.
- A server pinned to one project with `-f` and an absolute path. It works wherever the
  client starts it. A second project is a second server under its own name.

The example is given for Claude Code (`claude mcp add --scope user`) and Codex CLI
(`~/.codex/config.toml`), the two clients whose user-level configuration is described in
the compatibility section already.

**Rationale**: The guide today says of Claude Code that `claude mcp add --scope user rune
-- rune mcp` "adds the server for every project". That is true only while the client
starts the server in the project. The reader needs to know which of the two setups they
have.

The issue's acceptance criterion says to remove "the unsupported `-f` note". This
repository's guide has no such note; the criterion comes from the supplied patch.
