# Runefile Language Guide

A Runefile is a small, line-oriented file describing **tasks** (named blocks of commands)
plus a few settings and variables. If you know `make` or `just`, you'll recognize it at a
glance. This guide is a practical, example-driven tour; the formal grammar lives in
[`GRAMMAR.md`](GRAMMAR.md) (the parser is the source of truth).

New to Rune? Read [What is Rune?](overview.md) and [Getting started](getting-started.md) first.
For task-oriented deep dives on a single capability, see the [guides](how-to/README.md); for
runnable starting points, the [examples](examples/README.md).

> The Runefile expression language is intentionally **total** (no loops, no recursion).
> Real logic belongs in task bodies; the configuration stays readable and statically
> analyzable.

## Tasks

The core unit. A task has a name, an optional parameter list, an optional executor, an
optional dependency list, and an indented body:

```rune
# The comment directly above a task is its documentation (shown by `rune --list`).
build:
    go build ./...
    @echo "done"
```

Bodies use **significant indentation**. Each body line is a command. Two prefixes:

- `@` — do not echo the command before running it.
- `-` — continue even if this line fails (ignore its error).

```rune
clean:
    -rm -rf dist        # ignore failure if dist/ doesn't exist
    @echo "cleaned"     # don't print this echo line itself
```

### Parameters

```rune
# Defaulted parameter:
greet name="world":
    @echo "hello {{name}}"

# Required (no default):
deploy env:
    @echo "deploying to {{env}}"

# Variadic — one-or-more (+) and zero-or-more (*):
test +packages:
    go test {{packages}}

lint *paths:
    golangci-lint run {{paths}}
```

Run them: `rune greet Ada`, `rune deploy prod`, `rune test ./... ./cmd/...`.

### Dependencies and post-hooks

Dependencies run **before** the task; post-hooks (after `&&`) run **after** it succeeds;
failure hooks (after `||`) run **after** it fails — the place to run a diagnostic and
turn a raw exit code into a post-mortem. Each task runs at most once per invocation
(failure hooks are the one exception: they run once per failing task).

```rune
deploy: build test && notify
    @echo "deploying"

build:
    go build ./...

test: || diagnose
    go test ./...

notify:
    @echo "deployed ✓"

diagnose:
    @echo "post-mortem for $RUNE_FAILED_TASK (exit $RUNE_FAILED_EXIT_CODE)"
    go vet ./...
```

Clause order is fixed: dependencies, then `&&`, then `||`. Failure hooks fire only when
the task's **own body** fails (a failing dependency fires its own hooks instead), never
change the exit code, and never chain — a hook's own `||` hooks are ignored while it
runs as a hook. The hook body sees the failed task's name and exit code in
`RUNE_FAILED_TASK` and `RUNE_FAILED_EXIT_CODE`. On the CLI the hook's output streams
under a `diagnosing:` header; over MCP it reaches the agent as a *fix suggestion*
section in the same tool response as the failure (masked and size-capped) — see
[mcp.md](mcp.md). Cancelling with Ctrl-C aborts everything, hooks included.

Pass arguments to a dependency with the parenthesized form:

```rune
release: (build "release")
    @echo "releasing"

build target="debug":
    go build -tags {{target}} ./...
```

## Interpolation

`{{ expr }}` evaluates an expression and substitutes the result. Use `{{{{` for a literal
brace.

```rune
src := "src"
build:
    @echo "compiling from {{src}}/"
```

## Variables and settings

```rune
# Variable assignment:
out := "dist"
bin := out / "app"          # `/` joins paths (forward slash on every OS)

# Settings:
set export                  # export Runefile variables into task environments
set shell := ["bash", "-cu"]  # override the shell for (sh) bodies
set secrets := ["DEPLOY_CFG"]   # mask these variables' values in all output
set unmasked := ["OAUTH_METHOD"]  # exempt from built-in secret-name patterns
```

A bare `set name` is shorthand for `set name := true`.

