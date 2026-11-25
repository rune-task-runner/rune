# Quickstart: validating receipts end to end

A run guide that proves the feature works. Formats are in [contracts/](contracts/); the model is
in [data-model.md](data-model.md). Run Go tests in Docker only:

```sh
docker compose run --rm -e GOFLAGS=-buildvcs=false test go test ./internal/receipt/... ./internal/cli/... ./test/integration/... -run 'Receipt|Verify'
```

The manual walk below uses a built `rune` (`go build -o dist/rune ./cmd/rune` in the container)
and a scratch repository.

## 1. Record

```sh
mkdir demo && cd demo && git init -q && git config user.name t && git config user.email t@example.com
cat > Runefile <<'EOF'
set receipts

build:
    echo built > out.txt

test: build
    echo ok
EOF
printf 'out.txt\n' > .gitignore
git add -A && git commit -qm init
rune test
ls .rune/receipts/
```

Expected: a notice that the signing key was created (first time only), two `*.json` receipts and
a `.gitignore`. Decoding the payload of the `test` receipt shows `status: passed`, `exitCode: 0`,
`channel: cli`, `source.before == source.after == git rev-parse HEAD^{tree}`, and
`related.dependencies` holding the `build` receipt ID.

## 2. Verify

```sh
rune verify HEAD test build ; echo $?      # both passed at <short>, working tree matches; 0
rune verify HEAD lint ; echo $?            # lint: not verified: no receipt; 1
rune verify HEAD --json test | jq .verified   # true
```

## 3. Tamper and mismatch

| Step | Expected verify result |
|---|---|
| flip one character of the payload in the `test` receipt | `bad-signature`, exit 1 |
| `echo x >> Runefile && git commit -qam change`, verify `HEAD` | `different-snapshot` |
| `echo y > new.txt` (untracked), run `rune test`, verify the clean `HEAD` | `different-snapshot` (the run's snapshot holds `new.txt`) |
| commit `new.txt`, verify the new `HEAD` | passed "on a snapshot identical to" it |
| a task whose body runs `echo z >> tracked.txt` | `changed-during-run` |
| `exit 3` in the body | `failed`, exit code 3 recorded |
| `[cache]` task run twice, only the second receipt kept | `cached` |
| verify with a receipt from another machine's key and no `--key` | `unknown-signer` |

## 4. Keys

```sh
rune receipt key > me.pub                          # PEM; key ID on stderr
openssl genpkey -algorithm ed25519 -out ci.pem
RUNE_RECEIPT_KEY="$(cat ci.pem)" rune receipt key > ci.pub
```

A task `env` with receipts on, run with `RUNE_RECEIPT_KEY` set, prints no `RUNE_RECEIPT_KEY`.
The receipt it writes verifies with `--key ci.pub` from a shell without the variable.

## 5. Notes round trip

```sh
git init -q --bare ../remote.git && git remote add origin ../remote.git && git push -q origin HEAD
git notes add -m unrelated HEAD                       # refs/notes/commits
rune receipt attach HEAD                              # attached N receipts ...
git push -q origin refs/notes/rune-receipts
cd .. && git clone -q remote.git clone && cd clone
git fetch -q origin refs/notes/rune-receipts:refs/notes/rune-receipts
rune verify HEAD test --key ../demo/me.pub ; echo $?  # passed, from the note; 0
```

In `demo`, `git notes show HEAD` still prints `unrelated`. Copying the note to a commit with a
different tree (`git notes --ref=rune-receipts copy HEAD OTHER`) and verifying `OTHER` gives
`different-snapshot`.

## 6. Agents and CI

- `rune serve` with receipts on, a tool call to `test`: the receipt's channel is `mcp`, its
  invocation ID equals the call's ID in `rune log`, and `claims.client` holds the client name.
- `rune ci run test`: channel `ci-run`.
- With the sandbox enforced, a task that lists `~/.local/state/rune/receipts` sees it empty.

## 7. Secrets

With `TOKEN=s3cr3t-fixture` in the environment and passed as an argument, `grep -r s3cr3t-fixture
.rune/receipts` finds nothing, `git notes --ref=rune-receipts show HEAD` holds none, and neither
does `rune verify --json`.

## 8. Off by default

Without `set receipts`, the same runs create no `.rune/receipts/` and no key, and the existing
golden and integration tests pass unchanged.

## Results (2026-11-25, built binary in the Docker test container)

| Step | Result |
|---|---|
| 1. Record | Key created with the notice; two receipts written, one per task |
| 2. Verify | `test` and `build` passed at the commit, exit 0; `lint` "no receipt", exit 1; `--json` reports `"verified": true` |
| 3. Tamper | A changed signature byte gives "the signature does not match the receipt", exit 1 |
| 4. Keys | `rune receipt key` prints the PEM; the key ID and source go to stderr |
| 5. Notes | Attach added 2 receipts; a clone that fetched the notes ref verified `test` with `--key` alone; the unrelated note is unchanged |
| 6. CI | `rune ci run build` wrote one receipt with channel `ci-run` |
| 8. Off | A Runefile without the setting creates no `.rune` directory |

Steps 3 (other tamper fixtures), 6 (MCP and sandbox) and 7 are covered by the automated tests
in `test/integration/receipts_test.go`, `internal/cli/mcp_receipts_test.go` and
`test/integration/receipts_sandbox_unix_test.go`.
