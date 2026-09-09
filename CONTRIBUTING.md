# Contributing to Rune

Thanks for helping! This guide takes you from a fresh clone to a verified change with as
little friction as possible. You don't need to know the codebase to make your first useful
contribution.

## What to contribute

Anything that makes Rune better is welcome. Good places to start, easiest first:

- **Docs fixes** — a confusing sentence, a typo, a missing step. Edit the Markdown under
  [`docs/`](docs/) and open a PR.
- **A new example** — add a runnable example to the [example library](docs/examples/README.md).
  This is the **best first contribution**: self-contained, high-value, and walked through
  [below](#add-a-new-example).
- **Bug reports & fixes** — a task that misbehaves, a confusing diagnostic. Include a minimal
  `Runefile` that reproduces it.
- **Features** — for anything non-trivial, open an issue first so we can agree on the approach
  (Rune's scope and DSL are intentionally small — see the
  [constitution](.specify/memory/constitution.md)).

## Set up

You need:

- **Go 1.24+** (the project builds on Go 1.25; CI derives the version from `go.mod`).
- **Docker** + **docker-compose** — the test suite runs inside a container (see
  [Verify](#verify-your-change)).
- Optional: `golangci-lint` v2 and `goreleaser` for local linting / release dry-runs.

```sh
git clone https://github.com/rune-task-runner/rune
cd rune
go build -o rune ./cmd/rune    # build the CLI
./rune --version
```

Rune dogfoods itself: its own dev tasks live in the repo-root [`Runefile`](Runefile).

```sh
rune --list        # see all dev tasks (or: go run ./cmd/rune --list)
```

| Task | What it does |
|------|--------------|
| `rune fmt` | Format Go code (gofmt + gofumpt + goimports via golangci-lint). |
| `rune lint` | `gofmt` check + `go vet` + `golangci-lint run`. |
| `rune test` | Full test suite **inside Docker**. |
| `rune test-race` | Test suite with the race detector, inside Docker. |
| `rune build` | Build the static host binary into `dist/`. |
| `rune docker` | Build the production container image. |
| `rune docs-check` | Verify the documentation (examples, code blocks, links, site nav) inside Docker. |
| `rune docs-site-gen` | Generate the documentation site's content tree from `docs/`. |
| `rune docs-site-dev` | Serve the documentation site at <http://localhost:4321> with hot reload. |
| `rune docs-site` | Build the static documentation site into `website/dist`. |
| `rune docs-site-check` | Verify the generated site tree is current and the site builds. |
| `rune release-dryrun` | Local GoReleaser snapshot (no publish). |

> Don't have `rune` installed yet? Prefix any task with `go run ./cmd/rune` — e.g.
> `go run ./cmd/rune lint`.

### Working on the documentation site

The site at <https://rune-task-runner.github.io/rune/> is **generated** from `docs/` — edit
the Markdown there, never the generated tree under `website/src/content/docs/` (it is
gitignored and rewritten on every run). Node runs in the `website` container, so no host
Node install is needed.

A new page under `docs/` must be added to `docs/nav.yaml`, which declares the site's
navigation; `rune docs-check` fails if a page is in neither the manifest nor its `exclude`
list, so a page cannot silently go missing from the site.

## Add a new example

The example library lives in [`docs/examples/`](docs/examples/README.md). Each example is a
self-contained directory the docs harness runs and verifies, so it can never drift.

1. **Copy the simplest example as a template:**

   ```sh
   cp -r docs/examples/getting-started docs/examples/my-example
   ```

2. **Edit the `Runefile`** to show what you want. Use only the shipped DSL (see the
   [language guide](docs/runefile.md)); keep bodies portable (the default shell is
   cross-platform).

3. **Edit `README.md`** so it has these sections (the harness checks for them):
   a one-line **Use case**, a **Demonstrates** line, a **Prerequisites** line (`none` if it
   needs nothing beyond Rune), a **Run it** command, and the **Expected output**.

4. **List it** in [`docs/examples/README.md`](docs/examples/README.md) under the right group.

5. **Verify** (next section). The harness will pick up your example automatically.

That's a complete, mergeable contribution.

## Verify your change

**The Go test suite runs inside Docker, never directly on the host.** The harness is
`Dockerfile.test` + [`docker-compose.yml`](docker-compose.yml):

```sh
docker-compose run --rm test go test ./...                 # everything
docker-compose run --rm test go test ./test/docs/...       # just the docs verification
docker-compose run --rm -e CGO_ENABLED=1 test go test -race ./...
```

`rune test` / `rune test-race` / `rune docs-check` wrap these. If you changed docs or added an
example, `rune docs-check` is the fast check; if you don't have `rune`, run the
`docker-compose` line directly.

## Where things live

| Path | What's there |
|------|--------------|
| [`cmd/rune/`](cmd/rune/) | The CLI entry point (flags, subcommands). |
| `internal/` | The engine: `lexer`, `parser`, `analyzer`, `eval`, `runtime/…`, `cli`, plus the MCP server. |
| `internal/analysis`, `internal/language`, `internal/lsp`, `internal/formatter` | The read-only analysis layer behind `rune analyze` and `rune lsp` (the language server) — a shared `analysis.Service`, the symbol index and language registry, the LSP protocol glue, and the canonical formatter. Runs nothing. |
| [`editors/`](editors/) | Editor integration: the VS Code extension and Neovim/Helix/Zed setup. |
| [`docs/`](docs/) | User documentation — guides, examples, reference. |
| `test/integration`, `test/corpus`, `test/docs` | End-to-end, grammar-corpus, and documentation harnesses. |
| [`Runefile`](Runefile) | Rune's own dev tasks. |
| `Dockerfile`, `Dockerfile.test`, `docker-compose.yml` | Production image and the test harness. |

## Propose it

- Branch from `main`; keep changes focused.
- Make sure `rune lint` and `rune test` (or `rune test-race`) pass locally before pushing; for
  docs/example changes, `rune docs-check` too.
- Any DSL change ships with updated [`docs/GRAMMAR.md`](docs/GRAMMAR.md) and golden/integration
  fixtures in the same PR.
- Describe what changed and why; reference any related spec under `specs/`.

### Pull request titles (Conventional Commits)

PRs are **squash-merged**, and the PR **title** becomes the commit subject on `main`. Titles
**must** follow [Conventional Commits](https://www.conventionalcommits.org/) — a CI check
(`Validate PR title`) enforces this. The type drives the release changelog grouping:

| Title prefix | Changelog group | Example |
|--------------|-----------------|---------|
| `feat:` | Added | `feat: add --watch flag` |
| `fix:` | Fixed | `fix: handle CRLF in lexer` |
| `perf:` / `refactor:` | Changed | `refactor: split the eval package` |
| `feat!:` / `BREAKING CHANGE:` | (breaking — minor bump pre-1.0) | `feat!: drop legacy syntax` |
| `docs:`, `chore:`, `ci:`, `test:`, `build:` | omitted from the changelog | `docs: clarify install steps` |

Releases are automated from these titles — see [docs/releasing.md](docs/releasing.md).

### What CI will check

Every push and PR runs the gate set in [`.github/workflows/ci.yml`](.github/workflows/ci.yml);
all must pass to merge:

1. **lint** — `gofmt`, `go vet`, and `golangci-lint run` are clean.
2. **test** — the full suite with the race detector (`-race`) on Linux, macOS, and Windows.
3. **build** — the static, CGO-free binary compiles on all three OSes.
4. **golden** — committed golden files match a fresh regeneration.
5. **fuzz-smoke** — the lexer and parser fuzz targets build and run briefly.
6. **docs-verify** — the documentation harness (`test/docs`) passes: examples validate and run,
   code blocks validate, and internal links resolve.
7. **release-dryrun** — `goreleaser release --snapshot` succeeds.

### Golden files

Golden fixtures (`testdata/…`) are regenerated **deliberately** — never hand-edited to make a
test pass:

```sh
docker-compose run --rm test go test ./internal/diag ./internal/lexer ./internal/parser ./internal/cli ./test/corpus -update
```

If a change alters DSL surface, update `docs/GRAMMAR.md` **and** the fixtures in the same PR.

## Coding standards

Go code must be idiomatic and conform to the project's bundled Go skills and the
[constitution](.specify/memory/constitution.md) (Principle VIII): handle errors explicitly and
wrap with `%w`; every goroutine has a clear owner and exit; constructors over `init()` and
package globals; every external call carries a timeout; no unprofiled "optimizations".
`golangci-lint run` must report zero issues.

## Security

Found a vulnerability? Please follow the process in [SECURITY.md](SECURITY.md) rather than
opening a public issue.
