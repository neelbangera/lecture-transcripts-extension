# extension-tests/fixtures Architecture

## Purpose

`extension-tests/fixtures/` is the committed, sanitized Stage 0 evidence
packet required by `TECHNICAL_PLAN.md` ("Required Stage 0 packet"). It records
the observed Leccap page shapes, the observed selector and completion/date
policy, expected parser results, the personal course/term allowlist, and the
measured byte sizes of representative transcripts.

The packet is the parser's source of truth and a build input:

- `leccap-parser.test.ts`, `content.test.ts`, and `fixture-packet.test.ts`
  read the fixtures at test-collection time.
- `scripts/build-extension.mjs` validates and embeds
  `lecture-page.selectors.json` into the content bundle and checks
  `course-mapping.json` is nonempty; the built extension never reads a fixture
  path at runtime.
- `extension/src/course-config.ts` mirrors the course allowlist as
  `COURSE_MAPPINGS`, with the fixture remaining the recorded evidence.

Fixtures are **evidence, not configuration**: every value must be observed or
explicitly verified on a permitted page, no test or build step writes them,
and changing one requires new verified evidence plus the corresponding
plan/decision update (`fixtures/IMPLEMENTATION.md`, "Do not do").

## Boundaries and dependencies

- Consumed by:
  - `extension-tests/leccap-parser.test.ts` (selectors, course mapping,
    expected output, all HTML fixtures, overview correlation),
  - `extension-tests/content.test.ts` (selectors, course mapping,
    `lecture-page.html`, `non-lecture-page.html`, `overview-page.html`,
    `lecture-page.expected.json`),
  - `extension-tests/fixture-packet.test.ts` (the whole packet, as a
    meta-contract),
  - `scripts/build-extension.mjs` (`lecture-page.selectors.json`,
    `course-mapping.json`),
  - `extension-tests/build-smoke.test.ts` indirectly (it checks the embedded
    selector strings against the fixture).
- Not in this directory: `protocol/normalization-vectors.json`,
  `protocol/source-url-vectors.json`, and
  `uploader/internal/protocol/testdata/transcript-job.canonical.json` are
  shared cross-language contract vectors and live with the protocol, not the
  page-evidence packet.
- No runtime reads: `transcript-size-report.json` is evidence only; no
  production code or test asserts behavior from it beyond packet validation.
- Raw captures are excluded by the root `.gitignore` (`open.html`,
  `closed.html`, `*.mhtml`, `/raw-captures/`,
  `extension-tests/fixtures/*-real.html`) and must never be committed, pasted
  into prompts, or used as runtime input.

## Contracts and invariants

`fixture-packet.test.ts` (16 tests) enforces the packet-level contract; the
parser and content suites enforce behavioral consistency with the expected
outputs.

### Sanitization rules (enforced across every HTML fixture)

- No media or WebVTT URLs: absolute or attribute URLs must not match the
  case-insensitive media extension pattern
  `\.(?:mp4|m4v|mov|webm|m3u8|mpd|vtt|webvtt|srt|mp3|m4a|aac|wav)(?:[?#]|$)`.
- No secrets or user identifiers: no `set-cookie`, `sessionid`, `token=`,
  `cookie=`, `authorization:`, `bearer`, `access_token`, `refresh_token`,
  `device_code`, `user_code`, or email-address patterns.
- Route IDs are sanitized: every `player/r/<id>` or `site/<id>` capture must
  match `^sanitized[a-z0-9]+$` (observed IDs became `sanitized01`,
  `sanitized02`, `sanitized17`, `sanitized21`, `sanitizedoverview`).
- No template placeholder text (`TBD`, `e.g.`, `CSS selector string`) in the
  selector fixture.
- Instructor names are replaced with `[REDACTED]`; fixture HTML comments state
  the sanitization contract.

### Selector and policy invariants (`lecture-page.selectors.json`)

- `transcriptButtonSelector` must cover both control states:
  `#sourcebar button[title='Show Transcript'], #sourcebar button[title='Hide Transcript']`.
- `transcriptContainerSelector` is `.transcript-viewer`.
- `courseSelector` and `termSelector` share
  `#title-header .content-header-site-btn span` with regex
  `^\s*(.+?)\s+-\s+(Winter|Spring|Summer|Fall)\s+(\d{4})\s*$`; capture groups
  are 1 for the course, 2/3 for season/year.
