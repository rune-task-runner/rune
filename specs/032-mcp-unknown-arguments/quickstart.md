# Quickstart: Validating the Unknown-Argument Refusal

**Feature**: 032-mcp-unknown-arguments | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

Expected texts are defined in [contracts/mcp-tools.md](contracts/mcp-tools.md); this guide only
says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.12 golangci-lint run
```

Scenarios A to S, U, V, and W below are automated in `test/integration/unknown_args_mcp_test.go`, except the
no-arguments form of N, which is in `mcpserver/handler_test.go`. Scenario T is automated at library
level in `mcpserver/transport_test.go`. The docs harness (`test/docs`) validates the changed page.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Builds the project.
build target="debug":
    @echo built {{quote(target)}} > build.done

# Deploys the service.
deploy env:enum("staging","prod") replicas="2":
    @echo deployed {{quote(env)}} x{{quote(replicas)}} > deploy.done

# Records each name.
each +names:
    @echo {{quote_each(names)}} > each.done

# Removes caches. Takes no arguments.
clean_all:
    @echo cleaned > clean_all.done

# Prepares a release.
prep:
    @echo prep > prep.done

# Announces a release.
notify:
    @echo notify > notify.done

# Recovers from a failed release.
recover:
    @echo recover > recover.done

# Cuts a release.
release version="1": prep && notify || recover
    @echo released {{quote(version)}} > release.done

# Wipes local state.
[confirm("Wipe local state?")]
wipe:
    @echo wiped > wipe.done
```

## Scenarios

Start `rune mcp` in that directory, send the `initialize` and `notifications/initialized` frames,
and then one `tools/call`. Remove the markers between scenarios. "Refused" means `isError: true`,
no `[exit N]` line, and no marker file.

| # | Call | Expected |
|---|---|---|
| A | `build` with `{"targte":"release"}` | Refused, suggests `target` |
| B | `deploy` with `{"env":"prod","replicaz":"5"}` | Refused, suggests `replicas` |
| C | `deploy` with `{"evn":"prod"}` | Refused, suggests `env`, no complaint about a missing `env` |
| D | `release` with `{"versoin":"2"}` | Refused. No `prep.done`, `release.done`, `notify.done`, or `recover.done` |
| E | `build` with `{"zzzzzz":"1"}` | Refused, no suggestion |
| F | `clean_all` with `{"force":true}` | Refused, no suggestion |
| G | `deploy` with `{"replicaz":"5","evn":"prod"}` | Refused, two lines, `evn` first |
| H | `deploy` with `{"env":"nope","replicaz":"5"}` | Refused, the unknown-argument line only |
| I | `wipe` with `{"forcee":true}` | The destructive refusal only |
| J | `build` with `{"Target":"release"}` | Refused, suggests `target` |
| K | `build` with `{"targte":"s3cret"}` | Refused, and the text does not contain `s3cret` |
| L | `each` with `{"namez":["a","b"]}` | Refused, suggests `names` |

These behave as they do today:

| # | Call | Expected |
|---|---|---|
| M | `build` with `{"target":"release"}` | `build.done` holds `built release` |
| N | `build` with `{}`, `null`, or no arguments | `build.done` holds `built debug` |
| O | `each` with `{"names":["a","b"]}` | `each.done` exists |
| P | `deploy` with `{}` | `task "deploy" is missing required argument "env"` |
| Q | `build` with `[]` | `invalid arguments: json: cannot unmarshal ...` |
| R | `release` with `{"version":"2"}` | `prep.done`, `release.done`, and `notify.done` exist |

## Tool list

| # | Check | Expected |
|---|---|---|
| S | `tools/list` on stdio | Every `inputSchema` has `"additionalProperties":false`, including `clean_all` and `wipe` |
| T | `rune serve --http --token-file tok`, then scenario A and `tools/list` with the token | The same text and the same schemas as on stdio |

## Other server setups

| # | Setup | Call | Expected |
|---|---|---|---|
| U | `rune mcp --allow-destructive wipe` | `wipe` with `{"forcee":true}` | Refused, no suggestion, no `wipe.done` |
| V | Add `mod tools "tools.rune"` to the Runefile, and a `build target="debug"` task to `tools.rune` | `tools__build` with `{"targte":"x"}` | Refused, names `tools::build`, suggests `target` |
| W | Add `rm path:` with a body that writes `rm.done` | `rm` with `{"path":null}` | `invalid arguments: parameter "path": null is not a valid value`, no `rm.done` |
