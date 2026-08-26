# Contract: Failure-context environment variables

**Feature**: 022-post-mortem-diagnostics · Status: Draft

Environment visible to a failure-hook body (any executor: sh, python, node),
in addition to the standard task environment. Public contract for Runefile
authors (FR-009).

| Variable | Type | Value | Example |
|---|---|---|---|
| `RUNE_FAILED_TASK` | string | User-visible name of the task whose body failed (module-qualified when imported) | `test`, `ci::lint` |
| `RUNE_FAILED_EXIT_CODE` | decimal string | Exit code of the failed body; `1` when no numeric code is available (e.g. non-exec errors) | `2` |

Rules:

1. Set **only** during a failure-hook body run — never for ordinary tasks,
   dependencies, success hooks, or the hook's own dependency subtree.
2. Appended after all other env sources (process env, dotenv, exported
   module variables, `[env(...)]` pairs), so they win over any static
   declaration of the same names.
3. Values are stable across both surfaces (CLI and MCP) and across executors.
4. When one invocation has several failing tasks sharing a hook task, each
   hook run sees its own failure's values (once-per-failure rule).
5. Names are reserved: Runefile authors should not `[env(...)]`-declare them;
   if they do, the failure context silently wins during hook runs.

Example hook using the contract:

```rune
test: build || diagnose
    go test ./...

diagnose:
    @echo "post-mortem for $RUNE_FAILED_TASK (exit $RUNE_FAILED_EXIT_CODE)"
    go vet ./...
```
