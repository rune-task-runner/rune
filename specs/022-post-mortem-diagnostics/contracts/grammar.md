# Contract: Grammar surface for failure hooks

**Feature**: 022-post-mortem-diagnostics · Status: Draft

This is the user-facing DSL contract. `docs/GRAMMAR.md` must match it exactly
when the feature ships (docs harness + corpus enforce drift).

## EBNF delta

```ebnf
Task        = { Attribute } Signature ":" [ Deps ]
              [ "&&" PostHooks ] [ "||" FailHooks ]
              NEWLINE INDENT Body DEDENT ;

PostHooks   = DepCall { DepCall } ;   (* unchanged *)
FailHooks   = DepCall { DepCall } ;   (* NEW — same production as PostHooks *)
```

`DepCall` is unchanged: bare `Name`, or `"(" Name { Expr } ")"` to pass
arguments.

## Token delta

| Lexeme | Token | Today | After |
|---|---|---|---|
| `\|\|` | `PIPEPIPE` (new) | RUNE1001 illegal ×2 | single operator token |
| `\|` (lone) | — | RUNE1001 illegal | RUNE1001 illegal (unchanged) |

## Accepted / rejected forms

| Input | Verdict |
|---|---|
| `t: a && b \|\| c` | ✅ dep `a`, success hook `b`, fail hook `c` |
| `t: \|\| c` | ✅ no deps, no success hooks, one fail hook |
| `t: a \|\| c` | ✅ dep `a`, fail hook `c` |
| `t: a && b \|\| (c "arg")` | ✅ parenthesized fail hook with args |
| `t: a \|\| c d` | ✅ two fail hooks, run in order `c`, `d` |
| `t: a \|\| c && b` | ❌ parse error — `\|\|` clause must come last |
| `t: a \| c` | ❌ RUNE1001 (lone `\|` stays illegal) |

Clause order is fixed: deps, then `&&`, then `||`.

## Formatting canon (`rune fmt`)

One space around the operators, hooks in declaration order, single line:
`t: a && b || c`. Idempotency guaranteed by the existing
`TestFmtIdempotent` harness.

## Backward compatibility (FR-013)

`||` and lone `|` are lex-illegal today, so no currently-valid Runefile
changes meaning. The compatibility corpus (`testdata/corpus/full.rune` +
`full.ast`) gains a fail-hook task; any silent grammar drift fails
`test/corpus`.
