# extension Architecture

## Purpose

`extension/` is the unpacked Chrome Manifest V3 surface of the project. It has three jobs:

1. Observe recognized Leccap lecture/discussion pages, activate capture for the current recording, wait for the configured completion contract, and build a validated, schema-shaped `TranscriptJob`.
2. Hand that job to the local uploader through Chrome Native Messaging, retaining it only in a bounded pending-handoff outbox until the uploader gives a definitive acknowledgement.
3. Provide the popup status/control UI and the options page (course allowlist plus `autoCapture`/`notificationsEnabled` toggles).

The directory never talks to GitHub, never holds a GitHub credential, and never owns retry or remote state. The extension's responsibilities and limits are normative in [TECHNICAL_PLAN.md](../TECHNICAL_PLAN.md); module-level architecture is in [src/ARCHITECTURE.md](src/ARCHITECTURE.md).

## Boundaries and dependencies

- **Platform**: Chrome desktop only, Manifest V3. Runtime dependencies are Chrome APIs (`storage`, `runtime`, `alarms`, `notifications`, `runtime.connectNative`) and the web platform (`DOM`, `MutationObserver`, `crypto.subtle`, `TextEncoder`). There is no frontend framework, no remote code, and no server endpoint.
- **Permissions**: the manifest requests exactly `alarms`, `nativeMessaging`, `notifications`, and `storage`. The only host permission and content-script match is `https://leccap.engin.umich.edu/*`. There is no `tabs`, `cookies`, `webRequest`, or `<all_urls>` permission.
- **Native boundary**: one Native Messaging host name, `com.neelbangera.lecturetranscripts` (`extension/src/native-messaging.ts:19`). The host manifest template allows exactly one origin, `chrome-extension://<loaded-extension-id>/`; the installer requires and validates the real 32-character extension ID and refuses a wildcard ([native-host/com.neelbangera.lecturetranscripts.json.in](../native-host/com.neelbangera.lecturetranscripts.json.in), [scripts/install-native-host.sh](../scripts/install-native-host.sh)).
- **Wire contract**: the browser-to-uploader contract lives in [protocol/](../protocol/) (`transcript-job.schema.json`, `native-messaging.schema.json`, `normalization-vectors.json`, `source-url-vectors.json`) and in the plan's normative sections. The extension implements it; it does not define it.
- **Build dependencies**: `esbuild` only, invoked by [scripts/build-extension.mjs](../scripts/build-extension.mjs). Node.js 22 LTS, TypeScript strict mode, Vitest for tests (root [package.json](../package.json), [tsconfig.json](../tsconfig.json)).

## Contracts and invariants

### Manifest surface (`extension/manifest.json`)

| Field | Value |
| --- | --- |
| `manifest_version` | 3 |
| `name` / `description` | `Lecture Transcripts` / captures activated Leccap transcripts for the local uploader |
| `version` | `0.1.0` in source; the build rewrites it from `package.json` |
| `icons` / `action.default_icon` | `icons/icon128.png` (the only icon size; the build asserts it exists and starts with the PNG signature) |
| `permissions` | `alarms`, `nativeMessaging`, `notifications`, `storage` |
| `host_permissions` | `https://leccap.engin.umich.edu/*` |
| `background` | `background.js`, `type: "module"` |
| `action` | `default_title` `Lecture Transcripts`, `default_popup` `popup.html` |
| `options_ui` | `options.html`, `open_in_tab: true` |
| `content_scripts` | `content.js` on `https://leccap.engin.umich.edu/*`, `run_at: document_idle` |

### Build contract (`scripts/build-extension.mjs`)

The build is the only supported way to produce a loadable extension. It:

- reads and validates `extension-tests/fixtures/lecture-page.selectors.json` and fails on a missing file, invalid JSON, or a missing required selector field (`transcriptButtonSelector`, `transcriptContainerSelector`, `timestampFormat`, the three capture objects, `lectureDateSource.dateSelector`/`dateRegex`, `completionIndicator.selector`/`mode`, `loadingTextMarkers`, positive `stabilityDebounceMs`/`sanityMinChars`);
- reads `extension-tests/fixtures/course-mapping.json` and fails when `courseMappings` is missing or empty;
- bundles four entry points into `dist/extension/`: `content.js` (IIFE, with the selectors inlined as the compile-time define `__STAGE0_SELECTORS__`), `background.js` (ESM), `popup.js` (ESM), `options.js` (ESM);
- copies `popup.html`, `popup.css`, `options.html`, `options.css`, and `icons/`;
- rewrites `manifest.json` with the `package.json` version.

