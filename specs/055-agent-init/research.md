# Research: `rune agent init`

Decisions for spec 055. What each client does is not researched again here: it is taken from
spec 047 ([design note](../../docs/design/rune-only-agent-mode.md),
[observations](../047-rune-only-agent-mode/observations/README.md)). This file decides how
Rune writes the files.

## R1. One pure package plans, `internal/cli` touches the disk

**Decision**: a new package `internal/agentinit` takes the existing bytes of each client file
and returns the new bytes, the conflicts, the per-entry states and the outcome. It reads no
file, writes no file and starts no process. `internal/cli/agentinit.go` finds the Runefile,
reads the files, asks the client for its version, prints the report and the difference, and
writes.

**Rationale**: every merge rule becomes a test of bytes in and bytes out, with no temporary
directory. Dry run, check and a real run share one plan and differ only in the last step, so
they cannot disagree.

**Alternatives considered**: adapters that write as they go. Rejected: a conflict in the
second file would leave the first one written (FR-023).

## R2. Edit the text in place; never decode and encode a whole file

**Decision**: each change is a splice of the original bytes at a position a parser gave. The
document is parsed again after every splice, which is cheap for files of this size and keeps
each step simple.

**Rationale**: FR-017 asks that every byte outside the owned entries stays. Decoding a file
and encoding it again loses comments, key order and layout in JSON, and comments and layout
in TOML.

**Alternatives considered**: decode, change, encode with an order-keeping map. Rejected: it
rewrites the whole file, loses the comments of `.vscode/mcp.json` and `.codex/config.toml`,
and turns a one-line change into a diff of the whole file.

## R3. JSON: a small hand-written reader that keeps positions

**Decision**: `jsonedit.go` holds a reader for JSON with `//` and `/* */` comments and
trailing commas. It returns a tree whose nodes carry byte offsets. Two operations are built
on it:

- `jsonSet(doc, path, value, force)`: set a key at an object path. Missing objects on the way
  are created. A present key with an equal value is left alone; with another value it is a
  conflict, or with `force` its value is replaced.
- `jsonAppend(doc, path, items)`: make sure a list at an object path holds the strings. The
  list is created when missing. Present strings are not written again. Nothing is removed.

A value of the wrong type on the way (a string where an object is needed) is a conflict that
`force` does not replace.

Layout rules for inserted text: a member added to a multi-line object goes on its own line
with the indentation of the object's first member; a member added to a one-line object stays
on that line; an empty object is opened over several lines using the file's indentation unit
(two spaces when none can be read). Inserted line breaks use the file's line ending. A new
file starts as `{}` and takes the same path, so a new file and a merged file are laid out by
one piece of code.

All three JSON files are read with the tolerant reader. Whether Claude Code accepts a comment
in its own files is not known, and a stricter reader would only add a second mode.

**Rationale**: the standard library gives no positions and rejects comments. The reader is
about 250 lines, has no dependency, and is checked two ways: a fuzz test compares its decoded
value with `encoding/json` on every input both accept, and every edit is verified (R6).

**Alternatives considered**: `tidwall/sjson` keeps formatting but does not accept comments
and gives no conflict positions. `tailscale/hujson` keeps comments and has a patch API, at
the cost of a module for one small reader, and its patch output layout is not ours to choose.

## R4. TOML: `pelletier/go-toml/v2`, its `unstable.Parser` for positions

**Decision**: add `github.com/pelletier/go-toml/v2` (v2.4.3, MIT, no dependencies of its
own). `toml.Unmarshal` reads the meaning of the file. `unstable.Parser` with `KeepComments`
walks the top-level expressions and gives the byte range of every key, value, table header
and comment. One operation is built on it:

- `tomlSet(doc, table, key, value, force)`: if the key exists as a plain `key = value` line
  under the table's header, compare and leave, conflict, or replace the value. If the table's
  header exists and the key does not, add the line after the table's last key. If the table
  does not exist, append the header and the key at the end of the file.

