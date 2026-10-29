# Quickstart: validating interactive-only `[confirm]` approval

**Feature**: [spec.md](spec.md) | **Contract**: [contracts/confirm-cli.md](contracts/confirm-cli.md)

## Prerequisites

- Docker with standalone `docker-compose` (tests never run on the host).
- A built binary for the manual checks: `go build -o /tmp/rune ./cmd/rune`
  (or `rune build`).

## Automated gates

```sh
docker-compose run --rm test go test ./internal/cli/... ./test/integration/...
rune test-race     # full suite with -race, in Docker
rune docs-check    # docs harness, in Docker
rune lint          # gofmt, go vet, golangci-lint
```

All must pass. The pseudo-terminal tests run inside the Linux container; CI
also runs them on macOS and skips them on Windows by build tag.

## Manual scenarios

Use this Runefile in an empty directory:

```rune
[confirm("Apply to live infrastructure?")]
apply:
    @echo would-apply

deploy: apply
    @echo deployed

[confirm]
echo-stdin:
    cat
```

### A. Piped input never approves (Story 1)

```console
$ echo y | rune apply; echo "rc=$?"
rune: task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve
rc=1
$ yes | rune deploy; echo "rc=$?"          # no would-apply, no deployed
$ rune apply </dev/null; echo "rc=$?"      # same banner, rc=1
```

Expected: no `would-apply` or `deployed` output, no `[y/N]` prompt, exit 1.

### B. A person at a terminal still approves (Story 2)

Run in an interactive shell:

```console
$ rune apply
Apply to live infrastructure? [y/N] y
would-apply
$ rune apply > out.log      # answer y; out.log contains would-apply
$ rune apply                # answer n, or press Enter: declined, rc=1, no banner
```

On Windows, repeat `rune apply` in PowerShell, `cmd.exe`, and Git Bash
(MSYS terminal): answering `y` runs the task; `echo y | rune apply` declines.

### C. `--yes` approves and leaves stdin alone (FR-006, SC-004)

```console
$ printf 'payload' | rune --yes echo-stdin
payload
```

No prompt and no approval notice.

### D. Plan modes and MCP are unchanged (FR-010)

```console
$ echo y | rune --dry-run deploy; echo "rc=$?"   # would run: apply / deploy, rc=0
$ echo y | rune --summary deploy; echo "rc=$?"   # apply / deploy, rc=0
```

The MCP adapter suite (`internal/cli/mcp_test.go`, `failhook_test.go`) passes
without modification.

### E. Docs and help (Story 4)

```console
$ rune --help | grep -- --yes
```

Shows the contract's help line. `docs/cli.md`, `docs/runefile.md`, and the
editor hover for `[confirm]` state the terminal requirement.
