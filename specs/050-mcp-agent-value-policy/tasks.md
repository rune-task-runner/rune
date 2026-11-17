---

description: "Task list for spec 050: restrict the argument values an agent may pass to a task"
---

# Tasks: Restrict the Argument Values an Agent May Pass to a Task

**Input**: Design documents from `/specs/050-mcp-agent-value-policy/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and FR-034 ask for them. A test is written and
seen failing before the change that makes it pass. Where a test already passes before the
change, the task says so and says what it guards.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US8)

## Conventions for every task

- Tests run in Docker only:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- Every function added or changed gets a unit test of its own, with a case for each branch.
- "Nothing ran" is always checked from the outside: no marker file. A result text alone is not
  proof.
- The exact texts are in [contracts/mcp-tools.md](contracts/mcp-tools.md) and
  [contracts/agent-allow-attribute.md](contracts/agent-allow-attribute.md). Tests compare whole
  strings, not fragments, unless the task says otherwise.
- A Runefile with no `[agent-allow]` must give the same tools and results as before, byte for
  byte. No existing test is edited to make it pass.
- Goldens are regenerated with `-update`, never edited by hand.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `050-mcp-agent-value-policy` and `specs/050-mcp-agent-value-policy/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: `[agent-allow]` becomes a known attribute everywhere in the language surface (FR-001,
FR-004, FR-006, research R1, R2, R12). It has no effect on any run yet.

### Tests first

- [X] T002 [P] Create `internal/parser/agent_allow_test.go`. `TestParseAgentAllow`: `[agent-allow(env="staging")]` above `deploy env:enum("staging","prod"):` gives one attribute of kind `ast.AttrAgentAllow` with `Allow` holding one predicate `{Name: "env", Value: "staging"}` whose `NameSp` covers `env` and `ValueSp` covers `"staging"`; `[agent-allow(env = "staging", region="eu-west-1")]` gives two predicates in source order; `[confirm, agent-allow(env="staging")]` gives both attributes; a value with an escape (`"a\"b"`) is decoded; a parameter name with a hyphen (`dry-run="no"`) is one name. `TestParseAgentAllowErrors`: each of `[agent-allow]`, `[agent-allow()]`, `[agent-allow(env)]`, `[agent-allow(env=)]`, `[agent-allow(env=prod)]`, `[agent-allow(env="a",)]`, `[agent-allow("env"="a")]` gives at least one error diagnostic with code `RUNE1001`, and none whose message contains `unknown attribute`. Add the seed `"[agent-allow(env=\"staging\", region=\"eu\")]\ndeploy env region:\n    echo {{env}}\n"` to `FuzzParser` in `internal/parser/fuzz_test.go`. Run it and see it fail to compile.
- [X] T003 [P] Create `internal/ast/agent_allow_test.go`. `TestTaskAgentPolicy`: a task with no attribute gives nil; with one `agent-allow` attribute gives its predicates; with two gives the first one's. `TestDumpAgentAllow`: `Dump` of a file with `[agent-allow(env="staging", region="eu-west-1")]` contains the line `    Attr agent-allow(env="staging", region="eu-west-1")`. `TestKnownAttributesHasAgentAllow`: `KnownAttributes` contains `agent-allow`. Run it and see it fail to compile.
- [X] T004 [P] In `internal/formatter/formatter_test.go`, add `TestFormatAgentAllow`: the source `[agent-allow( env = "staging" ,region="eu-west-1" )]\ndeploy env region:\n    @echo hi\n` formats to a file whose attribute line is `[agent-allow(env="staging", region="eu-west-1")]`; `[confirm, agent-allow(env="staging")]` formats to two lines, `[confirm]` then `[agent-allow(env="staging")]`; formatting the output again gives the same text; and a value with a quote and a backslash (`"a\"b\\c"`) parses to the same predicate value after formatting. Create `testdata/fmt/agent-allow.rune` with the same two tasks written with irregular spacing; its golden is generated in T008. Run it and see it fail.

### Implementation

