# Contract: MCP Tools and Calls

**Feature**: 032-mcp-unknown-arguments | Covers FR-001 to FR-012

What a client sees on `tools/list` and `tools/call`. It applies the same way on stdio, on
HTTP, and on the endpoint an `agent` task opens for its own agent.

## `tools/list`

Every tool's input schema gains `"additionalProperties": false`. Nothing else in it changes.

| Task | `inputSchema` |
|---|---|
| `build target="debug"` | `{"additionalProperties":false,"properties":{"target":{"type":"string"}},"type":"object"}` |
| `clean_all` (no parameters) | `{"additionalProperties":false,"properties":{},"type":"object"}` |
| `deploy env replicas="2"` | `{"additionalProperties":false,"properties":{"env":{"type":"string"},"replicas":{"type":"string"}},"required":["env"],"type":"object"}` |

## `tools/call` with an undeclared name

The result has `isError: true`, no `[exit N]` line, and nothing runs.

| Call | Text |
|---|---|
| `build(targte="release")` | `unknown argument "targte" for task "build" (did you mean "target"?)` |
| `deploy(env="prod", replicaz="5")` | `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)` |
| `deploy(evn="prod")` | `unknown argument "evn" for task "deploy" (did you mean "env"?)` |
| `build(zzzzzz="1")` | `unknown argument "zzzzzz" for task "build"` |
| `clean_all(force=true)` | `unknown argument "force" for task "clean_all"` |
| `build(Target="release")` | `unknown argument "Target" for task "build" (did you mean "target"?)` |
| `deploy(ENV="prod")` | `unknown argument "ENV" for task "deploy" (did you mean "env"?)` |
| `tie(tag="1", tad="2")` where `tie` declares `tag`, then `tab` | `unknown argument "tad" for task "tie" (did you mean "tab"?)` |
| `tools__build(targte="release")` (tool for `tools::build`) | `unknown argument "targte" for task "tools::build" (did you mean "target"?)` |

Several names produce one line each, sorted by name, with no trailing newline:

```text
unknown argument "evn" for task "deploy" (did you mean "env"?)
unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)
```

Rules:

- A name is quoted and escaped, so `"a\nb"` stays on one line.
- A name is refused whatever its value's type is.
- No argument value ever appears in the text.
- The same call always gets the same text.
- A name over 64 characters is cut off at 64 with `...` and gets no suggestion.
- After 20 lines, a final line gives the count not shown:
  `unknown arguments not shown for task "build": 5`.

## `tools/call` with a value a task cannot receive

The result has `isError: true`, no `[exit N]` line, and nothing runs. This applies to a value
alone or as an array element.

| Value | Text |
|---|---|
| `build(target=null)` | `invalid arguments: parameter "target": null is not a valid value` |
| `build(target={"k":"v"})` | `invalid arguments: parameter "target": an object is not a valid value` |
| `each(names=[["a"]])` | `invalid arguments: parameter "names": a nested array is not a valid value` |

## What takes precedence

| Call | Result |
|---|---|
| `wipe(forcee=true)` where `wipe` is destructive and not granted | The destructive refusal only |
| `wipe(forcee=true)` where `wipe` is destructive and granted | `unknown argument "forcee" for task "wipe"`, and nothing runs |
| `deploy(env="nope", replicaz="5")` where `env` is an enum without `nope` | The unknown-argument line only |
| `deploy(evn="prod")` where `env` is required | The unknown-argument line, not the missing-argument error |
| Two invalid values | The error for the parameter declared first |

## What does not change

| Call | Result |
|---|---|
| `build(target="release")` | Runs with `release` |
| `build()`, `build({})`, `build(null)` | Runs with the default |
| `build(target=true)`, `build(target=1)` | Runs with `true` or `1` |
| `deploy()` | `task "deploy" is missing required argument "env"` |
| `build([])`, `build("x")` | `invalid arguments: json: cannot unmarshal ...` |
| `deploy(env="nope")` | `invalid arguments: parameter "env": invalid value "nope" (allowed values: ...)` |

## `mcpserver` embedders

No exported name or signature changes. The behavior changes for every engine: the parameters
a task's `TaskInfo` lists are the names its tool accepts. An embedder whose descriptors leave
out a parameter its engine reads will see those calls refused, and must add the parameter to
the descriptor.
