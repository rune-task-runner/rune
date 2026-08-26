# Quickstart: Validating Post-Mortem Diagnostics

**Feature**: 022-post-mortem-diagnostics · **Date**: 2026-08-25

Runnable scenarios proving the feature end-to-end. Contracts referenced:
[grammar](contracts/grammar.md), [MCP result](contracts/mcp-result.md),
[failure env](contracts/failure-env.md).

## Prerequisites

- Repo checked out on `022-post-mortem-diagnostics`; Docker (Rancher Desktop)
  running — tests execute in Docker only.
- Build a local binary for manual poking: `go build -o /tmp/rune ./cmd/rune`.

## Scenario A — CLI: failing task fires the hook, exit code preserved (US2)

`/tmp/qs-a/Runefile`:

```rune
test: || diagnose
    @echo "running tests"
    exit 7

diagnose:
    @echo "SUGGESTION: check the fixtures"
```

```sh
cd /tmp/qs-a && /tmp/rune test; echo "exit=$?"
```

**Expected**: stdout shows `running tests` and the hook output `SUGGESTION:
check the fixtures`; stderr shows a `diagnosing: diagnose` line before it;
final line `exit=1` — Rune's standard task-failure code, exactly what the run
would exit with if no hook were declared (hooks never alter it; the body's
real code 7 reaches the hook as `RUNE_FAILED_EXIT_CODE`). Re-run with
`exit 7` removed → task succeeds, **no** `diagnosing:` line appears.

## Scenario B — failure context env (US3, failure-env contract)

```rune
test: || diagnose
    exit 2

lint: || diagnose
    exit 5

diagnose:
    @echo "failed=$RUNE_FAILED_TASK code=$RUNE_FAILED_EXIT_CODE"
```

Run `/tmp/rune test` → `failed=test code=2`. Run `/tmp/rune lint` →
`failed=lint code=5`. Same hook task, correct per-failure context.

## Scenario C — MCP: fix-suggestion section in the tool result (US1, mcp-result contract)

Serve `/tmp/qs-a/Runefile` via `/tmp/rune mcp` (alias of `rune serve`, stdio
transport) and call the `test` tool (any MCP client; in CI this is the
`test/integration` MCP harness).

**Expected result text** (one content block, `IsError: true`):

```text
running tests
[exit 7]
[fix suggestion - from Runefile || failure hooks]
SUGGESTION: check the fixtures
```

Calling a succeeding task yields today's byte-identical format with no
section.

## Scenario D — static analysis gates (FR-010/011)

```rune
test: || missing
    exit 1
```

`/tmp/rune test` → exits 3 before running anything, RUNE2001 with caret at
`missing`. Also verify: `a: || a` → RUNE2003 cycle; `t: || agentic` where
`agentic` is an `(agent)` task → RUNE2011.

## Scenario E — Ctrl-C fires nothing (clarification Q4)

Task body `sleep 30` with a `|| diagnose` hook; run, hit Ctrl-C.
**Expected**: exit 130, no `diagnosing:` line, no hook output.

## Automated gate set (must all pass)

```sh
docker-compose run --rm test go test ./...                      # unit + golden + corpus
docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...
docker-compose run --rm test go test ./test/integration/...    # binary-level
docker-compose run --rm test go test ./test/docs/...           # docs harness
rune lint                                                       # golangci-lint clean
rune fmt                                                        # incl. new || canon
```

Golden regeneration, only when a diff is intentional:
`go test ./test/corpus -update` (and the package-local `-update` flags for
lexer/parser/fmt goldens).

## Success-criteria spot checks

| SC | Verified by |
|---|---|
| SC-001 (zero extra agent round-trips) | Scenario C — suggestion in same response |
| SC-002 (exit code preserved) | Scenarios A, E + scheduler unit tests |
| SC-003 (success path untouched) | Scenario A re-run + corpus |
| SC-004 (zero corpus regressions) | `test/corpus` in gate set |
| SC-005 (static rejection with span) | Scenario D |
