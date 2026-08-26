# OS filtering

> How to scope tasks to an operating system. Part of the [guides](README.md); full syntax in
> the [language guide](../runefile.md#attributes).

## Concept

An OS attribute makes a task exist only on the platforms it names. On a non-matching host
the task is **hidden** from `--list`, the interactive picker, shell completion, and the MCP
tool list; **not runnable** — invoking it by name fails with a diagnostic naming the
required OS (exit 3); and **silently skipped as a dependency or post-hook**, which lets one
cross-platform task depend on per-OS variants and run exactly the matching one (a `||`
failure hook is skipped **with a warning** instead — a silent skip would hide why no
diagnostic appeared). This keeps
platform-specific setup in one Runefile without cluttering every machine's task list. The
`os()` and `arch()` built-ins report the current platform for inline branching, and
`rune --dump --format json` reports the computed verdict per task in an `available` field.

## Syntax

```rune
[linux]
setup-linux:
    apt-get install -y build-essential

[macos]
setup-macos:
    brew install coreutils

[windows]
setup-windows:
    choco install make

# Available everywhere; reports the platform.
info:
    @echo "on {{os()}}/{{arch()}}"
```

`[unix]` matches every platform except Windows (Linux and macOS included). Several OS
attributes combine as OR: `[linux, windows]` means either.

## Runnable example

See **[examples/os-filtering](../examples/os-filtering/README.md)** — a cross-platform
`setup` task dispatches to per-OS variants, and `rune --list` shows only the variant for
your current OS.

## Pitfalls

- **Requesting an off-OS task reports why it's unavailable** (exit 3, e.g.
  `task "setup-win" is not available on macos (requires windows)`) rather than silently
  doing nothing.
- **An off-OS dependency is skipped silently** — the depending task still runs. That is
  what enables the dispatch pattern, but it also means an OS attribute added to a shared
  dependency quietly stops it running on other platforms.
- **Prefer the cross-platform `sh` default** for everything that *can* be portable; reserve OS
  filtering for genuinely platform-specific steps (package managers, paths).
- **Don't hard-code path separators.** Use the `/` path-join operator, which emits forward
  slashes on every OS.

## Next steps

- [Executors](executors.md) — cross-platform shell vs. platform tools.
- [CLI reference](../cli.md) — how filtered tasks appear in `--list`.