The built extension never reads a fixture path at runtime. Output inventory: `dist/extension/{manifest.json, background.js, content.js, popup.js, options.js, popup.html, popup.css, options.html, options.css, icons/icon128.png}`. `dist/` is gitignored.

### How the pieces are loaded

- **Service worker**: `background.js` is the single MV3 worker. Its tail (`extension/src/background.ts:595-599`) constructs and starts `BackgroundCoordinator` only when `chrome.runtime.onMessage` and `chrome.alarms` exist, so it installs listeners once. `onInstalled` recreates the one-minute `lecture-transcripts-drain` alarm; `onStartup` recreates it and runs a drain cycle.
- **Content script**: `content.js` is an IIFE injected at `document_idle` on Leccap pages. Its tail calls `installContentRuntime()` (`extension/src/content.ts:738-755`), which returns without touching the page when `document`, `chrome`, or the embedded `__STAGE0_SELECTORS__` define is unavailable, and otherwise installs exactly one coordinator per page context.
- **Popup**: `popup.html` loads `popup.js` as a module; it only sends extension-internal messages to the background.
- **Options**: `options.html` loads `options.js` as a module; the popup header exposes a Settings button that calls `chrome.runtime.openOptionsPage()` and is hidden when the API is unavailable (`extension/src/settings-actions.ts`).

### Security posture

