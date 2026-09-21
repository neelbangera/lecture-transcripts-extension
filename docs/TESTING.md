# Testing

The repository has three test layers: browser-side unit tests, Go uploader unit
tests, and packaging self-tests. All three run offline against fixtures, fakes,
and temporary directories; none requires a GitHub account, a network call, a
real Keychain record, or an authenticated Leccap page.

## What to run

From the repository root:

```sh
npm ci
npm run typecheck
npm test
npm run build
```

From `uploader/`:

```sh
go test ./...
```

Packaging self-tests, from the repository root:

```sh
sh scripts/tests/run.sh
```

Shell syntax check for every script:

```sh
sh -n scripts/build-uploader.sh
sh -n scripts/install-native-host.sh
sh -n scripts/uninstall-native-host.sh
sh -n scripts/tests/run.sh
```

Current baseline: `npm test` runs 7 files / 71 tests, `go test ./...` passes in
all 9 uploader packages, `npm run typecheck` is clean, `npm run build` writes
`dist/extension/`, and `sh scripts/tests/run.sh` reports 59 passed / 0 failed.

## Browser-side unit tests

`npm test` runs Vitest over `extension-tests/`:

| Suite | Covers |
| --- | --- |
| `transcript-normalizer.test.ts` | NFC, line-ending, horizontal-whitespace, and blank-line normalization; timestamp digit preservation and separator normalization; idempotence; framed-hash relationships. |
| `leccap-parser.test.ts` | Parser output against the Stage 0 fixture packet; activation and already-expanded extraction forms; unmapped course/term rejection; no-number and badge rejection; linked-overview date correlation (zero/multiple matches fail closed); unsafe URL rejection; not-ready and loading-only pages. |
| `transcript-job.test.ts` | Identity/path derivation from the committed course mapping; bounded metadata and transcript sizes; canonical serialization byte-for-byte against the Go fixture; hash recomputation. |
| `content.test.ts` | No submission on ordinary page visit; click activation; already-expanded activation; URL-change activation only when open and populated; stability-window reset on mutation; observation timeout; parser-rejection mapping; handoff-pending behavior when the background channel is missing or rejects; rejection of non-durable background responses. |
| `native-messaging.test.ts` | Request/response envelope validation, unknown-field rejection, persistent-port reuse, and concurrent response correlation. |
| `protocol-vectors.test.ts` | Normalization and source-URL vectors, cross-language canonical job fixture, and exact framed-hash framing. |
| `build-smoke.test.ts` | The built extension contains every manifest-referenced file with the Stage 0 selectors inlined and no unresolved placeholders. |

Coverage boundary: the bounded handoff outbox and background replay/acknowledgement
paths (`extension/src/extension-storage.ts`, `extension/src/background.ts`) are
implemented but do not yet have dedicated committed suites; `extension-tests/IMPLEMENTATION.md`
lists those suites as remaining work. Until they exist, treat outbox and replay
behavior as covered only by the owner's real-machine checks below.

## Go uploader unit tests

`go test ./...` from `uploader/` exercises every package:

| Package | Covers |
| --- | --- |
| `internal/protocol` | Closed schema vocabulary, request/response round trips, canonical job serialization and hash framing, byte boundaries, source-URL vectors, schema/Go parity. |
| `internal/host` | Little-endian framing, truncated and oversized frames, one-MiB cap in both directions, exact origin validation, connect lifecycle, serialized request handling, safe error responses. |
| `internal/config` | Load and validation rules, exact limit constants, path derivation, nonfunctional example file, rejection of missing/unknown/placeholder values. |
| `internal/queue` | Schema columns/indexes, enqueue-before-ack, `(lectureKey, contentHash)` deduplication, terminal duplicate actions, queue-full by count and bytes, serial claiming, future-retry skipping, stale-lease recovery, transition legality, pruning under pressure, pagination, counts, exclusive lock. |
| `internal/logging` | Restricted log directory/file modes, three-file rotation at the cap, allowlisted structured fields, sensitive-value redaction, source sanitization. |
| `internal/retry` | Schedule contract `5s,30s,2m,10m,1h`, ±20% jitter bounds, capped index after the last attempt, reset-to-zero behavior, statelessness across restarts. |
| `internal/auth` | Device transaction persistence/resume/expiry, pending and `slow_down` polling, terminal errors, refresh near expiry, forced refresh after 401, credential-left-untouched on failed refresh, both-records reset, repository verifier checks, unsupported-platform failure. |
| `internal/github` | Create-only PUT without `sha`, unchanged revisit, conflict on different hash, malformed file, directory, and symlink, race resolution to unchanged/conflict/classified error, non-created response rejection, HTTP classification, strict frontmatter hash parsing. |
| `internal/markdown` | Deterministic golden bytes, frontmatter sanitization, unsafe source-URL omission, missing-timestamped-source placeholder. |

All of these use fake HTTP clients, fake credential stores, test clocks, and
temporary directories. They never touch the real Keychain, the real GitHub API,
or the real queue database.

## Fixture packet

The committed, sanitized Stage 0 packet under `extension-tests/fixtures/` is
the parser's source of truth:

