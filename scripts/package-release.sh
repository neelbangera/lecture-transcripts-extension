#!/bin/sh
# Assemble versioned release assets for Lecture Transcripts (D2).
#
# Usage:
#   scripts/package-release.sh --app-client-id ID --app-install-url URL [--version VERSION] [--out-dir DIR]
#
# Builds the extension and the macOS uploader from the same tree, verifies
# product/protocol version compatibility, and writes into <out-dir> (default
# dist/release):
#   lecture-transcripts-extension-v<VERSION>.zip   (compiled extension)
#   lecture-transcripts-macos-arm64-v<VERSION>.zip (uploader, extension assets,
#       Install.command, Uninstall.command, host template, INSTALL.txt, release.json)
#   release.json
#   SHA256SUMS
#
# <VERSION> comes from --version or package.json. The public App client ID and
# installation URL are release metadata (distributable); no secret, private
# key, token, or signing credential is ever read or embedded. Fails closed on
# non-Darwin/non-arm64 hosts (the uploader target), missing toolchains,
# version mismatches, or secret patterns in staged files.

set -eu

fail() {
    printf 'package-release: %s\n' "$1" >&2
    exit 1
}

usage() {
    printf 'usage: %s --app-client-id ID --app-install-url URL [--version VERSION] [--out-dir DIR]\n' "$0" >&2
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)

app_client_id=
app_install_url=
version=
out_dir=$repo_root/dist/release

while [ "$#" -gt 0 ]; do
    case "$1" in
        --app-client-id)
            [ "$#" -ge 2 ] || fail "--app-client-id requires an argument"
            app_client_id=$2
            shift 2
            ;;
        --app-install-url)
            [ "$#" -ge 2 ] || fail "--app-install-url requires an argument"
            app_install_url=$2
            shift 2
            ;;
        --version)
            [ "$#" -ge 2 ] || fail "--version requires an argument"
            version=$2
            shift 2
            ;;
        --out-dir)
            [ "$#" -ge 2 ] || fail "--out-dir requires an argument"
            out_dir=$2
            shift 2
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

[ -n "$app_client_id" ] || fail "missing required --app-client-id (verified public GitHub App client ID)"
[ -n "$app_install_url" ] || fail "missing required --app-install-url (verified public GitHub App installation URL)"

# Client ID: same shape as config.ValidateClientID (public metadata, never a secret).
case "$app_client_id" in
    *[!A-Za-z0-9._-]*|"") fail "invalid --app-client-id: use only [A-Za-z0-9._-]" ;;
esac
if [ "$(printf '%s' "$app_client_id" | wc -c | tr -d ' ')" -lt 8 ] || [ "$(printf '%s' "$app_client_id" | wc -c | tr -d ' ')" -gt 128 ]; then
    fail "invalid --app-client-id: expected 8-128 characters"
fi
lower_client=$(printf '%s' "$app_client_id" | tr '[:upper:]' '[:lower:]')
case "$lower_client" in
    *replace*|*placeholder*|*changeme*|*your_*|*example*) fail "--app-client-id looks like a placeholder" ;;
esac

# Install URL: https github.com only, no credentials, no fragment.
case "$app_install_url" in
    https://github.com/*) : ;;
    *) fail "invalid --app-install-url: must start with https://github.com/" ;;
esac
case "$app_install_url" in
    *@*|*#*) fail "invalid --app-install-url: must not contain credentials or fragments" ;;
esac

pkg_version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$repo_root/package.json" | head -n 1)
[ -n "$pkg_version" ] || fail "could not read a version from package.json"
if [ -z "$version" ]; then
    version=$pkg_version
fi
[ "$version" = "$pkg_version" ] || fail "version '$version' does not match package.json '$pkg_version'; bump package.json first so the extension manifest, uploader, and release stay in lockstep"
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

[ "$(uname -s)" = "Darwin" ] || fail "unsupported platform $(uname -s): releases build on macOS"
[ "$(uname -m)" = "arm64" ] || fail "unsupported architecture $(uname -m): releases target Apple silicon"

command -v node >/dev/null 2>&1 || fail "node not found: install Node.js 22 LTS"
command -v npm >/dev/null 2>&1 || fail "npm not found"
command -v go >/dev/null 2>&1 || fail "go not found: install Go 1.24.x"
command -v zip >/dev/null 2>&1 || fail "zip not found"
command -v shasum >/dev/null 2>&1 || fail "shasum not found"
command -v python3 >/dev/null 2>&1 || fail "python3 not found: required to validate JSON"

# Build both components from this tree so versions always match.
printf 'package-release: building extension (version %s)…\n' "$version"
(cd "$repo_root" && npm run build >/dev/null) || fail "extension build failed"

printf 'package-release: building uploader (version %s)…\n' "$version"
sh "$repo_root/scripts/build-uploader.sh" --version "$version" >/dev/null || fail "uploader build failed"

ext_manifest=$repo_root/dist/extension/manifest.json
[ -f "$ext_manifest" ] || fail "built extension manifest missing: $ext_manifest"
manifest_version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$ext_manifest" | head -n 1)
[ "$manifest_version" = "$version" ] || fail "extension manifest version '$manifest_version' != release version '$version'"

