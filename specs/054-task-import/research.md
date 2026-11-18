# Research: `rune import`

Decisions for spec 054. Each has the decision, the reason and what was rejected. Facts about
Rune were checked against a binary built from this branch on 2026-11-18. Facts about the
source runners come from their documentation. GNU Make 4.4.1 and npm 11.19.0 are on the
development host and were run by hand to check the make and npm rules. just and Task are not
installed anywhere this feature is built, so their rules rest on their documentation alone;
the import never needs any runner.

## R1. One pipeline, four readers

**Decision**: `reader (per format) -> finish (shared) -> emit (shared) -> self-check -> write`.
A reader returns task records and file-level items. It knows the source format and nothing
about Runefile text beyond the few fragments it fills in (a parameter, an attribute, a body
line).

**Rationale**: the rules the spec states once (names, never-dropped dependencies, the
file-wide downgrade, stubs, wrappers, the report) are written once. A reader stays a reader.

**Rejected**: a full intermediate AST with an expression tree. The supported expressions are
"literal text and references to variables", which a string holds.

## R2. Static reading

**Decision**: a reader receives the file's bytes and returns records. It opens no other file
and starts no process. `mvdan.cc/sh/v3/syntax` is used only to parse command lines; the
interpreter package is not imported by `internal/taskimport`. A unit test reads the package's
import declarations and fails on `os`, `os/exec`, `net`, `net/http` and the shell interpreter, and
the integration test asserts that no marker file appears.

**Rationale**: FR-006 and SC-004. An import statement that the compiler checks is a stronger
guarantee than a comment.

## R3. Readers

- **justfile**: hand-written line reader. Top-level lines are a comment, a `set`, an
  `alias`, an `import` or `mod`, an assignment, an attribute line, or a recipe header followed
  by indented body lines. Anything else is a parse error with its line (exit 3).
