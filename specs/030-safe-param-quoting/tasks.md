---

description: "Task list for 030-safe-param-quoting"
---

# Tasks: Safe Quoting for Task Parameters

**Input**: Design documents from `specs/030-safe-param-quoting/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Included. Constitution Principle VI requires test-first, multi-layer
verification, and SC-002 and SC-004 name test matrices. Write each test task first
and confirm it fails before the implementation task it covers.

**Organization**: One phase per user story. Phases follow delivery order rather than
strict priority: PR 1 carries US1, US2, and US5; PR 2 carries US3 and US4 (research
R11). US5 (P3) comes before US3 and US4 (P2) because it ships in PR 1 and edits the
same engine code as Phase 2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: the user story the task serves (US1 to US5)

## Commands

Tests run in Docker only. On this host the `docker compose` plugin works and the
standalone `docker-compose` binary is missing; use whichever exists.

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

Check the golangci-lint pin in `.github/workflows/ci.yml` before linting.

---

## Phase 1: Setup

**Purpose**: Branch and baseline.

- [X] T001 Create branch `030-safe-param-quoting` from an up-to-date `main` (`git switch -c 030-safe-param-quoting`); the untracked `specs/030-safe-param-quoting/` directory moves with it
- [X] T002 Run the full suite in Docker (the `test` service in `docker-compose.yml`, first command under Commands) before any change and record any test that already fails, so later failures can be attributed

---

## Phase 2: Foundational (blocks US1, US2, US5)

**Purpose**: Carry variadic elements from binding to evaluation (research R1, R5).
Interpolated text and exit codes stay identical. `Lists` is filled, and only the
run-once key reads it: from here on, dependencies `(lint "a b")` and
`(lint "a" "b")` run `lint` twice (spec edge case "Same text, different
elements").

**Blocking**: US1, US2, and US5 cannot start until this phase is green.

### Tests

- [X] T003 [P] Write `internal/eval/args_test.go`: `Args.Key()` is the same for equal `Args` built in different map insertion orders; `Key()` differs for `{Values: {"p": "a b"}, Lists: {"p": ["a b"]}}` and `{Values: {"p": "a b"}, Lists: {"p": ["a", "b"]}}`; differs between `Lists: {"p": []}` and no `Lists` entry; and `{Values: {"a": "1;b=2"}}` differs from `{Values: {"a": "1", "b": "2"}}`
- [X] T004 [P] In `internal/runtime/scheduler/scheduler_test.go`, move `mockEngine` to the `eval.Args` signatures (T007) and add a test: two root `Invocation`s of one task whose `Values` match but whose `Lists` are `["a b"]` and `["a", "b"]` both execute, and two identical `Args` execute once

### Implementation

- [X] T005 Create `internal/eval/args.go` with `type Args struct { Values map[string]string; Lists map[string][]string }`. The type's doc comment states the data-model invariants verbatim: "For every key `k` in `Lists`, `Values[k] == strings.Join(Lists[k], " ")`", "`Lists` has at most one key", and "`Lists[k]` may be empty only for a `*` parameter". Add `func (a Args) Key() string` per research R5: `name=` + `strconv.Quote(value)` for each value in sorted name order, then `name[]=` + each element through `strconv.Quote`, space-separated, for each list in sorted name order
- [X] T006 In `internal/eval/eval.go`, add `Lists map[string][]string` to `Scope` (read only by `quote_each`) and `func (s *Scope) WithArgs(a Args) *Scope`, a shallow copy with `Params = a.Values` and `Lists = a.Lists`. Make `WithParams` set `Lists` to nil, so a default evaluated during binding never sees another invocation's elements
- [X] T007 In `internal/runtime/scheduler/scheduler.go`, change the `Engine` interface to `ResolveDep(curTask *ast.Task, cur eval.Args, dep *ast.DepCall) (*ast.Task, eval.Args, error)`, `Execute(task *ast.Task, args eval.Args) error`, and `ExecuteFailHook(task *ast.Task, args eval.Args, f Failure) error`; change `Invocation.Params` to `eval.Args`; thread `eval.Args` through `state.run`, `execute`, `runFailHooks`, `runFailHook`, and `runDep`; make `memoKey(namespace, name string, a eval.Args)` return `namespace + "::" + name + "(" + a.Key() + ")"`. The scheduler only passes `Args` through and asks for its key
- [X] T008 In `internal/runtime/scheduler/parallel.go`, change `runDepsParallel` to take `eval.Args`; update the other engine fakes in `failhook_test.go`, `parallel_test.go`, and `leak_test.go` that the compiler reports
- [X] T009 In `internal/cli/args.go`, make `bindParams` return `(eval.Args, error)`. For `ast.ParamVariadicPlus` and `ast.ParamVariadicStar`, set `Lists[p.Name]` to a copy of `pos[i:]` (a non-nil empty slice for `*` with no elements) and `Values[p.Name]` to `strings.Join(pos[i:], " ")`. Defaults still evaluate through `scope.WithParams(copyMap(args.Values))`. Error texts and per-element checks stay exactly as today
- [X] T010 In `internal/cli/args.go`, change `bindNamedParams` to `bindNamedParams(t *ast.Task, named map[string]string, lists map[string][]string, scope *eval.Scope) (eval.Args, error)`. For a variadic: elements come from `lists[p.Name]` when present; otherwise a provided value is one element, and an omitted `*` is zero elements. `Values[p.Name]` is the join of the elements. Keep today's `!provided || val == ""` check on the joined value for `+`, so the error cases do not change. Update the doc comment, which says variadic values arrive already space-joined
- [X] T011 In `internal/cli/run.go`, change `engine.ResolveDep`, `Execute`, `ExecuteFailHook`, `runBody`, `runBodyTo`, `cacheSpec`, and `collectVars` to take `eval.Args`, and evaluate with `e.scope.WithArgs(args)` where they now call `e.scope.WithParams(params)`. The root loop near `bindParams(t, r.args, e.scope)` builds `scheduler.Invocation{Task: t, Params: args}`. `ResolveDep` keeps today's logic in this task (evaluate each argument, call `bindParams`); forwarding is T031. Fix every other caller in `internal/cli` that the compiler reports
- [X] T012 In `internal/cli/mcp.go`, make `mcpAdapter.Call` pass `nil` lists to `bindNamedParams` and put the returned `eval.Args` in the `scheduler.Invocation`
- [X] T013 Run the full suite and the race suite in Docker (the `test` service in `docker-compose.yml`). Every test that passed in T002 passes; only engine fakes changed. T003 and T004 pass

**Checkpoint**: Elements reach `eval.Scope.Lists` on the CLI, MCP, and dependency paths. Output is unchanged.

---

## Phase 3: User Story 1 - Pass a variadic as separate, quoted arguments (Priority: P1, MVP)

**Goal**: `{{quote_each(names)}}` quotes each element as its own shell word, from the
command line and from MCP arrays.

**Independent Test**: With tasks `c` and `n` from spec Story 1, every row of the
`quote_each(x)` table in [contracts/runefile-surface.md](contracts/runefile-surface.md)
produces the listed stdout.

### Tests for User Story 1

- [X] T014 [P] [US1] Write `internal/eval/quote_each_test.go` from the data-model "`quote_each` evaluation" table: `Lists["names"] = ["yq", "x; echo pwned", "tofu"]` gives `'yq' 'x; echo pwned' 'tofu'`; `["a b", "", "it's"]` gives `'a b' '' 'it'\''s'`; `[]` gives empty text; elements with a newline, `$(id)`, a backtick, `;`, and a leading `-` each come out as one `quote()` word (SC-002); `quote_each(name)` for a scalar equals `quote(name)`; `quote_each("a b")` is `'a b'`; `quote_each(names + "")` is one word; a name present in `Params` but absent from `Lists` falls back to `quote()`; zero and two arguments give the error `quote()` gives for the same count, with the function name `quote_each`
- [X] T015 [P] [US1] Write `internal/cli/elements_test.go` (binding part): `bindParams` for `*names` with `[]`, `["a b"]`, and `["a", "b"]` yields the data-model `Args` (check the join invariant on each); `+names` with no arguments returns the usage error `task %q expects at least one %q argument`; `bindNamedParams` binds `lists` elements, treats a plain value with `nil` lists as one element, binds zero elements for an omitted `*`, and still rejects an empty `+`; per-element `enum` checks still run on the CLI path
- [X] T016 [P] [US1] In `mcpserver/handler_test.go`, test `decodeArgs` against the "Server behavior per tool call" table in [contracts/mcp.md](contracts/mcp.md) (four rows), and that arrays for a non-variadic or unknown parameter never appear in `lists`. With a fake engine that records which method ran: an engine implementing `ListEngine` gets `CallLists` with the decoded lists, and a plain `Engine` gets `Call` with the same `args`. Update the existing `decodeArgs` call near line 139 to the new return values
- [X] T017 [P] [US1] Create `test/integration/quoting_test.go` (PR 1 part) with tasks `c` and `n` from spec Story 1. Assert every CLI and MCP row of the `quote_each(x)` table in [contracts/runefile-surface.md](contracts/runefile-surface.md); drive MCP over stdio with the helper `typed_schema_mcp_test.go` uses. Add the Story 1 `+names` case (`rune c yq` prints `[yq]`) and the `set shell := ["bash", "-eu"]` case, skipped on Windows and when `bash` is missing, as `shell_override_test.go` does

