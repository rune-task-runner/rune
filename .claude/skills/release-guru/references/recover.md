# Recovering a failed or partial release

The Release workflow is built to be safely re-runnable, but **where** it failed decides the
right move. Diagnose before acting — a blind re-trigger can double-tag or leave the changelog
ahead of the artifacts.

## Step 1: find out how far it got

```sh
gh run view <run-id> -R rune-task-runner/rune            # which step failed
git fetch --tags origin
git tag --list 'v<target>*'                      # did the tag get created + pushed?
gh release view v<target> -R rune-task-runner/rune       # is there a (draft?) GitHub release?
```

The workflow's ordered checkpoints, each safe to cross twice:

1. **Guards** (clean tree / green CI) — nothing published yet. Fix the cause and re-trigger.
2. **Compute version** — refuses if the tag already exists. Idempotent.
3. **Changelog commit to `main`** + **tag push** — the first durable side effects.
4. **GoReleaser** — binaries, GHCR image, sigs, SBOMs, provenance.
5. **Homebrew cask / Scoop manifest** commits — the one spot that may need manual cleanup.

## Step 2: pick the recovery by failure point

**Failed at guards or version compute (before the tag):**
Nothing was published. Fix the underlying issue (push HEAD, wait for green CI, resolve a
dirty tree) and re-run the workflow with the *same* inputs. `--clean` wipes `dist/` first, so
there's no stale-artifact risk.

**Failed after the tag was pushed but during/after GoReleaser:**
GitHub Releases are upserted and GHCR pushes are content-addressed, so **re-running is safe** —
re-trigger the workflow (it will find the tag exists and… refuse at compute). Because compute
refuses an existing tag, the clean recovery is one of:

- **Re-run GoReleaser against the existing tag** locally or by re-dispatching only the publish
  step, *or*
- **Delete the tag and the (draft) release, then re-trigger** from scratch:
  ```sh
  gh release delete v<target> -R rune-task-runner/rune --yes    # remove the release first
  git push origin :refs/tags/v<target>                  # delete the remote tag
  git tag -d v<target>                                   # and the local tag
  ```
  Then re-run the workflow. Only do the delete path if publishing clearly did not complete —
  never delete a tag whose artifacts are already public and may be depended on.

**Homebrew tap / Scoop bucket left half-updated:**
Check `rune-task-runner/homebrew-tap` and `rune-task-runner/scoop-bucket` before re-running — a mid-way failure
can leave a bad or duplicate commit there. Revert/clean it manually, then re-run; `skip_upload:
auto` keeps prereleases off the stable channel, so an rc won't touch these.

**"Tag per-architecture images" failed (the last step):**
The release, the version tag, the signatures and the attestation are already published; only
`<version>-amd64` and `<version>-arm64` are missing. **Do not re-run the job** (GitHub re-runs
from the first step, which would compute a new version). Add the two tags by hand, logged in to
GHCR; they only point at images that already exist:
```sh
repo=ghcr.io/rune-task-runner/rune
for arch in amd64 arm64; do
  crane tag "${repo}@$(crane digest --platform "linux/${arch}" "${repo}:<version>")" "<version>-${arch}"
done
```

**The release page has no notes (empty body):**
The workflow's "Ensure the release notes are published" step should have set them. If a
release still has an empty body, do not edit `CHANGELOG.md`; copy the notes the run rendered
(search its log for `----- release notes -----`) into a file and set the body, after the user
agrees:
```sh
id=$(gh api repos/rune-task-runner/rune/releases/tags/<tag> --jq .id)
gh api -X PATCH repos/rune-task-runner/rune/releases/$id -F body=@notes.md
```
The cause that time was `changelog: disable: true` in `.goreleaser.yaml`, which also discards
the `--release-notes` file. `test/release` now fails if that setting comes back.

**A stable release's changelog section came out empty after release candidates:**
The rc sections had already taken the commits. Fixed in the workflow (stable notes are built
from the previous stable tag with rc tags ignored). If you see it in a `plan` preview, stop
before triggering and find out why the preview and the workflow disagree.

**The image build cannot fetch the base image:**
GoReleaser fails with `fetching base image: ... MANIFEST_UNKNOWN` (or `UNAUTHORIZED`). The
digest pinned in `.goreleaser.yaml` does not exist in the `rune` package, or the package is
not public. Check `crane digest ghcr.io/rune-task-runner/rune@<digest>` without credentials.
Nothing was pushed for the image; fix the pin through a pull request and re-run per the
"after the tag was pushed" rules above.

**The Base image workflow refuses to sign ("built X, expected Y: not signing"):**
The rebuild did not reproduce the pinned digest, usually because the pinned digest was built
somewhere other than the workflow. Compare the two images (`crane manifest`, `crane config`);
if only the compressed added layer differs, pin the digest the workflow built, through a pull
request, then run the workflow again with that digest as `expected_digest`.

**The Base image workflow is denied (`DENIED: permission_denied: write_package`):**
The package it pushes to is not linked to the repository. This cannot happen while the base
lives in the `rune` package; if a separate package is ever used again, the maintainer must add
the repository under that package's "Manage Actions access" with Write.

## Step 3: confirm the recovery

After any recovery, run `release-guru.sh verify <tag>` and confirm the checksum, signature, and
provenance all pass. A release isn't recovered until verification is green.

## When in doubt

Stop and surface the state to the user (which tags exist, whether the release is drafted,
whether the tap/bucket commits landed) rather than guessing. A wrong tag or a changelog that's
ahead of the published artifacts is more painful to unwind than a paused release.
