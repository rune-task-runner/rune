# Implementation Plan: Confine the Tasks an Agent Runs with an Operating-System Sandbox

**Branch**: `053-mcp-task-sandbox` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/053-mcp-task-sandbox/spec.md`

## Summary

Close issue #51. Today a task an agent calls runs as an ordinary process of the server's user,
and the default shell runs inside the server itself. This plan makes the operating system
confine every body an agent's call runs.

For a confined call the engine no longer runs the executor in the server. For each body it
starts `bwrap <rules> -- rune --sandbox-worker`, sends the worker one job over a pipe, and
reads one result back (R1, R5). The worker runs the same executor code, so the built-in shell,
interpreters, shell overrides, agent programs and all their children are inside (FR-003). The
rules give the body its own network namespace unless its task has `[network]`, a read-only
view of the host with the workspace writable, read-only binds over the paths that decide its
rights, empty runtime and credential locations, private temporary and cache directories, no
capabilities, no terminal and its own PID namespace, which is what ends every process with the
body (R3, R6, R13).

A new package, `internal/sandbox`, owns the mode, the backend interface, the bubblewrap
argument list and the path checks. `internal/cli` owns the state of a server, the preflight
that refuses a call before anything runs (R10), the worker and its protocol, and the operator
surface: `--sandbox`, `RUNE_SANDBOX`, the startup line, the warnings, the audit fields and the
`rune log` line (R8, R9, R17). `mcpserver` gains one constant.

Where no backend can confine, a call is refused. The operator can start a server unsandboxed,
and every record says so (FR-033 to FR-043). Full decisions are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27.0` in `go.mod`), pure Go, `CGO_ENABLED=0` builds

**Primary Dependencies**: existing only. `os/exec`, `encoding/json`, `syscall` and
`path/filepath` from the standard library; `mvdan.cc/sh/v3` through the existing shell
executor. No new modules. At run time on Linux: the `bwrap` program (bubblewrap), found on the
`PATH`, optional in the sense that a host without it refuses agent calls.

**Storage**: `<user cache dir>/rune/sandbox/<project id>/cache/` (persists) and
`.../tmp/<random>/` (one body). Two new fields in the `sandbox` object of the audit record.

**Testing**: `go test` in Docker only. Unit tests in `internal/sandbox`, `internal/cli`,
`mcpserver` and `cmd/rune` run in the default `test` service. Tests that start a real sandbox
run in a new privileged `test-sandbox` service and in the Linux CI job, where
`RUNE_TEST_SANDBOX=require` turns a skip into a failure (R18).

**Target Platform**: Linux, macOS, Windows (amd64, arm64), single static binary. The sandbox
backend exists on Linux only; the other systems refuse or run unsandboxed by the operator's
choice.

**Project Type**: CLI tool plus embeddable MCP server library (existing layout)

**Performance Goals**: Confinement adds under 150 ms to a body (SC-008); measured 13 ms for
bubblewrap plus the start of the Rune binary (R14). An unavailable backend is probed at most
once per second. A person's command-line run does no extra work.

**Constraints**: No body of an agent's call runs in the server process in mode `enforce`
(FR-003, FR-038). A refusal happens before any command of the call (FR-035). The mode is never
read from the Runefile, a dotenv file or a tool argument (FR-033). `[network]` applies to one
body (FR-009). Tool lists, results and the other refusal texts are unchanged (FR-046). An
embedder with its own engine sees no change (FR-045). New prose avoids the em dash
(maintainer rule).

**Scale/Scope**: 1 new package (`internal/sandbox`, about 5 files with tests), 3 new files in
`internal/cli` with tests, changes to about 14 existing Go files, 1 new test helper program, 1
new integration test file, updates to the integration and docs harnesses, the Docker harness,
the CI workflow, the constitution and the documents listed under Project Structure.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.4.1. One amendment is proposed
with this plan (Principle VII, version 1.5.0).*

