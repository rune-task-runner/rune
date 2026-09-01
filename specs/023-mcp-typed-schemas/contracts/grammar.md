# Grammar Delta: Inline Parameter Constraints, `returns`, `param-doc`

**Feature**: 023-mcp-typed-schemas · applies on top of `docs/GRAMMAR.md` (non-normative;
the parser remains the source of truth). Ships with updated GRAMMAR.md + lexer/parser/fmt/
corpus fixtures in the same PR (Engineering Constraints).

## Param production (replaces the existing three-alternative form)

```ebnf
Param      = Name [ TypeAnno ] [ "=" Expr ]        (* required or defaulted *)
           | "+" Name [ TypeAnno ]                 (* variadic, one-or-more *)
           | "*" Name [ TypeAnno ] ;               (* variadic, zero-or-more *)

TypeAnno   = ":" KindName [ EnumValues ] ;
KindName   = "string" | "number" | "boolean" | "path" | "enum" ;
EnumValues = "(" StringLit { "," StringLit } ")" ;    (* enum only; ≥1 value *)
```

**Lexical side conditions** (not expressible in EBNF; enforced via token span offsets):

1. The `:` of a `TypeAnno` must be **adjacent on both sides** — no whitespace between
   `Name` and `:`, nor between `:` and `KindName`. A spaced colon is always the task
   header colon.
2. In the parameter position, adjacent `IDENT ":" IDENT` is unconditionally a `TypeAnno`;
   kind-name validity is a semantic check (RUNE2012), not a parse decision.
3. `KindName`s are contextual keywords — they stay valid as task/parameter/variable names
   in every other position.
4. `EnumValues` is only legal after `enum` (RUNE1006 otherwise); `enum` without
   `EnumValues`, or with an empty list, is RUNE1006.

**Examples (canonical formatter output)**:

```rune
deploy env:enum("staging","prod") replicas:number="2":
    ./deploy.sh {{env}} {{replicas}}

build target:path="./dist" verbose:boolean="false":
    go build -o {{target}} ./...

test +packages:path:
    go test {{packages}}
```

## AttrItem additions

```ebnf
AttrItem   = ... existing alternatives ...
           | "returns" "(" StringLit ")"                    (* task outcome description *)
           | "param-doc" "(" StringLit "," StringLit ")" ;  (* param name, description *)
```

```rune
# List artifact IDs for a channel.
[returns("JSON array of artifact IDs, one per line")]
[param-doc("channel", "Release channel to query")]
list-artifacts channel:enum("stable","beta"):
    ./scripts/artifacts.sh {{channel}}
```

Semantic rules: `param-doc`'s first string must name a parameter of the task (RUNE2015);
at most one `param-doc` per parameter (RUNE2015); at most one `returns` per task
(RUNE2015).

## Backward compatibility statement

- Every Runefile without annotations parses to an identical AST (SC-003).
- **One documented break** (sanctioned by the constitution v1.1.0 backward-compat
  re-parse exception): an unspaced legacy header `task p:dep …` re-parses as a
  `TypeAnno`. Unknown kinds fail with RUNE2012 + "add a space" hint; the five kind words
  used as unspaced dependency names re-parse validly and are flagged by the RUNE2016
  shadow warning (annotated kind name matches an existing task name), so no meaning
  change is silent. The canonical formatter has only ever emitted the spaced form. No
  `rune_version` pragma gate — recorded in plan.md Complexity Tracking with the four
  exception conditions.
- The compatibility corpus (`test/corpus`) gains annotated fixtures; the regenerated
  golden is a reviewed, deliberate change.