- `lectureNumberSelector` is
  `#title-header .content-header-recording-title` with regex
  `^\s*(\d{1,3})\s+`, capture group 1. The numeric prefix is the only lecture
  identity source.
- `completionIndicator` uses mode `attribute_equals` on
  `#sourcebar button` with attribute `title` and `valueRegex`
  `^Hide Transcript$`, plus
  `populationSelector: ".transcript-viewer .transcript-row .transcript-text"`.
  The compound rule (open-state title + at least one populated row + two
  identical normalized snapshots) is recorded in the fixture note.
- `loadingIndicatorSelector` is `null`; the fixture must then carry a
  nonempty `loadingRegionNote`, which explains that the page-level
  `layout-loading` class persists after completion and the initial `loading`
  label sits outside the transcript container, so neither is a signal.
- `lectureDateSource` is exactly `page: "linked_overview_page"`,
  `runtimeLookup: "on_demand_fetch"`, `correlation: "absolute_player_href"`,
  `dateOrder: "month-first"`, with nonempty
  `lecturePageOverviewLinkSelector`, `recordingCardSelector`,
  `recordingLinkSelector`, `dateSelector`, `dateRegex`, and integer
  `dateCaptureGroup`.
- `dateYearPolicy` is `term_year_if_missing`; `stabilityDebounceMs` is 1500;
  `sanityMinChars` is at least 50; `isSPA` is a boolean (recorded `false`);
  `loadingTextMarkers` is an array of nonempty strings (currently empty).

### Expected-output invariants

- `lecture-page.expected.json`, `no-number-lecture-page.expected.json`, and
  `decoy-title-page.expected.json`
  declare `supported` as a boolean and cover both outcomes; a negative fixture
  must carry `expectedRejection`.
- A positive expected result must have `courseSlug` matching `^[a-z0-9]+$`,
  `term` matching `^\d{4}-(winter|spring|summer|fall)$`, an integer
  `lectureNumber`, a `lectureKey` equal to
  `<courseSlug>/<term>/<lectureNumber:03d>`, a 64-lowercase-hex `contentHash`,
  `derivedFrom` in `{both, timestamped-only, plain-only}`, a canonical HTTPS
  `leccap.engin.umich.edu` `sourceUrl` (no userinfo, port, query, or
  fragment; <= 2048 UTF-8 bytes), and a `(courseSlug, term)` pair present in
  the course mapping.

### Course mapping invariants

- `courseMappings` is a nonempty array. Each entry has a nonempty
  `pageCourseText` and `courseName` (<= 256 chars, no newline), a
  `courseSlug` matching `^[a-z0-9]+$`, and a nonempty `supportedTerms` list
  of unique `^\d{4}-(winter|spring|summer|fall)$` values.
- Normalized page labels (`NFC`, whitespace collapsed, trimmed), slugs, and
  `courseSlug/term` pairs must each be unique.
- The fixture is the allowlist source; unmapped labels fail closed as
  `rejected_ambiguous_metadata` (proven in `leccap-parser.test.ts` and
  `content.test.ts`).

### Size-report invariants

- `capturedAt` is RFC3339; at least two samples exist.
- Per-sample caps: `transcriptBytes <= 460800`,
  `timestampedTranscriptBytes <= 460800`, `serializedJobBytes <= 972800`,
  `nativeMessageBytes <= 972800`, `statusPageBytes <= 1048576`; a numeric
  `renderTimeMs` must be `< 30000`.
- `observedMaxima` has exactly the keys of `approvedLimits`
  (`transcriptBytes`, `timestampedTranscriptBytes`, `serializedJobBytes`,
  `nativeMessageBytes`, `statusPageBytes`, `renderTimeMs`), equals the
  computed sample maxima, and is strictly below the approved limit.
- `renderTimeMs` may be `null` **only** while
  `renderTimeMeasurement.status === "pending_live_measurement"`; the
  `approvedLimits` object is pinned exactly as shown in the fixture.
- Sample identity fields must match a supported course/term pair in the
  course mapping.

### Overview correlation rule

The lecture page has no date element. The parser fetches the linked overview,
selects the `.recording` card whose `.play-link a[href]` equals the
canonicalized current player URL, and parses the `.rec-date` prefix
`M/D/YYYY`. Exactly one match is required; zero matches, multiple matches, a
failed fetch, or a sign-in page fail closed as `rejected_ambiguous_metadata`.
The time-of-day tail after the bullet separator is ignored. The card badge is
a category label, never an identity: only a `Discussion - 0NN` badge yields
`discussionSection`, and any other badge yields `null`.

