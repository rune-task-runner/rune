# Quickstart: validating `rune agent init`

Runnable checks that the feature works end to end. The contracts are
[rune-agent-init.md](contracts/rune-agent-init.md) and
[client-files.md](contracts/client-files.md).

## Prerequisites

- Docker, for the test suite: `docker compose run --rm -e GOFLAGS=-buildvcs=false test ...`
- A built binary for the manual scenarios: `go build -o /tmp/rune ./cmd/rune` inside the
  container, or `rune build`.

## 1. The test suite

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/agentinit/... ./internal/cli/... ./cmd/rune/... ./test/integration/... ./test/docs/...
```

Expected: all packages pass. The golden test of `internal/agentinit` covers, per client: a new
project, other servers and unknown keys, comments, existing deny rules, some rules present,
conflicts with and without `--force`, Windows line endings, an empty file, and a path with
spaces.

## 2. A new project, each client

```sh
mkdir -p "/tmp/agent init demo" && cd "/tmp/agent init demo"
printf 'build:\n    @echo build-ok\n' > Runefile
rune agent init claude --dry-run     # prints two differences, writes nothing
ls -a                                # only Runefile
rune agent init claude
rune agent init codex
rune agent init vscode
```

Expected: `.mcp.json`, `.claude/settings.json`, `.codex/config.toml`, `.vscode/mcp.json` and
`.github/agents/rune.agent.md` exist with the content of the contract. The registered path is
`/tmp/agent init demo/Runefile`. The `vscode` run reports `.mcp.json: unchanged` and the
outcome "Registered only", and exits 1.

## 3. A second run writes nothing

```sh
rune agent init claude; rune agent init codex
```

Expected: every file line says `unchanged`. File modification times do not change.

## 4. A conflict

```sh
sed -i 's/"--no-reload"/"--allow-destructive", "clean"/' .mcp.json
rune agent init claude; echo "exit $?"
```

Expected: a diagnostic with `.mcp.json`, a line, a column and a caret, both values, `--force`
named, `agent init: 1 conflict(s), nothing written`, exit 2. With `--force` the arguments are
replaced.

## 5. A deny rule is never removed

```sh
# add "mcp__rune" to permissions.deny in .claude/settings.json by hand
rune agent init claude --force; echo "exit $?"
```

Expected: exit 2, the rule is named, the file is unchanged.

## 6. `--check`

```sh
rune agent init codex --check; echo "exit $?"      # complete: every entry present, exit 0
PATH=/nonexistent /full/path/to/rune agent init codex --check; echo "exit $?"   # the same: no client is needed
sed -i '/shell_tool/d' .codex/config.toml
rune agent init codex --check; echo "exit $?"      # features.shell_tool: missing, exit 1
```

## 7. Nothing outside the project

```sh
rm -rf .claude && mkdir /tmp/elsewhere && ln -s /tmp/elsewhere .claude
rune agent init claude; echo "exit $?"
ls /tmp/elsewhere
```

Expected: exit 2, the message names `.claude` and `/tmp/elsewhere`, and `/tmp/elsewhere` is
empty.

## 8. The outcome follows the version

With a client version that is in the adapter's checked list the outcome line is
`outcome: Rune-only, pending N steps` and the exit status is 0. With a program named `claude`
on `PATH` that prints `9.9.9`, or with none, it is `outcome: Registered only, pending N steps`, with the
`reason:` and `remains:` lines, and exit 1. The files are the same in both cases.

## 9. The recorded client runs

For each version this feature adds to an adapter's list, `observations/README.md` has a run in
a trusted project with only the files the command wrote: the client's own tool listing holds
no command-running tool (for Codex, a capture of the request), Rune's tools are there, a Rune
call ran, and a subagent had no command-running tool. The
runs use a model through the maintainer's account and are not part of the test suite.
