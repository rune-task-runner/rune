# Research: Confine the Tasks an Agent Runs with an Operating-System Sandbox

Decisions for [plan.md](plan.md). Every probe named here was run on 2026-11-18 on the
maintainer's host (Linux 7.0, bubblewrap 0.11.1, AppArmor with
`kernel.apparmor_restrict_unprivileged_userns=1`) and in a privileged `golang:1.27-bookworm`
container (bubblewrap 0.8.0, as root).

## R1. The process boundary: one worker process per task body

**Decision**: When a call is confined, the engine does not run the executor in the server. For
each body it starts `bwrap <rules> -- <rune binary> --sandbox-worker`. The worker reads one job
(the interpolated body, the executor, the directory, the environment), runs the same executor
code (`shell.Run`, `interp.Run`, `agent.CLIProvider.Run`) and writes one result. Everything else
of a call stays in the server: binding, policy, approval, the scheduler, the task cache, plan
capture, masking, the audit record.

**Rationale**: The built-in shell interprets redirections and built-ins in its own process, so
wrapping `exec.Command` would leave `echo x > /etc/passwd` free (issue, "Architecture
constraint"). A worker per body, not per call, is what makes `[network]` apply to one task and
not to its dependencies or hooks (FR-004, FR-009). Keeping Rune's own bookkeeping in the server
means it needs no write access inside the sandbox (FR-021).

**Alternatives considered**:
- One worker per call, running the scheduler inside. Rejected: every body of the call would
  share one network rule, and the plan store and the cache would have to be writable inside.
- Confining the server process itself. Rejected: the server needs the network (HTTP transport)
  and its own files, and one process cannot hold two rule sets at once (FR-005).
- A long-lived worker pool. Rejected: a pooled worker would carry state from one body to the
  next. The cost of a fresh sandbox is measured in R14.

## R2. The Linux backend: bubblewrap, found on the PATH

**Decision**: The backend on Linux is the `bwrap` program. Each probe resolves it with
`exec.LookPath` to an absolute path with its links followed, and that path is what runs. A
program that resolves inside the workspace or the private directories is not used: a task could
have put it there (FR-049). The same holds for the Rune binary. Rune does not bundle bubblewrap
and does not reimplement it.

**Rationale**: The issue names it as the mature runner. It is packaged by every major
distribution and is what Flatpak uses. Ubuntu since 24.04 denies unprivileged user namespaces to
programs without an AppArmor profile; `bwrap` ships with one (`bwrap-userns-restrict`), and a
Rune binary that created namespaces itself would be denied. Probe: `bwrap --unshare-all ...`
works on the host as uid 1000 under that profile.

**Alternatives considered**:
- Namespaces created by Rune itself (`SysProcAttr.Cloneflags` and mount setup in Go). Rejected:
  it repeats bubblewrap's work in code with no security history, and fails on Ubuntu's default
  policy.
- Landlock alone. Rejected, see R15.
- A container runtime (Docker, Podman). Rejected: it needs a daemon or a much larger
  dependency, and reaching a daemon socket is what the sandbox must prevent.

## R3. The rule set

**Decision**: One argument list, built by a pure function from a `Spec`, in this order:

```text
--unshare-all [--share-net] --die-with-parent --new-session --cap-drop ALL
--ro-bind / /
--dev /dev  --proc /proc
--tmpfs /run  --tmpfs /var/tmp  --bind <private tmp> /tmp
--tmpfs <hidden directory>            (each existing one outside the workspace)
--ro-bind /dev/null <hidden file>     (each existing file or socket outside the workspace)
--bind <workspace> <workspace>
--bind <ancestor> <ancestor>          (each directory between the workspace and a protected file)
--ro-bind <protected> <protected>     (each existing one)
--tmpfs / --ro-bind /dev/null         (each hidden location inside the workspace)
--bind <private cache> <private cache>
--ro-bind <real resolv.conf> <same>   ([network] only, when it lies under a hidden directory)
--ro-bind <rune binary> <rune binary>
--remount-ro <each tmpfs above>
--chdir <workspace>
-- <rune binary> --sandbox-worker
```

Later arguments win, so a workspace under `/tmp` is visible again after `/tmp` was replaced,
and the Rune binary is reachable wherever it lives. For the same reason a hidden location that
lies inside the workspace (a Runefile in the home directory) is mounted after the workspace,
or the workspace bind would show it again. The full list is in
[contracts/sandbox-rules.md](contracts/sandbox-rules.md).

A `tmpfs` is writable when it is made. Each one is remounted read-only at the end, after the
mounts that need a mount point inside it, so a hidden location takes no write and no memory
(probe: `--tmpfs /run --remount-ro /run`, then `touch /run/x` fails).

**Rationale and probes**, each run with this list:
- `--ro-bind / /` is recursive, so a second mount of the project is read-only too. Writes by
  absolute path, through `..` and through a symbolic link fail with "Read-only file system".
- A hard link from outside into the workspace fails with "Invalid cross-device link": the
  workspace is its own mount (FR-013). The same holds for a protected file.
- A read-only file bind cannot be removed, renamed or replaced: "Device or resource busy"
  (FR-016).
- A directory that is itself a mount point cannot be renamed. Binding each ancestor of a
  protected file onto itself therefore stops `mv sub sub2` (FR-017). Probe: with the pin the
  rename fails with "Device or resource busy".
- `--unshare-all` without `--share-net` gives an interface list with `lo` only, name lookup
  fails, and a listener in the host's abstract socket namespace refuses with ECONNREFUSED. With
  `--share-net` the same listener accepts (the limit of FR-044).
- `--dev /dev` shows `null zero full random urandom tty` plus `ptmx`, `pts`, `shm` and the
  standard stream links. `--proc /proc` with the PID namespace shows only the body's processes.
- `--new-session` detaches the body from the server's terminal (FR-026).
- `--cap-drop ALL` is required for a server that runs as root: without it the body keeps every
  capability (`CapEff: 000001ffffffffff` in the container probe). With it `CapEff` and
  `CapBnd` are zero for root and for an ordinary user.
- Open files: only standard input, output and error plus what `ExtraFiles` passes (R5). Go
  marks the descriptors it opens close-on-exec, but not one the server inherited from the
  program that started it. See R21.

**Alternatives considered**:
- `--tmpfs /tmp`. Rejected: a build that writes gigabytes to its temporary directory would fill
  memory. A directory on disk fails like a full disk (spec edge case).
- A read list in place of `--ro-bind / /`. Rejected by the spec's assumption: toolchains live
  in too many places.
- `--disable-userns`. Rejected for now: it needs bubblewrap 0.8, and mounts made by bubblewrap
  cannot be undone from a nested namespace anyway (probe: `unshare -Ur -m` inside the sandbox
  is denied on the host and has no capability over the outer mounts elsewhere).

## R4. Protected paths that cannot be bound safely are a refusal

**Decision**: Before a confined call runs anything, Rune checks each protected path that
exists inside the workspace:
- If any part of it below the workspace is a symbolic link, the call is refused.
- If it is a regular file with a link count above one, and it is the Runefile, a loaded file
  or the dotenv file, the call is refused.

**Rationale**: A mount follows symbolic links, so the link itself cannot be pinned: a task
could remove it and put its own file there. A file with a second name can be written through
that name. Both are rare for these few files, and refusing with the file named is cheap and
exact (FR-018). `.git` and `.rune` are not scanned for link counts: `git clone --local` links
objects on purpose.

**Alternatives considered**: scanning the whole workspace for linked files. Rejected in the
spec's assumptions: pnpm and uv link project files to a store outside the project.

## R5. The worker protocol: two pipes, a start mark and one JSON result

**Decision**: The server passes two pipes as descriptors 3 and 4 (`exec.Cmd.ExtraFiles`;
bubblewrap passes open descriptors through, probe: a payload on descriptor 5 was readable
inside). The worker reads the job as JSON from 3 until end of file, marks 4 close-on-exec,
writes the line `started` to 4, runs the body, and writes one JSON result line to 4. Standard
output and error of the worker are the body's own streams and go to the call's masked buffers
as before.

The result names the error class (`exec`, `missing-interpreter`, `interp-exec`,
`agent-not-configured`, `agent-not-installed`, `agent-auth`, `cancelled`, `error`) with its
fields, and the server rebuilds the same typed error the executor returned, so exit codes,
banners, `||` hooks and `classify` behave as today.

When the worker exits without `started`, the sandbox did not start and the body did not run
(FR-037). When it exits after `started` without a result, the worker died: the task fails with
the exit status.

**Rationale**: The body owns standard output and error, so the result needs its own channel.
A start mark is the only reliable way to tell "bubblewrap failed" from "the body failed".

The body can reach the result pipe: the built-in shell runs in the worker, and
`echo x > /proc/self/fd/4` opens it. That gives a body nothing it does not have, since it
decides its own exit status anyway, but it must not let a body feed the server without bound.
The server reads at most 64 KiB, expects the start mark and exactly one JSON object, and
treats anything else as a failed worker. Descriptors 3 and 4 are close-on-exec, so programs the
body starts do not hold them.

**Alternatives considered**:
- Exit codes only. Rejected: a missing interpreter, a parse error and an exit status cannot be
  told apart, and the failing line would be lost from the error text.
- A hidden cobra command. Rejected: a command name would shadow a task of that name, as `log`
  did. An argument that starts with `--` cannot be a task name, and it is checked before cobra
  runs.

## R6. Lifetime: the PID namespace does the reaping

**Decision**: `--unshare-all` puts the body in its own PID namespace, and `--die-with-parent`
ties it to the server. Cancelling the call kills the `bwrap` process (`exec.CommandContext`),
which ends the namespace's first process, and the kernel then kills every process in the
namespace. `cmd.WaitDelay` bounds the wait for the output pipes.

**Probes**: a child started with `setsid` wrote a heartbeat file every 100 ms. After
`kill -9` of `bwrap` the heartbeat stopped at once. When the body ended normally with the child
still running, the heartbeat stopped as well (FR-031, FR-032).

**Alternatives considered**: process groups and `Setpgid`. Rejected: a child that calls
`setsid` leaves the group.

## R7. Availability is decided by starting a sandbox

**Decision**: `Backend.Probe` runs the real rule set with `true`'s equivalent: the Rune binary
with `--sandbox-worker` and an empty job is not needed, so the probe runs
`bwrap <rules for a temporary directory> -- <rune binary> --sandbox-probe`, which exits 0. The
result is cached. While the result is a failure, the next call probes again, at most once per
second (FR-036).

**Rationale**: `bwrap --version` succeeds on a host where user namespaces are denied (Ubuntu
without the profile, a default Docker container: "No permissions to create a new namespace").
Running the Rune binary inside also proves that the binary is reachable in the sandbox.

Reasons reported: `bubblewrap (bwrap) was not found on the PATH`, or
`bubblewrap could not start a sandbox: <first line of its standard error>`. On another
operating system: `this system (<os>) has no sandbox backend`.

Before the probe, the state checks what a sandbox that starts would still get wrong: the
backend program or the Rune binary inside the workspace (R2), and a directory of the server's
`PATH` that resolves under `/run`, `/tmp` or `/var/tmp`. The last is NixOS, where the tools
live under `/run/current-system`: the probe would pass and every task would fail to find its
programs. Each is reported as unavailable with its own reason.

## R8. The mode comes from the flag, then the variable

**Decision**: `rune mcp --sandbox <enforce|off>`; when the flag is absent, `RUNE_SANDBOX`; when
both are absent, `enforce`. The endpoint of an `(agent)` task reads `RUNE_SANDBOX` only. Both
are read where the audit directory is read: `cmd/rune` puts the variable into `cli.Options`,
and `ServeMCP` parses before it loads the Runefile (FR-033, FR-034).

**Rationale**: A flag is what "explicit at startup" means for a server a client starts. The
endpoint of an agent task has no flag of its own, and a variable reaches all three entry
points. `RUNE_AUDIT_DIR` is the precedent.

## R9. The server starts when the sandbox is unavailable

**Decision**: `ServeMCP` probes after the audit log is open and before the `[context]` task.
On failure it prints the warning, puts `sandbox: unavailable, calls are refused` in the startup
line, and serves. Each call is refused in the adapter with error kind `sandbox`. The `[context]`
task goes through the same adapter path, so it is refused and yields today's notice.

**Rationale**: spec assumption "The server starts when the sandbox is unavailable".

## R10. Where the refusal sits in a call

**Decision**: In `mcpAdapter.call`, after the arguments are bound and the `[agent-allow]`
policy is checked, and before a plan is looked up or a person is asked. The checks, in order:
the backend is ready (R7); the private directories exist (R12); the working directory of every
task the call can reach is inside the workspace; no file the load looked for inside the
workspace is missing (R23); the protected paths pass R4.

**Rationale**: Binding and the value policy run no command, so the order among refusals does
not matter for safety. Nobody should be asked to approve a call that will be refused. The set
of reachable tasks is the walk `reach` already does; it is refactored to yield the tasks.

## R11. The workspace and the task directory

**Decision**: The workspace is `filepath.EvalSymlinks` of the Runefile's directory. A task's
directory (`taskDir`) is resolved the same way, with the part that does not exist yet kept as
written, and must lie within the workspace. `bwrap` always starts in the workspace; the
executor changes to the task's directory itself, so a directory that does not exist fails as
it does today.

## R12. Private directories

**Decision**: Under `<user cache dir>/rune/sandbox/<project id>/`, where the user cache
directory is `os.UserCacheDir()` of the server and the project id is the audit log's
(`audit.ProjectID`): `cache/`, bound at its own path and named by `XDG_CACHE_HOME`; and
`tmp/<random>/`, created per body, bound at `/tmp`, named by `TMPDIR=/tmp`, and removed when
the body ends. Both are created with mode 0700. Beside each temporary directory is a lock file
that the server holds while the body runs. When a new temporary directory is made, siblings
whose lock nobody holds are removed, so a killed server leaves nothing for long. A lock is
used because a directory's own time says nothing about a body that has run for a day, and a
process id would be reused, or mean another process to a server in another PID namespace. A base
that resolves inside the workspace is a refusal.

The two variables are added to the engine's base environment for the call, before the task's
`[env]` attributes, so an author can still override them (FR-027).

**Rationale**: FR-014. The project tree stays clean, and the directories are reachable only
through their mounts.

## R13. Hidden locations

**Decision**: Fixed in code, each used only when it exists: `/run`, `/var/tmp` and `/tmp`
(replaced, R3); the audit base directory; the home entries of FR-023 (directories get an empty
`tmpfs`, files get `/dev/null`); the path of `SSH_AUTH_SOCK`; the path of a `DOCKER_HOST` that
starts with `unix://`. `/var/run` is a link to `/run` on current systems; when it is a real
directory it gets a `tmpfs` too.

For a `[network]` body, when `/etc/resolv.conf` resolves to a path under `/run` (systemd-resolved:
`/run/systemd/resolve/stub-resolv.conf`), that one file is bound back read-only. Probe: without
it name lookup fails in a `[network]` sandbox, with it `getent hosts` succeeds (FR-028).

## R14. Cost

**Measured** on the host, 20 runs each: `bwrap <rules> true` takes 11 ms, the installed Rune
binary (11 MB) starts in 8 ms, and `bwrap <rules> rune --version` takes 13 ms. A confined body
therefore costs on the order of 15 to 30 ms over an unconfined one, under the 150 ms of SC-008.
The integration suite asserts the bound.

## R15. Landlock: evaluated, not used in this release

**Decision**: No Landlock layer.

**Rationale**: Landlock cannot provide the network rule by itself: it restricts TCP bind and
connect from kernel 6.7, not UDP, so name lookup would work, and it does not cover Unix
sockets by path. It cannot hide a directory, so sockets under `/run` would stay reachable. It
would be useful as a second layer inside the worker, in particular to scope abstract-namespace
sockets for `[network]` bodies (kernel 6.12). That needs a kernel-version matrix and a
"partially enforced" state, which the issue warns against presenting as complete. It is
recorded here as the next step, and the limit is documented (FR-044).

## R16. Nested Rune

**Decision**: Nothing special. A `rune` started by a confined body runs on the command-line
path, which never starts a sandbox, and it is already inside one. If that inner Rune runs an
`(agent)` task whose endpoint receives calls, those calls are confined again: a nested
bubblewrap either works or fails, and a failure is a refusal.

**Probes**: nested `bwrap` is denied on the host (AppArmor) and in the container (no
capability after `--cap-drop ALL`). So such calls are refused there, never unconfined.

## R17. The audit record and the server library

**Decision**: `audit.Sandbox` gains `Backend string` and `Mode string`, both `omitempty`. The
`auditor` in `internal/cli` gets a function that asks the state at the start of a call. Asking
is what probes: `ready` is called there, so a call that finds a backend installed since the
last failure is recorded as enforced, and the preflight a moment later reads the same cached
answer. The line of `rune log` is shown only for an action with an exit code.
`mcpserver` gains one constant, `AuditKindSandbox = "sandbox"`, which `CallAudit.end` maps to
outcome `refused` like `policy`. `rune log` adds the detail line
`ran without a sandbox (the server was started with the sandbox off)` for a call whose record
has mode `off`. Records written before this feature have no mode and get no line.

**Rationale**: FR-042, FR-043. The public package changes by one constant, so an embedder sees
nothing (FR-045).

## R18. Tests and where they can run

**Decision**:
- Unit tests that need no sandbox run everywhere: mode parsing, the argument list, the path
  checks, the job and result encoding, the worker run in-process over pipes, the refusals with
  a backend that reports unavailable, the startup text, the audit fields.
- Tests that start a real sandbox call `requireSandbox(t)`: it skips when the probe fails,
  unless `RUNE_TEST_SANDBOX=require` is set, in which case it fails. CI sets it on Linux after
  installing bubblewrap and lifting Ubuntu's namespace restriction, so the support table is
  tested and a silent skip cannot hide a regression.
- The Docker harness gains a `test-sandbox` service that runs privileged (a default container
  cannot create namespaces), and `Dockerfile.test` installs bubblewrap. The default `test`
  service is unchanged and skips the real-sandbox tests.
- Every existing server test runs unsandboxed: `RUNE_SANDBOX=off` in the integration harness
  (the docs harness starts no server), and the mode in `Options` for in-process tests. They
  test what they tested.
- Fixtures of the rule tests live outside `/tmp`, `/var/tmp` and `/run`. The sandbox replaces
  those directories, so a marker under `/tmp` would be absent whether or not a rule works.
  Each escape test first reads its marker from inside the body.
- Rule tests are written after the argument list exists, so "seen failing first" is done by
  mutation: the same tests run against a server started unsandboxed must all fail (SC-011).
  The unit tests of the argument list are written first in the usual way.
- Tests that start a process with extra descriptors, run a shell script as a program, or
  assert file modes are in `_unix_test.go` files: the Windows job runs `go test ./...`.
- Probes inside a task use a small Go program built into the test project, because the test
  container has no Python and the built-in shell has no network commands.

**Rationale**: Principle VI and the constraint that tests run in Docker. The real-sandbox
scenarios cannot run on Windows or macOS, and the refusal scenarios run there.

## R19. Documentation and the constitution

**Decision**: A new section "The sandbox" in `docs/mcp.md` carries the nine points of FR-041.
The security model, the audit record table, the hints table and the agent task section there
are updated. `docs/cli.md`, `docs/runefile.md`, `docs/GRAMMAR.md` (the comment on `network`),
`docs/troubleshooting.md`, `docs/docker.md`, `docs/use-cases/mcp-agents.md`,
`docs/design/rune-only-agent-mode.md` (boundary 7), `SECURITY.md`, `README.md` and
`CONTRIBUTING.md` follow. Principle VII of the constitution gains one sentence (version 1.5.0,
MINOR: a new commitment inside an existing principle).

## R20. Rune's own `git` commands run in a read-only sandbox

**Decision**: In mode `enforce`, the two places where the server runs `git` for a call
(`gitState` for the audit record, `gitInfo` for a plan) build their command through the
backend with a read-only `Spec`: no network, the repository bound read only, an empty
read-only `/tmp`, no cache. The repository root is found without running git, by walking up
from the workspace to the nearest directory with a `.git` entry. In mode `off`, and for an
adapter with no state, the commands run as today.

**Rationale**: `git status` runs programs named by the repository's configuration (a clean
filter, for one). `.git` is protected when it exists, but a project without one lets a task
create it, and a submodule's `.git` file is not in the protected set. The second analysis pass
confirmed in a scratch directory that `git -c core.fsmonitor=false status --porcelain` runs a
clean filter from the repository's configuration. Running git inside a sandbox makes the whole
class harmless instead of chasing each configuration key (FR-048).

