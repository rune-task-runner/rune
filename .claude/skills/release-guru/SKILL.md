---
name: release-guru
description: >-
  Manage the full release lifecycle for the Rune task runner — cut a release, ship a version,
  publish a tag, promote a release candidate, decide the version bump, preview the changelog,
  trigger the GitHub Release workflow, watch it, verify the published artifacts, or recover a
  failed/partial release. Use this whenever the user mentions releasing, shipping, cutting a
  version, tagging, bumping the version, an rc / release candidate, promoting an rc to stable,
  the changelog, GoReleaser, or the Release workflow — even if they don't say "release-guru".
  Rune-specific: knows the stable-tag-derived versioning, the pre-1.0 bump rules, git-cliff
  changelog, and the cosign/attestation verification.
---

# Release Guru

You are the release manager for **Rune**. Releases are automated by the `Release` GitHub
Actions workflow (`.github/workflows/release.yml`) driving GoReleaser — a maintainer picks a
bump, the workflow computes the tag, updates the changelog, tags, and publishes everything.
Your job is to run the human/agent side of that safely: **decide → preview → trigger → watch →
verify**, and recover if something breaks.

The hardest part of a release is getting the version and the go/no-go decision right. Don't do
that math in your head — a bundled script mirrors the workflow's exact logic. Lean on it.

## The one tool you need

`scripts/release-guru.sh` is the deterministic engine. Every command is **read-only except
`trigger`**. Resolve its path relative to this skill and run it from the repo root. It resolves
the repo via `gh` (or `RUNE_REPO=owner/name`); the canonical release repo is `rune-task-runner/rune`.

| Command | What it does |
|---------|--------------|
| `status` | Snapshot: repo, branch, latest stable tag, unreleased commits, candidate versions. |
| `recommend` | Suggest a bump from the unreleased Conventional Commits, with the reason. |
| `next-version <bump> [--pre]` | Print the exact tag that would be cut. |
| `changelog [<bump> [--pre]]` | Preview the unreleased changelog section. |
| `preflight` | Gate checks: clean tree, on `main`, HEAD pushed, **CI green for HEAD**. |
| `plan <bump> [--pre]` | The full pre-cut brief: status + version + changelog + preflight. |
| `trigger <bump> [--pre]` | **Fires** the Release workflow (the only mutating command). |
| `watch` | Tail the most recent Release run to completion. |
| `verify <version>` | Print/run the cosign + attestation checks for a published tag. |

`--pre` (or `--prerelease`) cuts a `-rc.N` instead of a stable tag.

## The mental model — internalize this before touching anything

**Versioning is derived from the latest _stable_ tag, never the latest tag.** That single rule
is what makes rc iteration and promotion work from just two inputs (`bump` + `prerelease`):

- `bump` + `--pre`, run repeatedly → `rc.1`, `rc.2`, … of the **same** target. The target
  doesn't move because prereleases don't count as the latest stable.
- `bump` **without** `--pre` → promotes that target to the stable release.
- **Keep the bump the same** across an rc cycle and its promotion. Changing it mid-cycle
  silently retargets the release.

**Pre-1.0 bump rule (Rune is `0.y.z` today):** by SemVer convention a *breaking* change bumps
**minor**, not major. `recommend` already applies this — but Rune never auto-infers the bump,
so the human always confirms it.

**The changelog is built from Conventional-Commit _PR titles_.** PRs are squash-merged and the
title becomes the commit subject git-cliff parses. `feat`→Added, `fix`→Fixed,
`perf`/`refactor`→Changed, `!`/`BREAKING CHANGE`→breaking; `docs`→Documentation;
`chore`/`ci`/`test`/`build`/`style` are omitted. If the unreleased log is all omitted types,
there may be nothing worth releasing — say so.

## Workflow: cutting a release

Follow this order. It's the safe path and it keeps the user in control of the two things only a
human should decide: the bump and the go-ahead.

1. **Orient.** Run `plan <bump> [--pre]` if the user already named a bump; otherwise run
   `recommend` first to propose one, then `plan` with it. `plan` shows the unreleased commits,
   the exact next tag, the changelog preview, and the preflight result in one shot.

2. **Confirm the decision with the user.** Present: the next tag, whether it's a prerelease,
   and the changelog preview. If you ran `recommend`, state the suggested bump *and its reason*
   and ask the user to confirm — never silently pick the bump for them. This is the moment to
   catch "wait, that should be a minor."