- [X] T005 In `internal/ast/ast.go`, add the constant `AttrAgentAllow = "agent-allow"` with the comment `// limits the values an agent may run the task with (spec 050)`, add it to `KnownAttributes` after `AttrReadonly`, add `type AllowPredicate struct { Name, Value string; NameSp, ValueSp token.Span }`, add the field `Allow []*AllowPredicate` to `Attribute` with a comment, and add `func (t *Task) AgentPolicy() []*AllowPredicate` returning `Allow` of the first attribute of that kind, or nil. Update the `Attribute` doc comment to mention the predicates.
- [X] T006 In `internal/ast/dump.go`, add a case to `dumpAttr` for `AttrAgentAllow` that writes `    Attr agent-allow(name="value", ...)` with each value through `strconv.Quote` and `, ` between predicates. T003 passes.
- [X] T007 In `internal/parser/attribute.go`, add `case ast.AttrAgentAllow: p.parseAgentAllowArgs(a)` to `parseAttrItem` and the function `parseAgentAllowArgs`: `expect(LPAREN, "to open agent-allow(...)")`, then a loop of `expect(IDENT, "parameter name in agent-allow(...)")`, `expect(EQUALS, "after the parameter name in agent-allow(...)")`, `expect(STRING, "as the value agents may pass")`, appending an `AllowPredicate` with the two token spans, continuing while a `COMMA` is accepted and stopping at the first failed `expect`; then `expect(RPAREN, "to close agent-allow(...)")` and extend `a.Sp` to it. T002 and `TestParserKnowsAllASTAttributes` pass.
- [X] T008 In `internal/formatter/formatter.go`, add a case to `formatAttr` for `ast.AttrAgentAllow` that writes `[agent-allow(name="value", ...)]` with each value through `strconv.Quote`. Generate `testdata/fmt/agent-allow.fmt` with `go test ./internal/formatter -update` in Docker and read it. T004 passes.
- [X] T009 [P] In `internal/language/builtin.go`, add to `builtinAttributes` after `readonly`: name `agent-allow`, signature `[agent-allow(param="value")]`, documentation `Limit the values an agent may run the task with: each named string or enum parameter must have exactly this value. People on the command line are not limited.` Run `go test ./internal/language` and see `TestRegistryMatchesASTAttributes` pass.
- [X] T010 [P] Create `testdata/corpus/agent-allow.rune` with a leading comment naming spec 050 and three tasks: `[agent-allow(env="staging")] deploy env:enum("staging","prod")`, `[agent-allow(env="staging", region="eu-west-1")] rollout env:enum("staging","prod") region="eu-west-1"`, and `[confirm, agent-allow(channel="beta")] tag channel`. Generate `testdata/corpus/agent-allow.ast` with `go test ./test/corpus -update` in Docker and check that the three `Attr agent-allow(...)` lines are in it.
- [X] T011 [P] Add `agent-allow` to the attribute alternatives in `editors/vscode/syntaxes/runefile.tmLanguage.json`, after `readonly`. Confirm the file still parses with `python3 -m json.tool`.

**Checkpoint**: `[agent-allow(...)]` parses, dumps, formats and completes in editors, and changes nothing else. Every existing test passes.

---

## Phase 3: User Story 1 - An agent may run a task with the allowed value and no other (Priority: P1) MVP

**Goal**: A call to a restricted task with a refused value is refused with the contract's text and
runs nothing. An allowed value runs (FR-007 to FR-010, FR-012, FR-016, FR-019, FR-023, FR-025,
FR-026, FR-030, research R4, R5, R9).

**Independent Test**: Quickstart rows A, B, C, N and R on stdio and HTTP.

### Tests first

- [X] T012 [P] [US1] Create `internal/cli/agentpolicy_test.go` with `TestPolicyBreach`: table cases for `policyBreach(task, args)`: no policy gives nil; every predicate met gives nil; one of two not met gives that one with its effective value; both not met gives both in the attribute's order; a predicate whose parameter is absent from the arguments is not met and shows the empty value; a policy that requires the empty string is met by an empty value; comparison is exact (`Staging` and ` staging` do not meet `staging`). `TestRenderPolicyValue`: a plain value gives `"prod"`; a value with a line break gives one line with `\n` escaped; a value of 200 characters gives the first 64 quoted and then `...`; a value holding a secret of the mask function is masked before it is cut, also when the secret straddles the 64th character. `TestPolicyDenialText`: the six texts of the `tools/call` table in [contracts/mcp-tools.md](contracts/mcp-tools.md), each from a `policyDenial` built by hand, compared whole, and the same denial rendered twice gives the same text (FR-026). Run it and see it fail to compile.
- [X] T013 [P] [US1] In `internal/cli/agentpolicy_test.go`, add `TestAdapterRefusesForbiddenValue`: with `adapterFor` on a Runefile whose `deploy` has `[agent-allow(env="staging")]` and a body that writes a marker in a temp directory, `Call` with `env=prod` returns the error `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"` and the marker does not exist; `Call` with `env=staging` returns exit 0 and the marker holds `staging`; a second parameter the policy does not name accepts any value; an empty map for a required restricted parameter gives the missing-argument error, not a run. `TestAdapterMasksRefusedValue`: with a secret in the adapter's environment, a refused value equal to the secret appears in the error as the mask and the secret does not appear. Run it and see it fail.
- [X] T014 [P] [US1] Create `test/integration/agent_policy_mcp_test.go` with the quickstart scratch Runefile as `agentPolicyRunefile` and `TestAgentPolicyMCP_DirectCall`, looping over `bothTransports` with `approvalSession`: row A (`deploy`, `env=staging`: not an error, `deploy.done` holds `staging`), row B (`deploy`, `env=prod`: `isError`, the whole text, `wantDone` with no names), row C (`rollout`, `env=staging`, `region=us-east-1`: the text with `region="us-east-1"` and both predicates), both values refused (`env=prod`, `region=us-east-1`), row N (`deploy` with `evn=prod`: the text starts with `unknown argument "evn" for task "deploy"`, no marker), and row R (`tag`). Each refused result has no structured content. Add `TestAgentPolicyMCP_MalformedValues` on stdio: for `tag`'s `channel`, the JSON values `7`, `true`, `null`, `{}` and `["be","ta"]` each give `isError` and no marker, and `["beta"]` either runs with `beta` or is refused, but never runs with another value. Add `TestAgentPolicyMCP_MasksSecret` on stdio, set up like `secret_masking_mcp_test.go`: with a secret in the server's environment, `tag` called with `channel` equal to the secret is refused and the text does not hold the secret. Run it and see it fail.

