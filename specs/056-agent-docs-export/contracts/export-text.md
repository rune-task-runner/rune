# Contract: the exported text

The exact text `rune export` produces. The golden fixtures of `internal/cli` hold this
text for whole Runefiles; this file is the rule they follow.

Notation: `NAME` stands for a value described below it. A line in parentheses is a
condition, not output. Every output line ends with LF, except in an existing file that uses
CR LF (see [rune-export.md](rune-export.md)).

## Rendering text from the Runefile

There are three renderers, and no text from the Runefile reaches the output another way.

**prose(text)**: for descriptions, returns texts and parameter texts.

1. Every run of white space and of characters that are not printable becomes one space, and
   the ends are trimmed. Not printable means that Go's `unicode.IsPrint` is false: control
   characters, line and paragraph separators, and format characters such as the ones that
   change the direction of text. The zero-width joiner and non-joiner are kept: some
   scripts need them inside a word, and joined emoji do too.
2. A text longer than 8192 bytes is cut at the last whole character at or before that, and
   ` [truncated]` is added after the escaping below.
3. In one pass over the characters of that text: `&` `<` `>` `` ` `` `$` `@` are written
   `&amp;` `&lt;` `&gt;` `&#96;` `&#36;` `&#64;`, and `\` `*` `_` `[` `]` `#` `~` `|` are
   written with a backslash before them. The output of the pass is not escaped again.
4. When the text starts with `-`, `+` or `=`, or with digits followed by `.` or `)`, a
   backslash is written before that `-`, `+`, `=`, `.` or `)`.

**value(text)**: for enum values, literal defaults, values allowed for agents, group names
and confirm questions. The text as a double-quoted string with the escapes of Go's
`strconv.Quote`, then every backtick as `\u0060`, every `$` as `\u0024`, every `<` as
`\u003c` and every `>` as `\u003e`, inside one pair of backticks. Example: the value `us east` is written `` `"us east"` ``.

**code(text)**: for a default that is an expression. The expression in the Runefile's
canonical form, with the same four replacements, inside one pair of backticks.

Task names, parameter names, usages and examples are written inside one pair of backticks as
they are. Names are identifiers of ASCII letters, digits, `_` and `-`, with `::` after a
module name, and an example holds only values that pass the filter of the Example rule.

## The body

Both formats hold this body.

```markdown
## Rune tasks

This project's commands are tasks of its Runefile. Run a task with `rune <task>` from any directory of the project. When a task below does what you need, run the task and not a command of your own.

- Arguments are values in the order the usage shows: `<name>` is required, `[name]` is optional, and `...` after either takes several values. `name=value` does not pass a parameter.
- Quote a value that has a space or a shell character. The examples are written for a POSIX shell.
- A task that asks a person to confirm, or that applies a plan a person approved, waits for that person at a terminal. Ask the person to run it. Do not answer in their place, and do not look for another way to do its work.
- Where an entry says which value the author allows agents, use that value and no other.
- This text is guidance. It does not limit what a shell can run. Rune enforces confirmations and the values allowed for agents only for calls through its MCP server, `rune mcp`.
- The entries are generated from the Runefile and list every public task, also those for another operating system. `rune --list` shows the tasks this machine can run.

ENTRIES
```

`ENTRIES` is the entries, each followed by one empty line except the last. With no public
task it is the one line:

```markdown
This Runefile has no public tasks.
```

## An entry

```markdown
### `NAME`

DESCRIPTION

- Usage: `USAGE`
- Example: `EXAMPLE`
- PARAMETER
- Group: GROUP
- Runs only on: SYSTEMS.
- Returns: RETURNS
- Asks a person to confirm: QUESTION. Ask the person to run it.
- A run can reach `TASK`, which asks a person to confirm.
- The author allows agents only ALLOWED.
- A run can reach `TASK`, where the author allows agents only ALLOWED.
- Makes a plan that Rune stores.
- Applies the plan of `PLANTASK` after a person approved it. Ask the person to run it.
- Declared read-only by its author.
- Declared to use the network by its author.
```

The lines appear in this order, each only when it applies:

