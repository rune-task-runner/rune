# Diagnostic Codes Delta: 023-mcp-typed-schemas

Append-only delta to the public diagnostic-code contract
(`internal/diag/codes.go`, `docs/diagnostics.md`,
`specs/011-rune-lsp/contracts/diagnostic-codes.md` — all three updated in lockstep).

Prior state: parser codes end at RUNE1005, semantic at RUNE2011.

| Code | Severity | Layer | Meaning | Span anchor |
|---|---|---|---|---|
| RUNE1006 | error | parser | Malformed parameter constraint syntax: `enum` without a value list, empty `enum()`, non-string enum value, or a value list on a non-`enum` kind. | the offending token run within the annotation |
| RUNE2012 | error | analyzer | Unknown parameter type kind. Message lists the supported kinds (`string`, `number`, `boolean`, `path`, `enum`). When the unknown kind matches an existing task name, a related note suggests: "if it was meant as a dependency, add a space after the parameter". | `Constraint.Sp` |
| RUNE2013 | error | analyzer | Duplicate enum value in a parameter's value list. Related span points at the first occurrence. | duplicate value literal |
| RUNE2014 | error | analyzer | A parameter's literal default violates its own constraint (out-of-enum, non-numeric for `number`, non-canonical boolean, empty `path`). Non-literal defaults are enforced by the identical runtime check at bind time. | default expression span |
| RUNE2015 | error | analyzer | Annotation-attribute misuse: `[param-doc]` names a parameter the task does not declare, repeats a `param-doc` for the same parameter, or a task carries more than one `[returns]`. Related span points at the first occurrence for duplicates. | attribute span |
| RUNE2016 | warning | analyzer | A parameter's annotated kind name is also the name of an existing task — an unspaced legacy header (e.g. `env:string`) may have silently re-parsed from "dependency on task `string`" to a type annotation. Message names both readings and the spaced spelling that restores the dependency (`env: string`). | `Constraint.Sp` |

Test obligations (per repo convention): one table row each in
`internal/parser/codes_test.go` (RUNE1006) and `internal/analyzer/codes_test.go`
(RUNE2012–2016), asserting code, severity, and `Span.IsValid()`.

RUNE2016 is the mechanism that satisfies condition (3) of the constitution's
backward-compat re-parse exception (v1.1.0) for the kind-word shadow case: the
re-parse is loud, never silent.

Once shipped, meanings never change (codes.go PUBLIC CONTRACT header).
