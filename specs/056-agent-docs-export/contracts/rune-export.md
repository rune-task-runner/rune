# Contract: the `rune export` command

## Synopsis

```text
rune export <format> [--file <Runefile>] [--write] [--output <path>] [--check]
```

`<format>` is `agents-md` or `claude-skill`.

| Flag | Meaning |
|---|---|
| `-f`, `--file <path>` | Export this Runefile instead of the one found from the working directory |
| `--write` | Write the export to its documented location in the project |
| `-o`, `--output <path>` | Write the export to this path: a Markdown file for `agents-md`, a skill directory for `claude-skill` |
| `--check` | Say whether the export at the target is what Rune would write now; write nothing |

| Format | Documented location (`--write`) |
|---|---|
| `agents-md` | `AGENTS.md` in the directory of the Runefile |
| `claude-skill` | `.claude/skills/rune-tasks/` in the directory of the Runefile |

Rules for the flags:

- With none of `--write`, `--output` and `--check`, the export is printed on standard output
  and no file is created or changed.
- `--write` with `--output`: the path of `--output` is the target.
- `--check` checks the path of `--output`, else the documented location. `--check` with
  `--write` is a usage error.
- A path given with `--output` is relative to the working directory.
- `--output` for `agents-md` that names an existing directory is a usage error.
- `--output` for `claude-skill` that names an existing file that is not a directory, or
  whose last element is not a valid skill name, is a usage error. A valid skill name is 1 to
  64 characters of lowercase letters, digits and single hyphens, and does not start or end
  with a hyphen.
- `--file` or `--output` with an empty value is a usage error.

## `rune export` and a task named `export`

`rune export` with no format, or with an unknown one, is a usage error. The command line
checks the number of arguments; the format is parsed in one place, `cli.Export`.

```text
rune export needs one format: agents-md or claude-skill; to run a task named export, use: rune -- export
unknown format "deploy" for "rune export" (agents-md or claude-skill); to run a task named export, use: rune -- export
```

No argument and more than one argument give the first text. Both name `rune -- export`, so
that `rune export deploy prod`, typed to run a task, says how to run it.

When the Runefile has a task named `export`, the command prints on standard error before
anything else:

```text
note: "export" is also a task of this Runefile; run it with: rune -- export
```

## What runs

Nothing. The Runefile and the files it imports are read, the `minimum_version` of the
Runefile is checked, and the Runefile is analyzed. No task, hook, `[context]` task or body
runs. No expression is evaluated: no default, no variable, no setting. The process
environment and a dotenv file are not read for content. No program is started and no network
is used. With `--write`, `--output` or `--check` the target files are read; with `--write`
or `--output` they are written.

## Streams

- Standard output: without `--write`, `--output` and `--check`, the export: the section for
  `agents-md` (the lines from the begin line to the end line), or `SKILL.md` for
  `claude-skill`. Nothing otherwise.
- Standard error: notes, the report, diagnostics and the final error line.

## Notes on standard error

```text
note: "export" is also a task of this Runefile; run it with: rune -- export
note: tasks.md holds the entries of the <N> tasks and is part of this skill; it is not printed. Write both files with --write
```

The second is printed when `claude-skill` is printed on standard output and the skill has a
`tasks.md`.

## The report of a write

One line per file, in the order `SKILL.md`, `tasks.md` for a skill. Paths are shown relative
to the working directory when they are inside it, with forward slashes.

```text
<path>: created: <N> tasks, <B> bytes
<path>: updated: <N> tasks, <B> bytes
<path>: unchanged
<path>: removed
```

A file that is unchanged or removed has no count: nothing of it was written. `<N>` is
`1 task` for one. `<B>` is the size of what Rune owns in the file: the section from
its begin line to its end line, or the whole skill file.

## The report of a check

One line per file, then the command that regenerates when any file is not current:

```text
<path>: current
<path>: missing
<path>: stale: the Runefile changed since the export
<path>: stale: the text differs from what this rune generates (edited by hand, or written by another rune version)
<path>: stale: no longer part of the skill
regenerate with: rune export <format> [-f <Runefile>] (--write | --output <path>)
```

- `missing`: the file does not exist, or a Markdown file has no section.
- `the Runefile changed since the export`: the fingerprint in the file is not the one of the
  catalog.
- `the text differs ...`: the fingerprints are equal and the text is not.
- `no longer part of the skill`: a generated `tasks.md` where the catalog needs none.
- The last line repeats `-f` and `--output` when the run had them, each path written as one
  shell word.

A difference only between LF and CR LF line endings is not a difference.

## Refusals

A target the command will not change. Nothing is written, and the exit status is 2, in a
write and in a check alike: a check cannot compare a target it cannot read as Rune's. The
third refusal applies when the target is the documented location, in a write and in a
check; a path given with `--output` is used as given.

```text
<path>:<line>: ... (one diagnostic per marker line, with the line and a caret)
rune: export: <path> has marker lines that are not one "rune:begin tasks" line followed by one "rune:end tasks" line; fix them by hand, nothing written
rune: export: <path> was not generated by rune export, so it is not replaced; move it away or write the skill to another directory with --output, nothing written
rune: export: <path> leads outside the project, to <resolved>. rune export --write writes only inside the project; name the path with --output
```

## Exit status

| Status | When |
|---|---|
| 0 | the export was printed; every file was written or already current; a check found every file current |
| 1 | a check found a file missing or stale; a target could not be read or written |
| 2 | a usage error; no Runefile found; a refusal |
| 3 | the Runefile is not valid: the diagnostics are printed and nothing is produced |

## Help

`rune export --help` states the two formats and their locations, the three modes (print,
write, check), that only Rune's section or Rune's files are replaced, that nothing of the
Runefile runs, that the text is guidance and enforces nothing, the exit statuses, and that a
task named `export` is run with `rune -- export`. It does not use the words "restricted",
"locked down", "safe", "secure" or "sandboxed" for what the file does.