### Implementation

- [X] T015 [US1] Create `internal/cli/agentpolicy.go` with: `type refusedValue struct{ name, value string }`; `func policyBreach(t *ast.Task, args eval.Args) []refusedValue`, which returns the predicates of `t.AgentPolicy()` whose `args.Values[name]` is not exactly the predicate's value; `type policyDenial` with the fields of [data-model.md](data-model.md) (`kind`, `called`, `task`, `owner`, `refused`, `policy`, `reason`); the constants `maxPolicyValueRunes = 64` and `maxPolicyReasonRunes = 200`; `func renderPolicyValue(value string, mask func(string) string) string` (mask, cut to 64 characters, `strconv.Quote`, `...` after the quote when cut); and `func (d *policyDenial) text(mask func(string) string) string` producing the texts of the contract, with `oneLine` and the 200-character cut for `reason`. A nil mask is the identity. T012 passes.
- [X] T016 [US1] In `internal/cli/run.go`, add to `engine` the field `agent bool`, with a comment saying it marks a run that started from an agent's tool call (spec 050), and the field `mask func(string) string`, the masker for values in policy texts. In `internal/cli/mcp.go`, set `agent: true` and `mask: a.maskText` on the engine `call` builds, add `func (a *mcpAdapter) maskText(s string) string` (the `masked` closure of `approvalRequest`, which then calls it), and after `bindNamedParams` and before the `pending` block, return `mcpserver.Result{}, errors.New(d.text(a.maskText))` when the called task breaks its policy. T013 and T014 pass. Run the whole `internal/cli` and `mcpserver` packages and see every existing test pass.

**Checkpoint**: an agent gets staging and is refused production on both transports.

---

## Phase 4: User Story 2 - An omitted argument is checked against the default the task would use (Priority: P1)

**Goal**: The check uses the effective value, and a refused default is never replaced (FR-009,
FR-010).

**Independent Test**: Quickstart rows D, E and F.

### Tests first

