# Data Model: Safe Quoting for Task Parameters

**Feature**: 030-safe-param-quoting | **Date**: 2026-11-03

Rune keeps no persistent data for this feature beyond the existing cache record.
The entities below are in-memory values and static rules.

## Bound arguments (`eval.Args`)

The parameters of one task invocation after binding (research R1).

| Field | Type | Meaning |
|---|---|---|
| `Values` | `map[string]string` | Every bound parameter. A variadic's value is its elements joined by single spaces, the same text as v0.6.0. |
| `Lists` | `map[string][]string` | Variadic parameters only. The elements as given, in order. Present (possibly empty) for every variadic of the task. |

**Invariants**

- For every key `k` in `Lists`, `Values[k] == strings.Join(Lists[k], " ")`, with
  one exception on the dependency path: `Values[k]` keeps v0.6.0's text, the
  written arguments joined by spaces, so an empty forwarded list beside other
  arguments leaves an extra space (`(test "x" pkgs)` with no elements gives
  `x `).
- `Lists` has at most one key: a task has at most one variadic parameter, and it
  is the last one.
- `Lists[k]` may be empty for a `*` parameter. A `+` parameter with no elements
  is a binding error on the command line and over MCP; a dependency call that
  forwards an empty list into it still binds, as v0.6.0 did, with `Values[k]`
  empty and no elements.
- On the dependency path, the elements pass the target's checks one by one, or
  the written arguments pass them as v0.6.0 checked them (research R4).
- `Args.Key()` is a canonical encoding of both maps. Two `Args` with equal `Key()`
  bind the same values and the same elements.

**Sources of elements** (FR-006, FR-007)

| Source | One element is |
|---|---|
| Command line | one shell argument after the task name, at or after the variadic's position |
| MCP, array value | one array element (numbers and booleans rendered as today) |
| MCP, scalar value for a variadic | the whole value, as one element; an empty string is no elements |
| MCP, `*` parameter omitted | no elements |
| Dependency or hook call | one written argument at or after the target's variadic index; a bare reference to the caller's variadic in that range contributes all of the caller's elements |

## Scope additions (`eval.Scope`)

| Field / method | Meaning |
|---|---|
| `Lists map[string][]string` | Copied from `Args.Lists`. Read only by `quote_each`. |
| `WithArgs(Args) *Scope` | Shallow copy with `Params = Values` and `Lists = Lists`. |
| `WithParams(map[string]string)` | Unchanged; used for default evaluation during binding (no lists needed). |

## `quote_each` evaluation

| Argument | Result |
|---|---|
| bare reference `names` with `Lists["names"] = [e1 … en]`, n ≥ 1 | `quote(e1) + " " + … + " " + quote(en)` |
| bare reference `names` with `Lists["names"] = []` | `""` |
| anything else (scalar, literal, concatenation, call, conditional) | same as `quote(value)` |
| not exactly one argument | the arity error `quote()` gives |

## Parameter kinds (`ast.ConstraintKind`)

| Kind | Accepts | Warned by RUNE2017 | MCP schema |
|---|---|---|---|
| none (unannotated) | any value | yes | `string` |
| `string` | any value | yes | `string` |
| `path` | non-empty value | yes | `string`, `format: path` |
| `number` | decimal number | no | `number` |
| `boolean` | `true` / `false` | no | `boolean` |
| `enum("a", …)` | one of the listed values | no | `string`, `enum` |
| `raw` (new) | any value | no | `string`, description gains "Split into shell words without quoting." |

A variadic of any kind becomes an `array` of the item schema, as today.

## Shell body (static rule, research R7)

| Task declares | Shell body when |
|---|---|
| `[script("cmd …")]` (non-empty) | some field of `cmd …` is a known shell |
| no `[script]`, executor `sh` or none, file has `set shell` | some string-literal element of the list is a known shell |
| no `[script]`, executor `sh` or none, no `set shell` | always (built-in shell) |
| no `[script]`, custom executor | the executor name is a known shell (`(bash)`, `(/bin/zsh)`) |
| executor `python`, `node`, or `agent` | never |

Known shells: `sh`, `bash`, `dash`, `zsh`, `ksh`, `mksh`, `ash`, `fish`, by base
name, ignoring case and a `.exe` suffix (`runtime.IsKnownShell`).

## RUNE2017 finding

| Field | Value |
|---|---|
| Severity | warning |
| Code | `RUNE2017` |
| Span | the `{{ … }}` interpolation |
| Message | see [contracts/diagnostic-rune2017.md](contracts/diagnostic-rune2017.md) |
| Count | at most one per warned parameter per interpolation |

## Cache fingerprint delta

`cache.Spec.Vars` gains one entry per variadic parameter the task references: key
`<name>[]`, value the elements through `strconv.Quote`, joined by spaces. Existing
entries are unchanged. Tasks without a referenced variadic keep their fingerprint.