| Principle | Verdict | Notes |
|---|---|---|
| I. Command runner, not a build system | PASS | Nothing is skipped or cached differently. A call runs or returns a result that says why not. |
| II. Errors are a feature | PASS | No DSL change, so no new static diagnostic. Every refusal names the task, the reason and the remedy, and the path when a path is the reason. The usage error of the mode names the accepted values. |
| III. Minimal, total DSL | PASS | The language is untouched. `[network]` keeps its syntax. The mode is not a Runefile setting. |
| IV. Hand-written front end, idiomatic Go | PASS, with one justified addition | No parser change. One new package, `internal/sandbox`. See Complexity Tracking. `mcpserver` stays public and gains one constant. |
| V. Boringly portable | PASS, with a note | Rune stays pure Go and one static binary on three systems. The default shell is still `mvdan.cc/sh`; it now runs in a worker process of the same binary. The sandbox needs an external program on Linux and does not exist elsewhere. That is stated, and it gates only the agent surface. See Complexity Tracking. |
| VI. Test-first, multi-layer verification | PASS, with a note | Failing tests first for the argument list and path checks, the worker over pipes and the refusals with a stub backend (R18). The binary-level rule tests come after the argument list they exercise, so they are seen failing by mutation instead: run against a server started unsandboxed, every one must fail (SC-011). See Complexity Tracking. The changed pages go through the docs harness. CI runs the real-sandbox tests on Linux and fails when they would skip. |
| VII. AI-native, secure by default | PASS, amended | This is the principle the feature serves. The default becomes: confined, or refused. The amendment adds that sentence. Secrets still come from the environment; the spec hides credential stores and states that reading is not otherwise confined. |
| VIII. Go engineering discipline | PASS | No `init()`, no package global. The worker command carries the call's context; the probe has a timeout. The only new goroutines are those `os/exec` owns for the output pipes, bounded by `WaitDelay`. Errors are typed where a caller branches (`sandboxStartError`, the refusal), wrapped with `%w`. `golangci-lint` stays clean. |
| Constraint: Docker-only testing | PASS | The default service runs everything that needs no namespace. A privileged service runs the rest. Nothing runs on the host. |
| Constraint: backward compatibility | PASS, with a note | No DSL change, so no Runefile fails to parse or analyze. The behaviour of the agent surface changes on purpose: a task without `[network]` loses the network, writes outside the project fail, and a host without a backend refuses calls until the operator passes `--sandbox off`. A refusal names its fix. A body that fails inside the sandbox gets a line that says it ran there, without the network and writable only inside the project (R22), because the tool's own message names no cause. The release notes call the change out. The command line is untouched. |
| Constraint: surface changes carry their docs | PASS | `docs/GRAMMAR.md` and `docs/runefile.md` change the meaning they give for `[network]` in the same PR (R19). |

**Post-design re-check**: PASS. The contracts add one flag, one environment variable, two
internal arguments of the binary, one constant on the server library, two fields of the audit
record, one line of `rune log` and one package. No principle verdict changes.

## Project Structure

### Documentation (this feature)

```text
specs/053-mcp-task-sandbox/
├── plan.md              # This file
├── research.md          # Phase 0 decisions R1 to R24
├── data-model.md        # Phase 1: mode, backend, state, spec, job, result, record
├── quickstart.md        # Phase 1: validation scenarios A to Z
├── contracts/
│   ├── sandbox-rules.md     # what a body can reach, the argument list, the refusals
│   └── operator-surface.md  # the mode, the startup line, warnings, audit, rune log
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks, not created here)
```

### Source Code (repository root)

```text
internal/sandbox/            # new package, standard library only
├── sandbox.go               # package comment; Mode, ParseMode; Backend; Spec; Detect; Unsupported
├── bwrap.go                 # BwrapArgs (pure); the bubblewrap Backend: LookPath, Probe, Command
├── paths.go                 # Resolve, Within; CheckProtected (symbolic link, link count); Ancestors
├── links_unix.go, links_other.go   # the link count of a file
└── *_test.go

internal/cli/
├── sandbox.go               # new: sandboxState (mode, backend, probe cache, private dirs,
│                            #   hidden locations), the texts of the contract, preflight
├── sandboxexec.go           # new: sandboxJob, sandboxResult, the launcher (engine.runConfined),
│                            #   the error mapping, the hint line, SandboxWorker
├── sandboxexec_unix.go, sandboxexec_other.go   # the worker's pipes; close-on-exec of
│                            #   inherited descriptors
├── sandbox_test.go, sandboxexec_test.go   # new
├── dispatch.go              # Options.MCPSandbox, Options.SandboxEnv
├── run.go                   # engine.sandbox; runBodyTo runs the executor or the launcher;
│                            #   the load's files are recorded for an agent task's endpoint
├── mcp.go                   # adapter.sandbox and adapter.loaded; call(): preflight, the
│                            #   private directories in the environment; reach yields tasks
├── serve.go                 # ServeMCP: parse the mode, build the state, probe, warn, the
│                            #   startup line; loaders record the load's files
├── mcpreload.go             # configure() hands the state to each new adapter
├── agentexec.go             # the endpoint's adapter gets a state from RUNE_SANDBOX and
│                            #   prints the warning
├── audit.go                 # the auditor writes backend and mode; gitState takes a command
│                            #   builder
├── planapply.go             # gitInfo takes a command builder
└── log.go                   # the unsandboxed line

internal/audit/record.go     # Sandbox.Backend, Sandbox.Mode
mcpserver/audit.go           # AuditKindSandbox; end() maps it to refused

cmd/rune/
├── main.go                  # --sandbox-worker and --sandbox-probe before cobra
├── serve.go                 # --sandbox; help text
└── root.go                  # RUNE_SANDBOX into Options

test/integration/
├── harness_test.go          # RUNE_SANDBOX=off for every existing test; builds the probe
├── sandbox_mcp_test.go      # new: quickstart A to Z, the overhead check
└── testdata/sandboxprobe/main.go   # new: the probe program

Dockerfile.test              # installs bubblewrap
docker-compose.yml           # the privileged test-sandbox service
Runefile                     # test-sandbox task
.github/workflows/ci.yml     # Linux: install bubblewrap, allow namespaces, RUNE_TEST_SANDBOX=require

.specify/memory/constitution.md    # Principle VII, version 1.5.0

docs/mcp.md, docs/cli.md, docs/runefile.md, docs/GRAMMAR.md, docs/troubleshooting.md,
docs/docker.md, docs/use-cases/mcp-agents.md, docs/design/rune-only-agent-mode.md,
SECURITY.md, README.md, CONTRIBUTING.md
```

