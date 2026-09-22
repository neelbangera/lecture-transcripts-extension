# Phase 1 Decisions and Rejected Alternatives

> This is the rationale record for the Automatic Leccap Transcript Uploader. `TECHNICAL_PLAN.md` is the enforceable source of truth; this document must agree with it. It records the reasoning behind the Phase 1 boundaries and should be used to prevent quietly reintroducing rejected approaches.

## Phase 1 baseline

Phase 1 is a personal tool for one Mac, one Chrome installation, and one authenticated Leccap session. The user must open or expand the transcript on a lecture page. That action activates capture for the current lecture; merely visiting a Leccap page does not.

If the transcript is already visibly expanded and populated when the content script starts, that state is treated as the same explicit activation. A collapsed or merely preloaded transcript is not.

After the transcript is activated, the system should capture the transcript without requiring a second upload click. It should send selected transcript text and lecture metadata through a Chrome extension to a local uploader, then write a new Markdown file to the personal GitHub repository.

The local uploader owns the acknowledged durable queue, retries, and GitHub credential. The extension has only a bounded pending-handoff outbox for jobs that have not yet been acknowledged. GitHub authorization happens through the website, while the resulting credential is kept in the Mac Keychain rather than the extension. Lecture files are machine-owned and write-once: an unchanged existing file is considered complete, but a different existing file is never overwritten automatically.

The first version relies on course, term, lecture number, and date from authenticated page metadata. Those values may be split across the lecture page and its linked course overview; any cross-page lookup and player-link correlation must be explicitly captured in Stage 0. Guest lectures, split recordings, historical crawling, multiple users, and multiple repositories are outside the Phase 1 model.

## Decisions and rejected alternatives

### 1. Transcript activation instead of page-visit upload

Considered: upload whenever a lecture page is visited, scan the Leccap home page, or infer that a transcript should be captured from the URL alone.

Rejected because visiting a page is not the same as asking to save its transcript. A page can be incomplete, can be opened for review only, or can contain a transcript that has not finished loading. It also creates a surprising publication action.

Selected: clicking or expanding the transcript is the capture trigger. If the content script starts with a transcript that is visibly expanded and populated, that observed state counts as the equivalent activation so a missed click cannot lose a capture. A hidden, collapsed, or merely preloaded transcript is not activation. The phrase “open a lecture” in the technical plan means opening the transcript, not merely navigating to the lecture page.

Guardrail: page navigation alone must never create a repository job.

### 2. Automatic handoff after activation instead of manual copy-and-upload

Considered: keep the workflow as manual copy/paste, require the user to press an upload button for every lecture, or provide only a helper that extracts text.

Rejected because the purpose of the feature is to remove repetitive filing work after the user has already chosen to open the transcript.

Selected: after the transcript activation, extraction, validation, and upload happen automatically. A status view may show what happened, but it is not part of the normal upload path.

Guardrail: “automatic” begins only after the transcript activation; it does not mean unattended crawling.

### 3. Local uploader instead of direct GitHub access from the extension

Considered: have the browser extension call the GitHub API directly.

Rejected because the extension would then need to hold or handle a long-lived GitHub credential. Browser extension lifecycle and storage also make durable retry behavior less reliable.

Selected: the extension sends a small, validated transcript job to a local uploader. The uploader is the security and reliability boundary for GitHub access.

Guardrail: the extension must never contain a GitHub access token, refresh token, or private key.

### 4. Web authorization plus Keychain storage instead of an extension token

Considered: paste a personal access token into the extension, bundle a GitHub App private key, store a token in extension storage, or ask the user to keep a plaintext credential file.

Rejected because each option puts a durable secret in the extension or in an easily copied file.

Selected: authorize a GitHub App through GitHub's web device flow. The App is installed only on the destination repository and requests only Contents read/write. The local uploader owns device-code polling, persists an unexpired in-flight device transaction in Keychain across host restarts, verifies the target repository and a successful Contents read on initialized `main` before reporting connected, and stores the resulting user credential in macOS Keychain. The extension receives only connection status and upload results; it never receives a token, refresh token, client secret, or App private key. The uploader refreshes an access token when it has five minutes or less remaining and treats a post-refresh 401 as reauthorization.

Guardrail: web authorization does not mean that no credential exists; it means the credential is kept out of the extension and restricted to the local uploader.

