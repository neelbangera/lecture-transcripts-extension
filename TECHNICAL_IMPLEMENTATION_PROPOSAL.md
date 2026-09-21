# Automatic Leccap Transcript Uploader

> Superseded historical proposal. Do not use this document to make implementation decisions. Follow TECHNICAL_PLAN.md; use PHASE_1_DECISIONS.md only for the rationale behind its guardrails. This file is retained as historical context and contains earlier alternatives for authentication, handoff durability, hashing, and GitHub writes that are no longer authoritative.

## Status and assumptions

This repository is currently an empty Git repository with no application files or commits. This proposal therefore describes a greenfield implementation of the system we discussed.

The goal is a personal Chrome extension for one Mac and one authenticated browser session. The user opens a lecture transcript on `https://leccap.engin.umich.edu/` by clicking the transcript control; that action activates capture for the current lecture. After that activation, the extension should extract and upload the transcript without requiring a separate upload click.

Sharing the recordings and transcripts is expressly permitted. The GitHub repository may be private initially, and making it public is a separate deployment choice. The implementation should still avoid collecting browser cookies, page HTML, or unrelated Leccap data.

For the first version, course, term, lecture number, and date are available in the page metadata. The personal course set does not include guest lectures or lectures split across multiple recordings, so those cases are out of scope unless that assumption changes.

## Proposed system

```text
┌──────────────────────────────┐
│ Leccap lecture page          │
│ authenticated browser session│
└──────────────┬───────────────┘
               │ DOM/transcript text only
               ▼
┌──────────────────────────────┐
│ Chrome extension              │
│                              │
│ Content script:               │
│ - recognizes lecture pages   │
│ - waits for transcript load  │
│ - extracts metadata/text     │
│                              │
│ Service worker:               │
│ - normalizes and hashes data │
│ - hands jobs to uploader     │
│ - reports status             │
└──────────────┬───────────────┘
               │ validated TranscriptJob
               ▼
┌──────────────────────────────┐
│ Local uploader on the Mac     │
│ - durable retry queue         │
│ - GitHub credential storage   │
│ - GitHub API client           │
└──────────────┬───────────────┘
               │ authenticated GitHub request
               ▼
┌──────────────────────────────┐
│ GitHub                       │
│ neelbangera/lecture-transcripts│
│ main branch                  │
└──────────────────────────────┘
```

The extension should not contain a GitHub access token, refresh token, or GitHub App private key. Initial authorization should happen through GitHub's web authorization page. The local uploader is the security boundary for the resulting credential and can keep it in macOS Keychain. A web authorization flow does not eliminate secret storage; it keeps the secret out of the extension and limits it to the personal uploader. Multi-user distribution and a hosted backend are not goals for the first version.

## Extension behavior

The extension should run only on the Leccap host and should use the narrowest possible permissions. A content script will inspect pages under `leccap.engin.umich.edu`, but it should upload only when it can confidently classify the page as a lecture page containing a transcript.

The extension should not upload merely because the user visited a Leccap page. The user clicking or expanding the transcript is the explicit capture trigger; it is an authorization to process that transcript, not by itself proof that the transcript is complete. The automatic processing after that trigger should be:

1. The URL and page structure identify a lecture or recording page.
2. A transcript container is present and the transcript has finished loading.
3. The transcript has remained unchanged for a short debounce period and passes basic sanity checks, such as having a reasonable amount of text and not consisting only of navigation or loading text.
4. Course, lecture number, and date can be resolved using the page metadata or a configured course mapping.

The content script should use a `MutationObserver` or equivalent DOM observation because the transcript may be inserted after the initial page load. It should debounce changes rather than attempting an upload for every DOM mutation.

If any required field is ambiguous, the extension should fail closed: it should record a diagnostic status and not guess a repository path. This prevents a transcript from one course being uploaded as another course's lecture.

The service worker should maintain capture state outside of in-memory variables because Manifest V3 service workers can be suspended. The durable transcript queue should belong to the local uploader, not be duplicated in extension storage. The extension should hand off a job and wait for the uploader to acknowledge that it has durably persisted the job before treating the handoff as complete. Lightweight status and the last known content hash can remain in extension storage.

