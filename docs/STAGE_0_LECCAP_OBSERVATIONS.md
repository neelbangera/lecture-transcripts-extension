# Leccap Stage 0 Observation Notes

> Status: `EVIDENCE_RECORD / RESOLVED_INTO_STAGE_0_PACKET (2026-09-20)`
>
> This is an evidence note, not a replacement for `TECHNICAL_PLAN.md`. The
> observations below were resolved on 2026-09-20 into the committed Stage 0
> packet: `docs/STAGE_0_REPORT.md` (decision record), the selector/expected/
> course/size fixtures under `extension-tests/fixtures/`, and deliberate plan
> revisions for the completion contract, the overview date source, and the
> timestamped-form serialization. Read with those files; this note remains the
> raw-evidence trail.

## Source capture

The source material was one authenticated, rendered Leccap lecture page:

- Page title: `EECS 484 - Fall 2026 - 01 Intro, Almomani`
- Visible course/term label: `EECS 484 - Fall 2026`
- Visible recording label: `01 Intro, Almomani`
- Capture date: 2026-09-20
- Transcript state: visible and populated when the page was saved
- Transcript rows: 1,473
- First observed timestamp: `00:00`
- Last observed timestamp: `1:22:58`
- Approximate serialized text under the transcript scroller: 53 KB

The page was saved after client-side rendering. It is a React application; the
saved DOM is not the same thing as the original server HTML or the page source.

## Landing-page metadata evidence

A separate authenticated landing-page capture for `EECS 484 - Fall 2026` was
also inspected. It contains a recording list with these relevant elements:

- `#recordings` contains one `.recording` card per item;
- `.play-link a` points to the corresponding player page;
- `.rec-title` contains the recording title;
- `.rec-date` contains a date and time such as `9/1/2026 • 4:29 PM`;
- the `.badge`/`Lecture - 001` text identifies the recording category, not the
  lecture number.

The first landing-page card links to the same player route as the captured
`01 Intro, Almomani` page and supplies `9/1/2026`. This establishes a reliable
date source in the authenticated overview page, even though the lecture page
itself has no date element.

