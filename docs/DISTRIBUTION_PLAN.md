# Easier installation

Status: D1 implemented; D2 tooling implemented, unpublished. `lecture-uploader --setup`, `scripts/package-release.sh`, `Install.command`/`Uninstall.command` + `INSTALL.txt`, `--stable` host scripts, the draft `release.yml` workflow, `docs/RELEASING.md`, and the distinct extension destination reset (`popup_reset_destination`) are in the tree with unit + packaging coverage. No public App has been provisioned and no real Mac/Chrome/Leccap acceptance has been recorded in `docs/RELEASING.md`; `docs/SETUP.md` remains the source-installation runbook until then. [SETUP.md](SETUP.md) describes the working source-installation path.

This is the implementation handoff for [the distribution phase in the technical plan](../TECHNICAL_PLAN.md#easier-installation-and-distribution). That plan defines the requirements; this guide explains the work and its release gates.

## Installation goal

The initial release should let a user download a Mac bundle, run its installer, load the included Chrome extension, connect GitHub, and choose a repository. They should not need Node, Go, Xcode, a source checkout, hand-written JSON, source edits, or their own GitHub App.

The first release still uses Chrome's unpacked-extension controls. The later published flow is:

1. Add Lecture Transcripts from the Chrome Web Store.
2. Install the Mac uploader.
3. Connect GitHub and choose a repository.

Apple silicon macOS and desktop Chrome remain the supported platform. Each installation has one account and one repository on initialized `main`. Windows, Intel Mac builds, hosted uploads, account profiles, automatic updates, and new Leccap page shapes are separate work.

## What must change

| Current setup | Planned replacement |
| --- | --- |
| Clone and build with Node, Go, and Xcode tools | Download prebuilt assets from a versioned GitHub Release |
| Edit fixed Go owner/repository constants | Validate the user's chosen repository in local config |
| Create and configure a GitHub App | Install and authorize the maintainer's public App |
| Find the numeric repository ID and write JSON | Guided setup discovers and verifies the destination, then writes config |
| Register a binary inside the source checkout | Register a binary in a stable per-user installation directory |
| Copy an unpacked extension ID | Keep this step initially; remove it once a verified published identity exists |

## Delivery sequence

### D1: configurable repository and setup

1. Generalize `uploader/internal/config/config.go` and its example/tests. Keep schema version 1, strict validation, positive repository ID, and `main`. Validate repository names as GitHub identifiers, not arbitrary paths.
2. Resolve private-repository discovery before building the full wizard. Existing auth requires a numeric ID before device authorization; a repository URL alone does not satisfy that contract. Record the verified GitHub API sequence and its permission requirements in this guide and the technical plan.
3. Add `uploader/internal/setup/` and a `lecture-uploader --setup` command. Keep interactive output separate from Native Messaging. Reuse the existing Keychain, GitHub verification, config permissions, and bounded network behavior.
4. Require a complete verified config before the native host can publish. Never save a dummy repository ID or partially populated runtime config to get through authorization.

**Authorization discovery gate:** final publishing credentials must be repository restricted. For public repositories, metadata lookup may resolve the ID before authorization. Private repositories require authenticated discovery. A temporary discovery authorization may omit `repository_id`, but it must not publish or become the saved publishing credential. If no supported narrowing mechanism exists, perform a second device authorization with the discovered ID. Explain the extra authorization in setup; do not weaken this requirement to hide an extra step. Canceling setup must clean up its local temporary state. Verify what server-side token revocation is available to a public device-flow client before selecting the bootstrap implementation; do not claim that deleting a local token revokes it remotely.

**Verified bootstrap (implemented in `uploader/internal/setup/setup.go`):** `GET /repos/{owner}/{repo}` unauthenticated returns `id`/`full_name` for public repos and 404 for private/missing. Private discovery runs one temporary Device Flow **without** `repository_id`, uses the in-memory token for a single authenticated `GET /repos/{owner}/{repo}`, then clears it; it is never written to Keychain or disk. Publishing always runs a second Device Flow **with** `repository_id` via `auth.Manager`, which verifies numeric ID, full name, and readable Contents on `main` before reporting connected. `DELETE /applications/{client_id}/token` requires client_id + client_secret Basic auth, which a public device-flow client does not ship, so the temporary token cannot be revoked server-side by the uploader: it expires automatically and the user may revoke it at GitHub Settings → Applications. Setup never describes local deletion as remote revocation. `uploader/internal/setup/setup_test.go` covers public/private parsing, unauthenticated discovery, existing-installation refusal, atomic config write, and a fake-server end-to-end public run.

**D1 is complete when:** setup works with public/private destinations, validates numeric ID and full name, confirms Contents access on `main`, stores only the final publishing credential in Keychain, and refuses unsafe reconfiguration of an existing installation. Maintainer App metadata is an external input; implementation tests use explicit fake values.

### D2: prebuilt bundle and guided installer

Ship these assets from the same release tag:

| Asset | Contents |
| --- | --- |
| `lecture-transcripts-extension-v<VERSION>.zip` | The compiled extension, ready for unpacked loading |
| `lecture-transcripts-macos-arm64-v<VERSION>.zip` | Uploader, extension assets, `Install.command`, `Uninstall.command`, host template, and a short installation note |
| `release.json` | Product version, protocol version, platform/architecture, asset names, public App client ID and installation URL |
| `SHA256SUMS` | SHA-256 digests of the archives and release metadata |

`<VERSION>` is a placeholder, not an existing release. Build dependencies belong on the maintainer/CI machine. The Mac bundle includes the extension so users need only one download. Generated artifacts stay outside source control.

The installer should:

1. Check macOS/arm64, bundle integrity, component compatibility, and existing installation state before making changes.
2. Ask the user to close Chrome so an active host cannot race setup or an update. Validate that the host is stopped before replacing active files.
3. Stage the uploader and extension in stable per-user directories. Explain where to load the extension in `chrome://extensions`, then validate the copied ID as 32 characters in `a`–`p`. For an initial unpacked install, open Chrome only for this step and close it again before setup commits the installation.
4. Ask for a `github.com` repository URL or `OWNER/REPO`. Reject credentials in URLs, other hosts, traversal, and extra path/query/fragment components rather than interpreting them as destinations.
5. Open the public App installation page and ask the user to grant access only to the selected repository. Run guided device authorization and authenticated target verification.
6. Commit complete config and the exact Chrome host registration only after validation. Preserve the previous installation until activation succeeds; roll back staged changes if setup is canceled or fails. Cross-file and Keychain writes need a recoverable sequence, not an assumption that they form one atomic transaction.
7. Tell the user to restart Chrome, open the extension, and verify connection and a real capture. Show useful recovery steps when a check fails.

No `sudo` is needed. Reuse and adapt the existing install/uninstall scripts instead of maintaining a second set of host-registration rules. Pin CI dependencies to reviewed versions and grant the release workflow only the permissions it needs. Create draft releases until the release checklist is complete.

An unsigned downloaded binary or `.command` file may trigger macOS security checks. Document the behavior observed on a clean machine before calling D2 ready for public use. Do not describe the initial bundle as a finished no-terminal installer. Signing/notarization and a graphical installer belong to D3.

### D3: store and signed Mac distribution

- Obtain the Chrome Web Store record and verified extension public key/ID. The manifest key must keep development/release identity consistent; host registration uses one exact verified origin.
- Package the uploader in a signed, notarized Mac installer using a real Developer ID identity. Validate installer launch, signature, notarization, Native Messaging, and Keychain access on a separate Mac.
- Reduce the user instructions to the published three-step flow. Keep source-building instructions available for contributors.

Store submission, signing materials, and App registration cannot be fabricated. They are release inputs, not reasons to skip implementing the local setup and packaging work.

## Installation state and upgrades

| Location | Ownership and lifecycle |
| --- | --- |
| `~/Library/Application Support/LectureTranscripts/bin/lecture-uploader` | Installed executable; replace during a validated update |
| `~/Library/Application Support/LectureTranscripts/extension/` | Stable unpacked-extension directory until store installation is available |
| `~/Library/Application Support/LectureTranscripts/config.json` | Verified user destination; preserve on update |
| `~/Library/Application Support/LectureTranscripts/queue.sqlite3` and sidecars/lock | Durable queue and deduplication state; preserve on update |
| `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json` | Per-user host registration with an absolute binary path and one allowed origin |
| macOS Keychain records | Existing uploader-owned credentials; no plaintext fallback |
| `~/Library/Logs/LectureTranscripts/` | Existing sanitized logs; never contain transcripts or tokens |

Keep directories `0700`, config/queue/log/host-manifest files `0600`, and executable files owner executable. Repeated installation of the same version should be safe. Reject an incompatible product/protocol combination before switching the active installation. Preserve extension storage by retaining its verified ID and registration; require a backup/recovery procedure if an identity change is unavoidable.

Queue jobs currently lack a repository identity. Changing config could redirect pending jobs; completed rows could also incorrectly suppress uploads to a new destination. Do not allow a destination change while reusing that state. Before reset, report queued work and require an explicit choice to drain it to the old repository or discard it. Reset must also clear the extension's pending handoffs so they cannot be replayed into the new destination. The current popup reset clears the connection and keeps queued jobs; it is not sufficient for a destination change. Add a distinct, explicit setup/reset procedure with capture paused and confirmation that both uploader and extension handoff state were cleared before accepting a new destination. Do not implement queue migration without a separate schema/contract change.

Default uninstall removes installed executable/extension files and host registration. Preserve config, queue, credentials, and logs unless the user explicitly requests reset; explain how to remove the unpacked extension from Chrome. Reset deletes local state and credentials only. It never deletes GitHub files or repositories. Migrating an existing checkout-based installation must retain its old registration until the stable installation has been verified.

## Required maintainer inputs

| Input | Needed for |
| --- | --- |
| Public App registration, client ID, installation URL, Device Flow enabled, Contents read/write | A functional shared-App release |
| Version/tag and compatible component/protocol versions | Reproducible assets and upgrade validation |
| Clean supported Mac, Chrome, permitted Leccap access, public/private test repositories | Real installation and capture acceptance |
| Chrome Web Store account, listing, verified public key/ID | D3 published extension identity |
| Developer ID identity and securely managed notarization credentials | D3 signed Mac installer |

The public App client ID and published extension key/ID may be committed as product metadata once verified. User repository IDs, machine config, tokens, device codes, signing credentials, and private keys remain local or in managed secrets. Do not invent live values to unblock a build or claim provisioning has happened.

## Checks required before release

These are requirements for the implementation phase; writing this plan does not mean they have passed.

- Install on a supported Mac without Node, Go, Xcode, or a checkout; finish without source edits, manual JSON, or creating an App.
- Test public and private repository discovery, canceled/expired authorization, wrong account, inaccessible repository, missing `main`, and declined Keychain access. No failure leaves an active partial config or usable discovery credential.
- Confirm the extension's exact origin, binary path, product/protocol versions, and expected file permissions. Reject invalid IDs, corrupt assets, unsupported architectures, and incompatible versions before activation.
- Capture a permitted lecture and check the remote Markdown. Verify offline/restart recovery, duplicates, write-once conflicts, and that no upload can leave the verified destination.
- Reinstall/update with pending and completed queue rows. Preserve queue, credentials, config, and extension state; verify rollback after an interrupted update.
- Refuse silent repository/App changes. Verify explicit reset clears uploader state and pending extension handoffs, preserves remote files, and requires reauthorization.
- Verify uninstall preserves state by default. Check actual downloaded-bundle behavior under Gatekeeper and Keychain, then check signing/store installation separately at D3.

Add config/setup/auth unit coverage and extend `scripts/tests/run.sh` for isolated-home packaging scenarios during implementation. Real Chrome, Keychain, Leccap, and Gatekeeper checks still need a supported Mac. Record the actual release evidence in `docs/RELEASING.md` when D2 is implemented; then update README, SETUP, SECURITY, TESTING, and TROUBLESHOOTING together.

## Official references

- [GitHub Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases): versioned downloadable assets.
- [Public GitHub Apps](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/making-a-github-app-public-or-private): installation and authorization by other users.
- [GitHub App Device Flow](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app): public-client authorization and optional repository restriction.
- [Refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens): the client-secret exception for tokens obtained with Device Flow.
- [Chrome Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging): host paths and exact allowed origins.
- [Chrome manifest key](https://developer.chrome.com/docs/extensions/reference/manifest/key): maintaining a verified extension identity.
- [Apple Developer ID](https://developer.apple.com/developer-id/): signing and notarization for software distributed outside the Mac App Store.