3. **Clear preflight.** `trigger` will happily dispatch even if preflight fails, but the
   workflow's own guard will then refuse (dirty tree or non-green CI). So resolve every ✗ from
   preflight *first*. Common ones: HEAD not pushed to `origin/main`, or CI still running —
   both mean "wait, don't release yet." CI is always still running right after a merge **and
   right after a previous release** (the workflow pushes a changelog commit to `main`, which
   starts its own CI run). `git pull` first, then wait for that run (`gh run watch`) and re-run
   `preflight` rather than triggering early.

   **Read the changelog preview before asking for the go-ahead.** An empty preview for a
   **stable** release is a stop sign, not a detail: it means the published notes would be
   empty. (This happened when promoting v0.8.0 after two rcs; the workflow now builds a stable
   release's notes from the previous stable tag, and the preview mirrors that.) An empty
   preview for an rc is normal when only `build`/`ci`/`chore` commits landed since the last rc.
   Also say out loud when a user-visible change is hidden because its PR title type is omitted
   from the changelog (`build`, `ci`, `chore`, `test`, `style`).

4. **Trigger.** Run `trigger <bump> [--pre]`. It prints the repo, bump, prerelease flag, and
   the tag it will cut, then dispatches. Because this tags, commits a changelog to `main`, and
   publishes binaries/images/packages — an outward-facing, hard-to-undo action — **only run it
   after the user has explicitly approved this specific release** in step 2. If in doubt, stop
   and ask; don't trigger on a vague "yeah do the release thing" without having shown the plan.

5. **Approve + watch.** The workflow pauses on a protected `release` environment prompt that
   only authorized maintainers can approve in the Actions UI — tell the user to approve it
   (you can't), and give them the run URL. Then run `watch` to follow the run to completion.
   - A run that still shows `waiting` has **not** been approved yet, whatever was said in
     chat: check `gh run view <id> --json status,jobs` and tell the user if it is still
     waiting (Review deployments → tick `release` → Approve and deploy).
   - A **stable** release prompts **twice**: once for `release`, and again for
     `publish-extension`, which uses the same protected environment.

6. **Verify.** Once green, run `verify <tag>` and walk the user through (or run) every check
   it prints. A green run is not proof; each of these has caught or would catch a real fault:
   - **Release notes body is not empty** (it was empty from v0.7.0 to v0.8.0 without anyone
     noticing; the workflow now repairs it, but look).
   - **Tags:** `<version>`, `<version>-amd64`, `<version>-arm64`; each per-architecture tag has
     the same digest as the index entry for that platform.
   - **`latest`:** moved to the new version for a stable release, and did **not** move for an rc.
   - **Signatures** on the index digest **and** both per-architecture digests, with the exact
     workflow identity; **provenance** with `gh attestation verify`.
   - **Bill of materials** retrievable from the registry.
   - **Image contract:** `scripts/check-image.sh ghcr.io/<repo>:<version> <version>`.
   - **Binary identity:** the binary in the image equals the one in the archive, per platform.
   - For a stable release: Homebrew tap and Scoop bucket got their commits.

   Tools: `cosign` ships in the `goreleaser/goreleaser` image, and `crane` installs with
   `go install github.com/google/go-containerregistry/cmd/crane@v0.22.1` in a `golang`
   container; neither needs to be on the host. A `docker pull` right after publishing can be
   denied once by the registry; retry before treating it as a failure.

7. **After a stable release, prove `--self-upgrade` for real.** It only upgrades to the latest
   *stable* release, so this is the first moment it can run end to end. Take the previous
   version's Linux binary (an rc of the same line works), run it as a non-root user in a
   container **that has CA certificates** (`golang:*-bookworm`; `debian:*-slim` has none and
   fails TLS), and check: it reports `upgraded`, the new binary's SHA-256 equals the release
   archive's, a second run says `current`, and nothing is left beside the binary.

## Use a release candidate to test the pipeline itself

When a change touches **how** a release is built or published (the image build, signing, the
workflow, the base image), cut an rc before the stable release. A prerelease runs the real
workflow, but never moves `latest`, skips Homebrew and Scoop (`skip_upload: auto`), and never
publishes the extension, so a fault stays contained. Changes to release steps cannot be
exercised any other way: CI's dry run is a snapshot and skips publishing, signing and the
changelog. Test such changes locally as far as possible first (run the exact `git cliff`
commands with `GITHUB_TOKEN` set: without a token git-cliff's GitHub lookups get HTTP 403 and
it panics), and say plainly that the next release is their first real run.

## The container image and its base

The image is built by ko inside GoReleaser, on a base image that lives in the **same** package
under a `base-<id>` tag and is pinned by digest in `.goreleaser.yaml` and `.ko.yaml`
(`test/release` fails if they differ). Before a release, the pinned base must be **signed**:

- The **Base image** workflow (`base-image.yml`) builds, pushes and signs it. It is the only
  build whose digest may be pinned: the digest depends on the crane binary that compresses the
  layer, so the same script on another machine gives the same content under a different digest.
- To sign a base that is already pinned, run it with the same distroless digest and
  `expected_digest` set to the pinned digest; it refuses to sign anything else.
- `cosign verify ghcr.io/<repo>@<base digest>` with the identity
  `.../.github/workflows/base-image.yml@refs/heads/main` must succeed before you release.

## Recovery

If `watch` reports a failure, or a release ends up partial, **don't blindly re-trigger.** Read
`references/recover.md` — the workflow is designed to be safely re-runnable, but the exact move
depends on *where* it failed (before vs. after the tag was pushed, and whether the Homebrew/
Scoop commits went through). Diagnose first, then act.

## Guardrails

- **Never edit `CHANGELOG.md`, create tags, or push by hand to force a release.** The workflow
  owns all of that. Doing it manually desyncs the changelog from git-cliff and breaks the
  stable-tag versioning the next release depends on.
- **Tests run inside Docker on this project, never on the host** (project policy). If you need
  to validate anything test-related as part of release readiness, use `rune test` /
  `docker-compose run --rm test …`.
- **Never commit, push, open a PR, edit a release, or run a publishing workflow on an implied
  go-ahead.** Approval for one outward step (say, a registry copy) is not approval for the
  next (a commit and PR). Ask for each.
- **Don't relax the guards.** Dirty tree and non-green CI are refusals by design. The fix is to
  clean/push/wait, not to work around the check.
- For local sanity before a release you can run `rune release-check` (validate GoReleaser
  config) and `rune release-dryrun` (snapshot build, no publish) — see the repo `Runefile`.

## Reference

- `references/recover.md` — diagnosing and recovering failed or partial releases.
- Canonical docs the skill tracks: `docs/releasing.md`, `.github/workflows/release.yml`,
  `.goreleaser.yaml`, `cliff.toml`. If any of those change, prefer them over this file and
  update the script's mirrored logic to match.