| Fixture | Purpose |
| --- | --- |
| `lecture-page.html` + `lecture-page.selectors.json` + `lecture-page.expected.json` | The supported lecture-page shape, observed selectors, completion policy, and expected normalized result. |
| `overview-page.html` | Linked-overview shape used for exact player-link/date correlation. |
| `no-number-lecture-page.html` + `.expected.json` | Fail-closed identity case: a title without a numeric lecture prefix is `rejected_ambiguous_metadata`; the category badge is never a lecture number. |
| `loading-transcript-page.html`, `non-lecture-page.html` | Negative cases for not-ready and unrelated pages. |
| `course-mapping.json` | The complete supported course/term allowlist (currently `eecs484` / `2026-fall`); unmapped labels fail closed. |
| `transcript-size-report.json` | Measured byte maxima against the approved caps; `renderTimeMs` is still a live owner measurement. |

Fixtures are sanitized: no user identifiers, cookies, media URLs, session
values, or real route IDs. Raw authenticated captures are gitignored and must
never be committed, pasted into prompts, or used as runtime input.

## Offline, restart, duplicate, conflict, and retry checks

These behaviors are already covered without a network or a real machine:

- **Offline handoff:** `content.test.ts` keeps the pending handoff when the
  background channel is missing or rejects, and rejects non-durable responses.
- **Restart:** `internal/queue` proves a job is persisted before acknowledgement,
  survives reopening the database, recovers stale leases, and skips retries
  whose `next_attempt_at` is still in the future; `internal/retry` proves the
  schedule is stateless so a restart cannot lose its position; `internal/auth`
  proves an unexpired device transaction and a stored credential restore after
  restart.
- **Duplicate:** `internal/queue` proves the `(lectureKey, contentHash)` pair is
  deduplicated and that terminal duplicates return the documented action;
  `internal/github` proves a same-hash remote file is `unchanged` and creates no
  new commit.
- **Conflict:** `internal/github` proves different-hash, malformed, directory,
  and symlink targets are `permanent_conflict` and are never overwritten, and
  that a create race resolves to unchanged, conflict, or a classified error.
- **Retry:** `internal/retry` and `internal/queue` cover the capped jittered
  schedule, promotion of due rows to `queued`, and lease recovery.

## Fake-versus-real integration boundary

Everything runnable from a clean checkout is fake-boundary: fake HTTP, fake
credential stores, fake handoff functions, committed fixtures, and temporary
`HOME` directories. There is no test that calls GitHub, Leccap, or the macOS
Keychain.

The real integration boundary begins at the machine-local setup described in
[SETUP.md](SETUP.md): the machine-local config, the real GitHub App, the loaded
extension ID, the Keychain prompt, and the actual permitted Leccap page. Those
inputs are intentionally not in the repository, so they cannot be part of the
offline suites. The `uploader/cmd/lecture-uploader` executable and
`uploader/internal/processor` connect the tested packages into a running host;
their behavior is covered with fakes, while the real host session requires the
machine-local setup above.

## Packaging self-tests

`sh scripts/tests/run.sh` runs 59 checks with a temporary `HOME` and a stubbed
`security` command, so it never touches the real home directory or Keychain. It
covers:

- template placeholders and the absence of a real extension ID;
- installer argument validation (missing, malformed, wildcard, uppercase,
  non-`a`-`p`, wrong-length IDs, relative or missing binary paths);
- rendered manifest path, `0600` mode, exact single origin, absolute binary
  path, no remaining placeholders, valid JSON, and no duplicate registrations;
- idempotent reinstall and ID-change replacement;
- uninstall gating: the default removes only the manifest and binary, keeps the
  queue database, config, and lock, and never calls `security`;
- `--reset` announcement order and removal of the queue database, WAL, lock,
  config, manifest, and binary, plus Keychain deletion;
- `scripts/build-uploader.sh` argument validation and `--dry-run` output.

## Checks that require the owner's real machine

These cannot be verified by the committed suites and must be performed by hand
on the real Mac:

1. **Render-time measurement.** Run `docs/render-time-snippet.js` on both
   permitted samples, click **Show Transcript**, and record the printed
   milliseconds in `extension-tests/fixtures/transcript-size-report.json`
   (replacing both `null` values and recomputing `observedMaxima`). A supported
   sample reaching 30,000 ms or timing out fails Stage 0.
2. **Unpacked load and ID capture.** Load `dist/extension/` unpacked in Chrome,
   confirm it renders, and copy the loaded extension ID.
3. **Host registration.** Run `scripts/install-native-host.sh <loaded-extension-id>`,
   confirm the rendered manifest mode and origin, then quit and reopen Chrome.
4. **Keychain behavior.** Authorize through the popup, choose **Always Allow**
   at the macOS prompt, and confirm the two records exist under service
   `com.neelbangera.lecturetranscripts`.
5. **Real capture.** Open a permitted Leccap lecture, activate the transcript,
   and confirm exactly one Markdown file appears at the derived path and that a
   revisit creates no new commit.
6. **File-mode check on real files.** Confirm the data directory is `0700` and
   the queue database, sidecars, lock, logs, and host manifest are `0600`.
7. **Alarm-delayed retry.** With Chrome open and the uploader temporarily
   unable to reach GitHub, confirm a `retryable_error` row returns to `queued`
   after its backoff and after the one-minute drain alarm reconnects.
