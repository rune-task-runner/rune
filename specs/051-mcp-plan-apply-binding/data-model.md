# Data Model: Bind Approval and Apply to the Exact Reviewed Plan

## Plan arguments (`ast.PlanSpec`)

From `[plan(...)]` on a task. All values are string literals.

| Field | Rule |
|---|---|
| `File` | required, not empty, a relative path written with forward slashes |
| `Summary` | required, same rules, a different file from `File` after `path.Clean` |
| `Tool` | optional, a bare executable name without a path separator |
| `Expires` | optional duration, one minute to seven days, default one hour |

A path is relative when it does not start with `/` or `\`, has no drive prefix such as `C:`,
holds no `\`, and has no `..` element. The rule reads the text only, so it is the same on
every host.

`ast.PlanExpiry(text)` parses `Expires` and applies the default and the range. The analyzer and
the engine both call it.

## Apply binding (`Task.AppliesPlan`)

The name of the plan task, after module namespacing. Validated by the analyzer: the task
exists, has `[plan]`, and has the same parameters. Parameters are the same when they agree in
number and, position by position, in name, in parameter kind (required, defaulted, `+`, `*`)
and in constraint kind and enum values. Defaults may differ.

## Target key

`key = SHA-256(absolute Runefile path, NUL, plan task name, NUL, eval.Args.Key() of the
effective arguments)`, in hex. The directory of the path has its symbolic links resolved, and so
has the bound working directory, so one project reached by two paths is one target. Two Runefiles in one directory, two plan tasks, or two sets of
arguments never share a key. The apply call computes the same key from its own arguments and
its plan task's name.

## Plan record (`planstore.Record`, `record.json`)

Written once when a plan is stored. Never holds plan or summary bytes.

| Field | Meaning |
|---|---|
| `id` | 16 hex characters from 8 random bytes. Also the directory name |
| `key` | the target key |
| `created`, `expires` | UTC times with nanoseconds, from the store's clock |
| `planTask` | the task that made the plan |
| `applyTasks` | the tasks whose `[applies]` names it, sorted |
| `args` | the effective arguments as masked display text, `name="value"` pairs |
| `binding.workDir` | the plan task's resolved directory |
| `binding.runefile` | SHA-256 of `ast.Dump` of the composed file |
| `binding.config` | SHA-256 of the resolved settings, the dotenv entries and the command-line variable overrides, as the load that made the plan had them |
| `binding.tool`, `binding.toolPath`, `binding.toolDigest` | the declared tool, or empty |
| `binding.host` | `GOOS/GOARCH` |
| `artifact.sha256`, `artifact.size` | the stored plan file |
| `summary.sha256`, `summary.size` | the stored summary |
| `runeVersion` | recorded only |
| `git.commit`, `git.dirty` | recorded only, empty when unknown |
| `surface` | `mcp` or `cli` |

The configuration digest is computed once per load of the Runefile, from what that load read:
a server computes it when it loads or reloads, the command line when it starts. A dotenv file
edited afterwards does not change what a running server runs with, so it does not change the
server's digest either. A restart, or the command line, sees the new file.

The tool is looked up on the `PATH` of the environment and directory the plan task's body runs
with, then resolved through symbolic links, and its bytes are hashed. An apply call resolves the
name again for the apply task's body and compares the result with the record, so the apply runs
the executable the plan was made with.

## Plan state

See [contracts/plan-store.md](contracts/plan-store.md) for the derivation.

| State | Can be applied |
|---|---|
| `ready` | yes |
| `unfinished`, `applied`, `failed`, `replaced`, `expired` | no |

Transitions: `ready` to `replaced` (a newer plan of the same key), `ready` to `expired` (time),
`ready` to `unfinished` (claim), `unfinished` to `applied` or `failed` (finish). There is no
way back to `ready`.

## Attempt (`planstore.Attempt`, `planstore.AttemptInfo`, `claim`, `outcome.json`)

| Field | Meaning |
|---|---|
| `attempt` | 16 hex characters. In `claim` and `outcome.json` |
| `started` | when the claim was made. For a claim file that cannot be read, its modification time |
| `applyTask`, `surface` | who claimed |
| `finished`, `exit`, `state`, `tampered` | in `outcome.json` only |

`Attempt.Path` is the private copy handed to the apply body. `AttemptInfo` is what
`LastAttempt` returns: the plan id, the fields above, and the state `applied`, `failed` or
`unfinished`.

## Journal event (one JSON line)

| Field | Present |
|---|---|
| `time`, `event`, `surface`, `planTask` | always |
| `plan`, `digest` | whenever a plan is known: every event except an `apply-refused` for no plan |
| `args` | every event |
| `applyTask` | approval and apply events |
| `attempt` | `apply-started`, `apply-finished` |
| `replacedBy` | `plan-replaced`: the id of the newer plan. `plan` is the replaced one |
| `reason` | `apply-refused`, `approval-not-given`, and `approval-given` by `--yes` |
| `exit`, `state`, `tampered` | `apply-finished` |

Events: `plan-created`, `plan-replaced`, `approval-requested`, `approval-given`,
`approval-not-given`, `apply-refused`, `apply-started`, `apply-finished`.

`apply-refused` is written for a refusal of the plan checks and of the claim. A call refused
earlier, for its arguments, for the `[agent-allow]` policy, or because the engine was called
with nobody to ask, involves no plan and is not journaled. A person who cannot be asked is
journaled as `approval-requested` followed by `approval-not-given` with the reason.

## Server library additions

- `mcpserver.TaskInfo.AppliesPlan string`: the plan task an apply task applies, or "".
- `mcpserver.ApprovalRequest.Plan *PlanApproval`: what the request shows about the plan.
- `mcpserver.PlanApproval`: `ID`, `Task`, `Created`, `Digest`, `Size`, `Summary`, `Notes`.
- `mcpserver.SummaryLines(summary string, head, tail int) string`: the summary as the request
  and the terminal show it.

## Engine additions (`internal/cli`)

- `engine.planDef`, `engine.planConfig string`: the Runefile definition digest and the
  configuration digest of this load.
- `engine.runefile string`: the absolute path of the Runefile, for the key.
- `engine.surface string`: `mcp` or `cli`.
- `engine.attempt *applyAttempt`: the claimed attempt of this run, with the apply task's name
  and the key of its arguments.
- `engine.clock func() time.Time`: the store's clock. Nil means `time.Now`.
- `engine.gitInfo func(ctx context.Context, dir string) (commit string, dirty *bool)`:
  replaceable in tests.

## Order of checks for an apply call from an agent

0. The engine can take an approval at all. If not: the server's refusal.
1. Unknown argument names and invalid values (specs 032, 023).
2. Binding of arguments and defaults.
3. The `[agent-allow]` policy (spec 050).
4. The plan: found, ready, bound parts equal, artifact and summary verify.
5. The approval: the request of spec 049 with the plan in it, or, when the person cannot be
   asked, the refusal that says why.
6. The claim and the private copy.
7. The run. Then the finish.

A refusal at steps 0 to 4 sends no request. A refusal at step 5 or 6 leaves the plan's state
as it was.
