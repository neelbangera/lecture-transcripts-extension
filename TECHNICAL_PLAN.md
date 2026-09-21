# Technical Plan — Automatic Leccap Transcript Uploader

> This document is the absolute source of truth and the guardrails for the code.
> All implementation must follow this plan. If code conflicts with this doc, the doc wins.
> PHASE_1_DECISIONS.md records the rationale and must agree with this plan.
> TECHNICAL_IMPLEMENTATION_PROPOSAL.md is historical and is not an implementation authority.

The implementation relies on three external contracts and must follow their current official documentation: [Chrome Native Messaging](https://developer.chrome.com/docs/apps/nativeMessaging), [GitHub App user authorization and device flow](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app), and the [GitHub repository contents API](https://docs.github.com/en/rest/repos/contents). If one of those contracts changes, update this plan before implementation continues.

## The Problem

University of Michigan engineering lectures are recorded on Leccap, but their transcripts stay locked inside the browser player. Right now, saving one means manually opening the transcript, copying the text, and filing it away — slow and easy to skip. The goal is to automatically save those transcripts to a personal GitHub repository, organized by course and lecture, after you open the transcript. Opening the transcript is the deliberate capture action; once it has been opened, no separate upload click should be required. This creates a clean, searchable, AI-readable archive for studying.

## The Technical Plan

The system has four big pieces that work in a chain. First is the Leccap lecture page itself, where you watch lectures while logged in. Second is a small Chrome extension that watches only those Leccap pages and reads the transcript text after you open it — it never takes your passwords, cookies, or the whole page, just the transcript and basic info like course, lecture number, and date. Third is a small helper program on your Mac called the local uploader, reached through Chrome Native Messaging, which receives the transcript from the extension, saves it safely in a durable queue so nothing is lost if the internet drops, and stores the GitHub App user credential in the Mac Keychain. GitHub authorization starts through the website, but the extension never receives the credential. Fourth is your GitHub repository, where each lecture ends up as one tidy Markdown file sorted by course and term.

In plain terms: you open a lecture and click the transcript button once. The extension waits for the page's completion signal and two identical stable reads, cleans the transcript up, and hands it to the helper on your Mac. A bounded extension outbox keeps an unacknowledged handoff across a service-worker restart; it is cleared only after a definitive uploader acknowledgement, with a metadata-only recovery notice for a rejection that was not persisted. The helper then uploads it to GitHub and retries on its own if something fails. Revisiting the same lecture later does nothing if nothing changed. A different transcript at an already-used lecture path is reported as a conflict and is never overwritten automatically.

```text
┌──────────────────────┐
│ Leccap lecture page  │
│ (you open transcript)│
└──────────┬───────────┘
           │ transcript text + course / lecture / date only
           ▼
┌──────────────────────┐
│ Chrome extension     │
│ - spots lecture page │
│ - waits until        │
│   transcript is done │
│ - cleans + packages  │
│ - retains until ack  │
└──────────┬───────────┘
           │ one packaged lecture job
           ▼
┌──────────────────────┐
│ Local uploader (Mac) │
│ - saves job safely   │
│ - holds GitHub login │
│ - retries if needed  │
└──────────┬───────────┘
           │ upload file
           ▼
┌──────────────────────┐
│ GitHub repo          │
│ lecture-transcripts  │
│ one file per lecture │
└──────────────────────┘
```

The extension service worker is allowed to sleep. A named one-minute Chrome alarm wakes it, reconnects the Native Messaging port, and gives the uploader another chance to drain due jobs. The uploader is therefore durable across disconnects, but it is not a daemon when Chrome is completely closed.

## Scope and non-goals

The first version is for one person, one Mac, one Chrome installation, one authenticated Leccap session, one GitHub account, and one destination repository. Sharing the recordings and transcripts is expressly permitted. The destination repository may be private initially and may be made public later, so the output format and logs must be safe in either mode.

The system is activated when the user clicks or expands the transcript on a recognized lecture page, or when the content script observes a transcript that is already visible, expanded, and populated at injection time. It must not crawl Leccap, upload on ordinary page visits, or guess a repository path from incomplete metadata. The first version assumes that course, term, lecture number, and date are available from authenticated page metadata, but those fields do not have to live in the same DOM: the lecture page may provide course/term/title while its linked course overview provides the recording date. Stage 0 must record the source and correlation rule for every field and choose how any overview lookup occurs at runtime. The personal course set has no guest lectures or split recordings.

The following are not first-version goals:

- Historical crawling or sitemap walking.
- Uploading arbitrary pages, raw HTML, cookies, or request headers.
- Supporting multiple GitHub accounts, repositories, users, browsers, or operating systems.
- A hosted backend or multi-user authorization service.
- Automatically replacing or merging an existing lecture file.
- Updating course README files on every upload.
- Uploading while Chrome is completely closed. The durable queue resumes the next time the native host connects; a separately running background agent would be a later feature.

## Implementation readiness

The repository is no longer design-only. Its current status is `IMPLEMENTED_THROUGH_PACKAGING / LIVE_ITEMS_OUTSTANDING`: the capture-side TypeScript, the versioned protocol, the Go packages for config, queue, logging, retry, auth, GitHub publishing, Markdown, the serial processor, and the Native Messaging host, the `lecture-uploader` executable, the packaging scripts, and the packaging self-tests are all in the tree. The live items are the Stage 0 render-time measurement and the machine-local GitHub provisioning/loaded extension ID; both are owner-side and are never committed.

Stage 0's code-ready portion passed on 2026-09-20 (see `docs/STAGE_0_REPORT.md`): Stages 1-6 may proceed against the recorded contracts and have largely landed. The remaining live items are owner-side and gate Stage 8 end-to-end verification, not writing code against the recorded contracts. If a required fact is absent, placeholder-only, or contradictory, the agent must stop with `STAGE_0_INCOMPLETE`; it must not choose a plausible value.

The facts that remain outstanding are external measurements or account-provisioning inputs, not design details that can be inferred from this document:

- the per-sample render-time measurement from permitted pages; the byte measurements, selectors, page facts, and course mapping are recorded in the committed Stage 0 packet;
- the GitHub App client ID, numeric repository ID, installation, selected branch, initialized-main-branch state, and target-repository sanity check used by this personal installation;
- the loaded Chrome extension ID used by the Native Messaging host. This is intentionally late-bound to Stage 7, after the extension is built and loaded; it must be supplied explicitly to the installer and must never be guessed or left as a placeholder.

### Required Stage 0 packet

Stage 0 must produce all of the following committed, non-secret artifacts:

- `docs/STAGE_0_REPORT.md`: evidence and pass/fail results for page capture, course inventory, size measurements, and account/repository setup. It must identify the source pages and dates without copying private tokens or session data.
- `extension-tests/fixtures/lecture-page.html`: a redacted or synthetic fixture whose relevant structure is faithful to an actually permitted page.
- `extension-tests/fixtures/lecture-page.selectors.json`: the observed selectors and page facts described in the Stage 0 schema below. This must contain real values, not the illustrative schema, `TBD`, `e.g.`, `CSS selector string`, or a guessed completion rule.
- `extension-tests/fixtures/lecture-page.expected.json`: the expected parser result for the fixture, including the completion outcome and metadata. It is not satisfied by an expected-shape example.
- `extension-tests/fixtures/overview-page.html`: a redacted overview-page fixture when `lectureDateSource.page` is `linked_overview_page`; it must preserve the recording-card/link/date shape used for the correlation test.
- `extension-tests/fixtures/course-mapping.json`: the complete allowlist of supported course/term combinations. The EECS 491 Winter 2026 entry in this document is an example and is not a complete mapping.
- `extension-tests/fixtures/transcript-size-report.json`: measured UTF-8 byte sizes for representative permitted transcripts and serialized jobs, the largest `submit_job` Native Messaging payload, render times, the observed maxima, and the approved limits. No limit is approved until the measurements demonstrate that supported pages fit.

The packet is valid only when every required field has an observed or explicitly verified value. A schema/template with unresolved placeholders does not satisfy the gate. If the real page cannot provide a reliable completion indicator, the result is `unsupported` and implementation stops; the parser must not guess readiness from a timer alone.

### Required provisioning packet

The personal installation requires a machine-local provisioning file at `~/Library/Application Support/LectureTranscripts/config.json`. It is an input to the implementation and is never committed. Its required fields are `githubAppClientId`, numeric `repositoryId`, `owner`, `repo`, and `branch`. The file must be validated at startup and the uploader must fail closed when it is absent or incomplete. The extension ID is not a Stage 0 config value: Stage 7 obtains the ID from the actually loaded extension and passes it explicitly to the host-manifest installer.

The provisioning report must confirm that GitHub Device Flow is enabled for the App, the App is installed only on the target repository, the Contents permission is read/write, and the selected branch is exactly `main`. The actual client ID and repository ID must not be invented, embedded in source, or replaced with a fake value. Unit tests use explicit test configuration; integration tests require the real machine-local configuration. Stage 7 separately records the loaded extension ID and the exact rendered allowed origin; if it is unavailable, installation stops.

The machine-local configuration has this exact shape; the values shown as prose or zero are template markers and are invalid at runtime:

```json
{
  "schemaVersion": 1,
  "githubAppClientId": "real App client ID supplied during setup",
  "repositoryId": 0,
  "owner": "neelbangera",
  "repo": "lecture-transcripts",
  "branch": "main"
}
```

`repositoryId` must be a positive integer, `githubAppClientId` must be the real public client ID for the selected App, `owner` must be `neelbangera`, `repo` must be `lecture-transcripts`, and `branch` must be exactly `main`. The loader rejects unknown fields, missing fields, zero values, and mismatched target values. Paths, byte limits, and retry intervals come from the contracts in this plan rather than from mutable config fields.

### What a first-pass agent may and may not decide

The first-pass agent may resolve dependency patch versions within the toolchain baseline and record them in lockfiles. It may not change product behavior, invent page facts, expand the course allowlist, raise safety limits, broaden GitHub permissions, or alter the queue/hash/status contracts below. The contracts in this plan are normative; the Stage 0 packet supplies the facts that cannot be known from prose.

## Phase 1 guardrails

The fuller rationale for these choices is preserved in [PHASE_1_DECISIONS.md](PHASE_1_DECISIONS.md). These are implementation constraints, not suggestions for future optimization.

| Decision | Rejected alternative | Guardrail |
| --- | --- | --- |
| Transcript activation is the trigger | Upload on any page visit, home-page scan, or URL-only inference | Page navigation alone must never create a job. |
| Automatic handoff after activation | Manual copy/paste or an upload button for every lecture | After the transcript is opened, no second upload click is required. |
| Local uploader owns GitHub access | Extension calls GitHub directly | No token, refresh token, or private key in the extension. |
| GitHub App web authorization plus Keychain | Token pasted into or stored by the extension; plaintext credential file | The uploader owns device authorization and stores the user credential in Keychain. The App is installed only on the destination repository and requests only Contents read/write. |
| Personal local deployment | Hosted backend, multi-user accounts, or distributable extension | Do not add multi-user abstractions without an explicit scope change. |
| Uploader-owned durable queue plus bounded handoff outbox | In-memory state or two independent retry queues | The extension may retain only unacknowledged handoffs for replay; the uploader owns all acknowledged jobs, retry state, and GitHub state. |
| Native Messaging boundary | Permanent unauthenticated loopback HTTP server | A loopback server is development-only and must be authenticated and bounded. |
| At-least-once delivery with deduplication | Promise of exactly-once delivery | Use deterministic `lectureKey` and `contentHash`; retries must be safe. |
| Stability and sanity checks | Upload immediately on click or after a fixed sleep | The click authorizes capture but does not prove completeness. |
| Page metadata identity | URL-only identity or generalized recording identity | Missing or ambiguous metadata fails closed; never guess a path. |
| Narrow known page support | Every Leccap page shape, guest lectures, split recordings | New page shapes require an explicit parser fixture and path rule. |
| Selected transcript text | Full HTML, cookies, request headers, or arbitrary page capture | Only transcript text, selected metadata, and sanitized source data cross the boundary. |
| Private-first deployment | Public-by-default archive | The format must remain safe if the repository later becomes public. |
| Write-once lecture files | Automatic replacement or merge of changed files | Same hash is a no-op; different content is a permanent conflict. |
| One combined Markdown artifact | Separate plain and timestamped files | Both transcript forms live in one lecture file. |
| Direct writes to configured `main` | Pull request per lecture or mandatory review | Direct writes are limited to machine-owned lecture paths. |
| Targeted GitHub API writes | Maintaining a local clone and pushing | The uploader checks the remote path and processes jobs serially. |
| User-triggered current capture | Historical crawler or sitemap walk | Historical backfill is a separate feature, not an optimization. |

## Transcript job and state model

The browser-to-uploader contract is a small, explicit JSON job. It must not be an arbitrary page dump.

```text
TranscriptJob {
  schemaVersion
  lectureKey
  courseSlug
  courseName
  term
  lectureNumber
  lectureDate
  sourceUrl
  capturedAt
  transcript
  timestampedTranscript
  contentHash
}
```

`lectureKey` is deterministic from the normalized course, term, and lecture number. It is the identity used for queue deduplication and the repository path. Format is strict:

```text
courseSlug: lowercase alphanumeric only, e.g. "EECS 491" -> "eecs491", must match ^[a-z0-9]+$
term: YYYY-season lowercase, e.g. "Winter 2026" -> "2026-winter", season in {winter,spring,summer,fall}. "Spring 2026" and "Summer 2026" are separate terms; the combined label "Spring/Summer 2026" is not a Phase 1 term and must fail closed.
lectureNumber: integer 1-999, stored as integer in job + frontmatter, zero-padded to 3 digits only in filename/path
lectureKey: "<courseSlug>/<term>/<lectureNumber:03d>", e.g. "eecs491/2026-winter/006"
stable path: "courses/<courseSlug>/<term>/lectures/<lectureNumber:03d>.md", e.g. "courses/eecs491/2026-winter/lectures/006.md"
```

Example course mapping (illustrative only; the complete table is the Stage 0 artifact `extension-tests/fixtures/course-mapping.json` and is copied into `extension/src/course-config.ts` during Stage 2):

```text
"EECS 491" + "Winter 2026" + lecture 6 -> courseSlug=eecs491, term=2026-winter, lectureKey=eecs491/2026-winter/006
human term "Winter 2026" <-> normalized "2026-winter"; "Fall 2025" <-> "2025-fall". Parser accepts case-insensitive /^(Winter|Spring|Summer|Fall)\s+(\d{4})$/ and emits both forms. It does not add a `spring-summer` enum: separate Spring and Summer courses remain separate, and a page that literally reports "Spring/Summer" is rejected as ambiguous.
```

Date/time formats (strict):

```text
lectureDate input: selected Stage 0 policy (2026-09-20) is `linked_overview_page` with `on_demand_fetch`. The lecture page itself has no date element, so the content script GETs the overview link found at `#title-header a.content-header-site-btn[href]` (a relative `/leccap/site/...` path, resolved against the page origin), selects the `.recording` card whose `.play-link a[href]` equals the canonicalized current player URL, and parses that card's `.rec-date` prefix `M/D/YYYY` (month-first, proven by the 9/17/2026 and 9/18/2026 samples; the time of day is ignored). Zero matches, multiple matches, a failed fetch, or an unparseable date fail closed as rejected_ambiguous_metadata; the fetch is same-origin, carries the existing Leccap session, and is not cached beyond the capture. The `.rec-date` value is the lecture's recording date (evidenced by the observed card titled "Lecture recorded on 9/17/2026" carrying rec-date 9/17/2026) and is the published `date` frontmatter value. If an observed page supplies an unambiguous month/day without a year (e.g. "Feb 12"), infer the year from the already-validated term. Never infer an ambiguous numeric month/day ordering, and reject an invalid or otherwise unparseable date as rejected_ambiguous_metadata.
frontmatter date: YYYY-MM-DD as parsed.
capturedAt: RFC3339 UTC from the current clock truncated to whole seconds (milliseconds removed before serialization), e.g. "2026-02-12T18:03:22Z". A raw JavaScript Date.toISOString() value with `.sssZ` is invalid. Must match ^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$.
Slug collision: if two distinct courseNames normalize to same courseSlug, fail-closed and require explicit override in course-config.ts; never auto-suffix.
```

The plain transcript and timestamped transcript are normalized independently using the rules below. The timestamped form keeps its timestamp prefixes; the plain form removes them only when deriving plain text from a timestamped-only source.

contentHash is lowercase hex SHA-256 (64 chars, ^[0-9a-f]{64}$) of one deterministic framed UTF-8 byte sequence containing both normalized forms. The exact sequence is:

    ASCII("transcript-hash-v1\0")
    + ASCII(decimal UTF-8 byte length of transcript) + ASCII(":") + transcript UTF-8 bytes
    + ASCII(decimal UTF-8 byte length of timestampedTranscript) + ASCII(":") + timestampedTranscript UTF-8 bytes

The version prefix, byte lengths, field order, separators, and empty timestamped form are normative. This prevents ambiguity and makes a meaningful timestamp change the hash. transcript_sha256 in Markdown frontmatter MUST equal contentHash. Same hash at the same path is unchanged; different content is permanent_conflict.

Normalization (must be identical in TypeScript and Go) for hash stability. Reference implementations MUST share test vectors in `protocol/normalization-vectors.json` (created in Stage 1; minimal seed vectors below must pass):

```text
seed vector 1: "Hello  world\r\n" -> "Hello world" (same hash)
seed vector 2: "a\n\n\n\nb" -> "a\n\nb"
seed vector 3: changing "fox" to "dog" MUST change hash
seed vector 4: changing one timestamp digit while plain text stays the same MUST change hash
seed vector 5: changing timestampedTranscript from empty to a supplied timestamped form MUST change hash
```

Ordered steps (order is normative):

```text
1. Unicode NFC normalize (TS: String.normalize('NFC'), Go: golang.org/x/text/unicode/norm.NFC)
2. CRLF/CR -> LF
3. For each line: detect timestamp prefix with NORMATIVE regex ^\s*[\[(]?\d{1,2}:\d{2}(?::\d{2})?[\]\)]?\s*(?:[-–—|]\s*)? — if matched, split into [prefix, rest]; strip trailing [ \t] from prefix detection area only, preserve prefix digits verbatim.
4. Then for rest (or whole line if no timestamp): strip trailing [ \t], collapse [ \t]{2,} to single space.
5. Rejoin with LF, collapse 3+ consecutive LF to exactly 2 LF, trim leading/trailing LF. So "a\n\n\n\nb" -> "a\n\nb".
6. Preserve meaningful words, speaker labels, and timestamp digits — they MUST change the hash. Parity is proven by shared vectors.
```

Normative timestamp strip regex (single source of truth for both normalization and derivation): `^\s*[\[(]?\d{1,2}:\d{2}(?::\d{2})?[\]\)]?\s*(?:[-–—|]\s*)?`. `timestampFormat` fixture field is a free-text example (e.g. `"[MM:SS] Speaker:"`), not a regex — parser uses the normative regex, fixture documents observed style. Stage 0 must prove that every timestamp prefix in the real fixture is fully consumed by this regex, including its delimiter. If the observed page uses fractional seconds, a colon delimiter, or any other style this regex does not consume, implementation is blocked until this plan and the shared vectors are deliberately updated; no parser may silently leave timestamp residue in plain text.

Serialization of the structured observed page (Stage 0, 2026-09-20): the observed Leccap player stores the timestamp and the spoken text in separate row elements (`.transcript-time`, `.transcript-text`) with no brackets or delimiters in the source. The parser therefore serializes the timestamped form as one line per row, `[<verbatim .transcript-time>] <text>`, and derives the plain form from the row text elements (derivedFrom=both). Normalization rejoins a detected timestamp prefix and its remainder with a single U+0020 when both are nonempty, and emits a nonempty prefix alone when the remainder is empty; the serialized form is idempotent under these rules. Stage 0 verified that every observed row prefix (`MM:SS` and `H:MM:SS`) is fully consumed by the normative regex in the serialized form, including the bracket and space delimiter.

`transcript` (plain) is required, min 50 non-whitespace chars after normalization. An exact whole-transcript match against case-insensitive `loading…|loading transcript|no transcript` is a defense-in-depth backstop and is not a general prose scan. The fixture's `loadingTextMarkers` are checked only inside the selected loading/error region, never against arbitrary transcript prose; when a shape records `loadingIndicatorSelector: null`, no region exists and the marker check is skipped entirely (stability snapshots carry completeness). `timestampedTranscript` derivation uses the same normative regex above:

```text
- If page provides both: store both as observed, derivedFrom=both.
- If page provides timestamped only: plain = strip normative prefix per line, then normalize, derivedFrom=timestamped-only.
- If page provides plain only: timestampedTranscript = "" (do not invent timestamps), derivedFrom=plain-only.
- Never silently duplicate one form into the other. Parser records `derivedFrom` in memory for tests (not part of TranscriptJob schema).
```

`sourceUrl` rules split into two layers (no contradiction). Both the extension and uploader use the same canonicalization vectors. The extension canonicalizes and pre-checks before submission; the uploader repeats the operation authoritatively and persists the canonical value, so the queue never contains two representations of the same URL:
- Parse an absolute URL; require `https`, exact host `leccap.engin.umich.edu` after lowercasing, no userinfo, and either no port or `:443`.
- Remove the query and fragment, preserve the encoded path and its trailing slash, normalize an empty path to `/`, and strip the default port. Do not decode arbitrary path bytes or copy query values into the job.
- Measure the resulting canonical URL in UTF-8 bytes. The TypeScript and Go implementations must pass the same source-URL vectors. The uploader rejects the job if canonicalization fails; it does not accept a raw unsafe URL merely because the extension pre-check was bypassed.
- Transport validation (reject): job is `rejected_unsafe_url` without persist if: scheme != `https`, host (lowercased, strip default :443, no port allowed otherwise) != `leccap.engin.umich.edu`, or canonical length >2048 UTF-8 bytes (measured post-canonicalization, chars==bytes for ASCII URLs, UTF-8 bytes for IDN). Path case preserved.
- Publish sanitization (omit): if transport-valid but lowercased path segments (split on `/._-`) contain whole segment `token|session|auth|sid`, omit `source_url` from frontmatter (still store canonical host+path in queue for dedup). Logs store host+path only, never query/fragment.

The end-to-end state model is:

```text
idle
  → activated
  → waiting_for_transcript
  → ready
  → handoff_pending
  → queued
  → uploading
  → uploaded | unchanged | retryable_error | permanent_conflict | rejected_*
```

The content script owns page states. The extension service worker owns capture and handoff status. The uploader owns queue and GitHub states. A retryable network or GitHub error persists as `retryable_error` with per-job backoff `5s,30s,2m,10m,1h` (±20% jitter, `uploader/internal/retry/backoff.go`, index=min(attempt_count,len-1) so it stays at 1h after the fifth attempt, reset to 0 on success, and attempt_count persists across restarts). When the delay expires, the uploader moves the row to `queued` and retries. Retryable jobs retry indefinitely while durable; the delay is capped, not the number of attempts.

Terminal job categories are `rejected_missing_identity|rejected_ambiguous_metadata|rejected_oversized|rejected_queue_full|rejected_invalid_hash|rejected_unsafe_url|rejected_unknown_field|rejected_invalid_schema|rejected_permission`. `rejected_handoff_full` is extension-local and is never emitted by the uploader. `rejected_duplicate_terminal` is a submit acknowledgement describing an existing terminal row; it is not a new queue row status. A submit acknowledgement is explicitly correlated to the submitted `requestId`, `lectureKey`, and `contentHash`; it is either `queued`, `already_queued`, or a documented rejection. The extension outbox keeps a bounded number of full jobs only until the uploader gives a definitive acknowledgement; only `queued` and `already_queued` mean the uploader durably owns the job. It never performs GitHub retries or owns remote status. Its capacity is three jobs, and a fourth capture fails visibly as `rejected_handoff_full` rather than overwriting an older pending job.

When the extension cannot retain a full job because the handoff outbox is full, or when the uploader returns `rejected_queue_full`, it stores a metadata-only overflow notice (lectureKey, lectureDate, capturedAt, reason; no transcript) in a bounded list of 20 notices and tells the user to reopen the transcript after capacity is available. The popup displays these notices and the page-facing status is visible immediately. If the notice list is also full, the current capture still gets an immediate visible failure; the system never pretends that the transcript was saved. Reopening the transcript is the documented recovery path.

`permanent_conflict` UX: the popup shows the target path, local contentHash, and either the remote transcript_sha256 or a malformed-file warning. The user can open the remote file, back it up, manually delete it in the GitHub UI if it is wrong, and press Retry. The uploader re-GETs the path: if 404, it moves permanent_conflict to queued; otherwise it stays in conflict. Conflict jobs are exempt from automatic pruning, but the user can explicitly discard a terminal local job; discard never deletes a remote file.

The GitHub Contents API is treated as preflight-plus-create, not as a conditional compare-and-swap. The uploader GETs the target first, sends a PUT without sha only when the target is absent, accepts only a documented create response, and never sends an update request. Any unexpected create response is followed by a fresh GET: an existing same-hash file becomes unchanged, an existing different or malformed file becomes permanent_conflict, and an absent file remains an error to classify and retry. No If-None-Match-style guarantee is assumed.

## Normative implementation contracts

The following contracts remove choices that would otherwise be reconstructed differently by separate agents. Stage 1 copies them into the named machine-readable files without changing field names, limits, statuses, or semantics. A change requires an edit to this plan and the decision log before implementation continues.

### Toolchain baseline

- The extension is Chrome Manifest V3, TypeScript, Node.js 22 LTS, npm, esbuild, Vitest, and `@types/chrome`. These are the only JavaScript dependencies; no frontend framework is needed or permitted for Phase 1.
- The uploader uses Go 1.24.x, the standard library, `modernc.org/sqlite`, and `golang.org/x/text/unicode/norm` for the required NFC normalization. These are the only Go dependencies. SQLite remains pure Go, but the Darwin Keychain adapter calls `Security.framework` through cgo; the supported Mac build therefore requires `CGO_ENABLED=1` and the Xcode Command Line Tools. A non-Darwin build is unsupported and must fail rather than silently producing a nonfunctional host. The first pass must not introduce a web server, ORM, or additional runtime service.
- The Native Messaging contract has `protocolVersion=1`. The extension build version comes from `package.json`; the uploader build version is injected at build time. Every status response exposes both versions so the popup can distinguish a protocol mismatch (fail closed) from a build-version mismatch (visible warning).
- `package-lock.json` and `go.sum` record exact resolved dependency versions. Patch-version resolution is allowed only within this baseline; it is not permission to change the runtime architecture or behavior.
- The browser and uploader test suites must run offline. Network tests use a fake GitHub server; no test requires the owner’s real token.

### Canonical Native Messaging contract

`protocol/native-messaging.schema.json` is the machine-readable rendering of this section. Every envelope is a JSON object with `additionalProperties=false`, `protocolVersion=1`, and a request `requestId` made of 1–64 printable ASCII characters. Native Messaging carries no transcript except inside `submit_job.job`; status and error messages never contain transcript text.

Extension-to-uploader requests are exactly:

```text
connect:
  { type, protocolVersion, requestId, extensionVersion }

submit_job:
  { type, protocolVersion, requestId, job }

status_request:
  { type, protocolVersion, requestId, beforeJobId?, limit? }

retry_job:
  { type, protocolVersion, requestId, jobId }

discard_job:
  { type, protocolVersion, requestId, jobId, confirmation: "discard" }

reset:
  { type, protocolVersion, requestId }
```

`extensionVersion` is required on `connect` and has maximum length 32. `beforeJobId`, when present, is a positive integer cursor. `limit` defaults to 50 and must be between 1 and 50; the uploader never returns more than 50 job summaries. `jobId` is a positive integer. The exact persisted queue-status enum is `queued|uploading|uploaded|unchanged|retryable_error|permanent_conflict|rejected_missing_identity|rejected_ambiguous_metadata|rejected_oversized|rejected_queue_full|rejected_invalid_hash|rejected_unsafe_url|rejected_unknown_field|rejected_invalid_schema|rejected_permission`; extension-local statuses and `rejected_duplicate_terminal` are not queue rows. In a normal response, `requestId` is echoed exactly; only unsolicited `status` messages use `requestId=null`. Unknown fields are invalid. There is no general RPC, arbitrary method name, or raw HTTP forwarding.

The uploader returns these exact response shapes:

The submit rejection statuses are exactly `rejected_invalid_schema`, `rejected_unknown_field`, `rejected_oversized`, `rejected_invalid_hash`, `rejected_unsafe_url`, `rejected_queue_full`, and `rejected_duplicate_terminal`. Extension-side `rejected_missing_identity`, `rejected_ambiguous_metadata`, and `rejected_handoff_full` are not uploader submit responses. `rejected_permission` occurs after a persisted job is processed and is delivered through `status`.

```text
ack (submit_job only):
  {
    type: "ack",
    protocolVersion: 1,
    requestId,
    operation: "submit",
    jobId: positive integer or null,
    lectureKey: string or null,
    contentHash: 64 lowercase hex characters or null,
    status: "queued" | "already_queued" | one of the submit rejection statuses,
    existingStatus: queue status or null,
    action: null | "retry_existing" | "discard_existing_then_recapture"
  }

command_result (retry_job, discard_job, reset):
  {
    type: "command_result",
    protocolVersion: 1,
    requestId,
    operation: "retry" | "discard" | "reset",
    jobId: positive integer or null,
    result: "accepted" | "rejected",
    status: queue status or "discarded" | "reset" | null,
    errorCategory: known error category or null
  }

status:
  {
    type: "status",
    protocolVersion: 1,
    requestId: string or null,
    extensionVersion: string,
    uploaderVersion: string,
    authState: "not_connected" | "authorizing" | "connected" |
               "reauthorization_required" | "target_repository_unavailable" |
               "protocol_mismatch",
    authorization: {
      userCode: string or null,
      verificationUri: string or null,
      verificationUriComplete: string or null,
      expiresAt: RFC3339 UTC or null
    },
    drainState: "idle" | "working" | "waiting_for_backoff" | "authorizing",
    counts: exact count for every queue status,
    jobs: at most 50 JobSummary objects,
    nextBeforeJobId: positive integer or null
  }

error:
  {
    type: "error",
    protocolVersion: 1,
    requestId: string or null,
    category: known error category,
    retryable: boolean
  }
```

`JobSummary` is exactly `{jobId, lectureKey, contentHash, status, attemptCount, nextAttemptAt, updatedAt, targetPath, lastErrorCategory, lastErrorHttpStatus, remoteContentHash, remoteFileKind}`. Its exact types and limits are: `jobId` positive integer; `lectureKey` string matching the lecture-key pattern and at most 128 characters; `contentHash` 64 lowercase hex characters; `status` one of the queue statuses below; `attemptCount` nonnegative integer; `nextAttemptAt` and `updatedAt` either a whole-second RFC3339 UTC string or null as appropriate; `targetPath` a nonempty string of at most 512 characters; `lastErrorCategory` a known category of at most 64 characters or null; `lastErrorHttpStatus` an integer 100–599 or null; `remoteContentHash` 64 lowercase hex characters or null; and `remoteFileKind` one of `file|directory|symlink|submodule|malformed|missing` or null. The remote fields are populated only when a remote inspection has occurred and never contain remote body text. It contains no transcript, course prose, full URL, or arbitrary error message. The response sorts summaries by `jobId DESC`; the next request sends `beforeJobId=nextBeforeJobId`. A null cursor means there are no more rows. The fixed page size and bounded fields keep status frames well below the one-megabyte frame cap even when the queue contains 500 jobs. The popup requests additional pages rather than asking for an unbounded list.

In `status`, `extensionVersion` and `uploaderVersion` are printable ASCII strings of at most 32 characters. `authorization.userCode` is printable ASCII of at most 64 characters; `verificationUri` and `verificationUriComplete` are HTTPS GitHub URLs of at most 2048 characters or null; and `authorization.expiresAt` is a whole-second RFC3339 UTC string or null. The popup treats these values as display data and never sends them to an arbitrary host.

For `command_result`, an accepted retry has `result=accepted,status=queued`; an ineligible retry has `result=rejected,status` equal to the current job status and `errorCategory=ineligible_command`; an accepted discard has `result=accepted,status=discarded`; and an accepted reset has `result=accepted,status=reset`. The `error.category` vocabulary is exactly `protocol_mismatch|invalid_message|host_unavailable|invalid_state|not_connected|reauthorization_required|target_repository_unavailable|internal|ineligible_command|rejected_missing_identity|rejected_ambiguous_metadata|rejected_oversized|rejected_queue_full|rejected_handoff_full|rejected_invalid_hash|rejected_unsafe_url|rejected_unknown_field|rejected_invalid_schema|rejected_permission`. `rejected_duplicate_terminal` is ack-only and never appears in an `error` message. Error payloads never carry raw exception text.

The `counts` object has exactly these keys, each a nonnegative integer: `queued`, `uploading`, `uploaded`, `unchanged`, `retryable_error`, `permanent_conflict`, `rejected_missing_identity`, `rejected_ambiguous_metadata`, `rejected_oversized`, `rejected_queue_full`, `rejected_invalid_hash`, `rejected_unsafe_url`, `rejected_unknown_field`, `rejected_invalid_schema`, and `rejected_permission`. It does not include `rejected_handoff_full`, `rejected_duplicate_terminal`, or auth states because those are not uploader queue rows.

For a new submission, `queued` means a new row was committed before the ack, and `already_queued` means the matching `(lectureKey, contentHash)` row already exists. If the matching row has a terminal `rejected_*` status, the ack is `rejected_duplicate_terminal`, includes its `jobId` and `existingStatus`, and sets `action` to `retry_existing` only for `rejected_permission`; every other terminal rejection uses `discard_existing_then_recapture`. `rejected_handoff_full` is never a Native Messaging response. `rejected_permission` is an asynchronous queue status, not a reason to refuse a valid job before persistence.

The `status` message is sent in response to `status_request` and may also be sent unsolicited after a job transition or authorization change. The extension correlates responses by `requestId`; a response to a valid request echoes that request ID, while unsolicited status has `requestId=null`. `connect` causes the host to open or resume its processor and emit a status snapshot. `drainState=working` means a queue job is actively being processed; `drainState=authorizing` means device-code polling is active; `drainState=idle` means no active job exists and no future retry is waiting; and `drainState=waiting_for_backoff` means at least one retryable job has a future `next_attempt_at` and no job is currently due. Terminal rows alone do not prevent `idle`. An alarm-owned connection may close only after observing `idle` or `waiting_for_backoff`, never merely after the first snapshot. The next alarm reconnects for work that becomes due. A popup/page-owned connection may remain open. This is the single drain protocol; the alarm does not invent a second queue owner.

When `authState` is `not_connected`, `reauthorization_required`, `target_repository_unavailable`, or `protocol_mismatch`, the processor does not claim queued jobs and does not convert them to `rejected_permission`; the jobs remain durable until authorization/protocol state is repaired. `rejected_permission` is reserved for a persisted job whose authenticated GitHub request definitively proves that the App/user lacks permission. Once `authState=connected`, the processor resumes due jobs.

### Canonical transcript-job schema

`protocol/transcript-job.schema.json` is copied from this exact schema. The byte limits are enforced in code after UTF-8 encoding because JSON Schema `maxLength` counts characters rather than bytes.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://lecture-transcripts.local/protocol/transcript-job.schema.json",
  "title": "TranscriptJob",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "schemaVersion": { "const": 1 },
    "lectureKey": { "type": "string", "pattern": "^[a-z0-9]+/[0-9]{4}-(winter|spring|summer|fall)/[0-9]{3}$", "maxLength": 128 },
    "courseSlug": { "type": "string", "pattern": "^[a-z0-9]+$", "maxLength": 64 },
    "courseName": { "type": "string", "minLength": 1, "maxLength": 256 },
    "term": { "type": "string", "pattern": "^[0-9]{4}-(winter|spring|summer|fall)$", "maxLength": 32 },
    "lectureNumber": { "type": "integer", "minimum": 1, "maximum": 999 },
    "lectureDate": { "type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", "maxLength": 10 },
    "sourceUrl": { "type": "string", "pattern": "^https://", "minLength": 1, "maxLength": 2048 },
    "capturedAt": { "type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$", "maxLength": 20 },
    "transcript": { "type": "string", "minLength": 1, "maxLength": 460800 },
    "timestampedTranscript": { "type": "string", "maxLength": 460800 },
    "contentHash": { "type": "string", "pattern": "^[0-9a-f]{64}$", "maxLength": 64 }
  },
  "required": [
    "schemaVersion",
    "lectureKey",
    "courseSlug",
    "courseName",
    "term",
    "lectureNumber",
    "lectureDate",
    "sourceUrl",
    "capturedAt",
    "transcript",
    "timestampedTranscript",
    "contentHash"
  ]
}
```

Runtime validation additionally requires a real calendar date, `lectureKey` consistency with the other identity fields, at least 50 non-whitespace characters in normalized `transcript`, UTF-8 byte limits of 460800 per transcript field and 972800 for the serialized job, and the source URL host/path rules above. JSON Schema validation alone is not sufficient.

### Canonical source-URL vectors

`protocol/source-url-vectors.json` is shared by TypeScript and Go. It must contain at least these exact cases:

| Input | Canonical URL | Publish `source_url` |
| --- | --- | --- |
| `https://LECCAP.ENGIN.UMICH.EDU:443/lecture/123?session=secret#transcript` | `https://leccap.engin.umich.edu/lecture/123` | include |
| `https://leccap.engin.umich.edu/lecture/token/123` | `https://leccap.engin.umich.edu/lecture/token/123` | omit |
| `http://leccap.engin.umich.edu/lecture/123` | reject | reject |

The vector file may add observed-path cases after Stage 0, but it may not change the canonicalization rules or make an unsafe input valid.

For byte measurements and Native Messaging, canonical JSON uses the property order shown in the schema (`schemaVersion` through `contentHash`), UTF-8 encoding, standard JSON escaping, no insignificant whitespace, and no trailing newline. The same canonical serializer is used for the size report and test vectors; semantic JSON key order is otherwise not part of the job's identity.

### Canonical SQLite queue schema

`uploader/internal/queue/schema.sql` is copied from this DDL and applied as migration version 1. SQLite stores all timestamps as UTC RFC3339 text. The queue never stores an access token or refresh token.

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY
);