### 5. Personal local deployment instead of a hosted multi-user service

Considered: build a hosted backend, support multiple GitHub accounts, or design the first version as a distributable extension.

Rejected for Phase 1 because those choices add account management, server operations, privacy boundaries, and a broader authentication model without helping the one-user workflow.

Selected: one Mac, one browser, one GitHub account, and one repository.

Guardrail: do not introduce backend or multi-user abstractions unless the product scope changes explicitly.

### 6. Uploader-owned durable queue instead of an extension-owned queue

Considered: keep the full pending queue in extension storage, keep duplicate queues in both the extension and uploader, or rely only on in-memory state.

Rejected because Manifest V3 service workers can be suspended, and two independent durable queues create unclear ownership and acknowledgement failures.

Selected: the local uploader is the sole owner of acknowledged durable jobs, retry state, and GitHub state. The extension may keep a bounded three-job handoff outbox only until the uploader acknowledges durable persistence; it cannot upload, retry GitHub calls, or represent remote state. The uploader acknowledges a job only after persisting it.

Guardrail: a handoff is not complete merely because a message was sent; it is complete only after durable persistence is acknowledged. The extension deletes its pending copy only after that acknowledgement and never overwrites an older pending copy when its outbox is full.

When the three-job extension outbox or the uploader queue is full, the system rejects the new handoff visibly and stores only a bounded metadata-only overflow notice for recapture. It never drops an older pending transcript and never claims that a full-queue rejection was saved.

### 7. Native Messaging instead of a general local HTTP server

Considered: expose a loopback HTTP endpoint as the permanent extension-to-uploader interface.

Rejected for the final design because even a loopback server introduces a port, authentication token, request validation, and local attack-surface problem.

Selected: Chrome Native Messaging for the permanent boundary. A loopback endpoint may be used temporarily during early development only if it is bound to `127.0.0.1`, authenticated, schema-validated, and size-limited.

Guardrail: do not turn the uploader into an unauthenticated general-purpose local web server.

### 8. At-least-once delivery instead of an exactly-once promise

Considered: promise that every transcript is delivered exactly once across browser crashes, process restarts, network failures, and GitHub responses.

Rejected because a crash can occur between a remote write and a local acknowledgement. Exactly-once behavior is not a reliable assumption at this boundary.

Selected: at-least-once handoff with a deterministic `lectureKey`, a framed `contentHash` over both normalized transcript forms, and idempotent repository checks. Repeated jobs are safe because an existing file with the same hash is treated as already complete.

Guardrail: retries must be safe; they do not need to be invisible at the message-delivery level.

### 9. Stability checks instead of immediate extraction on click

Considered: treat the transcript click as proof that the full transcript is ready, or use a fixed delay and upload whatever is present.

Rejected because the transcript may still be rendered or populated after the click. A fixed delay is also brittle across network and page-load conditions.

Selected: require the observed page's explicit completion signal, then take two normalized snapshots separated by the unchanged-content debounce. Both snapshots must agree and pass the sanity checks before submission. The observer is scoped only to the transcript container subtree, and a `not_ready` timeout is visible and recoverable by a later re-toggle/reopen rather than starting an unbounded retry loop.

Guardrail: the click authorizes capture but does not prove completeness. A page without a reliable completion signal is unsupported in Phase 1, and the system must report “not ready” without uploading.

### 10. Page metadata instead of URL-only or generalized recording identity

Considered: derive the repository path only from the URL, require a separate opaque recording ID, or support a generalized identity model for every possible Leccap recording type.

Rejected for Phase 1 because the relevant course, term, lecture number, and recording date are available in the authenticated lecture/overview metadata for the supported page shape, and the known personal course set does not contain guest lectures or split recordings. This does not permit URL guessing or an unbounded overview crawl: Stage 0 must record the exact source and correlation rule.

Selected: derive a deterministic lecture key and repository path from normalized course, term, lecture, and recording-date metadata. Keep the source URL as supporting metadata, not as the lecture's primary identity. If the date is on the linked overview, use only the explicitly recorded runtime lookup and player-link correlation.

Guardrail: if required metadata is missing or ambiguous, stop. Do not guess a course or lecture path from partial information.

