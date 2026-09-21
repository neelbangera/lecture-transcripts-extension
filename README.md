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

The implementation is being built against the contracts in
[TECHNICAL_PLAN.md](TECHNICAL_PLAN.md). Stage 0 fixtures and measurements are
recorded in [docs/STAGE_0_REPORT.md](docs/STAGE_0_REPORT.md). The live render
time measurement and machine-local GitHub provisioning remain owner-side setup
items; no credential, repository ID, or extension ID belongs in this
repository.

## Development

Prerequisites are Node.js 22 LTS, npm, Go 1.24.x, and (for the supported macOS
uploader build) Xcode Command Line Tools with cgo enabled.

```sh
npm ci
npm run typecheck
npm test
```

The uploader module is under `uploader/`. Its tests are run with Go's standard
tooling once uploader implementation files are present:

```sh
cd uploader
go test ./...
```

The JavaScript build and macOS Native Messaging installation steps are
described in the setup documentation as those files land. Do not place real
Leccap page captures, credentials, queue databases, rendered host manifests,
or build output under version control.

## Design guardrails

- The technical plan is the implementation authority; the historical proposal
  is not.
- The extension has no GitHub token, refresh token, or private key.
- Jobs are delivered at least once and deduplicated by deterministic
  `lectureKey` plus framed `contentHash`.
- A remote lecture file is write-once: the same hash is unchanged, while a
  different or malformed existing file is a conflict and is never overwritten
  automatically.
