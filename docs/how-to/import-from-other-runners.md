# Import tasks from another runner

> Move a project that uses just, make, Task or npm scripts to Rune with one command, and see
> exactly what converted and what did not.

`rune import` reads the task file of another runner and writes a Runefile with the same tasks.
It only reads the file. It runs no task, no variable command and no script, fetches no include,
and never changes the source.

```sh
rune import                         # the one task file in this directory
rune import --from make ./Makefile  # name the format and the file
rune import --dry-run               # print the Runefile and the report, write nothing
```

This command is not the [`import` statement](imports-and-modules.md) of a Runefile, which
splices another Runefile into the current one.

## What it reads

| Format | `--from` | File names it finds |
|--------|----------|---------------------|
| just | `justfile` | `justfile`, `Justfile`, `.justfile` |
| make | `make` | `GNUmakefile`, `makefile`, `Makefile` |
| Task (version 3) | `task` | `Taskfile.yml`, `Taskfile.yaml`, `taskfile.yml`, `taskfile.yaml` |
| npm scripts | `npm` | `package.json` |

With no argument Rune looks in the current directory. When it finds more than one of these
files it stops and lists them: name the one you mean. A file with another name needs `--from`.

## The report

Every task of the source gets one line on standard error, with the file and the line it comes
from:

```text
justfile:1: unsupported: variable version: its value runs a command (backtick), which import never runs
justfile:4: converted: build
justfile:9: review: serve: serve is also a rune command: run the task with rune -- serve
justfile:14: unsupported: tag: it uses the variable "version" of line 1, which was not converted
summary: 1 converted, 1 review, 2 unsupported, 0 wrappers
wrote Runefile
```

