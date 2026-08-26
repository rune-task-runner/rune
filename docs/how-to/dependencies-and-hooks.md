# Dependencies & post-hooks

> How to make tasks run in the right order. Part of the [guides](README.md); full syntax in the
> [language guide](../runefile.md#dependencies-and-post-hooks).

## Concept

A task can declare **dependencies** (tasks that must run *before* it), **post-hooks**
(tasks that run *after* it, only if it succeeds), and **failure hooks** (tasks that run
only if it fails — post-mortem diagnostics). Within one invocation, each task runs **at
most once**, even if several tasks depend on it — so a shared `build` step isn't repeated.
(Failure hooks are the deliberate exception: they run once per failing task.)

## Syntax

Dependencies go after the colon; post-hooks after `&&`; failure hooks after `||` —
in that fixed order:

```rune
deploy: build test && notify || diagnose
    @echo "deploying"

diagnose:
    @echo "post-mortem for $RUNE_FAILED_TASK (exit $RUNE_FAILED_EXIT_CODE)"
```

A failure hook fires only when the task's **own body** fails; it never changes the
exit code, and its output is printed under a `diagnosing:` header (and delivered to
MCP agents as a *fix suggestion* in the same tool response).

Pass arguments to a dependency with the parenthesized form:

```rune
release: (build "release")
    @echo "releasing"

build target="debug":
    @echo "building {{target}}"
```

## Runnable example

See **[examples/dependencies](../examples/dependencies/README.md)** — `rune deploy` runs
`build` and `test` first, then `notify` after.

## Pitfalls

- **Cycles are rejected up front.** `a: b` and `b: a` fail static analysis with the cycle path
  (`a → b → a`) and exit code `3` — nothing runs. See [Troubleshooting](../troubleshooting.md).
- **Each task runs once per invocation.** If you actually need a step to run twice, model it as
  two tasks; dependency memoization is by design. The one exception: a `||` failure hook runs
  once **per failing task**, so a shared diagnostic never skips a failure (its own
  dependencies still memoize normally).
- **Post-hooks run only on success.** If the task fails, its `&&` hooks do not run — declare a
  `||` failure hook when you want something to run on failure instead.
- **Failure hooks fire for the body only.** A failing dependency fires *its own* `||` hooks,
  not the dependent's; hooks never chain (a hook's own `||` hooks are ignored); Ctrl-C runs
  nothing.

## Next steps

- [Parameters](parameters.md) — pass values into tasks and dependencies.
- [Parallelism](parallelism.md) — run independent dependencies concurrently.
