# Scripts Architecture

## Purpose

`scripts/` contains every build and installation entry point for the personal
deployment: the extension bundler, the macOS uploader builder, the Native
Messaging host installer and uninstaller, and the packaging self-test suite.
Nothing here runs during capture or upload; these are packaging-time tools that
turn the source tree into `dist/extension/`, `dist/native/lecture-uploader`,
and the rendered per-user Chrome host manifest.

## Boundaries and dependencies

- `scripts/build-extension.mjs` reads `extension/`, the Stage 0 selector and
  course-mapping fixtures under `extension-tests/fixtures/`, and
  `package.json`; it writes only `dist/extension/`. It is invoked by
  `npm run build` / `npm run build:extension` (`package.json`) and by the
  build smoke test.
- `scripts/build-uploader.sh` reads `package.json` for the default version and
  builds `uploader/cmd/lecture-uploader` into `dist/native/lecture-uploader`.
  It requires macOS, the Xcode Command Line Tools, Go, and cgo.
- `scripts/install-native-host.sh` renders
  `native-host/com.neelbangera.lecturetranscripts.json.in` into the per-user
  Chrome Native Messaging Hosts directory. It requires the built binary and the
  real loaded extension ID; it never invents or defaults either.
- `scripts/uninstall-native-host.sh` removes the files the scripts installed;
  `--reset` additionally removes the queue, config, and Keychain records.
- `scripts/tests/run.sh` tests the three shell scripts without touching the
  real home directory or Keychain.
- No script talks to GitHub or Leccap, and none may print a credential, raw
  capture, or real extension ID.

## Contracts and invariants

### `scripts/build-extension.mjs`

- Node ESM (`node scripts/build-extension.mjs`), no CLI arguments; resolves
  the repository root from `import.meta.url`. All failures throw with a
  `build-extension: ` prefix and a nonzero exit.
- Validates `extension-tests/fixtures/lecture-page.selectors.json` before use:
  `transcriptButtonSelector`, `transcriptContainerSelector`, and
  `timestampFormat` must be nonempty strings; `courseSelector`,
  `termSelector`, and `lectureNumberSelector` must be objects with nonempty
  `selector` and `regex` strings; `lectureDateSource` must have nonempty
  `dateSelector` and `dateRegex`; `completionIndicator` must have nonempty
  `selector` and `mode`; `loadingTextMarkers` must be an array;
  `stabilityDebounceMs` and `sanityMinChars` must be positive numbers.
- Validates that `course-mapping.json` has a nonempty `courseMappings` array
  and that `package.json` and `extension/manifest.json` have a nonempty
  `version`; then sets `manifest.version = packageJson.version`.
- Requires `extension/icons/icon128.png` to exist.
- Deletes and recreates `dist/extension/`, then bundles four entries with
  esbuild: `content.ts` as `iife`; `background.ts`, `popup.ts`, and
  `options.ts` as `esm`. Shared options: `bundle: true`,
  `platform: "browser"`, `target: ["chrome120"]`, `sourcemap: false`,
  `logLevel: "warning"`, and
  `define: { __STAGE0_SELECTORS__: JSON.stringify(selectorsFixture) }`. The
  built content bundle therefore contains the verified selectors and has no
  runtime dependency on the fixture path.
- Copies `popup.html`, `popup.css`, `options.html`, `options.css`, the whole
  `extension/icons/` directory, and a pretty-printed `manifest.json`
  (2-space indent, trailing newline).
- Does not embed the course mapping; `extension/src/course-config.ts` holds the
  allowlist, which must stay an exact copy of the fixture (the fixture is
  validated here only for a nonempty array).
- The build script is committed with mode `0644` and is run through `node`;
  the plan inventory lists the shell scripts as `0755`.

### `scripts/build-uploader.sh`

- `#!/bin/sh`, `set -eu`, mode `0755`. Usage:
  `scripts/build-uploader.sh [--version VERSION] [--dry-run]`.
- Fails closed before any build when: the host is not Darwin; `xcrun` is
  missing; `xcode-select -p` fails; `xcrun --find clang` fails; `go` is
  missing.
- Version selection: `--version` wins; otherwise the first `"version"` value
  is read from `package.json`. Validation: must start with `[0-9A-Za-z]`, may
  contain only `[0-9A-Za-z._+-]`, and must be at most 32 characters (matching
  the protocol `versionString` pattern).
- `--dry-run` prints `version`, platform, architecture, and the output path
  and exits `0` before building (platform/toolchain checks still apply).
