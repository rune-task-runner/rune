# Contract: `[agent-allow]` and RUNE2019

**Feature**: 050-mcp-agent-value-policy | Covers FR-001 to FR-008, FR-032

## Grammar

One new alternative in `AttrItem`, and one new rule, in `docs/GRAMMAR.md`:

```text
            | "agent-allow" "(" AllowPred { "," AllowPred } ")"  (* values an agent may pass *)

AllowPred   = Name "=" StringLit ;
```

- At least one predicate. `[agent-allow]`, `[agent-allow()]`, a predicate without `=` or without a
  string literal, and a trailing comma are syntax errors (`RUNE1001`) at the token found.
- It may share a bracket list: `[confirm, agent-allow(env="staging")]`.
- Spaces around `=` are accepted. The formatter writes `[agent-allow(env="staging", region="eu-west-1")]`
  on its own line: predicates in source order, `, ` between them, no spaces around `=`.
- `--dump` shows `Attr agent-allow(env="staging", region="eu-west-1")`.

On the command line the attribute changes nothing: not how the task runs, and not `--list`. The
one exception is a `rune` started with `RUNE_AGENT_RUN=1` in its environment, which Rune sets for
the bodies of an agent run: see [mcp-tools.md](mcp-tools.md).

## Meaning

Each predicate names a parameter of the task and the one value an agent run of the task may have
for it. All predicates must hold. A parameter the attribute does not name is not limited by it.
Comparison is on the whole string, case-sensitive.

## RUNE2019

Severity: error. It stops the commands other analyzer errors stop, with exit status 3 and nothing
run: running a task, `--list`, `--dump`, `--summary`, `--dry-run`, `rune mcp` and `rune serve`.
`rune analyze` prints it as `error[RUNE2019]:`. The language server reports it.

| Fault | Reported at | Text |
|---|---|---|
| A second `[agent-allow]` on one task | The second attribute, with the first as a related location | `duplicate [agent-allow] on task "<task>": put every predicate in one attribute` |
| The name is not a parameter of the task | The name | `agent-allow names unknown parameter "<name>" of task "<task>"`, followed by ` (did you mean "<param>"?)` when a parameter is close |
| The name is used twice in the attribute | The second name | `duplicate agent-allow predicate for parameter "<name>"` |
| The parameter is variadic | The name | `agent-allow cannot restrict variadic parameter "<name>": only string and enum parameters are supported` |
| The parameter is `number`, `boolean`, `path` or `raw` | The name | `agent-allow cannot restrict parameter "<name>" of type <kind>: only string and enum parameters are supported` |
| The parameter is an `enum` without the value | The value | `agent-allow value "<value>" is not an allowed value of parameter "<name>" (allowed values: "a", "b")` |
| The value holds a character that is not printable, or is not valid UTF-8 | The value | `agent-allow value for parameter "<name>" holds a character that is not printable` |

Rules:

- An unannotated parameter counts as a string parameter.
- Each predicate is checked on its own, so one attribute can give several errors.
- The predicates of a second `[agent-allow]` are not checked: the duplicate is the error.
- A parameter with an unknown type name gets `RUNE2012` only.
- A variadic parameter is reported as variadic, whatever its type.
- A default the policy forbids, and a dependency argument the policy forbids, are not errors.
- The attribute is allowed with every other attribute, `[private]`, `[confirm]` and `[context]`
  included.

## `docs/diagnostics.md`

A row in the Semantic table:

```text
| `RUNE2019` | Invalid `[agent-allow]`: an unknown, repeated, variadic or non-string parameter, a value its enum does not have or that is not printable, or a second `[agent-allow]` on the task |
```

## Language registry

```text
agent-allow   [agent-allow(param="value")]
Limit the values an agent may run the task with: each named string or enum parameter must have exactly this value. People on the command line are not limited.
```