## Data flow / what is exercised

- **Build**: `scripts/build-extension.mjs` reads
  `lecture-page.selectors.json`, runs `validateSelectors`, and passes the
  object to esbuild as
  `define: { __STAGE0_SELECTORS__: JSON.stringify(selectorsFixture) }` for the
  content bundle. `extension/src/content.ts` uses the injected global and
  returns `null` when it is undefined (non-extension page context).
  `course-mapping.json` is validated as a nonempty `courseMappings` array and
  is mirrored by hand in `extension/src/course-config.ts`; the options page
  can override the runtime allowlist through `chrome.storage.local`, falling
  back to the built-in copy.
- **Parser tests**: load fixture JSON/HTML at module scope, build a JSDOM
  document with an explicit URL, inject an `OverviewFetcher` returning
  `overview-page.html`, and compare the parse result against
  `lecture-page.expected.json` or the rejection contract. In-test string
  replacements create derived shapes (unmapped course, no match/multiple
  matches, malformed timestamps, discussion title) without committing new
  files.
- **Content tests**: use the same fixtures to drive activation and stability
  through the coordinator, and derive a `Discussion 2` page by replacing the
  recording title in `lecture-page.html` at runtime.
- **Packet validation**: `fixture-packet.test.ts` parses the HTML with JSDOM
  and asserts the selector policies are executable against the committed
  documents (indicator present, population nonempty, overview link and date
  regex match every `.rec-date`).
- **Size report**: evidence only; the meta-suite recomputes maxima and checks
  the cap relationships, but no runtime code consumes it.

## File responsibilities

