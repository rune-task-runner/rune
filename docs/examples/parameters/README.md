# Parameters

> **Use case:** pass values into tasks — with defaults, required arguments, or a variadic list.

**Demonstrates:** parameters, variadics, interpolation  ·  **Guide:** [Parameters](../../runefile.md#parameters)

**Prerequisites:** none

## Run it

```sh
rune greet
```

## Expected output

```text
hello world
```

Try the variations: `rune greet Ada` → `hello Ada`; `rune deploy prod` → `deploying to prod`;
`rune test ./... ./cmd/...` → `go test ./... ./cmd/...`.

## How it works

`greet name="world"` has a default; `deploy env` is required; `test +packages` is variadic
(one or more). `{{quote(name)}}` passes a value to the command as one argument, and
`{{quote_each(packages)}}` passes each element of a variadic as its own argument.

`release channel:enum("stable","beta") builds:number="1"` adds inline **type
annotations**: `rune release beta` runs, while `rune release nightly` is rejected
before anything executes, with the allowed values in the error. The same
constraints appear in the MCP tool schema, `[param-doc]` describes the parameter
to agents, and `[returns]` states the expected output (also shown by
`rune --list`). See the [typed parameters guide](../../runefile.md#typed-parameters).
