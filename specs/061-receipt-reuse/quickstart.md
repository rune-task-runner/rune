# Quickstart: Validate Receipt Reuse

Prerequisites: a `rune` built from this branch, `git`, `openssl`. Run in a scratch directory.
Automated versions of these steps live in `test/integration/reuse_test.go`; see
[contracts/cli.md](contracts/cli.md) for the exact notices.

Keys live outside the repository: an untracked key file would be part of the snapshot.

## 1. A project with a reusable task

```sh
openssl genpkey -algorithm ed25519 -out ci.pem
RUNE_RECEIPT_KEY="$(cat ci.pem)" rune receipt key > ci.pub
git init -q reuse-demo && cd reuse-demo
cat > Runefile <<'EOF'
set receipts

mode := "debug"

# A task whose result depends only on the files.
[reuse]
check:
    echo {{mode}} >> ran.log
EOF
echo 'ran.log' > .gitignore
git add -A && git -c user.name=t -c user.email=t@e commit -qm init
export RUNE_RECEIPT_KEY="$(cat ../ci.pem)"
```

## 2. Record, then reuse

```sh
rune check                                   # runs; ran.log has 1 line
rune --reuse-key ../ci.pub check
```

Expected: `reused: check (receipt ..., signed by SHA256:...)`, exit 0, `ran.log` still has 1
line, and the newest receipt in `.rune/receipts/` has `"status":"cached"` and a `reuses` naming
the first. `rune verify --key ../ci.pub HEAD check` verifies the first run.

## 3. No reuse without the flag

```sh
rune check                                   # runs, prints nothing about reuse
```

## 4. Bad evidence runs the task

Each of these runs the body and prints `not reused: check (...)`:

```sh
sleep 2; rune --reuse-key ../ci.pub --reuse-max-age 1s check   # stale
rune --reuse-key ../ci.pub mode=release check                   # overridden
openssl genpkey -algorithm ed25519 -out ../other.pem
RUNE_RECEIPT_KEY="$(cat ../other.pem)" rune receipt key > ../other.pub
rune --reuse-key ../other.pub check                             # untrusted key
echo change > extra.txt; rune --reuse-key ../ci.pub check; rm extra.txt   # other files
```

## 5. Notes from another commit with the same tree

```sh
rune check
rune receipt attach --key ../ci.pub
git -c user.name=t -c user.email=t@e commit -q --allow-empty -m 'same tree'
rm -rf .rune/receipts
rune --reuse-key ../ci.pub check             # reused from the note of the previous commit
```

## 6. Analyzer

```sh
printf 'set receipts\n\n[reuse, confirm]\ndeploy:\n    echo x\n' > ../bad.Runefile
rune -f ../bad.Runefile deploy               # RUNE2022, exit 3
```

## 7. Usage errors

```sh
rune --reuse-max-age 1h check                # exit 2: needs --reuse-key
rune --reuse-key missing.pub check           # exit 2
```
