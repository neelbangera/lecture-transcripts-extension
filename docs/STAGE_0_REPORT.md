# Stage 0 Report

Status: `STAGE_0_CODE_READY — one live measurement + machine-local provisioning outstanding`

This report records the Stage 0 discovery evidence for the Automatic Leccap
Transcript Uploader. All design blockers from the earlier incomplete pass are
resolved with observed evidence and recorded plan revisions. Two items remain
and both are inherently live/user-side: the per-sample render-time measurement
(recipe below, ~2 minutes) and the machine-local GitHub provisioning packet.
Neither blocks writing code against the recorded contracts: Stage 1 (skeleton
and shared contracts), Stage 2 (parser and fixtures), Stage 3 (extension
runtime), and Stage 4 (native host and queue) can proceed now, because the
30-second observation budget is fixed by the plan regardless of the measured
value and all unit tests use fake configuration by design. Stage 8
(end-to-end verification) requires the provisioning packet.

Detailed raw observations are in
[`STAGE_0_LECCAP_OBSERVATIONS.md`](STAGE_0_LECCAP_OBSERVATIONS.md).

## Gate status by required packet item

| Required artifact | Status | Location |
| --- | --- | --- |
| `docs/STAGE_0_REPORT.md` | Complete (this file) | `docs/STAGE_0_REPORT.md` |
| Lecture-page fixture | Complete — sanitized, page-faithful, compact row markup | `extension-tests/fixtures/lecture-page.html` |
| Selectors/page-facts fixture | Complete — real observed values, matches the plan template incl. `lectureDateSource` | `extension-tests/fixtures/lecture-page.selectors.json` |
| Expected parser result | Complete — computed with the normative normalization + framed hash | `extension-tests/fixtures/lecture-page.expected.json` |
| Course/term allowlist | Complete — current personal set | `extension-tests/fixtures/course-mapping.json` |
| Size measurements | Complete for all byte caps; `renderTimeMs` pending live measurement | `extension-tests/fixtures/transcript-size-report.json` |
| Negative identity fixture | Complete (added beyond the required set) | `extension-tests/fixtures/no-number-lecture-page.html` + `.expected.json` |
| Overview fixture (required by `lectureDateSource.page=linked_overview_page`) | Complete | `extension-tests/fixtures/overview-page.html` |
| Render-time snippet (live measurement recipe) | Complete | `docs/render-time-snippet.js` |
| Provisioning packet | Outstanding — user machine-local setup | see "Provisioning" below |

## Evidence completed

