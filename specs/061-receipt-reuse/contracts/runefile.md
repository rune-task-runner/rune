# Contract: The `[reuse]` Attribute

## Grammar

`reuse` joins the attributes that take no argument, with `private`, `network`, `readonly` and the
others:

```ebnf
bare_attr = "private" | "parallel" | ... | "agent-summary" | "reuse" ;
```

```rune
set receipts

# Lint the code.
[reuse]
lint:
    golangci-lint run

# Run the tests.
[reuse, cache(inputs=["**/*.go", "go.mod", "go.sum"])]
test:
    go test ./...
```

## Meaning

The author states that the task's result depends only on the files git does not ignore, its
arguments, and Rune's version, OS and architecture. With `--reuse-key`, Rune may then skip it on
a qualifying receipt (contracts/cli.md). Without `--reuse-key` the attribute changes nothing.

## Diagnostics: RUNE2022 (error)

| Case | Span | Message |
|---|---|---|
| Second `[reuse]` on a task | the second attribute | `duplicate [reuse] on task "NAME"` (related: first declared here) |
| With `[confirm]` | `[reuse]` | `[reuse] cannot be combined with [confirm] on task "NAME": its run is its effect` |
| With `[plan]` | `[reuse]` | `[reuse] cannot be combined with [plan] on task "NAME": its run makes the plan` |
| With `[applies]` | `[reuse]` | `[reuse] cannot be combined with [applies] on task "NAME": its run applies a plan` |
| With `[network]` | `[reuse]` | `[reuse] cannot be combined with [network] on task "NAME": its result depends on the network` |
| Agent executor | `[reuse]` | `[reuse] cannot be used on agent task "NAME": its result is a model's answer` |

`[reuse(x)]` is a parse error: the attribute list expects `]` after `reuse`.

## Editor

Completion and hover list `reuse` with signature `[reuse]`. The VS Code grammar highlights it.
