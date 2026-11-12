#!/bin/sh
# Builds Rune's container image locally with ko. It never pushes anywhere.
#
#   scripts/build-image.sh load      build and load into Docker as rune:local
#   scripts/build-image.sh export    build and write an OCI layout to dist/image
#
# "export" needs no Docker daemon. The Runefile tasks `docker` and
# `docker-export` call this. Releases do not: they build the image through
# GoReleaser (.goreleaser.yaml), from the same settings as .ko.yaml.
set -eu

mode=${1:-}
case $mode in
load | export) ;;
*)
	echo "usage: build-image.sh load|export" >&2
	exit 2
	;;
esac

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "$2" >&2
		exit 1
	fi
}
need go "go is not installed. Install Go 1.27 or newer: https://go.dev/dl/"
need ko "ko is not installed. Install it with: go install github.com/google/ko@latest"

cd -- "$(dirname -- "$0")/.."
revision=$(git rev-parse HEAD 2>/dev/null || echo none)

# The image's platforms come from .ko.yaml and --platform. ko refuses to run
# when a cross-compiling shell also exports these.
unset GOOS GOARCH GOARM

# ko's config file has no place for labels, so they are passed here. A local
# build is not a release: it reports version "dev".
set -- --bare \
	--image-label "org.opencontainers.image.title=rune" \
	--image-label "org.opencontainers.image.description=A shared task runner for humans and AI agents" \
	--image-label "org.opencontainers.image.source=https://github.com/rune-task-runner/rune" \
	--image-label "org.opencontainers.image.licenses=MIT" \
	--image-label "org.opencontainers.image.version=dev" \
	--image-label "org.opencontainers.image.revision=$revision"

if [ "$mode" = load ]; then
	need docker "docker is not installed; it is needed to load the image. To build without it: rune docker-export"
	# ko.local is ko's name for "the local Docker daemon", not a registry.
	# A daemon holds one platform, so build only the one this machine runs.
	# GOHOSTARCH, not GOARCH: an exported GOARCH names a cross-compile target.
	KO_DOCKER_REPO=ko.local/rune ko build "$@" --tags local --platform "linux/$(go env GOHOSTARCH)" ./cmd/rune
	docker tag ko.local/rune:local rune:local
	echo "loaded rune:local"
else
	mkdir -p dist
	# ko appends to an existing layout, so an earlier export would stay listed
	# in index.json beside the new one.
	rm -rf dist/image
	# ko wants a repository name even when it does not push. ko.local would make
	# it look for a Docker daemon; a name under .invalid can never be reached.
	KO_DOCKER_REPO=rune.invalid/rune ko build "$@" --push=false --oci-layout-path dist/image ./cmd/rune
	echo "wrote dist/image"
fi