uploader_bin=$repo_root/dist/native/lecture-uploader
[ -x "$uploader_bin" ] || fail "built uploader missing: $uploader_bin"
uploader_version=$("$uploader_bin" --version 2>/dev/null | tr -d '\r\n')
[ "$uploader_version" = "$version" ] || fail "uploader version '$uploader_version' != release version '$version'"

# Protocol compatibility: extension, Go, and schema must all be 1.
ts_proto=$(grep -o 'PROTOCOL_VERSION = 1' "$repo_root/extension/src/native-messaging.ts" | head -n 1)
[ "$ts_proto" = "PROTOCOL_VERSION = 1" ] || fail "extension PROTOCOL_VERSION is not 1"
go_proto=$(grep -o 'ProtocolVersion[[:space:]]*= 1' "$repo_root/uploader/internal/protocol/job.go" | head -n 1)
[ -n "$go_proto" ] || fail "uploader ProtocolVersion is not 1"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["$defs"]["protocolVersion"]["const"] == 1, "schema protocolVersion != 1"' "$repo_root/protocol/native-messaging.schema.json" \
    || fail "protocol schema const is not 1"
protocol_version=1

# Stage the bundle contents.
work=$(mktemp -d "${TMPDIR:-/tmp}/lte-release.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
mac_root=$work/macos-arm64
mkdir -p "$mac_root/extension" "$out_dir"

cp "$uploader_bin" "$mac_root/lecture-uploader"
chmod 0700 "$mac_root/lecture-uploader"
cp -R "$repo_root/dist/extension/." "$mac_root/extension/"
chmod -R u=rwX,go= "$mac_root/extension"
cp "$repo_root/scripts/Install.command" "$mac_root/Install.command"
cp "$repo_root/scripts/Uninstall.command" "$mac_root/Uninstall.command"
chmod 0755 "$mac_root/Install.command" "$mac_root/Uninstall.command"
cp "$repo_root/native-host/com.neelbangera.lecturetranscripts.json.in" "$mac_root/"
cp "$repo_root/scripts/INSTALL.txt" "$mac_root/INSTALL.txt"

# No secrets or machine-local state may enter the bundle.
if grep -rIlE 'ghu_[A-Za-z0-9]+|ghr_[A-Za-z0-9]+|BEGIN [A-Z ]*PRIVATE KEY|github\.com/login/oauth/access_token.*secret' "$mac_root" 2>/dev/null | grep -q .; then
    fail "bundle contains secret-like material; refusing to package"
fi
if [ -e "$mac_root/extension/config.json" ] || [ -e "$mac_root/config.json" ]; then
    fail "bundle must not contain machine-local config"
fi
if grep -q '{{' "$mac_root/com.neelbangera.lecturetranscripts.json.in"; then
    : # template placeholders are expected in the .in file
else
    fail "host template lost its placeholders"
fi
python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "$mac_root/extension/manifest.json" \
    || fail "staged extension manifest is not valid JSON"

ext_asset="lecture-transcripts-extension-v$version.zip"
mac_asset="lecture-transcripts-macos-arm64-v$version.zip"

# Extension zip: compiled extension only, top-level files.
(cd "$repo_root/dist/extension" && zip -q -r -X "$work/$ext_asset" . -x '*.DS_Store*') \
    || fail "extension zip failed"

# Release metadata (public distributable values only).
release_json=$work/release.json
python3 - "$release_json" "$version" "$protocol_version" "$ext_asset" "$mac_asset" "$app_client_id" "$app_install_url" <<'EOF'
import json, sys
path, version, proto, ext_asset, mac_asset, client_id, install_url = sys.argv[1:8]
doc = {
    "productVersion": version,
    "protocolVersion": int(proto),
    "platform": "darwin",
    "arch": "arm64",
    "extensionAsset": ext_asset,
    "macAsset": mac_asset,
    "appClientId": client_id,
    "appInstallUrl": install_url,
    "minChrome": "desktop",
    "notes": "Unsigned D2 bundle: Gatekeeper may gate the binary/.command on first open. Do not disable Gatekeeper; signing/notarization is D3.",
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
EOF
cp "$release_json" "$mac_root/release.json"

(cd "$mac_root" && zip -q -r -X "$work/$mac_asset" . -x '*.DS_Store*') \
    || fail "mac zip failed"

mkdir -p "$out_dir"
cp "$work/$ext_asset" "$out_dir/$ext_asset"
cp "$work/$mac_asset" "$out_dir/$mac_asset"
cp "$work/release.json" "$out_dir/release.json"
(cd "$out_dir" && shasum -a 256 "$ext_asset" "$mac_asset" release.json > SHA256SUMS) \
    || fail "checksum generation failed"

trap - EXIT HUP INT TERM
rm -rf "$work"

printf 'package-release: version=%s protocol=%s\n' "$version" "$protocol_version"
printf 'package-release: wrote %s/%s\n' "$out_dir" "$ext_asset"
printf 'package-release: wrote %s/%s\n' "$out_dir" "$mac_asset"
printf 'package-release: wrote %s/release.json + SHA256SUMS\n' "$out_dir"
