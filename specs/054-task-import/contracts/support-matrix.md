# Contract: what `rune import` converts

For each format: what converts, what converts with status review, and what is unsupported.
Anything not listed as converted or review is unsupported and reported with its line. A task
that uses an unsupported construct is unsupported as a whole. An "item" is a file-level
construct with a report line of its own.

## Rules for all formats

- Tasks keep the order of the source. Names follow the name mapping
  ([data-model.md](../data-model.md#name-mapping)).
- A dependency on a task the source does not define becomes a failing task.
- A dependency cycle is an error of the source (exit 3).
- A task whose Rune name is also a rune command (`help`, `version`, `serve`, `mcp`, `log`,
  `analyze`, `lsp`, `completion`) keeps its name and is review: "NAME is also a rune command:
  run the task with rune -- NAME".
- A task that asks for confirmation and has dependencies is review: "the source runner asks
  before the dependencies run; Rune asks after them".
- A command line that Rune's shell cannot parse as one complete command makes the task
  unsupported.
- For just, make and Task, which start a shell for every command, a line that can change
  shell state is wrapped in `( ... )` unless it is the last line (research R4).
- A command that starts with `@` or `-` after the source's own prefixes are removed is
  unsupported, because Rune reads those characters as prefixes.
- A task whose name has a control character cannot be a wrapper: it stays a failing task.
- Four closing braces in a command are written as eight, as four opening ones are, so Rune
  prints them unchanged.
- A string with a control character other than newline, tab and carriage return, or one that
  is not UTF-8, is unsupported. A variable or parameter whose name is not a valid Rune name is unsupported.

Differences that hold for a whole format are documented once and are not report lines:

| Format | Difference |
|---|---|
| all | A bare `rune` lists the tasks; it does not run a default task (just's first recipe, make's default goal, Task's `default`) |
| all | Rune prints each command to standard error before running it; make prints to standard output, Task adds a `task: [name]` prefix, npm prints a banner |
| all | Tasks run in Rune's built-in shell, not the system shell |
| justfile | just runs each line with `sh -u`: an unset shell variable is an error under just and is empty under Rune |
| make | make exits 2 when a recipe fails; Rune exits with the failing command's status |
| make | `make NAME=value` becomes `rune NAME=value <task>` |
| task | Task exits 201 when a command fails; Rune exits with the failing command's status |
| task | `task build -- args` becomes `rune build args` |
| task | Task runs a task every time another task calls it; Rune runs each task at most once per invocation |
| npm | `npm run build -- args` becomes `rune build args` |

## justfile

| Construct | Result |
|---|---|
| Recipe `name:` with indented lines | converted |
| Comment line directly above a recipe (or above its attributes) | description |
| Parameters `p`, `p="text"`, `p='text'`, `+p`, `*p` | converted |
| Dependencies `a b`, `(a "x")`, `(a name)`, and the same after `&&` | converted |
| Variable `name := expr`, where `expr` is string literals, names, `+` and `/` | converted; `a / b` is written `a + "/" + b`, which is what just computes |
| `{{name}}` in a body, for a parameter or a converted variable; `{{{{` | converted, unchanged |
| Line prefixes `@`, `-`, `@-`, `-@` | converted |
| Quiet recipe `@name:` | converted: each line's echo is inverted |
| Line ending in `\` | joined with the next line, whose indentation is dropped |
| `[private]`, `[confirm]`, `[confirm("text")]`, `[group("g")]`, `[doc("d")]`, `[no-cd]`, `[linux]`, `[macos]`, `[windows]`, `[unix]`, `[no-exit-message]`, `[working-directory("dir")]` | converted |
| A name starting with `_` | kept; Rune hides such a task as just does |
| Parameter with another default, `$p`, variadic with a default; a parameter declared twice, after a variadic one, or required after a defaulted one | unsupported recipe |
| Dependency argument that is not a literal, a parameter or a converted variable | unsupported recipe |
| Any other `{{ ... }}` in a body, including a name that is not a parameter or a converted variable | unsupported recipe |
| Shebang recipe (first line `#!`) | unsupported recipe |
| Any other attribute | unsupported recipe |
| The same recipe name more than once (just allows one per operating system) | one unsupported recipe |
| Variable with a backtick, a call, a conditional, a triple-quoted or prefixed string; a variable that uses one of these | item; recipes that use the variable are unsupported |
| `export name := ...` | item, affects every task: a recipe can read it as a shell variable |
| `alias`, `import`, `mod` | item |
| `set ...` (any setting, also one that runs over several lines) | item, affects every task |
| A call whose number of arguments the called recipe does not take | error of the source (exit 3) |

Strings: `'text'` is taken as written. `"text"` decodes `\n`, `\t`, `\r`, `\"` and `\\`; any
other escape makes the string unsupported.

## Makefile

| Construct | Result |
|---|---|
| Target listed in `.PHONY`, with tab-indented recipe lines; its name is mapped when it has `/` or `.` | converted |
| Target with a plain name (`[A-Za-z0-9_-]+`) not listed in `.PHONY` | review: "not declared .PHONY: make skips it when a file named NAME is up to date; Rune always runs it" |
| Prerequisite that is a target of the Makefile | dependency, in order |
| Prerequisite that is not a target (a file, or something a pattern or built-in rule makes) | a failing task, as for any undefined dependency |
| `## text` at the end of the rule line, else the comment line directly above the rule | description |
| Line prefixes `@`, `-`, `+` in any order | `@` and `-` converted; `+` dropped (it only matters to `make -n`) |
| Recipe line ending in `\` | joined with the next line without its leading tab, as the shell joins them |
| `$$` | `$` |
| `NAME = text`, `NAME := text`, `NAME ::= text`, set once, where text is literal text and `$(OTHER)` references to converted variables | converted variable |
| `NAME ?= text` | `NAME := env("NAME", "text")` |
| `$(NAME)` or `${NAME}` of a converted variable, in a recipe | `{{NAME}}` |
| `.DEFAULT_GOAL` | ignored: see the differences above |
| Target not in `.PHONY` whose name has `/` or `.` | unsupported task: a file target |
| Several targets in one rule (one task per target); `::` rule; a target with more than one rule; a recipe on the rule line after `;`; order-only prerequisites after a bar; `$` in the rule line | unsupported task |
| `$@`, `$<`, `$^`, `$?`, `$*`, `$+`, single-letter `$X`, `$(func ...)`, `$(NAME)` of a name the Makefile does not set or did not convert | unsupported task |
| A recipe line that continues with `\` inside single quotes | unsupported task |
| A recipe line that starts with a variable whose value starts with `@`, `-` or `+` (make reads the prefix after expanding) | unsupported task |
| An odd run of `{` directly before a variable reference | unsupported task: Rune would read it as part of the interpolation |
| A top-level line that is only a `$(...)` reference, such as `$(shell ...)` or `$(info ...)` | item |
| A conditional block between the recipe lines of a rule | unsupported task: part of its recipe is in the block |
| Variable set with `+=`, `!=`, or more than once; `:=` that uses a variable set later; value with a function or an automatic variable | item; tasks that use the variable are unsupported |
| Pattern rule (`%`), suffix rule, `include`, `-include`, `sinclude`, `define ... endef`, `ifeq`/`ifneq`/`ifdef`/`ifndef` blocks (one item for the block), `vpath`, `override`, `undefine`, target-specific variables, `MAKEFLAGS`, `VPATH`, other special targets | item |
| `SHELL`, `.SHELLFLAGS`, `.ONESHELL`, `.SILENT`, `.IGNORE`, `.POSIX`, `.EXPORT_ALL_VARIABLES`, `export`, `unexport`, `.RECIPEPREFIX` | item, affects every task |

## Taskfile (version 3)

Top level:

| Key | Result |
|---|---|
| `version` | must be `3` or start with `3.`; otherwise an error of the source |
| `tasks` | see below |
| `vars` with text, number or boolean values, which may use `{{.OTHER}}` | converted variables |
| `vars` entry with `sh:`, `ref:`, a map or a list | item; tasks that use it are unsupported |
| `env` with text, number or boolean values | `[env]` on every task; every task is review: "Task keeps a value the environment already has; Rune's [env] replaces it" |
| `env` entry with another value | item, affects every task |
| `silent: true` | every command gets `@` |
| `includes`, `output`, `interval`, any other key | item |
| `dotenv`, `set`, `shopt`, `method`, `run` | item, affects every task |

A task may be written as a string, a list of commands, or a mapping:

| Key | Result |
|---|---|
| `desc` | description |
| `cmds`, `cmd`: a string of one line | body line |
| command mapping `{cmd, silent, ignore_error}` | body line with `@` and `-` |
| `{task: name}` at the start of `cmds`, with no `deps` on the task | dependency, in order |
| `{task: name}` at the end of `cmds` | runs after the body succeeds (`&&`), in order |
| `deps: [a, b]` (names, or `{task: name}`) | dependencies; with more than one, `[parallel]` |
| `{{.NAME}}` of a converted variable | `{{NAME}}` |
| `{{.CLI_ARGS}}` | `{{quote_each(args)}}`, and the task gets `*args`: Task quotes each argument, so the boundaries are kept |
| a task that uses `{{.CLI_ARGS}}` and is called or depended on by another task | review: "Task gives the arguments after -- to every task; here it gets them only when run by name" |
| `internal: true` | `[private]` |
| `prompt: text` | `[confirm("text")]` |
| `silent: true`, `ignore_error: true` | every line gets `@`, `-`; a boolean is read as YAML reads it (`true`, `True`, `yes`, `on`) |
| a task that uses `{{.CLI_ARGS}}` and a variable named `args` | unsupported task: the parameter would hide the variable |
| `env` with literal values | `[env]`; review, with the note above |
| `dir: path` (literal) | `[working-directory("path")]`; review: "Task creates a missing directory; Rune does not" |
| `sources` with optional `generates` (lists of text) | `[cache(inputs = [...], outputs = [...])]`; review: "Rune caches by the content of the inputs, the task body and the variables, and logs a hit" |
| `summary`, `aliases`, `label` | not carried over; review, with a note that names the key |
| multi-line command; `{{ ... }}` other than the two forms above; a reference to a variable the Taskfile does not set | unsupported task |
| `{task: ...}` between shell commands, with `vars`, or at the start of `cmds` together with `deps`; the same task called or depended on twice by one task | unsupported task |
| `vars`, `prompt` as a list, `for`, `defer`, `platforms`, `preconditions`, `requires`, `status`, `method`, `generates` without `sources`, `sources` with an `exclude` entry, `run`, `watch`, `interactive`, `set`, `shopt`, `dotenv`, `prefix`, any other key | unsupported task |
| task name with `*` | unsupported task |

## npm (`package.json`)

Every entry of `scripts` becomes a task, in file order:

```rune
[env("npm_lifecycle_event", "build")]
[env("npm_package_name", "demo")]
[env("npm_package_version", "1.0.0")]
build *args: prebuild && postbuild
    @export PATH="$PWD/node_modules/.bin{{path_sep}}$PATH"
    tsc -p . {{quote_each(args)}}
```

with `path_sep := if os() == "windows" { ";" } else { ":" }` once at the top.

| Construct | Result |
|---|---|
| Script text of one line | body line, with `{{` escaped |
| `pre<name>` and `post<name>` when `<name>` is a script | dependency and `&&` hook of `<name>` |
| A script that is itself such a hook and has a hook of its own (`preprebuild`) | no hook is attached to it; review: "npm run NAME also runs HOOK; the imported task does not" |
| Extra arguments | appended to the script, each quoted as one argument |
| `node_modules/.bin` on the path | first body line |
| `npm_lifecycle_event`; `npm_package_name` and `npm_package_version` when the file has them | `[env]` |
| Script that mentions `npm`, `npx`, `yarn` or `pnpm` as a word | review: "calls NAME, which must still be installed" |
| Script named `preinstall`, `install`, `postinstall`, `prepublish`, `preprepare`, `prepare`, `postprepare`, `prepublishOnly`, `prepack`, `postpack`, `publish`, `postpublish`, `preversion`, `version`, `postversion` or `dependencies` | review: "npm also runs this script by itself; Rune runs it only by name" |
| Script that mentions another `npm_` variable, or `INIT_CWD` | unsupported task |
| Script with a line break; a script value that is not text | unsupported task |
| `scripts` given twice | the last one counts, as for npm |
| No `scripts`, or an empty one | usage error: nothing to import |
