# Spec 024 — Documentation site foundation

**Status:** draft
**Date:** 2026-09-09
**Depends on:** 004-rich-documentation, 009-docs-and-badges, 015-vscode-lsp-extension (grammar)
**Followed by:** Spec 025 — documentation content depth (recipes cookbook, examples gallery,
how-to pages for `[context]` / post-mortem hooks / typed parameter schemas)

## 1. Problem

Rune's documentation is complete and well organised as Markdown — a goal-oriented index,
a guided user guide, nine how-to guides, three use-case walkthroughs, five reference pages,
and fourteen runnable examples — but it is only readable as files on GitHub. There is no
search, no navigation chrome, no syntax highlighting for the `rune` code blocks that carry
most of the teaching, and no single URL to point a new user at.

Meanwhile the same grammar that would make those code blocks legible already ships in the
VS Code extension (`editors/vscode/syntaxes/runefile.tmLanguage.json`, scope `source.rune`)
and goes unused by the docs.

## 2. Goal

Publish the existing documentation as a modern static site at
`https://rune-task-runner.github.io/rune/`, built from `docs/` as the single source of
truth, with real Runefile syntax highlighting, full-text search, and a navigation structure
defined in one reviewable file.

### Non-goals (deferred to Spec 025)

- The recipes cookbook and the promoted examples gallery.
- How-to pages for `[context]` (spec 021), `||` post-mortem hooks (spec 022), and typed
  parameter schemas (spec 023).
- Rewriting `docs/README.md`'s "I want to…" map.
- Versioned documentation, a custom domain, i18n, a blog.

## 3. Constraints

- **C1 — `docs/**.md` stay canonical and unmodified.** They must keep rendering correctly on
  GitHub, with working relative links. The site is a consumer, never the owner.
- **C2 — the existing docs harness keeps passing untouched.** `test/docs/` (links, code
  blocks, CLI-reference sync, example contract, badges) is the contract for docs quality;
  this spec adds to it and changes none of its existing assertions except the badge table
  (§8.4).
- **C3 — Go tests run inside Docker** (`docker-compose run --rm test go test ./...`), per
  project policy. Node likewise runs in a container, so no host Node install is required.
- **C4 — the shipped binary is unaffected.** GoReleaser pins `main: ./cmd/rune`; the
  generator is a development tool and must not enter a release artifact.
- **C5 — no content drift.** It must be impossible to add a page under `docs/` and have it
  silently missing from the site, or to list a page in the navigation that does not exist.

## 4. Architecture

Four components, each with one responsibility.

```
docs/**.md  ──────────────▶ cmd/docsite ──▶ website/src/content/docs/**.md  (gitignored)
docs/nav.yaml ────────────▶            ──▶ website/src/generated/sidebar.json
docs/img/** ──────────────▶ (copy)     ──▶ website/public/img/**
website/src/landing/index.mdx ─────────▶ website/src/content/docs/index.mdx
                                                     │
editors/vscode/syntaxes/runefile.tmLanguage.json ──▶ Shiki (lang "rune")
                                                     ▼
                                           astro build ──▶ website/dist ──▶ GitHub Pages
```

### 4.1 `internal/docsite` — the transform library

Pure functions over Markdown text; all filesystem access lives in `cmd/docsite`.

| Function | Behaviour |
|----------|-----------|
| `Title(src) (string, string)` | Returns the first `# ` heading's text and `src` with that heading removed. Starlight renders its own `<h1>` from frontmatter, so leaving the H1 in place would double it. |
| `Description(src) string` | First prose paragraph, with inline links and emphasis flattened to their text, truncated at 160 characters on a word boundary. Blockquotes, callouts (`> [!NOTE]`), badge rows, HTML blocks and code fences are skipped when locating that paragraph. Empty string if none is found. |
| `RewriteLinks(src, fromPath, cfg) string` | Rewrites Markdown inline-link targets per §4.2. |
| `Frontmatter(f Front) string` | Emits the Starlight YAML header. |
| `Slug(relPath) string` | Maps a `docs/`-relative path to a site path segment: lowercased, `.md` stripped, `README.md` → the directory itself. |

`Front` carries `Title`, `Description`, and an optional `SidebarOrder`.

### 4.2 Link-rewriting contract

Applied to every Markdown inline link `[text](target)` in a page being generated, where
`fromPath` is the page's `docs/`-relative path. `cfg` supplies `BasePath` (`/rune`) and
`BlobBase` (`https://github.com/rune-task-runner/rune/blob/main`).

