#!/bin/sh
# Asserts the contract of Rune's container image: its configuration, the files
# it must contain, and how it behaves with a project mounted at /work. The
# contract is specs/036-ko-container-image/contracts/image.md.
#
#   scripts/check-image.sh <image-ref> [expected-version]
#
# Reads only: nothing is pushed or tagged. Needs docker.
set -eu

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
	echo "usage: check-image.sh <image-ref> [expected-version]" >&2
	exit 2
fi
image=$1
want_version=${2:-}

if ! command -v docker >/dev/null 2>&1; then
	echo "docker is not installed; it is needed to inspect and run the image" >&2
	exit 2
fi
if ! docker image inspect "$image" >/dev/null 2>&1; then
	docker pull -q "$image" >/dev/null
fi

# The image's user is not the one who owns this checkout, so mount a copy that
# anyone can read, whatever the checkout's permissions are.
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
cp "$(dirname -- "$0")/../test/image/fixture/Runefile" "$fixture/Runefile"
chmod 0755 "$fixture"
chmod 0644 "$fixture/Runefile"
checks=0
fails=0

fail() {
	fails=$((fails + 1))
	echo "FAIL $1: got $2, want $3"
}

# equal <check> <got> <want>
equal() {
	checks=$((checks + 1))
	[ "$2" = "$3" ] || fail "$1" "$2" "$3"
}

# contains <check> <text> <needle>
contains() {
	checks=$((checks + 1))
	case $2 in
	*"$3"*) ;;
	*) fail "$1" "$(printf '%s' "$2" | head -n 3 | tr '\n' ' ')" "text containing '$3'" ;;
	esac
}

cfg() {
	docker image inspect --format "$1" "$image"
}

label() {
	cfg "{{index .Config.Labels \"org.opencontainers.image.$1\"}}"
}

equal "WorkingDir" "$(cfg '{{.Config.WorkingDir}}')" "/work"
equal "User" "$(cfg '{{.Config.User}}')" "65532"
equal "Entrypoint" "$(cfg '{{json .Config.Entrypoint}}')" '["/ko-app/rune"]'
equal "Cmd" "$(cfg '{{json .Config.Cmd}}')" "null"

env=$(cfg '{{range .Config.Env}}{{println .}}{{end}}')
contains "Env RUNE_CONTAINER" "$env" "RUNE_CONTAINER=1"
contains "Env SSL_CERT_FILE" "$env" "SSL_CERT_FILE=/"

equal "label title" "$(label title)" "rune"
equal "label description" "$(label description)" "A shared task runner for humans and AI agents"
equal "label source" "$(label source)" "https://github.com/rune-task-runner/rune"
equal "label licenses" "$(label licenses)" "MIT"
version=$(label version)
checks=$((checks + 2))
[ -n "$version" ] || fail "label version" "nothing" "a version"
[ -n "$(label revision)" ] || fail "label revision" "nothing" "a commit"
if [ -n "$want_version" ]; then
	equal "label version" "$version" "$want_version"
fi

# The image has no shell to list files with, so read its filesystem as a tar.
container=$(docker create "$image")
files=$(docker export "$container" | tar -tvf - 2>/dev/null || true)
docker rm "$container" >/dev/null
cert=$(printf '%s\n' "$env" | sed -n 's|^SSL_CERT_FILE=/||p')
contains "file /ko-app/rune" "$files" " ko-app/rune"
contains "link /rune" "$files" " rune -> /ko-app/rune"
contains "directory /work" "$files" " work/"
# A volume mounted at /work takes the directory's owner, so it must be the
# image's user or tasks cannot write there.
contains "owner of /work" "$(printf '%s\n' "$files" | sed -n '/ work\/$/p')" "65532/65532"
contains "CA certificates" "$files" " ${cert:-no-cert-path}"

# run <args...>: sets out and code from the image run with the fixture at /work.
run() {
	code=0
	out=$(docker run --rm -v "$fixture":/work:ro "$image" "$@" 2>&1) || code=$?
}

run --version
equal "--version exit code" "$code" "0"
contains "--version output" "$out" "rune version"
if [ -n "$want_version" ]; then
	contains "--version output" "$out" "rune version $want_version "
fi

run --list
equal "--list exit code" "$code" "0"
contains "--list output" "$out" "hello"
contains "--list output" "$out" "fail"

run hello there
equal "task with an argument, exit code" "$code" "0"
contains "task with an argument, output" "$out" "hi there"

run fail
equal "failing task exit code" "$code" "7"

code=0
out=$(docker run --rm -v "$fixture":/work:ro --entrypoint /rune "$image" --version 2>&1) || code=$?
equal "/rune as entrypoint, exit code" "$code" "0"
contains "/rune as entrypoint, output" "$out" "rune version"

code=0
out=$(docker run --rm "$image" --list 2>&1) || code=$?
checks=$((checks + 1))
[ "$code" -ne 0 ] || fail "no mount, --list exit code" "0" "non-zero"
contains "no mount, --list output" "$out" "no Runefile found"

# Rune must decline to upgrade itself inside the image. A release build says
# to pull a newer image; a local build is refused earlier, as a development build.
run --self-upgrade
equal "--self-upgrade exit code" "$code" "1"
if [ "$version" = dev ]; then
	contains "--self-upgrade output" "$out" "development build"
else
	contains "--self-upgrade output" "$out" "docker pull"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails of $checks checks failed for $image"
	exit 1
fi
echo "OK $checks checks for $image"
