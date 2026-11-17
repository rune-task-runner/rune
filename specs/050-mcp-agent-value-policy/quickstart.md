# Quickstart: Validating the Agent Value Policy

**Feature**: 050-mcp-agent-value-policy | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

The attribute and `RUNE2019` are in
[contracts/agent-allow-attribute.md](contracts/agent-allow-attribute.md), the tools and texts in
[contracts/mcp-tools.md](contracts/mcp-tools.md), the library field in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md). This guide only says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run
```

Scenarios A to T are automated in `test/integration/agent_policy_mcp_test.go`, against the built
binary with an SDK client. A to K and N to R run on stdio and on `rune serve --http`, the rest on
stdio. U is
automated in `internal/cli/agentpolicy_test.go`. V is automated in
`internal/analyzer/agent_allow_test.go` and, for one row, at binary level. W and X are automated
at binary level on stdio. The docs harness
(`test/docs`) validates the changed pages.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Deploy the service.
[agent-allow(env="staging")]
deploy env:enum("staging","prod"):
    @echo {{env}} > deploy.done

# Roll out to one region.
[agent-allow(env="staging", region="eu-west-1")]
rollout env:enum("staging","prod") region="eu-west-1":
    @echo {{env}}-{{quote(region)}} > rollout.done

# Deploy to production by default.
[agent-allow(env="staging")]
publish env:enum("staging","prod")="prod":
    @echo {{env}} > publish.done

# Run the tests.
test:
    @echo tested > test.done

# Preview on staging.
preview: test (deploy "staging")
    @echo previewed > preview.done

# Ship to production.
ship: test (deploy "prod")
    @echo shipped > ship.done

# Deploy where the caller says.
promote target: (deploy target)
    @echo promoted > promote.done

# Announce, then deploy production.
announce: && (deploy "prod")
    @echo announced > announce.done

# Try, deploying production on failure.
attempt: || (deploy "prod")
    @echo attempted > attempt.done

# Deploy by the state of a file.
nightly: test (deploy if path_exists("prod.flag") == "true" { "prod" } else { "staging" })
    @echo nightly > nightly.done

# Deploy to a setting that is missing.
broken: test (deploy env("RUNE_NO_SUCH_SETTING"))
    @echo broken > broken.done

# Release for real.
[confirm("Release for real?")]
[agent-allow(env="staging")]
release env:enum("staging","prod"):
    @echo {{env}} > release.done

# Tag a build.
[agent-allow(channel="beta")]
tag channel name="nightly":
    @echo {{quote(channel)}}-{{quote(name)}} > tag.done

# Deploy production from a body.
script:
    @echo scripted > script.done
    @"${RUNE_BIN:-rune}" deploy prod
```

## Scenarios

Served with `rune mcp` unless a row says otherwise. "Refused" means `isError: true`, the text of
[contracts/mcp-tools.md](contracts/mcp-tools.md), and no marker file.

| # | Setup and call | Expected |
|---|---|---|
| A | `deploy` with `env=staging` | Runs. `deploy.done` holds `staging` |
| B | `deploy` with `env=prod` | Refused: `task "deploy" is not allowed for agents with env="prod"; agent policy requires env="staging"` |
| C | `rollout` with `env=staging`, `region=us-east-1` | Refused, showing `region="us-east-1"` and both predicates |
| D | `rollout` with `env=staging` only | Runs. `rollout.done` holds `staging-eu-west-1` |
| E | `publish` with no arguments | Refused, showing `env="prod"`. No `publish.done` |
| F | `publish` with `env=staging` | Runs |
| G | `ship` | Refused: `task "ship" runs "deploy", which is not allowed for agents with env="prod"; ...`. No `test.done` |
| H | `preview` | Runs. `test.done`, `deploy.done` and `preview.done` exist |
| I | `promote` with `target=prod`, then with `target=staging` | Refused, then runs |
| J | `announce` | Refused before the body. No `announce.done` |
| K | `attempt` | Refused, though the task would not fail. No `attempt.done` |
| L | `nightly` | Refused: the value of `env` is not known before the run. No `test.done` |
| M | `broken` | Refused: the arguments could not be worked out. No `test.done` |
| N | `deploy` with `evn=prod` | The unknown-argument error of spec 032, with the hint `env` |
| O | `rune mcp --allow-destructive release`, `release` with `env=prod` | Refused for the value. No `release.done` |
| P | No grant, a client that approves every request, `release` with `env=prod` | Refused for the value. The client received no request |
| Q | The same client, `release` with `env=staging` | One request, then `release.done` holds `staging` |
| R | `tag` with `channel=stable`; then with `channel=beta` and `name=x` | Refused; then runs and `tag.done` holds `beta-x` |
| S | `tools/list` | `deploy` and `tag` carry the one-value `enum` and the policy paragraph. `test` is unchanged |
| T | Command line: `rune deploy prod`, `rune ship`, `rune publish` | Each runs with `prod` |
| U | The endpoint of an `agent` task: `deploy` with `env=prod` | Refused |
| W | `script`, with `RUNE_BIN` naming the test binary | The call returns exit 1, its stderr holds the refusal, `script.done` exists and `deploy.done` does not. `rune script` on the command line deploys `prod` |
| X | `tools/list` | `publish` lists `env` as required. `rollout` does not list `region` |
| V | Each invalid declaration of the attribute contract, with `rune analyze` | Exit 3 and `error[RUNE2019]:` with the row's text |

Further checks in the unit tests: a secret value in a refused argument is masked; a 200-character
value is cut; a value with a line break stays on one line; a module task and a private task with a
policy are checked when reached; a task unavailable on this host is skipped; a Runefile with no
`[agent-allow]` gives the same tool JSON as before.

## Documentation checks

- `docs/runefile.md` lists `[agent-allow(...)]` and explains it.
- `docs/GRAMMAR.md` has the alternative and the `AllowPred` rule.
- `docs/diagnostics.md` has the `RUNE2019` row.
- `docs/mcp.md` has the section on the policy, the order of checks, and the limits.
