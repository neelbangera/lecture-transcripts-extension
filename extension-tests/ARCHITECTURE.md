# extension-tests Architecture

## Purpose

`extension-tests/` is the offline browser-side test suite for the Chrome
extension. `npm test` (`vitest run`, see `package.json` and `vitest.config.ts`)
collects `extension-tests/**/*.test.ts` in the Node environment with
`globals: true` and `passWithNoTests: true`.

The suite protects the capture and handoff contract defined by
`TECHNICAL_PLAN.md`: page detection and activation, transcript extraction,
normalization and framed hashing, job identity and path derivation, the bounded
pending-handoff outbox, the background coordinator's replay and acknowledgement
logic, the Native Messaging client, popup/options display logic, the shared
protocol vectors, and the built bundle.

Verified on branch `agent/docs-b` (2026-09-23):

- `npm test` -> 16 test files / 224 tests, all passing.
- `npm run typecheck` (`tsc --noEmit`) -> clean.
- `npm run build` -> writes `dist/extension/`; `build-smoke.test.ts` runs the
  real build script as part of the suite.

`docs/TESTING.md` still records a 7-file / 71-test baseline and claims that the
outbox and background replay paths "do not yet have dedicated committed
suites". Both statements are stale on this branch: `background.test.ts` (49
tests) and `extension-storage.test.ts` (17 tests) now cover those paths. See
"Testing and verification" below.

## Boundaries and dependencies

- The only packages involved are the declared devDependencies: `vitest`,
  `jsdom`, `@types/jsdom`, `@types/node`, `@types/chrome`, `typescript`, and
  `esbuild` (the last is exercised indirectly by the build smoke test). No
  production dependency or frontend framework is added by tests.
- Tests import production modules through relative paths such as
  `../extension/src/content.ts`; shared contract vectors through
  `../protocol/normalization-vectors.json` and
  `../protocol/source-url-vectors.json`; the Go canonical job fixture through
  `../uploader/internal/protocol/testdata/transcript-job.canonical.json`; and
  the real popup markup through `../extension/popup.html`.
- `vitest.config.ts` is deliberately minimal: Node environment, no setup file,
  no coverage thresholds, include only `extension-tests/**/*.test.ts`.

### Fake and stub strategy

| Real dependency | Fake used in the suite |
| --- | --- |
| Chrome page DOM | Per-test `new JSDOM(html, { url })`. Parser tests pass the explicit URL so `location.href` is the stubbed Leccap player URL. Content tests additionally stub `globalThis.Element`, `HTMLElement`, and `MutationObserver` with the JSDOM window classes and hand the coordinator a wrapped `Window` that routes timers to the active (fake) clock. |
| Timers and clock | `content.test.ts` and `extension-storage.test.ts` use `vi.useFakeTimers()` plus `vi.setSystemTime(new Date(CAPTURED_AT))`. `background.test.ts` injects `now: () => now` and advances it with `harness.advance(ms)`. Popup/options tests use real timers with a bounded `settle()` macrotask drain. |
| `chrome.storage.local` | In-memory `MemoryStorageArea` classes implementing the production `StorageAreaLike` / `CourseStorageArea` / `SettingsStorageArea` shapes. The background/storage suites record `get`/`set` operation order; `options-page.test.ts` can hold writes to simulate an in-flight save. |
| Chrome runtime, alarms, notifications | `FakeRuntime`, `FakeAlarms`, `FakeNotifications`, and a generic `FakeEvent` in `background.test.ts`, wired through `BackgroundCoordinator`'s constructor-injected ports. |
| Native Messaging host | `FakeNativeMessagingClient` (records `connect`/`status`/`submit`/`retry`/`discard`/`reset`, can fail as `host_unavailable`, can emit status and disconnect events) in `background.test.ts`; `FakePort` with a deterministic `requestIdFactory` in `native-messaging.test.ts`. |
| Parser and handoff callables | `fakeParser()` and `vi.fn` handoffs in `content.test.ts`; the runtime adapter tests also drive the real parser with an injected `fetchOverview`. |
| Network | Injected `OverviewFetcher` functions returning committed fixture HTML. No test opens a socket. |
| Real build pipeline | `build-smoke.test.ts` shells out with `execFileSync(process.execPath, ["scripts/build-extension.mjs"])` and inspects `dist/extension/`. |
| Module registry | `options-page.test.ts` and `popup-page.test.ts` call `vi.resetModules()` and dynamically `import()` the self-initializing UI modules after mounting their DOM. |