| Line | When | Content |
|---|---|---|
| `DESCRIPTION` and the empty line after it | the task has a description | `prose(description)` |
| Usage | always | see Usage |
| Example | the entry has an example | see Example |
| `PARAMETER` | once per parameter, in declaration order | see Parameter |
| Group | the task has `[group]` | `value(group)` |
| Runs only on | the task has an operating system attribute | the systems in source order, each once, joined by `, `: `Linux`, `macOS`, `Windows`, and `Unix (every system but Windows)` for `[unix]` |
| Returns | the task has `[returns]` | `prose(returns)` |
| Asks a person to confirm | the task has `[confirm]` | with a question: `: value(question)` before the period; without one the line is `- Asks a person to confirm. Ask the person to run it.` |
| A run can reach ..., which asks | once per reached task with `[confirm]`, in reach order | the reached task's name |
| The author allows agents only | the task has `[agent-allow]` | `ALLOWED` for its predicates |
| A run can reach ..., where | once per reached task with `[agent-allow]`, in reach order | the reached task's name and `ALLOWED` for its predicates |
| Makes a plan | the task has `[plan]` | |
| Applies the plan | the task has `[applies]` | the plan task's name |
| Declared read-only | the task has `[readonly]` | |
| Declared to use the network | the task has `[network]` | |

`ALLOWED` is each predicate as `` `PARAM` = value(VALUE) ``, for the task's own policy in
the order of its parameters and for a reached task in the attribute's order, joined
by ` and `.

A reached task that has both a confirmation and a policy gets both lines.

### Usage

`rune NAME` and one token per parameter, separated by spaces. For a task whose name is a
command of Rune (`help`, `version`, `serve`, `mcp`, `log`, `agent`, `export`, `analyze`,
`lsp`, `completion`) it starts with `rune -- NAME`.

| Parameter | Token |
|---|---|
| required | `<name>` |
| optional | `[name]` |
| one or more | `<name>...` |
| zero or more | `[name]...` |

### Example

A parameter **needs a value** when it is required or one-or-more, or when the author allows
agents a value for it and its default is not the literal equal to that value.

There is no example when no parameter needs a value. Otherwise the example is `rune NAME`
(or `rune -- NAME`) and one value for each parameter from the first up to the last one that
needs a value. The value of a parameter is the first of:

1. the value the author allows agents;
2. its literal default, when it is optional, so that an example never changes an option the
   caller did not have to set;
3. the first value of its enum;
4. `true`, when its type is `boolean`.

There is no example when a parameter in that range has none of these, or when a value is
not safe to show as a command: it holds a backtick, a `$`, `<`, `>` or a character that is
not printable (`unicode.IsPrint` is false, which includes line breaks and tabs), or it
starts with `-`, which Rune's own command line or a reader could take for a flag.

Each value is written for a POSIX shell: as it is when it is not empty, holds only ASCII
letters, digits and `_ - . / : = , + @ %`, and does not start with `=`, which zsh reads as
the path of a command; otherwise between single quotes, with each single quote inside
written `'\''`. The empty value is `''`.

### Parameter

```markdown
- `NAME`: KIND, TYPE, DEFAULT. DOC
```

- `KIND`: `required`, `optional`, `one or more values` or `zero or more values`.
- `TYPE`, with its comma, only for a declared type other than `string`:

  | Type | Text |
  |---|---|
  | `number` | `a number` |
  | `boolean` | `` `true` or `false` `` |
  | `path` | `a path` |
  | `raw` | `split into shell words without quoting` |
  | `enum` | `one of` and each `value(v)`, joined by `, ` |

- `DEFAULT`, with its comma, only for an optional parameter: `default value(v)` for a
  literal, `default code(expr), worked out when the task runs` for any other expression.
- The value the author allows agents is not on this line. It is a limit of the task and
  has its own line, "The author allows agents only".
- `DOC` only when the parameter has a `[param-doc]`: `prose(doc)`.

Examples of whole lines:

```markdown
- `env`: required, one of `"staging"`, `"prod"`. The environment to deploy to.
- `region`: optional, default `"eu-west-1"`.
- `tag`: optional, default `env("TAG", "latest")`, worked out when the task runs.
- `files`: one or more values, a path.
```

## `agents-md`: the section

```markdown
<!-- rune:begin tasks fingerprint=sha256:HEX -->
<!-- Generated by `rune export agents-md` from the Runefile. Do not edit between the rune:begin and rune:end lines: change the Runefile and run the command again. -->

BODY

<!-- rune:end tasks -->
```

`HEX` is 64 lowercase hexadecimal digits. This is what standard output holds, what a new
file holds, and what stands in an existing file from the begin line to the end line.

Marker lines in an existing file:

- a **begin line** is a line that, without trailing white space, starts with
  `<!-- rune:begin tasks` and ends with `-->`;
- an **end line** is a line that, without trailing white space, is `<!-- rune:end tasks -->`.

Lines inside code fences count. A byte order mark at the start of the file is not part of
its first line. The fingerprint of an existing section is read from its begin line; a begin
line without one has the empty fingerprint.

| The file has | A write |
|---|---|
| no file, or no bytes, or only a byte order mark | creates the file with the section (after the mark) |
| text and no marker line | keeps the text, adds a line break when it does not end with one, adds one empty line, adds the section |
| one begin line and, after it, one end line | replaces the lines from the begin line to the end line |
| any other count or order of marker lines | is refused |

