# A reviewed plan with OpenTofu

> **Use case:** let an agent, or a person, plan an infrastructure change, and apply it only
> after a person approved that exact plan.

**Demonstrates:** `[plan]` and `[applies]`, plan approval  ·  **Guide:** [Applying a reviewed plan](../../mcp.md#applying-a-reviewed-plan)

**Prerequisites:** opentofu

## Run it

```sh
rune plan staging
rune apply staging
```

## Expected output

`rune plan staging` prints what `tofu` prints, which depends on its version. When the body
ends, Rune adds one line on standard error in this format:

```text
plan <id>: sha256:<hex>, <n> bytes, expires <time>. Apply it with "apply".
```

`<id>` is the plan's id, `sha256:<hex>` the digest of the saved plan, `<n>` its size, and
`<time>` the time after which it can no longer be applied. `tfplan` and `tfplan.txt` are then
gone from this directory: Rune moved them into `.rune/plans/`.

`rune apply staging` prints the plan's id, the target, the digest and the summary, and asks
`Apply this plan once? [y/N]`. On `y` it runs `tofu apply` with the stored plan. Any other
answer runs nothing and leaves the plan usable.

## How it works

- `[plan(file="tfplan", summary="tfplan.txt", tool="tofu")]` marks `plan` as the task that
  makes a plan. Its body saves the plan with `-out=tfplan` and writes what `tofu show` prints
  to `tfplan.txt`, the summary a person reads before approving.
- `[applies("plan")]` marks `apply` as the task that applies it. The body receives the stored
  plan as `RUNE_PLAN_FILE`. It has the same parameter as `plan`, and it uses the plan made
  for the same value: a plan for `staging` is no plan for `prod`.
- A plan is applied once. A second `rune apply staging` is refused until `rune plan staging`
  makes a new plan. `tofu` still makes its own stale-plan check when it applies.
- `tool="tofu"` binds the executable. If `tofu` on the path is another file when `apply`
  runs, the apply is refused.
- `plan` runs `init` first. `-input=false` makes `tofu` fail where it would ask for input.
- `main.tf` uses only the built-in `terraform_data` resource, so no provider is downloaded.
- Served with `rune mcp`, an agent's call to `apply` is put to the person at the client, with
  the same identity and summary. `--allow-destructive apply` does not approve a plan.

Tier-B verification skips this example unless OpenTofu is installed. The Runefile is still
statically validated.