## Transcript job format

The browser-to-uploader boundary should use a small, explicit data structure rather than sending arbitrary page content:

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

`lectureKey` is a deterministic identity derived from the configured course, term, and lecture number. It is used for delivery deduplication and for the repository path. `contentHash` identifies the normalized transcript content. The system should use at-least-once delivery between the extension and uploader, with the uploader making retries safe through `lectureKey` and `contentHash`.

`sourceUrl` should be canonicalized before storage. Query parameters that could contain session identifiers or temporary access tokens must be removed. The job should contain transcript text and selected metadata only; it should not contain cookies, authorization headers, or a full HTML snapshot.

The content hash should be calculated after normalization. Normalization should make harmless formatting differences—such as repeated whitespace or line-ending changes—irrelevant, while preserving meaningful speaker text and timestamps.

Conceptually, the automatic processing loop is:

```text
when the user activates the transcript:
    metadata = parse_course_lecture_date(page)
    transcript = extract_transcript(page)

    if metadata is incomplete or transcript is not credible:
        stop and report "not ready" or "unrecognized page"

    job = normalize_and_hash(metadata, transcript)
    job.lectureKey = derive_lecture_key(metadata)

    send job to local uploader

    if uploader confirms durable persistence:
        report "queued"
    otherwise:
        report "handoff failed" and allow a later retry
```

## Local uploader

The local uploader exists so the extension does not need to hold GitHub credentials and so the transcript queue survives service-worker, browser, and uploader restarts. It is the sole owner of durable jobs and retry state.

For the final Chrome implementation, Chrome Native Messaging is the cleanest extension-to-local-process boundary. It allows the extension to start the uploader on demand and avoids exposing a general-purpose HTTP server. The native host can persist a received job before acknowledging it and resume queued work the next time it connects. This design does not promise uploads while Chrome is completely closed; that would require a separately running background agent. During early development, a loopback-only HTTP endpoint could be used temporarily, but it would need an unguessable bearer token, strict schema validation, a size limit, and binding to `127.0.0.1` only.

The uploader should provide these responsibilities:

- Validate the `TranscriptJob` schema and reject unexpected fields.
- Persist each job before acknowledging receipt and maintain a durable queue so a temporary network or GitHub failure does not lose a transcript.
- Complete the GitHub web authorization flow and store the resulting access or refresh credential in macOS Keychain rather than in extension storage or a plaintext configuration file.
- Upload jobs serially, or otherwise handle GitHub conflicts safely.
- Return a small status result to the extension without logging transcript contents.
- Remove or archive a queued job only after GitHub confirms success.

The extension popup can show the last successful upload, pending jobs, and the most recent error. Automatic operation should not depend on the popup being open.

## GitHub authentication and upload

The intended repository is fixed for the initial personal version:

```text
owner:  neelbangera
repo:   lecture-transcripts
branch: main
```

Authentication should start from a user-visible connection action and open GitHub's web authorization page. The local uploader should own the authorization callback and credential exchange; the extension should receive only success, failure, and connection-status information. The local uploader should store whatever credential is needed in macOS Keychain. No long-lived GitHub credential should be placed in the extension package or extension storage.

The first version is intentionally personal and repository-specific. It does not need a multi-user OAuth backend, a distributable authorization experience, or support for multiple GitHub accounts. The authorization choice should remain limited to the repository and contents-writing capability required by this uploader.

The uploader should use a create-only, idempotent write process:

```text
for each queued job:
    path = derive_stable_path(job)
    existing = get_file(path)

    if existing contains the same transcript hash:
        mark job successful without creating a commit
    else if existing does not exist:
        create file with a descriptive commit message
    else:
        mark a permanent write-once conflict and do not overwrite the file
```

This prevents revisiting the same lecture from creating duplicate commits while preserving the write-once rule. A path collision with different content requires manual intervention; automatic transcript correction and replacement are out of scope for the first version.

## Repository layout

The repository should use stable, predictable paths that distinguish repeated offerings of the same course:

```text
README.md
courses/
  eecs491/
    README.md
    2026-winter/
      lectures/
        001.md
        002.md
        006.md
```

Each lecture file should be the main AI-readable artifact and should contain metadata plus two clearly labeled sections:

```text
---
course: EECS 491
term: Winter 2026
lecture: 6
date: 2026-02-12
source_url: https://leccap.engin.umich.edu/...
captured_at: 2026-02-12T...
transcript_sha256: ...
---

## Transcript

...

## Timestamped transcript

...
```

Keeping the plain and timestamped versions in one lecture file makes the repository easier for an AI tool to consume and avoids coordinating two separate uploads. Course README files can initially be maintained manually. If they later need to be updated automatically, the uploader should either create one atomic Git commit using Git's lower-level tree/commit API or update them in a controlled serial operation.

Generated lecture files are machine-owned and immutable from the uploader's perspective. The uploader may recognize an existing file with the same transcript hash as already complete, but it must not replace an existing file with different content.

## Reliability and safety requirements

The first implementation should include the following safeguards:

- Automatic retries with backoff for network failures.
- A persistent queue for jobs captured while GitHub is unavailable.
- At-least-once handoff with durable persistence before acknowledgment.
- A maximum transcript size and validation of all text fields.
- No upload when course or lecture identification is uncertain.
- No overwrite when a write-once lecture path already contains different content.
- No credentials or transcript bodies in extension console logs or uploader logs.
- A disconnect/reset action that removes stored GitHub credentials.
- A local status page or popup showing whether a lecture was uploaded, skipped as unchanged, queued, or rejected.
- Parser fixtures captured from representative Leccap pages so DOM changes can be detected by tests.

If the repository is public, the extension should avoid putting temporary session URLs or unrelated page text into Markdown metadata. The uploader should publish only the intended transcript and sanitized lecture metadata. The source URL may be omitted from the published frontmatter if it would expose private session details.

## Implementation sequence

### Phase 1: Leccap discovery and parser

Inspect one or two representative authenticated lecture pages and identify the actual transcript control, transcript container, course label, lecture number, date, and timestamp format. Build a parser around those observed structures rather than assuming the URL alone contains all metadata. Confirm that the transcript-click action provides a reliable activation point.

### Phase 2: Automatic extension capture

Create the Manifest V3 extension, content script, transcript-click activation, page-stability detection, normalization, hashing, and uploader handoff. At this stage the extension can display a local status without uploading.

### Phase 3: Local uploader and GitHub write

Add the Native Messaging host, durable queue, web-based GitHub authorization, Keychain credential storage, GitHub file comparison, and create-only behavior. Complete a vertical slice for one course and one lecture path.

### Phase 4: Authentication polish and recovery

Add reconnection behavior, retry handling, permanent-conflict reporting, credential reset, and a small extension status UI.

### Phase 5: Broader course support

Add configurable course mappings and support additional Leccap page variations only if the personal use case requires them. Each new page shape should have a parser fixture and an explicit repository-path rule.

## Definition of success

After one-time installation and GitHub web authorization, opening a completed lecture page should have no effect until the user opens the transcript. After that one activation, the following should happen without another upload click:

```text
lecture page opened
  → user clicks the transcript control
  → transcript becomes stable
  → one job is queued
  → one correctly named Markdown file is created
  → revisiting the page creates no new commit when unchanged
```

The system should remain quiet and make no repository change when the page is incomplete, not a lecture page, or cannot be classified confidently. If the target path already contains different content, it should report a permanent conflict and leave the existing file untouched.

## Non-goals for the first version

- Crawling every historical lecture automatically.
- Bypassing Leccap authentication or access controls.
- Uploading arbitrary pages or raw HTML.
- Supporting multiple GitHub repositories or users.
- Building a hosted multi-user service.
- Updating or replacing an existing lecture file automatically.
- Automatically rewriting course README files on every upload.

## Useful implementation references

- [GitHub Contents API](https://docs.github.com/en/rest/repos/contents?apiVersion=2022-11-28)