The section's line ending is the one of the file's first line break, LF when the file has
none.

## `claude-skill`: the files

`NAME` is the last element of the skill directory: `rune-tasks` on standard output and with
`--write`.

### `SKILL.md`, at most 30 tasks

```markdown
---
name: "NAME"
description: "DESCRIPTION"
---

<!-- rune:generated claude-skill fingerprint=sha256:HEX -->
<!-- Generated by `rune export claude-skill` from the Runefile. Do not edit: change the Runefile and run the command again. -->

BODY
```

Both values are double-quoted YAML strings, so that a directory named `true` or `123` is
still read as a string. No other file belongs to the skill.

### `SKILL.md`, more than 30 tasks

The same frontmatter and comment lines, then the body with `ENTRIES` replaced by:

```markdown
### Index

The Runefile has N public tasks. [tasks.md](tasks.md) has an entry for each, with its parameters and limits. Read a task's entry before you run it.

- `USAGE`: SUMMARY
- M more tasks are in [tasks.md](tasks.md).
```

- One `- USAGE: SUMMARY` line per task for the first 100 tasks, in catalog order. `SUMMARY`
  is the description after step 1 of `prose`, cut to its first 100 characters with `...`
  added when it was longer, and then escaped by steps 3 and 4, so that a cut never splits an
  escape. A task with no description has the line `` - `USAGE` ``.
- The last line only when the catalog has more than 100 tasks; `M` is the number not listed.
  For one task it reads `- 1 more task is in [tasks.md](tasks.md).`

### `tasks.md`, more than 30 tasks

```markdown
<!-- rune:generated claude-skill fingerprint=sha256:HEX -->
<!-- Generated by `rune export claude-skill` from the Runefile. Do not edit: change the Runefile and run the command again. -->

## Rune tasks: entries

How to call a task and what its limits mean is in [SKILL.md](SKILL.md).

ENTRIES
```

### The description

One line, written as a double-quoted YAML string with `\` and `"` escaped:

```text
Tasks of this project's Runefile and the rune command line for each: NAMES. Read it before you run a build, a test, a deploy or another project command.
```

`NAMES` is the task names in catalog order, joined by `, `, as many as keep the whole
description, with the ` and K more` that follows them, within 900 characters. When names are
left out it ends with ` and K more`, where `K` is their number. When not even the first name
fits, `NAMES` is `N tasks`, where `N` is the size of the catalog. With no public task
`NAMES` is `none yet`.

### Whose file it is

A file in the skill directory is Rune's when one of its lines, without trailing white space,
starts with `<!-- rune:generated claude-skill` and ends with `-->`. Its fingerprint is read
from that line.

| The directory has | A write |
|---|---|
| no `SKILL.md` | creates it |
| a `SKILL.md` that is Rune's | replaces it, or leaves it when it is equal |
| a `SKILL.md` that is not Rune's | is refused |
| a `tasks.md` that is Rune's, and the catalog needs one | replaces it, or leaves it when it is equal |
| a `tasks.md` that is Rune's, and the catalog needs none | removes it |
| a `tasks.md` that is not Rune's, and the catalog needs one | is refused |
| a `tasks.md` that is not Rune's, and the catalog needs none | leaves it |

Skill files are written with LF. A file whose only difference is CR LF is equal.

## The fingerprint

`sha256:` and the SHA-256, in hexadecimal, of the JSON encoding of the catalog: a version
number, 1, and the entries as [data-model.md](../data-model.md) lists their fields. It is
the same in the section, in `SKILL.md` and in `tasks.md` of one Runefile.

| A change to | Changes the fingerprint |
|---|---|
| a public task's name, description, parameters, group, operating systems, returns text or limits | yes |
| only the wrapping or the spacing of a description, a returns text or a parameter text | no |
| the limits of a private task a public task reaches | yes |
| a task body, a dependency that reaches no limit, a variable, a setting, a comment that is no description | no |
| a private task no public task reaches, the `[context]` task | no |
| the Rune version, the host, the directory, the environment | no |

## What never appears

- In Rune's own text and in every example: `--yes`, a pipe or a redirection into `rune`, or
  a flag or setting that approves or skips a question. A description that the author wrote
  is shown as the author wrote it, as text.
- A command that does a task's work without Rune.
- A backtick or a `$` that comes from the Runefile. The output has no `$` at all, and no `!`
  stands before a backtick. Outside the usage lines and the general text, no `<` and no `>`
  comes from the Runefile either.
- A value of the environment or of a dotenv file.
- The words "restricted", "locked down", "safe", "secure" or "sandboxed" for what the file
  does.
