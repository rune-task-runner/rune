---

description: "Task list for spec 045: warn on non-loopback MCP HTTP binds"
---

# Tasks: Warn on Non-Loopback MCP HTTP Binds

**Input**: Design documents from `/specs/045-mcp-http-bind-warning/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI (test-first, multi-layer). Every test that covers new
behavior is written and seen failing before the code that makes it pass.

**Organization**: Tasks are grouped by user story. Paths are from the repository root.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: The user story the task belongs to (US1 to US3)

## Conventions for every task

- Tests run in Docker only: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test <pkgs>`.
  Add `-e CGO_ENABLED=1` and `-race` for race runs.
- The warning text is fixed by [contracts/http-bind-warning.md](contracts/http-bind-warning.md):
  `serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext`.
  Tests pin it as a literal and do not read the production constant.
- Non-loopback binds in tests use `0.0.0.0:0` or `:0`, or `0.0.0.0:PORT` with a port found free
  on `127.0.0.1` ([research R7](research.md)).
- A test that asserts no warning first proves the bind happened: `StartHTTP` has returned, or the
  server has answered `401` to a request without a token.
- `mcpserver` tests run under `goleak`: every server a test starts is stopped before the test ends.
- No em dash in code, comments, or docs. Comments explain why, and stay few.
- Never weaken a test to make it pass.
- Every new or changed function gets a unit test.

---

## Phase 1: Setup

**Purpose**: Know the baseline before changing it.

- [X] T001 Run `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./mcpserver/ ./internal/cli/ ./cmd/rune/ ./test/integration/ ./test/docs/` on branch `045-mcp-http-bind-warning` and confirm it is green. If anything fails, report it and stop.

---

## Phase 2: Foundational (the bound address decides; the dead guard goes)

**Purpose**: Replace the string check with a check of the bound address and delete the guard that
can never fire ([research R1, R4](research.md)). No output changes yet. Every story builds on it.

- [X] T002 In `mcpserver/transport_test.go` replace `TestIsLoopback` with a table over `net.Addr` values, failing to compile until T003 (FR-007, FR-008, FR-015):
  - loopback: `&net.TCPAddr{IP: net.ParseIP("127.0.0.1")}`, `127.0.0.2`, `::1`, `::ffff:127.0.0.1`.
  - not loopback: `0.0.0.0`, `::`, `192.168.1.5`, `2001:db8::1`, a `*net.TCPAddr` with a nil `IP`, a `*net.UnixAddr`, and a nil `net.Addr`.
- [X] T003 In `mcpserver/transport.go`:
  - change `isLoopback` to `func isLoopback(a net.Addr) bool`, true only for a `*net.TCPAddr` whose `IP.IsLoopback()` is true;
  - delete the `if !isLoopback(cfg.Addr) { ... }` block of `ServeHTTP` and the error `binding a non-localhost address requires an explicit --addr` (FR-010);
  - rewrite the doc comment of `ServeHTTP` so it no longer says a non-localhost bind is refused (FR-011);
  - drop the `strings` import.
- [X] T004 Run `go test ./mcpserver/` in Docker. T002 is green and every other test passes unchanged.

**Checkpoint**: Same behavior for every caller; the decision is ready to use.

---

## Phase 3: User Story 1 - An operator who binds beyond loopback is warned (Priority: P1) MVP

**Goal**: `rune serve --http` on a non-loopback address prints the warning once on stderr and keeps
serving.

**Independent Test**: `rune serve --http --addr 0.0.0.0:PORT --token-file tok` prints the warning
line once, answers `401` without the token, and prints nothing on stdout.

### Tests first

- [X] T005 [P] [US1] In `mcpserver/transport_test.go` add, failing to compile until T008 (FR-002, FR-015):
  - `TestStartHTTPWarnsOnNonLoopbackBind`: for `0.0.0.0:0` and `:0`, `StartHTTP` with a `Warn` that appends to a slice returns with exactly one message, equal to the literal warning text.
  - `TestServeHTTPWarnsOnNonLoopbackBind`: `ServeHTTP` in a goroutine on `0.0.0.0:0` with a `Warn` that sends on a buffered channel; the message arrives; cancel the context; `ServeHTTP` returns `http.ErrServerClosed`; exactly one message was sent.
