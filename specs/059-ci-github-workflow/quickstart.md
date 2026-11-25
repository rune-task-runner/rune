# Quickstart: validate `rune ci github`

All tests run in Docker (`docker-compose run --rm test go test ./...`). The scenarios below
map to tests; the commands show what each proves.

## 1. Diamond graph, run-once, outputs (US1, US2, US4)

Fixture `test/integration/testdata/ci/diamond/Runefile`: `lint` and `test` depend on
`build`, which depends on `generate`; `build` declares `[cache(inputs=..., outputs=...)]`;
every body appends its name to `$RUNE_CI_LOG`.

```sh
rune ci github --rune-version 0.11.0 lint test      # 4 jobs; build once
```

`TestCIGithubSimulatedRun` runs each job in its own copy of the project in `needs:` order and
checks: each body logged once; `test`'s copy has `build`'s output before its body; the files
match a local `rune lint test`. A second simulated run logs `cached: build` and no `build`
body; after removing the output from the stored bundle, the third run runs `build`.

## 2. Order and hooks (US2)

`internal/ci` graph tests: a task without `[parallel]` on `a b c` gives the chain
`a -> b -> c -> task`; with `[parallel]` no needs among `a b c`; `&& notify` gives
`notify` needs the task and its dependents need `notify`.

`internal/cli` tests: `rune ci run` of a failing task with `|| diagnose` runs `diagnose`
with `RUNE_FAILED_TASK` and exits with the body's code; dependencies do not run.

## 3. Safety (US3)

Golden files under `internal/ci/testdata/` pass the invariants of contracts/workflow.md and
`actionlint`:

```sh
rune actionlint
```

Refusal fixtures (`[confirm]`, `[applies]`, agent executor, fail hook with dependencies,
background prerequisite, absolute output, `env()` argument, `${{` argument) each exit 3 with
the task named and nothing on standard output.

## 4. Cache keys (US4)

`internal/cli` key tests: changing the body, an imported variable the task uses, an input
file, the executor, `GOOS` or the version changes the key; changing a comment or another
task does not.

## 5. Write and check (US5)

```sh
rune ci github --rune-version 0.11.0 --write lint test   # created
rune ci github --rune-version 0.11.0 --write lint test   # unchanged
rune ci github --rune-version 0.11.0 --check lint test   # current, exit 0
# edit the Runefile
rune ci github --rune-version 0.11.0 --check lint test   # stale, exit 1
```

A hand-written `.github/workflows/rune.yml` is never replaced (exit 2).

## 6. Documentation example

`docs/examples/ci-github/` holds a Runefile and its committed workflow. Its README's run
command is `rune ci github --check ...`, so the docs harness fails when the example is
stale.
