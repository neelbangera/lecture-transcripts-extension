#!/bin/sh
# Build the macOS uploader binary into dist/native/lecture-uploader.
#
# Usage:
#   scripts/build-uploader.sh [--version VERSION] [--dry-run]
#
# The uploader requires macOS, the Xcode Command Line Tools, Go 1.24.x, and
# CGO_ENABLED=1 because the Darwin Keychain adapter calls Security.framework
# through cgo. A non-Darwin host or a missing compiler fails closed instead of
# producing a nonfunctional host.
#
# The build version is injected with -ldflags into the conventional
# main.version string variable of uploader/cmd/lecture-uploader. When
# --version is omitted, the version is read from the "version" field in
# package.json.

set -eu

fail() {
    printf 'build-uploader: %s\n' "$1" >&2
    exit 1
}

usage() {
    printf 'usage: %s [--version VERSION] [--dry-run]\n' "$0" >&2
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)

version=
dry_run=no

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] || fail "--version requires an argument"
            version=$2
            shift 2
            ;;
        --dry-run)
            dry_run=yes
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            fail "unknown argument: $1"
            ;;
    esac
done

[ "$(uname -s)" = "Darwin" ] || fail "unsupported platform $(uname -s): the uploader requires macOS"

command -v xcrun >/dev/null 2>&1 || fail "xcrun not found: install the Xcode Command Line Tools with 'xcode-select --install'"
xcode-select -p >/dev/null 2>&1 || fail "Xcode Command Line Tools are not selected: run 'xcode-select --install'"
xcrun --find clang >/dev/null 2>&1 || fail "clang not found: install the Xcode Command Line Tools with 'xcode-select --install'"

command -v go >/dev/null 2>&1 || fail "go not found: install Go 1.24.x"

if [ -z "$version" ]; then
    version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$repo_root/package.json" | head -n 1)
fi
[ -n "$version" ] || fail "could not read a version from package.json; pass --version VERSION"

case "$version" in
    [0-9A-Za-z]*)
        case "$version" in
            *[!0-9A-Za-z._+-]*) fail "invalid version '$version': use only [0-9A-Za-z._+-]" ;;
        esac
        ;;
    *)
        fail "invalid version '$version': must start with an alphanumeric character"
        ;;
esac
if [ "$(printf '%s' "$version" | wc -c | tr -d ' ')" -gt 32 ]; then
    fail "invalid version '$version': at most 32 characters"
fi

output_dir=$repo_root/dist/native
output=$output_dir/lecture-uploader

if [ "$dry_run" = "yes" ]; then
    printf 'build-uploader: dry run: version=%s platform=%s/%s output=%s\n' "$version" "$(uname -s)" "$(uname -m)" "$output"
    exit 0
fi

[ -d "$repo_root/uploader/cmd/lecture-uploader" ] || fail "uploader/cmd/lecture-uploader is missing; nothing to build"

mkdir -p "$output_dir"

CGO_ENABLED=1
export CGO_ENABLED
GOOS=darwin
GOARCH=arm64
export GOOS GOARCH

cd "$repo_root/uploader"
go build -trimpath -ldflags "-X main.version=$version" -o "$output" ./cmd/lecture-uploader

[ -x "$output" ] || fail "build did not produce an executable at $output"
printf 'build-uploader: wrote %s (version %s)\n' "$output" "$version"
