#!/bin/sh
# Lecture Transcripts Mac installer (D2 unsigned bundle entry point).
#
# Double-click in Finder, or run in Terminal:
#   ./Install.command [--app-client-id ID] [--app-install-url URL] [--repository OWNER/REPO]
#   ./Install.command --extension-id <32-char-ID> [--app-client-id ID ...]
#
# Stages the uploader + extension to stable per-user paths, validates the
# loaded unpacked extension ID, runs `lecture-uploader --setup`, and renders
# the exact Chrome Native Messaging host registration only after setup
# succeeds. Preserves config/credentials/queue on update; rolls back staged
# files if setup is canceled or fails. No sudo, no developer toolchain.
#
# Host-registration rules mirror scripts/install-native-host.sh (single exact
# origin, mode 0600, no wildcards); that script remains the canonical source
# workflow and this file points at it in comments.

set -eu

fail() {
    printf 'install: %s\n' "$1" >&2
    exit 1
}

bundle_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)

host_name=com.neelbangera.lecturetranscripts
manifest_name=$host_name.json
app_dir="$HOME/Library/Application Support/LectureTranscripts"
stable_bin=$app_dir/bin/lecture-uploader
stable_ext=$app_dir/extension
manifest_dir="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
manifest=$manifest_dir/$manifest_name
template=$bundle_dir/com.neelbangera.lecturetranscripts.json.in

app_client_id=
app_install_url=
repository_arg=
extension_id_arg=

while [ "$#" -gt 0 ]; do
    case "$1" in
        --app-client-id) [ "$#" -ge 2 ] || fail "--app-client-id requires an argument"; app_client_id=$2; shift 2 ;;
        --app-install-url) [ "$#" -ge 2 ] || fail "--app-install-url requires an argument"; app_install_url=$2; shift 2 ;;
        --repository) [ "$#" -ge 2 ] || fail "--repository requires an argument"; repository_arg=$2; shift 2 ;;
        --extension-id) [ "$#" -ge 2 ] || fail "--extension-id requires an argument"; extension_id_arg=$2; shift 2 ;;
        --help|-h) printf 'usage: %s [--app-client-id ID] [--app-install-url URL] [--repository OWNER/REPO] [--extension-id ID]\n' "$0" >&2; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done

[ "$(uname -s)" = "Darwin" ] || fail "unsupported platform $(uname -s): the installer targets macOS"
[ "$(uname -m)" = "arm64" ] || fail "unsupported architecture $(uname -m): the installer targets Apple silicon"

# 1. Bundle integrity + component compatibility before making changes.
[ -f "$bundle_dir/lecture-uploader" ] || fail "bundle is incomplete: lecture-uploader missing in $bundle_dir"
[ -x "$bundle_dir/lecture-uploader" ] || fail "bundle uploader is not executable"
[ -f "$bundle_dir/extension/manifest.json" ] || fail "bundle is incomplete: extension/manifest.json missing"
[ -f "$template" ] || fail "bundle is incomplete: host template missing"
[ -f "$bundle_dir/release.json" ] || fail "bundle is incomplete: release.json missing"
if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "$bundle_dir/extension/manifest.json" \
        || fail "bundled extension manifest is not valid JSON"
    bundle_version=$(python3 -c 'import json; print(json.load(open("'"$bundle_dir"'/release.json"))["productVersion"])')
    bundle_proto=$(python3 -c 'import json; print(json.load(open("'"$bundle_dir"'/release.json"))["protocolVersion"])')
    manifest_version=$(python3 -c 'import json; print(json.load(open("'"$bundle_dir"'/extension/manifest.json"))["version"])')
    [ "$bundle_version" = "$manifest_version" ] || fail "bundle version mismatch: release.json $bundle_version != manifest $manifest_version"
    [ "$bundle_proto" = "1" ] || fail "bundle protocolVersion $bundle_proto is not 1"
    bin_version=$("$bundle_dir/lecture-uploader" --version 2>/dev/null | tr -d '\r\n')
    [ "$bin_version" = "$bundle_version" ] || fail "bundled uploader version '$bin_version' != release version '$bundle_version'"
    if [ -z "$app_client_id" ]; then
        app_client_id=$(python3 -c 'import json; print(json.load(open("'"$bundle_dir"'/release.json")).get("appClientId",""))')
    fi
    if [ -z "$app_install_url" ]; then
        app_install_url=$(python3 -c 'import json; print(json.load(open("'"$bundle_dir"'/release.json")).get("appInstallUrl",""))')
    fi
else
    fail "python3 is required to verify the bundle"
fi
[ -n "$app_client_id" ] || fail "public App client ID is missing (release.json + --app-client-id are both empty)"

