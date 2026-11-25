# Quickstart: Validate Spec 058

Every scenario runs in Docker, through the test suite or the built binary, in a temporary
directory, with `XDG_STATE_HOME` pointing into it. No scenario runs a recorded command.

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/observe/... ./internal/tasksuggest/... ./internal/agentinit/... ./internal/cli/... ./cmd/rune/... ./test/integration/...
```

The integration test `test/integration/suggest_test.go` carries scenarios 1 to 6 with the
real binary. By hand, with a built `rune`:

```sh
export XDG_STATE_HOME=$(mktemp -d) CLAUDE_PROJECT_DIR=$(mktemp -d)
cd "$CLAUDE_PROJECT_DIR"
```

## 1. Cold start: record, list, apply (US1, US2)

```sh
rune agent observe claude
for i in $(seq 1 14); do
  printf '{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"s1","tool_use_id":"t%s","cwd":"%s","tool_input":{"command":"go test -run TestX ./pkg/...","description":"Run the parser tests"}}' "$i" "$PWD" | rune agent record
  printf '{"hook_event_name":"PostToolUse","tool_name":"Bash","session_id":"s1","tool_use_id":"t%s","cwd":"%s","tool_input":{"command":"go test -run TestX ./pkg/...","description":"Run the parser tests"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false,"isImage":false}}' "$i" "$PWD" | rune agent record
done
rune suggest
```

Expected: `.claude/settings.local.json` holds three handlers; `rune suggest` lists one
suggestion `the agent ran this 14 times (14 succeeded) in 1 session`, task `go-test`.

```sh
rune suggest apply <ID> --dry-run   # a diff creating Runefile; no file written
rune suggest apply <ID>             # Runefile: created, added go-test
rune analyze                        # no errors, no warnings
rune suggest                        # Already tasks: go-test (14 runs)
```

## 2. Nothing runs (US1 scenario 5)

Replay 20 runs of `touch "$PWD/SENTINEL"`, then list, preview and apply. `SENTINEL` must not
exist afterwards.

## 3. Secrets (US3)

Replay runs of:

```text
API_TOKEN=abc123xyz deploy
deploy --password=hunter22 --token tok_live_9
curl -H "Authorization: Bearer zzz111" https://api.example.com
psql postgres://app:pw0rd99@db/app
echo "$DEPLOY_SECRET_VALUE"   (with DEPLOY_SECRET=s3cr3tvalue in the recorder's environment and the command holding s3cr3tvalue)
cat <<EOF > .env
```

Then `grep -r` for each value under `$XDG_STATE_HOME` and in the output of `rune suggest`,
`rune suggest apply --dry-run` and `Runefile`. Expected: no match; the here-document is
counted under `Not recorded`.

## 4. One varying word (US4)

Replay `go test -run TestX ./pkg/...` 5 times, `-run TestY` 3 times, `-run TestZ` once.
Expected: one suggestion, 9 runs, `go-test run="TestX":` with body
`go test -run {{quote(run)}} ./pkg/...`. After applying, `rune --dry-run go-test 'Test A;rm x'`
shows the value passed as one quoted word.

## 5. Gaps and review items (US5)

With a Runefile task `vet:` whose body is `go vet ./...`, replay `go vet ./...` 10 times,
`cd web && npm test` 6 times and `go test $(go list ./...)` 5 times. Expected: `vet` under
`Already tasks`; `npm test` suggested with `[working-directory("web")]`; the substitution
listed for review with no ID.

## 6. Turning it off (US6)

Put a hook of your own on `PostToolUse` in `.claude/settings.local.json`, then:

```sh
rune agent observe claude     # updated
rune agent observe claude     # unchanged, bytes identical
rune agent observe claude --remove
rune suggest forget           # deleted N records
```

Expected: your hook is still there and no handler runs `rune agent record`; the project's
directory under `$XDG_STATE_HOME/rune/observe` is gone.

## 7. Strict profile note (edge case)

With `"permissions": {"deny": ["Bash"]}` in `.claude/settings.json`, `rune agent observe
claude` prints the note that nothing will be recorded and leaves `.claude/settings.json`
unchanged.