**Structure Decision**: Existing layout plus one package. `internal/sandbox` knows nothing
about tasks: it turns a set of paths and a network flag into a confined command, so its rules
are tested without an engine and a second backend can be added behind the same interface. The
worker and the job live in `internal/cli` because they mirror `runBodyTo`: the same executor
options go in, the same error types come out. The scheduler, the parser, the analyzer and the
executors are not changed.

## Design Notes

**The seam.** `engine.runBodyTo` computes the lines, the directory and the environment as
today. Its executor switch moves into `engine.runExecutor`. When `engine.sandbox` is set, the
switch is replaced by `engine.runConfined`, which builds a job with exactly those values. An
engine with no sandbox, which is every command-line run, takes the old path unchanged.

**Who sets `engine.sandbox`.** Only `mcpAdapter.call`, from `adapter.sandbox`, when the state
is confining. The adapter gets its state from `reloader.configure` (`rune mcp`, `rune serve`)
or from `engine.newAgentAdapter` (the endpoint of an agent task). An adapter built directly, as
tests do, has none.

**Agent bodies.** On the adapter's path the engine has no file, so `executeAgent` opens no
endpoint and gathers no context. A confined agent body is therefore one job: the prompt and
the agent command. The worker runs `agent.CLIProvider` and prints the final text.

**The worker's environment.** The worker process gets the server's environment, so
`exec.LookPath` for an interpreter resolves as it does today in the server. The task's own
environment travels in the job and is what the body sees.

**Errors.** The server rebuilds the executor's error type from the result, so
`engine.classifyRunErr`, the exit codes, `[no-exit-message]`, the `||` hooks and the failing
line in the banner need no change.

**The start record.** The auditor asks the state once per call, at the start, so a call killed
mid-way still states its mode and backend (US8, scenario 5).

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| A new package under `internal/` (locked layout) | The rule set is security-critical and order-sensitive: which mounts, in which order, which paths may not be bound. It needs table tests that build no engine and no server. | In `internal/cli` it would sit beside 20 files of engine code and could not be reviewed alone. In `internal/runtime` it would suggest a fourth executor, which it is not: it wraps all three. |
| An external program at run time on Linux (Principle V: single static binary) | The issue asks for enforcement by the operating system with a mature runner. bubblewrap is that runner, and Ubuntu permits namespaces to it and not to an arbitrary binary. | Creating namespaces in Go keeps one binary and fails on Ubuntu's default policy, with new security-critical code. Bundling bubblewrap needs C and per-architecture builds, against `CGO_ENABLED=0`. The dependency is optional, Linux-only and limited to the agent surface: the command line and every other system work without it. |
| A constitution amendment | Principle VII states the security defaults of the agent surface. Confinement is a new default and belongs there. | Leaving the principle as it is would let a later change remove the default without amending anything. |
| Binary-level rule tests are not seen failing before the code exists (Principle VI) | A rule test needs a server that confines, and the rule is one argument of a list that is built and unit-tested first. | Writing each binary-level test against a build without its argument means one throwaway build per rule. Running the finished tests against an unsandboxed server shows the same thing in one run: each test fails when its rule is absent. |
