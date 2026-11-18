# Quickstart: Validating the Plan and Apply Binding

**Feature**: 051-mcp-plan-apply-binding | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

The attributes and `RUNE2020` are in
[contracts/plan-attributes.md](contracts/plan-attributes.md), the tools, the request and the
texts in [contracts/mcp-tools.md](contracts/mcp-tools.md), the library fields in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md), the store in
[contracts/plan-store.md](contracts/plan-store.md). This guide only says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run
```

Scenarios A to Y are automated in `test/integration/plan_apply_mcp_test.go` against the built
binary with an SDK client. A to H, L, P to W, Y and AD run on stdio and on `rune serve --http`.
I, J, K, M, N, O, X and AE run on stdio. Z is automated in `test/integration/plan_apply_cli_test.go`. AA is automated in
`internal/analyzer/plan_binding_test.go` and, for one row, at binary level. AB is automated in
`internal/planstore` and at binary level. AC is automated at binary level.

## Scratch Runefile

The stand-in for the tool is written in Rune's own shell, so it runs on every host. The plan
holds the state serial it was made for, and the apply fails when the serial moved: that is the
tool's stale-plan check. Every body writes a marker file.

```rune
# Plan the change.
[plan(file="tfplan", summary="tfplan.txt")]
plan env:enum("staging","prod"):
    @read -r serial < state.serial; echo "plan env={{env}} serial=$serial" > tfplan; echo "Plan: 1 to add for {{env}}" > tfplan.txt; echo planned >> plan.done

# Apply the reviewed plan.
[applies("plan")]
apply env:enum("staging","prod"):
    @read -r serial < state.serial; read -r line < "$RUNE_PLAN_FILE"; case "$line" in *"serial=$serial") ;; *) echo "Saved plan is stale" >&2; exit 1 ;; esac; echo "$line" > applied.txt; echo $((serial + 1)) > state.serial; echo applied >> apply.done
```

`state.serial` starts as `1`.

## Scenarios

| # | Do | Expect |
|---|---|---|
| A | Call `plan` with `env=prod` | Runs. The result's standard error has the plan line. `tfplan` and `tfplan.txt` are gone from the workspace. The store holds one record whose artifact has the bytes the body wrote |
| B | Then call `apply` with `env=prod`, approving | One request with the task, `env: "prod"`, the plan id, the digest and the summary line. `applied.txt` holds the plan's line. `plan.done` has one line |
| C | Call `apply` again | Refused: already applied. No request. `apply.done` has one line |
| D | Fresh store, call `apply` | Refused: no plan. No request |
| E | Plan `staging`, call `apply` with `prod` | Refused: no plan for `env="prod"` |
| F | Plan, overwrite `<id>/artifact`, call `apply` | Refused: does not have the recorded digest |
| G | Plan, replace `<id>/artifact` with a symbolic link (POSIX) | Refused: is not a regular file |
| H | Plan, edit the Runefile, wait for the reload, call `apply` | Refused: the Runefile definition changed |
| I | Plan with `tool="faketool"`, replace `faketool` on the path, call `apply` | Refused: the tool changed |
| J | Plan through the server with `set dotenv`, change the dotenv file, run `rune --yes apply prod` on the command line | Refused: the configuration changed. Calling `apply` on the same server instead still asks |
| K | Plan, set the record's expiry to the past, call `apply` | Refused: expired |
| L | Plan twice, call `apply`, approving | The request names the second plan's id. The first record has no artifact |
| M | Plan, write other bytes to `tfplan` in the workspace, apply | `applied.txt` holds the stored plan's line |
| N | A plan task whose body leaves `tfplan` a symbolic link (POSIX) | The task fails. No record |
| O | Plan, raise `state.serial`, apply, approving | The call fails with `Saved plan is stale`. The record's outcome is failed |
| P | Plan, two `apply` calls at once, both approved | `apply.done` has one line. One result is the refusal: already used |
| Q | After O, call `apply` | Refused: the apply failed. No request |
| R | After O, plan again, call `apply` | The request holds the `Note:` line about the failed apply |
| S | Plan, write a claim into `<id>/claim` by hand, call `apply` | Refused: did not finish |
| T | Plan, call `apply`, decline. Call again, approve | Nothing ran the first time. The second runs |
| U | Serve with `--allow-destructive apply`, plan, call `apply` | A request still arrives |
| V | Client without form elicitation, call `apply` | Refused with the reason and `rune apply`. Nothing ran |
| W | `apply` also has `[agent-allow(env="staging")]`, call with `prod` | Refused for the value. No request |
| X | A task whose body runs `rune --yes apply prod`, called by an agent | The body fails with the agent-run refusal. `apply.done` is absent |
| Y | `apply` also has `[confirm("Really?")]`, call it | One request that holds the plan and `Really?` |
| Z | Command line: `rune plan prod`, then `rune apply prod` on a terminal with `n`, then `y`; `--yes`; no terminal; `rune apply prod apply staging` | Plan line; the question with the digest; `n` runs nothing and leaves the plan ready; `y` applies; `--yes` applies without a question; no terminal is refused naming `--yes`; two tasks with an apply task is a usage error |
| AA | Each invalid declaration of the attribute contract with `rune analyze` | Exit 3 and `error[RUNE2020]:` with the contract's message |
| AB | After B: list `.rune/plans` | Directories `0700`, no file readable by group or others (POSIX), no artifact left, journal lines for `plan-created`, `approval-requested`, `approval-given`, `apply-started`, `apply-finished` with one plan id, and no plan or summary bytes in it |
| AC | A Runefile with neither attribute, served and run | Tools and results as before. No `.rune/plans` |
| AD | Plan, overwrite `<id>/summary`, call `apply` | Refused: the stored summary does not have the recorded digest. No request |
| AE | Plan, call `apply`; while the request is pending, plan again; then approve | Nothing ran. Refused: replaced by a newer plan. No failed outcome. The next `apply` asks about the new plan with no `Note:` line |

## Documentation checks

- `docs/mcp.md` has the section on applying a reviewed plan, with the lifecycle, retention,
  recovery, limits, trust assumptions, and the statement on Helm and migrations.
- `docs/runefile.md`, `docs/GRAMMAR.md` and `docs/diagnostics.md` have the attributes and
  `RUNE2020`.
- `docs/examples/plan-apply-opentofu` and `docs/examples/plan-apply-terraform` validate.