- [X] T006 [P] [US1] In `internal/cli/serve_test.go` and `internal/cli/mcp_test.go` add, failing until T009 (FR-001, FR-005, FR-006, FR-013, FR-016):
  - `TestAgentEndpointBindsLoopback` in `internal/cli/mcp_test.go`, beside the other `TestAgentEndpoint` tests: `agentHTTPConfig("tok")` of an engine whose stderr is a buffer has `Addr` `127.0.0.1:0`, the token, and a `Warn` that prints `warning: message` to that stderr when called; `StartHTTP` with it returns a loopback address and leaves the stderr buffer empty.
  - `TestWarnTransport`: with `style.New(false, &buf)`, `warnTransport(&buf, theme)("message")` writes exactly `warning: message\n`; with `style.New(true, &buf)` it writes `theme.Warning.Render("warning")` followed by `: message\n`, so only the word `warning` is styled.
  - `TestServeMCPWarnsOnNonLoopbackBind`: `ServeMCP` on HTTP at `0.0.0.0:0` with a `lockedBuffer` as stderr and a cancelable context; wait for the warning; save a change to the Runefile and wait for the `reloaded Runefile` line (the reload edge case of the spec); stop; stderr holds the line `warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext` exactly once, after the startup line; `ServeMCP` returned the error it returns for a loopback server stopped the same way. This is the test of FR-004: the binary test of T007 stops its process by killing it, so it cannot observe an exit status.
- [X] T007 [P] [US1] In new `test/integration/http_bind_warning_test.go` add `TestBindWarning_NonLoopback`, failing until T009 (FR-001, FR-004, FR-005; acceptance scenarios 1 to 4): for `--addr 0.0.0.0:PORT` and `--addr :PORT`, start the real binary with `serve --http --token-file`, wait for the warning on stderr, confirm that a `POST` to `http://127.0.0.1:PORT` without a token answers `401` and that an MCP client with the token lists the Runefile's task, with no `cleartext` in the session instructions or in the tool list, then stop the process: stderr holds the warning exactly once and stdout is empty.

### Implementation

- [X] T008 [US1] In `mcpserver/transport.go` ([research R2, R3](research.md)):
  - add the field `Warn func(message string)` to `HTTPConfig`, documented as in the contract;
  - add the constant `cleartextWarning` with the fixed text;
  - add `func (cfg HTTPConfig) warnCleartext(bound net.Addr)`, which calls `cfg.Warn(cleartextWarning)` when `cfg.Warn` is not nil and `bound` is not loopback;
  - call it in `ServeHTTP` right after `lc.Listen` succeeds, and in `StartHTTP` right after `lc.Listen` succeeds, each with `ln.Addr()`.
- [X] T009 [US1] In `internal/cli/serve.go` ([research R5](research.md)): add `warnTransport(w io.Writer, theme style.Theme) func(string)`, which prints `warning: <message>` with `theme.Warning` on the word `warning`; pass `Warn: warnTransport(opts.Stderr, opts.themeStderr())` in the `HTTPConfig` given to `ServeHTTP`. In `internal/cli/agentexec.go` add `func (e *engine) agentHTTPConfig(token string) mcpserver.HTTPConfig`, beside `agentMCPOptions`, returning `Addr: "127.0.0.1:0"`, the token and `Warn: warnTransport(e.opts.Stderr, e.theme)`, and use it in the `StartHTTP` call of `executeAgent`, so the endpoint of an `agent` task would say so if it ever left loopback (FR-013).
- [X] T010 [US1] Run `go test ./mcpserver/ ./internal/cli/ ./test/integration/ -run 'Loopback|WarnTransport|BindWarning'` in Docker. T005 to T007 are green.

**Checkpoint**: The defect of the issue is fixed.

---

## Phase 4: User Story 2 - A loopback server stays quiet (Priority: P1)

