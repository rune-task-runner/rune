# Research: Safe Quoting for Task Parameters

**Feature**: 030-safe-param-quoting | **Date**: 2026-11-03 | **Spec**: [spec.md](spec.md)

## Baseline (v0.6.0, read from the code)

- **Binding joins early.** `bindParams` (`internal/cli/args.go`) checks each
  variadic value and then stores `strings.Join(pos[i:], " ")`. The MCP server's
  `decodeArgs` (`mcpserver/handler.go`) does the same with array elements before
  calling `Engine.Call(ctx, name, map[string]string)`. After binding, no layer
  can tell `["a b"]` from `["a", "b"]`.
- **One map type everywhere.** Bound parameters are `map[string]string` in the
  scheduler (`Engine.ResolveDep`, `Execute`, `ExecuteFailHook`, `Invocation`,
  `memoKey`), the CLI engine (`runBodyTo`, `cacheSpec`, `collectVars`), and
  `eval.Scope.Params`. Only `memoKey` iterates the map.
- **Dependency arguments.** `ResolveDep` evaluates each `dep.Args` expression to a
  string and calls `bindParams`. Forwarding `(test pkgs)` passes the caller's
  joined text as one element.
- **`quote()`** is `unary("quote", shellQuote)`: POSIX single quotes with `'\''`
  for embedded quotes. Builtin arity is checked at evaluation time only; the
  analyzer checks that the function name exists.
- **Warnings.** `CheckDocumentation` (RUNE2010) is a separate pass that
  `analysis.Service.Analyze` appends. `rune analyze` and the language server use
  that service; the run path calls `analyzer.Analyze` only and prints diagnostics
  only when there are errors.
- **Kinds.** `ast.SupportedConstraintKinds` is the single kind list, used by the
  parser hint and RUNE2012. The language server has no kind completion; hover on a
  parameter shows `name:kind` and its `[param-doc]` text.
- **Cache.** The fingerprint hashes the raw body plus `Vars`, the evaluated value
  of each referenced name. A variadic contributes its joined text.
- **Known shells.** `runtime.isKnownShell` (spec 029) matches `sh`, `bash`, `dash`,
  `zsh`, `ksh`, `mksh`, `ash`, `fish` by base name, ignoring case and `.exe`.

## R1: Carrying elements through binding and scheduling

**Decision**: Add a value type in `internal/eval`:

```go
type Args struct {
	Values map[string]string   // every bound parameter; a variadic is its elements joined by " "
	Lists  map[string][]string // variadic parameters only: the elements as given
}
```

`Args` replaces `map[string]string` for bound parameters in the scheduler's
`Engine` interface, `scheduler.Invocation`, and the CLI engine. `Scope` gains
`WithArgs(Args)`, which sets `Params` and a new `Lists` field. `WithParams` stays
for default evaluation in `bindParams`, which never needs lists: a default can
only see earlier parameters, and the variadic is always last. `Args.Key()`
replaces the body of `memoKey`.

**Rationale**: The type states the invariant: `Values` is what every existing
reader sees, unchanged; `Lists` is new data that only `quote_each`, forwarding,
and identity read. The scheduler stays ignorant of what the lists mean.

**Alternatives considered**:
- Hidden map keys such as `"*names"` holding an encoded list. No interface
  changes, but it is stringly typed and every future reader of the map has to
  know to skip the keys (Principle VIII).
- A second `map[string][]string` argument next to every params map. Doubles every
  signature and is easy to drop on one path.
- Storing an encoded list as the variadic's value and decoding it in `eval`. Every
  direct reader of the map would see the encoding.

## R2: Evaluating `quote_each`

**Decision**: Register `"quote_each": unary("quote_each", shellQuote)` in
`builtins()`, so `IsBuiltin`, `BuiltinNames`, the arity error, and the fallback
(FR-003, FR-004) match `quote()`. In `callBuiltin`, before arguments are evaluated,
handle the list case: when the name is `quote_each`, there is exactly one argument,
it is an `*ast.VarRef`, and `scope.Lists` has that name, return each element passed
through `shellQuote`, joined by single spaces. An empty list returns `""` (FR-002).
Map membership (`elems, ok := Lists[name]`) separates an empty variadic from a
non-variadic name.

The `internal/language` registry gains
`quote_each(list) -> string`, "Each element of a variadic parameter quoted as a
separate shell argument." `builtin_sync_test.go` keeps the registry and `eval` in
step.

**Rationale**: The only special case is in the one place that sees the AST. Name
resolution order is unchanged, so a parameter still shadows a variable of the same
name, and `Lists` holds only the current task's variadic.

