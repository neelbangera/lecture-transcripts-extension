# Lecture Transcripts Extension Architecture

## Purpose

This is the system-wide architecture index for the personal Leccap transcript
capture pipeline: a Chrome Manifest V3 extension, a macOS Native Messaging
uploader, the shared protocol contracts, and the packaging scripts. It explains
how the pieces connect end to end and links each directory's own architecture
document.

It is a navigational and conceptual overview, not a second specification.
[`TECHNICAL_PLAN.md`](TECHNICAL_PLAN.md) remains the normative source of truth
for behavior, limits, statuses, and schemas; where this file and the plan
disagree, the plan wins.

## Boundaries and dependencies

The runtime chain is:

```text
Leccap lecture/discussion page (authenticated)
  -> extension/ content script (content.js) captures the activated transcript
  -> extension/ service worker (background.js) validates and stores a handoff
  -> Chrome Native Messaging, protocolVersion 1
  -> native-host/ manifest launches dist/native/lecture-uploader
  -> uploader/internal/{config,queue,auth,github,markdown,retry,processor,logging}
  -> GitHub Contents API, branch main
  -> neelbangera/lecture-transcripts remote Markdown files
```

Component boundaries:

| Component | Owns | Never does |
| --- | --- | --- |
| `extension/` | Page classification, activation, stable-snapshot capture, normalization, hashing, job construction, bounded handoff outbox, popup/options UI | No GitHub token, refresh token, or private key; no GitHub host permission; no crawling; no remote state |
| `protocol/` | Machine-readable wire contracts and shared test vectors | No runtime code reads the JSON files; validators are hand-written in each language and mirrored by tests |
| `uploader/` | Durable queue, retry state, GitHub App device flow, Keychain storage, Contents publishing, sanitized logs | Never stores transcript text in logs or status, never overwrites a remote file, never runs as a general web server |
| `native-host/` + `scripts/` | Install/build/packaging only; the rendered host manifest and `dist/` outputs | No runtime capture logic |
| `docs/` | Owner-facing explanation of the installed system | Cannot redefine a contract; `TECHNICAL_PLAN.md` and the decision log outrank it |

External contracts the plan pins: Chrome Native Messaging, GitHub App user
authorization with device flow, and the GitHub repository contents API. The
toolchain baseline is Node.js >=22 with npm/esbuild/Vitest, Go 1.24.x with
`modernc.org/sqlite` and `golang.org/x/text/unicode/norm`, and macOS with the
Xcode Command Line Tools because the Keychain adapter uses cgo.

## Contracts and invariants

- **Authority order.** `TECHNICAL_PLAN.md` > `PHASE_1_DECISIONS.md` (rationale
  that must agree with the plan) > the guides under `docs/`.
  `TECHNICAL_IMPLEMENTATION_PROPOSAL.md` is historical and not an authority.
- **Activation.** A recognized lecture page loads or changes its in-page URL:
  the content script opens the transcript control itself when closed, captures
  an already-open populated transcript, and closes the control again after a
  terminal outcome when it opened it. Page navigation alone never creates a
  job. The `autoCapture` option (default on) gates only the automatic paths; a
  manual **Show Transcript** click still captures.
- **Completion.** Open-state control (`title="Hide Transcript"`) + at least one
  populated row + two identical normalized snapshots separated by the 1500 ms
  no-mutation debounce, inside a 30 s observation budget; otherwise
  `not_ready`, recoverable by a later reopen.
- **Wire contract.** `protocolVersion=1` on every envelope; no transcript
  anywhere except `submit_job.job`; unknown fields fail closed. Byte caps:
  460800 per transcript field, 972800 for the canonical serialized job,
  1,048,576 per Native Messaging frame, 2048 for the canonical `sourceUrl`.
- **Identity and paths.** `lectureKey = <courseSlug>/<term>/<NNN>`; `kind` is
  `lecture` or `discussion`. Remote paths are
  `<courseSlug>/<NNN>.md` + `<courseSlug>/timestamped/<NNN>.md` for lectures
  and `<courseSlug>/discussions/<NNN>.md` +
  `<courseSlug>/discussions/timestamped/<NNN>.md` for discussions. A job with
  no timestamped form writes only the plain file.
