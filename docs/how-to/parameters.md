# Parameters

> How tasks take input. Part of the [guides](README.md); full syntax in the
> [language guide](../runefile.md#parameters).

## Concept

A task can accept **parameters**: defaulted, required, or variadic. Parameters are
interpolated into the body with `{{ }}`, and they also define the input schema when the task
is [exposed to an agent over MCP](../mcp.md).

## Syntax

```rune
# Defaulted — rune greet  /  rune greet Ada
greet name="world":
    @echo "hello "{{quote(name)}}

# Required — rune deploy prod
deploy env:
    @echo "deploying to "{{quote(env)}}

# Variadic — one-or-more (+) and zero-or-more (*)
test +packages:
    @echo go test {{quote_each(packages)}}

# Raw: the value is split into shell words on purpose. rune check "-v -race"
check flags:raw="-v -race":
    @echo go test {{flags}} ./...
```

Run them: `rune greet Ada`, `rune deploy prod`, `rune test ./... ./cmd/...`.
`quote_each(packages)` passes each package as its own quoted argument, so a path
that contains a space stays one argument. `flags:raw` declares that the value is meant to
be split into words, so `rune check "-v -race"` passes two flags.

## Runnable example

See **[examples/parameters](../examples/parameters/README.md)**.

## Pitfalls

- **Required parameters are checked before anything runs.** Calling a task (or depending on
  one) with too few arguments fails static analysis with an arity error and exit code `3` —
  e.g. `task "greet" expects at least 1 argument(s), got 0`. See
  [Troubleshooting](../troubleshooting.md).
- **A variadic must be last.** Only the final parameter may be `+`/`*`.
- **Quote values with spaces** on the command line, as your shell requires.
- **Interpolate parameters with `quote()`.** `{{name}}` pastes the value into the command
  as shell text, so a space, quote, or `;` in it changes the command, even inside shell
  quotes. `rune analyze` warns about this (`RUNE2017`). Write `{{quote(name)}}`, or
  `{{quote_each(name)}}` for a variadic, outside any shell quotes; declare `name:raw` when
  you want the value split into words. See
  [Quoting parameters](../runefile.md#quoting-parameters).

## Next steps

- [Dependencies & post-hooks](dependencies-and-hooks.md) — pass arguments to a dependency.
- [Settings & dotenv](settings-and-dotenv.md) — values that aren't per-invocation arguments.