### Deliberately NOT covered

- Real Chrome: no extension is loaded, no service worker runs, no real
  `chrome.*` API is called, and no real `chrome.storage` persistence is tested.
- Real macOS Keychain, real Native Messaging host process, and the
  `lecture-uploader` executable.
- Real GitHub (device flow, Contents API, token storage), real Leccap pages or
  sessions, and any network call.
- Real popup/options rendering, layout, and CSS in a browser; JSDOM covers
  DOM/label logic only.
- Real file-system modes, queue database, and log rotation; those are Go
  uploader and packaging concerns covered elsewhere.
- Owner-machine checks (render-time measurement, unpacked load and extension
  ID capture, host registration, Keychain prompt, real capture, file modes,
  alarm-delayed retry) are listed in `docs/TESTING.md` and cannot be automated
  here.

## Contracts and invariants

- **Fixtures are evidence, not configuration.** Tests read the committed
  Stage 0 packet from `extension-tests/fixtures/` at collection time via
  `fileURLToPath(new URL("./fixtures/", import.meta.url))` and
  `readFileSync`/`JSON.parse`. No test writes a fixture, and no production
  code reads a fixture path at runtime: the build embeds the selector JSON
  (see below) and the allowlist is mirrored by hand into
  `extension/src/course-config.ts`. Changing a fixture requires new verified
  evidence and a plan/decision update, per `fixtures/IMPLEMENTATION.md`.
- **Raw captures never enter the suite.** The root `.gitignore` excludes
  `open.html`, `closed.html`, `*.mhtml`, `/raw-captures/`, and
  `extension-tests/fixtures/*-real.html`; tests use only the sanitized packet.
- **Exact protocol strings are asserted**, never renamed labels: parser
  statuses (`not_ready`, `skipped_section`, `rejected_missing_identity`,
  `rejected_ambiguous_metadata`, `rejected_oversized`, `rejected_invalid_hash`,
  `rejected_unsafe_url`), capture statuses (`idle`, `activated`,
  `handoff_pending`, `waiting_for_uploader`), submit ack statuses (`queued`,
  `already_queued`, `rejected_queue_full`, `rejected_duplicate_terminal`),
  drain states (`idle`, `working`, `authorizing`, `waiting_for_backoff`), and
  error categories (`invalid_message`, `host_unavailable`,
  `ineligible_command`, `protocol_mismatch`). `job-actions.test.ts` iterates
  the full `QUEUE_STATUSES` vocabulary from `extension/src/status.ts`.
- **Contract bounds are pinned by tests**: `MAX_PENDING_HANDOFFS = 3`,
  `MAX_OVERFLOW_NOTICES = 20`,
  `STORAGE_KEY = "lectureTranscriptsExtensionState"`, `MAX_STATUS_PAGE = 50`,
  `MAX_TRANSCRIPT_BYTES = 460800`, `MAX_SERIALIZED_JOB_BYTES = 972800`,
  `MAX_SOURCE_URL_BYTES = 2048`, `DRAIN_ALARM_NAME =
  "lecture-transcripts-drain"`, `DRAIN_PERIOD_MINUTES = 1`,
  `STABILITY_DEBOUNCE_MS = 1500`, `OBSERVATION_TIMEOUT_MS = 30000`,
  `URL_POLL_INTERVAL_MS = 1000`, `AUTO_ACTIVATE_RETRY_MS = 500`.
- **Determinism**: fixed `capturedAt` values, `vi.setSystemTime`, injected
  request IDs, and `structuredClone` defensive copies so storage tests cannot
  pass by aliasing caller objects.
- **Outbox durability invariant**: the background coordinator writes the full
  job to the outbox before submitting; the full copy is cleared only for a
  definitive `queued`/`already_queued` ack whose `requestId`, `lectureKey`, and
  `contentHash` match the submission. A mismatched echo keeps the full copy and
  records a `protocol_mismatch` outcome; a submit rejection clears the full
  copy and records a metadata-only overflow notice.
