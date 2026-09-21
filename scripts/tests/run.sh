#!/bin/sh
# Packaging self-tests for the native-host installer/uninstaller and the
# uploader build script. Run with:
#
#   sh scripts/tests/run.sh
#
# The tests use a temporary HOME and a stubbed "security" command, so they
# never touch the real home directory or the real Keychain. No test framework
# is required.

set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/../.." && pwd)
install_script=$repo_root/scripts/install-native-host.sh
uninstall_script=$repo_root/scripts/uninstall-native-host.sh
build_script=$repo_root/scripts/build-uploader.sh
template=$repo_root/native-host/com.neelbangera.lecturetranscripts.json.in

host_name=com.neelbangera.lecturetranscripts
valid_id=abcdefghijklmnopabcdefghijklmnop
other_id=ponmlkjihgfedcbaponmlkjihgfedcba

work=$(mktemp -d "${TMPDIR:-/tmp}/lte-packaging-tests.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

fake_home=$work/home
stub_bin=$work/stub-bin
mkdir -p "$fake_home" "$stub_bin"

fake_binary=$work/lecture-uploader
cat > "$fake_binary" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$fake_binary"

security_log=$work/security.log
cat > "$stub_bin/security" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$security_log"
exit 1
EOF
chmod 0755 "$stub_bin/security"

manifest_dir="$fake_home/Library/Application Support/Google/Chrome/NativeMessagingHosts"
manifest=$manifest_dir/$host_name.json
data_dir="$fake_home/Library/Application Support/LectureTranscripts"

passed=0
failed=0
last_output=$work/last-output

pass() {
    passed=$((passed + 1))
    printf 'ok - %s\n' "$1"
}

fail() {
    failed=$((failed + 1))
    printf 'not ok - %s\n' "$1"
}

run() {
    set +e
    "$@" >"$last_output" 2>&1
    status=$?
    set -e
}

expect_status() {
    expected=$1
    label=$2
    shift 2
    run "$@"
    if [ "$expected" = "ok" ]; then
        if [ "$status" -eq 0 ]; then
            pass "$label"
        else
            fail "$label (expected success, got status $status)"
            sed 's/^/    /' "$last_output"
        fi
    else
        if [ "$status" -ne 0 ]; then
            pass "$label"
        else
            fail "$label (expected failure, got success)"
            sed 's/^/    /' "$last_output"
        fi
    fi
}

assert_exists() {
    if [ -e "$1" ]; then pass "$2"; else fail "$2 (missing $1)"; fi
}

assert_absent() {
    if [ ! -e "$1" ]; then pass "$2"; else fail "$2 (still present: $1)"; fi
}

assert_eq() {
    if [ "$1" = "$2" ]; then pass "$3"; else fail "$3 (expected [$2], got [$1])"; fi
}

assert_mode() {
    mode=$(stat -f '%Lp' "$1" 2>/dev/null || printf 'unknown')
    if [ "$mode" = "$2" ]; then pass "$3"; else fail "$3 (mode $mode, expected $2)"; fi
}

assert_output_has() {
    if grep -Fq "$2" "$last_output"; then pass "$1"; else fail "$1 (output does not contain: $2)"; fi
}

assert_file_has() {
    if grep -Fq "$2" "$1"; then pass "$3"; else fail "$3 (file $1 does not contain: $2)"; fi
}

count_json_files() {
    if [ -d "$manifest_dir" ]; then
        find "$manifest_dir" -maxdepth 1 -name '*.json' -type f | wc -l | tr -d ' '
    else
        printf '0'
    fi
}

printf '# template checks\n'
assert_file_has "$template" '{{BINARY_PATH}}' "template keeps the BINARY_PATH placeholder"
assert_file_has "$template" '{{EXTENSION_ID}}' "template keeps the EXTENSION_ID placeholder"
assert_file_has "$template" '"name": "com.neelbangera.lecturetranscripts"' "template has the exact host name"
assert_file_has "$template" '"type": "stdio"' "template has the exact host type"
if grep -Eq 'chrome-extension://[a-p]{32}' "$template"; then
    fail "template contains a real extension ID"
else
    pass "template contains no real extension ID"
fi

printf '# install argument validation\n'
expect_status fail "install rejects a missing extension ID" env HOME="$fake_home" sh "$install_script" --binary "$fake_binary"
expect_status fail "install rejects a malformed extension ID" env HOME="$fake_home" sh "$install_script" not-an-extension-id --binary "$fake_binary"
expect_status fail "install rejects a wildcard extension ID" env HOME="$fake_home" sh "$install_script" '*' --binary "$fake_binary"
expect_status fail "install rejects an uppercase extension ID" env HOME="$fake_home" sh "$install_script" ABCDEFGHIJKLMNOPABCDEFGHIJKLMNOP --binary "$fake_binary"
expect_status fail "install rejects non-a-p characters in an extension ID" env HOME="$fake_home" sh "$install_script" zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz --binary "$fake_binary"
expect_status fail "install rejects a 33-character extension ID" env HOME="$fake_home" sh "$install_script" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --binary "$fake_binary"
expect_status fail "install rejects a relative --binary path" env HOME="$fake_home" sh "$install_script" "$valid_id" --binary relative/path
expect_status fail "install rejects a missing uploader binary" env HOME="$fake_home" sh "$install_script" "$valid_id" --binary "$work/does-not-exist"
assert_absent "$manifest" "no manifest is written by rejected installs"

printf '# install rendering\n'
expect_status ok "install accepts a valid extension ID" env HOME="$fake_home" sh "$install_script" "$valid_id" --binary "$fake_binary"
assert_exists "$manifest" "manifest is rendered to the Chrome NativeMessagingHosts path"
assert_mode "$manifest" 600 "manifest mode is 0600"
assert_file_has "$manifest" "chrome-extension://$valid_id/" "rendered manifest contains the exact origin"
assert_file_has "$manifest" "\"path\": \"$fake_binary\"" "rendered manifest contains the absolute uploader path"
if grep -q '{{' "$manifest"; then fail "no template placeholders remain"; else pass "no template placeholders remain"; fi
origin_count=$(grep -c 'chrome-extension://' "$manifest" || true)
assert_eq "1" "$origin_count" "rendered manifest contains exactly one allowed origin"
if command -v python3 >/dev/null 2>&1; then
    if python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "$manifest"; then
        pass "rendered manifest is valid JSON"
    else
        fail "rendered manifest is valid JSON"
    fi
fi
assert_eq "1" "$(count_json_files)" "exactly one registration file exists"

printf '# idempotent reinstall\n'
cp "$manifest" "$work/manifest.first"
expect_status ok "reinstall with the same ID succeeds" env HOME="$fake_home" sh "$install_script" "$valid_id" --binary "$fake_binary"
assert_eq "1" "$(count_json_files)" "reinstall does not create a duplicate registration"
if cmp -s "$work/manifest.first" "$manifest"; then
    pass "reinstall leaves identical content"
else
    fail "reinstall leaves identical content"
fi
expect_status ok "reinstall with a different ID replaces the registration" env HOME="$fake_home" sh "$install_script" "$other_id" --binary "$fake_binary"
assert_eq "1" "$(count_json_files)" "ID change still leaves exactly one registration"
assert_file_has "$manifest" "chrome-extension://$other_id/" "ID change installs the new origin"
if grep -Fq "chrome-extension://$valid_id/" "$manifest"; then
    fail "ID change removes the old origin"
else
    pass "ID change removes the old origin"
fi

printf '# uninstall gating\n'
mkdir -p "$data_dir"
printf 'fake queue\n' > "$data_dir/queue.sqlite3"
printf 'fake wal\n' > "$data_dir/queue.sqlite3-wal"
printf 'fake lock\n' > "$data_dir/queue.lock"
printf '{}\n' > "$data_dir/config.json"
expect_status ok "uninstall without --reset succeeds" env HOME="$fake_home" PATH="$stub_bin:$PATH" sh "$uninstall_script" --binary "$fake_binary"
assert_absent "$manifest" "default uninstall removes the rendered manifest"
assert_absent "$fake_binary" "default uninstall removes the built uploader"
assert_exists "$data_dir/queue.sqlite3" "default uninstall keeps the queue database"
assert_exists "$data_dir/config.json" "default uninstall keeps the machine-local config"
assert_exists "$data_dir/queue.lock" "default uninstall keeps the queue lock"
if [ -s "$security_log" ]; then
    fail "default uninstall never calls security"
else
    pass "default uninstall never calls security"
fi
expect_status ok "uninstall is idempotent when nothing is installed" env HOME="$fake_home" PATH="$stub_bin:$PATH" sh "$uninstall_script" --binary "$fake_binary"

printf '# reset gating\n'
cat > "$fake_binary" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$fake_binary"
expect_status ok "reinstall before --reset succeeds" env HOME="$fake_home" sh "$install_script" "$valid_id" --binary "$fake_binary"
expect_status ok "uninstall --reset succeeds" env HOME="$fake_home" PATH="$stub_bin:$PATH" sh "$uninstall_script" --reset --binary "$fake_binary"
assert_output_has "--reset announces the queue database path" "$data_dir/queue.sqlite3"
assert_output_has "--reset announces the config path" "$data_dir/config.json"
assert_output_has "--reset announces Keychain removal" "Keychain"
if awk '/will remove/{announced=NR} /reset removed/{removed=NR} END{exit !(announced && removed && announced < removed)}' "$last_output"; then
    pass "--reset announces what it will remove before removing it"
else
    fail "--reset announces what it will remove before removing it"
fi
assert_absent "$manifest" "--reset removes the manifest"
assert_absent "$fake_binary" "--reset removes the built uploader"
assert_absent "$data_dir/queue.sqlite3" "--reset removes the queue database"
assert_absent "$data_dir/queue.sqlite3-wal" "--reset removes the queue WAL"
assert_absent "$data_dir/config.json" "--reset removes the machine-local config"
assert_absent "$data_dir/queue.lock" "--reset removes the queue lock"
assert_exists "$repo_root" "--reset never removes the repository"
if [ -s "$security_log" ]; then
    pass "--reset calls security to remove Keychain records"
else
    fail "--reset calls security to remove Keychain records"
fi

printf '# build-uploader argument validation\n'
expect_status fail "build rejects invalid version characters" sh "$build_script" --version 'bad version!'
expect_status fail "build rejects an overlong version" sh "$build_script" --version aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
expected_version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$repo_root/package.json" | head -n 1)
expect_status ok "build --dry-run succeeds" sh "$build_script" --dry-run
assert_output_has "build dry-run reports the package.json version" "version=$expected_version"
assert_output_has "build dry-run reports the dist/native output path" "dist/native/lecture-uploader"
expect_status ok "build --dry-run accepts an explicit version" sh "$build_script" --dry-run --version 9.9.9
assert_output_has "build dry-run reports the explicit version" "version=9.9.9"

printf '\n%s passed, %s failed\n' "$passed" "$failed"
[ "$failed" -eq 0 ] || exit 1