| Target in `docs/…` | Result | Rationale |
|---|---|---|
| `caching.md`, `../overview.md` | Site path: `/rune/how-to/caching/`, `/rune/overview/` | Resolve against `dir(fromPath)`, then `Slug`. |
| `README.md` in a subdirectory | The directory index: `/rune/how-to/` | Starlight collapses index pages. |
| `GRAMMAR.md` | `/rune/grammar/` | Slugs are lowercase. |
| any of the above + `#anchor` / `?query` | Suffix preserved verbatim | Anchors are the docs' main intra-page navigation. |
| `img/icon.png`, `../img/icon.png` | `/rune/img/icon.png` | Assets are copied to `website/public/`. |
| `examples/caching/Runefile` — non-`.md`, inside `docs/` | `<BlobBase>/docs/examples/caching/Runefile` | Source files are read on GitHub, not hosted. |
| `../editors/README.md`, `../../CONTRIBUTING.md` — escapes `docs/` | `<BlobBase>/editors/README.md`, `<BlobBase>/CONTRIBUTING.md` | Outside the site's content set. |
| A `docs/` page that `nav.yaml` excludes (e.g. `guides/caching.md`) | `<BlobBase>/docs/guides/caching.md` | Excluded pages are not published, so a site path would 404. |
| `http://`, `https://`, `mailto:` | Untouched | — |

Reference-style links (`[text][ref]` plus a `[ref]: target` definition) are rewritten by the
same rules applied to the definition lines. Bare autolinks (`<https://…>`) are untouched.

**Invariant:** no generated file contains a relative link ending in `.md`. This is asserted
in §8.2 and is the single strongest guard against a broken site.

### 4.3 `cmd/docsite` — the generator

A thin `main`. Flags:

- `-out DIR` — output content tree (default `website/src/content/docs`).
- `-nav FILE` — navigation manifest (default `docs/nav.yaml`).
- `-assets DIR` — asset output root (default `website/public`).
- `-check` — generate into a temporary directory and exit non-zero if the result differs
  from `-out`, printing the differing paths. Writes nothing.

Behaviour: read `nav.yaml`; walk `docs/` collecting `**/*.md`; drop anything matched by
`exclude`; for each remaining page emit frontmatter + transformed body to the mirrored path
under `-out`; copy `docs/img/**` to `-assets/img/**`; copy `website/src/landing/index.mdx`
to `-out/index.mdx`; write `website/src/generated/sidebar.json`. The output tree is cleared
before writing so deletions propagate.

The output mirrors `docs/`'s directory shape exactly, which lets Starlight's `editUrl` base
of `https://github.com/rune-task-runner/rune/edit/main/docs/` resolve to the real source
file for every page.

### 4.4 `docs/nav.yaml` — the navigation manifest

The site's logical structure in one committed, reviewable file.

```yaml
base: /rune
exclude:
  - guides/**          # "Moved" redirect stubs; kept for old links, not published
  - release-guru/**    # an agent skill, not user documentation
groups:
  - group: Start here
    pages: [overview.md, installation.md, getting-started.md, user-guide/README.md]
  - group: How-to
    pages:
      - how-to/dependencies-and-hooks.md
      - how-to/parameters.md
      - how-to/caching.md
      - how-to/parallelism.md
      - how-to/executors.md
      - how-to/settings-and-dotenv.md
      - how-to/secret-masking.md
      - how-to/imports-and-modules.md
      - how-to/os-filtering.md
  - group: Use cases
    pages: [use-cases/python-project.md, use-cases/node-project.md, use-cases/mcp-agents.md]
  - group: Examples
    pages: [examples/README.md, "examples/*/README.md"]
  - group: Reference
    pages: [cli.md, runefile.md, GRAMMAR.md, diagnostics.md, mcp.md]
  - group: Operations
    pages: [docker.md, troubleshooting.md, releasing.md]
  - group: Editors
    links:
      - label: Editor setup (LSP)
        url: https://github.com/rune-task-runner/rune/blob/main/editors/README.md
```

Rules:

- A `pages` entry is a `docs/`-relative path or a glob. Order within a group is the listed
  order; glob matches expand alphabetically by generated label.
- `label:` may override a page's derived title in the sidebar only (`how-to/` renders as
  "How-to"; the on-disk directory name does not change in this spec).
- `links` entries are external sidebar links, for material that lives outside `docs/`.
- Two deliberate placements: `user-guide/README.md` sits in **Start here** as the guided
  tour rather than forming its own group, and `mcp.md` sits in **Reference** while the
  MCP walkthrough stays in **Use cases**.