| Status | Meaning |
|--------|---------|
| `converted` | The task is in the supported subset and does what the source task did. |
| `review` | The task was converted, and it differs from the source in the way the line says. You decide whether that matters. |
| `unsupported` | The task was not converted. It is still in the Runefile, as a task that fails with the same reason, so nothing that depends on it can pass without it. |
| `wrapper` | Only with `--wrap`: the task runs the original runner. See [Wrappers](#wrappers). |

A construct outside a task that was not converted, such as a setting or an include, has a line
of its own. A name that is not a valid Rune task name is mapped, and the line shows both:
`test:unit -> test-unit`.

| Exit status | Meaning |
|-------------|---------|
| `0` | Nothing is unsupported. |
| `1` | At least one line is unsupported. The Runefile was still written. |
| `2` | Usage error: no source, several sources, an existing output, no tasks. |
| `3` | The source cannot be parsed, or its tasks depend on each other in a cycle. Nothing was written. |

The same source always gives the same Runefile and the same report.

## From a justfile to a Runefile

Take this justfile:

```text
out := "dist"

# Build the app.
build:
    go build -o {{out}}/app ./...

# Run the tests.
test: build
    cd internal
    go test ./...
```

Run `rune import` next to it. Both tasks convert, and the Runefile is:

```rune
# Imported from justfile by rune import.

out := "dist"

# Build the app.
build:
    go build -o {{out}}/app ./...

# Run the tests.
test: build
    (cd internal)
    go test ./...
```

Two things to know:

- `cd internal` is in parentheses. just starts a new shell for every line, so the `cd` never
  reached `go test`. Rune keeps one shell for the whole task, so the import wraps a line that
  changes the shell in a subshell, and the task behaves as it did.
- A parameter is pasted into the command as just pastes it: `{{name}}` stays `{{name}}`.
  `rune analyze` then suggests `{{quote(name)}}` (`RUNE2017`), which is the safer form when
  you are ready to change it. See [Parameters](parameters.md).

Now `rune build` and `rune test` do what the just commands did. Check the `review` lines of
the report, fix the `unsupported` ones by hand or keep them as [wrappers](#wrappers), and
delete the justfile when nothing uses it any more.

## Keeping an existing Runefile

`rune import` never replaces a file, and it never writes to the source, even with `--force`.
When the directory already has a Runefile, it stops and names two choices:

```sh
rune import -o imported.rune   # write somewhere else
rune import --force            # replace the Runefile
```

To merge, write the import to another file and name it in your Runefile:

```rune
import? "imported.rune"

# A task of your own.
hello:
    @echo "hello"
```

## Wrappers

With `--wrap`, a task that cannot be converted becomes a task that runs the original runner:

```rune
# Runs "make gen" (wrapper, needs make). Generate the code.
gen *args:
    make -f Makefile gen {{quote_each(args)}}
```

Every task is then callable from Rune while you convert the difficult ones. A wrapper is
reported as `wrapper`, never as converted, and it needs the original runner installed.
`--wrap` writes the Runefile next to the source, because the runner must run where it always
ran. Without `--wrap`, no imported task calls the source's runner.

## Differences that hold for every task

These are not repeated in the report:

| Format | Difference |
|--------|------------|
| all | A bare `rune` lists the tasks. It does not run a default task. |
| all | Rune prints each command to standard error before running it. make prints to standard output, Task adds a `task: [name]` prefix, npm prints a banner. |
| all | Tasks run in Rune's built-in shell on every operating system, not in the system shell. |
| just | just runs each line with `sh -u`: an unset shell variable is an error under just and is empty under Rune. |
| make | make exits `2` when a command fails. Rune exits with the failing command's status. |
| make | `make NAME=value target` becomes `rune NAME=value target`. |
| Task | Task exits `201` when a command fails. Rune exits with the failing command's status. |
| Task | `task build -- args` becomes `rune build args`. |
| Task | Task runs a task every time another task calls it. Rune runs each task at most once per invocation. |
| npm | `npm run build -- args` becomes `rune build args`. |

For all four: a task named like a rune command (`help`, `version`, `serve`, `mcp`, `log`,
`agent`, `analyze`, `lsp`, `completion`) keeps its name and is run with `rune -- NAME`; a task that
asks for confirmation and has dependencies is `review`, because Rune asks after the
dependencies ran; and a dependency on a task the source does not define becomes a task that
fails.

## What converts

Anything not listed as converted or review is unsupported and reported with its line. A task
that uses an unsupported construct is unsupported as a whole.

### justfile

| Construct | Result |
|-----------|--------|
| A task with a comment above it, parameters (`p`, `p="text"`, `+p`, `*p`), dependencies before and after (`&&`), with literal arguments | converted |
| Variables built from string literals, other variables, `+` and `/` | converted |
| `{{name}}` of a parameter or a variable, `@` and `-` line prefixes, a quiet task (`@name:`), lines joined with `\` | converted |
| `[private]`, `[confirm]`, `[group]`, `[doc]`, `[no-cd]`, `[linux]`, `[macos]`, `[windows]`, `[unix]`, `[no-exit-message]`, `[working-directory]` | converted |
| A backtick, a function call or a conditional in an expression; any other `{{...}}` | unsupported |
| A shebang task, another attribute, an exported parameter, the same name defined twice | unsupported |
| `alias`, `import`, `mod` | reported |
| `set ...` | reported; every task is `review` |

### Makefile

| Construct | Result |
|-----------|--------|
| A target listed in `.PHONY`, with its prerequisites, a `## text` or a comment as description, `@` and `-` prefixes, `$$`, lines joined with `\` | converted |
| Variables set once with `=`, `:=` or `?=` to literal text and other variables, used as `$(NAME)` | converted; `?=` reads the environment first |
| A target with a plain name that is not in `.PHONY` | review: make skips it when a file of that name is up to date, Rune always runs it |
| A prerequisite that is not a target of the Makefile | a task that fails |
| A file target, a target with several rules, `$@`, `$<` and the other automatic variables, make functions, `$(MAKE)` | unsupported |
| Pattern and suffix rules, `include`, `define`, conditional blocks, `+=` | reported |
| `SHELL`, `.ONESHELL`, `export` and the other settings that change every command | reported; every task is `review` |

### Taskfile

| Construct | Result |
|-----------|--------|
| `desc`, `cmds`, `cmd`, command entries with `silent` and `ignore_error`, top-level `vars` with literal values | converted |
| `deps` | converted: the dependencies run concurrently |
| Calls to other tasks at the start and at the end of `cmds` | converted: they run one at a time, in order |
| `{{.NAME}}` of a variable, `{{.CLI_ARGS}}`, `internal`, one `prompt`, `silent`, `ignore_error` | converted |
| `env`, `dir`, `sources` with `generates`, `summary`, `aliases`, `label` | review, each with its note |
| `sh:` variables, task-level `vars`, other template expressions, multi-line commands, `for`, `defer`, `preconditions`, `status`, `platforms`, a call between shell commands | unsupported |
| `includes`, `output` | reported |
| `dotenv`, `set`, `shopt`, `method`, `run` at the top level | reported; every task is `review` |

`sources` become Rune's [caching](caching.md) with the same files.

### npm scripts

Each script becomes a task that runs from the package directory with `node_modules/.bin`
first on the path, as under `npm run`.

| Construct | Result |
|-----------|--------|
| A script of one line | converted |
| `pre<name>` and `post<name>` scripts | converted: they run before `<name>` and after it succeeds |
| Extra arguments | converted: each reaches the script as one argument |
| `npm_lifecycle_event`, `npm_package_name`, `npm_package_version` | converted |
| A script that calls `npm`, `npx`, `yarn` or `pnpm` | review: that program must still be installed |
| A script npm also runs by itself, such as `prepare` or `postinstall` | review: Rune runs it only by name |
| Another `npm_` variable, `INIT_CWD`, a script the built-in shell cannot parse | unsupported |

## Pitfalls

- A Runefile in another directory than the source runs its tasks from that directory. The
  report says so.
- Importing is not a test. Run the imported tasks once before you delete the old file.
- A script written for `cmd.exe` does not convert: imported tasks run in Rune's built-in
  shell.

---

**Next:** [Imports & modules](imports-and-modules.md) · [Parameters](parameters.md) · [CLI reference](../cli.md) · [All how-to guides](README.md)