A lecture-category overview card whose title contains no numeric lecture number
is therefore rejected as `rejected_ambiguous_metadata`. The category badge (for
example, `Lecture - 001`) is a recording/category label, not a lecture number,
and must never be substituted into the identity. Supporting such a card would
require an explicit Phase 1 identity change rather than a parser heuristic.

### 10a. Term-aware dates instead of rejecting every yearless date

Considered: require a year in every page date, infer a year from the term, or guess a locale for ambiguous numeric dates.

Rejected the first option because a lecture page may display an unambiguous month/day while the term already supplies the year. Rejected the last option because locale guessing changes identity silently.

Selected: accept an unambiguous month/day without a year by using the validated term year; reject ambiguous numeric formats and invalid calendar dates. `Spring 2026` and `Summer 2026` are separate valid terms. A literal `Spring/Summer 2026` label is not normalized into a new enum and is rejected as ambiguous.

Guardrail: date inference is permitted only after term validation and never overrides an explicit year.

### 11. Narrow personal page support instead of generalized Leccap coverage

Considered: support every Leccap page shape, guest lectures, office hours, split recordings, multiple courses, and historical pages during the first implementation.

Rejected because broad parser coverage would make the important identity and completeness rules unclear before the first known page shape works reliably.

Selected: support the observed lecture page and add another page shape only when the personal use case requires it and its repository rule is explicit.

Guardrail: a new page shape requires a parser fixture and an intentional path decision; it must not be accepted accidentally because it happens to contain text.

### 12. Transcript text instead of full HTML or arbitrary page capture

Considered: save the complete page, raw HTML, browser cookies, request headers, or unrelated Leccap content alongside the transcript.

Rejected because it increases privacy exposure, payload size, credential risk, and noise in the AI-readable repository.

Selected: send only the extracted transcript, timestamped transcript, selected lecture metadata, and a sanitized source reference.

Guardrail: no cookies, authorization headers, full HTML snapshots, or unrelated page text cross the browser-to-uploader boundary.

### 13. Private-first deployment instead of assuming a public archive

Considered: make the repository public by default and treat every captured transcript as immediately public.

Rejected as a deployment assumption. Sharing is permitted, but public visibility is still a separate choice and can change the consequences of leaking a session URL or unrelated metadata.

Selected: the repository may begin private. The job and output format must remain safe if the repository is later made public.

Guardrail: sanitize metadata and logs even when the repository is private; omit a source URL from the published metadata block if it exposes private session details.

### 14. Write-once files instead of automatic updates

Considered: update an existing lecture file whenever a later capture produces different transcript text, or merge corrections automatically.

Rejected because automatic replacement could destroy a deliberate manual edit and because Phase 1 does not define a trustworthy correction workflow.

Selected: create a lecture file once. If the same path already contains the same transcript hash, mark the job complete. If it contains different content, report a permanent conflict and leave the file untouched. The remote `transcript_sha256` metadata field is the authority for this comparison; a body-only manual edit that leaves the field unchanged is intentionally classified as unchanged in Phase 1.

Guardrail: the uploader must never overwrite a machine-owned lecture path automatically in Phase 1.

### 15. One combined lecture file instead of separate transcript files

Considered: write the plain transcript and timestamped transcript to separate files.

Rejected because two files create coordination and consistency problems for a reader or AI tool.

Selected: keep both clearly labeled sections in one Markdown lecture file.

Guardrail: a successful lecture upload represents one coherent artifact, not two independently managed files.

### 16. Direct commits instead of a pull-request workflow

Considered: create a branch and pull request for every transcript, or require review before each file reaches `main`.

Rejected for the personal first version because it adds friction to a single-writer workflow and conflicts with the goal of automatic capture after activation.

Selected: write directly to the configured `main` branch, with serial processing, deterministic paths, and write-once conflict handling.

Guardrail: direct writes apply only to the configured machine-owned lecture paths; the uploader must not rewrite course README files or arbitrary repository content.

### 17. GitHub API file writes instead of cloning and pushing the repository

Considered: maintain a local Git clone, create commits locally, and push them to GitHub.

Rejected for Phase 1 because a clone adds local repository state, synchronization, and another failure surface for a workflow that creates one file at a time.

Selected: use targeted GitHub Contents API file operations through the API and process jobs serially. The uploader performs a preflight GET, uses a no-sha PUT only when the path is absent, accepts only a created response, and re-GETs after any unexpected create response. It never sends an update request.

