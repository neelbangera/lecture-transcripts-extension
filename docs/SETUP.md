# Setup

This guide installs the three local pieces of the system on one Mac and connects
them to one GitHub repository:

1. the Chrome extension built from this repository,
2. the macOS uploader reached through Chrome Native Messaging,
3. the machine-local configuration and GitHub App that authorize uploads.

`TECHNICAL_PLAN.md` is the contract authority for behavior, limits, status
names, and paths. This guide only explains how to install and verify them. It
never contains a real client ID, repository ID, token, or extension ID. Anything
written as `<...>` is a value the owner supplies and must not be committed.

## Current build state

The capture-side TypeScript, the protocol contracts, and the Go packages for
config, queue, logging, retry, auth, GitHub publishing, Markdown rendering, and
the Native Messaging host are implemented and covered by the test suites in
[TESTING.md](TESTING.md). The packaging scripts and their self-tests are also in
the tree.

The Go executable entrypoint `uploader/cmd/lecture-uploader` and the serial
processor `uploader/internal/processor` are not in the tree yet. As a result,
`scripts/build-uploader.sh` currently fails closed with
`uploader/cmd/lecture-uploader is missing; nothing to build`. Everything up to
"Build the uploader" can be completed and verified; "Install the Native
Messaging host" and the end-to-end checks require the executable to land first.

## Prerequisites

- A Mac with Apple silicon for the supported uploader build (the build script
  targets `GOOS=darwin GOARCH=arm64`).
- Chrome desktop, signed in to the Leccap session used for capture.
- Node.js 22 LTS and npm (`package.json` requires `node >=22`).
- Go 1.24.x.
- Xcode Command Line Tools, selected and with a working compiler, because the
  Keychain adapter uses cgo and `Security.framework`:
  - install with `xcode-select --install`;
  - verify with `xcode-select -p` and `xcrun --find clang`.
  `scripts/build-uploader.sh` sets `CGO_ENABLED=1` itself.
- Optional but recommended: `python3` or `plutil` so the host installer can
  validate the rendered JSON manifest.

## Build and test the repository

From a clean checkout at the repository root:

```sh
npm ci
npm run typecheck
npm test
npm run build
```

`npm run build` writes the loadable unpacked extension to `dist/extension/`
(`background.js`, `content.js`, `popup.js`, `manifest.json`, `popup.html`,
`popup.css`).

Run the Go uploader tests from the module directory:

```sh
cd uploader
go test ./...
```

Run the packaging self-tests (temporary `HOME`, stubbed `security`; they never
touch the real home directory or Keychain):

```sh
sh scripts/tests/run.sh
```

## Load the extension unpacked

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Choose **Load unpacked** and select the `dist/extension/` directory.
4. Copy the 32-character extension ID Chrome shows on the card. This is the
   `<loaded-extension-id>` used by the host installer.
5. Pin the extension and open its popup once to confirm it renders.

