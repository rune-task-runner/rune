# Releasing Rune

Rune releases are automated end-to-end by the **Release** GitHub Actions workflow
(`.github/workflows/release.yml`) driving [GoReleaser](https://goreleaser.com). A maintainer
picks a version bump; the workflow computes the tag, updates the changelog, and publishes
binaries, multi-arch images, signatures, SBOMs, provenance, and the Homebrew/Scoop packages.

## Cutting a release

1. Ensure `main` is green (CI passing) and contains everything you want to ship.
2. **Actions → Release → Run workflow**, on branch `main`:
   - **bump**: `patch`, `minor`, or `major`.
   - **prerelease**: check to cut a `-rc.N` (release candidate).
3. Approve the `release` environment prompt (only authorized maintainers can).

The workflow then:

- computes the next version from the latest **stable** tag and your bump (see below), refusing
  if that tag already exists;
- prepends a dated section to `CHANGELOG.md` (from Conventional-Commit PR titles) and commits
  it to `main`;
- creates and pushes the annotated tag;
- runs GoReleaser: 6 binary archives + `checksums.txt`, a multi-arch GHCR image
  (`linux/amd64` + `linux/arm64`), cosign signatures, SPDX SBOMs, and — for stable releases —
  the `latest` image tag plus updated Homebrew cask and Scoop manifest;
- attaches GitHub build-provenance attestations to the binaries and the image;
- **for stable releases only**: runs the `publish-extension` job, which packages the VS Code
  extension from `editors/vscode/` at the released tag, attaches the `.vsix` to the GitHub
  release, and publishes it to the VS Code Marketplace and Open VSX (see below).

## VS Code extension publishing

Every **stable** release (prerelease unchecked) also publishes the VS Code extension
(`rune-task-runner.runefile`). Prereleases (`-rc.N`) never publish an extension.

- The extension version is the release tag without the leading `v` (tag `v0.4.0` → extension
  `0.4.0`). The repo's `editors/vscode/package.json` keeps a `0.0.0` placeholder; the workflow
  stamps the real version at publish time and commits nothing.
- The job reuses the protected `release` environment, so required reviewers get a **second
  approval prompt** after the core release finishes. Approving it is expected — it is the
  gate for the publishing tokens, not a manual publish step.
- Order inside the job: package → attach `runefile-<version>.vsix` to the GitHub release →
  publish to the Marketplace → publish the same file to Open VSX. Attaching first means even
  a registry outage leaves the exact artifact recoverable.

### Recovering a failed extension publish

The core release (binaries, images, tag) is never affected by an extension-publish failure —
**do not cut a new release**:

1. **Re-run failed jobs** on the same workflow run. Re-runs are idempotent — and end green:
   both publish steps pass `--skip-duplicate`, so an already-published side logs a skip and
   exits 0 while the other side publishes.
2. Manual fallback (registry outage, expired token): download `runefile-<version>.vsix` from the
   GitHub release assets and publish it locally:

   ```sh
   cd editors/vscode && npm ci
   npx vsce publish --packagePath runefile-<version>.vsix   # VSCE_PAT in the environment
   npx ovsx publish runefile-<version>.vsix                 # OVSX_PAT in the environment
   ```

3. **Token rotation**: Azure DevOps PATs expire (≤ 1 year) — when the Marketplace publish
   fails with 401/403, mint a fresh PAT (same scope as in one-time setup below) and update
   the `VSCE_PAT` secret on the `release` environment. Same procedure for `OVSX_PAT`
   (Open VSX tokens don't expire but can be revoked).

### Versioning: rc iteration and promotion

The target version is computed from the latest **stable** tag + your `bump`, so the same two
inputs cover the whole lifecycle — **keep the bump the same** while working toward a release:

| You want | bump | prerelease | Result (latest stable `v0.4.x`) |
|----------|------|------------|----------------------------------|
| First release candidate | `minor` | ✓ | `v0.5.0-rc.1` |
| Next release candidate | `minor` | ✓ | `v0.5.0-rc.2` (iterates — target doesn't move) |
| Promote rc → stable | `minor` | ✗ | `v0.5.0` |
| Start the next line | `minor`/`patch`/`major` | either | bumps from the new stable |

> **Pre-1.0 (`0.y.z`):** by SemVer convention a breaking change bumps **minor**, not major.
> You choose the bump; the tooling never auto-infers it.

## Verifying a release

Anyone can verify artifacts with public material only — no pre-shared secret.

```sh
# 1. Checksum (the install script does this automatically)
sha256sum --check checksums.txt --ignore-missing

# 2. Signature of the checksums file (covers every archive)
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/rune-task-runner/rune/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt

# 3. Build provenance
gh attestation verify checksums.txt --repo rune-task-runner/rune

# 4. Image signature + provenance
cosign verify ghcr.io/rune-task-runner/rune:<version> \
  --certificate-identity-regexp 'https://github.com/rune-task-runner/rune/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
gh attestation verify oci://ghcr.io/rune-task-runner/rune:<version> --repo rune-task-runner/rune
```

Modifying any byte of an artifact makes steps 1–2 fail — that is the point.

## Local dry-run (no publish)

```sh
rune release-dryrun            # goreleaser release --snapshot --clean
goreleaser check               # validate .goreleaser.yaml only
```

A full local dry-run needs `goreleaser`, `cosign`, `syft`, and `docker` (buildx) installed.
CI runs a lean dry-run (`check` + a snapshot skipping sign/sbom/docker) on every push/PR.

## Recovering from a failed release

The workflow is safe to re-run:

- The tag-exists check makes version computation idempotent; `--clean` wipes `dist/` first.
- GitHub Releases are upserted and GHCR pushes are content-addressed, so re-pushing is safe.
- If the tag was already created but publishing failed **after** that, delete the tag and the
  (draft) release, then re-run — or re-run GoReleaser locally against the existing tag.
- **Homebrew tap / Scoop bucket** commits are the one spot that may need manual cleanup if a
  run failed midway through updating them; check those repos before re-running.

## One-time setup (already provisioned? skip)

1. Create public repos `rune-task-runner/homebrew-tap` and `rune-task-runner/scoop-bucket`.
2. Mint a token that can push to those two repos (GitHub App install token preferred, or a
   fine-grained PAT with `contents: write`); store it as the secret `TAP_GITHUB_TOKEN`.
3. If `main` is protected, allow the release identity to bypass the push restriction (the
   workflow commits `CHANGELOG.md` to `main`); store that identity's token as `RELEASE_TOKEN`,
   or rely on the default token if branch protection permits.
4. Create a protected `release` Environment with required reviewers (authorized maintainers).
5. Enable **Settings → General → "Default to PR title for squash merge commits"** so the
   Conventional-Commit PR title becomes the squash commit subject the changelog reads.
6. Mark the **Validate PR title** and **release-dryrun** checks as required in branch protection.
7. **VS Code Marketplace publisher**: create the `rune-task-runner` publisher at
   [marketplace.visualstudio.com/manage](https://marketplace.visualstudio.com/manage) (needs a
   Microsoft/Azure DevOps account owned by the project, not a personal one). Mint an Azure
   DevOps PAT — organization **"All accessible organizations"**, scope **Marketplace →
   Manage** — and store it as the `VSCE_PAT` secret on the `release` environment.
8. **Open VSX namespace**: create an [Eclipse Foundation account](https://accounts.eclipse.org),
   sign the Open VSX publisher agreement, then claim the namespace and mint a token:

   ```sh
   npx ovsx create-namespace rune-task-runner -p <token>
   ```

   (Token from [open-vsx.org/user-settings/tokens](https://open-vsx.org/user-settings/tokens).)
   Store it as the `OVSX_PAT` secret on the `release` environment. Optionally file a
   [namespace ownership claim](https://github.com/EclipseFdn/open-vsx.org/issues) so the
   listing shows a verified publisher.

## Deferred follow-ups

- **SHA-pin** all third-party actions in the workflows (currently floating major tags); let
  Dependabot keep them current.
- Migrate `dockers:` + `docker_manifests:` to **`dockers_v2:`** before GoReleaser v3 removes
  the classic blocks.
- Optionally publish moving **`v0` / `v0.4`** image tags in addition to `:<version>`/`:latest`.
- **Linux packages** (`.deb` / `.rpm`) and additional package managers (apt, winget, nix).
- **Apple notarization** / **Windows Authenticode** code-signing to remove OS "unidentified
  developer" prompts (requires paid certificates). The Homebrew cask strips the macOS
  quarantine attribute as an interim measure.

## Documentation site

The site at <https://rune-task-runner.github.io/rune/> is generated from `docs/` and
deployed by `.github/workflows/pages.yml` on every push to `main` that touches `docs/`,
`website/`, `internal/docsite/`, `cmd/docsite/` or the Runefile TextMate grammar. It is not
tied to a release: documentation ships continuously.

- **Source of truth** is `docs/**.md`. `internal/docsite` derives each page's frontmatter
  from its H1 and first paragraph and rewrites relative links; the generated tree under
  `website/` is gitignored. Run `rune docs-site-gen` after editing.
- **Navigation** lives in `docs/nav.yaml`. A page under `docs/` that is neither listed nor
  excluded fails `rune docs-check`.
- **One-time setup:** the repository's Settings → Pages must have **Source: GitHub Actions**.
- **Moving to a custom domain** later: add a `CNAME` file to `website/public/`, set
  `base: '/'` and `site: 'https://<domain>'` in `website/astro.config.mjs`, and change
  `BasePath` in `docsite.DefaultOptions` to `/`. Point the DNS records at GitHub Pages, then
  regenerate — every link flows from those two values.