- Spec 025 inserts a **Recipes** group between **How-to** and **Use cases**.

### 4.5 `website/` — the Astro + Starlight application

- npm with a committed `package-lock.json`, matching `editors/vscode`.
- `astro.config.mjs`: `site: 'https://rune-task-runner.github.io'`, `base: '/rune'`,
  `outDir: './dist'`; the Starlight integration with `title: 'Rune'`, the logo from
  `public/img/icon.png`, `social` → the GitHub repo, `editLink.baseUrl` →
  `https://github.com/rune-task-runner/rune/edit/main/docs/`, and
  `sidebar` imported from `./src/generated/sidebar.json`.
- Syntax highlighting: Starlight renders code through Expressive Code, so the grammar is
  registered at `expressiveCode.shiki.langs` as
  `{ name: 'rune', scopeName: 'source.rune', ...grammar }`, where `grammar` is a JSON import
  of `editors/vscode/syntaxes/runefile.tmLanguage.json`. Existing ```rune fences highlight
  with no content change. (`markdown.shikiConfig` is *not* used — Starlight's code blocks do
  not go through it.)
- `src/content.config.ts`: the `docs` collection uses Starlight's stock
  `docsLoader()` with `docsSchema()`. Because the generator writes into Starlight's default
  `src/content/docs/`, no custom loader or `base` override is needed.
- Search (Pagefind), dark mode, mobile navigation and the table of contents come from
  Starlight's defaults.
- Gitignored: `/website/node_modules/`, `/website/dist/`, `/website/.astro/`,
  `/website/src/content/docs/`, `/website/src/generated/`.

### 4.6 Landing page

`website/src/landing/index.mdx` — committed, hand-written, copied into the content root by
the generator (so it is not stranded inside the gitignored tree). Starlight's `splash`
template with:

1. Hero: the `icon.png` logo, the title "Rune", the tagline *A shared task runner for
   humans and AI agents*, and three actions — **Get started** (`/rune/getting-started/`),
   **Examples** (`/rune/examples/`), **GitHub**.
2. The one-line install command.
3. The Runefile sample from `README.md`, in a ```rune fence, rendered with live
   `source.rune` highlighting, beside the four `rune` invocations it supports.
4. A card grid: the six "Why Rune?" points from `README.md`, plus a distinct card for the
   MCP/agent story.

`docs/README.md` is unchanged and remains the map for readers on GitHub. The duplication is
intentional: a hero page and a link table serve different media.

## 5. Build and local workflow

A `website` service is added to `docker-compose.yml` (`node:22`, repo mounted at `/src`,
`working_dir: /src/website`, a named `npm-cache` volume, port `4321` exposed) so the site
builds and serves without a host Node installation, consistent with C3.

New `Runefile` tasks:

```rune
# Generate the documentation site content tree from docs/.
docs-site-gen:
    go run ./cmd/docsite

# Serve the documentation site locally at http://localhost:4321.
docs-site-dev: docs-site-gen
    docker-compose run --rm --service-ports website npm run dev

# Build the static documentation site into website/dist.
docs-site: docs-site-gen
    docker-compose run --rm website npm ci
    docker-compose run --rm website npm run build

# Verify the generated tree is current and the site builds.
docs-site-check:
    go run ./cmd/docsite -check
    docker-compose run --rm website npm run build
```

## 6. Deployment

**`.github/workflows/pages.yml`** — on `push` to `main` filtered to `docs/**`,
`website/**`, `editors/vscode/syntaxes/**`, plus `workflow_dispatch`. Permissions
`contents: read`, `pages: write`, `id-token: write`; concurrency group `pages` with
`cancel-in-progress: false`. Two jobs:

1. **build** — `checkout`, `setup-go@v5` (1.25), `setup-node` (Node 22, npm cache; pin to
   the action's current major at implementation time),
   `go run ./cmd/docsite`, `npm ci --prefix website`, `npm run build --prefix website`,
   `actions/configure-pages@v5`, `actions/upload-pages-artifact@v3` with
   `path: website/dist`.
2. **deploy** — `actions/deploy-pages@v4`, `environment: github-pages`.

**`ci.yml`** gains a **`docs-site`** job running the same generate-and-build on every pull
request without deploying, so a broken rewrite or bad frontmatter fails review rather than
`main`. It runs on `ubuntu-latest` only.

**Manual prerequisite:** in the repository settings, GitHub Pages must be set to
*Source: GitHub Actions* before the first deployment. This is a one-time human step and is
recorded in `docs/releasing.md`.

## 7. Documentation of the site itself

- `docs/releasing.md` gains a short "Documentation site" section: how the site is built,
  the Pages prerequisite, and the future custom-domain switch (a CNAME file plus DNS, with
  `base` reset to `/`).
- `CONTRIBUTING.md` gains `rune docs-site-dev` to the local-workflow task list.
- `README.md` gains a **Docs** badge pointing at the site URL, and the nav line under the
  badge row gains the site link.

## 8. Testing

### 8.1 `internal/docsite` unit tests

Table-driven, in-package, no filesystem:

- `RewriteLinks` — one case per row of the §4.2 table, plus: an anchor-only link (`#usage`),
  a link whose text contains parentheses, a reference-style definition, a bare autolink, an
  already-absolute site path, and a link to an excluded page.
- `Title` — a normal page; a page with a badge row and an HTML block before the H1
  (`docs/README.md`'s shape); assertion that the returned body no longer contains the H1.
- `Description` — a first paragraph containing inline links; a page whose first block is a
  `> [!NOTE]` callout; a page whose first block is a code fence; a paragraph longer than
  160 characters (word-boundary truncation); a page with no prose.
- `Slug` — `GRAMMAR.md` → `grammar`, `how-to/README.md` → `how-to`,
  `use-cases/python-project.md` → `use-cases/python-project`.

### 8.2 `test/docs/site_test.go` — drift guards

Runs with the existing docs suite (`rune docs-check`), in Docker:

- **Coverage:** every `docs/**/*.md` not matched by `exclude` appears in exactly one
  `nav.yaml` group — no orphaned pages, no duplicates. Failure names the offending path.
- **Existence:** every non-glob `pages` entry resolves to a real file; every glob matches at
  least one file.
- **Exclusions:** `guides/**` and `release-guru/**` produce no output files.
- **Frontmatter:** every generated file carries a non-empty `title`.
- **No surviving `.md` links:** no generated file contains a relative link target ending in
  `.md` (the §4.2 invariant).
- **Golden test:** a small fixture tree under `testdata/` generates byte-identical expected
  output, covering frontmatter, H1 removal, and each link class.

### 8.3 CI gates

- `go test ./...` (in Docker locally, direct in CI) covers §8.1 and §8.2.
- `cmd/docsite -check` asserts the committed sidebar and generated tree agree.
- A real `astro build` in the `docs-site` job is the final gate: Starlight schema
  validation, Shiki grammar loading, and Astro's own link resolution all run for real.

### 8.4 Existing suite

`test/docs/`'s link, code-block, CLI-reference and example-contract tests are unchanged and
must stay green — C1 and C2 make that the definition of success. The one edit is a new
entry in `badges_test.go`'s `requiredBadges` table for the Docs badge added in §7.

## 9. Risks

| Risk | Mitigation |
|------|-----------|
| Starlight's `docsSchema()` rejects a generated page | The generator always emits `title`; the golden test pins the frontmatter shape; the real `astro build` in CI catches anything else before merge. |
| Shiki fails to load the tmLanguage grammar | Verified up front: the grammar declares `scopeName: source.rune`. If a pattern in it proves incompatible, the fallback is registering `rune` as an alias of `makefile` and filing a follow-up — legibility degrades, the build does not break. |
| The generated tree drifts from `docs/` | `-check` plus the §8.2 coverage test; the tree is gitignored, so there is no stale committed copy to diverge. |
| Pages not yet enabled — first deploy fails | §6 records it as an explicit manual prerequisite. |
| A `base: '/rune'` prefix bug produces sitewide broken links | Every rewritten path is built through one `cfg.BasePath` join, covered by §8.1; a custom-domain switch changes one config value. |

## 10. Acceptance criteria

1. `https://rune-task-runner.github.io/rune/` serves the landing page, with working search,
   dark mode, and sidebar navigation matching `docs/nav.yaml`.
2. Every page listed in `nav.yaml` renders, and every intra-doc link on the site resolves —
   no `.md` targets survive.
3. ```rune code fences are syntax-highlighted using the VS Code extension's grammar.
4. `docs/**.md` are byte-identical to their pre-spec state, and still render correctly with
   working links on GitHub.
5. Adding a new page under `docs/` without listing it in `nav.yaml` fails `rune docs-check`.
6. `rune test` and `rune docs-check` pass in Docker; `rune docs-site` produces
   `website/dist` with no host Node installed.
7. A pull request touching `docs/` runs the `docs-site` job; a push to `main` deploys.