For an unpacked extension Chrome derives the ID from the directory path. Keep
the checkout path fixed; moving or reloading from a different path changes the
ID and requires reinstalling the Native Messaging host (see
[Extension-ID drift](#extension-id-drift)).

## Create the destination repository and GitHub App

The repository must already exist and have at least one commit on `main`. The
uploader never initializes an empty repository.

1. Create or verify `neelbangera/lecture-transcripts` with at least one commit
   on `main` (for example a README created in the GitHub UI).
2. Record the numeric repository ID. It is the `"id"` field from
   `GET https://api.github.com/repos/neelbangera/lecture-transcripts`
   (`gh api repos/neelbangera/lecture-transcripts --jq .id` works too). For a
   private repository use the authenticated value.
3. Create one GitHub App under **Settings → Developer settings → GitHub Apps →
   New GitHub App**:
   - enable **Device Flow**;
   - under **Repository permissions**, set **Contents** to **Read and write**
     and grant nothing else;
   - install it **only** on `neelbangera/lecture-transcripts`;
   - copy the App's public **client ID** from the App settings page.
4. Do not create a client secret or App private key. The device flow and the
   uploader do not need either one.

The uploader verifies all of this at connect: the numeric repository ID, the
`owner/repo` full name, and a successful Contents read on `main`. If any check
fails it reports `target_repository_unavailable` and clears the just-authorized
credential.

## Write the machine-local configuration

Create `~/Library/Application Support/LectureTranscripts/config.json` (create
the directory with mode `0700` if it does not exist). It must contain exactly
these fields:

```json
{
  "schemaVersion": 1,
  "githubAppClientId": "<github-app-client-id>",
  "repositoryId": <numeric-repository-id>,
  "owner": "neelbangera",
  "repo": "lecture-transcripts",
  "branch": "main"
}
```

`uploader/internal/config/config.example.json` shows the same field names with
nonfunctional placeholders; it is documentation, not a template to copy
verbatim. The loader rejects unknown fields, missing fields, multiple JSON
values, a zero or negative `repositoryId`, a missing or placeholder
`githubAppClientId`, and any `owner`, `repo`, or `branch` other than the three
values above.

This file is machine-local and is never committed. It contains no secret (the
client ID is public and the repository ID is not sensitive), but it is still an
owner-specific file: do not copy it into the repository or into an issue.

## Build the uploader

```sh
scripts/build-uploader.sh
```

The script builds `dist/native/lecture-uploader` with `CGO_ENABLED=1`,
`GOOS=darwin`, `GOARCH=arm64`, and an injected version from `package.json`. It
fails closed on a non-Darwin host, a missing Xcode toolchain, or a missing Go
installation, and supports:

```sh
scripts/build-uploader.sh --dry-run
scripts/build-uploader.sh --version 0.1.0
```

As noted under [Current build state](#current-build-state), the script currently
stops at `uploader/cmd/lecture-uploader is missing; nothing to build` because
the executable entrypoint has not landed yet. The binary path and all checks
below assume that entrypoint exists.

## Install the Native Messaging host

With the uploader binary built and the loaded extension ID copied from
`chrome://extensions`:

```sh
scripts/install-native-host.sh <loaded-extension-id>
```

The installer:

- validates the ID (exactly 32 characters, `a`-`p` only) and requires the
  binary at `dist/native/lecture-uploader` unless `--binary ABSOLUTE_PATH` is
  given;
- renders `native-host/com.neelbangera.lecturetranscripts.json.in` to
  `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`
  with mode `0600`;
- writes exactly one allowed origin, `chrome-extension://<loaded-extension-id>/`,
  and never a wildcard;
- replaces an existing registration instead of creating a duplicate, and is a
  no-op when the rendered content is already identical;
- validates that the result is JSON and that no `{{...}}` placeholder remains.

Fully quit and reopen Chrome after installing or changing the host manifest so
Chrome reads the registration.

## Authorize GitHub

1. Open the extension popup and choose **Connect GitHub**.
2. The uploader starts the GitHub App device flow and the popup shows the
   `user_code` and a GitHub verification link.
3. Open the link (or `https://github.com/login/device`), enter the code, and
   authorize the App.
4. When macOS asks whether the uploader may use the Keychain item, choose
   **Always Allow**. The first access may prompt; a rebuilt binary can prompt
   again.
5. The popup reports **GitHub connected** only after the repository sanity
   checks pass. If it reports `target_repository_unavailable`, verify the
   numeric repository ID, the App installation, the Contents permission, and
   that `main` has at least one commit.

The extension never receives a token. See [SECURITY.md](SECURITY.md) for the
Keychain records and the full authorization boundary.

## Verify the installation

Run the automated checks:

```sh
npm run typecheck
npm test
cd uploader && go test ./...
cd .. && sh scripts/tests/run.sh
```

Check the installed files and modes:

```sh
stat -f '%Lp' "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json"
grep -c 'chrome-extension://' "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json"
python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$HOME/Library/Application Support/LectureTranscripts/config.json"
ls -la "$HOME/Library/Application Support/LectureTranscripts"
ls -la "$HOME/Library/Logs/LectureTranscripts"
```

Expected: manifest mode `600`, exactly one `chrome-extension://` origin, the
config parses as JSON, the data directory is `0700`, and the queue database and
its sidecars are `0600`.

Then verify the real flow:

1. Open a permitted Leccap lecture page and click **Show Transcript** (an
   already-expanded, populated transcript also activates capture).
2. The popup shows the job in the uploader queue as `Queued for upload`, then
   `Uploaded` or `Already uploaded; unchanged`.
3. The repository gains one file at
   `courses/<courseSlug>/<term>/lectures/<NNN>.md`.
4. Reopening the same transcript creates no new commit.

The per-sample render-time measurement is still an owner-side item; the recipe
is in [STAGE_0_REPORT.md](STAGE_0_REPORT.md) and
[render-time-snippet.js](render-time-snippet.js). Record the measured values in
`extension-tests/fixtures/transcript-size-report.json` only after running the
snippet on a real permitted page.

## Extension-ID drift

An unpacked extension's ID is derived from the directory path it was loaded
from. Reloading or moving the extension from a different path produces a new ID,
and the previously rendered host manifest no longer matches. Symptoms and the
fix:

- The popup reports the local uploader is unavailable, or the service worker
  cannot connect to the native host.
- Run `scripts/install-native-host.sh <new-loaded-extension-id>` and fully quit
  and reopen Chrome.
- Never edit the rendered manifest by hand and never use a wildcard origin.

Reinstalling from the same path keeps the same ID and is an idempotent no-op.
The extension ID is intentionally not stored in this repository; the installer
takes it as an explicit argument every time.

## Uninstall and reset

Remove only the host registration and built binary:

```sh
scripts/uninstall-native-host.sh
```

Reset additionally removes the local queue database (plus `-wal`/`-shm`
sidecars and `queue.lock`), the machine-local config, and this host's Keychain
records. The script prints exactly what it will remove before removing it:

```sh
scripts/uninstall-native-host.sh --reset
```

Neither form touches the repository, the remote GitHub repository, or the logs
under `~/Library/Logs/LectureTranscripts`. The popup's **Reset connection**
button deletes only the Keychain credential and in-flight device transaction;
queued jobs remain durable and resume after reconnection. The full reset
semantics are documented in [SECURITY.md](SECURITY.md#reset-semantics).
