# Lecture Transcripts Extension

Personal Chrome extension and macOS uploader for saving an explicitly opened
Leccap transcript as a write-once Markdown lecture file in a configured GitHub
repository.

The Phase 1 scope is deliberately narrow: one Chrome installation, one Mac,
one authenticated Leccap session, one GitHub account, and one destination
repository. Visiting a page does not upload anything; opening the transcript is
the capture action. The extension sends only selected transcript text and
validated lecture metadata to the local uploader. GitHub credentials stay in
the uploader's macOS Keychain, and the uploader owns the durable retry queue.

## Status

Status: `IMPLEMENTED_THROUGH_PACKAGING / LIVE_ITEMS_OUTSTANDING`.

Implemented and tested in this tree:

- the capture-side TypeScript (parser, normalizer, job builder, content runtime,
  outbox, Native Messaging client, popup) with the versioned protocol;
- the Go uploader packages for machine-local config, durable SQLite queue,
  sanitized rotating logs, retry backoff, GitHub App device flow and Keychain
  storage, GitHub Contents write-once publishing, Markdown rendering, and the
  Native Messaging host boundary;
- the extension build, uploader build, host install/uninstall scripts, and
  their packaging self-tests;
- the Stage 0 evidence packet and fixtures described in
  [docs/STAGE_0_REPORT.md](docs/STAGE_0_REPORT.md).

Remaining before personal use:

- `uploader/cmd/lecture-uploader` and `uploader/internal/processor` are not in
  the tree, so `scripts/build-uploader.sh` fails closed with
  `uploader/cmd/lecture-uploader is missing; nothing to build`;
- the per-sample render-time measurement is still a live owner-side item
  (recipe in [docs/STAGE_0_REPORT.md](docs/STAGE_0_REPORT.md));
- the machine-local GitHub provisioning packet (App client ID, numeric
  repository ID, initialized `main` branch, loaded extension ID) is owner-side
  setup and is never committed.

No credential, repository ID, token, or extension ID belongs in this
repository; use `<loaded-extension-id>`-style placeholders in notes and issues.

## Prerequisites

- macOS with the Xcode Command Line Tools installed and selected (`xcode-select
  --install`, then `xcode-select -p`); the Keychain adapter uses cgo.
- Chrome desktop.
- Node.js 22 LTS and npm.
- Go 1.24.x.

## Build and test

From the repository root:

```sh
npm ci
npm run typecheck
npm test
npm run build
```

`npm run build` writes the loadable unpacked extension to `dist/extension/`.

The uploader module is under `uploader/`:

```sh
cd uploader
go test ./...
```

Packaging self-tests (temporary `HOME`, stubbed `security`):

```sh
sh scripts/tests/run.sh
```

Build the macOS uploader and install the Native Messaging host:

```sh
scripts/build-uploader.sh
scripts/install-native-host.sh <loaded-extension-id>
```

See [docs/SETUP.md](docs/SETUP.md) for the complete install runbook, the GitHub
App and machine-local config steps, and the current executable gap.

## Documentation

- [docs/SETUP.md](docs/SETUP.md) — prerequisites, build, unpacked loading,
  GitHub App, machine-local config, host installation, extension-ID drift.
- [docs/SECURITY.md](docs/SECURITY.md) — permissions, Keychain records, queue
  and log modes, redaction, URL sanitization, write-once conflicts, reset.
- [docs/TESTING.md](docs/TESTING.md) — unit suites, fixture packet,
  offline/restart/duplicate/conflict/retry checks, real-machine checks.
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) — host-not-found,
  protocol mismatch, authorization, repository, queue, retry, log, conflict,
  oversized, and extension-ID-drift recovery.
- [TECHNICAL_PLAN.md](TECHNICAL_PLAN.md) — normative behavior, limits, status
  names, and schemas.
- [PHASE_1_DECISIONS.md](PHASE_1_DECISIONS.md) — rationale and rejected
  alternatives.

## Design guardrails

- The technical plan is the implementation authority; the historical proposal
  is not.
- The extension has no GitHub token, refresh token, or private key.
- Jobs are delivered at least once and deduplicated by deterministic
  `lectureKey` plus framed `contentHash`.
- A remote lecture file is write-once: the same hash is unchanged, while a
  different or malformed existing file is a conflict and is never overwritten
  automatically.
- Do not place real Leccap page captures, credentials, queue databases,
  rendered host manifests, or build output under version control.