The lecture page also contains a direct overview link at
`#title-header a.content-header-site-btn[href]` (the observed href is the
course's `/leccap/site/...` route). The deterministic correlation candidate is
therefore: fetch or read that linked overview, select the `.recording` whose
`.play-link a[href]` matches the current player URL after the plan's URL
canonicalization, and read its `.rec-date`. This is evidence for a possible
runtime mapping, not a decision to fetch or cache it.

The landing page contained nine cards: six lecture-category cards and three
discussion-category cards. The first five lecture titles begin with numeric
labels (`01` through `05`), while at least one later lecture card is titled
`Lecture recorded on 9/17/2026` and has no numeric lecture label. This is an
identity-policy edge case, not evidence that the category badge is a lecture
number.

The downloaded landing page is discovery evidence only. A runtime
implementation must still decide how an authenticated lecture page obtains
the overview mapping: on-demand fetch, a cache populated by visiting the
landing page, or an explicit unsupported state. It must not read the local
download as runtime input and must not guess a date.

## Second live lecture observation

On 2026-09-20, a second permitted live lecture page was opened and inspected:

- recording title: `02 The Entity-Relationship Model, Almomani`;
- the initial page briefly exposed a literal `loading` state;
- after the player loaded, the transcript control was `Show Transcript` and
  no transcript viewer was present;
- after the control was clicked, it changed to `Hide Transcript` and the
  transcript viewer appeared with 1,577 rows;
- observed timestamp range was `00:01` through `1:21:29`;
- raw DOM-derived UTF-8 sizes were approximately 49,177 bytes for spoken text
  and 59,523 bytes for timestamped text.

The existing first-page capture measured 1,473 rows, `00:00` through
`1:22:58`, approximately 47,985 bytes of spoken text, and 57,419 bytes of
timestamped text using the same raw row serialization. These are discovery
measurements, not the final serialized-job measurements required by the plan.

The closed/open captures establish the same activation behavior for the first
lecture: the closed page has the `Show Transcript` control and no transcript
viewer; the open page has the `Hide Transcript` control and the populated
transcript subtree.

## Observed DOM shape

The following structure was present in the rendered page:

    #root
      .App
        .Main.layout.layout-main.layout-loading
          .content.clearfix
            #title-header
              .content-header-site-btn span
              .content-header-recording-title
              #sourcebar
                button.showing[title="Hide Transcript"]
          .content-inner
            .viewer
              .feature-content-pane
                .transcript-viewer
                  h2.visuallyhidden
                  .esc-notice
                  .toolbar[role="search"]
                    #search_transcript
                  .transcript-scroller[role="list"]
                    .transcript-row[role="listitem"]
                      .transcript-time
                      .transcript-text

Each transcript row had an index-derived ID such as
`0-transcript-line` and `1472-transcript-line`. Those IDs are useful evidence
of the current rendering shape but are not stable identity values and must not
be used as lecture identity.

The saved page showed no transcript iframe. The transcript appeared in the
main document DOM. A separate browser-extension root with a closed shadow root
was present at the end of the document; it is unrelated to the transcript and
must not be included in the fixture.

## Candidate selectors observed

These are observations from this page, not yet cross-page-validated production
selectors:

| Purpose | Observed element |
| --- | --- |
| Course and term label | `#title-header .content-header-site-btn span` |
| Recording label | `#title-header .content-header-recording-title` |
| Transcript control | `#sourcebar` button whose text is `Transcript` and whose open-state title is `Hide Transcript` |
| Transcript root | `.feature-content-pane .transcript-viewer` |
| Transcript list | `.transcript-viewer .transcript-scroller` |
| Transcript row | `.transcript-scroller .transcript-row` |
| Timestamp | `.transcript-row .transcript-time` |
| Spoken text | `.transcript-row .transcript-text` |

The control title is state-dependent: a collapsed transcript is expected to
use a `Show Transcript` title. This must be confirmed on a live page before
choosing a selector.

For the landing page, the observed metadata selectors are `.recording`,
`.play-link a`, `.rec-title`, `.rec-date`, and `.badge`. These selectors belong
to the overview-page lookup, not to the lecture-page transcript parser. The
lecture-page overview-link selector is
`#title-header a.content-header-site-btn[href]`.

## Transcript and timestamp observations

The transcript is represented as one list item per caption line. The timestamp
and spoken text are separate child elements. Text can contain line breaks
inside `.transcript-text`; the fixture preserves this possibility.

The observed timestamps use two forms:

- `MM:SS`, such as `00:00`;
- `H:MM:SS`, such as `1:22:58`.

No brackets, fractional seconds, or delimiter characters were observed in this
capture. The page therefore fits the current timestamp pattern for this sample,
but a second lecture must still be checked.

The saved DOM contains no lecture date. The course and term can be read from
the visible header, and lecture number `01` can be read from the recording
label, but no date selector or date value was found in this capture. Do not
invent a date from the download timestamp, recording URL, or media filename.

## Loading, completion, and activation findings

The static captures are completed-looking pages, but the second live page also
provided a short loading-to-ready observation:

- `layout-loading` is present on the populated page. It is therefore not a
  reliable completion marker.
- The initial live page exposed a literal `loading` label before the player
  was ready, but it was outside the transcript subtree.
- No explicit loading/error element was found inside the transcript subtree.
- No explicit transcript-complete element or completion attribute was found in
  the saved DOM.
- The live second page proved that the transcript control is `Show Transcript`
  before activation and `Hide Transcript` after activation. The populated
  transcript appeared after the click.
- A static download cannot establish whether the page is a single-page
  application or how the DOM changes when moving to another lecture.

The current evidence supports a populated transcript as an observed ready
state, but it does not satisfy the technical plan's stronger requirement for
an explicit completion indicator. The observer cannot see the initial page
loading label if it is restricted to the transcript subtree. This must be
resolved in the plan before parser/content implementation: either identify a
real completion signal in the supported page shape, or deliberately revise the
completion contract to accept a populated, stable transcript after activation.

The bundled player code indicates that transcript rows are rendered from
caption data after the recording/product request is loaded. That is useful
context, but it does not provide an observed DOM completion signal for the
extension. A live observation of the loading state is still required.

## Safety and disposition

The original downloads contained more than the parser needs, including
authenticated-page markers, recording/site identifiers, media URLs, a
WebVTT URL with a query value, thumbnail URLs, tracking resources, and the
full transcript. Those originals must not be used as committed fixtures or
copied into future prompts.

`extension-tests/fixtures/lecture-page.html` is a small sanitized discovery
fixture containing only the relevant DOM shape and a short representative
transcript. It is intentionally not a complete Stage 0 fixture because the
date and completion contract are unresolved.

## Remaining decisions and evidence required before implementation

Resolved 2026-09-20 — see `docs/STAGE_0_REPORT.md` and the plan revisions for the recorded contracts:

1. Runtime date lookup: **selected** — on-demand authenticated fetch of the linked overview (`#title-header a.content-header-site-btn[href]`), exact play-link correlation, month-first `M/D/YYYY` parsing, fail-closed on zero/multiple matches. The rec-date is accepted as the lecture recording date (the "Lecture recorded on 9/17/2026" card carries rec-date 9/17/2026).
2. No-number cards: **recorded** — rejected as `rejected_ambiguous_metadata`; the badge is never a lecture number; committed negative fixture `no-number-lecture-page.html` + expected result.
3. Completion contract: **revised deliberately** — the observed shape has no in-container complete marker or loading/error region; completion is the compound open-state title + populated rows + two identical snapshots rule; `loadingIndicatorSelector: null` recorded in the selectors fixture.
4. Render times: **still a live measurement** — recipe in `docs/STAGE_0_REPORT.md` / `docs/render-time-snippet.js`; byte measurements are complete in `transcript-size-report.json`.
5. SPA behavior: **recorded** — `isSPA: false` (full-document navigation into players observed); the popstate + 1000 ms URL poll remains installed as defense.
6. Course allowlist: **recorded** — `course-mapping.json` with the observed personal set (EECS 484 + 2026-fall); extension requires a new verified entry.
7. Provisioning: **still user-side** — machine-local config + GitHub App per `docs/STAGE_0_REPORT.md`.

Stage 1/2 implementation may proceed against the recorded contracts; the two
live items above gate later stages (render time: before trusting captures on
slow connections; provisioning: before Stage 5 integration and Stage 8).
