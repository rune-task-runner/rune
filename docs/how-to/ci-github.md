# Generate a GitHub Actions workflow

> Write the CI workflow from the Runefile, so CI runs the same tasks, in the same order, with
> the same caching, as a run on your machine.

Name the tasks CI exists to run, the roots, and Rune writes a workflow with one job for each
task and arguments they reach:

```sh
rune ci github lint test                  # print the workflow
rune ci github --write lint test          # write .github/workflows/rune.yml
rune ci github --check lint test          # is the file current? exits 1 when not
```

Nothing runs while the workflow is generated: no task body, no hook, no network. The
environment is not read. The task bodies stay in the Runefile; each job installs a pinned
Rune release and runs `rune ci run TASK ARG...`.

## Choose the roots

Rune adds no task by itself: only the roots you name and the tasks they reach become jobs.
Name a deployment, a `[context]` hook or a slow task only when CI should run it on every
push and pull request.

A root takes arguments as on the command line, `rune ci github test ./pkg/... lint`.
Variable overrides (`NAME=value`) are refused: no job could repeat them.

Roots do not wait for each other, like the dependencies of a `[parallel]` task. To order
them, name one root that depends on all of them.

## Jobs and `needs:`

Every task with its arguments is one job, and runs once in the whole workflow, as it runs
once in a local run. Its `needs:` keep the order a local run keeps:

- A task's dependencies finish before it starts.
- Without `[parallel]`, each dependency also waits for the ones listed before it. A failure
  stops the later ones, as it does locally.
- With `[parallel]`, the dependencies are jobs that run at the same time.
- A post-hook (`&& notify`) waits for its task, and a task that depends on that task waits
  for the post-hook.

```rune
generate:
    @echo generating

# One job: build. It needs generate.
build: generate
    @echo building

lint:
    @echo linting

test: build
    @echo testing

# lint, test and their prerequisites run as parallel jobs; gate waits for them.
[parallel]
gate: lint test
    @echo "CI passed"
```

`rune ci github gate` gives five jobs: `generate`, `build` (needs `generate`), `lint`, `test`
(needs `build`) and `gate` (needs `lint` and `test`). Without `[parallel]` on `gate`, `test`
would also wait for `lint`.

Each job runs `rune ci run TASK ARG...`: the body of that task through the same engine as
`rune TASK`, without its dependencies and post-hooks, which ran in the jobs before it. When
the body fails, its failure hooks (`|| diagnose`) run in that job with the failed task and
exit code, the job fails with the body's exit code, and the jobs that need it do not run.
Jobs that do not need it keep running. A cancelled run stops the body as Ctrl-C does: no
failure hook runs.

Run `rune ci run TASK` on your machine to see what one job does.

## What travels between jobs

Every job starts from a fresh checkout. Files a task writes stay in its job, unless the task
declares them as `[cache]` outputs:

```rune
[cache(inputs=["src/**", "go.mod", "go.sum"], outputs=["dist/app"])]
build:
    go build -o dist/app ./cmd/app

test: build
    ./dist/app --self-test
```

The `build` job uploads `dist/app` as an artifact, and every job that waits for `build`,
directly or not, downloads it before its body runs. A job that does not wait for `build`
gets nothing from it. Outputs are regular files and directories inside the project; a link
in an output fails the job. Artifacts are kept for one day.

Nothing else is shared: no environment variable, no running process. A prerequisite that
starts a server in the background (`server &`) for a later task is refused, because the
server ends with its job. Run such a server in the body of the task that uses it.

## Caches

A `[cache]` task gets a cache key from Rune's own fingerprint of it: its input files, its
body, the variables it uses, its executor, the Rune version, the runner's OS and
architecture, and the runner image. The job restores the entry with exactly that key, never
a near match. The body is skipped only when Rune decides so, as on your machine: the stored
fingerprint matches and every declared output is there. The job then logs `cached: build`.
An entry is saved only after the body succeeded.

To make CI run a task every time, leave `[cache]` off it. Add the files that pin your
toolchain, such as `go.mod`, `package-lock.json` or `.python-version`, to `inputs` so a
toolchain change is a new key.