Guardrail: the uploader must check the target path before creating a file and treat remote conflicts as meaningful states, not as permission to overwrite. Phase 1 does not assume an If-None-Match-style conditional-create guarantee.

### 18. No automatic historical crawl in Phase 1

Considered: crawl every historical lecture, scan the course page, or reconstruct an archive without the user opening each transcript.

Rejected because it expands the access, identity, rate-limit, and completeness problems substantially. It also changes the user-controlled capture model.

Selected: capture the current transcript when the user activates it. Historical backfill, if ever needed, is a separate feature with separate controls.

Guardrail: no crawler, sitemap walk, or broad page scan should be added as an “optimization” to the first version.

### 19. Alarm-driven wakeup instead of user-triggered retry

Considered: keep the Native Messaging port alive forever, wait for a popup/page event to retry, or install a separate background daemon.

Rejected the first option because the Manifest V3 service worker and native host can both be suspended or disconnected. Rejected the daemon for Phase 1 because Chrome-closed processing is explicitly out of scope and a second long-lived service expands the installation and security surface.

Selected: request the Chrome `alarms` permission and create a one-minute `lecture-transcripts-drain` alarm. The alarm reconnects the persistent port, requests status, and lets the uploader resume due work. The status contract includes `drainState`; an alarm-owned connection stays open while work or device authorization is active and closes only after `idle` or `waiting_for_backoff`. Backoff values are exact while the host is alive; after suspension they are lower bounds and may wait for the next alarm.

Guardrail: no implementation may describe retry as indefinite while relying only on a page visit or popup. Chrome being completely closed remains deferred.

### 20. Explicit correlated messages instead of inferred acknowledgements

Considered: correlate acks by lecture key alone, return an unbounded list of jobs, or let separate agents invent status payloads for the popup.

Rejected because duplicate captures and service-worker replays can overlap, and a 500-job queue can exceed the Native Messaging frame cap if status has no page limit.

Selected: version every Native Messaging envelope, include a request ID, echo the lecture key and content hash on submit acks, distinguish `rejected_duplicate_terminal`, include an explicit `drainState`, and page fixed-size job summaries with a numeric cursor. Extension and uploader build versions are visible; protocol-version mismatch fails closed.

Guardrail: a new message field, status, or rejection action requires an edit to the canonical protocol contract in `TECHNICAL_PLAN.md` and `protocol/native-messaging.schema.json` before implementation.

### 21. Build-time selector embedding instead of runtime fixture reads

Considered: fetch selectors from an external server, copy a mutable JSON file beside the built extension, or compile selectors into the content-script bundle.

Rejected the server and mutable runtime file because a personal extension must work offline and the parser must not silently drift from the verified fixture.

Selected: the Stage 0 selector fixture is validated at build time and embedded in the production bundle. The same artifact remains available to parser tests; no runtime path or network fetch is required.

Guardrail: changing selectors requires a new verified fixture and a Stage 0/plan update, not a hand-edited production constant.

## Decisions resolved in the technical plan

The following were previously open while the design was being explored and are now settled in `TECHNICAL_PLAN.md`:

- Authentication is GitHub App Device Flow, with only Contents read/write permission and installation limited to the destination repository. The uploader stores the user credential in Keychain; the extension never sees it.
- The extension has a bounded three-job pending-handoff outbox plus bounded metadata-only overflow notices solely to survive service-worker suspension before durable acknowledgement. The uploader remains the only owner of acknowledged queue and retry state; alarm-driven reconnects wake due work after suspension.
- Completion requires the real page's completion indicator plus two identical normalized snapshots from a MutationObserver scoped to the transcript container subtree. A fixed delay or quiet DOM alone is insufficient; `not_ready` has an explicit re-toggle/reopen recovery path.
- The application payload cap is 950 KiB for a serialized job and 1 MiB for a Native Messaging frame. Oversized jobs are rejected rather than chunked.
- Conflict resolution is visible and manual: inspect or back up the remote file, optionally delete it in GitHub UI, then retry. The uploader never deletes or overwrites remote files.
- Remote creation uses preflight GET plus no-sha create; the design does not claim a transactional conditional-create operation.
- Spring and Summer are separate term values; a yearless unambiguous date may use the validated term year, while ambiguous numeric dates and literal Spring/Summer labels fail closed.
- Source URLs are canonicalized by the extension and authoritatively re-canonicalized by the uploader; shared vectors define the result, and query/fragment values never reach published metadata or logs.
- The Native Messaging contract is versioned, request-correlated, status-paginated, explicit about duplicate terminal rows, and explicit about whether the host is working, waiting, authorizing, or safe for an alarm-owned port to close. `rejected_handoff_full` remains extension-local.