| Area | Result | Evidence |
| --- | --- | --- |
| Lecture DOM | Pass | Lecture 01 open and closed rendered captures plus a live lecture 02 check show the same header, transcript control (`#sourcebar` button), transcript viewer (`.transcript-viewer`), row (`.transcript-row`), timestamp (`.transcript-time`), and text (`.transcript-text`) structure. |
| Activation | Pass | Closed state: `class="hiding"` + `title="Show Transcript"`, no transcript viewer in the DOM. Open state: `class="showing"` + `title="Hide Transcript"` with the populated viewer. Both states observed live (lecture 02) and in captures (lecture 01). |
| Already-expanded activation | Pass | Lecture 01 was saved with the transcript already open; the populated viewer + open-state control are unambiguous. Per plan, a visible, populated, already-expanded transcript at script start counts as activation. |
| Transcript rendering model | Pass | All 1,473 row IDs (`0-transcript-line` … `1472-transcript-line`) are present and contiguous in the populated capture — no list virtualization; the full transcript is in the DOM once rendered. No row has empty text. |
| Timestamp shape | Pass | `MM:SS` and `H:MM:SS`, no fractional seconds, no brackets or delimiters in the source elements. Mechanically verified: 100% of the 1,473 serialized `[time] text` prefixes are fully consumed by the plan's normative timestamp regex, including the bracket and single-space delimiter. |
| Metadata sources | Pass | Course/term from `#title-header .content-header-site-btn span` ("EECS 484 - Fall 2026"); lecture number from the recording-title numeric prefix (`01`–`05` observed); date from the overview page (below). |
| Lecture date source | Pass (policy selected) | The lecture page has no date element. Selected: `linked_overview_page` + `on_demand_fetch`. The overview card whose `.play-link a[href]` equals the canonicalized player URL supplies `.rec-date` (`M/D/YYYY • h:MM AM/PM`; month-first proven by the 9/17 and 9/18 samples; time of day ignored). Recorded in the plan and in the selectors fixture. |
| Identity edge cases | Pass | Every lecture card carries the same badge `Lecture - 001` — the badge is a category label, never a lecture number (plan and decisions doc updated). Real observed no-number titles (`Lecture recorded on 9/17/2026`, `Lecture recorded on 9/18/2026`) fail closed as `rejected_ambiguous_metadata`; negative fixture committed. |
| Completion contract | Resolved by plan revision | The page has no explicit in-container complete marker and no in-container loading/error region. The plan was deliberately revised (2026-09-20): completion is the compound rule — control `title="Hide Transcript"` + at least one populated row + two identical normalized snapshots 1500 ms apart; `loadingIndicatorSelector: null` is allowed and recorded. The page-level `layout-loading` class persists after completion and the initial `loading` label is outside the container — both documented as non-indicators. |
| SPA behavior | Pass (observed) | Recordings open by full document navigation from the overview (anchor navigation; each capture is a complete document with a full head). `isSPA: false` recorded; the plan's `popstate` + 1000 ms URL poll remains installed as defense for any in-player URL change. |
| Source-URL safety | Pass | Player URLs are path-only (`https://leccap.engin.umich.edu/leccap/player/r/<id>`); canonicalization keeps the path, drops query/fragment. Path segments contain no token/session/auth/sid whole-segment match, so `source_url` is published. |
| Sensitive-data handling | Pass | Raw captures (`/open.html`, `/closed.html`, the MHTML) contain authenticated-page markers (including an inline user identifier), recording/site route IDs, media and WebVTT URLs. They are gitignored via the new root `.gitignore` and must never be committed or pasted into prompts. Committed fixtures are sanitized ([REDACTED] names, `r/sanitizedNN` route IDs, no media URLs). |
| Byte-cap proof | Pass | All measured maxima are strictly below the approved caps — see the size table below. |
| Course inventory | Pass for current set | `EECS 484` + `2026-fall` is the only course/term observed on permitted pages; `course-mapping.json` contains exactly that entry. Adding a course later = new verified observation + new mapping entry; unmapped labels fail closed. |

## Measured size report (summary)

From `extension-tests/fixtures/transcript-size-report.json` (full method and
per-sample records there):

| Measure | Observed maximum | Approved limit | Result |
| --- | --- | --- | --- |
| Plain transcript (UTF-8, normalized) | 49,177 B (upper bound) | 460,800 B | pass |
| Timestamped transcript (UTF-8, normalized) | 59,523 B (upper bound) | 460,800 B | pass |
| Serialized TranscriptJob (canonical JSON) | 113,541 B | 972,800 B | pass |
| `submit_job` Native Messaging payload | 113,672 B | 1,048,576 B | pass |
| Worst-case 50-row status page | 63,155 B | 1,048,576 B | pass |

Sample 1 (lecture 01, exact computation from the rendered capture): 1,473 rows,
46,513 plain bytes, 58,893 timestamped bytes, 110,112 serialized-job bytes.
Sample 2 (lecture 02, live observation) is recorded as a documented upper
bound: raw pre-normalization text sizes with sample 1's JSON escape overhead
ratio applied; NFC was verified to be a no-op on sample 1, and normalization
only removes whitespace on this content, so the bounds are conservative.
Computed fixture hash (normative framed `transcript-hash-v1`):
`4bfc0f5736615f920fa7ad0f557ff1c20426f39525bfc8717f4d9f2a770ed0cd`.