| File | Fixture role and provenance | What it locks down |
| --- | --- | --- |
| `lecture-page.html` (63 lines) | Sanitized rendered lecture-01 page (EECS 484 - Fall 2026) captured 2026-09-20 in the open/transcript-visible state. Three representative compact rows (`0-`, `1-`, `1472-transcript-line`) stand in for the 1,473 real rows; timestamps `00:00`, `00:02`, `1:22:58`; instructor `[REDACTED]`; overview link `/leccap/site/sanitizedoverview`; `layout-loading` retained deliberately. | The supported page shape and row markup: `#title-header` course label, `content-header-recording-title`, `#sourcebar` control, `.transcript-viewer`/`.transcript-scroller`/`.transcript-row`/`.transcript-time`/`.transcript-text`. Parser and content suites parse it. |
| `lecture-page.selectors.json` (62 lines) | Observed selectors and page facts from the lecture-01 open/closed captures, the lecture-02 live check, and the authenticated overview (all 2026-09-20); `pageShape: "leccap-player-rendered-v1"`. | Selector policy, compound completion contract, `loadingIndicatorSelector: null` with `loadingRegionNote`, linked-overview date policy (`on_demand_fetch`, `absolute_player_href`, month-first), `dateYearPolicy`, `isSPA: false`, `stabilityDebounceMs: 1500`, `sanityMinChars: 50`. Embedded into the build and validated by `fixture-packet.test.ts` and the build script. |
| `lecture-page.expected.json` (26 lines) | Expected parser result for `lecture-page.html`, computed with the normative normalization and framed hash. `lectureDate: "2026-09-01"` from the overview card; `contentHash: 4bfc0f5736615f920fa7ad0f557ff1c20426f39525bfc8717f4d9f2a770ed0cd`; `derivedFrom: "both"`; `stableSnapshotCount: 2`; `discussionSection: null`. | The complete positive parse oracle: identity, date, both transcript forms, lecture key, hash, and notes explaining the stubbed URL, date source, badge rule, serialization, and `layout-loading` semantics. |
| `overview-page.html` (67 lines) | Sanitized authenticated EECS 484 - Fall 2026 overview captured 2026-09-20. Four `.recording` cards: lecture 01 (`r/sanitized01`, `9/1/2026`, badge `Lecture - 001`), lecture 02 (`r/sanitized02`, `9/3/2026`), the observed no-number card (`r/sanitized17`, `9/17/2026`, title `Lecture recorded on 9/17/2026`), and the discussion card (`r/sanitized21`, `9/8/2026`, title `Discussion 2, [REDACTED]`, badge `Discussion - 012`). | Exact player-link/date correlation and the month-first date policy; the no-number case feeding `no-number-lecture-page.expected.json`; the discussion badge section rule; the evidence that the repeated `Lecture - 001` badge is a category label, not a number. |
| `no-number-lecture-page.html` (63 lines) | Identity fixture identical to `lecture-page.html` except the recording title, which is the real observed lag form `Lecture recorded on 9/17/2026`. | That an unnumbered lag title is resolved from the linked overview sequence by "next from the last one" (01 on 9/1, 02 on 9/3, then this card → 3) even though the badge shows `Lecture - 001`. The overview fetch is required for the derivation. |
| `no-number-lecture-page.expected.json` | Expected result for the lag-title fixture: `supported: true`, `numberSource: "derived"`, `lectureNumber: 3`, `lectureKey: "eecs484/2026-fall/003"`; the badge is never used as the number. | The derived-identity contract and its human-readable rationale. |
| `decoy-title-page.html` + `decoy-title-page.expected.json` | Negative identity fixture identical to `no-number-lecture-page.html` except the title, which is the observed decoy `DISREGARD -- Empty discussion`. | That an unrecognized title fails closed as `rejected_ambiguous_metadata` and never becomes a job. |
| `loading-transcript-page.html` (22 lines) | Negative fixture: the player shell while the transcript is loading. `#sourcebar` button title is `Hide Transcript`, but `.transcript-viewer` contains only the text `Loading transcript` and no rows. | That a populated row set (not merely an open control) is required: the parser reports `completion: "not_ready"`, `status: "not_ready"`, no job. |
| `non-lecture-page.html` (14 lines) | Negative fixture: an unrelated Leccap overview-style page with no player shape. | That a non-lecture page yields `rejected_missing_identity` and the content coordinator submits nothing on an ordinary visit. |
| `course-mapping.json` (22 lines) | Personal allowlist captured 2026-09-20; notes describe the observed header-label source, the 2026-09-22 scope, and the slug rule. Contains `EECS 484` and `EECS 491`, both `2026-fall`. | The exact supported course/term pairs; unmapped labels fail closed. Validated by `fixture-packet.test.ts` and mirrored in `extension/src/course-config.ts`. |
| `transcript-size-report.json` (78 lines) | Byte-cap evidence captured 2026-09-21T02:40:30Z. Sample 1 is the exact lecture-01 rendered capture (1,473 rows, 46,513 plain bytes, 58,893 timestamped bytes, 110,112 serialized-job bytes, 110,239 native-message bytes); sample 2 is the lecture-02 live observation marked `upperBound: true` (1,577 rows, 49,177 / 59,523 / 113,541 / 113,672 bytes). Both `renderTimeMs` values and `observedMaxima.renderTimeMs` are still `null`. | The approved caps and the proof that observed byte maxima are strictly below them; the render-time measurement contract (`pending_live_measurement`, 30,000 ms fail threshold, recipe) and the queue file-mode checks. |

## Testing and verification

- `extension-tests/fixture-packet.test.ts` (16 tests) is the packet's own
  guard: selector field/policy validation, placeholder ban, course mapping
  uniqueness, expected-output contracts, size-report caps/maxima, and HTML
  sanitization (media URLs, secrets, route IDs).
- `extension-tests/leccap-parser.test.ts` (16 tests) proves the positive and
  negative fixtures produce exactly the expected and rejected results.
- `extension-tests/content.test.ts` (44 tests) consumes the fixtures through
  the full capture pipeline.
- `extension-tests/build-smoke.test.ts` (1 test) proves the selectors are
  embedded in `dist/extension/content.js` with no unresolved placeholder.
- `npm test` on branch `agent/docs-b` (2026-09-23): 16 files / 224 tests
  passing. See `extension-tests/ARCHITECTURE.md`.
- Owner-only work that remains (cannot be verified offline):
  - Replace both `renderTimeMs: null` values with measured values from
    `docs/render-time-snippet.js`, then recompute `observedMaxima`; a sample
    reaching 30,000 ms fails Stage 0. The recipe is in
    `docs/STAGE_0_REPORT.md` and repeated in the fixture itself.
  - The machine-local GitHub provisioning packet (config, App installation,
    branch state) is not part of this directory and is never committed.

### Open questions and inconsistencies