### Implementation for User Story 1

Run T023 (the constitution amendment) before T018: Principle III requires the
amendment before the built-in library grows.

- [X] T018 [US1] In `internal/eval/builtins.go`, register `"quote_each": unary("quote_each", shellQuote)` next to `"quote"` in `builtins()`, so `IsBuiltin`, `BuiltinNames`, the arity error, and the fallback match `quote()`. In `callBuiltin`, before evaluating arguments: when `fc.Name == "quote_each"`, `len(fc.Args) == 1`, `fc.Args[0]` is an `*ast.VarRef`, and `elems, ok := e.scope.Lists[ref.Name]` holds, return each element through `shellQuote` joined by single spaces (empty text for no elements). Use map membership, not length, so an empty variadic is not mistaken for a scalar. A short comment says why: the boundaries exist only in `Lists`, since the string value is already joined
- [X] T019 [P] [US1] In `internal/language/builtin.go`, add after `quote`: `{Name: "quote_each", Kind: BuiltinFunction, Signature: "quote_each(list) -> string", Documentation: "Each element of a variadic parameter quoted as a separate shell argument."}`; `TestRegistryMatchesEvalBuiltins` in `builtin_sync_test.go` must pass
- [X] T020 [P] [US1] In `mcpserver/server.go`, add the exported `ListEngine` interface with the doc comment from [contracts/mcp.md](contracts/mcp.md) verbatim. `Engine` does not change
- [X] T021 [US1] In `mcpserver/handler.go`, change `decodeArgs` to return `(map[string]string, map[string][]string, error)`: for a known parameter with `Variadic` set, an array gives its elements (after the per-element checks) and a scalar gives a one-element list. Rewrite the doc comment, which says the join destroys element boundaries. In the tool handler, call `CallLists` when `s.engine` implements `ListEngine`, otherwise `Call(ctx, taskName, args)` exactly as today
- [X] T022 [US1] In `internal/cli/mcp.go`, add `func (a *mcpAdapter) CallLists(ctx context.Context, name string, args map[string]string, lists map[string][]string) (mcpserver.Result, error)` holding today's body of `Call`, passing `lists` to `bindNamedParams`. `Call` becomes `return a.CallLists(ctx, name, args, nil)`. Add `var _ mcpserver.ListEngine = (*mcpAdapter)(nil)`
- [X] T023 [US1] Amend `.specify/memory/constitution.md` from v1.1.0 to v1.2.0 (MINOR): add the clause from research R9 verbatim to Principle III, write a new Sync Impact Report (`Version: 1.1.0 → 1.2.0`, the added clause, templates checked with no change needed), and set **Last Amended** to the date of the change
- [X] T024 [US1] In `docs/runefile.md`, add `quote_each(list)` to the built-in function table next to `quote(s)` with an example such as `prettier --write {{quote_each(files)}}`, and state that an empty list expands to nothing. In `docs/how-to/parameters.md`, make the variadic example use `{{quote_each(...)}}`. New ```` ```rune ```` blocks must pass `TestRuneCodeBlocksValidate`

**Checkpoint**: Quickstart scenarios A, B, and H (the `tools/call` part) pass. US1 works on its own.

---

## Phase 4: User Story 2 - Existing Runefiles produce the same commands (Priority: P1)

**Goal**: Prove FR-010 and FR-011: no Runefile that avoids `quote_each` and `raw`
changes its interpolated text.

**Independent Test**: The compatibility corpus and CLI goldens pass with no edits, and
tasks `a` and `b` from the issue repro print v0.6.0's output.

### Tests for User Story 2

- [X] T025 [P] [US2] In `internal/cli/elements_test.go`, bind `yq`, `x; echo pwned`, `tofu` through `bindParams`, interpolate through `eval.New(scope.WithArgs(args)).Interpolate`, and compare with the "Unchanged forms" table in [contracts/runefile-surface.md](contracts/runefile-surface.md) byte for byte. Also cover `{{"x" + names}}`, a conditional comparing `names == "a b"`, and a scalar parameter
- [X] T026 [P] [US2] In `test/integration/quoting_test.go`, run tasks `a` and `b` from the spec Overview with `yq 'x; echo pwned' tofu` and assert the v0.6.0 output shown there; add `commit +msg:` with the body `@printf '%s\n' {{quote(msg)}}` and assert `rune commit fix the build` prints the single line `fix the build` (spec Story 2 scenario 3, with `printf` standing in for `git`)

### Verification for User Story 2

- [X] T027 [US2] Run `go test ./test/corpus ./internal/formatter ./internal/cli` and the full suite in Docker with no edits to `testdata/corpus/` or any golden file (SC-003). A failure here means the code changed meaning: fix the code, never the golden

**Checkpoint**: Quickstart scenario C passes. US1 and US2 hold together.

---

## Phase 5: User Story 5 - Forwarding a variadic keeps its elements (Priority: P3, ships in PR 1)

**Goal**: `(test pkgs)` passes the caller's elements into the target's variadic, and
both run-once and the cache see element boundaries (FR-007, FR-008, FR-009).

**Independent Test**: Every row of the "Dependency forwarding" and "Run-once and cache
identity" sections of [contracts/runefile-surface.md](contracts/runefile-surface.md)
holds.

### Tests for User Story 5

- [X] T028 [P] [US5] In `internal/cli/elements_test.go`, test `engine.ResolveDep` against the six rows of the "Dependency forwarding" table, plus a post-hook `&& (test pkgs)` and a failure hook `|| (fix pkgs)` variant. For the empty-into-`+` row, assert no error, `Lists["pkgs"] == [""]`, and `Values["pkgs"] == ""`; for `(test "x" pkgs)` with no elements, assert `Lists["pkgs"] == ["x"]` and `Values["pkgs"] == "x "` (v0.6.0's text)
- [X] T029 [US5] In `internal/cli/elements_test.go`, test identity: `cacheSpec` for a `[cache]` task with `*names` referenced in its body has a `names[]` entry in `Vars`, and the entry differs between elements `["a b"]` and `["a", "b"]`; a `[cache]` task with no variadic has the same `Vars` as before (no `[]` key); through `scheduler.Run`, dependencies `(lint "a b")` and `(lint "a" "b")` run `lint` twice and `(lint "a")` twice runs it once
- [X] T030 [P] [US5] In `test/integration/quoting_test.go`, use the quickstart scratch Runefile: `rune ci 'a b' c` prints `2` then `done`; `rune plus` prints an empty line, then `after`, and exits 0

### Implementation for User Story 5

- [X] T031 [US5] In `internal/cli/run.go` `ResolveDep`, find the target's variadic index `v` (the parameter whose `Kind` is `ast.ParamVariadicPlus` or `ast.ParamVariadicStar`, or none). Evaluate with `e.scope.WithArgs(cur)`. For each argument at index `v` or later: when it is an `*ast.VarRef` and `elems, ok := cur.Lists[ref.Name]` holds, append all of `elems`; otherwise append the evaluated value. Arguments before `v`, and all arguments when there is no variadic, evaluate as today. When the spread leaves a `+` variadic with no elements, append one empty element. Then call `bindParams(target, pos, e.scope)`, and set the variadic's `Values` entry to v0.6.0's text: the written arguments from `v` on, each evaluated to its string, joined by single spaces. Short comments say why spreading stops at the variadic slot (scalar binding and arity stay as today) and why the text and the empty `+` case follow v0.6.0 (no opt-in, so no break; research R4)
- [X] T032 [US5] In `internal/cli/run.go` `collectVars`, for each referenced name with `elems, ok := args.Lists[name]`, add `out[name+"[]"]` set to the elements through `strconv.Quote`, joined by single spaces. A short comment notes the one-time re-run of `[cache]` tasks that reference a variadic (research R5)
- [X] T033 [US5] In `docs/runefile.md`, document forwarding where dependency arguments are described: a bare variadic reference fills the target's variadic element by element, fills a scalar with the joined text, and the text `{{p}}` gives is the same as before

**Checkpoint**: Quickstart scenario D passes. PR 1 scope is complete.

---

## Phase 6: PR 1 delivery

**Purpose**: Gate and open PR 1 (US1, US2, US5).

- [X] T034 Run the full suite, the race suite, and lint in Docker (all three commands under Commands, lint config in `.golangci.yml`); all green
- [X] T035 Build a binary in Docker and walk scenarios A, B, C, D, and the `tools/call` part of H of `specs/030-safe-param-quoting/quickstart.md` against it
- [X] T036 Check that no added line uses an em dash: `git diff main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` prints nothing

### Review fixes (after T036)

These supersede the empty-into-`+` parts of T028, T030, and T031.

- [X] T069 [US5] In `internal/cli/elements_test.go`, change the empty-into-`+` row to expect `Lists["pkgs"] == []` and `Values["pkgs"] == ""`; add a row where `test +pkgs:path:` receives `["./a", ""]` and is accepted with `Values["pkgs"] == "./a "`; add `TestResolveDepRejectsFailingElements` (enum `["a", "c"]` reports `invalid value "c"`). In `test/integration/quoting_test.go`, make `need` print the element count and expect `0` then `after` from `rune plus`
- [X] T070 [US5] In `internal/cli/run.go` `ResolveDep`, bind the spread elements; when that fails, bind the written arguments' values as v0.6.0 did and, if that succeeds, keep the spread elements (research R4). Remove the appended empty element. Set the variadic's `Values` entry to v0.6.0's text and its `Lists` entry to the spread elements
- [X] T071 [US1] Over MCP an empty string for a variadic is no elements: `decodeArgs` in `mcpserver/handler.go` lists it as `[]`, and `namedElements` in `internal/cli/args.go` binds none for an empty value. Tests: a `TestDecodeArgsLists` row, a `TestBindNamedParamsElements` row, and a `TestQuoteEach_MCP` row (`{"names": ""}` prints `0`)
- [X] T072 [US1] `bindNamedParams` checks each element it receives in `lists` against the parameter's constraint, as it checks scalars. Test: `deploy +hosts:enum("web")` with lists `["prod-db"]` is rejected
- [X] T073 [P] Add `eval.ElementsKey` in `internal/eval/args.go`, used by `Args.Key` and by `collectVars` in `internal/cli/run.go`, so run-once and the cache encode elements the same way; `TestIdentitySeesElements` asserts the `names[]` entry equals it
- [X] T074 [P] Remove `Scope.WithParams` from `internal/eval/eval.go`; default evaluation in `internal/cli/args.go` uses `scope.WithArgs(eval.Args{Values: copyMap(params)})`, which carries no elements
- [X] T075 Update `docs/runefile.md` (forwarding paragraph), spec FR-006 to FR-008 and Story 5, research R3 and R4, the data model, both contracts, the plan, and quickstart D to match T069 to T072

- [ ] T037 When the maintainer asks for the PR: run `git config user.name` and `gh auth status`, switch with `gh auth switch --user <name>` if the active account differs, and confirm with `gh auth status`. Open PR 1 titled exactly `feat(params): quote_each() passes a variadic as separate quoted arguments`. The body says `[cache]` tasks that reference a variadic run once after the upgrade, that dependencies `(lint "a b")` and `(lint "a" "b")` now run `lint` twice, and links issue #35 without closing it. No Co-Authored-By trailer and no tool attribution. Wait for every check to finish green before reporting the PR as done

---

## Phase 7: User Story 3 - The analyzer flags unquoted parameters (Priority: P2, PR 2)

**Goal**: RUNE2017 warns on each interpolation that puts a string or path parameter
into a shell body unquoted (FR-012 to FR-015).

**Independent Test**: The 27-row fixture in
[contracts/diagnostic-rune2017.md](contracts/diagnostic-rune2017.md) produces exactly
the expected warnings (SC-004).

Start PR 2 on a new branch from `main` after PR 1 merges, for example
`030-safe-param-quoting-analyzer`.

### Tests for User Story 3

- [ ] T038 [P] [US3] Write `internal/analyzer/quoting_test.go`: one subtest per row 1 to 27 of [contracts/diagnostic-rune2017.md](contracts/diagnostic-rune2017.md), asserting the exact list of RUNE2017 findings (parameter, count, order). Rows 22 to 24 each use their own file with the listed `set shell`. Assert the full message text for row 1 (scalar) and row 2 (`*names:raw`), a `+names` variant that names `+names:raw`, severity warning, and that the span covers the whole `{{ … }}` interpolation. Leave `flags:raw` out of the shared declarations and skip row 16 here: `raw` is a RUNE2012 error until US4, and T053 adds the row
- [ ] T039 [P] [US3] In `internal/analysis/service_test.go`, assert that `Service.Analyze` reports RUNE2017 as a warning, that a task in an imported module is reported at the imported file's path, and that `analyzer.Analyze` (the run path) never returns RUNE2017
- [ ] T040 [P] [US3] In `test/integration/quoting_test.go` (PR 2 part), with the quickstart scratch Runefile minus the `test` task: `rune analyze` reports RUNE2017 for task `a` only and exits 0; `rune analyze --json` has an entry with `"code": "RUNE2017"` and `"severity": "warning"`; `rune a x` prints no warning and exits 0 (Story 3 scenario 10)

### Implementation for User Story 3

- [ ] T041 [P] [US3] In `internal/diag/codes.go`, add `CodeUnquotedParam = "RUNE2017" // warning: string or path parameter interpolated unquoted into a shell body (spec 030)` after `CodeKindShadowsTask`; update `internal/analyzer/codes_test.go` if it lists every code
- [ ] T042 [P] [US3] In `internal/runtime/executor.go`, export `isKnownShell` as `IsKnownShell`, with a doc comment saying the analyzer uses it to decide shell bodies statically (spec 030 R7); update the call near line 79 and the package's tests
- [ ] T043 [US3] Create `internal/analyzer/quoting.go` with `func CheckQuoting(f *ast.File) diag.List`. Shell bodies follow the data-model "Shell body" table: a non-empty `[script("…")]` is a shell body when any whitespace-separated field passes `runtime.IsKnownShell`; executor `""` or `sh` with a `shell` setting is a shell body when any string-literal element passes `IsKnownShell` (read the setting as `settings_check.go` does; do not evaluate non-literal elements); executor `""` or `sh` with no `shell` setting is always a shell body; `python`, `node`, `agent`, and custom executors never are. Warned parameters have no constraint, or kind `ast.KindString` or `ast.KindPath`. For each body line, use `extractInterps` and `parseFragment`, skip fragments with parse errors, and walk the expression per the research R6 table: report a `VarRef` to a warned parameter; walk both sides of a `Binary`; stop at a `FuncCall` named `quote` or `quote_each`; walk the arguments of any other `FuncCall`; for a `Conditional`, skip each branch's comparison operands (`Left`, `Right`) and walk each `Result` and `Else`. Report each parameter at most once per interpolation, in order of first appearance, at the interpolation span, with the message from [contracts/diagnostic-rune2017.md](contracts/diagnostic-rune2017.md) (the sigil as declared for a variadic)
- [ ] T044 [US3] In `internal/analysis/service.go`, append `analyzer.CheckQuoting(file)...` right after `analyzer.CheckDocumentation(file)...`. Do not call it on the run path
- [ ] T045 [US3] Run the full suite. Where a test asserts the exact warnings of a Runefile that now gets RUNE2017, update the expectation to include it; never loosen an exact assertion to a substring check. `testdata/lsp/basic/Runefile` stays unchanged
- [ ] T046 [P] [US3] In `docs/diagnostics.md`, add the RUNE2017 row exactly as the "Docs" section of [contracts/diagnostic-rune2017.md](contracts/diagnostic-rune2017.md) gives it, and list RUNE2017 among the warnings in the RUNE2xxx heading
- [ ] T047 [US3] In `docs/runefile.md`, add a "Quoting parameters" subsection under Parameters: `{{quote(p)}}` and `{{quote_each(p)}}` written outside any shell quotes are the safe forms; `"{{p}}"` and `'{{p}}'` are still unsafe; RUNE2017 flags the rest; quoting does not stop a value starting with `-` being read as an option, so pass `--` where the command supports it. In `docs/how-to/parameters.md`, add a pitfall that explains the warning and its fix

