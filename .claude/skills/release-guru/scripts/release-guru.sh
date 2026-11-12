#!/usr/bin/env bash
# release-guru.sh — the deterministic engine behind the release-guru skill.
#
# It mirrors, byte-for-byte where it matters, the version + changelog logic in
# .github/workflows/release.yml so an agent never has to re-derive release math by
# hand (the #1 way a release goes wrong). Every subcommand is READ-ONLY except
# `trigger`, which fires the GitHub workflow and prints its confirmation up front.
#
# Repo resolution: uses `gh repo view` unless RUNE_REPO=owner/name is exported.
# The canonical release repo is rune-task-runner/rune; a fork's origin will differ, so the
# CI-status check always queries the repo gh reports as the default remote.
#
# Usage:  release-guru.sh <command> [args]
#   status                      full snapshot: repo, branch, tags, unreleased log
#   next-version <bump> [--pre] just print the tag that would be cut
#   recommend                   suggest a bump from unreleased Conventional Commits
#   changelog [<bump> [--pre]]  preview the unreleased changelog section
#   preflight                   gate checks (clean tree, on main, pushed, CI green)
#   plan <bump> [--pre]         preflight + version + changelog, the full pre-cut brief
#   trigger <bump> [--pre]      *fires* the Release workflow (asks the caller to confirm)
#   watch                       tail the most recent Release workflow run
#   verify <version>            print/run the artifact verification for a published tag
#
#   --pre / --prerelease  cut a -rc.N instead of a stable tag.
set -euo pipefail

# ---- small ui helpers -------------------------------------------------------
if [ -t 1 ]; then B=$'\033[1m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; C=$'\033[36m'; X=$'\033[0m'; else B= R= G= Y= C= X=; fi
say()  { printf '%s\n' "$*"; }
head() { printf '\n%s== %s ==%s\n' "$B" "$*" "$X"; }
ok()   { printf '%s✓%s %s\n' "$G" "$X" "$*"; }
warn() { printf '%s!%s %s\n' "$Y" "$X" "$*"; }
err()  { printf '%s✗%s %s\n' "$R" "$X" "$*" >&2; }
die()  { err "$*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

need_git() { git rev-parse --git-dir >/dev/null 2>&1 || die "not inside a git repository"; }

# pretty-print a stable version string (or <none>)
show_stable() { if [ -n "$1" ]; then printf 'v%s' "$1"; else printf '<none>'; fi; }

# repo in owner/name form
resolve_repo() {
  if [ -n "${RUNE_REPO:-}" ]; then printf '%s' "$RUNE_REPO"; return; fi
  have gh || die "gh CLI not found and RUNE_REPO not set"
  gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null \
    || die "could not resolve repo via gh; set RUNE_REPO=owner/name"
}

# ---- version math (mirrors release.yml "Compute next version") --------------
# Latest STABLE tag only (vX.Y.Z, no suffix), numerically sorted; empty -> 0.0.0.
last_stable() {
  git tag --list 'v[0-9]*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | sed 's/^v//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n1 || true
}

# next_version <bump> <prerelease:true|false> -> prints the tag (e.g. v0.5.0-rc.2)
next_version() {
  local bump="$1" pre="$2" ls ma rest mi pa target maxrc next
  ls="$(last_stable)"; ls="${ls:-0.0.0}"
  ma="${ls%%.*}"; rest="${ls#*.}"; mi="${rest%%.*}"; pa="${rest##*.}"
  case "$bump" in
    major) ma=$((ma + 1)); mi=0; pa=0 ;;
    minor) mi=$((mi + 1)); pa=0 ;;
    patch) pa=$((pa + 1)) ;;
    *) die "bump must be one of: patch minor major (got '$bump')" ;;
  esac
  target="${ma}.${mi}.${pa}"
  if [ "$pre" = "true" ]; then
    maxrc=$(git tag --list "v${target}-rc.*" | sed -E "s/^v${target}-rc\.//" \
      | grep -E '^[0-9]+$' | sort -n | tail -n1 || true)
    next="v${target}-rc.$(( ${maxrc:-0} + 1 ))"
  else
    next="v${target}"
  fi
  printf '%s' "$next"
}

