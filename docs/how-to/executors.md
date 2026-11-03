# Executors

> How to write task bodies in shell, Python, Node, or an AI agent. Part of the
> [guides](README.md); full syntax in the [language guide](../runefile.md#executors).

## Concept

A task body runs under an **executor** named in parentheses after the signature. The default
is `sh` — a **pure-Go, cross-platform shell** (`mvdan.cc/sh`) that behaves the same on Linux,
macOS, and Windows without invoking the system shell. Other executors (`python`, `node`,
custom) shell out to the real interpreter via a temp file; `agent` drives an installed
AI-agent CLI.

## Syntax

```rune
# default (sh) — no parentheses needed
hello:
    echo "hi"

# Python (needs python3 installed)
analyze (python):
    print("analyzing")

# Node (needs node installed)
bundle (node):
    console.log("bundling")

# Agent — a natural-language instruction (see the MCP guide)
summarize (agent):
    Summarize the latest git changes.
```

## Runnable example

See **[examples/polyglot](../examples/polyglot/README.md)** (shell + Python + Node in one
pipeline), plus [node-project](../examples/node-project/README.md) and
[python-project](../examples/python-project/README.md).

## Pitfalls

- **Interpreters must be installed.** A `(python)`/`(node)` task fails with an actionable error
  if the runtime isn't on `PATH`. The Rune container image ships only the static binary — see
  the [Docker guide](../docker.md) for that caveat.
- **Stick to the default `sh` for portability.** Only reach for `set shell := [...]` when you
  truly need a system shell feature; it trades away cross-platform parity. See
  [shell override](../runefile.md#shell-override) for how an override runs the body.
- **Agent bodies need a configured agent CLI** — see [AI agents (MCP)](../mcp.md).

## Next steps

- [AI agents (MCP)](../mcp.md) — the `agent` executor and the security model.
- [Settings & dotenv](settings-and-dotenv.md) — `set shell` and environment for bodies.