**Goal**: No cleartext warning on loopback, on the default address, on stdio, on a failed start, or
from the endpoint of an `agent` task.

**Independent Test**: `rune serve --http --addr 127.0.0.1:PORT --token-file tok` answers `401`
without the token and its stderr has no cleartext warning.

- [X] T011 [P] [US2] In `mcpserver/transport_test.go` add (FR-002, FR-003, FR-015):
  - `TestStartHTTPQuietOnLoopbackBind`: for `""` (the default) and `127.0.0.1:0`, `StartHTTP` with a recording `Warn` returns with no message.
  - `TestServeHTTPQuietOnLoopbackBind`: `ServeHTTP` on a free `127.0.0.1` port with a recording `Warn`; poll until a request without a token answers `401`; cancel; no message was recorded.
  - `TestHTTPFailedStartDoesNotWarn`: `StartHTTP` and `ServeHTTP` on `0.0.0.0:99999` return an error and record no message; `StartHTTP` and `ServeHTTP` on `0.0.0.0:0` with an empty token return the token error and record no message.
- [X] T012 [P] [US2] In `internal/cli/serve_test.go` add (FR-003, FR-016):
  - `TestServeMCPQuietOnLoopbackBind`: `ServeMCP` on HTTP at a free `127.0.0.1` port; poll until a request without a token answers `401`; stop; stderr holds the startup line and no `cleartext`.
  - `TestDefaultHTTPAddrIsLoopback`, failing to compile until the constant exists: `net.ResolveTCPAddr("tcp", defaultHTTPAddr)` gives an address whose IP is loopback, and `defaultHTTPAddr` is `127.0.0.1:7777`. Then, in `internal/cli/serve.go`, name the literal `127.0.0.1:7777` that `ServeMCP` applies when `--addr` is empty as the constant `defaultHTTPAddr`. No behavior changes; the test keeps the default from ever becoming a bind that warns (US2 scenario 1).
- [X] T013 [P] [US2] In `test/integration/http_bind_warning_test.go` add (FR-003, FR-013; acceptance scenarios 1 to 4):
  - `TestBindWarning_LoopbackIsQuiet`: the real binary on `--addr 127.0.0.1:PORT`; poll until `401`; stop; stderr has no `cleartext`.
  - `TestBindWarning_FailedBindIsQuiet`: with a listener held on `0.0.0.0:PORT`, the real binary on `--addr 0.0.0.0:PORT` exits with a non-zero status and stderr has no `cleartext`.
  - `TestBindWarning_AgentTaskIsQuiet`: run an `agent` task with the stub agent command that `test/integration/context_test.go` sets with `set agent_cmd`; the task exits 0 and stderr has no `cleartext`. The endpoint passes the warning callback (T009), so a quiet stderr shows that it bound loopback.
  - `TestBindWarning_StdioIsQuiet`: `rune mcp` over stdio with the `startMCP` helper; stderr has no `cleartext`.
  - `TestBindWarning_NoTokenIsQuiet`: `serve --http --addr 0.0.0.0:0` with no `--token-file` exits 2 and stderr has no `cleartext`.
- [X] T014 [US2] Run `go test ./mcpserver/ ./internal/cli/ ./test/integration/` in Docker. All green. If a quiet test fails, fix the code of T003, T008 or T009, not the test.

**Checkpoint**: The warning appears only where the token is exposed.

---

## Phase 5: User Story 3 - An embedder gets the same warning where it chooses (Priority: P2)

**Goal**: The library reports only through `HTTPConfig.Warn` and writes nothing on its own.

**Independent Test**: A unit test starts the transport with and without `Warn` on `0.0.0.0:0`.

- [X] T015 [US3] In `mcpserver/transport_test.go` add `TestHTTPNilWarnIsSilent` (FR-012): with `os.Stderr` and `os.Stdout` each replaced by the write end of a pipe for the duration of the call, `StartHTTP` on `0.0.0.0:0` with a nil `Warn` succeeds, serves a `401` to a request without a token, and nothing was written to either pipe. Also call `HTTPConfig{}.warnCleartext(&net.TCPAddr{IP: net.IPv4zero})` directly: it does not panic.
- [X] T016 [US3] Run `go test ./mcpserver/` in Docker with `-e CGO_ENABLED=1` and `-race`. Green, with no leak reported by `goleak`.

