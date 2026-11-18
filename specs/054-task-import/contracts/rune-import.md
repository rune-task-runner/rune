# Contract: the `rune import` command

```text
rune import [--from <format>] [-o <path>] [--dry-run] [--wrap] [--force] [<source>]
```

Reads one task file of another runner and writes a Runefile. It runs nothing: the source is
only read. It is not the `import` statement of a Runefile, which splices another Runefile.

## Arguments and flags

| Name | Meaning |
|---|---|
| `<source>` | The file to import, or a directory to look in. Default: the working directory |
| `--from <format>` | `justfile`, `make`, `task` or `npm`. Default: taken from the file name |
| `-o`, `--output <path>` | Where to write the Runefile. Default: `Runefile` next to the source |
| `--dry-run` | Print the Runefile to standard output and write no file |
| `--wrap` | Turn each unsupported task into a wrapper that runs the original runner |
| `--force` | Replace the file at the output path |

`--color` is inherited. More than one positional argument is a usage error.

## Finding the source

1. `<source>` names a file: that file. Its format is `--from`, else the format whose file
   names (see [data-model.md](../data-model.md#format)) include the base name. Otherwise
   exit 2: `cannot tell the format of <path>; pass --from justfile, make, task or npm`.
2. `<source>` is absent or a directory: the entries of that directory whose names are known
   file names are the candidates. The directory listing is read, so a file counts once
   whatever the case rules of the file system. With `--from`, only that format's names count.
   - none: exit 2, `no task file found in <dir> (looked for <names>); name one: rune import <path>`
   - one: that file
   - several: exit 2, `more than one task file in <dir>: <names>; name one: rune import <path>`
3. `--from` with a value outside the four: exit 2,
   `unknown format "<value>" (want justfile, make, task or npm)`.
4. A source that cannot be read: exit 2 with the system's message.

## Output

The output path is `--output`, else the Runefile the source's directory already has, else
`Runefile` in the source's directory. "A Runefile" is any name Rune's own discovery accepts
(`Runefile`, `.runefile`, in any case).

- The path exists and `--force` is absent: exit 2, nothing written,
  `<path> already exists; write to another file with --output <path>, or replace it with --force`.
- The output is the source file itself: exit 2 even with `--force`,
  `<path> is the source, which an import never changes; write to another file with --output <path>`.
- The output has a Runefile name and its directory holds another Runefile: exit 2 even with
  `--force`, `<dir> already has <name>; a directory holds one Runefile`.
- `--wrap` with an output outside the source's directory: exit 2,
  `--wrap needs the Runefile next to <source>, because a wrapper runs the original runner there`.
- `--dry-run`: the Runefile text goes to standard output; no file is created or changed,
  whatever exists.
- Otherwise the file is written once, after every check passed, with mode `0644`. A write
  that fails is exit 1 with the system's message.

Standard output carries only Runefile text, and only with `--dry-run`. The report and every
message go to standard error.

## The report

One line per task and per file-level item, ordered by source line, then the summary and the
destination:

```text
package.json:5: converted: build
package.json:6: converted: test:unit -> test-unit
package.json:7: review: release: calls npm, which must still be installed
package.json:8: unsupported: registry: it uses $npm_config_registry, which only npm sets
summary: 2 converted, 1 review, 1 unsupported, 0 wrappers
wrote Runefile
```

- Line form: `<file>:<line>: <status>: <name>[ -> <rune name>][: <reason>[; <reason>...]]`.
- `<file>` is the source path as the person gave it, or the found file's name, written with
  forward slashes on every operating system.
- Lines are sorted by line number; a task comes before an item on the same line.
- A task that the source refers to and does not define has a line of its own, `unsupported`,
  with the line of the first task that refers to it, right after that task.
- A name with a line break or another control character is printed quoted, so every line of
  the report stays one line. A reason is cut at its first line break.
- The summary counts every line.
- When the output directory differs from the source's directory, one line follows the
  summary: `note: tasks run from <output dir>, the directory of the Runefile`.
- The last line is `wrote <path>`, or `dry run: nothing written`.

The report has no color and is the same on every operating system.

## Exit status

| Status | Meaning |
|---|---|
| `0` | Nothing is unsupported |
| `1` | At least one line of the report is unsupported; the Runefile was still produced. Message: `import: <n> of <m> items were not converted`, where `<m>` is the number of report lines. Also when the Runefile could not be written |
| `2` | Usage error (see above), or the source has no tasks: `<file> has no tasks to import` |
| `3` | The source cannot be parsed. Nothing is produced |

A source that cannot be parsed is reported as Rune reports a static error, with the line, the
column and a caret under the offending line:

```text
justfile:4:1: error: this line is not a recipe, a variable or a setting: ???
4 | ???
  | ^^^
```

A Runefile that fails Rune's own analysis is never written: the diagnostics are printed with
`import: the generated Runefile is not valid; this is a bug in rune import`, exit 1.

## Help

`rune import --help` has the description above, the usage line, examples and the flags.
Examples:

```text
rune import                               # the one task file in this directory
rune import --from make ./Makefile        # name the format and the file
rune import --dry-run                     # print the Runefile and the report, write nothing
rune import -o imported.rune              # keep the existing Runefile; add: import "imported.rune"
rune import --wrap                        # unsupported tasks call the original runner
```

## Completion

`import` is offered with the other commands. `--from` completes to `justfile`, `make`, `task`
and `npm`. The positional argument completes to files.
