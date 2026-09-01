# Quickstart Validation: Typed Parameter Schemas and Outcome Descriptions

**Feature**: 023-mcp-typed-schemas · run these end-to-end checks to prove the feature.
Contracts referenced: [grammar](contracts/grammar.md) · [mcp-schema](contracts/mcp-schema.md) ·
[diagnostic-codes](contracts/diagnostic-codes.md)

## Prerequisites

- Repo checkout on `023-mcp-typed-schemas`, Docker running (tests run **only** in the
  harness — global policy).
- Build once: `go build -o /tmp/rune ./cmd/rune` (host build is fine; *tests* are Docker-only).

## Scenario fixture

Save as `Runefile` in a scratch dir:

```rune
# Deploy the service.
[returns("URL of the deployed environment on stdout")]
[param-doc("env", "Target environment")]
deploy env:enum("staging","prod") replicas:number="2":
    @echo "deploying {{env}} x{{replicas}}"

test +packages:path:
    @echo "testing {{packages}}"
```

## 1. Schema surfaces to agents (US1 / SC-001)

```sh
{ printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"qs","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; sleep 1; } | /tmp/rune mcp
```

(The `sleep` holds stdin open — the stdio session drops pending replies on EOF.)

**Expect** in the `deploy` tool: `"enum":["staging","prod"]` on `env`,
`"type":"number"` on `replicas`, `"description":"Target environment"`, and the tool
description ending `Returns: URL of the deployed environment on stdout`. The `test` tool's
`packages` is `{"type":"array","items":{"type":"string","format":"path"}}`.

## 2. Rejection before execution (US2 / SC-002)

```sh
/tmp/rune deploy production          # CLI path
echo $?                              # expect: 2 (usage), stderr names env, "production",
                                     #         and the allowed values; no "deploying" line
/tmp/rune deploy staging 2.5         # number accepts decimals → runs
/tmp/rune deploy staging abc         # → rejected: replicas, "abc", expected number
```

MCP path: send `tools/call` for `deploy` with `{"env":"production"}` through the pipe
above — **expect** an error result carrying the same sentence, and no `deploying` output.

## 3. Static authoring diagnostics (US4 / SC-005)

Each snippet must fail `rune analyze` (exit 3) with the given code, a `file:line:col`
caret span, and nothing executed. (`rune analyze` prints the `[RUNE####]` codes; the
default runner shows the same message and caret without the code string, per the
diagnostics contract.)

| Snippet | Code |
|---|---|
| `deploy env:enun("a"):` | RUNE2012 (lists supported kinds) |
| `deploy env:enum():` | RUNE1006 |
| `deploy env:enum("a","a"):` | RUNE2013 |
| `deploy env:enum("a","b")="c":` | RUNE2014 |
| `[param-doc("nope","x")]` above `deploy env:` | RUNE2015 |
| two `[returns("…")]` lines above one task | RUNE2015 |
| `deploy env:string:` while a task named `string` exists | RUNE2016 (warning — exit 0, build proceeds) |
| `deploy env:build` (legacy unspaced header, no second colon) | RUNE1005 with the "add a space" fix |

## 4. Zero change for legacy files (SC-003)

```sh
git stash && /tmp/rune --dump --format json > /tmp/before.json   # any pre-feature Runefile
# rebuild with feature, same file:
/tmp/rune --dump --format json > /tmp/after.json
diff /tmp/before.json /tmp/after.json                            # expect: identical
```

Also: `rune fmt` on an unannotated file is a no-op; `tools/list` output is byte-identical.

## 5. Formatter + dump round-trip (FR-012)

```sh
/tmp/rune fmt        # annotated fixture above is already canonical → no diff
/tmp/rune --dump --format json | jq '.tasks[0].params[0]'
# expect: {"name":"env","kind":"required","type":"enum","values":["staging","prod"],
#          "description":"Target environment"}
/tmp/rune --dump --format json | jq '.tasks[0].returns'
```

## 6. Full gate set (must be green before merge)

```sh
docker-compose run --rm test go test ./...
docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...
rune lint && rune docs-check      # golangci-lint clean; docs examples/links tested
go test ./test/corpus             # (inside harness) corpus drift is deliberate + regenerated
```

Same-PR obligations (Engineering Constraints): `docs/GRAMMAR.md`, `docs/runefile.md`,
`docs/mcp.md`, `docs/diagnostics.md`, `specs/011-rune-lsp/contracts/diagnostic-codes.md`,
`docs/examples/parameters/`, release-notes entry for the unspaced-header break
(plan.md Complexity Tracking).