**Checkpoint**: Quickstart scenarios E and G pass.

---

## Phase 8: User Story 4 - The `raw` kind (Priority: P2, PR 2)

**Goal**: `raw` declares intended word splitting, silences RUNE2017, and tells
agents about it (FR-016 to FR-019).

**Independent Test**: The "`raw` kind" section of
[contracts/runefile-surface.md](contracts/runefile-surface.md) and the schema rows of
[contracts/mcp.md](contracts/mcp.md) hold.

### Tests for User Story 4

- [ ] T048 [P] [US4] In `internal/parser/typed_params_test.go`, parse `name:raw`, `name:raw="d"`, `*name:raw`, and `+name:raw` with no diagnostics and kind `ast.KindRaw`; `Constraint.Check` accepts the empty string and arbitrary text
- [ ] T049 [P] [US4] Create `testdata/corpus/raw-kind.rune` with the four forms from T048 in tasks that use them, and generate `testdata/corpus/raw-kind.ast` with `go test ./test/corpus -update` in Docker. Leave existing corpus files untouched (SC-003)
- [ ] T050 [P] [US4] Add `raw` parameters (`flags:raw="-v -race"` and `*args:raw`) to `testdata/fmt/typed-params.rune` and regenerate `testdata/fmt/typed-params.fmt` with `go test ./internal/formatter -run TestFmtGolden -update` in Docker; formatting the result again changes nothing
- [ ] T051 [P] [US4] In `internal/language/hover_test.go`, hover on `flags` in `test flags:raw="-v -race":` shows `flags:raw` and the line "Split into shell words without quoting."
- [ ] T052 [P] [US4] In `mcpserver/server_test.go` `TestInputSchemaTyped`, add the four rows of the "Schema mapping delta" table in [contracts/mcp.md](contracts/mcp.md), and assert that a task with no `raw` parameter gets the same schema as before. Check that the loops over `ast.SupportedConstraintKinds` near lines 227 and 258 handle `raw`
- [ ] T053 [P] [US4] In `internal/analyzer/quoting_test.go`, add row 16 (`go test {{flags}}` with `flags:raw`) and a `*args:raw` variadic: no RUNE2017. A task named `raw` next to a parameter `x:raw` still gets the existing RUNE2016 warning
- [ ] T054 [P] [US4] In `test/integration/quoting_test.go`, `rune test` with `test flags:raw="-v -race":` and body `@printf "[%s]\n" {{flags}}` prints `[-v]` then `[-race]`; MCP `tools/list` shows `test.flags` as type `string` with description "Split into shell words without quoting."