# parse trailing --pre/--prerelease out of the args; sets PRE and strips it
PRE=false
parse_pre() {
  local a keep=(); for a in "$@"; do
    case "$a" in --pre|--prerelease) PRE=true ;; *) keep+=("$a") ;; esac
  done
  ARGS=("${keep[@]:-}")
}

# ---- commands ---------------------------------------------------------------
cmd_status() {
  need_git
  local repo ls branch sha ahead
  repo="$(resolve_repo)"; ls="$(last_stable)"
  branch="$(git rev-parse --abbrev-ref HEAD)"
  sha="$(git rev-parse --short HEAD)"
  head "Release status — $repo"
  say "branch           : $branch @ $sha"
  say "latest stable tag: $(show_stable "$ls")"
  say "latest any tag   : $(git describe --tags --abbrev=0 2>/dev/null || echo '<none>')"
  head "Unreleased commits (since $( [ -n "$ls" ] && echo "v$ls" || echo 'repo start'))"
  local range; if [ -n "$ls" ]; then range="v${ls}..HEAD"; else range="HEAD"; fi
  git log --no-merges --pretty='  %s' "$range" 2>/dev/null | grep -v '^  chore(release):' || say "  (none)"
  head "Suggested next versions"
  say "  patch → $(next_version patch false)      minor → $(next_version minor false)      major → $(next_version major false)"
  say "  (add --pre for rc:  minor+pre → $(next_version minor true))"
}

cmd_next_version() { need_git; parse_pre "$@"; local bump="${ARGS[0]:-}"; [ -n "$bump" ] || die "usage: next-version <patch|minor|major> [--pre]"; next_version "$bump" "$PRE"; echo; }

# Recommend a bump from unreleased Conventional Commits. Pre-1.0 (0.y.z), a
# breaking change bumps MINOR by SemVer convention — the same rule docs/releasing.md
# states. The maintainer always makes the final call; this is a suggestion.
cmd_recommend() {
  need_git
  local ls range major reason bump
  ls="$(last_stable)"; ls="${ls:-0.0.0}"; major="${ls%%.*}"
  if [ "$ls" = "0.0.0" ]; then range="HEAD"; else range="v${ls}..HEAD"; fi
  local log; log="$(git log --no-merges --pretty='%s%n%b' "$range" 2>/dev/null || true)"
  local has_break=false has_feat=false has_fix=false
  grep -qiE '(^|[^a-z])(feat|fix|refactor|perf)(\([^)]*\))?!:' <<<"$log" && has_break=true
  grep -qi  'BREAKING CHANGE' <<<"$log" && has_break=true
  grep -qE  '^feat(\([^)]*\))?:' <<<"$log" && has_feat=true
  grep -qE  '^(fix|perf|refactor)(\([^)]*\))?:' <<<"$log" && has_fix=true

  if $has_break; then
    if [ "$major" -eq 0 ]; then bump=minor; reason="breaking change present; pre-1.0 → minor (SemVer convention)"
    else bump=major; reason="breaking change present; ≥1.0 → major"; fi
  elif $has_feat; then bump=minor; reason="new feature(s) present, no breaking change → minor"
  elif $has_fix;  then bump=patch; reason="only fixes/perf/refactors → patch"
  else bump=patch; reason="no changelog-worthy commits found → patch (nothing to release?)"; fi

  head "Bump recommendation"
  say "recommended bump : ${B}${bump}${X}"
  say "reason           : $reason"
  say "would cut        : $(next_version "$bump" false)   (stable)   |   $(next_version "$bump" true)   (rc)"
  say ""
  warn "This is a suggestion. Rune never auto-infers the bump — you choose it."
}

