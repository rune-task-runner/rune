# Runefile Grammar

> **Non-normative.** The hand-written parser (`internal/parser`) is the source of
> truth. This document is generated/maintained from
> [`specs/001-rune-task-runner/contracts/grammar.md`](../specs/001-rune-task-runner/contracts/grammar.md).
>
> **Sync discipline (Constitution Principle VI):** any PR that changes DSL
> surface MUST update this file *and* the golden/integration fixtures
> (`testdata/lexer`, `testdata/parser`, `testdata/fmt`, `testdata/corpus`) in the
> same PR. The compatibility-corpus test (`test/corpus`) guards against silent
> grammar drift.

Bodies use **significant indentation**. `NEWLINE`, `INDENT`, `DEDENT` are
produced by the lexer.

```ebnf
File        = { Item } ;
Item        = Comment | Setting | Assignment | Import | Mod | Task ;

Comment     = "#" { any-char } NEWLINE ;          (* run of comments above a Task = its doc *)

Setting     = "set" Name [ ":=" Value ] NEWLINE ; (* bare form ≡ ":= true" *)
Value       = Expr | List ;
List        = "[" [ Expr { "," Expr } ] "]" ;

Assignment  = Name ":=" Expr NEWLINE ;

Import      = "import" [ "?" ] StringLit NEWLINE ;
Mod         = "mod" Name [ StringLit ] NEWLINE ;

Task        = { Attribute } Signature ":" [ Deps ] [ "&&" PostHooks ]
              [ "||" FailHooks ] NEWLINE INDENT Body DEDENT ;
Signature   = Name { Param } [ "(" Executor ")" ] ;
Param       = Name [ "=" Expr ]            (* defaulted *)
            | "+" Name                     (* variadic, one-or-more *)
            | "*" Name ;                   (* variadic, zero-or-more *)
Executor    = "sh" | "python" | "node" | "agent" | Name ;
Deps        = DepCall { DepCall } ;
PostHooks   = DepCall { DepCall } ;               (* run after, on success *)
FailHooks   = DepCall { DepCall } ;               (* run after, on failure *)
DepCall     = Name | "(" Name { Expr } ")" ;     (* parenthesized form passes args *)

Body        = { BodyLine } ;
BodyLine    = [ "@" ] [ "-" ] Text NEWLINE ;     (* @=no-echo, -=continue-on-error *)
Text        = { TextChar | Interpolation } ;
Interpolation = "{{" Expr "}}" ;                 (* "{{{{" escapes a literal brace *)

Attribute   = "[" AttrItem { "," AttrItem } "]" NEWLINE ;
AttrItem    = "private"
            | "confirm" [ "(" StringLit ")" ]
            | "group" "(" StringLit ")"
            | "parallel"
            | "linux" | "macos" | "windows" | "unix"
            | "no-cd"
            | "network"                  (* sets MCP openWorldHint *)
            | "no-exit-message"          (* suppress the trailing error banner *)
            | "context"                  (* project-health hook injected into agent context *)
            | "working-directory" "(" StringLit ")"
            | "env" "(" StringLit "," StringLit ")"
            | "doc" "(" StringLit ")"
            | "script" "(" StringLit ")"
            | "cache" "(" "inputs" "=" List [ "," "outputs" "=" List ] ")" ;

(* ---- Expression sublanguage (Pratt-parsed; total, non-Turing-complete) ---- *)
Expr        = Conditional | Concat ;
Conditional = "if" Expr CmpOp Expr "{" Expr "}"
              { "else" "if" Expr CmpOp Expr "{" Expr "}" }
              "else" "{" Expr "}" ;
Concat      = Primary { ("+" | "/") Primary } ;   (* "+"=concat, "/"=path-join *)
Primary     = StringLit | FuncCall | VarRef | "(" Expr ")" ;
FuncCall    = Name "(" [ Expr { "," Expr } ] ")" ;
VarRef      = Name ;
CmpOp       = "==" | "!=" | "=~" ;                (* =~ = regex match *)

StringLit   = "'" { ch } "'" | "\"" { ch } "\""
            | "'''" { ch } "'''" | "\"\"\"" { ch } "\"\"\"" ;  (* triple = de-dented *)
Name        = letter { letter | digit | "_" | "-" } ;
```

## Backward compatibility

Breaking changes to Runefile semantics are opt-in per file via a version pragma:

```rune
set rune_version := "1"
```

The default interpretation never changes under a user without such a pragma
(Constitution Governance / FR-033).

## Minimum Rune version

A project can pin the minimum Rune **binary** release it requires — distinct from
`rune_version`, which pins the Runefile *language* version:

```rune
set minimum_version := "0.8.0"
```

The value must be a static string literal holding a single valid semantic version
(`MAJOR.MINOR.PATCH`, optionally with a `-prerelease`/`+build` suffix); it means
"requires Rune ≥ this version". Before imports are spliced, before analysis, and
before any task runs, Rune compares the installed version (SemVer 2.0.0
precedence) and refuses an older binary with an actionable diagnostic, executing
nothing. Only the **root** Runefile's `minimum_version` is effective — an imported
file cannot impose or relax it. A non-static or non-semver value is a static
error. Ranges are not supported. Use `rune --ignore-version` to bypass the check
(it prints a warning) and `rune version --check` to report compatibility.

## Secret masking settings

Two list-valued settings control output masking (values of sensitive variables
are replaced with `***` in everything Rune emits — task output, echoed
commands, status lines, and MCP tool results):

```rune
set secrets := ["DEPLOY_CFG", "UPLOAD_URL"]   # extra names whose values are masked
set unmasked := ["OAUTH_METHOD"]              # exempt from the built-in name patterns
```

Both take a list of static string elements naming environment variables.
Masking itself is always on: variables whose names contain `TOKEN`, `SECRET`,
`PASSWORD`, `PASSWD`, `APIKEY`, `API_KEY`, `PRIVATE_KEY`, `ACCESS_KEY`,
`CREDENTIAL`, or `AUTH` (case-insensitive) are masked automatically; `secrets`
adds names the patterns miss, `unmasked` exempts false positives. Listing the
same name in both is a static error citing both spans. A listed name absent
from the environment is inert. See the
[secret masking guide](how-to/secret-masking.md) for semantics and limits.
