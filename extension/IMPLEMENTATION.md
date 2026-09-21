# Chrome Extension Implementation Plan

## Goal

Build a Manifest V3 extension that captures an explicitly opened Leccap transcript, validates it, and hands it to the local uploader. It must never talk to GitHub or store GitHub credentials.

## Current state

- `manifest.json`, popup HTML/CSS, and TypeScript source exist.
- The manifest expects generated `background.js`, `content.js`, and `popup.js`.
- No build script currently produces those files.
- The generic content coordinator exists, but there is no production bootstrap that supplies the parser, selectors, job builder, and handoff function.

## Implementation tasks

1. Read `src/IMPLEMENTATION.md` and create the missing production entrypoint(s).
2. Add `scripts/build-extension.mjs` at the repository root. It must:
   - validate and load the committed Stage 0 selector fixture;
   - bundle the background, content, and popup entrypoints with esbuild;
   - copy `manifest.json`, `popup.html`, and `popup.css`;
   - write only to `dist/extension/`;
   - leave no runtime dependency on `extension-tests/fixtures`.
3. Ensure the content bundle calls `createContentScript()` with the verified selectors, parser adapter, course mapping, and a `chrome.runtime.sendMessage` handoff.
4. Keep the manifest’s narrow Leccap host permission and existing `alarms`, `nativeMessaging`, and `storage` permissions unless the plan is deliberately revised.
5. Verify the popup bundle remains a module and that the background service worker starts only once.
6. Add a build smoke test that asserts all manifest-referenced files exist and contain no unresolved placeholders.

## Runtime invariants

- Page navigation alone never submits a job.
- A job is submitted only after transcript activation or an already-expanded populated transcript is observed.
- The content script uses the configured stable-snapshot and observation-time limits.
- Only validated transcript metadata/text crosses the Native Messaging boundary.
- The bounded extension outbox is cleared only after a definitive uploader acknowledgement.

## Done when

`npm run build` creates a loadable `dist/extension/`, Chrome accepts it as an unpacked extension, and the content script can hand off a fixture-equivalent job without credentials or raw HTML.