### Implementation for User Story 4

- [ ] T055 [US4] In `internal/ast/ast.go`, add `KindRaw` to `ConstraintKind` after `KindEnum`, so existing values keep their numbers. Set `SupportedConstraintKinds` to `{"string", "number", "boolean", "path", "raw", "enum"}`, the grammar's order. Map `"raw"` in `ConstraintKindFromName`, and make `Constraint.Check` accept every value for `KindRaw`, as for `KindString`. Update any test that asserts the supported-kinds hint text from `internal/parser/task.go` or the RUNE2012 message in `internal/analyzer/analyzer.go`
- [ ] T056 [US4] In `internal/language/hover.go` `varHover`, when the parameter's constraint kind is `ast.KindRaw`, add the line "Split into shell words without quoting." after any `[param-doc]` text
- [ ] T057 [US4] In `mcpserver/server.go` `inputSchema`, for `p.Kind == "raw"` set `description` to the author text plus the sentence "Split into shell words without quoting.", following the rule in [contracts/mcp.md](contracts/mcp.md): add a `.` when the author text does not end in `.`, `!`, or `?`, then a space and the sentence; the sentence alone when there is no author text. For a variadic it goes on the array property. The author text is already capped by `capAgentText` in `internal/cli/mcp.go`
- [ ] T058 [US4] Confirm `internal/analyzer/quoting.go` warns only for no constraint, `KindString`, and `KindPath`, so `KindRaw` is excluded; T053 passes
- [ ] T059 [US4] Docs: in `docs/GRAMMAR.md`, set `KindName = "string" | "number" | "boolean" | "path" | "raw" | "enum" ;`; in `docs/runefile.md`, add `raw` to the typed-parameters kinds; in `docs/mcp.md`, add the `raw` row to the schema mapping table; in `docs/how-to/parameters.md`, add the `test flags:raw="-v -race":` example

