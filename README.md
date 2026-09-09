<div align="center">

<p align="center">
  <img src="docs/img/icon.png" alt="Rune logo" width="120">
</p>

# Rune

**A shared task runner for humans and AI agents.**

<p align="center">
  <a href="https://github.com/rune-task-runner/rune/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/rune-task-runner/rune/ci.yml?branch=main"></a>
  <a href="https://github.com/rune-task-runner/rune/tags"><img alt="Release" src="https://img.shields.io/github/v/tag/rune-task-runner/rune?sort=semver"></a>
  <a href="https://github.com/rune-task-runner/rune/blob/main/LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-yellow.svg"></a>
  <img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/rune-task-runner/rune">
  <a href="https://goreportcard.com/report/github.com/rune-task-runner/rune"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/rune-task-runner/rune"></a>
  <a href="https://pkg.go.dev/github.com/rune-task-runner/rune"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/rune-task-runner/rune.svg"></a>
  <a href="https://rune-task-runner.github.io/rune/"><img alt="Docs" src="https://img.shields.io/badge/docs-site-blue"></a>
</p>

**[Docs](docs/README.md)** · **[Getting started](docs/getting-started.md)** · **[Examples](docs/examples/README.md)** · **[CLI reference](docs/cli.md)**

</div>

Rune runs your project's commands — `lint`, `test`, `build`, `deploy` — from one readable
file, the `Runefile`. Humans run tasks from the CLI; AI agents and IDEs run the *same*
tasks through the Model Context Protocol (MCP). It ships as a single static binary with no
runtime dependencies.

```rune
# Build the project.
build target="release": fetch
    go build -tags {{target}} ./...

# Fetch dependencies.
fetch:
    @go mod download

# Run tests.
test: build
    go test ./...
```

```sh
rune              # show the version + available tasks (runs nothing)
rune build        # run the `build` task
rune test         # runs `build` (a dependency), then `test`
rune --list       # shows documented tasks
```

## Why Rune?

If you've used `make` or `just`, Rune will feel familiar — with a few deliberate choices:

- **Command runner, not a build system.** Tasks always run when asked. No `.PHONY` traps,
  no timestamp-based "up-to-date" skipping. Content-hash caching exists, but only as an
  explicit per-task `[cache(...)]` opt-in.
- **Errors are a feature.** Undefined variables, unknown dependencies, dependency cycles,
  and arity mismatches are caught *before anything runs*, with precise `file:line:col` +
  caret-underlined diagnostics.
- **Boringly portable.** One static binary. The default shell executor is a pure-Go,
  cross-platform shell (`mvdan.cc/sh`) — your `(sh)` tasks behave the same on Linux, macOS,
  and Windows, with no WSL or Git-Bash required.
- **AI-native and secure by default.** Tasks are first-class MCP tools. Agent access is
  read-only by default, destructive tasks are gated, and secrets come from the environment
  only — never the Runefile.
- **Multi-language bodies.** Write task bodies in shell, Python, Node, or drive an AI agent.
- **First-class editor support.** `rune lsp` is a built-in language server (LSP 3.17): live
  diagnostics, completion, go-to-definition, hover, outline, and formatting in VS Code,
  Neovim, Helix, and Zed — reusing the same parser and analyzer, running nothing. For VS Code,
  install [the Rune extension](https://marketplace.visualstudio.com/items?itemName=rune-task-runner.runefile)
  from the Marketplace. `rune analyze` reports the same diagnostics for CI. See
  [editor setup](editors/README.md).

## Install

```sh
# Install script (Linux/macOS) — verifies the checksum:
curl -sSfL https://raw.githubusercontent.com/rune-task-runner/rune/main/scripts/install.sh | sh

# Homebrew (macOS/Linux):
brew install rune-task-runner/tap/rune

# Scoop (Windows):
scoop bucket add rune https://github.com/rune-task-runner/scoop-bucket && scoop install rune

# From source:
go install github.com/rune-task-runner/rune/cmd/rune@latest
```

Or grab a prebuilt binary from [Releases](https://github.com/rune-task-runner/rune/releases),
or run it in a container:

```sh
docker run --rm -v "$PWD":/work ghcr.io/rune-task-runner/rune --list
```

Full instructions: **[Installation guide](docs/installation.md)**.

## Documentation

**Start at the [documentation index](docs/README.md)** — it routes you to the right page by
goal ("I want to…"). Highlights:

| Guide | What's inside |
|-------|---------------|
| [Docs index](docs/README.md) | Find any page by what you want to do |
| [What is Rune?](docs/overview.md) | The idea, the mental model, and when to use it |
| [Getting started](docs/getting-started.md) | Zero → your first task in minutes |
| [User guide](docs/user-guide/README.md) | A guided tour of every capability, in order |
| [How-to guides](docs/how-to/README.md) | Task-oriented guides, one per capability |
| [Use cases](docs/use-cases/README.md) | Full walkthroughs: Python, Node, AI agents (MCP) |
| [Examples](docs/examples/README.md) | Runnable starting points for real project shapes |
| [Installation](docs/installation.md) | Binary, source, and container install per OS |
| [CLI reference](docs/cli.md) | Every command, flag, and exit code |
| [Runefile language](docs/runefile.md) | Tasks, deps, params, caching, executors, dotenv |
| [AI agents (MCP)](docs/mcp.md) | Expose tasks to agents; the security model |
| [Docker](docs/docker.md) | Run Rune install-free in a container |
| [Troubleshooting](docs/troubleshooting.md) | Errors, diagnostics, and exit codes |
| [Grammar](docs/GRAMMAR.md) | The formal Runefile grammar |

## Contributing

See **[CONTRIBUTING.md](CONTRIBUTING.md)**. In short: tests run inside Docker
(`docker-compose run --rm test go test ./...`), and every change must pass the CI gates
(format, lint, cross-platform race tests, fuzz smoke, golden consistency, build). Rune
dogfoods itself — run `rune --list` in the repo to see the development tasks.

## Security

Found a vulnerability? Please follow the process in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © The Rune Authors.
