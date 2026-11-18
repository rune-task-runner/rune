# Data Model: `rune import`

All types live in `internal/taskimport`. Nothing is stored; the model exists for one run.

## Format

`justfile`, `make`, `task`, `npm`. The value of `--from`, and the result of detection.

| Format | Runner | File names found by detection |
|---|---|---|
| `justfile` | `just` | `justfile`, `Justfile`, `.justfile` |
| `make` | `make` | `GNUmakefile`, `makefile`, `Makefile` |
| `task` | `task` | `Taskfile.yml`, `Taskfile.yaml`, `taskfile.yml`, `taskfile.yaml` |
| `npm` | `npm` | `package.json` |

## Status

`converted`, `review`, `unsupported`, `wrapper`.

Transitions, applied by the shared step in this order:

1. A reader sets `converted`, `review` or `unsupported` on each task.
2. Review notes of the shared step: a file-wide unsupported construct (`see <file>:<line>`),
   a Rune name that is a rune command, and a confirmation with dependencies. Each makes a
   `converted` task `review`.
3. With `--wrap`: `unsupported` becomes `wrapper` when the runner can run the task by name.

A status never moves toward `converted`.

## Task

One recipe, target, task or script of the source.

| Field | Meaning |
|---|---|
| `Source` | the name in the source file |
| `Name` | the Rune name, set by the name mapping |
| `Line` | 1-based line of the task in the source |
| `Doc` | the description, one line |
| `Attrs` | Runefile attributes as text without brackets, in order: `private`, `confirm("Sure?")` |
| `Params` | Runefile parameters as text: `env`, `target="debug"`, `*args` |
| `Deps`, `Post` | calls that run before the body and after it succeeds |
| `Body` | Runefile body lines as text, prefixes included |
| `Status` | see above |
| `Notes` | reasons, in the order found; required for every status but `converted` |
| `NoWrap` | the runner cannot run this task by name |

A **call** is the source name of the called task and its arguments as Runefile text. The
shared step replaces the source name with the mapped name.

Rules:

- `Source` is unique in a source. A name defined more than once is one unsupported task for
  just and make, and a parse error for a Taskfile.
- The calls of the tasks that convert form no cycle; a cycle is a parse error.
- An `unsupported` task has no `Params`, `Deps`, `Post` or `Body` from the reader; the shared
  step gives it the stub or wrapper form.
- A task has at most one variadic parameter, and it is last.
- The stub for a task that the source refers to and does not define has the line of the first
  task that refers to it, and the note `not defined in <file>`.

## Variable

A top-level assignment that converts: a name and a Runefile expression as text. A variable
converts when its name is a valid Rune name, its value converts and every variable it uses
converts. One that does not is a file-level item, and every task that refers to it is
unsupported.

## Item

A file-level construct that was not converted: a line, a short name (`set shell`,
`include config.mk`, `includes`) and a reason. Its status is always `unsupported`. An item can
be marked as affecting every task, which triggers the downgrade.

## Result

What `Convert` returns:

| Field | Meaning |
|---|---|
| `Tasks` | every source task and every stub for an undefined task, in source order |
| `Items` | file-level items, in source order |
| `Runefile` | the emitted text |
| `Report` | the report text |
| `Partial` | true when any task or item is `unsupported` |

## Name mapping

Input: the source names in source order. Output: one distinct Rune name per source name.

1. A name that matches `[A-Za-z_][A-Za-z0-9_-]*` and is not `import`, `set`, `mod`, `if` or
   `else` is kept.
2. Otherwise each run of other characters becomes one `-`, and leading and trailing `-` are
   removed. An empty result becomes `task`. A result that does not start with a letter or
   `_`, or is reserved, gets the prefix `task-`.
3. Names already taken by an earlier task: append `-2`, `-3`, and so on. Kept names (rule 1)
   are assigned first, so a mapped name never takes the name of a task that kept its own.

## Options

| Field | Meaning |
|---|---|
| `Format` | the source format |
| `SourceName` | the path printed in the report and in stubs, as the person gave it, with forward slashes |
| `RunnerPath` | the base name of the source, which a wrapper passes to the runner |
| `Wrap` | `--wrap` |