**Checkpoint**: Quickstart scenarios F and H (the schema part) pass.

---

## Phase 9: Polish and PR 2 delivery

**Purpose**: Make every doc and example warning-free (FR-020, FR-021, SC-005) and
open PR 2.

- [ ] T060 Write `test/docs/quoting_test.go`: run `rune analyze --json` on the `Runefile` of each directory from `exampleDirs`, on every ```` ```rune ```` block of `selfContainedPages` (written to a temp `Runefile`, as `TestRuneCodeBlocksValidate` does), and on the repository's root `Runefile`; fail with the file and line of every RUNE2017. Run it and expect failures before T061 to T064
- [ ] T061 [P] Rewrite unsafe interpolations in `README.md`, `docs/getting-started.md`, and `docs/troubleshooting.md` to forms that keep the output: `@echo "hello {{name}}"` becomes `@echo "hello "{{quote(name)}}`, variadics use `quote_each`, and intended word splitting declares `:raw`. A block that shows the unsafe form on purpose becomes a ```` ```text ```` fence
- [ ] T062 [P] Apply the same rewrite to `docs/how-to/os-filtering.md`, `docs/how-to/dependencies-and-hooks.md`, and `docs/how-to/settings-and-dotenv.md`
- [ ] T063 [P] Apply the same rewrite to every `docs/examples/*/Runefile` and `docs/examples/*/README.md`; `TestExamplesRun` output must not change
- [ ] T064 [P] Apply the same rewrite to the repository's root `./Runefile`
- [ ] T065 Run the docs suite (`go test ./test/docs`), the full suite, the race suite, and lint in Docker; all green, including T060 (SC-005)
- [ ] T066 Walk scenarios E, F, G, H, and I of `specs/030-safe-param-quoting/quickstart.md` against a binary built in Docker
- [ ] T067 Check that no added line uses an em dash: `git diff main -- . ':!specs' | grep '^+' | grep -P '\x{2014}'` prints nothing
- [ ] T068 When the maintainer asks for the PR: run the same `gh` account checks as T037, then open PR 2 (title from research R11 in `specs/030-safe-param-quoting/research.md`) titled exactly `feat(analyzer): warn when a string or path parameter reaches a shell command unquoted (RUNE2017); add the raw kind to opt out`, with `Closes #35` in the body. No Co-Authored-By trailer and no tool attribution. Wait for every check to finish green before reporting the PR as done

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: after Setup. Blocks US1, US2, and US5.
- **US1 (Phase 3)**: after Phase 2.
- **US2 (Phase 4)**: after Phase 2. T025 uses `WithArgs`; T026 and T027 also need US1 merged in, to show `quote_each` changed nothing else.
- **US5 (Phase 5)**: after Phase 2. Its tests use `quote_each` (T018) to show elements arrived.
- **PR 1 delivery (Phase 6)**: after Phases 3 to 5.
- **US3 (Phase 7)**: after PR 1 merges. Fixture rows 6 and 8 need `quote_each` registered, or the analyzer's `unknown function` error fires.
- **US4 (Phase 8)**: after T043, since T053 and T058 check the exclusion inside `CheckQuoting`. T048 to T052 and T055 to T057 do not touch the analyzer and can start with Phase 7.
- **PR 2 delivery (Phase 9)**: after Phases 7 and 8. The rewrites (T061 to T064) need `raw` for lines that split on purpose.

