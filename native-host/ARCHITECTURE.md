# Native Host Architecture

## Purpose

`native-host/` holds the Chrome Native Messaging host manifest template that
connects the extension to the macOS uploader. The installer substitutes two
variables and writes the rendered manifest into the per-user Chrome
NativeMessagingHosts directory; Chrome then launches the uploader binary and
passes the calling extension's origin as the first argument. This directory
contains no runtime code — only the template and its documentation.

## Boundaries and dependencies

- Template: `native-host/com.neelbangera.lecturetranscripts.json.in`
  (`{{BINARY_PATH}}`, `{{EXTENSION_ID}}`).
- Renderer: [`scripts/install-native-host.sh`](../scripts/install-native-host.sh),
  which validates the inputs, writes
  `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`
  with mode `0600`, and refuses to install a malformed or multi-origin
  manifest.
- Remover: [`scripts/uninstall-native-host.sh`](../scripts/uninstall-native-host.sh).
- Runtime consumer: Chrome launches the binary at the rendered `path`; the Go
  host process validates the Chrome-supplied origin before reading a frame
  (`uploader/cmd/lecture-uploader/main.go` `readAllowedOrigin` +
  `uploader/internal/host/native_messaging.go` `ValidateOrigin`).
- The template is the machine-readable rendering of
  `TECHNICAL_PLAN.md` § Canonical Native Messaging host template; the plan
  wins on disagreement.

## Contracts and invariants

### Template fields (exact)

```json
{
  "name": "com.neelbangera.lecturetranscripts",
  "description": "Lecture Transcripts uploader",
  "path": "{{BINARY_PATH}}",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://{{EXTENSION_ID}}/"
  ]
}
```

| Field | Value / variable | Rule |
| --- | --- | --- |
| `name` | `com.neelbangera.lecturetranscripts` | Fixed; must match the installer's host name, the extension's `NATIVE_HOST_NAME` (`extension/src/native-messaging.ts`), and the uploader's manifest filename expectation (`hostManifestName` in `main.go`) |
| `description` | `Lecture Transcripts uploader` | Fixed display text |
| `path` | `{{BINARY_PATH}}` | Absolute path to the built `dist/native/lecture-uploader` (or `--binary ABSOLUTE_PATH`); must be an executable regular file; may not contain quotes, backslashes, or newlines |
| `type` | `stdio` | Fixed; Chrome Native Messaging stdio transport |
| `allowed_origins` | `["chrome-extension://{{EXTENSION_ID}}/"]` | Exactly one origin, no wildcard, no trailing path, `{{EXTENSION_ID}}` is the 32-character loaded unpacked-extension ID (`a`-`p`) |

### Allowed-origin rule

- The rendered manifest contains exactly one `chrome-extension://` string: the
  exact loaded extension origin. The installer counts occurrences, checks the
  exact string, and refuses a wildcard or placeholder.
- At startup the Go executable reads `allowed_origins` from the installed
  manifest, requires exactly one entry, and validates its shape
  (`chrome-extension://` + 32 `a`-`p` characters + `/`). It then compares the
  origin Chrome supplied as `argv[1]` against that value with an exact string
  comparison. A missing, wildcard, alternate-scheme, path-varied, or
  mismatched origin fails closed before any protocol byte is written.
- The extension ID is never stored in the repository. The installer takes it
  as a required argument; `scripts/tests/run.sh` asserts the committed
  template contains no real ID.

### Install / uninstall flow

```text
scripts/build-uploader.sh
  -> dist/native/lecture-uploader (executable)

scripts/install-native-host.sh <loaded-extension-id> [--binary ABS]
  -> validate ID (32 x a-p), Darwin, absolute executable binary
  -> validate template has exactly one of each placeholder
  -> umask 077, render to mktemp, chmod 0600
  -> verify no {{...}}, exactly one origin, exact name/type/path, valid JSON
  -> mv into .../Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json
  -> re-check mode 600; print "quit and reopen Chrome" next steps

scripts/uninstall-native-host.sh [--reset]
  -> default: remove rendered manifest + built binary only
  -> --reset: print removals first, then also queue.sqlite3 (+ -wal/-shm),
     queue.lock, config.json, and Keychain records for service
     com.neelbangera.lecturetranscripts
```

The manifest file is ignored by Git (`native-host/*.json` in `.gitignore`), so
a rendered registration with a machine-specific path cannot be committed.