**Alternatives considered**: Passing elements through the string value with a
separator byte. Rejected for the same reason as the encoded value in R1.

## R3: Elements over MCP without breaking `mcpserver`

**Decision**: Keep `Engine.Call` as it is. Add an optional interface to the public
package:

```go
// ListEngine is an optional Engine extension. When the engine implements it,
// array arguments reach it with their elements intact (spec 030). args still
// holds every value, arrays joined by single spaces, exactly as Call receives it.
type ListEngine interface {
	CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (Result, error)
}
```

`decodeArgs` also returns `lists`: for a parameter the server knows is variadic, an
array gives its elements, a non-empty scalar gives a one-element list, and an
empty scalar gives an empty list (FR-006). The
handler calls `CallLists` when the engine implements `ListEngine`, otherwise `Call`.
`mcpAdapter` implements both; `Call` delegates with `nil` lists. `bindNamedParams`
takes the lists: a variadic present in `lists` binds those elements and checks
each one again, as it checks scalars; an omitted `*` or an empty value binds zero
elements. Per-element checks stay in `decodeArgs` as today.

**Rationale**: `mcpserver` is public and embeddable (Principle IV). An optional
interface, checked by type assertion, adds the capability without changing a
signature that embedders implement.

**Alternatives considered**: Changing `Engine.Call` to take `map[string]any` or
`map[string][]string`. Breaks every embedder for a feature most do not need.

## R4: Forwarding a variadic to a dependency

**Decision**: In `ResolveDep`, find the target's variadic index `v` (or none).
Arguments before `v` map one to one to scalar parameters, as today. For each
argument at index `v` or later, if it is a bare `*ast.VarRef` whose name is in the
caller's `Lists`, append the caller's elements; otherwise append the evaluated
value. `bindParams` then binds the flat list as for CLI arguments, which gives the
variadic its elements and runs the per-element checks (FR-007, FR-008). Two rules
keep v0.6.0's behavior:

- When that binding fails, `ResolveDep` binds the written arguments' values as
  v0.6.0 did. If that succeeds, the call is accepted with the spread elements. So
  every call v0.6.0 accepted still runs: an empty list forwarded into `+` (the
  spread is empty, the written argument is `""`), or an element that fails a check
  its joined text passes (`./a` and `""` against `path`). If both fail, the error
  is the per-element one.
- The variadic's `Values` entry is v0.6.0's text, the written arguments from `v`
  on evaluated and joined by spaces. It differs from the join of the elements
  only when an empty list is forwarded (`(test "x" pkgs)` gives `x ` with a
  trailing space, as before).

The same code serves dependencies, post-hooks, and failure hooks, since all three
go through `ResolveDep`.

The analyzer's arity check still counts written arguments. `(test pkgs)` is one
argument, so it passes statically against `test +pkgs`, and at runtime the empty
case runs `test` with no elements.