GitHub limits the total size of a repository's caches and evicts entries, and a pull request
can read the caches of its base branch while what it saves stays with the pull request; see
GitHub's [dependency caching reference](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching).

## Runners and toolchains

`--os linux` (the default) runs jobs on `ubuntu-latest`, `--os macos` on `macos-latest`;
give `--os` twice for both, and every job ID ends in its OS. Windows runners are not
supported. A task not available on a runner's OS is left out of that OS's jobs, as the
scheduler skips it; a root not available there is refused.

The workflow installs Rune and nothing else. Shell bodies run in Rune's own shell. Python
and Node bodies need the interpreter on the runner image, which the GitHub-hosted images
have. Install any other tool in a task the others depend on.

Each job installs the Rune release `--rune-version` names, by default this rune's own
version, and checks the archive against the release's published SHA-256 checksums. It must
satisfy the Runefile's `minimum_version`. A development build of Rune needs the flag.

## What the workflow may do

- It reads the repository: `permissions: contents: read`, and checkout keeps no credentials.
- It runs on pushes to `--branch` (default `main`) and on pull requests, with the pull
  request's own read-only token; never with `pull_request_target`.
- It references no secret, and caches and uploads only declared outputs and cache records.
- No value from the Runefile or an event is written into a script as an expression.

These tasks are refused, with the reason:

| Refused | Why | What to do |
|---|---|---|
| `[confirm]` on a task or failure hook | no person is there to ask, and Rune never answers for one | run it by hand |
| `[applies]` | it runs only a plan a person approved | run it by hand |
| a task with the `agent` executor | it needs an agent's credentials | run it by hand |
| a failure hook with dependencies or post-hooks | they would run again inside the failing job | give the hook neither |
| a background process (`cmd &`) in a task another job needs | the process ends with its job | start it in the task that uses it |
| a `[cache]` output outside the project | a bundle carries project files only | write outputs inside the project |
| a dependency argument or parameter default that calls `env`, `read`, `path_exists`, `which`, `require`, `datetime`, `uuid`, `num_cpus`, `arch`, `absolute_path` or `sha256_file` | the value would be this machine's, not the runner's | pass a literal or a parameter |
| a dependency call whose arguments a command line could not bind, such as an empty list forwarded into a `+` parameter | the job binds the arguments again from `rune ci run TASK ARG...` | pass the arguments the task needs |
| `${{`, a control character or a line break in an argument | GitHub would evaluate it, or a script could not carry it | change the argument |

`os()` and `os_family()` are evaluated for the runner's OS.

## Write the file and keep it current

```sh
rune ci github --write lint test
```

writes `.github/workflows/rune.yml` next to the Runefile. `--output PATH` writes another
file; when the Runefile is in a subdirectory of the repository, write to the root's
`.github/workflows/` and the jobs run Rune in the Runefile's directory. The jobs find the
Runefile there by name, without `-f`, so `-f` must name a file Rune would find in its own
directory: a `Runefile` or `.runefile`, the only one there. The first line marks
the file as generated, and the second is the command that writes it again. Rune replaces
only a file with that first line; a workflow you wrote by hand is never touched. Writing
twice with nothing changed leaves the file as it is.

`--check` compares the file with what `--write` would write, writes nothing, and exits 1 when
it is missing or stale, printing the command that fixes it. Make it part of CI with a task
that is one of the roots:

```rune
workflow-check:
    rune ci github --check --rune-version 0.11.0 gate

lint:
    @echo linting

[parallel]
gate: lint workflow-check
    @echo "CI passed"
```

A pull request that changes the Runefile and not the workflow then fails. See the
[ci-github example](../examples/ci-github/README.md).

## Not covered

- CI systems other than GitHub Actions, self-hosted runners and container jobs.
- Deployment: no protected environment, secret or write permission is ever emitted.
- Toolchain setup actions and dependency download caches.
- Editing a section of a hand-written workflow.

---

**See also:** [Caching](caching.md) · [Parallelism](parallelism.md) ·
[Dependencies & post-hooks](dependencies-and-hooks.md) · [CLI reference](../cli.md)