**Checkpoint**: Embedders get the decision and the text, and keep their output.

---

## Phase 6: Help, documentation and gates

**Purpose**: The help and the documentation say what happens (FR-011, FR-014), and the quality
gates pass.

- [X] T017 [P] In `cmd/rune/serve_test.go` add `TestServeHelpMentionsCleartextWarning`, failing until T018: `Long` contains `non-loopback` and `cleartext`; `Example` contains `--addr 127.0.0.1:7777` and does not contain `--addr :7777`; the usage of the `addr` flag contains `127.0.0.1:7777`.
- [X] T018 In `cmd/rune/serve.go` ([research R6](research.md)): change the HTTP example to `rune serve --http --addr 127.0.0.1:7777 --token-file ./tok`; change the `--addr` flag help to `HTTP listen address (with --http; default 127.0.0.1:7777)`; add a paragraph to `Long` after the transport paragraph: the HTTP transport has no TLS, the default address is loopback, and on any other address the bearer token is sent in cleartext and the server prints a warning.
- [X] T019 [P] In `docs/mcp.md`, in the Streamable HTTP part of "Starting the server": state the default address `127.0.0.1:7777`; state that the transport has no TLS; state that a bind is loopback when the bound address is in `127.0.0.0/8` or is `::1`, that `--addr :PORT` and `0.0.0.0:PORT` bind every interface, and that a host name counts as the address it resolves to; show the warning line; advise a loopback bind behind a TLS proxy or tunnel for remote clients. Reword the sentence that calls the endpoint "intended to be localhost-bound" so it stays true to Constitution Principle VII: the endpoint is opt-in, bound to loopback unless the operator names another address, and token-gated.
- [X] T020 [P] In `docs/cli.md`, in the `rune serve` row: add that `--addr` defaults to `127.0.0.1:7777` and that a bind beyond loopback prints a warning because the token is sent without TLS.
- [X] T021 Run the whole suite in Docker: `docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...`, then `docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./mcpserver/ ./internal/cli/ ./cmd/rune/`. All green, including `test/docs`.
- [X] T022 Run lint with the `golangci-lint:v2.14` Docker image (`golangci-lint run`) and the repository's `fmt` check. Zero issues.
- [X] T023 Walk [quickstart.md](quickstart.md) steps 2 to 6 with a binary built from the branch and confirm each expected output.
- [X] T024 Confirm coverage of the changed functions: `go test -cover -coverprofile` for `./mcpserver/` and `./internal/cli/`; `isLoopback`, `warnCleartext`, `ServeHTTP`, `StartHTTP`, `warnTransport` and `agentHTTPConfig` are each at 90% or more, and the lines changed in `ServeMCP` are covered. Two tests were added to reach that: `TestServeHTTPDefaultAddrIsQuiet` in `mcpserver/transport_test.go` and `TestServeMCPDefaultAddrIsQuiet` in `internal/cli/serve_test.go`, each on a context that is done from the start.

---

## Dependencies

- Phase 2 before every story. T002 before T003.
- US1: T005, T006, T007 before T008, T009. T008 before T009 (the field must exist).
- US2 needs T008 and T009; its tasks add tests, and T012 names one constant.
- US3 needs T008.
- Phase 6: T017 before T018. T019 and T020 are independent of code. T021 to T024 last.

## Parallel opportunities

- T005, T006 and T007 touch three different files.
- T011, T012 and T013 touch three different files.
- T017, T019 and T020 touch three different files.

## Implementation strategy

MVP is Phase 2 plus US1: the warning exists and the dead guard is gone. US2 proves the warning
stays out of the safe cases. US3 pins the library contract. Phase 6 closes documentation and
gates. One PR carries all of it.