- **Hash.** `contentHash` is lowercase hex SHA-256 over the framed
  `transcript-hash-v1\0` UTF-8 sequence (byte length + `:` + plain, then byte
  length + `:` + timestamped). The published `transcript_sha256` metadata field
  must equal it and is the write-once comparison authority.
- **Delivery.** At-least-once with deduplication by `(kind, lectureKey,
  contentHash)`. The extension keeps at most 3 full pending handoffs and 20
  metadata-only overflow notices; the uploader queue holds at most 500 jobs or
  100 MiB of job JSON and never drops an older row to accept a new one.
- **Write-once.** Preflight `GET`, then a create-only `PUT` without `sha` only
  when the path is absent. Same hash is `unchanged`; different, malformed,
  directory, symlink, or submodule content is `permanent_conflict` and is never
  overwritten. A body-only manual edit that leaves `transcript_sha256` intact
  is intentionally classified as unchanged.
- **Authorization.** GitHub App device flow owned by the uploader; the user
  token and the in-flight device transaction live in macOS Keychain under
  service `com.neelbangera.lecturetranscripts`. The extension sees only auth
  states, the `user_code`, and GitHub verification URIs.
- **Secrets and captures.** No credential, numeric repository ID, real
  extension ID, raw authenticated Leccap capture, rendered host manifest, queue
  database, or build output belongs in version control. `.gitignore` enforces
  the main cases; use `<loaded-extension-id>`-style placeholders in prose.

### Machine-local artifacts

These are created outside the repository and are never committed:

| Artifact | Path | Mode / notes |
| --- | --- | --- |
| Machine-local config | `~/Library/Application Support/LectureTranscripts/config.json` | Owner-created; exact fields `schemaVersion`, `githubAppClientId`, `repositoryId`, `owner`, `repo`, `branch`, optional `writeTimestamped` |
| Durable queue | `~/Library/Application Support/LectureTranscripts/queue.sqlite3` (+ `-wal`, `-shm`) | `0600`; directory `0700` |
| Single-instance lock | `~/Library/Application Support/LectureTranscripts/queue.lock` | `0600`; advisory `flock` held for process lifetime |
| Uploader log | `~/Library/Logs/LectureTranscripts/uploader.log` (+ `.1`, `.2`) | `0600`; 5 MiB rotation, three files |
| Rendered host manifest | `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json` | `0600`; exactly one allowed origin |
| Keychain records | Service `com.neelbangera.lecturetranscripts`; accounts `github-app-user-token`, `github-app-device-transaction` | Generic passwords; no plaintext fallback |
| Build output | `dist/extension/`, `dist/native/lecture-uploader` | Gitignored |

## Data flow

1. **Capture.** Chrome injects `content.js` into
   `https://leccap.engin.umich.edu/*` at `document_idle`. The runtime opens the
   transcript control if needed, observes the transcript container subtree,
   waits for the compound completion rule, then reads the plain and timestamped
   forms, normalizes both, computes the framed hash, and builds a
   `TranscriptJob`.
2. **Handoff.** The content script sends `{type: "capture_job", job}` to the
   service worker. `BackgroundCoordinator` validates the sender and shape,
   writes the job to the bounded `chrome.storage.local` outbox, opens the
   single persistent `runtime.connectNative` port, and sends `connect` and
   `submit_job`. The full copy is deleted only after a definitive ack
   (`queued` / `already_queued`); a non-persisting rejection records a
   metadata-only overflow notice instead.
3. **Wakeup.** The `lecture-transcripts-drain` alarm fires every minute,
   reconnects the port after service-worker suspension, requests status, and
   keeps an alarm-owned port open while `drainState` is `working` or
   `authorizing`, closing it only at `idle` or `waiting_for_backoff`.
4. **Host session.** Chrome launches the uploader binary named by the rendered
   host manifest. The executable reads 4-byte little-endian framed JSON from
   stdin and writes only framed responses to stdout; diagnostics go to the
   sanitized log. It validates the Chrome-supplied origin against the single
   allowed origin in the installed manifest and fails closed otherwise.
