# Feature Specification: Generate GitHub Actions Workflows from the Runefile

**Feature Branch**: `059-ci-github-workflow`

**Created**: 2026-11-25

**Status**: Draft

**Input**: GitHub issue [#62](https://github.com/rune-task-runner/rune/issues/62)
(backlog ADOPT-6): "Generate GitHub Actions workflows from the Runefile with `rune ci github`".
Related: receipts (#56), affected execution (#59), task verification (#60), runtime secret
providers (#63). None of those exists yet; see Out of scope.

## Overview

Teams write their task order, command bodies and cache settings twice: once in the
Runefile, once in the CI workflow. The two drift. This feature generates the GitHub
Actions workflow from the Runefile, so a person, CI and an agent run the same tasks the
same way.

```text
$ rune ci github --write lint test
.github/workflows/rune.yml: created: 4 jobs for 2 root tasks on linux

$ rune ci github --check lint test
.github/workflows/rune.yml: current
```

Each task a root reaches becomes one job. Its prerequisites become `needs:`. A job installs
a pinned Rune release and runs exactly one task with `rune ci run`, so the job runs the
same body the person runs locally, through the same engine, and never runs a prerequisite
again. Declared outputs travel to later jobs as artifacts. A `[cache]` task's cache key is
Rune's own fingerprint of that task.

| Question | Answer |
|---|---|
| Does generation run anything? | No. It reads the Runefile and writes text. No task body, no network, no secret |
| Which tasks become jobs? | Only the root tasks named on the command line and what they reach. Nothing is added on its own |
| Is the task's shell text copied into the workflow? | No. A job runs `rune ci run TASK ARGS`; the body stays in the Runefile |
| Does a prerequisite run twice? | No. One job per task and arguments; later jobs wait for it with `needs:` |
| Do jobs share files? | Only the outputs a task declares in `[cache(outputs=...)]`, passed as artifacts |
| Can a cache hit make a job pass? | Only on an exact key, built from Rune's fingerprint, with every declared output present |
| Is a hand-written workflow ever replaced? | No. Rune writes only a file it generated, and only with `--write` or `--output` |
| What can the workflow do? | Read the repository. It gets no secrets and no write permission, and runs no deployment |

### Out of scope

- CI systems other than GitHub Actions, and Windows runners. Linux and macOS runners only.
- Self-hosted runners, custom runner labels and container jobs.
- Deployment: tasks with `[confirm]` or `[applies]` are refused, and no protected
  environment, secret or write permission is ever emitted.
- Tasks run by an agent CLI (the `agent` executor): they need credentials.
- Toolchain setup (Go, Node, Python versions) and dependency download caches. Runner images
  provide common toolchains; a task can install what it needs.
- Receipts (#56), affected-only runs (#59), `[verify]` postconditions (#60) and runtime
  secret providers (#63). They do not exist yet. Because a job runs Rune's own engine, each
  applies to CI jobs when it lands, without a change to the generated text.
- Editing a section inside a hand-written workflow. Rune owns whole files only.
- Replacing Rune's own `.github/workflows/ci.yml`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Generate a workflow for chosen root tasks (Priority: P1)

A maintainer has a Runefile where `test` and `lint` both depend on `build`, which depends on
`generate`. They run `rune ci github lint test` and get a workflow with one job each for
`generate`, `build`, `lint` and `test`. `build` needs `generate`; `lint` and `test` need
`build`. The text is the same every time they run the command.

**Why this priority**: This is the feature: the workflow comes from the task graph.

**Independent Test**: Generate from a diamond fixture and compare with a golden file. Parse
the YAML and check jobs, `needs:` and that two runs give identical bytes.

**Acceptance Scenarios**:

1. **Given** the diamond Runefile, **When** the maintainer runs `rune ci github lint test`,
   **Then** the workflow is printed on standard output with four jobs, `build` appears once,
   and the summary on standard error names 4 jobs for 2 root tasks.
2. **Given** any Runefile, **When** `rune ci github` runs with no task, **Then** it fails
   with a usage error that asks for the root tasks and lists none by itself.
3. **Given** `build` takes a parameter and `lint` depends on `(build "release")` while
   `test` depends on `(build "debug")`, **When** the workflow is generated, **Then** there
   are two build jobs, each running its own argument.
4. **Given** a task from a module `api`, **When** `api::test` is a root, **Then** its job
   label reads `api::test` and its job ID is a valid, unique GitHub job ID.
5. **Given** the same Runefile and arguments, **When** the command runs twice, on any
   machine, **Then** the output is byte-identical.
6. **Given** no `--write` and no `--output`, **When** the command runs, **Then** no file is
   created or changed.

---

### User Story 2 - Each job runs exactly one task, as the local run would (Priority: P1)

In CI, the `test` job must not run `build` again: `build` already ran in its own job. The
job runs `rune ci run test`, which runs the body of `test` and nothing it depends on. When
`test` fails, its failure hooks run in that job, the job fails with the body's exit code, and
jobs that need `test` do not run.

**Why this priority**: Without a one-task execution mode, `needs:` and run-once cannot both
hold. This is the contract the issue asks for.

**Independent Test**: Run every generated job's `rune ci run` command in order, each in its
own copy of the project, and compare the tasks run and files produced with a local
`rune lint test`.

**Acceptance Scenarios**:

1. **Given** the diamond fixture, **When** every job is run in its own copy of the project
   in `needs:` order, **Then** each of the four bodies ran exactly once in total.
2. **Given** a task with `|| diagnose`, **When** its body fails in its job, **Then**
   `diagnose` runs in that job with the failed task's name and exit code, and the job exits
   with the body's code.
3. **Given** a task without `[parallel]` that depends on `a b c`, **When** the workflow is
   generated, **Then** `b` needs `a`, `c` needs `b`, and the task needs `c`, so a failure in
   `a` stops `b` and `c` as it does locally.
4. **Given** the same task with `[parallel]`, **When** the workflow is generated, **Then**
   `a`, `b` and `c` have no `needs:` between them and the task needs all three.
5. **Given** a task with a post-hook `&& notify`, **When** the workflow is generated,
   **Then** `notify` needs that task, and every job that depends on the task also needs
   `notify`.
6. **Given** `rune ci run` is interrupted, **When** the body stops, **Then** no failure hook
   runs and the exit code is 130, as for an interrupted local run.

---

### User Story 3 - The workflow is safe for pull requests (Priority: P1)

The generated workflow runs on pull requests, including those from forks. It must not give
PR code a secret, a write token or a deployment. Nothing from the Runefile or from an event
can inject an expression or a shell command.

**Why this priority**: A generator that is unsafe by default would put every user at risk.

**Independent Test**: Generate from fixtures with tricky names and arguments, then check the
permissions, triggers and escaping, and run a workflow linter on the result.

**Acceptance Scenarios**:

1. **Given** any generated workflow, **When** it is read, **Then** its only permission is
   `contents: read`, its triggers are `push` to the named branch and `pull_request`, it
   references no secret, and checkout does not keep credentials.
2. **Given** a root or prerequisite with `[confirm]` or `[applies]`, or run by the `agent`
   executor, **When** generation runs, **Then** it fails, names the task and the reason, and
   prints nothing on standard output.
3. **Given** an argument holding a single quote, a space or `$HOME`, **When** its job runs,
   **Then** the task receives the argument exactly as written.
4. **Given** an argument or path holding `${{`, a control character or a line break,
   **When** generation runs, **Then** it fails and names the task.
5. **Given** any generated workflow, **When** it is searched, **Then** no `run:` script
   contains a `${{ ... }}` expression and no `--yes` appears.

---

### User Story 4 - Outputs travel between jobs and cache keys come from the fingerprint (Priority: P2)

`build` declares `[cache(inputs=["src/**"], outputs=["dist/app"])]`. The `test` job needs
`dist/app`. On the next push with no change under `src/`, `build` is a cache hit and does not
run its body, but only when the restored entry is an exact match and `dist/app` is there.

**Why this priority**: Jobs that cannot see their prerequisites' outputs are useless for
build-then-test graphs, and a cache key that ignores the task definition gives false passes.

**Independent Test**: Simulate jobs with an in-memory artifact and cache store. Check what
downstream jobs receive, which keys change after each kind of edit, and that a bundle
without its outputs reruns the body.

**Acceptance Scenarios**:

1. **Given** `build` declares outputs and `test` depends on it, **When** the jobs run in
   separate copies, **Then** `dist/app` exists in the `test` copy before its body runs.
2. **Given** a task that does not depend on `build`, directly or through earlier jobs,
   **When** its job runs, **Then** it receives none of `build`'s outputs.
3. **Given** a `[cache]` task, **When** its job runs twice with nothing changed, **Then** the
   second run logs `cached: build` and does not run the body.
4. **Given** a `[cache]` task, **When** its body, an imported variable it uses, one of its
   input files, its executor, the runner OS or the Rune version changes, **Then** its key
   changes; **When** a comment or another task changes, **Then** its key does not.
5. **Given** a restored entry whose bundle lacks a declared output, **When** the job runs,
   **Then** the body runs.
6. **Given** any generated workflow, **When** it is read, **Then** no cache restore has
   `restore-keys`, and a cache is saved only after the task succeeded.

---

### User Story 5 - Write the workflow and detect drift in CI (Priority: P2)

The maintainer writes the workflow to the repository, and later adds a task that checks
it is current. When someone changes the Runefile and forgets to regenerate, the check fails
and prints the command that fixes it.

**Why this priority**: Generated files rot without a drift check, and a generator must never
overwrite a workflow someone wrote by hand.

**Independent Test**: Write, edit the Runefile, check, and try to write over a hand-written
workflow.

**Acceptance Scenarios**:

1. **Given** no workflow file, **When** `rune ci github --write lint test` runs, **Then**
   `.github/workflows/rune.yml` is created next to the Runefile and reported as created.
2. **Given** the file is current, **When** `--write` runs again, **Then** it reports
   unchanged and the file's bytes and modification time are unchanged.
3. **Given** the Runefile changed, **When** `rune ci github --check lint test` runs,
   **Then** it reports stale, prints the command that regenerates it, writes nothing and
   exits 1.
4. **Given** a hand-written workflow at the target path, **When** `--write` or `--check`
   runs, **Then** it is reported as not generated by Rune, nothing is written, and the exit
   code is 2.
5. **Given** `--output ci/rune.yml`, **When** the command runs, **Then** that path is
   written instead.

---

### User Story 6 - Graphs that CI cannot run faithfully are refused (Priority: P2)

Some graphs depend on things jobs cannot share: a prerequisite that starts a server for a
later task, an argument computed from the developer's environment, a failure hook that has
its own prerequisites. Generation refuses each with a message that names the task and says
why, instead of writing a workflow that behaves differently from a local run.

**Why this priority**: A workflow that looks right and runs differently is worse than none.

**Independent Test**: One fixture per refusal reason; each fails with exit 3, names the task,
and prints nothing on standard output.

**Acceptance Scenarios**:

1. **Given** a prerequisite whose shell body starts a background process with `&`,
   **When** a later task depends on it, **Then** generation fails and says that a later job
   cannot reach a process started in another job.
2. **Given** a dependency argument or a parameter default that reads the environment, a
   file, the clock or the machine's architecture, **When** generation runs, **Then** it fails
   and names the built-in and the task.
3. **Given** a failure hook with dependencies or post-hooks, **When** generation runs,
   **Then** it fails and names the hook.
4. **Given** a literal `[cache]` output that is absolute or leaves the project, **When**
   generation runs, **Then** it fails and names the output.
5. **Given** a root not available on the target OS, **When** generation runs, **Then** it
   fails as `rune` does for that task; a prerequisite not available there is left out, as
   the scheduler skips it.

---

### Edge Cases

- **A root that is also a prerequisite of another root**: one job, as run-once requires.
- **The same root named twice**: one job.
- **Two roots**: they are independent of each other, like the dependencies of a
  `[parallel]` task. A person who wants them ordered names one root that depends on both.
- **A task with an empty body**: it still gets a job, so a root that only groups others is
  a single status check that branch protection can require.
- **More than one `--os`**: each OS gets its own graph and jobs; job IDs end in the OS name.
- **A dependency cycle**: refused with the cycle path, as `rune` refuses it.
- **A post-hook target that is also a prerequisite of the same task**: it already ran; no
  second job and no extra `needs:`.
- **Job ID collisions after sanitizing** (`build:x` and `build-x`): the later one gets `-2`,
  in a stable order.
- **Very long arguments**: the job ID is shortened; the label keeps the whole invocation.
- **A Runefile with `minimum_version`**: the pinned Rune version must satisfy it.
- **A development build of Rune**: it cannot pin itself; `--rune-version` is required.
- **A variable override on the command line** (`NAME=value`): refused; jobs could not
  repeat it.
- **A task named `ci`**: `rune ci` runs the built-in command; the task runs with
  `rune -- ci`, and the built-in says so.
- **The Runefile is in a subdirectory of the repository**: jobs run Rune in that directory.
- **A `[cache]` task without outputs**: an exact hit skips the body, as it does locally.
- **Input globs that fail to expand in CI**: the key step fails the job rather than guess.
- **An artifact bundle with an absolute path, `..` or a link**: `rune ci run` refuses to
  unpack it and fails the job. A link already in the checkout cannot carry an entry outside
  the project either.
- **A declared output that holds a link** (for example `node_modules/.bin`): the job fails
  when the bundle is written and names the link; bundles carry regular files and
  directories only.
- **Interpreter tasks** (Python, Node): they run if the runner image has the interpreter;
  the documentation says so.

## Requirements *(mandatory)*

### Functional Requirements

**The command**

- **FR-001**: `rune ci github ROOT [ARG...] [ROOT [ARG...]]...` MUST build a GitHub Actions
  workflow from the analyzed Runefile, with its imports and modules, for the named root
  tasks and their arguments, read as `rune` reads tasks and arguments on its command line.
- **FR-002**: Without a root it MUST fail with a usage error. It MUST NOT choose roots
  itself.
- **FR-003**: It MUST NOT run any task body, read any secret, use the network, or read any
  file other than the Runefile, its imports and modules, and the target workflow file.
- **FR-004**: Without `--write`, `--output` or `--check` it MUST print the workflow on
  standard output and write no file. A summary (jobs, roots, OS) MUST go to standard error.
- **FR-005**: `--os OS` MUST select the runner OS: `linux` (runner `ubuntu-latest`, the
  default) or `macos` (runner `macos-latest`). It MAY be repeated. `windows` and any other
  value MUST be a usage error naming the supported values.
- **FR-006**: `--rune-version VERSION` MUST name the Rune release the jobs install. Without
  it, the version of the running Rune MUST be used when it is a release; a development build
  MUST require the flag. The version MUST satisfy the Runefile's `minimum_version`.
- **FR-007**: `--branch NAME` MUST name the branch whose pushes run the workflow, default
  `main`.
- **FR-008**: Variable overrides (`NAME=value`) MUST be refused with a usage error.

**The graph**

- **FR-009**: A node MUST be a task with its bound arguments, as the scheduler keys it for
  run-once. Every node a root reaches through dependencies and post-hooks on the target OS
  MUST become exactly one job.
- **FR-010**: Dependency arguments and parameter defaults MUST be evaluated as the scheduler
  evaluates them, with `os()` and `os_family()` giving the target OS. An argument or default
  that uses a built-in reading the environment, the file system, the clock, the PATH,
  random values or the machine's architecture MUST be refused, naming the built-in and the
  task.
- **FR-011**: A prerequisite not available on the target OS MUST be left out, as the
  scheduler skips it. A root not available there MUST be refused as `rune` refuses it.
- **FR-012**: A job's `needs:` MUST hold the jobs that must finish before Rune would start
  that task's body in a local run: its dependencies; for a task without `[parallel]`, each
  dependency also waits for the ones listed before it; for a post-hook, the task it follows
  and the post-hooks listed before it; and a task's dependents wait for its post-hooks.
  Roots MUST NOT wait for each other. A node reached again MUST reuse its job. Needs that
  another need already implies MAY be left out.
- **FR-013**: Dependency cycles MUST be refused as `rune` refuses them.
- **FR-014**: Generation MUST be refused, naming the task and reason, for a node that has
  `[confirm]` or `[applies]` or runs with the `agent` executor; for a failure hook with
  dependencies or post-hooks; for a node with dependents whose shell body starts a
  background process; and for a `[cache]` output written as a literal that is absolute or
  leaves the project. An output computed by an expression is checked when its bundle is
  written (FR-023).

**Jobs**

- **FR-015**: Job IDs MUST be built from the task name and arguments, contain only letters,
  digits, `-` and `_`, start with a letter, be at most 64 characters, and be unique, with
  `-2`, `-3` added in generation order on a clash. With more than one OS they MUST end in
  the OS name.
- **FR-016**: A job's name MUST be the task and its arguments as written on a `rune` command
  line, followed by the OS when there is more than one.
- **FR-017**: Every job MUST check out the repository without keeping credentials, install
  the pinned Rune release for the runner's OS and architecture and verify the archive
  against the release's published SHA-256 checksums before using it, then run the node with
  `rune ci run`.
- **FR-018**: The job MUST pass the arguments to `rune ci run` as single shell words that
  the shell does not expand. Every scalar from the Runefile MUST be quoted in the YAML.
  Text holding `${{`, a control character or a line break MUST be refused.
- **FR-019**: No `run:` script MUST contain a `${{ }}` expression. Values the runner
  provides MUST reach scripts as environment variables.
- **FR-020**: When the Runefile is not in the repository root, jobs MUST run Rune in its
  directory. The repository root MUST be the directory that holds the target's
  `.github/workflows`, or the Runefile's directory when the workflow is printed.

**Running one node: `rune ci run`**

- **FR-021**: `rune ci run TASK [ARG...]` MUST run the body of exactly that task with those
  arguments, through the same engine as `rune TASK`: `[cache]`, `[confirm]`, `[env]`,
  working directory, executors, secret masking and exit codes as for a local run. It MUST
  NOT run the task's dependencies or post-hooks.
- **FR-022**: When the body fails, its failure hooks MUST run in the same process with the
  failure context, as locally; when interrupted, none MUST run.
- **FR-023**: With `--dir DIR`, before the body it MUST unpack every bundle under `DIR/in`
  and the bundle `DIR/bundle.tar` when present into the project, refusing any entry that is
  absolute, holds `..`, is a link, is not a regular file or directory, or would be written
  outside the project through a link already in it. After a successful
  body of a `[cache]` task it MUST write `DIR/bundle.tar` holding the task's declared
  outputs and its cache record, and MUST fail when an output resolves outside the project or
  holds a link, naming it.
- **FR-024**: `rune ci run --key` MUST, after unpacking `DIR/in`, print one line
  `key=VALUE` for a `[cache]` task and run nothing. VALUE MUST be derived from Rune's cache
  fingerprint of the node (inputs, body, used variables, executor), the node's task and
  arguments, the Rune version, the OS and architecture, and the runner image name when the
  runner gives one. When the fingerprint cannot be computed it MUST fail.

**Outputs and caches**

- **FR-025**: A node whose task declares `[cache(outputs=...)]` MUST upload its bundle as an
  artifact, and every job that transitively needs it MUST download that artifact before its
  body. Artifacts MUST be kept 1 day.
- **FR-026**: A `[cache]` node's job MUST compute the key with `rune ci run --key`, restore
  the bundle by that exact key with no fallback keys, run the node, and save the bundle
  under that key only when the node succeeded and the restore was not an exact hit.
- **FR-027**: A restored bundle MUST let the body be skipped only when Rune's own cache
  decision says so: the stored fingerprint equals the current one and every declared output
  exists. Otherwise the body MUST run.
- **FR-028**: The workflow MUST NOT cache or upload anything but declared outputs and cache
  records, and MUST NOT pass any repository secret to any job.

**Workflow-level safety**

- **FR-029**: The workflow MUST set `permissions: contents: read` and no other permission.
- **FR-030**: It MUST trigger on `push` to the named branch and on `pull_request`, and on no
  trigger that runs pull request code with the base repository's secrets or write token.
- **FR-031**: It MUST NOT emit `--yes`, a deployment environment, or a reference to a
  secret.

**Writing and checking**

- **FR-032**: `--write` MUST write `.github/workflows/rune.yml` in the Runefile's directory;
  `--output PATH` MUST write PATH. A missing directory MUST be created. A current file MUST
  be left untouched and reported unchanged.
- **FR-033**: Rune MUST write or replace only a file whose first line marks it as generated
  by `rune ci github`. Any other existing file MUST be reported as not generated by Rune, and
  nothing written.
- **FR-034**: The generated file MUST begin with that marker line and a line with the exact
  command that regenerates it, including every option that changes the output.
- **FR-035**: `--check` MUST compare the target with what a write would produce and report
  current, stale, missing, or not generated by Rune. It MUST write nothing. When it is not
  current it MUST print the regeneration command.
- **FR-036**: The output MUST be byte-identical for the same Runefile files, arguments and
  options: no time, no absolute path, no host name, a stable order of jobs and keys.
- **FR-037**: Writes MUST be atomic.

**Command names and exit status**

- **FR-038**: `ci` MUST become a built-in command. A task named `ci` MUST run with
  `rune -- ci`, and `rune ci` MUST say so when the Runefile has such a task. `rune import`
  MUST treat `ci` as a rune command name.
- **FR-039**: `rune ci github` MUST exit 0 on success and when a check finds the file
  current; 1 when the file is stale or missing on a check, or a file cannot be written; 2 for
  usage errors and a target not generated by Rune; 3 when the Runefile is not valid or the
  graph cannot be represented. `rune ci run` MUST exit as `rune TASK` exits, and 1 when a
  bundle cannot be read or written.

**Documentation and validation**

- **FR-040**: The documentation MUST cover: choosing roots; the job and `needs:` contract,
  including the order of dependencies without `[parallel]`; what travels between jobs; cache
  keys and when a hit skips a body; runner and OS support; toolchain setup; GitHub's
  artifact and cache limits; each refused construct; the drift check; and a worked example
  whose committed workflow a test keeps current.
- **FR-041**: Every golden workflow in the test suite MUST pass a GitHub Actions workflow
  linter in CI.

### Key Entities

- **Root**: a task and its arguments named on the command line; what CI exists to run.
- **Node**: a task with bound arguments reached from a root on one OS; the unit of run-once.
- **Job**: the workflow entry for one node: ID, label, runner, needs, steps.
- **Bundle**: one archive per node holding its declared outputs and, for a `[cache]` task,
  its cache record; uploaded as an artifact and saved as a cache entry.
- **Cache key**: the exact key of a node's cached bundle, derived from Rune's fingerprint
  and the execution context.
- **Owned workflow file**: a file whose first line marks it as generated; the only kind Rune
  writes.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For the diamond fixture, simulating every job in its own copy of the project
  runs each task body exactly once, and the files the roots produce match a local run of the
  same roots.
- **SC-002**: Generating twice from each fixture gives byte-identical output, and every
  golden workflow passes the workflow linter with zero findings.
- **SC-003**: Across all fixtures, generation runs 0 task bodies: sentinel files a body
  would create never appear.
- **SC-004**: For each of 6 kinds of edit (body, imported variable, input file, executor,
  OS, Rune version) the node's cache key changes; for 2 unrelated edits it stays the same.
- **SC-005**: In 100% of simulated runs where a restored bundle lacks a declared output, the
  body runs.
- **SC-006**: Every generated workflow has exactly one permission (`contents: read`), 0
  secret references, 0 `restore-keys`, 0 `--yes`, and 0 expressions in `run:` scripts.
- **SC-007**: Every refusal fixture exits 3 with the task named and 0 bytes on standard
  output.
- **SC-008**: `--check` exits 1 for each stale fixture and 0 for each current one, and a
  write over a hand-written workflow leaves its bytes unchanged.

## Assumptions

- GitHub-hosted `ubuntu-latest` and `macos-latest` runners have `bash`, `curl`, `tar` and
  `shasum`, which the install step uses, as `scripts/install.sh` already does.
- Release archives are named `rune_VERSION_OS_ARCH.tar.gz` with a `checksums.txt` beside
  them, as GoReleaser publishes them today. The workflow checks the archive against that
  file; signature and provenance checks remain the manual steps in the installation guide.
- Actions are referenced by major version tag (`actions/checkout@v4`, `actions/cache@v4`,
  `actions/upload-artifact@v4`, `actions/download-artifact@v4`), as Rune's own workflows do.
- The person names the CI roots; Rune does not infer them from task names or attributes.
- A hit on a `[cache]` task skips its body in CI as it does locally (Principle I). A person
  who wants CI to always run a task leaves `[cache]` off it.
- Only declared outputs travel between jobs. Files a task writes without declaring them stay
  in its job; the documentation says to declare them.
- Reserving `ci` as a command name is a breaking change for a Runefile with a task of that
  name, handled as `agent`, `export` and `suggest` were.