- Build: exports `CGO_ENABLED=1`, `GOOS=darwin`, `GOARCH=arm64`, changes into
  `uploader/`, and runs
  `go build -trimpath -ldflags "-X main.version=$version" -o dist/native/lecture-uploader ./cmd/lecture-uploader`.
  The injected variable is the conventional `var version = "dev"` in
  `uploader/cmd/lecture-uploader/main.go`.
- Verifies the output is executable. The binary is gitignored
  (`uploader/lecture-uploader` and `dist/`).

### `scripts/install-native-host.sh`

- `#!/bin/sh`, `set -eu`, mode `0755`. Usage:
  `scripts/install-native-host.sh <extension-id> [--binary ABSOLUTE_PATH]`.
- Extension ID: required, exactly 32 characters, characters `a`-`p` only
  (Chrome's unpacked-extension alphabet); a missing, wildcard, uppercase,
  non-`a`-`p`, or wrong-length ID fails. It is never guessed or defaulted.
- `--binary`: must be absolute, may not contain quotes, backslashes, or
  newlines, must exist, and must be executable; the default is
  `dist/native/lecture-uploader`.
- Requires Darwin. Verifies the template exists and contains exactly one
  `{{BINARY_PATH}}` and exactly one `{{EXTENSION_ID}}` placeholder.
- Renders with `umask 077` into a `mktemp` file, then `chmod 0600`, into
  `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`.
- Post-render checks: no `{{` remains; exactly one `chrome-extension://`
  occurrence; the exact origin `chrome-extension://<extension-id>/`, the exact
  name, `"type": "stdio"`, and the exact absolute `"path"` are present; the
  file is valid JSON via `python3`, else `plutil`; if neither exists it refuses
  to install. It never writes a wildcard origin.
- Reinstalling with identical rendered content is a no-op (`cmp -s`) that
  re-applies mode `0600`; different content replaces the single registration.
- Prints next steps: fully quit and reopen Chrome, load the unpacked
  extension, create the machine-local config, and authorize GitHub choosing
  **Always Allow** at the Keychain prompt.

### `scripts/uninstall-native-host.sh`

- `#!/bin/sh`, `set -eu`, mode `0755`. Usage:
  `scripts/uninstall-native-host.sh [--reset] [--binary ABSOLUTE_PATH]`.
- Default: removes only the rendered host manifest
  (`.../NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`) and the
  built uploader binary. It never touches the repository, the remote GitHub
  repository, the queue database, the config, or Keychain records, and it is
  idempotent when nothing is installed.
- `--reset`: first prints exactly what it will remove, then additionally
  removes `queue.sqlite3` plus `-wal`/`-shm` sidecars, `queue.lock`,
  `config.json`, and Keychain generic-password records for service
  `com.neelbangera.lecturetranscripts` (the two named accounts plus up to ten
  further `security delete-generic-password` attempts for any other account
  under that service). It still never removes the repository, the remote
  repository, or `~/Library/Logs/LectureTranscripts`.
- A non-regular file at the manifest path fails rather than being removed; a
  missing `security` tool is reported and Keychain removal is skipped.

### `scripts/tests/run.sh`

- `#!/bin/sh`, `set -eu`, mode `0755`; run as `sh scripts/tests/run.sh`; no
  test framework. Creates a `mktemp -d` work directory, a fake `HOME`, a fake
  executable uploader, and a stubbed `security` command that appends its
  arguments to a log and exits `1`, so the real home directory and Keychain
  are never touched.
- Covers: template placeholders/name/type and absence of a real extension ID;
  installer argument validation (missing, malformed, wildcard, uppercase,
  non-`a`-`p`, 33-character IDs; relative or missing binary paths); rendered
  manifest path, mode `600`, exact single origin, absolute binary path, no
  remaining placeholders, valid JSON, exactly one registration file; idempotent
  reinstall and ID-change replacement; default uninstall gating (removes only
  manifest and binary, keeps queue/WAL/lock/config, never calls `security`);
  `--reset` announcement-before-removal, full removal, and Keychain calls;
  `build-uploader.sh` version validation and `--dry-run` output.
- Prints `N passed, M failed` and exits `1` if any check failed. Current tree:
  `59 passed, 0 failed` (verified while writing this document).

## Data flow

```text
extension/ + extension-tests/fixtures/ + package.json
  -> scripts/build-extension.mjs -> dist/extension/ (load unpacked in Chrome)

package.json + uploader/
  -> scripts/build-uploader.sh -> dist/native/lecture-uploader

native-host/*.in + loaded extension ID + built binary
  -> scripts/install-native-host.sh -> rendered host manifest (0600)
  -> Chrome launches the binary over Native Messaging

scripts/uninstall-native-host.sh [--reset]
  -> removes manifest + binary (default) or also queue/config/Keychain (--reset)
```

## File responsibilities

| File | Role |
| --- | --- |
| `build-extension.mjs` | Validate/embed the Stage 0 selector fixture, bundle the four extension entries with esbuild, copy HTML/CSS/icons, inject the `package.json` version into the manifest, write `dist/extension/` |
| `build-uploader.sh` | macOS/cgo/arm64 build of `dist/native/lecture-uploader` with `-X main.version`; `--version` and `--dry-run` |
| `install-native-host.sh` | Validate the loaded extension ID and binary, render the host manifest to the Chrome Native Messaging Hosts directory with mode `0600` and exactly one allowed origin |
| `uninstall-native-host.sh` | Remove the installed manifest and binary; `--reset` additionally removes queue, config, and Keychain records after announcing them |
| `tests/run.sh` | Packaging self-tests with temporary `HOME` and stubbed `security` |
| `ARCHITECTURE.md` | This document |

## Testing and verification

```sh
npm run build            # runs scripts/build-extension.mjs
npm test                 # includes extension-tests/build-smoke.test.ts
scripts/build-uploader.sh --dry-run
scripts/build-uploader.sh
scripts/install-native-host.sh <loaded-extension-id>
sh scripts/tests/run.sh
sh -n scripts/build-uploader.sh scripts/install-native-host.sh scripts/uninstall-native-host.sh scripts/tests/run.sh
```

- `extension-tests/build-smoke.test.ts` invokes `build-extension.mjs` itself,
  then asserts every manifest-referenced file exists, the icon is a PNG, the
  HTML-referenced local assets exist, no `__STAGE0_SELECTORS__` placeholder
  remains, and the built bundles contain the selector values, `capture_job`,
  `autoCapture`/`notificationsEnabled`, and `openOptionsPage`.
- `scripts/tests/run.sh` is the only automated check for the installer,
  uninstaller, and uploader build script.
- Verified while writing this document: `sh -n` is clean for all four shell
  scripts, `sh scripts/tests/run.sh` reports `59 passed, 0 failed`, and the
  npm suites were not re-run because `node_modules/` is not installed here.

## Related plan sections

- § Toolchain baseline
- § Canonical Native Messaging host template
- § Stage 7 — Package, install, and document the personal deployment
  (build, installer, uninstall, documentation requirements)
- § Complete implementation file inventory § Native host and build/install
  files
- § Definition of done (packaging acceptance)

## How to change this directory safely

- Treat every script as a contract: usage strings, exit codes, paths, modes,
  and the extension-ID alphabet are relied on by tests and documentation.
- Change the plan first when behavior, paths, or limits change; then update the
  script, `scripts/tests/run.sh`, this document, and `docs/SETUP.md` /
  `docs/TROUBLESHOOTING.md` together.
- Never add a default extension ID, a wildcard origin, a hardcoded absolute
  binary path, or a machine-local value. Never print credentials or raw
  captures.
- Keep `build-uploader.sh` failing closed on non-Darwin hosts and keep the
  `-X main.version` variable name stable (`main.version`).
- After changes run `sh -n` on every script, `sh scripts/tests/run.sh`, and
  `npm test` when the extension build was touched.

### Open questions and known inconsistencies

- Plan § Stage 7 item 1 requires the extension build to fail when the selector
  fixture contains template markers; `build-extension.mjs` validates shape,
  JSON validity, and non-emptiness but has no template-marker check. The
  placeholder check lives in `extension-tests/fixture-packet.test.ts`.
- `build-uploader.sh` always targets `darwin/arm64`; on an Intel Mac the
  `--dry-run` output would report `x86_64` while the build still sets
  `GOARCH=arm64`. The plan and `docs/SETUP.md` assume Apple silicon, so this
  is a documentation gap rather than a code defect.
- `scripts/tests/run.sh` uses `stat -f '%Lp'` and a `security` stub, so it is
  macOS-specific; there is no Linux/CI variant in the tree.
- `scripts/build-extension.mjs` is mode `0644` while the plan inventory lists
  the build/install scripts as `0755`; it is invoked through `node` and the
  smoke test calls `process.execPath`, so the mode is not functionally
  significant.