CREATE TABLE jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lecture_key TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  job_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  lease_started_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_error_category TEXT,
  last_error_http_status INTEGER,
  remote_content_hash TEXT,
  remote_file_kind TEXT,
  UNIQUE (lecture_key, content_hash),
  CHECK (attempt_count >= 0),
  CHECK (length(content_hash) = 64)
);

CREATE INDEX jobs_ready_idx
  ON jobs (status, next_attempt_at, created_at);

CREATE INDEX jobs_lecture_idx
  ON jobs (lecture_key, created_at);

INSERT INTO schema_migrations(version) VALUES (1);
```

The store validates the status vocabulary in Go, uses a transaction for every state transition, and treats `(lecture_key, content_hash)` as the deduplication key. A later migration must be additive and must never rewrite or delete a durable job without an explicit user-requested reset.

### Canonical status vocabulary

`extension/src/status.ts` and the uploader's status messages use these exact machine values and user-facing labels. Diagnostic detail is limited to the fixed fields in `JobSummary`, the fixed path/hash fields shown for conflicts, and sanitized local logs; it is not a new protocol field, arbitrary error text, or a renamed status.

| Machine status | Owner | User-facing label | Automatic retry | Terminal |
| --- | --- | --- | --- | --- |
| `not_ready` | extension | Transcript was not ready; nothing saved | no | yes, no job created |
| `waiting_for_transcript` | extension | Waiting for transcript to finish | no | no |
| `ready` | extension | Ready to save | no | no |
| `pending_handoff` | extension | Waiting for uploader | on reconnect | no |
| `queued` | uploader | Queued for GitHub | yes | no |
| `uploading` | uploader | Uploading | lease recovery | no |
| `uploaded` | uploader | Saved | no | yes |
| `unchanged` | uploader | Already saved | no | yes |
| `retryable_error` | uploader | Temporary error; will retry | yes | no |
| `permanent_conflict` | uploader | Conflict: existing file differs | no | yes, manual retry only |
| `rejected_missing_identity` | boundary | Rejected: missing lecture identity | no | yes |
| `rejected_ambiguous_metadata` | boundary | Rejected: ambiguous lecture metadata | no | yes |
| `rejected_oversized` | boundary | Rejected: transcript is too large | no | yes |
| `rejected_queue_full` | uploader | Rejected: local queue is full | no | yes |
| `rejected_handoff_full` | extension | Rejected: pending handoff is full | no | yes |
| `rejected_invalid_hash` | boundary | Rejected: invalid content hash | no | yes |
| `rejected_unsafe_url` | boundary | Rejected: unsafe source URL | no | yes |
| `rejected_unknown_field` | boundary | Rejected: unknown job field | no | yes |
| `rejected_invalid_schema` | boundary | Rejected: invalid job schema | no | yes |
| `rejected_permission` | uploader | Rejected: GitHub permission denied | no | yes |
| `not_connected` | uploader auth | GitHub is not connected | no | no |
| `authorizing` | uploader auth | Finish GitHub authorization | no | no |
| `connected` | uploader auth | GitHub connected | no | no |
| `reauthorization_required` | uploader | Reconnect GitHub | no | no |
| `target_repository_unavailable` | uploader auth | Target repository is unavailable | no | no |
| `protocol_mismatch` | boundary | Extension and uploader versions are incompatible | no | no |

The table includes extension-local outcomes and authorization states so the popup has one vocabulary, but they do not all represent SQLite rows. Persisted queue statuses are `queued`, `uploading`, `uploaded`, `unchanged`, `retryable_error`, `permanent_conflict`, and any `rejected_*` row the uploader explicitly records. `rejected_handoff_full` is local to the extension and never appears in the uploader status response; `reauthorization_required`, `target_repository_unavailable`, and `protocol_mismatch` are connection/auth states, not job rows. `rejected_duplicate_terminal` is only a correlated submit acknowledgement. The status response count keys are exactly the persisted queue statuses plus zero-valued counts for the defined rejection categories; no agent may invent a second status vocabulary.

`uploaded`, `unchanged`, and every `rejected_*` status are terminal outcomes for durable uploader rows; they are not a promise that the extension still retains a full unacknowledged copy. On a retryable failure, the uploader persists `retryable_error` with `next_attempt_at`, `last_error_category`, and `last_error_http_status`; it stores no free-form remote error text. When that time arrives it transactionally changes the row to `queued` and claims it normally. `retryable_error` remains durable in the uploader, is never auto-pruned, and is never returned to the extension as an unacknowledged full job.

The outbox is cleared on the submit acknowledgement, normally when the job becomes `queued` or `already_queued`; later `uploaded`, `unchanged`, `retryable_error`, `permanent_conflict`, and `rejected_permission` updates describe the durable uploader row and do not require the full job to return to extension storage. A non-persisting rejection clears the full copy only after the extension records its metadata-only recovery notice.

The uploader does not persist a job for extension-local `not_ready`, `rejected_missing_identity`, or `rejected_ambiguous_metadata` when the extension can reject before submission. If an invalid message reaches the uploader, it returns the corresponding validation rejection without a row. A queue-full rejection likewise does not drop an existing row; the extension creates the metadata-only overflow notice described above.

### Canonical normalization-vector contract

`protocol/normalization-vectors.json` must contain the following cases, with the exact inputs, normalized outputs, and hash relationship. It may include mechanically computed `expectedHash` fields, but it may not change these cases or replace the relationship assertions with snapshots from a different algorithm.

| ID | Plain input | Timestamped input | Expected plain | Expected timestamped | Hash assertion |
| --- | --- | --- | --- | --- | --- |
| `line-endings-and-spaces` | `Hello  world\\r\\n` | empty | `Hello world` | empty | normalized self is stable |
| `blank-lines` | `a\\n\\n\\n\\nb` | empty | `a\\n\\nb` | empty | normalized self is stable |
| `word-change-fox` | `The fox` | empty | `The fox` | empty | differs from `word-change-dog` |
| `word-change-dog` | `The dog` | empty | `The dog` | empty | differs from `word-change-fox` |
| `timestamp-one` | `The fox` | `[00:01] The fox` | `The fox` | `[00:01] The fox` | differs from `timestamp-two` |
| `timestamp-two` | `The fox` | `[00:02] The fox` | `The fox` | `[00:02] The fox` | differs from `timestamp-one` |
| `timestamped-empty` | `The fox` | empty | `The fox` | empty | differs from `timestamped-present` |
| `timestamped-present` | `The fox` | `[00:01] The fox` | `The fox` | `[00:01] The fox` | differs from `timestamped-empty` |
| `unicode-nfc` | `e\\u0301lan` | empty | `élan` | empty | equals its NFC-normalized equivalent |

The vector file's JSON field names are `id`, `plainInput`, `timestampedInput`, `expectedPlain`, `expectedTimestamped`, and `hashAssertion`; its required content is:

```json
[
  {
    "id": "line-endings-and-spaces",
    "plainInput": "Hello  world\r\n",
    "timestampedInput": "",
    "expectedPlain": "Hello world",
    "expectedTimestamped": "",
    "hashAssertion": "normalized-self"
  },
  {
    "id": "blank-lines",
    "plainInput": "a\n\n\n\nb",
    "timestampedInput": "",
    "expectedPlain": "a\n\nb",
    "expectedTimestamped": "",
    "hashAssertion": "normalized-self"
  },
  {
    "id": "word-change-fox",
    "plainInput": "The fox",
    "timestampedInput": "",
    "expectedPlain": "The fox",
    "expectedTimestamped": "",
    "hashAssertion": "different-from:word-change-dog"
  },
  {
    "id": "word-change-dog",
    "plainInput": "The dog",
    "timestampedInput": "",
    "expectedPlain": "The dog",
    "expectedTimestamped": "",
    "hashAssertion": "different-from:word-change-fox"
  },
  {
    "id": "timestamp-one",
    "plainInput": "The fox",
    "timestampedInput": "[00:01] The fox",
    "expectedPlain": "The fox",
    "expectedTimestamped": "[00:01] The fox",
    "hashAssertion": "different-from:timestamp-two"
  },
  {
    "id": "timestamp-two",
    "plainInput": "The fox",
    "timestampedInput": "[00:02] The fox",
    "expectedPlain": "The fox",
    "expectedTimestamped": "[00:02] The fox",
    "hashAssertion": "different-from:timestamp-one"
  },
  {
    "id": "timestamped-empty",
    "plainInput": "The fox",
    "timestampedInput": "",
    "expectedPlain": "The fox",
    "expectedTimestamped": "",
    "hashAssertion": "different-from:timestamped-present"
  },
  {
    "id": "timestamped-present",
    "plainInput": "The fox",
    "timestampedInput": "[00:01] The fox",
    "expectedPlain": "The fox",
    "expectedTimestamped": "[00:01] The fox",
    "hashAssertion": "different-from:timestamped-empty"
  },
  {
    "id": "unicode-nfc",
    "plainInput": "e\u0301lan",
    "timestampedInput": "",
    "expectedPlain": "élan",
    "expectedTimestamped": "",
    "hashAssertion": "same-as:unicode-nfc-equivalent"
  },
  {
    "id": "unicode-nfc-equivalent",
    "plainInput": "élan",
    "timestampedInput": "",
    "expectedPlain": "élan",
    "expectedTimestamped": "",
    "hashAssertion": "same-as:unicode-nfc"
  }
]
```

Hashes are computed only with the framed `transcript-hash-v1` algorithm; no implementation may use a plain-text-only hash.

### Canonical Native Messaging host template

`native-host/com.neelbangera.lecturetranscripts.json.in` has exactly this shape. `{{BINARY_PATH}}` and `{{EXTENSION_ID}}` are installer substitutions; neither placeholder may remain in the rendered file.

```json
{
  "name": "com.neelbangera.lecturetranscripts",
  "description": "Lecture Transcripts uploader",
  "path": "{{BINARY_PATH}}",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://{{EXTENSION_ID}}/"
  ]
}
```

The installer must reject an empty, malformed, or wildcard extension ID and must validate that the rendered origin is exactly the loaded extension's origin before installing it.

## Detailed Implementation

This is the implementation sequence. Each stage has a concrete output and a stop condition. Do not skip ahead to GitHub writes before the parser, job contract, and local queue have been tested.

### Stage 0 — Freeze discovery facts before writing implementation code

1. Read this plan and `PHASE_1_DECISIONS.md`. Treat this plan as authoritative if the older `TECHNICAL_IMPLEMENTATION_PROPOSAL.md` differs.
2. Open at least two representative authenticated Leccap lecture pages manually, including the page expected to produce the largest transcript/job if that can be identified. Include one page whose transcript is already expanded before the content script would observe a click, if the site permits that state. If the supported set contains only one permitted page, record that exception explicitly in `docs/STAGE_0_REPORT.md` rather than silently weakening the gate.
3. Record the exact transcript control, transcript container, course label, term label, lecture number, lecture date, timestamp format, and any visible loading or completion indicator. Record the DOM source for each metadata field separately. If the lecture date comes from a linked authenticated overview page, record the overview-link selector, recording-card selector, player-link correlation rule, date selector, and the runtime lookup policy; a downloaded overview capture is evidence only and is not runtime input. Verify that the completion indicator and, when the shape has one, the loading/error region are within the transcript container subtree; a shape with no in-container loading/error element records `loadingIndicatorSelector: null` in its fixture and relies on the compound completion rule. Metadata may come from outside that subtree, but it must have an explicit runtime source and correlation rule.
4. Determine whether the page is a normal document or a single-page interface that changes the URL or DOM without a full navigation. Record the URL and DOM behavior after navigation; the implementation uses `popstate` plus a 1000ms URL poll and never monkeypatches `history.pushState` or `history.replaceState` from the isolated content-script world.
5. Confirm that clicking or expanding the transcript is a reliable, user-visible activation point. Also record whether a visible, populated, already-expanded transcript at script start is distinguishable from a collapsed or merely preloaded transcript; the former counts as activation, while the latter does not.
6. Capture redacted or synthetic fixtures that preserve the lecture and, when needed, overview DOM shapes without retaining cookies, private identifiers, or unnecessary transcript content. Never commit an authenticated page dump.
7. Define the complete supported course mapping in `extension-tests/fixtures/course-mapping.json` before creating parser configuration. It must contain one object per distinct observed page course label; that object's `supportedTerms` list must enumerate every supported course/term combination for that label and must not be empty. Each accepted pair produces one stable `courseSlug` and one stable `lectureKey` for every supported page. Use the Identity rules above: `courseSlug ^[a-z0-9]+$`, `term YYYY-{winter|spring|summer|fall}`, `lectureKey <slug>/<term>/<NNN>`. The EECS 491 Winter 2026 entry is only an example; it cannot stand in for the full personal course set. Stage 2 copies this exact mapping into `extension/src/course-config.ts`. Missing or ambiguous course/term/number fails closed.
8. Measure at least two representative permitted lecture pages, including the largest observed transcript/job among the supported pages, and write the actual UTF-8 byte counts to `extension-tests/fixtures/transcript-size-report.json`. The current caps are approved safety caps only after the report proves that every supported sample fits: TranscriptJob serialized JSON max 972800 bytes (950*1024, leaving headroom below the 1,048,576-byte application frame cap), each transcript field max 460800 bytes (450*1024), sourceUrl max 2048 bytes after canonicalization, and all other string fields max 256 characters. For each sample, `nativeMessageBytes` is the largest compact UTF-8 `submit_job` request containing that sample, including the envelope but excluding the four-byte frame prefix; the report must also record the largest status page payload observed in the same fixture suite. If a permitted supported sample reaches or exceeds a cap, Stage 0 fails and this plan must be revised before implementation; do not silently raise the cap or reject the valid sample. The extension performs a fast pre-check and the uploader performs the authoritative check, both reporting `rejected_oversized`. The application rejects a Native Messaging frame over 1,048,576 bytes in either direction; this is a conservative product cap, not a claim that Chrome uses the same limit in both directions. SQLite queue max 100MB / 500 jobs: after each uploaded or unchanged job, delete the oldest uploaded or unchanged jobs older than 7 days until under limits. `queued`, `uploading`, `retryable_error`, `permanent_conflict`, and `rejected` jobs are never auto-deleted. If a new queued job would exceed either limit, reject it as `rejected_queue_full` without dropping an existing job. Create the database directory as 0700 and the database, WAL, and SHM files as 0600. For single-instance ownership, open `queue.lock` with `O_RDWR|O_CREAT` and acquire an advisory `flock LOCK_EX|LOCK_NB`; keep the file descriptor open for the process lifetime. Do not use `O_EXCL`, and treat a leftover lock file as harmless after a crash. A second process exits `already_running`. Multi-profile concurrent writers remain out of scope. No truncate/chunk in Phase 1. Also measure `renderTimeMs` from activation until the second matching stable snapshot and record it per sample, along with `observedMaxRenderTimeMs` and `approvedLimits.maxRenderTimeMs=30000`; if any supported sample reaches 30000ms or times out, Stage 0 fails and this plan must be revised before implementation.
9. Select the exact GitHub web authorization mechanism. Selected: GitHub App Device Authorization Flow (RFC 8628) owned by the local uploader. Provisioning: the owner creates one GitHub App, enables device flow, grants only repository Contents read/write permission, and installs it only on `neelbangera/lecture-transcripts`. The App client ID and numeric RepositoryID are runtime inputs in the machine-local config file described in the readiness gate; they are not copied into `config.go`, committed to source, or replaced with placeholders in `SETUP.md`. There is no client secret or App private key in the extension or uploader. The uploader requests a device code with the App client ID, shows `user_code` and `verification_uri` (and uses `verification_uri_complete` when GitHub supplies it), and polls the token endpoint no faster than the interval returned by GitHub; after `slow_down` it uses the newly returned interval. The device-flow expiration comes from the server-provided `expires_in`, not a hardcoded duration. The token request includes the configured numeric `repository_id` to further restrict the user access token; no repository ID is returned by the device-code response. The in-flight device transaction is stored in Keychain as a separate short-lived record, so a host restart resumes the same unexpired device code; an expired or terminal transaction is deleted and a new flow starts. The uploader stores the access token, refresh token when provided, and expiry metadata in macOS Keychain; refreshing replaces the token pair atomically. Revoked or expired credentials surface `reauthorization_required`. The extension receives only the exact auth states in the Native Messaging contract. Do not silently fall back to an OAuth App or a broad repo scope.
10. Verify the destination repository and App installation during Stage 0. The intended target is `owner=neelbangera`, `repo=lecture-transcripts`, `branch=main`, GitHub App permission `Contents: read and write`, and the numeric RepositoryID for that exact repository. The repository must already have at least one commit on `main` (for example, create a README through the GitHub UI); the uploader never initializes an empty repository. No other repository or path may be written. The uploader omits custom committer and author objects so GitHub uses the authenticated user identity. `courses/<slug>/README.md` is never auto-created or updated in Phase 1; only `courses/<slug>/<term>/lectures/<NNN>.md` may be created. During connect, the uploader performs `GET /repos/{owner}/{repo}` with the newly authorized credential and verifies the numeric ID and full name, then performs `GET /repos/{owner}/{repo}/contents?ref=main` and requires a successful response to prove the selected branch is initialized and Contents permission is usable. A 403 or 404 on either sanity request fails connect as `target_repository_unavailable`, clears the just-authorized credential, and tells the user to authorize the account that can access the configured repository; it is not allowed to degrade into a later upload failure.

Stage 0 is complete only when the required Stage 0 packet exists, a human can point to the exact DOM evidence for activation, extraction, metadata, and completion, the representative size measurements are recorded, and the provisioning report verifies the real App installation and repository ID. Naming the auth flow alone is insufficient. DOM selectors and observed page facts must be recorded in `extension-tests/fixtures/lecture-page.selectors.json` with the schema below. Keep parser output expectations separate in `lecture-page.expected.json` so test configuration cannot be mistaken for production data. The JSON shown next is a shape/template, not a valid completed fixture; every example value must be replaced by observed evidence before the gate can pass:

```json
{
  "transcriptButtonSelector": "CSS selector string, e.g. \"button[data-test='transcript-toggle']\"",
  "transcriptContainerSelector": "CSS selector string",
  "courseSelector": {
    "selector": "observed CSS selector",
    "regex": "observed regex with one course capture group",
    "captureGroup": 1
  },
  "termSelector": {
    "selector": "observed CSS selector",
    "regex": "observed regex with season and year capture groups",
    "seasonCaptureGroup": 1,
    "yearCaptureGroup": 2
  },
  "lectureNumberSelector": {
    "selector": "observed CSS selector",
    "regex": "observed regex with one lecture-number capture group",
    "captureGroup": 1
  },
 "lectureDateSource": {
   "page": "lecture_page|linked_overview_page",
   "runtimeLookup": "on_demand_fetch|landing_cache|unsupported_without_date",
   "lecturePageOverviewLinkSelector": "observed CSS selector or null",
   "recordingCardSelector": "observed CSS selector or null",
   "recordingLinkSelector": "observed CSS selector or null",
   "dateSelector": "observed CSS selector",
   "dateRegex": "observed regex with one date-text capture group",
   "dateCaptureGroup": 1,
   "correlation": "absolute_player_href|normalized_player_path|not_applicable"
 },
  "dateYearPolicy": "term_year_if_missing",
 "completionIndicator": {
    "selector": "CSS selector string for the page's explicit transcript-complete signal",
    "mode": "present|attribute_equals|text_matches",
    "attribute": "attribute name or null",
    "valueRegex": "regex string or null",
    "populationSelector": "CSS selector whose matched elements must have nonempty text for completion, or null"
  },
  "loadingIndicatorSelector": "CSS selector for a loading/error status region, or null for a page shape whose transcript container contains no loading/error element (the fixture must record the null explicitly and the completion rule then relies on the compound open-state + population + stability check)",
  "timestampFormat": "free-text example observed, e.g. \"[00:12]\" — informational only, parsing uses normative regex",
  "isSPA": false,
  "stabilityDebounceMs": 1500,
  "sanityMinChars": 50,
  "loadingTextMarkers": []
}
```

Do not write parser code until that JSON exists from a real permitted page.
SPA handling: after injection and after every URL change, the content script listens for `popstate` and polls `location.href` every 1000ms. It does not wrap `history.pushState` or `history.replaceState`; an isolated-world wrapper would not intercept page-script calls. A URL change resets the page capture state, and a new visible/populated transcript may activate capture for the new lecture.
Activation policy: a click or expansion of the observed transcript control activates capture. If the content script starts, or a URL change occurs, with the transcript container visible, expanded, and populated, that state counts as an already-completed activation so a missed click cannot silently lose a capture. A hidden, collapsed, or merely preloaded transcript does not activate capture.
Observer lifecycle: attach `MutationObserver` only to `transcriptContainerSelector` with `subtree=true`; never observe `document` or the video-player subtree. Read the fixture-defined completion indicator and loading/error region from that container. Observed-page completion contract (Stage 0, 2026-09-20): the Leccap player shape has no explicit in-container complete marker, so its completion rule is compound — the transcript control's `title` attribute must read `Hide Transcript` (open state), the container must contain at least one row with nonempty text (`populationSelector`), and then two normalized transcript snapshots separated by the 1500ms no-mutation debounce must have the same framed content hash. The page-level `layout-loading` class persists after completion (observed on fully rendered pages) and the initial `loading` label lives outside the transcript container; neither is a completion or loading signal, and a shape with no in-container loading region records `loadingIndicatorSelector: null`, making the region-clear condition vacuous while the stability snapshots carry completeness. After activation, wait for the compound indicator to match, then take the two snapshots. If the indicator is absent, never becomes true, or matching snapshots cannot be obtained before the 30s overall timeout, return `not_ready` and do not submit.
A `not_ready` result disconnects the observer and is visible to the user. A later click/expand, or a later already-expanded-and-populated state after navigation, starts a fresh observation window; the extension does not spin forever on an unchanged failed page. A hash change during ordinary transcript rendering is not itself a terminal error: discard the first snapshot, restart the 1500ms quiet window, and try again until the 30-second deadline. Return `not_ready` only when the completion indicator is unavailable/false at the deadline, the loading/error region remains active (when the shape defines a region), or two matching snapshots cannot be obtained before the deadline.


For the completed fixture, each selector object is required and its regex must be tested against the fixture. The course capture group identifies the canonical course label; the term season/year capture groups identify the two term components; the number object identifies its single capture group; and `lectureDateSource` identifies the actual page, runtime lookup policy, date selector, and correlation rule. If the source is `linked_overview_page`, the overview selectors and correlation must be tested against a redacted overview fixture or equivalent recorded evidence; `dateSelector` must not be falsely placed on the lecture-page fixture. The completion mode must be one actual value (present, attribute_equals, or text_matches), with attribute and valueRegex populated consistently with that mode, and `populationSelector` populated when completion requires populated row text. loadingIndicatorSelector must identify the actual loading/error region, even if its marker list is empty, or record an explicit null for a shape that has no loading/error element inside its transcript container (observed for Leccap, 2026-09-20); a null must never stand in for an unexamined region. The lecture number comes only from the recording-title numeric prefix; the overview category badge (observed `Lecture - 001` on every lecture card) must never be parsed as a lecture number, and a recording title without a numeric prefix fails closed as `rejected_ambiguous_metadata`. isSPA must be the observed boolean, not an assumed default. The fixed numeric fields must remain 1500 and 50.

`dateYearPolicy` is fixed to `term_year_if_missing`: an unambiguous page date without a year uses the validated term year; an ambiguous numeric date is rejected. Every timestamp prefix in the fixture must match the normative timestamp regex through the complete delimiter, and the expected fixture must show that the resulting plain transcript contains no leftover timestamp punctuation.

Completion modes are exact: `present` means the selector's existence is the signal and `attribute`/`valueRegex` are null; `attribute_equals` requires a nonempty attribute and a regex matched against that attribute; `text_matches` requires `valueRegex` matched against the selected element's normalized text and uses a null attribute. The completed fixture must record the mode actually observed on the page.

`lecture-page.expected.json` has this exact field shape for the valid fixture; the strings shown as placeholders must be replaced by values extracted from that fixture:

```json
{
  "fixtureId": "lecture-page",
  "supported": true,
  "completion": "complete",
  "courseName": "canonical course name",
  "courseSlug": "lowercasealphanumeric",
  "term": "YYYY-winter",
  "lectureNumber": 1,
  "lectureDate": "YYYY-MM-DD",
  "sourceUrl": "https://leccap.engin.umich.edu/observed/path",
  "transcript": "normalized redacted transcript text",
  "timestampedTranscript": "normalized redacted timestamped text or empty string",
  "derivedFrom": "both|timestamped-only|plain-only",
  "lectureKey": "course/term/001",
  "contentHash": "64 lowercase hex characters",
  "stableSnapshotCount": 2
}
```

The expected fixture must prove the completion indicator, metadata mapping, derivation mode, lecture key, and framed hash. It must not contain an unredacted authenticated URL, cookie, token, or transcript copied beyond what is necessary for the test.

The other Stage 0 data files have these minimum shapes and validation rules:

```json
{
  "courseMappings": [
    {
      "pageCourseText": "observed course label",
      "courseName": "canonical course name",
      "courseSlug": "lowercasealphanumeric",
      "supportedTerms": ["YYYY-winter"]
    }
  ]
}
```

`courseMappings` must contain one entry for every distinct supported page course label, and each `supportedTerms` list must contain every supported normalized term for that label; its example strings are placeholders and are invalid in the committed artifact. `supportedTerms` must not be empty, must contain no duplicates, and must contain only exact normalized terms. No two entries may normalize to the same page course label, `courseSlug`, or `(courseSlug, term)` pair. `courseName` must be nonempty, at most 256 characters, and contain no newline.

```json
{
  "capturedAt": "RFC3339 UTC",
  "samples": [
    {
      "sampleId": "stable-redacted-id",
      "courseSlug": "observed-course-slug",
      "term": "YYYY-winter",
      "lectureNumber": 1,
      "transcriptBytes": 0,
      "timestampedTranscriptBytes": 0,
      "serializedJobBytes": 0,
      "nativeMessageBytes": 0,
      "statusPageBytes": 0,
      "renderTimeMs": 0
    }
  ],
  "observedMaxima": {
    "transcriptBytes": 0,
    "timestampedTranscriptBytes": 0,
    "serializedJobBytes": 0,
    "nativeMessageBytes": 0,
    "statusPageBytes": 0,
    "renderTimeMs": 0
  },
  "approvedLimits": {
    "transcriptBytes": 460800,
    "timestampedTranscriptBytes": 460800,
    "serializedJobBytes": 972800,
    "nativeMessageBytes": 1048576,
    "statusPageBytes": 1048576,
    "renderTimeMs": 30000
  }
}
```

In the committed `transcript-size-report.json`, `capturedAt`, identifiers, and every measured byte count must be real values; zeroes and prose such as `RFC3339 UTC` or `observed-course-slug` are template markers only. `samples` must contain at least two permitted captures, `observedMaxima` must equal the maxima computed from `samples`, and every observed maximum must be strictly below its approved limit. `renderTimeMs` measures activation through the second stable snapshot, not page-load time. The report must also record the SQLite queue directory/file mode checks in `docs/STAGE_0_REPORT.md`.

Size definitions are exact: `transcriptBytes` and `timestampedTranscriptBytes` are UTF-8 bytes after normalization; `serializedJobBytes` is the UTF-8 length of compact canonical TranscriptJob JSON; `nativeMessageBytes` is the UTF-8 length of the complete compact `submit_job` JSON payload for that sample, including its envelope and excluding the four-byte framing prefix; and `statusPageBytes` is the UTF-8 length of a compact status response containing 50 worst-case bounded JobSummary objects and the exact counts/auth fields. The frame cap applies to the payload length, and the implementation must measure the same representation in both languages.

For each sample, `statusPageBytes` is measured with the same worst-case 50-row status construction; it is repeated in the sample record so `observedMaxima` remains mechanically computable from `samples`.

### Stage 1 — Create the project skeleton and shared contract

1. Create the root TypeScript package metadata and test configuration.
2. Create the Go module for the native uploader and lock its dependencies.
3. Create the canonical `TranscriptJob` JSON schema in `protocol/transcript-job.schema.json`. Required: `schemaVersion==1`, `lectureKey`, `courseSlug`, `courseName`, `term`, `lectureNumber int`, `lectureDate YYYY-MM-DD`, `sourceUrl https`, `capturedAt RFC3339`, `transcript >=50 chars`, `timestampedTranscript` (a string that may be empty), and `contentHash ^[0-9a-f]{64}$`. The wire field is always serialized; a page that has no timestamped source sends the empty string and never invents timestamps. Forbidden: additionalProperties false. The schema documentation must point to the framed hash input defined above. The TypeScript and Go implementations must be tested against the same field names, required fields, maximum sizes (JSON 972800 bytes, transcripts 460800 bytes each), and rejection rules.
4. Create `protocol/native-messaging.schema.json` from the exact Native Messaging contract above and implement the same strict shapes in TypeScript and Go. Every request carries `protocolVersion` and `requestId`; submit acks echo the correlation fields and distinguish `rejected_duplicate_terminal`; status uses the fixed 50-row cursor page and the explicit `drainState`; retry/discard/reset use `command_result`; no message may carry a transcript except `submit_job.job`. Retry is permitted only for `retryable_error`, `permanent_conflict` after the user confirms the remote file was removed, or `rejected_permission` after authorization is restored. Discard is permitted only for terminal local rejected or permanent-conflict jobs and never calls a GitHub delete operation. Do not expose a general RPC mechanism.
5. Define the queue, extension-local, and auth-state values from the canonical vocabulary before writing UI code. A `queued` or `already_queued` ack means durable uploader ownership; a validation or queue-full rejection clears the full outbox copy only after creating the metadata-only overflow notice. A `rejected_duplicate_terminal` ack is also definitive: for `retry_existing`, the extension records that the existing job is the one to retry; for `discard_existing_then_recapture`, it records that the user must discard the old row and reopen the transcript. The full duplicate payload is deleted after that notice is recorded. `extension/src/status.ts` maps the exact values to user strings (e.g. `permanent_conflict->"Conflict — manual review needed"`) and is the single mapping used by the popup.
6. Add the root ignore rules so build output, local queue databases, Keychain diagnostics, credentials, and real Leccap captures cannot be committed. `.gitignore` must contain at minimum: `dist/`, `*.sqlite3*`, `*.keychain`, `**/*token*.json`, `**/*secret*.json`, `extension-tests/fixtures/*-real.html`, `native-host/*.json` (rendered), `uploader/lecture-uploader`.
7. Add a project README that explains the personal scope and points to this plan.

Acceptance checks:

- TypeScript type-checking and test commands exist.
- Go unit tests run for the skeletal uploader packages.
- The job schema rejects missing identity, oversized text, unexpected fields, and invalid hashes.
- No test fixture contains a real session token or unredacted page capture.

### Stage 2 — Build and test the Leccap parser

1. Implement the explicit course mapping in `extension/src/course-config.ts`.
2. Implement page classification and metadata resolution in `extension/src/leccap-parser.ts`. It must recognize only the known lecture page shape and must return a rejected result for home pages, unrelated pages, loading-only states, and ambiguous metadata. When Stage 0 selects `linked_overview_page`, resolve the date only through the recorded runtime lookup and player-link correlation; never crawl the overview, guess from the URL, or use the local downloaded MHTML as runtime input.
3. Implement transcript extraction for both the plain and timestamped forms. If the page presents only one form, the parser must document how the second form is derived rather than silently duplicating or inventing timestamps.
4. Implement the transcript completion observer in `extension/src/content.ts`. It should begin after a transcript-control activation or the explicitly defined already-expanded-and-populated initial state, wait for the fixture-defined completion indicator, attach `MutationObserver` only to the transcript container subtree, take two normalized snapshots separated by a 1500ms no-mutation debounce, enforce a 30s overall timeout -> `not_ready`, require >=50 non-whitespace chars, and inspect loading markers only inside the fixture-defined loading/error region. Stop observing after two matching snapshots or a terminal rejection. A `not_ready` result must be recoverable by a later re-toggle/reopen, and must not be retried forever without a new activation.
5. Implement normalization and hashing in `extension/src/transcript-normalizer.ts`. Keep normalization deterministic and preserve meaningful speaker text and timestamps. Must pass shared vectors in `protocol/normalization-vectors.json` (same vectors used by Go).
6. Implement `lectureKey` and schema-shaped job creation in `extension/src/transcript-job.ts`.
7. Add parser fixtures for a valid lecture, a non-lecture page, and an incomplete/loading transcript.
8. Add tests for metadata extraction, transcript extraction, completion-indicator behavior, partial-to-complete rendering, initial already-expanded activation, URL-change reset, container-only mutation observation, timestamp-prefix coverage against the normative regex, month/day date inference from the term, normalization, hash repeatability, and fail-closed behavior.

Acceptance checks:

- The valid fixture produces the expected course, term, lecture number, date, transcript, timestamped transcript, lecture key, and hash.
- Reordering irrelevant whitespace does not change the hash.
- Changing a meaningful word or timestamp does change the hash.
- A missing course or lecture number produces a rejection and no job.
- One transcript activation produces at most one pending outbox entry for a given lecture key and content hash, while retries of that entry remain safe.
- A timestamp-only change changes the framed hash even when the plain transcript is unchanged.

### Stage 3 — Implement the extension runtime and handoff

1. Create the Manifest V3 manifest with only the Leccap host permission, storage, Native Messaging, and `alarms` permissions, plus the required content-script matches. On install/startup create the named `lecture-transcripts-drain` alarm with `periodInMinutes=1`; this is the watchdog that wakes the service worker after suspension.
2. Implement `extension/src/content.ts` as the page-facing coordinator. It must detect transcript activation, call the parser, and send only schema-shaped messages to the service worker.
3. Implement `extension/src/background.ts` as the service worker. It must validate message origin and shape, write each validated job to the bounded pending-handoff outbox before sending it, reconnect after suspension, and relay uploader status without storing GitHub credentials. There is one extension-wide Native Messaging port; concurrent alarm/page/popup callers reuse it and serialize requests. The alarm handler must open/reuse the port, send `connect`, request the first status page, and let the host drain due jobs. It must keep an alarm-owned port open while `drainState=working` or `authorizing`, and may close it only after `drainState=idle` or `waiting_for_backoff`. If the service worker or port is suspended, the next alarm retries; a 5s or 30s backoff is exact only while the host remains alive, while a suspended worker may resume on the next alarm (at most roughly one minute under normal Chrome scheduling). Chrome being fully closed remains out of scope.
4. Implement `extension/src/native-messaging.ts` as a narrow client for the defined messages. It must use the single persistent `runtime.connectNative` port for job submission and status, attach a unique `requestId` to every request, validate the exact protocol envelopes, never use `sendNativeMessage` for a job that may need retrying, and handle connection loss without deleting an unacknowledged outbox entry. An alarm-owned port closes only after the host reports `drainState=idle` or `waiting_for_backoff`; a page/popup connection may remain open while work or authorization is active. A port close during `working` or `authorizing` is treated as an interruption, not completion.
5. Implement `extension/src/extension-storage.ts` for lightweight status plus a bounded pending-handoff outbox of at most three full jobs and at most 20 metadata-only overflow notices. A job is written before its first submit attempt and deleted only after a definitive submit ack; `queued` and `already_queued` indicate durable ownership, while rejection acks are surfaced with the documented recapture/retry action. The outbox does not upload, retry GitHub calls, or own remote status; it exists only to survive service-worker suspension and Native Messaging disconnects.
6. Implement the popup files and `extension/src/popup.ts` for connection status, extension/uploader build versions, pending handoffs, overflow notices, last upload, paginated queued jobs, rejected jobs, permanent conflicts, retryable errors, connect, credential reset, eligible Retry actions, and terminal-job Discard actions. The popup must not be required for a normal upload. It must show the `rejected_duplicate_terminal` action and must never display a full transcript from an overflow notice.
7. Add tests using mocked Chrome APIs for activation, handoff, service-worker restart, duplicate capture, outbox replay, outbox capacity, terminal rejection, and status rendering.

Acceptance checks:

- Visiting a Leccap lecture page without opening its transcript causes no submission.
- Opening the transcript produces one validated handoff after the completion signal and two identical stable snapshots.
- The popup never displays or receives a GitHub credential.
- If the Native Messaging host is unavailable, the extension retains the job in the pending-handoff outbox, shows that it is waiting for the uploader, and does not claim that the job is queued.
- Restarting the service worker before acknowledgement replays the pending handoff; the uploader's duplicate check makes the replay safe.
- A fourth pending handoff is rejected without overwriting any of the three existing pending jobs.
- An alarm after service-worker suspension reconnects the host, resumes due uploader work, and never claims a retry is lost merely because the port was previously closed.
- The status UI can page through a 500-row queue without sending or receiving an unbounded status message, and displays both extension and uploader build versions.

### Stage 4 — Implement the native host and durable queue

1. Implement Native Messaging framing and the host loop in `uploader/internal/host/native_messaging.go`. Enforce the Phase 1 application cap of 1_048_576 bytes in either direction, using the 4-byte little-endian length prefix; reject `rejected_oversized` if the claimed frame exceeds that cap and reject malformed frames before parsing a job. The extension uses one persistent `runtime.connectNative` port; the host remains alive for that port and exits when the port closes. On each host start/connect, promote due `retryable_error` rows to `queued`, recover stale leases, and serially drain due work while the port is alive. If authorization is unavailable, leave queued rows untouched and expose the auth state instead of claiming them. Emit `drainState=working` during active processing, `authorizing` during device-code polling, `waiting_for_backoff` when durable work is not yet due, and `idle` when no active or future-due work remains. The one-minute extension alarm is the documented wakeup after suspension; it is not a second queue owner.
2. Implement the shared Go protocol types and validator. Unknown fields, invalid schemaVersion (only `1`), oversized (transcript/timestamped >460800 bytes, total JSON >972800 bytes, sourceUrl >2048 bytes post-canonical), unsafe URLs (non-https, host != leccap.engin.umich.edu), and invalid hashes (not `^[0-9a-f]{64}$`) must be rejected with `rejected_*` category.
3. Implement the SQLite queue in `uploader/internal/queue/store.go`. Store the full job, stable lecture key, content hash, status, attempt count, timestamps, last error category, and last HTTP status only; do not persist free-form remote error text. Use transactions for persist-before-acknowledge and status transitions. DB path: `~/Library/Application Support/LectureTranscripts/queue.sqlite3` with `0600` file mode. Serial claiming via `BEGIN IMMEDIATE; UPDATE jobs SET status='uploading' WHERE id=? AND status='queued'` — only one claimant wins.
4. Put the queue database under the user's Mac application-support directory with restrictive file permissions. Do not put it in the repository or extension storage.
5. Implement serial job claiming so two uploader processes cannot upload the same job concurrently. Processor polls single-job at a time; `uploading` lease is 10 min, expired leases return to `queued` on startup.
6. Implement restart recovery: a job left in `uploading` must return to `queued` with `attempt_count+1` on next startup unless a fresh GET of the target path proves that its frontmatter transcript_sha256 equals the job contentHash (then mark `uploaded` with a recovered-after-restart note). Never auto-mark a job complete without GET proof.
7. Implement sanitized logging to `~/Library/Logs/LectureTranscripts/uploader.log`, with mode 0600 and rotation at 5 MiB across at most three files (`uploader.log`, `.1`, `.2`). Logs may include `lectureKey`, `courseSlug`, `term`, `lectureNumber`, statuses, and error categories, but never transcript bodies, tokens, cookies, headers, full source URLs, or query strings. Log source as `host + path` only (e.g. `leccap.engin.umich.edu/lecture/123`), never `?…` or `#…`. Chrome stderr is not the troubleshooting log. Unit tests assert redaction with `transcript_body`, `token=`, `sessionid`, and device-code samples.
8. Return a submit `ack` only after the database transaction commits or after a non-persisting rejection is final. Echo `requestId`, `lectureKey`, and `contentHash` whenever available. A duplicate (`lectureKey`+`contentHash` match) returns `already_queued` without a new row when the existing job is active, complete, or in conflict. If the existing row is terminal `rejected_permission`, return `rejected_duplicate_terminal` with its job ID, existing status, and `action=retry_existing`; for every other terminal `rejected_*` row return `rejected_duplicate_terminal` with `action=discard_existing_then_recapture`. A new capture never silently replaces or resets an existing terminal row. `rejected_handoff_full` is extension-local and cannot be emitted here.
9. Handle `retry_job` transactionally: move only an eligible `retryable_error`, `permanent_conflict`, or `rejected_permission` row back to `queued`, reset its retry schedule, and preserve its original job and content hash. Require a fresh GET before retrying a permanent conflict; the popup must require the user to confirm that the remote file was deleted before sending the retry command. Do not allow retry to mutate the transcript or bypass write-once checks. Return the exact `command_result` correlation and result.
10. Handle `discard_job` only for a terminal local `rejected_*` or `permanent_conflict` row after an explicit confirmation. Delete that local row transactionally, never issue a GitHub delete, and report the result. Do not allow discard for queued, uploading, uploaded, or unchanged jobs.
11. Add queue tests for insertion, duplicate submission, crash recovery, serial claiming, status transitions, size limits, bounded error metadata, retry eligibility, and terminal-job discard rules.

Acceptance checks:

- Killing the uploader after it receives a job does not lose the job.
- Killing the extension service worker after it persists a pending handoff but before acknowledgement causes a replay, not a lost transcript or a false queued status.
- Restarting the uploader resumes a persisted job.
- A second identical submission does not create a second queue record or GitHub write.
- A malformed or oversized message is rejected without being persisted.

### Stage 5 — Implement web authorization and Keychain storage

1. Implement the selected GitHub App Device Flow in `uploader/internal/auth/web_flow.go`. POST to `https://github.com/login/device/code` with the public App client ID, show `user_code` and `verification_uri` in popup/status, prefer `verification_uri_complete` for the user-facing link when supplied, and poll `https://github.com/login/oauth/access_token` no faster than the returned `interval`. Send the configured numeric `repository_id` on the token request; it is configuration, not a field returned by the device-code response. Honor the new interval returned after `slow_down`, and stop polling at the server-provided `expires_in` deadline. Continue on `authorization_pending`; surface `access_denied`, `expired_token`, `device_flow_disabled`, and other terminal errors as `reauthorization_required`. Persist the in-flight device transaction in the Keychain before polling and resume it after a host restart if its `expiresAt` has not passed; delete it on success, expiry, or terminal failure. The uploader, not the extension, owns polling. After a token is obtained, perform `GET /repos/{owner}/{repo}` and `GET /repos/{owner}/{repo}/contents?ref=main`, verify the numeric repository ID, `owner/repo`, and successful Contents access before marking auth `connected`; wrong-account or missing-installation 403/404 becomes `target_repository_unavailable` and the just-authorized credential is cleared. Classifier reads only `status + x-ratelimit-remaining + Retry-After + message[0:120]` (never logs token/body/query); a target-path 404 means file-absent only after both sanity requests have succeeded, else `rejected_permission`.
2. Implement the credential interface in `uploader/internal/auth/auth.go` so the rest of the uploader never handles raw credential storage details. The interface returns an `Authorization: Bearer <token>` header value only in memory for the GitHub client; the App client ID and repository ID are configuration, not secrets, and no client secret or App private key exists in this design.
3. Implement the macOS Keychain adapter in `uploader/internal/auth/keychain_darwin.go` (`SecItemAdd/Update/Delete`, service=`com.neelbangera.lecturetranscripts`, account=`github-app-user-token`, accessibility=`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`). Store the access token, refresh token when present, expiry timestamps, and the granted repository identity as one replaceable record; store the in-flight device transaction under a separate account in the same Keychain service. Before every GitHub request, refresh when the access token has 5 minutes or less remaining (or no usable expiry is recorded), atomically replacing the token pair and expiry metadata. If no refresh token is present, an expiring access token cannot be refreshed and must surface `reauthorization_required` before the request. If refresh returns an invalid-grant/401, surface `reauthorization_required`. If a GitHub request returns 401, force at most one refresh; a 401 using the newly refreshed token is `reauthorization_required`, not an infinite retry. There must be no plaintext fallback. A missing, expired, or revoked credential is an explicit `not_connected` or `reauthorization_required` status. `reset` = delete both Keychain records + status clear, jobs untouched. The first Keychain access may show a macOS permission prompt; setup must instruct the owner to choose Always Allow, and a rebuilt binary may legitimately prompt again.
4. Wire connect and reset commands through the Native Messaging protocol. `connect` must perform the repository sanity GET before reporting `connected`, and must expose `authorizing` with the user code/link while the persisted device transaction is active. Reset must remove the stored credential and in-flight device transaction and clear connection status without deleting transcript jobs.
5. Add tests with a fake credential store and fake authorization provider. Do not test with a real GitHub token in the repository or test logs.
6. Update setup and security documentation with the one-time browser authorization flow, what is stored locally, and how to disconnect.

Acceptance checks:

- The extension can initiate connection but never sees the credential value.
- A credential is present only in the Keychain and the uploader's in-memory request path.
- Reset removes the Keychain item and leaves queued transcript jobs intact.
- Authorization failure is visible as a clear status and does not cause a transcript upload attempt.
- Closing/restarting the host during device authorization resumes the same unexpired code; an expired code starts a new flow.
- Authorizing the wrong GitHub account fails at connect with `target_repository_unavailable` rather than waiting for the first upload to fail.
- An access token near expiry refreshes before a request, and a post-refresh 401 becomes `reauthorization_required` without a retry storm.

### Stage 6 — Implement Markdown rendering and write-once GitHub publishing

1. Implement the Markdown renderer in `uploader/internal/markdown/render.go`. It must produce the exact frontmatter and the two labeled transcript sections defined below. `transcript_sha256` == job `contentHash`. If `timestampedTranscript==""`, render `## Timestamped transcript` with `_No timestamped source; see Transcript._`.
2. Sanitize or omit `source_url` according to the exact canonicalization and vector rules above. The extension pre-canonicalizes for fast rejection; the uploader repeats the operation and persists the canonical value before rendering. Split the canonical path on `/._-`, case-insensitive whole-segment match against `{token,session,auth,sid}` (so `author` does NOT trigger, `auth` does). Include in frontmatter only if host==`leccap.engin.umich.edu` and no segment matches. Else omit the field entirely. Never place session tokens, authorization parameters, query values, or unrelated page text in frontmatter. This keeps the artifact safe if the repository goes from private to public.
3. Implement the GitHub client and Contents API operations in `uploader/internal/github/client.go` and `contents.go`. Send the documented `Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2026-03-10` headers. Use `GET /repos/{owner}/{repo}/contents/{path}?ref={configured branch}` then `PUT` create with `message="Add <lectureKey> (<courseName> lecture <N>)"` and `branch={configured branch}`. The configured branch is verified during Stage 0 and must be `main`; the placeholder means the validated config value, not a second runtime choice. Encode the branch as a query/body value, never by string concatenation into a path. Each GitHub Contents request has a 30-second total deadline; a timeout or cancellation is a retryable error, and no request may outlive the ten-minute queue lease. Omit custom author and committer objects so GitHub uses the authenticated App user identity. Commit message example: `Add eecs491/2026-winter/006 (EECS 491 lecture 6)`.
4. For each queued job, derive the stable path `courses/<courseSlug>/<term>/lectures/<NNN>.md` with NNN zero-padded 3 digits (e.g. `courses/eecs491/2026-winter/lectures/006.md`). Frontmatter `lecture:` stays integer (e.g. `6`), `term:` stays human `Winter 2026`, filename uses normalized `term` + padded number.
5. Read the target path before writing. If it does not exist (404), issue a create-only PUT without a sha. If the Contents API returns a directory object or a JSON array for the target path, classify it as `permanent_conflict` with a directory-at-file-path diagnostic; never treat it as a missing file. Any object whose `type` is not exactly `file` (including directory, symlink, and submodule) is a conflict. Accept only the documented created response as a successful upload. For an existing file, decode the documented base64 content, require a frontmatter block beginning at byte zero and ending at the next `---`, and accept only one `transcript_sha256` scalar matching `^[0-9a-f]{64}$`. If it equals job `contentHash`, mark `unchanged` without commit. If the hash differs or the frontmatter is unparseable/missing, mark `permanent_conflict` and do not update. Malformed remote files are never trusted for same-hash comparison. Never send a PUT containing sha in Phase 1.

The write-once comparison intentionally trusts a valid remote `transcript_sha256` frontmatter field as the remote contract. If a person manually edits the Markdown body without changing that field, a later capture is classified as `unchanged`; Phase 1 does not attempt to reverse-engineer or overwrite manual edits. `SECURITY.md` and `TROUBLESHOOTING.md` must call this out as an accepted machine-owned-file invariant.
6. Treat errors as categories in `uploader/internal/github/errors.go`: `retryable` (timeout, network, 429, 5xx, 403 with `x-ratelimit-remaining: 0` or `Retry-After`) -> persist `retryable_error` with backoff `5s,30s,2m,10m,1h` and ±20% jitter, indefinitely; when `next_attempt_at` arrives, move the row to `queued` and retry, resetting on success. `auth` (401, 403 Bad credentials) -> `reauthorization_required`, pause queue; `permission` (403 App permission insufficient, 404 repo) -> `rejected_permission` stop + status, no retry; `conflict` (409 or a non-created response followed by a newly existing target) -> `permanent_conflict`. A non-created response is never treated as success: re-GET the target and classify it as unchanged, conflict, or the original error. Store only `{category, httpStatus}` — never token/body/URL query. Only `retryable` auto-retries. Popup for `permanent_conflict` shows path + local contentHash vs remote transcript_sha256 or malformed-file warning, with manual delete-then-retry flow and no overwrite button.
7. Process jobs serially. A GitHub conflict must not be resolved by blindly refetching and overwriting a write-once file.
Retryable GitHub failures are persisted as `retryable_error` with `next_attempt_at`; the scheduler moves them to `queued` only when eligible. The retry loop is indefinite, but only the uploader performs it and the extension never retains a second full copy after acknowledgement.
8. Add fake-server tests for create, unchanged revisit, different-content conflict, malformed existing file, authentication failure, rate limit, network retry, and remote conflict.

The generated lecture file format is:

```text
---
course: 'EECS 491'
term: 'Winter 2026'
lecture: 6
date: '2026-02-12'
source_url: 'https://leccap.engin.umich.edu/...'
captured_at: '2026-02-12T...'
transcript_sha256: '...'
---

## Transcript

...

## Timestamped transcript

...
```

Acceptance checks:

- A new lecture creates exactly one remote Markdown file and one commit.
- Reopening the same transcript produces no new commit.
- A changed transcript at the same path produces no overwrite and a permanent conflict status.
- No README or unrelated repository file is modified.

All frontmatter string values are emitted as YAML single-quoted scalars, with an internal single quote doubled. The canonical course name comes from the Stage 0 allowlist and must contain no newline; the fixed term/date/hash fields are validated before rendering. This prevents page-derived text from changing frontmatter structure.

### Stage 7 — Package, install, and document the personal deployment

1. Build the extension into a clean distribution directory. The build reads `extension-tests/fixtures/lecture-page.selectors.json` and bundles the verified selector configuration into the parser/content-script JavaScript; production does not fetch a fixture file at runtime. The build must fail if the selector fixture is missing, contains template markers, or is not valid JSON. The distribution must contain only the manifest, compiled extension scripts, popup assets, and required icons if icons are later added.
2. Build the Mac uploader as one native executable with `CGO_ENABLED=1` and the Darwin Security framework. The setup prerequisite is the Xcode Command Line Tools; the build must fail for unsupported platforms or a missing compiler rather than silently producing an unusable host.
3. Render the Native Messaging host manifest with the absolute path to the installed uploader and the exact extension ID supplied by the installer. The installer must require an explicit extension ID from the loaded extension, validate its format, place only that ID in `allowed_origins`, and never use a wildcard. The unpacked extension ID is tied to the loaded unpacked-extension path in this deployment: moving or reloading it from a different path can change the ID, so the installer must be rerun with the new ID. Do not commit a machine-specific absolute path as if it were portable configuration.
4. Install the rendered host manifest under the Chrome Native Messaging Hosts directory for the current user. The host must also validate the origin passed by Chrome at startup and exit if it is not the configured extension origin.
5. Provide an uninstall operation that removes only the host manifest and installed uploader created by this project. It must not delete the GitHub repository, queue jobs, or Keychain credentials without an explicit reset/uninstall choice.
6. Document setup, authorization, permissions, local data, reset, troubleshooting, testing, repository visibility, the initialized-main-branch prerequisite, Xcode Command Line Tools, the first Keychain Always Allow prompt, the alarm-based retry timing, the fixed unpacked-extension path/ID relationship, the log destination and rotation, wrong-account authorization, overflow recapture, and the accepted remote-hash/manual-body invariant.
7. Keep build output, native binaries, rendered manifests, queue databases, and real captures out of version control unless the plan is deliberately updated.

Acceptance checks:

- A clean Mac installation can load the unpacked extension and connect to the native host.
- Reinstalling does not create duplicate host registrations.
- Uninstalling removes only this feature's installed files.
- The setup documentation is sufficient for the owner to recover from a disconnected credential or failed queue.
- The setup documentation explicitly requires an initialized `main` branch, Xcode Command Line Tools, and the Keychain permission response; it explains that moving the unpacked extension requires rerunning host installation with the new ID.
- A troubleshooting reader can find the uploader log at the exact path, understand alarm granularity after worker suspension, and recover a full handoff/queue by reopening the transcript.

### Stage 8 — End-to-end verification and handoff

Run the following scenarios against one real permitted lecture and otherwise sanitized test data:

Before running the scenarios, verify that the configured repository exists, its `main` branch has at least one commit, the App is installed on that exact repository with Contents read/write, the local config contains the real client/repository IDs, the Mac has Xcode Command Line Tools, and the extension is loaded from the path used to render the host manifest. These are preconditions, not scenarios for the uploader to repair.

1. First capture: transcript activation creates one queue job and one remote Markdown file.
2. Revisit: the same transcript reports `unchanged` and creates no commit.
3. Page visit only: no job is created.
4. Transcript still loading: no upload occurs until the content is credible and stable; a page that exceeds the evidence-based 30s render budget returns `not_ready`, shows recovery guidance, and succeeds only after a later re-toggle/reopen.
5. Missing metadata: the job is rejected with a diagnostic and no repository change.
6. GitHub unavailable: the job remains queued, retries with backoff, and eventually succeeds without duplication. Kill the service worker between attempts and verify the one-minute alarm reconnects it; document that exact 5s/30s timing is not promised while Chrome is asleep.
7. Browser or uploader restart: a persisted job resumes, and a host restart during device authorization resumes the unexpired device code.
8. Different content at an occupied path: the existing file remains untouched and the job becomes a permanent conflict.
9. Credential reset: upload pauses, the job remains durable, and reconnection resumes it. Authorizing a GitHub account without access to the target repository fails fast as `target_repository_unavailable`.
10. Log inspection: no transcript body, credential, cookie, authorization header, unsafe URL, device code, or query string appears; the log file has the documented path, mode, and rotation.
11. Repository visibility change: the generated artifact remains valid when the repository is private or public.
12. Already-expanded transcript: a visible, populated transcript present before the content script observes a click captures once; a hidden/collapsed page visit does not.
13. Outbox/queue overflow: the fourth handoff or a full uploader queue is visibly rejected, produces a metadata-only recapture notice, and never overwrites an older pending job.
14. Remote directory/manual edit: a directory at the lecture path becomes `permanent_conflict`; a body-only manual edit with unchanged `transcript_sha256` is intentionally reported as `unchanged`.

The implementation is complete only when all scenarios pass, the file inventory below matches the actual change set, and any deviation has been added to this plan before being implemented.

## Complete implementation file inventory

This is the complete planned file set for the implementation. “Create” means the file does not exist in the current repository. “Change” means an existing file is intentionally edited. Generated and machine-local artifacts are listed separately. No additional source or configuration file should be added without first updating this inventory and its rationale.

### Existing design files

| File | Action | Rationale |
| --- | --- | --- |
| `TECHNICAL_PLAN.md` | Change now | This document becomes the implementation source of truth, including scope, guardrails, steps, tests, and file inventory. |
| `PHASE_1_DECISIONS.md` | Change now | Keeps the rejected-alternative rationale while updating its resolved-decision section so it cannot contradict this plan. |
| `TECHNICAL_IMPLEMENTATION_PROPOSAL.md` | Mark superseded now | It is an earlier proposal; its banner must direct every reader to this plan and prevent older auth, hash, handoff, and write semantics from being reused. |

### Root project files

| File | Action | Rationale |
| --- | --- | --- |
| `README.md` | Create | Explains the personal extension, quick start, scope, and links to this plan. |
| `.gitignore` | Create | Prevents build output, binaries, local databases, credentials, and real captures from entering Git. |
| `package.json` | Create | Defines TypeScript build, type-check, and test commands plus extension dependencies. |
| `package-lock.json` | Create/generated | Locks the JavaScript toolchain so extension builds are reproducible. |
| `tsconfig.json` | Create | Defines strict TypeScript compilation for the extension and tests. |
| `vitest.config.ts` | Create | Defines the TypeScript unit-test environment and fixture handling. |
| `protocol/transcript-job.schema.json` | Create | Canonical browser-to-uploader job contract shared conceptually by TypeScript and Go. |
| `protocol/native-messaging.schema.json` | Create | Canonical strict request/response envelopes, request correlation, status pagination, drain lifecycle, version fields, and rejection actions for the extension/uploader boundary. |
| `protocol/source-url-vectors.json` | Create | Shared canonicalization and publish-sanitization vectors so TypeScript and Go cannot disagree about the stored source URL. |
| `protocol/normalization-vectors.json` | Create | Shared NFC/whitespace/hash test vectors enforced by both TS and Go normalizers. |

### Extension files

| File | Action | Rationale |
| --- | --- | --- |
| `extension/manifest.json` | Create | Declares the Manifest V3 extension, narrow Leccap host permissions, storage, Native Messaging, and the `alarms` permission. The installed extension ID is supplied to the native-host installer and must be the only allowed origin. |
| `extension/popup.html` | Create | Provides the small status and connection UI shell. |
| `extension/popup.css` | Create | Styles the status UI without introducing a frontend framework. |
| `extension/src/popup.ts` | Create | Reads status and sends connect/reset/status requests; it never handles GitHub credentials. |
| `extension/src/content.ts` | Create | Runs on Leccap pages, detects transcript activation, observes loading, and submits extracted jobs. |
| `extension/src/background.ts` | Create | Implements the service-worker coordinator, exact message validation/correlation, alarm watchdog, handoff, reconnection, paginated status relay, and version display without storing GitHub credentials. |
| `extension/src/course-config.ts` | Create | Stores the explicit personal course mapping and path-safe course slug rules. |
| `extension/src/leccap-parser.ts` | Create | Classifies the known lecture page and extracts metadata and transcript forms. |
| `extension/src/transcript-normalizer.ts` | Create | Applies deterministic whitespace/line-ending normalization and calculates the content hash. |
| `extension/src/transcript-job.ts` | Create | Builds `lectureKey`, validates job fields, and produces the schema-shaped payload. |
| `extension/src/native-messaging.ts` | Create | Encapsulates the narrow Native Messaging request/response vocabulary. |
| `extension/src/extension-storage.ts` | Create | Persists lightweight status, a bounded three-job pending-handoff outbox, and at most 20 metadata-only overflow notices that survive service-worker suspension; it never owns GitHub retry state. |
| `extension/src/status.ts` | Create | Defines extension-facing status values and user-readable error mapping. |

### Extension tests and fixtures

| File | Action | Rationale |
| --- | --- | --- |
| `extension-tests/leccap-parser.test.ts` | Create | Tests page classification, metadata extraction, transcript extraction, and fail-closed cases. |
| `extension-tests/transcript-normalizer.test.ts` | Create | Tests stable normalization, timestamp preservation, and hash behavior. |
| `extension-tests/transcript-job.test.ts` | Create | Tests lecture-key derivation, schema limits, URL sanitization, and invalid jobs. |
| `extension-tests/native-messaging.test.ts` | Create | Tests message construction, acknowledgement handling, reconnectable errors, and status mapping with mocked Chrome APIs. |
| `extension-tests/fixtures/lecture-page.html` | Create | Redacted or synthetic valid lecture-page DOM fixture. |
| `extension-tests/fixtures/lecture-page.selectors.json` | Create | Human-verified selectors and completion/loading predicates captured during Stage 0; imported by the parser, tested as a fixture, and embedded into the production bundle at build time rather than fetched at runtime. |
| `extension-tests/fixtures/lecture-page.expected.json` | Create | Expected parser result for the valid fixture. |
| `extension-tests/fixtures/overview-page.html` | Create conditionally after Stage 0 | Redacted overview-page DOM used when the recording date is sourced from the linked course overview. |
| `extension-tests/fixtures/no-number-lecture-page.html` | Create after Stage 0 | Negative lecture-page fixture with the observed no-numeric-prefix recording title ("Lecture recorded on 9/17/2026"); proves the fail-closed identity rule. |
| `extension-tests/fixtures/no-number-lecture-page.expected.json` | Create after Stage 0 | Expected `rejected_ambiguous_metadata` result for the no-number fixture, including the badge-is-not-a-lecture-number rule. |
| `extension-tests/fixtures/course-mapping.json` | Create after Stage 0 | Complete supported course/term allowlist; the parser configuration is copied from this artifact rather than invented during Stage 2. |
| `extension-tests/fixtures/transcript-size-report.json` | Create after Stage 0 | Measured UTF-8 transcript, serialized-job, Native Messaging, and render-time values proving that the approved caps and 30-second observation budget fit representative supported pages. |
| `extension-tests/fixtures/non-lecture-page.html` | Create | Ensures unrelated pages are rejected. |
| `extension-tests/fixtures/loading-transcript-page.html` | Create | Ensures incomplete or loading text is not uploaded. |

### Native uploader files

| File | Action | Rationale |
| --- | --- | --- |
| `uploader/go.mod` | Create | Defines the Go module and native uploader dependencies. |
| `uploader/go.sum` | Create/generated | Locks Go dependency checksums. |
| `uploader/cmd/lecture-uploader/main.go` | Create | Starts the Native Messaging host and exposes narrowly scoped connect, reset, status, retry, discard, and processing commands. |
| `uploader/internal/protocol/job.go` | Create | Defines the Go representation of `TranscriptJob`. |
| `uploader/internal/protocol/messages.go` | Create | Defines the exact versioned Native Messaging requests/responses, request correlation, duplicate-terminal actions, fixed status summaries, and cursor pagination. |
| `uploader/internal/protocol/validate.go` | Create | Rejects malformed, oversized, unexpected, or unsafe messages before persistence. |
| `uploader/internal/host/native_messaging.go` | Create | Implements Chrome Native Messaging framing, the conservative application size cap, persistent-port host lifecycle, and origin validation. |
| `uploader/internal/queue/store.go` | Create | Implements the SQLite durable queue, transactions, status transitions, deduplication, bounded error metadata, and restart recovery. |
| `uploader/internal/queue/schema.sql` | Create | Canonical migration-1 DDL copied from the queue contract above; prevents agents from inventing tables or indexes. |
| `uploader/internal/queue/store_test.go` | Create | Tests durable persistence, duplicate jobs, crash recovery, claiming, status transitions, retry eligibility, and terminal discard rules. |
| `uploader/internal/auth/auth.go` | Create | Defines the credential-store and authorization interfaces used by the rest of the uploader. |
| `uploader/internal/auth/keychain_darwin.go` | Create | Uses cgo and the macOS Security framework to store the GitHub App credential and resumable device-flow transaction in separate Keychain records, with no plaintext fallback or partial refresh record. |
| `uploader/internal/auth/web_flow.go` | Create | Owns the selected GitHub App Device Flow, persisted in-flight authorization, server-provided polling interval/expiry, configured repository restriction, repository sanity check, token refresh threshold, and completion status. |
| `uploader/internal/auth/auth_test.go` | Create | Tests authorization and reset behavior with fake providers and stores. |
| `uploader/internal/github/client.go` | Create | Provides authenticated GitHub HTTP behavior, request limits, and sanitized error handling. |
| `uploader/internal/github/contents.go` | Create | Implements preflight path lookup, same-hash detection, no-sha create-only attempts, race rechecks, and conflict classification without claiming conditional-create semantics. |
| `uploader/internal/github/errors.go` | Create | Maps HTTP and network failures to retryable, authentication, permission, rate-limit, or permanent categories. |
| `uploader/internal/github/contents_test.go` | Create | Tests create, unchanged revisit, conflict, rate limit, authentication failure, and transient retry behavior. |
| `uploader/internal/markdown/render.go` | Create | Renders the fixed frontmatter, plain transcript, and timestamped transcript sections. |
| `uploader/internal/markdown/render_test.go` | Create | Locks the generated Markdown format and metadata sanitization. |
| `uploader/internal/retry/backoff.go` | Create | Defines the capped delay schedule and jitter for indefinitely retryable transient failures. |
| `uploader/internal/retry/backoff_test.go` | Create | Tests schedule, jitter bounds, reset behavior, and persistence across attempts under a test clock. |
| `uploader/internal/processor/processor.go` | Create | Serially claims queue jobs, calls GitHub, applies write-once rules, and records final status. |
| `uploader/internal/processor/processor_test.go` | Create | Tests end-to-end queue-to-publish decisions with fake GitHub and auth dependencies. |
| `uploader/internal/config/config.example.json` | Create | Documents the exact machine-local configuration shape with nonfunctional placeholders; it contains no real client ID, RepositoryID, token, extension ID, or secret. |
| `uploader/internal/config/config.go` | Create | Loads and validates the machine-local config path, target repository fields, paths, 972800/460800/2048 limits, and backoff schedule; it never hardcodes missing account values or stores secrets. |
| `uploader/internal/logging/sanitized.go` | Create | Centralizes privacy-safe structured logging to the documented rotated 0600 log files and prevents transcript, query, device-code, and credential leakage. |
| `uploader/internal/logging/sanitized_test.go` | Create | Verifies that sensitive fields and unsafe URL components never appear in logs. |
| `uploader/testdata/existing-same-hash.md` | Create | Fixture for an idempotent unchanged remote file. |
| `uploader/testdata/existing-different-hash.md` | Create | Fixture for a permanent write-once conflict. |
| `uploader/testdata/malformed-lecture.md` | Create | Fixture for an existing file that cannot be trusted for same-hash comparison. |

### Native host and build/install files

| File | Action | Rationale |
| --- | --- | --- |
| `native-host/com.neelbangera.lecturetranscripts.json.in` | Create | Template with `{{BINARY_PATH}}` + `{{EXTENSION_ID}}` vars; install requires and validates the loaded extension ID, renders one exact allowed origin, and never uses a wildcard. |
| `scripts/build-extension.mjs` | Create | Validates and bundles the Stage 0 selector JSON into the content/parser bundle, copies manifest/popup, injects the extension version, and outputs `dist/extension/`; it never leaves a runtime dependency on the test fixture path. |
| `scripts/build-uploader.sh` | Create | `#!/bin/sh` `0755`, requires macOS/Xcode Command Line Tools and `CGO_ENABLED=1`, builds `GOOS=darwin GOARCH=arm64` with an injected uploader version, and fails on non-darwin or missing compiler. |
| `scripts/install-native-host.sh` | Create | `0755`, requires the loaded extension ID as an argument, renders the template to `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json` (`0600`), validates the single allowed origin, and does not create duplicate registrations. |
| `scripts/uninstall-native-host.sh` | Create | `0755`, removes only rendered JSON + `dist` uploader, never repo/queue/Keychain unless `--reset`. |

### Documentation files

| File | Action | Rationale |
| --- | --- | --- |
| `docs/STAGE_0_REPORT.md` | Create after Stage 0 | Records the human evidence, measured maxima, full course inventory, and verified App/repository provisioning without secrets; it was the implementation gate artifact and now records the two outstanding live items (render-time measurement and machine-local provisioning). |
| `docs/SETUP.md` | Create | Gives the owner installation, fixed-path extension loading, initialized-main-branch/repository configuration, Xcode/Keychain prerequisites, alarm behavior, and web authorization steps. |
| `docs/SECURITY.md` | Create | Documents permissions, Keychain/device-flow storage, local queue data, logging restrictions, source-URL sanitization, remote-hash trust, and reset behavior. |
| `docs/TESTING.md` | Create | Documents unit, integration, fixture, offline, duplicate, conflict, and end-to-end tests. |
| `docs/TROUBLESHOOTING.md` | Create | Explains disconnected auth/wrong-account failures, Native Messaging failures and unpacked-ID drift, alarm-delayed retries, log location, queued jobs, overflow recapture, rejected metadata, and permanent conflicts. |

### Generated and machine-local artifacts

These artifacts are expected at runtime or during packaging but are not hand-authored source files and should not be committed by default:

- `dist/extension/`: compiled extension assets generated from `extension/` and the build script.
- `dist/native/lecture-uploader`: the Mac uploader binary generated from `uploader/`.
- `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`: the rendered per-user Chrome host manifest.
- `~/Library/Application Support/LectureTranscripts/config.json`: machine-local non-secret runtime configuration containing the actual App client ID, numeric RepositoryID, target repository, and branch; never committed. The extension ID is supplied only to the host-manifest installer.
- `~/Library/Application Support/LectureTranscripts/queue.sqlite3`: the local durable queue database.
- `~/Library/Logs/LectureTranscripts/uploader.log` plus at most `.1` and `.2`: mode-0600 sanitized uploader logs, rotated at 5 MiB.
- The macOS Keychain records containing the GitHub credential and, while active, the resumable device-flow transaction.

The uploader may create one remote file per successfully captured lecture at:

```text
neelbangera/lecture-transcripts/
  courses/<courseSlug>/<term>/lectures/<NNN>.md  (NNN zero-padded, e.g. 006.md; frontmatter lecture: 6)
```

Those remote lecture files are created dynamically from jobs. The uploader must never modify the repository README, course README, or an existing lecture file with different content. Repo must pre-exist; uploader never creates repo.

## Definition of done

Current status is `IMPLEMENTED_THROUGH_PACKAGING / LIVE_ITEMS_OUTSTANDING`: the code through the packaging layer is in the tree, but the Go executable entrypoint and serial processor, the live render-time measurement, and the machine-local provisioning packet are still outstanding. The implementation is not complete merely because the normative source contracts exist; the actual Leccap fixture, course inventory, measurements, and account setup must still pass the remaining readiness checks.

The implementation is ready for personal use only when all of the following are true:

Before evaluating these criteria, the Stage 0 packet and provisioning packet must pass validation, and the Stage 7 installer must receive the real loaded extension ID.

1. The file inventory above matches the repository change set.
2. The parser works against a redacted fixture derived from an actual permitted Leccap page.
3. Transcript activation, not page navigation, is the only normal capture trigger.
4. A job is durably queued before the extension reports success.
5. GitHub authorization is web-based and the credential is absent from extension storage and logs.
6. A new lecture creates one Markdown file, a revisit creates no new commit, and a different-content collision never overwrites.
7. Offline, restart, authentication failure, malformed page, and logging tests pass.
8. Setup, reset, uninstall, and recovery behavior are documented.
9. Any change to scope, identity, authentication boundary, queue ownership, or write-once behavior is made here before code changes begin.