- [X] T017 [P] [US2] In `internal/cli/agentpolicy_test.go`, add `TestAdapterChecksDefaults`: `publish` (default `prod`, policy `staging`) called with no arguments is refused with `env="prod"` in the text and no marker; called with `env=staging` runs; `rollout` called with `env=staging` only runs and its marker holds `staging-eu-west-1`; a default that is `env("RUNE_POLICY_TEST", "prod")` is refused when the variable is unset and runs when `t.Setenv` sets it to `staging`; a task with a default and the argument sent as an empty map never runs with a value other than its default. These pass after T016: they guard FR-009 and FR-010.
- [X] T018 [P] [US2] In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicyMCP_Defaults`, on both transports: row E (`publish` with no arguments: refused, `env="prod"`, no marker), row F (`publish` with `env=staging`: `publish.done` holds `staging`), row D (`rollout` with `env=staging`: `rollout.done` holds `staging-eu-west-1`), and a misspelled argument on a task with a default (`publish` with `evn=staging`: the unknown-argument error, no marker).

### Implementation

- [X] T019 [US2] No new code is expected: `call` checks the arguments `bindNamedParams` returned, which hold the defaults. If T017 or T018 fails, fix `call` so the check reads the bound arguments and nothing else.

**Checkpoint**: sending nothing does not pass the policy.

---

## Phase 5: User Story 3 - A task that runs a restricted task is not a way around it (Priority: P1)

**Goal**: Everything a call can reach is checked before the call starts, and again where a
restricted body would start. A body that runs `rune` carries the agent run with it (FR-012,
FR-012a, FR-014, FR-017 to FR-020, FR-018a, FR-024, research R6, R7, R8, R15).

**Independent Test**: Quickstart rows G to M, U and W.

### Tests first

- [X] T020 [P] [US3] In `internal/eval/eval_test.go`, add `TestScopeFork`: a fork sees the parameters, overrides, assignments and host hooks of its parent and the values already in the parent's cache; a variable first read through the fork is not in the parent's cache afterwards; a variable first read through the parent after the fork is not in the fork's cache. Run it and see it fail to compile.
- [X] T021 [P] [US3] In `internal/cli/agentpolicy_test.go`, add `TestVolatileExpr`: table cases for `(*policyWalk).volatileExpr(expr, params)` on expressions parsed with `parser.ParseExprFragment`: a string literal and `env("X")` are not volatile; `uuid()`, `datetime()`, `path_exists("f")`, `read("f")`, `sha256_file("f")`, `which("x")`, `require("x")` and `absolute_path("x")` are; a volatile call nested in a concatenation, in a function argument and in each part of a conditional is; a reference to a parameter is volatile exactly when the map says that parameter is; a module variable is volatile when its assignment calls a volatile function directly, or through a second variable, and not when a run-time override replaces it; a variable assigned a literal is not. Add `TestBuiltinsClassified`: the volatile list and the stable list are disjoint and together equal `eval.BuiltinNames()`. Add `TestPolicyReaches`: on a Runefile with a restricted task, a wrapper, a wrapper of the wrapper, and an unrelated task, `reaches` is true for the first three and false for the last, and a second call returns the memoized answer.
- [X] T022 [P] [US3] In `internal/cli/agentpolicy_test.go`, add `TestPreflightPolicy`, table cases through `adapterFor` and `Call`, each with marker files: `ship: test (deploy "prod")` is refused with the reached-task text and `test` did not run; `preview: test (deploy "staging")` runs all three; `promote target: (deploy target)` is refused for `prod` and runs for `staging`; a `&&` hook `(deploy "prod")` is refused before the body; a `||` hook `(deploy "prod")` is refused though the task succeeds; a task that runs `(deploy "staging")` and `(deploy "prod")` is refused; a two-level chain (`outer: mid`, `mid: (deploy "prod")`) is refused and names `outer` and `deploy`; a private restricted task reached by a public task is refused; a restricted task with `[windows]` reached on `linux` (set the adapter's `goos`) is not counted; the failure hook of a failure hook is not counted; a restricted dependency that takes its value from a default (`wrap: publish`) is refused; a dependency argument with `path_exists` gives the volatile text; a volatile argument forwarded through a middle task (`mid e: (deploy e)`) gives the volatile text; the same middle task reached first with a fixed `"staging"` and then with a volatile argument that also evaluates to `staging` still gives the volatile text; `env("RUNE_NO_SUCH_SETTING")` as the argument gives the unresolved text and no marker; an unresolvable second argument that is for a parameter no predicate names also gives the unresolved text; an unresolvable edge toward a task that reaches no restricted task is not a denial. Add `TestPreflightDoesNotFixVariables`: with `version := read("VERSION")`, `bump` rewriting the file, and `release: bump (deploy "staging" version)` where `deploy` has a second, unrestricted parameter, the call runs and `deploy` received the content `bump` wrote. Run it and see it fail.
- [X] T023 [P] [US3] In `internal/cli/agentpolicy_test.go`, add `TestPolicyGate`: for `engine.policyGate(task, args)`: an engine with `agent` false returns nil whatever the arguments; with `agent` true and the policy met returns nil; with a breach returns a failure whose error text is the first-row text, and writes nothing. `TestExecuteRunsGate`: `Execute` on an agent engine with a breaching restricted task does not run the body (no marker), writes the text once to the engine's stderr, and returns a silent failure that matches `scheduler.ErrBodyNotRun`; `ExecuteFailHook` does not run the body, writes nothing itself, and returns the failure, which the scheduler prints as its hook warning. `TestCommandLineIgnoresPolicy`: `Run` (the command-line entry) with `deploy prod` and with `ship` on the same Runefile runs the bodies. Run it and see it fail to compile.
- [X] T024 [P] [US3] In `internal/cli/agentpolicy_test.go`, add `TestAgentEndpointAppliesPolicy`, modeled on `TestAgentEndpointRefusesReach`: the adapter of `newAgentAdapter` served with `agentMCPOptions` refuses `deploy` with `env=prod` with the policy text and runs it with `staging`. Add `TestContextHookAppliesPolicy`: a `[context]` task that depends on `(deploy "prod")` gives the hook's failure notice from `gatherContext` and no marker. The module case needs the composed project, so it is covered at binary level in T026 and not here. Run it and see it fail.
- [X] T025 [P] [US3] In `internal/cli/agentpolicy_test.go`, add `TestAgentRunEnv`: `agentRunEnv(nil)` holds the process environment and `RUNE_AGENT_RUN=1`; `agentRunEnv(base)` holds `base` and the mark and leaves `base` unchanged; a body run through `adapterFor` and `Call` sees the variable (`@echo $RUNE_AGENT_RUN > marker`). `TestCommandLineHonorsAgentMark`: with `t.Setenv("RUNE_AGENT_RUN", "1")`, `Run` with `deploy prod` returns a task failure whose text is the policy refusal and runs nothing; with `deploy staging` it runs; with `ship` it is refused before `test` runs; without the variable, and with it set to another value, `deploy prod` runs. The refusal holds no secret when a refused value is one. Run it and see it fail.
- [X] T026 [P] [US3] In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicyMCP_Reach`: rows G to K and the module case on both transports, rows L and M on stdio, each refused row with the whole text of the contract (row M: compare the text up to `(` and after `)`) and `wantDone` with no names; the module case written like `TestDependencyRefusalMCP_ImportedTask`: the tool `infra__deploy` with `prod` and a root task running `(infra::deploy "prod")` are refused. Add `TestAgentPolicyMCP_BodyRunsRune` (row W) on stdio, with the test binary reachable by the body: the call to `script` returns `[exit 1]`, its text holds the refusal, `script.done` exists and `deploy.done` does not; `rune script` on the command line exits 0 and `deploy.done` holds `prod`. Run it and see it fail.

