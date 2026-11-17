# Quickstart: Validating Approval in the Client

**Feature**: 049-mcp-confirm-elicitation | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

The request and the texts are in [contracts/approval-request.md](contracts/approval-request.md) and
[contracts/mcp-tools.md](contracts/mcp-tools.md), the library changes in
[contracts/mcpserver-api.md](contracts/mcpserver-api.md). This guide only says how to check them.

## Automated gates (Docker only)

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./...
docker compose run --rm -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 test go test -race ./...
docker run --rm -v "$PWD":/app -w /app -e GOFLAGS=-buildvcs=false golangci/golangci-lint:v2.14 golangci-lint run
```

Scenarios A to W3 below are automated in `test/integration/approval_mcp_test.go`, against the built
binary, with an SDK client whose elicitation handler plays the person. Each runs on stdio; A, E, F,
H and P also run on `rune serve --http`. X is automated in `internal/cli/mcp_test.go`, Y in
`mcpserver/approval_test.go` and, over HTTP, in `mcpserver/transport_test.go`. The docs harness (`test/docs`) validates the changed pages.

## Scratch Runefile

Every body writes a marker file, so "nothing ran" is checkable.

```rune
# Builds the artifacts.
build:
    @echo built > build.done

# Applies the plan to an environment.
[confirm("Apply to prod? 3 resources change.")]
apply env region="eu":
    @echo {{env}}-{{region}} > apply.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Wipes everything.
[confirm]
wipe:
    @echo wiped > wipe.done

# Cuts a release.
release: build clean
    @echo released > release.done

# Publishes, then cleans.
publish: && clean
    @echo published > publish.done

# Deploys, wiping on failure.
deploy: || wipe
    @echo deployed > deploy.done

# Resets, which also wipes.
[confirm("Reset?")]
reset: wipe
    @echo reset > reset.done

# Cleans and wipes.
full: build clean wipe
    @echo full > full.done

# Ships after a private scrub.
ship: _scrub
    @echo shipped > ship.done

[private]
[confirm("Scrub?")]
_scrub:
    @echo scrubbed > scrub.done

# Notes something.
[confirm("Note it?")]
note text:
    @echo {{text}} >> note.done
```

## Scenarios

"Asked" means the client's handler received exactly one request for the call. "Nothing ran" means
no `*.done` file exists.

| | Server flags | Client | Call | Handler answers | Expect |
|---|---|---|---|---|---|
| A | none | form | `apply env=prod` | approve | Asked before any marker exists. The message has `Task: apply`, `- env: "prod"`, `- region: "eu"`, `- apply: Apply to prod? 3 resources change.` `apply.done` holds `prod-eu`. Result `[exit 0]`. |
| B | none | form | `apply env=prod`, twice | approve, approve | Asked twice. |
| C | none | form | `apply env=staging`, then `apply env=prod` | approve, decline | The second request shows `prod`. `apply.done` holds `staging-eu`. |
| D | none | form | `wipe` | approve | The message has `- wipe: Run "wipe"?`. `wipe.done` exists. |
| E | none | form | `release` | approve | One request, before `build.done` exists, with `Task: release` and `- clean: Delete dist?`. Afterwards `build.done`, `clean.done`, `release.done` exist. |
| F | none | form | `release` | decline | Nothing ran. Text: declined. |
| G | none | form | `publish`; `deploy` | approve | `publish` asks about `clean` before `publish.done` exists. `deploy` asks about `wipe`, and `wipe.done` does not exist afterwards because `deploy` succeeded. |
| H | none | form | `reset` | approve | One request names `reset` then `wipe`. Both markers exist. |
| I | none | form | `ship` | approve | The request names `_scrub`. `scrub.done` and `ship.done` exist. `_scrub` is not listed as a tool. |
| J | `--allow-destructive clean` | form | `full` | approve | The request names only `wipe`. All four markers exist. |
| K | `--allow-destructive clean` | form | `release`; `clean` | (none) | Not asked. Both run. |
| L | none | form | `release` | dismiss | Nothing ran. Text: dismissed, then how to run it without a request: `add clean to --allow-destructive` and `run "rune release" themselves`. |
| M | none | form | `release` | accept with `approved: false` | Nothing ran. Text: approval was not given. |
| N | none | form | `release` | accept with no content; action `sure` | Nothing ran. Text: no valid answer. |
| O | none | form | `release` | returns an error | Nothing ran. Text: no valid answer. |
| P | none | no elicitation | `apply env=prod` | (none) | Not asked. Nothing ran. The fallback text for `apply`. |
| Q | none | URL only | `release` | (none) | Not asked. Nothing ran. The fallback text names `clean` and `rune release`. |
| R | none | `elicitation: {}` | `clean` | approve | Asked. `clean.done` exists. |
| S | none | form | `apply` with `bogus=1`; `apply` with no `env`; `apply` with `env` set to a JSON object | (none) | Not asked. The unknown-argument error; the missing-argument error; the invalid-value error. Nothing ran. |
| S2 | `--deny apply` | form | `apply env=prod` | (none) | Not asked. The unknown-tool error a hidden task gets. Nothing ran. |
| S3 | `--deny clean` | form | `release` | approve | Asked. The request names `clean`. `build.done`, `clean.done`, `release.done` exist: `--deny` hides the tool `clean`, not what `release` runs. |
| T | none | form | `apply env=prod approved=true` | (none) | Not asked. Unknown argument `approved`. |
| U | none | form | two `note` calls at once, `text=a` and `text=b` | approve the one whose `text` is `a`, decline the other | `note.done` holds exactly `a`; one result is `[exit 0]`, the other declined. |
| V | none | form | `note` with `text` = the value of a secret in the server's environment | approve | The request and the result do not contain the value. |
| V2 | none | form | `note` with a `text` of 10,000 characters | (none) | Not asked. Nothing ran. The fallback text with the size reason. |
| W | none | form | `clean`; while the request is open, append a task to the Runefile and wait for `reloaded Runefile` | approve | Nothing ran. Text: the Runefile changed. |
| W2 | none | form | `clean`; while the request is open, save a Runefile with a syntax error and wait for `reload failed` | approve | `clean.done` exists: the server still serves what the request showed. |
| W3 | `--no-reload` | form | `clean`; while the request is open, append a task to the Runefile | approve | `clean.done` exists: this server never reloads. |
| X | the agent task endpoint | form | `clean` | (none) | Not asked. `task "clean" is destructive and is not allowed in this session`. |
| Y | library, `ApprovalTimeout` 50 ms | form | `clean` | answers after 500 ms with approve | Nothing ran. Text: no answer arrived within 50ms. |

## By hand, with a real client

With a client that states form elicitation (Claude Code and Codex CLI do, per the recorded
sessions):

1. In a scratch directory with the Runefile above, register `rune mcp` with the client.
2. Ask the agent to call `apply` with `env` set to `prod`.
3. The client shows Rune's request. Approve it. `apply.done` exists.
4. Ask for `apply` again. The client asks again. Decline. `apply.done` is unchanged.

What each client shows is recorded in the compatibility table of `docs/mcp.md`.
