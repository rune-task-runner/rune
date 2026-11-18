# Quickstart: Validating the Sandbox

Each scenario is a test in `test/integration`, in the files named at the end of the table.
Rules and texts are in [contracts/sandbox-rules.md](contracts/sandbox-rules.md) and
[contracts/operator-surface.md](contracts/operator-surface.md).

## Prerequisites

- Scenarios A to M, R, T, U, V, X, Y, Z and the confined half of S need a host where
  bubblewrap can start a sandbox. They call `requireSandbox(t)`, which skips otherwise and fails when
  `RUNE_TEST_SANDBOX=require`.
- Scenarios N to Q, W and the unsandboxed half of S run on every system. The refusals of R are
  also unit-tested everywhere with a stub backend.

```sh
# everything that needs no sandbox (the default harness)
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...

# the real sandbox, in the privileged service
docker compose run --rm -e GOFLAGS=-buildvcs=false test-sandbox \
  go test ./internal/sandbox/... ./internal/cli/... ./test/integration/... -run 'Sandbox'
```

## Setup shared by the scenarios

A project directory with a Runefile, a `probe` program built from
`test/integration/testdata/sandboxprobe`, a marker directory beside the project, and a TCP and
a UDP listener of the test on `127.0.0.1`. The project, the marker directory, the home fixture
and the audit directory of these tests live under a fixture base that is not under `/tmp`,
`/var/tmp` or `/run`, because the sandbox replaces those: `RUNE_TEST_SANDBOX_DIR` when it is
set, else `.rune/sandbox-test` in the module root. Every escape test first reads the marker
from inside the body, so a failed write is the rule and not a missing path.

Tests of a rule are named `TestSandboxRule...` and start their server through `ruleServer`.
With `RUNE_TEST_SANDBOX_MUTATE=off` that helper starts the server unsandboxed, and every such
test must then fail (SC-011). The server is `rune mcp --color=never`, started
without `RUNE_SANDBOX` in its environment, driven by the SDK client.

`probe` subcommands: `connect ADDR`, `udp ADDR`, `resolve HOST`, `unix PATH`, `abstract NAME`,
`read PATH`, `write PATH`, `link OLD NEW`, `rename OLD NEW`, `fds`, `ls DIR`, `tty`, `status`,
`selfserve`, `daemon FILE`, `spawn N`, `sleep SECONDS` and `agent ADDR` (a stand-in agent
program: it connects to ADDR, prints whether `RUNE_MCP_URL` is set and ignores its last
argument, the prompt). Each prints `ok` or the error and exits 0 or 1.

## Scenarios

