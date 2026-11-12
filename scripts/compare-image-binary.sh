#!/bin/sh
# Checks that the rune binary inside a container image is byte-identical to a
# binary on disk, normally the one GoReleaser put in dist/. The docs say the
# image contains the release binary; this is what keeps that true.
#
#   scripts/compare-image-binary.sh <image-ref> <binary-path>
#
# Needs docker. Reads only.
set -eu

if [ $# -ne 2 ]; then
	echo "usage: compare-image-binary.sh <image-ref> <binary-path>" >&2
	exit 2
fi
image=$1
binary=$2
if [ ! -f "$binary" ]; then
	echo "no such file: $binary" >&2
	exit 2
fi

work=$(mktemp -d)
container=
trap 'rm -rf "$work"; [ -z "$container" ] || docker rm "$container" >/dev/null' EXIT
container=$(docker create "$image")
if ! docker cp "$container":/ko-app/rune "$work/rune" >/dev/null; then
	echo "FAIL $image has no /ko-app/rune"
	exit 1
fi

in_image=$(sha256sum "$work/rune" | cut -d' ' -f1)
on_disk=$(sha256sum "$binary" | cut -d' ' -f1)
echo "image  $in_image  $image:/ko-app/rune"
echo "disk   $on_disk  $binary"
if [ "$in_image" != "$on_disk" ]; then
	echo "FAIL binary differs: the image does not contain the release binary"
	exit 1
fi
echo "OK identical"
