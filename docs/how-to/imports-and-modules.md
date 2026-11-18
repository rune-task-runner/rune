# Imports & modules

> How to split tasks across files and compose them. Part of the [guides](README.md); full
> syntax in the [language guide](../runefile.md#imports-and-modules).

## Concept

Two ways to compose Runefiles:

- **`import`** splices another file's tasks and variables **into the current namespace** —
  good for shared helpers.
- **`mod`** loads another file as a **namespace**, invoked with a prefix (`rune api::build`) —
  good for isolating per-service or per-package tasks in a monorepo.

The `import` statement is not the `rune import` command, which writes a Runefile from the
task file of another runner: see [Import from another runner](import-from-other-runners.md).

## Syntax

```rune
import "common.rune"          # splice tasks/vars into this file
import? "optional.rune"       # ignore if the file is missing

mod api "services/api.rune"   # namespace: rune api::build
mod web "services/web.rune"
```

## Runnable example

See **[examples/monorepo](../examples/monorepo/README.md)** — a shared `import` plus two `mod`
namespaces, with the helper files shipped alongside so it runs as-is.

## Pitfalls

- **Name collisions are reported, not silently merged.** Importing two tasks with the same
  name is a conflict — use `mod` namespaces to keep them distinct.
- **Paths are relative to the importing file.** `mod api "services/api.rune"` resolves from the
  Runefile's directory.
- **`import?` is for optional files only.** A plain `import` of a missing file is an error;
  use `import?` when absence is acceptable.

## Next steps

- [Dependencies & post-hooks](dependencies-and-hooks.md) — depend on tasks across files.
- [Settings & dotenv](settings-and-dotenv.md) — what imported files can configure.