| | Scenario | Expected |
|---|---|---|
| A | Plain task: `probe connect`, `probe udp`, `probe resolve example.com` | all three fail; the listeners saw nothing |
| B | `[network]` task: `probe connect`, `probe udp` | both succeed |
| C | Plain task: `probe selfserve` | `ok` |
| D | Plain and `[network]` task: write inside; then `probe write` to the marker directory, to `../outside/x`, through a link, through `/proc/self/root`; `probe link` from outside; read standard input | the inside file exists; every other step fails; the marker directory is unchanged |
| E | Task writes with shell redirection, with `cd /; echo > x`, in Python-free form through `[script("sh")]`, under `set shell := ["sh", "-eu"]`, and through a child `sh -c` | five failures, marker unchanged |
| F | Task appends to the Runefile, removes it, renames a file over it, renames the directory of an imported file, writes `.git/x`, `.rune/x`, the dotenv file, lists the audit directory and writes into it, writes `/run/x` and `/var/tmp/x`. Run once directly and once with the project reached through a symbolic link | every step fails or finds nothing; digests unchanged; no reload line on the server's standard error |
| G | Plain task depending on a `[network]` task; `[network]` task with a plain `&&` hook and a failing body with a plain `\|\|` hook | the `[network]` bodies connect, the plain ones do not |
| H | 20 calls at once, half `[network]` | every `[network]` call connects, no plain call does |
| I | Unix listeners in `$XDG_RUNTIME_DIR` (under `/run`), in `/tmp`, at `SSH_AUTH_SOCK` and at a `DOCKER_HOST` path in the home fixture; a second run with the Runefile in the home fixture itself; an extra open file in the server; plain and `[network]` task run `probe unix` on each `probe fds`, `probe ls /proc` and `probe tty` | no connection; descriptors are `0 1 2`; only the body's processes; no terminal |
| J | Task writes `$TMPDIR/a` and `$XDG_CACHE_HOME/b`; a second call reads both | `a` is gone, `b` is there; neither is inside the project |
| K | Task runs `probe daemon hb` then `probe sleep 30`; the client cancels | the heartbeat stops within 2 s; the next call answers; audit outcome `cancelled` |
| L | Task runs `probe daemon hb` and ends | the heartbeat stops |
| M | `[context]` task writes outside and prints a line; a body runs `rune` for a `[network]` task | marker unchanged and the line is in the instructions; the inner task does not connect |
| N | Server with a `PATH` that has no `bwrap` (Linux) or on another system: start, list tools, call a task that writes a marker | startup warning and line as in the contract; the tool list is served; the call is refused with the contract text; no marker; audit outcome `refused`, kind `sandbox`, `{"enforced":false,"mode":"enforce"}` |
| O | The same server with a `[context]` task | the task did not run; the instructions hold the context notice |
| P | `rune mcp --sandbox off`, and `RUNE_SANDBOX=off rune mcp`; also `rune mcp` with a dotenv file that holds `RUNE_SANDBOX=off`, which stays in mode `enforce` | the warning and the line; the task runs and writes outside the project; audit `{"enforced":false,"mode":"off"}`; `rune log` shows the unsandboxed line |
| Q | `rune mcp --sandbox maybe`, `RUNE_SANDBOX=maybe rune mcp`, with a `[context]` task that writes a marker | exit 2 with the contract text; no marker |
| R | A task whose `[working-directory]` is outside the project; a Runefile with a second hard link; an import reached through a symbolic link in the project; a missing optional import; a module in a directory with no explicit path; a private base that is a file; a server started below its Runefile's directory | refused with the contract texts; nothing ran |
| X | A project with no `.git`: a task writes a `.git` whose configuration names a clean filter that writes a marker beside the project; a second call follows | the marker does not exist; the second call's audit record has a git state |
| Y | A body that fails with `exit 3`, plain and `[network]`; a body that succeeds | the failed bodies' standard error ends with the contract's hint line; the successful one has none |
| Z | The server is started with three extra open files and, in another run, as root with the project bind-mounted at a second path (only where the test can mount) | `probe fds` prints `0 1 2`; `probe status` shows no capability and `NoNewPrivs: 1`; a write through the second path fails |
| S | `(agent)` task run by a person with the stand-in agent of the audit tests and a `[context]` task that writes outside the project; the agent calls a tool that writes outside the project. Once on a confining host, once with `RUNE_SANDBOX=off`, once with no backend, once with `RUNE_SANDBOX=maybe` | confined: both writes fail. Off: the warning is printed first and both writes succeed. No backend: the warning is printed, the context task did not run and the tool call is refused. `maybe`: exit 2 before the agent starts |

| T | `rune serve --http`: a plain task connects to the test listener and to the server's own address, prints its descriptors; then a second call | both connections fail; descriptors `0 1 2`; the second call answers |
| U | Confined: a `[cache]` task twice; a `[plan]` task, then its `[applies]` task with an approving client | cached on the second call; the plan is stored, read by the body and ends applied |
| V | Confined: a masked secret, an output over the limit, a failing task with a `\|\|` hook, a `[confirm]` task with and without a grant, an `[agent-allow]` refusal | each behaves as on an unsandboxed server |
| W | `rune write-outside` on the command line with `RUNE_SANDBOX` set to `enforce`, `off` and `maybe` | the task writes outside the project and exits 0 each time |

Also tested on the binary: the backend installed while the server runs (the next call is
confined), the default container where bubblewrap is present and cannot start (the "could not
start" reason), the worker run by hand (exit 2), a directory with two Runefiles (exit 2 for
every command), and a plan applied in a project reached through a link under `/tmp`.

The scenarios are in four files of `test/integration`: `sandbox_mcp_test.go` (N to Q, W, every
system), `sandbox_rules_unix_test.go` (A to D, J, Y, R), `sandbox_protect_unix_test.go` (E, F,
G, H, M, X, R) and `sandbox_host_unix_test.go` with `sandbox_calls_unix_test.go` and
`sandbox_mount_linux_test.go` (I, K, L, S, T, U, V, Z).

## Unit-level checks

| Package | What |
|---|---|
| `internal/sandbox` | `ParseMode`; the argument list for a plain and a `[network]` spec, with ancestors, nesting under a protected directory, hidden files and directories; the protected-path checks; `Probe` against the real backend and against a `PATH` without it |
| `internal/cli` | job and result encoding in both directions for every result kind; the worker run in-process over pipes for a shell, an interpreter and a failing body; preflight refusals with a backend that reports unavailable; the reachable-task walk; the startup line and warnings; the audit `sandbox` object and the `rune log` line; that `reach` still reports what it did |
| `mcpserver` | kind `sandbox` ends a call as `refused` |
| `cmd/rune` | the flag, the variable, and the two internal arguments |

## Performance check

`TestSandboxOverhead` in `test/integration/sandbox_mcp_test.go`: 20 calls of a task whose body
is `true`, confined, take under 150 ms each on average over the same calls with the sandbox
off (SC-008).

## Documentation check

`docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./test/docs/...` passes with
the new section, and every point of FR-041 has a heading or a table in it.
