# Contract: Runefile Surface for Safe Quoting

**Feature**: 030-safe-param-quoting. Observable behavior of `quote_each()`, the
`raw` kind, and variadic elements. Every example is a test case.

## `quote_each(x)`

Signature in the built-in table and editor hover:

```text
quote_each(list) -> string
Each element of a variadic parameter quoted as a separate shell argument.
```

```rune
c *names:
    @printf "[%s]\n" {{quote_each(names)}}

n *names:
    @set -- {{quote_each(names)}}; echo "$#"
```

| Invocation | stdout |
|---|---|
| `rune c yq 'x; echo pwned' tofu` | `[yq]` `[x; echo pwned]` `[tofu]` (three lines) |
| `rune c 'a b' '' "it's"` | `[a b]` `[]` `[it's]` |
| `rune n` | `0` |
| `rune n 'a b' c` | `2` |
| MCP `c` with `{"names": ["yq", "x; echo pwned", "tofu"]}` | same three lines as the first row, then `[exit 0]` |
| MCP `n` with `{"names": "a b"}` | `1` |

Quoting is `quote()`'s: each element in single quotes, an embedded `'` written as
`'\''`. An empty element is `''`. An empty list expands to nothing.

`quote_each` of anything other than a bare reference to a variadic parameter is
`quote()` of its value: `quote_each(name)`, `quote_each("a b")`, and
`quote_each(names + "")` each produce one quoted word.

Unchanged forms (byte-identical to v0.6.0):

| Body | `rune <task> yq 'x; echo pwned' tofu` interpolates to |
|---|---|
| `printf "[%s]\n" {{names}}` | `printf "[%s]\n" yq x; echo pwned tofu` |
| `printf "[%s]\n" {{quote(names)}}` | `printf "[%s]\n" 'yq x; echo pwned tofu'` |

## The `raw` kind

Grammar delta (`docs/GRAMMAR.md`):

```text
KindName    = "string" | "number" | "boolean" | "path" | "raw" | "enum" ;
```

```rune
test flags:raw="-v -race":
    go test {{flags}} ./...

run *args:raw:
    ./tool {{args}}
```

- Accepts any value, including the empty string, like an unannotated parameter.
- Interpolated text is unchanged: `{{flags}}` is `-v -race`.
- `rune --fmt` writes the annotation as `flags:raw="-v -race"` and `*args:raw`.
- Hover on the parameter shows `flags:raw` and the line
  "Split into shell words without quoting."
- No RUNE2017 warning for the parameter.
- `x:raw` in an older Runefile was a RUNE2012 error, so no file that ran before
  changes meaning.

## Dependency forwarding

| Runefile | Invocation | Target receives |
|---|---|---|
| `ci *pkgs: (test pkgs)` / `test *pkgs:` | `rune ci ./a './b c'` | `pkgs` = [`./a`, `./b c`] |
| `release *targets: (build "linux" targets)` / `build os *targets:` | `rune release a b` | `os` = `linux`, `targets` = [`a`, `b`] |
| `tag *names: (label names)` / `label text:` | `rune tag a b` | `text` = `a b` |
| `ci *pkgs: (test pkgs)` / `test *pkgs:enum("a","b"):` | `rune ci a b` | `pkgs` = [`a`, `b`], both pass the check |
| `ci *pkgs: (test pkgs)` / `test +pkgs:` | `rune ci` | `pkgs` = []; `{{pkgs}}` is empty text, as v0.6.0 |
| `ci *pkgs: (test pkgs)` / `test +pkgs:path:` | `rune ci ./a ''` | `pkgs` = [`./a`, the empty string]; accepted because v0.6.0's text `./a ` passes |
| `ci *pkgs: (test "x" pkgs)` / `test *pkgs:` | `rune ci a` | `pkgs` = [`x`, `a`] |
| `ci *pkgs: (test "x" pkgs)` / `test *pkgs:` | `rune ci` | `pkgs` = [`x`]; `{{pkgs}}` is `x` plus a trailing space, as v0.6.0 |

The text `{{pkgs}}` gives in the target is v0.6.0's in every row: the written
arguments from the variadic's position on, evaluated and joined by spaces.

The rule applies to dependencies, post-hooks (`&& …`), and failure hooks
(`|| …`) alike. A bare reference is the parameter name alone, not inside a
concatenation or call.

## Run-once and cache identity

- `rune ci` with dependencies `(lint "a b")` and `(lint "a" "b")` runs `lint`
  twice: the element lists differ.
- `(lint "a")` twice runs `lint` once, as today.
- A `[cache]` task's fingerprint includes the elements of each variadic it
  references, so `rune lint 'a b'` and `rune lint a b` are cached separately.
