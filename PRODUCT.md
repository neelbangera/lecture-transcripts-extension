# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary user: a UMich student watching Leccap lecture recordings during a semester, who opens a lecture's transcript and wants it saved for study and review without manual file handling.

Eventual audience: classmates in the same UMich/Leccap setting who must eventually be able to set this up and use it without hand-holding — which means setup must eventually avoid terminal/developer steps (today's command-line runbook is owner-side interim).

## Product Purpose

Capture explicitly opened Leccap lecture transcripts and publish them as write-once Markdown lecture files in a configured GitHub repository. Success is co-equal on two axes: hands-off zero-loss capture (every activated transcript lands in GitHub exactly once, even offline or after failures, with no manual file handling) and study-ready output quality (timestamps, titles, and structure good enough to revise from).

## Positioning

A capture pipeline whose browser side never holds a GitHub credential: the extension sends only transcript text and validated lecture metadata to a headless local uploader that keeps credentials in the macOS Keychain, owns a durable retry queue with at-least-once delivery deduplicated by deterministic `lectureKey` plus framed `contentHash`, and publishes write-once — an identical hash is unchanged, while different or malformed existing content is a conflict that is never overwritten automatically. Uncertain identity fails closed rather than capturing wrong.

## Operating Context

- Leccap lecture pages at `leccap.engin.umich.edu`, opened in Chrome desktop on macOS; transcripts are activated via the page's Show Transcript control (or captured automatically when the setting is on).
- A GitHub account with one configured destination repository and GitHub App device-flow authorization; macOS Keychain prompts when the local uploader first stores credentials.
- Course/term allowlist per semester in normalized form (`YYYY-fall`, `YYYY-winter`, …), with an optional preferred three-digit discussion section; observed set is EECS 484 and EECS 491, 2026-fall.
- Semester rituals: adding each new course/term to the allowlist before capture; reopening transcripts that were not durably accepted ("overflow") when capacity frees up.

## Capabilities and Constraints

Capabilities:

- Capture a recognized lecture page on load or in-page URL change; manual mode captures only when the transcript is explicitly opened. Desktop notifications on upload finish/fail.
- Popup surfaces: GitHub connection/device-flow authorization, pending handoffs, overflow/recapture list, last outcome, reset/refresh. Settings page: automatic capture toggle, notifications toggle, course allowlist management.
- Identity derivation for unnumbered lecture titles (overview-sequence derivation with later correction when a title reveals the real slot); ambiguous metadata is rejected.
- Local uploader (`lecture-uploader` Native Messaging host, Go): machine-local config, durable SQLite queue, sanitized rotating logs, retry backoff, Keychain storage, GitHub Contents write-once publishing, Markdown rendering, serial processor.

Constraints and limits (durable):

- The extension holds no GitHub token, refresh token, or private key — ever.
- Jobs are delivered at least once and deduplicated by deterministic `lectureKey` plus framed `contentHash`; remote lecture files are write-once and never automatically overwritten on conflict.
- Unmapped course/term labels and ambiguous lecture identity fail closed.
- Byte caps: transcripts ≤ 460,800 B, serialized job ≤ 972,800 B, Native Messaging payload ≤ 1,048,576 B.
- No credentials, repository IDs, tokens, extension IDs, or raw Leccap captures belong in the repository.
- Leccap is the only capture source; host permissions are limited to it.

Open decisions (explicitly undecided):

- How multi-machine / multi-user expansion is delivered (architecture and UX must not paint into a single-owner corner, per confirmed direction).
- How the no-terminal setup for classmates is packaged (what replaces today's build/install runbook).

## Brand Commitments

- Product name: "Lecture Transcripts" (extension manifest and all UI copy).
- Existing icon: `extension/icons/icon128.png`.
- No voice, personality, or identity system has been confirmed beyond the plain, factual, runbook-style copy that exists today.

## Evidence on Hand

- Sanitized fixture packet at `extension-tests/fixtures/`: page-faithful lecture-page and overview HTML captures, observed selectors, expected parser results (normative `transcript-hash-v1`), `course-mapping.json`, measured size report, and a negative no-number identity fixture. Committed fixtures are sanitized ([REDACTED] names, placeholder route IDs, no media URLs).
- Size/behavior evidence in `docs/STAGE_0_REPORT.md` and `docs/STAGE_0_LECCAP_OBSERVATIONS.md`.
- Full operational docs: `docs/SETUP.md`, `docs/SECURITY.md`, `docs/TESTING.md`, `docs/TROUBLESHOOTING.md`; normative behavior in `TECHNICAL_PLAN.md`; rationale in `PHASE_1_DECISIONS.md`.
- Sensitive raw captures exist only locally and are gitignored (`open.html`, `closed.html`, `EECS 484 - Fall 2026.mhtml`): authenticated-page markers, route IDs, media URLs. Never commit or paste them.
- Absences future work must not fabricate: no testimonials, customers, press, or benchmarks beyond the measured byte caps; the per-sample render-time measurement is still outstanding.

## Product Principles

1. Zero-loss, hands-off capture: an activated transcript must land in GitHub exactly once; durability (queue, retry, dedupe) outranks speed or elegance.
2. Write-once trust: never overwrite a published lecture file; surface conflicts to the human instead of resolving them silently.
3. The browser is untrusted with credentials: the security boundary is the Keychain-backed local uploader, and future features must not move tokens into the extension.
4. Fail closed on uncertainty: unmapped courses, ambiguous identity, and malformed content refuse the capture rather than guessing.
5. Leave room to grow: eventual classmates (no terminal, no hand-holding) and multi-machine/multi-user are real destinations — avoid single-owner assumptions in UX and architecture.

## Accessibility & Inclusion

No product-specific standard established. Confirmed floor: basic keyboard and screen-reader support already present in the extension UI (labelled controls, `aria-live` status regions) is the baseline future work must not regress.