### Extension-ID drift

An unpacked Chrome extension's ID is derived from the directory path it was
loaded from. Moving or reloading the extension from a different path changes
the ID, and the rendered manifest's single allowed origin no longer matches.
Symptoms: the popup reports the local uploader is unavailable and the service
worker cannot reach the native host. Fix: copy the new 32-character ID from
`chrome://extensions`, run
`scripts/install-native-host.sh <new-loaded-extension-id>`, and fully quit and
reopen Chrome. Never hand-edit the rendered manifest and never add a wildcard
origin. Reinstalling from the same path keeps the same ID and is an idempotent
no-op.

## Data flow

```text
Chrome starts the host for an extension port:
  argv[1] = chrome-extension://<id>/
Chrome -> host stdin : 4-byte little-endian length prefix + JSON request
host stdout -> Chrome: 4-byte little-endian length prefix + JSON response
```

The manifest never carries transcript data, credentials, or machine-local
configuration; it only names the binary and the one allowed extension origin.
The uploader's machine-local config and queue live under
`~/Library/Application Support/LectureTranscripts/`.

## File responsibilities

| File | Role |
| --- | --- |
| `com.neelbangera.lecturetranscripts.json.in` | The only source file: host name, description, `{{BINARY_PATH}}`, `stdio` type, single `{{EXTENSION_ID}}` allowed origin |
| `ARCHITECTURE.md` | This document |

Rendered (never committed):
`~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`.

## Testing and verification

- `sh scripts/tests/run.sh` asserts the template keeps both placeholders, the
  exact host name, and `"type": "stdio"`, and that it contains no real
  extension ID; it then renders the template into a fake `HOME` and checks the
  path, mode `0600`, exact origin, absolute binary path, valid JSON, absence of
  placeholders, and the absence of duplicate registrations.
- `uploader/internal/host/native_messaging_test.go` covers exact origin
  validation, wildcard/missing/mismatched origins, framing, and the frame cap.
- `sh -n scripts/install-native-host.sh scripts/uninstall-native-host.sh`
  checks shell syntax.
- Manual real-machine check (documented in `docs/SETUP.md` and
  `docs/TESTING.md`): after installing, confirm mode `600` and exactly one
  origin in the rendered manifest, then quit and reopen Chrome.
- Verified while writing this document: `sh scripts/tests/run.sh` passes
  `59 passed, 0 failed`, which includes the template and rendering checks.

## Related plan sections

- § Canonical Native Messaging host template
- § Normative implementation contracts (host template is one of them)
- § Stage 4 — Implement the native host and durable queue (origin validation)
- § Stage 7 — Package, install, and document the personal deployment
  (installer, uninstall, ID drift)
- § Complete implementation file inventory § Native host and build/install
  files
- § Generated and machine-local artifacts (rendered manifest path)

## How to change this directory safely

- Change `TECHNICAL_PLAN.md` § Canonical Native Messaging host template first,
  then the template, the installer's checks, `scripts/tests/run.sh`, and this
  document.
- Keep exactly one allowed origin and no wildcard. Do not add a default or
  example extension ID to the template; keep `{{EXTENSION_ID}}` unresolved.
- Keep `{{BINARY_PATH}}` as the only source of the executable path; never
  commit a machine-specific absolute path.
- If the host name changes, update all four places together: the template, the
  installer/uninstaller `host_name`, `extension/src/native-messaging.ts`
  `NATIVE_HOST_NAME`, and `uploader/cmd/lecture-uploader/main.go`
  `hostManifestName`.
- Re-run `sh scripts/tests/run.sh` and `sh -n` on both host scripts after any
  change.

### Open questions and known inconsistencies

- No `IMPLEMENTATION.md` exists in this directory; the plan's file inventory
  and § Stage 7 are the design record. Whether every directory should carry an
  `IMPLEMENTATION.md` is not specified.
- The installer requires the binary to exist and be executable before writing
  the manifest; the plan does not state whether the binary check belongs to
  install time or first Chrome launch. The current behavior is stricter and is
  locked by `scripts/tests/run.sh`.
- The rendered manifest is per-user under `$HOME`; the plan and scripts assume
  the default Chrome profile location and do not support multiple profiles or
  Chrome channels. This matches the one-Mac/one-Chrome Phase 1 scope but is
  not called out in the plan.