- **Build-time selector embedding**: `scripts/build-extension.mjs` validates
  `lecture-page.selectors.json` and defines `__STAGE0_SELECTORS__` for the
  content bundle with esbuild. `extension/src/content.ts` returns `null` when
  `typeof __STAGE0_SELECTORS__ === "undefined"`, so the runtime never reads a
  fixture path. The smoke test asserts the fixture selector strings appear in
  `content.js`, that `capture_job` appears in the content/background bundles,
  and that no bundle contains an unresolved `__STAGE0_SELECTORS__` token (the
  smoke regex tolerates esbuild's `// <define:__STAGE0_SELECTORS__>` comment).

## Data flow / what is exercised

The happy path threaded through the suites:

1. `content.test.ts` drives `createContentScript` over fixture DOMs. A
   recognized lecture page auto-activates on load (retrying every 500 ms until
   the transcript control appears), a `Show Transcript` click activates, or a
   URL change activates only when the transcript is already open and populated.
2. Activation starts a stability window: a snapshot every 1500 ms; two
   identical snapshots plus the open-state control (`title="Hide Transcript"`)
   and nonempty rows produce a build. A mutation restarts the window; 30 s
   without a stable transcript ends as `not_ready`.
3. `createContentRuntimeParser` builds the job through the real
   `parseLecturePage` path: canonicalize `sourceUrl`, read course/term from the
   header, read the numeric recording-title prefix, fetch the linked overview
   (`credentials: "include"`), correlate exactly one `.recording` card whose
   `.play-link a[href]` equals the canonical player URL, parse the month-first
   `.rec-date`, then normalize and hash both transcript forms.
4. `createRuntimeHandoff` sends `{ type: "capture_job", job }` through
   `chrome.runtime.sendMessage` and resolves only for an accepted durable
   response; missing channel, non-durable responses, and malformed response
   shapes all keep the page in `handoff_pending`.
5. `background.test.ts` validates the sender (same extension id, Leccap URL or
   tab URL), writes the job to the outbox, submits it through the fake native
   client, clears it on a definitive ack, and records the outcome. Alarms,
   startup, and popup connect replay pending jobs; the drain alarm closes the
   port only after `idle`/`waiting_for_backoff`; notifications fire once per
   terminal transition and never contain transcript text.
6. `popup-page.test.ts` renders the real `extension/popup.html` against a
   stubbed `chrome.runtime.sendMessage` and locks queue counts, empty-state
   copy, and success/warn/error outcome styling. `options-page.test.ts` mounts
   a minimal DOM matching `options.html` and locks dirty-state, save, reload,
   validation, and busy-lock behavior.

Bounded failure paths exercised: non-lecture no-op; parser rejections mapped to
page statuses; unmapped course/term; zero/multiple overview matches; sign-in
page; malformed/mixed timestamps; oversized job fields; unsafe URL; missing
background channel; outbox full at 3 with a fourth capture; overflow-notice
list full at 20; host unavailable keeps the outbox; `rejected_queue_full` and
`rejected_duplicate_terminal` outcomes; drain port lifecycle; popup
retry/discard/reset ineligible commands.

## File responsibilities

Test counts below are the verified `npm test` output on 2026-09-23.

| File | Suite or fixture | What it locks down |
| --- | --- | --- |
| `content.test.ts` (44 tests) | `extension/src/content.ts`, `extension/src/content-runtime.ts` | Activation sources (load, click, already-expanded, URL change), auto-activation retry and disable toggle, stability-window restart on mutation, 30 s observation timeout, parser rejection mapping, transcript close-after-capture, real parser + runtime handoff end to end, runtime handoff response validation, production bootstrap, discussion preferred-section skipping, overview iframe fallback decision. |
| `background.test.ts` (49 tests) | `extension/src/background.ts`, `extension/src/extension-storage.ts`, `extension/src/native-messaging.ts`, `extension/src/status.ts` | Message and sender validation, outbox write-before-submit, ack correlation and definitive-ack clearing, replay after restart, handoff-full and overflow-notice bounds, alarm drain lifecycle, popup commands (snapshot, connect, status paging, retry, discard, reset), terminal notifications and their suppression, derived-identity correction (in-flight replace, stale notice, title confirmation). |
| `extension-storage.test.ts` (17 tests) | `extension/src/extension-storage.ts` | Capacity constants and storage key, bounded outbox and notice lists with no eviction, `(lectureKey, contentHash)` dedup, defensive copies, serialized read-modify-write, restart replay, snapshot shape and clamping of corrupt persisted state, derived-identity record/find/upsert/clear and bound. |
| `leccap-parser.test.ts` (18 tests) | `extension/src/leccap-parser.ts` + fixture packet | Fixture parse result vs `lecture-page.expected.json`, overview fetch and exact player-link correlation, zero/multiple/failed/sign-in overview outcomes, unnumbered-title derivation and decoy rejection, non-lecture and loading pages, unmapped course, unsafe URL pre-check, plain-only and malformed timestamp shapes, discussion parsing and section badge rule, textual month/day year policy, source URL canonicalization. |
| `identity-derivation.test.ts` (10 tests) | `extension/src/identity-derivation.ts` | Title shapes (numeric prefix, `Lecture: N`, `Discussion N`, lag form, decoys), badge category/section, rec-time parsing, "next from the last one" derivation, consecutive unnumbered runs, kind separation, contradictions, start-time checks. |
| `identity-derivation.test.ts` (10 tests) | `extension/src/identity-derivation.ts` | Title shapes (numeric prefix, `Lecture: N`, `Discussion N`, lag form, decoys), badge category/section, rec-time parsing, "next from the last one" derivation, consecutive unnumbered runs, kind separation, contradictions, start-time checks. |
| `transcript-normalizer.test.ts` (8 tests) | `extension/src/transcript-normalizer.ts` | NFC, CRLF, horizontal whitespace, blank-line collapse, timestamp digit preservation, plain derivation from timestamped-only, plain-only behavior, exact framed hash bytes, committed fixture hash, hash sensitivity, idempotence. |
| `transcript-job.test.ts` (12 tests) | `extension/src/transcript-job.ts` | Identity/path derivation from the allowlist, whole-second `capturedAt`, URL canonicalization and publish sanitization, unsafe URL rejection, unknown-field/hash/schema/date checks, transcript and per-field size caps at their exact boundaries. |
| `protocol-vectors.test.ts` (9 tests) | `protocol/normalization-vectors.json`, `protocol/source-url-vectors.json`, `uploader/internal/protocol/testdata/transcript-job.canonical.json`, normalizer and job modules | Cross-language normalization vectors and hash assertions, source URL vector table, canonical job fixture byte-for-byte parity with the Go serializer, hash recomputation, compact escaping for Go parity. |
| `native-messaging.test.ts` (3 tests) | `extension/src/native-messaging.ts` | Bounded status request shape and unknown-field rejection, persistent port reuse, concurrent response correlation, exact response envelope validation (status frames must not carry a transcript). |
| `course-storage.test.ts` (16 tests) | `extension/src/course-storage.ts`, `extension/src/course-config.ts` | Allowlist validation (labels, slugs, terms, duplicates, preferred section), built-in fallback when storage is absent/invalid/unreadable, save-never-writes-invalid, stored mappings used when valid. |
| `settings-storage.test.ts` (10 tests) | `extension/src/settings-storage.ts` | Defaults for unset/malformed values, per-field boolean fallback, storage-unavailable behavior, validated writes only. |
| `settings-actions.test.ts` (3 tests) | `extension/src/settings-actions.ts` | `openOptionsPage` availability check, success result, non-throwing failure when the API is missing or throws. |
| `job-actions.test.ts` (5 tests) | `extension/src/job-actions.ts`, `extension/src/status.ts` | Clearable/retryable/discardable status sets across the full queue vocabulary, clear-uploaded selection order, confirmation copy promising that discard never deletes a remote file. |
| `options-page.test.ts` (14 tests) | `extension/src/options.ts` (dynamic import), course/settings storage | Dirty-state rules (focus and term highlight are not edits), add/remove course and term chips, save/reload, in-flight control lock, immediate behavior-toggle save, validation errors and `aria-invalid`, empty state. |
| `popup-page.test.ts` (12 tests) | `extension/src/popup.ts` (dynamic import), `extension/src/status.ts` | Queue count and empty copy, outcome notice styling for uploaded/conflict/retryable, stale-style replacement on action failure, clean no-outcome state, authorization auto-open (once per challenge, GitHub-only), code card + expiry countdown + Keychain heads-up, code copy, extension-ID display/copy. |
| `fixture-packet.test.ts` (17 tests) | `extension-tests/fixtures/` (plus type-only imports from `extension/src/leccap-parser.ts`) | Meta-suite over the Stage 0 packet: selector fields, completion mode consistency, linked-overview date policy, debounce/sanity values, placeholder ban, course mapping uniqueness, expected-output contracts, size report caps and maxima, HTML sanitization. |
| `build-smoke.test.ts` (1 test) | `scripts/build-extension.mjs`, `extension/manifest.json`, `dist/extension/` | Every manifest-referenced file exists, icon PNG signature, popup/options HTML references resolve, no unresolved selector placeholder, fixture selectors and `capture_job` present in the bundles, options/popup bundles contain their key behavior strings. |

Every module under `extension/src/` is imported by at least one suite.
`extension/src/status.ts` has no dedicated test file; it is exercised as the
shared vocabulary and as `EMPTY_COUNTS`/`QUEUE_STATUSES` oracles by the
background, storage, popup, and job-action suites.

## Testing and verification

```sh
npm ci
npm run typecheck
npm test
```

- On this branch, `npm test` reports 16 files / 224 tests passed and
  `npm run typecheck` is clean. Per-file counts are listed in the table above.
- `build-smoke.test.ts` invokes the real build script (120 s timeout) and then
  inspects `dist/extension/`; `dist/` is gitignored.
- The suite is fully offline: no test requires GitHub, Leccap, Keychain, a
  real browser, or network access.
- Known documentation drift to resolve with the owner: `docs/TESTING.md`
  reports a 7-file / 71-test baseline and a coverage boundary that predates
  the committed outbox/background suites; `extension-tests/IMPLEMENTATION.md`
  work items 1-7 are all represented in the tree, and its "Done when" bar
  (`npm test` collects every suite, all tests pass) is met.
- Open question: `transcript-job.test.ts` pins
  `deriveStableLecturePath("eecs484", 6)` to `eecs484/lectures/006.md`, while
  `TECHNICAL_PLAN.md` states the lecture plain path as
  `<courseSlug>/<lectureNumber:03d>.md` (`eecs484/006.md`). The test matches
  the current code in `extension/src/transcript-job.ts:606`; the plan text is
  the outlier and should be reconciled deliberately, not by editing the test.

## Related plan sections

- `TECHNICAL_PLAN.md`: "Toolchain baseline" (Vitest, `@types/chrome`, offline
  browser/uploader suites), "Canonical Native Messaging contract" (response
  shapes, status vocabulary, drain protocol), "Canonical transcript-job
  schema", "Canonical source-URL vectors", "Canonical status vocabulary",
  "Transcript job and state model" (identity, normalization, framed hash,
  outbox bounds), and the "Phase 1 guardrails" table.
- `extension-tests/IMPLEMENTATION.md`: work items 1-7 and the test discipline
  rules (committed fixtures only, exact status strings, deterministic clocks,
  regression tests before semantics changes).
- `docs/TESTING.md`: run commands, browser-side suite table, offline/restart/
  duplicate/conflict/retry coverage notes, fake-versus-real boundary.
- `extension-tests/fixtures/ARCHITECTURE.md`: the packet this suite consumes.

## How to change this directory safely

1. Add a regression test before changing parser, normalization, job, or queue
   semantics; the plan makes the contracts normative.
2. Never commit or read raw authenticated captures. Extend the sanitized
   fixture packet instead, and record new evidence in `docs/STAGE_0_REPORT.md`.
3. Keep the suite offline. Introduce a fake or an injected port; do not add a
   network call, a real Chrome dependency, or a Keychain touch.
4. Assert exact machine strings and bounded constants from the plan. Do not
   loosen a cap or rename a status to make a test pass.
5. Keep clocks, request IDs, and captured timestamps deterministic, and keep
   defensive-copy expectations when storage behavior changes.
6. When code and plan disagree, resolve it as a deliberate plan/decision
   update (or a code fix), not by silently rewriting the expectation.
7. Keep `build-smoke.test.ts` in sync with `extension/manifest.json` and the
   build script; update the manifest-target list if a new entry point appears.
8. Run `npm test` and `npm run typecheck` before committing.
