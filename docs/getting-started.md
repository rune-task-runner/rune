# Getting Started

> Zero → your first Rune task in a few minutes. New to the idea? Read
> **[What is Rune?](overview.md)** first. This guide is one linear path: install → write → run.

## 1. Install

Pick any method from the [installation guide](installation.md). The quickest, if you have Go:

```sh
go install github.com/rune-task-runner/rune/cmd/rune@latest
```

Confirm it works:

```sh
rune --version
```

> No Go toolchain? Grab a prebuilt binary or use the container image — see
> [Installation](installation.md). The steps below are identical afterward.

## 2. Write a Runefile

Create a file named `Runefile` in your project root:

```rune
# Greet someone by name (defaults to "world").
greet name="world":
    @echo "Hello, "{{quote(name)}}"!"
```

A few things to notice:

- The comment directly above a task becomes its documentation (shown by `rune --list`).
- `name="world"` is a parameter with a default value; `{{quote(name)}}` passes it to the
  command as one quoted argument.
- The leading `@` tells Rune not to echo the command line before running it.

Bodies use **significant indentation** (like Python): indent the command lines under the task.

> **Cross-platform:** the default shell executor is a pure-Go shell (`mvdan.cc/sh`), so this
> task behaves the same on Linux, macOS, and Windows — no WSL or Git-Bash needed.

## 3. Run it

```sh
rune              # show the version + available tasks (runs nothing)

rune greet        # run a task by name
# → Hello, world!

rune greet Ada    # pass an argument
# → Hello, Ada!
```

That's it — you've installed Rune and run a task.

## 4. Add a dependency

Tasks can depend on other tasks, which run first (each at most once per invocation):

```rune
greet name="world":
    @echo "Hello, "{{quote(name)}}"!"

# `check` depends on `greet`, so `greet` runs first.
check: greet
    @echo "All good ✓"
```

```sh
rune check
# → Hello, world!
# → All good ✓
```

## 5. See what's available

```sh
rune --list            # list documented tasks
rune --dry-run check   # show the execution plan without running anything
```

## A runnable, CI-verified copy

A runnable version of this example lives at
[`docs/examples/getting-started/`](examples/getting-started/) and is verified on every CI run,
so it always reflects working syntax. Run its `check` task directly:

```sh
rune --file docs/examples/getting-started/Runefile check
```

## Next steps

- **[Examples](examples/README.md)** — runnable starting points for Go, Node, Python, monorepos,
  CI, Docker, and more.
- **[Runefile language guide](runefile.md)** — dependencies, parameters, caching, executors, dotenv.
- **[CLI reference](cli.md)** — every command, flag, and exit code.
- **[Using Rune with AI agents (MCP)](mcp.md)** — expose these same tasks to agents and IDEs.