## Plan revisions recorded (2026-09-20)

The plan and `PHASE_1_DECISIONS.md` were deliberately updated before any
parser/content implementation, as the plan requires:

1. **Completion contract**: compound completion for the observed shape
   (open-state control title + populated rows + two stable snapshots);
   `loadingIndicatorSelector` may be `null` with an explicit recorded
   rationale; `layout-loading` and the out-of-container `loading` label are
   non-indicators.
2. **Lecture date source**: `linked_overview_page` + `on_demand_fetch`, exact
   player-link correlation, month-first `M/D/YYYY` parsing, fail-closed on
   zero/multiple matches or fetch failure; rec-date accepted as the lecture
   recording date (evidenced by the "Lecture recorded on 9/17/2026" card).
3. **Timestamped serialization**: rows serialized as `[<verbatim time>] <text>`;
   normalization rejoins a detected prefix and remainder with a single space;
   idempotent; every observed prefix consumed by the normative regex.
4. **Badge/identity rule**: the overview badge is never a lecture number; a
   title without a numeric prefix fails closed (`rejected_ambiguous_metadata`).
5. **Inventory**: added the overview fixture and the no-number negative
   fixture + expected result; `.gitignore` pulled forward from Stage 1 to
   protect raw captures immediately.

## Outstanding item 1 — render-time measurement (~2 minutes, live)

`renderTimeMs` (activation → second matching stable snapshot) must be recorded
per sample on a real permitted page. Both live observations showed the
populated transcript appearing immediately after the activation click with no
stall, but the plan requires measured values.

Recipe: open each sample lecture page, paste
[`docs/render-time-snippet.js`](render-time-snippet.js) into the DevTools
console before expanding the transcript, click `Show Transcript` once, and
record the printed milliseconds into the two `renderTimeMs` fields in
`extension-tests/fixtures/transcript-size-report.json` (and recompute
`observedMaxima`). Fail criteria: any supported sample reaching 30,000 ms or
timing out fails Stage 0 and forces a plan revision.

## Outstanding item 2 — machine-local provisioning packet

Required at `~/Library/Application Support/LectureTranscripts/config.json`
(never committed), per the plan's exact shape:

```json
{
  "schemaVersion": 1,
  "githubAppClientId": "<real public client ID of the GitHub App>",
  "repositoryId": <numeric ID of neelbangera/lecture-transcripts>,
  "owner": "neelbangera",
  "repo": "lecture-transcripts",
  "branch": "main"
}
```

Setup steps for the owner:

1. Create/verify the repository `neelbangera/lecture-transcripts` with at
   least one commit on `main` (e.g. a README via the GitHub UI); the uploader
   never initializes an empty repository. The numeric repository ID is
   visible via `GET https://api.github.com/repos/neelbangera/lecture-transcripts`
   (the `"id"` field, no auth needed for a public repo; use the authenticated
   value if private).
2. Create one GitHub App: enable **Device Flow**, request only **Repository
   contents: Read and write**, and install it **only** on
   `neelbangera/lecture-transcripts`. Copy the App's public **client ID**
   (from the App settings page) into the config file.
3. Verify before first connect: the config file parses with exactly the fields
   above; the App is installed on that repository; `main` has ≥1 commit.
   The uploader performs the repository/Contents sanity checks at connect and
   fails closed as `target_repository_unavailable` otherwise.

No client secret or App private key exists in this design; the extension never
receives any credential. Stage 7 separately requires the loaded unpacked
extension ID for the Native Messaging host manifest — supplied explicitly to
the installer at that point.

## Next steps

1. Owner: run the render-time snippet on both samples and paste the two
   numbers into the size report; complete the provisioning packet.
2. Implementation may begin with Stage 1 (project skeleton, protocol
   contracts, shared vector files) and Stage 2 (parser against the committed
   fixtures) immediately; the recorded contracts are final for these stages.
3. Re-run the full gate checklist above before Stage 8 end-to-end
   verification.
