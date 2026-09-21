# Extension Source Implementation Plan

## Goal

Finish and wire the existing browser-side modules without changing the Phase 1 contracts.

## File responsibilities

- `course-config.ts`: explicit course/term allowlist; no page-derived fallback.
- `leccap-parser.ts`: verified page classification, metadata extraction, overview-date correlation, transcript extraction, source URL canonicalization, and hash creation.
- `transcript-normalizer.ts`: deterministic NFC/line-ending/whitespace normalization and timestamp handling.
- `transcript-job.ts`: canonical job construction, identity/path derivation, size validation, and transport serialization.
- `content.ts`: activation detection, scoped mutation observation, stable reads, timeout, and handoff status.
- `extension-storage.ts`: bounded pending-handoff outbox and metadata-only notices in `chrome.storage.local`.
- `native-messaging.ts`: closed request/response vocabulary, validation, correlation, reconnectable client.
- `background.ts`: service-worker coordinator, alarm drain, replay, acknowledgement handling, and popup relay.
- `status.ts`: shared status/error vocabulary and user labels.
- `popup.ts`: status/authorization/queue UI; no credential handling.

## Current blockers

1. `leccap-parser.ts` has strict TypeScript errors around `crypto.subtle`, optional metadata, and discriminated-union narrowing.
2. The parser’s all-in-one `parseLecturePage()` API does not directly satisfy the snapshot/build-job adapter expected by `content.ts`.
3. No production content entry calls `createContentScript()`.
4. Selectors are available in tests but are not yet embedded into a production bundle.

## Implementation sequence

1. Fix types without weakening `strict` mode. Use explicit type guards and validated local variables; do not cast rejected results into parsed results.
2. Extract or add parser functions for:
   - normalized transcript snapshot and content hash;
   - final metadata/date/job construction after the second stable snapshot.
3. Add a production selector/config module generated or embedded by the build step.
4. Add a content entry adapter that:
   - calls the parser snapshot function;
   - calls final job construction with `capturedAt`;
   - sends `{ type: "capture_job", job }` to the background worker.
5. Verify service-worker message validation rejects wrong senders and non-Leccap URLs.
6. Verify storage operations remain serialized and never evict older pending handoffs or overflow notices.
7. Verify every Native Messaging response is runtime-validated before use.

## Tests required

- Parser fixture, loading page, unrelated page, missing number, unmapped course, unsafe URL, malformed timestamp, and overview correlation.
- Content activation/no-activation, mutation reset, stable snapshot, timeout, and handoff failure.
- Background replay, duplicate acknowledgement, rejection notice, alarm cycle, and popup command handling.
- Storage capacity and restart persistence.

## Done when

All source files typecheck, the production content entry is wired, and the browser-side tests prove activation, validation, persistence, and handoff behavior.