**Alternatives considered**: not asking git when `.git` was not in the protected set, plus
`--ignore-submodules`. Rejected: it closes the cases known today and no others.

## R21. Descriptors the server inherited

**Decision**: When a server starts in mode `enforce` on Linux, it marks every open descriptor
from 3 up close-on-exec (`/proc/self/fd`). This runs once, before any body.

**Rationale**: Go sets the flag on what it opens. A client that starts the server with extra
descriptors open would otherwise hand them through bubblewrap and the worker to every body
(FR-025).

## R22. The hint line

**Decision**: After a confined body fails, `runBodyTo` prints one line on the call's standard
error: that the task ran in the sandbox, whether it had the network, and the workspace
(FR-050, texts in the contract). A `*sandboxStartError` is printed as an error banner the same
way a policy refusal is, so its text reaches the agent.

**Rationale**: `Could not resolve host` and `Read-only file system` name no cause. The
constitution's backward-compatibility rule asks that a changed behaviour fails loudly with its
fix. The line is on failures only, so a passing task's result is unchanged.

## R23. A file the load looked for and did not find

**Decision**: `pathRecorder` already keeps the paths a load asked for and did not find. In the
preflight, each such path inside the workspace is a refusal that names it (FR-047). The dotenv
file is not recorded there and is not checked: see the spec's assumption.

