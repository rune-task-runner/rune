# Contract: the `[plan]` and `[applies]` attributes

## Grammar

```ebnf
AttrItem    = ... | "plan" "(" PlanArg { "," PlanArg } ")" | "applies" "(" STRING ")" ;
PlanArg     = IDENT "=" STRING ;
```

`plan` takes the arguments `file`, `summary`, `tool` and `expires`, each at most once, in any
order. The formatter keeps the order written.

```rune
[plan(file="tfplan", summary="tfplan.txt", tool="tofu", expires="30m")]
plan env:
    tofu plan -out=tfplan
    tofu show -no-color tfplan > tfplan.txt

[applies("plan")]
apply env:
    tofu apply "$RUNE_PLAN_FILE"
```

## Syntax errors (at the token found)

`RUNE1001` for `[plan]`, `[plan()]`, `[plan(file)]`, `[plan(file=tfplan)]`, a trailing comma
and `[applies]`. `[applies()]` and `[applies(plan)]` are reported as `[group()]` is: the
attribute requires a string argument.

## Static errors (`RUNE2020`)

| Case | Span | Message |
|---|---|---|
| Unknown argument | the name | `unknown plan argument "<name>" (expected file, summary, tool, expires)` |
| Argument twice | the second name | `duplicate plan argument "<name>"` |
| `file` or `summary` missing | the attribute | `plan(...) requires <name>="<path>"` |
| Empty value | the value | `plan argument "<name>" is empty` |
| A path that starts with `/` or `\`, has a drive prefix such as `C:`, holds `\`, or has a `..` element | the value | `plan <name> "<value>" must be a relative path with forward slashes inside the task's directory` |
| `file` equals `summary` after `path.Clean` | the summary value | `plan summary must be a different file from the plan file` |
| `tool` holds a path separator | the value | `plan tool "<value>" must be an executable name, not a path` |
| `expires` not a duration in range | the value | `plan expires "<value>" must be a duration from 1m to 168h, such as "30m"` |
| A value holds a character that is not printable | the value | `plan argument "<name>" holds a character that is not printable` |
| Second `[plan]` or `[applies]` on a task | the second | `duplicate [<attr>] on task "<task>"` |
| Both on one task | the `[applies]` | `task "<task>" cannot both make a plan and apply one` |
| With `[cache]`, `[readonly]` or `[context]` | the plan or applies attribute | `[<attr>] cannot be combined with [<other>] on task "<task>"` |
| On an `agent` task | the attribute | `[<attr>] cannot be used on agent task "<task>"` |
| `[applies]` names no task | the attribute | `applies names unknown task "<name>"`, with a hint `did you mean "<nearest>"?` |
| The named task has no `[plan]` | the attribute | `task "<name>" named by applies has no [plan] attribute` |
| Parameters differ | the `[applies]` attribute | `task "<apply>" must have the same parameters as its plan task "<plan>" (<plan signature>)`, where the signature is the plan task's parameters as a header writes them, without defaults, or `no parameters` |
| Body never mentions `RUNE_PLAN_FILE` | the attribute | `task "<apply>" applies a plan but its body does not use RUNE_PLAN_FILE, so it would not apply the reviewed plan` |
| Apply reaches its plan task | the attribute | `task "<apply>" must not run its plan task "<plan>": the plan would be made again after it was approved` |
| `[env]` sets `RUNE_PLAN_FILE`, `RUNE_PLAN_ID` or `RUNE_PLAN_DIGEST` on an apply task | the `[env]` attribute | `task "<apply>" must not set <NAME> with [env]: Rune sets it to the approved plan` |
| A dependency or hook names an apply task | the edge | `task "<apply>" applies a plan and cannot be run by task "<owner>": it has to be called itself` |

Parameters are the same when they agree in number and, position by position, in name, in
parameter kind (required, defaulted, `+`, `*`) and in constraint kind and enum values.
Defaults may differ.

## Language surface

- `internal/language/builtin.go`: `plan`, signature
  `[plan(file="path", summary="path")]`, and `applies`, signature `[applies("plan-task")]`.
- Formatter: `[plan(file="tfplan", summary="tfplan.txt")]`, `[applies("plan")]`.
- Dump: `Attr plan(file="tfplan", summary="tfplan.txt")`, `Attr applies("plan")`.
- VS Code grammar: both names join the attribute alternatives.

## What the body of an apply task receives

| Variable | Value |
|---|---|
| `RUNE_PLAN_FILE` | absolute path of the private copy of the approved plan |
| `RUNE_PLAN_ID` | the plan id |
| `RUNE_PLAN_DIGEST` | `sha256:<hex>` of the artifact |

Only the apply task's own body receives them, not its dependencies or hooks. Rune adds them
last, so they win over the process environment, a dotenv file and exported variables.
