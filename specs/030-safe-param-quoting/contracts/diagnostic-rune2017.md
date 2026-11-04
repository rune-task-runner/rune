# Contract: RUNE2017, Unquoted Parameter in a Shell Command

**Feature**: 030-safe-param-quoting. Severity: warning. Reported by
`rune analyze` (text and `--json`) and the language server. Never reported or
printed during a task run, and never changes an exit code.

## Message

```text
parameter "<p>" is interpolated into a shell command unquoted; write {{quote(<p>)}} outside any shell quotes, or declare <p>:raw if word splitting is intended
```

For a variadic, the fix names `quote_each` and the declared sigil:

```text
parameter "<p>" is interpolated into a shell command unquoted; write {{quote_each(<p>)}} outside any shell quotes, or declare *<p>:raw if word splitting is intended
```

The span is the whole `{{ … }}` interpolation. Each warned parameter is reported
at most once per interpolation, in order of first appearance.

## When it fires

All three must hold:

1. The task body is a shell body ([data-model.md](../data-model.md#shell-body-static-rule-research-r7)).
2. The parameter has no kind, or kind `string` or `path` (scalar or variadic).
3. The parameter appears in the interpolation outside every `quote()` and
   `quote_each()` call, and not only as an operand of a conditional's comparison.

## Fixture (golden test)

Rows 11 and 12 keep a space before the closing `}}`: an interpolation ends at
the first `}}`, so `{ "" }}}` would cut the expression short.

`deploy env`, `names` (`*names`), `version`, `n:number`, `ok:boolean`,
`mode:enum("a","b")`, `flags:raw`, all in a default-shell task unless noted.

| # | Body line | Expected |
|---|---|---|
| 1 | `./deploy.sh {{env}}` | warn `env` (quote) |
| 2 | `printf "[%s]\n" {{names}}` | warn `names` (quote_each, `*names:raw`) |
| 3 | `echo "hello {{env}}"` | warn `env` |
| 4 | `echo '{{env}}'` | warn `env` |
| 5 | `./deploy.sh {{quote(env)}}` | none |
| 6 | `printf "[%s]\n" {{quote_each(names)}}` | none |
| 7 | `echo {{quote("v" + version)}}` | none |
| 8 | `echo {{"--env=" + quote(env)}}` | none |
| 9 | `echo {{"--env=" + env}}` | warn `env` |
| 10 | `echo {{trim(env)}}` | warn `env` |
| 11 | `echo {{ if env == "prod" { "--strict" } else { "" } }}` | none |
| 12 | `echo {{ if mode == "a" { env } else { "" } }}` | warn `env` |
| 13 | `echo {{env + env}}` | warn `env` once |
| 14 | `echo {{env}} {{env}}` | warn `env` twice (two interpolations) |
| 15 | `echo {{n}} {{ok}} {{mode}}` | none |
| 16 | `go test {{flags}}` | none |
| 17 | `echo {{version}}` where `version` is a module variable, not a parameter | none |
| 18 | `(python)` body: `print("{{env}}")` | none |
| 19 | `(node)` body, agent body, `(ruby)` custom-executor body with `{{env}}` | none |
| 20 | `[script("bash -eu")]` body: `echo {{env}}` | warn `env` |
| 21 | `[script("python3")]` body: `print("{{env}}")` | none |
| 22 | file with `set shell := ["bash", "-eu"]`, default body `echo {{env}}` | warn `env` |
| 23 | file with `set shell := ["pwsh", "-Command"]`, default body `echo {{env}}` | none |
| 24 | file with `set shell := ["nix", "develop", "-c", "bash"]`, default body `echo {{env}}` | warn `env` |
| 25 | dependency argument `(deploy env)` | none (not a body) |
| 26 | `[confirm("Deploy {{env}}?")]` | none (not a body) |
| 27 | `echo {{path_exists(env)}}` | warn `env` (functions are not modeled) |
| 28 | `(bash)` custom-executor body: `echo {{env}}` | warn `env` |

`rune analyze` on the fixture exits 0. `rune analyze --json` lists each warning
with `"severity": "warning"` and `"code": "RUNE2017"`.

## Docs

`docs/diagnostics.md` gains:

```text
| `RUNE2017` | A string or path parameter reaches a shell command unquoted: **warning**, never gates execution or exit code. Write `{{quote(p)}}` (or `{{quote_each(p)}}` for a variadic) outside any shell quotes, or declare `p:raw` when word splitting is intended |
```

and the RUNE2xxx heading lists RUNE2017 among the warnings.
