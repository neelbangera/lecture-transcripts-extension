#!/bin/sh
# Remove the files created by the packaging scripts for this project.
#
# Usage:
#   scripts/uninstall-native-host.sh [--reset] [--binary ABSOLUTE_PATH]
#
# Without --reset this removes only:
#   - the rendered Native Messaging host manifest
#   - the built uploader binary (default dist/native/lecture-uploader)
# It never touches the repository, the remote GitHub repository, the queue
# database, the machine-local config, or Keychain records.
#
# With --reset it first prints exactly what it will remove, then additionally
# removes the local queue database (+ -wal/-shm sidecars and queue.lock), the
# machine-local config, and this host's Keychain records.

set -eu

host_name=com.neelbangera.lecturetranscripts
keychain_service=com.neelbangera.lecturetranscripts
token_account=github-app-user-token
device_flow_account=github-device-flow-transaction

fail() {
    printf 'uninstall-native-host: %s\n' "$1" >&2
    exit 1
}

usage() {
    printf 'usage: %s [--reset] [--binary ABSOLUTE_PATH]\n' "$0" >&2
}

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)

manifest_dir="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
manifest=$manifest_dir/$host_name.json
binary_path=$repo_root/dist/native/lecture-uploader
data_dir="$HOME/Library/Application Support/LectureTranscripts"
queue_db=$data_dir/queue.sqlite3
queue_lock=$data_dir/queue.lock
config_path=$data_dir/config.json

reset=no
while [ "$#" -gt 0 ]; do
    case "$1" in
        --reset)
            reset=yes
            shift
            ;;
        --binary)
            [ "$#" -ge 2 ] || fail "--binary requires an absolute path argument"
            binary_path=$2
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

case "$binary_path" in
    /*) : ;;
    *) fail "--binary must be an absolute path: $binary_path" ;;
esac

if [ "$reset" = "yes" ]; then
    cat <<EOF
uninstall-native-host: --reset will remove, in addition to the manifest and built uploader:
  - $queue_db (plus -wal and -shm sidecars)
  - $queue_lock
  - $config_path
  - Keychain generic-password records for service "$keychain_service"
    (accounts "$token_account" and "$device_flow_account", plus any other account for that service)
It will not remove the repository, the remote GitHub repository, or logs under $HOME/Library/Logs/LectureTranscripts.
EOF
fi

if [ -f "$manifest" ]; then
    rm -f "$manifest"
    printf 'uninstall-native-host: removed %s\n' "$manifest"
elif [ -e "$manifest" ]; then
    fail "$manifest exists and is not a regular file; refusing to remove it"
else
    printf 'uninstall-native-host: no rendered manifest at %s\n' "$manifest"
fi

if [ -f "$binary_path" ]; then
    rm -f "$binary_path"
    printf 'uninstall-native-host: removed %s\n' "$binary_path"
else
    printf 'uninstall-native-host: no built uploader at %s\n' "$binary_path"
fi

if [ "$reset" = "yes" ]; then
    for path in "$queue_db" "$queue_db-wal" "$queue_db-shm" "$queue_lock" "$config_path"; do
        if [ -f "$path" ]; then
            rm -f "$path"
            printf 'uninstall-native-host: reset removed %s\n' "$path"
        fi
    done

    if command -v security >/dev/null 2>&1; then
        security delete-generic-password -s "$keychain_service" -a "$token_account" >/dev/null 2>&1 || true
        security delete-generic-password -s "$keychain_service" -a "$device_flow_account" >/dev/null 2>&1 || true
        attempts=0
        while [ "$attempts" -lt 10 ] && security delete-generic-password -s "$keychain_service" >/dev/null 2>&1; do
            attempts=$((attempts + 1))
        done
        printf 'uninstall-native-host: reset removed Keychain records for service %s\n' "$keychain_service"
    else
        printf 'uninstall-native-host: security tool not found; Keychain records for %s were not removed\n' "$keychain_service" >&2
    fi
fi

printf 'uninstall-native-host: done\n'