5. **Persistence.** Valid requests are validated, deduplicated by
   `(kind, lectureKey, contentHash)`, and committed to SQLite before the ack is
   written. Jobs drain serially with a 10-minute lease; stale leases recover on
   startup.
6. **Publishing.** For each job the processor renders the plain document first
   and the timestamped document second (unless `writeTimestamped` is false),
   performs the preflight `GET` + create-only `PUT`, and records `uploaded`,
   `unchanged`, `retryable_error` (backoff `5s, 30s, 2m, 10m, 1h` with ±20%
   jitter), `permanent_conflict`, or a terminal `rejected_*` status.
7. **Status.** The popup reads a paginated status snapshot (at most 50
   `JobSummary` rows per page), terminal outcomes raise one desktop
   notification containing only the `lectureKey` and the status label, and the
   queue stays durable across restarts.

The end-to-end state model is `idle -> activated -> waiting_for_transcript ->
ready -> handoff_pending -> queued -> uploading -> uploaded | unchanged |
retryable_error | permanent_conflict | rejected_*`.

## File responsibilities

Root-level files:

| File | Role |
| --- | --- |
| `TECHNICAL_PLAN.md` | Normative product, wire, queue, auth, and packaging contracts; the implementation authority |
| `PHASE_1_DECISIONS.md` | Rationale and rejected alternatives; must agree with the plan |
| `TECHNICAL_IMPLEMENTATION_PROPOSAL.md` | Historical superseded proposal; never an authority |
| `README.md` | Entry point: status, prerequisites, build/test commands, documentation links |
| `ARCHITECTURE.md` | This file: system overview and directory index |
| `package.json` | Node >=22 metadata, `build`/`test`/`typecheck` scripts, dev dependencies (esbuild, Vitest, jsdom, TypeScript, `@types/chrome`) |
| `package-lock.json` | Locked JavaScript dependency versions |
| `tsconfig.json` | Strict TypeScript configuration for `extension/` and `extension-tests/` |
| `vitest.config.ts` | Vitest configuration: Node environment, `extension-tests/**/*.test.ts` |
| `.gitignore` | Keeps build output, queue databases, credentials, and raw captures out of Git |
| `opencode.json` | Local agent configuration (not part of the product runtime) |

### Directory index

Each directory's architecture document is the entry point for that subtree.
The five marked `(this branch)` are the documents delivered with this change;
the other `ARCHITECTURE.md` paths belong to the parallel per-directory
documentation work and are not present in this branch at review time, so the
existing `IMPLEMENTATION.md` is listed as the current entry point.

