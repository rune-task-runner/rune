# Settings & dotenv

> How to configure a Runefile and feed values to task bodies. Part of the [guides](README.md);
> full syntax in the [language guide](../runefile.md#variables-and-settings).

## Concept

**Variables** (`name := value`) are reusable values resolved statically and interpolated with
`{{ }}`. **Settings** (`set ...`) configure the whole file. A **`.env`** file that the
Runefile names with `set dotenv` is loaded into task environments, so configuration and
secrets come from the environment and are never hard-coded.

## Syntax

```rune
out := "dist"
# `/` joins paths (forward slash on every OS).
bin := out / "app"

# Export Runefile variables into task environments.
set export
# Load the KEY=VALUE lines of this file into task environments.
set dotenv := ".env"
# Override the shell for (sh) bodies (rarely needed).
set shell := ["bash", "-euo", "pipefail"]

build:
    @echo "building {{bin}}"
```

The file that `set dotenv` names holds `KEY=VALUE` lines:

```text
GREETING=hello
```

A relative path starts at the Runefile's directory. A file that does not exist is not an
error: the tasks run without its values.

## Runnable example

See **[examples/settings-dotenv](../examples/settings-dotenv/README.md)**: `set export` and
`set dotenv`, with a `.env` value reaching the body.

## Pitfalls

- **A `.env` file is not loaded by itself.** Without `set dotenv := ".env"` nothing reads
  it, and a body sees only the environment `rune` was started with.
- **Secrets live in the environment, not the Runefile.** Put them in `.env` (kept out of
  version control) or the real environment. They never belong in a committed Runefile, and are
  never exposed to agents (see [AI agents](../mcp.md)). As a safety net, values of
  sensitive-looking variables are masked as `***` in all output — see
  [secret masking](secret-masking.md).
- **`set export` is opt-in.** Without it, Runefile variables interpolate into commands but
  aren't necessarily present as environment variables for sub-processes.
- **Paths use `/`.** The path-join operator emits forward slashes on every OS for portability.
- **`set shell` runs the body as one script.** Include `-e` so a failing line stops the task.
  The `@` and `-` prefixes have no effect. See [shell override](../runefile.md#shell-override).

## Next steps

- [Parameters](parameters.md) — per-invocation inputs (vs. file-level settings).
- [Executors](executors.md) — how the environment reaches each body.
