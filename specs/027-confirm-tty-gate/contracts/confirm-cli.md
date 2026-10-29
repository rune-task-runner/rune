# Contract: `[confirm]` approval on the CLI

**Feature**: [../spec.md](../spec.md) | **Decision table**: [../data-model.md](../data-model.md)

This is the user-visible contract the tests pin. Byte shapes are given for
`--color=never`; with color on, only the `rune:` / `warning:` prefixes and the
prompt text gain styling, and stripping ANSI yields the same bytes.

## 1. Interactive (stdin is a terminal, no `--yes`)

Unchanged from the previous release.

```text
stderr: <prompt> [y/N] 
```

- `<prompt>` is the `[confirm("…")]` text, or `Run "<task>"?` when none.
- One line is read. `y` or `yes` (case-insensitive, surrounding whitespace
  trimmed) approves; anything else, an empty line, or end of input declines.
- Declined: body does not run, no banner, exit 1.
- Input after the answer's newline is left unread for the next prompt or the
  body.
- Prompts from `[parallel]` dependencies are asked one at a time; the next
  appears only after the previous one is answered.

## 2. Non-interactive (stdin is not a terminal, no `--yes`)

No prompt is printed and stdin is not read. The run ends with exactly one
banner line on stderr:

```text
rune: task "<task>" was not confirmed (<prompt>): stdin is not a terminal; pass --yes to approve
```

Examples:

```text
rune: task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve
rune: task "clean" was not confirmed (Run "clean"?): stdin is not a terminal; pass --yes to approve
```

- Control characters in `<prompt>` are escaped (`\n`, `\t`, `\x1b`), so the
  banner stays one line.
- Exit status: 1.
- Body does not run; the task's `||` failure hooks do not fire; dependents do
  not run. Tasks that ran earlier in the invocation are unaffected.
- Shown even when the task has `[no-exit-message]` (the body never started).
- Secret masking applies to the banner as it does to every banner.

### 2a. As a `||` failure hook

```text
warning: failure hook <task> failed: task "<task>" was not confirmed (<prompt>): stdin is not a terminal; pass --yes to approve
```

The run's exit status is the original failure's.

## 3. `--yes`

Unchanged. No prompt, no notice, stdin is not read, so the full stdin stream
reaches the task body.

## 4. Unchanged surfaces

- `--dry-run`, `--summary`: no prompt, no banner, no stdin read.
- MCP (`rune mcp`, `rune serve`) and the agent executor's callback endpoint:
  byte-identical results. The adapter passes no stdin, which keeps today's
  silent decline.
- `--choose`: the picker requires a terminal, so a chosen `[confirm]` task
  follows section 1.

## 5. Help text

```text
--yes   approve [confirm] tasks without prompting (required when stdin is not a terminal)
```
