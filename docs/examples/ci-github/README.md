# GitHub Actions workflow from the Runefile

> **Use case:** run the Runefile's tasks in GitHub Actions without writing them twice: one
> job per task, in the order a local run keeps, with the build's output passed to the test.

**Demonstrates:** `rune ci github`, `[parallel]` jobs, `[cache]` outputs between jobs, a
drift check  ·  **Guide:** [Generate a GitHub Actions workflow](../../how-to/ci-github.md)

**Prerequisites:** none

## Run it

```sh
rune ci github --check --rune-version 0.11.0 gate
```

## Expected output

```text
.github/workflows/rune.yml: current
```

The committed [`.github/workflows/rune.yml`](.github/workflows/rune.yml) is what
`rune ci github --rune-version 0.11.0 --write gate` writes. GitHub runs workflows only from the
repository root, so this copy is never run; in your project the file lands in your root's
`.github/workflows/`.

## How it works

`gate` is the one root task. It has `[parallel]`, so `lint`, `test` and `workflow-check` are
jobs that run at the same time; `test` needs `build`, which needs `generate`. Six jobs in
all, each running `rune ci run TASK`: the body of that task and nothing it depends on.

`build` declares `[cache(inputs=["src/**"], outputs=["dist/app"])]`. Its job uploads
`dist/app`, and the `test` job downloads it before its body runs. On a later push with no
change under `src/`, the `build` job restores its cache entry by exact key and logs
`cached: build` instead of running.

`workflow-check` runs `rune ci github --check`, so a pull request that changes the Runefile
without writing the workflow again fails.