### Within each phase

- Tests are written first and fail before the implementation task.
- `eval` (T005, T006) before the scheduler (T007, T008) before `cli` (T009 to T012).
- `mcpserver` interface (T020) before the handler (T021) before the adapter (T022).
- `diag` and `runtime` exports (T041, T042) before `CheckQuoting` (T043) before wiring (T044).

### Parallel opportunities

- Phase 2: T003 and T004.
- Phase 3: T014, T015, T016, T017 (four test files); then T019 and T020 beside T018.
- Phase 4: T025 and T026.
- Phase 5: T028 and T030.
- Phase 7: T038, T039, T040; then T041 and T042; T046 beside T043.
- Phase 8: T048 to T054 (seven different files).
- Phase 9: T061 to T064 (disjoint file sets).

---

## Parallel Example: User Story 1

```text
Task: "T014 Write internal/eval/quote_each_test.go from the data-model quote_each table"
Task: "T015 Write internal/cli/elements_test.go binding cases"
Task: "T016 Extend mcpserver/handler_test.go for decodeArgs lists and ListEngine dispatch"
Task: "T017 Create test/integration/quoting_test.go CLI and MCP rows"

# After the tests fail as expected:
Task: "T019 Add quote_each to internal/language/builtin.go"
Task: "T020 Add ListEngine to mcpserver/server.go"
```