cmd_changelog() {
  need_git; parse_pre "$@"
  local bump="${ARGS[0]:-}" tag=""
  if [ -n "$bump" ]; then tag="$(next_version "$bump" "$PRE")"; fi
  head "Unreleased changelog preview${tag:+ (as $tag)}"
  if have git-cliff; then
    # Mirrors the workflow: a stable release is described from the previous STABLE
    # tag with the rc tags ignored, or a promoted release would preview as empty.
    local stable; stable="$(last_stable)"
    if [ -n "$tag" ] && [ "$PRE" != true ] && [ -n "$stable" ]; then
      git cliff "v${stable}..HEAD" --ignore-tags 'rc\.[0-9]+$' --tag "$tag" --strip header
    elif [ -n "$tag" ]; then git cliff --unreleased --tag "$tag" --strip header
    else git cliff --unreleased --strip header; fi
  else
    warn "git-cliff not installed locally — showing raw Conventional-Commit subjects instead."
    warn "CI generates the real notes with git-cliff (see cliff.toml). Install: https://git-cliff.org"
    local ls range; ls="$(last_stable)"; if [ -n "$ls" ]; then range="v${ls}..HEAD"; else range="HEAD"; fi
    git log --no-merges --pretty='  - %s' "$range" | grep -vE '^\s*- (chore|ci|test|style|build)(\(|:)' || say "  (nothing changelog-worthy)"
  fi
}

cmd_preflight() {
  need_git
  local repo sha branch fail=0
  repo="$(resolve_repo)"
  branch="$(git rev-parse --abbrev-ref HEAD)"
  sha="$(git rev-parse HEAD)"
  head "Preflight — $repo @ ${sha:0:7}"

  if [ "$branch" = "main" ]; then ok "on main"; else warn "on '$branch', not main — the workflow only releases from main"; fi

  if [ -z "$(git status --porcelain)" ]; then ok "working tree clean"; else err "working tree is DIRTY — the release guard will refuse"; fail=1; fi

  # HEAD must be pushed: the workflow releases origin/main's HEAD, not your local one.
  git fetch -q origin main 2>/dev/null || warn "could not fetch origin/main"
  if [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main 2>/dev/null || echo none)" ]; then
    ok "HEAD matches origin/main"
  else
    err "HEAD differs from origin/main — push (or pull) first; CI runs on what's on origin"; fail=1
  fi

  # CI (ci.yml) conclusion for this exact SHA on push — the same query the workflow guard runs.
  if have gh; then
    local ci
    ci=$(gh api "repos/${repo}/actions/workflows/ci.yml/runs?head_sha=${sha}&event=push" \
      --jq 'if (.workflow_runs | length) == 0 then "missing" else (.workflow_runs | sort_by(.run_started_at) | last | .conclusion // "pending") end' 2>/dev/null || echo "error")
    case "$ci" in
      success) ok "CI green for this commit" ;;
      missing) err "no CI run found for ${sha:0:7} on push — is it pushed? has CI started?"; fail=1 ;;
      pending) err "CI still running for ${sha:0:7} — wait for green"; fail=1 ;;
      *)       err "CI conclusion is '$ci', not success — the release guard will refuse"; fail=1 ;;
    esac
  else
    warn "gh not available — cannot check CI status (the workflow will still enforce it)"
  fi

  if [ "$fail" -eq 0 ]; then head "Preflight PASSED"; ok "safe to trigger a release"; else head "Preflight FAILED"; die "resolve the ✗ items above before releasing"; fi
}

cmd_plan() {
  need_git; parse_pre "$@"
  local bump="${ARGS[0]:-}"; [ -n "$bump" ] || die "usage: plan <patch|minor|major> [--pre]"
  local tag; tag="$(next_version "$bump" "$PRE")"
  cmd_status
  head "This release"
  say "bump             : $bump    prerelease: $PRE"
  say "next tag         : ${B}${tag}${X}"
  git rev-parse -q --verify "refs/tags/${tag}" >/dev/null 2>&1 && die "tag ${tag} already exists — the workflow will refuse to re-release"
  local pre_flag=""; [ "$PRE" = true ] && pre_flag=" --pre"
  PRE_ARG=(); [ "$PRE" = true ] && PRE_ARG=(--pre)
  cmd_changelog "$bump" "${PRE_ARG[@]}"
  cmd_preflight
  head "To cut it"
  say "  ${C}release-guru.sh trigger ${bump}${pre_flag}${X}"
}

