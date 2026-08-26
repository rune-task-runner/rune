# Dependencies & post-hooks

> **Use case:** order work correctly — some tasks must run before, and some after, a task —
> and diagnose failures automatically.

**Demonstrates:** dependencies, post-hooks, failure hooks  ·  **Guide:** [Dependencies](../../runefile.md#dependencies-and-post-hooks)

**Prerequisites:** none

## Run it

```sh
rune deploy
```

## Expected output

```text
building
testing
deploying
notify: deployed ✓
```

`deploy: build test && notify` — `build` and `test` are dependencies (run first); `notify` is
a post-hook (runs after `deploy` succeeds). Each task runs at most once per invocation.

Then try the failure hook — `rune flaky` fails on purpose and runs its post-mortem:

```text
checking
diagnosing: diagnose
diagnose: flaky failed with exit 3
```

The invocation still exits non-zero (hooks never change the exit code), and over MCP the
diagnostic reaches the agent as a *fix suggestion* in the same tool response.

## How it works

See the `Runefile`: prerequisites go after the colon, post-hooks after `&&`, failure hooks
after `||`. The hook body reads `RUNE_FAILED_TASK` and `RUNE_FAILED_EXIT_CODE` from its
environment.