- **No GitHub credentials.** The extension never contains or receives an access token, refresh token, client secret, or App private key. It sees only closed auth states and the device-flow `userCode`/verification URIs needed to finish authorization ([docs/SECURITY.md](../docs/SECURITY.md#no-github-credentials-in-the-extension)). The popup renders a verification link only when it is `https:` on `github.com`/`www.github.com` (`extension/src/popup.ts:109-117`).
- **No broad page capture.** Only the extracted transcript forms plus selected metadata cross the boundary; cookies, headers, raw HTML, and unrelated page text are never sent. The content script's only network request is the same-origin linked-overview fetch on Leccap (with the hidden-iframe fallback), and the source URL is canonicalized before it enters a job.
- **Sender and origin validation.** The background rejects malformed messages and messages not from this extension, and restricts `capture_job` senders to a Leccap URL (`sender.url` or `sender.tab.url`) (`extension/src/background.ts:142-167`). The native host independently validates the Chrome-supplied origin against the one allowed `chrome-extension://<id>/` origin before reading a frame.
- **No secrets in extension storage.** `chrome.storage.local` holds only the bounded outbox, metadata-only overflow notices, the last uploader status snapshot, the course allowlist, and the two behavior toggles.

## Data flow (capture path, storage, messaging)

### Capture path

1. A recognized lecture/discussion page loads (or its URL changes in-page). With `autoCapture` true (default), the coordinator opens the transcript control itself when it is closed and captures directly when a visible, populated transcript is already open; a manual **Show Transcript** click always activates capture even when `autoCapture` is false (`extension/src/content.ts`).
2. The coordinator attaches one `MutationObserver` scoped to the transcript container subtree and waits for the configured completion indicator, populated rows, and a clear loading region.
3. It takes normalized snapshots separated by the configured no-mutation debounce (1500 ms), requiring two equal content hashes before promoting a stable read; mutations restart the quiet window, and a 30 s overall deadline produces `not_ready`.
4. `content-runtime`'s parser adapter re-parses the stable page (metadata, lecture date from the linked overview, discussion kind/section), builds the job via `createTranscriptJob`, and hands it to the background with `chrome.runtime.sendMessage({ type: "capture_job", job })`.
5. The background writes the job to the bounded outbox, runs a drain cycle (connect → first status page → replay pending handoffs), and returns `queued`/`already_queued` only when the outbox is empty; otherwise it reports `waiting_for_uploader`. A rejection clears the full copy only after the metadata-only notice is stored; a full outbox yields `rejected_handoff_full`.

### Storage keys (`chrome.storage.local`)

| Key | Owner | Contents |
| --- | --- | --- |
| `lectureTranscriptsExtensionState` | background / `extension-storage.ts` | `pendingHandoffs` (max 3 full jobs), `overflowNotices` (max 20, metadata only), `lastStatus`, `lastOutcome`, `updatedAt` |
| `courseMappings` | options / `course-storage.ts` | validated allowlist array written by the options page |
| `autoCapture` | options / `settings-storage.ts` | boolean, default true |
| `notificationsEnabled` | options / `settings-storage.ts` | boolean, default true |

### Messaging

**Extension-internal messages** (`extension/src/background.ts:81-88`), sent from the content runtime or popup:

| Message | Fields | Handled by |
| --- | --- | --- |
| `capture_job` | `job` (validated `TranscriptJob`) | outbox write + drain; Leccap sender required |
| `popup_snapshot` | none | returns stored `ExtensionSnapshot` |
| `popup_connect` | none | runs a drain cycle |
| `popup_reset` | none | uploader `reset` command, clears stored status |
| `popup_status` | optional `beforeJobId` | paginated `status_request` relay |
| `popup_retry` | `jobId` | uploader `retry_job` relay |
| `popup_discard` | `jobId` | uploader `discard_job` relay |

The background replies with `{ ok, snapshot?, errorCategory?, status?, message? }`.

**Native Messaging wire** (requests built in `extension/src/native-messaging.ts`): `connect`, `submit_job`, `status_request`, `retry_job`, `discard_job`, `reset`; responses `ack`, `command_result`, `status`, `error`. Envelopes carry `protocolVersion: 1` and a 1–64 printable-ASCII `requestId`; `status` pages are capped at 50 `JobSummary` objects; a transcript travels only inside `submit_job.job`. The exact contract is [TECHNICAL_PLAN.md § Canonical Native Messaging contract](../TECHNICAL_PLAN.md#canonical-native-messaging-contract) and [protocol/native-messaging.schema.json](../protocol/native-messaging.schema.json).

### Terminal outcomes

The uploader owns queue and GitHub state. The extension surfaces `uploaded`/`unchanged` and every `rejected_*`/`permanent_conflict` terminal outcome as a best-effort desktop notification (title `Lecture uploaded` or `Upload failed`, message `${statusLabel} · ${lectureKey}`), never for `queued`, `uploading`, `retryable_error`, or waiting/auth states. The popup displays paginated queue rows, conflict/retry/discard actions, overflow recapture notices, and both build versions.

## File responsibilities

| File | Role | Key exports / surface |
| --- | --- | --- |
| `manifest.json` | MV3 declaration: permissions, host allowlist, action, options UI, content script, icon | n/a |
| `popup.html` | Popup shell: connection summary, authorization card, actions, pending handoffs, overflow list, queue list, versions, Settings button | element IDs consumed by `popup.ts` |
| `popup.css` | Popup styling (light/dark custom properties, cards, pills, notices) | n/a |
| `options.html` | Options page shell: behavior toggles and course allowlist editor | element IDs consumed by `options.ts` |
| `options.css` | Options-page additions on top of `popup.css` (course cards, term chips, error list) | n/a |
| `icons/icon128.png` | The only manifest-referenced icon (128 px), also the notification icon | n/a |
| `src/content.ts` | Content-script entry and page coordinator; installs the production pipeline at load | `createContentScript`, `installContentRuntime`, `PageCaptureStatus`, constants |
| `src/background.ts` | Service-worker entry and coordinator: validation, alarm, outbox, drain/replay, status relay, notifications | `BackgroundCoordinator`, `DRAIN_ALARM_NAME`, `ExtensionMessage` |
| `src/popup.ts` | Popup behavior: snapshot/status rendering, connect/reset/retry/discard, Clear uploaded, Settings | entry side effects only |
| `src/options.ts` | Options behavior: allowlist editing/saving and immediate behavior-toggle persistence | entry side effects only |
| `src/` (other modules) | Parser, normalizer, job builder, runtime adapter, storage, native client, status vocabulary, settings/course storage | see [src/ARCHITECTURE.md](src/ARCHITECTURE.md) |
| `IMPLEMENTATION.md` | Historical extension-level implementation plan | documentation only |

## Testing and verification

- `npm run typecheck` (`tsc --noEmit`) covers `extension/**/*.ts`, `extension-tests/**/*.ts`, and `vitest.config.ts` in strict mode.
- `npm test` (`vitest run`) runs the suites in `extension-tests/`, which exercise this directory: parser fixtures, normalization vectors, job validation, content activation/observation, runtime handoff, outbox capacity/replay, background drain/acknowledgement/notifications, native client correlation, popup rendering, options dirty-state/save, settings storage, and the Stage 0 fixture packet.
- `extension-tests/build-smoke.test.ts` runs `scripts/build-extension.mjs` and asserts every manifest-referenced file exists, the icon is a PNG, no HTML asset reference is dangling, the selector placeholder `__STAGE0_SELECTORS__` is resolved, the content bundle contains the verified selectors and `capture_job`, the options bundle contains `autoCapture`/`notificationsEnabled`, and the popup bundle contains `openOptionsPage`.
- `npm run build` produces the loadable `dist/extension/`; Chrome accepts it as an unpacked extension.
- Packaging and native-host install checks live in `scripts/tests/run.sh` (temporary `HOME`, stubbed `security`); they verify the host-manifest rendering rules rather than the extension bundles.

## Related plan sections

- [Phase 1 guardrails](../TECHNICAL_PLAN.md#phase-1-guardrails) — activation, handoff, credential, and write-once boundaries.
- [Canonical Native Messaging contract](../TECHNICAL_PLAN.md#canonical-native-messaging-contract) — the exact envelopes and status fields the extension builds and validates.
- [Canonical transcript-job schema](../TECHNICAL_PLAN.md#canonical-transcript-job-schema) — the only payload shape that may cross the boundary.
- [Canonical status vocabulary](../TECHNICAL_PLAN.md#canonical-status-vocabulary) — the single status/label vocabulary implemented in `src/status.ts`.
- [Stage 3 — Implement the extension runtime and handoff](../TECHNICAL_PLAN.md#stage-3--implement-the-extension-runtime-and-handoff) — manifest permissions, alarm, outbox, popup, and options requirements.
- [Stage 7 — Package, install, and document the personal deployment](../TECHNICAL_PLAN.md#stage-7--package-install-and-document-the-personal-deployment) — build-time selector embedding and extension-ID/host-manifest rules.
- [Required provisioning packet](../TECHNICAL_PLAN.md#required-provisioning-packet) — why no account identifiers or secrets are committed here.
- [docs/SECURITY.md](../docs/SECURITY.md) — owner-facing security boundary.
- [PHASE_1_DECISIONS.md](../PHASE_1_DECISIONS.md) — rationale, including build-time selector embedding and alarm-driven wakeup.

## How to change this directory safely

1. Treat `TECHNICAL_PLAN.md` and `protocol/` as the authority. A new permission, host, message field, status, or limit requires a plan/schema edit first, not a local workaround.
2. Keep `manifest.json`, the build script's copied/bundled file list, and the smoke test in sync. Adding a manifest-referenced file without adding it to the build breaks `build-smoke.test.ts`.
3. Never add GitHub credentials, tokens, cookies, or arbitrary page data to any source file, storage key, or message. Auth data is display-only and limited to the documented states and device-flow fields.
4. New page shapes require a verified Stage 0 selector fixture and a deliberate path/identity decision; do not hand-edit selectors into production code ([PHASE_1_DECISIONS.md § Build-time selector embedding](../PHASE_1_DECISIONS.md#21-build-time-selector-embedding-instead-of-runtime-fixture-reads)).
5. Change status strings and labels only in `src/status.ts`; the popup, content runtime, and background all consume that mapping.
6. Keep the bounded outbox semantics: write before submit, clear only after a definitive ack plus any required metadata notice, never evict an older pending job, and never claim a job is queued when it is not.
7. Bump the version in `package.json` (the build injects it) and re-run `npm run typecheck`, `npm test`, and `npm run build`. Do not commit `dist/`.

## Open questions

Items that could not be fully verified from the code or that deviate from a document; none were changed by this doc.

- `extension/src/IMPLEMENTATION.md` and `extension/IMPLEMENTATION.md` are stale in their "Current blockers"/"Current state" lists: they claim missing `crypto.subtle` typings, no snapshot/build-job adapter, no production content entry, no embedded selectors, and no build script. All of those exist now (`extractTranscriptSnapshot`, `createContentRuntimeParser`, `installContentRuntime`, `__STAGE0_SELECTORS__`, `scripts/build-extension.mjs`).
- The plan does not define rules for browser-console diagnostics. `content-runtime.ts` logs a redacted overview-fetch line that includes the fetched page `<title>` (up to 80 characters) and shape booleans; this is not covered by the uploader log-redaction contract.
- `MAX_NATIVE_MESSAGE_BYTES` is defined in `transcript-job.ts` and `native-messaging.ts` but is not enforced anywhere in the extension at runtime; Chrome owns frame framing, and the uploader enforces the 1,048,576-byte frame cap.
- `skipped_section` (and the `preferredDiscussionSection` course field that produces it) appears in code and options UI but is not named in the plan's canonical status vocabulary table or in `PHASE_1_DECISIONS.md`.
