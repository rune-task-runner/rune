# Quickstart: Validating the Audit Log

**Feature**: 052-mcp-audit-log | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

The stored record and the JSON object are in
[contracts/audit-record.md](contracts/audit-record.md), the command and every message in
[contracts/rune-log.md](contracts/rune-log.md), the library types in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md). This guide only says how to check
them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run
```

Scenarios A to T are automated in `test/integration/audit_log_mcp_test.go` and
`test/integration/audit_log_cli_test.go` against the built binary with an SDK client. A, B, C,
E, I and M run on stdio and on `rune serve --http`. The rest run on stdio, except N and O,
which need HTTP. Every test sets `RUNE_AUDIT_DIR` to a temporary directory.

## Scratch Runefile

Every body writes a marker file, so a test can show that a refused call ran nothing. The
tests run as root in Docker, where a read-only directory stops nothing, so a write failure is
made by putting a directory where the day file belongs.

```rune
# Build the project.
build target="debug":
    @echo "built {{target}}" && echo built >> build.done

# Run the checks.
check:
    @echo checking >> check.done; exit 3

# Deploy to an environment.
deploy env:enum("staging","prod") token="":
    @echo deployed >> deploy.done

# Remove build output.
[confirm("Remove build output?")]
clean:
    @echo cleaned >> clean.done

# Wait.
slow:
    @echo started >> slow.started; sleep 30
```

Serve it with `API_TOKEN=s3cr3t-fixture-value` in the environment and
`RUNE_AUDIT_DIR=<tmp>`. `rune log --dir` prints the directory to read.

## Scenarios

| # | Do | Expect |
|---|---|---|
| A | Call `build` with no arguments | One action: `completed`, exit code 0, supplied arguments absent, effective `target=debug`, a duration, the session id, the client name and version, transport, git state, both digests, `sandbox.enforced` false |
| B | Call `check` | `failed`, exit code 3 |
| C | Call `nope` (no such tool) | `unknown-tool`, kind `protocol`, no task, no exit code |
| D | Call `build` with `colour=red`; call `deploy` with `env=qa` | Two actions `invalid-arguments`, kind `validation`, no effective arguments, no exit code. `build.done` and `deploy.done` are absent |
| E | Call `clean` from a client that cannot be asked | `refused`, kind `policy`, no exit code. `clean.done` is absent |
| F | Call `clean` from a client that approves | `completed`, `approval.asked` true, decision `approved`, tasks `["clean"]` |
| G | Call `clean` from a client that declines | `refused` with decision `declined`, kind `approval`, no exit code. A request nobody answers gives `timed-out`; the binary has no flag for the wait, so that is checked with a short wait in `mcpserver` |
| H | Serve with `--allow-destructive clean`, call `clean` | `completed`, `grants` is `["clean"]`, no `approval` |
| I | Call `deploy` with `env=staging` and `token=s3cr3t-fixture-value`; call `deploy` with `env=s3cr3t-fixture-value` | The fixture occurs zero times in the log files, in `rune log` and in `rune log --json`. `token` reads `***` |
| J | Call `build` with a 1 MiB `target` | The stored value ends with `[N bytes cut]` and the line is under 64 KiB |
| K | Call `slow`, kill the server when `slow.started` exists, run `rune log` | The action reads `incomplete`, has no end and no exit code. `--outcome completed` does not list it, `--outcome incomplete` does |
| L | Put a directory where today's day file would be, call `build` | The result is the audit error naming the directory and `RUNE_AUDIT_DIR`. `build.done` is absent. The server's standard error has the same text |
| M | Start a call to a task that waits on a file and then exits 3, replace today's day file with a directory while it waits, let it end | The result has exit code 3 and the error flag, plus the `[audit log]` block. Standard error has the warning. The task's marker has one line |
| N | Over HTTP, send a request with a wrong token | One `auth-failure` record with the remote address and no client. The token is not in the file |
| O | Over HTTP, send 30 requests with a wrong token within a second | At most 10 `auth-failure` records. The `suppressed` count of the next minute's record is checked with a test clock in `mcpserver` |
| P | Run two server processes for the project, make 200 calls to `build` across them at once | Every line parses. 200 actions, 200 distinct ids |
| Q | Append half a JSON object to today's file, call `build`, run `rune log` | Every earlier action and the new one are listed. Standard error reports one unreadable record |
| R | Run `rune log --date`, `--since`, `--until`, `--tz`, `--task`, `--outcome`, `--client`, `--session`, `--all`, `--json`, `--dir` over a prepared log | Each selects the documented actions. Each usage error prints its message and exits 2 |
| S | Run an `(agent)` task whose stand-in agent calls `build` through the endpoint | An `agent-run` action with start, end and exit code, and a `call` whose `parent` is its id and whose transport is `agent-endpoint` |
| T | Serve with `RUNE_AUDIT_DIR` inside the project; with `RUNE_AUDIT_RETENTION_DAYS=soon`; with a day file dated 100 days ago present | The first two do not start, exit 2, with the documented messages. The old file is gone after the third starts, and a file from yesterday remains |

Git states (unit level, `internal/cli`): a repository with a commit and a clean tree, the same
with an uncommitted change, a repository with no commit, a plain directory, and a `PATH`
without git give `commit` clean, `commit` dirty, `unborn`, `none` and `unavailable`.

Reload (unit level, `mcpserver`): two calls around a `Replace` carry the definition digest of
their own load.

## Manual check with a real client

1. `cd` to a project with a Runefile and add `rune mcp` to an MCP client.
2. Ask the agent to run a task, a task with a wrong argument, and a `[confirm]` task.
3. Run `rune log`. The three calls are listed with their outcomes.
4. Run `rune log --json | head -1`. The line is one JSON object with `schemaVersion` 1.
5. Run `rune log --dir` and check that the directory is not inside the project.