# 2. Ask the user to close Chrome so an active host cannot race setup/update.
if pgrep -x "Google Chrome" >/dev/null 2>&1 || pgrep -f "lecture-uploader" >/dev/null 2>&1; then
    printf 'Please fully quit Chrome now (Chrome > Quit), then press Enter to continue.\n'
    # shellcheck disable=SC2162
    read -r _line < /dev/tty || fail "Chrome must be closed before installing"
    if pgrep -x "Google Chrome" >/dev/null 2>&1 || pgrep -f "lecture-uploader" >/dev/null 2>&1; then
        fail "Chrome or a lecture-uploader host is still running; quit Chrome and rerun"
    fi
fi

# 3. Stage uploader + extension, keeping the previous install for rollback.
umask 077
mkdir -p "$app_dir/bin"
chmod 0700 "$app_dir" "$app_dir/bin"

backup_suffix=".pre-$(date +%Y%m%dT%H%M%S)"
rollback_bin=
rollback_ext=
rollback_manifest=
rollback_done=no
activated=no
staged_this_run=no
rollback() {
    # Restores the previous install when setup fails or the run is
    # interrupted before activation. Safe to call when nothing was staged
    # (every path is guarded by staged_this_run or an existence check).
    [ "$rollback_done" = "yes" ] && return 0
    [ "$activated" = "yes" ] && return 0
    rollback_done=yes
    rm -rf "$app_dir/bin/.lecture-uploader.new" "$app_dir/.extension.new" 2>/dev/null || true
    if [ "$staged_this_run" = "yes" ]; then
        if [ -n "$rollback_bin" ] && [ -f "$rollback_bin" ]; then
            mv -f "$rollback_bin" "$stable_bin" 2>/dev/null || true
        else
            # Fresh install with no previous binary: remove what this run staged.
            rm -f "$stable_bin" 2>/dev/null || true
        fi
        if [ -n "$rollback_ext" ] && [ -d "$rollback_ext" ]; then
            rm -rf "$stable_ext" 2>/dev/null || true
            mv -f "$rollback_ext" "$stable_ext" 2>/dev/null || true
        elif [ -z "$rollback_ext" ]; then
            rm -rf "$stable_ext" 2>/dev/null || true
        fi
    fi
    if [ -n "$rollback_manifest" ] && [ -f "$rollback_manifest" ]; then
        mkdir -p "$manifest_dir"
        mv -f "$rollback_manifest" "$manifest" 2>/dev/null || true
    fi
}
trap 'rollback; printf "install: interrupted; staged files were not activated\n" >&2; exit 1' HUP INT TERM

stage_binary() {
    tmp_new=$app_dir/bin/.lecture-uploader.new
    cp "$bundle_dir/lecture-uploader" "$tmp_new"
    chmod 0700 "$tmp_new"
    [ -x "$tmp_new" ] || fail "staged uploader is not executable"
    if [ -f "$stable_bin" ]; then
        rollback_bin=$stable_bin$backup_suffix
        mv -f "$stable_bin" "$rollback_bin"
    fi
    mv -f "$tmp_new" "$stable_bin"
    chmod 0700 "$stable_bin"
}

stage_extension() {
    tmp_new=$app_dir/.extension.new
    rm -rf "$tmp_new"
    mkdir -p "$tmp_new"
    cp -R "$bundle_dir/extension/." "$tmp_new/"
    chmod -R u=rwX,go= "$tmp_new"
    [ -f "$tmp_new/manifest.json" ] || fail "staged extension is incomplete"
    if [ -d "$stable_ext" ]; then
        rollback_ext=$stable_ext$backup_suffix
        rm -rf "$rollback_ext"
        mv -f "$stable_ext" "$rollback_ext"
    fi
    mv -f "$tmp_new" "$stable_ext"
}

stage_binary
stage_extension
staged_this_run=yes

printf 'Staged uploader to %s\n' "$stable_bin"
printf 'Staged extension to %s\n' "$stable_ext"
printf 'Load the unpacked extension from that directory in chrome://extensions (Developer mode > Load unpacked).\n'

# 4. Extension ID: flag or interactive prompt, validated like install-native-host.sh.
extension_id=$extension_id_arg
if [ -z "$extension_id" ]; then
    printf 'Paste the 32-character extension ID shown on its chrome://extensions card: '
    # shellcheck disable=SC2162
    if [ -t 0 ]; then read -r extension_id; else read -r extension_id < /dev/tty || extension_id=; fi
fi
case "$extension_id" in
    *[!a-p]*) rollback; fail "invalid extension ID: only the characters a-p are allowed" ;;
esac
if [ "${#extension_id}" -ne 32 ]; then
    rollback
    fail "invalid extension ID: expected exactly 32 characters, got ${#extension_id}"
fi
origin=chrome-extension://$extension_id/

