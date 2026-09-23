# extension/src Architecture

## Purpose

`extension/src/` is the TypeScript implementation of the browser side of the plan: page classification and metadata extraction, transcript normalization and framed hashing, canonical `TranscriptJob` construction/validation, activation and stability observation, the bounded pending-handoff outbox, the Native Messaging client, the MV3 service-worker coordinator, the shared status vocabulary, settings/course storage, and the popup/options behavior. Every module is built from this directory into `dist/extension/` by [scripts/build-extension.mjs](../../scripts/build-extension.mjs); the wire contract it must satisfy lives in [protocol/](../../protocol/) and [TECHNICAL_PLAN.md](../../TECHNICAL_PLAN.md).

## Boundaries and dependencies

### Module graph

```
entry bundles:  content.ts (IIFE)   background.ts (ESM)   popup.ts (ESM)   options.ts (ESM)

course-config.ts        (no imports)
course-storage.ts       -> course-config
settings-storage.ts     (no imports)
settings-actions.ts     (no imports)
status.ts               (no imports)
job-actions.ts          -> status
transcript-normalizer.ts(no imports)
transcript-job.ts       -> course-config, transcript-normalizer
leccap-parser.ts        (no imports; reads embedded selector config passed by callers)
content.ts              -> content-runtime, settings-storage, (type) leccap-parser
content-runtime.ts      -> course-storage, leccap-parser, transcript-job,
                           (type) background, (type) content, (type) course-config
extension-storage.ts    -> (type) native-messaging, status
native-messaging.ts     -> status, transcript-job
background.ts           -> native-messaging, extension-storage, settings-storage, status
popup.ts                -> job-actions, settings-actions, status, (type) background
options.ts              -> course-storage, settings-storage, (type) course-config
```

- No module imports a runtime dependency outside the web/Chrome platform. All npm packages are dev-time (TypeScript, esbuild, Vitest, jsdom, type packages).
- `content.ts` and `content-runtime.ts` reference each other, but `content-runtime`'s imports of `background`/`content`/`course-config` types are type-only and erased at build time; there is no runtime import cycle.
- `background.ts` bootstraps only when `chrome.runtime.onMessage` and `chrome.alarms` exist; `content.ts` bootstraps only when `document`, `chrome`, and the build-time `__STAGE0_SELECTORS__` define exist. Both are safe to import in tests.
- `popup.ts` and `options.ts` are DOM entry scripts: they resolve their elements at module load and have no exports.

### External contracts