### Opt-in backward-compatibility pragma

The default interpretation never changes under you. Opt into versioned semantics per file:

```rune
set rune_version := "1"
```

### Minimum Rune version

Pin the minimum Rune **binary** release your project needs (distinct from
`rune_version`, which is the Runefile *language* version):

```rune
set minimum_version := "0.8.0"
```

The value must be a static semantic version and means "requires Rune ≥ this
version". Rune checks it before running anything and refuses an older binary with
a clear diagnostic (required vs installed + an upgrade link), executing nothing.
Only the root Runefile's requirement is effective — imported files cannot change
it. Pass `rune --ignore-version` to bypass the check (a warning is printed), and
`rune version --check` (add `--json` for machine-readable output) to report
whether the installed binary is compatible.

### Secret masking

Values of sensitive environment variables are masked as `***` in everything
Rune emits — task stdout/stderr, echoed command lines, Rune's own status
lines, and MCP tool results. Variables are detected by *name* (any name
containing `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `APIKEY`, `API_KEY`,
`PRIVATE_KEY`, `ACCESS_KEY`, `CREDENTIAL`, or `AUTH`, case-insensitive);
masking is always on and the task itself still receives the real value:

```rune
set secrets := ["DEPLOY_CFG"]     # also mask names the patterns miss
set unmasked := ["OAUTH_METHOD"]  # exempt a false positive
```

See the [secret masking guide](how-to/secret-masking.md) for the exact rules
and the guarantee's limits (verbatim occurrences only; values shorter than
4 bytes are not masked).

## Expressions

The expression sublanguage supports string literals, concatenation, path-join,
conditionals, comparisons, and function calls.

```rune
# Concatenation (+) and path-join (/):
greeting := "hello, " + name
path := "src" / "main.go"

# Conditional with comparisons (== != =~, where =~ is regex match):
mode := if os() == "linux" { "lin" } else if os() == "windows" { "win" } else { "other" }
```

String literals come in single (`'...'`), double (`"..."`), and triple-quoted
(`'''...'''`, `"""..."""`) forms; triple-quoted strings are de-dented.

### Built-in functions

Rune ships a set of pure, total helper functions (mirroring the `just` family):

| Category | Functions |
|----------|-----------|
| Host | `os()`, `arch()`, `os_family()`, `num_cpus()` |
| Environment | `env("NAME")`, `env("NAME", "default")` |
| Paths | `join(...)`, `clean(p)`, `extension(p)`, `file_name(p)`, `file_stem(p)`, `parent_dir(p)`, `absolute_path(p)` |
| Strings | `uppercase`, `lowercase`, `capitalize`, `trim`, `trim_start`, `trim_end`, `replace(s, from, to)`, `replace_regex(s, re, to)` |
| Filesystem | `path_exists(p)`, `read(p)` |
| Misc | `uuid()`, `datetime(fmt)`, `quote(s)`, `which(name)`, `require(name)`, `error(msg)` |

```rune
home := env("HOME", "/tmp")
name := capitalize(trim(raw_name))
build:
    @echo "on {{os()}}/{{arch()}} with {{num_cpus()}} CPUs"
```

## Executors

A task body runs under an executor named in parentheses after the signature. The default
is `sh` — a **pure-Go, cross-platform shell** (`mvdan.cc/sh`) that behaves the same on
Linux, macOS, and Windows without invoking the system shell.

```rune
# default (sh) — no parentheses needed
hello:
    echo "hi"

# Python body (shells out to the real python3 via a temp file):
analyze (python):
    print("analyzing")

# Node body:
bundle (node):
    console.log("bundling")

# Agent body — drives an installed AI-agent CLI (see docs/mcp.md):
summarize (agent):
    Summarize the latest git changes.
```

`python`/`node`/custom executors shell out to the real interpreter, so those runtimes must
be installed (see the [Docker guide](docker.md) for the container caveat).

## Attributes

Attributes sit on their own line(s) above a task in `[...]`:

```rune
[private]                       # hidden from --list and MCP; callable only as a dependency
[confirm("Really clean?")]      # prompt before running (auto-approve with --yes)
[parallel]                      # run this task's dependencies concurrently
[group("build")]                # group label in listings
[linux]  [macos]  [windows]  [unix]   # restrict to an OS (see below)
[no-cd]                         # don't change into the Runefile's directory
[network]                       # marks the task as network-using (MCP openWorldHint)
[no-exit-message]               # suppress the trailing error banner on failure
[context]                       # project-health hook: output is injected into agent context
[working-directory("./sub")]    # run the body from a specific directory
[env("KEY", "value")]           # set an environment variable for the body
[doc("Custom one-line doc")]    # override the doc string
[script("/usr/bin/env python3")]  # run the whole body as a script under this interpreter
[cache(inputs = ["go.mod", "src/**/*.go"], outputs = ["dist/app"])]  # content-hash caching
```

`[context]` marks at most one task as the project's **context hook**: Rune runs it
when an agent session starts and injects its output into the agent's context — see
[Using Rune with AI Agents](mcp.md#project-context-for-agents). The hook must run
unattended, so it cannot carry `[confirm]`, take a parameter without a default, or
use the `agent` executor — and neither can any task in its dependency chain. It is
never exposed as an MCP tool, but stays runnable by name from the CLI.

### OS availability

The OS attributes make a task exist only on the platforms it names. Several
attributes combine as OR (`[linux, windows]` means either), and `[unix]`
matches every platform except Windows. On a non-matching host the task is:

- **hidden** from `--list`, the bare `rune` overview, the interactive picker,
  shell completion, and the MCP tool list — an AI agent never sees it;
- **not runnable**: invoking it by name fails before anything executes, with
  a diagnostic naming the required OS (exit 3);
- **skipped silently as a dependency or post-hook**, which turns per-OS tasks
  into a dispatch pattern — one cross-platform task can depend on a
  `[linux]`, a `[macos]`, and a `[windows]` variant, and only the matching
  one runs (a `||` failure hook is the exception: skipping it emits a
  one-line warning, because a silently missing diagnostic would hide why no
  post-mortem appeared):

```rune
# Install the toolchain for this platform.
setup: setup-nix setup-win
    @echo "toolchain ready"

[unix]
setup-nix:
    @echo "apt-get/brew install ..."

[windows]
setup-win:
    @echo "choco install ..."
```

`rune --dump --format json` reports the computed verdict per task in an
`available` field, alongside the raw attribute names.

### Caching (opt-in)

Rune never skips work based on timestamps. Caching is an explicit, per-task opt-in via
`[cache]`. A fingerprint is computed over the declared inputs, the task body, the resolved
variables, and the executor; a cache hit is **logged**, never silent.

```rune
[cache(inputs = ["go.mod", "go.sum", "**/*.go"], outputs = ["dist/app"])]
build:
    go build -o dist/app ./...
```

Clear the cache with `rune --clear-cache`.

## Imports and modules

```rune
import "common.rune"        # inline another Runefile's tasks/vars
import? "optional.rune"     # ignore if the file is missing

mod tools "tools.rune"      # namespace: invoke as `rune tools::build`
```

## Dotenv

A project `.env` file is loaded into task environments (parsed via the same pure-Go shell),
so secrets and config come from the environment — never hard-coded in the Runefile.

## Formatting

Rune is its own formatter. Reformat a Runefile canonically in place:

```sh
rune --fmt
```

## See also

- [Guides](how-to/README.md) — task-oriented deep dives (dependencies, caching, executors, …)
- [Examples](examples/README.md) — runnable starting points by use case
- [CLI reference](cli.md) — flags, subcommands, exit codes
- [Troubleshooting](troubleshooting.md) — errors, diagnostics, and exit codes
- [GRAMMAR.md](GRAMMAR.md) — the formal grammar
- [MCP guide](mcp.md) — exposing tasks to AI agents