Values are rendered by a small function of our own for the three kinds the command writes: a
boolean, a string and a list of strings. Strings are TOML basic strings in double quotes, as
in the files the research observed, with `\\`, `\"` and control characters escaped. The
library's encoder would write literal strings in single quotes.

When the value is defined in a form the operation cannot extend, such as an inline table
`mcp_servers = { rune = { ... } }`, or dotted keys at the top of the file, the append makes
the file invalid or changes its meaning. R6 catches both, and the command then writes nothing
and prints the setting to add by hand (FR-022).

**Rationale**: checked in a throwaway module on 2026-11-18: the parser reports the offset,
line and column of each key, the range of each scalar, and the range of a whole `key = [...]`
expression across lines, with comments as their own nodes. That is everything the operation
needs. A complete TOML reader written by hand is several hundred lines of grammar with no
other use in Rune. The `unstable` package may change between releases; `go.mod` pins the
version and the tests cover every call.

**Alternatives considered**: `BurntSushi/toml` has no positions for keys. A line scanner with
regular expressions is fooled by a multi-line string that contains `[features]`. No module at
all would mean either a full parser of our own or a file we cannot validate before writing.

## R5. Conflicts are values, reported with a position, and never written past

**Decision**: an operation returns a conflict with the file, the line and column of the value
found, a message with both values, and whether `--force` would replace it. The planner
collects the conflicts of all files of the client. With one or more, nothing is written
(FR-019, FR-023) and the exit status is 2. They are printed through `internal/diag`, so each
has the line and a caret, as a Runefile error has.

`--force` replaces only the value of an owned key and the agent file (FR-020). Three
conflicts are never forced: a value of the wrong type, the deny rule `mcp__rune`, and a file
that needs lines added by hand.

**Rationale**: Principle II. A person who sees `.codex/config.toml:6:1` can decide in seconds.

## R6. Every planned file is verified before anything is written

**Decision**: for each file the planner decodes the old text and the new text to plain
values, applies the intended changes to the old value in memory, and requires the new value
to equal that. It also requires that the new text parses. A failure is reported as a file
that needs lines added by hand (TOML) or as an internal error (JSON), and nothing is written.

The same check exists for JSON and for TOML: one function per format that sets the keys and
appends the rules on the decoded old value. A failure in JSON is a defect of the command and
exits 1 with a message that says so.

**Rationale**: this is what makes FR-017 and FR-023 a property of the code and not a hope.
The splice code can have a mistake; the check does not share it, because it compares
meanings.

## R7. Writing

**Decision**: `internal/cli` writes each changed file to a temporary file in the same
directory and renames it over the target. A new file gets mode 0644 and a new directory
0755 less the umask, as any new file; an existing file keeps its mode. The writer is an unexported function field of the
flags struct, nil for the real one, so a test can make the second write fail. The files are
written in the adapter's order. When
one cannot be written, the files this run already wrote are put back (the old bytes, or the
file removed when the run created it), so a failure never leaves half a setup (FR-023).
Before any read or write, each target is checked:
the nearest existing ancestor of the target, with symbolic links resolved, must be inside the
project directory with its links resolved. Otherwise the command refuses with the path and
where it leads (FR-005).

**Rationale**: `.claude` linked to `~/.claude` is a real setup. Writing through it would
change the user's own settings, which the issue forbids.

## R8. The client's version

**Decision**: the command runs `claude --version`, `codex --version` or `code --version` with
a five second limit and takes the first `N.N.N` in the output. A program that is not found,
fails, times out or prints no version gives "no version". Each adapter has a list of checked
versions. A version is checked when it equals one of them, compared with `internal/semver`;
anything else gets "Registered only" (FR-030). There are no ranges: a version between two
checked versions was not run. The lookup is an unexported function field of the flags struct,
nil for the real one, so tests replace it and no package variable is needed. `--check` never
calls it (R11).