**Rationale**: Spreading only into the variadic slot keeps scalar binding and
arity exactly as today (FR-007's scalar clause), so the only behavior change is
inside the variadic: different element boundaries, the same joined text.

**Alternatives considered**:
- No forwarding. `quote_each` in the dependency would see one element, which
  breaks the guarantee across task composition.
- Failing an empty list forwarded into `+` with the usage error, or checking
  forwarded elements only one by one. Clearer, but a Runefile that runs today
  would start failing with no opt-in, which the constitution's
  backward-compatibility rule forbids.
- Binding one empty element for an empty list forwarded into `+`. Runs as today,
  but `quote_each` then passes `''`, an argument the user never gave.
- Spreading into any slot. A forwarded list could then fill scalar parameters and
  change arity at runtime, a silent meaning change.

## R5: Identity for run-once and the cache

**Decision**: `Args.Key()` writes `name=` plus `strconv.Quote(value)` for each value
in sorted order, then `name[]=` plus each element through `strconv.Quote` for each
list. Memo keys live only for one run, so the format can change freely.

For the cache, `collectVars` adds one entry per referenced variadic parameter:
key `<name>[]`, which no identifier can collide with, and the elements through
`strconv.Quote`, joined by spaces. This changes the fingerprint of every `[cache]`
task that references a variadic parameter, so each such task runs once after the
upgrade.

**Rationale**: FR-009 needs both identities to see element boundaries. A one-time
re-run is the safe direction under Principle I, the same trade spec 029 made
(029 R5). The release notes mention it.

**Alternatives considered**: Adding the cache entry only when the task uses
`quote_each`. Keeps old fingerprints, but couples the cache to one function name,
so any later feature that reads elements would reuse stale entries.

## R6: The analyzer warning (RUNE2017)

**Decision**: A new pass, `analyzer.CheckQuoting(f *ast.File) diag.List`, with code
`diag.CodeUnquotedParam = "RUNE2017"`. Like `CheckDocumentation`, it is appended in
`analysis.Service.Analyze` and is not called on the run path, so it reaches
`rune analyze` (text and `--json`) and the language server, and never runs or
prints during a task run (FR-014).

For each task with a shell body (R7), the pass takes the warned parameters: those
with no constraint, or kind `string` or `path`. For each interpolation in the body
(the existing `extractInterps` and `parseFragment`), it walks the expression:

| Node | Rule |
|---|---|
| `VarRef` to a warned parameter | report |
| `Binary` (`+`, `/`) | walk both sides |
| `FuncCall` named `quote` or `quote_each` | stop: everything inside is quoted |
| any other `FuncCall` | walk the arguments |
| `Conditional` | skip comparison operands; walk each result and `else` |
| `StringLit` | nothing |

Each warned parameter is reported at most once per interpolation, in order of
first appearance, at the interpolation's span. Fragments that fail to parse are
skipped; `Analyze` already reports them.

Message, with the sigil as declared (`*` or `+`) for a variadic:

```text
parameter "env" is interpolated into a shell command unquoted; write {{quote(env)}} outside any shell quotes, or declare env:raw if word splitting is intended
parameter "names" is interpolated into a shell command unquoted; write {{quote_each(names)}} outside any shell quotes, or declare *names:raw if word splitting is intended
```

**Rationale**: A quote call anywhere above a reference makes that reference safe,
so `{{"--env=" + quote(env)}}` is accepted. The spec text said "outermost", which
would warn on that safe form; FR-012 is updated to match this rule. Functions are
not modeled individually: any function other than the two quote functions is
assumed to pass its argument's text through. That over-warns rare forms such as
`{{path_exists(p)}}`, where wrapping in `quote()` is harmless.

**Alternatives considered**:
- Putting the check inside `Analyze`. Its warnings would print beside every error
  on the run path, adding noise to failures that have nothing to do with quoting.
- Detecting shell quotes around the interpolation to tailor the hint. Needs a
  shell lexer for every body line; the fixed hint "outside any shell quotes"
  covers both cases.

## R7: Which bodies are shell bodies (static)

**Decision**: Mirror `runtime.Select`'s order using only static information:

1. A `[script("...")]` attribute with a non-empty string: a shell body when any
   whitespace-separated field names a known shell.
2. Otherwise, executor `""` or `sh`: when the composed file has a `shell` setting
   with at least one element, a shell body when any string-literal element names a
   known shell; with no `shell` setting, it is the built-in shell, so a shell body.
3. `python`, `node`, `agent`, and custom executors: not shell bodies.

`runtime.isKnownShell` becomes exported as `runtime.IsKnownShell` so both packages
use one list. `internal/analyzer` importing `internal/runtime` adds no cycle:
`runtime` imports `ast` and `config`, neither of which imports `analyzer`.

**Rationale**: The language server must not read the environment to evaluate
`set shell`, so non-literal elements are not evaluated. A list such as
`[env("SHELL"), "-eu"]` gets no warning; this is a documented miss, not a false
alarm. A wrapper such as `["nix", "develop", "-c", "bash"]` contains `bash`, so it
is a shell body.

**Alternatives considered**: Evaluating settings with `config.ResolveSettings`.
Makes editor diagnostics depend on the editor's environment.

## R8: The `raw` kind

**Decision**: Add `KindRaw` to `ast.ConstraintKind`, `"raw"` to
`SupportedConstraintKinds` and `ConstraintKindFromName`. `Constraint.Check`
accepts every value for `KindRaw`, as for `KindString`. The formatter, AST dump,
and hover already render `c.KindName` through `Constraint.String()`, so they need
no change beyond fixtures. Hover on a `raw` parameter adds one line: "Split into
shell words without quoting." `mcpserver.ParamInfo.check` needs no case
(unknown kinds accept), and `scalarType` already returns `"string"`.

`inputSchema` appends "Split into shell words without quoting." to the property's
`description` for kind `raw`, after any `[param-doc]` text and separated by a
space, on the array property for a variadic. The `[returns]`/`[param-doc]` cap
applies to the author text before the sentence is added.

The language server has no kind completion for any kind today, so FR-018's
completion clause is dropped from the spec rather than adding a completion context
for one kind. Hover covers the "explain it" part.

**Rationale**: A description is what an agent actually reads. A custom JSON
Schema `format` value is ignored by most clients.

**Alternatives considered**: Naming the kind `words` or `shell`. `raw` was the name
the user accepted, and it says what interpolation does with the value.

## R9: Constitution Principle III

**Decision**: Amend the constitution in this PR, v1.1.0 to v1.2.0 (MINOR), adding
to Principle III:

> The built-in function library MAY grow through the normal spec process, provided
> each new function adds no syntax, is total and free of side effects, is known to
> the analyzer and the language registry, and ships with documentation and
> fixtures.

The Sync Impact Report records the change. `.specify/templates/plan-template.md`
needs no structural change.

**Rationale**: Principle III says the expression sublanguage is frozen and that
growing it requires an amendment. A new built-in grows the vocabulary even though
the grammar is unchanged, so reading the freeze as syntax-only would be an
interpretation, not a rule. Spec 023 set the precedent of amending in the feature
PR (v1.0.0 to v1.1.0, MINOR for widening what may grow). `quote_each` is pure, so it
meets the new condition; the condition does not license functions that read the
host.

**Alternatives considered**: Recording the builtin as a justified deviation in
Complexity Tracking. Leaves the next builtin request with the same ambiguity.

## R10: Docs, fixtures, and release notes

**Decision**:

- `docs/runefile.md`: add `quote_each(list)` to the built-in table; add `raw` to
  the typed-parameters kinds; add a "Quoting parameters" subsection under
  Parameters with the safe forms, the inside-shell-quotes rule, `raw`, and the
  leading-dash note.
- `docs/how-to/parameters.md`: examples use `quote()` and `quote_each()`; a pitfall
  explains the warning.
- `docs/GRAMMAR.md`: `KindName` gains `"raw"`.
- `docs/diagnostics.md`: RUNE2017 row; the section heading lists it as a warning.
- `docs/mcp.md`: the schema mapping table gains the `raw` row.
- Every file that interpolates a string or path parameter unquoted in a shell body
  is rewritten to a safe form: `README.md`, `docs/getting-started.md`,
  `docs/troubleshooting.md`, `docs/how-to/{os-filtering,dependencies-and-hooks,settings-and-dotenv}.md`,
  and the Runefiles and READMEs under `docs/examples/`. For example,
  `@echo "hello {{name}}"` becomes `@echo "hello "{{quote(name)}}` so the output
  is unchanged. A page that shows the unsafe form on purpose fences it as
  ```` ```text ````, the existing convention for blocks that are not validated.
- New `test/docs/quoting_test.go` runs `rune analyze --json` on every example
  Runefile, every ```` ```rune ```` block of the self-contained pages, and the
  repository `Runefile`, and fails on any RUNE2017 (FR-021, SC-005).
- Fixtures: a new corpus fixture `testdata/corpus/raw-kind.rune` with its `.ast`
  golden (existing fixtures untouched, SC-003); a `raw` line in
  `testdata/fmt/typed-params.rune` and its `.fmt` golden. Tests that assert the
  diagnostics of a fixture with an unquoted parameter, such as the language
  server fixture `testdata/lsp/basic/Runefile`, are updated deliberately where
  they now include RUNE2017.
**Rationale**: SC-005 needs a test, not a review. The `"hello "{{quote(name)}}`
form keeps the documented output byte-identical while being safe.

## R11: Delivery in two PRs

**Decision**: Ship the feature as two PRs, in this order:

1. **Elements and `quote_each`** (US1, US2, US5; R1 to R5, R9): `Args`, binding,
   MCP `ListEngine`, forwarding, run-once and cache identity, the constitution
   amendment, and the `quote_each` docs. Title:
   `feat(params): quote_each() passes a variadic as separate quoted arguments`.
   The PR body also notes that `[cache]` tasks referencing a variadic run once
   after the upgrade (R5), and that dependencies such as `(lint "a b")` and
   `(lint "a" "b")` now run `lint` twice.
2. **Warning and `raw`** (US3, US4; R6 to R8, R10): RUNE2017, the `raw` kind, the
   docs and example rewrite, and the docs test. Title:
   `feat(analyzer): warn when a string or path parameter reaches a shell command unquoted (RUNE2017); add the raw kind to opt out`.

**Rationale**: `CHANGELOG.md` is generated by git-cliff from squash-commit
subjects, which are the PR titles (`docs/releasing.md`); a PR body never reaches
the release notes. One title cannot carry everything FR-022 lists, so FR-022 is
reworded to require the titles to cover it. The split also matches the stories:
PR 1 is useful alone, and PR 2's hint names `quote_each`, so it lands second.

**Alternatives considered**: One PR whose title names only the breaking change,
with the rest in docs. Leaves the new warning out of the changelog, although it
is the change most users will notice first.
