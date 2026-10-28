# Releasing Lecture Transcripts (D2 draft runbook)

This is the maintainer runbook for versioned draft releases. It records actual
release evidence; writing this file does not mean a release has passed.

Status: tooling implemented (`scripts/package-release.sh`,
`.github/workflows/release.yml`); no public release published yet. The public
App, version/tag, and clean-machine acceptance below are release inputs — they
do not unblock implementing the packaging work, and no live value is fabricated
here.

## Versioning

- Product version lives in `package.json` (`0.1.0` today). The extension
  manifest version is synced from it at build time; the uploader version is
  injected with `-ldflags -X main.version=`. Protocol version is `1` in
  `extension/src/native-messaging.ts`, `uploader/internal/protocol/job.go`,
  and `protocol/native-messaging.schema.json` — all three must agree.
- To cut a release: bump `package.json`, commit, tag `v<VERSION>` on that
  commit, push the tag. The workflow fails if the tag does not match
  `package.json`.
- Never rewrite a published tag. Bump and re-tag instead; rollback means
  publishing a newer version, never mutating an asset in place.

## Maintainer inputs (managed secrets / local-only)

| Input | Where it lives |
| --- | --- |
| Public App client ID + installation URL (Device Flow enabled, Contents read/write, installed per-user on one repo) | `RELEASE_APP_CLIENT_ID` / `RELEASE_APP_INSTALL_URL` CI secrets; `release.json` after publish (public metadata) |
| Clean supported Mac + Chrome + permitted Leccap access + public/private test repos | Maintainer hardware; results recorded below |
| Chrome Web Store account / Developer ID identity | D3 only; never in this repo |

User repository IDs, machine configs, tokens, device codes, signing credentials,
and private keys are never committed and never enter release assets.

## Build locally

```sh
npm ci
npm run typecheck
npm test
cd uploader && go vet ./... && go test ./... -count=1 && cd ..
sh scripts/tests/run.sh   # 107 checks incl. full packaging
sh scripts/package-release.sh \
  --app-client-id <public-app-client-id> \
  --app-install-url https://github.com/apps/<slug>/installations/new \
  --out-dir dist/release
```

Expected in `dist/release/` (all from one tag, versions in lockstep):

- `lecture-transcripts-extension-v<VERSION>.zip`
- `lecture-transcripts-macos-arm64-v<VERSION>.zip` (uploader, `extension/`,
  `Install.command`, `Uninstall.command`, host template, `INSTALL.txt`,
  `release.json`)
- `release.json` (product/protocol/platform/arch/asset names/App metadata)
- `SHA256SUMS` (`shasum -a 256 -c` must pass)

The packager fails closed on: non-Darwin/non-arm64 hosts, missing Node/Go/Xcode,
version or protocol mismatch, placeholder App metadata, non-github install URLs,
or secret-like material in the bundle. Generated artifacts stay out of source
control.

## Publish a draft

Pushing `v*` runs `.github/workflows/release.yml` (pinned actions, least
`contents` privilege) and creates a **draft** release with the four assets.
Drafts stay unpublished until the checklist below passes on a real supported
Mac. Create draft releases freely; publish only after every box is checked with
dated evidence.

## D2 acceptance checklist (record date + result for each)

On a clean supported Mac (Apple silicon, desktop Chrome) with no Node, Go,
Xcode, or checkout:

- [ ] Unzip installs without source edits, manual JSON, or creating an App.
- [ ] Public + private repository discovery both work; canceled/expired auth,
      wrong account, inaccessible repo, missing `main`, and declined Keychain
      each fail without leaving a partial config or usable discovery credential.
- [ ] Host manifest has the exact single origin, absolute stable binary path,
      product/protocol versions, and `0600` / `0700` modes; invalid IDs, corrupt
      assets, unsupported arch, and incompatible versions are rejected pre-activation.
- [ ] A permitted lecture captures and the remote Markdown appears; offline /
      restart recovery, duplicates, write-once conflicts, and destination
      pinning all behave (no upload can leave the verified destination).
- [ ] Reinstall/update preserves queue, credentials, config, and extension
      state; an interrupted update rolls back to the previous install.
- [ ] Destination/App changes are refused until an explicit reset clears
      uploader state **and** pending extension handoffs; remote files are never
      deleted by reset/uninstall.
- [ ] Uninstall preserves state by default; `--reset` removes local state only.
- [ ] Downloaded-bundle Gatekeeper behavior observed on a clean machine and
      documented (never advise disabling Gatekeeper).
- [ ] `docs/SETUP.md`, `README.md`, `SECURITY.md`, `TESTING.md`,
      `TROUBLESHOOTING.md` updated to the release flow only now (until then
      `docs/SETUP.md` stays the source-installation runbook).

Evidence log:

| Date | Version | Mac/Chrome | Public repo | Private repo | Gatekeeper observed | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| _unrecorded_ | — | — | — | — | — | No release acceptance has been performed. |

## Rollback

- Keep the previous stable install until the new one verifies (the installer
  does this automatically; on failure it restores the backup).
- If a published draft is bad: leave it as draft, fix forward, cut a new
  version. Never force-push a tag or overwrite an asset.
- Queue/config/credentials are never migrated across destinations; a
  destination change always requires the explicit reset path above.

## D3 handoff (not this phase)

Store submission (verified manifest key/ID) and Developer ID signing/notarization
use a real identity on a separate Mac; record launch, signature, notarization,
Native Messaging, and Keychain results here before reducing the user guide to
the three-step published flow.