**Rationale**: A module found at `foo/Runefile` was first looked for at `foo.rune`. An optional
import may be absent. A task that creates either file changes the next load, and the reloader
picks it up within the session: the new file can add `[network]` and drop `[confirm]`. A mount
cannot protect a name that does not exist without creating it in the real project. The remedy
is one edit: give the module its path, create the file, or remove the import.

**Alternatives considered**: ignoring files that appear after the server started. Rejected: the
next server start would read them, which is the "future run" the issue names.

## R25. Which Runefile is served must not be a task's choice

**Decision**: Two rules. Discovery fails on a directory that holds more than one Runefile
name, for every command. And a call is refused when the server found its Runefile by searching
up from a directory below it, unless the Runefile was named with `--file`.

**Rationale**: Found in the first code review. Discovery took the first match of a directory
listing, and `.runefile` and `RUNEFILE` sort before `Runefile`: a task could write one of them
and the next server start would load it, with no `[confirm]`, no `[agent-allow]` and
`[network]` everywhere. A mount cannot hold a name that does not exist, so the choice itself
has to be unambiguous. The second rule is the same hole from below: a server started in
`project/pkg` searches up, and `project/pkg/Runefile` is a path a task can write.

## R24. The `[context]` task and the mode of an agent task

**Decision**: `executeAgent` builds the endpoint's state from `RUNE_SANDBOX` before anything
else. A value that is not a mode fails the task with the usage error before the agent program
starts. The warning of an unavailable or switched-off sandbox is printed next, then the
`[context]` task runs through the adapter under that state, then the endpoint opens. The state
is attached in `executeAgent`, not in `newAgentAdapter`, so an adapter built directly keeps
running in process.

**Rationale**: FR-001 names this `[context]` task. The issue asks that no preparation hook
runs task code before protection starts.
