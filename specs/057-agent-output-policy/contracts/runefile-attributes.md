# Contract: `[agent-output]` and `[agent-summary]`

## Grammar

Added to `Attribute` in `docs/GRAMMAR.md`:

```text
| "agent-output" "(" OutputArg { "," OutputArg } ")"  (* lines an agent's result keeps *)
| "agent-summary"                                     (* a failure summary for agents *)

OutputArg = ( "head" | "tail" ) "=" STRING
```

## Examples

```rune
[agent-output(tail="50")]
test:
    go test ./...

[agent-output(head="10", tail="40")]
[agent-summary]
build:
    go build ./...

[agent-summary]
deploy env:
    ./deploy.sh {{env}}
```

`[agent-output(tail="40", head="10")]` is the same as `head="10", tail="40"`. `rune fmt`
keeps the order as written, like `[agent-allow]`.

## Values

A value is 1 to 5 decimal digits whose number is from 1 to 10,000. `"050"` is 50.

Rejected: `""`, `"0"`, `"-1"`, `"+5"`, `"5k"`, `" 5"`, `"1.5"`, `"10001"`, `"999999"`.

## Diagnostics (RUNE2021, error)

| Case | Span | Message |
|---|---|---|
| unknown argument | the name | `unknown agent-output argument "lines" (expected head, tail)` |
| repeated argument | the second name | `duplicate agent-output argument "tail"` |
| no `tail` | the attribute | `agent-output(...) requires tail="<lines>"` |
| bad value | the value | `agent-output tail "5k" must be a whole number of lines from 1 to 10000` |
| second `[agent-output]` | the second attribute | `duplicate [agent-output] on task "test"`, related: `first [agent-output] declared here` |
| second `[agent-summary]` | the second attribute | `duplicate [agent-summary] on task "test"`, related: `first [agent-summary] declared here` |

Arguments to `[agent-summary]` are a parse error, as for every attribute that takes none.

## Scope

- Only results an agent receives over MCP change: `rune mcp`, `rune serve`, and the endpoint
  an `agent` task opens. The command line ignores both attributes.
- Only the attributes of the task an agent calls apply to its result.
- Allowed on any task. On a private task, or a task never called as a tool, they have no
  effect.

## Language server

Completion and hover for both, from `internal/language/builtin.go`:

| Name | Signature | Documentation |
|---|---|---|
| `agent-output` | `[agent-output(head="10", tail="50")]` | An agent's result keeps the first head lines (optional) and the last tail lines of each of stdout and stderr, with one line that says what was left out. The command line prints everything. |
| `agent-summary` | `[agent-summary]` | A failed call from an agent gets a short summary after the exit line: the task and step that failed, the reason, and the last lines of output. The command line is unchanged. |
