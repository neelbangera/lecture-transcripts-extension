#!/bin/sh
# Lecture Transcripts Mac uninstaller (D2 unsigned bundle entry point).
#
# Double-click in Finder, or run in Terminal:
#   ./Uninstall.command [--reset]
#
# Default: removes the installed executable, staged extension, and Chrome host
# registration. Preserves config, queue, credentials, and logs.
# --reset (or answering yes when prompted): additionally removes local queue,
# config, and Keychain records after announcing them. Never deletes GitHub
# files or repositories. Remove the unpacked extension from
# chrome://extensions manually.

set -eu

fail() {
    printf 'uninstall: %s\n' "$1" >&2
    exit 1
}

host_name=com.neelbangera.lecturetranscripts
keychain_service=com.neelbangera.lecturetranscripts
token_account=github-app-user-token
device_flow_account=github-app-device-transaction

app_dir="$HOME/Library/Application Support/LectureTranscripts"
stable_bin=$app_dir/bin/lecture-uploader
stable_ext=$app_dir/extension
manifest_dir="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
manifest=$manifest_dir/$host_name.json
queue_db=$app_dir/queue.sqlite3
queue_lock=$app_dir/queue.lock
config_path=$app_dir/config.json

reset=no
for arg in "$@"; do
    case "$arg" in
        --reset) reset=yes ;;
        --help|-h) printf 'usage: %s [--reset]\n' "$0" >&2; exit 0 ;;
        *) fail "unknown argument: $arg" ;;
    esac
done

if [ "$reset" != "yes" ]; then
    printf 'Remove installed files but keep config, queue, credentials, and logs? [Y/n] '
    # shellcheck disable=SC2162
    if [ -t 0 ]; then read -r answer || answer=; else read -r answer < /dev/tty || answer=; fi
    case "$answer" in
        ""|[Yy]|[Yy]es) : ;;
        *) printf 'uninstall: canceled\n'; exit 0 ;;
    esac
    printf 'Remove local queue/config/credentials too (reset)? [y/N] '
    # shellcheck disable=SC2162
    if [ -t 0 ]; then read -r reset_answer || reset_answer=; else read -r reset_answer < /dev/tty || reset_answer=; fi
    case "$reset_answer" in
        [Yy]|[Yy]es) reset=yes ;;
    esac
fi

if [ "$reset" = "yes" ]; then
    cat <<EOF
uninstall: --reset will remove, in addition to the executable/extension/manifest:
  - $queue_db (plus -wal and -shm sidecars)
  - $queue_lock
  - $config_path
  - Keychain generic-password records for service "$keychain_service"
It will not remove the repository, the remote GitHub repository, or logs under $HOME/Library/Logs/LectureTranscripts.
EOF
fi

if [ -f "$manifest" ]; then
    rm -f "$manifest"
    printf 'uninstall: removed %s\n' "$manifest"
elif [ -e "$manifest" ]; then
    fail "$manifest exists and is not a regular file; refusing to remove it"
else
    printf 'uninstall: no host manifest at %s\n' "$manifest"
fi

if [ -f "$stable_bin" ]; then
    rm -f "$stable_bin"
    printf 'uninstall: removed %s\n' "$stable_bin"
    rmdir "$app_dir/bin" 2>/dev/null || true
else
    printf 'uninstall: no installed uploader at %s\n' "$stable_bin"
fi

if [ -d "$stable_ext" ]; then
    rm -rf "$stable_ext"
    printf 'uninstall: removed %s\n' "$stable_ext"
else
    printf 'uninstall: no staged extension at %s\n' "$stable_ext"
fi

if [ "$reset" = "yes" ]; then
    for path in "$queue_db" "$queue_db-wal" "$queue_db-shm" "$queue_lock" "$config_path"; do
        if [ -f "$path" ]; then
            rm -f "$path"
            printf 'uninstall: reset removed %s\n' "$path"
        fi
    done
    if command -v security >/dev/null 2>&1; then
        security delete-generic-password -s "$keychain_service" -a "$token_account" >/dev/null 2>&1 || true
        security delete-generic-password -s "$keychain_service" -a "$device_flow_account" >/dev/null 2>&1 || true
        attempts=0
        while [ "$attempts" -lt 10 ] && security delete-generic-password -s "$keychain_service" >/dev/null 2>&1; do
            attempts=$((attempts + 1))
        done
        printf 'uninstall: reset removed Keychain records for service %s\n' "$keychain_service"
    else
        printf 'uninstall: security tool not found; Keychain records for %s were not removed\n' "$keychain_service" >&2
    fi
fi

cat <<EOF
uninstall: done.
Remove the unpacked extension from chrome://extensions (click Remove) if you
no longer want it loaded. Your GitHub repository files are untouched.
EOF