1. **Course allowlist size.** `course-mapping.json` contains two entries
   (`EECS 484` and `EECS 491`, both `2026-fall`), and its own notes say this is
   the scope "as of 2026-09-22". `docs/STAGE_0_REPORT.md` states that
   `EECS 484` + `2026-fall` is the only course/term observed and that
   `course-mapping.json` "contains exactly that entry"; `docs/TESTING.md`
   likewise describes the allowlist as "currently `eecs484` / `2026-fall`".
   The Stage 0 report records no evidence for the `EECS 491` entry (the plan
   mentions `EECS 491 Winter 2026` only as an illustrative example). The
   fixture and the mirrored `extension/src/course-config.ts` agree with each
   other, so this is a documentation/evidence gap to resolve with the owner,
   not a test failure.
2. **Render-time measurement pending.** Both `renderTimeMs` fields and
   `observedMaxima.renderTimeMs` are `null`; the meta-suite accepts this only
   while `renderTimeMeasurement.status` is `pending_live_measurement`. The
   plan requires measured values before the Stage 0 gate is complete.
3. **Stale comment reference.** `lecture-page.html`'s comment points to
   `lecture-page.selectors.json "overviewPage"`, but the fixture's actual key
   is `lectureDateSource`. Cosmetic only.
4. **`sourceUrlPublish` is not asserted.** `lecture-page.expected.json`
   records `"sourceUrlPublish": "include"`, and the sanitized URL contains no
   sensitive path segment, but no suite asserts this field; publish omission
   is tested separately with constructed URLs in `transcript-job.test.ts`.
5. **Representative rows, not the full transcript.** The fixture keeps three
   rows from the 1,473-row capture. Byte measurements and row counts in the
   size report come from the real capture, not from the fixture, so the
   fixture must not be used to recompute those sizes.

## Related plan sections

- `TECHNICAL_PLAN.md`: "Required Stage 0 packet" (the exact required
  artifacts and the rule that placeholder schemas do not satisfy the gate),
  "Required provisioning packet", "Date/time formats" (linked overview,
  `on_demand_fetch`, month-first `M/D/YYYY`), "Normalization" and the
  serialization paragraph for `[<verbatim .transcript-time>] <text>`, the
  course-mapping identity rules, "Canonical source-URL vectors", and the
  "Phase 1 guardrails" table.
- `extension-tests/fixtures/IMPLEMENTATION.md`: work items (render-time
  values, fixture validation test, sanitization, badge rule, no new courses
  without evidence) and the "Do not do" list.
- `docs/STAGE_0_REPORT.md`: gate status per artifact, evidence table,
  measured size summary, and the two outstanding owner items.
- `docs/STAGE_0_LECCAP_OBSERVATIONS.md`: raw observation trail and the
  recorded resolutions (completion contract, date source, timestamp
  serialization, badge rule, inventory).
- `docs/TESTING.md`: fixture packet table and the sanitization statement.
- `extension-tests/ARCHITECTURE.md`: how the suites consume the packet.

## How to change this directory safely

1. New page shape or selector change: add a dedicated parser fixture and a
   plan/decision update first. Never widen a selector silently or reuse a
   fixture for an unobserved shape.
2. New course or term: require a new verified page observation, add the entry
   to `course-mapping.json` and the mirrored `COURSE_MAPPINGS`, and update the
   Stage 0 report. Unmapped labels must keep failing closed.
3. Never raise a cap to make a sample pass. Add a measured sample, recompute
   `observedMaxima`, and keep every maximum strictly below its approved limit.
4. Preserve sanitization: no user identifiers, cookies, session values, media
   or WebVTT URLs, and only `sanitizedNN` route IDs. Never copy a raw capture
   into this directory.
5. Replace the `renderTimeMs` nulls only with real measured values and
   recompute `observedMaxima`; the meta-suite will then require them to be
   below 30,000 ms.
6. Keep `lecture-page.expected.json` derived from the normative normalization
   and framed hash; regenerate the hash by computation, never by hand.
7. Keep the fixture allowlist and `extension/src/course-config.ts` in sync by
   hand; the parser suites read the fixture while `course-storage.test.ts`
   validates the built-in copy, and no automated check currently compares the
   two against each other.
8. Treat fixture prose as documentation, never as a substitute for an observed
   value or as runtime input. Run `npm test` after any packet change.