| Directory | Role | Architecture doc | Current entry point |
| --- | --- | --- | --- |
| `extension/` | Manifest V3 shell, HTML/CSS assets, manifest, icons | [`extension/ARCHITECTURE.md`](extension/ARCHITECTURE.md) | [`extension/IMPLEMENTATION.md`](extension/IMPLEMENTATION.md) |
| `extension/src/` | TypeScript source: parser, normalizer, job builder, content runtime, service worker, Native Messaging client, popup, options, storage | [`extension/src/ARCHITECTURE.md`](extension/src/ARCHITECTURE.md) | [`extension/src/IMPLEMENTATION.md`](extension/src/IMPLEMENTATION.md) |
| `extension-tests/` | Vitest browser-side suites | [`extension-tests/ARCHITECTURE.md`](extension-tests/ARCHITECTURE.md) | [`extension-tests/IMPLEMENTATION.md`](extension-tests/IMPLEMENTATION.md) |
| `extension-tests/fixtures/` | Sanitized Stage 0 fixture packet: page HTML, selectors, expected outputs, course mapping, size report | [`extension-tests/fixtures/ARCHITECTURE.md`](extension-tests/fixtures/ARCHITECTURE.md) | [`extension-tests/fixtures/IMPLEMENTATION.md`](extension-tests/fixtures/IMPLEMENTATION.md) |
| `protocol/` | Canonical JSON schemas and cross-language test vectors | [`protocol/ARCHITECTURE.md`](protocol/ARCHITECTURE.md) (this branch) | [`protocol/IMPLEMENTATION.md`](protocol/IMPLEMENTATION.md) |
| `scripts/` | Extension build, uploader build, host install/uninstall, packaging self-tests | [`scripts/ARCHITECTURE.md`](scripts/ARCHITECTURE.md) (this branch) | — |
| `native-host/` | Native Messaging host manifest template | [`native-host/ARCHITECTURE.md`](native-host/ARCHITECTURE.md) (this branch) | — |
| `docs/` | Owner-facing setup, security, testing, troubleshooting, Stage 0 evidence | [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) (this branch) | [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md) |
| `uploader/` | Go module for the macOS uploader | [`uploader/ARCHITECTURE.md`](uploader/ARCHITECTURE.md) | [`uploader/IMPLEMENTATION.md`](uploader/IMPLEMENTATION.md) |
| `uploader/cmd/` | `lecture-uploader` executable assembly | [`uploader/cmd/ARCHITECTURE.md`](uploader/cmd/ARCHITECTURE.md) | — |
| `uploader/internal/` | Internal packages; dependency order `protocol <- host`, `config -> queue/auth/github/logging`, `queue+auth+github+markdown+retry -> processor`, `processor+host -> cmd` | [`uploader/internal/ARCHITECTURE.md`](uploader/internal/ARCHITECTURE.md) | [`uploader/internal/IMPLEMENTATION.md`](uploader/internal/IMPLEMENTATION.md) |
| `uploader/internal/auth` | Keychain store, GitHub App device flow, refresh, repository verification | [`uploader/internal/auth/ARCHITECTURE.md`](uploader/internal/auth/ARCHITECTURE.md) | — |
| `uploader/internal/config` | Machine-local config loader and shared limit constants | [`uploader/internal/config/ARCHITECTURE.md`](uploader/internal/config/ARCHITECTURE.md) | — |
| `uploader/internal/github` | Authenticated HTTP client, Contents preflight/create, error classification | [`uploader/internal/github/ARCHITECTURE.md`](uploader/internal/github/ARCHITECTURE.md) | — |
| `uploader/internal/host` | Native Messaging framing, origin validation, server lifecycle | [`uploader/internal/host/ARCHITECTURE.md`](uploader/internal/host/ARCHITECTURE.md) | [`uploader/internal/host/IMPLEMENTATION.md`](uploader/internal/host/IMPLEMENTATION.md) |
| `uploader/internal/logging` | Sanitized, rotated JSON-lines logging | [`uploader/internal/logging/ARCHITECTURE.md`](uploader/internal/logging/ARCHITECTURE.md) | — |
| `uploader/internal/markdown` | Deterministic plain/timestamped document rendering and source-URL publish policy | [`uploader/internal/markdown/ARCHITECTURE.md`](uploader/internal/markdown/ARCHITECTURE.md) | — |
| `uploader/internal/processor` | Serial drain, request handling, status snapshots, transition rules | [`uploader/internal/processor/ARCHITECTURE.md`](uploader/internal/processor/ARCHITECTURE.md) | — |
| `uploader/internal/protocol` | Go wire types and strict validators | [`uploader/internal/protocol/ARCHITECTURE.md`](uploader/internal/protocol/ARCHITECTURE.md) | [`uploader/internal/protocol/IMPLEMENTATION.md`](uploader/internal/protocol/IMPLEMENTATION.md) |
| `uploader/internal/queue` | SQLite store, deduplication, leases, pagination, pruning, lock | [`uploader/internal/queue/ARCHITECTURE.md`](uploader/internal/queue/ARCHITECTURE.md) | — |
| `uploader/internal/retry` | Capped jittered backoff schedule | [`uploader/internal/retry/ARCHITECTURE.md`](uploader/internal/retry/ARCHITECTURE.md) | — |

## Testing and verification

Three offline layers; none requires GitHub, Leccap, the network, or the real
Keychain:

```sh
npm ci
npm run typecheck
npm test          # Vitest over extension-tests/**/*.test.ts
npm run build     # writes dist/extension/
cd uploader && go test ./...
sh scripts/tests/run.sh
sh -n scripts/build-uploader.sh scripts/install-native-host.sh scripts/uninstall-native-host.sh scripts/tests/run.sh
```

