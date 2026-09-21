# Repository Implementation Plan

Status: partial implementation; the capture-side TypeScript and Native Messaging contract exist, but the extension bundle and most of the Go uploader are not complete.

This is the entry point for an implementation agent. Read this file first, then read the `IMPLEMENTATION.md` in the directory being changed. The normative product and wire contracts remain in `TECHNICAL_PLAN.md`; these directory plans explain how to finish the current tree without inventing a second design.

## Current baseline

- Git branch: `main`; keep unrelated user changes intact.
- Stage 0 fixtures are committed for `EECS 484 / 2026-fall`.
- The Stage 0 render-time fields are still pending live measurement.
- Machine-local GitHub provisioning is intentionally outside the repository.
- `npm test` currently cannot collect the parser suite because `jsdom` is missing.
- `npm run typecheck` currently fails on missing Node/jsdom types and parser typing errors.
- `npm run build` currently fails because `scripts/build-extension.mjs` does not exist.
- `go test ./...` only covers the currently existing host/protocol packages; both report no test files.

## Execution order

1. **Restore the development baseline.** Add the missing test dependencies, fix TypeScript errors, and add the extension build entrypoint.
2. **Make the extension runnable.** Add the production content bootstrap, embed verified selectors/course configuration at build time, and produce `dist/extension/`.
3. **Lock the shared contracts.** Add cross-language protocol/vector tests before implementing the uploader.
4. **Complete the uploader foundation.** Implement config, SQLite queue, and the executable Native Messaging host.
5. **Complete publishing.** Implement Keychain/device-flow auth, GitHub Contents writes, Markdown rendering, retry, and the serial processor.
6. **Package and verify.** Add host-install/build scripts, documentation, fake end-to-end tests, then perform the real machine-local setup.

Do not broaden the course allowlist, change status names, relax byte limits, upload on page visit, or add credentials to the extension without first updating the authoritative plan and decision log.

## Global acceptance criteria

- `npm ci && npm run typecheck && npm test && npm run build` succeeds.
- `go test ./...` includes meaningful tests for every uploader package.
- The unpacked extension loads and only captures after transcript activation.
- The extension sends only a validated transcript job through Native Messaging.
- The uploader survives restart, deduplicates jobs, retries transient failures, and never overwrites a different remote lecture file.
- GitHub credentials remain in macOS Keychain and never enter extension storage, logs, or protocol payloads.
- The real end-to-end setup is blocked until the render-time measurement, repository/App provisioning, and loaded extension ID are verified.

## Agent workflow

For each task:

1. Read the local directory plan and the relevant section of `TECHNICAL_PLAN.md`.
2. Inspect existing code/tests before editing.
3. Make the smallest contract-preserving change.
4. Add or update tests in the nearest test directory.
5. Run the local checks listed in that directory plan and then the global checks above.
6. Update the relevant status/documentation only when the implementation actually changed.
