# Getting started

> **Use case:** your very first Runefile — the smallest example that shows a task, a
> parameter, and a dependency.

**Demonstrates:** tasks, parameters, dependencies  ·  **Guide:** [Runefile language](../../runefile.md)

**Prerequisites:** none (uses the built-in cross-platform shell)

## Run it

```sh
rune --file Runefile check
```

(or, from this directory, just `rune check`.)

## Expected output

```text
Hello, world! This is your first Rune task.
All good ✓
```

`check` depends on `greet`, so `greet` runs first. Run `rune greet Ada` to pass an argument,
or `rune --list` to see both tasks with their descriptions.

## How it works

```rune
# Greet someone by name (defaults to "world").
greet name="world":
    @echo "Hello, "{{quote(name)}}"! This is your first Rune task."

# Run the project's checks; `greet` runs first as a dependency.
check: greet
    @echo "All good ✓"
```

- `name="world"` is a parameter with a default; `{{quote(name)}}` passes it to the command
  as one quoted argument.
- `check: greet` declares a dependency — `greet` runs first, at most once per invocation.
- The leading `@` suppresses echoing the command line before it runs.