- Browser-side suites cover the parser against the Stage 0 fixtures,
  normalization and hash vectors, job identity and canonical serialization,
  content activation/stability/timeout, Native Messaging envelopes, protocol
  vectors, and a build smoke test.
- Go suites cover all ten `uploader/internal` packages plus schema/Go parity,
  the canonical cross-language job fixture, framing, origin validation, queue
  durability, Keychain/device-flow fakes, Contents classification, rendering,
  retry, and logging redaction.
- `scripts/tests/run.sh` is the packaging self-test: temporary `HOME`, stubbed
  `security`, and a fake uploader binary.

Verified while writing this document on this branch: `sh -n` on every shell
script is clean, `go test ./...` passes in all ten internal packages, and
`sh scripts/tests/run.sh` reports `59 passed, 0 failed`. The npm side was not
re-run because `node_modules/` is not installed in this worktree (open
question below).

## Related plan sections

- `TECHNICAL_PLAN.md` § The Technical Plan and § Scope and non-goals
- § Implementation readiness, § Required Stage 0 packet, § Required
  provisioning packet
- § Phase 1 guardrails
- § Transcript job and state model
- § Normative implementation contracts (toolchain, Native Messaging, job
  schema, source-URL vectors, queue DDL, status vocabulary, normalization
  vectors, host template)
- § Detailed Implementation, Stages 0-8
- § Complete implementation file inventory
- § Definition of done

## How to change this directory safely

- This file is documentation. Do not change code, tests, scripts, or
  `TECHNICAL_PLAN.md` from a docs-only change.
- Before changing any behavior, update `TECHNICAL_PLAN.md` (and the decision
  log when the rationale changes), then the code, then this index and the
  affected directory document.
- Keep the directory index current when a directory, package, or
  `ARCHITECTURE.md` is added or removed.
- Keep machine-local values out of every document. Use placeholders such as
  `<loaded-extension-id>`, `<github-app-client-id>`, and
  `<numeric-repository-id>`.
- Re-run the checks in "Testing and verification" after any documentation
  change that touches a command, path, or status name.

### Open questions and known inconsistencies

- The root `IMPLEMENTATION.md`, `extension/IMPLEMENTATION.md`,
  `extension-tests/IMPLEMENTATION.md`, and `protocol/IMPLEMENTATION.md` still
  describe the pre-implementation baseline (missing jsdom, missing build
  script, no cross-language tests). The tree has moved past those statements;
  those files were left unchanged by this docs-only change.
- `docs/TESTING.md` reports a stale baseline of "7 files / 71 tests" and
  "9 uploader packages"; the tree contains 16 Vitest suites and 10 Go internal
  packages. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
- `TECHNICAL_PLAN.md` § Definition of done still says the Go executable
  entrypoint and serial processor are outstanding, while § Implementation
  readiness says they are in the tree.
- The lecture plain path is stated inconsistently: the plan's identity and
  Stage 6 sections, the decision log, `docs/SETUP.md`, and the Go
  `queue/store.go` all use `<courseSlug>/<NNN>.md`, but
  `extension/src/transcript-job.ts` `deriveStableLecturePath` (and its test)
  plus one stale sentence in plan Stage 0 item 10 and
  `docs/TROUBLESHOOTING.md` still say `<courseSlug>/lectures/<NNN>.md`. The
  wire job carries no path, and the uploader derives the published path, so
  the remote result follows `queue/store.go`.
- The plan's canonical queue DDL is migration version 1 without a `kind`
  column; the implemented `uploader/internal/queue/schema.sql` is migration
  version 2 with `kind` and `UNIQUE (kind, lecture_key, content_hash)`, which
  matches the later discussion-capture decision but not the plan's DDL block.
- The plan's Stage 7 build requirement says the build must fail when the
  selector fixture contains template markers; `scripts/build-extension.mjs`
  validates shape, JSON, and non-emptiness but has no marker check (the
  fixture-packet suite checks placeholders).
- `README.md` still refers to "the current executable gap" in its pointer to
  `docs/SETUP.md`; `SETUP.md` states the executable is implemented.
- `PHASE_1_DECISIONS.md` line 268 says the allowlist is only EECS 484 Fall
  2026, while line 266 and the committed `course-mapping.json` include EECS 491
  Fall 2026 as well.