Lists at the start of this work, from spec 047: Claude Code 2.1.325, Codex CLI 0.167.0, VS
Code none. The installed versions are 2.1.326 and 0.169.0. The run of FR-031 is repeated for
them with the files the command writes and recorded in `observations/`; a version is added
only by such a record. For Codex the record needs two things, as in spec 047: a capture of
the request, which is the client's own tool listing, and a run with the model for the Rune
call.

**Rationale**: rule 4 of the setup outcome contract. The alternative, trusting every newer
version, is the false "Rune-only" the issue forbids. The cost is plain: after a client
updates, the command says "Registered only" until a Rune release has checked that version,
and the manual check in the report lets the person see for themselves.

**Alternatives considered**: a flag to state the version by hand. Rejected: it lets a person
claim a check nobody made. Reading the version from the client's files: undocumented.

## R9. One registration for all three clients

**Decision**: `rune mcp --file <absolute Runefile> --no-reload`, or `rune mcp --no-reload`
with `--unpinned`. The command is the name `rune`. Codex's `cwd` key is not written.

**Rationale**: `--file` is what spec 048 added for exactly this, and Rune documents that
tasks then run in the Runefile's directory, so a second mechanism per client adds nothing.
One registration means the `.mcp.json` that Claude Code and VS Code share never conflicts.
`${workspaceFolder}` in VS Code would be portable, but it was not run, and the default is
about one checkout anyway.

## R10. The report and the exit status

**Decision**: the report is lines of the form `kind: text` on standard error, in a fixed
order: file lines, `client:`, `outcome:`, then `reason:`, `remains:`, `step:`, `note:` and
`check:` lines. The exact text is in [contracts/rune-agent-init.md](contracts/rune-agent-init.md).
A dry run prints unified differences on standard output. The difference is computed by a
small line-based function in the package (longest common subsequence, three lines of
context); the files are a few dozen lines.

Exit status: 0 for "Rune-only" and "Rune-only within", 1 for "Registered only" and for a
failed check, 2 for usage and conflicts, 3 for a Runefile or client file that cannot be read
as its format. They are the existing `UsageError`, `CommandFailure` and `ValidationError`.

**Rationale**: a script must not take "Registered only" for success, which is the point of
SC-005. The scheme is the one `rune import` uses.

## R11. `--check` is the plan, compared and not written

**Decision**: `--check` builds the same plan with the same options and prints one line per
owned entry: `present`, `missing` or `differs`. It exits 0 when every entry is present and 1
otherwise. It does not start the client and names no outcome, so its answer depends on the
files alone and is the same in CI as on a developer's machine. A conflict at an owned key is
shown as `differs`. A conflict that is not about an owned entry (the deny rule `mcp__rune`, a
value of the wrong type) is printed as in a real run and makes the check fail with exit 1. A
parse failure keeps exit 3 and a file that needs lines added by hand keeps exit 2.

**Rationale**: one code path. A check that used other logic than the writer could pass a
setup the writer would change. The issue asks for a validation step that executes nothing.

## R12. The command name

**Decision**: `rune agent` is a parent command with one subcommand, `init`. A Runefile task
named `agent` is hidden on the command line, as `log` was by spec 052, and `rune -- agent`
runs it. The task stays an MCP tool. `rune agent` with no argument prints its help, and both
it and `rune agent init` print a note when the Runefile has such a task. `rune agent` with
any other argument is a usage error that names `rune -- agent`: Cobra's default for a parent
command is to print help and exit 0 whatever the arguments, which would let a script that
ran the task succeed while doing nothing. The commit is marked as breaking for that reason.
`agent` is also added to the names `rune import` treats as commands.

## R13. What is not built

- No remove command, no `--allow` or `--deny` passthrough, no reading of trust records, no
  JSON report, no Cursor or Zed adapter: see the spec's Out of scope.
- No `ask` rules for `[confirm]` tasks in Claude Code. The registration has no
  `--allow-destructive`, so Rune itself refuses such a task or asks the person.
- No generated `agent_cmd`. The documentation shows the one from run a2.