## Parallel Example: User Story 4

```text
Task: "T048 Parser cases for the four raw forms"
Task: "T049 testdata/corpus/raw-kind.rune and .ast"
Task: "T050 testdata/fmt/typed-params raw lines and .fmt"
Task: "T051 Hover test for flags:raw"
Task: "T052 inputSchema rows for raw"
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phase 1, then Phase 2 (the plumbing that keeps elements).
2. Phase 3: `quote_each` from the command line and MCP.
3. Stop and validate with quickstart scenarios A, B, and H. This closes the injection
   path for any author who switches to `quote_each`.

### Incremental delivery

1. **PR 1**: MVP plus US2 (proof that nothing else changed) and US5 (forwarding), then
   Phase 6. The changelog entry is the PR 1 title.
2. **PR 2**: US3 (the warning), US4 (the opt-out), and Phase 9 (docs and examples
   warning-free). The changelog entry is the PR 2 title.

Each PR passes the full Docker suite and lint on its own, and CI must be green
before either is reported done.

---

## Notes

- `[P]` tasks touch different files and depend on no incomplete task.
- Task IDs follow execution order; phases follow PR order, which is why US5 precedes
  US3 and US4.
- Comments in new code explain why, not what, and match the surrounding style.
- No added text uses an em dash (T036, T067).
- Commit only when the maintainer asks, and never with a Co-Authored-By trailer.
