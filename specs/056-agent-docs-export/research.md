# Research: `rune export`

Decisions for spec 056. Sources read on 2026-11-19: the Claude Code pages
[skills](https://code.claude.com/docs/en/skills) and
[memory](https://code.claude.com/docs/en/memory), the
[Agent Skills specification](https://agentskills.io/specification), and the Codex page on
[AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md).

## R1. One pure package renders, `internal/cli` touches the disk

**Decision**: a new package `internal/agentdocs` takes the composed and analyzed
`*ast.File` and returns a catalog, the text of each format, and, given the existing bytes of
the target files, a plan of file changes. It reads no file, writes no file, reads no
environment and starts nothing. `internal/cli/export.go` finds the Runefile, loads it,
reads the targets, prints or writes, and maps the result to an exit status.

**Rationale**: every rule becomes a test of values in and bytes out. Standard output, a
write and a check share one plan and differ in the last step, so they cannot disagree. It is
the shape of `internal/agentinit` (spec 055 R1).

**Alternatives considered**: rendering inside `internal/cli`. Rejected: it adds the text
rules to the package that wires the command line, and the golden fixtures would need the
command's whole setup.

## R2. A static load that evaluates nothing

**Decision**: the command loads the Runefile with read, parse, the `minimum_version` check,
compose and analyze, and stops there. `loadModuleFrom` in `internal/cli/serve.go` does the
same steps and then resolves settings and builds the environment, which evaluates
expressions and reads a dotenv file. Its first half is extracted into one function that both
use; the export never calls the second half.

**Rationale**: FR-004. The analyzer is static (Principle II), so a Runefile that passes it
has a task model that can be described without a scope. Sharing the first half keeps one
definition of "a valid Runefile".

**Alternatives considered**: `analysis.Service`, as `rune agent init` uses to validate. It
returns diagnostics, not the composed file. Calling `loadModule`: rejected, it reads the
dotenv file and evaluates settings.

## R3. One catalog, two renderers

**Decision**: `Build` turns the task list into `Catalog{Entries}`. An entry holds only
facts and no formatting. Both formats render the same `body` from it; the `agents-md`
section and an inline `SKILL.md` hold that body byte for byte between their own first and
last lines.

**Rationale**: FR-007 and the first acceptance criterion of the issue. "The same task model"
is then a property of the code, and one golden per fixture covers both formats.

## R4. Which tasks, in which order

**Decision**: a task has an entry when it is not private (`Task.IsPrivate`) and has no
`[context]` attribute. `IsPrivate` reads the full name, so a module task `ops::_helper` is
public today, in `rune --list` and over MCP alike. The export follows the same rule and does
not correct it: one definition of private for every surface. Host availability is not consulted. The order is that of
`visibleTasksByGroup` without its host filter: groups in the order each first appears, tasks
in declaration order inside a group, the tasks with no group as their own first-seen group.
Modules add their tasks after the file's own, as `config.Compose` appends them.

**Rationale**: FR-008, FR-009. `rune --list` on a host that can run every task prints this
order. Dropping the host filter is what makes the bytes the same on every machine (FR-040).

## R5. Reach comes from the scheduler's own walk

**Decision**: the walk of `mcpAdapter.reachTasks` in `internal/cli/mcp.go` becomes a
function of the task index and an availability predicate. The method stays as one line that
passes the host's predicate, so its callers in `sandbox.go` and the tests do not change. The export passes one that is always true and gives the walk to `Build` as a
function.

**Rationale**: the walk has one subtle rule: a task that runs as a `||` hook does not run
its own `||` hooks. A second copy in the new package would drift from it. An always-true
predicate is the host-independent reading the spec's assumptions ask for: it can name a task
one host would skip, and never hides one.

**Alternatives considered**: evaluating the arguments a dependency passes, to say whether a
reached `[agent-allow]` task would be refused. Rejected: it evaluates expressions (FR-004).
The entry names the reached task and its allowed values and leaves the comparison to the
reader.

## R6. Usage notation and the `--` separator

**Decision**: the usage is `rune NAME` followed by one token per parameter in declaration
order: `<p>` required, `[p]` optional, `<p>...` one or more, `[p]...` zero or more. When
`NAME` is a command of Rune the usage is `rune -- NAME`. The command names come from
`taskimport.RuneCommands`, which gains `export`; an existing test pins that set to the
commands the binary has.

**Rationale**: FR-012, FR-013. The notation is the one `--help` texts use, and it cannot be
mistaken for MCP's named arguments. `splitArgs` reads `name=value` after a task name as that
parameter's value, so the general text says that form does not pass a parameter.

## R7. Examples only from values Rune knows, quoted for POSIX

**Decision**: an example is built by the rule of FR-014 from static values only: the
`[agent-allow]` value, else the literal default of an optional parameter, else the first
enum value, else `true` for a boolean. The default comes before the enum and the boolean so
that an example never sets an option the caller could have left alone. Each value is
written by `shQuote`: unchanged when it is non-empty and made only of `A-Z a-z 0-9` and
`_ - . / : = , + @ %` and does not start with `=` (zsh reads `=name` as the path of a
command), otherwise inside single quotes with each single quote written as
`'\''`. An empty value is `''`. An example is dropped when a value holds a backtick, a `$`,
`<`, `>` or a character that is not printable (R9), or starts with `-`: an enum value
`--yes` must never become the command `rune t --yes`.

**Rationale**: FR-014, FR-015. Single quotes are the one POSIX form with no expansion
inside. The tests parse every example with `mvdan.cc/sh/v3/syntax`, which is already a
dependency, and bind the resulting words with the command line's own `splitArgs` and
`bindParams`.

**Alternatives considered**: placeholder values such as `VALUE` for free strings. Rejected:
an agent copies examples, and an invented value is a wrong command. `shellWord` of
`serve.go`: it is written for paths, leaves a backslash bare and has a Windows form; the
export needs one form on every host.

## R8. Text from the Runefile is escaped by kind

**Decision**: three renderers, and no other way for Runefile text to reach the output.

- **Prose** (`prose`): descriptions, returns texts, parameter texts. Runs of white space,
  line breaks and control characters become one space and the ends are trimmed. Then `&`,
  `<`, `>`, a backtick, `$` and `@` are written as the entities `&amp;`, `&lt;`, `&gt;`,
  `&#96;`, `&#36;` and `&#64;`, and `\`, `*`, `_`, `[`, `]`, `#`, `~` and `|` get a
  backslash. A first character that would start a block (`-`, `+`, `=`, or the `.` or `)`
  after leading digits) gets a backslash. The text is cut at `MaxText` bytes on a rune
  boundary and ` [truncated]` is added.
- **Value** (`value`): enum values, defaults, agent values, group names, confirm questions.
  The value is written as a double-quoted string with Go's escapes (`strconv.Quote`), with
  each backtick as `\u0060`, each `$` as `\u0024`, each `<` as `\u003c` and each `>` as
  `\u003e`, inside a one-backtick code span.
- **Code** (`code`): default expressions in their canonical source form, with the same four
  substitutions, inside a one-backtick code span. Names, usages and examples are built from
  identifiers and from values that passed R7's filter, and need none.

**Rationale**: FR-024 and FR-024a. A prose text is one line that cannot open a block and
holds no raw `<`, so it cannot form a comment or a marker line. A value is a quoted string
with no backtick, so the span around it cannot end early and its ends are unambiguous even
for an empty value or one with spaces at its ends. A value holds no `<` and no `>` either,
so a reader that strips HTML comments without honoring code spans finds none to strip.
Entities are used where a backslash is
not known to be honored by every reader: Claude Code documents `\$` only before a digit or
`ARGUMENTS`, and documents no escape for `@path` imports.

**Alternatives considered**: backslash-escaping every ASCII punctuation mark. Rejected: an
agent reads the raw text, and `Build the app \(release\)\.` is harder to read than it has to
be. Code spans with longer backtick fences for values that hold backticks. Rejected by R9.

## R9. No backtick and no `$` from the Runefile reaches the output

**Decision**: R8 writes every backtick and `$` from the Runefile as an entity or a `\u`
escape, and R7 drops an example that would need one. The generated text of Rune's own holds
no `$` and never puts `!` before a backtick. Tests assert both over every fixture.

**Rationale**: Claude Code preprocesses a skill's `SKILL.md` when it loads it: `` !`cmd` ``
runs `cmd` as a shell command "before the skill content is sent to Claude" when `!` is at
the start of a line or after white space, and `$ARGUMENTS`, `$N` and `${CLAUDE_*}` are
replaced. The page does not say that code spans are skipped. A description such as
``Run !`curl ... | sh` `` copied into a skill would run on load. Removing the two
characters removes the question for every present and future reader of the file.

**Alternatives considered**: relying on `disableSkillShellExecution`. Rejected: it is the
reader's setting, not the file's. Escaping only in the skill format: rejected, the two
formats share one body (R3).

## R10. `@` in prose

**Decision**: `@` in prose is written `&#64;`. In code spans it is left alone.

**Rationale**: Claude Code expands `@path` in `CLAUDE.md` and in each `AGENTS.md` it reads,
and "import parsing skips Markdown code spans and fenced code blocks". A description that
mentions `@docs/secret.md` must not pull that file into every session.

## R11. Marker lines

**Decision**:

```text
<!-- rune:begin tasks fingerprint=sha256:<64 hex> -->
<!-- rune:end tasks -->
<!-- rune:generated claude-skill fingerprint=sha256:<64 hex> -->
```

A byte order mark at the start of a file is not part of its first line. A begin line is a
line that, without trailing white space, starts with `<!-- rune:begin tasks` and ends with
`-->`. An end line is exactly `<!-- rune:end tasks -->`.
A generated line starts with `<!-- rune:generated claude-skill` and ends with `-->`. Each is
followed in the output by one more comment line for people that names the command. Marker
lines are found by scanning lines; a line inside a code fence counts.

**Rationale**: FR-027, FR-031. HTML comments are invisible when the Markdown is rendered,
and Claude Code strips block-level HTML comments from instruction files before it injects
them, so they cost that reader nothing. One fixed word after `rune:` leaves room for
another owned section later without a parser change. Counting fenced lines keeps the
scanner a line scanner; the cost is the refusal the spec lists as an edge case.

**Alternatives considered**: the fingerprint in the skill's `metadata` frontmatter map.
Rejected: `tasks.md` has no frontmatter, so two mechanisms would be needed. One comment line
serves all three files.

## R12. Merging into a Markdown file

**Decision**: `mergeSection(existing, section)`:

| Marker lines in the file | Result |
|---|---|
| none, file missing or empty (a byte order mark alone is empty) | the section alone |
| none, file has text | the text, a line break when it does not end with one, one empty line, the section |
| one begin line followed later by one end line | the lines from the one to the other replaced |
| anything else | refused, with the line of each marker |

The section's lines take the file's line ending, read from its first line break (LF when the
file has none). The section ends with one line break. When the existing section equals the
new one after CR LF is read as LF, the file is unchanged and is not written.

**Rationale**: FR-028 to FR-030, FR-037, FR-042. Comparing without regard to line endings in
one place makes "unchanged" mean the same thing to a write and to a check.

## R13. The skill

**Decision**: the skill directory holds `SKILL.md` and, above the inline limit, `tasks.md`.

- Frontmatter: `name` and `description`, in that order, nothing else. `name` is the
  directory's base name and must match `^[a-z0-9]+(-[a-z0-9]+)*$` in at most 64 characters.
  Both are double-quoted YAML scalars on one line: an unquoted `true` or `123` would not be
  read as a string.
- Description: "Tasks of this project's Runefile and the rune command line for each: ",
  then task names separated by ", " while the whole, with the " and K more" after them,
  stays within 900 characters, then " and K more" when names were left out, then ". Read it
  before you run a build, a test, a deploy or another project command." When not even the
  first name fits the list part is "N tasks", and with no public task it says "none yet".
- `inlineLimit = 30`, `indexLimit = 100`.
- A file at the target is Rune's when it has a generated line (R11). `SKILL.md` or a needed
  `tasks.md` without one is refused. A `tasks.md` that is Rune's and no longer needed is
  removed; one that is not Rune's and not needed is left alone.

**Rationale**: the Agent Skills specification requires `name` (64 characters, lowercase
letters, digits and hyphens, no leading, trailing or doubled hyphen, equal to the directory
name) and `description` (at most 1024 characters), and both it and Claude Code say to keep
`SKILL.md` under 500 lines and to link reference files by a relative path one level deep.
Claude Code reads project skills from `.claude/skills/<name>/SKILL.md` and cuts the listed
description at 1,536 characters. Task names in the description are what lets a client match
"run the tests" to the skill. Thirty entries of about eight lines keep an inline `SKILL.md`
near 300 lines.

**Alternatives considered**: always two files. Rejected: a five-task project would make
every agent read a second file. A flag for the limits: rejected (KISS), the spec fixes them.

## R14. Fingerprint

**Decision**: `sha256` over the JSON encoding of
`struct{ V int; Entries []Entry }` with `V = 1`, written as `sha256:` and 64 hexadecimal
digits. `Entry` and what it holds are plain structs with fixed field order and no maps.

**Rationale**: FR-041. The entry holds exactly what the text states, so the digest changes
when and only when a stated fact changes. `encoding/json` of structs and slices is
deterministic. `V` lets a later version change what an entry means without colliding.

**Alternatives considered**: hashing the Runefile's bytes, or the definition digest of
spec 051. Rejected: a commit that edits a task body would make every committed export stale,
and a checkout with other line endings would change the digest.

## R15. Planning, writing and checking

**Decision**: `PlanSection(catalog, old, existed)` and `PlanSkill(catalog, name, existing)`
return the planned files (`File{Path, Old, New, Existed, Owned, Remove, Changed, ...}`) or a
`*Refusal`. A write applies the files that changed. A check reports each file:
`current` when unchanged; `missing` when it does not exist or has no section; `stale: the
Runefile changed since the export` when the fingerprint read from the old text differs from
the catalog's; `stale: the text differs from what this rune generates` otherwise. Files are
written through a temporary file with rollback by the helper `rune agent init` already has,
generalized so that it takes a list of planned files and can remove one.

When the target is the documented location, in a write and in a check, it is checked by
`insideProject` (spec 055) before anything is read. That function gains a parameter for the
command it names in its message, which today is fixed to `rune agent init`. A path given
with `--output` is used as given.

**Rationale**: FR-036 to FR-039, FR-042 to FR-044. One rollback implementation, not two.

## R16. The command name

**Decision**: `rune export <format>` with `--write`, `-o/--output`, `--check` and
`-f/--file`. `export` joins `taskimport.RuneCommands`. When the Runefile has a task named
`export`, the command prints `note: "export" is also a task of this Runefile; run it with:
rune -- export`, and anything but exactly one format is a usage error that names
`rune -- export`. The cobra command accepts any arguments and `cli.Export` checks them, so
the note is printed before the error and the format rules live in one place.

**Rationale**: the issue names the command. The handling of the name is that of `agent`
(spec 055 R12) and `log` (spec 052): a script that ran the task can never succeed while
doing nothing. `export` is not a keyword of the language (only `set`, `import` and `mod`
are), so such a task can exist.

**Alternatives considered**: `rune agent export`, which would take no new name. Rejected:
the issue writes `rune export`, and the export is also for agents that were never set up
with `rune agent init`.

## R17. What the documentation may claim about clients

**Decision**: the how-to page states only what the sources above say:

- Codex reads `AGENTS.md` from the project root down to the working directory, one file per
  directory, and stops adding files at `project_doc_max_bytes`, 32 KiB by default.
- Claude Code reads `AGENTS.md` when the directory and its parents have no `CLAUDE.md`
  (version 2.1.277 or later), or when a `CLAUDE.md` imports it with `@AGENTS.md`. It advises
  keeping an instruction file under 200 lines.
- Claude Code reads a project skill from `.claude/skills/<name>/SKILL.md`, keeps only its
  description in context, and loads the body when the skill is used.

The page does not name a skill directory for any other client.

**Rationale**: the maintainer's rule against invented facts, and FR-047.