- The browser-to-uploader envelopes and limits are normative in [TECHNICAL_PLAN.md § Canonical Native Messaging contract](../../TECHNICAL_PLAN.md#canonical-native-messaging-contract) and [protocol/native-messaging.schema.json](../../protocol/native-messaging.schema.json).
- The job schema is normative in [TECHNICAL_PLAN.md § Canonical transcript-job schema](../../TECHNICAL_PLAN.md#canonical-transcript-job-schema) and [protocol/transcript-job.schema.json](../../protocol/transcript-job.schema.json).
- Normalization and URL vectors are shared with Go in [protocol/normalization-vectors.json](../../protocol/normalization-vectors.json) and [protocol/source-url-vectors.json](../../protocol/source-url-vectors.json).

## Contracts and invariants

### Identity and allowlist — `course-config.ts`, `course-storage.ts`

- `COURSE_MAPPINGS` is a frozen, explicit allowlist, currently EECS 484 and EECS 491, both only `2026-fall`. It is an exact copy of the Stage 0 fixture's list; it is never derived from page text. `assertCourseMappings()` runs at module load and throws `CourseConfigError` on an empty label, non-path-safe slug, slug/name mismatch, empty or duplicate terms, or two course names sharing one slug (`slug_collision`).
- `normalizeCourseLabel` only NFC-normalizes and collapses presentation whitespace. `normalizeCourseSlug` lowercases and strips non-alphanumerics, then requires `^[a-z0-9]+$`. `parseTerm` accepts `^(Winter|Spring|Summer|Fall)\s+(\d{4})$` (case-insensitive) or `^(\d{4})-(winter|spring|summer|fall)$` and emits both normalized and human forms; there is no `spring-summer` value.
- `getCourseMapping(courseText, term)` returns the one exact allowlisted pair or throws `unsupported_course` / `unsupported_term`. `matchesCourseIdentity` additionally requires the canonical name and slug to match.
- Stored allowlist key: `courseMappings` (`COURSE_MAPPINGS_STORAGE_KEY`). `validateCourseMappings` enforces: non-empty `pageCourseText`/`courseName` without newlines, `courseName` ≤ 256 characters, `courseSlug` matching `^[a-z0-9]+$`, non-empty unique `supportedTerms` matching `^\d{4}-(winter|spring|summer|fall)$`, optional `preferredDiscussionSection` empty or `^\d{3}$`, and no duplicate normalized page label, slug, or slug/term pair. Invalid data is never written.
- `loadCourseMappings` returns stored mappings only when present and valid; otherwise it falls back to the built-in `COURSE_MAPPINGS` (including when `chrome.storage.local` is unavailable, as under Vitest).

### Page parser — `leccap-parser.ts`

The parser is pure: it receives `SelectorFixture`, course mappings, an optional `sourceUrl`, and an optional `fetchOverview` function. It never reads files and never discovers selectors at runtime. Its three transcript row selectors are fixed constants (`.transcript-row`, `.transcript-time`, `.transcript-text`), matching the Stage 0 observation.

**Rejection statuses**: `not_ready | rejected_missing_identity | rejected_ambiguous_metadata | rejected_unsafe_url`. (The content runtime adds `skipped_section`, `rejected_oversized`, and `rejected_invalid_hash` for later stages; see below.)

**Source URL**: `canonicalizeLeccapUrl` accepts only `https`, host `leccap.engin.umich.edu`, no userinfo, and no port or `:443`; it drops query/fragment, preserves the encoded path and trailing slash, normalizes an empty path to `/`, and returns `https://leccap.engin.umich.edu<path>` or `null`. `parseLecturePage` rejects a non-canonicalizable URL as `rejected_unsafe_url` before any metadata or overview work.

**Identity** (`extractCourseIdentity`, `resolveNumericIdentity`, `identity-derivation.ts`):
- Course and term come from `selectors.courseSelector`/`termSelector`; a missing course or term element is `rejected_missing_identity`.
- The season must be one of `winter|spring|summer|fall`; anything else (including a literal `Spring/Summer`) is `rejected_ambiguous_metadata`.
- The page course/term pair must match exactly one mapping entry, and that entry must have a path-safe slug and newline-free name; otherwise `rejected_ambiguous_metadata`.
- Lecture number comes from the recording title when it carries one: a leading numeric prefix (`lectureNumberSelector`), `Lecture: N` / `Lecture N`, `Discussion N`, or `Discussion: N` (`parseTitleIdentity`). A lagging title (`Lecture recorded on ...` / `Discussion recorded on ...`) takes its number from the already-inventoried overview sequence by "next from the last one" (`deriveIdentityFromOverview`): same-kind cards are ordered by rec-date then rec-time and walked with `next = 1`; an explicit number `N` requires `N >= next` and advances `next` to `N + 1`; an unnumbered card takes `next`. An unrecognized title (decoy such as `DISREGARD -- Empty discussion`), a contradictory explicit number, a missing lecture start time, or lecture-kind start-time drift is `rejected_ambiguous_metadata` with `lectureNumber: null`. The overview category badge is never an identity input; it only classifies cards (`Lecture - *` / `Discussion - *`) and supplies the discussion section. A lagging page title with an already-updated overview card title uses the card's explicit number. `ParsedLecture.numberSource` records `"title"` vs `"derived"` for correction tracking.
- The result is `{ kind, courseName, courseSlug, term, lectureNumber }`, with `lectureNumber` in 1–999.

**Completion** (`completionIsReady`): the fixture's completion indicator must match (mode `present`, `attribute_equals`, or `text_matches`), the loading/error region must be clear when `loadingIndicatorSelector` is non-null (a `null` selector is vacuous), and, when `populationSelector` is set, at least one matching element inside the transcript container must have non-empty text.

**Transcript extraction** (`extractTranscript`, also exposed for the observer through `extractTranscriptSnapshot`):
- No rendered rows or no populated row text is `not_ready`.
- Rows mixing timestamped and plain-only shapes are `rejected_ambiguous_metadata`.
- A timestamped row whose `.transcript-time` does not match `^\d{1,2}:\d{2}(?::\d{2})?$` is `rejected_ambiguous_metadata`.
- The plain form is the normalized row texts; an exact whole-transcript loading marker (`loading…|loading transcript|no transcript`, case-insensitive) or fewer than `sanityMinChars` (50) non-whitespace characters is `not_ready`.
- Plain-only rows produce `timestampedTranscript: ""` and `derivedFrom: "plain-only"`. Timestamped rows are serialized one line per row as `[<verbatim .transcript-time>] <text>` and produce `derivedFrom: "both"`. The type permits `"timestamped-only"`, but this function never emits it; derivation from a timestamped-only source exists in `transcript-normalizer.ts` for callers that need it.
- The parser normalizes with its own private `normalizeTranscript` and hashes with `hashTranscriptForms` (framed `transcript-hash-v1\0`, byte lengths, `crypto.subtle` SHA-256). These duplicate the rules in `transcript-normalizer.ts`; the two implementations must stay identical.

**Recording date** (`resolveOverview`, `parseRecordingDate`, `parseRecordingTime`):
- The selected policy is `linked_overview_page` + `on_demand_fetch`. A `lecture_page` source reads `dateSelector` directly; a non-executable policy, missing overview link, unsafe link/player URL, failed or non-OK fetch, unreadable body, missing DOM parser, zero or multiple card matches, missing date element, or invalid date all fail as `rejected_ambiguous_metadata`. The same overview fetch also inventories every recording card (`title`, rec-date, rec-time, badge category/section, canonical href) for number derivation; the rec-date time tail (`• h:MM AM/PM`) is parsed as the start time.
- The fetch uses `credentials: "include"`, `cache: "no-store"`, and `Accept: text/html,application/xhtml+xml`, shaped like a document navigation so the Leccap session is carried. The response is parsed with `DOMParser`; the card set is `recordingCardSelector`, and exactly one card's `recordingLinkSelector` href must canonicalize to the canonical current player URL.
- `parseRecordingDate` accepts month-first `M/D/YYYY` (validated as a real calendar date) and unambiguous textual month/day (`Feb 12`, `Feb 12, 2027`), using the already-validated term year when the year is absent. Yearless numeric dates and invalid dates are rejected.
- A sign-in page in the overview response is reported with a session-specific `rejected_ambiguous_metadata` reason.
- The correlated card's `.badge` is retained only when it matches `^\s*Discussion\s*-\s*(\d{3})\s*$`; this becomes `discussionSection` (a section label, never an identity field). For lectures it is always `null`.

**`parseLecturePage` order**: canonicalize URL → require the transcript container (or classify the page as `not_ready` when a known header exists, else `rejected_missing_identity`) → require completion → require `stableSnapshotCount >= 2` (default 2) → course/term identity → transcript → overview (date, rec-time, card inventory) → kind + number (title explicit | overview-derived) → framed hash. A success returns `ParsedLecture` with `supported: true`, `completion: "complete"`, both transcript forms, `derivedFrom`, `lectureKey` (`<slug>/<term>/<NNN>`), `contentHash`, `stableSnapshotCount`, `discussionSection`, and `numberSource`.

### Normalization and hashing — `transcript-normalizer.ts`

- `normalizeTranscript` applies the plan's ordered rules: NFC, CRLF/CR→LF, detect a timestamp prefix with the normative regex (source exported as `TIMESTAMP_PREFIX_SOURCE`/`TIMESTAMP_PREFIX_RE`), preserve prefix digits verbatim, strip trailing spaces/tabs from the body, collapse runs of 2+ spaces/tabs to one space, rejoin prefix and body with one space (prefix alone when the body is empty), collapse 3+ newlines to 2, and trim leading/trailing newlines. It is idempotent.
- `normalizeTranscriptForms` applies the derivation policy: both forms supplied → `both`; timestamped only → `derivePlainTranscript` (strip one prefix per line, then normalize) → `timestamped-only`; plain only → `timestampedTranscript: ""` → `plain-only`. The wire job always carries both string fields.
- `frameTranscriptHashInput` builds `ASCII("transcript-hash-v1\0") + len: + transcript + len: + timestamped` as UTF-8; `computeNormalizedContentHash` hashes it with a browser-safe synchronous SHA-256 (`sha256Bytes`/`sha256Hex`) so job creation works without Node's crypto.
- `stripTimestampPrefix` is the single-prefix removal helper used for derivation.

### Job construction and validation — `transcript-job.ts`

- **Wire field order**: `schemaVersion, kind, lectureKey, courseSlug, courseName, term, lectureNumber, lectureDate, sourceUrl, capturedAt, transcript, timestampedTranscript, contentHash`. `canonicalJobObject` reconstructs in that order so serialization is deterministic.
- **Constants**: `TRANSCRIPT_JOB_SCHEMA_VERSION = 1`; `MAX_TRANSCRIPT_BYTES = 450 * 1024` (460800) per transcript field; `MAX_SERIALIZED_JOB_BYTES = 950 * 1024` (972800); `MAX_SOURCE_URL_BYTES = 2048`; `MAX_FIELD_CHARACTERS` = lectureKey 128, courseSlug 64, courseName 256, term 32, lectureDate 10, capturedAt 20, contentHash 64; `MAX_NATIVE_MESSAGE_BYTES = 1024 * 1024` (declared, not enforced here).
- **Validation sequence** (`validateTranscriptJob`): object check → unknown field → missing field → `schemaVersion === 1` → `kind` in `lecture|discussion` → string field types → integer `lectureNumber` in 1–999 → character caps for lectureKey/courseSlug/courseName/term/contentHash → `canonicalizeSourceUrl` and require the value already canonical → exact allowlist mapping (canonical name/slug, normalized term) → lectureKey pattern and derivation match → real calendar `lectureDate` (`YYYY-MM-DD`) → whole-second `capturedAt` matching `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` → both transcript forms already normalized → UTF-8 byte caps → ≥ 50 non-whitespace characters → not an exact loading marker → `contentHash` matches `^[0-9a-f]{64}$` and the framed recomputation → serialized-job byte cap. The declared rejection-code union is `rejected_missing_identity | rejected_ambiguous_metadata | rejected_oversized | rejected_invalid_hash | rejected_unsafe_url | rejected_unknown_field | rejected_invalid_schema`; this validator itself emits `rejected_unknown_field`, `rejected_invalid_schema`, `rejected_oversized`, `rejected_unsafe_url`, and `rejected_invalid_hash` (the parser produces the missing-identity/ambiguous-metadata codes earlier).
- **URL handling**: `canonicalizeSourceUrl` throws `SourceUrlError` with codes `invalid_absolute_url`, `unsafe_scheme`, `unsafe_host`, `userinfo_not_allowed`, `port_not_allowed`, `too_long`. `sanitizeSourceUrlForPublish` omits the URL when a lowercased path segment split on `/._-` is exactly `token`, `session`, `auth`, or `sid`; `sourceUrlInfo` returns `{ canonicalUrl, publishUrl, logValue }` where `logValue` is `host + path` only.
- **Builders**: `deriveLectureKey(slug, term, number)` returns `<slug>/<normalized-term>/<NNN>` and rejects a non-path-safe slug or out-of-range number. `createTranscriptJob` (alias `buildTranscriptJob`) resolves the mapping, normalizes term/forms, canonicalizes the URL, stamps whole-second UTC `capturedAt`, derives the key, and runs `assertValidTranscriptJob`. `serializeTranscriptJob` emits canonical compact JSON; `transcriptJobByteLength` measures UTF-8 bytes.
- **Legacy path helpers (deviation)**: `deriveStableLecturePath` returns `<slug>/lectures/<NNN>.md` and `deriveTimestampedLecturePath` returns `<slug>/timestamped/<NNN>.md` regardless of kind. These contradict the plan's lecture paths (`<slug>/<NNN>.md`) and the uploader's `queue.TargetPath`/`TimestampedPath`, which handle lecture vs discussion and place lectures directly under the slug. The helpers are exported and asserted only by `extension-tests/transcript-job.test.ts`; production code never calls them.

### Activation and observation — `content.ts`

- Constants: `STABILITY_DEBOUNCE_MS = 1500`, `OBSERVATION_TIMEOUT_MS = 30_000`, `URL_POLL_INTERVAL_MS = 1000`, `AUTO_ACTIVATE_RETRY_MS = 500`.
- `PageCaptureStatus`: `idle`, `activated`, `waiting_for_transcript`, `ready`, `handoff_pending`, `not_ready`, `skipped_section`, and the terminal `rejected_missing_identity`, `rejected_ambiguous_metadata`, `rejected_oversized`, `rejected_invalid_hash`, `rejected_unsafe_url`. `TERMINAL_PARSER_REJECTIONS` contains `skipped_section` and the five `rejected_*` values; any other parser status is surfaced as `not_ready`.
- `ContentScriptDependencies` keeps the coordinator independent of concrete parser/job/handoff implementations: `selectors`, `parser.snapshot`, `parser.buildJob`, `handoff`, optional `onStatus`, `autoActivateOnLoad`, clock, document, and window.
- **Activation**: with `autoActivateOnLoad` true (from stored `autoCapture`), a page load or in-page URL change retries every 500 ms until either a visible, populated transcript with the open-state title `Hide Transcript` is observed (capture directly) or the transcript control appears with title `Show Transcript` (set `pendingAutoOpen`, click it, and let the normal click listener start the run) or a 30 s deadline expires. With auto-capture off, only a manual `Show Transcript` click starts a capture; an already-open transcript is left alone. A `Hide Transcript` click cancels an unfinished run that has not started its handoff.
- **Observer**: one `MutationObserver` per run, attached only to the transcript container with `subtree: true, childList: true, characterData: true, attributes: true`. It never observes `document` or the player subtree. If the container is absent, attachment retries every 100 ms until the deadline.
- **Stability**: after completion is ready, snapshots are taken after each quiet debounce. The first snapshot is retained; a hash change discards it and restarts the quiet window; the second equal hash promotes `ready`, records `stableSnapshotCount`, and starts `buildJob` + handoff. A non-`ok` snapshot or job result ends the run: terminal statuses are surfaced as-is, and any other status (including `not_ready`) becomes `not_ready`. In production the adapter never returns a non-`ok` snapshot for a not-ready extraction (it returns an empty ok snapshot), so the run keeps waiting until rows appear.
- **Handoff**: the coordinator sets `handoff_pending` and awaits the injected `handoff`. It never claims durable ownership: a resolved `{ status: "queued" | "already_queued" }` means the background saw a definitive ack; a thrown handoff keeps `handoff_pending` (the background may still hold the outbox copy). Once the handoff starts, the observation timeout no longer overrides the state. A transcript the run opened is closed again after a terminal outcome, including a failed handoff.
- **URL changes**: `popstate` plus a 1000 ms `location.href` poll; `history.pushState`/`replaceState` are never wrapped. A URL change resets the run and reapplies the same auto-activation rule.
- **Bootstrap**: `installContentRuntime()` runs once per page context, returns null outside a Chrome page context or without embedded selectors, reads `loadAutoCapture()` before installing, and constructs `createProductionContentDependencies(__STAGE0_SELECTORS__, { autoActivateOnLoad })`.

### Runtime adapter — `content-runtime.ts`

- `CAPTURE_JOB_MESSAGE_TYPE = "capture_job"`; `DEFAULT_STABLE_SNAPSHOT_COUNT = 2`.
- `createContentRuntimeParser` exposes the observer-facing `snapshot` and the final `buildJob`:
  - `snapshot` calls `extractTranscriptSnapshot` on the container. A `not_ready` extraction becomes an `ok` empty snapshot (`transcript: ""`, `timestampedTranscript: ""`, hash of two empty forms) so the observer keeps waiting; other rejections pass through.
  - `buildJob` loads stored mappings (or uses embedded ones), calls `parseLecturePage` with `stableSnapshotCount` from the snapshot, applies the optional `preferredDiscussionSection` filter for discussions (mismatch → `skipped_section`, logged status-only), then `createTranscriptJob`. Job validation errors map back to a `ParserRejectionStatus`; the default mapping is `rejected_ambiguous_metadata`.
  - Overview fetching is bound to the page window (`document.defaultView.fetch`). `withOverviewFallback` wraps it: if a successful fetch lacks `id="recordings"` and is not a sign-in page, `renderOverviewInIframe` loads the same URL in a hidden 1 px same-origin iframe (12 s timeout, polling up to 4 s after `load` for the recording list) and returns the rendered HTML; otherwise the fetch HTML is returned. Diagnostics are sanitized: `redactPath` replaces 6+ hex-character runs or 3+ digit runs in the path with `<id>` and the log line contains URL path, source (`fetch`/`iframe`), status, byte count, page `<title>`, and shape booleans — never page content.
- `toPageSelectors` maps the fixture to the coordinator's `PageSelectors`. `createProductionContentDependencies` wires `createRuntimeHandoff()`, the parser, `autoActivateOnLoad`, and a status-only console callback.
- `createRuntimeHandoff` sends `{ type: "capture_job", job }` over `chrome.runtime.sendMessage`; it rejects on `lastError`, a non-conforming response, or `ok: false`, and resolves `{ status }` only for `queued`/`already_queued` (otherwise `{}`). `isBackgroundResponse` accepts `{ ok, status?, errorCategory?, message?, snapshot? }`.

### Outbox and notices — `extension-storage.ts`

- `STORAGE_KEY = "lectureTranscriptsExtensionState"`; `MAX_PENDING_HANDOFFS = 3`; `MAX_OVERFLOW_NOTICES = 20`; `MAX_DERIVED_IDENTITIES = 20`.
- Persisted shape: `{ pendingHandoffs, overflowNotices, derivedIdentities, lastStatus, lastOutcome, updatedAt }`. Reads clamp the arrays to the caps and treat a non-object value as empty state.
- `PendingHandoff` is `{ lectureKey, contentHash, lectureDate, capturedAt, job }`; jobs are stored as structured clones and deduplicated by `(lectureKey, contentHash)`. A fourth distinct job throws `OutboxFullError` without evicting anything. `removePendingHandoff` returns whether a row was removed.
- `OverflowNotice` is metadata only (`lectureKey`, `lectureDate`, `capturedAt`, `reason`, optional `staleLectureKey`); notices are newest-first, duplicate-suppressed by exact shape, capped at 20, and never evict an older notice (`OverflowNoticeFullError`). Reasons cover submit rejections plus extension-local `rejected_handoff_full` and `stale_derived_identity` (a lagging-title correction naming the superseded derived key).
- `DerivedIdentityRecord` is `{ sourceUrl, kind, lectureNumber, lectureKey, contentHash, capturedAt }`, written only for `numberSource: "derived"` captures so a later title-explicit capture can correct a stale slot. Records are upserted by `sourceUrl` and the oldest fall off first at 20.
- Every mutation runs through a promise-serialized critical section (`serial`), so concurrent read-modify-write calls cannot interleave. `removeOverflowNotice` accepts a valid index only.
- `snapshot()` returns `{ uploader, pendingHandoffs: count, overflowNotices, lastOutcome, updatedAt }`. `setUploaderStatus`/`clearStatus` manage the cached uploader snapshot; `recordOutcome` clamps the message to 256 characters and stamps `at`.

### Native Messaging client — `native-messaging.ts`

- `PROTOCOL_VERSION = 1`; `NATIVE_HOST_NAME = "com.neelbangera.lecturetranscripts"`; `MAX_STATUS_PAGE = 50`; `MAX_NATIVE_MESSAGE_BYTES = 1_048_576` (constant only; Chrome frames the wire).
- **Requests** (exact fields): `connect {type, protocolVersion, requestId, extensionVersion}`; `submit_job {…, job}`; `status_request {…, beforeJobId?, limit?}`; `retry_job {…, jobId}`; `discard_job {…, jobId, confirmation: "discard"}`; `reset {…}`. `requestId` matches `^[\x21-\x7e]{1,64}$`; `extensionVersion` is 1–32 printable ASCII; `beforeJobId`/`jobId` are positive safe integers; `limit` defaults to 50 and must be 1–50.
- **Responses**: `ack` (submit only) with `status` in `SUBMIT_ACK_STATUSES` (`queued`, `already_queued`, and the seven submit rejections including `rejected_duplicate_terminal`), `existingStatus`, and `action` (`retry_existing` / `discard_existing_then_recapture` / null); `command_result` for retry/discard/reset; `status` (exact keys: `type, protocolVersion, requestId, extensionVersion, uploaderVersion, authState, authorization, drainState, counts, jobs, nextBeforeJobId`); `error`. All validators enforce exact key sets, pattern-check `lectureKey`/`contentHash`/timestamps/URLs, cap `jobs` at 50, and require the exact `EMPTY_COUNTS` key set.
- `isNativeTranscriptJob` delegates to `validateTranscriptJob`; `isNativeRequest` validates outgoing requests, and every `make*Request` builder asserts its own shape. `isNativeResponse` validates incoming frames; an invalid frame rejects all pending requests with `NativeProtocolError` (`protocol_mismatch`).
- `NativeMessagingClient` owns one persistent `runtime.connectNative` port (`ensureConnected` coalesces concurrent connects), correlates responses by `requestId` in a pending map, forwards unsolicited/`status` messages to `onStatus` listeners, rejects pending requests with `NativeMessagingError("host_unavailable")` on disconnect, and never deletes outbox state. `createRequestId` prefers `crypto.randomUUID()`.
- Error types: `NativeMessagingError` (category + retryable, default retryable only for `host_unavailable`), `NativeProtocolError` (`protocol_mismatch`, not retryable), `NativeRemoteError` (carries the uploader's `error` category/retryable).

### Background coordinator — `background.ts`

- `DRAIN_ALARM_NAME = "lecture-transcripts-drain"`, `DRAIN_PERIOD_MINUTES = 1`; interactive commands hold a 30 s lease (`INTERACTIVE_LEASE_MS`) that keeps the port open.
- **Message validation**: `isExtensionMessage` enforces exact key sets for the seven internal message types. `handleMessage` rejects unknown/malformed messages, senders that are not this extension (a missing `sender.id` or missing `runtime.id` is tolerated), and `capture_job` senders whose `url`/`tab.url` is not an `https://leccap.engin.umich.edu` URL.
- **Capture**: `handleCapture` reconciles the job against any recorded derived identity for its `sourceUrl` (in-flight replace of a stale key, or a `stale_derived_identity` notice when the stale key may already have been published), writes the job to the outbox first, then runs a drain cycle. `queued`/`already_queued` is reported only when the outbox is empty afterward; a remaining copy reports `waiting_for_uploader` with `host_unavailable`; a matching `rejected_*` last outcome is surfaced instead of a false success. `OutboxFullError` records a `rejected_handoff_full` notice and outcome (appending a reopen instruction when the notice list is also full) and returns `ok: false`.
- **Drain**: `runDrainCycle` is single-flight. `drain()` connects, sends `connect`, persists the returned status, requests the first status page, replays pending handoffs in order, then closes the alarm-owned port only when `lastDrainState` is `idle` or `waiting_for_backoff`. A `host_unavailable` error keeps the outbox intact for the next alarm; other errors are recorded as outcomes.
- **Replay/ack**: `replayPendingHandoffs` submits each stored job. `acceptAck` requires the ack to echo the submitted `lectureKey`/`contentHash`; `queued`/`already_queued` removes the outbox copy and records the outcome; a submit rejection records a metadata-only notice first and removes the full copy only when that notice was saved, then records the ack's action.
- **Status**: `handleStatus` caches the status and notifies terminal transitions relative to the previously stored status. `handleStatusRequest` persists only the first page; older pages are returned to the popup without replacing the worker's last-known status.
- **Commands**: `handleReset` clears stored status and records a reset outcome only on an accepted `command_result`; `handleRetry`/`handleDiscard` relay the command and refresh the status page when accepted. Retry eligibility and the permanent-conflict confirmation live in the popup (`job-actions.ts`); the uploader remains authoritative.
- **Notifications**: terminal statuses (`uploaded`, `unchanged`, `permanent_conflict`, every `rejected_*`) raise one best-effort `chrome.notifications` entry per `lectureKey|contentHash|status`, titled `Lecture uploaded` or `Upload failed`, with message `${statusLabel(status)} · ${lectureKey}` and the 128 px icon. `queued`, `uploading`, `retryable_error`, waiting, and auth/drain states never notify. Notification creation is skipped when `chrome.notifications` is unavailable or `notificationsEnabled` is false; the in-memory dedup set is lost across worker restarts, so notifications are best-effort.
- **Wiring**: `start()` installs listeners once, creates the alarm, and `onStartup`/`onInstalled` recreate it; `onStartup` also drains. The module tail constructs the coordinator only when the runtime and alarms APIs exist.

### Status vocabulary — `status.ts`

- `QUEUE_STATUSES` is the exact persisted set: `queued`, `uploading`, `uploaded`, `unchanged`, `retryable_error`, `permanent_conflict`, and the nine `rejected_*` values through `rejected_permission`. `EXTENSION_LOCAL_STATUSES` is `rejected_handoff_full`, `not_ready`, `waiting_for_uploader`. `AUTH_STATES`, `DRAIN_STATES`, and `ERROR_CATEGORIES` mirror the plan's lists; `EMPTY_COUNTS` has exactly the 15 queue-status keys.
- `LABELS` is the single user-facing mapping for statuses, auth states, and error categories, including `skipped_section`, `rejected_handoff_full`, and `stale_derived_identity`. `statusLabel`/`errorLabel` return `Unknown status` for anything unmapped.
- Types `JobSummary`, `AuthorizationStatus`, `UploaderStatus`, `OverflowNotice`, `ExtensionSnapshot`, and `LastOutcome` mirror the wire shapes; `cloneCounts` fills missing count keys from `EMPTY_COUNTS`.

### Settings, options, and job actions — `settings-storage.ts`, `settings-actions.ts`, `job-actions.ts`, `options.ts`

- Settings keys: `autoCapture` and `notificationsEnabled`, both default true. A missing, unreadable, or non-boolean stored value falls back to the default per field; `saveSettings` refuses to write non-booleans (`SettingsValidationError`).
- `settings-actions.ts` gates the popup Settings button on `chrome.runtime.openOptionsPage` being a function and returns false without throwing when it is missing or throws.
- `job-actions.ts`: `CLEARABLE_STATUSES = ["uploaded", "unchanged"]`; `canDiscardJob` allows `permanent_conflict`, any `rejected_*`, and the clearable statuses; `canRetryJob` allows `retryable_error`, `permanent_conflict`, and `rejected_permission`. Confirm texts state explicitly that discard never deletes a remote file.
- `options.ts` renders one course card per mapping with page text, canonical name, slug, optional three-digit preferred discussion section, and term chips; the term dropdown offers the current and next year's four seasons plus any existing terms. Save validates through `validateCourseMappings` before writing `courseMappings`; invalid entries are listed and the offending control marked `aria-invalid`. The two behavior toggles save immediately on change and reload on failure.

### Popup — `popup.ts`

- `refresh()` sends `popup_snapshot` then `popup_status`, renders connection summary, drain state, versions, pending-handoff count/copy, overflow notices, last outcome (success/warn/error styling), and the first queue page. `loadMore()` sends `popup_status { beforeJobId }` and appends new `jobId`s. While a request is in flight the action buttons are disabled.
- Authorization rendering shows `authorizing` state copy and, when the URL passes the `https://github.com` check, a link preferring `verificationUriComplete`. The popup never receives or displays a credential.
- Job rows show `lectureKey — statusLabel`, `targetPath`, and remote hash/kind diagnostics, with Retry/Discard buttons per `job-actions.ts` eligibility. A permanent-conflict retry requires a confirmation that the remote file was corrected or deleted. **Clear uploaded** sequentially discards every loaded uploaded/unchanged row, refreshes, and reports the failure count.
- The Settings button is hidden when `openOptionsPage` is unavailable; otherwise it opens the options page.

## Data flow (capture path, storage, messaging)

### Capture path

```
Leccap page (document_idle)
  -> content.ts installContentRuntime()            [reads autoCapture]
  -> autoActivate / Show Transcript click
  -> MutationObserver on .transcript-viewer subtree
  -> parser.snapshot() after 1500 ms quiet windows  [two equal hashes]
  -> content-runtime buildJob()
       parseLecturePage -> course/term, transcript, overview (date + inventory),
                           kind + number (title | derived), hash
       createTranscriptJob -> validated TranscriptJob
  -> createRuntimeHandoff -> { type: "capture_job", job, numberSource? }
  -> background.handleCapture
       reconcileIdentity (derived-record correction / stale notice)
       ExtensionStorage.addPendingHandoff (max 3)
       drain(): connect -> status -> replay
       submit_job -> ack
  -> queued / already_queued: remove outbox copy, record outcome
     rejection: metadata-only notice, then remove copy
     host unavailable: keep copy, report waiting_for_uploader
```

### Storage keys

| Key | Module | Contents / invariant |
| --- | --- | --- |
| `lectureTranscriptsExtensionState` | `extension-storage.ts` | bounded outbox (3) + notices (20) + cached status/outcome; single writer path, serialized |
| `courseMappings` | `course-storage.ts` | validated allowlist; invalid data never written |
| `autoCapture` | `settings-storage.ts` | boolean, default true; non-boolean falls back |
| `notificationsEnabled` | `settings-storage.ts` | boolean, default true; non-boolean falls back |

### Messaging summary

- **Internal**: `capture_job`, `popup_snapshot`, `popup_connect`, `popup_reset`, `popup_status`, `popup_retry`, `popup_discard` → `BackgroundResponse { ok, snapshot?, errorCategory?, status?, message? }`.
- **Native wire**: requests `connect`, `submit_job`, `status_request`, `retry_job`, `discard_job`, `reset`; responses `ack`, `command_result`, `status`, `error`. Status pages carry at most 50 `JobSummary` rows and a `nextBeforeJobId` cursor; the transcript is only ever inside `submit_job.job`.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `course-config.ts` | Explicit Stage 0 allowlist and term/slug normalization | `COURSE_MAPPINGS`, `CourseConfig`, `CourseConfigError`, `normalizeCourseLabel`, `normalizeCourseSlug`, `parseTerm`, `normalizeTerm`, `humanizeTerm`, `getCourseMapping`, `isSupportedCourseTerm`, `matchesCourseIdentity` |
| `course-storage.ts` | Validate/persist/load the `courseMappings` allowlist | `COURSE_MAPPINGS_STORAGE_KEY`, `CourseMappingsValidationError`, `validateCourseMappings`, `loadStoredCourseMappings`, `loadCourseMappings`, `saveCourseMappings` |
| `settings-storage.ts` | Behavior-toggle persistence with per-field defaults | `AUTO_CAPTURE_STORAGE_KEY`, `NOTIFICATIONS_ENABLED_STORAGE_KEY`, `DEFAULT_EXTENSION_SETTINGS`, `validateSettings`, `loadSettings`, `loadAutoCapture`, `loadNotificationsEnabled`, `saveSettings` |
| `settings-actions.ts` | DOM-free options-page entry helpers | `canOpenOptionsPage`, `openOptionsPage` |
| `job-actions.ts` | Popup retry/discard/clear eligibility and confirm text | `CLEARABLE_STATUSES`, `isClearableStatus`, `canDiscardJob`, `canRetryJob`, `clearableJobs`, `discardConfirmText`, `clearUploadedConfirmText` |
| `status.ts` | Single status/auth/drain/error vocabulary and labels | `QUEUE_STATUSES`, `EXTENSION_LOCAL_STATUSES`, `AUTH_STATES`, `DRAIN_STATES`, `ERROR_CATEGORIES`, `EMPTY_COUNTS`, `statusLabel`, `errorLabel`, `UploaderStatus`, `JobSummary`, `OverflowNotice`, `ExtensionSnapshot`, `LastOutcome` |
| `transcript-normalizer.ts` | Deterministic normalization, timestamp handling, framed hash, browser-safe SHA-256 | `TIMESTAMP_PREFIX_SOURCE`, `TIMESTAMP_PREFIX_RE`, `normalizeTranscript`, `stripTimestampPrefix`, `derivePlainTranscript`, `normalizeTranscriptForms`, `frameTranscriptHashInput`, `computeContentHash`, `computeNormalizedContentHash`, `sha256Bytes`, `sha256Hex` |
| `transcript-job.ts` | Canonical job construction, validation, limits, URL canonicalization/sanitization | `TranscriptJob`, `validateTranscriptJob`, `assertValidTranscriptJob`, `createTranscriptJob`/`buildTranscriptJob`, `deriveLectureKey`, `canonicalizeSourceUrl`, `sanitizeSourceUrlForPublish`, `sourceUrlInfo`, `serializeTranscriptJob`, `transcriptJobByteLength`, limit constants, legacy `deriveStableLecturePath`/`deriveTimestampedLecturePath` |
| `leccap-parser.ts` | Page classification, identity, transcript extraction, overview date/inventory correlation, parser-side hash | `parseLecturePage`, `extractTranscriptSnapshot`, `canonicalizeLeccapUrl`, `hashTranscriptForms`, `parseRecordingDate`, `NORMATIVE_TIMESTAMP_PREFIX`, fixture/result types |
| `identity-derivation.ts` | Pure title-shape parsing and "next from the last one" overview-sequence number derivation | `parseTitleIdentity`, `parseRecordingBadge`, `parseRecordingTime`, `deriveIdentityFromOverview`, `OverviewCardFact`, `TitleIdentity` |
| `content.ts` | Page-facing activation, scoped observation, stability snapshots, handoff status; content-script entry | `createContentScript`, `installContentRuntime`, `PageCaptureStatus`, `ContentScriptDependencies`, timing constants |
| `content-runtime.ts` | Production parser/handoff adapter, overview fetch + iframe fallback, discussion-section filter | `createContentRuntimeParser`, `createProductionContentDependencies`, `createRuntimeHandoff`, `toPageSelectors`, `needsOverviewRender`, `renderOverviewInIframe`, `adaptCourseMappings`, `CAPTURE_JOB_MESSAGE_TYPE` |
| `extension-storage.ts` | Bounded outbox, metadata-only notices, derived-identity records, cached status/outcome | `ExtensionStorage`, `PendingHandoff`, `DerivedIdentityRecord`, `OutboxFullError`, `OverflowNoticeFullError`, `MAX_PENDING_HANDOFFS`, `MAX_OVERFLOW_NOTICES`, `MAX_DERIVED_IDENTITIES`, `STORAGE_KEY` |
| `native-messaging.ts` | Closed request/response vocabulary, validation, correlation, persistent port | `NativeMessagingClient`, `isNativeRequest`, `isNativeResponse`, `isStatusResponse`, `make*Request`, `createRequestId`, `NativeMessagingError`/`NativeProtocolError`/`NativeRemoteError`, `PROTOCOL_VERSION`, `NATIVE_HOST_NAME`, `MAX_STATUS_PAGE` |
| `background.ts` | Service-worker coordinator: validation, alarm, outbox, drain/replay, status relay, notifications | `BackgroundCoordinator`, `DRAIN_ALARM_NAME`, `DRAIN_PERIOD_MINUTES`, `ExtensionMessage`, `BackgroundResponse` |
| `popup.ts` | Popup rendering and commands; no credential handling | entry side effects only |
| `options.ts` | Course allowlist editor and immediate behavior-toggle saves | entry side effects only |
| `IMPLEMENTATION.md` | Historical source-level implementation plan | documentation only |

## Testing and verification

| Suite | Covers |
| --- | --- |
| `leccap-parser.test.ts` | Fixture identity/date/rows/hash, overview correlation (zero/multiple/failure/sign-in), unnumbered-title derivation and decoys, unmapped course, unsafe URL ordering, plain-only timestamps, mixed/malformed timestamps, discussions and badge section, textual dates |
| `transcript-normalizer.test.ts` | NFC/line-ending/whitespace/blank-line normalization, timestamp preservation, timestamped-only derivation, plain-only jobs, framed hash/byte counts, hash sensitivity, idempotence |
| `transcript-job.test.ts` | Identity/path derivation, whole-second `capturedAt`, URL canonicalization/publish sanitization, limits, unknown fields, invalid hashes |
| `protocol-vectors.test.ts` | Shared normalization vectors, source-URL vectors, cross-language canonical job bytes |
| `content.test.ts` | Activation/no-activation, auto-capture toggle, auto-open/close, already-expanded capture, URL-change capture, mutation restart, timeout, parser rejections, runtime parser adapter, discussion section filter, handoff adapter, production bootstrap, iframe fallback decision |
| `extension-storage.test.ts` | Capacity, dedupe, serialization, notices, restart replay, snapshot clamping, derived-identity records |
| `native-messaging.test.ts` | Bounded requests, single persistent port, concurrent correlation, exact response envelopes |
| `background.test.ts` | Sender/message validation, outbox-before-submit, replay/ack handling, rejection notices, handoff-full, drain lifecycle/port close, popup commands, terminal notifications, startup wiring, derived-identity correction |
| `popup-page.test.ts`, `options-page.test.ts`, `job-actions.test.ts`, `settings-storage.test.ts`, `settings-actions.test.ts` | Popup outcome styling/queue copy, options dirty-state/save/reload/busy-lock, action eligibility, settings defaults/validation |
| `fixture-packet.test.ts` | Stage 0 selector/course/expected/size fixtures and sanitization rules |
| `build-smoke.test.ts` | Builds the extension and asserts manifest-referenced files, inlined selectors, bundle markers |

Commands from the repository root: `npm run typecheck`, `npm test`, `npm run build`.

## Related plan sections

- [Transcript job and state model](../../TECHNICAL_PLAN.md#transcript-job-and-state-model) — identity, paths, date formats, hash framing, outbox/notice capacities, state model.
- [Canonical transcript-job schema](../../TECHNICAL_PLAN.md#canonical-transcript-job-schema) — the job contract implemented by `transcript-job.ts`.
- [Canonical Native Messaging contract](../../TECHNICAL_PLAN.md#canonical-native-messaging-contract) — envelopes, pagination, drain lifecycle, and rejection actions implemented by `native-messaging.ts`/`background.ts`.
- [Canonical normalization-vector contract](../../TECHNICAL_PLAN.md#canonical-normalization-vector-contract) — vectors implemented by `transcript-normalizer.ts`.
- [Canonical source-URL vectors](../../TECHNICAL_PLAN.md#canonical-source-url-vectors) — URL rules implemented in `transcript-job.ts` and `leccap-parser.ts`.
- [Canonical status vocabulary](../../TECHNICAL_PLAN.md#canonical-status-vocabulary) — the vocabulary in `status.ts`.
- [Stage 2 — Build and test the Leccap parser](../../TECHNICAL_PLAN.md#stage-2--build-and-test-the-leccap-parser) — parser, completion observer, normalization, and fixture requirements.
- [Stage 3 — Implement the extension runtime and handoff](../../TECHNICAL_PLAN.md#stage-3--implement-the-extension-runtime-and-handoff) — content runtime, background, native client, outbox, popup, options.
- [PHASE_1_DECISIONS.md](../../PHASE_1_DECISIONS.md) — rationale for activation, stability, outbox ownership, alarm wakeup, and build-time selector embedding.

## How to change this directory safely

1. **Do not change the wire contract locally.** A new field, status, limit, or message requires a deliberate edit to `TECHNICAL_PLAN.md` and the schemas in `protocol/` first; TypeScript and Go must agree, and the shared vectors must keep passing.
2. **Keep the two normalizer/hash implementations identical.** `leccap-parser.ts`'s private `normalizeTranscript`/`hashTranscriptForms` and `transcript-normalizer.ts` implement the same normative rules; any change to one must be mirrored and covered by `protocol-vectors.test.ts`.
3. **Keep identity fail-closed.** Never derive a course, term, number, kind, or date from partial/ambiguous page text; new page shapes require a Stage 0 fixture and a path decision, not a heuristic.
4. **Preserve activation semantics.** Page navigation alone never captures; only an explicit activation (click, auto-opened control, or a visible/populated already-open transcript) starts a run, and `autoCapture: false` must never auto-open or auto-capture.
5. **Preserve outbox semantics.** Write before submit; clear a full job only after a definitive ack, and after saving a metadata-only notice for a non-persisting rejection; never evict an older pending job or notice; never claim `queued` when the copy is still pending.
6. **Keep the port lifecycle rule.** An alarm-owned port closes only after `idle`/`waiting_for_backoff`, never mid-`working`/`authorizing`, and a disconnect is an interruption that leaves the outbox intact.
7. **Update all consumers when a status changes.** `status.ts` is the only label/vocabulary mapping; `background.ts`, `content.ts`, and `popup.ts` must not grow parallel lists.
8. **Treat the legacy path helpers as suspect.** `deriveStableLecturePath`/`deriveTimestampedLecturePath` contradict the plan and the uploader; do not use them for new work (see Open questions).
9. Re-run `npm run typecheck`, `npm test`, and `npm run build` after any change; keep tests deterministic (fake clocks, fake request IDs, fixture-backed fetches).

## Open questions

Verified deviations or unverifiable items; this document did not change any of them.

- **Legacy path helpers contradict the plan and the uploader.** `transcript-job.ts:606-619` derives `<slug>/lectures/<NNN>.md` for the plain file and ignores `kind` for the timestamped file. The plan's lecture plain path is `<slug>/<NNN>.md`, and `uploader/internal/queue/store.go` `TargetPath`/`TimestampedPath` use `<slug>/<NNN>.md` and `<slug>/timestamped/<NNN>.md` (with `discussions/` variants). Only `extension-tests/transcript-job.test.ts` references these helpers.
- **`ActivationSource` includes `'already-expanded'`, but no call site passes it.** Already-open transcripts are activated through `startCapture('page-load')` or `startCapture('url-change')`; the variant is effectively documentation-only.
- **`skipped_section` is outside the plan vocabulary.** It is produced by `content-runtime.ts` when `preferredDiscussionSection` does not match, and labelled in `status.ts`, but neither the plan's status table nor `PHASE_1_DECISIONS.md` names the status or the course field.
- **Browser-console logging is unspecified.** `content-runtime.ts` logs a sanitized overview-fetch diagnostic that includes the fetched page `<title>` (up to 80 characters); the plan defines sanitization for uploader logs only.
- **`MAX_NATIVE_MESSAGE_BYTES` is not enforced in this directory.** It is exported by `transcript-job.ts` and `native-messaging.ts`, but no runtime path checks it; Chrome frames Native Messaging and the uploader enforces the 1,048,576-byte cap.
- **`extension/src/IMPLEMENTATION.md` is stale.** Its "Current blockers" (strict TypeScript errors, missing snapshot adapter, missing production content entry, unembedded selectors) are all resolved in the current tree.