- **Makefile**: hand-written line reader. After joining `\` continuations, a line is a recipe
  line (starts with a tab, inside a rule), a comment, a directive, an assignment, or a rule.
  Conditional blocks and `define` blocks are skipped as one reported item each.
- **Taskfile**: `yaml.v3` decoded to `yaml.Node`, which keeps key order and line numbers.
  Only version 3 is read; another version is an error (exit 3).
- **package.json**: `encoding/json`. `name` and `version` are decoded normally; the keys of
  `scripts` are read with `json.Decoder.Token` so that their order and byte offsets (hence
  lines) are known.

**Rejected**: parsing just and make completely. Both languages are large; a reader that
recognizes the supported subset and reports the rest by line is enough, and smaller.

## R4. Per-line shell state and unparseable lines

**Facts** (checked): Rune's default shell runs all body lines on one interpreter, so `cd` and
`export` carry over to later lines. It parses each line by itself, so a line that is not a
complete command fails. `exit 0` on a line does not end the body. A line with `a; b` continues
after a failing `a`, as `sh -c` does.

**Decision**: for just, make and Task, which start a shell per line or per command, each
command line is parsed with `syntax.NewParser()` (the same parser Rune's shell uses):

- A line that does not parse makes the task unsupported ("not a complete shell command on one
  line"). This also covers multi-line `if` and `for` blocks that make joins with `\`: after
  joining they parse and convert.
- A line that can change shell state and is not the last line of the body is wrapped as
  `( line )`. The check fails closed: a line can change state when, outside a subshell or
  command substitution, it has an assignment without a command, a command whose name is not
  a literal word, a shell builtin other than the harmless ones (`:`, `true`, `false`, `echo`,
  `pwd`, `test`, `[`, `type`, and `printf` without `-v`), a `for`, function, `let`,
  arithmetic or `declare`-style clause, an arithmetic expansion, or an assigning parameter
  expansion (`${X:=v}`). A program that is not a builtin cannot change the shell.
- A wrapped line must still be one subshell: a line that ends in a comment would swallow the
  closing parenthesis, so it makes the task unsupported.

npm scripts are one command in one shell under npm and under Rune, so they are not wrapped,
but they are parsed: a script Rune's shell cannot parse is unsupported.

**Rationale**: a subshell gives exactly the source's isolation and keeps the exit status, so
the task stays "converted". Wrapping only where needed keeps simple recipes looking like the
source.

**Rejected**: wrapping every line (correct, but every imported file looks foreign); a
"review" note in place of wrapping (pushes a mechanical fix onto the person).

## R5. Interpolation and literal braces

**Facts** (checked): `{{name}}` interpolates in a Rune body; `{{{{` prints `{{`. just uses the
same two rules.

**Decision**:

- just: `{{name}}` with a variable or parameter name passes through. Any other interpolation
  (a call, an operator, a string) makes the recipe unsupported.
- make: `$(NAME)` and `${NAME}` of a converted variable become `{{NAME}}`; `$$` becomes `$`.
  Any other `$` form (automatic variable, function, single-letter variable, undefined name)
  makes the task unsupported.
- Task: `{{.NAME}}` of a converted variable becomes `{{NAME}}`; `{{.CLI_ARGS}}` becomes
  `{{quote_each(args)}}` with a `*args` parameter, because Task shell-quotes each argument
  after `--` before it joins them. Any other template action makes the task unsupported.
- make, Task and npm: a literal `{{` in command text is written as `{{{{`.
- Values of variables use the same substitution and become a Rune expression: literal pieces
  as string literals joined with `+` to variable names. just's `a / b` is written
  `a + "/" + b`: Rune's own `/` also trims the slashes around the joint, and just does not.
- A variable converts only when every variable it uses converts, so a cycle of variables does
  not. One helper settles this for the three formats that have variables.

**Parameters stay unquoted where the source pastes them.** just pastes `{{name}}` as text, so
the imported body does too. `rune analyze` then gives its usual RUNE2017 warning for a string
parameter outside `quote()`, which is the right hint and not an error.

## R6. Unsupported tasks never disappear

**Decision**: an unsupported task is emitted under its mapped name as

```rune
# Not imported (justfile:20): uses a backtick command, which import never runs
version *args:
    @echo 'rune import: "version" (justfile:20) was not imported: uses a backtick command, which import never runs' >&2; exit 1
```

It takes `*args` so that a dependency call with arguments still analyzes. It keeps the
`[private]` marker of the source. The message, like every name and path the importer writes
into a body, is shell-quoted, and its `{{` is escaped. A task that the source refers to and does not define gets
the same stub (FR-018).

**Rationale**: FR-016. A dependent task fails at the stub with the reason; nothing is skipped,
and the Runefile always analyzes.

**Rejected**: leaving the task out and commenting the dependents (drops a prerequisite);
making dependents unsupported too (cascades through the file).

## R7. Wrappers

**Decision**: with `--wrap`, the stub's body is the runner call instead:

| Format | Body |
|---|---|
| justfile | `just --justfile <source> <name> {{quote_each(args)}}` |
| make | `make -f <source> <name> {{quote_each(args)}}` |
| task | `task --taskfile <source> <name> -- {{quote_each(args)}}` |
| npm | `npm run <name> -- {{quote_each(args)}}` |

`<source>` is the base name of the source file: `--wrap` requires the Runefile next to the
source (and `--dry-run` prints the Runefile meant for that place), because each runner then
runs where it always ran. The description is `Runs "<runner> <name>" (wrapper, needs <runner>).` followed by the source
description. The report line is `wrapper`. A task the runner cannot run by name stays a
failing stub: a Taskfile task with `internal: true` or a wildcard name, and a task the source
does not define.

**Rationale**: FR-042. Always naming the source file needs no table of default names.

**Rejected**: `make -C <dir>` and `npm --prefix <dir>` to support a Runefile elsewhere. One
refusal is simpler than four path rules.

## R8. File-wide constructs

**Decision**: a reader returns file-level items (always status unsupported) and may mark one
as affecting every task. The shared step then gives every converted or review task the note
`see <file>:<line>` for each such item, which makes a converted task review.

The shared step adds two more review notes, for every format: a task whose Rune name is a
rune command (`rune help` runs the command; the task is run with `rune -- help`), and a task
that asks for confirmation and has dependencies (checked: Rune runs the dependencies, then
asks; just and Task ask first). It also refuses a dependency cycle, which Rune's analyzer
rejects, as an error of the source. Which constructs set the flag is listed
per format in [contracts/support-matrix.md](contracts/support-matrix.md).

**Rationale**: FR-019 with one boolean instead of per-construct handling.

## R9. Emission and report

**Decision**: the emitter writes canonical-looking text directly: a one-line header comment,
variables, then tasks in source order, each with its description comment, attributes one per
line, signature and body indented four spaces. String literals are double-quoted with `\\`,
`\"`, `\n`, `\t` and `\r` escaped, which is what Rune's lexer decodes; a value with any other
control character is unsupported.

The report has one line per task and per file-level item, ordered by source line:
`<file>:<line>: <status>: <name>[ -> <rune name>][: <reason>]`, then
`summary: N converted, N review, N unsupported, N wrappers`. The command adds the lines that
depend on the destination (`note:`, `wrote`) after the summary.

**Rejected**: passing the text through `internal/formatter`. It drops comments that are not a
task's description, including the header (checked).

## R10. Self-check before writing

**Decision**: `internal/cli` parses and analyzes the emitted text through the shared analysis
service with an overlay source, as `rune analyze` does. Errors mean a bug in the importer: the
diagnostics are printed, nothing is written, and the command exits 1. Warnings (RUNE2010 for a
task without a description, RUNE2017) are expected and not printed.

**Rationale**: FR-010. The check costs one parse and guards every reader at once.

## R11. `import` as a command name

**Fact** (checked): `import`, `set`, `mod`, `if` and `else` are keywords; `import:` as a task
header is a parse error. So no Runefile has a task named `import`, and the new command hides
nothing. This differs from `log` in spec 052, which could be a task name.

**Decision**: no note, no escape hatch. The name mapping gives a source task with one of the
five reserved names the prefix `task-`. The other command names can be task names and are
handled in R8.

## R12. Output, overwrite and merge

**Decision**: the output path is `--output`, else the Runefile the source's directory already
has, else `Runefile` there. An existing output path is refused; `--force` replaces it. An
output with a Runefile name in a directory that holds another Runefile is refused even with
`--force`, so a directory never ends up with two. "A Runefile name" is decided by the
matcher Rune's discovery uses, exported from `internal/config` for this.

`--dry-run` prints the Runefile to standard output and the report to standard error.

A source directory is searched through its listing, one candidate per entry, so a file system
that ignores case does not make one file look like two.

Merging is documented, not implemented: write to `imported.rune` and add
`import "imported.rune"` to the existing Runefile.

When the output is in another directory than the source, a `note:` line says that tasks run
from the Runefile's directory. With `--wrap` that combination is refused (R7).

**Rejected**: `--output -` as a second spelling of `--dry-run`, and detecting a format from a
file extension. `--from` covers both.

## R13. Exit status and errors

| Status | Meaning |
|---|---|
| 0 | Nothing is unsupported. Review items and wrappers do not change this |
| 1 | At least one report line is unsupported; the Runefile was still written or printed. Also a Runefile that could not be written |
| 2 | Usage: no source, several sources, unknown format, unreadable source, existing output, no tasks |
| 3 | The source cannot be parsed as its format, or its tasks form a cycle. Nothing was written |

They map to the existing `CommandFailure`, `UsageError` and `ValidationError` types.

A reader error carries a line. The command renders it through `internal/diag` as a
diagnostic that spans the whole line, so the person sees `file:line:col`, the line and a
caret, as for a Runefile error (Principle II). Report lines carry `file:line` only: they
describe a construct, not a position inside it.

## R14. Tests and parity

**Decision**:

- `internal/taskimport`: a golden test walks `testdata/<format>/<case>/`, converts `source`
  and compares `want.Runefile` and `want.report`; `-update` regenerates them. The package is
  added to the golden gate in `.github/workflows/ci.yml`. Every supported and every
  unsupported construct of the support matrix has a case.
- `test/integration`: one small project per format. The test imports it with the built
  binary, then runs the imported tasks and compares standard output, success or failure, exit
  status, working directory, argument boundaries, environment and the order of dependencies
  with the recorded behavior of the source runner. With `RUNE_TEST_SOURCE_RUNNERS=1`, a case
  whose runner is on the path also runs the runner and compares its standard output and
  success with the record. The test container has `make`, so the make records are checked
  there. The npm records were checked by hand with `npm run` on the development host. The
  just and Task records rest on documentation.
- `internal/taskimport`: a test cuts every golden source at every byte. No reader may panic,
  and whatever converts must still analyze. It found the missing arity check on the first
  run.
- Safety: adversarial fixtures whose variables, includes and bodies would create a marker
  file. The test asserts that no marker exists and that the source bytes are unchanged.

**Rationale**: SC-002 to SC-005. The recorded expectations make the suite hermetic; the
opt-in run against the real runner keeps the records honest.
