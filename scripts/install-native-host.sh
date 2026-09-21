#!/bin/sh
# Install the per-user Chrome Native Messaging host manifest for this project.
#
# Usage:
#   scripts/install-native-host.sh <extension-id> [--binary ABSOLUTE_PATH]
#
# <extension-id> is the 32-character ID of the actually loaded unpacked
# extension from chrome://extensions. It is required, validated against
# Chrome's extension-ID alphabet (a-p), and never guessed or defaulted. The
# rendered manifest contains exactly one allowed origin:
#   chrome-extension://<extension-id>/
# and never a wildcard. Re-running the installer replaces the single canonical
# registration instead of creating duplicates.

set -eu

host_name=com.neelbangera.lecturetranscripts
manifest_name=$host_name.json

fail() {
    printf 'install-native-host: %s\n' "$1" >&2
    exit 1
}

usage() {
    printf 'usage: %s <extension-id> [--binary ABSOLUTE_PATH]\n' "$0" >&2
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)
template=$repo_root/native-host/$manifest_name.in
binary_path=$repo_root/dist/native/lecture-uploader

extension_id=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --binary)
            [ "$#" -ge 2 ] || fail "--binary requires an absolute path argument"
            binary_path=$2
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        -*)
            fail "unknown option: $1"
            ;;
        *)
            [ -z "$extension_id" ] || fail "unexpected extra argument: $1"
            extension_id=$1
            shift
            ;;
    esac
done

if [ -z "$extension_id" ]; then
    usage
    fail "missing required <extension-id> argument"
fi

case "$extension_id" in
    *[!a-p]*) fail "invalid extension ID: only the characters a-p are allowed" ;;
esac
if [ "${#extension_id}" -ne 32 ]; then
    fail "invalid extension ID: expected exactly 32 characters, got ${#extension_id}"
fi

[ "$(uname -s)" = "Darwin" ] || fail "unsupported platform $(uname -s): the host manifest targets macOS Chrome"

case "$binary_path" in
    /*) : ;;
    *) fail "--binary must be an absolute path: $binary_path" ;;
esac
case "$binary_path" in
    *'"'*|*'\'*|*'
'*) fail "binary path may not contain quotes, backslashes, or newlines" ;;
esac
[ -f "$binary_path" ] || fail "uploader binary not found: $binary_path (run scripts/build-uploader.sh first)"
[ -x "$binary_path" ] || fail "uploader binary is not executable: $binary_path"

[ -f "$template" ] || fail "template not found: $template"
[ "$(grep -c '{{BINARY_PATH}}' "$template" || true)" -eq 1 ] || fail "template must contain exactly one {{BINARY_PATH}} placeholder"
[ "$(grep -c '{{EXTENSION_ID}}' "$template" || true)" -eq 1 ] || fail "template must contain exactly one {{EXTENSION_ID}} placeholder"

manifest_dir="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
manifest=$manifest_dir/$manifest_name
origin=chrome-extension://$extension_id/

umask 077
mkdir -p "$manifest_dir"

tmp=$(mktemp "$manifest_dir/.$manifest_name.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM

escape_sed_replacement() {
    printf '%s' "$1" | sed 's/[\\&|]/\\&/g'
}

escaped_binary=$(escape_sed_replacement "$binary_path")
escaped_id=$(escape_sed_replacement "$extension_id")
sed -e "s|{{BINARY_PATH}}|$escaped_binary|g" -e "s|{{EXTENSION_ID}}|$escaped_id|g" "$template" > "$tmp"
chmod 0600 "$tmp"

if grep -q '{{' "$tmp"; then
    fail "rendered manifest still contains a template placeholder"
fi

origin_count=$(grep -c 'chrome-extension://' "$tmp" || true)
if [ "$origin_count" -ne 1 ]; then
    fail "rendered manifest must contain exactly one allowed origin, found $origin_count"
fi
grep -Fq "\"$origin\"" "$tmp" || fail "rendered manifest does not contain the exact origin $origin"
grep -Fq "\"name\": \"$host_name\"" "$tmp" || fail "rendered manifest has the wrong name"
grep -Fq '"type": "stdio"' "$tmp" || fail "rendered manifest has the wrong type"
grep -Fq "\"path\": \"$binary_path\"" "$tmp" || fail "rendered manifest has the wrong path"

if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "$tmp" || fail "rendered manifest is not valid JSON"
elif command -v plutil >/dev/null 2>&1; then
    plutil -lint "$tmp" >/dev/null || fail "rendered manifest is not valid JSON"
else
    fail "no JSON validator available (python3 or plutil); refusing to install"
fi

print_next_steps() {
    cat <<EOF

Next steps:
  1. Fully quit and reopen Chrome so it reads the new Native Messaging host registration.
  2. Load the unpacked extension from $repo_root/dist/extension if it is not loaded yet; the installed ID is $extension_id.
     Moving or reloading the unpacked extension from a different path can change its ID; rerun this installer with the new ID.
  3. Create $HOME/Library/Application Support/LectureTranscripts/config.json with the real GitHub App client ID, numeric repository ID, owner, repo, and branch.
  4. Authorize GitHub from the extension popup; choose Always Allow if macOS prompts for Keychain access.
EOF
}

if [ -f "$manifest" ]; then
    if cmp -s "$tmp" "$manifest"; then
        chmod 0600 "$manifest"
        printf 'install-native-host: already installed with identical content: %s\n' "$manifest"
        printf 'install-native-host: allowed origin: %s\n' "$origin"
        print_next_steps
        exit 0
    fi
    printf 'install-native-host: replacing existing registration: %s\n' "$manifest"
elif [ -e "$manifest" ]; then
    fail "$manifest exists and is not a regular file"
fi

mv -f "$tmp" "$manifest"
chmod 0600 "$manifest"

mode=$(stat -f '%Lp' "$manifest")
[ "$mode" = "600" ] || fail "installed manifest mode is $mode, expected 600"

printf 'install-native-host: installed %s (mode 0600)\n' "$manifest"
printf 'install-native-host: allowed origin: %s\n' "$origin"
print_next_steps