# Chrome is needed only to load the extension and reveal its ID; close it again
# before setup commits so the host cannot race the update.
printf 'If Chrome is open for loading the extension, fully quit it again before setup continues.\n'
printf 'Press Enter once Chrome is fully quit: '
# shellcheck disable=SC2162
read -r _line < /dev/tty || fail "Chrome must be closed before setup"
if pgrep -x "Google Chrome" >/dev/null 2>&1 || pgrep -f "lecture-uploader" >/dev/null 2>&1; then
    rollback
    fail "Chrome or a lecture-uploader host is still running"
fi

# 5. Repository input (flag or prompt; strict parsing lives in the uploader).
repository=$repository_arg
if [ -z "$repository" ]; then
    printf 'Repository (OWNER/REPO or https://github.com/OWNER/REPO): '
    # shellcheck disable=SC2162
    if [ -t 0 ]; then read -r repository; else read -r repository < /dev/tty || repository=; fi
fi
[ -n "$repository" ] || { rollback; fail "repository is required"; }

if [ -n "$app_install_url" ]; then
    printf 'Install the App on only that repository first (or approve when asked):\n  %s\n' "$app_install_url"
    if command -v open >/dev/null 2>&1; then
        printf 'Opening the App installation page…\n'
        open "$app_install_url" 2>/dev/null || true
    fi
fi

# 6. Guided device authorization + verified config. Only a complete verified
# config is written; a cancel/failure rolls back staged files and never leaves
# a partial install active.
if [ -n "$app_install_url" ]; then
    setup_ok=yes
    "$stable_bin" --setup --app-client-id "$app_client_id" --app-install-url "$app_install_url" --repository "$repository" < /dev/tty || setup_ok=no
else
    setup_ok=yes
    "$stable_bin" --setup --app-client-id "$app_client_id" --repository "$repository" < /dev/tty || setup_ok=no
fi
if [ "$setup_ok" != "yes" ]; then
    rollback
    fail "setup did not complete; previous installation was restored. Rerun Install.command to retry"
fi

# 7. Commit the exact host registration only after setup succeeded.
[ "$(grep -c '{{BINARY_PATH}}' "$template" || true)" -eq 1 ] || { rollback; fail "host template must contain exactly one {{BINARY_PATH}} placeholder"; }
[ "$(grep -c '{{EXTENSION_ID}}' "$template" || true)" -eq 1 ] || { rollback; fail "host template must contain exactly one {{EXTENSION_ID}} placeholder"; }
mkdir -p "$manifest_dir"
tmp_manifest=$(mktemp "$manifest_dir/.$manifest_name.XXXXXX")
trap 'rm -f "$tmp_manifest"' EXIT
escape_sed_replacement() { printf '%s' "$1" | sed 's/[\\&|]/\\&/g'; }
escaped_binary=$(escape_sed_replacement "$stable_bin")
escaped_id=$(escape_sed_replacement "$extension_id")
sed -e "s|{{BINARY_PATH}}|$escaped_binary|g" -e "s|{{EXTENSION_ID}}|$escaped_id|g" "$template" > "$tmp_manifest"
chmod 0600 "$tmp_manifest"
grep -Fq "\"$origin\"" "$tmp_manifest" || { rollback; fail "rendered manifest does not contain the exact origin $origin"; }
origin_count=$(grep -c 'chrome-extension://' "$tmp_manifest" || true)
[ "$origin_count" -eq 1 ] || { rollback; fail "rendered manifest must contain exactly one allowed origin"; }
python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "$tmp_manifest" \
    || { rollback; fail "rendered manifest is not valid JSON"; }
if [ -f "$manifest" ]; then
    if cmp -s "$tmp_manifest" "$manifest"; then
        chmod 0600 "$manifest"
    else
        rollback_manifest=$manifest$backup_suffix
        cp -p "$manifest" "$rollback_manifest"
        mv -f "$tmp_manifest" "$manifest"
        chmod 0600 "$manifest"
    fi
else
    mv -f "$tmp_manifest" "$manifest"
    chmod 0600 "$manifest"
fi
trap - EXIT
trap - HUP INT TERM

# Activation succeeded: drop backups.
activated=yes
[ -n "$rollback_bin" ] && rm -f "$rollback_bin"
[ -n "$rollback_ext" ] && rm -rf "$rollback_ext"
[ -n "$rollback_manifest" ] && rm -f "$rollback_manifest"

cat <<EOF

Installed:
  uploader:  $stable_bin
  extension: $stable_ext
  host:      $manifest
  origin:    $origin

Next:
  1. Restart Chrome.
  2. Open the Lecture Transcripts extension and confirm GitHub connected.
  3. Capture a permitted Leccap lecture and check the Markdown in your repository.

If anything fails, rerun Install.command to retry; reinstalling the same
version is safe and preserves config, credentials, queue, and extension state.
EOF
