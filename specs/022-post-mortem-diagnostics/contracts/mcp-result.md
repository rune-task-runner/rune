# Contract: MCP tool result with fix suggestion

**Feature**: 022-post-mortem-diagnostics · Status: Draft

What an MCP client (agent) receives when it calls a task tool whose task
fails and has `||` failure hooks.

## Result shape

Unchanged envelope: one `TextContent` block, `IsError: true` when
`ExitCode != 0`. The text layout gains one optional trailing section:

```text
<masked stdout of the task body>
<masked stderr of the task body, incl. warning lines>
[exit <N>]
[fix suggestion - from Runefile || failure hooks]
<masked, capped stdout of the failure hooks>
```

Rules:

1. The `[fix suggestion - from Runefile || failure hooks]` line (ASCII only,
   matching the `[exit N]` marker register) and its body
   appear **only** when at least one hook run produced non-empty stdout
   (after trimming). No hooks / empty output / task succeeded → the section
   is absent and the result is byte-identical to today's format.
2. `[exit N]` always reflects the **original task's** exit code; hook
   outcomes never change it (FR-004).
3. The suggestion text is masked before capping: 8 KiB cap, cut backed up to
   a UTF-8 rune boundary, `\n[truncated]` appended after the cut and excluded
   from the cap (identical to the `[context]` instructions discipline).
4. Hook stderr and the `warning: failure hook …` lines appear in the normal
   stderr portion, not in the suggestion section.
5. Content contains no ANSI escapes (existing `Quiet`/no-theme invariant).

## Go API delta

```go
// mcpserver package
type Result struct {
    Stdout   string
    Stderr   string
    ExitCode int
    FixSuggestion string // NEW: pre-masked; "" = omit section
}
```

`formatResult` remains the single formatting choke point; existing tests that
assert the three-part layout stay valid because the section is additive and
conditional.

## Non-goals

- No structured/second content block in v1.
- No per-call opt-out; authors control the feature by (not) declaring `||`.
- The `[context]` instructions surface is unrelated and unchanged.