## Remaining open discovery facts

- The exact Leccap selectors, completion indicator, metadata fields, timestamp format, loading region, and SPA behavior were recorded on 2026-09-20 from two representative authenticated lecture pages and the course overview; they live in `extension-tests/fixtures/lecture-page.selectors.json` and `docs/STAGE_0_REPORT.md`. The recording date is sourced from the linked overview via on-demand fetch with exact player-link correlation, per the selected `lectureDateSource` policy. Because the observed shape has no in-container loading/error region or explicit complete marker, the plan's completion contract was deliberately revised to a compound rule (open-state control title + populated rows + two identical snapshots).
- Live use on 2026-09-22 showed the linked overview answering a bare `fetch` with a reduced 200 page (correct title, no recording list) even with the session included, while the same URL renders the full server-side list as a document navigation. The recorded on-demand lookup is therefore performed with `credentials: include`, a document-like `Accept` header, and `cache: no-store`; when the response still lacks the recording list and is not a sign-in page, the runtime renders the same linked overview in a hidden same-origin iframe and runs the existing player-link correlation against that rendered DOM. The linked page and correlation rule are unchanged; only the transport gains this fallback.
- On 2026-09-22 the owner selected a two-file, term-free layout for the personal repository: the plain transcript at `<courseSlug>/lectures/<NNN>.md` and the timestamped transcript at `<courseSlug>/timestamped/<NNN>.md`. Each document carries the same metadata block (course, term, lecture, date, optional source_url, captured_at, transcript_sha256) at the bottom, and the timestamped document renders exactly one line per timestamp entry. The term remains part of the lecture identity and lectureKey but not of the repository path because the repository holds one term. The hash framing and normalization are unchanged, so previously captured content stays hash-identical; the uploader publishes the plain file first and the timestamped file second, and a retry completes a partially created pair. Legacy combined files were deleted by the owner and recaptured.
- The complete supported course/term mapping is recorded in `extension-tests/fixtures/course-mapping.json` as the current personal allowlist (EECS 484 + Fall 2026, the only observed course/term); the EECS 491 Winter 2026 example remains illustrative only. Adding a course requires a verified page observation and a new entry; unmapped labels fail closed.
- The real transcript, serialized-job, Native Messaging, and status-page measurements are recorded in `extension-tests/fixtures/transcript-size-report.json` and sit far below the byte caps. The per-sample activation-to-second-stable-snapshot render time remains a live measurement (recipe in `docs/STAGE_0_REPORT.md`); both live observations showed the populated transcript appearing immediately after activation. The documented byte caps and 30-second observation budget are safety limits; if a supported sample does not fit, implementation is blocked and the plan must be deliberately revised.
- The real GitHub App client ID, numeric RepositoryID, installation, initialized-main-branch state, target-repository sanity check, and branch are machine-local provisioning inputs. The Chrome extension ID is a separate Stage 7 install input obtained after the extension is loaded. Both must be verified, not guessed or committed.
- The repository is no longer design-only: the package manifests, Go module, queue DDL, canonical schema files, normalization-vector file, serial processor, `lecture-uploader` executable, packaging scripts, and tests are in the tree. The remaining live items are the Stage 0 render-time measurement and the machine-local GitHub provisioning/loaded extension ID, both owner-side and never committed.
- Whether uploads must continue while Chrome is completely closed is not a Phase 1 requirement. If it becomes one, a separately running local background agent may be needed.

These are discovery facts, not permission to silently choose a rejected alternative or contradict the technical plan.

## Stage 0 gate

The Stage 0 packet's code-ready portion passed on 2026-09-20 and implementation proceeded against the recorded contracts; the per-sample render-time measurement and the machine-local provisioning packet remain outstanding and gate Stage 8 end-to-end verification. A later agent must still not fill missing selectors with guessed CSS, treat the example course as the complete course set, hardcode fake account identifiers, or proceed with unmeasured limits.
