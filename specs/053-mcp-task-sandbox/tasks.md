---

description: "Task list for spec 053: confine the tasks an agent runs with an operating-system sandbox"
---

# Tasks: Confine the Tasks an Agent Runs with an Operating-System Sandbox

**Input**: Design documents from `/specs/053-mcp-task-sandbox/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and the issue's acceptance criteria ask for
them. A unit test is written and seen failing before the change that makes it pass. A
binary-level rule test is seen failing by mutation (T049).

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (a different file from the tasks beside it, no dependency on an
  incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US10)

## Conventions for every task

- Tests run in Docker only. Everything that needs no namespace:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Tests that start a real sandbox:
  `docker compose run --rm -e GOFLAGS=-buildvcs=false test-sandbox go test ./internal/sandbox/...`
  and `... test-sandbox go test ./test/integration/... -run Sandbox`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- A test that starts a real sandbox calls `requireSandbox(t)` first, defined in each test
  package that needs it: it skips when the probe fails, and fails when
  `RUNE_TEST_SANDBOX=require` is set.
- A test that starts a process with extra descriptors, runs a script as a program, asserts a
  file mode or compares slash paths built by `filepath` lives in a `_unix_test.go` file with
  the build tag `unix`. The Windows job runs `go test ./...` and must stay green.
- Fixtures of a rule test (the project, the marker directory, the home fixture, the audit
  directory) live under `sandboxFixtureDir(t)`: `RUNE_TEST_SANDBOX_DIR` when set, else
  `<module root>/.rune/sandbox-test`, never under `/tmp`, `/var/tmp` or `/run`. Each escape
  test first reads its marker from inside the body.
- A binary-level test of a rule is named `TestSandboxRule...` and starts its server through
  `ruleServer`, which starts the server unsandboxed when `RUNE_TEST_SANDBOX_MUTATE=off`.
- Every function added or changed gets a unit test of its own, with a case for each branch.
- "Nothing ran" and "nothing was written" are always checked from the outside: a marker file,
  or the bytes of a marker directory.
- The exact texts are in [contracts/sandbox-rules.md](contracts/sandbox-rules.md) and
  [contracts/operator-surface.md](contracts/operator-surface.md). Tests compare whole strings
  unless the task says otherwise.
- Every existing server test keeps testing what it tested: it runs with the sandbox off, set
  by the harness or by `Options`. No existing assertion is weakened. An assertion on the
  startup line or on the server's whole standard error gains the sandbox part and nothing
  else.
- An adapter or engine with no sandbox state must behave byte for byte as before.
- No test writes into a real home or cache directory.
- No em dash in code, comments or docs. Comments explain why, and stay few.

## Phase 1: Setup

- [X] T001 Create branch `053-mcp-task-sandbox` and `specs/053-mcp-task-sandbox/` with spec.md, plan.md, research.md, data-model.md, quickstart.md and contracts/
- [X] T002 [P] In `Dockerfile.test`, install the `bubblewrap` package (one `RUN apt-get update && apt-get install -y --no-install-recommends bubblewrap && rm -rf /var/lib/apt/lists/*`), with a comment that says why. In `docker-compose.yml`, add the service `test-sandbox`: the same build, volumes, working directory and environment as `test`, plus `privileged: true`, `RUNE_TEST_SANDBOX: "require"` and `RUNE_TEST_SANDBOX_DIR: /var/lib/rune-sandbox-test`, with a comment that a default container cannot create namespaces and that the fixtures must not be under `/tmp`. In the repo-root `Runefile`, add the task `test-sandbox` beside `test`, in the form of the existing `test` task, that runs the two commands of the conventions.
- [X] T003 [P] In `test/integration/harness_test.go` `TestMain`, set `RUNE_SANDBOX=off` next to `RUNE_AUDIT_DIR`, with a comment that the existing tests exercise the server unsandboxed and the sandbox tests unset the variable. Add the constants `sandboxOffPart` and `sandboxOffWarning` with the contract's two strings. `test/docs` starts no server and needs no change.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The package exists (R2 to R4, R7), a body can run in a worker process (R1, R5),
a server has a sandbox state (R8, R9), and `mcpAdapter.call` uses it. After this phase a call
on a confining host runs its body under bubblewrap, and a call on any other host is refused or
runs unsandboxed by the operator's choice.

### Tests first

- [X] T004 [P] Create `internal/sandbox/sandbox_test.go`. `TestParseMode`: `enforce` and `off` parse; `""`, `on`, `Enforce`, `auto`, ` off` return an error whose text is `"<value>" is not a mode; use enforce or off`. `TestDetect`: `Detect("linux")` returns a backend named `bubblewrap`; `Detect("darwin")` and `Detect("windows")` return a backend whose `Probe` fails with `this system (macos) has no sandbox backend` and `this system (windows) has no sandbox backend`, whose `Name` is `""`, and whose `Command` returns an error. Run and see it fail to compile.
- [X] T005 [P] Create `internal/sandbox/bwrap_unix_test.go` (tag `unix`) with `requireSandbox(t)`. `TestBwrapArgs`, table driven, compares the whole argument list of [contracts/sandbox-rules.md](contracts/sandbox-rules.md) for: a plain spec (no `--share-net`, no resolver bind); a `[network]` spec with `ResolvConf` set (both present, in the contract's position); `ResolvConf` set with `Network` false (no resolver bind); protected paths `ws/Runefile`, `ws/sub/deep/t.rune`, `ws/.git`, `ws/.rune` (ancestors `ws/sub` then `ws/sub/deep` bound once each before any protected `--ro-bind`, shallowest first); two protected files in one directory (the ancestor once); a protected file under a protected directory (neither the file nor an ancestor inside the directory appears); hidden directories and files outside the workspace (before the workspace bind) and inside it (after the protected binds), each sorted; `VarRunDir` (the `tmpfs` and its remount); every `tmpfs` has its `--remount-ro` at the end, after the binary's bind; a `ReadOnly` spec (the workspace with `--ro-bind`, no ancestor, protected or cache bind, `--tmpfs /tmp` with its remount, no binary bind when `Exe` is empty); the argv after `--` kept as given. `TestSandboxBwrapProbe` with `requireSandbox`: `Probe` of the real backend with the test binary and `-test.run=^$` as the probe command returns nil. `TestBwrapProbeMissing`: a lookup that finds nothing fails with `bubblewrap (bwrap) was not found on the PATH`. `TestBwrapProbeFails`: a program that is a script printing `bwrap: boom` on standard error and exiting 1 fails with `bubblewrap could not start a sandbox: bwrap: boom`; a first line of 300 bytes is cut to 200. `TestSandboxBwrapCommand` with `requireSandbox`, in a workspace under a fixture directory outside `/tmp`: the command runs `sh -c 'echo x > inside && ! echo x > /etc/rune-probe 2>/dev/null && ! echo x > /run/rune-probe 2>/dev/null'` with exit 0 and `inside` exists; with a `ReadOnly` spec `echo x > inside` fails. Run and see it fail to compile.
- [X] T006 [P] Create `internal/sandbox/paths_unix_test.go` (tag `unix`). `TestResolve`: an existing path comes back with its links followed; a path whose last two parts do not exist keeps them under the resolved parent. `TestWithin`: equal paths, a child, a sibling with a common prefix (`/a/bc` is not within `/a/b`), a parent, and `..`. `TestAncestors`: `Ancestors("/ws", "/ws/a/b/f")` is `/ws/a`, `/ws/a/b`; a direct child has none; a path outside has none. `TestCheckProtected`: a plain file passes; a file with a second hard link fails with `*LinkedError` holding the path and the count 2 when `links` is true, and passes when `links` is false; a path with a symbolic link in a directory part fails with `*SymlinkError` holding the path and the link; a path that is itself a symbolic link fails the same way; a missing path passes; a directory with a link count above one passes. `TestRepoRoot`: the nearest ancestor with a `.git` directory or file; the start directory itself; none found returns the start directory. Run and see it fail to compile.
- [X] T007 [P] Create `internal/cli/sandboxexec_test.go` (portable). `TestSandboxResultRoundTrip`, table driven over every result kind of [data-model.md](data-model.md): `resultOf(err)` then `errorOf(result, ctx)` gives an error of the same type with the same `Error()` text and, for `*shell.ExecError` and `*interp.ExecError`, the same `Code` and `Span`; nil gives nil; `context.Canceled` gives kind `cancelled` and comes back as the context's error. `TestSandboxWorkerShell`: `runSandboxWorker` with a job reader holding a shell job of two lines (`echo out`, `echo err >&2`) writes `out` and `err` to the given writers, and the result writer holds `started\n` then a result with kind `""`. `TestSandboxWorkerShellFailure`: `exit 7` gives kind `exec`, code 7, the line and the task. `TestSandboxWorkerBadJob`: bytes that are not JSON give an error and no `started`. `TestSandboxWorkerEnvAndDir`: the job's `env` and `dir` are what the body sees. `TestReadWorkerResult`: `started\n` plus one object parses; no `started` reports not started; `started` with nothing, with two objects, with garbage, and with more than 64 KiB each report a failed worker and read no more than the bound. `TestHintLine`: the two texts of the contract. In `internal/cli/sandboxexec_unix_test.go` (tag `unix`): `TestSandboxWorkerInterp` (command `["sh"]`, script `echo hi` prints `hi`; `["no-such-interpreter-rune"]` gives kind `missing-interpreter`). Run and see it fail to compile.
- [X] T008 [P] Create `internal/cli/sandbox_test.go` (portable) and `internal/cli/sandbox_unix_test.go` (tag `unix`). Portable: `TestSandboxModeOf` (flag nil and variable `""` give `enforce`; variable `off` gives `off`; flag `enforce` over variable `off` gives `enforce`; flag `maybe` fails with `--sandbox: "maybe" is not a mode; use enforce or off`; variable `maybe` fails with `RUNE_SANDBOX: "maybe" is not a mode; use enforce or off`; an explicit empty flag fails); `TestSandboxStateReady` with a stub backend (`Name` given, `Probe` returns a settable error and counts calls) and a test clock (mode `off` reports off and never probes; a probe that succeeds is not repeated; a probe that fails reports the reason, is not repeated within one second and is repeated after it; a later success flips the state; after `failed()` the next `ready` probes whatever the clock says; a backend path or a Rune binary inside the workspace or the private base, and a `PATH` directory under `/run`, each report the contract's reason without probing); `TestSandboxTexts` (`startupPart`, `startupWarning` and `refusal` give the contract's strings for the three states and every reason); `TestSandboxRecord` (the three JSON forms). Unix: the stub backend's `Command` records the spec and returns a command that runs the test binary as a worker (`TestMain` in `internal/cli/leak_test.go` calls `SandboxWorker` and exits when `RUNE_CLI_TEST_WORKER=1`, before the leak check is set up); `TestPrivateDirs` (`privateDirs` creates `<base>/cache` and a fresh `<base>/tmp/<random>` with mode 0700; the cleanup removes the temporary one and keeps the cache; a sibling older than 24 hours is removed and a newer one kept; a base inside the workspace fails; a base whose place is taken by a file fails; a state in mode `off` leaves the base uncreated); `TestHiddenLocations` (with a home fixture holding `.ssh/`, `.netrc` and `.config/gh/`, an audit base, `SSH_AUTH_SOCK` and `DOCKER_HOST=unix://...` pointing at existing files, and `DOCKER_HOST=tcp://x` ignored: the directories and the files are exactly the existing ones, classified, and nothing for entries that do not exist). Run and see it fail to compile.

### Implementation

- [X] T009 Create `internal/sandbox/sandbox.go`: the package comment (what the package is, that it knows nothing about tasks), `type Mode string` with `ModeEnforce` and `ModeOff`, `ParseMode`, `type Spec` with the fields of [data-model.md](data-model.md), `type Backend interface` (`Name() string`, `Path() string`, `Probe(ctx) error`, `Command(ctx, Spec, argv) (*exec.Cmd, error)`), `Detect(goos string) Backend`, and the `unsupported` backend whose reason writes darwin as `macos`. Makes T004 pass.
- [X] T010 Create `internal/sandbox/paths.go`, `links_unix.go` (tag `unix`) and `links_other.go`: `Resolve`, `Within`, `Ancestors`, `RepoRoot`, `CheckProtected(workspace, path string, links bool) error` with `*LinkedError` and `*SymlinkError`, and `linkCount(os.FileInfo) uint64` (1 on other systems). Makes T006 pass.
- [X] T011 Create `internal/sandbox/bwrap.go`: `BwrapArgs(spec Spec, argv []string) []string` as a pure function in the contract's order, and the `bubblewrap` backend with an injectable lookup function and probe command. `Path` resolves the program to an absolute path with its links followed, or `""`. `Probe` resolves the program, builds a spec over a temporary directory it removes afterwards, runs the probe command under a 10 second timeout and maps a failure to the two reasons of R7 (first non-empty line of standard error, cut to 200 bytes). `Command` returns `exec.CommandContext` on the resolved path, and an error when the program was not found. Makes T005 pass.
- [X] T012 Create `internal/cli/sandboxexec.go`, worker side: `sandboxJob`, `sandboxResult`, `resultOf(error) sandboxResult`, `errorOf(sandboxResult, ctx) error`, `readWorkerResult(io.Reader) (result sandboxResult, started bool, err error)` with the 64 KiB bound, `hintLine(task string, network bool, workspace string) string`, and `runSandboxWorker(ctx, job io.Reader, result io.Writer, stdin io.Reader, stdout, stderr io.Writer) error`, which decodes the job, writes `started\n`, runs `shell.Run`, `interp.Run` or `agent.CLIProvider{}.Run` (printing the final text as `executeAgent` does) and writes the result. Add the exported `SandboxWorker(ctx) int`, which calls `workerPipes()` and then `runSandboxWorker` with the process streams, and prints `rune: --sandbox-worker is started by rune itself` with code 2 when there are no pipes. `workerPipes() (job, result *os.File, err error)` and `closeInheritedOnExec()` (marks every descriptor from 3 up close-on-exec, from `/proc/self/fd`) live in `internal/cli/sandboxexec_unix.go` (tag `unix`): `workerPipes` takes descriptors 3 and 4, requires both to be pipes (`fstat`) and marks both close-on-exec. `internal/cli/sandboxexec_other.go` returns an error and does nothing, so the package builds on Windows. Add `TestCloseInheritedOnExec` to `internal/cli/sandboxexec_unix_test.go`: a pipe opened with the flag cleared has it set afterwards. Makes T007 pass.
- [X] T013 In `cmd/rune/main.go`, add `internalCommand(ctx, args) (code int, handled bool)` and call it in `run` before the root command is built: `--sandbox-probe` as the only argument is handled with 0, `--sandbox-worker` as the only argument is handled with `cli.SandboxWorker(ctx)`, and anything else is not handled. Add `TestInternalCommand` to `cmd/rune/root_test.go`: the probe is `(0, true)`; `["build"]`, `[]` and `["--sandbox-probe", "x"]` are not handled. The worker run by hand is tested on the built binary in T019.
- [X] T014 Create `internal/cli/sandbox.go`: `sandboxModeOf(flag *string, env string) (sandbox.Mode, error)`; `sandboxState` with the fields of [data-model.md](data-model.md); `newSandboxState(mode, backend, exe, workspace, base, auditBase, home string, getenv func(string) string, now func() time.Time)`; `ready(ctx) error` with the checks of R7 and the one-second re-probe; `failed()`; `startupPart()`, `startupWarning()`, `refusal(task string, err error) string`, `record() audit.Sandbox`; `privateDirs() (tmp, cache string, cleanup func(), err error)`; `hidden() (dirs, files []string)`; `gitCommand(ctx, dir string, args ...string) *exec.Cmd` (the read-only spec of R20 around `git -C dir ...` when confining, a plain command otherwise); and `sandboxBase(runefile string) (string, error)` from `os.UserCacheDir` and `audit.ProjectID`. Building the state and asking it in mode `off` create nothing on disk. Add `Options.MCPSandbox *string` and `Options.SandboxEnv string` in `internal/cli/dispatch.go` with comments in the style of their neighbours. Extend `internal/audit/record.go` `Sandbox` with `Backend string` and `Mode string`, both `omitempty`, and add a case with the two fields to `internal/audit/record_test.go`. Makes T008 pass.
- [X] T015 In `internal/cli/audit.go` and `internal/cli/planapply.go`, give `gitState` and `gitInfo` a command builder: add `gitStateWith(ctx, dir, command)` and `gitInfoWith(ctx, dir, command, ignore...)` where `command` has the shape of `sandboxState.gitCommand`, and keep `gitState` and `gitInfo` as the same functions with a plain `git -C dir` command. `mcpAdapter.AuditIdentity` and `engine.git` use the state's builder when the adapter or engine has a state. Unit tests in `internal/cli/sandbox_unix_test.go`: `TestGitStateUsesBuilder` and `TestGitInfoUsesBuilder` (a builder that records its arguments and runs real git in a repository made by the test gives the same answers as the plain functions, and is called for every git command; with a state in mode `off` the builder returns a plain command).
- [X] T016 In `internal/cli/sandboxexec.go`, launcher side: `(e *engine) runConfined(task, sel, lines, dir, env, stdout) error`. It checks that `dir` resolves within the workspace, makes the private directories, builds the `Spec` (workspace, protected set, hidden directories and files, `Network` from the task's own `[network]`, the binary), starts the backend's command with `ExtraFiles` of a job pipe and a result pipe, the server's environment with `TMPDIR=/tmp`, the engine's standard input, `stdout` and standard error, `WaitDelay` of 5 seconds, writes the job, reads the result with `readWorkerResult`, and returns `errorOf`; `*sandboxStartError` (wrapping `scheduler.ErrBodyNotRun`, the contract's text) after calling `state.failed()` when `started` never came; the context's error when the call was cancelled or timed out; or the contract's failed-worker error. Add `type bodySandbox struct{ state *sandboxState; workspace string; protected []string }` and the field `engine.sandbox *bodySandbox` in `internal/cli/run.go`. In `runBodyTo`, move the executor switch into `(e *engine) runExecutor(...)` and call `runConfined` instead when `e.sandbox != nil`; print a `*sandboxStartError` as an error banner on the engine's standard error and return it as a silent `TaskFailure` that keeps `ErrBodyNotRun`; after any other failure of a confined body print `hintLine`. Tests in `internal/cli/sandboxexec_unix_test.go` with the stub backend: `TestRunConfinedShell` (output arrives; the spec the backend got has the task's network flag, the workspace and the binary; the worker's environment has `TMPDIR=/tmp`), `TestRunConfinedFailure` (a body that exits 3 gives a `*shell.ExecError` with code 3, the engine's exit code equals the unconfined one, and the hint line is on standard error), `TestRunConfinedSuccessHasNoHint`, `TestRunConfinedStartFailure` (a backend command that exits 1 without `started` gives `*sandboxStartError`, `errors.Is(err, scheduler.ErrBodyNotRun)`, the banner text on standard error, no `||` hook, and the state's next `ready` probes again), `TestRunConfinedCancel` (cancelling returns the context's error and removes the temporary directory; a passed deadline returns `context.DeadlineExceeded`), `TestRunConfinedDirOutside` (fails before the backend is asked), `TestRunConfinedWorkerDies` (a worker that prints `started` and exits gives the failed-worker error), and `TestRunBodyUnconfined` (an engine with no sandbox calls no backend).
- [X] T017 In `internal/cli/mcp.go`: add `mcpAdapter.sandbox *sandboxState`, `mcpAdapter.loaded []string` (the files the load read) and `mcpAdapter.missing []string` (the files it looked for and did not find); refactor `reach` so that a new method `reachTasks(t *ast.Task) []*ast.Task` returns the called task and every task its run can reach, in the order `reach` visits them, with `reach` built on the same walk and unchanged results (every existing `reach` test stays green; add `TestReachTasks` in `internal/cli/mcp_test.go` for a dependency, an `&&` hook, a `||` hook, a hook of a hook that is not chained, and a task for another OS). In `call`, after the `[agent-allow]` check and before the plan lookup: when `a.sandbox` is set and its mode is `enforce`, run `a.sandboxPreflight(ctx, t)`; on failure record `mcpserver.AuditKindSandbox` and return the refusal; on success set `eng.sandbox` and append `TMPDIR=/tmp` and `XDG_CACHE_HOME=<cache>` to `eng.env`. `sandboxPreflight` checks, in the order of R10: `state.ready`; the private base; the directory of every task of `reachTasks`; each path of `a.missing` inside the workspace; `sandbox.CheckProtected` for every path of `a.protectedPaths()`, creating `<workspace>/.rune` first. `protectedPaths` places each loaded path under the resolved workspace as [data-model.md](data-model.md) says, so a project reached through a symbolic link keeps its protected set. Add `mcpserver.AuditKindSandbox = "sandbox"` in `mcpserver/audit.go`, map it to `AuditRefused` in `CallAudit.end` beside `AuditKindPolicy`, and add its case to the kind test in `mcpserver/audit_test.go`. In `internal/cli/mcpreload.go`, `pathRecorder` gains `found() []string` and `notFound() []string` (by whether the source provider returned the file); in `internal/cli/serve.go`, `loadMCPAdapter` stores them in `adapter.loaded` and `adapter.missing`. Tests in `internal/cli/sandbox_unix_test.go`: `TestCallRefusedUnavailable` (stub probe fails: the contract's text, no marker, the recorded kind is `sandbox`), `TestCallConfined` (the body ran through the backend and the result is the body's), `TestCallOffRunsInProcess`, `TestCallNoStateRunsInProcess`, `TestCallRefusedDirOutside` (direct and through a dependency), `TestCallRefusedLinked`, `TestCallRefusedSymlink`, `TestCallRefusedMissingFile` (an optional import that is absent, and a module found at `foo/Runefile`; an explicit module path and a missing dotenv file are not refused), `TestCallRefusedPrivateDir`, `TestCallCreatesRuneDir`, `TestProtectedSet` (a Runefile, an import in a subdirectory, an import outside the workspace, a dotenv file that exists and one that does not, a `.git` directory, a `.git` file and no `.git`: exactly the existing paths inside the workspace plus `.rune`, sorted, no duplicate), `TestProtectedSetThroughSymlink` (the adapter was loaded through a link to the project directory: the set holds the resolved Runefile and import), `TestPathRecorderFoundAndMissing`, and `TestCallEnvHasPrivateDirs` (the body sees `TMPDIR` and `XDG_CACHE_HOME`, and a task's `[env("TMPDIR", "x")]` wins).

**Checkpoint**: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/sandbox/... ./internal/cli/... ./mcpserver/... ./cmd/rune/...` passes.

---

## Phase 3: User Story 4 - No call runs unconfined without the operator saying so (Priority: P1)

**Goal**: The mode reaches a server, the state is built and probed at start, and the operator
sees it. This comes first among the stories because every other story's binary-level test
needs a server that confines.

**Independent Test**: quickstart N, O, P, Q, W.

- [X] T018 [P] [US4] In `internal/cli/serve_test.go`, add `TestStartupLineSandbox` (`startupLine` with each of the three parts gives the three example lines of [contracts/operator-surface.md](contracts/operator-surface.md), and the part comes last) and `TestServeMCPSandboxMode` (`ServeMCP` with `MCPSandbox` set to `maybe`, and with `SandboxEnv` `maybe`, returns a `*UsageError` with the contract's text and runs no `[context]` task, checked with a marker). Update the existing startup-line expectations in `internal/cli/serve_test.go` and `internal/cli/mcpreload_test.go` to carry the sandbox part, and pass the mode `off` through `Options` in every existing in-process `ServeMCP` test of `internal/cli` and `cmd/rune/serve_test.go`. Run and see the new tests fail.
- [X] T019 [P] [US4] Create `test/integration/sandbox_mcp_test.go` with the helpers `requireSandbox(t)`, `sandboxFixtureDir(t)`, `sandboxServer(t, dir, env, flags...)` (starts `rune mcp --color=never` with `RUNE_SANDBOX` removed from the environment unless given and `XDG_CACHE_HOME` set to a directory of the test, returns the SDK client session and the server's standard error), `ruleServer` (the same, unsandboxed under `RUNE_TEST_SANDBOX_MUTATE=off`), `noBackendEnv(t)` (a `PATH` that holds one directory with a link to `sh` only), and the tests: `TestSandboxUnavailableRefuses` (N: startup warning and line, tools listed, the call's error is the contract's text and arrives in under one second, no marker, audit outcome `refused`, kind `sandbox`, sandbox object `{"enforced":false,"mode":"enforce"}`; on a system that is not Linux it runs without `noBackendEnv` and expects the unsupported text), `TestSandboxUnavailableContext` (O), `TestSandboxOff` (P, with the flag and with the variable: warning, line, the task writes outside the project, audit `{"enforced":false,"mode":"off"}`; and a server whose Runefile has `set dotenv` with `RUNE_SANDBOX=off` in the file stays in mode `enforce`), `TestSandboxBadMode` (Q: exit 2, the contract's text, no marker from the `[context]` task), `TestSandboxCannotStart` (runs only when `bwrap` is on the `PATH` and cannot start a sandbox, as in the default test container: the refusal carries the "could not start a sandbox" reason), `TestSandboxBackendInstalledLater` (with `requireSandbox`: the server starts with a `PATH` directory that lacks `bwrap`, a call is refused, the test links `bwrap` into that directory, waits over one second, and the next call runs confined with no restart and its record says enforced), `TestSandboxWorkerByHand` (`rune --sandbox-worker` exits 2 with the contract's text; `rune --sandbox-probe` exits 0 and prints nothing) and `TestSandboxCommandLineIgnoresMode` (W: `rune write-outside` on the command line with `RUNE_SANDBOX` set to `enforce`, `off` and `maybe` writes its marker outside the project and exits 0 each time). Run and see them fail.
- [X] T020 [US4] Update the existing binary-level expectations that T003 moves: in `test/integration/http_bind_warning_test.go`, `allowlist_mcp_test.go`, `reload_mcp_test.go`, `approval_mcp_test.go`, `file_flag_mcp_test.go`, `destructive_mcp_test.go`, `status_styling_test.go` and `clients_fixture_test.go`, every expected startup line gains `sandboxOffPart` in the contract's position, and every assertion on the server's whole standard error, or on the absence of a warning, accounts for the one `sandboxOffWarning` line. No assertion is removed.
- [X] T021 [US4] In `cmd/rune/root.go`, read `RUNE_SANDBOX` into `opts.SandboxEnv` beside the audit variables. In `cmd/rune/serve.go`, add `--sandbox` with the contract's help, set `opts.MCPSandbox` through `givenFlag`, and add a paragraph and an example to the command's long text. Extend `cmd/rune/serve_test.go` (`TestServeSandboxFlag`: the flag reaches `Options`, an absent flag leaves it nil, and the last of two flags wins) and `cmd/rune/root_test.go` (the variable reaches `Options`).
- [X] T022 [US4] In `internal/cli/serve.go` `ServeMCP`: parse the mode with `sandboxModeOf` before the Runefile is resolved; after the audit log is open, build the state (`sandbox.Detect(runtime.GOOS)`, the resolved `os.Executable`, the resolved workspace, `sandboxBase`, the audit base, the home directory, `os.Getenv`), call `closeInheritedOnExec()` in mode `enforce`, call `ready` once, print `startupWarning()` when it is not empty, give the state to the reloader, and add `startupPart()` to `startupLine` (a new last parameter). In `internal/cli/mcpreload.go`, `reloader` holds the state and `configure` sets `a.sandbox`. Makes T018 pass and T019 pass on a host without a backend.

**Checkpoint**: N, O, P, Q and W pass in the default `test` service.

---

## Phase 4: User Story 1 - A task has no network unless it is marked `[network]` (Priority: P1)

**Goal**: The network rule holds for a real body.

**Independent Test**: quickstart A, B, C.

- [X] T023 [P] [US1] Create `test/integration/testdata/sandboxprobe/main.go`: one `main` with the subcommands of [quickstart.md](quickstart.md), standard library only, each printing `ok` or the error and exiting 0 or 1. `connect` and `unix` use a 2 second timeout; `udp` sends one datagram and waits 1 second for the echo; `daemon FILE` starts itself again in a new session (`Setsid`) as a process that writes the time to FILE every 100 ms, and returns; `spawn N` starts N sleeping children and returns; `fds` prints the sorted entries of `/proc/self/fd` that are not its own directory handle; `status` prints the `CapEff`, `CapBnd` and `NoNewPrivs` lines of `/proc/self/status` and the user id; `agent ADDR` is the stand-in agent of the quickstart. The file has the build tag `unix`. In `test/integration/harness_test.go`, build it once into the build directory with `CGO_ENABLED=0` on Unix and add `installProbe(t, dir)` that copies it into a project as `probe`.
- [X] T024 [US1] In `internal/cli/sandboxexec.go`, add `resolvConf(resolve func(string) (string, error)) string`: the resolved path of `/etc/resolv.conf` when it lies under `/run`, else `""`; `runConfined` sets `Spec.ResolvConf` from it for a `[network]` task. Unit test `TestResolvConf` in `internal/cli/sandboxexec_test.go` with a fake resolver for: under `/run`, elsewhere, and an error.
- [X] T025 [US1] In `test/integration/sandbox_mcp_test.go`, add `sandboxProject(t, runefile)` (under `sandboxFixtureDir`: a project directory with the probe, a marker directory beside it with one file, and a TCP and a UDP echo listener on `127.0.0.1` that count what they received) and the tests `TestSandboxRulePlainHasNoNetwork` (A: `connect`, `udp` and `resolve example.com` all fail and both listeners counted zero, with `HTTP_PROXY` set in the server's environment), `TestSandboxNetworkTaskConnects` (B: `connect` and `udp` succeed, and `/etc/resolv.conf` is readable with content when the host has one) and `TestSandboxPlainOwnLoopback` (C). Run in `test-sandbox`; if one fails, fix `internal/sandbox/bwrap.go` and add the missing case to `TestBwrapArgs`.

**Checkpoint**: A, B and C pass in `test-sandbox`.

---

## Phase 5: User Story 2 - A task writes only inside the project (Priority: P1)

**Goal**: Writes outside fail, private directories work, and a directory outside is refused.

**Independent Test**: quickstart D, J, Y and the first case of R.

- [X] T026 [US2] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleWritesStayInside` (D, as subtests for a plain and a `[network]` task: `probe read` of the marker file succeeds; the inside file exists; `probe write` to the marker directory by absolute path, to `../outside/x`, through a symbolic link and through `/proc/self/root/<marker>/x` fail; `probe link` from the marker file fails; a body that reads standard input gets end of file at once; the marker directory is unchanged, compared with `treeDigest(t, dir)`), `TestSandboxPrivateDirs` (J: the first call writes `$TMPDIR/a` and `$XDG_CACHE_HOME/b` and prints both paths; the second finds `b` and not `a`; neither path is inside the project), `TestSandboxRefusesOutsideWorkdir` (R: a task with `[working-directory("/")]`, called directly and through a task that depends on it, is refused with the contract's text and no marker), `TestSandboxRefusesPrivateDir` (R: `XDG_CACHE_HOME` names a file) and `TestSandboxHintLine` (Y: a plain and a `[network]` body that exit 3 end their standard error with the contract's line; a successful body has none). Run in `test-sandbox`; fix what fails with a unit test for the fix.

**Checkpoint**: D, J, Y and R (directory) pass in `test-sandbox`.

---

## Phase 6: User Story 3 - A task cannot change what decides its own rights (Priority: P1)

**Goal**: The protected set holds against write, rename and replace, missing files are
refused, and Rune's own git cannot be turned into an escape.

**Independent Test**: quickstart F, X and the link and missing-file cases of R.

- [X] T027 [US3] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleProtectedPaths` (F: in a git repository made with `git init`, with a stored plan from a `[plan]` task, a `set dotenv` file and an import from `sub/tasks.rune` given by an explicit path, a task tries each step of F with `probe` and shell redirection, and `probe link Runefile copy`; every step fails; `treeDigest` of the Runefile, `sub/tasks.rune`, the dotenv file, `.git` and `.rune/plans` is unchanged; `git status` inside a task exits 0; the audit directory, placed under the fixture directory, lists as empty and a write into it fails; writes to `/run/x` and `/var/tmp/x` fail; the server's standard error has no reload line; the whole test runs a second time with the server started in a symbolic link to the project directory, and once with `--file` through that link), `TestSandboxRefusesLinkedRunefile`, `TestSandboxRefusesSymlinkedImport` and `TestSandboxRefusesMissingFile` (R: an absent optional import, and a module in `foo/Runefile` with no explicit path; the contract's texts, nothing ran), and `TestSandboxRuleGitCannotEscape` (X: a project with no `.git`; the first call's task writes a `.git` directory with a valid `HEAD`, an `objects` and a `refs` directory and a `config` whose `filter.x.clean` is a command that writes a marker beside the project, plus `.gitattributes` with `* filter=x` and a file to filter; a second call follows; the marker does not exist, and the second call's audit record has a git state). Run in `test-sandbox`; fix what fails with a unit test for the fix.

**Checkpoint**: F, X and R pass in `test-sandbox`.

---

## Phase 7: User Story 5 - Every way a task runs code is confined, each task with its own rights (Priority: P1)

**Goal**: The five ways to run code and the per-task network rule are proven on the binary,
and what a call did before still works when it is confined.

**Independent Test**: quickstart E, G, H, T, U, V and the second half of M.

- [X] T028 [US5] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleEveryExecutor` (E: five tasks, each a subtest: redirection `echo x > <marker>/f`; built-in `cd / && echo x > rune-probe-f`; `[script("sh")]`; a Runefile with `set shell := ["sh", "-eu"]`; a child `sh -c`; all fail and the marker directory is unchanged), `TestSandboxRuleNetworkIsPerTask` (G: a plain task that depends on a `[network]` task whose body connects, where the plain body then fails to connect; a `[network]` task with a plain `&&` hook; a failing `[network]` task with a plain `||` hook; the listener's count equals the number of `[network]` bodies), `TestSandboxRuleModuleTask` (a task of an imported module cannot write outside), `TestSandboxRuleConcurrentCalls` (H: 20 calls at once from one session, half to a `[network]` task and half to a plain one, each connecting: 10 `ok`, 10 failures, and the listener counted 10) and `TestSandboxRuleNestedRune` (M, second half: a plain body runs `<rune binary> fetch` for a `[network]` task that connects; the inner task fails to connect). Run in `test-sandbox`; fix what fails with a unit test for the fix.
- [X] T029 [US5] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleOverHTTP` (T: `rune serve --http` with a token file; a plain task fails to connect to the test listener and to the server's own HTTP address, `probe fds` prints `0 1 2`, and after the call the same session lists tools and makes a second call), `TestSandboxKeepsRunesOwnWork` (U: a `[cache]` task runs once and is reported cached on the second call; a `[plan]` task stores its plan; with a client that approves, the `[applies]` task reads `$RUNE_PLAN_FILE` and the plan ends applied; the audit log holds the end record of each call; a Runefile saved while a confined call sleeps is reloaded and the next call's body cannot write the new file) and `TestSandboxKeepsCallSemantics` (V: for a confined call, a secret from the environment is masked in the output; output over `--output-limit 1KiB` is cut with the truncation line; a failing task returns its exit code in the text and the structured result and its `||` hook's output as the fix suggestion; a `[confirm]` task is refused without a grant and runs with `--allow-destructive`; an `[agent-allow]` refusal has its text unchanged; a tool argument named `sandbox` with the value `off` is refused as an unknown argument). Run in `test-sandbox`; fix what fails with a unit test for the fix.
- [X] T030 [US5] In `internal/cli/sandboxexec_unix_test.go`, add `TestRunConfinedAgent` (with the stub backend and `agent_cmd` set to a script that prints its last argument, a confined `(agent)` body sends a job of kind `agent`, the final text reaches standard output, and the script sees no `RUNE_MCP_URL`) and `TestRunConfinedAgentErrors` (no `agent_cmd` gives `*agent.NotConfiguredError`, a missing program gives `*agent.NotInstalledError`, an exit status gives `*agent.AuthError`). Make them pass in `runConfined` and the worker if they do not.

**Checkpoint**: E, G, H, T, U, V and nested Rune pass in `test-sandbox`.

---

## Phase 8: User Story 6 - Sockets, agents and devices of the host are out of reach (Priority: P2)

**Goal**: The hidden locations are hidden for a real body, and a body holds nothing of the
server.

**Independent Test**: quickstart I and Z.

- [X] T031 [US6] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleHidesHostSockets` (I): Unix listeners at `<runtime dir>/rune-probe.sock` when `$XDG_RUNTIME_DIR` is under `/run` and writable (else that listener is left out and the subtest says so), at `/tmp/rune-probe-<random>/s.sock`, at `<home fixture>/agent/ssh.sock` (named by `SSH_AUTH_SOCK`) and at `<home fixture>/docker/docker.sock` (named by `DOCKER_HOST=unix://...`); `<home fixture>/.ssh/id_test` and `<home fixture>/.netrc` with content; the server started with `HOME` set to the fixture. A plain and a `[network]` task run `probe unix` on each path (all fail), `probe abstract` on a listener in the abstract namespace (fails for the plain task), `probe ls /dev` (exactly the contract's set), `probe ls /proc` (no process id above 50), `probe ls <home>/.ssh` (empty), `probe write <home>/.ssh/x` (fails) and read `.netrc` (empty). A second run puts the Runefile in the home fixture itself: `.ssh` is still empty and not writable. Add `TestSandboxRuleNoInheritedFiles` (Z: the server is started with three extra open files through `ExtraFiles`; `probe fds` prints `0 1 2`), `TestSandboxRuleNoTerminal` (the server is started on a pseudo-terminal with `github.com/creack/pty`, as `confirm_pty_test.go` does; `probe tty` fails), `TestSandboxRuleNoCapabilities` (`probe status` shows `CapEff` and `CapBnd` of zero, `NoNewPrivs: 1` and the server's user id, also when the test runs as root) and `TestSandboxRuleSecondMount` (only when the test runs as root and can `mount --bind` the project at a second path under the fixture directory, else skipped with the reason: a write through the second path fails). Run in `test-sandbox`; fix what fails in `sandboxState.hidden` or `BwrapArgs` with a unit test for the fix.

**Checkpoint**: I and Z pass in `test-sandbox`.

---

## Phase 9: User Story 7 - Cancelling a call stops everything it started (Priority: P2)

**Goal**: No process outlives its body or its call.

**Independent Test**: quickstart K, L.

- [X] T032 [US7] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleCancelStopsTree` (K: a task runs `probe daemon hb`, `probe spawn 200` and `probe sleep 30`; the test waits until `hb` changes, cancels the call's context, and checks that `hb` stops changing within 2 seconds, that a second call answers, and that the audit outcome of the first is `cancelled`), `TestSandboxRuleBackgroundEndsWithBody` (L: the body ends right after `probe daemon hb`; `hb` stops changing) and `TestSandboxRuleServerExitStopsTree` (the server process is killed during K's task; `hb` stops changing within 2 seconds). Run in `test-sandbox`; fix what fails in `runConfined` with a unit test for the fix.

**Checkpoint**: K and L pass in `test-sandbox`.

---

## Phase 10: User Story 8 - The record of a call says how it was confined (Priority: P2)

**Goal**: The audit record and `rune log` state the mode and the backend.

**Independent Test**: the audit assertions of quickstart N and P, and a confined call.

- [X] T033 [P] [US8] In `internal/cli/audit_test.go`, add `TestAuditorSandboxState`: an auditor with a state function writes the function's value in the start record for confining, unavailable and off, the function is called once per call, and an auditor with none writes `{"enforced":false}` as before. In `internal/cli/log_test.go`, add `TestLogUnsandboxedLine`: an action with mode `off` and an exit code gets the contract's detail line; one with mode `off` and no exit code (a refusal) gets none; mode `enforce` gets none; no mode (an old record) gets none; a refused action of kind `sandbox` shows its message once. Run and see them fail.
- [X] T034 [US8] In `internal/cli/audit.go`, give `auditor` the field `sandbox func(context.Context) audit.Sandbox` and use it in `CallStarted`; `ServeMCP` sets it to a function that calls the state's `ready` and returns `record()`. In `internal/cli/log.go` `rowOf`, add the detail line. Makes T033 pass.
- [X] T035 [US8] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxAuditRecord`: after a confined call, `rune log --json` holds `"sandbox":{"enforced":true,"backend":"bubblewrap","mode":"enforce"}` and `rune log` has no extra line under it; a call killed with the server mid-way reads as `incomplete` with the same sandbox object. Extend `TestSandboxOff` (T019) to check the `rune log` line. Update the two existing assertions on `"sandbox":{"enforced":false}` in `test/integration/audit_log_mcp_test.go` to the off form, which is what those servers now record.

**Checkpoint**: the audit assertions pass in both services.

---

## Phase 11: User Story 9 - Hooks and agent tasks follow the same rule (Priority: P2)

**Goal**: The `[context]` task and the endpoint of an agent task are confined.

**Independent Test**: the first half of quickstart M, and S.

- [X] T036 [P] [US9] In `internal/cli/sandbox_unix_test.go`, add `TestAgentTaskSandbox`: `executeAgent` on an engine whose options hold `SandboxEnv` `off` gives the endpoint's adapter a state that is off and prints the off warning once before the `[context]` task runs; with `""` and a stub backend that is unavailable it prints the unavailable warning, the `[context]` task does not run and a call through the endpoint is refused; with `maybe` it returns the variable's usage error before the agent program starts (a marker the stand-in agent would write is absent); the adapter's `loaded` and `missing` hold the files of the engine's load; the endpoint's auditor records the state. `TestNewAgentAdapterHasNoState`: an adapter from `newAgentAdapter` alone runs in process, as before. Run and see them fail.
- [X] T037 [US9] In `internal/cli/run.go` `execute`, record the files of the load with a `pathRecorder` and keep them on the engine (`engine.loaded`, `engine.missing`). In `internal/cli/agentexec.go`: `executeAgent` reads the mode with `sandboxModeOf(nil, e.opts.SandboxEnv)` first and returns the usage error; builds the state as `ServeMCP` does; prints `startupWarning()`; sets `adapter.sandbox`, `adapter.loaded` and `adapter.missing`; and `startAgentRun` gives the endpoint's auditor the state function. Existing in-process tests that reach `executeAgent` pass the mode `off` through `Options`. Makes T036 pass.
- [X] T038 [US9] In `test/integration/sandbox_mcp_test.go`, add `TestSandboxRuleContextTask` (M, first half: a `[context]` task that writes into the marker directory and prints a line; the marker directory is unchanged and the line is in the `initialize` instructions), `TestSandboxAgentEndpoint` (S: a person runs an `(agent)` task with a `[context]` task that writes into the marker directory and `agent_cmd` set to the stand-in agent of `audit_log_mcp_test.go`, which calls a tool that writes into the marker directory; confined, both writes fail and the marker directory is unchanged; with `RUNE_SANDBOX=off` the warning is on standard error before anything else of the task and both writes land; with `noBackendEnv` the warning is printed, the context task did not run and the tool call is refused; with `RUNE_SANDBOX=maybe` the exit code is 2 and the agent did not start) and `TestSandboxRuleAgentTaskCalledByAgent` (an `(agent)` task without `[network]` and `agent_cmd` `["./probe", "agent", "<addr>"]`, called through `rune mcp`: the stand-in agent's connection fails and it reports no `RUNE_MCP_URL`).

**Checkpoint**: M and S pass in `test-sandbox`.

---

## Phase 12: User Story 10 - The documentation states the rules and the limits (Priority: P3)

**Goal**: One section answers the nine points of FR-041, and the other pages agree with it.

**Independent Test**: the docs harness passes, and each point of FR-041 has a heading or a table.

- [X] T039 [US10] In `docs/mcp.md`, add the section "The sandbox" before "The audit log" with these subsections: what it enforces and what it does not; the modes, `--sandbox` and `RUNE_SANDBOX`, with the startup line and warnings; what a task can write; what a task can read and what is hidden; credentials; protected paths, and the refusals for a linked, symlinked or missing file; what `[network]` gives; lifetime of processes; the hint line; the support table (Linux with bubblewrap and user namespaces: supported, tested with bubblewrap 0.8.0 on Debian 12 and 0.11.1 on Linux 7.0; Linux without bubblewrap or without namespaces for it: unavailable; a default container: unavailable; NixOS, where the tools are under `/run`: unavailable; macOS and Windows: unsupported); when a call is refused, with the texts; the limits of FR-044; and "Making a Runefile work in the sandbox" (mark tasks `[network]`, give a directory module its path, create or remove an optional import, point tools into the workspace with `[env]`, tasks that commit or need credential files are for a person). Update in the same file: the security model list (`[network]` is enforced), the hints table row for `openWorldHint`, the audit record table and example (the three sandbox fields, kind `sandbox`), "What the log proves" (the sentence that waits for a sandbox), and "Agent task bodies" (the `[context]` task and the endpoint are confined; an agent task an agent calls runs inside).
- [X] T040 [P] [US10] Update `docs/cli.md` (the `--sandbox` flag and `RUNE_SANDBOX` in the `rune mcp` reference), `docs/runefile.md` and `docs/GRAMMAR.md` (`[network]` gives an agent's call the network and sets `openWorldHint`), `docs/troubleshooting.md` (entries for "the sandbox is unavailable", "Read-only file system" in a task an agent ran, "Could not resolve host", a tool that writes under the home directory, and "the Runefile looks for it and it does not exist"), `docs/docker.md` (the image has no backend: the container is the boundary, start with `--sandbox off`), `docs/use-cases/mcp-agents.md` (one paragraph and a link), and `docs/design/rune-only-agent-mode.md` (boundary 7 "A task is not sandboxed": what this feature closes, on which systems, and what stays open). Change the documentation string of `network` in `internal/language/builtin.go` to "Give the task the network when an agent calls it; without it an agent's call has none where a sandbox is enforced. Also sets the MCP openWorldHint, on this task and any task that runs it.", and update the test or golden file that holds the old string.
- [X] T041 [P] [US10] Update `SECURITY.md` (the sandbox in the security model, its limits in two sentences, link to the section), `README.md` (one line in the MCP feature list, and the install note that agent use on Linux wants bubblewrap) and `CONTRIBUTING.md` (the `test-sandbox` service, `RUNE_TEST_SANDBOX`, `RUNE_TEST_SANDBOX_DIR`, the mutation run, and what CI does).
- [X] T042 [US10] Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` and fix every failing block or link.

---

## Phase 13: Polish & Cross-Cutting Concerns

- [X] T043 Amend `.specify/memory/constitution.md`: Principle VII gains the sentence "A task an agent runs is confined by the operating system where a backend exists: it has no network unless its author declares `[network]`, and it writes only inside the project. Where no backend exists the call is refused, unless the operator started the server unsandboxed, which every record states." Version 1.5.0, last amended 2026-11-18, and a new Sync Impact Report in the style of the current one (MINOR, proposed with spec 053, the maintainer's merge is the approval, Principle V noted as unaffected: the binary stays static and the backend is an optional external program).
- [X] T044 [P] In `.github/workflows/ci.yml`, in the `test` job, add two steps that run only on Linux before "Test (race)": install bubblewrap with `sudo apt-get update && sudo apt-get install -y bubblewrap`, and allow it to create namespaces with `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 || true`. Set `RUNE_TEST_SANDBOX: require` and `RUNE_TEST_SANDBOX_DIR: ${{ runner.temp }}/rune-sandbox` in the environment of the test step on Linux only, with a comment that a skipped sandbox test must fail the job there.
- [X] T045 In `test/integration/sandbox_mcp_test.go`, add `TestSandboxOverhead` (20 calls of a task whose body is `true`, confined, against 20 with `--sandbox off`: the mean difference is under 150 ms) and run the file in `test-sandbox` with `-count=3` to catch a flaky scenario.
- [X] T046 Run the full gate set and fix what it finds: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`; the same with `-e CGO_ENABLED=1` and `-race`; the two `test-sandbox` commands of the conventions; the linter in the version CI pins; `go test ./internal/diag ./internal/lexer ./internal/parser ./internal/cli ./test/corpus -update` leaves no diff; `GOOS=windows go build ./...`, `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...` succeed; coverage of `internal/sandbox` and of the new files of `internal/cli` is at least 90%; the existing `mcpserver` tests, which use an engine of their own, pass without an edit other than the new kind's case (FR-045).
- [ ] T047 Walk [quickstart.md](quickstart.md) A to Z against the built binary and tick each scenario in the pull request description. Record in the description the two deviations from the issue's wording that the spec states: hard links are detected for policy files only, and a `[network]` task shares the host's abstract-namespace sockets. The pull request title starts with `feat(mcp)!:` and names the breaking changes, so the generated release notes call them out.
- [X] T048 Check `specs/053-mcp-task-sandbox/quickstart.md` against the final test names and correct any scenario whose expected result changed during implementation.
- [X] T049 Mutation run (SC-011): `docker compose run --rm -e GOFLAGS=-buildvcs=false -e RUNE_TEST_SANDBOX_MUTATE=off test-sandbox go test ./test/integration/... -run SandboxRule`. Every `TestSandboxRule...` test must fail. A test that passes is not testing its rule: move its fixture or strengthen it, and run again. Record the count of tests and failures in the pull request description.

---

## Dependencies & Execution Order

- Phase 1 before everything. T002 and T003 are independent of each other.
- Phase 2 blocks every story. Inside it: T004 to T008 are written first and in parallel; T009
  before T010 and T011; T012 before T013 and T016; T014 before T015 and T016; T016 before T017.
- Phase 3 (US4) blocks the binary-level tests of every later story: they need a server that
  takes the mode and confines.
- Phases 4 to 9 depend on Phase 3 and on T023 (the probe). They share one test file, so they
  are worked in order, though each story's scenarios are independent of the others'.
- T024 (the resolver file) before T025: scenario B needs name resolution on a host whose
  resolver file is under `/run`.
- Phase 10 (US8) needs T014 and T022. Phase 11 (US9) needs T017 and T034.
- Phase 12 needs the texts to be final: after Phases 3 to 11.
- Phase 13 last. T044 can be written any time after T002. T049 after every rule test exists.

## Parallel Opportunities

- T002, T003 (setup).
- T004, T005, T006, T007, T008 (the failing tests of the foundation, different files).
- T018 with T019; T033 with T036.
- T040 with T041 once T039 fixed the wording; T044 with any documentation task.

## Implementation Strategy

1. **Foundation** (Phases 1 and 2): the package, the worker and the seam, proven with a stub
   backend in the default service.
2. **MVP** (Phases 3 to 7, the P1 stories): the mode and the refusal, then the two guarantees
   and the protected set on the real backend. At this point the issue's first four acceptance
   criteria and the seventh hold.
3. **P2 stories** (Phases 8 to 11): sockets, lifetime, the record, hooks and agent tasks.
4. **Documentation and gates** (Phases 12 and 13), ending with the mutation run.

Each story's checkpoint is a set of quickstart scenarios that passes on its own.
