#!/bin/sh
# Builds the base image Rune's container image is built on, and pushes it.
#
# ko cannot set a working directory, an environment variable or the path of
# the program in the image; it inherits the first two from the base image. So
# the base is distroless static (non-root) plus what Rune's image promises:
#   /work              the working directory a project is mounted at
#   /rune              a link to where ko puts the binary (/ko-app/rune)
#   RUNE_CONTAINER=1   tells `rune --self-upgrade` it runs from the image
#
#   scripts/build-base-image.sh <distroless-digest> <destination-ref>
#
# Example:
#   scripts/build-base-image.sh sha256:0123... ghcr.io/rune-task-runner/rune:base-012345678901
#
# Prints the pushed index digest as the last line. The image's content depends
# only on the input digest, but its digest also depends on how crane compresses
# the added layer, which varies with the Go release crane was built with. The
# Base image workflow, with its pinned crane release binary, is therefore the
# one build whose digest is pinned in .ko.yaml and .goreleaser.yaml; a build
# on another machine can print a different digest for the same content.
# The source label links the GHCR package to this repository. Needs crane, GNU tar, and a login for the
# destination; no Docker daemon. CRANE_FLAGS is passed to every crane call
# (for example --insecure for a local test registry).
set -eu

usage() {
	echo "usage: build-base-image.sh <distroless-digest> <destination-ref>" >&2
	exit 2
}

[ $# -eq 2 ] || usage
digest=$1
dest=$2
case $digest in
sha256:*) ;;
*)
	echo "the first argument must be a digest (sha256:...), not a tag: a base built from a tag cannot be reproduced" >&2
	usage
	;;
esac
# Only the last path component can carry the tag: a colon before it is a
# registry port.
case ${dest##*/} in
*:*) ;;
*)
	echo "the destination must carry a tag, for example rune:base-$(printf '%s' "${digest#sha256:}" | cut -c1-12)" >&2
	usage
	;;
esac

if ! command -v crane >/dev/null 2>&1; then
	echo "crane is not installed. Install it with: go install github.com/google/go-containerregistry/cmd/crane@latest" >&2
	exit 1
fi

source_image="gcr.io/distroless/static-debian12@$digest"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/layer/work"
chmod 0755 "$work/layer/work"
ln -s /ko-app/rune "$work/layer/rune"
# Fixed owner, time and order, so the layer is byte-identical on every run.
# /work belongs to the image's user, as the Dockerfile's WORKDIR made it: a
# volume mounted there takes that owner, and Rune cannot write to a root-owned one.
tar -C "$work/layer" --owner=0 --group=0 --numeric-owner --mtime=@0 \
	-cf "$work/layer.tar" rune
tar -C "$work/layer" --owner=65532 --group=65532 --numeric-owner --mtime=@0 \
	-rf "$work/layer.tar" work

# Each architecture's image is pushed under the destination tag in turn and
# then referenced by digest, so the package ends up with one tag for the base
# and no per-architecture helper tags.
repo=${dest%:*}
manifests=""
# shellcheck disable=SC2086 # CRANE_FLAGS is a list of flags on purpose
for arch in amd64 arm64; do
	crane append ${CRANE_FLAGS:-} --platform "linux/$arch" \
		-b "$source_image" -f "$work/layer.tar" -t "$dest" >/dev/null
	crane mutate ${CRANE_FLAGS:-} --platform "linux/$arch" \
		--workdir /work --env RUNE_CONTAINER=1 \
		--label "org.opencontainers.image.source=https://github.com/rune-task-runner/rune" \
		-t "$dest" "$dest" >/dev/null
	manifests="$manifests -m $repo@$(crane digest ${CRANE_FLAGS:-} "$dest")"
done
# shellcheck disable=SC2086 # $manifests is a list of -m arguments on purpose
crane index append ${CRANE_FLAGS:-} $manifests -t "$dest" >/dev/null
# shellcheck disable=SC2086
crane digest ${CRANE_FLAGS:-} "$dest"