cmd_trigger() {
  need_git; parse_pre "$@"
  have gh || die "gh CLI required to trigger the workflow"
  local bump="${ARGS[0]:-}"; [ -n "$bump" ] || die "usage: trigger <patch|minor|major> [--pre]"
  local repo tag; repo="$(resolve_repo)"; tag="$(next_version "$bump" "$PRE")"
  head "Triggering Release workflow"
  say "repo       : $repo"
  say "bump       : $bump"
  say "prerelease : $PRE"
  say "will cut   : ${B}${tag}${X}"
  say ""
  warn "This dispatches .github/workflows/release.yml on main and (after the"
  warn "protected-environment approval) will TAG, CHANGELOG, and PUBLISH ${tag}."
  gh workflow run release.yml -R "$repo" --ref main -f "bump=${bump}" -f "prerelease=${PRE}" \
    && ok "workflow dispatched" || die "gh workflow run failed"
  say ""
  say "Approve the 'release' environment prompt in the Actions UI, then:"
  say "  ${C}release-guru.sh watch${X}"
}

cmd_watch() {
  have gh || die "gh CLI required"
  local repo; repo="$(resolve_repo)"
  local id
  id=$(gh run list -R "$repo" --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId' 2>/dev/null || true)
  [ -n "$id" ] || die "no Release workflow runs found"
  head "Watching Release run $id"
  gh run watch "$id" -R "$repo" --exit-status || { err "release run failed — see recovery guidance in references/recover.md"; exit 1; }
  ok "release run completed successfully"
  gh run view "$id" -R "$repo" --json url -q .url
}

cmd_verify() {
  local ver="${1:-}"; [ -n "$ver" ] || die "usage: verify <version>  (e.g. v0.2.0 or 0.2.0)"
  local repo; repo="$(resolve_repo)"
  local nov="${ver#v}"
  head "Verification for $ver — $repo"
  if ! have cosign; then warn "cosign not installed — install it to run signature checks (https://docs.sigstore.dev)"; fi
  if ! have gh;     then warn "gh not installed — needed for attestation checks"; fi
  say "Run these against the published artifacts (download checksums.txt + its bundle from the release):"
  cat <<EOF

  # 1. checksums
  sha256sum --check checksums.txt --ignore-missing

  # 2. signature of checksums.txt (covers every archive)
  cosign verify-blob --bundle checksums.txt.sigstore.json \\
    --certificate-identity-regexp 'https://github.com/${repo}/.*' \\
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' checksums.txt

  # 3. build provenance
  gh attestation verify checksums.txt --repo ${repo}

  # 4. image signature + provenance
  cosign verify ghcr.io/${repo}:${nov} \\
    --certificate-identity-regexp 'https://github.com/${repo}/.*' \\
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
  gh attestation verify oci://ghcr.io/${repo}:${nov} --repo ${repo}
EOF
}

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; }

main() {
  local cmd="${1:-}"; shift || true
  case "$cmd" in
    status)        cmd_status "$@" ;;
    next-version)  cmd_next_version "$@" ;;
    recommend)     cmd_recommend "$@" ;;
    changelog)     cmd_changelog "$@" ;;
    preflight)     cmd_preflight "$@" ;;
    plan)          cmd_plan "$@" ;;
    trigger)       cmd_trigger "$@" ;;
    watch)         cmd_watch "$@" ;;
    verify)        cmd_verify "$@" ;;
    ""|-h|--help|help) usage ;;
    *) err "unknown command: $cmd"; usage; exit 2 ;;
  esac
}
main "$@"