### Implementation

- [X] T027 [US3] In `internal/eval/eval.go`, add `func (s *Scope) Fork() *Scope`: a copy of the scope with its own cache, seeded with a copy of the parent's, and its own cycle guard. T020 passes. In `internal/cli/run.go`, move the body of `ResolveDep` into `func (e *engine) resolveDepIn(scope *eval.Scope, curTask *ast.Task, cur eval.Args, dep *ast.DepCall)` and make `ResolveDep` call it with `e.scope`. Every existing scheduler and binding test passes.
- [X] T028 [US3] In `internal/cli/agentpolicy.go`, add the two builtin lists (volatile: `uuid`, `datetime`, `path_exists`, `read`, `sha256_file`, `which`, `require`, `absolute_path`; stable: every other name of `eval.BuiltinNames()`), built without `init()`; `type policyWalk` (the engine, the forked scope, the called task's name, `seen`, `reach`, the memo of volatile variables); `func (w *policyWalk) volatileExpr(e ast.Expr, params map[string]bool) bool` over `StringLit`, `VarRef`, `Binary`, `FuncCall` and `Conditional`; and `func (w *policyWalk) volatileVar(name string) bool` with a guard against assignment cycles. T021's first two tests pass.
- [X] T029 [US3] In `internal/cli/agentpolicy.go`, add `func (w *policyWalk) reaches(name string) bool` (memoized walk over `Task.Edges()` by name with an in-progress guard), `func (w *policyWalk) volatility(caller map[string]bool, dep *ast.DepCall, target *ast.Task) map[string]bool` per the table in [data-model.md](data-model.md), `func (w *policyWalk) check(task *ast.Task, args eval.Args, volatile map[string]bool) *policyDenial`, `func (w *policyWalk) visit(task *ast.Task, args eval.Args, volatile map[string]bool, failHook bool) *policyDenial` following research R6 (own check, then dependencies, `&&` hooks, and `||` hooks unless `failHook`; skip unknown, unavailable and non-reaching targets; the visited key holds the memo key, the mode and the sorted volatile names; `resolveDepIn` errors on the forked scope become the unresolved denial), and `func (e *engine) preflightPolicy(root *ast.Task, args eval.Args) *policyDenial`, which returns nil at once when `reaches(root.Name)` is false. Replace the root-only check of T016 in `mcpAdapter.call` with `eng.preflightPolicy`. T021, T022, T024 and the reach rows of T026 pass.
- [X] T030 [US3] In `internal/cli/agentpolicy.go`, add `func (e *engine) policyGate(task *ast.Task, args eval.Args) *TaskFailure`: nil unless `e.agent` and `policyBreach` is not empty; otherwise a `TaskFailure` whose error is the denial's text, written with `e.mask`. In `internal/cli/run.go`, call it first in `Execute` when `e.plan == planRun`: on a failure print the text to `e.opts.Stderr` with the `error` label and return a silent `TaskFailure` wrapping `bodyNotRunError`, so the text appears once and `||` hooks do not fire. Call it in `ExecuteFailHook` before the `[confirm]` gate and return the failure as it is: the scheduler prints it in its hook warning. T023 passes.
- [X] T031 [US3] In `internal/cli/agentpolicy.go`, add the constant `agentRunVar = "RUNE_AGENT_RUN"` and `func agentRunEnv(base []string) []string`, which returns a new slice holding `base`, or the process environment when `base` is nil, and `RUNE_AGENT_RUN=1`. In `internal/cli/mcp.go`, build the engine of `call` with `env: agentRunEnv(a.baseEnv)`. In `internal/cli/run.go`, in `execute`, set `agent: os.Getenv(agentRunVar) == "1"` and `mask` from the mask set on the engine, and when `eng.agent`, after `resolveRoots`, run `eng.preflightPolicy` on each invocation and return `maskErr(&TaskFailure{Err: errors.New(text)}, set)` for the first denial. `executeAgent` is not changed: the agent program inherits the engine's environment, mark included, when its task runs in an agent run. T025 and row W of T026 pass.

**Checkpoint**: no dependency, hook, module, nested agent or body that runs `rune` reaches production for an agent.

---

## Phase 6: User Story 4 - Approval and operator settings cannot lift a refused value (Priority: P1)

**Goal**: The policy is independent of the destructive check (FR-015, FR-021, FR-022).

**Independent Test**: Quickstart rows O, P and Q.

### Tests first

- [X] T032 [P] [US4] In `internal/cli/agentpolicy_test.go`, add `TestPolicyBeforeApproval`: with `CallApproved` on a task that has `[confirm]` and the policy, a refused value returns the policy error and the approve function was never called; an allowed value calls it once and runs on nil. `TestGrantDoesNotLiftPolicy`: an adapter with `grants` naming the task refuses `prod` and runs `staging`. These pass after T029: they guard FR-021.
- [X] T033 [P] [US4] In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicyMCP_ApprovalAndGrant`, rows O, P and Q on both transports: row O (`--allow-destructive release`, `release` with `env=prod`: the policy text, no marker), row P (no grant, an `asker` answering `yes`, `release` with `env=prod`: the policy text, `shown()` is empty, no marker), row Q (the same client, `env=staging`: one request shown, `release.done` holds `staging`), a client with no elicitation and no grant calling `release` with `env=prod` (the destructive refusal of spec 049, unchanged), and `--allow deploy` with `deploy` and `env=prod` (the policy text). Add `TestAgentPolicyMCP_ExposesNothing`: with `--deny deploy`, the tool list has no `deploy`, and `ship` is still refused with the reached-task text.

### Implementation

- [X] T034 [US4] No new code is expected: the walk sits before the approval request in `call`, and grants are read only at the `[confirm]` gate. If T032 or T033 fails, fix the order in `mcpAdapter.call`.

**Checkpoint**: neither a grant nor a yes runs production for an agent.

---

## Phase 7: User Story 7 - People, and tasks without the attribute, see no change (Priority: P1)

**Goal**: The command line and unrestricted Runefiles are untouched (FR-011, FR-013, FR-029).

**Independent Test**: Quickstart row T, and the unchanged tool JSON.

### Tests first

- [X] T035 [P] [US7] In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicy_CommandLineUnchanged`, with `RUNE_AGENT_RUN` removed from the child's environment: on the scratch Runefile, `rune deploy prod` exits 0 and `deploy.done` holds `prod`; `rune ship` exits 0 with `test.done`, `deploy.done` holding `prod`, and `ship.done`; `rune publish` exits 0 with `publish.done` holding `prod`; `rune --yes release prod` exits 0; `rune --list` exits 0. These pass after T031: they guard FR-013.
- [X] T036 [P] [US7] In `internal/cli/agentpolicy_test.go`, add `TestNoPolicyNoWalk`: on a Runefile with no `[agent-allow]`, `preflightPolicy` returns nil for every task without resolving any edge (a dependency whose argument is `env("RUNE_NO_SUCH_SETTING")` still fails in the run, with the same result as before).

### Implementation

- [X] T037 [US7] No new code is expected. If T035 or T036 fails, the cause is a missing `e.agent` or `reaches` guard: fix it there.

**Checkpoint**: a person at a terminal cannot tell the feature exists.

---

## Phase 8: User Story 5 - A wrong policy is an error in the Runefile, not a silent gap (Priority: P2)

**Goal**: `RUNE2019` for every invalid declaration (FR-002, FR-003, FR-005, research R3, R16).

**Independent Test**: Quickstart row V.

### Tests first

- [X] T038 [P] [US5] Create `internal/analyzer/agent_allow_test.go`, modeled on `readonly_test.go`. `TestAgentAllowRejected`: one case per row of the `RUNE2019` table in [contracts/agent-allow-attribute.md](contracts/agent-allow-attribute.md), checking the whole message, the source text the span covers and the line: a second attribute (span: the second attribute, with a related location on the first); an unknown name with a close parameter (`evn` for `env`: the hint) and with none; a name used twice (span: the second name); a `+` and a `*` variadic; `number`, `boolean`, `path` and `raw`; an enum without the value (span: the value); a value with a NUL (`"a\0b"`) and one with a line break. Also: two faults in one attribute give two errors; the predicates of a second attribute give no further error; a parameter with an unknown type name gives `RUNE2012` and no `RUNE2019`. `TestAgentAllowAccepted`: the quickstart scratch Runefile, an unannotated parameter, a `string` parameter, an `enum` parameter, the empty string as a value, a value with non-ASCII letters, a defaulted parameter whose default the policy forbids, the attribute with `[private]`, with `[confirm]` and with `[context]`, and a dependency argument the policy forbids give no `RUNE2019`. Run it and see it fail to compile.
- [X] T039 [P] [US5] In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicy_InvalidDeclaration`: for the unknown-name case, `rune analyze` exits 3 with `error[RUNE2019]: ` and the row's text once; `rune deploy staging` exits 3 and runs nothing; `rune mcp` exits 3 without serving. Run it and see it fail.

### Implementation

- [X] T040 [US5] In `internal/diag/codes.go`, add `CodeInvalidAgentAllow = "RUNE2019"` with the comment `// invalid [agent-allow]: unknown, repeated, variadic or non-string parameter, a value its enum lacks or that is not printable, or a second attribute (spec 050)`. In `internal/analyzer/analyzer.go`, add `checkAgentAllow` with the rules and texts of research R3 and R16, using `suggest.Nearest` for the hint and `strconv.IsPrint` for the value, and call it from `Analyze` after `checkConstraints`. T038 and T039 pass, and `internal/analyzer/codes_test.go` still passes.

**Checkpoint**: a policy Rune cannot apply never loads.

---

## Phase 9: User Story 6 - The tool tells the agent what it may send (Priority: P2)

**Goal**: The narrowed schema and the description paragraph (FR-027 to FR-031, research R10, R11).

**Independent Test**: Quickstart rows S and X.

### Tests first

- [X] T041 [P] [US6] In `mcpserver/server_test.go`, add `TestInputSchemaAgentValues`: an `enum` parameter with `Enum: ["staging","prod"]` and `AgentValues: ["staging"]` gives `enum` `["staging"]`; a string parameter with `AgentValues: ["beta"]` gives `type` `string` and `enum` `["beta"]`; `AgentValues` nil gives the schema of today (compare JSON with a `ParamInfo` that has no such field set); a variadic parameter ignores the field; `required` is the same with and without it. `TestToolForAgentPolicy`: one restricted parameter appends `\n\nAgent policy: agents may run this task only with env="staging".`; two give `env="staging" and region="eu-west-1"`; with `Returns` set the paragraph comes after `Returns:`; with no `Doc` and no `Returns` the description is the paragraph alone; two values give `env="a" or "b"`; no restricted parameter leaves the description as it was. `TestParamCheckIgnoresAgentValues`: `ParamInfo.check("prod")` is nil for an enum parameter restricted to `staging`. Run it and see it fail to compile.
- [X] T042 [P] [US6] In `internal/cli/agentpolicy_test.go`, add `TestAdapterTasksAgentValues`: `Tasks()` on the scratch Runefile sets `AgentValues` to `["staging"]` on `deploy`'s `env`, to nil on `test` and on `tag`'s `name`, and keeps `Enum` as `["staging","prod"]`; `Required` is true for `publish`'s `env` (literal default `prod`, policy `staging`), false for `rollout`'s `region` (default equals the policy value), false for a restricted parameter whose default is an expression, and unchanged for every unrestricted parameter. In `test/integration/agent_policy_mcp_test.go`, add `TestAgentPolicyMCP_ToolsList` (rows S and X) through a session's `ListTools`: the `env` property of `deploy` is `{"type":"string","enum":["staging"]}`, its description ends with the paragraph, `tag`'s `channel` has `enum` `["beta"]`, `publish` lists `env` in `required`, `rollout` does not list `region`, and the JSON of `test` equals the JSON the same task gives on a Runefile without any `[agent-allow]`. Add `TestAgentPolicyMCP_Reload`: with a running `rune mcp`, rewriting the Runefile to remove the attribute changes the listed `deploy` schema back to the two values and a call with `env=prod` then runs; reuse the reload helpers of `reload_mcp_test.go`. Run them and see them fail.

### Implementation

- [X] T043 [US6] In `mcpserver/server.go`, add `AgentValues []string` to `ParamInfo` with the doc comment of [contracts/mcpserver-api.md](contracts/mcpserver-api.md); in `inputSchema`, for a non-variadic parameter with `AgentValues != nil`, set `prop["enum"] = p.AgentValues`; add `func agentPolicy(params []ParamInfo) string` returning the paragraph or `""`, and append it in `toolFor` after the `Returns:` trailer with the same blank-line rule. Update the `inputSchema` and `toolFor` comments. T041 passes.
- [X] T044 [US6] In `internal/cli/mcp.go`, in `Tasks()`, for each predicate of `t.AgentPolicy()` that names the parameter set `pi.AgentValues = []string{p.Value}`, with a comment that the values are Runefile literals, and set `pi.Required = true` when the parameter's default is an `*ast.StringLit` whose value is not the predicate's. T042 passes.

**Checkpoint**: an agent that reads the tool list sends an accepted call first.

---

## Phase 10: User Story 8 - The documentation says what the policy covers and where it stops (Priority: P2)

**Goal**: The reference pages carry the attribute, the code and the limits (FR-032, FR-033,
research R14).

**Independent Test**: The documentation checks of [quickstart.md](quickstart.md).

- [X] T045 [P] [US8] In `docs/GRAMMAR.md`, add the `agent-allow` alternative to `AttrItem` after `param-doc` and the `AllowPred` rule after `AttrItem`, as in [contracts/agent-allow-attribute.md](contracts/agent-allow-attribute.md). In `docs/diagnostics.md`, add the `RUNE2019` row after `RUNE2018`.
- [X] T046 [P] [US8] In `docs/runefile.md`, add `[agent-allow(env="staging")]  # the only value an agent may run the task with` to the attribute list after `[param-doc]`, and a paragraph after the `[readonly]` paragraph: what it does, that it names unannotated, `string` and `enum` parameters only, that predicates are combined with "and", that the command line is not limited, that a wrong declaration is `RUNE2019`, with a link to the new section of `docs/mcp.md`.
- [X] T047 [P] [US8] In `docs/mcp.md`, add the section `## Restricting the values an agent may pass` after "Approving a destructive call in the client": the example of the spec's Overview in a `rune` block, the refusal text, the table of calls, defaults (no substitution, and the parameter listed as required when its literal default is refused), dependencies, hooks, modules and `agent` tasks, the volatile and unresolved refusals with the list of functions, a body that runs `rune` and the `RUNE_AGENT_RUN` mark, the order of checks relative to the destructive refusal and the approval request, the narrowed tool, and the limits (an agent with its own shell, an agent that can edit the Runefile, a body that clears the mark or runs another tool, pointing to the design note on Rune-only agent mode). Add one sentence to "Security model" and one to "Typed tool schemas" that point to it.
- [X] T048 [P] [US8] In `SECURITY.md`, add one sentence beside the one on `[confirm]` and `[readonly]`: `[agent-allow]` limits the values an agent may run a task with, is checked before anything runs, and does not cover an agent's own shell. In `README.md`, in the "AI-native and secure by default" bullet, add the clause that `[agent-allow]` limits the values an agent may pass.
- [X] T049 [US8] Run the docs harness, `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...`, and fix any link or code-block failure in the changed pages.

**Checkpoint**: a reader can find the attribute, its error and its limits.

---

## Phase 11: Polish & Cross-Cutting Concerns

- [X] T050 Run `gofumpt` and `goimports` through the linter image, then `docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run`, and fix every finding in the changed files.
- [X] T051 Run the whole suite, `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`, then with the race detector, `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...`. Both pass.
- [X] T052 Measure coverage of the new code: `go test -coverprofile` for `./internal/cli`, `./internal/analyzer`, `./internal/parser`, `./internal/eval` and `./mcpserver`, then `go tool cover -func` filtered to `agentpolicy.go`, `checkAgentAllow`, `parseAgentAllowArgs`, `agentPolicy`, `AgentPolicy` and `Fork`. Each function is at 90% or more. Add cases for any branch below that.
- [X] T053 Check the goldens: `git status` shows no changed golden other than the new `testdata/corpus/agent-allow.ast` and `testdata/fmt/agent-allow.fmt`, and a search of the changed files for the character U+2014 finds none.
- [X] T054 Walk [quickstart.md](quickstart.md) once against the built binary for rows A, B, E, G and S by hand through `rune mcp`, and confirm each row of the table names a test that exists.

---

## Dependencies & Execution Order

- **Setup (Phase 1)**, then **Foundational (Phase 2)**: blocks every story.
- **US1 (Phase 3)**: after Foundational. The MVP.
- **US2 (Phase 4)**: after US1. Tests only, unless they find a gap.
- **US3 (Phase 5)**: after US1. T029 replaces the root-only check of T016.
- **US4 (Phase 6)** and **US7 (Phase 7)**: after US3. Tests only, unless they find a gap. US7 is
  P1 and comes after US4 because its tests guard what US3 adds to the command-line path.
- **US5 (Phase 8)**: after Foundational. Independent of US1 to US4.
- **US6 (Phase 9)**: after Foundational. T044 edits `internal/cli/mcp.go`, as T016, T029 and T031
  do, so it follows them.
- **US8 (Phase 10)**: after US3, US5 and US6, so the pages describe what was built.
- **Polish (Phase 11)**: last.

Within a story: tests first and seen failing, then the implementation, then the checkpoint.

## Parallel Opportunities

- T002, T003 and T004 touch three test files. T009, T010 and T011 touch three unrelated files.
- T012 and T013 are in one file and are written together; T014 is separate.
- T020 (`internal/eval`) is separate from T021 to T025 (`internal/cli`).
- US5 (T038 to T040) can be done beside US1 to US4: it touches `internal/analyzer` and
  `internal/diag` only.
- T041 (`mcpserver`) can be written beside any `internal/cli` task.
- T045 to T048 touch separate documents.

## Implementation Strategy

1. Phases 1 and 2: the attribute parses and prints everywhere.
2. Phase 3 (US1): the MVP. An agent is refused production on a direct call.
3. Phases 4 to 7: defaults, reach, independence from approval, and the unchanged command line.
   After Phase 5 a dependency, a hook, a module task, a nested agent and a body that runs `rune`
   are all checked. What stays out is listed in the spec: an agent's own shell, an edited
   Runefile, and a body that clears the mark.
4. Phases 8 and 9: static errors and the narrowed tool.
5. Phases 10 and 11: documents, lint, race run, coverage.
