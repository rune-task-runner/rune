# CI/CD pipeline

> **Use case:** run the same lint/test/build gate locally and in CI, with a gated deploy and
> predictable exit codes.

**Demonstrates:** dependencies, dry-run, confirm gating, exit codes  ·  **Guide:** [CLI reference](../../cli.md)

**Prerequisites:** none

## Run it

```sh
rune ci
```

## Expected output

```text
golangci-lint run
go test ./...
go build ./...
CI passed ✓
```

In CI, `rune ci` exits non-zero if any step fails, so the pipeline fails fast. Preview without
running via `rune --dry-run ci`, and list tasks with `rune --list`. `deploy` is gated with
`[confirm]`, so an unattended run needs `--yes`.

## How it works

`ci: lint test build` chains the gate; `[confirm("…")]` on `deploy` blocks accidental or
unattended production deploys. Exit codes: `0` success, a failed task body's own code (`1`
when the failure has no exit code), `2` usage, `3` validation, `130` interrupted.

## Branching on the exit code

When a task body fails, rune exits with that body's code, so CI can tell a deliberate refusal
from a failure without parsing output. `release-gate` exits `2` when the release window is
closed:

```sh
rc=0
rune release-gate "$WINDOW" || rc=$?
case "$rc" in
  0) echo "gate open, releasing" ;;
  2) echo "gate refused, skipping the release" ;;
  *) echo "release gate failed (exit $rc)"; exit "$rc" ;;
esac
```

Rune also exits `2` for its own usage errors, such as an unknown task name. The `rune:` line on
stderr tells the two apart:

```text
rune: task "release-gate" failed at "…": exit status 2   <- the gate refused
rune: unknown task: release-gat                          <- usage error, also 2
```
